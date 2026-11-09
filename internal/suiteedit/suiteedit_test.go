package suiteedit

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

func newTestProject(t *testing.T) *core.Project {
	t.Helper()
	dir := t.TempDir()
	p, err := core.NewProject(dir, "Suite Edit Test")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return p
}

func addRequest(t *testing.T, p *core.Project, collectionRel, name, url string) *core.RequestEntry {
	t.Helper()
	if collectionRel != "" {
		if _, err := p.NewFolder("", collectionRel); err != nil && !strings.Contains(err.Error(), "already exists") {
			t.Fatalf("NewFolder(%q): %v", collectionRel, err)
		}
	}
	entry, err := p.NewRequest(collectionRel, name)
	if err != nil {
		t.Fatalf("NewRequest(%q): %v", name, err)
	}
	entry.Request.HTTP.URL = url
	if _, err := p.SaveRequestPreserving(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatalf("SaveRequestPreserving: %v", err)
	}
	return entry
}

func writeSuite(t *testing.T, p *core.Project, rel, content string) {
	t.Helper()
	if err := p.Store.WriteAtomic(rel, []byte(content)); err != nil {
		t.Fatalf("write suite: %v", err)
	}
}

func stepByID(t *testing.T, d *Draft, id string) *StepDraft {
	t.Helper()
	for _, st := range d.Steps {
		if st.ID == id {
			return st
		}
	}
	t.Fatalf("step %q not found", id)
	return nil
}

// TestSuiteEditNewSaveReloadAndRun covers the acceptance path: create a
// cross-collection two-step suite with a CSV dataset, save it, reload it, then
// run it through the existing pure-Go runner against a loopback server and
// verify row isolation.
func TestSuiteEditNewSaveReloadAndRun(t *testing.T) {
	for _, format := range []string{"csv", "json"} {
		t.Run(format, func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string]string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/login":
					user := r.URL.Query().Get("user")
					fmt.Fprintf(w, `{"token":"tok-%s"}`, user)
				case "/profile":
					token := r.URL.Query().Get("token")
					mu.Lock()
					seen[r.URL.Query().Get("row")] = token
					mu.Unlock()
					fmt.Fprintf(w, `{"user":"%s"}`, token)
				default:
					http.NotFound(w, r)
				}
			}))
			defer server.Close()

			p := newTestProject(t)
			login := addRequest(t, p, "auth", "Login", server.URL+"/login?user={{data.user}}&row={{data.row}}")
			profile := addRequest(t, p, "user", "Profile", server.URL+"/profile?token={{steps.login.token}}&row={{data.row}}")

			d := NewDraft("checkout-flow", "Checkout flow")
			first := d.AddStep("login")
			first.Name = "Login"
			first.RequestRef = login.RelPath
			first.Extract = []runner.Extraction{{Name: "token", From: "jsonpath", Path: "$.token"}}
			second := d.AddStep("profile")
			second.Name = "Profile"
			second.RequestRef = profile.RelPath
			second.Needs = []string{"login"}
			second.Assert = mustNodes(t, `- status: 200`)

			d.Dataset = DatasetDraft{Enabled: true, Format: format, File: "data." + format}
			if format == "csv" {
				if err := p.Store.WriteAtomic("data.csv", []byte("user,row\nalice,1\nbob,2\n")); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := p.Store.WriteAtomic("data.json", []byte(`[{"user":"alice","row":"1"},{"user":"bob","row":"2"}]`)); err != nil {
					t.Fatal(err)
				}
			}

			rel, digest, err := Save(p, d)
			if err != nil {
				t.Fatalf("Save: %v", err)
			}
			if rel != "suites/checkout-flow.suite.yaml" || digest == "" {
				t.Fatalf("unexpected save result %q %q", rel, digest)
			}

			reloaded, err := Load(p, rel)
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if reloaded.ID != "checkout-flow" || len(reloaded.Steps) != 2 {
				t.Fatalf("reloaded draft mismatch: %+v", reloaded)
			}
			if reloaded.Steps[0].Extract[0].Name != "token" || len(reloaded.Steps[1].Needs) != 1 {
				t.Fatalf("preserved fields lost: %+v", reloaded.Steps)
			}

			suite, err := runner.LoadSuite(p, rel)
			if err != nil {
				t.Fatalf("runner.LoadSuite: %v", err)
			}
			r := runner.New(p, suite, runner.Options{})
			if err := r.Prepare(context.Background()); err != nil {
				t.Fatalf("Prepare: %v", err)
			}
			result, err := r.Run(context.Background())
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if result.Status != runner.StatusPassed || result.Summary.Rows != 2 {
				t.Fatalf("run result: status=%s rows=%d failure=%+v", result.Status, result.Summary.Rows, result.Failure)
			}
			mu.Lock()
			defer mu.Unlock()
			if seen["1"] != "tok-alice" || seen["2"] != "tok-bob" {
				t.Fatalf("row isolation broken: %+v", seen)
			}
		})
	}
}

