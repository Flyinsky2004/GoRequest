package desktop

import (
	"path/filepath"
	"testing"
)

func newTestApp(t *testing.T) *App {
	t.Helper()
	return &App{
		cfg:      newConfigStore(filepath.Join(t.TempDir(), "config.json")),
		navWidth: 260,
	}
}

func TestOpenProjectFailsWithoutBreakingState(t *testing.T) {
	a := newTestApp(t)
	dir := writeProject(t, "One")
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	ps := a.current
	if len(a.cfg.recent()) != 1 {
		t.Fatalf("want 1 recent project, got %d", len(a.cfg.recent()))
	}

	err := a.openProject(filepath.Join(t.TempDir(), "does-not-exist"))
	if err == nil {
		t.Fatal("expected an error opening a missing project")
	}
	if a.current != ps {
		t.Fatal("a failed open must not change the current project")
	}
	if len(a.cfg.recent()) != 1 {
		t.Fatalf("a failed open must not add to recents, got %d", len(a.cfg.recent()))
	}
}

func TestDuplicateOpenDoesNotDuplicate(t *testing.T) {
	a := newTestApp(t)
	dir := writeProject(t, "One")
	for _, p := range []string{dir, filepath.Join(dir, ".gorequest"), filepath.Join(dir, ".")} {
		if err := a.openProject(p); err != nil {
			t.Fatalf("openProject(%q): %v", p, err)
		}
	}
	if len(a.projects) != 1 {
		t.Fatalf("want 1 loaded project, got %d", len(a.projects))
	}
	if len(a.cfg.recent()) != 1 {
		t.Fatalf("want 1 recent entry, got %d", len(a.cfg.recent()))
	}
}

func TestProjectSwitchPreservesTabsEditsAndEnv(t *testing.T) {
	a := newTestApp(t)
	dir1 := writeProject(t, "One")
	dir2 := writeProject(t, "Two")

	if err := a.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	ps1 := a.current
	if _, err := ps1.newRequest(); err != nil {
		t.Fatal(err)
	}
	if err := ps1.openDoc("docs/README.md"); err != nil {
		t.Fatal(err)
	}
	ps1.tabs[0].Dirty = true
	ps1.envSel = "(none)"
	tabsBefore := len(ps1.tabs)

	if err := a.openProject(dir2); err != nil {
		t.Fatal(err)
	}
	if a.current == ps1 {
		t.Fatal("switching should change the current project")
	}
	if len(a.current.tabs) != 0 {
		t.Fatalf("a newly opened project should start with no tabs, got %d", len(a.current.tabs))
	}

	// Returning to the first project must reactivate the same state, not reload.
	if err := a.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	if a.current != ps1 {
		t.Fatal("reopening an already-loaded project should reactivate its state")
	}
	if len(ps1.tabs) != tabsBefore {
		t.Fatalf("tabs lost on switch: want %d, got %d", tabsBefore, len(ps1.tabs))
	}
	if !ps1.tabs[0].Dirty {
		t.Fatal("unsaved edit flag lost on switch")
	}
	if ps1.envSel != "(none)" {
		t.Fatalf("environment selection lost on switch: %q", ps1.envSel)
	}
	if len(a.projects) != 2 {
		t.Fatalf("want 2 project states, got %d", len(a.projects))
	}
}

func TestChooseProjectRevertsSelectionOnFailure(t *testing.T) {
	a := newTestApp(t)
	dir := writeProject(t, "One")
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	ps := a.current

	// Simulate a stale recent entry: the list points at a directory that no
	// longer exists, and the user clicks it.
	a.cfg.cfg.RecentProjects = append(a.cfg.cfg.RecentProjects, recentProject{Path: "/no/such/project", Name: "Gone"})
	a.projectSel = "project:/no/such/project"
	a.chooseProject()

	if a.current != ps {
		t.Fatal("a failed switch must keep the current project")
	}
	if want := "project:" + ps.root; a.projectSel != want {
		t.Fatalf("selection should revert to current project: want %q, got %q", want, a.projectSel)
	}
	if len(a.cfg.recent()) != 2 {
		t.Fatalf("a failed switch must not remove or add recents, got %d", len(a.cfg.recent()))
	}
}

func TestAllRequestsIncludesCreated(t *testing.T) {
	a := newTestApp(t)
	dir := writeProject(t, "One")
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	ps := a.current
	if got := len(allRequests(ps.project)); got != 0 {
		t.Fatalf("new project should have no requests, got %d", got)
	}
	entry, err := ps.newRequest()
	if err != nil {
		t.Fatal(err)
	}
	reqs := allRequests(ps.project)
	if len(reqs) != 1 || reqs[0] != entry {
		t.Fatalf("newly created request missing from sidebar list: %+v", reqs)
	}
}

func TestRecentsRestoreAcrossRestart(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	dir1 := writeProject(t, "One")
	dir2 := writeProject(t, "Two")

	a1 := &App{cfg: newConfigStore(cfgPath), navWidth: 260}
	if err := a1.openProject(dir1); err != nil {
		t.Fatal(err)
	}
	if err := a1.openProject(dir2); err != nil {
		t.Fatal(err)
	}

	// A fresh app with the same config file is a restart.
	a2 := &App{cfg: newConfigStore(cfgPath), navWidth: 260}
	rec := a2.cfg.recent()
	if len(rec) != 2 {
		t.Fatalf("want 2 restored recents, got %d", len(rec))
	}
	want2, _ := normalizeProjectPath(dir2)
	want1, _ := normalizeProjectPath(dir1)
	if rec[0].Path != want2 || rec[0].Name != "Two" {
		t.Fatalf("newest restored project mismatch: %+v", rec[0])
	}
	if rec[1].Path != want1 || rec[1].Name != "One" {
		t.Fatalf("older restored project mismatch: %+v", rec[1])
	}
}
