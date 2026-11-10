package websocket

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	"gorequest/internal/core"
	"gorequest/internal/stream"
)

type sentFrame struct {
	body      string
	sensitive bool
}

type fakeSubSender struct {
	mu         sync.Mutex
	sent       []sentFrame
	terminated bool
	closed     bool
	closeCode  int
	closeWhy   string
	sn         Snapshot
	log        *stream.Log
}

func (f *fakeSubSender) SendText(_ context.Context, payload string, sensitive bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, sentFrame{body: payload, sensitive: sensitive})
	return nil
}

func (f *fakeSubSender) CloseWith(code int, reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed, f.closeCode, f.closeWhy = true, code, reason
	return nil
}

func (f *fakeSubSender) Terminate() {
	f.mu.Lock()
	f.terminated = true
	f.mu.Unlock()
}

func (f *fakeSubSender) Snapshot() Snapshot {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.sn
}

func (f *fakeSubSender) Events() *stream.Log { return f.log }

func (f *fakeSubSender) sentBodies() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, 0, len(f.sent))
	for _, s := range f.sent {
		out = append(out, s.body)
	}
	return out
}

func newTestSubscription(t *testing.T, cfg SubscriptionConfig) (*Subscription, *fakeSubSender) {
	t.Helper()
	sub := newSubscription(cfg, core.NewRedactor())
	fake := &fakeSubSender{log: stream.NewLog(stream.Options{StreamID: "test"})}
	sub.sender = fake
	sub.log = fake.log
	return sub, fake
}

func mustAcked(t *testing.T, sub *Subscription) {
	t.Helper()
	sub.signalAck(nil)
	if err := sub.waitAck(context.Background()); err != nil {
		t.Fatalf("ack: %v", err)
	}
}

func TestSubscriptionRoutingIsolationAndComplete(t *testing.T) {
	sub, _ := newTestSubscription(t, SubscriptionConfig{})
	mustAcked(t, sub)
	ctx := context.Background()

	op1, err := sub.Subscribe(ctx, "subscription A { a }", "", nil, false)
	if err != nil {
		t.Fatalf("subscribe 1: %v", err)
	}
	op2, err := sub.Subscribe(ctx, "subscription B { b }", "", []byte(`{"x":1}`), true)
	if err != nil {
		t.Fatalf("subscribe 2: %v", err)
	}
	if op1.ID == op2.ID {
		t.Fatalf("operation ids must be unique: %q", op1.ID)
	}

	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op1.ID+`","type":"next","payload":{"data":{"a":1}}}`))
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op2.ID+`","type":"next","payload":{"data":{"b":2}}}`))
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op1.ID+`","type":"next","payload":{"data":{"a":3}}}`))

	msg, err := op1.Next(ctx)
	if err != nil || msg.Type != "next" || !strings.Contains(string(msg.Payload), `"a":1`) {
		t.Fatalf("op1 first message: %+v err=%v", msg, err)
	}
	msg2, err := op2.Next(ctx)
	if err != nil || !strings.Contains(string(msg2.Payload), `"b":2`) {
		t.Fatalf("op2 message: %+v err=%v", msg2, err)
	}
	msg3, err := op1.Next(ctx)
	if err != nil || !strings.Contains(string(msg3.Payload), `"a":3`) {
		t.Fatalf("op1 second message: %+v err=%v", msg3, err)
	}
	if msg.Seq == 0 || msg3.Seq == 0 {
		t.Fatal("messages must carry the session timeline sequence")
	}

	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op1.ID+`","type":"complete"}`))
	if _, err := op1.Next(ctx); !errors.Is(err, ErrSubscriptionComplete) {
		t.Fatalf("op1 after complete = %v", err)
	}
	// Completing op1 must not touch op2.
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op2.ID+`","type":"next","payload":{"data":{"b":4}}}`))
	if msg, err := op2.Next(ctx); err != nil || !strings.Contains(string(msg.Payload), `"b":4`) {
		t.Fatalf("op2 after op1 complete: %+v err=%v", msg, err)
	}
}