func mustNodes(t *testing.T, text string) []yaml.Node {
	t.Helper()
	var nodes []yaml.Node
	if err := yaml.Unmarshal([]byte(text), &nodes); err != nil {
		t.Fatalf("parse assertion nodes: %v", err)
	}
	return nodes
}

// TestSuiteEditPreservesUntouchedYAML pins the surgical patch: comments, key
// order, assert/extract/retry and untouched steps survive a rename + reorder.
func TestSuiteEditPreservesUntouchedYAML(t *testing.T) {
	p := newTestProject(t)
	addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	addRequest(t, p, "user", "Profile", "http://127.0.0.1/profile")
	writeSuite(t, p, "suites/demo.suite.yaml", `# top comment
schemaVersion: 1
id: demo
name: Demo
variables:
  base: "http://127.0.0.1"   # keep this comment
onFailure: stop
steps:
  # login step comment
  - id: login
    name: Login
    request: collections/auth/login.request.yaml
    assert:
      - status: 200
    extract:
      - name: token
        from: jsonpath
        path: $.token
    retry:
      maxAttempts: 2
      on: [network]
  # profile step comment
  - id: profile
    name: Profile
    request: collections/user/profile.request.yaml
    needs: [login]
`)
	d, err := Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	d.Name = "Renamed"
	d.MoveStep(1, -1) // profile first; declaration order is only a tie-breaker
	if _, _, err := Save(p, d); err != nil {
		t.Fatalf("Save: %v", err)
	}
	data, _, err := p.Store.ReadBounded("suites/demo.suite.yaml", runner.DefaultMaxSuiteBytes)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, want := range []string{
		"# top comment", "# keep this comment", "# login step comment", "# profile step comment",
		"name: Renamed", "assert:", "extract:", "retry:", "maxAttempts: 2", "needs:", "status: 200",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("saved suite lost %q:\n%s", want, text)
		}
	}
	if strings.Index(text, "id: profile") > strings.Index(text, "id: login") {
		t.Fatalf("steps were not reordered:\n%s", text)
	}
	suite, err := runner.ParseSuite(data)
	if err != nil {
		t.Fatalf("reparse: %v", err)
	}
	if err := suite.ValidateStructure(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if len(suite.Steps[0].Extract) != 0 || len(suite.Steps[1].Extract) != 1 {
		t.Fatalf("extract moved with the wrong node: %+v", suite.Steps)
	}
}

// TestSuiteEditRequestReferenceStability verifies an unchanged path reference is
// not rewritten to an id, while an explicit selection change writes the stable
// request id.
func TestSuiteEditRequestReferenceStability(t *testing.T) {
	p := newTestProject(t)
	login := addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	addRequest(t, p, "user", "Profile", "http://127.0.0.1/profile")
	writeSuite(t, p, "suites/demo.suite.yaml", `schemaVersion: 1
id: demo
name: Demo
steps:
  - id: login
    request: collections/auth/login.request.yaml
  - id: profile
    request: collections/user/profile.request.yaml
    needs: [login]
`)
	d, err := Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d.Name = "Demo 2"
	if _, _, err := Save(p, d); err != nil {
		t.Fatal(err)
	}
	data, _, _ := p.Store.ReadBounded("suites/demo.suite.yaml", runner.DefaultMaxSuiteBytes)
	if !strings.Contains(string(data), "request: collections/auth/login.request.yaml") {
		t.Fatalf("unchanged path reference was rewritten:\n%s", data)
	}
	// Simulate choosing the same request from the picker: identity unchanged, so
	// the editor leaves the reference alone. Then choose a different request.
	d2, err := Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d2.Steps[0].RequestRef = login.Request.ID
	if _, _, err := Save(p, d2); err != nil {
		t.Fatal(err)
	}
	data, _, _ = p.Store.ReadBounded("suites/demo.suite.yaml", runner.DefaultMaxSuiteBytes)
	if !strings.Contains(string(data), "request: "+login.Request.ID) {
		t.Fatalf("explicit selection did not write the stable id:\n%s", data)
	}
}

