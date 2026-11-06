package core

import (
	"context"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// OAuthServiceConfig configures an OAuthService instance and its injectable platform ports.
type OAuthServiceConfig struct {
	TokenStore      OAuthTokenStore
	BrowserOpener   BrowserOpener
	LoopbackFactory LoopbackListenerFactory
	HTTPDoer        OAuthHTTPDoer
	JWKSManager     *JWKSManager
	ExpirySkew      time.Duration
	Now             func() time.Time
	Sleep           func(ctx context.Context, d time.Duration) error
}

// OAuthService coordinates OAuth 2.0 flows, singleflight token refresh/rotation, OIDC verification, and revocation.
type OAuthService struct {
	store           OAuthTokenStore
	browser         BrowserOpener
	loopback        LoopbackListenerFactory
	httpDoer        OAuthHTTPDoer
	jwks            *JWKSManager
	expirySkew      time.Duration
	nowFn           func() time.Time
	sleepFn         func(ctx context.Context, d time.Duration) error
	discoveryMu     sync.RWMutex
	discoveryCache  map[string]*OIDCDiscoveryDocument
	singleflightMu  sync.Mutex
	inflightRefresh map[string]*inflightTokenCall
}

type inflightTokenCall struct {
	done   chan struct{}
	record *OAuthTokenRecord
	err    error
}

// NewOAuthService constructs an OAuthService with safe defaults.
func NewOAuthService(cfg OAuthServiceConfig) *OAuthService {
	store := cfg.TokenStore
	if store == nil {
		store = NewMemoryOAuthTokenStore()
	}
	jwks := cfg.JWKSManager
	if jwks == nil {
		jwks = NewJWKSManager()
	}
	skew := cfg.ExpirySkew
	if skew <= 0 {
		skew = DefaultTokenExpirySkew
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	if cfg.JWKSManager == nil {
		jwks.Now = nowFn
	}
	sleepFn := cfg.Sleep
	if sleepFn == nil {
		sleepFn = defaultContextSleep
	}
	return &OAuthService{
		store:           store,
		browser:         cfg.BrowserOpener,
		loopback:        cfg.LoopbackFactory,
		httpDoer:        cfg.HTTPDoer,
		jwks:            jwks,
		expirySkew:      skew,
		nowFn:           nowFn,
		sleepFn:         sleepFn,
		discoveryCache:  make(map[string]*OIDCDiscoveryDocument),
		inflightRefresh: make(map[string]*inflightTokenCall),
	}
}

func defaultContextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// TokenStore returns the underlying OAuthTokenStore port.
func (s *OAuthService) TokenStore() OAuthTokenStore {
	return s.store
}

// JWKSManager returns the bounded JWKS cache manager.
func (s *OAuthService) JWKSManager() *JWKSManager {
	return s.jwks
}

// OAuthFlowRequest encapsulates the resolved context for executing an OAuth 2.0 / OIDC operation.
type OAuthFlowRequest struct {
	ProjectRoot     string
	ProjectSlug     string
	SchemeID        string
	SchemeType      string // "oauth2" or "oidc"
	Config          OAuthConfig
	Vars            map[string]string
	Secrets         SecretProvider
	SecretValues    map[string]string
	Network         ExecuteOptions
	TargetPolicy    MergedTargetPolicy
	HasTargetPolicy bool
	EnvID           string
	Confirmed       bool
	Redactor        *Redactor
	Interactive     bool
	ForceRefresh    bool
	StaticJWKS      *JWKSet
}

type mapSecretFallback struct {
	primary SecretProvider
	extra   map[string]string
	red     *Redactor
}

func (m mapSecretFallback) Get(name string) (string, bool) {
	if m.primary != nil {
		if v, ok := m.primary.Get(name); ok {
			safeRegisterSecret(m.red, v)
			return v, true
		}
	}
	if len(m.extra) > 0 {
		if v, ok := m.extra[name]; ok {
			safeRegisterSecret(m.red, v)
			return v, true
		}
	}
	return "", false
}

func (req *OAuthFlowRequest) fetchOptions(expectedIssuer string) OIDCFetchOptions {
	return OIDCFetchOptions{
		Network:         req.Network,
		TargetPolicy:    req.TargetPolicy,
		HasTargetPolicy: req.HasTargetPolicy,
		EnvID:           req.EnvID,
		Interactive:     req.Interactive,
		Confirmed:       req.Confirmed,
		Redactor:        req.Redactor,
		ExpectedIssuer:  expectedIssuer,
	}
}

// ResolveConfig expands {{var}}, {{env.*}}, and {{secret.*}} placeholders in OAuthConfig
// and registers any resolved ClientSecret into Redactor.
func (req *OAuthFlowRequest) ResolveConfig() (OAuthConfig, error) {
	cfg := req.Config.Clone()
	if cfg == nil {
		cfg = &OAuthConfig{}
	}
	mergedVars := MergeVars(req.Vars)
	var sp SecretProvider = mapSecretFallback{
		primary: req.Secrets,
		extra:   req.SecretValues,
		red:     req.Redactor,
	}
	r := NewResolver(mergedVars, sp)

	resolveField := func(field, val string) (string, error) {
		if strings.TrimSpace(val) == "" {
			return "", nil
		}
		out, err := r.Resolve(val)
		if err != nil {
			return "", &OAuthError{
				Category: OAuthErrInvalidConfig,
				Message:  fmt.Sprintf("failed to resolve %s in scheme %q: %v", field, req.SchemeID, err),
				Cause:    err,
			}
		}
		return strings.TrimSpace(out), nil
	}

	var err error
	if cfg.Issuer, err = resolveField("oauth.issuer", cfg.Issuer); err != nil {
		return *cfg, err
	}
	if cfg.DiscoveryURL, err = resolveField("oauth.discoveryUrl", cfg.DiscoveryURL); err != nil {
		return *cfg, err
	}
	if cfg.AuthorizationURL, err = resolveField("oauth.authorizationUrl", cfg.AuthorizationURL); err != nil {
		return *cfg, err
	}
	if cfg.TokenURL, err = resolveField("oauth.tokenUrl", cfg.TokenURL); err != nil {
		return *cfg, err
	}
	if cfg.DeviceAuthorizationURL, err = resolveField("oauth.deviceAuthorizationUrl", cfg.DeviceAuthorizationURL); err != nil {
		return *cfg, err
	}
	if cfg.RevocationURL, err = resolveField("oauth.revocationUrl", cfg.RevocationURL); err != nil {
		return *cfg, err
	}
	if cfg.JWKSURI, err = resolveField("oauth.jwksUri", cfg.JWKSURI); err != nil {
		return *cfg, err
	}
	if cfg.ClientID, err = resolveField("oauth.clientId", cfg.ClientID); err != nil {
		return *cfg, err
	}
	if cfg.ClientSecret, err = resolveField("oauth.clientSecret", cfg.ClientSecret); err != nil {
		return *cfg, err
	}
	if cfg.Audience, err = resolveField("oauth.audience", cfg.Audience); err != nil {
		return *cfg, err
	}
	if cfg.Resource, err = resolveField("oauth.resource", cfg.Resource); err != nil {
		return *cfg, err
	}
	if cfg.RedirectURI, err = resolveField("oauth.redirectUri", cfg.RedirectURI); err != nil {
		return *cfg, err
	}

	if cfg.ClientSecret != "" {
		safeRegisterSecret(req.Redactor, cfg.ClientSecret)
	}
	if len(cfg.ExtraParams) > 0 {
		resolvedExtra := make(map[string]string, len(cfg.ExtraParams))
		for k, v := range cfg.ExtraParams {
			rv, rErr := resolveField("oauth.extraParams."+k, v)
			if rErr != nil {
				return *cfg, rErr
			}
			resolvedExtra[k] = rv
		}
		cfg.ExtraParams = resolvedExtra
	}
	return *cfg, nil
}

// CacheKey computes the 5-tuple isolation key for this request.
func (req *OAuthFlowRequest) CacheKey(resolvedCfg *OAuthConfig) OAuthCacheKey {
	cfg := resolvedCfg
	if cfg == nil {
		c := req.Config
		cfg = &c
	}
	return BuildOAuthCacheKey(req.ProjectRoot, req.ProjectSlug, req.SchemeID, cfg, req.SchemeType)
}

// ResolveEndpoints populates missing AuthorizationURL, TokenURL, DeviceAuthorizationURL, RevocationURL, and JWKSURI
// from OIDC Discovery when Issuer or DiscoveryURL is configured.
func (s *OAuthService) ResolveEndpoints(ctx context.Context, req OAuthFlowRequest) (OAuthConfig, *OIDCDiscoveryDocument, error) {
	cfg, err := req.ResolveConfig()
	if err != nil {
		return cfg, nil, err
	}

	discoveryTarget := strings.TrimSpace(cfg.DiscoveryURL)
	if discoveryTarget == "" {
		discoveryTarget = strings.TrimSpace(cfg.Issuer)
	}
	needsDiscovery := discoveryTarget != "" && (cfg.TokenURL == "" ||
		(cfg.NormalizedGrantType() == OAuthGrantAuthorizationCode && cfg.AuthorizationURL == "") ||
		(cfg.NormalizedGrantType() == OAuthGrantDeviceCode && cfg.DeviceAuthorizationURL == "") ||
		(cfg.WantsOIDC(req.SchemeType) && cfg.JWKSURI == ""))

	var doc *OIDCDiscoveryDocument
	if needsDiscovery {
		doc, err = s.GetOrFetchDiscovery(ctx, discoveryTarget, cfg.Issuer, req)
		if err != nil {
			return cfg, nil, err
		}
		if cfg.Issuer == "" {
			cfg.Issuer = doc.Issuer
		}
		if cfg.AuthorizationURL == "" {
			cfg.AuthorizationURL = doc.AuthorizationEndpoint
		}
		if cfg.TokenURL == "" {
			cfg.TokenURL = doc.TokenEndpoint
		}
		if cfg.DeviceAuthorizationURL == "" {
			cfg.DeviceAuthorizationURL = doc.DeviceAuthorizationEndpoint
		}
		if cfg.RevocationURL == "" {
			cfg.RevocationURL = doc.RevocationEndpoint
		}
		if cfg.JWKSURI == "" {
			cfg.JWKSURI = doc.JWKSURI
		}
	}
	return cfg, doc, nil
}

// GetOrFetchDiscovery returns a cached OIDC Discovery document or fetches and validates it from the network.
func (s *OAuthService) GetOrFetchDiscovery(ctx context.Context, issuerOrDiscoveryURL, expectedIssuer string, req OAuthFlowRequest) (*OIDCDiscoveryDocument, error) {
	discURL, _, err := BuildDiscoveryURL(issuerOrDiscoveryURL)
	if err != nil {
		return nil, err
	}
	s.discoveryMu.RLock()
	cached, ok := s.discoveryCache[discURL]
	s.discoveryMu.RUnlock()
	if ok && cached != nil {
		if expectedIssuer != "" && cached.Issuer != expectedIssuer {
			return nil, &OAuthError{
				Category: OAuthErrDiscoveryMismatch,
				Endpoint: sanitizeURLForDisplay(discURL, req.Redactor),
				Message:  fmt.Sprintf("OIDC discovery issuer mismatch: expected %q, got %q", expectedIssuer, cached.Issuer),
			}
		}
		cp := *cached
		return &cp, nil
	}

	fopts := req.fetchOptions(expectedIssuer)
	fopts.HTTPDoer = s.httpDoer
	doc, err := FetchOIDCDiscovery(ctx, issuerOrDiscoveryURL, fopts)
	if err != nil {
		return nil, err
	}
	s.discoveryMu.Lock()
	if s.discoveryCache == nil {
		s.discoveryCache = make(map[string]*OIDCDiscoveryDocument)
	}
	s.discoveryCache[discURL] = doc
	s.discoveryMu.Unlock()

	cp := *doc
	return &cp, nil
}

// AcquireClientCredentials executes the OAuth 2.0 Client Credentials Grant (RFC 6749 §4.4).
func (s *OAuthService) AcquireClientCredentials(ctx context.Context, req OAuthFlowRequest) (*OAuthTokenRecord, error) {
	cfg, _, err := s.ResolveEndpoints(ctx, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "tokenUrl (or OIDC issuer/discoveryUrl) is required for client_credentials grant",
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "clientId is required for client_credentials grant",
		}
	}

	form := url.Values{}
	form.Set("grant_type", OAuthGrantClientCredentials)
	if scopes := cfg.NormalizedScopes(req.SchemeType); len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	if cfg.Audience != "" {
		form.Set("audience", cfg.Audience)
	}
	if cfg.Resource != "" {
		form.Set("resource", cfg.Resource)
	}
	for k, v := range cfg.ExtraParams {
		if k != "" && form.Get(k) == "" {
			form.Set(k, v)
		}
	}

	rec, err := s.exchangeTokenForm(ctx, cfg, req, form, "")
	if err != nil {
		return nil, err
	}
	rec.GrantType = OAuthGrantClientCredentials
	if err := s.persistToken(ctx, req.CacheKey(&cfg), rec); err != nil {
		return nil, err
	}
	return rec, nil
}

// AuthorizePKCE executes the interactive Authorization Code + PKCE S256 flow via System Browser and Loopback Listener.
func (s *OAuthService) AuthorizePKCE(ctx context.Context, req OAuthFlowRequest) (*OAuthTokenRecord, error) {
	if !req.Interactive {
		return nil, &OAuthError{
			Category: OAuthErrInteractionRequired,
			Message:  "interactive authorization_code PKCE flow cannot be started in non-interactive mode",
		}
	}
	if s.loopback == nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "loopback listener port is not configured for PKCE authorization",
		}
	}
	if s.browser == nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "system browser opener port is not configured for PKCE authorization",
		}
	}

	cfg, _, err := s.ResolveEndpoints(ctx, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.AuthorizationURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "authorizationUrl (or OIDC issuer/discoveryUrl) is required for authorization_code PKCE flow",
		}
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "tokenUrl (or OIDC issuer/discoveryUrl) is required for authorization_code PKCE flow",
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "clientId is required for authorization_code PKCE flow",
		}
	}
	if err := evaluateOAuthEndpointPolicy(cfg.AuthorizationURL, http.MethodGet, false, req.fetchOptions(cfg.Issuer)); err != nil {
		return nil, err
	}

	verifier, challenge, err := GeneratePKCEVerifierAndChallenge()
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  err.Error(),
			Cause:    err,
		}
	}
	safeRegisterSecret(req.Redactor, verifier)

	state, err := GenerateRandomToken(24)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  err.Error(),
			Cause:    err,
		}
	}

	var nonce string
	if cfg.WantsOIDC(req.SchemeType) {
		nonce, err = GenerateRandomToken(24)
		if err != nil {
			return nil, &OAuthError{
				Category: OAuthErrInvalidConfig,
				Message:  err.Error(),
				Cause:    err,
			}
		}
	}

	session, err := s.loopback.StartLoopback(ctx, cfg.RedirectURI)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrCallbackFailed,
			Message:  "failed to start ephemeral loopback listener",
			Cause:    err,
		}
	}
	defer func() {
		_ = session.Close()
	}()

	redirectURI := session.RedirectURI()
	authURL, err := buildAuthorizationURL(cfg, req.SchemeType, redirectURI, state, challenge, nonce)
	if err != nil {
		return nil, err
	}

	if err := s.browser.OpenURL(ctx, authURL); err != nil {
		return nil, &OAuthError{
			Category: OAuthErrBrowserOpenFailed,
			Endpoint: sanitizeURLForDisplay(cfg.AuthorizationURL, req.Redactor),
			Message:  "failed to open system browser for OAuth authorization",
			Cause:    err,
		}
	}

	cbResult, err := session.WaitForCallback(ctx)
	_ = session.Close()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, err
		}
		return nil, &OAuthError{
			Category: OAuthErrCallbackFailed,
			Message:  "failed while waiting for loopback OAuth callback",
			Cause:    err,
		}
	}

	if cbResult.State == "" || subtle.ConstantTimeCompare([]byte(state), []byte(cbResult.State)) != 1 {
		return nil, &OAuthError{
			Category: OAuthErrStateMismatch,
			Message:  "OAuth callback state parameter is missing or does not match the initiated authorization request",
		}
	}

	if cbResult.Error != "" {
		cat := OAuthErrCallbackFailed
		if cbResult.Error == "access_denied" {
			cat = OAuthErrAccessDenied
		}
		oe := &OAuthError{
			Category:         cat,
			OAuthCode:        cbResult.Error,
			OAuthDescription: cbResult.ErrorDescription,
			Message:          fmt.Sprintf("authorization server returned error %q", cbResult.Error),
		}
		return nil, oe.Sanitize(req.Redactor)
	}

	if strings.TrimSpace(cbResult.Code) == "" {
		return nil, &OAuthError{
			Category: OAuthErrCallbackFailed,
			Message:  "OAuth callback did not include an authorization code",
		}
	}
	safeRegisterSecret(req.Redactor, cbResult.Code)

	form := url.Values{}
	form.Set("grant_type", OAuthGrantAuthorizationCode)
	form.Set("code", cbResult.Code)
	form.Set("redirect_uri", redirectURI)
	form.Set("code_verifier", verifier)
	if cfg.Audience != "" {
		form.Set("audience", cfg.Audience)
	}
	if cfg.Resource != "" {
		form.Set("resource", cfg.Resource)
	}
	for k, v := range cfg.ExtraParams {
		if k != "" && form.Get(k) == "" {
			form.Set(k, v)
		}
	}

	rec, err := s.exchangeTokenForm(ctx, cfg, req, form, nonce)
	if err != nil {
		return nil, err
	}
	rec.GrantType = OAuthGrantAuthorizationCode
	if err := s.persistToken(ctx, req.CacheKey(&cfg), rec); err != nil {
		return nil, err
	}
	return rec, nil
}

