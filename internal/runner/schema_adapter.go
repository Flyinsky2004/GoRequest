package runner

// This adapter deliberately supports an audited subset of A03's schema dialect.
// It resolves and freezes every schema branch before handing instances to A03.
import (
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
	"gorequest/internal/core"
)

var errSchemaConfig = errors.New("invalid or unsupported offline schema configuration")

type adapterSchema struct{ node any }
type adapterTarget struct {
	schema    *adapterSchema
	responses map[string]*adapterSchema
}

// ProjectSchemaAdapter is scoped to one saved project and one run. Ref is a
// Store-relative schema file plus optional RFC6901 fragment. Operation names a
// unique saved extensions.openapi.operationId; only application/json bodies
// without response-header contracts are currently supported. All other media
// contracts are rejected during preflight, rather than silently ignored.
// Prepared targets contain no live file references; concurrent validation is safe.
type ProjectSchemaAdapter struct {
	project *core.Project
	mu      sync.Mutex
	targets map[string]*adapterTarget
}

func NewProjectSchemaAdapter(p *core.Project) *ProjectSchemaAdapter {
	return &ProjectSchemaAdapter{project: p, targets: map[string]*adapterTarget{}}
}
func schemaKey(r SchemaRef) string { return r.Ref + "\x00" + r.Operation }
func (a *ProjectSchemaAdapter) PreflightSchema(r SchemaRef) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.targets[schemaKey(r)] != nil {
		return nil
	}
	if a.project == nil || a.project.Store == nil {
		return errSchemaConfig
	}
	loader := &schemaLoader{p: a.project, docs: map[string]any{}, active: map[string]bool{}}
	t := &adapterTarget{}
	if r.Operation == "" {
		if r.Ref == "" {
			return errSchemaConfig
		}
		n, file, err := loader.reference(r.Ref, "", nil, 0)
		if err != nil {
			return errSchemaConfig
		}
		n, err = loader.freeze(n, file, loader.docs[file], 0)
		if err != nil {
			return errSchemaConfig
		}
		t.schema = &adapterSchema{n}
	} else {
		if r.Ref != "" {
			return errSchemaConfig
		}
		var op *core.OperationContract
		for _, e := range a.project.Requests {
			c, err := core.GetRequestOpenAPI(e.Request)
			if err != nil {
				return errSchemaConfig
			}
			if c != nil && c.OperationID == r.Operation {
				if op != nil {
					return errSchemaConfig
				}
				op = c
			}
		}
		if op == nil || len(op.Responses) == 0 {
			return errSchemaConfig
		}
		t.responses = map[string]*adapterSchema{}
		for status, response := range op.Responses {
			if status != "default" && !regexp.MustCompile(`^[1-5]([0-9]{2}|XX)$`).MatchString(status) {
				return errSchemaConfig
			}
			if len(response.Headers) != 0 || len(response.Content) != 1 {
				return errSchemaConfig
			}
			mt, ok := response.Content["application/json"]
			if !ok {
				return errSchemaConfig
			}
			var n any = mt.Schema
			if mt.SchemaRef != "" {
				if len(mt.Schema) != 0 {
					return errSchemaConfig
				}
				n = map[string]any{"$ref": mt.SchemaRef}
			}
			if n == nil || (mt.SchemaRef == "" && len(mt.Schema) == 0) {
				return errSchemaConfig
			}
			n, err := loader.freeze(n, "", nil, 0)
			if err != nil {
				return errSchemaConfig
			}
			t.responses[status] = &adapterSchema{n}
		}
	}
	a.targets[schemaKey(r)] = t
	return nil
}
func (a *ProjectSchemaAdapter) ValidateSchema(body []byte, r SchemaRef) ([]SchemaIssue, error) {
	if err := a.PreflightSchema(r); err != nil {
		return nil, err
	}
	a.mu.Lock()
	t := a.targets[schemaKey(r)]
	a.mu.Unlock()
	s := t.schema
	if s == nil {
		media := "application/json"
		for _, h := range r.Headers {
			if strings.EqualFold(h[0], "Content-Type") {
				media = strings.TrimSpace(strings.Split(h[1], ";")[0])
				break
			}
		}
		if media != "application/json" {
			return []SchemaIssue{{InstancePath: "$", Message: "response media type does not match application/json contract"}}, nil
		}
		s = t.responses[strconv.Itoa(r.Status)]
		if s == nil {
			s = t.responses[fmt.Sprintf("%dXX", r.Status/100)]
		}
		if s == nil {
			s = t.responses["default"]
		}
		if s == nil {
			return []SchemaIssue{{InstancePath: "$.status", Message: "response status has no matching schema contract"}}, nil
		}
	}
	if !json.Valid(body) {
		return []SchemaIssue{{InstancePath: "$", Message: "response body is not valid JSON"}}, nil
	}
	instance := any(nil)
	dec := json.NewDecoder(strings.NewReader(string(body)))
	dec.UseNumber()
	if dec.Decode(&instance) != nil {
		return nil, errSchemaConfig
	}
	result, err := core.ValidateParsedSchema(s.node, instance, core.SchemaValidationOptions{})
	if err != nil {
		return nil, errSchemaConfig
	}
	issues := make([]SchemaIssue, 0, len(result.Violations))
	for _, v := range result.Violations {
		// Never expose schema constants, property values, or engine messages: they
		// can contain secrets unknown to the run's taint registry.
		issues = append(issues, SchemaIssue{InstancePath: v.InstancePath, Message: "schema constraint failed (" + v.Keyword + ")"})
	}
	if !result.Valid && len(issues) == 0 {
		return []SchemaIssue{{InstancePath: "$", Message: "schema validation failed"}}, nil
	}
	return issues, nil
}

