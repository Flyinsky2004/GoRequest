package desktop

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// openStorageProject opens a project with an isolated secret root and an
// injected fake OS credential driver. No real keychain or user home is used.
// Without a persisted preference, this opens on the default local-file backend.
func openStorageProject(t *testing.T, name, secretRoot string, drv *memoryIdentityDriver) (*Manager, *App, *projectState) {
	t.Helper()
	m, _ := newTestManager(t)
	m.secretStore = platform.NewSecretStore(secretRoot, drv)
	if err := m.openProject(writeProject(t, name)); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	a := m.workspaces[0]
	return m, a, a.ps
}

// openKeychainStorageProject opens a project whose scope explicitly selected
// SecretBackendKeychain in preferences.json before opening, exercising both
// preservation of explicit keychain preferences and Keychain -> File transitions.
func openKeychainStorageProject(t *testing.T, name, secretRoot string, drv *memoryIdentityDriver) (*Manager, *App, *projectState) {
	t.Helper()
	dir := writeProject(t, name)
	proj, err := core.OpenProject(dir)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	store := platform.NewSecretStore(secretRoot, drv)
	scope, err := store.Scope(proj.File.ID, dir)
	if err != nil {
		t.Fatalf("Scope: %v", err)
	}
	if err := store.SetSelectedKind(scope, platform.SecretBackendKeychain); err != nil {
		t.Fatalf("SetSelectedKind: %v", err)
	}
	m, _ := newTestManager(t)
	m.secretStore = store
	if err := m.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	a := m.workspaces[0]
	return m, a, a.ps
}

func declareStorageSecret(t *testing.T, ps *projectState, envID, name string) {
	t.Helper()
	writeProjectFile(t, ps, "environments/"+envID+".yaml", `schemaVersion: 1
id: `+envID+`
variables:
  baseUrl: http://127.0.0.1:4010
secrets:
  - `+name+`
`)
	reloaded, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	ps.project = reloaded
}

// assertSecretSendUsesValue drives the real send path (tracked plan + HTTP
// execution) and proves the selected backend is what the request actually
// reads, not just the UI label.
func assertSecretSendUsesValue(t *testing.T, a *App, ps *projectState, name, want string) {
	t.Helper()
	if _, err := ps.createRequestIn("", name); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.HeaderRows = []*KVRow{{ID: nextRowID(), Enabled: true, Name: "Authorization", Value: "Bearer {{secret.apiToken}}"}}
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tab.URL = srv.URL + "/orders"
	tab.Dirty = true
	_, plan, _, err := a.buildTrackedTabPlan(nil, tab)
	if err != nil {
		t.Fatalf("build plan: %v", err)
	}
	if _, err := core.Execute(nil, plan, nil); err != nil {
		t.Fatalf("send: %v", err)
	}
	if got != "Bearer "+want {
		t.Fatalf("request used the wrong backend value: %q, want %q", got, "Bearer "+want)
	}
}

func switchBackend(t *testing.T, a *App, ps *projectState, kind platform.SecretBackendKind) *identityManagementState {
	t.Helper()
	st := identityMgmtFor(ps)
	a.beginSecretSwitch(ps, st, kind)
	if st.switchPlan == nil {
		t.Fatalf("no switch plan for %s: %s", kind, st.switchErr)
	}
	// The helper models "sync the latest source value"; conflicts are resolved
	// explicitly here rather than by the UI's safe keep-target default.
	st.switchPolicy = platform.ConflictOverwriteTarget
	a.confirmSecretSwitch(ps, st)
	if st.switchErr != "" {
		t.Fatalf("switch to %s failed: %s", kind, st.switchErr)
	}
	return st
}

