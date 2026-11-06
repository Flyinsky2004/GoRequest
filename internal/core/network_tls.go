package core

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"strings"
)

// MaxCertFileBytes bounds the size of any CA bundle or client certificate/key
// file loaded into memory (4 MiB).
const MaxCertFileBytes int64 = 4 << 20

// TLSConfig configures server certificate verification (system CA, custom CA
// bundle) and mutual TLS (mTLS) client certificate authentication.
//
// Security invariants:
//   - TLS verification is NEVER skipped by default.
//   - Requesting InsecureSkipVerify when MergedTargetPolicy.EnforceTLSVerify is
//     true fails before network dial with a *ConfigError.
//   - mTLS private keys (ClientKeyRef) MUST use a machine-local mapping slot
//     ("local:<slot>"); referencing a shared .gorequest/ project file for a
//     private key is rejected with a *ConfigError.
type TLSConfig struct {
	// UseSystemCA controls whether the operating system's root CA pool is
	// trusted as the base pool. Nil defaults to true.
	UseSystemCA *bool `yaml:"useSystemCA,omitempty" json:"useSystemCA,omitempty"`

	// CustomCARef is an optional file reference ("local:<slot>" or a relative
	// path under .gorequest/) containing PEM-encoded root/intermediate CA
	// certificates to trust in addition to (or instead of) the system pool.
	CustomCARef string `yaml:"customCA,omitempty" json:"customCA,omitempty"`

	// CustomCAPEM holds optional in-memory PEM CA certificates (from device
	// store or tests).
	CustomCAPEM []byte `yaml:"-" json:"-"`

	// ClientCertRef references the mTLS client certificate PEM ("local:<slot>"
	// or .gorequest/ relative path).
	ClientCertRef string `yaml:"clientCert,omitempty" json:"clientCert,omitempty"`

	// ClientKeyRef references the mTLS private key PEM. It MUST be a machine-
	// local slot ("local:<slot>"); repository-relative paths are forbidden so
	// private keys never enter Git-tracked .gorequest/ files.
	ClientKeyRef string `yaml:"clientKey,omitempty" json:"clientKey,omitempty"`

	// ClientCertPEM and ClientKeyPEM hold optional in-memory mTLS credentials
	// loaded from machine-local storage.
	ClientCertPEM []byte `yaml:"-" json:"-"`
	ClientKeyPEM  []byte `yaml:"-" json:"-"`

	// ServerName optionally overrides the TLS SNI hostname.
	ServerName string `yaml:"serverName,omitempty" json:"serverName,omitempty"`

	// MinVersion sets the minimum acceptable TLS protocol version (defaults to
	// TLS 1.2 when 0).
	MinVersion uint16 `yaml:"-" json:"minVersion,omitempty"`

	// InsecureSkipVerify disables server certificate verification ONLY when
	// explicitly requested AND permitted by DeviceTargetPolicy.AllowInsecureTLS
	// (MergedTargetPolicy.EnforceTLSVerify == false).
	InsecureSkipVerify bool `yaml:"-" json:"insecureSkipVerify,omitempty"`
}

// SystemCAEnabled reports whether the OS root CA pool is enabled (nil defaults
// to true).
func (c TLSConfig) SystemCAEnabled() bool {
	return c.UseSystemCA == nil || *c.UseSystemCA
}

type builtTLSResult struct {
	config         *tls.Config
	customCAUsed   bool
	clientCertUsed bool
	insecureUsed   bool
}

