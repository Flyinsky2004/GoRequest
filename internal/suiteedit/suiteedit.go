// Package suiteedit is the pure-Go draft/load/validate/save service behind the
// desktop Suite editor (A02 phase 3). It edits the existing runner.Suite format
// and nothing else: the runner package stays the single source of truth for
// parsing, structure validation and dataset semantics. The package depends only
// on internal/core, internal/runner, the standard library and yaml; it performs
// no GUI work and never reads a SecretProvider.
package suiteedit

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
	"gorequest/internal/protocolrun"
	"gorequest/internal/runner"
)

// SuiteDir is the project-relative directory for newly created suite files.
const SuiteDir = "suites"

// MaxSuiteScan bounds how many saved suite files the editor lists.
const MaxSuiteScan = 64

// Draft is one editable suite. Existing drafts keep the original file bytes and
// baseline digest so saving can patch the original YAML AST and refuse to
// overwrite external edits. New drafts have IsNew=true and an empty baseline.
type Draft struct {
	RelPath  string
	IsNew    bool
	Baseline string
	Source   []byte

	SchemaVersion int
	ID            string
	Name          string
	OnFailure     string
	Variables     map[string]string
	Before        []runner.Action
	After         []runner.Action
	Dataset       DatasetDraft
	Steps         []*StepDraft

	nextRow int
}

// StepDraft is one editable step. RowID is a stable UI identity that never
// changes on reorder; ID is the serialized step id and is read-only for steps
// loaded from disk in this phase. Assert/Extract/Retry/Before/After are
// preserved verbatim and only shown as read-only summaries.
type StepDraft struct {
	RowID      string
	ID         string
	Name       string
	RequestRef string
	Needs      []string
	IsNew      bool

	Assert  []yaml.Node
	Extract []runner.Extraction
	Retry   *runner.RetryPolicy
	Before  []runner.Action
	After   []runner.Action
}

// DatasetDraft is the editable part of a dataset. Fields and Types are
// preserved untouched; the editor only changes format/file/limits/delimiter.
type DatasetDraft struct {
	Enabled   bool
	Format    string
	File      string
	MaxRows   int
	MaxBytes  int64
	Delimiter string
	Fields    []string
	Types     map[string]string
}

// Spec converts the draft to the runner's dataset type.
func (d DatasetDraft) Spec() *runner.DatasetSpec {
	return &runner.DatasetSpec{
		Format:    strings.ToLower(strings.TrimSpace(d.Format)),
		File:      strings.TrimSpace(d.File),
		MaxRows:   d.MaxRows,
		MaxBytes:  d.MaxBytes,
		Delimiter: d.Delimiter,
		Fields:    append([]string(nil), d.Fields...),
		Types:     cloneStringMap(d.Types),
	}
}

// Load reads a saved suite through the confined Store with its real digest,
// strictly parses it and validates its structure. Unknown fields are an error
// (runner.ParseSuite uses KnownFields); a malformed suite is never cleaned up
// and re-saved silently.
func Load(project *core.Project, rel string) (*Draft, error) {
	if project == nil || project.Store == nil {
		return nil, fmt.Errorf("loading a suite requires a loaded project")
	}
	rel = normalizeRel(rel)
	if !IsSuiteFile(rel) {
		return nil, fmt.Errorf("%q is not a .suite.yaml/.suite.yml file", rel)
	}
	data, digest, err := project.Store.ReadBounded(rel, runner.DefaultMaxSuiteBytes)
	if err != nil {
		return nil, fmt.Errorf("suite %q: %w", rel, err)
	}
	suite, err := runner.ParseSuite(data)
	if err != nil {
		return nil, fmt.Errorf("suite %q: %w", rel, err)
	}
	if err := suite.ValidateStructure(); err != nil {
		return nil, fmt.Errorf("suite %q: %w", rel, err)
	}
	d := draftFromSuite(suite)
	d.RelPath = rel
	d.Baseline = digest
	d.Source = append([]byte(nil), data...)
	d.IsNew = false
	return d, nil
}

// NewDraft returns an empty, unsaved draft. The ID is editable until the first
// save; the file name is derived from it then.
func NewDraft(id, name string) *Draft {
	return &Draft{
		SchemaVersion: runner.SuiteSchemaVersion,
		ID:            strings.TrimSpace(id),
		Name:          strings.TrimSpace(name),
		OnFailure:     string(runner.OnFailureStop),
		IsNew:         true,
	}
}

