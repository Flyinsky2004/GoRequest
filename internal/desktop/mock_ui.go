package desktop

import (
	"fmt"
	"sort"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/mockserver"
)

// BuildMockProjectMenuItems adds the "Local Mock Server..." entry to the
// Project menu.
func (a *App) BuildMockProjectMenuItems(mm *ui.Menu) {
	if mm == nil {
		return
	}
	if mm.Item(mockT(a, "menu.project.mock_server")).Chosen() {
		a.OpenMockServer()
	}
}

// MockModals renders the Local Mock Server modal dialog when open.
func (a *App) MockModals(c *ui.Context, t *ui.Theme) {
	s := a.mockState(false)
	if s == nil {
		return
	}
	a.ensureMockProjectBound(s)
	if !s.open {
		if s.suiteRunning {
			a.CancelControlledMockSuite()
		}
		return
	}
	if !s.visible {
		// ui.Modal writes its bool false on Escape / outside click. With an
		// unsaved rule draft that dismissal becomes an explicit discard
		// confirmation instead of silently dropping the edits.
		if s.editor != nil && s.editor.dirty() {
			s.visible = true
			s.editor.confirmDiscard = true
		} else {
			a.dismissMockPanel(s)
			return
		}
	}
	s.visible = true

	p := uikit.PaletteFor(t)
	winW, winH := c.Size()
	panelW := max(float32(360), min(float32(920), float32(winW)-48))
	panelH := max(float32(280), min(float32(760), float32(winH)-64))

	ui.Modal(c, &s.visible, func() {
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(18).Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			// Header row
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.modal.title")).FontSize(16).FontWeight(700).TextColor(p.Text)
				a.renderMockStatusBadge(c, t, p, s)
				ui.Box(c).Grow(1)

				reloadBtn := ui.Button(c, mockT(a, "mock.action.reload")).Disabled(s.suiteRunning)
				uikit.ActionCursor(reloadBtn)
				if reloadBtn.Clicked() {
					a.ReloadMockRules()
				}

				if s.running {
					stopBtn := ui.Button(c, mockT(a, "mock.action.stop"))
					uikit.ActionCursor(stopBtn)
					if stopBtn.Clicked() {
						a.StopMockServer()
					}
				} else {
					canStart := len(s.conflicts) == 0 && s.snapshot != nil && len(s.snapshot.Rules) > 0
					startBtn := ui.Button(c, mockT(a, "mock.action.start")).Disabled(!canStart)
					uikit.ActionCursor(startBtn)
					if startBtn.Clicked() {
						a.StartMockServer()
					}
				}

				closeBtn := ui.Button(c, mockT(a, "mock.action.close"))
				uikit.ActionCursor(closeBtn)
				if closeBtn.Clicked() {
					a.requestMockPanelClose(s)
				}
			})

			// Top configuration bar: Host, Port, AllowLAN, Scenario, Seed
			ui.Column(c).FillWidth().Gap(8).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, mockT(a, "mock.field.listen_host")).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &s.listenHost).Width(130).Disabled(s.running)

					ui.Text(c, mockT(a, "mock.field.listen_port")).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &s.listenPort).Width(72).Disabled(s.running)

					lanToggle := ui.Checkbox(c, &s.allowLAN, mockT(a, "mock.field.allow_lan")).Disabled(s.running)
					uikit.ActionCursor(lanToggle)

					ui.Box(c).Grow(1)

					ui.Text(c, mockT(a, "mock.field.scenario")).FontSize(12).TextColor(p.Muted)
					scenBtn := ui.MenuButton(c, s.scenario, func(mm *ui.Menu) {
						choices := s.availableScenarios
						if len(choices) == 0 {
							choices = []string{"default"}
						}
						for _, sc := range choices {
							choice := sc
							if mm.Item(choice).Chosen() {
								a.SetMockScenario(choice)
							}
						}
					})
					uikit.ActionCursor(scenBtn)

					ui.Text(c, mockT(a, "mock.field.seed")).FontSize(12).TextColor(p.Muted)
					ui.TextInput(c, &s.seedText).Width(68).Disabled(s.running || s.suiteRunning)
				})

				// Address & digest metadata line
				ui.Row(c).FillWidth().Gap(12).AlignItems(ui.Center).Children(func() {
					if s.running && s.baseURL != "" {
						ui.Text(c, mockTf(a, "mock.status.bound_url", s.baseURL)).Font("monospace").FontSize(12).FontWeight(600).TextColor(t.Success)
					} else {
						ui.Text(c, mockT(a, "mock.status.loopback_only")).FontSize(12).TextColor(p.Muted)
					}
					ruleCount := 0
					digestShort := "-"
					if s.snapshot != nil {
						ruleCount = len(s.snapshot.Rules)
						if len(s.snapshot.SnapshotDigest) >= 16 {
							digestShort = s.snapshot.SnapshotDigest[:16]
						} else if s.snapshot.SnapshotDigest != "" {
							digestShort = s.snapshot.SnapshotDigest
						}
					}
					ui.Text(c, mockTf(a, "mock.status.rule_count", ruleCount, len(s.files))).FontSize(12).TextColor(p.Text)
					ui.Text(c, mockTf(a, "mock.status.digest", digestShort)).Font("monospace").FontSize(11).TextColor(p.Muted)
				})

				if s.running && s.server != nil && s.snapshot != nil && s.server.CurrentSnapshot() != nil &&
					s.server.CurrentSnapshot().SnapshotDigest != s.snapshot.SnapshotDigest {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, mockT(a, "mock.status.pending_apply")).FontSize(11).FontWeight(700).TextColor(t.Warning)
						ui.Box(c).Grow(1)
						applyBtn := ui.Button(c, mockT(a, "mock.action.apply_snapshot"))
						uikit.ActionCursor(applyBtn)
						if applyBtn.Clicked() {
							a.ApplyMockSnapshotToServer()
						}
					})
				}
				if s.isLAN || (s.allowLAN && !isLoopbackHostText(s.listenHost)) {
					addrText := s.boundAddr
					if addrText == "" {
						addrText = strings.TrimSpace(s.listenHost) + ":" + strings.TrimSpace(s.listenPort)
					}
					ui.Text(c, mockTf(a, "mock.status.lan_warning", addrText)).FontSize(12).TextColor(t.Warning)
				}
			})

			// Visible error / notice banners
			if s.errMsg != "" {
				ui.Row(c).FillWidth().Padding(8, 10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(t.Danger).Children(func() {
					ui.Text(c, s.errMsg).FontSize(12).TextColor(t.Danger)
				})
			} else if s.notice != "" {
				ui.Row(c).FillWidth().Padding(6, 10).Background(p.Sidebar).Radius(6).Children(func() {
					ui.Text(c, s.notice).FontSize(12).TextColor(p.Text)
				})
			}

			// Segmented view switcher
			uikit.SimpleSegmented(c, p, &s.tab, []string{
				mockT(a, "mock.tab.rules"),
				mockT(a, "mock.tab.access_log"),
				mockT(a, "mock.tab.suite_control"),
			})

			// Tab body
			ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
				switch s.tab {
				case 0:
					a.renderMockRulesTab(c, t, p, s)
				case 1:
					a.renderMockAccessLogTab(c, t, p, s)
				case 2:
					a.renderMockSuiteControlTab(c, t, p, s)
				}
			})
		})
	})
}

