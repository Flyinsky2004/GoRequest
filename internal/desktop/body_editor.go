package desktop

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

func (bs *tabBodyState) toFormFields() []core.FormField {
	var out []core.FormField
	for _, r := range bs.formRows {
		if r == nil || r.isBlank() {
			continue
		}
		enabled := r.Enabled
		out = append(out, core.FormField{
			Name:    r.Name,
			Value:   r.Value,
			Enabled: &enabled,
			Encoded: r.Encoded,
			Extra:   r.Extra,
		})
	}
	return out
}

func (bs *tabBodyState) setFormFields(fields []core.FormField) {
	rows := make([]*formBodyRow, 0, len(fields)+1)
	for _, f := range fields {
		rows = append(rows, &formBodyRow{
			ID:      nextRowID(),
			Enabled: f.IsEnabled(),
			Name:    f.Name,
			Value:   f.Value,
			Encoded: f.Encoded,
			Extra:   f.Extra,
		})
	}
	rows = append(rows, newBlankFormRow())
	bs.formRows = rows
}

func validateFormRows(m *messages, rows []*formBodyRow) error {
	for i, r := range rows {
		if r == nil || r.isBlank() {
			continue
		}
		if strings.TrimSpace(r.Name) == "" && strings.TrimSpace(r.Value) != "" {
			return fmt.Errorf(m.RowNoKey, i+1, r.Value)
		}
	}
	return nil
}

func sanitizeLocalSlotFromPath(rawName, filePath string) string {
	cand := strings.TrimSpace(rawName)
	if cand == "" && filePath != "" {
		base := filepath.Base(filePath)
		cand = strings.TrimSuffix(base, filepath.Ext(base))
	}
	var sb strings.Builder
	for _, r := range cand {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}
	slot := strings.Trim(sb.String(), "-_.")
	if slot == "" || core.ValidateLocalSlot(slot) != nil {
		return "local-file"
	}
	return slot
}

func (a *App) mgrSecrets() core.SecretProvider {
	if a == nil || a.mgr == nil {
		return nil
	}
	return a.secretsProvider()
}

func (a *App) resolveTabVars(tab *Tab) map[string]string {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	if ps == nil || ps.project == nil {
		return map[string]string{}
	}
	vars, _, err := ps.project.ResolveVarsWith(tab.Entry, ps.currentEnv(), nil)
	if err != nil || vars == nil {
		return map[string]string{}
	}
	return vars
}

func (a *App) projectFileStatus(tab *Tab, rel string) (text string, isErr bool) {
	rel = strings.TrimSpace(rel)
	if rel == "" {
		return "", false
	}
	bm := a.bodyMsg()
	proj := a.projectOf(tab)
	if proj == nil || proj.Dir == "" {
		if _, err := core.ValidateProjectRelPath(rel); err != nil {
			return err.Error(), true
		}
		return "", false
	}
	opener := core.DirFileOpener{Root: proj.Dir}
	opened, err := opener.OpenProjectFile(context.Background(), rel)
	if err != nil {
		return err.Error(), true
	}
	_ = opened.Reader.Close()
	return fmt.Sprintf(bm.FileReady, opened.Name, opened.Size), false
}

func (a *App) localFileStatus(tab *Tab, slot string) (text string, isErr bool) {
	slot = strings.TrimSpace(slot)
	if slot == "" {
		return "", false
	}
	if err := core.ValidateLocalSlot(slot); err != nil {
		return err.Error(), true
	}
	bm := a.bodyMsg()
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	absPath, ok := a.BoundLocalFile(ps, slot)
	if !ok {
		return fmt.Sprintf(bm.FileUnbound, slot), true
	}
	stream, err := core.OpenBodyStream(context.Background(), &core.Body{
		Type: core.BodyBinary,
		File: core.FormatLocalFileRef(slot),
	}, nil, core.PlanOptions{
		LocalFiles: core.MapLocalFiles{slot: absPath},
	})
	if err != nil {
		return err.Error(), true
	}
	if stream != nil && stream.Reader != nil {
		_ = stream.Reader.Close()
	}
	return fmt.Sprintf(bm.FileReady, filepath.Base(absPath), stream.ContentLength), false
}

