package protocolrun

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	gorilla "github.com/gorilla/websocket"

	"gorequest/internal/core"
	"gorequest/internal/websocket"
)

// defaultFrameQueue bounds frames buffered between the session read pump and
// the runner. A plan always consumes between steps; a full queue is a resource
// fact, never a silent drop.
const defaultFrameQueue = 64

// WebSocketTransport adapts websocket.Session to the plan Transport. Network
// policy, TLS, proxy, cookies and secret handling stay in the websocket
// package; this adapter only moves bounded frames.
type WebSocketTransport struct {
	base websocket.Options
	lim  Limits

	mu       sync.Mutex
	sess     *websocket.Session
	frames   chan Message
	done     <-chan struct{}
	overflow string
}

// NewWebSocketTransport prepares an adapter from the caller's resolved network
// options. Nothing connects until the plan's connect step runs.
func NewWebSocketTransport(base websocket.Options, limits Limits) *WebSocketTransport {
	return &WebSocketTransport{base: base, lim: limits}
}

// Connect performs the plan's connect step. It records the session facts on
// failure exactly like websocket.Connect.
func (t *WebSocketTransport) Connect(ctx context.Context, spec ConnectSpec) error {
	t.mu.Lock()
	if t.sess != nil {
		t.mu.Unlock()
		return fmt.Errorf("websocket transport is already connected")
	}
	t.mu.Unlock()

	opts := t.base
	opts.URL = spec.URL
	opts.Subprotocols = append([]string(nil), spec.Subprotocols...)
	opts.Headers = make([][2]string, 0, len(spec.Headers))
	for _, h := range spec.Headers {
		opts.Headers = append(opts.Headers, [2]string{h.Name, h.Value})
	}
	opts.MaxMessageBytes = t.lim.EffectiveMaxMessageBytes()
	if opts.Redactor == nil {
		opts.Redactor = core.NewRedactor()
	}

	frames := make(chan Message, defaultFrameQueue)
	ready := make(chan struct{})
	var sess *websocket.Session
	opts.FrameHandler = func(mt int, data []byte) {
		<-ready
		if mt != gorilla.TextMessage && mt != gorilla.BinaryMessage {
			return
		}
		seq := uint64(0)
		if sess != nil {
			seq = uint64(sess.Events().Latest())
		}
		msg := Message{Seq: seq, Text: mt == gorilla.TextMessage, Data: append([]byte(nil), data...)}
		select {
		case frames <- msg:
		default:
			t.setOverflow("websocket frame queue overflowed")
		}
	}

	connected, err := websocket.Connect(ctx, opts)
	sess = connected
	close(ready)

	t.mu.Lock()
	t.sess = connected
	t.frames = frames
	if connected != nil {
		t.done = connected.Done()
	}
	t.mu.Unlock()
	if err != nil {
		return err
	}
	if sn := connected.Snapshot(); sn.State != websocket.StateOpen {
		return fmt.Errorf("websocket session did not open (state %s)", sn.State)
	}
	if len(spec.Subprotocols) > 0 && connected.Snapshot().Subprotocol == "" {
		_ = connected.CloseWith(1002, "no subprotocol negotiated")
		return fmt.Errorf("server did not negotiate any offered subprotocol")
	}
	return nil
}

func (t *WebSocketTransport) setOverflow(reason string) {
	t.mu.Lock()
	if t.overflow == "" {
		t.overflow = reason
	}
	t.mu.Unlock()
}

// Send writes one message; the returned sequence references the retained
// outbound event.
func (t *WebSocketTransport) Send(ctx context.Context, payload SendPayload) (SentFact, error) {
	t.mu.Lock()
	sess := t.sess
	t.mu.Unlock()
	if sess == nil {
		return SentFact{}, errors.New("websocket transport is not connected")
	}
	var seq uint64
	var err error
	if payload.Text {
		seq, err = sess.SendTextSeq(ctx, string(payload.Data), payload.Sensitive)
	} else {
		seq, err = sess.SendBinarySeq(ctx, payload.Data, payload.Sensitive)
	}
	if err != nil {
		return SentFact{}, err
	}
	return SentFact{Seq: seq, Bytes: len(payload.Data)}, nil
}

// Receive returns the next inbound message, honoring the caller's deadline.
func (t *WebSocketTransport) Receive(ctx context.Context) (Message, error) {
	t.mu.Lock()
	frames := t.frames
	done := t.done
	t.mu.Unlock()
	if frames == nil {
		return Message{}, errors.New("websocket transport is not connected")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case msg := <-frames:
		return msg, nil
	case <-done:
		// Prefer a frame that raced the close so a final message is not lost.
		select {
		case msg := <-frames:
			return msg, nil
		default:
		}
		return Message{}, t.closedError()
	case <-ctx.Done():
		return Message{}, ctx.Err()
	}
}

func (t *WebSocketTransport) closedError() error {
	if msg := t.overflowMessage(); msg != "" {
		return errors.New(msg)
	}
	t.mu.Lock()
	sess := t.sess
	t.mu.Unlock()
	if sess == nil {
		return websocket.ErrSessionClosed
	}
	sn := sess.Snapshot()
	if sn.CloseCode != 0 {
		reason := strings.TrimSpace(sn.CloseReason)
		if reason == "" {
			reason = string(sn.End)
		}
		return fmt.Errorf("websocket closed with code %d: %s", sn.CloseCode, reason)
	}
	if sn.LastErr != "" {
		return fmt.Errorf("websocket connection lost: %s", sn.LastErr)
	}
	return websocket.ErrSessionClosed
}

func (t *WebSocketTransport) overflowMessage() string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.overflow
}

// Close performs a graceful close and records the plan's code/reason.
func (t *WebSocketTransport) Close(code int, reason string) error {
	t.mu.Lock()
	sess := t.sess
	t.mu.Unlock()
	if sess == nil {
		return nil
	}
	return sess.CloseWith(code, reason)
}

// CloseFacts returns the terminal close code/reason observed by the session.
func (t *WebSocketTransport) CloseFacts() (int, string) {
	t.mu.Lock()
	sess := t.sess
	t.mu.Unlock()
	if sess == nil {
		return 0, ""
	}
	sn := sess.Snapshot()
	return sn.CloseCode, sn.CloseReason
}

// Session exposes the live session for desktop facts (state, close code,
// counters). It is nil before connect.
func (t *WebSocketTransport) Session() *websocket.Session {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sess
}
