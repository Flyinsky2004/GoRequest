package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo"

	"gorequest/internal/platform"
)

// fakeWindow is a headless stand-in for a MyGo window. It fires OnClose/OnClosed
// the same way the real backend does so the manager's lifecycle logic can be
// tested without a display.
type fakeWindow struct {
	title     string
	shown     bool
	destroyed bool
	updates   int
	onClose   []func(*mygo.CloseEvent)
	onClosed  []func()
}

func (f *fakeWindow) Show() {
	if !f.destroyed {
		f.shown = true
	}
}
func (f *fakeWindow) Hide()             { f.shown = false }
func (f *fakeWindow) Focus()            {}
func (f *fakeWindow) Close()            { f.requestClose() }
func (f *fakeWindow) SetTitle(t string) { f.title = t }
func (f *fakeWindow) Update(fn func()) {
	f.updates++
	fn()
}
func (f *fakeWindow) OnClose(fn func(*mygo.CloseEvent)) func() {
	f.onClose = append(f.onClose, fn)
	return func() {}
}
func (f *fakeWindow) OnClosed(fn func()) func() {
	f.onClosed = append(f.onClosed, fn)
	return func() {}
}
func (f *fakeWindow) IsDestroyed() bool { return f.destroyed }
func (f *fakeWindow) IsVisible() bool   { return f.shown }

func (f *fakeWindow) requestClose() {
	if f.destroyed {
		return
	}
	e := &mygo.CloseEvent{}
	for _, fn := range f.onClose {
		fn(e)
	}
	if e.DefaultPrevented() {
		return
	}
	f.destroy()
}

func (f *fakeWindow) destroy() {
	if f.destroyed {
		return
	}
	f.destroyed = true
	f.shown = false
	for _, fn := range f.onClosed {
		fn()
	}
}

type fakeFactory struct {
	created []*fakeWindow
}

func (ff *fakeFactory) new(s windowSpec) window {
	f := &fakeWindow{title: s.Title}
	ff.created = append(ff.created, f)
	return f
}

// newTestManager builds a manager whose windows are fakes, sharing one config
// file under the test's temp dir (so a second manager at the same path is a
// "restart").
func newTestManager(t *testing.T) (*Manager, *fakeFactory) {
	t.Helper()
	return newTestManagerAt(t, filepath.Join(t.TempDir(), "config.json"))
}

func newTestManagerAt(t *testing.T, cfgPath string) (*Manager, *fakeFactory) {
	t.Helper()
	ff := &fakeFactory{}
	m := &Manager{
		cfg:       newConfigStore(cfgPath),
		secrets:   platform.NewEnvSecrets(),
		newWindow: ff.new,
		// Keep secret preferences, the management manifest and any local-file
		// store under the test's temp directory. The keychain driver comes from
		// the same test-overridable factory the credential tests install, so no
		// automated run touches the host's real credential store.
		secretStore: platform.NewSecretStore(t.TempDir(), identityKeychainDriverFactory()),
	}
	// Most tests assert the English wording that predates the i18n catalog. The
	// real product default is Simplified Chinese (see i18n tests); tests opt into
	// English in memory so they keep testing behaviour, not translation strings.
	m.cfg.cfg.Language = string(langEnglish)
	return m, ff
}

// --- startup decision ----------------------------------------------------

func TestPlanStartupPriority(t *testing.T) {
	cfg := newConfigStore(filepath.Join(t.TempDir(), "config.json"))
	cfg.setLastProject("/last/project")

	// Explicit path wins over the last project.
	if p := planStartup("/explicit", cfg); p.kind != startProject || p.path != "/explicit" || p.fromRestore {
		t.Fatalf("explicit path not preferred: %+v", p)
	}
	// Last project restores by default.
	if p := planStartup("", cfg); p.kind != startProject || p.path != "/last/project" || !p.fromRestore {
		t.Fatalf("default restore not used: %+v", p)
	}
	// Turning restore off goes to the chooser.
	cfg.setRestoreLast(false)
	if p := planStartup("", cfg); p.kind != startWelcome {
		t.Fatalf("restore off should show welcome: %+v", p)
	}
	// No history goes to the chooser.
	if p := planStartup("", newConfigStore(filepath.Join(t.TempDir(), "c.json"))); p.kind != startWelcome {
		t.Fatalf("no history should show welcome: %+v", p)
	}
}

