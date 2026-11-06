package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeLoopbackSession struct {
	redirectURI string
	cbCh        chan LoopbackCallbackResult
	closed      atomic.Bool
}

func (s *fakeLoopbackSession) RedirectURI() string {
	return s.redirectURI
}

func (s *fakeLoopbackSession) WaitForCallback(ctx context.Context) (LoopbackCallbackResult, error) {
	select {
	case <-ctx.Done():
		return LoopbackCallbackResult{}, ctx.Err()
	case res := <-s.cbCh:
		return res, nil
	}
}

func (s *fakeLoopbackSession) Close() error {
	s.closed.Store(true)
	return nil
}

type fakeLoopbackFactory struct {
	mu      sync.Mutex
	current *fakeLoopbackSession
}

func (f *fakeLoopbackFactory) StartLoopback(_ context.Context, _ string) (LoopbackSession, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	s := &fakeLoopbackSession{
		redirectURI: "http://127.0.0.1:54321/oauth/callback",
		cbCh:        make(chan LoopbackCallbackResult, 1),
	}
	f.current = s
	return s, nil
}

func TestOAuthAuthorizePKCEAndOIDCEndToEnd(t *testing.T) {
	now := time.Date(2026, 11, 6, 14, 0, 0, 0, time.UTC)
	rsaKey := newTestRSAKeyPair(t, "oidc-rs256", "RS256")

	var srv *httptest.Server
	var lastChallenge, lastChallengeMethod, lastNonce string

	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(OIDCDiscoveryDocument{
				Issuer:                srv.URL,
				AuthorizationEndpoint: srv.URL + "/authorize",
				TokenEndpoint:         srv.URL + "/token",
				JWKSURI:               srv.URL + "/jwks",
				RevocationEndpoint:    srv.URL + "/revoke",
			})

		case "/jwks":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(JWKSet{Keys: []JWK{rsaKey.jwk}})

		case "/token":
			if err := r.ParseForm(); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			verifier := r.Form.Get("code_verifier")
			sum := sha256.Sum256([]byte(verifier))
			computedChallenge := base64.RawURLEncoding.EncodeToString(sum[:])
			if lastChallengeMethod != "S256" || computedChallenge != lastChallenge {
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{
					"error":             "invalid_grant",
					"error_description": "PKCE S256 verifier mismatch",
				})
				return
			}

			accessToken := "at_pkce_secret_token_val_111"
			refreshToken := "rt_pkce_secret_refresh_222"
			atHash, _ := computeAtHash("RS256", accessToken)
			idToken := rsaKey.signJWS(t, map[string]any{
				"iss":     srv.URL,
				"sub":     "user-pkce-42",
				"aud":     "pkce-desktop-client",
				"exp":     now.Add(1 * time.Hour).Unix(),
				"iat":     now.Unix(),
				"nonce":   lastNonce,
				"at_hash": atHash,
				"email":   "dev@example.com",
			})

			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token":  accessToken,
				"token_type":    "Bearer",
				"expires_in":    3600,
				"refresh_token": refreshToken,
				"id_token":      idToken,
				"scope":         "openid profile email",
			})

		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	lbFactory := &fakeLoopbackFactory{}
	store := NewMemoryOAuthTokenStore()
	browser := BrowserOpenerFunc(func(_ context.Context, authURL string) error {
		u, err := url.Parse(authURL)
		if err != nil {
			return err
		}
		q := u.Query()
		lastChallenge = q.Get("code_challenge")
		lastChallengeMethod = q.Get("code_challenge_method")
		lastNonce = q.Get("nonce")

		lbFactory.mu.Lock()
		sess := lbFactory.current
		lbFactory.mu.Unlock()
		sess.cbCh <- LoopbackCallbackResult{
			Code:  "auth_code_secret_999",
			State: q.Get("state"),
		}
		return nil
	})

	svc := NewOAuthService(OAuthServiceConfig{
		TokenStore:      store,
		BrowserOpener:   browser,
		LoopbackFactory: lbFactory,
		Now:             func() time.Time { return now },
	})

	red := NewRedactor()
	req := OAuthFlowRequest{
		ProjectRoot: "/tmp/proj-a",
		ProjectSlug: "demo",
		SchemeID:    "corp-oidc",
		SchemeType:  "oidc",
		Config: OAuthConfig{
			GrantType: OAuthGrantAuthorizationCode,
			Issuer:    srv.URL,
			ClientID:  "pkce-desktop-client",
			Scopes:    []string{"openid", "profile", "email"},
		},
		Redactor:    red,
		Interactive: true,
	}

	rec, err := svc.AuthorizePKCE(context.Background(), req)
	if err != nil {
		t.Fatalf("AuthorizePKCE failed: %v", err)
	}
	if rec.AccessToken != "at_pkce_secret_token_val_111" || rec.RefreshToken != "rt_pkce_secret_refresh_222" {
		t.Fatalf("unexpected token record: %+v", rec)
	}
	if rec.IDTokenInspection == nil || !rec.IDTokenInspection.Verified || rec.Subject != "user-pkce-42" {
		t.Fatalf("expected verified OIDC ID Token inspection, got: %+v", rec.IDTokenInspection)
	}
	if !lbFactory.current.closed.Load() {
		t.Fatalf("expected loopback session to be closed immediately after callback")
	}

	// Verify all sensitive tokens and codes were registered in Redactor.
	logLine := "Tokens: " + rec.AccessToken + " | " + rec.RefreshToken + " | " + rec.IDToken + " | auth_code_secret_999"
	redactedLine := red.RedactString(logLine)
	for _, secret := range []string{rec.AccessToken, rec.RefreshToken, rec.IDToken, "auth_code_secret_999"} {
		if strings.Contains(redactedLine, secret) {
			t.Fatalf("Redactor leaked secret %q in %q", secret, redactedLine)
		}
	}

	// Verify state mismatch is rejected.
	badStateBrowser := BrowserOpenerFunc(func(_ context.Context, _ string) error {
		lbFactory.mu.Lock()
		sess := lbFactory.current
		lbFactory.mu.Unlock()
		sess.cbCh <- LoopbackCallbackResult{
			Code:  "code-123",
			State: "tampered-state",
		}
		return nil
	})
	svcBadState := NewOAuthService(OAuthServiceConfig{
		TokenStore:      store,
		BrowserOpener:   badStateBrowser,
		LoopbackFactory: lbFactory,
		Now:             func() time.Time { return now },
	})
	_, err = svcBadState.AuthorizePKCE(context.Background(), req)
	if !IsOAuthErrorCategory(err, OAuthErrStateMismatch) {
		t.Fatalf("expected OAuthErrStateMismatch on tampered state, got: %v", err)
	}

	// Verify non-interactive mode refuses to start PKCE browser flow.
	nonInteractiveReq := req
	nonInteractiveReq.Interactive = false
	_, err = svc.AuthorizePKCE(context.Background(), nonInteractiveReq)
	if !IsOAuthErrorCategory(err, OAuthErrInteractionRequired) {
		t.Fatalf("expected OAuthErrInteractionRequired when Interactive=false, got: %v", err)
	}
}

