package desktop

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/stream"
	gows "gorequest/internal/websocket"
)

// websocket_state.go owns the manual WebSocket session dialog's per-window
// state and everything that needs to run off the UI thread's back: template
// resolution, the connect attempt, the live event pump and selected-message
// saving. It never holds a strong App pointer in a background goroutine —
// callbacks re-enter through weak.Pointer[App] + generation checks, copying
// the runner/mailbox lifecycle rules.

const (
	// wsTimelineLimit bounds how many events one modal frame renders.
	wsTimelineLimit = 200
	// wsEventSubBuffer bounds the live event subscription channel. A frozen
	// window drops deliveries here and re-syncs from the log cursor on the
	// next frame — the loss is observable in the log Stats.
	wsEventSubBuffer = 64
)

// wsDialog is the per-window manual WebSocket session state. It is created
// lazily and looked up through a weak-pointer table exactly like the runner
// state, so no global strong reference can outlive its window. The dialog
// allows ONE live session at a time; a reconnect requires closing the modal
// (which terminates the connection) and reopening it.
type wsDialog struct {
	open bool
	busy bool

	// Drafts (persist while the modal opens/closes within one window).
	urlDraft         string
	subprotoDraft    string
	headersDraft     string
	totalDraft       string
	compress         bool
	confirmChecked   bool
	confirmAsked     bool
	sendModeIdx      int // 0 = text, 1 = binary
	textDraft        string
	binaryDraft      string
	closeCodeDraft   string
	closeReasonDraft string
	dirFilterIdx     int // 0 = all, 1 = in, 2 = out
	kindFilterIdx    int // 0 = all payloads, 1 = messages only, 2 = control, 3 = facts
	filterQuery      string
	selectedSeq      uint64

	// headersEditor and sendEditor hold the multi-line drafts (editor.State
	// is heavy, so it is created lazily and kept per window).
	headersEditor *editor.State
	sendEditor    *editor.State

	// Session lifecycle. redactor is per session-generation; session may be
	// non-nil with a terminal state (facts remain visible until cleared).
	session     *gows.Session
	redactor    *core.Redactor
	generation  uint64
	mailbox     *wsMailbox
	promptFacts string

	notice string
}

var wsDialogs = struct {
	sync.Mutex
	byWindow map[weak.Pointer[App]]*wsDialog
}{byWindow: map[weak.Pointer[App]]*wsDialog{}}

// wsState returns (creating when create) this window's dialog state.
func (a *App) wsState(create bool) *wsDialog {
	if a == nil {
		return nil
	}
	key := weak.Make(a)
	wsDialogs.Lock()
	defer wsDialogs.Unlock()
	if s := wsDialogs.byWindow[key]; s != nil {
		return s
	}
	if !create {
		return nil
	}
	s := &wsDialog{
		sendModeIdx:    0,
		dirFilterIdx:   0,
		kindFilterIdx:  0,
		closeCodeDraft: "1000",
	}
	wsDialogs.byWindow[key] = s
	runtime.AddCleanup(a, func(k weak.Pointer[App]) {
		wsDialogs.Lock()
		delete(wsDialogs.byWindow, k)
		wsDialogs.Unlock()
	}, key)
	if a.win != nil {
		// Closing the workspace window terminates any live session —
		// no socket outlives its window.
		a.win.OnClosed(func() {
			s.generation++
			a.wsTeardown(s)
		})
	}
	return s
}

// wsTeardown stops the pump and terminates the session (idempotent).
func (a *App) wsTeardown(s *wsDialog) {
	if s == nil {
		return
	}
	if s.mailbox != nil {
		s.mailbox.stop()
		s.mailbox = nil
	}
	if s.session != nil {
		s.session.Terminate()
	}
}

// OpenWebSocketSession is the menu entry target: it opens the session modal
// for the current project. Sessions never auto-connect.
func (a *App) OpenWebSocketSession() {
	if a == nil {
		return
	}
	if a.ps == nil {
		a.status = a.websocketMsg().NoProject
		return
	}
	s := a.wsState(true)
	s.notice = ""
	s.open = true
}

