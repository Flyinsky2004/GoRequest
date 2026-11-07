package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/runner"
)

// RunnerEditorModals renders the assertion/extraction editor. It is invoked
// from RunnerModals, which is already wired into App.View, so this phase needs
// no public-file change for its entry. Dismissing the modal (Escape, outside
// click, Close) discards every draft without writing disk.
func (a *App) RunnerEditorModals(c *ui.Context, t *ui.Theme) {
	s := a.runnerEditorState(false)
	if s == nil || !s.open {
		return
	}
	m := a.runnerEditorMsg()
	p := uikit.PaletteFor(t)
	winW, winH := c.Size()
	panelW := max(float32(340), min(float32(920), float32(winW)-48))
	panelH := max(float32(280), min(float32(760), float32(winH)-64))
	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(18).Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, m.Title).FontSize(16).FontWeight(700).TextColor(p.Text)
				ui.Box(c).Grow(1)
				if uikit.ActionCursor(ui.Button(c, m.Close)).Clicked() {
					a.CloseRunnerEditor()
				}
			})
			a.runnerEditorRequestPicker(c, m, p)
			ui.Text(c, m.RunnerConnectHint).FontSize(11).TextColor(p.Muted).SingleLine().Tooltip(m.RunnerConnectHint)
			ui.Text(c, m.SecretRefHint).FontSize(11).TextColor(p.Muted).SingleLine().Tooltip(m.SecretRefHint)
			if !s.loaded {
				ui.Text(c, m.NoRequest).FontSize(13).TextColor(t.Warning)
			} else {
				if s.dirtyTabs > 0 {
					ui.Text(c, m.DirtyTabsHint).FontSize(11).TextColor(t.Warning)
				}
				uikit.SimpleSegmented(c, p, &s.targetKind, []string{m.AssertionSection, m.ExtractionSection})
				if s.targetKind == 0 {
					a.runnerEditorAssertionSection(c, t, m, p)
				} else {
					a.runnerEditorExtractionSection(c, t, m, p)
				}
			}
			if s.err != "" {
				ui.Text(c, s.err).FontSize(12).TextColor(t.Danger).FillWidth().Tooltip(s.err)
			}
			if s.notice != "" {
				ui.Text(c, s.notice).FontSize(12).TextColor(p.Text).FillWidth()
			}
			ui.Box(c).Height(1).Background(t.Border)
			ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.Reload)).Clicked() {
					a.ReloadRunnerEditor()
				}
				ui.Box(c).Grow(1)
				if uikit.ActionCursor(ui.PrimaryButton(c, m.Save)).Clicked() {
					a.SaveRunnerEditor()
				}
			})
		})
	})
	// Escape / outside-click dismissals cancel too.
	if !s.open {
		a.CloseRunnerEditor()
	}
}

func runnerEditorFormBusy(s *runnerEditorState) bool {
	return s.editingRow != -1 || s.editSel != -1
}

func (a *App) runnerEditorRequestPicker(c *ui.Context, m runnerEditorMessages, p uikit.Palette) {
	s := a.runnerEditorState(false)
	if s == nil || a.ps == nil || a.ps.project == nil {
		return
	}
	pj := a.ps.project
	labels := make([]string, 0, len(pj.Order))
	byLabel := make(map[string]string, len(pj.Order))
	for _, id := range pj.Order {
		entry := pj.Requests[id]
		if entry == nil || entry.Request == nil {
			continue
		}
		label := entry.Request.Name + " (" + entry.Request.ID + ")  " + entry.RelPath
		labels = append(labels, label)
		byLabel[label] = id
	}
	current := m.Request + ": "
	if entry := pj.Requests[s.requestID]; entry != nil && entry.Request != nil {
		current += entry.Request.Name + " (" + entry.Request.ID + ")  " + entry.RelPath
	} else {
		current += "—"
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
		ui.MenuButton(c, current, func(mm *ui.Menu) {
			if len(labels) == 0 {
				mm.Item(m.NoRequest).Disabled(true)
			}
			for _, label := range labels {
				label := label
				if mm.Item(label).Chosen() {
					a.SetRunnerEditorRequest(byLabel[label])
				}
			}
		})
	})
}

