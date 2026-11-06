package core

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"
)

// OAuth 2.0 / OIDC Grant Types.
const (
	OAuthGrantAuthorizationCode = "authorization_code"
	OAuthGrantClientCredentials = "client_credentials"
	OAuthGrantDeviceCode        = "device_code"
)

// OAuth 2.0 Client Authentication Methods at Token/Revocation Endpoints.
const (
	OAuthClientAuthBasic = "client_secret_basic"
	OAuthClientAuthPost  = "client_secret_post"
	OAuthClientAuthNone  = "none"
)

// OAuth Token Selection modes for business API injection.
const (
	OAuthUseAccessToken = "access_token"
	OAuthUseIDToken     = "id_token"
)

const (
	// DefaultTokenExpirySkew is the pre-expiry buffer before a cached token is proactively refreshed.
	DefaultTokenExpirySkew = 30 * time.Second
	// MaxOAuthTokenResponseBytes caps Token/Device/Revocation HTTP response bodies at 512 KiB.
	MaxOAuthTokenResponseBytes = 512 << 10
	// DefaultDevicePollInterval is the RFC 8628 §3.2 default minimum polling interval (5 seconds).
	DefaultDevicePollInterval = 5 * time.Second
	// DeviceSlowDownIncrement is the RFC 8628 §3.5 mandatory interval increase on "slow_down" (+5 seconds).
	DeviceSlowDownIncrement = 5 * time.Second
	// DefaultLoopbackCallbackPath is the default local callback path for RFC 8252 loopback redirects.
	DefaultLoopbackCallbackPath = "/oauth/callback"
)

// OAuthErrorCategory classifies OAuth 2.0 and OIDC failures for structured UI and Runner diagnostics.
type OAuthErrorCategory string

const (
	OAuthErrInvalidConfig           OAuthErrorCategory = "oauth_invalid_config"
	OAuthErrDiscoveryFailed         OAuthErrorCategory = "oidc_discovery_failed"
	OAuthErrDiscoveryMismatch       OAuthErrorCategory = "oidc_discovery_mismatch"
	OAuthErrJWKSFetchFailed         OAuthErrorCategory = "oidc_jwks_fetch_failed"
	OAuthErrJWKSKeyNotFound         OAuthErrorCategory = "oidc_jwks_key_not_found"
	OAuthErrIDTokenMalformed        OAuthErrorCategory = "oidc_id_token_malformed"
	OAuthErrIDTokenInvalidSignature OAuthErrorCategory = "oidc_id_token_invalid_signature"
	OAuthErrIDTokenInvalidClaims    OAuthErrorCategory = "oidc_id_token_invalid_claims"
	OAuthErrIDTokenExpired          OAuthErrorCategory = "oidc_id_token_expired"
	OAuthErrStateMismatch           OAuthErrorCategory = "oauth_state_mismatch"
	OAuthErrNonceMismatch           OAuthErrorCategory = "oidc_nonce_mismatch"
	OAuthErrCallbackFailed          OAuthErrorCategory = "oauth_callback_failed"
	OAuthErrBrowserOpenFailed       OAuthErrorCategory = "oauth_browser_open_failed"
	OAuthErrTokenExchangeFailed     OAuthErrorCategory = "oauth_token_exchange_failed"
	OAuthErrRefreshFailed           OAuthErrorCategory = "oauth_refresh_failed"
	OAuthErrReauthRequired          OAuthErrorCategory = "oauth_reauth_required"
	OAuthErrInteractionRequired     OAuthErrorCategory = "oauth_interaction_required"
	OAuthErrAccessDenied            OAuthErrorCategory = "oauth_access_denied"
	OAuthErrExpiredDeviceCode       OAuthErrorCategory = "oauth_expired_device_code"
	OAuthErrTargetPolicyDenied      OAuthErrorCategory = "oauth_target_policy_denied"
	OAuthErrStoreUnavailable        OAuthErrorCategory = "oauth_store_unavailable"
)

// OAuthError is a structured, redacted error returned by OAuth 2.0 and OIDC operations.
type OAuthError struct {
	Category         OAuthErrorCategory `json:"category"`
	OAuthCode        string             `json:"oauthCode,omitempty"`
	OAuthDescription string             `json:"oauthDescription,omitempty"`
	StatusCode       int                `json:"statusCode,omitempty"`
	Endpoint         string             `json:"endpoint,omitempty"`
	Message          string             `json:"message"`
	Cause            error              `json:"-"`
}

