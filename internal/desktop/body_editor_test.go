package desktop

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

type staticSecrets map[string]string

func (s staticSecrets) Get(name string) (string, bool) {
	v, ok := s[name]
	return v, ok
}

func TestBodyMessagesCatalogComplete(t *testing.T) {
	typ := reflect.TypeOf(bodyMessages{})
	zh := reflect.ValueOf(bodyMessagesZh)
	en := reflect.ValueOf(bodyMessagesEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		if strings.TrimSpace(zh.Field(i).String()) == "" {
			t.Errorf("bodyMessagesZh.%s is empty", f.Name)
		}
		if strings.TrimSpace(en.Field(i).String()) == "" {
			t.Errorf("bodyMessagesEn.%s is empty", f.Name)
		}
	}
	if bodyMessagesZh.ModeURLEncoded == bodyMessagesEn.ModeURLEncoded {
		t.Error("expected ModeURLEncoded to be translated in Chinese")
	}
	if bodyMessagesZh.CustomMethodTitle == bodyMessagesEn.CustomMethodTitle {
		t.Error("expected CustomMethodTitle to be translated in Chinese")
	}
}

func TestBodyEditorModesSaveReopenAndExecuteEndToEnd(t *testing.T) {
	var (
		gotMethod      string
		gotContentType string
		gotRawBody     []byte
		gotFormTitle   string
		gotProjAsset   string
		gotLocalAvatar string
		gotAvatarName  string
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotContentType = r.Header.Get("Content-Type")
		if strings.HasPrefix(gotContentType, "multipart/form-data;") {
			if err := r.ParseMultipartForm(8 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			gotFormTitle = r.FormValue("title")
			f1, _, err := r.FormFile("asset")
			if err == nil {
				b, _ := io.ReadAll(f1)
				_ = f1.Close()
				gotProjAsset = string(b)
			}
			f2, h2, err := r.FormFile("avatar")
			if err == nil {
				b, _ := io.ReadAll(f2)
				_ = f2.Close()
				gotLocalAvatar = string(b)
				gotAvatarName = h2.Filename
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"ok":true}`))
			return
		}
		raw, _ := io.ReadAll(r.Body)
		gotRawBody = raw
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	a, ps := openTestProject(t, "B03 Modes")
	a.mgr.secrets = staticSecrets{"api": "SUPER_SECRET_TOKEN_99"}

	// Create a project-relative attachment inside .gorequest/assets/hello.txt
	assetDir := filepath.Join(ps.project.Dir, "assets")
	if err := os.MkdirAll(assetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(assetDir, "hello.txt"), []byte("project-asset-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create an outside local file (never copied into .gorequest/)
	outsideDir := t.TempDir()
	localAvatarPath := filepath.Join(outsideDir, "user-avatar.png")
	avatarBytes := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	if err := os.WriteFile(localAvatarPath, avatarBytes, 0o600); err != nil {
		t.Fatal(err)
	}

	tab := openRequestTabs(t, ps, "Upload Req")[0]
	tab.Method = "POST"
	tab.URL = srv.URL + "/submit"
	tab.Section = 3 // Body tab

	tt := ui.NewTester(a.View, 1280, 860)
	tt.Frame()

	// 1. Form URL-Encoded mode: paste raw encoded string with duplicates, empty value, and secret template
	if err := tt.Click("Form URL-Encoded"); err != nil {
		t.Fatalf("switch to Form URL-Encoded: %v", err)
	}
	bs := ensureBodyState(tab, a)
	parsedFields, err := core.ParseRawURLEncodedFields("a=hello%20world&dup=1&dup=2&empty=&tok={{secret.api}}")
	if err != nil {
		t.Fatal(err)
	}
	bs.setFormFields(parsedFields)
	bs.markMutated(tab)
	tt.Frame()

	// Secret must be masked in UI texts and never leaked in cleartext
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "SUPER_SECRET_TOKEN_99") {
			t.Fatalf("secret leaked in UI preview text: %q", txt)
		}
	}
	if !tt.HasText("a=hello+world&dup=1&dup=2&empty=&tok=%28secret%29") && !tt.HasText("Wire Preview:") {
		t.Fatalf("expected wire preview in Form URL-Encoded view; texts=%v", tt.Texts())
	}

	// Save via UI button, reopen from disk, and execute against httptest.Server
	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("save urlencoded request: %v", err)
	}
	if tab.Dirty {
		t.Fatalf("expected clean tab after save; status=%q", a.status)
	}
	_, plan, err := a.buildTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTabPlan urlencoded: %v", err)
	}
	if _, err := core.Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("Execute urlencoded: %v", err)
	}
	if gotMethod != "POST" || gotContentType != "application/x-www-form-urlencoded" {
		t.Fatalf("urlencoded headers: method=%q ct=%q", gotMethod, gotContentType)
	}
	wantFormWire := "a=hello+world&dup=1&dup=2&empty=&tok=SUPER_SECRET_TOKEN_99"
	if string(gotRawBody) != wantFormWire {
		t.Fatalf("urlencoded body = %q, want %q (must not double-encode)", string(gotRawBody), wantFormWire)
	}

	// 2. Multipart form-data mode: text part + project file + local mapped file + engine boundary override
	if err := tt.Click("Multipart"); err != nil {
		t.Fatalf("switch to Multipart: %v", err)
	}
	// User sets a stale boundary-less Content-Type header; engine must replace it with its boundary!
	tab.HeaderRows = []*KVRow{
		{ID: nextRowID(), Enabled: true, Name: "Content-Type", Value: "multipart/form-data"},
		newBlankRow(),
	}
	if err := a.BindLocalFile(ps, "avatar-slot", localAvatarPath); err != nil {
		t.Fatalf("BindLocalFile: %v", err)
	}
	bs = ensureBodyState(tab, a)
	bs.multipartRows = []*multipartBodyRow{
		{
			ID:         nextRowID(),
			Enabled:    true,
			Name:       "title",
			PartSource: partSourceText,
			Value:      "Profile {{baseUrl}}",
		},
		{
			ID:          nextRowID(),
			Enabled:     true,
			Name:        "asset",
			PartSource:  partSourceProjectFile,
			ProjectFile: "assets/hello.txt",
		},
		{
			ID:          nextRowID(),
			Enabled:     true,
			Name:        "avatar",
			PartSource:  partSourceLocalFile,
			LocalSlot:   "avatar-slot",
			FileName:    "custom-avatar.png",
			ContentType: "image/png",
		},
	}
	bs.markMutated(tab)
	tt.Frame()

	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("save multipart request: %v", err)
	}

	// Verify saved .request.yaml contains NO host path and outside file was NOT copied into .gorequest/
	rawYAML, _, err := ps.project.Store.Read(tab.Entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawYAML), outsideDir) || strings.Contains(string(rawYAML), "user-avatar.png") {
		t.Fatalf("machine path leaked into shared .request.yaml:\n%s", rawYAML)
	}
	if !strings.Contains(string(rawYAML), "localFile: avatar-slot") {
		t.Fatalf("expected localFile: avatar-slot in saved YAML:\n%s", rawYAML)
	}
	if _, statErr := os.Stat(filepath.Join(ps.project.Dir, "user-avatar.png")); !os.IsNotExist(statErr) {
		t.Fatal("outside local file must never be copied into .gorequest/")
	}

	_, plan, err = a.buildTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTabPlan multipart: %v", err)
	}
	if _, err := core.Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("Execute multipart: %v", err)
	}
	if !strings.HasPrefix(gotContentType, "multipart/form-data; boundary=") {
		t.Fatalf("engine failed to manage multipart boundary, got Content-Type %q", gotContentType)
	}
	if !strings.HasPrefix(gotFormTitle, "Profile ") {
		t.Fatalf("gotFormTitle = %q", gotFormTitle)
	}
	if gotProjAsset != "project-asset-bytes" {
		t.Fatalf("gotProjAsset = %q, want project-asset-bytes", gotProjAsset)
	}
	if !bytes.Equal([]byte(gotLocalAvatar), avatarBytes) || gotAvatarName != "custom-avatar.png" {
		t.Fatalf("gotLocalAvatar=%x name=%q", []byte(gotLocalAvatar), gotAvatarName)
	}

	// 3. Binary mode with local file mapping + custom Content-Type
	if err := tt.Click("Binary"); err != nil {
		t.Fatalf("switch to Binary: %v", err)
	}
	tab.HeaderRows = []*KVRow{newBlankRow()}
	bs = ensureBodyState(tab, a)
	bs.binSource = binSourceLocalFile
	bs.binLocalSlot = "avatar-slot"
	bs.binContentType = "image/png"
	bs.markMutated(tab)
	tt.Frame()

	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("save binary request: %v", err)
	}
	// Reload tab from persisted request to prove full round-trip
	ps.reloadRequestTab(tab)
	tab.resetBodyState()
	tt.Frame()

	_, plan, err = a.buildTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTabPlan binary after reload: %v", err)
	}
	if _, err := core.Execute(context.Background(), plan, nil); err != nil {
		t.Fatalf("Execute binary: %v", err)
	}
	if gotContentType != "image/png" || !bytes.Equal(gotRawBody, avatarBytes) {
		t.Fatalf("binary got ct=%q body=%x, want image/png %x", gotContentType, gotRawBody, avatarBytes)
	}
}

func TestLocalFileMappingPersistsLocallyAndIsolatesClones(t *testing.T) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	m1, _ := newTestManagerAt(t, cfgPath)

	dirA := writeProject(t, "Repo Clone A")
	dirB := writeProject(t, "Repo Clone B")
	if err := m1.openProject(dirA); err != nil {
		t.Fatal(err)
	}
	if err := m1.openProject(dirB); err != nil {
		t.Fatal(err)
	}
	appA := m1.workspaces[0]
	appB := m1.workspaces[1]

	secretPayload := filepath.Join(t.TempDir(), "client-payload.bin")
	if err := os.WriteFile(secretPayload, []byte("device-only-bytes"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := appA.BindLocalFile(appA.ps, "payload-slot", secretPayload); err != nil {
		t.Fatal(err)
	}

	// Configure identical "local:payload-slot" binary request on both clones.
	for _, app := range []*App{appA, appB} {
		tab := openRequestTabs(t, app.ps, "Send Binary")[0]
		tab.Method = "PUT"
		tab.URL = srv.URL + "/upload"
		bs := ensureBodyState(tab, app)
		bs.modeIdx = uiBodyModeIndex(core.BodyBinary)
		bs.binSource = binSourceLocalFile
		bs.binLocalSlot = "payload-slot"
		bs.markMutated(tab)
		if err := app.ps.saveTab(tab); err != nil {
			t.Fatal(err)
		}
	}

	// Clone B has not bound "payload-slot" -> MUST fail before hitting network!
	tabB := appB.ps.activeTab()
	_, _, err := appB.buildTabPlan(context.Background(), tabB)
	if err == nil || !core.IsConfig(err) {
		t.Fatalf("expected pre-network ConfigError on unbound Clone B, got %v", err)
	}
	if hits.Load() != 0 {
		t.Fatalf("unbound local slot must fail before network; server hits = %d", hits.Load())
	}

	// Simulate app restart with same config dir: Clone A keeps its local binding.
	m2, _ := newTestManagerAt(t, cfgPath)
	if err := m2.openProject(dirA); err != nil {
		t.Fatal(err)
	}
	appARestarted := m2.workspaces[0]
	bound, ok := appARestarted.BoundLocalFile(appARestarted.ps, "payload-slot")
	if !ok || bound != secretPayload {
		t.Fatalf("expected persisted local binding %q after restart, got %q (ok=%v)", secretPayload, bound, ok)
	}
	appARestarted.ps.openRequest(allRequests(appARestarted.ps.project)[0])
	// Find "Send Binary" tab
	for _, reqEntry := range allRequests(appARestarted.ps.project) {
		if reqEntry.Request.Name == "Send Binary" {
			appARestarted.ps.openRequest(reqEntry)
		}
	}
	_, planA, err := appARestarted.buildTabPlan(context.Background(), appARestarted.ps.activeTab())
	if err != nil {
		t.Fatalf("Clone A buildTabPlan after restart: %v", err)
	}
	if _, err := core.Execute(context.Background(), planA, nil); err != nil {
		t.Fatalf("Clone A Execute: %v", err)
	}
	if hits.Load() != 1 {
		t.Fatalf("expected 1 hit from Clone A, got %d", hits.Load())
	}
}

func TestUnsupportedBodyAndUnknownItemFieldsPreservedOnSave(t *testing.T) {
	dir := writeProject(t, "Preserve Unknown")
	graphqlYAML := `schemaVersion: 1
id: req-graphql
name: GraphQL Query
http:
  method: POST
  url: https://api.example.com/graphql
  body:
    type: graphql
    value:
      query: "{ viewer { login } }"
      customExtension: keep-graphql-ext
`
	urlencodedExtYAML := `schemaVersion: 1
id: req-ext-form
name: Form With Unknown Item Key
http:
  method: POST
  url: https://api.example.com/form
  body:
    type: urlencoded
    value:
      - name: client_id
        value: demo
        xCustomFlag: preserve-me
`
	colDir := filepath.Join(dir, ".gorequest", "collections")
	if err := os.MkdirAll(colDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gqlPath := filepath.Join(colDir, "graphql.request.yaml")
	extPath := filepath.Join(colDir, "ext-form.request.yaml")
	if err := os.WriteFile(gqlPath, []byte(graphqlYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(extPath, []byte(urlencodedExtYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	ps := app.ps

	// 1. Unsupported body mode (graphql): editing URL and clicking Save must NOT swallow body!
	gqlEntry := ps.project.Requests["req-graphql"]
	if gqlEntry == nil {
		t.Fatal("req-graphql not loaded")
	}
	ps.openRequest(gqlEntry)
	gqlTab := ps.activeTab()
	gqlTab.Section = 3 // Body

	tt := ui.NewTester(app.View, 1240, 840)
	tt.Frame()
	if !tt.HasText("Preserved unsupported body mode: graphql") || !tt.HasText("Replace Body") {
		t.Fatalf("expected unsupported body preservation card; texts=%v", tt.Texts())
	}

	gqlTab.URL = "https://api.example.com/v2/graphql"
	gqlTab.Dirty = true
	tt.Frame()
	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("click Save on graphql request: %v", err)
	}
	savedGQL, err := os.ReadFile(gqlPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"https://api.example.com/v2/graphql", "type: graphql", "viewer", "customExtension: keep-graphql-ext"} {
		if !strings.Contains(string(savedGQL), want) {
			t.Fatalf("saved graphql YAML lost %q:\n%s", want, savedGQL)
		}
	}

	// Clicking "Replace Body" explicitly lets the user replace it with None.
	if err := tt.Click("Replace Body"); err != nil {
		t.Fatalf("click Replace Body: %v", err)
	}
	if !gqlTab.Dirty {
		t.Fatal("Replace Body should mark tab dirty")
	}

	// 2. Supported body mode (urlencoded) with unknown item key xCustomFlag:
	// Editing rows and saving preserves xCustomFlag on disk, while send refuses unknown field!
	extEntry := ps.project.Requests["req-ext-form"]
	ps.openRequest(extEntry)
	extTab := ps.activeTab()
	extTab.Section = 3
	tt.Frame()

	if !tt.HasText("xCustomFlag") {
		t.Fatalf("expected warning banner mentioning unknown field xCustomFlag; texts=%v", tt.Texts())
	}
	bs := ensureBodyState(extTab, app)
	bs.formRows[0].Value = "updated-demo"
	bs.markMutated(extTab)
	tt.Frame()

	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("save ext-form: %v", err)
	}
	savedExt, err := os.ReadFile(extPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(savedExt), "updated-demo") || !strings.Contains(string(savedExt), "xCustomFlag: preserve-me") {
		t.Fatalf("saved urlencoded YAML lost unknown item field:\n%s", savedExt)
	}
	if _, _, err := app.buildTabPlan(context.Background(), extTab); err == nil || !core.IsConfig(err) {
		t.Fatalf("expected buildTabPlan to reject unknown item field xCustomFlag with ConfigError, got %v", err)
	}
}

func TestCustomHTTPMethodPickerAndModal(t *testing.T) {
	var gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		w.WriteHeader(http.StatusMultiStatus)
	}))
	defer srv.Close()

	a, ps := openTestProject(t, "Custom Method")
	tab := openRequestTabs(t, ps, "WebDAV")[0]
	tab.URL = srv.URL + "/dav"

	harness := func(c *ui.Context) {
		th := c.Theme()
		p := uikit.PaletteFor(th)
		ui.Row(c).Padding(16).Children(func() {
			a.b03MethodPicker(c, p, tab)
		})
		a.b03URLPreview(c, th, tab)
		a.b03CustomMethodModal(c, th)
	}

	tt := ui.NewTester(harness, 640, 420)
	tt.Frame()

	if err := tt.Click("GET"); err != nil {
		t.Fatalf("open method menu: %v", err)
	}
	if err := tt.Click("Custom Method…"); err != nil {
		t.Fatalf("click Custom Method…: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	if !tt.HasText("Custom HTTP Method") {
		t.Fatalf("custom method modal did not open; texts=%v", tt.Texts())
	}

	// Invalid method token with space is rejected
	st := a.customMethodUI()
	st.draft = "BAD METHOD"
	if err := tt.Click("Apply"); err != nil {
		t.Fatalf("click Apply invalid: %v", err)
	}
	if !st.open || st.errMsg == "" {
		t.Fatal("invalid HTTP method token must keep dialog open with error")
	}

	// Valid custom RFC 9110 method token succeeds, normalizes to uppercase, and executes
	st.draft = "propfind"
	if err := tt.Click("Apply"); err != nil {
		t.Fatalf("click Apply valid: %v", err)
	}
	if st.open || tab.Method != "PROPFIND" || !tab.Dirty {
		t.Fatalf("expected PROPFIND applied and dirty, got method=%q open=%v dirty=%v", tab.Method, st.open, tab.Dirty)
	}

	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("saveTab custom method: %v", err)
	}
	_, plan, err := a.buildTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTabPlan PROPFIND: %v", err)
	}
	res, err := core.Execute(context.Background(), plan, nil)
	if err != nil {
		t.Fatalf("Execute PROPFIND: %v", err)
	}
	if gotMethod != "PROPFIND" || res.Status != http.StatusMultiStatus {
		t.Fatalf("gotMethod=%q status=%d", gotMethod, res.Status)
	}
}

func TestWorkspaceIdleWithBodyEditorRequestsNoFrames(t *testing.T) {
	a, ps := openTestProject(t, "Idle Budget")
	tab := openRequestTabs(t, ps, "Check Idle")[0]
	tab.Section = 3 // Body

	for i := range uiBodyModes {
		bs := ensureBodyState(tab, a)
		bs.modeIdx = i
		tab.bodyEditor = nil
		bs.builtEditorKey = ""

		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		tt.Advance(time.Second)
		tt.Frame()
		if tt.FramePending() {
			t.Fatalf("body mode %q keeps requesting animation frames while idle", uiBodyModes[i])
		}
	}
}
