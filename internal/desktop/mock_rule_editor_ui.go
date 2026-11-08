package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/mockserver"
)

// mock_rule_editor_ui.go renders the C01 phase 2 structured rule & scenario
// editor inside the Local Mock Server modal's Rules tab. Design follows the
// repo's MyGo Native style: uikit palette tokens only, explicit visible
// labels with helper text, errors rendered next to their field, monospace
// for technical values, one primary action (Save), and explicit
// Cancel/Discard confirmation for unsaved drafts.

// mockRuleApplies mirrors the engine's scenario applicability check for the
// edit list's display hint only; compilation stays authoritative.
func mockRuleApplies(r mockserver.MockRule, scenario string) bool {
	if len(r.Scenarios) == 0 {
		return true
	}
	for _, sc := range r.Scenarios {
		if strings.EqualFold(strings.TrimSpace(sc), scenario) || strings.TrimSpace(sc) == "*" {
			return true
		}
	}
	if _, ok := r.Variants[scenario]; ok {
		return true
	}
	return false
}

// renderMockSectionCard wraps one labeled section of the editor form.
func (a *App) renderMockSectionCard(c *ui.Context, t *ui.Theme, p uikit.Palette, title, hint string, body func()) {
	ui.Column(c).FillWidth().Gap(8).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(700).TextColor(p.Text)
		if hint != "" {
			ui.Text(c, hint).FontSize(11).TextColor(p.Muted)
		}
		body()
	})
}

func renderMockEditorFieldError(c *ui.Context, t *ui.Theme, msg string) {
	if msg == "" {
		return
	}
	ui.Text(c, msg).FontSize(11).TextColor(t.Danger)
}

// mockCondOpLabel renders an unset condition as a visible dash.
func mockCondOpLabel(op string) string {
	if strings.TrimSpace(op) == "" {
		return "–"
	}
	return op
}

func mockMatcherRemoveBtn(c *ui.Context, a *App, clicked *bool) {
	if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.remove"))).Clicked() {
		*clicked = true
	}
}