func (a *App) runnerEditorAssertionSection(c *ui.Context, t *ui.Theme, m runnerEditorMessages, p uikit.Palette) {
	s := a.runnerEditorState(false)
	if s == nil {
		return
	}
	ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				ui.MenuButton(c, m.NewAssertion, func(mm *ui.Menu) {
					for _, kind := range []string{runner.TargetStatus, runner.TargetHeader, runner.TargetJSONPath, runner.TargetBody} {
						kind := kind
						if mm.Item(m.TargetLabel(kind)).Chosen() {
							a.BeginRunnerEditorRow(kind)
						}
					}
				})
			})
			for i := range s.rows {
				row := s.rows[i]
				i := i
				ui.Row(c).Key(fmt.Sprintf("runner-editor-assert-%d", i)).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
					summary := runnerEditorRowSummary(m, row)
					tooltip := runnerEditorRowTooltip(m, row)
					color := p.Text
					switch {
					case row.spec.Err != "":
						color = t.Danger
					case !row.spec.Structured:
						color = p.Muted
					}
					ui.Text(c, summary).FontSize(12).TextColor(color).Grow(1).Tooltip(tooltip)
					if row.spec.Structured {
						if uikit.ActionCursor(ui.Button(c, m.EditButton)).Disabled(runnerEditorFormBusy(s)).Clicked() {
							a.EditRunnerEditorRow(i)
						}
					}
					if uikit.ActionCursor(ui.Button(c, m.DeleteButton)).Clicked() {
						a.DeleteRunnerEditorRow(i)
					}
				})
				if row.spec.Err != "" {
					ui.Text(c, row.spec.Err).FontSize(11).TextColor(t.Danger).FillWidth().Tooltip(row.spec.Err)
				}
				if !row.spec.Structured && row.spec.Raw != "" {
					ui.Text(c, row.spec.Raw).Font("monospace").FontSize(10).TextColor(p.Muted).FillWidth().Tooltip(row.spec.Raw)
				}
			}
			if len(s.rows) == 0 {
				ui.Text(c, "("+m.AssertionSection+")").FontSize(11).TextColor(p.Faint)
			}
			if s.editingRow != -1 {
				a.runnerEditorAssertionForm(c, t, m, p)
			}
		})
	})
}

