package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

// Matcher is the shared comparison primitive used by header, JSONPath and body
// assertions. Empty fields are inactive; every configured matcher must pass.
type Matcher struct {
	Exists      *bool
	Equals      *literal
	NotEquals   *literal
	In          []literal
	Contains    *string
	NotContains *string
	Regex       string
	regex       *regexp.Regexp
	Type        *string
}

type literal struct{ v any }

type assertionTarget int

const (
	targetStatus assertionTarget = iota
	targetHeader
	targetJSONPath
	targetBody
	targetDuration
	targetSize
	targetSchema
)

// Assertion is one typed request/step assertion parsed from assert: entries.
type Assertion struct {
	Message  string
	target   assertionTarget
	status   *statusExpectation
	header   *headerExpectation
	jsonPath *jsonPathExpectation
	body     *matcherExpectation
	duration *numberExpectation
	size     *numberExpectation
	schema   *schemaExpectation
}

type statusExpectation struct {
	equals *int
	in     []int
}

type headerExpectation struct {
	name    string
	matcher Matcher
}

type jsonPathExpectation struct {
	path    string
	query   *jsonPathQuery
	matcher Matcher
}

type matcherExpectation struct{ matcher Matcher }

type numberExpectation struct {
	equals *float64
	lt     *float64
	lte    *float64
	gt     *float64
	gte    *float64
}

type schemaExpectation struct {
	ref       string
	operation string
}

var assertionTargetKeys = map[string]assertionTarget{
	"status":     targetStatus,
	"header":     targetHeader,
	"jsonpath":   targetJSONPath,
	"body":       targetBody,
	"durationMs": targetDuration,
	"size":       targetSize,
	"schema":     targetSchema,
}

var matcherKeys = map[string]bool{
	"exists": true, "equals": true, "notEquals": true, "in": true,
	"contains": true, "notContains": true, "regex": true, "type": true,
}

var numberKeys = map[string]bool{"equals": true, "lt": true, "lte": true, "gt": true, "gte": true}

// parseAssertions converts raw assert: nodes into typed assertions. Unknown
// keys, missing targets and ambiguous matchers are config errors.
func parseAssertions(nodes []yaml.Node) ([]Assertion, error) {
	out := make([]Assertion, 0, len(nodes))
	for i := range nodes {
		a, err := parseAssertion(&nodes[i])
		if err != nil {
			return nil, fmt.Errorf("assertion %d: %w", i+1, err)
		}
		out = append(out, a)
	}
	return out, nil
}

func parseAssertion(n *yaml.Node) (Assertion, error) {
	var a Assertion
	if n == nil {
		return a, fmt.Errorf("empty assertion")
	}
	if n.Kind == yaml.DocumentNode && len(n.Content) > 0 {
		n = n.Content[0]
	}
	if n.Kind != yaml.MappingNode {
		return a, fmt.Errorf("assertion must be a mapping")
	}
	targets := 0
	seen := map[string]bool{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		if seen[key] {
			return a, fmt.Errorf("duplicate key %q", key)
		}
		seen[key] = true
		switch key {
		case "message":
			if err := scalarString(val, &a.Message); err != nil {
				return a, err
			}
		case "status":
			targets++
			se, err := parseStatusExpectation(val)
			if err != nil {
				return a, err
			}
			a.target = targetStatus
			a.status = se
		case "header":
			targets++
			var name string
			if err := scalarString(val, &name); err != nil {
				return a, fmt.Errorf("header: %w", err)
			}
			if strings.TrimSpace(name) == "" {
				return a, fmt.Errorf("header name is empty")
			}
			a.target = targetHeader
			a.header = &headerExpectation{name: name}
		case "jsonpath":
			targets++
			var path string
			if err := scalarString(val, &path); err != nil {
				return a, fmt.Errorf("jsonpath: %w", err)
			}
			q, err := parseJSONPath(path)
			if err != nil {
				return a, err
			}
			a.target = targetJSONPath
			a.jsonPath = &jsonPathExpectation{path: path, query: q}
		case "body":
			targets++
			if val.Kind != yaml.MappingNode {
				return a, fmt.Errorf("body assertion must be a mapping of matchers")
			}
			m, err := parseMatcher(val, true)
			if err != nil {
				return a, fmt.Errorf("body: %w", err)
			}
			a.target = targetBody
			a.body = &matcherExpectation{matcher: m}
		case "durationMs", "size":
			targets++
			ne, err := parseNumberExpectation(val)
			if err != nil {
				return a, fmt.Errorf("%s: %w", key, err)
			}
			if key == "durationMs" {
				a.target = targetDuration
				a.duration = ne
			} else {
				a.target = targetSize
				a.size = ne
			}
		case "schema":
			targets++
			se, err := parseSchemaExpectation(val)
			if err != nil {
				return a, fmt.Errorf("schema: %w", err)
			}
			a.target = targetSchema
			a.schema = se
		default:
			if !matcherKeys[key] {
				return a, fmt.Errorf("unknown assertion key %q", key)
			}
		}
	}
	if targets != 1 {
		return a, fmt.Errorf("assertion must have exactly one target (status, header, jsonpath, body, durationMs, size, schema), found %d", targets)
	}
	switch a.target {
	case targetHeader:
		m, err := parseMatcher(n, false)
		if err != nil {
			return a, err
		}
		a.header.matcher = m
	case targetJSONPath:
		m, err := parseMatcher(n, false)
		if err != nil {
			return a, err
		}
		a.jsonPath.matcher = m
	}
	return a, nil
}

