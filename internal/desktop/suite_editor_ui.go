package desktop

// Suite editor rendering (A02 phase 3). Native MyGo controls only: no WebView,
// no React, no browser aria attributes. The panel scrolls as a whole at small
// window sizes, uses the shared uikit palette for light/dark, and keeps one
// primary action (Save).

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/runner"
	"gorequest/internal/suiteedit"
)

// SuiteEditorModals renders the editor. It is called from RunnerModals through
// the small integration patch, so it survives the Runner modal being closed.
func (a *App) SuiteEditorModals(c *ui.Context, t *ui.Theme) {
	s := a.suiteEditorState(false)
	if s == nil || !s.open {
		return
	}
	m := a.suiteEditorMsg()
	p := uikit.PaletteFor(t)
	winW, winH := c.Size()
	panelW := max(float32(420), min(float32(1180), float32(winW)-40))
	panelH := max(float32(320), min(float32(820), float32(winH)-56))
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(16).Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Title).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if uikit.ActionCursor(ui.Button(c, m.Close)).Clicked() {
					a.CloseSuiteEditor()
				}
			})
			if s.confirmDiscard {
				a.suiteEditorDiscardConfirm(c, t, m, p)
			} else {
				ui.Row(c).Grow(1).FillWidth().FillHeight().Gap(12).MinHeight(0).Children(func() {
					a.suiteEditorList(c, t, m, p)
					ui.Scroll(c).Grow(1).FillWidth().FillHeight().MinHeight(0).Children(func() {
						ui.Column(c).FillWidth().Gap(10).Children(func() {
							if s.draft == nil {
								ui.Text(c, m.NewDraftHint).FontSize(12).TextColor(p.Muted)
							} else {
								a.suiteEditorBasics(c, t, m, p)
								a.suiteEditorSteps(c, t, m, p)
								a.suiteEditorDataset(c, t, m, p)
							}
						})
					})
				})
				if s.err != "" {
					ui.Text(c, s.err).FontSize(12).TextColor(t.Danger).FillWidth().Tooltip(s.err)
				}
				if s.notice != "" {
					ui.Text(c, s.notice).FontSize(12).TextColor(p.Text).FillWidth()
				}
				ui.Box(c).Height(1).Background(t.Border)
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					if uikit.ActionCursor(ui.Button(c, m.Cancel)).Clicked() {
						a.CloseSuiteEditor()
					}
					ui.Box(c).Grow(1)
					if uikit.ActionCursor(uikit.PrimaryButton(c, m.Save)).Clicked() {
						a.SaveSuiteEditor()
					}
				})
			}
		})
	})
	// Escape / outside click dismisses the modal. A dirty draft must not be
	// discarded silently: reopen the modal in confirmation state instead.
	if !s.open {
		if s.dirty() {
			s.open = true
			s.confirmDiscard = true
			s.pending = func() { s.reset() }
		} else {
			s.reset()
		}
	}
}

func (a *App) suiteEditorDiscardConfirm(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette) {
	ui.Column(c).Grow(1).FillWidth().Gap(10).Children(func() {
		ui.Text(c, m.DiscardTitle).FontSize(14).FontWeight(700).TextColor(p.Text)
		ui.Text(c, m.DiscardBody).FontSize(12).TextColor(p.Muted)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, m.KeepEditing)).Clicked() {
				a.KeepSuiteEditorDraft()
			}
			if uikit.ActionCursor(ui.Button(c, m.Discard)).Clicked() {
				a.DiscardSuiteEditorDraft()
			}
		})
	})
}

func (a *App) suiteEditorList(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette) {
	s := a.suiteEditorState(false)
	if s == nil {
		return
	}
	ui.Column(c).Width(210).FillHeight().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.SavedSuites).FontSize(12).FontWeight(700).TextColor(p.Text)
			ui.Box(c).Grow(1)
			if uikit.ActionCursor(ui.Button(c, m.NewSuite)).Clicked() {
				a.NewSuiteEditor()
			}
		})
		ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				if len(s.suites) == 0 {
					ui.Text(c, m.EmptySuites).FontSize(11).TextColor(p.Muted)
				}
				for _, rel := range s.suites {
					rel := rel
					label := rel
					if s.draft != nil && !s.draft.IsNew && s.draft.RelPath == rel {
						label = "● " + rel
					}
					if uikit.ActionCursor(ui.Button(c, label).Tooltip(rel)).Clicked() {
						a.LoadSuiteEditor(rel)
					}
				}
			})
		})
		if s.suitesSkipped > 0 {
			ui.Text(c, fmt.Sprintf(m.SuitesSkipped, s.suitesSkipped)).FontSize(10).TextColor(p.Muted)
		}
	})
}