func (a *App) renderMockStatusBadge(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState) {
	label := mockT(a, "mock.status.stopped")
	color := p.Muted
	switch {
	case len(s.conflicts) > 0:
		label = mockT(a, "mock.status.conflict")
		color = t.Danger
	case s.errMsg != "" && !s.running:
		label = mockT(a, "mock.status.error")
		color = t.Danger
	case s.running:
		label = mockT(a, "mock.status.running")
		color = t.Success
	}
	ui.Row(c).Padding(2, 8).Radius(4).BorderWidth(1).BorderColor(color).Children(func() {
		ui.Text(c, label).FontSize(11).FontWeight(700).TextColor(color)
	})
}

func (a *App) renderMockRulesTab(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState) {
	if s.editor != nil {
		if !s.editor.open {
			s.editor = nil
		} else {
			a.renderMockRuleEditor(c, t, p, s, s.editor)
			return
		}
	}

	hasActiveReq := a.ps != nil && a.ps.activeTab() != nil && a.ps.activeTab().Kind == "request" && a.ps.activeTab().Entry != nil
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			fromReqBtn := ui.Button(c, mockT(a, "mock.action.from_current_req")).Disabled(!hasActiveReq)
			uikit.ActionCursor(fromReqBtn)
			if fromReqBtn.Clicked() {
				a.CreateMockRuleFromActiveRequest()
			}

			fromSchemaBtn := ui.Button(c, mockT(a, "mock.action.from_openapi_schema")).Disabled(!hasActiveReq)
			uikit.ActionCursor(fromSchemaBtn)
			if fromSchemaBtn.Clicked() {
				a.CreateMockRuleFromActiveOpenAPI()
			}

			ui.Box(c).Grow(1)
		})
		ui.Text(c, mockT(a, "mock.rules.manage_hint")).FontSize(11).TextColor(p.Muted)

		if len(s.conflicts) > 0 {
			ui.Column(c).FillWidth().Gap(4).Padding(10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(t.Danger).Children(func() {
				ui.Text(c, mockT(a, "mock.rules.conflicts_title")).FontSize(12).FontWeight(700).TextColor(t.Danger)
				for _, cf := range s.conflicts {
					ui.Text(c, "• "+cf.String()).Font("monospace").FontSize(11).TextColor(t.Danger)
				}
			})
		}

		if len(s.files) == 0 {
			ui.Text(c, mockT(a, "mock.rules.empty")).FontSize(12).TextColor(p.Muted)
			return
		}

		for _, f := range s.files {
			a.renderMockRuleFileSection(c, t, p, s, f)
		}
	})
}

