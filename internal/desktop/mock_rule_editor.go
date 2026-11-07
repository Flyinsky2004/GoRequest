package desktop

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gorequest/internal/core"
	"gorequest/internal/mockserver"
)

// mock_rule_editor.go implements the C01 phase 2 structured rule editor model.
//
// Invariants:
//   - The editor drafts against a deep clone of the file as it was loaded
//     (mockserver.CloneMockFile), keyed by the digest captured when the draft
//     opened. Saving goes through mockserver.SaveMockFile with that baseline
//     digest, so external modifications surface as an explicit conflict instead
//     of being overwritten silently; user edits are never lost to an outside
//     change without an explicit decision.
//   - Everything the desktop UI does not expose (matchers with combined
//     predicates, body jsonEquals/jsonFields, per-variant headers or
//     delayRangeMs, sibling rules) is preserved verbatim through locked rows
//     and cloned response structs, so saving an edited rule never drops
//     unknown fields or other rules.
//   - Editing and saving never start, stop, or reload a listener. The compile
//     preview runs CompileMockFiles fully in memory; applying a fresh snapshot
//     to a running server happens only through the explicit Apply action.
//   - The engine's save-time validation is the authority for template and
//     credential rules; the editor adds early, field-localized feedback only.

// mockMatcherOps are the single-predicate operators the structured editor can
// author. Existing rules that combine several predicates remain editable
// only as locked (read-only, preserved) rows.
var mockMatcherOps = []string{"equals", "notEquals", "in", "contains", "prefix", "regex", "exists"}

// mockBodyOps are the single-predicate condition operators the body matcher
// supports (the engine's BodyMatcher has no notEquals predicate).
var mockBodyOps = []string{"equals", "contains", "prefix", "regex"}

// mockBodyOpNone marks a rule that carries no body matcher.
const mockBodyOpNone = ""

type mockMatcherRow struct {
	ID    string
	Name  string
	Op    string // one of mockMatcherOps or "" while untouched
	Value string // exists: "true"/"false"; in: comma-separated candidates
	// IgnoreCase applies only to query/header matchers.
	IgnoreCase bool
	// Locked rows carry predicate combinations the structured editor cannot
	// author; they are preserved verbatim on save.
	Locked   bool
	lockedKV *mockserver.KeyValueMatcher
	lockedVM *mockserver.ValueMatcher
	Err      string
}

func (r *mockMatcherRow) empty() bool {
	return !r.Locked && strings.TrimSpace(r.Name) == "" && r.Op == "" && strings.TrimSpace(r.Value) == ""
}

// mockKVRow is one response header pair draft.
type mockKVRow struct {
	ID      string
	Name    string
	Value   string
	Enabled bool
	Err     string
}

// mockVariantRow edits one scenario variant. resp holds a full clone so
// headers, delayRangeMs, and any other non-UI fields survive untouched.
type mockVariantRow struct {
	ID         string
	Scenario   string
	resp       mockserver.RuleResponse
	statusText string
	ctype      string
	body       string
	delayText  string
	Err        string
}

// mockRuleEditor is the per-window draft for one rule being created, edited,
// or duplicated. It lives on the mockState that opened it; switching projects
// clears the editor before another project's rules can be saved into it.
type mockRuleEditor struct {
	mode           string // "new" | "edit"
	open           bool
	visible        bool
	confirmDiscard bool
	saveConflict   bool
	editErr        string

	fileRelPath    string
	ruleIndex      int // index into fileBase.Rules; -1 while mode == "new"
	baselineDigest string
	// fileBase is the file cloned when the draft opened. It is never mutated:
	// candidate assembly clones from it again.
	fileBase *mockserver.MockFile
	// ruleDraft is the cloned original rule (structure of record for fields
	// the UI does not expose, e.g. locked matchers and variant data).
	ruleDraft mockserver.MockRule

	idText       string
	nameText     string
	descText     string
	priorityText string
	scenariosTxt string
	enabled      bool
	methodText   string
	pathText     string

	statusText     string
	contentTypeTxt string
	bodyText       string
	delayMsText    string
	delayRangeOn   bool
	delayMinText   string
	delayMaxText   string
	bodyOp         string
	bodyValueText  string
	lockedBody     *mockserver.BodyMatcher

	pathParamRows  []*mockMatcherRow
	queryRows      []*mockMatcherRow
	headerRows     []*mockMatcherRow
	respHeaderRows []*mockKVRow
	variantRows    []*mockVariantRow

	// errs maps a section key (rendered next to that section) to a message.
	errs map[string]string

	// lastBuilt caches the last successful build for save assembly.
	lastBuilt *mockserver.MockRule

	dirtySnapshot    string
	lastPreviewSig   string
	previewComputed  bool
	previewConflict  bool
	previewConflicts []mockserver.RuleConflict
	previewErrMsg    string
	previewErrDetail string
	previewOK        bool
	previewRuleCount int
	previewDigest    string
}

func newMockMatcherRow() *mockMatcherRow {
	return &mockMatcherRow{ID: nextRowID()}
}

func newMockKVRow() *mockKVRow {
	return &mockKVRow{ID: nextRowID(), Enabled: true}
}

func newMockVariantRow(scenario string) *mockVariantRow {
	return &mockVariantRow{ID: nextRowID(), Scenario: scenario, statusText: "200"}
}