func parseMatcher(n *yaml.Node, allKeys bool) (Matcher, error) {
	var m Matcher
	count := 0
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		if key == "header" || key == "jsonpath" || key == "message" || key == "status" ||
			key == "durationMs" || key == "size" || key == "schema" || key == "body" {
			continue
		}
		if !matcherKeys[key] {
			if allKeys {
				return m, fmt.Errorf("unknown matcher %q", key)
			}
			continue
		}
		count++
		switch key {
		case "exists":
			var b bool
			if err := scalarBool(val, &b); err != nil {
				return m, err
			}
			m.Exists = &b
		case "equals", "notEquals":
			lit, err := parseLiteral(val)
			if err != nil {
				return m, err
			}
			if key == "equals" {
				m.Equals = &lit
			} else {
				m.NotEquals = &lit
			}
		case "in":
			if val.Kind != yaml.SequenceNode || len(val.Content) == 0 {
				return m, fmt.Errorf("in must be a non-empty sequence")
			}
			for _, c := range val.Content {
				lit, err := parseLiteral(c)
				if err != nil {
					return m, err
				}
				m.In = append(m.In, lit)
			}
		case "contains", "notContains":
			var s string
			if err := scalarString(val, &s); err != nil {
				return m, err
			}
			if key == "contains" {
				m.Contains = &s
			} else {
				m.NotContains = &s
			}
		case "regex":
			var s string
			if err := scalarString(val, &s); err != nil {
				return m, err
			}
			re, err := regexp.Compile(s)
			if err != nil {
				return m, fmt.Errorf("invalid regex %q: %v", s, err)
			}
			m.Regex = s
			m.regex = re
		case "type":
			var s string
			if err := scalarString(val, &s); err != nil {
				return m, err
			}
			switch s = strings.ToLower(strings.TrimSpace(s)); s {
			case "string", "number", "boolean", "object", "array", "null":
				m.Type = &s
			default:
				return m, fmt.Errorf("unknown type %q (want string, number, boolean, object, array or null)", s)
			}
		}
	}
	if count == 0 {
		return m, fmt.Errorf("missing matcher (exists, equals, notEquals, in, contains, notContains, regex or type)")
	}
	if m.Exists != nil && !*m.Exists && count > 1 {
		return m, fmt.Errorf("exists: false cannot be combined with other matchers")
	}
	return m, nil
}

