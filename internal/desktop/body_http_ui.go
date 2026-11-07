package desktop

import (
	"context"
	"slices"
	"strings"
	"sync"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

type customMethodState struct {
	open      bool
	targetTab *Tab
	draft     string
	errMsg    string
}

var (
	customMethodMu     sync.Mutex
	customMethodStates = map[*App]*customMethodState{}
)

func (a *App) customMethodUI() *customMethodState {
	customMethodMu.Lock()
	defer customMethodMu.Unlock()
	st, ok := customMethodStates[a]
	if !ok {
		st = &customMethodState{}
		customMethodStates[a] = st
	}
	return st
}

// openCustomMethodDialog opens the RFC 9110 custom HTTP method modal for tab.
func (a *App) openCustomMethodDialog(tab *Tab) {
	if a == nil || tab == nil {
		return
	}
	st := a.customMethodUI()
	st.targetTab = tab
	if !slices.Contains(methods, tab.Method) {
		st.draft = tab.Method
	} else {
		st.draft = ""
	}
	st.errMsg = ""
	st.open = true
}

func (a *App) submitCustomMethodDialog() {
	st := a.customMethodUI()
	if st.targetTab == nil {
		st.open = false
		return
	}
	meth := strings.ToUpper(strings.TrimSpace(st.draft))
	if err := core.ValidateHTTPMethod(meth); err != nil {
		st.errMsg = err.Error()
		return
	}
	st.targetTab.Method = meth
	st.targetTab.Dirty = true
	st.errMsg = ""
	st.open = false
}

// b03MethodPicker renders the HTTP method selector supporting the 7 standard
// methods plus arbitrary RFC 9110 custom method tokens via "Custom Method…".
func (a *App) b03MethodPicker(c *ui.Context, p uikit.Palette, tab *Tab) {
	bm := a.bodyMsg()
	isCustom := tab.Method != "" && !slices.Contains(methods, tab.Method)
	width := float32(108)
	if len(tab.Method) > 7 {
		width = 128
	}
	row := ui.Row(c).Width(width).Height(40).Padding(0, 10).Gap(4).AlignItems(ui.Center)
	// R4: a hovered method segment gets the existing Press wash (a stronger
	// neutral than Hover, which would blend into the URL input's surface), so
	// the segment stays distinct from the URL on hover as well as at rest.
	if row.Hovered() {
		row = row.Background(p.Press)
	}
	row.Children(func() {
		ui.Text(c, tab.Method).Font("monospace").FontWeight(700).FontSize(13).
			TextColor(uikit.MethodColor(tab.Method, p)).SingleLine()
		ui.Icon(c, uikit.IconChevron).FontSize(11).TextColor(p.Muted)
	})
	uikit.ActionCursor(row).PopoverMenuButton(func(mm *ui.Menu) {
		for _, meth := range methods {
			if mm.Item(meth).Checked(meth == tab.Method).Chosen() {
				tab.Method = meth
				tab.Dirty = true
			}
		}
		mm.Separator()
		if isCustom {
			if mm.Item(tab.Method).Checked(true).Chosen() {
				a.openCustomMethodDialog(tab)
			}
		}
		if mm.Item(bm.CustomMethodMenu).Chosen() {
			a.openCustomMethodDialog(tab)
		}
	})
}

// b03CustomMethodModal renders the custom HTTP method input dialog when open.
func (a *App) b03CustomMethodModal(c *ui.Context, t *ui.Theme) {
	st := a.customMethodUI()
	if !st.open {
		return
	}
	m := a.msg()
	bm := a.bodyMsg()
	modal := ui.Modal(c, &st.open, func() {
		ui.Column(c).Width(380).Gap(10).Padding(16).Children(func() {
			ui.Text(c, bm.CustomMethodTitle).Bold().FontSize(16)
			ui.Text(c, bm.CustomMethodHint).FontSize(12).TextColor(t.TextMuted)
			inp := ui.TextInput(c, &st.draft).FillWidth().Font("monospace").Placeholder(bm.CustomMethodPlaceholder)
			if inp.Submitted() {
				a.submitCustomMethodDialog()
			}
			if st.errMsg != "" {
				ui.Text(c, st.errMsg).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					st.open = false
				}
				if uikit.ActionCursor(ui.PrimaryButton(c, bm.ApplyMethod)).Clicked() {
					a.submitCustomMethodDialog()
				}
			})
		})
	})
	if modal.Dismissed() {
		st.open = false
	}
}

