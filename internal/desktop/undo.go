package desktop

import (
	"time"
)

// undoEntry is one reversible action offered after a destructive operation (§8).
// Actions run immediately and the toast offers Undo for a short window instead
// of a blocking confirmation dialog.
type undoEntry struct {
	desc string
	at   time.Time
	fn   func()
}

// undoWindow is how long an undo stays available.
const undoWindow = 5 * time.Second

// pushUndo records a reversible action and advertises it in the status line.
func (a *App) pushUndo(desc string, fn func()) {
	a.undo = &undoEntry{desc: desc, at: time.Now(), fn: fn}
	a.status = desc
	a.win.Update(func() {})
}

// undoLast runs the pending undo, if it has not expired. It reports whether
// anything was undone.
func (a *App) undoLast() bool {
	e := a.undo
	if e == nil {
		return false
	}
	if time.Since(e.at) > undoWindow {
		a.undo = nil
		return false
	}
	a.undo = nil
	e.fn()
	a.status = ""
	return true
}

// expireUndo drops an undo whose window closed, so ⌘Z cannot resurrect
// something long deleted.
func (a *App) expireUndo() {
	if a.undo != nil && time.Since(a.undo.at) > undoWindow {
		a.undo = nil
	}
}
