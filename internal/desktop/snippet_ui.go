package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/snippet"
)

// BuildSnippetProjectMenuItems appends the C04 snippet entry to the project
// dropdown menu. The modal itself renders via SnippetModals (integration
// patch adds that one call to App.View).
func (a *App) BuildSnippetProjectMenuItems(mm *ui.Menu) {
	if a == nil || a.ps == nil || a.ps.project == nil || mm == nil {
		return
	}
	sm := a.snippetMsg()
	mm.Separator()
	if mm.Item(sm.MenuLabel).Chosen() {
		a.OpenSnippetModal()
	}
}

// SnippetModals renders the request snippet modal when open. When closed it
// returns immediately without allocating UI nodes or requesting frames.
func (a *App) SnippetModals(c *ui.Context, t *ui.Theme) {
	st := a.peekSnippetState()
	if st == nil || !st.Open {
		return
	}
	a.snippetModal(c, t, st)
}

// snippetNoteColor maps note severity to theme color. The code is always
// printed next to the message, so color never carries meaning alone.
func snippetNoteColor(t *ui.Theme, severity string) (ui.Color, bool) {
	switch severity {
	case snippet.SeverityBlocked:
		return t.Danger, true
	case snippet.SeverityWarning, snippet.SeverityUnsupported:
		return t.Warning, true
	case snippet.SeverityScrubbed:
		return t.Warning, true
	case snippet.SeverityInfo:
		return t.TextMuted, true
	default:
		return ui.Transparent, false
	}
}

// SnippetView wraps a.View(c) and renders the snippet modal afterwards so
// headless UI tests exercise the full C04 flow even when shared files are
// reverted to the baseline (same pattern as InteropView).
func (a *App) SnippetView(c *ui.Context) {
	a.View(c)
	a.SnippetModals(c, c.Theme())
}

