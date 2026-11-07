package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

const (
	// gitCheckSide is the side of the Git file row's visible box, the same
	// dense 16 DIP control the KV tables use.
	gitCheckSide float32 = 16
	// gitCheckPad widens the pointer target around the box without changing
	// its look, keeping the selection target clear of the filename button.
	gitCheckPad float32 = 2
)

// gitCheckbox draws a Git file row's selection box on the framework's
// CheckboxBase: clicking, Space, the focus ring and the check-box role stay
// the framework's, only the look is local. It is a sibling of the filename
// button, so selecting a file never opens its diff and opening the diff never
// changes the selection. The palette keeps it legible in both appearances.
func gitCheckbox(c *ui.Context, p uikit.Palette, checked *bool, disabled bool) ui.Element {
	e := ui.CheckboxBase(c, checked).FocusRing(false).Shrink(0).Padding(gitCheckPad)
	if disabled {
		e.Disabled(true)
	}
	on := *checked
	hovered := e.Hovered()
	e.Children(func() {
		box := ui.Box(c).Size(gitCheckSide, gitCheckSide).Radius(4).Shrink(0)
		switch {
		case disabled:
			// A conflict row cannot be selected: a faint hollow box reads as
			// unavailable in both appearances and never shows a tick.
			box.Background(p.Hover).BorderWidth(1).BorderColor(p.Faint)
		case on:
			box.Background(p.Accent)
		default:
			// Muted gray keeps the empty box above the non-text contrast
			// floor; hover darkens it without turning it accent-colored.
			stroke := p.Muted
			if hovered {
				stroke = p.Text
			}
			box.BorderWidth(1).BorderColor(stroke)
		}
		box.DrawOver(func(painter *ui.Painter, r ui.Rect) {
			if on && !disabled {
				painter.StrokePath(gitCheckPath(r), 1.5, p.OnAccent)
			}
			if e.FocusVisible() {
				painter.FocusRing(r, [4]float32{4, 4, 4, 4})
			}
		})
	})
	return e
}

// gitCheckPath is the tick drawn inside a checked box.
func gitCheckPath(r ui.Rect) *ui.Path {
	var path ui.Path
	return path.MoveTo(r.X+r.W*0.22, r.Y+r.H*0.52).
		LineTo(r.X+r.W*0.42, r.Y+r.H*0.71).
		LineTo(r.X+r.W*0.78, r.Y+r.H*0.31)
}
