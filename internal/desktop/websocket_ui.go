package desktop

import (
	"fmt"
	"os"
	"strings"
	"weak"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/stream"
	gows "gorequest/internal/websocket"
)

// websocket_ui.go renders a WebSocket request inside the same main workspace
// as HTTP requests: the URL row, environment selector (workspace header),
// Headers/Auth settings and the message timeline/send area all reuse the
// existing layout language. There is no separate modal: a WebSocket tab is a
// first-class tab beside HTTP and document tabs.

// wsTabView is the WebSocket workspace for one request tab.
func (a *App) wsTabView(c *ui.Context, t *ui.Theme, tab *Tab) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	m := a.websocketMsg()
	wm := a.wsWorkspaceMsg()
	p := uikit.PaletteFor(t)
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil {
		// A WebSocket tab always belongs to a project; keep a defensive path so
		// a detached tab can never panic the frame.
		ui.Column(c).Grow(1).FillHeight().Padding(12, 16).Gap(10).Children(func() {
			a.wsURLRow(c, t, tab, st, m, wm, p)
		})
		return
	}
	// §9 shortcuts: ⌘S saves the tab; ⌘↩ connects when disconnected.
	c.OnShortcut(ui.Cmd, ui.KeyS, func() { a.saveActiveWithFeedback() })
	c.OnShortcut(ui.Cmd, ui.KeyEnter, func() {
		if st.session == nil || st.session.State() != gows.StateOpen {
			a.wsTabConnect(tab)
		}
	})
	ui.Column(c).Grow(1).FillHeight().Padding(12, 16).Gap(10).Children(func() {
		a.wsURLRow(c, t, tab, st, m, wm, p)

		labels := []string{wm.SectionHeaders, wm.SectionAuth, wm.SectionSettings}
		badges := make([]string, len(labels))
		if n := kvCount(tab.HeaderRows); n > 0 {
			badges[0] = badgeCount(n)
		}
		uikit.UnderlineTabs(c, p, &st.section, labels, badges)

		// Reuse the request split preference so switching between HTTP and
		// WebSocket tabs keeps the same build/response geometry.
		splitHeight := float32(0)
		eff := ps.buildPct
		if ps != nil {
			splitHeight = ps.splitRef.Bounds(c).H
			eff = clampBuildPctForHeight(ps.buildPct, splitHeight)
			ps.layoutPct = eff
		}
		split := ui.SplitVerticalPercent(c, &ps.layoutPct, func() {
			uikit.TabContent(c, st.section, func(int) {
				ui.Column(c).Grow(1).FillWidth().Gap(4).Children(func() {
					a.wsConfigSection(c, t, tab, st, m, wm, p)
				})
			}).Grow(1)
		}, func() {
			ui.Column(c).Grow(1).FillHeight().Bind(&ps.respRef).Children(func() {
				a.wsMessagePane(c, t, tab, st, m, wm, p)
			})
		}).Grow(1).FillWidth().Bind(&ps.splitRef)
		if ps != nil {
			if moved := ps.layoutPct; moved != eff {
				ps.buildPct = applyBuildPctMove(ps.buildPct, eff, moved)
				a.mgr.cfg.setBuildPct(ps.buildPct)
			}
			if split.DoubleClicked() {
				ps.buildPct = defaultBuildPct
				ps.layoutPct = defaultBuildPct
				a.mgr.cfg.setBuildPct(defaultBuildPct)
			}
		}
	})
}

