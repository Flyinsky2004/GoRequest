package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/localstate"
)

// newHistoryTestApp opens a project window whose history store points at a
// throwaway directory, so these tests never touch the user's real history.
func newHistoryTestApp(t *testing.T) (*App, *projectState, *historyStore) {
	t.Helper()
	m, _ := newTestManager(t)
	dir := writeProject(t, "History")
	hs := &historyStore{dir: t.TempDir()}
	m.history = hs
	if err := m.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	return m.workspaces[0], m.workspaces[0].ps, hs
}

func treeDigest(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		rel, _ := filepath.Rel(root, path)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:])
		return nil
	})
	if err != nil {
		t.Fatalf("walk project: %v", err)
	}
	return out
}

func sameDigest(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

func historySecret() string { return "tok-" + "Hist-Test-4a1c" }

func TestHistoryKeySeparatesClones(t *testing.T) {
	_, ps1 := openTestProject(t, "Same")
	_, ps2 := openTestProject(t, "Same")
	if ps1.project.File.ID != ps2.project.File.ID {
		t.Fatalf("fixture should share a project id: %q vs %q", ps1.project.File.ID, ps2.project.File.ID)
	}
	if historyKey(ps1) == historyKey(ps2) {
		t.Fatalf("two clones must not share history: %q", historyKey(ps1))
	}
	if historyKey(nil) != "" {
		t.Fatal("nil project state must have no history key")
	}
}

func TestRecordHistoryRedactsAndLeavesProjectUntouched(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	tab := openRequestTabs(t, ps, "Login")[0]
	secret := historySecret()

	red := core.NewRedactor()
	red.RegisterSecret(secret)
	plan := &core.Plan{
		Method:  "POST",
		URL:     "https://api.test/login",
		Headers: [][2]string{{"Authorization", "Bearer " + secret}, {"Content-Type", "application/json"}},
		Query:   [][2]string{{"token", secret}, {"page", "2"}},
		Body:    []byte(`{"password":"` + secret + `"}`),
		HasBody: true,
	}
	res := &core.ExecResult{
		Status:   200,
		Proto:    "HTTP/1.1",
		Duration: 1500 * time.Millisecond,
		Body:     []byte(`{"echo":"` + secret + `"}`),
		BodySize: int64(len(`{"echo":"` + secret + `"}`)),
		Headers:  [][2]string{{"Content-Type", "application/json"}, {"Set-Cookie", "sid=" + secret}},
	}

	before := treeDigest(t, ps.root)
	a.recordHistory(tab, plan, red, res)
	after := treeDigest(t, ps.root)
	if !sameDigest(before, after) {
		t.Fatal("recording history must not modify any project file")
	}

	loaded, err := hs.loadFiltered(historyKey(ps), localstate.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded.Records) != 1 {
		t.Fatalf("records = %d, want 1", len(loaded.Records))
	}
	r := loaded.Records[0]
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), secret) {
		t.Fatalf("stored record leaked the secret: %s", raw)
	}
	if r.URL != "https://api.test/login?page=2&token=***" {
		t.Fatalf("effective URL/query not recorded redacted: %q", r.URL)
	}
	if r.Status != 200 || r.Outcome != localstate.OutcomeOK {
		t.Fatalf("result metadata wrong: %+v", r)
	}
	if r.Request == nil || r.Response == nil {
		t.Fatal("redacted metadata snapshots should be present by default")
	}
	if r.Request.Body != "" || r.Request.Omitted != localstate.OmitPolicy {
		t.Fatalf("request body must not be stored by default: %+v", r.Request)
	}
	if r.Response.Body != "" || r.Response.Omitted != localstate.OmitPolicy {
		t.Fatalf("response body must not be stored by default: %+v", r.Response)
	}
	if r.Request.BodySize == 0 || r.Response.BodySize == 0 {
		t.Fatalf("body sizes should be recorded even when omitted: %+v", r)
	}
	if !strings.Contains(r.Request.Headers[0][1], "Bearer ***") {
		t.Fatalf("authorization not masked: %+v", r.Request.Headers)
	}
}

