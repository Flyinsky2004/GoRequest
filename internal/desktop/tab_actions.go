package desktop

import (
	"strings"

	"gorequest/internal/core"
)

// This file holds the tab lifecycle logic: identity, activation, closing
// (including dirty Save/Discard/Cancel decisions), batch close, and cancelling
// in-flight requests. It is kept free of view code so the rules can be unit
// tested directly.

// tabIdentity returns a stable key for a tab: the request's stable ID or the
// document's relative path. Closing or rearranging tabs never depends on the
// slice index alone.
func tabIdentity(tab *Tab) string {
	if tab == nil {
		return ""
	}
	switch tab.Kind {
	case "request":
		if tab.Entry != nil && tab.Entry.Request != nil {
			return "req:" + tab.Entry.Request.ID
		}
		return "req:?"
	case "doc":
		return "doc:" + tab.DocRel
	}
	return tab.Kind
}

// indexOfTab returns the position of tab in the project's tab list, or -1.
func (ps *projectState) indexOfTab(tab *Tab) int {
	for i, t := range ps.tabs {
		if t == tab {
			return i
		}
	}
	return -1
}

// removeTab removes tab from the project. An in-flight request is cancelled so
// its context is released; the tab pointer may still receive the async result,
// but that callback only touches its own (now detached) tab, never another tab
// or the project. The active selection keeps the same tab identity when the
// removed tab was not the active one, and moves to a neighbour when it was.
func (ps *projectState) removeTab(tab *Tab) bool {
	i := ps.indexOfTab(tab)
	if i < 0 {
		return false
	}
	active := ps.activeTab()
	if tab.Sending && tab.cancel != nil {
		tab.cancel()
		tab.cancel = nil
		tab.Sending = false
	}
	kept := make([]*Tab, 0, len(ps.tabs)-1)
	kept = append(kept, ps.tabs[:i]...)
	kept = append(kept, ps.tabs[i+1:]...)
	ps.tabs = kept

	switch {
	case len(ps.tabs) == 0:
		ps.active = 0
	case tab == active:
		// Neighbour: the tab that shifted into i, else the last one.
		if i >= len(ps.tabs) {
			i = len(ps.tabs) - 1
		}
		ps.active = i
	case active != nil:
		ps.active = ps.indexOfTab(active)
		if ps.active < 0 {
			ps.active = 0
		}
	default:
		ps.active = 0
	}
	return true
}

// closeFlow is an in-progress close of one or more tabs. Clean tabs close as the
// flow advances; a dirty tab pauses the flow until the user chooses Save,
// Discard or Cancel.
type closeFlow struct {
	project *projectState
	queue   []*Tab
	dirty   *Tab
	err     string
}

// startClose begins closing the given tabs of ps. Nil and already-closed tabs
// are ignored.
func (a *App) startClose(ps *projectState, targets []*Tab) {
	if ps == nil {
		return
	}
	q := make([]*Tab, 0, len(targets))
	for _, t := range targets {
		if t != nil && ps.indexOfTab(t) >= 0 {
			q = append(q, t)
		}
	}
	if len(q) == 0 {
		return
	}
	a.closeFlow = &closeFlow{project: ps, queue: q}
	a.advanceClose()
}

// advanceClose closes clean tabs until a dirty one needs a decision, then stops.
func (a *App) advanceClose() {
	f := a.closeFlow
	if f == nil {
		return
	}
	ps := f.project
	for len(f.queue) > 0 {
		t := f.queue[0]
		f.queue = f.queue[1:]
		if ps.indexOfTab(t) < 0 {
			continue
		}
		if t.Dirty {
			f.dirty = t
			f.err = ""
			return
		}
		ps.removeTab(t)
	}
	a.closeFlow = nil
}

// resolveCloseDirty applies Save (save then close) or Discard to the dirty tab
// currently awaiting a decision. A failed save keeps the tab open and the dialog
// visible so no input is lost.
func (a *App) resolveCloseDirty(save bool) {
	f := a.closeFlow
	if f == nil || f.dirty == nil {
		return
	}
	t := f.dirty
	if save {
		if err := f.project.saveTab(t); err != nil {
			f.err = translate(a.msg().SaveFailedPrefix, err.Error())
			return
		}
	}
	f.dirty = nil
	f.err = ""
	f.project.removeTab(t)
	a.advanceClose()
}

// cancelClose abandons the remaining close queue. Tabs already closed stay
// closed; every tab not yet decided stays open with its content intact.
func (a *App) cancelClose() { a.closeFlow = nil }