// renderMockRuleEditor renders the whole editor form (or its confirm/discard states). It is only called from the Rules tab while a draft is open.
func (a *App) renderMockRuleEditor(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState, ed *mockRuleEditor) {
	// Keep preview + field validation current; the refresh early-returns when
	// nothing changed and never touches the listener.
	a.RefreshMockRulePreview(false)

	seed := s.seedInt()
	title := mockT(a, "mock.edit.title.new")
	if ed.mode == "edit" {
		title = mockTf(a, "mock.edit.title.edit", ed.ruleDraft.ID)
	}

	ui.Column(c).FillWidth().Gap(10).Children(func() {
		// Header: title, dirty badge, target file.
		ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, title).FontSize(15).FontWeight(700).TextColor(p.Text)
			if ed.dirty() {
				ui.Text(c, mockT(a, "mock.edit.dirty.badge")).FontSize(11).TextColor(t.Warning)
			}
			ui.Box(c).Grow(1)
			ui.Text(c, mockTf(a, "mock.edit.file_caption", ed.fileRelPath)).Font("monospace").FontSize(11).TextColor(p.Muted)
		})

		if ed.confirmDiscard {
			a.renderMockEditorConfirmDiscard(c, t, p, s)
			return
		}
		if ed.saveConflict {
			a.renderMockEditorSaveConflict(c, t, p, s)
		}
		if ed.editErr != "" {
			ui.Row(c).FillWidth().Padding(8, 10).Background(p.Sidebar).Radius(6).BorderWidth(1).BorderColor(t.Danger).Children(func() {
				ui.Text(c, ed.editErr).FontSize(12).TextColor(t.Danger)
			})
		}

		// --- Identity ------------------------------------------------------
		a.renderMockSectionCard(c, t, p, mockT(a, "mock.edit.section.identity"), "", func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.id")).FontSize(11).TextColor(p.Muted).Width(72)
				ui.TextInput(c, &ed.idText).Width(180).Font("monospace")
				renderMockEditorFieldError(c, t, ed.errs[mockFieldID])
				ui.Box(c).Grow(1)
				ui.Text(c, mockT(a, "mock.edit.field.priority")).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &ed.priorityText).Width(64)
				renderMockEditorFieldError(c, t, ed.errs[mockFieldPriority])
				enabledToggle := ui.Checkbox(c, &ed.enabled, mockT(a, "mock.edit.field.enabled"))
				uikit.ActionCursor(enabledToggle)
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.name")).FontSize(11).TextColor(p.Muted).Width(72)
				ui.TextInput(c, &ed.nameText).Grow(1)
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.desc")).FontSize(11).TextColor(p.Muted).Width(72)
				ui.TextInput(c, &ed.descText).Grow(1)
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.scenarios")).FontSize(11).TextColor(p.Muted).Width(72)
				ui.TextInput(c, &ed.scenariosTxt).Grow(1)
				renderMockEditorFieldError(c, t, ed.errs[mockFieldScenarios])
			})
			ui.Text(c, mockT(a, "mock.edit.hint.scenarios")).FontSize(11).TextColor(p.Muted)
		})

		// --- Match ----------------------------------------------------------
		a.renderMockSectionCard(c, t, p, mockT(a, "mock.edit.section.match"), mockT(a, "mock.edit.hint.kvop"), func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.method")).FontSize(11).TextColor(p.Muted).Width(72)
				ui.TextInput(c, &ed.methodText).Width(96).Font("monospace")
				uikit.MenuButton(c, mockT(a, "mock.edit.field.method"), func(mm *ui.Menu) {
					for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"} {
						choice := m
						if mm.Item(choice).Chosen() {
							ed.methodText = choice
						}
					}
				})
				renderMockEditorFieldError(c, t, ed.errs[mockFieldMethod])
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.path")).FontSize(11).TextColor(p.Muted).Width(72)
				ui.TextInput(c, &ed.pathText).Grow(1).Font("monospace")
				renderMockEditorFieldError(c, t, ed.errs[mockFieldPath])
			})
			ui.Text(c, mockT(a, "mock.edit.hint.path")).FontSize(11).TextColor(p.Muted)

			a.renderMockMatcherGroup(c, t, p, mockT(a, "mock.edit.section.pathparams"), false, &ed.pathParamRows)
			a.renderMockMatcherGroup(c, t, p, mockT(a, "mock.edit.section.query"), true, &ed.queryRows)
			a.renderMockMatcherGroup(c, t, p, mockT(a, "mock.edit.section.headers"), true, &ed.headerRows)

			// Request body condition.
			ui.Column(c).FillWidth().Gap(6).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.section.body")).FontSize(12).FontWeight(600).TextColor(p.Text)
				if ed.lockedBody != nil {
					ui.Text(c, mockT(a, "mock.edit.body.locked")).FontSize(11).TextColor(t.Warning)
					ui.Text(c, mockT(a, "mock.edit.locked.badge")).FontSize(11).TextColor(p.Muted)
					return
				}
				ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
					uikit.MenuButton(c, mockCondOpLabel(ed.bodyOp), func(mm *ui.Menu) {
						if mm.Item("–").Chosen() {
							ed.bodyOp = mockBodyOpNone
							ed.bodyValueText = ""
						}
						for _, op := range mockBodyOps {
							choice := op
							if mm.Item(choice).Chosen() {
								ed.bodyOp = choice
							}
						}
					})
					bodyValIn := ui.TextInput(c, &ed.bodyValueText).Grow(1)
					_ = bodyValIn
				})
				renderMockEditorFieldError(c, t, ed.errs[mockFieldBodyMatch])
			})
		})

		// --- Response --------------------------------------------------------
		a.renderMockSectionCard(c, t, p, mockT(a, "mock.edit.section.response"), "", func() {
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.status")).FontSize(11).TextColor(p.Muted).Width(96)
				ui.TextInput(c, &ed.statusText).Width(64)
				renderMockEditorFieldError(c, t, ed.errs[mockFieldStatus])
				ui.Box(c).Grow(1)
				ui.Text(c, mockT(a, "mock.edit.field.content_type")).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &ed.contentTypeTxt).Width(180).Font("monospace")
			})

			// Response headers.
			removeKV := -1
			ui.Text(c, mockT(a, "mock.edit.section.respheaders")).FontSize(12).FontWeight(600).TextColor(p.Text)
			for i, row := range ed.respHeaderRows {
				ui.Row(c).Key(row.ID).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
					caseToggle := ui.Checkbox(c, &row.Enabled, "")
					uikit.ActionCursor(caseToggle)
					ui.TextInput(c, &row.Name).Width(170).Font("monospace")
					ui.TextInput(c, &row.Value).Grow(1).Font("monospace")
					clicked := false
					mockMatcherRemoveBtn(c, a, &clicked)
					if clicked {
						removeKV = i
					}
					if row.Err != "" {
						ui.Text(c, row.Err).FontSize(11).TextColor(t.Danger)
					}
				})
			}
			if removeKV >= 0 && removeKV < len(ed.respHeaderRows) {
				ed.respHeaderRows = append(ed.respHeaderRows[:removeKV], ed.respHeaderRows[removeKV+1:]...)
			}
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.add_header"))).Clicked() {
				ed.respHeaderRows = append(ed.respHeaderRows, newMockKVRow())
			}

			// Response body.
			ui.Text(c, mockT(a, "mock.edit.field.body")).FontSize(12).FontWeight(600).TextColor(p.Text)
			ui.Box(c).Height(150).FillWidth().Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.TextArea(c, &ed.bodyText).Grow(1).FillWidth().Font("monospace")
			})
			ui.Text(c, mockT(a, "mock.edit.hint.bodytpl")).FontSize(11).TextColor(p.Muted)

			// Delays.
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				ui.Text(c, mockT(a, "mock.edit.field.delayms")).FontSize(11).TextColor(p.Muted).Width(96)
				ui.TextInput(c, &ed.delayMsText).Width(64)
				renderMockEditorFieldError(c, t, ed.errs[mockFieldDelay])
			})
			ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
				rangeToggle := ui.Checkbox(c, &ed.delayRangeOn, mockT(a, "mock.edit.field.delayrange"))
				uikit.ActionCursor(rangeToggle)
				ui.TextInput(c, &ed.delayMinText).Width(56)
				ui.Text(c, "–").FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &ed.delayMaxText).Width(56)
			})
		})

		// --- Variants ---------------------------------------------------------
		a.renderMockSectionCard(c, t, p, mockT(a, "mock.edit.section.variants"), mockT(a, "mock.edit.variant.hint"), func() {
			removeVariant := -1
			for i, row := range ed.variantRows {
				a.renderMockVariantRow(c, t, p, row, i, &removeVariant)
			}
			if removeVariant >= 0 && removeVariant < len(ed.variantRows) {
				ed.variantRows = append(ed.variantRows[:removeVariant], ed.variantRows[removeVariant+1:]...)
			}
			candidates := a.mockVariantAddCandidates(s, ed)
			if len(candidates) == 0 {
				ui.Text(c, mockT(a, "mock.edit.variant.no_more")).FontSize(11).TextColor(p.Muted)
				return
			}
			uikit.MenuButton(c, mockT(a, "mock.edit.variant.add"), func(mm *ui.Menu) {
				for _, sc := range candidates {
					choice := sc
					if mm.Item(choice).Chosen() {
						ed.variantRows = append(ed.variantRows, newMockVariantRow(choice))
					}
				}
			})
		})

		// --- Validation & deterministic conflict preview ----------------------
		a.renderMockEditorPreview(c, t, p, s, ed, seed)

		// --- Footer -------------------------------------------------------------
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.action.cancel"))).Clicked() {
				if ed.dirty() {
					ed.confirmDiscard = true
				} else {
					a.CloseMockRuleEditor()
				}
			}
			saveBtn := ui.PrimaryButton(c, mockT(a, "mock.edit.action.save")).Disabled(ed.hasErrors())
			uikit.ActionCursor(saveBtn)
			if saveBtn.Clicked() {
				a.SaveMockRuleDraft()
			}
		})
	})
}