// wsURLRow is the WebSocket request line: protocol badge, endpoint input,
// live state badge and the connect/disconnect control.
func (a *App) wsURLRow(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, m *wsMessages, wm *wsWorkspaceMessages, p uikit.Palette) {
	open := st.session != nil && st.session.State() == gows.StateOpen
	ui.Column(c).Gap(4).Children(func() {
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			grp := ui.Row(c).Grow(1).Height(40).Radius(10).BorderWidth(1).
				BorderColor(wsStateBorderColor(st, t, p)).
				Background(p.BG).AlignItems(ui.Center).Clip().Label("websocket URL group")
			var urlInput ui.Element
			grp.Children(func() {
				ui.Row(c).Width(108).Height(40).Padding(0, 10).Gap(4).AlignItems(ui.Center).Children(func() {
					ui.Text(c, wm.TabBadge).Font("monospace").FontWeight(700).FontSize(13).
						TextColor(wsTabTagColor(p)).SingleLine()
				})
				ui.Box(c).Width(1).Height(40).Background(p.Faint).Label("protocol separator")
				urlBox := ui.Row(c).Grow(1).Padding(6, 10).AlignItems(ui.Center).
					Radius(8).Background(p.Sidebar)
				urlBox.Children(func() {
					urlInput = ui.TextInputBase(c, &tab.URL).Grow(1).Font("monospace").Label("websocket URL")
					urlInput.OnShortcut(0, ui.KeyEnter, func() { a.wsTabConnect(tab) })
					if urlInput.Changed() {
						tab.Dirty = true
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
			a.wsStateBadge(c, t, st, m, p)
			if open || st.busy {
				if uikit.ActionCursor(ui.Button(c, m.BtnDisconnect)).Clicked() {
					a.wsTabDisconnect(tab)
				}
			} else {
				if uikit.ActionCursor(uikit.PrimaryButton(c, m.BtnConnect)).Disabled(st.busy).Clicked() {
					a.wsTabConnect(tab)
				}
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, wm.ConnectHint).FontSize(11).TextColor(p.Faint).SingleLine()
			if st.notice != "" {
				ui.Text(c, st.notice).FontSize(11).TextColor(p.Muted).SingleLine().Grow(1)
			}
		})
	})
}

// wsConfigSection renders the selected configuration pane. Headers and Auth are
// exactly the HTTP editors, so the user's mental model and validation are the
// same across protocols.
func (a *App) wsConfigSection(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, m *wsMessages, wm *wsWorkspaceMessages, p uikit.Palette) {
	switch st.section {
	case 0:
		ui.Column(c).Grow(1).FillWidth().Children(func() {
			a.kvForm(c, t, tab, kvHeader)
		})
	case 1:
		ui.Column(c).Grow(1).FillWidth().Children(func() {
			a.authEditor(c, t, tab)
		})
	default:
		a.wsSettingsSection(c, t, tab, st, m, wm, p)
	}
}

// wsSettingsSection holds WebSocket-only options: offered subprotocols,
// permessage-deflate and the total session deadline (reusing Request.Timeout).
func (a *App) wsSettingsSection(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, m *wsMessages, wm *wsWorkspaceMessages, p uikit.Palette) {
	ui.Scroll(c).Grow(1).FillWidth().Children(func() {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, m.FieldSubprotocols).FontSize(12).TextColor(p.Muted)
			sub := uikit.ActionCursor(ui.TextInput(c, &st.subprotoDraft)).
				Placeholder("graphql-transport-ws")
			sub.FillWidth()
			if sub.Changed() {
				tab.Dirty = true
			}
			comp := uikit.ActionCursor(ui.Checkbox(c, &st.compress, m.CompressionLabel))
			if comp.Changed() {
				tab.Dirty = true
			}
			ui.Text(c, wm.TotalTimeoutLabel).FontSize(12).TextColor(p.Muted)
			timeout := uikit.ActionCursor(ui.TextInput(c, &tab.Timeout)).
				Placeholder("30s")
			timeout.Width(160)
			if timeout.Changed() {
				tab.Dirty = true
			}
			ui.Text(c, wm.TotalTimeoutHint).FontSize(11).TextColor(p.Faint)
		})
	})
}

