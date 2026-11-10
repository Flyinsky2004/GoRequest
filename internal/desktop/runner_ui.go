package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/runner"
	"weak"
)

func (a *App) BuildRunnerProjectMenuItems(mm *ui.Menu) {
	if mm.Item(a.runnerMsg().Title).Chosen() {
		a.OpenRunner()
	}
}
func (a *App) RunnerModals(c *ui.Context, t *ui.Theme) {
	// The assertion/extraction editor renders independently of the runner modal
	// itself; it opens from the runner modal's entry button and survives the
	// runner modal being closed.
	a.RunnerEditorModals(c, t)
	a.SuiteEditorModals(c, t)
	if child := a.suiteEditorState(false); child != nil && child.open {
		return
	}
	if child := a.runnerEditorState(false); child != nil && child.open {
		return
	}
	s := a.runnerState(false)
	if s == nil || !s.open {
		return
	}
	m := a.runnerMsg()
	p := uikit.PaletteFor(t)
	winW, winH := c.Size()
	panelW := max(float32(320), min(float32(880), float32(winW)-48))
	panelH := max(float32(240), min(float32(740), float32(winH)-64))
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(18).Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Title).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if ui.Button(c, m.Cancel).Disabled(!s.running).Clicked() {
					a.CancelRunner()
				}
				if ui.Button(c, m.Close).Clicked() {
					a.CancelRunner()
					s.open = false
				}
			})
			ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					ui.Text(c, m.Snapshot).FontSize(12).TextColor(p.Muted)
					if a.ps != nil && a.ps.hasDirty() {
						ui.Text(c, m.Dirty).FontSize(13).TextColor(t.Warning)
					}
					if !s.running {
						uikit.SimpleSegmented(c, p, &s.mode, []string{m.Collection, m.Suite})
						if s.mode == 0 {
							runnerSelect(c, m.Collection, &s.collection, s.collections, m.Empty)
						} else {
							runnerSelect(c, m.Suite, &s.suite, s.suites, m.Empty)
						}
						runnerSelect(c, m.Environment, &s.environment, s.environments, m.Empty)
						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							ui.Text(c, m.StepConcurrency).FontSize(12)
							ui.TextInput(c, &s.stepConcurrencyText).Width(64)
							ui.Text(c, m.RowConcurrency).FontSize(12)
							ui.TextInput(c, &s.rowConcurrencyText).Width(64)
						})
						ui.Text(c, m.ConcurrencyHint).FontSize(11).TextColor(p.Muted)
					} else {
						ui.Text(c, m.Environment+": "+runnerValue(s.environment)).FontSize(12)
					}
					a.runnerMockConfigUI(c, t, p, s)
					ui.Row(c).Gap(10).Children(func() {
						if ui.Button(c, m.Start).Disabled(s.running).Clicked() {
							a.StartRunner()
						}
						if ui.Button(c, m.EditorEntry).Clicked() {
							a.OpenRunnerEditor()
						}
						if ui.Button(c, a.suiteEditorMsg().OpenEntry).Clicked() {
							a.OpenSuiteEditor()
						}
					})
					if s.notice != "" {
						ui.Text(c, s.notice).FontSize(13).TextColor(p.Text)
					}
					if s.result != nil {
						ui.Text(c, runnerSummaryText(s.result)).FontSize(12).TextColor(p.Text)
						ui.Text(c, "Environment: "+runnerValue(s.result.Environment)).FontSize(12).TextColor(p.Text)
						ui.Text(c, "Snapshot: "+s.result.Snapshot).Font("monospace").FontSize(11).TextColor(p.Muted)
						if s.result.Failure != nil {
							ui.Text(c, string(s.result.Failure.Category)+" / "+string(s.result.Failure.Kind)+": "+s.result.Failure.Message).FontSize(12).TextColor(t.Danger)
						}
					}
					a.runnerMockResultUI(c, t, p, s)
					// Live events matter while a run is active (and before the first
					// run). Once a final result exists, the filtered/paged result view
					// replaces the event tail instead of overflowing the panel.
					if s.running || s.result == nil {
						ui.Text(c, m.EventLimit).FontSize(11).TextColor(p.Muted)
						ui.Scroll(c).FillWidth().Height(220).Padding(8).Background(p.Sidebar).Radius(6).Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								for _, e := range s.events {
									ui.Text(c, fmt.Sprintf("%d · row %d · %s · attempt %d · %s · %s · HTTP %d %s", e.Seq, e.Row, e.StepID, e.Attempt, e.Kind, e.Status, e.HTTPStatus, e.Message)).Font("monospace").FontSize(11).TextColor(p.Text)
								}
							})
						})
					}
					// Final rows include skipped steps that emitted no live event. The
					// view is filtered and paged; the full RunResult still backs export.
					if s.result != nil {
						labels := []string{m.FilterAll, m.FilterFailed, m.FilterSkipped, m.FilterCanceled, m.FilterConfig, m.FilterInternal, m.FilterPassed}
						if s.resultFilter < 0 || s.resultFilter >= len(labels) {
							s.resultFilter = 0
						}
						failures := runner.FailureLocations(s.result, 0)
						page := runner.QueryResult(s.result, runner.ResultFilter{Statuses: runnerFilterStatuses(s.resultFilter), Text: s.resultFilterText}, s.resultOffset, s.resultLimit)
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							ui.Text(c, m.ResultFilter).FontSize(12)
							uikit.MenuButton(c, labels[s.resultFilter], func(mm *ui.Menu) {
								for i, label := range labels {
									if mm.Item(label).Chosen() {
										s.resultFilter = i
										s.resultOffset = 0
									}
								}
							})
							if ui.TextInput(c, &s.resultFilterText).Grow(1).Placeholder(m.FilterText).Changed() {
								s.resultOffset = 0
							}
							if ui.Button(c, m.ShowFailures).Disabled(failures.Total == 0).Clicked() {
								s.resultFilter = 1
								s.resultOffset = 0
							}
						})
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							if ui.Button(c, m.PrevPage).Disabled(s.resultOffset == 0).Clicked() {
								s.resultOffset = max(0, s.resultOffset-s.resultLimit)
							}
							if ui.Button(c, m.NextPage).Disabled(!page.HasMore).Clicked() {
								s.resultOffset += s.resultLimit
							}
							if len(page.Items) == 0 {
								ui.Text(c, m.NoMatches).FontSize(12).TextColor(p.Muted)
							} else {
								ui.Text(c, fmt.Sprintf(m.PageInfo, s.resultOffset+1, s.resultOffset+len(page.Items), page.Total)).FontSize(12).TextColor(p.Muted)
							}
							ui.Box(c).Grow(1)
							ui.Text(c, fmt.Sprintf(m.FailureSummary, failures.Total)).FontSize(12).TextColor(p.Muted)
						})
						ui.Scroll(c).FillWidth().Height(180).Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								for _, item := range page.Items {
									detail := fmt.Sprintf("row %d · %s · %s · %s · attempts %d", item.Row, item.StepID, item.Status, item.Request, item.Attempts)
									if item.Retries > 0 {
										detail += fmt.Sprintf(" · retries %d", item.Retries)
									}
									if item.HTTPStatus > 0 {
										detail += fmt.Sprintf(" · HTTP %d", item.HTTPStatus)
									}
									if item.FailureCategory != "" {
										detail += fmt.Sprintf(" · %s/%s: %s", item.FailureCategory, item.FailureKind, item.FailureMessage)
									} else if item.Reason != "" {
										detail += " · " + item.Reason
									}
									ui.Text(c, detail).FontSize(12).SingleLine().TextColor(runnerItemColor(p, t, item.Status)).Tooltip(detail)
								}
							})
						})
					}

				})
			})
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, m.ExportPath).FontSize(12).TextColor(p.Muted)
				ui.TextInput(c, &s.exportPath).FillWidth()
				ui.Row(c).Gap(10).Children(func() {
					if ui.Button(c, m.Browse).Disabled(s.running || s.result == nil).Clicked() {
						a.chooseRunnerReportPath()
					}
					if ui.Button(c, m.JSON).Disabled(s.running || s.result == nil).Clicked() {
						a.exportRunner(false)
					}
					if ui.Button(c, m.JUnit).Disabled(s.running || s.result == nil).Clicked() {
						a.exportRunner(true)
					}
				})
				if s.exportNotice != "" {
					ui.Text(c, s.exportNotice).FontSize(12).TextColor(p.Text).MaxHeight(44).Tooltip(s.exportNotice)
				}
			})
		})
	})
	// Escape / outside-click dismissals cancel too; no invisible background run.
	if !s.open {
		a.CancelRunner()
	}
}
func runnerValue(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}
func runnerSelect(c *ui.Context, label string, selected *string, choices []string, empty string) {
	ui.Row(c).Gap(10).Children(func() {
		ui.Text(c, label).FontSize(12)
		uikit.MenuButton(c, runnerValue(*selected), func(mm *ui.Menu) {
			if len(choices) == 0 {
				mm.Item(empty).Disabled(true)
			}
			for _, choice := range choices {
				if mm.Item(runnerValue(choice)).Chosen() {
					*selected = choice
				}
			}
		})
	})
}
func (a *App) exportRunner(junit bool) {
	s := a.runnerState(false)
	if s == nil || s.running {
		return
	}
	if err := writeRunnerReport(s.result, s.exportPath, s.snapshotRoot, junit); err != nil {
		s.exportNotice = "internal_error / report: " + err.Error()
		a.notifyError(a.feedbackMsg().ExportFailed, err.Error())
	} else {
		s.exportNotice = a.runnerMsg().Exported
		a.notifySuccess(a.feedbackMsg().ExportOK)
	}
}
func (a *App) chooseRunnerReportPath() {
	s := a.runnerState(false)
	if s == nil || a.win == nil {
		return
	}
	nw, ok := a.win.(nativeWindower)
	if !ok || nw.nativeWindow() == nil {
		s.exportNotice = "Native file dialog unavailable; enter an absolute local path."
		return
	}
	parent := nw.nativeWindow()
	win := a.win
	key := weak.Make(a)
	generation := s.generation
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{Parent: parent, Title: "Runner report", DefaultPath: "runner-report.json"})
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			st := owner.runnerState(false)
			if st == nil || st.generation != generation {
				return
			}
			if err != nil {
				st.exportNotice = "internal_error / report: file dialog failed"
			} else if path != "" {
				st.exportPath = path
			}
		})
	}()
}

