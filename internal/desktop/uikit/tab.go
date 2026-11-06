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
// hovered — the spec's "clean tab shows × on hover". A dirty tab shows its amber
// dot instead while not hovered, and the dot turns into the × as the pointer
// enters, exactly as §4 describes.
func TabItem(c *ui.Context, p Palette, key any, s TabSpec, closeLabel string) (ui.Element, bool, bool) {
	outer := ui.Row(c).Key(key).Height(40).Radius(8).Padding(0, 6).Gap(4).
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
		act := ui.ButtonBase(c).Grow(1).Height(38).Gap(6).PaddingX(2)
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

		// Fixed-width status slot. The close target always exists (stable node, so
		// a click is never lost to a rebuild); the amber unsaved dot sits over it
		// while a dirty tab is not hovered, and the × shows on hover. The dot is
		// pointer-transparent, so the close button underneath still gets the click.
		slot := ui.Box(c).Width(16).Height(16).Center()
		slot.Children(func() {
			dot := ui.Box(c).Size(7, 7).Radius(999).Background(MethodColor("POST", p)).PassThrough()
			dot.Opacity(tern(s.Dirty && !hovered, 1, 0))
			cb := ui.Button(c, "✕").Label(closeLabel)
			cb.Opacity(tern(hovered, 1, 0))
			if cb.Clicked() {
				closeClicked = true
			}
		})
		if s.Active {
			ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(2).Background(p.Accent)
		}
	})

	if closeClicked {
		clicked = false
	}
	return outer, clicked, closeClicked
}
