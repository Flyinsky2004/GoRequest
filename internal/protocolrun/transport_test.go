package protocolrun

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"

	"gorequest/internal/core"
	"gorequest/internal/stream"
	"gorequest/internal/websocket"
)

func liveBaseOptions(t *testing.T) websocket.Options {
	t.Helper()
	return websocket.Options{
		Proxy:       core.ProxyConfig{Mode: core.ProxyNone},
		Interactive: true,
		Deadlines:   stream.DeadlinePolicy{Total: 5 * time.Second},
	}
}

func echoServer(t *testing.T) string {
	t.Helper()
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
	t.Cleanup(srv.Close)
	return "ws" + strings.TrimPrefix(srv.URL, "http")
}

func TestLiveWebSocketPlanTextEcho(t *testing.T) {
	url := echoServer(t)
	tr := NewWebSocketTransport(liveBaseOptions(t), Limits{TotalTimeout: Duration(5 * time.Second)})
	p := &Plan{Version: 1, Limits: Limits{TotalTimeout: Duration(5 * time.Second)}, Steps: []Step{
		{Action: ActionConnect, URL: url},
		{Action: ActionSend, Text: strPtr(`{"type":"ping","n":1}`)},
		{Action: ActionAwait, Match: &MatchSpec{JSONPath: "type", JSONEquals: []byte(`"ping"`)}, Timeout: Duration(2 * time.Second)},
		{Action: ActionClose, Code: 1000, Reason: "done"},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed {
		t.Fatalf("result = %+v steps=%+v", res, res.Steps)
	}
	if res.Steps[1].SentSeq == 0 || res.Steps[2].ReceivedSeq == 0 {
		t.Fatalf("timeline sequences missing: send=%+v await=%+v", res.Steps[1], res.Steps[2])
	}
	if got := string(res.Steps[2].Payload); !strings.Contains(got, `"n":1`) {
		t.Fatalf("payload preview = %q", got)
	}
	if res.CloseCode != 1000 {
		t.Fatalf("result close code = %d", res.CloseCode)
	}
	if code, _ := tr.CloseFacts(); code != 1000 {
		t.Fatalf("transport close code = %d", code)
	}
}

func TestLiveWebSocketPlanBinaryFile(t *testing.T) {
	url := echoServer(t)
	path := filepath.Join(t.TempDir(), "blob.bin")
	if err := os.WriteFile(path, []byte{0xde, 0xad, 0xbe, 0xef}, 0o644); err != nil {
		t.Fatal(err)
	}
	tr := NewWebSocketTransport(liveBaseOptions(t), Limits{TotalTimeout: Duration(5 * time.Second)})
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: url},
		{Action: ActionSend, File: path},
		{Action: ActionAwait, Match: &MatchSpec{BinaryHex: "deadbeef"}, Timeout: Duration(2 * time.Second)},
		{Action: ActionClose, Code: 1000},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed {
		t.Fatalf("binary plan = %+v steps=%+v", res, res.Steps)
	}
	if res.BytesSent != 4 || res.Steps[2].ReceivedBytes != 4 {
		t.Fatalf("binary byte facts: sent=%d recv=%+v", res.BytesSent, res.Steps[2])
	}
	if string(res.Steps[2].Payload) != "***" {
		t.Fatalf("binary preview must be masked: %q", res.Steps[2].Payload)
	}
}

func TestLiveWebSocketServerClosePreservesCode(t *testing.T) {
	upgrader := gorilla.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		_, data, err := c.ReadMessage()
		if err != nil {
			return
		}
		if string(data) == "bye" {
			_ = c.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(4001, "server says bye"), time.Now().Add(time.Second))
		}
		time.Sleep(200 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	tr := NewWebSocketTransport(liveBaseOptions(t), Limits{TotalTimeout: Duration(5 * time.Second)})
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: url},
		{Action: ActionSend, Text: strPtr("bye")},
		{Action: ActionAwait, Match: &MatchSpec{Contains: "never"}, Timeout: Duration(2 * time.Second)},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StateFailed {
		t.Fatalf("plan without a match must fail: %+v", res)
	}
	if res.CloseCode != 4001 || !strings.Contains(res.CloseReason, "server says bye") {
		t.Fatalf("server close facts lost: code=%d reason=%q", res.CloseCode, res.CloseReason)
	}
}

func TestLiveWebSocketFloodHitsRetainBound(t *testing.T) {
	upgrader := gorilla.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for i := 0; i < 100; i++ {
			if err := c.WriteMessage(gorilla.TextMessage, []byte("flood")); err != nil {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	limits := Limits{TotalTimeout: Duration(5 * time.Second), MaxRetainMessages: 5}
	tr := NewWebSocketTransport(liveBaseOptions(t), limits)
	p := &Plan{Version: 1, Limits: limits, Steps: []Step{
		{Action: ActionConnect, URL: url},
		{Action: ActionAwait, Match: &MatchSpec{Contains: "never"}, Timeout: Duration(2 * time.Second)},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StateFailed || !strings.Contains(res.Error, "retained message bound") {
		t.Fatalf("flood result = %+v", res)
	}
}

func TestLiveWebSocketCancelClosesConnection(t *testing.T) {
	closed := make(chan int, 1)
	upgrader := gorilla.Upgrader{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				if ce, ok := err.(*gorilla.CloseError); ok {
					closed <- ce.Code
				}
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(80 * time.Millisecond); cancel() }()
	tr := NewWebSocketTransport(liveBaseOptions(t), Limits{TotalTimeout: Duration(5 * time.Second)})
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: url},
		{Action: ActionAwait, Match: &MatchSpec{Contains: "never"}, Timeout: Duration(4 * time.Second)},
	}}
	res := Run(ctx, compiled(t, p, CompileOptions{}), tr)
	if res.State != StateCanceled {
		t.Fatalf("cancel result = %+v", res)
	}
	select {
	case code := <-closed:
		// Cancellation aborts the parent context, so the server may observe a
		// graceful 1011/1001 close or an abnormal 1006 termination. Both prove
		// the connection is gone; a silent open socket would fail this test.
		switch code {
		case 1000, 1001, 1011, 1006:
		default:
			t.Fatalf("unexpected close code after cancel: %d", code)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not observe the canceled connection")
	}
}

func TestLiveWebSocketSubprotocolNegotiation(t *testing.T) {
	upgrader := gorilla.Upgrader{Subprotocols: []string{"graphql-transport-ws"}}
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
	t.Cleanup(srv.Close)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	tr := NewWebSocketTransport(liveBaseOptions(t), Limits{TotalTimeout: Duration(5 * time.Second)})
	p := &Plan{Version: 1, Steps: []Step{
		{Action: ActionConnect, URL: url, Subprotocols: []string{"graphql-transport-ws"}},
		{Action: ActionClose, Code: 1000},
	}}
	res := Run(context.Background(), compiled(t, p, CompileOptions{}), tr)
	if res.State != StatePassed {
		t.Fatalf("subprotocol plan = %+v", res)
	}
	if tr.Session() == nil || tr.Session().Snapshot().Subprotocol != "graphql-transport-ws" {
		t.Fatalf("subprotocol not negotiated: %+v", tr.Session())
	}
}
