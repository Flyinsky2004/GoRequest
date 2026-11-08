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
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// FIX09 regression suite. Every test uses a temporary project directory and
// the in-memory fake OS credential backend (withIdentityFakeBackend); no real
// system keychain, global config, history or network (other than loopback
// httptest) is touched. Secret *values* are represented by {{SECRET_...}}
// sentinel strings and must never appear in a shared project file, a UI text
// label, a diagnostic, or a log.

func TestSecretEntryMessagesCoverBothLanguages(t *testing.T) {
	zh := reflect.ValueOf(secretEntryMessagesZh)
	en := reflect.ValueOf(secretEntryMessagesEn)
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

// declareSecretEnv writes an environment that declares a secret name (and only
// its name) into the project, then reloads memory so the opened project sees it.
func declareSecretEnv(t *testing.T, a *App, ps *projectState, envID, name string) string {
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
	// Route both the dialog/management backend and the send path's secret
	// resolution through the same fake project-scoped keychain backend so the
	// FIX09 keychain driver suite tests keychain read/write/lock deterministically
	// without touching the real OS keychain.
	kc, err := platform.NewKeychainSecrets(identityKeychainDriverFactory(), reloaded.File.ID, ps.root)
	if err != nil {
		t.Fatal(err)
	}
	ps.secretKind = platform.SecretBackendKeychain
	ps.secretBackend = kc
	layered, err := platform.NewLayeredSecrets(kc, nil)
	if err != nil {
		t.Fatal(err)
	}
	ps.secrets = layered
	resetManagedCredentials(ps)
	a.mgr.secrets = kc
	return ps.root
}

func assertNoRealValueInProject(t *testing.T, root, secretValue string) {
	t.Helper()
	var leaks []string
	_ = filepath.WalkDir(filepath.Join(root, ".gorequest"), func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if strings.Contains(string(b), secretValue) {
			leaks = append(leaks, p)
		}
		return nil
	})
	if len(leaks) != 0 {
		t.Fatalf("secret real value leaked into shared project files: %v", leaks)
	}
}

// scopedValue reads back the fake OS store for one project/secret pair. It is a
// test-only assertion about the backend, never a UI read-back.
func scopedValue(t *testing.T, drv *memoryIdentityDriver, projectID, root, name string) (string, bool) {
	t.Helper()
	scope, err := platform.ProjectScopeKey(projectID, root)
	if err != nil {
		t.Fatal(err)
	}
	key, err := platform.ScopedSecretKey(scope, name)
	if err != nil {
		t.Fatal(err)
	}
	return drv.count(key)
}

