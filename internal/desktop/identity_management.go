package desktop

import (
	"fmt"
	"net/url"
	"path"
	"runtime"
	"sort"
	"strings"
	"sync"
	"weak"

	"gorequest/internal/core"
	"gorequest/internal/platform"
)

// Identity management UI state for one project. It lives in a weak-keyed table
// (no global strong reference to UI objects) exactly like the B04 network state:
// closing the project window releases the state with the projectState.
type identityManagementState struct {
	open    bool
	section int // 0 schemes, 1 folder, 2 credentials, 3 diagnostics

	// Reusable scheme editor.
	selectedID string
	isNew      bool
	draft      authSchemeDraft
	baseline   string
	deleteDlg  *schemeDeleteDialog

	// Folder editor.
	folderRel      string
	folderMode     string
	folderRef      string
	folderToken    string
	folderUser     string
	folderPass     string
	folderKeyName  string
	folderKeyIn    string
	folderKeyValue string
	folderBaseline string
	folderConfirm  bool
	folderImpact   []core.IdentityReference
	folderDirty    []string

	// Credential editor.
	credName        string
	credValue       string
	credConfirmName string
	credEntries     []credentialView
	credEntriesKey  string

	// Secret storage location (U18). storageChoice is the label shown in the
	// in-window dropdown; switchPlan is the reviewable copy plan and is cleared
	// as soon as the user cancels or the switch completes.
	storageChoice string
	switchPlan    *platform.SecretSwitchPlan
	switchPolicy  platform.ConflictPolicy
	switchErr     string
	switchNotice  string

	// Diagnostics.
	diagKey   string
	diagItems []core.IdentityDiagnostic

	errMsg string
	notice string

	// Production target confirmation (one-shot, exact scope).
	policy *policyPrompt
	token  *core.TargetConfirmation
}

type authSchemeDraft struct {
	SchemaVersion int
	ID            string
	Type          string
	Token         string
	Username      string
	Password      string
	Name          string
	In            string
	Value         string

	// OAuth is carried unchanged through an edit session. The B05 scheme
	// editor only knows bearer/basic/apikey fields; silently coercing an
	// oauth2/oidc scheme to bearer would drop its OAuthConfig on the next
	// save. Keeping the original config intact while presenting the scheme
	// read-only avoids that data loss without inventing a new OAuth editor.
	OAuth       *core.OAuthConfig
	Unsupported bool
}

type schemeDeleteDialog struct {
	ID         string
	Baseline   string
	References []core.IdentityReference
	DirtyNames []string
}

type credentialView struct {
	Name    string
	Sources []string
	Status  core.SecretStatus
}

type policyPrompt struct {
	Tab          *Tab
	RequestID    string
	Method       string
	EnvID        string
	URL          string
	Reasons      string
	Confirmation *core.TargetConfirmation
}

var (
	identityMgmtMu     sync.Mutex
	identityMgmtByWeak = map[weak.Pointer[projectState]]*identityManagementState{}

	// identityKeychainDriverFactory is replaceable in tests so credential
	// management never touches the host's real keychain during automated runs.
	identityKeychainDriverFactory = func() platform.KeychainDriver {
		return platform.NewMyGoKeychainDriver("")
	}

	// managedCredentialsByWeak keeps the project-scoped managed credential
	// store (and its observed names) alive only as long as its projectState.
	// A weak key avoids a global strong reference to UI state.
	managedCredsMu     sync.Mutex
	managedCredsByWeak = map[weak.Pointer[projectState]]*platform.ManagedCredentials{}
)

func identityMgmtFor(ps *projectState) *identityManagementState {
	identityMgmtMu.Lock()
	defer identityMgmtMu.Unlock()
	if ps == nil {
		return &identityManagementState{}
	}
	key := weak.Make(ps)
	if st, ok := identityMgmtByWeak[key]; ok && st != nil {
		return st
	}
	st := &identityManagementState{
		section:     0,
		folderMode:  "inherit",
		folderKeyIn: "header",
		draft:       authSchemeDraft{Type: "bearer", In: "header"},
	}
	identityMgmtByWeak[key] = st
	runtime.AddCleanup(ps, func(k weak.Pointer[projectState]) {
		identityMgmtMu.Lock()
		delete(identityMgmtByWeak, k)
		identityMgmtMu.Unlock()
	}, key)
	return st
}

// openIdentityManagement opens the dialog on a section for the project that owns
// tab (or the active project when tab is nil).
func (a *App) openIdentityManagement(tab *Tab, section int) {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	if ps == nil || ps.project == nil {
		return
	}
	st := identityMgmtFor(ps)
	st.open = true
	st.section = section
	st.errMsg = ""
	st.notice = ""
	switch section {
	case 0:
		a.refreshSchemeEditor(ps, st)
	case 1:
		a.openFolderAuthEditor(ps, st, tab)
	case 2:
		a.probeSecretBackend(ps)
		a.resetStorageUI(ps, st)
		a.refreshCredentialEntries(ps, st, true)
	case 3:
		a.refreshDiagnostics(ps, st, true)
	}
}

// --- reusable auth schemes -------------------------------------------------

