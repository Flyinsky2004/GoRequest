package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// Drop-down motion is deliberately kept out of motion.go: this task owns its
// own file so the shared beat sheet stays untouched. The values follow the
// same rules as the rest of the design system — one spring for every
// controllable drop-down, a short overshoot on open, a smaller exit motion
// that reads quicker, and nothing that keeps ticking once the popup is at
// rest.

var (
	// SpringDropdown drives the popup surface. 260ms with a small bounce
	// reads as a menu arriving from its trigger without feeling playful.
	SpringDropdown = ui.Animation{Spring: ui.SpringOptions{Duration: 260 * time.Millisecond, Bounce: 0.16}}

	// SpringDropdownChevron turns the trigger's chevron. It is a little
	// faster than the surface so the affordance leads the menu.
	SpringDropdownChevron = ui.Animation{Spring: ui.SpringOptions{Duration: 220 * time.Millisecond, Bounce: 0.12}}
)

// Drop-down geometry, in DIPs and degrees.
const (
	// DropdownEnterScale and DropdownExitScale keep the popup close to its
	// final size: a menu that grows from 0.96 reads as attached to the
	// trigger, while a deeper scale reads as a dialog.
	DropdownEnterScale float32 = 0.96
	DropdownExitScale  float32 = 0.98

	// DropdownEnterLift and DropdownExitLift move the surface toward the
	// trigger as it appears and as it goes.
	DropdownEnterLift float32 = -4
	DropdownExitLift  float32 = -3

	// DropdownChevronTurn is the chevron's open angle.
	DropdownChevronTurn float32 = 180
)

// DropdownTransition is the one-shot enter/exit transition shared by every
// controllable drop-down popup. MyGo keeps a copy of a leaving element while
// its parent is still built, which is why Select keeps the shared overlay
// layer alive: the popup's parent is that layer, and without it the leaving
// copy would be dropped in the same frame the popup closes.
//
// Under ReduceMotion MyGo fades these in place (the motions have no
// Collapse), so the popup never moves for people who ask for less motion.
var DropdownTransition = ui.ElementTransition{
	Animation: &SpringDropdown,
	Enter:     &ui.Motion{Y: DropdownEnterLift, Opacity: 0, Scale: DropdownEnterScale},
	Exit:      &ui.Motion{Y: DropdownExitLift, Opacity: 0, Scale: DropdownExitScale},
}

// dropdownChevron draws the trigger chevron and turns it to point up while
// the popup is open. The angle is a normal animated value, so it settles and
// stops requesting frames; ReduceMotion jumps it to its target instead. The
// second result is the angle the element shows, which the tests check instead
// of sampling rotated pixels.
func dropdownChevron(c *ui.Context, open bool, color ui.Color) (ui.Element, float32) {
	target := float32(0)
	if open {
		target = DropdownChevronTurn
	}
	ic := ui.Icon(c, IconChevron).FontSize(IconSizeDefault).TextColor(color)
	angle := ic.AnimateTo("dropdown-chevron", target, SpringDropdownChevron)
	return ic.Rotate(angle).TransformOrigin(0.5, 0.5), angle
}
