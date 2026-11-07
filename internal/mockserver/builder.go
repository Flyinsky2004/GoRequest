package mockserver

import (
	"encoding/json"
	"fmt"
	stdmath "math"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gorequest/internal/core"
)

// SchemaExampleError is returned when a JSON Schema uses constructs outside the
// supported deterministic synthesis subset or fails A03 validation, requiring
// the user or contract to supply an explicit example.
type SchemaExampleError struct {
	SchemaPath string
	Keyword    string
	Reason     string
}

func (e *SchemaExampleError) Error() string {
	if e.Keyword != "" {
		return fmt.Sprintf("schema example synthesis unsupported at %s (%s): %s — provide an explicit example", e.SchemaPath, e.Keyword, e.Reason)
	}
	return fmt.Sprintf("schema example synthesis failed at %s: %s — provide an explicit example", e.SchemaPath, e.Reason)
}

var templateSegmentRegexp = regexp.MustCompile(`^\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}$`)

// BuildRuleOptions configures how a MockRule is derived from a Request and an
// optional response example.
type BuildRuleOptions struct {
	RuleID         string
	Priority       int
	IncludeQuery   bool
	DefaultDelayMs int
	Redactor       *core.Redactor
}

// BuildRuleFromRequest constructs a sanitized MockRule from an existing
// core.Request and an optional response example (*core.ExecResult). Sensitive
// headers (Authorization, Cookie, Set-Cookie, Proxy-Authorization, API keys),
// sensitive query parameters, and secret body fields are scrubbed and never
// persisted into the rule.
func BuildRuleFromRequest(req *core.Request, respExample *core.ExecResult, opts BuildRuleOptions) (*MockRule, error) {
	if req == nil || req.HTTP == nil {
		return nil, &core.ConfigError{Err: fmt.Errorf("request has no HTTP definition")}
	}
	red := opts.Redactor
	if red == nil {
		red = core.NewRedactor()
	}

	method := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
	if method == "" {
		method = http.MethodGet
	}
	if err := core.ValidateHTTPMethod(method); err != nil {
		return nil, err
	}

	pathTmpl := extractMockPathTemplate(req.HTTP.URL)
	ruleID := strings.TrimSpace(opts.RuleID)
	if ruleID == "" {
		ruleID = sanitizeIDSlug(req.ID)
		if ruleID == "" {
			ruleID = sanitizeIDSlug(method + "-" + pathTmpl)
		}
		if ruleID == "" {
			ruleID = "mock-rule"
		}
	}
	priority := opts.Priority
	if priority == 0 {
		priority = 100
	}

	var queryMatchers []KeyValueMatcher
	if opts.IncludeQuery {
		for _, kv := range req.HTTP.Query {
			if !kv.IsEnabled() {
				continue
			}
			k := strings.TrimSpace(kv.Name)
			v := strings.TrimSpace(kv.Value)
			if k == "" || IsSensitiveKeyName(k) || strings.Contains(k, "{{") {
				continue
			}
			if strings.Contains(v, "{{secret.") {
				continue
			}
			cleanVal := ScrubFreeText(v, red)
			if cleanVal == redactedLiteral {
				continue
			}
			// If value is a template like {{page}}, require existence rather than literal "{{page}}"
			if strings.Contains(cleanVal, "{{") {
				tr := true
				queryMatchers = append(queryMatchers, KeyValueMatcher{Name: k, Exists: &tr})
			} else {
				valCopy := cleanVal
				queryMatchers = append(queryMatchers, KeyValueMatcher{Name: k, Equals: &valCopy})
			}
		}
	}

	resp := RuleResponse{
		Status:      http.StatusOK,
		ContentType: "application/json; charset=utf-8",
		Body:        fmt.Sprintf("{\n  \"id\": \"{{req.id}}\",\n  \"requestId\": %q,\n  \"status\": \"ok\"\n}", req.ID),
		DelayMs:     opts.DefaultDelayMs,
	}

	if respExample != nil {
		sanitizedResp := red.SanitizeExecResult(respExample)
		if sanitizedResp.Status >= 100 && sanitizedResp.Status <= 599 {
			resp.Status = sanitizedResp.Status
		}
		var safeHeaders []core.KV
		for _, h := range sanitizedResp.Headers {
			hName := strings.TrimSpace(h[0])
			hVal := strings.TrimSpace(h[1])
			if hName == "" || isSkippedResponseExampleHeader(hName) || IsSensitiveKeyName(hName) {
				continue
			}
			if hVal == redactedLiteral || looksLikeRawCredential(hVal) {
				continue
			}
			if strings.EqualFold(hName, "Content-Type") {
				resp.ContentType = hVal
				continue
			}
			safeHeaders = append(safeHeaders, core.KV{Name: hName, Value: ScrubFreeText(hVal, red)})
		}
		resp.Headers = safeHeaders

		rawBody := string(sanitizedResp.Body)
		if sanitizedResp.Pretty != "" {
			rawBody = sanitizedResp.Pretty
		}
		resp.Body = SanitizeBodyText(rawBody, red)
	}

	rule := &MockRule{
		ID:          ruleID,
		Name:        strings.TrimSpace(req.Name),
		Description: fmt.Sprintf("Mock rule derived from request %q", req.ID),
		Priority:    priority,
		Match: RuleMatch{
			Method: method,
			Path:   pathTmpl,
			Query:  queryMatchers,
		},
		Response: resp,
		Variants: defaultErrorVariants(),
	}
	if rule.Name == "" {
		rule.Name = method + " " + pathTmpl
	}
	if err := validateMockRule(rule, ServerLimits{}); err != nil {
		return nil, err
	}
	return rule, nil
}

