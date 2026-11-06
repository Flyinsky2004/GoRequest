package core

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/binary"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type certBundle struct {
	caCert     *x509.Certificate
	caKey      *ecdsa.PrivateKey
	caPEM      []byte
	serverCert tls.Certificate
	serverPEM  []byte
	serverKey  []byte
	clientPEM  []byte
	clientKey  []byte
}

func generateTestPKI(t *testing.T) *certBundle {
	t.Helper()
	now := time.Now().Add(-5 * time.Minute)

	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "GoRequest Test Root CA"},
		NotBefore:             now,
		NotAfter:              now.Add(2 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})

	// Server certificate valid for 127.0.0.1 and localhost
	srvKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	srvTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		NotBefore:    now,
		NotAfter:     now.Add(2 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	srvDER, err := x509.CreateCertificate(rand.Reader, srvTmpl, caCert, &srvKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	srvKeyDER, err := x509.MarshalECPrivateKey(srvKey)
	if err != nil {
		t.Fatal(err)
	}
	srvPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srvDER})
	srvKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: srvKeyDER})
	srvTLSCert, err := tls.X509KeyPair(srvPEM, srvKeyPEM)
	if err != nil {
		t.Fatal(err)
	}

	// Client certificate for mTLS
	cliKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cliTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(3),
		Subject:      pkix.Name{CommonName: "gorequest-mtls-client"},
		NotBefore:    now,
		NotAfter:     now.Add(2 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	cliDER, err := x509.CreateCertificate(rand.Reader, cliTmpl, caCert, &cliKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	cliKeyDER, err := x509.MarshalECPrivateKey(cliKey)
	if err != nil {
		t.Fatal(err)
	}
	cliPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cliDER})
	cliKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: cliKeyDER})

	return &certBundle{
		caCert:     caCert,
		caKey:      caKey,
		caPEM:      caPEM,
		serverCert: srvTLSCert,
		serverPEM:  srvPEM,
		serverKey:  srvKeyPEM,
		clientPEM:  cliPEM,
		clientKey:  cliKeyPEM,
	}
}

func TestExecuteTLSDefaultRejectsUntrustedAndCustomCASucceeds(t *testing.T) {
	pki := generateTestPKI(t)

	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tls":"trusted"}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	defer srv.Close()

	plan := &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: 5 * time.Second}

	// 1. Default Execute must reject the self-signed/custom-CA server certificate.
	_, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{})
	if err == nil {
		t.Fatal("expected default TLS verification to reject untrusted server cert")
	}
	if !strings.Contains(strings.ToLower(err.Error()), "tls") && !strings.Contains(strings.ToLower(err.Error()), "certificate") {
		t.Fatalf("expected TLS certificate error, got: %v", err)
	}

	// 2. Skipping TLS verification must be blocked when EnforceTLSVerify is true (default).
	_, err = ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		TLS: TLSConfig{InsecureSkipVerify: true},
	})
	if err == nil || !IsConfig(err) {
		t.Fatalf("expected ConfigError when InsecureSkipVerify is blocked by target policy, got: %v", err)
	}

	// 3. Corrupt custom CA PEM must fail pre-flight with a ConfigError.
	_, err = ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		TLS: TLSConfig{CustomCAPEM: []byte("not-a-valid-pem")},
	})
	if err == nil || !IsConfig(err) {
		t.Fatalf("expected ConfigError on invalid custom CA PEM, got: %v", err)
	}

	// 4. Supplying the valid custom CA via local file slot succeeds and records trace metadata.
	tmpDir := t.TempDir()
	caFile := filepath.Join(tmpDir, "ca.pem")
	if err := os.WriteFile(caFile, pki.caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	fFalse := false
	res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		TLS: TLSConfig{
			UseSystemCA: &fFalse,
			CustomCARef: "local:corp_ca",
		},
		LocalFiles: MapLocalFiles{"corp_ca": caFile},
	})
	if err != nil {
		t.Fatalf("expected custom CA request to succeed, got: %v", err)
	}
	if res.Status != 200 || !strings.Contains(res.Pretty, `"trusted"`) {
		t.Fatalf("unexpected response: status=%d body=%q", res.Status, res.Body)
	}
	if res.Trace == nil || !res.Trace.CustomCAUsed || !res.Trace.TLSObserved || res.Trace.TLSVersion == "" {
		t.Fatalf("expected TLS trace with CustomCAUsed=true, got: %+v", res.Trace)
	}
}

