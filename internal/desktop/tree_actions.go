package desktop

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// This file owns the sidebar tree's interactive actions (§3): the row context
// menu, in-place rename, drag reordering and undoable deletes. It is declared
// here so the window-level shortcut table (shortcuts.go) can call into it.

// draggedRequest is the value a request row drags to a folder row (or to the
// tree's root area). It is a distinct type so only these rows accept it.
type draggedRequest struct {
	id string
}

// --- selection helpers ----------------------------------------------------

// folderRelOfPath maps a request's project-relative file path to the
// collections-relative folder that holds it ("" for the root).
func folderRelOfPath(relPath string) string {
	dir := filepath.ToSlash(filepath.Dir(relPath))
	if dir == "." || dir == "collections" {
		return ""
	}
	return collectionRelOf(dir)
}

// findFolderInTree returns the folder node with the given project-relative
// path, or nil.
func findFolderInTree(ps *projectState, relPath string) *core.CollectionEntry {
	if ps == nil || ps.project == nil {
		return nil
	}
	var walk func([]*core.CollectionEntry) *core.CollectionEntry
	walk = func(nodes []*core.CollectionEntry) *core.CollectionEntry {
		for _, n := range nodes {
			if n.RelPath == relPath {
				return n
			}
			if found := walk(n.Children); found != nil {
				return found
			}
		}
		return nil
	}
	return walk(ps.project.Collections)
}

// findDocInProject returns the document entry with the given project-relative
// path, or nil.
func findDocInProject(ps *projectState, relPath string) *core.DocEntry {
	if ps == nil || ps.project == nil {
		return nil
	}
	for _, d := range ps.project.Docs {
		if d.RelPath == relPath {
			return d
		}
	}
	return nil
}

// countFolderRequests counts the requests in a folder subtree.
func countFolderRequests(n *core.CollectionEntry) int {
	if n == nil {
		return 0
	}
	total := len(n.Requests)
	for _, ch := range n.Children {
		total += countFolderRequests(ch)
	}
	return total
}

// listFolderRels appends the collections-relative path of every folder in the
// subtree, parents before children.
func listFolderRels(nodes []*core.CollectionEntry, out []string) []string {
	for _, n := range nodes {
		out = append(out, collectionRelOf(n.RelPath))
		out = listFolderRels(n.Children, out)
	}
	return out
}

// isUnder reports whether rel is path or a descendant of path.
func isUnder(rel, path string) bool {
	return rel == path || strings.HasPrefix(rel, path+"/")
}

// --- in-place rename ------------------------------------------------------

// beginRenameTreeRow starts in-place renaming of the selected tree row, if one
// is selected. F2 reaches this through shortcuts.go.
func (a *App) beginRenameTreeRow() {
	if a.ps == nil || a.ps.sel == "" {
		return
	}
	a.beginRename(a.ps.sel)
}

// beginRename starts in-place editing of the given tree row selection id.
func (a *App) beginRename(sel string) {
	ps := a.ps
	if ps == nil || ps.project == nil {
		return
	}
	name, ok := a.treeRowName(ps, sel)
	if !ok {
		return
	}
	ps.renaming = sel
	ps.renameText = name
	if a.win != nil {
		a.win.Update(func() {})
	}
}

// treeRowName returns the current name of a tree row selection id.
func (a *App) treeRowName(ps *projectState, sel string) (string, bool) {
	switch {
	case strings.HasPrefix(sel, "req:"):
		e := ps.project.Requests[strings.TrimPrefix(sel, "req:")]
		if e == nil || e.Request == nil {
			return "", false
		}
		return e.Request.Name, true
	case strings.HasPrefix(sel, "folder:"):
		n := findFolderInTree(ps, strings.TrimPrefix(sel, "folder:"))
		if n == nil {
			return "", false
		}
		return n.Name, true
	case strings.HasPrefix(sel, "doc:"):
		d := findDocInProject(ps, strings.TrimPrefix(sel, "doc:"))
		if d == nil {
			return "", false
		}
		return d.Name, true
	}
	return "", false
}

