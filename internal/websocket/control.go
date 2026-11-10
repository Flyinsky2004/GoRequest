package websocket

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"gorequest/internal/stream"
)

// ErrSessionClosed is returned by sends attempted after the stream ended: a
// broken line must never fake a successful send.
var ErrSessionClosed = errors.New("websocket session is not open")

// maxCloseReasonBytes is the RFC 6455 close-reason cap (123 payload bytes).
const maxCloseReasonBytes = 123

// Events exposes the shared bounded stream log. UI and automation (A01/P02/
// P03) consume it read-only: cursor reads via SnapshotSince or live delivery
// via Subscribe; backpressure and eviction are observable via Stats.
func (s *Session) Events() *stream.Log { return s.log }

// StreamID returns the correlation ID shared by every event of this session.
func (s *Session) StreamID() string { return s.log.StreamID() }

// Done is closed when the read pump stops (peer close, lost connection, abort
// or pre-network rejection). It lets in-process protocol drivers wait for a
// terminal event without polling the stream log. nil for a nil session.
func (s *Session) Done() <-chan struct{} {
	if s == nil {
		return nil
	}
	return s.readDone
}

// State returns the current session state.
func (s *Session) State() State {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	return s.mu.state
}

// Snapshot returns a consistent, pre-redacted facts view for UI and reports.
func (s *Session) Snapshot() Snapshot {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	sn := Snapshot{
		State:             s.mu.state,
		End:               s.mu.end,
		URL:               s.redact(strings.TrimSpace(s.opts.URL)),
		HTTPStatus:        s.mu.httpStatus,
		Subprotocol:       s.mu.subproto,
		RemoteAddr:        s.mu.remoteAddr,
		ProxyUsed:         s.mu.proxyUsed,
		TLS:               s.mu.tlsFacts,
		CompressUsed:      s.mu.compress,
		StartedAt:         s.mu.startedAt,
		EndedAt:           s.mu.endedAt,
		CloseCode:         s.mu.closeCode,
		CloseReason:       s.mu.closeReason,
		LastErr:           s.mu.lastErr,
		SentCount:         s.mu.sent,
		ReceiveCount:      s.mu.received,
		PingCount:         s.mu.pings,
		PongCount:         s.mu.pongs,
		InboxDropped:      s.mu.inboxDrop,
		InboxCapacity:     s.opts.EffectiveInboxCapacity(),
		StreamStats:       s.log.Stats(),
		MaxMessage:        s.opts.EffectiveMaxMessageBytes(),
		ConnectDeadline:   s.opts.EffectiveConnect(),
		HandshakeDeadline: s.opts.EffectiveHandshake(),
		MessageDeadline:   s.opts.EffectiveMessage(),
		TotalDeadline:     s.opts.TotalDeadline(),
	}
	return sn
}

// Inbox returns the retained full payloads, oldest first (bounded by
// EffectiveInboxCapacity; evictions counted in Snapshot.InboxDropped).
func (s *Session) Inbox() []InboxItem {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	out := make([]InboxItem, len(s.mu.inbox))
	copy(out, s.mu.inbox)
	return out
}

// InboxFind returns the retained item with the given timeline Seq.
func (s *Session) InboxFind(seq uint64) (InboxItem, bool) {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	for _, it := range s.mu.inbox {
		if it.Seq == seq {
			return it, true
		}
	}
	return InboxItem{}, false
}

// connForWrite returns the live connection only while StateOpen holds.
func (s *Session) connForWrite() (*websocket.Conn, error) {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	if s.mu.state != StateOpen || s.mu.conn == nil {
		return nil, ErrSessionClosed
	}
	return s.mu.conn, nil
}

// SendText sends one logical text message and records the outbound event.
// sensitive marks payloads built from secret material: their stored payloads
// and summaries are redacted at append time via the session Redactor.
func (s *Session) SendText(ctx context.Context, payload string, sensitive bool) error {
	_, err := s.SendTextSeq(ctx, payload, sensitive)
	return err
}

