package uikit

import "github.com/egoist/mygo/ui"

// TabSpec describes one request/document tab for TabItem.
type TabSpec struct {
	// Method is the HTTP method shown as a small colored monospace tag; empty
	// for a document tab.
	Method string
	Title  string
	Active bool
	Dirty  bool
}

// TabItem renders one tab of the request strip (§4): a method-colored monospace
// tag, a truncated title, an unsaved amber dot and a 2px accent underline on the
// active tab. Unselected tabs are borderless and transparent, gaining only a
// hover wash, so the strip reads as rows rather than buttons.
//
// The close × is always in the tree (so assistive technology and the headless
// tester can reach its stable label) but is drawn transparent unless the tab is
// hovered or its close button has keyboard focus. A dirty tab shows its amber
// dot instead while not hovered, and the dot turns into the × as the pointer
// enters, exactly as §4 describes.
func TabItem(c *ui.Context, p Palette, key any, s TabSpec, closeLabel string) (ui.Element, bool, bool) {
	outer := ui.Row(c).Key(key).Height(TabHeight).Radius(8).Padding(0, 6).Gap(4).
		AlignItems(ui.Center).MaxWidth(180).Clip()
	hovered := outer.Hovered()
	switch {
	case s.Active:
		outer.Background(p.BG)
	case hovered:
		outer.Background(p.Hover)
	default:
		outer.Background(ui.Transparent)
	}
	titleColor := p.Muted
	if s.Active {
		titleColor = p.Text
	}

	label := s.Title
	if s.Method != "" {
		label = s.Method + " " + s.Title
	}
	if s.Dirty {
		label += " •"
	}

	var clicked, closeClicked bool
	outer.Children(func() {
		act := ui.ButtonBase(c).Grow(1).Height(TabHeight - 2).Gap(6).PaddingX(2)
		if !act.IsDisabled() {
			act.Cursor(ui.CursorPointer)
		}
		act.Children(func() {
			if s.Method != "" {
				ui.Text(c, s.Method).Font("monospace").FontWeight(700).FontSize(11).
					TextColor(MethodColor(s.Method, p)).Width(38).SingleLine()
			}
			ui.Text(c, s.Title).FontSize(13).TextColor(titleColor).Grow(1).SingleLine()
		})
		act.Label(label)
		if act.Clicked() {
			clicked = true
		}

		if TabClose(c, p, closeLabel, s.Dirty, hovered).Clicked() {
			closeClicked = true
		}
		if s.Active {
			ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(2).Background(p.Accent)
		}
	})

	if closeClicked {
		clicked = false
	}
	return outer, clicked, closeClicked
}

// TabHeight leaves a 24 DIP close target inside the compact strip.
const TabHeight float32 = 26

// TabClose keeps the status dot and vector cross on the same geometric center.
// ButtonBase retains native keyboard, focus and accessibility behavior without
// the default Button padding overflowing the old 16 DIP status slot.
func TabClose(c *ui.Context, p Palette, label string, dirty, hovered bool) ui.Element {
	b := ui.ButtonBase(c).Size(24, 24).Radius(4).Label(label)
	if !b.IsDisabled() {
		b.Cursor(ui.CursorPointer)
	}
	visible := hovered || b.Focused()
	if b.Hovered() || b.Pressed() {
		b.Background(p.Hover)
	}
	b.Draw(func(painter *ui.Painter, r ui.Rect) {
		x, y := r.X+r.W/2, r.Y+r.H/2
		if visible {
			var cross ui.Path
			cross.MoveTo(x-3, y-3).LineTo(x+3, y+3)
			cross.MoveTo(x+3, y-3).LineTo(x-3, y+3)
			painter.StrokePath(&cross, 1.5, p.Muted)
		} else if dirty {
			painter.Fill(ui.Rect{X: x - 3.5, Y: y - 3.5, W: 7, H: 7}, MethodColor("POST", p), 3.5)
		}
	})
	return b
}