func defaultErrorVariants() map[string]RuleResponse {
	return map[string]RuleResponse{
		"success": {}, // populated by caller or falls back to rule.Response when omitted
		"error_400": {
			Status:      http.StatusBadRequest,
			ContentType: "application/json; charset=utf-8",
			Body:        "{\n  \"error\": \"bad_request\",\n  \"message\": \"Simulated 400 Bad Request scenario\"\n}",
		},
		"error_401": {
			Status:      http.StatusUnauthorized,
			ContentType: "application/json; charset=utf-8",
			Body:        "{\n  \"error\": \"unauthorized\",\n  \"message\": \"Simulated 401 Unauthorized scenario\"\n}",
		},
		"error_500": {
			Status:      http.StatusInternalServerError,
			ContentType: "application/json; charset=utf-8",
			Body:        "{\n  \"error\": \"internal_error\",\n  \"message\": \"Simulated 500 Internal Server Error scenario\"\n}",
		},
	}
}

func isSkippedResponseExampleHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie",
		"content-length", "transfer-encoding", "connection", "date", "server":
		return true
	default:
		return false
	}
}

func extractMockPathTemplate(rawURL string) string {
	s := strings.TrimSpace(rawURL)
	if s == "" {
		return "/"
	}
	// Strip leading {{baseUrl}} or {{host}} prefix before the first '/'
	if strings.HasPrefix(s, "{{") {
		if slashIdx := strings.IndexByte(s, '/'); slashIdx >= 0 {
			s = s[slashIdx:]
		} else {
			return "/"
		}
	} else if u, err := url.Parse(s); err == nil && (u.Scheme != "" || u.Host != "") {
		s = u.Path
	} else if idx := strings.IndexAny(s, "?#"); idx >= 0 {
		s = s[:idx]
	}

	norm := normalizeURLPath(s)
	if norm == "/" {
		return "/"
	}
	parts := strings.Split(strings.TrimPrefix(norm, "/"), "/")
	usedNames := map[string]int{}
	for i, part := range parts {
		if m := templateSegmentRegexp.FindStringSubmatch(part); len(m) == 2 {
			param := sanitizeParamIdentifier(m[1])
			if param == "" {
				param = fmt.Sprintf("param%d", i+1)
			}
			usedNames[param]++
			if usedNames[param] > 1 {
				param = fmt.Sprintf("%s_%d", param, usedNames[param])
			}
			parts[i] = "{" + param + "}"
			continue
		}
		if strings.Contains(part, "{{") || strings.Contains(part, "}}") {
			param := fmt.Sprintf("param%d", i+1)
			parts[i] = "{" + param + "}"
		}
	}
	return "/" + strings.Join(parts, "/")
}