// signature is a stable representation of every editable input, used for
// dirty tracking and to avoid recomputing the compile preview on each frame.
func (ed *mockRuleEditor) signature() string {
	var b strings.Builder
	b.WriteString(ed.mode)
	b.WriteByte(0x1f)
	b.WriteString(ed.idText)
	b.WriteByte(0x1f)
	b.WriteString(ed.nameText)
	b.WriteByte(0x1f)
	b.WriteString(ed.descText)
	b.WriteByte(0x1f)
	b.WriteString(ed.priorityText)
	b.WriteByte(0x1f)
	b.WriteString(ed.scenariosTxt)
	b.WriteByte(0x1f)
	fmt.Fprintf(&b, "%t", ed.enabled)
	b.WriteByte(0x1f)
	b.WriteString(ed.methodText)
	b.WriteByte(0x1f)
	b.WriteString(ed.pathText)
	b.WriteByte(0x1f)
	b.WriteString(ed.statusText)
	b.WriteByte(0x1f)
	b.WriteString(ed.contentTypeTxt)
	b.WriteByte(0x1f)
	b.WriteString(ed.bodyText)
	b.WriteByte(0x1f)
	b.WriteString(ed.delayMsText)
	b.WriteByte(0x1f)
	fmt.Fprintf(&b, "%t", ed.delayRangeOn)
	b.WriteByte(0x1f)
	b.WriteString(ed.delayMinText)
	b.WriteByte(0x1f)
	b.WriteString(ed.delayMaxText)
	b.WriteByte(0x1f)
	b.WriteString(ed.bodyOp)
	b.WriteByte(0x1f)
	b.WriteString(ed.bodyValueText)
	b.WriteByte(0x1f)
	appendMatcherRows(&b, ed.pathParamRows)
	appendMatcherRows(&b, ed.queryRows)
	appendMatcherRows(&b, ed.headerRows)
	for _, r := range ed.respHeaderRows {
		b.WriteString(r.Name)
		b.WriteByte(0x1e)
		b.WriteString(r.Value)
		b.WriteByte(0x1e)
		fmt.Fprintf(&b, "%t", r.Enabled)
		b.WriteByte(0x1f)
	}
	for _, r := range ed.variantRows {
		b.WriteString(r.Scenario)
		b.WriteByte(0x1e)
		b.WriteString(r.statusText)
		b.WriteByte(0x1e)
		b.WriteString(r.ctype)
		b.WriteByte(0x1e)
		b.WriteString(r.body)
		b.WriteByte(0x1e)
		b.WriteString(r.delayText)
		b.WriteByte(0x1f)
	}
	return b.String()
}

func appendMatcherRows(b *strings.Builder, rows []*mockMatcherRow) {
	for _, r := range rows {
		fmt.Fprintf(b, "%t", r.Locked)
		b.WriteByte(0x1e)
		b.WriteString(r.Name)
		b.WriteByte(0x1e)
		b.WriteString(r.Op)
		b.WriteByte(0x1e)
		b.WriteString(r.Value)
		b.WriteByte(0x1e)
		fmt.Fprintf(b, "%t", r.IgnoreCase)
		b.WriteByte(0x1f)
	}
}

func (ed *mockRuleEditor) dirty() bool {
	return ed != nil && ed.signature() != ed.dirtySnapshot
}

func (ed *mockRuleEditor) hasErrors() bool {
	return ed != nil && ed.errorCount() > 0
}

func (ed *mockRuleEditor) errorCount() int {
	if ed == nil {
		return 0
	}
	n := len(ed.errs)
	for _, rows := range [][]*mockMatcherRow{ed.pathParamRows, ed.queryRows, ed.headerRows} {
		for _, r := range rows {
			if r.Err != "" {
				n++
			}
		}
	}
	for _, r := range ed.respHeaderRows {
		if r.Err != "" {
			n++
		}
	}
	for _, r := range ed.variantRows {
		if r.Err != "" {
			n++
		}
	}
	return n
}

func (ed *mockRuleEditor) close() {
	ed.open = false
	ed.visible = false
	ed.confirmDiscard = false
	ed.saveConflict = false
	ed.editErr = ""
}

// --- helpers ---------------------------------------------------------------

// mockProjectRuleIDs returns the rule IDs of every other project mock file
// (excluding the file at exceptFileRel), used for early ID uniqueness
// feedback; the engine re-verifies at load/compile time.
func mockProjectRuleIDs(s *mockState, exceptFileRel string) map[string]bool {
	ids := map[string]bool{}
	if s == nil {
		return ids
	}
	for _, f := range s.files {
		if f == nil || f.SourceRelPath == exceptFileRel {
			continue
		}
		for _, r := range f.Rules {
			ids[r.ID] = true
		}
	}
	return ids
}

func (a *App) mockFileByRel(s *mockState, rel string) *mockserver.MockFile {
	if s == nil {
		return nil
	}
	for _, f := range s.files {
		if f != nil && f.SourceRelPath == rel {
			return f
		}
	}
	return nil
}

// mockEditorLoadFile loads the target file fresh from disk (current digest,
// current sibling rules). The panel's cached list stays a fallback for files
// that no longer load; opening the editor never recompiles or swaps a running
// server snapshot.
func (a *App) mockEditorLoadFile(s *mockState, rel string) *mockserver.MockFile {
	if a.ps == nil || a.ps.project == nil {
		return nil
	}
	if strings.TrimSpace(rel) != "" {
		if mf, err := mockserver.LoadMockFile(a.ps.project, rel); err == nil {
			return mf
		}
	}
	return a.mockFileByRel(s, rel)
}

// mockSuggestRuleID returns a project-unique "rule-N" style id.
func mockSuggestRuleID(taken map[string]bool) string {
	base := "rule-1"
	if !taken[base] {
		return base
	}
	for i := 2; ; i++ {
		cand := fmt.Sprintf("rule-%d", i)
		if !taken[cand] {
			return cand
		}
	}
}

// mockSuggestCopyID derives an available "<base>-copy[-N]" id.
func mockSuggestCopyID(base string, taken map[string]bool) string {
	cand := base
	for i := 0; ; i++ {
		if i == 0 {
			cand = base
		} else if i == 1 {
			cand = base + "-copy"
		} else {
			cand = fmt.Sprintf("%s-copy-%d", base, i)
		}
		if !taken[cand] {
			return cand
		}
	}
}

// defaultDraftRule builds a brand-new rule shell whose response validates
// through the engine on save.
func defaultDraftRule(id string) mockserver.MockRule {
	return mockserver.MockRule{
		ID:       id,
		Priority: 100,
		Match:    mockserver.RuleMatch{Method: "GET", Path: "/"},
		Response: mockserver.RuleResponse{Status: 200},
	}
}

// --- open flows -----------------------------------------------------------

