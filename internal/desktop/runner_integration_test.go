package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/core"
	"gorequest/internal/runner"
)

func runnerFixture(t *testing.T, url, format string) (*App, *queuedTestWindow) {
	t.Helper()
	root := writeProject(t, "A02 local flow")
	store := core.NewStore(filepath.Join(root, ".gorequest"))
	put := func(name, body string) {
		t.Helper()
		if err := store.WriteAtomic(name, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	for _, step := range []string{"login", "query", "update"} {
		method := "GET"
		extra := ""
		body := ""
		if step == "login" {
			method = "POST"
			body = "  body:\n    type: json\n    value: {user: '{{data.user}}'}\n"
		} else {
			extra = "auth: {type: bearer, token: '{{steps.login.token}}'}\n"
		}
		if step == "update" {
			method = "PUT"
			body = "  body:\n    type: json\n    value: {display: 'new-{{data.user}}'}\n"
		}
		put("collections/flow/"+step+".request.yaml", fmt.Sprintf("schemaVersion: 1\nid: %s\nname: %s\nhttp:\n  method: %s\n  url: '%s/%s?user={{data.user}}'\n%s%sassert:\n  - status: 200\n  - schema: {ref: schemas/%s.yaml}\n", step, step, method, url, step, body, extra, step))
	}
	put("schemas/login.yaml", `type: object
properties: {token: {type: string}}
required: [token]
`)
	put("schemas/query.yaml", `type: object
properties: {data: {type: object, properties: {id: {type: integer}}, required: [id]}}
required: [data]
`)
	put("schemas/update.yaml", `type: object
properties: {ok: {const: true}}
required: [ok]
`)
	data := "user\nalice\nbob\n"
	if format == "json" {
		data = `[{"user":"alice"},{"user":"bob"}]`
	}
	put("data/users."+format, data)
	put("suites/flow.suite.yaml", fmt.Sprintf(`schemaVersion: 1
id: a02-flow
name: Local login query update
onFailure: continue
dataset: {format: %s, file: data/users.%s}
steps:
  - id: login
    request: login
    extract: [{name: token, from: jsonpath, path: $.token, sensitive: true}]
  - {id: query, request: query, needs: [login]}
  - {id: update, request: update, needs: [query]}
`, format, format))
	p, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range p.Issues {
		if issue.Level == "error" {
			t.Fatal(issue)
		}
	}
	mgr, _ := newTestManager(t)
	mgr.secrets = core.MapSecrets{"unused": "bound-secret-A02-918"}
	w := newQueuedTestWindow("A02")
	a := &App{mgr: mgr, win: w, ps: newProjectState(root, p)}
	// Unlike a real manager/window, this fake window does not retain App.View.
	// Keep the owner alive until cleanup so weak routing models a live window.
	t.Cleanup(a.CancelRunner)
	a.OpenRunner()
	s := a.runnerState(false)
	s.mode = 1
	s.suite = "suites/flow.suite.yaml"
	return a, w
}

func TestRunnerDesktopHTTPFlowTwoRowsCSVJSON(t *testing.T) {
	for _, format := range []string{"csv", "json"} {
		t.Run(format, func(t *testing.T) {
			var mu sync.Mutex
			calls := 0
			logins := map[string]int{}
			seen := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				user := r.URL.Query().Get("user")
				token := "token-private-A02-" + user
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/login":
					if _, err := r.Cookie("session"); err == nil {
						t.Error("row reused a previous login cookie")
					}
					var input map[string]string
					_ = json.NewDecoder(r.Body).Decode(&input)
					if input["user"] != user {
						t.Error("row body mismatch")
					}
					logins[user]++
					http.SetCookie(w, &http.Cookie{Name: "session", Value: token, Path: "/"})
					fmt.Fprintf(w, `{"token":%q}`, token)
				case "/query", "/update":
					cookie, err := r.Cookie("session")
					if err != nil || cookie.Value != token || r.Header.Get("Authorization") != "Bearer "+token {
						t.Errorf("row auth/cookie isolation failed for %s", user)
					}
					seen[user]++
					if r.URL.Path == "/query" {
						fmt.Fprint(w, `{"data":{"id":7}}`)
					} else {
						var input map[string]string
						_ = json.NewDecoder(r.Body).Decode(&input)
						if input["display"] != "new-"+user {
							t.Error("update row mismatch")
						}
						fmt.Fprint(w, `{"ok":true}`)
					}
				default:
					t.Error("unexpected path", r.URL.Path)
				}
			}))
			defer server.Close()
			a, w := runnerFixture(t, server.URL, format)
			s := a.runnerState(false)
			initialNet := a.buildExecuteOptionsForTab(a.ps, &Tab{Owner: a.ps}, nil, nil, nil)
			// A dirty tab contains a wrong URL; only the saved request may be run.
			a.ps.tabs = []*Tab{{Dirty: true, URL: "http://127.0.0.1:1/unsaved", Owner: a.ps}}
			tt := ui.NewTester(func(c *ui.Context) { a.RunnerModals(c, c.Theme()) }, 1280, 1000)
			tt.Frame()
			if err := tt.Click(a.runnerMsg().Start); err != nil {
				t.Fatal(err)
			}
			if !s.running {
				t.Fatalf("not started: %s", s.notice)
			}
			// Mutate UI/model/environment/device options immediately after capture.
			a.ps.envSel = "production"
			for _, entry := range a.ps.project.Requests {
				entry.Request.HTTP.URL = "http://127.0.0.1:1/changed"
			}
			dev := projectNetStateFor(a.ps, a).netStore.DeviceSettings()
			dev.DevicePolicy.DeniedHosts = []string{"127.0.0.1"}
			if err := projectNetStateFor(a.ps, a).netStore.SaveDeviceSettings(dev); err != nil {
				t.Fatal(err)
			}
			drainRunner(t, w, s)
			if s.result == nil || s.result.Status != runner.StatusPassed || s.result.Summary.Passed != 6 {
				t.Fatalf("%s result=%+v", s.notice, s.result)
			}
			mu.Lock()
			if calls != 6 || seen["alice"] != 2 || seen["bob"] != 2 || logins["alice"] != 1 || logins["bob"] != 1 {
				t.Errorf("calls=%d seen=%v login=%v", calls, seen, logins)
			}
			mu.Unlock()
			// Run identical saved input directly through Core+adapter and compare the
			// snapshot and complete verdict/summary, independent of UI's mutated model.
			p, err := core.OpenProject(a.ps.root)
			if err != nil {
				t.Fatal(err)
			}
			suite, err := runner.LoadSuite(p, s.suite)
			if err != nil {
				t.Fatal(err)
			}
			r, err := prepareDesktopRunner(context.Background(), p, suite, s.result.Environment, initialNet, core.MapSecrets{}, nil, desktopRunnerOptions{})
			if err != nil {
				t.Fatal(err)
			}
			expected, err := r.Run(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if expected.Snapshot != s.result.Snapshot || expected.Status != s.result.Status || !reflect.DeepEqual(expected.Summary, s.result.Summary) {
				t.Fatalf("UI/Core differ: %+v / %+v", s.result, expected)
			}
			for i, row := range expected.Rows {
				for j, step := range row.Steps {
					actual := s.result.Rows[i].Steps[j]
					if step.Status != actual.Status || len(step.Attempts) != len(actual.Attempts) {
						t.Fatal("UI/Core step differs")
					}
				}
			}
			for _, junit := range []bool{false, true} {
				s.exportPath = filepath.Join(t.TempDir(), fmt.Sprintf("report-%t", junit))
				a.exportRunner(junit)
				b, err := os.ReadFile(s.exportPath)
				if err != nil {
					t.Fatal(err)
				}
				for _, secret := range []string{"token-private-A02-alice", "token-private-A02-bob", "bound-secret-A02-918"} {
					if strings.Contains(string(b), secret) {
						t.Fatal("secret in report")
					}
				}
			}
			tt.Frame()
			if !tt.HasText(runnerSummaryText(s.result)) {
				t.Fatal("UI summary differs from Core result")
			}
		})
	}
}