func buildAuthorizationURL(cfg OAuthConfig, schemeType, redirectURI, state, challenge, nonce string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(cfg.AuthorizationURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  fmt.Sprintf("invalid authorizationUrl %q", cfg.AuthorizationURL),
			Cause:    err,
		}
	}
	q := u.Query()
	q.Set("response_type", "code")
	q.Set("client_id", cfg.ClientID)
	q.Set("redirect_uri", redirectURI)
	q.Set("state", state)
	q.Set("code_challenge", challenge)
	q.Set("code_challenge_method", "S256")
	if scopes := cfg.NormalizedScopes(schemeType); len(scopes) > 0 {
		q.Set("scope", strings.Join(scopes, " "))
	}
	if nonce != "" {
		q.Set("nonce", nonce)
	}
	if cfg.Audience != "" {
		q.Set("audience", cfg.Audience)
	}
	if cfg.Resource != "" {
		q.Set("resource", cfg.Resource)
	}
	for k, v := range cfg.ExtraParams {
		if k != "" && q.Get(k) == "" {
			q.Set(k, v)
		}
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// DeviceAuthorizationChallenge represents the user-facing verification challenge returned in RFC 8628 §3.2.
type DeviceAuthorizationChallenge struct {
	DeviceCode              string        `json:"device_code"`
	UserCode                string        `json:"user_code"`
	VerificationURI         string        `json:"verification_uri"`
	VerificationURLFallback string        `json:"verification_url,omitempty"`
	VerificationURIComplete string        `json:"verification_uri_complete,omitempty"`
	ExpiresIn               int           `json:"expires_in"`
	Interval                int           `json:"interval,omitempty"`
	ExpiresAt               time.Time     `json:"expiresAt"`
	PollInterval            time.Duration `json:"pollInterval"`
}

// DevicePollStatus reports progress during interactive Device Code polling.
type DevicePollStatus struct {
	Attempt      int
	NextInterval time.Duration
	SlowedDown   bool
	ExpiresAt    time.Time
}

// StartDeviceAuthorization initiates the RFC 8628 Device Authorization Request and returns the user_code challenge.
func (s *OAuthService) StartDeviceAuthorization(ctx context.Context, req OAuthFlowRequest) (*DeviceAuthorizationChallenge, error) {
	if !req.Interactive {
		return nil, &OAuthError{
			Category: OAuthErrInteractionRequired,
			Message:  "device_code authorization cannot be started in non-interactive mode",
		}
	}
	cfg, _, err := s.ResolveEndpoints(ctx, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.DeviceAuthorizationURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "deviceAuthorizationUrl (or OIDC discovery with device_authorization_endpoint) is required for device_code grant",
		}
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "tokenUrl is required for device_code grant",
		}
	}
	if strings.TrimSpace(cfg.ClientID) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "clientId is required for device_code grant",
		}
	}
	hasCreds := strings.TrimSpace(cfg.ClientSecret) != ""
	if err := evaluateOAuthEndpointPolicy(cfg.DeviceAuthorizationURL, http.MethodPost, hasCreds, req.fetchOptions(cfg.Issuer)); err != nil {
		return nil, err
	}

	form := url.Values{}
	form.Set("client_id", cfg.ClientID)
	if scopes := cfg.NormalizedScopes(req.SchemeType); len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}
	if cfg.Audience != "" {
		form.Set("audience", cfg.Audience)
	}
	if cfg.Resource != "" {
		form.Set("resource", cfg.Resource)
	}
	for k, v := range cfg.ExtraParams {
		if k != "" && form.Get(k) == "" {
			form.Set(k, v)
		}
	}

	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Accept":       "application/json",
	}
	applyClientAuth(headers, form, cfg)

	resp, err := executeOAuthHTTP(ctx, http.MethodPost, cfg.DeviceAuthorizationURL, headers, []byte(form.Encode()), MaxOAuthTokenResponseBytes, req.Network, s.httpDoer)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Endpoint: sanitizeURLForDisplay(cfg.DeviceAuthorizationURL, req.Redactor),
			Message:  "device authorization request failed",
			Cause:    err,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseOAuthHTTPError(resp.StatusCode, resp.Body, cfg.DeviceAuthorizationURL, OAuthErrTokenExchangeFailed, req.Redactor)
	}

	var ch DeviceAuthorizationChallenge
	if err := json.Unmarshal(resp.Body, &ch); err != nil {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Endpoint: sanitizeURLForDisplay(cfg.DeviceAuthorizationURL, req.Redactor),
			Message:  "invalid JSON in device authorization response",
			Cause:    err,
		}
	}
	if ch.VerificationURI == "" && ch.VerificationURLFallback != "" {
		ch.VerificationURI = ch.VerificationURLFallback
	}
	if strings.TrimSpace(ch.DeviceCode) == "" || strings.TrimSpace(ch.UserCode) == "" || strings.TrimSpace(ch.VerificationURI) == "" {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Endpoint: sanitizeURLForDisplay(cfg.DeviceAuthorizationURL, req.Redactor),
			Message:  "device authorization response is missing required device_code, user_code, or verification_uri",
		}
	}
	safeRegisterSecret(req.Redactor, ch.DeviceCode)

	now := s.nowFn()
	expiresIn := ch.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 600
	}
	ch.ExpiresAt = now.Add(time.Duration(expiresIn) * time.Second)
	if ch.Interval <= 0 {
		ch.PollInterval = DefaultDevicePollInterval
	} else {
		ch.PollInterval = time.Duration(ch.Interval) * time.Second
	}
	return &ch, nil
}