// NewMockRuleDraft opens a create-rule draft in the target file. When the
// project has no mock files yet, a default workspace file is drafted (and
// must not already exist on disk; an unparsable existing file is reported
// instead of being silently overwritten).
func (a *App) NewMockRuleDraft(fileRel string) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)

	rel := strings.TrimSpace(fileRel)
	var baseFile *mockserver.MockFile
	if rel != "" {
		if _, err := mockserver.LoadMockFile(a.ps.project, rel); err == nil {
			if strings.HasPrefix(rel, "mocks/") {
				baseFile = a.mockEditorLoadFile(s, rel)
			}
		} else {
			s.errMsg = mockTf(a, "mock.edit.err.missing_file", rel)
			a.reloadMockCatalogLocked(s)
			return
		}
	}
	if baseFile == nil && len(s.files) > 0 {
		baseFile = s.files[0]
		rel = baseFile.SourceRelPath
	}
	if baseFile == nil {
		rel = "mocks/" + defaultWorkspaceMockFileID + ".mock.yaml"
		if a.ps.project.Store.Exists(rel) {
			s.errMsg = mockTf(a, "mock.edit.err.unparsable_target", rel)
			return
		}
		baseFile = &mockserver.MockFile{
			SchemaVersion:   mockserver.MockSchemaVersion,
			ID:              defaultWorkspaceMockFileID,
			Name:            "Workspace Mock Rules",
			DefaultScenario: mockserver.DefaultScenario,
			SourceRelPath:   rel,
		}
	}

	taken := mockProjectRuleIDs(s, rel)
	for _, r := range baseFile.Rules {
		taken[r.ID] = true
	}
	ed := &mockRuleEditor{
		mode:           "new",
		open:           true,
		visible:        true,
		ruleIndex:      -1,
		fileRelPath:    baseFile.SourceRelPath,
		baselineDigest: baseFile.SourceDigest,
		fileBase:       mockserver.CloneMockFile(baseFile),
		enabled:        true,
		methodText:     "GET",
		pathText:       "/",
		priorityText:   "100",
		statusText:     "200",
	}
	ed.ruleDraft = defaultDraftRule(mockSuggestRuleID(taken))
	ed.loadRowsFromDraft()
	ed.resetDirty()
	s.editor = ed
	s.errMsg = ""
	s.deletingRuleKey = ""
}

// OpenMockRuleEditor opens an edit draft for the rule with id ruleID in file
// fileRel, loading the file and its baseline digest fresh at open time.
func (a *App) OpenMockRuleEditor(fileRel, ruleID string) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)

	rel := strings.TrimSpace(fileRel)
	mf := a.mockEditorLoadFile(s, rel)
	if mf == nil {
		s.errMsg = mockTf(a, "mock.edit.err.missing_file", fileRel)
		return
	}
	idx := -1
	for i := range mf.Rules {
		if mf.Rules[i].ID == ruleID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.errMsg = mockTf(a, "mock.edit.err.missing_rule", ruleID, mf.SourceRelPath)
		return
	}
	ed := a.newMockRuleEditorFor(mf, idx, false)
	if ed == nil {
		return
	}
	s.editor = ed
	s.errMsg = ""
	s.deletingRuleKey = ""
}

// DuplicateMockRule opens a pre-populated create draft cloning the source
// rule with a project-unique id. Saving stays explicit; nothing is written to
// disk until the user saves the draft.
func (a *App) DuplicateMockRule(fileRel, ruleID string) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)

	rel := strings.TrimSpace(fileRel)
	mf := a.mockEditorLoadFile(s, rel)
	if mf == nil {
		s.errMsg = mockTf(a, "mock.edit.err.missing_file", fileRel)
		return
	}
	idx := -1
	for i := range mf.Rules {
		if mf.Rules[i].ID == ruleID {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.errMsg = mockTf(a, "mock.edit.err.missing_rule", ruleID, mf.SourceRelPath)
		return
	}
	ed := a.newMockRuleEditorFor(mf, idx, true)
	s.editor = ed
	s.errMsg = ""
	s.deletingRuleKey = ""
}

// newMockRuleEditorFor constructs the edit (dup=false) or duplicate (dup=true)
// draft for the rule at ruleIndex.
func (a *App) newMockRuleEditorFor(mf *mockserver.MockFile, idx int, dup bool) *mockRuleEditor {
	src := mf.Rules[idx]
	draft := mockserver.CloneMockRule(src)
	mode := "edit"
	ruleIndex := idx
	baseline := mf.SourceDigest

	taken := mockProjectRuleIDs(a.mockState(false), mf.SourceRelPath)
	for i := range mf.Rules {
		if !dup && i == idx {
			continue
		}
		taken[mf.Rules[i].ID] = true
	}
	if dup {
		mode = "new"
		ruleIndex = -1
		draft.ID = mockSuggestCopyID(src.ID, taken)
		if strings.TrimSpace(draft.Name) != "" {
			draft.Name = strings.TrimSpace(draft.Name) + " (copy)"
		}
	}

	ed := &mockRuleEditor{
		mode:           mode,
		open:           true,
		visible:        true,
		fileRelPath:    mf.SourceRelPath,
		ruleIndex:      ruleIndex,
		baselineDigest: baseline,
		fileBase:       mockserver.CloneMockFile(mf),
		ruleDraft:      draft,
	}
	ed.loadRowsFromDraft()
	ed.resetDirty()
	return ed
}

// resetDirty captures the draft's clean signature and invalidates preview.
func (ed *mockRuleEditor) resetDirty() {
	ed.dirtySnapshot = ed.signature()
	ed.lastPreviewSig = ""
	ed.previewComputed = false
}

// CloseMockRuleEditor closes the editor. Unsaved drafts must be discarded
// explicitly; the UI gates calls behind the confirm banner.
func (a *App) CloseMockRuleEditor() {
	if a == nil {
		return
	}
	s := a.mockState(false)
	if s == nil || s.editor == nil {
		return
	}
	s.editor = nil
	s.notice = mockT(a, "mock.edit.msg.discard_done")
}

// --- load: rule struct -> UI rows ----------------------------------------

