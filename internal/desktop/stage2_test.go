package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// TestUndefinedVariableBlocksSend verifies the pre-send guard (§5/D7): a URL
// with an undefined variable opens a confirmation instead of sending, while a
// resolvable variable does not.
func TestUndefinedVariableBlocksSend(t *testing.T) {
	a, ps := openTestProject(t, "Vars")
	tab := openRequestTabs(t, ps, "Alpha")[0]

	tab.URL = "https://api.example.com/{{missing}}"
	a.trySend(tab)
	if a.sendGuard == nil {
		t.Fatal("undefined variable should open the send guard")
	}
	if tab.Sending {
		t.Fatal("must not send while undefined variables are pending")
	}
	a.sendGuard = nil

	tab.URL = "{{baseUrl}}/users" // baseUrl is seeded in project + dev env
	if got := a.undefinedFor(tab); len(got) != 0 {
		t.Fatalf("baseUrl should resolve, undefined=%v", got)
	}
}

// TestSaveControlSavesRequest verifies the manual save entry (§8): clicking Save
// clears the dirty flag.
func TestSaveControlSavesRequest(t *testing.T) {
	a, ps := openTestProject(t, "Save")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = "http://saved.local/x"
	tab.Dirty = true
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if err := tt.Click("Save ⌘S"); err != nil {
		t.Fatalf("click Save: %v", err)
	}
	if tab.Dirty {
		t.Fatalf("Save should clear the dirty flag; status=%q", a.status)
	}
}

func TestUndefinedTemplatesCore(t *testing.T) {
	vars := map[string]string{"baseUrl": "http://x"}
	got := core.UndefinedTemplates("{{baseUrl}}/{{missing}}/{{missing}}", vars, nil)
	if len(got) != 1 || got[0] != "missing" {
		t.Fatalf("UndefinedTemplates = %v, want [missing]", got)
	}
}

func TestKVTextRoundTrip(t *testing.T) {
	rows := []*KVRow{
		{ID: nextRowID(), Enabled: true, Name: "page", Value: "2"},
		{ID: nextRowID(), Enabled: false, Name: "limit", Value: "20"},
		newBlankRow(),
	}
	txt := rowsToText(rows, kvQuery)
	if txt != "page = 2\n# limit = 20" {
		t.Fatalf("rowsToText query = %q", txt)
	}
	got, err := textToRows(txt, kvQuery)
	if err != nil {
		t.Fatal(err)
	}
	// two real rows + ghost
	if len(got) != 3 {
		t.Fatalf("parsed rows = %d, want 3", len(got))
	}
	if got[0].Name != "page" || !got[0].Enabled {
		t.Fatalf("row0 = %+v", got[0])
	}
	if got[1].Name != "limit" || got[1].Enabled {
		t.Fatalf("row1 = %+v", got[1])
	}

	if _, err := textToRows("no-separator-here", kvQuery); err == nil {
		t.Fatal("a line without a separator must be an error")
	}
}

func TestResponseEmptyStateSingleMessage(t *testing.T) {
	a, ps := openTestProject(t, "Empty")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.Resp = nil
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if !tt.HasText("Send a request to see its response here") {
		t.Fatalf("empty state missing; texts=%v", tt.Texts())
	}
	if tt.HasText("No response yet") {
		t.Fatalf("duplicate 'No response yet' should be gone; texts=%v", tt.Texts())
	}
}

func TestAutoHeadersShown(t *testing.T) {
	a, ps := openTestProject(t, "AutoHdr")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = "https://api.example.com/users"
	tab.Section = 2 // Headers
	tab.BodyText = "hello"
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	for _, want := range []string{"Host", "Accept-Encoding", "Content-Length"} {
		if !tt.HasText(want) {
			t.Fatalf("auto header %q missing; texts=%v", want, tt.Texts())
		}
	}
}

func TestHistoryStore(t *testing.T) {
	s := &historyStore{dir: t.TempDir()}
	key := "proj-abc"
	if err := s.append(key, historyRecord{Ts: time.Now().Add(-time.Hour), Method: "GET", URL: "a", Status: 200}); err != nil {
		t.Fatal(err)
	}
	if err := s.append(key, historyRecord{Ts: time.Now(), Method: "POST", URL: "b", Status: 201}); err != nil {
		t.Fatal(err)
	}
	recs := s.load(key)
	if len(recs) != 2 || recs[0].URL != "b" {
		t.Fatalf("load order wrong: %+v", recs)
	}
	s.remove(key, recs[0].Ts)
	if recs = s.load(key); len(recs) != 1 || recs[0].URL != "a" {
		t.Fatalf("remove wrong: %+v", recs)
	}
	if err := s.clear(key); err != nil {
		t.Fatal(err)
	}
	if recs = s.load(key); len(recs) != 0 {
		t.Fatalf("clear failed: %+v", recs)
	}
}

func TestRestoreHistory(t *testing.T) {
	a, ps := openTestProject(t, "Hist")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	ps.openRequest(e)
	tab := ps.activeTab()
	a.historyView = true
	a.restoreHistory(historyRecord{Method: "POST", URL: "http://x/y", RequestID: e.Request.ID})
	if tab.URL != "http://x/y" || tab.Method != "POST" || !tab.Dirty {
		t.Fatalf("restore did not fill tab: url=%q method=%q dirty=%v", tab.URL, tab.Method, tab.Dirty)
	}
	if a.historyView {
		t.Fatal("restoring should switch back to the request view")
	}
}
