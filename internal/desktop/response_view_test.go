package desktop

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func TestResponseBodyExcludesMetadata(t *testing.T) {
	res := &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Duration: 12 * time.Millisecond,
		BodySize: 12,
		Body:     []byte(`{"ok":true}`),
		Pretty:   "{\n  \"ok\": true\n}",
		Headers: [][2]string{
			{"Content-Type", "application/json"},
			{"Content-Length", "12"},
			{"Date", "Wed, 05 Nov 2026 00:00:00 GMT"},
		},
	}

	body := responseBodyText(res, false)
	for _, meta := range []string{"Content-Type", "Content-Length", "Date", "HTTP/1.1", "200"} {
		if strings.Contains(body, meta) {
			t.Errorf("pretty body leaked metadata %q: %q", meta, body)
		}
	}
	if body != res.Pretty {
		t.Errorf("pretty body = %q, want res.Pretty", body)
	}
	if !strings.Contains(body, "\n  \"ok\"") {
		t.Errorf("pretty body should be indented: %q", body)
	}

	raw := responseBodyText(res, true)
	if raw != string(res.Body) {
		t.Errorf("raw body = %q, want original bytes", raw)
	}
	if strings.Contains(raw, "Content-Type") {
		t.Errorf("raw body leaked headers: %q", raw)
	}
}

func TestResponseBodyNonJSONFallsBackToRaw(t *testing.T) {
	res := &core.ExecResult{Body: []byte("<html><body>hi</body></html>")}
	if responseBodyIsJSON(res) {
		t.Error("non-JSON body must not be reported as JSON")
	}
	if got := responseBodyText(res, false); got != "<html><body>hi</body></html>" {
		t.Errorf("got %q, want raw text fallback", got)
	}
}

func TestResponseBodyInvalidJSONFallsBackToRaw(t *testing.T) {
	// Execute leaves Pretty empty when the body is not valid JSON.
	res := &core.ExecResult{Body: []byte(`{"broken": `)}
	if got := responseBodyText(res, false); got != `{"broken": ` {
		t.Errorf("got %q, want raw fallback", got)
	}
}

func TestResponseBodyEmpty(t *testing.T) {
	if got := responseBodyText(nil, false); got != "" {
		t.Errorf("nil result body = %q, want empty", got)
	}
	if got := responseBodyText(&core.ExecResult{}, false); got != "" {
		t.Errorf("empty result body = %q, want empty", got)
	}
}

func TestHeaderRowsPreserveDuplicates(t *testing.T) {
	res := &core.ExecResult{Headers: [][2]string{
		{"Set-Cookie", "a=1"},
		{"Set-Cookie", "b=2"},
		{"X-Dup", "1"},
		{"X-Dup", "2"},
	}}
	rows := headerRows(res)
	if len(rows) != 4 {
		t.Fatalf("rows = %d, want 4 (duplicates preserved)", len(rows))
	}
	setCookie := 0
	dup := map[string]bool{}
	for _, r := range rows {
		switch r.Name {
		case "Set-Cookie":
			setCookie++
			if r.Value != "***" {
				t.Errorf("Set-Cookie not masked: %q", r.Value)
			}
		case "X-Dup":
			dup[r.Value] = true
		}
	}
	if setCookie != 2 {
		t.Errorf("Set-Cookie occurrences = %d, want 2", setCookie)
	}
	if !dup["1"] || !dup["2"] {
		t.Errorf("X-Dup values lost: %v", dup)
	}
}

func TestHeaderRowsRedactSensitive(t *testing.T) {
	res := &core.ExecResult{Headers: [][2]string{
		{"Authorization", "Bearer topsecret"},
		{"authorization", "Bearer lower"},
		{"Set-Cookie", "session=abc"},
		{"Content-Type", "application/json"},
	}}
	rows := headerRows(res)
	for _, r := range rows {
		switch strings.ToLower(r.Name) {
		case "authorization", "set-cookie":
			if r.Value != "***" || !r.Sensitive {
				t.Errorf("%s not redacted: %+v", r.Name, r)
			}
			if strings.Contains(r.Value, "topsecret") || strings.Contains(r.Value, "abc") {
				t.Errorf("%s leaked value %q", r.Name, r.Value)
			}
		case "content-type":
			if r.Sensitive || r.Value != "application/json" {
				t.Errorf("content-type wrongly masked: %+v", r)
			}
		}
	}
}

