package desktop

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// workspace draws the right side: the project/environment bar and the open tabs
// with their editor and response. A project window always has a loaded project.
func (a *App) workspace(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Column(c).Grow(1).FillHeight().MinWidth(0).Background(p.BG).Children(func() {
		if ps == nil {
			ui.Column(c).Grow(1).Center().Children(func() {
				ui.Text(c, m.NoProjectLoaded).TextColor(p.Muted)
			})
			return
		}
		a.workspaceHeader(c, t, ps)
		a.tabsAndEditor(c, t, ps)
	})
}

// workspaceHeader is the top bar (§2). It holds exactly three groups: the
// project name with its switch dropdown on the left; the environment selector in
// the middle-right; and the New Request pill plus the "+" new menu at the far
// right. The project path line and the old flat row of six buttons are gone —
// every secondary action now lives in a menu.
func (a *App) workspaceHeader(c *ui.Context, t *ui.Theme, ps *projectState) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Row(c).Padding(8, 12).Gap(6).AlignItems(ui.Center).Height(46).
		Background(p.BG).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		// Left: project name + switch dropdown.
		ui.MenuButton(c, ps.name(), func(mm *ui.Menu) { a.buildProjectMenu(c, mm, ps) })
		ui.Box(c).Grow(1)
		// Environment area: a fixed single-line label (never wraps) + selector.
		ui.Text(c, m.EnvLabel).FontSize(12).TextColor(p.Muted).SingleLine().Width(32)
		a.envMenu(c, ps)
		if !ps.canEditEnv() {
			ui.Text(c, m.SelectEnvHint).FontSize(11).TextColor(p.Faint).SingleLine()
		}
		// Far right: the New Request pill and the "+" new menu. Saving lives
		// in the request's own header (§8), not here.
		pill := ui.ButtonBase(c).Height(36).Radius(999).Padding(0, 18).Gap(6).AlignItems(ui.Center).Background(p.Hover)
		pill.Children(func() {
			ui.Icon(c, uikit.IconPlus).FontSize(14).TextColor(p.Text)
			ui.Text(c, m.NewRequest).FontSize(14).FontWeight(600).TextColor(p.Text).SingleLine()
		})
		pill.Label(m.NewRequest).Tooltip(m.NewRequest + " ⌘T")
		if pill.Clicked() {
			a.openNewRequest()
		}
		ui.MenuButton(c, "+", func(mm *ui.Menu) { a.buildNewMenu(mm, ps) })
	})
}

// buildProjectMenu fills the project switcher menu (§2).
func (a *App) buildProjectMenu(c *ui.Context, mm *ui.Menu, ps *projectState) {
	m := a.msg()
	if mm.Item(m.OpenProject).Chosen() {
		a.openProjectPicker()
	}
	if mm.Item(m.NewDoc).Chosen() {
		if de, err := ps.newDoc(); err != nil {
			a.status = translate(m.NewDocFailed, err.Error())
		} else {
			a.status = translate(m.Created, de.RelPath)
		}
	}
	tab := ps.activeTab()
	canMove := tab != nil && tab.Kind == "request"
	if mm.Item(m.MoveEllipsis).Disabled(!canMove).Chosen() {
		a.openMove(tab)
	}
	mm.Separator()
	if mm.Item(m.CopyProjectPath).Chosen() {
		if c != nil {
			c.WriteClipboard(ps.root)
		}
		a.status = m.Copied
	}
	mm.Separator()
	if mm.Item(m.CloseProject).Chosen() {
		a.closeWindow()
	}
}

// buildNewMenu fills the "+" new menu (§2): request / folder / document /
// environment.
func (a *App) buildNewMenu(mm *ui.Menu, ps *projectState) {
	m := a.msg()
	if mm.Item(m.NewRequest).Chosen() {
		a.openNewRequest()
	}
	if mm.Item(m.NewFolder).Chosen() {
		a.openNewFolder()
	}
	if mm.Item(m.NewDoc).Chosen() {
		if de, err := ps.newDoc(); err != nil {
			a.status = translate(m.NewDocFailed, err.Error())
		} else {
			a.status = translate(m.Created, de.RelPath)
		}
	}
	if mm.Item(m.NewEnv).Chosen() {
		a.openNewEnvironment()
	}
}