// closeCurrent closes the active tab of the current project.
func (a *App) closeCurrent() {
	ps := a.ps
	if ps == nil {
		return
	}
	if t := ps.activeTab(); t != nil {
		a.startClose(ps, []*Tab{t})
	}
}

// closeOthers closes every tab of the current project except the active one.
func (a *App) closeOthers() {
	ps := a.ps
	if ps == nil {
		return
	}
	active := ps.activeTab()
	targets := make([]*Tab, 0, len(ps.tabs))
	for _, t := range ps.tabs {
		if t != active {
			targets = append(targets, t)
		}
	}
	a.startClose(ps, targets)
}

// closeAll closes every tab of the current project.
func (a *App) closeAll() {
	ps := a.ps
	if ps == nil {
		return
	}
	targets := make([]*Tab, len(ps.tabs))
	copy(targets, ps.tabs)
	a.startClose(ps, targets)
}

// --- rename --------------------------------------------------------------

// beginTabRename starts in-place renaming of a request tab (§4). The draft
// begins at the current title so the user edits it rather than retyping, and
// only one tab can be renaming at a time. Documents are left alone: their title
// is the file name, and there is no Core operation that renames the file.
func beginTabRename(ps *projectState, tab *Tab) {
	if ps == nil || tab == nil || tab.Kind != "request" {
		return
	}
	for _, other := range ps.tabs {
		if other != tab {
			other.renaming = false
			other.renameText = ""
		}
	}
	ps.activateTab(tab)
	tab.renaming = true
	tab.renameText = tab.Title
}

// cancelTabRename closes the editor without touching the title (Esc).
func cancelTabRename(tab *Tab) {
	if tab == nil {
		return
	}
	tab.renaming = false
	tab.renameText = ""
}

// commitTabRename applies the draft title (Enter). A blank draft, or one that
// matches the current title, keeps the old title and leaves the tab clean.
// Otherwise the tab is marked dirty so the rename goes through the normal
// save/close protection.
func commitTabRename(tab *Tab) {
	if tab == nil {
		return
	}
	tab.renaming = false
	title := strings.TrimSpace(tab.renameText)
	tab.renameText = ""
	if title == "" || title == tab.Title {
		return
	}
	tab.Title = title
	syncTabTitleToRequest(tab)
	tab.Dirty = true
}

// syncTabTitleToRequest copies a committed tab title into the request model.
// requestFromTab (tab.go) rebuilds the request from the tab's fields but does
// not copy Tab.Title into Request.Name, and SaveRequest persists whatever that
// request holds, so without this the rename would vanish on save. This is the
// smallest implementation that keeps the title on the save path; it lives here
// because tab.go is outside this task's file scope.
func syncTabTitleToRequest(tab *Tab) {
	if tab == nil || tab.Kind != "request" || tab.Entry == nil || tab.Entry.Request == nil {
		return
	}
	tab.Entry.Request.Name = tab.Title
}

// --- move ----------------------------------------------------------------

// folderChoices lists selectable move destinations for p: "(root)" followed by
// every folder as a collections-relative path, in tree order.
func folderChoices(p *core.Project) []string {
	out := []string{"(root)"}
	var walk func(nodes []*core.CollectionEntry)
	walk = func(nodes []*core.CollectionEntry) {
		for _, n := range nodes {
			out = append(out, collectionRelOf(n.RelPath))
			walk(n.Children)
		}
	}
	if p != nil {
		walk(p.Collections)
	}
	return out
}

// folderChoiceToRel converts a move choice back to a project-relative folder
// path ("" for the root).
func folderChoiceToRel(choice string) string {
	if choice == "" || choice == "(root)" {
		return ""
	}
	return "collections/" + choice
}

// doMove moves the pending request tab to the chosen folder. When save is set
// the tab is saved first; when discard is set the edits are dropped first. A
// save or move failure keeps the dialog open and the input intact.
func (a *App) doMove(save, discard bool) {
	ps := a.moveProject
	tab := a.moveTab
	if ps == nil || tab == nil {
		a.moveOpen = false
		return
	}
	m := a.msg()
	if save {
		if err := ps.saveTab(tab); err != nil {
			a.moveErr = translate(m.SaveFailedPrefix, err.Error())
			return
		}
	}
	if discard {
		ps.reloadRequestTab(tab)
	}
	folderRel := folderChoiceToRel(a.moveTarget)
	if err := ps.moveRequest(tab, folderRel); err != nil {
		a.moveErr = translate(m.MoveFailedPrefix, err.Error())
		return
	}
	a.moveOpen = false
	a.moveErr = ""
	rel := ""
	if tab.Entry != nil {
		rel = tab.Entry.RelPath
	}
	a.status = translate(m.MovedStatus, rel)
}