func TestTruncationNoticeIsOutOfBand(t *testing.T) {
	res := &core.ExecResult{
		Body:      []byte(`{"a":1}`),
		Pretty:    "{\n  \"a\": 1\n}",
		Truncated: true,
	}
	if notice := truncationNotice(res); !strings.Contains(notice, "truncated") {
		t.Errorf("notice = %q, want truncation message", notice)
	}
	if strings.Contains(responseBodyText(res, false), "truncated") {
		t.Error("pretty body must not embed the truncation notice")
	}
	if strings.Contains(responseBodyText(res, true), "truncated") {
		t.Error("raw body must not embed the truncation notice")
	}
	if got := truncationNotice(&core.ExecResult{}); got != "" {
		t.Errorf("untruncated notice = %q, want empty", got)
	}
}

func TestIsCanceledErr(t *testing.T) {
	if !isCanceledErr("request canceled") {
		t.Error("expected canceled detection")
	}
	if isCanceledErr("request failed: boom") {
		t.Error("did not expect canceled detection")
	}
}

func TestNetworkMessagesCoverBothLanguages(t *testing.T) {
	typ := reflect.TypeOf(networkMessages{})
	zh := reflect.ValueOf(networkMessagesZh)
	en := reflect.ValueOf(networkMessagesEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		zhVal := strings.TrimSpace(zh.Field(i).String())
		enVal := strings.TrimSpace(en.Field(i).String())
		if zhVal == "" {
			t.Errorf("networkMessagesZh.%s is empty", f.Name)
		}
		if enVal == "" {
			t.Errorf("networkMessagesEn.%s is empty", f.Name)
		}
		if zhVal == enVal {
			t.Errorf("networkMessages.%s has identical zh and en strings (%q)", f.Name, zhVal)
		}
	}
}

func TestResponseJSONTreeUIExpandCollapseSearchAndCopy(t *testing.T) {
	a, ps := openTestProject(t, "JSONTree")
	tab := openRequestTabs(t, ps, "Users")[0]
	rawJSON := `{"z_last":42,"a_first":{"token":"needle-value","items":[true,null]}}`
	tab.Resp = &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Duration: 15 * time.Millisecond,
		Body:     []byte(rawJSON),
		Pretty:   "{\n  \"z_last\": 42,\n  \"a_first\": {\n    \"token\": \"needle-value\",\n    \"items\": [\n      true,\n      null\n    ]\n  }\n}",
		BodySize: int64(len(rawJSON)),
	}
	tab.respVersion = 1

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// Switch to JSON Tree mode.
	if err := tt.Click("Tree"); err != nil {
		t.Fatalf("click Tree: %v", err)
	}
	if tab.RespRawIdx != 2 {
		t.Fatalf("RespRawIdx = %d, want 2 (Tree)", tab.RespRawIdx)
	}
	for _, want := range []string{"z_last:", "42", "$.z_last", "a_first:", "$.a_first.token", "\"needle-value\"", "Collapse All", "Expand All"} {
		if !tt.HasText(want) {
			t.Fatalf("JSON tree missing %q; texts=%v", want, tt.Texts())
		}
	}

	// Collapse All hides nested $.a_first.token.
	if err := tt.Click("Collapse All"); err != nil {
		t.Fatalf("click Collapse All: %v", err)
	}
	if tt.HasText("$.a_first.token") {
		t.Fatalf("expected $.a_first.token to be hidden after Collapse All; texts=%v", tt.Texts())
	}

	// Searching in Tree mode automatically expands ancestor paths leading to matches.
	tt.Key(ui.Cmd, ui.KeyF)
	tt.Frame()
	tab.respFindQuery = "needle-value"
	tab.respFindJump = true
	tt.Frame()
	if !tt.HasText("1 / 1") || !tt.HasText("$.a_first.token") {
		t.Fatalf("expected search in collapsed tree to auto-expand match and show 1 / 1; texts=%v", tt.Texts())
	}

	// Copy Path & Copy Value buttons update status.
	if err := tt.Click("Copy Path"); err != nil {
		t.Fatalf("click Copy Path: %v", err)
	}
	if a.status != "Copied" {
		t.Fatalf("status after Copy Path = %q, want Copied", a.status)
	}
}

