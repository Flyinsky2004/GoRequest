package desktop

import (
	"errors"
	"fmt"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"weak"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

// Runner assertion & extraction editor (A02 phase 2).
//
// The editor edits two real, engine-consumed stores and nothing else:
//
//   - the current saved request's `assert:` node list, saved through the same
//     SaveRequestPreserving baseline-conflict port the tab save flow uses;
//   - the `extract:` lists of saved suite steps that reference the request,
//     patched surgically on the suite file's own YAML node tree so comments,
//     key order and untouched steps survive, and written only after the whole
//     suite re-parses and re-validates through runner.ParseSuite.
//
// It never parses assertion or extraction syntax itself; description and
// validation come from the runner package (editorview.go). Unknown, unsupported
// or invalid assertion nodes are kept verbatim and are only removed on an
// explicit delete, never silently. No secret value is read or written: rule
// values may carry {{secret.name}} references that only the engine resolves.

// Selectable option values inside the editor forms. These are the canonical
// DSL tokens (also the values written to disk), so selects bind strings that
// apply converts directly; localized labels live in the UI layer.
var runnerEditorExtractionFroms = []string{"jsonpath", "header", "cookie", "body"}

var runnerEditorTriStates = []string{"default", "yes", "no"}

var runnerEditorExistsOptions = []string{"true", "false"}

const (
	// runnerEditorMaxSuites bounds the suite scan at open time.
	runnerEditorMaxSuites = 32
	// runnerEditorSelectNone marks "no inline edit form open".
	runnerEditorSelectNone = -1
	// runnerEditorSelectNew is the inline-edit sentinel for a new row.
	runnerEditorSelectNew = -2
)

var runnerEditorLiteralKinds = []string{runner.LiteralString, runner.LiteralNumber, runner.LiteralBoolean, runner.LiteralNull}

// runnerEditorState belongs to one window and holds only value data, mirroring
// the runnerState weak-key lifecycle. All fields are read/written only on the
// owning window's UI thread; the editor performs no background work.
type runnerEditorState struct {
	open       bool
	requestID  string
	requestRel string
	targetKind int // assertion section (0) / extraction section (1)

	rows     []*runnerEditorRow
	rowDraft bool

	targets   []*runnerExtractTarget
	targetSel int

	editingRow    int
	editingRowNew bool
	form          runnerEditorForm
	formErr       string

	editSel     int // index into the selected target's rows, -1 = none
	extractForm runnerEditorExtractForm
	extractErr  string

	notice        string
	err           string
	dirtyTabs     int
	suitesSkipped int
	loaded        bool
}

// runnerEditorRow is one assertion draft row. Structured rows rebuild their
// node from spec at save time; preserved rows keep a deep copy of the original
// node and are never rewritten.
type runnerEditorRow struct {
	spec runner.AssertionSpec
	node *yaml.Node
}

// runnerExtractTarget is one saved suite step whose request matches the edited
// request, with the step's extraction list as a working copy.
type runnerExtractTarget struct {
	suiteRel string
	stepID   string
	label    string
	stepIdx  int
	data     []byte
	digest   string
	rows     []runner.Extraction
	dirty    bool
}

type runnerEditorForm struct {
	target      string // status | header | jsonpath | body
	message     string
	header      string
	jsonPath    string
	statusMode  int // 0 equals, 1 in
	statusEqual string
	statusIn    string
	matchers    []*runnerEditorMatcherDraft
}

type runnerEditorMatcherDraft struct {
	kind    string
	exists  string // runnerEditorExistsOptions value
	litKind string // runnerEditorLiteralKinds value
	litText string
	inText  string
	text    string
	typeSel string // runnerEditorTypeOptions value
}

var runnerEditorTypeOptions = []string{"string", "number", "boolean", "object", "array", "null"}

type runnerEditorExtractForm struct {
	name         string
	fromSel      string // runnerEditorExtractionFroms value
	path         string
	header       string
	cookie       string
	regex        string
	group        string
	requiredSel  string // runnerEditorTriStates value
	sensitiveSel string // runnerEditorTriStates value
}

var runnerEditorStates = struct {
	sync.Mutex
	byWindow map[weak.Pointer[App]]*runnerEditorState
}{byWindow: map[weak.Pointer[App]]*runnerEditorState{}}

func (a *App) runnerEditorState(create bool) *runnerEditorState {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	runnerEditorStates.Lock()
	defer runnerEditorStates.Unlock()
	if s := runnerEditorStates.byWindow[key]; s != nil {
		return s
	}
	if !create {
		return nil
	}
	s := &runnerEditorState{editingRow: -1, editSel: -1}
	runnerEditorStates.byWindow[key] = s
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		runnerEditorStates.Lock()
		delete(runnerEditorStates.byWindow, k)
		runnerEditorStates.Unlock()
	}, key)
	if a.win != nil {
		a.win.OnClosed(func() {
			runnerEditorStates.Lock()
			delete(runnerEditorStates.byWindow, key)
			runnerEditorStates.Unlock()
		})
	}
	return s
}

func (s *runnerEditorState) dirty() bool {
	if s == nil {
		return false
	}
	if s.rowDraft || s.editingRow != -1 || s.editSel != -1 {
		return true
	}
	for _, t := range s.targets {
		if t.dirty {
			return true
		}
	}
	return false
}

func (s *runnerEditorState) reset() {
	if s == nil {
		return
	}
	s.rows = nil
	s.rowDraft = false
	s.targets = nil
	s.targetSel = 0
	s.editingRow = -1
	s.editingRowNew = false
	s.formErr = ""
	s.editSel = -1
	s.extractErr = ""
	s.notice = ""
	s.err = ""
	s.loaded = false
}