func (a *App) runnerEditorAssertionForm(c *ui.Context, t *ui.Theme, m runnerEditorMessages, p uikit.Palette) {
	s := a.runnerEditorState(false)
	if s == nil || s.editingRow < 0 {
		return
	}
	f := &s.form
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(6).Children(func() {
		ui.Text(c, m.TargetLabel(f.target)).FontSize(12).FontWeight(700).TextColor(p.Text)
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.MessageLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &f.message).Grow(1).Placeholder(m.MessageLabel).Changed()
		})
		switch f.target {
		case runner.TargetStatus:
			uikit.SimpleSegmented(c, p, &f.statusMode, []string{m.StatusEqualsLabel, m.StatusInLabel})
			if f.statusMode == 0 {
				ui.TextInput(c, &f.statusEqual).Width(160).Placeholder("200").Changed()
			} else {
				ui.TextInput(c, &f.statusIn).Grow(1).Placeholder("200, 204").Changed()
			}
		case runner.TargetHeader:
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.HeaderLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.header).Grow(1).Placeholder("X-Request-Id").Changed()
			})
		case runner.TargetJSONPath:
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.JSONPathLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.jsonPath).Grow(1).Placeholder("$.user.id").Changed()
			})
		}
		if f.target != runner.TargetStatus {
			ui.Text(c, m.MatchersLabel).FontSize(11).Bold().TextColor(p.Muted)
			for mi := range f.matchers {
				d := f.matchers[mi]
				mi := mi
				ui.Row(c).Key(fmt.Sprintf("runner-editor-matcher-%d", mi)).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Text(c, m.MatcherLabel(d.kind)).FontSize(11).TextColor(p.Text).Width(84)
					switch d.kind {
					case runner.MatcherExists:
						if uikit.ActionCursor(ui.Select(c, &d.exists, runnerEditorExistsOptions)).Width(120).Changed() {
						}
					case runner.MatcherEquals, runner.MatcherNotEquals:
						if uikit.ActionCursor(ui.Select(c, &d.litKind, runnerEditorLiteralKinds)).Width(110).Changed() {
						}
						ui.TextInput(c, &d.litText).Grow(1).Changed()
					case runner.MatcherIn:
						ui.TextInput(c, &d.inText).Grow(1).Changed()
						ui.Text(c, m.InHint).FontSize(10).TextColor(p.Faint).Grow(1)
					case runner.MatcherType:
						if uikit.ActionCursor(ui.Select(c, &d.typeSel, runnerEditorTypeOptions)).Width(120).Changed() {
						}
					default:
						ui.TextInput(c, &d.text).Grow(1).Changed()
					}
					if uikit.ActionCursor(ui.Button(c, "✕")).Clicked() {
						runnerEditorRemoveMatcherDraft(s, mi)
					}
				})
			}
			ui.MenuButton(c, m.AddMatcher, func(mm *ui.Menu) {
				kindOrder := []string{runner.MatcherExists, runner.MatcherEquals, runner.MatcherNotEquals, runner.MatcherIn, runner.MatcherContains, runner.MatcherNotContain, runner.MatcherRegex, runner.MatcherType}
				for _, kind := range kindOrder {
					kind := kind
					if mm.Item(m.MatcherLabel(kind)).Chosen() {
						runnerEditorAddMatcherDraft(s, kind)
					}
				}
			})
		}
		if s.formErr != "" {
			ui.Text(c, s.formErr).FontSize(11).TextColor(t.Danger).FillWidth().Tooltip(s.formErr)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, m.CancelEditButton)).Clicked() {
				a.CancelRunnerEditorRow()
			}
			if uikit.ActionCursor(ui.PrimaryButton(c, m.ApplyButton)).Clicked() {
				a.ApplyRunnerEditorRow()
			}
		})
	})
}

func (a *App) runnerEditorExtractionSection(c *ui.Context, t *ui.Theme, m runnerEditorMessages, p uikit.Palette) {
	s := a.runnerEditorState(false)
	if s == nil {
		return
	}
	if len(s.targets) == 0 {
		ui.Text(c, m.NoSuiteTarget).FontSize(12).TextColor(t.Warning).FillWidth()
		if s.suitesSkipped > 0 {
			ui.Text(c, fmt.Sprintf(m.SuitesSkipped, s.suitesSkipped)).FontSize(11).TextColor(p.Muted)
		}
		return
	}
	target := s.targets[s.targetSel]
	if target == nil {
		return
	}
	ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.SuiteTargetLabel+":").FontSize(12).TextColor(p.Muted)
		ui.MenuButton(c, target.label, func(mm *ui.Menu) {
			for i := range s.targets {
				i := i
				if mm.Item(s.targets[i].label).Chosen() {
					a.SelectRunnerEditorTarget(i)
				}
			}
		})
		if target.dirty {
			ui.Text(c, "●").FontSize(12).TextColor(t.Warning)
		}
	})
	if s.suitesSkipped > 0 {
		ui.Text(c, fmt.Sprintf(m.SuitesSkipped, s.suitesSkipped)).FontSize(11).TextColor(p.Muted)
	}
	ui.Scroll(c).Grow(1).FillWidth().MinHeight(0).Children(func() {
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				if uikit.ActionCursor(ui.Button(c, m.NewExtraction)).Disabled(runnerEditorFormBusy(s)).Clicked() {
					a.BeginRunnerEditorExtractNew()
				}
			})
			for i := range target.rows {
				i := i
				ui.Row(c).Key(fmt.Sprintf("runner-editor-extract-%d", i)).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
					ui.Text(c, runnerEditorExtractSummary(m, &target.rows[i])).FontSize(12).TextColor(p.Text).Grow(1).Tooltip(runnerEditorExtractSummary(m, &target.rows[i]))
					if uikit.ActionCursor(ui.Button(c, m.EditButton)).Disabled(runnerEditorFormBusy(s)).Clicked() {
						a.EditRunnerEditorExtract(i)
					}
					if uikit.ActionCursor(ui.Button(c, m.DeleteButton)).Clicked() {
						a.DeleteRunnerEditorExtract(i)
					}
				})
			}
			if len(target.rows) == 0 {
				ui.Text(c, "("+m.ExtractionSection+")").FontSize(11).TextColor(p.Faint)
			}
			if s.editSel != runnerEditorSelectNone {
				a.runnerEditorExtractForm(c, t, m, p)
			}
		})
	})
}

