package runner

import (
	"bytes"
	"fmt"
	"io"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// LoadSuite reads and strictly decodes a suite file relative to the
// .gorequest/ directory. Unknown fields, duplicate keys, extra YAML documents
// and oversized files are rejected.
func LoadSuite(project *core.Project, rel string) (*Suite, error) {
	if project == nil || project.Store == nil {
		return nil, fmt.Errorf("loading a suite requires a loaded project")
	}
	data, _, err := project.Store.ReadBounded(rel, DefaultMaxSuiteBytes)
	if err != nil {
		return nil, fmt.Errorf("suite %q: %w", rel, err)
	}
	s, err := ParseSuite(data)
	if err != nil {
		return nil, fmt.Errorf("suite %q: %w", rel, err)
	}
	s.sourceDigest = digestBytes(data)
	return s, nil
}

// ParseSuite decodes suite YAML without reading any other file. Structural
// validation is performed by ValidateStructure. It uses the same bounded AST
// audit as core.decodeStrict before decoding, so a hostile suite cannot spend
// unbounded memory in aliases or nesting.
func ParseSuite(data []byte) (*Suite, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("suite is empty")
	}
	if int64(len(data)) > DefaultMaxSuiteBytes {
		return nil, fmt.Errorf("suite exceeds %d bytes", DefaultMaxSuiteBytes)
	}
	if _, err := core.ParseStrictYAMLTree(data, core.YAMLLimits{MaxBytes: DefaultMaxSuiteBytes}); err != nil {
		return nil, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	var s Suite
	if err := dec.Decode(&s); err != nil {
		if err == io.EOF {
			return nil, fmt.Errorf("suite is empty")
		}
		return nil, err
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("expected a single YAML document, found more")
		}
		return nil, err
	}
	return &s, nil
}

// ValidateStructure checks versions, step IDs, dependencies, the DAG,
// retry/action declarations and suite variables. Dataset readability and
// request/cross-file references are checked by Prepare.
func (s *Suite) ValidateStructure() error {
	if s == nil {
		return fmt.Errorf("nil suite")
	}
	if s.SchemaVersion != SuiteSchemaVersion {
		return fmt.Errorf("suite schemaVersion %d unsupported (this build reads %d)", s.SchemaVersion, SuiteSchemaVersion)
	}
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("suite id is empty")
	}
	if s.OnFailure == "" {
		s.OnFailure = OnFailureStop
	}
	if s.OnFailure != OnFailureStop && s.OnFailure != OnFailureContinue {
		return fmt.Errorf("onFailure must be stop or continue, got %q", s.OnFailure)
	}
	for k := range s.Variables {
		if strings.TrimSpace(k) == "" || core.IsReservedNamespace(k) {
			return fmt.Errorf("suite variable %q is empty or uses a reserved namespace", k)
		}
	}
	if err := validateActions(s.Before, "suite before"); err != nil {
		return err
	}
	if err := validateActions(s.After, "suite after"); err != nil {
		return err
	}
	if len(s.Steps) == 0 {
		return fmt.Errorf("suite has no steps")
	}
	seen := map[string]bool{}
	for i := range s.Steps {
		st := &s.Steps[i]
		if !stepIDPattern(st.ID) {
			return fmt.Errorf("step %d: id %q must match [A-Za-z_][A-Za-z0-9_-]*", i+1, st.ID)
		}
		if seen[st.ID] {
			return fmt.Errorf("duplicate step id %q", st.ID)
		}
		seen[st.ID] = true
		if strings.TrimSpace(st.Request) == "" {
			return fmt.Errorf("step %q: request is empty", st.ID)
		}
		if err := validateRetry(st.Retry, st.ID); err != nil {
			return err
		}
		if err := validateActions(st.Before, "step "+st.ID+" before"); err != nil {
			return err
		}
		if err := validateActions(st.After, "step "+st.ID+" after"); err != nil {
			return err
		}
		if _, err := parseExtractions(st.Extract); err != nil {
			return fmt.Errorf("step %q: %w", st.ID, err)
		}
		needSeen := map[string]bool{}
		for _, dep := range st.Needs {
			if dep == st.ID {
				return fmt.Errorf("step %q depends on itself", st.ID)
			}
			if needSeen[dep] {
				return fmt.Errorf("step %q lists dependency %q twice", st.ID, dep)
			}
			needSeen[dep] = true
		}
	}
	for i := range s.Steps {
		st := &s.Steps[i]
		for _, dep := range st.Needs {
			if !seen[dep] {
				return fmt.Errorf("step %q depends on unknown step %q", st.ID, dep)
			}
		}
	}
	if _, err := topoOrder(s); err != nil {
		return err
	}
	return nil
}