func draftFromScheme(s *core.AuthScheme) authSchemeDraft {
	d := authSchemeDraft{SchemaVersion: core.SupportedSchemaVersion}
	if s == nil {
		return d
	}
	d.SchemaVersion = s.SchemaVersion
	d.ID = s.ID
	d.Type = strings.ToLower(strings.TrimSpace(s.Type))
	switch d.Type {
	case "basic":
		d.Username, d.Password = s.Username, s.Password
	case "apikey":
		d.Name, d.In, d.Value = s.Name, core.NormalizeAPIKeyLocation(s.In), s.Value
	case "bearer":
		d.Token = s.Token
	case "oauth2", "oidc":
		// Preserve the real type and the OAuth configuration, and mark the
		// draft unsupported so the editor does not offer a bearer conversion.
		if cfg, err := core.ExtractOAuthConfig(s); err == nil {
			d.OAuth = cfg
		}
		d.Unsupported = true
	default:
		// Unknown future types: keep them read-only too rather than coercing
		// them into bearer and dropping their fields.
		d.Unsupported = true
	}
	return d
}

func (d *authSchemeDraft) toScheme() (*core.AuthScheme, error) {
	if d.Unsupported {
		return nil, fmt.Errorf("auth type %q is read-only in the scheme editor", d.Type)
	}
	s := &core.AuthScheme{
		SchemaVersion: d.SchemaVersion,
		ID:            strings.TrimSpace(d.ID),
		Type:          strings.ToLower(strings.TrimSpace(d.Type)),
	}
	switch s.Type {
	case "bearer":
		s.Token = d.Token
	case "basic":
		s.Username = d.Username
		s.Password = d.Password
	case "apikey":
		s.Name = strings.TrimSpace(d.Name)
		in, err := core.ValidateAPIKeyLocation(d.In)
		if err != nil {
			return nil, err
		}
		s.In = in
		s.Value = d.Value
	default:
		return nil, fmt.Errorf("unsupported auth type %q", d.Type)
	}
	if err := core.ValidateAuthSchemeID(s.ID); err != nil {
		return nil, err
	}
	return s, nil
}

func (a *App) refreshSchemeEditor(ps *projectState, st *identityManagementState) {
	if st.isNew {
		return
	}
	if st.selectedID == "" {
		infos := ps.project.ListAuthSchemes()
		if len(infos) == 0 {
			st.draft = authSchemeDraft{Type: "bearer", In: "header", SchemaVersion: core.SupportedSchemaVersion}
			st.baseline = ""
			return
		}
		st.selectedID = infos[0].ID
	}
	s, digest, err := ps.project.LoadAuthScheme(st.selectedID)
	if err != nil {
		st.errMsg = err.Error()
		return
	}
	st.draft = draftFromScheme(s)
	st.baseline = digest
	st.errMsg = ""
}

func (a *App) beginNewAuthScheme(ps *projectState, st *identityManagementState) {
	st.isNew = true
	st.selectedID = ""
	st.baseline = ""
	st.draft = authSchemeDraft{Type: "bearer", In: "header", SchemaVersion: core.SupportedSchemaVersion}
	st.errMsg = ""
	st.notice = ""
}

func (a *App) selectAuthScheme(ps *projectState, st *identityManagementState, id string) {
	st.isNew = false
	st.selectedID = id
	st.errMsg = ""
	st.notice = ""
	a.refreshSchemeEditor(ps, st)
}

func (a *App) saveSchemeDraft(ps *projectState, st *identityManagementState) {
	scheme, err := st.draft.toScheme()
	if err != nil {
		st.errMsg = a.identityManagementMsg().SchemeValidation + " (" + err.Error() + ")"
		return
	}
	if st.isNew {
		digest, err := ps.project.CreateAuthScheme(scheme)
		if err != nil {
			if core.IsConflict(err) {
				st.errMsg = a.identityManagementMsg().SchemeConflict
			} else {
				st.errMsg = err.Error()
			}
			return
		}
		st.isNew = false
		st.selectedID = scheme.ID
		st.baseline = digest
		st.draft = draftFromScheme(scheme)
		st.notice = a.identityManagementMsg().SchemeCreated
		st.errMsg = ""
		return
	}
	digest, err := ps.project.SaveAuthScheme(scheme, st.baseline)
	if err != nil {
		if core.IsConflict(err) {
			st.errMsg = a.identityManagementMsg().SchemeConflict
		} else {
			st.errMsg = err.Error()
		}
		return
	}
	st.baseline = digest
	st.draft = draftFromScheme(scheme)
	st.notice = a.identityManagementMsg().SchemeSaved
	st.errMsg = ""
}

func (a *App) requestDeleteAuthScheme(ps *projectState, st *identityManagementState) {
	if st.isNew || strings.TrimSpace(st.selectedID) == "" {
		return
	}
	id := strings.TrimSpace(st.selectedID)
	refs := ps.project.AuthSchemeReferences(id)
	st.deleteDlg = &schemeDeleteDialog{
		ID:         id,
		Baseline:   st.baseline,
		References: refs,
		DirtyNames: dirtyRequestNamesReferencing(ps, id),
	}
	st.errMsg = ""
}

