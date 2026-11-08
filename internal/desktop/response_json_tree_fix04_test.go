package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/core"
)

// fix04TreeFixture renders the production response panel in JSON Tree mode over
// a small object holding one container (a) and scalar rows at known depths, so
// layout and interaction can be asserted headlessly (no window).
func fix04TreeFixture(t *testing.T) (*ui.Tester, *App) {
	t.Helper()
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "FIX04")); err != nil {
		t.Fatal(err)
	}
	m.cfg.cfg.Language = string(langEnglish)
	a := m.workspaces[0]
	body := []byte(`{"a":{"b":1},"c":2}`)
	tab := &Tab{Owner: a.ps, Resp: &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Body:     body,
		Pretty:   "{\n  \"a\": {\n    \"b\": 1\n  },\n  \"c\": 2\n}",
		BodySize: int64(len(body)),
	}}
	tab.RespRawIdx = 2 // Tree mode
	tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, 640, 600)
	return tt, a
}

// TestJSONTreeToggleCompactAndAligned verifies the collapse chevron sits in a
// compact square that tracks the row's text height, and that scalar rows reserve
// the same width so container and non-container keys line up in one column.
// This is the regression guard for FIX04 (oversized toggle button, undersized
// glyph): with the old padded ui.Button the toggle box was ~37px wide, so the
// container key sat ~21px further right than a scalar key at the same depth, and
// a click 28px left of the key still landed on the button.
func TestJSONTreeToggleCompactAndAligned(t *testing.T) {
	tt, _ := fix04TreeFixture(t)

	glyph, ok := tt.Find(jsonTreeToggleExpandedLabel)
	if !ok {
		t.Fatalf("collapse chevron missing; texts=%v", tt.Texts())
	}
	// U30: the SVG chevron (16px) must fit its hit target with a small margin
	// and be at least 18px for clear clicking, yet stay a compact square
	// (<=24px) rather than a large padded control box.
	if glyph.H != glyph.W || glyph.H < 18 || glyph.H > 24 || glyph.H < jsonTreeToggleGlyph {
		t.Fatalf("chevron box not compact: %+v", glyph)
	}

	// Same depth (1): container "a:" and scalar "c:" must share a left edge.
	// The old 22px placeholder drifted from the padded button; the shared
	// jsonTreeToggleSize keeps them equal.
	aKey, ok := tt.Find("a:")
	if !ok {
		t.Fatalf("container key missing; texts=%v", tt.Texts())
	}
	cKey, ok := tt.Find("c:")
	if !ok {
		t.Fatalf("scalar key missing; texts=%v", tt.Texts())
	}
	if aKey.X != cKey.X {
		t.Fatalf("container/scalar keys misaligned: a:=%+v c:=%+v", aKey, cKey)
	}

	// Deeper row "b:" must be indented by a whole depth unit to the right.
	bKey, ok := tt.Find("b:")
	if !ok {
		t.Fatalf("nested key missing; texts=%v", tt.Texts())
	}
	if bKey.X <= cKey.X {
		t.Fatalf("nested key not indented: b:=%+v c:=%+v", bKey, cKey)
	}

	// Toggle is vertically centered on the key text of its row.
	// The first toggle belongs to the root row, whose only text is its "$" path.
	rootPath, ok := tt.Find("$")
	if !ok {
		t.Fatalf("root path missing; texts=%v", tt.Texts())
	}
	if dy := (glyph.Y + glyph.H/2) - (rootPath.Y + rootPath.H/2); dy > 3 || dy < -3 {
		t.Fatalf("toggle not vertically centered with row text: toggle=%+v text=%+v", glyph, rootPath)
	}

	// Hit target stays compact: a point 28px left of the key sits in the row's gutter.
	// The old padded button reached there and would collapse "a"; the compact
	// toggle does not, so the click must be a no-op.
	yy := aKey.Y + aKey.H/2
	tt.ClickAt(aKey.X-28, yy)
	tt.Frame()
	if _, gone := tt.Find("b:"); !gone {
		t.Fatalf("click in gutter collapsed the row; toggle hit area too large")
	}
	t.Logf("chevron=%+v aKey=%+v bKey=%+v cKey=%+v", glyph, aKey, bKey, cKey)
}

// TestJSONTreeToggleCollapseExpandAndCopy verifies the compact control still
// collapses/expands on click and that Copy Path / Copy Value keep working.
func TestJSONTreeToggleCollapseExpandAndCopy(t *testing.T) {
	tt, a := fix04TreeFixture(t)
	nm := a.networkMsg()

	// Collapse the root container via its chevron: children disappear.
	glyph, ok := tt.Find(jsonTreeToggleExpandedLabel)
	if !ok {
		t.Fatalf("collapse chevron missing; texts=%v", tt.Texts())
	}
	tt.ClickAt(glyph.X+glyph.W/2, glyph.Y+glyph.H/2)
	tt.Frame()
	if _, still := tt.Find("a:"); still {
		t.Fatalf("clicking ▾ did not collapse children; texts=%v", tt.Texts())
	}
	// Expand again via the now-pointing ▸.
	expanded, ok := tt.Find(jsonTreeToggleCollapsedLabel)
	if !ok {
		t.Fatalf("expand chevron missing after collapse; texts=%v", tt.Texts())
	}
	tt.ClickAt(expanded.X+expanded.W/2, expanded.Y+expanded.H/2)
	tt.Frame()
	if _, back := tt.Find("a:"); !back {
		t.Fatalf("clicking ▸ did not expand children; texts=%v", tt.Texts())
	}

	// Copy Path: the first row is the root, whose JSONPath is "$".
	if err := tt.Click(nm.CopyJSONPathBtn); err != nil {
		t.Fatalf("copy path: %v", err)
	}
	if got := tt.Clipboard(); !strings.Contains(got, "$") {
		t.Fatalf("copy path clipboard=%q", got)
	}
	// Copy Value: the root's raw value is the whole document.
	if err := tt.Click(nm.CopyValueBtn); err != nil {
		t.Fatalf("copy value: %v", err)
	}
	if got := tt.Clipboard(); !strings.Contains(got, `"a"`) {
		t.Fatalf("copy value clipboard=%q", got)
	}
}
