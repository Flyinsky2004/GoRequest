package uikit

import (
	"image"
	"testing"

	"github.com/egoist/mygo/ui"
)

// selectHarness builds a themed select over options with icons and records the
// choice and whether Changed fired.
type selectHarness struct {
	choice  string
	changed bool
}

func newSelectHarness(t *testing.T, options []string, iconFor SelectIconFunc) (*selectHarness, *ui.Tester) {
	t.Helper()
	h := &selectHarness{choice: options[0]}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Gap(10).Children(func() {
			sel := Select(c, &h.choice, options, iconFor)
			sel.Label("select-trigger").Width(180)
			if sel.Changed() {
				h.changed = true
			}
		})
	}, 360, 320)
	return h, tt
}

// TestThemedSelectOpensInWindowPopup checks that Select draws its options in
// the window rather than in a platform menu, and that choosing one updates the
// pointer and reports Changed.
func TestThemedSelectOpensInWindowPopup(t *testing.T) {
	h, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, IconMap(map[string]*ui.SVG{
		"Alpha": IconSun,
		"Beta":  IconMoon,
		"Gamma": IconGlobe,
	}))
	if tt.HasText("Beta") {
		t.Fatal("the popup shows before the trigger is clicked")
	}
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	if !tt.HasText("Beta") || !tt.HasText("Gamma") {
		t.Fatalf("the popup did not draw its options in the window; texts=%v", tt.Texts())
	}
	if menu := tt.Menu(); len(menu) != 0 {
		t.Fatalf("the themed select opened a platform menu: %v", menu)
	}
	if err := tt.Click("Beta"); err != nil {
		t.Fatalf("click option: %v", err)
	}
	if h.choice != "Beta" {
		t.Fatalf("choice = %q, want Beta", h.choice)
	}
	if !h.changed {
		t.Fatal("Changed did not fire after choosing an option")
	}
	if tt.HasText("Gamma") {
		t.Fatal("the popup stayed open after a choice")
	}
}

// TestThemedSelectKeyboard checks arrows, Enter, Space and Escape still drive
// the themed select.
func TestThemedSelectKeyboard(t *testing.T) {
	h, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Key(0, ui.KeyDown) // Alpha -> Beta
	tt.Key(0, ui.KeyEnter)
	if h.choice != "Beta" || tt.HasText("Gamma") {
		t.Fatalf("Enter chose %q and left the popup open: %v", h.choice, tt.HasText("Gamma"))
	}

	// Space chooses the highlighted option too.
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("reopen trigger: %v", err)
	}
	tt.Key(0, ui.KeyDown) // Beta -> Gamma
	tt.Key(0, ui.KeySpace)
	if h.choice != "Gamma" {
		t.Fatalf("Space chose %q, want Gamma", h.choice)
	}

	// Escape closes without choosing. The trigger now shows Gamma, so Alpha
	// only appears while the popup is open.
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("reopen trigger: %v", err)
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("Alpha") {
		t.Fatal("Escape did not close the popup")
	}
}

// TestThemedSelectRendersIcons checks that each option draws its icon left of
// the label and that the trigger draws its chevron. Icons are decorative (no
// label), so this samples the rendered pixels instead of the label tree.
func TestThemedSelectRendersIcons(t *testing.T) {
	_, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, IconMap(map[string]*ui.SVG{
		"Alpha": IconSun,
		"Beta":  IconMoon,
		"Gamma": IconGlobe,
	}))
	img := tt.Image()

	// The trigger's chevron sits at its right edge.
	tr, ok := tt.Find("select-trigger")
	if !ok {
		t.Fatal("trigger not found")
	}
	chevron := darkPixels(img, tr.X+tr.W-24, tr.Y+tr.H/2-8, tr.X+tr.W-8, tr.Y+tr.H/2+8)
	if chevron == 0 {
		t.Fatalf("the trigger drew no chevron; box=%+v", tr)
	}

	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	img = tt.Image()
	// An option's icon is 16 wide, 8 left of its label (gap 8). Alpha is
	// skipped because the trigger also shows it, and Find returns the first
	// match (the trigger's label).
	for _, option := range []string{"Beta", "Gamma"} {
		r, ok := tt.Find(option)
		if !ok {
			t.Fatalf("option %q not found", option)
		}
		got := darkPixels(img, r.X-24, r.Y+2, r.X-8, r.Y+r.H-2)
		if got == 0 {
			t.Fatalf("option %q drew no icon; text box=%+v", option, r)
		}
	}
}

// darkPixels counts pixels darker than the light palette's surfaces in the
// half-open box [x0,x1)×[y0,y1).
func darkPixels(img *image.RGBA, x0, y0, x1, y1 float32) int {
	b := img.Bounds()
	n := 0
	for y := int(y0); y < int(y1); y++ {
		for x := int(x0); x < int(x1); x++ {
			if x < b.Min.X || y < b.Min.Y || x >= b.Max.X || y >= b.Max.Y {
				continue
			}
			r, g, bl, _ := img.At(x, y).RGBA()
			if r>>8 < 200 && g>>8 < 200 && bl>>8 < 200 {
				n++
			}
		}
	}
	return n
}