// TestSuiteEditDeleteGuard blocks deleting a step another step depends on or
// references through a template.
func TestSuiteEditDeleteGuard(t *testing.T) {
	p := newTestProject(t)
	addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	profile := addRequest(t, p, "user", "Profile", "http://127.0.0.1/profile")
	writeSuite(t, p, "suites/demo.suite.yaml", `schemaVersion: 1
id: demo
name: Demo
steps:
  - id: login
    request: collections/auth/login.request.yaml
  - id: profile
    request: collections/user/profile.request.yaml
    needs: [login]
`)
	d, err := Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := d.CanDeleteStep(p, d.Steps[0].RowID); err == nil || !strings.Contains(err.Error(), "depends") {
		t.Fatalf("dependency delete guard: %v", err)
	}
	// Add a template reference through the other step's request and verify it is
	// detected too.
	profile.Request.HTTP.URL = "http://127.0.0.1/profile?token={{steps.login.token}}"
	if _, err := p.SaveRequestPreserving(profile, profile.Request, profile.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	d, err = Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	d.Steps[1].Needs = nil
	if err := d.CanDeleteStep(p, d.Steps[0].RowID); err == nil || !strings.Contains(err.Error(), "references") {
		t.Fatalf("template delete guard: %v", err)
	}
	if err := d.CanDeleteStep(p, d.Steps[1].RowID); err != nil {
		t.Fatalf("independent step must be deletable: %v", err)
	}
}

// TestSuiteEditValidationErrors covers the explicit failure cases.
func TestSuiteEditValidationErrors(t *testing.T) {
	p := newTestProject(t)
	login := addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	ws, err := p.NewWebSocketRequest("", "Socket")
	if err != nil {
		t.Fatal(err)
	}
	_ = ws

	cases := []struct {
		name  string
		build func() *Draft
		want  string
	}{
		{"empty", func() *Draft { return NewDraft("x", "X") }, "no steps"},
		{"self", func() *Draft {
			d := NewDraft("x", "X")
			st := d.AddStep("one")
			st.RequestRef = login.RelPath
			st.Needs = []string{"one"}
			return d
		}, "depends on itself"},
		{"duplicate", func() *Draft {
			d := NewDraft("x", "X")
			for i := 0; i < 2; i++ {
				st := d.AddStep("one")
				st.RequestRef = login.RelPath
			}
			return d
		}, "duplicate step id"},
		{"missing request", func() *Draft {
			d := NewDraft("x", "X")
			st := d.AddStep("one")
			st.RequestRef = "collections/missing.request.yaml"
			return d
		}, "was not found"},
		{"websocket", func() *Draft {
			d := NewDraft("x", "X")
			st := d.AddStep("one")
			st.RequestRef = ws.RelPath
			return d
		}, "WebSocket"},
		{"bad dataset", func() *Draft {
			d := NewDraft("x", "X")
			st := d.AddStep("one")
			st.RequestRef = login.RelPath
			d.Dataset = DatasetDraft{Enabled: true, Format: "csv", File: "missing.csv"}
			return d
		}, "missing.csv"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.build().Validate(p); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want error containing %q, got %v", tc.want, err)
			}
		})
	}

	if _, err := SuiteRelPath("a/b"); err == nil {
		t.Fatal("path separator must be rejected")
	}
	if _, err := SuiteRelPath(`a\b`); err == nil {
		t.Fatal("backslash must be rejected")
	}
	if _, err := SuiteRelPath(".."); err == nil {
		t.Fatal("dot-dot must be rejected")
	}
	if _, err := SuiteRelPath(""); err == nil {
		t.Fatal("empty id must be rejected")
	}
}

// TestSuiteEditConflictAndNoWriteOnLoad verifies external edits are not
// overwritten and loading never writes.
func TestSuiteEditConflictAndNoWriteOnLoad(t *testing.T) {
	p := newTestProject(t)
	addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	writeSuite(t, p, "suites/demo.suite.yaml", `schemaVersion: 1
id: demo
name: Demo
steps:
  - id: login
    request: collections/auth/login.request.yaml
`)
	before, _, _ := p.Store.ReadBounded("suites/demo.suite.yaml", runner.DefaultMaxSuiteBytes)
	d, err := Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	after, _, _ := p.Store.ReadBounded("suites/demo.suite.yaml", runner.DefaultMaxSuiteBytes)
	if string(before) != string(after) {
		t.Fatal("Load wrote to disk")
	}
	external := append([]byte("# external edit\n"), before...)
	if err := p.Store.WriteAtomic("suites/demo.suite.yaml", external); err != nil {
		t.Fatal(err)
	}
	d.Name = "Mine"
	if _, _, err := Save(p, d); err == nil || !core.IsConflict(err) {
		t.Fatalf("want conflict, got %v", err)
	}
	if d.Name != "Mine" || d.Baseline == "" {
		t.Fatal("draft was not retained after conflict")
	}
}

