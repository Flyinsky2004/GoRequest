package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"gorequest/internal/core"
)

const (
	// DefaultOAuthKeychainService is the OS credential manager service namespace for OAuth tokens.
	DefaultOAuthKeychainService = "dev.gorequest.oauth"
	// maxKeychainChunkPayloadBytes leaves room inside MaxKeychainSecretBytes (2560 on Windows) for UTF-8 safety.
	maxKeychainChunkPayloadBytes = 2048
	// maxKeychainChunks caps chunked token storage to 16 chunks (32 KiB).
	maxKeychainChunks = 16
)

// KeychainOAuthTokenStore persists OAuth/OIDC tokens in the OS Keychain isolated by OAuthCacheKey.
// It strictly refuses any plaintext file fallback when the OS Keychain backend is unavailable.
type KeychainOAuthTokenStore struct {
	driver  KeychainDriver
	service string
	session *core.MemoryOAuthTokenStore
	mu      sync.Mutex
}

// NewKeychainOAuthTokenStore creates an OS Keychain-backed OAuthTokenStore.
func NewKeychainOAuthTokenStore(driver KeychainDriver, service string) *KeychainOAuthTokenStore {
	if strings.TrimSpace(service) == "" {
		service = DefaultOAuthKeychainService
	}
	if driver == nil {
		driver = NewMyGoKeychainDriver(service)
	}
	return &KeychainOAuthTokenStore{
		driver:  driver,
		service: service,
		session: core.NewMemoryOAuthTokenStore(),
	}
}

type compactKeychainTokenEnvelope struct {
	Chunks     int    `json:"chunks,omitempty"`
	Access     string `json:"a,omitempty"`
	Type       string `json:"t,omitempty"`
	Refresh    string `json:"r,omitempty"`
	ID         string `json:"i,omitempty"`
	Scope      string `json:"s,omitempty"`
	ExpiresAt  int64  `json:"e,omitempty"`
	ObtainedAt int64  `json:"o"`
	Issuer     string `json:"iss,omitempty"`
	ClientID   string `json:"cid,omitempty"`
	Subject    string `json:"sub,omitempty"`
	GrantType  string `json:"gt,omitempty"`
	Verified   bool   `json:"v,omitempty"`
	VerifiedAt int64  `json:"va,omitempty"`
}

func (s *KeychainOAuthTokenStore) requireDriver(op string) error {
	if s.driver == nil {
		return &core.ConfigError{
			Err: fmt.Errorf("oauth token %s failed: system credential store is unavailable (plaintext fallback disabled): %w", op, errors.ErrUnsupported),
		}
	}
	return nil
}

func (s *KeychainOAuthTokenStore) wrapDriverErr(op, account string, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, errors.ErrUnsupported) {
		return &core.ConfigError{
			Err: fmt.Errorf("oauth token %s for %q failed: system credential backend is unsupported on this host (plaintext fallback disabled): %w", op, account, errors.ErrUnsupported),
		}
	}
	return &core.ConfigError{
		Err: fmt.Errorf("oauth token %s for %q failed: %w", op, account, err),
	}
}