func TestSecretStorageDefaultsToFileAndPreservesExplicitKeychain(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	secretRoot := filepath.Join(t.TempDir(), "secrets")

	// 1. Fresh project without preferences.json defaults to SecretBackendFile
	// and never touches the OS keychain driver.
	m, a, ps := openStorageProject(t, "DefaultFileStorage", secretRoot, drv)
	if ps.secretKind != platform.SecretBackendFile {
		t.Fatalf("default secretKind = %q, want %q", ps.secretKind, platform.SecretBackendFile)
	}
	declareStorageSecret(t, ps, "dev", "apiToken")
	a.openSecretEntry(ps, "apiToken")
	d := secretEntryFor(ps)
	d.value = "{{SECRET_default_file}}"
	a.saveSecretEntry(ps)
	if d.open {
		t.Fatalf("saveSecretEntry on default file backend failed: %s", d.errMsg)
	}
	drv.mu.Lock()
	kcGets, kcSets := drv.gets, drv.sets
	drv.mu.Unlock()
	if kcGets != 0 || kcSets != 0 {
		t.Fatalf("default file backend must not touch keychain driver: gets=%d sets=%d", kcGets, kcSets)
	}
	filePath, err := m.secretStore.FilePath(ps.secretScope)
	if err != nil {
		t.Fatal(err)
	}
	rawFile, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatalf("expected local secret file at %s: %v", filePath, err)
	}
	if !strings.Contains(string(rawFile), "{{SECRET_default_file}}") {
		t.Fatalf("local secret file did not store value: %s", rawFile)
	}
	assertNoRealValueInProject(t, ps.root, "{{SECRET_default_file}}")
	assertSecretSendUsesValue(t, a, ps, "OrdersDefaultFile", "{{SECRET_default_file}}")

	// 2. Another project with an explicit Keychain preference keeps Keychain
	// across restart and does NOT auto-migrate its keychain value into a file.
	kcDir := writeProject(t, "ExplicitKeychain")
	kcProj, err := core.OpenProject(kcDir)
	if err != nil {
		t.Fatal(err)
	}
	store1 := platform.NewSecretStore(secretRoot, drv)
	kcScope, err := store1.Scope(kcProj.File.ID, kcDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := store1.SetSelectedKind(kcScope, platform.SecretBackendKeychain); err != nil {
		t.Fatal(err)
	}
	kcKey, err := platform.ScopedSecretKey(kcScope, "apiToken")
	if err != nil {
		t.Fatal(err)
	}
	if err := drv.Set(kcKey, "{{SECRET_explicit_kc}}"); err != nil {
		t.Fatal(err)
	}

	m1, _ := newTestManagerAt(t, cfgPath)
	m1.secretStore = store1
	if err := m1.openProject(kcDir); err != nil {
		t.Fatal(err)
	}
	a1 := m1.workspaces[0]
	if a1.ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("explicit keychain project opened as %q", a1.ps.secretKind)
	}
	if v, ok := a1.ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_explicit_kc}}" {
		t.Fatalf("explicit keychain value = %q %v", v, ok)
	}
	kcFilePath, err := store1.FilePath(kcScope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(kcFilePath); !os.IsNotExist(err) {
		t.Fatalf("opening explicit keychain project must not auto-migrate to local file: %v", err)
	}

	// Simulated app restart preserves the explicit Keychain choice.
	m2, _ := newTestManagerAt(t, cfgPath)
	m2.secretStore = platform.NewSecretStore(secretRoot, drv)
	if err := m2.openProject(kcDir); err != nil {
		t.Fatal(err)
	}
	a2 := m2.workspaces[0]
	if a2.ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("restart lost explicit keychain choice: %q", a2.ps.secretKind)
	}
	declareStorageSecret(t, a2.ps, "dev", "apiToken")
	assertSecretSendUsesValue(t, a2, a2.ps, "OrdersExplicitKC", "{{SECRET_explicit_kc}}")
}

