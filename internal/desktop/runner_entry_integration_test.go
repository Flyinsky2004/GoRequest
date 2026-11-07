//go:build a02_integration

package desktop

import (
	"github.com/egoist/mygo/ui"
	"testing"
)

// This gate intentionally requires the public entry patch. It renders the
// production App.View, not an isolated Runner wrapper.
func TestA02PatchedAppRunnerEntry(t *testing.T) {
	a, _ := openTestProject(t, "Runner entry integration")
	tt := ui.NewTester(a.View, 1280, 1000)
	tt.Frame()
	if err := tt.Click(a.ps.name()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem(a.runnerMsg().Title); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(a.runnerMsg().Start) || !tt.HasText(a.runnerMsg().Snapshot) {
		t.Fatal("App.View does not render reachable runner")
	}
}