func TestExecuteMTLSClientCertAndLocalPrivateKeyBoundary(t *testing.T) {
	pki := generateTestPKI(t)
	clientCAPool := x509.NewCertPool()
	clientCAPool.AddCert(pki.caCert)

	var gotClientCN string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.TLS != nil && len(r.TLS.PeerCertificates) > 0 {
			gotClientCN = r.TLS.PeerCertificates[0].Subject.CommonName
		}
		_, _ = w.Write([]byte(`{"mtls":"ok"}`))
	}))
	srv.TLS = &tls.Config{
		Certificates: []tls.Certificate{pki.serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCAPool,
		MinVersion:   tls.VersionTLS12,
	}
	srv.StartTLS()
	defer srv.Close()

	tmpDir := t.TempDir()
	caPath := filepath.Join(tmpDir, "ca.pem")
	certPath := filepath.Join(tmpDir, "client.crt")
	keyPath := filepath.Join(tmpDir, "client.key")
	if err := os.WriteFile(caPath, pki.caPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(certPath, pki.clientPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyPath, pki.clientKey, 0o600); err != nil {
		t.Fatal(err)
	}

	plan := &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: 5 * time.Second}

	// 1. Referencing a shared project path for the private key MUST be rejected.
	_, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		TLS: TLSConfig{
			CustomCARef:   "local:ca",
			ClientCertRef: "local:cert",
			ClientKeyRef:  "certs/client.key", // forbidden project-relative private key!
		},
		LocalFiles: MapLocalFiles{"ca": caPath, "cert": certPath},
	})
	if err == nil || !IsConfig(err) || !strings.Contains(err.Error(), "machine-local slot") {
		t.Fatalf("expected ConfigError rejecting project-relative private key, got: %v", err)
	}

	// 2. Connecting without client cert to mTLS server fails handshake.
	_, err = ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		TLS:        TLSConfig{CustomCARef: "local:ca"},
		LocalFiles: MapLocalFiles{"ca": caPath},
	})
	if err == nil || !IsRun(err) {
		t.Fatalf("expected mTLS handshake failure without client cert, got: %v", err)
	}

	// 3. Connecting with local client cert + local private key slot succeeds.
	res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		TLS: TLSConfig{
			CustomCARef:   "local:ca",
			ClientCertRef: "local:cert",
			ClientKeyRef:  "local:key",
		},
		LocalFiles: MapLocalFiles{
			"ca":   caPath,
			"cert": certPath,
			"key":  keyPath,
		},
	})
	if err != nil {
		t.Fatalf("mTLS request failed: %v", err)
	}
	if res.Status != 200 || gotClientCN != "gorequest-mtls-client" {
		t.Fatalf("mTLS status=%d clientCN=%q", res.Status, gotClientCN)
	}
	if res.Trace == nil || !res.Trace.ClientCertUsed {
		t.Fatalf("expected ClientCertUsed=true in trace, got %+v", res.Trace)
	}
}

func TestExecuteStageTimeoutsAndCancellation(t *testing.T) {
	// 1. ResponseHeaderTimeout (TTFB timeout) when server accepts TCP but delays headers.
	releaseSrv := make(chan struct{})
	slowHeaderSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-releaseSrv:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(releaseSrv)
		slowHeaderSrv.Close()
	}()

	plan := &Plan{Method: http.MethodGet, URL: slowHeaderSrv.URL, Timeout: 5 * time.Second}
	_, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Timeouts: StageTimeouts{
			ResponseHeaderTimeout: 35 * time.Millisecond,
		},
	})
	if err == nil || !IsRun(err) || !strings.Contains(strings.ToLower(err.Error()), "response header timeout") {
		t.Fatalf("expected response header timeout error, got: %v", err)
	}

	// 2. Mid-download cancellation releases connection promptly.
	streamStarted := make(chan struct{})
	streamBlock := make(chan struct{})
	streamSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("chunk-1\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		close(streamStarted)
		select {
		case <-streamBlock:
		case <-r.Context().Done():
		}
	}))
	defer func() {
		close(streamBlock)
		streamSrv.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-streamStarted
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, err = ExecuteWithOptions(ctx, &Plan{
		Method:  http.MethodGet,
		URL:     streamSrv.URL,
		Timeout: 10 * time.Second,
	}, ExecuteOptions{})
	elapsed := time.Since(start)
	if err == nil || !strings.Contains(err.Error(), "canceled") {
		t.Fatalf("expected request canceled error, got: %v", err)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("cancellation took too long (%v)", elapsed)
	}
}

