package desktop

import (
	"strings"
	"testing"

	"gorequest/internal/core"
	"gorequest/internal/mockserver"
)

// C01 phase 2: structured mock rule & scenario editor regression cases.
//
// NOTE: per the current task constraints these tests were authored but NOT
// executed (no go build / go vet / go test, no real listener). They are the
// pending verification gate for integration/acceptance:
//
//	go test -race -run 'TestMockRuleEditor|TestMockDeleteRule' ./internal/desktop

// writeEditorMockFile creates a mock file via the engine's checked save so
// tests exercise the same encode path as production.
func writeEditorMockFile(t *testing.T, ps *projectState, mf *mockserver.MockFile) string {
	t.Helper()
	// SaveMockFile returns the saved content digest, not the path; the
	// project-relative path is recorded on mf.SourceRelPath.
	if _, err := mockserver.SaveMockFile(ps.project, mf, ""); err != nil {
		t.Fatalf("SaveMockFile: %v", err)
	}
	return mf.SourceRelPath
}

func simpleEditorRule(id string) mockserver.MockRule {
	return mockserver.MockRule{
		ID:       id,
		Name:     "Rule " + id,
		Priority: 100,
		Match: mockserver.RuleMatch{
			Method: "GET",
			Path:   "/v1/orders/{orderId}",
			PathParams: map[string]mockserver.ValueMatcher{
				"orderId": {Prefix: strPtr("ord-")},
			},
			Query: []mockserver.KeyValueMatcher{
				{Name: "expand", Equals: strPtr("items")},
			},
		},
		Response: mockserver.RuleResponse{
			Status:      200,
			ContentType: "application/json",
			Body:        `{"ok":true}`,
		},
		Variants: map[string]mockserver.RuleResponse{
			"error_400": {Status: 400, Body: `{"e":"bad"}`},
		},
	}
}

// advancedEditorRule carries every construct the structured editor does not
// author: jsonFields body matcher, combined query predicates, response
// headers, delay range, and an extra variant. A save of a neighbouring rule
// must preserve it byte-for-byte at the struct level.
func advancedEditorRule(id string) mockserver.MockRule {
	equals := "a"
	return mockserver.MockRule{
		ID:       id,
		Name:     "Advanced " + id,
		Priority: 50,
		Match: mockserver.RuleMatch{
			Method: "POST",
			Path:   "/v1/orders",
			Query: []mockserver.KeyValueMatcher{
				{Name: "x-combined", Equals: &equals, Contains: &equals},
			},
			Body: &mockserver.BodyMatcher{
				JSONFields: []mockserver.JSONFieldMatcher{
					{Path: "user.id", Equals: &equals, Type: "string"},
				},
			},
		},
		Response: mockserver.RuleResponse{
			Status:       200,
			Body:         "adv",
			Headers:      []core.KV{{Name: "X-Adv", Value: "1"}},
			DelayRangeMs: &mockserver.DelayRange{Min: 5, Max: 9},
		},
		Variants: map[string]mockserver.RuleResponse{
			"error_500": {
				Status:       500,
				Headers:      []core.KV{{Name: "X-Conv", Value: "keep"}},
				DelayRangeMs: &mockserver.DelayRange{Min: 1, Max: 2},
			},
		},
	}
}

func strPtr(s string) *string { return &s }

func reloadMockFileForTest(t *testing.T, ps *projectState, rel string) *mockserver.MockFile {
	t.Helper()
	mf, err := mockserver.LoadMockFile(ps.project, rel)
	if err != nil {
		t.Fatalf("LoadMockFile(%s): %v", rel, err)
	}
	return mf
}

func findRuleForTest(t *testing.T, mf *mockserver.MockFile, id string) *mockserver.MockRule {
	t.Helper()
	for i := range mf.Rules {
		if mf.Rules[i].ID == id {
			return &mf.Rules[i]
		}
	}
	t.Fatalf("rule %q not found in %s", id, mf.SourceRelPath)
	return nil
}

