package sdkgen

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestGeneratedModuleCompilesAndCallsLoopback writes the generated SDK to a
// temporary module, adds a real test, and runs `go test` with networking and
// the workspace disabled. The generated test drives a loopback server so the
// method, path, query, headers and body are verified end to end.
func TestGeneratedModuleCompilesAndCallsLoopback(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain is not available")
	}
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	testSrc := `package petapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	petapi "example.com/petapi"
)

func ptr[T any](v T) *T { return &v }

func TestGeneratedClient(t *testing.T) {
	var gotMethod, gotPath, gotQuery, gotTrace, gotCT, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.EscapedPath()
		gotQuery = r.URL.RawQuery
		gotTrace = r.Header.Get("X-Trace")
		gotCT = r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		if r.URL.Path == "/pets/missing" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte("{\"error\":\"missing\"}"))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"ok\":true}"))
	}))
	defer srv.Close()

	c, err := petapi.NewClient(srv.URL)
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	resp, err := c.GetPet(context.Background(), &petapi.GetPetInput{
		PetId:  ptr("a/b"),
		Limit:  ptr(int64(5)),
		Tags:   []string{"x", "y"},
		XTrace: ptr("trace-1"),
	})
	if err != nil {
		t.Fatalf("GetPet: %v", err)
	}
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if gotMethod != "GET" {
		t.Fatalf("method = %q", gotMethod)
	}
	if gotPath != "/pets/a%2Fb" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotQuery != "limit=5&tags=x&tags=y" {
		t.Fatalf("query = %q", gotQuery)
	}
	if gotTrace != "trace-1" {
		t.Fatalf("trace = %q", gotTrace)
	}
	var decoded map[string]any
	if err := resp.JSON(&decoded); err != nil || decoded["ok"] != true {
		t.Fatalf("JSON decode: %v %v", err, decoded)
	}

	if _, err := c.UpdatePet(context.Background(), &petapi.UpdatePetInput{
		PetId: ptr("p1"),
		Body:  json.RawMessage("{\"name\":\"n\"}"),
	}); err != nil {
		t.Fatalf("UpdatePet: %v", err)
	}
	if gotMethod != "POST" || gotPath != "/pets/p1" {
		t.Fatalf("update method/path = %q %q", gotMethod, gotPath)
	}
	if gotCT != "application/json" {
		t.Fatalf("content-type = %q", gotCT)
	}
	if gotBody != "{\"name\":\"n\"}" {
		t.Fatalf("body = %q", gotBody)
	}

	if _, err := c.GetSearch(context.Background(), &petapi.GetSearchInput{Q: ptr("hello world")}); err != nil {
		t.Fatalf("GetSearch: %v", err)
	}
	if gotQuery != "q=hello+world" {
		t.Fatalf("search query = %q", gotQuery)
	}

	// Required validation happens before any request.
	if _, err := c.GetSearch(context.Background(), &petapi.GetSearchInput{}); err == nil {
		t.Fatalf("expected a required-parameter error")
	}

	// Non-2xx keeps the real status and bounded body.
	if _, err := c.GetPet(context.Background(), &petapi.GetPetInput{PetId: ptr("missing")}); err == nil {
		t.Fatalf("expected an APIError")
	} else {
		var apiErr *petapi.APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("error is not *APIError: %v", err)
		}
		if apiErr.StatusCode != 404 || string(apiErr.Body) != "{\"error\":\"missing\"}" {
			t.Fatalf("APIError = %d %q", apiErr.StatusCode, apiErr.Body)
		}
	}

	// Cancellation propagates.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetPet(ctx, &petapi.GetPetInput{PetId: ptr("p1")}); err == nil {
		t.Fatalf("expected a cancellation error")
	}

	// Header values with CRLF are rejected before the request is sent.
	if _, err := c.GetPet(context.Background(), &petapi.GetPetInput{
		PetId:  ptr("p1"),
		XTrace: ptr("bad\r\nInjected: 1"),
	}); err == nil {
		t.Fatalf("expected a header injection error")
	}
}
`
	if err := os.WriteFile(filepath.Join(opts.TargetDir, "client_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatalf("write generated test: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = opts.TargetDir
	cmd.Env = append(os.Environ(),
		"GOWORK=off",
		"GOPROXY=off",
		"GOSUMDB=off",
		"GOTOOLCHAIN=local",
		"GOFLAGS=-mod=mod",
		"CGO_ENABLED=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module failed to test: %v\n%s", err, out)
	}
	if len(out) > 0 {
		t.Logf("generated module test output:\n%s", out)
	}
}
