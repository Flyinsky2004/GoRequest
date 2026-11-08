package desktop

import (
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// fileSyncBtnKind selects the visual weight of a conflict action. Exactly one
// primary action per conflict state: Danger for "accept delete" (discarding
// unsaved edits), Accent for non-destructive saves. Everything else is
// secondary or ghost, so "restore file" is never highlighted in red.
type fileSyncBtnKind int

const (
	fileSyncBtnSecondary fileSyncBtnKind = iota
	fileSyncBtnGhost
	fileSyncBtnAccent
	fileSyncBtnDanger
)

const (
	fileSyncIconStart = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">`
	fileSyncIconEnd   = `</svg>`
)

func fileSyncIcon(body string) *ui.SVG {
	svg, err := ui.ParseSVG([]byte(fileSyncIconStart + body + fileSyncIconEnd))
	if err != nil {
		return nil
	}
	return svg
}

// Icons reused from uikit where one exists; trash, undo and columns are
// dedicated to the conflict dialog.
var (
	fileSyncIconCompare = fileSyncIcon(`<rect x="3" y="4" width="7" height="16" rx="1.5"/><rect x="14" y="4" width="7" height="16" rx="1.5"/>`)
	fileSyncIconTrash   = fileSyncIcon(`<polyline points="3 6 5 6 21 6"/><path d="M19 6l-1 14a2 2 0 0 1-2 2H8a2 2 0 0 1-2-2L5 6"/><path d="M10 11v6"/><path d="M14 11v6"/><path d="M9 6V4a1 1 0 0 1 1-1h4a1 1 0 0 1 1 1v2"/>`)
	fileSyncIconRestore = fileSyncIcon(`<polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>`)
	fileSyncIconMerge   = fileSyncIcon(`<circle cx="6" cy="6" r="2.5"/><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="12" r="2.5"/><path d="M6 8.5v7"/><path d="M8.5 6a6 6 0 0 1 7 4.5"/>`)
	fileSyncIconLater   = uikit.IconClock
	fileSyncIconSave    = uikit.IconCheck
	fileSyncIconCopy    = uikit.IconCopy
	fileSyncIconReload  = uikit.IconRetry
)

// fileSyncButtonColors returns background, hover background, foreground and
// optional border for kind under the active theme. Pure so it is unit-testable.
func fileSyncButtonColors(p uikit.Palette, kind fileSyncBtnKind, disabled bool) (bg, hover, fg, border ui.Color) {
	switch kind {
	case fileSyncBtnDanger:
		bg, fg = p.Danger, p.OnAccent
	case fileSyncBtnAccent:
		bg, fg = p.Accent, p.OnAccent
	case fileSyncBtnGhost:
		bg, fg = ui.RGBA(0, 0, 0, 0), p.Text
	default:
		bg, fg, border = p.Hover, p.Text, p.Hairline
	}
	hover = bg.Mix(ui.RGB(255, 255, 255), 0.12)
	if kind == fileSyncBtnSecondary || kind == fileSyncBtnGhost {
		hover = p.Hairline
	}
	if disabled {
		bg, hover, fg = bg.Alpha(0.45), bg.Alpha(0.45), fg.Alpha(0.45)
	}
	return
}

// fileSyncButton draws an icon+label action button. compact is the banner
// size. The returned element reports Clicked(); disabled buttons ignore input
// and keep the default cursor.
func fileSyncButton(c *ui.Context, t *ui.Theme, kind fileSyncBtnKind, label string, ic *ui.SVG, compact, disabled bool) ui.Element {
	p := uikit.PaletteFor(t)
	bg, hover, fg, border := fileSyncButtonColors(p, kind, disabled)
	h, padX, fs := float32(32), float32(12), float32(13)
	if compact {
		h, padX, fs = 28, 10, 12
	}
	e := ui.ButtonBase(c).Height(h).Radius(8).Padding(0, padX).Gap(6).AlignItems(ui.Center)
	if disabled {
		e = e.Disabled(true)
	}
	e = uikit.ActionCursor(e)
	if border != (ui.Color{}) {
		e = e.BorderWidth(1).BorderColor(border)
	}
	cur := bg
	if !disabled && e.Hovered() {
		cur = hover
	}
	e.Background(cur)
	e.Children(func() {
		if ic != nil {
			ui.Icon(c, ic).FontSize(14).TextColor(fg)
		}
		ui.Text(c, label).FontSize(fs).FontWeight(600).TextColor(fg).SingleLine()
	})
	return e
}
