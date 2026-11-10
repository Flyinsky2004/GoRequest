package sdkgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// specDoc is the subset of an OpenAPI 3.1/3.2 document the generator consumes.
type specDoc struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths      map[string]json.RawMessage `json:"paths"`
	Webhooks   map[string]json.RawMessage `json:"webhooks"`
	Components struct {
		Schemas       map[string]json.RawMessage `json:"schemas"`
		Parameters    map[string]json.RawMessage `json:"parameters"`
		RequestBodies map[string]json.RawMessage `json:"requestBodies"`
		Responses     map[string]json.RawMessage `json:"responses"`
	} `json:"components"`
}

type rawOperation struct {
	Ref         string                     `json:"$ref"`
	OperationID string                     `json:"operationId"`
	Summary     string                     `json:"summary"`
	Description string                     `json:"description"`
	Deprecated  bool                       `json:"deprecated"`
	Parameters  []json.RawMessage          `json:"parameters"`
	RequestBody json.RawMessage            `json:"requestBody"`
	Responses   map[string]json.RawMessage `json:"responses"`
	Security    []map[string][]string      `json:"security"`
	Callbacks   map[string]json.RawMessage `json:"callbacks"`
}

type rawParameter struct {
	Ref      string                     `json:"$ref"`
	Name     string                     `json:"name"`
	In       string                     `json:"in"`
	Required bool                       `json:"required"`
	Style    string                     `json:"style"`
	Explode  *bool                      `json:"explode"`
	Schema   json.RawMessage            `json:"schema"`
	Content  map[string]json.RawMessage `json:"content"`
}

type rawRequestBody struct {
	Ref      string                     `json:"$ref"`
	Required bool                       `json:"required"`
	Content  map[string]json.RawMessage `json:"content"`
}

type rawMediaType struct {
	Schema         json.RawMessage `json:"schema"`
	ItemSchema     json.RawMessage `json:"itemSchema"`
	ItemEncoding   json.RawMessage `json:"itemEncoding"`
	PrefixEncoding json.RawMessage `json:"prefixEncoding"`
}

type rawSchema struct {
	Ref      string            `json:"$ref"`
	Type     any               `json:"type"`
	Format   string            `json:"format"`
	Items    json.RawMessage   `json:"items"`
	Enum     []any             `json:"enum"`
	OneOf    []json.RawMessage `json:"oneOf"`
	AnyOf    []json.RawMessage `json:"anyOf"`
	AllOf    []json.RawMessage `json:"allOf"`
	Nullable bool              `json:"nullable"`
}

// paramKind is the generated Go scalar type of a parameter.
type paramKind string

const (
	kindString  paramKind = "string"
	kindInt64   paramKind = "int64"
	kindFloat64 paramKind = "float64"
	kindBool    paramKind = "bool"
)

// paramSpec is one resolved, supported parameter.
type paramSpec struct {
	Name     string
	In       string // path | query | header
	Required bool
	Kind     paramKind
	Array    bool
	// Query array serialization.
	Explode bool
	Style   string
}

// bodySpec is the supported request body (application/json only).
type bodySpec struct {
	Required bool
	Schema   json.RawMessage
}

var supportedMethods = map[string]bool{
	"get": true, "put": true, "post": true, "delete": true,
	"options": true, "head": true, "patch": true, "trace": true, "query": true,
}

var pathItemNonMethodKeys = map[string]bool{
	"$ref": true, "summary": true, "description": true, "servers": true,
	"parameters": true, "additionalOperations": true,
}

var managedHeaders = map[string]bool{
	"host": true, "content-length": true, "transfer-encoding": true,
	"connection": true, "trailer": true, "upgrade": true,
	"proxy-connection": true, "te": true, "keep-alive": true,
}

// parseSpec decodes and version-checks the snapshot.
func parseSpec(snapshot []byte) (*specDoc, error) {
	if len(snapshot) == 0 {
		return nil, fmt.Errorf("sdkgen: OpenAPI snapshot is empty")
	}
	var doc specDoc
	if err := json.Unmarshal(snapshot, &doc); err != nil {
		return nil, fmt.Errorf("sdkgen: parse OpenAPI JSON snapshot: %w", err)
	}
	v := strings.TrimSpace(doc.OpenAPI)
	if !strings.HasPrefix(v, "3.1.") && !strings.HasPrefix(v, "3.2.") {
		return nil, fmt.Errorf("sdkgen: unsupported OpenAPI version %q (supported: 3.1.x, 3.2.x)", v)
	}
	if len(doc.Paths) == 0 {
		return nil, fmt.Errorf("sdkgen: snapshot has no paths")
	}
	return &doc, nil
}

