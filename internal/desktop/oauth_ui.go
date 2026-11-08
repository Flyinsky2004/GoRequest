package desktop

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/platform"
)

var (
	oauthGrantTypeOptions  = []string{core.OAuthGrantAuthorizationCode, core.OAuthGrantClientCredentials, core.OAuthGrantDeviceCode}
	oauthClientAuthOptions = []string{core.OAuthClientAuthBasic, core.OAuthClientAuthPost, core.OAuthClientAuthNone}
	oauthUseTokenOptions   = []string{core.OAuthUseAccessToken, core.OAuthUseIDToken}
	oauthSchemeTypeOptions = []string{"oauth2", "oidc"}
)

// oauthProjectManager owns the isolated OAuthService and ports for a single projectState.
// It is indexed by weak.Pointer[projectState] with runtime.AddCleanup and holds no strong pointer back to projectState or App.
type oauthProjectManager struct {
	mu            sync.Mutex
	service       *core.OAuthService
	store         core.OAuthTokenStore
	browser       core.BrowserOpener
	loopback      core.LoopbackListenerFactory
	httpDoer      core.OAuthHTTPDoer
	redactor      *core.Redactor
	syncExecution bool // When true (in deterministic headless UI tests), button actions run synchronously.
}

var (
	oauthProjMu       sync.Mutex
	oauthProjByWeak   = map[weak.Pointer[projectState]]*oauthProjectManager{}
	fallbackOAuthProj *oauthProjectManager

	oauthTabMu     sync.Mutex
	oauthTabByWeak = map[weak.Pointer[Tab]]*oauthTabUIState{}
)

func getOrCreateOAuthProjectManager(ps *projectState) *oauthProjectManager {
	oauthProjMu.Lock()
	defer oauthProjMu.Unlock()

	if ps == nil {
		if fallbackOAuthProj == nil {
			fallbackOAuthProj = newDefaultOAuthProjectManager()
		}
		return fallbackOAuthProj
	}

	key := weak.Make(ps)
	if mgr, ok := oauthProjByWeak[key]; ok && mgr != nil {
		return mgr
	}

	mgr := newDefaultOAuthProjectManager()
	oauthProjByWeak[key] = mgr
	runtime.AddCleanup(ps, func(k weak.Pointer[projectState]) {
		oauthProjMu.Lock()
		delete(oauthProjByWeak, k)
		oauthProjMu.Unlock()
	}, key)
	return mgr
}

func newDefaultOAuthProjectManager() *oauthProjectManager {
	red := core.NewRedactor()
	store := core.NewMemoryOAuthTokenStore()
	browser := platform.NewSystemBrowserOpener()
	loopback := platform.NewLocalLoopbackFactory()
	return &oauthProjectManager{
		service: core.NewOAuthService(core.OAuthServiceConfig{
			TokenStore:      store,
			BrowserOpener:   browser,
			LoopbackFactory: loopback,
		}),
		store:    store,
		browser:  browser,
		loopback: loopback,
		redactor: red,
	}
}

// ConfigureOAuthService overrides the OAuthService ports for a projectState (used by integration wiring or tests).
func (ps *projectState) ConfigureOAuthService(cfg core.OAuthServiceConfig, syncExec bool) *core.OAuthService {
	mgr := getOrCreateOAuthProjectManager(ps)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	if cfg.TokenStore != nil {
		mgr.store = cfg.TokenStore
	}
	if cfg.BrowserOpener != nil {
		mgr.browser = cfg.BrowserOpener
	}
	if cfg.LoopbackFactory != nil {
		mgr.loopback = cfg.LoopbackFactory
	}
	if cfg.HTTPDoer != nil {
		mgr.httpDoer = cfg.HTTPDoer
	}
	cfg.TokenStore = mgr.store
	cfg.BrowserOpener = mgr.browser
	cfg.LoopbackFactory = mgr.loopback
	cfg.HTTPDoer = mgr.httpDoer
	mgr.service = core.NewOAuthService(cfg)
	mgr.syncExecution = syncExec
	return mgr.service
}

// OAuthService returns the project's isolated *core.OAuthService.
func (ps *projectState) OAuthService() *core.OAuthService {
	mgr := getOrCreateOAuthProjectManager(ps)
	mgr.mu.Lock()
	defer mgr.mu.Unlock()
	return mgr.service
}

// oauthTabUIState holds the editable OAuth 2.0 / OIDC form draft and transient runtime status for a single Tab.
// Bound via weak.Pointer[Tab] + runtime.AddCleanup so closing a Tab releases its UI state automatically.
type oauthTabUIState struct {
	mu                     sync.Mutex
	LoadedKey              string
	SchemeID               string
	SchemeType             string // "oauth2" or "oidc"
	GrantType              string
	Issuer                 string
	DiscoveryURL           string
	AuthorizationURL       string
	TokenURL               string
	DeviceAuthorizationURL string
	RevocationURL          string
	JWKSURI                string
	RedirectURI            string
	ClientID               string
	ClientSecret           string
	ClientAuthMethod       string
	ScopesText             string
	Audience               string
	Resource               string
	UseToken               string

	// Transient runtime state — never serialized to shared YAML files.
	Busy                  bool
	StatusText            string
	ErrorText             string
	ActiveCancel          context.CancelFunc
	ActiveDeviceChallenge *core.DeviceAuthorizationChallenge
	DevicePollStatus      core.DevicePollStatus
	CachedRecord          *core.OAuthTokenRecord
}