func (a *App) snippetModal(c *ui.Context, t *ui.Theme, st *SnippetState) {
	sm := a.snippetMsg()
	p := uikit.PaletteFor(t)

	open := st.Open
	ui.Modal(c, &open, func() {
		ui.Column(c).Width(820).MaxHeight(700).Gap(10).Padding(18).
			Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			// Header
			ui.Row(c).FillWidth().AlignItems(ui.Center).Children(func() {
				ui.Text(c, sm.ModalTitle).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
			})
			ui.Text(c, sm.SecurityNote).FontSize(11).TextColor(p.Muted)

			// Target row: request + environment snapshot
			ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
				ui.Text(c, sm.RequestLabel+":").FontSize(12).TextColor(p.Muted)
				ui.Box(c).Grow(1)
				ui.Text(c, snippetTargetLabel(st)).Font("monospace").FontSize(12).TextColor(p.Text).SingleLine()
				ui.Text(c, sm.EnvLabel+":").FontSize(12).TextColor(p.Muted)
				envLabel := st.EnvID
				if envLabel == "" {
					envLabel = sm.EnvNoneLabel
				}
				ui.Text(c, envLabel).FontSize(12).TextColor(p.Text).SingleLine()
			})

			// Language switch
			langLabels := snippetLanguageLabels()
			if _, changed := uikit.SimpleSegmented(c, p, &st.LanguageIdx, langLabels); changed {
				a.SelectSnippetLanguage(st.LanguageIdx)
			}

			if st.Err != "" {
				ui.Text(c, st.Err).FontSize(12).TextColor(t.Danger)
			}
			if st.Notice != "" {
				ui.Text(c, st.Notice).FontSize(12).TextColor(t.Success)
			}

			// Result block
			ui.Scroll(c).FillWidth().Height(360).Padding(10).
				Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Column(c).FillWidth().Gap(8).Children(func() {
					if st.Result == nil {
						ui.Text(c, sm.NoPreviewYet).FontSize(12).TextColor(p.Muted)
						return
					}
					res := st.Result
					badge := sm.ReadyBadge
					badgeColor := t.Success
					if res.Blocked != "" {
						badge = sm.BlockedBadge
						badgeColor = t.Danger
					}
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, "["+badge+"]").FontSize(12).FontWeight(700).TextColor(badgeColor)
						ui.Text(c, sm.RuntimeLabel+": "+res.Runtime).FontSize(11).TextColor(p.Muted)
					})

					if len(res.RequiredEnv) > 0 {
						ui.Text(c, fmt.Sprintf("%s (%d)", a.snippetMsg().RequiredEnvHeading, len(res.RequiredEnv))).FontSize(12).FontWeight(600).TextColor(p.Text)
						for _, env := range res.RequiredEnv {
							ui.Text(c, "• "+env).Font("monospace").FontSize(11).TextColor(t.Warning)
						}
					}

					if len(res.Notes) > 0 {
						ui.Text(c, fmt.Sprintf("%s (%d)", a.snippetMsg().NotesHeading, len(res.Notes))).FontSize(12).FontWeight(600).TextColor(p.Text)
						for _, n := range res.Notes {
							col := p.Muted
							if c2, ok := snippetNoteColor(t, n.Severity); ok {
								col = c2
							}
							ui.Text(c, fmt.Sprintf("[%s/%s] %s", n.Severity, n.Code, n.Message)).FontSize(11).TextColor(col)
						}
					}

					ui.Text(c, sm.PreviewHeading).FontSize(12).FontWeight(600).TextColor(p.Text)
					if res.Code != "" {
						for _, line := range strings.Split(res.Code, "\n") {
							ui.Text(c, line).Font("monospace").FontSize(11).TextColor(p.Text)
						}
					}
				})
			})

			// Export path + overwrite guard
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, sm.PathLabel+":").FontSize(12).TextColor(p.Muted)
				if ui.TextInput(c, &st.OutputPath).Grow(1).Placeholder(sm.PathPlaceholder).Submitted() {
					a.ExecuteSnippetExportToFile()
				}
				if uikit.ActionCursor(ui.Button(c, sm.BrowseFileButton)).Clicked() {
					a.pickLocalFileDialog(func(chosen string) {
						st.OutputPath = chosen
					})
				}
				uikit.ActionCursor(ui.Checkbox(c, &st.Overwrite, sm.AllowOverwriteLabel))
			})

			// Footer actions: refresh / copy / export / close. Copy never
			// executes code; export never overwrites without the checkbox.
			ui.Row(c).FillWidth().Gap(8).Justify(ui.End).AlignItems(ui.Center).Children(func() {
				if uikit.ActionCursor(ui.Button(c, sm.RefreshButton)).Clicked() {
					a.RefreshSnippetPreview()
				}
				if uikit.ActionCursor(ui.Button(c, sm.CopyButton)).Clicked() {
					a.CopySnippetToClipboard(c)
				}
				if uikit.ActionCursor(uikit.PrimaryButton(c, sm.ExportButton)).Clicked() {
					a.ExecuteSnippetExportToFile()
				}
				if uikit.ActionCursor(ui.Button(c, sm.CloseButton)).Clicked() {
					st.Open = false
				}
			})
		})
	})
	if !open {
		st.Open = false
	}
}

// snippetTargetLabel is the reviewed request provenance line.
func snippetTargetLabel(st *SnippetState) string {
	if st == nil || st.TargetRel == "" {
		return "—"
	}
	return st.TargetRel
}

// snippetLanguageLabels are the segmented control labels; language names are
// not localized.
func snippetLanguageLabels() []string {
	langs := snippet.Languages()
	labels := make([]string, 0, len(langs))
	for _, l := range langs {
		switch l {
		case snippet.LangCURL:
			labels = append(labels, "cURL")
		case snippet.LangGo:
			labels = append(labels, "Go")
		case snippet.LangPython:
			labels = append(labels, "Python")
		case snippet.LangJavaScript:
			labels = append(labels, "JavaScript")
		case snippet.LangTypeScript:
			labels = append(labels, "TypeScript")
		default:
			labels = append(labels, string(l))
		}
	}
	return labels
}
