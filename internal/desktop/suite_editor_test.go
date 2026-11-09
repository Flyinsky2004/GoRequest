package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

func suiteEditorRequest(t *testing.T, ps *projectState, name, url string) *core.RequestEntry {
	t.Helper()
	entry, err := ps.project.NewRequest("", name)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", name, err)
	}
	entry.Request.HTTP.URL = url
	if _, err := ps.project.SaveRequestPreserving(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatalf("SaveRequestPreserving: %v", err)
	}
	return entry
}

func suiteEditorWrite(t *testing.T, ps *projectState, rel, content string) {
	t.Helper()
	if err := ps.project.Store.WriteAtomic(rel, []byte(content)); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestSuiteEditorNewSaveAndRunnerRefresh covers the real action flow: create a
// new suite, add two cross-request steps with a dependency, save, and verify the
// open Runner modal now lists and selects the saved file.
func TestSuiteEditorNewSaveAndRunnerRefresh(t *testing.T) {
	a, ps := openTestProject(t, "Suite Editor")
	one := suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	two := suiteEditorRequest(t, ps, "Two", "http://127.0.0.1/two")
	a.OpenRunner()

	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	if s == nil || !s.open || s.draft == nil || !s.draft.IsNew {
		t.Fatalf("editor did not open a new draft: %+v", s)
	}
	s.draft.ID = "demo"
	s.draft.Name = "Demo"
	a.AddSuiteStep()
	first := s.draft.Steps[0]
	a.SetSuiteStepRequest(first.RowID, one.Request.ID)
	a.AddSuiteStep()
	second := s.draft.Steps[1]
	a.SetSuiteStepRequest(second.RowID, two.Request.ID)
	a.ToggleSuiteStepNeed(second.RowID, first.ID)
	a.SaveSuiteEditor()
	if s.err != "" {
		t.Fatalf("save error: %s", s.err)
	}
	if !ps.project.Store.Exists("suites/demo.suite.yaml") {
		t.Fatal("suite file was not created")
	}
	rs := a.runnerState(false)
	if rs == nil || rs.suite != "suites/demo.suite.yaml" || !containsString(rs.suites, "suites/demo.suite.yaml") {
		t.Fatalf("runner selection not refreshed: %+v", rs)
	}

	tt := ui.NewTester(func(c *ui.Context) { a.SuiteEditorModals(c, c.Theme()) }, 1100, 760)
	tt.Frame()
	for _, want := range []string{a.suiteEditorMsg().Title, "demo", "One", a.suiteEditorMsg().Save} {
		if !tt.HasText(want) {
			t.Fatalf("headless render missing %q", want)
		}
	}
}

// TestSuiteEditorDirtyDiscardConfirm verifies a dirty draft is never discarded
// silently and discarding writes nothing.
func TestSuiteEditorDirtyDiscardConfirm(t *testing.T) {
	a, ps := openTestProject(t, "Discard")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	s.draft.ID = "draft"
	s.draft.Name = "Draft"
	a.CloseSuiteEditor()
	if !s.open || !s.confirmDiscard {
		t.Fatalf("dirty close must ask first: open=%v confirm=%v", s.open, s.confirmDiscard)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.SuiteEditorModals(c, c.Theme()) }, 1000, 700)
	tt.Frame()
	if !tt.HasText(a.suiteEditorMsg().DiscardTitle) {
		t.Fatal("discard confirmation is not visible")
	}
	a.KeepSuiteEditorDraft()
	if s.confirmDiscard || !s.open {
		t.Fatal("keep editing must retain the draft")
	}
	a.CloseSuiteEditor()
	a.DiscardSuiteEditorDraft()
	if s.open {
		t.Fatal("discard must close the editor")
	}
	if ps.project.Store.Exists("suites/draft.suite.yaml") {
		t.Fatal("discard wrote a file")
	}
}

