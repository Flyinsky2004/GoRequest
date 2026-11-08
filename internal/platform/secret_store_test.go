package platform

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorequest/internal/core"
)

func newTestSecretStore(t *testing.T) (*SecretStore, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "secrets")
	return NewSecretStore(root, newMemoryKeychainDriver()), root
}

func TestFileSecretsPermissionsAtomicAndPlaintextLabel(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	fs, err := NewFileSecrets(root, "demo:abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("apiToken", "{{SECRET_file_1}}"); err != nil {
		t.Fatal(err)
	}
	dirInfo, err := os.Stat(root)
	if err != nil {
		t.Fatal(err)
	}
	if dirInfo.Mode().Perm() != 0o700 {
		t.Fatalf("secret dir mode = %o, want 0700", dirInfo.Mode().Perm())
	}
	fileInfo, err := os.Stat(fs.Path())
	if err != nil {
		t.Fatal(err)
	}
	if fileInfo.Mode().Perm() != 0o600 {
		t.Fatalf("secret file mode = %o, want 0600", fileInfo.Mode().Perm())
	}
	info := fs.Info()
	if !info.Available || !info.Plaintext || info.Path != fs.Path() {
		t.Fatalf("unexpected file backend info: %+v", info)
	}
	if !strings.Contains(info.Label, "plaintext") {
		t.Fatalf("file backend must advertise plaintext, got %q", info.Label)
	}
	if v, ok := fs.Get("apiToken"); !ok || v != "{{SECRET_file_1}}" {
		t.Fatalf("Get = %q %v", v, ok)
	}
	if st := fs.InspectSecret("apiToken"); st.State != core.SecretStateBound || st.Backend != "file" {
		t.Fatalf("unexpected status: %+v", st)
	}
	if st := fs.InspectSecret("missing"); st.State != core.SecretStateMissing {
		t.Fatalf("unexpected missing status: %+v", st)
	}
	names, ok := fs.Names()
	if !ok || len(names) != 1 || names[0] != "apiToken" {
		t.Fatalf("Names = %v %v", names, ok)
	}
}

func TestFileSecretsRejectsSymlinkAndBounds(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	fs, err := NewFileSecrets(root, "demo:abc")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(target, []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, fs.Path()); err != nil {
		t.Fatal(err)
	}
	if err := fs.Set("apiToken", "v"); err == nil {
		t.Fatal("writing through a symlinked store must fail")
	}
	if st := fs.InspectSecret("apiToken"); st.State != core.SecretStateUnavailable {
		t.Fatalf("symlinked store must read as unavailable, got %+v", st)
	}

	// A symlinked root is refused too.
	linkRoot := filepath.Join(t.TempDir(), "linked-secrets")
	if err := os.Symlink(root, linkRoot); err != nil {
		t.Fatal(err)
	}
	linked, err := NewFileSecrets(linkRoot, "demo:abc")
	if err != nil {
		t.Fatal(err)
	}
	if linked.Info().Available {
		t.Fatal("symlinked secret root must be unavailable")
	}

	// Oversized values are rejected before any write.
	clean, err := NewFileSecrets(filepath.Join(t.TempDir(), "clean"), "demo:abc")
	if err != nil {
		t.Fatal(err)
	}
	if err := clean.Set("apiToken", strings.Repeat("x", MaxFileSecretBytes+1)); err == nil {
		t.Fatal("oversized value must be rejected")
	}
	if err := clean.Set("../escape", "v"); err == nil {
		t.Fatal("path-like name must be rejected")
	}
}

