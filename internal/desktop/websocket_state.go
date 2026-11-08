package desktop

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
	"weak"

	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/core"
	"gorequest/internal/stream"
	gows "gorequest/internal/websocket"
)

// websocket_state.go owns the per-tab WebSocket workspace state and everything
// that runs off the UI thread's back: template resolution, the connect attempt,
// the live event pump and selected-message saving. Every WebSocket tab owns its
// own session, mailbox and generation, so HTTP and WebSocket tabs can coexist
// and a background callback can only ever land in the tab that started it. No
// goroutine holds a strong App pointer: callbacks re-enter through
// weak.Pointer[App] + generation + tab-identity checks, mirroring the request
// runner's lifecycle rules.

const (
	// wsTimelineLimit bounds how many events one tab frame renders.
	wsTimelineLimit = 200
	// wsEventSubBuffer bounds the live event subscription channel. A frozen
	// window drops deliveries here and re-syncs from the log cursor on the next
	// frame — the loss is observable in the log Stats.
	wsEventSubBuffer = 64
)

// wsTabState is the WebSocket workspace state of one request tab. It is created
// lazily for protocol: websocket tabs and never shared between tabs.
type wsTabState struct {
	// section selects the configuration pane: 0 headers, 1 auth, 2 settings.
	section int

	// Drafts persist while the tab stays open and survive switching tabs.
	subprotoDraft    string
	compress         bool
	sendModeIdx      int // 0 = text, 1 = binary
	textDraft        string
	binaryDraft      string
	closeCodeDraft   string
	closeReasonDraft string
	dirFilterIdx     int // 0 = all, 1 = in, 2 = out
	kindFilterIdx    int // 0 = all payloads, 1 = messages only, 2 = control, 3 = facts
	filterQuery      string
	selectedSeq      uint64

	// sendEditor holds the multi-line text draft. *Gen records the theme
	// generation it was themed with, so an appearance switch restyles it
	// without losing the draft.
	sendEditor    *editor.State
	sendEditorGen int

	// Session lifecycle. session may be non-nil with a terminal state (facts
	// remain visible until cleared). generation invalidates every callback from
	// an older attempt or a torn-down tab.
	session       *gows.Session
	redactor      *core.Redactor
	generation    uint64
	mailbox       *wsMailbox
	busy          bool
	connectCancel context.CancelFunc
	promptFacts   string

	confirmChecked bool
	confirmAsked   bool
	notice         string
}

// ensureWSState returns this tab's WebSocket state, creating it on first use.
func (tab *Tab) ensureWSState() *wsTabState {
	if tab == nil {
		return nil
	}
	if tab.ws == nil {
		tab.ws = &wsTabState{closeCodeDraft: "1000"}
	}
	return tab.ws
}

// wsTeardownTab releases a tab's live connection and invalidates callbacks. It
// is safe to call on any tab and is idempotent. The terminal session is kept so
// its facts stay inspectable until the tab itself goes away.
func wsTeardownTab(tab *Tab) {
	if tab == nil || tab.ws == nil {
		return
	}
	st := tab.ws
	st.generation++
	if st.connectCancel != nil {
		st.connectCancel()
		st.connectCancel = nil
	}
	if st.mailbox != nil {
		st.mailbox.stop()
		st.mailbox = nil
	}
	if st.session != nil {
		st.session.Terminate()
	}
	st.busy = false
	st.confirmAsked = false
	st.promptFacts = ""
}

// wsMailbox is the bounded live-event pump driving window redraws. It never
// invokes the UI directly from the receive goroutine: every delivery only
// schedules a single Window.Update (coalesced flag).
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

// wsPump subscribes to the session log and schedules redraws. It exits when the
// mailbox stops or the subscription channel closes.
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

// wsStopMailbox stops a tab's event pump (UI thread only).
func wsStopMailbox(st *wsTabState) {
	if st == nil {
		return
	}
	if st.mailbox != nil {
		st.mailbox.stop()
		st.mailbox = nil
	}
}

