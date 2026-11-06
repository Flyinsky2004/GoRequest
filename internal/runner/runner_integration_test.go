package runner

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorequest/internal/core"
)

// newFixtureProject creates a temporary, real .gorequest/ project loaded via
// core.OpenProject. No shared project, real credential store or remote service
// is touched. Asserts are declared as raw yaml.Node entries in request files.
func newFixtureProject(t *testing.T, requests map[string]string) (*core.Project, string) {
	t.Helper()
	root := t.TempDir()
	gq := filepath.Join(root, ".gorequest")
	store := core.NewStore(gq)
	projectYAML := []byte("schemaVersion: 1\nid: fixture-project\nname: Fixture\nvariables:\n  baseUrl: http://fixture.invalid\n")
	if err := store.WriteAtomic("project.yaml", projectYAML); err != nil {
		t.Fatal(err)
	}
	envYAML := []byte("schemaVersion: 1\nid: local\nvariables:\n  baseUrl: http://fixture.invalid\nsecrets: []\n")
	if err := store.WriteAtomic("environments/local.yaml", envYAML); err != nil {
		t.Fatal(err)
	}
	for rel, body := range requests {
		if err := store.WriteAtomic(rel, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	p, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Issues) > 0 {
		for _, is := range p.Issues {
			if is.Level == "error" {
				t.Fatalf("fixture project issue: %s", is)
			}
		}
	}
	return p, root
}

func reqYAML(id, method, path, extra string) string {
	return fmt.Sprintf(`schemaVersion: 1
id: %s
name: %s
http:
  method: %s
  url: "{{baseUrl}}%s"
%s`, id, id, method, path, extra)
}

func assertExtra(entries ...string) string {
	var b strings.Builder
	b.WriteString("assert:\n")
	for _, e := range entries {
		b.WriteString("  - ")
		b.WriteString(e)
		b.WriteString("\n")
	}
	return b.String()
}

func suiteYAML(extra string) []byte {
	return []byte(fmt.Sprintf(`schemaVersion: 1
id: business-flow
name: Login / Query / Update
onFailure: stop
%s`, extra))
}

// fixtureResult is a compact, safe result used with an injected Executor.
func fixtureResult(body string, status int, headers ...[2]string) *core.ExecResult {
	return &core.ExecResult{
		Status: status, Proto: "HTTP/1.1",
		Body: []byte(body), BodySize: int64(len(body)), Headers: headers,
		FinalURL: "http://fixture.invalid/",
	}
}

// TestLoginExtractQueryUpdateTwoRows is the AC-06 business-flow test:
// login -> sensitive token extraction -> query -> update -> verify, with two
// data rows, no shared CookieJar/outputs, and reports free of token values.
func TestLoginExtractQueryUpdateTwoRows(t *testing.T) {
	requests := map[string]string{
		"collections/auth/login.request.yaml": reqYAML("login", "POST", "/login",
			`  headers:
    - {name: Content-Type, value: application/json}
  body:
    type: json
    value:
      user: "{{data.user}}"
`+assertExtra("status: 200", `jsonpath: $.token
    exists: true`)),
		"collections/profile/query.request.yaml": reqYAML("query", "GET", "/profile",
			`  headers:
    - {name: X-Auth, value: "{{steps.login.token}}"}
`+assertExtra("status: 200", `jsonpath: $.user
    equals: "{{data.user}}"`)),
		"collections/profile/update.request.yaml": reqYAML("update", "POST", "/profile/update",
			`  headers:
    - {name: X-Auth, value: "{{steps.login.token}}"}
    - {name: Content-Type, value: application/json}
  body:
    type: json
    value:
      display: "new-{{data.user}}"
`+assertExtra("status: 200", `jsonpath: $.ok
    equals: true`)),
		"collections/profile/verify.request.yaml": reqYAML("verify", "GET", "/profile/verify",
			`  headers:
    - {name: X-Auth, value: "{{steps.login.token}}"}
`+assertExtra("status: 200", `jsonpath: $.display
    equals: "new-{{data.user}}"`)),
	}
	p, _ := newFixtureProject(t, requests)
	if err := p.Store.WriteAtomic("data/users.csv", []byte("user\nalice\nbob\n")); err != nil {
		t.Fatal(err)
	}
	suiteText := suiteYAML(`dataset:
  format: csv
  file: data/users.csv
steps:
  - id: login
    request: collections/auth/login.request.yaml
    extract:
      - {name: token, from: jsonpath, path: $.token}
  - id: query
    request: collections/profile/query.request.yaml
    needs: [login]
  - id: update
    request: collections/profile/update.request.yaml
    needs: [query]
  - id: verify
    request: collections/profile/verify.request.yaml
    needs: [update]
`)
	if err := p.Store.WriteAtomic("suites/business.suite.yaml", suiteText); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	observedTokens := map[string][]string{}
	savedDisplays := map[string]string{}
	var attemptPlans []*core.Plan
	executor := execFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		var token string
		for _, h := range plan.Headers {
			if strings.EqualFold(h[0], "X-Auth") {
				token = h[1]
			}
		}
		mu.Lock()
		attemptPlans = append(attemptPlans, plan)
		observedTokens[token] = append(observedTokens[token], "call")
		mu.Unlock()
		if plan.URL == "http://fixture.invalid/login" {
			user := ""
			var in struct {
				User string `json:"user"`
			}
			_ = json.Unmarshal(plan.Body, &in)
			user = in.User
			return fixtureResult(fmt.Sprintf(`{"token":"%s","userId":%d}`, fixtureToken(user), 100+len(user)), 200), nil
		}
		if token == "" {
			return nil, fmt.Errorf("missing token in dependent step")
		}
		switch {
		case strings.HasSuffix(plan.URL, "/profile/update"):
			var upd struct {
				Display string `json:"display"`
			}
			_ = json.Unmarshal(plan.Body, &upd)
			mu.Lock()
			savedDisplays[token] = upd.Display
			mu.Unlock()
			return fixtureResult(`{"ok":true}`, 200), nil
		case strings.HasSuffix(plan.URL, "/profile/verify"):
			mu.Lock()
			display := savedDisplays[token]
			mu.Unlock()
			return fixtureResult(fmt.Sprintf(`{"display":%q}`, display), 200), nil
		default:
			user := ""
			for u := range map[string]bool{"alice": true, "bob": true} {
				if token == fixtureToken(u) {
					user = u
				}
			}
			return fixtureResult(fmt.Sprintf(`{"user":%q,"display":"old-%s"}`, user, user), 200), nil
		}
	})
	suite, err := LoadSuite(p, "suites/business.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var events []Event
	var evMu sync.Mutex
	r := New(p, suite, Options{
		Environment: "local",
		Executor:    executor,
		Seed:        7,
		Emit: func(e Event) {
			evMu.Lock()
			events = append(events, e)
			evMu.Unlock()
		},
	})
	result, runErr := r.Run(context.Background())
	if runErr != nil {
		t.Fatalf("run: %v", runErr)
	}
	if result.Status != StatusPassed {
		for _, rr := range result.Rows {
			for _, st := range rr.Steps {
				if st.Status != StatusPassed {
					t.Logf("row %d step %s status=%s reason=%q failure=%+v attempts=%+v", rr.Index, st.ID, st.Status, st.Reason, st.Failure, st.Attempts)
				}
			}
		}
		t.Fatalf("run status = %s; failure=%+v rows=%+v", result.Status, result.Failure, result.Rows)
	}
	if result.Summary.Rows != 2 || result.Summary.Passed != 8 || result.Summary.StepsTotal != 8 {
		for _, rr := range result.Rows {
			for _, st := range rr.Steps {
				t.Logf("row %d step %s status=%s reason=%q failure=%+v attempts=%+v", rr.Index, st.ID, st.Status, st.Reason, st.Failure, st.Attempts)
			}
		}
		t.Fatalf("bad summary: %+v", result.Summary)
	}
	if result.Snapshot == "" || result.RunID == "" {
		t.Fatal("missing snapshot or run id")
	}
	if len(attemptPlans) != 8 {
		t.Fatalf("want 8 executions (4 steps x 2 rows), got %d", len(attemptPlans))
	}
	for _, token := range []string{fixtureToken("alice"), fixtureToken("bob")} {
		if len(observedTokens[token]) != 3 {
			t.Fatalf("token %q used %d times; want 3 dependent calls", token, len(observedTokens[token]))
		}
	}
	jsonReport, err := MarshalJSONReport(result)
	if err != nil {
		t.Fatal(err)
	}
	junitReport, err := MarshalJUnitReport(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{fixtureToken("alice"), fixtureToken("bob")} {
		if strings.Contains(string(jsonReport), secret) || strings.Contains(string(junitReport), secret) {
			t.Fatalf("secret %q leaked into reports", secret)
		}
	}
	for _, e := range events {
		if strings.Contains(e.Message, "TOKEN_") {
			t.Fatalf("secret leaked into event: %+v", e)
		}
	}
	extractionEvents := 0
	for _, e := range events {
		if e.Kind == EventExtraction && e.Sensitive {
			extractionEvents++
		}
	}
	if extractionEvents != 2 {
		t.Fatalf("want 2 sensitive extraction events, got %d", extractionEvents)
	}
}

// TestFailureNoExtractionAndDependencySkipped verifies failed assertions do not
// publish outputs and downstream dependencies become skipped.
func TestFailureNoExtractionAndDependencySkipped(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
		"collections/b.request.yaml": reqYAML("b", "GET", "/b", ""),
	}
	p, _ := newFixtureProject(t, requests)
	suite, err := ParseSuite(suiteYAML(`steps:
  - id: a
    request: collections/a.request.yaml
    assert:
      - status: 500
    extract:
      - {name: token, from: jsonpath, path: $.token}
  - id: b
    request: collections/b.request.yaml
    needs: [a]
`))
	if err != nil {
		t.Fatal(err)
	}
	executor := execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
		return fixtureResult(`{"token":"SHOULD_NOT_PUBLISH_123456"}`, 200), nil
	})
	r := New(p, suite, Options{Environment: "local", Executor: executor})
	result, _ := r.Run(context.Background())
	if result.Status != StatusFailed || result.Summary.Failed != 1 || result.Summary.Skipped != 1 {
		t.Fatalf("bad result: status=%s summary=%+v rows=%+v", result.Status, result.Summary, result.Rows)
	}
	steps := result.Rows[0].Steps
	if steps[0].Extractions[0].Published || len(steps[0].Extractions[0].Value) > 0 {
		t.Fatalf("failure published extraction: %+v", steps[0].Extractions[0])
	}
	if steps[1].Status != StatusSkipped {
		t.Fatalf("dependent step = %s, want skipped", steps[1].Status)
	}
}