func getOrCreateOAuthTabState(tab *Tab) *oauthTabUIState {
	if tab == nil {
		return &oauthTabUIState{
			SchemeType:       "oauth2",
			GrantType:        core.OAuthGrantAuthorizationCode,
			ClientAuthMethod: core.OAuthClientAuthBasic,
			UseToken:         core.OAuthUseAccessToken,
		}
	}
	oauthTabMu.Lock()
	defer oauthTabMu.Unlock()

	key := weak.Make(tab)
	if st, ok := oauthTabByWeak[key]; ok && st != nil {
		return st
	}

	st := &oauthTabUIState{
		SchemeType:       "oauth2",
		GrantType:        core.OAuthGrantAuthorizationCode,
		ClientAuthMethod: core.OAuthClientAuthBasic,
		UseToken:         core.OAuthUseAccessToken,
	}
	oauthTabByWeak[key] = st
	runtime.AddCleanup(tab, func(k weak.Pointer[Tab]) {
		oauthTabMu.Lock()
		if s, exists := oauthTabByWeak[k]; exists && s != nil {
			s.mu.Lock()
			if s.ActiveCancel != nil {
				s.ActiveCancel()
			}
			s.mu.Unlock()
		}
		delete(oauthTabByWeak, k)
		oauthTabMu.Unlock()
	}, key)
	return st
}

func (a *App) oauthLocale() string {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return "en"
	}
	return "zh-CN"
}

// resolveActiveOAuthScheme locates the AuthScheme being edited on tab (either project-level scheme or inline auth).
//
// The tab's CURRENT AuthMode is authoritative. In particular, "none" (and any
// other non-OAuth mode) must never fall back to a stale inline OAuth scheme left
// in the saved Request.Auth: silently resurrecting it would leak old credentials
// and contradict the user's explicit choice. Only oauth2/oidc inline, ref to an
// oauth2/oidc scheme, and inherit of an oauth2/oidc scheme are resolved.
func (a *App) resolveActiveOAuthScheme(ps *projectState, tab *Tab) (*core.AuthScheme, string) {
	if ps == nil && tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	if ps == nil && a != nil {
		ps = a.ps
	}
	var proj *core.Project
	if ps != nil {
		proj = ps.project
	}
	if tab == nil {
		return nil, ""
	}

	mode := strings.ToLower(strings.TrimSpace(tab.AuthMode))
	switch mode {
	case "oauth2", "oidc":
		if tab.authPassthrough != nil && tab.authPassthrough.Inline != nil {
			id := strings.TrimSpace(tab.authPassthrough.Inline.ID)
			if id == "" {
				id = "inline:" + strings.TrimSpace(tab.Title)
			}
			return tab.authPassthrough.Inline, id
		}
		// An OAuth mode loaded from disk always carries its inline scheme in
		// authPassthrough (see fillAuth). No saved-Entry fallback here: that is
		// precisely the stale-inline leak this guard prevents.
	case "ref":
		if proj != nil && proj.AuthSchemes != nil {
			refID := strings.TrimSpace(tab.AuthRefID)
			if s := proj.AuthSchemes[refID]; s != nil {
				st := strings.ToLower(strings.TrimSpace(s.Type))
				if st == "oauth2" || st == "oidc" {
					return s, refID
				}
			}
		}
	case "inherit":
		if proj != nil {
			if eff, err := proj.ResolveEffectiveAuth(tab.Entry, &core.Request{}); err == nil && eff != nil && eff.Scheme != nil {
				st := strings.ToLower(strings.TrimSpace(eff.Scheme.Type))
				if st == "oauth2" || st == "oidc" {
					id := eff.RefID
					if id == "" {
						id = eff.Scheme.ID
					}
					return eff.Scheme, id
				}
			}
		}
	}
	return nil, ""
}

// ensureInlineOAuthScheme creates or returns the inline oauth2/oidc AuthScheme on tab when mode is "oauth2" or "oidc".
func ensureInlineOAuthScheme(tab *Tab, schemeType string) (*core.AuthScheme, string) {
	if tab == nil {
		return nil, ""
	}
	if schemeType != "oidc" {
		schemeType = "oauth2"
	}
	tab.AuthMode = schemeType
	if tab.authPassthrough == nil || tab.authPassthrough.Inline == nil {
		id := tab.authInlineID
		if strings.TrimSpace(id) == "" {
			id = "inline-oauth"
		}
		ver := tab.authInlineVer
		if ver == 0 {
			ver = core.SupportedSchemaVersion
		}
		tab.authPassthrough = &core.AuthRef{
			Mode: "inline",
			Inline: &core.AuthScheme{
				SchemaVersion: ver,
				ID:            id,
				Type:          schemeType,
			},
		}
	} else {
		tab.authPassthrough.Mode = "inline"
		tab.authPassthrough.Inline.Type = schemeType
	}
	return tab.authPassthrough.Inline, tab.authPassthrough.Inline.ID
}