// TestSuiteEditorConflictKeepsDraft verifies an external edit is not overwritten
// and the draft survives the failed save.
func TestSuiteEditorConflictKeepsDraft(t *testing.T) {
	a, ps := openTestProject(t, "Conflict")
	one := suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	original := "schemaVersion: 1\nid: demo\nname: Demo\nsteps:\n  - id: step1\n    request: " + one.RelPath + "\n"
	suiteEditorWrite(t, ps, "suites/demo.suite.yaml", original)

	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	if s.draft == nil || s.draft.RelPath != "suites/demo.suite.yaml" {
		t.Fatalf("editor did not load the saved suite: %+v", s.draft)
	}
	s.draft.Name = "Mine"
	external := original + "# external edit\n"
	suiteEditorWrite(t, ps, "suites/demo.suite.yaml", external)
	a.SaveSuiteEditor()
	if s.err == "" {
		t.Fatal("expected a conflict error")
	}
	if s.draft == nil || s.draft.Name != "Mine" {
		t.Fatal("draft was not retained after conflict")
	}
	data, _, err := ps.project.Store.ReadBounded("suites/demo.suite.yaml", 1<<20)
	if err != nil || string(data) != external {
		t.Fatalf("external content was overwritten: %q %v", data, err)
	}
}

// TestSuiteEditorNoOpSaveDoesNotWrite verifies an unchanged draft is not
// rewritten on disk.
func TestSuiteEditorNoOpSaveDoesNotWrite(t *testing.T) {
	a, ps := openTestProject(t, "Noop")
	one := suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	suiteEditorWrite(t, ps, "suites/demo.suite.yaml", "schemaVersion: 1\nid: demo\nname: Demo\nsteps:\n  - id: step1\n    request: "+one.RelPath+"\n")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	before := s.draft.Baseline
	a.SaveSuiteEditor()
	if s.notice != a.suiteEditorMsg().NothingToSave {
		t.Fatalf("want nothing-to-save notice, got %q", s.notice)
	}
	_, after, err := ps.project.Store.ReadBounded("suites/demo.suite.yaml", 1<<20)
	if err != nil || after != before {
		t.Fatalf("no-op save rewrote the file: %q %v", after, err)
	}
}

// TestSuiteEditorTwoWindowsIsolated verifies two App windows never share drafts.
func TestSuiteEditorTwoWindowsIsolated(t *testing.T) {
	a1, _ := openTestProject(t, "Window One")
	a2, _ := openTestProject(t, "Window Two")
	a1.OpenSuiteEditor()
	a2.OpenSuiteEditor()
	s1 := a1.suiteEditorState(false)
	s2 := a2.suiteEditorState(false)
	if s1 == nil || s2 == nil || s1 == s2 {
		t.Fatal("editor states are not per-window")
	}
	s1.draft.Name = "First"
	if s2.draft.Name == "First" {
		t.Fatal("drafts leaked between windows")
	}
}

// TestSuiteEditorHeadlessInteraction covers click, keyboard dismissal and the
// compact-height scroll path.
func TestSuiteEditorHeadlessInteraction(t *testing.T) {
	a, ps := openTestProject(t, "Interactive")
	suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	suiteEditorWrite(t, ps, "data.csv", "user\nu1\n")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	s.draft.Dataset.Enabled = true
	s.draft.Dataset.Format = "csv"
	s.draft.Dataset.File = "data.csv"
	m := a.suiteEditorMsg()

	tt := ui.NewTester(func(c *ui.Context) { a.SuiteEditorModals(c, c.Theme()) }, 900, 600)
	tt.Frame()
	if err := tt.Click(m.AddStep); err != nil {
		t.Fatalf("click add step: %v", err)
	}
	tt.Frame()
	if len(s.draft.Steps) != 1 {
		t.Fatalf("add step click did not add a step: %d", len(s.draft.Steps))
	}
	for _, label := range []string{m.Save, m.Cancel} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 600 {
			t.Fatalf("%q is not reachable at 900x600: %+v", label, r)
		}
	}
	// The dataset section is inside the scrolling body; scroll it into view.
	tt.Scroll(650, 350, 0, 600)
	tt.Frame()
	if r, ok := tt.Find(m.PreviewButton); !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 600 {
		t.Fatalf("%q is not reachable after scrolling: %+v", m.PreviewButton, r)
	}
	// Escape on a dirty draft must reopen in confirmation state, not discard.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !s.open || !s.confirmDiscard {
		t.Fatalf("escape on a dirty draft must ask first: open=%v confirm=%v", s.open, s.confirmDiscard)
	}
}

