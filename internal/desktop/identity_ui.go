package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// requestAuthModes lists the request-level auth modes editable in the Auth tab.
// Keeping technical tokens untranslated follows internal/desktop/i18n.go rules.
// oauth2/oidc are first-class inline modes (A04): renderAuthEditor shows the
// OAuth panel, ensureInlineOAuthScheme creates the inline scheme,
// authRefFromTab saves it with the request, and buildTrackedTabPlan injects the
// token at send time. Previously these modes were only reachable through a
// pre-existing ref/inline request file.
var requestAuthModes = []string{"inherit", "none", "ref", "bearer", "basic", "apikey", "oauth2", "oidc"}

type authDiagItem struct {
	text string
	warn bool
}

// identityMessages holds localized labels for the B05 Auth/Identity UI so that
// internal/desktop/i18n.go (owned by the integrator) does not need to be edited
// directly in this worktree. Exact merge keys for i18n.go are documented in
// docs/task-results/B05.md.
type identityMessages struct {
	InheritSummaryNone     string
	InheritSummaryProject  string // %s (scheme summary)
	InheritSummaryFolder   string // %s (folder path), %s (scheme summary)
	InheritClearedByFolder string // %s (folder path)
	InheritSummaryMissing  string // %q (scheme id)
	ExplicitNoneHint       string
	SchemeRefLabel         string
	ChooseScheme           string
	SchemeResolvedPrefix   string // %s
	SchemeMissingWarning   string // %q
	SchemeEmptyHint        string
	APIKeyLocation         string
	APIKeyName             string
	APIKeyValue            string
	PreservedAuthHint      string // %q
	DiagSectionTitle       string
	SecretBoundFmt         string // %s (name), %s (backend)
	SecretMissingFmt       string // %s (name)
	SecretUnavailableFmt   string // %s (name), %s (backend)
	VarOriginFmt           string // %s (name), %s (source), %s (masked/safe value)
	VarOverrideSuffixFmt   string // %s (overridden layers)
	VarUndefinedFmt        string // %s (name)
	VarErrorFmt            string // %s (name), %s (error)
	PolicyWarnPrefix       string // %s (reason)
}

var identityMessagesZh = identityMessages{
	InheritSummaryNone:     "继承上级鉴权（当前上级未配置默认鉴权，实际不附加凭据）。",
	InheritSummaryProject:  "继承项目默认鉴权：%s",
	InheritSummaryFolder:   "继承文件夹鉴权（%s）：%s",
	InheritClearedByFolder: "上级文件夹（%s）已显式清除鉴权，此请求实际不附加凭据。",
	InheritSummaryMissing:  "上级引用的鉴权方案 %q 未找到，发送前需修正。",
	ExplicitNoneHint:       "显式清除继承鉴权（此请求不附加任何凭据）。",
	SchemeRefLabel:         "复用鉴权方案 ID（.gorequest/auth/*.auth.yaml）",
	ChooseScheme:           "选择方案",
	SchemeResolvedPrefix:   "当前引用方案：%s",
	SchemeMissingWarning:   "未找到鉴权方案 %q；保存仍保留引用，但发送前需创建该方案。",
	SchemeEmptyHint:        "请输入或选择一个鉴权方案 ID。",
	APIKeyLocation:         "发送位置",
	APIKeyName:             "参数/头/Cookie 名称",
	APIKeyValue:            "密钥值（建议使用 {{secret.name}} 避免明文落盘）",
	PreservedAuthHint:      "保留已配置的鉴权类型 %q（未修改时原样往返保存）。",
	DiagSectionTitle:       "变量来源与目标策略预览",
	SecretBoundFmt:         "{{secret.%s}} · 已绑定（%s）",
	SecretMissingFmt:       "{{secret.%s}} · 未绑定秘密",
	SecretUnavailableFmt:   "{{secret.%s}} · 凭据后端不可用（%s）",
	VarOriginFmt:           "{{%s}} · 来源 %s = %s",
	VarOverrideSuffixFmt:   "（覆盖 %s）",
	VarUndefinedFmt:        "{{%s}} · 未定义变量",
	VarErrorFmt:            "{{%s}} · 解析错误：%s",
	PolicyWarnPrefix:       "目标策略提示：%s",
}