// OpenRunnerEditor opens the assertion/extraction editor for the current saved
// request: the active request tab if there is one, otherwise the first saved
// request. An explicit empty state is shown when the project has none.
func (a *App) OpenRunnerEditor() {
	if a == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	s := a.runnerEditorState(true)
	s.reset()
	s.open = true
	id := ""
	if tab := a.ps.activeTab(); tab != nil && tab.Kind == "request" && tab.Entry != nil && tab.Entry.Request != nil {
		id = tab.Entry.Request.ID
	} else if len(a.ps.project.Order) > 0 {
		id = a.ps.project.Order[0]
	}
	s.requestID = id
	a.loadRunnerEditor()
}

// CloseRunnerEditor discards every draft without touching disk.
func (a *App) CloseRunnerEditor() {
	s := a.runnerEditorState(false)
	if s == nil {
		return
	}
	s.reset()
	s.open = false
}

// SetRunnerEditorRequest switches the edited request. Switching with unsaved
// drafts is refused with a notice so edits are never dropped silently.
func (a *App) SetRunnerEditorRequest(id string) {
	s := a.runnerEditorState(false)
	if s == nil || id == s.requestID {
		return
	}
	if s.dirty() {
		s.notice = a.runnerEditorMsg().PendingEdits
		return
	}
	s.reset()
	s.open = true
	s.requestID = id
	a.loadRunnerEditor()
}

// SetRunnerEditorSection switches between the assertion and extraction sections.
// A pending inline edit must be applied or canceled first.
func (a *App) SetRunnerEditorSection(idx int) {
	s := a.runnerEditorState(false)
	if s == nil || idx == s.targetKind || idx < 0 || idx > 1 {
		return
	}
	if s.editingRow != -1 || s.editSel != -1 {
		s.notice = a.runnerEditorMsg().PendingForm
		return
	}
	s.targetKind = idx
}

// ReloadRunnerEditor discards drafts and re-reads everything from the saved
// project model plus the suite files' current disk bytes.
func (a *App) ReloadRunnerEditor() {
	s := a.runnerEditorState(false)
	if s == nil || a.ps == nil {
		return
	}
	s.reset()
	s.open = true
	a.loadRunnerEditor()
}

func (a *App) loadRunnerEditor() {
	s := a.runnerEditorState(false)
	if s == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	p := a.ps.project
	entry := p.Requests[s.requestID]
	if entry == nil || entry.Request == nil {
		s.notice = a.runnerEditorMsg().NoRequest
		return
	}
	s.requestRel = entry.RelPath
	s.rows = make([]*runnerEditorRow, 0, len(entry.Request.Assert))
	specs := runner.DescribeAssertions(entry.Request.Assert)
	for i := range specs {
		row := &runnerEditorRow{spec: specs[i]}
		if !specs[i].Structured {
			row.node = runnerEditorCloneNode(&entry.Request.Assert[i])
		}
		s.rows = append(s.rows, row)
	}
	s.dirtyTabs = 0
	for _, tab := range a.ps.tabs {
		if tab.Kind == "request" && tab.Entry == entry && tab.Dirty {
			s.dirtyTabs++
		}
	}
	targets, skipped := runnerEditorExtractTargets(p, entry)
	s.targets = targets
	s.suitesSkipped = skipped
	if s.targetSel >= len(targets) {
		s.targetSel = 0
	}
	s.loaded = true
}

// runnerEditorExtractTargets scans saved suite files for steps whose request
// references the entry. Matching mirrors the runner's own step/request
// resolution (RelPath or request ID). Unreadable or invalid suites are skipped
// and reported, never silently treated as empty.
func runnerEditorExtractTargets(p *core.Project, entry *core.RequestEntry) ([]*runnerExtractTarget, int) {
	if p == nil || p.Store == nil || entry == nil || entry.Request == nil {
		return nil, 0
	}
	files, err := p.Store.List(".", func(name string) bool {
		return strings.HasSuffix(name, ".suite.yaml") || strings.HasSuffix(name, ".suite.yml")
	})
	if err != nil {
		return nil, 0
	}
	sort.Strings(files)
	if len(files) > runnerEditorMaxSuites {
		files = files[:runnerEditorMaxSuites]
	}
	var out []*runnerExtractTarget
	skipped := 0
	for _, rel := range files {
		data, digest, rerr := p.Store.ReadBounded(rel, runner.DefaultMaxSuiteBytes)
		if rerr != nil {
			skipped++
			continue
		}
		suite, perr := runner.ParseSuite(data)
		if perr != nil || suite.ValidateStructure() != nil {
			skipped++
			continue
		}
		for i := range suite.Steps {
			step := &suite.Steps[i]
			if step.Request == entry.RelPath || step.Request == entry.Request.ID {
				label := step.ID
				if step.Name != "" {
					label = step.ID + " · " + step.Name
				}
				out = append(out, &runnerExtractTarget{
					suiteRel: rel,
					stepID:   step.ID,
					label:    rel + " → " + label,
					stepIdx:  i,
					data:     append([]byte(nil), data...),
					digest:   digest,
					rows:     append([]runner.Extraction(nil), step.Extract...),
				})
			}
		}
	}
	return out, skipped
}

// --- Assertion draft actions ----------------------------------------------

// BeginRunnerEditorRow starts an inline form for a new structured assertion of
// the given target kind (status/header/jsonpath/body).
func (a *App) BeginRunnerEditorRow(target string) {
	s := a.runnerEditorState(false)
	if s == nil || !s.open {
		return
	}
	s.notice = ""
	s.err = ""
	if s.editingRow != -1 || s.editSel != -1 {
		s.err = a.runnerEditorMsg().PendingForm
		return
	}
	s.editingRow = len(s.rows)
	s.editingRowNew = true
	s.formErr = ""
	s.form = runnerEditorNewForm(target)
}

