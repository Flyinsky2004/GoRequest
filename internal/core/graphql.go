package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
)

// GraphQL phase-1 bounds. The document and variables are user-editable text
// stored in the request YAML, so they are bounded before parsing to keep a
// hostile project file from exhausting memory or the parser stack.
const (
	MaxGraphQLDocumentBytes  = 256 << 10
	MaxGraphQLVariablesBytes = 256 << 10
	// MaxGraphQLTokens bounds parser work independently of the byte limit; the
	// gqlparser parser also enforces its own 1000-level nesting limit.
	MaxGraphQLTokens = 20000
	// MaxGraphQLVariablesDepth bounds nested JSON objects/arrays in variables.
	MaxGraphQLVariablesDepth = 64

	graphQLAccept = "application/graphql-response+json, application/json"
)

// GraphQLOperation is one operation found in a document.
type GraphQLOperation struct {
	Name      string
	Type      string // query | mutation | subscription
	Line      int
	Column    int
	Anonymous bool
}

// GraphQLAnalysis is the parsed document plus the operation selected by the
// request's operationName. When selection fails, Operations is still populated
// so an editor can offer the names it found.
type GraphQLAnalysis struct {
	Operations []GraphQLOperation
	Selected   GraphQLOperation
}

// isGraphQLRequest reports whether req declares protocol: graphql.
func isGraphQLRequest(req *Request) bool {
	return req != nil && strings.EqualFold(strings.TrimSpace(req.Protocol), ProtocolGraphQL)
}

// NewGraphQLRequest creates and saves a new GraphQL request in a collection
// directory (relative to collections, may be empty for the root). It reuses the
// shared request creation path so IDs, paths and collection variables stay
// consistent with HTTP and WebSocket requests.
func (p *Project) NewGraphQLRequest(collectionRel, name string) (*RequestEntry, error) {
	if strings.TrimSpace(name) == "" {
		name = "New GraphQL"
	}
	return p.newRequestWith(collectionRel, name, ProtocolGraphQL)
}

// NormalizeGraphQLMethod returns the effective GraphQL over HTTP method. An
// omitted method means POST, the safe default for a mutation-capable document.
func NormalizeGraphQLMethod(method string) string {
	m := strings.ToUpper(strings.TrimSpace(method))
	if m == "" {
		return http.MethodPost
	}
	return m
}

// AnalyzeGraphQL parses document with gqlparser and selects an operation.
//
// Selection rules follow the GraphQL specification's single-operation rules:
// a multi-operation document requires an explicit operationName, anonymous
// operations cannot be mixed with other operations, and names must be unique.
// Subscriptions are rejected in phase 1 before any network call; the existing
// WebSocket transport is not a GraphQL subscription implementation.
func AnalyzeGraphQL(document, operationName string) (*GraphQLAnalysis, error) {
	if len(document) > MaxGraphQLDocumentBytes {
		return nil, configErrorf("graphql document exceeds %d bytes", MaxGraphQLDocumentBytes)
	}
	if strings.TrimSpace(document) == "" {
		return nil, configErrorf("graphql document is required")
	}
	doc, err := parser.ParseQueryWithTokenLimit(&ast.Source{
		Name:  "request.graphql",
		Input: document,
	}, MaxGraphQLTokens)
	if err != nil {
		return nil, graphQLParseError(err)
	}
	analysis := &GraphQLAnalysis{}
	seen := map[string]bool{}
	anonymous := 0
	for _, op := range doc.Operations {
		info := GraphQLOperation{
			Name:      op.Name,
			Type:      string(op.Operation),
			Anonymous: op.Name == "",
		}
		if op.Position != nil {
			info.Line = op.Position.Line
			info.Column = op.Position.Column
		}
		if info.Name == "" {
			anonymous++
		} else if seen[info.Name] {
			return analysis, configErrorf("graphql document defines duplicate operation name %q", info.Name)
		} else {
			seen[info.Name] = true
		}
		analysis.Operations = append(analysis.Operations, info)
	}
	if len(analysis.Operations) == 0 {
		return analysis, configErrorf("graphql document contains no operations")
	}
	if len(analysis.Operations) > 1 && anonymous > 0 {
		return analysis, configErrorf("an anonymous operation cannot be combined with other operations")
	}

	name := strings.TrimSpace(operationName)
	switch {
	case name != "":
		found := false
		for _, op := range analysis.Operations {
			if op.Name == name {
				analysis.Selected = op
				found = true
				break
			}
		}
		if !found {
			return analysis, configErrorf("graphql operationName %q not found (available: %s)", name, graphQLOperationNames(analysis.Operations))
		}
	case len(analysis.Operations) == 1:
		analysis.Selected = analysis.Operations[0]
	default:
		return analysis, configErrorf("graphql document defines %d operations; operationName is required (available: %s)", len(analysis.Operations), graphQLOperationNames(analysis.Operations))
	}

	if analysis.Selected.Type == string(ast.Subscription) {
		return analysis, configErrorf("GraphQL subscriptions are not supported in this phase")
	}
	return analysis, nil
}

