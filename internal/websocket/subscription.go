package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"

	"gorequest/internal/core"
	"gorequest/internal/stream"
)

// GraphQLTransportWS implements the "graphql-transport-ws" subprotocol
// (graphql-ws PROTOCOL.md): one WebSocket connection carrying connection_init,
// connection_ack, ping/pong and multiplexed subscribe/next/error/complete
// operations keyed by operation id. The legacy same-named "graphql-ws"
// subprotocol is deliberately not accepted, and nothing here reconnects
// silently.
const (
	GraphQLTransportWSSubprotocol = "graphql-transport-ws"

	DefaultSubscriptionAckTimeout  = 10 * time.Second
	DefaultSubscriptionQueue       = 256
	DefaultSubscriptionMaxMessages = 10000
)

var (
	// ErrSubscriptionNotAcknowledged is returned when an operation is attempted
	// before the server's connection_ack.
	ErrSubscriptionNotAcknowledged = errors.New("graphql subscription connection is not acknowledged")
	// ErrSubscriptionComplete reports that the server ended the operation.
	ErrSubscriptionComplete = errors.New("graphql subscription completed")
	// ErrSubscriptionClosed reports that the connection ended before the
	// operation completed (no silent reconnect — the caller decides).
	ErrSubscriptionClosed = errors.New("graphql subscription connection is closed")
)

// SubscriptionConfig carries the protocol-level options. Connection options
// (URL, headers, auth, TLS, proxy, policy, cookies) stay in Options.
type SubscriptionConfig struct {
	// InitPayload is sent in connection_init. Authentication material belongs
	// here; set InitPayloadSensitive so it is redacted from the timeline.
	InitPayload          map[string]any
	InitPayloadSensitive bool
	// AckTimeout bounds the wait for connection_ack. Default 10s.
	AckTimeout time.Duration
	// QueueCapacity bounds each operation's delivered-message queue. A full
	// queue fails that operation with an explicit resource error.
	QueueCapacity int
	// MaxMessages bounds messages accepted per operation. Default 10000.
	MaxMessages int
}

func (c SubscriptionConfig) effectiveAckTimeout() time.Duration {
	if c.AckTimeout > 0 {
		return c.AckTimeout
	}
	return DefaultSubscriptionAckTimeout
}

func (c SubscriptionConfig) effectiveQueue() int {
	if c.QueueCapacity > 0 {
		return c.QueueCapacity
	}
	return DefaultSubscriptionQueue
}

func (c SubscriptionConfig) effectiveMaxMessages() int {
	if c.MaxMessages > 0 {
		return c.MaxMessages
	}
	return DefaultSubscriptionMaxMessages
}

// subscriptionSender is the subset of *Session the protocol driver needs.
// Tests substitute a fake.
type subscriptionSender interface {
	SendText(ctx context.Context, payload string, sensitive bool) error
	CloseWith(code int, reason string) error
	Terminate()
	Snapshot() Snapshot
	Events() *stream.Log
}

// SubscriptionSnapshot is the protocol driver's own fact view. Transport facts
// (TLS, close code, counters) come from Session.Snapshot.
type SubscriptionSnapshot struct {
	Acked          bool
	Closed         bool
	ProtocolErrors []string
	PongsReceived  uint64
	Operations     int
	Transport      Snapshot
}

// Subscription is one graphql-transport-ws connection.
type Subscription struct {
	cfg    SubscriptionConfig
	red    *core.Redactor
	sender subscriptionSender
	log    *stream.Log
	done   <-chan struct{}

	mu             sync.Mutex
	acked          bool
	ackErr         error
	ackCh          chan struct{}
	ackOnce        sync.Once
	ops            map[string]*SubscriptionOperation
	nextID         uint64
	closed         bool
	protocolErrors []string
	pongsReceived  uint64
}

func newSubscription(cfg SubscriptionConfig, red *core.Redactor) *Subscription {
	return &Subscription{
		cfg:   cfg,
		red:   red,
		ackCh: make(chan struct{}),
		ops:   map[string]*SubscriptionOperation{},
	}
}

