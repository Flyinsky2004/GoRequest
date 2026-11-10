package runner

// This adapter deliberately supports an audited subset of A03's schema dialect.
// It resolves and freezes every schema branch before handing instances to A03.
import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
	"gorequest/internal/core"
)

var errSchemaConfig = errors.New("invalid or unsupported offline schema configuration")

type adapterSchema struct {
	node    any
	encoded []byte
}

type mediaKind int

const (
	mediaJSON mediaKind = iota
	mediaText
	mediaBinary
)

type adapterMedia struct {
	kind   mediaKind
	schema *adapterSchema
}

type adapterHeader struct {
	name     string
	required bool
	typ      string // string | integer | number | boolean
	schema   *adapterSchema
}

type adapterResponse struct {
	media   map[string]*adapterMedia
	headers []*adapterHeader
}

type adapterTarget struct {
	schema    *adapterSchema
	responses map[string]*adapterResponse
}

// ProjectSchemaAdapter is scoped to one saved project and one run. Ref is a
// Store-relative schema file plus optional RFC6901 fragment. Operation names a
// unique saved extensions.openapi.operationId.
//
// Supported response contracts: application/json and *+json bodies (JSON
// Schema), text/* bodies (UTF-8 boundary plus an optional string schema) and
// application/octet-stream bodies (media boundary only), plus scalar response
// header contracts. Every other media type or header/body schema shape is
// rejected during preflight rather than silently ignored. Prepared targets
// contain no live file references; concurrent validation is safe.
type ProjectSchemaAdapter struct {
	project        *core.Project
	mu             sync.Mutex
	targets        map[string]*adapterTarget
	loader         *schemaLoader
	expandedBudget int64
}

func NewProjectSchemaAdapter(p *core.Project) *ProjectSchemaAdapter {
	return &ProjectSchemaAdapter{project: p, targets: map[string]*adapterTarget{}, expandedBudget: core.DefaultMaxSchemaBytes}
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
	if a.loader == nil {
		a.loader = &schemaLoader{p: a.project, docs: map[string]any{}, active: map[string]bool{}}
	}
	loader := a.loader
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
		t.schema = &adapterSchema{node: n}
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
		t.responses = map[string]*adapterResponse{}
		for status, response := range op.Responses {
			if status != "default" && !regexp.MustCompile(`^[1-5]([0-9]{2}|XX)$`).MatchString(status) {
				return errSchemaConfig
			}
			resp, err := a.prepareResponse(response, loader)
			if err != nil {
				return errSchemaConfig
			}
			t.responses[status] = resp
		}
	}
	// Ensure annotation/const/enum values are JSON-compatible too (YAML permits
	// non-string keys, NaN and infinities which are not JSON Schema values).
	if t.schema != nil {
		if err := boundSchemaJSON(t.schema.node, &a.expandedBudget, 0); err != nil {
			return err
		}
		encoded, err := json.Marshal(t.schema.node)
		if err != nil {
			return errSchemaConfig
		}
		t.schema.encoded = encoded
	} else {
		for _, resp := range t.responses {
			for _, media := range resp.media {
				if media.schema == nil {
					continue
				}
				if err := boundSchemaJSON(media.schema.node, &a.expandedBudget, 0); err != nil {
					return err
				}
				encoded, err := json.Marshal(media.schema.node)
				if err != nil {
					return errSchemaConfig
				}
				media.schema.encoded = encoded
			}
			for _, h := range resp.headers {
				if h.schema == nil {
					continue
				}
				if err := boundSchemaJSON(h.schema.node, &a.expandedBudget, 0); err != nil {
					return err
				}
				encoded, err := json.Marshal(h.schema.node)
				if err != nil {
					return errSchemaConfig
				}
				h.schema.encoded = encoded
			}
		}
	}
	a.targets[schemaKey(r)] = t
	return nil
}