func graphQLOperationNames(ops []GraphQLOperation) string {
	names := make([]string, 0, len(ops))
	for _, op := range ops {
		if op.Name == "" {
			names = append(names, "(anonymous)")
		} else {
			names = append(names, op.Name)
		}
	}
	return strings.Join(names, ", ")
}

func graphQLParseError(err error) error {
	var ge *gqlerror.Error
	if errors.As(err, &ge) {
		if len(ge.Locations) > 0 {
			loc := ge.Locations[0]
			return configErrorf("graphql syntax error at line %d, column %d: %s", loc.Line, loc.Column, ge.Message)
		}
		return configErrorf("graphql syntax error: %s", ge.Message)
	}
	return configErrorf("graphql syntax error: %v", err)
}

// ValidateGraphQL checks a stored graphql block without touching the network.
func ValidateGraphQL(g *GraphQLDef, method string) error {
	if g == nil {
		return configErrorf("graphql block is missing")
	}
	if len(g.Document) > MaxGraphQLDocumentBytes {
		return configErrorf("graphql document exceeds %d bytes", MaxGraphQLDocumentBytes)
	}
	analysis, err := AnalyzeGraphQL(g.Document, g.OperationName)
	if err != nil {
		return err
	}
	m := NormalizeGraphQLMethod(method)
	if m != http.MethodGet && m != http.MethodPost {
		return configErrorf("graphql method must be GET or POST, got %q", method)
	}
	if m == http.MethodGet && analysis.Selected.Type != string(ast.Query) {
		return configErrorf("GraphQL GET can only execute a query, not a %s", analysis.Selected.Type)
	}
	if _, err := parseGraphQLVariables(g.Variables); err != nil {
		return err
	}
	return nil
}

// parseGraphQLVariables decodes variables as a JSON object while preserving
// number literals (json.Number) and rejecting arrays, scalars, trailing JSON,
// oversized input and excessive nesting. Empty input is an empty object.
func parseGraphQLVariables(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return map[string]any{}, nil
	}
	if len(trimmed) > MaxGraphQLVariablesBytes {
		return nil, configErrorf("graphql variables exceed %d bytes", MaxGraphQLVariablesBytes)
	}
	dec := json.NewDecoder(strings.NewReader(trimmed))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, configErrorf("graphql variables must be valid JSON: %v", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		return nil, configErrorf("graphql variables must contain exactly one JSON value")
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return nil, configErrorf("graphql variables must be a JSON object")
	}
	if err := checkGraphQLJSONDepth(obj, 1); err != nil {
		return nil, err
	}
	return obj, nil
}