func TestExecuteRedirectsTargetPolicyAndCrossOriginStripping(t *testing.T) {
	var destAuth, destCookie, destAPIKey, destCustomSafe string
	var destHits atomic.Int32

	destSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		destHits.Add(1)
		destAuth = r.Header.Get("Authorization")
		destCookie = r.Header.Get("Cookie")
		destAPIKey = r.Header.Get("X-Api-Key")
		destCustomSafe = r.Header.Get("X-Safe-Trace")
		w.Header().Set("Set-Cookie", "dest_ck=from_dest; Path=/")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"reached":"dest"}`))
	}))
	defer destSrv.Close()

	hop1Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Set-Cookie", "hop1_ck=secret_hop1; Path=/")
		http.Redirect(w, r, destSrv.URL+"/final", http.StatusFound)
	}))
	defer hop1Srv.Close()

	jar := NewCookieJar("proj-redirect")
	_ = jar.Upsert(CookieEntry{Name: "init_ck", Value: "init_val", Domain: "127.0.0.1", HostOnly: true, Path: "/"})
	red := NewRedactor()
	red.RegisterSensitiveKey("header", "X-Api-Key")
	red.RegisterSecret("super-api-key-999")

	authScheme := &AuthScheme{
		Type:  "apikey",
		In:    "header",
		Name:  "X-Api-Key",
		Value: "super-api-key-999",
	}

	hop1URL := strings.Replace(hop1Srv.URL, "127.0.0.1", "localhost", 1)
	plan := &Plan{
		Method: http.MethodGet,
		URL:    hop1URL + "/start",
		Headers: [][2]string{
			{"Authorization", "Bearer top-secret-jwt"},
			{"Cookie", "manual_ck=secret_manual"},
			{"X-Api-Key", "super-api-key-999"},
			{"X-Safe-Trace", "keep-me"},
		},
		Timeout: 5 * time.Second,
	}

	// 1. RedirectNone stops at the first 302 response without hitting destSrv.
	resNone, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Redirect:  RedirectPolicy{Mode: RedirectNone},
		CookieJar: jar,
	})
	if err != nil {
		t.Fatalf("RedirectNone failed: %v", err)
	}
	if resNone.Status != http.StatusFound || destHits.Load() != 0 {
		t.Fatalf("RedirectNone status=%d destHits=%d", resNone.Status, destHits.Load())
	}

	// 2. RedirectSameOriginOnly blocks cross-port redirect between hop1Srv and destSrv.
	_, err = ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Redirect: RedirectPolicy{Mode: RedirectSameOriginOnly},
	})
	if err == nil || !IsConfig(err) || destHits.Load() != 0 {
		t.Fatalf("expected RedirectSameOriginOnly to block cross-port redirect, err=%v destHits=%d", err, destHits.Load())
	}

	// 3. RedirectFollow follows hop1 -> destSrv, strips Authorization, Cookie (from hop1),
	// and custom X-Api-Key across origins while keeping X-Safe-Trace!
	// Use a fresh CookieJar scoped only to hop1's port/host or empty jar to verify no leak.
	emptyJar := NewCookieJar("proj-redirect-clean")
	resFollow, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Redirect:   RedirectPolicy{Mode: RedirectFollow, MaxHops: 5},
		CookieJar:  emptyJar,
		AuthScheme: authScheme,
		Redactor:   red,
	})
	if err != nil {
		t.Fatalf("RedirectFollow failed: %v", err)
	}
	if resFollow.Status != http.StatusOK || len(resFollow.Redirects) != 1 {
		t.Fatalf("status=%d redirects=%+v", resFollow.Status, resFollow.Redirects)
	}
	if !resFollow.Redirects[0].OriginChanged || !resFollow.Redirects[0].StrippedCredentials {
		t.Fatalf("expected OriginChanged & StrippedCredentials on redirect hop: %+v", resFollow.Redirects[0])
	}
	if destAuth != "" || destAPIKey != "" || destCookie != "" {
		t.Fatalf("cross-origin redirect leaked credentials: Authorization=%q X-Api-Key=%q Cookie=%q", destAuth, destAPIKey, destCookie)
	}
	if destCustomSafe != "keep-me" {
		t.Fatalf("expected non-sensitive header X-Safe-Trace to be preserved, got %q", destCustomSafe)
	}

	// 4. TargetPolicy blocks redirect hop when redirect destination host is denied.
	destURL, _ := url.Parse(destSrv.URL)
	deniedPolicy := MergeTargetPolicy(&DeviceTargetPolicy{
		DeniedHosts: []string{destURL.Hostname()},
	}, nil)
	// Even if hop 0 is evaluated with a policy that only denies a specific redirect host:
	hop0Srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://denied.internal.example.com/secret", http.StatusFound)
	}))
	defer hop0Srv.Close()

	_, err = ExecuteWithOptions(context.Background(), &Plan{
		Method:  http.MethodGet,
		URL:     hop0Srv.URL,
		Timeout: 5 * time.Second,
	}, ExecuteOptions{
		TargetPolicy: MergeTargetPolicy(&DeviceTargetPolicy{
			DeniedHosts: []string{"*.internal.example.com"},
		}, nil),
		HasTargetPolicy: true,
	})
	if err == nil || !IsConfig(err) || !strings.Contains(err.Error(), "blocked by target policy") {
		t.Fatalf("expected redirect to denied host to be blocked by target policy, got: %v (policy=%+v)", err, deniedPolicy)
	}
}

func TestExecuteHTTPProxyAndNoProxyBypass(t *testing.T) {
	var targetHits atomic.Int32
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"via":"target"}`))
	}))
	defer targetSrv.Close()

	var proxyHits atomic.Int32
	var gotProxyAuth string
	proxySrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		gotProxyAuth = r.Header.Get("Proxy-Authorization")
		resp, err := http.DefaultTransport.RoundTrip(r)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		for k, vals := range resp.Header {
			for _, v := range vals {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
	}))
	defer proxySrv.Close()

	plan := &Plan{Method: http.MethodGet, URL: targetSrv.URL + "/hello", Timeout: 5 * time.Second}
	red := NewRedactor()

	// 1. Route through HTTP proxy with local proxy credentials via SecretProvider.
	res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Proxy: ProxyConfig{
			Mode:           ProxyHTTP,
			URL:            proxySrv.URL,
			Username:       "proxyuser",
			PasswordSecret: "proxy_pass",
		},
		Secrets:  MapSecrets{"proxy_pass": "proxypass-secret-777"},
		Redactor: red,
	})
	if err != nil {
		t.Fatalf("HTTP proxy request failed: %v", err)
	}
	if res.Status != 200 || proxyHits.Load() != 1 || targetHits.Load() != 1 {
		t.Fatalf("status=%d proxyHits=%d targetHits=%d", res.Status, proxyHits.Load(), targetHits.Load())
	}
	if gotProxyAuth == "" {
		t.Fatal("expected Proxy-Authorization header to be sent to HTTP proxy")
	}
	if res.Trace == nil || res.Trace.ProxyUsed == "" || strings.Contains(res.Trace.ProxyUsed, "proxypass-secret-777") {
		t.Fatalf("unexpected Trace.ProxyUsed: %+v", res.Trace)
	}

	// 2. NoProxy bypasses the HTTP proxy and connects directly.
	resBypass, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Proxy: ProxyConfig{
			Mode:    ProxyHTTP,
			URL:     proxySrv.URL,
			NoProxy: "127.0.0.1, localhost",
		},
	})
	if err != nil {
		t.Fatalf("NoProxy request failed: %v", err)
	}
	if resBypass.Status != 200 || proxyHits.Load() != 1 || targetHits.Load() != 2 {
		t.Fatalf("expected NoProxy to bypass proxy: proxyHits=%d targetHits=%d", proxyHits.Load(), targetHits.Load())
	}
}