// TestSuiteEditExternalDeleteIsConflict verifies a deleted file is not recreated
// silently from a stale draft.
func TestSuiteEditExternalDeleteIsConflict(t *testing.T) {
	p := newTestProject(t)
	addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	writeSuite(t, p, "suites/demo.suite.yaml", `schemaVersion: 1
id: demo
name: Demo
steps:
  - id: login
    request: collections/auth/login.request.yaml
`)
	d, err := Load(p, "suites/demo.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Store.RemoveChecked("suites/demo.suite.yaml", d.Baseline); err != nil {
		t.Fatal(err)
	}
	d.Name = "Mine"
	if _, _, err := Save(p, d); err == nil || !core.IsConflict(err) {
		t.Fatalf("want delete conflict, got %v", err)
	}
	if d.Name != "Mine" {
		t.Fatal("draft was not retained")
	}
}

// TestSuiteEditNewTargetExists refuses to overwrite an existing new target.
func TestSuiteEditNewTargetExists(t *testing.T) {
	p := newTestProject(t)
	addRequest(t, p, "auth", "Login", "http://127.0.0.1/login")
	writeSuite(t, p, "suites/demo.suite.yaml", "schemaVersion: 1\nid: demo\nname: Demo\nsteps:\n  - id: login\n    request: collections/auth/login.request.yaml\n")
	d := NewDraft("demo", "Demo")
	st := d.AddStep("login")
	st.RequestRef = "collections/auth/login.request.yaml"
	if _, _, err := Save(p, d); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("want existing-target error, got %v", err)
	}
}

// TestSuiteEditPreview covers bounded preview, truncation, masking and path
// safety.
func TestSuiteEditPreview(t *testing.T) {
	p := newTestProject(t)
	var b strings.Builder
	b.WriteString("user,token\n")
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "u%d,t%d\n", i, i)
	}
	if err := p.Store.WriteAtomic("data.csv", []byte(b.String())); err != nil {
		t.Fatal(err)
	}
	d := NewDraft("x", "X")
	d.Dataset = DatasetDraft{Enabled: true, Format: "csv", File: "data.csv"}
	preview, err := PreviewDataset(p, d, 0, 0)
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if len(preview.Rows) != 20 || !preview.RowsTruncated {
		t.Fatalf("want 20 truncated rows, got %d truncated=%v", len(preview.Rows), preview.RowsTruncated)
	}
	if preview.Rows[0]["token"] != "***" {
		t.Fatalf("sensitive column not masked: %+v", preview.Rows[0])
	}
	if preview.EffectiveMaxRows != runner.DefaultMaxRows || preview.EffectiveMaxBytes != runner.DefaultMaxDatasetBytes {
		t.Fatalf("effective limits wrong: %+v", preview)
	}

	if err := p.Store.WriteAtomic("data.json", []byte(`[{"user":"a"},{"user":"b"}]`)); err != nil {
		t.Fatal(err)
	}
	d.Dataset = DatasetDraft{Enabled: true, Format: "json", File: "data.json"}
	preview, err = PreviewDataset(p, d, 0, 0)
	if err != nil || len(preview.Rows) != 2 || preview.RowsTruncated {
		t.Fatalf("json preview: %+v %v", preview, err)
	}

	d.Dataset = DatasetDraft{Enabled: true, Format: "csv", File: "../escape.csv"}
	if _, err := PreviewDataset(p, d, 0, 0); err == nil {
		t.Fatal("path escape must be rejected")
	}
	outside := filepath.Join(t.TempDir(), "outside.csv")
	if err := os.WriteFile(outside, []byte("a\n1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Dir, "link.csv")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	d.Dataset = DatasetDraft{Enabled: true, Format: "csv", File: "link.csv"}
	if _, err := PreviewDataset(p, d, 0, 0); err == nil {
		t.Fatal("symlink escape must be rejected")
	}
}

// TestSuiteEditListSuites verifies listing is sorted and capped.
func TestSuiteEditListSuites(t *testing.T) {
	p := newTestProject(t)
	for i := 0; i < 3; i++ {
		writeSuite(t, p, fmt.Sprintf("suites/s%d.suite.yaml", i), "schemaVersion: 1\nid: s\nname: S\nsteps:\n  - id: x\n    request: r\n")
	}
	files, skipped, err := ListSuites(p, 2)
	if err != nil || len(files) != 2 || skipped != 1 {
		t.Fatalf("ListSuites: %v %v %v", files, skipped, err)
	}
	if files[0] != "suites/s0.suite.yaml" || files[1] != "suites/s1.suite.yaml" {
		t.Fatalf("not sorted: %v", files)
	}
}
