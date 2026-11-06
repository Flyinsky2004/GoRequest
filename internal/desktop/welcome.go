package desktop

import (
	"os"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// welcomeState is the lightweight project-chooser window: the brand, the
// scrollable recent-project list, an Open Project action, a New Project wizard,
// the personal workspace entry, a Settings entry and a status line. It
// deliberately does not build the request editor workspace.
type welcomeState struct {
	mgr          *Manager
	win          window
	status       string
	sel          string // sidebar selection id ("project:<root>")
	showSettings bool

	// recentFilter narrows the recent list by name or path. Pinned projects are
	// ordered first (see Manager.recentEntries).
	recentFilter string

	// New-project wizard state. The parent is typed or chosen with the native
	// folder picker; creation never overwrites an existing folder.
	newProjOpen   bool
	newProjName   string
	newProjParent string
	newProjErr    string
}

// wire connects the welcome window's lifecycle. Closing it is always allowed;
// if it is the only window the application's default "last window closed"
// behavior quits, and if project windows are still open they keep running.
func (w *welcomeState) wire() {
	w.win.OnClose(func(e *mygo.CloseEvent) {})
	w.win.OnClosed(func() {
		if w.mgr.welcome == w {
			w.mgr.welcome = nil
		}
	})
}

// view builds one frame of the welcome window.
func (w *welcomeState) view(c *ui.Context) {
	mgr := w.mgr
	applyAppearance(c, mgr.cfg)
	t := c.Theme()
	m := w.msg()

	ui.Column(c).Fill().Children(func() {
		// Brand block.
		ui.Column(c).Padding(24, 24, 10, 24).Gap(6).Children(func() {
			ui.Text(c, "GoRequest").FontSize(24).Bold().SingleLine()
			ui.Text(c, m.WelcomeTagline).FontSize(12).TextColor(t.TextMuted).SingleLine()
		})

		// Actions.
		ui.Row(c).Padding(0, 24, 8, 24).Gap(10).AlignItems(ui.Center).Children(func() {
			if uikit.ActionCursor(ui.PrimaryButton(c, m.OpenProject)).Tooltip(m.ChooseFolderTip).Clicked() {
				mgr.pickProject(w.win)
			}
			if uikit.ActionCursor(ui.Button(c, m.NewProject)).Tooltip(m.NewProjectTip).Clicked() {
				w.openNewProject()
			}
			if uikit.ActionCursor(ui.Button(c, m.GlobalWorkspace)).Tooltip(m.GlobalWorkspaceTip).Clicked() {
				if err := mgr.openGlobalWorkspace(); err != nil {
					w.status = translate(m.OpenFailed, err.Error())
				}
			}
			ui.Box(c).Grow(1)
			if uikit.ActionCursor(ui.Button(c, m.Settings)).Clicked() {
				w.showSettings = true
			}
		})

		// Recent projects: searchable, independently scrollable, with per-row
		// open/pin/remove actions.
		ui.Row(c).Padding(12, 24, 4, 24).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.RecentProjects).FontSize(11).Bold().TextColor(t.TextMuted).SingleLine()
			ui.Box(c).Grow(1)
			in := ui.TextInput(c, &w.recentFilter).Width(220).Placeholder(m.RecentSearchPlaceholder)
			in.OnShortcut(0, ui.KeyEscape, func() { w.recentFilter = "" })
		})

		sb := ui.Sidebar(c, &w.sel, func() {
			projects := mgr.recentEntries(w.recentFilter)
			if len(projects) == 0 {
				empty := m.WelcomeEmpty
				if strings.TrimSpace(w.recentFilter) != "" {
					empty = m.RecentNoMatch
				}
				ui.Text(c, empty).FontSize(12).TextColor(t.TextMuted).Padding(2, 8)
				return
			}
			for _, rp := range projects {
				rp := rp
				var icon *ui.SVG
				if mgr.workspacePrefs().pinned(rp.Path) {
					icon = uikit.IconPin
				}
				item := uikit.ActionCursor(ui.SidebarItem(c, "project:"+rp.Path, icon, mgr.recentLabelWithPin(rp)))
				item.PopoverMenu(func(mm *ui.Menu) {
					if mm.Item(m.OpenRow).Chosen() {
						w.openRecent(rp.Path)
					}
					pinned := mgr.workspacePrefs().pinned(rp.Path)
					pinLabel := m.PinRecent
					if pinned {
						pinLabel = m.UnpinRecent
					}
					if mm.Item(pinLabel).Chosen() {
						mgr.pinRecent(rp.Path, !pinned)
					}
					mm.Separator()
					if mm.Item(m.RemoveRecent).Chosen() {
						mgr.forgetRecent(rp.Path)
						if w.sel == "project:"+rp.Path {
							w.sel = ""
						}
						w.status = translate(m.RemovedRecent, rp.Name)
					}
				})
			}
		}).Grow(1).FillWidth().Label(m.RecentProjects)
		if sb.Changed() {
			w.openSelected()
		}

		// Status feedback (open errors, outcomes).
		if w.status != "" {
			ui.Row(c).Padding(6, 24).Clip().BorderWidth(1, 0, 0, 0).BorderColor(t.Border).Children(func() {
				ui.Text(c, w.status).FontSize(12).TextColor(t.TextMuted).SingleLine()
			})
		}
	})

	w.settingsModal(c, t)
	w.newProjectModal(c, t)
}