// renderBodyEditor builds the native B03 HTTP Body editor inside workspace.go's
// bodyEditor slot, covering None / JSON / XML / Text / Form URL-Encoded /
// Multipart / Binary, project-relative files, device-local file bindings, and
// safe preservation of unsupported modes and unknown fields.
func (a *App) renderBodyEditor(c *ui.Context, t *ui.Theme, tab *Tab) {
	bs := ensureBodyState(tab, a)
	if bs == nil {
		return
	}
	m := a.msg()
	bm := a.bodyMsg()
	p := uikit.PaletteFor(t)
	danger := uikit.MethodColor("DELETE", p)

	if bs.unsupported {
		a.renderUnsupportedBodyCard(c, p, danger, bm, tab, bs)
		return
	}

	modeLabels := []string{
		bm.ModeNone,
		bm.ModeJSON,
		bm.ModeXML,
		bm.ModeText,
		bm.ModeURLEncoded,
		bm.ModeMultipart,
		bm.ModeBinary,
	}
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, m.BodyType).FontSize(12).TextColor(p.Muted)
		if uikit.NativeSegmented(c, &bs.modeIdx, modeLabels...).Changed() {
			tab.bodyEditor = nil
			bs.builtEditorKey = ""
			bs.markMutated(tab)
		}
	})

	if len(bs.unknownKeys) > 0 {
		ui.Text(c, fmt.Sprintf(bm.UnknownFieldsWarning, strings.Join(bs.unknownKeys, ", "))).
			FontSize(11).TextColor(danger).Padding(2, 6)
	}

	kind := bs.currentKind()
	switch kind {
	case core.BodyNone:
		a.emptyTabHint(c, p, bm.NoBodyHint)

	case core.BodyJSON, core.BodyXML, core.BodyText:
		a.renderTextLikeBody(c, t, p, bm, tab, bs, kind)

	case core.BodyURLEncoded:
		a.renderURLEncodedBody(c, p, danger, m, bm, tab, bs)

	case core.BodyFormData:
		a.renderMultipartBody(c, p, danger, m, bm, tab, bs)

	case core.BodyBinary:
		a.renderBinaryBody(c, t, p, bm, tab, bs)
	}
}

func (a *App) renderUnsupportedBodyCard(c *ui.Context, p uikit.Palette, danger ui.Color, bm *bodyMessages, tab *Tab, bs *tabBodyState) {
	rawLabel := strings.TrimSpace(bs.rawType)
	if rawLabel == "" {
		rawLabel = "(custom/invalid)"
	}
	ui.Column(c).Grow(1).FillWidth().Gap(8).Padding(12).Radius(10).
		BorderWidth(1).BorderColor(danger).Children(func() {
		ui.Text(c, fmt.Sprintf(bm.UnsupportedBodyTitle, rawLabel)).FontSize(13).FontWeight(600).TextColor(danger)
		if bs.loadErr != "" {
			ui.Text(c, bs.loadErr).FontSize(12).TextColor(danger)
		}
		ui.Text(c, bm.UnsupportedBodyHint).FontSize(12).TextColor(p.Muted)

		preview := core.BodyPreviewYAML(bs.origBody)
		if preview != "" {
			ui.Box(c).Grow(1).FillWidth().Radius(8).BorderWidth(1).BorderColor(p.Hairline).Padding(8).Clip().Children(func() {
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					ui.Text(c, preview).Font("monospace").FontSize(12).TextColor(p.Text)
				})
			})
		}

		ui.Row(c).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.Button(c, bm.ReplaceUnsupportedBody)).Clicked() {
				bs.unsupported = false
				bs.loadErr = ""
				bs.modeIdx = uiBodyModeIndex(core.BodyNone)
				tab.bodyEditor = nil
				bs.builtEditorKey = ""
				bs.markMutated(tab)
			}
		})
	})
}