func (a *App) runnerEditorExtractForm(c *ui.Context, t *ui.Theme, m runnerEditorMessages, p uikit.Palette) {
	s := a.runnerEditorState(false)
	if s == nil {
		return
	}
	f := &s.extractForm
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(6).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.ExtractNameLabel).FontSize(11).TextColor(p.Muted)
			ui.TextInput(c, &f.name).Grow(1).Placeholder("token").Changed()
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.ExtractFromLabel).FontSize(11).TextColor(p.Muted)
			if uikit.ActionCursor(ui.Select(c, &f.fromSel, runnerEditorExtractionFroms)).Width(120).Changed() {
			}
		})
		switch f.fromSel {
		case "jsonpath":
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.ExtractPathLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.path).Grow(1).Placeholder("$.accessToken").Changed()
			})
		case "header":
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.ExtractHeaderLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.header).Grow(1).Placeholder("X-Request-Id").Changed()
			})
		case "cookie":
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.ExtractCookieLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.cookie).Grow(1).Placeholder("session").Changed()
			})
		case "body":
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.ExtractRegexLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.regex).Grow(1).Placeholder("token=([A-Za-z0-9]+)").Changed()
			})
			ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
				ui.Text(c, m.ExtractGroupLabel).FontSize(11).TextColor(p.Muted)
				ui.TextInput(c, &f.group).Width(120).Placeholder("1").Changed()
			})
		}
		ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.ExtractRequiredLabel).FontSize(11).TextColor(p.Muted)
			if uikit.ActionCursor(ui.Select(c, &f.requiredSel, runnerEditorTriStates)).Width(120) {
			}
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, m.ExtractSensitiveLabel).FontSize(11).TextColor(p.Muted)
			if uikit.ActionCursor(ui.Select(c, &f.sensitiveSel, runnerEditorTriStates)).Width(120) {
			}
		})
		if s.extractErr != "" {
			ui.Text(c, s.extractErr).FontSize(11).TextColor(t.Danger).FillWidth().Tooltip(s.extractErr)
		}
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, m.CancelEditButton)).Clicked() {
				a.CancelRunnerEditorExtract()
			}
			if uikit.ActionCursor(ui.PrimaryButton(c, m.ApplyButton)).Clicked() {
				a.ApplyRunnerEditorExtract()
			}
		})
	})
}

func (m runnerEditorMessages) TargetLabel(target string) string {
	switch target {
	case runner.TargetStatus:
		return m.TargetStatus
	case runner.TargetHeader:
		return m.TargetHeader
	case runner.TargetJSONPath:
		return m.TargetJSONPath
	case runner.TargetBody:
		return m.TargetBody
	case runner.TargetDuration:
		return m.TargetDuration
	case runner.TargetSize:
		return m.TargetSize
	case runner.TargetSchema:
		return m.TargetSchema
	}
	return m.UnknownTarget
}

func (m runnerEditorMessages) MatcherLabel(kind string) string {
	switch kind {
	case runner.MatcherExists:
		return m.MatchExists
	case runner.MatcherEquals:
		return m.MatchEquals
	case runner.MatcherNotEquals:
		return m.MatchNotEquals
	case runner.MatcherIn:
		return m.MatchIn
	case runner.MatcherContains:
		return m.MatchContains
	case runner.MatcherNotContain:
		return m.MatchNotContains
	case runner.MatcherRegex:
		return m.MatchRegex
	case runner.MatcherType:
		return m.MatchType
	}
	return m.UnknownTarget
}