// SendTextSeq is SendText returning the outbound timeline sequence so a
// protocol runner can reference the exact sent fact in its report.
func (s *Session) SendTextSeq(ctx context.Context, payload string, sensitive bool) (uint64, error) {
	return s.sendPayload(ctx, websocket.TextMessage, []byte(payload), sensitive)
}

// SendBinary sends one logical binary message.
func (s *Session) SendBinary(ctx context.Context, payload []byte, sensitive bool) error {
	_, err := s.SendBinarySeq(ctx, payload, sensitive)
	return err
}

// SendBinarySeq is SendBinary returning the outbound timeline sequence.
func (s *Session) SendBinarySeq(ctx context.Context, payload []byte, sensitive bool) (uint64, error) {
	return s.sendPayload(ctx, websocket.BinaryMessage, payload, sensitive)
}

func (s *Session) sendPayload(ctx context.Context, mt int, payload []byte, sensitive bool) (uint64, error) {
	conn, err := s.connForWrite()
	if err != nil {
		return 0, err
	}
	if int64(len(payload)) > s.opts.EffectiveMaxMessageBytes() {
		err := configErrorf("outbound message is %d bytes; the session limit is %d", len(payload), s.opts.EffectiveMaxMessageBytes())
		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirNone,
			Summary:   s.redactError(err).Error(),
		})
		return 0, err
	}
	if ctx != nil {
		select {
		case <-ctx.Done():
			return 0, ctx.Err()
		default:
		}
	}
	data := append([]byte(nil), payload...)
	isText := mt == websocket.TextMessage

	// Sensitive payloads are redacted BEFORE retention: the timeline event
	// and the inbox only ever hold the redacted form, which keeps detail and
	// save views honest without leaking secret material.
	stored := data
	if sensitive {
		stored = s.redactPayload(data, isText)
	}

	s.writeMu.Lock()
	deadline := time.Now().Add(s.opts.EffectiveMessage())
	_ = conn.SetWriteDeadline(deadline)
	err = conn.WriteMessage(mt, data)
	s.writeMu.Unlock()
	if err != nil {
		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirOut,
			Summary:   fmt.Sprintf("send failed: %s", s.redactError(err).Error()),
		})
		s.finish(EndLostConnection, err)
		return 0, runErrorf("send failed: %w", err)
	}

	ev := s.log.Append(stream.Event{
		Kind:      stream.KindSend,
		Direction: stream.DirOut,
		IsText:    isText,
		Payload:   stored,
		Sensitive: sensitive,
		Summary:   frameSummary(stream.DirOut, isText, len(data)),
	})
	s.mu.mu.Lock()
	s.mu.sent++
	s.mu.pushInboxLocked(InboxItem{
		Seq:       ev.Seq,
		Time:      ev.Time,
		Direction: stream.DirOut,
		Kind:      stream.KindSend,
		IsText:    isText,
		Data:      stored,
		Sensitive: sensitive,
	}, s.opts.EffectiveInboxCapacity())
	s.mu.mu.Unlock()
	return ev.Seq, nil
}

// redactPayload scrubs sensitive payload material for retention. Binary
// payloads are masked outright: string redaction cannot scrub arbitrary bytes
// without guessing.
func (s *Session) redactPayload(data []byte, isText bool) []byte {
	if !isText {
		return []byte("***")
	}
	return []byte(s.redact(string(data)))
}

