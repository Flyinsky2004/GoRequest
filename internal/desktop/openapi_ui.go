package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// BuildOpenAPIProjectMenuItems appends A03 OpenAPI 3.1/3.2 Import/Sync, Export/
// Coverage, and Operation Contract / Schema Validator items to a Menu.
func (a *App) BuildOpenAPIProjectMenuItems(c *ui.Context, mm *ui.Menu) {
	if a == nil || a.ps == nil || mm == nil {
		return
	}
	om := a.openAPIMsg()
	mm.Separator()
	if mm.Item(om.MenuImportOpenAPI).Chosen() {
		a.OpenOpenAPIModal(OpenAPITabImportSync)
	}
	if mm.Item(om.MenuExportOpenAPI).Chosen() {
		a.OpenOpenAPIModal(OpenAPITabExportCoverage)
	}
	if mm.Item(om.MenuContractInspector).Chosen() {
		a.OpenOpenAPIModal(OpenAPITabContractValidator)
	}
	st := a.peekOpenAPIState()
	hasUndo := st != nil && st.LastReceipt != nil
	if mm.Item(om.UndoLastButton).Disabled(!hasUndo).Chosen() {
		a.RollbackLastOpenAPI()
	}
}

// OpenAPIModals renders the A03 OpenAPI 3.1/3.2 & JSON Schema Studio modal when
// open. When closed, it returns immediately without allocating UI nodes or
// requesting frames.
func (a *App) OpenAPIModals(c *ui.Context, t *ui.Theme) {
	st := a.peekOpenAPIState()
	if st == nil || !st.ModalOpen {
		return
	}
	a.openAPIStudioModal(c, t, st)
}

// OpenAPIView wraps a.View(c), a.InteropModals(c, c.Theme()), and
// a.OpenAPIModals(c, c.Theme()) so headless UI tests can exercise the full A03
// OpenAPI & Schema Studio workflow while shared files remain on clean baseline.
func (a *App) OpenAPIView(c *ui.Context) {
	a.View(c)
	a.InteropModals(c, c.Theme())
	a.OpenAPIModals(c, c.Theme())
}

func (a *App) openAPIStudioModal(c *ui.Context, t *ui.Theme, st *OpenAPIState) {
	om := a.openAPIMsg()
	p := uikit.PaletteFor(t)

	ui.Modal(c, &st.ModalOpen, func() {
		ui.Column(c).Width(840).MaxHeight(720).Gap(10).Padding(18).
			Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			// Header row
			ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Text(c, om.ModalTitle).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if st.LastReceipt != nil {
					if ui.Button(c, om.UndoLastButton).Clicked() {
						a.RollbackLastOpenAPI()
					}
				}
			})

			// Top-level Studio Tab Switcher
			tabLabels := []string{om.TabImportSync, om.TabExportCoverage, om.TabContractValidator}
			if _, changed := uikit.SimpleSegmented(c, p, &st.ActiveTab, tabLabels); changed {
				switch st.ActiveTab {
				case OpenAPITabExportCoverage:
					a.RefreshOpenAPIExportAndCoverage()
				case OpenAPITabContractValidator:
					a.LoadActiveRequestContract()
				}
			}

			switch st.ActiveTab {
			case OpenAPITabImportSync:
				a.renderOpenAPIImportSyncTab(c, t, p, om, st)
			case OpenAPITabExportCoverage:
				a.renderOpenAPIExportCoverageTab(c, t, p, om, st)
			case OpenAPITabContractValidator:
				a.renderOpenAPIContractValidatorTab(c, t, p, om, st)
			}
		})
	})
}