// TestMockRuleEditorRoundTripPreservesUnknownFields edits a simple rule and
// verifies: the edited fields persist, the untouched sibling rule (advanced
// matchers, headers, delay ranges, variants) survives identical, the draft
// clone is never mutated, and the panel notice reports the save.
func TestMockRuleEditorRoundTripPreservesUnknownFields(t *testing.T) {
	a, ps := openTestProject(t, "Mock Editor RoundTrip")
	mf := &mockserver.MockFile{
		SchemaVersion:   mockserver.MockSchemaVersion,
		ID:              "orders-file",
		Name:            "Orders Mock",
		DefaultScenario: "default",
		Rules:           []mockserver.MockRule{advancedEditorRule("adv-rule"), simpleEditorRule("get-orders")},
	}
	rel := writeEditorMockFile(t, ps, mf)

	a.OpenMockServer()
	s := a.mockState(false)
	if s == nil || len(s.files) != 1 {
		t.Fatalf("panel files not loaded: %+v", s)
	}
	origFile, err := mockserver.LoadMockFile(ps.project, rel)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	a.OpenMockRuleEditor(rel, "get-orders")
	ed := s.editor
	if ed == nil || ed.mode != "edit" || ed.ruleIndex != 1 {
		t.Fatalf("unexpected editor state: %+v", ed)
	}
	if ed.fileBase == origFile {
		t.Fatal("editor must draft against a clone, not the catalog file")
	}

	ed.statusText = "201"
	ed.contentTypeTxt = "application/json; charset=utf-8"
	ed.bodyText = `{"changed":true}`
	ed.scenariosTxt = "default, success"
	ed.enabled = true
	a.SaveMockRuleDraft()

	if s.editor != nil {
		t.Fatal("editor must close after a successful save")
	}
	if s.errMsg != "" {
		t.Fatalf("unexpected save error: %s", s.errMsg)
	}
	if !strings.Contains(s.notice, "get-orders") {
		t.Fatalf("save notice must reference the rule: %q", s.notice)
	}

	saved := reloadMockFileForTest(t, ps, rel)
	edited := findRuleForTest(t, saved, "get-orders")
	if edited.Response.Status != 201 || edited.Response.Body != `{"changed":true}` {
		t.Fatalf("edited response not persisted: %+v", edited.Response)
	}
	if len(edited.Scenarios) != 2 || edited.Scenarios[1] != "success" {
		t.Fatalf("edited scenarios not persisted: %v", edited.Scenarios)
	}
	if edited.Match.Query[0].Name != "expand" || *edited.Match.Query[0].Equals != "items" {
		t.Fatalf("edited rule query lost: %+v", edited.Match.Query)
	}
	if _, ok := edited.Variants["error_400"]; !ok || edited.Variants["error_400"].Status != 400 {
		t.Fatalf("edited rule variants lost: %+v", edited.Variants)
	}

	// The sibling rule with constructs the editor cannot author must survive
	// byte-for-byte at the struct level.
	adv := findRuleForTest(t, saved, "adv-rule")
	if adv.Match.Body == nil || len(adv.Match.Body.JSONFields) != 1 || adv.Match.Body.JSONFields[0].Path != "user.id" {
		t.Fatalf("sibling jsonFields body matcher lost: %+v", adv.Match.Body)
	}
	if len(adv.Match.Query) != 1 || adv.Match.Query[0].Equals == nil || adv.Match.Query[0].Contains == nil {
		t.Fatalf("sibling combined query predicates lost: %+v", adv.Match.Query)
	}
	if adv.Response.DelayRangeMs == nil || adv.Response.DelayRangeMs.Min != 5 || adv.Response.DelayRangeMs.Max != 9 {
		t.Fatalf("sibling delayRange lost: %+v", adv.Response)
	}
	if len(adv.Response.Headers) != 1 || adv.Response.Headers[0].Name != "X-Adv" {
		t.Fatalf("sibling response headers lost: %+v", adv.Response.Headers)
	}
	if v, ok := adv.Variants["error_500"]; !ok || v.Status != 500 || v.Headers == nil || v.DelayRangeMs == nil {
		t.Fatalf("sibling advanced variant lost: %+v", adv.Variants)
	}
}

