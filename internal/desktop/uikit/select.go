package uikit

import "github.com/egoist/mygo/ui"

// SelectIconFunc picks the icon shown beside an option. Returning nil asks
// Select to draw its neutral chevron instead, so an option never renders a
// blank icon slot.
type SelectIconFunc func(option string) *ui.SVG

// IconMap returns a SelectIconFunc backed by a label→icon map. Labels missing
// from the map get the neutral icon.
func IconMap(icons map[string]*ui.SVG) SelectIconFunc {
	return func(option string) *ui.SVG { return icons[option] }
}

// SameIcon returns a SelectIconFunc that gives every option ic.
func SameIcon(ic *ui.SVG) SelectIconFunc {
	return func(string) *ui.SVG { return ic }
}

// Select is the themed drop-down built on ui.SelectBase: it draws its popup in
// the window (never a platform menu) so the surface, border, radius, shadow and
// option rows follow the palette. The trigger shows the current label and a
// chevron; each option shows its icon, label and an accent check when chosen.
//
// It returns the trigger element, so callers keep the usual chain — Width,
// FillWidth, Grow, Label, Cursor, Disabled and Changed — exactly as with
// ui.Select. Keyboard navigation (arrows, Home/End, Enter/Space, Escape) is
// handled by SelectBase.
func Select(c *ui.Context, selected *string, options []string, iconFor SelectIconFunc) ui.Element {
	p := PaletteFor(c.Theme())
	sel := ui.SelectBase[string](c, selected)
	trigger := sel.Trigger

	disabled := trigger.IsDisabled()
	bg := p.Sidebar
	textColor, iconColor := p.Text, p.Muted
	switch {
	case disabled:
		textColor, iconColor = p.Faint, p.Faint
	case trigger.Pressed():
		bg = p.Press
	case trigger.Hovered():
		bg = p.Hover
	}

	trigger.Padding(7, 10).Gap(8).Radius(8).
		Background(bg).BorderWidth(1).BorderColor(p.Hairline).
		Justify(ui.SpaceBetween)
	trigger.Children(func() {
		ui.Text(c, *selected).FontSize(13).TextColor(textColor).SingleLine()
		ui.Icon(c, IconChevron).FontSize(IconSizeDefault).TextColor(iconColor)
	})

	// The popup is the elevated surface: white over the light page, the lighter
	// panel face over the dark window.
	popupBG := p.BG
	if p.Dark {
		popupBG = p.Sidebar
	}
	sel.Popup(func(panel ui.Element) {
		panel.Padding(4).Gap(1).Radius(10).
			Background(popupBG).BorderWidth(1).BorderColor(p.Hairline).
			Shadow(0, 6, 20, 0, p.CardShade)
		for _, option := range options {
			item := sel.Item(option).Padding(6, 8).Gap(8).Radius(6).AlignItems(ui.Center)
			if item.Highlighted() {
				item.Background(p.Hover)
			}
			item.Children(func() {
				ic := IconChevron
				if iconFor != nil {
					if got := iconFor(option); got != nil {
						ic = got
					}
				}
				ui.Icon(c, ic).FontSize(IconSizeDefault).TextColor(p.Muted)
				ui.Text(c, option).FontSize(13).TextColor(p.Text).SingleLine().Grow(1)
				if option == *selected {
					ui.Icon(c, IconCheck).FontSize(IconSizeDefault).TextColor(p.Accent)
				} else {
					// Reserve the check's space so labels stay aligned.
					ui.Box(c).Size(IconSizeDefault, IconSizeDefault)
				}
			})
		}
	})
	return trigger
}