// TestResponseFormatSwitchRightAlignedAfterActions pins U01's placement: the
// Pretty/Raw/Tree switch is the tool row's last control, after find/copy/save,
// and right-aligned to the response pane. The row wraps instead of overflowing
// at the compact acceptance width. Geometry comes from the real layout engine
// via ui.NewTester; no screenshots.
func TestResponseFormatSwitchRightAlignedAfterActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		w, h int
	}{
		{"1200x820", 1200, 820},
		{"1024x640", 1024, 640},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ps := openTestProject(t, "RespFormat "+tc.name)
			tab := openRequestTabs(t, ps, "Users")[0]
			tab.Resp = &core.ExecResult{
				Status:   200,
				Proto:    "HTTP/1.1",
				Duration: 10 * time.Millisecond,
				Body:     []byte(`{"ok":true}`),
				Pretty:   "{\n  \"ok\": true\n}",
				BodySize: 11,
			}
			tab.respVersion = 1

			var respRect ui.Rect
			tt := ui.NewTester(func(c *ui.Context) {
				a.View(c)
				respRect = ps.respRef.Bounds(c)
			}, tc.w, tc.h)
			tt.Frame()
			tt.Frame()
			if respRect.W <= 0 {
				t.Fatalf("response pane not measured: %+v", respRect)
			}
			paneRight := respRect.X + respRect.W

			tree, ok := tt.Find(networkMessagesEn.TreeMode)
			if !ok {
				t.Fatalf("Tree switch missing; texts=%v", tt.Texts())
			}
			save, ok := tt.Find(messagesEn.SaveResponse)
			if !ok {
				t.Fatalf("Save control missing; texts=%v", tt.Texts())
			}
			copyBtn, ok := tt.Find(messagesEn.CopyResponse)
			if !ok {
				t.Fatalf("Copy control missing; texts=%v", tt.Texts())
			}
			find, ok := tt.Find(messagesEn.FindPlaceholder)
			if !ok {
				t.Fatalf("Find control missing; texts=%v", tt.Texts())
			}

			// Reading order: find, then copy/save, then the body format switch.
			if copyBtn.X < find.X+find.W-1 {
				t.Fatalf("Copy is not after Find: find=%+v copy=%+v", find, copyBtn)
			}
			if save.X < copyBtn.X+copyBtn.W-1 {
				t.Fatalf("Save is not after Copy: copy=%+v save=%+v", copyBtn, save)
			}
			if tree.X < save.X+save.W-1 {
				t.Fatalf("Tree switch is not after Save: save=%+v tree=%+v", save, tree)
			}

			// Right-aligned to the pane's inner edge (the row's 10 DIP padding
			// plus the segmented track/segment padding sit to the text's right).
			if gap := paneRight - (tree.X + tree.W); gap < 0 || gap > 40 {
				t.Fatalf("Tree switch is not right-aligned: gap=%g pane=%+v tree=%+v", gap, respRect, tree)
			}
			// No overflow at either width: every tool control stays in the pane.
			for name, r := range map[string]ui.Rect{"find": find, "copy": copyBtn, "save": save, "tree": tree} {
				if r.X < respRect.X-1 || r.X+r.W > paneRight+1 {
					t.Fatalf("%s overflows the response pane: %+v pane=%+v", name, r, respRect)
				}
			}

			// The moved switch still drives the body format.
			if err := tt.Click(networkMessagesEn.TreeMode); err != nil {
				t.Fatalf("click Tree: %v", err)
			}
			if tab.RespRawIdx != 2 {
				t.Fatalf("RespRawIdx = %d, want 2 (Tree)", tab.RespRawIdx)
			}
		})
	}
}

