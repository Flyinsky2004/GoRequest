// Package desktop builds the native MyGo user interface for GoRequest. It
// holds only views and interaction state; all request, variable, auth and
// execution logic lives in the pure Go core package.
package desktop

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

var (
	methods   = []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	bodyTypes = []string{"json", "text", "xml"}
	authModes = []string{"none", "bearer", "basic"}
)

// closeGuard holds the save/discard/cancel prompt shown when a project window
// or the whole application is closing with unsaved edits.
type closeGuard struct {
	active bool
	quit   bool // true when this guard is part of an application quit
	errMsg string
}

// App is one project work window: it owns exactly one projectState (tabs,
// unsaved edits, selection, environment) and one native window. Multiple
// projects are separate App instances managed by Manager, so switching or
// closing one never discards another's edits, and a background request result
// is posted to the window that started it.
type App struct {
	git      *gitController
	gitView  bool
	mgr      *Manager
	win      window
	ps       *projectState
	navWidth float32

	// navScroll keeps the sidebar tree's scroll position so the hard scroll edge
	// can show while content is scrolled under the rail header.
	navScroll ui.ScrollState

	status       string
	showSettings bool
	closeGuard   *closeGuard

	// sendGuard holds a pending "send with undefined variables?" confirmation.
	sendGuard *sendGuard

	// undo is the pending reversible action (§8).
	undo *undoEntry
	// sidebarHidden folds the whole rail (§9 ⌘\).
	sidebarHidden bool

	// History view (§3): whether the sidebar shows history, the loaded records
	// for the current project, and the clear-confirmation state.
	historyView      bool
	historyRecords   []historyRecord
	historyKey       string
	historyClearOpen bool
	// historyUI holds this window's filters, diagnostics and snapshot preview.
	// It is one owned field so no package-level map carries window state.
	historyUI historyUIState

	// filterQuery is the live text of the sidebar filter box. It is view state
	// of this window, independent of any project's stored data.
	filterQuery string
	// filterFocus asks the next frame to focus the sidebar filter (§9 ⌘F when
	// no response is in view).
	filterFocus bool

	// closed and bypassGuard keep close handling honest: closed means the
	// window is gone (no more UI updates), bypassGuard lets the second Close
	// after a resolved guard actually destroy the window.
	closed      bool
	bypassGuard bool

	// Tab close flow (Save/Discard/Cancel for dirty tabs) and the folder/request
	// dialogs. They act on a captured project so switching projects mid-dialog
	// cannot redirect an action to the wrong project.
	closeFlow *closeFlow
	// closeModalOpen backs the dirty-close Modal so Escape/outside-click is a
	// real dismissal (treated as cancelling the remaining close queue).
	closeModalOpen bool

	newReqOpen      bool
	newReqName      string
	newReqFolder    string
	newFolderOpen   bool
	newFolderName   string
	newFolderParent string
	formProject     *projectState
	formErr         string

	moveOpen    bool
	moveTab     *Tab
	moveProject *projectState
	moveTarget  string
	moveErr     string

	// isGlobal marks the personal/scratch workspace window opened from the OS
	// application-data directory. It is never written to the recent-project
	// list and offers project transfers instead of being a project itself.
	isGlobal bool

	// transfer is the pending cross-workspace copy/move preview (B02).
	transfer *transferDraft

	// autoSaveStop stops the safe auto-save ticker when the window closes;
	// autoSaveLast is the UI-thread clock of the last attempt.
	autoSaveStop chan struct{}
	autoSaveLast time.Time

	// themeGen counts resolved light/dark appearance changes, and themeDark is
	// the mode it last resolved. Cached editor states record the generation
	// they were themed with, so a theme switch refreshes only their Theme
	// instead of rebuilding the tab or dropping the user's edits.
	themeGen  int
	themeDark bool

	// feedbackPending is the next action confirmation toast (feedback.go);
	// inView suppresses redundant win.Update wakeups while View is running.
	feedbackPending *feedbackToast
	inView          bool
}