func TestRecordHistoryOptInBodiesAreRedacted(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	a.mgr.cfg.setHistorySaveBodies(true)
	tab := openRequestTabs(t, ps, "Echo")[0]
	secret := historySecret()
	red := core.NewRedactor()
	red.RegisterSecret(secret)
	plan := &core.Plan{Method: "POST", URL: "https://api.test/echo", Headers: [][2]string{{"Content-Type", "text/plain"}}, Body: []byte("req " + secret), HasBody: true}
	res := &core.ExecResult{Status: 200, Proto: "HTTP/1.1", Body: []byte("res " + secret), BodySize: int64(len("res " + secret)), Headers: [][2]string{{"Content-Type", "text/plain"}}}
	a.recordHistory(tab, plan, red, res)

	loaded, _ := hs.loadFiltered(historyKey(ps), localstate.Filter{})
	if len(loaded.Records) != 1 {
		t.Fatalf("records = %d", len(loaded.Records))
	}
	r := loaded.Records[0]
	if r.Request.Body == "" || r.Response.Body == "" {
		t.Fatalf("bodies should be captured after opt-in: %+v", r)
	}
	raw, _ := json.Marshal(r)
	if strings.Contains(string(raw), secret) {
		t.Fatalf("opt-in bodies leaked the secret: %s", raw)
	}
	if !strings.Contains(r.Response.Body, "***") {
		t.Fatalf("expected redaction mask in body: %q", r.Response.Body)
	}
}

func TestRecordHistoryFailureOutcomes(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		ctxErr error
		want   string
	}{
		{"network", errors.New("dial tcp: connection refused"), nil, localstate.OutcomeError},
		{"canceled", errors.New("request canceled"), context.Canceled, localstate.OutcomeCanceled},
		{"deadline", errors.New("request timed out after 5s"), nil, localstate.OutcomeTimeout},
		{"ctx-deadline", errors.New("request canceled"), context.DeadlineExceeded, localstate.OutcomeTimeout},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a, ps, hs := newHistoryTestApp(t)
			tab := openRequestTabs(t, ps, "Failing")[0]
			secret := historySecret()
			red := core.NewRedactor()
			red.RegisterSecret(secret)
			plan := &core.Plan{Method: "GET", URL: "https://api.test/x?token=" + secret}
			a.recordHistoryFailure(tab, plan, red, tc.err, tc.ctxErr)

			loaded, err := hs.loadFiltered(historyKey(ps), localstate.Filter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded.Records) != 1 {
				t.Fatalf("records = %d, want 1", len(loaded.Records))
			}
			r := loaded.Records[0]
			if r.Outcome != tc.want {
				t.Fatalf("outcome = %q, want %q (%+v)", r.Outcome, tc.want, r)
			}
			if r.Status != 0 || r.Response != nil {
				t.Fatalf("a failed send must not fake a response: %+v", r)
			}
			raw, _ := json.Marshal(r)
			if strings.Contains(string(raw), secret) {
				t.Fatalf("failure record leaked the secret: %s", raw)
			}
			if r.Request == nil {
				t.Fatal("request metadata should still be captured for a failed send")
			}
		})
	}
}

func TestHistoryFiltersAndProjectScope(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	key := historyKey(ps)
	now := time.Now()
	records := []localstate.Record{
		{Ts: now, ProjectKey: key, ProjectName: "History", RequestID: "r1", RequestName: "Login", Method: "POST", URL: "https://api.test/login", Status: 200, Outcome: localstate.OutcomeOK},
		{Ts: now.AddDate(0, 0, -10), ProjectKey: key, ProjectName: "History", RequestID: "r2", RequestName: "Users", Method: "GET", URL: "https://api.test/users", Status: 404, Outcome: localstate.OutcomeOK},
		{Ts: now.AddDate(0, 0, -40), ProjectKey: key, ProjectName: "History", RequestID: "r3", RequestName: "Crash", Method: "GET", URL: "https://api.test/crash", Status: 500, Outcome: localstate.OutcomeOK},
		{Ts: now, ProjectKey: "other-deadbeef", ProjectName: "Other", RequestID: "r4", RequestName: "Remote", Method: "GET", URL: "https://other.test/x", Outcome: localstate.OutcomeError, Error: "dial failed"},
	}
	for _, r := range records {
		if _, err := hs.appendRecord(r.ProjectKey, r); err != nil {
			t.Fatal(err)
		}
	}

	a.historyUI = historyUIState{}
	a.reloadHistory()
	if len(a.historyRecords) != 3 {
		t.Fatalf("this-project records = %d, want 3", len(a.historyRecords))
	}

	a.historyUI.timeRange = 2 // last 7 days
	if got := len(a.visibleHistory()); got != 1 {
		t.Fatalf("7-day filter = %d, want 1", got)
	}
	a.historyUI = historyUIState{result: 2} // client error
	if got := a.visibleHistory(); len(got) != 1 || got[0].RequestID != "r2" {
		t.Fatalf("client filter = %+v", got)
	}
	a.historyUI = historyUIState{query: "users"}
	if got := a.visibleHistory(); len(got) != 1 || got[0].RequestID != "r2" {
		t.Fatalf("query filter = %+v", got)
	}
	a.historyUI = historyUIState{result: 5} // canceled
	if got := a.visibleHistory(); len(got) != 0 {
		t.Fatalf("canceled filter should be empty: %+v", got)
	}

	a.historyUI = historyUIState{allProjects: true, projectKey: "other-deadbeef"}
	a.reloadHistory()
	if len(a.historyRecords) != 4 {
		t.Fatalf("all-project records = %d, want 4", len(a.historyRecords))
	}
	if got := a.visibleHistory(); len(got) != 1 || got[0].RequestID != "r4" {
		t.Fatalf("project filter = %+v", got)
	}
	labels, keys := historyProjectOptions(a.historyRecords, "All projects")
	if len(keys) != 3 || keys[0] != "" || labels[0] != "All projects" {
		t.Fatalf("project options = %v / %v", labels, keys)
	}
	a.historyUI.query = "other"
	if got := a.visibleHistory(); len(got) != 1 || got[0].RequestID != "r4" {
		t.Fatalf("project/query filter = %+v", got)
	}
}