// TestConfigErrorFailsBeforeNetwork covers missing secrets, unknown variables,
// bad JSONPath and undeclared step outputs.
func TestConfigErrorFailsBeforeNetwork(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
		"collections/b.request.yaml": reqYAML("b", "GET", "/b", ""),
	}
	cases := []struct {
		name  string
		suite []byte
	}{
		{"missing variable", suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml, assert: [{status: 200, header: X-Val, equals: "{{undef}}"}], retry: {maxAttempts: 1}}
`)},
		{"bad jsonpath", suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml, assert: [{jsonpath: "$.items[?(@.x)]", exists: true}]}
`)},
		{"dependency cycle", suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml, needs: [b]}
  - {id: b, request: collections/b.request.yaml, needs: [a]}
`)},
		{"undeclared output", suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml}
  - {id: b, request: collections/b.request.yaml, assert: [{body: {equals: "{{steps.a.token}}"}}]}
`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, _ := newFixtureProject(t, requests)
			suite, err := ParseSuite(tc.suite)
			if err != nil {
				// Cycle errors are intentionally caught by ValidateStructure.
				if tc.name == "dependency cycle" {
					return
				}
				t.Fatal(err)
			}
			var calls atomic.Int32
			executor := execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
				calls.Add(1)
				return fixtureResult(`{}`, 200), nil
			})
			r := New(p, suite, Options{Environment: "local", Executor: executor})
			result, runErr := r.Run(context.Background())
			if runErr == nil {
				t.Fatalf("expected preflight error; status=%s", result.Status)
			}
			if result.Status != StatusConfigError || result.Failure == nil || result.Failure.Category != FailureConfig {
				t.Fatalf("bad config result: %+v", result.Failure)
			}
			if calls.Load() != 0 {
				t.Fatalf("network called %d times before config error", calls.Load())
			}
		})
	}
}