func TestSubscriptionServerErrorAndProtocolErrors(t *testing.T) {
	sub, fake := newTestSubscription(t, SubscriptionConfig{})
	mustAcked(t, sub)
	op, err := sub.Subscribe(context.Background(), "subscription A { a }", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op.ID+`","type":"error","payload":[{"message":"boom"}]}`))
	msg, err := op.Next(context.Background())
	if err != nil || msg.Type != "error" {
		t.Fatalf("server error message: %+v err=%v", msg, err)
	}
	if _, err := op.Next(context.Background()); !errors.Is(err, ErrSubscriptionComplete) {
		t.Fatalf("after error payload = %v", err)
	}

	if _, err := sub.Subscribe(context.Background(), "subscription B { b }", "", nil, false); err != nil {
		t.Fatal(err)
	}
	sub.onFrame(gorilla.TextMessage, []byte(`[]`))
	if !fake.terminated {
		t.Fatal("invalid JSON frame must terminate the connection")
	}
	if got := sub.Snapshot().ProtocolErrors; len(got) == 0 || !strings.Contains(got[0], "not valid JSON") {
		t.Fatalf("protocol errors = %v", got)
	}
	sub2, fake2 := newTestSubscription(t, SubscriptionConfig{})
	mustAcked(t, sub2)
	sub2.onFrame(gorilla.BinaryMessage, []byte{1, 2, 3})
	if !fake2.terminated || len(sub2.Snapshot().ProtocolErrors) == 0 {
		t.Fatal("binary frame must be a protocol error for graphql-transport-ws")
	}
	sub3, fake3 := newTestSubscription(t, SubscriptionConfig{})
	mustAcked(t, sub3)
	sub3.onFrame(gorilla.TextMessage, []byte(`{"type":"connection_ack"}`))
	if fake3.terminated {
		t.Fatal("duplicate ack must be ignored, not terminate")
	}
}

func TestSubscriptionPingPongAndUnknownOperation(t *testing.T) {
	sub, fake := newTestSubscription(t, SubscriptionConfig{})
	mustAcked(t, sub)
	sub.onFrame(gorilla.TextMessage, []byte(`{"type":"ping","payload":{"n":1}}`))
	if !waitFor(t, time.Second, func() bool {
		for _, body := range fake.sentBodies() {
			if strings.Contains(body, `"type":"pong"`) && strings.Contains(body, `"n":1`) {
				return true
			}
		}
		return false
	}) {
		t.Fatal("server ping was not answered with a pong")
	}
	// Unknown operation ids are ignored with a fact, never fatal.
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"nope","type":"next","payload":{}}`))
	if fake.terminated {
		t.Fatal("unknown operation id must not terminate the connection")
	}
	var ignored bool
	for _, ev := range sub.log.Snapshot() {
		if strings.Contains(ev.Summary, "ignored next for unknown") {
			ignored = true
		}
	}
	if !ignored {
		t.Fatal("unknown operation id was not recorded")
	}
}

func TestSubscriptionQueueFloodAndMessageBound(t *testing.T) {
	sub, _ := newTestSubscription(t, SubscriptionConfig{QueueCapacity: 1, MaxMessages: 10})
	mustAcked(t, sub)
	op, err := sub.Subscribe(context.Background(), "subscription A { a }", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op.ID+`","type":"next","payload":{"n":1}}`))
	sub.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op.ID+`","type":"next","payload":{"n":2}}`))
	msg, err := op.Next(context.Background())
	if err != nil || !strings.Contains(string(msg.Payload), `"n":1`) {
		t.Fatalf("first message: %+v err=%v", msg, err)
	}
	if _, err := op.Next(context.Background()); err == nil || !strings.Contains(err.Error(), "queue is full") {
		t.Fatalf("queue overflow = %v", err)
	}

	sub2, _ := newTestSubscription(t, SubscriptionConfig{QueueCapacity: 32, MaxMessages: 2})
	mustAcked(t, sub2)
	op2, _ := sub2.Subscribe(context.Background(), "subscription A { a }", "", nil, false)
	for i := 0; i < 3; i++ {
		sub2.onFrame(gorilla.TextMessage, []byte(`{"id":"`+op2.ID+`","type":"next","payload":{}}`))
	}
	var sawBound bool
	for i := 0; i < 3; i++ {
		if _, err := op2.Next(context.Background()); err != nil && strings.Contains(err.Error(), "message bound") {
			sawBound = true
			break
		}
	}
	if !sawBound {
		t.Fatal("per-operation message bound was not enforced")
	}
}