func (a *App) renderOpenAPIImportSyncTab(c *ui.Context, t *ui.Theme, p uikit.Palette, om openAPIMessages, st *OpenAPIState) {
	// Source Mode + Staging Action + Conflict Policy
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.SourceModeLabel).FontSize(12).TextColor(p.Muted)
			modeLabels := []string{om.ModeNative, om.ModeExternalSpec}
			if _, changed := uikit.SimpleSegmented(c, p, &st.SourceModeIdx, modeLabels); changed && st.StagedPlan != nil {
				a.StageOpenAPIPreview()
			}
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.SyncActionLabel).FontSize(12).TextColor(p.Muted)
			actionLabels := []string{om.ActionFreshImport, om.ActionThreeWaySync}
			if _, changed := uikit.SimpleSegmented(c, p, &st.SyncActionIdx, actionLabels); changed && st.StagedPlan != nil {
				a.StageOpenAPIPreview()
			}
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.ConflictPolicyLabel).FontSize(12).TextColor(p.Muted)
			policyLabels := []string{om.PolicyFail, om.PolicyRename, om.PolicyOverwrite, om.PolicySkip}
			if _, changed := uikit.SimpleSegmented(c, p, &st.ConflictPolicyIdx, policyLabels); changed && st.StagedPlan != nil {
				a.StageOpenAPIPreview()
			}
		})
	})

	// Target Folder + Target Environment + Checkboxes
	ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.TargetFolderLabel).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.TargetFolderRel).FillWidth().Placeholder("collections")
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.TargetEnvLabel).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.TargetEnvID).FillWidth().Placeholder("dev")
		})
	})

	ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Checkbox(c, &st.StrictLossReject, om.StrictLossRejectLabel)
		ui.Checkbox(c, &st.GroupByTag, om.GroupByTagLabel)
	})

	// File path + Inline spec input
	ui.Text(c, om.SpecSourceFileLabel).FontSize(12).TextColor(p.Muted)
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		if ui.TextInput(c, &st.SpecFilePath).Grow(1).Placeholder(om.SpecSourcePlaceholder).Submitted() {
			a.StageOpenAPIPreview()
		}
		if ui.Button(c, om.BrowseButton).Clicked() {
			a.pickLocalFileDialog(func(chosen string) {
				st.SpecFilePath = chosen
				a.StageOpenAPIPreview()
			})
		}
	})

	ui.Text(c, om.SpecInlineLabel).FontSize(12).TextColor(p.Muted)
	if ui.TextInput(c, &st.SpecInlineInput).FillWidth().Placeholder(om.SpecInlinePlaceholder).Submitted() {
		a.StageOpenAPIPreview()
	}

	if st.ImportErr != "" {
		ui.Text(c, st.ImportErr).FontSize(12).TextColor(t.Danger)
	}
	if st.ImportNotice != "" {
		ui.Text(c, st.ImportNotice).FontSize(12).TextColor(t.Success)
	}

	// Scrollable Preview Panel
	ui.Scroll(c).FillWidth().Height(250).Padding(10).
		Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			if st.ValidationReport != nil {
				vr := st.ValidationReport
				ui.Text(c, fmt.Sprintf("%s: %s (%s) — %q (API %s, %d operations)",
					om.ValidationHeading, vr.Kind, vr.Version, vr.Title, vr.APIVersion, vr.OperationCount)).
					FontSize(12).FontWeight(600).TextColor(p.Text)
				if len(vr.Features32Used) > 0 {
					ui.Text(c, "OpenAPI 3.2 constructs detected: "+strings.Join(vr.Features32Used, ", ")).
						Font("monospace").FontSize(11).TextColor(p.Muted)
				}
				if len(vr.MissingDependencies) > 0 {
					ui.Text(c, fmt.Sprintf("%s (%d)", om.MissingRefsHeading, len(vr.MissingDependencies))).
						FontSize(12).FontWeight(600).TextColor(t.Danger)
					for _, dep := range vr.MissingDependencies {
						kindTag := "missing-local"
						if dep.IsRemote {
							kindTag = "blocked-remote"
						}
						ui.Text(c, fmt.Sprintf("[%s] %s at %s: %s", kindTag, dep.RefURI, dep.SchemaPath, dep.Reason)).
							Font("monospace").FontSize(11).TextColor(t.Danger)
					}
				}
			}

			if st.SyncPreview != nil {
				sp := st.SyncPreview
				ui.Text(c, fmt.Sprintf("%s: +%d added, ~%d updated, =%d unchanged, %d local-only, !%d conflict(s)",
					om.SyncSummaryHeading, sp.AddedCount, sp.UpdatedCount, sp.UnchangedCount, sp.LocalOnlyCount, sp.ConflictCount)).
					FontSize(12).FontWeight(600).TextColor(p.Text)
				for _, opItem := range sp.Operations {
					preservedNote := ""
					if len(opItem.PreservedLocalFields) > 0 {
						preservedNote = " [preserved: " + strings.Join(opItem.PreservedLocalFields, ", ") + "]"
					}
					ui.Text(c, fmt.Sprintf("[%s] %s -> %s (%s)%s",
						strings.ToUpper(string(opItem.Kind)), opItem.OperationKey, opItem.RequestID, opItem.RelPath, preservedNote)).
						Font("monospace").FontSize(11).TextColor(p.Text)
				}
				for _, cf := range sp.Conflicts {
					ui.Text(c, fmt.Sprintf("CONFLICT [%s / %s]: %s", cf.OperationKey, cf.Field, cf.Message)).
						FontSize(11).TextColor(t.Danger)
				}
			}

			if st.StagedPlan != nil {
				prev := st.StagedPlan.Preview()
				badge := om.PreviewReadyBadge
				badgeCol := t.Success
				if !prev.CanApply {
					badge = om.PreviewBlockedBadge
					badgeCol = t.Danger
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "["+badge+"]").FontSize(12).FontWeight(700).TextColor(badgeCol)
					ui.Text(c, prev.Summary).FontSize(12).TextColor(p.Text)
				})
				for _, cf := range prev.Conflicts {
					ui.Text(c, "• "+cf).FontSize(11).TextColor(t.Danger)
				}
				for _, ch := range prev.Changes {
					ui.Text(c, fmt.Sprintf("[%s] %s (%s)", strings.ToUpper(string(ch.Kind)), ch.RelPath, formatByteSize(int64(ch.AfterBytes)))).
						Font("monospace").FontSize(11).TextColor(p.Muted)
				}
				for _, l := range prev.Losses {
					ui.Text(c, fmt.Sprintf("[%s / %s] %s", l.Severity, l.Code, l.Message)).
						FontSize(11).TextColor(t.Warning)
				}
			}
		})
	})

	// Footer buttons
	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, om.StagePreviewButton).Clicked() {
			a.StageOpenAPIPreview()
		}
		canApply := st.StagedPlan != nil && st.StagedPlan.Preview().CanApply
		applyBtn := uikit.PrimaryButton(c, om.ApplyButton)
		if !canApply {
			applyBtn.Disabled(true)
		}
		if applyBtn.Clicked() && canApply {
			a.ConfirmAndApplyOpenAPI()
		}
		if ui.Button(c, om.CloseButton).Clicked() {
			st.ModalOpen = false
		}
	})
}