func TestResponseBinaryInspectorAndExactSave(t *testing.T) {
	a, ps := openTestProject(t, "BinaryResp")
	tab := openRequestTabs(t, ps, "Download")[0]
	rawBytes := []byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0xff, 0xfe, 0x42}
	tab.Resp = &core.ExecResult{
		Status:            200,
		Proto:             "HTTP/1.1",
		Duration:          9 * time.Millisecond,
		Body:              rawBytes,
		BodySize:          int64(len(rawBytes)),
		TransferSize:      int64(len(rawBytes)),
		ContentType:       "image/png",
		IsBinary:          true,
		SuggestedFilename: "logo.png",
	}
	tab.respVersion = 1

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	for _, want := range []string{"Binary Response Payload", "MIME: image/png", "Save Raw Binary…", "89 50 4e 47"} {
		if !tt.HasText(want) {
			t.Fatalf("binary inspector missing %q; texts=%v", want, tt.Texts())
		}
	}
	if got := defaultSaveFilename(tab.Resp); got != "logo.png" {
		t.Fatalf("defaultSaveFilename = %q, want logo.png", got)
	}

	// Verify exact byte-for-byte binary save to disk (preserving NUL and non-UTF8 bytes).
	dest := filepath.Join(t.TempDir(), "saved-logo.png")
	if err := saveResponseToPath(tab.Resp, false, dest); err != nil {
		t.Fatalf("saveResponseToPath: %v", err)
	}
	saved, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(saved, rawBytes) {
		t.Fatalf("saved binary bytes mismatch: got %x, want %x", saved, rawBytes)
	}
}

func TestResponseTransferVsDecompressedSizeAndNetworkTraceTabs(t *testing.T) {
	a, ps := openTestProject(t, "NetTrace")
	tab := openRequestTabs(t, ps, "Compressed")[0]
	tab.Resp = &core.ExecResult{
		Status:          200,
		Proto:           "HTTP/2.0",
		Duration:        24 * time.Millisecond,
		Body:            []byte(`{"ok":true}`),
		Pretty:          "{\n  \"ok\": true\n}",
		BodySize:        4096,
		TransferSize:    180,
		ContentEncoding: "gzip",
		Decompressed:    true,
		Redirects: []core.RedirectHop{
			{
				Hop:                 1,
				StatusCode:          302,
				Method:              "GET",
				FromURL:             "https://auth.example.com/login",
				ToURL:               "https://api.example.com/v1/data",
				OriginChanged:       true,
				StrippedCredentials: true,
			},
		},
		Trace: &core.NetworkTrace{
			DNSObserved:      false, // reused connection: must NOT fake 0.00 ms
			ConnectObserved:  true,
			ConnectDuration:  4200 * time.Microsecond,
			TLSObserved:      true,
			TLSDuration:      8500 * time.Microsecond,
			WriteObserved:    true,
			WriteDuration:    600 * time.Microsecond,
			TTFBObserved:     true,
			TTFBDuration:     9100 * time.Microsecond,
			DownloadDuration: 1500 * time.Microsecond,
			ReusedConn:       true,
			RemoteAddr:       "127.0.0.1:8443",
			TLSVersion:       "TLS 1.3",
			TLSCipherSuite:   "TLS_AES_128_GCM_SHA256",
			TLSServerName:    "api.example.com",
			CustomCAUsed:     true,
			ClientCertUsed:   true,
			ProxyUsed:        "http://127.0.0.1:8080",
		},
	}
	tab.respVersion = 1

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// Status bar shows wire transfer size vs decompressed body size and redirect count.
	if !tt.HasText("180 B wire · 4 KiB body (gzip)") {
		t.Fatalf("status bar missing wire vs body size summary; texts=%v", tt.Texts())
	}
	if !tt.HasText("1 redirects") {
		t.Fatalf("status bar missing redirects badge; texts=%v", tt.Texts())
	}

	// Switch to Network diagnostic section.
	if err := tt.Click("Network"); err != nil {
		t.Fatalf("click Network tab: %v", err)
	}
	for _, want := range []string{
		"180 B",
		"4 KiB",
		"reused / n/a", // unobserved DNS must not be shown as 0.00 ms
		"4.2ms",
		"8.5ms",
		"TLS 1.3 · TLS_AES_128_GCM_SHA256",
		"http://127.0.0.1:8080",
		"#1 302 GET",
		"Cross-Origin",
		"Credentials Stripped",
	} {
		if !tt.HasText(want) {
			t.Fatalf("Network tab missing %q; texts=%v", want, tt.Texts())
		}
	}
}