func TestSecretStorageSwitchUpdatesProviderAndKeepsSource(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	m, a, ps := openKeychainStorageProject(t, "StorageSwitch", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")

	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_switch_1}}"); err != nil {
		t.Fatal(err)
	}
	if v, ok := ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_switch_1}}" {
		t.Fatalf("keychain provider = %q %v", v, ok)
	}

	switchBackend(t, a, ps, platform.SecretBackendFile)
	if ps.secretKind != platform.SecretBackendFile {
		t.Fatalf("kind = %q", ps.secretKind)
	}
	if v, ok := ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_switch_1}}" {
		t.Fatalf("file provider = %q %v", v, ok)
	}
	// HTTP/WS/Runner all read ps.secrets; the app's own resolver must see it too.
	if v, ok := a.secretsProvider().Get("apiToken"); !ok || v != "{{SECRET_switch_1}}" {
		t.Fatalf("app resolver = %q %v", v, ok)
	}
	// The source keychain copy is preserved.
	scope, err := platform.ProjectScopeKey(ps.project.File.ID, ps.root)
	if err != nil {
		t.Fatal(err)
	}
	key, err := platform.ScopedSecretKey(scope, "apiToken")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := drv.count(key); !ok || v != "{{SECRET_switch_1}}" {
		t.Fatalf("keychain source removed: %q %v", v, ok)
	}
	// The file store is outside the project and holds the value.
	filePath, err := m.secretStore.FilePath(scope)
	if err != nil {
		t.Fatal(err)
	}
	if strings.HasPrefix(filePath, ps.root) {
		t.Fatalf("local secret file must not live in the project: %s", filePath)
	}
	data, err := os.ReadFile(filePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "{{SECRET_switch_1}}") {
		t.Fatal("file backend did not store the value")
	}
	// The project itself never contains the value.
	assertNoRealValueInProject(t, ps.root, "{{SECRET_switch_1}}")
	// The real send path reads the file backend.
	assertSecretSendUsesValue(t, a, ps, "OrdersFile", "{{SECRET_switch_1}}")

	// Switch back re-reads the latest value from the file.
	fileBackend := ps.secretBackend
	if err := fileBackend.Set("apiToken", "{{SECRET_switch_2}}"); err != nil {
		t.Fatal(err)
	}
	switchBackend(t, a, ps, platform.SecretBackendKeychain)
	if ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("kind after switch back = %q", ps.secretKind)
	}
	if v, ok := ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_switch_2}}" {
		t.Fatalf("switch-back provider = %q %v", v, ok)
	}
	if v, ok := drv.count(key); !ok || v != "{{SECRET_switch_2}}" {
		t.Fatalf("keychain not resynced: %q %v", v, ok)
	}
	// The real send path now reads the re-synced keychain value.
	assertSecretSendUsesValue(t, a, ps, "OrdersKeychain", "{{SECRET_switch_2}}")
}

func TestSecretStorageChoicePersistsAcrossRestart(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	secretRoot := filepath.Join(t.TempDir(), "secrets")

	m1, _ := newTestManagerAt(t, cfgPath)
	m1.secretStore = platform.NewSecretStore(secretRoot, drv)
	dir := writeProject(t, "StorageRestart")
	if err := m1.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a1 := m1.workspaces[0]
	if a1.ps.secretKind != platform.SecretBackendFile {
		t.Fatalf("initial default backend = %q, want %q", a1.ps.secretKind, platform.SecretBackendFile)
	}
	declareStorageSecret(t, a1.ps, "dev", "apiToken")
	mc, err := a1.managedCredentials(a1.ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_restart}}"); err != nil {
		t.Fatal(err)
	}
	switchBackend(t, a1, a1.ps, platform.SecretBackendKeychain)

	// A second manager at the same config and secret root is a restart.
	m2, _ := newTestManagerAt(t, cfgPath)
	m2.secretStore = platform.NewSecretStore(secretRoot, drv)
	if err := m2.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a2 := m2.workspaces[0]
	if a2.ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("restart lost the selected keychain backend: %q", a2.ps.secretKind)
	}
	if v, ok := a2.ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_restart}}" {
		t.Fatalf("restart provider = %q %v", v, ok)
	}
}

func TestSecretStorageConflictKeepsTargetByDefault(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openKeychainStorageProject(t, "StorageConflict", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")

	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_source}}"); err != nil {
		t.Fatal(err)
	}
	// Seed a different value in the target file store.
	scope := ps.secretScope
	fileBackend, err := a.mgr.secretStoreFor().Backend(scope, ps.project.File.ID, ps.root, platform.SecretBackendFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := fileBackend.Set("apiToken", "{{SECRET_target}}"); err != nil {
		t.Fatal(err)
	}

	st := identityMgmtFor(ps)
	a.beginSecretSwitch(ps, st, platform.SecretBackendFile)
	if st.switchPlan == nil || !st.switchPlan.HasConflicts() {
		t.Fatalf("conflict not planned: %+v err=%s", st.switchPlan, st.switchErr)
	}
	st.switchPolicy = platform.ConflictKeepTarget
	a.confirmSecretSwitch(ps, st)
	if st.switchErr != "" {
		t.Fatal(st.switchErr)
	}
	if v, ok := ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_target}}" {
		t.Fatalf("keep-target changed the target: %q %v", v, ok)
	}

	// Explicit overwrite replaces it.
	st2 := identityMgmtFor(ps)
	a.beginSecretSwitch(ps, st2, platform.SecretBackendKeychain)
	if st2.switchPlan == nil {
		t.Fatal(st2.switchErr)
	}
	// Source is now the file (target value), so seed the keychain explicitly
	// and switch back with overwrite to prove the policy is honored.
	if _, err := mc.Set("apiToken", "{{SECRET_keychain}}"); err != nil {
		t.Fatal(err)
	}
	st2.switchPlan = nil
	a.beginSecretSwitch(ps, st2, platform.SecretBackendKeychain)
	if st2.switchPlan == nil || !st2.switchPlan.HasConflicts() {
		t.Fatalf("switch-back conflict not planned: %+v", st2.switchPlan)
	}
	st2.switchPolicy = platform.ConflictOverwriteTarget
	a.confirmSecretSwitch(ps, st2)
	if st2.switchErr != "" {
		t.Fatal(st2.switchErr)
	}
	// The source (file) value replaces the different keychain value.
	if v, ok := ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_target}}" {
		t.Fatalf("overwrite did not apply: %q %v", v, ok)
	}
	key, err := platform.ScopedSecretKey(scope, "apiToken")
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := drv.count(key); !ok || v != "{{SECRET_target}}" {
		t.Fatalf("keychain target was not overwritten: %q %v", v, ok)
	}
}

