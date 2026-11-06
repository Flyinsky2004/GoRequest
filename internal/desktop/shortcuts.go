package desktop

import "github.com/egoist/mygo/ui"

// registerShortcuts wires the window-level shortcuts of §9. Feature-specific
// behavior lives in the files that own it: tabs in tab_bar.go/tab_actions.go,
// the tree in tree_actions.go, KV in request_form.go, undo in undo.go. This file
// only binds keys to those entry points, so it stays a single writer and never
// conflicts with feature work.
func (a *App) registerShortcuts(c *ui.Context) {
	// New request tab.
	c.OnShortcut(ui.Cmd, ui.KeyT, func() { a.openNewRequest() })
	// Close current tab (dirty tabs still go through the save/discard flow).
	c.OnShortcut(ui.Cmd, ui.KeyW, func() { a.closeCurrent() })
	// Switch tabs: ⌘1…⌘9 (⌘9 = last).
	tabKeys := []ui.Key{ui.Key1, ui.Key2, ui.Key3, ui.Key4, ui.Key5, ui.Key6, ui.Key7, ui.Key8, ui.Key9}
	for i, k := range tabKeys {
		n := i + 1
		c.OnShortcut(ui.Cmd, k, func() { a.selectTabIndex(n) })
	}
	// Focus the URL input and select it.
	c.OnShortcut(ui.Cmd, ui.KeyL, func() { a.focusActiveURL() })
	// Quick open: the sidebar filter is the searchable entry point today.
	c.OnShortcut(ui.Cmd, ui.KeyK, func() {
		a.historyView = false
		a.filterFocus = true
	})
	// Environment: edit the current one, else create.
	c.OnShortcut(ui.Cmd, ui.KeyE, func() {
		if a.ps == nil {
			return
		}
		if a.ps.canEditEnv() {
			a.openEditEnvironment()
		} else {
			a.openNewEnvironment()
		}
	})
	// Toggle the sidebar.
	c.OnShortcut(ui.Cmd, ui.KeyBackslash, func() { a.sidebarHidden = !a.sidebarHidden })
	// Undo the last delete.
	c.OnShortcut(ui.Cmd, ui.KeyZ, func() { a.undoLast() })
	// Rename the selected tree row in place.
	c.OnShortcut(0, ui.KeyF2, func() { a.beginRenameTreeRow() })
}

// selectTabIndex activates the n-th tab (1-based), clamping ⌘9 to the last tab.
func (a *App) selectTabIndex(n int) {
	ps := a.ps
	if ps == nil || len(ps.tabs) == 0 {
		return
	}
	i := n - 1
	if n == 9 {
		i = len(ps.tabs) - 1
	}
	if i < 0 || i >= len(ps.tabs) {
		return
	}
	ps.active = i
}

// focusActiveURL puts the caret in the active request's URL field.
func (a *App) focusActiveURL() {
	if a.ps == nil {
		return
	}
	if tab := a.ps.activeTab(); tab != nil && tab.Kind == "request" {
		tab.focusURL = true
	}
}
