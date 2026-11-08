package desktop

import (
	"fmt"
	"strings"
	"weak"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/diagnostics"
)

// BuildPerformanceProjectMenuItems adds the HTTP Performance Diagnostics item
// to the Project menu.
func (a *App) BuildPerformanceProjectMenuItems(mm *ui.Menu) {
	if mm.Item(a.perfMsg().MenuLabel).Chosen() {
		a.OpenPerformance()
	}
}

// PerformanceModals renders the HTTP Performance Diagnostics modal when open
// for the current window. Dismissing the modal cancels any active run in that
// window so no invisible background sampling continues.
func (a *App) PerformanceModals(c *ui.Context, t *ui.Theme) {
	s := a.performanceState(false)
	if s == nil || !s.open {
		return
	}
	m := a.perfMsg()
	p := uikit.PaletteFor(t)
	winW, winH := c.Size()
	panelW := max(float32(360), min(float32(960), float32(winW)-40))
	panelH := max(float32(280), min(float32(780), float32(winH)-56))

	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(18).Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			// Header bar
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Column(c).Gap(2).Grow(1).Children(func() {
					ui.Text(c, m.Title).FontSize(16).FontWeight(700).TextColor(p.Text)
					ui.Text(c, m.Subtitle).FontSize(11).TextColor(p.Muted)
				})
				if s.report != nil && !s.running {
					if ui.Button(c, m.PinBaseline).Clicked() {
						a.PinPerformanceBaseline()
					}
				}
				if s.baseline != nil && !s.running {
					if ui.Button(c, m.ClearBaseline).Clicked() {
						a.ClearPerformanceBaseline()
					}
				}
				if ui.Button(c, m.Cancel).Disabled(!s.running).Clicked() {
					a.CancelPerformance()
				}
				if ui.Button(c, m.Close).Clicked() {
					a.CancelPerformance()
					s.open = false
				}
			})

			// Scrollable main body
			ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
				ui.Column(c).FillWidth().Gap(10).Children(func() {
					ui.Text(c, m.ScopeNotice).FontSize(12).TextColor(p.Muted)
					if a.ps != nil && a.ps.hasDirty() {
						ui.Text(c, m.DirtyWarn).FontSize(12).TextColor(t.Warning)
					}

					// Sampling Configuration Card
					ui.Column(c).FillWidth().Gap(8).Padding(10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).Children(func() {
						if !s.running {
							ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
								perfSelect(c, m.Request, &s.requestRef, s.requests, m.NoRequests)
								perfSelect(c, m.Environment, &s.environment, s.environments, m.NoRequests)
							})
							ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
								perfLabeledInput(c, p, m.Iterations, &s.iterationsText, 68)
								perfLabeledInput(c, p, m.Concurrency, &s.concurrencyText, 60)
								perfLabeledInput(c, p, m.Warmup, &s.warmupText, 60)
								perfLabeledInput(c, p, m.IntervalMs, &s.intervalMsText, 76)
								perfLabeledInput(c, p, m.DeadlineSec, &s.deadlineSecText, 68)
								perfLabeledInput(c, p, m.Seed, &s.seedText, 76)
							})
							ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
								ui.Text(c, m.Strategy).FontSize(12).TextColor(p.Muted)
								ui.Box(c).Width(320).Children(func() {
									uikit.SimpleSegmented(c, p, &s.strategyMode, []string{m.StrategyReuse, m.StrategyFresh})
								})
								ui.Checkbox(c, &s.gateEnabled, m.EnableGate)
							})
							if s.gateEnabled {
								ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
									perfSelect(c, m.GateQuantile, &s.gateQuantile, []string{"P50", "P90", "P95", "P99", "MEAN", "MAX"}, "P95")
									perfLabeledInput(c, p, m.GateMaxMs, &s.gateMaxMsText, 76)
									perfLabeledInput(c, p, m.GateMinSamples, &s.gateMinSamplesText, 68)
									perfLabeledInput(c, p, m.GateMaxFailPct, &s.gateMaxFailRateText, 76)
								})
							}
						} else {
							ui.Text(c, fmt.Sprintf("%s: %s · %s: %s", m.Request, perfValue(s.requestRef), m.Environment, perfValue(s.environment))).FontSize(12).TextColor(p.Text)
						}

						ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
							if uikit.PrimaryButton(c, m.Start).Disabled(s.running || len(s.requests) == 0).Clicked() {
								a.StartPerformance()
							}
							if s.baseline != nil {
								ui.Text(c, fmt.Sprintf("Baseline pinned: %s (%s)", s.baseline.RunID, s.baseline.RequestPath)).Font("monospace").FontSize(11).TextColor(p.Muted)
							}
						})
					})

					if s.notice != "" {
						col := p.Text
						if s.report != nil && (s.report.Status == diagnostics.StatusConfigError || s.report.Status == diagnostics.StatusPolicyBlocked || s.report.Status == diagnostics.StatusGateFailed) {
							col = t.Danger
						}
						ui.Text(c, s.notice).FontSize(12).TextColor(col)
					}

					// Live progress stream (bounded to 256 events)
					if len(s.events) > 0 {
						ui.Scroll(c).FillWidth().Height(96).Padding(8).Background(p.Sidebar).Radius(6).Children(func() {
							ui.Column(c).FillWidth().Gap(3).Children(func() {
								for _, ev := range s.events {
									phase := "measured"
									if ev.IsWarmup {
										phase = "warmup"
									}
									ui.Text(c, fmt.Sprintf("#%d · %s · sample %d (%s) · %s · HTTP %d · %.2f ms %s",
										ev.Seq, ev.Kind, ev.SampleIndex, phase, ev.Outcome, ev.StatusCode, ev.DurationMs, ev.Message)).
										Font("monospace").FontSize(11).TextColor(p.Text)
								}
							})
						})
					}

					// Detailed Report Inspector Tabs
					if s.report != nil {
						uikit.SimpleSegmented(c, p, &s.viewTab, []string{
							m.TabSummary,
							m.TabStages,
							m.TabTimeline,
							m.TabSamples,
							m.TabCompare,
						})
						uikit.TabContent(c, s.viewTab, func(tab int) {
							switch tab {
							case 0:
								renderPerfSummaryTab(c, t, p, s.report)
							case 1:
								renderPerfStagesTab(c, p, s.report)
							case 2:
								renderPerfTimelineTab(c, p, s)
							case 3:
								renderPerfSamplesTab(c, t, p, s.report)
							case 4:
								renderPerfCompareTab(c, t, p, s)
							}
						})
					}
				})
			})

			// Explicit Local Export Footer
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, m.ExportPath).FontSize(12).TextColor(p.Muted)
				ui.TextInput(c, &s.exportPath).FillWidth().Font("monospace")
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					if ui.Button(c, m.Browse).Disabled(s.running || s.report == nil).Clicked() {
						a.choosePerformanceReportPath(diagnostics.ExportJSON)
					}
					if ui.Button(c, m.ExportJSON).Disabled(s.running || s.report == nil).Clicked() {
						a.exportPerformance(diagnostics.ExportJSON)
					}
					if ui.Button(c, m.ExportCSV).Disabled(s.running || s.report == nil).Clicked() {
						a.exportPerformance(diagnostics.ExportCSV)
					}
				})
				if s.exportNotice != "" {
					ui.Text(c, s.exportNotice).FontSize(12).TextColor(p.Text).MaxHeight(44).Tooltip(s.exportNotice)
				}
			})
		})
	})

	if !s.open {
		a.CancelPerformance()
	}
}

