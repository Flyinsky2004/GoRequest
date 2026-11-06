package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// seedOAuthSchemeProject writes a project with one reusable oauth2 scheme so
// ref/inherit resolution has a real project-level scheme to find.
func seedOAuthSchemeProject(t *testing.T, schemeYAML string) string {
	t.Helper()
	dir := seedAuthFixtureProject(t)
	if err := os.WriteFile(filepath.Join(dir, ".gorequest", "auth", "corp-oauth.auth.yaml"), []byte(schemeYAML), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// TestResolveActiveOAuthSchemeHonorsCurrentMode guards the cross-card leak:
// a stale inline oauth2 scheme saved in Request.Auth must NOT be resurrected
// once the tab's current AuthMode is "none" (or another non-OAuth mode).
func TestResolveActiveOAuthSchemeHonorsCurrentMode(t *testing.T) {
	m, _ := newTestManager(t)
	dir := seedAuthFixtureProject(t)
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	if _, err := ps.createRequestIn("", "Leak Guard"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()

	stale := &core.AuthScheme{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            "stale-oauth",
		Type:          "oauth2",
		OAuth: &core.OAuthConfig{
			GrantType: core.OAuthGrantClientCredentials,
			ClientID:  "old-client",
			TokenURL:  "https://old.example.invalid/token",
		},
	}
	// Simulate a request file that still carries an old inline OAuth scheme.
	tab.Entry.Request.Auth = &core.AuthRef{Mode: "inline", Inline: stale}

	// (1) Explicit "none": no scheme may resolve, even with stale inline auth.
	tab.AuthMode = "none"
	tab.authPassthrough = nil
	if got, _ := a.resolveActiveOAuthScheme(ps, tab); got != nil {
		t.Fatalf("mode none resurrected stale inline OAuth scheme: %+v", got)
	}

	// (2) "bearer": likewise must not fall back to the stale OAuth scheme.
	tab.AuthMode = "bearer"
	if got, _ := a.resolveActiveOAuthScheme(ps, tab); got != nil {
		t.Fatalf("mode bearer resurrected stale inline OAuth scheme: %+v", got)
	}

	// (3) "ref" to a non-OAuth scheme: must not resolve the stale inline either.
	tab.AuthMode = "ref"
	tab.AuthRefID = "team-bearer"
	if got, _ := a.resolveActiveOAuthScheme(ps, tab); got != nil {
		t.Fatalf("mode ref resurrected stale inline OAuth scheme: %+v", got)
	}

	// (4) An explicit inline oauth2 mode DOES resolve its own scheme.
	tab.AuthMode = "oauth2"
	tab.authPassthrough = &core.AuthRef{Mode: "inline", Inline: stale}
	if got, id := a.resolveActiveOAuthScheme(ps, tab); got == nil || id != "stale-oauth" {
		t.Fatalf("inline oauth2 mode did not resolve own scheme: scheme=%+v id=%q", got, id)
	}
}

// TestRefOAuthEditorIsReadOnlyAndDoesNotMutateScheme verifies that viewing a
// ref/inherit OAuth scheme through the request Auth tab never presents an
// editable form, never marks the request Dirty, and never mutates the shared
// scheme. This prevents a phantom "save" that only changed in-memory state.
func TestRefOAuthEditorIsReadOnlyAndDoesNotMutateScheme(t *testing.T) {
	schemeYAML := `schemaVersion: 1
id: corp-oauth
type: oauth2
extensions:
  oauth:
    grantType: client_credentials
    tokenUrl: https://issuer.example.invalid/token
    clientId: corp-client
`
	m, _ := newTestManager(t)
	if err := m.openProject(seedOAuthSchemeProject(t, schemeYAML)); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	if _, err := ps.createRequestIn("", "Ref OAuth"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.AuthMode = "ref"
	tab.AuthRefID = "corp-oauth"
	tab.Dirty = false

	before := ps.project.AuthSchemes["corp-oauth"]
	if before == nil {
		t.Fatal("oauth scheme not loaded from project file")
	}
	beforeCfg, err := core.ExtractOAuthConfig(before)
	if err != nil {
		t.Fatal(err)
	}

	// Resolve does find the ref scheme (so the read-only panel can show it).
	if got, id := a.resolveActiveOAuthScheme(ps, tab); got == nil || id != "corp-oauth" {
		t.Fatalf("ref oauth scheme not resolved: scheme=%+v id=%q", got, id)
	}

	tt := ui.NewTester(func(c *ui.Context) {
		a.renderAuthEditor(c, c.Theme(), tab)
	}, 1200, 800)
	tt.Frame()

	// The read-only notice is displayed, and no editable flow controls appear.
	if !tt.HasText(oauthReadOnlyNotice(a.oauthLocale())) {
		t.Fatalf("read-only notice not rendered; texts=%v", tt.Texts())
	}
	om := oauthMsg(a.oauthLocale())
	if tt.HasText(om.BtnGetTokenClientCreds) || tt.HasText(om.BtnFetchDiscovery) {
		t.Fatalf("read-only editor exposed interactive OAuth controls; texts=%v", tt.Texts())
	}
	if tab.Dirty {
		t.Fatal("read-only OAuth view marked the request Dirty")
	}
	afterCfg, err := core.ExtractOAuthConfig(ps.project.AuthSchemes["corp-oauth"])
	if err != nil {
		t.Fatal(err)
	}
	if beforeCfg.ClientID != afterCfg.ClientID || beforeCfg.TokenURL != afterCfg.TokenURL {
		t.Fatalf("read-only view mutated the shared scheme: before=%+v after=%+v", beforeCfg, afterCfg)
	}
}

// TestSchemeDraftPreservesOAuthFields guards against the B05 scheme editor
// coercing an oauth2/oidc scheme to bearer and dropping its OAuthConfig.
func TestSchemeDraftPreservesOAuthFields(t *testing.T) {
	cfg := &core.OAuthConfig{
		GrantType:        core.OAuthGrantClientCredentials,
		ClientID:         "corp-client",
		ClientSecret:     "{{secret.oauthClientSecret}}",
		TokenURL:         "https://issuer.example.invalid/token",
		Scopes:           []string{"orders.read"},
		ClientAuthMethod: core.OAuthClientAuthBasic,
	}
	s := &core.AuthScheme{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            "corp-oauth",
		Type:          "oauth2",
		OAuth:         cfg,
	}

	d := draftFromScheme(s)
	if d.Type != "oauth2" {
		t.Fatalf("oauth2 scheme coerced to %q by draftFromScheme", d.Type)
	}
	if !d.Unsupported {
		t.Fatal("oauth2 scheme draft not marked read-only")
	}
	if d.OAuth == nil || d.OAuth.ClientID != cfg.ClientID || d.OAuth.TokenURL != cfg.TokenURL {
		t.Fatalf("OAuthConfig lost in draft: %+v", d.OAuth)
	}

	// A save attempt through the read-only draft must be refused rather than
	// silently writing a bearer scheme with no OAuth fields.
	if got, err := d.toScheme(); err == nil {
		t.Fatalf("read-only oauth draft produced a scheme anyway: %+v", got)
	}

	// Editing an ordinary bearer scheme still works unchanged.
	bearer := &core.AuthScheme{SchemaVersion: core.SupportedSchemaVersion, ID: "team", Type: "bearer", Token: "t"}
	bd := draftFromScheme(bearer)
	if bd.Type != "bearer" || bd.Unsupported {
		t.Fatalf("bearer draft regressed: %+v", bd)
	}
	bs, err := bd.toScheme()
	if err != nil || bs.Token != "t" {
		t.Fatalf("bearer round-trip broke: scheme=%+v err=%v", bs, err)
	}
}

// TestOAuthModesAreSelectableAndRoundTrip verifies that oauth2/oidc are
// first-class request modes (not only reachable by importing a file) and that
// an inline OAuth scheme survives save/reload without losing its config.
func TestOAuthModesAreSelectableAndRoundTrip(t *testing.T) {
	for _, mode := range []string{"oauth2", "oidc"} {
		if !containsString(requestAuthModes, mode) {
			t.Fatalf("requestAuthModes missing %q: %v", mode, requestAuthModes)
		}
	}

	m, _ := newTestManager(t)
	if err := m.openProject(seedAuthFixtureProject(t)); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	if _, err := ps.createRequestIn("", "Inline OAuth"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.AuthMode = "oauth2"
	tab.Dirty = true

	// The editor creates the inline scheme on the tab.
	tt := ui.NewTester(func(c *ui.Context) {
		a.renderAuthEditor(c, c.Theme(), tab)
	}, 1200, 800)
	tt.Frame()
	if tab.authPassthrough == nil || tab.authPassthrough.Inline == nil {
		t.Fatal("oauth2 mode did not create an inline auth scheme on the tab")
	}
	if got := strings.ToLower(strings.TrimSpace(tab.authPassthrough.Inline.Type)); got != "oauth2" {
		t.Fatalf("inline scheme type = %q, want oauth2", got)
	}

	// Commit a config and verify it is persisted with the request, then reloads
	// as an oauth2 tab with the same OAuth fields.
	st := getOrCreateOAuthTabState(tab)
	st.mu.Lock()
	st.SchemeType = "oauth2"
	st.GrantType = core.OAuthGrantClientCredentials
	st.ClientID = "roundtrip-client"
	st.TokenURL = "https://issuer.example.invalid/token"
	st.ClientAuthMethod = core.OAuthClientAuthBasic
	st.UseToken = core.OAuthUseAccessToken
	st.mu.Unlock()
	a.commitOAuthConfigToScheme(ps, tab, st)
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatalf("save inline oauth request: %v", err)
	}

	raw, err := os.ReadFile(filepath.Join(ps.project.Dir, tab.Entry.RelPath))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "oauth2") || !strings.Contains(string(raw), "roundtrip-client") {
		t.Fatalf("oauth config not persisted in request file: %s", raw)
	}
	// Exactly one serialized representation: the native auth.oauth field. A
	// duplicate extensions.oauth would be rejected by the strict loader.
	if n := strings.Count(string(raw), "roundtrip-client"); n != 1 {
		t.Fatalf("oauth config serialized %d times (want 1): %s", n, raw)
	}

	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	ps.project = reopened
	entry := reopened.Requests[tab.Entry.Request.ID]
	if entry == nil {
		keys := make([]string, 0, len(reopened.Requests))
		for k := range reopened.Requests {
			keys = append(keys, k)
		}
		t.Fatalf("request %q missing after reopen; keys=%v", tab.Entry.Request.ID, keys)
	}
	reloaded := &Tab{}
	fillAuth(reopened, reloaded, entry.Request)
	if reloaded.AuthMode != "oauth2" || reloaded.authPassthrough == nil || reloaded.authPassthrough.Inline == nil {
		t.Fatalf("oauth2 mode lost on reload: mode=%q ref=%+v", reloaded.AuthMode, reloaded.authPassthrough)
	}
	cfg, err := core.ExtractOAuthConfig(reloaded.authPassthrough.Inline)
	if err != nil || cfg == nil || cfg.ClientID != "roundtrip-client" {
		t.Fatalf("oauth config lost on reload: cfg=%+v err=%v", cfg, err)
	}
}

// TestSchemeEditorOAuthIsReadOnly verifies the UI shows the real oauth type
// and the read-only notice, and does not offer the save/convert button.
func TestSchemeEditorOAuthIsReadOnly(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(seedOAuthSchemeProject(t, `schemaVersion: 1
id: corp-oauth
type: oauth2
extensions:
  oauth:
    grantType: client_credentials
    tokenUrl: https://issuer.example.invalid/token
    clientId: corp-client
`)); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	st := identityMgmtFor(ps)
	a.openIdentityManagement(nil, 0)
	a.selectAuthScheme(ps, st, "corp-oauth")
	if st.draft.Type != "oauth2" || !st.draft.Unsupported {
		t.Fatalf("scheme editor did not load oauth scheme read-only: %+v", st.draft)
	}
	if st.draft.OAuth == nil || st.draft.OAuth.ClientID != "corp-client" {
		t.Fatalf("scheme editor lost OAuth config: %+v", st.draft.OAuth)
	}

	tt := ui.NewTester(func(c *ui.Context) {
		a.renderSchemeSection(c, c.Theme(), ps, st)
	}, 1280, 900)
	tt.Frame()
	if !tt.HasText(schemeReadOnlyNotice(a.identityLangCode())) {
		t.Fatalf("scheme read-only notice not rendered; texts=%v", tt.Texts())
	}
	// The type Select must not offer a bearer conversion for this draft.
	if tt.HasText(identityManagementMessagesEn.SchemeSave) {
		t.Fatalf("read-only scheme still exposed a save action; texts=%v", tt.Texts())
	}

	// The on-disk scheme is untouched: no phantom save through this editor.
	onDisk := readProjectFile(t, ps, "auth/corp-oauth.auth.yaml")
	if !strings.Contains(onDisk, "oauth2") || !strings.Contains(onDisk, "corp-client") {
		t.Fatalf("oauth scheme file changed unexpectedly: %s", onDisk)
	}
}
