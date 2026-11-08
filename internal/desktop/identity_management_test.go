package desktop

import (
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

func TestIdentityManagementMessagesCoverBothLanguages(t *testing.T) {
	zh := reflect.ValueOf(identityManagementMessagesZh)
	en := reflect.ValueOf(identityManagementMessagesEn)
	typ := zh.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		a := strings.TrimSpace(zh.Field(i).String())
		b := strings.TrimSpace(en.Field(i).String())
		if a == "" || b == "" {
			t.Errorf("%s empty: zh=%q en=%q", f.Name, a, b)
		}
		if a == b {
			t.Errorf("%s not translated: %q", f.Name, a)
		}
	}
}

// memoryIdentityDriver is an in-memory fake OS credential backend. It never
// writes a real system keychain.
type memoryIdentityDriver struct {
	mu   sync.Mutex
	data map[string]string
	err  error
	gets int
	sets int
	dels int
}

func newMemoryIdentityDriver() *memoryIdentityDriver {
	return &memoryIdentityDriver{data: map[string]string{}}
}

func (d *memoryIdentityDriver) Get(key string) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.gets++
	if d.err != nil {
		return "", d.err
	}
	v, ok := d.data[key]
	if !ok {
		return "", platform.ErrSecretNotFound
	}
	return v, nil
}

func (d *memoryIdentityDriver) Set(key, value string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.sets++
	if d.err != nil {
		return d.err
	}
	d.data[key] = value
	return nil
}

func (d *memoryIdentityDriver) Delete(key string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dels++
	if d.err != nil {
		return d.err
	}
	if _, ok := d.data[key]; !ok {
		return platform.ErrSecretNotFound
	}
	delete(d.data, key)
	return nil
}

func (d *memoryIdentityDriver) count(key string) (string, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.data[key]
	return v, ok
}

// withIdentityFakeBackend installs a fake keychain driver for one test and
// restores the previous factory.
func withIdentityFakeBackend(t *testing.T) *memoryIdentityDriver {
	t.Helper()
	drv := newMemoryIdentityDriver()
	prev := identityKeychainDriverFactory
	identityKeychainDriverFactory = func() platform.KeychainDriver { return drv }
	t.Cleanup(func() { identityKeychainDriverFactory = prev })
	return drv
}

func projectRootFor(ps *projectState) string { return ps.root }

