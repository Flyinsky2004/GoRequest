package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestLastTabClosesThroughMergedWorkspace(t *testing.T) {
	a, ps := openTestProject(t, "Last tab")
	tab := openRequestTabs(t, ps, "Only")[0]
	tt := ui.NewTester(a.View, 1200, 820)
	if err := tt.Click("Close " + tabIdentity(tab)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(ps.tabs) != 0 || !tt.HasText("Choose a request or document on the left.") {
		t.Fatalf("closing the last tab should return to the workspace empty state: %v", tt.Texts())
	}
	if !tt.HasText("+ Folder") || !tt.HasText("New Env") {
		t.Fatal("folder and environment actions must remain available without tabs")
	}
}

func TestMergedProjectWindowsKeepFoldersTabsAndEnvironmentsSeparate(t *testing.T) {
	a, ps := openTestProject(t, "One")
	m := a.mgr
	if err := m.openProject(writeProject(t, "Two")); err != nil {
		t.Fatal(err)
	}
	b := m.workspaces[1]
	if _, err := ps.createFolder("", "API"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.newRequest(); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = "http://unsaved.local"
	tab.Dirty = true
	a.openNewEnvironment()
	ps.envUI.idText = "integration"
	ps.envUI.varRows = []*envVarRow{{ID: nextRowID(), Key: "baseUrl", Value: "http://one.local"}}
	if err := a.saveEnvironmentDraft(ps); err != nil {
		t.Fatal(err)
	}
	if tab.URL != "http://unsaved.local" || !tab.Dirty || tab.Entry.RelPath != "collections/api/new-request.request.yaml" {
		t.Fatal("saving an environment changed the folder request or its unsaved edits")
	}
	if _, exists := b.ps.project.Environments["integration"]; exists || len(b.ps.tabs) != 0 || len(b.ps.project.Collections) != 0 {
		t.Fatal("project one state leaked into project two")
	}
	tt := ui.NewTester(a.View, 1200, 820)
	for _, want := range []string{"New Env", "Edit Env", "+ Folder", "Open Tabs (1)"} {
		if !tt.HasText(want) {
			t.Fatalf("merged workspace missing %q: %v", want, tt.Texts())
		}
	}
	a.closeAll()
	a.cancelClose()
	if ps.activeTab() != tab || !tab.Dirty {
		t.Fatal("cancelling tab close should keep the folder request's unsaved edits")
	}
	if err := m.openProject(ps.root); err != nil || len(m.workspaces) != 2 {
		t.Fatalf("reopening a project should reuse its window: %v", err)
	}
}

func TestWindowCloseProtectsEnvironmentDraft(t *testing.T) {
	for _, action := range []string{"save", "discard", "invalid"} {
		t.Run(action, func(t *testing.T) {
			a, ps := openTestProject(t, "Environment")
			a.openNewEnvironment()
			ps.envUI.idText = "unsaved-env"
			if action == "invalid" {
				ps.envUI.idText = "../invalid"
			}
			a.win.Close()
			if a.closed || a.closeGuard == nil || !a.closeGuard.active {
				t.Fatal("closing a window with an environment draft must request a decision")
			}
			a.cancelCloseGuard()
			if a.closed || !ps.envUI.open || ps.envUI.idText == "" {
				t.Fatal("cancel must keep the window and environment draft")
			}
			a.win.Close()
			a.resolveCloseGuard(action == "discard")
			if action == "invalid" {
				if a.closed || !a.closeGuard.active || a.closeGuard.errMsg == "" || !ps.envUI.open {
					t.Fatal("invalid environment must block closing and keep the draft")
				}
				return
			}
			if !a.closed || len(a.mgr.workspaces) != 0 || a.mgr.welcome == nil {
				t.Fatal("resolved close should return the last project to the chooser")
			}
			_, saved := ps.project.Environments["unsaved-env"]
			if saved != (action == "save") {
				t.Fatalf("environment saved=%v after %s", saved, action)
			}
		})
	}
}