func TestOAuthDeviceCodeFlowPollingAndSlowDown(t *testing.T) {
	now := time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	var pollCount atomic.Int32

	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/device/code":
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"device_code":               "dev_secret_code_777",
				"user_code":                 "WDJB-MJHT",
				"verification_uri":          srv.URL + "/activate",
				"verification_uri_complete": srv.URL + "/activate?user_code=WDJB-MJHT",
				"expires_in":                600,
				"interval":                  2,
			})
		case "/token":
			n := pollCount.Add(1)
			w.Header().Set("Content-Type", "application/json")
			switch n {
			case 1:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "authorization_pending"})
			case 2:
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "slow_down"})
			default:
				_ = json.NewEncoder(w).Encode(map[string]any{
					"access_token": "at_device_token_888",
					"token_type":   "Bearer",
					"expires_in":   1800,
				})
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	var recordedSleeps []time.Duration
	svc := NewOAuthService(OAuthServiceConfig{
		TokenStore: NewMemoryOAuthTokenStore(),
		Now:        func() time.Time { return now },
		Sleep: func(ctx context.Context, d time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			recordedSleeps = append(recordedSleeps, d)
			return nil
		},
	})

	red := NewRedactor()
	req := OAuthFlowRequest{
		ProjectRoot: "/tmp/proj-dev",
		ProjectSlug: "dev-proj",
		SchemeID:    "cli-device",
		SchemeType:  "oauth2",
		Config: OAuthConfig{
			GrantType:              OAuthGrantDeviceCode,
			DeviceAuthorizationURL: srv.URL + "/device/code",
			TokenURL:               srv.URL + "/token",
			ClientID:               "device-client-1",
			Scopes:                 []string{"read:org"},
		},
		Redactor:    red,
		Interactive: true,
	}

	ch, err := svc.StartDeviceAuthorization(context.Background(), req)
	if err != nil {
		t.Fatalf("StartDeviceAuthorization failed: %v", err)
	}
	if ch.UserCode != "WDJB-MJHT" || ch.PollInterval != 2*time.Second {
		t.Fatalf("unexpected device challenge: %+v", ch)
	}

	var statuses []DevicePollStatus
	rec, err := svc.PollDeviceToken(context.Background(), req, ch, func(st DevicePollStatus) {
		statuses = append(statuses, st)
	})
	if err != nil {
		t.Fatalf("PollDeviceToken failed: %v", err)
	}
	if rec.AccessToken != "at_device_token_888" {
		t.Fatalf("unexpected device token: %+v", rec)
	}
	// Verify RFC 8628 §3.5 slow_down increased polling interval by +5s (from 2s -> 7s on third poll).
	if len(recordedSleeps) != 3 || recordedSleeps[0] != 2*time.Second || recordedSleeps[1] != 2*time.Second || recordedSleeps[2] != 7*time.Second {
		t.Fatalf("expected sleep intervals [2s, 2s, 7s] after slow_down, got: %v", recordedSleeps)
	}
	if len(statuses) != 2 || !statuses[1].SlowedDown {
		t.Fatalf("expected status callback to record SlowedDown=true, got: %+v", statuses)
	}
}