func sanitizeParamIdentifier(raw string) string {
	// e.g. "steps.login.userId" -> "userId"
	if idx := strings.LastIndexByte(raw, '.'); idx >= 0 {
		raw = raw[idx+1:]
	}
	var b strings.Builder
	for i, r := range raw {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (i > 0 && r >= '0' && r <= '9') || (i > 0 && r == '_'):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// GenerateSchemaExample synthesizes a deterministic JSON example (pretty-printed
// UTF-8 JSON bytes) from a JSON Schema (Draft 2020-12 / OpenAPI 3.1 / 3.2) and
// verifies the generated payload through A03 core.NewSchemaValidator().
//
// Supported deterministic subset:
//   - Explicit `const`, `enum`, `default`, `example`, `examples`
//   - `type: object` (`properties`, `required`, `minProperties`, `maxProperties`, `additionalProperties`)
//   - `type: array` (`prefixItems`, `items`, `minItems`, `maxItems`, `uniqueItems`)
//   - `type: string` (`minLength`, `maxLength`, `format`: email, uri, uuid, date, date-time, ipv4, ipv6)
//   - `type: integer` / `type: number` (`minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`)
//   - `type: boolean`, `type: null`
//   - In-document `$ref` (`#/...`) with cycle detection
//   - `allOf` (compatible object merge), `anyOf` / `oneOf` (selecting a branch that passes A03 validation)
//
// Unsupported constructs (such as arbitrary regex `pattern` without an explicit
// example/const/enum, external `$ref`, recursive `$ref`, or unsatisfiable
// `not`/`oneOf`) return a *SchemaExampleError requiring an explicit example.
func GenerateSchemaExample(schemaMap map[string]any, rootDoc map[string]any, seed int64) ([]byte, error) {
	if len(schemaMap) == 0 {
		return nil, &SchemaExampleError{
			SchemaPath: "#",
			Reason:     "schema is empty",
		}
	}
	if rootDoc == nil {
		rootDoc = schemaMap
	}
	if seed == 0 {
		seed = 1
	}

	gen := &schemaSynthesizer{
		rootDoc:    rootDoc,
		seed:       seed,
		activeRefs: map[string]bool{},
		validator:  core.NewSchemaValidator(),
	}
	val, err := gen.synthesize(schemaMap, "#")
	if err != nil {
		return nil, err
	}

	exampleBytes, err := json.MarshalIndent(val, "", "  ")
	if err != nil {
		return nil, &SchemaExampleError{
			SchemaPath: "#",
			Reason:     fmt.Sprintf("marshal synthesized example: %v", err),
		}
	}
	schemaBytes, err := json.Marshal(schemaMap)
	if err != nil {
		return nil, &SchemaExampleError{
			SchemaPath: "#",
			Reason:     fmt.Sprintf("marshal schema: %v", err),
		}
	}

	// Mandatory A03 validation gate
	valRes, verr := gen.validator.ValidateInstance(schemaBytes, exampleBytes, core.SchemaValidationOptions{
		RootDocument:       rootDoc,
		AllowOAS30Nullable: true,
		ValidateFormats:    true,
	})
	if verr != nil {
		return nil, &SchemaExampleError{
			SchemaPath: "#",
			Reason:     fmt.Sprintf("A03 schema validation failed: %v", verr),
		}
	}
	if !valRes.Valid {
		firstMsg := "synthesized value did not satisfy schema constraints"
		firstPath := "#"
		firstKw := ""
		if len(valRes.Violations) > 0 {
			v := valRes.Violations[0]
			firstMsg = v.Message
			firstPath = v.SchemaPath
			firstKw = v.Keyword
		}
		return nil, &SchemaExampleError{
			SchemaPath: firstPath,
			Keyword:    firstKw,
			Reason:     firstMsg,
		}
	}
	return exampleBytes, nil
}

type schemaSynthesizer struct {
	rootDoc    map[string]any
	seed       int64
	activeRefs map[string]bool
	depth      int
	validator  core.SchemaValidatorPort
}

func (g *schemaSynthesizer) synthesize(node any, schemaPath string) (any, error) {
	g.depth++
	defer func() { g.depth-- }()
	if g.depth > 32 {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath,
			Reason:     "schema nesting depth exceeds synthesis limit (32)",
		}
	}

	switch s := node.(type) {
	case bool:
		if !s {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath,
				Keyword:    "booleanSchema",
				Reason:     "false schema disallows all values",
			}
		}
		return map[string]any{}, nil
	case map[string]any:
		return g.synthesizeMap(s, schemaPath)
	default:
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath,
			Reason:     fmt.Sprintf("invalid schema node type %T", node),
		}
	}
}

