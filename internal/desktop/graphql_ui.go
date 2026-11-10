package desktop

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// graphql_ui.go renders a GraphQL request inside the same main workspace as
// HTTP and WebSocket requests. The upper pane edits the document, variables,
// headers and auth; the lower pane separates data from errors and keeps the raw
// body and headers reachable.

const (
	gqlDataDisplayCap   = 512 << 10
	gqlErrorsDisplayCap = 100
)

// gqlTabView is the GraphQL workspace for one request tab.
func (a *App) gqlTabView(c *ui.Context, t *ui.Theme, tab *Tab) {
	st := tab.ensureGQLState()
	if st == nil {
		return
	}
	m := a.gqlMsg()
	p := uikit.PaletteFor(t)
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveActiveWithFeedback() })
	c.OnShortcut(ui.Cmd, ui.KeyEnter, func() { a.gqlTrySend(tab) })

	ui.Column(c).Grow(1).FillHeight().Padding(12, 16).Gap(10).Children(func() {
		a.gqlEnsureSchema(tab)
		st.gqlRefreshAnalysis()
		a.gqlURLRow(c, t, tab, st, m, p)
		// Introspection results are a preview until the user saves them.
		a.gqlIntrospectionModal(c, t, tab, st, m, p)

		labels := []string{m.SectionDocument, m.SectionVariables, m.SectionSchema, m.SectionHeaders, m.SectionAuth}
		badges := make([]string, len(labels))
		if n := kvCount(tab.HeaderRows); n > 0 {
			// Badge the Headers tab, not whichever section sits at index 2:
			// inserting Schema shifted Headers to index 3.
			badges[3] = badgeCount(n)
		}
		uikit.UnderlineTabs(c, p, &st.section, labels, badges)

		upper := func() {
			uikit.TabContent(c, st.section, func(int) {
				ui.Column(c).Grow(1).FillWidth().Gap(4).Children(func() {
					switch st.section {
					case 0:
						a.gqlDocumentSection(c, t, tab, st, m, p)
					case 1:
						a.gqlVariablesSection(c, t, tab, st, m, p)
					case 2:
						a.gqlSchemaSection(c, t, tab, st, m, p)
					case 3:
						a.kvForm(c, t, tab, kvHeader)
					default:
						a.authEditor(c, t, tab)
					}
				})
			}).Grow(1)
		}
		lower := func() {
			col := ui.Column(c).Grow(1).FillHeight()
			if ps != nil {
				col = col.Bind(&ps.respRef)
			}
			col.Children(func() {
				a.gqlResultPane(c, t, tab, st, m, p)
			})
		}
		if ps == nil {
			ui.SplitVerticalPercent(c, new(float32), upper, lower).Grow(1).FillWidth()
			return
		}
		splitHeight := ps.splitRef.Bounds(c).H
		eff := clampBuildPctForHeight(ps.buildPct, splitHeight)
		ps.layoutPct = eff
		split := ui.SplitVerticalPercent(c, &ps.layoutPct, upper, lower).Grow(1).FillWidth().Bind(&ps.splitRef)
		if moved := ps.layoutPct; moved != eff {
			ps.buildPct = applyBuildPctMove(ps.buildPct, eff, moved)
			a.mgr.cfg.setBuildPct(ps.buildPct)
		}
		if split.DoubleClicked() {
			ps.buildPct = defaultBuildPct
			ps.layoutPct = defaultBuildPct
			a.mgr.cfg.setBuildPct(defaultBuildPct)
		}
		a.cookieJarModal(c, t, tab)
		a.networkSettingsModal(c, t, tab)
	})
}

