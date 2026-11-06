package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

// These tests drive the real rendered view with ui.NewTester (no window), so the
// folder tree, the tab strip's close buttons and the "Open Tabs" menu are
// exercised through their actual widgets rather than a mirrored model.

func TestFolderTreeCreateAndOpenThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	// Create a folder through the real "+" new menu and modal.
	if err := tt.Click("+"); err != nil {
		t.Fatalf("open new menu: %v", err)
	}
	if err := tt.ChooseMenuItem("New Folder"); err != nil {
		t.Fatalf("choose New Folder: %v", err)
	}
	if !tt.HasText("New Folder") {
		t.Fatalf("new folder dialog not shown; texts=%v", tt.Texts())
	}
	a.newFolderName = "Alpha"
	if err := tt.Click("Create Folder"); err != nil {
		t.Fatalf("click Create Folder: %v", err)
	}
	if len(ps.project.Collections) != 1 {
		t.Fatalf("folder not created; tree=%+v", ps.project.Collections)
	}
	if ps.sel != "folder:collections/alpha" {
		t.Fatalf("new folder not selected as target: %q", ps.sel)
	}
	tt.Frame()
	if !tt.HasText("Alpha") {
		t.Fatalf("folder not visible in tree; texts=%v", tt.Texts())
	}

	// The new-request form targets the selected folder.
	if err := tt.Click("New Request"); err != nil {
		t.Fatalf("click New Request: %v", err)
	}
	if !tt.HasText("Target folder: alpha") {
		t.Fatalf("new request form should show the selected folder; texts=%v", tt.Texts())
	}
	a.newReqName = "Ping"
	if err := tt.Click("Create"); err != nil {
		t.Fatalf("click Create request: %v", err)
	}
	if len(ps.tabs) != 1 {
		t.Fatalf("new request tab not opened, got %d tabs", len(ps.tabs))
	}
	if tab := ps.activeTab(); tab == nil || tab.Entry == nil || tab.Entry.RelPath != "collections/alpha/ping.request.yaml" {
		t.Fatalf("request created in wrong place: %+v", ps.activeTab())
	}
	tt.Frame()
	if !tt.HasText("Ping") {
		t.Fatalf("request not shown under its folder; texts=%v", tt.Texts())
	}

	// Clicking the folder row collapses it (real widget interaction).
	if err := tt.Click("Alpha"); err != nil {
		t.Fatalf("click folder row: %v", err)
	}
	if ps.expanded["collections/alpha"] {
		t.Fatal("clicking the folder should collapse it")
	}
	tt.Frame()
	if !tt.HasText("Alpha") {
		t.Fatalf("collapsed folder row missing; texts=%v", tt.Texts())
	}
}

func TestTabBarCloseButtonThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta")
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if !tt.HasText(tabDisplayLabel(tabs[0])) || !tt.HasText(tabDisplayLabel(tabs[1])) {
		t.Fatalf("tab labels missing; texts=%v", tt.Texts())
	}

	// The per-tab close button has a unique accessibility label.
	if err := tt.Click("Close " + tabIdentity(tabs[0])); err != nil {
		t.Fatalf("click close button: %v", err)
	}
	if ps.indexOfTab(tabs[0]) != -1 {
		t.Fatal("clicking the tab close button should close that tab")
	}
	if len(ps.tabs) != 1 || ps.activeTab() != tabs[1] {
		t.Fatalf("the remaining tab should stay active: %v", ps.tabs)
	}
}

func TestOpenedTabsMenuSwitchThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	// The menu only appears once the strip holds more than two tabs, so open
	// seven and drive the menu as a normal overflow case.
	tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma", "Delta", "Epsilon", "Zeta", "Eta")
	ps.active = 0
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	if err := tt.Click("Open Tabs (7)"); err != nil {
		t.Fatalf("open tabs menu: %v", err)
	}
	items := tt.Menu()
	if len(items) == 0 {
		t.Fatal("the Open Tabs menu should list items")
	}
	if err := tt.ChooseMenuItem(tabDisplayLabel(tabs[1]), "Switch"); err != nil {
		t.Fatalf("switch to Beta through menu: %v; menu=%v", err, items)
	}
	if ps.activeTab() != tabs[1] {
		t.Fatal("menu switch should activate Beta")
	}

	// Closing a specific tab from the opened list.
	if err := tt.Click("Open Tabs (7)"); err != nil {
		t.Fatalf("reopen tabs menu: %v", err)
	}
	if err := tt.ChooseMenuItem(tabDisplayLabel(tabs[0]), "Close"); err != nil {
		t.Fatalf("close Alpha through menu: %v", err)
	}
	if ps.indexOfTab(tabs[0]) != -1 {
		t.Fatal("menu close should remove Alpha")
	}
}

func TestManyTabsNarrowWindowDoesNotCrash(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	names := []string{"A", "B", "C", "D", "E", "F", "G", "H", "I", "J", "K", "L"}
	openRequestTabs(t, ps, names...)
	tt := ui.NewTester(a.View, 760, 560)
	tt.Frame()
	// The active tab must remain reachable even when the strip overflows.
	if ps.activeTab() == nil {
		t.Fatal("active tab lost in narrow window")
	}
	tt.SetSize(640, 480)
	tt.Frame()
}

func TestMoveThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	if _, err := ps.project.NewFolder("", "Group"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if err := tt.Click("Demo"); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	if err := tt.ChooseMenuItem("Move…"); err != nil {
		t.Fatalf("choose Move…: %v", err)
	}
	if !tt.HasText("Move Request") {
		t.Fatalf("move dialog not shown; texts=%v", tt.Texts())
	}
	a.moveTarget = "group"
	if err := tt.Click("Move"); err != nil {
		t.Fatalf("click Move: %v", err)
	}
	if a.moveOpen {
		t.Fatal("move dialog should close after a successful move")
	}
	if got := filepath.ToSlash(filepath.Dir(tab.Entry.RelPath)); got != "collections/group" {
		t.Fatalf("request not moved through the dialog: %q", tab.Entry.RelPath)
	}
}

func TestDirtyCloseModalThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	tab.URL = "http://changed"
	tab.Dirty = true
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if err := tt.Click("Close " + tabIdentity(tab)); err != nil {
		t.Fatalf("click close: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Unsaved Changes") {
		t.Fatalf("dirty close should show a decision dialog; texts=%v", tt.Texts())
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatalf("click Discard: %v", err)
	}
	if ps.indexOfTab(tab) != -1 {
		t.Fatal("Discard should close the dirty tab")
	}
}

// TestSidebarFilterThroughView proves the filter box filters the tree by input,
// hiding non-matching requests and reporting the hit count (§3).
func TestSidebarFilterThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	if _, err := ps.project.NewRequest("", "Alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.project.NewRequest("", "Beta"); err != nil {
		t.Fatal(err)
	}

	a.filterQuery = "alp"
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if !tt.HasText("Alpha") {
		t.Fatalf("matching request should remain; texts=%v", tt.Texts())
	}
	if tt.HasText("Beta") {
		t.Fatalf("non-matching request should be hidden; texts=%v", tt.Texts())
	}
	if n := a.matchCount(); n != 1 {
		t.Fatalf("match count = %d, want 1", n)
	}

	// Clearing the query restores the full tree.
	a.filterQuery = ""
	tt.Frame()
	if !tt.HasText("Beta") {
		t.Fatalf("cleared filter should show all requests; texts=%v", tt.Texts())
	}
}

// TestSectionCollapseThroughView proves clicking a section header folds it (§3).
func TestSectionCollapseThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	if _, err := ps.project.NewRequest("", "Alpha"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if !tt.HasText("Alpha") {
		t.Fatalf("request should be visible before folding; texts=%v", tt.Texts())
	}
	if err := tt.Click("section:Requests"); err != nil {
		t.Fatalf("click Requests header: %v", err)
	}
	tt.Frame()
	if !ps.collapsed["requests"] {
		t.Fatal("clicking the Requests header should fold the section")
	}
	if tt.HasText("Alpha") {
		t.Fatalf("folded section should hide its requests; texts=%v", tt.Texts())
	}
}
