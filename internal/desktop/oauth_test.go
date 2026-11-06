package desktop

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
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
)

func TestOAuthMessagesCoverBothLanguages(t *testing.T) {
	typ := reflect.TypeOf(oauthMessages{})
	zh := reflect.ValueOf(oauthZhCN)
	en := reflect.ValueOf(oauthEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		zhStr := strings.TrimSpace(zh.Field(i).String())
		enStr := strings.TrimSpace(en.Field(i).String())
		if zhStr == "" || enStr == "" {
			t.Errorf("oauthMessages.%s empty: zh=%q en=%q", f.Name, zhStr, enStr)
		}
	}
}

type desktopTestLoopbackSession struct {
	redirectURI string
	cbCh        chan core.LoopbackCallbackResult
}

func (s *desktopTestLoopbackSession) RedirectURI() string { return s.redirectURI }
func (s *desktopTestLoopbackSession) WaitForCallback(ctx context.Context) (core.LoopbackCallbackResult, error) {
	select {
	case <-ctx.Done():
		return core.LoopbackCallbackResult{}, ctx.Err()
	case r := <-s.cbCh:
		return r, nil
	}
}
func (s *desktopTestLoopbackSession) Close() error { return nil }

type desktopTestLoopbackFactory struct {
	mu      sync.Mutex
	current *desktopTestLoopbackSession
}

func (f *desktopTestLoopbackFactory) StartLoopback(_ context.Context, _ string) (core.LoopbackSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &desktopTestLoopbackSession{
		redirectURI: "http://127.0.0.1:58888/oauth/callback",
		cbCh:        make(chan core.LoopbackCallbackResult, 1),
	}
	f.current = s
	return s, nil
}

