package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docgen"
)

// This file draws the derived-document export panel - opened from the doc
// header's Export button (integration patch) - and its preview surface. The
// layout follows the existing MyGo Native panels: small muted captions,
// segmented controls, hairline-separated list rows, status text instead of
// dialogs. Preview and export only recompute on their button clicks, so
// typing in the editor never rebuilds the set.

// docgenExportButton draws the header button and toggles the panel; it is
// the single entry the integration patch adds to the doc header.
func (a *App) docgenExportButton(c *ui.Context, p uikit.Palette, root ui.Element, tab *Tab) {
	dm := a.derivedDocMsg()
	st := derivedDocStateFor(root, tab)
	label := dm.Export
	if n := len(st.problemsHint()); st.open && n > 0 {
		label = fmt.Sprintf("%s (%d)", dm.Export, n)
	}
	if uikit.ActionCursor(ui.Button(c, label)).Clicked() {
		st.open = !st.open
	}
}

// problemsHint returns the stored preview diagnostics for the button badge.
func (st *derivedDocState) problemsHint() []core.DocDiagnostic {
	if st.preview == nil {
		return nil
	}
	return st.preview.Diagnostics
}

// docgenExportPanel draws the panel below the doc header when open.
func (a *App) docgenExportPanel(c *ui.Context, t *ui.Theme, p uikit.Palette, root ui.Element, tab *Tab) {
	dm := a.derivedDocMsg()
	st := derivedDocStateFor(root, tab)
	if !st.open {
		return
	}
	ui.Column(c).FillWidth().MaxWidth(560).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Background(p.Sidebar).Children(func() {
		ui.Row(c).FillWidth().Padding(8, 12).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Text(c, dm.PanelTitle).FontSize(12).FontWeight(600).TextColor(p.Text)
			ui.Box(c).Grow(1)
			ui.Text(c, dm.ScopeLabel).FontSize(12).TextColor(p.Muted)
			uikit.SimpleSegmented(c, p, &st.scope, []string{dm.ScopeThis, dm.ScopeAll})
			ui.Text(c, dm.FormatLabel).FontSize(12).TextColor(p.Muted)
			uikit.SimpleSegmented(c, p, &st.format, []string{dm.FormatMD, dm.FormatHTML})
		})

		ui.Row(c).FillWidth().Padding(0, 12, 8, 12).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, dm.TargetLabel+":").FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &st.target).Grow(1).Placeholder(dm.TargetEmpty)
			if ui.Button(c, dm.Browse).Clicked() {
				a.docgenPickTargetDir(st)
			}
		})

		ui.Row(c).FillWidth().Padding(0, 12, 8, 12).Gap(16).AlignItems(ui.Center).Children(func() {
			ui.Checkbox(c, &st.assets, dm.IncludeAssets)
			ui.Checkbox(c, &st.overwr, dm.AllowOverwrite)
			ui.Box(c).Grow(1)
			if ui.Button(c, dm.PreviewAction).Clicked() {
				st.result = nil
				a.docgenPreview(tab, st)
			}
			if uikit.ActionCursor(ui.PrimaryButton(c, dm.ExportAction)).Clicked() {
				a.docgenExport(root, tab, st)
			}
		})

		if st.lastErr != "" {
			ui.Text(c, st.lastErr).FontSize(12).TextColor(t.Danger).Padding(0, 12, 8, 12).SingleLine()
		}
		if st.notice != "" {
			ui.Text(c, st.notice).FontSize(12).TextColor(t.Success).Padding(0, 12, 8, 12).SingleLine()
		}

		if st.preview != nil {
			ui.Row(c).FillWidth().Padding(2, 12, 4, 12).Gap(14).AlignItems(ui.Center).Children(func() {
				pv := st.preview
				problems := len(pv.Diagnostics)
				conflicts := len(pv.Conflicts)
				ui.Text(c, fmt.Sprintf(dm.Files, len(pv.Files))).FontSize(11).Font("monospace").TextColor(p.Text)
				ui.Text(c, fmt.Sprintf(dm.Problems, problems)).FontSize(11).Font("monospace").
					TextColor(uikit.MethodColor("POST", p))
				ui.Text(c, fmt.Sprintf(dm.Conflicts, conflicts)).FontSize(11).Font("monospace").
					TextColor(uikit.MethodColor("DELETE", p))
			})
			a.docgenPreviewList(c, p, root, tab, st, dm)
		}
	})
}

