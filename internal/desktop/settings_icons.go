package desktop

import "github.com/egoist/mygo/ui"

// Settings icons follow the same drawing rules as the shared uikit icon table:
// a 24×24 grid, 1.75 DIP strokes, round caps and joins, fill:none, and
// currentColor so every icon follows the text color of the control it sits in.
// They live in this file rather than uikit/icons.go because U13 owns the
// settings surface and must not edit that public table; U14 can later promote
// them if a shared entry is wanted.
const (
	settingsIconOpen  = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">`
	settingsIconClose = `</svg>`
)

// settingsSVG parses one settings icon once at startup. parseIcon returns nil
// for malformed markup, so a bad icon can never crash the sheet; callers skip a
// nil icon instead of drawing it.
func settingsSVG(body string) *ui.SVG {
	return parseIcon(settingsIconOpen + body + settingsIconClose)
}

// Category icons. Each one names the category it opens, never a decorative
// motif: contrast for Appearance, a globe for Language, power for Startup,
// sliders for Workspace, a text document for Markdown preview, a clock with a
// history arrow for History, and an information mark for About.
var (
	settingsIconAppearance = settingsSVG(`<circle cx="12" cy="12" r="9"/><path d="M12 3a9 9 0 0 0 0 18z"/>`)
	settingsIconLanguage   = settingsSVG(`<circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>`)
	settingsIconStartup    = settingsSVG(`<path d="M18.36 6.64a9 9 0 1 1-12.73 0"/><line x1="12" y1="2" x2="12" y2="12"/>`)
	settingsIconWorkspace  = settingsSVG(`<line x1="4" y1="21" x2="4" y2="14"/><line x1="4" y1="10" x2="4" y2="3"/><line x1="12" y1="21" x2="12" y2="12"/><line x1="12" y1="8" x2="12" y2="3"/><line x1="20" y1="21" x2="20" y2="16"/><line x1="20" y1="12" x2="20" y2="3"/><line x1="1" y1="14" x2="7" y2="14"/><line x1="9" y1="8" x2="15" y2="8"/><line x1="17" y1="16" x2="23" y2="16"/>`)
	settingsIconMarkdown   = settingsSVG(`<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><polyline points="14 3 14 8 19 8"/><line x1="8" y1="13" x2="16" y2="13"/><line x1="8" y1="17" x2="14" y2="17"/>`)
	settingsIconHistory    = settingsSVG(`<path d="M3 12a9 9 0 1 0 9-9 9.75 9.75 0 0 0-6.74 2.74L3 8"/><path d="M3 3v5h5"/><path d="M12 7v5l4 2"/>`)
	settingsIconAbout      = settingsSVG(`<circle cx="12" cy="12" r="10"/><line x1="12" y1="16" x2="12" y2="12"/><line x1="12" y1="8" x2="12.01" y2="8"/>`)

	// Action icons for the real operations on the sheet: Done dismisses it and
	// Reset restores the Markdown preview defaults.
	settingsIconDone  = settingsSVG(`<polyline points="20 6 9 17 4 12"/>`)
	settingsIconReset = settingsSVG(`<polyline points="1 4 1 10 7 10"/><path d="M3.51 15a9 9 0 1 0 2.13-9.36L1 10"/>`)
)

// settingsCategoryIcons returns one icon per rail category in display order.
// The length always matches settingsCategoryCount so a caller can index it by
// settingsCategory without a bounds surprise.
func settingsCategoryIcons() []*ui.SVG {
	return []*ui.SVG{
		settingsIconAppearance,
		settingsIconLanguage,
		settingsIconStartup,
		settingsIconWorkspace,
		settingsIconMarkdown,
		settingsIconHistory,
		settingsIconAbout,
	}
}

// settingsCategoryIcon returns the icon for one category, or nil when the
// category is outside the known range.
func settingsCategoryIcon(cat settingsCategory) *ui.SVG {
	icons := settingsCategoryIcons()
	if int(cat) < 0 || int(cat) >= len(icons) {
		return nil
	}
	return icons[int(cat)]
}