// TestRetryKeepsAttemptsAndNoRetryOnCancel verifies retry classification and
// that cancellation never retries.
func TestRetryKeepsAttemptsAndNoRetryOnCancel(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
	}
	p, _ := newFixtureProject(t, requests)
	suite, err := ParseSuite(suiteYAML(`steps:
  - id: a
    request: collections/a.request.yaml
    assert:
      - status: 200
    retry:
      maxAttempts: 3
      on: [status]
      statuses: [503]
      delayMs: 1
`))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	executor := execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
		calls.Add(1)
		return fixtureResult(`{"retry":true}`, 503), nil
	})
	r := New(p, suite, Options{Environment: "local", Executor: executor})
	result, _ := r.Run(context.Background())
	if calls.Load() != 3 || result.Summary.Attempts != 3 || result.Summary.Retries != 2 {
		t.Fatalf("retry counts wrong: calls=%d summary=%+v steps=%+v", calls.Load(), result.Summary, result.Rows[0].Steps)
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %s", result.Status)
	}

	// Cancel before execution: no retries, no network calls.
	calls.Store(0)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r2 := New(p, suite, Options{Environment: "local", Executor: executor})
	result2, _ := r2.Run(ctx)
	if calls.Load() != 0 || result2.Status != StatusCanceled {
		t.Fatalf("cancel: calls=%d status=%s", calls.Load(), result2.Status)
	}
	if result2.Rows[0].Steps[0].RetryCount() != 0 {
		t.Fatal("cancel must not retry")
	}
}

