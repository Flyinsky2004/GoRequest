package core

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/subtle"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	// MaxOIDCDiscoveryBytes caps OIDC Discovery responses at 1 MiB.
	MaxOIDCDiscoveryBytes = 1 << 20
	// MaxJWKSPayloadBytes caps JWKS endpoint responses at 1 MiB.
	MaxJWKSPayloadBytes = 1 << 20
	// MaxJWKSKeys caps the number of keys parsed from a single JWKS document.
	MaxJWKSKeys = 64
	// MaxJWKSCacheEntries caps the number of distinct jwks_uri entries held in memory.
	MaxJWKSCacheEntries = 32
	// DefaultJWKSTTL is the default cache lifetime for a fetched JWKS document.
	DefaultJWKSTTL = 15 * time.Minute
	// MinJWKSRefreshInterval is the minimum cooldown before an unknown kid triggers a JWKS network refetch.
	MinJWKSRefreshInterval = 30 * time.Second
	// DefaultOIDCClockSkew is the default clock tolerance for exp/nbf/iat validation.
	DefaultOIDCClockSkew = 60 * time.Second
)

// DefaultAllowedJWSAlgorithms lists the asymmetric JWS algorithms allowed for OIDC ID Token verification.
var DefaultAllowedJWSAlgorithms = []string{
	"RS256", "RS384", "RS512",
	"PS256", "PS384", "PS512",
	"ES256", "ES384", "ES512",
	"EdDSA",
}

// OIDCDiscoveryDocument represents the OpenID Provider Metadata returned by /.well-known/openid-configuration.
type OIDCDiscoveryDocument struct {
	Issuer                            string   `json:"issuer"`
	AuthorizationEndpoint             string   `json:"authorization_endpoint"`
	TokenEndpoint                     string   `json:"token_endpoint"`
	UserinfoEndpoint                  string   `json:"userinfo_endpoint,omitempty"`
	JWKSURI                           string   `json:"jwks_uri"`
	DeviceAuthorizationEndpoint       string   `json:"device_authorization_endpoint,omitempty"`
	RevocationEndpoint                string   `json:"revocation_endpoint,omitempty"`
	EndSessionEndpoint                string   `json:"end_session_endpoint,omitempty"`
	ScopesSupported                   []string `json:"scopes_supported,omitempty"`
	ResponseTypesSupported            []string `json:"response_types_supported,omitempty"`
	GrantTypesSupported               []string `json:"grant_types_supported,omitempty"`
	TokenEndpointAuthMethodsSupported []string `json:"token_endpoint_auth_methods_supported,omitempty"`
	IDTokenSigningAlgValuesSupported  []string `json:"id_token_signing_alg_values_supported,omitempty"`
	CodeChallengeMethodsSupported     []string `json:"code_challenge_methods_supported,omitempty"`
}

// JWKSet represents a JSON Web Key Set (RFC 7517).
type JWKSet struct {
	Keys []JWK `json:"keys"`
}

// JWK represents an individual JSON Web Key (RFC 7517 / RFC 7518 / RFC 8037).
type JWK struct {
	Kty string   `json:"kty"`
	Use string   `json:"use,omitempty"`
	Kid string   `json:"kid,omitempty"`
	Alg string   `json:"alg,omitempty"`
	Crv string   `json:"crv,omitempty"`
	N   string   `json:"n,omitempty"`
	E   string   `json:"e,omitempty"`
	X   string   `json:"x,omitempty"`
	Y   string   `json:"y,omitempty"`
	X5c []string `json:"x5c,omitempty"`
}

// JWSHeader represents the JOSE header of a compact JWS token.
type JWSHeader struct {
	Alg string `json:"alg"`
	Typ string `json:"typ,omitempty"`
	Kid string `json:"kid,omitempty"`
}

// AudienceClaim supports both a JSON string and a JSON array of strings per OIDC Core §2.
type AudienceClaim []string

// UnmarshalJSON parses a single string or string slice into AudienceClaim.
func (a *AudienceClaim) UnmarshalJSON(data []byte) error {
	trimmed := strings.TrimSpace(string(data))
	if trimmed == "" || trimmed == "null" {
		*a = nil
		return nil
	}
	if strings.HasPrefix(trimmed, "[") {
		var arr []string
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		*a = AudienceClaim(arr)
		return nil
	}
	var single string
	if err := json.Unmarshal(data, &single); err != nil {
		return err
	}
	if single != "" {
		*a = AudienceClaim{single}
	} else {
		*a = nil
	}
	return nil
}

