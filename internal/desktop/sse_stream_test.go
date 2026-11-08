package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// TestSSEMessagesCoverBothLanguages keeps the independent SSE catalog complete
// in both languages and checks that every count-bearing string has its verb.
func TestSSEMessagesCoverBothLanguages(t *testing.T) {
	for _, tc := range []struct {
		name string
		sm   *sseMessages
	}{{"zh", &sseMessagesZh}, {"en", &sseMessagesEn}} {
		fields := []struct {
			name string
			val  string
			fmt  bool
		}{
			{"StreamingFmt", tc.sm.StreamingFmt, true},
			{"CompletedFmt", tc.sm.CompletedFmt, true},
			{"CanceledFmt", tc.sm.CanceledFmt, true},
			{"TimeoutFmt", tc.sm.TimeoutFmt, true},
			{"FailedFmt", tc.sm.FailedFmt, true},
			{"Truncated", tc.sm.Truncated, false},
			{"Waiting", tc.sm.Waiting, false},
		}
		for _, f := range fields {
			if strings.TrimSpace(f.val) == "" {
				t.Errorf("sseMessages%s.%s is empty", tc.name, f.name)
			}
			if f.fmt && !strings.Contains(f.val, "%d") {
				t.Errorf("sseMessages%s.%s lacks its %%d verb: %q", tc.name, f.name, f.val)
			}
		}
	}
}

func TestFormatSSEEventRoundTrip(t *testing.T) {
	ev := core.SSEEvent{
		ID:       "1",
		Type:     "message",
		Data:     "a\nb",
		HasData:  true,
		Retry:    2 * time.Second,
		HasRetry: true,
		Comments: []string{"c"},
	}
	want := ": c\nevent: message\nid: 1\nretry: 2000\ndata: a\ndata: b\n\n"
	if got := formatSSEEvent(ev); got != want {
		t.Fatalf("format = %q, want %q", got, want)
	}
	if got := formatSSEEvent(core.SSEEvent{HasData: true}); got != "data: \n\n" {
		t.Fatalf("empty data format = %q", got)
	}
}

// sseTestWindow is a headless window whose Update runs synchronously under a
// mutex and then signals a channel. The mutex mirrors the real UI thread so a
// tester frame and a stream callback never touch tab state concurrently; tests
// wait on the channel instead of sleeping.
type sseTestWindow struct {
	*fakeWindow
	mu      sync.Mutex
	changed chan struct{}
}

