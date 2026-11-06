package uikit

import "github.com/egoist/mygo/ui"

// UnderlineTabs renders a row of underline tabs (§5): 13px labels, the selected
// one in the text color at 600 with a 2px accent underline that slides between
// tabs with the shared SpringSlide spring, widths following each label. badges,
// when non-empty at an index, draw a small faint count after the label (for
// "Headers (3)"). A hairline runs along the whole row's bottom edge. It reports
// whether the selection changed this frame.
//
// The slide is measured from the previous frame's tab rectangles, so the
// indicator moves to the newly chosen tab and animates from where it was.
func UnderlineTabs(c *ui.Context, p Palette, selected *int, labels []string, badges []string) (ui.Element, bool) {
	n := len(labels)
	if *selected < 0 {
		*selected = 0
	}
	if *selected >= n {
		*selected = n - 1
	}

	row := ui.Row(c).Height(40).AlignItems(ui.Center).Gap(2).
		BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline)
	rowBounds := row.Bounds() // previous frame

	els := make([]ui.Element, n)
	var changed bool
	row.Children(func() {
		for i, label := range labels {
			i := i
			sel := i == *selected
			b := ActionCursor(ui.ButtonBase(c).Height(40).Padding(0, 10).Gap(4).AlignItems(ui.Center))
			b.Children(func() {
				col, weight := p.Muted, 500
				if sel {
					col, weight = p.Text, 600
				} else if b.Hovered() {
					col = p.Text
				}
				ui.Text(c, label).FontSize(13).FontWeight(weight).TextColor(col).SingleLine()
				if i < len(badges) && badges[i] != "" {
					ui.Text(c, badges[i]).FontSize(11).TextColor(p.Faint).SingleLine()
				}
			})
			b.Label(label)
			els[i] = b
			if b.Clicked() && i != *selected {
				*selected = i
				changed = true
			}
		}

		// Sliding indicator, positioned from the previous frame's chosen tab.
		var tx, tw float32
		if *selected >= 0 && *selected < n {
			if r := els[*selected].Bounds(); r.W > 0 {
				tx, tw = r.X-rowBounds.X, r.W
			}
		}
		ix := row.AnimateTo("underline-x", tx, SpringSlide)
		iw := row.AnimateTo("underline-w", tw, SpringSlide)
		ui.Box(c).Absolute().Bottom(0).Left(ix).Width(iw).Height(2).Background(p.Accent)
	})

	return row, changed
}
