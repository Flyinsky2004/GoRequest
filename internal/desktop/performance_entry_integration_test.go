//go:build c02_integration

package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestC02PatchedAppPerformanceEntry verifies that the public wiring patch
// exposes the HTTP Performance Diagnostics modal from the production App.View
// Project menu.
func TestC02PatchedAppPerformanceEntry(t *testing.T) {
	a, _ := openTestProject(t, "Performance entry integration")
	tt := ui.NewTester(a.View, 1280, 1000)
	tt.Frame()
	if err := tt.Click(a.ps.name()); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.ChooseMenuItem(a.perfMsg().MenuLabel); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText(a.perfMsg().Start) || !tt.HasText(a.perfMsg().ScopeNotice) {
		t.Fatal("App.View does not render reachable performance diagnostics modal")
	}
}