func TestSecretStorageUnavailableTargetKeepsOldScheme(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openKeychainStorageProject(t, "StorageUnavailable", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_keep}}"); err != nil {
		t.Fatal(err)
	}

	// Replace the file root with a symlink so the target reports unavailable.
	if err := os.RemoveAll(secretRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), secretRoot); err != nil {
		t.Fatal(err)
	}
	st := identityMgmtFor(ps)
	a.beginSecretSwitch(ps, st, platform.SecretBackendFile)
	// The manifest shares the replaced root. Refuse before reading it rather
	// than following the link just to build a plan for an unavailable target.
	if st.switchPlan != nil || !strings.Contains(st.switchErr, "symlink") {
		t.Fatalf("symlinked manifest root was not rejected before planning: %s", st.switchErr)
	}
	if st.switchErr == "" {
		t.Fatal("switch to an unavailable target must fail")
	}
	if ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("old scheme not kept: %q", ps.secretKind)
	}
	if v, ok := ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_keep}}" {
		t.Fatalf("old provider broken: %q %v", v, ok)
	}
}

func TestSecretStorageMultipleWindowsStayIsolated(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	m, _ := newTestManager(t)
	m.secretStore = platform.NewSecretStore(secretRoot, drv)
	if err := m.openProject(writeProject(t, "StorageA")); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(writeProject(t, "StorageB")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	b := m.workspaces[1]
	declareStorageSecret(t, a.ps, "dev", "apiToken")
	declareStorageSecret(t, b.ps, "dev", "apiToken")
	mcA, err := a.managedCredentials(a.ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mcA.Set("apiToken", "{{SECRET_A}}"); err != nil {
		t.Fatal(err)
	}
	mcB, err := b.managedCredentials(b.ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mcB.Set("apiToken", "{{SECRET_B}}"); err != nil {
		t.Fatal(err)
	}

	switchBackend(t, a, a.ps, platform.SecretBackendKeychain)
	if a.ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("window A kind = %q", a.ps.secretKind)
	}
	if b.ps.secretKind != platform.SecretBackendFile {
		t.Fatalf("window B kind changed: %q", b.ps.secretKind)
	}
	if v, ok := b.ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_B}}" {
		t.Fatalf("window B provider = %q %v", v, ok)
	}
	if v, ok := a.ps.secrets.Get("apiToken"); !ok || v != "{{SECRET_A}}" {
		t.Fatalf("window A provider = %q %v", v, ok)
	}
}

func TestSecretStorageEnvOverrideStillWins(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openStorageProject(t, "StorageEnv", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_stored}}"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GOREQUEST_SECRET_APITOKEN", "{{SECRET_env}}")
	if v, ok := a.secretsProvider().Get("apiToken"); !ok || v != "{{SECRET_env}}" {
		t.Fatalf("environment override no longer wins: %q %v", v, ok)
	}
}

