package desktop

import (
	"testing"

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