// BuildWebSocketProjectMenuItems adds the session entry to the project menu.
func (a *App) BuildWebSocketProjectMenuItems(mm *ui.Menu) {
	if mm == nil {
		return
	}
	if mm.Item(a.websocketMsg().MenuEntry).Chosen() {
		a.OpenWebSocketSession()
	}
}

// wsMailbox is the bounded live-event pump driving window redraws. It never
// invokes the UI directly from the receive goroutine: every delivery only
// schedules a single Window.Update (coalesced flag), and the update itself
// re-enters through the weak window key.
type wsMailbox struct {
	post func(func())

	mu        sync.Mutex
	scheduled bool
	stopCh    chan struct{}
}

func (m *wsMailbox) schedule() {
	m.mu.Lock()
	already := m.scheduled
	m.scheduled = true
	m.mu.Unlock()
	if already {
		return
	}
	// Window.Update already runs this on the UI thread and schedules the
	// next frame; no nested Update is needed.
	m.post(func() {
		m.mu.Lock()
		m.scheduled = false
		m.mu.Unlock()
	})
}

func (m *wsMailbox) stop() {
	select {
	case <-m.stopCh:
		// already stopped
	default:
		close(m.stopCh)
	}
}

// wsPump subscribes to the session log and schedules redraws. It exits when
// the mailbox stops or the subscription channel closes.
func (a *App) wsPump(sess *gows.Session) *wsMailbox {
	if sess == nil || a.win == nil {
		return nil
	}
	ch := make(chan stream.Event, wsEventSubBuffer)
	unsubscribe := sess.Events().Subscribe(ch)
	m := &wsMailbox{
		post:   a.win.Update,
		stopCh: make(chan struct{}),
	}
	go func() {
		defer unsubscribe()
		for {
			select {
			case <-m.stopCh:
				return
			case _, ok := <-ch:
				if !ok {
					return
				}
				m.schedule()
			}
		}
	}()
	return m
}

// wsHeaders parses the header draft (one "Name: Value" per line). Values are
// returned verbatim — template resolution happens in wsConnect.
func wsHeaders(raw string) ([][2]string, error) {
	var pairs [][2]string
	for _, line := range strings.Split(raw, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok {
			return nil, fmt.Errorf("header line %q is missing ':'", trimmed)
		}
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("header line %q has an empty name", trimmed)
		}
		pairs = append(pairs, [2]string{strings.TrimSpace(name), strings.TrimSpace(value)})
	}
	return pairs, nil
}

// wsResolveTemplate resolves one draft string against project variables and
// secrets. Missing variables/secrets fail loudly — a template never silently
// becomes empty (§ 规范: 重放在目标环境重新解析 Secret).
func wsResolveTemplate(ps *projectState, resolver *core.Resolver, label, raw string, red *core.Redactor) (string, bool, error) {
	if !strings.Contains(raw, "{{") {
		return raw, false, nil
	}
	tv, err := resolver.ResolveTracked(raw)
	if err != nil {
		return "", false, fmt.Errorf("%s: %w", label, err)
	}
	if tv.Sensitive && red != nil {
		red.RegisterSecret(tv.Value)
	}
	return tv.Value, tv.Sensitive, nil
}