func renderPerfSummaryTab(c *ui.Context, t *ui.Theme, p uikit.Palette, rep *diagnostics.Report) {
	ui.Column(c).FillWidth().Gap(8).Padding(10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).Children(func() {
		ui.Text(c, fmt.Sprintf("Run ID: %s · Request: %s (%s %s) · Env: %s · Strategy: %s · Reuse Support: %s",
			rep.RunID, rep.RequestPath, rep.Method, rep.TargetURL, perfValue(rep.Environment), rep.Config.ConnectionStrategy, rep.ReuseSupport)).
			Font("monospace").FontSize(11).TextColor(p.Muted)
		ui.Text(c, fmt.Sprintf("Snapshot Digest: %s · Seed: %d · Proxy: %s · Total Elapsed: %.2f ms",
			rep.SnapshotDigest, rep.Config.Seed, perfValue(rep.ProxyMode), rep.TotalElapsedMs)).
			Font("monospace").FontSize(11).TextColor(p.Muted)

		if rep.ReuseNote != "" {
			ui.Text(c, "TransportReuse Note: "+rep.ReuseNote).FontSize(12).TextColor(t.Warning)
		}

		sum := rep.Summary
		ui.Text(c, fmt.Sprintf(
			"Outcomes: Measured=%d (Warmup=%d) · Success=%d (%.1f%%) · Failed=%d (%.1f%%) [Errors=%d, Timeouts=%d, Canceled=%d, Truncated=%d] · Reused=%d / Fresh=%d",
			sum.MeasuredTotal, sum.WarmupCompleted, sum.SuccessCount, sum.SuccessRate*100,
			sum.FailureCount, sum.FailureRate*100, sum.ErrorCount, sum.TimeoutCount, sum.CanceledCount, sum.TruncatedCount,
			sum.ReusedConnCount, sum.FreshConnCount,
		)).FontSize(12).TextColor(p.Text)

		if rep.SuccessLatency != nil {
			sl := rep.SuccessLatency
			ui.Text(c, fmt.Sprintf(
				"Success Latency (%s, n=%d): Mean=%.3f ms · Min=%.3f ms · P50=%.3f ms · P90=%.3f ms · P95=%.3f ms · P99=%.3f ms · Max=%.3f ms",
				sl.Method, sl.Count, sl.MeanMs, sl.MinMs, sl.P50Ms, sl.P90Ms, sl.P95Ms, sl.P99Ms, sl.MaxMs,
			)).Font("monospace").FontSize(12).TextColor(p.Text)
			if sl.ResolutionNote != "" {
				ui.Text(c, sl.ResolutionNote).FontSize(11).TextColor(p.Muted)
			}
		} else {
			ui.Text(c, "Success Latency: n=0 (no complete successful samples observed; quantiles not applicable)").FontSize(12).TextColor(t.Warning)
		}

		if rep.FailureLatency != nil {
			fl := rep.FailureLatency
			ui.Text(c, fmt.Sprintf(
				"Failure Latency (separate from success, n=%d): Mean=%.3f ms · Min=%.3f ms · P50=%.3f ms · P95=%.3f ms · Max=%.3f ms",
				fl.Count, fl.MeanMs, fl.MinMs, fl.P50Ms, fl.P95Ms, fl.MaxMs,
			)).Font("monospace").FontSize(12).TextColor(p.Muted)
		}

		if rep.GateResult != nil {
			gr := rep.GateResult
			col := p.Text
			if !gr.Passed {
				col = t.Danger
			}
			ui.Text(c, fmt.Sprintf(
				"Threshold Gate [%s]: %s=%0.3f ms <= %0.3f ms · valid n=%d (min %d) · failureRate=%.3f (max %.3f)",
				gr.Status, gr.Quantile, gr.ObservedQuantileMs, gr.MaxLatencyMs, gr.ValidSamples, gr.MinValidSamples, gr.FailureRate, gr.MaxFailureRate,
			)).Font("monospace").FontSize(12).TextColor(col)
			for _, reason := range gr.Reasons {
				ui.Text(c, "  • "+reason).FontSize(11).TextColor(col)
			}
		}

		for _, lim := range rep.Limitations {
			ui.Text(c, "Limitation: "+lim).FontSize(11).TextColor(p.Muted)
		}
	})
}