// TestSecretEntryEnvDeclareSetSendReopenIsTheUserFlow is the end-to-end FIX09
// acceptance: a declared secret name -> click "Set value" in the environment
// editor -> type a real value into a password-only dialog backed by the fake
// system credential store -> a request carrying {{secret.name}} reaches a
// loopback httptest server with that exact value -> reopen shows the binding,
// and no shared project file ever contains the value.
func TestSecretEntryEnvDeclareSetSendReopen(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Fix09Flow")
	declareSecretEnv(t, a, ps, "dev", "apiToken")

	// Open the environment editor for "dev" and render it: the declared name
	// must show a discoverable "Set value" action right beside the name.
	ps.envSel = "dev"
	if err := ps.startEditEnvironmentWith(englishMessages); err != nil {
		t.Fatal(err)
	}
	m := a.secretEntryMessages()
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	if !tt.HasText(m.EnvSetValue) {
		t.Fatalf("no per-name set-value action in env editor; texts=%v", tt.Texts())
	}
	if !tt.HasText(m.EnvStatusMissing) {
		t.Fatalf("unbound status not shown; texts=%v", tt.Texts())
	}
	if !tt.HasText(m.EnvHint) {
		t.Fatalf("actionable secret hint not shown; texts=%v", tt.Texts())
	}

	// Click the row's "Set value" button in the rendered environment editor.
	// This proves the entry is genuinely discoverable from a declared name,
	// not just reachable through a controller helper.
	if err := tt.Click(m.EnvSetValue); err != nil {
		t.Fatalf("click env Set value button: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	d := secretEntryFor(ps)
	if !d.open || d.name != "apiToken" || !d.nameLocked {
		t.Fatalf("dialog not pre-filled/locked from the clicked row: %+v", d)
	}
	if d.value != "" {
		t.Fatal("dialog must start with an empty temporary value")
	}
	// Render: value input is password-only, status is non-sensitive, and the
	// real value is never displayed as text.
	if !tt.HasText(m.Title) || !tt.HasText("{{secret.apiToken}}") {
		t.Fatalf("value dialog not rendered; texts=%v", tt.Texts())
	}
	if tt.HasText(m.EnvStatusBound) {
		t.Fatal("must not claim a value is bound before it is set")
	}

	// Type the real value into the focused password-only input and click the
	// dialog's "Set value" primary button: the full UI path, not just the
	// controller. The password input must not echo the value as a text label.
	secretValue := "{{SECRET_09f1a2}}"
	tt.Type(secretValue)
	tt.Frame()
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, secretValue) {
			t.Fatalf("secret value echoed by password input: %q", txt)
		}
	}
	if err := tt.Click(m.SetValue); err != nil {
		t.Fatalf("click dialog Set value: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	if d.value != "" {
		t.Fatal("temporary plaintext was not cleared after save")
	}
	if d.status.State != core.SecretStateBound {
		t.Fatalf("binding status not refreshed to bound: %+v", d.status)
	}
	if v, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); !ok || v != secretValue {
		t.Fatalf("value not written to project-scoped fake system store: %q %v", v, ok)
	}
	if !ps.envUI.open {
		t.Fatal("saving a secret value must not discard the unsaved environment draft")
	}
	a.closeSecretEntry(ps)

	// Build a request whose Authorization header resolves {{secret.apiToken}}.
	entry, err := ps.createRequestIn("", "Orders")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.HeaderRows = []*KVRow{{ID: nextRowID(), Enabled: true, Name: "Authorization", Value: "Bearer {{secret.apiToken}}"}}
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	tab.URL = srv.URL + "/orders"
	tab.Dirty = true
	_, plan, _, err := a.buildTrackedTabPlan(nil, tab)
	if err != nil {
		t.Fatalf("build plan with secret: %v", err)
	}
	if _, err := core.Execute(nil, plan, nil); err != nil {
		t.Fatalf("send with {{secret.apiToken}}: %v", err)
	}
	if gotAuth != "Bearer "+secretValue {
		t.Fatalf("request did not use the locally set secret value: %q", gotAuth)
	}
	_ = entry

	// No real value may appear in any shared project file, and the value field
	// of an ordinary variable must never be mistaken for a secret value.
	ps.envUI.varRows = append(ps.envUI.varRows, &envVarRow{ID: nextRowID(), Key: "token", Value: "plain-ordinary-value"})
	env, err := ps.envUI.build(ps)
	if err != nil {
		t.Fatal(err)
	}
	if env.Variables["token"] != "plain-ordinary-value" {
		t.Fatal("ordinary variable value must be preserved verbatim")
	}
	if len(env.Secrets) != 1 || env.Secrets[0] != "apiToken" {
		t.Fatalf("secret declaration lost: %v", env.Secrets)
	}
	assertNoRealValueInProject(t, ps.root, secretValue)

	// Reopen: binding is visible from the non-sensitive status cache, and the
	// value is never read back or rendered.
	a.openSecretEntry(ps, "apiToken")
	if d.status.State != core.SecretStateBound {
		t.Fatalf("reopen did not show an existing binding: %+v", d.status)
	}
	tt.Frame()
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, secretValue) || strings.Contains(txt, "SECRET_09f1a2") {
			t.Fatalf("real value echoed in UI: %q", txt)
		}
	}
	if !tt.HasText(m.UpdateValue) {
		t.Fatalf("reopened dialog should offer Update local value; texts=%v", tt.Texts())
	}
	a.closeSecretEntry(ps)
	if d.value != "" {
		t.Fatal("temporary value must be cleared on close")
	}

	// The system credentials page sees the same binding (shared ManagedCredentials).
	st := identityMgmtFor(ps)
	a.refreshCredentialEntries(ps, st, true)
	found := false
	for _, c := range st.credEntries {
		if c.Name == "apiToken" && c.Status.State == core.SecretStateBound {
			found = true
		}
	}
	if !found {
		t.Fatalf("system credentials page does not show the binding: %+v", st.credEntries)
	}
}

