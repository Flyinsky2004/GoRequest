package desktop

import "github.com/egoist/mygo/ui"

// The table toolbar's icons are drawn on the same 24×24 grid, stroke and
// round joins as uikit's set, so the controls sit beside the app's other
// icon buttons without a second visual language.
//
// The structural icons share one picture: a table frame with a header
// band. A row control puts its plus or minus in the body below the band,
// a column control puts it in the right-hand column, so "add row" and
// "add column" cannot be mistaken for each other at 16 DIPs.

const docTableIconOpen = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.75" stroke-linecap="round" stroke-linejoin="round">`
const docTableIconClose = `</svg>`

func docTableIcon(body string) *ui.SVG {
	svg, err := ui.ParseSVG([]byte(docTableIconOpen + body + docTableIconClose))
	if err != nil {
		return nil
	}
	return svg
}

var (
	docTableIconRowAdd    = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="2"/><line x1="4" y1="10" x2="20" y2="10"/><line x1="12" y1="13.5" x2="12" y2="19.5"/><line x1="9" y1="16.5" x2="15" y2="16.5"/>`)
	docTableIconRowDelete = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="2"/><line x1="4" y1="10" x2="20" y2="10"/><line x1="9" y1="16.5" x2="15" y2="16.5"/>`)
	docTableIconColAdd    = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="2"/><line x1="10" y1="4" x2="10" y2="20"/><line x1="13.5" y1="12" x2="19.5" y2="12"/><line x1="16.5" y1="9" x2="16.5" y2="15"/>`)
	docTableIconColDelete = docTableIcon(`<rect x="4" y="4" width="16" height="16" rx="2"/><line x1="10" y1="4" x2="10" y2="20"/><line x1="13.5" y1="12" x2="19.5" y2="12"/>`)

	docTableIconAlignLeft   = docTableIcon(`<line x1="4" y1="6" x2="20" y2="6"/><line x1="4" y1="12" x2="14" y2="12"/><line x1="4" y1="18" x2="18" y2="18"/>`)
	docTableIconAlignCenter = docTableIcon(`<line x1="4" y1="6" x2="20" y2="6"/><line x1="7" y1="12" x2="17" y2="12"/><line x1="5" y1="18" x2="19" y2="18"/>`)
	docTableIconAlignRight  = docTableIcon(`<line x1="4" y1="6" x2="20" y2="6"/><line x1="10" y1="12" x2="20" y2="12"/><line x1="6" y1="18" x2="20" y2="18"/>`)

	// docTableIconScroll marks the hint shown when the table is wider than
	// the canvas and scrolls sideways.
	docTableIconScroll = docTableIcon(`<path d="M4 12h16"/><path d="m8 8-4 4 4 4"/><path d="m16 8 4 4-4 4"/>`)

	// docTableIconWarning marks a refused edit beside its message.
	docTableIconWarning = docTableIcon(`<path d="M12 4 2.8 20h18.4Z"/><line x1="12" y1="10" x2="12" y2="14.5"/><circle cx="12" cy="17.2" r="0.6" fill="currentColor" stroke="none"/>`)
)