// envMenu draws the environment selector. Its menu previews the current
// environment's variables and offers switching plus edit/new entries (§2).
func (a *App) envMenu(c *ui.Context, ps *projectState) {
	label := ps.envSel
	if label == "" {
		label = "(none)"
	}
	ui.MenuButton(c, label, func(mm *ui.Menu) {
		m := a.msg()
		if ps.currentEnv() != "" {
			if env := ps.project.Environments[ps.currentEnv()]; env != nil {
				keys := env.SortedVariableKeys()
				for i, k := range keys {
					if i >= 4 {
						break
					}
					mm.Item(fmt.Sprintf("%s = %s", k, env.Variables[k])).Disabled(true)
				}
				if len(keys) > 4 {
					mm.Item(fmt.Sprintf("… (%d)", len(keys))).Disabled(true)
				}
				mm.Separator()
			}
		}
		opts := append([]string{"(none)"}, ps.project.EnvOrder...)
		for _, id := range opts {
			if mm.Item(id).Checked(ps.envSel == id).Chosen() {
				ps.envSel = id
				a.status = translate(m.SwitchedTo, id)
			}
		}
		mm.Separator()
		if mm.Item(m.EditEnv).Disabled(!ps.canEditEnv()).Chosen() {
			a.openEditEnvironment()
		}
		if mm.Item(m.NewEnv).Chosen() {
			a.openNewEnvironment()
		}
	})
}

func (a *App) tabsAndEditor(c *ui.Context, t *ui.Theme, ps *projectState) {
	if len(ps.tabs) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, a.msg().ChooseRequestOrDoc).TextColor(t.TextMuted)
		})
		return
	}
	if ps.active >= len(ps.tabs) {
		ps.active = len(ps.tabs) - 1
	}
	if ps.active < 0 {
		ps.active = 0
	}
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		a.tabBar(c, t, ps)
		tab := ps.activeTab()
		if tab == nil {
			return // The final tab may have been closed by the tab bar this frame.
		}
		if tab.Kind == "request" {
			a.requestView(c, t, tab)
		} else {
			a.docView(c, t, tab)
		}
	})
}

func (a *App) requestView(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	// §9 shortcuts: ⌘↩ sends the active request from anywhere in the request
	// area; ⌘S saves it.
	c.OnShortcut(ui.Cmd, ui.KeyEnter, func() {
		if tb := tab.Owner.activeTab(); tb != nil && tb.Kind == "request" {
			a.trySend(tb)
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveActive() })
	ui.Column(c).Grow(1).FillHeight().Padding(12, 16).Gap(10).Children(func() {
		a.urlGroup(c, t, tab)

		// Build underline tabs (§3): Params / Auth / Headers / Body / Scripts /
		// Tests / Settings, with counts on Params and Headers.
		labels := []string{m.ParamsTab, m.AuthTab, m.HeadersTab, m.BodyTab, m.ScriptsTab, m.TestsTab, m.BuildSettings}
		badges := make([]string, len(labels))
		if n := kvCount(tab.QueryRows); n > 0 {
			badges[0] = badgeCount(n)
		}
		if n := kvCount(tab.HeaderRows); n > 0 {
			badges[2] = badgeCount(n)
		}
		uikit.UnderlineTabs(c, p, &tab.Section, labels, badges)

		split := ui.SplitVerticalPercent(c, &tab.Owner.buildPct, func() {
			ui.Column(c).Grow(1).FillWidth().Gap(4).Children(func() {
				switch tab.Section {
				case 0:
					a.kvForm(c, t, tab, kvQuery)
				case 1:
					a.authEditor(c, t, tab)
				case 2:
					a.kvForm(c, t, tab, kvHeader)
				case 3:
					a.bodyEditor(c, t, tab)
				case 4:
					a.emptyTabHint(c, p, m.NoScriptsHint)
				case 5:
					a.emptyTabHint(c, p, m.NoTestsHint)
				case 6:
					a.buildSettings(c, t, tab, m)
				}
			})
		}, func() {
			a.responseView(c, t, tab)
		}).Grow(1).FillWidth()

		// §7: double-click the splitter to restore the default 64:36, and
		// persist the user's dragged ratio across launches.
		if tab.Owner.buildPct != a.mgr.cfg.buildPct() {
			a.mgr.cfg.setBuildPct(tab.Owner.buildPct)
		}
		if split.DoubleClicked() {
			tab.Owner.buildPct = 64
			a.mgr.cfg.setBuildPct(64)
		}
	})
}

// urlGroup is the joined request line (§3): a method selector (108 wide) and a
// monospace URL input in one rounded group, with the Send button (104×40) as a
// separate accent control 8 DIPs to its right.
func (a *App) urlGroup(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			a.saveControl(c, p, tab)
			grp := ui.Row(c).Grow(1).Height(40).Radius(10).BorderWidth(1).BorderColor(p.Hairline).
				Background(p.BG).AlignItems(ui.Center).Clip()
			grp.Children(func() {
				a.methodPicker(c, p, tab)
				ui.Box(c).Width(1).Height(22).Background(p.Hairline)
				inp := ui.TextInput(c, &tab.URL).Grow(1).Font("monospace")
				inp.OnShortcut(0, ui.KeyEnter, func() { a.trySend(tab) })
				if inp.Changed() {
					tab.Dirty = true
				}
				if tab.focusURL {
					inp.Focus()
					tab.focusURL = false
				}
				if b := uikit.IconButton(c, p, uikit.IconCopy, "Copy URL"); b.Clicked() {
					c.WriteClipboard(tab.URL)
					a.status = m.Copied
				}
			})
			if n := len(a.undefinedFor(tab)); n > 0 && !tab.Sending {
				ui.Text(c, translate(m.UndefinedVars, n)).FontSize(12).
					TextColor(uikit.MethodColor("DELETE", p)).SingleLine()
			}
			if tab.Sending {
				if _, clicked := uikit.SendButton(c, p, true, m.CancelRequest); clicked {
					if tab.cancel != nil {
						tab.cancel()
					}
				}
			} else if _, clicked := uikit.SendButton(c, p, false, m.Send); clicked {
				a.trySend(tab)
			}
		})
		a.urlVarLine(c, t, tab)
	})
}

