package core

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func collectSSE(t *testing.T, chunks ...string) []SSEEvent {
	t.Helper()
	p := newSSEParser()
	var got []SSEEvent
	emit := func(ev SSEEvent) { got = append(got, ev) }
	for _, c := range chunks {
		p.Feed([]byte(c), emit)
	}
	p.Flush(emit)
	return got
}

func TestSSEParserFieldsMultilineDataAndComments(t *testing.T) {
	got := collectSSE(t, "id: 7\nevent: update\nretry: 1500\ndata: one\ndata: two\n: note\n\n")
	if len(got) != 1 {
		t.Fatalf("events = %d, want 1: %+v", len(got), got)
	}
	ev := got[0]
	if ev.ID != "7" || ev.Type != "update" || ev.Data != "one\ntwo" || !ev.HasData {
		t.Fatalf("event = %+v", ev)
	}
	if !ev.HasRetry || ev.Retry != 1500*time.Millisecond {
		t.Fatalf("retry = %v has=%v", ev.Retry, ev.HasRetry)
	}
	if len(ev.Comments) != 1 || ev.Comments[0] != "note" {
		t.Fatalf("comments = %v", ev.Comments)
	}
}

func TestSSEParserLastEventIDPersistsAcrossEvents(t *testing.T) {
	got := collectSSE(t, "id: 9\ndata: a\n\ndata: b\n\n")
	if len(got) != 2 {
		t.Fatalf("events = %d", len(got))
	}
	if got[0].ID != "9" || got[1].ID != "9" {
		t.Fatalf("ids = %q, %q; want the last event ID to persist", got[0].ID, got[1].ID)
	}
}

func TestSSEParserCRLFSplitAcrossChunks(t *testing.T) {
	got := collectSSE(t, "data: a\r", "\n\r\n")
	if len(got) != 1 || got[0].Data != "a" {
		t.Fatalf("events = %+v", got)
	}
}

func TestSSEParserCRLFFullChunk(t *testing.T) {
	got := collectSSE(t, "data: a\r\n\r\n")
	if len(got) != 1 || got[0].Data != "a" {
		t.Fatalf("events = %+v", got)
	}
}

func TestSSEParserUTF8SplitAcrossChunks(t *testing.T) {
	full := []byte("data: 你好\n\n")
	// Split inside the three-byte sequence of 你.
	cut := 6 + 1
	got := collectSSE(t, string(full[:cut]), string(full[cut:]))
	if len(got) != 1 || got[0].Data != "你好" {
		t.Fatalf("events = %+v", got)
	}
}

func TestSSEParserCommentOnlyIsNotData(t *testing.T) {
	got := collectSSE(t, ": ping\n\n")
	if len(got) != 1 {
		t.Fatalf("events = %d", len(got))
	}
	if got[0].Data != "" || got[0].HasData || len(got[0].Comments) != 1 || got[0].Comments[0] != "ping" {
		t.Fatalf("comment event = %+v", got[0])
	}
}

func TestSSEParserEmptyDataFieldDispatches(t *testing.T) {
	got := collectSSE(t, "data:\n\n")
	if len(got) != 1 || !got[0].HasData || got[0].Data != "" {
		t.Fatalf("events = %+v", got)
	}
}

func TestSSEParserDispatchesPendingEventAtEOF(t *testing.T) {
	got := collectSSE(t, "data: last")
	if len(got) != 1 || got[0].Data != "last" {
		t.Fatalf("events = %+v", got)
	}
}

func TestSSEParserBoundsEventData(t *testing.T) {
	line := strings.Repeat("x", 60<<10)
	var b strings.Builder
	for i := 0; i < 5; i++ {
		b.WriteString("data: ")
		b.WriteString(line)
		b.WriteString("\n")
	}
	b.WriteString("\n")
	got := collectSSE(t, b.String())
	if len(got) != 1 {
		t.Fatalf("events = %d", len(got))
	}
	if !got[0].Truncated || len(got[0].Data) > MaxSSEEventBytes {
		t.Fatalf("truncated=%v len=%d", got[0].Truncated, len(got[0].Data))
	}
}

func TestSSEParserDiscardsOverlongLine(t *testing.T) {
	line := strings.Repeat("x", MaxSSELineBytes+10)
	got := collectSSE(t, "data: "+line+"\ndata: ok\n\n")
	if len(got) != 1 || got[0].Data != "ok" {
		t.Fatalf("events = %+v", got)
	}
}

func TestIsEventStreamContentType(t *testing.T) {
	cases := map[string]bool{
		"text/event-stream":                  true,
		"text/event-stream; charset=utf-8":   true,
		" TEXT/EVENT-STREAM ;charset=UTF-8 ": true,
		"text/plain":                         false,
		"application/json":                   false,
		"":                                   false,
	}
	for ct, want := range cases {
		if got := IsEventStreamContentType(ct); got != want {
			t.Errorf("IsEventStreamContentType(%q) = %v, want %v", ct, got, want)
		}
	}
}