// wsMessagePane is the lower half: live facts, timeline/detail and the send
// area. It is shown even before connecting so the layout never jumps.
func (a *App) wsMessagePane(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, m *wsMessages, wm *wsWorkspaceMessages, p uikit.Palette) {
	ui.Column(c).Grow(1).FillHeight().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, wm.MessagesTitle).FontSize(12).FontWeight(600).TextColor(p.Text)
			ui.Box(c).Grow(1)
			if st.notice != "" {
				ui.Text(c, st.notice).FontSize(11).TextColor(p.Muted).SingleLine()
			}
		})
		if st.session != nil {
			a.wsRenderFacts(c, t, st, m, p)
		}
		if st.confirmAsked {
			a.wsRenderConfirm(c, t, st, m, p)
		}
		if st.session == nil {
			ui.Column(c).Grow(1).FillWidth().Center().Children(func() {
				ui.Text(c, wm.NoSessionHint).FontSize(12).TextColor(p.Faint)
			})
		} else {
			ui.Row(c).Grow(1).FillWidth().Gap(8).Children(func() {
				ui.Column(c).Grow(1).Gap(6).Children(func() {
					ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
						uikit.SimpleSegmented(c, p, &st.dirFilterIdx, []string{m.DirFilterAll, m.DirFilterIn, m.DirFilterOut})
						fq := uikit.ActionCursor(ui.TextInput(c, &st.filterQuery))
						fq.FillWidth().Placeholder(m.FilterLabel)
					})
					ui.Box(c).Grow(1).FillWidth().Children(func() {
						ui.Scroll(c).Grow(1).FillWidth().Children(func() {
							a.wsRenderTimelineRows(c, t, st, m, p)
						})
					})
				})
				ui.Column(c).Width(320).Gap(6).Children(func() {
					ui.Text(c, m.DetailTitle).FontSize(12).FontWeight(600).TextColor(p.Text)
					a.wsRenderDetail(c, t, tab, st, m, p)
				})
			})
		}
		a.wsSendArea(c, t, tab, st, m, p)
	})
}

// wsStateBadge shows the session state as text + color (never color alone).
func (a *App) wsStateBadge(c *ui.Context, t *ui.Theme, st *wsTabState, m *wsMessages, p uikit.Palette) {
	state := gows.StateDisconnected
	if st != nil && st.session != nil {
		state = st.session.State()
		// A terminal failure is reported as ERROR rather than a plain CLOSED
		// badge; the underlying lifecycle state stays closed.
		if state == gows.StateClosed && st.session.Snapshot().LastErr != "" {
			state = gows.StateError
		}
	}
	label, color := m.StateDisconnected, p.Muted
	switch state {
	case gows.StateConnecting:
		label, color = m.StateConnecting, t.Warning
	case gows.StateOpen:
		label, color = m.StateOpen, t.Success
	case gows.StateClosing:
		label, color = m.StateClosing, t.Warning
	case gows.StateClosed:
		label, color = m.StateClosed, p.Muted
	case gows.StateError:
		label, color = m.StateError, t.Danger
	}
	ui.Text(c, "●").FontSize(12).TextColor(color)
	ui.Text(c, label).FontSize(12).FontWeight(600).TextColor(color)
}

// wsStateBorderColor colors the URL group by the live session state.
func wsStateBorderColor(st *wsTabState, t *ui.Theme, p uikit.Palette) ui.Color {
	if st == nil || st.session == nil {
		return p.Hairline
	}
	switch st.session.State() {
	case gows.StateOpen:
		return t.Success
	case gows.StateConnecting, gows.StateClosing:
		return t.Warning
	case gows.StateClosed:
		if st.session.Snapshot().LastErr != "" {
			return t.Danger
		}
	}
	return p.Hairline
}

// wsTabTagColor is the protocol tag color used by the URL row and tab strip.
func wsTabTagColor(p uikit.Palette) ui.Color { return p.Text }

