package desktop

import (
	"context"
	"time"

	"github.com/egoist/mygo"

	"gorequest/internal/update"
)

// installerHandoff is a verified update package waiting to be opened. It lives
// on the Manager because the unsaved-edit guard is per project window while the
// installer launch is application-wide.
type installerHandoff struct {
	// launch opens the installer. It must only run once no window is dirty.
	launch func() error
	// launched/failed/canceled report the outcome on the UI thread.
	launched func()
	failed   func(error)
	canceled func()
}

// quit asks the application to quit through the normal lifecycle.
func (m *Manager) quit() {
	if m.quitApp != nil {
		m.quitApp()
		return
	}
	mygo.App.Quit()
}

// anyGuardActive reports whether a project window is showing its close guard.
func (m *Manager) anyGuardActive() bool {
	for _, app := range m.workspaces {
		if !app.closed && app.closeGuard != nil && app.closeGuard.active {
			return true
		}
	}
	return false
}

// beginHandoff must run on the UI thread. It reuses the quit guard: every dirty
// project is offered save / discard / cancel in turn, and only when all are
// clean is the installer launched (finishHandoff). Returns false when another
// handoff already waits.
func (m *Manager) beginHandoff(h *installerHandoff) bool {
	if m.handoff != nil {
		return false
	}
	m.handoff = h
	m.pendingQuit = true
	m.promptQuitGuard()
	return true
}

// finishHandoff runs on the UI thread right after the last dirty check, with
// no yield in between, so an edit made during download or while a guard was
// open cannot slip past. A launch failure never quits.
func (m *Manager) finishHandoff() {
	h := m.handoff
	m.handoff = nil
	m.pendingQuit = false
	if h == nil {
		return
	}
	if m.anyDirty() { // defensive: never launch over unsaved work
		h.canceled()
		return
	}
	if err := h.launch(); err != nil {
		h.failed(err)
		return
	}
	h.launched()
	m.quitting = true
	m.quit()
}

// reconcileHandoff cancels a waiting handoff once the guard is gone without
// having settled it (the user pressed Cancel or dismissed the modal). It runs
// every frame from updateModalFor, so no app.go change is needed.
func (m *Manager) reconcileHandoff() {
	if m == nil || m.handoff == nil || m.anyGuardActive() {
		return
	}
	h := m.handoff
	m.handoff = nil
	m.pendingQuit = false
	h.canceled()
}

// abandonHandoff drops a waiting handoff without opening the installer, e.g.
// when the user starts their own quit.
func (m *Manager) abandonHandoff() {
	if m.handoff == nil {
		return
	}
	h := m.handoff
	m.handoff = nil
	m.pendingQuit = false
	h.canceled()
}

// handOffInstaller runs on the UI thread after a verified download. The
// installer is opened only after the unsaved-edit guard passes.
func (u *updateController) handOffInstaller(ctx context.Context, dl *update.Downloaded) {
	if ctx.Err() != nil { // download canceled just as it finished
		u.closeDialog()
		return
	}
	fail := func(err error) {
		u.dialogKind = updateDialogResult
		u.result = updateResultError
		u.detail = err.Error()
		u.dialogOpen = true
		u.cancel = nil
	}
	h := &installerHandoff{
		launch: func() error { return u.open(dl.Path) },
		launched: func() {
			u.state.Mutate(func(st *update.State) {
				st.LastCheckedAt = u.now().UTC().Format(time.RFC3339)
				st.LastResult = update.ResultDownloaded
				st.LastError = ""
			})
			u.dialogKind = updateDialogResult
			u.result = updateResultDownloaded
			u.detail = dl.Path
			u.dialogOpen = true
			u.cancel = nil
		},
		failed: fail,
		canceled: func() {
			u.dialogKind = updateDialogResult
			u.result = updateResultHandoffCanceled
			u.detail = dl.Path
			u.dialogOpen = true
			u.cancel = nil
		},
	}
	if u.mgr == nil {
		if err := h.launch(); err != nil {
			h.failed(err)
			return
		}
		h.launched()
		return
	}
	u.cancel = nil
	if u.mgr.anyDirty() {
		u.dialogOpen = false // the save guard is the only modal meanwhile
	}
	if !u.mgr.beginHandoff(h) {
		fail(errHandoffBusy)
	}
}

var errHandoffBusy = handoffError("another update handoff is already waiting")

type handoffError string

func (e handoffError) Error() string { return string(e) }