// PollDeviceToken polls the Token Endpoint per RFC 8628 §3.4–3.5 until the user completes authorization,
// denies access, the device_code expires, or ctx is canceled.
func (s *OAuthService) PollDeviceToken(ctx context.Context, req OAuthFlowRequest, challenge *DeviceAuthorizationChallenge, onStatus func(DevicePollStatus)) (*OAuthTokenRecord, error) {
	if !req.Interactive {
		return nil, &OAuthError{
			Category: OAuthErrInteractionRequired,
			Message:  "device_code polling cannot run in non-interactive mode",
		}
	}
	if challenge == nil || strings.TrimSpace(challenge.DeviceCode) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "device authorization challenge is nil or missing device_code",
		}
	}
	cfg, _, err := s.ResolveEndpoints(ctx, req)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "tokenUrl is required for device_code polling",
		}
	}
	if err := evaluateOAuthEndpointPolicy(cfg.TokenURL, http.MethodPost, true, req.fetchOptions(cfg.Issuer)); err != nil {
		return nil, err
	}

	interval := challenge.PollInterval
	if interval <= 0 {
		interval = DefaultDevicePollInterval
	}
	expiresAt := challenge.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = s.nowFn().Add(10 * time.Minute)
	}

	for attempt := 1; ; attempt++ {
		if !s.nowFn().Before(expiresAt) {
			return nil, &OAuthError{
				Category:  OAuthErrExpiredDeviceCode,
				OAuthCode: "expired_token",
				Endpoint:  sanitizeURLForDisplay(cfg.TokenURL, req.Redactor),
				Message:   "device verification code has expired before authorization completed",
			}
		}

		if err := s.sleepFn(ctx, interval); err != nil {
			return nil, err
		}

		if !s.nowFn().Before(expiresAt) {
			return nil, &OAuthError{
				Category:  OAuthErrExpiredDeviceCode,
				OAuthCode: "expired_token",
				Endpoint:  sanitizeURLForDisplay(cfg.TokenURL, req.Redactor),
				Message:   "device verification code has expired",
			}
		}

		form := url.Values{}
		form.Set("grant_type", "urn:ietf:params:oauth:grant-type:device_code")
		form.Set("device_code", challenge.DeviceCode)
		form.Set("client_id", cfg.ClientID)

		rec, err := s.exchangeTokenForm(ctx, cfg, req, form, "")
		if err == nil {
			rec.GrantType = OAuthGrantDeviceCode
			if persistErr := s.persistToken(ctx, req.CacheKey(&cfg), rec); persistErr != nil {
				return nil, persistErr
			}
			return rec, nil
		}

		var oe *OAuthError
		if !errors.As(err, &oe) {
			return nil, err
		}

		switch oe.OAuthCode {
		case "authorization_pending":
			if onStatus != nil {
				onStatus(DevicePollStatus{
					Attempt:      attempt,
					NextInterval: interval,
					SlowedDown:   false,
					ExpiresAt:    expiresAt,
				})
			}
			continue

		case "slow_down":
			// RFC 8628 §3.5: the interval MUST be increased by 5 seconds for this and all subsequent requests.
			interval += DeviceSlowDownIncrement
			if onStatus != nil {
				onStatus(DevicePollStatus{
					Attempt:      attempt,
					NextInterval: interval,
					SlowedDown:   true,
					ExpiresAt:    expiresAt,
				})
			}
			continue

		case "access_denied":
			oe.Category = OAuthErrAccessDenied
			return nil, oe.Sanitize(req.Redactor)

		case "expired_token":
			oe.Category = OAuthErrExpiredDeviceCode
			return nil, oe.Sanitize(req.Redactor)

		default:
			return nil, oe.Sanitize(req.Redactor)
		}
	}
}

