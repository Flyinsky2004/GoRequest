package core

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testOIDCKeyPair struct {
	kid     string
	alg     string
	rsaPriv *rsa.PrivateKey
	ecPriv  *ecdsa.PrivateKey
	edPriv  ed25519.PrivateKey
	jwk     JWK
}

func newTestRSAKeyPair(t *testing.T, kid, alg string) testOIDCKeyPair {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey: %v", err)
	}
	pub := &priv.PublicKey
	jwk := JWK{
		Kty: "RSA",
		Use: "sig",
		Kid: kid,
		Alg: alg,
		N:   base64.RawURLEncoding.EncodeToString(pub.N.Bytes()),
		E:   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(pub.E)).Bytes()),
	}
	return testOIDCKeyPair{kid: kid, alg: alg, rsaPriv: priv, jwk: jwk}
}

func newTestECKeyPair(t *testing.T, kid string) testOIDCKeyPair {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}
	pub := &priv.PublicKey
	xb := pub.X.FillBytes(make([]byte, 32))
	yb := pub.Y.FillBytes(make([]byte, 32))
	jwk := JWK{
		Kty: "EC",
		Use: "sig",
		Kid: kid,
		Alg: "ES256",
		Crv: "P-256",
		X:   base64.RawURLEncoding.EncodeToString(xb),
		Y:   base64.RawURLEncoding.EncodeToString(yb),
	}
	return testOIDCKeyPair{kid: kid, alg: "ES256", ecPriv: priv, jwk: jwk}
}

func newTestEd25519KeyPair(t *testing.T, kid string) testOIDCKeyPair {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("ed25519.GenerateKey: %v", err)
	}
	jwk := JWK{
		Kty: "OKP",
		Use: "sig",
		Kid: kid,
		Alg: "EdDSA",
		Crv: "Ed25519",
		X:   base64.RawURLEncoding.EncodeToString(pub),
	}
	return testOIDCKeyPair{kid: kid, alg: "EdDSA", edPriv: priv, jwk: jwk}
}