var identityMessagesEn = identityMessages{
	InheritSummaryNone:     "Inherits parent auth (no default auth configured; sends without credentials).",
	InheritSummaryProject:  "Inherits project default auth: %s",
	InheritSummaryFolder:   "Inherits folder auth (%s): %s",
	InheritClearedByFolder: "Folder (%s) explicitly clears inherited auth; sends without credentials.",
	InheritSummaryMissing:  "Inherited auth scheme %q not found; fix before sending.",
	ExplicitNoneHint:       "Explicitly clears inherited auth for this request.",
	SchemeRefLabel:         "Reusable auth scheme ID (.gorequest/auth/*.auth.yaml)",
	ChooseScheme:           "Schemes",
	SchemeResolvedPrefix:   "Referenced scheme: %s",
	SchemeMissingWarning:   "Auth scheme %q not found; ref is preserved on save, define it before sending.",
	SchemeEmptyHint:        "Enter or choose an auth scheme ID.",
	APIKeyLocation:         "Add to",
	APIKeyName:             "Key name",
	APIKeyValue:            "Value (use {{secret.name}} to keep secrets out of files)",
	PreservedAuthHint:      "Preserving configured auth %q (round-trips unchanged unless switched).",
	DiagSectionTitle:       "Variable origins & target policy",
	SecretBoundFmt:         "{{secret.%s}} · bound (%s)",
	SecretMissingFmt:       "{{secret.%s}} · missing secret",
	SecretUnavailableFmt:   "{{secret.%s}} · backend unavailable (%s)",
	VarOriginFmt:           "{{%s}} · from %s = %s",
	VarOverrideSuffixFmt:   " (overrides %s)",
	VarUndefinedFmt:        "{{%s}} · undefined variable",
	VarErrorFmt:            "{{%s}} · resolution error: %s",
	PolicyWarnPrefix:       "Target policy notice: %s",
}

func (a *App) identityLangCode() string {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
		return "en"
	}
	return "zh-CN"
}

func (a *App) identityMsg() *identityMessages {
	if a.identityLangCode() == "en" {
		return &identityMessagesEn
	}
	return &identityMessagesZh
}

// authRefFromTab converts a Tab's auth fields back into a *core.AuthRef without
// collapsing inherit/ref into inline/none. Returning (nil, nil) represents
// "inherit" (omits `auth` when marshaled to YAML).
func authRefFromTab(tab *Tab) (*core.AuthRef, error) {
	if tab == nil {
		return nil, nil
	}
	switch tab.AuthMode {
	case "", "inherit":
		return nil, nil
	case "none":
		return &core.AuthRef{Mode: "none"}, nil
	case "ref":
		ref := strings.TrimSpace(tab.AuthRefID)
		if ref == "" {
			return nil, fmt.Errorf("auth ref requires a scheme id")
		}
		return &core.AuthRef{Mode: "ref", Ref: ref}, nil
	case "bearer":
		ver := tab.authInlineVer
		if ver == 0 {
			ver = core.SupportedSchemaVersion
		}
		return &core.AuthRef{
			Mode: "inline",
			Inline: &core.AuthScheme{
				SchemaVersion: ver,
				ID:            tab.authInlineID,
				Type:          "bearer",
				Token:         tab.AuthToken,
			},
		}, nil
	case "basic":
		ver := tab.authInlineVer
		if ver == 0 {
			ver = core.SupportedSchemaVersion
		}
		return &core.AuthRef{
			Mode: "inline",
			Inline: &core.AuthScheme{
				SchemaVersion: ver,
				ID:            tab.authInlineID,
				Type:          "basic",
				Username:      tab.AuthUser,
				Password:      tab.AuthPass,
			},
		}, nil
	case "apikey":
		name := strings.TrimSpace(tab.AuthKeyName)
		if name == "" {
			return nil, fmt.Errorf("api key auth requires a name")
		}
		loc, err := core.ValidateAPIKeyLocation(tab.AuthKeyIn)
		if err != nil {
			return nil, err
		}
		inField := loc
		if tab.authKeyInOmitted && loc == "header" {
			inField = ""
		}
		ver := tab.authInlineVer
		if ver == 0 {
			ver = core.SupportedSchemaVersion
		}
		return &core.AuthRef{
			Mode: "inline",
			Inline: &core.AuthScheme{
				SchemaVersion: ver,
				ID:            tab.authInlineID,
				Type:          "apikey",
				Name:          name,
				In:            inField,
				Value:         tab.AuthKeyValue,
			},
		}, nil
	default:
		if tab.authPassthrough != nil {
			return core.CloneAuthRef(tab.authPassthrough), nil
		}
		return nil, fmt.Errorf("unsupported auth mode %q", tab.AuthMode)
	}
}

