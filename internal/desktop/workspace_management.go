package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// This file owns the B02 workbench management layer above the existing tree
// actions: breadcrumbs, the welcome window's new-project and recent-list
// operations, and the project <-> global-workspace transfer preview.

// --- manager: project creation, global workspace, recents ----------------

// createProjectAt scaffolds a project under parent and opens it.
func (m *Manager) createProjectAt(parent, name string) (string, error) {
	root, err := core.CreateProjectAt(parent, name)
	if err != nil {
		return "", err
	}
	// Normalize exactly as openProject does (resolving symlinks such as
	// macOS /var), so the caller and the window registry use one identity.
	root, err = normalizeProjectPath(root)
	if err != nil {
		return "", err
	}
	if err := m.openProject(root); err != nil {
		return "", err
	}
	return root, nil
}

// projectWorkspaces returns the open project windows that are not the global
// scratch workspace, newest last. Transfers use this as their target list.
func (m *Manager) projectWorkspaces() []*App {
	var out []*App
	for _, app := range m.workspaces {
		if app != nil && !app.isGlobal && !app.closed {
			out = append(out, app)
		}
	}
	return out
}

// globalWorkspaceApp returns the open scratch window, if any.
func (m *Manager) globalWorkspaceApp() *App {
	for _, app := range m.workspaces {
		if app != nil && app.isGlobal && !app.closed {
			return app
		}
	}
	return nil
}

// ensureGlobalWorkspaceApp opens the personal workspace when a transfer needs
// it and returns that window. It never touches project files or the network.
func (m *Manager) ensureGlobalWorkspaceApp() (*App, error) {
	prev := m.cfg.lastProject()
	if app := m.globalWorkspaceApp(); app != nil {
		m.activate(app)
		m.keepLastProjectAfterGlobal(prev, app.ps.root)
		return app, nil
	}
	// openGlobalWorkspace restores the previous last project itself.
	if err := m.openGlobalWorkspace(); err != nil {
		return nil, err
	}
	if app := m.globalWorkspaceApp(); app != nil {
		return app, nil
	}
	return nil, fmt.Errorf("global workspace did not open")
}

// pickDirectory shows the native folder chooser parented to w and reports the
// chosen path back on the UI thread. Cancelling changes nothing.
func (m *Manager) pickDirectory(w window, title, message string, done func(string)) {
	var pw *mygo.Window
	if nw, ok := w.(nativeWindower); ok {
		pw = nw.nativeWindow()
	}
	if pw == nil {
		m.setStatusFor(w, m.msg().WindowNotReady)
		return
	}
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:      pw,
			Title:       title,
			ButtonLabel: m.msg().Open,
			Message:     message,
			Directory:   true,
		})
		if err != nil {
			w.Update(func() { m.setStatusFor(w, translate(m.msg().OpenFailed, err.Error())) })
			return
		}
		if len(paths) == 0 {
			return
		}
		w.Update(func() { done(paths[0]) })
	}()
}

// recentEntries returns the recent projects filtered by query, with pinned
// entries first and otherwise preserving recency order.
func (m *Manager) recentEntries(query string) []recentProject {
	recents := m.cfg.recent()
	q := strings.ToLower(strings.TrimSpace(query))
	prefs := m.workspacePrefs()
	out := make([]recentProject, 0, len(recents))
	for _, rp := range recents {
		if q != "" && !strings.Contains(strings.ToLower(rp.Name), q) &&
			!strings.Contains(strings.ToLower(rp.Path), q) {
			continue
		}
		out = append(out, rp)
	}
	sort.SliceStable(out, func(i, j int) bool {
		pi, pj := prefs.pinned(out[i].Path), prefs.pinned(out[j].Path)
		if pi != pj {
			return pi
		}
		return false
	})
	return out
}

// pinRecent toggles a recent project's pinned state. Pinning is device-local
// and only affects ordering in this chooser.
func (m *Manager) pinRecent(path string, pinned bool) {
	m.workspacePrefs().setPinned(path, pinned)
}

// forgetRecent removes a project from the recent list and drops any pin, so a
// forgotten project cannot come back through the pinned set.
func (m *Manager) forgetRecent(path string) {
	m.cfg.removeRecent(path)
	m.workspacePrefs().setPinned(path, false)
	// A forgotten project must not come back through startup restore.
	if m.cfg.lastProject() == path {
		m.cfg.clearLastProject()
	}
}

// --- breadcrumb -----------------------------------------------------------

