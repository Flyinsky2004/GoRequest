package desktop

// This file owns the sidebar tree's interactive actions (§3): the row context
// menu, in-place rename, drag reordering and undoable deletes. It is declared
// here so the window-level shortcut table (shortcuts.go) can call into it.

// beginRenameTreeRow starts in-place renaming of the selected tree row, if one
// is selected. (Implemented with the tree context menu.)
func (a *App) beginRenameTreeRow() {}
