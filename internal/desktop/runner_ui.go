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
					ui.Text(c, m.EventLimit).FontSize(11).TextColor(p.Muted)
					ui.Scroll(c).FillWidth().Height(220).Padding(8).Background(p.Sidebar).Radius(6).Children(func() {
						ui.Column(c).FillWidth().Gap(4).Children(func() {
							for _, e := range s.events {
								ui.Text(c, fmt.Sprintf("%d · row %d · %s · attempt %d · %s · %s · HTTP %d %s", e.Seq, e.Row, e.StepID, e.Attempt, e.Kind, e.Status, e.HTTPStatus, e.Message)).Font("monospace").FontSize(11).TextColor(p.Text)
							}
						})
					})
					// Final rows include skipped steps, which may not have emitted live events.
					if s.result != nil {
						ui.Scroll(c).FillWidth().Height(125).Children(func() {
							ui.Column(c).FillWidth().Gap(4).Children(func() {
								count := 0
								for _, row := range s.result.Rows {
									for _, step := range row.Steps {
										if count >= runnerEventLimit {
											continue
										}
										count++
										ui.Text(c, fmt.Sprintf("row %d · %s · %s · attempts %d · retries %d · %s", row.Index, step.ID, step.Status, len(step.Attempts), step.RetryCount(), step.Reason)).FontSize(12).TextColor(p.Text)
										for _, attempt := range step.Attempts {
											if count >= runnerEventLimit {
												break
											}
											count++
											ui.Text(c, fmt.Sprintf("  attempt %d · %s · HTTP %d · %s", attempt.Number, attempt.Status, attempt.HTTPStatus, formatDurationMs(float64(attempt.DurationMs)))).FontSize(11).TextColor(p.Muted)
											if attempt.Failure != nil {
												ui.Text(c, string(attempt.Failure.Category)+" / "+string(attempt.Failure.Kind)+": "+attempt.Failure.Message).FontSize(11).TextColor(t.Danger)
											}
										}
										for _, ar := range step.Assertions {
											if count >= runnerEventLimit {
												break
											}
											count++
											ui.Text(c, runnerAssertionDetailText(ar)).FontSize(11).SingleLine().TextColor(runnerResultDetailColor(p, t, ar.Passed)).Tooltip(runnerAssertionDetailText(ar))
										}
										for _, er := range step.Extractions {
											if count >= runnerEventLimit {
												break
											}
											count++
											ui.Text(c, runnerExtractionDetailText(er)).FontSize(11).SingleLine().TextColor(runnerResultDetailColor(p, t, er.Published)).Tooltip(runnerExtractionDetailText(er))
										}
										if step.Failure != nil {
											ui.Text(c, string(step.Failure.Category)+" / "+string(step.Failure.Kind)+": "+step.Failure.Message).FontSize(11).TextColor(t.Danger)
										}
									}
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
