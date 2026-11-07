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

// WebSocketModals renders the manual WebSocket session modal when open.
// Launch entry: project menu (BuildWebSocketProjectMenuItems).
func (a *App) WebSocketModals(c *ui.Context, t *ui.Theme) {
	s := a.wsState(false)
	if s == nil || !s.open {
		return
	}
	m := a.websocketMsg()
	p := uikit.PaletteFor(t)
	winW, winH := c.Size()
	panelW := max(float32(420), min(float32(960), float32(winW)-48))
	panelH := max(float32(360), min(float32(820), float32(winH)-64))

	ui.Modal(c, &s.open, func() {
		ui.Column(c).Width(panelW).Height(panelH).Gap(10).Padding(18).Background(p.BG).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
			a.wsRenderHeader(c, t, s, m, p)
			if s.confirmAsked {
				a.wsRenderConfirm(c, t, s, m, p)
			}
			if s.session == nil {
				a.wsRenderSetup(c, t, s, m, p)
			} else {
				// A closed/failed session keeps its timeline and facts
				// visible; "Clear View" returns to the setup form.
				a.wsRenderSession(c, t, s, m, p)
			}
			if s.notice != "" {
				ui.Text(c, s.notice).FontSize(12).TextColor(p.Muted)
			}
		})
	})
	if modalDismissed(s) {
		a.wsTerminateAndClose()
	}
	// Modal's ui.Modal dismissal path: ui.Modal toggles open=false itself.
}

// modalDismissed is a tiny helper keeping the dismissal check in one place.
func modalDismissed(s *wsDialog) bool { return s != nil && !s.open }

// WebSocketView wraps a.View(c) and a.WebSocketModals(c, c.Theme()) so
// headless UI tests can exercise the session dialog while shared files stay on
// the common baseline.
func (a *App) WebSocketView(c *ui.Context) {
	a.View(c)
	a.WebSocketModals(c, c.Theme())
}

// wsRenderHeader draws the title, live state badge and connection facts.
func (a *App) wsRenderHeader(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	ui.Row(c).FillWidth().Gap(10).AlignItems(ui.Center).Children(func() {
		ui.Text(c, m.Title).FontSize(16).FontWeight(700).TextColor(p.Text)
		a.wsStateBadge(c, t, s, m, p)
		ui.Box(c).Grow(1)
		btn := uikit.ActionCursor(ui.Button(c, m.BtnDone))
		if btn.Clicked() {
			a.wsTerminateAndClose()
		}
	})
	if s.session != nil {
		a.wsRenderFacts(c, t, s, m, p)
	}
}

// wsStateBadge shows the session state as text + color (never color alone).
func (a *App) wsStateBadge(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	state := gows.StateDisconnected
	if s.session != nil {
		state = s.session.State()
		// A terminal failure is reported as ERROR rather than a plain
		// CLOSED badge; the underlying lifecycle state stays closed.
		if state == gows.StateClosed && s.session.Snapshot().LastErr != "" {
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

// wsRenderFacts shows the negotiated session facts line: HTTP status,
// subprotocol, remote, proxy, TLS, deadlines and bounds.
func (a *App) wsRenderFacts(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	sn := s.session.Snapshot()
	ui.Column(c).FillWidth().Gap(4).Padding(8, 10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			ui.Text(c, m.FactsProxy+": "+wsNetLabel(sn)).FontSize(11).TextColor(p.Muted)
			if sn.Subprotocol != "" {
				ui.Text(c, "subprotocol: "+sn.Subprotocol).FontSize(11).Font("monospace").TextColor(p.Muted)
			}
		})
		ui.Row(c).FillWidth().Gap(12).Children(func() {
			ui.Text(c, m.FactsDeadlines+": "+wsDeadlineLabel(sn)).FontSize(11).TextColor(p.Muted)
			ui.Text(c, fmt.Sprintf(m.BoundsNoteFmt, sn.StreamStats.Capacity, sn.MaxMessage, sn.InboxCapacity)).FontSize(11).TextColor(p.Muted)
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
func (a *App) wsRenderConfirm(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	ui.Column(c).FillWidth().Gap(6).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(t.Warning).Children(func() {
		ui.Text(c, m.ConfirmRequired).FontSize(12).FontWeight(600).TextColor(t.Warning)
		if s.promptFacts != "" {
			ui.Text(c, s.promptFacts).FontSize(11).TextColor(p.Muted)
		}
		uikit.ActionCursor(ui.Checkbox(c, &s.confirmChecked, m.ConfirmCheckbox))
	})
}

// wsRenderSetup renders the connection drafts and Connect action.
func (a *App) wsRenderSetup(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	ui.Column(c).FillWidth().Gap(8).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.FieldURL).FontSize(12).TextColor(p.Muted).Width(150)
			uikit.ActionCursor(ui.TextInput(c, &s.urlDraft)).Placeholder("wss://host/path").Disabled(s.busy)
			{
				tx := ui.TextInput(c, &s.totalDraft)
				tx.Width(90).Disabled(s.busy)
				tx.Tooltip(m.FieldTotalTimeout)
				uikit.ActionCursor(tx)
			}
			ui.Text(c, m.FieldTotalTimeout).FontSize(11).TextColor(p.Faint)
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, m.FieldSubprotocols).FontSize(12).TextColor(p.Muted).Width(150)
			uikit.ActionCursor(ui.TextInput(c, &s.subprotoDraft)).Placeholder("graphql-transport-ws").Disabled(s.busy)
		})
		ui.Column(c).FillWidth().Gap(4).Children(func() {
			ui.Text(c, m.FieldHeaders+" ("+m.FieldHeadersHint+")").FontSize(12).TextColor(p.Muted)
			a.wsHeadersEditor(c, t, s, p)
			uikit.ActionCursor(ui.Checkbox(c, &s.compress, m.CompressionLabel).Disabled(s.busy))
		})
		ui.Row(c).FillWidth().Gap(8).Justify(ui.End).Children(func() {
			connect := uikit.ActionCursor(ui.PrimaryButton(c, m.BtnConnect)).Disabled(s.busy)
			if connect.Clicked() {
				a.wsConnect()
			}
		})
	})
}