func (e *OAuthError) Error() string {
	if e == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(string(e.Category))
	if e.OAuthCode != "" {
		b.WriteString(" [")
		b.WriteString(e.OAuthCode)
		b.WriteString("]")
	}
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.OAuthDescription != "" && !strings.Contains(e.Message, e.OAuthDescription) {
		b.WriteString(" (")
		b.WriteString(e.OAuthDescription)
		b.WriteString(")")
	}
	if e.Cause != nil && !strings.Contains(e.Message, e.Cause.Error()) {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	return b.String()
}

func (e *OAuthError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Cause
}

var oauthRedactMu sync.Mutex

func safeRegisterSecret(redactor *Redactor, secret string) {
	if redactor == nil || secret == "" {
		return
	}
	oauthRedactMu.Lock()
	redactor.RegisterSecret(secret)
	oauthRedactMu.Unlock()
}

func safeRedactString(redactor *Redactor, s string) string {
	if redactor == nil || s == "" {
		return s
	}
	oauthRedactMu.Lock()
	defer oauthRedactMu.Unlock()
	return redactor.RedactString(s)
}

func safeRedactError(redactor *Redactor, err error) error {
	if redactor == nil || err == nil {
		return err
	}
	oauthRedactMu.Lock()
	defer oauthRedactMu.Unlock()
	return redactor.RedactError(err)
}

// Sanitize scrubs all registered secrets from the error fields using redactor.
func (e *OAuthError) Sanitize(redactor *Redactor) *OAuthError {
	if e == nil || redactor == nil {
		return e
	}
	cp := *e
	cp.OAuthCode = safeRedactString(redactor, cp.OAuthCode)
	cp.OAuthDescription = safeRedactString(redactor, cp.OAuthDescription)
	cp.Endpoint = safeRedactString(redactor, cp.Endpoint)
	cp.Message = safeRedactString(redactor, cp.Message)
	if cp.Cause != nil {
		cp.Cause = safeRedactError(redactor, cp.Cause)
	}
	return &cp
}

// OAuthConfig defines the shareable, secret-free configuration for an oauth2 or oidc AuthScheme.
// Runtime tokens (access_token, refresh_token, id_token) are NEVER stored in OAuthConfig.
type OAuthConfig struct {
	GrantType              string            `yaml:"grantType,omitempty" json:"grantType,omitempty"`
	Issuer                 string            `yaml:"issuer,omitempty" json:"issuer,omitempty"`
	DiscoveryURL           string            `yaml:"discoveryUrl,omitempty" json:"discoveryUrl,omitempty"`
	AuthorizationURL       string            `yaml:"authorizationUrl,omitempty" json:"authorizationUrl,omitempty"`
	TokenURL               string            `yaml:"tokenUrl,omitempty" json:"tokenUrl,omitempty"`
	DeviceAuthorizationURL string            `yaml:"deviceAuthorizationUrl,omitempty" json:"deviceAuthorizationUrl,omitempty"`
	RevocationURL          string            `yaml:"revocationUrl,omitempty" json:"revocationUrl,omitempty"`
	JWKSURI                string            `yaml:"jwksUri,omitempty" json:"jwksUri,omitempty"`
	ClientID               string            `yaml:"clientId,omitempty" json:"clientId,omitempty"`
	ClientSecret           string            `yaml:"clientSecret,omitempty" json:"clientSecret,omitempty"`
	ClientAuthMethod       string            `yaml:"clientAuthMethod,omitempty" json:"clientAuthMethod,omitempty"`
	Scopes                 []string          `yaml:"scopes,omitempty" json:"scopes,omitempty"`
	Audience               string            `yaml:"audience,omitempty" json:"audience,omitempty"`
	Resource               string            `yaml:"resource,omitempty" json:"resource,omitempty"`
	RedirectURI            string            `yaml:"redirectUri,omitempty" json:"redirectUri,omitempty"`
	UseToken               string            `yaml:"useToken,omitempty" json:"useToken,omitempty"`
	HeaderPrefix           string            `yaml:"headerPrefix,omitempty" json:"headerPrefix,omitempty"`
	ExtraParams            map[string]string `yaml:"extraParams,omitempty" json:"extraParams,omitempty"`
}

// Clone returns a deep copy of OAuthConfig.
func (c *OAuthConfig) Clone() *OAuthConfig {
	if c == nil {
		return nil
	}
	cp := *c
	if len(c.Scopes) > 0 {
		cp.Scopes = slices.Clone(c.Scopes)
	}
	if len(c.ExtraParams) > 0 {
		cp.ExtraParams = make(map[string]string, len(c.ExtraParams))
		for k, v := range c.ExtraParams {
			cp.ExtraParams[k] = v
		}
	}
	return &cp
}

// NormalizedGrantType returns the normalized grant type, defaulting to authorization_code.
func (c *OAuthConfig) NormalizedGrantType() string {
	if c == nil {
		return OAuthGrantAuthorizationCode
	}
	switch strings.ToLower(strings.TrimSpace(c.GrantType)) {
	case OAuthGrantClientCredentials, "clientcredentials":
		return OAuthGrantClientCredentials
	case OAuthGrantDeviceCode, "device", "urn:ietf:params:oauth:grant-type:device_code":
		return OAuthGrantDeviceCode
	case OAuthGrantAuthorizationCode, "auth_code", "pkce", "":
		return OAuthGrantAuthorizationCode
	default:
		return strings.ToLower(strings.TrimSpace(c.GrantType))
	}
}

// NormalizedClientAuthMethod returns the normalized token endpoint authentication method.
func (c *OAuthConfig) NormalizedClientAuthMethod() string {
	if c == nil {
		return OAuthClientAuthBasic
	}
	switch strings.ToLower(strings.TrimSpace(c.ClientAuthMethod)) {
	case "none", "public":
		return OAuthClientAuthNone
	case "post", OAuthClientAuthPost:
		return OAuthClientAuthPost
	case "basic", OAuthClientAuthBasic, "":
		if strings.TrimSpace(c.ClientSecret) == "" && c.NormalizedGrantType() != OAuthGrantClientCredentials {
			return OAuthClientAuthNone
		}
		return OAuthClientAuthBasic
	default:
		return strings.ToLower(strings.TrimSpace(c.ClientAuthMethod))
	}
}

// NormalizedUseToken returns whether business API requests should inject access_token (default) or id_token.
func (c *OAuthConfig) NormalizedUseToken() string {
	if c != nil && strings.EqualFold(strings.TrimSpace(c.UseToken), OAuthUseIDToken) {
		return OAuthUseIDToken
	}
	return OAuthUseAccessToken
}

// EffectiveHeaderPrefix returns the Authorization header scheme prefix (default "Bearer").
func (c *OAuthConfig) EffectiveHeaderPrefix() string {
	if c != nil && strings.TrimSpace(c.HeaderPrefix) != "" {
		return strings.TrimSpace(c.HeaderPrefix)
	}
	return "Bearer"
}

// WantsOIDC reports whether the scheme or config requires OpenID Connect verification.
func (c *OAuthConfig) WantsOIDC(schemeType string) bool {
	if strings.EqualFold(strings.TrimSpace(schemeType), "oidc") {
		return true
	}
	if c == nil {
		return false
	}
	if c.NormalizedUseToken() == OAuthUseIDToken {
		return true
	}
	for _, s := range c.Scopes {
		for _, tok := range strings.Fields(s) {
			if strings.EqualFold(tok, "openid") {
				return true
			}
		}
	}
	return false
}

// NormalizedScopes returns a deduplicated, whitespace-trimmed list of scopes.
func (c *OAuthConfig) NormalizedScopes(schemeType string) []string {
	var out []string
	seen := make(map[string]struct{})
	add := func(s string) {
		for _, part := range strings.Fields(strings.ReplaceAll(s, ",", " ")) {
			if part == "" {
				continue
			}
			if _, exists := seen[part]; !exists {
				seen[part] = struct{}{}
				out = append(out, part)
			}
		}
	}
	if strings.EqualFold(strings.TrimSpace(schemeType), "oidc") {
		add("openid")
	}
	if c != nil {
		for _, s := range c.Scopes {
			add(s)
		}
	}
	return out
}

// Optional hooks populated when AuthScheme gains a top-level OAuth *OAuthConfig field via integration patch.
var (
	authSchemeGetOAuthHook func(s *AuthScheme) *OAuthConfig
	authSchemeSetOAuthHook func(s *AuthScheme, cfg *OAuthConfig)
)

// ExtractOAuthConfig extracts OAuthConfig from an AuthScheme (supporting both top-level OAuth hook and Extensions["oauth"]).
func ExtractOAuthConfig(s *AuthScheme) (*OAuthConfig, error) {
	if s == nil {
		return nil, nil
	}
	if authSchemeGetOAuthHook != nil {
		if cfg := authSchemeGetOAuthHook(s); cfg != nil {
			return cfg.Clone(), nil
		}
	}
	if len(s.Extensions) == 0 {
		return &OAuthConfig{}, nil
	}
	raw, exists := s.Extensions["oauth"]
	if !exists || raw == nil {
		return &OAuthConfig{}, nil
	}
	if typed, ok := raw.(*OAuthConfig); ok {
		return typed.Clone(), nil
	}
	if typedVal, ok := raw.(OAuthConfig); ok {
		return typedVal.Clone(), nil
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  fmt.Sprintf("failed to encode auth scheme %q oauth extension", s.ID),
			Cause:    err,
		}
	}
	// Reject any raw runtime tokens accidentally placed in extensions.oauth.
	var rawMap map[string]any
	if err := json.Unmarshal(encoded, &rawMap); err == nil {
		for _, forbidden := range []string{"accessToken", "access_token", "refreshToken", "refresh_token", "idToken", "id_token"} {
			if val, ok := rawMap[forbidden]; ok && strings.TrimSpace(fmt.Sprint(val)) != "" {
				return nil, &OAuthError{
					Category: OAuthErrInvalidConfig,
					Message:  fmt.Sprintf("auth scheme %q contains forbidden runtime token field %q in shared config", s.ID, forbidden),
				}
			}
		}
	}
	var cfg OAuthConfig
	if err := json.Unmarshal(encoded, &cfg); err != nil {
		return nil, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  fmt.Sprintf("invalid oauth configuration in auth scheme %q", s.ID),
			Cause:    err,
		}
	}
	return &cfg, nil
}

