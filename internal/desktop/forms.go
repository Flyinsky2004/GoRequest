package desktop

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// This file holds the native "new folder", "new request", "move request" and
// dirty-tab close dialogs. Each is a real MyGo modal; their state lives on App
// and is captured per project so a project switch cannot misdirect an action.

// openNewRequest opens the new-request form targeting the currently selected
// folder (or the root when no folder is selected).
func (a *App) openNewRequest() {
	ps := a.ps
	if ps == nil {
		return
	}
	a.formProject = ps
	a.newReqFolder = ps.selectedFolder()
	a.newReqName = ""
	a.formErr = ""
	a.newReqOpen = true
}

// submitNewRequest validates and creates the request, then opens its tab.
func (a *App) submitNewRequest() {
	ps := a.formProject
	if ps == nil {
		a.newReqOpen = false
		return
	}
	name := strings.TrimSpace(a.newReqName)
	if name == "" {
		a.formErr = a.msg().ErrEnterRequestName
		return
	}
	entry, err := ps.createRequestIn(a.newReqFolder, name)
	if err != nil {
		a.formErr = err.Error()
		return
	}
	a.newReqOpen = false
	a.formErr = ""
	a.status = translate(a.msg().Created, entry.RelPath)
}

// openNewFolder opens the new-folder form under the selected folder (or root).
func (a *App) openNewFolder() {
	ps := a.ps
	if ps == nil {
		return
	}
	a.formProject = ps
	a.newFolderParent = ps.selectedFolder()
	a.newFolderName = ""
	a.formErr = ""
	a.newFolderOpen = true
}

// submitNewFolder creates the folder and reveals it in the tree.
func (a *App) submitNewFolder() {
	ps := a.formProject
	if ps == nil {
		a.newFolderOpen = false
		return
	}
	name := strings.TrimSpace(a.newFolderName)
	if name == "" {
		a.formErr = a.msg().ErrEnterFolderName
		return
	}
	node, err := ps.createFolder(a.newFolderParent, name)
	if err != nil {
		a.formErr = err.Error()
		return
	}
	a.newFolderOpen = false
	a.formErr = ""
	a.status = translate(a.msg().CreatedFolder, node.RelPath)
}

// openMove opens the move dialog for the active request tab. Its target defaults
// to the request's current folder.
func (a *App) openMove(tab *Tab) {
	ps := a.ps
	if ps == nil || tab == nil || tab.Kind != "request" || tab.Entry == nil {
		return
	}
	a.moveProject = ps
	a.moveTab = tab
	a.moveErr = ""
	a.moveTarget = "(root)"
	if dir := filepath.ToSlash(filepath.Dir(tab.Entry.RelPath)); dir != "collections" {
		a.moveTarget = collectionRelOf(dir)
	}
	a.moveOpen = true
}

// folderLabel renders a folder path for a dialog "target" line.
func folderLabel(rel string) string {
	if rel == "" || rel == "(root)" {
		return "(root)"
	}
	return collectionRelOf(rel)
}

// formsModals draws whichever of the dialogs is open.
func (a *App) formsModals(c *ui.Context, t *ui.Theme) {
	a.newRequestModal(c, t)
	a.newFolderModal(c, t)
	a.moveModal(c, t)
	a.closeModal(c, t)
}

func (a *App) newRequestModal(c *ui.Context, t *ui.Theme) {
	if !a.newReqOpen {
		return
	}
	m := a.msg()
	ui.Modal(c, &a.newReqOpen, func() {
		ui.Column(c).Width(360).Gap(10).Padding(16).Children(func() {
			ui.Text(c, m.NewRequestTitle).Bold().FontSize(16)
			ui.Text(c, translate(m.TargetFolder, folderLabel(a.newReqFolder))).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, m.NameLabel).FontSize(12).TextColor(t.TextMuted)
			if ui.TextInput(c, &a.newReqName).FillWidth().Placeholder(m.RequestNamePlaceholder).Submitted() {
				a.submitNewRequest()
			}
			if a.formErr != "" {
				ui.Text(c, a.formErr).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.newReqOpen = false
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.Create)).Clicked() {
					a.submitNewRequest()
				}
			})
		})
	})
}

