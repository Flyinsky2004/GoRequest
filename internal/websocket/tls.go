package websocket

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gorequest/internal/core"
)

// maxCertFileBytes mirrors core.MaxCertFileBytes (4 MiB) for material loaded
// outside core's bounded readers.
const maxCertFileBytes int64 = 4 << 20

// validateTLSOptions rejects TLS configuration that could never be honored
// before any network byte moves: insecure-verify requests under an enforcing
// policy, private keys referenced through repository paths, or missing
// providers when references require them.
func validateTLSOptions(o *Options) error {
	policy := o.effectiveTargetPolicy()
	if o.TLS.InsecureSkipVerify && policy.EnforceTLSVerify {
		return configErrorf("TLS verification cannot be skipped: target policy enforces certificate verification on this device")
	}
	if strings.TrimSpace(o.TLS.ClientKeyRef) != "" {
		ref, err := core.ParseFileRef(strings.TrimSpace(o.TLS.ClientKeyRef))
		if err != nil {
			return configErrorf("mTLS client private key: %v", err)
		}
		// Private keys are machine-local ONLY (B04 boundary): repository paths
		// inside .gorequest/ are rejected at validation, not mid-dial.
		if ref.Kind != core.FileRefLocal {
			return configErrorf("mTLS client private key %q must use a machine-local slot (local:<slot>); private keys cannot live in shared project files", o.TLS.ClientKeyRef)
		}
		if o.LocalFiles == nil {
			return configErrorf("mTLS client private key %q requires a local file provider", o.TLS.ClientKeyRef)
		}
	}
	if strings.TrimSpace(o.TLS.CustomCARef) != "" || strings.TrimSpace(o.TLS.ClientCertRef) != "" {
		if o.ProjectRoot == "" && o.ProjectFiles == nil {
			return configErrorf("TLS references require a project root or project file opener")
		}
	}
	return nil
}