func (a *App) renderOpenAPIExportCoverageTab(c *ui.Context, t *ui.Theme, p uikit.Palette, om openAPIMessages, st *OpenAPIState) {
	ui.Row(c).FillWidth().Gap(14).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.TargetVersionLabel).FontSize(12).TextColor(p.Muted)
			verLabels := []string{om.Version31, om.Version32}
			if _, changed := uikit.SimpleSegmented(c, p, &st.ExportVersionIdx, verLabels); changed {
				a.RefreshOpenAPIExportAndCoverage()
			}
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.ExportFormatLabel).FontSize(12).TextColor(p.Muted)
			fmtLabels := []string{om.FormatYAML, om.FormatJSON}
			if _, changed := uikit.SimpleSegmented(c, p, &st.ExportFormatIdx, fmtLabels); changed {
				a.RefreshOpenAPIExportAndCoverage()
			}
		})
	})

	ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
		ui.Checkbox(c, &st.ExportStrict, om.StrictExportLabel)
		ui.Checkbox(c, &st.ExportAllowLossy, om.AllowLossyDowngrade)
		ui.Checkbox(c, &st.ExportIncludeExs, om.IncludeExamplesLabel)
	})

	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, om.OutputPathLabel+":").FontSize(12).TextColor(p.Muted)
		ui.TextInput(c, &st.ExportOutputPath).Grow(1).Placeholder(om.OutputPathPlaceholder)
		if ui.Button(c, om.BrowseButton).Clicked() {
			a.pickLocalFileDialog(func(chosen string) {
				st.ExportOutputPath = chosen
			})
		}
		ui.Checkbox(c, &st.ExportOverwrite, om.AllowOverwriteLabel)
	})

	if st.ExportErr != "" {
		ui.Text(c, st.ExportErr).FontSize(12).TextColor(t.Danger)
	}
	if st.ExportNotice != "" {
		ui.Text(c, st.ExportNotice).FontSize(12).TextColor(t.Success)
	}

	ui.Scroll(c).FillWidth().Height(320).Padding(10).
		Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Column(c).FillWidth().Gap(8).Children(func() {
			// Coverage Summary
			if cov := st.CoverageReport; cov != nil {
				ui.Text(c, om.CoverageHeading).FontSize(12).FontWeight(700).TextColor(p.Text)
				ui.Text(c, fmt.Sprintf(
					"Exportable HTTP Operations: %d (Total Project Requests: %d, Non-HTTP Excluded: %d)",
					cov.ExportableHTTPOperations, cov.TotalRequests, cov.NotApplicableCount,
				)).Font("monospace").FontSize(11).TextColor(p.Text)
				ui.Text(c, fmt.Sprintf(
					"Descriptions: %d/%d (%.0f%%) | Response Schemas: %d/%d (%.0f%%) | Examples: %d/%d (%.0f%%) | Runtime Assertions (Test): %d/%d (%.0f%%)",
					cov.WithDescriptionCount, cov.ExportableHTTPOperations, cov.DescriptionCoveragePct,
					cov.WithResponseSchemaCount, cov.ExportableHTTPOperations, cov.ResponseSchemaCoveragePct,
					cov.WithExamplesCount, cov.ExportableHTTPOperations, cov.ExamplesCoveragePct,
					cov.WithTestAssertionsCount, cov.ExportableHTTPOperations, cov.TestAssertionsCoveragePct,
				)).Font("monospace").FontSize(11).TextColor(p.Muted)
				for _, na := range cov.NotApplicableRequests {
					ui.Text(c, fmt.Sprintf("[N/A: %s] %s (%s) — %s", na.Protocol, na.Name, na.RequestID, na.Reason)).
						Font("monospace").FontSize(11).TextColor(t.Warning)
				}
			}

			// Compatibility Report
			if res := st.ExportResult; res != nil {
				cr := res.Compatibility
				badge := om.CompatLosslessBadge
				badgeCol := t.Success
				if !cr.Compatible {
					badge = om.CompatRejectedBadge
					badgeCol = t.Danger
				} else if cr.Lossy {
					badge = om.CompatLossyBadge
					badgeCol = t.Warning
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, om.CompatHeading+":").FontSize(12).FontWeight(700).TextColor(p.Text)
					ui.Text(c, "["+badge+"]").FontSize(12).FontWeight(700).TextColor(badgeCol)
					ui.Text(c, fmt.Sprintf("(%s → %s)", cr.SourceVersion, cr.TargetVersion)).Font("monospace").FontSize(11).TextColor(p.Muted)
				})
				for _, iss := range cr.Issues {
					ui.Text(c, fmt.Sprintf("[%s] %s at %s: %s", strings.ToUpper(iss.Resolution), iss.Feature, iss.Location, iss.Message)).
						Font("monospace").FontSize(11).TextColor(badgeCol)
				}
			}

			// Serialized Spec Preview
			if st.ExportPreviewText != "" {
				for _, line := range strings.Split(st.ExportPreviewText, "\n") {
					ui.Text(c, line).Font("monospace").FontSize(11).TextColor(p.Text)
				}
			}
		})
	})

	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, om.RefreshExportButton).Clicked() {
			a.RefreshOpenAPIExportAndCoverage()
		}
		if ui.Button(c, om.CopySpecButton).Clicked() {
			a.CopyOpenAPIExportToClipboard(c)
		}
		if uikit.PrimaryButton(c, om.SaveSpecFileButton).Clicked() {
			a.ExecuteOpenAPIExportToFile()
		}
		if ui.Button(c, om.CloseButton).Clicked() {
			st.ModalOpen = false
		}
	})
}