// TestSecretEntryUpdateDeleteErrorsAndBackendFailure covers update, delete with
// confirmation, empty/invalid input, and an unavailable/locked backend with NO
// plaintext fallback.
// TestSecretEntryCredentialPageEntryAndSharedStatus proves the existing
// "System credentials" page also offers a per-name value entry, that it opens
// the same focused dialog with the name pre-filled, and that a value saved
// through the management page's existing password form refreshes the env
// editor's non-sensitive badge (shared ManagedCredentials store).
func TestSecretEntryCredentialPageEntryAndSharedStatus(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Fix09CredPage")
	declareSecretEnv(t, a, ps, "dev", "apiToken")
	sm := a.secretEntryMessages()
	st := identityMgmtFor(ps)
	a.openIdentityManagement(nil, 2)

	// Use the production App.View path (it wires IdentityManagementModals
	// exactly once). The test-only IdentityManagementView wrapper renders the
	// modals a second time, which would double every overlay in headless tests.
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	if !tt.HasText(sm.CredSetValue) {
		t.Fatalf("credentials page missing per-name value entry; texts=%v", tt.Texts())
	}
	if err := tt.Click(sm.CredSetValue); err != nil {
		t.Fatalf("click credentials Set value: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	d := secretEntryFor(ps)
	if !d.open || d.name != "apiToken" || !d.nameLocked {
		t.Fatalf("dialog not opened from credentials row with pre-filled name: %+v", d)
	}
	// The management modal is suppressed while the dialog owns the overlay:
	// exactly one value dialog, no stacked backdrops.
	titleCount := 0
	for _, txt := range tt.Texts() {
		if txt == sm.Title {
			titleCount++
		}
	}
	if titleCount != 1 {
		t.Fatalf("expected one overlay, got %d; texts=%v", titleCount, tt.Texts())
	}
	a.closeSecretEntry(ps)

	// Save through the management page's OWN existing form (not the dialog),
	// then confirm the value lives in the same project-scoped store.
	st.credName = "apiToken"
	st.credValue = "{{SECRET_fromCredPage}}"
	a.saveCredential(ps, st)
	if v, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); !ok || v != "{{SECRET_fromCredPage}}" {
		t.Fatalf("management page value not stored: %q %v", v, ok)
	}
	if st.credValue != "" {
		t.Fatal("management page must clear its own password draft")
	}
	// A freshly opened environment dialog observes the binding too: both
	// surfaces consume the same ManagedCredentials store.
	a.openSecretEntry(ps, "apiToken")
	if d.status.State != core.SecretStateBound {
		t.Fatalf("env dialog did not observe the management page value: %+v", d.status)
	}
	a.closeSecretEntry(ps)

	// The env editor's own per-name badge/label also refreshes after a value
	// written through the management page (shared generation counter).
	ps.envSel = "dev"
	if err := ps.startEditEnvironmentWith(englishMessages); err != nil {
		t.Fatal(err)
	}
	a.refreshEnvSecretStatuses(ps, ps.envUI)
	if st := ps.envUI.secretStatus["apiToken"]; st.State != core.SecretStateBound {
		t.Fatalf("env badge stale after management-page save: %+v", st)
	}
	assertNoRealValueInProject(t, ps.root, "{{SECRET_fromCredPage}}")
}

func TestSecretEntryUpdateDeleteAndErrors(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Fix09Errors")
	declareSecretEnv(t, a, ps, "dev", "apiToken")
	m := a.secretEntryMessages()

	a.openSecretEntry(ps, "apiToken")
	d := secretEntryFor(ps)
	// Empty value: clear error, no backend write.
	d.value = ""
	a.saveSecretEntry(ps)
	if d.errMsg != m.Empty {
		t.Fatalf("empty value not reported: %q", d.errMsg)
	}
	if _, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); ok {
		t.Fatal("empty value should not write a credential")
	}

	// First set.
	d.value = "{{SECRET_v1}}"
	a.saveSecretEntry(ps)
	if d.lastAction != "created" || d.status.State != core.SecretStateBound {
		t.Fatalf("first set failed: %+v", d)
	}

	// Update through the same dialog.
	d.value = "{{SECRET_v2}}"
	a.saveSecretEntry(ps)
	if d.lastAction != "updated" {
		t.Fatalf("second save should be an update, got %q", d.lastAction)
	}
	if v, _ := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); v != "{{SECRET_v2}}" {
		t.Fatalf("value not updated: %q", v)
	}
	if d.value != "" {
		t.Fatal("temporary value retained after update")
	}

	// Delete requires an explicit confirmation step.
	a.requestDeleteSecretEntry(ps)
	if !d.confirmDelete {
		t.Fatal("delete must ask for confirmation")
	}
	a.confirmDeleteSecretEntry(ps)
	if d.confirmDelete || d.lastAction != "deleted" {
		t.Fatalf("delete not applied: %+v", d)
	}
	if _, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); ok {
		t.Fatal("scoped credential still present after delete")
	}
	if d.status.State != core.SecretStateMissing {
		t.Fatalf("status should be missing after delete: %+v", d.status)
	}

	// Invalid name (namespace/brace injection) fails at the boundary without a
	// backend write. The dialog stays open with the typed value so the user can
	// correct the name and retry.
	a.openSecretEntry(ps, "bad/../name")
	d.value = "{{SECRET_shouldnotwrite}}"
	a.saveSecretEntry(ps)
	if !strings.Contains(d.errMsg, m.InvalidName[:6]) {
		t.Fatalf("invalid name not reported: %q", d.errMsg)
	}
	if !d.open {
		t.Fatal("a failed save must keep the dialog open for retry")
	}
	if d.value != "{{SECRET_shouldnotwrite}}" {
		t.Fatal("a failed save must keep the typed value for retry")
	}

	// Backend unavailable / locked: explicit error, dialog and retry input kept,
	// NO plaintext fallback into project files.
	drv.mu.Lock()
	drv.err = errors.ErrUnsupported
	drv.mu.Unlock()
	a.openSecretEntry(ps, "apiToken")
	d.value = "{{SECRET_locked}}"
	a.saveSecretEntry(ps)
	if !strings.Contains(d.errMsg, m.BackendFailed[:6]) {
		t.Fatalf("backend failure not reported: %q", d.errMsg)
	}
	if !d.open {
		t.Fatal("a backend failure must keep the dialog open for retry")
	}
	if d.value != "{{SECRET_locked}}" {
		t.Fatal("a backend failure must keep the typed value for retry")
	}
	if strings.Contains(d.errMsg, "{{SECRET_locked}}") || strings.Contains(d.status.Detail, "{{SECRET_locked}}") {
		t.Fatalf("backend error/status leaked the real value: %q / %q", d.errMsg, d.status.Detail)
	}
	if _, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); ok {
		t.Fatal("unavailable backend still stored a value")
	}
	assertNoRealValueInProject(t, ps.root, "{{SECRET_locked}}")
	assertNoRealValueInProject(t, ps.root, "{{SECRET_shouldnotwrite}}")
	// A locked backend is also reported as an unavailable status, never as a
	// misleading "not set".
	if d.status.State != core.SecretStateUnavailable {
		t.Fatalf("locked backend status should be unavailable: %+v", d.status)
	}
}