func (kp testOIDCKeyPair) signJWS(t *testing.T, claims map[string]any) string {
	t.Helper()
	hdr := map[string]any{
		"alg": kp.alg,
		"typ": "JWT",
		"kid": kp.kid,
	}
	hdrJSON, err := json.Marshal(hdr)
	if err != nil {
		t.Fatalf("marshal header: %v", err)
	}
	payloadJSON, err := json.Marshal(claims)
	if err != nil {
		t.Fatalf("marshal claims: %v", err)
	}
	signingInput := base64.RawURLEncoding.EncodeToString(hdrJSON) + "." + base64.RawURLEncoding.EncodeToString(payloadJSON)

	var sig []byte
	switch kp.alg {
	case "RS256":
		sum := sha256.Sum256([]byte(signingInput))
		sig, err = rsa.SignPKCS1v15(rand.Reader, kp.rsaPriv, crypto.SHA256, sum[:])
	case "PS256":
		sum := sha256.Sum256([]byte(signingInput))
		sig, err = rsa.SignPSS(rand.Reader, kp.rsaPriv, crypto.SHA256, sum[:], &rsa.PSSOptions{
			SaltLength: rsa.PSSSaltLengthEqualsHash,
			Hash:       crypto.SHA256,
		})
	case "ES256":
		sum := sha256.Sum256([]byte(signingInput))
		var r, s *big.Int
		r, s, err = ecdsa.Sign(rand.Reader, kp.ecPriv, sum[:])
		if err == nil {
			sig = make([]byte, 64)
			r.FillBytes(sig[:32])
			s.FillBytes(sig[32:])
		}
	case "EdDSA":
		sig = ed25519.Sign(kp.edPriv, []byte(signingInput))
	default:
		t.Fatalf("unsupported test signing alg %q", kp.alg)
	}
	if err != nil {
		t.Fatalf("signJWS (%s): %v", kp.alg, err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func TestOIDCDiscoveryStrictIssuerValidation(t *testing.T) {
	var srv *httptest.Server
	var returnMismatchedIssuer atomic.Bool
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/.well-known/openid-configuration" {
			http.NotFound(w, r)
			return
		}
		iss := srv.URL
		if returnMismatchedIssuer.Load() {
			iss = "https://spoofed-issuer.example.com"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(OIDCDiscoveryDocument{
			Issuer:                iss,
			AuthorizationEndpoint: srv.URL + "/authorize",
			TokenEndpoint:         srv.URL + "/token",
			JWKSURI:               srv.URL + "/jwks.json",
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	doc, err := FetchOIDCDiscovery(ctx, srv.URL, OIDCFetchOptions{
		ExpectedIssuer: srv.URL,
	})
	if err != nil {
		t.Fatalf("FetchOIDCDiscovery valid: %v", err)
	}
	if doc.Issuer != srv.URL || doc.TokenEndpoint != srv.URL+"/token" {
		t.Fatalf("unexpected discovery document: %+v", doc)
	}

	// Trigger issuer mismatch.
	returnMismatchedIssuer.Store(true)
	_, err = FetchOIDCDiscovery(ctx, srv.URL, OIDCFetchOptions{
		ExpectedIssuer: srv.URL,
	})
	if !IsOAuthErrorCategory(err, OAuthErrDiscoveryMismatch) {
		t.Fatalf("expected OAuthErrDiscoveryMismatch on mismatched issuer, got: %v", err)
	}
}

func TestJWKSManagerBoundedCacheAndKeyRotation(t *testing.T) {
	keyV1 := newTestRSAKeyPair(t, "kid-v1", "RS256")
	keyV2 := newTestRSAKeyPair(t, "kid-v2", "RS256")

	var activeKey atomic.Int32
	activeKey.Store(1)
	var jwksHits atomic.Int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		jwksHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "public, max-age=600")
		if activeKey.Load() == 1 {
			_ = json.NewEncoder(w).Encode(JWKSet{Keys: []JWK{keyV1.jwk}})
			return
		}
		_ = json.NewEncoder(w).Encode(JWKSet{Keys: []JWK{keyV1.jwk, keyV2.jwk}})
	}))
	defer srv.Close()

	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	mgr := NewJWKSManager()
	mgr.MinRefreshCooldown = 30 * time.Second
	mgr.Now = func() time.Time { return now }

	ctx := context.Background()
	baseClaims := map[string]any{
		"iss":   srv.URL,
		"sub":   "user-123",
		"aud":   "client-abc",
		"exp":   now.Add(10 * time.Minute).Unix(),
		"iat":   now.Unix(),
		"nonce": "nonce-xyz",
	}

	// 1. Verify token signed with keyV1 (populates JWKS cache, hits = 1).
	tokV1 := keyV1.signJWS(t, baseClaims)
	insp, err := VerifyIDToken(ctx, tokV1, IDTokenVerifyParams{
		ExpectedIssuer: srv.URL,
		ClientID:       "client-abc",
		ExpectedNonce:  "nonce-xyz",
		JWKSURI:        srv.URL + "/jwks",
		JWKSManager:    mgr,
		Now:            func() time.Time { return now },
	})
	if err != nil || !insp.Verified {
		t.Fatalf("VerifyIDToken keyV1 failed: %v", err)
	}
	if jwksHits.Load() != 1 {
		t.Fatalf("expected 1 JWKS network fetch, got %d", jwksHits.Load())
	}

	// Second verification with keyV1 must be served from cache (hits still = 1).
	if _, err := VerifyIDToken(ctx, tokV1, IDTokenVerifyParams{
		ExpectedIssuer: srv.URL,
		ClientID:       "client-abc",
		ExpectedNonce:  "nonce-xyz",
		JWKSURI:        srv.URL + "/jwks",
		JWKSManager:    mgr,
		Now:            func() time.Time { return now },
	}); err != nil {
		t.Fatalf("cached VerifyIDToken keyV1 failed: %v", err)
	}
	if jwksHits.Load() != 1 {
		t.Fatalf("expected cached JWKS lookup with 1 hit, got %d", jwksHits.Load())
	}

	// 2. Server rotates to keyV2. Before cooldown (5s later), unknown kid-v2 must NOT spam the JWKS endpoint.
	activeKey.Store(2)
	now = now.Add(5 * time.Second)
	tokV2 := keyV2.signJWS(t, baseClaims)
	_, err = VerifyIDToken(ctx, tokV2, IDTokenVerifyParams{
		ExpectedIssuer: srv.URL,
		ClientID:       "client-abc",
		ExpectedNonce:  "nonce-xyz",
		JWKSURI:        srv.URL + "/jwks",
		JWKSManager:    mgr,
		Now:            func() time.Time { return now },
	})
	if !IsOAuthErrorCategory(err, OAuthErrJWKSKeyNotFound) {
		t.Fatalf("expected OAuthErrJWKSKeyNotFound during cooldown, got: %v", err)
	}
	if jwksHits.Load() != 1 {
		t.Fatalf("expected cooldown to block refetch (hits=1), got %d", jwksHits.Load())
	}

	// 3. Advance clock past MinRefreshCooldown (35s): VerifyIDToken automatically refetches JWKS and succeeds!
	now = now.Add(30 * time.Second)
	inspV2, err := VerifyIDToken(ctx, tokV2, IDTokenVerifyParams{
		ExpectedIssuer: srv.URL,
		ClientID:       "client-abc",
		ExpectedNonce:  "nonce-xyz",
		JWKSURI:        srv.URL + "/jwks",
		JWKSManager:    mgr,
		Now:            func() time.Time { return now },
	})
	if err != nil || !inspV2.Verified || inspV2.Header.Kid != "kid-v2" {
		t.Fatalf("expected rotated keyV2 verification to succeed after cooldown, got err=%v insp=%+v", err, inspV2)
	}
	if jwksHits.Load() != 2 {
		t.Fatalf("expected exactly 2 JWKS fetches after rotation, got %d", jwksHits.Load())
	}
}

func TestVerifyIDTokenAlgorithmsAndSecurityRejections(t *testing.T) {
	now := time.Date(2026, 11, 6, 12, 0, 0, 0, time.UTC)
	rsaPSS := newTestRSAKeyPair(t, "ps-key", "PS256")
	ecKey := newTestECKeyPair(t, "ec-key")
	edKey := newTestEd25519KeyPair(t, "ed-key")

	staticJWKS := &JWKSet{
		Keys: []JWK{rsaPSS.jwk, ecKey.jwk, edKey.jwk},
	}

	accessToken := "at_live_9876543210abcdef"
	atHashPS256, err := computeAtHash("PS256", accessToken)
	if err != nil {
		t.Fatalf("computeAtHash: %v", err)
	}

	validClaims := map[string]any{
		"iss":     "https://auth.example.com",
		"sub":     "subject-001",
		"aud":     []string{"my-client", "secondary-aud"},
		"azp":     "my-client",
		"exp":     now.Add(5 * time.Minute).Unix(),
		"nbf":     now.Add(-1 * time.Minute).Unix(),
		"iat":     now.Unix(),
		"nonce":   "n-12345",
		"at_hash": atHashPS256,
	}

	ctx := context.Background()
	for _, kp := range []testOIDCKeyPair{rsaPSS, ecKey, edKey} {
		claimsCopy := make(map[string]any, len(validClaims))
		for k, v := range validClaims {
			claimsCopy[k] = v
		}
		atHash, _ := computeAtHash(kp.alg, accessToken)
		claimsCopy["at_hash"] = atHash

		raw := kp.signJWS(t, claimsCopy)
		insp, err := VerifyIDToken(ctx, raw, IDTokenVerifyParams{
			ExpectedIssuer: "https://auth.example.com",
			ClientID:       "my-client",
			ExpectedNonce:  "n-12345",
			AccessToken:    accessToken,
			StaticKeySet:   staticJWKS,
			Now:            func() time.Time { return now },
		})
		if err != nil || !insp.Verified {
			t.Fatalf("VerifyIDToken(%s) failed: %v", kp.alg, err)
		}
	}

	// 1. Reject alg: "none" unsigned token.
	noneHeader := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none","typ":"JWT"}`))
	nonePayload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://auth.example.com","sub":"s","aud":"my-client","exp":9999999999,"iat":1000}`))
	_, err = VerifyIDToken(ctx, noneHeader+"."+nonePayload+".", IDTokenVerifyParams{
		ExpectedIssuer: "https://auth.example.com",
		ClientID:       "my-client",
		StaticKeySet:   staticJWKS,
		Now:            func() time.Time { return now },
	})
	if !IsOAuthErrorCategory(err, OAuthErrIDTokenInvalidSignature) {
		t.Fatalf("expected alg:none to fail with OAuthErrIDTokenInvalidSignature, got: %v", err)
	}

	// 2. Reject forged payload (tampered sub).
	validRaw := ecKey.signJWS(t, validClaims)
	parts := strings.Split(validRaw, ".")
	tamperedPayload := base64.RawURLEncoding.EncodeToString([]byte(`{"iss":"https://auth.example.com","sub":"admin","aud":"my-client","exp":9999999999,"iat":1000}`))
	forgedToken := parts[0] + "." + tamperedPayload + "." + parts[2]
	_, err = VerifyIDToken(ctx, forgedToken, IDTokenVerifyParams{
		ExpectedIssuer: "https://auth.example.com",
		ClientID:       "my-client",
		StaticKeySet:   staticJWKS,
		Now:            func() time.Time { return now },
	})
	if !IsOAuthErrorCategory(err, OAuthErrIDTokenInvalidSignature) {
		t.Fatalf("expected forged token to fail with OAuthErrIDTokenInvalidSignature, got: %v", err)
	}

	// 3. Reject wrong issuer.
	_, err = VerifyIDToken(ctx, validRaw, IDTokenVerifyParams{
		ExpectedIssuer: "https://other-issuer.example.com",
		ClientID:       "my-client",
		ExpectedNonce:  "n-12345",
		StaticKeySet:   staticJWKS,
		Now:            func() time.Time { return now },
	})
	if !IsOAuthErrorCategory(err, OAuthErrIDTokenInvalidClaims) {
		t.Fatalf("expected wrong issuer to fail with OAuthErrIDTokenInvalidClaims, got: %v", err)
	}

	// 4. Reject wrong audience.
	_, err = VerifyIDToken(ctx, validRaw, IDTokenVerifyParams{
		ExpectedIssuer: "https://auth.example.com",
		ClientID:       "wrong-client-id",
		ExpectedNonce:  "n-12345",
		StaticKeySet:   staticJWKS,
		Now:            func() time.Time { return now },
	})
	if !IsOAuthErrorCategory(err, OAuthErrIDTokenInvalidClaims) {
		t.Fatalf("expected wrong audience to fail with OAuthErrIDTokenInvalidClaims, got: %v", err)
	}

	// 5. Reject wrong nonce.
	_, err = VerifyIDToken(ctx, validRaw, IDTokenVerifyParams{
		ExpectedIssuer: "https://auth.example.com",
		ClientID:       "my-client",
		ExpectedNonce:  "wrong-nonce",
		StaticKeySet:   staticJWKS,
		Now:            func() time.Time { return now },
	})
	if !IsOAuthErrorCategory(err, OAuthErrNonceMismatch) {
		t.Fatalf("expected wrong nonce to fail with OAuthErrNonceMismatch, got: %v", err)
	}

	// 6. Reject expired token (beyond clock skew).
	_, err = VerifyIDToken(ctx, validRaw, IDTokenVerifyParams{
		ExpectedIssuer: "https://auth.example.com",
		ClientID:       "my-client",
		ExpectedNonce:  "n-12345",
		StaticKeySet:   staticJWKS,
		Now:            func() time.Time { return now.Add(10 * time.Minute) },
	})
	if !IsOAuthErrorCategory(err, OAuthErrIDTokenExpired) {
		t.Fatalf("expected expired token to fail with OAuthErrIDTokenExpired, got: %v", err)
	}

	// 7. Unverified inspection must explicitly mark Verified=false and include a warning.
	unverified, err := InspectIDTokenUnverified(validRaw)
	if err != nil {
		t.Fatalf("InspectIDTokenUnverified failed: %v", err)
	}
	if unverified.Verified || unverified.Warning == "" {
		t.Fatalf("expected unverified inspection to have Verified=false and non-empty Warning, got: %+v", unverified)
	}
}
