package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/core"
)

func TestFoundationCombinedSettingsAndInteropEntry(t *testing.T) {
	a, _ := openTestProject(t, "Foundation")
	a.showSettings = true
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	for _, label := range []string{a.msg().EditorSection, a.msg().AutoSaveSection, a.msg().HistorySettingsLabel} {
		if !tt.HasText(label) {
			t.Fatalf("missing combined settings group %q", label)
		}
	}
	a.showSettings = false
	a.OpenImportCURLModal()
	tt.Frame()
	if !tt.HasText(a.interopMsg().ImportModalTitle) {
		t.Fatal("import modal not wired to application view")
	}
	a.Interop().ImportOpen = false
	tt.Frame()
	// The Git rail must have its own controller and leave the HTTP project intact.
	qw := &queuedWindow{fakeWindow: &fakeWindow{}, updates: make(chan func(), 16)}
	a.win = qw
	if err := tt.Click(a.gitMsg().NavLabel); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if a.git != nil {
		t.Cleanup(a.git.close)
	}
	if !a.gitView || a.git == nil || a.ps.project == nil {
		t.Fatal("Git navigation did not retain project state")
	}
}

func TestFoundationImportUndoProtectsDirtyRequest(t *testing.T) {
	a, ps := openTestProject(t, "ImportUndo")
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	defer a.StopFileSync()
	a.OpenImportCURLModal()
	a.Interop().CURLInput = "curl https://example.com/undo-protection"
	a.StageImportPreview()
	a.ConfirmAndApplyImport()
	st := a.Interop()
	if st.LastReceipt == nil {
		t.Fatalf("import failed: %s", st.ImportErr)
	}
	tab := ps.activeTab()
	rel := tab.Entry.RelPath
	tab.URL = "https://example.com/unsaved"
	tab.Dirty = true
	a.RollbackLastImport()
	if st.ImportErr == "" || st.LastReceipt == nil || !ps.project.Store.Exists(rel) || ps.indexOfTab(tab) < 0 || tab.URL != "https://example.com/unsaved" {
		t.Fatal("undo discarded imported request edits")
	}
	tab.Dirty = false
	a.RollbackLastImport()
	if st.ImportErr != "" || ps.project.Store.Exists(rel) {
		t.Fatalf("undo after discard failed: %s", st.ImportErr)
	}
}

func TestFoundationTransferUsesLatestSavedRequestInMemory(t *testing.T) {
	_, src := openTestProject(t, "TransferSource")
	_, dst := openTestProject(t, "TransferTarget")
	tab := openRequestTabs(t, src, "TransferLatest")[0]
	plan, err := core.PlanRequestTransfer(src.project, dst.project, tab.Entry, "", core.TransferCopy)
	if err != nil {
		t.Fatal(err)
	}
	tab.URL = "https://example.com/latest"
	if err := src.saveTab(tab); err != nil {
		t.Fatal(err)
	}
	if err := plan.Apply(); err != nil {
		t.Fatal(err)
	}
	target := dst.project.Requests[plan.TargetID]
	if target.Request.HTTP.URL != tab.URL {
		t.Fatal("target in-memory request stayed at preview version")
	}
	raw, _, err := dst.project.Store.Read(target.RelPath)
	if err != nil || !strings.Contains(string(raw), tab.URL) {
		t.Fatal("target disk and memory disagree")
	}
}

func TestFoundationImportIsRecordedInLocalHistoryAndVisibleToGit(t *testing.T) {
	a, ps, g := gitProject(t, "ImportedHistory")
	a.mgr.history = &historyStore{dir: t.TempDir()}
	a.OpenImportCURLModal()
	a.Interop().CURLInput = "curl https://example.com/import-history"
	a.StageImportPreview()
	a.ConfirmAndApplyImport()
	if err := a.Interop().ImportErr; err != "" {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	_, plan, red, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatal(err)
	}
	a.recordHistory(tab, plan, red, &core.ExecResult{Status: 204, Proto: "HTTP/1.1"})
	if len(a.mgr.history.load(historyKey(ps))) != 1 {
		t.Fatal("imported request absent from local history")
	}
	if err := g.refreshNow(context.Background()); err != nil {
		t.Fatal(err)
	}
	expected := ".gorequest/" + tab.Entry.RelPath
	found := false
	for _, e := range g.status.Entries {
		if e.Path == expected {
			found = true
		}
	}
	if !found {
		t.Fatalf("imported request %s absent from Git status", expected)
	}
	if _, err := os.Stat(ps.project.Dir + "/" + tab.Entry.RelPath); err != nil {
		t.Fatal(err)
	}
}

func TestFoundationSendUsesNetworkJarAndLocalHistory(t *testing.T) {
	a, ps := openTestProject(t, "NetworkHistory")
	a.mgr.history = &historyStore{dir: t.TempDir()}
	qw := &queuedWindow{fakeWindow: &fakeWindow{}, updates: make(chan func(), 16)}
	a.win = qw
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "session", Value: "test-session-secret", Path: "/"})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	tab := openRequestTabs(t, ps, "SendWithNetwork")[0]
	tab.URL = server.URL
	a.send(tab)
	waitForSendDone(t, qw, tab)
	if tab.Resp == nil || tab.Resp.Trace == nil || a.cookieJarFor(ps).Count() != 1 {
		t.Fatal("application send did not attach network options")
	}
	if len(a.mgr.history.load(historyKey(ps))) != 1 {
		t.Fatal("network send not recorded in local history")
	}
}