// IDTokenClaims represents standard OpenID Connect ID Token claims.
type IDTokenClaims struct {
	Issuer            string         `json:"iss"`
	Subject           string         `json:"sub"`
	Audience          AudienceClaim  `json:"aud"`
	AuthorizedParty   string         `json:"azp,omitempty"`
	ExpiresAt         int64          `json:"exp"`
	NotBefore         int64          `json:"nbf,omitempty"`
	IssuedAt          int64          `json:"iat"`
	AuthTime          int64          `json:"auth_time,omitempty"`
	Nonce             string         `json:"nonce,omitempty"`
	AccessTokenHash   string         `json:"at_hash,omitempty"`
	Email             string         `json:"email,omitempty"`
	EmailVerified     bool           `json:"email_verified,omitempty"`
	Name              string         `json:"name,omitempty"`
	PreferredUsername string         `json:"preferred_username,omitempty"`
	RawClaims         map[string]any `json:"-"`
}

// IDTokenInspection holds decoded ID Token metadata and an explicit cryptographic verification status.
type IDTokenInspection struct {
	Verified   bool          `json:"verified"`
	Warning    string        `json:"warning,omitempty"`
	Header     JWSHeader     `json:"header"`
	Claims     IDTokenClaims `json:"claims"`
	VerifiedAt time.Time     `json:"verifiedAt,omitempty"`
}

// OIDCFetchOptions configures transport, target security policy, and redaction for Discovery and JWKS requests.
type OIDCFetchOptions struct {
	Network         ExecuteOptions
	TargetPolicy    MergedTargetPolicy
	HasTargetPolicy bool
	EnvID           string
	Interactive     bool
	Confirmed       bool
	Redactor        *Redactor
	HTTPDoer        OAuthHTTPDoer
	ExpectedIssuer  string
}

// BuildDiscoveryURL normalizes an issuer URL or discovery endpoint into a full /.well-known/openid-configuration URL.
func BuildDiscoveryURL(issuerOrDiscoveryURL string) (discoveryURL string, inferredIssuer string, err error) {
	raw := strings.TrimSpace(issuerOrDiscoveryURL)
	if raw == "" {
		return "", "", &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "OIDC issuer or discovery URL is empty",
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return "", "", &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  fmt.Sprintf("invalid OIDC issuer or discovery URL %q", raw),
			Cause:    err,
		}
	}
	const wellKnownSuffix = "/.well-known/openid-configuration"
	if strings.HasSuffix(u.Path, wellKnownSuffix) {
		basePath := strings.TrimSuffix(u.Path, wellKnownSuffix)
		issuerCopy := *u
		issuerCopy.Path = basePath
		issuerCopy.RawQuery = ""
		issuerCopy.Fragment = ""
		return u.String(), issuerCopy.String(), nil
	}
	inferredCopy := *u
	inferredCopy.RawQuery = ""
	inferredCopy.Fragment = ""
	inferredIssuer = inferredCopy.String()

	u.Path = strings.TrimRight(u.Path, "/") + wellKnownSuffix
	u.RawQuery = ""
	u.Fragment = ""
	return u.String(), inferredIssuer, nil
}

