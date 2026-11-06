package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestWelcomeViewSmoke exercises the real welcome view headlessly: brand,
// recent list, Open Project action and the settings sheet, at the small
// launcher size.
func TestWelcomeViewSmoke(t *testing.T) {
	m, _ := newTestManager(t)
	m.showWelcome("")

	tt := ui.NewTester(m.welcome.view, 720, 480)
	if !tt.HasText("GoRequest") {
		t.Fatalf("brand missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("Open Project…") {
		t.Fatalf("open-project action missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("Recent Projects") {
		t.Fatalf("recent list heading missing; texts=%v", tt.Texts())
	}

	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if !tt.HasText("Appearance") {
		t.Fatalf("settings sheet did not open; texts=%v", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if m.welcome.showSettings {
		t.Fatal("settings should close after Done")
	}
}

// TestWelcomeSeparatedFromWorkspace verifies the welcome window does not
// render the editor workspace, and that opening a recent project replaces the
// chooser with a project window.
func TestWelcomeSeparatedFromWorkspace(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "Recent One")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	// Close the project so we return to the chooser with this recent recorded.
	m.workspaces[0].win.Close()
	if m.welcome == nil {
		t.Fatal("expected chooser after closing the only project")
	}

	tt := ui.NewTester(m.welcome.view, 720, 480)
	// The chooser is a light window: no editor-only controls.
	if tt.HasText("Params") || tt.HasText("Send") {
		t.Fatalf("welcome window should not show editor controls; texts=%v", tt.Texts())
	}
	label := m.recentLabel(m.cfg.recent()[0])
	if err := tt.Click(label); err != nil {
		t.Fatalf("click recent %q: %v", label, err)
	}
	if len(m.workspaces) != 1 {
		t.Fatalf("clicking a recent project should open it, workspaces=%d", len(m.workspaces))
	}
	if m.welcome.win.IsVisible() {
		t.Fatal("welcome should hide after opening a project")
	}
}

// TestWorkspaceViewSeparatedFromWelcome exercises the project window view: it
// shows project contents and settings but no global recent-project list.
func TestWorkspaceViewSeparatedFromWelcome(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "Project X")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	tt := ui.NewTester(app.View, 1200, 820)

	for _, want := range []string{"GoRequest", "Requests", "Documents", "Settings", "New Request"} {
		if !tt.HasText(want) {
			t.Fatalf("workspace missing %q; texts=%v", want, tt.Texts())
		}
	}
	// Close Project now lives in the project menu, not as a flat button.
	if err := tt.Click("Project X"); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	items := tt.Menu()
	hasClose := false
	for _, it := range items {
		if it == "Close Project" {
			hasClose = true
		}
	}
	if !hasClose {
		t.Fatalf("project menu missing Close Project: %v", items)
	}
	tt.CloseMenu()
	if tt.HasText("Recent Projects") {
		t.Fatalf("workspace should not show the global recent list; texts=%v", tt.Texts())
	}

	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("open workspace settings: %v", err)
	}
	if !tt.HasText("Appearance") {
		t.Fatalf("workspace settings missing expected controls; texts=%v", tt.Texts())
	}
	// The startup restore control lives under its own category in the new
	// left-rail layout (FIX05).
	if err := tt.Click("Startup"); err != nil {
		t.Fatalf("open startup category: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Restore last opened project on startup") {
		t.Fatalf("workspace settings missing expected controls; texts=%v", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("close settings: %v", err)
	}

	// Narrow window must still build.
	tt.SetSize(760, 560)
	tt.Frame()
}

// TestCloseGuardModalRendersAndDiscards renders the unsaved-changes modal and
// discards through it, exercising the guard's view path headlessly.
func TestCloseGuardModalRendersAndDiscards(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "Guarded")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	if _, err := app.ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	app.ps.activeTab().Dirty = true
	app.openCloseGuard(false)

	tt := ui.NewTester(app.View, 1200, 820)
	for _, want := range []string{"Unsaved changes", "Save All", "Discard", "Cancel"} {
		if !tt.HasText(want) {
			t.Fatalf("close guard missing %q; texts=%v", want, tt.Texts())
		}
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatalf("click discard: %v", err)
	}
	if !app.closed {
		t.Fatal("discard through the modal should close the window")
	}
}