// collectOperations walks paths deterministically and returns supported
// operations plus operation-level unsupported findings.
func collectOperations(doc *specDoc) ([]Operation, []UnsupportedOperation, []Diagnostic, error) {
	var ops []Operation
	var unsupported []UnsupportedOperation
	var diags []Diagnostic

	if len(doc.Webhooks) > 0 {
		for _, name := range sortedMapKeys(doc.Webhooks) {
			unsupported = append(unsupported, UnsupportedOperation{
				Method: "WEBHOOK", Path: name,
				Reason: "webhooks are not generated as client operations",
			})
		}
	}

	paths := sortedMapKeys(doc.Paths)
	for _, path := range paths {
		rawItem := doc.Paths[path]
		var item map[string]json.RawMessage
		if err := json.Unmarshal(rawItem, &item); err != nil {
			return nil, nil, nil, fmt.Errorf("sdkgen: parse path item %q: %w", path, err)
		}
		if _, hasRef := item["$ref"]; hasRef {
			unsupported = append(unsupported, UnsupportedOperation{
				Method: "*", Path: path, Reason: "path item $ref is not supported",
			})
			continue
		}
		var pathParamsRaw []json.RawMessage
		if raw, ok := item["parameters"]; ok {
			if err := json.Unmarshal(raw, &pathParamsRaw); err != nil {
				unsupported = append(unsupported, UnsupportedOperation{
					Method: "*", Path: path, Reason: "path-level parameters is not an array",
				})
				continue
			}
		}
		pathParams, err := parseParameterList(pathParamsRaw, doc, 0)
		if err != nil {
			unsupported = append(unsupported, UnsupportedOperation{
				Method: "*", Path: path, Reason: err.Error(),
			})
			continue
		}

		methods := make([]string, 0, len(item))
		for key := range item {
			lower := strings.ToLower(key)
			if pathItemNonMethodKeys[key] || !supportedMethods[lower] {
				continue
			}
			methods = append(methods, key)
		}
		sort.Strings(methods)

		for _, methodKey := range methods {
			method := strings.ToUpper(methodKey)
			rawOpBytes := item[methodKey]
			var raw rawOperation
			if err := json.Unmarshal(rawOpBytes, &raw); err != nil {
				unsupported = append(unsupported, UnsupportedOperation{
					Method: method, Path: path, Reason: "operation object is not valid JSON",
				})
				continue
			}
			op, reason := buildOperation(doc, path, method, &raw, pathParams)
			if reason != "" {
				unsupported = append(unsupported, UnsupportedOperation{
					Method: method, Path: path, OperationID: raw.OperationID, Reason: reason,
				})
				continue
			}
			ops = append(ops, *op)
		}

		if addOps, ok := item["additionalOperations"]; ok {
			var extra map[string]json.RawMessage
			if err := json.Unmarshal(addOps, &extra); err == nil {
				for _, m := range sortedMapKeys(extra) {
					unsupported = append(unsupported, UnsupportedOperation{
						Method: strings.ToUpper(m), Path: path,
						Reason: "OpenAPI 3.2 additionalOperations are not supported in this phase",
					})
				}
			}
		}
	}

	// Deterministic order by path then method.
	sort.SliceStable(ops, func(i, j int) bool {
		if ops[i].Path == ops[j].Path {
			return ops[i].Method < ops[j].Method
		}
		return ops[i].Path < ops[j].Path
	})
	sort.SliceStable(unsupported, func(i, j int) bool {
		if unsupported[i].Path == unsupported[j].Path {
			return unsupported[i].Method < unsupported[j].Method
		}
		return unsupported[i].Path < unsupported[j].Path
	})
	return ops, unsupported, diags, nil
}

