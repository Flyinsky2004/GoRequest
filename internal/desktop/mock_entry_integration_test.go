//go:build c01_integration

package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestC01PatchedAppMockEntry verifies that when the public C01-phase1 integration
// patch is applied to App.View and App.buildProjectMenu, the Local Mock Server
// modal is reachable directly from the production Project menu.
func TestC01PatchedAppMockEntry(t *testing.T) {
	a, _ := openTestProject(t, "Mock entry integration")
	tt := ui.NewTester(a.View, 1280, 1000)
	tt.Frame()
	if err := tt.Click(a.ps.name()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem(mockT(a, "menu.project.mock_server")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(mockT(a, "mock.modal.title")) || !tt.HasText(mockT(a, "mock.action.start")) {
		t.Fatal("App.View does not render reachable Local Mock Server modal")
	}
}
