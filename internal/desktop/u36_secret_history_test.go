package desktop

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/localstate"
)

func TestConfigFullCapturePersistsAndDefaultsOff(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// 1. Fresh config defaults fullCapture to false while keeping redacted snapshots on.
	c1 := newConfigStore(cfgPath)
	if c1.historyFullCapture() {
		t.Fatal("fresh config must default historyFullCapture to false")
	}
	if !c1.historySaveSnapshots() || c1.historySaveBodies() {
		t.Fatalf("unexpected default history prefs: snap=%v bodies=%v", c1.historySaveSnapshots(), c1.historySaveBodies())
	}

	// 2. Old config.json without fullCapture loads safely as false.
	if err := os.WriteFile(cfgPath, []byte(`{"history":{"snapshots":true,"bodies":true,"retentionDays":30}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c2 := newConfigStore(cfgPath)
	if c2.historyFullCapture() {
		t.Fatal("legacy config without fullCapture must default to false")
	}
	if c2.historyRetentionDays() != 30 || !c2.historySaveBodies() {
		t.Fatalf("legacy config fields lost: retention=%d bodies=%v", c2.historyRetentionDays(), c2.historySaveBodies())
	}

	// 3. Enabling and disabling fullCapture persists across reload without losing retention/redacted prefs.
	c2.setHistoryFullCapture(true)
	c3 := newConfigStore(cfgPath)
	if !c3.historyFullCapture() {
		t.Fatal("fullCapture=true did not persist")
	}
	if c3.historyRetentionDays() != 30 {
		t.Fatalf("retentionDays = %d, want 30", c3.historyRetentionDays())
	}

	c3.setHistoryFullCapture(false)
	c4 := newConfigStore(cfgPath)
	if c4.historyFullCapture() {
		t.Fatal("fullCapture=false did not persist")
	}
	if !c4.historySaveBodies() || c4.historyRetentionDays() != 30 {
		t.Fatalf("redacted prefs not preserved after turning fullCapture off: bodies=%v retention=%d", c4.historySaveBodies(), c4.historyRetentionDays())
	}
}

func TestSettingsHistoryFullCaptureToggleAndNarrowLayout(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		lang := lang
		t.Run(string(lang), func(t *testing.T) {
			m, _ := newTestManager(t)
			m.cfg.cfg.Language = string(lang)
			cfg, prefs := m.cfg, m.workspacePrefs()
			msgs := messagesFor(cfg.language())
			var gotStatus string
			width := 340

			tt := ui.NewTester(func(c *ui.Context) {
				p := uikit.PaletteFor(c.Theme())
				ui.Column(c).Width(float32(width)).Children(func() {
					settingsCategoryContent(c, p, msgs, settingsCategoryHistory, cfg, prefs, func(s string) {
						gotStatus = s
					})
				})
			}, width, 900)
			tt.Frame()
			assertNoHorizontalOverflow(t, tt, width)
			if cfg.historyFullCapture() {
				t.Fatal("full capture should start off")
			}
			if tt.HasText(msgs.HistoryFullCaptureNote) {
				t.Fatal("active full-capture note must not render while full capture is off")
			}

			// Single click enables full capture immediately (no confirmation modal).
			if err := tt.Click(msgs.HistoryFullCapture); err != nil {
				t.Fatalf("click HistoryFullCapture on: %v; texts=%v", err, tt.Texts())
			}
			tt.Frame()
			if !cfg.historyFullCapture() {
				t.Fatal("clicking HistoryFullCapture did not enable config")
			}
			if gotStatus != msgs.HistoryFullCaptureOn {
				t.Fatalf("status = %q, want %q", gotStatus, msgs.HistoryFullCaptureOn)
			}
			if !tt.HasText(msgs.HistoryFullCaptureNote) {
				t.Fatalf("expected HistoryFullCaptureNote when enabled; texts=%v", tt.Texts())
			}
			assertNoHorizontalOverflow(t, tt, width)

			// Clicking again restores default redacted mode.
			if err := tt.Click(msgs.HistoryFullCapture); err != nil {
				t.Fatalf("click HistoryFullCapture off: %v", err)
			}
			tt.Frame()
			if cfg.historyFullCapture() {
				t.Fatal("clicking HistoryFullCapture again did not disable config")
			}
			if gotStatus != msgs.HistoryFullCaptureOff {
				t.Fatalf("status = %q, want %q", gotStatus, msgs.HistoryFullCaptureOff)
			}
		})
	}
}

func TestDesktopFullHistoryEndToEndAndLegacyRedactedDistinction(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	tab := openRequestTabs(t, ps, "UploadOrder")[0]
	secret := "tok_u36_test_sentinel_99f"
	red := core.NewRedactor()
	red.RegisterSecret(secret)

	beforeProject := treeDigest(t, ps.root)

	// 1. Record an initial entry under the default redacted mode.
	redactedPlan := &core.Plan{
		Method:  "POST",
		URL:     "https://api.test/orders",
		Query:   [][2]string{{"token", secret}},
		Headers: [][2]string{{"Authorization", "Bearer " + secret}, {"Content-Type", "application/json"}},
		Body:    []byte(`{"secret":"` + secret + `"}`),
		HasBody: true,
	}
	redactedRes := &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Duration: 25 * time.Millisecond,
		Headers:  [][2]string{{"Content-Type", "application/json"}, {"Set-Cookie", "sid=" + secret}},
		Body:     []byte(`{"echo":"` + secret + `"}`),
		BodySize: int64(len(`{"echo":"` + secret + `"}`)),
	}
	a.recordHistory(tab, redactedPlan, red, redactedRes)

	// 2. Enable full unredacted capture in Settings/config.
	a.mgr.cfg.setHistoryFullCapture(true)

	// 700 KiB binary request body (> 64 KiB DefaultMaxSnapshotBody and > 512 KiB DefaultMaxRecordBytes).
	binReq := make([]byte, 700<<10)
	copy(binReq, []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0xff})
	copy(binReq[32:], []byte(secret))
	for i := 128; i < len(binReq); i++ {
		binReq[i] = byte((i * 17) & 0xff)
	}
	// 800 KiB JSON response body (> 64 KiB and > 512 KiB).
	largeJSON := []byte(fmt.Sprintf(`{"token":%q,"payload":%q}`, secret, strings.Repeat("Z", 800<<10)))

	fullPlan := &core.Plan{
		Method:  "POST",
		URL:     "https://user:" + secret + "@api.test/orders/binary",
		Query:   [][2]string{{"api_key", secret}, {"mode", "raw"}},
		Headers: [][2]string{{"Authorization", "Bearer " + secret}, {"Content-Type", "application/octet-stream"}},
		Body:    binReq,
		HasBody: true,
	}
	fullRes := &core.ExecResult{
		Status:   201,
		Proto:    "HTTP/2.0",
		Duration: 80 * time.Millisecond,
		Headers:  [][2]string{{"Content-Type", "application/json"}, {"Set-Cookie", "sid=" + secret}},
		Body:     largeJSON,
		BodySize: int64(len(largeJSON)),
	}
	a.recordHistory(tab, fullPlan, red, fullRes)

	// 3. Record a failure with raw secret in the error string.
	rawErr := errors.New(`Post "https://api.test/orders/fail?token=` + secret + `": connection reset by peer`)
	failPlan := &core.Plan{
		Method:  "POST",
		URL:     "https://api.test/orders/fail",
		Query:   [][2]string{{"token", secret}},
		Headers: [][2]string{{"X-Token", secret}},
	}
	a.recordHistoryFailure(tab, failPlan, red, rawErr, nil)

	// 4. Record a network-truncated response and an unbuffered streamed upload to test honest boundaries.
	truncBody := []byte("prefix-" + secret)
	truncPlan := &core.Plan{
		Method:  "POST",
		URL:     "https://api.test/orders/stream",
		Headers: [][2]string{{"Content-Type", "multipart/form-data; boundary=abc"}, {"Content-Length", "4194304"}},
		Body:    nil,
		HasBody: true, // unbuffered stream: Body is nil while HasBody is true
	}
	truncRes := &core.ExecResult{
		Status:    200,
		Proto:     "HTTP/1.1",
		Headers:   [][2]string{{"Content-Type", "text/plain"}},
		Body:      truncBody,
		BodySize:  int64(len(truncBody)),
		Truncated: true,
	}
	a.recordHistory(tab, truncPlan, red, truncRes)

	// Reload history (runs loadFiltered + cleanup + loadFiltered) and verify no secondary trim!
	a.historyView = true
	a.reloadHistory()
	if len(a.historyRecords) != 4 {
		t.Fatalf("historyRecords = %d, want 4; diag=%q", len(a.historyRecords), a.historyUI.diag)
	}
	if strings.Contains(a.historyUI.diag, a.msg().HistorySnapshotTrimmed) {
		t.Fatalf("full capture unexpectedly trimmed snapshots: diag=%q", a.historyUI.diag)
	}

	var (
		gotRedacted *historyRecord
		gotFull     *historyRecord
		gotFail     *historyRecord
		gotTrunc    *historyRecord
	)
	for i := range a.historyRecords {
		r := &a.historyRecords[i]
		switch {
		case strings.Contains(r.URL, "/orders/binary"):
			gotFull = r
		case strings.Contains(r.URL, "/orders/fail"):
			gotFail = r
		case strings.Contains(r.URL, "/orders/stream"):
			gotTrunc = r
		default:
			gotRedacted = r
		}
	}
	if gotRedacted == nil || gotFull == nil || gotFail == nil || gotTrunc == nil {
		t.Fatalf("missing expected records: %+v", a.historyRecords)
	}

	// Verify older redacted record remained redacted and never masquerades as unredacted!
	if gotRedacted.IsUnredacted() {
		t.Fatal("legacy redacted record must NOT report IsUnredacted()")
	}
	rawOldJSON, _ := json.Marshal(gotRedacted)
	if strings.Contains(string(rawOldJSON), secret) {
		t.Fatalf("old redacted record leaked secret: %s", rawOldJSON)
	}

	// Verify full record preserved raw URL/userinfo/query, headers, 700 KiB binary req, and 800 KiB JSON resp.
	if !gotFull.IsUnredacted() || !gotFull.HasFullSnapshots() {
		t.Fatalf("full record flags wrong: IsUnredacted=%v HasFullSnapshots=%v", gotFull.IsUnredacted(), gotFull.HasFullSnapshots())
	}
	if !strings.Contains(gotFull.URL, "user:"+secret+"@") || !strings.Contains(gotFull.URL, "api_key="+secret) {
		t.Fatalf("full record URL did not preserve userinfo/query secret: %q", gotFull.URL)
	}
	gotBin, err := gotFull.Request.BodyBytes()
	if err != nil || !bytes.Equal(gotBin, binReq) {
		t.Fatalf("reloaded binary request body mismatch: len=%d want=%d err=%v", len(gotBin), len(binReq), err)
	}
	gotLargeJSON, err := gotFull.Response.BodyBytes()
	if err != nil || !bytes.Equal(gotLargeJSON, largeJSON) {
		t.Fatalf("reloaded large JSON response body mismatch: len=%d want=%d err=%v", len(gotLargeJSON), len(largeJSON), err)
	}

	// Verify failure record preserved raw error & raw query token.
	if !gotFail.IsUnredacted() || !strings.Contains(gotFail.Error, secret) || !strings.Contains(gotFail.URL, secret) {
		t.Fatalf("failure record did not preserve raw secret: %+v", gotFail)
	}

	// Verify network-truncated + streamed-upload record preserved captured response prefix and marked boundaries honestly.
	if gotTrunc.HasFullSnapshots() {
		t.Fatal("truncated/streamed record must not claim HasFullSnapshots()")
	}
	if gotTrunc.Request == nil || gotTrunc.Request.Omitted != localstate.OmitStream {
		t.Fatalf("expected OmitStream on unbuffered upload, got %+v", gotTrunc.Request)
	}
	if gotTrunc.Response == nil || !gotTrunc.Response.Truncated || string(gotTrunc.Response.Body) != string(truncBody) {
		t.Fatalf("expected captured prefix + Truncated=true, got %+v", gotTrunc.Response)
	}

	// 5. Verify UI truthfulness in History panel & History Detail modal!
	m := a.msg()
	tt := ui.NewTester(a.View, 1180, 860)

	// 5a. Old redacted record opened in detail modal while full capture is enabled MUST still say Redacted!
	a.historyUI.detailOpen = true
	a.historyUI.detail = *gotRedacted
	tt.Frame()
	if !hasExactLabel(tt, m.HistoryPrivacyFullNote) {
		t.Fatalf("history panel missing active full-capture privacy note; texts=%v", tt.Texts())
	}
	if !hasExactLabel(tt, m.HistoryCaptureRedacted) || !hasExactLabel(tt, m.HistoryRedactedNote) {
		t.Fatalf("old redacted record did not show redacted status/note; texts=%v", tt.Texts())
	}
	if hasExactLabel(tt, m.HistoryUnredactedNote) || hasExactLabel(tt, m.HistoryCaptureFull) {
		t.Fatalf("old redacted record falsely claimed unredacted status; texts=%v", tt.Texts())
	}

	// 5b. Full unredacted record shows Full unredacted status, unredacted note, lossless binary indicator, and shortened UI preview note.
	a.historyUI.detail = *gotFull
	tt.Frame()
	if !hasExactLabel(tt, m.HistoryCaptureFull) || !hasExactLabel(tt, m.HistoryUnredactedNote) {
		t.Fatalf("full record missing unredacted status/note; texts=%v", tt.Texts())
	}
	joined := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(joined, "base64") || !strings.Contains(joined, "Bearer "+secret) {
		t.Fatalf("full record detail missing binary/raw auth info; texts=%v", tt.Texts())
	}

	// 5c. Network-truncated + streamed-upload record shows partial boundary notes and NEVER claims Full unredacted.
	a.historyUI.detail = *gotTrunc
	tt.Frame()
	if !hasExactLabel(tt, m.HistoryCapturePartial) || !hasExactLabel(tt, m.HistoryTruncatedBoundaryNote) || !hasExactLabel(tt, m.HistoryStreamBoundaryNote) {
		t.Fatalf("partial record missing boundary disclosures; texts=%v", tt.Texts())
	}
	if hasExactLabel(tt, m.HistoryCaptureFull) {
		t.Fatalf("partial record must not claim HistoryCaptureFull; texts=%v", tt.Texts())
	}
	a.historyUI.detailOpen = false

	// Restoring the full record places the raw URL into the active tab.
	a.restoreHistory(*gotFull)
	if active := ps.activeTab(); active == nil || active.URL != gotFull.URL {
		t.Fatalf("restoreHistory did not restore raw URL: got %+v, want %q", active, gotFull.URL)
	}

	// 6. Retention cleanup still works on full records, and project files were never touched.
	a.mgr.cfg.setHistoryRetentionDays(30)
	expired := localstate.BuildRecord(localstate.HistoryInput{
		Ts:           time.Now().AddDate(0, 0, -60),
		Method:       "GET",
		URL:          "https://api.test/very-old?token=" + secret,
		Status:       200,
		ResponseBody: []byte("old-" + secret),
	}, localstate.FullPolicy())
	if _, err := hs.appendRecord(historyKey(ps), expired); err != nil {
		t.Fatal(err)
	}
	a.reloadHistory()
	for _, r := range a.historyRecords {
		if strings.Contains(r.URL, "very-old") {
			t.Fatalf("retention cleanup failed to prune expired full record: %+v", r)
		}
	}
	afterProject := treeDigest(t, ps.root)
	if !sameDigest(beforeProject, afterProject) {
		t.Fatal("history capture modified shared project files")
	}
}