// wsResolveTemplate resolves one draft string against project variables and
// secrets. Missing variables/secrets fail loudly — a template never silently
// becomes empty.
func wsResolveTemplate(resolver *core.Resolver, label, raw string, red *core.Redactor) (string, bool, error) {
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

// wsRegisterAuthSecrets registers the resolved secret values of an auth scheme
// with the session redactor before the scheme is applied.
func wsRegisterAuthSecrets(scheme *core.AuthScheme, resolver *core.Resolver, red *core.Redactor) {
	if scheme == nil || resolver == nil || red == nil {
		return
	}
	for _, raw := range []string{scheme.Token, scheme.Username, scheme.Password, scheme.Value} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		if tv, err := resolver.ResolveTracked(raw); err == nil && tv.Sensitive && tv.Value != "" {
			red.RegisterSecret(tv.Value)
		}
	}
}

// wsApplyAuth appends the effective auth scheme's header/query pairs and folds
// query auth into the endpoint URL.
func wsApplyAuth(ps *projectState, tab *Tab, req *core.Request, vars map[string]string, resolver *core.Resolver, red *core.Redactor, endpoint string, headers *[][2]string) (string, bool, error) {
	if ps == nil || ps.project == nil || tab == nil || tab.Entry == nil {
		return endpoint, false, nil
	}
	eff, err := ps.project.ResolveEffectiveAuth(tab.Entry, req)
	if err != nil {
		return endpoint, false, err
	}
	if eff == nil || eff.Scheme == nil {
		return endpoint, false, nil
	}
	wsRegisterAuthSecrets(eff.Scheme, resolver, red)
	var query [][2]string
	if err := core.ApplyAuthToHandshake(eff.Scheme, vars, ps.secrets, headers, &query); err != nil {
		return endpoint, false, err
	}
	hasCreds := !strings.EqualFold(strings.TrimSpace(eff.Scheme.Type), "none") &&
		!strings.EqualFold(strings.TrimSpace(eff.Scheme.Type), "") &&
		!strings.EqualFold(strings.TrimSpace(eff.Scheme.Type), "oauth2") &&
		!strings.EqualFold(strings.TrimSpace(eff.Scheme.Type), "oidc")
	if len(query) == 0 {
		return endpoint, hasCreds, nil
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return endpoint, hasCreds, err
	}
	q := u.Query()
	for _, pair := range query {
		q.Add(pair[0], pair[1])
	}
	u.RawQuery = q.Encode()
	return u.String(), hasCreds, nil
}