func (a *App) renderTextLikeBody(c *ui.Context, t *ui.Theme, p uikit.Palette, bm *bodyMessages, tab *Tab, bs *tabBodyState, kind string) {
	srcLabels := []string{bm.SourceInline, bm.SourceProjectFile, bm.SourceLocalFile}
	ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, bm.SourceLabel).FontSize(12).TextColor(p.Muted)
		if uikit.NativeSegmented(c, &bs.textSource, srcLabels...).Changed() {
			tab.bodyEditor = nil
			bs.builtEditorKey = ""
			bs.markMutated(tab)
		}
	})

	switch bs.textSource {
	case bodySourceProjectFile:
		a.renderProjectFileControl(c, p, bm, tab, bs, &bs.projectFile)

	case bodySourceLocalFile:
		a.renderLocalFileControl(c, p, bm, tab, bs, &bs.localSlot, &bs.localPathDraft, kind)

	default:
		bufPtr := bs.activeInlineBufferPtr()
		if tab.bodyEditor == nil || bs.builtEditorKey != kind {
			st := &editor.State{Buffer: editor.NewBuffer(*bufPtr), Theme: uikit.EditorTheme(t)}
			switch kind {
			case core.BodyJSON:
				st.Buffer.SetLanguage(editor.LanguageNamed("json"))
			case core.BodyXML:
				st.Buffer.SetLanguage(editor.LanguageNamed("xml"))
			}
			tab.bodyEditor = st
			bs.builtEditorKey = kind
			bs.builtEditorGen = a.themeGeneration()
		}
		// An appearance switch restyles the cached editor in place; the
		// buffer, caret and undo history are untouched.
		a.syncEditorTheme(tab.bodyEditor, &bs.builtEditorGen, t)
		if s := tab.bodyEditor.Buffer.String(); s != *bufPtr {
			*bufPtr = s
			bs.markMutated(tab)
		}
		editor.View(c, tab.bodyEditor).Grow(1).FillWidth()
		a.renderTemplateVarPreview(c, p, tab, *bufPtr)
	}
}

func (a *App) renderProjectFileControl(c *ui.Context, p uikit.Palette, bm *bodyMessages, tab *Tab, bs *tabBodyState, relPtr *string) {
	danger := uikit.MethodColor("DELETE", p)
	opts := filterSuggestions(projectFilesSuggestions(a.projectOf(tab)), *relPtr)
	ui.Column(c).Gap(8).FillWidth().Padding(8).Radius(10).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, bm.ProjectFileLabel).FontSize(12).TextColor(p.Muted).Width(110)
			if ui.Autocomplete(c, relPtr, opts).Grow(1).Font("monospace").Placeholder(bm.ProjectFilePlaceholder).Changed() {
				bs.markMutated(tab)
			}
		})
		if status, isErr := a.projectFileStatus(tab, *relPtr); status != "" {
			col := p.Muted
			if isErr {
				col = danger
			}
			ui.Text(c, status).FontSize(11).TextColor(col)
		}
	})
}