func writeProjectFile(t *testing.T, ps *projectState, rel, content string) {
	t.Helper()
	full := filepath.Join(ps.project.Dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readProjectFile(t *testing.T, ps *projectState, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(ps.project.Dir, filepath.FromSlash(rel)))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// TestIdentityManagementCreateReferenceSendSavePreservesRef is the main
// no-window lifecycle: create a reusable scheme, reference it from a request,
// send to a temporary local HTTP server, edit the URL and save, reopen the
// project, and verify `auth.ref` is still a ref (never expanded inline).
func TestIdentityManagementCreateReferenceSendSavePreservesRef(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Identity")
	root := projectRootFor(ps)

	// Declare a secret and store its value in the fake OS backend only.
	writeProjectFile(t, ps, "environments/dev.yaml", `schemaVersion: 1
id: dev
variables:
  baseUrl: http://127.0.0.1:4010
secrets:
  - apiToken
`)
	// Reload so the opened project sees the declared secret.
	reloaded, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	ps.project = reloaded
	// The plan builder resolves through mgrSecrets(); inject the same
	// project-scoped fake backend there so no real keychain is touched.
	kcProvider, err := platform.NewKeychainSecrets(drv, reloaded.File.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	a.mgr.secrets = kcProvider

	st := identityMgmtFor(ps)
	a.openIdentityManagement(nil, 0)
	a.beginNewAuthScheme(ps, st)
	st.draft.ID = "team-bearer"
	st.draft.Type = "bearer"
	st.draft.Token = "{{secret.apiToken}}"
	tt := ui.NewTester(a.IdentityManagementView, 1280, 900)
	tt.Frame()
	if !tt.HasText("Identity and credential management") {
		// Chinese default in real config; the test manager forces English.
		if !tt.HasText(identityManagementMessagesEn.IdentityManageTitle) {
			t.Fatalf("management dialog not rendered; texts=%v", tt.Texts())
		}
	}
	if err := tt.Click(identityManagementMessagesEn.SchemeCreate); err != nil {
		t.Fatalf("click create scheme: %v", err)
	}
	if ps.project.AuthSchemes["team-bearer"] == nil {
		t.Fatalf("scheme not created; texts=%v", tt.Texts())
	}
	schemeBytes := readProjectFile(t, ps, "auth/team-bearer.auth.yaml")
	if !strings.Contains(schemeBytes, "{{secret.apiToken}}") {
		t.Fatalf("scheme template not saved: %s", schemeBytes)
	}

	// Create a request, reference the scheme through the tab, and write the
	// credential to the fake OS backend through the management API.
	entry, err := ps.createRequestIn("", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.AuthMode = "ref"
	tab.AuthRefID = "team-bearer"
	tab.Dirty = true
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(readProjectFile(t, ps, entry.RelPath), "ref: team-bearer") {
		t.Fatalf("auth.ref not persisted: %s", readProjectFile(t, ps, entry.RelPath))
	}

	secretValue := "{{SECRET_hg7t2qre}}"
	if err := kcProvider.Set("apiToken", secretValue); err != nil {
		t.Fatal(err)
	}
	scope, _ := platform.ProjectScopeKey(ps.project.File.ID, root)
	scopedKey, _ := platform.ScopedSecretKey(scope, "apiToken")
	if got, ok := drv.count(scopedKey); !ok || got != secretValue {
		t.Fatalf("credential not stored under scoped key: %q %v", got, ok)
	}

	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(204)
	}))
	defer srv.Close()
	tab.URL = srv.URL + "/orders"
	tab.Dirty = true
	_, plan, red, err := a.buildTrackedTabPlan(nil, tab)
	if err != nil {
		t.Fatal(err)
	}
	_ = red
	if _, err := core.Execute(nil, plan, nil); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+secretValue {
		t.Fatalf("request did not carry the resolved bearer token: %q", gotAuth)
	}

	// Editing only the URL and saving must preserve `ref`, not expand it.
	tab.URL = srv.URL + "/orders?edited=1"
	tab.Dirty = true
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	reopened, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	reloadedEntry := reopened.Requests[entry.Request.ID]
	if reloadedEntry == nil || reloadedEntry.Request.Auth == nil ||
		reloadedEntry.Request.Auth.Mode != "ref" || reloadedEntry.Request.Auth.Ref != "team-bearer" {
		t.Fatalf("auth.ref lost on save/reopen: %+v", reloadedEntry)
	}
	if raw := readProjectFile(t, ps, entry.RelPath); !strings.Contains(raw, "ref: team-bearer") &&
		!strings.Contains(raw, "ref: team-bearer\n") {
		t.Fatalf("auth.ref lost from YAML: %s", raw)
	}
	// No secret value may appear anywhere in the shared project files.
	var leaks []string
	_ = filepath.WalkDir(filepath.Join(root, ".gorequest"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), secretValue) {
			leaks = append(leaks, path)
		}
		return nil
	})
	if len(leaks) != 0 {
		t.Fatalf("secret value leaked into project files: %v", leaks)
	}
}