// wire connects the project window's lifecycle to the manager and the close
// guard.
func (a *App) wire() {
	a.win.OnClose(func(e *mygo.CloseEvent) {
		a.saveGeometry()
		if a.bypassGuard || a.mgr.quitting {
			return
		}
		flushAppDocEdits(a)
		if a.ps.hasDirty() {
			e.PreventDefault()
			a.openCloseGuard(false)
		}
	})
	a.win.OnClosed(func() {
		a.stopAutoSaveTicker()
		a.mgr.onWorkspaceClosed(a)
	})
	// Only a real native window owns a UI queue that can drive the timer; the
	// headless fake windows used by tests never start a background goroutine.
	if nw, ok := a.win.(nativeWindower); ok && nw.nativeWindow() != nil {
		a.startAutoSaveTicker()
	}
}

// saveGeometry remembers the window size and sidebar width so the workbench
// reopens where the user left it. Fake windows in tests do not expose a size, so
// only the real window's geometry is saved there.
func (a *App) saveGeometry() {
	if sz, ok := a.win.(interface{ Size() (int, int) }); ok {
		if w, h := sz.Size(); w > 0 && h > 0 {
			a.mgr.cfg.setWindowSize(w, h)
		}
	}
	a.mgr.cfg.setNavWidth(a.navWidth)
}

// View builds one frame of the project window.
func (a *App) View(c *ui.Context) {
	a.inView = true
	defer func() { a.inView = false }()
	// Input may land after the last document frame, then switch to a request.
	// Commit it before building any tab so the header, save and sync see it.
	if a.ps != nil {
		for _, tab := range a.ps.tabs {
			if tab.flushDocEdit != nil {
				tab.flushDocEdit()
			}
		}
	}
	a.applyTheme(c)
	t := c.Theme()
	a.registerShortcuts(c)
	// The rail keeps its workspace range (220–380 DIPs) even while dragged.
	if a.navWidth < 220 {
		a.navWidth = 220
	} else if a.navWidth > 380 {
		a.navWidth = 380
	}
	// §9: ⌘F searches the response when one is shown, else focuses the sidebar
	// filter. (The response editor's own ⌘F covers the focused-editor case.)
	c.OnShortcut(ui.Cmd, ui.KeyF, func() {
		if ps := a.ps; ps != nil {
			if tab := ps.activeTab(); tab != nil && tab.Kind == "request" &&
				tab.Resp != nil && tab.RespSection == 0 {
				tab.respFindOpen = true
				tab.respFindFocus = true
				return
			}
		}
		a.filterFocus = true
	})
	ui.Column(c).Fill().Children(func() {
		if a.sidebarHidden {
			a.workspace(c, t)
		} else {
			ui.Split(c, &a.navWidth, func() { a.nav(c, t) }, func() { a.workspace(c, t) }).Grow(1).FillWidth()
		}
		if a.status != "" {
			ui.Row(c).Padding(5, 12).Clip().BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		}
	})
	a.expireUndo()
	a.envEditorModal(c, t)
	a.settingsModal(c, t)
	// The manager-level update dialog sits above the Settings sheet it can be
	// opened from, but below the close/send guards so a critical confirmation
	// is never hidden by an update prompt.
	a.mgr.updateModalFor(a.win, c, t)
	a.closeGuardModal(c, t)
	a.sendGuardModal(c, t)
	a.historyClearModal(c, t)
	a.historyDetailModal(c, t)
	a.formsModals(c, t)
	a.transferModal(c, t)
	a.b03CustomMethodModal(c, t)
	a.InteropModals(c, t)
	a.SnippetModals(c, t)
	a.SDKModals(c, t)
	a.OpenAPIModals(c, t)
	a.RunnerModals(c, t)
	a.MockModals(c, t)
	a.PerformanceModals(c, t)
	a.IdentityManagementModals(c, t)
	if a.git != nil {
		a.git.modals(c, t)
	}
	a.drainFeedback(c)
	a.renderFeedbackViewport(c, t)
}

func (a *App) settingsModal(c *ui.Context, t *ui.Theme) {
	renderSettingsWithUpdates(c, t, a.mgr.cfg, a.mgr.workspacePrefs(), a.mgr.update, a.win,
		&a.showSettings, func(s string) { a.status = s })
}

// --- actions -------------------------------------------------------------

