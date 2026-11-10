package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// BuildSDKProjectMenuItems appends the SDK generator entry to the project
// dropdown menu. The modal itself renders through SDKModals.
func (a *App) BuildSDKProjectMenuItems(mm *ui.Menu) {
	if a == nil || a.ps == nil || a.ps.project == nil || mm == nil {
		return
	}
	sm := a.sdkMsg()
	mm.Separator()
	if mm.Item(sm.MenuLabel).Chosen() {
		a.OpenSDKModal()
	}
}

// SDKModals renders the Go SDK generator modal when open. When closed it
// returns immediately without allocating UI nodes or requesting frames.
func (a *App) SDKModals(c *ui.Context, t *ui.Theme) {
	st := a.peekSDKState()
	if st == nil || !st.Open {
		return
	}
	a.sdkModal(c, t, st)
}

func (a *App) sdkModal(c *ui.Context, t *ui.Theme, st *SDKState) {
	sm := a.sdkMsg()
	p := uikit.PaletteFor(t)
	open := st.Open
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(860).MaxHeight(760).Gap(10).Padding(18).
			Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			// Header
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, sm.ModalTitle).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
			})
			ui.Text(c, sm.SecurityNote).FontSize(11).TextColor(p.Muted)

			// Contract version, language, package and module.
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, sm.SpecVersionLabel).FontSize(12).TextColor(p.Muted)
					labels := []string{"3.1.2", "3.2.1"}
					if _, changed := uikit.SimpleSegmented(c, p, &st.SpecVersionIdx, labels); changed {
						st.Plan = nil
					}
				})
				ui.Column(c).Gap(4).Children(func() {
					ui.Text(c, sm.LanguageLabel).FontSize(12).TextColor(p.Muted)
					labels := []string{sm.LanguageGo, sm.LanguageTS}
					if _, changed := uikit.SimpleSegmented(c, p, &st.LanguageIdx, labels); changed {
						st.Plan = nil
					}
				})
				ui.Column(c).Grow(1).Gap(4).Children(func() {
					ui.Text(c, sm.PackageLabel).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &st.PackageName).FillWidth()
				})
				ui.Column(c).Grow(2).Gap(4).Children(func() {
					ui.Text(c, sm.ModuleLabel).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &st.ModulePath).FillWidth()
				})
			})
			if st.LanguageIdx == 1 {
				ui.Text(c, sm.TSNote).FontSize(11).TextColor(p.Muted)
			}

			// Output directory.
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, sm.TargetLabel+":").FontSize(12).TextColor(p.Muted)
				ui.TextInput(c, &st.TargetDir).Grow(1).Placeholder("/path/to/generated-sdk")
				if uikit.ActionCursor(ui.Button(c, sm.Browse)).Clicked() {
					a.PickSDKTargetDir()
				}
			})

			// Explicit opt-ins. None of them defaults to true.
			ui.Row(c).FillWidth().Gap(16).AlignItems(ui.Center).Children(func() {
				uikit.ActionCursor(ui.Checkbox(c, &st.IncludeUnsupported, sm.IncludeUnsupported))
				uikit.ActionCursor(ui.Checkbox(c, &st.AllowOverwrite, sm.AllowOverwrite))
				uikit.ActionCursor(ui.Checkbox(c, &st.AllowDeleteStale, sm.AllowDeleteStale))
			})
			a.invalidateSDKPreview(st)

			if st.Err != "" {
				ui.Text(c, st.Err).FontSize(12).TextColor(t.Danger)
			}
			if st.Notice != "" {
				ui.Text(c, st.Notice).FontSize(12).TextColor(t.Success)
			}

			// Preview panel.
			ui.Scroll(c).FillWidth().Height(400).Padding(10).
				Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					a.sdkPreviewBody(c, t, p, sm, st)
				})
			})

			// Footer actions.
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				preview := ui.Button(c, sm.PreviewAction)
				if st.Busy {
					preview = preview.Disabled(true)
				}
				if uikit.ActionCursor(preview).Clicked() {
					a.RefreshSDKPreview()
				}
				apply := uikit.PrimaryButton(c, sm.ApplyAction)
				if st.Plan == nil || len(st.Plan.Conflicts) > 0 || st.Busy {
					apply = apply.Disabled(true)
				}
				if uikit.ActionCursor(apply).Clicked() {
					a.ApplySDK()
				}
				if uikit.ActionCursor(ui.Button(c, sm.Close)).Clicked() {
					a.CloseSDKModal()
				}
			})
		})
	})
	if !open {
		a.CloseSDKModal()
	}
}