func (a *ProjectSchemaAdapter) prepareResponse(response core.ResponseContract, loader *schemaLoader) (*adapterResponse, error) {
	resp := &adapterResponse{media: map[string]*adapterMedia{}}
	for name, hc := range response.Headers {
		h, err := prepareHeaderContract(name, hc, loader)
		if err != nil {
			return nil, err
		}
		resp.headers = append(resp.headers, h)
	}
	// Header order is not semantically significant, but deterministic reports
	// are. Sort by lower-case name after collection.
	sort.Slice(resp.headers, func(i, j int) bool { return resp.headers[i].name < resp.headers[j].name })
	for mediaType, mt := range response.Content {
		key, kind, err := classifyMediaType(mediaType)
		if err != nil {
			return nil, err
		}
		if _, dup := resp.media[key]; dup {
			return nil, errSchemaConfig
		}
		media := &adapterMedia{kind: kind}
		if mt.SchemaRef != "" && len(mt.Schema) != 0 {
			return nil, errSchemaConfig
		}
		var n any
		if mt.SchemaRef != "" {
			n = map[string]any{"$ref": mt.SchemaRef}
		} else if len(mt.Schema) != 0 {
			n = mt.Schema
		}
		switch kind {
		case mediaJSON:
			if n == nil {
				return nil, errSchemaConfig
			}
		case mediaText:
			if n == nil {
				break
			}
		case mediaBinary:
			// Binary content has no JSON Schema semantics. Pretending a schema
			// validates bytes would be false precision.
			if n != nil {
				return nil, errSchemaConfig
			}
			resp.media[key] = media
			continue
		}
		if n != nil {
			frozen, err := loader.freeze(n, "", nil, 0)
			if err != nil {
				return nil, errSchemaConfig
			}
			media.schema = &adapterSchema{node: frozen}
			if kind == mediaText {
				typ, ok := schemaScalarType(frozen)
				if !ok || typ != "string" {
					return nil, errSchemaConfig
				}
			}
		}
		resp.media[key] = media
	}
	return resp, nil
}

func prepareHeaderContract(name string, hc core.HeaderContract, loader *schemaLoader) (*adapterHeader, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errSchemaConfig
	}
	h := &adapterHeader{name: name, required: hc.Required}
	if hc.SchemaRef != "" && len(hc.Schema) != 0 {
		return nil, errSchemaConfig
	}
	var n any
	if hc.SchemaRef != "" {
		n = map[string]any{"$ref": hc.SchemaRef}
	} else if len(hc.Schema) != 0 {
		n = hc.Schema
	}
	if n == nil {
		return h, nil // presence-only contract
	}
	frozen, err := loader.freeze(n, "", nil, 0)
	if err != nil {
		return nil, errSchemaConfig
	}
	typ, ok := schemaScalarType(frozen)
	if !ok {
		return nil, errSchemaConfig
	}
	h.typ = typ
	h.schema = &adapterSchema{node: frozen}
	return h, nil
}

// schemaScalarType returns the declared JSON type of a header/text schema. A
// single allOf wrapper produced by $ref freezing is followed; unions,
// arrays/objects and untyped schemas are rejected because header values are
// strings with an unambiguous scalar interpretation.
func schemaScalarType(n any) (string, bool) {
	m, ok := n.(map[string]any)
	if !ok {
		return "", false
	}
	if t, ok := m["type"].(string); ok {
		switch t {
		case "string", "integer", "number", "boolean":
			return t, true
		default:
			return "", false
		}
	}
	if all, ok := m["allOf"].([]any); ok && len(all) == 1 {
		return schemaScalarType(all[0])
	}
	return "", false
}

func classifyMediaType(raw string) (string, mediaKind, error) {
	base := strings.ToLower(strings.TrimSpace(strings.SplitN(raw, ";", 2)[0]))
	if base == "" {
		return "", mediaJSON, errSchemaConfig
	}
	switch {
	case base == "application/json", strings.HasSuffix(base, "+json"):
		return base, mediaJSON, nil
	case strings.HasPrefix(base, "text/"):
		return base, mediaText, nil
	case base == "application/octet-stream":
		return base, mediaBinary, nil
	default:
		return "", mediaJSON, errSchemaConfig
	}
}

func (a *ProjectSchemaAdapter) ValidateSchema(body []byte, r SchemaRef) ([]SchemaIssue, error) {
	if err := a.PreflightSchema(r); err != nil {
		return nil, err
	}
	a.mu.Lock()
	t := a.targets[schemaKey(r)]
	a.mu.Unlock()
	if t.schema != nil {
		return validateJSONBody(t.schema, body)
	}
	resp := t.responses[strconv.Itoa(r.Status)]
	if resp == nil {
		resp = t.responses[fmt.Sprintf("%dXX", r.Status/100)]
	}
	if resp == nil {
		resp = t.responses["default"]
	}
	if resp == nil {
		return []SchemaIssue{{InstancePath: "$.status", Message: "response status has no matching schema contract"}}, nil
	}
	issues := validateResponseHeaders(resp.headers, r.Headers)
	if len(resp.media) == 0 {
		if len(body) != 0 {
			issues = append(issues, SchemaIssue{InstancePath: "$.body", Message: "response body is not declared by the selected contract"})
		}
		return issues, nil
	}
	actual := responseContentType(r.Headers)
	if actual == "" {
		return append(issues, SchemaIssue{InstancePath: "$.headers.Content-Type", Message: "response Content-Type is missing"}), nil
	}
	media := resp.media[actual]
	if media == nil {
		return append(issues, SchemaIssue{InstancePath: "$.headers.Content-Type", Message: "response media type is not declared by the selected contract"}), nil
	}
	contentIssues, err := validateMediaBody(media, body)
	if err != nil {
		return nil, err
	}
	return append(issues, contentIssues...), nil
}