// gqlURLRow is the GraphQL request line: POST/GET selector, endpoint, send or
// cancel, and a short draft/parse notice.
func (a *App) gqlURLRow(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			grp := ui.Row(c).Grow(1).Height(40).Radius(10).BorderWidth(1).
				BorderColor(urlStatusBorderColor(urlSendStateOf(tab), p)).
				Background(p.BG).AlignItems(ui.Center).Clip().Label("graphql URL group")
			var urlInput ui.Element
			grp.Children(func() {
				a.gqlMethodPicker(c, p, tab)
				ui.Box(c).Width(1).Height(40).Background(methodURLDividerColor(p)).Label("method separator")
				urlBox := ui.Row(c).Grow(1).Padding(6, 10).AlignItems(ui.Center).
					Radius(8).Background(p.Sidebar)
				urlBox.Children(func() {
					urlInput = ui.TextInputBase(c, &tab.URL).Grow(1).Font("monospace").Label("graphql URL")
					urlInput.OnShortcut(0, ui.KeyEnter, func() { a.gqlTrySend(tab) })
					if urlInput.Changed() {
						tab.Dirty = true
					}
					if tab.focusURL {
						urlInput.Focus()
						tab.focusURL = false
					}
				})
			})
			grp.DrawOver(func(painter *ui.Painter, r ui.Rect) {
				if urlInput.Focused() {
					const ring = float32(2)
					painter.Stroke(ui.Rect{
						X: r.X - ring - 1, Y: r.Y - ring - 1,
						W: r.W + 2*ring + 2, H: r.H + 2*ring + 2,
					}, p.Muted, 12, ring)
				}
			})
			a.renderTimeoutBadge(c, t, tab)
			if gqlBusy(tab) {
				if _, clicked := uikit.SendButton(c, p, true, a.msg().CancelRequest); clicked {
					a.gqlCancel(tab)
				}
			} else if _, clicked := uikit.SendButton(c, p, false, a.msg().Send); clicked {
				a.gqlTrySend(tab)
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.DocumentHint).FontSize(11).TextColor(p.Faint).SingleLine()
			if st.parseErr != "" {
				ui.Text(c, fmt.Sprintf(m.ParseErrorFmt, st.parseErr)).FontSize(11).
					TextColor(p.Danger).SingleLine().Grow(1)
			} else if st.notice != "" {
				ui.Text(c, st.notice).FontSize(11).TextColor(p.Muted).SingleLine().Grow(1)
			}
		})
	})
}

// gqlMethodPicker offers only POST and GET; GET cannot execute a mutation.
func (a *App) gqlMethodPicker(c *ui.Context, p uikit.Palette, tab *Tab) {
	method := tab.gqlMethod()
	row := ui.Row(c).Width(108).Height(40).Padding(0, 10).Gap(4).AlignItems(ui.Center)
	row.Children(func() {
		ui.Text(c, method).Font("monospace").FontWeight(700).FontSize(13).
			TextColor(uikit.MethodColor(method, p)).SingleLine()
		ui.Icon(c, uikit.IconChevron).FontSize(11).TextColor(p.Muted)
	})
	uikit.ActionCursor(row).PopoverMenuButton(func(mm *ui.Menu) {
		for _, meth := range []string{"POST", "GET"} {
			if mm.Item(meth).Checked(meth == method).Chosen() {
				tab.Method = meth
				tab.Dirty = true
			}
		}
	})
}

// gqlDocumentSection edits the document and selects an operation.
func (a *App) gqlDocumentSection(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	names := make([]string, 0, len(st.parsed))
	for _, op := range st.parsed {
		if op.Name != "" {
			names = append(names, op.Name)
		}
	}
	ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.OperationLabel).FontSize(12).TextColor(p.Muted).Width(72)
		op := ui.Autocomplete(c, &st.operationName, names).Grow(1).Font("monospace").
			Placeholder(m.OperationPlaceholder)
		if op.Changed() {
			st.parsedDoc = ""
			tab.Dirty = true
		}
		if len(names) > 0 {
			btn := ui.Row(c).Height(30).Padding(0, 8).Gap(4).AlignItems(ui.Center)
			btn.Children(func() {
				ui.Text(c, m.OperationLabel).FontSize(11).TextColor(p.Muted)
				ui.Icon(c, uikit.IconChevron).FontSize(10).TextColor(p.Muted)
			})
			uikit.ActionCursor(btn).PopoverMenuButton(func(mm *ui.Menu) {
				for _, name := range names {
					if mm.Item(name).Checked(name == st.operationName).Chosen() {
						a.gqlSetOperation(tab, name)
					}
				}
			})
		}
	})
	if len(st.parsed) > 1 && strings.TrimSpace(st.operationName) == "" {
		ui.Text(c, m.OperationHint).FontSize(11).TextColor(p.Warning)
	}
	if st.parseErr == "" && len(st.parsed) > 0 {
		selected := ""
		for _, op := range st.parsed {
			if op.Name == st.operationName || (st.operationName == "" && len(st.parsed) == 1) {
				selected = fmt.Sprintf(m.ParseSelectedFmt, op.Type, op.Name)
				break
			}
		}
		line := fmt.Sprintf(m.ParseOperationsFmt, len(st.parsed))
		if selected != "" {
			line += " · " + selected
		}
		ui.Text(c, line).FontSize(11).TextColor(p.Success)
	}

	if st.docEditor == nil {
		st.docEditor = &editor.State{Buffer: editor.NewBuffer(st.document), Theme: uikit.EditorTheme(t)}
		st.docGen = a.themeGeneration()
	}
	a.syncEditorTheme(st.docEditor, &st.docGen, t)
	if got := st.docEditor.Buffer.String(); got != st.document {
		st.document = got
		tab.Dirty = true
	}
	editor.View(c, st.docEditor).Grow(1).FillWidth()
	ui.Text(c, m.DocumentHint).FontSize(11).TextColor(p.Faint)
}