// TestSuiteEditorDeleteGuardAndReorder exercises the dependency guard and the
// stable-row reorder.
func TestSuiteEditorDeleteGuardAndReorder(t *testing.T) {
	a, ps := openTestProject(t, "Guard")
	one := suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	two := suiteEditorRequest(t, ps, "Two", "http://127.0.0.1/two")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	s.draft.ID = "demo"
	a.AddSuiteStep()
	first := s.draft.Steps[0]
	a.SetSuiteStepRequest(first.RowID, one.Request.ID)
	a.AddSuiteStep()
	second := s.draft.Steps[1]
	a.SetSuiteStepRequest(second.RowID, two.Request.ID)
	a.ToggleSuiteStepNeed(second.RowID, first.ID)

	a.DeleteSuiteStep(first.RowID)
	if s.err == "" || !strings.Contains(s.err, "depends") {
		t.Fatalf("delete guard did not block: %q", s.err)
	}
	if len(s.draft.Steps) != 2 {
		t.Fatal("blocked delete changed the draft")
	}
	a.MoveSuiteStep(second.RowID, -1)
	if s.draft.Steps[0].RowID != second.RowID {
		t.Fatal("reorder did not move the stable row")
	}
	a.DeleteSuiteStep(second.RowID)
	if s.err != "" || len(s.draft.Steps) != 1 {
		t.Fatalf("independent step delete failed: %q steps=%d", s.err, len(s.draft.Steps))
	}
}

// TestSuiteEditorNavigateToRunnerEditorAndResume verifies the two editors can
// navigate to each other without losing the suite draft or a dirty runner-editor
// draft.
func TestSuiteEditorNavigateToRunnerEditorAndResume(t *testing.T) {
	a, ps := openTestProject(t, "Navigate")
	one := suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	s.draft.ID = "demo"
	s.draft.Name = "Draft"
	a.AddSuiteStep()
	st := s.draft.Steps[0]
	a.SetSuiteStepRequest(st.RowID, one.Request.ID)

	a.OpenRunnerEditorForSuiteStep(st)
	if s.open {
		t.Fatal("suite editor should hide while the runner editor opens")
	}
	if s.draft == nil || s.draft.Name != "Draft" {
		t.Fatal("suite draft was lost on navigation")
	}
	re := a.runnerEditorState(false)
	if re == nil || !re.open || re.requestID != one.Request.ID {
		t.Fatalf("runner editor did not focus the step request: %+v", re)
	}
	a.CloseRunnerEditor()
	a.OpenSuiteEditor()
	if !s.open || s.draft == nil || s.draft.Name != "Draft" {
		t.Fatal("suite draft did not resume after returning")
	}

	// A dirty runner-editor draft blocks navigation instead of being discarded.
	a.OpenRunnerEditor()
	a.BeginRunnerEditorRow(runner.TargetStatus)
	a.OpenRunnerEditorForSuiteStep(st)
	if s.err == "" {
		t.Fatal("dirty runner editor must block navigation")
	}
}

// TestSuiteEditorDatasetPreview covers the preview action and masked columns.
func TestSuiteEditorDatasetPreview(t *testing.T) {
	a, ps := openTestProject(t, "Preview")
	suiteEditorRequest(t, ps, "One", "http://127.0.0.1/one")
	suiteEditorWrite(t, ps, "data.csv", "user,token\nu1,t1\nu2,t2\n")
	a.OpenSuiteEditor()
	s := a.suiteEditorState(false)
	s.draft.Dataset.Enabled = true
	s.draft.Dataset.Format = "csv"
	s.draft.Dataset.File = "data.csv"
	a.PreviewSuiteDataset()
	if s.previewErr != "" || s.preview == nil {
		t.Fatalf("preview failed: %q", s.previewErr)
	}
	if len(s.preview.Rows) != 2 || s.preview.Rows[0]["token"] != "***" {
		t.Fatalf("preview rows/masking wrong: %+v", s.preview)
	}
	if s.preview.EffectiveMaxRows != 1000 || s.preview.EffectiveMaxBytes != 8<<20 {
		t.Fatalf("preview limits wrong: %+v", s.preview)
	}
}