func draftFromSuite(s *runner.Suite) *Draft {
	d := &Draft{
		SchemaVersion: s.SchemaVersion,
		ID:            s.ID,
		Name:          s.Name,
		OnFailure:     string(s.OnFailure),
		Variables:     cloneStringMap(s.Variables),
		Before:        cloneActions(s.Before),
		After:         cloneActions(s.After),
	}
	if d.OnFailure == "" {
		d.OnFailure = string(runner.OnFailureStop)
	}
	if s.Dataset != nil {
		d.Dataset = DatasetDraft{
			Enabled:   true,
			Format:    s.Dataset.Format,
			File:      s.Dataset.File,
			MaxRows:   s.Dataset.MaxRows,
			MaxBytes:  s.Dataset.MaxBytes,
			Delimiter: s.Dataset.Delimiter,
			Fields:    append([]string(nil), s.Dataset.Fields...),
			Types:     cloneStringMap(s.Dataset.Types),
		}
	}
	for i := range s.Steps {
		st := &s.Steps[i]
		step := &StepDraft{
			RowID:      fmt.Sprintf("row-%d", i+1),
			ID:         st.ID,
			Name:       st.Name,
			RequestRef: st.Request,
			Needs:      append([]string(nil), st.Needs...),
			Assert:     cloneNodes(st.Assert),
			Extract:    append([]runner.Extraction(nil), st.Extract...),
			Retry:      cloneRetry(st.Retry),
			Before:     cloneActions(st.Before),
			After:      cloneActions(st.After),
		}
		d.Steps = append(d.Steps, step)
	}
	d.nextRow = len(d.Steps)
	return d
}

// AddStep appends a new step with a stable row id.
func (d *Draft) AddStep(id string) *StepDraft {
	if d == nil {
		return nil
	}
	d.nextRow++
	st := &StepDraft{
		RowID:      fmt.Sprintf("row-%d", d.nextRow),
		ID:         strings.TrimSpace(id),
		IsNew:      true,
		RequestRef: "",
	}
	d.Steps = append(d.Steps, st)
	return st
}

// RemoveStep removes the step at index. Callers must run CanDeleteStep first;
// this only mutates the draft.
func (d *Draft) RemoveStep(index int) {
	if d == nil || index < 0 || index >= len(d.Steps) {
		return
	}
	d.Steps = append(d.Steps[:index], d.Steps[index+1:]...)
}

// MoveStep swaps the step at index with its neighbour in direction dir (-1 up,
// +1 down) without touching any step field.
func (d *Draft) MoveStep(index, dir int) {
	if d == nil || index < 0 || index >= len(d.Steps) {
		return
	}
	j := index + dir
	if j < 0 || j >= len(d.Steps) {
		return
	}
	d.Steps[index], d.Steps[j] = d.Steps[j], d.Steps[index]
}

// StepIndex returns the index of the step with the given row id, or -1.
func (d *Draft) StepIndex(rowID string) int {
	if d == nil {
		return -1
	}
	for i, st := range d.Steps {
		if st.RowID == rowID {
			return i
		}
	}
	return -1
}

// BuildSuite converts the draft to the runner's suite type. It is used for
// validation, for the dirty signature and for new-file encoding.
func (d *Draft) BuildSuite() *runner.Suite {
	if d == nil {
		return nil
	}
	s := &runner.Suite{
		SchemaVersion: runner.SuiteSchemaVersion,
		ID:            strings.TrimSpace(d.ID),
		Name:          strings.TrimSpace(d.Name),
		Variables:     cloneStringMap(d.Variables),
		OnFailure:     runner.OnFailure(strings.TrimSpace(d.OnFailure)),
		Before:        cloneActions(d.Before),
		After:         cloneActions(d.After),
	}
	if s.OnFailure == "" {
		s.OnFailure = runner.OnFailureStop
	}
	if d.Dataset.Enabled {
		s.Dataset = d.Dataset.Spec()
	}
	for _, st := range d.Steps {
		if st == nil {
			continue
		}
		s.Steps = append(s.Steps, runner.SuiteStep{
			ID:      strings.TrimSpace(st.ID),
			Name:    strings.TrimSpace(st.Name),
			Request: strings.TrimSpace(st.RequestRef),
			Needs:   append([]string(nil), st.Needs...),
			Assert:  cloneNodes(st.Assert),
			Extract: append([]runner.Extraction(nil), st.Extract...),
			Retry:   cloneRetry(st.Retry),
			Before:  cloneActions(st.Before),
			After:   cloneActions(st.After),
		})
	}
	return s
}