func parseStatusExpectation(n *yaml.Node) (*statusExpectation, error) {
	if n.Kind == yaml.ScalarNode {
		var v int
		if err := n.Decode(&v); err != nil {
			return nil, fmt.Errorf("status must be an integer or {equals|in}: %v", err)
		}
		return &statusExpectation{equals: &v}, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("status must be an integer or {equals|in}")
	}
	se := &statusExpectation{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		switch key {
		case "equals":
			var v int
			if err := val.Decode(&v); err != nil {
				return nil, fmt.Errorf("status.equals must be an integer")
			}
			se.equals = &v
		case "in":
			if val.Kind != yaml.SequenceNode || len(val.Content) == 0 {
				return nil, fmt.Errorf("status.in must be a non-empty sequence")
			}
			for _, c := range val.Content {
				var v int
				if err := c.Decode(&v); err != nil {
					return nil, fmt.Errorf("status.in values must be integers")
				}
				se.in = append(se.in, v)
			}
		default:
			return nil, fmt.Errorf("unknown status key %q", key)
		}
	}
	if se.equals == nil && len(se.in) == 0 {
		return nil, fmt.Errorf("status assertion needs equals or in")
	}
	return se, nil
}

func parseNumberExpectation(n *yaml.Node) (*numberExpectation, error) {
	ne := &numberExpectation{}
	set := func(key string, val *yaml.Node) error {
		var f float64
		if err := val.Decode(&f); err != nil {
			return fmt.Errorf("%s must be a number", key)
		}
		switch key {
		case "equals":
			ne.equals = &f
		case "lt":
			ne.lt = &f
		case "lte":
			ne.lte = &f
		case "gt":
			ne.gt = &f
		case "gte":
			ne.gte = &f
		}
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		if err := set("equals", n); err != nil {
			return nil, err
		}
		return ne, nil
	}
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("must be a number or {equals|lt|lte|gt|gte} mapping")
	}
	count := 0
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		if !numberKeys[key] {
			return nil, fmt.Errorf("unknown key %q", key)
		}
		if err := set(key, n.Content[i+1]); err != nil {
			return nil, err
		}
		count++
	}
	if count == 0 {
		return nil, fmt.Errorf("no comparison configured")
	}
	return ne, nil
}

func parseSchemaExpectation(n *yaml.Node) (*schemaExpectation, error) {
	if n.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("schema must be a mapping with ref/operation")
	}
	se := &schemaExpectation{}
	for i := 0; i+1 < len(n.Content); i += 2 {
		key := n.Content[i].Value
		val := n.Content[i+1]
		switch key {
		case "ref":
			if err := scalarString(val, &se.ref); err != nil {
				return nil, err
			}
		case "operation":
			if err := scalarString(val, &se.operation); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("unknown schema key %q", key)
		}
	}
	if strings.TrimSpace(se.ref) == "" {
		return nil, fmt.Errorf("schema.ref is empty")
	}
	return se, nil
}

func parseLiteral(n *yaml.Node) (literal, error) {
	if n.Kind != yaml.ScalarNode {
		return literal{}, fmt.Errorf("expected a scalar value, got %s", nodeKindName(n.Kind))
	}
	var v any
	if err := n.Decode(&v); err != nil {
		return literal{}, err
	}
	switch v.(type) {
	case string, bool, int, int64, uint64, float64, nil:
		return literal{v: v}, nil
	default:
		return literal{}, fmt.Errorf("unsupported literal type %T", v)
	}
}

func scalarString(n *yaml.Node, out *string) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected a string, got %s", nodeKindName(n.Kind))
	}
	return n.Decode(out)
}

func scalarBool(n *yaml.Node, out *bool) error {
	if n.Kind != yaml.ScalarNode {
		return fmt.Errorf("expected a boolean, got %s", nodeKindName(n.Kind))
	}
	return n.Decode(out)
}

func nodeKindName(k yaml.Kind) string {
	switch k {
	case yaml.ScalarNode:
		return "scalar"
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	default:
		return fmt.Sprintf("kind %d", k)
	}
}

// evalContext caches the decoded JSON body for a single attempt.
type evalContext struct {
	result   *core.ExecResult
	red      *core.Redactor
	resolver *core.Resolver
	schema   SchemaValidator

	// onSensitive registers a sensitive extracted value with the run-level
	// redactor so reports and later diagnostics can never echo it.
	onSensitive func(string)

	bodyDoc    any
	bodyParsed bool
	bodyErr    error
}