func TestRunnerDesktopBadSchemaAndPreciseSkipped(t *testing.T) {
	for _, bad := range []bool{true, false} {
		t.Run(fmt.Sprint(bad), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/login" {
					fmt.Fprint(w, `{"token":"secret-value-771"}`)
				} else {
					fmt.Fprint(w, `{"data":{"id":"wrong"}}`)
				}
			}))
			defer server.Close()
			a, w := runnerFixture(t, server.URL, "csv")
			s := a.runnerState(false)
			if bad {
				if err := a.ps.project.Store.WriteAtomic("schemas/query.yaml", []byte("properties: {unused: {$ref: missing.yaml}}")); err != nil {
					t.Fatal(err)
				}
			}
			a.StartRunner()
			drainRunner(t, w, s)
			if bad {
				if s.result.Status != runner.StatusConfigError || calls.Load() != 0 {
					t.Fatalf("%+v calls=%d", s.result, calls.Load())
				}
				return
			}
			if s.result.Status != runner.StatusFailed || calls.Load() != 4 {
				t.Fatalf("%+v calls=%d", s.result, calls.Load())
			}
			for _, row := range s.result.Rows {
				if row.Steps[2].Status != runner.StatusSkipped {
					t.Fatal("dependency failure not skipped")
				}
				if f := row.Steps[1].Failure; f == nil || !strings.Contains(f.Message, "$.data.id") {
					t.Fatalf("missing precise schema path: %+v", f)
				}
			}
		})
	}
}

