package desktop

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/egoist/mygo"

	"gorequest/internal/brand"
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
	watchFiles bool
	cfg        *configStore
	secrets    core.SecretProvider
	history    *historyStore
	newWindow  windowFactory
	explicit   string // -project flag value, "" when none
	// secretStore resolves the per-project-clone secret backend, its persisted
	// preference and the managed-name manifest. It is created lazily so a
	// Manager literal in a test can supply its own temp root.
	secretStore   *platform.SecretStore
	secretStoreMu sync.Mutex
	// prefs holds the B02 workspace preferences (editor, auto-save, shortcut
	// toggles and pinned recents) in their own local file. It is created on
	// first use from the config directory, so a config-less test or run never
	// writes to a real user path.
	prefs *workspacePrefsStore

	// update owns the manager-level update check, its persistent daily cache
	// and the shared update dialog. It is nil in tests that build a Manager
	// literal, so no test reaches the network; NewManager wires the real one.
	update *updateController

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
	m := &Manager{
		cfg:         newConfigStore(configFilePath()),
		watchFiles:  true,
		secrets:     platform.NewEnvSecrets(),
		history:     newHistoryStore(),
		newWindow:   newRealWindow,
		explicit:    explicitProject,
		secretStore: platform.NewSecretStore("", nil),
	}
	m.update = newRealUpdateController(m)
	return m
}

// secretStoreFor returns the manager's secret store, creating one lazily for a
// Manager literal. When the config lives in a temp directory (tests), the
// secret documents stay beside it instead of touching the real user home.
func (m *Manager) secretStoreFor() *platform.SecretStore {
	m.secretStoreMu.Lock()
	defer m.secretStoreMu.Unlock()
	if m.secretStore != nil {
		return m.secretStore
	}
	root := ""
	if m.cfg != nil && m.cfg.path != "" {
		root = filepath.Join(filepath.Dir(m.cfg.path), "secrets")
	}
	m.secretStore = platform.NewSecretStore(root, identityKeychainDriverFactory())
	return m.secretStore
}

// WireApp registers application-level lifecycle hooks. Call it once from
// App.WhenReady, before Start.
func (m *Manager) WireApp() {
	// Use the user-provided logo as the running application's icon. In MyGo
	// v0.7.4 App.Dock.SetIcon calls NSApplication's
	// setApplicationIconImage: with the PNG on macOS; it also sets the default
	// window icon on Windows/Linux. WhenReady runs after the native backend is
	// initialized, so this is a supported call point. A failure keeps the
	// platform default icon instead of breaking startup. This only affects a
	// running process; a bundled .app needs CFBundleIconFile (see
	// scripts/make-macos-app.sh) and real Dock/Finder display is not claimed
	// from this code path alone.
	_ = mygo.App.Dock.SetIcon(brand.PNG())
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
	// The update check runs after the startup window exists, on its own
	// goroutine: an offline or slow network never delays or blocks startup.
	defer m.startUpdateCheck()
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
	store := m.secretStoreFor()
	scope, err := store.Scope(p.File.ID, root)
	if err != nil {
		return err
	}
	kind, err := store.SelectedKind(scope)
	if err != nil {
		return err
	}
	backend, err := store.Backend(scope, p.File.ID, root, kind)
	if err != nil {
		return err
	}
	ps.secretScope = scope
	ps.secretKind = kind
	ps.secretBackend = backend
	ps.secrets, err = platform.NewLayeredSecrets(backend, nil)
	if err != nil {
		return err
	}
	// Restore the last used build/response split (§7).
	ps.buildPct = m.cfg.buildPct()
	winW, winH := m.cfg.windowSize()
	app := &App{mgr: m, ps: ps, navWidth: m.cfg.navWidth()}
	app.win = m.newWindow(windowSpec{
		Title:     "GoRequest — " + ps.name(),
		Width:     winW,
		Height:    winH,
		MinWidth:  1024,
		MinHeight: 640,
		Chrome:    true,
		View:      app.View,
	})
	app.wire()
	var syncErr error
	if m.watchFiles {
		syncErr = app.StartFileSync()
	}
	m.workspaces = append(m.workspaces, app)

	m.cfg.addRecent(root, ps.name())
	m.activate(app)
	// An update dialog owned by the chooser follows the user into the project
	// window instead of disappearing with the hidden chooser.
	if m.update != nil && m.update.dialogOpen {
		m.update.setHost(app.win)
		app.win.Update(func() {})
	}
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
	if syncErr != nil {
		app.status += " · " + syncErr.Error()
	}
	return nil
}