func (g *schemaSynthesizer) synthesizeMap(s map[string]any, schemaPath string) (any, error) {
	// Reject unsupported keywords that require custom domain examples unless an explicit example/const/enum is present
	if _, hasNot := s["not"]; hasNot && !hasExplicitValue(s) {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/not",
			Keyword:    "not",
			Reason:     "'not' schema synthesis requires an explicit example, default, const, or enum",
		}
	}

	// 1. Resolve in-document $ref
	if refRaw, ok := s["$ref"].(string); ok && strings.TrimSpace(refRaw) != "" {
		ref := strings.TrimSpace(refRaw)
		if !strings.HasPrefix(ref, "#/") {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/$ref",
				Keyword:    "$ref",
				Reason:     fmt.Sprintf("only in-document JSON Pointer $ref ('#/...') is supported for automatic synthesis (got %q)", ref),
			}
		}
		if g.activeRefs[ref] {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/$ref",
				Keyword:    "$ref",
				Reason:     fmt.Sprintf("recursive $ref cycle at %q cannot be synthesized automatically", ref),
			}
		}
		target, err := resolveInDocPointer(g.rootDoc, ref[1:])
		if err != nil {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/$ref",
				Keyword:    "$ref",
				Reason:     err.Error(),
			}
		}
		g.activeRefs[ref] = true
		defer delete(g.activeRefs, ref)
		return g.synthesize(target, ref)
	}

	// 2. Explicit const / enum / example / default
	if constVal, ok := s["const"]; ok {
		return constVal, nil
	}
	if enumSlice, ok := s["enum"].([]any); ok && len(enumSlice) > 0 {
		return enumSlice[0], nil
	}
	if exVal, ok := s["example"]; ok && exVal != nil {
		return exVal, nil
	}
	if exSlice, ok := s["examples"].([]any); ok && len(exSlice) > 0 && exSlice[0] != nil {
		return exSlice[0], nil
	}
	if defVal, ok := s["default"]; ok && defVal != nil {
		return defVal, nil
	}

	// 3. Combinators: allOf, oneOf, anyOf
	if allOf, ok := s["allOf"].([]any); ok && len(allOf) > 0 {
		return g.synthesizeAllOf(s, allOf, schemaPath)
	}
	if oneOf, ok := s["oneOf"].([]any); ok && len(oneOf) > 0 {
		return g.synthesizeBranchList(s, oneOf, schemaPath, "oneOf")
	}
	if anyOf, ok := s["anyOf"].([]any); ok && len(anyOf) > 0 {
		return g.synthesizeBranchList(s, anyOf, schemaPath, "anyOf")
	}

	// 4. Determine target type
	targetType := primarySchemaType(s)
	switch targetType {
	case "object":
		return g.synthesizeObject(s, schemaPath)
	case "array":
		return g.synthesizeArray(s, schemaPath)
	case "string":
		return g.synthesizeString(s, schemaPath)
	case "integer":
		return g.synthesizeNumber(s, schemaPath, true)
	case "number":
		return g.synthesizeNumber(s, schemaPath, false)
	case "boolean":
		return true, nil
	case "null":
		return nil, nil
	default:
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath,
			Keyword:    "type",
			Reason:     "schema must declare a supported type (object, array, string, integer, number, boolean, null) or an explicit example/const/enum",
		}
	}
}

func (g *schemaSynthesizer) synthesizeAllOf(parent map[string]any, allOf []any, schemaPath string) (any, error) {
	mergedObj := map[string]any{}
	for i, branch := range allOf {
		val, err := g.synthesize(branch, fmt.Sprintf("%s/allOf/%d", schemaPath, i))
		if err != nil {
			return nil, err
		}
		m, ok := val.(map[string]any)
		if !ok {
			if len(allOf) == 1 {
				return val, nil
			}
			return nil, &SchemaExampleError{
				SchemaPath: fmt.Sprintf("%s/allOf/%d", schemaPath, i),
				Keyword:    "allOf",
				Reason:     "multi-branch allOf synthesis only supports object schemas",
			}
		}
		for k, v := range m {
			mergedObj[k] = v
		}
	}
	if _, hasProps := parent["properties"]; hasProps {
		parentCopy := make(map[string]any, len(parent))
		for k, v := range parent {
			if k != "allOf" {
				parentCopy[k] = v
			}
		}
		ownVal, err := g.synthesizeObject(parentCopy, schemaPath)
		if err != nil {
			return nil, err
		}
		if ownMap, ok := ownVal.(map[string]any); ok {
			for k, v := range ownMap {
				mergedObj[k] = v
			}
		}
	}
	return mergedObj, nil
}

