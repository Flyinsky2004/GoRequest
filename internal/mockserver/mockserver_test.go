package mockserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

func strPtr(s string) *string { return &s }

func TestParseAndSaveMockFileStrict(t *testing.T) {
	raw := []byte(`
schemaVersion: 1
id: users-service
name: Users Service
defaultScenario: default
variables:
  region: ap-east
rules:
  - id: get-user
    name: Get User
    priority: 10
    match:
      method: GET
      path: /v1/users/{id}
    response:
      status: 200
      contentType: application/json
      body: '{"id":"{{path.id}}","region":"{{var.region}}"}'
`)
	mf, err := ParseMockFile(raw)
	if err != nil {
		t.Fatalf("ParseMockFile failed: %v", err)
	}
	if mf.ID != "users-service" || len(mf.Rules) != 1 {
		t.Fatalf("unexpected parsed mock file: %+v", mf)
	}

	// Reject unknown fields strictly
	unknown := []byte(`
schemaVersion: 1
id: bad
name: Bad
unknownField: true
rules: []
`)
	if _, err := ParseMockFile(unknown); err == nil {
		t.Fatalf("expected strict YAML parser to reject unknown field")
	}

	// Save & reload roundtrip inside project
	tmp := t.TempDir()
	proj, err := core.NewProject(tmp, "MockProj")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	digest, err := SaveMockFile(proj, mf, "")
	if err != nil || digest == "" {
		t.Fatalf("SaveMockFile: digest=%q err=%v", digest, err)
	}
	loaded, err := LoadProjectMocks(proj)
	if err != nil {
		t.Fatalf("LoadProjectMocks: %v", err)
	}
	if len(loaded) != 1 || loaded[0].ID != "users-service" || loaded[0].SourceRelPath != "mocks/users-service.mock.yaml" {
		t.Fatalf("unexpected loaded files: %+v", loaded)
	}
}

func TestCompilePriorityAndAmbiguityDetection(t *testing.T) {
	// Overlapping rules at the same priority must fail with AmbiguityError
	ambiguousA := &MockFile{
		SchemaVersion: 1,
		ID:            "file-b",
		Name:          "File B",
		SourceRelPath: "mocks/b.mock.yaml",
		Rules: []MockRule{
			{
				ID:       "rule-beta",
				Priority: 50,
				Match:    RuleMatch{Method: "GET", Path: "/items/{id}"},
				Response: RuleResponse{Status: 200, Body: "beta"},
			},
		},
	}
	ambiguousB := &MockFile{
		SchemaVersion: 1,
		ID:            "file-a",
		Name:          "File A",
		SourceRelPath: "mocks/a.mock.yaml",
		Rules: []MockRule{
			{
				ID:       "rule-alpha",
				Priority: 50,
				Match:    RuleMatch{Method: "GET", Path: "/items/{itemId}"},
				Response: RuleResponse{Status: 200, Body: "alpha"},
			},
		},
	}

	_, err1 := CompileMockFiles([]*MockFile{ambiguousA, ambiguousB}, "default", 99, ServerLimits{})
	_, err2 := CompileMockFiles([]*MockFile{ambiguousB, ambiguousA}, "default", 99, ServerLimits{})
	if err1 == nil || err2 == nil {
		t.Fatalf("expected ambiguity error regardless of input file order, got err1=%v err2=%v", err1, err2)
	}
	var ambErr1 *AmbiguityError
	var ambErr2 *AmbiguityError
	if !errors.As(err1, &ambErr1) || !errors.As(err2, &ambErr2) {
		t.Fatalf("expected *AmbiguityError, got %T / %T", err1, err2)
	}
	if ambErr1.Error() != ambErr2.Error() {
		t.Fatalf("expected deterministic conflict ordering regardless of file load order:\n1: %s\n2: %s", ambErr1.Error(), ambErr2.Error())
	}

	// Raising priority of one rule resolves ambiguity deterministically
	ambiguousB.Rules[0].Priority = 100
	snap1, err := CompileMockFiles([]*MockFile{ambiguousA, ambiguousB}, "default", 99, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileMockFiles after priority adjustment: %v", err)
	}
	snap2, err := CompileMockFiles([]*MockFile{ambiguousB, ambiguousA}, "default", 99, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileMockFiles reverse order: %v", err)
	}
	if snap1.SnapshotDigest != snap2.SnapshotDigest {
		t.Fatalf("expected identical snapshot digest regardless of file order, got %s vs %s", snap1.SnapshotDigest, snap2.SnapshotDigest)
	}

	outcome := snap1.Evaluate(IncomingRequest{
		Seq:    1,
		Method: "GET",
		Path:   "/items/42",
	}, core.NewRedactor())
	if !outcome.Matched || outcome.RuleID != "rule-alpha" || string(outcome.Body) != "alpha" {
		t.Fatalf("expected higher-priority rule-alpha to win, got %+v", outcome)
	}
}

