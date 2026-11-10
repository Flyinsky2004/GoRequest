package desktop

import (
	"fmt"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/runner"
)

func TestRunnerFreezeConcurrencyValidation(t *testing.T) {
	s := &runnerState{stepConcurrencyText: "2", rowConcurrencyText: "3"}
	if err := s.freezeConcurrency(); err != nil || s.stepConcurrency != 2 || s.rowConcurrency != 3 {
		t.Fatalf("%v %d/%d", err, s.stepConcurrency, s.rowConcurrency)
	}
	s = &runnerState{stepConcurrencyText: "0", rowConcurrencyText: "0"}
	if err := s.freezeConcurrency(); err != nil || s.stepConcurrency != 1 || s.rowConcurrency != 1 {
		t.Fatalf("zero defaults: %v %d/%d", err, s.stepConcurrency, s.rowConcurrency)
	}
	for _, tc := range []struct{ step, row string }{
		{"abc", "1"}, {"1", "abc"}, {"-1", "1"}, {"1", "-1"},
		{fmt.Sprint(runner.MaxStepConcurrency + 1), "1"}, {"1", fmt.Sprint(runner.MaxRowConcurrency + 1)},
	} {
		s = &runnerState{stepConcurrencyText: tc.step, rowConcurrencyText: tc.row}
		if err := s.freezeConcurrency(); err == nil {
			t.Fatalf("accepted %q/%q", tc.step, tc.row)
		}
	}
}

func TestRunnerInvalidConcurrencyBlocksStart(t *testing.T) {
	a, _ := openTestProject(t, "Bad concurrency")
	a.OpenRunner()
	s := a.runnerState(false)
	s.stepConcurrencyText = "999"
	a.StartRunner()
	if s.running || s.notice == "" || !strings.Contains(strings.ToLower(s.notice), "concurrency") {
		t.Fatalf("invalid input did not block start: running=%v notice=%q", s.running, s.notice)
	}
	if s.result != nil {
		t.Fatal("invalid input created a fake run result")
	}
}

func longRunnerResult(steps int) *runner.RunResult {
	result := &runner.RunResult{Status: runner.StatusFailed, Rows: []runner.RowResult{{Index: 1, Status: runner.StatusFailed}}}
	for i := 0; i < steps; i++ {
		status := runner.StatusPassed
		if i == steps-1 {
			status = runner.StatusFailed
		}
		result.Rows[0].Steps = append(result.Rows[0].Steps, runner.StepResult{
			ID: fmt.Sprintf("step-%03d", i), Request: fmt.Sprintf("collections/r%03d.request.yaml", i),
			Status: status, DurationMs: 1, Attempts: []runner.AttemptResult{{Number: 1, Status: status, HTTPStatus: 200}},
			Failure: func() *runner.Failure {
				if status == runner.StatusFailed {
					return &runner.Failure{Category: runner.FailureRun, Kind: runner.KindAssertion, Message: "assertion failed"}
				}
				return nil
			}(),
		})
	}
	return result
}

func TestRunnerResultPagingFilterAndFailureEntry(t *testing.T) {
	a, _ := openTestProject(t, "Paged results")
	a.OpenRunner()
	s := a.runnerState(false)
	s.result = longRunnerResult(120)
	s.resultOffset = 0
	tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 1100, 1400)
	tt.Frame()
	if !tt.HasText("1–50 of 120 step results") {
		t.Fatal("first page summary missing")
	}
	if tt.HasText("step-060") {
		t.Fatal("page rendered more than the requested window")
	}
	if !tt.HasText(a.runnerMsg().NextPage) {
		t.Fatal("next-page action missing")
	}
	if err := tt.Click(a.runnerMsg().NextPage); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.resultOffset != s.resultLimit || !tt.HasText("51–100 of 120 step results") {
		t.Fatalf("second page offset=%d", s.resultOffset)
	}
	// Failures are reachable directly without walking every page.
	if err := tt.Click(a.runnerMsg().ShowFailures); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if s.resultFilter != 1 || !tt.HasText("1–1 of 1 step results") || !tt.HasText("step-119") {
		t.Fatalf("failure filter offset=%d filter=%d", s.resultOffset, s.resultFilter)
	}
	// Text search narrows by step id.
	s.resultFilter = 0
	s.resultFilterText = "step-119"
	s.resultOffset = 0
	tt.Frame()
	if !tt.HasText("1–1 of 1 step results") || !tt.HasText("step-119") {
		t.Fatalf("text filter did not narrow results: offset=%d", s.resultOffset)
	}
}

func TestRunnerPanelNarrowAndConcurrencyInputsVisible(t *testing.T) {
	a, _ := openTestProject(t, "Narrow runner")
	a.OpenRunner()
	s := a.runnerState(false)
	s.result = longRunnerResult(3)
	tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 560, 700)
	tt.Frame()
	for _, label := range []string{a.runnerMsg().StepConcurrency, a.runnerMsg().RowConcurrency, a.runnerMsg().JSON, a.runnerMsg().JUnit} {
		r, ok := tt.Find(label)
		if !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 700 {
			t.Fatalf("control outside narrow window: %s %+v", label, r)
		}
	}
}

func TestRunnerResultViewRendersInLightAndDarkThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprintf("dark=%t", dark), func(t *testing.T) {
			a, _ := openTestProject(t, "Themed runner")
			a.OpenRunner()
			s := a.runnerState(false)
			s.result = longRunnerResult(40)
			tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 1100, 900)
			tt.SetDark(dark)
			tt.Frame()
			// Result rows live in a fixed-height inner scroll, so only the page
			// chrome is guaranteed to be inside the modal viewport. Rows are
			// asserted by content instead of by exact label geometry.
			for _, label := range []string{a.runnerMsg().ResultFilter, a.runnerMsg().NextPage, "1–40 of 40 step results"} {
				r, ok := tt.Find(label)
				if !ok || r.W <= 0 || r.H <= 0 || r.Y < 0 || r.Y+r.H > 900 {
					t.Fatalf("dark=%v control missing/out of window: %s %+v", dark, label, r)
				}
			}
			if !tt.HasText("step-000") || !tt.HasText("row 1 · step-000") {
				t.Fatalf("dark=%v first page row not rendered", dark)
			}
		})
	}
}