func (g *schemaSynthesizer) synthesizeBranchList(parent map[string]any, branches []any, schemaPath string, kw string) (any, error) {
	parentBytes, _ := json.Marshal(parent)
	var lastErr error
	for i, branch := range branches {
		val, err := g.synthesize(branch, fmt.Sprintf("%s/%s/%d", schemaPath, kw, i))
		if err != nil {
			lastErr = err
			continue
		}
		instBytes, merr := json.Marshal(val)
		if merr != nil {
			continue
		}
		res, verr := g.validator.ValidateInstance(parentBytes, instBytes, core.SchemaValidationOptions{
			RootDocument:       g.rootDoc,
			AllowOAS30Nullable: true,
			ValidateFormats:    true,
		})
		if verr == nil && res != nil && res.Valid {
			return val, nil
		}
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, &SchemaExampleError{
		SchemaPath: schemaPath + "/" + kw,
		Keyword:    kw,
		Reason:     fmt.Sprintf("could not synthesize a candidate satisfying %s constraints", kw),
	}
}

func (g *schemaSynthesizer) synthesizeObject(s map[string]any, schemaPath string) (any, error) {
	if _, hasPatProps := s["patternProperties"]; hasPatProps {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/patternProperties",
			Keyword:    "patternProperties",
			Reason:     "patternProperties requires an explicit example",
		}
	}
	props, _ := s["properties"].(map[string]any)
	requiredSet := map[string]bool{}
	if reqSlice, ok := s["required"].([]any); ok {
		for _, item := range reqSlice {
			if str, ok := item.(string); ok {
				requiredSet[str] = true
			}
		}
	} else if reqStrs, ok := s["required"].([]string); ok {
		for _, str := range reqStrs {
			requiredSet[str] = true
		}
	}

	keys := make([]string, 0, len(props)+len(requiredSet))
	seenKeys := map[string]bool{}
	for k := range props {
		keys = append(keys, k)
		seenKeys[k] = true
	}
	for k := range requiredSet {
		if !seenKeys[k] {
			keys = append(keys, k)
			seenKeys[k] = true
		}
	}
	sort.Strings(keys)

	maxProps, hasMaxProps := schemaInt(s["maxProperties"])
	minProps, _ := schemaInt(s["minProperties"])
	if hasMaxProps && len(requiredSet) > maxProps {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/maxProperties",
			Keyword:    "maxProperties",
			Reason:     fmt.Sprintf("required properties count (%d) exceeds maxProperties (%d)", len(requiredSet), maxProps),
		}
	}

	out := map[string]any{}
	// First synthesize all required properties, then optional properties up to maxProperties
	for _, k := range keys {
		if !requiredSet[k] {
			continue
		}
		propSchema, hasSchema := props[k]
		if !hasSchema {
			if addlMap, ok := s["additionalProperties"].(map[string]any); ok {
				propSchema = addlMap
				hasSchema = true
			}
		}
		if !hasSchema {
			out[k] = "example-" + k
			continue
		}
		val, err := g.synthesize(propSchema, schemaPath+"/properties/"+k)
		if err != nil {
			return nil, err
		}
		out[k] = val
	}
	for _, k := range keys {
		if requiredSet[k] {
			continue
		}
		if hasMaxProps && len(out) >= maxProps {
			break
		}
		propSchema := props[k]
		val, err := g.synthesize(propSchema, schemaPath+"/properties/"+k)
		if err != nil {
			return nil, err
		}
		out[k] = val
	}

	if len(out) < minProps {
		if addlBool, ok := s["additionalProperties"].(bool); ok && !addlBool {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/minProperties",
				Keyword:    "minProperties",
				Reason:     fmt.Sprintf("minProperties (%d) exceeds defined properties (%d) while additionalProperties is false", minProps, len(out)),
			}
		}
		addlSchema, hasAddlSchema := s["additionalProperties"].(map[string]any)
		for idx := 1; len(out) < minProps && idx <= 32; idx++ {
			extraKey := fmt.Sprintf("extra_%d", idx)
			if _, exists := out[extraKey]; exists {
				continue
			}
			if hasAddlSchema {
				v, err := g.synthesize(addlSchema, schemaPath+"/additionalProperties")
				if err != nil {
					return nil, err
				}
				out[extraKey] = v
			} else {
				out[extraKey] = fmt.Sprintf("value_%d", idx)
			}
		}
	}
	return out, nil
}

