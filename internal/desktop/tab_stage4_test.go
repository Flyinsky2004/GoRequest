package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// doubleClickTab clicks a tab's activation area twice with the tester's clock
// standing still, which the engine counts as a double click (§4 rename).
func doubleClickTab(t *testing.T, tt *ui.Tester, tab *Tab) {
	t.Helper()
	label := tabDisplayLabel(tab)
	if err := tt.Click(label); err != nil {
		t.Fatalf("first click on %q: %v", label, err)
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("second click on %q: %v", label, err)
	}
}

// Double-clicking a tab name opens the in-place editor; typing a new name and
// pressing Enter renames the request and marks the tab dirty, so the normal
// save path writes the new name back.
func TestTabDoubleClickRenameCommitsOnEnter(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	doubleClickTab(t, tt, tab)
	if !tab.renaming {
		t.Fatalf("double click should start renaming; texts=%v", tt.Texts())
	}
	if tab.renameText != "Alpha" {
		t.Fatalf("rename draft should start at the title, got %q", tab.renameText)
	}

	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Renamed")
	tt.Key(0, ui.KeyEnter)

	if tab.renaming {
		t.Fatal("Enter should close the rename editor")
	}
	if tab.Title != "Renamed" {
		t.Fatalf("Enter should commit the typed title, got %q", tab.Title)
	}
	if !tab.Dirty {
		t.Fatal("a rename should mark the tab dirty")
	}
	// requestFromTab does not copy Tab.Title into Request.Name, so the commit
	// syncs it; otherwise SaveRequest would persist the old name.
	if tab.Entry == nil || tab.Entry.Request == nil || tab.Entry.Request.Name != "Renamed" {
		t.Fatalf("rename should reach the request model, got %+v", tab.Entry)
	}
	// Saving then writes the new name back through the normal path.
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save the renamed tab: %v", err)
	}
	if tab.Dirty {
		t.Fatal("save should clear the dirty flag")
	}
	if got := ps.project.Requests[tab.Entry.Request.ID]; got == nil || got.Request.Name != "Renamed" {
		t.Fatalf("save should persist the renamed request, got %+v", got)
	}
}

// Esc abandons the draft: the title and the request model stay untouched and
// the tab is not marked dirty.
func TestTabRenameEscapeCancels(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	doubleClickTab(t, tt, tab)
	if !tab.renaming {
		t.Fatal("double click should start renaming")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Discarded")
	tt.Key(0, ui.KeyEscape)

	if tab.renaming {
		t.Fatal("Esc should close the rename editor")
	}
	if tab.Title != "Alpha" || tab.Dirty {
		t.Fatalf("Esc should keep the title and the clean state, got %q dirty=%v", tab.Title, tab.Dirty)
	}
	if tab.Entry.Request.Name != "Alpha" {
		t.Fatalf("Esc should not touch the request model, got %q", tab.Entry.Request.Name)
	}
}

// The overflow menu is noise for one or two tabs: it only renders once there
// are more than two.
func TestOpenTabsMenuOnlyWithOverflow(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tt := ui.NewTester(a.View, 1100, 820)

	openRequestTabs(t, ps, "Alpha")
	tt.Frame()
	if tt.HasText("Open Tabs") {
		t.Fatalf("one tab should not render the Open Tabs menu; texts=%v", tt.Texts())
	}

	openRequestTabs(t, ps, "Beta")
	tt.Frame()
	if tt.HasText("Open Tabs") {
		t.Fatalf("two tabs should not render the Open Tabs menu; texts=%v", tt.Texts())
	}

	openRequestTabs(t, ps, "Gamma")
	tt.Frame()
	if !tt.HasText("Open Tabs (3)") {
		t.Fatalf("three tabs should render the Open Tabs menu; texts=%v", tt.Texts())
	}
}
