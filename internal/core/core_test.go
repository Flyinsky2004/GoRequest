package core

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func bodyValue(t *testing.T, src string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(src), &n); err != nil {
		t.Fatalf("parse body: %v", err)
	}
	if len(n.Content) == 0 {
		t.Fatalf("empty body node")
	}
	return n.Content[0]
}

func TestDecodeStrictRejectsUnknownField(t *testing.T) {
	src := []byte("schemaVersion: 1\nid: x\nname: n\nhttp:\n  method: GET\n  url: http://example.com\nbogus: 1\n")
	var r Request
	if err := decodeStrict(src, &r); err == nil {
		t.Fatal("expected unknown field error")
	}
}

func TestDecodeStrictRejectsDuplicateKey(t *testing.T) {
	src := []byte("a: 1\na: 2\n")
	var m map[string]int
	if err := decodeStrict(src, &m); err == nil {
		t.Fatal("expected duplicate key error")
	}
}

func TestResolveVariables(t *testing.T) {
	vars := map[string]string{
		"baseUrl": "http://h",
		"full":    "{{baseUrl}}/api",
	}
	r := NewResolver(vars, MapSecrets{"token": "s3cret"})
	got, err := r.Resolve("{{full}}/x?t={{secret.token}}")
	if err != nil {
		t.Fatal(err)
	}
	if got != "http://h/api/x?t=s3cret" {
		t.Fatalf("got %q", got)
	}
	if _, err := r.Resolve("{{missing}}"); err == nil {
		t.Fatal("expected undefined variable error")
	}
	if _, err := r.Resolve("{{secret.nope}}"); err == nil {
		t.Fatal("expected missing secret error")
	}
}

func TestResolveCycle(t *testing.T) {
	vars := map[string]string{"a": "{{b}}", "b": "{{a}}"}
	r := NewResolver(vars, nil)
	if _, err := r.Resolve("{{a}}"); err == nil {
		t.Fatal("expected cycle error")
	}
}

func TestBuildPlanJSONAndAuth(t *testing.T) {
	req := &Request{
		SchemaVersion: 1,
		ID:            "r",
		HTTP: &HTTPDef{
			Method: "POST",
			URL:    "{{baseUrl}}/users",
			Query:  []KV{{Name: "a", Value: "1"}, {Name: "a", Value: "2"}, {Name: "off", Value: "x", Enabled: BoolPtr(false)}},
			Headers: []KV{
				{Name: "X-Test", Value: "{{v}}"},
				{Name: "Off", Value: "y", Enabled: BoolPtr(false)},
			},
			Body: &Body{Type: "json", Value: bodyValue(t, "name: \"{{who}}\"\nrole: admin\n")},
		},
		Auth: &AuthRef{Mode: "inline", Inline: &AuthScheme{Type: "bearer", Token: "{{secret.token}}"}},
	}
	vars := map[string]string{"baseUrl": "http://h", "v": "vv", "who": "alice"}
	plan, err := BuildPlan(req, vars, MapSecrets{"token": "tok"}, req.Auth.Inline)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Method != "POST" || plan.URL != "http://h/users" {
		t.Fatalf("bad plan method/url: %s %s", plan.Method, plan.URL)
	}
	if len(plan.Query) != 2 {
		t.Fatalf("expected 2 enabled query rows, got %d", len(plan.Query))
	}
	var gotAuth, gotCT string
	for _, h := range plan.Headers {
		if h[0] == "Authorization" {
			gotAuth = h[1]
		}
		if h[0] == "Content-Type" {
			gotCT = h[1]
		}
	}
	if gotAuth != "Bearer tok" {
		t.Fatalf("auth header %q", gotAuth)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type %q", gotCT)
	}
	var body map[string]any
	if err := json.Unmarshal(plan.Body, &body); err != nil {
		t.Fatalf("body not json: %v (%s)", err, plan.Body)
	}
	if body["name"] != "alice" || body["role"] != "admin" {
		t.Fatalf("body = %v", body)
	}
}

func TestBuildPlanRejectsHeaderInjection(t *testing.T) {
	req := &Request{HTTP: &HTTPDef{Method: "GET", URL: "http://h", Headers: []KV{{Name: "X", Value: "a\r\nInjected: 1"}}}}
	if _, err := BuildPlan(req, nil, nil, nil); err == nil {
		t.Fatal("expected header injection error")
	}
}

func TestBuildPlanRejectsRelativeURL(t *testing.T) {
	req := &Request{HTTP: &HTTPDef{Method: "GET", URL: "/relative"}}
	if _, err := BuildPlan(req, nil, nil, nil); err == nil {
		t.Fatal("expected relative url error")
	}
}