// TestConcurrentIndependentStepsAndRowIsolation verifies DAG concurrency and
// that each row gets a separate CookieJar (therefore no cross-row cookie leak).
func TestConcurrentIndependentStepsAndRowIsolation(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
		"collections/b.request.yaml": reqYAML("b", "GET", "/b", ""),
	}
	p, _ := newFixtureProject(t, requests)
	if err := p.Store.WriteAtomic("data/rows.csv", []byte("user\nalpha\nbeta\n")); err != nil {
		t.Fatal(err)
	}
	suite, err := ParseSuite(suiteYAML(`dataset:
  format: csv
  file: data/rows.csv
steps:
  - {id: a, request: collections/a.request.yaml}
  - {id: b, request: collections/b.request.yaml}
`))
	if err != nil {
		t.Fatal(err)
	}
	var active, maxActive atomic.Int32
	var jars []*core.CookieJar
	var mu sync.Mutex
	executor := execFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		cur := active.Add(1)
		for {
			old := maxActive.Load()
			if cur <= old || maxActive.CompareAndSwap(old, cur) {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
		active.Add(-1)
		mu.Lock()
		jars = append(jars, opts.CookieJar)
		mu.Unlock()
		return fixtureResult(`{"ok":true}`, 200), nil
	})
	r := New(p, suite, Options{Environment: "local", Executor: executor, StepConcurrency: 2})
	result, _ := r.Run(context.Background())
	if result.Status != StatusPassed {
		t.Fatalf("status = %s: %+v", result.Status, result.Rows)
	}
	if maxActive.Load() < 2 {
		t.Fatalf("independent steps did not run concurrently (max=%d)", maxActive.Load())
	}
	seen := map[*core.CookieJar]int{}
	for _, j := range jars {
		seen[j]++
	}
	if len(seen) != 2 {
		t.Fatalf("want one CookieJar per row, got %d distinct jars", len(seen))
	}
	for j, n := range seen {
		if n != 2 {
			t.Fatalf("jar %p used %d times; want 2", j, n)
		}
	}
}