// gqlVariablesSection edits the variables JSON object.
func (a *App) gqlVariablesSection(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	if st.varEditor == nil {
		st.varEditor = &editor.State{Buffer: editor.NewBuffer(st.variables), Theme: uikit.EditorTheme(t)}
		st.varEditor.Buffer.SetLanguage(editor.LanguageNamed("json"))
		st.varGen = a.themeGeneration()
	}
	a.syncEditorTheme(st.varEditor, &st.varGen, t)
	if got := st.varEditor.Buffer.String(); got != st.variables {
		st.variables = got
		tab.Dirty = true
	}
	editor.View(c, st.varEditor).Grow(1).FillWidth()
	ui.Text(c, m.VariablesHint).FontSize(11).TextColor(p.Faint)
}

// gqlResultPane is the lower half: HTTP facts plus Data/Errors/Raw/Headers, or
// the live graphql-transport-ws event pane when a subscription is active.
func (a *App) gqlResultPane(c *ui.Context, t *ui.Theme, tab *Tab, st *gqlTabState, m *gqlMessages, p uikit.Palette) {
	if gqlSubscriptionActive(st) {
		a.gqlSubscriptionPane(c, t, tab, st, m, p)
		return
	}
	if tab.Sending {
		uikit.SendProgress(c, p)
		if tab.Resp == nil {
			ui.Column(c).Grow(1).FillWidth().Children(func() {
				uikit.SkeletonLines(c, p, 8)
			})
			return
		}
	}
	if tab.RespErr != "" {
		ui.Column(c).Grow(1).FillWidth().Center().Gap(12).Children(func() {
			ui.Text(c, tab.RespErr).FontSize(13).TextColor(p.Muted).MaxWidth(460).TextAlign(ui.Center)
			if b := uikit.PillButton(c, p, uikit.PillPrimary, a.msg().Retry, uikit.IconRetry).Cursor(ui.CursorPointer); b.Clicked() {
				a.send(tab)
			}
		})
		return
	}
	if tab.Resp == nil {
		ui.Column(c).Grow(1).Center().Gap(8).Children(func() {
			ui.Icon(c, uikit.IconSend).FontSize(28).TextColor(p.Faint)
			ui.Text(c, a.msg().NoResponseHint).FontSize(13).TextColor(p.Muted)
			ui.Text(c, a.msg().SendShortcutHint).FontSize(12).TextColor(p.Faint)
		})
		return
	}

	res := tab.Resp
	cls := core.ClassifyGraphQLResult(res)
	ui.Column(c).Grow(1).FillHeight().Background(p.BG).Children(func() {
		ui.Row(c).FillWidth().Wrap().Gap(8).Padding(6, 10).AlignItems(ui.Center).
			BorderWidth(1, 0, 0, 0).BorderColor(p.Hairline).Children(func() {
			ui.Text(c, fmt.Sprintf(m.StatusFmt, res.Status, formatDuration(res.Duration), formatByteSize(res.BodySize))).
				FontSize(12).Font("monospace").FontFeatures("tnum").TextColor(p.Text)
			a.gqlClassificationChips(c, cls, m, p)
			uikit.NativeSegmented(c, &st.resultSection, m.ResultData, m.ResultErrors, m.ResultRaw, m.ResultHeaders)
			ui.Row(c).Shrink(0).Gap(8).AlignItems(ui.Center).Margin(0, 0, 0, ui.Auto).Children(func() {
				if b := uikit.IconButton(c, p, uikit.IconCopy, a.msg().CopyResponse).Label(a.msg().CopyResponse).Cursor(ui.CursorPointer); b.Clicked() {
					a.copyToClipboard(c, string(res.Body))
				}
				if b := uikit.IconButton(c, p, uikit.IconFile, a.msg().SaveResponse).Label(a.msg().SaveResponse).Cursor(ui.CursorPointer); b.Clicked() {
					a.saveResponse(tab)
				}
			})
		})
		switch st.resultSection {
		case 0:
			a.gqlDataView(c, cls, m, p)
		case 1:
			a.gqlErrorsView(c, tab, cls, m, p)
		case 2:
			ui.Row(c).Padding(2, 10).Children(func() {
				uikit.NativeSegmented(c, &tab.RespRawIdx, a.msg().Pretty, a.msg().Raw, a.networkMsg().TreeMode)
			})
			a.responseBodyArea(c, t, tab, a.msg(), p)
		default:
			a.responseHeaders(c, t, tab.Resp, p)
		}
	})
}