// startMockSOCKS5Server starts a local RFC 1928 + RFC 1929 SOCKS5 server for testing.
func startMockSOCKS5Server(t *testing.T, wantUser, wantPass string) (addr string, hits *atomic.Int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var counter atomic.Int32

	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			counter.Add(1)
			go handleSOCKS5Conn(conn, wantUser, wantPass)
		}
	}()
	return ln.Addr().String(), &counter
}

func handleSOCKS5Conn(conn net.Conn, wantUser, wantPass string) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))

	var hdr [2]byte
	if _, err := io.ReadFull(conn, hdr[:]); err != nil || hdr[0] != 0x05 {
		return
	}
	methods := make([]byte, int(hdr[1]))
	if _, err := io.ReadFull(conn, methods); err != nil {
		return
	}

	if wantUser != "" || wantPass != "" {
		_, _ = conn.Write([]byte{0x05, 0x02})
		var authHdr [2]byte
		if _, err := io.ReadFull(conn, authHdr[:]); err != nil || authHdr[0] != 0x01 {
			return
		}
		uBytes := make([]byte, int(authHdr[1]))
		if _, err := io.ReadFull(conn, uBytes); err != nil {
			return
		}
		var pLen [1]byte
		if _, err := io.ReadFull(conn, pLen[:]); err != nil {
			return
		}
		pBytes := make([]byte, int(pLen[0]))
		if _, err := io.ReadFull(conn, pBytes); err != nil {
			return
		}
		if string(uBytes) != wantUser || string(pBytes) != wantPass {
			_, _ = conn.Write([]byte{0x01, 0x01}) // auth failed
			return
		}
		_, _ = conn.Write([]byte{0x01, 0x00})
	} else {
		_, _ = conn.Write([]byte{0x05, 0x00})
	}

	var reqHdr [4]byte
	if _, err := io.ReadFull(conn, reqHdr[:]); err != nil || reqHdr[1] != 0x01 {
		return
	}
	var host string
	switch reqHdr[3] {
	case 0x01:
		var ip [4]byte
		if _, err := io.ReadFull(conn, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	case 0x03:
		var dLen [1]byte
		if _, err := io.ReadFull(conn, dLen[:]); err != nil {
			return
		}
		dom := make([]byte, int(dLen[0]))
		if _, err := io.ReadFull(conn, dom); err != nil {
			return
		}
		host = string(dom)
	case 0x04:
		var ip [16]byte
		if _, err := io.ReadFull(conn, ip[:]); err != nil {
			return
		}
		host = net.IP(ip[:]).String()
	default:
		return
	}
	var portBytes [2]byte
	if _, err := io.ReadFull(conn, portBytes[:]); err != nil {
		return
	}
	port := binary.BigEndian.Uint16(portBytes[:])
	targetConn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 3*time.Second)
	if err != nil {
		_, _ = conn.Write([]byte{0x05, 0x05, 0x00, 0x01, 0, 0, 0, 0, 0, 0})
		return
	}
	defer targetConn.Close()
	_, _ = conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 127, 0, 0, 1, 0, 0})
	_ = conn.SetDeadline(time.Time{})

	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(targetConn, conn); done <- struct{}{} }()
	go func() { _, _ = io.Copy(conn, targetConn); done <- struct{}{} }()
	<-done
}

