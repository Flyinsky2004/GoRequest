package core

import (
	"encoding/base64"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

type stubUnavailableSecrets struct {
	detail string
}

func (s stubUnavailableSecrets) Get(string) (string, bool) { return "", false }

func (s stubUnavailableSecrets) InspectSecret(name string) SecretStatus {
	return SecretStatus{
		Name:    name,
		State:   SecretStateUnavailable,
		Backend: "keychain",
		Detail:  s.detail,
	}
}

func TestVariableSevenLayerPrecedenceAndOriginChain(t *testing.T) {
	dir := t.TempDir()
	gq := filepath.Join(dir, ".gorequest")
	for _, sub := range []string{"collections/api/users", "environments", "docs", "auth"} {
		if err := os.MkdirAll(filepath.Join(gq, filepath.FromSlash(sub)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(gq, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	write("project.yaml", `schemaVersion: 1
id: var-layers
name: Variable Layers
variables:
  tier: from-project
  projOnly: p-val
  user.id: usr-project
`)
	write("environments/dev.yaml", `schemaVersion: 1
id: dev
variables:
  tier: from-env
  envOnly: e-val
secrets:
  - apiToken
  - unusedSecret
`)
	write("collections/api/collection.yaml", `schemaVersion: 1
id: api
name: API
variables:
  tier: from-outer-folder
  folderScope: outer
`)
	write("collections/api/users/collection.yaml", `schemaVersion: 1
id: users
name: Users
variables:
  tier: from-inner-folder
  folderScope: inner
  authHeaderPreview: "Bearer {{secret.apiToken}}"
`)
	write("collections/api/users/get.request.yaml", `schemaVersion: 1
id: users.get
name: Get User
protocol: http
variables:
  tier: from-request
  user.id: usr-request
http:
  method: GET
  url: "https://api.example.com/users/{{user.id}}"
`)

	p, err := OpenProject(dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	entry := p.Requests["users.get"]
	if entry == nil {
		t.Fatal("missing users.get request")
	}

	secrets := MapSecrets{"apiToken": "TOP-SECRET-TOKEN-999"}

	// Static 4 layers first: request wins over inner folder > outer folder > env > project.
	inspStatic, err := p.InspectVariables(entry, "dev", secrets, nil)
	if err != nil {
		t.Fatalf("InspectVariables static: %v", err)
	}
	tierEntry := inspStatic.ByName["tier"]
	if tierEntry.ResolvedValue != "from-request" || tierEntry.Effective.Layer != LayerRequest {
		t.Fatalf("static tier winner = %+v, want from-request", tierEntry)
	}
	if len(tierEntry.Overridden) != 4 {
		t.Fatalf("expected 4 overridden layers on tier, got %+v", tierEntry.Overridden)
	}
	wantOverridden := []string{
		"project",
		"environment:dev",
		"folder:collections/api",
		"folder:collections/api/users",
	}
	for i, want := range wantOverridden {
		if got := tierEntry.Overridden[i].Label(); got != want {
			t.Fatalf("overridden[%d] = %q, want %q", i, got, want)
		}
	}

	// Non-reserved dotted variable user.id works and tracks override chain.
	uid := inspStatic.ByName["user.id"]
	if uid.ResolvedValue != "usr-request" || len(uid.Overridden) != 1 || uid.Overridden[0].Layer != LayerProject {
		t.Fatalf("dotted variable user.id broken: %+v", uid)
	}

	// Tainted variable referencing {{secret.apiToken}} masks ResolvedValue and DisplayValue!
	tainted := inspStatic.ByName["authHeaderPreview"]
	if !tainted.Sensitive || tainted.ResolvedValue != "" || tainted.DisplayValue != "***" {
		t.Fatalf("tainted variable leaked or lost taint: %+v", tainted)
	}
	if strings.Contains(tainted.DisplayValue, "TOP-SECRET-TOKEN-999") || strings.Contains(tainted.ResolvedValue, "TOP-SECRET-TOKEN-999") {
		t.Fatal("secret leaked into VariableInspection")
	}

	// SecretStatuses include both bound apiToken and missing unusedSecret without values.
	if len(inspStatic.Secrets) != 2 {
		t.Fatalf("expected 2 secret statuses, got %+v", inspStatic.Secrets)
	}
	if inspStatic.Secrets[0].Name != "apiToken" || inspStatic.Secrets[0].State != SecretStateBound {
		t.Fatalf("unexpected apiToken status: %+v", inspStatic.Secrets[0])
	}
	if inspStatic.Secrets[1].Name != "unusedSecret" || inspStatic.Secrets[1].State != SecretStateMissing {
		t.Fatalf("unexpected unusedSecret status: %+v", inspStatic.Secrets[1])
	}

	// Add runtime layers: run -> step -> override.
	opts := &ResolveOptions{
		RunVars:      map[string]string{"tier": "from-run", "runOnly": "r-val"},
		StepVars:     map[string]string{"tier": "from-step", "stepOnly": "s-val"},
		OverrideVars: map[string]string{"tier": "from-override"},
	}
	merged7, _, err := p.ResolveVarsWith(entry, "dev", opts)
	if err != nil {
		t.Fatalf("ResolveVarsWith: %v", err)
	}
	if merged7["tier"] != "from-override" || merged7["runOnly"] != "r-val" || merged7["stepOnly"] != "s-val" {
		t.Fatalf("7-layer merge mismatch: %+v", merged7)
	}
	insp7, err := p.InspectVariables(entry, "dev", secrets, opts)
	if err != nil {
		t.Fatalf("InspectVariables 7-layer: %v", err)
	}
	if insp7.ByName["tier"].Effective.Layer != LayerOverride || len(insp7.ByName["tier"].Overridden) != 7 {
		t.Fatalf("7-layer override history mismatch: %+v", insp7.ByName["tier"])
	}
}

func TestReservedNamespacesCannotBeSpoofedAndProtectAgainstTemplateInjection(t *testing.T) {
	vars := map[string]string{
		"secret.realKey":   "SPOOFED-PLAIN-SECRET",
		"data.rowId":       "SPOOFED-DATA",
		"steps.auth.token": "SPOOFED-STEP",
		"user.id":          "42",
	}
	secrets := MapSecrets{"realKey": "ACTUAL-KEYCHAIN-SECRET-888"}

	// 1. Without DataVars / StepOutputs, data.* and steps.* fail as ConfigError
	// rather than reading spoofed keys in vars, while secret.* reads SecretProvider.
	rPlain := NewResolver(vars, secrets)
	tv, err := rPlain.ResolveTracked("{{secret.realKey}}:{{user.id}}")
	if err != nil {
		t.Fatalf("ResolveTracked secret+dotted: %v", err)
	}
	if tv.Value != "ACTUAL-KEYCHAIN-SECRET-888:42" || !tv.Sensitive {
		t.Fatalf("unexpected tracked value: %+v", tv)
	}
	if _, err := rPlain.Resolve("{{data.rowId}}"); !IsConfig(err) {
		t.Fatalf("expected ConfigError for {{data.rowId}} outside dataset run, got %v", err)
	}
	if _, err := rPlain.Resolve("{{steps.auth.token}}"); !IsConfig(err) {
		t.Fatalf("expected ConfigError for {{steps.auth.token}} outside suite run, got %v", err)
	}

	// 2. With DataVars and StepOutputs attached:
	// Verify untrusted HTTP response extracted into steps.auth.echo containing
	// "{{secret.realKey}}" is NOT re-expanded into the secret!
	rRun := NewResolver(vars, secrets).
		WithDataVars(map[string]string{"rowId": "row-007"}).
		WithStepOutputs(map[string]StepOutput{
			"auth.token": {Value: "STEP-JWT-777", Sensitive: true},
			"auth.echo":  {Value: "{{secret.realKey}}", Sensitive: false},
		})

	tvData, err := rRun.ResolveTracked("id={{data.rowId}}")
	if err != nil || tvData.Value != "id=row-007" || tvData.Sensitive {
		t.Fatalf("data resolution mismatch: tv=%+v err=%v", tvData, err)
	}
	tvStep, err := rRun.ResolveTracked("Bearer {{steps.auth.token}}")
	if err != nil || tvStep.Value != "Bearer STEP-JWT-777" || !tvStep.Sensitive {
		t.Fatalf("step sensitive taint mismatch: tv=%+v err=%v", tvStep, err)
	}
	tvEcho, err := rRun.ResolveTracked("echo={{steps.auth.echo}}")
	if err != nil {
		t.Fatalf("step echo failed: %v", err)
	}
	if tvEcho.Value != "echo={{secret.realKey}}" || tvEcho.Sensitive {
		t.Fatalf("template injection from step output must not expand secrets: %+v", tvEcho)
	}

	// 3. Unavailable secret store surfaces backend state in ConfigError.
	rUnavail := NewResolver(nil, stubUnavailableSecrets{detail: "keychain is locked"})
	_, err = rUnavail.Resolve("{{secret.realKey}}")
	if !IsConfig(err) || !strings.Contains(err.Error(), "unavailable") || !strings.Contains(err.Error(), "keychain is locked") {
		t.Fatalf("expected unavailable keychain ConfigError, got %v", err)
	}
}

func TestFolderAuthInheritanceClearAndPersistence(t *testing.T) {
	dir := t.TempDir()
	p, err := NewProject(dir, "Folder Auth Demo")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	gq := filepath.Join(dir, ".gorequest")
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(gq, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	// Configure project default auth + two reusable schemes.
	write("project.yaml", `schemaVersion: 1
id: folder-auth-demo
name: Folder Auth Demo
variables:
  baseUrl: "https://api.example.com"
defaults:
  timeout: 10s
  auth: proj-bearer
`)
	write("auth/proj-bearer.auth.yaml", `schemaVersion: 1
id: proj-bearer
type: bearer
token: "{{secret.projTok}}"
`)
	write("auth/v1-apikey.auth.yaml", `schemaVersion: 1
id: v1-apikey
type: apikey
name: X-V1-Key
in: header
value: "{{secret.v1Key}}"
`)

	p, err = OpenProject(dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	if _, err := p.NewFolder("", "v1"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewFolder("v1", "public"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewFolder("v1/public", "admin"); err != nil {
		t.Fatal(err)
	}

	rootReq, err := p.NewRequest("", "Root Ping")
	if err != nil {
		t.Fatal(err)
	}
	v1Req, err := p.NewRequest("v1", "List Items")
	if err != nil {
		t.Fatal(err)
	}
	pubReq, err := p.NewRequest("v1/public", "Health")
	if err != nil {
		t.Fatal(err)
	}
	admInheritReq, err := p.NewRequest("v1/public/admin", "Admin Panel")
	if err != nil {
		t.Fatal(err)
	}
	admNoneReq, err := p.NewRequest("v1/public/admin", "Admin Public")
	if err != nil {
		t.Fatal(err)
	}
	admNoneCopy := *admNoneReq.Request
	admNoneCopy.Auth = &AuthRef{Mode: "none"}
	if _, err := p.SaveRequest(admNoneReq, &admNoneCopy, admNoneReq.BaselineDigest); err != nil {
		t.Fatal(err)
	}

	// Persist folder auth at 3 nested levels:
	// v1 -> ref: v1-apikey
	// v1/public -> none (clears v1-apikey & proj-bearer)
	// v1/public/admin -> inline basic
	dV1, err := p.SaveFolderAuth("v1", &AuthRef{Mode: "ref", Ref: "v1-apikey"}, "")
	if err != nil {
		t.Fatalf("SaveFolderAuth v1: %v", err)
	}
	if _, err := p.SaveFolderAuth("v1/public", &AuthRef{Mode: "none"}, ""); err != nil {
		t.Fatalf("SaveFolderAuth v1/public: %v", err)
	}
	if _, err := p.SaveFolderAuth("v1/public/admin", &AuthRef{
		Mode: "inline",
		Inline: &AuthScheme{
			SchemaVersion: 1,
			Type:          "basic",
			Username:      "ops",
			Password:      "{{secret.opsPass}}",
		},
	}, ""); err != nil {
		t.Fatalf("SaveFolderAuth v1/public/admin: %v", err)
	}

	// Stale baseline must fail with ConflictError.
	if _, err := p.SaveFolderAuth("v1", &AuthRef{Mode: "none"}, "stale-digest"); !IsConflict(err) {
		t.Fatalf("expected ConflictError on stale baseline, got %v (valid=%s)", err, dV1)
	}

	// Reopen project from disk and call LoadFolderIdentities to verify full round-trip.
	reloaded, err := OpenProject(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	issues, err := reloaded.LoadFolderIdentities()
	if err != nil {
		t.Fatalf("LoadFolderIdentities: %v", err)
	}
	if ErrorCount(issues) != 0 {
		t.Fatalf("expected 0 validation errors after LoadFolderIdentities, got %+v", issues)
	}

	// 1. Root request inherits project defaults.auth ("proj-bearer").
	effRoot, err := reloaded.ResolveEffectiveAuth(reloaded.Requests[rootReq.Request.ID], nil)
	if err != nil || effRoot.SourceKind != "project" || effRoot.RefID != "proj-bearer" || effRoot.Scheme == nil {
		t.Fatalf("root effective auth = %+v, err=%v", effRoot, err)
	}

	// 2. v1 request inherits folder v1 ("v1-apikey").
	effV1, err := reloaded.ResolveEffectiveAuth(reloaded.Requests[v1Req.Request.ID], nil)
	if err != nil || effV1.SourceKind != "folder" || effV1.RefID != "v1-apikey" || effV1.SourcePath != "collections/v1/collection.yaml" {
		t.Fatalf("v1 effective auth = %+v, err=%v", effV1, err)
	}

	// 3. v1/public request inherits folder v1/public's explicit "none" -> Scheme == nil!
	effPub, err := reloaded.ResolveEffectiveAuth(reloaded.Requests[pubReq.Request.ID], nil)
	if err != nil || effPub.SourceKind != "folder" || effPub.Mode != "none" || effPub.Scheme != nil {
		t.Fatalf("v1/public should clear inherited auth: %+v, err=%v", effPub, err)
	}

	// 4. v1/public/admin request inherits inner folder inline basic!
	effAdm, err := reloaded.ResolveEffectiveAuth(reloaded.Requests[admInheritReq.Request.ID], nil)
	if err != nil || effAdm.SourceKind != "folder" || effAdm.Mode != "inline" || effAdm.Scheme == nil || effAdm.Scheme.Type != "basic" {
		t.Fatalf("v1/public/admin effective auth = %+v, err=%v", effAdm, err)
	}

	// 5. Request with explicit auth: none overrides inner folder inline basic!
	effAdmNone, err := reloaded.ResolveEffectiveAuth(reloaded.Requests[admNoneReq.Request.ID], nil)
	if err != nil || effAdmNone.SourceKind != "request" || effAdmNone.Mode != "none" || effAdmNone.Scheme != nil {
		t.Fatalf("request explicit none should clear folder auth: %+v, err=%v", effAdmNone, err)
	}

	// 6. ValidateIdentity catches broken folder auth refs.
	if err := reloaded.SetFolderAuth("v1", &AuthRef{Mode: "ref", Ref: "missing-scheme"}); err != nil {
		t.Fatal(err)
	}
	idIssues := reloaded.ValidateIdentity()
	foundFolderRefErr := false
	for _, iss := range idIssues {
		if iss.Code == "folder-auth-ref" && iss.Path == "collections/v1/collection.yaml" {
			foundFolderRefErr = true
		}
	}
	if !foundFolderRefErr {
		t.Fatalf("expected folder-auth-ref issue, got %+v", idIssues)
	}
}

func TestRedactorMultiEncodingAndBuildTrackedPlanNoSecretLeak(t *testing.T) {
	rawSecret := `S3cr3t+/="Value"&Key`
	basicPass := `P@ss:w0rd!`
	shortKey := "k9" // < 4 chars: must still be masked in structured API Key slot!

	var bodyNode yaml.Node
	if err := yaml.Unmarshal([]byte(`{"apiSecret": "{{secret.main}}", "nested": ["{{steps.login.session}}"]}`), &bodyNode); err != nil {
		t.Fatal(err)
	}

	req := &Request{
		SchemaVersion: 1,
		ID:            "redact-check",
		Name:          "Redact Check",
		HTTP: &HTTPDef{
			Method: "POST",
			URL:    "https://api.example.com/v1/items?echo={{secret.main}}",
			Headers: []KV{
				{Name: "X-Trace", Value: "trace-{{secret.main}}"},
			},
			Body: &Body{
				Type:  "json",
				Value: &bodyNode,
			},
		},
	}
	scheme := &AuthScheme{
		SchemaVersion: 1,
		Type:          "apikey",
		Name:          "X-Tiny-Key",
		In:            "header",
		Value:         "{{secret.short}}",
	}
	secrets := MapSecrets{
		"main":      rawSecret,
		"basicPass": basicPass,
		"short":     shortKey,
	}
	opts := &ResolveOptions{
		StepOutputs: map[string]StepOutput{
			"login.session": {Value: "SESSION-TKN-98765", Sensitive: true},
		},
	}

	plan, red, err := BuildTrackedPlan(req, nil, secrets, scheme, opts)
	if err != nil {
		t.Fatalf("BuildTrackedPlan: %v", err)
	}
	red.RegisterBasicPair("alice", basicPass)

	// Real plan still carries real credentials for actual network transmission!
	if !strings.Contains(string(plan.Body), "SESSION-TKN-98765") {
		t.Fatalf("live plan must carry resolved body for HTTP execution: %s", string(plan.Body))
	}

	// Sanitized plan must scrub every secret, custom short API key header, URL query, and JSON-escaped body!
	safePlan := red.SanitizePlan(plan)
	serializedSafePlan := safePlan.URL + "\n" + string(safePlan.Body)
	for _, h := range safePlan.Headers {
		serializedSafePlan += "\n" + h[0] + ": " + h[1]
		if h[0] == "X-Tiny-Key" && h[1] != "***" {
			t.Fatalf("short custom API key header was not masked: %+v", h)
		}
	}
	for _, leak := range []string{
		rawSecret,
		url.QueryEscape(rawSecret),
		`S3cr3t+/=\"Value\"&Key`,
		"SESSION-TKN-98765",
	} {
		if strings.Contains(serializedSafePlan, leak) {
			t.Fatalf("sanitized plan leaked %q:\n%s", leak, serializedSafePlan)
		}
	}

	// Verify Base64, RawURLEncoding, URL-escaped, and Basic user:pass encodings in ExecResult & errors!
	basicB64 := base64.StdEncoding.EncodeToString([]byte("alice:" + basicPass))
	urlB64 := base64.RawURLEncoding.EncodeToString([]byte(rawSecret))
	res := &ExecResult{
		Status: 200,
		Headers: [][2]string{
			{"Set-Cookie", "sid=" + rawSecret + "; Path=/; HttpOnly"},
			{"X-Echo-Basic", basicB64},
		},
		Body:   []byte(`{"b64":"` + urlB64 + `","basic":"` + basicB64 + `"}`),
		Pretty: "{\n  \"b64\": \"" + urlB64 + "\"\n}",
	}
	safeRes := red.SanitizeExecResult(res)
	combinedRes := string(safeRes.Body) + "\n" + safeRes.Pretty
	for _, h := range safeRes.Headers {
		combinedRes += "\n" + h[0] + ": " + h[1]
	}
	for _, leak := range []string{rawSecret, basicPass, basicB64, urlB64} {
		if strings.Contains(combinedRes, leak) {
			t.Fatalf("sanitized ExecResult leaked %q:\n%s", leak, combinedRes)
		}
	}

	// Verify BuildTrackedPlan redacts secrets out of ConfigError when URL validation fails!
	badReq := &Request{
		SchemaVersion: 1,
		ID:            "bad-url",
		Name:          "Bad URL",
		HTTP: &HTTPDef{
			Method: "GET",
			URL:    "{{secret.main}}", // resolves to rawSecret, then fails "must be absolute"
		},
	}
	_, _, buildErr := BuildTrackedPlan(badReq, nil, secrets, nil, nil)
	if !IsConfig(buildErr) {
		t.Fatalf("expected *ConfigError preserved after RedactError, got %T (%v)", buildErr, buildErr)
	}
	if strings.Contains(buildErr.Error(), rawSecret) || strings.Contains(buildErr.Error(), "S3cr3t") {
		t.Fatalf("secret leaked into BuildTrackedPlan error: %v", buildErr)
	}
}

func TestTargetPolicyMonotonicMergeAndCrossOriginRedirectStripping(t *testing.T) {
	device := &DeviceTargetPolicy{
		ProductionAction:        ActionConfirm,
		ProductionHosts:         []string{"*.prod.corp.internal"},
		AllowedHosts:            []string{"*.corp.internal", "127.0.0.1"},
		DeniedHosts:             []string{"vault.corp.internal"},
		AllowInsecureTLS:        false,
		AllowPlainHTTPWithCreds: false,
	}
	// Malicious or misconfigured project attempts to weaken every guardrail:
	// - lowers ProductionAction to ActionAllow
	// - adds external "attacker.example.com" and device-denied "vault.corp.internal" to AllowedHosts
	// - tries to set RequireHTTPSForCreds = false
	project := &ProjectTargetPolicy{
		ProductionAction:     ActionAllow,
		ProductionEnvs:       []string{"live-eu"},
		AllowedHosts:         []string{"attacker.example.com", "vault.corp.internal", "api.corp.internal", "orders.prod.corp.internal", "127.0.0.1"},
		RequireHTTPSForCreds: BoolPtr(false),
	}

	merged := MergeTargetPolicy(device, project)
	if merged.ProductionAction != ActionConfirm {
		t.Fatalf("project weakened ProductionAction to %q, want %q", merged.ProductionAction, ActionConfirm)
	}
	if !merged.EnforceTLSVerify {
		t.Fatal("EnforceTLSVerify must remain true")
	}
	if !merged.RequireHTTPSForCreds {
		t.Fatal("project must not be able to disable RequireHTTPSForCreds")
	}

	// 1. Host outside device allowlist ("attacker.example.com") -> DENIED even though listed in project AllowedHosts!
	if dec, err := EvaluateTarget(merged, TargetCheckInput{
		URL:         "https://attacker.example.com/steal",
		Method:      "GET",
		Interactive: true,
	}); !IsConfig(err) || dec.Outcome != DecisionDeny {
		t.Fatalf("expected attacker.example.com denied, got dec=%+v err=%v", dec, err)
	}

	// 2. Device-denied host ("vault.corp.internal") -> DENIED!
	if dec, err := EvaluateTarget(merged, TargetCheckInput{
		URL:         "https://vault.corp.internal/keys",
		Method:      "GET",
		Interactive: true,
	}); !IsConfig(err) || dec.Outcome != DecisionDeny {
		t.Fatalf("expected vault.corp.internal denied, got dec=%+v err=%v", dec, err)
	}

	// 3. Loopback plain HTTP with credentials -> ALLOWED for local dev!
	if dec, err := EvaluateTarget(merged, TargetCheckInput{
		URL:            "http://127.0.0.1:4010/dev",
		Method:         "POST",
		EnvID:          "dev",
		HasCredentials: true,
		Interactive:    false,
	}); err != nil || dec.Outcome != DecisionAllow {
		t.Fatalf("loopback HTTP with credentials should be allowed, got dec=%+v err=%v", dec, err)
	}

	// 4. Non-loopback plain HTTP with credentials -> ConfirmRequired in UI, ConfigError in headless runner!
	decUI, err := EvaluateTarget(merged, TargetCheckInput{
		URL:            "http://api.corp.internal/v1",
		Method:         "GET",
		EnvID:          "dev",
		HasCredentials: true,
		Interactive:    true,
	})
	if err != nil || decUI.Outcome != DecisionConfirmRequired {
		t.Fatalf("expected DecisionConfirmRequired for plain HTTP creds in UI, got dec=%+v err=%v", decUI, err)
	}
	if _, err := EvaluateTarget(merged, TargetCheckInput{
		URL:            "http://api.corp.internal/v1",
		Method:         "GET",
		EnvID:          "dev",
		HasCredentials: true,
		Interactive:    false,
	}); !IsConfig(err) {
		t.Fatalf("expected ConfigError for plain HTTP creds in headless mode, got %v", err)
	}

	// 5. Production mutating request (via project-added "live-eu" env or *.prod.corp.internal host):
	// Safe GET is allowed; mutating DELETE requires confirmation in UI and errors in headless mode.
	if dec, err := EvaluateTarget(merged, TargetCheckInput{
		URL:         "https://orders.prod.corp.internal/items",
		Method:      "GET",
		EnvID:       "live-eu",
		Interactive: false,
	}); err != nil || dec.Outcome != DecisionAllow {
		t.Fatalf("safe GET to prod under ActionConfirm should allow, got dec=%+v err=%v", dec, err)
	}
	if dec, err := EvaluateTarget(merged, TargetCheckInput{
		URL:         "https://orders.prod.corp.internal/items/1",
		Method:      "DELETE",
		EnvID:       "live-eu",
		Interactive: true,
		Confirmed:   false,
	}); err != nil || dec.Outcome != DecisionConfirmRequired {
		t.Fatalf("unconfirmed DELETE to prod should require confirm, got dec=%+v err=%v", dec, err)
	}
	if dec, err := EvaluateTarget(merged, TargetCheckInput{
		URL:         "https://orders.prod.corp.internal/items/1",
		Method:      "DELETE",
		EnvID:       "live-eu",
		Interactive: true,
		Confirmed:   true,
	}); err != nil || dec.Outcome != DecisionAllow {
		t.Fatalf("confirmed DELETE to prod should proceed, got dec=%+v err=%v", dec, err)
	}

	// 6. Cross-origin and scheme-downgrade redirect credential stripping (including custom API Key header!).
	apiKeyScheme := &AuthScheme{Type: "apikey", Name: "X-Partner-Token", In: "header"}
	origHeaders := [][2]string{
		{"Accept", "application/json"},
		{"Authorization", "Bearer s3cr3t"},
		{"Cookie", "sid=abc"},
		{"X-Partner-Token", "partner-secret-123"},
	}

	// 6a. Same-origin relative redirect keeps credentials.
	keptSame, nextSame, _, err := EvaluateRedirectHop(
		merged,
		"https://api.corp.internal/v1/old",
		"/v2/new",
		"GET", "dev",
		origHeaders, apiKeyScheme,
		false, false,
	)
	if err != nil || nextSame != "https://api.corp.internal/v2/new" || len(keptSame) != 4 {
		t.Fatalf("same-origin redirect should preserve headers: next=%q kept=%+v err=%v", nextSame, keptSame, err)
	}

	// 6b. Same-host HTTPS -> HTTP downgrade strips Authorization, Cookie, AND X-Partner-Token!
	keptDowngrade, stripped, err := StripCrossOriginCredentials(
		"https://api.corp.internal/v1/old",
		"http://api.corp.internal/v1/old",
		origHeaders,
		apiKeyScheme,
	)
	if err != nil || !stripped || len(keptDowngrade) != 1 || keptDowngrade[0][0] != "Accept" {
		t.Fatalf("HTTPS->HTTP downgrade must strip all credential headers including custom API Key: stripped=%v kept=%+v err=%v", stripped, keptDowngrade, err)
	}

	// 6c. Cross-host redirect to disallowed host is blocked by EvaluateRedirectHop!
	if _, _, _, err := EvaluateRedirectHop(
		merged,
		"https://api.corp.internal/v1/start",
		"https://vault.corp.internal/secret",
		"GET", "dev",
		origHeaders, apiKeyScheme,
		true, false,
	); !IsConfig(err) {
		t.Fatalf("redirect hop to denied host must fail with ConfigError, got %v", err)
	}
}
