package desktop

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func TestIdentityMessagesCoverBothLanguages(t *testing.T) {
	typ := reflect.TypeOf(identityMessages{})
	zh := reflect.ValueOf(identityMessagesZh)
	en := reflect.ValueOf(identityMessagesEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		zhStr := strings.TrimSpace(zh.Field(i).String())
		enStr := strings.TrimSpace(en.Field(i).String())
		if zhStr == "" || enStr == "" {
			t.Errorf("identityMessages.%s empty: zh=%q en=%q", f.Name, zhStr, enStr)
		}
		if zhStr == enStr {
			t.Errorf("identityMessages.%s not translated: %q", f.Name, zhStr)
		}
	}
}

// seedAuthFixtureProject builds a project on disk with reusable auth schemes,
// project default auth, and requests exercising inherit, none, ref, inline
// bearer/basic/apikey (header/query/cookie), and unknown-type passthrough.
func seedAuthFixtureProject(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gq := filepath.Join(dir, ".gorequest")
	for _, sub := range []string{"collections", "environments", "docs", "auth"} {
		if err := os.MkdirAll(filepath.Join(gq, sub), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(rel, content string) {
		t.Helper()
		full := filepath.Join(gq, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	mustWrite("project.yaml", `schemaVersion: 1
id: auth-fixture
name: Auth Fixture
variables:
  baseUrl: "http://127.0.0.1:4010"
defaults:
  timeout: 10s
  auth: default-bearer
`)
	mustWrite("environments/dev.yaml", `schemaVersion: 1
id: dev
variables:
  baseUrl: "http://127.0.0.1:4010"
secrets:
  - defToken
  - refToken
  - basicPass
  - apiKeyHdr
  - apiKeyQry
  - apiKeyCk
`)
	mustWrite("auth/default-bearer.auth.yaml", `schemaVersion: 1
id: default-bearer
type: bearer
token: "{{secret.defToken}}"
`)
	mustWrite("auth/shared-bearer.auth.yaml", `schemaVersion: 1
id: shared-bearer
type: bearer
token: "{{secret.refToken}}"
`)
	mustWrite("auth/shared-basic.auth.yaml", `schemaVersion: 1
id: shared-basic
type: basic
username: alice
password: "{{secret.basicPass}}"
`)
	mustWrite("auth/shared-apikey-query.auth.yaml", `schemaVersion: 1
id: shared-apikey-query
type: apikey
name: api_key
in: query
value: "{{secret.apiKeyQry}}"
`)

	// 1. Inherit (auth omitted)
	mustWrite("collections/inherit.request.yaml", `schemaVersion: 1
id: req.inherit
name: Inherit Request
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/inherit"
`)
	// 2. Explicit none (clears project default-bearer)
	mustWrite("collections/explicit-none.request.yaml", `schemaVersion: 1
id: req.none
name: Explicit None
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/none"
auth: none
`)
	// 3. Ref to shared-bearer
	mustWrite("collections/ref-bearer.request.yaml", `schemaVersion: 1
id: req.ref-bearer
name: Ref Bearer
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/ref-bearer"
auth:
  ref: shared-bearer
`)
	// 4. Ref to shared-apikey-query
	mustWrite("collections/ref-apikey.request.yaml", `schemaVersion: 1
id: req.ref-apikey
name: Ref API Key
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/ref-apikey"
auth:
  ref: shared-apikey-query
`)
	// 5. Ref to not-yet-created scheme (must still preserve ref on URL edit!)
	mustWrite("collections/ref-missing.request.yaml", `schemaVersion: 1
id: req.ref-missing
name: Ref Missing
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/ref-missing"
auth:
  ref: future-scheme
`)
	// 6. Inline bearer
	mustWrite("collections/inline-bearer.request.yaml", `schemaVersion: 1
id: req.inline-bearer
name: Inline Bearer
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/inline-bearer"
auth:
  schemaVersion: 1
  id: ""
  type: bearer
  token: "{{secret.refToken}}"
`)
	// 7. Inline basic
	mustWrite("collections/inline-basic.request.yaml", `schemaVersion: 1
id: req.inline-basic
name: Inline Basic
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/inline-basic"
auth:
  schemaVersion: 1
  id: ""
  type: basic
  username: bob
  password: "{{secret.basicPass}}"
`)
	// 8. Inline API Key (header, query, cookie)
	mustWrite("collections/inline-apikey-header.request.yaml", `schemaVersion: 1
id: req.inline-apikey-header
name: Inline API Key Header
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/apikey-header"
auth:
  schemaVersion: 1
  id: ""
  type: apikey
  name: X-API-Key
  in: header
  value: "{{secret.apiKeyHdr}}"
`)
	mustWrite("collections/inline-apikey-query.request.yaml", `schemaVersion: 1
id: req.inline-apikey-query
name: Inline API Key Query
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/apikey-query"
auth:
  schemaVersion: 1
  id: ""
  type: apikey
  name: token_q
  in: query
  value: "{{secret.apiKeyQry}}"
`)
	mustWrite("collections/inline-apikey-cookie.request.yaml", `schemaVersion: 1
id: req.inline-apikey-cookie
name: Inline API Key Cookie
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/apikey-cookie"
auth:
  schemaVersion: 1
  id: ""
  type: apikey
  name: sid_cookie
  in: cookie
  value: "{{secret.apiKeyCk}}"
`)
	return dir
}

// TestURLOnlyEditPreservesAllAuthSemantics verifies the core B05 regression:
// opening a request tab, modifying ONLY the URL, and saving must never turn
// inherit into none, never flatten ref into inline, and never drop apikey.
func TestURLOnlyEditPreservesAllAuthSemantics(t *testing.T) {
	dir := seedAuthFixtureProject(t)
	m, _ := newTestManager(t)
	m.secrets = core.MapSecrets{
		"defToken":  "PLAIN-SECRET-DEF-001",
		"refToken":  "PLAIN-SECRET-REF-002",
		"basicPass": "PLAIN-SECRET-BAS-003",
		"apiKeyHdr": "PLAIN-SECRET-HDR-004",
		"apiKeyQry": "PLAIN-SECRET-QRY-005",
		"apiKeyCk":  "PLAIN-SECRET-CK-006",
	}
	if err := m.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	app := m.workspaces[0]
	ps := app.ps

	cases := []struct {
		id           string
		wantTabMode  string
		wantDiskMode string // "" means req.Auth == nil (inherit)
		wantRef      string
		wantType     string
		wantIn       string
		wantName     string
	}{
		{id: "req.inherit", wantTabMode: "inherit", wantDiskMode: ""},
		{id: "req.none", wantTabMode: "none", wantDiskMode: "none"},
		{id: "req.ref-bearer", wantTabMode: "ref", wantDiskMode: "ref", wantRef: "shared-bearer"},
		{id: "req.ref-apikey", wantTabMode: "ref", wantDiskMode: "ref", wantRef: "shared-apikey-query"},
		{id: "req.ref-missing", wantTabMode: "ref", wantDiskMode: "ref", wantRef: "future-scheme"},
		{id: "req.inline-bearer", wantTabMode: "bearer", wantDiskMode: "inline", wantType: "bearer"},
		{id: "req.inline-basic", wantTabMode: "basic", wantDiskMode: "inline", wantType: "basic"},
		{id: "req.inline-apikey-header", wantTabMode: "apikey", wantDiskMode: "inline", wantType: "apikey", wantIn: "header", wantName: "X-API-Key"},
		{id: "req.inline-apikey-query", wantTabMode: "apikey", wantDiskMode: "inline", wantType: "apikey", wantIn: "query", wantName: "token_q"},
		{id: "req.inline-apikey-cookie", wantTabMode: "apikey", wantDiskMode: "inline", wantType: "apikey", wantIn: "cookie", wantName: "sid_cookie"},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			entry := ps.project.Requests[tc.id]
			if entry == nil {
				t.Fatalf("missing fixture request %s", tc.id)
			}
			ps.openRequest(entry)
			tab := ps.activeTab()
			if tab.AuthMode != tc.wantTabMode {
				t.Fatalf("opened tab.AuthMode = %q, want %q", tab.AuthMode, tc.wantTabMode)
			}

			// Render the Auth tab in UI Tester to ensure drawing does not mutate AuthMode.
			tab.Section = 1
			tt := ui.NewTester(app.View, 1200, 820)
			tt.Frame()

			// Modify ONLY the URL and save.
			newURL := entry.Request.HTTP.URL + "?edited=1"
			tab.URL = newURL
			tab.Dirty = true
			if err := ps.saveTab(tab); err != nil {
				t.Fatalf("saveTab: %v", err)
			}
		})
	}

	// Also verify unknown future inline auth type passthrough in memory -> YAML.
	t.Run("unknown_inline_type_passthrough", func(t *testing.T) {
		entry := ps.project.Requests["req.none"]
		entry.Request.Auth = &core.AuthRef{
			Mode:   "inline",
			Inline: &core.AuthScheme{SchemaVersion: 1, Type: "oauth2-pkce", Token: "{{secret.refToken}}"},
		}
		tab := &Tab{Kind: "request", Owner: ps, Entry: entry}
		fillTabFromRequest(tab, entry, ps.project)
		tab.URL = "{{baseUrl}}/oauth-edited"
		gotReq, err := requestFromTab(tab)
		if err != nil {
			t.Fatalf("requestFromTab: %v", err)
		}
		if gotReq.Auth == nil || gotReq.Auth.Mode != "inline" || gotReq.Auth.Inline == nil || gotReq.Auth.Inline.Type != "oauth2-pkce" {
			t.Fatalf("unknown inline auth clobbered: %+v", gotReq.Auth)
		}
	})

	// Reload from disk and verify every request's persisted YAML and parsed AuthRef.
	reloaded, err := core.OpenProject(dir)
	if err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	for _, tc := range cases {
		entry := reloaded.Requests[tc.id]
		if entry == nil {
			t.Fatalf("reloaded missing %s", tc.id)
		}
		if !strings.HasSuffix(entry.Request.HTTP.URL, "?edited=1") {
			t.Fatalf("%s URL was not saved: %q", tc.id, entry.Request.HTTP.URL)
		}
		rawBytes, _, err := reloaded.Store.Read(entry.RelPath)
		if err != nil {
			t.Fatalf("read %s: %v", entry.RelPath, err)
		}
		rawYAML := string(rawBytes)

		if tc.wantDiskMode == "" {
			if entry.Request.Auth != nil {
				t.Fatalf("%s: expected nil Auth (inherit), got %+v\nYAML:\n%s", tc.id, entry.Request.Auth, rawYAML)
			}
			if strings.Contains(rawYAML, "\nauth:") || strings.HasPrefix(rawYAML, "auth:") {
				t.Fatalf("%s: inherit request should omit auth key in YAML:\n%s", tc.id, rawYAML)
			}
			continue
		}
		if entry.Request.Auth == nil || entry.Request.Auth.Mode != tc.wantDiskMode {
			t.Fatalf("%s: Auth = %+v, want mode %q\nYAML:\n%s", tc.id, entry.Request.Auth, tc.wantDiskMode, rawYAML)
		}
		switch tc.wantDiskMode {
		case "none":
			if !strings.Contains(rawYAML, "auth: none") {
				t.Fatalf("%s: expected 'auth: none' in YAML:\n%s", tc.id, rawYAML)
			}
		case "ref":
			if entry.Request.Auth.Ref != tc.wantRef {
				t.Fatalf("%s: Auth.Ref = %q, want %q", tc.id, entry.Request.Auth.Ref, tc.wantRef)
			}
			if strings.Contains(rawYAML, "type: ") {
				t.Fatalf("%s: ref auth must not flatten inline scheme into YAML:\n%s", tc.id, rawYAML)
			}
		case "inline":
			in := entry.Request.Auth.Inline
			if in == nil || in.Type != tc.wantType {
				t.Fatalf("%s: inline = %+v, want type %q", tc.id, in, tc.wantType)
			}
			if tc.wantType == "apikey" {
				if in.In != tc.wantIn || in.Name != tc.wantName {
					t.Fatalf("%s: apikey inline = %+v, want in=%q name=%q", tc.id, in, tc.wantIn, tc.wantName)
				}
			}
		}
	}

	assertNoSecretInDir(t, dir, []string{
		"PLAIN-SECRET-DEF-001",
		"PLAIN-SECRET-REF-002",
		"PLAIN-SECRET-BAS-003",
		"PLAIN-SECRET-HDR-004",
		"PLAIN-SECRET-QRY-005",
		"PLAIN-SECRET-CK-006",
	})
}

// queuedWindow wraps fakeWindow and dispatches Update callbacks on a channel so
// tests run them on the test goroutine, matching the real single-UI-thread
// contract of mygo.Window.Update without races or polling sleeps.
type queuedWindow struct {
	*fakeWindow
	updates chan func()
}

func (q *queuedWindow) Update(fn func()) {
	q.updates <- fn
}

func waitForSendDone(t *testing.T, qw *queuedWindow, tab *Tab) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for tab.Sending {
		select {
		case fn := <-qw.updates:
			fn()
		case <-timer.C:
			t.Fatalf("timed out waiting for tab send completion (Sending=%v RespErr=%q)", tab.Sending, tab.RespErr)
		}
	}
	if tab.RespErr != "" {
		t.Fatalf("unexpected send error: %s", tab.RespErr)
	}
}

func assertNoSecretInDir(t *testing.T, root string, secrets []string) {
	t.Helper()
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		s := string(data)
		for _, sec := range secrets {
			if sec != "" && strings.Contains(s, sec) {
				t.Errorf("secret %q leaked into file %s:\n%s", sec, path, s)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

// TestAPIKeyUISendAndSaveAllThreeLocations drives the real Desktop Auth UI and
// send/save pipeline against a local httptest server for header, query, cookie,
// plus inherit vs explicit none.
func TestAPIKeyUISendAndSaveAllThreeLocations(t *testing.T) {
	var mu sync.Mutex
	var lastAuthHeader, lastAPIHeader, lastAPIQuery, lastAPICookie string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		lastAuthHeader = r.Header.Get("Authorization")
		lastAPIHeader = r.Header.Get("X-Custom-Key")
		lastAPIQuery = r.URL.Query().Get("k_param")
		lastAPICookie = ""
		if c, err := r.Cookie("k_session"); err == nil {
			lastAPICookie = c.Value
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	dir := seedAuthFixtureProject(t)
	cfgDir := t.TempDir()
	m, ff := newTestManagerAt(t, filepath.Join(cfgDir, "config.json"))
	var qWin *queuedWindow
	m.newWindow = func(s windowSpec) window {
		fw := ff.new(s).(*fakeWindow)
		qWin = &queuedWindow{fakeWindow: fw, updates: make(chan func(), 16)}
		return qWin
	}
	secretList := []string{
		"LIVE-DEF-BEARER-701",
		"LIVE-HDR-SECRET-702",
		"LIVE-QRY-SECRET-703",
		"LIVE-CK-SECRET-704",
	}
	m.secrets = core.MapSecrets{
		"defToken":  secretList[0],
		"apiKeyHdr": secretList[1],
		"apiKeyQry": secretList[2],
		"apiKeyCk":  secretList[3],
	}
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	ps := app.ps

	// Point dev baseUrl at our local httptest server.
	ps.project.Environments["dev"].Variables["baseUrl"] = srv.URL

	entry, err := ps.createRequestIn("", "Live Auth Check")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = "{{baseUrl}}/check"

	tt := ui.NewTester(app.View, 1200, 820)
	tt.Frame()

	// Switch to the Auth section via UI click.
	if err := tt.Click("Auth"); err != nil {
		t.Fatalf("click Auth tab: %v", err)
	}
	if tab.AuthMode != "inherit" {
		t.Fatalf("new request should start in inherit mode, got %q", tab.AuthMode)
	}
	if !tt.HasText("Inherits project default auth: default-bearer · bearer (token: {{secret.defToken}})") {
		t.Fatalf("inherit summary missing; texts=%v", tt.Texts())
	}

	// 1. Send in "inherit" mode -> must send project's default-bearer!
	app.trySend(tab)
	waitForSendDone(t, qWin, tab)
	mu.Lock()
	gotAuth := lastAuthHeader
	mu.Unlock()
	if gotAuth != "Bearer "+secretList[0] {
		t.Fatalf("inherit send Authorization = %q, want %q", gotAuth, "Bearer "+secretList[0])
	}

	// 2. Switch UI Select from "inherit" -> "none", save, send -> clears default auth!
	if err := tt.Click("inherit"); err != nil {
		t.Fatalf("open auth mode select: %v", err)
	}
	if err := tt.Click("none"); err != nil {
		t.Fatalf("choose none: %v", err)
	}
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save none: %v", err)
	}
	app.trySend(tab)
	waitForSendDone(t, qWin, tab)
	mu.Lock()
	gotAuth = lastAuthHeader
	mu.Unlock()
	if gotAuth != "" {
		t.Fatalf("explicit none must clear project default auth, got Authorization=%q", gotAuth)
	}

	// 3. Switch UI Select from "none" -> "ref" (shared-apikey-query), send & verify!
	if err := tt.Click("none"); err != nil {
		t.Fatalf("open auth mode select from none: %v", err)
	}
	if err := tt.Click("ref"); err != nil {
		t.Fatalf("choose ref: %v", err)
	}
	tab.AuthRefID = "shared-apikey-query"
	ps.project.AuthSchemes["shared-apikey-query"].Name = "k_param"
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save ref: %v", err)
	}
	app.trySend(tab)
	waitForSendDone(t, qWin, tab)
	mu.Lock()
	if lastAPIQuery != secretList[2] {
		t.Fatalf("ref apikey query send mismatch: k_param=%q, want %q", lastAPIQuery, secretList[2])
	}
	mu.Unlock()

	// 4. Switch UI Select from "ref" -> "apikey", then exercise header, query, cookie!
	tt.Frame()
	if err := tt.Click("ref"); err != nil {
		t.Fatalf("open auth mode select from ref: %v", err)
	}
	if err := tt.Click("apikey"); err != nil {
		t.Fatalf("choose apikey: %v", err)
	}
	tt.Frame()

	// 4a. Header
	tab.AuthKeyName = "X-Custom-Key"
	tab.AuthKeyValue = "{{secret.apiKeyHdr}}"
	if err := tt.Click("header"); err != nil {
		t.Fatalf("click header segment: %v", err)
	}
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save apikey header: %v", err)
	}
	app.trySend(tab)
	waitForSendDone(t, qWin, tab)
	mu.Lock()
	if lastAPIHeader != secretList[1] || lastAuthHeader != "" {
		t.Fatalf("apikey header send mismatch: X-Custom-Key=%q Auth=%q", lastAPIHeader, lastAuthHeader)
	}
	mu.Unlock()

	// 4b. Query
	if err := tt.Click("query"); err != nil {
		t.Fatalf("click query segment: %v", err)
	}
	tab.AuthKeyName = "k_param"
	tab.AuthKeyValue = "{{secret.apiKeyQry}}"
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save apikey query: %v", err)
	}
	app.trySend(tab)
	waitForSendDone(t, qWin, tab)
	mu.Lock()
	if lastAPIQuery != secretList[2] || lastAPIHeader != "" {
		t.Fatalf("apikey query send mismatch: k_param=%q X-Custom-Key=%q", lastAPIQuery, lastAPIHeader)
	}
	mu.Unlock()

	// 4c. Cookie
	if err := tt.Click("cookie"); err != nil {
		t.Fatalf("click cookie segment: %v", err)
	}
	tab.AuthKeyName = "k_session"
	tab.AuthKeyValue = "{{secret.apiKeyCk}}"
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save apikey cookie: %v", err)
	}
	app.trySend(tab)
	waitForSendDone(t, qWin, tab)
	mu.Lock()
	if lastAPICookie != secretList[3] || lastAPIQuery != "" {
		t.Fatalf("apikey cookie send mismatch: k_session=%q k_param=%q", lastAPICookie, lastAPIQuery)
	}
	mu.Unlock()

	// Verify saved request reloads with exact cookie apikey and placeholder, no secret leak.
	reloaded, err := core.OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	saved := reloaded.Requests[entry.Request.ID]
	if saved == nil || saved.Request.Auth == nil || saved.Request.Auth.Mode != "inline" || saved.Request.Auth.Inline == nil {
		t.Fatalf("saved auth invalid: %+v", saved)
	}
	gotInline := saved.Request.Auth.Inline
	if gotInline.Type != "apikey" || gotInline.In != "cookie" || gotInline.Name != "k_session" || gotInline.Value != "{{secret.apiKeyCk}}" {
		t.Fatalf("saved inline apikey mismatch: %+v", gotInline)
	}

	assertNoSecretInDir(t, dir, secretList)
	assertNoSecretInDir(t, cfgDir, secretList)
}
