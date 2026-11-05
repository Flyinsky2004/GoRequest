package desktop

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// startKind is what startup resolves to: reopen a project window, or show the
// lightweight project chooser.
type startKind int

const (
	startWelcome startKind = iota
	startProject
)

// startPlan is the result of the window-free startup decision. path is the
// project to try when kind is startProject; fromRestore tells the caller the
// path came from the saved last project (so an open failure should clear that
// stale record), rather than an explicit -project argument (which must leave
// the saved record alone).
type startPlan struct {
	kind        startKind
	path        string
	fromRestore bool
}

// planStartup decides where startup goes without touching any window:
//
//  1. an explicit -project path, if one was given;
//  2. the recorded last active project, if "restore last project" is enabled;
//  3. the welcome/project chooser.
//
// It never probes the working directory implicitly, so a .gorequest folder in
// the current directory cannot override the user's last project. Validity of
// the chosen path is checked when it is opened, which falls back to the
// chooser on failure.
func planStartup(explicit string, cfg *configStore) startPlan {
	if p := strings.TrimSpace(explicit); p != "" {
		return startPlan{kind: startProject, path: p}
	}
	if cfg != nil && cfg.restoreLastEnabled() {
		if lp := cfg.lastProject(); lp != "" {
			return startPlan{kind: startProject, path: lp, fromRestore: true}
		}
	}
	return startPlan{kind: startWelcome}
}

// Manager is the application-level object. It owns the one shared configStore
// and secret provider, the open project windows, and the welcome/chooser
// window. Every project lives in exactly one window; reopening the same
// normalized path focuses that window instead of creating a second editing
// state. Background results and dialogs are routed back through the specific
// window that owns them, never through a single global "last window".
type Manager struct {
	cfg       *configStore
	secrets   core.SecretProvider
	newWindow windowFactory
	explicit  string // -project flag value, "" when none

	workspaces []*App
	welcome    *welcomeState

	// quitting marks an application-wide quit in progress so window-close
	// handling does not mistake it for "user closed the last project" and
	// reopen the chooser.
	quitting bool
	// pendingQuit marks a quit paused on a dirty-tab guard.
	pendingQuit bool
}

// msg returns the catalog for the manager's current language (read per frame).
func (m *Manager) msg() *messages {
	if m == nil || m.cfg == nil {
		return messagesFor(defaultLanguage())
	}
	return messagesFor(m.cfg.language())
}

// NewManager builds the application manager, loading the shared config once.
// explicitProject is the -project flag value ("" when not given).
func NewManager(explicitProject string) *Manager {
	return &Manager{
		cfg:       newConfigStore(configFilePath()),
		secrets:   platform.NewEnvSecrets(),
		newWindow: newRealWindow,
		explicit:  explicitProject,
	}
}

// WireApp registers application-level lifecycle hooks. Call it once from
// App.WhenReady, before Start.
func (m *Manager) WireApp() {
	mygo.App.OnBeforeQuit(func(e *mygo.QuitEvent) {
		if m.quitting {
			return // already resolved: let the quit proceed
		}
		if !m.anyDirty() {
			m.quitting = true
			return
		}
		e.PreventDefault()
		m.pendingQuit = true
		m.promptQuitGuard()
	})
}

// Start runs the startup decision. A failed explicit or restore open falls
// back to the welcome chooser with the error shown, never silently opening a
// different project.
func (m *Manager) Start() {
	plan := planStartup(m.explicit, m.cfg)
	if plan.kind == startProject {
		if err := m.openProject(plan.path); err != nil {
			if plan.fromRestore {
				// The recorded project is gone or broken: forget it so the next
				// launch does not retry a known-bad path.
				m.cfg.clearLastProject()
			}
			m.showWelcome(translate(m.msg().OpenFailed, err.Error()))
		}
		return
	}
	m.showWelcome("")
}

// openProject loads path or, when it is already open, activates that window.
// A failed open leaves the current windows and the recent/last-project records
// untouched.
func (m *Manager) openProject(path string) error {
	root, err := normalizeProjectPath(path)
	if err != nil {
		return err
	}
	if app := m.findWorkspace(root); app != nil {
		m.activate(app)
		app.status = translate(m.msg().SwitchedTo, app.ps.name())
		return nil
	}
	p, err := core.OpenProject(root)
	if err != nil {
		return err
	}
	ps := newProjectState(root, p)
	app := &App{mgr: m, ps: ps, navWidth: 260}
	app.win = m.newWindow(windowSpec{
		Title:     "GoRequest — " + ps.name(),
		Width:     1200,
		Height:    820,
		MinWidth:  820,
		MinHeight: 560,
		View:      app.View,
	})
	app.wire()
	m.workspaces = append(m.workspaces, app)

	m.cfg.addRecent(root, ps.name())
	m.activate(app)
	// Hide (not destroy) the chooser so it can come back for "switch project";
	// a project window is now the foreground.
	if m.welcome != nil && m.welcome.win != nil && !m.welcome.win.IsDestroyed() {
		m.welcome.win.Hide()
	}
	if n := core.ErrorCount(p.Issues); n > 0 {
		app.status = translate(m.msg().OpenedWithErrors, ps.name(), n)
	} else {
		app.status = translate(m.msg().OpenedRequests, ps.name(), len(p.Requests))
	}
	return nil
}

