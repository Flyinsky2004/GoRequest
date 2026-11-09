package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSDKRejectsChangedPreviewInputs(t *testing.T) {
	cases := map[string]func(*SDKState){
		"target":    func(s *SDKState) { s.TargetDir += "-different" },
		"package":   func(s *SDKState) { s.PackageName = "different" },
		"module":    func(s *SDKState) { s.ModulePath = "example.com/different" },
		"version":   func(s *SDKState) { s.SpecVersionIdx = 1 },
		"subset":    func(s *SDKState) { s.IncludeUnsupported = !s.IncludeUnsupported },
		"overwrite": func(s *SDKState) { s.AllowOverwrite = !s.AllowOverwrite },
		"delete":    func(s *SDKState) { s.AllowDeleteStale = !s.AllowDeleteStale },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			a, ps := openTestProject(t, "SDK input guard")
			sdkTestRequest(t, ps)
			a.OpenSDKModal()
			s := a.SDK()
			target := filepath.Join(t.TempDir(), "sdk")
			s.TargetDir = target
			a.RefreshSDKPreview()
			waitSDKPreview(t, s)
			if s.Plan == nil {
				t.Fatal(s.Err)
			}
			change(s)
			a.ApplySDK()
			if s.Err == "" || s.Plan != nil {
				t.Fatal("changed inputs retained an applicable preview")
			}
			if _, err := os.Stat(target); !os.IsNotExist(err) {
				t.Fatalf("stale target was written: %v", err)
			}
		})
	}
}

func TestSDKRejectsChangedContract(t *testing.T) {
	a, ps := openTestProject(t, "SDK snapshot guard")
	tab := sdkTestRequest(t, ps)
	a.OpenSDKModal()
	s := a.SDK()
	s.TargetDir = filepath.Join(t.TempDir(), "sdk")
	a.RefreshSDKPreview()
	waitSDKPreview(t, s)
	if s.Plan == nil {
		t.Fatal(s.Err)
	}
	tab.Entry.Request.HTTP.Method = "DELETE"
	a.ApplySDK()
	if s.Err == "" || s.Plan != nil {
		t.Fatal("changed contract retained an applicable preview")
	}
	if _, err := os.Stat(s.TargetDir); !os.IsNotExist(err) {
		t.Fatalf("stale SDK was written: %v", err)
	}
}

func TestSuiteDraftParticipatesInWindowAndUpdateGuards(t *testing.T) {
	r := newHandoffRig(t)
	if err := r.m.openProject(writeProject(t, "Suite guard")); err != nil {
		t.Fatal(err)
	}
	a := r.m.workspaces[0]
	entry := suiteEditorRequest(t, a.ps, "Request", "http://127.0.0.1/one")
	a.OpenRunner()
	a.OpenSuiteEditor()
	a.AddSuiteStep()
	s := a.suiteEditorState(false)
	a.SetSuiteStepRequest(s.draft.Steps[0].RowID, entry.Request.ID)
	if !r.m.anyDirty() {
		t.Fatal("Suite draft is missing from quit protection")
	}
	// The production view must expose the child editor without a parent modal
	// intercepting its controls.
	tt := ui.NewTester(a.View, 1200, 900)
	tt.Frame()
	if err := tt.Click(a.suiteEditorMsg().AddStep); err != nil {
		t.Fatal(err)
	}
	if len(s.draft.Steps) != 2 {
		t.Fatal("Suite child controls were intercepted")
	}
	a.DeleteSuiteStep(s.draft.Steps[1].RowID)
	a.win.Close()
	if a.closed || a.closeGuard == nil || !a.closeGuard.active {
		t.Fatal("window closed over a Suite draft")
	}
	a.cancelCloseGuard()
	r.openErr = errors.New("fake installer failed")
	r.run()
	if r.order() != "" {
		t.Fatal("installer bypassed Suite draft")
	}
	a.resolveCloseGuard(true)
	if r.order() != "open" || !s.dirty() {
		t.Fatal("failed handoff discarded the Suite draft")
	}
	if err := a.saveAllDirty(); err != nil {
		t.Fatal(err)
	}
	if s.dirty() || r.m.anyDirty() {
		t.Fatal("saved Suite still dirty")
	}
	s.draft.Name = "unsaved"
	if err := a.ps.project.Store.WriteAtomic(s.draft.RelPath, []byte("# external edit\n")); err != nil {
		t.Fatal(err)
	}
	if err := a.saveAllDirty(); err == nil || !s.dirty() {
		t.Fatal("Suite conflict failed to retain draft")
	}
	a.clearDirty()
	if r.m.anyDirty() {
		t.Fatal("discard did not clear Suite draft")
	}
}

func TestInvalidSuiteLimitRemainsDirty(t *testing.T) {
	a, _ := openTestProject(t, "Suite limit")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	s.draft.Dataset.Enabled = true
	s.originalSig = s.draft.Signature()
	s.maxRowsText = "invalid"
	if !s.dirty() {
		t.Fatal("invalid numeric input was silently considered clean")
	}
	h := &installerHandoff{}
	h.approveWindow(a)
	s.maxRowsText = "different invalid input"
	if h.isApproved(a) {
		t.Fatal("changed invalid draft retained discard approval")
	}
}

func TestInvalidGraphQLDraftInvalidatesDiscardApproval(t *testing.T) {
	a, ps := openTestProject(t, "GraphQL update guard")
	entry, err := ps.project.NewGraphQLRequest("", "GraphQL")
	if err != nil {
		t.Fatal(err)
	}
	ps.openRequest(entry)
	tab := ps.activeTab()
	tab.Dirty = true
	tab.ensureGQLState().variables = "{"
	h := &installerHandoff{}
	h.approveWindow(a)
	if !h.isApproved(a) {
		t.Fatal("unchanged draft lost approval")
	}
	tab.ensureGQLState().variables = "{ "
	if h.isApproved(a) {
		t.Fatal("changed invalid GraphQL variables retained discard approval")
	}
}
