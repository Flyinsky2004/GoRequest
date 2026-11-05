package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"gorequest/internal/core"
)

func TestRunEndToEnd(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"path":"` + r.URL.Path + `"}`))
	}))
	defer srv.Close()

	dir := t.TempDir()
	p, err := core.NewProject(dir, "E2E")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.HTTP.URL = srv.URL + "/ping"
	if _, err := p.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}

	if code := run([]string{"validate", "--project", dir, "--json"}); code != exitOK {
		t.Fatalf("validate exit = %d, want 0", code)
	}
	if code := run([]string{"run", "--project", dir, "--request", entry.Request.ID, "--json"}); code != exitOK {
		t.Fatalf("run exit = %d, want 0", code)
	}
}

func TestRunMissingRequestIsConfigError(t *testing.T) {
	dir := t.TempDir()
	if _, err := core.NewProject(dir, "E2E"); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"run", "--project", dir, "--request", "nope"}); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
}

func TestRunNetworkFailureIsRunError(t *testing.T) {
	dir := t.TempDir()
	p, err := core.NewProject(dir, "E2E")
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewRequest("", "Bad")
	if err != nil {
		t.Fatal(err)
	}
	// Reserved TEST-NET address that will refuse/fail to connect quickly.
	entry.Request.HTTP.URL = "http://127.0.0.1:1/none"
	if _, err := p.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatal(err)
	}
	if code := run([]string{"run", "--project", dir, "--request", entry.Request.ID}); code != exitRun {
		t.Fatalf("exit = %d, want %d", code, exitRun)
	}
}

func TestValidateBadProject(t *testing.T) {
	if code := run([]string{"validate", "--project", t.TempDir()}); code != exitConfig {
		t.Fatalf("exit = %d, want %d", code, exitConfig)
	}
}