func validateRetry(p *RetryPolicy, stepID string) error {
	if p == nil {
		return nil
	}
	if p.MaxAttempts < 1 {
		return fmt.Errorf("step %q: retry.maxAttempts must be >= 1", stepID)
	}
	if p.DelayMs < 0 || p.JitterMs < 0 || p.DeadlineMs < 0 {
		return fmt.Errorf("step %q: retry delays must not be negative", stepID)
	}
	if p.Backoff < 0 || (p.Backoff > 0 && p.Backoff < 1) {
		return fmt.Errorf("step %q: retry.backoff must be >= 1 when set", stepID)
	}
	for _, reason := range p.On {
		switch reason {
		case RetryNetwork, RetryTimeout, RetryStatus, RetryAssertion:
		default:
			return fmt.Errorf("step %q: unknown retry reason %q", stepID, reason)
		}
	}
	for _, st := range p.Statuses {
		if st < 100 || st > 599 {
			return fmt.Errorf("step %q: retry status %d is not a valid HTTP status", stepID, st)
		}
	}
	return nil
}

// topoOrder returns steps in dependency order, using declaration order as the
// tie-breaker, and rejects cycles.
func topoOrder(s *Suite) ([]*SuiteStep, error) {
	index := make(map[string]int, len(s.Steps))
	for i := range s.Steps {
		index[s.Steps[i].ID] = i
	}
	indeg := make([]int, len(s.Steps))
	dependents := make([][]int, len(s.Steps))
	for i := range s.Steps {
		for _, dep := range s.Steps[i].Needs {
			j, ok := index[dep]
			if !ok {
				return nil, fmt.Errorf("step %q depends on unknown step %q", s.Steps[i].ID, dep)
			}
			indeg[i]++
			dependents[j] = append(dependents[j], i)
		}
	}
	var ready []int
	for i := range s.Steps {
		if indeg[i] == 0 {
			ready = append(ready, i)
		}
	}
	sort.Ints(ready)
	var order []*SuiteStep
	for len(ready) > 0 {
		i := ready[0]
		ready = ready[1:]
		order = append(order, &s.Steps[i])
		for _, j := range dependents[i] {
			indeg[j]--
			if indeg[j] == 0 {
				ready = append(ready, j)
				sort.Ints(ready)
			}
		}
	}
	if len(order) != len(s.Steps) {
		return nil, fmt.Errorf("suite contains a dependency cycle")
	}
	return order, nil
}

func stepIDPattern(id string) bool {
	if id == "" {
		return false
	}
	for i, r := range id {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r == '_':
		case i > 0 && (r >= '0' && r <= '9' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// CollectionSuite builds an explicitly ordered, linear suite from the requests
// in one collection folder. Ordering is sorted relative paths (never disk
// traversal order), and every step depends on the previous one. Callers that
// need a different order or a DAG should write a suite file instead.
func CollectionSuite(project *core.Project, collectionRel string) (*Suite, error) {
	if project == nil {
		return nil, fmt.Errorf("collection suite requires a loaded project")
	}
	collectionRel = strings.Trim(strings.ReplaceAll(collectionRel, "\\", "/"), "/")
	var found *core.CollectionEntry
	var walk func(list []*core.CollectionEntry)
	walk = func(list []*core.CollectionEntry) {
		for _, c := range list {
			if strings.Trim(strings.ReplaceAll(c.RelPath, "\\", "/"), "/") == collectionRel {
				found = c
			}
			walk(c.Children)
		}
	}
	walk(project.Collections)
	if found == nil {
		return nil, fmt.Errorf("collection %q not found", collectionRel)
	}
	var entries []*core.RequestEntry
	var collect func(c *core.CollectionEntry)
	collect = func(c *core.CollectionEntry) {
		entries = append(entries, c.Requests...)
		children := append([]*core.CollectionEntry(nil), c.Children...)
		sort.Slice(children, func(i, j int) bool { return children[i].RelPath < children[j].RelPath })
		for _, ch := range children {
			collect(ch)
		}
	}
	collect(found)
	if len(entries) == 0 {
		return nil, fmt.Errorf("collection %q has no requests", collectionRel)
	}
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].RelPath < entries[j].RelPath })
	suite := &Suite{
		SchemaVersion: SuiteSchemaVersion,
		ID:            "collection-" + strings.ReplaceAll(collectionRel, "/", "-"),
		Name:          found.Name,
		OnFailure:     OnFailureStop,
	}
	used := map[string]bool{}
	for i, e := range entries {
		name := ""
		if e.Request != nil {
			name = e.Request.Name
		}
		id := ""
		if e.Request != nil && stepIDPattern(e.Request.ID) && !used[e.Request.ID] {
			id = e.Request.ID
		} else {
			id = fmt.Sprintf("step%d", i+1)
			for used[id] {
				id += "_"
			}
		}
		used[id] = true
		step := SuiteStep{ID: id, Name: name, Request: e.RelPath}
		if i > 0 {
			step.Needs = []string{suite.Steps[i-1].ID}
		}
		suite.Steps = append(suite.Steps, step)
	}
	if err := suite.ValidateStructure(); err != nil {
		return nil, err
	}
	return suite, nil
}
