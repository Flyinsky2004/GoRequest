package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// sendGuard is a pending "there are undefined variables, send anyway?" decision.
type sendGuard struct {
	tab   *Tab
	names []string
}

// undefinedFor returns the distinct template names used by a tab's URL, query,
// header values and body that the current environment / globals / secrets cannot
// resolve. Empty means sending is safe with respect to variables.
func (a *App) undefinedFor(tab *Tab) []string {
	if tab == nil || tab.Entry == nil || tab.Entry.Request == nil {
		return nil
	}
	vars, _, err := a.ps.project.ResolveVarsWith(tab.Entry, a.ps.currentEnv(), nil)
	if err != nil || vars == nil {
		vars = map[string]string{}
	}
	parts := make([]string, 0, 8+2*len(tab.QueryRows)+2*len(tab.HeaderRows))
	parts = append(parts, tab.URL)
	for _, r := range tab.QueryRows {
		parts = append(parts, r.Name, r.Value)
	}
	for _, r := range tab.HeaderRows {
		parts = append(parts, r.Name, r.Value)
	}
	parts = append(parts, tab.BodyText)
	parts = append(parts, tab.authTemplateStrings(a.ps.project)...)
	seen := map[string]bool{}
	var out []string
	for _, s := range parts {
		for _, name := range core.UndefinedTemplates(s, vars, a.secretsProvider()) {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	return out
}

// trySend sends a tab unless it has undefined variables, in which case it opens
// a confirmation instead of silently shipping literal {{...}} (§5, D7).
func (a *App) trySend(tab *Tab) {
	if tab == nil {
		return
	}
	if err := a.CanSendTab(tab); err != nil {
		return
	}
	if undef := a.undefinedFor(tab); len(undef) > 0 {
		a.sendGuard = &sendGuard{tab: tab, names: undef}
		if a.win != nil && !a.win.IsDestroyed() {
			a.win.Update(func() {})
		}
		return
	}
	a.send(tab)
}

// sendGuardModal renders the undefined-variable confirmation.
func (a *App) sendGuardModal(c *ui.Context, t *ui.Theme) {
	if a.sendGuard == nil {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	open := true
	named := make([]string, len(a.sendGuard.names))
	for i, n := range a.sendGuard.names {
		named[i] = "{{" + n + "}}"
	}
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(12).MinWidth(440).Children(func() {
			ui.Text(c, m.SendGuardTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, translate(m.SendGuardHint, strings.Join(named, ", "))).FontSize(12).TextColor(p.Muted)
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.sendGuard = nil
					open = false
				}
				if uikit.ActionCursor(ui.Button(c, m.GoDefine)).Clicked() {
					a.sendGuard = nil
					open = false
					a.openEditEnvironment()
				}
				if uikit.ActionCursor(ui.PrimaryButton(c, m.SendAnyway)).Clicked() {
					tab := a.sendGuard.tab
					a.sendGuard = nil
					open = false
					a.send(tab)
				}
			})
		})
	})
	if !open && a.sendGuard != nil {
		a.sendGuard = nil
	}
}