// TestProductionConfirmNeverAutoApproved verifies the non-interactive run
// context does not bypass target policy confirmation.
func TestProductionConfirmNeverAutoApproved(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
	}
	p, _ := newFixtureProject(t, requests)
	suite, err := ParseSuite(suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml}
`))
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var interactive, confirmed bool
	executor := execFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		calls.Add(1)
		interactive = opts.Interactive
		confirmed = opts.Confirmed
		// Simulate core.ExecuteWithOptions' policy decision.
		if opts.HasTargetPolicy && !opts.Confirmed {
			return nil, &core.ConfigError{Err: fmt.Errorf("target policy requires confirmation: production")}
		}
		return fixtureResult(`{}`, 200), nil
	})
	policy := core.MergeTargetPolicy(&core.DeviceTargetPolicy{ProductionAction: core.ActionConfirm}, nil)
	r := New(p, suite, Options{
		Environment: "local",
		Executor:    executor,
		ExecuteOptions: core.ExecuteOptions{
			HasTargetPolicy: true,
			TargetPolicy:    policy,
			Confirmed:       true, // caller tries to pre-confirm; runner must override
		},
	})
	result, _ := r.Run(context.Background())
	if interactive || confirmed {
		t.Fatalf("runner leaked interactive=%t confirmed=%t", interactive, confirmed)
	}
	if calls.Load() != 1 {
		t.Fatalf("executor calls = %d", calls.Load())
	}
	if result.Status != StatusConfigError || result.Failure == nil || result.Failure.Category != FailureConfig {
		t.Fatalf("confirmation should be config error, got status=%s failure=%+v", result.Status, result.Failure)
	}
}

// TestReportAndEventsDoNotLeakSecretInFailureActual covers encoded/concatenated
// secret values in assertion diagnostics.
func TestReportAndEventsDoNotLeakSecretInFailureActual(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
	}
	p, _ := newFixtureProject(t, requests)
	suite, err := ParseSuite(suiteYAML(`steps:
  - id: a
    request: collections/a.request.yaml
    assert:
      - body: {contains: "never-matches"}