// FetchOIDCDiscovery fetches and validates an OpenID Provider Configuration Document.
func FetchOIDCDiscovery(ctx context.Context, issuerOrDiscoveryURL string, opts OIDCFetchOptions) (*OIDCDiscoveryDocument, error) {
	discoveryURL, inferredIssuer, err := BuildDiscoveryURL(issuerOrDiscoveryURL)
	if err != nil {
		return nil, err
	}
	expectedIssuer := strings.TrimSpace(opts.ExpectedIssuer)
	if expectedIssuer == "" {
		expectedIssuer = inferredIssuer
	}

	if err := evaluateOAuthEndpointPolicy(discoveryURL, http.MethodGet, false, opts); err != nil {
		return nil, err
	}

	resp, err := executeOAuthHTTP(ctx, http.MethodGet, discoveryURL, map[string]string{
		"Accept": "application/json",
	}, nil, MaxOIDCDiscoveryBytes, opts.Network, opts.HTTPDoer)
	if err != nil {
		return nil, &OAuthError{
			Category: OAuthErrDiscoveryFailed,
			Endpoint: sanitizeURLForDisplay(discoveryURL, opts.Redactor),
			Message:  "failed to fetch OIDC discovery document",
			Cause:    err,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &OAuthError{
			Category:   OAuthErrDiscoveryFailed,
			StatusCode: resp.StatusCode,
			Endpoint:   sanitizeURLForDisplay(discoveryURL, opts.Redactor),
			Message:    fmt.Sprintf("OIDC discovery endpoint returned HTTP %d", resp.StatusCode),
		}
	}

	var doc OIDCDiscoveryDocument
	if err := json.Unmarshal(resp.Body, &doc); err != nil {
		return nil, &OAuthError{
			Category: OAuthErrDiscoveryFailed,
			Endpoint: sanitizeURLForDisplay(discoveryURL, opts.Redactor),
			Message:  "invalid JSON in OIDC discovery document",
			Cause:    err,
		}
	}
	if strings.TrimSpace(doc.Issuer) == "" {
		return nil, &OAuthError{
			Category: OAuthErrDiscoveryMismatch,
			Endpoint: sanitizeURLForDisplay(discoveryURL, opts.Redactor),
			Message:  "OIDC discovery document is missing required 'issuer' field",
		}
	}
	if expectedIssuer != "" && doc.Issuer != expectedIssuer {
		return nil, &OAuthError{
			Category: OAuthErrDiscoveryMismatch,
			Endpoint: sanitizeURLForDisplay(discoveryURL, opts.Redactor),
			Message:  fmt.Sprintf("OIDC discovery issuer mismatch: expected %q, got %q", expectedIssuer, doc.Issuer),
		}
	}
	if strings.TrimSpace(doc.TokenEndpoint) == "" {
		return nil, &OAuthError{
			Category: OAuthErrDiscoveryFailed,
			Endpoint: sanitizeURLForDisplay(discoveryURL, opts.Redactor),
			Message:  "OIDC discovery document is missing required 'token_endpoint'",
		}
	}
	return &doc, nil
}

type jwksCacheEntry struct {
	URI       string
	KeySet    JWKSet
	FetchedAt time.Time
	ExpiresAt time.Time
}

// JWKSManager manages a bounded, thread-safe cache of remote JWKS documents with key rotation support.
type JWKSManager struct {
	mu                 sync.Mutex
	fetchMu            sync.Map // map[string]*sync.Mutex per jwks_uri
	entries            map[string]jwksCacheEntry
	MaxEntries         int
	DefaultTTL         time.Duration
	MinRefreshCooldown time.Duration
	Now                func() time.Time
}

// NewJWKSManager creates a bounded JWKS cache manager with safe defaults.
func NewJWKSManager() *JWKSManager {
	return &JWKSManager{
		entries:            make(map[string]jwksCacheEntry),
		MaxEntries:         MaxJWKSCacheEntries,
		DefaultTTL:         DefaultJWKSTTL,
		MinRefreshCooldown: MinJWKSRefreshInterval,
		Now:                time.Now,
	}
}

func (m *JWKSManager) now() time.Time {
	if m != nil && m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *JWKSManager) uriMutex(jwksURI string) *sync.Mutex {
	val, _ := m.fetchMu.LoadOrStore(jwksURI, &sync.Mutex{})
	return val.(*sync.Mutex)
}

// GetKey resolves the verification public key for (jwksURI, kid, alg).
// If kid is not found in a cached JWKS and MinRefreshCooldown has elapsed since the last fetch,
// GetKey refreshes the JWKS once to seamlessly handle upstream signing key rotation.
func (m *JWKSManager) GetKey(ctx context.Context, jwksURI, kid, alg string, opts OIDCFetchOptions) (crypto.PublicKey, JWK, error) {
	uri := strings.TrimSpace(jwksURI)
	if uri == "" {
		return nil, JWK{}, &OAuthError{
			Category: OAuthErrInvalidConfig,
			Message:  "JWKS URI is required for OIDC ID Token signature verification",
		}
	}

	now := m.now()
	m.mu.Lock()
	entry, ok := m.entries[uri]
	m.mu.Unlock()

	if ok && now.Before(entry.ExpiresAt) {
		if pub, matchedJWK, err := selectKeyFromSet(entry.KeySet, kid, alg); err == nil {
			return pub, matchedJWK, nil
		}
		// Unknown kid or mismatched key in cache: check if rotation refresh is allowed by cooldown.
		cooldown := m.MinRefreshCooldown
		if cooldown <= 0 {
			cooldown = MinJWKSRefreshInterval
		}
		if now.Sub(entry.FetchedAt) < cooldown {
			return nil, JWK{}, &OAuthError{
				Category: OAuthErrJWKSKeyNotFound,
				Endpoint: sanitizeURLForDisplay(uri, opts.Redactor),
				Message:  fmt.Sprintf("signing key kid=%q alg=%q not found in cached JWKS (rotation refresh cooldown active)", kid, alg),
			}
		}
	}

	// Serialize network fetch per jwks_uri so concurrent verifications coalesce.
	uMu := m.uriMutex(uri)
	uMu.Lock()
	defer uMu.Unlock()

	// Re-check cache after acquiring per-URI lock.
	now = m.now()
	m.mu.Lock()
	entry, ok = m.entries[uri]
	m.mu.Unlock()
	if ok && now.Before(entry.ExpiresAt) {
		if pub, matchedJWK, err := selectKeyFromSet(entry.KeySet, kid, alg); err == nil {
			return pub, matchedJWK, nil
		}
		cooldown := m.MinRefreshCooldown
		if cooldown <= 0 {
			cooldown = MinJWKSRefreshInterval
		}
		if now.Sub(entry.FetchedAt) < cooldown {
			return nil, JWK{}, &OAuthError{
				Category: OAuthErrJWKSKeyNotFound,
				Endpoint: sanitizeURLForDisplay(uri, opts.Redactor),
				Message:  fmt.Sprintf("signing key kid=%q alg=%q not found in JWKS", kid, alg),
			}
		}
	}

	set, ttl, err := fetchJWKSRemote(ctx, uri, m.DefaultTTL, opts)
	if err != nil {
		return nil, JWK{}, err
	}

	fetchedAt := m.now()
	newEntry := jwksCacheEntry{
		URI:       uri,
		KeySet:    set,
		FetchedAt: fetchedAt,
		ExpiresAt: fetchedAt.Add(ttl),
	}
	m.storeEntry(newEntry)

	pub, matchedJWK, err := selectKeyFromSet(set, kid, alg)
	if err != nil {
		return nil, JWK{}, &OAuthError{
			Category: OAuthErrJWKSKeyNotFound,
			Endpoint: sanitizeURLForDisplay(uri, opts.Redactor),
			Message:  fmt.Sprintf("signing key kid=%q alg=%q not found after JWKS refresh", kid, alg),
			Cause:    err,
		}
	}
	return pub, matchedJWK, nil
}

func (m *JWKSManager) storeEntry(entry jwksCacheEntry) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.entries == nil {
		m.entries = make(map[string]jwksCacheEntry)
	}
	maxEntries := m.MaxEntries
	if maxEntries <= 0 {
		maxEntries = MaxJWKSCacheEntries
	}
	if _, exists := m.entries[entry.URI]; !exists && len(m.entries) >= maxEntries {
		// Evict the entry with the earliest ExpiresAt.
		var oldestURI string
		var oldestTime time.Time
		for k, v := range m.entries {
			if oldestURI == "" || v.ExpiresAt.Before(oldestTime) {
				oldestURI = k
				oldestTime = v.ExpiresAt
			}
		}
		if oldestURI != "" {
			delete(m.entries, oldestURI)
		}
	}
	m.entries[entry.URI] = entry
}