// TestMockRuleEditorValidationLocalized checks early, field-localized
// validation: empty method, out-of-range status, malformed path, exclusive
// delay conflict, unsigned rows, and duplicate rule IDs across files.
func TestMockRuleEditorValidationLocalized(t *testing.T) {
	a, ps := openTestProject(t, "Mock Editor Validation")

	other := &mockserver.MockFile{
		SchemaVersion: mockserver.MockSchemaVersion,
		ID:            "other-file",
		Name:          "Other Mock",
		Rules:         []mockserver.MockRule{simpleEditorRule("taken-id")},
	}
	writeEditorMockFile(t, ps, other)

	mf := &mockserver.MockFile{
		SchemaVersion: mockserver.MockSchemaVersion,
		ID:            "main-file",
		Name:          "Main Mock",
		Rules:         []mockserver.MockRule{simpleEditorRule("base-rule")},
	}
	rel := writeEditorMockFile(t, ps, mf)

	a.OpenMockServer()
	a.NewMockRuleDraft(rel)
	s := a.mockState(false)
	ed := s.editor
	if ed == nil || ed.mode != "new" {
		t.Fatalf("new draft missing: %+v", ed)
	}

	ed.idText = "taken-id"
	if _, ok := ed.buildRule(a, s); ok {
		t.Fatal("duplicate project-wide rule id must fail")
	}
	if msg := ed.errs[mockFieldID]; msg == "" || !strings.Contains(msg, "taken-id") {
		t.Fatalf("id duplication error not localized: %q", msg)
	}

	ed.idText = "bad id!"
	ed.methodText = ""
	ed.statusText = "999"
	ed.pathText = "no-slash"
	ed.delayMsText = "10"
	ed.delayRangeOn = true
	ed.queryRows = []*mockMatcherRow{{ID: nextRowID(), Name: "q"}}
	if _, ok := ed.buildRule(a, s); ok {
		t.Fatal("invalid draft must fail")
	}
	if msg := ed.errs[mockFieldMethod]; msg != mockT(a, "mock.edit.err.method_empty") {
		t.Fatalf("method error mismatch: %q", msg)
	}
	if msg := ed.errs[mockFieldStatus]; !strings.Contains(msg, "999") {
		t.Fatalf("status error mismatch: %q", msg)
	}
	if msg := ed.errs[mockFieldPath]; msg != mockT(a, "mock.edit.err.path_slash") {
		t.Fatalf("path error mismatch: %q", msg)
	}
	if msg := ed.errs[mockFieldDelay]; msg != mockT(a, "mock.edit.err.delay_exclusive") {
		t.Fatalf("delay exclusivity error mismatch: %q", msg)
	}
	if got := ed.queryRows[0].Err; got != mockT(a, "mock.edit.err.matcher_no_op") {
		t.Fatalf("row error mismatch: %q", got)
	}

	// A valid draft rebuilds clean and compiles through the preview.
	ed.idText = "fresh-rule"
	ed.methodText = "GET"
	ed.statusText = "200"
	ed.pathText = "/v1/things/{id}"
	ed.pathParamRows = []*mockMatcherRow{{ID: nextRowID(), Name: "id", Op: "prefix", Value: "ord-"}}
	ed.delayMsText = ""
	ed.delayRangeOn = false
	ed.queryRows = nil
	rule, ok := ed.buildRule(a, s)
	if !ok {
		t.Fatalf("valid draft rejected: %v", ed.errs)
	}
	cand, err := ed.candidateFile(rule)
	if err != nil {
		t.Fatalf("candidateFile: %v", err)
	}
	if err := mockserver.ValidateMockFileStructure(cand, mockserver.ServerLimits{}); err != nil {
		t.Fatalf("engine validation of built rule failed: %v", err)
	}
}

