package platform

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gorequest/internal/core"
)

type memoryKeychainDriver struct {
	mu   sync.Mutex
	data map[string]string
	err  error
}

func newMemoryKeychainDriver() *memoryKeychainDriver {
	return &memoryKeychainDriver{data: map[string]string{}}
}

func (m *memoryKeychainDriver) Get(key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return "", m.err
	}
	v, ok := m.data[key]
	if !ok {
		return "", ErrSecretNotFound
	}
	return v, nil
}

func (m *memoryKeychainDriver) Set(key, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	m.data[key] = value
	return nil
}

func (m *memoryKeychainDriver) Delete(key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.err != nil {
		return m.err
	}
	if _, ok := m.data[key]; !ok {
		return ErrSecretNotFound
	}
	delete(m.data, key)
	return nil
}

func TestMyGoKeychainDriverImplementsInterface(t *testing.T) {
	var drv KeychainDriver = NewMyGoKeychainDriver("")
	mg, ok := drv.(MyGoKeychainDriver)
	if !ok || mg.Service != DefaultKeychainService {
		t.Fatalf("unexpected MyGoKeychainDriver: %#v", drv)
	}
}

func TestKeychainSecretsCloneIsolationAndLimits(t *testing.T) {
	drv := newMemoryKeychainDriver()
	cloneA := filepath.Join(t.TempDir(), "payments-repo")
	cloneB := filepath.Join(t.TempDir(), "payments-repo")
	if err := os.MkdirAll(filepath.Join(cloneA, ".gorequest"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(cloneB, ".gorequest"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Passing <root> vs <root>/.gorequest yields identical scope key.
	scopeRoot, err := ProjectScopeKey("payments-api", cloneA)
	if err != nil {
		t.Fatal(err)
	}
	scopeGQ, err := ProjectScopeKey("payments-api", filepath.Join(cloneA, ".gorequest"))
	if err != nil {
		t.Fatal(err)
	}
	if scopeRoot != scopeGQ {
		t.Fatalf("scope mismatch between project root (%q) and .gorequest dir (%q)", scopeRoot, scopeGQ)
	}

	kcA, err := NewKeychainSecrets(drv, "payments-api", cloneA)
	if err != nil {
		t.Fatal(err)
	}
	kcB, err := NewKeychainSecrets(drv, "payments-api", cloneB)
	if err != nil {
		t.Fatal(err)
	}
	if kcA.Scope() == kcB.Scope() {
		t.Fatalf("distinct clones must not share keychain scope: %q", kcA.Scope())
	}

	if err := kcA.Set("apiToken", "CLONE-A-SECRET-111"); err != nil {
		t.Fatalf("kcA.Set: %v", err)
	}
	if err := kcB.Set("apiToken", "CLONE-B-SECRET-222"); err != nil {
		t.Fatalf("kcB.Set: %v", err)
	}

	if v, ok := kcA.Get("apiToken"); !ok || v != "CLONE-A-SECRET-111" {
		t.Fatalf("kcA.Get = %q (%v)", v, ok)
	}
	if v, ok := kcB.Get("apiToken"); !ok || v != "CLONE-B-SECRET-222" {
		t.Fatalf("kcB.Get = %q (%v)", v, ok)
	}

	// InspectSecret never exposes the secret value.
	stA := kcA.InspectSecret("apiToken")
	if stA.State != core.SecretStateBound || stA.Backend != "keychain" || strings.Contains(stA.Detail, "CLONE-A-SECRET-111") {
		t.Fatalf("unexpected InspectSecret status: %+v", stA)
	}
	if stMissing := kcA.InspectSecret("doesNotExist"); stMissing.State != core.SecretStateMissing {
		t.Fatalf("expected missing status, got %+v", stMissing)
	}

	// Delete is idempotent for missing keys and removes existing secret.
	if err := kcA.Delete("apiToken"); err != nil {
		t.Fatalf("kcA.Delete: %v", err)
	}
	if err := kcA.Delete("apiToken"); err != nil {
		t.Fatalf("kcA.Delete idempotent: %v", err)
	}
	if _, ok := kcA.Get("apiToken"); ok {
		t.Fatal("deleted secret still returned by kcA.Get")
	}
	// Clone B's secret remains intact!
	if v, ok := kcB.Get("apiToken"); !ok || v != "CLONE-B-SECRET-222" {
		t.Fatalf("deleting clone A secret affected clone B: %q (%v)", v, ok)
	}

	// Enforce 256-byte key limit and 2560-byte value limit without leaking value.
	tooLongName := strings.Repeat("k", 260)
	if err := kcA.Set(tooLongName, "SECRET-VAL-333"); !core.IsConfig(err) || strings.Contains(err.Error(), "SECRET-VAL-333") {
		t.Fatalf("expected ConfigError without secret leak for oversized key, got %v", err)
	}
	oversizedSecret := strings.Repeat("S", MaxKeychainSecretBytes+1)
	if err := kcA.Set("huge", oversizedSecret); !core.IsConfig(err) || strings.Contains(err.Error(), oversizedSecret) {
		t.Fatalf("expected ConfigError without secret leak for oversized value, got %v", err)
	}
}

func TestZeroPlaintextFallbackWhenKeychainUnsupportedOrLocked(t *testing.T) {
	projectDir := t.TempDir()
	drv := newMemoryKeychainDriver()
	drv.err = fmt.Errorf("dbus session unavailable: %w", errors.ErrUnsupported)

	layered, err := NewProjectSecrets(drv, "headless-proj", projectDir, nil)
	if err != nil {
		t.Fatal(err)
	}

	// 1. Attempting to persist a secret when OS keychain is unsupported MUST fail
	// closed and NEVER write any file under projectDir.
	setErr := layered.Keychain.Set("apiToken", "NEVER-WRITE-TO-DISK-555")
	if !core.IsConfig(setErr) || !strings.Contains(setErr.Error(), "plaintext fallback is forbidden") {
		t.Fatalf("expected fail-closed ConfigError, got %v", setErr)
	}
	if strings.Contains(setErr.Error(), "NEVER-WRITE-TO-DISK-555") {
		t.Fatalf("secret value leaked into Set error: %v", setErr)
	}

	var filesWritten []string
	_ = filepath.WalkDir(projectDir, func(path string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			filesWritten = append(filesWritten, path)
		}
		return nil
	})
	if len(filesWritten) > 0 {
		t.Fatalf("keychain failure wrote fallback files to disk: %v", filesWritten)
	}

	// 2. Without env override, InspectSecret & core.Resolver surface unavailable keychain.
	st := layered.InspectSecret("apiToken")
	if st.State != core.SecretStateUnavailable || st.Backend != "keychain" {
		t.Fatalf("expected SecretStateUnavailable, got %+v", st)
	}
	r := core.NewResolver(nil, layered)
	if _, err := r.Resolve("{{secret.apiToken}}"); !core.IsConfig(err) || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("expected Resolver to report unavailable keychain, got %v", err)
	}

	// 3. Explicit process environment variable GOREQUEST_SECRET_APITOKEN satisfies
	// LayeredSecrets cleanly in CI/headless environments without touching disk.
	t.Setenv("GOREQUEST_SECRET_APITOKEN", "CI-ENV-SECRET-777")
	val, ok := layered.Get("apiToken")
	if !ok || val != "CI-ENV-SECRET-777" {
		t.Fatalf("expected EnvSecrets fallback in CI, got %q (%v)", val, ok)
	}
	stEnv := layered.InspectSecret("apiToken")
	if stEnv.State != core.SecretStateBound || stEnv.Backend != "env" {
		t.Fatalf("expected bound env status, got %+v", stEnv)
	}

	// 4. Explicit memory override takes highest precedence over Keychain and Env.
	drv.err = nil
	if err := layered.Keychain.Set("apiToken", "KEYCHAIN-SECRET-888"); err != nil {
		t.Fatal(err)
	}
	if v, ok := layered.Get("apiToken"); !ok || v != "KEYCHAIN-SECRET-888" {
		t.Fatalf("expected Keychain over Env, got %q (%v)", v, ok)
	}
	layered.Override = core.MapSecrets{"apiToken": "MEMORY-OVERRIDE-999"}
	if v, ok := layered.Get("apiToken"); !ok || v != "MEMORY-OVERRIDE-999" {
		t.Fatalf("expected Override over Keychain, got %q (%v)", v, ok)
	}
}