// cancelRename abandons the in-place edit without changing anything.
func (a *App) cancelRename() {
	if a.ps == nil {
		return
	}
	a.ps.renaming = ""
	a.ps.renameText = ""
}

// commitRename applies the in-place edit to the request, folder or document it
// was started on.
func (a *App) commitRename() {
	ps := a.ps
	if ps == nil || ps.project == nil || ps.renaming == "" {
		return
	}
	sel := ps.renaming
	name := ps.renameText
	ps.renaming = ""
	ps.renameText = ""

	switch {
	case strings.HasPrefix(sel, "req:"):
		e := ps.project.Requests[strings.TrimPrefix(sel, "req:")]
		if e == nil || e.Request == nil {
			return
		}
		if err := ps.project.RenameRequest(e, name); err != nil {
			a.status = err.Error()
			return
		}
		// Keep an open tab's title and baseline in step with the renamed file.
		for _, tab := range ps.tabs {
			if tab.Kind == "request" && tab.Entry == e {
				tab.Title = e.Request.Name
				tab.Baseline = e.BaselineDigest
			}
		}
		ps.sel = sel
		a.status = ""
	case strings.HasPrefix(sel, "folder:"):
		oldRel := strings.TrimPrefix(sel, "folder:")
		node := findFolderInTree(ps, oldRel)
		if node == nil {
			return
		}
		renamed, err := ps.project.RenameFolder(node, name)
		if err != nil {
			a.status = err.Error()
			return
		}
		a.rekeyExpanded(ps, oldRel, renamed.RelPath)
		ps.sel = "folder:" + renamed.RelPath
		a.status = ""
	case strings.HasPrefix(sel, "doc:"):
		oldRel := strings.TrimPrefix(sel, "doc:")
		d := findDocInProject(ps, oldRel)
		if d == nil {
			return
		}
		if err := ps.project.RenameDoc(d, name); err != nil {
			a.status = err.Error()
			return
		}
		for _, tab := range ps.tabs {
			if tab.Kind == "doc" && tab.DocRel == oldRel {
				tab.DocRel = d.RelPath
				tab.Title = d.Name
			}
		}
		ps.sel = "doc:" + d.RelPath
		a.status = ""
	}
}

// rekeyExpanded moves the expansion state of a renamed/moved folder and its
// descendants from the old path prefix to the new one.
func (a *App) rekeyExpanded(ps *projectState, oldRel, newRel string) {
	if ps.expanded == nil || oldRel == newRel {
		return
	}
	var keys []string
	for key := range ps.expanded {
		if isUnder(key, oldRel) {
			keys = append(keys, key)
		}
	}
	for _, key := range keys {
		open := ps.expanded[key]
		delete(ps.expanded, key)
		if open {
			ps.expanded[newRel+strings.TrimPrefix(key, oldRel)] = true
		}
	}
}

// treeRowLabel draws a row's name, or the in-place rename input when this row
// is being renamed. The caller draws no label of its own for a renamed row.
func (a *App) treeRowLabel(c *ui.Context, sel string, spans []ui.Span) {
	ps := a.ps
	if ps == nil || ps.renaming != sel {
		ui.RichText(c, spans...).Grow(1)
		return
	}
	in := ui.TextInput(c, &ps.renameText).Grow(1).SingleLine().AutoFocus().
		Label(a.msg().Rename)
	in.OnTextKey(func(k ui.TextKey) bool {
		if k.Key == ui.KeyEscape {
			a.cancelRename()
			return true
		}
		return false
	})
	if in.Submitted() {
		a.commitRename()
	}
}

// --- context menus --------------------------------------------------------