func TestCookieJarModalViewEditClearAndProjectIsolation(t *testing.T) {
	m, _ := newTestManager(t)
	dirA := writeProject(t, "ProjA")
	dirB := writeProject(t, "ProjB")
	if err := m.openProject(dirA); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(dirB); err != nil {
		t.Fatal(err)
	}
	appA := m.workspaces[0]
	appB := m.workspaces[1]
	t.Cleanup(appA.StopFileSync)
	t.Cleanup(appB.StopFileSync)

	tabA := openRequestTabs(t, appA.ps, "ReqA")[0]
	openRequestTabs(t, appB.ps, "ReqB")
	// A response makes the view switch available; the Cookie Jar entry lives
	// inside the Cookies view now, not on a second tool row.
	tabA.Resp = &core.ExecResult{Status: 200, Proto: "HTTP/1.1", Body: []byte(`{}`), BodySize: 2}
	tabA.respVersion = 1

	ttA := ui.NewTester(appA.View, 1200, 820)
	ttA.Frame()

	// Open the Cookies view, then the Cookie Jar manager from inside it.
	if err := ttA.Click(appA.networkMsg().CookiesSection); err != nil {
		t.Fatalf("open Cookies view: %v", err)
	}
	if err := ttA.Click(appA.networkMsg().OpenCookieJarBtn); err != nil {
		t.Fatalf("open Cookie Jar modal: %v", err)
	}
	if !ttA.HasText("Project Cookie Jar") {
		t.Fatalf("Cookie Jar modal did not open; texts=%v", ttA.Texts())
	}

	stA := projectNetStateFor(appA.ps, appA)
	stA.cookieUI.editDomain = "api.example.com"
	stA.cookieUI.editPath = "/"
	stA.cookieUI.editName = "session_id"
	stA.cookieUI.editValue = "super-secret-cookie-a"
	stA.cookieUI.editSecure = true
	stA.cookieUI.editHttpOnly = true
	ttA.Frame()

	if err := ttA.Click("Save Cookie"); err != nil {
		t.Fatalf("click Save Cookie: %v", err)
	}
	if appA.cookieJarFor(appA.ps).Count() != 1 {
		t.Fatalf("expected 1 cookie in ProjA jar, got %d", appA.cookieJarFor(appA.ps).Count())
	}
	//ProjB's Cookie Jar must remain completely isolated (0 cookies).
	if appB.cookieJarFor(appB.ps).Count() != 0 {
		t.Fatalf("ProjB Cookie Jar was polluted by ProjA: count=%d", appB.cookieJarFor(appB.ps).Count())
	}

	// Cookie value is masked (***) by default in the modal; clicking Show reveals it.
	if ttA.HasText("super-secret-cookie-a") {
		t.Fatalf("cookie value should be masked by default; texts=%v", ttA.Texts())
	}
	if err := ttA.Click("Show"); err != nil {
		t.Fatalf("click Show: %v", err)
	}
	if !ttA.HasText("super-secret-cookie-a") {
		t.Fatalf("cookie value was not revealed after clicking Show; texts=%v", ttA.Texts())
	}

	// Edit loads the cookie back into the form; Clear All removes all cookies.
	if err := ttA.Click("Edit"); err != nil {
		t.Fatalf("click Edit: %v", err)
	}
	if stA.cookieUI.editName != "session_id" || stA.cookieUI.editValue != "super-secret-cookie-a" {
		t.Fatalf("Edit did not populate form: %+v", stA.cookieUI)
	}
	if err := ttA.Click("Clear All"); err != nil {
		t.Fatalf("click Clear All: %v", err)
	}
	if appA.cookieJarFor(appA.ps).Count() != 0 {
		t.Fatalf("Clear All left %d cookies", appA.cookieJarFor(appA.ps).Count())
	}
	if err := ttA.Click("Close"); err != nil {
		t.Fatalf("close Cookie Jar modal: %v", err)
	}

	// Verify Response Cookies tab renders Set-Cookie entries masked by default.
	tabA.Resp = &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Body:     []byte(`{}`),
		Pretty:   `{}`,
		BodySize: 2,
		FinalURL: "https://api.example.com/login",
		Headers: [][2]string{
			{"Set-Cookie", "resp_tok=top-secret-resp-cookie; Path=/; HttpOnly"},
		},
	}
	tabA.respVersion = 1
	ttA.Frame()
	if err := ttA.Click("Cookies"); err != nil {
		t.Fatalf("switch to Response Cookies tab: %v", err)
	}
	if !ttA.HasText("resp_tok") || ttA.HasText("top-secret-resp-cookie") {
		t.Fatalf("Response Cookies tab should show cookie name and mask value; texts=%v", ttA.Texts())
	}

	// Ensure no cookie values ever leaked into .gorequest/ files on disk.
	err := filepath.Walk(dirA, func(p string, info os.FileInfo, err error) error {
		if err != nil || info == nil || info.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), "super-secret-cookie-a") || strings.Contains(string(b), "top-secret-resp-cookie") {
			t.Fatalf("cookie secret leaked into project file %s", p)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNetworkSettingsModalAndEndToEndExecuteTabPlan(t *testing.T) {
	a, ps := openTestProject(t, "NetSettings")
	tab := openRequestTabs(t, ps, "RedirectTest")[0]
	// The Network/TLS settings entry lives inside the Network view, which
	// requires a response to be shown.
	tab.Resp = &core.ExecResult{Status: 200, Proto: "HTTP/1.1", Body: []byte(`{}`), BodySize: 2}
	tab.respVersion = 1

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// Open the Network view, then the settings modal from inside it.
	if err := tt.Click(a.networkMsg().NetworkSection); err != nil {
		t.Fatalf("click Network view: %v", err)
	}
	if err := tt.Click(a.networkMsg().NetworkBtn); err != nil {
		t.Fatalf("click Network & TLS…: %v", err)
	}
	if !tt.HasText("Network Transport, Proxy, TLS/mTLS & Target Policy") {
		t.Fatalf("Network modal did not open; texts=%v", tt.Texts())
	}

	st := projectNetStateFor(ps, a)
	st.dialTimeoutDraft = "6s"
	st.maxRedirectsDraft = "4"
	tt.Frame()
	if err := tt.Click("Apply & Save Device Network Settings"); err != nil {
		t.Fatalf("save network settings: %v", err)
	}
	if st.netModalOpen {
		t.Fatal("Network modal should close after Apply & Save")
	}
	if got := st.netStore.DeviceSettings().Timeouts.DialTimeout; got != 6*time.Second {
		t.Fatalf("DialTimeout = %v, want 6s", got)
	}

	// End-to-end execution via executeTabPlan: hop1 sets a cookie and redirects
	// cross-origin to hop2 while carrying a custom API key header.
	var hop2APIKey, hop2Cookie string
	hop2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop2APIKey = r.Header.Get("X-Custom-Key")
		hop2Cookie = r.Header.Get("Cookie")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"hop":2}`))
	}))
	defer hop2.Close()

	hop1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "jar_sid=captured_123; Path=/")
		http.Redirect(w, r, hop2.URL+"/dest", http.StatusFound)
	}))
	defer hop1.Close()

	tab.URL = strings.Replace(hop1.URL, "127.0.0.1", "localhost", 1) + "/start"
	tab.AuthMode = "apikey"
	tab.AuthKeyIn = "header"
	tab.AuthKeyInIdx = 0 // header
	tab.AuthKeyName = "X-Custom-Key"
	tab.AuthKeyValue = "custom-secret-key-val"

	req, plan, red, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatalf("buildTrackedTabPlan: %v", err)
	}
	res, err := a.executeTabPlan(context.Background(), tab, req, plan, red)
	if err != nil {
		t.Fatalf("executeTabPlan: %v", err)
	}
	if res.Status != 200 || len(res.Redirects) != 1 {
		t.Fatalf("status=%d redirects=%+v", res.Status, res.Redirects)
	}
	if hop2APIKey != "" || hop2Cookie != "" {
		t.Fatalf("cross-origin redirect leaked custom API key (%q) or cookie (%q)", hop2APIKey, hop2Cookie)
	}
	// Verify hop1's Set-Cookie was stored in this project's isolated CookieJar.
	if a.cookieJarFor(ps).Count() != 1 {
		t.Fatalf("expected project CookieJar to capture hop1 cookie, count=%d", a.cookieJarFor(ps).Count())
	}
}
