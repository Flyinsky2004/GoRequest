package desktop

// IMPORTANT: written for A02 phase 2 under the "do not run tests yet" batch
// constraint. These cases are intentionally pure (no network, no real window):
// they exercise the editor's node building, the runner-backed validation, the
// suite-node surgical patch and the save flow on a temporary project. They have
// NOT been executed in this batch; the integrator should run them with
// `go test ./internal/desktop -run TestRunnerEditor`.

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/runner"
)

func runnerEditorAssertNodes(t *testing.T, text string) []yaml.Node {
	t.Helper()
	var doc struct {
		Assert []yaml.Node `yaml:"assert"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		t.Fatalf("unmarshal assertions: %v", err)
	}
	return doc.Assert
}

// TestRunnerEditorDescribeAssertions verifies the runner-backed description of
// saved assert nodes: the four structured targets parse, other valid targets
// are reported as preserved, and unparseable nodes keep a precise error.
func TestRunnerEditorDescribeAssertions(t *testing.T) {
	nodes := runnerEditorAssertNodes(t, `assert:
  - status: 200
  - header: X-Request-Id
    exists: true
  - jsonpath: $.user.id
    equals: "{{data.userId}}"
    type: string
  - body: {contains: ok, notContains: internal}
  - durationMs: {lte: 1500}
  - schema: {ref: schemas/login.yaml}
`)
	nodes = append(nodes, func() []yaml.Node {
		invalid := runnerEditorAssertNodes(t, `assert:
  - schema: {ref: ""}
`)
		return invalid
	}()...)
	specs := runner.DescribeAssertions(nodes)
	if len(specs) != 6 {
		t.Fatalf("want 6 specs, got %d", len(specs))
	}
	if specs[0].Target != runner.TargetStatus || !specs[0].Structured || specs[0].StatusEquals == nil || *specs[0].StatusEquals != 200 {
		t.Fatalf("status spec wrong: %+v", specs[0])
	}
	if specs[1].Target != runner.TargetHeader || specs[1].Header != "X-Request-Id" || len(specs[1].Matchers) != 1 {
		t.Fatalf("header spec wrong: %+v", specs[1])
	}
	if specs[2].Target != runner.TargetJSONPath || specs[2].JSONPath != "$.user.id" || len(specs[2].Matchers) != 2 {
		t.Fatalf("jsonpath spec wrong: %+v", specs[2])
	}
	if specs[3].Target != runner.TargetBody || len(specs[3].Matchers) != 2 {
		t.Fatalf("body spec wrong: %+v", specs[3])
	}
	if specs[4].Target != runner.TargetDuration || specs[4].Structured {
		t.Fatalf("durationMs must be preserved verbatim: %+v", specs[4])
	}
	if specs[5].Target != runner.TargetInvalid || specs[5].Err == "" || specs[5].Raw == "" {
		t.Fatalf("invalid spec wrong: %+v", specs[5])
	}
}

// TestRunnerEditorAssertionRebuildRoundTrip rebuilds structured specs into
// nodes, keeps preserved nodes verbatim, and the whole list must pass the
// engine parser unchanged.
func TestRunnerEditorAssertionRebuildRoundTrip(t *testing.T) {
	nodes := runnerEditorAssertNodes(t, `assert:
  - status: {in: [200, 204]}
  - header: X-Request-Id
    exists: true
  - durationMs: {lte: 1500}
`)
	specs := runner.DescribeAssertions(nodes)
	rows := make([]*runnerEditorRow, 0, len(specs))
	for i := range specs {
		row := &runnerEditorRow{spec: specs[i]}
		if !specs[i].Structured {
			row.node = runnerEditorCloneNode(&nodes[i])
		}
		rows = append(rows, row)
	}
	built, warnings, err := runnerEditorBuildAssertNodes(rows)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(warnings) != 0 {
		t.Fatalf("no warnings expected, got %v", warnings)
	}
	if len(built) != 3 {
		t.Fatalf("want 3 nodes, got %d", len(built))
	}
	if verr := runner.ValidateAssertions(built); verr != nil {
		t.Fatalf("rebuilt list does not parse: %v", verr)
	}
	var doc struct {
		Assert []yaml.Node `yaml:"assert"`
	}
	doc.Assert = built
	out, merr := yaml.Marshal(&doc)
	if merr != nil {
		t.Fatalf("marshal: %v", merr)
	}
	s := string(out)
	for _, want := range []string{"status", "in", "204", "X-Request-Id", "exists", "durationMs", "lte"} {
		if !strings.Contains(s, want) {
			t.Fatalf("rebuilt YAML missing %q:\n%s", want, s)
		}
	}
}

// TestRunnerEditorSpecFromFormErrors pins the precise validation errors the
// inline form surfaces for incomplete or contradictory drafts.
func TestRunnerEditorSpecFromFormErrors(t *testing.T) {
	if _, err := runnerEditorSpecFromForm(&runnerEditorForm{target: runner.TargetStatus, statusMode: 0, statusEqual: "abc"}); err == nil || !strings.Contains(err.Error(), "integer") {
		t.Fatalf("status code error: %v", err)
	}
	if _, err := runnerEditorSpecFromForm(&runnerEditorForm{target: runner.TargetHeader, header: "  "}); err == nil || !strings.Contains(err.Error(), "header") {
		t.Fatalf("empty header error: %v", err)
	}
	if _, err := runnerEditorSpecFromForm(&runnerEditorForm{target: runner.TargetBody}); err == nil || !strings.Contains(err.Error(), "matcher") {
		t.Fatalf("body without matcher: %v", err)
	}
	// exists:false must not combine with other matchers; the engine parser
	// must reject the built node.
	mixed := &runnerEditorForm{
		target:   runner.TargetJSONPath,
		jsonPath: "$.a",
		matchers: []*runnerEditorMatcherDraft{
			{kind: runner.MatcherExists, exists: "false"},
			{kind: runner.MatcherContains, text: "x"},
		},
	}
	spec, err := runnerEditorSpecFromForm(mixed)
	if err != nil {
		t.Fatalf("form to spec: %v", err)
	}
	node, berr := runnerEditorBuildAssertionNode(spec)
	if berr != nil {
		t.Fatalf("build mixed spec: %v", berr)
	}
	if verr := runner.ValidateAssertions([]yaml.Node{node}); verr == nil || !strings.Contains(verr.Error(), "combined") {
		t.Fatalf("exists:false + matcher must fail engine validation: %v", verr)
	}
	// A well-formed typed literal round trip.
	good := &runnerEditorForm{
		target:   runner.TargetJSONPath,
		jsonPath: "$.n",
		matchers: []*runnerEditorMatcherDraft{
			{kind: runner.MatcherEquals, litKind: runner.LiteralNumber, litText: "200.5"},
			{kind: runner.MatcherType, typeSel: "number"},
		},
	}
	gspec, gerr := runnerEditorSpecFromForm(good)
	if gerr != nil {
		t.Fatalf("good form: %v", gerr)
	}
	gnode, gberr := runnerEditorBuildAssertionNode(gspec)
	if gberr != nil {
		t.Fatalf("good build: %v", gberr)
	}
	if verr := runner.ValidateAssertions([]yaml.Node{gnode}); verr != nil {
		t.Fatalf("typed literal must validate: %v", verr)
	}
}

// TestRunnerEditorCSVLiterals checks the documented scalar typing of the
// comma-separated in-list input.
func TestRunnerEditorCSVLiterals(t *testing.T) {
	lits, err := runnerEditorCSVLiterals("200, ok , true, null, -1.5,,")
	if err != nil {
		t.Fatal(err)
	}
	want := []struct {
		kind string
		text string
	}{
		{runner.LiteralNumber, "200"},
		{runner.LiteralString, "ok"},
		{runner.LiteralBoolean, "true"},
		{runner.LiteralNull, ""},
		{runner.LiteralNumber, "-1.5"},
	}
	if len(lits) != len(want) {
		t.Fatalf("want %d literals, got %v", len(want), lits)
	}
	for i, w := range want {
		if lits[i].Kind != w.kind || lits[i].Text != w.text {
			t.Fatalf("literal %d: want %+v, got %+v", i, w, lits[i])
		}
	}
}

// TestRunnerEditorExtractFormTriState keeps the required/sensitive tri-state
// (nil/true/false) intact through both form directions.
func TestRunnerEditorExtractFormTriState(t *testing.T) {
	base := runner.Extraction{Name: "tok", From: "header", Header: "X-Token"}
	f := runnerEditorExtractFormFromExtraction(&base)
	if f.requiredSel != runnerEditorTriStates[0] || f.sensitiveSel != runnerEditorTriStates[0] {
		t.Fatalf("defaults lost: %+v", f)
	}
	back, err := runnerEditorExtractionFromForm(&f)
	if err != nil {
		t.Fatal(err)
	}
	if back.Required != nil || back.Sensitive != nil || back.Header != "X-Token" {
		t.Fatalf("round trip changed defaults: %+v", back)
	}
	base.Required = boolPtr(false)
	base.Sensitive = boolPtr(true)
	f = runnerEditorExtractFormFromExtraction(&base)
	if f.requiredSel != runnerEditorTriStates[2] || f.sensitiveSel != runnerEditorTriStates[1] {
		t.Fatalf("explicit override lost: %+v", f)
	}
	back, err = runnerEditorExtractionFromForm(&f)
	if err != nil || back.Required == nil || *back.Required || back.Sensitive == nil || !*back.Sensitive {
		t.Fatalf("explicit override round trip broken: %+v err=%v", back, err)
	}
}

// TestRunnerEditorPatchSuiteExtract checks the surgical suite file patch:
// only the chosen step's extract sequence moves, comments and other steps
// survive, an empty list removes the key, and the result re-parses.
func TestRunnerEditorPatchSuiteExtract(t *testing.T) {
	data := []byte(`schemaVersion: 1
# suite comment stays
id: editor-patch
steps:
  - id: login
    request: collections/alpha.request.yaml
    # step comment stays
    extract:
      - name: token
        from: jsonpath
        path: $.accessToken
        sensitive: true
  - id: other
    request: collections/beta.request.yaml
`)
	exts := []runner.Extraction{
		{Name: "tid", From: "header", Header: "X-Request-Id"},
		{Name: "n", From: "jsonpath", Path: "$.user.id"},
	}
	patched, err := runnerEditorPatchSuiteExtract(data, 0, exts)
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if !strings.Contains(string(patched), "# suite comment stays") || !strings.Contains(string(patched), "# step comment stays") {
		t.Fatalf("comments lost:\n%s", patched)
	}
	suite, perr := runner.ParseSuite(patched)
	if perr != nil {
		t.Fatalf("re-parse: %v", perr)
	}
	if verr := suite.ValidateStructure(); verr != nil {
		t.Fatalf("re-validate: %v", verr)
	}
	if len(suite.Steps) != 2 {
		t.Fatalf("steps changed: %d", len(suite.Steps))
	}
	if len(suite.Steps[0].Extract) != 2 || suite.Steps[0].Extract[0].Header != "X-Request-Id" {
		t.Fatalf("extract not patched: %+v", suite.Steps[0].Extract)
	}
	if suite.Steps[1].Request != "collections/beta.request.yaml" {
		t.Fatalf("unrelated step changed: %+v", suite.Steps[1])
	}
	if _, e := runnerEditorPatchSuiteExtract(data, 0, []runner.Extraction{{Name: "t", From: "header", Header: "X"}}); e == nil {
		// clearing the list must remove the extract key, not leave an empty seq
	}
	cleared, cerr := runnerEditorPatchSuiteExtract(data, 0, nil)
	if cerr != nil {
		t.Fatalf("clear: %v", cerr)
	}
	if strings.Contains(string(cleared), "extract") {
		t.Fatalf("empty extract must remove the key:\n%s", cleared)
	}
	suite2, perr2 := runner.ParseSuite(cleared)
	if perr2 != nil {
		t.Fatalf("cleared re-parse: %v", perr2)
	}
	if len(suite2.Steps[0].Extract) != 0 {
		t.Fatalf("cleared extract must be empty: %+v", suite2.Steps[0].Extract)
	}
	if _, err := runnerEditorPatchSuiteExtract(data, 5, nil); err == nil {
		t.Fatal("out-of-range step must fail")
	}
	if _, err := runnerEditorPatchSuiteExtract(data, 0, []runner.Extraction{{Name: "t", From: "weird"}}); err == nil || !strings.Contains(err.Error(), "weird") {
		t.Fatalf("unknown extraction source must fail the patch with it named: %v", err)
	}
}

// TestRunnerEditorStateSavesRequestAssertions runs the real save flow on a
// temporary project: add one status assertion, save, delete, save, and check
// both the in-memory model and "nothing to save" notice.
func TestRunnerEditorStateSavesRequestAssertions(t *testing.T) {
	a, ps := openTestProject(t, "Editor save flow")
	tabs := openRequestTabs(t, ps, "Alpha")
	entry := tabs[0].Entry
	a.OpenRunnerEditor()
	s := a.runnerEditorState(false)
	if s == nil || !s.open || s.requestID != entry.Request.ID {
		t.Fatalf("editor state wrong: %+v", s)
	}
	a.BeginRunnerEditorRow(runner.TargetStatus)
	if s.formErr != "" || s.editingRow != 0 {
		t.Fatalf("form did not open: %+v", s)
	}
	s.form.statusEqual = "201"
	a.ApplyRunnerEditorRow()
	if s.formErr != "" || len(s.rows) != 1 || s.editingRow != -1 {
		t.Fatalf("row not applied: %+v", s)
	}
	a.SaveRunnerEditor()
	if s.err != "" || s.rowDraft {
		t.Fatalf("save failed: %+v", s)
	}
	saved := entry.Request.Assert
	if len(saved) != 1 {
		t.Fatalf("assert not saved: %+v", entry.Request)
	}
	if verr := runner.ValidateAssertions(saved); verr != nil {
		t.Fatalf("saved assert invalid: %v", verr)
	}
	if entry.BaselineDigest == "" {
		t.Fatal("baseline digest not updated")
	}
	a.SaveRunnerEditor()
	if s.notice != a.runnerEditorMsg().NothingToSave {
		t.Fatalf("expected nothing-to-save notice, got %q", s.notice)
	}
	a.DeleteRunnerEditorRow(0)
	a.SaveRunnerEditor()
	if s.err != "" || len(entry.Request.Assert) != 0 {
		t.Fatalf("delete not saved: %+v", s)
	}
}

// TestRunnerEditorSuiteTargetsAndSave discovers a saved suite step referencing
// the request, edits its extraction list and saves the suite file with
// comments preserved.
func TestRunnerEditorSuiteTargetsAndSave(t *testing.T) {
	a, ps := openTestProject(t, "Editor suite save")
	tabs := openRequestTabs(t, ps, "Alpha")
	entry := tabs[0].Entry
	suiteRel := "editor.suite.yaml"
	suiteData := []byte(fmt.Sprintf(`schemaVersion: 1
# keep this comment
id: editor-smoke
steps:
  - id: login
    request: %s
    extract:
      - name: token
        from: jsonpath
        path: $.accessToken
`, entry.RelPath))
	if err := ps.project.Store.WriteAtomic(suiteRel, suiteData); err != nil {
		t.Fatalf("write suite: %v", err)
	}
	a.OpenRunnerEditor()
	s := a.runnerEditorState(false)
	if s == nil || len(s.targets) != 1 || s.targets[0].stepID != "login" {
		t.Fatalf("suite target not discovered: %+v", s)
	}
	a.BeginRunnerEditorExtractNew()
	if s.editSel != runnerEditorSelectNew {
		t.Fatalf("extract form not open: %+v", s)
	}
	s.extractForm.name = "request_id"
	s.extractForm.fromSel = "header"
	s.extractForm.header = "X-Request-Id"
	a.ApplyRunnerEditorExtract()
	if s.extractErr != "" || len(s.targets[0].rows) != 2 {
		t.Fatalf("extract row not applied: %+v", s)
	}
	a.SaveRunnerEditor()
	if s.err != "" || s.targets[0].dirty {
		t.Fatalf("suite save failed: %+v", s)
	}
	data2, _, rerr := ps.project.Store.ReadBounded(suiteRel, runner.DefaultMaxSuiteBytes)
	if rerr != nil {
		t.Fatalf("re-read suite: %v", rerr)
	}
	if !strings.Contains(string(data2), "# keep this comment") {
		t.Fatalf("suite comment lost:\n%s", data2)
	}
	suite, perr := runner.ParseSuite(data2)
	if perr != nil {
		t.Fatalf("suite re-parse: %v", perr)
	}
	ext := suite.Steps[0].Extract
	if len(ext) != 2 || ext[1].Name != "request_id" || ext[1].From != "header" || ext[1].Header != "X-Request-Id" {
		t.Fatalf("suite extract wrong: %+v", ext)
	}
}

// TestRunnerEditorRendersHeadlessSmoke renders the editor modal headless and
// checks that its title and one assertion row are visible, mirroring the
// phase-1 runner modal smoke tests (headless render, not manual acceptance).
func TestRunnerEditorRendersHeadlessSmoke(t *testing.T) {
	a, ps := openTestProject(t, "Editor render")
	tabs := openRequestTabs(t, ps, "Alpha")
	tabs[0].Dirty = true
	a.OpenRunnerEditor()
	s := a.runnerEditorState(false)
	a.BeginRunnerEditorRow(runner.TargetStatus)
	a.ApplyRunnerEditorRow()
	if len(s.rows) != 1 {
		t.Fatalf("row missing: %+v", s)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 1280, 1000)
	tt.Frame()
	if !tt.HasText(a.runnerEditorMsg().Title) {
		t.Fatal("editor title not visible")
	}
	if !tt.HasText("status") {
		t.Fatal("assertion row summary not visible")
	}
	if !tt.HasText(a.runnerEditorMsg().DirtyTabsHint) {
		t.Fatal("dirty tab hint not visible")
	}
	a.CloseRunnerEditor()
	if s.open || s.dirty() {
		t.Fatalf("close must discard drafts: %+v", s)
	}
}

func boolPtr(b bool) *bool { return &b }