func (a *App) renderLocalFileControl(c *ui.Context, p uikit.Palette, bm *bodyMessages, tab *Tab, bs *tabBodyState, slotPtr, pathPtr *string, defaultSlotHint string) {
	danger := uikit.MethodColor("DELETE", p)
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	// Keep displayed path synchronized if bound externally (e.g. via API or another tab).
	if slot := strings.TrimSpace(*slotPtr); slot != "" && *pathPtr == "" {
		if bound, ok := a.BoundLocalFile(ps, slot); ok {
			*pathPtr = bound
		}
	}

	ui.Column(c).Gap(8).FillWidth().Padding(8).Radius(10).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, bm.LocalSlotLabel).FontSize(12).TextColor(p.Muted).Width(110)
			if ui.TextInput(c, slotPtr).Grow(1).Font("monospace").Placeholder(bm.LocalSlotPlaceholder).Changed() {
				bs.markMutated(tab)
				slot := strings.TrimSpace(*slotPtr)
				if bound, ok := a.BoundLocalFile(ps, slot); ok {
					*pathPtr = bound
				} else if strings.TrimSpace(*pathPtr) != "" && core.ValidateLocalSlot(slot) == nil {
					_ = a.BindLocalFile(ps, slot, *pathPtr)
				}
			}
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			ui.Text(c, bm.LocalPathLabel).FontSize(12).TextColor(p.Muted).Width(110)
			if ui.TextInput(c, pathPtr).Grow(1).Font("monospace").Placeholder(bm.LocalPathPlaceholder).Changed() {
				slot := strings.TrimSpace(*slotPtr)
				if slot == "" && strings.TrimSpace(*pathPtr) != "" {
					*slotPtr = sanitizeLocalSlotFromPath(defaultSlotHint, *pathPtr)
					slot = *slotPtr
					bs.markMutated(tab)
				}
				if core.ValidateLocalSlot(slot) == nil {
					_ = a.BindLocalFile(ps, slot, *pathPtr)
				}
			}
			if uikit.ActionCursor(ui.Button(c, bm.ChooseFile)).Clicked() {
				a.pickLocalFileDialog(func(chosen string) {
					*pathPtr = chosen
					slot := strings.TrimSpace(*slotPtr)
					if slot == "" {
						*slotPtr = sanitizeLocalSlotFromPath(defaultSlotHint, chosen)
						slot = *slotPtr
						bs.markMutated(tab)
					}
					_ = a.BindLocalFile(ps, slot, chosen)
				})
			}
			if strings.TrimSpace(*slotPtr) != "" {
				if uikit.ActionCursor(ui.Button(c, bm.UnbindFile)).Clicked() {
					*pathPtr = ""
					_ = a.UnbindLocalFile(ps, *slotPtr)
				}
			}
		})
		ui.Text(c, bm.LocalIsolationHint).FontSize(11).TextColor(p.Muted)
		if status, isErr := a.localFileStatus(tab, *slotPtr); status != "" {
			col := p.Muted
			if isErr {
				col = danger
			}
			ui.Text(c, status).FontSize(11).TextColor(col)
		}
	})
}

func (a *App) renderURLEncodedBody(c *ui.Context, p uikit.Palette, danger ui.Color, m *messages, bm *bodyMessages, tab *Tab, bs *tabBodyState) {
	bs.ensureTrailingFormBlank()

	prevMode := bs.formViewMode
	ui.Row(c).Gap(8).AlignItems(ui.Center).Padding(0, 8).FillWidth().Children(func() {
		if bs.formViewMode == 0 {
			ui.Box(c).Width(28)
			ui.Text(c, m.Key).FontSize(12).FontWeight(600).TextColor(p.Muted).Width(180)
			ui.Text(c, m.Value).FontSize(12).FontWeight(600).TextColor(p.Muted).Grow(1)
			ui.Text(c, bm.PreEncodedFlag).FontSize(12).FontWeight(600).TextColor(p.Muted).Width(100)
			ui.Box(c).Width(28)
		} else {
			ui.Box(c).Grow(1)
		}
		if uikit.NativeSegmented(c, &bs.formViewMode, m.KeyValueMode, bm.BatchTextMode, bm.RawEncodedMode).Changed() {
			if prevMode != 0 && bs.formErr != "" {
				// Keep raw draft visible until syntax error is fixed so text is never lost.
				bs.formViewMode = prevMode
				return
			}
			switch bs.formViewMode {
			case 1:
				bs.formRawText = core.FormatFormBatchLines(bs.toFormFields())
				bs.formErr = ""
			case 2:
				bs.formRawText = core.FormatRawURLEncodedFields(bs.toFormFields())
				bs.formErr = ""
			default:
				bs.formErr = ""
				bs.ensureTrailingFormBlank()
			}
		}
	})

	if bs.formViewMode != 0 {
		ui.Box(c).Grow(1).FillWidth().Radius(10).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
			ta := ui.TextArea(c, &bs.formRawText).Grow(1).FillWidth().Font("monospace")
			if ta.Changed() {
				var (
					parsed []core.FormField
					err    error
				)
				if bs.formViewMode == 1 {
					parsed, err = core.ParseFormBatchLines(bs.formRawText)
				} else {
					parsed, err = core.ParseRawURLEncodedFields(bs.formRawText)
				}
				if err != nil {
					bs.formErr = err.Error()
					bs.bodyMutated = true
					tab.Dirty = true
				} else {
					bs.setFormFields(parsed)
					bs.formErr = ""
					bs.markMutated(tab)
				}
			}
		})
		if bs.formErr != "" {
			ui.Text(c, bs.formErr).FontSize(12).TextColor(danger).Padding(2, 8)
		}
	} else {
		ui.Box(c).Grow(1).FillWidth().Radius(10).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
			ui.Scroll(c).Grow(1).FillWidth().Children(func() {
				ui.Column(c).FillWidth().Children(func() {
					for _, row := range bs.formRows {
						a.renderFormBodyRow(c, p, m, bm, tab, bs, row)
					}
				})
			})
		})
		if err := validateFormRows(m, bs.formRows); err != nil {
			ui.Text(c, err.Error()).FontSize(12).TextColor(danger).Padding(2, 8)
		}
	}

	vars := a.resolveTabVars(tab)
	wire := core.PreviewURLEncodedBody(bs.toFormFields(), vars, a.mgrSecrets())
	if wire != "" {
		ui.Row(c).Gap(6).AlignItems(ui.Center).Padding(2, 8).FillWidth().Children(func() {
			ui.Text(c, bm.WirePreviewLabel+":").FontSize(11).FontWeight(600).TextColor(p.Muted)
			ui.Text(c, wire).Font("monospace").FontSize(11).TextColor(p.Muted).SingleLine().Grow(1)
		})
	}
}