func (a *App) confirmDeleteAuthScheme(ps *projectState, st *identityManagementState) {
	dlg := st.deleteDlg
	if dlg == nil {
		return
	}
	if len(dlg.DirtyNames) > 0 {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().SchemeDirtyBlock, strings.Join(dlg.DirtyNames, ", "))
		return
	}
	refs, err := ps.project.DeleteAuthScheme(dlg.ID, dlg.Baseline, len(dlg.References) > 0)
	if err != nil {
		if core.IsConflict(err) {
			st.errMsg = a.identityManagementMsg().SchemeConflict
		} else {
			st.errMsg = err.Error()
		}
		return
	}
	_ = refs
	st.deleteDlg = nil
	st.selectedID = ""
	st.isNew = false
	st.baseline = ""
	st.notice = a.identityManagementMsg().SchemeDeleted
	st.errMsg = ""
	// Open clean request tabs that referenced the scheme now hold an explicit
	// none on disk; refresh them so a later save cannot resurrect a dangling ref.
	a.refreshTabsAfterSchemeDelete(ps, dlg.ID)
	a.refreshSchemeEditor(ps, st)
}

// dirtyRequestNamesReferencing returns dirty tab titles whose effective or
// declared auth references id.
func dirtyRequestNamesReferencing(ps *projectState, id string) []string {
	var names []string
	for _, tab := range ps.tabs {
		if tab == nil || tab.Kind != "request" || !tab.Dirty || tab.Entry == nil || tab.Entry.Request == nil {
			continue
		}
		if tab.AuthMode == "ref" && strings.TrimSpace(tab.AuthRefID) == id {
			names = append(names, tab.Title)
			continue
		}
		if tab.AuthMode == "inherit" || tab.AuthMode == "" {
			if eff, err := ps.project.ResolveEffectiveAuth(tab.Entry, nil); err == nil && eff != nil && eff.RefID == id {
				names = append(names, tab.Title)
				continue
			}
		}
		if tab.Entry.Request.Auth != nil && tab.Entry.Request.Auth.Mode == "ref" &&
			strings.TrimSpace(tab.Entry.Request.Auth.Ref) == id {
			names = append(names, tab.Title)
		}
	}
	sort.Strings(names)
	return names
}

func (a *App) refreshTabsAfterSchemeDelete(ps *projectState, id string) {
	for _, tab := range ps.tabs {
		if tab == nil || tab.Kind != "request" || tab.Dirty || tab.Entry == nil || tab.Entry.Request == nil {
			continue
		}
		auth := tab.Entry.Request.Auth
		if auth == nil || auth.Mode != "ref" || strings.TrimSpace(auth.Ref) != id {
			continue
		}
		ps.reloadRequestTab(tab)
	}
}

// --- folder auth -----------------------------------------------------------

func folderRelForTab(tab *Tab) string {
	if tab == nil || tab.Entry == nil || tab.Entry.RelPath == "" {
		return ""
	}
	dir := path.Dir(tab.Entry.RelPath)
	if dir == "collections" || dir == "." {
		return ""
	}
	return collectionRelOf(dir)
}

func (a *App) openFolderAuthEditor(ps *projectState, st *identityManagementState, tab *Tab) {
	rel := st.folderRel
	if rel == "" {
		rel = folderRelForTab(tab)
	}
	if rel == "" {
		rel = ps.selectedFolder()
	}
	st.folderRel = rel
	st.folderConfirm = false
	st.folderDirty = nil
	if rel == "" {
		st.errMsg = "select a folder to edit its auth"
		st.folderBaseline = ""
		st.folderMode = "inherit"
		st.folderRef = ""
		return
	}
	st.folderBaseline = ps.project.FolderAuthBaseline(rel)
	st.folderImpact = ps.project.FolderAuthImpact(rel)
	auth := ps.project.FolderAuth(rel)
	switch {
	case auth == nil || auth.Mode == "" || auth.Mode == "inherit":
		st.folderMode = "inherit"
	case auth.Mode == "none":
		st.folderMode = "none"
	case auth.Mode == "ref":
		st.folderMode = "ref"
		st.folderRef = auth.Ref
	case auth.Mode == "inline":
		st.folderMode = "inline"
		if auth.Inline != nil {
			switch strings.ToLower(strings.TrimSpace(auth.Inline.Type)) {
			case "basic":
				st.folderMode = "inline-basic"
				st.folderUser, st.folderPass = auth.Inline.Username, auth.Inline.Password
			case "apikey":
				st.folderMode = "inline-apikey"
				st.folderKeyName = auth.Inline.Name
				st.folderKeyIn = core.NormalizeAPIKeyLocation(auth.Inline.In)
				st.folderKeyValue = auth.Inline.Value
			default:
				st.folderMode = "inline-bearer"
				st.folderToken = auth.Inline.Token
			}
		}
	}
	st.errMsg = ""
}

