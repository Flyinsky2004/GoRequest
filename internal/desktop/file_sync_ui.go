package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// fileSyncTabBanner renders a warning bar above the active editor tab whenever
// the tab has an unresolved three-version external sync conflict or its backing
// file has unresolved Git conflict markers on disk. When no conflict applies,
// it emits zero UI elements and requests zero animation frames.
func (a *App) fileSyncTabBanner(c *ui.Context, t *ui.Theme, ps *projectState, tab *Tab) {
	if a == nil || ps == nil || tab == nil {
		return
	}
	conflict := a.ConflictForTab(tab)
	gitConflictOnDisk := a.tabHasDiskGitConflict(ps, tab)
	if conflict == nil && !gitConflictOnDisk {
		return
	}
	ctrl := peekFileSync(ps)
	modalActiveForTab := ctrl != nil && ctrl.modalOpen && ctrl.modalTab == tab

	fm := a.fileSyncMsg()
	p := uikit.PaletteFor(t)
	// A plain on-disk deletion is expected unsaved-edit protection, not an
	// error: use the warning tint. Only real Git conflict markers use danger.
	warnColor := t.Warning
	if gitConflictOnDisk || (conflict != nil && conflict.HasGitConflict) {
		warnColor = t.Danger
	}

	ui.Column(c).FillWidth().Padding(8, 14).Gap(6).
		Background(warnColor.Alpha(0.10)).
		BorderWidth(0, 0, 1, 0).BorderColor(warnColor.Alpha(0.35)).
		Children(func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				var summary string
				switch {
				case conflict == nil:
					summary = fm.BannerGitConflict
				case conflict.ConflictKind == core.ConflictDeleted:
					summary = translate(fm.BannerDeleted, conflict.RelPath)
				case conflict.ConflictKind == core.ConflictRenamedModified:
					summary = translate(fm.BannerRenamedModified, conflict.OldPath, conflict.RelPath)
				default:
					summary = translate(fm.BannerModified, conflict.RelPath)
				}
				ui.Text(c, summary).Grow(1).MinWidth(180).FontSize(12).FontWeight(600).TextColor(p.Text)

				if conflict != nil && !modalActiveForTab {
					if fileSyncButton(c, t, fileSyncBtnSecondary, fm.ResolveConflictBtn, fileSyncIconCompare, true, false).Clicked() {
						a.OpenConflictModal(tab)
					}
					// Destructive resolutions must never run as a one-click
					// silent shortcut from the banner while unsaved edits
					// exist: route the user through the three-version modal.
					// Only the non-destructive "Save As Copy" stays inline.
					if conflict.DiskExists {
						if conflict.ParseErr == "" && !conflict.HasGitConflict {
							if fileSyncButton(c, t, fileSyncBtnSecondary, fm.ReloadDiskBtn, fileSyncIconReload, true, false).Clicked() {
								a.OpenConflictModal(tab)
							}
						}
					}
					if fileSyncButton(c, t, fileSyncBtnSecondary, fm.SaveAsCopyBtn, fileSyncIconCopy, true, false).Clicked() {
						if err := a.ResolveTabConflict(tab, core.ResolutionSaveAsCopy); err != nil {
							setFileSyncBannerErr(tab, err.Error())
						} else {
							setFileSyncBannerErr(tab, "")
						}
					}
					if !conflict.DiskExists {
						if fileSyncButton(c, t, fileSyncBtnDanger, fm.AcceptDeleteReviewBtn, fileSyncIconTrash, true, false).Clicked() {
							a.OpenConflictModal(tab)
						}
					}
				}
			})

			if conflict != nil && conflict.HasGitConflict {
				ui.Text(c, fm.BannerGitConflict).FontSize(11).TextColor(t.Danger)
			}
			if msg := fileSyncBannerErr(tab); msg != "" && conflict != nil {
				ui.Text(c, msg).FontSize(11).TextColor(t.Danger)
			}
		})
}

// Banner-level resolution errors (Save As Copy runs inline, outside the modal
// whose modalErr line would otherwise show them). UI-thread only.
var fileSyncBannerErrs = map[*Tab]string{}

func setFileSyncBannerErr(tab *Tab, msg string) {
	if tab == nil {
		return
	}
	if msg == "" {
		delete(fileSyncBannerErrs, tab)
		return
	}
	fileSyncBannerErrs[tab] = msg
}

func fileSyncBannerErr(tab *Tab) string { return fileSyncBannerErrs[tab] }

func (a *App) tabHasDiskGitConflict(ps *projectState, tab *Tab) bool {
	if ps == nil || ps.project == nil || tab == nil {
		return false
	}
	rel := tabRelPath(tab)
	if rel == "" {
		return false
	}
	for _, iss := range ps.project.Issues {
		if (iss.Code == "git-conflict" || iss.Code == "doc-git-conflict") && iss.Path == rel {
			return true
		}
	}
	return false
}

