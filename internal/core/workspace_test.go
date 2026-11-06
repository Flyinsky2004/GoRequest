package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func newWorkspaceProject(t *testing.T, name string) *Project {
	t.Helper()
	dir := t.TempDir()
	p, err := NewProject(dir, name)
	if err != nil {
		t.Fatalf("NewProject(%q): %v", name, err)
	}
	return p
}

func saveWorkspaceRequest(t *testing.T, p *Project, rel, name string, mutate func(*Request)) *RequestEntry {
	t.Helper()
	entry, err := p.NewRequest(rel, name)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", name, err)
	}
	req := *entry.Request
	mutate(&req)
	if _, err := p.SaveRequest(entry, &req, entry.BaselineDigest); err != nil {
		t.Fatalf("SaveRequest(%q): %v", name, err)
	}
	return entry
}

func TestCreateProjectAtScaffoldsAndNeverOverwrites(t *testing.T) {
	parent := t.TempDir()
	root, err := CreateProjectAt(parent, "My API")
	if err != nil {
		t.Fatalf("CreateProjectAt: %v", err)
	}
	if want := filepath.Join(parent, "my-api"); root != want {
		t.Fatalf("root = %q, want %q", root, want)
	}
	p, err := OpenProject(root)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if p.File.Name != "My API" {
		t.Fatalf("project name = %q", p.File.Name)
	}
	if _, err := os.Stat(filepath.Join(root, ".gorequest", "project.yaml")); err != nil {
		t.Fatal(err)
	}

	// A second create into the same target is refused instead of rewriting it.
	if _, err := CreateProjectAt(parent, "My API"); err == nil {
		t.Fatal("expected refusal for a non-empty target")
	}

	// Non-ASCII names keep a readable folder instead of collapsing to "item".
	other := t.TempDir()
	cnRoot, err := CreateProjectAt(other, "订单 API")
	if err != nil {
		t.Fatalf("CreateProjectAt(Chinese): %v", err)
	}
	if filepath.Base(cnRoot) != "订单 API" {
		t.Fatalf("Chinese project folder = %q", filepath.Base(cnRoot))
	}
	cn, err := OpenProject(cnRoot)
	if err != nil {
		t.Fatal(err)
	}
	if cn.File.Name != "订单 API" {
		t.Fatalf("Chinese project name = %q", cn.File.Name)
	}
}

func TestCreateProjectAtRejectsBadNamesAndMissingParent(t *testing.T) {
	parent := t.TempDir()
	for _, name := range []string{"", "   ", "../escape", "a/b", `a\b`, ".", ".."} {
		if _, err := CreateProjectAt(parent, name); err == nil {
			t.Fatalf("name %q should be rejected", name)
		}
	}
	if _, err := CreateProjectAt(filepath.Join(parent, "missing"), "Demo"); err == nil {
		t.Fatal("missing parent should be rejected")
	}
	// A brand-new target folder is cleaned up when scaffolding fails (empty
	// parent directory cannot be scaffolded as a project parent? use a file
	// where the target folder would be).
	fileParent := t.TempDir()
	if err := os.WriteFile(filepath.Join(fileParent, "demo"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := CreateProjectAt(fileParent, "Demo"); err == nil {
		t.Fatal("file at target path should be rejected")
	}
}

func TestEnsureGlobalWorkspaceCreatesAndReopens(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "global")
	p, err := EnsureGlobalWorkspace(dir)
	if err != nil {
		t.Fatalf("EnsureGlobalWorkspace: %v", err)
	}
	if p.File.Name != globalWorkspaceName {
		t.Fatalf("global name = %q", p.File.Name)
	}
	entry, err := p.NewRequest("", "Scratch")
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.HTTP.URL = "https://scratch.example"
	if _, err := p.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}

	again, err := EnsureGlobalWorkspace(dir)
	if err != nil {
		t.Fatal(err)
	}
	if again.Requests[entry.Request.ID] == nil {
		t.Fatalf("reopened global workspace lost request %q", entry.Request.ID)
	}
	if len(again.Requests) != 1 {
		t.Fatalf("reopened global workspace requests = %d", len(again.Requests))
	}
}

func TestGlobalWorkspaceDirHonorsEnvironmentOverride(t *testing.T) {
	want := filepath.Join(t.TempDir(), "scratch")
	t.Setenv(GlobalWorkspaceDirEnv, want)
	got, err := GlobalWorkspaceDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("GlobalWorkspaceDir = %q, want %q", got, want)
	}
}