func renderPerfStagesTab(c *ui.Context, p uikit.Palette, rep *diagnostics.Report) {
	ui.Column(c).FillWidth().Gap(6).Padding(10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).Children(func() {
		ui.Text(c, "Per-Stage Distributions (only genuinely observed & complete samples count toward independent n; reused/unobserved stages are never 0 ms):").
			FontSize(12).TextColor(p.Muted)
		for _, kind := range diagnostics.AllStageKinds {
			st := rep.StageStats[kind]
			if st == nil {
				continue
			}
			if st.Stats != nil && st.ObservedCount > 0 {
				d := st.Stats
				ui.Text(c, fmt.Sprintf(
					"%-14s | n=%-3d (reused=%d, unobs=%d, incomp=%d, n/a=%d) | Mean=%7.2f ms | Min=%7.2f | P50=%7.2f | P90=%7.2f | P95=%7.2f | P99=%7.2f | Max=%7.2f",
					kind, st.ObservedCount, st.SkippedReusedCount, st.UnobservedCount, st.IncompleteCount, st.NotApplicableCount,
					d.MeanMs, d.MinMs, d.P50Ms, d.P90Ms, d.P95Ms, d.P99Ms, d.MaxMs,
				)).Font("monospace").FontSize(11).TextColor(p.Text)
			} else {
				ui.Text(c, fmt.Sprintf(
					"%-14s | n=0   (reused=%d, unobs=%d, incomp=%d, n/a=%d) | — (not observed or skipped via connection reuse)",
					kind, st.SkippedReusedCount, st.UnobservedCount, st.IncompleteCount, st.NotApplicableCount,
				)).Font("monospace").FontSize(11).TextColor(p.Muted)
			}
		}
	})
}