// wsConnect validates drafts, resolves templates, runs the target-policy
// preflight (showing/no the confirm step) and starts the connect attempt off
// the UI thread. It is the ONLY path to a session — no auto anything.
func (a *App) wsConnect() {
	s := a.wsState(false)
	m := a.websocketMsg()
	if s == nil || !s.open || s.busy {
		return
	}
	if s.session != nil && s.session.State() == gows.StateOpen {
		s.notice = m.Busy
		return
	}
	ps := a.ps
	if ps == nil || ps.project == nil {
		s.notice = m.NoProject
		return
	}
	red := core.NewRedactor()

	// Header drafts must parse before any template resolution.
	rawPairs, err := wsHeaders(s.headersDraft)
	if err != nil {
		s.notice = err.Error()
		return
	}

	entry := &core.RequestEntry{Request: &core.Request{}}
	vars, _, err := ps.project.ResolveVars(entry, ps.currentEnv())
	if err != nil {
		s.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	resolver := core.NewResolver(vars, ps.secrets)

	url, _, err := wsResolveTemplate(ps, resolver, m.FieldURL, strings.TrimSpace(s.urlDraft), red)
	if err != nil {
		s.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	var pairs [][2]string
	hasCreds := false
	for _, p := range rawPairs {
		name := strings.TrimSpace(p[0])
		value, sensitive, err := wsResolveTemplate(ps, resolver, name, p[1], red)
		if err != nil {
			s.notice = fmt.Sprintf(m.VarsResolveFailed, err)
			return
		}
		pairs = append(pairs, [2]string{name, value})
		if sensitive || gowsHeaderCarriesCredentials(name) {
			hasCreds = true
		}
	}

	var subprotocols []string
	for _, sp := range strings.Split(s.subprotoDraft, ",") {
		if sp = strings.TrimSpace(sp); sp != "" {
			subprotocols = append(subprotocols, sp)
		}
	}

	var total time.Duration
	if t := strings.TrimSpace(s.totalDraft); t != "" {
		secs, err := strconv.ParseFloat(t, 64)
		if err != nil || secs < 0 {
			s.notice = m.FieldTotalTimeout + ": " + t
			return
		}
		if secs > 0 {
			total = time.Duration(secs * float64(time.Second))
		}
	}

	// Device network policy snapshot happens on the UI thread (same rule as
	// the runner): values are copied into the options that travel to the
	// connect goroutine; no App/Tab pointer crosses the boundary. A nil tab
	// uses the per-project network state, not a request tab's overrides.
	net := a.buildExecuteOptionsForTab(ps, nil, nil, nil, red)

	opts := gows.Options{
		URL:               url,
		Headers:           pairs,
		Subprotocols:      subprotocols,
		EnableCompression: s.compress,
		Deadlines: stream.DeadlinePolicy{
			Connect:   net.Timeouts.DialTimeout,
			Handshake: net.Timeouts.TLSHandshakeTimeout,
			Message:   gows.DefaultMessageTimeout,
			Total:     total,
		},
		Proxy:           net.Proxy,
		TLS:             net.TLS,
		TargetPolicy:    net.TargetPolicy,
		HasTargetPolicy: net.HasTargetPolicy,
		EnvID:           net.EnvID,
		Interactive:     true,
		Confirmed:       s.confirmChecked,
		HasCredentials:  hasCreds,
		Secrets:         net.Secrets,
		LocalFiles:      net.LocalFiles,
		ProjectRoot:     net.ProjectRoot,
		Redactor:        red,
	}

	// Preflight the exact decision so the confirm step is visible BEFORE an
	// attempt; Connect re-evaluates and enforces the same check.
	if decision, perr := gows.PromptRequiredBy(&opts); perr == nil && decision != nil &&
		decision.Outcome == core.DecisionConfirmRequired && !s.confirmChecked {
		s.confirmAsked = true
		s.notice = m.ConfirmNeeded
		s.promptFacts = strings.Join(decision.Reasons, "; ")
		return
	}

	// Confirmation is a one-time approval of THIS exact target: consume it so
	// a later reconnect cannot inherit it silently.
	s.confirmChecked = false

	ctx, cancel := context.WithCancel(context.Background())
	s.busy = true
	s.notice = fmt.Sprintf(m.Connecting, red.RedactString(opts.URL))
	s.generation++
	gen := s.generation
	key := weak.Make(a)
	win := a.win
	s.redactor = red

	go func() {
		sess, cerr := gows.Connect(ctx, opts)
		cancel()
		if win == nil || win.IsDestroyed() {
			// The window vanished mid-attempt: never leave a live socket
			// behind just because no UI can observe it.
			if sess != nil {
				sess.Terminate()
			}
			return
		}
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				if sess != nil {
					sess.Terminate()
				}
				return
			}
			d := owner.wsState(false)
			if d == nil || d.generation != gen {
				// A newer attempt or teardown won; keep this one's session
				// terminating in the background without touching the UI.
				if sess != nil {
					sess.Terminate()
				}
				return
			}
			d.busy = false
			if d.session != nil {
				d.session.Terminate()
			}
			owner.wsStopMailbox(d)
			d.session = sess
			if cerr != nil {
				if errors.Is(cerr, gows.ErrConfirmRequired) {
					d.confirmAsked = true
					d.notice = owner.websocketMsg().ConfirmNeeded
				} else {
					lastErr := sess.Snapshot().LastErr
					if lastErr == "" {
						lastErr = cerr.Error()
					}
					d.notice = fmt.Sprintf(owner.websocketMsg().ConnectFailed, lastErr)
				}
			} else {
				d.confirmAsked = false
				d.promptFacts = ""
				d.notice = owner.websocketMsg().Connected
				d.mailbox = owner.wsPump(sess)
			}
		})
	}()
}