// EditRunnerEditorRow opens the inline form for an existing structured row.
// Preserved (unsupported/invalid) rows are shown verbatim and cannot be
// structurally edited; they can only be kept or explicitly deleted.
func (a *App) EditRunnerEditorRow(i int) {
	s := a.runnerEditorState(false)
	if s == nil || i < 0 || i >= len(s.rows) || !s.rows[i].spec.Structured {
		return
	}
	s.notice = ""
	s.err = ""
	if s.editingRow != -1 || s.editSel != -1 {
		s.err = a.runnerEditorMsg().PendingForm
		return
	}
	s.editingRow = i
	s.editingRowNew = false
	s.formErr = ""
	s.form = runnerEditorFormFromSpec(&s.rows[i].spec)
}

// CancelRunnerEditorRow discards the inline form.
func (a *App) CancelRunnerEditorRow() {
	s := a.runnerEditorState(false)
	if s == nil {
		return
	}
	s.editingRow = -1
	s.editingRowNew = false
	s.formErr = ""
}

// ApplyRunnerEditorRow commits the inline form: it rebuilds the assertion node,
// validates it with the engine parser, and only then replaces/inserts the row.
func (a *App) ApplyRunnerEditorRow() {
	s := a.runnerEditorState(false)
	if s == nil || s.editingRow < 0 {
		return
	}
	spec, err := runnerEditorSpecFromForm(&s.form)
	if err == nil {
		var node yaml.Node
		var nerr error
		node, nerr = runnerEditorBuildAssertionNode(spec)
		if nerr != nil {
			err = nerr
		} else if verr := runner.ValidateAssertions([]yaml.Node{node}); verr != nil {
			err = verr
		}
	}
	if err != nil {
		s.formErr = err.Error()
		return
	}
	spec.Index = s.editingRow
	row := &runnerEditorRow{spec: *spec}
	if s.editingRowNew {
		s.rows = append(s.rows, row)
	} else {
		s.rows[s.editingRow] = row
	}
	s.rowDraft = true
	s.editingRow = -1
	s.editingRowNew = false
	s.formErr = ""
}

// DeleteRunnerEditorRow explicitly removes a row from the draft list.
func (a *App) DeleteRunnerEditorRow(i int) {
	s := a.runnerEditorState(false)
	if s == nil || i < 0 || i >= len(s.rows) {
		return
	}
	s.rows = append(s.rows[:i], s.rows[i+1:]...)
	if s.editingRow == i {
		s.editingRow = -1
		s.editingRowNew = false
		s.formErr = ""
	} else if s.editingRow > i {
		s.editingRow--
	}
	s.rowDraft = true
}

// --- Extraction draft actions ---------------------------------------------

func (a *App) runnerEditorTarget() *runnerExtractTarget {
	s := a.runnerEditorState(false)
	if s == nil || s.targetSel < 0 || s.targetSel >= len(s.targets) {
		return nil
	}
	return s.targets[s.targetSel]
}

func (a *App) BeginRunnerEditorExtractNew() {
	s := a.runnerEditorState(false)
	if s == nil || !s.open || a.runnerEditorTarget() == nil {
		return
	}
	s.notice = ""
	s.err = ""
	if s.editingRow != -1 || s.editSel != -1 {
		s.err = a.runnerEditorMsg().PendingForm
		return
	}
	s.editSel = runnerEditorSelectNew
	s.extractErr = ""
	s.extractForm = runnerEditorExtractForm{fromSel: runnerEditorExtractionFroms[0], requiredSel: runnerEditorTriStates[0], sensitiveSel: runnerEditorTriStates[0]}
}

func (a *App) EditRunnerEditorExtract(i int) {
	t := a.runnerEditorTarget()
	s := a.runnerEditorState(false)
	if s == nil || t == nil || i < 0 || i >= len(t.rows) {
		return
	}
	s.notice = ""
	s.err = ""
	if s.editingRow != -1 || s.editSel != -1 {
		s.err = a.runnerEditorMsg().PendingForm
		return
	}
	s.editSel = i
	s.extractErr = ""
	s.extractForm = runnerEditorExtractFormFromExtraction(&t.rows[i])
}

func (a *App) CancelRunnerEditorExtract() {
	s := a.runnerEditorState(false)
	if s == nil {
		return
	}
	s.editSel = -1
	s.extractErr = ""
}

func (a *App) ApplyRunnerEditorExtract() {
	s := a.runnerEditorState(false)
	t := a.runnerEditorTarget()
	if s == nil || t == nil || s.editSel == -1 {
		return
	}
	ext, err := runnerEditorExtractionFromForm(&s.extractForm)
	if err == nil {
		list := append([]runner.Extraction(nil), t.rows...)
		if s.editSel == -2 {
			list = append(list, ext)
		} else {
			list[s.editSel] = ext
		}
		if verr := runner.ValidateExtractions(list); verr != nil {
			err = verr
		}
	}
	if err != nil {
		s.extractErr = err.Error()
		return
	}
	if s.editSel == -2 {
		t.rows = append(t.rows, ext)
	} else {
		t.rows[s.editSel] = ext
	}
	t.dirty = true
	s.editSel = -1
	s.extractErr = ""
}

func (a *App) DeleteRunnerEditorExtract(i int) {
	t := a.runnerEditorTarget()
	s := a.runnerEditorState(false)
	if s == nil || t == nil || i < 0 || i >= len(t.rows) {
		return
	}
	t.rows = append(t.rows[:i], t.rows[i+1:]...)
	t.dirty = true
	if s.editSel == i {
		s.editSel = -1
		s.extractErr = ""
	} else if s.editSel > i {
		s.editSel--
	}
}

// SelectRunnerEditorTarget switches the viewed suite step target. A pending
// inline extract edit is refused explicitly, never discarded.
func (a *App) SelectRunnerEditorTarget(idx int) {
	s := a.runnerEditorState(false)
	if s == nil || idx < 0 || idx >= len(s.targets) || idx == s.targetSel {
		return
	}
	if s.editSel != -1 {
		s.notice = a.runnerEditorMsg().PendingForm
		return
	}
	s.targetSel = idx
}