func (a *App) folderAuthFromDraft(st *identityManagementState) (*core.AuthRef, error) {
	switch st.folderMode {
	case "", "inherit":
		return nil, nil
	case "none":
		return &core.AuthRef{Mode: "none"}, nil
	case "ref":
		ref := strings.TrimSpace(st.folderRef)
		if ref == "" {
			return nil, fmt.Errorf("folder auth ref requires a scheme id")
		}
		return &core.AuthRef{Mode: "ref", Ref: ref}, nil
	case "inline-bearer":
		return &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{SchemaVersion: core.SupportedSchemaVersion, Type: "bearer", Token: st.folderToken}}, nil
	case "inline-basic":
		return &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{SchemaVersion: core.SupportedSchemaVersion, Type: "basic", Username: st.folderUser, Password: st.folderPass}}, nil
	case "inline-apikey":
		loc, err := core.ValidateAPIKeyLocation(st.folderKeyIn)
		if err != nil {
			return nil, err
		}
		return &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{
			SchemaVersion: core.SupportedSchemaVersion, Type: "apikey",
			Name: strings.TrimSpace(st.folderKeyName), In: loc, Value: st.folderKeyValue,
		}}, nil
	}
	return nil, fmt.Errorf("unsupported folder auth mode %q", st.folderMode)
}

func (a *App) requestSaveFolderAuth(ps *projectState, st *identityManagementState, clear bool) {
	if st.folderRel == "" {
		return
	}
	auth := (*core.AuthRef)(nil)
	if !clear {
		var err error
		auth, err = a.folderAuthFromDraft(st)
		if err != nil {
			st.errMsg = err.Error()
			return
		}
	}
	dirty := dirtyTabsInFolder(ps, st.folderRel)
	if len(dirty) > 0 && !st.folderConfirm {
		st.folderConfirm = true
		st.folderDirty = dirty
		return
	}
	digest, err := ps.project.SaveFolderAuthPreserving(st.folderRel, auth, st.folderBaseline)
	if err != nil {
		if core.IsConflict(err) {
			st.errMsg = a.identityManagementMsg().FolderConflict
		} else {
			st.errMsg = err.Error()
		}
		return
	}
	st.folderBaseline = digest
	st.folderConfirm = false
	st.folderDirty = nil
	st.folderImpact = ps.project.FolderAuthImpact(st.folderRel)
	if clear {
		st.folderMode = "inherit"
		st.notice = a.identityManagementMsg().FolderCleared
	} else {
		st.notice = a.identityManagementMsg().FolderSaved
	}
	st.errMsg = ""
	// Refresh clean tabs' auth fields so their inherit diagnostics reflect the
	// new folder state immediately.
	for _, tab := range ps.tabs {
		if tab != nil && tab.Kind == "request" && !tab.Dirty && (tab.AuthMode == "inherit" || tab.AuthMode == "") {
			tab.authDiagKey = ""
			tab.authDiagItems = nil
		}
	}
}

func (a *App) reloadFolderAuth(ps *projectState, st *identityManagementState) {
	if st.folderRel == "" {
		return
	}
	if _, err := ps.project.LoadFolderIdentities(); err != nil {
		st.errMsg = err.Error()
		return
	}
	a.openFolderAuthEditor(ps, st, nil)
}

func dirtyTabsInFolder(ps *projectState, folderRel string) []string {
	rel := collectionRelOf(folderRel)
	if rel == "" {
		return nil
	}
	prefix := "collections/" + rel + "/"
	var names []string
	for _, tab := range ps.tabs {
		if tab == nil || tab.Kind != "request" || !tab.Dirty || tab.Entry == nil {
			continue
		}
		if strings.HasPrefix(tab.Entry.RelPath, prefix) {
			names = append(names, tab.Title)
		}
	}
	sort.Strings(names)
	return names
}

// --- credentials -----------------------------------------------------------

// managedCredentials returns the project-scoped ManagedCredentials store for
// the backend the user selected. The driver factory can inject an in-memory
// backend in tests; no plaintext files are involved unless the user explicitly
// chose the local-file backend.
func (a *App) managedCredentials(ps *projectState) (*platform.ManagedCredentials, error) {
	if ps == nil || ps.project == nil || ps.project.File == nil {
		return nil, fmt.Errorf("no project")
	}
	managedCredsMu.Lock()
	defer managedCredsMu.Unlock()
	key := weak.Make(ps)
	if mc, ok := managedCredsByWeak[key]; ok && mc != nil {
		return mc, nil
	}
	var (
		mc  *platform.ManagedCredentials
		err error
	)
	if ps.secretBackend != nil {
		var manifest *platform.ManifestStore
		if a.mgr != nil && ps.secretScope != "" {
			manifest = a.mgr.secretStoreFor().Manifest(ps.secretScope)
		}
		mc, err = platform.NewManagedCredentialsWithBackend(ps.secretBackend, manifest)
	} else {
		// Legacy fallback for a projectState built without manager wiring.
		mc, err = platform.NewManagedCredentials(identityKeychainDriverFactory(), ps.project.File.ID, ps.root)
	}
	if err != nil {
		return nil, err
	}
	managedCredsByWeak[key] = mc
	runtime.AddCleanup(ps, func(k weak.Pointer[projectState]) {
		managedCredsMu.Lock()
		delete(managedCredsByWeak, k)
		managedCredsMu.Unlock()
	}, key)
	return mc, nil
}

