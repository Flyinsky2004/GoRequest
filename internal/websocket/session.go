package websocket

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gorequest/internal/core"
	"gorequest/internal/stream"
)

// EndReason records how a session ended, for reports and automation.
type EndReason string

const (
	EndNone            EndReason = ""
	EndUserClosed      EndReason = "user_closed"
	EndServerClosed    EndReason = "server_closed"
	EndAborted         EndReason = "aborted"
	EndTotalDeadline   EndReason = "total_deadline"
	EndLostConnection  EndReason = "lost_connection"
	EndHandshakeFailed EndReason = "handshake_failed"
	EndConfigRejected  EndReason = "config_rejected"
)

// TLSFacts summarizes the negotiated TLS layer (zero Enabled for plain ws://).
type TLSFacts struct {
	Enabled      bool
	Version      string
	CipherSuite  string
	ServerName   string
	InsecureUsed bool
	VerifyOK     bool
}

// Snapshot is the session's fact view for UI and reports. All error/reason
// text is pre-redacted through the session's core.Redactor.
type Snapshot struct {
	State             State
	End               EndReason
	URL               string // redacted
	HTTPStatus        int    // 101 on success, non-101 on handshake failure
	Subprotocol       string
	RemoteAddr        string
	ProxyUsed         string
	TLS               *TLSFacts
	CompressUsed      bool
	StartedAt         time.Time
	EndedAt           time.Time
	CloseCode         int
	CloseReason       string
	LastErr           string
	SentCount         uint64
	ReceiveCount      uint64
	PingCount         uint64
	PongCount         uint64
	InboxDropped      uint64
	InboxCapacity     int
	StreamStats       stream.Stats
	MaxMessage        int64
	ConnectDeadline   time.Duration
	HandshakeDeadline time.Duration
	MessageDeadline   time.Duration
	TotalDeadline     time.Duration
}

// InboxItem is one retained full message payload for the manual session's
// detail view and "save selected". Items evicted by the inbox bound are
// counted in Snapshot.InboxDropped — never silently lost.
type InboxItem struct {
	Seq       uint64 // timeline correlation (the stream event's Seq)
	Time      time.Time
	Direction stream.Direction
	Kind      stream.Kind
	IsText    bool
	Data      []byte // full payload bounded by EffectiveMaxMessageBytes
	Sensitive bool
}

// Session owns one manual WebSocket connection and its shared bounded event
// log. It is safe for concurrent use; the read pump is the only reader and
// user sends funnel through a write mutex (gorilla permits concurrent
// WriteControl from control handlers, which is the only exception).
type Session struct {
	opts Options
	log  *stream.Log
	red  *core.Redactor

	sessionCtx    context.Context
	sessionCancel context.CancelFunc

	mu stateMutex

	writeMu sync.Mutex

	// redMu serializes access to the shared core.Redactor: the read pump
	// redacts close facts while the UI thread may register a send-time secret.
	// Callers must use RegisterSecret/redact rather than touching s.red.
	redMu sync.Mutex

	readDone chan struct{}
}

// stateMutex carries every mutable session field behind mu.
type stateMutex struct {
	mu          sync.Mutex
	state       State
	end         EndReason
	conn        *websocket.Conn
	remoteAddr  string
	subproto    string
	httpStatus  int
	proxyUsed   string
	tlsFacts    *TLSFacts
	compress    bool
	closeCode   int
	closeReason string
	lastErr     string
	startedAt   time.Time
	endedAt     time.Time
	sent        uint64
	received    uint64
	pings       uint64
	pongs       uint64
	opSeq       uint64
	pending     map[string]pendingPing
	inbox       []InboxItem
	inboxDrop   uint64
	totalT      *time.Timer
}

type pendingPing struct {
	sent  time.Time
	waitC chan struct{}
}

// GracefulCloseWait bounds how long Close waits for the peer's close frame
// echo before aborting the TCP connection.
const GracefulCloseWait = 2 * time.Second