// methodPicker draws the colored HTTP-method selector (§5, D6): the method in
// its functional color as a pop-up button whose menu lists every method with a
// check on the current one.
func (a *App) methodPicker(c *ui.Context, p uikit.Palette, tab *Tab) {
	row := ui.Row(c).Width(108).Height(40).Padding(0, 10).Gap(4).AlignItems(ui.Center)
	row.Children(func() {
		ui.Text(c, tab.Method).Font("monospace").FontWeight(700).FontSize(13).
			TextColor(uikit.MethodColor(tab.Method, p)).SingleLine()
		ui.Icon(c, uikit.IconChevron).FontSize(11).TextColor(p.Muted)
	})
	row.PopoverMenuButton(func(mm *ui.Menu) {
		for _, meth := range methods {
			if mm.Item(meth).Checked(meth == tab.Method).Chosen() {
				tab.Method = meth
				tab.Dirty = true
			}
		}
	})
}

// saveControl is the request's manual save entry (§8): accent-outlined while
// dirty, plain otherwise, with a ⌘S tooltip.
func (a *App) saveControl(c *ui.Context, p uikit.Palette, tab *Tab) {
	if tab.Kind != "request" {
		return
	}
	m := a.msg()
	btn := ui.Button(c, m.Save).Label(m.Save + " ⌘S").Tooltip(m.Save + " ⌘S")
	if tab.Dirty {
		btn = btn.TextColor(p.Accent)
	}
	if btn.Clicked() {
		a.saveActive()
	}
}

// urlVarLine is the variable resolution preview under the URL (§5, D7): each
// {{name}} shows its value, and an undefined one is flagged in red. (The inline
// highlight inside the editable field itself is a later increment.)
func (a *App) urlVarLine(c *ui.Context, t *ui.Theme, tab *Tab) {
	names := core.TemplateNames(tab.URL)
	if len(names) == 0 {
		return
	}
	p := uikit.PaletteFor(t)
	m := a.msg()
	vars, _, err := a.ps.project.ResolveVars(tab.Entry, a.ps.currentEnv())
	if err != nil || vars == nil {
		vars = map[string]string{}
	}
	danger := uikit.MethodColor("DELETE", p)
	spans := []ui.Span{{Text: m.VarLabel + "  ", Size: 11, Weight: 600, Color: p.Muted}}
	for _, name := range names {
		if rest, ok := strings.CutPrefix(name, "secret."); ok {
			defined := false
			if a.mgr.secrets != nil {
				if _, ok := a.mgr.secrets.Get(rest); ok {
					defined = true
				}
			}
			if defined {
				spans = append(spans, ui.Span{Text: "{{" + name + "}} = (secret)   ", Size: 11, Color: p.Muted})
			} else {
				spans = append(spans, ui.Span{Text: "{{" + name + "}} ?   ", Size: 11, Color: danger,
					Background: danger.Alpha(0.12), Radius: 3})
			}
			continue
		}
		if v, ok := vars[name]; ok {
			spans = append(spans, ui.Span{Text: name + " = " + v + "   ", Size: 11, Color: p.Muted})
		} else {
			spans = append(spans, ui.Span{Text: "{{" + name + "}} ?   ", Size: 11, Color: danger,
				Background: danger.Alpha(0.12), Radius: 3})
		}
	}
	ui.RichText(c, spans...).FillWidth()
}