func TestStartRestoresLastProject(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	dir := writeProject(t, "Restored")
	root, err := normalizeProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	seed := newConfigStore(cfgPath)
	seed.setLastProject(root)

	m, ff := newTestManagerAt(t, cfgPath)
	m.Start()
	if len(m.workspaces) != 1 || m.workspaces[0].ps.root != root {
		t.Fatalf("last project not restored: workspaces=%d", len(m.workspaces))
	}
	if len(ff.created) != 1 || !ff.created[0].shown {
		t.Fatalf("expected one shown project window")
	}
	if m.welcome != nil {
		t.Fatalf("no welcome window expected when restoring")
	}
}

func TestStartRestoreMissingFallsBackAndClearsRecord(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	seed := newConfigStore(cfgPath)
	seed.setLastProject("/no/such/project")

	m, _ := newTestManagerAt(t, cfgPath)
	m.Start()
	if len(m.workspaces) != 0 {
		t.Fatalf("no project should open for a missing path")
	}
	if m.welcome == nil || !m.welcome.win.IsVisible() {
		t.Fatalf("welcome window should be shown on restore failure")
	}
	if got := m.cfg.lastProject(); got != "" {
		t.Fatalf("stale last project should be cleared, got %q", got)
	}
}

func TestStartExplicitInvalidKeepsLastRecord(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	valid := writeProject(t, "Valid")
	validRoot, _ := normalizeProjectPath(valid)
	seed := newConfigStore(cfgPath)
	seed.setLastProject(validRoot)

	m, _ := newTestManagerAt(t, cfgPath)
	m.explicit = "/no/such/explicit"
	m.Start()
	if len(m.workspaces) != 0 {
		t.Fatalf("invalid explicit path must not open a project")
	}
	if m.welcome == nil {
		t.Fatalf("welcome should be shown for invalid explicit path")
	}
	if got := m.cfg.lastProject(); got != validRoot {
		t.Fatalf("explicit failure must not clobber last good record, got %q", got)
	}
	if m.welcome.status == "" {
		t.Fatalf("welcome should report the explicit open error")
	}
}

// --- open / dedup / switching -------------------------------------------

func TestOpenProjectDedupNormalizedPaths(t *testing.T) {
	m, ff := newTestManager(t)
	dir := writeProject(t, "One")
	for _, p := range []string{dir, filepath.Join(dir, ".gorequest"), filepath.Join(dir, ".")} {
		if err := m.openProject(p); err != nil {
			t.Fatalf("openProject(%q): %v", p, err)
		}
	}
	if len(m.workspaces) != 1 {
		t.Fatalf("want one workspace for a deduplicated path, got %d", len(m.workspaces))
	}
	if len(ff.created) != 1 {
		t.Fatalf("want one window created, got %d", len(ff.created))
	}
	if len(m.cfg.recent()) != 1 {
		t.Fatalf("want one recent entry, got %d", len(m.cfg.recent()))
	}
}

func TestSwitchProjectPreservesEdits(t *testing.T) {
	m, _ := newTestManager(t)
	dir1 := writeProject(t, "One")
	dir2 := writeProject(t, "Two")
	if err := m.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	app1 := m.workspaces[0]
	if _, err := app1.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	if err := app1.ps.openDoc("docs/README.md"); err != nil {
		t.Fatal(err)
	}
	app1.ps.tabs[0].Dirty = true
	app1.ps.envSel = "(none)"
	tabsBefore := len(app1.ps.tabs)

	if err := m.openProject(dir2); err != nil {
		t.Fatal(err)
	}
	if m.workspaces[len(m.workspaces)-1] == app1 {
		t.Fatal("opening a second project should focus a new one")
	}
	if len(m.findWorkspace(mustRoot(t, dir1)).ps.tabs) != tabsBefore {
		t.Fatalf("tabs lost after switch")
	}
	// Returning to the first project reuses its state, edits intact.
	if err := m.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	if len(app1.ps.tabs) != tabsBefore || !app1.ps.tabs[0].Dirty || app1.ps.envSel != "(none)" {
		t.Fatal("unsaved edits or env selection lost across project switch")
	}
}