// ConnectSubscription dials with the graphql-transport-ws subprotocol, sends
// connection_init, waits for connection_ack and returns a ready client. The
// returned *Subscription is non-nil even on failure when a session recorded
// facts (that mirrors Session.Connect).
func ConnectSubscription(ctx context.Context, opts Options, cfg SubscriptionConfig) (*Subscription, error) {
	switch {
	case len(opts.Subprotocols) == 0:
		opts.Subprotocols = []string{GraphQLTransportWSSubprotocol}
	case len(opts.Subprotocols) == 1 && strings.EqualFold(strings.TrimSpace(opts.Subprotocols[0]), GraphQLTransportWSSubprotocol):
		opts.Subprotocols = []string{GraphQLTransportWSSubprotocol}
	default:
		return nil, configErrorf("graphql subscriptions require the %q subprotocol, got %v", GraphQLTransportWSSubprotocol, opts.Subprotocols)
	}
	if opts.Redactor == nil {
		opts.Redactor = core.NewRedactor()
	}
	sub := newSubscription(cfg, opts.Redactor)
	ready := make(chan struct{})
	opts.FrameHandler = func(mt int, data []byte) {
		// Frames can only arrive after the dial succeeds; the session assigns
		// itself before returning. Wait until ConnectSubscription attached the
		// sender so a prompt connection_ack is never lost.
		<-ready
		sub.onFrame(mt, data)
	}
	// Every retained inbound payload passes through the redactor: auth
	// material may echo back from the server.
	opts.InboundSensitive = opts.InboundSensitive || cfg.InitPayloadSensitive

	sess, err := Connect(ctx, opts)
	sub.sender = sess
	sub.log = sess.Events()
	sub.done = sess.Done()
	close(ready)
	if err != nil {
		return sub, err
	}
	if got := sess.Snapshot().Subprotocol; got != GraphQLTransportWSSubprotocol {
		sess.Terminate()
		return sub, runErrorf("server did not negotiate the %q subprotocol (got %q)", GraphQLTransportWSSubprotocol, got)
	}
	go sub.watchDone()
	if err := sub.sendInit(ctx); err != nil {
		sess.Terminate()
		return sub, err
	}
	if err := sub.waitAck(ctx); err != nil {
		return sub, err
	}
	return sub, nil
}

// watchDone fails every active operation when the transport ends on its own,
// so no waiter hangs after a lost connection or an explicit cancellation.
func (c *Subscription) watchDone() {
	if c.done == nil {
		return
	}
	<-c.done
	c.mu.Lock()
	c.closed = true
	ops := make([]*SubscriptionOperation, 0, len(c.ops))
	for _, op := range c.ops {
		ops = append(ops, op)
	}
	c.mu.Unlock()
	err := c.closedError()
	c.signalAck(err)
	for _, op := range ops {
		op.fail(err)
	}
}

func (c *Subscription) closedError() error {
	if c.sender == nil {
		return ErrSubscriptionClosed
	}
	sn := c.sender.Snapshot()
	switch {
	case sn.CloseCode != 0:
		reason := strings.TrimSpace(sn.CloseReason)
		if reason == "" {
			reason = string(sn.End)
		}
		return fmt.Errorf("%w (close code %d: %s)", ErrSubscriptionClosed, sn.CloseCode, reason)
	case sn.LastErr != "":
		return fmt.Errorf("%w: %s", ErrSubscriptionClosed, sn.LastErr)
	default:
		return ErrSubscriptionClosed
	}
}

// sendInit transmits connection_init with the optional sensitive payload.
func (c *Subscription) sendInit(ctx context.Context) error {
	msg := map[string]any{"type": "connection_init"}
	sensitive := c.cfg.InitPayloadSensitive
	if len(c.cfg.InitPayload) > 0 {
		msg["payload"] = c.cfg.InitPayload
		if sensitive {
			registerJSONStrings(c.red, c.cfg.InitPayload)
		}
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return configErrorf("graphql connection_init: %v", err)
	}
	if err := c.sender.SendText(ctx, string(body), sensitive); err != nil {
		return err
	}
	c.fact(stream.KindInfo, "graphql connection_init sent")
	return nil
}

func (c *Subscription) waitAck(ctx context.Context) error {
	timeout := c.cfg.effectiveAckTimeout()
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-c.ackCh:
		c.mu.Lock()
		err := c.ackErr
		c.mu.Unlock()
		return err
	case <-c.done:
		return c.closedError()
	case <-timer.C:
		err := runErrorf("graphql subscription connection was not acknowledged within %s", timeout)
		c.fact(stream.KindError, err.Error())
		if c.sender != nil {
			c.sender.Terminate()
		}
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *Subscription) signalAck(err error) {
	c.ackOnce.Do(func() {
		c.mu.Lock()
		c.ackErr = err
		if err == nil {
			c.acked = true
		}
		c.mu.Unlock()
		close(c.ackCh)
	})
}

