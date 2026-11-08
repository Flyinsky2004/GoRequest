package desktop

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"

	"gorequest/internal/core"
	gows "gorequest/internal/websocket"
)

// TestWSWorkspaceMessagesCoverEveryField keeps the independent WebSocket
// workspace catalog complete in both languages.
func TestWSWorkspaceMessagesCoverEveryField(t *testing.T) {
	typ := reflect.TypeOf(wsWorkspaceMessages{})
	zh := reflect.ValueOf(wsWorkspaceMessagesZh)
	en := reflect.ValueOf(wsWorkspaceMessagesEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		if strings.TrimSpace(zh.Field(i).String()) == "" {
			t.Errorf("wsWorkspaceMessagesZh.%s is empty", f.Name)
		}
		if strings.TrimSpace(en.Field(i).String()) == "" {
			t.Errorf("wsWorkspaceMessagesEn.%s is empty", f.Name)
		}
	}
}

// wsTestWindow is a headless window whose Update executes synchronously and
// signals a channel afterwards. A mutex serializes background callbacks with
// frames, mirroring the real UI thread, so tests can render while a connect or
// echo callback is in flight without a data race. Tests wait on the channel
// instead of sleeping.
type wsTestWindow struct {
	*fakeWindow
	mu      sync.Mutex
	changed chan struct{}
}

func (w *wsTestWindow) Update(fn func()) {
	w.mu.Lock()
	fn()
	w.mu.Unlock()
	select {
	case w.changed <- struct{}{}:
	default:
	}
}

// view wraps App.View with the same mutex so a tester frame and a background
// callback never touch tab state concurrently.
func (w *wsTestWindow) view(a *App) func(*ui.Context) {
	return func(c *ui.Context) {
		w.mu.Lock()
		defer w.mu.Unlock()
		a.View(c)
	}
}

type wsTestFactory struct {
	last *wsTestWindow
}

func (f *wsTestFactory) new(s windowSpec) window {
	w := &wsTestWindow{fakeWindow: &fakeWindow{title: s.Title}, changed: make(chan struct{}, 1024)}
	f.last = w
	return w
}

// openWSProject opens a project whose window signals every Update.
func openWSProject(t *testing.T, name string) (*App, *projectState, *wsTestWindow) {
	t.Helper()
	m, _ := newTestManager(t)
	f := &wsTestFactory{}
	m.newWindow = f.new
	dir := writeProject(t, name)
	if err := m.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	a := m.workspaces[0]
	w, ok := a.win.(*wsTestWindow)
	if !ok {
		t.Fatalf("unexpected window type %T", a.win)
	}
	return a, a.ps, w
}