// GetToken retrieves a token record from the in-memory session cache or the OS Keychain.
func (s *KeychainOAuthTokenStore) GetToken(ctx context.Context, key core.OAuthCacheKey) (*core.OAuthTokenRecord, error) {
	if err := s.requireDriver("read"); err != nil {
		return nil, err
	}
	if cached, _ := s.session.GetToken(ctx, key); cached != nil {
		return cached, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	account := key.StorageKey()
	raw, err := s.driver.Get(account)
	if err != nil {
		if errors.Is(err, ErrSecretNotFound) {
			return nil, nil
		}
		return nil, s.wrapDriverErr("read", account, err)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var env compactKeychainTokenEnvelope
	if err := json.Unmarshal([]byte(raw), &env); err != nil {
		return nil, &core.ConfigError{
			Err: fmt.Errorf("corrupted OAuth token record in system keychain for %q: %w", account, err),
		}
	}

	if env.Chunks > 0 {
		if env.Chunks > maxKeychainChunks {
			return nil, &core.ConfigError{
				Err: fmt.Errorf("keychain OAuth token chunk count %d exceeds maximum %d", env.Chunks, maxKeychainChunks),
			}
		}
		var b strings.Builder
		for idx := 0; idx < env.Chunks; idx++ {
			chunkKey := fmt.Sprintf("%s#%d", account, idx)
			part, chunkErr := s.driver.Get(chunkKey)
			if chunkErr != nil {
				if errors.Is(chunkErr, ErrSecretNotFound) {
					return nil, nil
				}
				return nil, s.wrapDriverErr("read", chunkKey, chunkErr)
			}
			b.WriteString(part)
		}
		if err := json.Unmarshal([]byte(b.String()), &env); err != nil {
			return nil, &core.ConfigError{
				Err: fmt.Errorf("corrupted chunked OAuth token record in system keychain for %q: %w", account, err),
			}
		}
	}

	rec := expandKeychainEnvelope(env)
	_ = s.session.PutToken(ctx, key, rec)
	return rec, nil
}

// PutToken stores a token record in the OS Keychain and updates the session cache.
func (s *KeychainOAuthTokenStore) PutToken(ctx context.Context, key core.OAuthCacheKey, record *core.OAuthTokenRecord) error {
	if err := s.requireDriver("write"); err != nil {
		return err
	}
	if record == nil {
		return s.DeleteToken(ctx, key)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	account := key.StorageKey()
	env := toKeychainEnvelope(record)
	encoded, err := json.Marshal(env)
	if err != nil {
		return &core.ConfigError{
			Err: fmt.Errorf("failed to encode OAuth token record for %q: %w", account, err),
		}
	}

	payload := string(encoded)
	if len(payload) <= maxKeychainChunkPayloadBytes {
		if err := s.driver.Set(account, payload); err != nil {
			return s.wrapDriverErr("write", account, err)
		}
	} else {
		chunks := splitStringChunks(payload, maxKeychainChunkPayloadBytes)
		if len(chunks) > maxKeychainChunks {
			return &core.ConfigError{
				Err: fmt.Errorf("OAuth token bundle size (%d bytes) exceeds keychain capacity", len(payload)),
			}
		}
		for idx, chunk := range chunks {
			chunkKey := fmt.Sprintf("%s#%d", account, idx)
			if err := s.driver.Set(chunkKey, chunk); err != nil {
				return s.wrapDriverErr("write", chunkKey, err)
			}
		}
		headerJSON, _ := json.Marshal(compactKeychainTokenEnvelope{Chunks: len(chunks)})
		if err := s.driver.Set(account, string(headerJSON)); err != nil {
			return s.wrapDriverErr("write", account, err)
		}
	}

	_ = s.session.PutToken(ctx, key, record)
	return nil
}

// DeleteToken removes a token record from both the session cache and the OS Keychain.
func (s *KeychainOAuthTokenStore) DeleteToken(ctx context.Context, key core.OAuthCacheKey) error {
	if err := s.requireDriver("delete"); err != nil {
		return err
	}
	_ = s.session.DeleteToken(ctx, key)

	s.mu.Lock()
	defer s.mu.Unlock()

	account := key.StorageKey()
	if raw, err := s.driver.Get(account); err == nil && raw != "" {
		var env compactKeychainTokenEnvelope
		if json.Unmarshal([]byte(raw), &env) == nil && env.Chunks > 0 && env.Chunks <= maxKeychainChunks {
			for idx := 0; idx < env.Chunks; idx++ {
				_ = s.driver.Delete(fmt.Sprintf("%s#%d", account, idx))
			}
		}
	}
	if err := s.driver.Delete(account); err != nil && !errors.Is(err, ErrSecretNotFound) {
		return s.wrapDriverErr("delete", account, err)
	}
	return nil
}

func toKeychainEnvelope(rec *core.OAuthTokenRecord) compactKeychainTokenEnvelope {
	env := compactKeychainTokenEnvelope{
		Access:     rec.AccessToken,
		Type:       rec.TokenType,
		Refresh:    rec.RefreshToken,
		ID:         rec.IDToken,
		Scope:      rec.Scope,
		ObtainedAt: rec.ObtainedAt.Unix(),
		Issuer:     rec.Issuer,
		ClientID:   rec.ClientID,
		Subject:    rec.Subject,
		GrantType:  rec.GrantType,
	}
	if !rec.ExpiresAt.IsZero() {
		env.ExpiresAt = rec.ExpiresAt.Unix()
	}
	if rec.IDTokenInspection != nil && rec.IDTokenInspection.Verified {
		env.Verified = true
		if !rec.IDTokenInspection.VerifiedAt.IsZero() {
			env.VerifiedAt = rec.IDTokenInspection.VerifiedAt.Unix()
		}
	}
	return env
}

func expandKeychainEnvelope(env compactKeychainTokenEnvelope) *core.OAuthTokenRecord {
	rec := &core.OAuthTokenRecord{
		AccessToken:  env.Access,
		TokenType:    env.Type,
		RefreshToken: env.Refresh,
		IDToken:      env.ID,
		Scope:        env.Scope,
		ObtainedAt:   time.Unix(env.ObtainedAt, 0).UTC(),
		Issuer:       env.Issuer,
		ClientID:     env.ClientID,
		Subject:      env.Subject,
		GrantType:    env.GrantType,
	}
	if env.ExpiresAt > 0 {
		rec.ExpiresAt = time.Unix(env.ExpiresAt, 0).UTC()
	}
	if rec.IDToken != "" {
		if insp, err := core.InspectIDTokenUnverified(rec.IDToken); err == nil {
			if env.Verified {
				insp.Verified = true
				insp.Warning = ""
				if env.VerifiedAt > 0 {
					insp.VerifiedAt = time.Unix(env.VerifiedAt, 0).UTC()
				}
			}
			rec.IDTokenInspection = insp
			if rec.Subject == "" {
				rec.Subject = insp.Claims.Subject
			}
		}
	}
	return rec
}

func splitStringChunks(s string, chunkSize int) []string {
	if chunkSize <= 0 || len(s) <= chunkSize {
		return []string{s}
	}
	var out []string
	for len(s) > 0 {
		n := chunkSize
		if n > len(s) {
			n = len(s)
		}
		out = append(out, s[:n])
		s = s[n:]
	}
	return out
}

// SystemBrowserOpener opens an authorization URL in the user's system browser via mygo.Shell.OpenExternal.
// It validates the URL scheme to ensure only https:// or loopback/http:// URLs can be opened.
type SystemBrowserOpener struct {
	OpenExternalFunc func(targetURL string) error
}

// NewSystemBrowserOpener creates a SystemBrowserOpener backed by mygo.Shell.OpenExternal.
func NewSystemBrowserOpener() *SystemBrowserOpener {
	return &SystemBrowserOpener{
		OpenExternalFunc: func(targetURL string) error {
			return mygo.Shell.OpenExternal(targetURL)
		},
	}
}

// OpenURL validates authorizationURL and launches the system browser.
func (b *SystemBrowserOpener) OpenURL(ctx context.Context, authorizationURL string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := ValidateBrowserAuthorizationURL(authorizationURL); err != nil {
		return err
	}
	openFn := b.OpenExternalFunc
	if openFn == nil {
		openFn = func(targetURL string) error {
			return mygo.Shell.OpenExternal(targetURL)
		}
	}
	return openFn(authorizationURL)
}

// ValidateBrowserAuthorizationURL ensures only safe HTTP(S) authorization URLs are passed to the OS shell.
func ValidateBrowserAuthorizationURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return fmt.Errorf("authorization URL is empty")
	}
	if strings.ContainsAny(trimmed, "\r\n\x00") {
		return fmt.Errorf("authorization URL contains control characters")
	}
	u, err := url.Parse(trimmed)
	if err != nil || u.Host == "" {
		return fmt.Errorf("invalid authorization URL %q", trimmed)
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme != "https" && scheme != "http" {
		return fmt.Errorf("disallowed browser URL scheme %q (only https and http are permitted)", u.Scheme)
	}
	return nil
}

// LocalLoopbackFactory starts ephemeral single-callback loopback HTTP servers on 127.0.0.1 (or [::1]).
type LocalLoopbackFactory struct {
	DefaultHost string
	DefaultPath string
}

// NewLocalLoopbackFactory creates a LocalLoopbackFactory binding 127.0.0.1:0 by default.
func NewLocalLoopbackFactory() *LocalLoopbackFactory {
	return &LocalLoopbackFactory{
		DefaultHost: "127.0.0.1",
		DefaultPath: core.DefaultLoopbackCallbackPath,
	}
}

// StartLoopback binds a local loopback listener and serves a single OAuth callback.
func (f *LocalLoopbackFactory) StartLoopback(ctx context.Context, preferredRedirectURI string) (core.LoopbackSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	host := "127.0.0.1"
	if f != nil && strings.TrimSpace(f.DefaultHost) != "" {
		host = strings.TrimSpace(f.DefaultHost)
	}
	callbackPath := core.DefaultLoopbackCallbackPath
	if f != nil && strings.TrimSpace(f.DefaultPath) != "" {
		callbackPath = strings.TrimSpace(f.DefaultPath)
	}
	port := "0"
	redirectHost := host

	if strings.TrimSpace(preferredRedirectURI) != "" {
		u, err := url.Parse(strings.TrimSpace(preferredRedirectURI))
		if err != nil {
			return nil, fmt.Errorf("invalid redirectUri %q: %w", preferredRedirectURI, err)
		}
		if !strings.EqualFold(u.Scheme, "http") {
			return nil, fmt.Errorf("loopback redirectUri must use http:// scheme (got %q)", u.Scheme)
		}
		hostname := u.Hostname()
		if !isLoopbackHost(hostname) {
			return nil, fmt.Errorf("loopback redirectUri host %q must be 127.0.0.1, ::1, or localhost", hostname)
		}
		redirectHost = hostname
		if strings.EqualFold(hostname, "localhost") {
			host = "127.0.0.1"
		} else {
			host = hostname
		}
		if p := u.Port(); p != "" {
			if n, pErr := strconv.Atoi(p); pErr != nil || n < 0 || n > 65535 {
				return nil, fmt.Errorf("invalid loopback port %q", p)
			}
			port = p
		}
		if u.Path != "" {
			callbackPath = u.Path
		}
	}
	if !strings.HasPrefix(callbackPath, "/") {
		callbackPath = "/" + callbackPath
	}

	listenAddr := net.JoinHostPort(host, port)
	ln, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to bind loopback listener on %s: %w", listenAddr, err)
	}

	tcpAddr, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return nil, fmt.Errorf("unexpected loopback listener address type %T", ln.Addr())
	}
	actualPort := strconv.Itoa(tcpAddr.Port)
	redirectURI := (&url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(redirectHost, actualPort),
		Path:   callbackPath,
	}).String()

	sess := &localLoopbackSession{
		listener:    ln,
		redirectURI: redirectURI,
		path:        callbackPath,
		resultCh:    make(chan core.LoopbackCallbackResult, 1),
	}

	mux := http.NewServeMux()
	mux.HandleFunc(callbackPath, sess.handleCallback)
	sess.server = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	go func() {
		_ = sess.server.Serve(ln)
	}()

	return sess, nil
}