// requestMenu fills a request row's context menu (§3).
func (a *App) requestMenu(mm *ui.Menu, e *core.RequestEntry, sel string) {
	ps := a.ps
	m := a.msg()
	if mm.Item(m.OpenRow).Chosen() {
		ps.sel = sel
		if err := ps.openSelection(sel); err != nil {
			a.status = err.Error()
		}
	}
	if mm.Item(m.NewRequestHere).Chosen() {
		a.newRequestInFolder(folderRelOfPath(e.RelPath))
	}
	if mm.Item(m.Rename).Shortcut(0, ui.KeyF2).Chosen() {
		a.beginRename(sel)
	}
	if mm.Item(m.Duplicate).Chosen() {
		a.duplicateRequest(e)
	}
	current := folderRelOfPath(e.RelPath)
	mm.Submenu(m.MoveTo, func(sm *ui.Menu) {
		if sm.Item(folderLabel("")).Checked(current == "").Chosen() {
			a.moveRequestTo(e, "")
		}
		for _, rel := range listFolderRels(ps.project.Collections, nil) {
			target := rel
			if sm.Item(folderLabel(target)).Checked(current == target).Chosen() {
				a.moveRequestTo(e, target)
			}
		}
	})
	mm.Separator()
	a.appendTransferMenu(mm, e)
	mm.Separator()
	if mm.Item(m.Delete).Chosen() {
		a.deleteTreeRow(sel)
	}
}

// folderMenu fills a folder row's context menu (§3).
func (a *App) folderMenu(mm *ui.Menu, n *core.CollectionEntry, sel string) {
	ps := a.ps
	m := a.msg()
	if mm.Item(m.OpenRow).Chosen() {
		ps.sel = sel
		ps.expanded[n.RelPath] = true
	}
	if mm.Item(m.NewRequestHere).Chosen() {
		a.newRequestInFolder(n.RelPath)
	}
	if mm.Item(m.Rename).Shortcut(0, ui.KeyF2).Chosen() {
		a.beginRename(sel)
	}
	self := collectionRelOf(n.RelPath)
	mm.Submenu(m.MoveTo, func(sm *ui.Menu) {
		if sm.Item(folderLabel("")).Chosen() {
			a.moveFolderTo(n, "")
		}
		for _, rel := range listFolderRels(ps.project.Collections, nil) {
			target := rel
			if isUnder(target, self) {
				continue // never into itself or a descendant
			}
			if sm.Item(folderLabel(target)).Chosen() {
				a.moveFolderTo(n, target)
			}
		}
	})
	mm.Separator()
	if mm.Item(m.Delete).Chosen() {
		a.deleteTreeRow(sel)
	}
}

// docMenu fills a document row's context menu (§3).
func (a *App) docMenu(mm *ui.Menu, d *core.DocEntry, sel string) {
	ps := a.ps
	m := a.msg()
	if mm.Item(m.OpenRow).Chosen() {
		ps.sel = sel
		if err := ps.openSelection(sel); err != nil {
			a.status = err.Error()
		}
	}
	if mm.Item(m.Rename).Shortcut(0, ui.KeyF2).Chosen() {
		a.beginRename(sel)
	}
	if mm.Item(m.DuplicateDoc).Chosen() {
		a.duplicateDoc(d)
	}
	mm.Separator()
	if mm.Item(m.Delete).Chosen() {
		a.deleteTreeRow(sel)
	}
}

// --- row actions ----------------------------------------------------------

// newRequestInFolder opens the new-request form targeting folderRel (a
// project-relative folder path, "" for the root).
func (a *App) newRequestInFolder(folderRel string) {
	ps := a.ps
	if ps == nil {
		return
	}
	a.formProject = ps
	a.newReqFolder = folderRel
	a.newReqName = ""
	a.formErr = ""
	a.newReqOpen = true
}