// wsHeadersEditor keeps a small editor for header lines.
func (a *App) wsHeadersEditor(c *ui.Context, t *ui.Theme, s *wsDialog, p uikit.Palette) {
	if s.headersEditor == nil {
		s.headersEditor = &editor.State{Buffer: editor.NewBuffer(s.headersDraft), Theme: uikit.EditorTheme(t)}
		s.headersEditor.Buffer.SetLanguage(editor.LanguageNamed("text"))
	}
	if got := s.headersEditor.Buffer.String(); got != s.headersDraft {
		s.headersDraft = got
	}
	s.headersEditor.ReadOnly = s.busy
	editor.View(c, s.headersEditor).Height(90).FillWidth()
}

// wsRenderSession renders the live session: timeline, filters, detail and
// the send/close actions.
func (a *App) wsRenderSession(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	ui.Row(c).FillWidth().Gap(8).Children(func() {
		// 1) Timeline (left).
		ui.Column(c).Grow(1).Gap(6).Children(func() {
			ui.Row(c).FillWidth().Gap(6).AlignItems(ui.Center).Children(func() {
				seg, changed := uikit.SimpleSegmented(c, p, &s.dirFilterIdx, []string{m.DirFilterAll, m.DirFilterIn, m.DirFilterOut})
				_ = seg
				_ = changed
				fq := uikit.ActionCursor(ui.TextInput(c, &s.filterQuery))
				fq.FillWidth().Placeholder(m.FilterLabel)
			})
			ui.Box(c).Grow(1).FillWidth().Children(func() {
				ui.Scroll(c).Grow(1).FillWidth().Children(func() {
					a.wsRenderTimelineRows(c, t, s, m, p)
				})
			})
		})
		// 2) Detail (right).
		ui.Column(c).Width(320).Gap(6).Children(func() {
			ui.Text(c, m.DetailTitle).FontSize(12).FontWeight(600).TextColor(p.Text)
			a.wsRenderDetail(c, t, s, m, p)
		})
	})
	a.wsRenderActions(c, t, s, m, p)
}

// wsRenderTimelineRows draws the bounded event window with selection.
func (a *App) wsRenderTimelineRows(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	if s.session == nil {
		return
	}
	events := s.session.Events().Snapshot()
	events = wsFilterEvents(events, s)
	start := max(0, len(events)-wsTimelineLimit)
	events = events[start:]
	for i := range events {
		ev := events[i]
		selected := s.selectedSeq != 0 && s.selectedSeq == ev.Seq
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
			s.selectedSeq = ev.Seq
		}
	}
	if len(events) == 0 {
		ui.Text(c, "—").FontSize(11).TextColor(p.Faint)
	}
}