type schemaLoader struct {
	p      *core.Project
	docs   map[string]any
	active map[string]bool
	bytes  int64
	nodes  int
}

func (l *schemaLoader) reference(ref, file string, root any, depth int) (any, string, error) {
	if depth > 48 || strings.ContainsAny(ref, "\\:?%") {
		return nil, "", errSchemaConfig
	}
	name, fragment, _ := strings.Cut(ref, "#")
	if name != "" {
		if strings.HasPrefix(name, "/") {
			return nil, "", errSchemaConfig
		}
		file = path.Join(path.Dir(file), name)
		if file == ".." || strings.HasPrefix(file, "../") {
			return nil, "", errSchemaConfig
		}
		doc, ok := l.docs[file]
		if !ok {
			if len(l.docs) >= 128 {
				return nil, "", errSchemaConfig
			}
			b, _, err := l.p.Store.ReadBounded(file, core.DefaultMaxSchemaBytes-l.bytes)
			if err != nil {
				return nil, "", errSchemaConfig
			}
			l.bytes += int64(len(b))
			if l.bytes > core.DefaultMaxSchemaBytes {
				return nil, "", errSchemaConfig
			}
			if yaml.Unmarshal(b, &doc) != nil {
				return nil, "", errSchemaConfig
			}
			l.docs[file] = doc
		}
		root = doc
	}
	if root == nil {
		return nil, "", errSchemaConfig
	}
	n := root
	if fragment != "" {
		if !strings.HasPrefix(fragment, "/") {
			return nil, "", errSchemaConfig
		}
		for _, key := range strings.Split(fragment[1:], "/") {
			key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
			switch v := n.(type) {
			case map[string]any:
				var ok bool
				n, ok = v[key]
				if !ok {
					return nil, "", errSchemaConfig
				}
			case []any:
				i, e := strconv.Atoi(key)
				if e != nil || i < 0 || i >= len(v) {
					return nil, "", errSchemaConfig
				}
				n = v[i]
			default:
				return nil, "", errSchemaConfig
			}
		}
	}
	return n, file, nil
}
func (l *schemaLoader) freeze(n any, file string, root any, depth int) (any, error) {
	l.nodes++
	if depth > 48 || l.nodes > 10000 {
		return nil, errSchemaConfig
	}
	if _, ok := n.(bool); ok {
		return n, nil
	}
	m, ok := n.(map[string]any)
	if !ok || m == nil {
		return nil, errSchemaConfig
	}
	out := map[string]any{}
	for k, v := range m {
		switch k {
		case "$ref":
			ref, ok := v.(string)
			if !ok || ref == "" {
				return nil, errSchemaConfig
			}
			key := file + "#" + ref
			if l.active[key] {
				return nil, errSchemaConfig
			}
			l.active[key] = true
			target, f, err := l.reference(ref, file, root, depth+1)
			if err != nil {
				return nil, err
			}
			newRoot := root
			if f != file {
				newRoot = l.docs[f]
			}
			frozen, err := l.freeze(target, f, newRoot, depth+1)
			delete(l.active, key)
			if err != nil {
				return nil, err
			}
			out["allOf"] = []any{frozen}
		case "properties", "$defs", "definitions":
			values, ok := v.(map[string]any)
			if !ok {
				return nil, errSchemaConfig
			}
			dst := map[string]any{}
			for name, child := range values {
				f, err := l.freeze(child, file, root, depth+1)
				if err != nil {
					return nil, err
				}
				dst[name] = f
			}
			out[k] = dst
		case "items", "additionalProperties", "not":
			f, err := l.freeze(v, file, root, depth+1)
			if err != nil {
				return nil, err
			}
			out[k] = f
		case "allOf", "anyOf", "oneOf", "prefixItems":
			arr, ok := v.([]any)
			if !ok || len(arr) == 0 {
				return nil, errSchemaConfig
			}
			dst := []any{}
			for _, child := range arr {
				f, err := l.freeze(child, file, root, depth+1)
				if err != nil {
					return nil, err
				}
				dst = append(dst, f)
			}
			if k == "allOf" {
				if prev, ok := out[k].([]any); ok {
					dst = append(prev, dst...)
				}
			}
			out[k] = dst
		case "type":
			valid := func(x any) bool {
				s, ok := x.(string)
				return ok && strings.Contains("|null|boolean|object|array|number|integer|string|", "|"+s+"|")
			}
			if arr, ok := v.([]any); ok {
				if len(arr) == 0 {
					return nil, errSchemaConfig
				}
				for _, x := range arr {
					if !valid(x) {
						return nil, errSchemaConfig
					}
				}
			} else if !valid(v) {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "required":
			arr, ok := v.([]any)
			if !ok {
				return nil, errSchemaConfig
			}
			for _, x := range arr {
				if _, ok := x.(string); !ok {
					return nil, errSchemaConfig
				}
			}
			out[k] = v
		case "pattern":
			s, ok := v.(string)
			if !ok {
				return nil, errSchemaConfig
			}
			if _, err := regexp.Compile(s); err != nil {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "minLength", "maxLength", "minItems", "maxItems", "minProperties", "maxProperties":
			f, ok := schemaNumber(v)
			if !ok || f < 0 || f != float64(int64(f)) {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum", "multipleOf":
			f, ok := schemaNumber(v)
			if !ok || (k == "multipleOf" && f <= 0) {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "enum":
			arr, ok := v.([]any)
			if !ok || len(arr) == 0 {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "uniqueItems":
			if _, ok := v.(bool); !ok {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "const", "title", "description", "examples", "default", "$comment":
			out[k] = v
		case "$schema":
			s, ok := v.(string)
			if !ok || s != "https://json-schema.org/draft/2020-12/schema" {
				return nil, errSchemaConfig
			}
		default:
			return nil, errSchemaConfig
		}
	}
	// Preserve both $ref and explicit allOf regardless of Go map iteration order.
	if _, has := m["$ref"]; has {
		if _, also := m["allOf"]; also {
			return nil, errSchemaConfig
		}
	}
	return out, nil
}
func schemaNumber(v any) (float64, bool) {
	switch n := v.(type) {
	case int:
		return float64(n), true
	case float64:
		return n, true
	}
	return 0, false
}