// duplicateRequest copies a request into the same folder under a new stable ID.
func (a *App) duplicateRequest(e *core.RequestEntry) {
	ps := a.ps
	if ps == nil || e == nil || e.Request == nil {
		return
	}
	m := a.msg()
	target := folderRelOfPath(e.RelPath)
	entry, err := ps.project.NewRequest(target, e.Request.Name+" copy")
	if err != nil {
		a.status = err.Error()
		return
	}
	copied := *e.Request
	copied.ID = entry.Request.ID
	copied.Name = entry.Request.Name
	if _, err := ps.project.SaveRequest(entry, &copied, entry.BaselineDigest); err != nil {
		a.status = err.Error()
		return
	}
	ps.expandChain(filepath.ToSlash(filepath.Dir(entry.RelPath)))
	ps.sel = "req:" + entry.Request.ID
	ps.openRequest(entry)
	a.status = translate(m.Created, entry.RelPath)
}

// duplicateDoc copies a document next to the original under a new file name,
// preserving its content. The new document gets its own path and baseline; the
// source is untouched.
func (a *App) duplicateDoc(d *core.DocEntry) {
	ps := a.ps
	if ps == nil || ps.project == nil || d == nil {
		return
	}
	m := a.msg()
	content, _, err := ps.project.Store.Read(d.RelPath)
	if err != nil {
		a.status = translate(m.NewDocFailed, err.Error())
		return
	}
	base := strings.TrimSuffix(d.Name, filepath.Ext(d.Name)) + " copy"
	de, err := ps.project.NewDoc(base)
	if err != nil {
		a.status = translate(m.NewDocFailed, err.Error())
		return
	}
	if _, err := ps.project.SaveDoc(de.RelPath, content, de.BaselineDigest); err != nil {
		_, _ = ps.project.DeleteDoc(de)
		a.status = translate(m.NewDocFailed, err.Error())
		return
	}
	ps.sel = "doc:" + de.RelPath
	if err := ps.openDoc(de.RelPath); err != nil {
		a.status = err.Error()
		return
	}
	a.status = translate(m.Created, de.RelPath)
}

// moveRequestTo moves a request into targetRel (collections-relative, "" for
// the root) and keeps any open tab's baseline in step.
func (a *App) moveRequestTo(e *core.RequestEntry, targetRel string) {
	ps := a.ps
	if ps == nil || e == nil {
		return
	}
	m := a.msg()
	if err := ps.project.MoveRequest(e, targetRel); err != nil {
		a.status = translate(m.MoveFailedPrefix, err.Error())
		return
	}
	if targetRel != "" {
		ps.expandChain("collections/" + targetRel)
	}
	for _, tab := range ps.tabs {
		if tab.Kind == "request" && tab.Entry == e {
			tab.Baseline = e.BaselineDigest
		}
	}
	ps.sel = "req:" + e.Request.ID
	a.status = translate(m.MovedStatus, e.RelPath)
}

// moveRequestByID moves the request with the given stable ID into targetRel.
func (a *App) moveRequestByID(id, targetRel string) {
	ps := a.ps
	if ps == nil {
		return
	}
	a.moveRequestTo(ps.project.Requests[id], targetRel)
}

// moveFolderTo reparents a folder under targetRel (collections-relative).
func (a *App) moveFolderTo(n *core.CollectionEntry, targetRel string) {
	ps := a.ps
	if ps == nil || n == nil {
		return
	}
	oldRel := n.RelPath
	moved, err := ps.project.MoveFolder(n, targetRel)
	if err != nil {
		a.status = err.Error()
		return
	}
	a.rekeyExpanded(ps, oldRel, moved.RelPath)
	ps.expandChain(moved.RelPath)
	ps.sel = "folder:" + moved.RelPath
	a.status = translate(a.msg().MovedStatus, moved.RelPath)
}

