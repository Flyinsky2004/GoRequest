package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// kvCheckSide is the side of the KV enable checkbox's visible box. It is a
// dense table control, deliberately neutral rather than a brand button.
const kvCheckSide float32 = 16

// kvCheckbox draws the KV tables' enable toggle: a 16 DIP box with a neutral
// gray stroke when off and the accent fill when on. It is ui.CheckboxBase, so
// clicking, Space, the focus ring and the check-box role stay the framework's;
// only the look is local, and only the KV tables use it.
func kvCheckbox(c *ui.Context, p uikit.Palette, checked *bool) ui.Element {
	e := ui.CheckboxBase(c, checked).FocusRing(false).Shrink(0)
	on := *checked
	hovered := e.Hovered()
	e.Children(func() {
		box := ui.Box(c).Size(kvCheckSide, kvCheckSide).Radius(4).Shrink(0)
		if on {
			box.Background(p.Accent)
		} else {
			// Muted gray keeps the empty box boundary above the 3:1 non-text
			// contrast floor; the hover step darkens it without turning the
			// control accent-colored.
			stroke := p.Muted
			if hovered {
				stroke = p.Text
			}
			box.BorderWidth(1).BorderColor(stroke)
		}
		box.DrawOver(func(painter *ui.Painter, r ui.Rect) {
			if on {
				painter.StrokePath(kvCheckPath(r), 1.5, p.OnAccent)
			}
			if e.FocusVisible() {
				painter.FocusRing(r, [4]float32{4, 4, 4, 4})
			}
		})
	})
	return e
}

// kvCheckPath is the tick drawn inside the checked box.
func kvCheckPath(r ui.Rect) *ui.Path {
	var path ui.Path
	return path.MoveTo(r.X+r.W*0.22, r.Y+r.H*0.52).
		LineTo(r.X+r.W*0.42, r.Y+r.H*0.71).
		LineTo(r.X+r.W*0.78, r.Y+r.H*0.31)
}
