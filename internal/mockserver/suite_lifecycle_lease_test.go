package mockserver

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"gorequest/internal/core"
)

const leaseFakeSecret = "fake-lease-secret-7c1d"

func leaseProject(t *testing.T) *core.Project {
	t.Helper()
	proj, err := core.NewProject(t.TempDir(), "LeaseProject")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	mf := &MockFile{
		SchemaVersion:   1,
		ID:              "items-mock",
		Name:            "Items Mock",
		DefaultScenario: "default",
		Rules: []MockRule{
			{
				ID:       "get-item",
				Priority: 10,
				Match:    RuleMatch{Method: "GET", Path: "/v1/items/{id}"},
				Response: RuleResponse{
					Status:      200,
					ContentType: "application/json",
					Body:        `{"id":"{{path.id}}","ok":true}`,
				},
			},
		},
	}
	if _, err := SaveMockFile(proj, mf, ""); err != nil {
		t.Fatalf("SaveMockFile: %v", err)
	}
	return proj
}

func TestSuiteMockLeaseBindsLoopbackInjectsAndStops(t *testing.T) {
	proj := leaseProject(t)
	lease, err := StartSuiteMockLease(context.Background(), proj, SuiteMockConfig{
		Port:        0,
		Scenario:    "default",
		Seed:        1,
		EndpointVar: "baseUrl",
	})
	if err != nil {
		t.Fatalf("StartSuiteMockLease: %v", err)
	}
	base := lease.BaseURL()
	port := lease.Port()
	if !strings.HasPrefix(base, "http://127.0.0.1:") || port == 0 {
		t.Fatalf("lease not bound to loopback: base=%q port=%d", base, port)
	}
	if lease.Scenario() != "default" || lease.SnapshotDigest() == "" {
		t.Fatalf("lease missing frozen identity: scenario=%q digest=%q", lease.Scenario(), lease.SnapshotDigest())
	}
	vars := lease.Variables()
	for _, name := range []string{DefaultMockURLVar, DefaultMockBaseVar, "baseUrl"} {
		if vars[name] != base {
			t.Fatalf("variable %s = %q, want %q", name, vars[name], base)
		}
	}
	if vars[DefaultMockPortVar] != strconv.Itoa(port) {
		t.Fatalf("port variable = %q, want %d", vars[DefaultMockPortVar], port)
	}

	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(base + "/v1/items/101")
	if err != nil {
		t.Fatalf("GET mock: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"id":"101"`) {
		t.Fatalf("unexpected mock response: %d %s", resp.StatusCode, body)
	}

	if err := lease.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if !lease.ServerStopped() {
		t.Fatal("lease reported not stopped after a clean Stop")
	}
	if lease.BaseURL() != "" || lease.Port() != 0 {
		t.Fatalf("stopped lease still reports endpoint: %q %d", lease.BaseURL(), lease.Port())
	}
	// Idempotent second stop returns the same clean result.
	if err := lease.Stop(context.Background()); err != nil {
		t.Fatalf("second Stop: %v", err)
	}
	// The port must be free for another listener after release.
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		t.Fatalf("port %d not reusable after Stop: %v", port, err)
	}
	_ = ln.Close()
}

func TestSuiteMockLeaseRedactsFirstAccessRecord(t *testing.T) {
	proj := leaseProject(t)
	red := core.NewRedactor()
	red.RegisterSecret(leaseFakeSecret)
	lease, err := StartSuiteMockLease(context.Background(), proj, SuiteMockConfig{
		Port:     0,
		Scenario: "default",
		Seed:     1,
		Redactor: red,
	})
	if err != nil {
		t.Fatalf("StartSuiteMockLease: %v", err)
	}
	defer func() { _ = lease.Stop(context.Background()) }()

	req, err := http.NewRequest(http.MethodGet, lease.BaseURL()+"/v1/items/7", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Token", leaseFakeSecret)
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	resp.Body.Close()

	records := lease.AccessRecords()
	if len(records) != 1 {
		t.Fatalf("want 1 access record, got %d", len(records))
	}
	for _, pair := range records[0].RequestHeaders {
		if strings.Contains(pair[1], leaseFakeSecret) {
			t.Fatalf("first access record leaked the registered secret in header %q", pair[0])
		}
	}
}

func TestSuiteMockLeasePortConflictDoesNotStealPort(t *testing.T) {
	proj := leaseProject(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	_, err = StartSuiteMockLease(context.Background(), proj, SuiteMockConfig{Port: port, Scenario: "default", Seed: 1})
	var pce *PortConflictError
	if !errors.As(err, &pce) {
		t.Fatalf("want PortConflictError, got %v", err)
	}
	// The external listener must still be usable.
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", port), time.Second)
	if err != nil {
		t.Fatalf("external listener was disturbed: %v", err)
	}
	_ = conn.Close()
}

func TestSuiteMockLeaseUnknownScenarioNoRules(t *testing.T) {
	proj := leaseProject(t)
	snap, err := CompileMockFiles([]*MockFile{{
		SchemaVersion: 1,
		ID:            "scoped",
		Name:          "Scoped",
		Rules: []MockRule{{
			ID:        "only-other",
			Priority:  1,
			Scenarios: []string{"other"},
			Match:     RuleMatch{Method: "GET", Path: "/x"},
			Response:  RuleResponse{Status: 200, Body: "ok"},
		}},
	}}, "special", 1, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileMockFiles: %v", err)
	}
	if len(snap.Rules) != 0 {
		t.Fatalf("expected no rules for scenario special, got %d", len(snap.Rules))
	}
	_, err = StartSuiteMockLease(context.Background(), proj, SuiteMockConfig{Scenario: "special", Seed: 1, Snapshot: snap})
	if err == nil {
		t.Fatal("expected a config error for an empty scenario snapshot")
	}
	var ce *core.ConfigError
	if !errors.As(err, &ce) {
		t.Fatalf("want ConfigError, got %T %v", err, err)
	}
}

func TestSuiteMockLeaseConcurrentStopIsSafe(t *testing.T) {
	proj := leaseProject(t)
	lease, err := StartSuiteMockLease(context.Background(), proj, SuiteMockConfig{Port: 0, Scenario: "default", Seed: 1})
	if err != nil {
		t.Fatalf("StartSuiteMockLease: %v", err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 8)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			errs[i] = lease.Stop(ctx)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent Stop %d: %v", i, err)
		}
	}
	if !lease.ServerStopped() {
		t.Fatal("lease not stopped after concurrent Stop calls")
	}
}