func responseContentType(headers [][2]string) string {
	for _, h := range headers {
		if strings.EqualFold(h[0], "Content-Type") {
			base := strings.ToLower(strings.TrimSpace(strings.SplitN(h[1], ";", 2)[0]))
			return base
		}
	}
	return ""
}

func validateResponseHeaders(contracts []*adapterHeader, headers [][2]string) []SchemaIssue {
	actual := map[string][]string{}
	for _, h := range headers {
		name := strings.ToLower(strings.TrimSpace(h[0]))
		actual[name] = append(actual[name], h[1])
	}
	var issues []SchemaIssue
	for _, h := range contracts {
		values, ok := actual[strings.ToLower(h.name)]
		if !ok || len(values) == 0 {
			if h.required {
				issues = append(issues, SchemaIssue{
					InstancePath: "$.headers." + h.name,
					Message:      "required response header is missing",
				})
			}
			continue
		}
		if h.schema == nil {
			continue
		}
		raw := strings.Join(values, ", ")
		var instance any
		switch h.typ {
		case "string":
			instance = raw
		case "integer":
			n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
			if err != nil {
				issues = append(issues, SchemaIssue{InstancePath: "$.headers." + h.name, Message: "response header is not a valid integer"})
				continue
			}
			instance = n
		case "number":
			n, err := strconv.ParseFloat(strings.TrimSpace(raw), 64)
			if err != nil {
				issues = append(issues, SchemaIssue{InstancePath: "$.headers." + h.name, Message: "response header is not a valid number"})
				continue
			}
			instance = n
		case "boolean":
			b, err := strconv.ParseBool(strings.TrimSpace(raw))
			if err != nil {
				issues = append(issues, SchemaIssue{InstancePath: "$.headers." + h.name, Message: "response header is not a valid boolean"})
				continue
			}
			instance = b
		default:
			continue
		}
		encoded, err := json.Marshal(instance)
		if err != nil {
			issues = append(issues, SchemaIssue{InstancePath: "$.headers." + h.name, Message: "response header value cannot be evaluated"})
			continue
		}
		bodyIssues, err := validateJSONBody(h.schema, encoded)
		if err != nil {
			issues = append(issues, SchemaIssue{InstancePath: "$.headers." + h.name, Message: "response header schema could not be evaluated"})
			continue
		}
		for i := range bodyIssues {
			bodyIssues[i].InstancePath = "$.headers." + h.name
		}
		issues = append(issues, bodyIssues...)
	}
	return issues
}

func validateMediaBody(media *adapterMedia, body []byte) ([]SchemaIssue, error) {
	switch media.kind {
	case mediaJSON:
		return validateJSONBody(media.schema, body)
	case mediaText:
		if !utf8.Valid(body) || bytes.IndexByte(body, 0) >= 0 {
			return []SchemaIssue{{InstancePath: "$", Message: "response body is not valid UTF-8 text"}}, nil
		}
		if media.schema == nil {
			return nil, nil
		}
		encoded, err := json.Marshal(string(body))
		if err != nil {
			return nil, errSchemaConfig
		}
		return validateJSONBody(media.schema, encoded)
	case mediaBinary:
		// The contract declares only the media boundary; there is no schema
		// language here that can truthfully validate arbitrary bytes.
		return nil, nil
	default:
		return nil, errSchemaConfig
	}
}

