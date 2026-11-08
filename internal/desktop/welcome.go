package desktop

import (
	"errors"
	"os"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/brand"
	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// newProjectMode is the kind of project the creation sheet builds. The two
// modes have different path rules, so the UI keeps the choice explicit instead
// of inferring it from whether the folder happens to be empty.
type newProjectMode int

const (
	// newProjectIndependent creates <parent>/<project name>/.gorequest.
	newProjectIndependent newProjectMode = iota
	// newProjectExisting creates <existing code root>/.gorequest directly.
	newProjectExisting
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
	// folder picker; creation never overwrites an existing folder. The mode
	// chooses between a new project subfolder and an existing code root.
	newProjOpen     bool
	newProjMode     newProjectMode
	newProjName     string
	newProjParent   string
	newProjDir      string
	newProjErr      string
	newProjOpenPath string // existing project offered by "Open It"
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
		// Brand block. The logo goes to the left of the existing wordmark and
		// tagline; it is a small, single decorative image, not a replacement
		// for any functional icon or a backdrop behind the whole window. The
		// text, padding, drag region and actions below are unchanged.
		windowDragRegion(c).Padding(24, 24, 10, 24).Gap(6).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				if logo := brand.Logo(); logo != nil {
					ui.Image(c, logo).Size(48, 48)
				}
				ui.Column(c).Gap(6).Children(func() {
					ui.Text(c, "GoRequest").FontSize(24).Bold().SingleLine()
					ui.Text(c, m.WelcomeTagline).FontSize(12).TextColor(t.TextMuted).SingleLine()
				})
			})
		})

		// Actions.
		ui.Row(c).Padding(0, 24, 8, 24).Gap(10).AlignItems(ui.Center).Children(func() {
			if uikit.ActionCursor(uikit.PrimaryButton(c, m.OpenProject)).Tooltip(m.ChooseFolderTip).Clicked() {
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
	w.mgr.updateModalFor(w.win, c, t)
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
	renderSettingsWithUpdates(c, t, w.mgr.cfg, w.mgr.workspacePrefs(), w.mgr.update, w.win,
		&w.showSettings, func(s string) { w.status = s })
}

// openNewProject resets and shows the wizard, defaulting the location to the
// user's home directory. The independent mode is the default because it is the
// safer choice for a new project; the existing-code mode is one click away.
func (w *welcomeState) openNewProject() {
	home, _ := os.UserHomeDir()
	w.newProjOpen = true
	w.newProjMode = newProjectIndependent
	w.newProjName = ""
	w.newProjParent = home
	w.newProjDir = ""
	w.newProjErr = ""
	w.newProjOpenPath = ""
}

// newProject geometry. The sheet sizes itself from the window instead of
// relying on a fixed width, so a 560×360 welcome window still shows both mode
// cards and the footer: the body scrolls, the action row never does.
//
// The height cap is deliberately generous: the default 720×640 welcome window
// already yields a 560-DIP sheet, and a user-enlarged window grows the sheet to
// 600 DIPs so the two mode cards, the fields, the final-path preview and the
// footer all keep comfortable spacing instead of being compressed.
const (
	newProjectWindowInset = 80 // 40 DIPs of modal margin + 20 DIPs of padding per side
	newProjectMaxWidth    = 640
	newProjectMinWidth    = 360
	newProjectMaxHeight   = 600
	newProjectMinHeight   = 220
)

func newProjectGeometryFor(winW, winH float32) (width, height float32) {
	width = winW - newProjectWindowInset
	if width > newProjectMaxWidth {
		width = newProjectMaxWidth
	}
	if width < newProjectMinWidth {
		width = newProjectMinWidth
	}
	height = winH - newProjectWindowInset
	if height > newProjectMaxHeight {
		height = newProjectMaxHeight
	}
	if height < newProjectMinHeight {
		height = newProjectMinHeight
	}
	// A window smaller than the preferred floor must still contain the sheet.
	if room := winW - newProjectWindowInset; room > 0 && width > room {
		width = room
	}
	if room := winH - newProjectWindowInset; room > 0 && height > room {
		height = room
	}
	return width, height
}

func (w *welcomeState) setNewProjectMode(mode newProjectMode) {
	if w.newProjMode == mode {
		return
	}
	w.newProjMode = mode
	w.newProjErr = ""
	w.newProjOpenPath = ""
}

func (w *welcomeState) newProjectModal(c *ui.Context, t *ui.Theme) {
	if !w.newProjOpen {
		return
	}
	m := w.msg()
	pm := w.projectMsg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		winW, winH := c.Size()
		width, height := newProjectGeometryFor(winW, winH)
		ui.Column(c).Gap(12).Width(width).Height(height).Children(func() {
			ui.Text(c, m.NewProjectTitle).FontSize(16).Bold().TextColor(p.Text).Shrink(0)
			ui.Text(c, m.NewProjectHint).FontSize(12).TextColor(p.Muted).Shrink(0)

			ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
				ui.Column(c).Gap(12).FillWidth().Children(func() {
					ui.Text(c, pm.ModeSection).FontSize(11).FontWeight(600).TextColor(p.Muted).SingleLine()
					ui.Row(c).Gap(10).FillWidth().AlignItems(ui.Stretch).Children(func() {
						w.projectModeCard(c, p, newProjectIndependent, uikit.IconFolder,
							pm.ModeIndependent, pm.ModeIndependentDesc)
						w.projectModeCard(c, p, newProjectExisting, uikit.IconCode,
							pm.ModeExisting, pm.ModeExistingDesc)
					})

					w.newProjectFields(c, p, m, pm)
					w.newProjectPathPreview(c, p, pm)
					ui.Text(c, pm.KeyboardHint).FontSize(11).TextColor(p.Faint)

					if w.newProjErr != "" {
						ui.Text(c, w.newProjErr).FontSize(12).TextColor(t.Danger)
					}
					if w.newProjOpenPath != "" {
						ui.Text(c, pm.ProjectExistsHint).FontSize(11).TextColor(p.Muted)
					}
				})
			})

			ui.Row(c).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
				if w.newProjOpenPath != "" {
					if uikit.ActionCursor(ui.Button(c, pm.OpenExisting)).Clicked() {
						w.openExistingProject()
					}
				}
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					w.newProjOpen = false
					open = false
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.Create)).Clicked() {
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

// projectModeCard draws one large, selectable mode card. Selection is shown by
// the border, a tinted surface and a check icon (never color alone); the card
// is a ButtonBase, so it takes keyboard focus and Enter/Space choose it, and
// the left/right arrows switch modes while the card has focus.
func (w *welcomeState) projectModeCard(c *ui.Context, p uikit.Palette, mode newProjectMode,
	icon *ui.SVG, title, desc string) {
	selected := w.newProjMode == mode
	card := ui.ButtonBase(c).Grow(1).Shrink(1).MinWidth(0).
		AlignItems(ui.Stretch).Justify(ui.Start).Radius(10).Padding(12).
		BorderWidth(1).BorderColor(p.Hairline)
	switch {
	case selected:
		card.BorderWidth(2).BorderColor(p.InputBorder).Background(p.InputFill)
	case card.Hovered():
		card.Background(p.Hover)
	}
	card.Label(title)
	card.Children(func() {
		ui.Column(c).Grow(1).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				if icon != nil {
					ui.Icon(c, icon).FontSize(18).TextColor(p.Text)
				}
				ui.Text(c, title).FontSize(13).FontWeight(600).TextColor(p.Text).SingleLine().Grow(1)
				if selected {
					ui.Icon(c, uikit.IconCheck).FontSize(14).TextColor(p.Focus)
				}
			})
			ui.Text(c, desc).FontSize(11).TextColor(p.Muted)
		})
	})
	if card.Clicked() {
		w.setNewProjectMode(mode)
	}
	card.OnShortcut(0, ui.KeyLeft, func() { w.setNewProjectMode(newProjectIndependent) })
	card.OnShortcut(0, ui.KeyRight, func() { w.setNewProjectMode(newProjectExisting) })
}