// authTemplateStrings returns the template-bearing strings active for this
// tab's configured auth, including folder/project inherited schemes.
func (tab *Tab) authTemplateStrings(p *core.Project) []string {
	if tab == nil {
		return nil
	}
	switch tab.AuthMode {
	case "", "inherit":
		if p != nil {
			if eff, err := p.ResolveEffectiveAuth(tab.Entry, &core.Request{}); err == nil && eff != nil && eff.Scheme != nil {
				return schemeTemplateStrings(eff.Scheme)
			}
		}
		return nil
	case "ref":
		if p != nil {
			if s, ok := p.AuthSchemes[strings.TrimSpace(tab.AuthRefID)]; ok {
				return schemeTemplateStrings(s)
			}
		}
		return nil
	case "bearer":
		return []string{tab.AuthToken}
	case "basic":
		return []string{tab.AuthUser, tab.AuthPass}
	case "apikey":
		return []string{tab.AuthKeyName, tab.AuthKeyValue}
	default:
		return nil
	}
}

func schemeTemplateStrings(s *core.AuthScheme) []string {
	if s == nil {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "bearer":
		return []string{s.Token}
	case "basic":
		return []string{s.Username, s.Password}
	case "apikey":
		return []string{s.Name, s.Value}
	default:
		return nil
	}
}