// --- Save ------------------------------------------------------------------

// SaveRunnerEditor writes the drafts. The request assertions go first through
// SaveRequestPreserving (digest-baseline conflict protection), then every dirty
// suite target through Store.SaveChecked. Any failure stops with an explicit
// notice and leaves later targets unwritten; nothing is written silently.
func (a *App) SaveRunnerEditor() {
	s := a.runnerEditorState(false)
	if s == nil || !s.open || a.ps == nil || a.ps.project == nil {
		return
	}
	m := a.runnerEditorMsg()
	p := a.ps.project
	s.notice = ""
	s.err = ""
	entry := p.Requests[s.requestID]
	if entry == nil || entry.Request == nil {
		s.err = m.NoRequest
		return
	}
	if !s.dirty() {
		s.notice = m.NothingToSave
		return
	}
	if s.editingRow != -1 {
		s.err = m.PendingForm
		return
	}
	if s.editSel != -1 {
		s.err = m.PendingForm
		return
	}
	if s.rowDraft {
		nodes, _, err := runnerEditorBuildAssertNodes(s.rows)
		if err != nil {
			s.err = err.Error()
			return
		}
		if verr := runner.ValidateAssertions(nodes); verr != nil {
			s.err = verr.Error()
			return
		}
		req := *entry.Request
		req.Assert = nodes
		if _, serr := p.SaveRequestPreserving(entry, &req, entry.BaselineDigest); serr != nil {
			if core.IsConflict(serr) {
				s.notice = m.SaveConflict
				s.err = ""
			} else {
				s.err = serr.Error()
			}
			return
		}
		s.rowDraft = false
		a.runnerEditorRefreshTabs(entry)
		s.notice = m.Saved
	}
	for _, t := range s.targets {
		if !t.dirty {
			continue
		}
		patched, err := runnerEditorPatchSuiteExtract(t.data, t.stepIdx, t.rows)
		if err != nil {
			s.err = err.Error()
			return
		}
		suite, perr := runner.ParseSuite(patched)
		if perr != nil {
			s.err = perr.Error()
			return
		}
		if verr := suite.ValidateStructure(); verr != nil {
			s.err = verr.Error()
			return
		}
		if suite.Steps[t.stepIdx].ID != t.stepID {
			s.err = m.SuiteChanged
			return
		}
		if len(suite.Steps[t.stepIdx].Extract) != len(t.rows) {
			s.err = m.SuiteChanged
			return
		}
		if werr := p.Store.SaveChecked(t.suiteRel, patched, t.digest); werr != nil {
			if core.IsConflict(werr) {
				s.notice = m.SaveConflict
			} else {
				s.err = werr.Error()
			}
			return
		}
		t.data = patched
		t.digest = core.Digest(patched)
		t.dirty = false
		if s.notice == "" {
			s.notice = m.Saved
		}
	}
}

// runnerEditorRefreshTabs brings this window's non-dirty tabs of the saved
// request up to the new saved state and baseline. Dirty tabs keep their edits;
// their stale baseline forces an explicit conflict on their later save instead
// of a silent overwrite of the editor's changes.
func (a *App) runnerEditorRefreshTabs(entry *core.RequestEntry) {
	s := a.runnerEditorState(false)
	if a.ps == nil || s == nil {
		return
	}
	s.dirtyTabs = 0
	for _, tab := range a.ps.tabs {
		if tab.Kind != "request" || tab.Entry != entry {
			continue
		}
		if tab.Dirty {
			s.dirtyTabs++
			continue
		}
		fillTabFromRequest(tab, entry, a.ps.project)
		tab.Baseline = entry.BaselineDigest
	}
	if s.dirtyTabs > 0 {
		m := a.runnerEditorMsg()
		s.notice = fmt.Sprintf(m.DirtyTabs, s.dirtyTabs)
	}
}

// --- Pure helpers: node building and suite patching -----------------------

// runnerEditorBuildAssertNodes materializes the draft rows into the saved
// node list: structured rows from spec, preserved rows verbatim (deep copy).
func runnerEditorBuildAssertNodes(rows []*runnerEditorRow) ([]yaml.Node, []string, error) {
	out := make([]yaml.Node, 0, len(rows))
	var warnings []string
	for i, row := range rows {
		if row.spec.Structured {
			node, err := runnerEditorBuildAssertionNode(&row.spec)
			if err != nil {
				return nil, warnings, fmt.Errorf("assertion %d: %w", i+1, err)
			}
			out = append(out, node)
			continue
		}
		if row.node == nil {
			return nil, warnings, fmt.Errorf("assertion %d: preserved node missing", i+1)
		}
		cp := runnerEditorCloneNode(row.node)
		out = append(out, *cp)
		if row.spec.Err != "" {
			warnings = append(warnings, row.spec.Err)
		}
	}
	return out, warnings, nil
}

func runnerEditorScalar(tag, value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value}
}

func runnerEditorKeyNode(key string) *yaml.Node {
	return runnerEditorScalar("!!str", key)
}

func runnerEditorStringNode(s string) *yaml.Node {
	return runnerEditorScalar("!!str", s)
}

func runnerEditorIntNode(v int) *yaml.Node {
	return runnerEditorScalar("!!int", strconv.Itoa(v))
}

