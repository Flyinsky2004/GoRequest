package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// This file continues documentation.go with link navigation, caret helpers
// and the validated attachment import flow.

func (a *App) handleDocLink(c *ui.Context, root ui.Element, tab *Tab, st *docViewState, dest string) {
	ps := tab.Owner
	if ps == nil || ps.project == nil {
		return
	}
	// C03: request references carry their own target behavior; a gorequest
	// destination resolves in the derived-docs layer, never as a filesystem
	// path. Unhandled document failures fall back to the B09 paths below.
	if strings.HasPrefix(dest, "gorequest://") {
		if a.docgenOpenRequestLink(tab, dest) {
			return
		}
	}
	tgt, err := ps.project.ResolveDocLink(tab.DocRel, dest)
	if err != nil {
		a.status = a.docLinkProblem(err)
		return
	}
	switch tgt.Kind {
	case core.LinkAnchor:
		if tgt.Line > 0 {
			a.docGoToLine(tab, st, tgt.Line, 1)
		} else {
			st.anchor = tgt.Slug
			st.anchorVersion++
		}
	case core.LinkDoc:
		if err := ps.openDoc(tgt.RelPath); err != nil {
			a.status = err.Error()
			return
		}
		target := ps.activeTab()
		if target == nil || target.Kind != "doc" {
			return
		}
		if target != tab {
			// The target tab is not built yet this frame; park the anchor in
			// its state, which the next frame applies before rendering.
			docStateFor(root, target).pendingAnchor = tgt.Slug
		} else {
			st.anchor = tgt.Slug
			st.anchorVersion++
		}
		if tgt.Line > 0 {
			target.DocEditor.Select(0, 0)
			a.docGoToLine(target, docStateFor(root, target), tgt.Line, 1)
		}
	case core.LinkExternal:
		// A link is only ever opened by the user's click that produced this
		// callback: opening a document or rendering the preview never opens
		// a URL.
		c.OpenURL(tgt.URL)
	case core.LinkDocRef:
		a.docgenOpenRequestLink(tab, tgt.Dest)
	case core.LinkAsset:
		a.status = fmt.Sprintf(a.docMsg().AttachmentRef, tgt.RelPath)
	}
}

func (a *App) docLinkProblem(err error) string {
	dm := a.docMsg()
	dest := err.Error()
	if p, ok := err.(*core.LinkProblem); ok {
		dest = p.Dest
		switch p.Kind {
		case core.LinkProblemMissingDocument:
			return fmt.Sprintf(dm.MissingDocument, dest)
		case core.LinkProblemMissingAsset:
			return fmt.Sprintf(dm.MissingAttachment, dest)
		case core.LinkProblemMissingAnchor:
			return fmt.Sprintf(dm.MissingAnchor, dest)
		case core.LinkProblemUnsupportedAsset:
			return fmt.Sprintf(dm.UnsupportedAttach, dest)
		}
	}
	return fmt.Sprintf(dm.BlockedLink, dest)
}

func (a *App) docAssetProblem(err error) string {
	dm := a.docMsg()
	if p, ok := err.(*core.AssetProblem); ok {
		switch p.Kind {
		case core.AssetProblemTooLarge:
			return fmt.Sprintf(dm.ImportFailed, p.Kind+" "+p.Detail+" "+p.Ref)
		case core.AssetProblemUnsupported:
			return fmt.Sprintf(dm.UnsupportedAttach, p.Ref)
		case core.AssetProblemSymlink:
			return fmt.Sprintf(dm.ImportFailed, "symlink "+p.Ref)
		}
		return fmt.Sprintf(dm.ImportFailed, p.Kind+" "+p.Ref)
	}
	return fmt.Sprintf(dm.ImportFailed, err.Error())
}

func (a *App) docGoToHeading(tab *Tab, st *docViewState, h core.Heading) {
	a.docGoToLine(tab, st, h.Line, 1)
}

func (a *App) docGoToLine(tab *Tab, st *docViewState, line, col int) {
	if tab == nil || tab.DocEditor == nil {
		return
	}
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	pos := editor.Pos{Line: line - 1, Col: col - 1}
	off := tab.DocEditor.Buffer.Offset(pos)
	tab.DocEditor.Select(off, off)
	tab.DocEditor.ScrollTo(line - 1)
	// Keep the preview on the section that contains the caret, so an outline
	// or diagnostics jump moves both panes.
	if h, ok := core.HeadingAtLine(st.headings, line); ok {
		st.anchor = h.Slug
		st.anchorVersion++
	}
}