// Connect performs the full pre-network gate (config validation + target
// policy evaluation — both fail with *core.ConfigError BEFORE any socket
// activity), then dials with the resolved proxy/TLS settings.
//
// On failure the returned error is classified: *HandshakeError for non-101
// responses, *core.ConfigError for pre-network refusals (including
// ErrConfirmRequired when an interactive confirmation decision is pending)
// and transport failures wrapped as *core.RunError. A non-nil Session is
// returned ALONGSIDE the error whenever the attempt could record facts: its
// event log carries the exact rejection/handshake timeline for the UI even
// though no connection was ever established (state is closed; sends fail).
//
// Canceling ctx aborts the session at any point.
func Connect(ctx context.Context, opts Options) (*Session, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Redactor == nil {
		opts.Redactor = core.NewRedactor()
	}
	s := &Session{
		opts: opts,
		red:  opts.Redactor,
	}
	evOpts := opts.EventOptions()
	evOpts.Redactor = opts.Redactor
	// The log redacts through the session's mutex-guarded function so a
	// send-time RegisterSecret cannot race the read pump's appends.
	evOpts.Redact = s.redact
	if strings.TrimSpace(evOpts.StreamID) == "" {
		evOpts.StreamID = correlationIDFor(opts.URL)
	}
	s.log = stream.NewLog(evOpts)
	s.mu.startedAt = time.Now()
	s.mu.state = StateConnecting

	// Credential-bearing handshake header values are registered before any
	// event can retain them, so redaction covers the timeline and reports.
	for _, h := range s.opts.Headers {
		if isSensitiveHeaderName(h[0]) {
			s.RegisterSecret(h[1])
		}
	}

	// 1. Config-level validation — nothing below runs when this fails.
	if err := Validate(&s.opts); err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}

	// 2. Target policy, BEFORE any network byte (websocket sessions count as
	// mutating traffic because a live socket can carry writes).
	policyURL, err := policyURLFor(strings.TrimSpace(s.opts.URL))
	if err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}
	decision, err := core.EvaluateTarget(s.opts.effectiveTargetPolicy(), core.TargetCheckInput{
		URL:            policyURL,
		Method:         "WEBSOCKET",
		EnvID:          s.opts.EnvID,
		HasCredentials: s.opts.HasCredentials || headersCarryCredentials(s.opts.Headers),
		Interactive:    s.opts.Interactive,
		Confirmed:      s.opts.Confirmed,
	})
	if err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirNone,
			Summary:   s.redactError(fmt.Errorf("connection refused before network: %w", err)).Error(),
		})
		if decision != nil {
			for _, r := range decision.Reasons {
				s.log.Append(stream.Event{Kind: stream.KindInfo, Direction: stream.DirNone, Summary: s.redact(r)})
			}
		}
		return s, err
	}
	if decision.Outcome == core.DecisionConfirmRequired {
		err = &core.ConfigError{Err: fmt.Errorf(
			"connecting to %s requires interactive confirmation before a websocket session: %w",
			decision.Host, ErrConfirmRequired)}
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}

	s.log.Append(stream.Event{
		Kind:      stream.KindState,
		Direction: stream.DirNone,
		Summary:   fmt.Sprintf("connecting to %s", s.sanitizedTarget()),
	})

	// 3. Resolve proxy and TLS while still off the network.
	rp, err := resolveProxy(&s.opts, s.red)
	if err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}
	tlsCfg, err := buildTLSConfig(ctx, &s.opts, s.red)
	if err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}
	header, err := httpHandshakeHeader(s.opts.Headers)
	if err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}

	// 3b. Resolve the concrete dial strategy (direct, SOCKS5 or an HTTP
	// CONNECT tunnel) while still off the network. An unsupported proxy
	// scheme is refused here, never silently ignored.
	dialer, err := s.buildDialer(rp, tlsCfg)
	if err != nil {
		s.rejectPreNetwork(EndConfigRejected, err)
		s.logFailureFact(err)
		return s, err
	}

	sessionCtx, cancel := context.WithCancel(ctx)
	s.sessionCtx = sessionCtx
	s.sessionCancel = cancel
	s.mu.pending = map[string]pendingPing{}
	s.mu.inbox = nil
	s.readDone = make(chan struct{})

	dialCtx := sessionCtx
	if total := s.opts.TotalDeadline(); total > 0 {
		s.startTotalWatchdog(total)
	}

	conn, resp, dialErr := dialer.DialContext(dialCtx, strings.TrimSpace(s.opts.URL), header)
	if dialErr != nil {
		cancel()
		s.stopTotalTimer()
		if hs := classifyDialError(dialErr, resp); hs != nil {
			s.recordHandshakeFailure(hs, dialCtx.Err())
			s.rejectPreNetwork(EndHandshakeFailed, hs)
			return s, hs
		}
		mapped := s.mapDialError(dialErr, dialCtx.Err())
		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirNone,
			Summary:   fmt.Sprintf("connect failed: %s", mapped.Error()),
		})
		s.rejectPreNetwork(EndLostConnection, mapped)
		return s, mapped
	}

	// 4. Open: snapshot facts and start the read pump.
	conn.SetReadLimit(s.opts.EffectiveMaxMessageBytes())
	conn.SetPingHandler(s.pingHandler(conn))
	conn.SetPongHandler(s.pongHandler)

	s.mu.mu.Lock()
	s.mu.conn = conn
	s.mu.httpStatus = resp.StatusCode
	s.mu.subproto = conn.Subprotocol()
	s.mu.compress = resp.Header.Get("Sec-Websocket-Extensions") != ""
	if nc := conn.NetConn(); nc != nil {
		s.mu.remoteAddr = nc.RemoteAddr().String()
		if tc, ok := nc.(*tls.Conn); ok {
			s.mu.tlsFacts = tlsFactsOf(tc, s.opts.TLS.InsecureSkipVerify)
		}
	}
	s.mu.mu.Unlock()

	s.log.Append(stream.Event{
		Kind:      stream.KindState,
		Direction: stream.DirNone,
		Summary:   s.openFactSummary(),
	})
	s.startReadLoop(conn)
	s.watchParentCtx(ctx)

	return s, nil
}

