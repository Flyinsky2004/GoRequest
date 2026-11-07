package websocket

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	"gorequest/internal/core"
	"gorequest/internal/stream"
)

// These tests are written but NOT run in the P01 phase-1 delivery (the task
// explicitly defers go test/go build). They use only loopback httptest servers
// and are the first execution step for the integrator.

func testOptions(url string) Options {
	return Options{
		URL:         url,
		Proxy:       core.ProxyConfig{Mode: core.ProxyNone},
		Interactive: true,
		Deadlines:   stream.DeadlinePolicy{Total: 5 * time.Second},
	}
}

func waitFor(t *testing.T, d time.Duration, cond func() bool) bool {
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func TestConnectSendReceivePingClose(t *testing.T) {
	upgrader := gorilla.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, data); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	sess, err := Connect(context.Background(), testOptions(wsURL))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Terminate()
	if sess.State() != StateOpen {
		t.Fatalf("state = %q", sess.State())
	}
	if err := sess.SendText(context.Background(), "hello", false); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !waitFor(t, 2*time.Second, func() bool {
		for _, ev := range sess.Events().Snapshot() {
			if ev.Kind == stream.KindReceive && ev.IsText && string(ev.Payload) == "hello" {
				return true
			}
		}
		return false
	}) {
		t.Fatal("echoed message was not received")
	}
	if _, err := sess.Ping(context.Background()); err != nil {
		t.Fatalf("ping: %v", err)
	}
	if err := sess.CloseWith(1000, "done"); err != nil {
		t.Fatalf("close: %v", err)
	}
	sn := sess.Snapshot()
	if sn.State != StateClosed {
		t.Fatalf("state after close = %q", sn.State)
	}
	if sn.CloseCode != 1000 {
		t.Fatalf("close code = %d", sn.CloseCode)
	}
	if sn.ReceiveCount == 0 || sn.SentCount == 0 {
		t.Fatalf("counters: sent=%d recv=%d", sn.SentCount, sn.ReceiveCount)
	}
}

func TestBadHandshakeKeepsFacts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	sess, err := Connect(context.Background(), testOptions(wsURL))
	if sess == nil {
		t.Fatal("session must be returned with handshake facts")
	}
	defer sess.Terminate()
	var hs *HandshakeError
	if !errors.As(err, &hs) {
		t.Fatalf("error = %v, want *HandshakeError", err)
	}
	if hs.Status != http.StatusForbidden {
		t.Fatalf("status = %d", hs.Status)
	}
	sn := sess.Snapshot()
	if sn.HTTPStatus != http.StatusForbidden || sn.State != StateClosed {
		t.Fatalf("snapshot = %+v", sn)
	}
	if sn.End != EndHandshakeFailed {
		t.Fatalf("end = %q", sn.End)
	}
}

func TestPreNetworkRejections(t *testing.T) {
	if _, err := Connect(context.Background(), testOptions("http://example.com/ws")); !core.IsConfig(err) {
		t.Fatalf("http url error = %v, want config error", err)
	}

	opts := testOptions("wss://example.com/ws")
	opts.TLS = core.TLSConfig{InsecureSkipVerify: true}
	opts.HasTargetPolicy = true
	opts.TargetPolicy = core.MergeTargetPolicy(nil, nil) // EnforceTLSVerify = true
	if _, err := Connect(context.Background(), opts); !core.IsConfig(err) {
		t.Fatalf("insecure TLS error = %v, want config error", err)
	}
}

func TestSendAfterCloseNeverFakesSuccess(t *testing.T) {
	upgrader := gorilla.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer srv.Close()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	sess, err := Connect(context.Background(), testOptions(wsURL))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	sess.Terminate()
	if err := sess.SendText(context.Background(), "late", false); !errors.Is(err, ErrSessionClosed) {
		t.Fatalf("send after close = %v, want ErrSessionClosed", err)
	}
}