// sseTestServer streams one event, flushes it, then waits for release before
// writing the rest, so a test can prove the first event was delivered before
// the server ended the response.
func sseTestServer(t *testing.T, first, rest string) (*httptest.Server, <-chan struct{}, chan struct{}, *atomic.Bool) {
	t.Helper()
	firstFlushed := make(chan struct{})
	release := make(chan struct{})
	var ended atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, first)
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, rest)
		if fl != nil {
			fl.Flush()
		}
		ended.Store(true)
	}))
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
		srv.Close()
	})
	return srv, firstFlushed, release, &ended
}

func TestExecuteSSEDeliversFirstEventBeforeEOF(t *testing.T) {
	srv, firstFlushed, release, ended := sseTestServer(t, "id: 1\ndata: first\n\n", "data: second\n\n")
	events := make(chan SSEEvent, 8)
	endedCh := make(chan StreamEnd, 1)
	opened := make(chan StreamOpen, 1)
	plan := &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: DefaultTimeout}

	type outcome struct {
		res *ExecResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
			Stream: &StreamHandler{
				OnOpen:  func(o StreamOpen) { opened <- o },
				OnEvent: func(ev SSEEvent) { events <- ev },
				OnEnd:   func(e StreamEnd) { endedCh <- e },
			},
		})
		done <- outcome{res, err}
	}()

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed the first event")
	}
	select {
	case o := <-opened:
		if o.Status != http.StatusOK || !IsEventStreamContentType(o.ContentType) {
			t.Fatalf("open = %+v", o)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream never opened")
	}
	select {
	case ev := <-events:
		if ev.ID != "1" || ev.Data != "first" {
			t.Fatalf("first event = %+v", ev)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first event was not delivered before EOF")
	}
	// The server is still blocked on release, so no end may have arrived.
	select {
	case e := <-endedCh:
		t.Fatalf("stream ended before EOF: %+v", e)
	default:
	}

	close(release)
	select {
	case e := <-endedCh:
		if e.Phase != StreamPhaseCompleted || e.EventCount != 2 {
			t.Fatalf("end = %+v", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("stream never ended")
	}
	select {
	case out := <-done:
		if out.err != nil {
			t.Fatalf("execute error: %v", out.err)
		}
		if out.res.Stream == nil || out.res.Stream.Phase != StreamPhaseCompleted || out.res.Stream.EventCount != 2 {
			t.Fatalf("stream info = %+v", out.res.Stream)
		}
		if !strings.Contains(string(out.res.Body), "first") || !strings.Contains(string(out.res.Body), "second") {
			t.Fatalf("body = %q", out.res.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execute did not return")
	}
	if !ended.Load() {
		t.Fatal("server did not finish")
	}
}

func TestExecuteSSECancelRetainsPartialContent(t *testing.T) {
	ctxDone := make(chan struct{})
	firstFlushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: kept-before-cancel\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		<-r.Context().Done()
		close(ctxDone)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	events := make(chan SSEEvent, 8)
	plan := &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: 10 * time.Second, TimeoutExplicit: true}
	type outcome struct {
		res *ExecResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := ExecuteWithOptions(ctx, plan, ExecuteOptions{
			Stream: &StreamHandler{OnEvent: func(ev SSEEvent) { events <- ev }},
		})
		done <- outcome{res, err}
	}()

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	select {
	case <-events:
	case <-time.After(3 * time.Second):
		t.Fatal("first event never arrived")
	}
	cancel()
	select {
	case <-ctxDone:
	case <-time.After(3 * time.Second):
		t.Fatal("server never observed cancellation")
	}
	select {
	case out := <-done:
		if out.err == nil || !IsRun(out.err) || !strings.Contains(out.err.Error(), "cancel") {
			t.Fatalf("err = %v", out.err)
		}
		if out.res == nil || out.res.Stream == nil {
			t.Fatal("partial result missing")
		}
		if out.res.Stream.Phase != StreamPhaseCanceled {
			t.Fatalf("phase = %q", out.res.Stream.Phase)
		}
		if !strings.Contains(string(out.res.Body), "kept-before-cancel") {
			t.Fatalf("partial body = %q", out.res.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execute did not return after cancel")
	}
}

func TestExecuteSSETimeoutRetainsPartialContent(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	firstFlushed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		_, _ = io.WriteString(w, "data: kept-before-timeout\n\n")
		if fl != nil {
			fl.Flush()
		}
		close(firstFlushed)
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	plan := &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: 80 * time.Millisecond, TimeoutExplicit: true}
	type outcome struct {
		res *ExecResult
		err error
	}
	done := make(chan outcome, 1)
	go func() {
		res, err := ExecuteWithOptions(context.Background(), plan, ExecuteOptions{
			Stream: &StreamHandler{OnEvent: func(SSEEvent) {}},
		})
		done <- outcome{res, err}
	}()

	select {
	case <-firstFlushed:
	case <-time.After(3 * time.Second):
		t.Fatal("server never flushed")
	}
	select {
	case out := <-done:
		if out.err == nil || !strings.Contains(out.err.Error(), "timed out after 80ms") {
			t.Fatalf("err = %v", out.err)
		}
		if out.res == nil || out.res.Stream == nil || out.res.Stream.Phase != StreamPhaseTimeout {
			t.Fatalf("stream info = %+v", out.res.Stream)
		}
		if !strings.Contains(string(out.res.Body), "kept-before-timeout") {
			t.Fatalf("partial body = %q", out.res.Body)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("execute did not return after timeout")
	}
}

func TestExecuteSSEDisconnectRetainsPartialContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			t.Error("test server does not support hijacking")
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			t.Errorf("hijack: %v", err)
			return
		}
		// Promise more bytes than are written, then drop the connection, so the
		// client sees an unexpected EOF rather than a clean end.
		_, _ = buf.WriteString("HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Length: 4096\r\n\r\n")
		_, _ = buf.WriteString("data: kept-before-drop\n\n")
		_ = buf.Flush()
		_ = conn.Close()
	}))
	defer srv.Close()

	res, err := ExecuteWithOptions(context.Background(), &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: 10 * time.Second, TimeoutExplicit: true}, ExecuteOptions{
		Stream: &StreamHandler{OnEvent: func(SSEEvent) {}},
	})
	if err == nil || !IsRun(err) {
		t.Fatalf("expected a run error for the dropped connection, got %v", err)
	}
	if res == nil || res.Stream == nil || res.Stream.Phase != StreamPhaseFailed {
		t.Fatalf("stream info = %+v", res)
	}
	if !strings.Contains(string(res.Body), "kept-before-drop") {
		t.Fatalf("partial body = %q", res.Body)
	}
}

