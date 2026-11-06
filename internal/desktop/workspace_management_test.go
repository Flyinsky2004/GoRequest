package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
)

// --- preferences ----------------------------------------------------------

func TestWorkspacePrefsDefaultsAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	s := newWorkspacePrefsStore(path)
	if s.fontSize() != defaultEditorFontSize || s.tabSize() != defaultEditorTabSize {
		t.Fatalf("unexpected defaults: font=%d tab=%d", s.fontSize(), s.tabSize())
	}
	if s.softWrap() || !s.lineNumbers() || s.autoSaveEnabled() {
		t.Fatal("unexpected boolean defaults")
	}
	if s.autoSaveDelay() != defaultAutoSaveDelay*time.Second {
		t.Fatalf("delay default = %v", s.autoSaveDelay())
	}

	s.setFontSize(17)
	s.setTabSize(2)
	s.setSoftWrap(true)
	s.setLineNumbers(false)
	s.setAutoSaveEnabled(true)
	s.setAutoSaveDelaySeconds(10)
	s.setShortcutEnabled(shortcutQuickSearch, false)
	s.setPinned("/proj/a", true)

	again := newWorkspacePrefsStore(path)
	if again.fontSize() != 17 || again.tabSize() != 2 || !again.softWrap() || again.lineNumbers() {
		t.Fatalf("persisted editor prefs = %+v", again.cfg)
	}
	if !again.autoSaveEnabled() || again.autoSaveDelay() != 10*time.Second {
		t.Fatalf("persisted auto-save prefs = %+v", again.cfg)
	}
	if again.shortcutEnabled(shortcutQuickSearch) {
		t.Fatal("disabled shortcut was not persisted")
	}
	if !again.pinned("/proj/a") {
		t.Fatal("pinned project was not persisted")
	}
	if !again.shortcutEnabled(shortcutNewRequest) {
		t.Fatal("unrelated shortcuts must stay enabled")
	}
}

func TestWorkspacePrefsClampInvalidValues(t *testing.T) {
	s := newWorkspacePrefsStore("")
	s.setFontSize(100)
	s.setTabSize(0)
	s.setAutoSaveDelaySeconds(999)
	if s.fontSize() != defaultEditorFontSize || s.tabSize() != defaultEditorTabSize {
		t.Fatal("invalid setter values must be ignored")
	}
	if s.autoSaveDelay() != defaultAutoSaveDelay*time.Second {
		t.Fatal("invalid delay must fall back to the default")
	}
	// A hand-edited file with out-of-range numbers is normalized on read too.
	s.cfg.EditorFontSize = 99
	s.cfg.EditorTabSize = -1
	s.cfg.AutoSaveDelaySeconds = 10000
	if s.fontSize() != defaultEditorFontSize || s.tabSize() != defaultEditorTabSize {
		t.Fatal("out-of-range stored values must be normalized")
	}
	if s.autoSaveDelay() != defaultAutoSaveDelay*time.Second {
		t.Fatal("out-of-range stored delay must be normalized")
	}
}

func TestApplyEditorPrefsToLiveEditorStates(t *testing.T) {
	s := newWorkspacePrefsStore("")
	s.setFontSize(18)
	s.setTabSize(2)
	s.setSoftWrap(true)
	s.setLineNumbers(false)
	pol := s.editorPolicy()
	st := &editor.State{}
	pol.apply(st)
	if st.Font.Size != 18 || st.TabSize != 2 || !st.SoftWrap {
		t.Fatalf("editor policy not applied: font=%v tab=%d wrap=%v", st.Font.Size, st.TabSize, st.SoftWrap)
	}
	if st.LineNumbers != editor.LineNumbersOff {
		t.Fatalf("line numbers = %v", st.LineNumbers)
	}
}