// SetOAuthConfig writes an OAuthConfig onto an AuthScheme so it round-trips through YAML preservation cleanly.
func SetOAuthConfig(s *AuthScheme, cfg *OAuthConfig) {
	if s == nil {
		return
	}
	if authSchemeSetOAuthHook != nil {
		authSchemeSetOAuthHook(s, cfg.Clone())
	}
	if cfg == nil {
		if s.Extensions != nil {
			delete(s.Extensions, "oauth")
		}
		return
	}
	if s.Extensions == nil {
		s.Extensions = make(map[string]any)
	}
	data, err := json.Marshal(cfg)
	if err != nil {
		return
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err == nil {
		s.Extensions["oauth"] = m
	}
}

func isLiteralSecretCandidate(raw string) bool {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return false
	}
	return !strings.Contains(trimmed, "{{")
}

// ValidateOAuthScheme validates an oauth2 or oidc AuthScheme for static schema and security compliance.
func ValidateOAuthScheme(loc string, s *AuthScheme) []Issue {
	var issues []Issue
	if s == nil {
		return issues
	}
	t := strings.ToLower(strings.TrimSpace(s.Type))
	if t != "oauth2" && t != "oidc" {
		return issues
	}
	if strings.TrimSpace(s.Token) != "" && isLiteralSecretCandidate(s.Token) {
		issues = append(issues, Issue{
			Level:   "error",
			Code:    "oauth-literal-token",
			Path:    loc,
			Message: "oauth2/oidc scheme must not store literal runtime tokens in shared project files",
		})
	}
	cfg, err := ExtractOAuthConfig(s)
	if err != nil {
		issues = append(issues, Issue{
			Level:   "error",
			Code:    "oauth-config",
			Path:    loc,
			Message: err.Error(),
		})
		return issues
	}
	if cfg == nil {
		return issues
	}
	gt := cfg.NormalizedGrantType()
	switch gt {
	case OAuthGrantAuthorizationCode, OAuthGrantClientCredentials, OAuthGrantDeviceCode:
	default:
		issues = append(issues, Issue{
			Level:   "error",
			Code:    "oauth-grant-type",
			Path:    loc,
			Message: fmt.Sprintf("unsupported OAuth grantType %q (expected authorization_code, client_credentials, or device_code)", cfg.GrantType),
		})
	}
	switch cfg.NormalizedClientAuthMethod() {
	case OAuthClientAuthBasic, OAuthClientAuthPost, OAuthClientAuthNone:
	default:
		issues = append(issues, Issue{
			Level:   "error",
			Code:    "oauth-client-auth",
			Path:    loc,
			Message: fmt.Sprintf("unsupported OAuth clientAuthMethod %q", cfg.ClientAuthMethod),
		})
	}
	switch cfg.NormalizedUseToken() {
	case OAuthUseAccessToken, OAuthUseIDToken:
	default:
		issues = append(issues, Issue{
			Level:   "error",
			Code:    "oauth-use-token",
			Path:    loc,
			Message: fmt.Sprintf("unsupported OAuth useToken %q", cfg.UseToken),
		})
	}
	if strings.TrimSpace(cfg.ClientSecret) != "" && isLiteralSecretCandidate(cfg.ClientSecret) {
		issues = append(issues, Issue{
			Level:   "warning",
			Code:    "oauth-literal-secret",
			Path:    loc,
			Message: "oauth.clientSecret appears to contain a literal secret; reference {{secret.name}} or {{env.NAME}} instead",
		})
	}
	return issues
}