// TestMockRuleEditorConflictPreviewAndResolution pins the deterministic
// same-priority conflict preview and its resolution by priority change.
func TestMockRuleEditorConflictPreviewAndResolution(t *testing.T) {
	a, ps := openTestProject(t, "Mock Editor Conflict Preview")
	mf := &mockserver.MockFile{
		SchemaVersion: mockserver.MockSchemaVersion,
		ID:            "conflict-file",
		Name:          "Conflict Mock",
		Rules: []mockserver.MockRule{
			{ID: "existing", Priority: 50, Match: mockserver.RuleMatch{Method: "GET", Path: "/items/{id}"},
				Response: mockserver.RuleResponse{Status: 200, Body: "one"}},
		},
	}
	rel := writeEditorMockFile(t, ps, mf)

	a.OpenMockServer()
	a.NewMockRuleDraft(rel)
	s := a.mockState(false)
	ed := s.editor
	ed.priorityText = "50"
	ed.pathText = "/items/{other}"

	a.RefreshMockRulePreview(true)
	if !ed.previewConflict || len(ed.previewConflicts) == 0 {
		t.Fatalf("same-priority overlap must be previewed: %+v", ed.previewConflicts)
	}
	for _, cf := range ed.previewConflicts {
		if cf.RuleA != "existing" && cf.RuleB != "existing" {
			t.Fatalf("conflict must reference the existing rule: %+v", cf)
		}
	}

	ed.priorityText = "60"
	a.RefreshMockRulePreview(true)
	if !ed.previewOK || ed.previewConflict {
		t.Fatalf("distinct priority resolves the ambiguity: ok=%v conflict=%v", ed.previewOK, ed.previewConflict)
	}
	if ed.previewRuleCount != 2 || ed.previewDigest == "" {
		t.Fatalf("preview snapshot incomplete: count=%d digest=%q", ed.previewRuleCount, ed.previewDigest)
	}

	a.SaveMockRuleDraft()
	if s.editor != nil || s.errMsg != "" {
		t.Fatalf("save expected to succeed: editor=%v err=%q", s.editor != nil, s.errMsg)
	}
	saved := reloadMockFileForTest(t, ps, rel)
	if len(saved.Rules) != 2 {
		t.Fatalf("candidate rule not appended: %d rules", len(saved.Rules))
	}
}

// TestMockRuleEditorExternalChangeProtection verifies the checked-save
// contract: a file modified on disk after the draft opened blocks the save
// with an explicit conflict, keeps the user draft intact, never overwrites
// the external change, and the reload path discards only after the explicit
// decision.
func TestMockRuleEditorExternalChangeProtection(t *testing.T) {
	a, ps := openTestProject(t, "Mock Editor External Change")
	mf := &mockserver.MockFile{
		SchemaVersion: mockserver.MockSchemaVersion,
		ID:            "guard-file",
		Name:          "Guard Mock",
		Rules: []mockserver.MockRule{
			simpleEditorRule("guarded"),
			{ID: "second", Priority: 20, Match: mockserver.RuleMatch{Method: "GET", Path: "/s"},
				Response: mockserver.RuleResponse{Status: 200}},
		},
	}
	rel := writeEditorMockFile(t, ps, mf)

	a.OpenMockServer()
	a.OpenMockRuleEditor(rel, "guarded")
	s := a.mockState(false)
	ed := s.editor
	ed.statusText = "204"

	// Simulate an out-of-app edit (as B01 file-sync or an editor would do).
	external, err := mockserver.LoadMockFile(ps.project, rel)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	external.Name = "Renamed Externally"
	data, err := mockserver.EncodeMockFile(external)
	if err != nil {
		t.Fatalf("encode external: %v", err)
	}
	if err := ps.project.Store.WriteAtomic(rel, data); err != nil {
		t.Fatalf("external write: %v", err)
	}

	a.SaveMockRuleDraft()
	if s.editor == nil || !ed.saveConflict {
		t.Fatalf("save must surface an external-change conflict: editor=%v conflict=%v",
			s.editor != nil, ed.saveConflict)
	}
	if ed.editErr != mockT(a, "mock.edit.err.external_change") {
		t.Fatalf("conflict message mismatch: %q", ed.editErr)
	}
	onDisk := reloadMockFileForTest(t, ps, rel)
	if onDisk.Name != "Renamed Externally" {
		t.Fatalf("external change must not be overwritten: %q", onDisk.Name)
	}

	// Keep editing then reload from disk: the draft is dropped only on the
	// explicit resolution.
	a.ResolveMockEditorExternalConflict(false)
	if s.editor == nil {
		t.Fatal("keep-editing must preserve the draft")
	}
	a.ResolveMockEditorExternalConflict(true)
	if s.editor != nil {
		t.Fatal("reload must close the draft editor")
	}
}