func runnerEditorLiteralNode(l runner.LiteralSpec) (*yaml.Node, error) {
	switch l.Kind {
	case runner.LiteralString:
		return runnerEditorStringNode(l.Text), nil
	case runner.LiteralBoolean:
		switch strings.ToLower(strings.TrimSpace(l.Text)) {
		case "true":
			return runnerEditorScalar("!!bool", "true"), nil
		case "false":
			return runnerEditorScalar("!!bool", "false"), nil
		}
		return nil, errors.New("boolean value must be true or false")
	case runner.LiteralNull:
		return runnerEditorScalar("!!null", ""), nil
	case runner.LiteralNumber:
		text := strings.TrimSpace(l.Text)
		if _, err := strconv.ParseInt(text, 10, 64); err == nil {
			return runnerEditorScalar("!!int", text), nil
		}
		if _, err := strconv.ParseFloat(text, 64); err == nil {
			return runnerEditorScalar("!!float", text), nil
		}
		return nil, fmt.Errorf("number value %q is not numeric", l.Text)
	}
	return nil, fmt.Errorf("unknown literal kind %q", l.Kind)
}

// runnerEditorMatcherPairs appends key/value pairs for the spec's matchers in
// the engine's DSL, in a canonical order.
func runnerEditorMatcherPairs(spec *runner.AssertionSpec, pairs []*yaml.Node) ([]*yaml.Node, error) {
	for _, m := range spec.Matchers {
		switch m.Kind {
		case runner.MatcherExists:
			if m.Exists == nil {
				return pairs, errors.New("exists needs a value")
			}
			pairs = append(pairs, runnerEditorKeyNode("exists"), runnerEditorScalar("!!bool", strconv.FormatBool(*m.Exists)))
		case runner.MatcherEquals, runner.MatcherNotEquals:
			if m.Literal == nil {
				return pairs, errors.New(m.Kind + " needs a value")
			}
			v, err := runnerEditorLiteralNode(*m.Literal)
			if err != nil {
				return pairs, err
			}
			pairs = append(pairs, runnerEditorKeyNode(m.Kind), v)
		case runner.MatcherIn:
			if len(m.In) == 0 {
				return pairs, errors.New("in needs at least one value")
			}
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, l := range m.In {
				v, err := runnerEditorLiteralNode(l)
				if err != nil {
					return pairs, err
				}
				seq.Content = append(seq.Content, v)
			}
			pairs = append(pairs, runnerEditorKeyNode("in"), seq)
		case runner.MatcherContains, runner.MatcherNotContain:
			pairs = append(pairs, runnerEditorKeyNode(m.Kind), runnerEditorStringNode(m.Text))
		case runner.MatcherRegex:
			if strings.TrimSpace(m.Text) == "" {
				return pairs, errors.New("regex must not be empty")
			}
			pairs = append(pairs, runnerEditorKeyNode("regex"), runnerEditorStringNode(m.Text))
		case runner.MatcherType:
			pairs = append(pairs, runnerEditorKeyNode("type"), runnerEditorStringNode(m.Text))
		default:
			return pairs, fmt.Errorf("unknown matcher %q", m.Kind)
		}
	}
	return pairs, nil
}

// runnerEditorBuildAssertionNode rebuilds one structured assertion node from a
// spec, using the exact grammar the runner parser accepts.
func runnerEditorBuildAssertionNode(spec *runner.AssertionSpec) (yaml.Node, error) {
	var node yaml.Node
	if spec == nil {
		return node, errors.New("empty assertion")
	}
	content := make([]*yaml.Node, 0, 4)
	if msg := strings.TrimSpace(spec.Message); msg != "" {
		content = append(content, runnerEditorKeyNode("message"), runnerEditorStringNode(msg))
	}
	switch spec.Target {
	case runner.TargetStatus:
		if spec.StatusEquals == nil && len(spec.StatusIn) == 0 {
			return node, errors.New("status assertion needs a code or a code list")
		}
		if spec.StatusEquals != nil && len(spec.StatusIn) == 0 {
			content = append(content, runnerEditorKeyNode("status"), runnerEditorIntNode(*spec.StatusEquals))
			break
		}
		statusMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		pairs := make([]*yaml.Node, 0, 2)
		if spec.StatusEquals != nil {
			pairs = append(pairs, runnerEditorKeyNode("equals"), runnerEditorIntNode(*spec.StatusEquals))
		}
		if len(spec.StatusIn) > 0 {
			seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for _, v := range spec.StatusIn {
				seq.Content = append(seq.Content, runnerEditorIntNode(v))
			}
			pairs = append(pairs, runnerEditorKeyNode("in"), seq)
		}
		statusMap.Content = pairs
		content = append(content, runnerEditorKeyNode("status"), statusMap)
	case runner.TargetHeader:
		if strings.TrimSpace(spec.Header) == "" {
			return node, errors.New("header name is empty")
		}
		content = append(content, runnerEditorKeyNode("header"), runnerEditorStringNode(spec.Header))
		var err error
		content, err = runnerEditorMatcherPairs(spec, content)
		if err != nil {
			return node, err
		}
	case runner.TargetJSONPath:
		if strings.TrimSpace(spec.JSONPath) == "" {
			return node, errors.New("jsonpath is empty")
		}
		content = append(content, runnerEditorKeyNode("jsonpath"), runnerEditorStringNode(spec.JSONPath))
		var err error
		content, err = runnerEditorMatcherPairs(spec, content)
		if err != nil {
			return node, err
		}
	case runner.TargetBody:
		bodyMap := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		pairs := make([]*yaml.Node, 0, 4)
		pairs, err := runnerEditorMatcherPairs(spec, pairs)
		if err != nil {
			return node, err
		}
		if len(pairs) == 0 {
			return node, errors.New("body assertion must be a mapping of matchers")
		}
		bodyMap.Content = pairs
		content = append(content, runnerEditorKeyNode("body"), bodyMap)
	default:
		return node, fmt.Errorf("target %q cannot be built structurally", spec.Target)
	}
	return yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: content}, nil
}