// fileSyncModal renders the native MyGo three-version conflict comparison and
// merge dialog when open for ps. When closed, it emits zero UI elements.
func (a *App) fileSyncModal(c *ui.Context, t *ui.Theme, ps *projectState) {
	if a == nil || ps == nil {
		return
	}
	ctrl := peekFileSync(ps)
	if ctrl == nil || !ctrl.modalOpen || ctrl.modalConflict == nil || ctrl.modalTab == nil {
		return
	}
	if ps.indexOfTab(ctrl.modalTab) < 0 {
		ctrl.closeModal()
		return
	}

	a.syncFileSyncModalTheme(ctrl, t)

	fm := a.fileSyncMsg()
	p := uikit.PaletteFor(t)
	conflict := ctrl.modalConflict
	tab := ctrl.modalTab

	modal := ui.Modal(c, &ctrl.modalOpen, func() {
		winW, winH := c.Size()
		panelW := max(float32(320), min(float32(960), float32(winW)-48))
		panelH := max(float32(360), min(float32(600), float32(winH)-48))
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(16).Children(func() {
			// Header: conflict title + view mode selector.
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				title := fm.ModalTitleModified
				switch conflict.ConflictKind {
				case core.ConflictDeleted:
					title = fm.ModalTitleDeleted
				case core.ConflictRenamedModified:
					title = fm.ModalTitleRenamed
				}
				ui.Text(c, title).Bold().FontSize(16).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if conflict.DiskExists {
					uikit.NativeSegmented(c, &ctrl.modalMode, fm.ModeThreeVersions, fm.ModeBaseToDisk, fm.ModeDiskToEdited)
				}
			})

			// File path & disk diagnostic line.
			if conflict.OldPath != "" && conflict.OldPath != conflict.RelPath {
				ui.Text(c, translate(fm.RenamedPathSummary, conflict.OldPath, conflict.RelPath)).
					FontSize(12).TextColor(p.Muted)
			} else {
				ui.Text(c, translate(fm.PathSummary, conflict.RelPath)).
					FontSize(12).TextColor(p.Muted)
			}
			if conflict.ConflictKind == core.ConflictDeleted && !conflict.DiskExists {
				ui.Text(c, fm.DeletedHint).FontSize(12).TextColor(t.Warning)
			}
			if strings.TrimSpace(conflict.ParseErr) != "" {
				ui.Text(c, translate(fm.DiskParseWarning, conflict.ParseErr)).
					FontSize(12).TextColor(t.Danger)
			} else if conflict.HasGitConflict {
				ui.Text(c, fm.BannerGitConflict).FontSize(12).TextColor(t.Danger)
			}

			// Comparison / merge content area.
			mode := ctrl.modalMode
			if !conflict.DiskExists {
				mode = 0
			}
			switch mode {
			case 1:
				a.renderDiffModalPane(c, p, fm.BaselinePaneTitle+"  ↔  "+fm.DiskPaneTitle, ctrl.baseDiskDiff)
			case 2:
				a.renderDiffModalPane(c, p, fm.DiskPaneTitle+"  ↔  "+fm.EditedPaneTitle, ctrl.diskMergeDiff)
			default:
				a.renderThreeVersionPanes(c, p, fm, ctrl, conflict)
			}

			// Merge notes & validation error feedback.
			if ctrl.modalNote != "" {
				noteCol := p.Muted
				if ctrl.modalNoteWarn {
					noteCol = t.Warning
				}
				ui.Text(c, ctrl.modalNote).FontSize(12).TextColor(noteCol)
			}
			if ctrl.modalErr != "" {
				ui.Text(c, ctrl.modalErr).FontSize(12).TextColor(t.Danger)
			}

			// Action bar: wraps in narrow windows. The red primary action is only
			// "Accept Delete"; "Restore File" is a secondary action.
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Wrap().Children(func() {
				if fileSyncButton(c, t, fileSyncBtnGhost, fm.KeepEditingLater, fileSyncIconLater, false, false).Clicked() {
					a.CloseConflictModal()
				}
				if conflict.DiskExists {
					if fileSyncButton(c, t, fileSyncBtnSecondary, fm.AutoMergeThreeWay, fileSyncIconMerge, false, false).Clicked() {
						a.RunThreeWayMergeInModal()
					}
				}
				ui.Box(c).Grow(1)
				if conflict.DiskExists {
					if conflict.ParseErr == "" && !conflict.HasGitConflict {
						if fileSyncButton(c, t, fileSyncBtnSecondary, fm.ReloadDiskBtn, fileSyncIconReload, false, false).Clicked() {
							_ = a.ResolveTabConflict(tab, core.ResolutionReloadDisk)
						}
					}
				}
				if fileSyncButton(c, t, fileSyncBtnSecondary, fm.SaveAsCopyBtn, fileSyncIconCopy, false, false).Clicked() {
					_ = a.ResolveTabConflict(tab, core.ResolutionSaveAsCopy)
				}
				if conflict.DiskExists {
					if fileSyncButton(c, t, fileSyncBtnAccent, fm.SaveResolution, fileSyncIconSave, false, false).Clicked() {
						_ = a.ResolveTabConflict(tab, core.ResolutionApplyMerged)
					}
				} else {
					if fileSyncButton(c, t, fileSyncBtnSecondary, fm.RestoreDeleted, fileSyncIconRestore, false, false).Clicked() {
						_ = a.ResolveTabConflict(tab, core.ResolutionApplyMerged)
					}
					if fileSyncButton(c, t, fileSyncBtnDanger, fm.AcceptDeleteBtn, fileSyncIconTrash, false, false).Clicked() {
						_ = a.ResolveTabConflict(tab, core.ResolutionAcceptDelete)
					}
				}
			})
		})
	})

	if modal.Dismissed() {
		ctrl.closeModal()
	}
}