// renderMockEditorConfirmDiscard renders the explicit unsaved-draft gate for
// panel close, cancel, and modal-dismissal actions.
func (a *App) renderMockEditorConfirmDiscard(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState) {
	ui.Column(c).FillWidth().Gap(8).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(t.Warning).Children(func() {
		ui.Text(c, mockT(a, "mock.edit.confirm_discard")).FontSize(12).FontWeight(700).TextColor(t.Warning)
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.action.keep_editing"))).Clicked() {
				if s.editor != nil {
					s.editor.confirmDiscard = false
					s.editor.saveConflict = false
				}
			}
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.action.discard"))).Clicked() {
				if s.editor != nil && s.editor.saveConflict {
					// From the external-conflict banner this discards the
					// draft and reloads from disk; from cancel it simply drops
					// unsaved edits.
					a.ResolveMockEditorExternalConflict(true)
				} else {
					a.CloseMockRuleEditor()
				}
			}
		})
	})
}

// renderMockEditorSaveConflict explains an external-change save refusal and
// offers the two explicit ways out.
func (a *App) renderMockEditorSaveConflict(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState) {
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(t.Danger).Children(func() {
		ui.Text(c, mockT(a, "mock.edit.saveconflict.title")).FontSize(12).FontWeight(700).TextColor(t.Danger)
		ui.Text(c, mockT(a, "mock.edit.saveconflict.hint")).FontSize(11).TextColor(p.Muted)
		ui.Row(c).Gap(8).Children(func() {
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.saveconflict.reload"))).Clicked() {
				if s.editor != nil && s.editor.dirty() {
					// Dropping the draft for the reloaded file is a second
					// explicit step, not an accident.
					s.editor.confirmDiscard = true
				} else {
					a.ResolveMockEditorExternalConflict(true)
				}
			}
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.saveconflict.keep"))).Clicked() {
				a.ResolveMockEditorExternalConflict(false)
			}
		})
	})
}

