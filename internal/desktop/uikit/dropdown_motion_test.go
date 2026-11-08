package uikit

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// inkBelow counts the dark pixels the popup paints below top. The harness
// leaves that part of the window empty, so the count is the popup's own ink:
// its option text and icons, not the trigger's.
func inkBelow(tt *ui.Tester, top float32) int {
	img := tt.Image()
	return darkPixels(img, 0, top, float32(img.Bounds().Dx()), float32(img.Bounds().Dy()))
}

// TestSelectPopupEntersWithMotion checks that the popup is not simply
// snapped on: on the opening frame it is still transparent, and it becomes
// visible as the shared drop-down spring settles.
func TestSelectPopupEntersWithMotion(t *testing.T) {
	_, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	tr, ok := tt.Find("select-trigger")
	if !ok {
		t.Fatal("trigger not found")
	}
	if got := inkBelow(tt, tr.Y+tr.H+2); got != 0 {
		t.Fatalf("the window below the trigger had ink before opening: %d", got)
	}
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	if got := inkBelow(tt, tr.Y+tr.H+2); got != 0 {
		t.Fatalf("the popup was fully drawn on its opening frame; ink=%d", got)
	}
	tt.Advance(400 * time.Millisecond)
	if got := inkBelow(tt, tr.Y+tr.H+2); got == 0 {
		t.Fatal("the popup never became visible after the enter spring settled")
	}
}

// TestSelectPopupLeavesWithMotion checks that closing plays the exit
// transition instead of dropping the popup in one frame: the leaving copy is
// still painted right after Escape, and gone once the spring settles.
func TestSelectPopupLeavesWithMotion(t *testing.T) {
	_, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	tr, ok := tt.Find("select-trigger")
	if !ok {
		t.Fatal("trigger not found")
	}
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Advance(400 * time.Millisecond)
	if got := inkBelow(tt, tr.Y+tr.H+2); got == 0 {
		t.Fatal("the popup did not open")
	}
	tt.Key(0, ui.KeyEscape)
	if got := inkBelow(tt, tr.Y+tr.H+2); got == 0 {
		t.Fatal("the popup vanished on the closing frame; the exit transition did not run")
	}
	tt.Advance(400 * time.Millisecond)
	if got := inkBelow(tt, tr.Y+tr.H+2); got != 0 {
		t.Fatalf("the leaving popup was still painted after the exit spring settled: ink=%d", got)
	}
}

// TestSelectPopupRespectsReduceMotion checks the popup fades in place rather
// than scaling or sliding when the desktop asks for less motion.
func TestSelectPopupRespectsReduceMotion(t *testing.T) {
	_, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	atOpen, ok := tt.Find("Beta")
	if !ok {
		t.Fatal("popup option not found")
	}
	tt.Advance(400 * time.Millisecond)
	settled, ok := tt.Find("Beta")
	if !ok {
		t.Fatal("popup option not found after settling")
	}
	if atOpen != settled {
		t.Fatalf("the popup moved under ReduceMotion: opening %+v, settled %+v", atOpen, settled)
	}
}

// TestSelectClickOutsideClosesWithMotion checks the press outside still
// dismisses the popup, plays its exit, and chooses nothing.
func TestSelectClickOutsideClosesWithMotion(t *testing.T) {
	h, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	tr, ok := tt.Find("select-trigger")
	if !ok {
		t.Fatal("trigger not found")
	}
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Advance(400 * time.Millisecond)
	tt.ClickAt(5, 5) // outside the trigger and the popup
	if tt.HasText("Gamma") {
		t.Fatal("a press outside did not close the popup")
	}
	if h.choice != "Alpha" {
		t.Fatalf("a press outside chose %q", h.choice)
	}
	if got := inkBelow(tt, tr.Y+tr.H+2); got == 0 {
		t.Fatal("the popup vanished on the closing frame; the exit transition did not run")
	}
	tt.Advance(400 * time.Millisecond)
	if got := inkBelow(tt, tr.Y+tr.H+2); got != 0 {
		t.Fatalf("the leaving popup was still painted after the exit spring settled: ink=%d", got)
	}
}