// ComputeProjectCloneScope returns the canonical "<slug>:<sha256(canonicalRoot)[:16]>" credential isolation scope.
func ComputeProjectCloneScope(projectRoot, projectSlug string) string {
	slug := sanitizeKeySegment(strings.ToLower(strings.TrimSpace(projectSlug)))
	if slug == "" {
		slug = "project"
	}
	root := strings.TrimSpace(projectRoot)
	if root == "" {
		return slug + ":ephemeral"
	}
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}
	if eval, err := filepath.EvalSymlinks(root); err == nil {
		root = eval
	}
	root = filepath.Clean(root)
	sum := sha256.Sum256([]byte(root))
	return fmt.Sprintf("%s:%s", slug, hex.EncodeToString(sum[:8]))
}

// OAuthCacheKey uniquely isolates an OAuth token record across 5 dimensions:
// ProjectCloneID + SchemeID + Issuer/TokenURL + ClientID + Scopes/Audience/Subject.
type OAuthCacheKey struct {
	ProjectCloneID string `json:"projectCloneId"`
	SchemeID       string `json:"schemeId"`
	IssuerOrURL    string `json:"issuerOrUrl"`
	ClientID       string `json:"clientId"`
	ScopeHash      string `json:"scopeHash"`
	Subject        string `json:"subject,omitempty"`
}