// activate makes app the foreground project: it focuses the window and records
// it as the last active project. It does not reorder the recent list, which is
// why last-project tracking needs its own record.
func (m *Manager) activate(app *App) {
	if app == nil || app.win == nil || app.win.IsDestroyed() {
		return
	}
	app.win.Show()
	app.win.Focus()
	m.cfg.setLastProject(app.ps.root)
}

func (m *Manager) findWorkspace(root string) *App {
	for _, app := range m.workspaces {
		if app.ps.root == root {
			return app
		}
	}
	return nil
}

// showWelcome opens (or re-shows) the small welcome/project-chooser window. It
// never renders the editor workspace, so starting with no project shows only
// this light window instead of a large empty editor window.
func (m *Manager) showWelcome(status string) {
	if m.welcome != nil && m.welcome.win != nil && !m.welcome.win.IsDestroyed() {
		m.welcome.status = status
		m.welcome.win.Show()
		m.welcome.win.Focus()
		return
	}
	w := &welcomeState{mgr: m, status: status}
	m.welcome = w
	w.win = m.newWindow(windowSpec{
		Title:     "GoRequest",
		Width:     720,
		Height:    480,
		MinWidth:  560,
		MinHeight: 360,
		View:      w.view,
	})
	w.wire()
	w.win.Show()
}

// pickProject shows the platform directory chooser, parented to the window that
// asked, then opens the chosen folder through that same window's Update so the
// UI thread is the only one touching window state. Cancelling changes nothing.
func (m *Manager) pickProject(parent window) {
	var pw *mygo.Window
	if nw, ok := parent.(nativeWindower); ok {
		pw = nw.nativeWindow()
	}
	msg := m.msg()
	if pw == nil {
		m.setStatusFor(parent, msg.WindowNotReady)
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      pw,
			Title:       msg.OpenDialogTitle,
			ButtonLabel: msg.Open,
			Message:     msg.OpenDialogMessage,
			Directory:   true,
		})
		if err != nil {
			parent.Update(func() { m.setStatusFor(parent, translate(msg.OpenFailed, err.Error())) })
			return
		}
		if len(paths) == 0 {
			return // cancelled: no state change
		}
		parent.Update(func() {
			if err := m.openProject(paths[0]); err != nil {
				m.setStatusFor(parent, translate(msg.OpenFailed, err.Error()))
			}
		})
	}()
}

// setStatusFor writes a status message to the specific window that owns an
// action, so an error from the welcome window does not appear in a project
// window (or vice versa).
func (m *Manager) setStatusFor(w window, s string) {
	if m.welcome != nil && m.welcome.win == w {
		m.welcome.status = s
		return
	}
	for _, app := range m.workspaces {
		if app.win == w {
			app.status = s
			return
		}
	}
}

// onWorkspaceClosed runs when a project window is actually destroyed (after
// any close guard). It forgets the window, cancels its requests, keeps the
// last-active record accurate, and returns the user to the chooser when the
// last project closes — unless this close is part of an application quit.
func (m *Manager) onWorkspaceClosed(app *App) {
	app.closed = true
	app.cancelAllRequests()

	kept := make([]*App, 0, len(m.workspaces))
	for _, w := range m.workspaces {
		if w != app {
			kept = append(kept, w)
		}
	}
	m.workspaces = kept

	if m.quitting {
		return // keep the last-project record so a normal quit still restores
	}

	if m.cfg.lastProject() == app.ps.root {
		if len(m.workspaces) > 0 {
			m.cfg.setLastProject(m.workspaces[len(m.workspaces)-1].ps.root)
		} else {
			m.cfg.clearLastProject()
		}
	}

	if len(m.workspaces) == 0 {
		m.showWelcome("")
	}
}

// anyDirty reports whether any open project has unsaved edits.
func (m *Manager) anyDirty() bool {
	for _, app := range m.workspaces {
		if !app.closed && app.ps.hasDirty() {
			return true
		}
	}
	return false
}

// promptQuitGuard walks the dirty windows one at a time: it shows the
// save/discard/cancel guard on the first dirty project, and once that window is
// resolved it calls itself again for the next. When nothing is dirty, it marks
// the quit in progress and asks the app to quit for real.
func (m *Manager) promptQuitGuard() {
	for _, app := range m.workspaces {
		if !app.closed && app.ps.hasDirty() {
			app.win.Show()
			app.win.Focus()
			app.openCloseGuard(true)
			return
		}
	}
	m.quitting = true
	m.pendingQuit = false
	mygo.App.Quit()
}

// onQuitGuardSettled resumes the quit after a dirty window's guard was resolved
// (saved or discarded).
func (m *Manager) onQuitGuardSettled(app *App) {
	app.closeGuard.active = false
	m.promptQuitGuard()
}

// --- recent-list helpers -------------------------------------------------

func (m *Manager) visibleRecents() []recentProject { return m.cfg.recent() }

// recentLabel returns a recent project's display name followed by its
// secondary path, so a long or duplicate name can still be told apart. The
// sidebar item lays it out single-line and truncates when narrow.
func (m *Manager) recentLabel(rp recentProject) string {
	return m.projectName(rp) + "  ·  " + displayPath(rp.Path)
}

func (m *Manager) projectName(rp recentProject) string {
	if app := m.findWorkspace(rp.Path); app != nil {
		return app.ps.name()
	}
	if rp.Name != "" {
		return rp.Name
	}
	return filepath.Base(rp.Path)
}