// wsRenderFacts shows the negotiated session facts line: HTTP status,
// subprotocol, remote, proxy, TLS, deadlines and bounds.
func (a *App) wsRenderFacts(c *ui.Context, t *ui.Theme, st *wsTabState, m *wsMessages, p uikit.Palette) {
	if st == nil || st.session == nil {
		return
	}
	sn := st.session.Snapshot()
	ui.Column(c).FillWidth().Gap(4).Padding(8, 10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			ui.Text(c, m.FactsProxy+": "+wsNetLabel(sn)).FontSize(11).TextColor(p.Muted)
			if sn.Subprotocol != "" {
				ui.Text(c, "subprotocol: "+sn.Subprotocol).FontSize(11).Font("monospace").TextColor(p.Muted)
			}
		})
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			ui.Text(c, m.FactsDeadlines+": "+wsDeadlineLabel(sn)).FontSize(11).TextColor(p.Muted)
			ui.Text(c, fmt.Sprintf(m.BoundsNoteFmt, sn.StreamStats.Capacity, formatByteSize(sn.MaxMessage), sn.InboxCapacity)).FontSize(11).TextColor(p.Muted)
		})
		if sn.StreamStats.Overflowed > 0 || sn.StreamStats.SubscriberDrops > 0 {
			ui.Text(c, fmt.Sprintf(m.OverflowNoteFmt, sn.StreamStats.Overflowed, sn.StreamStats.SubscriberDrops)).FontSize(11).TextColor(t.Warning)
		}
		if sn.CloseCode != 0 || sn.End != gows.EndNone {
			ui.Text(c, wsEndLabel(sn)).FontSize(11).TextColor(t.Danger)
		}
	})
}

// wsRenderConfirm renders the target-policy confirmation step.
func (a *App) wsRenderConfirm(c *ui.Context, t *ui.Theme, st *wsTabState, m *wsMessages, p uikit.Palette) {
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(t.Warning).Children(func() {
		ui.Text(c, m.ConfirmRequired).FontSize(12).FontWeight(600).TextColor(t.Warning)
		if st.promptFacts != "" {
			ui.Text(c, st.promptFacts).FontSize(11).TextColor(p.Muted)
		}
		uikit.ActionCursor(ui.Checkbox(c, &st.confirmChecked, m.ConfirmCheckbox))
	})
}

// wsRenderTimelineRows draws the bounded event window with selection.
func (a *App) wsRenderTimelineRows(c *ui.Context, t *ui.Theme, st *wsTabState, m *wsMessages, p uikit.Palette) {
	if st == nil || st.session == nil {
		return
	}
	events := st.session.Events().Snapshot()
	events = wsFilterEvents(events, st)
	start := max(0, len(events)-wsTimelineLimit)
	events = events[start:]
	for i := range events {
		ev := events[i]
		selected := st.selectedSeq != 0 && st.selectedSeq == ev.Seq
		row := uikit.ActionCursor(ui.ButtonBase(c)).
			FillWidth().Height(26).Radius(6).Padding(0, 8).
			Background(p.BG)
		if selected {
			row.Background(p.Hover)
		}
		row.Children(func() {
			ui.Text(c, ev.Time.Format("15:04:05.000")).FontSize(11).Font("monospace").TextColor(p.Faint)
			ui.Text(c, wsDirectionGlyph(ev.Direction)).FontSize(11).TextColor(wsDirectionColor(ev.Direction, p)).Width(14)
			ui.Text(c, string(ev.Kind)).FontSize(11).Font("monospace").TextColor(wsKindColor(ev.Kind, t, p)).Width(64)
			preview := wsEventPreview(ev)
			ui.Text(c, preview).FontSize(11).Font("monospace").SingleLine().TextColor(p.Text).Grow(1)
			if ev.Sensitive {
				ui.Text(c, m.SensitiveBadge).FontSize(10).TextColor(t.Warning)
			}
		})
		if row.Clicked() {
			st.selectedSeq = ev.Seq
		}
	}
	if len(events) == 0 {
		ui.Text(c, "—").FontSize(11).TextColor(p.Faint)
	}
}