func (c *evalContext) jsonDoc() (any, error) {
	if c.bodyParsed {
		return c.bodyDoc, c.bodyErr
	}
	c.bodyParsed = true
	dec := json.NewDecoder(bytes.NewReader(c.result.Body))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		c.bodyErr = fmt.Errorf("response body is not valid JSON: %v", err)
		return nil, c.bodyErr
	}
	var extra any
	if err := dec.Decode(&extra); err == nil {
		c.bodyErr = fmt.Errorf("response body contains multiple JSON documents")
		return nil, c.bodyErr
	}
	c.bodyDoc = v
	return v, nil
}

// evaluateAssertions evaluates every assertion, reporting all results.
// redactAssertionResults is applied after sensitive extraction values and
// response credential headers are registered. Assertion actual/expected fields
// must never retain a secret that was discovered later in the same attempt.
func redactAssertionResults(results []AssertionResult, redact func(string) string) []AssertionResult {
	if redact == nil || len(results) == 0 {
		return results
	}
	out := append([]AssertionResult(nil), results...)
	for i := range out {
		out[i].Message = redact(out[i].Message)
		out[i].Expected = redact(out[i].Expected)
		out[i].Actual = redact(out[i].Actual)
	}
	return out
}

func evaluateAssertions(asserts []Assertion, ctx *evalContext) ([]AssertionResult, *Failure) {
	results := make([]AssertionResult, 0, len(asserts))
	var first *Failure
	for _, a := range asserts {
		res, fail := evaluateAssertion(a, ctx)
		results = append(results, res)
		if fail != nil && first == nil {
			first = fail
		}
	}
	return results, first
}

func evaluateAssertion(a Assertion, ctx *evalContext) (AssertionResult, *Failure) {
	switch a.target {
	case targetStatus:
		return evalStatus(a, ctx)
	case targetHeader:
		return evalHeader(a, ctx)
	case targetJSONPath:
		return evalJSONPath(a, ctx)
	case targetBody:
		return evalBody(a, ctx)
	case targetDuration:
		return evalNumber("duration", "durationMs", float64(ctx.result.Duration)/float64(1e6), a.duration, a.Message)
	case targetSize:
		return evalNumber("size", "size", float64(ctx.result.BodySize), a.size, a.Message)
	case targetSchema:
		return evalSchema(a, ctx)
	default:
		return AssertionResult{Kind: "unknown", Message: "unsupported assertion target"}, newFailure(FailureAssertion, KindAssertion, "unsupported assertion target")
	}
}

func evalStatus(a Assertion, ctx *evalContext) (AssertionResult, *Failure) {
	got := ctx.result.Status
	res := AssertionResult{Kind: "status", Target: "status", Actual: strconv.Itoa(got)}
	var expected string
	if a.status.equals != nil {
		expected = strconv.Itoa(*a.status.equals)
		res.Expected = expected
		if got == *a.status.equals {
			res.Passed = true
			return res, nil
		}
	} else {
		parts := make([]string, len(a.status.in))
		for i, v := range a.status.in {
			parts[i] = strconv.Itoa(v)
			if v == got {
				res.Passed = true
			}
		}
		expected = "in [" + strings.Join(parts, ", ") + "]"
		res.Expected = expected
		if res.Passed {
			return res, nil
		}
	}
	msg := assertionMessage(a.Message, fmt.Sprintf("status: expected %s, got %d", expected, got))
	res.Message = ctx.redact(msg)
	return res, newFailure(FailureAssertion, KindAssertion, res.Message)
}

func evalHeader(a Assertion, ctx *evalContext) (AssertionResult, *Failure) {
	joined, present := lookupHeader(ctx.result.Headers, a.header.name)
	res := AssertionResult{Kind: "header", Target: a.header.name, Actual: ctx.redact(joined)}
	values := []any{}
	if present {
		values = append(values, joined)
	}
	if ok, msg := evalMatcher(&a.header.matcher, values, present, ctx); !ok {
		msg = assertionMessage(a.Message, "header "+a.header.name+": "+msg)
		res.Message = ctx.redact(msg)
		res.Expected = ctx.redact(describeMatcher(&a.header.matcher))
		return res, newFailure(FailureAssertion, KindAssertion, res.Message)
	}
	res.Passed = true
	res.Expected = ctx.redact(describeMatcher(&a.header.matcher))
	return res, nil
}