// Signature is a deterministic semantic snapshot used for dirty detection. It
// deliberately ignores comments and row ids: those are not user edits.
func (d *Draft) Signature() string {
	if d == nil {
		return ""
	}
	b, err := yaml.Marshal(d.BuildSuite())
	if err != nil {
		return ""
	}
	return string(b)
}

// Validate runs the runner's own structure validation plus the checks the
// editor can make without sending a request: every step request must resolve to
// a saved HTTP request, and a configured dataset must be readable and valid.
func (d *Draft) Validate(project *core.Project) error {
	if d == nil {
		return fmt.Errorf("nil suite draft")
	}
	suite := d.BuildSuite()
	if err := suite.ValidateStructure(); err != nil {
		return err
	}
	for i := range suite.Steps {
		st := &suite.Steps[i]
		if _, err := ResolveRequest(project, st.Request); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
	}
	if suite.Dataset != nil {
		if err := runner.ValidateDataset(project, suite.Dataset); err != nil {
			return err
		}
	}
	if d.IsNew {
		rel, err := SuiteRelPath(d.ID)
		if err != nil {
			return err
		}
		if project != nil && project.Store != nil && project.Store.Exists(rel) {
			return fmt.Errorf("suite file %q already exists; choose another id", rel)
		}
	}
	return nil
}

// Save validates the draft and writes it. A new draft uses an empty baseline, so
// an existing target is a conflict. An edited draft writes only after the
// on-disk digest still matches the digest captured at load time.
func Save(project *core.Project, d *Draft) (rel, digest string, err error) {
	if d == nil {
		return "", "", fmt.Errorf("nil suite draft")
	}
	if project == nil || project.Store == nil {
		return "", "", fmt.Errorf("saving a suite requires a loaded project")
	}
	if err := d.Validate(project); err != nil {
		return "", "", err
	}
	if d.IsNew {
		rel, err = SuiteRelPath(d.ID)
		if err != nil {
			return "", "", err
		}
		data, merr := yaml.Marshal(d.BuildSuite())
		if merr != nil {
			return "", "", merr
		}
		data = ensureTrailingNewline(data)
		if _, perr := runner.ParseSuite(data); perr != nil {
			return "", "", fmt.Errorf("built suite does not parse: %w", perr)
		}
		if werr := project.Store.SaveChecked(rel, data, ""); werr != nil {
			return "", "", werr
		}
		d.RelPath = rel
		d.Source = append([]byte(nil), data...)
		d.Baseline = core.Digest(data)
		d.IsNew = false
		return rel, d.Baseline, nil
	}
	if d.RelPath == "" {
		return "", "", fmt.Errorf("suite draft has no file path")
	}
	patched, err := PatchSuite(d.Source, d)
	if err != nil {
		return "", "", err
	}
	suite, err := runner.ParseSuite(patched)
	if err != nil {
		return "", "", fmt.Errorf("patched suite does not parse: %w", err)
	}
	if err := suite.ValidateStructure(); err != nil {
		return "", "", err
	}
	if err := project.Store.SaveChecked(d.RelPath, patched, d.Baseline); err != nil {
		return "", "", err
	}
	d.Source = append([]byte(nil), patched...)
	d.Baseline = core.Digest(patched)
	return d.RelPath, d.Baseline, nil
}