// BuildOAuthCacheKey constructs a deterministic, collision-resistant isolation key for token storage.
func BuildOAuthCacheKey(projectRoot, projectSlug, schemeID string, cfg *OAuthConfig, schemeType string) OAuthCacheKey {
	cloneScope := ComputeProjectCloneScope(projectRoot, projectSlug)
	sid := strings.TrimSpace(schemeID)
	if sid == "" {
		sid = "default"
	}
	var issuerOrURL, clientID, audience, resource, useToken, grantType string
	var scopes []string
	if cfg != nil {
		issuerOrURL = strings.TrimSpace(cfg.Issuer)
		if issuerOrURL == "" {
			issuerOrURL = strings.TrimSpace(cfg.DiscoveryURL)
		}
		if issuerOrURL == "" {
			issuerOrURL = strings.TrimSpace(cfg.TokenURL)
		}
		clientID = strings.TrimSpace(cfg.ClientID)
		audience = strings.TrimSpace(cfg.Audience)
		resource = strings.TrimSpace(cfg.Resource)
		useToken = cfg.NormalizedUseToken()
		grantType = cfg.NormalizedGrantType()
		scopes = cfg.NormalizedScopes(schemeType)
	}
	sortedScopes := slices.Clone(scopes)
	sort.Strings(sortedScopes)
	scopeDescriptor := fmt.Sprintf("gt=%s|scopes=%s|aud=%s|res=%s|use=%s",
		grantType, strings.Join(sortedScopes, " "), audience, resource, useToken)
	sum := sha256.Sum256([]byte(scopeDescriptor))

	return OAuthCacheKey{
		ProjectCloneID: cloneScope,
		SchemeID:       sid,
		IssuerOrURL:    strings.ToLower(strings.TrimRight(issuerOrURL, "/")),
		ClientID:       clientID,
		ScopeHash:      hex.EncodeToString(sum[:8]),
	}
}