// rejectPreNetwork finalizes a session that never reached the wire: state
// goes straight from connecting to closed with the exact recorded end.
func (s *Session) rejectPreNetwork(end EndReason, err error) {
	s.stopTotalTimer()
	s.mu.mu.Lock()
	alreadyDone := s.mu.state == StateClosed
	if !alreadyDone {
		s.mu.state = StateClosed
		if s.mu.end == EndNone {
			s.mu.end = end
		}
		if err != nil && s.mu.lastErr == "" {
			s.mu.lastErr = s.redactError(err).Error()
		}
		if s.mu.endedAt.IsZero() {
			s.mu.endedAt = time.Now()
		}
	}
	s.mu.mu.Unlock()
	if alreadyDone {
		return
	}
	s.log.Append(stream.Event{
		Kind:      stream.KindState,
		Direction: stream.DirNone,
		Summary:   fmt.Sprintf("state: closed (%s)", end),
	})
}

// buildDialer assembles the gorilla dialer with the resolved dial plan.
// SOCKS5 uses gorilla's bundled RFC 1928/1929 client; HTTP/HTTPS proxies use
// this package's CONNECT tunnel (gorilla's Proxy callback only understands
// SOCKS5). An unsupported scheme is returned as a pre-network error.
func (s *Session) buildDialer(rp *resolvedWSProxy, tlsCfg *tls.Config) (*websocket.Dialer, error) {
	base := &net.Dialer{Timeout: s.opts.EffectiveConnect()}
	d := &websocket.Dialer{
		Subprotocols:      s.opts.Subprotocols,
		EnableCompression: s.opts.EnableCompression,
		HandshakeTimeout:  s.opts.EffectiveHandshake(),
		TLSClientConfig:   tlsCfg,
		NetDialContext:    base.DialContext,
	}
	plan, err := rp.plan(strings.TrimSpace(s.opts.URL), base, func(label string) {
		s.mu.mu.Lock()
		if s.mu.proxyUsed == "" {
			s.mu.proxyUsed = label
		}
		s.mu.mu.Unlock()
	})
	if err != nil {
		return nil, err
	}
	if plan.proxyFn != nil {
		d.Proxy = plan.proxyFn
	}
	if plan.netDial != nil {
		d.NetDialContext = plan.netDial
	}
	return d, nil
}