func (ed *mockRuleEditor) loadRowsFromDraft() {
	r := ed.ruleDraft
	ed.idText = r.ID
	ed.nameText = r.Name
	ed.descText = r.Description
	ed.priorityText = strconv.Itoa(r.Priority)
	ed.scenariosTxt = strings.Join(r.Scenarios, ", ")
	ed.enabled = r.IsEnabled()
	ed.methodText = r.Match.Method
	ed.pathText = r.Match.Path

	ed.statusText = strconv.Itoa(r.Response.Status)
	ed.contentTypeTxt = r.Response.ContentType
	ed.bodyText = r.Response.Body
	if r.Response.DelayMs > 0 {
		ed.delayMsText = strconv.Itoa(r.Response.DelayMs)
	} else {
		ed.delayMsText = ""
	}
	if r.Response.DelayRangeMs != nil {
		ed.delayRangeOn = true
		ed.delayMinText = strconv.Itoa(r.Response.DelayRangeMs.Min)
		ed.delayMaxText = strconv.Itoa(r.Response.DelayRangeMs.Max)
	} else {
		ed.delayRangeOn = false
		ed.delayMinText = ""
		ed.delayMaxText = ""
	}

	ed.pathParamRows = loadValueRows(ed.sortedMapKeys(r.Match.PathParams), r.Match.PathParams)
	ed.queryRows = loadKvRows(r.Match.Query)
	ed.headerRows = loadKvRows(r.Match.Headers)

	ed.respHeaderRows = nil
	for _, kv := range r.Response.Headers {
		ed.respHeaderRows = append(ed.respHeaderRows, &mockKVRow{
			ID: nextRowID(), Name: kv.Name, Value: kv.Value, Enabled: kv.IsEnabled(),
		})
	}

	ed.bodyOp = mockBodyOpNone
	ed.bodyValueText = ""
	ed.lockedBody = nil
	if r.Match.Body != nil {
		vm := r.Match.Body
		textOps := 0
		for _, p := range []*string{vm.Equals, vm.Contains, vm.Prefix, vm.Regex} {
			if p != nil {
				textOps++
			}
		}
		hasJSON := vm.JSONEquals != nil || len(vm.JSONFields) > 0
		if textOps == 1 && !hasJSON {
			switch {
			case vm.Equals != nil:
				ed.bodyOp = "equals"
				ed.bodyValueText = *vm.Equals
			case vm.Contains != nil:
				ed.bodyOp = "contains"
				ed.bodyValueText = *vm.Contains
			case vm.Prefix != nil:
				ed.bodyOp = "prefix"
				ed.bodyValueText = *vm.Prefix
			case vm.Regex != nil:
				ed.bodyOp = "regex"
				ed.bodyValueText = *vm.Regex
			}
		}
		if ed.bodyOp == mockBodyOpNone {
			locked := *vm
			ed.lockedBody = &locked
		}
	}

	ed.variantRows = nil
	for _, sc := range ed.sortedVariantKeys(r.Variants) {
		vr := r.Variants[sc]
		row := &mockVariantRow{ID: nextRowID(), Scenario: sc, resp: vr}
		row.statusText = strconv.Itoa(vr.Status)
		row.ctype = vr.ContentType
		row.body = vr.Body
		if vr.DelayMs > 0 {
			row.delayText = strconv.Itoa(vr.DelayMs)
		}
		ed.variantRows = append(ed.variantRows, row)
	}

	ed.errs = map[string]string{}
	ed.editErr = ""
	ed.saveConflict = false
	ed.confirmDiscard = false
}

func (ed *mockRuleEditor) sortedMapKeys(m map[string]mockserver.ValueMatcher) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (ed *mockRuleEditor) sortedVariantKeys(m map[string]mockserver.RuleResponse) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// loadValueRows converts single-predicate path param matchers into editable
// rows; anything else stays as a locked, verbatim-preserved row.
func loadValueRows(names []string, params map[string]mockserver.ValueMatcher) []*mockMatcherRow {
	rows := make([]*mockMatcherRow, 0, len(names))
	for _, name := range names {
		vm := params[name]
		if op, val, single := singlePredicateVM(vm); single {
			rows = append(rows, &mockMatcherRow{ID: nextRowID(), Name: name, Op: op, Value: val})
			continue
		}
		locked := vm
		rows = append(rows, &mockMatcherRow{ID: nextRowID(), Name: name, Locked: true, lockedVM: &locked})
	}
	return rows
}

// loadKvRows converts single-predicate query/header matchers into editable
// rows; combined predicates stay locked and preserved verbatim.
func loadKvRows(kvs []mockserver.KeyValueMatcher) []*mockMatcherRow {
	rows := make([]*mockMatcherRow, 0, len(kvs))
	for _, kv := range kvs {
		if op, val, single := singlePredicateKV(kv); single {
			rows = append(rows, &mockMatcherRow{ID: nextRowID(), Name: kv.Name, Op: op, Value: val, IgnoreCase: kv.IgnoreCase})
			continue
		}
		locked := kv
		rows = append(rows, &mockMatcherRow{ID: nextRowID(), Name: kv.Name, Locked: true, lockedKV: &locked})
	}
	return rows
}

// singlePredicateVM reports whether vm is exactly one editor-representable
// predicate and returns its op/value form.
func singlePredicateVM(vm mockserver.ValueMatcher) (string, string, bool) {
	if predCount(vm) != 1 {
		return "", "", false
	}
	switch {
	case vm.Equals != nil:
		return "equals", *vm.Equals, true
	case vm.NotEquals != nil:
		return "notEquals", *vm.NotEquals, true
	case len(vm.In) > 0:
		return "in", strings.Join(vm.In, ", "), true
	case vm.Contains != nil:
		return "contains", *vm.Contains, true
	case vm.Prefix != nil:
		return "prefix", *vm.Prefix, true
	case vm.Regex != nil:
		return "regex", *vm.Regex, true
	case vm.Exists != nil:
		return "exists", strconv.FormatBool(*vm.Exists), true
	}
	return "", "", false
}

func singlePredicateKV(kv mockserver.KeyValueMatcher) (string, string, bool) {
	return singlePredicateVM(kv.ToValueMatcher())
}

func predCount(vm mockserver.ValueMatcher) int {
	n := 0
	if vm.Exists != nil {
		n++
	}
	for _, p := range []*string{vm.Equals, vm.NotEquals, vm.Contains, vm.Prefix, vm.Regex} {
		if p != nil {
			n++
		}
	}
	if len(vm.In) > 0 {
		n++
	}
	return n
}

