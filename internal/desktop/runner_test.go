package desktop

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/core"
	"gorequest/internal/runner"
)

func TestRunnerMailboxBoundedAndFinal(t *testing.T) {
	var pending []func()
	var got []runner.Event
	var final *runner.RunResult
	m := &runnerMailbox{post: func(f func()) { pending = append(pending, f) }, deliver: func(e []runner.Event, r *runner.RunResult, _ bool) { got = e; final = r }}
	for i := 0; i < 10000; i++ {
		e := runner.Event{Seq: int64(i)}
		m.publish(&e, nil, false)
	}
	result := &runner.RunResult{Status: runner.StatusPassed}
	m.publish(nil, result, true)
	if len(pending) != 1 || len(m.events) != runnerEventLimit {
		t.Fatalf("callbacks=%d events=%d", len(pending), len(m.events))
	}
	pending[0]()
	if len(got) != runnerEventLimit || got[0].Seq != 10000-runnerEventLimit || final != result {
		t.Fatal("bounded queue lost final result")
	}
}
func TestRunnerSnapshotProvidersSealed(t *testing.T) {
	_, ps := openTestProject(t, "Snapshot")
	tabs := openRequestTabs(t, ps, "one")
	entry := tabs[0].Entry
	// Use fixed project file body and a secret credential, then mutate both.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		if string(b) != "saved bytes" || req.Header.Get("X-Secret") != "snapshot-secret-71" {
			t.Error("run read mutable file or secret provider")
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()
	entry.Request.HTTP.URL = server.URL
	entry.Request.HTTP.Body = &core.Body{Type: "text", File: "payload.txt"}
	entry.Request.HTTP.Headers = []core.KV{{Name: "X-Secret", Value: "{{secret.key}}"}}
	if err := ps.project.Store.WriteAtomic("payload.txt", []byte("saved bytes")); err != nil {
		t.Fatal(err)
	}
	source := core.MapSecrets{"key": "snapshot-secret-71"}
	suite := &runner.Suite{SchemaVersion: 1, ID: "fixed", Steps: []runner.SuiteStep{{ID: "one", Request: entry.Request.ID}}}
	r, err := prepareDesktopRunner(context.Background(), ps.project, suite, "", core.ExecuteOptions{}, source, nil, desktopRunnerOptions{})
	if err != nil {
		t.Fatal(err)
	}
	source["key"] = "changed"
	_ = ps.project.Store.WriteAtomic("payload.txt", []byte("changed"))
	result, err := r.Run(context.Background())
	if err != nil || result.Status != runner.StatusPassed {
		t.Fatalf("%+v %v", result, err)
	}
	files := &runnerFiles{source: core.NewDirFileOpener(ps.project.Dir), files: map[string]runnerFile{}}
	f, err := files.OpenProjectFile(context.Background(), "payload.txt")
	if err != nil {
		t.Fatal(err)
	}
	f.Reader.Close()
	files.source = nil
	_ = ps.project.Store.WriteAtomic("payload.txt", []byte("later"))
	f, err = files.OpenProjectFile(context.Background(), "payload.txt")
	if err != nil || f.Size != 7 {
		t.Fatalf("frozen file %+v %v", f, err)
	}
	f.Reader.Close()
	if _, err = files.OpenProjectFile(context.Background(), "new.txt"); err == nil {
		t.Fatal("sealed opener read a new file")
	}
}
func TestRunnerReportExplicitPathAndVisibleFailure(t *testing.T) {
	a, ps := openTestProject(t, "Reports")
	s := a.runnerState(true)
	s.result = &runner.RunResult{Status: runner.StatusPassed}
	s.snapshotRoot = ps.root
	s.exportPath = filepath.Join(t.TempDir(), "missing", "report.json")
	a.exportRunner(false)
	if !strings.Contains(s.exportNotice, "internal_error / report") {
		t.Fatal(s.exportNotice)
	}
	s.exportPath = filepath.Join(t.TempDir(), "report.json")
	a.exportRunner(false)
	if _, err := os.Stat(s.exportPath); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerReport(s.result, filepath.Join(ps.root, "report.json"), ps.root, false); err == nil {
		t.Fatal("wrote into project")
	}
	link := filepath.Join(t.TempDir(), "project-link")
	if err := os.Symlink(ps.root, link); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerReport(s.result, filepath.Join(link, "report.xml"), ps.root, true); err == nil {
		t.Fatal("symlink escaped report policy")
	}
}
func TestRunnerNativePanelAndDirtySnapshot(t *testing.T) {
	a, ps := openTestProject(t, "Runner UI")
	tabs := openRequestTabs(t, ps, "one")
	tabs[0].Dirty = true
	a.OpenRunner()
	s := a.runnerState(false)
	if s == nil || !s.open || len(s.collections) == 0 {
		t.Fatalf("%+v", s)
	}
	tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 1280, 1000)
	tt.Frame()
	if !tt.HasText(a.runnerMsg().Dirty) {
		t.Fatal("dirty snapshot warning is not visible")
	}
	if !tt.HasText(a.runnerMsg().Start) {
		t.Fatal("run action missing")
	}
}
func drainRunner(t *testing.T, w *queuedTestWindow, s *runnerState) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for s.running && time.Now().Before(deadline) {
		w.drainUI()
		time.Sleep(time.Millisecond)
	}
	w.drainUI()
	if s.running {
		t.Fatal("runner did not complete")
	}
}

func TestRunnerEmptySavedSelectionIsVisibleConfigError(t *testing.T) {
	a, _ := openTestProject(t, "Empty runner")
	a.OpenRunner()
	a.StartRunner()
	s := a.runnerState(false)
	if s.running || s.result == nil || s.result.Status != runner.StatusConfigError || s.notice == "" {
		t.Fatalf("%+v", s)
	}
}
func TestRunnerExportRejectsOtherProjectMetadata(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(t.TempDir(), ".gorequest")
	if err := os.Mkdir(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeRunnerReport(&runner.RunResult{}, filepath.Join(other, "report.json"), root, false); err == nil {
		t.Fatal("report wrote into other project metadata")
	}
}

func TestRunnerPanelExportReachableAtCompactHeight(t *testing.T) {
	a, _ := openTestProject(t, "Compact runner")
	a.OpenRunner()
	s := a.runnerState(false)
	s.result = &runner.RunResult{Status: runner.StatusPassed, Rows: []runner.RowResult{{Index: 1, Steps: []runner.StepResult{{ID: "sample", Status: runner.StatusPassed, Attempts: []runner.AttemptResult{{Number: 1, Status: runner.StatusPassed}}}}}}}
	tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 1000, 700)
	tt.Frame()
	for _, label := range []string{a.runnerMsg().Start, a.runnerMsg().JSON, a.runnerMsg().JUnit} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 700 {
			t.Fatalf("action outside compact window: %s %+v", label, r)
		}
	}
}