// RefreshToken refreshes an existing token record using its refresh_token, coalescing concurrent refreshes
// for the same isolation key (singleflight) and atomically persisting rotated refresh tokens.
func (s *OAuthService) RefreshToken(ctx context.Context, req OAuthFlowRequest) (*OAuthTokenRecord, error) {
	cfg, _, err := s.ResolveEndpoints(ctx, req)
	if err != nil {
		return nil, err
	}
	cacheKey := req.CacheKey(&cfg)
	return s.doSingleflight(ctx, cacheKey.StorageKey(), func(sfCtx context.Context) (*OAuthTokenRecord, error) {
		existing, err := s.loadToken(sfCtx, cacheKey, req.Redactor)
		if err != nil {
			return nil, err
		}
		if existing == nil || strings.TrimSpace(existing.RefreshToken) == "" {
			return nil, &OAuthError{
				Category: OAuthErrReauthRequired,
				Message:  "no refresh_token is available in local credential store; please sign in again",
			}
		}
		return s.refreshWithRecord(sfCtx, cfg, req, cacheKey, existing)
	})
}

func (s *OAuthService) refreshWithRecord(ctx context.Context, cfg OAuthConfig, req OAuthFlowRequest, cacheKey OAuthCacheKey, existing *OAuthTokenRecord) (*OAuthTokenRecord, error) {
	if strings.TrimSpace(cfg.TokenURL) == "" {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "tokenUrl (or OIDC issuer/discoveryUrl) is required for token refresh",
		}
	}
	safeRegisterSecret(req.Redactor, existing.RefreshToken)

	form := url.Values{}
	form.Set("grant_type", "refresh_token")
	form.Set("refresh_token", existing.RefreshToken)
	if scopes := cfg.NormalizedScopes(req.SchemeType); len(scopes) > 0 {
		form.Set("scope", strings.Join(scopes, " "))
	}

	updated, err := s.exchangeTokenForm(ctx, cfg, req, form, "")
	if err != nil {
		var oe *OAuthError
		if errors.As(err, &oe) && (oe.OAuthCode == "invalid_grant" || oe.StatusCode == http.StatusBadRequest || oe.StatusCode == http.StatusUnauthorized) {
			_ = s.store.DeleteToken(ctx, cacheKey)
			oe.Category = OAuthErrReauthRequired
			oe.Message = fmt.Sprintf("refresh token was rejected (%s); re-authorization is required", oe.OAuthCode)
			return nil, oe.Sanitize(req.Redactor)
		}
		if errors.As(err, &oe) {
			oe.Category = OAuthErrRefreshFailed
			return nil, oe.Sanitize(req.Redactor)
		}
		return nil, &OAuthError{
			Category: OAuthErrRefreshFailed,
			Message:  "failed to refresh OAuth token",
			Cause:    err,
		}
	}

	if strings.TrimSpace(updated.RefreshToken) == "" {
		updated.RefreshToken = existing.RefreshToken
	}
	if strings.TrimSpace(updated.IDToken) == "" && cfg.NormalizedUseToken() == OAuthUseAccessToken {
		updated.IDToken = existing.IDToken
		updated.IDTokenInspection = existing.IDTokenInspection
	}
	if updated.IDTokenInspection != nil && existing.IDTokenInspection != nil {
		if existing.IDTokenInspection.Claims.Subject != "" &&
			updated.IDTokenInspection.Claims.Subject != existing.IDTokenInspection.Claims.Subject {
			return nil, &OAuthError{
				Category: OAuthErrIDTokenInvalidClaims,
				Message:  "refreshed ID Token 'sub' claim does not match original subject",
			}
		}
	}
	updated.GrantType = existing.GrantType
	if updated.GrantType == "" {
		updated.GrantType = cfg.NormalizedGrantType()
	}

	if err := s.persistToken(ctx, cacheKey, updated); err != nil {
		return nil, err
	}
	return updated, nil
}

