package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// tabBarTester renders just the tab strip. A right-click on a tab label then
// cannot land on the sidebar's same-named row, which would open the tree menu.
func tabBarTester(a *App, ps *projectState, dark ...bool) *ui.Tester {
	isDark := len(dark) > 0 && dark[0]
	theme := uikit.Theme(uikit.PaletteFor(&ui.Theme{Dark: isDark}), isDark)
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Fill().Children(func() {
			a.tabBar(c, theme, ps)
			ui.Text(c, "content boundary")
		})
	}, 760, 320)
	tt.Frame()
	return tt
}

// TestTabRightClickOpensContextMenu proves the secondary button on a tab opens
// the VSCode-like close menu and targets the tab under the pointer.
func TestTabRightClickOpensContextMenu(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			a, ps := openTestProject(t, "TabMenu")
			tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
			ps.active = 2 // Gamma active; right-clicking Alpha must retarget it.
			tt := tabBarTester(a, ps, dark)

			if err := tt.RightClick(tabDisplayLabel(tabs[0])); err != nil {
				t.Fatalf("right-click tab: %v", err)
			}
			if got := tt.Menu(); got != nil {
				t.Fatalf("tab context menu must be drawn in the window, got native menu %v", got)
			}
			for _, want := range []string{"Close", "Close Others", "Close to the Right", "Close All"} {
				if !tt.HasText(want) {
					t.Fatalf("tab context menu missing %q; texts=%v", want, tt.Texts())
				}
			}
			if ps.activeTab() != tabs[0] {
				t.Fatalf("right-click should activate the tab; active = %q", ps.activeTab().Title)
			}
			r, ok := tt.Find("Close Others")
			if !ok {
				t.Fatal("drawn menu item missing")
			}
			got := tt.Image().RGBAAt(int(r.X+2), int(r.Y+r.H/2))
			want := uikit.PaletteFor(&ui.Theme{Dark: dark}).BG
			if got.R != want.R || got.G != want.G || got.B != want.B {
				t.Fatalf("menu surface = %v, want theme background %v", got, want)
			}
			tt.Key(0, ui.KeyEscape)
			if tt.HasText("Close Others") {
				t.Fatal("Escape left the tab menu open")
			}
			if err := tt.RightClick(tabDisplayLabel(tabs[0])); err != nil {
				t.Fatal(err)
			}
			tt.ClickAt(740, 300)
			if tt.HasText("Close Others") {
				t.Fatal("outside click left the tab menu open")
			}
		})
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
	if err := tt.Click("Close Others"); err != nil {
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
	if err := tt.Click("Close to the Right"); err != nil {
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
	if err := tt.Click("Close to the Right"); err != nil {
		t.Fatalf("click disabled item: %v", err)
	}
	tt.Key(0, ui.KeyEscape)
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
	if err := tt.Click("Close to the Right"); err != nil {
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
