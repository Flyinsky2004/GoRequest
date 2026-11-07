package desktop

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// sendStatusApp opens a project whose window queues Update callbacks, so the
// test drains a send's completion on its own goroutine.
func sendStatusApp(t *testing.T, name string) (*App, *projectState, *queuedWindow) {
	t.Helper()
	m, ff := newTestManager(t)
	var qWin *queuedWindow
	m.newWindow = func(s windowSpec) window {
		fw := ff.new(s).(*fakeWindow)
		qWin = &queuedWindow{fakeWindow: fw, updates: make(chan func(), 16)}
		return qWin
	}
	if err := m.openProject(writeProject(t, name)); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	a := m.workspaces[0]
	return a, a.ps, qWin
}

// drainSend runs queued window updates until the tab leaves its sending state.
func drainSend(t *testing.T, qw *queuedWindow, tab *Tab) {
	t.Helper()
	timer := time.NewTimer(3 * time.Second)
	defer timer.Stop()
	for tab.Sending {
		select {
		case fn := <-qw.updates:
			fn()
		case <-timer.C:
			t.Fatalf("timed out waiting for send completion (Sending=%v RespErr=%q)", tab.Sending, tab.RespErr)
		}
	}
}

// drainPending runs any queued updates that arrive within d; it is used to
// give a stale callback a chance to (wrongly) land.
func drainPending(qw *queuedWindow, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	for {
		select {
		case fn := <-qw.updates:
			fn()
		case <-timer.C:
			return
		}
	}
}

// TestLayoutStatusMessagesCoverBothLanguages keeps the R9 status catalog
// complete in both languages without touching the shared i18n.go.
func TestLayoutStatusMessagesCoverBothLanguages(t *testing.T) {
	for _, m := range []*layoutStatusMessages{&layoutStatusMessagesZh, &layoutStatusMessagesEn} {
		if m.SendDoneFmt == "" || m.SendFailedFmt == "" || m.SendCanceledFmt == "" {
			t.Fatalf("incomplete layout status catalog: %+v", m)
		}
	}
}

// TestSendStatusReachesTerminalState sends the same request five times and
// checks that every completion replaces the "sending…" line with a terminal
// status carrying the status code and duration.
func TestSendStatusReachesTerminalState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	a, ps, qWin := sendStatusApp(t, "Send status")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = srv.URL

	for i := 1; i <= 5; i++ {
		a.trySend(tab)
		waitForSendDone(t, qWin, tab)
		status := a.status
		if strings.Contains(strings.ToLower(status), "sending") {
			t.Fatalf("send %d: status still says sending: %q", i, status)
		}
		if !strings.Contains(status, "200") || !strings.Contains(status, "ms") {
			t.Fatalf("send %d: terminal status lacks status code/duration: %q", i, status)
		}
		if tab.Resp == nil || tab.Resp.Status != http.StatusOK {
			t.Fatalf("send %d: response = %+v", i, tab.Resp)
		}
	}
}

// TestSendStatusFailureIsTerminal checks that a transport failure replaces the
// sending line with an error terminal state.
func TestSendStatusFailureIsTerminal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // no listener: the next send fails to connect

	a, ps, qWin := sendStatusApp(t, "Send failure")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = url

	a.trySend(tab)
	drainSend(t, qWin, tab)
	if tab.RespErr == "" {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(strings.ToLower(a.status), "sending") {
		t.Fatalf("status still says sending: %q", a.status)
	}
	if !strings.Contains(a.status, "request failed") || !strings.Contains(a.status, "ms") {
		t.Fatalf("failure terminal status = %q", a.status)
	}
}

// TestSendStatusCancelIsTerminal checks that canceling an in-flight request
// ends in a canceled terminal state instead of leaving "sending…".
func TestSendStatusCancelIsTerminal(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
	}))
	defer srv.Close()
	defer close(release)

	a, ps, qWin := sendStatusApp(t, "Send cancel")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = srv.URL

	a.trySend(tab)
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("request never reached the server")
	}
	if tab.cancel == nil {
		t.Fatal("send did not install a cancel function")
	}
	tab.cancel()
	drainSend(t, qWin, tab)

	if strings.Contains(strings.ToLower(a.status), "sending") {
		t.Fatalf("status still says sending: %q", a.status)
	}
	if !strings.Contains(strings.ToLower(a.status), "cancel") {
		t.Fatalf("cancel terminal status = %q", a.status)
	}
}

// TestStaleSendCallbackIsDropped forces the Sending flag down, starts a second
// send, then releases the first. The first result must not overwrite the
// second send's response or status.
func TestStaleSendCallbackIsDropped(t *testing.T) {
	arrived := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseNow := func() { releaseOnce.Do(func() { close(release) }) }
	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(arrived)
		<-release
		w.WriteHeader(http.StatusCreated) // 201, distinguishable from the second send
	}))
	defer slow.Close()
	defer releaseNow()
	fast := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK) // 200
	}))
	defer fast.Close()

	a, ps, qWin := sendStatusApp(t, "Send stale")
	tab := openRequestTabs(t, ps, "Alpha")[0]

	tab.URL = slow.URL
	a.trySend(tab)
	select {
	case <-arrived:
	case <-time.After(3 * time.Second):
		t.Fatal("first request never reached the server")
	}

	// Simulate a forced reset of the in-flight flag, then supersede the send.
	tab.Sending = false
	tab.URL = fast.URL
	a.trySend(tab)
	drainSend(t, qWin, tab)
	if tab.Resp == nil || tab.Resp.Status != http.StatusOK {
		t.Fatalf("second send response = %+v", tab.Resp)
	}

	// Let the first request finish and deliver its stale callback.
	releaseNow()
	drainPending(qWin, 300*time.Millisecond)
	if tab.Resp == nil || tab.Resp.Status != http.StatusOK {
		t.Fatalf("stale callback overwrote the response: %+v", tab.Resp)
	}
	if !strings.Contains(a.status, "200") || strings.Contains(a.status, "201") {
		t.Fatalf("stale callback overwrote the status: %q", a.status)
	}
}