// EnsureValidToken returns a valid token for the given scheme, transparently coalescing refreshes
// or executing non-interactive client_credentials when needed.
func (s *OAuthService) EnsureValidToken(ctx context.Context, req OAuthFlowRequest) (*OAuthTokenRecord, error) {
	cfg, err := req.ResolveConfig()
	if err != nil {
		return nil, err
	}
	cacheKey := req.CacheKey(&cfg)

	if !req.ForceRefresh {
		cached, err := s.loadToken(ctx, cacheKey, req.Redactor)
		if err != nil {
			return nil, err
		}
		if cached != nil && !cached.IsExpired(s.nowFn(), s.expirySkew) && hasSelectedToken(cached, cfg) {
			return cached, nil
		}
	}

	return s.doSingleflight(ctx, cacheKey.StorageKey(), func(sfCtx context.Context) (*OAuthTokenRecord, error) {
		cached, err := s.loadToken(sfCtx, cacheKey, req.Redactor)
		if err != nil {
			return nil, err
		}
		if !req.ForceRefresh && cached != nil && !cached.IsExpired(s.nowFn(), s.expirySkew) && hasSelectedToken(cached, cfg) {
			return cached, nil
		}

		resolvedCfg, _, err := s.ResolveEndpoints(sfCtx, req)
		if err != nil {
			return nil, err
		}

		if cached != nil && strings.TrimSpace(cached.RefreshToken) != "" {
			refreshed, refreshErr := s.refreshWithRecord(sfCtx, resolvedCfg, req, cacheKey, cached)
			if refreshErr == nil {
				return refreshed, nil
			}
			if resolvedCfg.NormalizedGrantType() != OAuthGrantClientCredentials {
				return nil, refreshErr
			}
		}

		if resolvedCfg.NormalizedGrantType() == OAuthGrantClientCredentials {
			return s.AcquireClientCredentials(sfCtx, req)
		}

		return nil, &OAuthError{
			Category: OAuthErrInteractionRequired,
			Message:  fmt.Sprintf("OAuth scheme %q (%s) has no valid cached token; explicit user sign-in is required", req.SchemeID, resolvedCfg.NormalizedGrantType()),
		}
	})
}