// syncOAuthTabStateFromScheme loads the AuthScheme's OAuthConfig into oauthTabUIState without triggering any network call.
func (a *App) syncOAuthTabStateFromScheme(ps *projectState, tab *Tab, st *oauthTabUIState) *core.AuthScheme {
	scheme, schemeID := a.resolveActiveOAuthScheme(ps, tab)
	if scheme == nil {
		mode := ""
		if tab != nil {
			mode = strings.ToLower(strings.TrimSpace(tab.AuthMode))
		}
		if mode == "oauth2" || mode == "oidc" {
			scheme, schemeID = ensureInlineOAuthScheme(tab, mode)
		}
	}
	if scheme == nil {
		return nil
	}

	loadKey := fmt.Sprintf("%p|%s|%s", scheme, schemeID, scheme.Type)
	st.mu.Lock()
	defer st.mu.Unlock()
	if st.LoadedKey == loadKey {
		return scheme
	}

	cfg, err := core.ExtractOAuthConfig(scheme)
	if err != nil || cfg == nil {
		cfg = &core.OAuthConfig{}
	}
	st.LoadedKey = loadKey
	st.SchemeID = schemeID
	st.SchemeType = strings.ToLower(strings.TrimSpace(scheme.Type))
	if st.SchemeType != "oidc" {
		st.SchemeType = "oauth2"
	}
	st.GrantType = cfg.NormalizedGrantType()
	st.Issuer = cfg.Issuer
	st.DiscoveryURL = cfg.DiscoveryURL
	st.AuthorizationURL = cfg.AuthorizationURL
	st.TokenURL = cfg.TokenURL
	st.DeviceAuthorizationURL = cfg.DeviceAuthorizationURL
	st.RevocationURL = cfg.RevocationURL
	st.JWKSURI = cfg.JWKSURI
	st.RedirectURI = cfg.RedirectURI
	st.ClientID = cfg.ClientID
	st.ClientSecret = cfg.ClientSecret
	st.ClientAuthMethod = cfg.NormalizedClientAuthMethod()
	st.ScopesText = strings.Join(cfg.Scopes, " ")
	st.Audience = cfg.Audience
	st.Resource = cfg.Resource
	st.UseToken = cfg.NormalizedUseToken()

	// Load cached token from local isolated store (memory / keychain) without hitting the network.
	req := a.buildOAuthFlowRequestLocked(ps, tab, st, false)
	if rec, loadErr := ps.OAuthService().GetCachedToken(context.Background(), req); loadErr == nil {
		st.CachedRecord = rec
	}
	return scheme
}

func (st *oauthTabUIState) toOAuthConfigLocked() *core.OAuthConfig {
	var scopes []string
	for _, part := range strings.Fields(strings.ReplaceAll(st.ScopesText, ",", " ")) {
		if part != "" {
			scopes = append(scopes, part)
		}
	}
	return &core.OAuthConfig{
		GrantType:              st.GrantType,
		Issuer:                 strings.TrimSpace(st.Issuer),
		DiscoveryURL:           strings.TrimSpace(st.DiscoveryURL),
		AuthorizationURL:       strings.TrimSpace(st.AuthorizationURL),
		TokenURL:               strings.TrimSpace(st.TokenURL),
		DeviceAuthorizationURL: strings.TrimSpace(st.DeviceAuthorizationURL),
		RevocationURL:          strings.TrimSpace(st.RevocationURL),
		JWKSURI:                strings.TrimSpace(st.JWKSURI),
		RedirectURI:            strings.TrimSpace(st.RedirectURI),
		ClientID:               strings.TrimSpace(st.ClientID),
		ClientSecret:           strings.TrimSpace(st.ClientSecret),
		ClientAuthMethod:       st.ClientAuthMethod,
		Scopes:                 scopes,
		Audience:               strings.TrimSpace(st.Audience),
		Resource:               strings.TrimSpace(st.Resource),
		UseToken:               st.UseToken,
	}
}

func (a *App) commitOAuthConfigToScheme(ps *projectState, tab *Tab, st *oauthTabUIState) {
	scheme, _ := a.resolveActiveOAuthScheme(ps, tab)
	if scheme == nil && tab != nil {
		st.mu.Lock()
		stType := st.SchemeType
		st.mu.Unlock()
		scheme, _ = ensureInlineOAuthScheme(tab, stType)
	}
	if scheme == nil {
		return
	}
	st.mu.Lock()
	cfg := st.toOAuthConfigLocked()
	if st.SchemeType == "oidc" || st.SchemeType == "oauth2" {
		scheme.Type = st.SchemeType
		if tab != nil && (tab.AuthMode == "oauth2" || tab.AuthMode == "oidc") {
			tab.AuthMode = st.SchemeType
		}
	}
	st.LoadedKey = fmt.Sprintf("%p|%s|%s", scheme, st.SchemeID, scheme.Type)
	req := a.buildOAuthFlowRequestLocked(ps, tab, st, false)
	if rec, err := ps.OAuthService().GetCachedToken(context.Background(), req); err == nil {
		st.CachedRecord = rec
	}
	st.mu.Unlock()

	core.SetOAuthConfig(scheme, cfg)
	if tab != nil {
		tab.Dirty = true
		if tab.Entry != nil && tab.Entry.Request != nil && (tab.AuthMode == "oauth2" || tab.AuthMode == "oidc") {
			tab.Entry.Request.Auth = core.CloneAuthRef(tab.authPassthrough)
		}
	}
}