func TestMatchRequestSearchesNameMethodURLAndBody(t *testing.T) {
	p := newWorkspaceProject(t, "Search")
	entry := saveWorkspaceRequest(t, p, "", "Create User", func(r *Request) {
		r.HTTP.Method = "POST"
		r.HTTP.URL = "https://api.example.com/v1/users"
		r.HTTP.Body = &Body{Type: BodyJSON, Value: jsonNode(t, `{"email":"Ada@Example.com"}`)}
	})
	cases := []struct{ query, field string }{
		{"create", "name"},
		{"POST", "method"},
		{"v1/users", "url"},
		{"ada@example", "body"},
	}
	for _, tc := range cases {
		if got := matchRequestField(entry, tc.query); got != tc.field {
			t.Fatalf("query %q matched %q, want %q", tc.query, got, tc.field)
		}
	}
	if MatchRequest(entry, "not-present") {
		t.Fatal("unrelated query should not match")
	}
	if MatchRequest(entry, "  ") {
		t.Fatal("blank query should not match")
	}

	hits := SearchProject(p, "users")
	if len(hits) != 1 || hits[0].Kind != "request" || hits[0].ID != entry.Request.ID {
		t.Fatalf("SearchProject hits = %+v", hits)
	}
}

func jsonNode(t *testing.T, text string) *yaml.Node {
	t.Helper()
	var n yaml.Node
	if err := yaml.Unmarshal([]byte(text), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Content) == 0 {
		t.Fatal("empty yaml node")
	}
	return n.Content[0]
}

func TestTransferCopyGetsNewIDAndKeepsSecretReferences(t *testing.T) {
	src := newWorkspaceProject(t, "Source")
	dst := newWorkspaceProject(t, "Target")
	entry := saveWorkspaceRequest(t, src, "", "Login", func(r *Request) {
		r.HTTP.Method = "POST"
		r.HTTP.URL = "https://api.example.com/login"
		r.HTTP.Headers = []KV{{Name: "Authorization", Value: "Bearer {{secret.token}}"}}
		r.Auth = &AuthRef{Mode: "ref", Ref: "bearer"}
	})

	plan, err := PlanRequestTransfer(src, dst, entry, "", TransferCopy)
	if err != nil {
		t.Fatalf("PlanRequestTransfer: %v", err)
	}
	if plan.Blocked {
		t.Fatalf("secret reference must not block: %+v", plan.Warnings)
	}
	if plan.TargetID == entry.Request.ID {
		t.Fatal("copy must assign a new ID")
	}
	if len(plan.SecretRefs) != 1 || plan.SecretRefs[0] != "token" {
		t.Fatalf("secret refs = %v", plan.SecretRefs)
	}
	if plan.Warnings == nil {
		t.Fatal("an unresolved source auth ref should be previewed")
	}
	foundAuth := false
	for _, w := range plan.Warnings {
		if w.Code == WarnUnresolvedAuth && w.Detail == "bearer" {
			foundAuth = true
		}
	}
	if !foundAuth {
		t.Fatalf("missing unresolved auth warning: %+v", plan.Warnings)
	}
	if dst.Store.Exists(plan.TargetRel) {
		t.Fatal("planning must not write the target")
	}

	if err := plan.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if src.Requests[entry.Request.ID] == nil {
		t.Fatal("copy removed the source request")
	}
	copied := dst.Requests[plan.TargetID]
	if copied == nil {
		t.Fatalf("target missing copied request %q", plan.TargetID)
	}
	if copied.Request.HTTP.Headers[0].Value != "Bearer {{secret.token}}" {
		t.Fatalf("secret reference changed: %q", copied.Request.HTTP.Headers[0].Value)
	}
	if !dst.Store.Exists(plan.TargetRel) {
		t.Fatal("apply did not write the target file")
	}
}

func TestTransferMoveKeepsIDAndRemovesSource(t *testing.T) {
	src := newWorkspaceProject(t, "Source")
	dst := newWorkspaceProject(t, "Target")
	entry := saveWorkspaceRequest(t, src, "", "Orders", func(r *Request) {
		r.HTTP.URL = "https://api.example.com/orders"
	})
	original := filepath.Join(src.Store.Root, filepath.FromSlash(entry.RelPath))
	before, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}

	plan, err := PlanRequestTransfer(src, dst, entry, "", TransferMove)
	if err != nil {
		t.Fatalf("PlanRequestTransfer: %v", err)
	}
	if !plan.KeepID || plan.TargetID != entry.Request.ID {
		t.Fatalf("move must keep ID: %+v", plan)
	}
	if err := plan.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if src.Requests[entry.Request.ID] != nil {
		t.Fatal("move left the source indexed")
	}
	if _, err := os.Stat(original); !os.IsNotExist(err) {
		t.Fatal("move left the source file on disk")
	}
	if dst.Requests[entry.Request.ID] == nil {
		t.Fatal("move did not index the request in the destination")
	}
	after, err := os.ReadFile(filepath.Join(dst.Store.Root, filepath.FromSlash(plan.TargetRel)))
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatal("move changed the request file bytes")
	}
}