func TestOAuthUINoImplicitNetworkOnProjectOpenAndFullPKCEOIDCLifecycle(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	jwk := core.JWK{
		Kty: "RSA",
		Use: "sig",
		Kid: "ui-kid-1",
		Alg: "RS256",
		N:   base64.RawURLEncoding.EncodeToString(priv.PublicKey.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(priv.PublicKey.E)).Bytes()),
	}

	var serverHits atomic.Int32
	var revokedHits atomic.Int32
	var lastNonce atomic.Value
	var srv *httptest.Server

	signIDToken := func(sub, nonce string) string {
		hdr, _ := json.Marshal(map[string]any{"alg": "RS256", "typ": "JWT", "kid": "ui-kid-1"})
		claims, _ := json.Marshal(map[string]any{
			"iss":   srv.URL,
			"sub":   sub,
			"aud":   "ui-oidc-client",
			"exp":   now.Add(1 * time.Hour).Unix(),
			"iat":   now.Unix(),
			"nonce": nonce,
		})
		signingInput := base64.RawURLEncoding.EncodeToString(hdr) + "." + base64.RawURLEncoding.EncodeToString(claims)
		sum := sha256.Sum256([]byte(signingInput))
		sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, sum[:])
		if err != nil {
			t.Fatalf("rsa.SignPKCS1v15: %v", err)
		}
		return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
	}

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serverHits.Add(1)
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(core.OIDCDiscoveryDocument{
				Issuer:                srv.URL,
				AuthorizationEndpoint: srv.URL + "/authorize",
				TokenEndpoint:         srv.URL + "/token",
				JWKSURI:               srv.URL + "/jwks",
				RevocationEndpoint:    srv.URL + "/revoke",
			})
		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(core.JWKSet{Keys: []core.JWK{jwk}})
		case "/token":
			_ = r.ParseForm()
			gt := r.Form.Get("grant_type")
			w.Header().Set("Content-Type", "application/json")
			if gt == "refresh_token" {
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token":  "UI_SECRET_ACCESS_TOKEN_ROTATED_222",
					"token_type":    "Bearer",
					"expires_in":    3600,
					"refresh_token": "UI_SECRET_REFRESH_TOKEN_ROTATED_222",
					"scope":         "openid profile",
				})
				return
			}
			nonceStr, _ := lastNonce.Load().(string)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  "UI_SECRET_ACCESS_TOKEN_INITIAL_111",
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": "UI_SECRET_REFRESH_TOKEN_INITIAL_111",
				"id_token":      signIDToken("ui-user-99", nonceStr),
				"scope":         "openid profile",
			})
		case "/revoke":
			revokedHits.Add(1)
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := seedAuthFixtureProject(t)
	oauthSchemeYAML := `schemaVersion: 1
id: corp-oidc
type: oidc
extensions:
  oauth:
    grantType: authorization_code
    issuer: "` + srv.URL + `"
    clientId: ui-oidc-client
    clientSecret: "{{secret.oauthClientSecret}}"
    scopes:
      - openid
      - profile
`
	if err := os.WriteFile(filepath.Join(dir, ".gorequest", "auth", "corp-oidc.auth.yaml"), []byte(oauthSchemeYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	m.secrets = core.MapSecrets{
		"oauthClientSecret": "UI_SECRET_CLIENT_SECRET_000",
	}
	if err := m.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	app := m.workspaces[0]
	ps := app.ps

	lbFactory := &desktopTestLoopbackFactory{}
	var browserOpened atomic.Int32
	ps.ConfigureOAuthService(core.OAuthServiceConfig{
		TokenStore: core.NewMemoryOAuthTokenStore(),
		BrowserOpener: core.BrowserOpenerFunc(func(_ context.Context, authURL string) error {
			browserOpened.Add(1)
			u, err := url.Parse(authURL)
			if err != nil {
				return err
			}
			q := u.Query()
			lastNonce.Store(q.Get("nonce"))
			lbFactory.mu.Lock()
			sess := lbFactory.current
			lbFactory.mu.Unlock()
			sess.cbCh <- core.LoopbackCallbackResult{
				Code:  "UI_SECRET_AUTH_CODE_777",
				State: q.Get("state"),
			}
			return nil
		}),
		LoopbackFactory: lbFactory,
		Now:             func() time.Time { return now },
	}, true)

	if _, err := ps.createRequestIn("", "OIDC Protected Request"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = srv.URL + "/api/me"
	tab.AuthMode = "ref"
	tab.AuthRefID = "corp-oidc"

	tt := ui.NewTester(func(c *ui.Context) {
		app.renderOAuthEditor(c, c.Theme(), tab)
	}, 1100, 780)
	tt.Frame()

	// 1. Project open + initial render MUST NOT perform any network request or open browser!
	if serverHits.Load() != 0 || browserOpened.Load() != 0 {
		t.Fatalf("expected 0 implicit network/browser calls on project open & render, got serverHits=%d browserOpened=%d",
			serverHits.Load(), browserOpened.Load())
	}

	om := oauthMsg(app.oauthLocale())
	if !tt.HasText(om.InspectorNoToken) {
		t.Fatalf("expected InspectorNoToken text before login, texts=%v", tt.Texts())
	}

	// 2. Explicit click on "Fetch Discovery".
	if err := tt.Click(om.BtnFetchDiscovery); err != nil {
		t.Fatalf("Click FetchDiscovery: %v", err)
	}
	tt.Frame()
	if serverHits.Load() == 0 {
		t.Fatalf("expected FetchDiscovery click to contact discovery server")
	}

	// 3. Explicit click on "Sign In with Browser (PKCE)".
	if err := tt.Click(om.BtnGetTokenPKCE); err != nil {
		t.Fatalf("Click GetTokenPKCE: %v", err)
	}
	tt.Frame()
	if browserOpened.Load() != 1 {
		t.Fatalf("expected 1 browser open after explicit PKCE button click, got %d", browserOpened.Load())
	}
	if !tt.HasText(om.OIDCBadgeVerified) {
		t.Fatalf("expected OIDC verified badge in UI after PKCE login, texts=%v", tt.Texts())
	}
	// Ensure raw secret token is masked in UI texts.
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "UI_SECRET_ACCESS_TOKEN_INITIAL_111") || strings.Contains(txt, "UI_SECRET_REFRESH_TOKEN_INITIAL_111") {
			t.Fatalf("UI text leaked raw token: %q", txt)
		}
	}

	// 4. Save tab & verify zero runtime tokens leaked to shared project files on disk.
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("saveTab: %v", err)
	}
	assertNoSecretInDir(t, dir, []string{
		"UI_SECRET_CLIENT_SECRET_000",
		"UI_SECRET_ACCESS_TOKEN_INITIAL_111",
		"UI_SECRET_REFRESH_TOKEN_INITIAL_111",
		"UI_SECRET_AUTH_CODE_777",
	})

	// 5. Explicit click on "Refresh Token Now".
	if err := tt.Click(om.BtnRefreshToken); err != nil {
		t.Fatalf("Click RefreshToken: %v", err)
	}
	tt.Frame()
	plan := &core.Plan{Method: "GET", URL: srv.URL + "/api/me"}
	rec, err := app.ApplyTabOAuthIfConfigured(context.Background(), ps, tab, plan, nil, false)
	if err != nil || rec == nil || rec.AccessToken != "UI_SECRET_ACCESS_TOKEN_ROTATED_222" {
		t.Fatalf("expected rotated token after RefreshToken click, got rec=%+v err=%v", rec, err)
	}

	// 6. Explicit click on "Revoke & Clear Token".
	if err := tt.Click(om.BtnRevokeToken); err != nil {
		t.Fatalf("Click RevokeToken: %v", err)
	}
	tt.Frame()
	if revokedHits.Load() != 1 {
		t.Fatalf("expected 1 remote revocation call, got %d", revokedHits.Load())
	}
	if !tt.HasText(om.InspectorNoToken) {
		t.Fatalf("expected InspectorNoToken after RevokeToken click, texts=%v", tt.Texts())
	}
}