func TestOAuthSingleflightConcurrentRefreshAndRotation(t *testing.T) {
	now := time.Date(2026, 11, 6, 16, 0, 0, 0, time.UTC)
	var refreshCalls atomic.Int32
	var currentValidRefreshToken sync.Map
	currentValidRefreshToken.Store("valid", "rt_gen_1_initial")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if r.Form.Get("grant_type") != "refresh_token" {
			http.Error(w, "expected refresh_token grant", http.StatusBadRequest)
			return
		}
		gotRT := r.Form.Get("refresh_token")
		expectedRT, _ := currentValidRefreshToken.Load("valid")
		if gotRT != expectedRT.(string) {
			// Simulate strict refresh token rotation replay detection!
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_grant",
				"error_description": "refresh token replayed or already rotated",
			})
			return
		}
		// Rotate immediately so any second concurrent call with rt_gen_1_initial would fail!
		currentValidRefreshToken.Store("valid", "rt_gen_2_rotated")
		refreshCalls.Add(1)
		time.Sleep(25 * time.Millisecond)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  "at_gen_2_fresh_token",
			"token_type":    "Bearer",
			"expires_in":    3600,
			"refresh_token": "rt_gen_2_rotated",
		})
	}))
	defer srv.Close()

	store := NewMemoryOAuthTokenStore()
	svc := NewOAuthService(OAuthServiceConfig{
		TokenStore: store,
		Now:        func() time.Time { return now },
	})

	req := OAuthFlowRequest{
		ProjectRoot: "/tmp/proj-sf",
		ProjectSlug: "sf",
		SchemeID:    "rot-oauth",
		SchemeType:  "oauth2",
		Config: OAuthConfig{
			GrantType: OAuthGrantAuthorizationCode,
			TokenURL:  srv.URL + "/token",
			ClientID:  "sf-client",
		},
		Redactor: NewRedactor(),
	}

	// Pre-populate an expiring token (expires in 10s, within DefaultTokenExpirySkew = 30s).
	key := req.CacheKey(nil)
	if err := store.PutToken(context.Background(), key, &OAuthTokenRecord{
		AccessToken:  "at_gen_1_expiring",
		RefreshToken: "rt_gen_1_initial",
		ExpiresAt:    now.Add(10 * time.Second),
		ObtainedAt:   now.Add(-1 * time.Hour),
	}); err != nil {
		t.Fatalf("PutToken: %v", err)
	}

	// Launch 16 concurrent callers requesting EnsureValidToken simultaneously.
	const workers = 16
	var wg sync.WaitGroup
	errs := make([]error, workers)
	tokens := make([]string, workers)

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			rec, err := svc.EnsureValidToken(context.Background(), req)
			errs[idx] = err
			if rec != nil {
				tokens[idx] = rec.AccessToken
			}
		}(i)
	}
	wg.Wait()

	for i := 0; i < workers; i++ {
		if errs[i] != nil {
			t.Fatalf("worker %d failed: %v", i, errs[i])
		}
		if tokens[i] != "at_gen_2_fresh_token" {
			t.Fatalf("worker %d got token %q, want at_gen_2_fresh_token", i, tokens[i])
		}
	}
	if refreshCalls.Load() != 1 {
		t.Fatalf("expected singleflight to coalesce 16 concurrent refreshes into 1 HTTP request, got %d", refreshCalls.Load())
	}

	persisted, err := store.GetToken(context.Background(), key)
	if err != nil || persisted == nil || persisted.RefreshToken != "rt_gen_2_rotated" {
		t.Fatalf("expected rotated refresh_token rt_gen_2_rotated in store, got: %+v", persisted)
	}
}