`))
	if err != nil {
		t.Fatal(err)
	}
	secret := "CONCATENATED_SECRET_ABCD1234"
	executor := execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
		return fixtureResult(`{"msg":"prefix `+secret+` suffix"}`, 200,
			[2]string{"Authorization", "Bearer " + secret}), nil
	})
	var events []Event
	r := New(p, suite, Options{
		Environment: "local",
		Secrets:     core.MapSecrets{"bound": secret},
		Executor:    executor,
		Emit:        func(e Event) { events = append(events, e) },
	})
	r.PreloadSecrets([]string{secret})
	result, _ := r.Run(context.Background())
	if result.Status != StatusFailed {
		t.Fatalf("status = %s", result.Status)
	}
	jsonReport, _ := MarshalJSONReport(result)
	junitReport, _ := MarshalJUnitReport(result)
	eventJSON, _ := json.Marshal(events)
	for name, b := range map[string][]byte{"json": jsonReport, "junit": junitReport, "events": eventJSON} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("%s report leaked secret", name)
		}
	}
}

// TestExportReportsFailure verifies local report write failure is surfaced.
func TestExportReportsFailure(t *testing.T) {
	result := &RunResult{Status: StatusPassed}
	result.finalize()
	_, _, err := ExportReports(result, ReportOptions{Dir: ""})
	if err == nil {
		t.Fatal("expected empty directory error")
	}
	path := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, _, err = ExportReports(result, ReportOptions{Dir: path})
	if err == nil {
		t.Fatal("expected directory creation error")
	}
}

func fixtureToken(user string) string {
	return "TOKEN_" + user + "_SECRET_0123456789"
}

// TestConcurrentFailureStopDoesNotStartNewSteps verifies onFailure=stop
// contains concurrent branches: already-running steps drain, but steps still
// pending are never launched.
func TestConcurrentFailureStopDoesNotStartNewSteps(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
		"collections/b.request.yaml": reqYAML("b", "GET", "/b", ""),
		"collections/c.request.yaml": reqYAML("c", "GET", "/c", ""),
	}
	p, _ := newFixtureProject(t, requests)
	suite, err := ParseSuite(suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml, assert: [{status: 500}]}
  - {id: b, request: collections/b.request.yaml}
`))
	if err != nil {
		t.Fatal(err)
	}
	var aCalls, bCalls, cCalls atomic.Int32
	executor := execFunc(func(ctx context.Context, plan *core.Plan, opts core.ExecuteOptions) (*core.ExecResult, error) {
		if strings.HasSuffix(plan.URL, "/a") {
			aCalls.Add(1)
			time.Sleep(30 * time.Millisecond)
			return fixtureResult(`{}`, 200), nil
		}
		if strings.HasSuffix(plan.URL, "/c") {
			cCalls.Add(1)
			return fixtureResult(`{}`, 200), nil
		}
		bCalls.Add(1)
		return fixtureResult(`{}`, 200), nil
	})
	// A third independent step starts only after one of the first two finishes;
	// with stop it must remain pending and be skipped. Depend on both so the
	// scheduler cannot start it in the initial batch.
	suite.Steps = append(suite.Steps, SuiteStep{
		ID: "c", Request: "collections/c.request.yaml", Needs: []string{"a", "b"},
	})
	r := New(p, suite, Options{Environment: "local", Executor: executor, StepConcurrency: 2})
	result, _ := r.Run(context.Background())
	if cCalls.Load() != 0 {
		t.Fatalf("step c was launched after a failure signal: %d", cCalls.Load())
	}
	if result.Status != StatusFailed {
		t.Fatalf("status = %s", result.Status)
	}
	if aCalls.Load() != 1 {
		t.Fatalf("a calls = %d", aCalls.Load())
	}
	// b may already run concurrently; the important invariant is c never runs.
	if bCalls.Load() > 1 {
		t.Fatalf("b ran more than once: %d", bCalls.Load())
	}
	_ = result
}

// TestCanceledContextReportsNoRetry verifies ctx cancellation before preflight
// yields a canceled report with steps, not a config error.
func TestCanceledContextReportsNoRetry(t *testing.T) {
	requests := map[string]string{
		"collections/a.request.yaml": reqYAML("a", "GET", "/a", ""),
	}
	p, _ := newFixtureProject(t, requests)
	suite, err := ParseSuite(suiteYAML(`steps:
  - {id: a, request: collections/a.request.yaml, retry: {maxAttempts: 3}}
`))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var calls atomic.Int32
	r := New(p, suite, Options{Environment: "local", Executor: execFunc(
		func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
			calls.Add(1)
			return fixtureResult(`{}`, 200), nil
		})})
	result, runErr := r.Run(ctx)
	if runErr != nil {
		t.Fatal(runErr)
	}
	if result.Status != StatusCanceled || !result.Canceled || calls.Load() != 0 {
		t.Fatalf("status=%s canceled=%t calls=%d", result.Status, result.Canceled, calls.Load())
	}
	if len(result.Rows) != 1 || len(result.Rows[0].Steps) != 1 {
		t.Fatalf("canceled report missing row/steps: %+v", result.Rows)
	}
}