func (a *App) openDocSearchMatch(root ui.Element, tab *Tab, match core.DocMatch) {
	ps := tab.Owner
	if ps == nil {
		return
	}
	target := tab
	if match.RelPath != tab.DocRel {
		if err := ps.openDoc(match.RelPath); err != nil {
			a.status = err.Error()
			return
		}
		target = ps.activeTab()
		if target == nil || target.Kind != "doc" {
			return
		}
	}
	line := max(1, match.Line)
	off := target.DocEditor.Buffer.Offset(editor.Pos{Line: line - 1, Col: match.ByteColumn})
	end := min(off+(match.End-match.Start), target.DocEditor.Buffer.Len())
	target.DocEditor.Select(off, max(off, end))
	target.DocEditor.ScrollTo(line - 1)

	anchor := ""
	if h, ok := core.HeadingAtLine(core.ExtractHeadings(target.DocEditor.Buffer.String()), line); ok {
		anchor = h.Slug
	}
	st := docStateFor(root, target)
	if target != tab {
		st.pendingAnchor = anchor
	} else if anchor != "" {
		st.anchor = anchor
		st.anchorVersion++
	}
}

// --- insertion helpers ----------------------------------------------------

func wrapDocSelection(tab *Tab, left, right string) {
	if tab == nil || tab.DocEditor == nil {
		return
	}
	sel := tab.DocEditor.Selection()
	start, end := sel.Start(), sel.End()
	text := tab.DocEditor.Buffer.Slice(start, end)
	tab.DocEditor.Select(start, end)
	tab.DocEditor.Insert(left + text + right)
	tab.DocEditor.Select(start+len(left), start+len(left)+len(text))
}

func insertDocLink(tab *Tab) {
	if tab == nil || tab.DocEditor == nil {
		return
	}
	sel := tab.DocEditor.Selection()
	start, end := sel.Start(), sel.End()
	text := tab.DocEditor.Buffer.Slice(start, end)
	if strings.TrimSpace(text) == "" {
		text = "text"
	}
	prefix := "[" + text + "]("
	tab.DocEditor.Select(start, end)
	tab.DocEditor.Insert(prefix + "url)")
	tab.DocEditor.Select(start+len(prefix), start+len(prefix)+3)
}

func insertDocTemplate(tab *Tab, before, after string) {
	if tab == nil || tab.DocEditor == nil {
		return
	}
	cur := tab.DocEditor.Cursor()
	tab.DocEditor.Select(cur, cur)
	tab.DocEditor.Insert(before + after)
	tab.DocEditor.Select(cur+len(before), cur+len(before))
}

// --- asset import ---------------------------------------------------------

// chooseDocAsset opens the native file picker in a background goroutine, as
// the project picker does, then imports on the UI thread through the window's
// Update.
func (a *App) chooseDocAsset(tab *Tab, targetDir string) {
	dm := a.docMsg()
	var pw *mygo.Window
	if nw, ok := a.win.(nativeWindower); ok {
		pw = nw.nativeWindow()
	}
	if pw == nil {
		a.status = dm.DialogUnavailable
		return
	}
	filters := []mygo.FileFilter{
		{Name: dm.ImageFilter, Extensions: core.ImageAssetExts()},
		{Name: dm.AttachmentFilter, Extensions: core.AllowedAssetExts()},
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent: pw, Title: dm.InsertImageDocs,
			ButtonLabel: dm.Insert, Filters: filters,
		})
		if err != nil {
			a.win.Update(func() { a.status = fmt.Sprintf(dm.ImportFailed, err.Error()) })
			return
		}
		if len(paths) == 0 {
			return
		}
		a.win.Update(func() { a.importDocAsset(tab, paths[0], targetDir) })
	}()
}

func (a *App) importDocAsset(tab *Tab, srcPath, targetDir string) {
	if tab == nil || tab.Owner == nil || tab.DocEditor == nil {
		return
	}
	imp, err := tab.Owner.project.ImportDocAsset(tab.DocRel, srcPath, targetDir)
	if err != nil {
		a.status = a.docAssetProblem(err)
		return
	}
	cur := tab.DocEditor.Cursor()
	tab.DocEditor.Select(cur, cur)
	tab.DocEditor.Insert(imp.Markdown)
	a.status = fmt.Sprintf(a.docMsg().Imported, imp.RelPath)
}
