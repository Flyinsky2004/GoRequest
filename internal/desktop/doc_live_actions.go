package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/docedit"
)

// This file continues the canvas with formatting actions, the U07 table
// replacement callback, asset insertion and the save/mode flush points.

// --- formatting -----------------------------------------------------------

// docBlockWrap wraps the active editor's selection, one local edit that the
// next frame commits as one undo step.
func (a *App) docBlockWrap(be *docBlockEdit, left, right string) {
	if be == nil || be.ed == nil {
		return
	}
	sel := be.ed.Selection()
	start, end := sel.Start(), sel.End()
	text := be.ed.Buffer.Slice(start, end)
	be.ed.Select(start, end)
	be.ed.Insert(left + text + right)
	be.ed.Select(start+len(left), start+len(left)+len(text))
}

// docWrapSelection routes the Insert menu's bold/italic to the active block
// when the canvas is editing one.
func (a *App) docWrapSelection(tab *Tab, st *docViewState, left, right string) {
	if st != nil && st.live != nil && st.live.block != nil {
		a.docBlockWrap(st.live.block, left, right)
		return
	}
	wrapDocSelection(tab, left, right)
}

// docInsertLink routes the Insert menu's link to the active block.
func (a *App) docInsertLink(tab *Tab, st *docViewState) {
	if st == nil || st.live == nil || st.live.block == nil {
		insertDocLink(tab)
		return
	}
	be := st.live.block
	sel := be.ed.Selection()
	start, end := sel.Start(), sel.End()
	text := be.ed.Buffer.Slice(start, end)
	if strings.TrimSpace(text) == "" {
		text = "text"
	}
	prefix := "[" + text + "]("
	be.ed.Select(start, end)
	be.ed.Insert(prefix + "url)")
	be.ed.Select(start+len(prefix), start+len(prefix)+3)
}

// docInsertTemplate routes the Insert menu's code/table templates to the
// active block.
func (a *App) docInsertTemplate(tab *Tab, st *docViewState, before, after string) {
	if st == nil || st.live == nil || st.live.block == nil {
		insertDocTemplate(tab, before, after)
		return
	}
	be := st.live.block
	cur := be.ed.Cursor()
	be.ed.Select(cur, cur)
	be.ed.Insert(before + after)
	be.ed.Select(cur+len(before), cur+len(before))
}

// --- table integration ----------------------------------------------------

// replaceDocBlockSource applies a U07 table edit as one minimal transaction.
func (a *App) replaceDocBlockSource(tab *Tab, st *docViewState, b docedit.Block, newSource string) {
	a.commitDocBlock(tab, st)
	src := tab.DocEditor.Buffer.String()
	blocks := docLiveFor(st).blocksFor(src)
	nb, ok := docedit.BlockAt(blocks, b.Start)
	if !ok {
		return
	}
	tab.DocEditor.Buffer.Edit(editor.Edit{Start: nb.Start, End: nb.End, Text: newSource})
	if st.live != nil && st.live.block != nil && st.live.block.start == nb.Start {
		a.relocateDocBlock(tab, st, nb.Start)
	}
}

// --- asset insertion ------------------------------------------------------

// chooseDocAssetAt is chooseDocAsset for the canvas: the picked image is
// inserted into the active block when it is still active, else at the
// document caret.
func (a *App) chooseDocAssetAt(tab *Tab, st *docViewState, targetDir string) {
	be := (*docBlockEdit)(nil)
	if st != nil && st.live != nil {
		be = st.live.block
	}
	a.chooseDocAssetWith(tab, targetDir, func(md string) {
		if st != nil && st.live != nil && st.live.block == be && be != nil {
			cur := be.ed.Cursor()
			be.ed.Select(cur, cur)
			be.ed.Insert(md)
			return
		}
		if tab.DocEditor == nil {
			return
		}
		cur := tab.DocEditor.Cursor()
		tab.DocEditor.Select(cur, cur)
		tab.DocEditor.Insert(md)
	})
}

// flushDocCanvas commits a pending block edit, so saving and mode switches
// keep the last keystroke.
func (a *App) flushDocCanvas(tab *Tab, st *docViewState) {
	if st == nil || st.live == nil {
		return
	}
	a.commitDocBlock(tab, st)
}

// closeDocSlash closes the slash menu without touching the active block.
func (a *App) closeDocSlash(st *docViewState) {
	if st == nil || st.live == nil {
		return
	}
	if st.live.slashOpen {
		st.live.slashDismissed = st.live.slashQuery
	}
	st.live.slashOpen = false
	st.live.slashMode = 0
	st.live.slashIndex = 0
}