func renderPerfTimelineTab(c *ui.Context, p uikit.Palette, s *performanceState) {
	rep := s.report
	ui.Column(c).FillWidth().Gap(8).Padding(10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).Children(func() {
		if len(rep.Samples) == 0 {
			ui.Text(c, "No measured samples available for timeline inspection.").FontSize(12).TextColor(p.Muted)
			return
		}
		if s.selectedSampleIdx < 0 || s.selectedSampleIdx >= len(rep.Samples) {
			s.selectedSampleIdx = 0
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, fmt.Sprintf("Inspecting Measured Sample #%d of %d", s.selectedSampleIdx+1, len(rep.Samples))).FontSize(12).FontWeight(600).TextColor(p.Text)
			if ui.Button(c, "Prev Sample").Disabled(s.selectedSampleIdx <= 0).Clicked() {
				s.selectedSampleIdx--
			}
			if ui.Button(c, "Next Sample").Disabled(s.selectedSampleIdx+1 >= len(rep.Samples)).Clicked() {
				s.selectedSampleIdx++
			}
		})

		sample := rep.Samples[s.selectedSampleIdx]
		for _, note := range sample.TimelineNotes {
			ui.Text(c, note).FontSize(11).TextColor(p.Muted)
		}
		for _, hop := range sample.Hops {
			ui.Column(c).FillWidth().Gap(4).Padding(8).Background(p.Sidebar).Radius(6).Children(func() {
				ui.Text(c, fmt.Sprintf(
					"Hop #%d · %s %s · HTTP %d (%s) · Remote=%s · Reused=%v (known=%v) · Proxy=%s · TLS=%s %s",
					hop.Hop, hop.Method, hop.URL, hop.StatusCode, perfValue(hop.Proto),
					perfValue(hop.RemoteAddr), hop.ReusedConn, hop.ReusedKnown, perfValue(hop.ProxyUsed),
					hop.TLSVersion, hop.TLSCipherSuite,
				)).Font("monospace").FontSize(11).TextColor(p.Text)

				if hop.OriginChanged || hop.StrippedCredentials {
					ui.Text(c, fmt.Sprintf("  Redirect provenance: from=%s · originChanged=%v · strippedCredentials=%v",
						hop.FromURL, hop.OriginChanged, hop.StrippedCredentials)).Font("monospace").FontSize(11).TextColor(p.Muted)
				}

				for _, da := range hop.DialAttempts {
					winTag := ""
					if da.Winner {
						winTag = " [WINNER]"
					}
					ivText := ""
					if da.Interval != nil {
						ivText = fmt.Sprintf("[%.2f..%.2f ms]", da.Interval.StartOffsetMs, da.Interval.EndOffsetMs)
					}
					ui.Text(c, fmt.Sprintf("  DialAttempt: %s %s%s · observed=%v · %.2f ms %s %s",
						da.Network, da.RemoteAddr, winTag, da.Observed, da.DurationMs, ivText, da.Error)).
						Font("monospace").FontSize(11).TextColor(p.Muted)
				}

				for _, kind := range diagnostics.AllStageKinds {
					sm, ok := hop.Stages[kind]
					if !ok {
						continue
					}
					if sm.Status == diagnostics.ObsObserved {
						ivStr := ""
						if sm.Interval != nil {
							ivStr = fmt.Sprintf("[%.2f..%.2f ms]", sm.Interval.StartOffsetMs, sm.Interval.EndOffsetMs)
						}
						ui.Text(c, fmt.Sprintf("  %-14s | %-22s | %7.2f ms %-18s | %s",
							kind, sm.Status, sm.DurationMs, ivStr, sm.Note)).Font("monospace").FontSize(11).TextColor(p.Text)
					} else {
						ui.Text(c, fmt.Sprintf("  %-14s | %-22s | — (not summed/not 0ms)     | %s %s",
							kind, sm.Status, sm.Note, sm.Error)).Font("monospace").FontSize(11).TextColor(p.Muted)
					}
				}

				for _, lim := range hop.Limitations {
					ui.Text(c, "  Limitation: "+lim).FontSize(11).TextColor(p.Muted)
				}
			})
		}
	})
}