// refreshSecretBackend rebuilds the live provider and every cached status for
// all windows that share scope. It is called only after a switch has copied and
// verified all values and the new preference has been persisted, so HTTP, WS,
// Runner and the management pages all read the same backend immediately.
func (m *Manager) refreshSecretBackend(scope string, kind platform.SecretBackendKind) error {
	store := m.secretStoreFor()
	var firstErr error
	for _, app := range m.workspaces {
		ps := app.ps
		if ps == nil || ps.secretScope != scope || ps.project == nil || ps.project.File == nil {
			continue
		}
		backend, err := store.Backend(scope, ps.project.File.ID, ps.root, kind)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		layered, err := platform.NewLayeredSecrets(backend, nil)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		ps.secretKind = kind
		ps.secretBackend = backend
		ps.secrets = layered
		app.resetSecretCaches()
	}
	return firstErr
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
//
// The default height leaves the new-project sheet room for both mode cards,
// the name/location fields, the final-path preview and the footer without
// scrolling. MinHeight stays small so the window still fits short screens,
// where the sheet body scrolls and the footer remains reachable.
func (m *Manager) showWelcome(status string) {
	if m.welcome != nil && m.welcome.win != nil && !m.welcome.win.IsDestroyed() {
		m.welcome.status = status
		m.welcome.win.Show()
		m.welcome.win.Focus()
		if m.update != nil && m.update.dialogOpen {
			m.update.setHost(m.welcome.win)
			m.welcome.win.Update(func() {})
		}
		return
	}
	w := &welcomeState{mgr: m, status: status}
	m.welcome = w
	w.win = m.newWindow(windowSpec{
		Title:     "GoRequest",
		Width:     720,
		Height:    640,
		MinWidth:  560,
		MinHeight: 360,
		View:      w.view,
	})
	w.wire()
	w.win.Show()
	if m.update != nil && m.update.dialogOpen {
		m.update.setHost(w.win)
	}
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
	app.StopFileSync()
	app.git.close()
	customMethodMu.Lock()
	delete(customMethodStates, app)
	customMethodMu.Unlock()

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
	} else if m.update != nil && m.update.getHost() == app.win {
		nh := m.updateHostWindow()
		m.update.setHost(nh)
		if nh != nil && m.update.dialogOpen {
			nh.Update(func() {})
		}
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

// --- update lifecycle ----------------------------------------------------

// startUpdateCheck launches the once-per-day automatic check. It records the
// host window on the UI thread first, so the dialog opens in the window the
// user is looking at. A nil controller (test Manager literals) does nothing.
func (m *Manager) startUpdateCheck() {
	if m == nil || m.update == nil {
		return
	}
	m.update.setHost(m.updateHostWindow())
	go m.update.autoCheck()
}

// updateHostWindow picks the window an application-level update dialog belongs
// to: the visible project chooser, otherwise the first live project window.
func (m *Manager) updateHostWindow() window {
	if m == nil {
		return nil
	}
	if m.welcome != nil && m.welcome.win != nil && !m.welcome.win.IsDestroyed() && m.welcome.win.IsVisible() {
		return m.welcome.win
	}
	for _, app := range m.workspaces {
		if app != nil && app.win != nil && !app.win.IsDestroyed() {
			return app.win
		}
	}
	if m.welcome != nil && m.welcome.win != nil && !m.welcome.win.IsDestroyed() {
		return m.welcome.win
	}
	return nil
}

// openSettingsWindow brings the Settings sheet forward in the host window, or
// creates the chooser with Settings open when no window exists.
func (m *Manager) openSettingsWindow() {
	if m == nil {
		return
	}
	if m.welcome != nil && m.welcome.win != nil && !m.welcome.win.IsDestroyed() && m.welcome.win.IsVisible() {
		m.welcome.showSettings = true
		m.welcome.win.Focus()
		return
	}
	for _, app := range m.workspaces {
		if app != nil && !app.closed && app.win != nil && !app.win.IsDestroyed() {
			app.showSettings = true
			app.win.Show()
			app.win.Focus()
			return
		}
	}
	m.showWelcome("")
	if m.welcome != nil {
		m.welcome.showSettings = true
	}
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