// openSelected opens the recent project chosen in the list. A failure stays in
// the welcome window with a recoverable message and clears the selection, since
// no project became current.
func (w *welcomeState) openSelected() {
	id := w.sel
	const prefix = "project:"
	if !strings.HasPrefix(id, prefix) {
		return
	}
	w.openRecent(strings.TrimPrefix(id, prefix))
}

func (w *welcomeState) openRecent(path string) {
	if err := w.mgr.openProject(path); err != nil {
		w.status = translate(w.msg().OpenFailed, err.Error())
		w.sel = ""
	}
}

func (w *welcomeState) settingsModal(c *ui.Context, t *ui.Theme) {
	renderSettings(c, t, w.mgr.cfg, w.mgr.workspacePrefs(), &w.showSettings, func(s string) { w.status = s })
}

// openNewProject resets and shows the wizard, defaulting the location to the
// user's home directory.
func (w *welcomeState) openNewProject() {
	home, _ := os.UserHomeDir()
	w.newProjOpen = true
	w.newProjName = ""
	w.newProjParent = home
	w.newProjErr = ""
}

func (w *welcomeState) newProjectModal(c *ui.Context, t *ui.Theme) {
	if !w.newProjOpen {
		return
	}
	m := w.msg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(10).MinWidth(460).Children(func() {
			ui.Text(c, m.NewProjectTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, m.NewProjectHint).FontSize(12).TextColor(p.Muted)
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.NameLabel).FontSize(12).TextColor(p.Muted).Width(80)
				ui.TextInput(c, &w.newProjName).Grow(1).SingleLine().Placeholder(m.ProjectNamePlaceholder).AutoFocus()
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.ProjectLocationLabel).FontSize(12).TextColor(p.Muted).Width(80)
				ui.TextInput(c, &w.newProjParent).Grow(1).SingleLine().Font("monospace")
				if uikit.ActionCursor(ui.Button(c, m.Browse)).Clicked() {
					w.mgr.pickDirectory(w.win, m.ChooseProjectLocation, m.ChooseProjectLocationMsg, func(path string) {
						w.newProjParent = path
					})
				}
			})
			ui.Text(c, m.NewProjectFolderHint).FontSize(11).TextColor(p.Faint)
			if w.newProjErr != "" {
				ui.Text(c, w.newProjErr).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					w.newProjOpen = false
					open = false
				}
				if uikit.ActionCursor(ui.PrimaryButton(c, m.Create)).Clicked() {
					w.createProject()
					open = w.newProjOpen
				}
			})
		})
	})
	if !open {
		w.newProjOpen = false
	}
}

func (w *welcomeState) createProject() {
	m := w.msg()
	if strings.TrimSpace(w.newProjName) == "" {
		w.newProjErr = m.ErrEnterProjectName
		return
	}
	if strings.TrimSpace(w.newProjParent) == "" {
		w.newProjErr = m.ErrEnterProjectLocation
		return
	}
	if _, err := w.mgr.createProjectAt(w.newProjParent, w.newProjName); err != nil {
		w.newProjErr = translate(m.NewProjectFailed, err.Error())
		return
	}
	w.newProjOpen = false
	w.newProjErr = ""
	w.status = translate(m.CreatedProject, w.newProjName)
}