var maxAgePattern = regexp.MustCompile(`(?i)max-age=(\d+)`)

func parseJWKSCacheTTL(headers map[string]string, defaultTTL time.Duration) time.Duration {
	if defaultTTL <= 0 {
		defaultTTL = DefaultJWKSTTL
	}
	for k, v := range headers {
		if strings.EqualFold(k, "Cache-Control") {
			lower := strings.ToLower(v)
			if strings.Contains(lower, "no-store") {
				return time.Minute
			}
			if matches := maxAgePattern.FindStringSubmatch(v); len(matches) == 2 {
				if secs, err := strconv.ParseInt(matches[1], 10, 64); err == nil && secs > 0 {
					ttl := time.Duration(secs) * time.Second
					if ttl < time.Minute {
						return time.Minute
					}
					if ttl > 24*time.Hour {
						return 24 * time.Hour
					}
					return ttl
				}
			}
		}
	}
	return defaultTTL
}

func fetchJWKSRemote(ctx context.Context, jwksURI string, defaultTTL time.Duration, opts OIDCFetchOptions) (JWKSet, time.Duration, error) {
	if err := evaluateOAuthEndpointPolicy(jwksURI, http.MethodGet, false, opts); err != nil {
		return JWKSet{}, 0, err
	}
	resp, err := executeOAuthHTTP(ctx, http.MethodGet, jwksURI, map[string]string{
		"Accept": "application/json",
	}, nil, MaxJWKSPayloadBytes, opts.Network, opts.HTTPDoer)
	if err != nil {
		return JWKSet{}, 0, &OAuthError{
			Category: OAuthErrJWKSFetchFailed,
			Endpoint: sanitizeURLForDisplay(jwksURI, opts.Redactor),
			Message:  "failed to fetch JWKS document",
			Cause:    err,
		}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return JWKSet{}, 0, &OAuthError{
			Category:   OAuthErrJWKSFetchFailed,
			StatusCode: resp.StatusCode,
			Endpoint:   sanitizeURLForDisplay(jwksURI, opts.Redactor),
			Message:    fmt.Sprintf("JWKS endpoint returned HTTP %d", resp.StatusCode),
		}
	}
	if resp.BodyTruncated {
		return JWKSet{}, 0, &OAuthError{
			Category: OAuthErrJWKSFetchFailed,
			Endpoint: sanitizeURLForDisplay(jwksURI, opts.Redactor),
			Message:  fmt.Sprintf("JWKS payload exceeds maximum limit of %d bytes", MaxJWKSPayloadBytes),
		}
	}

	var set JWKSet
	if err := json.Unmarshal(resp.Body, &set); err != nil {
		return JWKSet{}, 0, &OAuthError{
			Category: OAuthErrJWKSFetchFailed,
			Endpoint: sanitizeURLForDisplay(jwksURI, opts.Redactor),
			Message:  "invalid JSON in JWKS document",
			Cause:    err,
		}
	}
	if len(set.Keys) == 0 {
		return JWKSet{}, 0, &OAuthError{
			Category: OAuthErrJWKSFetchFailed,
			Endpoint: sanitizeURLForDisplay(jwksURI, opts.Redactor),
			Message:  "JWKS document contains no keys",
		}
	}
	if len(set.Keys) > MaxJWKSKeys {
		return JWKSet{}, 0, &OAuthError{
			Category: OAuthErrJWKSFetchFailed,
			Endpoint: sanitizeURLForDisplay(jwksURI, opts.Redactor),
			Message:  fmt.Sprintf("JWKS document contains %d keys, exceeding maximum of %d", len(set.Keys), MaxJWKSKeys),
		}
	}

	ttl := parseJWKSCacheTTL(resp.Headers, defaultTTL)
	return set, ttl, nil
}

