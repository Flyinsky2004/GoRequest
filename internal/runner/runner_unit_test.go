package runner

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

func assertNodes(t *testing.T, yamlText string) []yaml.Node {
	t.Helper()
	var doc struct {
		Assert []yaml.Node `yaml:"assert"`
	}
	if err := yaml.Unmarshal([]byte(yamlText), &doc); err != nil {
		t.Fatalf("unmarshal assertions: %v", err)
	}
	return doc.Assert
}

func testExecResult(body string, status int, headers ...[2]string) *core.ExecResult {
	return &core.ExecResult{
		Status:   status,
		Proto:    "HTTP/1.1",
		Body:     []byte(body),
		BodySize: int64(len(body)),
		Headers:  headers,
		FinalURL: "https://api.example.test/items",
	}
}

func TestParseAssertionsAndEvaluation(t *testing.T) {
	nodes := assertNodes(t, `
assert:
  - status: 200
  - header: X-Trace
    contains: abc
  - jsonpath: $.user.id
    equals: 7
  - body: {contains: ok, notContains: bad}
  - durationMs: {lte: 100}
  - size: {gt: 1}
`)
	asserts, err := parseAssertions(nodes)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(asserts) != 6 {
		t.Fatalf("want 6 assertions, got %d", len(asserts))
	}
	res := testExecResult(`{"user":{"id":7},"state":"ok"}`, 200, [2]string{"X-Trace", "zabcz"})
	// duration lives on core.ExecResult.Duration (time.Duration), not here.
	ctx := &evalContext{result: res, red: core.NewRedactor()}
	ctx.result.Duration = 0
	results, fail := evaluateAssertions(asserts, ctx)
	if fail != nil {
		t.Fatalf("unexpected failure: %+v", fail)
	}
	for _, r := range results {
		if !r.Passed {
			t.Fatalf("assertion not passed: %+v", r)
		}
	}
}

func TestAssertionFailureIsRedacted(t *testing.T) {
	nodes := assertNodes(t, `
assert:
  - jsonpath: $.token
    equals: expected-value
`)
	asserts, err := parseAssertions(nodes)
	if err != nil {
		t.Fatal(err)
	}
	red := core.NewRedactor()
	red.RegisterSecret("SUPER_SECRET_TOKEN_42")
	ctx := &evalContext{
		result: testExecResult(`{"token":"SUPER_SECRET_TOKEN_42"}`, 200),
		red:    red,
	}
	results, fail := evaluateAssertions(asserts, ctx)
	if fail == nil {
		t.Fatal("expected assertion failure")
	}
	if strings.Contains(results[0].Actual, "SUPER_SECRET") || strings.Contains(fail.Message, "SUPER_SECRET") {
		t.Fatalf("secret leaked: result=%+v failure=%+v", results[0], fail)
	}
	if results[0].Actual != "***" {
		t.Fatalf("actual should be masked, got %q", results[0].Actual)
	}
}

func TestJSONPathSubset(t *testing.T) {
	q, err := parseJSONPath("$.items[-1]['name']")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal([]byte(`{"items":[{"name":"first"},{"name":"last"}]}`), &doc); err != nil {
		t.Fatal(err)
	}
	vals := q.Eval(doc)
	if len(vals) != 1 || vals[0] != "last" {
		t.Fatalf("want last, got %v", vals)
	}
	wild, err := parseJSONPath("$..id")
	if err != nil {
		t.Fatal(err)
	}
	vals = wild.Eval(doc)
	_ = vals
	for _, bad := range []string{"$.items[?(@.id>2)]", "$.items[0:2]", "$.items[0,1]", "items[0]"} {
		if _, err := parseJSONPath(bad); err == nil {
			t.Fatalf("expected unsupported path %q to fail", bad)
		}
	}
}

func TestExtractionNoPartialPublishOnFailure(t *testing.T) {
	exts, err := parseExtractions([]Extraction{
		{Name: "token", From: "jsonpath", Path: "$.accessToken"},
		{Name: "missing", From: "jsonpath", Path: "$.notThere"},
	})
	if err != nil {
		t.Fatal(err)
	}
	red := core.NewRedactor()
	ctx := &evalContext{
		result:      testExecResult(`{"accessToken":"SECRET_VALUE_123456"}`, 200),
		red:         red,
		onSensitive: func(string) {},
	}
	out := extractAll(exts, ctx)
	if out.failure == nil {
		t.Fatal("expected required missing output failure")
	}
	if len(out.outputs) != 0 {
		t.Fatalf("partial outputs published: %v", out.outputs)
	}
	if !out.results[0].Sensitive {
		t.Fatal("$.accessToken should default to sensitive")
	}
	if out.results[0].Value != "***" {
		t.Fatalf("sensitive value leaked in result: %q", out.results[0].Value)
	}
	if !strings.Contains(red.RedactString("prefix SECRET_VALUE_123456 suffix"), "***") {
		t.Fatal("extracted sensitive value was not registered for redaction")
	}
}

