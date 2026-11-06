package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestVarStripShowsResolvedValueAndSource verifies the URL variable strip
// (§5/D7): a resolvable {{baseUrl}} renders as an inline chip with its resolved
// value and the layer that supplies it. The seeded dev environment overrides
// the project-level global, so the tag must name the environment.
func TestVarStripShowsResolvedValueAndSource(t *testing.T) {
	a, ps := openTestProject(t, "VarsStage4")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = "{{baseUrl}}/users"

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if !tt.HasText("{{baseUrl}} = http://127.0.0.1:4010") {
		t.Fatalf("resolved chip missing from the URL strip; texts=%v", tt.Texts())
	}
	if !tt.HasText("env dev") {
		t.Fatalf("environment source tag missing; texts=%v", tt.Texts())
	}
}

// TestVarStripUndefinedChipOpensEnvironment verifies that an unresolved name is
// a clickable red chip whose click opens the selected environment for editing.
func TestVarStripUndefinedChipOpensEnvironment(t *testing.T) {
	a, ps := openTestProject(t, "VarsStage4")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = "{{missing}}/users"

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if !tt.HasText("{{missing}} ?") {
		t.Fatalf("undefined chip missing from the URL strip; texts=%v", tt.Texts())
	}
	if err := tt.Click("{{missing}} ?"); err != nil {
		t.Fatalf("click undefined chip: %v", err)
	}
	if ps.envUI == nil || !ps.envUI.open || ps.envUI.mode != "edit" {
		t.Fatalf("undefined chip should open the environment editor, got %+v", ps.envUI)
	}
}

// TestVarStripUndefinedChipWithoutEnvStartsNew verifies the (none) case: with no
// environment selected there is nothing to edit, so the chip starts a new
// environment draft instead.
func TestVarStripUndefinedChipWithoutEnvStartsNew(t *testing.T) {
	a, ps := openTestProject(t, "VarsStage4")
	ps.envSel = "(none)"
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.URL = "{{missing}}"

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if err := tt.Click("{{missing}} ?"); err != nil {
		t.Fatalf("click undefined chip: %v", err)
	}
	if ps.envUI == nil || ps.envUI.mode != "new" {
		t.Fatalf("undefined chip without an environment should start a new draft, got %+v", ps.envUI)
	}
}

// TestVarChipSourceFollowsMergeOrder verifies the chip's source label matches
// the value the request will actually use: core.MergeVars applies project,
// environment, folder then request layers, so the request layer wins and the
// label must say so rather than claiming the environment.
func TestVarChipSourceFollowsMergeOrder(t *testing.T) {
	a, ps := openTestProject(t, "VarsStage4")
	tab := openRequestTabs(t, ps, "Alpha")[0]
	tab.Entry.Request.Variables = map[string]string{"baseUrl": "http://from-request.local"}
	tab.URL = "{{baseUrl}}"

	chips := a.varChipsFor(tab)
	if len(chips) != 1 {
		t.Fatalf("want 1 chip, got %d: %+v", len(chips), chips)
	}
	chip := chips[0]
	if !chip.Defined || chip.Value != "http://from-request.local" {
		t.Fatalf("chip should show the winning request value, got %+v", chip)
	}
	if chip.Source.kind != varSourceRequest || chip.Source.label() != "request" {
		t.Fatalf("chip source = %+v, want request", chip.Source)
	}
}