// newProjectFields draws the inputs that belong to the chosen mode. The
// independent mode asks for a project name and a parent folder; the
// existing-code mode asks only for the code root, because the folder name is
// the project name and no extra folder is created.
func (w *welcomeState) newProjectFields(c *ui.Context, p uikit.Palette, m *messages, pm *welcomeProjectMessages) {
	if w.newProjMode == newProjectExisting {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, pm.ExistingLocationLabel).FontSize(12).TextColor(p.Muted).Width(88).Shrink(0)
			ui.TextInput(c, &w.newProjDir).Grow(1).SingleLine().Font("monospace")
			if uikit.ActionCursor(ui.Button(c, m.Browse)).Clicked() {
				w.mgr.pickDirectory(w.win, pm.ChooseExistingLocation, pm.ChooseExistingLocationMsg, func(path string) {
					w.newProjDir = path
					w.newProjErr = ""
					w.newProjOpenPath = ""
					if core.HasProject(path) {
						w.newProjOpenPath = path
					}
				})
			}
		})
		ui.Text(c, pm.ExistingLocationHint).FontSize(11).TextColor(p.Faint)
		if dir := strings.TrimSpace(w.newProjDir); dir != "" {
			ui.Text(c, translate(pm.ExistingNameHint, filepath.Base(dir))).
				FontSize(11).TextColor(p.Muted).SingleLine()
		}
		return
	}

	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.NameLabel).FontSize(12).TextColor(p.Muted).Width(88).Shrink(0)
		ui.TextInput(c, &w.newProjName).Grow(1).SingleLine().
			Placeholder(m.ProjectNamePlaceholder).AutoFocus()
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.ProjectLocationLabel).FontSize(12).TextColor(p.Muted).Width(88).Shrink(0)
		ui.TextInput(c, &w.newProjParent).Grow(1).SingleLine().Font("monospace")
		if uikit.ActionCursor(ui.Button(c, m.Browse)).Clicked() {
			w.mgr.pickDirectory(w.win, m.ChooseProjectLocation, m.ChooseProjectLocationMsg, func(path string) {
				w.newProjParent = path
			})
		}
	})
	ui.Text(c, m.NewProjectFolderHint).FontSize(11).TextColor(p.Faint)
}