// wsRenderDetail renders the selected event with a text/hex payload view and
// the save action.
func (a *App) wsRenderDetail(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, m *wsMessages, p uikit.Palette) {
	if st == nil || st.session == nil || st.selectedSeq == 0 {
		ui.Text(c, m.DetailEmpty).FontSize(11).TextColor(p.Faint)
		return
	}
	item, ok := st.session.InboxFind(st.selectedSeq)
	if !ok {
		// Facts (state/info/error/close) have no retained payload; show their
		// summary and bounded preview from the event log instead.
		ev, found := wsFindEvent(st, st.selectedSeq)
		if !found {
			ui.Text(c, m.DetailEmpty).FontSize(11).TextColor(p.Faint)
			return
		}
		ui.Column(c).FillWidth().Gap(6).Children(func() {
			ui.Row(c).FillWidth().Gap(8).Children(func() {
				ui.Text(c, ev.Time.Format("15:04:05.000")).FontSize(11).Font("monospace").TextColor(p.Faint)
				ui.Text(c, string(ev.Kind)+" "+string(ev.Direction)).FontSize(11).Font("monospace").TextColor(p.Muted)
			})
			ui.Box(c).Height(1).Background(p.Hairline)
			ui.Scroll(c).MaxHeight(240).FillWidth().Children(func() {
				ui.Text(c, ev.Summary).FontSize(11).Font("monospace").TextColor(p.Text)
			})
		})
		return
	}
	ui.Column(c).FillWidth().Gap(6).Children(func() {
		ui.Row(c).FillWidth().Gap(8).Children(func() {
			ui.Text(c, item.Time.Format("15:04:05.000")).FontSize(11).Font("monospace").TextColor(p.Faint)
			ui.Text(c, string(item.Kind)+" "+string(item.Direction)).FontSize(11).Font("monospace").TextColor(p.Muted)
			ui.Box(c).Grow(1)
			save := uikit.ActionCursor(ui.Button(c, m.BtnSaveSelected))
			if save.Clicked() {
				a.wsSaveSelected(tab, item)
			}
		})
		ui.Box(c).Height(1).Background(p.Hairline)
		if item.IsText {
			ui.Scroll(c).MaxHeight(240).FillWidth().Children(func() {
				ui.Text(c, string(item.Data)).FontSize(11).Font("monospace").TextColor(p.Text)
			})
		} else {
			sha, hexDump := core.FormatBinaryPreview(item.Data, 512)
			ui.Column(c).Gap(4).Children(func() {
				ui.Text(c, "sha256: "+sha).FontSize(10).Font("monospace").TextColor(p.Muted)
				ui.Scroll(c).MaxHeight(220).FillWidth().Children(func() {
					ui.Text(c, hexDump).FontSize(11).Font("monospace").TextColor(p.Text)
				})
			})
		}
	})
}

// wsSendArea renders the text/binary composer and the session controls.
func (a *App) wsSendArea(c *ui.Context, t *ui.Theme, tab *Tab, st *wsTabState, m *wsMessages, p uikit.Palette) {
	open := st.session != nil && st.session.State() == gows.StateOpen
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			uikit.SimpleSegmented(c, p, &st.sendModeIdx, []string{m.SendModeText, m.SendModeBinary})
			if st.sendModeIdx == 0 {
				a.wsSendEditor(c, t, st, p)
			} else {
				hexIn := uikit.ActionCursor(ui.TextInput(c, &st.binaryDraft))
				hexIn.FillWidth().Placeholder(m.SendBinaryHint).Disabled(!open)
			}
			if uikit.ActionCursor(uikit.PrimaryButton(c, m.BtnSend).Disabled(!open)).Clicked() {
				a.wsTabSend(tab)
			}
			if uikit.ActionCursor(ui.Button(c, m.BtnPing).Disabled(!open)).Clicked() {
				a.wsTabPing(tab)
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			codeIn := uikit.ActionCursor(ui.TextInput(c, &st.closeCodeDraft))
			codeIn.Width(70).Disabled(!open)
			reasonIn := uikit.ActionCursor(ui.TextInput(c, &st.closeReasonDraft))
			reasonIn.FillWidth().Width(200).Disabled(!open)
			if uikit.ActionCursor(ui.Button(c, m.BtnCloseSession).Disabled(!open)).Clicked() {
				a.wsTabCloseSession(tab)
			}
			if uikit.ActionCursor(ui.Button(c, m.BtnClearView)).Clicked() {
				a.wsTabClear(tab)
			}
		})
	})
}

