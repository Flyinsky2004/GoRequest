package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// projectModeTestWindow opens the welcome window and its new-project sheet in
// a tester of the given size.
func projectModeTestWindow(t *testing.T, m *Manager, w, h int) *ui.Tester {
	t.Helper()
	m.showWelcome("")
	tt := ui.NewTester(m.welcome.view, w, h)
	tt.Frame()
	if err := tt.Click("New Project…"); err != nil {
		t.Fatalf("open new project sheet: %v", err)
	}
	tt.Frame()
	return tt
}

func resolvedPath(t *testing.T, path string) string {
	t.Helper()
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return resolved
	}
	return path
}

func TestWelcomeNewProjectShowsTwoPathModes(t *testing.T) {
	m, _ := newTestManager(t)
	tt := projectModeTestWindow(t, m, 720, 480)

	for _, want := range []string{
		"Project type", "Independent project", "Alongside existing code",
		"Final location", "<parent>/<name>/.gorequest",
	} {
		if !tt.HasText(want) {
			t.Fatalf("sheet missing %q; texts=%v", want, tt.Texts())
		}
	}
	if m.welcome.newProjMode != newProjectIndependent {
		t.Fatalf("default mode = %v, want independent", m.welcome.newProjMode)
	}
	// The independent mode asks for a name and a parent folder.
	if !tt.HasText("Name") || !tt.HasText("Location") {
		t.Fatalf("independent fields missing; texts=%v", tt.Texts())
	}

	if err := tt.Click("Alongside existing code"); err != nil {
		t.Fatalf("choose coexistence mode: %v", err)
	}
	tt.Frame()
	if m.welcome.newProjMode != newProjectExisting {
		t.Fatalf("mode = %v, want existing", m.welcome.newProjMode)
	}
	if !tt.HasText("Code folder") || !tt.HasText("<code folder>/.gorequest") {
		t.Fatalf("coexistence fields missing; texts=%v", tt.Texts())
	}
	if tt.HasText("Name") {
		t.Fatal("coexistence mode must not ask for a project name")
	}
}

func TestWelcomeCreatesIndependentProjectWithChineseName(t *testing.T) {
	m, _ := newTestManager(t)
	tt := projectModeTestWindow(t, m, 720, 480)
	parent := t.TempDir()

	m.welcome.newProjName = "订单 API"
	m.welcome.newProjParent = parent
	tt.Frame()
	wantPath := filepath.Join(parent, "订单 API", ".gorequest")
	if !tt.HasText(wantPath) {
		t.Fatalf("final path preview missing %q; texts=%v", wantPath, tt.Texts())
	}

	if err := tt.Click("Create"); err != nil {
		t.Fatalf("create: %v", err)
	}
	tt.Frame()
	if m.welcome.newProjOpen {
		t.Fatalf("sheet stayed open after create: %s", m.welcome.newProjErr)
	}
	root := filepath.Join(parent, "订单 API")
	if _, err := os.Stat(filepath.Join(root, ".gorequest", "project.yaml")); err != nil {
		t.Fatalf("project not created: %v", err)
	}
	if m.findWorkspace(resolvedPath(t, root)) == nil {
		t.Fatal("created project did not open a workspace")
	}
}