// newProjectPathPreview shows exactly where .gorequest will be written. It is
// the same computation the creation uses, so the preview cannot drift from the
// real result.
func (w *welcomeState) newProjectPathPreview(c *ui.Context, p uikit.Palette, pm *welcomeProjectMessages) {
	path := w.newProjectFinalPath(pm)
	ui.Column(c).Gap(3).FillWidth().Padding(8, 10).Radius(8).
		Background(p.Sidebar).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Text(c, pm.FinalPathLabel).FontSize(11).FontWeight(600).TextColor(p.Muted).SingleLine()
		ui.Text(c, path).Font("monospace").FontSize(12).TextColor(p.Text).SingleLine().Tooltip(path)
	})
}

func (w *welcomeState) newProjectFinalPath(pm *welcomeProjectMessages) string {
	if w.newProjMode == newProjectExisting {
		dir := strings.TrimSpace(w.newProjDir)
		if dir == "" {
			return pm.FinalPathExistingPlaceholder
		}
		return filepath.Join(dir, ".gorequest")
	}
	if strings.TrimSpace(w.newProjParent) == "" || strings.TrimSpace(w.newProjName) == "" {
		return pm.FinalPathIndependentPlaceholder
	}
	target, err := core.ProjectDirFor(w.newProjParent, w.newProjName)
	if err != nil {
		return pm.FinalPathIndependentPlaceholder
	}
	return filepath.Join(target, ".gorequest")
}

func (w *welcomeState) createProject() {
	m := w.msg()
	pm := w.projectMsg()
	w.newProjErr = ""
	w.newProjOpenPath = ""

	if w.newProjMode == newProjectExisting {
		dir := strings.TrimSpace(w.newProjDir)
		if dir == "" {
			w.newProjErr = pm.ErrChooseExistingDir
			return
		}
		root, err := w.mgr.createProjectInPlace(dir)
		if err != nil {
			w.newProjErr = translate(m.NewProjectFailed, err.Error())
			if errors.Is(err, core.ErrProjectExists) {
				w.newProjOpenPath = dir
			}
			return
		}
		w.newProjOpen = false
		w.status = translate(m.CreatedProject, filepath.Base(root))
		return
	}

	if strings.TrimSpace(w.newProjName) == "" {
		w.newProjErr = m.ErrEnterProjectName
		return
	}
	if strings.TrimSpace(w.newProjParent) == "" {
		w.newProjErr = m.ErrEnterProjectLocation
		return
	}
	root, err := w.mgr.createProjectAt(w.newProjParent, w.newProjName)
	if err != nil {
		w.newProjErr = translate(m.NewProjectFailed, err.Error())
		if errors.Is(err, core.ErrProjectExists) {
			if target, terr := core.ProjectDirFor(w.newProjParent, w.newProjName); terr == nil {
				w.newProjOpenPath = target
			}
		}
		return
	}
	w.newProjOpen = false
	w.newProjErr = ""
	w.status = translate(m.CreatedProject, filepath.Base(root))
}

// openExistingProject opens a project the creation flow refused to overwrite.
func (w *welcomeState) openExistingProject() {
	path := w.newProjOpenPath
	if path == "" {
		return
	}
	if err := w.mgr.openProject(path); err != nil {
		w.newProjErr = translate(w.msg().OpenFailed, err.Error())
		return
	}
	w.newProjOpen = false
	w.newProjErr = ""
	w.newProjOpenPath = ""
}