func (a *App) newFolderModal(c *ui.Context, t *ui.Theme) {
	if !a.newFolderOpen {
		return
	}
	m := a.msg()
	ui.Modal(c, &a.newFolderOpen, func() {
		ui.Column(c).Width(360).Gap(10).Padding(16).Children(func() {
			ui.Text(c, m.NewFolderTitle).Bold().FontSize(16)
			ui.Text(c, translate(m.ParentFolder, folderLabel(a.newFolderParent))).FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, m.NameLabel).FontSize(12).TextColor(t.TextMuted)
			in := folderNameInput(c, &a.newFolderName, m.FolderNamePlaceholder, m.FolderNamePlaceholder)
			in.FillWidth().AutoFocus()
			if folderNameSubmit(in, func() { a.newFolderOpen = false }) {
				a.submitNewFolder()
			}
			if a.formErr != "" {
				ui.Text(c, a.formErr).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.newFolderOpen = false
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.CreateFolder)).Clicked() {
					a.submitNewFolder()
				}
			})
		})
	})
}

func (a *App) moveModal(c *ui.Context, t *ui.Theme) {
	if !a.moveOpen {
		return
	}
	ps := a.moveProject
	if ps == nil || a.moveTab == nil {
		a.moveOpen = false
		return
	}
	m := a.msg()
	ui.Modal(c, &a.moveOpen, func() {
		ui.Column(c).Width(400).Gap(10).Padding(16).Children(func() {
			ui.Text(c, m.MoveTitle).Bold().FontSize(16)
			ui.Text(c, m.MoveHint).FontSize(12).TextColor(t.TextMuted)
			uikit.ActionCursor(uikit.Select(c, &a.moveTarget, folderChoices(ps.project), uikit.SameIcon(uikit.IconFolder))).FillWidth()
			if a.moveErr != "" {
				ui.Text(c, a.moveErr).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
					a.moveOpen = false
				}
				if a.moveTab.Dirty {
					if uikit.ActionCursor(uikit.PrimaryButton(c, m.SaveAndMove)).Clicked() {
						a.doMove(true, false)
					}
					if uikit.ActionCursor(ui.Button(c, m.DiscardAndMove)).Clicked() {
						a.doMove(false, true)
					}
				} else {
					if uikit.ActionCursor(uikit.PrimaryButton(c, m.MoveAction)).Clicked() {
						a.doMove(false, false)
					}
				}
			})
		})
	})
}

func (a *App) closeModal(c *ui.Context, t *ui.Theme) {
	f := a.closeFlow
	if f == nil || f.dirty == nil {
		a.closeModalOpen = false
		return
	}
	a.closeModalOpen = true
	msgs := a.msg()
	m := ui.Modal(c, &a.closeModalOpen, func() {
		ui.Column(c).Width(420).Gap(10).Padding(16).Children(func() {
			ui.Text(c, msgs.UnsavedChangesTitle).Bold().FontSize(16)
			ui.Text(c, translate(msgs.UnsavedChangesMsg, f.dirty.Title)).FontSize(12).TextColor(t.TextMuted)
			if f.err != "" {
				ui.Text(c, f.err).FontSize(12).TextColor(t.Danger)
			}
			ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
				if uikit.ActionCursor(ui.Button(c, msgs.Cancel)).Clicked() {
					a.cancelClose()
				}
				if uikit.ActionCursor(ui.Button(c, msgs.Discard)).Clicked() {
					a.resolveCloseDirty(false)
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, msgs.SaveAndClose)).Clicked() {
					a.resolveCloseDirty(true)
				}
			})
		})
	})
	// Escape or a click outside the dialog cancels the remaining close queue
	// rather than leaving content in an ambiguous state.
	if m.Dismissed() {
		a.cancelClose()
	}
}