func TestOAuthClientCredentialsRevocationAndCrossOriginRedirectGuard(t *testing.T) {
	now := time.Date(2026, 11, 6, 17, 0, 0, 0, time.UTC)
	var revokedToken atomic.Value

	authSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/token":
			user, pass, ok := r.BasicAuth()
			if !ok || user != "m2m-client" || pass != "super_secret_m2m_pass" {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"access_token": "at_m2m_live_token_555",
				"token_type":   "Bearer",
				"expires_in":   1200,
			})
		case "/revoke":
			_ = r.ParseForm()
			revokedToken.Store(r.Form.Get("token"))
			w.WriteHeader(http.StatusOK)
		default:
			http.NotFound(w, r)
		}
	}))
	defer authSrv.Close()

	// Cross-origin redirect target server (simulates redirect to a different port/origin).
	var redirectedAuthHeader atomic.Value
	redirectTargetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		redirectedAuthHeader.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer redirectTargetSrv.Close()

	// Primary API server that verifies Authorization header on Hop 0 and redirects to redirectTargetSrv.
	var hop0AuthHeader atomic.Value
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hop0AuthHeader.Store(r.Header.Get("Authorization"))
		http.Redirect(w, r, redirectTargetSrv.URL+"/landed", http.StatusFound)
	}))
	defer apiSrv.Close()

	store := NewMemoryOAuthTokenStore()
	svc := NewOAuthService(OAuthServiceConfig{
		TokenStore: store,
		Now:        func() time.Time { return now },
	})

	red := NewRedactor()
	req := OAuthFlowRequest{
		ProjectRoot: "/tmp/proj-m2m",
		ProjectSlug: "m2m",
		SchemeID:    "service-auth",
		SchemeType:  "oauth2",
		Config: OAuthConfig{
			GrantType:        OAuthGrantClientCredentials,
			TokenURL:         authSrv.URL + "/token",
			RevocationURL:    authSrv.URL + "/revoke",
			ClientID:         "m2m-client",
			ClientSecret:     "{{secret.m2m_secret}}",
			ClientAuthMethod: OAuthClientAuthBasic,
		},
		SecretValues: map[string]string{
			"m2m_secret": "super_secret_m2m_pass",
		},
		Redactor: red,
	}

	plan := &Plan{
		Method:  http.MethodGet,
		URL:     apiSrv.URL + "/v1/resource",
		Timeout: 5 * time.Second,
	}
	if _, err := svc.ApplyOAuthToPlan(context.Background(), plan, req); err != nil {
		t.Fatalf("ApplyOAuthToPlan failed: %v", err)
	}

	res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Redirect: RedirectPolicy{Mode: RedirectFollow, MaxHops: 5},
		Redactor: red,
	})
	if err != nil {
		t.Fatalf("ExecuteWithOptions failed: %v", err)
	}
	if hop0AuthHeader.Load() != "Bearer at_m2m_live_token_555" {
		t.Fatalf("expected Hop 0 to receive Bearer token, got %v", hop0AuthHeader.Load())
	}
	if gotRedirectAuth, _ := redirectedAuthHeader.Load().(string); gotRedirectAuth != "" {
		t.Fatalf("SECURITY VIOLATION: cross-origin redirect leaked Authorization header %q", gotRedirectAuth)
	}
	if len(res.Redirects) != 1 || !res.Redirects[0].StrippedCredentials {
		t.Fatalf("expected RedirectHop to record StrippedCredentials=true, got: %+v", res.Redirects)
	}

	// Test RevokeToken clears local store and revokes remotely.
	revRes, err := svc.RevokeToken(context.Background(), req)
	if err != nil {
		t.Fatalf("RevokeToken failed: %v", err)
	}
	if !revRes.LocalCleared || !revRes.RemoteAttempted || !revRes.RemoteSucceeded {
		t.Fatalf("unexpected RevokeResult: %+v", revRes)
	}
	if revokedToken.Load() != "at_m2m_live_token_555" {
		t.Fatalf("expected remote revocation of at_m2m_live_token_555, got %v", revokedToken.Load())
	}
	cachedAfter, _ := svc.GetCachedToken(context.Background(), req)
	if cachedAfter != nil {
		t.Fatalf("expected local token cache to be nil after RevokeToken, got: %+v", cachedAfter)
	}

	// Test TargetPolicy blocks non-loopback plain HTTP token endpoint and TLS bypass.
	insecureReq := req
	insecureReq.Config.TokenURL = "http://idp.external.example.com/oauth/token"
	insecureReq.HasTargetPolicy = true
	insecureReq.TargetPolicy = MergeTargetPolicy(nil, nil)
	_, err = svc.AcquireClientCredentials(context.Background(), insecureReq)
	if !IsOAuthErrorCategory(err, OAuthErrTargetPolicyDenied) {
		t.Fatalf("expected non-loopback plain HTTP token endpoint to be blocked with OAuthErrTargetPolicyDenied, got: %v", err)
	}

	tlsBypassReq := req
	tlsBypassReq.Network = ExecuteOptions{
		TLS: TLSConfig{InsecureSkipVerify: true},
	}
	tlsBypassReq.HasTargetPolicy = true
	tlsBypassReq.TargetPolicy = MergeTargetPolicy(nil, nil)
	_, err = svc.AcquireClientCredentials(context.Background(), tlsBypassReq)
	if !IsOAuthErrorCategory(err, OAuthErrTargetPolicyDenied) {
		var oe *OAuthError
		if !errors.As(err, &oe) || oe.Category != OAuthErrTargetPolicyDenied {
			t.Fatalf("expected InsecureSkipVerify under EnforceTLSVerify to fail with OAuthErrTargetPolicyDenied, got: %v", err)
		}
	}
}