func renderPerfSamplesTab(c *ui.Context, t *ui.Theme, p uikit.Palette, rep *diagnostics.Report) {
	ui.Column(c).FillWidth().Gap(4).Padding(10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).Children(func() {
		for _, ws := range rep.WarmupSamples {
			ui.Text(c, fmt.Sprintf("[warmup #%d] %s · HTTP %d · %.2f ms · reused=%v · redirects=%d · %s",
				ws.Index, ws.Outcome, ws.StatusCode, ws.DurationMs, ws.ReusedConn, ws.RedirectCount, ws.Error)).
				Font("monospace").FontSize(11).TextColor(p.Muted)
		}
		for _, ms := range rep.Samples {
			col := p.Text
			if ms.Outcome != diagnostics.OutcomeSuccess || ms.Truncated {
				col = t.Danger
			}
			ui.Text(c, fmt.Sprintf("[sample #%d] %s · HTTP %d (%s) · %.2f ms · reused=%v · body=%d B (truncated=%v) · redirects=%d · %s",
				ms.Index, ms.Outcome, ms.StatusCode, perfValue(ms.Proto), ms.DurationMs, ms.ReusedConn, ms.BodySize, ms.Truncated, ms.RedirectCount, ms.Error)).
				Font("monospace").FontSize(11).TextColor(col)
		}
	})
}