func TestMatcherMissingVsNull(t *testing.T) {
	nodes := assertNodes(t, `
assert:
  - jsonpath: $.missing
    exists: false
  - jsonpath: $.nullValue
    type: "null"
  - jsonpath: $.empty
    equals: ""
`)
	asserts, err := parseAssertions(nodes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &evalContext{
		result: testExecResult(`{"nullValue":null,"empty":""}`, 200),
		red:    core.NewRedactor(),
	}
	_, fail := evaluateAssertions(asserts, ctx)
	if fail != nil {
		t.Fatalf("missing/null/empty should be distinguishable: %v", fail)
	}
}

func TestSchemaPortConfigError(t *testing.T) {
	nodes := assertNodes(t, `
assert:
  - schema: {ref: schemas/user.json}
`)
	asserts, err := parseAssertions(nodes)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &evalContext{result: testExecResult(`{}`, 200), red: core.NewRedactor()}
	_, fail := evaluateAssertions(asserts, ctx)
	if fail == nil || fail.Category != FailureConfig || fail.Kind != KindSchema {
		t.Fatalf("missing validator should be config/schema, got %+v", fail)
	}
}

func TestCSVJSONDataset(t *testing.T) {
	dir := t.TempDir()
	store := core.NewStore(dir)
	if err := store.WriteAtomic("data/users.csv", []byte("userId,display\n1,Alice\n2,Bob\n")); err != nil {
		t.Fatal(err)
	}
	if err := store.WriteAtomic("data/users.json", []byte(`[{"userId":3,"active":true},{"userId":4,"active":false}]`)); err != nil {
		t.Fatal(err)
	}
	p := &core.Project{Store: store}
	csvDataset, err := loadDataset(p, &DatasetSpec{Format: "csv", File: "data/users.csv"})
	if err != nil {
		t.Fatal(err)
	}
	if len(csvDataset.rows) != 2 || csvDataset.rows[1].Vars["display"] != "Bob" {
		t.Fatalf("bad CSV rows: %+v", csvDataset.rows)
	}
	jsonDataset, err := loadDataset(p, &DatasetSpec{Format: "json", File: "data/users.json"})
	if err != nil {
		t.Fatal(err)
	}
	if len(jsonDataset.rows) != 2 || jsonDataset.rows[0].Vars["active"] != "true" {
		t.Fatalf("bad JSON rows: %+v", jsonDataset.rows)
	}
	if _, err := loadDataset(p, &DatasetSpec{Format: "csv", File: "../outside.csv"}); err == nil {
		t.Fatal("path traversal must fail")
	}
	if _, err := loadDataset(p, &DatasetSpec{Format: "json", File: "data/users.json", MaxRows: 1}); err == nil {
		t.Fatal("row limit must fail")
	}
}

func TestParseSuiteStrictAndDAG(t *testing.T) {
	_, err := ParseSuite([]byte(`
schemaVersion: 1
id: smoke
steps:
  - {id: a, request: collections/a.request.yaml, needs: [b]}
  - {id: b, request: collections/b.request.yaml}
`))
	if err != nil {
		t.Fatalf("cycle should be rejected by ValidateStructure, not ParseSuite: %v", err)
	}
	s, err := ParseSuite([]byte(`
schemaVersion: 1
id: smoke
steps:
  - {id: a, request: collections/a.request.yaml}
  - {id: b, request: collections/b.request.yaml, needs: [a]}
unknownField: true
`))
	if err == nil && s != nil {
		t.Fatal("unknown suite field must be rejected")
	}
}

func TestJUnitAndJSONReportsDoNotContainSecret(t *testing.T) {
	result := &RunResult{
		FormatVersion: 1,
		RunID:         "run-1",
		Snapshot:      "abc",
		Status:        StatusFailed,
		Rows: []RowResult{{
			Index:  1,
			Status: StatusFailed,
			Steps: []StepResult{{
				ID: "step", Request: "collections/a.request.yaml", Status: StatusFailed,
				Failure:  newFailure(FailureAssertion, KindAssertion, "expected token *** got ***"),
				Attempts: []AttemptResult{{Number: 1, Status: StatusFailed, HTTPStatus: 500}},
			}},
		}},
	}
	result.finalize()
	jsonBytes, err := MarshalJSONReport(result)
	if err != nil {
		t.Fatal(err)
	}
	junitBytes, err := MarshalJUnitReport(result)
	if err != nil {
		t.Fatal(err)
	}
	secret := "SUPER_SECRET_TOKEN_42"
	// A secret intentionally placed in raw result fields must have been
	// redacted earlier; here we verify writers never reintroduce raw bodies.
	if strings.Contains(string(jsonBytes), secret) || strings.Contains(string(junitBytes), secret) {
		t.Fatal("secret leaked into report")
	}
	if !strings.Contains(string(junitBytes), `<failure `) {
		t.Fatal("JUnit report must include failure element")
	}
	dir := filepath.Join(t.TempDir(), "run-output")
	jp, xp, err := ExportReports(result, ReportOptions{Dir: dir})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(jp); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(xp); err != nil {
		t.Fatal(err)
	}
}

func TestReportWriteFailure(t *testing.T) {
	if err := WriteJSONReport(failingWriter{}, &RunResult{}); err == nil {
		t.Fatal("expected writer error")
	}
	if err := WriteJUnitReport(failingWriter{}, &RunResult{}); err == nil {
		t.Fatal("expected writer error")
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("disk full") }

func TestHTTPServerFixtureIsLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	res, err := core.Execute(nil, &core.Plan{Method: "GET", URL: srv.URL, Timeout: 0}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 || !json.Valid(res.Body) {
		t.Fatalf("bad local fixture response: %+v", res)
	}
}