func previewQueryKVs(tab *Tab) []core.KV {
	if tab == nil {
		return nil
	}
	var out []core.KV
	for _, r := range tab.QueryRows {
		if r == nil || r.isBlank() {
			continue
		}
		enabled := r.Enabled
		out = append(out, core.KV{
			Name:    r.Name,
			Value:   r.Value,
			Enabled: &enabled,
		})
	}
	return out
}

// b03URLPreview renders the secret-masked variable chips and the single
// "resolved preview" on one line under the URL bar, so the two no longer take
// two rows of height. It previews the URL field alone (its own inline query
// included) and deliberately not the Params table: the full URL with the
// Params query is shown once, at the bottom of Params, and only when the query
// actually changes the final URL. That keeps the two lines from repeating the
// same text.
func (a *App) b03URLPreview(c *ui.Context, t *ui.Theme, tab *Tab) {
	if tab == nil {
		return
	}
	prev := core.PreviewRequestURL(tab.URL, nil, a.resolveTabVars(tab), a.mgrSecrets())
	if len(prev.Variables) == 0 {
		return
	}

	a.urlVarLine(c, t, tab, prev.ResolvedURL)
}

// renderQueryURLPreview shows the full context-encoded & secret-masked URL at
// the bottom of the Params (Query) editor, but only when the Params query
// changes the final URL relative to the URL-only preview above. With no
// Params (or only disabled/blank rows) it renders nothing, so the same URL is
// never printed twice.
func (a *App) renderQueryURLPreview(c *ui.Context, t *ui.Theme, tab *Tab) {
	if tab == nil {
		return
	}
	query := previewQueryKVs(tab)
	if len(query) == 0 {
		return
	}
	vars := a.resolveTabVars(tab)
	secrets := a.mgrSecrets()
	full := core.PreviewRequestURL(tab.URL, query, vars, secrets)
	if full.ResolvedURL == "" {
		return
	}
	base := core.PreviewRequestURL(tab.URL, nil, vars, secrets)
	if full.ResolvedURL == base.ResolvedURL {
		return
	}
	p := uikit.PaletteFor(t)
	bm := a.bodyMsg()
	ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(2, 8).FillWidth().Children(func() {
		ui.Text(c, bm.ResolvedURLWithParamsLabel+":").FontSize(11).FontWeight(600).TextColor(p.Muted)
		ui.Text(c, full.ResolvedURL).Font("monospace").FontSize(11).TextColor(p.Muted).SingleLine().Grow(1)
	})
}

// buildTabPlan constructs an executable core.Plan from a request Tab, wiring
// the project's confined .gorequest/ file opener and device-local file bindings
// while enforcing pre-network attachment checks and upload size limits.
func (a *App) buildTabPlan(ctx context.Context, tab *Tab) (*core.Request, *core.Plan, error) {
	req, plan, _, err := a.buildTrackedTabPlan(ctx, tab)
	return req, plan, err
}

func (a *App) buildTrackedTabPlan(ctx context.Context, tab *Tab) (*core.Request, *core.Plan, *core.Redactor, error) {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	req, err := requestFromTab(tab)
	if err != nil {
		return nil, nil, core.NewRedactor(), err
	}
	var (
		vars        map[string]string
		auth        *core.AuthScheme
		projectRoot string
	)
	if ps != nil && ps.project != nil {
		vars, _, err = ps.project.ResolveVarsWith(tab.Entry, ps.currentEnv(), nil)
		if err != nil {
			return req, nil, core.NewRedactor(), err
		}
		eff, authErr := ps.project.ResolveEffectiveAuth(tab.Entry, req)
		err = authErr
		if eff != nil {
			auth = eff.Scheme
		}
		if err != nil {
			return req, nil, core.NewRedactor(), err
		}
		projectRoot = ps.project.Dir
	}
	plan, red, err := core.BuildTrackedPlanWithOptions(ctx, req, vars, a.mgrSecrets(), auth, nil, core.PlanOptions{
		ProjectRoot:    projectRoot,
		LocalFiles:     a.localFilesFor(ps),
		MaxUploadBytes: core.DefaultMaxUploadBytes,
	})
	if err != nil {
		return req, nil, red, err
	}
	if auth != nil {
		if t := strings.ToLower(strings.TrimSpace(auth.Type)); t == "oauth2" || t == "oidc" {
			if _, err := a.ApplyTabOAuthIfConfigured(ctx, ps, tab, plan, red, true); err != nil {
				return req, nil, red, err
			}
		}
	}
	return req, plan, red, nil
}
