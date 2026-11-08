package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// BuildInteropProjectMenuItems appends B08 Import / Export / Undo items to the
// project dropdown menu.
func (a *App) BuildInteropProjectMenuItems(c *ui.Context, mm *ui.Menu) {
	if a == nil || a.ps == nil || mm == nil {
		return
	}
	im := a.interopMsg()
	mm.Separator()
	if mm.Item(im.ImportMenuLabel).Chosen() {
		a.OpenImportModal(ImportSourcePostman)
	}
	if mm.Item(im.ImportCURLMenuLabel).Chosen() {
		a.OpenImportCURLModal()
	}
	if mm.Item(im.ExportMenuLabel).Chosen() {
		a.OpenExportModal(ExportKindPortableProj)
	}
	if mm.Item(im.ExportCURLMenuLabel).Chosen() {
		a.OpenExportCURLForActiveRequest()
	}
	st := a.peekInteropState()
	hasUndo := st != nil && st.LastReceipt != nil
	if mm.Item(im.UndoLastImportMenu).Disabled(!hasUndo).Chosen() {
		a.RollbackLastImport()
	}
}

// BuildInteropNewMenuItems appends B08 Import items to the top-bar "+" menu.
func (a *App) BuildInteropNewMenuItems(mm *ui.Menu) {
	if a == nil || a.ps == nil || mm == nil {
		return
	}
	im := a.interopMsg()
	mm.Separator()
	if mm.Item(im.ImportCURLMenuLabel).Chosen() {
		a.OpenImportCURLModal()
	}
	if mm.Item(im.ImportMenuLabel).Chosen() {
		a.OpenImportModal(ImportSourcePostman)
	}
}

// InteropModals renders the B08 Import and Export modals when open. When both
// modals are closed, it returns immediately without allocating UI nodes or
// requesting frames.
func (a *App) InteropModals(c *ui.Context, t *ui.Theme) {
	st := a.peekInteropState()
	if st == nil {
		return
	}
	if st.ImportOpen {
		a.importModal(c, t, st)
	}
	if st.ExportOpen {
		a.exportModal(c, t, st)
	}
}

// InteropView wraps a.View(c) and renders a.InteropModals(c, c.Theme()) so
// headless UI tests can exercise the full B08 workflow even when shared files
// are reverted to baseline.
func (a *App) InteropView(c *ui.Context) {
	a.View(c)
	a.InteropModals(c, c.Theme())
}