// startReadLoop runs the receive/ping/pong/close pump; the only reader.
func (s *Session) startReadLoop(conn *websocket.Conn) {
	go func() {
		defer close(s.readDone)
		for {
			mt, data, err := conn.ReadMessage()
			if err != nil {
				s.handleReadError(conn, err)
				return
			}
			s.handleDataFrame(mt, data)
		}
	}()
}

// handleDataFrame records inbound text/binary into the shared log and inbox.
func (s *Session) handleDataFrame(mt int, data []byte) {
	isText := mt == websocket.TextMessage
	dataCopy := append([]byte(nil), data...)

	ev := s.log.Append(stream.Event{
		Kind:      stream.KindReceive,
		Direction: stream.DirIn,
		IsText:    isText,
		Payload:   dataCopy,
		Summary:   frameSummary(stream.DirIn, isText, len(data)),
	})

	s.mu.mu.Lock()
	s.mu.received++
	s.mu.pushInboxLocked(InboxItem{
		Seq:       ev.Seq,
		Time:      ev.Time,
		Direction: stream.DirIn,
		Kind:      stream.KindReceive,
		IsText:    isText,
		Data:      dataCopy,
	}, s.opts.EffectiveInboxCapacity())
	s.mu.mu.Unlock()
}

// pushInboxLocked appends a bounded full-payload item (caller holds mu).
func (st *stateMutex) pushInboxLocked(item InboxItem, capacity int) {
	st.inbox = append(st.inbox, item)
	if len(st.inbox) > capacity {
		evicted := len(st.inbox) - capacity
		st.inbox = append([]InboxItem(nil), st.inbox[evicted:]...)
		st.inboxDrop += uint64(evicted)
	}
}

// pingHandler answers pings (mirroring gorilla's default pong) and records
// both facts in the timeline. WriteControl is safe concurrently with the
// user's WriteMessage traffic.
func (s *Session) pingHandler(conn *websocket.Conn) func(appData string) error {
	return func(appData string) error {
		s.log.Append(stream.Event{
			Kind:      stream.KindPing,
			Direction: stream.DirIn,
			Payload:   []byte(appData),
			IsText:    true,
			Summary:   fmt.Sprintf("ping received (%d bytes)", len(appData)),
		})
		deadline := time.Now().Add(s.opts.EffectiveMessage())
		if err := conn.WriteControl(websocket.PongMessage, []byte(appData), deadline); err != nil {
			s.log.Append(stream.Event{
				Kind:      stream.KindError,
				Direction: stream.DirNone,
				Summary:   fmt.Sprintf("pong reply failed: %s", s.redactError(err).Error()),
			})
			return err
		}
		s.mu.mu.Lock()
		s.mu.pongs++
		s.mu.mu.Unlock()
		s.log.Append(stream.Event{
			Kind:      stream.KindPong,
			Direction: stream.DirOut,
			Payload:   []byte(appData),
			IsText:    true,
			Summary:   fmt.Sprintf("pong sent (%d bytes)", len(appData)),
		})
		return nil
	}
}

// pongHandler records pong facts and resolves pending pings by correlation
// token carried in the pong application data.
func (s *Session) pongHandler(appData string) error {
	now := time.Now()
	s.mu.mu.Lock()
	pend, ok := s.pending[appData]
	if ok {
		delete(s.pending, appData)
	}
	s.mu.pongs++
	s.mu.mu.Unlock()
	summary := fmt.Sprintf("pong received (%d bytes)", len(appData))
	if ok {
		summary = fmt.Sprintf("pong received (rtt %s)", now.Sub(pend.sent).Round(time.Microsecond))
	}
	s.log.Append(stream.Event{
		Kind:      stream.KindPong,
		Direction: stream.DirIn,
		OpID:      appData,
		Payload:   []byte(appData),
		IsText:    true,
		Summary:   summary,
	})
	if ok {
		close(pend.waitC)
	}
	return nil
}