// onFrame is the transport callback. It runs on the session read pump and must
// stay fast; protocol violations terminate the connection explicitly.
func (c *Subscription) onFrame(mt int, data []byte) {
	if mt != websocket.TextMessage {
		c.protocolError("received a non-text message (type %d)", mt)
		return
	}
	var env struct {
		Type    string          `json:"type"`
		ID      string          `json:"id"`
		Payload json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &env); err != nil {
		c.protocolError("message is not valid JSON: %v", err)
		return
	}
	if strings.TrimSpace(env.Type) == "" {
		c.protocolError("message has no type")
		return
	}
	switch env.Type {
	case "connection_ack":
		c.mu.Lock()
		already := c.acked
		c.mu.Unlock()
		if already {
			c.fact(stream.KindInfo, "ignored duplicate connection_ack")
			return
		}
		c.fact(stream.KindInfo, "graphql connection acknowledged")
		c.signalAck(nil)
	case "connection_error":
		err := runErrorf("graphql connection_init rejected: %s", c.payloadMessage(env.Payload))
		c.fact(stream.KindError, err.Error())
		c.signalAck(err)
		if c.sender != nil {
			c.sender.Terminate()
		}
	case "ping":
		c.replyPong(env.Payload)
	case "pong":
		c.mu.Lock()
		c.pongsReceived++ // server pongs are expected only after our pings; count facts.
		c.mu.Unlock()
	case "next", "error", "complete":
		c.route(env.Type, env.ID, env.Payload)
	default:
		c.protocolError("unknown message type %q", env.Type)
	}
}

// replyPong answers a server ping promptly on its own goroutine so the read
// pump never waits on a write.
func (c *Subscription) replyPong(payload json.RawMessage) {
	sender := c.sender
	if sender == nil {
		return
	}
	go func() {
		msg := map[string]any{"type": "pong"}
		if len(payload) > 0 && string(payload) != "null" {
			msg["payload"] = payload
		}
		body, err := json.Marshal(msg)
		if err != nil {
			return
		}
		if err := sender.SendText(context.Background(), string(body), false); err != nil {
			c.fact(stream.KindError, fmt.Sprintf("graphql pong failed: %s", c.redact(err.Error())))
			return
		}
		c.fact(stream.KindInfo, "graphql pong sent")
	}()
}

// route delivers one operation-scoped message. Unknown/complete operation ids
// are ignored with a fact, as the protocol permits.
func (c *Subscription) route(typ, id string, payload json.RawMessage) {
	if strings.TrimSpace(id) == "" {
		c.protocolError("%s message has no id", typ)
		return
	}
	c.mu.Lock()
	op := c.ops[id]
	c.mu.Unlock()
	if op == nil {
		c.fact(stream.KindInfo, fmt.Sprintf("ignored %s for unknown or completed operation %q", typ, id))
		return
	}
	switch typ {
	case "next":
		seq := uint64(0)
		if c.log != nil {
			seq = uint64(c.log.Latest())
		}
		if !op.deliver(SubscriptionMessage{Type: "next", Payload: payload, Seq: seq, Time: time.Now().UTC()}) {
			return
		}
	case "error":
		seq := uint64(0)
		if c.log != nil {
			seq = uint64(c.log.Latest())
		}
		op.deliver(SubscriptionMessage{Type: "error", Payload: payload, Seq: seq, Time: time.Now().UTC()})
		c.fact(stream.KindError, fmt.Sprintf("graphql operation %q returned errors: %s", id, c.payloadMessage(payload)))
		op.complete()
	case "complete":
		op.complete()
	}
}

func (c *Subscription) protocolError(format string, args ...any) {
	msg := c.redact(fmt.Sprintf(format, args...))
	c.mu.Lock()
	c.protocolErrors = append(c.protocolErrors, msg)
	c.mu.Unlock()
	c.fact(stream.KindError, "graphql-transport-ws protocol error: "+msg)
	if c.sender != nil {
		c.sender.Terminate()
	}
}

func (c *Subscription) fact(kind stream.Kind, summary string) {
	if c.log == nil {
		return
	}
	c.log.Append(stream.Event{Kind: kind, Direction: stream.DirNone, Summary: c.redact(summary)})
}

func (c *Subscription) redact(s string) string {
	if c.red == nil {
		return s
	}
	return c.red.RedactString(s)
}

// payloadMessage summarizes a server payload without retaining the raw body.
func (c *Subscription) payloadMessage(payload json.RawMessage) string {
	if len(payload) == 0 || string(payload) == "null" {
		return "(no payload)"
	}
	var errs []struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(payload, &errs) == nil && len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Message)
		}
		return truncateSummary(c.redact(strings.Join(msgs, "; ")))
	}
	return truncateSummary(c.redact(string(payload)))
}

