package desktop

import (
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	gorilla "github.com/gorilla/websocket"
	"gorequest/internal/core"
)

func TestFormal4SchemaExplorerAndSafeSave(t *testing.T) {
	a, ps := openTestProject(t, "Schema integration")
	tab := openGraphQLTab(t, ps, "Schema query")
	sdl := "type Query { hello: String }"
	if err := ps.project.Store.WriteAtomic("schemas/local.graphql", []byte(sdl)); err != nil {
		t.Fatal(err)
	}
	a.gqlLoadSchemaPath(tab, filepath.Join(ps.project.Dir, "schemas/local.graphql"))
	st := tab.gql
	st.section = 2
	st.schemaTypeSel = "Query"
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	if !tt.HasText("hello: String") {
		t.Fatalf("schema explorer unreachable: %v", tt.Texts())
	}
	protected := []byte("manually authored schema")
	if err := ps.project.Store.WriteAtomic("schemas/introspected.graphql", protected); err != nil {
		t.Fatal(err)
	}
	model, err := core.ParseGraphQLSchemaSDL("preview", sdl)
	if err != nil {
		t.Fatal(err)
	}
	st.introspect = &gqlIntrospectionPreview{SDL: sdl, Model: model}
	tt.Frame()
	if err := tt.Click(a.gqlMsg().SchemaIntrospectSave); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got, _, err := ps.project.Store.Read("schemas/introspected.graphql")
	if err != nil || string(got) != string(protected) {
		t.Fatalf("manual schema overwritten: %q %v", got, err)
	}
	if st.schemaRef.Path != "schemas/introspected-2.graphql" {
		t.Fatalf("save reference: %+v", st.schemaRef)
	}
}

func TestFormal4SubscriptionTabCloseCancelsConnection(t *testing.T) {
	closed := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := (&gorilla.Upgrader{Subprotocols: []string{"graphql-transport-ws"}}).Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer c.Close()
		defer close(closed)
		var msg map[string]any
		if c.ReadJSON(&msg) != nil {
			return
		}
		if c.WriteJSON(map[string]any{"type": "connection_ack"}) != nil {
			return
		}
		if c.ReadJSON(&msg) != nil {
			return
		}
		if c.WriteJSON(map[string]any{"id": msg["id"], "type": "next", "payload": map[string]any{"data": map[string]any{"tick": 1}}}) != nil {
			return
		}
		for {
			if c.ReadJSON(&msg) != nil {
				return
			}
			if msg["type"] == "complete" {
				return
			}
		}
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()
	a, ps, w := openWSProject(t, "Subscription integration")
	var tab *Tab
	w.Update(func() {
		tab = openGraphQLTab(t, ps, "Ticks")
		tab.URL = srv.URL
		tab.gql.document = "subscription { tick }"
		tab.gql.parsedDoc = ""
		a.gqlTrySend(tab)
	})
	waitWS(t, w, "subscription event", func() bool {
		w.mu.Lock()
		defer w.mu.Unlock()
		return tab.gql.sub != nil && len(tab.gql.sub.messages) > 0
	})
	tt := ui.NewTester(w.view(a), 1200, 820)
	tt.Frame()
	tt.Frame()
	w.Update(func() {
		if !ps.removeTab(tab) {
			t.Error("tab not removed")
		}
	})
	select {
	case <-closed:
	case <-time.After(3 * time.Second):
		t.Fatal("subscription outlived tab")
	}
}

func TestFormal4CloseCancelsIntrospection(t *testing.T) {
	started, ended := make(chan struct{}), make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(ended)
	}))
	defer srv.Close()
	defer srv.CloseClientConnections()
	a, ps, w := openWSProject(t, "Introspection integration")
	var tab *Tab
	w.Update(func() { tab = openGraphQLTab(t, ps, "Inspect"); tab.URL = srv.URL; a.gqlIntrospect(tab) })
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("introspection did not start")
	}
	w.Update(func() { ps.removeTab(tab) })
	select {
	case <-ended:
	case <-time.After(3 * time.Second):
		t.Fatal("introspection outlived tab")
	}
}