func isLoopbackHost(host string) bool {
	h := strings.ToLower(strings.TrimSpace(host))
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

type localLoopbackSession struct {
	listener    net.Listener
	server      *http.Server
	redirectURI string
	path        string
	resultOnce  sync.Once
	closeOnce   sync.Once
	resultCh    chan core.LoopbackCallbackResult
}

func (s *localLoopbackSession) RedirectURI() string {
	return s.redirectURI
}

func (s *localLoopbackSession) handleCallback(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	w.Header().Set("X-Content-Type-Options", "nosniff")

	if r.Method != http.MethodGet {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != s.path {
		w.WriteHeader(http.StatusNotFound)
		return
	}

	q := r.URL.Query()
	res := core.LoopbackCallbackResult{
		Code:             q.Get("code"),
		State:            q.Get("state"),
		Error:            q.Get("error"),
		ErrorDescription: q.Get("error_description"),
	}

	accepted := false
	s.resultOnce.Do(func() {
		accepted = true
		s.resultCh <- res
	})
	if !accepted {
		w.WriteHeader(http.StatusGone)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if res.Error != "" || res.Code == "" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(loopbackFailureHTML))
	} else {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(loopbackSuccessHTML))
	}

	go func() {
		_ = s.Close()
	}()
}

func (s *localLoopbackSession) WaitForCallback(ctx context.Context) (core.LoopbackCallbackResult, error) {
	select {
	case <-ctx.Done():
		_ = s.Close()
		return core.LoopbackCallbackResult{}, ctx.Err()
	case res := <-s.resultCh:
		_ = s.Close()
		return res, nil
	}
}