// --- build: UI rows -> validated rule ------------------------------------

const (
	mockFieldID        = "id"
	mockFieldPriority  = "priority"
	mockFieldScenarios = "scenarios"
	mockFieldMethod    = "method"
	mockFieldPath      = "path"
	mockFieldStatus    = "status"
	mockFieldDelay     = "delay"
	mockFieldBodyMatch = "bodyMatch"
)

// buildRule merges the UI drafts into a fresh clone of the original rule and
// validates every editor-exposed field, collecting localized messages into
// section-keyed buckets (rendered next to the failing section) and per-row
// Err strings. It never mutates the draft.
func (ed *mockRuleEditor) buildRule(a *App, s *mockState) (mockserver.MockRule, bool) {
	ed.errs = map[string]string{}
	for _, rows := range [][]*mockMatcherRow{ed.pathParamRows, ed.queryRows, ed.headerRows} {
		for _, r := range rows {
			r.Err = ""
		}
	}
	for _, r := range ed.respHeaderRows {
		r.Err = ""
	}
	for _, r := range ed.variantRows {
		r.Err = ""
	}
	ed.editErr = ""

	rule := mockserver.CloneMockRule(ed.ruleDraft)
	ok := true
	fail := func(field, format string, args ...any) {
		ed.errs[field] = mockTf(a, format, args...)
		ok = false
	}

	id := strings.TrimSpace(ed.idText)
	switch {
	case id == "":
		fail(mockFieldID, "mock.edit.err.id_empty")
	case !mockValidIdentifier(id):
		fail(mockFieldID, "mock.edit.err.id_charset", id)
	default:
		taken := mockProjectRuleIDs(s, ed.fileRelPath)
		for i, r := range ed.fileBase.Rules {
			if ed.mode == "edit" && i == ed.ruleIndex {
				continue
			}
			taken[r.ID] = true
		}
		if taken[id] {
			fail(mockFieldID, "mock.edit.err.id_dup", id, ed.fileRelPath)
		}
	}
	rule.ID = id
	rule.Name = ed.nameText
	rule.Description = ed.descText
	if !ed.enabled {
		f := false
		rule.Enabled = &f
	} else {
		rule.Enabled = nil
	}

	if p, err := strconv.Atoi(strings.TrimSpace(ed.priorityText)); err != nil {
		fail(mockFieldPriority, "mock.edit.err.priority_int", strings.TrimSpace(ed.priorityText))
	} else {
		rule.Priority = p
	}

	var scenarios []string
	seenSc := map[string]bool{}
	for _, part := range strings.Split(ed.scenariosTxt, ",") {
		sc := strings.TrimSpace(part)
		if sc == "" {
			if strings.TrimSpace(ed.scenariosTxt) != "" {
				fail(mockFieldScenarios, "mock.edit.err.scenario_blank")
			}
			continue
		}
		if seenSc[sc] {
			continue
		}
		seenSc[sc] = true
		scenarios = append(scenarios, sc)
	}
	rule.Scenarios = scenarios

	method := strings.ToUpper(strings.TrimSpace(ed.methodText))
	switch {
	case method == "":
		fail(mockFieldMethod, "mock.edit.err.method_empty")
	default:
		if err := core.ValidateHTTPMethod(method); err != nil {
			fail(mockFieldMethod, "mock.edit.err.method_invalid", method)
		}
	}
	rule.Match.Method = method

	path := strings.TrimSpace(ed.pathText)
	pathParams, pathErr := mockParsePathTemplate(path)
	if pathErr != "" {
		fail(mockFieldPath, pathErr)
	}
	rule.Match.Path = path

	pp := map[string]mockserver.ValueMatcher{}
	for _, row := range ed.pathParamRows {
		if row.empty() {
			continue
		}
		if row.Locked {
			pp[row.Name] = *row.lockedVM
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			row.Err = mockT(a, "mock.edit.err.matcher_no_name")
			ok = false
			continue
		}
		if !pathParams[name] {
			row.Err = mockTf(a, "mock.edit.err.pathparam_unknown", name, path)
			ok = false
			continue
		}
		vm, vmOK := ed.buildValueMatcher(a, row)
		if !vmOK {
			ok = false
			continue
		}
		pp[name] = vm
	}
	rule.Match.PathParams = pp

	if qm, built := ed.buildKVList(a, ed.queryRows); built {
		rule.Match.Query = qm
	} else {
		ok = false
	}
	if hm, built := ed.buildKVList(a, ed.headerRows); built {
		rule.Match.Headers = hm
	} else {
		ok = false
	}

	switch {
	case ed.lockedBody != nil:
		rule.Match.Body = ed.lockedBody
	case ed.bodyOp != mockBodyOpNone:
		bm := &mockserver.BodyMatcher{}
		val := strings.TrimSpace(ed.bodyValueText)
		switch ed.bodyOp {
		case "equals":
			bm.Equals = &val
		case "contains":
			bm.Contains = &val
		case "prefix":
			bm.Prefix = &val
		case "regex":
			if _, err := regexp.Compile(val); err != nil {
				ed.errs[mockFieldBodyMatch] = mockTf(a, "mock.edit.err.body_regex", err)
				ok = false
				return rule, false
			}
			if len(val) > 512 {
				ed.errs[mockFieldBodyMatch] = mockTf(a, "mock.edit.err.regex_len", 512)
				ok = false
				return rule, false
			}
			bm.Regex = &val
		default:
			ed.errs[mockFieldBodyMatch] = mockTf(a, "mock.edit.err.matcher_unknown_op", ed.bodyOp)
			ok = false
			return rule, false
		}
		rule.Match.Body = bm
	default:
		rule.Match.Body = nil
	}

	if st := strings.TrimSpace(ed.statusText); st == "" {
		rule.Response.Status = 0 // engine persists/normalizes to 200 on save
	} else if v, err := strconv.Atoi(st); err != nil || v < 100 || v > 599 {
		fail(mockFieldStatus, "mock.edit.err.status_range", st)
	} else {
		rule.Response.Status = v
	}
	rule.Response.ContentType = ed.contentTypeTxt
	rule.Response.Body = ed.bodyText
	rule.Response.DelayMs = 0
	rule.Response.DelayRangeMs = nil
	if dm := strings.TrimSpace(ed.delayMsText); dm != "" {
		v, err := strconv.Atoi(dm)
		switch {
		case err != nil || v < 0:
			fail(mockFieldDelay, "mock.edit.err.delay_int", dm)
		case v > mockserver.DefaultMaxDelayMs:
			fail(mockFieldDelay, "mock.edit.err.delay_max", v, mockserver.DefaultMaxDelayMs)
		case ed.delayRangeOn:
			fail(mockFieldDelay, "mock.edit.err.delay_exclusive")
		default:
			rule.Response.DelayMs = v
		}
	}
	if ed.delayRangeOn {
		vmin, errMin := strconv.Atoi(strings.TrimSpace(ed.delayMinText))
		vmax, errMax := strconv.Atoi(strings.TrimSpace(ed.delayMaxText))
		switch {
		case errMin != nil || errMax != nil || vmin < 0 || vmax < vmin:
			fail(mockFieldDelay, "mock.edit.err.delay_range")
		case vmax > mockserver.DefaultMaxDelayMs:
			fail(mockFieldDelay, "mock.edit.err.delay_max", vmax, mockserver.DefaultMaxDelayMs)
		default:
			rule.Response.DelayRangeMs = &mockserver.DelayRange{Min: vmin, Max: vmax}
		}
	}

	var respHeaders []core.KV
	for _, row := range ed.respHeaderRows {
		name := strings.TrimSpace(row.Name)
		val := row.Value
		if name == "" && strings.TrimSpace(val) == "" {
			continue
		}
		if name == "" {
			row.Err = mockT(a, "mock.edit.err.header_no_name")
			ok = false
			continue
		}
		if strings.ContainsAny(name, "\r\n") || strings.ContainsAny(val, "\r\n") {
			row.Err = mockT(a, "mock.edit.err.header_newline")
			ok = false
			continue
		}
		kv := core.KV{Name: name, Value: val}
		if !row.Enabled {
			f := false
			kv.Enabled = &f
		}
		respHeaders = append(respHeaders, kv)
	}
	rule.Response.Headers = respHeaders

	variants := map[string]mockserver.RuleResponse{}
	seenVr := map[string]bool{}
	for _, row := range ed.variantRows {
		sc := strings.TrimSpace(row.Scenario)
		if sc == "" {
			row.Err = mockT(a, "mock.edit.err.variant_scenario_empty")
			ok = false
			continue
		}
		if seenVr[sc] {
			row.Err = mockTf(a, "mock.edit.err.variant_dup", sc)
			ok = false
			continue
		}
		seenVr[sc] = true
		resp := row.resp
		if st := strings.TrimSpace(row.statusText); st == "" {
			resp.Status = 0
		} else if v, err := strconv.Atoi(st); err != nil || v < 100 || v > 599 {
			row.Err = mockTf(a, "mock.edit.err.status_range", st)
			ok = false
			continue
		} else {
			resp.Status = v
		}
		resp.ContentType = row.ctype
		resp.Body = row.body
		resp.DelayMs = 0
		if dm := strings.TrimSpace(row.delayText); dm != "" {
			v, err := strconv.Atoi(dm)
			if err != nil || v < 0 || v > mockserver.DefaultMaxDelayMs {
				row.Err = mockTf(a, "mock.edit.err.delay_int", dm)
				ok = false
				continue
			}
			resp.DelayMs = v
		}
		variants[sc] = resp
	}
	rule.Variants = variants

	if !ok {
		return rule, false
	}
	ed.lastBuilt = &rule
	return rule, true
}