// resetManagedCredentials drops the cached store so the next call rebuilds it
// against the newly selected backend.
func resetManagedCredentials(ps *projectState) {
	if ps == nil {
		return
	}
	managedCredsMu.Lock()
	delete(managedCredsByWeak, weak.Make(ps))
	managedCredsMu.Unlock()
}

// --- secret storage location ----------------------------------------------

// secretBackendInfo describes the project's live backend without reading any
// secret value. It is safe to call every frame.
func (a *App) secretBackendInfo(ps *projectState) platform.SecretBackendInfo {
	if ps != nil && ps.secretBackend != nil {
		return ps.secretBackend.Info()
	}
	return platform.SecretBackendInfo{
		Kind:   platform.SecretBackendKeychain,
		Label:  "System Keychain",
		Detail: "no secret backend configured",
	}
}

func (a *App) secretBackendKind(ps *projectState) platform.SecretBackendKind {
	if ps != nil && ps.secretKind.Valid() {
		return ps.secretKind
	}
	return platform.SecretBackendKeychain
}

// probeSecretBackend asks the live backend to confirm availability. It is
// called on explicit user actions only, so a locked or unsupported OS store can
// show its reason without probing every frame.
func (a *App) probeSecretBackend(ps *projectState) {
	if ps == nil || ps.secretBackend == nil {
		return
	}
	if prober, ok := ps.secretBackend.(platform.SecretBackendProber); ok {
		prober.Probe()
	}
}

func (a *App) secretBackendLabel(kind platform.SecretBackendKind) string {
	m := a.identityManagementMsg()
	if kind == platform.SecretBackendFile {
		return m.StorageFile
	}
	return m.StorageKeychain
}

// secretBackendStatusLabel localizes the backend string carried by a
// non-sensitive SecretStatus ("keychain"/"file"/"env"/"memory").
func (a *App) secretBackendStatusLabel(backend string) string {
	switch platform.SecretBackendKind(backend) {
	case platform.SecretBackendFile:
		return a.identityManagementMsg().StorageFile
	case platform.SecretBackendKeychain:
		return a.identityManagementMsg().StorageKeychain
	}
	return backend
}

func (a *App) secretBackendKindForLabel(label string) platform.SecretBackendKind {
	if label == a.identityManagementMsg().StorageFile {
		return platform.SecretBackendFile
	}
	return platform.SecretBackendKeychain
}

// resetStorageUI clears the transient switch plan when the credentials section
// is (re)opened, so a stale plan from another project or an older state never
// appears as if it were current.
func (a *App) resetStorageUI(ps *projectState, st *identityManagementState) {
	if st == nil {
		return
	}
	st.storageChoice = a.secretBackendLabel(a.secretBackendKind(ps))
	st.switchPlan = nil
	st.switchPolicy = platform.ConflictKeepTarget
	st.switchErr = ""
	st.switchNotice = ""
}

// resetSecretCaches drops every non-sensitive status cache that depends on the
// selected backend. Called after a successful switch for every window sharing
// the project scope; values are re-inspected on the next render, never read
// back for display.
func (a *App) resetSecretCaches() {
	ps := a.ps
	if ps == nil {
		return
	}
	resetManagedCredentials(ps)
	secretEntryMu.Lock()
	if d, ok := secretEntryByWeak[weak.Make(ps)]; ok && d != nil {
		d.generation++
		d.status = core.SecretStatus{}
		d.lastAction = ""
		d.notice = ""
	}
	secretEntryMu.Unlock()
	if ps.envUI != nil {
		ps.envUI.secretStatusKey = ""
	}
	st := identityMgmtFor(ps)
	st.credEntriesKey = ""
	st.diagKey = ""
	for _, tab := range ps.tabs {
		if tab != nil {
			tab.authDiagKey = ""
			tab.authDiagItems = nil
		}
	}
}

func secretReferenceNames(ps *projectState) []string {
	if ps == nil || ps.project == nil {
		return nil
	}
	refs := ps.project.SecretReferences(nil)
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	return names
}

func (a *App) secretBackendForKind(ps *projectState, kind platform.SecretBackendKind) (platform.SecretBackend, error) {
	if a.mgr == nil || ps == nil || ps.project == nil || ps.project.File == nil {
		return nil, fmt.Errorf("no project secret store")
	}
	return a.mgr.secretStoreFor().Backend(ps.secretScope, ps.project.File.ID, ps.root, kind)
}

// beginSecretSwitch pre-reads the source, lists every covered name, and builds
// the reviewable plan. Nothing is written here.
func (a *App) beginSecretSwitch(ps *projectState, st *identityManagementState, kind platform.SecretBackendKind) {
	if ps == nil || st == nil {
		return
	}
	m := a.identityManagementMsg()
	st.switchErr = ""
	st.switchNotice = ""
	st.switchPlan = nil
	if kind == a.secretBackendKind(ps) {
		st.switchErr = m.StorageSame
		return
	}
	source := ps.secretBackend
	if source == nil {
		st.switchErr = m.StorageNoBackend
		return
	}
	a.probeSecretBackend(ps)
	target, err := a.secretBackendForKind(ps, kind)
	if err != nil {
		st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error())
		return
	}
	if prober, ok := target.(platform.SecretBackendProber); ok {
		prober.Probe()
	}
	var managed []string
	if a.mgr != nil && ps.secretScope != "" {
		names, err := a.mgr.secretStoreFor().Manifest(ps.secretScope).Names()
		if err != nil {
			// Without the manifest we cannot know which undeclared names were
			// managed; refusing is safer than silently under-copying.
			st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error())
			return
		}
		managed = names
	}
	plan, err := platform.BuildSecretSwitchPlan(ps.secretScope, source, target, secretReferenceNames(ps), managed)
	if err != nil {
		st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error())
		return
	}
	st.switchPlan = &plan
	st.switchPolicy = platform.ConflictKeepTarget
}

