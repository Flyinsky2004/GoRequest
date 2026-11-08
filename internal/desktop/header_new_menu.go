package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// newRequestIcon is the workspace header's single new-entry glyph. The header
// used to pair a "New Request" pill with a "+" drop-down; U26 removes the pill
// and puts this real icon on the one remaining menu trigger. It follows the
// shared icon rules — 24×24 grid, 1.75 stroke, round caps/joins, fill:none,
// currentColor — but lives here because this task owns only the workspace
// header and must not edit the shared UIKit files.
var newRequestIcon = parseHeaderIcon(
	`<path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z"/>` +
		`<polyline points="14 2 14 8 20 8"/>` +
		`<line x1="12" y1="18" x2="12" y2="12"/>` +
		`<line x1="9" y1="15" x2="15" y2="15"/>`,
)

// parseHeaderIcon builds an SVG from the shared icon wrapper. A malformed
// markup yields nil, exactly like uikit's own icon helper, so a bad icon can
// never crash the header.
func parseHeaderIcon(body string) *ui.SVG {
	svg, err := ui.ParseSVG([]byte(
		`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" ` +
			`stroke="currentColor" stroke-width="1.75" stroke-linecap="round" ` +
			`stroke-linejoin="round">` + body + `</svg>`))
	if err != nil {
		return nil
	}
	return svg
}

// newMenuButton is the header's one new-entry drop-down. It keeps the trigger
// geometry and states of uikit.MenuButtonWithPalette — 30 high, 10 horizontal
// padding, radius 8, hairline ring, sidebar face, hover/press wash, pointer
// cursor, and the drawn in-window popover with its spring — but shows the
// new-request icon instead of a text label. The accessible name stays on the
// trigger, so the icon-only button is still announced as "New Request".
func newMenuButton(c *ui.Context, p uikit.Palette, label, tooltip string, build func(*ui.Menu)) ui.Element {
	e := uikit.ActionCursor(ui.ButtonBase(c)).Height(30).Padding(0, 10).Gap(6).
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
		ui.Icon(c, newRequestIcon).FontSize(uikit.IconSizeDefault).TextColor(textColor)
		ui.Icon(c, uikit.IconChevron).FontSize(11).TextColor(iconColor)
	})
	if tooltip != "" {
		e.Tooltip(tooltip)
	}
	return e.PopoverMenuButton(build)
}
