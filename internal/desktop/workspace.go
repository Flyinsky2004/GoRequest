package desktop

import (
	"fmt"
	"strconv"

	"github.com/egoist/mygo/ui"

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
		a.fileSyncModal(c, t, ps)
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
	left, right := windowDragInsets(c, a.sidebarHidden)
	ui.Row(c).Padding(8, right, 8, left).Gap(6).AlignItems(ui.Center).Height(46).
		Background(p.BG).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		// Left: project name + switch dropdown.
		uikit.ActionCursor(ui.MenuButton(c, ps.name(), func(mm *ui.Menu) { a.buildProjectMenu(c, mm, ps) }))
		windowDragSpacer(c, 8)
		// Environment area: a fixed single-line label (never wraps) + selector,
		// then the active tab's save control. The save control is drawn only
		// while the active tab has unsaved changes, so it sits right of the
		// environment selector and disappears once the save succeeds.
		ui.Text(c, m.EnvLabel).FontSize(12).TextColor(p.Muted).SingleLine().Width(32)
		a.envMenu(c, ps)
		a.saveControl(c, p, ps.activeTab())
		if !ps.canEditEnv() {
			ui.Text(c, m.SelectEnvHint).FontSize(11).TextColor(p.Faint).SingleLine()
		}
		// Far right: the New Request pill and the "+" new menu.
		pill := uikit.ActionCursor(ui.ButtonBase(c)).Height(36).Radius(999).Padding(0, 18).Gap(6).AlignItems(ui.Center).Background(p.Hover)
		pill.Children(func() {
			ui.Icon(c, uikit.IconPlus).FontSize(14).TextColor(p.Text)
			ui.Text(c, m.NewRequest).FontSize(14).FontWeight(600).TextColor(p.Text).SingleLine()
		})
		pill.Label(m.NewRequest).Tooltip(m.NewRequest + " ⌘T")
		if pill.Clicked() {
			a.openNewRequest()
		}
		headerMenuButton(c, p, "+", func(mm *ui.Menu) { a.buildNewMenu(mm, ps) })
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
	a.BuildInteropProjectMenuItems(c, mm)
	a.BuildSnippetProjectMenuItems(mm)
	a.BuildOpenAPIProjectMenuItems(c, mm)
	a.BuildRunnerProjectMenuItems(mm)
	a.BuildMockProjectMenuItems(mm)
	a.BuildPerformanceProjectMenuItems(mm)
	mm.Separator()
	a.BuildIdentityManagementMenuItems(mm, tab)
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
	if mm.Item(a.wsWorkspaceMsg().NewWebSocket).Chosen() {
		a.openNewWebSocket()
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
	a.BuildInteropNewMenuItems(mm)
}

// openNewWebSocket creates a protocol: websocket request in the selected
// folder (or the root) and opens its main-workspace tab. The tab can be renamed
// in place like any request tab.
func (a *App) openNewWebSocket() {
	ps := a.ps
	if ps == nil {
		return
	}
	entry, err := ps.createWebSocketIn(ps.selectedFolder(), a.wsWorkspaceMsg().NewWebSocketDefaultName)
	if err != nil {
		a.status = err.Error()
		return
	}
	a.status = translate(a.msg().Created, entry.RelPath)
}

// envMenu draws the environment selector. Its menu previews the current
// environment's variables and offers switching plus edit/new entries (§2). The
// menu is drawn inside the window by headerMenuButton, not by the system.
func (a *App) envMenu(c *ui.Context, ps *projectState) {
	p := uikit.PaletteFor(c.Theme())
	label := ps.envSel
	if label == "" {
		label = "(none)"
	}
	headerMenuButton(c, p, label, func(mm *ui.Menu) {
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
		a.fileSyncTabBanner(c, t, ps, tab)
		a.applyEditorPrefs(tab)
		a.workspaceBreadcrumb(c, t, tab)
		switch {
		case isWebSocketTab(tab):
			a.wsTabView(c, t, tab)
		case tab.Kind == "request":
			a.requestView(c, t, tab)
		default:
			a.docView(c, t, tab)
		}
	})
}

func (a *App) requestView(c *ui.Context, t *ui.Theme, tab *Tab) {
	m := a.msg()
	p := uikit.PaletteFor(t)
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	// §9 shortcuts: ⌘↩ sends the active request from anywhere in the request
	// area; ⌘S saves it.
	c.OnShortcut(ui.Cmd, ui.KeyEnter, func() {
		if tb := tab.Owner.activeTab(); tb != nil && tb.Kind == "request" {
			a.trySend(tb)
		}
	})
	c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveActiveSynced() })
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

		// §7 split. The splitter receives a per-frame effective share: the
		// stored preference, reduced only when the window is too short to keep
		// the response pane readable. MyGo's SplitVerticalPercent has no
		// minimum-response-height option, so the guard is applied here.
		splitHeight := ps.splitRef.Bounds(c).H
		eff := clampBuildPctForHeight(ps.buildPct, splitHeight)
		ps.layoutPct = eff
		split := ui.SplitVerticalPercent(c, &ps.layoutPct, func() {
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
			ui.Column(c).Grow(1).FillHeight().Bind(&ps.respRef).Children(func() {
				a.responseView(c, t, tab)
			})
		}).Grow(1).FillWidth().Bind(&ps.splitRef)

		// The splitter writes layoutPct only on a user move. Fold that move
		// into the preference and persist it; a height clamp (eff != buildPct)
		// is never written back. §7: double-click restores the 55:45 default.
		if moved := ps.layoutPct; moved != eff {
			ps.buildPct = applyBuildPctMove(ps.buildPct, eff, moved)
			a.mgr.cfg.setBuildPct(ps.buildPct)
		}
		if split.DoubleClicked() {
			ps.buildPct = defaultBuildPct
			ps.layoutPct = defaultBuildPct
			a.mgr.cfg.setBuildPct(defaultBuildPct)
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
			// The group owns the single border, colored by the last send's
			// outcome. MyGo's TextInput paints its own border with the theme
			// Accent (a red-orange) whenever it has focus, so after a send the
			// URL field looked permanently red even after a success. The
			// look-less base input below has no border of its own; the group
			// supplies the status color and a separate focus ring.
			grp := ui.Row(c).Grow(1).Height(40).Radius(10).BorderWidth(1).
				BorderColor(urlStatusBorderColor(urlSendStateOf(tab), p)).
				Background(p.BG).AlignItems(ui.Center).Clip().Label("request URL group")
			var urlInput ui.Element
			grp.Children(func() {
				a.b03MethodPicker(c, p, tab)
				// R4: the old 22-high Hairline (7% alpha) was nearly invisible
				// against the URL input's surface, so the method segment and the
				// URL read as one field. A full-height step of the existing Faint
				// neutral from the same palette makes the boundary clear without
				// adding a color token.
				ui.Box(c).Width(1).Height(40).Background(methodURLDividerColor(p)).
					Label("method separator")
				// Keep the URL's existing Sidebar surface and padding, but draw
				// them on a plain row instead of a TextInput, whose border would
				// otherwise turn accent-colored on focus.
				urlBox := ui.Row(c).Grow(1).Padding(6, 10).AlignItems(ui.Center).
					Radius(8).Background(p.Sidebar)
				urlBox.Children(func() {
					urlInput = ui.TextInputBase(c, &tab.URL).Grow(1).Font("monospace").Label("request URL")
					urlInput.OnShortcut(0, ui.KeyEnter, func() { a.trySend(tab) })
					if urlInput.Changed() {
						tab.Dirty = true
					}
					if tab.focusURL {
						urlInput.Focus()
						tab.focusURL = false
					}
				})
				if b := uikit.IconButton(c, p, uikit.IconCopy, "Copy URL"); b.Clicked() {
					c.WriteClipboard(tab.URL)
					a.status = m.Copied
				}
			})
			// Focus stays visible independently of the status color. The ring
			// uses the palette's neutral Muted step rather than the theme's
			// accent focus color: the accent is the same red-orange that caused
			// the bug, and a red halo around a successful (green) field would
			// still read as an error.
			grp.DrawOver(func(painter *ui.Painter, r ui.Rect) {
				if urlInput.Focused() {
					const ring = float32(2)
					painter.Stroke(ui.Rect{
						X: r.X - ring - 1, Y: r.Y - ring - 1,
						W: r.W + 2*ring + 2, H: r.H + 2*ring + 2,
					}, p.Muted, 12, ring)
				}
			})
			// R2: the timeout entry is a compact badge in the URL row; the old
			// standalone row is gone, so at most the variable chips and the
			// resolved preview sit between the URL and the section tabs.
			a.renderTimeoutBadge(c, t, tab)
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
		a.b03URLPreview(c, t, tab)
	})
}

