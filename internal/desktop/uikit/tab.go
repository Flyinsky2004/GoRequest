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

// TabItem renders one tab of the request strip (§3): a method-colored monospace
// tag, a truncated title, an unsaved amber dot, a close ×, and a 2px accent
// underline on the active tab. The activation area and the close × are two
// sibling controls, so a click lands on exactly one of them; the close × carries
// a stable accessibility label so the strip stays operable by assistive
// technology and the headless tester.
func TabItem(c *ui.Context, p Palette, key any, s TabSpec, closeLabel string) (ui.Element, bool, bool) {
	bg := p.Sidebar.Mix(p.Text, 0.05) // unselected: a notch darker than the rail
	if s.Active {
		bg = p.BG
	}
	label := s.Title
	if s.Method != "" {
		label = s.Method + " " + s.Title
	}
	if s.Dirty {
		label += " •"
	}
	titleColor := p.Muted
	if s.Active {
		titleColor = p.Text
	}

	outer := ui.Row(c).Key(key).Height(40).Radius(8).Padding(0, 6).Gap(4).
		AlignItems(ui.Center).Background(bg).MaxWidth(190).Clip()

	var clicked, closeClicked bool
	outer.Children(func() {
		act := ui.ButtonBase(c).Grow(1).Height(38).Gap(6).PaddingX(2)
		act.Children(func() {
			if s.Method != "" {
				ui.Text(c, s.Method).Font("monospace").FontWeight(700).FontSize(11).
					TextColor(MethodColor(s.Method, p)).Width(40).SingleLine()
			}
			ui.Text(c, s.Title).FontSize(13).TextColor(titleColor).Grow(1).SingleLine()
			if s.Dirty {
				ui.Text(c, "•").FontSize(12).FontWeight(700).TextColor(MethodColor("POST", p))
			}
		})
		act.Label(label)
		if act.Clicked() {
			clicked = true
		}
		if ui.Button(c, "✕").Label(closeLabel).Clicked() {
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