// openProjectPicker opens the native chooser from this workspace.
func (a *App) openProjectPicker() { a.mgr.pickProject(a.win) }

// switchProject brings the project chooser forward so the user can change
// projects without quitting.
func (a *App) switchProject() { a.mgr.showWelcome("") }

// closeWindow closes this project window through the normal close path, so the
// dirty guard runs first; if it is the last project the manager returns to the
// chooser.
func (a *App) closeWindow() {
	if a.win != nil && !a.win.IsDestroyed() {
		a.win.Close()
	}
}

// saveActive saves the active tab of this project, reporting conflicts rather
// than overwriting external edits.
func (a *App) saveActive() {
	a.saveActiveWithFeedback()
}

// saveAllDirty saves every dirty tab of this project; the first failure stops
// the sequence so a close cannot proceed past a failed or conflicting save.
func (a *App) saveAllDirty() error {
	if s := a.suiteEditorState(false); s != nil && s.dirty() {
		a.SaveSuiteEditor()
		if s.err != "" {
			return fmt.Errorf("Suite: %s", s.err)
		}
		if s.dirty() {
			return fmt.Errorf("Suite: %s", a.suiteEditorMsg().InvalidDraft)
		}
	}
	if e := a.ps.envUI; e != nil && e.open && e.dirty() {
		if err := a.saveEnvironmentDraft(a.ps); err != nil {
			return fmt.Errorf("environment: %w", err)
		}
	}
	for _, tab := range a.ps.tabs {
		if !tab.Dirty {
			continue
		}
		if err := a.saveTabSynced(tab); err != nil {
			return fmt.Errorf("%s: %w", tab.Title, err)
		}
	}
	return nil
}

func (a *App) clearDirty() {
	if s := a.suiteEditorState(false); s != nil {
		s.reset()
	}
	if a.ps.envUI != nil {
		a.ps.envUI.close()
	}
	for _, tab := range a.ps.tabs {
		tab.Dirty = false
		if tab.DocEditor != nil && tab.DocEditor.Buffer != nil {
			tab.DocEditor.Buffer.MarkSaved()
		}
	}
}

// send executes the tab's request on a background goroutine. The result is
// posted through THIS window's Update, and is dropped if the window was closed
// meanwhile, so a result can never land in another project's window.
func (a *App) send(tab *Tab) {
	ps := a.ps
	if tab == nil || tab.Sending {
		return
	}
	if isWebSocketTab(tab) {
		// WebSocket tabs are driven by internal/websocket.Session; they must
		// never be sent through the HTTP executor.
		return
	}
	if tab.Owner != nil {
		ps = tab.Owner
	}
	if err := a.CanSendTab(tab); err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	confirmed := a.prepareConfirmedSend(tab)
	if confirmed == nil && identityMgmtFor(ps).policy != nil {
		cancel()
		return
	}
	req, plan, red, err := a.buildTrackedTabPlan(ctx, tab)
	if err != nil {
		cancel()
		a.status = translate(a.msg().RequestFailedPrefix, red.RedactError(err).Error())
		return
	}
	a.attachNetworkToPlan(tab, req, plan, red)
	a.attachConfirmedNetworkOptions(tab, plan, confirmed)

	// A newer send always supersedes the tab's previous stream; its callbacks
	// become no-ops and cannot write into this response. Cancel any request the
	// tab still holds (for example after a forced reset) so no connection
	// outlives the send that replaced it.
	if tab.cancel != nil {
		tab.cancel()
		tab.cancel = nil
	}
	teardownTabStream(tab)
	tab.cancel = cancel
	tab.sendSeq++
	seq := tab.sendSeq
	win := a.win
	stream := newTabSSEStream(a, tab, win, seq)
	if plan != nil && plan.Network != nil {
		plan.Network.Stream = stream.handler()
	}
	tab.sse = stream
	tab.Sending = true
	tab.RespErr = ""
	started := time.Now()
	a.status = translate(a.msg().Sending, req.HTTP.Method, red.SanitizePlan(plan).URL)

	go func() {
		res, err := core.Execute(ctx, plan, nil)
		// Drop the result if the owning window is gone. IsDestroyed is the
		// race-free check; closed is only touched on the UI thread.
		if win == nil || win.IsDestroyed() {
			return
		}
		win.Update(func() {
			if a.closed || win.IsDestroyed() || ps.indexOfTab(tab) < 0 {
				return
			}
			// Only the newest send may finish the tab. A forced reset (window
			// teardown) or a superseding send leaves this callback stale.
			if tab.sendSeq != seq {
				return
			}
			// Apply any events still buffered before deciding how the send ended.
			stream.flush()
			tab.Sending = false
			lm := a.layoutMsg()
			if stream.opened {
				a.finishStreamSend(tab, stream, res, err, plan, red, ctx, started, lm)
				tab.focusURL = true
				return
			}
			tab.sse = nil
			stream.close()
			if err != nil {
				tab.Resp = nil
				tab.RespErr = red.RedactError(err).Error()
				a.recordHistoryFailure(tab, plan, red, err, ctx.Err())
				// The status bar must leave its "sending…" state even when the
				// request failed or was canceled.
				if errors.Is(ctx.Err(), context.Canceled) || isCanceledErr(tab.RespErr) {
					a.status = translate(lm.SendCanceledFmt, formatDuration(time.Since(started)))
				} else {
					a.status = translate(lm.SendFailedFmt, formatDuration(time.Since(started)), tab.RespErr)
				}
			} else {
				tab.Resp = res
				tab.RespErr = ""
				tab.respVersion++
				tab.respEditor = nil
				a.recordHistory(tab, plan, red, res)
				// Terminal success: status code and measured duration, so the
				// line never stays on "sending…" after completion.
				a.status = translate(lm.SendDoneFmt, res.Status, formatDuration(res.Duration))
			}
			// Put the caret back in the URL so the user can tweak and resend.
			tab.focusURL = true
		})
	}()
}