func selectKeyFromSet(set JWKSet, kid, alg string) (crypto.PublicKey, JWK, error) {
	expectedKty := expectedKtyForAlg(alg)
	var candidates []JWK
	for _, key := range set.Keys {
		if key.Use != "" && key.Use != "sig" {
			continue
		}
		if key.Alg != "" && key.Alg != alg {
			continue
		}
		if expectedKty != "" && !strings.EqualFold(key.Kty, expectedKty) {
			continue
		}
		if kid != "" {
			if key.Kid == kid {
				candidates = append(candidates, key)
			}
		} else {
			candidates = append(candidates, key)
		}
	}
	if len(candidates) == 0 {
		return nil, JWK{}, fmt.Errorf("no matching JWK for kid=%q alg=%q", kid, alg)
	}
	if kid == "" && len(candidates) > 1 {
		return nil, JWK{}, fmt.Errorf("ambiguous JWK selection: token header has no 'kid' and JWKS has %d candidate keys", len(candidates))
	}
	pub, err := parseJWKPublicKey(candidates[0])
	if err != nil {
		return nil, JWK{}, err
	}
	return pub, candidates[0], nil
}

func expectedKtyForAlg(alg string) string {
	switch alg {
	case "RS256", "RS384", "RS512", "PS256", "PS384", "PS512":
		return "RSA"
	case "ES256", "ES384", "ES512":
		return "EC"
	case "EdDSA":
		return "OKP"
	default:
		return ""
	}
}

func parseJWKPublicKey(key JWK) (crypto.PublicKey, error) {
	switch strings.ToUpper(key.Kty) {
	case "RSA":
		if key.N != "" && key.E != "" {
			nb, err := base64.RawURLEncoding.DecodeString(key.N)
			if err != nil {
				return nil, fmt.Errorf("invalid RSA modulus 'n': %w", err)
			}
			eb, err := base64.RawURLEncoding.DecodeString(key.E)
			if err != nil {
				return nil, fmt.Errorf("invalid RSA exponent 'e': %w", err)
			}
			n := new(big.Int).SetBytes(nb)
			eBig := new(big.Int).SetBytes(eb)
			if !eBig.IsInt64() {
				return nil, fmt.Errorf("RSA exponent 'e' overflows int64")
			}
			e := int(eBig.Int64())
			if e < 3 || e%2 == 0 {
				return nil, fmt.Errorf("invalid RSA public exponent %d", e)
			}
			if n.BitLen() < 2048 {
				return nil, fmt.Errorf("RSA modulus too small (%d bits < 2048)", n.BitLen())
			}
			return &rsa.PublicKey{N: n, E: e}, nil
		}
		if len(key.X5c) > 0 {
			der, err := base64.StdEncoding.DecodeString(key.X5c[0])
			if err != nil {
				return nil, fmt.Errorf("invalid x5c certificate encoding: %w", err)
			}
			cert, err := x509.ParseCertificate(der)
			if err != nil {
				return nil, fmt.Errorf("invalid x5c certificate: %w", err)
			}
			rsaPub, ok := cert.PublicKey.(*rsa.PublicKey)
			if !ok {
				return nil, fmt.Errorf("x5c certificate does not contain an RSA public key")
			}
			if rsaPub.N.BitLen() < 2048 {
				return nil, fmt.Errorf("x5c RSA modulus too small (%d bits < 2048)", rsaPub.N.BitLen())
			}
			return rsaPub, nil
		}
		return nil, fmt.Errorf("RSA JWK missing 'n'/'e' parameters")

	case "EC":
		var curve elliptic.Curve
		var coordLen int
		switch key.Crv {
		case "P-256":
			curve = elliptic.P256()
			coordLen = 32
		case "P-384":
			curve = elliptic.P384()
			coordLen = 48
		case "P-521":
			curve = elliptic.P521()
			coordLen = 66
		default:
			return nil, fmt.Errorf("unsupported EC curve %q", key.Crv)
		}
		xb, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(xb) == 0 || len(xb) > coordLen {
			return nil, fmt.Errorf("invalid EC 'x' coordinate")
		}
		yb, err := base64.RawURLEncoding.DecodeString(key.Y)
		if err != nil || len(yb) == 0 || len(yb) > coordLen {
			return nil, fmt.Errorf("invalid EC 'y' coordinate")
		}
		// Build uncompressed SEC1 point 0x04 || X || Y padded to coordLen bytes and validate via crypto/ecdh.
		uncompressed := make([]byte, 1+2*coordLen)
		uncompressed[0] = 0x04
		copy(uncompressed[1+coordLen-len(xb):1+coordLen], xb)
		copy(uncompressed[1+2*coordLen-len(yb):1+2*coordLen], yb)
		x := new(big.Int).SetBytes(xb)
		y := new(big.Int).SetBytes(yb)
		pub := &ecdsa.PublicKey{Curve: curve, X: x, Y: y}
		if _, err := pub.ECDH(); err != nil {
			return nil, fmt.Errorf("EC point is not on curve %s: %w", key.Crv, err)
		}
		return pub, nil

	case "OKP":
		if key.Crv != "Ed25519" {
			return nil, fmt.Errorf("unsupported OKP curve %q", key.Crv)
		}
		xb, err := base64.RawURLEncoding.DecodeString(key.X)
		if err != nil || len(xb) != ed25519.PublicKeySize {
			return nil, fmt.Errorf("invalid Ed25519 public key length")
		}
		return ed25519.PublicKey(xb), nil

	default:
		return nil, fmt.Errorf("unsupported or disallowed JWK kty %q", key.Kty)
	}
}