func TestAutoSaveStepHonorsSettingsAndSkipsExternalChanges(t *testing.T) {
	t.Cleanup(StopAllFileSync)
	a, ps := openTestProject(t, "AutoSave")
	tab := openRequestTabs(t, ps, "Typed")[0]
	prefs := a.mgr.workspacePrefs()
	prefs.setAutoSaveEnabled(false)

	tab.URL = "https://autosave.example/one"
	tab.Dirty = true
	base := time.Now()
	a.autoSaveLast = base
	if a.autoSaveStep(base.Add(time.Hour)) {
		t.Fatal("disabled auto-save must not run")
	}
	if !tab.Dirty {
		t.Fatal("disabled auto-save wrote the tab")
	}

	prefs.setAutoSaveEnabled(true)
	a.autoSaveLast = base
	if a.autoSaveStep(base.Add(time.Second)) {
		t.Fatal("auto-save ran before the configured delay")
	}
	if !a.autoSaveStep(base.Add(prefs.autoSaveDelay())) {
		t.Fatal("auto-save should run once the delay elapsed")
	}
	if tab.Dirty {
		t.Fatal("auto-save did not clear the dirty flag")
	}
	stored, _, err := ps.project.Store.Read(tab.Entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstring(string(stored), "https://autosave.example/one") {
		t.Fatalf("auto-save did not write the URL: %s", stored)
	}

	// Edit again, then change the file externally: the next tick must skip it.
	tab.URL = "https://autosave.example/two"
	tab.Dirty = true
	abs := filepath.Join(ps.project.Store.Root, filepath.FromSlash(tab.Entry.RelPath))
	external, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, append(external, []byte("\n# external\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	a.autoSaveLast = time.Now().Add(-time.Hour)
	a.autoSaveStep(time.Now())
	if !tab.Dirty {
		t.Fatal("auto-save must skip a tab whose file changed externally")
	}
	disk, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if !hasSubstring(string(disk), "# external") {
		t.Fatal("auto-save overwrote the external change")
	}
	if !hasSubstring(a.status, "skipped") {
		t.Fatalf("expected a skipped auto-save status, got %q", a.status)
	}
}

// --- recent list ----------------------------------------------------------

func TestRecentEntriesPinSearchAndForget(t *testing.T) {
	m, _ := newTestManager(t)
	m.cfg.addRecent("/proj/a", "Alpha")
	m.cfg.addRecent("/proj/b", "Beta")
	m.pinRecent("/proj/a", true)

	got := m.recentEntries("")
	if len(got) != 2 || got[0].Name != "Alpha" {
		t.Fatalf("pinned project should be first: %+v", got)
	}
	if filtered := m.recentEntries("bet"); len(filtered) != 1 || filtered[0].Name != "Beta" {
		t.Fatalf("search filter = %+v", filtered)
	}
	if filtered := m.recentEntries("no-such"); len(filtered) != 0 {
		t.Fatalf("unexpected matches: %+v", filtered)
	}

	m.cfg.setLastProject("/proj/a")
	m.forgetRecent("/proj/a")
	if m.cfg.lastProject() != "" {
		t.Fatal("forgetting the last project must clear startup restore")
	}
	if len(m.recentEntries("")) != 1 {
		t.Fatal("forgetRecent did not remove the entry")
	}
	if m.workspacePrefs().pinned("/proj/a") {
		t.Fatal("forgetting a project must also drop its pin")
	}
}

// --- new project ----------------------------------------------------------

func TestManagerCreateProjectOpensItAndNeverOverwrites(t *testing.T) {
	m, _ := newTestManager(t)
	parent := t.TempDir()
	root, err := m.createProjectAt(parent, "My Service")
	if err != nil {
		t.Fatalf("createProjectAt: %v", err)
	}
	if filepath.Base(root) != "my-service" {
		t.Fatalf("root = %q", root)
	}
	if resolved, err := filepath.EvalSymlinks(parent); err == nil {
		if root != filepath.Join(resolved, "my-service") {
			t.Fatalf("root = %q, want it under %q", root, resolved)
		}
	}
	app := m.findWorkspace(root)
	if app == nil {
		t.Fatal("new project did not open a workspace window")
	}
	if app.ps.project.File.Name != "My Service" {
		t.Fatalf("project name = %q", app.ps.project.File.Name)
	}
	if _, err := m.createProjectAt(parent, "My Service"); err == nil {
		t.Fatal("creating over a non-empty project folder must fail")
	}
}

// --- global workspace and transfer ---------------------------------------

func TestGlobalWorkspaceIsPersonalAndNotARecentProject(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "global")
	t.Setenv(core.GlobalWorkspaceDirEnv, dir)
	m, _ := newTestManager(t)

	if err := m.openGlobalWorkspace(); err != nil {
		t.Fatalf("openGlobalWorkspace: %v", err)
	}
	app := m.globalWorkspaceApp()
	if app == nil || !app.isGlobal {
		t.Fatal("global workspace window not marked global")
	}
	if len(m.cfg.recent()) != 0 {
		t.Fatalf("global workspace must not enter recents: %+v", m.cfg.recent())
	}
	if m.cfg.lastProject() != "" {
		t.Fatalf("global workspace must not become the startup target, got %q", m.cfg.lastProject())
	}
	wantDir := filepath.Join(dir, ".gorequest")
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		wantDir = filepath.Join(resolved, ".gorequest")
	}
	if app.ps.project.Dir != wantDir {
		t.Fatalf("global project dir = %q, want %q", app.ps.project.Dir, wantDir)
	}

	// Opening again focuses the same window instead of creating a duplicate.
	if err := m.openGlobalWorkspace(); err != nil {
		t.Fatal(err)
	}
	if len(m.workspaces) != 1 {
		t.Fatalf("global workspace opened twice: %d windows", len(m.workspaces))
	}
}

func TestTransferBetweenProjectAndGlobalKeepsIDRules(t *testing.T) {
	t.Setenv(core.GlobalWorkspaceDirEnv, filepath.Join(t.TempDir(), "global"))
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "Project")); err != nil {
		t.Fatal(err)
	}
	project := m.workspaces[0]
	entry, err := project.ps.project.NewRequest("", "Shared")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := m.ensureGlobalWorkspaceApp(); err != nil {
		t.Fatal(err)
	}
	global := m.globalWorkspaceApp()
	if global == nil {
		t.Fatal("global workspace missing")
	}
	if m.cfg.lastProject() != project.ps.root {
		t.Fatalf("opening the global workspace changed the last project: %q", m.cfg.lastProject())
	}

	// Copy project -> global: new ID, source stays.
	project.beginTransfer(entry, global, core.TransferCopy)
	if project.transfer == nil {
		t.Fatalf("transfer draft not created: %s", project.status)
	}
	copiedID := project.transfer.plan.TargetID
	if copiedID == entry.Request.ID {
		t.Fatal("copy must not reuse the source ID across workspaces")
	}
	project.applyTransfer()
	if global.ps.project.Requests[copiedID] == nil {
		t.Fatal("copy did not land in the global workspace")
	}
	if project.ps.project.Requests[entry.Request.ID] == nil {
		t.Fatal("copy removed the source request")
	}

	// Move global -> project: stable ID, global copy removed.
	moved := global.ps.project.Requests[copiedID]
	global.beginTransfer(moved, project, core.TransferMove)
	if global.transfer == nil {
		t.Fatalf("move draft not created: %s", global.status)
	}
	if global.transfer.plan.TargetID != moved.Request.ID {
		t.Fatal("move must keep the stable ID")
	}
	global.applyTransfer()
	if global.ps.project.Requests[copiedID] != nil {
		t.Fatal("move left the request in the global workspace")
	}
	if project.ps.project.Requests[copiedID] == nil {
		t.Fatal("move did not land in the project")
	}
}

