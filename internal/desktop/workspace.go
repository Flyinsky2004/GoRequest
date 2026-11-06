package desktop

import (
	"strconv"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

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

func (a *App) workspaceHeader(c *ui.Context, t *ui.Theme, ps *projectState) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).Height(46).
		Background(p.BG).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		ui.Column(c).Gap(1).Shrink(1).MinWidth(0).Clip().Children(func() {
			ui.Text(c, ps.name()).Bold().SingleLine().TextColor(p.Text)
			ui.Text(c, displayPath(ps.root)).FontSize(11).TextColor(p.Muted).SingleLine()
		})
		ui.Box(c).Grow(1)
		ui.Text(c, m.EnvLabel).FontSize(12).TextColor(p.Muted)
		opts := append([]string{"(none)"}, ps.project.EnvOrder...)
		if ui.Select(c, &ps.envSel, opts).Width(150).Changed() {
			a.status = translate(m.EnvironmentStatus, ps.envSel)
		}
		if ui.Button(c, m.NewEnv).Tooltip(m.CreateEnvTip).Clicked() {
			a.openNewEnvironment()
		}
		edit := ui.Button(c, m.EditEnv)
		if ps.canEditEnv() {
			edit = edit.Tooltip(m.EditEnvTip)
		} else {
			edit = edit.Disabled(true).Tooltip(m.SelectEnvTip)
		}
		if edit.Clicked() {
			a.openEditEnvironment()
		}
		if !ps.canEditEnv() {
			ui.Text(c, m.SelectEnvHint).FontSize(11).TextColor(p.Muted)
		}
		if ui.Button(c, m.NewRequest).Clicked() {
			a.openNewRequest()
		}
		if tab := ps.activeTab(); tab != nil && tab.Kind == "request" {
			if ui.Button(c, m.MoveEllipsis).Tooltip(m.MoveTip).Clicked() {
				a.openMove(tab)
			}
		}
		if ui.Button(c, m.NewDoc).Clicked() {
			if de, err := ps.newDoc(); err != nil {
				a.status = translate(m.NewDocFailed, err.Error())
			} else {
				a.status = translate(m.Created, de.RelPath)
			}
		}
		if tab := ps.activeTab(); tab != nil && tab.Dirty {
			if ui.PrimaryButton(c, m.Save).Clicked() {
				a.saveActive()
			}
		}
		if ui.Button(c, m.CloseProject).Tooltip(m.CloseProjectTip).Clicked() {
			a.closeWindow()
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

		ui.SplitVertical(c, &tab.RespSize, func() {
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
	})
}

// urlGroup is the joined request line (§3): a method selector (108 wide) and a
// monospace URL input in one rounded group, with the Send button (104×40) as a
// separate accent control 8 DIPs to its right.
func (a *App) urlGroup(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		grp := ui.Row(c).Grow(1).Height(40).Radius(10).BorderWidth(1).BorderColor(p.Hairline).
			Background(p.BG).AlignItems(ui.Center).Clip()
		grp.Children(func() {
			if ui.Select(c, &tab.Method, methods).Width(108).Changed() {
				tab.Dirty = true
			}
			ui.Box(c).Width(1).Height(22).Background(p.Hairline)
			if ui.TextInput(c, &tab.URL).Grow(1).Font("monospace").Changed() {
				tab.Dirty = true
			}
		})
		if tab.Sending {
			if ui.Button(c, m.CancelRequest).Width(104).Height(40).Radius(10).Clicked() {
				if tab.cancel != nil {
					tab.cancel()
				}
			}
		} else if ui.PrimaryButton(c, m.Send).Width(104).Height(40).Radius(10).Clicked() {
			a.send(tab)
		}
	})
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
