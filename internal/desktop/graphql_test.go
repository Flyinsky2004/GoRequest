package desktop

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func openGraphQLTab(t *testing.T, ps *projectState, name string) *Tab {
	t.Helper()
	entry, err := ps.createGraphQLIn("", name)
	if err != nil {
		t.Fatalf("createGraphQLIn: %v", err)
	}
	if ps.activeTab() == nil || ps.activeTab().Entry != entry {
		t.Fatal("GraphQL tab was not opened")
	}
	return ps.activeTab()
}

func TestGraphQLTabSaveReopenPreservesDocumentAndVariables(t *testing.T) {
	_, ps := openTestProject(t, "GQL Save")
	tab := openGraphQLTab(t, ps, "GQL Save")
	st := tab.ensureGQLState()
	st.document = "query Me {\n  me { id }\n}"
	st.operationName = "Me"
	st.variables = `{"id": 9007199254740993}`
	tab.Dirty = true

	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("save: %v", err)
	}
	rel := tab.Entry.RelPath
	data, err := os.ReadFile(filepath.Join(ps.root, ".gorequest", rel))
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"protocol: graphql", "document:", "operationName: Me", "9007199254740993"} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("saved YAML lost %q:\n%s", want, data)
		}
	}
	if strings.Contains(string(data), "body:") {
		t.Fatalf("graphql request must not write http.body:\n%s", data)
	}

	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	entry := reopened.Requests[tab.Entry.Request.ID]
	if entry == nil || entry.Request.GraphQL == nil {
		t.Fatal("reopened request lost its graphql block")
	}
	if entry.Request.GraphQL.Document != st.document || entry.Request.GraphQL.OperationName != "Me" {
		t.Fatalf("reopened graphql = %+v", entry.Request.GraphQL)
	}
	if !strings.Contains(entry.Request.GraphQL.Variables, "9007199254740993") {
		t.Fatalf("reopened variables = %q", entry.Request.GraphQL.Variables)
	}
}

func TestGraphQLTabURLOnlyEditKeepsGraphQLData(t *testing.T) {
	_, ps := openTestProject(t, "GQL URL")
	tab := openGraphQLTab(t, ps, "GQL URL")
	st := tab.ensureGQLState()
	st.document = "query Q { q }"
	st.variables = `{"n": 1}`
	tab.Dirty = true
	if err := ps.saveTab(tab); err != nil {
		t.Fatal(err)
	}

	tab.URL = "https://api.example.com/v2/graphql"
	tab.Dirty = true
	if err := ps.saveTab(tab); err != nil {
		t.Fatalf("second save: %v", err)
	}
	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatal(err)
	}
	g := reopened.Requests[tab.Entry.Request.ID].Request.GraphQL
	if g == nil || g.Document != "query Q { q }" || g.Variables != `{"n": 1}` {
		t.Fatalf("URL-only edit lost graphql data: %+v", g)
	}
}

func TestGraphQLTabExternalConflictIsReported(t *testing.T) {
	_, ps := openTestProject(t, "GQL Conflict")
	tab := openGraphQLTab(t, ps, "GQL Conflict")
	st := tab.ensureGQLState()
	st.document = "query Q { q }"
	tab.Dirty = true
	if err := ps.saveTab(tab); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(ps.root, ".gorequest", tab.Entry.RelPath)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = f.WriteString("# external edit\n")
	_ = f.Close()

	tab.URL = "https://changed.example/graphql"
	tab.Dirty = true
	err = ps.saveTab(tab)
	if err == nil || !core.IsConflict(err) {
		t.Fatalf("expected a conflict, got %v", err)
	}
}

func TestGraphQLProjectsKeepIndependentDocuments(t *testing.T) {
	_, psA := openTestProject(t, "GQL A")
	_, psB := openTestProject(t, "GQL B")
	tabA := openGraphQLTab(t, psA, "Doc A")
	tabB := openGraphQLTab(t, psB, "Doc B")
	tabA.ensureGQLState().document = "query A { a }"
	tabB.ensureGQLState().document = "query B { b }"
	if tabA.gqlDef().Document != "query A { a }" || tabB.gqlDef().Document != "query B { b }" {
		t.Fatalf("documents crossed projects: %q / %q", tabA.gqlDef().Document, tabB.gqlDef().Document)
	}
	tabA.Dirty = true
	if err := psA.saveTab(tabA); err != nil {
		t.Fatal(err)
	}
	if tabB.gqlDef().Document != "query B { b }" {
		t.Fatal("saving project A changed project B's draft")
	}
}