func sortedAuthSchemeIDs(p *core.Project) []string {
	if p == nil || len(p.AuthSchemes) == 0 {
		return nil
	}
	ids := make([]string, 0, len(p.AuthSchemes))
	for id := range p.AuthSchemes {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func safeValuePreview(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "(empty)"
	}
	// Template placeholders like {{accessToken}} or {{secret.apiKey}} are safe to
	// display verbatim; literal credential material is masked.
	if strings.HasPrefix(trimmed, "{{") && strings.HasSuffix(trimmed, "}}") && strings.Count(trimmed, "{{") == 1 {
		return trimmed
	}
	return "***"
}

func summarizeAuthScheme(s *core.AuthScheme) string {
	if s == nil {
		return "none"
	}
	typ := strings.ToLower(strings.TrimSpace(s.Type))
	switch typ {
	case "", "none":
		return "none"
	case "bearer":
		return fmt.Sprintf("bearer (token: %s)", safeValuePreview(s.Token))
	case "basic":
		user := strings.TrimSpace(s.Username)
		if user == "" {
			user = "(empty)"
		}
		return fmt.Sprintf("basic (user: %s, password: %s)", user, safeValuePreview(s.Password))
	case "apikey":
		loc := core.NormalizeAPIKeyLocation(s.In)
		return fmt.Sprintf("apikey (%s: %s = %s)", loc, strings.TrimSpace(s.Name), safeValuePreview(s.Value))
	case "oauth2", "oidc":
		return typ + " (token stored in system credential store)"
	default:
		return typ
	}
}

func containsString(list []string, target string) bool {
	for _, s := range list {
		if s == target {
			return true
		}
	}
	return false
}

func (a *App) projectOf(tab *Tab) *core.Project {
	if tab != nil && tab.Owner != nil && tab.Owner.project != nil {
		return tab.Owner.project
	}
	if a != nil && a.ps != nil {
		return a.ps.project
	}
	return nil
}

func (a *App) activeEnvOf(tab *Tab) string {
	if tab != nil && tab.Owner != nil {
		return tab.Owner.currentEnv()
	}
	if a != nil && a.ps != nil {
		return a.ps.currentEnv()
	}
	return ""
}

type projectSecretSources struct{ primary, fallback core.SecretProvider }

func (s projectSecretSources) Get(name string) (string, bool) {
	if s.primary != nil {
		if v, ok := s.primary.Get(name); ok {
			return v, true
		}
	}
	if s.fallback != nil {
		return s.fallback.Get(name)
	}
	return "", false
}
func (s projectSecretSources) InspectSecret(name string) core.SecretStatus {
	if s.primary != nil {
		if _, ok := s.primary.Get(name); ok {
			return core.InspectSecretFrom(s.primary, name)
		}
	}
	return core.InspectSecretFrom(s.fallback, name)
}
func (a *App) secretsProvider() core.SecretProvider {
	if a == nil {
		return nil
	}
	var primary, fallback core.SecretProvider
	if a.mgr != nil {
		primary = a.mgr.secrets
	}
	if a.ps != nil {
		fallback = a.ps.secrets
	}
	return projectSecretSources{primary: primary, fallback: fallback}
}

// renderAuthEditor renders the request Auth tab, supporting inherit (across
// folder and project layers), none, ref, bearer, basic and apikey
// (header/query/cookie) plus memoized variable-origin & target-policy previews.
func (a *App) renderAuthEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	im := a.identityMsg()
	pal := uikit.PaletteFor(t)
	proj := a.projectOf(tab)

	if tab.AuthMode == "" {
		tab.AuthMode = "inherit"
	}
	modes := requestAuthModes
	if !containsString(requestAuthModes, tab.AuthMode) {
		modes = append(append([]string(nil), requestAuthModes...), tab.AuthMode)
	}

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.AuthTab).FontSize(12).TextColor(t.TextMuted)
		if uikit.ActionCursor(uikit.Select(c, &tab.AuthMode, modes, uikit.IconMap(map[string]*ui.SVG{
			"inherit": uikit.IconLayers,
			"none":    uikit.IconClose,
			"ref":     uikit.IconLink,
			"bearer":  uikit.IconKey,
			"basic":   uikit.IconLock,
			"apikey":  uikit.IconKey,
			"oauth2":  uikit.IconShield,
			"oidc":    uikit.IconGlobe,
		}))).Changed() {
			tab.AuthIdx = indexOf(requestAuthModes, tab.AuthMode)
			tab.authPassthrough = nil
			tab.authKeyInOmitted = false
			if tab.AuthMode == "apikey" && strings.TrimSpace(tab.AuthKeyIn) == "" {
				tab.AuthKeyIn = "header"
			}
			if tab.AuthMode == "ref" && strings.TrimSpace(tab.AuthRefID) == "" {
				if ids := sortedAuthSchemeIDs(proj); len(ids) > 0 {
					tab.AuthRefID = ids[0]
				}
			}
			tab.Dirty = true
		}
	})

	switch tab.AuthMode {
	case "inherit":
		renderInheritSummary(c, t, pal, im, proj, tab)

	case "none":
		ui.Text(c, m.NoAuth).TextColor(t.TextMuted)
		ui.Text(c, im.ExplicitNoneHint).FontSize(12).TextColor(t.TextMuted)

	case "ref":
		ui.Text(c, im.SchemeRefLabel).FontSize(12).TextColor(t.TextMuted)
		schemeIDs := sortedAuthSchemeIDs(proj)
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			if ui.TextInput(c, &tab.AuthRefID).Grow(1).Placeholder("bearer").Changed() {
				tab.Dirty = true
			}
			if len(schemeIDs) > 0 {
				uikit.ActionCursor(ui.MenuButton(c, im.ChooseScheme, func(mm *ui.Menu) {
					for _, id := range schemeIDs {
						if mm.Item(id).Checked(strings.TrimSpace(tab.AuthRefID) == id).Chosen() {
							tab.AuthRefID = id
							tab.Dirty = true
						}
					}
				}))
			}
		})
		refID := strings.TrimSpace(tab.AuthRefID)
		switch {
		case refID == "":
			ui.Text(c, im.SchemeEmptyHint).FontSize(12).TextColor(uikit.MethodColor("DELETE", pal))
		case proj != nil && proj.AuthSchemes[refID] != nil:
			ui.Text(c, translate(im.SchemeResolvedPrefix, summarizeAuthScheme(proj.AuthSchemes[refID]))).
				FontSize(12).TextColor(t.TextMuted)
		default:
			ui.Text(c, translate(im.SchemeMissingWarning, refID)).
				FontSize(12).TextColor(uikit.MethodColor("DELETE", pal))
		}

	case "bearer":
		ui.Text(c, m.TokenHint).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthToken).Placeholder("{{secret.apiToken}}").Changed() {
			tab.Dirty = true
		}

	case "basic":
		ui.Text(c, m.Username).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthUser).Changed() {
			tab.Dirty = true
		}
		ui.Text(c, m.Password).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthPass).Placeholder("{{secret.password}}").Changed() {
			tab.Dirty = true
		}

	case "apikey":
		tab.AuthKeyInIdx = indexOf(core.APIKeyLocations, core.NormalizeAPIKeyLocation(tab.AuthKeyIn))
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, im.APIKeyLocation).FontSize(12).TextColor(t.TextMuted)
			if uikit.NativeSegmented(c, &tab.AuthKeyInIdx, core.APIKeyLocations...).Changed() {
				tab.AuthKeyIn = core.APIKeyLocations[tab.AuthKeyInIdx]
				tab.authKeyInOmitted = false
				tab.Dirty = true
			}
		})
		ui.Text(c, im.APIKeyName).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthKeyName).Placeholder("X-API-Key").Changed() {
			tab.Dirty = true
		}
		ui.Text(c, im.APIKeyValue).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthKeyValue).Placeholder("{{secret.apiKey}}").Changed() {
			tab.Dirty = true
		}

	default:
		ui.Text(c, translate(im.PreservedAuthHint, tab.AuthMode)).FontSize(12).TextColor(t.TextMuted)
	}

	if m := strings.ToLower(strings.TrimSpace(tab.AuthMode)); m == "oauth2" || m == "oidc" {
		// Inline oauth2/oidc mode: this request owns the scheme, so edits are
		// saved with the request (authPassthrough -> request YAML).
		a.renderOAuthEditor(c, t, tab)
	} else if sch, _ := a.resolveActiveOAuthScheme(nil, tab); sch != nil {
		// ref/inherit resolving to an oauth2/oidc scheme (A04): the scheme lives
		// in a shared project file / parent folder. Show it read-only so we never
		// imply an in-memory edit was saved. See renderOAuthEditorMode.
		a.renderOAuthEditorMode(c, t, tab, true)
	}
	a.renderAuthDiagnostics(c, t, pal, im, proj, tab)
}