func TestSecretStorageReferenceNamesAndManifestSurviveSwitch(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openKeychainStorageProject(t, "StorageRefs", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	// An undeclared but managed name must be recoverable through the manifest.
	if _, err := mc.Set("undeclaredSecret", "{{SECRET_undeclared}}"); err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_declared}}"); err != nil {
		t.Fatal(err)
	}
	switchBackend(t, a, ps, platform.SecretBackendFile)
	refs := ps.project.SecretReferences(nil)
	names := map[string]bool{}
	for _, ref := range refs {
		names[ref.Name] = true
	}
	if !names["apiToken"] {
		t.Fatalf("declared reference lost: %+v", refs)
	}
	if v, ok := ps.secrets.Get("undeclaredSecret"); !ok || v != "{{SECRET_undeclared}}" {
		t.Fatalf("undeclared managed secret not copied: %q %v", v, ok)
	}
	manifestNames, err := a.mgr.secretStoreFor().Manifest(ps.secretScope).Names()
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(manifestNames, ",")
	if !strings.Contains(joined, "apiToken") || !strings.Contains(joined, "undeclaredSecret") {
		t.Fatalf("manifest lost managed names: %v", manifestNames)
	}
	// The environment file still holds only the name.
	envFile := readProjectFile(t, ps, "environments/dev.yaml")
	if strings.Contains(envFile, "{{SECRET_declared}}") {
		t.Fatal("secret value leaked into the project environment file")
	}
}

func TestSecretEntryShowsActualBackendPath(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openStorageProject(t, "StorageEntryUI", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_ui}}"); err != nil {
		t.Fatal(err)
	}

	a.openSecretEntry(ps, "apiToken")
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	m := a.secretEntryMessages()
	filePath, err := a.mgr.secretStoreFor().FilePath(ps.secretScope)
	if err != nil {
		t.Fatal(err)
	}
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, m.BackendPlaintext) {
		t.Fatalf("plaintext warning missing; texts=%v", tt.Texts())
	}
	if !strings.Contains(texts, filePath) {
		t.Fatalf("actual file path missing; texts=%v", tt.Texts())
	}
	if !strings.Contains(texts, m.ChangeStorage) {
		t.Fatalf("change-storage action missing; texts=%v", tt.Texts())
	}
}

func TestSecretStorageUIShowsUnavailableReason(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openStorageProject(t, "StorageUnavailableUI", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_ui_unavail}}"); err != nil {
		t.Fatal(err)
	}

	// The selected file backend becomes unavailable (for example a planted
	// symlink); the UI must show the real reason instead of pretending it works.
	if err := os.RemoveAll(secretRoot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), secretRoot); err != nil {
		t.Fatal(err)
	}
	a.openIdentityManagement(nil, 2)
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	m := a.identityManagementMsg()
	texts := strings.Join(tt.Texts(), "\n")
	if !strings.Contains(texts, m.StoragePlaintext) {
		t.Fatalf("plaintext warning missing; texts=%v", tt.Texts())
	}
	if !strings.Contains(texts, "symlink") {
		t.Fatalf("unavailable reason missing; texts=%v", tt.Texts())
	}
}

func TestCredentialsPageShowsStoragePlanAndSwitches(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	secretRoot := filepath.Join(t.TempDir(), "secrets")
	_, a, ps := openStorageProject(t, "StoragePageUI", secretRoot, drv)
	declareStorageSecret(t, ps, "dev", "apiToken")
	mc, err := a.managedCredentials(ps)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mc.Set("apiToken", "{{SECRET_page}}"); err != nil {
		t.Fatal(err)
	}

	a.openIdentityManagement(nil, 2)
	st := identityMgmtFor(ps)
	st.storageChoice = a.identityManagementMsg().StorageKeychain
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	m := a.identityManagementMsg()
	if !tt.HasText(m.StorageTitle) || !tt.HasText(m.StorageSwitch) {
		t.Fatalf("storage section missing; texts=%v", tt.Texts())
	}
	if err := tt.Click(m.StorageSwitch); err != nil {
		t.Fatalf("click switch: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	if st.switchPlan == nil {
		t.Fatalf("plan not built: %s", st.switchErr)
	}
	if !tt.HasText(m.StoragePlanTitle) || !tt.HasText(m.StorageConfirm) {
		t.Fatalf("plan not rendered; texts=%v", tt.Texts())
	}
	if err := tt.Click(m.StorageConfirm); err != nil {
		t.Fatalf("click confirm: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	if ps.secretKind != platform.SecretBackendKeychain {
		t.Fatalf("switch not applied: %q err=%s", ps.secretKind, st.switchErr)
	}
	if !tt.HasText(m.StorageKeychain) {
		t.Fatalf("new location not shown; texts=%v", tt.Texts())
	}
}