// buildOperation resolves one operation and returns a non-empty reason when it
// cannot be generated faithfully.
func buildOperation(doc *specDoc, path, method string, raw *rawOperation, pathParams []paramSpec) (*Operation, string) {
	if raw.Ref != "" {
		return nil, "operation $ref is not supported"
	}
	if len(raw.Callbacks) > 0 {
		return nil, "callbacks are not supported"
	}
	opParams, err := parseParameterList(raw.Parameters, doc, 0)
	if err != nil {
		return nil, err.Error()
	}
	params := mergeParams(pathParams, opParams)

	templateParams := pathTemplateParams(path)
	pathParamSet := map[string]bool{}
	for _, p := range params {
		if p.In == "path" {
			pathParamSet[p.Name] = true
		}
	}
	for _, name := range templateParams {
		if !pathParamSet[name] {
			return nil, fmt.Sprintf("path template parameter %q has no path parameter definition", name)
		}
	}
	for name := range pathParamSet {
		if !containsString(templateParams, name) {
			return nil, fmt.Sprintf("path parameter %q does not appear in the path template", name)
		}
	}

	body, reason := resolveRequestBody(doc, raw.RequestBody)
	if reason != "" {
		return nil, reason
	}
	if reason := checkResponseStreaming(raw.Responses); reason != "" {
		return nil, reason
	}

	security, reason := resolveSecurity(raw.Security)
	if reason != "" {
		return nil, reason
	}

	opID := strings.TrimSpace(raw.OperationID)
	if opID == "" {
		opID = deriveOperationName(method, path)
	}
	return &Operation{
		Method:       method,
		Path:         path,
		OperationID:  opID,
		Summary:      strings.TrimSpace(raw.Summary),
		Deprecated:   raw.Deprecated,
		Security:     security,
		paramsList:   params,
		body:         body,
		rawResponses: raw.Responses,
	}, ""
}

// mergeParams merges path-level and operation-level parameters; operation
// level wins on (in, lower(name)). The result is sorted deterministically.
func mergeParams(pathLevel, opLevel []paramSpec) []paramSpec {
	byKey := map[string]paramSpec{}
	order := []string{}
	add := func(p paramSpec) {
		key := p.In + ":" + strings.ToLower(p.Name)
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = p
	}
	for _, p := range pathLevel {
		add(p)
	}
	for _, p := range opLevel {
		add(p)
	}
	out := make([]paramSpec, 0, len(order))
	for _, key := range order {
		out = append(out, byKey[key])
	}
	sort.SliceStable(out, func(i, j int) bool {
		ri, rj := paramInRank(out[i].In), paramInRank(out[j].In)
		if ri == rj {
			return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
		}
		return ri < rj
	})
	return out
}

func paramInRank(in string) int {
	switch in {
	case "path":
		return 0
	case "query":
		return 1
	default:
		return 2
	}
}

// parseParameterList resolves local parameter refs and validates support.
func parseParameterList(list []json.RawMessage, doc *specDoc, depth int) ([]paramSpec, error) {
	if len(list) == 0 {
		return nil, nil
	}
	if depth > 8 {
		return nil, fmt.Errorf("parameter $ref nesting is too deep")
	}
	out := make([]paramSpec, 0, len(list))
	seen := map[string]string{}
	for _, item := range list {
		var p rawParameter
		if err := json.Unmarshal(item, &p); err != nil {
			return nil, fmt.Errorf("parameter is not an object")
		}
		if p.Ref != "" {
			resolved, err := resolveComponentRef(doc.Components.Parameters, p.Ref, "#/components/parameters/")
			if err != nil {
				return nil, err
			}
			var nested rawParameter
			if err := json.Unmarshal(resolved, &nested); err != nil {
				return nil, fmt.Errorf("resolved parameter is not an object")
			}
			p = nested
		}
		spec, reason := resolveParameter(doc, &p)
		if reason != "" {
			return nil, fmt.Errorf("%s", reason)
		}
		key := spec.In + ":" + spec.Name
		if spec.In == "header" {
			key = spec.In + ":" + strings.ToLower(spec.Name)
		}
		if previous, dup := seen[key]; dup {
			return nil, fmt.Errorf("duplicate %s parameter %q (also declared as %q); duplicate parameters cannot be merged faithfully", spec.In, spec.Name, previous)
		}
		seen[key] = spec.Name
		out = append(out, spec)
	}
	return out, nil
}