func evalJSONPath(a Assertion, ctx *evalContext) (AssertionResult, *Failure) {
	doc, err := ctx.jsonDoc()
	if err != nil {
		res := AssertionResult{Kind: "jsonpath", Target: a.jsonPath.path, Message: err.Error()}
		return res, newFailure(FailureAssertion, KindAssertion, err.Error())
	}
	values := a.jsonPath.query.Eval(doc)
	res := AssertionResult{Kind: "jsonpath", Target: a.jsonPath.path, Actual: ctx.redact(joinValues(values, 5))}
	res.Expected = ctx.redact(describeMatcher(&a.jsonPath.matcher))
	if ok, msg := evalMatcher(&a.jsonPath.matcher, values, len(values) > 0, ctx); !ok {
		msg = assertionMessage(a.Message, "jsonpath "+a.jsonPath.path+": "+msg)
		res.Message = ctx.redact(msg)
		return res, newFailure(FailureAssertion, KindAssertion, res.Message)
	}
	res.Passed = true
	return res, nil
}

func evalBody(a Assertion, ctx *evalContext) (AssertionResult, *Failure) {
	text := string(ctx.result.Body)
	res := AssertionResult{Kind: "body", Target: "body", Actual: ctx.redact(truncate(text, 200))}
	res.Expected = ctx.redact(describeMatcher(&a.body.matcher))
	if ok, msg := evalMatcher(&a.body.matcher, []any{text}, true, ctx); !ok {
		msg = assertionMessage(a.Message, "body: "+msg)
		res.Message = ctx.redact(msg)
		return res, newFailure(FailureAssertion, KindAssertion, res.Message)
	}
	res.Passed = true
	return res, nil
}

func evalNumber(kind, target string, actual float64, ne *numberExpectation, userMessage string) (AssertionResult, *Failure) {
	res := AssertionResult{Kind: kind, Target: target, Actual: formatFloat(actual)}
	var expected []string
	ok := true
	check := func(name string, want *float64, pass bool) {
		if want == nil {
			return
		}
		expected = append(expected, fmt.Sprintf("%s %s", name, formatFloat(*want)))
		if !pass {
			ok = false
		}
	}
	if ne.equals != nil {
		check("equals", ne.equals, actual == *ne.equals)
	}
	if ne.lt != nil {
		check("<", ne.lt, actual < *ne.lt)
	}
	if ne.lte != nil {
		check("<=", ne.lte, actual <= *ne.lte)
	}
	if ne.gt != nil {
		check(">", ne.gt, actual > *ne.gt)
	}
	if ne.gte != nil {
		check(">=", ne.gte, actual >= *ne.gte)
	}
	res.Expected = strings.Join(expected, " && ")
	if ok {
		res.Passed = true
		return res, nil
	}
	res.Message = assertionMessage(userMessage, fmt.Sprintf("%s: expected %s, got %s", kind, res.Expected, res.Actual))
	return res, newFailure(FailureAssertion, KindAssertion, res.Message)
}

func evalSchema(a Assertion, ctx *evalContext) (AssertionResult, *Failure) {
	res := AssertionResult{Kind: "schema", Target: "schema"}
	if ctx.schema == nil {
		res.Message = "schema assertion requires an injected schema validator"
		return res, newFailure(FailureConfig, KindSchema, res.Message)
	}
	issues, err := ctx.schema.ValidateSchema(ctx.result.Body, SchemaRef{
		Ref:       a.schema.ref,
		Operation: a.schema.operation,
		Status:    ctx.result.Status,
		Headers:   ctx.result.Headers,
	})
	if err != nil {
		msg := ctx.redact("schema validation failed to run: " + err.Error())
		res.Message = msg
		return res, newFailure(FailureConfig, KindSchema, msg)
	}
	if len(issues) == 0 {
		res.Passed = true
		return res, nil
	}
	limit := len(issues)
	if limit > 5 {
		limit = 5
	}
	parts := make([]string, 0, limit)
	for _, is := range issues[:limit] {
		if is.InstancePath != "" {
			parts = append(parts, is.InstancePath+": "+is.Message)
		} else {
			parts = append(parts, is.Message)
		}
	}
	res.Expected = "valid against selected schema"
	res.Actual = fmt.Sprintf("%d issue(s)", len(issues))
	res.Message = ctx.redact(assertionMessage(a.Message, "schema: "+strings.Join(parts, "; ")))
	return res, newFailure(FailureAssertion, KindSchema, res.Message)
}

