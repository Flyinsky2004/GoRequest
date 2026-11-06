// Package desktop builds the native MyGo user interface for GoRequest. It
// holds only views and interaction state; all request, variable, auth and
// execution logic lives in the pure Go core package.
package desktop

import (
	"context"
	"fmt"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
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

	// History view (§3): whether the sidebar shows history, the loaded records
	// for the current project, and the clear-confirmation state.
	historyView      bool
	historyRecords   []historyRecord
	historyKey       string
	historyClearOpen bool

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
}

// wire connects the project window's lifecycle to the manager and the close
// guard.
func (a *App) wire() {
	a.win.OnClose(func(e *mygo.CloseEvent) {
		a.saveGeometry()
		if a.bypassGuard || a.mgr.quitting {
			return
		}
		if a.ps.hasDirty() {
			e.PreventDefault()
			a.openCloseGuard(false)
		}
	})
	a.win.OnClosed(func() { a.mgr.onWorkspaceClosed(a) })
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
	applyAppearance(c, a.mgr.cfg)
	t := c.Theme()
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
		ui.Split(c, &a.navWidth, func() { a.nav(c, t) }, func() { a.workspace(c, t) }).Grow(1).FillWidth()
		if a.status != "" {
			ui.Row(c).Padding(5, 12).Clip().BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		}
	})
	a.envEditorModal(c, t)
	a.settingsModal(c, t)
	a.closeGuardModal(c, t)
	a.sendGuardModal(c, t)
	a.historyClearModal(c, t)
	a.formsModals(c, t)
}

func (a *App) settingsModal(c *ui.Context, t *ui.Theme) {
	renderSettings(c, t, a.mgr.cfg, &a.showSettings, func(s string) { a.status = s })
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
	tab := a.ps.activeTab()
	if tab == nil {
		return
	}
	rel := tab.DocRel
	if tab.Kind == "request" {
		rel = tab.Entry.RelPath
	}
	if err := a.ps.saveTab(tab); err != nil {
		m := a.msg()
		if core.IsConflict(err) {
			a.status = translate(m.Conflict, rel)
		} else {
			a.status = translate(m.SaveFailedPrefix, err.Error())
		}
		return
	}
	a.status = translate(a.msg().Saved, rel)
}

// saveAllDirty saves every dirty tab of this project; the first failure stops
// the sequence so a close cannot proceed past a failed or conflicting save.
func (a *App) saveAllDirty() error {
	if e := a.ps.envUI; e != nil && e.open && e.dirty() {
		if err := a.saveEnvironmentDraft(a.ps); err != nil {
			return fmt.Errorf("environment: %w", err)
		}
	}
	for _, tab := range a.ps.tabs {
		if !tab.Dirty {
			continue
		}
		if err := a.ps.saveTab(tab); err != nil {
			return fmt.Errorf("%s: %w", tab.Title, err)
		}
	}
	return nil
}

func (a *App) clearDirty() {
	if a.ps.envUI != nil {
		a.ps.envUI.close()
	}
	for _, tab := range a.ps.tabs {
		tab.Dirty = false
	}
}

// send executes the tab's request on a background goroutine. The result is
// posted through THIS window's Update, and is dropped if the window was closed
// meanwhile, so a result can never land in another project's window.
func (a *App) send(tab *Tab) {
	ps := a.ps
	req, err := requestFromTab(tab)
	if err != nil {
		a.status = translate(a.msg().RequestFailedPrefix, err.Error())
		return
	}
	vars, _, err := ps.project.ResolveVars(tab.Entry, ps.currentEnv())
	if err != nil {
		a.status = err.Error()
		return
	}
	auth, err := ps.project.AuthFor(req)
	if err != nil {
		a.status = err.Error()
		return
	}
	plan, err := core.BuildPlan(req, vars, a.mgr.secrets, auth)
	if err != nil {
		a.status = err.Error()
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	tab.cancel = cancel
	tab.Sending = true
	tab.RespErr = ""
	a.status = translate(a.msg().Sending, req.HTTP.Method, req.HTTP.URL)

	win := a.win
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
			tab.Sending = false
			if err != nil {
				tab.Resp = nil
				tab.RespErr = err.Error()
			} else {
				tab.Resp = res
				tab.RespErr = ""
				tab.respVersion++
				tab.respEditor = nil
				a.recordHistory(tab, plan.URL, res)
			}
			// Put the caret back in the URL so the user can tweak and resend.
			tab.focusURL = true
		})
	}()
}

// cancelAllRequests cancels every in-flight request owned by this window and
// marks its tabs idle. Called when the window is destroyed, so late results are
// ignored and no callback touches a dead window.
func (a *App) cancelAllRequests() {
	for _, tab := range a.ps.tabs {
		if tab.cancel != nil {
			tab.cancel()
			tab.cancel = nil
		}
		if tab.Sending {
			tab.Sending = false
		}
	}
}

// --- close guard ---------------------------------------------------------

func (a *App) openCloseGuard(quit bool) {
	if a.closeGuard == nil {
		a.closeGuard = &closeGuard{}
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
				if ui.Button(c, m.Cancel).Clicked() {
					a.cancelCloseGuard()
					open = false
				}
				if ui.Button(c, m.Discard).Clicked() {
					a.resolveCloseGuard(true)
					open = false
				}
				if ui.PrimaryButton(c, m.SaveAll).Clicked() {
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