// wsTabConnect validates the tab's drafts, resolves templates, runs the
// target-policy preflight and starts the connect attempt off the UI thread. It
// is the only path to a session — nothing auto-connects.
func (a *App) wsTabConnect(tab *Tab) {
	st := tab.ensureWSState()
	m := a.websocketMsg()
	if st == nil {
		return
	}
	if st.busy {
		st.notice = m.Busy
		return
	}
	if st.session != nil && st.session.State() == gows.StateOpen {
		st.notice = m.Busy
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil || ps.project == nil {
		st.notice = m.NoProject
		return
	}

	req, err := requestFromTab(tab)
	if err != nil {
		st.notice = err.Error()
		return
	}
	entry := tab.Entry
	vars, _, err := ps.project.ResolveVars(entry, ps.currentEnv())
	if err != nil {
		st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	resolver := core.NewResolver(vars, ps.secrets)
	red := core.NewRedactor()

	endpoint, _, err := wsResolveTemplate(resolver, m.FieldURL, strings.TrimSpace(tab.URL), red)
	if err != nil {
		st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	if strings.TrimSpace(endpoint) == "" {
		st.notice = m.FieldURL + ": " + endpoint
		return
	}

	var pairs [][2]string
	hasCreds := false
	if req.HTTP != nil {
		for _, kv := range req.HTTP.Headers {
			if !kv.IsEnabled() {
				continue
			}
			name := strings.TrimSpace(kv.Name)
			value, sensitive, err := wsResolveTemplate(resolver, name, kv.Value, red)
			if err != nil {
				st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
				return
			}
			pairs = append(pairs, [2]string{name, value})
			if sensitive || gowsHeaderCarriesCredentials(name) {
				hasCreds = true
			}
		}
	}
	endpoint, authCreds, err := wsApplyAuth(ps, tab, req, vars, resolver, red, endpoint, &pairs)
	if err != nil {
		st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	hasCreds = hasCreds || authCreds

	var subprotocols []string
	for _, sp := range strings.Split(st.subprotoDraft, ",") {
		if sp = strings.TrimSpace(sp); sp != "" {
			subprotocols = append(subprotocols, sp)
		}
	}

	var total time.Duration
	if raw := strings.TrimSpace(tab.Timeout); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d < 0 {
			st.notice = m.FieldTotalTimeout + ": " + raw
			return
		}
		total = d
	}

	// Device network policy snapshot happens on the UI thread (same rule as the
	// runner): values are copied into the options that travel to the connect
	// goroutine; no App/Tab pointer crosses the boundary.
	net := a.buildExecuteOptionsForTab(ps, tab, nil, nil, red)

	opts := gows.Options{
		URL:               endpoint,
		Headers:           pairs,
		Subprotocols:      subprotocols,
		EnableCompression: st.compress,
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
		Confirmed:       st.confirmChecked,
		HasCredentials:  hasCreds,
		Secrets:         net.Secrets,
		LocalFiles:      net.LocalFiles,
		ProjectRoot:     net.ProjectRoot,
		Redactor:        red,
	}

	// Preflight the exact decision so the confirm step is visible BEFORE an
	// attempt; Connect re-evaluates and enforces the same check.
	if decision, perr := gows.PromptRequiredBy(&opts); perr == nil && decision != nil &&
		decision.Outcome == core.DecisionConfirmRequired && !st.confirmChecked {
		st.confirmAsked = true
		st.notice = m.ConfirmNeeded
		st.promptFacts = strings.Join(decision.Reasons, "; ")
		return
	}

	// Confirmation is a one-time approval of THIS exact target: consume it so a
	// later reconnect cannot inherit it silently.
	st.confirmChecked = false

	ctx, cancel := context.WithCancel(context.Background())
	st.connectCancel = cancel
	st.busy = true
	st.notice = fmt.Sprintf(m.Connecting, red.RedactString(endpoint))
	st.generation++
	gen := st.generation
	key := weak.Make(a)
	win := a.win
	st.redactor = red

	go func() {
		sess, cerr := gows.Connect(ctx, opts)
		if cerr != nil {
			cancel()
		}
		if win == nil || win.IsDestroyed() {
			// The window vanished mid-attempt: never leave a live socket behind
			// just because no UI can observe it.
			cancel()
			if sess != nil {
				sess.Terminate()
			}
			return
		}
		win.Update(func() {
			owner := key.Value()
			if owner == nil || owner.closed {
				cancel()
				if sess != nil {
					sess.Terminate()
				}
				return
			}
			d := tab.ws
			if d == nil || d.generation != gen || ps.indexOfTab(tab) < 0 {
				// A newer attempt, a closed tab or a torn-down window won; keep
				// this session terminating without touching any UI state.
				cancel()
				if sess != nil {
					sess.Terminate()
				}
				return
			}
			d.busy = false
			if d.session != nil {
				d.session.Terminate()
			}
			wsStopMailbox(d)
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

// wsTabSend resolves the send draft and performs one bounded send.
func (a *App) wsTabSend(tab *Tab) {
	st := tab.ensureWSState()
	m := a.websocketMsg()
	if st == nil || st.session == nil {
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	if ps == nil || ps.project == nil {
		st.notice = m.NoProject
		return
	}
	sess := st.session
	if sess.State() != gows.StateOpen {
		st.notice = m.SendNotOpen
		return
	}
	red := core.NewRedactor()
	entry := tab.Entry
	vars, _, err := ps.project.ResolveVars(entry, ps.currentEnv())
	if err != nil {
		st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
		return
	}
	resolver := core.NewResolver(vars, ps.secrets)

	if st.sendModeIdx == 0 {
		raw := st.textDraft
		if st.sendEditor != nil {
			raw = st.sendEditor.Buffer.String()
		}
		value, sensitive, err := wsResolveTemplate(resolver, m.BtnSend, raw, red)
		if err != nil {
			st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
			return
		}
		if sensitive {
			sess.RegisterSecret(value)
		}
		if len(value) > int(sess.Snapshot().MaxMessage) {
			st.notice = fmt.Sprintf("message exceeds the session limit (%d bytes)", sess.Snapshot().MaxMessage)
			return
		}
		if err := sess.SendText(context.Background(), value, sensitive); err != nil {
			if !errors.Is(err, gows.ErrSessionClosed) {
				st.notice = fmt.Sprintf(m.SendFailedFmt, err.Error())
			} else {
				st.notice = m.SendNotOpen
			}
			return
		}
		st.notice = m.SendDone
		return
	}

	rawHex := strings.TrimSpace(st.binaryDraft)
	raw := strings.ReplaceAll(strings.ReplaceAll(rawHex, " ", ""), "\t", "")
	binarySensitive := false
	if strings.Contains(raw, "{{") {
		value, sensitive, err := wsResolveTemplate(resolver, m.BtnSend, raw, red)
		if err != nil {
			st.notice = fmt.Sprintf(m.VarsResolveFailed, err)
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
		st.notice = m.SendBinaryHint + ": " + err.Error()
		return
	}
	if len(data) > int(sess.Snapshot().MaxMessage) {
		st.notice = fmt.Sprintf("message exceeds the session limit (%d bytes)", sess.Snapshot().MaxMessage)
		return
	}
	if err := sess.SendBinary(context.Background(), data, binarySensitive); err != nil {
		if errors.Is(err, gows.ErrSessionClosed) {
			st.notice = m.SendNotOpen
		} else {
			st.notice = fmt.Sprintf(m.SendFailedFmt, err.Error())
		}
		return
	}
	st.notice = m.SendDone
}

// wsTabPing sends one manual ping and reports the RTT fact.
func (a *App) wsTabPing(tab *Tab) {
	st := tab.ensureWSState()
	m := a.websocketMsg()
	if st == nil || st.session == nil {
		return
	}
	sess := st.session
	if sess.State() != gows.StateOpen {
		st.notice = m.SendNotOpen
		return
	}
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	gen := st.generation
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
			d := tab.ws
			if d == nil || d.session != sess || d.generation != gen || (ps != nil && ps.indexOfTab(tab) < 0) {
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

// wsTabCloseSession performs the graceful close with the drafted code/reason.
func (a *App) wsTabCloseSession(tab *Tab) {
	st := tab.ensureWSState()
	m := a.websocketMsg()
	if st == nil || st.session == nil {
		return
	}
	sess := st.session
	if sess.State() != gows.StateOpen {
		st.notice = m.CloseNotOpen
		return
	}
	code := 1000
	if raw := strings.TrimSpace(st.closeCodeDraft); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1000 || n > 4999 {
			st.notice = m.BtnCloseSession + ": " + raw
			return
		}
		code = n
	}
	reason := st.closeReasonDraft
	ps := tab.Owner
	if ps == nil {
		ps = a.ps
	}
	gen := st.generation
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
			d := tab.ws
			if d == nil || d.session != sess || d.generation != gen || (ps != nil && ps.indexOfTab(tab) < 0) {
				return
			}
			d.notice = fmt.Sprintf(owner.websocketMsg().ClosingFmt, code)
		})
	}()
}

// wsTabDisconnect aborts the live session without clearing the timeline, so the
// events and facts stay inspectable. No close frame is sent.
func (a *App) wsTabDisconnect(tab *Tab) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	wsTeardownTab(tab)
	st.notice = a.websocketMsg().Terminated
}

// wsTabClear resets the timeline/detail selection and failed-attempt facts,
// terminating any live connection. Drafts are kept.
func (a *App) wsTabClear(tab *Tab) {
	st := tab.ensureWSState()
	if st == nil {
		return
	}
	wsTeardownTab(tab)
	st.session = nil
	st.redactor = nil
	st.selectedSeq = 0
	st.notice = ""
	st.confirmAsked = false
	st.promptFacts = ""
	st.busy = false
}

// wsHeaderCarriesCredentials mirrors the module-level name set for the manual
// workspace's credential detection.
func gowsHeaderCarriesCredentials(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "x-api-key":
		return true
	}
	return false
}