func TestHistoryClearOnlyRemovesCurrentProject(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	key := historyKey(ps)
	other := "other-cafebabe"
	now := time.Now()
	if _, err := hs.appendRecord(key, localstate.Record{Ts: now, Method: "GET", URL: "http://mine", Status: 200}); err != nil {
		t.Fatal(err)
	}
	if _, err := hs.appendRecord(other, localstate.Record{Ts: now, Method: "GET", URL: "http://other", Status: 200}); err != nil {
		t.Fatal(err)
	}
	a.historyUI = historyUIState{allProjects: true}
	a.reloadHistory()
	if len(a.historyRecords) != 2 {
		t.Fatalf("all records = %d", len(a.historyRecords))
	}
	a.clearHistory()
	otherRes, _ := hs.loadFiltered(other, localstate.Filter{})
	if len(otherRes.Records) != 1 {
		t.Fatal("clearing this project must not clear another project's history")
	}
	mine, _ := hs.loadFiltered(key, localstate.Filter{})
	if len(mine.Records) != 0 {
		t.Fatalf("current project history not cleared: %+v", mine.Records)
	}
}

func TestHistoryAllProjectDeleteTargetsOwningProject(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	other := "other-abc12345"
	now := time.Now()
	mine, err := hs.appendRecord(historyKey(ps), localstate.Record{Ts: now, Method: "GET", URL: "http://mine", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := hs.appendRecord(other, localstate.Record{Ts: now, ProjectName: "Other", Method: "GET", URL: "http://other", Status: 200})
	if err != nil {
		t.Fatal(err)
	}
	a.historyUI = historyUIState{allProjects: true}
	a.reloadHistory()
	if len(a.historyRecords) != 2 {
		t.Fatalf("all records = %d", len(a.historyRecords))
	}
	a.deleteHistory(theirs.Record)

	left, _ := hs.loadFiltered(historyKey(ps), localstate.Filter{})
	if len(left.Records) != 1 || left.Records[0].ID != mine.Record.ID {
		t.Fatalf("current project history changed: %+v", left.Records)
	}
	otherLeft, _ := hs.loadFiltered(other, localstate.Filter{})
	if len(otherLeft.Records) != 0 {
		t.Fatalf("other project record not deleted: %+v", otherLeft.Records)
	}
}

func TestHistoryRestoreAnotherProjectWindow(t *testing.T) {
	m, _ := newTestManager(t)
	m.history = &historyStore{dir: t.TempDir()}
	if err := m.openProject(writeProject(t, "One")); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(writeProject(t, "Two")); err != nil {
		t.Fatal(err)
	}
	one, two := m.workspaces[0], m.workspaces[1]
	entry, err := two.ps.project.NewRequest("", "Beta")
	if err != nil {
		t.Fatal(err)
	}
	one.historyView = true
	one.restoreHistory(historyRecord{
		ProjectKey: historyKey(two.ps),
		RequestID:  entry.Request.ID,
		Method:     "POST",
		URL:        "http://two.test/restored",
		Ts:         time.Now(),
	})
	tab := two.ps.activeTab()
	if tab == nil || tab.URL != "http://two.test/restored" || tab.Method != "POST" || !tab.Dirty {
		t.Fatalf("record was not restored into the owning project: %+v", tab)
	}
	if one.historyView {
		t.Fatal("restoring should leave the history view")
	}
	if one.ps.activeTab() != nil {
		t.Fatal("another project's history must not modify the focused project's tabs")
	}

	one.historyView = true
	one.restoreHistory(historyRecord{ProjectKey: "closed-ffffffff", Method: "GET", URL: "http://x"})
	if !strings.Contains(one.status, "Open that project first") {
		t.Fatalf("closed-project restore should be refused with a clear status: %q", one.status)
	}
}

func TestHistoryDiagnosticsReportCorruptLines(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	key := historyKey(ps)
	valid, _ := json.Marshal(localstate.Record{Ts: time.Now(), Method: "GET", URL: "http://ok", Status: 200})
	content := []byte("{broken\n" + string(valid) + "\n")
	if err := os.MkdirAll(hs.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hs.dir, key+".jsonl"), content, 0o600); err != nil {
		t.Fatal(err)
	}
	a.reloadHistory()
	if len(a.historyRecords) != 1 {
		t.Fatalf("valid records = %d, want 1", len(a.historyRecords))
	}
	if !strings.Contains(a.historyUI.diag, "corrupt") {
		t.Fatalf("corrupt-line diagnostic missing: %q", a.historyUI.diag)
	}
}

func TestHistoryRetentionCleanupOnOpen(t *testing.T) {
	a, ps, hs := newHistoryTestApp(t)
	a.mgr.cfg.setHistoryRetentionDays(30)
	key := historyKey(ps)
	old, _ := json.Marshal(localstate.Record{Ts: time.Now().AddDate(0, 0, -60), Method: "GET", URL: "http://old", Status: 200})
	fresh, _ := json.Marshal(localstate.Record{Ts: time.Now(), Method: "GET", URL: "http://new", Status: 200})
	if err := os.MkdirAll(hs.dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hs.dir, key+".jsonl"), append(append(old, '\n'), append(fresh, '\n')...), 0o600); err != nil {
		t.Fatal(err)
	}
	a.reloadHistory()
	if len(a.historyRecords) != 1 || a.historyRecords[0].URL != "http://new" {
		t.Fatalf("retention did not remove the expired record: %+v", a.historyRecords)
	}
	onDisk, _ := hs.loadFiltered(key, localstate.Filter{})
	if len(onDisk.Records) != 1 {
		t.Fatalf("cleanup did not compact the file: %+v", onDisk.Records)
	}
}