func TestMatchersScenariosTemplatesAnd404NearMiss(t *testing.T) {
	mf := &MockFile{
		SchemaVersion:   1,
		ID:              "orders",
		Name:            "Orders Mock",
		DefaultScenario: "default",
		Variables: map[string]string{
			"env": "staging",
		},
		CORS: &CORSConfig{
			AllowOrigins: []string{"https://app.local"},
			AllowMethods: []string{"GET", "POST"},
			AllowHeaders: []string{"X-Trace-Id", "Content-Type"},
		},
		Rules: []MockRule{
			{
				ID:       "create-order",
				Priority: 20,
				Match: RuleMatch{
					Method: "POST",
					Path:   "/v1/orders/{orderId}",
					PathParams: map[string]ValueMatcher{
						"orderId": {Prefix: strPtr("ord-")},
					},
					Query: []KeyValueMatcher{
						{Name: "dryRun", Equals: strPtr("false")},
					},
					Headers: []KeyValueMatcher{
						{Name: "X-Client", Equals: strPtr("gorequest")},
					},
					Body: &BodyMatcher{
						JSONFields: []JSONFieldMatcher{
							{Path: "$.item.sku", Equals: strPtr("SKU-1")},
							{Path: "$.quantity", Equals: strPtr("2")},
						},
					},
				},
				Response: RuleResponse{
					Status:       201,
					ContentType:  "application/json",
					Body:         `{"orderId":"{{path.orderId}}","env":"{{var.env}}","seq":{{req.seq}},"hex":"{{rand.hex8}}"}`,
					DelayRangeMs: &DelayRange{Min: 5, Max: 25},
				},
				Variants: map[string]RuleResponse{
					"error_500": {
						Status:      500,
						ContentType: "application/json",
						Body:        `{"error":"Order service unavailable","orderId":"{{path.orderId}}"}`,
					},
				},
			},
		},
	}

	snapA, err := CompileMockFiles([]*MockFile{mf}, "default", 777, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileMockFiles: %v", err)
	}
	snapB, err := CompileMockFiles([]*MockFile{mf}, "default", 777, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileMockFiles B: %v", err)
	}

	req := IncomingRequest{
		Seq:     1,
		Method:  "POST",
		Path:    "/v1/orders/ord-900",
		Query:   url.Values{"dryRun": {"false"}},
		Headers: http.Header{"X-Client": {"gorequest"}, "Origin": {"https://app.local"}},
		Body:    []byte(`{"item":{"sku":"SKU-1"},"quantity":2}`),
	}

	red := core.NewRedactor()
	decA := snapA.Evaluate(req, red)
	decB := snapB.Evaluate(req, red)
	if !decA.Matched || decA.Status != 201 {
		t.Fatalf("expected match 201, got %+v", decA)
	}
	if string(decA.Body) != string(decB.Body) || decA.Delay != decB.Delay {
		t.Fatalf("expected deterministic output for identical seed+seq:\nA=%s (%v)\nB=%s (%v)", decA.Body, decA.Delay, decB.Body, decB.Delay)
	}

	// Scenario switch via immutable clone does not mutate snapA
	errSnap, err := snapA.WithScenario("error_500")
	if err != nil {
		t.Fatalf("WithScenario: %v", err)
	}
	decErr := errSnap.Evaluate(req, red)
	if decErr.Status != 500 || !strings.Contains(string(decErr.Body), "Order service unavailable") {
		t.Fatalf("expected error_500 scenario response, got %+v", decErr)
	}
	decStill201 := snapA.Evaluate(req, red)
	if decStill201.Status != 201 {
		t.Fatalf("expected original snapshot scenario to remain intact, got %d", decStill201.Status)
	}

	// Unmatched request returns structured 404 with near-miss diagnostics
	missReq := IncomingRequest{
		Seq:     2,
		Method:  "POST",
		Path:    "/v1/orders/ord-900",
		Query:   url.Values{"dryRun": {"true"}},
		Headers: http.Header{"X-Client": {"gorequest"}},
		Body:    []byte(`{"item":{"sku":"SKU-1"},"quantity":2}`),
	}
	missDec := snapA.Evaluate(missReq, red)
	if missDec.Matched || missDec.Status != http.StatusNotFound || len(missDec.NearMisses) == 0 {
		t.Fatalf("expected 404 with near-miss diagnostics, got %+v", missDec)
	}
}

