package runneradapter

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"gorequest/internal/core"
	"gorequest/internal/protocolrun"
	"gorequest/internal/runner"
)

func fixture(t *testing.T, protocol, endpoint, plan string) (*core.Project, *runner.Suite) {
	t.Helper()
	root := t.TempDir()
	store := core.NewStore(filepath.Join(root, ".gorequest"))
	if err := store.WriteAtomic("project.yaml", []byte("schemaVersion: 1\nid: protocol-fixture\nname: Protocol fixture\n")); err != nil {
		t.Fatal(err)
	}
	p, err := core.OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	var entry *core.RequestEntry
	switch protocol {
	case core.ProtocolWebSocket:
		entry, err = p.NewWebSocketRequest("", "stream")
	case core.ProtocolGraphQL:
		entry, err = p.NewGraphQLRequest("", "query")
	}
	if err != nil {
		t.Fatal(err)
	}
	entry.Request.HTTP.URL = endpoint
	if protocol == core.ProtocolGraphQL {
		entry.Request.GraphQL.Document = "query { hello }"
	}
	if plan != "" {
		if err := p.Store.WriteAtomic("plans/stream.json", []byte(plan)); err != nil {
			t.Fatal(err)
		}
		protocolrun.SetPlanRef(entry.Request, protocolrun.PlanRef{Path: "plans/stream.json", Digest: core.Digest([]byte(plan))})
	}
	return p, &runner.Suite{SchemaVersion: 1, ID: "protocol-suite", Name: "protocol suite", Steps: []runner.SuiteStep{{ID: "call", Request: entry.Request.ID}}}
}

func runSuite(t *testing.T, p *core.Project, s *runner.Suite) *runner.RunResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	res, err := runner.New(p, s, runner.Options{Protocol: New(), PlanOptions: core.PlanOptions{AllowGraphQL: true, ProjectRoot: p.Dir, ProjectFiles: core.NewDirFileOpener(p.Dir)}}).Run(ctx)
	if err != nil {
		t.Fatalf("run: %v result=%+v", err, res)
	}
	return res
}

func TestIntegratedGraphQLRunnerBusinessErrors(t *testing.T) {
	var failure atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if failure.Load() {
			_, _ = w.Write([]byte(`{"data":null,"errors":[{"message":"denied"}]}`))
		} else {
			_, _ = w.Write([]byte(`{"data":{"hello":"world"}}`))
		}
	}))
	defer srv.Close()
	p, s := fixture(t, core.ProtocolGraphQL, srv.URL, "")
	if got := runSuite(t, p, s); got.Status != runner.StatusPassed {
		t.Fatalf("query: %+v", got)
	}
	failure.Store(true)
	if got := runSuite(t, p, s); got.Status == runner.StatusPassed {
		t.Fatal("GraphQL errors passed")
	}
	for _, e := range p.Requests {
		e.Request.Extensions = map[string]any{"graphqlAllowErrors": true}
	}
	if got := runSuite(t, p, s); got.Status != runner.StatusPassed {
		t.Fatalf("expected errors: %+v", got)
	}
}

func TestIntegratedWebSocketRunnerFactsAndVariables(t *testing.T) {
	for _, reject := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "early-close"}[reject], func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				c, err := (&gorilla.Upgrader{}).Upgrade(w, r, nil)
				if err != nil {
					return
				}
				defer c.Close()
				if reject {
					_ = c.WriteControl(gorilla.CloseMessage, gorilla.FormatCloseMessage(1008, "denied"), time.Now().Add(time.Second))
					return
				}
				for {
					kind, payload, err := c.ReadMessage()
					if err != nil {
						return
					}
					if err = c.WriteMessage(kind, payload); err != nil {
						return
					}
				}
			}))
			defer srv.Close()
			endpoint := "ws" + strings.TrimPrefix(srv.URL, "http")
			plan := `{"version":1,"limits":{"totalTimeout":"2s"},"steps":[{"action":"connect","url":"{{endpoint}}"},{"action":"send","text":"hello"},{"action":"await","timeout":"1s","match":{"equals":"hello"}},{"action":"close","code":1000}]}`
			p, s := fixture(t, core.ProtocolWebSocket, endpoint, plan)
			s.Variables = map[string]string{"endpoint": endpoint}
			got := runSuite(t, p, s)
			if !reject && got.Status != runner.StatusPassed {
				t.Fatalf("WebSocket: %+v", got)
			}
			if reject && got.Status == runner.StatusPassed {
				t.Fatal("early close passed")
			}
			a := got.Rows[0].Steps[0].Attempts[0]
			if a.HTTPStatus != 0 || a.Proto != "websocket" || !json.Valid(a.ProtocolFacts) {
				t.Fatalf("lost protocol facts: %+v", a)
			}
			var facts struct {
				CloseCode int `json:"closeCode"`
			}
			if err := json.Unmarshal(a.ProtocolFacts, &facts); err != nil {
				t.Fatal(err)
			}
			if reject && facts.CloseCode != 1008 {
				t.Fatalf("lost close: %s", a.ProtocolFacts)
			}
		})
	}
}

func TestRunnerPlanRejectsHostFilesAndStaleDigest(t *testing.T) {
	p, s := fixture(t, core.ProtocolWebSocket, "ws://127.0.0.1:1", `{"version":1,"steps":[{"action":"connect","url":"ws://127.0.0.1:1"},{"action":"send","file":"/etc/hosts"}]}`)
	r := runner.New(p, s, runner.Options{Protocol: New(), PlanOptions: core.PlanOptions{ProjectRoot: p.Dir, ProjectFiles: core.NewDirFileOpener(p.Dir)}})
	if err := r.Prepare(context.Background()); err == nil {
		t.Fatal("host file was accepted")
	}
	for _, e := range p.Requests {
		protocolrun.SetPlanRef(e.Request, protocolrun.PlanRef{Path: "plans/stream.json", Digest: "changed"})
	}
	if err := runner.New(p, s, runner.Options{Protocol: New(), PlanOptions: core.PlanOptions{ProjectRoot: p.Dir, ProjectFiles: core.NewDirFileOpener(p.Dir)}}).Prepare(context.Background()); err == nil {
		t.Fatal("stale plan accepted")
	}
}