func TestHistoryUnavailableWithoutConfigDirectory(t *testing.T) {
	a, _ := openTestProject(t, "NoHistory")
	a.reloadHistory()
	if !strings.Contains(a.historyUI.diag, "disabled") {
		t.Fatalf("an unavailable history directory should be diagnosed: %q", a.historyUI.diag)
	}
}

func TestHistoryWriteFailureIsSurfaced(t *testing.T) {
	a, ps, _ := newHistoryTestApp(t)
	blocker := filepath.Join(t.TempDir(), "blocked")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.mgr.history = &historyStore{dir: blocker}
	tab := openRequestTabs(t, ps, "WontSave")[0]
	plan := &core.Plan{Method: "GET", URL: "http://x"}
	a.recordHistory(tab, plan, core.NewRedactor(), &core.ExecResult{Status: 200})
	if !strings.Contains(a.status, "Could not write history") {
		t.Fatalf("write failure not surfaced in status: %q", a.status)
	}
	if a.historyUI.diag == "" {
		t.Fatal("write failure should also be visible in the history panel diagnostics")
	}
}

func TestHistoryDetailShowsRedactedSnapshotOnly(t *testing.T) {
	a, ps, _ := newHistoryTestApp(t)
	a.historyView = true
	a.historyKey = historyKey(ps)
	secret := historySecret()
	a.historyRecords = []historyRecord{{
		Ts:          time.Now(),
		ID:          "detail",
		ProjectName: "History",
		RequestID:   "r1",
		RequestName: "Login",
		Method:      "POST",
		URL:         "https://api.test/login?token=***",
		Status:      200,
		Outcome:     localstate.OutcomeOK,
		Request: &localstate.Snapshot{
			Headers:  [][2]string{{"Authorization", "Bearer ***"}},
			Body:     `{"password":"***"}`,
			BodySize: 18,
			Redacted: true,
		},
	}}
	a.historyUI = historyUIState{detailOpen: true, detail: a.historyRecords[0]}
	tt := ui.NewTester(a.View, 1100, 860)
	tt.Frame()
	for _, want := range []string{"History Detail", "Redacted.", "Request snapshot", "Bearer ***"} {
		if !tt.HasText(want) {
			t.Fatalf("detail modal missing %q; texts=%v", want, tt.Texts())
		}
	}
	if tt.HasText(secret) {
		t.Fatal("detail modal must never show the raw secret")
	}
}

func TestHistoryPanelRendersFiltersAndEmptyState(t *testing.T) {
	a, ps, _ := newHistoryTestApp(t)
	a.historyView = true
	a.historyKey = historyKey(ps)
	a.historyRecords = nil
	tt := ui.NewTester(a.View, 1100, 860)
	tt.Frame()
	for _, want := range []string{"This project", "All projects", "All time", "All results"} {
		if !tt.HasText(want) {
			t.Fatalf("history filters missing %q; texts=%v", want, tt.Texts())
		}
	}
	if !tt.HasText("No history yet") {
		t.Fatalf("empty state missing; texts=%v", tt.Texts())
	}
}