func TestGraphQLWorkspaceRendersAndSendsThroughRealButton(t *testing.T) {
	var mu sync.Mutex
	var gotQuery, gotVars string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var env struct {
			Query     string          `json:"query"`
			Variables json.RawMessage `json:"variables"`
		}
		_ = json.Unmarshal(body, &env)
		mu.Lock()
		gotQuery, gotVars = env.Query, string(env.Variables)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"ok":true}}`))
	}))
	defer srv.Close()

	a, ps, qWin := sendStatusApp(t, "GQL UI")
	tab := openGraphQLTab(t, ps, "GQL UI")
	st := tab.ensureGQLState()
	tab.URL = srv.URL
	st.document = "query Q($n: Int!) { ok(n: $n) }"
	st.variables = `{"n": 9007199254740993}`
	tab.Dirty = true

	view := func(c *ui.Context) { a.gqlTabView(c, c.Theme(), tab) }
	tt := ui.NewTester(view, 1200, 820)
	tt.Frame()
	for _, want := range []string{"Document", "Variables", "Headers", "Auth", "Send"} {
		if !tt.HasText(want) {
			t.Fatalf("GraphQL workspace missing %q: %v", want, tt.Texts())
		}
	}
	if err := tt.Click("Variables"); err != nil {
		t.Fatalf("click Variables: %v", err)
	}
	tt.Frame()
	if !tt.HasText("JSON object") {
		t.Fatalf("variables hint missing: %v", tt.Texts())
	}
	if err := tt.Click("Document"); err != nil {
		t.Fatalf("click Document: %v", err)
	}
	tt.Frame()
	if !tt.HasText("POST") {
		t.Fatalf("method selector missing: %v", tt.Texts())
	}

	if err := tt.Click("Send"); err != nil {
		t.Fatalf("click Send: %v", err)
	}
	drainSend(t, qWin, tab)
	if tab.Resp == nil || tab.Resp.Status != 200 {
		t.Fatalf("response = %+v err=%q", tab.Resp, tab.RespErr)
	}
	cls := gqlResult(tab)
	if !cls.DataPresent || cls.DataNull || cls.HasErrors() {
		t.Fatalf("classification = %+v", cls)
	}
	mu.Lock()
	defer mu.Unlock()
	if gotQuery != st.document {
		t.Fatalf("server query = %q", gotQuery)
	}
	if !strings.Contains(gotVars, "9007199254740993") {
		t.Fatalf("server variables = %q", gotVars)
	}
}

func TestGraphQLErrorViewMasksRegisteredSecrets(t *testing.T) {
	a, ps := openTestProject(t, "GQL Mask")
	tab := openGraphQLTab(t, ps, "GQL Mask")
	tab.Resp = &core.ExecResult{Status: 200, Body: []byte(`{"errors":[{"message":"bad SECRET-X","path":["field"]}]}`)}
	red := core.NewRedactor()
	red.RegisterSecret("SECRET-X")
	tab.respRedactor = red
	tab.ensureGQLState().resultSection = 1

	view := func(c *ui.Context) { a.gqlTabView(c, c.Theme(), tab) }
	tt := ui.NewTester(view, 1200, 820)
	tt.Frame()
	if tt.HasText("SECRET-X") {
		t.Fatalf("secret leaked into the GraphQL error pane: %v", tt.Texts())
	}
	if !tt.HasText("bad ***") {
		t.Fatalf("expected the masked message, got %v", tt.Texts())
	}
}

func TestGraphQLRequestFromTabNeverWritesHTTPBody(t *testing.T) {
	_, ps := openTestProject(t, "GQL Body")
	tab := openGraphQLTab(t, ps, "GQL Body")
	st := tab.ensureGQLState()
	st.document = "query Q { q }"
	st.variables = `{}`
	tab.BodyText = `{"should":"not appear"}`
	tab.BodyIdx = 0
	req, err := requestFromTab(tab)
	if err != nil {
		t.Fatal(err)
	}
	if req.Protocol != core.ProtocolGraphQL || req.HTTP.Body != nil {
		t.Fatalf("request = %+v http=%+v", req, req.HTTP)
	}
	if req.GraphQL == nil || req.GraphQL.Document != "query Q { q }" {
		t.Fatalf("graphql = %+v", req.GraphQL)
	}
}
