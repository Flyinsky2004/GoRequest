package desktop

import (
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
			f.err = "save: " + err.Error()
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
	if save {
		if err := ps.saveTab(tab); err != nil {
			a.moveErr = "save: " + err.Error()
			return
		}
	}
	if discard {
		ps.reloadRequestTab(tab)
	}
	folderRel := folderChoiceToRel(a.moveTarget)
	if err := ps.moveRequest(tab, folderRel); err != nil {
		a.moveErr = "move: " + err.Error()
		return
	}
	a.moveOpen = false
	a.moveErr = ""
	rel := ""
	if tab.Entry != nil {
		rel = tab.Entry.RelPath
	}
	a.status = "moved " + rel
}