// StorageKey returns the canonical string key used for Keychain / Memory token storage and singleflight locking.
func (k OAuthCacheKey) StorageKey() string {
	raw := fmt.Sprintf("%s|%s|%s|%s|%s|%s",
		k.ProjectCloneID, k.SchemeID, k.IssuerOrURL, k.ClientID, k.ScopeHash, k.Subject)
	sum := sha256.Sum256([]byte(raw))
	safeScheme := sanitizeKeySegment(k.SchemeID)
	return fmt.Sprintf("%s/oauth/%s/%s", k.ProjectCloneID, safeScheme, hex.EncodeToString(sum[:12]))
}

func sanitizeKeySegment(s string) string {
	var b strings.Builder
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "default"
	}
	if len(out) > 32 {
		return out[:32]
	}
	return out
}

// OAuthTokenRecord represents an acquired OAuth 2.0 / OIDC token bundle held in isolated storage.
type OAuthTokenRecord struct {
	AccessToken       string             `json:"accessToken,omitempty"`
	TokenType         string             `json:"tokenType,omitempty"`
	RefreshToken      string             `json:"refreshToken,omitempty"`
	IDToken           string             `json:"idToken,omitempty"`
	Scope             string             `json:"scope,omitempty"`
	ExpiresAt         time.Time          `json:"expiresAt,omitempty"`
	ObtainedAt        time.Time          `json:"obtainedAt"`
	Issuer            string             `json:"issuer,omitempty"`
	ClientID          string             `json:"clientId,omitempty"`
	Subject           string             `json:"subject,omitempty"`
	GrantType         string             `json:"grantType,omitempty"`
	IDTokenInspection *IDTokenInspection `json:"idTokenInspection,omitempty"`
}

// Clone returns a deep copy of OAuthTokenRecord.
func (r *OAuthTokenRecord) Clone() *OAuthTokenRecord {
	if r == nil {
		return nil
	}
	cp := *r
	if r.IDTokenInspection != nil {
		insp := *r.IDTokenInspection
		insp.Claims.Audience = slices.Clone(r.IDTokenInspection.Claims.Audience)
		cp.IDTokenInspection = &insp
	}
	return &cp
}

// IsExpired reports whether the token record has expired (or will expire within skew).
func (r *OAuthTokenRecord) IsExpired(now time.Time, skew time.Duration) bool {
	if r == nil {
		return true
	}
	if r.ExpiresAt.IsZero() {
		return false
	}
	if skew < 0 {
		skew = 0
	}
	return !now.Add(skew).Before(r.ExpiresAt)
}

// MaskedPreview returns a UI-safe masked preview of a token (e.g., "eyJhbG...9x2Q") without exposing its secret value.
func MaskedPreview(token string) string {
	t := strings.TrimSpace(token)
	if t == "" {
		return ""
	}
	if len(t) <= 12 {
		return "••••••••"
	}
	return t[:4] + "••••" + t[len(t)-4:]
}

// RegisterOAuthSecrets registers all sensitive values from a token record and config into Redactor.
func RegisterOAuthSecrets(redactor *Redactor, rec *OAuthTokenRecord, extraSecrets ...string) {
	if redactor == nil {
		return
	}
	oauthRedactMu.Lock()
	defer oauthRedactMu.Unlock()
	if rec != nil {
		redactor.RegisterSecret(rec.AccessToken)
		redactor.RegisterSecret(rec.RefreshToken)
		redactor.RegisterSecret(rec.IDToken)
	}
	for _, s := range extraSecrets {
		redactor.RegisterSecret(s)
	}
}

// OAuthTokenStore is the injectable port for isolated OAuth token persistence.
type OAuthTokenStore interface {
	GetToken(ctx context.Context, key OAuthCacheKey) (*OAuthTokenRecord, error)
	PutToken(ctx context.Context, key OAuthCacheKey, record *OAuthTokenRecord) error
	DeleteToken(ctx context.Context, key OAuthCacheKey) error
}