func (a *App) buildOAuthFlowRequestLocked(ps *projectState, tab *Tab, st *oauthTabUIState, interactive bool) core.OAuthFlowRequest {
	if ps == nil && tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	if ps == nil && a != nil {
		ps = a.ps
	}

	cfg := st.toOAuthConfigLocked()
	mgr := getOrCreateOAuthProjectManager(ps)
	red := mgr.redactor
	if red == nil {
		red = core.NewRedactor()
	}

	var projectRoot, projectSlug, envID string
	var vars map[string]string
	var secrets core.SecretProvider
	var netOpts core.ExecuteOptions

	if a != nil {
		secrets = a.secretsProvider()
	} else if ps != nil {
		secrets = ps.secrets
	}

	if ps != nil {
		projectRoot = ps.root
		projectSlug = ps.name()
		envID = ps.currentEnv()
		if ps.project != nil {
			if tab != nil && tab.Entry != nil && tab.Entry.Request != nil {
				vars, _, _ = ps.project.ResolveVars(tab.Entry, envID)
			} else {
				var projVars, envVars map[string]string
				if ps.project.File != nil {
					projVars = ps.project.File.Variables
				}
				if envID != "" && ps.project.Environments != nil && ps.project.Environments[envID] != nil {
					envVars = ps.project.Environments[envID].Variables
				}
				vars = core.MergeVars(projVars, envVars)
			}
		}
	}

	var reqModel *core.Request
	if tab != nil && tab.Entry != nil {
		reqModel = tab.Entry.Request
	}
	if a != nil {
		netOpts = a.buildExecuteOptionsForTab(ps, tab, reqModel, nil, red)
	} else if ps != nil {
		netOpts = (*App)(nil).buildExecuteOptionsForTab(ps, tab, reqModel, nil, red)
	}

	schemeID := st.SchemeID
	if schemeID == "" {
		schemeID = "default"
	}
	return core.OAuthFlowRequest{
		ProjectRoot:     projectRoot,
		ProjectSlug:     projectSlug,
		SchemeID:        schemeID,
		SchemeType:      st.SchemeType,
		Config:          *cfg,
		Vars:            vars,
		Secrets:         secrets,
		Network:         netOpts,
		TargetPolicy:    netOpts.EffectiveTargetPolicy(),
		HasTargetPolicy: netOpts.HasTargetPolicy,
		EnvID:           envID,
		Confirmed:       netOpts.Confirmed,
		Redactor:        red,
		Interactive:     interactive,
	}
}

// ApplyTabOAuthIfConfigured resolves or refreshes the OAuth/OIDC token for tab's active scheme and injects it into plan.
func (a *App) ApplyTabOAuthIfConfigured(ctx context.Context, ps *projectState, tab *Tab, plan *core.Plan, red *core.Redactor, interactive bool) (*core.OAuthTokenRecord, error) {
	scheme, _ := a.resolveActiveOAuthScheme(ps, tab)
	if scheme == nil {
		return nil, nil
	}
	st := getOrCreateOAuthTabState(tab)
	a.syncOAuthTabStateFromScheme(ps, tab, st)

	st.mu.Lock()
	req := a.buildOAuthFlowRequestLocked(ps, tab, st, interactive)
	if red != nil {
		req.Redactor = red
	}
	st.mu.Unlock()

	svc := ps.OAuthService()
	rec, err := svc.ApplyOAuthToPlan(ctx, plan, req)
	st.mu.Lock()
	if err != nil {
		st.ErrorText = sanitizeOAuthUIError(err, req.Redactor)
	} else if rec != nil {
		st.CachedRecord = rec
		st.ErrorText = ""
	}
	st.mu.Unlock()
	return rec, err
}

func sanitizeOAuthUIError(err error, redactor *core.Redactor) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if redactor != nil {
		msg = redactor.RedactString(msg)
	}
	return msg
}

func (a *App) runOAuthUIAction(ps *projectState, tab *Tab, st *oauthTabUIState, statusLabel string, fn func(ctx context.Context, req core.OAuthFlowRequest, svc *core.OAuthService) error) {
	mgr := getOrCreateOAuthProjectManager(ps)
	mgr.mu.Lock()
	svc := mgr.service
	syncExec := mgr.syncExecution
	mgr.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	st.mu.Lock()
	if st.ActiveCancel != nil {
		st.ActiveCancel()
	}
	st.Busy = true
	st.StatusText = statusLabel
	st.ErrorText = ""
	st.ActiveCancel = cancel
	req := a.buildOAuthFlowRequestLocked(ps, tab, st, true)
	st.mu.Unlock()

	execFn := func() {
		defer cancel()
		err := fn(ctx, req, svc)
		st.mu.Lock()
		st.Busy = false
		st.ActiveCancel = nil
		if err != nil {
			st.ErrorText = sanitizeOAuthUIError(err, req.Redactor)
		}
		st.mu.Unlock()
	}

	if syncExec {
		execFn()
	} else {
		go execFn()
	}
}

func (a *App) triggerOAuthFetchDiscovery(ps *projectState, tab *Tab) {
	st := getOrCreateOAuthTabState(tab)
	om := oauthMsg(a.oauthLocale())
	a.runOAuthUIAction(ps, tab, st, om.DiscoveryFetching, func(ctx context.Context, req core.OAuthFlowRequest, svc *core.OAuthService) error {
		target := strings.TrimSpace(req.Config.DiscoveryURL)
		if target == "" {
			target = strings.TrimSpace(req.Config.Issuer)
		}
		doc, err := svc.GetOrFetchDiscovery(ctx, target, req.Config.Issuer, req)
		if err != nil {
			return err
		}
		st.mu.Lock()
		if st.Issuer == "" {
			st.Issuer = doc.Issuer
		}
		if doc.AuthorizationEndpoint != "" {
			st.AuthorizationURL = doc.AuthorizationEndpoint
		}
		if doc.TokenEndpoint != "" {
			st.TokenURL = doc.TokenEndpoint
		}
		if doc.DeviceAuthorizationEndpoint != "" {
			st.DeviceAuthorizationURL = doc.DeviceAuthorizationEndpoint
		}
		if doc.RevocationEndpoint != "" {
			st.RevocationURL = doc.RevocationEndpoint
		}
		if doc.JWKSURI != "" {
			st.JWKSURI = doc.JWKSURI
		}
		st.StatusText = om.DiscoverySuccess
		st.mu.Unlock()
		a.commitOAuthConfigToScheme(ps, tab, st)
		return nil
	})
}