// renderMockMatcherGroup renders one editable name/condition/value list
// (path params, query, or headers).
func (a *App) renderMockMatcherGroup(c *ui.Context, t *ui.Theme, p uikit.Palette, label string, allowIgnoreCase bool, rows *[]*mockMatcherRow) {
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(p.Text)
			ui.Box(c).Grow(1)
			if uikit.ActionCursor(ui.Button(c, mockT(a, "mock.edit.add_matcher"))).Clicked() {
				*rows = append(*rows, newMockMatcherRow())
			}
		})
		if len(*rows) == 0 {
			ui.Text(c, "–").FontSize(11).TextColor(p.Faint)
			return
		}
		removeIdx := -1
		for i, row := range *rows {
			clicked := false
			a.renderMockMatcherRow(c, t, p, row, allowIgnoreCase, &clicked)
			if clicked {
				removeIdx = i
			}
		}
		if removeIdx >= 0 && removeIdx < len(*rows) {
			*rows = append((*rows)[:removeIdx], (*rows)[removeIdx+1:]...)
		}
	})
}

func (a *App) renderMockMatcherRow(c *ui.Context, t *ui.Theme, p uikit.Palette, row *mockMatcherRow, allowIgnoreCase bool, clicked *bool) {
	ui.Row(c).Key(row.ID).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
		if row.Locked {
			// Predicate combination the structured editor cannot author:
			// show it read-only and keep it byte-identical on save.
			ui.Text(c, row.Name).Width(150).Font("monospace").FontSize(12).TextColor(p.Muted)
			ui.Text(c, mockT(a, "mock.edit.locked.badge")).FontSize(11).TextColor(t.Warning)
			mockMatcherRemoveBtn(c, a, clicked)
			return
		}
		ui.TextInput(c, &row.Name).Width(150).Font("monospace")
		uikit.MenuButton(c, mockCondOpLabel(row.Op), func(mm *ui.Menu) {
			for _, op := range mockMatcherOps {
				choice := op
				if mm.Item(choice).Chosen() {
					row.Op = choice
					if choice == "exists" && strings.TrimSpace(row.Value) == "" {
						row.Value = "true"
					}
				}
			}
		})
		ui.TextInput(c, &row.Value).Grow(1)
		if allowIgnoreCase {
			caseToggle := ui.Checkbox(c, &row.IgnoreCase, mockT(a, "mock.edit.ignore_case"))
			uikit.ActionCursor(caseToggle)
		}
		mockMatcherRemoveBtn(c, a, clicked)
	})
	if row.Err != "" {
		ui.Text(c, row.Err).FontSize(11).TextColor(t.Danger)
	}
}

