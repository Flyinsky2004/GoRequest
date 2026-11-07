package desktop

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/mockserver"
	"gorequest/internal/runner"
)

func TestMockModalDoesNotAutoStartAndCreatesRuleFromActiveRequest(t *testing.T) {
	a, ps := openTestProject(t, "Mock Panel Basic")
	if st := a.mockState(false); st != nil && st.running {
		t.Fatal("opening a project must never auto-start the mock server")
	}

	tabs := openRequestTabs(t, ps, "one")
	tab := tabs[0]
	tab.Entry.Request.HTTP.Method = "GET"
	tab.Entry.Request.HTTP.URL = "{{baseUrl}}/v1/profile/{{userId}}"
	tab.Entry.Request.HTTP.Query = []core.KV{
		{Name: "access_token", Value: "secret-query-token"},
		{Name: "format", Value: "json"},
	}
	tab.Resp = &core.ExecResult{
		Status: 200,
		Headers: [][2]string{
			{"Content-Type", "application/json"},
			{"Set-Cookie", "sid=secret-session-cookie"},
		},
		Body: []byte(`{"id":"u-1","password":"super-secret-password","role":"admin"}`),
	}

	a.OpenMockServer()
	s := a.mockState(false)
	if s == nil || !s.open || s.running {
		t.Fatalf("unexpected initial mock state: %+v", s)
	}

	a.CreateMockRuleFromActiveRequest()
	if s.errMsg != "" {
		t.Fatalf("CreateMockRuleFromActiveRequest failed: %s", s.errMsg)
	}
	if s.snapshot == nil || len(s.snapshot.Rules) != 1 {
		t.Fatalf("expected 1 compiled rule, got %+v", s.snapshot)
	}

	// Ensure saved .mock.yaml scrubbed all secrets
	rawBytes, _, err := ps.project.Store.Read("mocks/workspace-mocks.mock.yaml")
	if err != nil {
		t.Fatalf("read saved mock file: %v", err)
	}
	savedText := string(rawBytes)
	for _, secret := range []string{"secret-query-token", "secret-session-cookie", "super-secret-password"} {
		if strings.Contains(savedText, secret) {
			t.Fatalf("saved mock file leaked secret %q:\n%s", secret, savedText)
		}
	}

	tt := ui.NewTester(func(c *ui.Context) { a.MockModals(c, c.Theme()) }, 1280, 900)
	tt.Frame()
	if !tt.HasText(mockT(a, "mock.modal.title")) || !tt.HasText(mockT(a, "mock.action.start")) {
		t.Fatal("MockModals did not render title or start button")
	}
	if !tt.HasText("GET /v1/profile/{userId}") {
		t.Fatal("MockModals did not render created rule path template")
	}
}

func TestMockPanelDisplaysAmbiguityConflictAndBlocksStart(t *testing.T) {
	a, ps := openTestProject(t, "Mock Conflict UI")
	mf := &mockserver.MockFile{
		SchemaVersion: 1,
		ID:            "conflict-file",
		Name:          "Conflict Rules",
		Rules: []mockserver.MockRule{
			{
				ID:       "rule-1",
				Priority: 50,
				Match:    mockserver.RuleMatch{Method: "GET", Path: "/orders/{id}"},
				Response: mockserver.RuleResponse{Status: 200, Body: "one"},
			},
			{
				ID:       "rule-2",
				Priority: 50,
				Match:    mockserver.RuleMatch{Method: "GET", Path: "/orders/{orderId}"},
				Response: mockserver.RuleResponse{Status: 200, Body: "two"},
			},
		},
	}
	if _, err := mockserver.SaveMockFile(ps.project, mf, ""); err != nil {
		t.Fatalf("SaveMockFile: %v", err)
	}

	a.OpenMockServer()
	s := a.mockState(false)
	if len(s.conflicts) == 0 || s.errMsg == "" {
		t.Fatalf("expected ambiguity conflicts to be populated, got conflicts=%v errMsg=%q", s.conflicts, s.errMsg)
	}

	a.StartMockServer()
	if s.running {
		a.StopMockServer()
		t.Fatal("StartMockServer must not start when same-priority rule ambiguity exists")
	}

	tt := ui.NewTester(func(c *ui.Context) { a.MockModals(c, c.Theme()) }, 1280, 900)
	tt.Frame()
	if !tt.HasText(mockT(a, "mock.status.conflict")) {
		t.Fatal("MockModals did not render RULE CONFLICT status badge")
	}
}

