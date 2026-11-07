package desktop

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// TestBuildPctClampKeepsResponseReadable checks the layout guard directly: the
// effective share shrinks with the window, but never below the stored
// preference when there is room, and a move is applied as a delta.
func TestBuildPctClampKeepsResponseReadable(t *testing.T) {
	if got := clampBuildPctForHeight(55, 0); got != 55 {
		t.Fatalf("first frame (no measurement) = %v, want 55", got)
	}
	// 541 DIPs of panes: 55% leaves 243 DIPs for the response, above 180.
	if got := clampBuildPctForHeight(55, 542); got != 55 {
		t.Fatalf("tall window = %v, want 55", got)
	}
	// 361 DIPs of panes: 55% would leave 162, so the build pane is compressed
	// to keep 180 for the response.
	got := clampBuildPctForHeight(55, 362)
	want := (361 - minResponseHeight) / 361 * 100
	if math.Abs(float64(got-want)) > 0.01 {
		t.Fatalf("short window = %v, want %v", got, want)
	}
	if (361 - got/100*361) < minResponseHeight-0.01 {
		t.Fatalf("response below minimum: share=%v", got)
	}
	// A preference outside the accepted range is bounded, not used raw.
	if got := clampBuildPctForHeight(5, 542); got != minBuildPct {
		t.Fatalf("low preference = %v, want %v", got, minBuildPct)
	}
	if got := clampBuildPctForHeight(95, 1400); got != maxBuildPct {
		t.Fatalf("high preference = %v, want %v", got, maxBuildPct)
	}

	// A 10-point move applies to the preference, not to the clamped value.
	if got := applyBuildPctMove(55, 50, 60); got != 65 {
		t.Fatalf("move from clamped layout = %v, want 65", got)
	}
	if got := applyBuildPctMove(80, 80, 90); got != maxBuildPct {
		t.Fatalf("move past the maximum = %v, want %v", got, maxBuildPct)
	}
}