func (w *sseTestWindow) Update(fn func()) {
	w.mu.Lock()
	fn()
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (w *sseTestWindow) view(a *App) func(*ui.Context) {
	return func(c *ui.Context) {
		w.mu.Lock()
		defer w.mu.Unlock()
		a.View(c)
	}
}

type sseTestFactory struct {
	last *sseTestWindow
}

func (f *sseTestFactory) new(s windowSpec) window {
	w := &sseTestWindow{fakeWindow: &fakeWindow{title: s.Title}, changed: make(chan struct{}, 1024)}
	f.last = w
	return w
}

func openSSEProject(t *testing.T, name string) (*App, *projectState, *sseTestWindow) {
	t.Helper()
	m, _ := newTestManager(t)
	f := &sseTestFactory{}
	m.newWindow = f.new
	if err := m.openProject(writeProject(t, name)); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	a := m.workspaces[0]
	w, ok := a.win.(*sseTestWindow)
	if !ok {
		t.Fatalf("unexpected window type %T", a.win)
	}
	return a, a.ps, w
}

// waitSSE waits for an Update signal and then checks cond under the window
// mutex. The timer is only a failure bound, never a success condition.
func waitSSE(t *testing.T, w *sseTestWindow, what string, cond func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		w.mu.Lock()
		ok := cond()
		w.mu.Unlock()
		if ok {
			return
		}
		select {
		case <-w.changed:
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func sseLocked(w *sseTestWindow, fn func()) {
	w.mu.Lock()
	defer w.mu.Unlock()
	fn()
}

// TestSSELiveFirstEventBeforeServerEOF proves the response area shows the first
// event while the server is still holding the connection open.
func TestSSELiveFirstEventBeforeServerEOF(t *testing.T) {
	release := make(chan struct{})
	firstFlushed := make(chan struct{})
	var secondWritten atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "id: 1\ndata: first-event\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, "id: 2\ndata: second-event\n\n")
		if fl != nil {
			fl.Flush()
		}
		secondWritten.Store(true)
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	a, ps, w := openSSEProject(t, "SSE live")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL

	tt := ui.NewTester(w.view(a), 1200, 820)
	a.trySend(tab)

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed the first event")
	}
	waitSSE(t, w, "first event in the response area", func() bool {
		return tab.Resp != nil && strings.Contains(string(tab.Resp.Body), "first-event")
	})
	if secondWritten.Load() {
		t.Fatal("server already wrote the second event; first-event visibility is not proven")
	}
	select {
	case <-release:
		t.Fatal("release was already closed")
	default:
	}

	tt.Frame()
	if !tt.HasText("SSE streaming") {
		t.Fatalf("live stream status missing; texts=%v", tt.Texts())
	}
	var editorBody string
	sseLocked(w, func() {
		if tab.respEditor != nil {
			editorBody = tab.respEditor.Buffer.String()
		}
	})
	if !strings.Contains(editorBody, "first-event") {
		t.Fatalf("response editor does not show the first event: %q", editorBody)
	}
	var sending bool
	sseLocked(w, func() { sending = tab.Sending })
	if !sending {
		t.Fatal("tab left the sending state before the server ended the stream")
	}

	close(release)
	waitSSE(t, w, "stream completion", func() bool { return !tab.Sending })
	tt.Frame()
	var (
		phase core.StreamPhase
		body  string
		count int
	)
	sseLocked(w, func() {
		if tab.Resp != nil && tab.Resp.Stream != nil {
			phase, body, count = tab.Resp.Stream.Phase, string(tab.Resp.Body), tab.Resp.Stream.EventCount
		}
	})
	if phase != core.StreamPhaseCompleted || count != 2 {
		t.Fatalf("phase=%q count=%d", phase, count)
	}
	if !strings.Contains(body, "first-event") || !strings.Contains(body, "second-event") {
		t.Fatalf("body = %q", body)
	}
	if !tt.HasText("SSE completed") {
		t.Fatalf("completed stream status missing; texts=%v", tt.Texts())
	}
}

// TestSSECancelRetainsEventsAndReleasesConnection cancels a live stream and
// proves both that the server observes the disconnect and that the already
// received event stays on screen.
func TestSSECancelRetainsEventsAndReleasesConnection(t *testing.T) {
	ctxDone := make(chan struct{})
	firstFlushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: kept-after-cancel\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		<-r.Context().Done()
		close(ctxDone)
	}))
	defer srv.Close()

	a, ps, w := openSSEProject(t, "SSE cancel")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL
	a.trySend(tab)

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	waitSSE(t, w, "first event", func() bool {
		return tab.Resp != nil && strings.Contains(string(tab.Resp.Body), "kept-after-cancel")
	})
	if tab.cancel == nil {
		t.Fatal("send did not install a cancel function")
	}
	tab.cancel()

	select {
	case <-ctxDone:
	case <-time.After(3 * time.Second):
		t.Fatal("server never observed the canceled connection")
	}
	waitSSE(t, w, "canceled stream completion", func() bool { return !tab.Sending })
	var (
		phase   core.StreamPhase
		body    string
		respErr string
	)
	sseLocked(w, func() {
		respErr = tab.RespErr
		if tab.Resp != nil && tab.Resp.Stream != nil {
			phase, body = tab.Resp.Stream.Phase, string(tab.Resp.Body)
		}
	})
	if phase != core.StreamPhaseCanceled {
		t.Fatalf("phase = %q, want canceled", phase)
	}
	if !strings.Contains(body, "kept-after-cancel") {
		t.Fatalf("received content was lost: %q", body)
	}
	if respErr != "" {
		t.Fatalf("partial stream should not use the error view: %q", respErr)
	}
}

// TestSSETimeoutRetainsEvents covers an explicit request timeout: the stream is
// cut, the partial event remains, and the phase says timeout.
func TestSSETimeoutRetainsEvents(t *testing.T) {
	firstFlushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: kept-after-timeout\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		<-r.Context().Done()
	}))
	defer srv.Close()

	a, ps, w := openSSEProject(t, "SSE timeout")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL
	tab.Timeout = "80ms"
	a.trySend(tab)

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	waitSSE(t, w, "timed-out stream completion", func() bool { return !tab.Sending })
	var (
		phase  core.StreamPhase
		body   string
		status string
	)
	sseLocked(w, func() {
		status = a.status
		if tab.Resp != nil && tab.Resp.Stream != nil {
			phase, body = tab.Resp.Stream.Phase, string(tab.Resp.Body)
		}
	})
	if phase != core.StreamPhaseTimeout {
		t.Fatalf("phase = %q, want timeout", phase)
	}
	if !strings.Contains(body, "kept-after-timeout") {
		t.Fatalf("received content was lost: %q", body)
	}
	if !strings.Contains(status, "timed out") {
		t.Fatalf("status does not report the timeout: %q", status)
	}
}