func TestTransferMoveRefusesDestinationIDConflict(t *testing.T) {
	src := newWorkspaceProject(t, "Source")
	dst := newWorkspaceProject(t, "Target")
	entry := saveWorkspaceRequest(t, src, "", "Login", func(*Request) {})
	if _, err := dst.NewRequest("", "Login"); err != nil {
		t.Fatal(err)
	}
	if _, err := PlanRequestTransfer(src, dst, entry, "", TransferMove); err == nil {
		t.Fatal("move must refuse a destination ID conflict")
	}
	if src.Requests[entry.Request.ID] == nil {
		t.Fatal("a refused move must not remove the source")
	}
}

func TestTransferBlocksInlineCredentialsAndLiteralCookies(t *testing.T) {
	src := newWorkspaceProject(t, "Source")
	dst := newWorkspaceProject(t, "Target")
	inline := saveWorkspaceRequest(t, src, "", "Inline", func(r *Request) {
		r.Auth = &AuthRef{Mode: "inline", Inline: &AuthScheme{Type: "bearer", Token: "inline-token-value"}}
	})
	plan, err := PlanRequestTransfer(src, dst, inline, "", TransferCopy)
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if !plan.Blocked {
		t.Fatalf("inline token should block: %+v", plan.Warnings)
	}
	if err := plan.Apply(); err == nil {
		t.Fatal("blocked transfer must not apply")
	}
	if dst.Store.Exists(plan.TargetRel) {
		t.Fatal("blocked transfer wrote a file")
	}

	cookie := saveWorkspaceRequest(t, src, "", "Cookie", func(r *Request) {
		r.HTTP.Headers = []KV{{Name: "Cookie", Value: "session=abc123"}}
	})
	plan, err = PlanRequestTransfer(src, dst, cookie, "", TransferCopy)
	if err != nil {
		t.Fatalf("plan cookie: %v", err)
	}
	if !plan.Blocked {
		t.Fatal("literal Cookie value should block")
	}
	for _, w := range plan.Warnings {
		if strings.Contains(w.Detail, "abc123") {
			t.Fatal("preview leaked the cookie value")
		}
	}
}

func TestTransferWarnsUnresolvedVariablesAndDocs(t *testing.T) {
	src := newWorkspaceProject(t, "Source")
	dst := newWorkspaceProject(t, "Target")
	entry := saveWorkspaceRequest(t, src, "", "Docs", func(r *Request) {
		r.HTTP.URL = "https://{{apiHost}}/x"
		r.Documentation = []string{"docs/guide.md"}
	})
	plan, err := PlanRequestTransfer(src, dst, entry, "", TransferCopy)
	if err != nil {
		t.Fatal(err)
	}
	var varWarn, docWarn bool
	for _, w := range plan.Warnings {
		switch w.Code {
		case WarnUnresolvedVariable:
			varWarn = w.Detail == "apiHost"
		case WarnMissingDoc:
			docWarn = w.Detail == "docs/guide.md"
		}
	}
	if !varWarn || !docWarn {
		t.Fatalf("warnings = %+v", plan.Warnings)
	}
}

func TestTransferApplyRejectsSourceChangedAfterPreview(t *testing.T) {
	src := newWorkspaceProject(t, "Source")
	dst := newWorkspaceProject(t, "Target")
	entry := saveWorkspaceRequest(t, src, "", "Orders", func(*Request) {})
	plan, err := PlanRequestTransfer(src, dst, entry, "", TransferCopy)
	if err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(src.Store.Root, filepath.FromSlash(entry.RelPath))
	data, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, append(data, []byte("\n# external edit\n")...), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); !IsConflict(err) {
		t.Fatalf("Apply error = %v, want conflict", err)
	}
	if dst.Store.Exists(plan.TargetRel) {
		t.Fatal("conflicting transfer must not write the target")
	}
}