// sdkPreviewBody renders the preview, conflicts, unsupported operations, stale
// files, planned actions and operations without touching disk.
func (a *App) sdkPreviewBody(c *ui.Context, t *ui.Theme, p uikit.Palette, sm *sdkMessages, st *SDKState) {
	if st.Busy {
		ui.Text(c, sm.Busy).FontSize(12).TextColor(p.Muted)
		return
	}
	if st.Plan == nil || st.Plan.Result == nil {
		ui.Text(c, sm.NoPreview).FontSize(12).TextColor(p.Muted)
		return
	}
	plan := st.Plan
	res := plan.Result

	summary := fmt.Sprintf(sm.SummaryFmt,
		len(res.Files), res.TotalBytes, len(res.Operations), len(res.Models),
		len(res.Unsupported), len(plan.Conflicts), len(plan.Stale))
	ui.Text(c, summary).FontSize(12).FontWeight(600).TextColor(p.Text)
	ui.Text(c, fmt.Sprintf(sm.ManifestFmt, res.Language, res.GeneratorVersion, res.TemplateVersion, shortDigest(res.SpecDigest))).
		FontSize(11).TextColor(p.Muted)

	if len(plan.Conflicts) > 0 {
		ui.Text(c, fmt.Sprintf("%s (%d)", sm.ConflictsHeading, len(plan.Conflicts))).FontSize(12).FontWeight(600).TextColor(t.Danger)
		for _, cf := range plan.Conflicts {
			ui.Text(c, "• "+cf.RelPath+": "+cf.Reason).FontSize(11).TextColor(t.Danger)
		}
	}
	if len(res.Unsupported) > 0 {
		ui.Text(c, fmt.Sprintf("%s (%d)", sm.UnsupportedHeading, len(res.Unsupported))).FontSize(12).FontWeight(600).TextColor(t.Warning)
		for _, u := range res.Unsupported {
			ui.Text(c, "• "+u.Method+" "+u.Path+": "+u.Reason).FontSize(11).TextColor(t.Warning)
		}
	}
	if len(plan.Stale) > 0 {
		ui.Text(c, fmt.Sprintf("%s (%d)", sm.StaleHeading, len(plan.Stale))).FontSize(12).FontWeight(600).TextColor(t.Warning)
		for _, s := range plan.Stale {
			reason := s.Reason
			if reason == "" {
				reason = sm.ActionDelete
			}
			ui.Text(c, "• "+s.RelPath+" — "+reason).FontSize(11).TextColor(t.Warning)
		}
	}

	ui.Text(c, fmt.Sprintf("%s (%d)", sm.FilesHeading, len(plan.Actions))).FontSize(12).FontWeight(600).TextColor(p.Text)
	for _, action := range plan.Actions {
		color := p.Muted
		switch action.Kind {
		case "create":
			color = t.Success
		case "update":
			color = t.Warning
		case "delete-stale":
			color = t.Danger
		}
		ui.Text(c, fmt.Sprintf("[%s] %s (%d bytes)", sm.sdkActionLabel(action.Kind), action.RelPath, action.Bytes)).
			Font("monospace").FontSize(11).TextColor(color)
	}

	if len(res.Operations) > 0 {
		ui.Text(c, fmt.Sprintf("%s (%d)", sm.OperationsHeading, len(res.Operations))).FontSize(12).FontWeight(600).TextColor(p.Text)
		for _, op := range res.Operations {
			ui.Text(c, fmt.Sprintf("%s %s → %s", op.Method, op.Path, op.GoName)).
				Font("monospace").FontSize(11).TextColor(p.Text)
		}
	} else {
		ui.Text(c, sm.NoOperations).FontSize(11).TextColor(p.Muted)
	}
	if len(res.Models) > 0 {
		ui.Text(c, fmt.Sprintf("%s (%d)", sm.ModelsHeading, len(res.Models))).FontSize(12).FontWeight(600).TextColor(p.Text)
		for _, m := range res.Models {
			ui.Text(c, fmt.Sprintf("%s · %s · %s", m.Name, m.Kind, m.Origin)).
				Font("monospace").FontSize(11).TextColor(p.Muted)
		}
	}
	if len(res.Diagnostics) > 0 {
		ui.Text(c, fmt.Sprintf("%s (%d)", sm.SupportHeading, len(res.Diagnostics))).FontSize(12).FontWeight(600).TextColor(t.Warning)
		for _, d := range res.Diagnostics {
			ui.Text(c, fmt.Sprintf("[%s] %s: %s", d.Code, d.Location, d.Message)).FontSize(11).TextColor(t.Warning)
		}
	}
}