func renderInheritSummary(c *ui.Context, t *ui.Theme, pal uikit.Palette, im *identityMessages, proj *core.Project, tab *Tab) {
	if proj == nil {
		ui.Text(c, im.InheritSummaryNone).FontSize(12).TextColor(t.TextMuted)
		return
	}
	// Pass an empty Request override so we resolve the parent hierarchy even if
	// the underlying saved entry had a different auth before this edit.
	eff, err := proj.ResolveEffectiveAuth(tab.Entry, &core.Request{})
	if err != nil {
		missingID := extractMissingSchemeName(proj, tab.Entry)
		if missingID != "" {
			ui.Text(c, translate(im.InheritSummaryMissing, missingID)).
				FontSize(12).TextColor(uikit.MethodColor("DELETE", pal))
			return
		}
		ui.Text(c, err.Error()).FontSize(12).TextColor(uikit.MethodColor("DELETE", pal))
		return
	}
	if eff == nil || eff.SourceKind == "none" {
		ui.Text(c, im.InheritSummaryNone).FontSize(12).TextColor(t.TextMuted)
		return
	}
	switch eff.SourceKind {
	case "folder":
		folderLabel := strings.TrimSuffix(eff.SourcePath, "/collection.yaml")
		if eff.Mode == "none" || eff.Scheme == nil {
			ui.Text(c, translate(im.InheritClearedByFolder, folderLabel)).
				FontSize(12).TextColor(t.TextMuted)
			return
		}
		desc := summarizeAuthScheme(eff.Scheme)
		if eff.RefID != "" {
			desc = eff.RefID + " · " + desc
		}
		ui.Text(c, translate(im.InheritSummaryFolder, folderLabel, desc)).
			FontSize(12).TextColor(t.TextMuted)
	case "project":
		if eff.Mode == "none" || eff.Scheme == nil {
			ui.Text(c, im.InheritSummaryNone).FontSize(12).TextColor(t.TextMuted)
			return
		}
		desc := summarizeAuthScheme(eff.Scheme)
		if eff.RefID != "" {
			desc = eff.RefID + " · " + desc
		}
		ui.Text(c, translate(im.InheritSummaryProject, desc)).
			FontSize(12).TextColor(t.TextMuted)
	default:
		ui.Text(c, im.InheritSummaryNone).FontSize(12).TextColor(t.TextMuted)
	}
}