func buildStdTLSConfig(
	ctx context.Context,
	cfg TLSConfig,
	policy MergedTargetPolicy,
	opts ExecuteOptions,
	red *Redactor,
) (*builtTLSResult, error) {
	if cfg.InsecureSkipVerify && policy.EnforceTLSVerify {
		return nil, configErrorf("TLS verification cannot be skipped: target policy enforces certificate verification on this device")
	}

	minVer := cfg.MinVersion
	if minVer == 0 {
		minVer = tls.VersionTLS12
	}

	stdCfg := &tls.Config{
		MinVersion:         minVer,
		InsecureSkipVerify: cfg.InsecureSkipVerify,
		ServerName:         strings.TrimSpace(cfg.ServerName),
	}

	var customCAPEM []byte
	if strings.TrimSpace(cfg.CustomCARef) != "" {
		ref, err := parseCertFileRef(cfg.CustomCARef, false, "custom CA")
		if err != nil {
			return nil, err
		}
		var used int64
		data, _, err := readFileRefBounded(ctx, ref, PlanOptions{
			ProjectRoot:  opts.ProjectRoot,
			ProjectFiles: opts.ProjectFiles,
			LocalFiles:   opts.LocalFiles,
		}, &used, MaxCertFileBytes)
		if err != nil {
			return nil, configErrorf("loading custom CA %q: %v", cfg.CustomCARef, err)
		}
		customCAPEM = append(customCAPEM, data...)
	}
	if len(cfg.CustomCAPEM) > 0 {
		if len(customCAPEM) > 0 {
			customCAPEM = append(customCAPEM, '\n')
		}
		customCAPEM = append(customCAPEM, cfg.CustomCAPEM...)
	}

	useSys := cfg.SystemCAEnabled()
	customCAUsed := false
	if !useSys || len(customCAPEM) > 0 {
		var pool *x509.CertPool
		if useSys {
			sysPool, err := x509.SystemCertPool()
			if err != nil || sysPool == nil {
				pool = x509.NewCertPool()
			} else {
				pool = sysPool
			}
		} else {
			pool = x509.NewCertPool()
		}
		if len(customCAPEM) > 0 {
			if !hasCertificatePEMBlock(customCAPEM) || !pool.AppendCertsFromPEM(customCAPEM) {
				return nil, configErrorf("custom CA bundle contains no valid PEM certificates")
			}
			customCAUsed = true
		}
		stdCfg.RootCAs = pool
	}

	hasCertRef := strings.TrimSpace(cfg.ClientCertRef) != ""
	hasKeyRef := strings.TrimSpace(cfg.ClientKeyRef) != ""
	hasCertPEM := len(cfg.ClientCertPEM) > 0
	hasKeyPEM := len(cfg.ClientKeyPEM) > 0

	clientCertUsed := false
	if hasCertRef || hasKeyRef || hasCertPEM || hasKeyPEM {
		var certPEM, keyPEM []byte
		if hasCertRef {
			ref, err := parseCertFileRef(cfg.ClientCertRef, false, "mTLS client certificate")
			if err != nil {
				return nil, err
			}
			var used int64
			data, _, err := readFileRefBounded(ctx, ref, PlanOptions{
				ProjectRoot:  opts.ProjectRoot,
				ProjectFiles: opts.ProjectFiles,
				LocalFiles:   opts.LocalFiles,
			}, &used, MaxCertFileBytes)
			if err != nil {
				return nil, configErrorf("loading mTLS client certificate %q: %v", cfg.ClientCertRef, err)
			}
			certPEM = data
		} else if hasCertPEM {
			certPEM = cfg.ClientCertPEM
		}

		if hasKeyRef {
			// Enforce that mTLS private keys ONLY come from local:<slot> bindings!
			ref, err := parseCertFileRef(cfg.ClientKeyRef, true, "mTLS client private key")
			if err != nil {
				return nil, err
			}
			var used int64
			data, _, err := readFileRefBounded(ctx, ref, PlanOptions{
				ProjectRoot:  opts.ProjectRoot,
				ProjectFiles: opts.ProjectFiles,
				LocalFiles:   opts.LocalFiles,
			}, &used, MaxCertFileBytes)
			if err != nil {
				return nil, configErrorf("loading mTLS client private key %q: %v", cfg.ClientKeyRef, err)
			}
			keyPEM = data
		} else if hasKeyPEM {
			keyPEM = cfg.ClientKeyPEM
		}

		if len(certPEM) == 0 || len(keyPEM) == 0 {
			return nil, configErrorf("mTLS requires both a client certificate and a client private key")
		}
		if red != nil && len(keyPEM) > 0 {
			red.RegisterSecret(string(keyPEM))
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, configErrorf("invalid mTLS client certificate or key pair: %v", err)
		}
		stdCfg.Certificates = []tls.Certificate{pair}
		clientCertUsed = true
	}

	return &builtTLSResult{
		config:         stdCfg,
		customCAUsed:   customCAUsed,
		clientCertUsed: clientCertUsed,
		insecureUsed:   cfg.InsecureSkipVerify,
	}, nil
}