// handleReadError maps read failures to terminal facts: a peer close frame
// is recorded with its code and (redacted) reason; a local abort is a plain
// termination; anything else is a lost-connection fact (断流), never a fake
// success state.
func (s *Session) handleReadError(conn *websocket.Conn, err error) {
	var closeErr *websocket.CloseError
	switch {
	case errors.As(err, &closeErr):
		// The raw socket is ours to release — gorilla drains but does not
		// close it on a close frame.
		_ = conn.Close()
		reason := s.redact(closeErr.Text)
		s.mu.mu.Lock()
		s.mu.conn = nil
		if s.mu.end == EndNone {
			if s.mu.state == StateClosing {
				// Our close frame raced the peer's: the session is still
				// user-initiated.
				s.mu.end = EndUserClosed
			} else {
				s.mu.end = EndServerClosed
			}
		}
		s.mu.state = StateClosed
		s.mu.closeCode = closeErr.Code
		s.mu.closeReason = reason
		if s.mu.endedAt.IsZero() {
			s.mu.endedAt = time.Now()
		}
		s.mu.mu.Unlock()
		s.stopTotalTimer()
		if s.sessionCancel != nil {
			s.sessionCancel()
		}
		s.log.Append(stream.Event{
			Kind:      stream.KindClose,
			Direction: stream.DirIn,
			Summary:   fmt.Sprintf("connection closed by peer: code %d", closeErr.Code),
			Code:      closeErr.Code,
			Reason:    reason,
		})
		s.log.Append(stream.Event{
			Kind:      stream.KindState,
			Direction: stream.DirNone,
			Summary:   fmt.Sprintf("state: closed (code %d)", closeErr.Code),
		})
	default:
		if s.sessionCtx != nil && s.sessionCtx.Err() != nil {
			s.finish(EndAborted, nil)
			return
		}
		// finish() releases the socket via s.mu.conn; nothing to close here.
		s.finish(EndLostConnection, err)
	}
}

// mapDialError classifies transport dial failures for honest reporting.
func (s *Session) mapDialError(err error, ctxErr error) error {
	switch ctxErr {
	case context.DeadlineExceeded:
		return runErrorf("websocket connect to %s exceeded its deadline", s.sanitizedTarget())
	case context.Canceled:
		return runErrorf("websocket connect to %s was canceled", s.sanitizedTarget())
	default:
		return runErrorf("websocket connect to %s failed: %v", s.sanitizedTarget(), err)
	}
}

// recordHandshakeFailure stores non-101 facts (status, headers, body preview).
func (s *Session) recordHandshakeFailure(hs *HandshakeError, ctxErr error) {
	s.mu.mu.Lock()
	s.mu.httpStatus = hs.Status
	s.mu.mu.Unlock()
	summary := fmt.Sprintf("handshake failed: HTTP %d", hs.Status)
	switch ctxErr {
	case context.DeadlineExceeded:
		summary = "handshake failed: deadline exceeded"
	case context.Canceled:
		summary = "handshake failed: canceled"
	}
	ev := s.log.Append(stream.Event{
		Kind:      stream.KindError,
		Direction: stream.DirNone,
		Summary:   s.redact(summary),
	})
	for _, h := range hs.Headers {
		name := strings.TrimSpace(h[0])
		sensitive := isSensitiveHeaderName(name)
		value := h[1]
		if sensitive {
			// Response credentials (Set-Cookie, WWW-Authenticate tokens) were
			// never registered ahead of time; mask them outright rather than
			// trusting a redactor that cannot know them.
			value = "***"
		}
		s.log.Append(stream.Event{
			Kind:      stream.KindInfo,
			Direction: stream.DirIn,
			OpID:      strconv.FormatUint(ev.Seq, 10),
			Summary:   fmt.Sprintf("handshake response header %s", name),
			Payload:   []byte(value),
			IsText:    true,
			Sensitive: sensitive,
		})
	}
	if b := strings.TrimSpace(hs.Body); b != "" {
		s.log.Append(stream.Event{
			Kind:      stream.KindInfo,
			Direction: stream.DirIn,
			OpID:      strconv.FormatUint(ev.Seq, 10),
			Summary:   "handshake response body excerpt",
			Payload:   []byte(b),
			IsText:    true,
			Sensitive: false,
		})
	}
}