// workspaceBreadcrumb draws the path of the active request or document:
// project / folders / item. Folder crumbs are clickable targets in the sidebar,
// so the bar is navigation, not a label. It is called once per frame from the
// tab area.
func (a *App) workspaceBreadcrumb(c *ui.Context, t *ui.Theme, tab *Tab) {
	ps := a.ps
	if ps == nil || tab == nil {
		return
	}
	p := uikit.PaletteFor(t)
	m := a.msg()
	ui.Row(c).Height(22).Padding(0, 2).Gap(2).AlignItems(ui.Center).Clip().Children(func() {
		a.breadcrumbCrumb(c, p, ps.name(), func() {
			ps.sel = ""
			ps.expanded = map[string]bool{}
		})
		if tab.Kind == "doc" {
			ui.Text(c, "›").FontSize(11).TextColor(p.Faint)
			ui.Text(c, m.DocumentsGroup).FontSize(11).TextColor(p.Muted).SingleLine()
			ui.Text(c, "›").FontSize(11).TextColor(p.Faint)
			ui.Text(c, tab.Title).FontSize(11).FontWeight(600).TextColor(p.Text).SingleLine()
			return
		}
		if tab.Kind != "request" || tab.Entry == nil {
			return
		}
		folder := folderRelOfPath(tab.Entry.RelPath)
		if folder != "" {
			rel := ""
			for _, seg := range strings.Split(folder, "/") {
				rel = strings.TrimPrefix(rel+"/"+seg, "/")
				label := seg
				if node := findFolderInTree(ps, "collections/"+rel); node != nil && node.Name != "" {
					label = node.Name
				}
				ui.Text(c, "›").FontSize(11).TextColor(p.Faint)
				target := rel
				a.breadcrumbCrumb(c, p, label, func() {
					ps.sel = "folder:collections/" + target
					ps.expandChain("collections/" + target)
				})
			}
		}
		ui.Text(c, "›").FontSize(11).TextColor(p.Faint)
		ui.Text(c, tab.Title).FontSize(11).FontWeight(600).TextColor(p.Text).SingleLine().Tooltip(tab.Entry.RelPath)
	})
}

// breadcrumbCrumb draws one clickable path segment with a hover wash.
func (a *App) breadcrumbCrumb(c *ui.Context, p uikit.Palette, label string, click func()) {
	r := ui.Row(c).Height(20).Padding(0, 5).Radius(4).AlignItems(ui.Center)
	if r.Hovered() {
		r.Background(p.Hover)
	}
	r.Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(p.Muted).SingleLine()
	})
	if r.Clicked() && click != nil {
		click()
	}
}

// --- transfer preview and actions ----------------------------------------

// transferDraft is one pending project <-> global transfer shown as a modal.
type transferDraft struct {
	sourceApp *App
	targetApp *App
	mode      string
	entry     *core.RequestEntry
	plan      *core.RequestTransfer
	dirtyCopy bool
	err       string
}

// appendTransferMenu adds the global/project transfer entries to a request
// row's context menu. A project row offers copy/move to the personal workspace;
// a scratch row offers copy/move into any open project.
func (a *App) appendTransferMenu(mm *ui.Menu, e *core.RequestEntry) {
	if e == nil || e.Request == nil || a.ps == nil {
		return
	}
	m := a.msg()
	if a.isGlobal {
		targets := a.mgr.projectWorkspaces()
		if len(targets) == 0 {
			mm.Item(m.CopyToProject).Disabled(true)
			mm.Item(m.MoveToProject).Disabled(true)
			return
		}
		mm.Submenu(m.CopyToProject, func(sm *ui.Menu) {
			for _, target := range targets {
				t := target
				if sm.Item(t.ps.name()).Chosen() {
					a.beginTransfer(e, t, core.TransferCopy)
				}
			}
		})
		mm.Submenu(m.MoveToProject, func(sm *ui.Menu) {
			for _, target := range targets {
				t := target
				if sm.Item(t.ps.name()).Chosen() {
					a.beginTransfer(e, t, core.TransferMove)
				}
			}
		})
		return
	}
	if mm.Item(m.CopyToGlobal).Chosen() {
		a.transferToGlobal(e, core.TransferCopy)
	}
	if mm.Item(m.MoveToGlobal).Chosen() {
		a.transferToGlobal(e, core.TransferMove)
	}
}