// TestSecretEntryCloneIsolation proves two clones of the same repo (same
// project ID, different roots) never share a real value, and that the
// environment editor dialog is bound to the project that opened it.
func TestSecretEntryCloneIsolation(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	m, _ := newTestManager(t)
	rootA := writeProject(t, "CloneA")
	rootB := writeProject(t, "CloneB")
	if err := m.openProject(rootA); err != nil {
		t.Fatal(err)
	}
	aA := m.workspaces[0]
	psA := aA.ps
	if err := m.openProject(rootB); err != nil {
		t.Fatal(err)
	}
	aB := m.workspaces[1]
	psB := aB.ps
	if psA == psB {
		t.Fatal("two projects must not share state")
	}
	for _, pair := range []struct {
		a   *App
		ps  *projectState
		env string
	}{{aA, psA, "dev"}, {aB, psB, "dev"}} {
		writeProjectFile(t, pair.ps, "environments/dev.yaml", `schemaVersion: 1
id: dev
variables: {}
secrets:
  - apiToken
`)
		reloaded, err := core.OpenProject(pair.ps.root)
		if err != nil {
			t.Fatal(err)
		}
		pair.ps.project = reloaded
		kc, err := platform.NewKeychainSecrets(drv, reloaded.File.ID, pair.ps.root)
		if err != nil {
			t.Fatal(err)
		}
		pair.ps.secretKind = platform.SecretBackendKeychain
		pair.ps.secretBackend = kc
		layered, err := platform.NewLayeredSecrets(kc, nil)
		if err != nil {
			t.Fatal(err)
		}
		pair.ps.secrets = layered
		resetManagedCredentials(pair.ps)
		pair.a.mgr.secrets = kc
	}

	// Set a value only in clone A.
	aA.openSecretEntry(psA, "apiToken")
	dA := secretEntryFor(psA)
	dA.value = "{{SECRET_cloneA}}"
	aA.saveSecretEntry(psA)

	scopedA, _ := platform.ProjectScopeKey(psA.project.File.ID, psA.root)
	scopedB, _ := platform.ProjectScopeKey(psB.project.File.ID, psB.root)
	if scopedA == scopedB {
		t.Fatal("clone scopes must differ")
	}
	if v, ok := scopedValue(t, drv, psA.project.File.ID, psA.root, "apiToken"); !ok || v != "{{SECRET_cloneA}}" {
		t.Fatalf("clone A value missing: %q %v", v, ok)
	}
	if _, ok := scopedValue(t, drv, psB.project.File.ID, psB.root, "apiToken"); ok {
		t.Fatal("clone B must not see clone A's value")
	}
	// Clone B's dialog reports its own unbound status, and opening A's dialog
	// cannot apply A's draft to B.
	aB.openSecretEntry(psB, "apiToken")
	dB := secretEntryFor(psB)
	if dB.status.State != core.SecretStateMissing {
		t.Fatalf("clone B should be unbound: %+v", dB.status)
	}
	if dA == dB {
		t.Fatal("dialog state must be per project")
	}
	if dA.value != "" {
		t.Fatal("clone A dialog must not retain plaintext")
	}
	assertNoRealValueInProject(t, psA.root, "{{SECRET_cloneA}}")
	assertNoRealValueInProject(t, psB.root, "{{SECRET_cloneA}}")
}