func TestKeychainProbeReportsUnavailableWithoutWriting(t *testing.T) {
	drv := newMemoryKeychainDriver()
	drv.err = errors.ErrUnsupported
	kc, err := NewKeychainSecrets(drv, "demo", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info := kc.Info(); !info.Available {
		t.Fatal("Info must not probe before an explicit action")
	}
	info := kc.Probe()
	if info.Available {
		t.Fatalf("unsupported store must probe as unavailable: %+v", info)
	}
	if !strings.Contains(info.Detail, "unsupported") {
		t.Fatalf("missing reason: %+v", info)
	}
	if info := kc.Info(); info.Available {
		t.Fatalf("Info must reflect the cached probe: %+v", info)
	}
	drv.mu.Lock()
	writes := len(drv.data)
	drv.mu.Unlock()
	if writes != 0 {
		t.Fatalf("probe must not write; driver holds %d entries", writes)
	}
}

func TestFileSecretsCloneIsolation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "secrets")
	cloneA := filepath.Join(t.TempDir(), "repo")
	cloneB := filepath.Join(t.TempDir(), "repo")
	scopeA, err := ProjectScopeKey("demo", cloneA)
	if err != nil {
		t.Fatal(err)
	}
	scopeB, err := ProjectScopeKey("demo", cloneB)
	if err != nil {
		t.Fatal(err)
	}
	a, err := NewFileSecrets(root, scopeA)
	if err != nil {
		t.Fatal(err)
	}
	b, err := NewFileSecrets(root, scopeB)
	if err != nil {
		t.Fatal(err)
	}
	if a.Path() == b.Path() {
		t.Fatalf("distinct clones share a local store: %s", a.Path())
	}
	if err := a.Set("apiToken", "{{SECRET_A}}"); err != nil {
		t.Fatal(err)
	}
	if err := b.Set("apiToken", "{{SECRET_B}}"); err != nil {
		t.Fatal(err)
	}
	if v, _ := a.Get("apiToken"); v != "{{SECRET_A}}" {
		t.Fatalf("clone A = %q", v)
	}
	if v, _ := b.Get("apiToken"); v != "{{SECRET_B}}" {
		t.Fatalf("clone B = %q", v)
	}
}

func TestSecretStorePreferencePersists(t *testing.T) {
	store, root := newTestSecretStore(t)
	scope := "demo:abc"
	if got := store.SelectedKind(scope); got != SecretBackendKeychain {
		t.Fatalf("default kind = %q", got)
	}
	if err := store.SetSelectedKind(scope, SecretBackendFile); err != nil {
		t.Fatal(err)
	}
	// A fresh store at the same root is a restart.
	reloaded := NewSecretStore(root, newMemoryKeychainDriver())
	if got := reloaded.SelectedKind(scope); got != SecretBackendFile {
		t.Fatalf("persisted kind = %q", got)
	}
	// Another clone keeps its own default.
	if got := reloaded.SelectedKind("demo:def"); got != SecretBackendKeychain {
		t.Fatalf("unrelated scope kind = %q", got)
	}
	prefsInfo, err := os.Stat(filepath.Join(root, "preferences.json"))
	if err != nil {
		t.Fatal(err)
	}
	if prefsInfo.Mode().Perm() != 0o600 {
		t.Fatalf("preferences mode = %o", prefsInfo.Mode().Perm())
	}
}

func TestManifestPersistsManagedNames(t *testing.T) {
	store, root := newTestSecretStore(t)
	scope := "demo:abc"
	manifest := store.Manifest(scope)
	if err := manifest.Add("apiToken"); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Add("apiToken"); err != nil {
		t.Fatal(err)
	}
	if err := manifest.Add("folderPass"); err != nil {
		t.Fatal(err)
	}
	reloaded := NewSecretStore(root, newMemoryKeychainDriver()).Manifest(scope)
	names, err := reloaded.Names()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "apiToken,folderPass" {
		t.Fatalf("manifest names = %v", names)
	}
	if err := reloaded.Remove("apiToken"); err != nil {
		t.Fatal(err)
	}
	names, err = reloaded.Names()
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "folderPass" {
		t.Fatalf("manifest names after remove = %v", names)
	}
}

func TestManagedCredentialsPersistsManifestAndEnumeratesFile(t *testing.T) {
	store, root := newTestSecretStore(t)
	scope := "demo:abc"
	fileBackend, err := store.Backend(scope, "demo", t.TempDir(), SecretBackendFile)
	if err != nil {
		t.Fatal(err)
	}
	mc, err := NewManagedCredentialsWithBackend(fileBackend, store.Manifest(scope))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_manifest_1}}"); err != nil {
		t.Fatal(err)
	}
	// A fresh credentials store at the same root must still list the managed
	// name even if the backend could not enumerate.
	reloadedBackend, err := store.Backend(scope, "demo", t.TempDir(), SecretBackendFile)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := NewManagedCredentialsWithBackend(reloadedBackend, NewSecretStore(root, nil).Manifest(scope))
	if err != nil {
		t.Fatal(err)
	}
	records, err := reloaded.List(nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, rec := range records {
		if rec.Name == "apiToken" && rec.State == core.SecretStateBound {
			found = true
		}
	}
	if !found {
		t.Fatalf("managed name not recovered from manifest: %+v", records)
	}
}

