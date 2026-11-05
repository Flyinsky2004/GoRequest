package desktop

import (
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
)

// workspace draws the right side: the current project/environment bar and
// either a welcome empty state or the open tabs with their editor and response.
func (a *App) workspace(c *ui.Context, t *ui.Theme) {
	ps := a.current
	ui.Column(c).Grow(1).FillHeight().MinWidth(0).Children(func() {
		if ps == nil {
			a.welcome(c, t)
			return
		}
		a.workspaceHeader(c, t, ps)
		a.tabsAndEditor(c, t, ps)
	})
}

func (a *App) welcome(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Grow(1).FillWidth().FillHeight().Center().Gap(10).Children(func() {
		ui.Text(c, "Open a project").FontSize(20).Bold()
		ui.Text(c, "Choose a folder that contains a .gorequest/ directory to start.").TextColor(t.TextMuted)
		if ui.PrimaryButton(c, "Open Project…").Clicked() {
			a.pickProject()
		}
	})
}

func (a *App) workspaceHeader(c *ui.Context, t *ui.Theme, ps *projectState) {
	ui.Row(c).Padding(8, 12).Gap(8).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.Column(c).Gap(1).Shrink(1).MinWidth(0).Clip().Children(func() {
			ui.Text(c, ps.name()).Bold().SingleLine()
			ui.Text(c, displayPath(ps.root)).FontSize(11).TextColor(t.TextMuted).SingleLine()
		})
		ui.Box(c).Grow(1)
		ui.Text(c, "Env").FontSize(12).TextColor(t.TextMuted)
		opts := append([]string{"(none)"}, ps.project.EnvOrder...)
		if ui.Select(c, &ps.envSel, opts).Width(150).Changed() {
			a.status = "environment: " + ps.envSel
		}
		if ui.Button(c, "New Env").Tooltip("Create a new environment").Clicked() {
			a.openNewEnvironment()
		}
		edit := ui.Button(c, "Edit Env")
		if ps.canEditEnv() {
			edit = edit.Tooltip("Edit the selected environment")
		} else {
			edit = edit.Disabled(true).Tooltip("Select an environment to edit")
		}
		if edit.Clicked() {
			a.openEditEnvironment()
		}
		if !ps.canEditEnv() {
			ui.Text(c, "Select an environment to edit").FontSize(11).TextColor(t.TextMuted)
		}
		if ui.Button(c, "New Request").Clicked() {
			if e, err := ps.newRequest(); err != nil {
				a.status = "new request: " + err.Error()
			} else {
				a.status = "created " + e.RelPath
			}
		}
		if ui.Button(c, "New Doc").Clicked() {
			if de, err := ps.newDoc(); err != nil {
				a.status = "new doc: " + err.Error()
			} else {
				a.status = "created " + de.RelPath
			}
		}
		if tab := ps.activeTab(); tab != nil && tab.Dirty {
			if ui.PrimaryButton(c, "Save").Clicked() {
				a.saveActive()
			}
		}
	})
}

func (a *App) tabsAndEditor(c *ui.Context, t *ui.Theme, ps *projectState) {
	if len(ps.tabs) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, "Choose a request or document on the left.").TextColor(t.TextMuted)
		})
		return
	}
	if ps.active >= len(ps.tabs) {
		ps.active = len(ps.tabs) - 1
	}
	if ps.active < 0 {
		ps.active = 0
	}
	titles := make([]string, len(ps.tabs))
	for i, tab := range ps.tabs {
		titles[i] = tab.Title
		if tab.Dirty {
			titles[i] += " •"
		}
	}
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Tabs(c, &ps.active, titles...)
		tab := ps.tabs[ps.active]
		if tab.Kind == "request" {
			a.requestView(c, t, tab)
		} else {
			a.docView(c, t, tab)
		}
	})
}

func (a *App) requestView(c *ui.Context, t *ui.Theme, tab *Tab) {
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
				if ui.Button(c, "Cancel").Clicked() {
					if tab.cancel != nil {
						tab.cancel()
					}
				}
			} else if ui.PrimaryButton(c, "Send").Clicked() {
				a.send(tab)
			}
		})
		// Section tabs.
		ui.Segmented(c, &tab.Section, "Params", "Headers", "Body", "Auth")
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
		ui.Text(c, "Body type").FontSize(12).TextColor(t.TextMuted)
		if ui.Segmented(c, &tab.BodyIdx, bodyTypes...).Changed() {
			tab.Dirty = true
		}
	})
	if ui.TextArea(c, &tab.BodyText).Grow(1).FillWidth().Changed() {
		tab.Dirty = true
	}
}

func (a *App) authEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, "Auth").FontSize(12).TextColor(t.TextMuted)
		if ui.Select(c, &tab.AuthMode, authModes).Changed() {
			tab.AuthIdx = indexOf(authModes, tab.AuthMode)
			tab.Dirty = true
		}
	})
	switch tab.AuthMode {
	case "bearer":
		ui.Text(c, "Token (use {{secret.name}} to keep the value out of the file)").FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthToken).Changed() {
			tab.Dirty = true
		}
	case "basic":
		ui.Text(c, "Username").FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthUser).Changed() {
			tab.Dirty = true
		}
		ui.Text(c, "Password").FontSize(12).TextColor(t.TextMuted)
		if ui.TextInput(c, &tab.AuthPass).Changed() {
			tab.Dirty = true
		}
	default:
		ui.Text(c, "No auth.").TextColor(t.TextMuted)
	}
}

func (a *App) docView(c *ui.Context, t *ui.Theme, tab *Tab) {
	ui.Column(c).Grow(1).FillHeight().Children(func() {
		ui.Row(c).Gap(8).Padding(8, 12).AlignItems(ui.Center).BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
			ui.Segmented(c, &tab.Section, "Split", "Edit", "Preview")
			ui.Box(c).Grow(1)
			if tab.Dirty {
				if ui.PrimaryButton(c, "Save Doc").Clicked() {
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
