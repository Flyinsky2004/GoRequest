package desktop

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"

	"gorequest/internal/core"
	"gorequest/internal/update"
)

// installerHandoff is a verified update package waiting to be opened. It lives
// on the Manager because the unsaved-edit guard is per project window while the
// installer launch is application-wide.
//
// Choosing "Discard" on a window during an update handoff does not clear its
// Dirty flags up front: instead it records the window's current dirty-content
// fingerprint in approved. If a subsequent window cancels or the installer
// fails to launch, dropping the handoff leaves every window's Dirty flags and
// in-memory drafts intact. If an approved window is edited again while another
// window's guard is open, its fingerprint no longer matches and it is prompted
// again before the installer can launch.
type installerHandoff struct {
	// launch opens the installer. It must only run once no window has
	// unapproved dirty work.
	launch func() error
	// launched/failed/canceled report the outcome on the UI thread.
	launched func()
	failed   func(error)
	canceled func()

	approved map[*App]windowDirtyFingerprint
	preGuard map[*App]*preGuardSnapshot
}

type windowDirtyFingerprint struct {
	tabs   map[*Tab]string
	envSig string
}

// preGuardSnapshot lets onQuitGuardSettled preserve Dirty flags even before the
// app.go resolveCloseGuard patch is integrated. Once app.go calls
// tryResolveHandoffGuard directly, preGuard is simply cleared unused.
type preGuardSnapshot struct {
	tabs       map[*Tab]preGuardTab
	envOpen    bool
	envVisible bool
	envID      string
	envPtr     *core.Environment
}

type preGuardTab struct {
	baseline string
	reqPtr   *core.Request
}

func captureDirtyFingerprint(app *App) windowDirtyFingerprint {
	fp := windowDirtyFingerprint{tabs: make(map[*Tab]string)}
	if app == nil || app.ps == nil {
		return fp
	}
	for _, tab := range app.ps.tabs {
		if tab != nil && tab.Dirty {
			fp.tabs[tab] = tabDraftFingerprint(app.ps, tab)
		}
	}
	if e := app.ps.envUI; e != nil && e.open && e.dirty() {
		fp.envSig = "open:" + e.signature()
	}
	return fp
}

func tabDraftFingerprint(ps *projectState, tab *Tab) string {
	if tab == nil {
		return ""
	}
	if tab.flushDocEdit != nil {
		tab.flushDocEdit()
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%s|%s|%d|%s|%t|%t|%s|%t|%s|%s|%d|%s|%s|%s|%s|%s|%s|%d|%s|%s|",
		tab.Kind, tab.Title, tab.Baseline, tab.Method, tab.URL,
		tab.BodyIdx, tab.BodyText,
		tab.queryText, tab.queryRawEncoded, tab.queryRaw,
		tab.headerText, tab.headerRaw,
		tab.AuthMode, tab.AuthIdx, tab.AuthRefID, tab.AuthToken,
		tab.AuthUser, tab.AuthPass, tab.AuthKeyName, tab.AuthKeyIn,
		tab.AuthKeyInIdx, tab.AuthKeyValue, tab.Timeout,
	)
	for _, r := range tab.QueryRows {
		if r != nil {
			fmt.Fprintf(&b, "q:%t:%s=%s;", r.Enabled, r.Name, r.Value)
		}
	}
	for _, r := range tab.HeaderRows {
		if r != nil {
			fmt.Fprintf(&b, "h:%t:%s=%s;", r.Enabled, r.Name, r.Value)
		}
	}
	if tab.bodyEditor != nil && tab.bodyEditor.Buffer != nil {
		b.WriteString("|be:")
		b.WriteString(tab.bodyEditor.Buffer.String())
	}
	if tab.DocEditor != nil && tab.DocEditor.Buffer != nil {
		b.WriteString("|de:")
		b.WriteString(tab.DocEditor.Buffer.String())
	}
	if ps != nil && tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
		if req, err := requestForSaveFromTab(ps, tab); err == nil && req != nil {
			fmt.Fprintf(&b, "|req:%s:%s", req.Name, req.Protocol)
		}
	}
	return b.String()
}

func (h *installerHandoff) approveWindow(app *App) {
	if h == nil || app == nil {
		return
	}
	if h.approved == nil {
		h.approved = make(map[*App]windowDirtyFingerprint)
	}
	h.approved[app] = captureDirtyFingerprint(app)
}

func (h *installerHandoff) isApproved(app *App) bool {
	if h == nil || app == nil || app.ps == nil {
		return false
	}
	prev, ok := h.approved[app]
	if !ok {
		return false
	}
	cur := captureDirtyFingerprint(app)
	if cur.envSig != prev.envSig || len(cur.tabs) != len(prev.tabs) {
		return false
	}
	for tab, sig := range cur.tabs {
		if prevSig, exists := prev.tabs[tab]; !exists || prevSig != sig {
			return false
		}
	}
	return true
}