func (a *App) renderThreeVersionPanes(
	c *ui.Context,
	p uikit.Palette,
	fm *fileSyncMessages,
	ctrl *fileSyncController,
	conflict *core.SyncConflict,
) {
	ui.Row(c).Grow(1).FillWidth().Gap(8).MinHeight(0).Children(func() {
		// 1. Reading Baseline (read-only)
		ui.Column(c).Grow(1).FillHeight().MinWidth(0).Radius(6).
			BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
			paneTitleBar(c, p, fm.BaselinePaneTitle, false)
			if len(conflict.BaselineBytes) == 0 || ctrl.baseEditor == nil {
				ui.Column(c).Grow(1).FillWidth().Padding(12).Center().Children(func() {
					ui.Text(c, fm.BaselineUnavailable).FontSize(12).TextColor(p.Muted)
				})
			} else {
				editor.View(c, ctrl.baseEditor).Grow(1).FillWidth()
			}
		})

		// 2. Disk Version (read-only or deleted notice)
		ui.Column(c).Grow(1).FillHeight().MinWidth(0).Radius(6).
			BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
			paneTitleBar(c, p, fm.DiskPaneTitle, false)
			if !conflict.DiskExists || ctrl.diskEditor == nil {
				ui.Column(c).Grow(1).FillWidth().Padding(12).Center().Children(func() {
					ui.Text(c, fm.DeletedOnDiskEmpty).FontSize(12).TextColor(p.Muted)
				})
			} else {
				editor.View(c, ctrl.diskEditor).Grow(1).FillWidth()
			}
		})

		// 3. Edited / Merge Result (editable)
		ui.Column(c).Grow(1).FillHeight().MinWidth(0).Radius(6).
			BorderWidth(1).BorderColor(p.InputBorder).Clip().Children(func() {
			paneTitleBar(c, p, fm.EditedPaneTitle, true)
			if ctrl.mergeEditor != nil {
				editor.View(c, ctrl.mergeEditor).Grow(1).FillWidth()
			}
		})
	})
}

func (a *App) renderDiffModalPane(c *ui.Context, p uikit.Palette, heading string, ds *editor.DiffState) {
	ui.Column(c).Grow(1).FillWidth().MinHeight(0).Radius(6).
		BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
		paneTitleBar(c, p, heading, false)
		if ds != nil {
			editor.DiffView(c, ds).Grow(1).FillWidth()
		}
	})
}

func paneTitleBar(c *ui.Context, p uikit.Palette, title string, accent bool) {
	col := p.Muted
	if accent {
		col = p.Focus
	}
	ui.Row(c).FillWidth().Padding(6, 10).
		Background(p.Hover).
		BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
		Children(func() {
			ui.Text(c, title).FontSize(11).FontWeight(600).TextColor(col).SingleLine()
		})
}

// syncFileSyncModalTheme restyles the conflict modal's cached editors when the
// app's theme generation moved. The modal's five states are created together,
// so one generation covers them; only Theme changes, so buffers, selections and
// scroll positions survive an appearance switch.
func (a *App) syncFileSyncModalTheme(ctrl *fileSyncController, t *ui.Theme) {
	if ctrl == nil {
		return
	}
	cur := a.themeGeneration()
	if ctrl.modalThemeGen == cur && ctrl.baseEditor != nil && ctrl.baseEditor.Theme != nil {
		return
	}
	syncModalEditorThemes(ctrl, uikit.EditorTheme(t))
	ctrl.modalThemeGen = cur
}

func syncModalEditorThemes(ctrl *fileSyncController, edTheme *editor.Theme) {
	if ctrl == nil || edTheme == nil {
		return
	}
	if ctrl.baseEditor != nil {
		ctrl.baseEditor.Theme = edTheme
	}
	if ctrl.diskEditor != nil {
		ctrl.diskEditor.Theme = edTheme
	}
	if ctrl.mergeEditor != nil {
		ctrl.mergeEditor.Theme = edTheme
	}
	if ctrl.baseDiskDiff != nil {
		ctrl.baseDiskDiff.Theme = edTheme
	}
	if ctrl.diskMergeDiff != nil {
		ctrl.diskMergeDiff.Theme = edTheme
	}
}