// runnerResultDetail helpers render the per-step assertion/extraction verdicts
// already redacted by the engine; failed rule evaluations draw attention in
// Danger while passed facts stay quiet.
func runnerResultDetailColor(p uikit.Palette, t *ui.Theme, ok bool) ui.Color {
	if ok {
		return p.Muted
	}
	return t.Danger
}

func runnerAssertionDetailText(ar runner.AssertionResult) string {
	verdict := "PASS"
	if !ar.Passed {
		verdict = "FAIL"
	}
	target := ar.Target
	if target == "" {
		target = ar.Kind
	}
	parts := []string{fmt.Sprintf("  assert %s: %s", target, verdict)}
	if ar.Expected != "" {
		parts = append(parts, "expected "+ar.Expected)
	}
	if ar.Actual != "" {
		parts = append(parts, "actual "+ar.Actual)
	}
	if ar.Message != "" {
		parts = append(parts, ar.Message)
	}
	return strings.Join(parts, " · ")
}

func runnerExtractionDetailText(er runner.ExtractionResult) string {
	verdict := "published"
	if !er.Published {
		verdict = "NOT published"
	}
	target := er.Target
	if target == "" {
		target = er.From
	}
	parts := []string{fmt.Sprintf("  extract %s (%s): %s", er.Name, target, verdict)}
	if er.Required {
		parts = append(parts, "required")
	}
	if er.Sensitive {
		parts = append(parts, "sensitive")
	}
	if er.Message != "" {
		parts = append(parts, er.Message)
	}
	return strings.Join(parts, " · ")
}

// runnerFilterStatuses maps the compact filter menu to engine statuses. Index 0
// (All) returns nil so QueryResult keeps every step.
func runnerFilterStatuses(index int) []runner.Status {
	switch index {
	case 1:
		return []runner.Status{runner.StatusFailed}
	case 2:
		return []runner.Status{runner.StatusSkipped}
	case 3:
		return []runner.Status{runner.StatusCanceled}
	case 4:
		return []runner.Status{runner.StatusConfigError}
	case 5:
		return []runner.Status{runner.StatusInternalError}
	case 6:
		return []runner.Status{runner.StatusPassed}
	default:
		return nil
	}
}

func runnerItemColor(p uikit.Palette, t *ui.Theme, status runner.Status) ui.Color {
	switch status {
	case runner.StatusFailed, runner.StatusConfigError, runner.StatusInternalError:
		return t.Danger
	case runner.StatusCanceled:
		return t.Warning
	default:
		return p.Text
	}
}