func TestIdentityManagementFolderInheritanceAndExternalProtection(t *testing.T) {
	withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Identity")
	st := identityMgmtFor(ps)
	_, err := ps.createFolder("", "team")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.project.CreateAuthScheme(&core.AuthScheme{
		ID: "folder-scheme", Type: "apikey", Name: "X-Team", In: "header", Value: "{{secret.folderKey}}",
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := ps.createRequestIn("team", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	if tab.AuthMode != "inherit" {
		t.Fatalf("new request should inherit, got %q", tab.AuthMode)
	}

	// Set folder auth through the management controller, then verify the request
	// resolves to it without writing auth into the request file.
	st.folderRel = "team"
	a.openIdentityManagement(tab, 1)
	st.folderMode = "ref"
	st.folderRef = "folder-scheme"
	a.requestSaveFolderAuth(ps, st, false)
	rawCollection := readProjectFile(t, ps, "collections/team/collection.yaml")
	if !strings.Contains(rawCollection, "ref: folder-scheme") {
		t.Fatalf("folder auth not saved: %s", rawCollection)
	}
	if strings.Contains(readProjectFile(t, ps, entry.RelPath), "auth:") {
		t.Fatalf("folder auth should not be written into request files: %s", readProjectFile(t, ps, entry.RelPath))
	}
	eff, err := ps.project.ResolveEffectiveAuth(entry, nil)
	if err != nil || eff == nil || eff.Mode != "ref" || eff.RefID != "folder-scheme" {
		t.Fatalf("request did not inherit folder auth: %+v %v", eff, err)
	}
	if len(ps.project.FolderAuthImpact("team")) != 1 {
		t.Fatalf("expected one inherited request, got %+v", ps.project.FolderAuthImpact("team"))
	}

	// External modification must be reported and must not overwrite disk.
	baseline := ps.project.FolderAuthBaseline("team")
	writeProjectFile(t, ps, "collections/team/collection.yaml", "schemaVersion: 1\nid: team\nname: Team\nauth: none\n")
	st.folderBaseline = baseline
	st.folderMode = "none"
	st.folderConfirm = false
	a.requestSaveFolderAuth(ps, st, false)
	if !strings.Contains(st.errMsg, identityManagementMessagesEn.FolderConflict) {
		t.Fatalf("external change not reported: %q", st.errMsg)
	}
	if raw := readProjectFile(t, ps, "collections/team/collection.yaml"); !strings.Contains(raw, "auth: none") {
		t.Fatalf("external change was overwritten: %s", raw)
	}

	// A dirty affected buffer requires an explicit second confirmation.
	ps.project.LoadFolderIdentities()
	tab.Dirty = true
	st.errMsg = ""
	st.folderBaseline = ps.project.FolderAuthBaseline("team")
	st.folderConfirm = false
	st.folderMode = "none"
	a.requestSaveFolderAuth(ps, st, false)
	if !st.folderConfirm || len(st.folderDirty) != 1 {
		t.Fatalf("dirty buffer protection not triggered: %+v", st)
	}
	if ps.project.FolderAuth("team") == nil || ps.project.FolderAuth("team").Mode != "none" {
		t.Fatal("folder changed before dirty confirmation")
	}
	a.requestSaveFolderAuth(ps, st, false)
	if err := st.errMsg; err != "" {
		t.Fatalf("confirmed save failed: %q", err)
	}
}

func TestIdentityManagementCredentialCreateMissingFailureDelete(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Identity")
	root := projectRootFor(ps)
	writeProjectFile(t, ps, "environments/dev.yaml", `schemaVersion: 1
id: dev
variables: {}
secrets:
  - apiToken
`)
	reopened, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	ps.project = reopened
	kcBackend, err := platform.NewKeychainSecrets(drv, reopened.File.ID, root)
	if err != nil {
		t.Fatal(err)
	}
	ps.secretKind = platform.SecretBackendKeychain
	ps.secretBackend = kcBackend
	layered, err := platform.NewLayeredSecrets(kcBackend, nil)
	if err != nil {
		t.Fatal(err)
	}
	ps.secrets = layered
	resetManagedCredentials(ps)
	st := identityMgmtFor(ps)
	a.openIdentityManagement(nil, 2)
	a.refreshCredentialEntries(ps, st, true)
	if len(st.credEntries) != 1 || st.credEntries[0].Status.State != core.SecretStateMissing {
		t.Fatalf("expected missing credential status: %+v", st.credEntries)
	}
	// Show the missing state in the real dialog.
	tt := ui.NewTester(a.IdentityManagementView, 1280, 900)
	tt.Frame()
	if !tt.HasText(identityManagementMessagesEn.CredStatusMissing) {
		t.Fatalf("missing status not rendered; texts=%v", tt.Texts())
	}
	_ = tt

	secretValue := "{{SECRET_5xpt8v3a}}"
	st.credName = "apiToken"
	st.credValue = secretValue
	a.saveCredential(ps, st)
	if st.credValue != "" {
		t.Fatal("credential value not cleared from UI draft")
	}
	a.refreshCredentialEntries(ps, st, true)
	if st.credEntries[0].Status.State != core.SecretStateBound {
		t.Fatalf("credential should be bound after save: %+v", st.credEntries[0])
	}

	// Update through the same API.
	st.credValue = "{{SECRET_v8hq2kcn}}"
	a.saveCredential(ps, st)
	scope, _ := platform.ProjectScopeKey(ps.project.File.ID, root)
	key, _ := platform.ScopedSecretKey(scope, "apiToken")
	if got, _ := drv.count(key); got != "{{SECRET_v8hq2kcn}}" {
		t.Fatalf("credential not updated: %q", got)
	}

	// Delete with confirmation.
	a.requestDeleteCredential(st, "apiToken")
	if st.credConfirmName != "apiToken" {
		t.Fatal("delete needs an explicit confirmation state")
	}
	a.confirmDeleteCredential(ps, st)
	if st.credConfirmName != "" {
		t.Fatal("confirmation state not cleared")
	}
	if _, ok := drv.count(key); ok {
		t.Fatal("scoped credential still present after delete")
	}
	a.refreshCredentialEntries(ps, st, true)
	if st.credEntries[0].Status.State != core.SecretStateMissing {
		t.Fatalf("deleted credential should be missing: %+v", st.credEntries[0])
	}

	// Backend unavailable -> failure with no plaintext fallback anywhere.
	drv.mu.Lock()
	drv.err = errors.ErrUnsupported
	drv.mu.Unlock()
	st.credName = "apiToken"
	st.credValue = "SHOULD-NOT-BE-WRITTEN"
	st.notice = ""
	a.saveCredential(ps, st)
	if st.errMsg == "" || !strings.Contains(st.errMsg, identityManagementMessagesEn.CredBackendFailed[:12]) {
		t.Fatalf("backend failure not reported: %q", st.errMsg)
	}
	if st.credValue == "" {
		t.Fatal("draft value should be retained on backend failure so user can retry")
	}
	drv.mu.Lock()
	_, present := drv.data[key]
	drv.mu.Unlock()
	if present {
		t.Fatal("backend failure still wrote a credential")
	}
	var leaks []string
	_ = filepath.WalkDir(filepath.Join(root, ".gorequest"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(path)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), "SHOULD-NOT-BE-WRITTEN") {
			leaks = append(leaks, path)
		}
		return nil
	})
	if len(leaks) != 0 {
		t.Fatalf("plaintext fallback wrote secret to project files: %v", leaks)
	}
}

func TestIdentityManagementSchemeDeleteReferenceAndConfirmation(t *testing.T) {
	withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Identity")
	st := identityMgmtFor(ps)
	if _, err := ps.project.CreateAuthScheme(&core.AuthScheme{ID: "shared", Type: "bearer", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	entry, err := ps.createRequestIn("", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.AuthMode = "ref"
	tab.AuthRefID = "shared"
	tab.Dirty = true
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}

	a.openIdentityManagement(tab, 0)
	a.selectAuthScheme(ps, st, "shared")
	st.deleteDlg = nil
	a.requestDeleteAuthScheme(ps, st)
	if st.deleteDlg == nil || len(st.deleteDlg.References) != 1 {
		t.Fatalf("reference diagnostic missing: %+v", st.deleteDlg)
	}

	// Render the dialog and click the confirmation button.
	tt := ui.NewTester(a.IdentityManagementView, 1280, 900)
	tt.Frame()
	if !tt.HasText(identityManagementMessagesEn.SchemeClearDelete) {
		t.Fatalf("delete confirmation not rendered; texts=%v", tt.Texts())
	}
	if err := tt.Click(identityManagementMessagesEn.SchemeClearDelete); err != nil {
		t.Fatalf("click clear+delete: %v", err)
	}
	if ps.project.AuthSchemes["shared"] != nil {
		t.Fatal("scheme still present in memory after delete")
	}
	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	if got := reopened.Requests[entry.Request.ID]; got == nil || got.Request.Auth == nil || got.Request.Auth.Mode != "none" {
		t.Fatalf("dangling ref not cleared to none: %+v", got)
	}
	_ = reopened
	// A dirty tab referencing the scheme blocks the clear-and-delete flow.
	if _, err := ps.project.CreateAuthScheme(&core.AuthScheme{ID: "dirty", Type: "bearer", Token: "t"}); err != nil {
		t.Fatal(err)
	}
	ps.project.Requests[entry.Request.ID].Request.Auth = &core.AuthRef{Mode: "ref", Ref: "dirty"}
	tab.Dirty = true
	a.selectAuthScheme(ps, st, "dirty")
	a.requestDeleteAuthScheme(ps, st)
	if len(st.deleteDlg.DirtyNames) != 1 {
		t.Fatalf("dirty buffer not diagnosed: %+v", st.deleteDlg)
	}
	a.confirmDeleteAuthScheme(ps, st)
	if !strings.Contains(st.errMsg, identityManagementMessagesEn.SchemeDirtyBlock[:12]) {
		t.Fatalf("dirty buffer not blocked: %q", st.errMsg)
	}
	if !ps.project.Store.Exists("auth/dirty.auth.yaml") {
		t.Fatal("scheme deleted despite dirty buffer")
	}
}

func TestProductionPolicyPromptIsOneShotAndExact(t *testing.T) {
	withIdentityFakeBackend(t)
	m, ff := newTestManager(t)
	var qWin *queuedWindow
	m.newWindow = func(s windowSpec) window {
		fw := ff.new(s).(*fakeWindow)
		qWin = &queuedWindow{fakeWindow: fw, updates: make(chan func(), 16)}
		return qWin
	}
	dir := writeProject(t, "Identity")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	entry, err := ps.createRequestIn("", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.Method = "POST"
	// A local httptest server is the ONLY network target. Marking the "dev"
	// environment as production still forces the one-shot confirmation, but an
	// approved send must reach this loopback server rather than any real public
	// host. The handler also acts as a tripwire: any request arriving here is
	// expected and counted; the test asserts an unconfirmed send never arrives.
	var hits atomic.Int32
	var gotMethod atomic.Value
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		gotMethod.Store(r.Method)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tab.URL = srv.URL + "/v1/orders"
	// Mark this exact existing environment as production in device-local
	// settings only. A shared project file never relaxes device policy.
	netSt := projectNetStateFor(ps, a)
	dev := netSt.netStore.DeviceSettings()
	dev.DevicePolicy.ProductionEnvs = []string{"dev"}
	if err := netSt.netStore.SaveDeviceSettings(dev); err != nil {
		t.Fatal(err)
	}
	st := identityMgmtFor(ps)
	envID := ps.currentEnv()

	// Default device policy confirms mutating production requests, even to a
	// loopback target, because the production guard is environment-based.
	if a.preflightTargetPolicy(tab) {
		t.Fatal("production POST should require confirmation")
	}
	if st.policy == nil || st.policy.Confirmation == nil {
		t.Fatalf("no exact-scope prompt recorded: %+v", st.policy)
	}
	prompt := st.policy
	if prompt.RequestID != entry.Request.ID || prompt.Method != "POST" || prompt.EnvID != envID {
		t.Fatalf("prompt scope wrong: %+v", prompt)
	}
	if prompt.URL != tab.URL {
		t.Fatalf("prompt target must be the local server, got %q", prompt.URL)
	}

	// Render the confirmation dialog, click through, and verify the token is one
	// shot and bound to the exact request.
	tt := ui.NewTester(a.IdentityManagementView, 1280, 900)
	tt.Frame()
	if !tt.HasText(identityManagementMessagesEn.PolicyTitle) {
		t.Fatalf("policy dialog not rendered; texts=%v", tt.Texts())
	}
	if err := tt.Click(identityManagementMessagesEn.PolicyConfirmSend); err != nil {
		t.Fatalf("click confirm: %v", err)
	}
	// The public send path runs the request on a background goroutine and
	// posts its result to this window's queue. Drain it on the test goroutine
	// so no later assertion races with a late UI update. With the wiring in
	// place the approved send consumes the exact token and reaches ONLY the
	// local httptest server.
	timer := time.NewTimer(10 * time.Second)
	defer timer.Stop()
	for tab.Sending {
		select {
		case fn := <-qWin.updates:
			fn()
		case <-timer.C:
			t.Fatalf("timed out draining the send result; Sending=%v RespErr=%q", tab.Sending, tab.RespErr)
		}
	}
	if st.policy != nil {
		t.Fatal("prompt not dismissed")
	}
	if tab.RespErr != "" {
		t.Fatalf("confirmed production send failed: %q", tab.RespErr)
	}
	if got := hits.Load(); got != 1 {
		t.Fatalf("confirmed send must hit the local server exactly once, got %d", got)
	}
	if got := gotMethod.Load(); got != "POST" {
		t.Fatalf("confirmed send used wrong method %v", got)
	}

	// Re-create a fresh one-shot token for the exact-scope assertions so the
	// test is independent of whether the integrator patch is applied.
	st.token, err = core.NewTargetConfirmation(entry.Request.ID, "POST", envID, tab.URL, []string{"production write"})
	if err != nil {
		t.Fatal(err)
	}
	token := st.token
	// Method, target or environment change must invalidate the token.
	if token.Consume(entry.Request.ID, "PUT", envID, tab.URL) {
		t.Fatal("token matched a different method")
	}
	if token.Consume(entry.Request.ID, "POST", envID, srv.URL+"/v1/orders?changed=1") {
		t.Fatal("token matched a different target")
	}
	if token.Consume(entry.Request.ID, "POST", "other-env", tab.URL) {
		t.Fatal("token matched a different environment")
	}
	// The right scope consumes it exactly once.
	if consumed, ok := a.authorizeConfirmedSend(tab); !ok || consumed == nil {
		t.Fatal("exact scope should consume the token")
	}
	if _, ok := a.authorizeConfirmedSend(tab); ok {
		t.Fatal("token must not be reusable")
	}
	if tabNetStateFor(tab).policyConfirmed {
		t.Fatal("confirmation must not be kept as a broad tab flag")
	}
	// A new send must re-evaluate policy rather than reuse a broad authorization.
	if a.preflightTargetPolicy(tab) {
		t.Fatal("new send must re-evaluate policy")
	}
	// Re-evaluation records a fresh pending prompt and must NOT have issued a
	// second network request: the earlier approval was one-shot only.
	if got := hits.Load(); got != 1 {
		t.Fatalf("unconfirmed re-send reached the network (%d hits)", got)
	}
}

func TestIdentityManagementDiagnosticsAndRenderIdle(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Identity")
	writeProjectFile(t, ps, "environments/dev.yaml", `schemaVersion: 1
id: dev
variables: {}
secrets:
  - missingSecret
`)
	writeProjectFile(t, ps, "collections/missing.request.yaml", `schemaVersion: 1
id: req.missing
name: Missing Ref
auth:
  ref: absent-scheme
http:
  method: GET
  url: https://example.com
`)
	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	ps.project = reopened
	st := identityMgmtFor(ps)
	a.openIdentityManagement(nil, 3)
	if len(st.diagItems) < 2 {
		t.Fatalf("expected missing ref + unbound secret diagnostics: %+v", st.diagItems)
	}
	tt := ui.NewTester(a.IdentityManagementView, 1280, 900)
	tt.Frame()
	tt.Advance(time.Minute)
	if tt.FramePending() {
		t.Fatal("management dialog must not continuously request animation frames")
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "{{SECRET_") {
			t.Fatalf("diagnostics leaked a secret placeholder value: %q", txt)
		}
	}
	_ = fmt.Sprintf("%v", drv)
}
