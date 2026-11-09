package desktop

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/mockserver"
	"gorequest/internal/runner"

	"github.com/egoist/mygo/ui"
)

// Fake secret values are plain literals (not harness placeholders) so tests
// never touch a real credential.
const (
	n03FakeSecret = "n03-fake-secret-1"
	n03NewSecret  = "n03-fake-secret-2"
)

func newMockRunnerApp(t *testing.T, files map[string]string) (*App, *queuedTestWindow, string) {
	t.Helper()
	root := writeProject(t, "N03 Mock Runner")
	store := core.NewStore(filepath.Join(root, ".gorequest"))
	for name, body := range files {
		if err := store.WriteAtomic(name, []byte(body)); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	p, err := core.OpenProject(root)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	for _, issue := range p.Issues {
		if issue.Level == "error" {
			t.Fatalf("project issue: %v", issue)
		}
	}
	mgr, _ := newTestManager(t)
	mgr.secrets = core.MapSecrets{"token": n03FakeSecret}
	w := newQueuedTestWindow("N03")
	a := &App{mgr: mgr, win: w, ps: newProjectState(root, p)}
	t.Cleanup(a.CancelRunner)
	a.OpenRunner()
	s := a.runnerState(false)
	s.mode = 1
	s.suite = "suites/items.suite.yaml"
	s.mockEnabled = true
	s.mockPort = "0"
	s.mockScenario = "default"
	s.mockEndpointVar = "mockUrl"
	return a, w, root
}

func mockRequestAndSuite() map[string]string {
	return map[string]string{
		"environments/dev.yaml": `schemaVersion: 1
id: dev
secrets:
  - token
`,
		"collections/items/get.request.yaml": `schemaVersion: 1
id: get-item
name: Get Item
http:
  method: GET
  url: '{{mockUrl}}/v1/items/{{data.id}}'
  headers:
    - {name: X-Token, value: '{{secret.token}}'}
assert:
  - status: 200
`,
		"data/items.csv": "id\n101\n102\n",
		"suites/items.suite.yaml": `schemaVersion: 1
id: items-suite
name: Items Suite
dataset: {format: csv, file: data/items.csv}
steps:
  - id: first
    request: get-item
  - id: second
    request: get-item
    needs: [first]
`,
	}
}

func mockRuleFile(status int, body string) string {
	return fmt.Sprintf(`schemaVersion: 1
id: items-mock
name: Items
defaultScenario: default
rules:
  - id: get-item
    priority: 10
    match: {method: GET, path: '/v1/items/{id}'}
    response:
      status: %d
      contentType: application/json
      body: '%s'
`, status, body)
}

func drainRunnerWithin(t *testing.T, w *queuedTestWindow, s *runnerState, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for s.running && time.Now().Before(deadline) {
		w.drainUI()
		time.Sleep(time.Millisecond)
	}
	w.drainUI()
	if s.running {
		t.Fatalf("runner did not complete within %s", timeout)
	}
}

func assertPortFree(t *testing.T, port int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if err == nil {
			_ = ln.Close()
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("port %d is still bound after the run: %v", port, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func assertNoSecretInRecords(t *testing.T, records []mockserver.AccessRecord, secret string) {
	t.Helper()
	for _, rec := range records {
		fields := []string{rec.SanitizedQuery, rec.RequestBodyPreview, rec.ResponseBodyPreview, rec.Diagnostic}
		for _, pair := range rec.RequestHeaders {
			fields = append(fields, pair[1])
		}
		for _, pair := range rec.ResponseHeaders {
			fields = append(fields, pair[1])
		}
		for _, f := range fields {
			if strings.Contains(f, secret) {
				t.Fatalf("access record leaked the fake secret: %q", f)
			}
		}
	}
}

func TestRunnerControlledMockTwoStepTwoRowsLoopback(t *testing.T) {
	files := mockRequestAndSuite()
	files["mocks/items.mock.yaml"] = mockRuleFile(200, `{"id":"{{path.id}}","ok":true}`)
	a, w, root := newMockRunnerApp(t, files)

	mockPath := filepath.Join(root, ".gorequest", "mocks", "items.mock.yaml")
	envPath := filepath.Join(root, ".gorequest", "environments", "dev.yaml")
	mockBefore, _ := os.ReadFile(mockPath)
	envBefore, _ := os.ReadFile(envPath)

	a.StartRunner()
	s := a.runnerState(false)
	if !s.running {
		t.Fatalf("runner did not start: %+v", s)
	}
	drainRunnerWithin(t, w, s, 10*time.Second)

	if s.result == nil || s.result.Status != runner.StatusPassed {
		t.Fatalf("expected passed run, got %+v", s.result)
	}
	if s.result.Summary.Rows != 2 || s.result.Summary.StepsTotal != 4 || s.result.Summary.Passed != 4 {
		t.Fatalf("unexpected summary: %+v", s.result.Summary)
	}
	mr := s.mockResult
	if mr == nil {
		t.Fatal("missing controlled Mock result metadata")
	}
	if !strings.HasPrefix(mr.BaseURL, "http://127.0.0.1:") || mr.Port == 0 {
		t.Fatalf("missing actual loopback endpoint: %+v", mr)
	}
	if mr.Scenario != "default" || mr.Snapshot == "" {
		t.Fatalf("missing frozen scenario/digest: %+v", mr)
	}
	if !mr.ServerStopped || mr.StopErr != "" {
		t.Fatalf("Mock server was not cleanly released: %+v", mr)
	}
	if len(mr.AccessRecords) != 4 {
		t.Fatalf("want 4 mock access records, got %d", len(mr.AccessRecords))
	}
	for _, rec := range mr.AccessRecords {
		if rec.MatchedRuleID != "get-item" {
			t.Fatalf("unexpected matched rule: %+v", rec)
		}
	}
	assertNoSecretInRecords(t, mr.AccessRecords, n03FakeSecret)
	assertPortFree(t, mr.Port)

	// JSON/JUnit reports must not carry the fake secret either.
	jsonReport, err := runner.MarshalJSONReport(s.result)
	if err != nil {
		t.Fatal(err)
	}
	junitReport, err := runner.MarshalJUnitReport(s.result)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(jsonReport), n03FakeSecret) || strings.Contains(string(junitReport), n03FakeSecret) {
		t.Fatal("runner report leaked the fake secret")
	}

	// The run must not rewrite shared mock/environment files.
	mockAfter, _ := os.ReadFile(mockPath)
	envAfter, _ := os.ReadFile(envPath)
	if string(mockBefore) != string(mockAfter) || string(envBefore) != string(envAfter) {
		t.Fatal("controlled Mock run modified shared project files")
	}
}

func TestRunnerControlledMockFreezesRules(t *testing.T) {
	files := mockRequestAndSuite()
	files["mocks/items.mock.yaml"] = mockRuleFile(200, `{"ok":true}`)
	a, w, root := newMockRunnerApp(t, files)

	a.StartRunner()
	s := a.runnerState(false)
	// The snapshot is frozen synchronously in StartRunner; changing the file now
	// must not affect this run.
	mockPath := filepath.Join(root, ".gorequest", "mocks", "items.mock.yaml")
	if err := os.WriteFile(mockPath, []byte(mockRuleFile(500, `{"ok":false}`)), 0600); err != nil {
		t.Fatal(err)
	}
	drainRunnerWithin(t, w, s, 10*time.Second)
	if s.result == nil || s.result.Status != runner.StatusPassed {
		t.Fatalf("first run did not use the frozen 200 rule: %+v", s.result)
	}

	// The next run must see the changed rule and fail the status assertion.
	a.StartRunner()
	drainRunnerWithin(t, w, s, 10*time.Second)
	if s.result == nil || s.result.Status != runner.StatusFailed {
		t.Fatalf("second run did not use the changed 500 rule: %+v", s.result)
	}
}

func TestRunnerControlledMockFreezesSecrets(t *testing.T) {
	files := map[string]string{
		"environments/dev.yaml": `schemaVersion: 1
id: dev
secrets:
  - token
`,
		"collections/items/get.request.yaml": `schemaVersion: 1
id: get-item
name: Get Item
http:
  method: GET
  url: '{{mockUrl}}/v1/items/1'
  headers:
    - {name: X-Token, value: '{{secret.token}}'}
assert:
  - status: 200
`,
		"suites/items.suite.yaml": `schemaVersion: 1
id: items-suite
name: Items Suite
steps:
  - id: first
    request: get-item
`,
		"mocks/secret.mock.yaml": fmt.Sprintf(`schemaVersion: 1
id: secret-mock
name: Secret
defaultScenario: default
rules:
  - id: old-secret
    priority: 10
    match:
      method: GET
      path: /v1/items/{id}
      headers:
        - {name: X-Token, equals: '%s'}
    response: {status: 200, contentType: application/json, body: '{"ok":true}'}
  - id: new-secret
    priority: 10
    match:
      method: GET
      path: /v1/items/{id}
      headers:
        - {name: X-Token, equals: '%s'}
    response: {status: 500, contentType: application/json, body: '{"ok":false}'}
`, n03FakeSecret, n03NewSecret),
	}
	a, w, _ := newMockRunnerApp(t, files)

	a.StartRunner()
	s := a.runnerState(false)
	// The sealed secret snapshot must keep the old value for this run.
	a.mgr.secrets = core.MapSecrets{"token": n03NewSecret}
	drainRunnerWithin(t, w, s, 10*time.Second)
	if s.result == nil || s.result.Status != runner.StatusPassed {
		t.Fatalf("first run did not use the frozen secret: %+v", s.result)
	}

	a.StartRunner()
	drainRunnerWithin(t, w, s, 10*time.Second)
	if s.result == nil || s.result.Status != runner.StatusFailed {
		t.Fatalf("second run did not use the changed secret: %+v", s.result)
	}
}

func delayedMockFiles(delayMs int) map[string]string {
	return map[string]string{
		"environments/dev.yaml": `schemaVersion: 1
id: dev
secrets:
  - token
`,
		"collections/items/get.request.yaml": `schemaVersion: 1
id: get-item
name: Get Item
http:
  method: GET
  url: '{{mockUrl}}/v1/items/1'
assert:
  - status: 200
`,
		"suites/items.suite.yaml": `schemaVersion: 1
id: items-suite
name: Items Suite
steps:
  - id: first
    request: get-item
`,
		"mocks/slow.mock.yaml": fmt.Sprintf(`schemaVersion: 1
id: slow-mock
name: Slow
defaultScenario: default
rules:
  - id: slow-item
    priority: 10
    match: {method: GET, path: '/v1/items/{id}'}
    response:
      status: 200
      contentType: application/json
      delayMs: %d
      body: '{"ok":true}'
`, delayMs),
	}
}

func TestRunnerControlledMockCancelDuringDelayReleases(t *testing.T) {
	a, w, _ := newMockRunnerApp(t, delayedMockFiles(5000))
	a.StartRunner()
	s := a.runnerState(false)
	if !s.running {
		t.Fatal("runner did not start")
	}
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	a.CancelRunner()
	drainRunnerWithin(t, w, s, 4*time.Second)
	if elapsed := time.Since(start); elapsed > 4*time.Second {
		t.Fatalf("cancel did not interrupt the mock delay promptly: %s", elapsed)
	}
	if s.result == nil || s.result.Status != runner.StatusCanceled {
		t.Fatalf("expected canceled run, got %+v", s.result)
	}
	if s.mockResult == nil || !s.mockResult.ServerStopped {
		t.Fatalf("Mock server not released after cancel: %+v", s.mockResult)
	}
	assertPortFree(t, s.mockResult.Port)
}

func TestRunnerControlledMockWindowCloseReleases(t *testing.T) {
	a, w, _ := newMockRunnerApp(t, delayedMockFiles(5000))
	a.StartRunner()
	s := a.runnerState(false)
	if !s.running {
		t.Fatal("runner did not start")
	}
	// The actual endpoint is published while the run is active, so capture it
	// before the window state is torn down.
	if s.mockResult == nil || s.mockResult.Port == 0 {
		t.Fatalf("active run did not publish its bound endpoint: %+v", s.mockResult)
	}
	port := s.mockResult.Port
	time.Sleep(50 * time.Millisecond)
	// Fire the registered OnClosed handlers without destroying the headless
	// window. Closing the window cancels the run context and deletes the window
	// state, so the final result is intentionally not delivered to a dead window.
	w.mu.Lock()
	handlers := append([]func(){}, w.onClosed...)
	w.mu.Unlock()
	for _, fn := range handlers {
		fn()
	}
	// The window is gone, so the result is not delivered; the listener must still
	// be released by the run-context watcher.
	assertPortFree(t, port)
}

func TestRunnerControlledMockPrepareErrorReleasesLease(t *testing.T) {
	files := map[string]string{
		"environments/dev.yaml": `schemaVersion: 1
id: dev
`,
		"collections/items/upload.request.yaml": `schemaVersion: 1
id: upload
name: Upload
http:
  method: POST
  url: '{{mockUrl}}/v1/upload'
  body:
    type: text
    file: 'local:slot'
`,
		"suites/items.suite.yaml": `schemaVersion: 1
id: items-suite
name: Items Suite
steps:
  - id: upload
    request: upload
`,
		"mocks/items.mock.yaml": `schemaVersion: 1
id: items-mock
name: Items
defaultScenario: default
rules:
  - id: upload
    priority: 10
    match: {method: POST, path: /v1/upload}
    response: {status: 200, contentType: application/json, body: '{"ok":true}'}
`,
	}
	a, _, _ := newMockRunnerApp(t, files)
	a.StartRunner()
	s := a.runnerState(false)
	if s.running {
		t.Fatal("runner must not enter the worker when preparation fails")
	}
	if s.result == nil || s.result.Status != runner.StatusConfigError {
		t.Fatalf("expected config error, got %+v", s.result)
	}
	if s.mockResult == nil || !s.mockResult.ServerStopped {
		t.Fatalf("Mock lease was not released after prepare failure: %+v", s.mockResult)
	}
	assertPortFree(t, s.mockResult.Port)
}

func TestRunnerControlledMockPortConflict(t *testing.T) {
	files := mockRequestAndSuite()
	files["mocks/items.mock.yaml"] = mockRuleFile(200, `{"ok":true}`)
	a, _, _ := newMockRunnerApp(t, files)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port
	s := a.runnerState(false)
	s.mockPort = fmt.Sprintf("%d", port)

	a.StartRunner()
	if s.running {
		t.Fatal("runner must not start on a conflicting port")
	}
	if s.result == nil || s.result.Status != runner.StatusConfigError {
		t.Fatalf("expected config error, got %+v", s.result)
	}
	if !strings.Contains(strings.ToLower(s.notice), "port conflict") {
		t.Fatalf("notice does not explain the port conflict: %q", s.notice)
	}
	if s.mockResult != nil {
		t.Fatalf("no Mock lease should exist after a bind failure: %+v", s.mockResult)
	}
}

func TestRunnerControlledMockBadRuleAndUnknownScenario(t *testing.T) {
	t.Run("bad rule", func(t *testing.T) {
		files := mockRequestAndSuite()
		files["mocks/items.mock.yaml"] = "schemaVersion: 99\nid: bad\nrules: []\n"
		a, _, _ := newMockRunnerApp(t, files)
		a.StartRunner()
		s := a.runnerState(false)
		if s.result == nil || s.result.Status != runner.StatusConfigError || s.running {
			t.Fatalf("expected config error for bad rule, got %+v", s.result)
		}
	})
	t.Run("unknown scenario", func(t *testing.T) {
		files := mockRequestAndSuite()
		files["mocks/items.mock.yaml"] = `schemaVersion: 1
id: scoped
name: Scoped
defaultScenario: only
rules:
  - id: only-rule
    priority: 10
    scenarios: [only]
    match: {method: GET, path: '/v1/items/{id}'}
    response: {status: 200, body: '{"ok":true}'}
`
		a, _, _ := newMockRunnerApp(t, files)
		s := a.runnerState(false)
		s.mockScenario = "special"
		a.StartRunner()
		if s.result == nil || s.result.Status != runner.StatusConfigError || s.running {
			t.Fatalf("expected config error for empty scenario, got %+v", s.result)
		}
	})
}

func TestRunnerControlledMockTwoProjectsIsolated(t *testing.T) {
	fileSets := []map[string]string{delayedMockFiles(300), delayedMockFiles(300)}
	apps := make([]*App, 2)
	wins := make([]*queuedTestWindow, 2)
	states := make([]*runnerState, 2)
	for i := range apps {
		apps[i], wins[i], _ = newMockRunnerApp(t, fileSets[i])
		apps[i].StartRunner()
		states[i] = apps[i].runnerState(false)
		if !states[i].running {
			t.Fatalf("project %d did not start", i)
		}
	}
	time.Sleep(50 * time.Millisecond)
	apps[0].CancelRunner()
	drainRunnerWithin(t, wins[0], states[0], 4*time.Second)
	drainRunnerWithin(t, wins[1], states[1], 4*time.Second)

	if states[0].result == nil || states[0].result.Status != runner.StatusCanceled {
		t.Fatalf("project 0 should be canceled: %+v", states[0].result)
	}
	if states[1].result == nil || states[1].result.Status != runner.StatusPassed {
		t.Fatalf("canceling project 0 must not affect project 1: %+v", states[1].result)
	}
	if states[0].mockResult == nil || states[1].mockResult == nil {
		t.Fatal("missing per-project Mock metadata")
	}
	if states[0].mockResult.BaseURL == states[1].mockResult.BaseURL {
		t.Fatalf("two concurrent projects shared a Mock endpoint: %s", states[0].mockResult.BaseURL)
	}
	if !states[0].mockResult.ServerStopped || !states[1].mockResult.ServerStopped {
		t.Fatal("both Mock servers must be released")
	}
}

func TestRunnerControlledMockConfigValidation(t *testing.T) {
	s := &runnerState{mockEnabled: true, mockPort: "70000", mockEndpointVar: "mockUrl"}
	if s.mockConfigError() == "" {
		t.Fatal("out-of-range port must be rejected")
	}
	if _, err := s.mockPortValue(); err == nil {
		t.Fatal("mockPortValue accepted 70000")
	}
	s.mockPort = "0"
	s.mockEndpointVar = "secret.token"
	if s.mockConfigError() == "" {
		t.Fatal("reserved endpoint variable must be rejected")
	}
	s.mockEndpointVar = "baseUrl"
	if s.mockConfigError() != "" {
		t.Fatalf("valid configuration reported %q", s.mockConfigError())
	}
	s.mockEnabled = false
	if s.mockConfigError() != "" {
		t.Fatal("disabled Mock must not report a config error")
	}
}

func TestRunnerMockConfigUIShowsLoopbackAndValidatesPort(t *testing.T) {
	a, _ := openTestProject(t, "Mock UI")
	a.OpenRunner()
	s := a.runnerState(false)
	s.mockEnabled = true
	s.mockPort = "70000"
	tt := ui.NewTester(func(c *ui.Context) {
		a.runnerMockConfigUI(c, c.Theme(), uikit.PaletteFor(c.Theme()), s)
	}, 900, 600)
	tt.Frame()
	m := a.runnerMockMsg()
	for _, want := range []string{m.Enable, m.Loopback, m.Port, m.EndpointHint} {
		if !tt.HasText(want) {
			t.Fatalf("Mock config UI missing %q: %v", want, tt.Texts())
		}
	}
	if !tt.HasText(s.mockConfigError()) {
		t.Fatalf("port validation error is not shown: %v", tt.Texts())
	}
}

func TestRunnerMockResultUIShowsEndpointAndRelease(t *testing.T) {
	a, _ := openTestProject(t, "Mock result UI")
	a.OpenRunner()
	s := a.runnerState(false)
	s.mockResult = &runnerMockResult{
		BaseURL:       "http://127.0.0.1:41234",
		Port:          41234,
		Scenario:      "default",
		Snapshot:      "abcdef0123456789",
		ServerStopped: true,
	}
	tt := ui.NewTester(func(c *ui.Context) {
		a.runnerMockResultUI(c, c.Theme(), uikit.PaletteFor(c.Theme()), s)
	}, 900, 600)
	tt.Frame()
	m := a.runnerMockMsg()
	for _, want := range []string{"http://127.0.0.1:41234", "default", "abcdef0123456789", m.Released} {
		if !tt.HasText(want) {
			t.Fatalf("Mock result UI missing %q: %v", want, tt.Texts())
		}
	}
	s.mockResult.ServerStopped = false
	tt.Frame()
	if !tt.HasText(m.NotReleased) {
		t.Fatalf("unreleased Mock server is not surfaced: %v", tt.Texts())
	}
}

func TestRunnerControlledMockCancelThenRerunUsesNewGeneration(t *testing.T) {
	a, w, root := newMockRunnerApp(t, delayedMockFiles(5000))
	a.StartRunner()
	s := a.runnerState(false)
	genA := s.generation
	// Freeze run A's snapshot, then make the next run fast.
	mockPath := filepath.Join(root, ".gorequest", "mocks", "slow.mock.yaml")
	fast := `schemaVersion: 1
id: slow-mock
name: Fast
defaultScenario: default
rules:
  - id: slow-item
    priority: 10
    match: {method: GET, path: '/v1/items/{id}'}
    response: {status: 200, contentType: application/json, body: '{"ok":true}'}
`
	if err := os.WriteFile(mockPath, []byte(fast), 0600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	a.CancelRunner()
	drainRunnerWithin(t, w, s, 4*time.Second)
	if s.result == nil || s.result.Status != runner.StatusCanceled {
		t.Fatalf("first run should be canceled: %+v", s.result)
	}
	if s.generation != genA {
		t.Fatalf("generation changed without a new run: %d -> %d", genA, s.generation)
	}

	a.StartRunner()
	if s.generation != genA+1 {
		t.Fatalf("rerun did not advance generation: %d -> %d", genA, s.generation)
	}
	drainRunnerWithin(t, w, s, 4*time.Second)
	if s.result == nil || s.result.Status != runner.StatusPassed {
		t.Fatalf("rerun should pass against the new fast rule: %+v", s.result)
	}
	if s.mockResult == nil || !s.mockResult.ServerStopped {
		t.Fatalf("rerun Mock server not released: %+v", s.mockResult)
	}
	assertPortFree(t, s.mockResult.Port)
}