func (c *evalContext) redact(s string) string {
	if c.red == nil {
		return s
	}
	return c.red.RedactString(s)
}

func (c *evalContext) resolveExpectedString(s string) (string, error) {
	if !strings.Contains(s, "{{") || c.resolver == nil {
		return s, nil
	}
	tv, err := c.resolver.ResolveTracked(s)
	if err != nil {
		return "", err
	}
	return tv.Value, nil
}

// evalMatcher applies m to values. present distinguishes "header/body exists
// with an empty value" from "path matched nothing".
func evalMatcher(m *Matcher, values []any, present bool, ctx *evalContext) (bool, string) {
	if m.Exists != nil {
		if *m.Exists != present {
			if *m.Exists {
				return false, "value is missing"
			}
			return false, "value unexpectedly exists"
		}
		if !*m.Exists {
			return true, ""
		}
	}
	if !present || len(values) == 0 {
		return false, "no value matched"
	}
	if len(m.In) > 0 {
		for _, v := range values {
			found := false
			for _, want := range m.In {
				if literalsEqual(v, want.v) {
					found = true
					break
				}
			}
			if !found {
				return false, fmt.Sprintf("value %s not in %s", formatValue(v), describeMatcher(m))
			}
		}
	}
	if m.Equals != nil {
		want, err := resolvedLiteral(*m.Equals, ctx)
		if err != nil {
			return false, "expected value cannot be resolved: " + err.Error()
		}
		for _, v := range values {
			if !valuesEqual(v, want) {
				return false, fmt.Sprintf("expected %s, got %s", formatValue(want), formatValue(v))
			}
		}
	}
	if m.NotEquals != nil {
		want, err := resolvedLiteral(*m.NotEquals, ctx)
		if err != nil {
			return false, "expected value cannot be resolved: " + err.Error()
		}
		for _, v := range values {
			if valuesEqual(v, want) {
				return false, fmt.Sprintf("value must not equal %s", formatValue(want))
			}
		}
	}
	if m.Contains != nil {
		needle, err := ctx.resolveExpectedString(*m.Contains)
		if err != nil {
			return false, "expected value cannot be resolved: " + err.Error()
		}
		for _, v := range values {
			s, ok := scalarStringValue(v)
			if !ok {
				return false, fmt.Sprintf("value %s is not comparable as text", formatValue(v))
			}
			if !strings.Contains(s, needle) {
				return false, fmt.Sprintf("value does not contain %q", ctx.redact(needle))
			}
		}
	}
	if m.NotContains != nil {
		needle, err := ctx.resolveExpectedString(*m.NotContains)
		if err != nil {
			return false, "expected value cannot be resolved: " + err.Error()
		}
		for _, v := range values {
			s, ok := scalarStringValue(v)
			if ok && strings.Contains(s, needle) {
				return false, fmt.Sprintf("value must not contain %q", ctx.redact(needle))
			}
		}
	}
	if m.regex != nil {
		for _, v := range values {
			s, ok := scalarStringValue(v)
			if !ok {
				return false, fmt.Sprintf("value %s is not comparable as text", formatValue(v))
			}
			if !m.regex.MatchString(s) {
				return false, fmt.Sprintf("value does not match /%s/", m.Regex)
			}
		}
	}
	if m.Type != nil {
		for _, v := range values {
			if valueTypeName(v) != *m.Type {
				return false, fmt.Sprintf("expected type %s, got %s", *m.Type, valueTypeName(v))
			}
		}
	}
	return true, ""
}