// runnerEditorPatchSuiteExtract replaces one step's extract sequence inside
// the suite file's own YAML tree. The extract key node is kept in place so its
// comments and key order survive; only the value node is replaced. Clearing the
// list removes the key/value but keeps any comments attached to them at a
// stable position inside the same step (never on a sibling step). The yaml.v3
// node round-trip preserves comments and untouched keys, not byte-level layout.
func runnerEditorPatchSuiteExtract(data []byte, stepIdx int, exts []runner.Extraction) ([]byte, error) {
	if stepIdx < 0 {
		return nil, errors.New("suite step index is invalid")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, err
	}
	root := &doc
	if root.Kind == yaml.DocumentNode && len(root.Content) > 0 {
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, errors.New("suite file is not a mapping")
	}
	var steps *yaml.Node
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "steps" {
			steps = root.Content[i+1]
			break
		}
	}
	if steps == nil || steps.Kind != yaml.SequenceNode || stepIdx >= len(steps.Content) {
		return nil, errors.New("suite steps changed; reload before editing")
	}
	step := steps.Content[stepIdx]
	if step.Kind != yaml.MappingNode {
		return nil, errors.New("suite step is not a mapping")
	}

	extractIdx := -1
	for i := 0; i+1 < len(step.Content); i += 2 {
		if step.Content[i].Value == "extract" {
			extractIdx = i
			break
		}
	}

	if len(exts) > 0 {
		seq, err := runnerEditorBuildExtractSeq(exts)
		if err != nil {
			return nil, err
		}
		if extractIdx >= 0 {
			// Keep the original key node (and its comments) in place; only the
			// value changes. Carry over any comments attached to the old value.
			oldVal := step.Content[extractIdx+1]
			seq.HeadComment = oldVal.HeadComment
			seq.LineComment = oldVal.LineComment
			seq.FootComment = oldVal.FootComment
			step.Content[extractIdx+1] = seq
		} else {
			step.Content = append(step.Content, runnerEditorKeyNode("extract"), seq)
		}
	} else if extractIdx >= 0 {
		key := step.Content[extractIdx]
		val := step.Content[extractIdx+1]
		comments := runnerEditorMergeComments(key.HeadComment, key.LineComment, key.FootComment, val.HeadComment, val.LineComment, val.FootComment)
		step.Content = append(step.Content[:extractIdx], step.Content[extractIdx+2:]...)
		if comments != "" {
			if extractIdx >= 2 {
				// Keep the comment inside the same step, after the previous key.
				prevKey := step.Content[extractIdx-2]
				prevKey.FootComment = runnerEditorMergeComments(prevKey.FootComment, comments)
			} else {
				// extract was the step's first key: keep it on the step itself.
				step.HeadComment = runnerEditorMergeComments(step.HeadComment, comments)
			}
		}
	}
	return runnerEditorEncodeNode(&doc)
}

// runnerEditorBuildExtractSeq builds the extract sequence node from validated
// extraction values using the exact grammar the runner parser accepts.
func runnerEditorBuildExtractSeq(exts []runner.Extraction) (*yaml.Node, error) {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, e := range exts {
		item := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		pairs := make([]*yaml.Node, 0, 8)
		pairs = append(pairs, runnerEditorKeyNode("name"), runnerEditorStringNode(e.Name))
		pairs = append(pairs, runnerEditorKeyNode("from"), runnerEditorStringNode(strings.ToLower(strings.TrimSpace(e.From))))
		switch strings.ToLower(strings.TrimSpace(e.From)) {
		case "jsonpath":
			pairs = append(pairs, runnerEditorKeyNode("path"), runnerEditorStringNode(e.Path))
		case "header":
			pairs = append(pairs, runnerEditorKeyNode("header"), runnerEditorStringNode(e.Header))
		case "cookie":
			pairs = append(pairs, runnerEditorKeyNode("cookie"), runnerEditorStringNode(e.Cookie))
		case "body":
			pairs = append(pairs, runnerEditorKeyNode("regex"), runnerEditorStringNode(e.Regex))
			if e.Group != nil {
				pairs = append(pairs, runnerEditorKeyNode("group"), runnerEditorIntNode(*e.Group))
			}
		default:
			return nil, fmt.Errorf("extraction %q: unknown source %q", e.Name, e.From)
		}
		if e.Required != nil {
			pairs = append(pairs, runnerEditorKeyNode("required"), runnerEditorScalar("!!bool", strconv.FormatBool(*e.Required)))
		}
		if e.Sensitive != nil {
			pairs = append(pairs, runnerEditorKeyNode("sensitive"), runnerEditorScalar("!!bool", strconv.FormatBool(*e.Sensitive)))
		}
		item.Content = pairs
		seq.Content = append(seq.Content, item)
	}
	return seq, nil
}

// runnerEditorMergeComments joins non-empty comment blocks, dropping exact
// duplicate lines so relocating a removed field's comments never doubles a
// line or overwrites an existing neighbouring comment.
func runnerEditorMergeComments(parts ...string) string {
	var out []string
	seen := map[string]bool{}
	for _, p := range parts {
		for _, line := range strings.Split(p, "\n") {
			line = strings.TrimRight(line, " \t")
			if strings.TrimSpace(line) == "" || seen[line] {
				continue
			}
			seen[line] = true
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func runnerEditorEncodeNode(doc *yaml.Node) ([]byte, error) {
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	out := []byte(sb.String())
	if len(out) == 0 || out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out, nil
}

// runnerEditorCloneNode deep-copies a node tree without style stripping.
func runnerEditorCloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = runnerEditorCloneNode(c)
		}
	}
	return &cp
}

// --- Form <-> spec conversions --------------------------------------------

