package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// This file renders the URL variable strip (§5, D7). The editable text input
// cannot highlight individual {{variables}}, so the resolution surface is a
// compact bar of chips under the URL: each chip shows the name, the value it
// resolves to and the layer that supplies it, and an unresolved name is a red
// chip that opens the environment editor.

// varSourceKind identifies the layer that supplies a resolved variable's
// effective value. The constants follow core.MergeVars' precedence: a later
// layer overrides an earlier one, so a name defined in several layers reports
// the layer whose value actually wins.
type varSourceKind int

const (
	varSourceNone varSourceKind = iota
	varSourceGlobal
	varSourceEnv
	varSourceFolder
	varSourceRequest
	varSourceSecret
)

// varSource is where a resolved variable's displayed value comes from.
type varSource struct {
	kind  varSourceKind
	envID string
}

// label is the short source tag shown next to a chip. It stays in English so
// the strip needs no new translation catalog entries; user data such as the
// environment id is appended verbatim.
func (s varSource) label() string {
	switch s.kind {
	case varSourceGlobal:
		return "global"
	case varSourceEnv:
		if s.envID != "" {
			return "env " + s.envID
		}
		return "env"
	case varSourceFolder:
		return "folder"
	case varSourceRequest:
		return "request"
	case varSourceSecret:
		return "secret"
	default:
		return ""
	}
}

// varChip is one URL variable as the strip renders it.
type varChip struct {
	Name    string
	Value   string
	Defined bool
	Secret  bool
	Source  varSource
}

// urlVarLine renders the variable strip under the URL (§5, D7): resolved names
// as "{{name}} = value" chips with a faint source tag, undefined ones as red
// chips that open the environment editor. Sending is still guarded separately
// by trySend; this bar is display and navigation only.
//
// The resolved preview shares this row instead of taking its own line, which
// saves one row of height. The row never wraps: the labels keep their width,
// the chips shrink, and the preview text is single-line and grows into
// whatever room is left, so many chips and a long URL in a narrow window
// ellipsize instead of running past the window edge.
func (a *App) urlVarLine(c *ui.Context, t *ui.Theme, tab *Tab, resolvedURL string) {
	chips := a.varChipsFor(tab)
	if len(chips) == 0 {
		return
	}
	p := uikit.PaletteFor(t)
	m := a.msg()
	bm := a.bodyMsg()
	ui.Row(c).Gap(6).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.VarLabel).FontSize(11).FontWeight(600).TextColor(p.Muted).SingleLine().Shrink(0)
		for _, chip := range chips {
			a.drawVarChip(c, p, m, chip)
		}
		if resolvedURL == "" {
			return
		}
		ui.Text(c, bm.ResolvedURLPreviewLabel+":").FontSize(11).FontWeight(600).TextColor(p.Muted).SingleLine().Shrink(0)
		ui.Text(c, resolvedURL).Font("monospace").FontSize(11).TextColor(p.Muted).SingleLine().Grow(1)
	})
}

// drawVarChip draws one variable chip. An undefined chip is red and clicking it
// opens the environment editor (or a new-environment draft when none is
// selected).
func (a *App) drawVarChip(c *ui.Context, p uikit.Palette, m *messages, chip varChip) {
	danger := uikit.MethodColor("DELETE", p)
	e := ui.Row(c).Height(20).Radius(4).Padding(0, 6).Gap(4).AlignItems(ui.Center)
	text := "{{" + chip.Name + "}} = " + chip.Value
	if chip.Defined {
		e = e.Background(p.Hover)
	} else {
		e = e.Background(danger.Alpha(0.12)).Cursor(ui.CursorPointer)
		text = "{{" + chip.Name + "}} ?"
	}
	e.Children(func() {
		if chip.Defined {
			ui.Text(c, text).FontSize(11).TextColor(p.Text).SingleLine()
			if label := chip.Source.label(); label != "" {
				ui.Text(c, label).FontSize(10).TextColor(p.Faint).SingleLine()
			}
			return
		}
		ui.Text(c, text).FontSize(11).TextColor(danger).SingleLine()
	})
	if chip.Defined {
		tip := chip.Name + " = " + chip.Value
		if label := chip.Source.label(); label != "" {
			tip += " (" + label + ")"
		}
		e.Tooltip(tip)
		return
	}
	e.Tooltip(translate(m.UndefinedVars, 1) + " · " + m.GoDefine)
	if e.Clicked() {
		a.openVariableDefinition()
	}
}

// openVariableDefinition opens the editor that can define a missing variable:
// the selected environment when there is one, otherwise a new environment
// draft. With no project loaded it does nothing.
func (a *App) openVariableDefinition() {
	if a.ps == nil {
		return
	}
	if a.ps.canEditEnv() {
		a.openEditEnvironment()
		return
	}
	a.openNewEnvironment()
}

// varChipsFor resolves the URL's template names against the current layered
// variables for display. order is the first-seen order from the URL.
func (a *App) varChipsFor(tab *Tab) []varChip {
	if tab == nil {
		return nil
	}
	names := core.TemplateNames(tab.URL)
	if len(names) == 0 {
		return nil
	}
	vars := map[string]string{}
	var env *core.Environment
	if a.ps != nil && a.ps.project != nil {
		merged, e, err := a.ps.project.ResolveVarsWith(tab.Entry, a.ps.currentEnv(), nil)
		if err == nil {
			env = e
			if merged != nil {
				vars = merged
			}
		}
	}
	sources := varSourcesFor(a.ps, tab.Entry, env)
	chips := make([]varChip, 0, len(names))
	for _, name := range names {
		chip := varChip{Name: name}
		if rest, ok := strings.CutPrefix(name, "secret."); ok {
			chip.Secret = true
			_, chip.Defined = a.secretsProvider().Get(rest)
			if chip.Defined {
				chip.Value = "(secret)"
				chip.Source = varSource{kind: varSourceSecret}
			}
			chips = append(chips, chip)
			continue
		}
		if v, ok := vars[name]; ok {
			chip.Defined = true
			chip.Value = v
			chip.Source = sources[name]
		}
		chips = append(chips, chip)
	}
	return chips
}

// varSourcesFor maps every known variable name to the layer that supplies its
// effective value. The assignment order mirrors core.MergeVars — global, then
// environment, then folder, then request — so the label always matches the
// value the build will actually use.
func varSourcesFor(ps *projectState, entry *core.RequestEntry, env *core.Environment) map[string]varSource {
	out := map[string]varSource{}
	if ps != nil && ps.project != nil && ps.project.File != nil {
		for k := range ps.project.File.Variables {
			out[k] = varSource{kind: varSourceGlobal}
		}
	}
	if env != nil {
		for k := range env.Variables {
			out[k] = varSource{kind: varSourceEnv, envID: env.ID}
		}
	}
	if entry != nil {
		for k := range entry.CollectionVars {
			out[k] = varSource{kind: varSourceFolder}
		}
		if entry.Request != nil {
			for k := range entry.Request.Variables {
				out[k] = varSource{kind: varSourceRequest}
			}
		}
	}
	return out
}