func (a *App) triggerOAuthAcquireToken(ps *projectState, tab *Tab) {
	st := getOrCreateOAuthTabState(tab)
	om := oauthMsg(a.oauthLocale())

	st.mu.Lock()
	gt := st.toOAuthConfigLocked().NormalizedGrantType()
	st.ActiveDeviceChallenge = nil
	st.mu.Unlock()

	var initialStatus string
	switch gt {
	case core.OAuthGrantClientCredentials:
		initialStatus = om.StatusAcquiringToken
	case core.OAuthGrantDeviceCode:
		initialStatus = om.StatusPollingDevice
	default:
		initialStatus = om.StatusAuthorizingPKCE
	}

	a.runOAuthUIAction(ps, tab, st, initialStatus, func(ctx context.Context, req core.OAuthFlowRequest, svc *core.OAuthService) error {
		var rec *core.OAuthTokenRecord
		var err error
		switch gt {
		case core.OAuthGrantClientCredentials:
			rec, err = svc.AcquireClientCredentials(ctx, req)
		case core.OAuthGrantDeviceCode:
			ch, startErr := svc.StartDeviceAuthorization(ctx, req)
			if startErr != nil {
				return startErr
			}
			st.mu.Lock()
			st.ActiveDeviceChallenge = ch
			st.DevicePollStatus = core.DevicePollStatus{
				Attempt:      0,
				NextInterval: ch.PollInterval,
				ExpiresAt:    ch.ExpiresAt,
			}
			st.mu.Unlock()
			rec, err = svc.PollDeviceToken(ctx, req, ch, func(status core.DevicePollStatus) {
				st.mu.Lock()
				st.DevicePollStatus = status
				st.mu.Unlock()
			})
			st.mu.Lock()
			st.ActiveDeviceChallenge = nil
			st.mu.Unlock()
		default:
			rec, err = svc.AuthorizePKCE(ctx, req)
		}
		if err != nil {
			return err
		}
		st.mu.Lock()
		st.CachedRecord = rec
		st.StatusText = om.StatusTokenAcquired
		st.mu.Unlock()
		return nil
	})
}

func (a *App) triggerOAuthCancelFlow(tab *Tab) {
	st := getOrCreateOAuthTabState(tab)
	om := oauthMsg(a.oauthLocale())
	st.mu.Lock()
	if st.ActiveCancel != nil {
		st.ActiveCancel()
		st.ActiveCancel = nil
	}
	st.Busy = false
	st.ActiveDeviceChallenge = nil
	st.StatusText = om.StatusIdle
	st.mu.Unlock()
}

func (a *App) triggerOAuthRefreshToken(ps *projectState, tab *Tab) {
	st := getOrCreateOAuthTabState(tab)
	om := oauthMsg(a.oauthLocale())
	a.runOAuthUIAction(ps, tab, st, om.StatusRefreshing, func(ctx context.Context, req core.OAuthFlowRequest, svc *core.OAuthService) error {
		rec, err := svc.RefreshToken(ctx, req)
		if err != nil {
			return err
		}
		st.mu.Lock()
		st.CachedRecord = rec
		st.StatusText = om.StatusTokenRefreshed
		st.mu.Unlock()
		return nil
	})
}

func (a *App) triggerOAuthClearLocalToken(ps *projectState, tab *Tab) {
	st := getOrCreateOAuthTabState(tab)
	om := oauthMsg(a.oauthLocale())
	a.runOAuthUIAction(ps, tab, st, om.StatusLocalTokenCleared, func(ctx context.Context, req core.OAuthFlowRequest, svc *core.OAuthService) error {
		if err := svc.ClearLocalToken(ctx, req); err != nil {
			return err
		}
		st.mu.Lock()
		st.CachedRecord = nil
		st.StatusText = om.StatusLocalTokenCleared
		st.mu.Unlock()
		return nil
	})
}

func (a *App) triggerOAuthRevokeToken(ps *projectState, tab *Tab) {
	st := getOrCreateOAuthTabState(tab)
	om := oauthMsg(a.oauthLocale())
	a.runOAuthUIAction(ps, tab, st, om.StatusRevoking, func(ctx context.Context, req core.OAuthFlowRequest, svc *core.OAuthService) error {
		res, err := svc.RevokeToken(ctx, req)
		if err != nil {
			return err
		}
		st.mu.Lock()
		st.CachedRecord = nil
		if res.RemoteMessage != "" {
			st.StatusText = res.RemoteMessage
		} else {
			st.StatusText = om.StatusLocalTokenCleared
		}
		st.mu.Unlock()
		return nil
	})
}

// oauthReadOnlyNotice explains why ref/inherit schemes cannot be edited here.
// It is kept out of oauthMessages so the two-language completeness test does
// not require a new entry in every translation table.
func oauthReadOnlyNotice(locale string) string {
	if locale == "en" {
		return "Read-only: this scheme is referenced or inherited from a project file. Edits made here are not saved; use the scheme editor in Identity Management."
	}
	return "只读：此方案由项目文件引用或上级继承。此处编辑不会被保存；请在身份管理的方案编辑器中修改。"
}

// renderOAuthEditor renders the editable native MyGo UI configuration panel,
// interactive flow buttons, RFC 8628 Device Challenge banner, and Token / OIDC
// Claims Inspector for an inline oauth2/oidc request mode.
func (a *App) renderOAuthEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	a.renderOAuthEditorMode(c, t, tab, false)
}