// SuiteRelPath maps a suite id to the new-file path suites/<slug>.suite.yaml.
// It rejects absolute paths, traversal and backslashes before slugging.
func SuiteRelPath(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return "", fmt.Errorf("suite id is empty")
	}
	if strings.ContainsAny(id, `/\`) || id == "." || id == ".." {
		return "", fmt.Errorf("suite id %q must not contain path separators or be a dot path", id)
	}
	slug := core.Slug(id)
	if slug == "item" && strings.ToLower(id) != "item" {
		return "", fmt.Errorf("suite id %q needs at least one letter or digit", id)
	}
	return SuiteDir + "/" + slug + ".suite.yaml", nil
}

// IsSuiteFile reports whether rel names a saved suite file.
func IsSuiteFile(rel string) bool {
	rel = strings.ToLower(rel)
	return strings.HasSuffix(rel, ".suite.yaml") || strings.HasSuffix(rel, ".suite.yml")
}

// ListSuites lists saved suite files, sorted and capped. The skipped count is
// reported so the UI never silently hides files.
func ListSuites(project *core.Project, max int) (files []string, skipped int, err error) {
	if project == nil || project.Store == nil {
		return nil, 0, fmt.Errorf("listing suites requires a loaded project")
	}
	files, err = project.Store.List(".", IsSuiteFile)
	if err != nil {
		return nil, 0, err
	}
	sort.Strings(files)
	if max > 0 && len(files) > max {
		skipped = len(files) - max
		files = files[:max]
	}
	return files, skipped, nil
}

// RequestOption is one saved request offered to a step. Selectable is false for
// WebSocket and future non-HTTP protocols, with a reason the UI can show.
type RequestOption struct {
	Entry      *core.RequestEntry
	Selectable bool
	Reason     string
}

// RequestOptions returns the project's requests in project order (then any
// stragglers by relative path), marking unsupported protocols.
func RequestOptions(project *core.Project) []RequestOption {
	if project == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []RequestOption
	add := func(entry *core.RequestEntry) {
		if entry == nil || entry.Request == nil || seen[entry.Request.ID] {
			return
		}
		seen[entry.Request.ID] = true
		opt := RequestOption{Entry: entry, Selectable: true}
		if err := CheckSelectable(entry); err != nil {
			opt.Selectable = false
			opt.Reason = err.Error()
		}
		out = append(out, opt)
	}
	for _, id := range project.Order {
		add(project.Requests[id])
	}
	var rest []*core.RequestEntry
	for _, entry := range project.Requests {
		if entry != nil && entry.Request != nil && !seen[entry.Request.ID] {
			rest = append(rest, entry)
		}
	}
	sort.Slice(rest, func(i, j int) bool { return rest[i].RelPath < rest[j].RelPath })
	for _, entry := range rest {
		add(entry)
	}
	return out
}

// ResolveRequest mirrors the runner's step request resolution: a relative path
// first, then a stable request id. Unsupported protocols are rejected here so a
// suite cannot be saved with a WebSocket/GraphQL step in this phase.
func ResolveRequest(project *core.Project, ref string) (*core.RequestEntry, error) {
	if project == nil {
		return nil, fmt.Errorf("project is not loaded")
	}
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil, fmt.Errorf("request is empty")
	}
	norm := normalizeRel(ref)
	var byRel, byID *core.RequestEntry
	for _, entry := range project.Requests {
		if entry == nil || entry.Request == nil {
			continue
		}
		if normalizeRel(entry.RelPath) == norm {
			byRel = entry
		}
		if entry.Request.ID == ref {
			byID = entry
		}
	}
	entry := byRel
	if entry == nil {
		entry = byID
	}
	if entry == nil {
		return nil, fmt.Errorf("request %q was not found", ref)
	}
	if err := CheckSelectable(entry); err != nil {
		return nil, err
	}
	return entry, nil
}

// PreviewDataset previews the draft's configured dataset through the runner's
// own bounded reader. It never resolves {{secret.name}} references.
func PreviewDataset(project *core.Project, d *Draft, maxRows int, maxBytes int64) (*runner.DatasetPreview, error) {
	if d == nil || !d.Dataset.Enabled {
		return nil, fmt.Errorf("dataset is not configured")
	}
	return runner.PreviewDataset(project, d.Dataset.Spec(), maxRows, maxBytes)
}

// CheckSelectable rejects protocols this phase cannot automate.
func CheckSelectable(entry *core.RequestEntry) error {
	if entry == nil || entry.Request == nil {
		return fmt.Errorf("request is not loaded")
	}
	proto := strings.ToLower(strings.TrimSpace(entry.Request.Protocol))
	switch proto {
	case "", core.ProtocolHTTP:
	case core.ProtocolGraphQL:
		if entry.Request.GraphQL == nil || entry.Request.GraphQL.Document == "" {
			return fmt.Errorf("GraphQL document is required")
		}
		if _, err := core.AnalyzeGraphQL(entry.Request.GraphQL.Document, entry.Request.GraphQL.OperationName); err != nil {
			return err
		}
	case core.ProtocolWebSocket:
		if _, ok := protocolrun.PlanRefFromRequest(entry.Request); !ok {
			return fmt.Errorf("WebSocket request %q needs a saved action plan", entry.Request.Name)
		}
	default:
		return fmt.Errorf("request %q uses unsupported protocol %q", entry.Request.Name, entry.Request.Protocol)
	}
	return nil
}

// CanDeleteStep blocks deleting a step that another step depends on or whose
// output is referenced by a template. It never rewrites dependencies or
// templates; the user must remove those references explicitly.
func (d *Draft) CanDeleteStep(project *core.Project, rowID string) error {
	if d == nil {
		return fmt.Errorf("nil suite draft")
	}
	index := d.StepIndex(rowID)
	if index < 0 {
		return fmt.Errorf("step row %q was not found", rowID)
	}
	id := strings.TrimSpace(d.Steps[index].ID)
	if id == "" {
		return nil
	}
	for i, other := range d.Steps {
		if i == index || other == nil {
			continue
		}
		for _, dep := range other.Needs {
			if strings.TrimSpace(dep) == id {
				return fmt.Errorf("step %q depends on %q; remove that dependency first", other.ID, id)
			}
		}
		for _, s := range stepTemplateStrings(other) {
			if referencesStep(s, id) {
				return fmt.Errorf("step %q references {{steps.%s.*}}; remove that reference first", other.ID, id)
			}
		}
		entry, err := ResolveRequest(project, other.RequestRef)
		if err != nil {
			continue
		}
		for _, s := range requestTemplateStrings(entry.Request) {
			if referencesStep(s, id) {
				return fmt.Errorf("request %q used by step %q references {{steps.%s.*}}; remove that reference first", entry.RelPath, other.ID, id)
			}
		}
	}
	for _, s := range actionTemplateStrings(d.After) {
		if referencesStep(s, id) {
			return fmt.Errorf("suite after action references {{steps.%s.*}}; remove that reference first", id)
		}
	}
	return nil
}

func referencesStep(s, id string) bool {
	prefix := "steps." + id + "."
	for _, name := range core.TemplateNames(s) {
		if strings.HasPrefix(name, prefix) {
			return true
		}
	}
	return false
}

func stepTemplateStrings(st *StepDraft) []string {
	if st == nil {
		return nil
	}
	var out []string
	for i := range st.Assert {
		out = append(out, nodeScalarStrings(&st.Assert[i])...)
	}
	out = append(out, actionTemplateStrings(st.Before)...)
	out = append(out, actionTemplateStrings(st.After)...)
	return out
}

func actionTemplateStrings(actions []runner.Action) []string {
	var out []string
	for _, a := range actions {
		if a.Set != nil {
			out = append(out, a.Set.Value)
		}
	}
	return out
}

func requestTemplateStrings(req *core.Request) []string {
	if req == nil {
		return nil
	}
	var out []string
	if req.HTTP != nil {
		out = append(out, req.HTTP.URL)
		for _, kv := range req.HTTP.Query {
			if kv.IsEnabled() {
				out = append(out, kv.Name, kv.Value)
			}
		}
		for _, kv := range req.HTTP.Headers {
			if kv.IsEnabled() {
				out = append(out, kv.Name, kv.Value)
			}
		}
		if req.HTTP.Body != nil {
			out = append(out, nodeScalarStrings(req.HTTP.Body.Value)...)
			out = append(out, req.HTTP.Body.File)
		}
	}
	out = append(out, req.Timeout)
	for _, v := range req.Variables {
		out = append(out, v)
	}
	if req.Auth != nil && req.Auth.Inline != nil {
		a := req.Auth.Inline
		out = append(out, a.Token, a.Username, a.Password, a.Name, a.Value)
	}
	return out
}

func nodeScalarStrings(n *yaml.Node) []string {
	if n == nil {
		return nil
	}
	var out []string
	if n.Kind == yaml.ScalarNode {
		out = append(out, n.Value)
	}
	for _, c := range n.Content {
		out = append(out, nodeScalarStrings(c)...)
	}
	return out
}

func normalizeRel(s string) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\\", "/"))
	return strings.TrimPrefix(s, "./")
}

func ensureTrailingNewline(b []byte) []byte {
	if len(b) == 0 || b[len(b)-1] != '\n' {
		b = append(b, '\n')
	}
	return b
}

func cloneStringMap(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func cloneActions(in []runner.Action) []runner.Action {
	if in == nil {
		return nil
	}
	out := make([]runner.Action, len(in))
	copy(out, in)
	return out
}

func cloneRetry(p *runner.RetryPolicy) *runner.RetryPolicy {
	if p == nil {
		return nil
	}
	cp := *p
	cp.On = append([]string(nil), p.On...)
	cp.Statuses = append([]int(nil), p.Statuses...)
	return &cp
}

func cloneNodes(in []yaml.Node) []yaml.Node {
	if in == nil {
		return nil
	}
	out := make([]yaml.Node, len(in))
	for i := range in {
		out[i] = *cloneNode(&in[i])
	}
	return out
}

func cloneNode(n *yaml.Node) *yaml.Node {
	if n == nil {
		return nil
	}
	cp := *n
	if len(n.Content) > 0 {
		cp.Content = make([]*yaml.Node, len(n.Content))
		for i, c := range n.Content {
			cp.Content[i] = cloneNode(c)
		}
	}
	return &cp
}