// TestSSETabsAreIsolated runs two streams in one window and checks that each
// tab keeps only its own events.
func TestSSETabsAreIsolated(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		switch r.URL.Path {
		case "/a":
			_, _ = io.WriteString(w, "data: event-a\n\n")
		case "/b":
			_, _ = io.WriteString(w, "data: event-b\n\n")
		}
		if fl != nil {
			fl.Flush()
		}
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()

	a, ps, w := openSSEProject(t, "SSE tabs")
	tabs := openRequestTabs(t, ps, "A", "B")
	tabs[0].URL = srv.URL + "/a"
	tabs[1].URL = srv.URL + "/b"

	a.trySend(tabs[0])
	waitSSE(t, w, "tab A first event", func() bool {
		return tabs[0].Resp != nil && strings.Contains(string(tabs[0].Resp.Body), "event-a")
	})
	a.trySend(tabs[1])
	waitSSE(t, w, "tab B first event", func() bool {
		return tabs[1].Resp != nil && strings.Contains(string(tabs[1].Resp.Body), "event-b")
	})

	var bodyA, bodyB string
	sseLocked(w, func() {
		bodyA, bodyB = string(tabs[0].Resp.Body), string(tabs[1].Resp.Body)
	})
	if strings.Contains(bodyA, "event-b") || strings.Contains(bodyB, "event-a") {
		t.Fatalf("streams crossed tabs: A=%q B=%q", bodyA, bodyB)
	}
	close(release)
	waitSSE(t, w, "both streams completed", func() bool {
		return !tabs[0].Sending && !tabs[1].Sending
	})
}

// TestSSETabRemovalCancelsStream checks that closing a tab cancels its request
// and detaches the stream while the received content stays on the tab.
func TestSSETabRemovalCancelsStream(t *testing.T) {
	ctxDone := make(chan struct{})
	firstFlushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: kept-after-close\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		<-r.Context().Done()
		close(ctxDone)
	}))
	defer srv.Close()

	a, ps, w := openSSEProject(t, "SSE close")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL
	a.trySend(tab)

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	waitSSE(t, w, "first event", func() bool {
		return tab.Resp != nil && strings.Contains(string(tab.Resp.Body), "kept-after-close")
	})
	w.mu.Lock()
	ps.removeTab(tab)
	w.mu.Unlock()

	select {
	case <-ctxDone:
	case <-time.After(3 * time.Second):
		t.Fatal("closing the tab did not cancel the request")
	}
	var stillAttached bool
	sseLocked(w, func() { stillAttached = tab.sse != nil })
	if stillAttached {
		t.Fatal("closed tab still holds a stream")
	}
	var kept string
	sseLocked(w, func() { kept = string(tab.Resp.Body) })
	if !strings.Contains(kept, "kept-after-close") {
		t.Fatalf("content lost on tab close: %q", kept)
	}
}

// TestSSERedactsSecretBeforeUI registers a secret used in a request header and
// proves the event delivered to the tab and the final body are masked.
func TestSSERedactsSecretBeforeUI(t *testing.T) {
	const secret = "sse-secret-token-value"
	firstFlushed := make(chan struct{})
	gotHeader := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeader <- r.Header.Get("X-Token")
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: token="+secret+"\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
	}))
	defer srv.Close()

	a, ps, w := openSSEProject(t, "SSE redact")
	a.mgr.secrets = core.MapSecrets{"token": secret}
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL
	tab.HeaderRows = []*KVRow{{ID: nextRowID(), Enabled: true, Name: "X-Token", Value: "{{secret.token}}"}}
	a.trySend(tab)

	select {
	case h := <-gotHeader:
		if h != secret {
			t.Fatalf("request header = %q, want the resolved secret", h)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server never saw the request")
	}
	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	waitSSE(t, w, "redacted event", func() bool {
		return tab.Resp != nil && strings.Contains(string(tab.Resp.Body), "***")
	})
	var body string
	sseLocked(w, func() { body = string(tab.Resp.Body) })
	if strings.Contains(body, secret) {
		t.Fatalf("raw secret reached the UI: %q", body)
	}
	if !strings.Contains(body, "token=***") {
		t.Fatalf("redacted event not shown: %q", body)
	}
}