// renderMockRuleFileSection renders one mock file's editable rule section
// with its own "add rule" affordance, so file selection for new rules is
// explicit.
func (a *App) renderMockRuleFileSection(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState, f *mockserver.MockFile) {
	if f == nil {
		return
	}
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, f.SourceRelPath).Font("monospace").FontSize(12).FontWeight(700).TextColor(p.Text)
			ui.Text(c, mockTf(a, "mock.rules.file_rule_count", len(f.Rules))).FontSize(11).TextColor(p.Muted)
			if strings.TrimSpace(f.DefaultScenario) != "" {
				ui.Text(c, mockTf(a, "mock.rules.source", f.ID)).FontSize(11).TextColor(p.Faint)
			}
			ui.Box(c).Grow(1)
			addBtn := ui.Button(c, mockTf(a, "mock.rules.add_in_file", f.ID))
			uikit.ActionCursor(addBtn)
			if addBtn.Clicked() {
				a.NewMockRuleDraft(f.SourceRelPath)
			}
		})
		for i := range f.Rules {
			a.renderMockRuleRow(c, t, p, s, f.SourceRelPath, &f.Rules[i])
		}
	})
}

// renderMockRuleRow renders one rule row with Edit/Copy and the two-step
// inline delete confirmation.
func (a *App) renderMockRuleRow(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState, fileRel string, rule *mockserver.MockRule) {
	if rule == nil {
		return
	}
	key := fileRel + "#" + rule.ID
	if s.deletingRuleKey == key {
		ui.Row(c).Key(key).FillWidth().Gap(8).AlignItems(ui.Center).Padding(8, 10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(t.Danger).Children(func() {
			ui.Text(c, mockT(a, "mock.action.confirm_delete")+" ["+rule.ID+"]").FontSize(12).FontWeight(700).TextColor(t.Danger)
			ui.Box(c).Grow(1)
			yesBtn := ui.Button(c, mockT(a, "mock.action.delete"))
			uikit.ActionCursor(yesBtn)
			if yesBtn.Clicked() {
				a.DeleteMockRule(fileRel, rule.ID)
			}
			noBtn := ui.Button(c, mockT(a, "mock.action.cancel_delete"))
			uikit.ActionCursor(noBtn)
			if noBtn.Clicked() {
				s.deletingRuleKey = ""
			}
		})
		return
	}

	variantNames := make([]string, 0, len(rule.Variants))
	for sc := range rule.Variants {
		variantNames = append(variantNames, sc)
	}
	sort.Strings(variantNames)

	ui.Column(c).Key(key).FillWidth().Gap(4).Padding(10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mockTf(a, "mock.rules.priority_badge", rule.Priority)).Font("monospace").FontSize(11).FontWeight(700).TextColor(p.Muted)
			if rule.IsEnabled() {
				ui.Text(c, mockT(a, "mock.rules.rule_enabled")).FontSize(10).TextColor(p.Faint)
			} else {
				ui.Text(c, mockT(a, "mock.rules.rule_disabled")).FontSize(10).TextColor(t.Warning)
			}
			ui.Text(c, rule.Match.Method+" "+rule.Match.Path).Font("monospace").FontSize(13).FontWeight(700).TextColor(p.Text)
			ui.Text(c, fmt.Sprintf("→ HTTP %d", rule.Response.Status)).Font("monospace").FontSize(12).TextColor(t.Success)
			if !mockRuleApplies(*rule, s.scenario) {
				ui.Text(c, mockTf(a, "mock.rules.inactive_scenario", s.scenario)).FontSize(10).TextColor(p.Faint)
			}
			ui.Box(c).Grow(1)
			if len(variantNames) > 0 {
				ui.Text(c, mockTf(a, "mock.rules.variants_count", len(variantNames))+" ("+strings.Join(variantNames, ", ")+")").FontSize(10).TextColor(p.Faint)
			}
			ui.Text(c, rule.ID).Font("monospace").FontSize(11).TextColor(p.Muted)
		})
		if rule.Name != "" {
			ui.Text(c, rule.Name).FontSize(11).TextColor(p.Muted)
		}
		ui.Row(c).FillWidth().Gap(6).Justify(ui.End).Children(func() {
			editBtn := ui.Button(c, mockT(a, "mock.action.edit"))
			uikit.ActionCursor(editBtn)
			if editBtn.Clicked() {
				a.OpenMockRuleEditor(fileRel, rule.ID)
			}
			copyBtn := ui.Button(c, mockT(a, "mock.action.copy"))
			uikit.ActionCursor(copyBtn)
			if copyBtn.Clicked() {
				a.DuplicateMockRule(fileRel, rule.ID)
			}
			delBtn := ui.Button(c, mockT(a, "mock.action.delete"))
			uikit.ActionCursor(delBtn)
			if delBtn.Clicked() {
				s.deletingRuleKey = key
			}
		})
	})
}