// confirmSecretSwitch copies and verifies every value, then persists the
// choice and refreshes the live providers. Any failure leaves the previous
// backend selected and rolls back the target writes made this round.
func (a *App) confirmSecretSwitch(ps *projectState, st *identityManagementState) {
	if ps == nil || st == nil || st.switchPlan == nil {
		return
	}
	m := a.identityManagementMsg()
	plan := *st.switchPlan
	source := ps.secretBackend
	target, err := a.secretBackendForKind(ps, plan.Target.Kind)
	if err != nil {
		st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error())
		st.switchPlan = nil
		return
	}
	result, err := platform.ExecuteSecretSwitch(source, target, plan, st.switchPolicy)
	if err != nil {
		st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error())
		st.switchPlan = nil
		return
	}
	if a.mgr == nil {
		st.switchErr = m.StorageNoBackend
		st.switchPlan = nil
		return
	}
	store := a.mgr.secretStoreFor()
	if err := store.SetSelectedKind(ps.secretScope, plan.Target.Kind); err != nil {
		if rbErr := result.Rollback(); rbErr != nil {
			st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error()+" / "+rbErr.Error())
		} else {
			st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, err.Error())
		}
		st.switchPlan = nil
		return
	}
	if err := a.mgr.refreshSecretBackend(ps.secretScope, plan.Target.Kind); err != nil {
		// The preference was written but the live provider could not switch:
		// restore the old preference, roll the target back, and rebuild the old
		// backend so the running app keeps its previous behavior.
		_ = store.SetSelectedKind(ps.secretScope, plan.Source.Kind)
		rbErr := result.Rollback()
		_ = a.mgr.refreshSecretBackend(ps.secretScope, plan.Source.Kind)
		detail := err.Error()
		if rbErr != nil {
			detail += " / " + rbErr.Error()
		}
		st.switchErr = fmt.Sprintf(m.StorageSwitchFailedFmt, detail)
		st.switchPlan = nil
		return
	}
	st.switchPlan = nil
	st.switchErr = ""
	st.switchNotice = fmt.Sprintf(m.StorageSwitchedFmt, a.secretBackendLabel(plan.Target.Kind), len(result.Copied))
	st.storageChoice = a.secretBackendLabel(plan.Target.Kind)
}

func (a *App) refreshCredentialEntries(ps *projectState, st *identityManagementState, force bool) {
	key := fmt.Sprintf("%d", len(ps.project.AuthSchemes)) + "\x1f" + ps.currentEnv()
	if !force && st.credEntriesKey == key {
		return
	}
	st.credEntriesKey = key
	refs := ps.project.SecretReferences(nil)
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		names = append(names, ref.Name)
	}
	mc, err := a.managedCredentials(ps)
	if err != nil {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().CredBackendFailed, err.Error())
		return
	}
	for _, name := range names {
		_ = mc.Observe(name)
	}
	records, err := mc.List(names)
	if err != nil {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().CredBackendFailed, err.Error())
		return
	}
	byName := map[string]credentialView{}
	for _, ref := range refs {
		byName[ref.Name] = credentialView{Name: ref.Name, Sources: ref.Sources}
	}
	entries := make([]credentialView, 0, len(records))
	for _, rec := range records {
		view := byName[rec.Name]
		view.Name = rec.Name
		view.Status = core.SecretStatus{Name: rec.Name, State: rec.State, Backend: rec.Backend, Detail: rec.Detail}
		entries = append(entries, view)
	}
	st.credEntries = entries
}

func (a *App) saveCredential(ps *projectState, st *identityManagementState) {
	name := strings.TrimSpace(st.credName)
	value := st.credValue
	if name == "" || value == "" {
		st.errMsg = a.identityManagementMsg().CredEmpty
		return
	}
	mc, err := a.managedCredentials(ps)
	if err != nil {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().CredBackendFailed, err.Error())
		return
	}
	if _, err := mc.Set(name, value); err != nil {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().CredBackendFailed, err.Error())
		return
	}
	st.credValue = ""
	st.notice = a.identityManagementMsg().CredSaved
	st.errMsg = ""
	// FIX09: the environment editor caches non-sensitive binding status per
	// declared name. Bump the shared generation so its "Set / Update local
	// value" badges refresh after a value written through this page too (both
	// surfaces use the same project-scoped ManagedCredentials store).
	secretEntryFor(ps).generation++
	a.refreshCredentialEntries(ps, st, true)
	a.invalidateIdentityDiagnostics(ps)
}

func (a *App) requestDeleteCredential(st *identityManagementState, name string) {
	st.credConfirmName = strings.TrimSpace(name)
	st.errMsg = ""
}