func (a *App) renderOpenAPIContractValidatorTab(c *ui.Context, t *ui.Theme, p uikit.Palette, om openAPIMessages, st *OpenAPIState) {
	_, entry := a.activeHTTPRequestTabAndEntry()
	if entry == nil || entry.Request == nil {
		ui.Text(c, om.NoActiveHTTPRequest).FontSize(12).TextColor(p.Muted)
		return
	}
	if st.LoadedReqID != entry.Request.ID {
		a.LoadActiveRequestContract()
	}

	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, om.ActiveRequestLabel+":").FontSize(12).TextColor(p.Muted)
		ui.Text(c, fmt.Sprintf("%s (%s) — %s", entry.Request.Name, entry.Request.ID, entry.RelPath)).
			Font("monospace").FontSize(12).FontWeight(600).TextColor(p.Text)
	})

	// Operation Contract Metadata Inputs
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.OperationIDLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.ContractOpID).FillWidth().Placeholder("getUserById")
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.PathTemplateLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.ContractPathTmpl).FillWidth().Placeholder("/users/{id}")
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.SummaryLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.ContractSummary).FillWidth().Placeholder("Operation summary")
		})
		ui.Checkbox(c, &st.ContractDeprecated, om.DeprecatedLabel)
	})

	// Multi-status / Multi-media-type Response Contract Builder
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(130).Gap(4).Children(func() {
			ui.Text(c, om.ResponseStatusLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.DraftStatusKey).FillWidth().Placeholder("200")
		})
		ui.Column(c).Width(200).Gap(4).Children(func() {
			ui.Text(c, om.ResponseMediaTypeLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.DraftMediaType).FillWidth().Placeholder("application/json")
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.ResponseSchemaJSONLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.DraftSchemaJSON).FillWidth().Placeholder(`{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}`)
		})
		if ui.Button(c, om.AddUpdateResponseBtn).Clicked() {
			a.StageDraftResponseContractEntry()
		}
		if uikit.PrimaryButton(c, om.SaveContractButton).Clicked() {
			a.SaveActiveRequestContract()
		}
	})

	// Response Schema Validator Inputs
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Column(c).Width(130).Gap(4).Children(func() {
			ui.Text(c, om.TestStatusInputLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.TestStatusCode).FillWidth().Placeholder("200")
		})
		ui.Column(c).Width(200).Gap(4).Children(func() {
			ui.Text(c, om.TestContentTypeLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.TestContentType).FillWidth().Placeholder("application/json")
		})
		ui.Column(c).Grow(1).Gap(4).Children(func() {
			ui.Text(c, om.TestBodyJSONLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &st.TestBodyJSON).FillWidth().Placeholder(`{"id":"wrong-type"}`)
		})
		if ui.Button(c, om.UseLiveResponseBtn).Clicked() {
			a.LoadLatestLiveResponseIntoValidator()
		}
		if uikit.PrimaryButton(c, om.ValidateResponseBtn).Clicked() {
			a.ValidateActiveResponseAgainstContract()
		}
	})

	if st.ContractErr != "" {
		ui.Text(c, st.ContractErr).FontSize(12).TextColor(t.Danger)
	}
	if st.ContractNotice != "" {
		ui.Text(c, st.ContractNotice).FontSize(12).TextColor(t.Success)
	}

	// Contract & Validation Results Scroll View
	ui.Scroll(c).FillWidth().Height(220).Padding(10).
		Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			if st.ValidationResult != nil {
				vr := st.ValidationResult
				badge := om.ValidationPassedBadge
				col := t.Success
				if !vr.Valid {
					badge = om.ValidationFailedBadge
					col = t.Danger
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, "["+badge+"]").FontSize(12).FontWeight(700).TextColor(col)
					ui.Text(c, fmt.Sprintf("Matched status=%s mediaType=%s (%d violation(s))",
						vr.MatchedStatus, vr.MatchedMediaType, len(vr.Violations))).
						Font("monospace").FontSize(11).TextColor(p.Muted)
				})
				for _, v := range vr.Violations {
					ui.Text(c, fmt.Sprintf("• %s [ptr: %s] (%s): %s (expected: %s, actual: %s)",
						v.InstancePath, v.InstancePointer, v.Keyword, v.Message, v.Expected, v.Actual)).
						Font("monospace").FontSize(11).TextColor(t.Danger)
				}
			}

			if st.WorkingContract != nil && len(st.WorkingContract.Responses) > 0 {
				ui.Text(c, "Declared Operation Response Contracts:").FontSize(12).FontWeight(600).TextColor(p.Text)
				for _, stCode := range sortedContractResponseKeys(st.WorkingContract.Responses) {
					rc := st.WorkingContract.Responses[stCode]
					var mts []string
					for m := range rc.Content {
						mts = append(mts, m)
					}
					ui.Text(c, fmt.Sprintf("Status %s — %s (media types: %s)", stCode, rc.Description, strings.Join(mts, ", "))).
						Font("monospace").FontSize(11).TextColor(p.Muted)
				}
			}
		})
	})

	ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
		if ui.Button(c, om.CloseButton).Clicked() {
			st.ModalOpen = false
		}
	})
}

func sortedContractResponseKeys(m map[string]core.ResponseContract) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 0; i < len(keys); i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[j] < keys[i] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	return keys
}