// TestSelectPopupRapidReopen checks that closing and reopening before the
// exit finishes leaves a usable popup and settles without a panic.
func TestSelectPopupRapidReopen(t *testing.T) {
	h, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	tr, ok := tt.Find("select-trigger")
	if !ok {
		t.Fatal("trigger not found")
	}
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Advance(400 * time.Millisecond)
	tt.Key(0, ui.KeyEscape) // start the exit
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("reopen trigger: %v", err)
	}
	if !tt.HasText("Gamma") {
		t.Fatal("the popup did not reopen")
	}
	tt.Advance(400 * time.Millisecond)
	if got := inkBelow(tt, tr.Y+tr.H+2); got == 0 {
		t.Fatal("the reopened popup is not visible")
	}
	if err := tt.Click("Beta"); err != nil {
		t.Fatalf("choose after reopen: %v", err)
	}
	if h.choice != "Beta" {
		t.Fatalf("choice = %q, want Beta", h.choice)
	}
}

// TestSelectPopupStopsWhenSettled checks that the popup and the chevron stop
// asking for frames once their springs rest: no ticker and no idle repaint.
func TestSelectPopupStopsWhenSettled(t *testing.T) {
	_, tt := newSelectHarness(t, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
	if err := tt.Click("select-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("the open popup still asks for frames after it settled")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(time.Second)
	if tt.FramePending() {
		t.Fatal("the closed popup still asks for frames after it settled")
	}
}

// TestSelectChevronTurns checks the trigger chevron animates from 0 to the
// open angle and back, using the tester's controllable clock.
func TestSelectChevronTurns(t *testing.T) {
	open := false
	var angle float32
	tt := ui.NewTester(func(c *ui.Context) {
		el, got := dropdownChevron(c, open, ui.Hex("#000000"))
		_ = el
		angle = got
	}, 120, 80)
	if angle != 0 {
		t.Fatalf("closed chevron angle = %v, want 0", angle)
	}
	open = true
	tt.Frame()
	if angle != 0 {
		t.Fatalf("chevron jumped on the opening frame: %v", angle)
	}
	tt.Advance(400 * time.Millisecond)
	if diff := angle - DropdownChevronTurn; diff > 1 || diff < -1 {
		t.Fatalf("open chevron angle = %v, want %v (±1 for the spring overshoot)", angle, DropdownChevronTurn)
	}
	open = false
	tt.Advance(400 * time.Millisecond)
	if angle > 1 || angle < -1 {
		t.Fatalf("closed chevron angle = %v, want 0 (±1 for the spring overshoot)", angle)
	}
}

// TestSelectPopupFlipsAtTheWindowEdge checks the animated popup still uses
// MyGo's placement: with no room below, it opens above the trigger.
func TestSelectPopupFlipsAtTheWindowEdge(t *testing.T) {
	choice := "Alpha"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Height(140).Justify(ui.End).Padding(10).Children(func() {
			sel := Select(c, &choice, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
			sel.Label("edge-trigger").Width(180)
		})
	}, 360, 160)
	tr, ok := tt.Find("edge-trigger")
	if !ok {
		t.Fatal("trigger not found")
	}
	if err := tt.Click("edge-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Advance(400 * time.Millisecond)
	option, ok := tt.Find("Gamma")
	if !ok {
		t.Fatal("popup option not found")
	}
	if option.Y >= tr.Y {
		t.Fatalf("popup did not flip above the trigger: option y=%v trigger y=%v", option.Y, tr.Y)
	}
}

// TestSelectDisabledDoesNotOpen checks a disabled trigger keeps its keyboard
// and pointer semantics while the new motion is in place.
func TestSelectDisabledDoesNotOpen(t *testing.T) {
	choice := "Alpha"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			sel := Select(c, &choice, []string{"Alpha", "Beta", "Gamma"}, SameIcon(IconList))
			sel.Label("disabled-trigger").Width(180).Disabled(true)
		})
	}, 360, 320)
	if err := tt.Click("disabled-trigger"); err != nil {
		t.Fatalf("click trigger: %v", err)
	}
	tt.Advance(400 * time.Millisecond)
	if tt.HasText("Gamma") {
		t.Fatal("a disabled select opened its popup")
	}
}