func (g *schemaSynthesizer) synthesizeArray(s map[string]any, schemaPath string) (any, error) {
	if _, hasContains := s["contains"]; hasContains {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/contains",
			Keyword:    "contains",
			Reason:     "'contains' array constraint requires an explicit example",
		}
	}
	minItems, _ := schemaInt(s["minItems"])
	maxItems, hasMax := schemaInt(s["maxItems"])
	if hasMax && minItems > maxItems {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/minItems",
			Keyword:    "minItems",
			Reason:     fmt.Sprintf("minItems (%d) exceeds maxItems (%d)", minItems, maxItems),
		}
	}
	if minItems > 16 {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/minItems",
			Keyword:    "minItems",
			Reason:     fmt.Sprintf("minItems (%d) exceeds synthesis bound (16)", minItems),
		}
	}
	count := 1
	if minItems > count {
		count = minItems
	}
	if hasMax && count > maxItems {
		count = maxItems
	}

	var prefixSchemas []any
	if ps, ok := s["prefixItems"].([]any); ok {
		prefixSchemas = ps
		if len(prefixSchemas) > count && (!hasMax || len(prefixSchemas) <= maxItems) {
			count = len(prefixSchemas)
		}
	}
	itemsSchema := s["items"]
	unique, _ := s["uniqueItems"].(bool)

	out := make([]any, 0, count)
	for i := 0; i < count; i++ {
		var elemSchema any
		elemPath := schemaPath + "/items"
		if i < len(prefixSchemas) {
			elemSchema = prefixSchemas[i]
			elemPath = fmt.Sprintf("%s/prefixItems/%d", schemaPath, i)
		} else if itemsSchema != nil {
			elemSchema = itemsSchema
		}
		if elemSchema == nil {
			out = append(out, fmt.Sprintf("item-%d", i+1))
			continue
		}
		val, err := g.synthesize(elemSchema, elemPath)
		if err != nil {
			return nil, err
		}
		if unique && i > 0 {
			switch v := val.(type) {
			case string:
				if m, ok := elemSchema.(map[string]any); ok && m["format"] == nil && m["enum"] == nil && m["const"] == nil {
					val = fmt.Sprintf("%s-%d", v, i+1)
				}
			case int64:
				val = v + int64(i)
			case float64:
				val = v + float64(i)
			}
		}
		out = append(out, val)
	}
	return out, nil
}

func (g *schemaSynthesizer) synthesizeString(s map[string]any, schemaPath string) (any, error) {
	if pat, ok := s["pattern"].(string); ok && strings.TrimSpace(pat) != "" {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/pattern",
			Keyword:    "pattern",
			Reason:     fmt.Sprintf("arbitrary regex pattern %q cannot be synthesized deterministically without an explicit example, const, or enum", pat),
		}
	}
	minLen, _ := schemaInt(s["minLength"])
	maxLen, hasMax := schemaInt(s["maxLength"])
	if hasMax && minLen > maxLen {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/minLength",
			Keyword:    "minLength",
			Reason:     fmt.Sprintf("minLength (%d) exceeds maxLength (%d)", minLen, maxLen),
		}
	}
	if minLen > 4096 {
		return nil, &SchemaExampleError{
			SchemaPath: schemaPath + "/minLength",
			Keyword:    "minLength",
			Reason:     fmt.Sprintf("minLength (%d) exceeds synthesis limit (4096)", minLen),
		}
	}

	if fmtName, ok := s["format"].(string); ok && strings.TrimSpace(fmtName) != "" {
		f := strings.ToLower(strings.TrimSpace(fmtName))
		var formatted string
		switch f {
		case "email":
			formatted = "user@example.com"
		case "uri", "url":
			formatted = "https://example.com/resource"
		case "uuid":
			rng := seededRNG(g.seed, 1, schemaPath)
			formatted = deterministicUUID(rng)
		case "date":
			formatted = "2026-01-02"
		case "date-time":
			formatted = "2026-01-02T15:04:05Z"
		case "ipv4":
			formatted = "127.0.0.1"
		case "ipv6":
			formatted = "::1"
		default:
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/format",
				Keyword:    "format",
				Reason:     fmt.Sprintf("unsupported string format %q requires an explicit example", fmtName),
			}
		}
		if len(formatted) < minLen || (hasMax && len(formatted) > maxLen) {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/format",
				Keyword:    "format",
				Reason:     fmt.Sprintf("format %q example length (%d) conflicts with minLength/maxLength constraints", fmtName, len(formatted)),
			}
		}
		return formatted, nil
	}

	base := "example"
	if hasMax && maxLen < len(base) {
		base = base[:maxLen]
	}
	if len(base) < minLen {
		pad := strings.Repeat("x", minLen-len(base))
		base += pad
	}
	return base, nil
}