func renderPerfCompareTab(c *ui.Context, t *ui.Theme, p uikit.Palette, s *performanceState) {
	ui.Column(c).FillWidth().Gap(6).Padding(10).BorderWidth(1).BorderColor(p.Hairline).Radius(8).Children(func() {
		if s.baseline == nil {
			ui.Text(c, "No baseline report pinned yet. Click 'Pin Current as Baseline' after a run to compare subsequent diagnostic runs.").
				FontSize(12).TextColor(p.Muted)
			return
		}
		comp := s.comparison
		if comp == nil && s.report != nil {
			comp = diagnostics.CompareReports(s.baseline, s.report)
			s.comparison = comp
		}
		if comp == nil {
			return
		}
		ui.Text(c, comp.AttributionDisclaimer).FontSize(11).TextColor(t.Warning)
		ui.Text(c, fmt.Sprintf("Baseline=%s vs Candidate=%s · Comparable=%v · SnapshotMatched=%v · EnvMatched=%v · StrategyMatched=%v · SeedMatched=%v",
			comp.BaselineRunID, comp.CandidateRunID, comp.Comparable, comp.SnapshotMatched, comp.EnvironmentMatched, comp.StrategyMatched, comp.SeedMatched)).
			Font("monospace").FontSize(11).TextColor(p.Text)

		for _, diff := range comp.ConfigDiffs {
			col := p.Muted
			if diff.Severity == "blocking" {
				col = t.Danger
			}
			ui.Text(c, fmt.Sprintf("ConfigDiff [%s] %s: %q -> %q (%s)",
				diff.Severity, diff.Field, diff.Baseline, diff.Candidate, diff.Note)).Font("monospace").FontSize(11).TextColor(col)
		}

		for _, d := range comp.OverallDeltas {
			ui.Text(c, fmt.Sprintf("%-12s | base=%.3f (n=%d) | cand=%.3f (n=%d) | delta=%+0.3f (%+0.1f%%) | comparable=%v %s",
				d.Metric, d.BaselineValue, d.BaselineN, d.CandidateValue, d.CandidateN, d.Delta, d.DeltaPct, d.Comparable, d.Note)).
				Font("monospace").FontSize(11).TextColor(p.Text)
		}
	})
}

func perfValue(s string) string {
	if strings.TrimSpace(s) == "" {
		return "(none)"
	}
	return s
}

func perfSelect(c *ui.Context, label string, selected *string, choices []string, empty string) {
	ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(12)
		uikit.MenuButton(c, perfValue(*selected), func(mm *ui.Menu) {
			if len(choices) == 0 {
				mm.Item(empty).Disabled(true)
			}
			for _, choice := range choices {
				if mm.Item(perfValue(choice)).Chosen() {
					*selected = choice
				}
			}
		})
	})
}

func perfLabeledInput(c *ui.Context, p uikit.Palette, label string, value *string, width float32) {
	ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(p.Muted)
		ui.TextInput(c, value).Width(width).Font("monospace")
	})
}

func (a *App) exportPerformance(format diagnostics.ExportFormat) {
	s := a.performanceState(false)
	if s == nil || s.running {
		return
	}
	root := s.snapshotRoot
	if root == "" && a.ps != nil {
		root = a.ps.root
	}
	if err := diagnostics.ExportReportFile(s.exportPath, root, format, s.report, nil); err != nil {
		s.exportNotice = "export_error: " + err.Error()
	} else {
		s.exportNotice = a.perfMsg().Exported + s.exportPath
	}
}

func (a *App) choosePerformanceReportPath(format diagnostics.ExportFormat) {
	s := a.performanceState(false)
	if s == nil || a.win == nil {
		return
	}
	nw, ok := a.win.(nativeWindower)
	if !ok || nw.nativeWindow() == nil {
		s.exportNotice = "Native file dialog unavailable; enter an absolute local path outside the project directory."
		return
	}
	defaultName := "performance-report.json"
	if format == diagnostics.ExportCSV {
		defaultName = "performance-report.csv"
	}
	parent := nw.nativeWindow()
	win := a.win
	key := weak.Make(a)
	generation := s.generation
	go func() {
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      parent,
			Title:       "Export Performance Diagnostics Report",
			DefaultPath: defaultName,
		})
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			st := owner.performanceState(false)
			if st == nil || st.generation != generation {
				return
			}
			if err != nil {
				st.exportNotice = "export_error: file dialog failed"
			} else if path != "" {
				st.exportPath = path
			}
		})
	}()
}
