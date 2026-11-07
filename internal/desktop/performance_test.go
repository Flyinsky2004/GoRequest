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

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/diagnostics"
)

func TestPerformanceMailboxBoundedAndFinalReport(t *testing.T) {
	var pending []func()
	var got []diagnostics.Event
	var final *diagnostics.Report

	m := &performanceMailbox{
		post: func(f func()) { pending = append(pending, f) },
		deliver: func(e []diagnostics.Event, r *diagnostics.Report, _ bool) {
			got = e
			final = r
		},
	}
	for i := 0; i < 5000; i++ {
		ev := diagnostics.Event{Seq: int64(i)}
		m.publish(&ev, nil, false)
	}
	rep := &diagnostics.Report{Status: diagnostics.StatusCompleted}
	m.publish(nil, rep, true)

	if len(pending) != 1 || len(m.events) != perfEventLimit {
		t.Fatalf("callbacks=%d events=%d, want 1 and %d", len(pending), len(m.events), perfEventLimit)
	}
	pending[0]()
	if len(got) != perfEventLimit || got[0].Seq != 5000-perfEventLimit || final != rep {
		t.Fatal("bounded performanceMailbox dropped final report or misordered events")
	}
}

func TestPerformanceSnapshotProvidersSealed(t *testing.T) {
	_, ps := openTestProject(t, "PerfSnapshot")
	tabs := openRequestTabs(t, ps, "one")
	entry := tabs[0].Entry

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		b, _ := io.ReadAll(req.Body)
		if string(b) != "saved-perf-body" || req.Header.Get("X-Perf-Secret") != "perf-secret-42" {
			t.Errorf("worker read mutable file or secret: body=%q header=%q", string(b), req.Header.Get("X-Perf-Secret"))
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	entry.Request.HTTP.URL = srv.URL
	entry.Request.HTTP.Body = &core.Body{Type: "text", File: "perf-body.txt"}
	entry.Request.HTTP.Headers = []core.KV{{Name: "X-Perf-Secret", Value: "{{secret.perf_key}}"}}
	if err := ps.project.Store.WriteAtomic("perf-body.txt", []byte("saved-perf-body")); err != nil {
		t.Fatal(err)
	}

	source := core.MapSecrets{"perf_key": "perf-secret-42"}
	cfg := diagnostics.SamplingConfig{
		Iterations:         2,
		Concurrency:        1,
		ConnectionStrategy: diagnostics.StrategyKeepAlive,
	}
	eng, err := prepareDesktopPerformance(
		context.Background(),
		ps.project,
		entry.RelPath,
		"",
		cfg,
		core.ExecuteOptions{},
		source,
		nil,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}

	// Mutate both source map and on-disk file after preparation; sealed engine must ignore mutations.
	source["perf_key"] = "mutated-secret"
	_ = ps.project.Store.WriteAtomic("perf-body.txt", []byte("mutated-body"))

	rep, err := eng.Run(context.Background())
	if err != nil || rep.Status != diagnostics.StatusCompleted || rep.Summary.SuccessCount != 2 {
		t.Fatalf("unexpected report: %+v err=%v", rep, err)
	}
}

func TestPerformanceReportExplicitExportAndVisibleFailure(t *testing.T) {
	a, ps := openTestProject(t, "PerfExport")
	s := a.performanceState(true)
	s.report = &diagnostics.Report{
		RunID:          "run-exp-1",
		SnapshotDigest: "snap-exp-1",
		RequestID:      "req-1",
		RequestPath:    "requests/one.yaml",
		Method:         http.MethodGet,
		Status:         diagnostics.StatusCompleted,
		Config:         diagnostics.SamplingConfig{Iterations: 1, Concurrency: 1, Seed: 1, ConnectionStrategy: diagnostics.StrategyKeepAlive},
		ReuseSupport:   diagnostics.ReuseSupported,
	}
	s.snapshotRoot = ps.root

	// 1. Non-existent parent directory -> visible export_error notice.
	s.exportPath = filepath.Join(t.TempDir(), "missing-parent", "report.json")
	a.exportPerformance(diagnostics.ExportJSON)
	if !strings.HasPrefix(s.exportNotice, "export_error:") {
		t.Fatalf("expected export_error notice for missing parent directory, got %q", s.exportNotice)
	}

	// 2. Inside shared project root -> visible export_error notice.
	s.exportPath = filepath.Join(ps.root, "perf-report.json")
	a.exportPerformance(diagnostics.ExportJSON)
	if !strings.HasPrefix(s.exportNotice, "export_error:") {
		t.Fatalf("expected export_error notice when exporting inside project root, got %q", s.exportNotice)
	}

	// 3. Symlink into project root -> rejected.
	link := filepath.Join(t.TempDir(), "proj-symlink")
	if err := os.Symlink(ps.root, link); err != nil {
		t.Fatal(err)
	}
	s.exportPath = filepath.Join(link, "perf-report.csv")
	a.exportPerformance(diagnostics.ExportCSV)
	if !strings.HasPrefix(s.exportNotice, "export_error:") {
		t.Fatalf("expected export_error notice when exporting through symlink into project, got %q", s.exportNotice)
	}

	// 4. Valid external JSON and CSV paths -> succeed.
	outDir := t.TempDir()
	s.exportPath = filepath.Join(outDir, "perf-report.json")
	a.exportPerformance(diagnostics.ExportJSON)
	if _, err := os.Stat(s.exportPath); err != nil {
		t.Fatalf("expected exported JSON file: %v (notice=%s)", err, s.exportNotice)
	}

	s.exportPath = filepath.Join(outDir, "perf-report.csv")
	a.exportPerformance(diagnostics.ExportCSV)
	if _, err := os.Stat(s.exportPath); err != nil {
		t.Fatalf("expected exported CSV file: %v (notice=%s)", err, s.exportNotice)
	}
}

func TestPerformanceNativePanelDirtyWarningAndCompactViewport(t *testing.T) {
	a, ps := openTestProject(t, "PerfUI")
	tabs := openRequestTabs(t, ps, "one")
	tabs[0].Dirty = true

	a.OpenPerformance()
	s := a.performanceState(false)
	if s == nil || !s.open || len(s.requests) == 0 {
		t.Fatalf("expected open performanceState with saved requests, got %+v", s)
	}
	s.report = &diagnostics.Report{
		RunID:          "run-ui-1",
		RequestPath:    s.requestRef,
		Method:         http.MethodGet,
		TargetURL:      "http://127.0.0.1:8080/items",
		Status:         diagnostics.StatusCompleted,
		ReuseSupport:   diagnostics.ReuseSupported,
		Summary:        diagnostics.SampleSummary{MeasuredTotal: 5, SuccessCount: 5, SuccessRate: 1.0},
		SuccessLatency: diagnostics.ComputeDistribution([]float64{100, 200, 300, 400, 500}),
	}

	tt := ui.NewTester(func(c *ui.Context) { a.PerformanceModals(c, c.Theme()) }, 1000, 700)
	tt.Frame()
	if !tt.HasText(a.perfMsg().DirtyWarn) {
		t.Fatal("dirty snapshot warning is not visible in PerformanceModals")
	}
	for _, label := range []string{a.perfMsg().Start, a.perfMsg().ExportJSON, a.perfMsg().ExportCSV} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 700 {
			t.Fatalf("control outside compact 1000x700 viewport: %s %+v", label, r)
		}
	}
}

func TestPerformanceWindowIsolationAndBoundsRejection(t *testing.T) {
	a1, _ := openTestProject(t, "PerfWin1")
	a2, _ := openTestProject(t, "PerfWin2")

	a1.OpenPerformance()
	a2.OpenPerformance()
	s1 := a1.performanceState(false)
	s2 := a2.performanceState(false)
	if s1 == nil || s2 == nil || s1 == s2 {
		t.Fatal("expected distinct per-window performanceState instances")
	}

	// Reject excessive concurrency without running workers.
	s1.concurrencyText = "999"
	a1.StartPerformance()
	if s1.running || s1.report == nil || s1.report.Status != diagnostics.StatusConfigError {
		t.Fatalf("expected StatusConfigError for concurrency=999, got %+v", s1.report)
	}
	if s2.report != nil {
		t.Fatal("window 1 state leaked into window 2")
	}
}