func (g *schemaSynthesizer) synthesizeNumber(s map[string]any, schemaPath string, isInt bool) (any, error) {
	step := 1.0
	if !isInt {
		step = 0.5
	}
	if mult, ok := schemaFloat(s["multipleOf"]); ok && mult > 0 {
		if isInt && stdmath.Abs(mult-stdmath.Round(mult)) > 1e-9 {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath + "/multipleOf",
				Keyword:    "multipleOf",
				Reason:     fmt.Sprintf("integer schema cannot satisfy non-integer multipleOf (%v)", mult),
			}
		}
		step = mult
	}

	hasLower := false
	lower := 0.0
	lowerExclusive := false

	if minVal, ok := schemaFloat(s["minimum"]); ok {
		hasLower = true
		lower = minVal
		if exclBool, ok := s["exclusiveMinimum"].(bool); ok && exclBool {
			lowerExclusive = true
		}
	}
	if exclMin, ok := schemaFloat(s["exclusiveMinimum"]); ok {
		if !hasLower || exclMin > lower || (exclMin == lower && !lowerExclusive) {
			hasLower = true
			lower = exclMin
			lowerExclusive = true
		}
	}

	hasUpper := false
	upper := 0.0
	upperExclusive := false
	if maxVal, ok := schemaFloat(s["maximum"]); ok {
		hasUpper = true
		upper = maxVal
		if exclBool, ok := s["exclusiveMaximum"].(bool); ok && exclBool {
			upperExclusive = true
		}
	}
	if exclMax, ok := schemaFloat(s["exclusiveMaximum"]); ok {
		if !hasUpper || exclMax < upper || (exclMax == upper && !upperExclusive) {
			hasUpper = true
			upper = exclMax
			upperExclusive = true
		}
	}

	candidate := 1.0
	if hasLower {
		candidate = lower
		if lowerExclusive {
			candidate += step
		}
	} else if hasUpper && candidate >= upper {
		candidate = upper
		if upperExclusive {
			candidate -= step
		}
	}

	if mult, ok := schemaFloat(s["multipleOf"]); ok && mult > 0 {
		q := stdmath.Ceil(candidate / mult)
		candidate = q * mult
		if hasLower && lowerExclusive && candidate <= lower {
			candidate += mult
		}
	}
	if isInt {
		candidate = stdmath.Ceil(candidate)
		if hasLower && lowerExclusive && candidate <= lower {
			candidate += 1
		}
	}

	if hasLower {
		if (lowerExclusive && !(candidate > lower)) || (!lowerExclusive && candidate < lower) {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath,
				Keyword:    "minimum",
				Reason:     "unsatisfiable numeric minimum constraints",
			}
		}
	}
	if hasUpper {
		if (upperExclusive && !(candidate < upper)) || (!upperExclusive && candidate > upper) {
			return nil, &SchemaExampleError{
				SchemaPath: schemaPath,
				Keyword:    "maximum",
				Reason:     "unsatisfiable numeric maximum/minimum range",
			}
		}
	}

	if isInt {
		return int64(stdmath.Round(candidate)), nil
	}
	return candidate, nil
}