func TestExecuteSOCKS5ProxyAuthAndFailureDiagnostics(t *testing.T) {
	targetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"socks5":"connected"}`))
	}))
	defer targetSrv.Close()

	socksAddr, socksHits := startMockSOCKS5Server(t, "socks-admin", "socks-secret-pw")
	plan := &Plan{Method: http.MethodGet, URL: targetSrv.URL, Timeout: 5 * time.Second}

	// 1. Wrong password fails with SOCKS5 authentication error and scrubs the password.
	red := NewRedactor()
	_, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Proxy: ProxyConfig{
			Mode:     ProxySOCKS5,
			URL:      "socks5://" + socksAddr,
			Username: "socks-admin",
			Password: "wrong-secret-password",
		},
		Redactor: red,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "socks5") {
		t.Fatalf("expected SOCKS5 auth error, got: %v", err)
	}
	if strings.Contains(err.Error(), "wrong-secret-password") {
		t.Fatalf("SOCKS5 error leaked password: %v", err)
	}

	// 2. Correct SOCKS5 credentials succeed and tunnel the HTTP request.
	res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
		Proxy: ProxyConfig{
			Mode:     ProxySOCKS5,
			URL:      "socks5h://" + socksAddr,
			Username: "socks-admin",
			Password: "socks-secret-pw",
		},
		Redactor: red,
	})
	if err != nil {
		t.Fatalf("SOCKS5 request failed: %v", err)
	}
	if res.Status != 200 || !strings.Contains(res.Pretty, `"connected"`) || socksHits.Load() < 2 {
		t.Fatalf("unexpected SOCKS5 result: status=%d body=%q hits=%d", res.Status, res.Body, socksHits.Load())
	}
}

func TestExecuteTransferSizeVsDecompressedBodyAndDownloadCap(t *testing.T) {
	largeJSON := `{"message":"` + strings.Repeat("compressible-payload-", 400) + `"}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/gzip":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			var buf bytes.Buffer
			gw := gzip.NewWriter(&buf)
			_, _ = gw.Write([]byte(largeJSON))
			_ = gw.Close()
			_, _ = w.Write(buf.Bytes())

		case "/deflate-zlib":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "deflate")
			var buf bytes.Buffer
			zw := zlib.NewWriter(&buf)
			_, _ = zw.Write([]byte(largeJSON))
			_ = zw.Close()
			_, _ = w.Write(buf.Bytes())

		case "/deflate-raw":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "deflate")
			var buf bytes.Buffer
			fw, _ := flate.NewWriter(&buf, flate.DefaultCompression)
			_, _ = fw.Write([]byte(largeJSON))
			_ = fw.Close()
			_, _ = w.Write(buf.Bytes())

		case "/corrupt-gzip":
			w.Header().Set("Content-Type", "application/json")
			w.Header().Set("Content-Encoding", "gzip")
			_, _ = w.Write([]byte("not-a-valid-gzip-stream"))

		case "/binary":
			w.Header().Set("Content-Type", "image/png")
			w.Header().Set("Content-Disposition", `attachment; filename="../avatar.png"`)
			_, _ = w.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00, 0x01, 0x02, 0xff})
		}
	}))
	defer srv.Close()

	// 1. Gzip response distinguishes TransferSize (<500 B) from BodySize (>8000 B).
	resGzip, err := ExecuteWithOptions(context.Background(), &Plan{
		Method:  http.MethodGet,
		URL:     srv.URL + "/gzip",
		Timeout: 5 * time.Second,
	}, ExecuteOptions{})
	if err != nil {
		t.Fatalf("gzip request failed: %v", err)
	}
	if !resGzip.Decompressed || resGzip.ContentEncoding != "gzip" {
		t.Fatalf("expected Decompressed=true and ContentEncoding=gzip, got %+v", resGzip)
	}
	if resGzip.BodySize != int64(len(largeJSON)) {
		t.Fatalf("BodySize = %d, want %d", resGzip.BodySize, len(largeJSON))
	}
	if resGzip.TransferSize <= 0 || resGzip.TransferSize >= resGzip.BodySize/2 {
		t.Fatalf("expected TransferSize (%d) << BodySize (%d)", resGzip.TransferSize, resGzip.BodySize)
	}
	if resGzip.Pretty == "" {
		t.Fatal("expected decompressed JSON to be pretty-formatted")
	}

	// 2. Deflate (zlib and raw flate) both decompress accurately.
	for _, path := range []string{"/deflate-zlib", "/deflate-raw"} {
		resDef, err := ExecuteWithOptions(context.Background(), &Plan{
			Method:  http.MethodGet,
			URL:     srv.URL + path,
			Timeout: 5 * time.Second,
		}, ExecuteOptions{})
		if err != nil {
			t.Fatalf("%s failed: %v", path, err)
		}
		if resDef.BodySize != int64(len(largeJSON)) || resDef.TransferSize >= resDef.BodySize {
			t.Fatalf("%s sizes: TransferSize=%d BodySize=%d", path, resDef.TransferSize, resDef.BodySize)
		}
	}

	// 3. Corrupt gzip returns a RunError.
	_, err = ExecuteWithOptions(context.Background(), &Plan{
		Method:  http.MethodGet,
		URL:     srv.URL + "/corrupt-gzip",
		Timeout: 5 * time.Second,
	}, ExecuteOptions{})
	if err == nil || !IsRun(err) || !strings.Contains(err.Error(), "decompressing") {
		t.Fatalf("expected decompression error on corrupt gzip, got: %v", err)
	}

	// 4. MaxResponseBytes caps decompressed output and sets Truncated=true.
	resCap, err := ExecuteWithOptions(context.Background(), &Plan{
		Method:  http.MethodGet,
		URL:     srv.URL + "/gzip",
		Timeout: 5 * time.Second,
	}, ExecuteOptions{
		MaxResponseBytes: 512,
	})
	if err != nil {
		t.Fatalf("capped gzip failed: %v", err)
	}
	if !resCap.Truncated || resCap.BodySize != 512 || int64(len(resCap.Body)) != 512 {
		t.Fatalf("expected truncated 512-byte body, got Truncated=%v BodySize=%d", resCap.Truncated, resCap.BodySize)
	}

	// 5. Binary payload detection and path-traversal-safe SuggestedFilename.
	resBin, err := ExecuteWithOptions(context.Background(), &Plan{
		Method:  http.MethodGet,
		URL:     srv.URL + "/binary",
		Timeout: 5 * time.Second,
	}, ExecuteOptions{})
	if err != nil {
		t.Fatalf("binary request failed: %v", err)
	}
	if !resBin.IsBinary || resBin.ContentType != "image/png" || resBin.SuggestedFilename != "avatar.png" {
		t.Fatalf("unexpected binary metadata: IsBinary=%v ContentType=%q SuggestedFilename=%q",
			resBin.IsBinary, resBin.ContentType, resBin.SuggestedFilename)
	}
}