// failingBackend wraps a backend and fails Set for one name.
type failingBackend struct {
	SecretBackend
	failOn string
}

func (f *failingBackend) Set(name, value string) error {
	if name == f.failOn {
		return errors.New("injected write failure")
	}
	return f.SecretBackend.Set(name, value)
}

func TestSwitchBidirectionalCopiesVerifiesAndKeepsSource(t *testing.T) {
	store, _ := newTestSecretStore(t)
	scope := "demo:abc"
	projectRoot := t.TempDir()
	keychain, err := store.Backend(scope, "demo", projectRoot, SecretBackendKeychain)
	if err != nil {
		t.Fatal(err)
	}
	fileBackend, err := store.Backend(scope, "demo", projectRoot, SecretBackendFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := keychain.Set("apiToken", "{{SECRET_kc_1}}"); err != nil {
		t.Fatal(err)
	}
	if err := keychain.Set("folderPass", "{{SECRET_kc_2}}"); err != nil {
		t.Fatal(err)
	}

	plan, err := BuildSecretSwitchPlan(scope, keychain, fileBackend, []string{"apiToken", "folderPass"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.SourceEnumerated {
		t.Fatal("keychain must not claim enumeration")
	}
	if plan.CopyCount() != 2 || plan.HasConflicts() {
		t.Fatalf("unexpected plan: %+v", plan)
	}
	result, err := ExecuteSecretSwitch(keychain, fileBackend, plan, ConflictKeepTarget)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Copied) != 2 || result.Verified != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
	if v, ok := fileBackend.Get("apiToken"); !ok || v != "{{SECRET_kc_1}}" {
		t.Fatalf("file copy = %q %v", v, ok)
	}
	// The source copy is never removed.
	if v, ok := keychain.Get("apiToken"); !ok || v != "{{SECRET_kc_1}}" {
		t.Fatalf("source copy removed: %q %v", v, ok)
	}

	// Switch back re-reads the latest file value.
	if err := fileBackend.Set("apiToken", "{{SECRET_file_updated}}"); err != nil {
		t.Fatal(err)
	}
	backPlan, err := BuildSecretSwitchPlan(scope, fileBackend, keychain, []string{"apiToken"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !backPlan.SourceEnumerated {
		t.Fatal("file backend must enumerate")
	}
	if _, err := ExecuteSecretSwitch(fileBackend, keychain, backPlan, ConflictOverwriteTarget); err != nil {
		t.Fatal(err)
	}
	if v, ok := keychain.Get("apiToken"); !ok || v != "{{SECRET_file_updated}}" {
		t.Fatalf("switch-back value = %q %v", v, ok)
	}
}

func TestSwitchConflictPolicies(t *testing.T) {
	store, _ := newTestSecretStore(t)
	scope := "demo:abc"
	projectRoot := t.TempDir()
	keychain, _ := store.Backend(scope, "demo", projectRoot, SecretBackendKeychain)
	fileBackend, _ := store.Backend(scope, "demo", projectRoot, SecretBackendFile)
	_ = keychain.Set("apiToken", "{{SECRET_source}}")
	_ = fileBackend.Set("apiToken", "{{SECRET_target}}")

	plan, err := BuildSecretSwitchPlan(scope, keychain, fileBackend, []string{"apiToken"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !plan.HasConflicts() || plan.ConflictCount() != 1 {
		t.Fatalf("conflict not detected: %+v", plan)
	}
	if _, err := ExecuteSecretSwitch(keychain, fileBackend, plan, ConflictKeepTarget); err != nil {
		t.Fatal(err)
	}
	if v, _ := fileBackend.Get("apiToken"); v != "{{SECRET_target}}" {
		t.Fatalf("keep-target overwrote: %q", v)
	}
	if _, err := ExecuteSecretSwitch(keychain, fileBackend, plan, ConflictOverwriteTarget); err != nil {
		t.Fatal(err)
	}
	if v, _ := fileBackend.Get("apiToken"); v != "{{SECRET_source}}" {
		t.Fatalf("overwrite did not replace: %q", v)
	}
}

func TestSwitchMidFailureRollsBackTarget(t *testing.T) {
	store, _ := newTestSecretStore(t)
	scope := "demo:abc"
	projectRoot := t.TempDir()
	keychain, _ := store.Backend(scope, "demo", projectRoot, SecretBackendKeychain)
	fileBackend, _ := store.Backend(scope, "demo", projectRoot, SecretBackendFile)
	_ = keychain.Set("apiToken", "{{SECRET_a}}")
	_ = keychain.Set("folderPass", "{{SECRET_b}}")
	_ = fileBackend.Set("existing", "{{SECRET_old}}")

	plan, err := BuildSecretSwitchPlan(scope, keychain, fileBackend, []string{"apiToken", "folderPass", "existing"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	failing := &failingBackend{SecretBackend: fileBackend, failOn: "folderPass"}
	if _, err := ExecuteSecretSwitch(keychain, failing, plan, ConflictKeepTarget); err == nil {
		t.Fatal("expected injected failure")
	}
	// apiToken was written before the failure and must be rolled back.
	if _, ok := fileBackend.Get("apiToken"); ok {
		t.Fatal("target write was not rolled back")
	}
	if v, ok := fileBackend.Get("existing"); !ok || v != "{{SECRET_old}}" {
		t.Fatalf("pre-existing target value changed: %q %v", v, ok)
	}
	// Source is untouched.
	if v, ok := keychain.Get("apiToken"); !ok || v != "{{SECRET_a}}" {
		t.Fatalf("source changed: %q %v", v, ok)
	}
}

func TestSwitchUnavailableTargetFailsBeforeWriting(t *testing.T) {
	store, _ := newTestSecretStore(t)
	scope := "demo:abc"
	projectRoot := t.TempDir()
	keychain, _ := store.Backend(scope, "demo", projectRoot, SecretBackendKeychain)
	_ = keychain.Set("apiToken", "{{SECRET_a}}")

	// A file backend whose root is a symlink reports unavailable.
	linkRoot := filepath.Join(t.TempDir(), "link")
	if err := os.Symlink(t.TempDir(), linkRoot); err != nil {
		t.Fatal(err)
	}
	unavailable, err := NewFileSecrets(linkRoot, scope)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := BuildSecretSwitchPlan(scope, keychain, unavailable, []string{"apiToken"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Target.Available {
		t.Fatal("symlinked target must be unavailable")
	}
	if _, err := ExecuteSecretSwitch(keychain, unavailable, plan, ConflictKeepTarget); err == nil {
		t.Fatal("switch to unavailable target must fail")
	}
}

func TestSwitchRollbackAfterSuccessfulExecute(t *testing.T) {
	store, _ := newTestSecretStore(t)
	scope := "demo:abc"
	projectRoot := t.TempDir()
	keychain, _ := store.Backend(scope, "demo", projectRoot, SecretBackendKeychain)
	fileBackend, _ := store.Backend(scope, "demo", projectRoot, SecretBackendFile)
	_ = keychain.Set("apiToken", "{{SECRET_new}}")
	_ = fileBackend.Set("apiToken", "{{SECRET_old}}")

	plan, _ := BuildSecretSwitchPlan(scope, keychain, fileBackend, []string{"apiToken"}, nil)
	result, err := ExecuteSecretSwitch(keychain, fileBackend, plan, ConflictOverwriteTarget)
	if err != nil {
		t.Fatal(err)
	}
	if v, _ := fileBackend.Get("apiToken"); v != "{{SECRET_new}}" {
		t.Fatalf("switch did not write: %q", v)
	}
	if err := result.Rollback(); err != nil {
		t.Fatal(err)
	}
	if v, _ := fileBackend.Get("apiToken"); v != "{{SECRET_old}}" {
		t.Fatalf("rollback did not restore: %q", v)
	}
}