func TestLastActiveProjectTracksActivation(t *testing.T) {
	m, _ := newTestManager(t)
	dir1 := writeProject(t, "One")
	dir2 := writeProject(t, "Two")
	r1, r2 := mustRoot(t, dir1), mustRoot(t, dir2)

	if err := m.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(dir2); err != nil {
		t.Fatal(err)
	}
	if got := m.cfg.lastProject(); got != r2 {
		t.Fatalf("last project should be the newest: got %q want %q", got, r2)
	}
	// Re-activating (not just reopening) an already-loaded project updates it.
	if err := m.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	if got := m.cfg.lastProject(); got != r1 {
		t.Fatalf("activation should update last project: got %q want %q", got, r1)
	}
}

func TestClosingOneProjectKeepsOthersAndReturnsToWelcome(t *testing.T) {
	m, _ := newTestManager(t)
	dir1 := writeProject(t, "One")
	dir2 := writeProject(t, "Two")
	if err := m.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(dir2); err != nil {
		t.Fatal(err)
	}
	app1, app2 := m.workspaces[0], m.workspaces[1]

	// Closing one of two projects keeps the app running with the other.
	app2.win.Close()
	if !app2.closed || len(m.workspaces) != 1 || m.workspaces[0] != app1 {
		t.Fatalf("closing one project should keep the other; workspaces=%d", len(m.workspaces))
	}
	if m.welcome != nil {
		t.Fatalf("no chooser while a project is still open")
	}
	if got := m.cfg.lastProject(); got != app1.ps.root {
		t.Fatalf("last project should move to the survivor, got %q", got)
	}

	// Closing the last project returns to the chooser and clears restore.
	app1.win.Close()
	if len(m.workspaces) != 0 {
		t.Fatalf("want no workspaces after closing the last project")
	}
	if m.welcome == nil || !m.welcome.win.IsVisible() {
		t.Fatalf("closing the last project should show the chooser")
	}
	if got := m.cfg.lastProject(); got != "" {
		t.Fatalf("closing all projects should clear last-project, got %q", got)
	}
}

// --- dirty close protection ---------------------------------------------

func TestCloseGuardBlocksDirtyThenSaveCloses(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "One")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	if _, err := app.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	tab := app.ps.activeTab()
	tab.Dirty = true

	app.win.Close() // must be prevented by the dirty guard
	if app.win.IsDestroyed() {
		t.Fatal("window closed despite unsaved changes")
	}
	if app.closeGuard == nil || !app.closeGuard.active {
		t.Fatal("close guard should be active")
	}

	app.resolveCloseGuard(false) // Save All, then close
	if !app.win.IsDestroyed() || app.ps.hasDirty() {
		t.Fatalf("save should persist and close; destroyed=%v dirty=%v", app.win.IsDestroyed(), app.ps.hasDirty())
	}
	if len(m.workspaces) != 0 || m.welcome == nil {
		t.Fatalf("closing last project should return to chooser")
	}
}

func TestCloseGuardSaveFailureKeepsWindowOpen(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "One")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	if _, err := app.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	tab := app.ps.activeTab()
	tab.Dirty = true
	tab.Entry = nil // force requestFromTab/save to fail

	app.win.Close()
	if !app.closeGuard.active {
		t.Fatal("guard should be active")
	}
	app.resolveCloseGuard(false) // Save All fails
	if app.win.IsDestroyed() {
		t.Fatal("a failed save must not close the window")
	}
	if !app.closeGuard.active || app.closeGuard.errMsg == "" {
		t.Fatalf("guard should stay open with an error, got %+v", app.closeGuard)
	}
	// Cancel clears the guard and keeps the window.
	app.cancelCloseGuard()
	if app.closeGuard.active || app.win.IsDestroyed() {
		t.Fatal("cancel should dismiss the guard and keep the window")
	}
}

