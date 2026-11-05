package desktop

import (
	"os"
	"path/filepath"
	"testing"

	"gorequest/internal/core"
)

// openTestProject opens a fresh project and returns the app and its state.
func openTestProject(t *testing.T, name string) (*App, *projectState) {
	t.Helper()
	a := newTestApp(t)
	dir := writeProject(t, name)
	if err := a.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	return a, a.current
}

// openRequestTabs creates n root requests named a.. and opens each tab.
func openRequestTabs(t *testing.T, ps *projectState, names ...string) []*Tab {
	t.Helper()
	tabs := make([]*Tab, 0, len(names))
	for _, name := range names {
		entry, err := ps.project.NewRequest("", name)
		if err != nil {
			t.Fatalf("NewRequest(%q): %v", name, err)
		}
		ps.openRequest(entry)
		tabs = append(tabs, ps.tabs[len(ps.tabs)-1])
	}
	return tabs
}

func TestOpenRequestReusesExistingTab(t *testing.T) {
	_, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta")
	if len(ps.tabs) != 2 {
		t.Fatalf("want 2 tabs, got %d", len(ps.tabs))
	}
	ps.active = 0
	ps.openRequest(tabs[1].Entry) // reopen Beta while Alpha is active
	if len(ps.tabs) != 2 {
		t.Fatalf("reopening must not add a tab, got %d", len(ps.tabs))
	}
	if ps.activeTab() != tabs[1] {
		t.Fatal("reopening an open request should activate its existing tab")
	}
}

func TestRemoveTabKeepsNeighbourIdentity(t *testing.T) {
	_, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
	a, b, c := tabs[0], tabs[1], tabs[2]

	// Close the active middle tab: a neighbour takes over.
	ps.active = 1
	ps.removeTab(b)
	if len(ps.tabs) != 2 || ps.indexOfTab(b) != -1 {
		t.Fatalf("middle tab not removed: %v", ps.tabs)
	}
	if ps.activeTab() != c {
		t.Fatalf("after closing active middle, expected Gamma active")
	}

	// Close a preceding tab while the active tab is later: identity preserved.
	ps.active = ps.indexOfTab(c)
	ps.removeTab(a)
	if ps.activeTab() != c {
		t.Fatal("closing a preceding tab must keep the same active tab")
	}

	// Close the last remaining tab: empty state, no panic.
	ps.removeTab(c)
	if len(ps.tabs) != 0 {
		t.Fatalf("want empty tabs, got %d", len(ps.tabs))
	}
	if ps.activeTab() != nil {
		t.Fatal("empty project should have no active tab")
	}
}

func TestCloseCurrentOthersAll(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	openRequestTabs(t, ps, "Alpha", "Beta", "Gamma")
	ps.active = 1

	a.closeOthers()
	if len(ps.tabs) != 1 || ps.activeTab().Title != "Beta" {
		t.Fatalf("close others kept the wrong tabs: %v", ps.tabs)
	}

	a.closeCurrent()
	if len(ps.tabs) != 0 {
		t.Fatalf("close current should empty the project, got %d", len(ps.tabs))
	}

	openRequestTabs(t, ps, "One", "Two")
	a.closeAll()
	if len(ps.tabs) != 0 {
		t.Fatalf("close all should empty the project, got %d", len(ps.tabs))
	}
}

func TestDirtyCloseSave(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	tab.URL = "http://127.0.0.1:4010/changed"
	tab.Dirty = true

	a.startClose(ps, []*Tab{tab})
	if a.closeFlow == nil || a.closeFlow.dirty != tab {
		t.Fatal("closing a dirty tab should prompt for a decision")
	}
	a.resolveCloseDirty(true) // Save
	if a.closeFlow != nil {
		t.Fatal("close flow should finish after saving")
	}
	if ps.indexOfTab(tab) != -1 {
		t.Fatal("dirty tab should be closed after Save")
	}
	// The saved edit must be on disk.
	abs := filepath.Join(ps.root, ".gorequest", filepath.FromSlash(tab.Entry.RelPath))
	if _, err := os.Stat(abs); err != nil {
		t.Fatalf("saved request missing: %v", err)
	}
	p2, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	got := p2.Requests[tab.Entry.Request.ID]
	if got == nil || got.Request.HTTP.URL != "http://127.0.0.1:4010/changed" {
		t.Fatalf("edited URL not saved: %+v", got)
	}
}

func TestDirtyCloseDiscard(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	tab.URL = "http://changed"
	tab.Dirty = true
	a.startClose(ps, []*Tab{tab})
	a.resolveCloseDirty(false) // Discard
	if ps.indexOfTab(tab) != -1 {
		t.Fatal("dirty tab should close after Discard")
	}
	p2, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	got := p2.Requests[tab.Entry.Request.ID]
	if got == nil || got.Request.HTTP.URL == "http://changed" {
		t.Fatalf("discard must not have written edits: %+v", got)
	}
}