// parseCertFileRef parses a certificate or private key file reference. When
// requireLocalSlot is true (used for mTLS private keys), project-relative paths
// are rejected so private keys can never be stored in .gorequest/ files.
func parseCertFileRef(raw string, requireLocalSlot bool, label string) (FileRef, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return FileRef{}, configErrorf("%s reference is empty", label)
	}
	if requireLocalSlot {
		slot, ok := strings.CutPrefix(trimmed, LocalFileRefPrefix)
		if !ok {
			if err := ValidateLocalSlot(trimmed); err == nil {
				slot = trimmed
			} else {
				return FileRef{}, configErrorf("%s %q must use a machine-local slot (%s<slot>); private keys cannot be stored in shared project files", label, raw, LocalFileRefPrefix)
			}
		}
		if err := ValidateLocalSlot(slot); err != nil {
			return FileRef{}, configErrorf("%s: %v", label, err)
		}
		return FileRef{Kind: FileRefLocal, Slot: slot}, nil
	}
	ref, err := ParseFileRef(trimmed)
	if err != nil {
		return FileRef{}, configErrorf("%s: %v", label, err)
	}
	return ref, nil
}

func hasCertificatePEMBlock(data []byte) bool {
	rest := data
	for {
		block, r := pem.Decode(rest)
		if block == nil {
			return false
		}
		if block.Type == "CERTIFICATE" && len(block.Bytes) > 0 {
			return true
		}
		rest = r
	}
}

func tlsVersionName(v uint16) string {
	switch v {
	case tls.VersionTLS13:
		return "TLS 1.3"
	case tls.VersionTLS12:
		return "TLS 1.2"
	case tls.VersionTLS11:
		return "TLS 1.1"
	case tls.VersionTLS10:
		return "TLS 1.0"
	default:
		return ""
	}
}

// classifyTransportError wraps low-level dial, proxy, TLS/x509, and stage
// timeout failures into clear, actionable *RunError diagnostics while keeping
// the underlying cause unwrappable.
func classifyTransportError(err error) error {
	if err == nil {
		return nil
	}
	var ce *ConfigError
	if errors.As(err, &ce) {
		return ce
	}
	var re *RunError
	if errors.As(err, &re) {
		return re
	}

	var uaErr x509.UnknownAuthorityError
	if errors.As(err, &uaErr) {
		return &RunError{Err: fmt.Errorf("TLS certificate verification failed (unknown or untrusted CA): %w", err)}
	}
	var hostErr x509.HostnameError
	if errors.As(err, &hostErr) {
		return &RunError{Err: fmt.Errorf("TLS certificate hostname mismatch for %q: %w", hostErr.Host, err)}
	}
	var certInvErr x509.CertificateInvalidError
	if errors.As(err, &certInvErr) {
		return &RunError{Err: fmt.Errorf("TLS certificate is invalid or expired: %w", err)}
	}
	var recHdrErr tls.RecordHeaderError
	if errors.As(err, &recHdrErr) {
		return &RunError{Err: fmt.Errorf("TLS handshake failed (server did not speak TLS): %w", err)}
	}

	msg := err.Error()
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "tls:") || strings.Contains(lower, "x509:") || strings.Contains(lower, "certificate") {
		if strings.Contains(lower, "handshake timeout") {
			return &RunError{Err: fmt.Errorf("TLS handshake timed out: %w", err)}
		}
		if strings.Contains(lower, "bad certificate") || strings.Contains(lower, "certificate required") || strings.Contains(lower, "unknown certificate authority") {
			return &RunError{Err: fmt.Errorf("mTLS handshake rejected by server (client certificate required or untrusted): %w", err)}
		}
		return &RunError{Err: fmt.Errorf("TLS error: %w", err)}
	}
	if strings.Contains(lower, "awaiting response headers") || strings.Contains(lower, "timeout awaiting response headers") {
		return &RunError{Err: fmt.Errorf("response header timeout (TTFB exceeded): %w", err)}
	}
	if strings.Contains(lower, "socks5") || strings.Contains(lower, "proxyconnect") || strings.Contains(lower, "proxy ") {
		return &RunError{Err: fmt.Errorf("proxy error: %w", err)}
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		var opErr *net.OpError
		if errors.As(err, &opErr) && opErr.Op == "dial" {
			return &RunError{Err: fmt.Errorf("connection dial timed out: %w", err)}
		}
		return &RunError{Err: fmt.Errorf("network stage timed out: %w", err)}
	}

	return &RunError{Err: fmt.Errorf("request failed: %w", err)}
}
