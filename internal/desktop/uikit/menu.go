package uikit

import "github.com/egoist/mygo/ui"

// MenuButton is the single drop-down entry point of the workbench. Its trigger
// follows the palette like every other control — the sidebar face at rest, the
// hover/press washes, the hairline ring, the muted chevron — and its menu is
// drawn inside the window by MyGo (ui.PopoverMenuButton) instead of asking the
// system for a native menu. The header's "+" new menu, the project and
// environment switchers, the tab overflow menu, and the runner, mock, identity
// and document menus all come through here, so every drop-down shares one
// surface, one keyboard model and one dismissal model.
//
// The build function is the same one a native ui.MenuButton takes, so every
// item, separator, Disabled, Checked, Radio, Shortcut and Submenu is preserved.
// The keyboard, focus, Escape, click-outside and window-edge flipping behavior
// comes from the drawn menu itself: Enter/Space/Down open it on the first item,
// the arrows move among the choosable items, Right/Left open and close
// submenus, Escape closes the top level, and a press outside closes it.
//
// Motion boundary: this menu surface belongs to MyGo's drawn-menu runtime, not
// to a panel this package builds. v0.7.4 exposes no open state or surface hook
// for PopoverMenuButton (the trigger's expanded state is written after the view
// is built and has no getter), so there is no safe way to scale, fade or turn
// the menu without reimplementing its items, submenus, type-ahead and focus
// handling. The trigger keeps its existing hover/press feedback; the
// controllable uikit.Select popup keeps its own spring. Replacing the drawn
// menu would require a new MyGo API, which this task must not add.
func MenuButton(c *ui.Context, label string, build func(m *ui.Menu)) ui.Element {
	return MenuButtonWithPalette(c, PaletteFor(c.Theme()), label, build)
}

// MenuButtonWithPalette is MenuButton for a view that already resolved the
// palette from its theme, as the workspace header does.
func MenuButtonWithPalette(c *ui.Context, p Palette, label string, build func(m *ui.Menu)) ui.Element {
	e := ActionCursor(ui.ButtonBase(c)).Height(30).Padding(0, 10).Gap(6).
		Radius(8).AlignItems(ui.Center).BorderWidth(1).BorderColor(p.Hairline)
	textColor, iconColor := p.Text, p.Muted
	switch {
	case e.IsDisabled():
		textColor, iconColor = p.Faint, p.Faint
		e.Background(p.Sidebar)
	case e.Pressed():
		e.Background(p.Press)
	case e.Hovered():
		e.Background(p.Hover)
	default:
		e.Background(p.Sidebar)
	}
	e.Label(label)
	e.Children(func() {
		ui.Text(c, label).FontSize(13).TextColor(textColor).SingleLine()
		ui.Icon(c, IconChevron).FontSize(11).TextColor(iconColor)
	})
	return e.PopoverMenuButton(build)
}