// Ping sends a manual ping carrying a correlation token and waits for the
// matching pong up to the per-message deadline. It records both events with
// the measured RTT; a timeout is an honest fact, not a fake liveness answer.
func (s *Session) Ping(ctx context.Context) (time.Duration, error) {
	conn, err := s.connForWrite()
	if err != nil {
		return 0, err
	}
	token := s.nextOpToken()
	waitC := make(chan struct{})
	start := time.Now()

	s.mu.mu.Lock()
	if s.mu.pending == nil {
		s.mu.pending = map[string]pendingPing{}
	}
	s.mu.pending[token] = pendingPing{sent: start, waitC: waitC}
	s.mu.pings++
	s.mu.mu.Unlock()

	s.writeMu.Lock()
	deadline := time.Now().Add(s.opts.EffectiveMessage())
	_ = conn.SetWriteDeadline(deadline)
	err = conn.WriteControl(websocket.PingMessage, []byte(token), deadline)
	s.writeMu.Unlock()
	if err != nil {
		s.mu.mu.Lock()
		delete(s.mu.pending, token)
		s.mu.mu.Unlock()
		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirOut,
			Summary:   fmt.Sprintf("ping failed: %s", s.redactError(err).Error()),
		})
		s.finish(EndLostConnection, err)
		return 0, runErrorf("ping failed: %w", err)
	}

	s.log.Append(stream.Event{
		Kind:      stream.KindPing,
		Direction: stream.DirOut,
		OpID:      token,
		Payload:   []byte(token),
		IsText:    true,
		Summary:   "ping sent",
	})

	timeout := time.NewTimer(s.opts.EffectiveMessage())
	defer timeout.Stop()
	select {
	case <-waitC:
		return time.Since(start), nil
	case <-timeout.C:
		s.mu.mu.Lock()
		delete(s.mu.pending, token)
		s.mu.mu.Unlock()
		s.log.Append(stream.Event{
			Kind:      stream.KindInfo,
			Direction: stream.DirNone,
			OpID:      token,
			Summary:   fmt.Sprintf("no pong within %s", s.opts.EffectiveMessage()),
		})
		return 0, runErrorf("no pong within %s", s.opts.EffectiveMessage())
	case <-ctx.Done():
		s.mu.mu.Lock()
		delete(s.mu.pending, token)
		s.mu.mu.Unlock()
		return 0, ctx.Err()
	}
}

// nextOpToken returns a unique correlation token for ping→pong matching.
func (s *Session) nextOpToken() string {
	s.mu.mu.Lock()
	defer s.mu.mu.Unlock()
	s.mu.opSeq++
	return fmt.Sprintf("ws-%s-%d", s.log.StreamID(), s.mu.opSeq)
}

// CloseWith performs a graceful close: sends the close frame with the given
// code and (redacted, RFC-bounded) reason, waits up to GracefulCloseWait for
// the peer's close echo, then tears the socket down. The session ends with
// EndUserClosed (unless the server raced its own close first).
func (s *Session) CloseWith(code int, reason string) error {
	conn, err := s.connForWrite()
	if err != nil {
		return err
	}
	bounded := truncateReason(s.redact(reason), maxCloseReasonBytes)

	s.mu.mu.Lock()
	if s.mu.state == StateOpen {
		s.mu.state = StateClosing
	}
	s.mu.mu.Unlock()
	s.log.Append(stream.Event{
		Kind:      stream.KindState,
		Direction: stream.DirNone,
		Summary:   fmt.Sprintf("state: closing (code %d)", code),
	})

	s.writeMu.Lock()
	deadline := time.Now().Add(s.opts.EffectiveMessage())
	err = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(code, bounded), deadline)
	s.writeMu.Unlock()
	if err != nil {
		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirOut,
			Summary:   fmt.Sprintf("close frame send failed: %s", s.redactError(err).Error()),
		})
		s.finish(EndUserClosed, err)
		return runErrorf("close frame send failed: %w", err)
	}

	deadlineTimer := time.NewTimer(GracefulCloseWait)
	defer deadlineTimer.Stop()
	select {
	case <-s.readDone:
	case <-deadlineTimer.C:
		s.log.Append(stream.Event{
			Kind:      stream.KindInfo,
			Direction: stream.DirNone,
			Summary:   fmt.Sprintf("peer close echo not observed within %s; aborting socket", GracefulCloseWait),
		})
		s.abandon(EndUserClosed)
		return nil
	}

	s.mu.mu.Lock()
	if s.mu.closeCode == 0 {
		s.mu.closeCode = code
		s.mu.closeReason = bounded
	}
	if s.mu.end == EndNone {
		s.mu.end = EndUserClosed
	}
	s.mu.mu.Unlock()
	return nil
}

// Terminate aborts the session without a close frame (window close, explicit
// cancel). Idempotent.
func (s *Session) Terminate() {
	s.finish(EndAborted, nil)
}