func (a *App) suiteEditorBasics(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	d := s.draft
	ui.Text(c, m.BasicSection).FontSize(13).FontWeight(700).TextColor(p.Text)
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.IDLabel).FontSize(11).TextColor(p.Muted).Width(80)
		if d.IsNew {
			ui.TextInput(c, &d.ID).Grow(1).Placeholder("suite-id").Changed()
		} else {
			ui.Text(c, d.ID).FontSize(12).TextColor(p.Text)
		}
	})
	if !d.IsNew {
		ui.Text(c, m.ReadonlyIDHint).FontSize(10).TextColor(p.Muted).SingleLine().Tooltip(m.ReadonlyIDHint)
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.NameLabel).FontSize(11).TextColor(p.Muted).Width(80)
		ui.TextInput(c, &d.Name).Grow(1).Placeholder(m.NameLabel).Changed()
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.OnFailureLabel).FontSize(11).TextColor(p.Muted).Width(80)
		mode := 0
		if strings.EqualFold(d.OnFailure, string(runner.OnFailureContinue)) {
			mode = 1
		}
		if _, changed := uikit.SimpleSegmented(c, p, &mode, []string{m.OnFailureStop, m.OnFailureContinue}); changed {
			if mode == 1 {
				d.OnFailure = string(runner.OnFailureContinue)
			} else {
				d.OnFailure = string(runner.OnFailureStop)
			}
		}
	})
	ui.Text(c, fmt.Sprintf(m.SuitePreservedLabel, len(d.Variables), len(d.Before), len(d.After))).FontSize(10).TextColor(p.Muted)
	ui.Text(c, m.SuitePreservedHint).FontSize(10).TextColor(p.Muted).SingleLine().Tooltip(m.SuitePreservedHint)
}

func (a *App) suiteEditorSteps(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	d := s.draft
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.StepsSection).FontSize(13).FontWeight(700).TextColor(p.Text)
		ui.Text(c, fmt.Sprintf(m.StepCount, len(d.Steps))).FontSize(11).TextColor(p.Muted)
		ui.Box(c).Grow(1)
		if uikit.ActionCursor(ui.Button(c, m.AddStep)).Clicked() {
			a.AddSuiteStep()
		}
	})
	if len(d.Steps) == 0 {
		ui.Text(c, m.NoSteps).FontSize(11).TextColor(t.Warning)
	}
	for i, st := range d.Steps {
		st := st
		i := i
		selected := st.RowID == s.selectedRow
		bg := p.BG
		if selected {
			bg = p.Sidebar
		}
		ui.Row(c).Key(st.RowID).FillWidth().Gap(6).AlignItems(ui.Center).Padding(4).Radius(4).Background(bg).Children(func() {
			ui.Text(c, fmt.Sprintf("%d", i+1)).FontSize(11).TextColor(p.Muted).Width(18)
			stepLabel := st.ID
			if stepLabel == "" {
				stepLabel = "—"
			}
			if uikit.ActionCursor(ui.Button(c, stepLabel)).Clicked() {
				a.SelectSuiteStep(st.RowID)
			}
			reqLabel := a.suiteStepRequestLabel(m, st)
			reqColor := p.Text
			if strings.TrimSpace(st.RequestRef) == "" {
				reqColor = t.Warning
			}
			ui.Text(c, reqLabel).FontSize(11).TextColor(reqColor).Grow(1).SingleLine().Tooltip(reqLabel)
			if uikit.ActionCursor(ui.Button(c, m.MoveUp)).Disabled(i == 0).Clicked() {
				a.MoveSuiteStep(st.RowID, -1)
			}
			if uikit.ActionCursor(ui.Button(c, m.MoveDown)).Disabled(i == len(d.Steps)-1).Clicked() {
				a.MoveSuiteStep(st.RowID, 1)
			}
			if uikit.ActionCursor(ui.Button(c, m.DeleteStep)).Clicked() {
				a.DeleteSuiteStep(st.RowID)
			}
		})
	}
	idx := d.StepIndex(s.selectedRow)
	if idx < 0 {
		if len(d.Steps) > 0 {
			ui.Text(c, m.SelectStepHint).FontSize(11).TextColor(p.Muted)
		}
		return
	}
	a.suiteEditorStepForm(c, t, m, p, d.Steps[idx])
}

