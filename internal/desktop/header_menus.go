package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// headerMenuButton draws a themed header menu trigger whose menu MyGo renders
// inside the window (ui.PopoverMenuButton) instead of asking the system for a
// native menu. The trigger follows the palette like uikit.Select's trigger:
// the sidebar face at rest, the existing hover/press washes, the hairline ring
// and the muted chevron. The drawn menu uses the app theme, which uikit.Theme
// derives from the same palette, so light and dark match.
//
// The build function is the same one a native ui.MenuButton takes, so every
// item, separator, Disabled and Checked state is preserved. The keyboard,
// focus, Escape and click-outside behavior comes from the drawn menu itself:
// Enter/Space/Down open it on the first item, the arrows move among the
// choosable items, Escape closes the top level, and a press outside closes it.
func headerMenuButton(c *ui.Context, p uikit.Palette, label string, build func(*ui.Menu)) ui.Element {
	e := uikit.ActionCursor(ui.ButtonBase(c)).Height(30).Padding(0, 10).Gap(6).
		Radius(8).AlignItems(ui.Center).BorderWidth(1).BorderColor(p.Hairline)
	switch {
	case e.Pressed():
		e.Background(p.Press)
	case e.Hovered():
		e.Background(p.Hover)
	default:
		e.Background(p.Sidebar)
	}
	e.Label(label)
	e.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(p.Text).SingleLine()
		ui.Icon(c, uikit.IconChevron).FontSize(11).TextColor(p.Muted)
	})
	return e.PopoverMenuButton(build)
}
