package desktop

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/gorilla/websocket"

	"gorequest/internal/protocolrun"
	"gorequest/internal/stream"
)

// These tests cover the bounded script-action surface: opening and validating
// a plan never touches the network, only an explicit run opens one connection,
// the per-step facts keep direction/sequence/cursor separate, and the Script
// section never renders the hand-driven message pane.

// wsPlanSnapshot reads one tab's plan state under the window mutex, so the test
// goroutine never races the background run callback that writes the same
// fields from inside Window.Update.
func wsPlanSnapshot(w *wsTestWindow, tab *Tab) (running bool, result *protocolrun.Result, loadErr, note string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if tab == nil || tab.ws == nil || tab.ws.plan == nil {
		return false, nil, "", ""
	}
	pl := tab.ws.plan
	return pl.running, pl.result, pl.err, pl.note
}

// writePlanFile writes a plan document into a temp file and returns its path.
func writePlanFile(t *testing.T, doc string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "plan.json")
	if err := os.WriteFile(path, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// openWSSocket creates a WebSocket tab in the project and returns it.
func openWSSocket(t *testing.T, ps *projectState, name string) *Tab {
	t.Helper()
	if _, err := ps.createWebSocketIn("", name); err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	if tab == nil || !isWebSocketTab(tab) {
		t.Fatal("expected a WebSocket tab")
	}
	return tab
}

func TestWebSocketPlanLoadValidatesWithoutNetwork(t *testing.T) {
	a, ps, _ := openWSProject(t, "WS Plan Load")
	tab := openWSSocket(t, ps, "Socket")

	path := writePlanFile(t, `{"version":1,"name":"load","steps":[
	 {"action":"connect","url":"ws://127.0.0.1:1/ws","label":"open"},
	 {"action":"send","text":"hi"},
	 {"action":"close","code":1000}]}`)
	a.wsPlanLoad(tab, path)

	pl := tab.ws.plan
	if pl == nil || pl.plan == nil {
		t.Fatalf("plan was not loaded: %+v", pl)
	}
	if len(pl.plan.Steps) != 3 {
		t.Fatalf("loaded steps = %d", len(pl.plan.Steps))
	}
	if !strings.Contains(pl.note, "3 steps") {
		t.Fatalf("load note = %q", pl.note)
	}
	if pl.err != "" {
		t.Fatalf("load reported an error: %q", pl.err)
	}
	// Loading is offline: no session, no run, nothing connected.
	if tab.ws.session != nil {
		t.Fatal("loading a plan must not open a connection")
	}
	if pl.running || pl.result != nil {
		t.Fatal("loading a plan must not start a run")
	}

	// An invalid document is reported and leaves no stale plan behind.
	bad := writePlanFile(t, `{"version":1,"steps":[{"action":"teleport"}]}`)
	a.wsPlanLoad(tab, bad)
	if tab.ws.plan.err == "" {
		t.Fatal("invalid plan must surface a load error")
	}
	if !strings.Contains(tab.ws.plan.err, "Could not open the plan") {
		t.Fatalf("load error = %q", tab.ws.plan.err)
	}
}

func TestWebSocketPlanRunRequiresAPlanAndAProject(t *testing.T) {
	a, ps, _ := openWSProject(t, "WS Plan Guard")
	tab := openWSSocket(t, ps, "Socket")

	// No file selected yet: refuse instead of running an empty plan.
	a.wsPlanRun(tab)
	if got := tab.ws.plan.err; got == "" || !strings.Contains(got, "Open a plan file") {
		t.Fatalf("missing-plan error = %q", got)
	}

	// A detached tab has no variable scope; refuse instead of dereferencing.
	detached := &Tab{Owner: ps}
	a.wsPlanLoad(detached, writePlanFile(t, `{"version":1,"steps":[{"action":"connect","url":"ws://127.0.0.1:1/ws"}]}`))
	a.wsPlanRun(detached)
	if got := detached.ws.plan.err; got == "" || !strings.Contains(got, "needs a project") {
		t.Fatalf("detached-tab error = %q", got)
	}
}

func TestWebSocketPlanRunKeepsPerStepFacts(t *testing.T) {
	srv := newWSEchoServer(t)
	a, ps, w := openWSProject(t, "WS Plan Run")
	tab := openWSSocket(t, ps, "Socket")

	path := writePlanFile(t, fmt.Sprintf(`{"version":1,"name":"desktop-echo",
	 "limits":{"totalTimeout":"5s"},
	 "steps":[
	  {"action":"connect","url":%q,"label":"open"},
	  {"action":"send","text":"hi","label":"say hi"},
	  {"action":"await","match":{"contains":"echo/plan:hi"},"timeout":"2s","label":"hear echo"},
	  {"action":"close","code":1000,"reason":"done"}]}`, srv.wsURL("/plan")))

	a.wsPlanLoad(tab, path)
	if tab.ws.plan.err != "" {
		t.Fatalf("load: %s", tab.ws.plan.err)
	}
	a.wsPlanRun(tab)

	waitWS(t, w, "plan result", func() bool {
		running, res, _, _ := wsPlanSnapshot(w, tab)
		return !running && res != nil
	})

	running, res, loadErr, note := wsPlanSnapshot(w, tab)
	if running || loadErr != "" {
		t.Fatalf("run did not finish cleanly: running=%v err=%q", running, loadErr)
	}
	if res.State != protocolrun.StatePassed {
		t.Fatalf("run state = %s err=%q steps=%+v", res.State, res.Error, res.Steps)
	}
	if len(res.Steps) != 4 {
		t.Fatalf("step results = %d", len(res.Steps))
	}
	if res.SentCount != 1 || res.ReceivedCount != 1 {
		t.Fatalf("totals: sent=%d received=%d", res.SentCount, res.ReceivedCount)
	}
	if res.CloseCode != 1000 || res.CloseReason != "done" {
		t.Fatalf("close facts = %d %q", res.CloseCode, res.CloseReason)
	}
	// Direction, sequence and cursor movement stay per step; nothing is folded
	// into one status.
	send, await := res.Steps[1], res.Steps[2]
	if send.Direction != stream.DirOut || send.SentSeq == 0 {
		t.Fatalf("send facts = %+v", send)
	}
	if await.Direction != stream.DirIn || await.ReceivedSeq == 0 {
		t.Fatalf("await facts = %+v", await)
	}
	if await.CursorBefore != 0 || await.CursorAfter <= await.CursorBefore {
		t.Fatalf("await cursor = %d→%d", await.CursorBefore, await.CursorAfter)
	}
	if !strings.Contains(string(await.Payload), "echo/plan:hi") {
		t.Fatalf("await payload = %q", await.Payload)
	}
	if res.Steps[0].Direction != stream.DirNone {
		t.Fatalf("connect direction = %v", res.Steps[0].Direction)
	}
	if !strings.Contains(note, "Run finished") {
		t.Fatalf("run note = %q", note)
	}
}

func TestWebSocketPlanCancelStopsTheRun(t *testing.T) {
	// The server signals acceptance so the test cancels only after the plan's
	// own connection is up; the await step then blocks until cancel.
	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	accepted := make(chan struct{})
	var once sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		c, err := up.Upgrade(rw, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		once.Do(func() { close(accepted) })
		for {
			if _, _, err := c.ReadMessage(); err != nil {
				return
			}
		}
	}))
	t.Cleanup(srv.Close)

	a, ps, w := openWSProject(t, "WS Plan Cancel")
	tab := openWSSocket(t, ps, "Socket")

	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	path := writePlanFile(t, fmt.Sprintf(`{"version":1,"name":"desktop-cancel",
	 "limits":{"totalTimeout":"20s"},
	 "steps":[{"action":"connect","url":%q},
	  {"action":"await","match":{"contains":"never-arrives"},"timeout":"15s"}]}`, url))

	a.wsPlanLoad(tab, path)
	a.wsPlanRun(tab)
	select {
	case <-accepted:
	case <-time.After(10 * time.Second):
		t.Fatal("plan never connected")
	}
	w.Update(func() { a.wsPlanCancel(tab) })
	waitWS(t, w, "canceled run", func() bool {
		running, res, _, _ := wsPlanSnapshot(w, tab)
		return !running && res != nil
	})

	_, res, _, _ := wsPlanSnapshot(w, tab)
	if res.State != protocolrun.StateCanceled {
		t.Fatalf("canceled run state = %s err=%q", res.State, res.Error)
	}
	// The cancel fact stays on the step that was in flight; it is never folded
	// into a generic failure or an HTTP-like status.
	last := res.Steps[len(res.Steps)-1]
	if last.State != protocolrun.StateFailed || !strings.Contains(last.Error, "cancel") {
		t.Fatalf("in-flight step did not record the cancel: %+v", last)
	}
	if res.FailedStep != len(res.Steps)-1 {
		t.Fatalf("failed step = %d of %d", res.FailedStep, len(res.Steps))
	}
}

