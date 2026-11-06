package core

import (
	"bytes"
	"fmt"
	"io"
	"strconv"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Default YAML AST safety limits (storage-format.md §3, Q-02).
const (
	DefaultMaxYAMLDepth         = 64
	DefaultMaxYAMLNodes         = 50000
	DefaultMaxYAMLAliases       = 64
	DefaultMaxYAMLExpandedNodes = 100000
)

// YAMLLimits bounds YAML document size, recursion depth, node count, and alias
// expansion cost so untrusted project files cannot trigger stack overflows or
// quadratic/exponential alias bombs.
type YAMLLimits struct {
	MaxBytes          int64
	MaxDepth          int
	MaxNodes          int
	MaxAliases        int
	MaxExpandedNodes  int
	AllowMergeKeys    bool // false by default: implicit cross/in-file << merges are rejected
}

// Effective returns a copy of l with zero fields replaced by safe defaults.
func (l YAMLLimits) Effective() YAMLLimits {
	out := l
	if out.MaxBytes <= 0 {
		out.MaxBytes = DefaultMaxYAMLBytes
	}
	if out.MaxDepth <= 0 {
		out.MaxDepth = DefaultMaxYAMLDepth
	}
	if out.MaxNodes <= 0 {
		out.MaxNodes = DefaultMaxYAMLNodes
	}
	if out.MaxAliases <= 0 {
		out.MaxAliases = DefaultMaxYAMLAliases
	}
	if out.MaxExpandedNodes <= 0 {
		out.MaxExpandedNodes = DefaultMaxYAMLExpandedNodes
	}
	return out
}

func init() {
	decodeYAMLStrictCheckHook = func(rel string, kind SyncEntityKind, data []byte) error {
		_, err := ValidateEntityYAML(kind, data, YAMLLimits{})
		return err
	}
	decodeStrictExtensionStripper = func(data []byte, out any) error {
		doc, err := ParseStrictYAMLTree(data, YAMLLimits{})
		if err != nil {
			return err
		}
		stripped := stripTopLevelExtensions(doc)
		var buf bytes.Buffer
		enc := yaml.NewEncoder(&buf)
		enc.SetIndent(2)
		if err := enc.Encode(stripped); err != nil {
			return err
		}
		if err := enc.Close(); err != nil {
			return err
		}
		return decodeStrict(buf.Bytes(), out)
	}
}

// ParseStrictYAMLTree parses a single UTF-8 YAML document into a *yaml.Node
// while enforcing byte size, Git conflict marker absence, nesting depth, AST
// node count, alias/anchor expansion limits, implicit merge-key rejection, and
// duplicate-key rejection at every mapping level (including custom decoders).
func ParseStrictYAMLTree(data []byte, limits YAMLLimits) (*yaml.Node, error) {
	lim := limits.Effective()
	if int64(len(data)) > lim.MaxBytes {
		return nil, configErrorf("YAML exceeds size limit (%d > %d bytes)", len(data), lim.MaxBytes)
	}
	if !utf8.Valid(data) {
		return nil, configErrorf("YAML must be valid UTF-8")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return nil, configErrorf("YAML contains NUL byte")
	}
	if line, has := DetectGitConflictMarkers(data); has {
		return nil, configErrorf("unresolved Git merge conflict markers at line %d", line)
	}

	dec := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := dec.Decode(&doc); err != nil {
		if err == io.EOF {
			return nil, configErrorf("empty YAML document")
		}
		return nil, &ConfigError{Err: err}
	}
	var extra yaml.Node
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return nil, configErrorf("expected a single YAML document, found multiple")
		}
		return nil, &ConfigError{Err: err}
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, configErrorf("invalid YAML document root")
	}

	auditor := &yamlASTAuditor{
		lim:          lim,
		expandedMemo: map[*yaml.Node]int{},
		visiting:     map[*yaml.Node]bool{},
	}
	if err := auditor.walk(doc.Content[0], 1); err != nil {
		return nil, err
	}
	return &doc, nil
}

type yamlASTAuditor struct {
	lim           YAMLLimits
	nodes         int
	aliases       int
	expandedTotal int
	expandedMemo  map[*yaml.Node]int
	visiting      map[*yaml.Node]bool
}

