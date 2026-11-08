package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/update"
)

// TestU21U22U23ProjectMenuGenericImportSourceSwitchAndRepeatedCURLChinese verifies
// the integrated flow across U21 (Chinese project menu), U22 (Chinese import
// diagnostics + hot language switch) and U23 (generic import modal source-switch
// default policy + 3x repeated cURL copies + undo + stale-plan replanning).
func TestU21U22U23ProjectMenuGenericImportSourceSwitchAndRepeatedCURLChinese(t *testing.T) {
	a, ps := openChineseTestProject(t, "集成中文导入")
	_ = a.InitFileSync()

	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()

	// 1. Open project menu in Chinese: both U21 groups and U22 interop entries
	// must be present in Simplified Chinese, with no native menu.
	if err := tt.Click(ps.name()); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	tt.Frame()
	if got := tt.Menu(); got != nil {
		t.Fatalf("unexpected native menu: %v", got)
	}
	allChineseMenuLabels := append(projectMenuChineseLabels(),
		"导入 (cURL / Postman / 便携包)…",
		"从 cURL 导入…",
		"导出 / 便携包…",
		"复制为脱敏 cURL…",
	)
	for _, want := range allChineseMenuLabels {
		assertHasText(t, tt, want)
	}
	assertNoText(t, tt, "Import (cURL / Postman / Bundle)…")
	assertNoText(t, tt, "Performance Diagnostics…")

	// Hot-switch language while the project menu is open: both U21 and U22
	// entries switch to English on the next frame, then back to Chinese.
	a.mgr.cfg.cfg.Language = string(langEnglish)
	tt.Frame()
	assertHasText(t, tt, "Import (cURL / Postman / Bundle)…")
	assertHasText(t, tt, "Performance Diagnostics…")
	assertNoText(t, tt, "导入 (cURL / Postman / 便携包)…")

	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	tt.Frame()
	assertHasText(t, tt, "导入 (cURL / Postman / 便携包)…")
	assertHasText(t, tt, "性能诊断与采样…")

	// 2. Click the generic "导入 (cURL / Postman / 便携包)…" entry (which opens
	// in Postman mode with ConflictFail=0), then switch source to cURL inside
	// the modal and verify U23's UI patch updates ConflictPolicyIdx to 1 (Copy).
	if err := tt.Click("导入 (cURL / Postman / 便携包)…"); err != nil {
		t.Fatalf("click generic import menu entry: %v", err)
	}
	tt.Frame()
	st := a.Interop()
	if !st.ImportOpen || st.ImportSourceKind != ImportSourcePostman || st.ConflictPolicyIdx != 0 {
		t.Fatalf("generic import modal initial state: open=%v source=%d policy=%d",
			st.ImportOpen, st.ImportSourceKind, st.ConflictPolicyIdx)
	}

	if err := tt.Click(a.interopMsg().SourceCURL); err != nil {
		t.Fatalf("switch source to cURL in modal: %v", err)
	}
	tt.Frame()
	if st.ImportSourceKind != ImportSourceCURL || st.ConflictPolicyIdx != 1 {
		t.Fatalf("switching modal source to cURL must default policy to Copy(1): source=%d policy=%d",
			st.ImportSourceKind, st.ConflictPolicyIdx)
	}

	// Switching to Portable Bundle resets default policy to Fail(0), and back to cURL sets Copy(1).
	if err := tt.Click(a.interopMsg().SourcePortableBundle); err != nil {
		t.Fatalf("switch source to Portable Bundle: %v", err)
	}
	tt.Frame()
	if st.ConflictPolicyIdx != 0 {
		t.Fatalf("switching modal source to Portable Bundle must default policy to Fail(0), got %d", st.ConflictPolicyIdx)
	}
	if err := tt.Click(a.interopMsg().SourceCURL); err != nil {
		t.Fatalf("switch source back to cURL: %v", err)
	}
	tt.Frame()
	if st.ConflictPolicyIdx != 1 {
		t.Fatalf("switching modal source back to cURL must default policy to Copy(1), got %d", st.ConflictPolicyIdx)
	}

	// 3. Import the same cURL 3 times through the UI in Chinese mode.
	st.CURLInput = `curl -X POST https://api.example.com/v1/orders -H "Authorization: Bearer tok_secret_u21_25" -d '{"sku":"A1"}' -k`
	st.ImportRequestName = "创建订单"
	st.ImportTargetFolder = "orders"

	var ids []string
	var firstDigest string
	for i := 0; i < 3; i++ {
		if err := tt.Click(a.interopMsg().PreviewButton); err != nil {
			t.Fatalf("import %d click preview: %v", i, err)
		}
		tt.Frame()
		assertHasText(t, tt, "可应用")
		assertHasText(t, tt, "cURL 导入：POST https://api.example.com/v1/orders")
		assertHasText(t, tt, "curl-flag-unsupported")
		assertNoText(t, tt, "tok_secret_u21_25")
		if i > 0 {
			assertHasText(t, tt, "[已重命名]")
		}

		id := st.StagedPlan.ImportedRequestIDs()[0]
		if slices.Contains(ids, id) {
			t.Fatalf("import %d reused request ID %q (existing=%v)", i, id, ids)
		}
		ids = append(ids, id)

		if err := tt.Click(a.interopMsg().ConfirmApplyButton); err != nil {
			t.Fatalf("import %d click apply: %v", i, err)
		}
		tt.Frame()
		assertHasText(t, tt, "项文件变更")

		entry := ps.project.Requests[id]
		if entry == nil {
			t.Fatalf("import %d missing request %q", i, id)
		}
		rawBytes, _, err := ps.project.Store.Read(entry.RelPath)
		if err != nil {
			t.Fatalf("read %q: %v", entry.RelPath, err)
		}
		if i == 0 {
			firstDigest = core.Digest(rawBytes)
		} else {
			firstBytes, _, err := ps.project.Store.Read(ps.project.Requests[ids[0]].RelPath)
			if err != nil {
				t.Fatalf("read first copy: %v", err)
			}
			if core.Digest(firstBytes) != firstDigest {
				t.Fatalf("first copy mutated on import %d", i)
			}
		}
	}

	// Hot-switch language while the applied notice is visible in the import modal.
	a.status = ""
	a.mgr.cfg.cfg.Language = string(langEnglish)
	tt.Frame()
	assertHasText(t, tt, "Imported 1 file change(s)")
	assertNoText(t, tt, "已导入")

	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	tt.Frame()
	assertHasText(t, tt, "已导入 1 项文件变更")

	// Undo the 3rd import: copies 0 and 1 remain intact.
	if err := tt.Click(a.interopMsg().UndoLastImportButton); err != nil {
		t.Fatalf("click undo last import: %v", err)
	}
	tt.Frame()
	assertHasText(t, tt, "已回滚上次导入")
	if ps.project.Requests[ids[2]] != nil {
		t.Fatalf("third copy %q still present after undo", ids[2])
	}
	if ps.project.Requests[ids[0]] == nil || ps.project.Requests[ids[1]] == nil {
		t.Fatalf("first two copies lost after undoing third: %v", ps.project.Order)
	}

	// 4. Stale preview replanning + hot language switch of replan notice.
	if err := tt.Click(a.interopMsg().PreviewButton); err != nil {
		t.Fatalf("preview before stale test: %v", err)
	}
	tt.Frame()
	plannedRel := st.StagedPlan.Preview().IDMappings[0].TargetPath
	intruder := &core.Request{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            "req-external-intruder",
		Name:          "External Intruder",
		Protocol:      "http",
		HTTP:          &core.HTTPDef{Method: "GET", URL: "https://example.com/intruder"},
	}
	intruderBytes, err := ps.project.EncodeRequest(intruder)
	if err != nil {
		t.Fatalf("encode intruder: %v", err)
	}
	if err := ps.project.Store.WriteAtomic(plannedRel, intruderBytes); err != nil {
		t.Fatalf("write intruder: %v", err)
	}

	if err := tt.Click(a.interopMsg().ConfirmApplyButton); err != nil {
		t.Fatalf("click apply on stale plan: %v", err)
	}
	tt.Frame()
	assertHasText(t, tt, "目标在预览后发生变化，已重新生成预览；请检查后再次确认。")
	if st.StagedPlan == nil || st.StagedPlan.Preview().IDMappings[0].TargetPath == plannedRel {
		t.Fatalf("stale confirm did not re-plan to a new path (occupied=%q)", plannedRel)
	}

	// Hot-switch to English: the replan notice and re-staged preview both switch to English.
	a.mgr.cfg.cfg.Language = string(langEnglish)
	tt.Frame()
	assertHasText(t, tt, "The target changed after the preview; a fresh preview was generated. Review it and confirm again.")
	assertHasText(t, tt, "[RENAMED]")
	assertNoText(t, tt, "目标在预览后发生变化")
}

