package core

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func graphQLRequest(method, document, operationName, variables string) *Request {
	return &Request{
		SchemaVersion: SupportedSchemaVersion,
		ID:            "gql.test",
		Name:          "GraphQL Test",
		Protocol:      ProtocolGraphQL,
		HTTP:          &HTTPDef{Method: method, URL: "http://example.test/graphql"},
		GraphQL:       &GraphQLDef{Document: document, OperationName: operationName, Variables: variables},
	}
}

func TestAnalyzeGraphQLOperationsAndSelection(t *testing.T) {
	doc := "query One { a }\nmutation Two($id: ID!) { b(id: $id) }"
	a, err := AnalyzeGraphQL(doc, "")
	if err == nil {
		t.Fatal("multi-operation document without operationName must fail")
	}
	if a == nil || len(a.Operations) != 2 {
		t.Fatalf("operations should still be listed: %+v", a)
	}
	a, err = AnalyzeGraphQL(doc, "Two")
	if err != nil {
		t.Fatalf("select Two: %v", err)
	}
	if a.Selected.Name != "Two" || a.Selected.Type != "mutation" {
		t.Fatalf("selected = %+v", a.Selected)
	}
	if _, err := AnalyzeGraphQL(doc, "Missing"); err == nil || !strings.Contains(err.Error(), "available") {
		t.Fatalf("missing operation error should list names: %v", err)
	}
}

func TestAnalyzeGraphQLRejectsInvalidDocuments(t *testing.T) {
	cases := []struct {
		name string
		doc  string
		op   string
		want string
	}{
		{"syntax", "query {", "", "syntax error"},
		{"empty", "   ", "", "required"},
		{"subscription", "subscription S { events }", "", "not supported"},
		{"duplicate", "query A { a } query A { b }", "", "duplicate operation name"},
		{"anonymous mix", "{ a } query B { b }", "", "anonymous"},
		{"no operations", "fragment F on Query { a }", "", "no operations"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := AnalyzeGraphQL(tc.doc, tc.op)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}
	// A syntax error carries a line/column diagnostic.
	_, err := AnalyzeGraphQL("query { a( }", "")
	if err == nil || !strings.Contains(err.Error(), "line") || !strings.Contains(err.Error(), "column") {
		t.Fatalf("syntax error should include line/column: %v", err)
	}
}

func TestGraphQLVariablesPreserveTypesAndNumbers(t *testing.T) {
	raw := `{"id": 9007199254740993, "ok": true, "none": null, "list": [1, "x"], "nested": {"n": 2}}`
	obj, err := parseGraphQLVariables(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := json.Marshal(obj)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), "9007199254740993") {
		t.Fatalf("large integer lost precision: %s", got)
	}
	if !strings.Contains(string(got), `"ok":true`) || !strings.Contains(string(got), `"none":null`) {
		t.Fatalf("types changed: %s", got)
	}
	if _, err := parseGraphQLVariables("[]"); err == nil {
		t.Fatal("array variables must be rejected")
	}
	if _, err := parseGraphQLVariables("1"); err == nil {
		t.Fatal("scalar variables must be rejected")
	}
	if _, err := parseGraphQLVariables(`{"a":1} {"b":2}`); err == nil {
		t.Fatal("trailing JSON must be rejected")
	}
	deep := strings.Repeat("[", MaxGraphQLVariablesDepth+1) + strings.Repeat("]", MaxGraphQLVariablesDepth+1)
	if _, err := parseGraphQLVariables(`{"a":` + deep + `}`); err == nil {
		t.Fatal("over-deep variables must be rejected")
	}
}

func TestResolveGraphQLVariablesTracksSecretsAndPreservesTypes(t *testing.T) {
	red := NewRedactor()
	r := NewResolver(map[string]string{"id": "42", "flag": "true"}, MapSecrets{"token": "SECRET-VALUE"})
	raw := `{"id": "{{id}}", "keep": 7, "token": "{{secret.token}}", "nested": {"ok": true}}`
	out, secrets, err := ResolveGraphQLVariables(raw, r, red)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if len(secrets) != 1 || secrets[0] != "SECRET-VALUE" {
		t.Fatalf("secrets = %v", secrets)
	}
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if got["id"] != "42" || got["keep"] != float64(7) || got["token"] != "SECRET-VALUE" {
		t.Fatalf("resolved = %s", out)
	}
	if !strings.Contains(string(out), `"keep":7`) {
		t.Fatalf("number type changed: %s", out)
	}
	if strings.Contains(red.RedactString("x SECRET-VALUE y"), "SECRET-VALUE") {
		t.Fatal("secret was not registered with the redactor")
	}
}