// renderOAuthEditorMode is the shared implementation. readOnly is used when the
// active OAuth scheme comes from "ref"/"inherit" rather than an inline
// oauth2/oidc mode. Those schemes belong to a shared project file (or an
// inherited parent), and editing them through the request tab would only mutate
// the in-memory project while marking the request Dirty — a save of the request
// would NOT persist the scheme. To avoid claiming a save that never happens, the
// panel is a read-only inspector: no field writes, no flow buttons, no config
// commit, and no Dirty flag. Editing must go through the reusable scheme editor
// (Identity Management), which has a real, digest-protected save.
func (a *App) renderOAuthEditorMode(c *ui.Context, t *ui.Theme, tab *Tab, readOnly bool) {
	if tab == nil {
		return
	}
	var ps *projectState
	if tab.Owner != nil {
		ps = tab.Owner
	} else if a != nil {
		ps = a.ps
	}

	st := getOrCreateOAuthTabState(tab)
	scheme := a.syncOAuthTabStateFromScheme(ps, tab, st)
	if scheme == nil {
		scheme, _ = ensureInlineOAuthScheme(tab, "oauth2")
		a.syncOAuthTabStateFromScheme(ps, tab, st)
	}

	om := oauthMsg(a.oauthLocale())
	pal := uikit.PaletteFor(t)
	warnColor := uikit.MethodColor("DELETE", pal)
	okColor := uikit.MethodColor("GET", pal)

	st.mu.Lock()
	schemeType := st.SchemeType
	grantType := st.GrantType
	issuer := st.Issuer
	discoveryURL := st.DiscoveryURL
	authURL := st.AuthorizationURL
	tokenURL := st.TokenURL
	deviceURL := st.DeviceAuthorizationURL
	revokeURL := st.RevocationURL
	jwksURI := st.JWKSURI
	redirectURI := st.RedirectURI
	clientID := st.ClientID
	clientSecret := st.ClientSecret
	clientAuthMethod := st.ClientAuthMethod
	scopesText := st.ScopesText
	audience := st.Audience
	resource := st.Resource
	useToken := st.UseToken
	busy := st.Busy
	statusText := st.StatusText
	errText := st.ErrorText
	deviceChallenge := st.ActiveDeviceChallenge
	devicePoll := st.DevicePollStatus
	cachedRec := st.CachedRecord.Clone()
	reqPreview := a.buildOAuthFlowRequestLocked(ps, tab, st, false)
	cacheKey := reqPreview.CacheKey(nil)
	st.mu.Unlock()

	if statusText == "" {
		statusText = om.StatusIdle
	}

	if readOnly {
		// Inspect-only: the scheme is owned by a project file / parent folder,
		// not by this request. Never present an editable form that would only
		// mark the request Dirty while the scheme itself is never saved.
		ui.Column(c).Gap(6).Padding(10, 12).BorderWidth(1).BorderColor(pal.Hairline).Radius(8).FillWidth().Children(func() {
			ui.Text(c, om.SectionTitle).FontSize(13).FontWeight(600)
			ui.Text(c, oauthReadOnlyNotice(a.oauthLocale())).FontSize(11).TextColor(pal.Muted)
			ui.Text(c, fmt.Sprintf("%s / %s", schemeType, grantType)).FontSize(12).TextColor(pal.Text)
			if strings.TrimSpace(issuer) != "" {
				renderOAuthInspectorKV(c, pal, om.IssuerLabel, issuer)
			}
			if strings.TrimSpace(tokenURL) != "" {
				renderOAuthInspectorKV(c, pal, om.TokenURLLabel, tokenURL)
			}
			if strings.TrimSpace(clientID) != "" {
				renderOAuthInspectorKV(c, pal, om.ClientIDLabel, clientID)
			}
			renderOAuthInspectorCard(c, pal, okColor, warnColor, cachedRec, om)
		})
		return
	}

	changed := false

	ui.Column(c).Gap(8).Padding(10, 12).BorderWidth(1).BorderColor(pal.Hairline).Radius(8).FillWidth().Children(func() {
		// 1. Header + Isolation Scope
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.SectionTitle).FontSize(13).FontWeight(600)
			ui.Box(c).Grow(1)
			ui.Text(c, fmt.Sprintf("%s: %s", om.IsolationScopeHint, cacheKey.StorageKey())).
				FontSize(10).Font("monospace").TextColor(pal.Muted)
		})
		ui.Text(c, om.SectionSub).FontSize(11).TextColor(pal.Muted)

		// 2. Scheme Type & Grant Type
		ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.SchemeTypeOAuth2+" / OIDC").FontSize(12).TextColor(pal.Muted)
			if uikit.Select(c, &schemeType, oauthSchemeTypeOptions, uikit.IconMap(map[string]*ui.SVG{
				"oauth2": uikit.IconShield,
				"oidc":   uikit.IconGlobe,
			})).Changed() {
				if schemeType == "oidc" && !strings.Contains(scopesText, "openid") {
					scopesText = strings.TrimSpace("openid " + scopesText)
				}
				changed = true
			}
			ui.Text(c, om.GrantTypeLabel).FontSize(12).TextColor(pal.Muted)
			if uikit.Select(c, &grantType, oauthGrantTypeOptions, uikit.SameIcon(uikit.IconKey)).Changed() {
				changed = true
			}
		})

		// 3. OIDC Issuer & Discovery URL + Fetch Discovery Button
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.IssuerLabel).Width(110).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &issuer).Grow(1).Font("monospace").Placeholder(om.IssuerPlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.DiscoveryURLLabel).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &discoveryURL).Grow(1).Font("monospace").Placeholder(om.DiscoveryURLPlaceholder).Changed() {
				changed = true
			}
			if !busy && (strings.TrimSpace(issuer) != "" || strings.TrimSpace(discoveryURL) != "") {
				if ui.Button(c, om.BtnFetchDiscovery).Clicked() {
					a.triggerOAuthFetchDiscovery(ps, tab)
				}
			}
		})

		// 4. Endpoints based on Grant Type
		if grantType == core.OAuthGrantAuthorizationCode {
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, om.AuthURLLabel).Width(140).FontSize(11).TextColor(pal.Muted)
				if ui.TextInput(c, &authURL).Grow(1).Font("monospace").Placeholder(om.AuthURLPlaceholder).Changed() {
					changed = true
				}
				ui.Text(c, om.RedirectURLLabel).FontSize(11).TextColor(pal.Muted)
				if ui.TextInput(c, &redirectURI).Width(230).Font("monospace").Placeholder(om.RedirectURLPlaceholder).Changed() {
					changed = true
				}
			})
		}
		if grantType == core.OAuthGrantDeviceCode {
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, om.DeviceAuthURLLabel).Width(180).FontSize(11).TextColor(pal.Muted)
				if ui.TextInput(c, &deviceURL).Grow(1).Font("monospace").Placeholder(om.DeviceAuthURLPlaceholder).Changed() {
					changed = true
				}
			})
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.TokenURLLabel).Width(140).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &tokenURL).Grow(1).Font("monospace").Placeholder(om.TokenURLPlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.RevocationURLLabel).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &revokeURL).Grow(1).Font("monospace").Placeholder(om.RevocationURLPlaceholder).Changed() {
				changed = true
			}
		})
		if schemeType == "oidc" || jwksURI != "" || useToken == core.OAuthUseIDToken {
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, om.JWKSURILabel).Width(180).FontSize(11).TextColor(pal.Muted)
				if ui.TextInput(c, &jwksURI).Grow(1).Font("monospace").Placeholder(om.JWKSURIPlaceholder).Changed() {
					changed = true
				}
			})
		}

		// 5. Client ID, Client Secret & Client Auth Method
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.ClientIDLabel).Width(110).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &clientID).Grow(1).Font("monospace").Placeholder(om.ClientIDPlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.ClientSecretLabel).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &clientSecret).Grow(1).Font("monospace").Placeholder(om.ClientSecretPlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.ClientAuthMethodLabel).FontSize(11).TextColor(pal.Muted)
			if uikit.Select(c, &clientAuthMethod, oauthClientAuthOptions, uikit.IconMap(map[string]*ui.SVG{
				core.OAuthClientAuthBasic: uikit.IconLock,
				core.OAuthClientAuthPost:  uikit.IconKey,
				core.OAuthClientAuthNone:  uikit.IconClose,
			})).Changed() {
				changed = true
			}
		})
		if strings.TrimSpace(clientSecret) != "" && !strings.Contains(clientSecret, "{{") {
			ui.Text(c, om.LiteralSecretWarn).FontSize(11).TextColor(uikit.MethodColor("POST", pal))
		}

		// 6. Scopes, Audience, Resource & Token Selection
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.ScopesLabel).Width(130).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &scopesText).Grow(1).Font("monospace").Placeholder(om.ScopesPlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.AudienceLabel).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &audience).Width(160).Font("monospace").Placeholder(om.AudiencePlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.ResourceLabel).FontSize(11).TextColor(pal.Muted)
			if ui.TextInput(c, &resource).Width(150).Font("monospace").Placeholder(om.ResourcePlaceholder).Changed() {
				changed = true
			}
			ui.Text(c, om.UseTokenLabel).FontSize(11).TextColor(pal.Muted)
			if uikit.Select(c, &useToken, oauthUseTokenOptions, uikit.SameIcon(uikit.IconKey)).Changed() {
				changed = true
			}
		})

		if changed {
			st.mu.Lock()
			st.SchemeType = schemeType
			st.GrantType = grantType
			st.Issuer = issuer
			st.DiscoveryURL = discoveryURL
			st.AuthorizationURL = authURL
			st.TokenURL = tokenURL
			st.DeviceAuthorizationURL = deviceURL
			st.RevocationURL = revokeURL
			st.JWKSURI = jwksURI
			st.RedirectURI = redirectURI
			st.ClientID = clientID
			st.ClientSecret = clientSecret
			st.ClientAuthMethod = clientAuthMethod
			st.ScopesText = scopesText
			st.Audience = audience
			st.Resource = resource
			st.UseToken = useToken
			st.mu.Unlock()
			a.commitOAuthConfigToScheme(ps, tab, st)
		}

		// 7. Interactive Action Bar (Explicit User Actions Only)
		var acquireLabel string
		switch grantType {
		case core.OAuthGrantClientCredentials:
			acquireLabel = om.BtnGetTokenClientCreds
		case core.OAuthGrantDeviceCode:
			acquireLabel = om.BtnStartDeviceFlow
		default:
			acquireLabel = om.BtnGetTokenPKCE
		}

		hasRefresh := cachedRec != nil && strings.TrimSpace(cachedRec.RefreshToken) != ""
		hasAnyToken := cachedRec != nil && (cachedRec.AccessToken != "" || cachedRec.IDToken != "" || cachedRec.RefreshToken != "")

		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			if !busy {
				if uikit.PrimaryButton(c, acquireLabel).Clicked() {
					a.triggerOAuthAcquireToken(ps, tab)
				}
			} else {
				if ui.Button(c, om.BtnCancelFlow).Clicked() {
					a.triggerOAuthCancelFlow(tab)
				}
			}
			if !busy && hasRefresh {
				if ui.Button(c, om.BtnRefreshToken).Clicked() {
					a.triggerOAuthRefreshToken(ps, tab)
				}
			}
			if !busy && hasAnyToken {
				if ui.Button(c, om.BtnClearLocalToken).Clicked() {
					a.triggerOAuthClearLocalToken(ps, tab)
				}
				if ui.Button(c, om.BtnRevokeToken).Clicked() {
					a.triggerOAuthRevokeToken(ps, tab)
				}
			}
		})

		// 8. Status & Sanitized Error Banner
		ui.Text(c, statusText).FontSize(11).TextColor(pal.Muted)
		if errText != "" {
			ui.Text(c, errText).FontSize(11).Font("monospace").TextColor(warnColor)
		}

		// 9. Active RFC 8628 Device Challenge Card
		if deviceChallenge != nil {
			renderOAuthDeviceChallengeCard(c, pal, ps, deviceChallenge, devicePoll, om)
		}

		// 10. Token & OIDC Verification Inspector Card
		renderOAuthInspectorCard(c, pal, okColor, warnColor, cachedRec, om)
	})
}