// finish finalizes session state (idempotent), records the terminal state
// event and frees the socket.
func (s *Session) finish(end EndReason, err error) {
	s.mu.mu.Lock()
	if s.mu.state == StateClosed {
		conn := s.mu.conn
		s.mu.conn = nil
		s.mu.mu.Unlock()
		s.stopTotalTimer()
		if s.sessionCancel != nil {
			s.sessionCancel()
		}
		if conn != nil {
			_ = conn.Close()
		}
		return
	}
	conn := s.mu.conn
	s.mu.conn = nil
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
	s.mu.mu.Unlock()

	s.stopTotalTimer()
	if s.sessionCancel != nil {
		s.sessionCancel()
	}
	if conn != nil {
		_ = conn.Close()
	}
	summary := fmt.Sprintf("state: closed (%s)", end)
	if err != nil {
		summary = fmt.Sprintf("state: error/ended (%s): %s", end, s.redactError(err).Error())
	}
	s.log.Append(stream.Event{
		Kind:      stream.KindState,
		Direction: stream.DirNone,
		Summary:   summary,
	})
}

// abandon tears the socket down without claiming a graceful close.
func (s *Session) abandon(end EndReason) {
	if s.sessionCancel != nil {
		s.sessionCancel()
	}
	s.mu.mu.Lock()
	conn := s.mu.conn
	s.mu.conn = nil
	s.mu.mu.Unlock()
	if conn != nil {
		_ = conn.Close()
	}
	s.finish(end, nil)
}

// startTotalWatchdog arms the session-wide deadline. On fire it records the
// fact, aborts a still-connecting dial, and closes an open session with 1001
// Going Away; the session ends EndTotalDeadline (never a silent hang).
func (s *Session) startTotalWatchdog(total time.Duration) {
	timer := time.AfterFunc(total, func() {
		s.mu.mu.Lock()
		state := s.mu.state
		if state == StateClosed {
			s.mu.mu.Unlock()
			return
		}
		conn := s.mu.conn
		s.mu.state = StateClosing
		if s.mu.end == EndNone {
			s.mu.end = EndTotalDeadline
		}
		s.mu.mu.Unlock()

		s.log.Append(stream.Event{
			Kind:      stream.KindError,
			Direction: stream.DirNone,
			Summary:   fmt.Sprintf("total session deadline (%s) exceeded", total),
		})
		// Abort a dial that is still in flight; an open connection gets a
		// best-effort close frame instead.
		if s.sessionCancel != nil {
			s.sessionCancel()
		}
		if conn != nil {
			deadline := time.Now().Add(s.opts.EffectiveMessage())
			s.writeMu.Lock()
			_ = conn.SetWriteDeadline(deadline)
			_ = conn.WriteControl(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseGoingAway, "total deadline exceeded"), deadline)
			s.writeMu.Unlock()
			select {
			case <-s.readDone:
			case <-time.After(GracefulCloseWait):
			}
		}
		s.finish(EndTotalDeadline, nil)
	})
	s.mu.mu.Lock()
	s.mu.totalT = timer
	s.mu.mu.Unlock()
}

// stopTotalTimer disarms the session-wide deadline (idempotent).
func (s *Session) stopTotalTimer() {
	if s == nil {
		return
	}
	s.mu.mu.Lock()
	timer := s.mu.totalT
	s.mu.totalT = nil
	s.mu.mu.Unlock()
	if timer != nil {
		timer.Stop()
	}
}

// watchParentCtx aborts the session when the caller's context is canceled
// (window/app shutdown): no orphaned socket survives its caller.
func (s *Session) watchParentCtx(parent context.Context) {
	if parent == nil || parent.Done() == nil {
		return
	}
	if s.State() == StateClosed {
		return
	}
	go func() {
		select {
		case <-parent.Done():
			s.Terminate()
		case <-s.readDone:
		}
	}()
}

// truncateReason bounds a close reason to max bytes on a UTF-8 rune boundary.
func truncateReason(reason string, maxBytes int) string {
	if len(reason) <= maxBytes {
		return reason
	}
	cut := maxBytes
	for cut > 0 && !isUTF8Start(reason[cut]) {
		cut--
	}
	return reason[:cut]
}

func isUTF8Start(b byte) bool {
	return b&0xC0 != 0x80
}