// TestU24U25SSEAndUpdateModalCoexistWithCrossChunkRedactionAndHostTransfer
// verifies that U24 (live SSE streaming, cross-chunk secret redaction + UTF-8/CRLF
// reassembly, multi-tab isolation, cancel retention) and U25 (Manager update
// check/dialog/host transfer) coexist in App without clobbering callbacks,
// cancellation or language switching.
func TestU24U25SSEAndUpdateModalCoexistWithCrossChunkRedactionAndHostTransfer(t *testing.T) {
	const secret = "super-secret-chunk-token-999"
	releaseA := make(chan struct{})
	releaseB := make(chan struct{})
	flushedA := make(chan struct{})
	flushedB := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)

		switch r.URL.Path {
		case "/stream-a":
			// Split the secret, a multi-byte UTF-8 rune ("中" = E4 B8 AD) and
			// CRLF terminators across separate flushed chunks before EOF.
			part1 := []byte("id: 1\r")
			part2 := append([]byte("\ndata: 实时"), []byte("中")[:1]...)
			part3 := append([]byte("中")[1:], []byte("文 tok="+secret[:10])...)
			part4 := []byte(secret[10:] + "\r")
			part5 := []byte("\n\r\n")
			for _, chunk := range [][]byte{part1, part2, part3, part4, part5} {
				_, _ = w.Write(chunk)
				if fl != nil {
					fl.Flush()
				}
			}
			close(flushedA)
			select {
			case <-releaseA:
			case <-r.Context().Done():
			}
		case "/stream-b":
			_, _ = io.WriteString(w, "data: tab-b-only-event\n\n")
			if fl != nil {
				fl.Flush()
			}
			close(flushedB)
			select {
			case <-releaseB:
			case <-r.Context().Done():
			}
		}
	}))
	defer srv.Close()
	defer func() {
		for _, ch := range []chan struct{}{releaseA, releaseB} {
			select {
			case <-ch:
			default:
				close(ch)
			}
		}
	}()

	m, _ := newTestManager(t)
	m.cfg.cfg.Language = string(langSimplifiedChinese)
	var windows []*sseTestWindow
	m.newWindow = func(s windowSpec) window {
		w := &sseTestWindow{fakeWindow: &fakeWindow{title: s.Title}, changed: make(chan struct{}, 1024)}
		windows = append(windows, w)
		return w
	}

	installed := "0.0.1-dev"
	clock := fixedClock()
	cand := testUpdateCandidate(t, "0.0.2-dev")
	checker := &fakeUpdateChecker{result: availableUpdate(cand)}
	statePath := filepath.Join(filepath.Dir(m.cfg.path), "update-state.json")
	u := newUpdateController(m, update.NewStateStore(statePath), checker, &fakeUpdateDownloader{},
		clock.Now, func() string { return installed }, func(string) error { return nil }, "darwin")
	m.update = u

	if err := m.openProject(writeProject(t, "SSE+Update One")); err != nil {
		t.Fatalf("openProject 1: %v", err)
	}
	if err := m.openProject(writeProject(t, "SSE+Update Two")); err != nil {
		t.Fatalf("openProject 2: %v", err)
	}
	app1, w1 := m.workspaces[0], windows[0]
	app2, w2 := m.workspaces[1], windows[1]
	m.secrets = core.MapSecrets{"streamTok": secret}

	tabs := openRequestTabs(t, app1.ps, "TabA", "TabB")
	tabA, tabB := tabs[0], tabs[1]
	tabA.URL = srv.URL + "/stream-a"
	tabA.HeaderRows = []*KVRow{{ID: nextRowID(), Enabled: true, Name: "X-Stream-Secret", Value: "{{secret.streamTok}}"}}
	tabB.URL = srv.URL + "/stream-b"

	// Start both SSE streams on app1.
	app1.trySend(tabA)
	app1.trySend(tabB)

	select {
	case <-flushedA:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed stream A")
	}
	select {
	case <-flushedB:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed stream B")
	}

	waitSSE(t, w1, "stream A first event before EOF", func() bool {
		return tabA.Resp != nil && strings.Contains(string(tabA.Resp.Body), "实时中文")
	})
	waitSSE(t, w1, "stream B first event before EOF", func() bool {
		return tabB.Resp != nil && strings.Contains(string(tabB.Resp.Body), "tab-b-only-event")
	})

	// Check cross-chunk secret redaction and multi-tab isolation before EOF.
	sseLocked(w1, func() {
		bodyA := string(tabA.Resp.Body)
		bodyB := string(tabB.Resp.Body)
		if strings.Contains(bodyA, secret) {
			t.Fatalf("split secret leaked into stream A body: %q", bodyA)
		}
		if !strings.Contains(bodyA, "实时中文 tok=***") {
			t.Fatalf("expected reassembled UTF-8 + redacted secret in stream A, got %q", bodyA)
		}
		if strings.Contains(bodyA, "tab-b-only-event") || strings.Contains(bodyB, "实时中文") {
			t.Fatalf("events crossed tabs: A=%q B=%q", bodyA, bodyB)
		}
	})

	// Trigger automatic update check hosted on app1 while both SSE streams are open.
	u.setHost(w1)
	u.autoCheck()
	if !u.dialogOpen || u.dialogKind != updateDialogAvailable {
		t.Fatalf("expected update dialog to open on app1: open=%v kind=%v", u.dialogOpen, u.dialogKind)
	}

	app1.ps.active = 0
	tt1 := ui.NewTester(w1.view(app1), 1280, 860)
	tt1.Frame()

	// Both the live Chinese SSE status and the Chinese update modal render together.
	assertHasText(t, tt1, "SSE 接收中 · 1 个事件")
	assertHasText(t, tt1, translate(updateMessagesZh.AvailableTitle, "0.0.2-dev"))
	assertHasText(t, tt1, "0.0.1-dev")
	assertNoText(t, tt1, "0.1.0 (MVP)")

	// Hot-switch to English: both SSE status and update modal switch on the next frame.
	m.cfg.cfg.Language = string(langEnglish)
	tt1.Frame()
	assertHasText(t, tt1, "SSE streaming · 1 events")
	assertHasText(t, tt1, translate(updateMessagesEn.AvailableTitle, "0.0.2-dev"))

	// Cancel Tab A's stream: Tab A retains its event, Tab B stays streaming,
	// and the update modal remains open.
	sseLocked(w1, func() {
		if tabA.cancel != nil {
			tabA.cancel()
		}
	})
	waitSSE(t, w1, "tab A canceled completion", func() bool { return !tabA.Sending })
	tt1.Frame()
	assertHasText(t, tt1, "SSE canceled · 1 events kept")
	assertHasText(t, tt1, translate(updateMessagesEn.AvailableTitle, "0.0.2-dev"))
	sseLocked(w1, func() {
		if !strings.Contains(string(tabA.Resp.Body), "实时中文 tok=***") {
			t.Fatalf("tab A lost event after cancel: %q", string(tabA.Resp.Body))
		}
		if !tabB.Sending {
			t.Fatal("canceling tab A should not stop tab B")
		}
	})

	// Close app1 while app2 is still open: app1's remaining stream (tab B) is
	// canceled and the open update modal transfers to app2.
	w1.mu.Lock()
	app1.win.Close()
	w1.mu.Unlock()
	waitSSE(t, w1, "tab B canceled on window close", func() bool { return !tabB.Sending })

	if got := u.getHost(); got != w2 {
		t.Fatalf("closing host workspace did not transfer update modal to surviving workspace: host=%v want=%v", got, w2)
	}
	tt2 := ui.NewTester(w2.view(app2), 1280, 860)
	tt2.Frame()
	assertHasText(t, tt2, translate(updateMessagesEn.AvailableTitle, "0.0.2-dev"))
}