func renderOAuthDeviceChallengeCard(c *ui.Context, pal uikit.Palette, ps *projectState, ch *core.DeviceAuthorizationChallenge, poll core.DevicePollStatus, om oauthMessages) {
	verifyTarget := ch.VerificationURIComplete
	if verifyTarget == "" {
		verifyTarget = ch.VerificationURI
	}
	pollHint := fmt.Sprintf("%s (#%d, interval=%s)", om.DevicePollingHint, poll.Attempt, poll.NextInterval)
	if poll.SlowedDown {
		pollHint = om.DeviceSlowDownHint + " — " + pollHint
	}

	ui.Column(c).Gap(6).Padding(8, 10).BorderWidth(1).BorderColor(pal.Hairline).Radius(6).FillWidth().Children(func() {
		ui.Text(c, om.DeviceCardTitle).FontSize(12).FontWeight(600)
		ui.Row(c).Gap(12).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.DeviceUserCodeLabel+":").FontSize(11).TextColor(pal.Muted)
			ui.Text(c, ch.UserCode).FontSize(15).FontWeight(700).Font("monospace")
			ui.Text(c, om.DeviceVerifyURLLabel+":").FontSize(11).TextColor(pal.Muted)
			ui.Text(c, ch.VerificationURI).FontSize(12).Font("monospace")
			ui.Box(c).Grow(1)
			if ui.Button(c, om.BtnOpenVerificationPage).Clicked() {
				mgr := getOrCreateOAuthProjectManager(ps)
				if mgr.browser != nil {
					_ = mgr.browser.OpenURL(context.Background(), verifyTarget)
				}
			}
		})
		ui.Text(c, pollHint).FontSize(11).TextColor(pal.Muted)
	})
}