// dismissMockPanel performs the final modal close: cancels an in-flight
// controlled suite run (phase 1 semantics), clears any rule draft or pending
// delete confirmation, and marks the modal closed. It runs only after unsaved
// drafts have been discarded explicitly.
func (a *App) dismissMockPanel(s *mockState) {
	if s == nil {
		return
	}
	if s.suiteRunning {
		a.CancelControlledMockSuite()
	}
	s.open = false
	s.visible = false
	s.editor = nil
	s.deletingRuleKey = ""
}

// requestMockPanelClose guards the panel's Close button: a dirty rule draft
// must be discarded explicitly first.
func (a *App) requestMockPanelClose(s *mockState) {
	if s == nil {
		return
	}
	if s.editor != nil && s.editor.dirty() {
		s.editor.confirmDiscard = true
		return
	}
	a.dismissMockPanel(s)
}

func (a *App) renderMockAccessLogTab(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mockTf(a, "mock.status.log_count", len(s.accessLog), s.logCapacity)).FontSize(12).FontWeight(600).TextColor(p.Text)
			ui.Text(c, mockT(a, "mock.log.redaction_note")).FontSize(11).TextColor(p.Muted)
			ui.Box(c).Grow(1)
			clearBtn := ui.Button(c, mockT(a, "mock.action.clear_log")).Disabled(len(s.accessLog) == 0)
			uikit.ActionCursor(clearBtn)
			if clearBtn.Clicked() {
				a.ClearMockAccessLog()
			}
		})

		if len(s.accessLog) == 0 {
			ui.Text(c, mockT(a, "mock.log.empty")).FontSize(12).TextColor(p.Muted)
			return
		}

		// Render newest first
		for i := len(s.accessLog) - 1; i >= 0; i-- {
			rec := s.accessLog[i]
			pathAndQuery := rec.Path
			if rec.SanitizedQuery != "" {
				pathAndQuery += "?" + rec.SanitizedQuery
			}
			ui.Column(c).FillWidth().Gap(4).Padding(10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprintf("#%d", rec.Seq)).Font("monospace").FontSize(11).TextColor(p.Muted)
					ui.Text(c, rec.Method+" "+pathAndQuery).Font("monospace").FontSize(12).FontWeight(700).TextColor(p.Text)
					statusColor := t.Success
					if rec.Status >= 400 {
						statusColor = t.Warning
					}
					ui.Text(c, fmt.Sprintf("HTTP %d (%d ms)", rec.Status, rec.DurationMs)).Font("monospace").FontSize(11).TextColor(statusColor)
					ui.Box(c).Grow(1)
					if rec.Matched {
						ui.Text(c, mockTf(a, "mock.log.matched_rule", rec.MatchedRuleID, rec.Scenario)).FontSize(11).TextColor(p.Muted)
					} else {
						ui.Text(c, mockT(a, "mock.log.unmatched")).FontSize(11).TextColor(t.Warning)
					}
				})
				if rec.RequestBodyPreview != "" {
					ui.Text(c, "Req: "+rec.RequestBodyPreview).Font("monospace").FontSize(11).TextColor(p.Muted).SingleLine()
				}
				if rec.Diagnostic != "" {
					ui.Text(c, "404: "+rec.Diagnostic).Font("monospace").FontSize(11).TextColor(t.Warning).SingleLine()
				}
			})
		}
	})
}

