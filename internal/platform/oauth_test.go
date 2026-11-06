package platform

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"gorequest/internal/core"
)

type memKeychainDriver struct {
	mu   sync.Mutex
	data map[string]string
	err  error
}

func newMemKeychainDriver() *memKeychainDriver {
	return &memKeychainDriver{data: make(map[string]string)}
}

func (d *memKeychainDriver) Get(key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return "", d.err
	}
	v, ok := d.data[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (d *memKeychainDriver) Set(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	if len(value) > MaxKeychainSecretBytes {
		return errors.New("exceeds MaxKeychainSecretBytes")
	}
	d.data[key] = value
	return nil
}

func (d *memKeychainDriver) Delete(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.err != nil {
		return d.err
	}
	delete(d.data, key)
	return nil
}

func TestKeychainOAuthTokenStoreCloneIsolationAndChunking(t *testing.T) {
	drv := newMemKeychainDriver()
	store := NewKeychainOAuthTokenStore(drv, "test.gorequest.oauth")

	cfg := &core.OAuthConfig{
		GrantType: core.OAuthGrantAuthorizationCode,
		Issuer:    "https://idp.example.com",
		ClientID:  "desktop-app",
		Scopes:    []string{"openid", "profile"},
	}

	cloneADir := t.TempDir()
	cloneBDir := t.TempDir()

	keyCloneA := core.BuildOAuthCacheKey(cloneADir, "my-api", "corp-sso", cfg, "oidc")
	keyCloneB := core.BuildOAuthCacheKey(cloneBDir, "my-api", "corp-sso", cfg, "oidc")
	if keyCloneA.StorageKey() == keyCloneB.StorageKey() {
		t.Fatalf("expected distinct StorageKey for different local project clones, got %q", keyCloneA.StorageKey())
	}

	ctx := context.Background()
	recA := &core.OAuthTokenRecord{
		AccessToken:  "at_clone_A_" + strings.Repeat("x", 2200),
		RefreshToken: "rt_clone_A_" + strings.Repeat("y", 2200),
		TokenType:    "Bearer",
		Scope:        "openid profile",
		ObtainedAt:   time.Now().UTC().Truncate(time.Second),
		ExpiresAt:    time.Now().Add(time.Hour).UTC().Truncate(time.Second),
	}
	if err := store.PutToken(ctx, keyCloneA, recA); err != nil {
		t.Fatalf("PutToken cloneA (chunked): %v", err)
	}

	// Clone B must not see Clone A's token.
	gotB, err := store.GetToken(ctx, keyCloneB)
	if err != nil || gotB != nil {
		t.Fatalf("expected nil token for Clone B, got rec=%+v err=%v", gotB, err)
	}

	// Read from a fresh KeychainOAuthTokenStore sharing the same underlying OS keychain driver (tests cold keychain load + chunk reassembly).
	coldStore := NewKeychainOAuthTokenStore(drv, "test.gorequest.oauth")
	gotA, err := coldStore.GetToken(ctx, keyCloneA)
	if err != nil || gotA == nil {
		t.Fatalf("GetToken cloneA from cold store failed: rec=%+v err=%v", gotA, err)
	}
	if gotA.AccessToken != recA.AccessToken || gotA.RefreshToken != recA.RefreshToken {
		t.Fatalf("chunked token round-trip mismatch")
	}

	// DeleteToken must clean up both main entry and all chunk entries.
	if err := coldStore.DeleteToken(ctx, keyCloneA); err != nil {
		t.Fatalf("DeleteToken cloneA failed: %v", err)
	}
	drv.mu.Lock()
	remaining := len(drv.data)
	drv.mu.Unlock()
	if remaining != 0 {
		t.Fatalf("expected 0 remaining keychain entries after DeleteToken, got %d", remaining)
	}
}

func TestKeychainOAuthTokenStoreUnsupportedNoPlaintextFallback(t *testing.T) {
	workDir := t.TempDir()
	drv := &memKeychainDriver{err: errors.ErrUnsupported}
	store := NewKeychainOAuthTokenStore(drv, "test.gorequest.oauth")

	key := core.BuildOAuthCacheKey(workDir, "proj", "oauth-scheme", &core.OAuthConfig{
		ClientID: "c1",
	}, "oauth2")

	err := store.PutToken(context.Background(), key, &core.OAuthTokenRecord{
		AccessToken: "secret_token_never_on_disk",
		ObtainedAt:  time.Now(),
	})
	if !errors.Is(err, errors.ErrUnsupported) {
		t.Fatalf("expected errors.ErrUnsupported when keychain backend is unavailable, got: %v", err)
	}
	var cfgErr *core.ConfigError
	if !errors.As(err, &cfgErr) {
		t.Fatalf("expected *core.ConfigError wrapper, got %T: %v", err, err)
	}

	// Verify zero plaintext fallback files were created in workDir.
	entries, readErr := os.ReadDir(workDir)
	if readErr != nil {
		t.Fatalf("ReadDir: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("SECURITY VIOLATION: unexpected files written to project dir when keychain unsupported: %v", entries)
	}
}

func TestLocalLoopbackListenerSingleUseAndBrowserURLValidation(t *testing.T) {
	// 1. Validate browser URL scheme allowlist.
	var openedURL string
	opener := &SystemBrowserOpener{
		OpenExternalFunc: func(targetURL string) error {
			openedURL = targetURL
			return nil
		},
	}
	if err := opener.OpenURL(context.Background(), "https://idp.example.com/authorize?client_id=123"); err != nil {
		t.Fatalf("expected https URL to be allowed: %v", err)
	}
	if openedURL != "https://idp.example.com/authorize?client_id=123" {
		t.Fatalf("unexpected openedURL %q", openedURL)
	}
	for _, disallowed := range []string{
		"file:///etc/passwd",
		"javascript:alert(1)",
		"https://idp.example.com/auth\r\nX-Injected: 1",
		"custom-scheme://login",
	} {
		if err := opener.OpenURL(context.Background(), disallowed); err == nil {
			t.Fatalf("expected disallowed browser URL %q to be rejected", disallowed)
		}
	}

	// 2. Reject non-loopback redirectUri in LocalLoopbackFactory.
	factory := NewLocalLoopbackFactory()
	if _, err := factory.StartLoopback(context.Background(), "http://evil.example.com:8080/cb"); err == nil {
		t.Fatalf("expected non-loopback redirectUri to be rejected")
	}

	// 3. Ephemeral loopback callback lifecycle.
	sess, err := factory.StartLoopback(context.Background(), "")
	if err != nil {
		t.Fatalf("StartLoopback: %v", err)
	}
	defer func() { _ = sess.Close() }()

	redirectURI := sess.RedirectURI()
	if !strings.HasPrefix(redirectURI, "http://127.0.0.1:") || !strings.HasSuffix(redirectURI, "/oauth/callback") {
		t.Fatalf("unexpected RedirectURI %q", redirectURI)
	}

	// Request to an unrelated path (/favicon.ico) must 404 and NOT consume the callback slot.
	baseOrigin := strings.TrimSuffix(redirectURI, "/oauth/callback")
	favResp, err := http.Get(baseOrigin + "/favicon.ico")
	if err != nil {
		t.Fatalf("GET /favicon.ico: %v", err)
	}
	_ = favResp.Body.Close()
	if favResp.StatusCode != http.StatusNotFound {
		t.Fatalf("expected 404 for /favicon.ico, got %d", favResp.StatusCode)
	}

	// Deliver actual OAuth callback.
	cbResp, err := http.Get(redirectURI + "?code=loopback_code_abc&state=loopback_state_xyz")
	if err != nil {
		t.Fatalf("GET callback: %v", err)
	}
	_ = cbResp.Body.Close()
	if cbResp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 OK on callback, got %d", cbResp.StatusCode)
	}
	if !strings.Contains(cbResp.Header.Get("Cache-Control"), "no-store") {
		t.Fatalf("expected Cache-Control: no-store on callback response, got %q", cbResp.Header.Get("Cache-Control"))
	}

	waitCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	res, err := sess.WaitForCallback(waitCtx)
	if err != nil {
		t.Fatalf("WaitForCallback: %v", err)
	}
	if res.Code != "loopback_code_abc" || res.State != "loopback_state_xyz" {
		t.Fatalf("unexpected callback result: %+v", res)
	}
}
