package runner

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"gorequest/internal/core"
)

type fakeAuthProvider struct {
	preflightCalls atomic.Int32
	applyCalls     atomic.Int32
	preflightErr   error
	applyErr       error
	token          string
	lastPreflight  AuthRequest
}

func (f *fakeAuthProvider) PreflightAuth(_ context.Context, req AuthRequest) error {
	f.preflightCalls.Add(1)
	f.lastPreflight = req
	return f.preflightErr
}

func (f *fakeAuthProvider) ApplyAuth(_ context.Context, plan *core.Plan, req AuthRequest) (AuthResult, error) {
	f.applyCalls.Add(1)
	if f.applyErr != nil {
		return AuthResult{}, f.applyErr
	}
	if plan != nil {
		plan.Headers = append(plan.Headers, [2]string{"Authorization", "Bearer " + f.token})
	}
	req.Redactor.RegisterSecret(f.token)
	return AuthResult{Secrets: []string{f.token}}, nil
}

func oauthRequestYAML(id, grant string) string {
	return reqYAML(id, "GET", "/protected", `auth:
  type: oauth2
  oauth:
    grantType: `+grant+`
    clientId: fixture-client
    clientSecret: "{{secret.client}}"
    tokenUrl: http://127.0.0.1:1/token
`)
}

func TestAuthProviderInjectedIntoPreflightAndAttempt(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{
		"collections/a/r.request.yaml": oauthRequestYAML("r", "client_credentials"),
	})
	suite, err := ParseSuite(suiteYAML("steps:\n  - {id: r, request: r}\n"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeAuthProvider{token: "SECRET_runner_token_719"}
	var calls atomic.Int32
	opts := Options{
		Secrets: core.MapSecrets{"client": "SECRET_client_442"},
		Auth:    provider,
		Executor: execFunc(func(_ context.Context, plan *core.Plan, _ core.ExecuteOptions) (*core.ExecResult, error) {
			calls.Add(1)
			auth := ""
			for _, h := range plan.Headers {
				if strings.EqualFold(h[0], "Authorization") {
					auth = h[1]
				}
			}
			if auth != "Bearer SECRET_runner_token_719" {
				t.Errorf("token not injected before execution: %q", auth)
			}
			return fixtureResult(`{"ok":true}`, 200), nil
		}),
	}
	result, err := New(p, suite, opts).Run(context.Background())
	if err != nil || result.Status != StatusPassed {
		t.Fatalf("%+v %v", result, err)
	}
	if calls.Load() != 1 || provider.preflightCalls.Load() != 1 || provider.applyCalls.Load() != 1 {
		t.Fatalf("calls=%d preflight=%d apply=%d", calls.Load(), provider.preflightCalls.Load(), provider.applyCalls.Load())
	}
	if provider.lastPreflight.SchemeID != "inline:collections/a/r.request.yaml" {
		t.Fatalf("inline scheme id = %q", provider.lastPreflight.SchemeID)
	}
	report, err := MarshalJSONReport(result)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"SECRET_runner_token_719", "SECRET_client_442"} {
		if strings.Contains(string(report), secret) {
			t.Fatalf("secret %q leaked into report", secret)
		}
	}
}

func TestAuthPreflightFailureSendsNothing(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{
		"collections/a/r.request.yaml": oauthRequestYAML("r", "authorization_code"),
	})
	suite, err := ParseSuite(suiteYAML("steps:\n  - {id: r, request: r}\n"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeAuthProvider{preflightErr: &core.OAuthError{
		Category: core.OAuthErrInteractionRequired,
		Message:  "sign-in required",
	}}
	var calls atomic.Int32
	opts := Options{
		Secrets: core.MapSecrets{"client": "SECRET_client_442"},
		Auth:    provider,
		Executor: execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
			calls.Add(1)
			return fixtureResult(`{}`, 200), nil
		}),
	}
	result, err := New(p, suite, opts).Run(context.Background())
	if err == nil || result.Status != StatusConfigError || calls.Load() != 0 || provider.applyCalls.Load() != 0 {
		t.Fatalf("%+v %v calls=%d apply=%d", result, err, calls.Load(), provider.applyCalls.Load())
	}
	if result.Failure == nil || result.Failure.Category != FailureConfig || result.Failure.Kind != KindAuth {
		t.Fatalf("failure classification = %+v", result.Failure)
	}
	if !strings.Contains(result.Failure.Message, "sign-in required") {
		t.Fatalf("diagnostic missing: %q", result.Failure.Message)
	}
}