func TestOAuthUIDeviceCodeChallengeAndCancel(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	var pollStep atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/device/code":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":      "DEV_CODE_SECRET_555",
				"user_code":        "ABCD-EFGH",
				"verification_uri": srv.URL + "/activate",
				"expires_in":       300,
				"interval":         1,
			})
		case "/token":
			if pollStep.Add(1) == 1 {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "DEV_FLOW_ACCESS_TOKEN_888",
				"token_type":   "Bearer",
				"expires_in":   1800,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	dir := seedAuthFixtureProject(t)
	deviceYAML := `schemaVersion: 1
id: cli-device
type: oauth2
extensions:
  oauth:
    grantType: device_code
    deviceAuthorizationUrl: "` + srv.URL + `/device/code"
    tokenUrl: "` + srv.URL + `/token"
    clientId: device-ui-client
    scopes:
      - repo
`
	if err := os.WriteFile(filepath.Join(dir, ".gorequest", "auth", "cli-device.auth.yaml"), []byte(deviceYAML), 0o644); err != nil {
		t.Fatal(err)
	}

	m, _ := newTestManager(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	ps := app.ps

	ps.ConfigureOAuthService(core.OAuthServiceConfig{
		TokenStore: core.NewMemoryOAuthTokenStore(),
		Now:        func() time.Time { return now },
		Sleep:      func(_ context.Context, _ time.Duration) error { return nil },
	}, true)

	if _, err := ps.createRequestIn("", "Device Flow Request"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.AuthMode = "ref"
	tab.AuthRefID = "cli-device"

	tt := ui.NewTester(func(c *ui.Context) {
		app.renderOAuthEditor(c, c.Theme(), tab)
	}, 1100, 780)
	tt.Frame()

	om := oauthMsg(app.oauthLocale())
	if err := tt.Click(om.BtnStartDeviceFlow); err != nil {
		t.Fatalf("Click StartDeviceFlow: %v", err)
	}
	tt.Frame()

	if !tt.HasText(om.StatusTokenAcquired) {
		t.Fatalf("expected StatusTokenAcquired after device flow completion, texts=%v", tt.Texts())
	}

	// Verify Cancel button works when a flow is busy.
	st := getOrCreateOAuthTabState(tab)
	canceled := false
	st.mu.Lock()
	st.Busy = true
	st.ActiveCancel = func() { canceled = true }
	st.ActiveDeviceChallenge = &core.DeviceAuthorizationChallenge{
		UserCode:        "WXYZ-1234",
		VerificationURI: srv.URL + "/activate",
	}
	st.mu.Unlock()
	tt.Frame()

	if !tt.HasText("WXYZ-1234") {
		t.Fatalf("expected active device user_code WXYZ-1234 in UI, texts=%v", tt.Texts())
	}
	if err := tt.Click(om.BtnCancelFlow); err != nil {
		t.Fatalf("Click CancelFlow: %v", err)
	}
	tt.Frame()
	if !canceled {
		t.Fatalf("expected ActiveCancel to be invoked on CancelFlow click")
	}
}