func TestMockPanelStartStopScenarioSwitchAccessLogAndCleanup(t *testing.T) {
	a, ps := openTestProject(t, "Mock Start Stop")
	w := &queuedTestWindow{}
	a.win = w

	mf := &mockserver.MockFile{
		SchemaVersion: 1,
		ID:            "live-service",
		Name:          "Live Service",
		Rules: []mockserver.MockRule{
			{
				ID:       "health",
				Priority: 10,
				Match:    mockserver.RuleMatch{Method: "GET", Path: "/v1/health"},
				Response: mockserver.RuleResponse{
					Status:      200,
					ContentType: "application/json",
					Body:        `{"status":"ok"}`,
				},
				Variants: map[string]mockserver.RuleResponse{
					"error_500": {
						Status:      500,
						ContentType: "application/json",
						Body:        `{"status":"degraded"}`,
					},
				},
			},
		},
	}
	if _, err := mockserver.SaveMockFile(ps.project, mf, ""); err != nil {
		t.Fatalf("SaveMockFile: %v", err)
	}

	a.OpenMockServer()
	a.StartMockServer()
	s := a.mockState(false)
	if !s.running || s.baseURL == "" {
		t.Fatalf("expected mock server to be running on ephemeral port, got %+v", s)
	}
	defer a.StopMockServer()

	req, _ := http.NewRequest("GET", s.baseURL+"/v1/health?token=secret-q", nil)
	req.Header.Set("Authorization", "Bearer header-secret-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}

	w.drainUI()
	if len(s.accessLog) != 1 {
		t.Fatalf("expected 1 sanitized access log entry, got %d", len(s.accessLog))
	}
	if strings.Contains(s.accessLog[0].SanitizedQuery, "secret-q") {
		t.Fatalf("access log leaked query token: %+v", s.accessLog[0])
	}

	// Switch scenario to error_500
	a.SetMockScenario("error_500")
	resp2, err := http.Get(s.baseURL + "/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health after scenario switch: %v", err)
	}
	_ = resp2.Body.Close()
	if resp2.StatusCode != 500 {
		t.Fatalf("expected 500 after switching to error_500, got %d", resp2.StatusCode)
	}

	a.ClearMockAccessLog()
	if len(s.accessLog) != 0 {
		t.Fatalf("expected ClearMockAccessLog to empty s.accessLog")
	}

	a.StopMockServer()
	if s.running || s.baseURL != "" {
		t.Fatalf("expected server stopped, got %+v", s)
	}
}

func TestMockPanelOpenAPISchemaRuleAndControlledSuiteRun(t *testing.T) {
	a, ps := openTestProject(t, "Mock Controlled Suite")
	w := &queuedTestWindow{}
	a.win = w

	tabs := openRequestTabs(t, ps, "one")
	req := tabs[0].Entry.Request
	req.HTTP.Method = "GET"
	req.HTTP.URL = "{{baseUrl}}/v1/users/100"
	if err := core.SetRequestOpenAPI(req, &core.OperationContract{
		OperationID:  "getUserById",
		PathTemplate: "/v1/users/{id}",
		Responses: map[string]core.ResponseContract{
			"200": {
				Description: "User response",
				Content: map[string]core.MediaTypeContract{
					"application/json": {
						Schema: map[string]any{
							"type":     "object",
							"required": []any{"id", "username"},
							"properties": map[string]any{
								"id":       map[string]any{"type": "string", "const": "100"},
								"username": map[string]any{"type": "string", "example": "alice"},
							},
						},
					},
				},
			},
		},
	}); err != nil {
		t.Fatalf("SetRequestOpenAPI: %v", err)
	}
	if _, err := ps.project.SaveRequest(tabs[0].Entry, req, tabs[0].Entry.BaselineDigest); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}

	suiteYAML := []byte("schemaVersion: 1\nid: mock-suite\nname: Mock Suite\nsteps:\n  - id: step1\n    request: " + req.ID + "\n")
	if err := ps.project.Store.WriteAtomic("suites/mock.suite.yaml", suiteYAML); err != nil {
		t.Fatalf("WriteAtomic suite: %v", err)
	}

	a.OpenMockServer()
	a.CreateMockRuleFromActiveOpenAPI()
	s := a.mockState(false)
	if s.errMsg != "" {
		t.Fatalf("CreateMockRuleFromActiveOpenAPI: %s", s.errMsg)
	}

	a.StartControlledMockSuite()
	deadline := time.Now().Add(5 * time.Second)
	for s.suiteRunning && time.Now().Before(deadline) {
		w.drainUI()
		time.Sleep(time.Millisecond)
	}
	w.drainUI()
	if s.suiteRunning {
		t.Fatal("controlled mock suite did not finish within deadline")
	}
	if s.suiteOutcome == nil || s.suiteOutcome.Result == nil || s.suiteOutcome.Result.Status != runner.StatusPassed {
		t.Fatalf("expected controlled suite run to pass, got outcome=%+v err=%q", s.suiteOutcome, s.errMsg)
	}
	if !s.suiteOutcome.ServerStopped {
		t.Fatal("expected controlled suite mock server to be stopped after run")
	}
}