// buildValueMatcher converts one editable row into a ValueMatcher.
func (ed *mockRuleEditor) buildValueMatcher(a *App, row *mockMatcherRow) (mockserver.ValueMatcher, bool) {
	val := row.Value
	switch row.Op {
	case "":
		row.Err = mockT(a, "mock.edit.err.matcher_no_op")
		return mockserver.ValueMatcher{}, false
	case "equals":
		v := val
		return mockserver.ValueMatcher{Equals: &v}, true
	case "notEquals":
		v := val
		return mockserver.ValueMatcher{NotEquals: &v}, true
	case "contains":
		v := val
		return mockserver.ValueMatcher{Contains: &v}, true
	case "prefix":
		v := val
		return mockserver.ValueMatcher{Prefix: &v}, true
	case "in":
		var list []string
		for _, part := range strings.Split(val, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				row.Err = mockT(a, "mock.edit.err.in_empty")
				return mockserver.ValueMatcher{}, false
			}
			list = append(list, part)
		}
		if len(list) == 0 {
			row.Err = mockT(a, "mock.edit.err.in_empty")
			return mockserver.ValueMatcher{}, false
		}
		return mockserver.ValueMatcher{In: list}, true
	case "regex":
		v := val
		if len(v) > 512 {
			row.Err = mockTf(a, "mock.edit.err.regex_len", 512)
			return mockserver.ValueMatcher{}, false
		}
		if _, err := regexp.Compile(v); err != nil {
			row.Err = mockTf(a, "mock.edit.err.body_regex", err)
			return mockserver.ValueMatcher{}, false
		}
		return mockserver.ValueMatcher{Regex: &v}, true
	case "exists":
		b := strings.EqualFold(strings.TrimSpace(val), "true")
		return mockserver.ValueMatcher{Exists: &b}, true
	default:
		row.Err = mockTf(a, "mock.edit.err.matcher_unknown_op", row.Op)
		return mockserver.ValueMatcher{}, false
	}
}