func hasSelectedToken(rec *OAuthTokenRecord, cfg OAuthConfig) bool {
	if rec == nil {
		return false
	}
	if cfg.NormalizedUseToken() == OAuthUseIDToken {
		return strings.TrimSpace(rec.IDToken) != ""
	}
	return strings.TrimSpace(rec.AccessToken) != ""
}

func (s *OAuthService) doSingleflight(ctx context.Context, key string, fn func(context.Context) (*OAuthTokenRecord, error)) (*OAuthTokenRecord, error) {
	s.singleflightMu.Lock()
	if call, ok := s.inflightRefresh[key]; ok {
		s.singleflightMu.Unlock()
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-call.done:
			if call.err != nil {
				return nil, call.err
			}
			return call.record.Clone(), nil
		}
	}
	call := &inflightTokenCall{done: make(chan struct{})}
	s.inflightRefresh[key] = call
	s.singleflightMu.Unlock()

	rec, err := fn(ctx)

	s.singleflightMu.Lock()
	call.record = rec.Clone()
	call.err = err
	delete(s.inflightRefresh, key)
	close(call.done)
	s.singleflightMu.Unlock()

	if err != nil {
		return nil, err
	}
	return rec.Clone(), nil
}

// RevokeResult distinguishes local cache removal from remote RFC 7009 revocation status.
type RevokeResult struct {
	LocalCleared    bool   `json:"localCleared"`
	RemoteAttempted bool   `json:"remoteAttempted"`
	RemoteSucceeded bool   `json:"remoteSucceeded"`
	RemoteMessage   string `json:"remoteMessage,omitempty"`
	RemoteError     error  `json:"-"`
}

// ClearLocalToken deletes the cached token record for req from the local isolated credential store.
func (s *OAuthService) ClearLocalToken(ctx context.Context, req OAuthFlowRequest) error {
	cfg, err := req.ResolveConfig()
	if err != nil {
		cfg = req.Config
	}
	key := req.CacheKey(&cfg)
	if err := s.store.DeleteToken(ctx, key); err != nil {
		return &OAuthError{
			Category: OAuthErrStoreUnavailable,
			Message:  "failed to clear local OAuth token cache",
			Cause:    err,
		}
	}
	return nil
}