// emptyTabHint shows a centered faint line for a build tab with no content yet.
func (a *App) emptyTabHint(c *ui.Context, p uikit.Palette, msg string) {
	ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
		ui.Text(c, msg).FontSize(13).TextColor(p.Muted)
	})
}

// buildSettings holds per-request build options (currently the timeout).
func (a *App) buildSettings(c *ui.Context, t *ui.Theme, tab *Tab, m *messages) {
	p := uikit.PaletteFor(t)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.TimeoutLabel).FontSize(12).TextColor(p.Muted)
		if ui.TextInput(c, &tab.Timeout).Width(120).Placeholder("10s").Changed() {
			tab.Dirty = true
		}
	})
}

// kvCount counts the meaningful (non-blank) rows of a KV table for the tab
// badges.
func kvCount(rows []*KVRow) int {
	n := 0
	for _, r := range rows {
		if r != nil && !r.isBlank() {
			n++
		}
	}
	return n
}

// badgeCount renders a tab's count badge like "(3)".
func badgeCount(n int) string { return "(" + strconv.Itoa(n) + ")" }

func (a *App) bodyEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.BodyType).FontSize(12).TextColor(p.Muted)
		if ui.Segmented(c, &tab.BodyIdx, bodyTypes...).Changed() {
			tab.Dirty = true
			tab.bodyEditor = nil // rebuild with the new language
		}
	})
	// A codeView editor with §1.3 syntax colors, rebuilt when the body type
	// changes; edits write back to the tab's BodyText.
	if tab.bodyEditor == nil || tab.bodyBuiltIdx != tab.BodyIdx {
		st := &editor.State{Buffer: editor.NewBuffer(tab.BodyText), Theme: uikit.EditorTheme(t)}
		switch bodyTypes[tab.BodyIdx] {
		case "json":
			st.Buffer.SetLanguage(editor.LanguageNamed("json"))
		case "xml":
			st.Buffer.SetLanguage(editor.LanguageNamed("xml"))
		}
		tab.bodyEditor = st
		tab.bodyBuiltIdx = tab.BodyIdx
	}
	if s := tab.bodyEditor.Buffer.String(); s != tab.BodyText {
		tab.BodyText = s
		tab.Dirty = true
	}
	editor.View(c, tab.bodyEditor).Grow(1).FillWidth()
}

func (a *App) authEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.AuthTab).FontSize(12).TextColor(t.TextMuted)
		if ui.Select(c, &tab.AuthMode, authModes).Changed() {
			tab.AuthIdx = indexOf(authModes, tab.AuthMode)
			tab.Dirty = true
		}
	})
	switch tab.AuthMode {
	case "bearer":
		ui.Text(c, m.TokenHint).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthToken).Changed() {
			tab.Dirty = true
		}
	case "basic":
		ui.Text(c, m.Username).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthUser).Changed() {
			tab.Dirty = true
		}
		ui.Text(c, m.Password).FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthPass).Changed() {
			tab.Dirty = true
		}
	default:
		ui.Text(c, m.NoAuth).TextColor(t.TextMuted)
	}
}

func (a *App) docView(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Segmented(c, &tab.Section, m.SplitView, m.EditView, m.PreviewView)
			ui.Box(c).Grow(1)
			if tab.Dirty {
				if ui.PrimaryButton(c, m.SaveDoc).Clicked() {
					a.saveActive()
				}
			}
		})
		src := tab.DocEditor.Buffer.String()
		switch tab.Section {
		case 1:
			editor.View(c, tab.DocEditor).Grow(1).FillWidth()
		case 2:
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				markdown.View(c, src, nil)
			})
		default:
			docSplit := &tab.Owner.docSplit
			ui.Split(c, docSplit, func() {
				editor.View(c, tab.DocEditor).Grow(1).FillWidth()
			}, func() {
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					markdown.View(c, src, nil)
				})
			}).Grow(1).FillWidth()
		}
	})
}