func (a *App) importModal(c *ui.Context, t *ui.Theme, st *InteropState) {
	im := a.interopMsg()
	p := uikit.PaletteFor(t)

	ui.Modal(c, &st.ImportOpen, func() {
		ui.Column(c).Width(780).MaxHeight(680).Gap(10).Padding(18).
			Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			// Header row
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, im.ImportModalTitle).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if st.LastReceipt != nil {
					if uikit.ActionCursor(ui.Button(c, im.UndoLastImportButton)).Clicked() {
						a.RollbackLastImport()
					}
				}
			})

			// Source selector + Conflict strategy selector
			ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, im.SourceLabel).FontSize(12).TextColor(p.Muted)
					sourceLabels := []string{im.SourceCURL, im.SourcePostman, im.SourcePortableBundle}
					if _, changed := uikit.SimpleSegmented(c, p, &st.ImportSourceKind, sourceLabels); changed {
						st.ConflictPolicyIdx = defaultConflictPolicyIdx(st.ImportSourceKind)
						st.StagedPlan = nil
						st.ImportErr = ""
					}
				})
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, im.ConflictPolicyLabel).FontSize(12).TextColor(p.Muted)
					policyLabels := []string{im.PolicyFail, im.PolicyRename, im.PolicyOverwrite, im.PolicySkip}
					if _, changed := uikit.SimpleSegmented(c, p, &st.ConflictPolicyIdx, policyLabels); changed && st.StagedPlan != nil {
						a.StageImportPreview()
					}
				})
			})

			// Target Folder + Target Environment inputs
			ui.Row(c).FillWidth().Gap(12).Children(func() {
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, im.TargetFolderLabel).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &st.ImportTargetFolder).FillWidth().Placeholder(im.RootFolderPlaceholder)
				})
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, im.TargetEnvLabel).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &st.ImportTargetEnv).FillWidth().Placeholder("dev")
				})
			})

			// Source input (cURL text vs file path)
			if st.ImportSourceKind == ImportSourceCURL {
				ui.Text(c, im.CURLInputLabel).FontSize(12).TextColor(p.Muted)
				if ui.TextInput(c, &st.CURLInput).FillWidth().Placeholder(im.CURLPlaceholder).Submitted() {
					a.StageImportPreview()
				}
			} else {
				ui.Text(c, im.FilePathLabel).FontSize(12).TextColor(p.Muted)
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					if ui.TextInput(c, &st.ImportFilePath).Grow(1).Placeholder(im.FilePathPlaceholder).Submitted() {
						a.StageImportPreview()
					}
					if uikit.ActionCursor(ui.Button(c, im.BrowseFileButton)).Clicked() {
						a.pickLocalFileDialog(func(chosen string) {
							st.ImportFilePath = chosen
							a.StageImportPreview()
						})
					}
				})
			}

			// Error / Notice banner
			if st.ImportErr != "" {
				ui.Text(c, a.displayImportError(st.ImportErr)).FontSize(12).TextColor(t.Danger)
			}
			if st.ImportNotice != "" {
				ui.Text(c, a.displayImportNotice(st.ImportNotice)).FontSize(12).TextColor(t.Success)
			}

			// Preview panel
			ui.Text(c, im.PreviewSectionTitle).FontSize(13).FontWeight(600).TextColor(p.Text)
			ui.Scroll(c).FillWidth().Height(280).Padding(10).
				Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					if st.StagedPlan == nil {
						ui.Text(c, im.NoPreviewYet).FontSize(12).TextColor(p.Muted)
						return
					}
					prev := st.StagedPlan.Preview()
					badgeText := im.CanApplyBadge
					badgeColor := t.Success
					if !prev.CanApply {
						badgeText = im.BlockedBadge
						badgeColor = t.Danger
					}
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "["+badgeText+"]").FontSize(12).FontWeight(700).TextColor(badgeColor)
						ui.Text(c, fmt.Sprintf("%s (%s)", prev.Format, prev.FormatVersion)).Font("monospace").FontSize(12).TextColor(p.Muted)
					})
					ui.Text(c, a.displayImportSummary(prev)).FontSize(12).TextColor(p.Text)

					// Conflicts
					if len(prev.Conflicts) > 0 {
						ui.Text(c, im.ConflictsHeading).FontSize(12).FontWeight(600).TextColor(t.Danger)
						for _, cf := range prev.Conflicts {
							ui.Text(c, "• "+a.displayConflict(cf)).FontSize(12).TextColor(t.Danger)
						}
					}

					// Staged file changes
					if len(prev.Changes) > 0 {
						ui.Text(c, fmt.Sprintf("%s (%d)", im.ChangesHeading, len(prev.Changes))).FontSize(12).FontWeight(600).TextColor(p.Text)
						for _, ch := range prev.Changes {
							ui.Text(c, a.displayChangeLine(ch)).Font("monospace").FontSize(11).TextColor(p.Text)
						}
					}

					// ID Mappings
					if len(prev.IDMappings) > 0 {
						ui.Text(c, fmt.Sprintf("%s (%d)", im.IDMappingsHeading, len(prev.IDMappings))).FontSize(12).FontWeight(600).TextColor(p.Text)
						for _, mp := range prev.IDMappings {
							ui.Text(c, a.displayIDMappingLine(mp)).Font("monospace").FontSize(11).TextColor(p.Muted)
						}
					}

					// Variables, Secrets, Local File Slots
					if len(prev.Variables) > 0 || len(prev.SecretBindings) > 0 || len(prev.LocalFileSlots) > 0 {
						ui.Text(c, im.SecretsVarsHeading).FontSize(12).FontWeight(600).TextColor(p.Text)
						for _, v := range prev.Variables {
							ui.Text(c, a.displayVariableLine(v)).Font("monospace").FontSize(11).TextColor(p.Muted)
						}
						for _, slot := range prev.LocalFileSlots {
							ui.Text(c, a.displayLocalFileSlot(slot)).Font("monospace").FontSize(11).TextColor(t.Warning)
						}
					}

					// Losses & Warnings
					if len(prev.Losses) > 0 {
						ui.Text(c, fmt.Sprintf("%s (%d)", im.LossesHeading, len(prev.Losses))).FontSize(12).FontWeight(600).TextColor(t.Warning)
						for _, l := range prev.Losses {
							col := p.Muted
							switch l.Severity {
							case core.LossBlocked:
								col = t.Danger
							case core.LossWarning, core.LossUnsupported, core.LossSecurityScrubbed:
								col = t.Warning
							}
							ui.Text(c, a.displayLossLine(l)).FontSize(11).TextColor(col)
						}
					}
				})
			})

			// Footer action buttons
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if uikit.ActionCursor(ui.Button(c, im.PreviewButton)).Clicked() {
					a.StageImportPreview()
				}
				canApply := st.StagedPlan != nil && st.StagedPlan.Preview().CanApply
				applyBtn := uikit.PrimaryButton(c, im.ConfirmApplyButton)
				if !canApply {
					applyBtn.Disabled(true)
				}
				if uikit.ActionCursor(applyBtn).Clicked() && canApply {
					a.ConfirmAndApplyImport()
				}
				if uikit.ActionCursor(ui.Button(c, im.CloseButton)).Clicked() {
					st.ImportOpen = false
				}
			})
		})
	})
}

