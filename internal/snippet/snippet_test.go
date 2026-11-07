package snippet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorequest/internal/core"
)

// NOTE (C04-phase1): per the standing user decision this batch does not run
// go test. These cases are the executable review checklist for the
// integration round; they are written against the current core APIs but are
// NOT verified as passing yet. docs/task-results/C04-phase1.md records this.

// writeFixtureProject materializes a .gorequest project from relative files.
func writeFixtureProject(t *testing.T, files map[string]string) *core.Project {
	t.Helper()
	dir := t.TempDir()
	if _, err := core.NewProject(dir, "Demo"); err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	for rel, content := range files {
		path := filepath.Join(dir, ".gorequest", filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	p, err := core.OpenProject(dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	return p
}

func fixtureEntry(t *testing.T, p *core.Project, id string) *core.RequestEntry {
	t.Helper()
	entry := p.Requests[id]
	if entry == nil {
		t.Fatalf("request %q not loaded", id)
	}
	return entry
}

const fixtureBaseRequest = `schemaVersion: 1
id: users.create
name: Create user
protocol: http
http:
  method: POST
  url: "{{baseUrl}}/v1/users"
  query:
  - name: debug
    value: "1"
  headers:
  - name: Accept
    value: application/json
  - name: X-Trace
    value: req-{{itemName}}
  body:
    type: json
    value:
      name: "{{itemName}}"
      count: 3
timeout: 5s
`

const fixtureBearerRequest = `schemaVersion: 1
id: users.me
name: Current user
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/v1/me"
auth:
  ref: api-bearer
`

const fixtureBasicRequest = `schemaVersion: 1
id: users.basic
name: Basic login
protocol: http
http:
  method: GET
  url: "https://api.example.com/v1/basic"
auth:
  ref: api-basic
`

const fixtureDuplicateHeadersRequest = `schemaVersion: 1
id: users.dup
name: Duplicate headers
protocol: http
http:
  method: GET
  url: "https://api.example.com/v1/dup"
  headers:
  - name: X-Feature
    value: alpha
  - name: X-Feature
    value: beta
`

const fixtureAPIKeyRequest = `schemaVersion: 1
id: users.key
name: API key header
protocol: http
http:
  method: GET
  url: "https://api.example.com/v1/key"
  headers:
  - name: X-Api-Key
    value: 12345-shared-definition-literal
`

const fixtureFormMultipartRequest = `schemaVersion: 1
id: users.upload
name: Upload avatar
protocol: http
http:
  method: POST
  url: "https://api.example.com/v1/avatar"
  headers:
  - name: X-Form
    value: literal form
  body:
    type: form-data
    value:
    - name: note
      value: hello {{userName}}
    - name: avatar
      file: files/avatar.png
    - name: localdoc
      localFile: doc-slot
`

const fixtureURLEncodedRequest = `schemaVersion: 1
id: users.form
name: urlencoded login
protocol: http
http:
  method: POST
  url: "https://api.example.com/v1/login"
  body:
    type: urlencoded
    value:
    - name: email
      value: "a b@example.com"
    - name: pre
      value: "a%20b"
      encoded: true
`

const fixtureEnvFile = `schemaVersion: 1
id: dev
variables:
  baseUrl: https://api.example.com
  itemName: glycerin
  userName: alex
secrets:
- api_token
- login_password
`

func baseFixtureFiles() map[string]string {
	return map[string]string{
		"collections/users.create.request.yaml": fixtureBaseRequest,
		"collections/users.me.request.yaml":     fixtureBearerRequest,
		"collections/users.basic.request.yaml":  fixtureBasicRequest,
		"collections/users.dup.request.yaml":    fixtureDuplicateHeadersRequest,
		"collections/users.key.request.yaml":    fixtureAPIKeyRequest,
		"collections/users.upload.request.yaml": fixtureFormMultipartRequest,
		"collections/users.form.request.yaml":   fixtureURLEncodedRequest,
		"environments/dev.yaml":                 fixtureEnvFile,
		"auth/api-bearer.auth.yaml":             "schemaVersion: 1\nid: api-bearer\ntype: bearer\ntoken: \"{{secret.api_token}}\"\n",
		"auth/api-basic.auth.yaml":              "schemaVersion: 1\nid: api-basic\ntype: basic\nusername: ops@example.com\npassword: \"{{secret.login_password}}\"\n",
	}
}

func TestSnippetSecretStaysPlaceholderAllLanguages(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.me")
	for _, lang := range Languages() {
		res, err := Generate(p, entry, lang, Options{EnvID: "dev"})
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if res.Blocked != "" {
			t.Fatalf("%s: blocked: %s", lang, res.Blocked)
		}
		if strings.Contains(res.Code, "s3cret") || strings.Contains(res.Code, "real-token") {
			t.Fatalf("%s: secret value leaked into snippet", lang)
		}
		if !strings.Contains(res.Code, "GOREQ_SECRET_API_TOKEN") {
			t.Fatalf("%s: secret placeholder env var missing:\n%s", lang, res.Code)
		}
		found := false
		for _, n := range res.Notes {
			if n.Code == "snippet-secret-placeholder" && n.Severity == SeverityScrubbed {
				found = true
			}
		}
		if !found {
			t.Fatalf("%s: secret placeholder note missing", lang)
		}
		if len(res.RequiredEnv) == 0 {
			t.Fatalf("%s: required env list empty", lang)
		}
	}
}

func TestSnippetBasicAuthNeverEmbedsPassword(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.basic")
	for _, lang := range Languages() {
		res, err := Generate(p, entry, lang, Options{EnvID: "dev"})
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if res.Blocked != "" {
			t.Fatalf("%s: blocked: %s", lang, res.Blocked)
		}
		if !strings.Contains(res.Code, "GOREQ_SECRET_LOGIN_PASSWORD") {
			t.Fatalf("%s: password placeholder missing", lang)
		}
		// The username is definition data and stays visible; the password is
		// never resolved.
		if !strings.Contains(res.Code, "ops@example.com") {
			t.Fatalf("%s: username vanished", lang)
		}
	}
}

func TestSnippetJSONBodyAndQueryEncoding(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.create")
	res, err := Generate(p, entry, LangCURL, Options{EnvID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocked != "" {
		t.Fatalf("blocked: %s", res.Blocked)
	}
	// Variables resolved from the dev environment snapshot; url.Values.Encode
	// re-sorts query pairs exactly like the runner.
	if !strings.Contains(res.Code, "https://api.example.com/v1/users?debug=1") {
		t.Fatalf("resolved url missing:\n%s", res.Code)
	}
	if !strings.Contains(res.Code, `{"count":3,"name":"glycerin"}`) {
		t.Fatalf("json body not core-parity (map key sort + var resolved):\n%s", res.Code)
	}
	if !strings.Contains(res.Code, `'X-Trace: req-glycerin'`) {
		t.Fatalf("templated header not resolved:\n%s", res.Code)
	}
	if !strings.Contains(res.Code, "--max-time \\\n  '5'") && !strings.Contains(res.Code, "--max-time '5'") {
		t.Fatalf("request timeout not carried:\n%s", res.Code)
	}
}

func TestSnippetDuplicateHeaders(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.dup")
	curlRes, err := Generate(p, entry, LangCURL, Options{})
	if err != nil || curlRes.Blocked != "" {
		t.Fatalf("curl: %v / %s", err, curlRes.Blocked)
	}
	if strings.Count(curlRes.Code, "'X-Feature: ") != 2 ||
		!strings.Contains(curlRes.Code, "'X-Feature: alpha'") ||
		!strings.Contains(curlRes.Code, "'X-Feature: beta'") {
		t.Fatalf("cURL must carry duplicate headers twice:\n%s", curlRes.Code)
	}
	pyRes, err := Generate(p, entry, LangPython, Options{})
	if err != nil || pyRes.Blocked != "" {
		t.Fatalf("python: %v / %s", err, pyRes.Blocked)
	}
	if !strings.Contains(pyRes.Code, "http.client") {
		t.Fatalf("python must switch to http.client for duplicate headers:\n%s", pyRes.Code)
	}
	jsRes, err := Generate(p, entry, LangJavaScript, Options{})
	if err != nil || jsRes.Blocked != "" {
		t.Fatalf("js: %v / %s", err, jsRes.Blocked)
	}
	if !strings.Contains(jsRes.Code, "new Headers([") {
		t.Fatalf("js must use the Headers constructor for duplicates:\n%s", jsRes.Code)
	}
}

func TestSnippetSensitiveHeaderLiteralScrubbed(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.key")
	for _, lang := range Languages() {
		res, err := Generate(p, entry, lang, Options{})
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if res.Blocked != "" {
			t.Fatalf("%s: blocked: %s", lang, res.Blocked)
		}
		if strings.Contains(res.Code, "12345-shared-definition-literal") {
			t.Fatalf("%s: literal api key leaked into snippet", lang)
		}
		if !strings.Contains(res.Code, "GOREQ_X_API_KEY") {
			t.Fatalf("%s: scrub placeholder missing", lang)
		}
	}
}

func TestSnippetMultipartAndFilePlaceholders(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.upload")
	for _, lang := range Languages() {
		res, err := Generate(p, entry, lang, Options{EnvID: "dev"})
		if err != nil {
			t.Fatalf("%s: %v", lang, err)
		}
		if res.Blocked != "" {
			t.Fatalf("%s: blocked: %s", lang, res.Blocked)
		}
		if strings.Contains(res.Code, "/Users/") {
			t.Fatalf("%s: host path leaked", lang)
		}
		if !strings.Contains(res.Code, "hello alex") && !strings.Contains(res.Code, "hello \" + ") && !strings.Contains(res.Code, "alex") {
			t.Fatalf("%s: multipart text var not visible", lang)
		}
		if !strings.Contains(res.Code, "files/avatar.png") && !strings.Contains(res.Code, "local-doc-slot") && !strings.Contains(res.Code, "local:doc-slot") {
			// Each language names the placeholder with its file hint.
			t.Fatalf("%s: file placeholder hint missing:\n%s", lang, res.Code)
		}
	}
}

func TestSnippetURLEncodedPrebuiltLiteral(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.form")
	res, err := Generate(p, entry, LangGo, Options{})
	if err != nil || res.Blocked != "" {
		t.Fatalf("go: %v / %s", err, res.Blocked)
	}
	// Core.EncodeURLEncodedFields keeps encoded:true fields as-is and escapes
	// plain fields with QueryEscape (space → +).
	if !strings.Contains(res.Code, "email=a+b%40example.com") {
		t.Fatalf("plain field not QueryEscaped like the runner:\n%s", res.Code)
	}
	if !strings.Contains(res.Code, "pre=a%20b") {
		t.Fatalf("pre-encoded field re-encoded:\n%s", res.Code)
	}
}

func TestSnippetBlocksAndTimeouts(t *testing.T) {
	// Invalid timeout refuses generation (mirrors core config error).
	files := baseFixtureFiles()
	withID := func(id string) string {
		return strings.Replace(fixtureBaseRequest, "id: users.create", "id: "+id, 1)
	}
	files["collections/users.badtime.request.yaml"] = strings.Replace(withID("users.badtime"), "timeout: 5s", "timeout: soon", 1)
	files["collections/users.ws.request.yaml"] = strings.Replace(withID("users.ws"), "protocol: http", "protocol: ws", 1)
	files["collections/users.undef.request.yaml"] = strings.Replace(withID("users.undef"), "{{itemName}}", "{{mysteryName}}", 2)
	p2 := writeFixtureProject(t, files)

	for id, want := range map[string]string{
		"users.badtime": "snippet-invalid-timeout",
		"users.ws":      "snippet-non-http-protocol",
		"users.undef":   "snippet-undefined-variable",
	} {
		entry := fixtureEntry(t, p2, id)
		res, err := Generate(p2, entry, LangGo, Options{EnvID: "dev"})
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		if res.Blocked == "" {
			t.Fatalf("%s: expected blocked result", id)
		}
		if res.Code != "" {
			t.Fatalf("%s: blocked result must not carry code", id)
		}
		if !strings.Contains(res.Blocked, want) && !noteHasCode(res.Notes, want) {
			t.Fatalf("%s: blocked reason %q misses code %s", id, res.Blocked, want)
		}
	}
}

func noteHasCode(notes []Note, code string) bool {
	for _, n := range notes {
		if n.Code == code {
			return true
		}
	}
	return false
}

func TestSnippetDeterministicOutput(t *testing.T) {
	p := writeFixtureProject(t, baseFixtureFiles())
	entry := fixtureEntry(t, p, "users.create")
	for _, lang := range Languages() {
		a, err := Generate(p, entry, lang, Options{EnvID: "dev"})
		if err != nil || a.Blocked != "" {
			t.Fatalf("%s: %v / %s", lang, err, a.Blocked)
		}
		b2, err := Generate(p, entry, lang, Options{EnvID: "dev"})
		if err != nil || b2.Blocked != "" {
			t.Fatalf("%s second: %v / %s", lang, err, b2.Blocked)
		}
		if a.Code != b2.Code || len(a.Notes) != len(b2.Notes) {
			t.Fatalf("%s: generation is not deterministic", lang)
		}
	}
}

func TestSnippetRuntimeDeclared(t *testing.T) {
	for _, lang := range Languages() {
		decl := RuntimeDecl(lang)
		if decl == "" || decl == "unsupported language" {
			t.Fatalf("%s: runtime declaration missing", lang)
		}
		if ext := FileExtension(lang); ext == ".txt" || ext == "" {
			t.Fatalf("%s: file extension missing", lang)
		}
	}
}

func TestSnippetJSONSecretKeepsVisiblePlaceholder(t *testing.T) {
	files := baseFixtureFiles()
	base := strings.Replace(fixtureBaseRequest, "id: users.create", "id: users.jsonsecret", 1)
	files["collections/users.jsonsecret.request.yaml"] = strings.Replace(
		base, `"{{itemName}}"`, `"{{secret.api_token}}"`, 1)
	p := writeFixtureProject(t, files)
	entry := fixtureEntry(t, p, "users.jsonsecret")
	res, err := Generate(p, entry, LangGo, Options{EnvID: "dev"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Blocked != "" {
		t.Fatalf("blocked: %s", res.Blocked)
	}
	// Inside raw JSON there is no concatenation point, so the reference stays
	// a visible text placeholder and the env var note explains how to bind it.
	if !strings.Contains(res.Code, "{{secret.api_token}}") {
		t.Fatalf("json secret placeholder not visible:\n%s", res.Code)
	}
	if !noteHasCode(res.Notes, "snippet-secret-placeholder") {
		t.Fatal("json secret env note missing")
	}
}