func (a *App) gqlClassificationChips(c *ui.Context, cls core.GraphQLResult, m *gqlMessages, p uikit.Palette) {
	chip := func(text string, col ui.Color) {
		ui.Text(c, text).FontSize(11).TextColor(col).Padding(2, 6).
			Background(p.Sidebar).Radius(6).SingleLine()
	}
	if cls.InvalidJSON {
		chip(m.InvalidJSON, p.Danger)
	}
	if cls.HTTPError {
		chip(m.HTTPError, p.Danger)
	}
	switch {
	case cls.Partial():
		chip(m.PartialSuccess, p.Warning)
	case cls.HasErrors():
		chip(m.GraphQLErrors, p.Danger)
	}
	if cls.Truncated {
		chip(m.Truncated, p.Warning)
	}
	if !cls.DataPresent {
		chip(m.DataAbsent, p.Muted)
	} else if cls.DataNull {
		chip(m.DataNull, p.Muted)
	}
}

func (a *App) gqlDataView(c *ui.Context, cls core.GraphQLResult, m *gqlMessages, p uikit.Palette) {
	if !cls.DataPresent {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, m.DataAbsent).TextColor(p.Muted)
		})
		return
	}
	if cls.DataNull {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, m.DataNull).Font("monospace").TextColor(p.Muted)
		})
		return
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, cls.Data, "", "  "); err != nil {
		buf.Reset()
		buf.Write(cls.Data)
	}
	text := buf.String()
	note := ""
	if len(text) > gqlDataDisplayCap {
		text = text[:gqlDataDisplayCap]
		note = fmt.Sprintf(m.DataTooLargeFmt, formatByteSize(int64(len(cls.Data))))
	}
	ui.Column(c).Grow(1).FillWidth().Children(func() {
		if note != "" {
			ui.Text(c, note).FontSize(11).TextColor(p.Warning).Padding(2, 10)
		}
		ui.Scroll(c).Grow(1).FillWidth().Padding(6, 10).Children(func() {
			ui.Text(c, text).Font("monospace").FontSize(12).Selectable().TextColor(p.Text)
		})
	})
}

func (a *App) gqlErrorsView(c *ui.Context, tab *Tab, cls core.GraphQLResult, m *gqlMessages, p uikit.Palette) {
	if len(cls.Errors) == 0 {
		ui.Column(c).Grow(1).Center().Children(func() {
			ui.Text(c, m.NoErrors).TextColor(p.Muted)
		})
		return
	}
	red := tab.respRedactor
	mask := func(s string) string {
		if red == nil {
			return s
		}
		return red.RedactString(s)
	}
	shown := cls.Errors
	truncated := false
	if len(shown) > gqlErrorsDisplayCap {
		shown = shown[:gqlErrorsDisplayCap]
		truncated = true
	}
	ui.Scroll(c).Grow(1).FillWidth().Padding(8, 10).Children(func() {
		ui.Column(c).Gap(10).Children(func() {
			for i, e := range shown {
				ui.Text(c, fmt.Sprintf("%d. %s", i+1, mask(e.Message))).FontSize(12).Bold().
					TextColor(p.Danger).Selectable()
				if len(e.Path) > 0 {
					if raw, err := json.Marshal(e.Path); err == nil {
						ui.Text(c, m.ErrorPath+": "+mask(string(raw))).FontSize(11).Font("monospace").
							TextColor(p.Muted).Selectable()
					}
				}
				if len(e.Locations) > 0 {
					locs := make([]string, 0, len(e.Locations))
					for _, loc := range e.Locations {
						locs = append(locs, fmt.Sprintf("%d:%d", loc.Line, loc.Column))
					}
					ui.Text(c, m.ErrorLocations+": "+strings.Join(locs, ", ")).FontSize(11).
						Font("monospace").TextColor(p.Muted).Selectable()
				}
				if len(e.Extensions) > 0 {
					var pretty bytes.Buffer
					if json.Indent(&pretty, e.Extensions, "", "  ") == nil {
						ui.Text(c, m.ErrorExtensions+":\n"+mask(pretty.String())).FontSize(11).
							Font("monospace").TextColor(p.Muted).Selectable()
					}
				}
			}
			if truncated {
				ui.Text(c, fmt.Sprintf(m.ErrorsTruncatedFmt, gqlErrorsDisplayCap)).FontSize(11).TextColor(p.Warning)
			}
		})
	})
}