// buildKVList converts editable/locked rows into a matcher list.
func (ed *mockRuleEditor) buildKVList(a *App, rows []*mockMatcherRow) ([]mockserver.KeyValueMatcher, bool) {
	out := make([]mockserver.KeyValueMatcher, 0, len(rows))
	ok := true
	for _, row := range rows {
		if row.empty() {
			continue
		}
		if row.Locked {
			out = append(out, *row.lockedKV)
			continue
		}
		name := strings.TrimSpace(row.Name)
		if name == "" {
			row.Err = mockT(a, "mock.edit.err.matcher_no_name")
			ok = false
			continue
		}
		vm, vmOK := ed.buildValueMatcher(a, row)
		if !vmOK {
			ok = false
			continue
		}
		out = append(out, mockserver.KeyValueMatcher{
			Name:       name,
			Exists:     vm.Exists,
			Equals:     vm.Equals,
			NotEquals:  vm.NotEquals,
			In:         vm.In,
			Contains:   vm.Contains,
			Prefix:     vm.Prefix,
			Regex:      vm.Regex,
			IgnoreCase: row.IgnoreCase,
		})
	}
	return out, ok
}

// mockValidIdentifier mirrors the engine's rule/file id charset for immediate
// field feedback: [A-Za-z0-9][A-Za-z0-9._-]* bytes, up to 128.
func mockValidIdentifier(id string) bool {
	if id == "" || len(id) > 128 {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case i > 0 && (c == '_' || c == '-' || c == '.'):
		default:
			return false
		}
	}
	return true
}

// mockParsePathTemplate performs the editor-level path parse: leading slash,
// no query/fragment/newlines, and well-formed param segments, returning the
// declared path parameter names. Deeper template rules stay owned by the
// engine's save/compile validation.
func mockParsePathTemplate(path string) (map[string]bool, string) {
	params := map[string]bool{}
	p := strings.TrimSpace(path)
	if p == "" {
		return params, "mock.edit.err.path_empty"
	}
	if !strings.HasPrefix(p, "/") {
		return params, "mock.edit.err.path_slash"
	}
	if strings.ContainsAny(p, "?#\r\n") {
		return params, "mock.edit.err.path_chars"
	}
	seen := map[string]bool{}
	for _, seg := range strings.Split(strings.TrimPrefix(p, "/"), "/") {
		if seg == "" {
			continue
		}
		name := ""
		switch {
		case strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}"):
			name = strings.TrimSpace(seg[1 : len(seg)-1])
		case strings.HasPrefix(seg, ":"):
			name = strings.TrimSpace(seg[1:])
		default:
			if strings.ContainsAny(seg, "{}") {
				return params, "mock.edit.err.path_malformed"
			}
			continue
		}
		if !mockValidIdentifier(name) {
			return params, "mock.edit.err.path_param_name"
		}
		if seen[name] {
			return params, "mock.edit.err.path_param_dup"
		}
		seen[name] = true
		params[name] = true
	}
	return params, ""
}

// --- candidate & preview --------------------------------------------------

// candidateFile clones fileBase, replaces/inserts the built rule, encode-
// validates the whole file with the engine, and records the encoded bytes'
// digest so preview digests reflect exactly what save would write.
func (ed *mockRuleEditor) candidateFile(rule mockserver.MockRule) (*mockserver.MockFile, error) {
	cand := mockserver.CloneMockFile(ed.fileBase)
	if cand == nil {
		return nil, fmt.Errorf("editor draft has no base file")
	}
	if ed.mode == "edit" {
		if ed.ruleIndex < 0 || ed.ruleIndex >= len(cand.Rules) {
			return nil, fmt.Errorf("rule index %d out of range", ed.ruleIndex)
		}
		cand.Rules[ed.ruleIndex] = rule
	} else {
		cand.Rules = append(cand.Rules, rule)
	}
	cand.SourceRelPath = ed.fileRelPath
	data, err := mockserver.EncodeMockFile(cand)
	if err != nil {
		return nil, err
	}
	cand.SourceDigest = core.Digest(data)
	return cand, nil
}

// mockEditorPreviewFiles assembles the candidate catalog: the in-memory panel
// files with the edited file replaced by the candidate (or the candidate
// appended when it targets a not-yet-existing file).
func mockEditorPreviewFiles(s *mockState, cand *mockserver.MockFile) []*mockserver.MockFile {
	list := make([]*mockserver.MockFile, 0, len(s.files)+1)
	replaced := false
	for _, f := range s.files {
		if f == nil {
			continue
		}
		if f.SourceRelPath == cand.SourceRelPath {
			list = append(list, cand)
			replaced = true
			continue
		}
		list = append(list, f)
	}
	if !replaced {
		list = append(list, cand)
	}
	return list
}

// RefreshMockRulePreview recomputes the deterministic compile preview for the
// draft (structure validation, cross-file ID collisions, and same-priority
// ambiguity conflicts) against the panel's current scenario and seed. It runs
// fully in memory and never touches a listener.
func (a *App) RefreshMockRulePreview(force bool) {
	if a == nil {
		return
	}
	s := a.mockState(false)
	if s == nil || s.editor == nil {
		return
	}
	ed := s.editor
	sig := ed.signature()
	if !force && ed.previewComputed && sig == ed.lastPreviewSig {
		return
	}
	ed.lastPreviewSig = sig
	ed.previewComputed = true
	ed.previewConflict = false
	ed.previewConflicts = nil
	ed.previewErrMsg = ""
	ed.previewErrDetail = ""
	ed.previewOK = false
	ed.previewRuleCount = 0
	ed.previewDigest = ""

	rule, ok := ed.buildRule(a, s)
	if !ok {
		// Build errors render next to their fields; the compile preview just
		// reports that validation is pending.
		ed.previewErrMsg = mockTf(a, "mock.edit.err.fix_fields_first", ed.errorCount())
		return
	}
	cand, err := ed.candidateFile(rule)
	if err != nil {
		ed.previewErrMsg = mockT(a, "mock.edit.err.invalid_candidate")
		ed.previewErrDetail = err.Error()
		return
	}
	files := mockEditorPreviewFiles(s, cand)
	snap, compErr := mockserver.CompileMockFiles(files, s.scenario, s.seedInt(), mockserver.ServerLimits{})
	var ambErr *mockserver.AmbiguityError
	if compErr != nil && errors.As(compErr, &ambErr) {
		ed.previewConflict = true
		ed.previewConflicts = ambErr.Conflicts
		ed.previewErrMsg = mockT(a, "mock.edit.preview.conflict")
		return
	}
	if compErr != nil {
		ed.previewErrMsg = mockT(a, "mock.edit.preview.invalid")
		ed.previewErrDetail = compErr.Error()
		return
	}
	ed.previewOK = true
	ed.previewConflict = len(snap.Conflicts) > 0
	ed.previewConflicts = snap.Conflicts
	ed.previewRuleCount = len(snap.Rules)
	ed.previewDigest = snap.SnapshotDigest
}

