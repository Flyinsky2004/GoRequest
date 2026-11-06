package desktop

import (
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
	ui.Column(c).Grow(1).FillHeight().Padding(10, 12).Gap(8).Children(func() {
		// Method + URL + send.
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			if ui.Select(c, &tab.Method, methods).Changed() {
				tab.Dirty = true
			}
			if ui.TextInput(c, &tab.URL).Grow(1).Changed() {
				tab.Dirty = true
			}
			if ui.TextInput(c, &tab.Timeout).Width(70).Changed() {
				tab.Dirty = true
			}
			if tab.Sending {
				if ui.Button(c, m.CancelRequest).Clicked() {
					if tab.cancel != nil {
						tab.cancel()
					}
				}
			} else if ui.PrimaryButton(c, m.Send).Clicked() {
				a.send(tab)
			}
		})
		// Section tabs.
		ui.Segmented(c, &tab.Section, m.ParamsTab, m.HeadersTab, m.BodyTab, m.AuthTab)
		// Editor above, response below.
		ui.SplitVertical(c, &tab.RespSize, func() {
			ui.Column(c).Grow(1).FillWidth().Gap(4).Children(func() {
				switch tab.Section {
				case 0:
					a.kvForm(c, t, tab, kvQuery)
				case 1:
					a.kvForm(c, t, tab, kvHeader)
				case 2:
					a.bodyEditor(c, t, tab)
				case 3:
					a.authEditor(c, t, tab)
				}
			})
		}, func() {
			a.responseView(c, t, tab)
		}).Grow(1).FillWidth()
	})
}

func (a *App) bodyEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, a.msg().BodyType).FontSize(12).TextColor(t.TextMuted)
		if ui.Segmented(c, &tab.BodyIdx, bodyTypes...).Changed() {
			tab.Dirty = true
		}
	})
	if ui.TextArea(c, &tab.BodyText).Grow(1).FillWidth().Changed() {
		tab.Dirty = true
	}
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