func TestExecuteEcho(t *testing.T) {
	var gotMethod, gotBody, gotHeader string
	var gotQuery []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotHeader = r.Header.Get("X-Test")
		gotQuery = r.URL.Query()["a"]
		b := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(b)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	req := &Request{HTTP: &HTTPDef{
		Method:  "POST",
		URL:     srv.URL + "/echo",
		Query:   []KV{{Name: "a", Value: "1"}, {Name: "a", Value: "2"}},
		Headers: []KV{{Name: "X-Test", Value: "hello"}},
		Body:    &Body{Type: "json", Value: bodyValue(t, "k: v\n")},
	}}
	plan, err := BuildPlan(req, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Execute(context.Background(), plan, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if res.Status != 200 {
		t.Fatalf("status %d", res.Status)
	}
	if res.Pretty == "" || !strings.Contains(res.Pretty, "\"ok\"") {
		t.Fatalf("pretty body = %q", res.Pretty)
	}
	if gotMethod != "POST" || gotHeader != "hello" {
		t.Fatalf("server got method=%q header=%q", gotMethod, gotHeader)
	}
	if len(gotQuery) != 2 || gotQuery[0] != "1" || gotQuery[1] != "2" {
		t.Fatalf("query = %v", gotQuery)
	}
	if !strings.Contains(gotBody, `"k":"v"`) {
		t.Fatalf("body = %q", gotBody)
	}
}

func TestExecuteTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	req := &Request{HTTP: &HTTPDef{Method: "GET", URL: srv.URL}, Timeout: "20ms"}
	plan, err := BuildPlan(req, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Execute(context.Background(), plan, srv.Client())
	if err == nil || !IsRun(err) {
		t.Fatalf("expected run error, got %v", err)
	}
	if !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout message, got %v", err)
	}
}

func TestExecuteCancel(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()
	req := &Request{HTTP: &HTTPDef{Method: "GET", URL: srv.URL}, Timeout: "5s"}
	plan, err := BuildPlan(req, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = Execute(ctx, plan, srv.Client())
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected canceled, got %v", err)
	}
}

func TestStoreConflictAndTraversal(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)
	if err := s.WriteAtomic("a.yaml", []byte("one\n")); err != nil {
		t.Fatal(err)
	}
	_, digest, err := s.Read("a.yaml")
	if err != nil {
		t.Fatal(err)
	}
	// External change.
	if err := os.WriteFile(filepath.Join(root, "a.yaml"), []byte("external\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = s.SaveChecked("a.yaml", []byte("mine\n"), digest)
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
	if _, err := s.resolve("../evil"); err == nil {
		t.Fatal("expected traversal rejection")
	}
}

func TestProjectNewRequestAndSave(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Demo")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewRequest("", "Hello World")
	if err != nil {
		t.Fatal(err)
	}
	if entry.Request.ID != "hello-world" {
		t.Fatalf("id = %q", entry.Request.ID)
	}
	entry.Request.HTTP.URL = "http://127.0.0.1:4010/ping"
	digest, err := p.SaveRequest(entry, entry.Request, entry.BaselineDigest)
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" {
		t.Fatal("empty digest")
	}
	// Reload and confirm the request persisted.
	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, ok := p2.Requests["hello-world"]
	if !ok {
		t.Fatal("request not reloaded")
	}
	if got.Request.HTTP.URL != "http://127.0.0.1:4010/ping" {
		t.Fatalf("url = %q", got.Request.HTTP.URL)
	}
	// Saving with a stale baseline must conflict.
	_, err = p2.SaveRequest(got, got.Request, "deadbeef")
	if !IsConflict(err) {
		t.Fatalf("expected conflict, got %v", err)
	}
}

func TestProjectDuplicateID(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewProject(dir, "Demo"); err != nil {
		t.Fatal(err)
	}
	gq := filepath.Join(dir, ".gorequest", "collections")
	if err := os.MkdirAll(gq, 0o755); err != nil {
		t.Fatal(err)
	}
	reqFile := "schemaVersion: 1\nid: dup\nname: A\nhttp:\n  method: GET\n  url: http://h/1\n"
	if err := os.WriteFile(filepath.Join(gq, "a.request.yaml"), []byte(reqFile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gq, "b.request.yaml"), []byte(strings.ReplaceAll(reqFile, "A", "B")), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if ErrorCount(p.Issues) == 0 {
		t.Fatal("expected duplicate id error")
	}
}

func TestRequestRoundTripPreservesOrder(t *testing.T) {
	req := &Request{
		SchemaVersion: 1,
		ID:            "x",
		Name:          "X",
		HTTP: &HTTPDef{
			Method:  "POST",
			URL:     "http://h",
			Headers: []KV{{Name: "B", Value: "2"}, {Name: "A", Value: "1"}},
			Body:    &Body{Type: "json", Value: bodyValue(t, "z: 1\na: 2\n")},
		},
		Auth: &AuthRef{Mode: "none"},
	}
	out, err := encodeYAML(req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "auth: none") {
		t.Fatalf("expected auth: none, got:\n%s", out)
	}
	var back Request
	if err := decodeStrict(out, &back); err != nil {
		t.Fatalf("re-decode: %v\n%s", err, out)
	}
	if len(back.HTTP.Headers) != 2 || back.HTTP.Headers[0].Name != "B" {
		t.Fatalf("header order lost: %+v", back.HTTP.Headers)
	}
}