// TestBuildPctLegacyConfigValues verifies that a stored ratio from an older
// build wins over the new 55 default, while an invalid one falls back to it.
func TestBuildPctLegacyConfigValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"buildPct":70}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newConfigStore(path).buildPct(); got != 70 {
		t.Fatalf("legacy buildPct = %v, want 70", got)
	}
	if err := os.WriteFile(path, []byte(`{"buildPct":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newConfigStore(path).buildPct(); got != defaultBuildPct {
		t.Fatalf("invalid buildPct = %v, want %v", got, defaultBuildPct)
	}
}

// splitGeometry renders the request view twice (the split measures the frame
// it committed) and returns the split, build and response heights in DIPs.
func splitGeometry(t *testing.T, a *App, ps *projectState, width, height int) (splitH, buildH, respH float32) {
	t.Helper()
	var splitRect, respRect ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		a.View(c)
		splitRect = ps.splitRef.Bounds(c)
		respRect = ps.respRef.Bounds(c)
	}, width, height)
	tt.Frame()
	tt.Frame()
	if splitRect.H <= 0 || respRect.H <= 0 {
		t.Fatalf("split not measured: split=%+v resp=%+v", splitRect, respRect)
	}
	buildH = respRect.Y - splitRect.Y - 1 // minus the 1 DIP divider
	respH = splitRect.H - (buildH + 1)
	return splitRect.H, buildH, respH
}

// TestBuildSplitDefaultGeometry checks the factory 55:45 split and the
// minimum response height at the two acceptance window sizes.
func TestBuildSplitDefaultGeometry(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"1200x820", 1200, 820},
		{"1024x640", 1024, 640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ps := openTestProject(t, "Split "+tc.name)
			tab := openRequestTabs(t, ps, "Alpha")[0]
			tab.Resp = &core.ExecResult{Status: 200, Body: []byte("ok"), BodySize: 2}

			splitH, buildH, respH := splitGeometry(t, a, ps, tc.w, tc.h)
			share := buildH / (splitH - 1) * 100
			if respH < minResponseHeight-1 {
				t.Fatalf("response pane %g DIP below the %g minimum (split=%g build=%g)", respH, float32(minResponseHeight), splitH, buildH)
			}
			if tc.w == 1200 {
				if math.Abs(float64(share-55)) > 2 {
					t.Fatalf("default build share = %g%%, want ~55%% (split=%g build=%g)", share, splitH, buildH)
				}
			} else if share >= 55 {
				t.Fatalf("short window should compress the request pane: share=%g%% (split=%g build=%g)", share, splitH, buildH)
			}
			// A layout clamp must not be written back to the preference.
			if ps.buildPct != defaultBuildPct {
				t.Fatalf("layout clamp changed the preference: %v", ps.buildPct)
			}
			if got := a.mgr.cfg.buildPct(); got != defaultBuildPct {
				t.Fatalf("layout clamp persisted %v, want %v", got, defaultBuildPct)
			}
		})
	}
}

// TestBuildSplitPersistedRatioWins verifies that a ratio the user dragged in an
// earlier session is used as-is when the window has room for it.
func TestBuildSplitPersistedRatioWins(t *testing.T) {
	a, ps := openTestProject(t, "Split Ratio")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.Resp = &core.ExecResult{Status: 200, Body: []byte("ok"), BodySize: 2}
	ps.buildPct = 60
	a.mgr.cfg.setBuildPct(60)

	splitH, buildH, respH := splitGeometry(t, a, ps, 1200, 820)
	share := buildH / (splitH - 1) * 100
	if math.Abs(float64(share-60)) > 2 {
		t.Fatalf("persisted build share = %g%%, want ~60%% (split=%g build=%g)", share, splitH, buildH)
	}
	if respH < minResponseHeight-1 {
		t.Fatalf("response pane %g DIP below the %g minimum", respH, float32(minResponseHeight))
	}
	if ps.buildPct != 60 {
		t.Fatalf("preference changed to %v", ps.buildPct)
	}
}

// TestBuildSplitClampedRatioKeepsPreference verifies that a ratio too large for
// the current window is clamped on screen but kept as the stored preference.
func TestBuildSplitClampedRatioKeepsPreference(t *testing.T) {
	a, ps := openTestProject(t, "Split Clamp")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.Resp = &core.ExecResult{Status: 200, Body: []byte("ok"), BodySize: 2}
	ps.buildPct = 70
	a.mgr.cfg.setBuildPct(70)

	splitH, buildH, respH := splitGeometry(t, a, ps, 1200, 820)
	share := buildH / (splitH - 1) * 100
	if share >= 70 {
		t.Fatalf("70%% should be clamped in this window: share=%g%% (split=%g)", share, splitH)
	}
	if respH < minResponseHeight-1 {
		t.Fatalf("response pane %g DIP below the %g minimum", respH, float32(minResponseHeight))
	}
	if ps.buildPct != 70 {
		t.Fatalf("layout clamp changed the preference to %v", ps.buildPct)
	}
	if got := a.mgr.cfg.buildPct(); got != 70 {
		t.Fatalf("layout clamp persisted %v, want 70", got)
	}
}

// TestBuildSplitDragPersistsPreference drags the request/response divider and
// checks that the move is folded into the preference and saved. The sidebar
// split has its own divider, so the press is placed on the request split's
// committed geometry instead of the first "Divider" label.
func TestBuildSplitDragPersistsPreference(t *testing.T) {
	a, ps := openTestProject(t, "Split Drag")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.Resp = &core.ExecResult{Status: 200, Body: []byte("ok"), BodySize: 2}

	var splitRect, respRect ui.Rect
	tt := ui.NewTester(func(c *ui.Context) {
		a.View(c)
		splitRect = ps.splitRef.Bounds(c)
		respRect = ps.respRef.Bounds(c)
	}, 1200, 820)
	tt.Frame()
	tt.Frame()
	if splitRect.H <= 0 || respRect.H <= 0 {
		t.Fatalf("split not measured: split=%+v resp=%+v", splitRect, respRect)
	}
	x := splitRect.X + splitRect.W/2
	y := respRect.Y - 0.5 // the 1 DIP divider between the panes
	tt.Press(x, y)
	tt.Move(x, y+20)
	tt.Release(x, y+20)
	tt.Frame()

	if ps.buildPct <= defaultBuildPct {
		t.Fatalf("drag down should grow the request pane: buildPct=%v", ps.buildPct)
	}
	if got := a.mgr.cfg.buildPct(); got != ps.buildPct {
		t.Fatalf("dragged ratio not persisted: cfg=%v state=%v", got, ps.buildPct)
	}
}