func extractMissingSchemeName(proj *core.Project, entry *core.RequestEntry) string {
	if proj == nil {
		return ""
	}
	if entry != nil {
		parts := strings.Split(strings.TrimPrefix(entry.RelPath, "collections/"), "/")
		if len(parts) > 1 {
			for i := len(parts) - 1; i >= 1; i-- {
				sub := strings.Join(parts[:i], "/")
				if fa := proj.FolderAuth(sub); fa != nil && fa.Mode != "" {
					if fa.Mode == "ref" {
						return fa.Ref
					}
					return ""
				}
			}
		}
	}
	if proj.File != nil && proj.File.Defaults != nil {
		return strings.TrimSpace(proj.File.Defaults.Auth)
	}
	return ""
}

func (a *App) renderAuthDiagnostics(c *ui.Context, t *ui.Theme, pal uikit.Palette, im *identityMessages, proj *core.Project, tab *Tab) {
	if tab == nil || proj == nil {
		return
	}
	envID := a.activeEnvOf(tab)
	relPath := ""
	if tab.Entry != nil {
		relPath = tab.Entry.RelPath
	}
	cacheKey := strings.Join([]string{
		a.identityLangCode(),
		envID,
		relPath,
		tab.Method,
		tab.URL,
		tab.AuthMode,
		tab.AuthRefID,
		tab.AuthToken,
		tab.AuthUser,
		tab.AuthPass,
		tab.AuthKeyIn,
		tab.AuthKeyName,
		tab.AuthKeyValue,
	}, "\x1f")

	if tab.authDiagKey != cacheKey {
		tab.authDiagKey = cacheKey
		tab.authDiagItems = computeAuthDiagnostics(im, proj, tab, envID, a.secretsProvider())
	}

	if len(tab.authDiagItems) == 0 {
		return
	}

	warnColor := uikit.MethodColor("DELETE", pal)
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, im.DiagSectionTitle).FontSize(11).TextColor(t.TextMuted)
		for _, item := range tab.authDiagItems {
			col := t.TextMuted
			if item.warn {
				col = warnColor
			}
			ui.Text(c, item.text).FontSize(11).TextColor(col)
		}
	})
}