// finishStreamSend publishes a stream that opened, whether it completed
// cleanly or ended with a cancel, timeout or disconnect. Everything received
// before the end is kept and shown; the stream phase tells the user which of
// those happened instead of replacing the content with a bare error.
func (a *App) finishStreamSend(
	tab *Tab,
	stream *tabSSEStream,
	res *core.ExecResult,
	err error,
	plan *core.Plan,
	red *core.Redactor,
	ctx context.Context,
	started time.Time,
	lm *layoutStatusMessages,
) {
	if res == nil {
		res = &core.ExecResult{}
	}
	text := stream.display.String()
	// A server may label a non-conforming body text/event-stream (for example a
	// JSON error page). If the parser produced no event text, keep the redacted
	// raw body instead of showing an empty response.
	if text == "" && len(res.Body) > 0 {
		text = string(res.Body)
	}
	size := stream.bytes
	if size <= 0 {
		size = int64(len(text))
	}
	// The UI, copy, save and history all use the redacted event log, never the
	// raw wire body.
	res.Body = []byte(text)
	res.BodySize = size
	res.Pretty = ""
	res.IsBinary = false
	if res.Stream == nil {
		res.Stream = &core.StreamInfo{Kind: "sse"}
	}
	res.Stream.Kind = "sse"
	res.Stream.Phase = stream.phase
	res.Stream.EventCount = stream.eventCount
	res.Stream.BytesReceived = size
	res.Stream.Truncated = stream.truncated

	tab.Resp = res
	tab.RespErr = ""
	tab.respVersion++
	tab.respEditor = nil
	tab.sse = nil
	stream.close()

	switch res.Stream.Phase {
	case core.StreamPhaseCompleted:
		a.status = translate(lm.SendDoneFmt, res.Status, formatDuration(res.Duration))
		a.recordHistory(tab, plan, red, res)
	case core.StreamPhaseCanceled:
		a.status = translate(lm.SendCanceledFmt, formatDuration(time.Since(started)))
		a.recordHistoryFailure(tab, plan, red, err, ctx.Err())
	default:
		reason := res.Stream.EndReason
		if reason == "" && err != nil {
			reason = red.RedactError(err).Error()
		}
		a.status = translate(lm.SendFailedFmt, formatDuration(time.Since(started)), reason)
		a.recordHistoryFailure(tab, plan, red, err, ctx.Err())
	}
}

