package uikit

import "github.com/egoist/mygo/ui"

// ActionCursor applies pointer feedback to an existing click target, without
// changing its geometry, input handling, focus or accessibility semantics.
// Apply it after Disabled when disabling the target itself. MyGo v0.7.4 does
// not clear an explicit cursor on disabled elements. Decorative children inherit
// this cursor; editors and nested click targets retain their own cursor.
func ActionCursor(e ui.Element) ui.Element {
	cursor := ui.CursorPointer
	if e.IsDisabled() {
		cursor = ui.CursorDefault
	}
	return e.Cursor(cursor)
}
