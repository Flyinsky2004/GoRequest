package core

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func writeTestFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readTestFile(t *testing.T, root, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func setProjectDefaultAuth(t *testing.T, p *Project, auth string) {
	t.Helper()
	original, digest, err := p.Store.Read("project.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var pf ProjectFile
	if err := decodeStrict(original, &pf); err != nil {
		t.Fatal(err)
	}
	if pf.Defaults == nil {
		pf.Defaults = &Defaults{}
	}
	def := *pf.Defaults
	def.Auth = auth
	pf.Defaults = &def
	data, err := MergeProjectFileYAML(original, &pf)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Store.SaveChecked("project.yaml", data, digest); err != nil {
		t.Fatal(err)
	}
	if err := p.reloadFromDiskForTest(); err != nil {
		t.Fatal(err)
	}
}

// reloadFromDiskForTest reopens the project in place for tests that mutate
// project.yaml directly.
func (p *Project) reloadFromDiskForTest() error {
	reopened, err := OpenProject(filepath.Dir(p.Dir))
	if err != nil {
		return err
	}
	*p = *reopened
	return nil
}

func saveRequestAuth(t *testing.T, p *Project, entry *RequestEntry, auth *AuthRef) {
	t.Helper()
	req := *entry.Request
	req.Auth = auth
	if _, err := p.SaveRequestPreserving(entry, &req, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}
}

func TestAuthSchemeCRUDPreservesCommentsAndReferences(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Identity")
	if err != nil {
		t.Fatal(err)
	}
	root := p.Dir

	// A hand-written file with a comment and an extension key.
	writeTestFile(t, root, "auth/shared.auth.yaml", `schemaVersion: 1
# keep this comment
id: shared
type: bearer
token: "{{secret.token}}"
extensions:
  owner: platform
`)
	if err := p.reloadFromDiskForTest(); err != nil {
		t.Fatal(err)
	}

	entry, err := p.NewRequest("", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	saveRequestAuth(t, p, entry, &AuthRef{Mode: "ref", Ref: "shared"})
	beforeReq := readTestFile(t, root, entry.RelPath)

	scheme, digest, err := p.LoadAuthScheme("shared")
	if err != nil {
		t.Fatal(err)
	}
	if digest == "" || scheme.Type != "bearer" || scheme.Extensions["owner"] != "platform" {
		t.Fatalf("unexpected loaded scheme: %+v digest=%q", scheme, digest)
	}
	scheme.Token = "{{secret.rotatedToken}}"
	if _, err := p.SaveAuthScheme(scheme, digest); err != nil {
		t.Fatalf("SaveAuthScheme: %v", err)
	}
	raw := readTestFile(t, root, "auth/shared.auth.yaml")
	if !strings.Contains(raw, "# keep this comment") {
		t.Fatalf("comment lost on scheme save:\n%s", raw)
	}
	if !strings.Contains(raw, "rotatedToken") {
		t.Fatalf("token not updated:\n%s", raw)
	}
	if got := readTestFile(t, root, entry.RelPath); got != beforeReq {
		t.Fatalf("saving a scheme rewrote a referencing request:\n%s", got)
	}
	if got := p.AuthSchemes["shared"]; got == nil || got.Token != "{{secret.rotatedToken}}" {
		t.Fatalf("in-memory scheme not updated: %+v", got)
	}

	// A duplicate create must not overwrite the existing file.
	if _, err := p.CreateAuthScheme(&AuthScheme{ID: "shared", Type: "bearer", Token: "x"}); !IsConflict(err) {
		t.Fatalf("duplicate create should conflict, got %v", err)
	}

	// External edits are detected instead of overwritten.
	writeTestFile(t, root, "auth/shared.auth.yaml", "schemaVersion: 1\nid: shared\ntype: bearer\ntoken: external\n")
	if _, err := p.SaveAuthScheme(scheme, digest); !IsConflict(err) {
		t.Fatalf("stale baseline should conflict, got %v", err)
	}
	if raw := readTestFile(t, root, "auth/shared.auth.yaml"); !strings.Contains(raw, "external") {
		t.Fatalf("conflicting save overwrote external edit:\n%s", raw)
	}

	// Create + validation paths.
	if _, err := p.CreateAuthScheme(&AuthScheme{ID: "../escape", Type: "bearer"}); err == nil {
		t.Fatal("path-traversal scheme id must be rejected")
	}
	if _, err := p.CreateAuthScheme(&AuthScheme{ID: "new-key", Type: "apikey", Name: "", Value: "v"}); err == nil {
		t.Fatal("apikey without a name must be rejected")
	}
	if _, err := p.CreateAuthScheme(&AuthScheme{ID: "new-key", Type: "apikey", Name: "X-Key", In: "header", Value: "{{secret.k}}"}); err != nil {
		t.Fatalf("CreateAuthScheme: %v", err)
	}
	if p.AuthSchemes["new-key"] == nil {
		t.Fatal("created scheme missing from memory")
	}
}

func TestDeleteAuthSchemeReferenceDiagnosticsAndAtomicClear(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Identity")
	if err != nil {
		t.Fatal(err)
	}
	root := p.Dir

	if _, err := p.CreateAuthScheme(&AuthScheme{ID: "shared", Type: "bearer", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewFolder("", "team"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveFolderAuthPreserving("team", &AuthRef{Mode: "ref", Ref: "shared"}, p.FolderAuthBaseline("team")); err != nil {
		t.Fatal(err)
	}
	setProjectDefaultAuth(t, p, "shared")
	entry, err := p.NewRequest("team", "Team List")
	if err != nil {
		t.Fatal(err)
	}
	saveRequestAuth(t, p, entry, &AuthRef{Mode: "ref", Ref: "shared"})

	refs := p.AuthSchemeReferences("shared")
	if len(refs) != 3 {
		t.Fatalf("expected project+folder+request references, got %+v", refs)
	}

	_, digest, err := p.LoadAuthScheme("shared")
	if err != nil {
		t.Fatal(err)
	}
	refs, err = p.DeleteAuthScheme("shared", digest, false)
	var inUse *AuthSchemeInUseError
	if !errors.As(err, &inUse) {
		t.Fatalf("expected AuthSchemeInUseError, got %v", err)
	}
	if len(refs) != 3 {
		t.Fatalf("in-use error should carry refs: %+v", refs)
	}
	if !p.Store.Exists("auth/shared.auth.yaml") {
		t.Fatal("refused delete removed the scheme anyway")
	}

	if _, err := p.DeleteAuthScheme("shared", digest, true); err != nil {
		t.Fatalf("clear-and-delete: %v", err)
	}
	if p.Store.Exists("auth/shared.auth.yaml") {
		t.Fatal("scheme file still exists after delete")
	}

	reopened, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if reopened.File.Defaults == nil || strings.TrimSpace(reopened.File.Defaults.Auth) != "none" {
		t.Fatalf("project default auth not cleared: %+v", reopened.File.Defaults)
	}
	if fa := reopened.FolderAuth("team"); fa == nil || fa.Mode != "none" {
		t.Fatalf("folder auth not cleared to explicit none: %+v", fa)
	}
	got := reopened.Requests[entry.Request.ID]
	if got == nil || got.Request.Auth == nil || got.Request.Auth.Mode != "none" {
		t.Fatalf("request auth not cleared to explicit none: %+v", got)
	}
	for _, diag := range reopened.IdentityDiagnostics(nil) {
		if strings.Contains(diag.Code, "missing-auth-ref") {
			t.Fatalf("dangling reference after atomic clear: %+v", diag)
		}
	}
	// The request file's non-auth content must survive the rewrite.
	if raw := readTestFile(t, root, entry.RelPath); !strings.Contains(raw, "Team List") {
		t.Fatalf("request content lost during reference clear:\n%s", raw)
	}
}

func TestFolderAuthImpactAndPreservingSave(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Identity")
	if err != nil {
		t.Fatal(err)
	}
	root := p.Dir
	writeTestFile(t, root, "auth/team.auth.yaml", "schemaVersion: 1\nid: team\ntype: bearer\ntoken: t\n")
	// Hand-written collection with a comment to prove preserving saves.
	writeTestFile(t, root, "collections/team/collection.yaml", `schemaVersion: 1
# team folder
id: team
name: Team
variables:
  region: eu
`)
	writeTestFile(t, root, "collections/team/list.request.yaml", `schemaVersion: 1
id: req.team.list
name: Team List
http:
  method: GET
  url: https://example.com/team
`)
	writeTestFile(t, root, "collections/team/public.request.yaml", `schemaVersion: 1
id: req.team.public
name: Public
auth: none
http:
  method: GET
  url: https://example.com/public
`)
	if err := p.reloadFromDiskForTest(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewFolder("team", "deep"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewRequest("team/deep", "Deep List"); err != nil {
		t.Fatal(err)
	}

	if _, err := p.SaveFolderAuthPreserving("team", &AuthRef{Mode: "ref", Ref: "team"}, p.FolderAuthBaseline("team")); err != nil {
		t.Fatalf("SaveFolderAuthPreserving: %v", err)
	}
	raw := readTestFile(t, root, "collections/team/collection.yaml")
	if !strings.Contains(raw, "# team folder") || !strings.Contains(raw, "region: eu") {
		t.Fatalf("folder comment/variables lost:\n%s", raw)
	}
	impact := p.FolderAuthImpact("team")
	paths := map[string]bool{}
	for _, r := range impact {
		paths[r.Path] = true
	}
	if !paths["collections/team/list.request.yaml"] || !paths["collections/team/deep/deep-list.request.yaml"] {
		t.Fatalf("expected both inheriting requests in impact, got %+v", impact)
	}
	if paths["collections/team/public.request.yaml"] {
		t.Fatalf("explicit none request must not be reported as affected: %+v", impact)
	}

	// External edit is reported rather than overwritten.
	digest := p.FolderAuthBaseline("team")
	writeTestFile(t, root, "collections/team/collection.yaml", "schemaVersion: 1\nid: team\nname: Team\nauth: none\n")
	if _, err := p.SaveFolderAuthPreserving("team", &AuthRef{Mode: "none"}, digest); !IsConflict(err) {
		t.Fatalf("stale folder baseline should conflict, got %v", err)
	}
	if raw := readTestFile(t, root, "collections/team/collection.yaml"); !strings.Contains(raw, "auth: none") {
		t.Fatalf("conflict overwrote external folder edit:\n%s", raw)
	}

	// A folder created in this session has no recorded baseline; the matching
	// in-memory copy is adopted safely.
	if _, err := p.NewFolder("", "fresh"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.SaveFolderAuthPreserving("fresh", &AuthRef{Mode: "ref", Ref: "team"}, ""); err != nil {
		t.Fatalf("baseline adoption for a newly created folder failed: %v", err)
	}
}

func TestSecretReferencesAndDiagnosticsNeverLeakValues(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Identity")
	if err != nil {
		t.Fatal(err)
	}
	root := p.Dir
	writeTestFile(t, root, "environments/dev.yaml", `schemaVersion: 1
id: dev
variables: {}
secrets:
  - apiToken
  - folderPass
  - reqKey
`)
	writeTestFile(t, root, "auth/shared.auth.yaml", "schemaVersion: 1\nid: shared\ntype: bearer\ntoken: \"{{secret.apiToken}}\"\n")
	writeTestFile(t, root, "collections/team/collection.yaml", `schemaVersion: 1
id: team
name: Team
auth:
  type: basic
  username: alice
  password: "{{secret.folderPass}}"
`)
	writeTestFile(t, root, "collections/team/a.request.yaml", `schemaVersion: 1
id: req.a
name: A
auth:
  type: apikey
  name: X-Req-Key
  in: header
  value: "{{secret.reqKey}}"
http:
  method: GET
  url: https://example.com
`)
	if err := p.reloadFromDiskForTest(); err != nil {
		t.Fatal(err)
	}

	secrets := MapSecrets{"apiToken": "ULTRA-SECRET-123"}
	refs := p.SecretReferences(secrets)
	byName := map[string]SecretReference{}
	for _, r := range refs {
		byName[r.Name] = r
	}
	if len(refs) != 3 {
		t.Fatalf("expected three declared secrets, got %+v", refs)
	}
	if byName["apiToken"].State != SecretStateBound {
		t.Fatalf("apiToken should be bound: %+v", byName["apiToken"])
	}
	if byName["folderPass"].State != SecretStateMissing || byName["reqKey"].State != SecretStateMissing {
		t.Fatalf("unbound secrets should be missing: %+v", refs)
	}
	if !containsString(byName["apiToken"].Sources, "environment:dev") ||
		!containsString(byName["apiToken"].Sources, "auth:shared") {
		t.Fatalf("apiToken sources incomplete: %+v", byName["apiToken"].Sources)
	}
	if !containsString(byName["folderPass"].Sources, "collections/team/collection.yaml") {
		t.Fatalf("folder secret source missing: %+v", byName["folderPass"].Sources)
	}
	if !containsString(byName["reqKey"].Sources, "collections/team/a.request.yaml") {
		t.Fatalf("request secret source missing: %+v", byName["reqKey"].Sources)
	}

	diags := p.IdentityDiagnostics(secrets)
	var unbound []string
	for _, d := range diags {
		if d.Code == "secret-unbound" {
			unbound = append(unbound, d.Message)
		}
		for _, hay := range []string{d.Message, d.Path} {
			if strings.Contains(hay, "ULTRA-SECRET-123") {
				t.Fatalf("secret value leaked into diagnostics: %+v", d)
			}
		}
	}
	if len(unbound) != 2 {
		t.Fatalf("expected two unbound diagnostics, got %+v", diags)
	}

	unavailable := secretInspectorStub{SecretStatus{Name: "all", State: SecretStateUnavailable, Backend: "keychain", Detail: "locked"}}
	diags = p.IdentityDiagnostics(unavailable)
	foundBackend := false
	for _, d := range diags {
		if d.Code == "secret-backend" && strings.Contains(d.Message, "keychain") {
			foundBackend = true
		}
	}
	if !foundBackend {
		t.Fatalf("unavailable backend not diagnosed: %+v", diags)
	}
}

type secretInspectorStub struct {
	status SecretStatus
}

func (s secretInspectorStub) Get(string) (string, bool) { return "", false }
func (s secretInspectorStub) InspectSecret(name string) SecretStatus {
	st := s.status
	st.Name = name
	return st
}

func containsString(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func TestTargetConfirmationIsExactAndOneShot(t *testing.T) {
	c, err := NewTargetConfirmation("req.orders", "POST", "prod", "https://api.example.com/v1/orders?limit=10#frag", []string{"production write"})
	if err != nil {
		t.Fatal(err)
	}
	if c.Origin != "https://api.example.com:443" {
		t.Fatalf("unexpected origin %q", c.Origin)
	}
	if !c.Matches("req.orders", "post", "prod", "https://api.example.com/v1/orders?limit=10") {
		t.Fatal("exact target with method case and fragment difference should match")
	}
	cases := []struct{ req, method, env, url string }{
		{"req.other", "POST", "prod", "https://api.example.com/v1/orders?limit=10"},
		{"req.orders", "PUT", "prod", "https://api.example.com/v1/orders?limit=10"},
		{"req.orders", "POST", "staging", "https://api.example.com/v1/orders?limit=10"},
		{"req.orders", "POST", "prod", "https://api.example.com/v1/orders?limit=11"},
		{"req.orders", "POST", "prod", "https://evil.example.com/v1/orders?limit=10"},
		{"req.orders", "POST", "prod", "https://api.example.com:8443/v1/orders?limit=10"},
	}
	for _, tc := range cases {
		if c.Matches(tc.req, tc.method, tc.env, tc.url) {
			t.Fatalf("confirmation unexpectedly matched %+v", tc)
		}
	}
	if !c.Consume("req.orders", "POST", "prod", "https://api.example.com/v1/orders?limit=10") {
		t.Fatal("first consume should succeed")
	}
	if c.Consume("req.orders", "POST", "prod", "https://api.example.com/v1/orders?limit=10") {
		t.Fatal("confirmation must be one-shot")
	}
	if !c.IsConsumed() {
		t.Fatal("IsConsumed should report true")
	}
	if _, err := NewTargetConfirmation("r", "GET", "dev", "/relative", nil); err == nil {
		t.Fatal("relative target must be rejected")
	}
}

func TestValidateSecretName(t *testing.T) {
	for _, bad := range []string{"", "   ", "secret.api", "data.x", "steps.y", "a{b", "a\x00b"} {
		if err := ValidateSecretName(bad); err == nil {
			t.Errorf("expected %q to be rejected", bad)
		}
	}
	for _, ok := range []string{"apiToken", "db.password", "GOREQUEST-1"} {
		if err := ValidateSecretName(ok); err != nil {
			t.Errorf("expected %q to be accepted: %v", ok, err)
		}
	}
}

func TestOneShotConfirmGateDoesNotAuthorizeRedirect(t *testing.T) {
	var destHits atomic.Int32
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destHits.Add(1)
		w.WriteHeader(204)
	}))
	defer dest.Close()

	initial := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/final", http.StatusFound)
	}))
	defer initial.Close()

	policy := MergeTargetPolicy(&DeviceTargetPolicy{ProductionEnvs: []string{"prod"}}, nil)
	token, err := NewTargetConfirmation("req.orders", "POST", "prod", initial.URL+"/orders", []string{"production write"})
	if err != nil {
		t.Fatal(err)
	}
	if !token.Consume("req.orders", "POST", "prod", initial.URL+"/orders") {
		t.Fatal("token should be consumable for the exact initial target")
	}
	gate := NewOneShotConfirmGate(token, "req.orders", "prod")

	plan := &Plan{Method: http.MethodPost, URL: initial.URL + "/orders", Timeout: 5 * time.Second}
	// A production POST at Hop 0 needs confirmation; the gate supplies it.
	// The redirect destination is evaluated independently with Confirmed=false.
	SetPlanConfirmGate(plan, gate)
	hop0 := ExecuteOptions{
		Redirect:        RedirectPolicy{Mode: RedirectFollow, MaxHops: 5},
		TargetPolicy:    policy,
		HasTargetPolicy: true,
		EnvID:           "prod",
		Interactive:     true,
	}
	plan.Network = &hop0
	// The gate itself is always exact-scope and one-shot. When the integrator
	// transport patch is applied, ExecuteWithOptions consumes it for Hop 0 and
	// reaches the redirect destination. Without the patch, Execute must fail
	// closed rather than authorize a production target.
	res, err := ExecuteWithOptions(context.Background(), plan, hop0)
	patched := err == nil && res != nil && len(res.Redirects) == 1 && destHits.Load() == 1
	if err != nil && !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("unpatched core must fail closed with an explicit confirmation error, got %v", err)
	}
	if !patched && !gate.AuthorizeInitialHop("POST", initial.URL+"/orders") {
		t.Fatal("unpatched core must leave the one-shot gate unconsumed")
	}
	if patched && gate.AuthorizeInitialHop("POST", initial.URL+"/orders") {
		t.Fatal("patched core must consume the gate exactly once")
	}

	// A second send with the same gate can never reuse the prior approval,
	// whether or not the first send was authorized.
	repeat := ExecuteOptions{
		TargetPolicy:    policy,
		HasTargetPolicy: true,
		EnvID:           "prod",
		Interactive:     true,
	}
	plan.Network = &repeat
	_, err = ExecuteWithOptions(context.Background(), plan, repeat)
	if err == nil || !strings.Contains(err.Error(), "requires confirmation") {
		t.Fatalf("stale gate must not bypass confirmation, got %v", err)
	}
}