// cancelAllRequests cancels every in-flight request owned by this window and
// marks its tabs idle. Called when the window is destroyed, so late results are
// ignored and no callback touches a dead window. Live WebSocket sessions are
// terminated here too: no socket outlives its window.
func (a *App) cancelAllRequests() {
	if a == nil || a.ps == nil {
		return
	}
	for _, tab := range a.ps.tabs {
		if tab.cancel != nil {
			tab.cancel()
			tab.cancel = nil
		}
		if tab.Sending {
			tab.Sending = false
		}
		teardownTabStream(tab)
		wsTeardownTab(tab)
	}
}

// --- close guard ---------------------------------------------------------

func (a *App) openCloseGuard(quit bool) {
	if a.closeGuard == nil {
		a.closeGuard = &closeGuard{}
	}
	if a.closeGuard.active && a.closeGuard.quit && !quit {
		return // A window close must not replace an application/update decision.
	}
	a.closeGuard.active = true
	a.closeGuard.quit = quit
	a.closeGuard.errMsg = ""
	if a.win != nil && !a.win.IsDestroyed() {
		a.win.Update(func() {})
	}
}

// resolveCloseGuard runs the chosen action. Saving must fully succeed before
// the window closes; a failure keeps the guard open with the error.
func (a *App) resolveCloseGuard(discard bool) {
	if a.closeGuard == nil || !a.closeGuard.active {
		return
	}
	if a.closeGuard.quit && a.mgr != nil && a.mgr.tryResolveHandoffGuard(a, discard) {
		return
	}
	if discard {
		a.clearDirty()
	} else if err := a.saveAllDirty(); err != nil {
		a.closeGuard.errMsg = err.Error()
		return
	}
	quit := a.closeGuard.quit
	a.closeGuard.active = false
	if quit {
		a.mgr.onQuitGuardSettled(a)
		return
	}
	a.bypassGuard = true
	// Defer the actual close to the next UI pass: this runs while building the
	// guard modal, and destroying a window mid-frame is unsafe.
	a.win.Update(func() { a.win.Close() })
}

func (a *App) cancelCloseGuard() {
	if a.closeGuard == nil {
		return
	}
	a.closeGuard.active = false
	if a.closeGuard.quit {
		a.mgr.pendingQuit = false
		a.mgr.abandonHandoff()
	}
}

func (a *App) closeGuardModal(c *ui.Context, t *ui.Theme) {
	if a.closeGuard == nil || !a.closeGuard.active {
		return
	}
	open := true
	m := a.msg()
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(420).Children(func() {
			ui.Text(c, m.GuardTitle).FontSize(16).Bold()
			ui.Text(c, m.GuardItems).FontSize(12).TextColor(t.TextMuted)
			ui.Scroll(c).MaxHeight(160).Children(func() {
				ui.Column(c).Gap(2).Children(func() {
					if s := a.suiteEditorState(false); s != nil && s.dirty() {
						ui.Text(c, "• "+a.suiteEditorMsg().Title+": "+s.draft.Name).FontSize(12).SingleLine()
					}
					if e := a.ps.envUI; e != nil && e.open && e.dirty() {
						ui.Text(c, translate(m.GuardEnvironment, e.idText)).FontSize(12).SingleLine()
					}
					for _, tab := range a.ps.tabs {
						if tab.Dirty {
							ui.Text(c, "• "+tab.Title).FontSize(12).SingleLine()
						}
					}
				})
			})
			if a.closeGuard.errMsg != "" {
				ui.Text(c, a.closeGuard.errMsg).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.cancelCloseGuard()
					open = false
				}
				if uikit.ActionCursor(ui.Button(c, m.Discard)).Clicked() {
					a.resolveCloseGuard(true)
					open = false
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.SaveAll)).Clicked() {
					a.resolveCloseGuard(false)
					// Keep the modal open only while a save error remains.
					open = a.closeGuard.active
				}
			})
		})
	})
	if !open {
		// Modal dismissed by Escape or an outside click acts as Cancel.
		if a.closeGuard.active {
			a.cancelCloseGuard()
		}
	}
}

// Status returns the current status line text (used by tests).
func (a *App) Status() string { return a.status }