func truncateSummary(s string) string {
	const max = 512
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !isUTF8Start(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

// Subscribe starts one operation. sensitive marks a payload built from secret
// material so the session redacts the retained copy.
func (c *Subscription) Subscribe(ctx context.Context, query, operationName string, variables json.RawMessage, sensitive bool) (*SubscriptionOperation, error) {
	if strings.TrimSpace(query) == "" {
		return nil, configErrorf("graphql subscription document is required")
	}
	if len(query) > core.MaxGraphQLDocumentBytes {
		return nil, configErrorf("graphql document exceeds %d bytes", core.MaxGraphQLDocumentBytes)
	}
	vars := variables
	if len(vars) == 0 || strings.TrimSpace(string(vars)) == "null" {
		vars = json.RawMessage("{}")
	}
	if !json.Valid(vars) {
		return nil, configErrorf("graphql subscription variables must be valid JSON")
	}

	c.mu.Lock()
	if !c.acked {
		c.mu.Unlock()
		return nil, ErrSubscriptionNotAcknowledged
	}
	if c.closed {
		c.mu.Unlock()
		return nil, ErrSubscriptionClosed
	}
	c.nextID++
	id := strconv.FormatUint(c.nextID, 10)
	op := newSubscriptionOperation(c, id, c.cfg.effectiveQueue(), c.cfg.effectiveMaxMessages())
	c.ops[id] = op
	c.mu.Unlock()

	payload := struct {
		Query         string          `json:"query"`
		OperationName string          `json:"operationName,omitempty"`
		Variables     json.RawMessage `json:"variables"`
	}{Query: query, OperationName: strings.TrimSpace(operationName), Variables: vars}
	body, err := json.Marshal(struct {
		ID      string `json:"id"`
		Type    string `json:"type"`
		Payload any    `json:"payload"`
	}{ID: id, Type: "subscribe", Payload: payload})
	if err != nil {
		c.removeOp(id)
		return nil, configErrorf("graphql subscribe message: %v", err)
	}
	if sensitive {
		registerJSONStrings(c.red, json.RawMessage(vars))
	}
	if err := c.sender.SendText(ctx, string(body), sensitive); err != nil {
		c.removeOp(id)
		return nil, err
	}
	c.fact(stream.KindInfo, fmt.Sprintf("graphql operation %q subscribed", id))
	return op, nil
}

func (c *Subscription) removeOp(id string) {
	c.mu.Lock()
	delete(c.ops, id)
	c.mu.Unlock()
}

// sendComplete tells the server to stop one operation. A closed session is not
// an error here: the local operation is already finished.
func (c *Subscription) sendComplete(id string) error {
	if c.sender == nil {
		return nil
	}
	body, err := json.Marshal(map[string]string{"id": id, "type": "complete"})
	if err != nil {
		return err
	}
	if err := c.sender.SendText(context.Background(), string(body), false); err != nil && !errors.Is(err, ErrSessionClosed) {
		return err
	}
	c.fact(stream.KindInfo, fmt.Sprintf("graphql operation %q canceled", id))
	return nil
}

// Close completes every active operation and performs a graceful WebSocket
// close with the given code and reason.
func (c *Subscription) Close(code int, reason string) error {
	c.completeAll()
	if c.sender == nil {
		return nil
	}
	return c.sender.CloseWith(code, reason)
}

// Terminate aborts the connection without a close handshake.
func (c *Subscription) Terminate() {
	if c.sender != nil {
		c.sender.Terminate()
	}
}

func (c *Subscription) completeAll() {
	c.mu.Lock()
	ops := make([]*SubscriptionOperation, 0, len(c.ops))
	for _, op := range c.ops {
		ops = append(ops, op)
	}
	c.mu.Unlock()
	for _, op := range ops {
		op.cancel(false)
	}
}

// Snapshot returns the protocol driver's current facts.
func (c *Subscription) Snapshot() SubscriptionSnapshot {
	c.mu.Lock()
	sn := SubscriptionSnapshot{
		Acked:          c.acked,
		Closed:         c.closed,
		ProtocolErrors: append([]string(nil), c.protocolErrors...),
		PongsReceived:  c.pongsReceived,
		Operations:     len(c.ops),
	}
	c.mu.Unlock()
	if c.sender != nil {
		sn.Transport = c.sender.Snapshot()
	}
	return sn
}

// Subprotocol returns the negotiated subprotocol.
func (c *Subscription) Subprotocol() string {
	if c.sender == nil {
		return ""
	}
	return c.sender.Snapshot().Subprotocol
}

// SubscriptionMessage is one delivered operation message. Seq is the session
// timeline sequence of the frame that carried it.
type SubscriptionMessage struct {
	Type    string // next | error
	Payload json.RawMessage
	Seq     uint64
	Time    time.Time
}

// SubscriptionOperation is one multiplexed operation. Messages are delivered
// through a bounded queue; a slow consumer or a message flood fails the
// operation explicitly instead of growing memory.
type SubscriptionOperation struct {
	parent *Subscription
	ID     string
	ch     chan SubscriptionMessage
	done   chan struct{}
	max    int

	mu       sync.Mutex
	finished bool
	count    int
	err      error
}

func newSubscriptionOperation(parent *Subscription, id string, queue, max int) *SubscriptionOperation {
	if queue <= 0 {
		queue = DefaultSubscriptionQueue
	}
	return &SubscriptionOperation{
		parent: parent,
		ID:     id,
		ch:     make(chan SubscriptionMessage, queue),
		done:   make(chan struct{}),
		max:    max,
	}
}

// deliver enqueues a message without blocking. The message count bound turns a
// flood into an explicit failure for this operation only.
func (o *SubscriptionOperation) deliver(msg SubscriptionMessage) bool {
	o.mu.Lock()
	if o.finished {
		o.mu.Unlock()
		return false
	}
	o.count++
	if o.max > 0 && o.count > o.max {
		o.mu.Unlock()
		o.fail(runErrorf("graphql operation %q exceeded the %d-message bound", o.ID, o.max))
		return false
	}
	select {
	case o.ch <- msg:
		o.mu.Unlock()
		return true
	default:
		o.mu.Unlock()
		o.fail(runErrorf("graphql operation %q delivery queue is full", o.ID))
		return false
	}
}

// Next returns the next message. It returns ErrSubscriptionComplete after the
// server completed the operation and the transport error for a broken
// connection. A server error payload is returned as a normal message with
// Type "error" so callers can report the GraphQL facts.
func (o *SubscriptionOperation) Next(ctx context.Context) (SubscriptionMessage, error) {
	select {
	case msg := <-o.ch:
		return msg, nil
	default:
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case msg := <-o.ch:
		return msg, nil
	case <-o.done:
		select {
		case msg := <-o.ch:
			return msg, nil
		default:
		}
		o.mu.Lock()
		err := o.err
		o.mu.Unlock()
		if err != nil {
			return SubscriptionMessage{}, err
		}
		return SubscriptionMessage{}, ErrSubscriptionComplete
	case <-ctx.Done():
		return SubscriptionMessage{}, ctx.Err()
	}
}

// Cancel stops the operation locally and tells the server with a client
// complete message.
func (o *SubscriptionOperation) Cancel() error {
	return o.cancel(true)
}

func (o *SubscriptionOperation) cancel(notifyServer bool) error {
	o.mu.Lock()
	already := o.finished
	o.finished = true
	o.mu.Unlock()
	if already {
		return nil
	}
	close(o.done)
	o.parent.removeOp(o.ID)
	if notifyServer {
		return o.parent.sendComplete(o.ID)
	}
	return nil
}

// complete ends the operation after the server's complete/error message.
func (o *SubscriptionOperation) complete() {
	o.mu.Lock()
	already := o.finished
	o.finished = true
	o.mu.Unlock()
	if already {
		return
	}
	close(o.done)
	o.parent.removeOp(o.ID)
}

// fail ends the operation with a transport/resource error.
func (o *SubscriptionOperation) fail(err error) {
	o.mu.Lock()
	already := o.finished
	if !already {
		o.finished = true
		o.err = err
	}
	o.mu.Unlock()
	if already {
		return
	}
	close(o.done)
	o.parent.removeOp(o.ID)
}

// Done is closed when the operation finished for any reason.
func (o *SubscriptionOperation) Done() <-chan struct{} { return o.done }

// registerJSONStrings registers every string value reachable in v with the
// redactor. Over-registration is safe (it only broadens scrubbing).
func registerJSONStrings(red *core.Redactor, v any) {
	if red == nil {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		for _, child := range t {
			registerJSONStrings(red, child)
		}
	case []any:
		for _, child := range t {
			registerJSONStrings(red, child)
		}
	case string:
		if t != "" {
			red.RegisterSecret(t)
		}
	case json.RawMessage:
		var decoded any
		if json.Unmarshal(t, &decoded) == nil {
			registerJSONStrings(red, decoded)
		}
	}
}