// runnerEditorRowSummary and the other summary builders stay in the DSL's own
// vocabulary (status/equals/jsonpath tokens) on purpose: the runner's results
// and YAML use the same terms in every language.
func runnerEditorRowSummary(m runnerEditorMessages, row *runnerEditorRow) string {
	spec := &row.spec
	switch spec.Target {
	case runner.TargetStatus:
		parts := make([]string, 0, 2)
		if spec.StatusEquals != nil {
			parts = append(parts, fmt.Sprintf("equals %d", *spec.StatusEquals))
		}
		if len(spec.StatusIn) > 0 {
			vals := make([]string, 0, len(spec.StatusIn))
			for _, v := range spec.StatusIn {
				vals = append(vals, fmt.Sprintf("%d", v))
			}
			parts = append(parts, "in ["+strings.Join(vals, ", ")+"]")
		}
		if len(parts) == 0 {
			return "status"
		}
		return "status " + strings.Join(parts, " · ")
	case runner.TargetHeader:
		return fmt.Sprintf("header %q: %s", spec.Header, runnerEditorMatcherSummary(spec.Matchers))
	case runner.TargetJSONPath:
		return fmt.Sprintf("jsonpath %s: %s", spec.JSONPath, runnerEditorMatcherSummary(spec.Matchers))
	case runner.TargetBody:
		return "body: " + runnerEditorMatcherSummary(spec.Matchers)
	case runner.TargetInvalid:
		return "⚠ " + spec.Err
	default:
		return m.TargetLabel(spec.Target)
	}
}

func runnerEditorRowTooltip(m runnerEditorMessages, row *runnerEditorRow) string {
	if row.spec.Err != "" {
		return m.InvalidHint
	}
	if !row.spec.Structured {
		return m.PreservedHint
	}
	return runnerEditorRowSummary(m, row)
}

func runnerEditorMatcherSummary(ms []runner.MatcherSpec) string {
	parts := make([]string, 0, len(ms))
	for _, m := range ms {
		switch m.Kind {
		case runner.MatcherExists:
			if m.Exists != nil && *m.Exists {
				parts = append(parts, "exists")
			} else {
				parts = append(parts, "exists: false")
			}
		case runner.MatcherEquals, runner.MatcherNotEquals:
			v := ""
			if m.Literal != nil {
				v = runnerEditorLiteralSummary(*m.Literal)
			}
			parts = append(parts, m.Kind+" "+v)
		case runner.MatcherIn:
			vals := make([]string, 0, len(m.In))
			for _, l := range m.In {
				vals = append(vals, runnerEditorLiteralSummary(l))
			}
			parts = append(parts, "in ["+strings.Join(vals, ", ")+"]")
		case runner.MatcherContains, runner.MatcherNotContain, runner.MatcherRegex, runner.MatcherType:
			parts = append(parts, m.Kind+" "+m.Text)
		}
	}
	s := strings.Join(parts, ", ")
	if s == "" {
		return "(no matcher)"
	}
	return s
}

func runnerEditorLiteralSummary(l runner.LiteralSpec) string {
	switch l.Kind {
	case runner.LiteralString:
		return fmt.Sprintf("%q", l.Text)
	case runner.LiteralNull:
		return "null"
	default:
		return l.Text
	}
}

func runnerEditorExtractSummary(m runnerEditorMessages, e *runner.Extraction) string {
	target := ""
	switch strings.ToLower(strings.TrimSpace(e.From)) {
	case "jsonpath":
		target = e.Path
	case "header":
		target = e.Header
	case "cookie":
		target = e.Cookie
	case "body":
		target = e.Regex
	}
	sensitive := ""
	if runner.ExtractionEffectiveSensitive(*e) {
		sensitive = " · " + m.SensitiveBadge
		if e.Sensitive == nil {
			sensitive += " (default)"
		}
	} else if e.Sensitive != nil {
		sensitive = " · " + m.SensitiveBadge + "=no"
	}
	required := ""
	if !e.IsRequired() {
		required = " · " + m.RequiredBadge + "=no"
	}
	return fmt.Sprintf("%s ← %s %s%s%s", e.Name, e.From, target, required, sensitive)
}