// BuildRuleFromOperationContract generates a MockRule for a request's OpenAPI
// OperationContract by synthesizing a deterministic, A03-validated response
// example for the target status code (default "200").
func BuildRuleFromOperationContract(req *core.Request, statusKey string, priority int, seed int64) (*MockRule, error) {
	if req == nil || req.HTTP == nil {
		return nil, &core.ConfigError{Err: fmt.Errorf("request has no HTTP definition")}
	}
	contract, err := core.GetRequestOpenAPI(req)
	if err != nil {
		return nil, err
	}
	if contract == nil || len(contract.Responses) == 0 {
		return nil, &SchemaExampleError{
			SchemaPath: "#/responses",
			Reason:     fmt.Sprintf("request %q has no OpenAPI response contract configured", req.ID),
		}
	}

	statusKey = strings.TrimSpace(statusKey)
	if statusKey == "" {
		statusKey = "200"
	}
	rc, ok := contract.Responses[statusKey]
	if !ok {
		keys := make([]string, 0, len(contract.Responses))
		for k := range contract.Responses {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		statusKey = keys[0]
		rc = contract.Responses[statusKey]
	}

	statusCode, err := strconv.Atoi(statusKey)
	if err != nil || statusCode < 100 || statusCode > 599 {
		statusCode = http.StatusOK
	}

	mt, ok := rc.Content["application/json"]
	if !ok {
		for k, v := range rc.Content {
			if strings.Contains(strings.ToLower(k), "json") {
				mt = v
				ok = true
				break
			}
		}
	}
	if !ok || len(mt.Schema) == 0 {
		return nil, &SchemaExampleError{
			SchemaPath: fmt.Sprintf("#/responses/%s/content", statusKey),
			Reason:     fmt.Sprintf("response %q has no inline application/json schema", statusKey),
		}
	}

	exampleBytes, err := GenerateSchemaExample(mt.Schema, mt.Schema, seed)
	if err != nil {
		return nil, err
	}

	pathTmpl := strings.TrimSpace(contract.PathTemplate)
	if pathTmpl == "" {
		pathTmpl = extractMockPathTemplate(req.HTTP.URL)
	} else {
		pathTmpl = normalizeURLPath(pathTmpl)
	}
	method := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
	if method == "" {
		method = http.MethodGet
	}
	if priority == 0 {
		priority = 100
	}
	ruleID := sanitizeIDSlug(contract.OperationID)
	if ruleID == "" {
		ruleID = sanitizeIDSlug(req.ID)
	}
	if ruleID == "" {
		ruleID = "schema-rule"
	}

	rule := &MockRule{
		ID:          ruleID,
		Name:        firstNonEmpty(contract.Summary, req.Name, method+" "+pathTmpl),
		Description: fmt.Sprintf("Generated from OpenAPI response schema (%s) and validated via A03", statusKey),
		Priority:    priority,
		Match: RuleMatch{
			Method: method,
			Path:   pathTmpl,
		},
		Response: RuleResponse{
			Status:      statusCode,
			ContentType: "application/json; charset=utf-8",
			Body:        string(exampleBytes),
		},
		Variants: defaultErrorVariants(),
	}
	if err := validateMockRule(rule, ServerLimits{}); err != nil {
		return nil, err
	}
	return rule, nil
}

func primarySchemaType(s map[string]any) string {
	switch t := s["type"].(type) {
	case string:
		return strings.ToLower(strings.TrimSpace(t))
	case []any:
		for _, item := range t {
			if str, ok := item.(string); ok && str != "null" {
				return strings.ToLower(strings.TrimSpace(str))
			}
		}
		if len(t) > 0 {
			if str, ok := t[0].(string); ok {
				return strings.ToLower(strings.TrimSpace(str))
			}
		}
	case []string:
		for _, str := range t {
			if str != "null" {
				return strings.ToLower(strings.TrimSpace(str))
			}
		}
	}
	if _, hasProps := s["properties"]; hasProps {
		return "object"
	}
	if _, hasReq := s["required"]; hasReq {
		return "object"
	}
	if _, hasItems := s["items"]; hasItems {
		return "array"
	}
	if _, hasPrefix := s["prefixItems"]; hasPrefix {
		return "array"
	}
	return ""
}

func hasExplicitValue(s map[string]any) bool {
	if _, ok := s["const"]; ok {
		return true
	}
	if e, ok := s["enum"].([]any); ok && len(e) > 0 {
		return true
	}
	if e, ok := s["example"]; ok && e != nil {
		return true
	}
	if d, ok := s["default"]; ok && d != nil {
		return true
	}
	return false
}

func resolveInDocPointer(root map[string]any, pointer string) (any, error) {
	if root == nil {
		return nil, fmt.Errorf("root document is nil")
	}
	if pointer == "" || pointer == "/" {
		return root, nil
	}
	parts := strings.Split(strings.TrimPrefix(pointer, "/"), "/")
	var cur any = root
	for _, rawTok := range parts {
		tok := strings.ReplaceAll(rawTok, "~1", "/")
		tok = strings.ReplaceAll(tok, "~0", "~")
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, fmt.Errorf("JSON pointer #%s not found at %q", pointer, tok)
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, fmt.Errorf("JSON pointer #%s invalid array index %q", pointer, tok)
			}
			cur = node[idx]
		default:
			return nil, fmt.Errorf("JSON pointer #%s cannot traverse %T", pointer, cur)
		}
	}
	return cur, nil
}

func schemaInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		return int(n), true
	case json.Number:
		i, err := n.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func schemaFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}