func (a *App) renderFormBodyRow(c *ui.Context, p uikit.Palette, m *messages, bm *bodyMessages, tab *Tab, bs *tabBodyState, row *formBodyRow) {
	blank := row.isBlank()
	r := ui.Row(c).Key(row.ID).Height(36).Gap(8).AlignItems(ui.Center).FillWidth().Padding(0, 8)
	if r.Hovered() {
		r.Background(p.Hover)
	}
	r.Children(func() {
		if uikit.ActionCursor(ui.Checkbox(c, &row.Enabled, "")).Changed() {
			bs.markMutated(tab)
		}
		keyPh, valPh := "", ""
		if blank {
			keyPh = m.Key
			valPh = m.Value
		}
		if ui.TextInput(c, &row.Name).Width(180).Placeholder(keyPh).Changed() {
			bs.ensureTrailingFormBlank()
			bs.markMutated(tab)
		}
		if ui.TextInput(c, &row.Value).Grow(1).Placeholder(valPh).Changed() {
			bs.ensureTrailingFormBlank()
			bs.markMutated(tab)
		}
		ui.Box(c).Width(100).Children(func() {
			if uikit.ActionCursor(ui.Checkbox(c, &row.Encoded, "")).Changed() {
				bs.markMutated(tab)
			}
		})
		if blank {
			ui.Box(c).Width(28)
		} else if uikit.ActionCursor(ui.Button(c, "✕")).Clicked() {
			bs.removeFormRow(row.ID)
			bs.markMutated(tab)
		}
	})
}

func (a *App) renderMultipartBody(c *ui.Context, p uikit.Palette, danger ui.Color, m *messages, bm *bodyMessages, tab *Tab, bs *tabBodyState) {
	if len(bs.multipartRows) == 0 {
		bs.multipartRows = append(bs.multipartRows, newBlankMultipartRow(partSourceText))
	}

	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		if uikit.ActionCursor(ui.Button(c, bm.AddTextPart)).Clicked() {
			bs.multipartRows = append(bs.multipartRows, newBlankMultipartRow(partSourceText))
			bs.markMutated(tab)
		}
		if uikit.ActionCursor(ui.Button(c, bm.AddFilePart)).Clicked() {
			bs.multipartRows = append(bs.multipartRows, newBlankMultipartRow(partSourceProjectFile))
			bs.markMutated(tab)
		}
		ui.Box(c).Grow(1)
		ui.Text(c, bm.MultipartBoundaryHint).FontSize(11).TextColor(p.Muted).SingleLine()
	})

	ui.Box(c).Grow(1).FillWidth().Radius(10).BorderWidth(1).BorderColor(p.Hairline).Clip().Children(func() {
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).FillWidth().Gap(4).Padding(4).Children(func() {
				for _, row := range bs.multipartRows {
					a.renderMultipartRow(c, p, danger, m, bm, tab, bs, row)
				}
			})
		})
	})
}