func TestCloseGuardDiscardCloses(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "One")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	if _, err := app.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	app.ps.activeTab().Dirty = true

	app.win.Close()
	if !app.closeGuard.active {
		t.Fatal("guard should be active")
	}
	app.resolveCloseGuard(true) // Discard
	if !app.win.IsDestroyed() {
		t.Fatal("discard should close the window")
	}
}

// --- async / close safety ------------------------------------------------

func TestCloseCancelsInflightAndMarksClosed(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "One")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	if _, err := app.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	tab := app.ps.activeTab()
	cancelled := false
	tab.cancel = func() { cancelled = true }
	tab.Sending = true

	app.win.Close() // no dirty edits, so the window closes
	if !app.closed || !app.win.IsDestroyed() {
		t.Fatal("closing an idle window should close it")
	}
	if !cancelled {
		t.Fatal("in-flight request cancel func should run on close")
	}
	if tab.Sending {
		t.Fatal("sending flag should clear when the window closes")
	}
}

func TestProjectStateHasDirty(t *testing.T) {
	ps := &projectState{tabs: []*Tab{{Dirty: false}, {Dirty: true}}}
	if !ps.hasDirty() {
		t.Fatal("hasDirty should be true with a dirty tab")
	}
	ps.tabs[1].Dirty = false
	if ps.hasDirty() {
		t.Fatal("hasDirty should be false with no dirty tabs")
	}
}

func TestStartRestoreDisabledShowsWelcome(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	dir := writeProject(t, "Saved")
	root := mustRoot(t, dir)
	seed := newConfigStore(cfgPath)
	seed.setLastProject(root)
	seed.setRestoreLast(false)

	m, _ := newTestManagerAt(t, cfgPath)
	m.Start()
	if len(m.workspaces) != 0 {
		t.Fatalf("restore disabled should not auto-open a project")
	}
	if m.welcome == nil || !m.welcome.win.IsVisible() {
		t.Fatalf("restore disabled should show the chooser")
	}
	if got := m.cfg.lastProject(); got != root {
		t.Fatalf("disabled restore must not touch the recorded project, got %q", got)
	}
}

// TestQuitKeepsLastProjectRecord proves that stopping the process (a normal
// application quit) does not clear the restore record, unlike closing a
// project window to return to the chooser.
func TestQuitKeepsLastProjectRecord(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "One")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	root := app.ps.root

	m.quitting = true // as set by the OnBeforeQuit handler with no dirty tabs
	app.win.Close()
	if !app.closed {
		t.Fatal("window should close during quit")
	}
	if got := m.cfg.lastProject(); got != root {
		t.Fatalf("normal quit must keep the last project, got %q", got)
	}
	if m.welcome != nil {
		t.Fatal("no chooser should open while quitting")
	}
}

// TestQuitGuardChainsAcrossDirtyWindows checks that quitting with several
// windows holding unsaved edits prompts each window in turn, and that Cancel
// stops the quit.
func TestQuitGuardChainsAcrossDirtyWindows(t *testing.T) {
	m, _ := newTestManager(t)
	d1 := writeProject(t, "One")
	d2 := writeProject(t, "Two")
	if err := m.openProject(d1); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(d2); err != nil {
		t.Fatal(err)
	}
	a1, a2 := m.workspaces[0], m.workspaces[1]
	if _, err := a1.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	if _, err := a2.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	a1.ps.activeTab().Dirty = true
	a2.ps.activeTab().Dirty = true

	m.promptQuitGuard()
	if a1.closeGuard == nil || !a1.closeGuard.active || !a1.closeGuard.quit {
		t.Fatalf("first dirty window should prompt for quit")
	}
	a1.resolveCloseGuard(true) // Discard this one; the next dirty window prompts.
	if a1.ps.hasDirty() {
		t.Fatal("discard should clear the dirty flag")
	}
	if a2.closeGuard == nil || !a2.closeGuard.active || !a2.closeGuard.quit {
		t.Fatalf("second dirty window should prompt next")
	}
	a2.cancelCloseGuard()
	if a2.closeGuard.active || m.pendingQuit {
		t.Fatal("cancel should stop the quit chain")
	}
}

func mustRoot(t *testing.T, dir string) string {
	t.Helper()
	r, err := normalizeProjectPath(dir)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
