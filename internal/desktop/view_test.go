package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestViewSmoke exercises the real view without a window: empty state, opening
// a project, the settings sheet, and a narrow window. It checks that the layout
// builds and the navigation/settings entry points work end to end.
func TestViewSmoke(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1200, 820)

	if !tt.HasText("GoRequest") {
		t.Fatalf("navigation title missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("Open a project") {
		t.Fatalf("welcome empty state missing; texts=%v", tt.Texts())
	}

	dir := writeProject(t, "Example Project")
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Example Project") {
		t.Fatalf("project name not shown after open; texts=%v", tt.Texts())
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
	if a.showSettings {
		t.Fatal("settings should be closed after Done")
	}

	// A narrow window must still build without panicking.
	tt.SetSize(760, 560)
	tt.Frame()
}