// RevokeToken clears the local token record AND attempts RFC 7009 remote token revocation when a revocation endpoint is configured.
func (s *OAuthService) RevokeToken(ctx context.Context, req OAuthFlowRequest) (RevokeResult, error) {
	cfg, _, resolveErr := s.ResolveEndpoints(ctx, req)
	if resolveErr != nil {
		cfg, _ = req.ResolveConfig()
	}
	key := req.CacheKey(&cfg)
	existing, _ := s.loadToken(ctx, key, req.Redactor)

	if err := s.store.DeleteToken(ctx, key); err != nil {
		return RevokeResult{LocalCleared: false}, &OAuthError{
			Category: OAuthErrStoreUnavailable,
			Message:  "failed to clear local OAuth token before revocation",
			Cause:    err,
		}
	}
	res := RevokeResult{LocalCleared: true}

	revocationURL := strings.TrimSpace(cfg.RevocationURL)
	if revocationURL == "" {
		res.RemoteMessage = "Local token cleared (no revocationUrl configured on provider)."
		return res, nil
	}
	if existing == nil || (existing.RefreshToken == "" && existing.AccessToken == "") {
		res.RemoteMessage = "Local token cleared (no active token to revoke remotely)."
		return res, nil
	}

	res.RemoteAttempted = true
	if err := evaluateOAuthEndpointPolicy(revocationURL, http.MethodPost, true, req.fetchOptions(cfg.Issuer)); err != nil {
		res.RemoteError = err
		res.RemoteMessage = fmt.Sprintf("Local token cleared, but remote revocation blocked by target policy: %v", err)
		return res, nil
	}

	tokenToRevoke := existing.RefreshToken
	hint := "refresh_token"
	if tokenToRevoke == "" {
		tokenToRevoke = existing.AccessToken
		hint = "access_token"
	}

	form := url.Values{}
	form.Set("token", tokenToRevoke)
	form.Set("token_type_hint", hint)
	headers := map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"Accept":       "application/json",
	}
	applyClientAuth(headers, form, cfg)

	resp, err := executeOAuthHTTP(ctx, http.MethodPost, revocationURL, headers, []byte(form.Encode()), MaxOAuthTokenResponseBytes, req.Network, s.httpDoer)
	if err != nil {
		res.RemoteError = err
		res.RemoteMessage = fmt.Sprintf("Local token cleared, but remote revocation request failed: %v", err)
		return res, nil
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		oe := parseOAuthHTTPError(resp.StatusCode, resp.Body, revocationURL, OAuthErrTokenExchangeFailed, req.Redactor)
		res.RemoteError = oe
		res.RemoteMessage = fmt.Sprintf("Local token cleared, but remote server returned HTTP %d during revocation.", resp.StatusCode)
		return res, nil
	}

	res.RemoteSucceeded = true
	res.RemoteMessage = "Local token cleared and remotely revoked at authorization server."
	return res, nil
}

// GetCachedToken returns the currently cached token record (if any) without triggering network requests.
func (s *OAuthService) GetCachedToken(ctx context.Context, req OAuthFlowRequest) (*OAuthTokenRecord, error) {
	cfg, err := req.ResolveConfig()
	if err != nil {
		cfg = req.Config
	}
	return s.loadToken(ctx, req.CacheKey(&cfg), req.Redactor)
}

// ApplyOAuthToPlan resolves or refreshes the OAuth/OIDC token for scheme and injects it into plan.Headers
// after verifying the business request URL against MergedTargetPolicy.
func (s *OAuthService) ApplyOAuthToPlan(ctx context.Context, plan *Plan, req OAuthFlowRequest) (*OAuthTokenRecord, error) {
	if plan == nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "target execution plan is nil",
		}
	}
	if err := evaluateOAuthEndpointPolicy(plan.URL, plan.Method, true, req.fetchOptions("")); err != nil {
		return nil, err
	}

	rec, err := s.EnsureValidToken(ctx, req)
	if err != nil {
		return nil, err
	}
	cfg, err := req.ResolveConfig()
	if err != nil {
		return nil, err
	}

	var tokenValue string
	if cfg.NormalizedUseToken() == OAuthUseIDToken {
		tokenValue = strings.TrimSpace(rec.IDToken)
		if tokenValue == "" {
			return nil, &OAuthError{
				Category: OAuthErrIDTokenMalformed,
				Message:  fmt.Sprintf("scheme %q is configured with useToken=id_token, but token response did not include an id_token", req.SchemeID),
			}
		}
	} else {
		tokenValue = strings.TrimSpace(rec.AccessToken)
		if tokenValue == "" {
			return nil, &OAuthError{
				Category: OAuthErrTokenExchangeFailed,
				Message:  fmt.Sprintf("scheme %q token record does not contain an access_token", req.SchemeID),
			}
		}
	}

	if strings.ContainsAny(tokenValue, "\r\n") {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Message:  "OAuth token contains newline characters; refusing header injection",
		}
	}

	RegisterOAuthSecrets(req.Redactor, rec, cfg.ClientSecret)
	if !hasHeaderKey(plan.Headers, "Authorization") {
		plan.Headers = append(plan.Headers, [2]string{"Authorization", cfg.EffectiveHeaderPrefix() + " " + tokenValue})
	}
	return rec, nil
}