func TestParseAndFlattenJSONTreeOrderAndSearchCap(t *testing.T) {
	raw := []byte(`{"z_last":1,"a_first":{"nested":[true,null,"needle-1","needle-2"]}}`)
	root, trunc, err := ParseJSONTree(raw, 100)
	if err != nil || trunc {
		t.Fatalf("ParseJSONTree failed: trunc=%v err=%v", trunc, err)
	}
	if len(root.Children) != 2 || root.Children[0].Key != "z_last" || root.Children[1].Key != "a_first" {
		t.Fatalf("JSON tree did not preserve object key order: %+v", root.Children)
	}

	rows, matches, capped := FlattenJSONTree(root, nil, "needle", 1)
	if matches != 1 || !capped {
		t.Fatalf("expected matches=1 and capped=true with maxMatches=1, got matches=%d capped=%v", matches, capped)
	}
	if len(rows) == 0 {
		t.Fatal("expected ancestor and matched rows in flattened tree")
	}

	// Collapse $.a_first and verify its children are hidden when query is empty.
	collapsed := map[string]bool{"$.a_first": true}
	collapsedRows, _, _ := FlattenJSONTree(root, collapsed, "", 100)
	for _, r := range collapsedRows {
		if strings.HasPrefix(r.Node.Path, "$.a_first.") || strings.HasPrefix(r.Node.Path, "$.a_first[") {
			t.Fatalf("collapsed child node %q should not be visible", r.Node.Path)
		}
	}

	// Verify errors.As still works on ConfigError from target policy.
	var ce *ConfigError
	_, polErr := EvaluateTarget(MergeTargetPolicy(&DeviceTargetPolicy{DeniedHosts: []string{"evil.example"}}, nil), TargetCheckInput{
		URL:    "https://evil.example/api",
		Method: "GET",
	})
	if !errors.As(polErr, &ce) {
		t.Fatalf("expected ConfigError, got %v", polErr)
	}
}