// --- save -----------------------------------------------------------------

// SaveMockRuleDraft builds, validates, and saves the draft through the engine
// with the baseline digest captured at open time. External disk changes return
// an explicit conflict state instead of being overwritten. Saving never starts
// or stops a listener; a running server snapshot is swapped only when the
// freshly compiled catalog is conflict-free, and the outcome is reported.
func (a *App) SaveMockRuleDraft() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	if s == nil || s.editor == nil {
		return
	}
	ed := s.editor
	rule, ok := ed.buildRule(a, s)
	if !ok {
		ed.editErr = mockTf(a, "mock.edit.err.fix_fields_first", ed.errorCount())
		return
	}
	cand, err := ed.candidateFile(rule)
	if err != nil {
		ed.editErr = err.Error()
		return
	}

	if _, saveErr := mockserver.SaveMockFile(a.ps.project, cand, ed.baselineDigest); saveErr != nil {
		var conflict *core.ConflictError
		if errors.As(saveErr, &conflict) {
			ed.saveConflict = true
			ed.editErr = mockT(a, "mock.edit.err.external_change")
			return
		}
		ed.editErr = saveErr.Error()
		return
	}

	ruleID := rule.ID
	fileRel := ed.fileRelPath
	ed.close()
	s.editor = nil
	s.deletingRuleKey = ""
	a.reloadMockCatalogLocked(s)

	if s.errMsg != "" {
		// The rule file itself was saved, but the project catalog could not
		// fully recompile (e.g. issues in other files); surface the error and
		// state that a running server kept its previous snapshot.
		s.errMsg = mockTf(a, "mock.edit.msg.saved_recompile_failed", ruleID, s.errMsg)
		return
	}
	serving := !s.running
	if s.running && s.server != nil && s.snapshot != nil {
		if cur := s.server.CurrentSnapshot(); cur != nil {
			serving = cur.SnapshotDigest == s.snapshot.SnapshotDigest
		}
	}
	switch {
	case s.running && serving:
		s.notice = mockTf(a, "mock.edit.msg.saved_serving", ruleID, fileRel, s.scenario)
	case s.running:
		s.notice = mockTf(a, "mock.edit.msg.saved_pending", ruleID, fileRel)
	default:
		s.notice = mockTf(a, "mock.edit.msg.saved", ruleID, fileRel)
	}
	s.errMsg = ""
	a.status = s.notice
}

// ResolveMockEditorExternalConflict handles an external-change save conflict:
// reload keeps the user draft open for manual reconciliation, while discard
// closes the editor and reloads the catalog (unsaved edits are dropped — the
// UI only reaches this after the explicit discard confirmation).
func (a *App) ResolveMockEditorExternalConflict(reload bool) {
	if a == nil {
		return
	}
	s := a.mockState(false)
	if s == nil || s.editor == nil {
		return
	}
	if !reload {
		s.editor.saveConflict = false
		s.editor.editErr = ""
		return
	}
	s.editor = nil
	s.errMsg = ""
	a.reloadMockCatalogLocked(s)
	s.notice = mockT(a, "mock.edit.msg.reloaded_after_conflict")
	a.status = s.notice
}

// --- delete ---------------------------------------------------------------

// DeleteMockRule removes one rule from its file using a freshly loaded copy
// and its digest as the save baseline, so concurrent external edits fail the
// checked save instead of being overwritten. The last rule of a file cannot
// be deleted (the engine requires at least one rule per file); the user can
// disable that rule instead.
func (a *App) DeleteMockRule(fileRel, ruleID string) {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.mockState(true)
	a.ensureMockProjectBound(s)
	rel := strings.TrimSpace(fileRel)
	if s.editor != nil && s.editor.fileRelPath == rel && edContains(s.editor, ruleID) {
		// Deleting the rule currently being edited would orphan the editor;
		// require closing it first.
		s.errMsg = mockTf(a, "mock.edit.err.delete_open_editor", ruleID)
		return
	}
	mf, err := mockserver.LoadMockFile(a.ps.project, rel)
	if err != nil {
		s.errMsg = err.Error()
		return
	}
	idx := -1
	for i := range mf.Rules {
		if mf.Rules[i].ID == ruleID {
			idx = i
			break
		}
	}
	if idx < 0 {
		a.reloadMockCatalogLocked(s)
		s.errMsg = mockTf(a, "mock.edit.err.missing_rule", ruleID, rel)
		return
	}
	if len(mf.Rules) <= 1 {
		s.errMsg = mockTf(a, "mock.edit.err.last_rule", rel)
		return
	}
	next := make([]mockserver.MockRule, 0, len(mf.Rules)-1)
	for i := range mf.Rules {
		if i != idx {
			next = append(next, mf.Rules[i])
		}
	}
	mf.Rules = next
	if _, saveErr := mockserver.SaveMockFile(a.ps.project, mf, mf.SourceDigest); saveErr != nil {
		s.errMsg = saveErr.Error()
		a.reloadMockCatalogLocked(s)
		if s.errMsg == "" {
			s.errMsg = mockTf(a, "mock.edit.msg.delete_conflict", saveErr.Error())
		}
		return
	}
	s.deletingRuleKey = ""
	a.reloadMockCatalogLocked(s)
	if s.errMsg != "" {
		return
	}
	s.notice = mockTf(a, "mock.edit.msg.deleted", ruleID, rel)
	s.errMsg = ""
	a.status = s.notice
}

func edContains(ed *mockRuleEditor, ruleID string) bool {
	if ed == nil {
		return false
	}
	if ed.mode == "edit" && ed.ruleIndex >= 0 && ed.ruleIndex < len(ed.fileBase.Rules) {
		return ed.fileBase.Rules[ed.ruleIndex].ID == ruleID
	}
	return ed.ruleDraft.ID == ruleID
}