func (h *installerHandoff) capturePreGuard(app *App) {
	if h == nil || app == nil || app.ps == nil {
		return
	}
	if h.preGuard == nil {
		h.preGuard = make(map[*App]*preGuardSnapshot)
	}
	snap := &preGuardSnapshot{tabs: make(map[*Tab]preGuardTab)}
	for _, tab := range app.ps.tabs {
		if tab != nil && tab.Dirty {
			var reqPtr *core.Request
			if tab.Entry != nil {
				reqPtr = tab.Entry.Request
			}
			snap.tabs[tab] = preGuardTab{
				baseline: tab.Baseline,
				reqPtr:   reqPtr,
			}
		}
	}
	if e := app.ps.envUI; e != nil && e.open && e.dirty() {
		snap.envOpen = true
		snap.envVisible = e.visible
		id := e.envID
		if e.mode == "new" {
			id = strings.TrimSpace(e.idText)
		}
		snap.envID = id
		if app.ps.project != nil {
			snap.envPtr = app.ps.project.Environments[id]
		}
	}
	h.preGuard[app] = snap
}

func (h *installerHandoff) settleCompat(app *App) {
	if h == nil || app == nil || app.ps == nil {
		return
	}
	snap := h.preGuard[app]
	delete(h.preGuard, app)
	if snap == nil {
		return
	}
	// If tryResolveHandoffGuard was not yet wired into app.go,
	// resolveCloseGuard(true) called clearDirty() before onQuitGuardSettled.
	// Detect whether saveAllDirty() actually saved or clearDirty() discarded.
	saved := false
	for tab, pre := range snap.tabs {
		if tab.Baseline != pre.baseline {
			saved = true
			break
		}
		if tab.Entry != nil && tab.Entry.Request != pre.reqPtr {
			saved = true
			break
		}
	}
	if !saved && snap.envOpen && app.ps.project != nil {
		if app.ps.project.Environments[snap.envID] != snap.envPtr {
			saved = true
		}
	}
	if saved {
		delete(h.approved, app)
		return
	}
	// Restore the Dirty markers that clearDirty() cleared and record per-window
	// discard approval instead, so Dirty stays intact until launch succeeds.
	for tab := range snap.tabs {
		tab.Dirty = true
	}
	if snap.envOpen && app.ps.envUI != nil {
		app.ps.envUI.open = true
		app.ps.envUI.visible = snap.envVisible
	}
	h.approveWindow(app)
}

// tryResolveHandoffGuard is the Manager entry point for app.go's
// resolveCloseGuard when an update handoff is active. Save still runs
// saveAllDirty(); Discard records the window's dirty fingerprint in the
// handoff without clearing Dirty until the installer launches.
func (m *Manager) tryResolveHandoffGuard(app *App, discard bool) bool {
	if m == nil || m.handoff == nil || app == nil || app.closeGuard == nil || !app.closeGuard.active {
		return false
	}
	if !m.pendingQuit {
		m.abandonHandoff()
		return false
	}
	delete(m.handoff.preGuard, app)
	if !discard {
		if err := app.saveAllDirty(); err != nil {
			app.closeGuard.errMsg = err.Error()
			return true
		}
		delete(m.handoff.approved, app)
	} else {
		m.handoff.approveWindow(app)
	}
	app.closeGuard.active = false
	m.promptQuitGuard()
	return true
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

// hasUnapprovedDirtyForHandoff reports whether any open project has unsaved
// edits that have not been approved for discard at their current fingerprint.
func (m *Manager) hasUnapprovedDirtyForHandoff(h *installerHandoff) bool {
	for _, app := range m.workspaces {
		if !app.closed && app.ps.hasDirty() && !h.isApproved(app) {
			return true
		}
	}
	return false
}

// beginHandoff must run on the UI thread. It reuses the quit guard: every dirty
// project is offered save / discard / cancel in turn, and only when all are
// saved or approved at their current content fingerprint is the installer
// launched (finishHandoff). Returns false when another handoff already waits.
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
// open cannot slip past. A launch failure never clears Dirty or quits.
func (m *Manager) finishHandoff() {
	h := m.handoff
	m.handoff = nil
	m.pendingQuit = false
	if h == nil {
		return
	}
	if m.hasUnapprovedDirtyForHandoff(h) { // defensive: never launch over unapproved edits
		h.canceled()
		return
	}
	if err := h.launch(); err != nil {
		h.failed(err)
		return
	}
	for app := range h.approved {
		if app != nil && !app.closed && app.ps != nil {
			app.clearDirty()
		}
	}
	h.launched()
	m.quitting = true
	m.quit()
}

// reconcileHandoff cancels a waiting handoff once the guard is gone without
// having settled it (the user pressed Cancel or dismissed the modal). It runs
// every frame from updateModalFor, so it also works before app.go is patched.
func (m *Manager) reconcileHandoff() {
	if m == nil || m.handoff == nil {
		return
	}
	if m.pendingQuit && m.anyGuardActive() {
		return
	}
	m.abandonHandoff()
}

// abandonHandoff drops a waiting handoff without opening the installer or
// clearing Dirty on any previously approved window.
func (m *Manager) abandonHandoff() {
	if m == nil || m.handoff == nil {
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