func (a *App) renderMultipartRow(c *ui.Context, p uikit.Palette, danger ui.Color, m *messages, bm *bodyMessages, tab *Tab, bs *tabBodyState, row *multipartBodyRow) {
	ps := a.ps
	if tab != nil && tab.Owner != nil {
		ps = tab.Owner
	}
	if row.PartSource == partSourceLocalFile && strings.TrimSpace(row.LocalSlot) != "" && row.LocalPathDraft == "" {
		if bound, ok := a.BoundLocalFile(ps, row.LocalSlot); ok {
			row.LocalPathDraft = bound
		}
	}

	card := ui.Column(c).Key(row.ID).FillWidth().Gap(6).Padding(6, 8).Radius(8).
		BorderWidth(1).BorderColor(p.Hairline)
	if card.Hovered() {
		card.Background(p.Hover)
	}
	card.Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
			if uikit.ActionCursor(ui.Checkbox(c, &row.Enabled, "")).Changed() {
				bs.markMutated(tab)
			}
			if ui.TextInput(c, &row.Name).Width(150).Placeholder(m.Key).Changed() {
				bs.markMutated(tab)
			}
			if uikit.NativeSegmented(c, &row.PartSource, bm.PartKindText, bm.PartKindProjectFile, bm.PartKindLocalFile).Changed() {
				bs.markMutated(tab)
			}
			if ui.Autocomplete(c, &row.ContentType, filterSuggestions(contentTypeSuggestions, row.ContentType)).
				Width(170).Placeholder(bm.ContentTypePlaceholder).Changed() {
				bs.markMutated(tab)
			}
			if row.PartSource != partSourceText {
				if ui.TextInput(c, &row.FileName).Width(150).Placeholder(bm.FileNameOverridePlaceholder).Changed() {
					bs.markMutated(tab)
				}
			}
			ui.Box(c).Grow(1)
			if uikit.ActionCursor(ui.Button(c, "✕")).Clicked() {
				bs.removeMultipartRow(row.ID)
				bs.markMutated(tab)
			}
		})

		ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Padding(0, 0, 0, 28).Children(func() {
			switch row.PartSource {
			case partSourceProjectFile:
				opts := filterSuggestions(projectFilesSuggestions(a.projectOf(tab)), row.ProjectFile)
				if ui.Autocomplete(c, &row.ProjectFile, opts).Grow(1).Font("monospace").Placeholder(bm.ProjectFilePlaceholder).Changed() {
					bs.markMutated(tab)
				}
				if status, isErr := a.projectFileStatus(tab, row.ProjectFile); status != "" {
					col := p.Muted
					if isErr {
						col = danger
					}
					ui.Text(c, status).FontSize(11).TextColor(col).SingleLine()
				}

			case partSourceLocalFile:
				if ui.TextInput(c, &row.LocalSlot).Width(150).Font("monospace").Placeholder(bm.LocalSlotLabel).Changed() {
					bs.markMutated(tab)
					slot := strings.TrimSpace(row.LocalSlot)
					if bound, ok := a.BoundLocalFile(ps, slot); ok {
						row.LocalPathDraft = bound
					} else if strings.TrimSpace(row.LocalPathDraft) != "" && core.ValidateLocalSlot(slot) == nil {
						_ = a.BindLocalFile(ps, slot, row.LocalPathDraft)
					}
				}
				if ui.TextInput(c, &row.LocalPathDraft).Grow(1).Font("monospace").Placeholder(bm.LocalPathPlaceholder).Changed() {
					slot := strings.TrimSpace(row.LocalSlot)
					if slot == "" && strings.TrimSpace(row.LocalPathDraft) != "" {
						row.LocalSlot = sanitizeLocalSlotFromPath(row.Name, row.LocalPathDraft)
						slot = row.LocalSlot
						bs.markMutated(tab)
					}
					if core.ValidateLocalSlot(slot) == nil {
						_ = a.BindLocalFile(ps, slot, row.LocalPathDraft)
					}
				}
				if uikit.ActionCursor(ui.Button(c, bm.ChooseFile)).Clicked() {
					a.pickLocalFileDialog(func(chosen string) {
						row.LocalPathDraft = chosen
						slot := strings.TrimSpace(row.LocalSlot)
						if slot == "" {
							row.LocalSlot = sanitizeLocalSlotFromPath(row.Name, chosen)
							slot = row.LocalSlot
							bs.markMutated(tab)
						}
						_ = a.BindLocalFile(ps, slot, chosen)
					})
				}
				if strings.TrimSpace(row.LocalSlot) != "" {
					if uikit.ActionCursor(ui.Button(c, bm.UnbindFile)).Clicked() {
						row.LocalPathDraft = ""
						_ = a.UnbindLocalFile(ps, row.LocalSlot)
					}
				}
				if status, isErr := a.localFileStatus(tab, row.LocalSlot); status != "" {
					col := p.Muted
					if isErr {
						col = danger
					}
					ui.Text(c, status).FontSize(11).TextColor(col).SingleLine()
				}

			default:
				if ui.TextInput(c, &row.Value).Grow(1).Placeholder(m.Value).Changed() {
					bs.markMutated(tab)
				}
			}
		})
	})
}