func TestSubscriptionCancelSendsComplete(t *testing.T) {
	sub, fake := newTestSubscription(t, SubscriptionConfig{})
	mustAcked(t, sub)
	op, err := sub.Subscribe(context.Background(), "subscription A { a }", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	if err := op.Cancel(); err != nil {
		t.Fatal(err)
	}
	if _, err := op.Next(context.Background()); !errors.Is(err, ErrSubscriptionComplete) {
		t.Fatalf("after cancel = %v", err)
	}
	var complete bool
	for _, body := range fake.sentBodies() {
		if strings.Contains(body, `"type":"complete"`) && strings.Contains(body, `"id":"`+op.ID+`"`) {
			complete = true
		}
	}
	if !complete {
		t.Fatal("cancel must send a client complete message")
	}
}

func TestSubscriptionAckTimeoutAndInitRejection(t *testing.T) {
	sub, fake := newTestSubscription(t, SubscriptionConfig{AckTimeout: 25 * time.Millisecond})
	err := sub.waitAck(context.Background())
	if err == nil || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("ack timeout = %v", err)
	}
	if !fake.terminated {
		t.Fatal("ack timeout must terminate the connection")
	}

	sub2, fake2 := newTestSubscription(t, SubscriptionConfig{})
	sub2.onFrame(gorilla.TextMessage, []byte(`{"type":"connection_error","payload":[{"message":"unauthorized"}]}`))
	if err := sub2.waitAck(context.Background()); err == nil || !strings.Contains(err.Error(), "unauthorized") {
		t.Fatalf("init rejection = %v", err)
	}
	if !fake2.terminated {
		t.Fatal("init rejection must terminate the connection")
	}

	sub3, fake3 := newTestSubscription(t, SubscriptionConfig{})
	fake3.sn = Snapshot{CloseCode: 4403, CloseReason: "Forbidden"}
	fake3.CloseWith(0, "")
	done := make(chan struct{})
	close(done)
	sub3.done = done
	if err := sub3.waitAck(context.Background()); err == nil || !strings.Contains(err.Error(), "4403") {
		t.Fatalf("closed before ack = %v", err)
	}
}

func TestSubscriptionRequiresAckBeforeSubscribe(t *testing.T) {
	sub, _ := newTestSubscription(t, SubscriptionConfig{})
	if _, err := sub.Subscribe(context.Background(), "subscription A { a }", "", nil, false); !errors.Is(err, ErrSubscriptionNotAcknowledged) {
		t.Fatalf("subscribe before ack = %v", err)
	}
	if _, err := sub.Subscribe(context.Background(), "  ", "", nil, false); err == nil {
		t.Fatal("empty document must be rejected")
	}
	if _, err := sub.Subscribe(context.Background(), "subscription A { a }", "", []byte("not json"), false); err == nil {
		t.Fatal("invalid variables must be rejected")
	}
}

func TestSubscriptionSensitiveInitIsRedactedAndRegistered(t *testing.T) {
	sub, fake := newTestSubscription(t, SubscriptionConfig{
		InitPayload:          map[string]any{"token": "tok-init-123"},
		InitPayloadSensitive: true,
	})
	mustAcked(t, sub)
	if err := sub.sendInit(context.Background()); err != nil {
		t.Fatal(err)
	}
	fake.mu.Lock()
	if len(fake.sent) != 1 || !fake.sent[0].sensitive {
		fake.mu.Unlock()
		t.Fatalf("init frame must be marked sensitive: %+v", fake.sent)
	}
	fake.mu.Unlock()
	if got := sub.redact("echo tok-init-123"); !strings.Contains(got, "***") || strings.Contains(got, "tok-init-123") {
		t.Fatalf("init secret was not registered with the redactor: %q", got)
	}
}

// --- live loopback graphql-transport-ws server ---