type rawTokenEndpointResponse struct {
	AccessToken  string `json:"access_token"`
	TokenType    string `json:"token_type"`
	ExpiresIn    int64  `json:"expires_in"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	Scope        string `json:"scope"`
}

func (s *OAuthService) exchangeTokenForm(ctx context.Context, cfg OAuthConfig, req OAuthFlowRequest, form url.Values, expectedNonce string) (*OAuthTokenRecord, error) {
	if err := evaluateOAuthEndpointPolicy(cfg.TokenURL, http.MethodPost, true, req.fetchOptions(cfg.Issuer)); err != nil {
		return nil, err
	}
	headers := map[string]string{
		"Content-Type":  "application/x-www-form-urlencoded",
		"Accept":        "application/json",
		"Cache-Control": "no-store",
	}
	applyClientAuth(headers, form, cfg)

	resp, err := executeOAuthHTTP(ctx, http.MethodPost, cfg.TokenURL, headers, []byte(form.Encode()), MaxOAuthTokenResponseBytes, req.Network, s.httpDoer)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Endpoint: sanitizeURLForDisplay(cfg.TokenURL, req.Redactor),
			Message:  "token endpoint request failed",
			Cause:    err,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseOAuthHTTPError(resp.StatusCode, resp.Body, cfg.TokenURL, OAuthErrTokenExchangeFailed, req.Redactor)
	}

	var parsed rawTokenEndpointResponse
	if err := json.Unmarshal(resp.Body, &parsed); err != nil {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Endpoint: sanitizeURLForDisplay(cfg.TokenURL, req.Redactor),
			Message:  "invalid JSON in token endpoint response",
			Cause:    err,
		}
	}

	now := s.nowFn()
	rec := &OAuthTokenRecord{
		AccessToken:  strings.TrimSpace(parsed.AccessToken),
		TokenType:    strings.TrimSpace(parsed.TokenType),
		RefreshToken: strings.TrimSpace(parsed.RefreshToken),
		IDToken:      strings.TrimSpace(parsed.IDToken),
		Scope:        strings.TrimSpace(parsed.Scope),
		ObtainedAt:   now.UTC(),
		Issuer:       cfg.Issuer,
		ClientID:     cfg.ClientID,
	}
	if rec.TokenType == "" {
		rec.TokenType = "Bearer"
	}
	if parsed.ExpiresIn > 0 {
		rec.ExpiresAt = now.Add(time.Duration(parsed.ExpiresIn) * time.Second).UTC()
	}

	RegisterOAuthSecrets(req.Redactor, rec, cfg.ClientSecret)

	if rec.AccessToken == "" && rec.IDToken == "" {
		return nil, &OAuthError{
			Category: OAuthErrTokenExchangeFailed,
			Endpoint: sanitizeURLForDisplay(cfg.TokenURL, req.Redactor),
			Message:  "token endpoint response did not include an access_token or id_token",
		}
	}

	wantsOIDC := cfg.WantsOIDC(req.SchemeType)
	if wantsOIDC && rec.IDToken == "" && form.Get("grant_type") != "refresh_token" && form.Get("grant_type") != OAuthGrantClientCredentials {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Endpoint: sanitizeURLForDisplay(cfg.TokenURL, req.Redactor),
			Message:  "OIDC flow requires an id_token, but the token endpoint did not return one",
		}
	}

	if rec.IDToken != "" {
		shouldVerify := wantsOIDC || cfg.JWKSURI != "" || cfg.Issuer != "" || req.StaticJWKS != nil
		if shouldVerify {
			fopts := req.fetchOptions(cfg.Issuer)
			fopts.HTTPDoer = s.httpDoer
			insp, verifyErr := VerifyIDToken(ctx, rec.IDToken, IDTokenVerifyParams{
				ExpectedIssuer: cfg.Issuer,
				ClientID:       cfg.ClientID,
				ExpectedNonce:  expectedNonce,
				AccessToken:    rec.AccessToken,
				JWKSURI:        cfg.JWKSURI,
				JWKSManager:    s.jwks,
				StaticKeySet:   req.StaticJWKS,
				Now:            s.nowFn,
				FetchOptions:   fopts,
			})
			if verifyErr != nil {
				return nil, verifyErr
			}
			rec.IDTokenInspection = insp
			rec.Subject = insp.Claims.Subject
			if rec.ExpiresAt.IsZero() && insp.Claims.ExpiresAt > 0 {
				rec.ExpiresAt = time.Unix(insp.Claims.ExpiresAt, 0).UTC()
			}
		} else {
			if insp, inspectErr := InspectIDTokenUnverified(rec.IDToken); inspectErr == nil {
				rec.IDTokenInspection = insp
				rec.Subject = insp.Claims.Subject
			}
		}
	}

	return rec, nil
}

func applyClientAuth(headers map[string]string, form url.Values, cfg OAuthConfig) {
	method := cfg.NormalizedClientAuthMethod()
	switch method {
	case OAuthClientAuthBasic:
		encodedID := url.QueryEscape(cfg.ClientID)
		encodedSecret := url.QueryEscape(cfg.ClientSecret)
		cred := base64.StdEncoding.EncodeToString([]byte(encodedID + ":" + encodedSecret))
		headers["Authorization"] = "Basic " + cred
	case OAuthClientAuthPost:
		if cfg.ClientID != "" {
			form.Set("client_id", cfg.ClientID)
		}
		if cfg.ClientSecret != "" {
			form.Set("client_secret", cfg.ClientSecret)
		}
	case OAuthClientAuthNone:
		if cfg.ClientID != "" {
			form.Set("client_id", cfg.ClientID)
		}
	}
}

type oauthWireError struct {
	Error            string `json:"error"`
	ErrorDescription string `json:"error_description"`
	ErrorURI         string `json:"error_uri"`
}

func parseOAuthHTTPError(statusCode int, body []byte, endpointURL string, defaultCat OAuthErrorCategory, redactor *Redactor) *OAuthError {
	var wire oauthWireError
	_ = json.Unmarshal(body, &wire)
	oe := &OAuthError{
		Category:         defaultCat,
		OAuthCode:        strings.TrimSpace(wire.Error),
		OAuthDescription: strings.TrimSpace(wire.ErrorDescription),
		StatusCode:       statusCode,
		Endpoint:         sanitizeURLForDisplay(endpointURL, redactor),
		Message:          fmt.Sprintf("OAuth endpoint returned HTTP %d", statusCode),
	}
	if oe.OAuthCode != "" {
		oe.Message = fmt.Sprintf("OAuth error %q (HTTP %d)", oe.OAuthCode, statusCode)
	}
	return oe.Sanitize(redactor)
}

func (s *OAuthService) loadToken(ctx context.Context, key OAuthCacheKey, redactor *Redactor) (*OAuthTokenRecord, error) {
	if s.store == nil {
		return nil, nil
	}
	rec, err := s.store.GetToken(ctx, key)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrStoreUnavailable,
			Message:  fmt.Sprintf("failed to read OAuth token from credential store: %v", err),
			Cause:    err,
		}
	}
	if rec != nil {
		RegisterOAuthSecrets(redactor, rec)
	}
	return rec, nil
}

func (s *OAuthService) persistToken(ctx context.Context, key OAuthCacheKey, rec *OAuthTokenRecord) error {
	if s.store == nil {
		return nil
	}
	if err := s.store.PutToken(ctx, key, rec); err != nil {
		return &OAuthError{
			Category: OAuthErrStoreUnavailable,
			Message:  fmt.Sprintf("failed to store OAuth token in credential store: %v", err),
			Cause:    err,
		}
	}
	return nil
}
