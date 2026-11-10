package desktop

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"gorequest/internal/core"
	"gorequest/internal/runner"
)

const (
	oauthAccessToken       = "SECRET_oauth_access_7f2k"
	oauthRefreshedToken    = "SECRET_oauth_refreshed_9c4m"
	oauthRotatedRefresh    = "SECRET_oauth_rotated_5p1q"
	oauthInitialRefresh    = "SECRET_oauth_refresh_3d8n"
	oauthClientSecretValue = "SECRET_oauth_client_6w0z"
)

func oauthFixtureProject(t *testing.T, requestYAML, suiteYAML string, files map[string]string) *core.Project {
	t.Helper()
	root := t.TempDir()
	store := core.NewStore(filepath.Join(root, ".gorequest"))
	put := func(rel, body string) {
		t.Helper()
		if err := store.WriteAtomic(rel, []byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	put("project.yaml", "schemaVersion: 1\nid: oauth-fixture\nname: OAuth Fixture\n")
	put("collections/a/r.request.yaml", requestYAML)
	put("suites/oauth.suite.yaml", suiteYAML)
	for rel, body := range files {
		put(rel, body)
	}
	p, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range p.Issues {
		if issue.Level == "error" {
			t.Fatalf("fixture issue: %+v", issue)
		}
	}
	return p
}

func oauthRequestYAML(tokenURL, resourceURL, grant string) string {
	return fmt.Sprintf(`schemaVersion: 1
id: r
name: oauth request
http:
  method: GET
  url: %q
auth:
  type: oauth2
  oauth:
    grantType: %s
    clientId: fixture-client
    clientSecret: "{{secret.clientSecret}}"
    tokenUrl: %q
assert:
  - status: 200
`, resourceURL, grant, tokenURL)
}

func oauthSuiteYAML(dataset string) string {
	return "schemaVersion: 1\nid: oauth-suite\nonFailure: stop\n" + dataset + "steps:\n  - {id: r, request: r}\n"
}

func newOAuthService(t *testing.T) (*core.OAuthService, *core.MemoryOAuthTokenStore) {
	t.Helper()
	store := core.NewMemoryOAuthTokenStore()
	return core.NewOAuthService(core.OAuthServiceConfig{TokenStore: store}), store
}

func runPreparedOAuth(t *testing.T, p *core.Project, svc *core.OAuthService, network core.ExecuteOptions, opts desktopRunnerOptions) (*runner.RunResult, error) {
	t.Helper()
	suite, err := runner.LoadSuite(p, "suites/oauth.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if opts.OAuth == nil {
		opts.OAuth = svc
	}
	r, err := prepareDesktopRunner(context.Background(), p, suite, "", network, core.MapSecrets{"clientSecret": oauthClientSecretValue}, nil, opts)
	if err != nil {
		t.Fatal(err)
	}
	return r.Run(context.Background())
}

func assertNoSecretsInReports(t *testing.T, result *runner.RunResult) {
	t.Helper()
	for _, marshal := range []func(*runner.RunResult) ([]byte, error){runner.MarshalJSONReport, runner.MarshalJUnitReport} {
		b, err := marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		for _, secret := range []string{oauthAccessToken, oauthRefreshedToken, oauthRotatedRefresh, oauthInitialRefresh, oauthClientSecretValue} {
			if strings.Contains(string(b), secret) {
				t.Fatalf("OAuth secret %q leaked into a report", secret)
			}
		}
	}
}

func TestRunnerOAuthClientCredentialsLoopback(t *testing.T) {
	var tokenCalls, businessCalls atomic.Int32
	var mu sync.Mutex
	var authHeader string
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		businessCalls.Add(1)
		mu.Lock()
		authHeader = r.Header.Get("Authorization")
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer resource.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
		if err := r.ParseForm(); err != nil {
			t.Errorf("token form: %v", err)
		}
		user, pass, ok := r.BasicAuth()
		if !ok || user != "fixture-client" || pass != oauthClientSecretValue {
			t.Errorf("client auth = %q/%q ok=%v", user, pass, ok)
		}
		if r.Form.Get("grant_type") != "client_credentials" {
			t.Errorf("grant_type = %q", r.Form.Get("grant_type"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, oauthAccessToken)
	}))
	defer token.Close()

	p := oauthFixtureProject(t, oauthRequestYAML(token.URL+"/token", resource.URL, "client_credentials"), oauthSuiteYAML(""), nil)
	svc, _ := newOAuthService(t)
	result, err := runPreparedOAuth(t, p, svc, core.ExecuteOptions{}, desktopRunnerOptions{StepConcurrency: 1, RowConcurrency: 1})
	if err != nil || result.Status != runner.StatusPassed {
		t.Fatalf("%+v %v", result, err)
	}
	if tokenCalls.Load() != 1 || businessCalls.Load() != 1 {
		t.Fatalf("token calls=%d business=%d", tokenCalls.Load(), businessCalls.Load())
	}
	mu.Lock()
	got := authHeader
	mu.Unlock()
	if got != "Bearer "+oauthAccessToken {
		t.Fatalf("business Authorization = %q", got)
	}
	assertNoSecretsInReports(t, result)
}

func preloadExpiredToken(t *testing.T, p *core.Project, store *core.MemoryOAuthTokenStore, access, refresh string) {
	t.Helper()
	var entry *core.RequestEntry
	for _, e := range p.Requests {
		entry = e
	}
	eff, err := p.ResolveEffectiveAuth(entry, nil)
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := core.ExtractOAuthConfig(eff.Scheme)
	if err != nil {
		t.Fatal(err)
	}
	key := core.BuildOAuthCacheKey(p.Dir, p.File.ID, "inline:collections/a/r.request.yaml", cfg, "oauth2")
	if err := store.PutToken(context.Background(), key, &core.OAuthTokenRecord{
		AccessToken:  access,
		RefreshToken: refresh,
		TokenType:    "Bearer",
		ExpiresAt:    time.Now().Add(-time.Minute),
		ObtainedAt:   time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRunnerOAuthRefreshRotationConcurrentSingleflight(t *testing.T) {
	var refreshCalls, businessCalls atomic.Int32
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		businessCalls.Add(1)
		if r.Header.Get("Authorization") != "Bearer "+oauthRefreshedToken {
			t.Errorf("business token = %q", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer resource.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		refreshCalls.Add(1)
		// Hold the first refresh long enough for the second row to join the
		// singleflight instead of racing the rotating refresh token.
		time.Sleep(50 * time.Millisecond)
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != oauthInitialRefresh {
			t.Errorf("refresh form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"refresh_token":%q,"token_type":"Bearer","expires_in":3600}`, oauthRefreshedToken, oauthRotatedRefresh)
	}))
	defer token.Close()

	p := oauthFixtureProject(t, oauthRequestYAML(token.URL+"/token", resource.URL, "authorization_code"),
		oauthSuiteYAML("dataset: {format: json, file: data/rows.json}\n"),
		map[string]string{"data/rows.json": `[{"id":1},{"id":2}]`})
	svc, store := newOAuthService(t)
	preloadExpiredToken(t, p, store, "SECRET_oauth_old_access_2r8x", oauthInitialRefresh)

	result, err := runPreparedOAuth(t, p, svc, core.ExecuteOptions{}, desktopRunnerOptions{StepConcurrency: 1, RowConcurrency: 2})
	if err != nil || result.Status != runner.StatusPassed {
		t.Fatalf("%+v %v", result, err)
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("concurrent refresh was not coalesced: %d token calls", refreshCalls.Load())
	}
	if businessCalls.Load() != 2 {
		t.Fatalf("business calls = %d", businessCalls.Load())
	}
	if result.RowConcurrency != 2 || result.StepConcurrency != 1 {
		t.Fatalf("frozen concurrency = %d/%d", result.StepConcurrency, result.RowConcurrency)
	}
	assertNoSecretsInReports(t, result)
}

func TestRunnerOAuthInteractionRequiredSendsNothing(t *testing.T) {
	var tokenCalls, businessCalls atomic.Int32
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		businessCalls.Add(1)
	}))
	defer resource.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenCalls.Add(1)
	}))
	defer token.Close()

	p := oauthFixtureProject(t, oauthRequestYAML(token.URL+"/token", resource.URL, "authorization_code"), oauthSuiteYAML(""), nil)
	svc, _ := newOAuthService(t)
	result, err := runPreparedOAuth(t, p, svc, core.ExecuteOptions{}, desktopRunnerOptions{StepConcurrency: 1, RowConcurrency: 1})
	if err == nil || result.Status != runner.StatusConfigError {
		t.Fatalf("%+v %v", result, err)
	}
	if result.Failure == nil || result.Failure.Category != runner.FailureConfig || result.Failure.Kind != runner.KindAuth {
		t.Fatalf("failure = %+v", result.Failure)
	}
	if !strings.Contains(result.Failure.Message, "non-interactive") {
		t.Fatalf("diagnostic = %q", result.Failure.Message)
	}
	if tokenCalls.Load() != 0 || businessCalls.Load() != 0 {
		t.Fatalf("token calls=%d business=%d", tokenCalls.Load(), businessCalls.Load())
	}
}