// TestSecretEntryPreservesEnvDraftFocusAndSingleOverlay verifies the two modal
// owners never stack, and that opening/closing the value dialog neither
// discards an unsaved environment draft nor switches the current project.
func TestSecretEntryPreservesEnvDraftAndSingleOverlay(t *testing.T) {
	withIdentityFakeBackend(t)
	m, _ := newTestManager(t)
	rootA := writeProject(t, "DraftA")
	rootB := writeProject(t, "DraftB")
	if err := m.openProject(rootA); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	ps.envSel = "dev"
	if err := ps.startEditEnvironmentWith(englishMessages); err != nil {
		t.Fatal(err)
	}
	ps.envUI.varRows = append(ps.envUI.varRows, &envVarRow{ID: nextRowID(), Key: "draftKey", Value: "unsaved-value"})
	ps.envUI.secretRows = append(ps.envUI.secretRows, &envSecretRow{ID: nextRowID(), Name: "apiToken"})
	snapshot := ps.envUI.signature()
	activeBefore := m.findWorkspace(ps.root)

	// With the dialog open, envEditorModal must yield (single overlay) and the
	// management modal must not render its own backdrop.
	a.openSecretEntry(ps, "apiToken")
	if !a.secretEntryActive(ps) {
		t.Fatal("secret dialog should be active")
	}
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	// A single frame must render the value dialog, not a second management
	// modal on top of it. count the dialog title: it must appear exactly once.
	titleCount := 0
	for _, txt := range tt.Texts() {
		if txt == a.secretEntryMessages().Title {
			titleCount++
		}
	}
	if titleCount != 1 {
		t.Fatalf("expected exactly one value dialog overlay, got %d; texts=%v", titleCount, tt.Texts())
	}
	// The unsaved environment draft is untouched.
	if ps.envUI == nil || !ps.envUI.open || ps.envUI.signature() != snapshot {
		t.Fatalf("environment draft changed while value dialog open: %+v", ps.envUI)
	}

	// Opening a second project while the dialog is open does not apply A's
	// draft to B, and A's dialog stays bound to A.
	if err := m.openProject(rootB); err != nil {
		t.Fatal(err)
	}
	psB := m.workspaces[1].ps
	if psB.envUI != nil {
		t.Fatal("A's environment draft must not leak into B")
	}
	if secretEntryFor(psB).open {
		t.Fatal("A's value dialog must not render for B")
	}
	_ = activeBefore

	// Closing the dialog clears the plaintext and returns to A's draft intact.
	secretEntryFor(ps).value = "{{SECRET_temp}}"
	a.closeSecretEntry(ps)
	d := secretEntryFor(ps)
	if d.open || d.value != "" {
		t.Fatalf("close did not clear temporary value: %+v", d)
	}
	if a.secretEntryActive(ps) {
		t.Fatal("dialog should be inactive after close")
	}
	if ps.envUI == nil || !ps.envUI.open || ps.envUI.signature() != snapshot {
		t.Fatal("closing the value dialog must not discard the env draft")
	}
}