func runnerEditorNewForm(target string) runnerEditorForm {
	switch target {
	case runner.TargetStatus:
		return runnerEditorForm{target: runner.TargetStatus, statusMode: 0, statusEqual: "200"}
	case runner.TargetHeader:
		return runnerEditorForm{target: runner.TargetHeader, matchers: []*runnerEditorMatcherDraft{{kind: runner.MatcherExists, exists: runnerEditorExistsOptions[0]}}}
	case runner.TargetJSONPath:
		return runnerEditorForm{target: runner.TargetJSONPath, jsonPath: "$.", matchers: []*runnerEditorMatcherDraft{{kind: runner.MatcherExists, exists: runnerEditorExistsOptions[0]}}}
	default:
		return runnerEditorForm{target: runner.TargetBody, matchers: []*runnerEditorMatcherDraft{{kind: runner.MatcherContains}}}
	}
}

func runnerEditorFormFromSpec(spec *runner.AssertionSpec) runnerEditorForm {
	f := runnerEditorForm{target: spec.Target, message: spec.Message, header: spec.Header, jsonPath: spec.JSONPath}
	if spec.StatusEquals != nil && len(spec.StatusIn) == 0 {
		f.statusMode = 0
		f.statusEqual = strconv.Itoa(*spec.StatusEquals)
	} else {
		f.statusMode = 1
		vals := make([]string, 0, len(spec.StatusIn))
		if spec.StatusEquals != nil {
			vals = append(vals, strconv.Itoa(*spec.StatusEquals))
		}
		for _, v := range spec.StatusIn {
			vals = append(vals, strconv.Itoa(v))
		}
		f.statusIn = strings.Join(vals, ", ")
	}
	for _, m := range spec.Matchers {
		switch m.Kind {
		case runner.MatcherExists:
			v := runnerEditorExistsOptions[0]
			if m.Exists != nil && !*m.Exists {
				v = runnerEditorExistsOptions[1]
			}
			f.matchers = append(f.matchers, &runnerEditorMatcherDraft{kind: runner.MatcherExists, exists: v})
		case runner.MatcherEquals, runner.MatcherNotEquals:
			litKind := runnerEditorLiteralKinds[0]
			litText := ""
			if m.Literal != nil {
				litText = m.Literal.Text
				if i := runnerEditorIndexOf(runnerEditorLiteralKinds, m.Literal.Kind); i >= 0 {
					litKind = m.Literal.Kind
				} else {
					litKind = m.Literal.Kind
				}
			}
			f.matchers = append(f.matchers, &runnerEditorMatcherDraft{kind: m.Kind, litKind: litKind, litText: litText})
		case runner.MatcherIn:
			vals := make([]string, 0, len(m.In))
			for _, l := range m.In {
				if l.Kind == runner.LiteralNull {
					vals = append(vals, "null")
					continue
				}
				vals = append(vals, l.Text)
			}
			f.matchers = append(f.matchers, &runnerEditorMatcherDraft{kind: runner.MatcherIn, inText: strings.Join(vals, ", ")})
		case runner.MatcherContains, runner.MatcherNotContain, runner.MatcherRegex:
			f.matchers = append(f.matchers, &runnerEditorMatcherDraft{kind: m.Kind, text: m.Text})
		case runner.MatcherType:
			typeSel := runnerEditorTypeOptions[0]
			if i := runnerEditorIndexOf(runnerEditorTypeOptions, m.Text); i >= 0 {
				typeSel = m.Text
			}
			f.matchers = append(f.matchers, &runnerEditorMatcherDraft{kind: runner.MatcherType, typeSel: typeSel})
		}
	}
	return f
}

func runnerEditorSpecFromForm(f *runnerEditorForm) (*runner.AssertionSpec, error) {
	spec := &runner.AssertionSpec{Target: f.target, Structured: true, Message: strings.TrimSpace(f.message)}
	switch f.target {
	case runner.TargetStatus:
		if f.statusMode == 0 {
			v, err := strconv.Atoi(strings.TrimSpace(f.statusEqual))
			if err != nil {
				return nil, errors.New("status code must be an integer")
			}
			spec.StatusEquals = &v
		} else {
			vals, err := runnerEditorIntList(f.statusIn)
			if err != nil {
				return nil, err
			}
			if len(vals) == 0 {
				return nil, errors.New("status code list must not be empty")
			}
			spec.StatusIn = vals
		}
	case runner.TargetHeader:
		if strings.TrimSpace(f.header) == "" {
			return nil, errors.New("header name is empty")
		}
		spec.Header = strings.TrimSpace(f.header)
	case runner.TargetJSONPath:
		if strings.TrimSpace(f.jsonPath) == "" {
			return nil, errors.New("jsonpath is empty")
		}
		spec.JSONPath = strings.TrimSpace(f.jsonPath)
	case runner.TargetBody:
	default:
		return nil, fmt.Errorf("unsupported target %q", f.target)
	}
	for _, d := range f.matchers {
		switch d.kind {
		case runner.MatcherExists:
			v := d.exists != runnerEditorExistsOptions[1]
			spec.Matchers = append(spec.Matchers, runner.MatcherSpec{Kind: runner.MatcherExists, Exists: &v})
		case runner.MatcherEquals, runner.MatcherNotEquals:
			if runnerEditorIndexOf(runnerEditorLiteralKinds, d.litKind) < 0 {
				return nil, errors.New("literal type must be selected")
			}
			spec.Matchers = append(spec.Matchers, runner.MatcherSpec{Kind: d.kind, Literal: &runner.LiteralSpec{Kind: d.litKind, Text: d.litText}})
		case runner.MatcherIn:
			lits, err := runnerEditorCSVLiterals(d.inText)
			if err != nil {
				return nil, err
			}
			if len(lits) == 0 {
				return nil, errors.New("in list must not be empty")
			}
			spec.Matchers = append(spec.Matchers, runner.MatcherSpec{Kind: runner.MatcherIn, In: lits})
		case runner.MatcherContains, runner.MatcherNotContain, runner.MatcherRegex:
			spec.Matchers = append(spec.Matchers, runner.MatcherSpec{Kind: d.kind, Text: d.text})
		case runner.MatcherType:
			if runnerEditorIndexOf(runnerEditorTypeOptions, d.typeSel) < 0 {
				return nil, errors.New("type must be selected")
			}
			spec.Matchers = append(spec.Matchers, runner.MatcherSpec{Kind: runner.MatcherType, Text: d.typeSel})
		default:
			return nil, fmt.Errorf("unknown matcher %q", d.kind)
		}
	}
	if spec.Target != runner.TargetStatus && len(spec.Matchers) == 0 {
		return nil, errors.New("at least one matcher is required")
	}
	return spec, nil
}