func TestTransferRefusesDirtyMoveAndPreviewsInlineSecrets(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "One")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	b := writeProject(t, "Two")
	if err := m.openProject(b); err != nil {
		t.Fatal(err)
	}
	target := m.workspaces[1]

	entry := saveDesktopRequestWithAuth(t, a, "Dirty", &core.AuthRef{
		Mode:   "inline",
		Inline: &core.AuthScheme{Type: "bearer", Token: "fake-inline-token"},
	})
	tab := a.ps.tabs[len(a.ps.tabs)-1]
	tab.Dirty = true

	a.beginTransfer(entry, target, core.TransferMove)
	if a.transfer != nil {
		t.Fatal("a dirty move must be refused")
	}
	if !hasSubstring(a.status, "unsaved") {
		t.Fatalf("dirty move status = %q", a.status)
	}
	if target.ps.project.Requests[entry.Request.ID] != nil {
		t.Fatal("refused dirty move still wrote the target")
	}

	tab.Dirty = false
	a.beginTransfer(entry, target, core.TransferCopy)
	if a.transfer == nil {
		t.Fatalf("copy draft missing: %s", a.status)
	}
	if !a.transfer.plan.Blocked {
		t.Fatal("inline token must block the transfer")
	}
	a.applyTransfer()
	if a.transfer == nil {
		t.Fatal("blocked apply must keep the preview open to explain the block")
	}
	if target.ps.project.Requests[entry.Request.ID] != nil || len(target.ps.project.Requests) != 0 {
		t.Fatal("blocked transfer wrote the destination")
	}
}

