package suiteedit

// Surgical YAML patching for saved suites. The original file is decoded to a
// yaml.Node tree; only the fields the editor actually changed are touched, and
// existing step nodes are reused by stable step id so comments, key order and
// untouched assert/extract/retry/before/after/variables survive. The result is
// re-parsed and re-validated by the runner before it is written.

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/runner"
)

// PatchSuite applies the draft to the original suite bytes and returns the new
// file content. It does not write anything.
func PatchSuite(source []byte, d *Draft) ([]byte, error) {
	if d == nil {
		return nil, fmt.Errorf("nil suite draft")
	}
	if len(source) == 0 {
		return nil, fmt.Errorf("suite source is empty; reload the file")
	}
	orig, err := runner.ParseSuite(source)
	if err != nil {
		return nil, fmt.Errorf("suite source no longer parses: %w", err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(source, &doc); err != nil {
		return nil, err
	}
	root := &doc
	if root.Kind == yaml.DocumentNode {
		if len(root.Content) == 0 {
			return nil, fmt.Errorf("suite document is empty")
		}
		root = root.Content[0]
	}
	if root.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("suite file is not a mapping")
	}
	if err := patchHeader(root, orig, d); err != nil {
		return nil, err
	}
	if err := patchDataset(root, orig, d); err != nil {
		return nil, err
	}
	if err := patchSteps(root, orig, d); err != nil {
		return nil, err
	}
	return encodeDoc(&doc)
}

func patchHeader(root *yaml.Node, orig *runner.Suite, d *Draft) error {
	id := strings.TrimSpace(d.ID)
	if id != orig.ID {
		setString(root, "id", id)
	}
	name := strings.TrimSpace(d.Name)
	if name != orig.Name {
		if name == "" {
			mappingRemove(root, "name")
		} else {
			setString(root, "name", name)
		}
	}
	origOnFailure := string(orig.OnFailure)
	if origOnFailure == "" {
		origOnFailure = string(runner.OnFailureStop)
	}
	newOnFailure := strings.TrimSpace(d.OnFailure)
	if newOnFailure == "" {
		newOnFailure = string(runner.OnFailureStop)
	}
	if newOnFailure != origOnFailure {
		setString(root, "onFailure", newOnFailure)
	}
	return nil
}

func patchDataset(root *yaml.Node, orig *runner.Suite, d *Draft) error {
	if !d.Dataset.Enabled {
		if orig.Dataset != nil {
			mappingRemove(root, "dataset")
		}
		return nil
	}
	node := mappingValue(root, "dataset")
	if node == nil || node.Kind != yaml.MappingNode {
		node = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		mappingSet(root, "dataset", node)
	}
	spec := d.Dataset.Spec()
	origSpec := orig.Dataset

	if origSpec == nil || spec.Format != origSpec.Format {
		setString(node, "format", spec.Format)
	}
	if origSpec == nil || spec.File != origSpec.File {
		setString(node, "file", spec.File)
	}
	if spec.MaxRows <= 0 {
		mappingRemove(node, "maxRows")
	} else if origSpec == nil || spec.MaxRows != origSpec.MaxRows {
		setInt(node, "maxRows", int64(spec.MaxRows))
	}
	if spec.MaxBytes <= 0 {
		mappingRemove(node, "maxBytes")
	} else if origSpec == nil || spec.MaxBytes != origSpec.MaxBytes {
		setInt(node, "maxBytes", spec.MaxBytes)
	}
	if spec.Format != "csv" || spec.Delimiter == "" {
		mappingRemove(node, "delimiter")
	} else if origSpec == nil || spec.Delimiter != origSpec.Delimiter {
		setString(node, "delimiter", spec.Delimiter)
	}
	// Fields and Types are preserved as-is for an existing dataset. Only a
	// newly created dataset node needs them written out.
	if origSpec == nil {
		if len(spec.Fields) > 0 {
			mappingSet(node, "fields", seqStrings(spec.Fields))
		}
		if len(spec.Types) > 0 {
			types := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			keys := make([]string, 0, len(spec.Types))
			for k := range spec.Types {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				types.Content = append(types.Content, keyNode(k), stringNode(spec.Types[k]))
			}
			mappingSet(node, "types", types)
		}
	}
	return nil
}

func patchSteps(root *yaml.Node, orig *runner.Suite, d *Draft) error {
	steps := mappingValue(root, "steps")
	if steps == nil || steps.Kind != yaml.SequenceNode {
		return fmt.Errorf("suite steps are missing or not a sequence; reload the file")
	}
	if len(steps.Content) != len(orig.Steps) {
		return fmt.Errorf("suite steps changed on disk; reload before saving")
	}
	byID := make(map[string]int, len(orig.Steps))
	for i := range orig.Steps {
		byID[orig.Steps[i].ID] = i
	}
	used := make(map[int]bool, len(orig.Steps))
	content := make([]*yaml.Node, 0, len(d.Steps))
	for _, st := range d.Steps {
		if st == nil {
			continue
		}
		if st.IsNew {
			node, err := newStepNode(st)
			if err != nil {
				return err
			}
			content = append(content, node)
			continue
		}
		id := strings.TrimSpace(st.ID)
		idx, ok := byID[id]
		if !ok {
			return fmt.Errorf("step %q is not in the saved suite; reload before saving", id)
		}
		if used[idx] {
			return fmt.Errorf("step %q appears twice in the draft", id)
		}
		used[idx] = true
		node := steps.Content[idx]
		if err := patchStepNode(node, st, &orig.Steps[idx]); err != nil {
			return err
		}
		content = append(content, node)
	}
	steps.Content = content
	return nil
}

func patchStepNode(node *yaml.Node, st *StepDraft, orig *runner.SuiteStep) error {
	if node == nil || node.Kind != yaml.MappingNode {
		return fmt.Errorf("step %q is not a mapping; reload the file", orig.ID)
	}
	if strings.TrimSpace(st.ID) != orig.ID {
		return fmt.Errorf("step id %q cannot be renamed in this phase", orig.ID)
	}
	name := strings.TrimSpace(st.Name)
	if name != orig.Name {
		if name == "" {
			mappingRemove(node, "name")
		} else {
			setString(node, "name", name)
		}
	}
	request := strings.TrimSpace(st.RequestRef)
	if request != orig.Request {
		setString(node, "request", request)
	}
	if !equalStrings(st.Needs, orig.Needs) {
		if len(st.Needs) == 0 {
			mappingRemove(node, "needs")
		} else {
			mappingSet(node, "needs", seqStrings(st.Needs))
		}
	}
	return nil
}

func newStepNode(st *StepDraft) (*yaml.Node, error) {
	m := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	mappingSet(m, "id", stringNode(strings.TrimSpace(st.ID)))
	if name := strings.TrimSpace(st.Name); name != "" {
		mappingSet(m, "name", stringNode(name))
	}
	mappingSet(m, "request", stringNode(strings.TrimSpace(st.RequestRef)))
	if len(st.Needs) > 0 {
		mappingSet(m, "needs", seqStrings(st.Needs))
	}
	if len(st.Assert) > 0 {
		seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
		for i := range st.Assert {
			seq.Content = append(seq.Content, cloneNode(&st.Assert[i]))
		}
		mappingSet(m, "assert", seq)
	}
	if len(st.Extract) > 0 {
		n, err := nodeFromValue(st.Extract)
		if err != nil {
			return nil, err
		}
		mappingSet(m, "extract", n)
	}
	if st.Retry != nil {
		n, err := nodeFromValue(st.Retry)
		if err != nil {
			return nil, err
		}
		mappingSet(m, "retry", n)
	}
	if len(st.Before) > 0 {
		n, err := nodeFromValue(st.Before)
		if err != nil {
			return nil, err
		}
		mappingSet(m, "before", n)
	}
	if len(st.After) > 0 {
		n, err := nodeFromValue(st.After)
		if err != nil {
			return nil, err
		}
		mappingSet(m, "after", n)
	}
	return m, nil
}

// --- yaml.Node helpers -----------------------------------------------------

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func mappingSet(m *yaml.Node, key string, value *yaml.Node) {
	if m == nil || m.Kind != yaml.MappingNode || value == nil {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = value
			return
		}
	}
	m.Content = append(m.Content, keyNode(key), value)
}

func mappingRemove(m *yaml.Node, key string) {
	if m == nil || m.Kind != yaml.MappingNode {
		return
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return
		}
	}
}

func setString(m *yaml.Node, key, value string) {
	if v := mappingValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		v.Value = value
		v.Tag = "!!str"
		return
	}
	mappingSet(m, key, stringNode(value))
}

func setInt(m *yaml.Node, key string, value int64) {
	if v := mappingValue(m, key); v != nil && v.Kind == yaml.ScalarNode {
		v.Value = strconv.FormatInt(value, 10)
		v.Tag = "!!int"
		return
	}
	mappingSet(m, key, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: strconv.FormatInt(value, 10)})
}

func keyNode(key string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}
}

func stringNode(value string) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
}

func seqStrings(values []string) *yaml.Node {
	seq := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for _, v := range values {
		seq.Content = append(seq.Content, stringNode(v))
	}
	return seq
}

func nodeFromValue(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var n yaml.Node
	if err := yaml.Unmarshal(b, &n); err != nil {
		return nil, err
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		return n.Content[0], nil
	}
	return &n, nil
}

func encodeDoc(doc *yaml.Node) ([]byte, error) {
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

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
