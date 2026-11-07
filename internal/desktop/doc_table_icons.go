package desktop

import "github.com/egoist/mygo/ui"

// The table toolbar's icons are drawn on the same 24×24 grid, stroke and
// round joins as uikit's set, so the controls sit beside the app's other
// icon buttons without a second visual language.

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
	docTableIconRowAdd    = docTableIcon(`<line x1="4" y1="5" x2="20" y2="5"/><line x1="4" y1="19" x2="20" y2="19"/><line x1="12" y1="9" x2="12" y2="15"/><line x1="9" y1="12" x2="15" y2="12"/>`)
	docTableIconRowDelete = docTableIcon(`<line x1="4" y1="5" x2="20" y2="5"/><line x1="4" y1="19" x2="20" y2="19"/><line x1="9" y1="12" x2="15" y2="12"/>`)
	docTableIconColAdd    = docTableIcon(`<line x1="5" y1="4" x2="5" y2="20"/><line x1="19" y1="4" x2="19" y2="20"/><line x1="12" y1="9" x2="12" y2="15"/><line x1="9" y1="12" x2="15" y2="12"/>`)
	docTableIconColDelete = docTableIcon(`<line x1="5" y1="4" x2="5" y2="20"/><line x1="19" y1="4" x2="19" y2="20"/><line x1="9" y1="12" x2="15" y2="12"/>`)

	docTableIconAlignLeft   = docTableIcon(`<line x1="4" y1="6" x2="20" y2="6"/><line x1="4" y1="12" x2="14" y2="12"/><line x1="4" y1="18" x2="18" y2="18"/>`)
	docTableIconAlignCenter = docTableIcon(`<line x1="4" y1="6" x2="20" y2="6"/><line x1="7" y1="12" x2="17" y2="12"/><line x1="5" y1="18" x2="19" y2="18"/>`)
	docTableIconAlignRight  = docTableIcon(`<line x1="4" y1="6" x2="20" y2="6"/><line x1="10" y1="12" x2="20" y2="12"/><line x1="6" y1="18" x2="20" y2="18"/>`)
)