// MemoryOAuthTokenStore is a thread-safe in-memory OAuthTokenStore for session-only caching and tests.
type MemoryOAuthTokenStore struct {
	mu      sync.RWMutex
	records map[string]*OAuthTokenRecord
}

// NewMemoryOAuthTokenStore creates an empty in-memory token store.
func NewMemoryOAuthTokenStore() *MemoryOAuthTokenStore {
	return &MemoryOAuthTokenStore{
		records: make(map[string]*OAuthTokenRecord),
	}
}

func (s *MemoryOAuthTokenStore) GetToken(_ context.Context, key OAuthCacheKey) (*OAuthTokenRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.records[key.StorageKey()]
	if !ok || rec == nil {
		return nil, nil
	}
	return rec.Clone(), nil
}

func (s *MemoryOAuthTokenStore) PutToken(_ context.Context, key OAuthCacheKey, record *OAuthTokenRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.records == nil {
		s.records = make(map[string]*OAuthTokenRecord)
	}
	if record == nil {
		delete(s.records, key.StorageKey())
		return nil
	}
	s.records[key.StorageKey()] = record.Clone()
	return nil
}

func (s *MemoryOAuthTokenStore) DeleteToken(_ context.Context, key OAuthCacheKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.records, key.StorageKey())
	return nil
}

// BrowserOpener opens an external authorization URL in the user's system browser.
type BrowserOpener interface {
	OpenURL(ctx context.Context, authorizationURL string) error
}

// BrowserOpenerFunc adapts a function to BrowserOpener.
type BrowserOpenerFunc func(ctx context.Context, authorizationURL string) error

func (f BrowserOpenerFunc) OpenURL(ctx context.Context, authorizationURL string) error {
	return f(ctx, authorizationURL)
}

// LoopbackCallbackResult holds the query parameters captured by a single-use RFC 8252 loopback redirect.
type LoopbackCallbackResult struct {
	Code             string
	State            string
	Error            string
	ErrorDescription string
}

// LoopbackSession represents an active ephemeral loopback HTTP listener waiting for an OAuth redirect.
type LoopbackSession interface {
	RedirectURI() string
	WaitForCallback(ctx context.Context) (LoopbackCallbackResult, error)
	Close() error
}

// LoopbackListenerFactory starts an ephemeral loopback listener on 127.0.0.1 (or [::1]).
type LoopbackListenerFactory interface {
	StartLoopback(ctx context.Context, preferredRedirectURI string) (LoopbackSession, error)
}

// OAuthHTTPResponse is the bounded HTTP response returned by OAuthHTTPDoer.
type OAuthHTTPResponse struct {
	StatusCode    int
	Headers       map[string]string
	Body          []byte
	BodyTruncated bool
}

// OAuthHTTPDoer abstracts HTTP execution for OAuth/OIDC control requests (Discovery, JWKS, Token, Device, Revoke).
type OAuthHTTPDoer interface {
	Do(ctx context.Context, method, targetURL string, headers map[string]string, body []byte, maxBytes int64, netOpts ExecuteOptions) (*OAuthHTTPResponse, error)
}

type defaultOAuthHTTPDoer struct{}

func (defaultOAuthHTTPDoer) Do(ctx context.Context, method, targetURL string, headers map[string]string, body []byte, maxBytes int64, netOpts ExecuteOptions) (*OAuthHTTPResponse, error) {
	opts := netOpts
	// OAuthService performs synchronized redaction on all returned OAuthError values,
	// so avoid unsynchronized concurrent Redactor mutations inside ExecuteWithOptions.
	opts.Redactor = nil
	// OAuth control endpoints (Discovery, JWKS, Token, Device, Revocation) must NEVER follow redirects
	// that could leak client secrets, authorization codes, or tokens across origins.
	opts.Redirect = RedirectPolicy{
		Mode:    RedirectNone,
		MaxHops: 0,
	}
	if maxBytes > 0 {
		opts.MaxResponseBytes = maxBytes
	}
	kvHeaders := make([][2]string, 0, len(headers))
	for k, v := range headers {
		kvHeaders = append(kvHeaders, [2]string{k, v})
	}
	plan := &Plan{
		Method:  method,
		URL:     targetURL,
		Headers: kvHeaders,
		Body:    body,
		HasBody: len(body) > 0,
		Timeout: DefaultTimeout,
	}
	res, err := ExecuteWithOptions(ctx, plan, opts)
	if err != nil {
		return nil, err
	}
	hdrMap := make(map[string]string, len(res.Headers))
	for _, kv := range res.Headers {
		hdrMap[kv[0]] = kv[1]
	}
	return &OAuthHTTPResponse{
		StatusCode:    res.Status,
		Headers:       hdrMap,
		Body:          res.Body,
		BodyTruncated: res.Truncated,
	}, nil
}