// transferToGlobal resolves (and opens when needed) the personal workspace,
// then starts the preview.
func (a *App) transferToGlobal(e *core.RequestEntry, mode string) {
	target, err := a.mgr.ensureGlobalWorkspaceApp()
	if err != nil {
		a.status = translate(a.msg().TransferFailed, err.Error())
		return
	}
	a.beginTransfer(e, target, mode)
}

// beginTransfer builds the plan and opens its preview. A dirty request can be
// copied only from its saved version (the preview says so) but cannot be moved:
// moving would silently discard the unsaved buffer.
func (a *App) beginTransfer(e *core.RequestEntry, target *App, mode string) {
	if e == nil || target == nil || target.ps == nil {
		return
	}
	if target.ps.project == a.ps.project {
		a.status = a.msg().TransferSameWorkspace
		return
	}
	dirty := a.dirtyTabFor(e)
	if mode == core.TransferMove && dirty != nil {
		a.status = translate(a.msg().TransferDirtyMove, dirty.Title)
		return
	}
	// Keep the request's folder when the destination has the same folder path;
	// otherwise place it at the destination root. The preview shows the result.
	targetCollection := folderRelOfPath(e.RelPath)
	if !target.ps.project.HasCollection(targetCollection) {
		targetCollection = ""
	}
	plan, err := core.PlanRequestTransfer(a.ps.project, target.ps.project, e, targetCollection, mode)
	if err != nil {
		a.status = translate(a.msg().TransferFailed, err.Error())
		return
	}
	a.transfer = &transferDraft{
		sourceApp: a,
		targetApp: target,
		mode:      mode,
		entry:     e,
		plan:      plan,
		dirtyCopy: dirty != nil,
	}
	if a.win != nil {
		a.win.Update(func() {})
	}
}

func (a *App) dirtyTabFor(e *core.RequestEntry) *Tab {
	if a.ps == nil || e == nil {
		return nil
	}
	for _, tab := range a.ps.tabs {
		if tab.Kind == "request" && tab.Dirty && tab.Entry == e {
			return tab
		}
	}
	return nil
}

// applyTransfer commits the prepared transfer and refreshes both windows.
func (a *App) applyTransfer() {
	d := a.transfer
	if d == nil || d.plan == nil {
		return
	}
	if err := d.plan.Apply(); err != nil {
		d.err = err.Error()
		return
	}
	// A move removed the source request: close its clean tab and clear a now
	// dangling selection without touching any other tab.
	if d.mode == core.TransferMove {
		src := d.sourceApp
		if src != nil && src.ps != nil {
			src.closeCleanTabs(src.ps, func(t *Tab) bool { return t.Kind == "request" && t.Entry == d.entry })
			src.clearSelectionIfGone(src.ps)
		}
	}
	if target := d.targetApp; target != nil && target.win != nil && !target.win.IsDestroyed() {
		target.win.Update(func() {})
	}
	rel := d.plan.TargetRel
	a.transfer = nil
	a.status = translate(a.msg().TransferDone, rel)
}

func (a *App) cancelTransfer() {
	a.transfer = nil
}

// transferModal draws the preview. Confirm is disabled for a blocked plan
// (inline credentials) so a real secret or cookie is never copied between
// workspaces.
func (a *App) transferModal(c *ui.Context, t *ui.Theme) {
	d := a.transfer
	if d == nil || d.plan == nil {
		return
	}
	m := a.msg()
	p := uikit.PaletteFor(t)
	open := true
	ui.Modal(c, &open, func() {
		ui.Column(c).Gap(10).MinWidth(520).Children(func() {
			ui.Text(c, m.TransferTitle).FontSize(16).Bold().TextColor(p.Text)
			ui.Text(c, fmt.Sprintf("%s → %s", d.sourceApp.ps.name(), d.targetApp.ps.name())).
				FontSize(12).TextColor(p.Muted)
			ui.Text(c, m.TransferTarget+": "+d.plan.TargetRel).Font("monospace").FontSize(12).
				TextColor(p.Text).SingleLine().Tooltip(d.plan.TargetRel)
			rule := m.TransferIDCopy
			if d.mode == core.TransferMove {
				rule = translate(m.TransferIDMove, d.plan.TargetID)
			} else {
				rule = translate(rule, d.plan.TargetID)
			}
			ui.Text(c, rule).FontSize(12).TextColor(p.Muted)

			if d.dirtyCopy {
				ui.Text(c, m.TransferDirtyCopy).FontSize(12).TextColor(t.TextMuted)
			}
			if len(d.plan.SecretRefs) > 0 {
				ui.Text(c, translate(m.TransferSecrets, strings.Join(d.plan.SecretRefs, ", "))).
					FontSize(11).TextColor(p.Muted)
			}
			if len(d.plan.Warnings) > 0 {
				ui.Scroll(c).MaxHeight(150).Children(func() {
					ui.Column(c).Gap(2).Children(func() {
						for _, w := range d.plan.Warnings {
							col := p.Muted
							if w.Code == core.WarnInlineCredential {
								col = t.Danger
							}
							ui.Text(c, transferWarningText(m, w)).FontSize(11).TextColor(col)
						}
					})
				})
			}
			if d.plan.Blocked {
				ui.Text(c, m.TransferBlocked).FontSize(12).FontWeight(600).TextColor(t.Danger)
			}
			if d.err != "" {
				ui.Text(c, d.err).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if ui.Button(c, m.Cancel).Clicked() {
					a.cancelTransfer()
					open = false
				}
				confirm := ui.PrimaryButton(c, m.TransferConfirm).Disabled(d.plan.Blocked)
				if confirm.Clicked() {
					a.applyTransfer()
					// A failed apply keeps the draft (and its error) open; a
					// successful one cleared it.
					open = a.transfer != nil
				}
			})
		})
	})
	if !open {
		a.cancelTransfer()
	}
}