// wsSendEditor keeps a JSON-flavored editor for text messages.
func (a *App) wsSendEditor(c *ui.Context, t *ui.Theme, st *wsTabState, p uikit.Palette) {
	if st == nil {
		return
	}
	if st.sendEditor == nil {
		st.sendEditor = &editor.State{Buffer: editor.NewBuffer(st.textDraft), Theme: uikit.EditorTheme(t)}
		st.sendEditor.Buffer.SetLanguage(editor.LanguageNamed("json"))
		st.sendEditorGen = a.themeGeneration()
	}
	a.syncEditorTheme(st.sendEditor, &st.sendEditorGen, t)
	if got := st.sendEditor.Buffer.String(); got != st.textDraft {
		st.textDraft = got
	}
	st.sendEditor.ReadOnly = st.session == nil || st.session.State() != gows.StateOpen
	editor.View(c, st.sendEditor).Height(72).FillWidth()
}

// wsSaveSelected writes the selected message to a user-chosen local path (raw
// bytes, exactly like response saving), off the UI thread.
func (a *App) wsSaveSelected(tab *Tab, item gows.InboxItem) {
	st := tab.ws
	m := a.websocketMsg()
	if st == nil || st.session == nil {
		return
	}
	win := a.win
	var parent *mygo.Window
	if nw, ok := win.(nativeWindower); ok {
		parent = nw.nativeWindow()
	}
	data := append([]byte(nil), item.Data...)
	name := "ws-message.txt"
	if !item.IsText {
		name = "ws-message.bin"
	}
	key := weak.Make(a)
	gen := st.generation
	ps := tab.Owner
	go func() {
		if win == nil || win.IsDestroyed() {
			return
		}
		path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:      parent,
			Title:       m.BtnSaveSelected,
			DefaultPath: name,
			ButtonLabel: m.BtnSaveSelected,
		})
		if err != nil || path == "" {
			return
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			win.Update(func() {
				owner := key.Value()
				if owner == nil || owner.closed {
					return
				}
				if d := tab.ws; d != nil && d.generation == gen && (ps == nil || ps.indexOfTab(tab) >= 0) {
					d.notice = fmt.Sprintf(m.SaveFailedFmt, err)
				}
			})
			return
		}
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			if d := tab.ws; d != nil && d.generation == gen && (ps == nil || ps.indexOfTab(tab) >= 0) {
				d.notice = fmt.Sprintf(m.SavedToFmt, path)
			}
		})
	}()
}

// wsFindEvent returns the retained stream event with the given Seq.
func wsFindEvent(st *wsTabState, seq uint64) (stream.Event, bool) {
	if st == nil || st.session == nil || seq == 0 {
		return stream.Event{}, false
	}
	for _, ev := range st.session.Events().Snapshot() {
		if ev.Seq == seq {
			return ev, true
		}
	}
	return stream.Event{}, false
}