func executeOAuthHTTP(ctx context.Context, method, targetURL string, headers map[string]string, body []byte, maxBytes int64, netOpts ExecuteOptions, doer OAuthHTTPDoer) (*OAuthHTTPResponse, error) {
	if doer == nil {
		doer = defaultOAuthHTTPDoer{}
	}
	return doer.Do(ctx, method, targetURL, headers, body, maxBytes, netOpts)
}

func evaluateOAuthEndpointPolicy(endpointURL, method string, hasCredentials bool, opts OIDCFetchOptions) error {
	if strings.TrimSpace(endpointURL) == "" {
		return &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "OAuth endpoint URL is empty",
		}
	}
	pol := opts.TargetPolicy
	if !opts.HasTargetPolicy {
		pol = opts.Network.EffectiveTargetPolicy()
	}
	if opts.Network.TLS.InsecureSkipVerify && pol.EnforceTLSVerify {
		return &OAuthError{
			Category: OAuthErrTargetPolicyDenied,
			Endpoint: sanitizeURLForDisplay(endpointURL, opts.Redactor),
			Message:  "target policy forbids InsecureSkipVerify on OAuth/OIDC endpoints",
		}
	}
	dec, err := EvaluateTarget(pol, TargetCheckInput{
		URL:            endpointURL,
		Method:         method,
		EnvID:          opts.EnvID,
		HasCredentials: hasCredentials,
		Interactive:    opts.Interactive,
		Confirmed:      opts.Confirmed,
	})
	if err != nil {
		return &OAuthError{
			Category: OAuthErrTargetPolicyDenied,
			Endpoint: sanitizeURLForDisplay(endpointURL, opts.Redactor),
			Message:  err.Error(),
			Cause:    err,
		}
	}
	if dec != nil && dec.Outcome == DecisionConfirmRequired && !opts.Confirmed {
		return &OAuthError{
			Category: OAuthErrTargetPolicyDenied,
			Endpoint: sanitizeURLForDisplay(endpointURL, opts.Redactor),
			Message:  fmt.Sprintf("target policy requires confirmation: %s", strings.Join(dec.Reasons, "; ")),
		}
	}
	return nil
}

func sanitizeURLForDisplay(rawURL string, redactor *Redactor) string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		if redactor != nil {
			return safeRedactString(redactor, rawURL)
		}
		return "[invalid-url]"
	}
	u.User = nil
	u.RawQuery = ""
	u.Fragment = ""
	out := u.String()
	if redactor != nil {
		out = safeRedactString(redactor, out)
	}
	return out
}

// GeneratePKCEVerifierAndChallenge generates a 256-bit high-entropy RFC 7636 PKCE code_verifier and S256 code_challenge.
func GeneratePKCEVerifierAndChallenge() (verifier string, challenge string, err error) {
	buf := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", "", fmt.Errorf("failed to generate PKCE code_verifier entropy: %w", err)
	}
	verifier = base64.RawURLEncoding.EncodeToString(buf)
	sum := sha256.Sum256([]byte(verifier))
	challenge = base64.RawURLEncoding.EncodeToString(sum[:])
	return verifier, challenge, nil
}

// GenerateRandomToken generates a URL-safe base64 string with byteLen cryptographic random bytes (for state and nonce).
func GenerateRandomToken(byteLen int) (string, error) {
	if byteLen < 16 {
		byteLen = 24
	}
	buf := make([]byte, byteLen)
	if _, err := io.ReadFull(rand.Reader, buf); err != nil {
		return "", fmt.Errorf("failed to generate cryptographic random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// IsOAuthErrorCategory checks whether err is an *OAuthError with the given category.
func IsOAuthErrorCategory(err error, cat OAuthErrorCategory) bool {
	var oe *OAuthError
	if errors.As(err, &oe) {
		return oe.Category == cat
	}
	return false
}