// InspectIDTokenUnverified decodes a compact JWT/JWS into header and claims WITHOUT verifying its signature.
// It always sets Verified=false and populates Warning so the caller/UI never confuses it with a verified token.
func InspectIDTokenUnverified(rawToken string) (*IDTokenInspection, error) {
	header, claims, _, _, err := splitAndDecodeJWS(rawToken)
	if err != nil {
		return nil, err
	}
	return &IDTokenInspection{
		Verified: false,
		Warning:  "Unverified preview: signature and issuer/audience claims have NOT been cryptographically verified.",
		Header:   header,
		Claims:   claims,
	}, nil
}

// IDTokenVerifyParams defines the verification parameters for VerifyIDToken.
type IDTokenVerifyParams struct {
	ExpectedIssuer    string
	ClientID          string
	ExpectedNonce     string
	AccessToken       string
	JWKSURI           string
	JWKSManager       *JWKSManager
	StaticKeySet      *JWKSet
	AllowedAlgorithms []string
	ClockSkew         time.Duration
	Now               func() time.Time
	FetchOptions      OIDCFetchOptions
}

// VerifyIDToken cryptographically verifies an OIDC ID Token's JWS signature against JWKS (or StaticKeySet)
// and validates iss, sub, aud, azp, exp, nbf, iat, nonce, and at_hash claims.
func VerifyIDToken(ctx context.Context, rawToken string, params IDTokenVerifyParams) (*IDTokenInspection, error) {
	header, claims, signingInput, signature, err := splitAndDecodeJWS(rawToken)
	if err != nil {
		return nil, err
	}

	alg := strings.TrimSpace(header.Alg)
	if alg == "" || strings.EqualFold(alg, "none") {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidSignature,
			Message:  fmt.Sprintf("disallowed JWS algorithm %q in ID Token header", header.Alg),
		}
	}
	allowedAlgs := params.AllowedAlgorithms
	if len(allowedAlgs) == 0 {
		allowedAlgs = DefaultAllowedJWSAlgorithms
	}
	if !slices.Contains(allowedAlgs, alg) {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidSignature,
			Message:  fmt.Sprintf("JWS algorithm %q is not in allowed asymmetric algorithms %v", alg, allowedAlgs),
		}
	}
	if len(signature) == 0 {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidSignature,
			Message:  "ID Token has an empty signature segment",
		}
	}

	// Resolve public key via StaticKeySet or JWKSManager.
	var pubKey crypto.PublicKey
	if params.StaticKeySet != nil {
		var keyErr error
		pubKey, _, keyErr = selectKeyFromSet(*params.StaticKeySet, header.Kid, alg)
		if keyErr != nil {
			return nil, &OAuthError{
				Category: OAuthErrJWKSKeyNotFound,
				Message:  fmt.Sprintf("signing key kid=%q alg=%q not found in static JWKS", header.Kid, alg),
				Cause:    keyErr,
			}
		}
	} else {
		mgr := params.JWKSManager
		if mgr == nil {
			mgr = NewJWKSManager()
		}
		var keyErr error
		pubKey, _, keyErr = mgr.GetKey(ctx, params.JWKSURI, header.Kid, alg, params.FetchOptions)
		if keyErr != nil {
			return nil, keyErr
		}
	}

	if err := verifyJWSSignature(alg, pubKey, []byte(signingInput), signature); err != nil {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidSignature,
			Message:  fmt.Sprintf("ID Token signature verification failed (%s): %v", alg, err),
			Cause:    err,
		}
	}

	// Validate Claims per OpenID Connect Core 1.0 §3.1.3.7.
	now := time.Now()
	if params.Now != nil {
		now = params.Now()
	}
	skew := params.ClockSkew
	if skew < 0 {
		skew = 0
	} else if skew == 0 {
		skew = DefaultOIDCClockSkew
	}

	expectedIss := strings.TrimSpace(params.ExpectedIssuer)
	if expectedIss == "" {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  "expected OIDC issuer is not configured",
		}
	}
	if claims.Issuer != expectedIss {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  fmt.Sprintf("ID Token issuer mismatch: expected %q, got %q", expectedIss, claims.Issuer),
		}
	}

	if strings.TrimSpace(claims.Subject) == "" || len(claims.Subject) > 255 {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  "ID Token 'sub' claim is missing or exceeds 255 characters",
		}
	}

	clientID := strings.TrimSpace(params.ClientID)
	if clientID == "" {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  "expected OIDC client_id is empty",
		}
	}
	if !slices.Contains([]string(claims.Audience), clientID) {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  fmt.Sprintf("ID Token audience %v does not contain client_id %q", claims.Audience, clientID),
		}
	}
	if (len(claims.Audience) > 1 || claims.AuthorizedParty != "") && claims.AuthorizedParty != "" && claims.AuthorizedParty != clientID {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  fmt.Sprintf("ID Token authorized party (azp) %q does not match client_id %q", claims.AuthorizedParty, clientID),
		}
	}

	if claims.ExpiresAt <= 0 {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  "ID Token is missing required 'exp' claim",
		}
	}
	if now.Add(-skew).Unix() >= claims.ExpiresAt {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenExpired,
			Message:  fmt.Sprintf("ID Token expired at %s", time.Unix(claims.ExpiresAt, 0).UTC().Format(time.RFC3339)),
		}
	}

	if claims.NotBefore > 0 && now.Add(skew).Unix() < claims.NotBefore {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  fmt.Sprintf("ID Token is not valid before %s", time.Unix(claims.NotBefore, 0).UTC().Format(time.RFC3339)),
		}
	}

	if claims.IssuedAt <= 0 {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  "ID Token is missing required 'iat' claim",
		}
	}
	if claims.IssuedAt > now.Add(skew).Unix() {
		return nil, &OAuthError{
			Category: OAuthErrIDTokenInvalidClaims,
			Message:  "ID Token 'iat' claim is in the future beyond clock skew tolerance",
		}
	}

	if params.ExpectedNonce != "" || claims.Nonce != "" {
		if params.ExpectedNonce == "" || claims.Nonce == "" ||
			subtle.ConstantTimeCompare([]byte(params.ExpectedNonce), []byte(claims.Nonce)) != 1 {
			return nil, &OAuthError{
				Category: OAuthErrNonceMismatch,
				Message:  "ID Token 'nonce' claim does not match authorization request nonce",
			}
		}
	}

	if claims.AccessTokenHash != "" && params.AccessToken != "" {
		expectedAtHash, err := computeAtHash(alg, params.AccessToken)
		if err != nil || subtle.ConstantTimeCompare([]byte(expectedAtHash), []byte(claims.AccessTokenHash)) != 1 {
			return nil, &OAuthError{
				Category: OAuthErrIDTokenInvalidClaims,
				Message:  "ID Token 'at_hash' claim does not match the issued access_token",
			}
		}
	}

	return &IDTokenInspection{
		Verified:   true,
		Header:     header,
		Claims:     claims,
		VerifiedAt: now.UTC(),
	}, nil
}