// wsRenderDetail renders the selected event with a text/hex payload view and
// the save action.
func (a *App) wsRenderDetail(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	if s.session == nil || s.selectedSeq == 0 {
		ui.Text(c, m.DetailEmpty).FontSize(11).TextColor(p.Faint)
		return
	}
	item, ok := s.session.InboxFind(s.selectedSeq)
	if !ok {
		// Facts (state/info/error/close) have no retained payload; show their
		// summary and bounded preview from the event log instead.
		ev, found := wsFindEvent(s, s.selectedSeq)
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
				a.wsSaveSelected(item)
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

// wsRenderActions renders the send box and close controls.
func (a *App) wsRenderActions(c *ui.Context, t *ui.Theme, s *wsDialog, m *wsMessages, p uikit.Palette) {
	open := s.session != nil && s.session.State() == gows.StateOpen
	ui.Column(c).FillWidth().Gap(8).Padding(10).Background(p.Sidebar).Radius(8).BorderWidth(1).BorderColor(p.Hairline).Children(func() {
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			seg, changed := uikit.SimpleSegmented(c, p, &s.sendModeIdx, []string{m.SendModeText, m.SendModeBinary})
			_ = seg
			_ = changed
			if s.sendModeIdx == 0 {
				a.wsSendEditor(c, t, s, p)
			} else {
				hexIn := uikit.ActionCursor(ui.TextInput(c, &s.binaryDraft))
				hexIn.FillWidth().Placeholder(m.SendBinaryHint).Disabled(!open)
			}
			sendBtn := uikit.ActionCursor(ui.PrimaryButton(c, m.BtnSend).Disabled(!open))
			if sendBtn.Clicked() {
				a.wsSend()
			}
			pingBtn := uikit.ActionCursor(ui.Button(c, m.BtnPing).Disabled(!open))
			if pingBtn.Clicked() {
				a.wsPing()
			}
			disconnectBtn := uikit.ActionCursor(ui.Button(c, m.BtnDisconnect).Disabled(!open))
			if disconnectBtn.Clicked() {
				a.wsDisconnect()
			}
		})
		ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
			codeIn := uikit.ActionCursor(ui.TextInput(c, &s.closeCodeDraft))
			codeIn.Width(70).Disabled(!open)
			reasonIn := uikit.ActionCursor(ui.TextInput(c, &s.closeReasonDraft))
			reasonIn.FillWidth().Width(200).Disabled(!open)
			closeBtn := uikit.ActionCursor(ui.Button(c, m.BtnCloseSession).Disabled(!open))
			if closeBtn.Clicked() {
				a.wsCloseSession()
			}
			clearBtn := uikit.ActionCursor(ui.Button(c, m.BtnClearView))
			if clearBtn.Clicked() {
				a.wsClearView()
			}
		})
	})
}

// wsSendEditor keeps a JSON-flavored editor for text messages.
func (a *App) wsSendEditor(c *ui.Context, t *ui.Theme, s *wsDialog, p uikit.Palette) {
	if s.sendEditor == nil {
		s.sendEditor = &editor.State{Buffer: editor.NewBuffer(s.textDraft), Theme: uikit.EditorTheme(t)}
		s.sendEditor.Buffer.SetLanguage(editor.LanguageNamed("json"))
	}
	if got := s.sendEditor.Buffer.String(); got != s.textDraft {
		s.textDraft = got
	}
	s.sendEditor.ReadOnly = s.session == nil || s.session.State() != gows.StateOpen
	editor.View(c, s.sendEditor).Height(84).FillWidth()
}

// wsSaveSelected writes the selected message to a user-chosen local path
// (raw bytes, exactly like response saving), off the UI thread.
func (a *App) wsSaveSelected(item gows.InboxItem) {
	s := a.wsState(false)
	m := a.websocketMsg()
	if s == nil || s.session == nil {
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
				if d := owner.wsState(false); d != nil {
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
			if d := owner.wsState(false); d != nil {
				d.notice = fmt.Sprintf(m.SavedToFmt, path)
			}
		})
	}()
}

// wsFindEvent returns the retained stream event with the given Seq.
func wsFindEvent(s *wsDialog, seq uint64) (stream.Event, bool) {
	if s == nil || s.session == nil || seq == 0 {
		return stream.Event{}, false
	}
	for _, ev := range s.session.Events().Snapshot() {
		if ev.Seq == seq {
			return ev, true
		}
	}
	return stream.Event{}, false
}

// wsFilterEvents applies the direction/kind/query filters.
func wsFilterEvents(events []stream.Event, s *wsDialog) []stream.Event {
	q := strings.ToLower(strings.TrimSpace(s.filterQuery))
	var out []stream.Event
	for _, ev := range events {
		switch s.dirFilterIdx {
		case 1:
			if ev.Direction != stream.DirIn {
				continue
			}
		case 2:
			if ev.Direction != stream.DirOut {
				continue
			}
		}
		switch s.kindFilterIdx {
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

// wsInboxCap exposes the dialog's inbox capacity for the bounds note.
func wsInboxCap(s *wsDialog) int {
	if s.session == nil {
		return gows.DefaultInboxCapacity
	}
	return s.session.Snapshot().InboxCapacity
}