// wsFilterEvents applies the direction/kind/query filters.
func wsFilterEvents(events []stream.Event, st *wsTabState) []stream.Event {
	if st == nil {
		return events
	}
	q := strings.ToLower(strings.TrimSpace(st.filterQuery))
	var out []stream.Event
	for _, ev := range events {
		switch st.dirFilterIdx {
		case 1:
			if ev.Direction != stream.DirIn {
				continue
			}
		case 2:
			if ev.Direction != stream.DirOut {
				continue
			}
		}
		switch st.kindFilterIdx {
		case 1:
			if !(ev.Kind == stream.KindSend || ev.Kind == stream.KindReceive) {
				continue
			}
		case 2:
			if !(ev.Kind == stream.KindPing || ev.Kind == stream.KindPong || ev.Kind == stream.KindClose) {
				continue
			}
		case 3:
			if !(ev.Kind == stream.KindState || ev.Kind == stream.KindInfo || ev.Kind == stream.KindError) {
				continue
			}
		}
		if q != "" {
			hay := ev.Summary + " " + string(ev.Kind) + " " + fmt.Sprint(ev.Seq)
			if ev.IsText {
				hay += " " + string(ev.Payload)
			}
			if !strings.Contains(strings.ToLower(hay), q) {
				continue
			}
		}
		out = append(out, ev)
	}
	return out
}

// wsEventPreview renders a single-line payload preview.
func wsEventPreview(ev stream.Event) string {
	if ev.Kind == stream.KindClose {
		return fmt.Sprintf("code %d %s", ev.Code, ev.Reason)
	}
	body := ev.Summary
	if len(ev.Payload) > 0 && ev.IsText {
		body = strings.ReplaceAll(string(ev.Payload), "\n", " ")
		if len(body) > 110 {
			body = body[:110] + "…"
		}
	}
	return body
}

// wsDirectionGlyph/color render direction without relying on color alone.
func wsDirectionGlyph(d stream.Direction) string {
	switch d {
	case stream.DirIn:
		return "↓"
	case stream.DirOut:
		return "↑"
	default:
		return "·"
	}
}

func wsDirectionColor(d stream.Direction, p uikit.Palette) (out ui.Color) {
	switch d {
	case stream.DirIn:
		return p.Text
	case stream.DirOut:
		return p.Muted
	default:
		return p.Faint
	}
}

func wsKindColor(k stream.Kind, t *ui.Theme, p uikit.Palette) ui.Color {
	switch k {
	case stream.KindError:
		return t.Danger
	case stream.KindPing, stream.KindPong:
		return t.Warning
	case stream.KindState, stream.KindInfo:
		return p.Faint
	case stream.KindClose:
		return t.Danger
	default:
		return p.Text
	}
}

// wsNetLabel renders the proxy/remote line.
func wsNetLabel(sn gows.Snapshot) string {
	parts := []string{}
	if sn.ProxyUsed != "" {
		parts = append(parts, "proxy "+sn.ProxyUsed)
	} else {
		parts = append(parts, "direct")
	}
	if sn.RemoteAddr != "" {
		parts = append(parts, sn.RemoteAddr)
	}
	if sn.TLS != nil {
		v := sn.TLS.Version
		if sn.TLS.InsecureUsed {
			v += " (insecure!)"
		}
		parts = append(parts, "tls "+v)
	}
	if sn.Subprotocol != "" {
		parts = append(parts, "subprotocol "+sn.Subprotocol)
	}
	return strings.Join(parts, " · ")
}

// wsDeadlineLabel renders the effective deadline facts.
func wsDeadlineLabel(sn gows.Snapshot) string {
	total := "∞"
	if sn.TotalDeadline > 0 {
		total = sn.TotalDeadline.String()
	}
	return fmt.Sprintf("connect %s · handshake %s · message %s · total %s",
		sn.ConnectDeadline, sn.HandshakeDeadline, sn.MessageDeadline, total)
}

// wsEndLabel renders termination facts.
func wsEndLabel(sn gows.Snapshot) string {
	parts := []string{string(sn.End)}
	if sn.CloseCode != 0 {
		parts = append(parts, fmt.Sprintf("close code %d", sn.CloseCode))
	}
	if sn.CloseReason != "" {
		parts = append(parts, sn.CloseReason)
	}
	if sn.LastErr != "" {
		parts = append(parts, sn.LastErr)
	}
	return strings.Join(parts, " · ")
}