// TestSecretEntryPasswordNotEchoed ensures the real value is never rendered as
// a UI text label (the input is Password()-only) and the management page's own
// value input remains password-masked.
func TestSecretEntryPasswordNotEchoed(t *testing.T) {
	withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "Fix09Mask")
	declareSecretEnv(t, a, ps, "dev", "apiToken")
	a.openSecretEntry(ps, "apiToken")
	d := secretEntryFor(ps)
	d.value = "{{SECRET_echo_check}}"
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "{{SECRET_echo_check}}") || strings.Contains(txt, "echo_check") {
			t.Fatalf("secret value echoed as UI text: %q", txt)
		}
	}
	// No real value in the environment editor's own texts either.
	a.closeSecretEntry(ps)
	ps.envSel = "dev"
	if err := ps.startEditEnvironmentWith(englishMessages); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "{{SECRET_echo_check}}") {
			t.Fatalf("secret value echoed in environment editor: %q", txt)
		}
	}
}

// TestSecretEntrySuccessfulSaveAutoClosesAndPreservesContext is the U28
// acceptance: a successful local-value write closes the dialog on its own (no
// "Cancel" click), clears the sensitive input, refreshes the non-sensitive
// status/caches, and leaves the unsaved environment draft and the active
// request exactly as they were.
func TestSecretEntrySuccessfulSaveAutoClosesAndPreservesContext(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "U28AutoClose")
	declareSecretEnv(t, a, ps, "dev", "apiToken")

	// An unsaved environment draft and an edited current request must survive
	// the dialog's auto-close.
	ps.envSel = "dev"
	if err := ps.startEditEnvironmentWith(englishMessages); err != nil {
		t.Fatal(err)
	}
	ps.envUI.varRows = append(ps.envUI.varRows, &envVarRow{ID: nextRowID(), Key: "draftKey", Value: "unsaved-value"})
	draftSig := ps.envUI.signature()
	if _, err := ps.createRequestIn("", "Orders"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = "http://127.0.0.1:9/unsaved-request"
	tab.Dirty = true

	m := a.secretEntryMessages()
	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	if err := tt.Click(m.EnvSetValue); err != nil {
		t.Fatalf("click env Set value: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	d := secretEntryFor(ps)
	if !d.open {
		t.Fatal("dialog did not open from the environment row")
	}
	secretValue := "{{SECRET_u28_autoclose}}"
	tt.Type(secretValue)
	tt.Frame()
	if err := tt.Click(m.SetValue); err != nil {
		t.Fatalf("click dialog Set value: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()

	// Success: the dialog closed itself, the sensitive input is gone, and the
	// binding status was refreshed from the store.
	if d.open {
		t.Fatal("successful save must close the dialog without a Cancel click")
	}
	if d.value != "" {
		t.Fatal("successful save must clear the temporary plaintext")
	}
	if d.status.State != core.SecretStateBound {
		t.Fatalf("binding status not refreshed after save: %+v", d.status)
	}
	if v, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); !ok || v != secretValue {
		t.Fatalf("value not written to the project-scoped fake store: %q %v", v, ok)
	}
	// The non-sensitive confirmation moves to the status line and never carries
	// the value.
	if want := fmt.Sprintf(m.SavedFmt, "apiToken"); a.status != want {
		t.Fatalf("status line = %q, want %q", a.status, want)
	}
	if strings.Contains(a.status, secretValue) {
		t.Fatal("status line leaked the secret value")
	}
	// The environment draft and the current request are untouched.
	if ps.envUI == nil || !ps.envUI.open || ps.envUI.signature() != draftSig {
		t.Fatal("auto-close must not discard the unsaved environment draft")
	}
	if ps.activeTab() != tab || tab.URL != "http://127.0.0.1:9/unsaved-request" || !tab.Dirty {
		t.Fatal("auto-close must not change the current request")
	}
	// The environment editor's non-sensitive badge refreshed to Bound, and the
	// value is not echoed anywhere in the UI.
	if !tt.HasText(m.EnvStatusBound) {
		t.Fatalf("env badge did not refresh after save; texts=%v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, secretValue) {
			t.Fatalf("secret value echoed after auto-close: %q", txt)
		}
	}
	assertNoRealValueInProject(t, ps.root, secretValue)
}

// TestSecretEntryFailureKeepsDialogAndRetryInput proves a failed write leaves
// the dialog open with the typed value intact, and that retrying once the
// backend recovers succeeds and then closes the dialog.
func TestSecretEntryFailureKeepsDialogAndRetryInput(t *testing.T) {
	drv := withIdentityFakeBackend(t)
	a, ps := openTestProject(t, "U28Retry")
	declareSecretEnv(t, a, ps, "dev", "apiToken")
	m := a.secretEntryMessages()

	a.openSecretEntry(ps, "apiToken")
	d := secretEntryFor(ps)
	drv.mu.Lock()
	drv.err = errors.ErrUnsupported
	drv.mu.Unlock()

	tt := ui.NewTester(a.View, 1280, 900)
	tt.Frame()
	secretValue := "{{SECRET_u28_retry}}"
	tt.Type(secretValue)
	tt.Frame()
	if err := tt.Click(m.SetValue); err != nil {
		t.Fatalf("click dialog Set value: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()

	// Failure: nothing written, dialog still open, retry input preserved.
	if !d.open {
		t.Fatal("a failed save must keep the dialog open")
	}
	if d.value != secretValue {
		t.Fatalf("a failed save must keep the retry input, got %q", d.value)
	}
	if !strings.Contains(d.errMsg, m.BackendFailed[:6]) {
		t.Fatalf("backend failure not reported: %q", d.errMsg)
	}
	if !tt.HasText(m.Title) {
		t.Fatalf("dialog disappeared after a failed save; texts=%v", tt.Texts())
	}
	if _, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); ok {
		t.Fatal("a failed save must not write a value")
	}

	// Backend recovers: the same dialog retries without retyping and closes.
	drv.mu.Lock()
	drv.err = nil
	drv.mu.Unlock()
	if err := tt.Click(m.SetValue); err != nil {
		t.Fatalf("retry click: %v; texts=%v", err, tt.Texts())
	}
	tt.Frame()
	if d.open {
		t.Fatal("a successful retry must close the dialog")
	}
	if d.value != "" {
		t.Fatal("a successful retry must clear the temporary plaintext")
	}
	if d.status.State != core.SecretStateBound {
		t.Fatalf("retry did not refresh the binding status: %+v", d.status)
	}
	if v, ok := scopedValue(t, drv, ps.project.File.ID, ps.root, "apiToken"); !ok || v != secretValue {
		t.Fatalf("retry did not store the value: %q %v", v, ok)
	}
	assertNoRealValueInProject(t, ps.root, secretValue)
}