func (a *App) exportModal(c *ui.Context, t *ui.Theme, st *InteropState) {
	im := a.interopMsg()
	p := uikit.PaletteFor(t)

	ui.Modal(c, &st.ExportOpen, func() {
		ui.Column(c).Width(780).MaxHeight(680).Gap(10).Padding(18).
			Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			ui.Text(c, im.ExportModalTitle).FontSize(16).FontWeight(700).TextColor(p.Text)
			ui.Text(c, im.ExportSecurityNote).FontSize(11).TextColor(p.Muted)

			// Export kind selector
			ui.Text(c, im.ExportFormatLabel).FontSize(12).TextColor(p.Muted)
			kindLabels := []string{
				im.ExportKindCURL,
				im.ExportKindPortableReq,
				im.ExportKindPortableFolder,
				im.ExportKindPortableProj,
				im.ExportKindPostmanCol,
				im.ExportKindPostmanEnv,
			}
			if _, changed := uikit.SimpleSegmented(c, p, &st.ExportKind, kindLabels); changed {
				a.RefreshExportPreview()
			}

			// Scope-specific target input
			switch st.ExportKind {
			case ExportKindRedactedCURL, ExportKindPortableReq:
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, im.RequestLabel+im.LabelColon).FontSize(12).TextColor(p.Muted)
					if ui.TextInput(c, &st.ExportTargetReqRel).Grow(1).Placeholder("collections/req.request.yaml").Submitted() {
						a.RefreshExportPreview()
					}
				})
			case ExportKindPortableFolder:
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, im.TargetFolderLabel+im.LabelColon).FontSize(12).TextColor(p.Muted)
					if ui.TextInput(c, &st.ExportTargetFolder).Grow(1).Placeholder("collections/folder").Submitted() {
						a.RefreshExportPreview()
					}
				})
			case ExportKindPostmanEnv:
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, im.TargetEnvLabel+im.LabelColon).FontSize(12).TextColor(p.Muted)
					if ui.TextInput(c, &st.ExportTargetEnvID).Grow(1).Placeholder("dev").Submitted() {
						a.RefreshExportPreview()
					}
				})
			}

			// Output file path & overwrite checkbox
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, im.OutputPathLabel+im.LabelColon).FontSize(12).TextColor(p.Muted)
				ui.TextInput(c, &st.ExportOutputPath).Grow(1).Placeholder(im.OutputPathPlaceholder)
				if uikit.ActionCursor(ui.Button(c, im.BrowseFileButton)).Clicked() {
					a.pickLocalFileDialog(func(chosen string) {
						st.ExportOutputPath = chosen
					})
				}
				uikit.ActionCursor(ui.Checkbox(c, &st.ExportOverwrite, im.AllowOverwriteLabel))
			})

			if st.ExportErr != "" {
				ui.Text(c, a.displayExportError(st.ExportErr)).FontSize(12).TextColor(t.Danger)
			}
			if st.ExportNotice != "" {
				ui.Text(c, a.displayExportNotice(st.ExportNotice)).FontSize(12).TextColor(t.Success)
			}

			// Preview & Redaction/Loss notes
			ui.Text(c, im.ExportPreviewHeading).FontSize(13).FontWeight(600).TextColor(p.Text)
			ui.Scroll(c).FillWidth().Height(250).Padding(10).
				Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Column(c).FillWidth().Gap(6).Children(func() {
					if st.ExportPreviewText != "" {
						for _, line := range strings.Split(st.ExportPreviewText, "\n") {
							ui.Text(c, line).Font("monospace").FontSize(11).TextColor(p.Text)
						}
					}
					if len(st.ExportLosses) > 0 {
						ui.Text(c, fmt.Sprintf("%s (%d)", im.LossesHeading, len(st.ExportLosses))).FontSize(12).FontWeight(600).TextColor(t.Warning)
						for _, l := range st.ExportLosses {
							ui.Text(c, a.displayLossLine(l)).FontSize(11).TextColor(t.Warning)
						}
					}
				})
			})

			// Footer actions
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if uikit.ActionCursor(ui.Button(c, im.RefreshPreviewButton)).Clicked() {
					a.RefreshExportPreview()
				}
				if uikit.ActionCursor(ui.Button(c, im.CopyClipboardButton)).Clicked() {
					a.CopyExportPreviewToClipboard(c)
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, im.SaveToFileButton)).Clicked() {
					a.ExecuteExportToFile()
				}
				if uikit.ActionCursor(ui.Button(c, im.CloseButton)).Clicked() {
					st.ExportOpen = false
				}
			})
		})
	})
}