// resolveParameter validates one parameter and returns its Go mapping.
func resolveParameter(doc *specDoc, p *rawParameter) (paramSpec, string) {
	name := strings.TrimSpace(p.Name)
	if name == "" {
		return paramSpec{}, "parameter has an empty name"
	}
	in := strings.ToLower(strings.TrimSpace(p.In))
	switch in {
	case "path", "query", "header":
	case "cookie":
		return paramSpec{}, fmt.Sprintf("cookie parameter %q is not supported", name)
	default:
		return paramSpec{}, fmt.Sprintf("parameter %q has unsupported location %q", name, p.In)
	}
	if len(p.Content) > 0 {
		return paramSpec{}, fmt.Sprintf("parameter %q uses content instead of schema, which is not supported", name)
	}
	if in == "header" {
		lower := strings.ToLower(name)
		if managedHeaders[lower] {
			return paramSpec{}, fmt.Sprintf("header parameter %q targets a managed header", name)
		}
		if !isValidHeaderName(name) {
			return paramSpec{}, fmt.Sprintf("header parameter %q has an invalid header name", name)
		}
	}
	kind, array, reason := resolveSchemaType(doc, p.Schema, 0)
	if reason != "" {
		return paramSpec{}, fmt.Sprintf("parameter %q: %s", name, reason)
	}
	style := strings.ToLower(strings.TrimSpace(p.Style))
	explode := true
	if p.Explode != nil {
		explode = *p.Explode
	}
	switch in {
	case "path":
		if style != "" && style != "simple" {
			return paramSpec{}, fmt.Sprintf("path parameter %q style %q is not supported", name, p.Style)
		}
	case "header":
		if style != "" && style != "simple" {
			return paramSpec{}, fmt.Sprintf("header parameter %q style %q is not supported", name, p.Style)
		}
	case "query":
		switch style {
		case "", "form":
			// default
		case "spacedelimited", "pipedelimited":
			if !array {
				return paramSpec{}, fmt.Sprintf("query parameter %q style %q requires an array", name, p.Style)
			}
			if p.Explode != nil && *p.Explode {
				return paramSpec{}, fmt.Sprintf("query parameter %q style %q does not support explode=true", name, p.Style)
			}
			explode = false
		case "deepObject":
			return paramSpec{}, fmt.Sprintf("query parameter %q uses unsupported deepObject style", name)
		default:
			return paramSpec{}, fmt.Sprintf("query parameter %q style %q is not supported", name, p.Style)
		}
	}
	required := p.Required || in == "path"
	return paramSpec{
		Name:     name,
		In:       in,
		Required: required,
		Kind:     kind,
		Array:    array,
		Explode:  explode,
		Style:    style,
	}, ""
}

// resolveRequestBody returns the supported body mapping or a reason.
func resolveRequestBody(doc *specDoc, raw json.RawMessage) (*bodySpec, string) {
	if len(raw) == 0 {
		return nil, ""
	}
	var rb rawRequestBody
	if err := json.Unmarshal(raw, &rb); err != nil {
		return nil, "requestBody is not an object"
	}
	if rb.Ref != "" {
		resolved, err := resolveComponentRef(doc.Components.RequestBodies, rb.Ref, "#/components/requestBodies/")
		if err != nil {
			return nil, err.Error()
		}
		if err := json.Unmarshal(resolved, &rb); err != nil {
			return nil, "resolved requestBody is not an object"
		}
	}
	if len(rb.Content) == 0 {
		return nil, ""
	}
	if len(rb.Content) != 1 {
		return nil, "multi-media request bodies are not supported"
	}
	var out bodySpec
	out.Required = rb.Required
	for media, rawMT := range rb.Content {
		if strings.ToLower(strings.TrimSpace(media)) != "application/json" {
			return nil, fmt.Sprintf("request body media type %q is not supported (only application/json)", media)
		}
		var mt rawMediaType
		if len(rawMT) > 0 {
			if err := json.Unmarshal(rawMT, &mt); err != nil {
				return nil, "request body media type is not an object"
			}
		}
		if len(mt.ItemSchema) > 0 || len(mt.ItemEncoding) > 0 || len(mt.PrefixEncoding) > 0 {
			return nil, "OpenAPI 3.2 streaming request bodies are not supported"
		}
		out.Schema = mt.Schema
	}
	return &out, ""
}

// checkResponseStreaming rejects 3.2 streaming response media types.
func checkResponseStreaming(responses map[string]json.RawMessage) string {
	for _, raw := range responses {
		var resp struct {
			Content map[string]json.RawMessage `json:"content"`
		}
		if err := json.Unmarshal(raw, &resp); err != nil {
			continue
		}
		for _, rawMT := range resp.Content {
			var mt rawMediaType
			if err := json.Unmarshal(rawMT, &mt); err != nil {
				continue
			}
			if len(mt.ItemSchema) > 0 || len(mt.ItemEncoding) > 0 || len(mt.PrefixEncoding) > 0 {
				return "OpenAPI 3.2 streaming responses are not supported"
			}
		}
	}
	return ""
}