func (a *App) confirmDeleteCredential(ps *projectState, st *identityManagementState) {
	name := strings.TrimSpace(st.credConfirmName)
	if name == "" {
		return
	}
	mc, err := a.managedCredentials(ps)
	if err != nil {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().CredBackendFailed, err.Error())
		return
	}
	if _, err := mc.Delete(name); err != nil {
		st.errMsg = fmt.Sprintf(a.identityManagementMsg().CredBackendFailed, err.Error())
		return
	}
	st.credConfirmName = ""
	st.notice = a.identityManagementMsg().CredDeleted
	st.errMsg = ""
	// FIX09: see saveCredential — keep the env editor's status cache in sync.
	secretEntryFor(ps).generation++
	a.refreshCredentialEntries(ps, st, true)
	a.invalidateIdentityDiagnostics(ps)
}

// --- diagnostics -----------------------------------------------------------

func diagCacheKey(ps *projectState) string {
	var b strings.Builder
	if ps != nil && ps.project != nil {
		fmt.Fprintf(&b, "%d|%d|%d|%s", len(ps.project.AuthSchemes), len(ps.project.Requests), len(ps.project.Issues), ps.currentEnv())
	}
	return b.String()
}

func (a *App) refreshDiagnostics(ps *projectState, st *identityManagementState, force bool) {
	key := diagCacheKey(ps)
	if !force && st.diagKey == key {
		return
	}
	st.diagKey = key
	st.diagItems = ps.project.IdentityDiagnostics(a.secretsProvider())
}

func (a *App) invalidateIdentityDiagnostics(ps *projectState) {
	st := identityMgmtFor(ps)
	st.diagKey = ""
}

func (a *App) openIdentityDiagnostics(tab *Tab) {
	a.openIdentityManagement(tab, 3)
}

// --- production target confirmation ---------------------------------------

// policyTargetURL resolves the tab's URL and enabled query pairs for policy
// evaluation. The result is used for matching only and must never be rendered
// without redaction.
func (a *App) policyTargetURL(tab *Tab) (string, *core.Redactor, error) {
	if tab == nil || tab.Owner == nil || tab.Owner.project == nil {
		return "", core.NewRedactor(), fmt.Errorf("no request tab")
	}
	ps := tab.Owner
	red := core.NewRedactor()
	vars, _, err := ps.project.ResolveVarsWith(tab.Entry, ps.currentEnv(), nil)
	if err != nil {
		return "", red, err
	}
	resolved, err := resolveWithRedactor(tab.URL, vars, a.secretsProvider(), red)
	if err != nil {
		return "", red, err
	}
	u, err := url.Parse(strings.TrimSpace(resolved))
	if err != nil {
		return "", red, err
	}
	q := u.Query()
	for _, row := range tab.QueryRows {
		if row == nil || row.isBlank() || !row.Enabled {
			continue
		}
		name, err := resolveWithRedactor(row.Name, vars, a.secretsProvider(), red)
		if err != nil {
			return "", red, err
		}
		value, err := resolveWithRedactor(row.Value, vars, a.secretsProvider(), red)
		if err != nil {
			return "", red, err
		}
		q.Add(name, value)
	}
	u.RawQuery = q.Encode()
	return u.String(), red, nil
}

// preflightTargetPolicy returns true when the send may proceed. When policy
// requires interactive confirmation it records a one-shot prompt for this exact
// request/method/target/environment and returns false.
func (a *App) preflightTargetPolicy(tab *Tab) bool {
	if tab == nil || tab.Owner == nil || tab.Owner.project == nil {
		return false
	}
	ps := tab.Owner
	resolved, red, err := a.policyTargetURL(tab)
	if err != nil {
		a.status = red.RedactError(err).Error()
		return false
	}
	st := projectNetStateFor(ps, a)
	dev := st.netStore.DeviceSettings()
	pol := core.MergeTargetPolicy(&dev.DevicePolicy, nil)
	requestID := ""
	if tab.Entry != nil && tab.Entry.Request != nil {
		requestID = tab.Entry.Request.ID
	}
	dec, err := core.EvaluateTarget(pol, core.TargetCheckInput{
		URL:            resolved,
		Method:         tab.Method,
		EnvID:          ps.currentEnv(),
		HasCredentials: tabHasEffectiveCredentials(ps.project, tab),
		Interactive:    true,
		Confirmed:      false,
	})
	if err != nil {
		a.status = red.RedactError(err).Error()
		return false
	}
	if dec != nil && dec.Outcome == core.DecisionConfirmRequired {
		conf, cerr := core.NewTargetConfirmation(requestID, tab.Method, ps.currentEnv(), resolved, dec.Reasons)
		if cerr != nil {
			a.status = red.RedactError(cerr).Error()
			return false
		}
		mst := identityMgmtFor(ps)
		mst.policy = &policyPrompt{
			Tab:          tab,
			RequestID:    requestID,
			Method:       strings.ToUpper(strings.TrimSpace(tab.Method)),
			EnvID:        ps.currentEnv(),
			URL:          resolved,
			Reasons:      red.RedactString(conf.ReasonsText()),
			Confirmation: conf,
		}
		return false
	}
	return true
}

