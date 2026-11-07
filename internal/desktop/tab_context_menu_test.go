package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// tabBarTester renders just the tab strip. A right-click on a tab label then
// cannot land on the sidebar's same-named row, which would open the tree menu.
func tabBarTester(a *App, ps *projectState) *ui.Tester {
	theme := ui.LightTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Fill().Children(func() {
			a.tabBar(c, theme, ps)
			ui.Text(c, "content boundary")
		})
	}, 760, 120)
	tt.Frame()
	return tt
}

// TestTabRightClickOpensContextMenu proves the secondary button on a tab opens
// the VSCode-like close menu and targets the tab under the pointer.
func TestTabRightClickOpensContextMenu(t *testing.T) {
	a, ps := openTestProject(t, "TabMenu")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
	ps.active = 2 // Gamma active; right-clicking Alpha must retarget it.
	tt := tabBarTester(a, ps)

	if err := tt.RightClick(tabDisplayLabel(tabs[0])); err != nil {
		t.Fatalf("right-click tab: %v", err)
	}
	got := tt.Menu()
	want := []string{"Close", "Close Others", "Close to the Right", "Close All"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("tab context menu = %v, want %v", got, want)
	}
	if ps.activeTab() != tabs[0] {
		t.Fatalf("right-click should activate the tab; active = %q", ps.activeTab().Title)
	}
}

// TestTabContextMenuCloseOthers proves Close Others keeps exactly the
// right-clicked tab.
func TestTabContextMenuCloseOthers(t *testing.T) {
	a, ps := openTestProject(t, "TabMenuOthers")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
	tt := tabBarTester(a, ps)

	if err := tt.RightClick(tabDisplayLabel(tabs[1])); err != nil {
		t.Fatalf("right-click tab: %v", err)
	}
	if err := tt.ChooseMenuItem("Close Others"); err != nil {
		t.Fatalf("choose Close Others: %v", err)
	}
	if len(ps.tabs) != 1 || ps.tabs[0] != tabs[1] {
		t.Fatalf("Close Others should keep only Beta, got %v", tabTitles(ps))
	}
	if ps.activeTab() != tabs[1] {
		t.Fatalf("the kept tab should be active, got %q", ps.activeTab().Title)
	}
}

// TestTabContextMenuCloseRight proves Close to the Right closes exactly the
// tabs after the right-clicked one.
func TestTabContextMenuCloseRight(t *testing.T) {
	a, ps := openTestProject(t, "TabMenuRight")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma", "Delta")
	tt := tabBarTester(a, ps)

	if err := tt.RightClick(tabDisplayLabel(tabs[1])); err != nil {
		t.Fatalf("right-click tab: %v", err)
	}
	if err := tt.ChooseMenuItem("Close to the Right"); err != nil {
		t.Fatalf("choose Close to the Right: %v", err)
	}
	if len(ps.tabs) != 2 || ps.tabs[0] != tabs[0] || ps.tabs[1] != tabs[1] {
		t.Fatalf("Close to the Right should keep Alpha and Beta, got %v", tabTitles(ps))
	}
}

// TestTabContextMenuCloseRightDisabledOnLastTab proves the action that would
// do nothing is disabled rather than closing anything.
func TestTabContextMenuCloseRightDisabledOnLastTab(t *testing.T) {
	a, ps := openTestProject(t, "TabMenuLast")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta")
	tt := tabBarTester(a, ps)

	if err := tt.RightClick(tabDisplayLabel(tabs[1])); err != nil {
		t.Fatalf("right-click tab: %v", err)
	}
	err := tt.ChooseMenuItem("Close to the Right")
	if err == nil || !strings.Contains(err.Error(), "disabled") {
		t.Fatalf("Close to the Right on the last tab should be disabled, got %v", err)
	}
	tt.CloseMenu()
	if len(ps.tabs) != 2 {
		t.Fatalf("a disabled action must close nothing, got %v", tabTitles(ps))
	}
}

// TestTabContextMenuDirtyPausesForDecision proves the menu's bulk close still
// goes through the Save/Discard/Cancel flow: clean tabs close, the dirty tab
// pauses the flow and shows the dialog.
func TestTabContextMenuDirtyPausesForDecision(t *testing.T) {
	a, ps := openTestProject(t, "TabMenuDirty")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
	tabs[2].Dirty = true
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	if err := tt.RightClick(tabDisplayLabel(tabs[0])); err != nil {
		t.Fatalf("right-click tab: %v", err)
	}
	if err := tt.ChooseMenuItem("Close to the Right"); err != nil {
		t.Fatalf("choose Close to the Right: %v", err)
	}
	tt.Frame()

	if ps.indexOfTab(tabs[1]) >= 0 {
		t.Fatal("the clean tab to the right should have closed")
	}
	if ps.indexOfTab(tabs[2]) < 0 {
		t.Fatal("the dirty tab must stay open until decided")
	}
	if a.closeFlow == nil || a.closeFlow.dirty != tabs[2] {
		t.Fatal("the dirty tab should pause the close flow")
	}
	for _, want := range []string{"Save & Close", "Discard", "Cancel"} {
		if !hasExactLabel(tt, want) {
			t.Fatalf("close dialog missing %q; texts=%v", want, tt.Texts())
		}
	}

	// Cancel keeps the dirty tab and its edits.
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("cancel close: %v", err)
	}
	tt.Frame()
	if ps.indexOfTab(tabs[2]) < 0 || !tabs[2].Dirty {
		t.Fatal("Cancel must keep the dirty tab open with its edits")
	}
	if a.closeFlow != nil {
		t.Fatal("Cancel should end the close flow")
	}
}

// tabTitles lists a project's tab titles for failure messages.
func tabTitles(ps *projectState) []string {
	out := make([]string, len(ps.tabs))
	for i, tab := range ps.tabs {
		out[i] = tab.Title
	}
	return out
}