func computeAuthDiagnostics(im *identityMessages, proj *core.Project, tab *Tab, envID string, secrets core.SecretProvider) []authDiagItem {
	var items []authDiagItem

	tplStrings := tab.authTemplateStrings(proj)
	seenTpl := map[string]bool{}
	var tplNames []string
	for _, s := range tplStrings {
		for _, name := range core.TemplateNames(s) {
			if !seenTpl[name] {
				seenTpl[name] = true
				tplNames = append(tplNames, name)
			}
		}
	}

	insp, _ := proj.InspectVariables(tab.Entry, envID, secrets, nil)

	for _, name := range tplNames {
		if secName, ok := strings.CutPrefix(name, "secret."); ok {
			secName = strings.TrimSpace(secName)
			st := core.InspectSecretFrom(secrets, secName)
			switch st.State {
			case core.SecretStateBound:
				items = append(items, authDiagItem{
					text: translate(im.SecretBoundFmt, secName, st.Backend),
				})
			case core.SecretStateUnavailable:
				items = append(items, authDiagItem{
					text: translate(im.SecretUnavailableFmt, secName, st.Backend),
					warn: true,
				})
			default:
				items = append(items, authDiagItem{
					text: translate(im.SecretMissingFmt, secName),
					warn: true,
				})
			}
			continue
		}

		if insp != nil {
			if ve, exists := insp.ByName[name]; exists {
				if ve.Error != "" {
					items = append(items, authDiagItem{
						text: translate(im.VarErrorFmt, name, ve.Error),
						warn: true,
					})
					continue
				}
				line := translate(im.VarOriginFmt, name, ve.Effective.Label(), ve.DisplayValue)
				if len(ve.Overridden) > 0 {
					prevLabels := make([]string, len(ve.Overridden))
					for i, ov := range ve.Overridden {
						prevLabels[i] = ov.Label()
					}
					line += translate(im.VarOverrideSuffixFmt, strings.Join(prevLabels, ", "))
				}
				items = append(items, authDiagItem{text: line})
				continue
			}
		}
		items = append(items, authDiagItem{
			text: translate(im.VarUndefinedFmt, name),
			warn: true,
		})
	}

	// Best-effort target & production policy preview (never leaks secret values).
	if strings.TrimSpace(tab.URL) != "" {
		vars, _, err := proj.ResolveVarsWith(tab.Entry, envID, nil)
		if err == nil {
			red := core.NewRedactor()
			resolvedURL, errU := resolveWithRedactor(tab.URL, vars, secrets, red)
			if errU == nil && strings.TrimSpace(resolvedURL) != "" {
				hasCreds := tabHasEffectiveCredentials(proj, tab)
				pol := core.MergeTargetPolicy(nil, nil)
				dec, _ := core.EvaluateTarget(pol, core.TargetCheckInput{
					URL:            resolvedURL,
					Method:         tab.Method,
					EnvID:          envID,
					HasCredentials: hasCreds,
					Interactive:    true,
				})
				if dec != nil && (dec.Outcome == core.DecisionConfirmRequired || dec.Outcome == core.DecisionDeny) {
					for _, reason := range dec.Reasons {
						items = append(items, authDiagItem{
							text: translate(im.PolicyWarnPrefix, red.RedactString(reason)),
							warn: true,
						})
					}
				}
			}
		}
	}

	return items
}

func resolveWithRedactor(raw string, vars map[string]string, secrets core.SecretProvider, red *core.Redactor) (string, error) {
	r := core.NewResolver(vars, secretTracker{inner: secrets, red: red})
	return r.Resolve(raw)
}

type secretTracker struct {
	inner core.SecretProvider
	red   *core.Redactor
}

func (s secretTracker) Get(name string) (string, bool) {
	if s.inner == nil {
		return "", false
	}
	v, ok := s.inner.Get(name)
	if ok && s.red != nil {
		s.red.RegisterSecret(v)
	}
	return v, ok
}

func tabHasEffectiveCredentials(proj *core.Project, tab *Tab) bool {
	if tab == nil {
		return false
	}
	switch tab.AuthMode {
	case "none":
		return false
	case "bearer", "basic", "apikey", "ref":
		return true
	case "", "inherit":
		if proj != nil {
			if eff, err := proj.ResolveEffectiveAuth(tab.Entry, &core.Request{}); err == nil && eff != nil {
				return eff.Scheme != nil
			}
		}
		return false
	default:
		return true
	}
}