func (a *App) suiteEditorStepForm(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette, st *suiteedit.StepDraft) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil || st == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.StepIDLabel).FontSize(11).TextColor(p.Muted).Width(80)
			if st.IsNew {
				ui.TextInput(c, &st.ID).Width(220).Placeholder("step1").Changed()
			} else {
				ui.Text(c, st.ID).FontSize(12).TextColor(p.Text)
			}
		})
		if !st.IsNew {
			ui.Text(c, m.ReadonlyStepID).FontSize(10).TextColor(p.Muted).SingleLine().Tooltip(m.ReadonlyStepID)
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.StepNameLabel).FontSize(11).TextColor(p.Muted).Width(80)
			ui.TextInput(c, &st.Name).Grow(1).Placeholder(m.StepNameLabel).Changed()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.RequestLabel).FontSize(11).TextColor(p.Muted).Width(80)
			project := a.ps.project
			uikit.MenuButton(c, a.suiteStepRequestLabel(m, st), func(mm *ui.Menu) {
				opts := suiteedit.RequestOptions(project)
				if len(opts) == 0 {
					mm.Item(m.NoRequests).Disabled(true)
				}
				for _, opt := range opts {
					if opt.Entry == nil || opt.Entry.Request == nil {
						continue
					}
					label := a.suiteRequestOptionLabel(opt)
					item := mm.Item(label)
					if !opt.Selectable {
						item.Disabled(true)
						continue
					}
					if item.Chosen() {
						a.SetSuiteStepRequest(st.RowID, opt.Entry.Request.ID)
					}
				}
			})
		})
		ui.Text(c, m.NeedsLabel).FontSize(11).TextColor(p.Muted)
		needCount := 0
		for _, other := range s.draft.Steps {
			if other == nil || other.RowID == st.RowID || strings.TrimSpace(other.ID) == "" {
				continue
			}
			needCount++
			other := other
			checked := containsString(st.Needs, other.ID)
			cb := checked
			if ui.Checkbox(c, &cb, other.ID).Changed() {
				a.ToggleSuiteStepNeed(st.RowID, other.ID)
			}
		}
		if needCount == 0 {
			ui.Text(c, "—").FontSize(11).TextColor(p.Faint)
		}
		ui.Text(c, m.NeedsHint).FontSize(10).TextColor(p.Muted).SingleLine().Tooltip(m.NeedsHint)
		ui.Text(c, fmt.Sprintf(m.PreservedLabel, len(st.Assert), len(st.Extract), len(st.Before), len(st.After), st.Retry != nil)).FontSize(10).TextColor(p.Muted)
		ui.Text(c, m.PreservedHint).FontSize(10).TextColor(p.Muted).SingleLine().Tooltip(m.PreservedHint)
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			if uikit.ActionCursor(ui.Button(c, a.runnerMsg().EditorEntry)).Clicked() {
				a.OpenRunnerEditorForSuiteStep(st)
			}
			ui.Text(c, m.OpenAssertionEditorHint).FontSize(10).TextColor(p.Muted).Grow(1).SingleLine().Tooltip(m.OpenAssertionEditorHint)
		})
	})
}