// waitWS waits for an Update signal and then checks cond. It never sleeps; the
// timer is only a failure bound.
func waitWS(t *testing.T, w *wsTestWindow, what string, cond func() bool) {
	t.Helper()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case <-w.changed:
			if cond() {
				return
			}
		case <-deadline.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// wsEchoServer upgrades WebSocket connections and echoes each message prefixed
// with the request path, so two tabs can be told apart.
type wsEchoServer struct {
	srv *httptest.Server
}

func newWSEchoServer(t *testing.T) *wsEchoServer {
	t.Helper()
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		prefix := "echo" + r.URL.Path + ":"
		for {
			mt, data, err := c.ReadMessage()
			if err != nil {
				return
			}
			if err := c.WriteMessage(mt, append([]byte(prefix), data...)); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)
	return &wsEchoServer{srv: srv}
}

func (s *wsEchoServer) wsURL(path string) string {
	return "ws" + strings.TrimPrefix(s.srv.URL, "http") + path
}

// wsLogContains reports whether a tab's session log retained substr.
func wsLogContains(tab *Tab, substr string) bool {
	if tab == nil || tab.ws == nil || tab.ws.session == nil {
		return false
	}
	for _, ev := range tab.ws.session.Events().Snapshot() {
		if strings.Contains(ev.Summary, substr) {
			return true
		}
		if ev.IsText && strings.Contains(string(ev.Payload), substr) {
			return true
		}
	}
	return false
}

func TestWebSocketTabsKeepIndependentSessionsAndLifecycle(t *testing.T) {
	srv := newWSEchoServer(t)
	a, ps, w := openWSProject(t, "WS Isolation")

	if _, err := ps.createWebSocketIn("", "Alpha"); err != nil {
		t.Fatal(err)
	}
	alpha := ps.activeTab()
	alpha.URL = srv.wsURL("/alpha")
	alpha.Dirty = false

	if _, err := ps.createWebSocketIn("", "Beta"); err != nil {
		t.Fatal(err)
	}
	beta := ps.activeTab()
	beta.URL = srv.wsURL("/beta")
	beta.Dirty = false

	if !isWebSocketTab(alpha) || !isWebSocketTab(beta) {
		t.Fatal("new WebSocket requests must open WebSocket tabs")
	}
	if alpha.ws == nil || beta.ws == nil || alpha.ws == beta.ws {
		t.Fatal("each WebSocket tab must own independent state")
	}

	// Beta is active while Alpha connects: the background callback must land on
	// Alpha, not on whichever tab is on screen.
	ps.activateTab(beta)
	a.wsTabConnect(alpha)
	waitWS(t, w, "alpha open", func() bool {
		return alpha.ws.session != nil && alpha.ws.session.State() == gows.StateOpen
	})
	if beta.ws.session != nil {
		t.Fatal("alpha's connect callback leaked into beta")
	}

	a.wsTabConnect(beta)
	waitWS(t, w, "beta open", func() bool {
		return beta.ws.session != nil && beta.ws.session.State() == gows.StateOpen
	})
	if alpha.ws.session == beta.ws.session || alpha.ws.mailbox == beta.ws.mailbox {
		t.Fatal("tabs must not share a session or mailbox")
	}

	alpha.ws.textDraft = "alpha-payload"
	if alpha.ws.session.State() != gows.StateOpen {
		sn := alpha.ws.session.Snapshot()
		t.Fatalf("alpha session not open before send: state=%s end=%s err=%q", sn.State, sn.End, sn.LastErr)
	}
	a.wsTabSend(alpha)
	waitWS(t, w, "alpha echo", func() bool { return wsLogContains(alpha, "echo/alpha:alpha-payload") })
	if wsLogContains(beta, "alpha-payload") {
		t.Fatal("alpha's message appeared in beta's timeline")
	}

	beta.ws.textDraft = "beta-payload"
	a.wsTabSend(beta)
	waitWS(t, w, "beta echo", func() bool { return wsLogContains(beta, "echo/beta:beta-payload") })
	if wsLogContains(alpha, "beta-payload") {
		t.Fatal("beta's message appeared in alpha's timeline")
	}

	// Closing Alpha's tab releases only Alpha's connection.
	a.startClose(ps, []*Tab{alpha})
	if ps.indexOfTab(alpha) >= 0 {
		t.Fatal("alpha tab was not closed")
	}
	if alpha.ws.session.State() != gows.StateClosed {
		t.Fatalf("closing the tab must terminate its session, got %s", alpha.ws.session.State())
	}
	if beta.ws.session.State() != gows.StateOpen {
		t.Fatalf("closing alpha must not touch beta, got %s", beta.ws.session.State())
	}

	// Closing the window releases every remaining connection.
	a.win.Close()
	if beta.ws.session.State() != gows.StateClosed {
		t.Fatalf("closing the window must terminate the session, got %s", beta.ws.session.State())
	}
}

func TestWebSocketConnectResolvesEnvironmentURL(t *testing.T) {
	srv := newWSEchoServer(t)
	a, ps, w := openWSProject(t, "WS Env")
	ps.project.Environments["dev"] = &core.Environment{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            "dev",
		Variables:     map[string]string{"wsUrl": srv.wsURL("/env")},
	}
	ps.project.EnvOrder = append(ps.project.EnvOrder, "dev")
	ps.envSel = "dev"

	if _, err := ps.createWebSocketIn("", "EnvSocket"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = "{{wsUrl}}"
	a.wsTabConnect(tab)
	waitWS(t, w, "env connect", func() bool {
		return tab.ws.session != nil && tab.ws.session.State() == gows.StateOpen
	})
	if got := tab.ws.session.Snapshot().URL; got != srv.wsURL("/env") {
		t.Fatalf("environment URL not resolved: %q", got)
	}
	tab.ws.textDraft = "env-payload"
	a.wsTabSend(tab)
	waitWS(t, w, "env echo", func() bool { return wsLogContains(tab, "echo/env:env-payload") })
}

func TestWebSocketConnectAppliesAuthAndHeadersWithoutPersistingSecrets(t *testing.T) {
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-token" || r.Header.Get("X-Trace") != "trace-1" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		c, err := up.Upgrade(w, r, nil)
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

	a, ps, w := openWSProject(t, "WS Auth")
	ps.secrets = core.MapSecrets{"token": "test-token"}
	entry, err := ps.createWebSocketIn("", "AuthSocket")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = "ws" + strings.TrimPrefix(srv.URL, "http")
	tab.HeaderRows = append(tab.HeaderRows, &KVRow{
		ID: nextRowID(), Enabled: true, Name: "X-Trace", Value: "trace-1",
	})
	tab.AuthMode = "bearer"
	tab.AuthToken = "{{secret.token}}"
	tab.Dirty = true

	a.wsTabConnect(tab)
	waitWS(t, w, "authorized open", func() bool {
		return tab.ws.session != nil && tab.ws.session.State() == gows.StateOpen
	})

	// The resolved token lives only in memory; the project file keeps the
	// template so no credential is written to disk.
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	raw, _, err := ps.project.Store.Read(entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "test-token") {
		t.Fatalf("resolved secret leaked to disk:\n%s", raw)
	}
	if !strings.Contains(string(raw), "{{secret.token}}") {
		t.Fatalf("auth template not preserved:\n%s", raw)
	}
}

func TestWebSocketRequestPersistsProtocolConfigWithoutSessionState(t *testing.T) {
	a, ps, _ := openWSProject(t, "WS Persist")
	entry, err := ps.createWebSocketIn("", "Socket")
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = "wss://example.test/socket"
	tab.Timeout = "45s"
	tab.ws.subprotoDraft = "graphql-transport-ws, chat"
	tab.ws.compress = true
	tab.HeaderRows = append(tab.HeaderRows, &KVRow{
		ID: nextRowID(), Enabled: true,
		Name: "Authorization", Value: "Bearer {{secret.token}}",
	})
	tab.AuthMode = "bearer"
	tab.AuthToken = "{{secret.apiToken}}"
	tab.Dirty = true

	if err := a.saveTabSynced(tab); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, _, err := ps.project.Store.Read(entry.RelPath)
	if err != nil {
		t.Fatal(err)
	}
	yaml := string(raw)
	for _, want := range []string{
		"protocol: websocket",
		"ws:",
		"subprotocols:",
		"compression: true",
		"wss://example.test/socket",
		"Authorization",
		"{{secret.token}}",
		"{{secret.apiToken}}",
		"timeout: 45s",
	} {
		if !strings.Contains(yaml, want) {
			t.Fatalf("saved YAML missing %q:\n%s", want, yaml)
		}
	}
	// No live-session object, generation, mailbox or resolved credential is
	// ever written to the project file.
	for _, forbidden := range []string{"session", "mailbox", "generation", "redactor", "OPEN"} {
		if strings.Contains(yaml, forbidden) {
			t.Fatalf("saved YAML must not contain %q:\n%s", forbidden, yaml)
		}
	}

	// Reopening from disk keeps the protocol and configuration.
	reloaded, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	if n := core.ErrorCount(reloaded.Issues); n != 0 {
		t.Fatalf("reloaded project has %d validation errors: %v", n, reloaded.Issues)
	}
	re := reloaded.Requests[entry.Request.ID]
	if re == nil || re.Request.Protocol != core.ProtocolWebSocket {
		t.Fatalf("protocol not preserved: %+v", re)
	}
	if re.Request.WS == nil || len(re.Request.WS.Subprotocols) != 2 || !re.Request.WS.Compression {
		t.Fatalf("ws options not preserved: %+v", re.Request.WS)
	}
	if re.Request.HTTP == nil || re.Request.HTTP.URL != "wss://example.test/socket" {
		t.Fatalf("endpoint not preserved: %+v", re.Request.HTTP)
	}

	// Reopen the tab from the tree selection and verify the editable fields.
	ps.removeTab(tab)
	if err := ps.openSelection("req:" + entry.Request.ID); err != nil {
		t.Fatal(err)
	}
	reopened := ps.activeTab()
	if !isWebSocketTab(reopened) {
		t.Fatal("reopened tab is not a WebSocket tab")
	}
	if reopened.URL != "wss://example.test/socket" || reopened.Timeout != "45s" {
		t.Fatalf("reopened drafts wrong: url=%q timeout=%q", reopened.URL, reopened.Timeout)
	}
	if reopened.ws.subprotoDraft != "graphql-transport-ws, chat" || !reopened.ws.compress {
		t.Fatalf("reopened ws options wrong: %q compress=%v", reopened.ws.subprotoDraft, reopened.ws.compress)
	}
	pairs, err := kvFromRows(reopened.HeaderRows)
	if err != nil {
		t.Fatal(err)
	}
	foundAuth := false
	for _, kv := range pairs {
		if kv.Name == "Authorization" && kv.Value == "Bearer {{secret.token}}" {
			foundAuth = true
		}
	}
	if !foundAuth {
		t.Fatalf("reopened headers lost the template: %+v", pairs)
	}

	// A fresh manager (a real restart) opens the same project and tree entry as
	// a WebSocket tab with the same configuration.
	m2, _ := newTestManager(t)
	if err := m2.openProject(ps.root); err != nil {
		t.Fatalf("restart open: %v", err)
	}
	ps2 := m2.workspaces[0].ps
	if err := ps2.openSelection("req:" + entry.Request.ID); err != nil {
		t.Fatal(err)
	}
	restarted := ps2.activeTab()
	if !isWebSocketTab(restarted) {
		t.Fatal("restarted project opened the request as HTTP")
	}
	if restarted.URL != "wss://example.test/socket" || restarted.ws.subprotoDraft != "graphql-transport-ws, chat" || !restarted.ws.compress {
		t.Fatalf("restarted tab lost configuration: url=%q ws=%q compress=%v",
			restarted.URL, restarted.ws.subprotoDraft, restarted.ws.compress)
	}

	// An ordinary HTTP request still opens as an HTTP tab.
	httpEntry, err := ps.project.NewRequest("", "Plain HTTP")
	if err != nil {
		t.Fatal(err)
	}
	ps.openRequest(httpEntry)
	httpTab := ps.activeTab()
	if isWebSocketTab(httpTab) || httpTab.ws != nil {
		t.Fatal("an HTTP request must not get WebSocket state")
	}
	req, err := requestFromTab(httpTab)
	if err != nil {
		t.Fatal(err)
	}
	if req.Protocol != core.ProtocolHTTP {
		t.Fatalf("HTTP request protocol changed: %q", req.Protocol)
	}
}

func TestWebSocketWorkspaceConnectsFromHeadlessUI(t *testing.T) {
	srv := newWSEchoServer(t)
	a, ps, w := openWSProject(t, "WS UI Connect")
	if _, err := ps.createWebSocketIn("", "Socket"); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.URL = srv.wsURL("/ui")

	tt := ui.NewTester(w.view(a), 1200, 820)
	if err := tt.Click("Connect"); err != nil {
		t.Fatalf("click Connect: %v", err)
	}
	waitWS(t, w, "UI connect", func() bool {
		return tab.ws.session != nil && tab.ws.session.State() == gows.StateOpen
	})
	tt.Frame()
	if !tt.HasText("OPEN") {
		t.Fatalf("status badge did not become OPEN: %v", tt.Texts())
	}
	if err := tt.Click("Disconnect"); err != nil {
		t.Fatalf("click Disconnect: %v", err)
	}
	tt.Frame()
	if tab.ws.session.State() != gows.StateClosed {
		t.Fatalf("Disconnect did not close the session: %s", tab.ws.session.State())
	}
}

func TestWebSocketWorkspaceEntryFromNewMenuAndTree(t *testing.T) {
	a, ps, w := openWSProject(t, "WS UI")
	tt := ui.NewTester(w.view(a), 1200, 820)

	if err := tt.Click("+"); err != nil {
		t.Fatalf("open new menu: %v", err)
	}
	tt.Frame()
	if !tt.HasText("New WebSocket") {
		t.Fatalf("new menu missing WebSocket entry: %v", tt.Texts())
	}
	if err := tt.Click("New WebSocket"); err != nil {
		t.Fatalf("choose WebSocket: %v", err)
	}
	tt.Frame()

	tab := ps.activeTab()
	if tab == nil || !isWebSocketTab(tab) {
		t.Fatal("the new menu must open a WebSocket tab")
	}
	for _, want := range []string{"WS", "Connect", "Headers", "Auth", "Settings", "Messages"} {
		if !tt.HasText(want) {
			t.Fatalf("WebSocket workspace missing %q: %v", want, tt.Texts())
		}
	}
	// Every configuration pane must render without leaving the workspace. The
	// section index is set directly because the sidebar also has an exact
	// "Settings" label, which would make a text click ambiguous.
	for i := 0; i <= 2; i++ {
		tab.ws.section = i
		tt.Frame()
	}
	if !tt.HasText("Subprotocols") || !tt.HasText("Total session deadline") {
		t.Fatalf("settings pane missing WebSocket options: %v", tt.Texts())
	}

	// Close the tab, then reopen it from the project tree.
	a.startClose(ps, []*Tab{tab})
	tt.Frame()
	if len(ps.tabs) != 0 {
		t.Fatalf("tab not closed: %d", len(ps.tabs))
	}
	if err := tt.Click("WebSocket"); err != nil {
		t.Fatalf("reopen from tree: %v", err)
	}
	tt.Frame()
	if len(ps.tabs) != 1 || !isWebSocketTab(ps.activeTab()) {
		t.Fatal("tree navigation must reopen the WebSocket tab")
	}
}
