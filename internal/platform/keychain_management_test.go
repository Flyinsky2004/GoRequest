package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorequest/internal/core"
)

func TestManagedCredentialsLifecycleWithMemoryBackend(t *testing.T) {
	root := t.TempDir()
	drv := newMemoryKeychainDriver()
	mc, err := NewManagedCredentials(drv, "identity", root)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := ProjectScopeKey("identity", root)
	if err != nil {
		t.Fatal(err)
	}

	declinedSecret := "{{SECRET_nf4u7bgt}}"
	if _, err := mc.Set("apiToken", ""); err == nil {
		t.Fatal("empty value must be rejected")
	}
	if _, err := mc.Set("../escape", "v"); err == nil {
		t.Fatal("path-like secret name must be rejected")
	}
	if _, err := mc.Set("secret.x", "v"); err == nil {
		t.Fatal("reserved namespace must be rejected")
	}
	change, err := mc.Set("apiToken", declinedSecret)
	if err != nil {
		t.Fatalf("Set: %v", err)
	}
	if change.Action != "created" || change.Name != "apiToken" {
		t.Fatalf("unexpected create change: %+v", change)
	}
	records, err := mc.List([]string{"apiToken", "other"})
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]CredentialRecord{}
	for _, r := range records {
		byName[r.Name] = r
	}
	if byName["apiToken"].State != core.SecretStateBound || byName["other"].State != core.SecretStateMissing {
		t.Fatalf("unexpected statuses: %+v", records)
	}
	for _, r := range records {
		if strings.Contains(r.Detail, declinedSecret) {
			t.Fatalf("status leaked secret value: %+v", r)
		}
	}
	// Only the scoped key is used; no plaintext file is written.
	key, err := ScopedSecretKey(scope, "apiToken")
	if err != nil {
		t.Fatal(err)
	}
	drv.mu.Lock()
	got, ok := drv.data[key]
	drv.mu.Unlock()
	if !ok || got != declinedSecret {
		t.Fatalf("credential not in scoped backend: key=%q ok=%v", key, ok)
	}
	var plaintext []string
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), declinedSecret) {
			plaintext = append(plaintext, path)
		}
		return nil
	})
	if len(plaintext) != 0 {
		t.Fatalf("plaintext secret written to project files: %v", plaintext)
	}

	change, err = mc.Set("apiToken", "ROTATED-VALUE-2")
	if err != nil || change.Action != "updated" {
		t.Fatalf("update: %+v, %v", change, err)
	}
	change, err = mc.Delete("apiToken")
	if err != nil || change.Action != "deleted" {
		t.Fatalf("delete: %+v, %v", change, err)
	}
	records, _ = mc.List(nil)
	for _, r := range records {
		if r.Name == "apiToken" {
			t.Fatalf("deleted secret still listed: %+v", r)
		}
	}
	if _, err := mc.Delete("apiToken"); err != nil {
		t.Fatalf("deleting missing secret should be idempotent: %v", err)
	}
	// A second clone has a distinct scope and cannot see or affect this one.
	otherRoot := t.TempDir()
	mc2, err := NewManagedCredentials(drv, "identity", otherRoot)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc2.Set("apiToken", "OTHER-CLONE"); err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "FIRST-CLONE"); err != nil {
		t.Fatal(err)
	}
	key1, _ := ScopedSecretKey(scope, "apiToken")
	scope2, _ := ProjectScopeKey("identity", otherRoot)
	key2, _ := ScopedSecretKey(scope2, "apiToken")
	drv.mu.Lock()
	first, second := drv.data[key1], drv.data[key2]
	drv.mu.Unlock()
	if first != "FIRST-CLONE" || second != "OTHER-CLONE" {
		t.Fatalf("clone isolation broken: first=%q second=%q", first, second)
	}
}

func TestManagedCredentialsBackendUnavailableFailsClosed(t *testing.T) {
	root := t.TempDir()
	drv := newMemoryKeychainDriver()
	drv.err = errors.ErrUnsupported
	mc, err := NewManagedCredentials(drv, "identity", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("token", "value"); err == nil {
		t.Fatal("unavailable backend Set must fail")
	}
	records, err := mc.List([]string{"token"})
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].State != core.SecretStateUnavailable {
		t.Fatalf("unavailable backend should be diagnosed, not masked as missing: %+v", records)
	}
	if _, err := mc.Delete("token"); err == nil {
		t.Fatal("unavailable backend Delete must fail")
	}
	var count int
	_ = filepath.WalkDir(root, func(_ string, d fs.DirEntry, _ error) error {
		if d != nil && !d.IsDir() {
			count++
		}
		return nil
	})
	if count != 0 {
		t.Fatalf("plaintext fallback wrote %d file(s)", count)
	}
}

func TestManagedCredentialsFromRejectsNilPorts(t *testing.T) {
	if _, err := NewManagedCredentialsFrom(nil, nil); err == nil {
		t.Fatal("nil ports must be rejected")
	}
}