// resolveSecurity records scheme names and rejects AND-combined requirements.
func resolveSecurity(security []map[string][]string) ([]string, string) {
	seen := map[string]bool{}
	for _, requirement := range security {
		if len(requirement) > 1 {
			return nil, "security requirements combined with AND are not supported; supply authentication through Client.Auth"
		}
		for name := range requirement {
			seen[name] = true
		}
	}
	return sortedMapKeys(seen), ""
}

// resolveSchemaType maps a schema to a primitive Go type. Objects, unions and
// nested arrays are unsupported in this phase.
func resolveSchemaType(doc *specDoc, raw json.RawMessage, depth int) (paramKind, bool, string) {
	if depth > 8 {
		return "", false, "schema $ref nesting is too deep"
	}
	if len(raw) == 0 {
		return kindString, false, ""
	}
	var s rawSchema
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", false, "schema is not an object"
	}
	if s.Ref != "" {
		resolved, err := resolveComponentRef(doc.Components.Schemas, s.Ref, "#/components/schemas/")
		if err != nil {
			return "", false, err.Error()
		}
		return resolveSchemaType(doc, resolved, depth+1)
	}
	if len(s.OneOf) > 0 || len(s.AnyOf) > 0 || len(s.AllOf) > 0 {
		return "", false, "union or composed schemas are not supported"
	}
	types := schemaTypes(s.Type)
	if len(types) == 0 && len(s.Enum) > 0 {
		types = inferEnumTypes(s.Enum)
	}
	nonNull := make([]string, 0, len(types))
	for _, t := range types {
		if strings.EqualFold(t, "null") {
			continue
		}
		nonNull = append(nonNull, strings.ToLower(t))
	}
	if len(nonNull) == 0 {
		return kindString, false, ""
	}
	if len(nonNull) > 1 {
		return "", false, "multi-type schemas are not supported"
	}
	switch nonNull[0] {
	case "string":
		return kindString, false, ""
	case "integer":
		return kindInt64, false, ""
	case "number":
		return kindFloat64, false, ""
	case "boolean":
		return kindBool, false, ""
	case "array":
		if len(s.Items) == 0 {
			return "", false, "array schema has no items"
		}
		itemKind, itemArray, reason := resolveSchemaType(doc, s.Items, depth+1)
		if reason != "" {
			return "", false, reason
		}
		if itemArray {
			return "", false, "nested arrays are not supported"
		}
		return itemKind, true, ""
	default:
		return "", false, fmt.Sprintf("schema type %q is not supported", nonNull[0])
	}
}

func schemaTypes(v any) []string {
	switch t := v.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func inferEnumTypes(values []any) []string {
	kind := ""
	for _, v := range values {
		var cur string
		switch v.(type) {
		case string:
			cur = "string"
		case float64, int, int64:
			cur = "number"
		case bool:
			cur = "boolean"
		default:
			return nil
		}
		if kind == "" {
			kind = cur
		} else if kind != cur {
			return nil
		}
	}
	if kind == "" {
		return nil
	}
	return []string{kind}
}

// resolveComponentRef resolves a local component reference and rejects remote
// or escaping references.
func resolveComponentRef(components map[string]json.RawMessage, ref, prefix string) (json.RawMessage, error) {
	ref = strings.TrimSpace(ref)
	if !strings.HasPrefix(ref, prefix) {
		return nil, fmt.Errorf("reference %q is not a local %s reference", ref, prefix)
	}
	name := strings.TrimPrefix(ref, prefix)
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, "..") {
		return nil, fmt.Errorf("reference %q has an invalid component name", ref)
	}
	raw, ok := components[name]
	if !ok {
		return nil, fmt.Errorf("reference %q does not resolve to a component", ref)
	}
	return raw, nil
}

// pathTemplateParams extracts {name} parameters in order.
func pathTemplateParams(path string) []string {
	var out []string
	seen := map[string]bool{}
	for i := 0; i < len(path); i++ {
		if path[i] != '{' {
			continue
		}
		end := strings.IndexByte(path[i+1:], '}')
		if end < 0 {
			break
		}
		name := path[i+1 : i+1+end]
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
		i += end + 1
	}
	return out
}

func isValidHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if r <= 32 || r >= 127 {
			return false
		}
		switch r {
		case '(', ')', ',', '/', ':', ';', '<', '=', '>', '?', '@', '[', '\\', ']', '{', '}', '"':
			return false
		}
	}
	return true
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func sortedMapKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// rawResponse is the OpenAPI Response Object subset used for typed bodies.
type rawResponse struct {
	Ref         string                     `json:"$ref"`
	Description string                     `json:"description"`
	Content     map[string]json.RawMessage `json:"content"`
}

// compileResponseTypes maps every declared JSON response body to a named model.
// Responses without a JSON schema stay untyped (the raw body is still
// returned); a non-JSON media type is reported as an informational diagnostic.
func compileResponseTypes(comp *typeCompiler, op *Operation) ([]responseType, error) {
	keys := make([]string, 0, len(op.rawResponses))
	for key := range op.rawResponses {
		keys = append(keys, key)
	}
	sort.SliceStable(keys, func(i, j int) bool { return statusSortKey(keys[i]) < statusSortKey(keys[j]) })

	out := make([]responseType, 0, len(keys))
	for _, key := range keys {
		resp, err := resolveResponseObject(comp.doc, op.rawResponses[key])
		if err != nil {
			return nil, fmt.Errorf("response %q: %w", key, err)
		}
		if len(resp.Content) == 0 {
			continue
		}
		mediaKey, mt, ok := pickJSONMedia(resp.Content)
		if !ok {
			comp.addDiagnostic("info", "sdk-response-untyped", op.Method+" "+op.Path+" "+key,
				"response has no application/json content; the typed model is not generated")
			continue
		}
		if len(resp.Content) > 1 {
			comp.addDiagnostic("info", "sdk-response-media", op.Method+" "+op.Path+" "+key,
				fmt.Sprintf("using %s; other response media types are returned as raw bytes", mediaKey))
		}
		if len(mt.Schema) == 0 {
			continue
		}
		label := statusLabel(key)
		t, err := comp.compileRoot(mt.Schema, op.GoName+"Response"+label,
			"operation "+op.Method+" "+op.Path+" responses."+key, false)
		if err != nil {
			return nil, fmt.Errorf("response %q schema: %w", key, err)
		}
		out = append(out, responseType{Status: key, Type: t})
	}
	return out, nil
}

// resolveResponseObject resolves a local components.responses reference.
func resolveResponseObject(doc *specDoc, raw json.RawMessage) (*rawResponse, error) {
	var resp rawResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("response is not an object")
	}
	if resp.Ref != "" {
		resolved, err := resolveComponentRef(doc.Components.Responses, resp.Ref, "#/components/responses/")
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(resolved, &resp); err != nil {
			return nil, fmt.Errorf("resolved response is not an object")
		}
	}
	return &resp, nil
}

// pickJSONMedia selects the JSON media type for a response.
func pickJSONMedia(content map[string]json.RawMessage) (string, rawMediaType, bool) {
	keys := sortedMapKeys(content)
	var fallback string
	for _, k := range keys {
		base := strings.ToLower(strings.TrimSpace(strings.SplitN(k, ";", 2)[0]))
		if base == "application/json" {
			var mt rawMediaType
			if err := json.Unmarshal(content[k], &mt); err != nil {
				return "", rawMediaType{}, false
			}
			return k, mt, true
		}
		if fallback == "" && strings.HasSuffix(base, "+json") {
			fallback = k
		}
	}
	if fallback == "" {
		return "", rawMediaType{}, false
	}
	var mt rawMediaType
	if err := json.Unmarshal(content[fallback], &mt); err != nil {
		return "", rawMediaType{}, false
	}
	return fallback, mt, true
}

// statusSortKey orders exact statuses before ranges before default.
func statusSortKey(status string) string {
	s := strings.ToLower(strings.TrimSpace(status))
	if s == "" {
		return "9:"
	}
	if s == "default" {
		return "8:default"
	}
	if len(s) == 3 && s[0] >= '1' && s[0] <= '5' && (s[1] == 'x' || s[2] == 'x') {
		return "5:" + s
	}
	return "0:" + s
}

// statusLabel builds a Go/TypeScript identifier fragment from a status key.
func statusLabel(status string) string {
	var b strings.Builder
	for _, r := range status {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
		}
	}
	out := b.String()
	if out == "" {
		return "Default"
	}
	if out[0] >= 'a' && out[0] <= 'z' {
		out = strings.ToUpper(out[:1]) + out[1:]
	}
	return out
}