func (s *localLoopbackSession) Close() error {
	var closeErr error
	s.closeOnce.Do(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		if s.server != nil {
			closeErr = s.server.Shutdown(shutdownCtx)
		}
		if s.listener != nil {
			_ = s.listener.Close()
		}
	})
	return closeErr
}

const loopbackSuccessHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>GoRequest — Authorization Complete</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
.card { background: #1e293b; border: 1px solid #334155; border-radius: 12px; padding: 32px 40px; max-width: 420px; text-align: center; box-shadow: 0 10px 25px rgba(0,0,0,0.4); }
.badge { display: inline-block; background: #065f46; color: #a7f3d0; padding: 4px 12px; border-radius: 999px; font-size: 12px; font-weight: 600; margin-bottom: 12px; }
h1 { font-size: 20px; margin: 0 0 8px; }
p { font-size: 14px; color: #94a3b8; margin: 0; line-height: 1.5; }
</style>
</head>
<body>
<div class="card">
  <div class="badge">OAuth 2.0 / OIDC</div>
  <h1>Sign-in Complete</h1>
  <p>Authorization response received by GoRequest. You may safely close this browser tab and return to the application.</p>
</div>
</body>
</html>`

const loopbackFailureHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<title>GoRequest — Authorization Failed</title>
<style>
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #0f172a; color: #f8fafc; display: flex; align-items: center; justify-content: center; height: 100vh; margin: 0; }
.card { background: #1e293b; border: 1px solid #7f1d1d; border-radius: 12px; padding: 32px 40px; max-width: 420px; text-align: center; box-shadow: 0 10px 25px rgba(0,0,0,0.4); }
.badge { display: inline-block; background: #7f1d1d; color: #fecaca; padding: 4px 12px; border-radius: 999px; font-size: 12px; font-weight: 600; margin-bottom: 12px; }
h1 { font-size: 20px; margin: 0 0 8px; }
p { font-size: 14px; color: #94a3b8; margin: 0; line-height: 1.5; }
</style>
</head>
<body>
<div class="card">
  <div class="badge">Authorization Error</div>
  <h1>Sign-in Not Completed</h1>
  <p>The authorization server did not complete sign-in. Please return to GoRequest to view details or try again.</p>
</div>
</body>
</html>`