func resolvedLiteral(l literal, ctx *evalContext) (any, error) {
	s, ok := l.v.(string)
	if !ok || !strings.Contains(s, "{{") {
		return l.v, nil
	}
	return ctx.resolveExpectedString(s)
}

func describeMatcher(m *Matcher) string {
	if m == nil {
		return ""
	}
	var parts []string
	if m.Exists != nil {
		parts = append(parts, fmt.Sprintf("exists=%t", *m.Exists))
	}
	if m.Equals != nil {
		parts = append(parts, "equals "+formatValue(m.Equals.v))
	}
	if m.NotEquals != nil {
		parts = append(parts, "notEquals "+formatValue(m.NotEquals.v))
	}
	if len(m.In) > 0 {
		vals := make([]string, len(m.In))
		for i, l := range m.In {
			vals[i] = formatValue(l.v)
		}
		parts = append(parts, "in ["+strings.Join(vals, ", ")+"]")
	}
	if m.Contains != nil {
		parts = append(parts, "contains "+strconv.Quote(*m.Contains))
	}
	if m.NotContains != nil {
		parts = append(parts, "notContains "+strconv.Quote(*m.NotContains))
	}
	if m.Regex != "" {
		parts = append(parts, "regex /"+m.Regex+"/")
	}
	if m.Type != nil {
		parts = append(parts, "type "+*m.Type)
	}
	return strings.Join(parts, " && ")
}

func assertionMessage(user, generated string) string {
	if strings.TrimSpace(user) == "" {
		return generated
	}
	return user + ": " + generated
}

func lookupHeader(headers [][2]string, name string) (string, bool) {
	var vals []string
	for _, h := range headers {
		if strings.EqualFold(strings.TrimSpace(h[0]), strings.TrimSpace(name)) {
			vals = append(vals, h[1])
		}
	}
	if len(vals) == 0 {
		return "", false
	}
	return strings.Join(vals, ", "), true
}

func joinValues(values []any, limit int) string {
	if len(values) == 0 {
		return ""
	}
	if limit <= 0 || limit > len(values) {
		limit = len(values)
	}
	parts := make([]string, 0, limit)
	for _, v := range values[:limit] {
		parts = append(parts, formatValue(v))
	}
	if len(values) > limit {
		parts = append(parts, fmt.Sprintf("… (%d more)", len(values)-limit))
	}
	return strings.Join(parts, ", ")
}

func formatValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case string:
		return t
	case json.Number:
		return t.String()
	case bool:
		return strconv.FormatBool(t)
	case float64:
		return formatFloat(t)
	case int:
		return strconv.Itoa(t)
	case int64:
		return strconv.FormatInt(t, 10)
	default:
		if b, err := json.Marshal(v); err == nil {
			return string(b)
		}
		return fmt.Sprintf("%v", v)
	}
}

func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'f', -1, 64)
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func scalarStringValue(v any) (string, bool) {
	switch v.(type) {
	case string, json.Number, bool, int, int64, float64:
		return formatValue(v), true
	default:
		return "", false
	}
}

func valueTypeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case string:
		return "string"
	case json.Number, float64, int, int64:
		return "number"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "array"
	default:
		return "unknown"
	}
}

func valuesEqual(got, want any) bool {
	if gn, ok := toFloat(got); ok {
		if wn, ok := toFloat(want); ok {
			return gn == wn
		}
		return false
	}
	switch w := want.(type) {
	case string:
		g, ok := got.(string)
		return ok && g == w
	case bool:
		g, ok := got.(bool)
		return ok && g == w
	case nil:
		return got == nil
	}
	return false
}

func literalsEqual(got any, want any) bool { return valuesEqual(got, want) }

func toFloat(v any) (float64, bool) {
	switch t := v.(type) {
	case json.Number:
		f, err := t.Float64()
		return f, err == nil
	case float64:
		return t, true
	case int:
		return float64(t), true
	case int64:
		return float64(t), true
	case uint64:
		return float64(t), true
	default:
		return 0, false
	}
}

// sortedKeys is a small helper shared by deterministic reporting.
func sortedKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