// methodURLDividerColor is the method/URL boundary color. It reuses the
// palette's Faint neutral step: the Hairline token (7% alpha) is too close to
// the URL input's surface to read as a boundary on a real display.
func methodURLDividerColor(p uikit.Palette) ui.Color { return p.Faint }

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
	uikit.ActionCursor(row).PopoverMenuButton(func(mm *ui.Menu) {
		for _, meth := range methods {
			if mm.Item(meth).Checked(meth == tab.Method).Chosen() {
				tab.Method = meth
				tab.Dirty = true
			}
		}
	})
}

// saveControl is the active tab's manual save entry in the workspace header
// (§8). It appears only while the active tab has unsaved changes — request tabs
// mark Dirty on edits, document tabs mirror their editor buffer's Modified
// state — and disappears once the save succeeds. It saves through
// saveActiveSynced, the same path as ⌘S, so the file-sync engine and the
// three-version conflict modal stay intact.
func (a *App) saveControl(c *ui.Context, p uikit.Palette, tab *Tab) {
	if tab == nil || !tab.Dirty {
		return
	}
	m := a.msg()
	btn := uikit.ActionCursor(ui.Button(c, m.Save)).TextColor(p.Accent).
		Label(m.Save + " ⌘S").Tooltip(m.Save + " ⌘S")
	if btn.Clicked() {
		a.saveActiveSynced()
	}
}

// emptyTabHint shows a centered faint line for a build tab with no content yet.
func (a *App) emptyTabHint(c *ui.Context, p uikit.Palette, msg string) {
	ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
		ui.Text(c, msg).FontSize(13).TextColor(p.Muted)
	})
}

// buildSettings holds per-request build options and B04 network/transport settings.
func (a *App) buildSettings(c *ui.Context, t *ui.Theme, tab *Tab, m *messages) {
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		a.renderRequestNetworkSettings(c, t, tab)
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
	a.renderBodyEditor(c, t, tab)
}

func (a *App) authEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	a.renderAuthEditor(c, t, tab)
}

func (a *App) docView(c *ui.Context, t *ui.Theme, tab *Tab) {
	a.docWorkspaceView(c, t, tab)
}