// wsStopMailbox stops any running event pump (UI thread only).
func (a *App) wsStopMailbox(s *wsDialog) {
	if s == nil {
		return
	}
	if s.mailbox != nil {
		s.mailbox.stop()
		s.mailbox = nil
	}
}

// wsSend resolves the send draft and performs one bounded send.
func (a *App) wsSend() {
	s := a.wsState(false)
	m := a.websocketMsg()
	if s == nil || !s.open || s.session == nil {
		return
	}
	ps := a.ps
	if ps == nil || ps.project == nil {
		s.notice = m.NoProject
		return
	}
	sess := s.session
	if sess.State() != gows.StateOpen {
		s.notice = m.SendNotOpen
		return
	}
	red := core.NewRedactor()
	entry := &core.RequestEntry{Request: &core.Request{}}
	vars, _, err := ps.project.ResolveVars(entry, ps.currentEnv())
	if err != nil {
		s.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	resolver := core.NewResolver(vars, ps.secrets)

	if s.sendModeIdx == 0 {
		raw := s.textDraft
		if s.sendEditor != nil {
			raw = s.sendEditor.Buffer.String()
		}
		value, sensitive, err := wsResolveTemplate(ps, resolver, m.BtnSend, raw, red)
		if err != nil {
			s.notice = fmt.Sprintf(m.VarsResolveFailed, err)
			return
		}
		if sensitive {
			// The session redacts its retained copy at append time; register
			// the send-time secret through the session's locked path first.
			sess.RegisterSecret(value)
		}
		if len(value) > int(sess.Snapshot().MaxMessage) {
			s.notice = fmt.Sprintf("message exceeds the session limit (%d bytes)", sess.Snapshot().MaxMessage)
			return
		}
		if err := sess.SendText(context.Background(), value, sensitive); err != nil {
			if !errors.Is(err, gows.ErrSessionClosed) {
				s.notice = fmt.Sprintf(m.SendFailedFmt, err.Error())
			} else {
				s.notice = m.SendNotOpen
			}
			return
		}
		s.notice = m.SendDone
		return
	}

	rawHex := strings.TrimSpace(s.binaryDraft)
	raw := strings.ReplaceAll(strings.ReplaceAll(rawHex, " ", ""), "\t", "")
	binarySensitive := false
	if strings.Contains(raw, "{{") {
		value, sensitive, err := wsResolveTemplate(ps, resolver, m.BtnSend, raw, red)
		if err != nil {
			s.notice = fmt.Sprintf(m.VarsResolveFailed, err)
			return
		}
		binarySensitive = sensitive
		if sensitive {
			sess.RegisterSecret(value)
		}
		raw = strings.ReplaceAll(strings.ReplaceAll(value, " ", ""), "\t", "")
	}
	data, err := hex.DecodeString(raw)
	if err != nil {
		s.notice = m.SendBinaryHint + ": " + err.Error()
		return
	}
	if len(data) > int(sess.Snapshot().MaxMessage) {
		s.notice = fmt.Sprintf("message exceeds the session limit (%d bytes)", sess.Snapshot().MaxMessage)
		return
	}
	if err := sess.SendBinary(context.Background(), data, binarySensitive); err != nil {
		if errors.Is(err, gows.ErrSessionClosed) {
			s.notice = m.SendNotOpen
		} else {
			s.notice = fmt.Sprintf(m.SendFailedFmt, err.Error())
		}
		return
	}
	s.notice = m.SendDone
}

// wsPing sends one manual ping and reports the RTT fact.
func (a *App) wsPing() {
	s := a.wsState(false)
	m := a.websocketMsg()
	if s == nil || !s.open || s.session == nil {
		return
	}
	sess := s.session
	if sess.State() != gows.StateOpen {
		s.notice = m.SendNotOpen
		return
	}
	win := a.win
	key := weak.Make(a)
	go func() {
		if win == nil || win.IsDestroyed() {
			return
		}
		rtt, err := sess.Ping(context.Background())
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			d := owner.wsState(false)
			if d == nil || d.session != sess {
				return
			}
			if err != nil {
				d.notice = fmt.Sprintf(owner.websocketMsg().PongMissingFmt, err.Error())
				return
			}
			d.notice = fmt.Sprintf(owner.websocketMsg().PongOKFmt, rtt.Round(time.Microsecond))
		})
	}()
}