func (a *App) suiteEditorDataset(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette) {
	s := a.suiteEditorState(false)
	if s == nil || s.draft == nil {
		return
	}
	d := s.draft
	ui.Text(c, m.DatasetSection).FontSize(13).FontWeight(700).TextColor(p.Text)
	mode := 0
	if d.Dataset.Enabled {
		mode = 1
		if strings.EqualFold(d.Dataset.Format, "json") {
			mode = 2
		}
	}
	if _, changed := uikit.SimpleSegmented(c, p, &mode, []string{m.DatasetNone, m.DatasetCSV, m.DatasetJSON}); changed {
		a.SetSuiteDatasetMode(mode)
	}
	if !d.Dataset.Enabled {
		ui.Text(c, m.DatasetDisabledHint).FontSize(11).TextColor(p.Muted)
		return
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.DatasetFileLabel).FontSize(11).TextColor(p.Muted).Width(120)
		if ui.TextInput(c, &d.Dataset.File).Grow(1).Placeholder("data.csv").Changed() {
			s.preview = nil
			s.previewErr = ""
		}
	})
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.DatasetMaxRowsLabel).FontSize(11).TextColor(p.Muted).Width(120)
		if ui.TextInput(c, &s.maxRowsText).Width(140).Placeholder("1000").Changed() {
			if err := s.syncDatasetNumbers(); err != nil {
				s.err = err.Error()
			} else {
				s.err = ""
			}
		}
		ui.Text(c, m.DatasetMaxBytesLabel).FontSize(11).TextColor(p.Muted).Width(90)
		if ui.TextInput(c, &s.maxBytesText).Width(160).Placeholder("8388608").Changed() {
			if err := s.syncDatasetNumbers(); err != nil {
				s.err = err.Error()
			} else {
				s.err = ""
			}
		}
	})
	if strings.EqualFold(d.Dataset.Format, "csv") {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.DatasetDelimiterLabel).FontSize(11).TextColor(p.Muted).Width(120)
			if ui.TextInput(c, &d.Dataset.Delimiter).Width(80).Placeholder(",").Changed() {
				s.preview = nil
				s.previewErr = ""
			}
		})
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
		if uikit.ActionCursor(ui.Button(c, m.PreviewButton)).Clicked() {
			a.PreviewSuiteDataset()
		}
		ui.Text(c, m.PreviewHint).FontSize(10).TextColor(p.Muted).Grow(1).SingleLine().Tooltip(m.PreviewHint)
	})
	if s.previewErr != "" {
		ui.Text(c, m.PreviewFailed+": "+s.previewErr).FontSize(11).TextColor(t.Danger).FillWidth().Tooltip(s.previewErr)
	}
	if s.preview != nil {
		a.suiteEditorPreview(c, t, m, p, s.preview)
	}
}

func (a *App) suiteEditorPreview(c *ui.Context, t *ui.Theme, m suiteEditorMessages, p uikit.Palette, pv *runner.DatasetPreview) {
	ui.Text(c, fmt.Sprintf(m.PreviewRowsLabel, len(pv.Rows))).FontSize(11).TextColor(p.Text)
	if pv.RowsTruncated || pv.BytesTruncated {
		ui.Text(c, m.PreviewTruncated).FontSize(10).TextColor(t.Warning)
	}
	ui.Text(c, fmt.Sprintf(m.PreviewLimits, pv.EffectiveMaxRows, pv.EffectiveMaxBytes)).FontSize(10).TextColor(p.Muted)
	if len(pv.SensitiveFields) > 0 {
		ui.Text(c, fmt.Sprintf(m.PreviewSensitive, strings.Join(pv.SensitiveFields, ", "))).FontSize(10).TextColor(p.Muted)
	}
	ui.Scroll(c).FillWidth().Height(160).Padding(6).Background(p.BG).Radius(4).Children(func() {
		ui.Column(c).FillWidth().Gap(2).Children(func() {
			ui.Text(c, strings.Join(pv.Fields, " · ")).Font("monospace").FontSize(10).TextColor(p.Muted).SingleLine()
			for _, row := range pv.Rows {
				parts := make([]string, 0, len(pv.Fields))
				for _, f := range pv.Fields {
					parts = append(parts, f+"="+row[f])
				}
				line := strings.Join(parts, " · ")
				ui.Text(c, line).Font("monospace").FontSize(10).TextColor(p.Text).SingleLine().Tooltip(line)
			}
		})
	})
}

func (a *App) suiteStepRequestLabel(m suiteEditorMessages, st *suiteedit.StepDraft) string {
	if st == nil {
		return ""
	}
	if a.ps == nil || a.ps.project == nil {
		return st.RequestRef
	}
	entry, err := suiteedit.ResolveRequest(a.ps.project, st.RequestRef)
	if err != nil {
		if strings.TrimSpace(st.RequestRef) == "" {
			return m.SelectRequest
		}
		return fmt.Sprintf(m.RequestUnavailable, st.RequestRef)
	}
	return a.suiteRequestEntryLabel(entry)
}

func (a *App) suiteRequestOptionLabel(opt suiteedit.RequestOption) string {
	if opt.Entry == nil || opt.Entry.Request == nil {
		return ""
	}
	label := a.suiteRequestEntryLabel(opt.Entry)
	if !opt.Selectable {
		label += " — " + opt.Reason
	}
	return label
}

func (a *App) suiteRequestEntryLabel(entry *core.RequestEntry) string {
	if entry == nil || entry.Request == nil {
		return ""
	}
	method := "GET"
	if entry.Request.HTTP != nil && strings.TrimSpace(entry.Request.HTTP.Method) != "" {
		method = strings.ToUpper(strings.TrimSpace(entry.Request.HTTP.Method))
	}
	return fmt.Sprintf("%s · %s · %s", entry.Request.Name, method, entry.RelPath)
}