func TestWebSocketScriptSectionReplacesManualPane(t *testing.T) {
	a, ps, w := openWSProject(t, "WS Script UI")
	tab := openWSSocket(t, ps, "Socket")

	path := writePlanFile(t, `{"version":1,"name":"ui","steps":[
	 {"action":"connect","url":"ws://127.0.0.1:1/ws","label":"open"},
	 {"action":"send","text":"hi","label":"say hi"},
	 {"action":"close","code":1000}]}`)
	a.wsPlanLoad(tab, path)
	tab.ws.section = wsSectionScript

	tt := ui.NewTester(w.view(a), 1200, 820)
	tt.Frame()

	if !tt.HasText("Script") || !tt.HasText("Declared steps") {
		t.Fatalf("script section missing: %v", tt.Texts())
	}
	if !tt.HasText("open") || !tt.HasText("say hi") {
		t.Fatalf("declared steps not listed: %v", tt.Texts())
	}
	// The bounded surface must not present itself as a hand-driven session.
	if tt.HasText("Messages") {
		t.Fatalf("manual message pane still rendered in Script mode: %v", tt.Texts())
	}
	if !tt.HasText("Plan run") || !tt.HasText("No run yet — press Run plan.") {
		t.Fatalf("plan result pane missing: %v", tt.Texts())
	}

	// Switching back restores the manual debugging surface.
	tab.ws.section = 0
	tt.Frame()
	if !tt.HasText("Messages") {
		t.Fatalf("manual pane did not return: %v", tt.Texts())
	}
}