func checkGraphQLJSONDepth(v any, depth int) error {
	if depth > MaxGraphQLVariablesDepth {
		return configErrorf("graphql variables nesting exceeds %d levels", MaxGraphQLVariablesDepth)
	}
	switch t := v.(type) {
	case map[string]any:
		for _, child := range t {
			if err := checkGraphQLJSONDepth(child, depth+1); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range t {
			if err := checkGraphQLJSONDepth(child, depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

// ResolveGraphQLVariables resolves templates only inside variables string
// values, preserving every non-string JSON type exactly. It returns the
// compact JSON object and the secret values it registered, so the caller can
// merge them into the plan's final Redactor.
func ResolveGraphQLVariables(raw string, r *Resolver, red *Redactor) (json.RawMessage, []string, error) {
	obj, err := parseGraphQLVariables(raw)
	if err != nil {
		return nil, nil, err
	}
	var secrets []string
	resolved, err := resolveGraphQLValue(obj, r, red, &secrets)
	if err != nil {
		return nil, secrets, err
	}
	out, err := json.Marshal(resolved)
	if err != nil {
		return nil, secrets, configErrorf("graphql variables: %v", err)
	}
	return out, secrets, nil
}

func resolveGraphQLValue(v any, r *Resolver, red *Redactor, secrets *[]string) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, child := range t {
			rv, err := resolveGraphQLValue(child, r, red, secrets)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, child := range t {
			rv, err := resolveGraphQLValue(child, r, red, secrets)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	case string:
		if !strings.Contains(t, "{{") {
			return t, nil
		}
		tv, err := r.ResolveTracked(t)
		if err != nil {
			return nil, err
		}
		if tv.Sensitive && tv.Value != "" {
			if red != nil {
				red.RegisterSecret(tv.Value)
			}
			*secrets = append(*secrets, tv.Value)
		}
		return tv.Value, nil
	default:
		return v, nil
	}
}

// buildGraphQLTrackedPlan derives an HTTP request from a graphql request and
// reuses the normal tracked plan path for URL/header/auth resolution and
// secret tracking. Variables are resolved separately so JSON number literals
// keep their exact text instead of passing through YAML/float64.
func buildGraphQLTrackedPlan(
	ctx context.Context,
	req *Request,
	vars map[string]string,
	secrets SecretProvider,
	authScheme *AuthScheme,
	opts *ResolveOptions,
	bodyOpts PlanOptions,
) (*Plan, *Redactor, error) {
	red := NewRedactor()
	if req == nil || req.HTTP == nil {
		return nil, red, configErrorf("request has no http definition")
	}
	if req.GraphQL == nil {
		return nil, red, configErrorf("graphql request %q has no graphql block", req.Name)
	}
	method := NormalizeGraphQLMethod(req.HTTP.Method)
	if method != http.MethodGet && method != http.MethodPost {
		return nil, red, configErrorf("graphql method must be GET or POST, got %q", req.HTTP.Method)
	}
	analysis, err := AnalyzeGraphQL(req.GraphQL.Document, req.GraphQL.OperationName)
	if err != nil {
		return nil, red, red.RedactError(err)
	}
	if method == http.MethodGet && analysis.Selected.Type != string(ast.Query) {
		return nil, red, red.RedactError(configErrorf("GraphQL GET can only execute a query, not a %s", analysis.Selected.Type))
	}

	resolver := newGraphQLResolver(vars, secrets, opts, red)
	variables, varSecrets, err := ResolveGraphQLVariables(req.GraphQL.Variables, resolver, red)
	if err != nil {
		return nil, red, red.RedactError(err)
	}

	derived := deriveGraphQLHTTPRequest(req, method)
	plan, planRed, err := BuildTrackedPlanWithOptions(ctx, derived, vars, secrets, authScheme, opts, bodyOpts)
	if planRed == nil {
		planRed = red
	}
	for _, s := range varSecrets {
		planRed.RegisterSecret(s)
	}
	if err != nil {
		return nil, planRed, planRed.RedactError(err)
	}

	if method == http.MethodGet {
		if err := applyGraphQLGETParams(plan, req.GraphQL.Document, strings.TrimSpace(req.GraphQL.OperationName), variables); err != nil {
			return nil, planRed, planRed.RedactError(err)
		}
		return plan, planRed, nil
	}
	body, err := graphQLEnvelope(req.GraphQL.Document, strings.TrimSpace(req.GraphQL.OperationName), variables)
	if err != nil {
		return nil, planRed, planRed.RedactError(err)
	}
	plan.Body = body
	plan.HasBody = true
	return plan, planRed, nil
}

// newGraphQLResolver mirrors the merged-variable and tracking setup used by
// BuildTrackedPlanWithOptions so variables resolve against the same layers and
// taint rules.
func newGraphQLResolver(vars map[string]string, secrets SecretProvider, opts *ResolveOptions, red *Redactor) *Resolver {
	merged := MergeVars(vars)
	if opts != nil {
		for _, extra := range []map[string]string{opts.RunVars, opts.StepVars, opts.OverrideVars} {
			for k, v := range extra {
				if !IsReservedNamespace(k) {
					merged[k] = v
				}
			}
		}
		for _, so := range opts.StepOutputs {
			if so.Sensitive && so.Value != "" {
				red.RegisterSecret(so.Value)
			}
		}
	}
	var sp SecretProvider
	if secrets != nil {
		sp = &trackingSecrets{inner: secrets, red: red}
	}
	r := NewResolver(merged, sp)
	if opts != nil {
		r.WithDataVars(opts.DataVars)
		r.WithStepOutputs(opts.StepOutputs)
	}
	return r
}

// deriveGraphQLHTTPRequest creates the HTTP plan input. Its Protocol is set to
// http so the tracked plan builder never re-enters the GraphQL branch.
func deriveGraphQLHTTPRequest(req *Request, method string) *Request {
	d := *req
	d.Protocol = ProtocolHTTP
	d.GraphQL = nil
	d.WS = nil
	httpDef := &HTTPDef{Method: method, URL: req.HTTP.URL}
	if req.HTTP != nil {
		httpDef.Query = append([]KV(nil), req.HTTP.Query...)
		httpDef.Headers = append([]KV(nil), req.HTTP.Headers...)
	}
	httpDef.Headers = setEnabledHeader(httpDef.Headers, "Accept", graphQLAccept)
	if method == http.MethodPost {
		httpDef.Headers = setEnabledHeader(httpDef.Headers, "Content-Type", "application/json")
	}
	d.HTTP = httpDef
	return &d
}

// setEnabledHeader replaces enabled headers with the same name and appends the
// new value; disabled rows are preserved untouched.
func setEnabledHeader(headers []KV, name, value string) []KV {
	out := make([]KV, 0, len(headers)+1)
	for _, kv := range headers {
		if kv.IsEnabled() && strings.EqualFold(strings.TrimSpace(kv.Name), name) {
			continue
		}
		out = append(out, kv)
	}
	out = append(out, KV{Name: name, Value: value, Enabled: BoolPtr(true)})
	return out
}

func graphQLEnvelope(document, operationName string, variables json.RawMessage) ([]byte, error) {
	if len(variables) == 0 {
		variables = json.RawMessage("{}")
	}
	env := struct {
		Query         string          `json:"query"`
		OperationName string          `json:"operationName,omitempty"`
		Variables     json.RawMessage `json:"variables"`
	}{Query: document, OperationName: operationName, Variables: variables}
	body, err := json.Marshal(env)
	if err != nil {
		return nil, configErrorf("graphql envelope: %v", err)
	}
	return body, nil
}

// applyGraphQLGETParams puts the three GraphQL parameters into the URL exactly
// once. Existing same-name parameters (case-insensitively) are replaced rather
// than duplicated, and request-level query rows with those names are dropped.
func applyGraphQLGETParams(plan *Plan, document, operationName string, variables json.RawMessage) error {
	if plan == nil {
		return configErrorf("nil plan")
	}
	u, err := url.Parse(plan.URL)
	if err != nil {
		return configErrorf("invalid url: %v", err)
	}
	q := u.Query()
	deleteQueryKeyFold(q, "query")
	deleteQueryKeyFold(q, "operationName")
	deleteQueryKeyFold(q, "variables")
	q.Set("query", document)
	if operationName != "" {
		q.Set("operationName", operationName)
	}
	q.Set("variables", string(variables))
	u.RawQuery = q.Encode()
	plan.URL = u.String()

	filtered := make([][2]string, 0, len(plan.Query))
	for _, kv := range plan.Query {
		if isGraphQLParamName(kv[0]) {
			continue
		}
		filtered = append(filtered, kv)
	}
	plan.Query = filtered
	plan.Body = nil
	plan.HasBody = false
	return nil
}

func deleteQueryKeyFold(q url.Values, name string) {
	for key := range q {
		if strings.EqualFold(key, name) {
			delete(q, key)
		}
	}
}

func isGraphQLParamName(name string) bool {
	return strings.EqualFold(name, "query") ||
		strings.EqualFold(name, "operationName") ||
		strings.EqualFold(name, "variables")
}

// GraphQLErrorLocation is one location in a GraphQL response error.
type GraphQLErrorLocation struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

// GraphQLError is one entry of the response's errors array. Path numbers stay
// json.Number so large array indices are not rounded.
type GraphQLError struct {
	Message    string                 `json:"message"`
	Path       []any                  `json:"path,omitempty"`
	Locations  []GraphQLErrorLocation `json:"locations,omitempty"`
	Extensions json.RawMessage        `json:"extensions,omitempty"`
}

// GraphQLResult classifies a GraphQL over HTTP response without treating HTTP
// 200 as business success.
type GraphQLResult struct {
	Status      int
	HTTPError   bool
	InvalidJSON bool
	ParseError  string
	DataPresent bool
	DataNull    bool
	Data        json.RawMessage
	Errors      []GraphQLError
	Extensions  json.RawMessage
	Truncated   bool
}

// HasErrors reports whether the response carried a non-empty errors array.
func (r GraphQLResult) HasErrors() bool { return len(r.Errors) > 0 }

// Partial reports a response with non-null data and at least one error.
func (r GraphQLResult) Partial() bool {
	return r.DataPresent && !r.DataNull && len(r.Errors) > 0
}

// ClassifyGraphQLResult decodes the bounded response body into HTTP facts plus
// data/errors/extensions. It never treats a 200 with errors as success.
func ClassifyGraphQLResult(res *ExecResult) GraphQLResult {
	out := GraphQLResult{}
	if res == nil {
		return out
	}
	out.Status = res.Status
	out.HTTPError = res.Status < 200 || res.Status >= 300
	out.Truncated = res.Truncated
	body := bytes.TrimSpace(res.Body)
	if len(body) == 0 {
		out.InvalidJSON = true
		out.ParseError = "empty response body"
		return out
	}
	var top map[string]json.RawMessage
	if err := json.Unmarshal(body, &top); err != nil {
		out.InvalidJSON = true
		out.ParseError = err.Error()
		return out
	}
	if top == nil {
		out.InvalidJSON = true
		out.ParseError = "response is not a JSON object"
		return out
	}
	if raw, ok := top["data"]; ok {
		out.DataPresent = true
		out.Data = raw
		out.DataNull = string(bytes.TrimSpace(raw)) == "null"
	}
	if raw, ok := top["errors"]; ok && string(bytes.TrimSpace(raw)) != "null" {
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.UseNumber()
		if err := dec.Decode(&out.Errors); err != nil {
			out.InvalidJSON = true
			out.ParseError = "errors: " + err.Error()
			return out
		}
	}
	out.Extensions = top["extensions"]
	return out
}

// GraphQLResultSummary is a short, UI-independent classification label used by
// tests and the desktop status line.
func GraphQLResultSummary(r GraphQLResult) string {
	switch {
	case r.InvalidJSON:
		return "invalid-json"
	case r.HTTPError && r.HasErrors():
		return "http-error-with-graphql-errors"
	case r.HTTPError:
		return "http-error"
	case r.Partial():
		return "partial-success"
	case r.HasErrors():
		return "graphql-errors"
	case r.DataPresent && r.DataNull:
		return "data-null"
	case r.DataPresent:
		return "data"
	default:
		return "no-data"
	}
}

// GraphQLVariablesJSON returns the normalized variables JSON for display, or
// "{}" for empty input. It does not resolve templates.
func GraphQLVariablesJSON(raw string) (string, error) {
	obj, err := parseGraphQLVariables(raw)
	if err != nil {
		return "", err
	}
	out, err := json.Marshal(obj)
	if err != nil {
		return "", fmt.Errorf("graphql variables: %w", err)
	}
	return string(out), nil
}