func TestWelcomeCreatesProjectAlongsideExistingCode(t *testing.T) {
	m, _ := newTestManager(t)
	tt := projectModeTestWindow(t, m, 720, 480)
	dir := t.TempDir()
	code := filepath.Join(dir, "main.go")
	if err := os.WriteFile(code, []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	head := filepath.Join(dir, ".git", "HEAD")
	if err := os.MkdirAll(filepath.Dir(head), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(head, []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := tt.Click("Alongside existing code"); err != nil {
		t.Fatal(err)
	}
	m.welcome.newProjDir = dir
	tt.Frame()
	if !tt.HasText(filepath.Join(dir, ".gorequest")) {
		t.Fatalf("final path preview missing; texts=%v", tt.Texts())
	}
	if tt.HasText(filepath.Join(dir, filepath.Base(dir), ".gorequest")) {
		t.Fatal("preview must not nest a project-name folder")
	}

	if err := tt.Click("Create"); err != nil {
		t.Fatalf("create: %v", err)
	}
	tt.Frame()
	if m.welcome.newProjOpen {
		t.Fatalf("sheet stayed open: %s", m.welcome.newProjErr)
	}
	if _, err := os.Stat(filepath.Join(dir, ".gorequest", "project.yaml")); err != nil {
		t.Fatalf(".gorequest not created in the code root: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(dir))); !os.IsNotExist(err) {
		t.Fatalf("a nested project folder was created: %v", err)
	}
	if got, err := os.ReadFile(code); err != nil || string(got) != "package main\n" {
		t.Fatalf("existing code changed: %q (%v)", got, err)
	}
	if got, err := os.ReadFile(head); err != nil || string(got) != "ref: refs/heads/main\n" {
		t.Fatalf("existing .git changed: %q (%v)", got, err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	if m.findWorkspace(resolvedPath(t, dir)) == nil {
		t.Fatal("coexistence project did not open a workspace")
	}
}

func TestWelcomeRefusesExistingProjectAndOffersOpen(t *testing.T) {
	dir := t.TempDir()
	if _, err := core.CreateProjectInPlace(dir); err != nil {
		t.Fatal(err)
	}
	projectFile := filepath.Join(dir, ".gorequest", "project.yaml")
	before, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	tt := projectModeTestWindow(t, m, 720, 480)
	if err := tt.Click("Alongside existing code"); err != nil {
		t.Fatal(err)
	}
	m.welcome.newProjDir = dir
	tt.Frame()
	if err := tt.Click("Create"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()

	if m.welcome.newProjOpenPath != dir {
		t.Fatalf("open path = %q, want %q (err=%q)", m.welcome.newProjOpenPath, dir, m.welcome.newProjErr)
	}
	if !tt.HasText("already has .gorequest") || !tt.HasText("Open It") {
		t.Fatalf("refusal hint missing; texts=%v", tt.Texts())
	}
	after, err := os.ReadFile(projectFile)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("refused create rewrote the existing project")
	}

	if err := tt.Click("Open It"); err != nil {
		t.Fatalf("open existing: %v", err)
	}
	tt.Frame()
	if m.welcome.newProjOpen {
		t.Fatal("sheet should close after opening the existing project")
	}
	if m.findWorkspace(resolvedPath(t, dir)) == nil {
		t.Fatal("existing project did not open")
	}
}

func TestWelcomeNewProjectFitsSmallWindow(t *testing.T) {
	m, _ := newTestManager(t)
	const w, h = 560, 360
	tt := projectModeTestWindow(t, m, w, h)

	for _, label := range []string{"Independent project", "Alongside existing code", "Create", "Cancel"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing at %d×%d; texts=%v", label, w, h, tt.Texts())
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > w || r.Y+r.H > h {
			t.Fatalf("%q is clipped at %d×%d: %+v", label, w, h, r)
		}
	}
	left, _ := tt.Find("Independent project")
	right, _ := tt.Find("Alongside existing code")
	if left.X >= right.X {
		t.Fatalf("mode cards should be side by side: left=%+v right=%+v", left, right)
	}
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("Create must stay clickable in a small window: %v", err)
	}
	if m.welcome.newProjErr == "" {
		t.Fatal("empty create should report a validation error, not close")
	}
}

func TestWelcomeProjectModeCardsSwitchWithKeyboard(t *testing.T) {
	m, _ := newTestManager(t)
	tt := projectModeTestWindow(t, m, 720, 480)

	// Clicking a card gives it the keyboard focus; the arrow keys then switch
	// modes without touching the pointer again.
	if err := tt.Click("Independent project"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyRight)
	tt.Frame()
	if m.welcome.newProjMode != newProjectExisting {
		t.Fatalf("Right did not switch to coexistence: %v", m.welcome.newProjMode)
	}
	tt.Key(0, ui.KeyLeft)
	tt.Frame()
	if m.welcome.newProjMode != newProjectIndependent {
		t.Fatalf("Left did not switch to independent: %v", m.welcome.newProjMode)
	}
}