func (a *App) renderMockSuiteControlTab(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState) {
	ui.Column(c).FillWidth().Gap(10).Children(func() {
		ui.Text(c, mockTf(a, "mock.suite.isolation_note", s.targetVar)).FontSize(12).TextColor(p.Muted)

		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mockT(a, "mock.suite.select_label")).FontSize(12).TextColor(p.Text)
			suiteBtn := ui.MenuButton(c, runnerValue(s.selectedSuite), func(mm *ui.Menu) {
				if len(s.suites) == 0 {
					mm.Item(mockT(a, "mock.suite.empty")).Disabled(true)
					return
				}
				for _, choice := range s.suites {
					cName := choice
					if mm.Item(cName).Chosen() {
						s.selectedSuite = cName
					}
				}
			}).Disabled(s.suiteRunning)
			uikit.ActionCursor(suiteBtn)

			ui.Text(c, mockT(a, "mock.field.target_var")).FontSize(12).TextColor(p.Muted)
			ui.TextInput(c, &s.targetVar).Width(110).Disabled(s.suiteRunning)

			ui.Box(c).Grow(1)

			canRunSuite := !s.suiteRunning && len(s.suites) > 0 && len(s.conflicts) == 0 && s.snapshot != nil && len(s.snapshot.Rules) > 0
			runBtn := ui.Button(c, mockT(a, "mock.action.run_controlled_suite")).Disabled(!canRunSuite)
			uikit.ActionCursor(runBtn)
			if runBtn.Clicked() {
				a.StartControlledMockSuite()
			}

			stopSuiteBtn := ui.Button(c, mockT(a, "mock.action.stop_controlled_suite")).Disabled(!s.suiteRunning)
			uikit.ActionCursor(stopSuiteBtn)
			if stopSuiteBtn.Clicked() {
				a.CancelControlledMockSuite()
			}
		})

		if s.suiteOutcome != nil && s.suiteOutcome.Result != nil {
			res := s.suiteOutcome.Result
			ui.Column(c).FillWidth().Gap(4).Padding(10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.Text(c, runnerSummaryText(res)).FontSize(12).FontWeight(600).TextColor(p.Text)
				ui.Text(c, fmt.Sprintf("Mock Endpoint: %s · Scenario: %s · Snapshot: %s · Stopped: %v",
					s.suiteOutcome.MockBaseURL, s.suiteOutcome.MockScenario, s.suiteOutcome.MockSnapshot, s.suiteOutcome.ServerStopped)).
					Font("monospace").FontSize(11).TextColor(p.Muted)
				if res.Failure != nil {
					ui.Text(c, string(res.Failure.Category)+" / "+string(res.Failure.Kind)+": "+res.Failure.Message).FontSize(12).TextColor(t.Danger)
				}
			})
		}

		if len(s.suiteEvents) > 0 {
			ui.Column(c).FillWidth().Gap(4).Padding(8).Background(p.Sidebar).Radius(6).Children(func() {
				for _, e := range s.suiteEvents {
					ui.Text(c, fmt.Sprintf("%d · row %d · %s · attempt %d · %s · %s · HTTP %d %s",
						e.Seq, e.Row, e.StepID, e.Attempt, e.Kind, e.Status, e.HTTPStatus, e.Message)).
						Font("monospace").FontSize(11).TextColor(p.Text)
				}
			})
		}
	})
}

func isLoopbackHostText(h string) bool {
	trimmed := strings.ToLower(strings.TrimSpace(h))
	return trimmed == "" || trimmed == "127.0.0.1" || trimmed == "::1" || trimmed == "localhost"
}