// deleteTreeRow deletes the selected request, folder or document immediately
// and offers undo for five seconds (§8).
func (a *App) deleteTreeRow(sel string) {
	ps := a.ps
	if ps == nil || ps.project == nil {
		return
	}
	m := a.msg()
	switch {
	case strings.HasPrefix(sel, "req:"):
		id := strings.TrimPrefix(sel, "req:")
		e := ps.project.Requests[id]
		if e == nil || e.Request == nil {
			return
		}
		name := e.Request.Name
		content, err := ps.project.DeleteRequest(e)
		if err != nil {
			a.status = err.Error()
			return
		}
		a.closeCleanTabs(ps, func(t *Tab) bool { return t.Kind == "request" && t.Entry == e })
		a.clearSelectionIfGone(ps)
		a.pushUndo(translate(m.DeletedToast, name), func() {
			if err := ps.project.RestoreRequest(e, content); err != nil {
				a.status = err.Error()
				return
			}
			ps.expandChain(filepath.Dir(e.RelPath))
			ps.sel = sel
			if a.win != nil {
				a.win.Update(func() {})
			}
		})
	case strings.HasPrefix(sel, "folder:"):
		rel := strings.TrimPrefix(sel, "folder:")
		n := findFolderInTree(ps, rel)
		if n == nil {
			return
		}
		name := n.Name
		count := countFolderRequests(n)
		snap, err := ps.project.DeleteFolder(n)
		if err != nil {
			a.status = err.Error()
			return
		}
		a.closeCleanTabs(ps, func(t *Tab) bool {
			return t.Kind == "request" && t.Entry != nil &&
				isUnder(filepath.ToSlash(filepath.Dir(t.Entry.RelPath)), rel)
		})
		a.clearSelectionIfGone(ps)
		desc := translate(m.DeletedToast, fmt.Sprintf("%s · %d %s", name, count, m.RequestsGroup))
		a.pushUndo(desc, func() {
			if err := ps.project.RestoreFolder(snap); err != nil {
				a.status = err.Error()
				return
			}
			ps.expandChain(filepath.Dir(rel))
			if a.win != nil {
				a.win.Update(func() {})
			}
		})
	case strings.HasPrefix(sel, "doc:"):
		rel := strings.TrimPrefix(sel, "doc:")
		d := findDocInProject(ps, rel)
		if d == nil {
			return
		}
		name := d.Name
		content, err := ps.project.DeleteDoc(d)
		if err != nil {
			a.status = err.Error()
			return
		}
		a.closeCleanTabs(ps, func(t *Tab) bool { return t.Kind == "doc" && t.DocRel == rel })
		a.clearSelectionIfGone(ps)
		a.pushUndo(translate(m.DeletedToast, name), func() {
			if err := ps.project.RestoreDoc(d, content); err != nil {
				a.status = err.Error()
				return
			}
			ps.sel = sel
			if a.win != nil {
				a.win.Update(func() {})
			}
		})
	}
}

// closeCleanTabs closes tabs matching pred that have no unsaved edits. Dirty
// tabs stay open so the user's edits are never discarded silently.
func (a *App) closeCleanTabs(ps *projectState, pred func(*Tab) bool) {
	for i := len(ps.tabs) - 1; i >= 0; i-- {
		tab := ps.tabs[i]
		if tab == nil || tab.Dirty || !pred(tab) {
			continue
		}
		ps.removeTab(tab)
	}
}

// clearSelectionIfGone resets the sidebar selection when the selected row no
// longer exists after a delete.
func (a *App) clearSelectionIfGone(ps *projectState) {
	sel := ps.sel
	switch {
	case strings.HasPrefix(sel, "req:"):
		if ps.project.Requests[strings.TrimPrefix(sel, "req:")] == nil {
			ps.sel = ""
		}
	case strings.HasPrefix(sel, "folder:"):
		if findFolderInTree(ps, strings.TrimPrefix(sel, "folder:")) == nil {
			ps.sel = ""
		}
	case strings.HasPrefix(sel, "doc:"):
		if findDocInProject(ps, strings.TrimPrefix(sel, "doc:")) == nil {
			ps.sel = ""
		}
	}
}