// TestWebSocketPlanRefPersistsAndRestores verifies that loading a plan inside
// the project store binds a project-relative reference to the request (so a
// bounded WS request can be executed later without copying the plan body) and
// that opening the tab again restores the plan from that reference without
// network I/O.
func TestWebSocketPlanRefPersistsAndRestores(t *testing.T) {
	a, ps, _ := openWSProject(t, "WS Plan Ref")
	tab := openWSSocket(t, ps, "Socket")
	if tab.Entry == nil || tab.Entry.Request == nil {
		t.Fatal("expected a request entry on the WebSocket tab")
	}

	// A plan file inside the project store must be persisted as relative ref.
	planDir := filepath.Join(ps.project.Dir, "protocols")
	if err := os.MkdirAll(planDir, 0o755); err != nil {
		t.Fatal(err)
	}
	abs := filepath.Join(planDir, "ticks.json")
	doc := `{"version":1,"name":"ticks","steps":[{"action":"connect","url":"ws://127.0.0.1:1/ws"}]}`
	if err := os.WriteFile(abs, []byte(doc), 0o644); err != nil {
		t.Fatal(err)
	}

	a.wsPlanLoad(tab, abs)
	ref, ok := protocolrun.PlanRefFromRequest(tab.Entry.Request)
	if !ok || ref.Path != "protocols/ticks.json" || ref.Digest == "" {
		t.Fatalf("plan ref not persisted: %+v ok=%v", ref, ok)
	}
	if !tab.Dirty {
		t.Fatal("persisting a project-relative plan ref must mark the tab dirty")
	}

	// Simulate a reopen: drop the tab-local state and restore from the ref.
	tab.ws.plan = nil
	a.wsPlanEnsure(tab)
	st := tab.ensureWSState()
	if st.plan == nil || st.plan.plan == nil || st.plan.path != abs {
		t.Fatalf("plan was not restored from its ref: %+v", st.plan)
	}

	// A file outside the project store stays tab-local and writes no ref.
	other := writePlanFile(t, `{"version":1,"name":"local","steps":[{"action":"connect","url":"ws://127.0.0.1:1/ws"}]}`)
	tab2 := openWSSocket(t, ps, "Socket 2")
	a.wsPlanLoad(tab2, other)
	if _, ok := protocolrun.PlanRefFromRequest(tab2.Entry.Request); ok {
		t.Fatal("a plan outside the project store must not be persisted into the request")
	}
}