// runnerEditorCSVLiterals parses a comma-separated list with YAML scalar
// inference: ints/floats become numbers, true/false booleans, null null,
// everything else a literal string (documented to the user; strings never
// silently coerce at comparison time).
func runnerEditorCSVLiterals(text string) ([]runner.LiteralSpec, error) {
	var out []runner.LiteralSpec
	for _, tok := range strings.Split(text, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		switch tok {
		case "true", "false":
			out = append(out, runner.LiteralSpec{Kind: runner.LiteralBoolean, Text: tok})
			continue
		case "null", "~":
			out = append(out, runner.LiteralSpec{Kind: runner.LiteralNull})
			continue
		}
		if _, err := strconv.ParseInt(tok, 10, 64); err == nil {
			out = append(out, runner.LiteralSpec{Kind: runner.LiteralNumber, Text: tok})
			continue
		}
		if _, err := strconv.ParseFloat(tok, 64); err == nil {
			out = append(out, runner.LiteralSpec{Kind: runner.LiteralNumber, Text: tok})
			continue
		}
		out = append(out, runner.LiteralSpec{Kind: runner.LiteralString, Text: tok})
	}
	return out, nil
}

func runnerEditorIntList(text string) ([]int, error) {
	var out []int
	for _, tok := range strings.Split(text, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		v, err := strconv.Atoi(tok)
		if err != nil {
			return nil, fmt.Errorf("status code %q must be an integer", tok)
		}
		out = append(out, v)
	}
	return out, nil
}

func runnerEditorAddMatcherDraft(s *runnerEditorState, kind string) {
	if s == nil {
		return
	}
	d := &runnerEditorMatcherDraft{kind: kind}
	switch kind {
	case runner.MatcherExists:
		d.exists = runnerEditorExistsOptions[0]
	case runner.MatcherEquals, runner.MatcherNotEquals:
		d.litKind = runnerEditorLiteralKinds[0]
	case runner.MatcherType:
		d.typeSel = runnerEditorTypeOptions[0]
	}
	s.form.matchers = append(s.form.matchers, d)
}

func runnerEditorRemoveMatcherDraft(s *runnerEditorState, i int) {
	if s == nil || i < 0 || i >= len(s.form.matchers) {
		return
	}
	s.form.matchers = append(s.form.matchers[:i], s.form.matchers[i+1:]...)
}

// --- Extraction form <-> runner.Extraction -------------------------------

func runnerEditorExtractionFromForm(f *runnerEditorExtractForm) (runner.Extraction, error) {
	e := runner.Extraction{}
	e.Name = strings.TrimSpace(f.name)
	if runnerEditorIndexOf(runnerEditorExtractionFroms, f.fromSel) < 0 {
		return e, errors.New("extraction source must be selected")
	}
	e.From = f.fromSel
	e.Path = strings.TrimSpace(f.path)
	e.Header = strings.TrimSpace(f.header)
	e.Cookie = strings.TrimSpace(f.cookie)
	e.Regex = strings.TrimSpace(f.regex)
	if g := strings.TrimSpace(f.group); g != "" {
		v, err := strconv.Atoi(g)
		if err != nil || v < 0 {
			return e, errors.New("regex group must be a non-negative integer")
		}
		e.Group = &v
	}
	switch e.From {
	case "body":
		if e.Group != nil && e.Regex == "" {
			return e, errors.New("group requires a regex")
		}
	default:
		e.Group = nil
	}
	if f.requiredSel == runnerEditorTriStates[1] {
		v := true
		e.Required = &v
	} else if f.requiredSel == runnerEditorTriStates[2] {
		v := false
		e.Required = &v
	}
	if f.sensitiveSel == runnerEditorTriStates[1] {
		v := true
		e.Sensitive = &v
	} else if f.sensitiveSel == runnerEditorTriStates[2] {
		v := false
		e.Sensitive = &v
	}
	return e, nil
}

func runnerEditorExtractFormFromExtraction(e *runner.Extraction) runnerEditorExtractForm {
	f := runnerEditorExtractForm{name: e.Name, fromSel: runnerEditorExtractionFroms[0], requiredSel: runnerEditorTriStates[0], sensitiveSel: runnerEditorTriStates[0]}
	if i := runnerEditorIndexOf(runnerEditorExtractionFroms, strings.ToLower(strings.TrimSpace(e.From))); i >= 0 {
		f.fromSel = runnerEditorExtractionFroms[i]
	}
	f.path = e.Path
	f.header = e.Header
	f.cookie = e.Cookie
	f.regex = e.Regex
	if e.Group != nil {
		f.group = strconv.Itoa(*e.Group)
	}
	if e.Required != nil {
		f.requiredSel = runnerEditorTriStates[1]
		if !*e.Required {
			f.requiredSel = runnerEditorTriStates[2]
		}
	}
	if e.Sensitive != nil {
		f.sensitiveSel = runnerEditorTriStates[1]
		if !*e.Sensitive {
			f.sensitiveSel = runnerEditorTriStates[2]
		}
	}
	return f
}

func runnerEditorIndexOf(list []string, v string) int {
	for i, s := range list {
		if s == v {
			return i
		}
	}
	return -1
}