// buildTLSConfig materializes the *tls.Config for the dial. It replicates
// core.buildStdTLSConfig semantics with core's exported building blocks:
// system pool + custom CA (machine-local slot or .gorequest-confined file,
// bounded by maxCertFileBytes), mTLS pairs with machine-local private keys,
// SNI override and a TLS 1.2 floor. Private-key PEM registers into the
// Redactor so it can never leak into facts or error text.
func buildTLSConfig(ctx context.Context, o *Options, red *core.Redactor) (*tls.Config, error) {
	if err := validateTLSOptions(o); err != nil {
		return nil, err
	}

	minVer := o.TLS.MinVersion
	if minVer == 0 {
		minVer = tls.VersionTLS12
	}
	cfg := &tls.Config{
		MinVersion:         minVer,
		InsecureSkipVerify: o.TLS.InsecureSkipVerify,
		ServerName:         strings.TrimSpace(o.TLS.ServerName),
	}

	customCAPEM := append([]byte(nil), o.TLS.CustomCAPEM...)
	if ref := strings.TrimSpace(o.TLS.CustomCARef); ref != "" {
		data, err := readCertRef(ctx, o, ref, false, "custom CA")
		if err != nil {
			return nil, err
		}
		if len(customCAPEM) > 0 {
			customCAPEM = append(customCAPEM, '\n')
		}
		customCAPEM = append(customCAPEM, data...)
	}

	useSys := o.TLS.SystemCAEnabled()
	if !useSys || len(customCAPEM) > 0 {
		pool := x509.NewCertPool()
		if useSys {
			if sys, err := x509.SystemCertPool(); err == nil && sys != nil {
				pool = sys
			}
		}
		if len(customCAPEM) > 0 {
			if !hasCertificatePEMBlock(customCAPEM) || !pool.AppendCertsFromPEM(customCAPEM) {
				return nil, configErrorf("custom CA bundle contains no valid PEM certificates")
			}
		}
		cfg.RootCAs = pool
	}

	certRef := strings.TrimSpace(o.TLS.ClientCertRef)
	keyRef := strings.TrimSpace(o.TLS.ClientKeyRef)
	if certRef != "" || keyRef != "" || len(o.TLS.ClientCertPEM) > 0 || len(o.TLS.ClientKeyPEM) > 0 {
		certPEM := append([]byte(nil), o.TLS.ClientCertPEM...)
		keyPEM := append([]byte(nil), o.TLS.ClientKeyPEM...)
		if certRef != "" {
			data, err := readCertRef(ctx, o, certRef, false, "mTLS client certificate")
			if err != nil {
				return nil, err
			}
			certPEM = data
		}
		if keyRef != "" {
			data, err := readCertRef(ctx, o, keyRef, true, "mTLS client private key")
			if err != nil {
				return nil, err
			}
			keyPEM = data
		}
		if len(certPEM) == 0 || len(keyPEM) == 0 {
			return nil, configErrorf("mTLS requires both a client certificate and a client private key")
		}
		if red != nil {
			red.RegisterSecret(string(keyPEM))
		}
		pair, err := tls.X509KeyPair(certPEM, keyPEM)
		if err != nil {
			return nil, configErrorf("invalid mTLS client certificate or key pair: %v", err)
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	return cfg, nil
}

// readCertRef loads bounded PEM material from a "local:<slot>" binding or a
// .gorequest-confined project path, reusing core.ParseFileRef,
// core.LocalFileProvider and core.DirFileOpener for identical confinement
// rules (no path escapes, no symlinks out of .gorequest/, regular files only).
func readCertRef(ctx context.Context, o *Options, raw string, requireLocal bool, label string) ([]byte, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, configErrorf("%s reference is empty", label)
	}
	ref, err := core.ParseFileRef(trimmed)
	if err != nil {
		return nil, configErrorf("%s: %v", label, err)
	}
	if requireLocal && ref.Kind != core.FileRefLocal {
		return nil, configErrorf("%s %q must use a machine-local slot (local:<slot>)", label, raw)
	}
	switch ref.Kind {
	case core.FileRefLocal:
		if o.LocalFiles == nil {
			return nil, configErrorf("%s %q requires a local file provider", label, raw)
		}
		abs, ok := o.LocalFiles.ResolveLocalFile(ref.Slot)
		if !ok || strings.TrimSpace(abs) == "" {
			return nil, configErrorf("%s slot %q is not bound on this machine", label, ref.Slot)
		}
		data, err := readRegularFileBounded(abs, maxCertFileBytes)
		if err != nil {
			return nil, configErrorf("loading %s %q: %v", label, raw, err)
		}
		return data, nil
	case core.FileRefProject:
		opener := o.ProjectFiles
		if opener == nil {
			if strings.TrimSpace(o.ProjectRoot) == "" {
				return nil, configErrorf("%s %q requires a project root", label, raw)
			}
			opener = core.NewDirFileOpener(filepath.Join(o.ProjectRoot, ".gorequest"))
		}
		opened, err := opener.OpenProjectFile(ctx, ref.Path)
		if err != nil {
			return nil, configErrorf("loading %s %q: %v", label, raw, err)
		}
		defer opened.Reader.Close()
		data, err := readAllBounded(opened.Reader, opened.Size, maxCertFileBytes)
		if err != nil {
			return nil, configErrorf("loading %s %q: %v", label, raw, err)
		}
		return data, nil
	default:
		return nil, configErrorf("%s %q is not bound to any file", label, raw)
	}
}

// readRegularFileBounded reads a machine-local file, refusing non-regular
// files and oversized material.
func readRegularFileBounded(abs string, maxBytes int64) ([]byte, error) {
	f, err := os.Open(abs)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%q is not a regular file", abs)
	}
	return readAllBounded(f, info.Size(), maxBytes)
}

// readAllBounded copies the stream, failing once it exceeds maxBytes rather
// than truncating security material silently.
func readAllBounded(r io.Reader, declaredSize, maxBytes int64) ([]byte, error) {
	if declaredSize > 0 && declaredSize > maxBytes {
		return nil, fmt.Errorf("file is %d bytes; the limit is %d", declaredSize, maxBytes)
	}
	buf := make([]byte, 0, min64(min64(declaredSize, maxBytes), 1<<16))
	tmp := make([]byte, 32*1024)
	total := int64(0)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			total += int64(n)
			if total > maxBytes {
				return nil, fmt.Errorf("file exceeds the %d byte limit", maxBytes)
			}
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return nil, err
		}
	}
	return buf, nil
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}

// hasCertificatePEMBlock mirrors core's requirement that at least one
// CERTIFICATE block exists in the bundle.
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