// renderMockVariantRow edits one scenario variant.
func (a *App) renderMockVariantRow(c *ui.Context, t *ui.Theme, p uikit.Palette, row *mockVariantRow, idx int, removeIdx *int) {
	ui.Column(c).Key(row.ID).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c, &row.Scenario).Width(120).Font("monospace")
			ui.Text(c, mockT(a, "mock.edit.field.status")).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &row.statusText).Width(52)
			ui.Text(c, mockT(a, "mock.edit.field.content_type")).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &row.ctype).Width(150).Font("monospace")
			ui.Text(c, mockT(a, "mock.edit.field.delayms")).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &row.delayText).Width(52)
			ui.Box(c).Grow(1)
			clicked := false
			mockMatcherRemoveBtn(c, a, &clicked)
			if clicked {
				*removeIdx = idx
			}
		})
		ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, mockT(a, "mock.edit.field.body")).FontSize(11).TextColor(p.Muted)
			ui.Box(c).Height(84).Grow(1).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
				ui.TextArea(c, &row.body).Grow(1).FillWidth().Font("monospace")
			})
			if row.resp.Headers != nil || row.resp.DelayRangeMs != nil {
				ui.Text(c, mockT(a, "mock.edit.locked.badge")).FontSize(11).TextColor(p.Muted)
			}
		})
		if row.Err != "" {
			ui.Text(c, row.Err).FontSize(11).TextColor(t.Danger)
		}
	})
}

// mockVariantAddCandidates lists scenario names that may still gain a variant
// on this rule (builtin + panel-known scenarios minus existing rows).
func (a *App) mockVariantAddCandidates(s *mockState, ed *mockRuleEditor) []string {
	if ed == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, row := range ed.variantRows {
		seen[strings.TrimSpace(row.Scenario)] = true
	}
	var out []string
	added := map[string]bool{}
	for _, sc := range mockserver.BuiltinScenarios {
		if !seen[sc] && !added[sc] {
			out = append(out, sc)
			added[sc] = true
		}
	}
	if s != nil && s.snapshot != nil {
		for _, sc := range s.snapshot.AvailableScenarios {
			if !seen[sc] && !added[sc] {
				out = append(out, sc)
				added[sc] = true
			}
		}
	}
	return out
}

// renderMockEditorPreview shows field validation state and the deterministic
// in-memory compile preview: same-priority conflicts first, then compile
// errors, then the healthy snapshot summary and digest.
func (a *App) renderMockEditorPreview(c *ui.Context, t *ui.Theme, p uikit.Palette, s *mockState, ed *mockRuleEditor, seed int64) {
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Text(c, mockTf(a, "mock.edit.preview.caption", s.scenario, seed)).FontSize(11).FontWeight(700).TextColor(p.Muted)

		if ed.errorCount() > 0 {
			ui.Text(c, mockTf(a, "mock.edit.err.fix_fields_first", ed.errorCount())).FontSize(12).TextColor(t.Danger)
			for _, field := range []string{
				mockFieldID, mockFieldPriority, mockFieldScenarios, mockFieldMethod,
				mockFieldPath, mockFieldBodyMatch, mockFieldStatus, mockFieldDelay,
			} {
				if msg := ed.errs[field]; msg != "" {
					ui.Text(c, field+": "+msg).Font("monospace").FontSize(11).TextColor(t.Danger)
				}
			}
			return
		}

		switch {
		case ed.previewConflict:
			ui.Text(c, mockT(a, "mock.edit.preview.conflict")).FontSize(12).FontWeight(700).TextColor(t.Danger)
			for _, cf := range ed.previewConflicts {
				ui.Text(c, "• "+cf.String()).Font("monospace").FontSize(11).TextColor(t.Danger)
			}
			ui.Text(c, mockT(a, "mock.edit.preview.dirty_note")).FontSize(11).TextColor(p.Muted)
		case ed.previewErrMsg != "":
			ui.Text(c, mockT(a, "mock.edit.preview.invalid")).FontSize(12).FontWeight(700).TextColor(t.Danger)
			ui.Text(c, ed.previewErrMsg).FontSize(11).TextColor(t.Danger)
			if ed.previewErrDetail != "" {
				ui.Text(c, ed.previewErrDetail).Font("monospace").FontSize(11).TextColor(t.Danger)
			}
		case ed.previewOK:
			digest := ed.previewDigest
			if len(digest) >= 16 {
				digest = digest[:16]
			}
			ui.Text(c, mockTf(a, "mock.edit.preview.ok", ed.previewRuleCount, s.scenario, digest)).FontSize(12).TextColor(t.Success)
			ui.Text(c, mockT(a, "mock.edit.preview.dirty_note")).FontSize(11).TextColor(p.Muted)
		default:
			ui.Text(c, mockT(a, "mock.edit.preview.invalid")).FontSize(11).TextColor(p.Muted)
		}
	})
}