func (a *yamlASTAuditor) walk(n *yaml.Node, depth int) error {
	if n == nil {
		return nil
	}
	if depth > a.lim.MaxDepth {
		return configErrorf("YAML nesting depth exceeds limit (%d)", a.lim.MaxDepth)
	}
	a.nodes++
	if a.nodes > a.lim.MaxNodes {
		return configErrorf("YAML node count exceeds limit (%d)", a.lim.MaxNodes)
	}

	if n.Kind == yaml.AliasNode {
		a.aliases++
		if a.aliases > a.lim.MaxAliases {
			return configErrorf("YAML alias count exceeds limit (%d)", a.lim.MaxAliases)
		}
		cost, err := a.expandedCost(n.Alias, depth+1)
		if err != nil {
			return err
		}
		a.expandedTotal += cost
		if a.expandedTotal > a.lim.MaxExpandedNodes {
			return configErrorf("YAML alias expansion exceeds limit (%d)", a.lim.MaxExpandedNodes)
		}
		return nil
	}

	if n.Kind == yaml.MappingNode {
		seen := make(map[string]int, len(n.Content)/2)
		for i := 0; i+1 < len(n.Content); i += 2 {
			kNode := n.Content[i]
			vNode := n.Content[i+1]
			if !a.lim.AllowMergeKeys && (kNode.Tag == "!!merge" || (kNode.Kind == yaml.ScalarNode && kNode.Value == "<<")) {
				return configErrorf("YAML merge key '<<' is not supported (line %d)", kNode.Line)
			}
			if kNode.Kind != yaml.ScalarNode {
				return configErrorf("YAML mapping key must be a scalar (line %d)", kNode.Line)
			}
			if prevLine, dup := seen[kNode.Value]; dup {
				return configErrorf("duplicate YAML key %q at line %d (previous at line %d)", kNode.Value, kNode.Line, prevLine)
			}
			seen[kNode.Value] = kNode.Line
			if err := a.walk(kNode, depth+1); err != nil {
				return err
			}
			if err := a.walk(vNode, depth+1); err != nil {
				return err
			}
		}
		return nil
	}

	for _, child := range n.Content {
		if err := a.walk(child, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func (a *yamlASTAuditor) expandedCost(target *yaml.Node, depth int) (int, error) {
	if target == nil {
		return 1, nil
	}
	if depth > a.lim.MaxDepth {
		return 0, configErrorf("YAML alias depth exceeds limit (%d)", a.lim.MaxDepth)
	}
	if cached, ok := a.expandedMemo[target]; ok {
		return cached, nil
	}
	if a.visiting[target] {
		return 0, configErrorf("cyclic YAML alias reference at line %d", target.Line)
	}
	a.visiting[target] = true
	defer delete(a.visiting, target)

	cost := 1
	if target.Kind == yaml.AliasNode {
		c, err := a.expandedCost(target.Alias, depth+1)
		if err != nil {
			return 0, err
		}
		cost += c
	} else {
		for _, child := range target.Content {
			if child.Kind == yaml.AliasNode {
				c, err := a.expandedCost(child.Alias, depth+1)
				if err != nil {
					return 0, err
				}
				cost += c
			} else {
				c, err := a.expandedCost(child, depth+1)
				if err != nil {
					return 0, err
				}
				cost += c
			}
			if cost > a.lim.MaxExpandedNodes {
				return 0, configErrorf("YAML alias expansion exceeds limit (%d)", a.lim.MaxExpandedNodes)
			}
		}
	}
	a.expandedMemo[target] = cost
	return cost, nil
}

// ValidateEntityYAML parses data strictly and audits entity-specific custom
// decoder branches (schemaVersion ceiling, AuthRef ref/inline exclusivity and
// known fields, HTTP Body mutual exclusion, and extensions mapping shape).
func ValidateEntityYAML(kind SyncEntityKind, data []byte, limits YAMLLimits) (*yaml.Node, error) {
	doc, err := ParseStrictYAMLTree(data, limits)
	if err != nil {
		return nil, err
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, configErrorf("%s root must be a YAML mapping", kind)
	}

	// 1. Validate schemaVersion before struct decode so higher versions are
	// explicitly reported rather than silently downgraded or mis-parsed.
	if svNode := mappingValue(root, "schemaVersion"); svNode != nil {
		if svNode.Kind != yaml.ScalarNode {
			return nil, configErrorf("schemaVersion must be an integer scalar")
		}
		sv, perr := strconv.Atoi(svNode.Value)
		if perr != nil {
			return nil, configErrorf("invalid schemaVersion %q", svNode.Value)
		}
		if sv > SupportedSchemaVersion {
			return nil, configErrorf("schemaVersion %d unsupported (this build supports %d); refusing execution or downgrade", sv, SupportedSchemaVersion)
		}
		if sv <= 0 {
			return nil, configErrorf("schemaVersion %d must be >= 1", sv)
		}
	} else {
		return nil, configErrorf("missing required schemaVersion")
	}

	// 2. Validate optional top-level extensions section.
	if extNode := mappingValue(root, "extensions"); extNode != nil {
		if extNode.Kind != yaml.MappingNode {
			return nil, configErrorf("extensions must be a YAML mapping (line %d)", extNode.Line)
		}
	}

	// 3. Entity-specific custom decoder audits + strict known-field check.
	switch kind {
	case EntityRequest:
		if err := auditRequestAST(root); err != nil {
			return nil, err
		}
		var r Request
		if err := decodeStrictExtensionStripper(data, &r); err != nil {
			return nil, &ConfigError{Err: err}
		}
	case EntityProject:
		var pf ProjectFile
		if err := decodeStrictExtensionStripper(data, &pf); err != nil {
			return nil, &ConfigError{Err: err}
		}
	case EntityEnvironment:
		var env Environment
		if err := decodeStrictExtensionStripper(data, &env); err != nil {
			return nil, &ConfigError{Err: err}
		}
	case EntityCollection:
		var col Collection
		if err := decodeStrictExtensionStripper(data, &col); err != nil {
			return nil, &ConfigError{Err: err}
		}
	case EntityAuth:
		if err := auditAuthSchemeMapping(root, true); err != nil {
			return nil, err
		}
		var s AuthScheme
		if err := decodeStrictExtensionStripper(data, &s); err != nil {
			return nil, &ConfigError{Err: err}
		}
	}

	return doc, nil
}

var allowedInlineAuthKeys = map[string]bool{
	"schemaVersion": true,
	"id":            true,
	"type":          true,
	"token":         true,
	"username":      true,
	"password":      true,
	"name":          true,
	"in":            true,
	"value":         true,
}

func auditRequestAST(root *yaml.Node) error {
	// Audit AuthRef custom UnmarshalYAML branch (model.go's n.Decode does not
	// set KnownFields(true) and previously ignored extra/mixed keys).
	if authNode := mappingValue(root, "auth"); authNode != nil {
		switch authNode.Kind {
		case yaml.ScalarNode:
			if authNode.Value != "" && authNode.Value != "none" {
				return configErrorf("auth scalar must be \"none\", got %q (line %d)", authNode.Value, authNode.Line)
			}
		case yaml.MappingNode:
			refNode := mappingValue(authNode, "ref")
			if refNode != nil {
				if refNode.Kind != yaml.ScalarNode || refNode.Value == "" {
					return configErrorf("auth.ref must be a non-empty string scalar (line %d)", refNode.Line)
				}
				if len(authNode.Content) > 2 {
					for i := 0; i+1 < len(authNode.Content); i += 2 {
						k := authNode.Content[i]
						if k.Value != "ref" {
							return configErrorf("auth with ref must not also define %q (line %d)", k.Value, k.Line)
						}
					}
				}
			} else {
				if err := auditAuthSchemeMapping(authNode, false); err != nil {
					return err
				}
			}
		default:
			return configErrorf("auth must be \"none\" or a mapping (line %d)", authNode.Line)
		}
	}

	// Audit HTTPDef.Body custom UnmarshalYAML branch.
	if httpNode := mappingValue(root, "http"); httpNode != nil && httpNode.Kind == yaml.MappingNode {
		if bodyNode := mappingValue(httpNode, "body"); bodyNode != nil {
			if bodyNode.Kind != yaml.MappingNode {
				return configErrorf("http.body must be a mapping (line %d)", bodyNode.Line)
			}
			for i := 0; i+1 < len(bodyNode.Content); i += 2 {
				k := bodyNode.Content[i]
				v := bodyNode.Content[i+1]
				switch k.Value {
				case "type":
					if v.Kind != yaml.ScalarNode {
						return configErrorf("http.body.type must be a string scalar (line %d)", v.Line)
					}
				case "file":
					if v.Kind != yaml.ScalarNode {
						return configErrorf("http.body.file must be a string scalar (line %d)", v.Line)
					}
				case "value":
				default:
					return configErrorf("unknown body field %q (line %d)", k.Value, k.Line)
				}
			}
			fileNode := mappingValue(bodyNode, "file")
			valNode := mappingValue(bodyNode, "value")
			if fileNode != nil && fileNode.Value != "" && valNode != nil && !(valNode.Kind == yaml.ScalarNode && valNode.Tag == "!!null") {
				return configErrorf("http.body cannot specify both file and inline value (line %d)", bodyNode.Line)
			}
		}
	}
	return nil
}

func auditAuthSchemeMapping(n *yaml.Node, allowTopExtensions bool) error {
	if n.Kind != yaml.MappingNode {
		return configErrorf("auth scheme must be a mapping")
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		k := n.Content[i]
		v := n.Content[i+1]
		if allowTopExtensions && k.Value == "extensions" {
			if v.Kind != yaml.MappingNode {
				return configErrorf("extensions must be a mapping (line %d)", v.Line)
			}
			continue
		}
		if !allowedInlineAuthKeys[k.Value] {
			return configErrorf("unknown auth field %q (line %d)", k.Value, k.Line)
		}
		if v.Kind != yaml.ScalarNode {
			return configErrorf("auth field %q must be a scalar (line %d)", k.Value, v.Line)
		}
	}
	return nil
}

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

func stripTopLevelExtensions(doc *yaml.Node) *yaml.Node {
	if doc == nil || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return doc
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode || mappingValue(root, "extensions") == nil {
		return doc
	}
	cpRoot := *root
	cpRoot.Content = make([]*yaml.Node, 0, len(root.Content))
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "extensions" {
			continue
		}
		cpRoot.Content = append(cpRoot.Content, root.Content[i], root.Content[i+1])
	}
	cpDoc := *doc
	cpDoc.Content = []*yaml.Node{&cpRoot}
	return &cpDoc
}

// AuditProjectStrict runs bounded size, symlink, Git conflict marker, and
// strict YAML AST checks across every tracked file under p.Store, returning
// structured Issues without modifying disk or touching network.
func (p *Project) AuditProjectStrict(limits YAMLLimits) []Issue {
	if p == nil || p.Store == nil {
		return []Issue{{Level: "error", Code: "project-nil", Message: "project or store is nil"}}
	}
	storeLim := p.Store.EffectiveLimits()
	if limits.MaxBytes > 0 {
		storeLim.MaxYAMLBytes = limits.MaxBytes
	}
	snap, err := ScanDiskSnapshot(p.Store, storeLim)
	if err != nil {
		return []Issue{{Level: "error", Code: "scan-error", Message: err.Error()}}
	}
	var issues []Issue
	issues = append(issues, snap.Issues...)
	paths := make([]string, 0, len(snap.Files))
	for rel := range snap.Files {
		paths = append(paths, rel)
	}
	sortStrings(paths)
	for _, rel := range paths {
		rec := snap.Files[rel]
		if rec.Kind == EntityDoc {
			if rec.HasGitConflict {
				issues = append(issues, Issue{Level: "warning", Code: "doc-git-conflict", Path: rel, Message: "document contains Git merge conflict markers"})
			}
			continue
		}
		if _, err := ValidateEntityYAML(rec.Kind, rec.Bytes, limits); err != nil {
			code := string(rec.Kind) + "-strict"
			if rec.HasGitConflict {
				code = "git-conflict"
			}
			issues = append(issues, Issue{Level: "error", Code: code, Path: rel, Message: err.Error()})
		}
	}
	return issues
}

// OpenProjectStrict opens a project after running full strict storage & YAML
// AST validation on project.yaml and appending any strictness findings on child
// files into Project.Issues.
func OpenProjectStrict(dir string, storeLimits StoreLimits, yamlLimits YAMLLimits) (*Project, error) {
	gq, err := LocateProjectDir(dir)
	if err != nil {
		return nil, &ConfigError{Err: err}
	}
	s := NewStore(gq)
	s.Limits = storeLimits.Effective()
	projBytes, _, err := s.ReadBounded("project.yaml", s.Limits.MaxYAMLBytes)
	if err != nil {
		return nil, fmt.Errorf("reading project.yaml: %w", err)
	}
	if _, err := ValidateEntityYAML(EntityProject, projBytes, yamlLimits); err != nil {
		return nil, &ConfigError{Err: fmt.Errorf("project.yaml: %w", err)}
	}
	p, err := OpenProject(dir)
	if err != nil {
		return nil, err
	}
	p.Store.Limits = s.Limits
	strictIssues := p.AuditProjectStrict(yamlLimits)
	seen := map[string]bool{}
	for _, iss := range p.Issues {
		seen[iss.Path+"|"+iss.Message] = true
	}
	for _, iss := range strictIssues {
		key := iss.Path + "|" + iss.Message
		if !seen[key] {
			seen[key] = true
			p.Issues = append(p.Issues, iss)
		}
	}
	return p, nil
}
