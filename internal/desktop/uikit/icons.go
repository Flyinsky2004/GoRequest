package uikit

import "github.com/egoist/mygo/ui"

// Icon rules (§8 stage 1): every icon is drawn on a 24×24 grid, stroked at
// 1.75 DIP with round caps and joins, `fill:none`, and colored by
// `currentColor` so it follows the text color where it is placed. Icons are
// parsed once at startup; malformed markup yields nil so a bad icon can never
// crash the app.
const IconStrokeWidth = 1.75

// IconSizeDefault is the default icon size in DIPs (also the icon part of an
// icon button's hit target).
const IconSizeDefault float32 = 16

func icon(markup string) *ui.SVG {
	svg, err := ui.ParseSVG([]byte(markup))
	if err != nil {
		return nil
	}
	return svg
}

const iconOpen = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">`
const iconClose = `</svg>`

func svgIcon(body string) *ui.SVG { return icon(iconOpen + body + iconClose) }

// A starter icon set. Grow this rather than inlining SVG in views.
var (
	IconSettings = svgIcon(`<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 0 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 0 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 0 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 0 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z"/>`)
	IconPlus     = svgIcon(`<line x1="12" y1="5" x2="12" y2="19"/><line x1="5" y1="12" x2="19" y2="12"/>`)
	IconMore     = svgIcon(`<circle cx="5" cy="12" r="1"/><circle cx="12" cy="12" r="1"/><circle cx="19" cy="12" r="1"/>`)
	IconClose    = svgIcon(`<line x1="18" y1="6" x2="6" y2="18"/><line x1="6" y1="6" x2="18" y2="18"/>`)
	IconSearch   = svgIcon(`<circle cx="11" cy="11" r="7"/><line x1="21" y1="21" x2="16.65" y2="16.65"/>`)
	IconChevron  = svgIcon(`<polyline points="6 9 12 15 18 9"/>`)
	IconCheck    = svgIcon(`<polyline points="20 6 9 17 4 12"/>`)
	IconSend     = svgIcon(`<line x1="22" y1="2" x2="11" y2="13"/><polygon points="22 2 15 22 11 13 2 9 22 2"/>`)
	IconStar     = svgIcon(`<polygon points="12 2 15.09 8.26 22 9.27 17 14.14 18.18 21.02 12 17.77 5.82 21.02 7 14.14 2 9.27 8.91 8.26 12 2"/>`)
	IconPin      = svgIcon(`<path d="M12 17v5"/><path d="M9 10.76V7h6v3.76a5 5 0 0 0 2 4V17H7v-2.24a5 5 0 0 0 2-4z"/>`)
	IconRetry    = svgIcon(`<polyline points="23 4 23 10 17 10"/><path d="M20.49 15a9 9 0 1 1-2.12-9.36L23 10"/>`)
	IconCopy     = svgIcon(`<rect x="9" y="9" width="13" height="13" rx="2"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>`)
	IconFolder   = svgIcon(`<path d="M3 7a2 2 0 0 1 2-2h4l2 2h6a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/>`)
	IconFile     = svgIcon(`<path d="M14 3H7a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h10a2 2 0 0 0 2-2V8z"/><polyline points="14 3 14 8 19 8"/>`)
	IconLock     = svgIcon(`<rect x="4" y="11" width="16" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>`)

	// Icons used by option pickers and the themed select.
	IconGlobe   = svgIcon(`<circle cx="12" cy="12" r="10"/><line x1="2" y1="12" x2="22" y2="12"/><path d="M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z"/>`)
	IconSun     = svgIcon(`<circle cx="12" cy="12" r="5"/><line x1="12" y1="1" x2="12" y2="3"/><line x1="12" y1="21" x2="12" y2="23"/><line x1="4.22" y1="4.22" x2="5.64" y2="5.64"/><line x1="18.36" y1="18.36" x2="19.78" y2="19.78"/><line x1="1" y1="12" x2="3" y2="12"/><line x1="21" y1="12" x2="23" y2="12"/><line x1="4.22" y1="19.78" x2="5.64" y2="18.36"/><line x1="18.36" y1="5.64" x2="19.78" y2="4.22"/>`)
	IconMoon    = svgIcon(`<path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z"/>`)
	IconMonitor = svgIcon(`<rect x="2" y="3" width="20" height="14" rx="2"/><line x1="8" y1="21" x2="16" y2="21"/><line x1="12" y1="17" x2="12" y2="21"/>`)
	IconKey     = svgIcon(`<path d="M21 2l-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0l3 3L22 7l-3-3m-3.5 3.5L19 4"/>`)
	IconClock   = svgIcon(`<circle cx="12" cy="12" r="10"/><polyline points="12 6 12 12 16 14"/>`)
	IconList    = svgIcon(`<line x1="8" y1="6" x2="21" y2="6"/><line x1="8" y1="12" x2="21" y2="12"/><line x1="8" y1="18" x2="21" y2="18"/><line x1="3" y1="6" x2="3.01" y2="6"/><line x1="3" y1="12" x2="3.01" y2="12"/><line x1="3" y1="18" x2="3.01" y2="18"/>`)
	IconCode    = svgIcon(`<polyline points="16 18 22 12 16 6"/><polyline points="8 6 2 12 8 18"/>`)
	IconServer  = svgIcon(`<rect x="2" y="2" width="20" height="8" rx="2"/><rect x="2" y="14" width="20" height="8" rx="2"/><line x1="6" y1="6" x2="6.01" y2="6"/><line x1="6" y1="18" x2="6.01" y2="18"/>`)
	IconShield  = svgIcon(`<path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z"/>`)
	IconCookie  = svgIcon(`<circle cx="12" cy="12" r="10"/><circle cx="8.5" cy="8.5" r="1"/><circle cx="15.5" cy="8.5" r="1"/><circle cx="12" cy="15" r="1"/>`)
	IconType    = svgIcon(`<polyline points="4 7 4 4 20 4 20 7"/><line x1="9" y1="20" x2="15" y2="20"/><line x1="12" y1="4" x2="12" y2="20"/>`)
	IconLayers  = svgIcon(`<polygon points="12 2 2 7 12 12 22 7 12 2"/><polyline points="2 17 12 22 22 17"/><polyline points="2 12 12 17 22 12"/>`)
	IconLink    = svgIcon(`<path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71"/><path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71"/>`)
)