func validateJSONBody(schema *adapterSchema, body []byte) ([]SchemaIssue, error) {
	if schema == nil || len(schema.encoded) == 0 {
		return nil, errSchemaConfig
	}
	if !json.Valid(body) {
		return []SchemaIssue{{InstancePath: "$", Message: "response body is not valid JSON"}}, nil
	}
	// Delegate instance semantics to the actual A03 port. All references were
	// already resolved by configuration preflight; the engine cannot reopen files.
	result, err := core.NewSchemaValidator().ValidateInstance(schema.encoded, body, core.SchemaValidationOptions{})
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
			if _, err := core.ParseStrictYAMLTree(b, core.YAMLLimits{MaxBytes: core.DefaultMaxSchemaBytes, MaxDepth: 64, MaxNodes: 100000, MaxExpandedNodes: 100000}); err != nil {
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
			for i := 0; i < len(key); i++ {
				if key[i] == '~' {
					if i+1 >= len(key) || (key[i+1] != '0' && key[i+1] != '1') {
						return nil, "", errSchemaConfig
					}
					i++
				}
			}
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
	// Schema depth counts the root as level one, like the A03 evaluator.
	if depth >= core.DefaultMaxSchemaDepth || l.nodes > 10000 {
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
				if !ok {
					return false
				}
				switch s {
				case "null", "boolean", "object", "array", "number", "integer", "string":
					return true
				}
				return false
			}
			if arr, ok := v.([]any); ok {
				if len(arr) == 0 {
					return nil, errSchemaConfig
				}
				seen := map[any]bool{}
				for _, x := range arr {
					if !valid(x) || seen[x] {
						return nil, errSchemaConfig
					}
					seen[x] = true
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
			seen := map[string]bool{}
			for _, x := range arr {
				name, ok := x.(string)
				if !ok || seen[name] {
					return nil, errSchemaConfig
				}
				seen[name] = true
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
			seen := map[string]bool{}
			for _, value := range arr {
				localBudget := int64(core.DefaultMaxSchemaBytes)
				if err := boundSchemaJSON(value, &localBudget, 0); err != nil {
					return nil, err
				}
				b, err := json.Marshal(value)
				if err != nil || seen[string(b)] {
					return nil, errSchemaConfig
				}
				seen[string(b)] = true
			}
			out[k] = v
		case "uniqueItems":
			if _, ok := v.(bool); !ok {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "title", "description", "$comment":
			if _, ok := v.(string); !ok {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "examples":
			if _, ok := v.([]any); !ok {
				return nil, errSchemaConfig
			}
			out[k] = v
		case "const", "default":
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

// SchemaSnapshotDigest binds the report snapshot to the prepared schema bytes,
// including the selected Operation responses; no schema metadata is exposed.
func (a *ProjectSchemaAdapter) SchemaSnapshotDigest() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	targets := map[string]any{}
	for key, t := range a.targets {
		if t.schema != nil {
			targets[key] = t.schema.node
			continue
		}
		responses := map[string]any{}
		for status, resp := range t.responses {
			entry := map[string]any{}
			media := map[string]any{}
			for mt, m := range resp.media {
				switch m.kind {
				case mediaJSON:
					media[mt] = m.schema.node
				case mediaText:
					if m.schema == nil {
						media[mt] = "text"
					} else {
						media[mt] = m.schema.node
					}
				case mediaBinary:
					media[mt] = "binary"
				}
			}
			headers := map[string]any{}
			for _, h := range resp.headers {
				var schema any
				if h.schema != nil {
					schema = h.schema.node
				}
				headers[h.name] = map[string]any{"required": h.required, "type": h.typ, "schema": schema}
			}
			entry["media"] = media
			entry["headers"] = headers
			responses[status] = entry
		}
		targets[key] = responses
	}
	b, _ := json.Marshal(targets)
	return digestBytes(b)
}

// Bound the expanded representation too: repeatedly referencing a large const
// or annotation must not turn a small offline file set into an unbounded JSON
// allocation. String escaping uses a conservative six-byte upper bound.
func boundSchemaJSON(value any, budget *int64, depth int) error {
	if depth > 64 || *budget < 0 {
		return errSchemaConfig
	}
	*budget -= 2
	switch v := value.(type) {
	case map[string]any:
		for key, child := range v {
			*budget -= int64(len(key))*6 + 4
			if err := boundSchemaJSON(child, budget, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range v {
			if err := boundSchemaJSON(child, budget, depth+1); err != nil {
				return err
			}
		}
	case string:
		*budget -= int64(len(v)) * 6
	case nil, bool:
		*budget -= 5
	case int, int64, uint64, float64:
		*budget -= 64
	case json.Number:
		*budget -= int64(len(v))
	default:
		return errSchemaConfig
	}
	if *budget < 0 {
		return errSchemaConfig
	}
	return nil
}