// wsCloseSession performs the graceful close with the drafted code/reason.
func (a *App) wsCloseSession() {
	s := a.wsState(false)
	m := a.websocketMsg()
	if s == nil || !s.open || s.session == nil {
		return
	}
	sess := s.session
	if sess.State() != gows.StateOpen {
		s.notice = m.CloseNotOpen
		return
	}
	code := 1000
	if raw := strings.TrimSpace(s.closeCodeDraft); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1000 || n > 4999 {
			s.notice = m.BtnCloseSession + ": " + raw
			return
		}
		code = n
	}
	reason := s.closeReasonDraft
	win := a.win
	key := weak.Make(a)
	go func() {
		if win == nil || win.IsDestroyed() {
			return
		}
		_ = sess.CloseWith(code, reason)
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				return
			}
			if d := owner.wsState(false); d != nil && d.session == sess {
				d.notice = fmt.Sprintf(owner.websocketMsg().ClosingFmt, code)
			}
		})
	}()
}

// wsDisconnect aborts the live session without closing the dialog, so the
// timeline and facts stay inspectable. No close frame is sent.
func (a *App) wsDisconnect() {
	s := a.wsState(false)
	m := a.websocketMsg()
	if s == nil || !s.open || s.session == nil {
		return
	}
	a.wsStopMailbox(s)
	s.session.Terminate()
	s.notice = m.Terminated
}

// wsTerminateAndClose is the modal close path: closing the session window
// terminates the connection (spec §3), then drops the modal.
func (a *App) wsTerminateAndClose() {
	s := a.wsState(false)
	if s == nil {
		return
	}
	a.wsStopMailbox(s)
	if s.session != nil {
		s.session.Terminate()
	}
	s.session = nil
	s.selectedSeq = 0
	s.busy = false
	s.confirmAsked = false
	s.promptFacts = ""
	s.notice = a.websocketMsg().Terminated
	s.open = false
}

// wsClearView resets the timeline/detail selection and failed-attempt facts.
func (a *App) wsClearView() {
	s := a.wsState(false)
	if s == nil {
		return
	}
	a.wsStopMailbox(s)
	if s.session != nil {
		s.session.Terminate()
	}
	s.session = nil
	s.redactor = nil
	s.selectedSeq = 0
	s.notice = ""
	s.confirmAsked = false
	s.promptFacts = ""
}

// wsHeaderCarriesCredentials mirrors the module-level name set for the
// manual dialog's credential detection.
func gowsHeaderCarriesCredentials(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key":
		return true
	}
	return false
}