func TestRunnerOAuthCancelDuringRefreshStopsBusinessRequest(t *testing.T) {
	entered := make(chan struct{}, 1)
	stop := make(chan struct{})
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("business request sent after cancellation")
	}))
	defer resource.Close()
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		select {
		case <-r.Context().Done():
		case <-stop:
		}
	}))
	defer token.Close()
	defer close(stop) // LIFO: release the handler before Close waits for it

	p := oauthFixtureProject(t, oauthRequestYAML(token.URL+"/token", resource.URL, "authorization_code"), oauthSuiteYAML(""), nil)
	svc, store := newOAuthService(t)
	preloadExpiredToken(t, p, store, "SECRET_oauth_old_access_2r8x", oauthInitialRefresh)
	suite, err := runner.LoadSuite(p, "suites/oauth.suite.yaml")
	if err != nil {
		t.Fatal(err)
	}
	r, err := prepareDesktopRunner(context.Background(), p, suite, "", core.ExecuteOptions{}, core.MapSecrets{"clientSecret": oauthClientSecretValue}, nil,
		desktopRunnerOptions{StepConcurrency: 1, RowConcurrency: 1, OAuth: svc})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan *runner.RunResult, 1)
	go func() {
		res, _ := r.Run(ctx)
		done <- res
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("refresh request did not start")
	}
	cancel()
	select {
	case res := <-done:
		if res == nil || res.Status != runner.StatusCanceled {
			t.Fatalf("canceled result = %+v", res)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("run did not stop after cancellation")
	}
}

func TestRunnerOAuthExportedReportContainsNoToken(t *testing.T) {
	// Reuse a simple passing result and the existing explicit-path writer to
	// prove the desktop export path also observes redaction.
	token := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"access_token":%q,"token_type":"Bearer","expires_in":3600}`, oauthAccessToken)
	}))
	defer token.Close()
	resource := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"ok":true}`)
	}))
	defer resource.Close()
	p := oauthFixtureProject(t, oauthRequestYAML(token.URL+"/token", resource.URL, "client_credentials"), oauthSuiteYAML(""), nil)
	svc, _ := newOAuthService(t)
	result, err := runPreparedOAuth(t, p, svc, core.ExecuteOptions{}, desktopRunnerOptions{StepConcurrency: 1, RowConcurrency: 1})
	if err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "report.json")
	if err := writeRunnerReport(result, dest, p.Dir, false); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(dest)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), oauthAccessToken) || strings.Contains(string(b), oauthClientSecretValue) {
		t.Fatal("exported report contains an OAuth secret")
	}
	var decoded map[string]any
	if err := json.Unmarshal(b, &decoded); err != nil {
		t.Fatalf("report is not valid JSON: %v", err)
	}
}