func TestAuthApplyFailureSkipsBusinessRequestAndRetry(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{
		"collections/a/r.request.yaml": oauthRequestYAML("r", "client_credentials"),
	})
	suite, err := ParseSuite(suiteYAML("steps:\n  - id: r\n    request: r\n    retry: {maxAttempts: 3, on: [network]}\n"))
	if err != nil {
		t.Fatal(err)
	}
	provider := &fakeAuthProvider{applyErr: &core.OAuthError{
		Category:   core.OAuthErrTokenExchangeFailed,
		StatusCode: 503,
		Message:    "token endpoint unavailable",
	}}
	var calls atomic.Int32
	opts := Options{
		Secrets: core.MapSecrets{"client": "SECRET_client_442"},
		Auth:    provider,
		Executor: execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
			calls.Add(1)
			return fixtureResult(`{}`, 200), nil
		}),
	}
	result, err := New(p, suite, opts).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 0 || provider.applyCalls.Load() != 1 {
		t.Fatalf("business calls=%d apply=%d", calls.Load(), provider.applyCalls.Load())
	}
	step := result.Rows[0].Steps[0]
	if step.Status != StatusFailed || step.Failure == nil || step.Failure.Category != FailureRun || step.Failure.Kind != KindAuth {
		t.Fatalf("step = %+v", step)
	}
	if len(step.Attempts) != 1 {
		t.Fatalf("auth failure was retried: %d attempts", len(step.Attempts))
	}
}

type fakeProtocol struct {
	supports   func(*core.Request) bool
	preflight  error
	execErr    error
	calls      atomic.Int32
	preflights atomic.Int32
}

func (f *fakeProtocol) Supports(req *core.Request) bool {
	if f.supports != nil {
		return f.supports(req)
	}
	return true
}
func (f *fakeProtocol) PreflightProtocol(context.Context, ProtocolRequest) error {
	f.preflights.Add(1)
	return f.preflight
}
func (f *fakeProtocol) ExecuteProtocol(_ context.Context, req ProtocolRequest) (*core.ExecResult, error) {
	f.calls.Add(1)
	if f.execErr != nil {
		return nil, f.execErr
	}
	return fixtureResult(`{"message":"hello"}`, 101, [2]string{"Content-Type", "application/json"}), nil
}

func wsRequestYAML() string {
	return `schemaVersion: 1
id: ws
name: ws
protocol: websocket
http:
  method: WS
  url: "ws://127.0.0.1:1/socket"
ws:
  subprotocols: [chat]
assert:
  - status: 101
`
}

func TestProtocolPortRequiredAndExecutes(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{"collections/a/ws.request.yaml": wsRequestYAML()})
	suite, err := ParseSuite(suiteYAML("steps:\n  - {id: ws, request: ws}\n"))
	if err != nil {
		t.Fatal(err)
	}
	var httpCalls atomic.Int32
	httpExec := execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
		httpCalls.Add(1)
		return fixtureResult(`{}`, 200), nil
	})
	// Missing adapter: config error, no requests.
	result, err := New(p, suite, Options{Executor: httpExec}).Run(context.Background())
	if err == nil || result.Status != StatusConfigError || result.Failure.Kind != KindProtocol || httpCalls.Load() != 0 {
		t.Fatalf("missing port: %+v %v calls=%d", result, err, httpCalls.Load())
	}
	// Unsupported adapter.
	unsupported := &fakeProtocol{supports: func(*core.Request) bool { return false }}
	result, err = New(p, suite, Options{Executor: httpExec, Protocol: unsupported}).Run(context.Background())
	if err == nil || result.Status != StatusConfigError || unsupported.calls.Load() != 0 {
		t.Fatalf("unsupported port: %+v %v", result, err)
	}
	// Preflight failure: no execution.
	bad := &fakeProtocol{preflight: errors.New("handshake configuration invalid")}
	result, err = New(p, suite, Options{Executor: httpExec, Protocol: bad}).Run(context.Background())
	if err == nil || result.Status != StatusConfigError || bad.calls.Load() != 0 || bad.preflights.Load() != 1 {
		t.Fatalf("preflight port: %+v %v", result, err)
	}
	// Success: the adapter, not the HTTP executor, runs the attempt.
	adapter := &fakeProtocol{}
	result, err = New(p, suite, Options{Executor: httpExec, Protocol: adapter}).Run(context.Background())
	if err != nil || result.Status != StatusPassed || adapter.calls.Load() != 1 || httpCalls.Load() != 0 {
		t.Fatalf("protocol run: %+v %v calls=%d http=%d", result, err, adapter.calls.Load(), httpCalls.Load())
	}
}

func TestValidateConcurrencyBounds(t *testing.T) {
	for _, tc := range []struct {
		step, row int
		ok        bool
	}{
		{0, 0, true}, {1, 1, true}, {MaxStepConcurrency, MaxRowConcurrency, true},
		{-1, 1, false}, {1, -1, false}, {MaxStepConcurrency + 1, 1, false}, {1, MaxRowConcurrency + 1, false},
	} {
		if err := ValidateConcurrency(tc.step, tc.row); (err == nil) != tc.ok {
			t.Fatalf("ValidateConcurrency(%d,%d) = %v", tc.step, tc.row, err)
		}
	}
}