func renderOAuthInspectorCard(c *ui.Context, pal uikit.Palette, okColor, warnColor ui.Color, rec *core.OAuthTokenRecord, om oauthMessages) {
	ui.Column(c).Gap(4).Padding(8, 10).BorderWidth(1).BorderColor(pal.Hairline).Radius(6).FillWidth().Children(func() {
		if rec == nil || (rec.AccessToken == "" && rec.IDToken == "" && rec.RefreshToken == "") {
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, om.InspectorTitle).FontSize(11).FontWeight(600)
				ui.Box(c).Grow(1)
				ui.Text(c, om.InspectorNoToken).FontSize(11).TextColor(pal.Muted)
			})
			return
		}

		expired := rec.IsExpired(time.Now(), 0)
		statusLabel := om.InspectorTokenValid
		statusColor := okColor
		if expired {
			statusLabel = om.InspectorTokenExpired
			statusColor = warnColor
		}

		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, om.InspectorTitle).FontSize(11).FontWeight(600)
			ui.Box(c).Grow(1)
			ui.Text(c, statusLabel).FontSize(11).FontWeight(600).TextColor(statusColor)
		})

		if rec.AccessToken != "" {
			renderOAuthInspectorKV(c, pal, om.InspectorAccessToken, core.MaskedPreview(rec.AccessToken))
		}
		if rec.RefreshToken != "" {
			renderOAuthInspectorKV(c, pal, om.InspectorRefreshToken, core.MaskedPreview(rec.RefreshToken))
		}
		if rec.IDToken != "" {
			renderOAuthInspectorKV(c, pal, om.InspectorIDToken, core.MaskedPreview(rec.IDToken))
		}
		if rec.Scope != "" {
			renderOAuthInspectorKV(c, pal, om.InspectorScope, rec.Scope)
		}
		if !rec.ExpiresAt.IsZero() {
			renderOAuthInspectorKV(c, pal, om.InspectorExpiresAt, rec.ExpiresAt.UTC().Format(time.RFC3339))
		}

		if rec.IDTokenInspection != nil {
			insp := rec.IDTokenInspection
			badgeText := om.OIDCBadgeUnverified
			badgeColor := warnColor
			if insp.Verified {
				badgeText = om.OIDCBadgeVerified
				badgeColor = okColor
			}
			ui.Text(c, badgeText).FontSize(11).FontWeight(600).TextColor(badgeColor)
			renderOAuthInspectorKV(c, pal, om.OIDCAlgKidLabel, fmt.Sprintf("alg=%s kid=%s", insp.Header.Alg, insp.Header.Kid))
			renderOAuthInspectorKV(c, pal, om.OIDCIssuerSubLabel, fmt.Sprintf("iss=%s | sub=%s", insp.Claims.Issuer, insp.Claims.Subject))
			renderOAuthInspectorKV(c, pal, om.OIDCAudienceLabel, strings.Join(insp.Claims.Audience, ", "))
		}
	})
}

func renderOAuthInspectorKV(c *ui.Context, pal uikit.Palette, label, value string) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(pal.Muted)
		ui.Box(c).Grow(1)
		ui.Text(c, value).FontSize(11).Font("monospace")
	})
}