func TestGraphQLRejectedByAutomationPlanPath(t *testing.T) {
	req := graphQLRequest("POST", "query Q { q }", "", "")
	_, _, err := BuildTrackedPlanWithOptions(context.Background(), req, nil, nil, nil, nil, PlanOptions{})
	if err == nil || !strings.Contains(err.Error(), "not supported by this execution path") {
		t.Fatalf("automation path must reject GraphQL, got %v", err)
	}
	if _, _, err := BuildTrackedPlanWithOptions(context.Background(), req, nil, nil, nil, nil, PlanOptions{AllowGraphQL: true}); err != nil {
		t.Fatalf("interactive path should allow GraphQL: %v", err)
	}
}

func TestBuildGraphQLPlanPOSTEnvelope(t *testing.T) {
	var gotMethod, gotCT, gotAccept, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCT = r.Header.Get("Content-Type")
		gotAccept = r.Header.Get("Accept")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()

	req := graphQLRequest("POST", "mutation Save($id: ID!) { save(id: $id) }", "Save", `{"id": 9007199254740993}`)
	req.HTTP.URL = srv.URL + "/graphql"
	plan, red, err := BuildTrackedPlan(req, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	res, err := Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotMethod != http.MethodPost || gotCT != "application/json" {
		t.Fatalf("method/content-type = %q/%q", gotMethod, gotCT)
	}
	if !strings.Contains(gotAccept, "application/graphql-response+json") || !strings.Contains(gotAccept, "application/json") {
		t.Fatalf("accept = %q", gotAccept)
	}
	var env struct {
		Query         string          `json:"query"`
		OperationName string          `json:"operationName"`
		Variables     json.RawMessage `json:"variables"`
	}
	if err := json.Unmarshal([]byte(gotBody), &env); err != nil {
		t.Fatalf("envelope: %v (%s)", err, gotBody)
	}
	if env.Query != req.GraphQL.Document || env.OperationName != "Save" {
		t.Fatalf("envelope = %+v", env)
	}
	if !strings.Contains(string(env.Variables), "9007199254740993") {
		t.Fatalf("variables lost precision: %s", env.Variables)
	}
	cls := ClassifyGraphQLResult(res)
	if cls.Status != 200 || !cls.DataPresent || cls.DataNull || cls.HasErrors() {
		t.Fatalf("classification = %+v", cls)
	}
	if red == nil {
		t.Fatal("redactor missing")
	}
}

func TestBuildGraphQLPlanGETMergesQueryWithoutDuplicates(t *testing.T) {
	var gotQuery, gotOp, gotVars, gotOther string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		gotQuery = q.Get("query")
		gotOp = q.Get("operationName")
		gotVars = q.Get("variables")
		gotOther = q.Get("keep")
		if len(q["query"]) != 1 {
			t.Errorf("query parameter sent %d times", len(q["query"]))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"1"}}}`))
	}))
	defer srv.Close()

	req := graphQLRequest("GET", "query Viewer { viewer { id } }", "", `{"id": 1}`)
	req.HTTP.URL = srv.URL + "/graphql?query=old&keep=yes"
	req.HTTP.Query = []KV{{Name: "query", Value: "stale", Enabled: BoolPtr(true)}, {Name: "keep2", Value: "v", Enabled: BoolPtr(true)}}
	plan, _, err := BuildTrackedPlan(req, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if gotQuery != req.GraphQL.Document {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotOp != "" {
		t.Fatalf("operationName should be omitted, got %q", gotOp)
	}
	if gotVars != `{"id":1}` {
		t.Fatalf("variables = %q", gotVars)
	}
	if gotOther != "yes" {
		t.Fatalf("existing URL query lost: %q", gotOther)
	}
	if strings.Contains(plan.URL, "stale") {
		t.Fatalf("stale query leaked: %s", plan.URL)
	}
}

func TestGraphQLRejectsBeforeNetwork(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(200)
	}))
	defer srv.Close()

	cases := []struct {
		name string
		req  *Request
	}{
		{"get mutation", graphQLRequest("GET", "mutation M { m }", "", "")},
		{"subscription", graphQLRequest("POST", "subscription S { s }", "", "")},
		{"bad variables", graphQLRequest("POST", "query Q { q }", "", `[]`)},
		{"bad document", graphQLRequest("POST", "query {", "", "")},
		{"multi without name", graphQLRequest("POST", "query A { a } query B { b }", "", "")},
		{"missing secret", graphQLRequest("POST", "query Q { q }", "", `{"t":"{{secret.missing}}"}`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.req.HTTP.URL = srv.URL
			_, _, err := BuildTrackedPlan(tc.req, nil, MapSecrets{}, nil, nil)
			if err == nil {
				t.Fatal("expected a pre-network error")
			}
		})
	}
	if hits.Load() != 0 {
		t.Fatalf("validation failures reached the network %d times", hits.Load())
	}
}

func TestGraphQLPlanInheritsAuthAndTLS(t *testing.T) {
	var auth string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()

	req := graphQLRequest("POST", "query Q { ok }", "", "")
	req.HTTP.URL = srv.URL + "/graphql"
	plan, _, err := BuildTrackedPlan(req, nil, MapSecrets{"token": "SECRET-TOKEN"}, &AuthScheme{Type: "bearer", Token: "{{secret.token}}"}, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	plan.Network = &ExecuteOptions{TLS: TLSConfig{CustomCAPEM: caPEM}}
	if _, err := Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if auth != "Bearer SECRET-TOKEN" {
		t.Fatalf("authorization = %q", auth)
	}
}

func TestClassifyGraphQLResult(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"data", 200, `{"data":{"a":1}}`, "data"},
		{"errors", 200, `{"errors":[{"message":"bad","path":["a"],"locations":[{"line":1,"column":2}]}]}`, "graphql-errors"},
		{"partial", 200, `{"data":{"a":1},"errors":[{"message":"partial"}]}`, "partial-success"},
		{"data null", 200, `{"data":null}`, "data-null"},
		{"http error", 500, `{"errors":[{"message":"boom"}]}`, "http-error-with-graphql-errors"},
		{"non json", 200, `not json`, "invalid-json"},
		{"empty", 204, ``, "invalid-json"},
		{"not object", 200, `[]`, "invalid-json"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res := &ExecResult{Status: tc.status, Body: []byte(tc.body)}
			got := GraphQLResultSummary(ClassifyGraphQLResult(res))
			if got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
	// Path numbers stay exact.
	cls := ClassifyGraphQLResult(&ExecResult{Status: 200, Body: []byte(`{"errors":[{"message":"x","path":[9007199254740993]}]}`)})
	if len(cls.Errors) != 1 || len(cls.Errors[0].Path) != 1 {
		t.Fatalf("errors = %+v", cls.Errors)
	}
	if n, ok := cls.Errors[0].Path[0].(json.Number); !ok || n.String() != "9007199254740993" {
		t.Fatalf("path number = %#v", cls.Errors[0].Path[0])
	}
}

func TestGraphQLTimeoutAndTruncation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()

	req := graphQLRequest("POST", "query Q { ok }", "", "")
	req.HTTP.URL = srv.URL
	req.Timeout = "20ms"
	plan, _, err := BuildTrackedPlan(req, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if _, err := Execute(context.Background(), plan, nil); err == nil {
		t.Fatal("expected timeout")
	}

	// Truncation is reported as a response fact, not hidden.
	big := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"text":"` + strings.Repeat("x", 256) + `"}}`))
	}))
	defer big.Close()
	req2 := graphQLRequest("POST", "query Q { text }", "", "")
	req2.HTTP.URL = big.URL
	plan2, _, err := BuildTrackedPlan(req2, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	plan2.Network = &ExecuteOptions{MaxResponseBytes: 32}
	res, err := Execute(context.Background(), plan2, nil)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !res.Truncated {
		t.Fatal("expected truncated response")
	}
	if !ClassifyGraphQLResult(res).Truncated {
		t.Fatal("classification must carry truncation")
	}
}