func saveDesktopRequestWithAuth(t *testing.T, a *App, name string, auth *core.AuthRef) *core.RequestEntry {
	t.Helper()
	entry, err := a.ps.project.NewRequest("", name)
	if err != nil {
		t.Fatal(err)
	}
	req := *entry.Request
	req.Auth = auth
	if _, err := a.ps.project.SaveRequest(entry, &req, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	a.ps.openRequest(entry)
	return entry
}

// --- breadcrumb -----------------------------------------------------------

func TestWorkspaceBreadcrumbShowsFolderPathAndIsClickable(t *testing.T) {
	a, ps := openTestProject(t, "Breadcrumbs")
	folder, err := ps.createFolder("", "API")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := ps.createRequestIn(folder.RelPath, "List Orders")
	if err != nil {
		t.Fatal(err)
	}
	ps.openRequest(entry)

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("Breadcrumbs") || !tt.HasText("API") || !tt.HasText("List Orders") {
		t.Fatalf("breadcrumb missing path segments; texts=%v", tt.Texts())
	}
	if err := tt.Click("API"); err != nil {
		t.Fatalf("breadcrumb folder should be clickable: %v", err)
	}
	if ps.sel != "folder:"+folder.RelPath {
		t.Fatalf("breadcrumb click selected %q, want %q", ps.sel, "folder:"+folder.RelPath)
	}
}

func hasSubstring(haystack, needle string) bool { return strings.Contains(haystack, needle) }

// --- UI smoke -------------------------------------------------------------

func TestWelcomeNewProjectWizardAndGlobalWorkspaceEntry(t *testing.T) {
	t.Setenv(core.GlobalWorkspaceDirEnv, filepath.Join(t.TempDir(), "global"))
	m, _ := newTestManager(t)
	m.showWelcome("")
	tt := ui.NewTester(m.welcome.view, 720, 480)
	tt.Frame()

	if err := tt.Click("New Project…"); err != nil {
		t.Fatalf("open new project wizard: %v", err)
	}
	if !tt.HasText("New Project") || !tt.HasText("Location") {
		t.Fatalf("wizard fields missing; texts=%v", tt.Texts())
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel wizard: %v", err)
	}
	if m.welcome.newProjOpen {
		t.Fatal("cancel should close the wizard")
	}

	if err := tt.Click("Personal Workspace"); err != nil {
		t.Fatalf("open global workspace: %v", err)
	}
	tt.Frame()
	if m.globalWorkspaceApp() == nil {
		t.Fatalf("global workspace did not open; status=%q", m.welcome.status)
	}
}

func TestSettingsSheetShowsWorkbenchPreferenceGroups(t *testing.T) {
	m, _ := newTestManager(t)
	m.showWelcome("")
	tt := ui.NewTester(m.welcome.view, 720, 480)
	tt.Frame()
	if err := tt.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Editor", "Auto-save", "Shortcuts", "Font size", "Enable safe auto-save"} {
		if !tt.HasText(want) {
			t.Fatalf("settings missing %q; texts=%v", want, tt.Texts())
		}
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatal(err)
	}
}

func TestTransferPreviewRendersTargetAndWarnings(t *testing.T) {
	t.Setenv(core.GlobalWorkspaceDirEnv, filepath.Join(t.TempDir(), "global"))
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "Source")); err != nil {
		t.Fatal(err)
	}
	src := m.workspaces[0]
	entry, err := src.ps.project.NewRequest("", "Needs Secret")
	if err != nil {
		t.Fatal(err)
	}
	req := *entry.Request
	req.HTTP.Headers = []core.KV{{Name: "Authorization", Value: "Bearer {{secret.token}}"}}
	req.Auth = &core.AuthRef{Mode: "ref", Ref: "missing"}
	if _, err := src.ps.project.SaveRequest(entry, &req, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	src.ps.openRequest(entry)
	global, err := m.ensureGlobalWorkspaceApp()
	if err != nil {
		t.Fatal(err)
	}
	src.beginTransfer(entry, global, core.TransferCopy)
	if src.transfer == nil {
		t.Fatalf("no preview: %s", src.status)
	}

	tt := ui.NewTester(src.View, 1200, 820)
	tt.Frame()
	for _, want := range []string{"Cross-workspace Transfer Preview", "Target file", "auth scheme", "token"} {
		if !tt.HasText(want) {
			t.Fatalf("preview missing %q; texts=%v", want, tt.Texts())
		}
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatal(err)
	}
	if src.transfer != nil {
		t.Fatal("cancel should discard the preview")
	}
}

func TestDuplicateDocCopiesContentWithoutTouchingSource(t *testing.T) {
	a, ps := openTestProject(t, "Docs")
	original, err := ps.newDoc()
	if err != nil {
		t.Fatal(err)
	}
	content := []byte("# Original\n\nKeep me.\n")
	if _, err := ps.project.SaveDoc(original.RelPath, content, original.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	before := len(ps.project.Docs)
	a.duplicateDoc(original)
	if len(ps.project.Docs) != before+1 {
		t.Fatalf("docs = %d, want %d", len(ps.project.Docs), before+1)
	}
	var copyRel string
	for _, d := range ps.project.Docs {
		if d.RelPath != original.RelPath && strings.Contains(d.Name, "copy") {
			copyRel = d.RelPath
		}
	}
	if copyRel == "" {
		t.Fatal("duplicate document not registered")
	}
	got, _, err := ps.project.Store.Read(copyRel)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(content) {
		t.Fatalf("duplicate content = %q", got)
	}
	src, _, err := ps.project.Store.Read(original.RelPath)
	if err != nil || string(src) != string(content) {
		t.Fatalf("source document changed: %q (%v)", src, err)
	}
}