func TestRunnerMultiWindowCancellationNoRetryAndClose(t *testing.T) {
	release := make(chan struct{})
	started := make(chan struct{}, 4)
	ended := make(chan struct{}, 4)
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		started <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-release:
		}
		ended <- struct{}{}
	}))
	defer server.Close()
	defer close(release)
	a, wa := runnerFixture(t, server.URL, "csv")
	b, wb := runnerFixture(t, server.URL, "json")
	for _, app := range []*App{a, b} {
		// Explicit retry policy: cancellation must still stop after first attempt.
		data := `schemaVersion: 1
id: cancel-flow
steps:
  - id: login
    request: login
    before: [{set: {name: unused, value: x}}]
    retry: {maxAttempts: 3, on: [network], allowNonIdempotent: true}
`
		// Login fixture needs data; use a one-row dataset for this focused test.
		data = strings.Replace(data, "steps:", "dataset: {format: json, file: data/cancel.json}\nsteps:", 1)
		_ = app.ps.project.Store.WriteAtomic("data/cancel.json", []byte(`[{"user":"cancel"}]`))
		_ = app.ps.project.Store.WriteAtomic("suites/cancel.suite.yaml", []byte(data))
		app.runnerState(false).suite = "suites/cancel.suite.yaml"
	}
	defer a.CancelRunner()
	defer b.CancelRunner()
	a.StartRunner()
	b.StartRunner()
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-time.After(5 * time.Second):
			t.Fatal("request not started")
		}
	}
	a.CancelRunner()
	drainRunner(t, wa, a.runnerState(false))
	if a.runnerState(false).result.Status != runner.StatusCanceled || a.runnerState(false).result.Summary.Attempts != 1 {
		t.Fatalf("%+v", a.runnerState(false).result)
	}
	// B's callback queue has not been drained. A's results cannot appear there.
	if b.runnerState(false).result != nil || !b.runnerState(false).running {
		t.Fatal("window A changed window B")
	}
	wb.Close() // registered close callback must cancel B without a final UI pump.
	for i := 0; i < 2; i++ {
		select {
		case <-ended:
		case <-time.After(5 * time.Second):
			t.Fatal("close/cancel did not reclaim HTTP request")
		}
	}
	if b.runnerState(false) != nil {
		t.Fatal("closed window retained runner state")
	}
	if calls.Load() != 2 {
		t.Fatalf("cancellation retried: %d", calls.Load())
	}
	wa.Close()
}