func TestValidateGraphQLContract(t *testing.T) {
	ok := &GraphQLDef{Document: "query Q { q }", Variables: `{"n": 1}`}
	if err := ValidateGraphQL(ok, "GET"); err != nil {
		t.Fatalf("GET query should be valid: %v", err)
	}
	if err := ValidateGraphQL(&GraphQLDef{Document: "mutation M { m }"}, "POST"); err != nil {
		t.Fatalf("POST mutation should be valid: %v", err)
	}
	if err := ValidateGraphQL(&GraphQLDef{Document: "mutation M { m }"}, "GET"); err == nil {
		t.Fatal("GET mutation must be rejected")
	}
	if err := ValidateGraphQL(&GraphQLDef{Document: "subscription S { s }"}, "POST"); err == nil {
		t.Fatal("subscription must be rejected")
	}
	if err := ValidateGraphQL(&GraphQLDef{Document: "query Q { q }", Variables: `[]`}, "POST"); err == nil {
		t.Fatal("array variables must be rejected")
	}
	if err := ValidateGraphQL(&GraphQLDef{Document: strings.Repeat(" ", MaxGraphQLDocumentBytes+1)}, "POST"); err == nil {
		t.Fatal("oversized document must be rejected")
	}
}

func TestValidateEntityYAMLGraphQLShape(t *testing.T) {
	valid := []byte(`schemaVersion: 1
id: gql.valid
name: Valid
protocol: graphql
http:
  method: POST
  url: https://example.test/graphql
graphql:
  document: "query Q { q }"
  variables: '{"n": 1}'
`)
	if _, err := ValidateEntityYAML(EntityRequest, valid, YAMLLimits{}); err != nil {
		t.Fatalf("valid graphql request rejected: %v", err)
	}
	withBody := []byte(`schemaVersion: 1
id: gql.body
name: Body
protocol: graphql
http:
  method: POST
  url: https://example.test/graphql
  body:
    type: json
    value: {q: 1}
graphql:
  document: "query Q { q }"
`)
	if _, err := ValidateEntityYAML(EntityRequest, withBody, YAMLLimits{}); err == nil {
		t.Fatal("graphql request with http.body must be rejected")
	}
	unknownField := []byte(`schemaVersion: 1
id: gql.unknown
name: Unknown
protocol: graphql
http:
  method: POST
  url: https://example.test/graphql
graphql:
  document: "query Q { q }"
  extra: nope
`)
	if _, err := ValidateEntityYAML(EntityRequest, unknownField, YAMLLimits{}); err == nil {
		t.Fatal("unknown graphql field must be rejected")
	}
}

func TestMergeRequestYAMLPreservesGraphQLBlock(t *testing.T) {
	original := []byte(`schemaVersion: 1
id: gql.me
name: GraphQL Me
protocol: graphql
http:
  method: POST
  url: https://api.example.com/graphql
  headers:
    - name: X-Trace
      value: keep
graphql:
  document: |
    query Me {
      me { id }
    }
  operationName: Me
  variables: |
    {"id": 9007199254740993}
`)
	req := graphQLRequest("POST", "query Me {\n  me { id }\n}\n", "Me", "{\"id\": 9007199254740993}\n")
	req.ID = "gql.me"
	req.Name = "GraphQL Me"
	req.HTTP.URL = "https://api.example.com/graphql"
	req.HTTP.Headers = []KV{{Name: "X-Trace", Value: "keep", Enabled: BoolPtr(true)}}
	out, err := MergeRequestYAML(original, req)
	if err != nil {
		t.Fatalf("merge: %v", err)
	}
	for _, want := range []string{"protocol: graphql", "document:", "operationName: Me", "9007199254740993", "X-Trace"} {
		if !strings.Contains(string(out), want) {
			t.Fatalf("merged YAML lost %q:\n%s", want, out)
		}
	}
}