// docgenPreviewList renders diagnostics, conflicts and bounded document
// snippets; diagnostics refer to the authored (unexpanded) source and open
// its document at the position on click.
func (a *App) docgenPreviewList(c *ui.Context, p uikit.Palette, root ui.Element, tab *Tab, st *derivedDocState, dm *derivedDocMessages) {
	pv := st.preview
	ui.Scroll(c).Grow(1).FillWidth().MaxHeight(320).TrackScroll(&st.scroll).Children(func() {
		ui.Column(c).FillWidth().Gap(2).Padding(0, 0, 8, 0).Children(func() {
			if len(pv.Diagnostics) == 0 {
				ui.Text(c, dm.NoProblems).FontSize(11).TextColor(p.Muted).Padding(2, 12)
			}
			for _, d := range pv.Diagnostics {
				d := d
				label := fmt.Sprintf("%s:%d  %s", d.RelPath, d.Line, a.derivedDiagText(d))
				row := ui.Row(c).Key("docgen-diag:"+d.RelPath+":"+fmt.Sprint(d.Line)+":"+fmt.Sprint(d.Column)).
					FillWidth().Height(24).Padding(0, 12).AlignItems(ui.Center)
				if row.Hovered() {
					row.Background(p.Hover)
				}
				row.Children(func() {
					ui.Text(c, label).FontSize(11).TextColor(derivedSeverityColor(d.Severity, p)).SingleLine()
				})
				row.Label(label)
				if uikit.ActionCursor(row).Clicked() {
					a.openDocDiagnostic(root, tab, d)
				}
			}
			if len(pv.Conflicts) > 0 {
				ui.Text(c, dm.NoConflicts).FontSize(11).TextColor(p.Muted).Padding(4, 12, 0, 12)
				for _, cf := range pv.Conflicts {
					ui.Text(c, "⚠ "+cf.TargetPath+": "+cf.Reason).FontSize(11).Font("monospace").
						TextColor(uikit.MethodColor("POST", p)).Padding(2, 12).SingleLine()
				}
			}
			for _, rel := range pv.Docs {
				sn := pv.Snippets[rel]
				if sn == "" {
					continue
				}
				ui.Text(c, rel).FontSize(11).FontWeight(600).TextColor(p.Muted).Padding(6, 12, 0, 12)
				for _, line := range boundedLines(sn, 20) {
					ui.Text(c, line).FontSize(11).Font("monospace").TextColor(p.Text).Padding(0, 12).SingleLine()
				}
			}
		})
	})
}

// openDocDiagnostic jumps to the source position of a preview diagnostic.
func (a *App) openDocDiagnostic(root ui.Element, tab *Tab, d core.DocDiagnostic) {
	ps := tab.Owner
	if ps == nil || ps.project == nil {
		return
	}
	if d.RelPath != tab.DocRel {
		// Jump to another document of the set first.
		if err := ps.openDoc(d.RelPath); err != nil {
			a.status = err.Error()
			return
		}
		tab = ps.activeTab()
		if tab == nil || tab.Kind != "doc" {
			return
		}
	}
	a.docGoToLine(tab, docStateFor(root, tab), d.Line, d.Column)
}

// derivedDiagText renders a preview diagnostic with the panel strings for
// docgen kinds and the B09 document strings for shared kinds.
func (a *App) derivedDiagText(d core.DocDiagnostic) string {
	dm := a.derivedDocMsg()
	base := a.docMsg()
	switch d.Kind {
	case docgen.DiagMissingRequest:
		return fmt.Sprintf(dm.RequestMissing, d.Dest)
	case core.DiagMissingLink:
		if d.Dest != "" {
			return fmt.Sprintf(base.DiagMissingLink, d.Dest)
		}
		return base.DiagMissingLink
	case core.DiagMissingAsset:
		return fmt.Sprintf(base.DiagMissingAsset, d.Dest)
	case core.DiagDangerousScheme:
		return fmt.Sprintf(base.DiagDangerous, d.Dest)
	case core.DiagRemoteImage:
		return fmt.Sprintf(base.DiagRemoteImage, d.Dest)
	case core.DiagHTMLNotExecuted:
		return base.DiagHTML
	case core.DiagMermaid:
		return base.DiagMermaid
	case core.DiagBadLink:
		return fmt.Sprintf(base.DiagBadLink, d.Dest)
	default:
		return d.Message
	}
}

func derivedSeverityColor(s core.DiagnosticSeverity, p uikit.Palette) ui.Color {
	switch s {
	case core.SeverityError:
		return uikit.MethodColor("DELETE", p)
	case core.SeverityWarning:
		return uikit.MethodColor("POST", p)
	default:
		return p.Muted
	}
}

// boundedLines splits a snippet into lines and caps the display count.
func boundedLines(sn string, max int) []string {
	lines := strings.Split(strings.TrimRight(sn, "\n"), "\n")
	if len(lines) > max {
		lines = append(lines[:max], "…")
	}
	return lines
}
