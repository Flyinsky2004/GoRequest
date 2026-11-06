package desktop

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
)

// These scenarios exercise the public entry points after combining the four
// deliveries, rather than testing each branch's isolated adapter again.
func TestMergedBodyAuthAndSyncedSave(t *testing.T) {
	dir := t.TempDir()
	if _, err := core.NewProject(dir, "Merged"); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t)
	m.secrets = core.MapSecrets{"key": "integration-secret-927"}
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	t.Cleanup(a.StopFileSync)
	entry, err := a.ps.createRequestIn("", "Multipart")
	if err != nil {
		t.Fatal(err)
	}
	a.ps.project.AuthSchemes["first"] = &core.AuthScheme{Type: "apikey", In: "header", Name: "X-First", Value: "{{secret.key}}"}
	a.ps.project.AuthSchemes["second"] = &core.AuthScheme{Type: "apikey", In: "header", Name: "X-Second", Value: "{{secret.key}}"}
	entry.Request.Auth = &core.AuthRef{Mode: "ref", Ref: "first"}
	tab := a.ps.activeTab()
	fillAuth(a.ps.project, tab, entry.Request)
	tab.AuthRefID = "second"
	tab.Method = "POST"
	tab.BodyText = ""
	body, err := core.ParseBodyText("urlencoded", "name=hello+world&token={{secret.key}}")
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.HTTP.Body = body
	tab.BodyText = core.BodyEditText(body)
	tab.resetBodyState()
	tab.Dirty = true
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	if entry.Request.Auth.Ref != "second" {
		t.Fatalf("synced save reverted edited ref: %+v", entry.Request.Auth)
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Second") != "integration-secret-927" || r.Header.Get("X-First") != "" {
			t.Error("wrong auth on wire")
		}
		if err := r.ParseForm(); err != nil {
			t.Error(err)
		}
		if r.Form.Get("name") != "hello world" || r.Form.Get("token") != "integration-secret-927" {
			t.Errorf("form=%v", r.Form)
		}
		w.WriteHeader(204)
	}))
	defer srv.Close()
	tab.URL = srv.URL
	_, plan, red, err := a.buildTrackedTabPlan(context.Background(), tab)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := core.Execute(context.Background(), plan, nil); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(red.RedactString(string(plan.Body)), "integration-secret-927") {
		t.Fatal("complex body secret not tracked")
	}
	p, err := core.OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if p.Requests[entry.Request.ID].Request.HTTP.Body.Type != "urlencoded" {
		t.Fatal("body lost on reopen")
	}
}

func TestMergedSyncRefreshesFolderAuthAndDocumentSearch(t *testing.T) {
	dir := t.TempDir()
	if _, err := core.NewProject(dir, "Merged"); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(dir, ".gorequest")
	if err := os.MkdirAll(filepath.Join(root, "collections/group"), 0755); err != nil {
		t.Fatal(err)
	}
	write := func(rel, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, rel), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	collection := "schemaVersion: 1\nid: group\nname: Group\nauth:\n  type: bearer\n  token: before\n"
	write("collections/group/collection.yaml", collection)
	write("collections/group/a.request.yaml", "schemaVersion: 1\nid: nested\nname: Nested\nhttp:\n  method: GET\n  url: https://example.com\n")
	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	t.Cleanup(a.StopFileSync)
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	eff, err := a.ps.project.ResolveEffectiveAuth(a.ps.project.Requests["nested"], nil)
	if err != nil || eff.Scheme == nil || eff.Scheme.Token != "before" {
		t.Fatalf("initial folder auth: %+v, %v", eff, err)
	}
	write("collections/group/collection.yaml", strings.ReplaceAll(collection, "before", "after"))
	write("docs/new.md", "# New\nneedle after refresh\n")
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	eff, err = a.ps.project.ResolveEffectiveAuth(a.ps.project.Requests["nested"], nil)
	if err != nil || eff.Scheme == nil || eff.Scheme.Token != "after" {
		t.Fatalf("refreshed folder auth: %+v, %v", eff, err)
	}
	if err := a.ps.openDoc("docs/new.md"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.ps.activeTab().DocEditor.Buffer.String(), "needle") {
		t.Fatal("new document not exposed after refresh")
	}
}

func TestMergedManagerStartsAndStopsFileWatcher(t *testing.T) {
	dir := t.TempDir()
	if _, err := core.NewProject(dir, "Watch"); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t)
	m.watchFiles = true
	var win *queuedTestWindow
	m.newWindow = func(s windowSpec) window { win = newQueuedTestWindow(s.Title); return win }
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	defer a.StopFileSync()
	ctrl := peekFileSync(a.ps)
	if ctrl == nil || ctrl.watcher == nil {
		t.Fatal("manager did not start file watcher")
	}
	if err := os.WriteFile(filepath.Join(dir, ".gorequest/docs/external.md"), []byte("# External\n"), 0644); err != nil {
		t.Fatal(err)
	}
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for {
		found := false
		for _, doc := range a.ps.project.Docs {
			if doc.RelPath == "docs/external.md" {
				found = true
			}
		}
		if found {
			break
		}
		select {
		case <-win.notifyCh:
			win.drainUI()
		case <-timer.C:
			t.Fatal("external document was not refreshed through manager watcher")
		}
	}
	win.Close()
	if peekFileSync(a.ps) != nil {
		t.Fatal("closing project left sync controller alive")
	}
	ctrl.watcherMu.Lock()
	active := ctrl.watcher != nil
	ctrl.watcherMu.Unlock()
	if active {
		t.Fatal("closing project left watcher alive")
	}
}

func TestMergedEnvironmentDraftRetainsOpeningBaselineAcrossRefresh(t *testing.T) {
	dir := t.TempDir()
	p, err := core.NewProject(dir, "Environment")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.SaveEnvironment("dev", &core.Environment{SchemaVersion: 1, ID: "dev", Variables: map[string]string{"base": "before"}}); err != nil {
		t.Fatal(err)
	}
	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	defer a.StopFileSync()
	if err := a.InitFileSync(); err != nil {
		t.Fatal(err)
	}
	if err := a.ps.startEditEnvironment(); err != nil {
		t.Fatal(err)
	}
	draft := a.ps.envUI
	draft.varRows[0].Value = "unsaved"
	rel := a.ps.project.EnvEntries["dev"].RelPath
	abs := filepath.Join(dir, ".gorequest", rel)
	external := "schemaVersion: 1\nid: dev\nvariables:\n  base: external\n"
	if err := os.WriteFile(abs, []byte(external), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SyncNow(); err != nil {
		t.Fatal(err)
	}
	if err := a.saveEnvironmentDraft(a.ps); !core.IsConflict(err) {
		t.Fatalf("draft overwrote refreshed environment: %v", err)
	}
	got, err := os.ReadFile(abs)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != external || !draft.open || draft.varRows[0].Value != "unsaved" {
		t.Fatal("environment conflict did not preserve both sides")
	}
}