// transferWarningText localizes one preview finding. Details are names/paths,
// never values.
func transferWarningText(m *messages, w core.TransferWarning) string {
	switch w.Code {
	case core.WarnInlineCredential:
		return translate(m.TransferWarnInline, w.Detail)
	case core.WarnUnresolvedAuth:
		return translate(m.TransferWarnAuth, w.Detail)
	case core.WarnUnresolvedVariable:
		return translate(m.TransferWarnVar, w.Detail)
	case core.WarnMissingDoc:
		return translate(m.TransferWarnDoc, w.Detail)
	case core.WarnLocalFileBinding:
		return translate(m.TransferWarnLocalFile, w.Detail)
	case core.WarnTargetDefaultAuth:
		return translate(m.TransferWarnDefaultAuth, w.Detail)
	default:
		return w.Detail
	}
}

// openGlobalWorkspace loads the personal workspace in its own window. It is a
// regular loaded project (so saving, search and transfer reuse the same engine)
// but it is excluded from the recent-project list and marked isGlobal so its
// menus offer project transfers.
func (m *Manager) openGlobalWorkspace() error {
	prevLast := m.cfg.lastProject()
	dir, err := core.GlobalWorkspaceDir()
	if err != nil {
		return err
	}
	p, err := core.EnsureGlobalWorkspace(dir)
	if err != nil {
		return err
	}
	root := p.DirParent()
	if root == "" {
		return fmt.Errorf("global workspace has no root")
	}
	root, err = normalizeProjectPath(root)
	if err != nil {
		return err
	}
	if app := m.findWorkspace(root); app != nil {
		app.isGlobal = true
		m.cfg.removeRecent(root)
		m.activate(app)
		m.keepLastProjectAfterGlobal(prevLast, root)
		return nil
	}
	if err := m.openProject(root); err != nil {
		return err
	}
	app := m.findWorkspace(root)
	if app == nil {
		return fmt.Errorf("global workspace window did not open")
	}
	app.isGlobal = true
	// The scratch area is not a project the user picked; keep it out of recents.
	m.cfg.removeRecent(root)
	if app.win != nil {
		app.win.SetTitle("GoRequest — " + m.msg().GlobalWorkspaceTitle)
	}
	app.status = m.msg().GlobalWorkspaceOpened
	m.keepLastProjectAfterGlobal(prevLast, root)
	return nil
}

// keepLastProjectAfterGlobal prevents the scratch workspace from becoming the
// startup target: opening/focusing it must not replace the last real project,
// and if it was the only recorded path the record is cleared.
func (m *Manager) keepLastProjectAfterGlobal(prevLast, globalRoot string) {
	if prevLast == "" || prevLast == globalRoot {
		m.cfg.clearLastProject()
		return
	}
	m.cfg.setLastProject(prevLast)
}

// --- welcome helpers ------------------------------------------------------

// recentLabelWithPin keeps one label shape for pinned and unpinned entries; the
// pin icon (not a text prefix) marks a pinned row, so the label stays stable
// for assistive technology and the existing chooser tests.
func (m *Manager) recentLabelWithPin(rp recentProject) string {
	return m.recentLabel(rp)
}