func TestExecuteSSERedactsEventsAndBody(t *testing.T) {
	srv, firstFlushed, release, _ := sseTestServer(t, "data: token=hunter2\n\n", "")
	close(release) // the first event is the whole body
	_ = firstFlushed

	red := NewRedactor()
	red.RegisterSecret("hunter2")
	var got SSEEvent
	seen := make(chan struct{}, 1)
	res, err := ExecuteWithOptions(context.Background(), &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: DefaultTimeout}, ExecuteOptions{
		Redactor: red,
		Stream: &StreamHandler{OnEvent: func(ev SSEEvent) {
			got = ev
			seen <- struct{}{}
		}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	select {
	case <-seen:
	default:
		t.Fatal("event callback not called")
	}
	if strings.Contains(got.Data, "hunter2") || !strings.Contains(got.Data, redactedMask) {
		t.Fatalf("callback event leaked a secret: %q", got.Data)
	}
	if strings.Contains(string(res.Body), "hunter2") || !strings.Contains(string(res.Body), redactedMask) {
		t.Fatalf("final body leaked a secret: %q", res.Body)
	}
}

func TestExecuteNonSSEResponseIsNotStreamed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "plain body")
	}))
	defer srv.Close()

	called := false
	res, err := ExecuteWithOptions(context.Background(), &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: DefaultTimeout}, ExecuteOptions{
		Stream: &StreamHandler{OnEvent: func(SSEEvent) { called = true }},
	})
	if err != nil {
		t.Fatal(err)
	}
	if called || res.Stream != nil {
		t.Fatal("a non-SSE response must not use the streaming path")
	}
	if string(res.Body) != "plain body" {
		t.Fatalf("body = %q", res.Body)
	}
}

func TestExecuteSSEWithoutHandlerUsesNormalRead(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: x\n\n")
	}))
	defer srv.Close()

	res, err := Execute(context.Background(), &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: DefaultTimeout}, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	if res.Stream != nil {
		t.Fatal("no Stream handler means no streaming result")
	}
	if string(res.Body) != "data: x\n\n" {
		t.Fatalf("body = %q", res.Body)
	}
}

func TestExecuteSSEMaxBodyTruncates(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fl, _ := w.(http.Flusher)
		for {
			select {
			case <-r.Context().Done():
				return
			case <-release:
				return
			default:
			}
			if _, err := io.WriteString(w, "data: 0123456789abcdef\n\n"); err != nil {
				return
			}
			if fl != nil {
				fl.Flush()
			}
		}
	}))
	defer srv.Close()

	res, err := ExecuteWithOptions(context.Background(), &Plan{Method: http.MethodGet, URL: srv.URL, Timeout: 10 * time.Second, TimeoutExplicit: true}, ExecuteOptions{
		MaxResponseBytes: 64,
		Stream:           &StreamHandler{OnEvent: func(SSEEvent) {}},
	})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res.Stream == nil || !res.Stream.Truncated {
		t.Fatalf("stream info = %+v", res.Stream)
	}
	if len(res.Body) > 64 {
		t.Fatalf("body length = %d, want <= 64", len(res.Body))
	}
}

func TestClassifyStreamErrorParentDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	<-ctx.Done()
	phase, _, err := classifyStreamError(sseReadConfig{parentCtx: ctx, timeoutLabel: "5ms"}, io.ErrUnexpectedEOF)
	if phase != StreamPhaseTimeout || err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("phase=%q err=%v", phase, err)
	}
	if !errors.Is(ctx.Err(), context.DeadlineExceeded) {
		t.Fatal("test setup: expected a deadline")
	}
}