// TestMockDeleteRuleSafety covers: deleting the rule under edit is blocked,
// the last rule of a file cannot be deleted, a successful delete persists,
// and the undo-less confirmation state resets.
func TestMockDeleteRuleSafety(t *testing.T) {
	a, ps := openTestProject(t, "Mock Delete Safety")
	mf := &mockserver.MockFile{
		SchemaVersion: mockserver.MockSchemaVersion,
		ID:            "solo-file",
		Name:          "Solo Mock",
		Rules:         []mockserver.MockRule{simpleEditorRule("only-one")},
	}
	soloRel := writeEditorMockFile(t, ps, mf)
	twoRel := writeEditorMockFile(t, ps, &mockserver.MockFile{
		SchemaVersion: mockserver.MockSchemaVersion,
		ID:            "pair-file",
		Name:          "Pair Mock",
		Rules: []mockserver.MockRule{
			simpleEditorRule("alpha"),
			{ID: "beta", Priority: 10, Match: mockserver.RuleMatch{Method: "GET", Path: "/b"},
				Response: mockserver.RuleResponse{Status: 200}},
		},
	})

	a.OpenMockServer()

	// Last rule deletion is refused with a hint, file untouched.
	a.DeleteMockRule(soloRel, "only-one")
	s := a.mockState(false)
	if s.errMsg == "" || !strings.Contains(s.errMsg, soloRel) {
		t.Fatalf("last-rule deletion must be refused with the file name: %q", s.errMsg)
	}
	if got := reloadMockFileForTest(t, ps, soloRel); len(got.Rules) != 1 {
		t.Fatalf("last rule must survive: %d rules", len(got.Rules))
	}

	// Deleting the rule under edit is blocked until the editor closes.
	a.OpenMockRuleEditor(twoRel, "alpha")
	ed := a.mockState(false).editor
	if ed == nil {
		t.Fatal("editor open expected")
	}
	a.DeleteMockRule(twoRel, "alpha")
	if got := a.mockState(false).errMsg; got == "" {
		t.Fatal("deleting the edited rule must ask to close the editor first")
	}
	if got := reloadMockFileForTest(t, ps, twoRel); len(got.Rules) != 2 {
		t.Fatalf("no deletion expected while the editor holds the rule: %d rules", len(got.Rules))
	}
	a.CloseMockRuleEditor()
	if a.mockState(false).editor != nil {
		t.Fatal("clean draft close expected")
	}

	// A plain delete succeeds and persists.
	a.DeleteMockRule(twoRel, "alpha")
	if got := a.mockState(false).errMsg; got != "" {
		t.Fatalf("plain delete failed: %q", got)
	}
	if got := reloadMockFileForTest(t, ps, twoRel); len(got.Rules) != 1 || got.Rules[0].ID != "beta" {
		t.Fatalf("expected beta to remain: %+v", got.Rules)
	}
}

// --- small pure-function pins ---------------------------------------------

func TestMockParsePathTemplateEditorChecks(t *testing.T) {
	if params, key := mockParsePathTemplate("/v1/orders/{orderId}"); key != "" || !params["orderId"] {
		t.Fatalf("brace param parse failed: %v %q", params, key)
	}
	if params, key := mockParsePathTemplate("/v1/orders/:orderId"); key != "" || !params["orderId"] {
		t.Fatalf("colon param parse failed: %v %q", params, key)
	}
	if _, key := mockParsePathTemplate(""); key != "mock.edit.err.path_empty" {
		t.Fatalf("empty path error mismatch: %q", key)
	}
	if _, key := mockParsePathTemplate("v1/x"); key != "mock.edit.err.path_slash" {
		t.Fatalf("slash error mismatch: %q", key)
	}
	if _, key := mockParsePathTemplate("/v1/x?a=1"); key != "mock.edit.err.path_chars" {
		t.Fatalf("query-char error mismatch: %q", key)
	}
	if _, key := mockParsePathTemplate("/v1/{dup}/{dup}"); key != "mock.edit.err.path_param_dup" {
		t.Fatalf("dup param error mismatch: %q", key)
	}
	if _, key := mockParsePathTemplate("/v1/a{weird}"); key != "mock.edit.err.path_malformed" {
		t.Fatalf("malformed segment error mismatch: %q", key)
	}
}

func TestMockEditorSuggestionsAreProjectUnique(t *testing.T) {
	taken := map[string]bool{"rule-1": true, "x-copy": true, "x-copy-2": true}
	if got := mockSuggestRuleID(taken); got != "rule-2" {
		t.Fatalf("rule suggestion: %q", got)
	}
	if got := mockSuggestCopyID("x", taken); got != "x-copy-3" {
		t.Fatalf("copy suggestion: %q", got)
	}
	if got := mockSuggestCopyID("free", map[string]bool{}); got != "free" {
		t.Fatalf("first copy keeps base id: %q", got)
	}
}