func splitAndDecodeJWS(rawToken string) (JWSHeader, IDTokenClaims, string, []byte, error) {
	trimmed := strings.TrimSpace(rawToken)
	parts := strings.Split(trimmed, ".")
	if len(parts) != 3 {
		return JWSHeader{}, IDTokenClaims{}, "", nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Message:  "compact JWS ID Token must have exactly 3 dot-separated segments",
		}
	}

	headerBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return JWSHeader{}, IDTokenClaims{}, "", nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Message:  "invalid base64url encoding in ID Token header",
			Cause:    err,
		}
	}
	var header JWSHeader
	if err := json.Unmarshal(headerBytes, &header); err != nil {
		return JWSHeader{}, IDTokenClaims{}, "", nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Message:  "invalid JSON in ID Token header",
			Cause:    err,
		}
	}

	payloadBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return JWSHeader{}, IDTokenClaims{}, "", nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Message:  "invalid base64url encoding in ID Token payload",
			Cause:    err,
		}
	}
	var claims IDTokenClaims
	if err := json.Unmarshal(payloadBytes, &claims); err != nil {
		return JWSHeader{}, IDTokenClaims{}, "", nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Message:  "invalid JSON in ID Token payload",
			Cause:    err,
		}
	}
	var rawMap map[string]any
	_ = json.Unmarshal(payloadBytes, &rawMap)
	claims.RawClaims = rawMap

	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return JWSHeader{}, IDTokenClaims{}, "", nil, &OAuthError{
			Category: OAuthErrIDTokenMalformed,
			Message:  "invalid base64url encoding in ID Token signature",
			Cause:    err,
		}
	}

	return header, claims, parts[0] + "." + parts[1], sigBytes, nil
}