// TestSSENonConformingBodyFallsBackToRawText keeps the body visible when a
// server labels a non-SSE payload as text/event-stream (for example a JSON
// error page): the parser yields no events, so the redacted raw body is shown.
func TestSSENonConformingBodyFallsBackToRawText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, `{"error":"not an event"}`)
	}))
	defer srv.Close()

	a, ps, w := openSSEProject(t, "SSE fallback")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL
	a.trySend(tab)

	waitSSE(t, w, "fallback completion", func() bool { return !tab.Sending })
	var body string
	var phase core.StreamPhase
	sseLocked(w, func() {
		body = string(tab.Resp.Body)
		if tab.Resp.Stream != nil {
			phase = tab.Resp.Stream.Phase
		}
	})
	if !strings.Contains(body, `"error"`) {
		t.Fatalf("non-conforming body was lost: %q", body)
	}
	if phase != core.StreamPhaseCompleted {
		t.Fatalf("phase = %q, want completed", phase)
	}
}

// TestTabSSEStreamDropsSupersededGeneration is the deterministic guard for
// "an old stream never overwrites a newer request": a stream whose send
// generation is stale closes without touching the tab.
func TestTabSSEStreamDropsSupersededGeneration(t *testing.T) {
	a, ps, w := openSSEProject(t, "SSE generation")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.sendSeq = 2
	stale := newTabSSEStream(a, tab, w, 1)
	stale.mu.Lock()
	stale.pending = []core.SSEEvent{{Data: "stale-event", HasData: true}}
	stale.mu.Unlock()

	stale.flush()

	if tab.Resp != nil {
		t.Fatalf("superseded stream wrote a response: %+v", tab.Resp)
	}
	if !stale.closed {
		t.Fatal("superseded stream was not closed")
	}
}

// TestSSEWindowCloseCancelsStream checks that destroying the window cancels a
// live stream and detaches it.
func TestSSEWindowCloseCancelsStream(t *testing.T) {
	ctxDone := make(chan struct{})
	firstFlushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: kept-after-window-close\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		<-r.Context().Done()
		close(ctxDone)
	}))
	defer srv.Close()

	a, ps, w := openSSEProject(t, "SSE window close")
	tab := openRequestTabs(t, ps, "Stream")[0]
	tab.URL = srv.URL
	a.trySend(tab)

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	waitSSE(t, w, "first event", func() bool {
		return tab.Resp != nil && strings.Contains(string(tab.Resp.Body), "kept-after-window-close")
	})

	w.mu.Lock()
	a.win.Close()
	w.mu.Unlock()

	select {
	case <-ctxDone:
	case <-time.After(3 * time.Second):
		t.Fatal("closing the window did not cancel the stream")
	}
	var attached, sending bool
	sseLocked(w, func() {
		attached, sending = tab.sse != nil, tab.Sending
	})
	if attached || sending {
		t.Fatalf("window close left stream=%v sending=%v", attached, sending)
	}
}

// sseQueueWindow queues Update closures instead of running them, so a test can
// count how many frames a burst of events requested.
type sseQueueWindow struct {
	*fakeWindow
	mu      sync.Mutex
	queue   []func()
	changed chan struct{}
}

func (w *sseQueueWindow) Update(fn func()) {
	w.mu.Lock()
	w.queue = append(w.queue, fn)
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

func (w *sseQueueWindow) pop() (func(), bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if len(w.queue) == 0 {
		return nil, false
	}
	fn := w.queue[0]
	w.queue = w.queue[1:]
	return fn, true
}

// TestSSECoalescesFramesForEventBurst sends many events in one chunk and checks
// that the tab asked for far fewer UI updates than events, rather than one
// frame per event.
func TestSSECoalescesFramesForEventBurst(t *testing.T) {
	const events = 60
	var payload strings.Builder
	for i := 0; i < events; i++ {
		payload.WriteString("data: event-")
		payload.WriteString(strings.Repeat("x", 4))
		payload.WriteString("\n\n")
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, payload.String())
	}))
	defer srv.Close()

	m, _ := newTestManager(t)
	qw := &sseQueueWindow{fakeWindow: &fakeWindow{}, changed: make(chan struct{}, 1024)}
	m.newWindow = func(s windowSpec) window { return qw }
	if err := m.openProject(writeProject(t, "SSE coalesce")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	tab := openRequestTabs(t, a.ps, "Stream")[0]
	tab.URL = srv.URL

	a.trySend(tab)

	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	frames := 0
	for {
		fn, ok := qw.pop()
		if ok {
			frames++
			fn()
			if !tab.Sending {
				break
			}
			continue
		}
		select {
		case <-qw.changed:
		case <-deadline.C:
			t.Fatalf("timed out draining updates (frames=%d Sending=%v)", frames, tab.Sending)
		}
	}
	if frames >= events {
		t.Fatalf("frames=%d for %d events; updates were not coalesced", frames, events)
	}
	if tab.Resp == nil || tab.Resp.Stream == nil || tab.Resp.Stream.EventCount != events {
		t.Fatalf("stream result = %+v", tab.Resp)
	}
}