func TestServerBindPolicyPortConflictRestartAndCancellation(t *testing.T) {
	// 1. Reject non-loopback bind without explicit AllowLAN
	if _, _, _, err := ValidateBindAddress("0.0.0.0", 18080, false); err == nil {
		t.Fatalf("expected 0.0.0.0 to be rejected when AllowLAN is false")
	} else {
		var bindErr *BindPolicyError
		if !errors.As(err, &bindErr) {
			t.Fatalf("expected *BindPolicyError, got %T: %v", err, err)
		}
	}

	// 2. Allow LAN bind only when AllowLAN is true, returning isLAN=true
	_, _, isLAN, err := ValidateBindAddress("0.0.0.0", 18080, true)
	if err != nil || !isLAN {
		t.Fatalf("expected AllowLAN=true to allow 0.0.0.0 with isLAN=true, got err=%v isLAN=%v", err, isLAN)
	}

	// 3. Start server on ephemeral loopback port :0 and verify actual address reporting
	mf := &MockFile{
		SchemaVersion: 1,
		ID:            "ping",
		Name:          "Ping Service",
		Rules: []MockRule{
			{
				ID:       "fast-ping",
				Priority: 10,
				Match:    RuleMatch{Method: "GET", Path: "/ping"},
				Response: RuleResponse{Status: 200, Body: "pong"},
			},
			{
				ID:       "slow-ping",
				Priority: 10,
				Match:    RuleMatch{Method: "GET", Path: "/slow"},
				Response: RuleResponse{Status: 200, Body: "slow-pong", DelayMs: 2000},
			},
		},
	}
	snap, err := CompileMockFiles([]*MockFile{mf}, "default", 1, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileMockFiles: %v", err)
	}

	srv := NewServer(ServerConfig{
		Host: "127.0.0.1",
		Port: 0,
	})
	if err := srv.Start(context.Background(), snap); err != nil {
		t.Fatalf("srv.Start: %v", err)
	}
	defer func() {
		_ = srv.Stop(context.Background())
	}()
	if !srv.IsRunning() || srv.BoundAddr() == "" || srv.Port() == 0 {
		t.Fatalf("expected concrete ephemeral BoundAddr, got addr=%q port=%d", srv.BoundAddr(), srv.Port())
	}

	// 4. Port conflict detection: a second server requesting the same fixed port must fail with PortConflictError
	conflictSrv := NewServer(ServerConfig{
		Host: "127.0.0.1",
		Port: srv.Port(),
	})
	if err := conflictSrv.Start(context.Background(), snap); err == nil {
		_ = conflictSrv.Stop(context.Background())
		t.Fatalf("expected PortConflictError when binding occupied port %d", srv.Port())
	} else {
		var pcErr *PortConflictError
		if !errors.As(err, &pcErr) {
			t.Fatalf("expected *PortConflictError, got %T: %v", err, err)
		}
	}

	// Verify first server is still healthy and untouched
	resp, err := http.Get(srv.BaseURL() + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	bodyBytes, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || string(bodyBytes) != "pong" {
		t.Fatalf("expected 200 pong, got %d %q", resp.StatusCode, string(bodyBytes))
	}

	// 5. Request cancellation during delay terminates promptly
	reqCtx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	slowReq, _ := http.NewRequestWithContext(reqCtx, "GET", srv.BaseURL()+"/slow", nil)
	start := time.Now()
	_, err = http.DefaultClient.Do(slowReq)
	if err == nil {
		t.Fatalf("expected cancelled request to fail")
	}
	if elapsed := time.Since(start); elapsed > 750*time.Millisecond {
		t.Fatalf("expected prompt delay cancellation, took %v", elapsed)
	}

	// 6. Stop releases listener and allows clean restart on the same port
	fixedPort := srv.Port()
	stopCtx, stopCancel := context.WithTimeout(context.Background(), time.Second)
	defer stopCancel()
	if err := srv.Stop(stopCtx); err != nil {
		t.Fatalf("srv.Stop: %v", err)
	}

	restarted := NewServer(ServerConfig{
		Host: "127.0.0.1",
		Port: fixedPort,
	})
	if err := restarted.Start(context.Background(), snap); err == nil {
		if restarted.Port() != fixedPort {
			t.Fatalf("expected restarted server on port %d, got %d", fixedPort, restarted.Port())
		}
		_ = restarted.Stop(context.Background())
	}
}

func TestAccessLogRedactionAndBoundedRingBuffer(t *testing.T) {
	redactor := core.NewRedactor()
	redactor.RegisterSecret("super-secret-value-12345")

	logBuf := NewAccessLog(3, 512)
	for i := 1; i <= 5; i++ {
		rec := AccessRecord{
			Seq:            uint64(i),
			Method:         "POST",
			Path:           "/v1/login",
			SanitizedQuery: "access_token=query-secret-token&page=1",
			RequestHeaders: [][2]string{
				{"Authorization", "Bearer super-secret-value-12345"},
				{"Cookie", "sid=cookie-secret"},
				{"X-Trace", "trace-ok"},
			},
			RequestBodyPreview: `{"username":"alice","password":"my-password-999","token":"super-secret-value-12345"}`,
			Status:             200,
			ResponseHeaders: [][2]string{
				{"Set-Cookie", "sid=cookie-secret; HttpOnly"},
			},
		}
		logBuf.Record(rec, redactor)
	}

	if logBuf.Count() != 3 {
		t.Fatalf("expected bounded capacity 3, got %d", logBuf.Count())
	}
	records := logBuf.Entries()
	// Oldest to newest in ring buffer: seq 3, 4, 5
	if records[0].Seq != 3 || records[2].Seq != 5 {
		t.Fatalf("unexpected ring buffer ordering: %+v", records)
	}
	latest := records[2]
	rawLatest, _ := json.Marshal(latest)
	serialized := string(rawLatest)
	for _, secret := range []string{"super-secret-value-12345", "cookie-secret", "query-secret-token", "my-password-999"} {
		if strings.Contains(serialized, secret) {
			t.Fatalf("access log leaked secret %q in %s", secret, serialized)
		}
	}

	logBuf.Clear()
	if logBuf.Count() != 0 {
		t.Fatalf("expected Clear() to empty access log")
	}
}

func TestBuildRuleFromRequestScrubsSecrets(t *testing.T) {
	redactor := core.NewRedactor()
	redactor.RegisterSecret("top-secret-token-xyz")

	req := &core.Request{
		ID:       "req-1",
		Name:     "Get Profile",
		Protocol: "http",
		HTTP: &core.HTTPDef{
			Method: "POST",
			URL:    "https://api.example.local/v1/users/{{userId}}",
			Query: []core.KV{
				{Name: "api_key", Value: "leak123"},
				{Name: "lang", Value: "zh"},
			},
			Headers: []core.KV{
				{Name: "Authorization", Value: "Bearer top-secret-token-xyz"},
				{Name: "Accept", Value: "application/json"},
			},
		},
	}

	respExample := &core.ExecResult{
		Status: 200,
		Headers: [][2]string{
			{"Content-Type", "application/json"},
			{"Set-Cookie", "session=secret-cookie"},
		},
		Body: []byte(`{"id":42,"access_token":"top-secret-token-xyz","name":"Alice"}`),
	}

	rule, err := BuildRuleFromRequest(req, respExample, BuildRuleOptions{
		IncludeQuery: true,
		Redactor:     redactor,
	})
	if err != nil {
		t.Fatalf("BuildRuleFromRequest: %v", err)
	}

	rawRule, _ := json.Marshal(rule)
	serialized := string(rawRule)
	for _, forbidden := range []string{"top-secret-token-xyz", "leak123", "secret-cookie"} {
		if strings.Contains(serialized, forbidden) {
			t.Fatalf("rule leaked secret %q in %s", forbidden, serialized)
		}
	}
	if rule.Match.Path != "/v1/users/{userId}" || len(rule.Match.Query) != 1 || rule.Match.Query[0].Name != "lang" {
		t.Fatalf("expected sensitive query param stripped and lang preserved, got %+v", rule.Match)
	}
	if _, ok := rule.Variants["error_400"]; !ok {
		t.Fatalf("expected default error_400 scenario variant to be populated")
	}
}

func TestGenerateSchemaExampleAndA03Validation(t *testing.T) {
	schemaMap := map[string]any{
		"type":     "object",
		"required": []any{"id", "email", "tags", "status"},
		"properties": map[string]any{
			"id":     map[string]any{"type": "integer", "minimum": 10},
			"email":  map[string]any{"type": "string", "format": "email"},
			"status": map[string]any{"type": "string", "enum": []any{"active", "pending"}},
			"tags": map[string]any{
				"type":     "array",
				"minItems": 2,
				"items":    map[string]any{"type": "string"},
			},
		},
	}

	body, err := GenerateSchemaExample(schemaMap, schemaMap, 1)
	if err != nil {
		t.Fatalf("GenerateSchemaExample failed: %v", err)
	}
	if !strings.Contains(string(body), `"status": "active"`) || !strings.Contains(string(body), `"user@example.com"`) {
		t.Fatalf("unexpected generated schema body:\n%s", string(body))
	}

	// Unsupported regex pattern without explicit example/default/enum must fail explicitly
	unsupportedSchema := map[string]any{
		"type":     "object",
		"required": []any{"code"},
		"properties": map[string]any{
			"code": map[string]any{"type": "string", "pattern": "^[A-Z]{3}-[0-9]{4}$"},
		},
	}
	if _, err := GenerateSchemaExample(unsupportedSchema, unsupportedSchema, 1); err == nil {
		t.Fatalf("expected SchemaExampleError for unsupported regex pattern without explicit example")
	} else {
		var schemaErr *SchemaExampleError
		if !errors.As(err, &schemaErr) {
			t.Fatalf("expected *SchemaExampleError, got %T: %v", err, err)
		}
	}

	// BuildRuleFromOperationContract succeeds with A03-validated schema
	req := &core.Request{
		ID:       "get-user",
		Name:     "Get User",
		Protocol: "http",
		HTTP: &core.HTTPDef{
			Method: "GET",
			URL:    "{{baseUrl}}/v1/users/{id}",
		},
	}
	if err := core.SetRequestOpenAPI(req, &core.OperationContract{
		OperationID:  "getUser",
		PathTemplate: "/v1/users/{id}",
		Responses: map[string]core.ResponseContract{
			"200": {
				Description: "OK",
				Content: map[string]core.MediaTypeContract{
					"application/json": {
						Schema: schemaMap,
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("SetRequestOpenAPI: %v", err)
	}

	rule, err := BuildRuleFromOperationContract(req, "200", 25, 1)
	if err != nil {
		t.Fatalf("BuildRuleFromOperationContract: %v", err)
	}
	if rule.Response.Status != 200 || rule.Match.Path != "/v1/users/{id}" {
		t.Fatalf("unexpected rule from operation contract: %+v", rule)
	}
}

func TestRunSuiteWithControlledMockAndScenarioIsolation(t *testing.T) {
	tmp := t.TempDir()
	proj, err := core.NewProject(tmp, "SuiteMockProj")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	// Create and save a request that uses {{mockUrl}}/v1/items/101
	entry, err := proj.NewRequest("", "Get Item 101")
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	entry.Request.HTTP.Method = "GET"
	entry.Request.HTTP.URL = "{{mockUrl}}/v1/items/101"
	if _, err := proj.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	// Save a mock file into .gorequest/mocks/
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
					Body:        `{"id":"{{path.id}}","status":"ok"}`,
				},
				Variants: map[string]RuleResponse{
					"error_500": {
						Status:      500,
						ContentType: "application/json",
						Body:        `{"error":"boom"}`,
					},
				},
			},
		},
	}
	if _, err := SaveMockFile(proj, mf, ""); err != nil {
		t.Fatalf("SaveMockFile: %v", err)
	}

	// Start an interactive panel server whose scenario is switched mid-flight to verify isolation
	snap, err := CompileProjectMocks(proj, "default", 1, ServerLimits{})
	if err != nil {
		t.Fatalf("CompileProjectMocks: %v", err)
	}
	panelSrv := NewServer(ServerConfig{Host: "127.0.0.1", Port: 0})
	if err := panelSrv.Start(context.Background(), snap); err != nil {
		t.Fatalf("panelSrv.Start: %v", err)
	}
	defer func() { _ = panelSrv.Stop(context.Background()) }()

	// Switching panel server to error_500 must not affect ControlledSuiteMock started with frozen snap ("default")
	errSnap, err := snap.WithScenario("error_500")
	if err != nil {
		t.Fatalf("WithScenario: %v", err)
	}
	if err := panelSrv.SwapSnapshot(errSnap); err != nil {
		t.Fatalf("SwapSnapshot: %v", err)
	}

	suite := &runner.Suite{
		SchemaVersion: 1,
		ID:            "suite-items",
		Name:          "Items Suite",
		Steps: []runner.SuiteStep{
			{
				ID:      "step-1",
				Name:    "Fetch item 101",
				Request: entry.Request.ID,
			},
		},
	}

	outcome, err := RunSuiteWithControlledMock(
		context.Background(),
		proj,
		suite,
		SuiteMockConfig{
			Scenario:    "default",
			Seed:        42,
			EndpointVar: "baseUrl",
			Snapshot:    snap,
		},
		runner.Options{},
	)
	if err != nil {
		t.Fatalf("RunSuiteWithControlledMock: %v", err)
	}
	if outcome == nil || outcome.Result == nil || outcome.Result.Status != runner.StatusPassed {
		t.Fatalf("expected suite to pass against controlled mock, got outcome=%+v", outcome)
	}
	if !outcome.ServerStopped || len(outcome.AccessRecords) != 1 || outcome.AccessRecords[0].MatchedRuleID != "get-item" {
		t.Fatalf("expected stopped controlled server and 1 access log record for get-item, got %+v", outcome)
	}

	// Ensure suite.Variables was not mutated in-place
	if _, mutated := suite.Variables["mockUrl"]; mutated {
		t.Fatalf("expected original suite.Variables not to be mutated")
	}

	// Verify body size limit returns 413 Request Entity Too Large
	smallSnap, err := CompileMockFiles([]*MockFile{
		{
			SchemaVersion: 1,
			ID:            "tiny",
			Name:          "Tiny",
			Rules: []MockRule{
				{
					ID:       "upload",
					Priority: 1,
					Match:    RuleMatch{Method: "POST", Path: "/upload"},
					Response: RuleResponse{Status: 200, Body: "ok"},
				},
			},
		},
	}, "default", 1, ServerLimits{MaxRequestBodyBytes: 16})
	if err != nil {
		t.Fatalf("CompileMockFiles tiny: %v", err)
	}
	tinySrv := NewServer(ServerConfig{
		Host:   "127.0.0.1",
		Port:   0,
		Limits: ServerLimits{MaxRequestBodyBytes: 16},
	})
	if err := tinySrv.Start(context.Background(), smallSnap); err != nil {
		t.Fatalf("tinySrv.Start: %v", err)
	}
	defer func() { _ = tinySrv.Stop(context.Background()) }()

	resp, err := http.Post(tinySrv.BaseURL()+"/upload", "text/plain", bytes.NewReader([]byte("this payload exceeds sixteen bytes")))
	if err != nil {
		t.Fatalf("POST /upload: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected 413 for oversized body, got %d", resp.StatusCode)
	}

	// Verify no runtime artifacts leaked into .gorequest/environments
	entries, err := os.ReadDir(filepath.Join(proj.Dir, "environments"))
	if err != nil {
		t.Fatalf("ReadDir environments: %v", err)
	}
	if len(entries) != 1 { // only default dev.yaml created by NewProject
		t.Fatalf("expected only dev.yaml in environments, found %d", len(entries))
	}
}