// logFailureFact records a pre-network/config failure on the log.
func (s *Session) logFailureFact(err error) {
	s.log.Append(stream.Event{
		Kind:      stream.KindError,
		Direction: stream.DirNone,
		Summary:   fmt.Sprintf("connection refused before network: %s", s.redactError(err).Error()),
	})
}

// openFactSummary builds the "open" fact line.
func (s *Session) openFactSummary() string {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	parts := []string{fmt.Sprintf("state: open (%s)", s.remoteAddr)}
	if s.subproto != "" {
		parts = append(parts, "subprotocol "+s.subproto)
	}
	if s.tlsFacts != nil {
		parts = append(parts, "tls "+s.tlsFacts.Version)
	}
	if s.compress {
		parts = append(parts, "permessage-deflate")
	}
	return strings.Join(parts, " · ")
}

// frameSummary renders a one-line direction/kind/size summary.
func frameSummary(dir stream.Direction, isText bool, size int) string {
	typ := "binary"
	if isText {
		typ = "text"
	}
	return fmt.Sprintf("%s %s message (%d bytes)", dir, typ, size)
}

// isSensitiveHeaderName marks handshakes/report header names whose values
// carry credentials and must never appear un-redacted.
func isSensitiveHeaderName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "authorization", "proxy-authorization", "cookie", "set-cookie", "x-api-key":
		return true
	}
	return false
}

// RegisterSecret records a secret with the session's redactor under the
// redaction mutex. UI and automation callers use this instead of touching the
// shared core.Redactor directly.
func (s *Session) RegisterSecret(secret string) {
	if s == nil || s.red == nil || secret == "" {
		return
	}
	s.redMu.Lock()
	defer s.redMu.Unlock()
	s.red.RegisterSecret(secret)
}

// redact scrubs one string through the session redactor.
func (s *Session) redact(v string) string {
	if s == nil || s.red == nil {
		return v
	}
	s.redMu.Lock()
	defer s.redMu.Unlock()
	return s.red.RedactString(v)
}

// redactError scrubs an error message while preserving its classification.
func (s *Session) redactError(err error) error {
	if s == nil || s.red == nil {
		return err
	}
	s.redMu.Lock()
	defer s.redMu.Unlock()
	return s.red.RedactError(err)
}

// sanitizedTarget redacts the target for logging surfaces.
func (s *Session) sanitizedTarget() string {
	return s.redact(strings.TrimSpace(s.opts.URL))
}

// correlationIDFor builds a stable human-readable stream correlation ID from
// the target URL (no credentials — URLs with userinfo are rejected earlier).
func correlationIDFor(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u == nil || strings.TrimSpace(u.Host) == "" {
		return "ws"
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "wss" {
		scheme = "ws"
	}
	return scheme + "-" + u.Host
}

// tlsFactsOf snapshots the negotiated TLS connection state.
func tlsFactsOf(tc *tls.Conn, insecureUsed bool) *TLSFacts {
	st := tc.ConnectionState()
	facts := &TLSFacts{
		Enabled:      true,
		Version:      tls.VersionName(st.Version),
		CipherSuite:  tls.CipherSuiteName(st.CipherSuite),
		InsecureUsed: insecureUsed,
		VerifyOK:     len(st.VerifiedChains) > 0,
	}
	if st.ServerName != "" {
		facts.ServerName = st.ServerName
	}
	return facts
}
