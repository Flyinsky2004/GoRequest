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
var requestAuthModes = []string{"inherit", "none", "ref", "bearer", "basic", "apikey"}

// identityMessages holds localized labels for the B05 Auth/Identity UI so that
// internal/desktop/i18n.go (owned by the integrator) does not need to be edited
// directly in this worktree. Exact merge keys for i18n.go are documented in
// docs/task-results/B05.md.
type identityMessages struct {
	InheritSummaryNone    string
	InheritSummaryProject string // %s (scheme summary)
	InheritSummaryMissing string // %q (scheme id)
	ExplicitNoneHint      string
	SchemeRefLabel        string
	ChooseScheme          string
	SchemeResolvedPrefix  string // %s
	SchemeMissingWarning  string // %q
	SchemeEmptyHint       string
	APIKeyLocation        string
	APIKeyName            string
	APIKeyValue           string
	PreservedAuthHint     string // %q
}

var identityMessagesZh = identityMessages{
	InheritSummaryNone:    "继承上级鉴权（当前上级未配置默认鉴权，实际不附加凭据）。",
	InheritSummaryProject: "继承项目默认鉴权：%s",
	InheritSummaryMissing: "项目默认鉴权 %q 未找到，发送前需修正。",
	ExplicitNoneHint:      "显式清除继承鉴权（此请求不附加任何凭据）。",
	SchemeRefLabel:        "复用鉴权方案 ID（.gorequest/auth/*.auth.yaml）",
	ChooseScheme:          "选择方案",
	SchemeResolvedPrefix:  "当前引用方案：%s",
	SchemeMissingWarning:  "未找到鉴权方案 %q；保存仍保留引用，但发送前需创建该方案。",
	SchemeEmptyHint:       "请输入或选择一个鉴权方案 ID。",
	APIKeyLocation:        "发送位置",
	APIKeyName:            "参数/头/Cookie 名称",
	APIKeyValue:           "密钥值（建议使用 {{secret.name}} 避免明文落盘）",
	PreservedAuthHint:     "保留已配置的鉴权类型 %q（未修改时原样往返保存）。",
}

var identityMessagesEn = identityMessages{
	InheritSummaryNone:    "Inherits parent auth (no default auth configured; sends without credentials).",
	InheritSummaryProject: "Inherits project default auth: %s",
	InheritSummaryMissing: "Project default auth %q not found; fix before sending.",
	ExplicitNoneHint:      "Explicitly clears inherited auth for this request.",
	SchemeRefLabel:        "Reusable auth scheme ID (.gorequest/auth/*.auth.yaml)",
	ChooseScheme:          "Schemes",
	SchemeResolvedPrefix:  "Referenced scheme: %s",
	SchemeMissingWarning:  "Auth scheme %q not found; ref is preserved on save, define it before sending.",
	SchemeEmptyHint:       "Enter or choose an auth scheme ID.",
	APIKeyLocation:        "Add to",
	APIKeyName:            "Key name",
	APIKeyValue:           "Value (use {{secret.name}} to keep secrets out of files)",
	PreservedAuthHint:     "Preserving configured auth %q (round-trips unchanged unless switched).",
}

func (a *App) identityMsg() *identityMessages {
	if a != nil && a.mgr != nil && a.mgr.cfg != nil && parseLanguage(string(a.mgr.cfg.language())) == langEnglish {
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
// tab's configured auth, for pre-send undefined-variable checks and origin
// previews.
func (tab *Tab) authTemplateStrings(p *core.Project) []string {
	if tab == nil {
		return nil
	}
	switch tab.AuthMode {
	case "", "inherit":
		if p != nil && p.File != nil && p.File.Defaults != nil && p.File.Defaults.Auth != "" && p.File.Defaults.Auth != "none" {
			if s, ok := p.AuthSchemes[p.File.Defaults.Auth]; ok {
				return schemeTemplateStrings(s)
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

// renderAuthEditor renders the request Auth tab, supporting inherit, none, ref,
// bearer, basic and apikey (header/query/cookie) without losing references.
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
		if ui.Select(c, &tab.AuthMode, modes).Changed() {
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
		defAuth := ""
		if proj != nil && proj.File != nil && proj.File.Defaults != nil {
			defAuth = strings.TrimSpace(proj.File.Defaults.Auth)
		}
		if defAuth == "" || defAuth == "none" {
			ui.Text(c, im.InheritSummaryNone).FontSize(12).TextColor(t.TextMuted)
		} else if s, ok := proj.AuthSchemes[defAuth]; ok {
			ui.Text(c, translate(im.InheritSummaryProject, defAuth+" · "+summarizeAuthScheme(s))).
				FontSize(12).TextColor(t.TextMuted)
		} else {
			ui.Text(c, translate(im.InheritSummaryMissing, defAuth)).
				FontSize(12).TextColor(uikit.MethodColor("DELETE", pal))
		}

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
				ui.MenuButton(c, im.ChooseScheme, func(mm *ui.Menu) {
					for _, id := range schemeIDs {
						if mm.Item(id).Checked(strings.TrimSpace(tab.AuthRefID) == id).Chosen() {
							tab.AuthRefID = id
							tab.Dirty = true
						}
					}
				})
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
		locIdx := indexOf(core.APIKeyLocations, core.NormalizeAPIKeyLocation(tab.AuthKeyIn))
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, im.APIKeyLocation).FontSize(12).TextColor(t.TextMuted)
			if ui.Segmented(c, &locIdx, core.APIKeyLocations...).Changed() {
				tab.AuthKeyIn = core.APIKeyLocations[locIdx]
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
}