func TestDirtyCloseCancelKeepsTab(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	tab.Dirty = true
	a.startClose(ps, []*Tab{tab})
	a.cancelClose()
	if ps.indexOfTab(tab) == -1 {
		t.Fatal("cancel must keep the tab open")
	}
	if !tab.Dirty {
		t.Fatal("cancel must not clear the dirty flag")
	}
	if a.closeFlow != nil {
		t.Fatal("cancel should end the close flow")
	}
}

func TestDirtyCloseSaveConflictKeepsTabOpen(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	tab.URL = "http://mine"
	tab.Dirty = true
	// External edit invalidates the baseline.
	abs := filepath.Join(ps.root, ".gorequest", filepath.FromSlash(tab.Entry.RelPath))
	if err := os.WriteFile(abs, []byte("external change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	a.startClose(ps, []*Tab{tab})
	a.resolveCloseDirty(true) // Save should conflict
	if ps.indexOfTab(tab) == -1 {
		t.Fatal("a conflicting save must not close the tab")
	}
	if !tab.Dirty {
		t.Fatal("tab should stay dirty after a failed save")
	}
	if a.closeFlow == nil || a.closeFlow.dirty != tab {
		t.Fatal("dialog should stay open after a save conflict")
	}
	if a.closeFlow.err == "" {
		t.Fatal("expected a conflict message")
	}
}

func TestBatchCloseCancelKeepsUndecided(t *testing.T) {
	a, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha", "Beta")
	first, second := tabs[0], tabs[1]
	first.Dirty = true
	second.Dirty = true
	a.startClose(ps, []*Tab{first, second})
	if a.closeFlow.dirty != first {
		t.Fatal("first dirty tab should be prompted first")
	}
	a.resolveCloseDirty(false) // discard first
	if ps.indexOfTab(first) != -1 {
		t.Fatal("discarded tab should be closed")
	}
	if a.closeFlow == nil || a.closeFlow.dirty != second {
		t.Fatal("second dirty tab should now be prompted")
	}
	a.cancelClose()
	if ps.indexOfTab(second) == -1 {
		t.Fatal("cancelling must leave the undecided tab open")
	}
	if !second.Dirty {
		t.Fatal("cancelled tab should keep its edits")
	}
}

func TestClosingSendingTabCancelsContext(t *testing.T) {
	_, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	canceled := false
	tab.Sending = true
	tab.cancel = func() { canceled = true }
	ps.removeTab(tab)
	if !canceled {
		t.Fatal("closing a sending tab must cancel its context")
	}
	if tab.Sending {
		t.Fatal("closed tab should no longer be sending")
	}
}

func TestProjectIsolationOnClose(t *testing.T) {
	a, ps1 := openTestProject(t, "One")
	openRequestTabs(t, ps1, "Alpha", "Beta")
	dir2 := writeProject(t, "Two")
	if err := a.openProject(dir2); err != nil {
		t.Fatal(err)
	}
	ps2 := a.current
	openRequestTabs(t, ps2, "Gamma")

	a.current = ps1
	a.closeAll()
	if len(ps1.tabs) != 0 {
		t.Fatalf("closing project one should empty it, got %d", len(ps1.tabs))
	}
	if len(ps2.tabs) != 1 {
		t.Fatalf("closing project one must not touch project two, got %d", len(ps2.tabs))
	}
}

func TestDiscardReloadsStoredRequest(t *testing.T) {
	_, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	original := tab.URL
	tab.URL = "http://changed"
	tab.Dirty = true
	ps.reloadRequestTab(tab)
	if tab.Dirty {
		t.Fatal("reload should clear the dirty flag")
	}
	if tab.URL != original {
		t.Fatalf("reload should restore the stored URL %q, got %q", original, tab.URL)
	}
}

func TestMoveKeepsTabAndStableID(t *testing.T) {
	_, ps := openTestProject(t, "Demo")
	tabs := openRequestTabs(t, ps, "Alpha")
	tab := tabs[0]
	id := tab.Entry.Request.ID
	if _, err := ps.project.NewFolder("", "Group"); err != nil {
		t.Fatal(err)
	}
	if err := ps.moveRequest(tab, "collections/group"); err != nil {
		t.Fatalf("moveRequest: %v", err)
	}
	if ps.indexOfTab(tab) == -1 {
		t.Fatal("moving a request must keep its tab")
	}
	if tab.Entry.Request.ID != id {
		t.Fatalf("request id changed on move: %q -> %q", id, tab.Entry.Request.ID)
	}
	if got := filepath.ToSlash(filepath.Dir(tab.Entry.RelPath)); got != "collections/group" {
		t.Fatalf("request not in target folder: %q", tab.Entry.RelPath)
	}
}