// acceptPolicyPrompt stores the confirmation token after the user approves the
// exact prompt. A later send can consume it only when the request, method,
// target, and environment still match.
func (a *App) acceptPolicyPrompt(ps *projectState, st *identityManagementState) {
	if st == nil || st.policy == nil || st.policy.Confirmation == nil {
		return
	}
	st.token = st.policy.Confirmation
	st.policy = nil
}

func (a *App) cancelPolicyPrompt(st *identityManagementState) {
	st.policy = nil
}

// authorizeConfirmedSend consumes the one-shot token for tab immediately before
// execution and returns the matched TargetConfirmation. The returned token is
// valid only for the plan built from this exact tab state. Redirect hops cannot
// inherit its authorization because no broad tab flag is set.
func (a *App) authorizeConfirmedSend(tab *Tab) (*core.TargetConfirmation, bool) {
	if tab == nil || tab.Owner == nil {
		return nil, false
	}
	st := identityMgmtFor(tab.Owner)
	if st.token == nil || st.token.IsConsumed() {
		return nil, false
	}
	resolved, _, err := a.policyTargetURL(tab)
	if err != nil {
		return nil, false
	}
	requestID := ""
	if tab.Entry != nil && tab.Entry.Request != nil {
		requestID = tab.Entry.Request.ID
	}
	if !st.token.Consume(requestID, tab.Method, tab.Owner.currentEnv(), resolved) {
		st.token = nil
		return nil, false
	}
	token := st.token
	st.token = nil
	return token, true
}

// confirmedForPlan reports whether a one-shot token was issued and consumed for
// the exact plan URL (the initial target). It never authorizes a redirect hop:
// the match is against plan.URL, not a redirect destination.
func (a *App) confirmedForPlan(tab *Tab, plan *core.Plan, token *core.TargetConfirmation) bool {
	if tab == nil || tab.Owner == nil || plan == nil || token == nil || !token.IsConsumed() {
		return false
	}
	requestID := ""
	if tab.Entry != nil && tab.Entry.Request != nil {
		requestID = tab.Entry.Request.ID
	}
	// Bind the consumed token to the exact plan that Execute will see. A URL or
	// method changed between the confirmation and plan construction invalidates
	// it; redirects still carry Confirmed=false for redirected hops below.
	return token.MatchesOnConsumed(requestID, plan.Method, planURLWithQuery(plan), tab.Owner.currentEnv())
}

func planURLWithQuery(plan *core.Plan) string {
	if plan == nil {
		return ""
	}
	u, err := url.Parse(strings.TrimSpace(plan.URL))
	if err != nil || u == nil {
		return plan.URL
	}
	q := u.Query()
	for _, kv := range plan.Query {
		q.Add(kv[0], kv[1])
	}
	u.RawQuery = q.Encode()
	return u.String()
}

// clearSendConfirmation resets the per-send confirmed flag so it can never leak
// into a later send or redirect hop. The identity management flow never sets
// this broad flag; the helper is used by the public send wiring patch to clean
// up any legacy state before attaching exact-scope confirmation.
func (a *App) clearSendConfirmation(tab *Tab) {
	if tab != nil {
		tabNetStateFor(tab).policyConfirmed = false
	}
}

// prepareConfirmedSend is the exact integration entry point for the public
// trySend/send path. It returns nil when the send may proceed (or a new prompt
// must be shown), and a one-shot token when the user already approved this exact
// target. The caller must set opts.Confirmed only for the first hop and keep
// redirects' Confirmed=false; see docs/task-results/B05-UI-integration.patch.
func (a *App) prepareConfirmedSend(tab *Tab) *core.TargetConfirmation {
	if tab == nil || tab.Owner == nil {
		return nil
	}
	a.clearSendConfirmation(tab)
	if token, ok := a.authorizeConfirmedSend(tab); ok {
		return token
	}
	if !a.preflightTargetPolicy(tab) {
		return nil
	}
	return nil
}

// attachConfirmedNetworkOptions sets Confirmed=true only when token exactly
// matches the plan that is about to execute. The supplied one-shot gate is
// consumed by core.Execute for Hop 0 only: any redirect hop is evaluated with
// Confirmed=false, so the initial approval never becomes a broad authorization.
func (a *App) attachConfirmedNetworkOptions(tab *Tab, plan *core.Plan, token *core.TargetConfirmation) {
	if plan == nil || plan.Network == nil {
		return
	}
	requestID := ""
	if tab != nil && tab.Entry != nil && tab.Entry.Request != nil {
		requestID = tab.Entry.Request.ID
	}
	envID := ""
	if tab != nil && tab.Owner != nil {
		envID = tab.Owner.currentEnv()
	}
	if token == nil {
		plan.Network.Confirmed = false
		return
	}
	gate := core.NewOneShotConfirmGate(token, requestID, envID)
	if core.SetPlanConfirmGate(plan, gate) == nil {
		// The transport patch is not applied yet: do not send with a broad
		// Confirmed flag. The exact-scope check remains authoritative.
		plan.Network.Confirmed = false
		return
	}
	plan.Network.Confirmed = false
}
