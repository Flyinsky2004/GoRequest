package platform

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorequest/internal/core"
)

func TestNetworkStorePersistenceAndProjectIsolation(t *testing.T) {
	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "network.json")
	store := NewNetworkStore(storePath)

	cfg := DefaultDeviceNetworkSettings()
	cfg.Timeouts.DialTimeout = 4 * time.Second
	cfg.Proxy = core.ProxyConfig{
		Mode:           core.ProxySOCKS5,
		URL:            "socks5://127.0.0.1:1080",
		Username:       "alice",
		Password:       "ephemeral-session-secret",
		PasswordSecret: "proxy_pw_slot",
	}
	cfg.TLS.CustomCARef = "local:corp_ca"
	cfg.TLS.ClientCertRef = "local:mtls_cert"
	cfg.TLS.ClientKeyRef = "local:mtls_key"

	if err := store.SaveDeviceSettings(cfg); err != nil {
		t.Fatal(err)
	}

	// Verify plaintext proxy password is NOT written to disk JSON.
	rawDisk, err := os.ReadFile(storePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rawDisk), "ephemeral-session-secret") {
		t.Fatalf("proxy password leaked to disk file: %s", rawDisk)
	}

	// Bind certificate slots per project scope and verify isolation.
	caFileA := filepath.Join(tmp, "ca-a.pem")
	caFileB := filepath.Join(tmp, "ca-b.pem")
	if err := store.BindCertSlot("proj-a:1111", "local:corp_ca", caFileA); err != nil {
		t.Fatal(err)
	}
	if err := store.BindCertSlot("proj-b:2222", "corp_ca", caFileB); err != nil {
		t.Fatal(err)
	}
	// Reject non-absolute path in BindCertSlot.
	if err := store.BindCertSlot("proj-a:1111", "corp_ca", "relative/path.pem"); err == nil {
		t.Fatal("expected relative path to be rejected by BindCertSlot")
	}

	// Reload from disk and verify bindings + device settings round-trip.
	reloaded := NewNetworkStore(storePath)
	gotCfg := reloaded.DeviceSettings()
	if gotCfg.Timeouts.DialTimeout != 4*time.Second || gotCfg.Proxy.Mode != core.ProxySOCKS5 {
		t.Fatalf("reloaded device settings mismatch: %+v", gotCfg)
	}

	provA := reloaded.LocalFileProviderFor("proj-a:1111", core.MapLocalFiles{"body_slot": filepath.Join(tmp, "body.bin")})
	provB := reloaded.LocalFileProviderFor("proj-b:2222", nil)

	if p, ok := provA.ResolveLocalFile("corp_ca"); !ok || p != caFileA {
		t.Fatalf("proj-a corp_ca = %q (ok=%v), want %q", p, ok, caFileA)
	}
	if p, ok := provB.ResolveLocalFile("corp_ca"); !ok || p != caFileB {
		t.Fatalf("proj-b corp_ca = %q (ok=%v), want %q", p, ok, caFileB)
	}
	if p, ok := provA.ResolveLocalFile("body_slot"); !ok || !strings.HasSuffix(p, "body.bin") {
		t.Fatalf("fallback LocalFileProvider failed: %q (ok=%v)", p, ok)
	}
}
