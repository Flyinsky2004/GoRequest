package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// TestViewSmoke exercises the real view without a window: empty state, opening
// a project, the settings sheet, and a narrow window. It checks that the layout
// builds and the navigation/settings entry points work end to end.
func TestViewSmoke(t *testing.T) {
	a := newTestApp(t)
	tt := ui.NewTester(a.View, 1200, 820)

	if !tt.HasText("GoRequest") {
		t.Fatalf("navigation title missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("Open a project") {
		t.Fatalf("welcome empty state missing; texts=%v", tt.Texts())
	}

	dir := writeProject(t, "Example Project")
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Example Project") {
		t.Fatalf("project name not shown after open; texts=%v", tt.Texts())
	}

	if err := tt.Click("Settings"); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	if !tt.HasText("Appearance") {
		t.Fatalf("settings sheet did not open; texts=%v", tt.Texts())
	}
	if err := tt.Click("Done"); err != nil {
		t.Fatalf("close settings: %v", err)
	}
	if a.showSettings {
		t.Fatal("settings should be closed after Done")
	}

	// A narrow window must still build without panicking.
	tt.SetSize(760, 560)
	tt.Frame()
}

// Exercise the merged project workspace with structured request rows and the
// separate response views, including saving after switching projects.
func TestWorkspaceFormAndResponseAcrossProjects(t *testing.T) {
	a := newTestApp(t)
	dir := writeProject(t, "One")
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	ps := a.current
	entry, err := ps.newRequest()
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.QueryRows = rowsFromKV([]core.KV{
		{Name: "tag", Value: "a=b&c:d"},
		{Name: "tag", Value: "disabled", Enabled: core.BoolPtr(false)},
	})
	tab.HeaderRows = rowsFromKV([]core.KV{{Name: "Accept", Value: "application/json"}})
	tab.Dirty = true
	tab.Resp = &core.ExecResult{
		Status: 200, Proto: "HTTP/1.1", Body: []byte(`{"ok":true}`),
		Pretty:  "{\n  \"ok\": true\n}",
		Headers: [][2]string{{"Content-Type", "application/json"}},
	}
	tt := ui.NewTester(a.View, 1200, 820)
	if !tt.HasText("Add row") || !tt.HasText("Pretty") {
		t.Fatalf("form or response controls missing: %v", tt.Texts())
	}
	if got := a.responseEditorFor(tab).Buffer.String(); got != tab.Resp.Pretty {
		t.Fatalf("body should contain only JSON: %q", got)
	}
	tab.RespSection = 1
	tt.Frame()
	if !tt.HasText("Content-Type") || !tt.HasText("application/json") {
		t.Fatalf("response headers missing: %v", tt.Texts())
	}
	if err := a.openProject(writeProject(t, "Two")); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := a.openProject(dir); err != nil {
		t.Fatal(err)
	}
	if a.current.activeTab() != tab || !tab.Dirty || tab.RespSection != 1 {
		t.Fatal("project switching lost form or response state")
	}
	a.saveActive()
	if tab.Dirty {
		t.Fatalf("saving merged form failed: %s", a.Status())
	}
	ps.tabs = nil
	ps.openRequest(entry)
	reopened := ps.activeTab()
	query, err := kvFromRows(reopened.QueryRows)
	if err != nil || len(query) != 2 || query[0].Value != "a=b&c:d" || query[1].IsEnabled() {
		t.Fatalf("saved form lost values, duplicates or disabled state: %+v, %v", query, err)
	}
	tt.SetSize(760, 560)
	for _, section := range []int{0, 1} {
		reopened.Section = section
		tt.Frame()
	}
}