func (a *App) renderBinaryBody(c *ui.Context, t *ui.Theme, p uikit.Palette, bm *bodyMessages, tab *Tab, bs *tabBodyState) {
	_ = t
	ui.Row(c).Gap(8).AlignItems(ui.Center).FillWidth().Children(func() {
		ui.Text(c, bm.SourceLabel).FontSize(12).TextColor(p.Muted)
		if uikit.NativeSegmented(c, &bs.binSource, bm.SourceProjectFile, bm.SourceLocalFile).Changed() {
			bs.markMutated(tab)
		}
		ui.Box(c).Width(12)
		ui.Text(c, "Content-Type").FontSize(12).TextColor(p.Muted)
		if ui.Autocomplete(c, &bs.binContentType, filterSuggestions(contentTypeSuggestions, bs.binContentType)).
			Grow(1).Placeholder("application/octet-stream").Changed() {
			bs.markMutated(tab)
		}
	})

	if bs.binSource == binSourceLocalFile {
		a.renderLocalFileControl(c, p, bm, tab, bs, &bs.binLocalSlot, &bs.binLocalPath, "binary-payload")
	} else {
		a.renderProjectFileControl(c, p, bm, tab, bs, &bs.binProjectFile)
	}
}

func (a *App) renderTemplateVarPreview(c *ui.Context, p uikit.Palette, tab *Tab, input string) {
	items := core.PreviewTemplateVars(a.resolveTabVars(tab), a.mgrSecrets(), input)
	if len(items) == 0 {
		return
	}
	m := a.msg()
	danger := uikit.MethodColor("DELETE", p)
	spans := []ui.Span{{Text: m.VarLabel + "  ", Size: 11, Weight: 600, Color: p.Muted}}
	for _, it := range items {
		switch it.Status {
		case core.VarStatusDefined:
			spans = append(spans, ui.Span{Text: it.Name + " = " + it.Display + "   ", Size: 11, Color: p.Muted})
		case core.VarStatusSecretBound:
			spans = append(spans, ui.Span{Text: "{{" + it.Name + "}} = (secret)   ", Size: 11, Color: p.Muted})
		default:
			spans = append(spans, ui.Span{
				Text:       "{{" + it.Name + "}} ?   ",
				Size:       11,
				Color:      danger,
				Background: danger.Alpha(0.12),
				Radius:     3,
			})
		}
	}
	ui.RichText(c, spans...).FillWidth()
}