func verifyJWSSignature(alg string, pubKey crypto.PublicKey, signingInput, sig []byte) error {
	switch alg {
	case "RS256", "RS384", "RS512":
		rsaPub, ok := pubKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("expected RSA public key for %s", alg)
		}
		h, digest := hashForJWSAlg(alg, signingInput)
		return rsa.VerifyPKCS1v15(rsaPub, h, digest, sig)

	case "PS256", "PS384", "PS512":
		rsaPub, ok := pubKey.(*rsa.PublicKey)
		if !ok {
			return fmt.Errorf("expected RSA public key for %s", alg)
		}
		h, digest := hashForJWSAlg(alg, signingInput)
		return rsa.VerifyPSS(rsaPub, h, digest, sig, &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       h,
		})

	case "ES256", "ES384", "ES512":
		ecPub, ok := pubKey.(*ecdsa.PublicKey)
		if !ok {
			return fmt.Errorf("expected ECDSA public key for %s", alg)
		}
		var coordLen int
		var expectedCurve elliptic.Curve
		switch alg {
		case "ES256":
			coordLen = 32
			expectedCurve = elliptic.P256()
		case "ES384":
			coordLen = 48
			expectedCurve = elliptic.P384()
		case "ES512":
			coordLen = 66
			expectedCurve = elliptic.P521()
		}
		if ecPub.Curve != expectedCurve {
			return fmt.Errorf("ECDSA curve mismatch for %s", alg)
		}
		if len(sig) != 2*coordLen {
			return fmt.Errorf("invalid JWS ECDSA signature length %d for %s (expected %d)", len(sig), alg, 2*coordLen)
		}
		r := new(big.Int).SetBytes(sig[:coordLen])
		s := new(big.Int).SetBytes(sig[coordLen:])
		_, digest := hashForJWSAlg(alg, signingInput)
		if !ecdsa.Verify(ecPub, digest, r, s) {
			return fmt.Errorf("ECDSA signature verification failed")
		}
		return nil

	case "EdDSA":
		edPub, ok := pubKey.(ed25519.PublicKey)
		if !ok {
			return fmt.Errorf("expected Ed25519 public key for EdDSA")
		}
		if len(sig) != ed25519.SignatureSize || !ed25519.Verify(edPub, signingInput, sig) {
			return fmt.Errorf("Ed25519 signature verification failed")
		}
		return nil

	default:
		return fmt.Errorf("unsupported JWS algorithm %q", alg)
	}
}

func hashForJWSAlg(alg string, input []byte) (crypto.Hash, []byte) {
	switch alg {
	case "RS256", "PS256", "ES256":
		sum := sha256.Sum256(input)
		return crypto.SHA256, sum[:]
	case "RS384", "PS384", "ES384":
		sum := sha512.Sum384(input)
		return crypto.SHA384, sum[:]
	case "RS512", "PS512", "ES512", "EdDSA":
		sum := sha512.Sum512(input)
		return crypto.SHA512, sum[:]
	default:
		sum := sha256.Sum256(input)
		return crypto.SHA256, sum[:]
	}
}

func computeAtHash(alg, accessToken string) (string, error) {
	_, digest := hashForJWSAlg(alg, []byte(accessToken))
	if len(digest) < 16 {
		return "", fmt.Errorf("invalid digest length for %s", alg)
	}
	leftHalf := digest[:len(digest)/2]
	return base64.RawURLEncoding.EncodeToString(leftHalf), nil
}