func startGraphQLWSServer(t *testing.T, handle func(c *gorilla.Conn, msg map[string]any) bool) string {
	t.Helper()
	upgrader := gorilla.Upgrader{Subprotocols: []string{GraphQLTransportWSSubprotocol}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			_, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			var msg map[string]any
			if err := json.Unmarshal(data, &msg); err != nil {
				return
			}
			if !handle(c, msg) {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestGraphQLTransportWSLive(t *testing.T) {
	pongSeen := make(chan struct{}, 1)
	url := startGraphQLWSServer(t, func(c *gorilla.Conn, msg map[string]any) bool {
		switch msg["type"] {
		case "connection_init":
			p, _ := msg["payload"].(map[string]any)
			if p != nil && p["token"] == "bad" {
				_ = c.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(4403, "Forbidden"), time.Now().Add(time.Second))
				return false
			}
			_ = c.WriteJSON(map[string]any{"type": "connection_ack"})
		case "pong":
			select {
			case pongSeen <- struct{}{}:
			default:
			}
		case "subscribe":
			id, _ := msg["id"].(string)
			payload, _ := msg["payload"].(map[string]any)
			query, _ := payload["query"].(string)
			switch {
			case strings.Contains(query, "Two"):
				_ = c.WriteJSON(map[string]any{"id": id, "type": "next", "payload": map[string]any{"data": map[string]any{"two": 2}}})
				_ = c.WriteJSON(map[string]any{"id": id, "type": "complete"})
			default:
				_ = c.WriteJSON(map[string]any{"type": "ping", "payload": map[string]any{"n": 1}})
				_ = c.WriteJSON(map[string]any{"id": id, "type": "next", "payload": map[string]any{"data": map[string]any{"one": 1}}})
				_ = c.WriteJSON(map[string]any{"id": id, "type": "next", "payload": map[string]any{"data": map[string]any{"one": 11}}})
			}
		case "complete":
			// client cancel: nothing else to do
		}
		return true
	})

	opts := testOptions(url)
	sub, err := ConnectSubscription(context.Background(), opts, SubscriptionConfig{
		InitPayload:          map[string]any{"token": "tok-live-456"},
		InitPayloadSensitive: true,
		AckTimeout:           2 * time.Second,
	})
	if err != nil {
		t.Fatalf("connect subscription: %v", err)
	}
	defer sub.Terminate()
	if sub.Subprotocol() != GraphQLTransportWSSubprotocol {
		t.Fatalf("subprotocol = %q", sub.Subprotocol())
	}
	ctx := context.Background()
	one, err := sub.Subscribe(ctx, "subscription One { one }", "", []byte(`{"id":"1"}`), true)
	if err != nil {
		t.Fatal(err)
	}
	two, err := sub.Subscribe(ctx, "subscription Two { two }", "", nil, false)
	if err != nil {
		t.Fatal(err)
	}
	// The server pings before sending next; the client must answer and still
	// deliver the data.
	m1, err := one.Next(ctx)
	if err != nil || !strings.Contains(string(m1.Payload), `"one":1`) {
		t.Fatalf("op1 first: %+v err=%v", m1, err)
	}
	m1b, err := one.Next(ctx)
	if err != nil || !strings.Contains(string(m1b.Payload), `"one":11`) {
		t.Fatalf("op1 second: %+v err=%v", m1b, err)
	}
	m2, err := two.Next(ctx)
	if err != nil || !strings.Contains(string(m2.Payload), `"two":2`) {
		t.Fatalf("op2: %+v err=%v", m2, err)
	}
	if _, err := two.Next(ctx); !errors.Is(err, ErrSubscriptionComplete) {
		t.Fatalf("op2 complete = %v", err)
	}
	select {
	case <-pongSeen:
	case <-time.After(2 * time.Second):
		t.Fatal("client did not answer the server ping")
	}
	if err := one.Cancel(); err != nil {
		t.Fatal(err)
	}
	// The init payload must never appear un-redacted in retained events.
	var sawInit, leaked bool
	for _, ev := range sub.log.Snapshot() {
		if ev.Kind == stream.KindSend && strings.Contains(string(ev.Payload), "connection_init") {
			sawInit = true
			if strings.Contains(string(ev.Payload), "tok-live-456") {
				leaked = true
			}
		}
	}
	if !sawInit || leaked {
		t.Fatalf("init frame retention saw=%v leaked=%v", sawInit, leaked)
	}
	if err := sub.Close(1000, "done"); err != nil {
		t.Fatalf("close: %v", err)
	}
	if sn := sub.Snapshot(); sn.Transport.State != StateClosed {
		t.Fatalf("state after close = %q", sn.Transport.State)
	}
}

func TestGraphQLTransportWSInitRejectedLive(t *testing.T) {
	url := startGraphQLWSServer(t, func(c *gorilla.Conn, msg map[string]any) bool {
		if msg["type"] == "connection_init" {
			_ = c.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(4403, "Forbidden"), time.Now().Add(time.Second))
			return false
		}
		return true
	})
	sub, err := ConnectSubscription(context.Background(), testOptions(url), SubscriptionConfig{AckTimeout: 2 * time.Second})
	if err == nil {
		t.Fatal("421/4403 rejection must fail the connection")
	}
	if !strings.Contains(err.Error(), "4403") && !strings.Contains(err.Error(), "closed") {
		t.Fatalf("unexpected rejection error: %v", err)
	}
	sub.Terminate()
}

func TestGraphQLTransportWSAckTimeoutLive(t *testing.T) {
	url := startGraphQLWSServer(t, func(c *gorilla.Conn, msg map[string]any) bool {
		return true // read but never acknowledge
	})
	sub, err := ConnectSubscription(context.Background(), testOptions(url), SubscriptionConfig{AckTimeout: 50 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "not acknowledged") {
		t.Fatalf("ack timeout = %v", err)
	}
	sub.Terminate()
}

func TestGraphQLTransportWSRejectsWrongSubprotocol(t *testing.T) {
	opts := testOptions("ws://example.invalid")
	opts.Subprotocols = []string{"graphql-ws"}
	if _, err := ConnectSubscription(context.Background(), opts, SubscriptionConfig{}); err == nil || !core.IsConfig(err) {
		t.Fatalf("legacy subprotocol must be refused before network: %v", err)
	}
}
