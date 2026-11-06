package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// writeProjectWithoutEnvs creates a project then removes its environments so a
// project genuinely has no environments to test the from-scratch path.
func writeProjectWithoutEnvs(t *testing.T, name string) string {
	t.Helper()
	dir := writeProject(t, name)
	if err := os.RemoveAll(filepath.Join(dir, ".gorequest", "environments")); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestEnvEditorNewAvailableButEditDisabledWithoutEnvironments(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProjectWithoutEnvs(t, "NoEnv")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	if ps.envSel != "(none)" || ps.canEditEnv() {
		t.Fatalf("project without envs should start at (none), not editable: %q %v", ps.envSel, ps.canEditEnv())
	}

	// Edit must be a no-op when nothing is selected.
	a.openEditEnvironment()
	if ps.envUI != nil {
		t.Fatal("Edit Env must not open when (none) is selected")
	}

	// New must work with no environments at all.
	a.openNewEnvironment()
	if ps.envUI == nil || !ps.envUI.open || ps.envUI.mode != "new" {
		t.Fatalf("New Env should open a create draft: %+v", ps.envUI)
	}

	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	// The env selector is present in the top bar; New/Edit Env live in its menu.
	if !tt.HasText("(none)") {
		t.Fatalf("env selector missing from header: %v", tt.Texts())
	}
	if !tt.HasText("Select an environment to edit") {
		t.Fatalf("(none) reason not shown: %v", tt.Texts())
	}
}

func TestEnvEditorEditLoadsAndSavesPreservingSelection(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "EditEnv")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	ps.envSel = "dev"

	if err := ps.startEditEnvironment(); err != nil {
		t.Fatal(err)
	}
	e := ps.envUI
	if e.mode != "edit" || !e.idLocked || e.idText != "dev" {
		t.Fatalf("edit draft not bound to dev: %+v", e)
	}

	// Change the value of baseUrl and add a variable.
	found := false
	for _, r := range e.varRows {
		if r.Key == "baseUrl" {
			r.Value = "http://edited.local"
			found = true
		}
	}
	if !found {
		t.Fatal("baseUrl row not loaded from environment")
	}
	e.varRows = append(e.varRows, &envVarRow{ID: nextRowID(), Key: "token_url", Value: "{{baseUrl}}/auth"})

	a.saveEnvironmentDraft(ps)
	if e.open {
		t.Fatalf("save should close the panel, error=%q", e.err)
	}
	if ps.envSel != "dev" {
		t.Fatalf("edit must keep the current selection, got %q", ps.envSel)
	}
	if got := ps.project.Environments["dev"].Variables["baseUrl"]; got != "http://edited.local" {
		t.Fatalf("in-memory env not updated: %q", got)
	}
	p2, err := core.OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := p2.Environments["dev"].Variables["token_url"]; got != "{{baseUrl}}/auth" {
		t.Fatalf("edited variable not persisted: %q", got)
	}
}

func TestEnvEditorCreateAutoSelectsAndKeepsDirtyTabs(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "CreateEnv")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	entry, err := ps.newRequest()
	if err != nil {
		t.Fatal(err)
	}
	dirtyTab := ps.activeTab()
	dirtyTab.Dirty = true
	tabsBefore := len(ps.tabs)

	a.openNewEnvironment()
	e := ps.envUI
	e.idText = "staging"
	e.varRows = []*envVarRow{
		{ID: nextRowID(), Key: "baseUrl", Value: "http://staging.local"},
		{ID: nextRowID()}, // trailing blank
	}
	a.saveEnvironmentDraft(ps)
	if e.err != "" || e.open {
		t.Fatalf("create failed: err=%q open=%v", e.err, e.open)
	}
	if ps.envSel != "staging" {
		t.Fatalf("new env should be auto-selected, got %q", ps.envSel)
	}
	if _, ok := ps.project.Environments["staging"]; !ok {
		t.Fatal("new environment not in memory")
	}
	if strings.Join(ps.project.EnvOrder, ",") != "dev,staging" {
		t.Fatalf("EnvOrder = %v", ps.project.EnvOrder)
	}
	// No reload: the dirty tab and its entry must be intact.
	if len(ps.tabs) != tabsBefore || ps.activeTab() != dirtyTab || !dirtyTab.Dirty {
		t.Fatal("saving an environment must not drop dirty request tabs")
	}
	if ps.activeTab().Entry != entry {
		t.Fatal("request entry lost after environment save")
	}
}

func TestEnvEditorValidationKeepsInputAndPanel(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "Validate")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps

	// Duplicate id.
	a.openNewEnvironment()
	e := ps.envUI
	e.idText = "dev"
	a.saveEnvironmentDraft(ps)
	if e.err == "" || !e.open {
		t.Fatalf("duplicate id should keep panel with error, err=%q open=%v", e.err, e.open)
	}
	if e.idText != "dev" {
		t.Fatal("input must be preserved on validation failure")
	}

	// Duplicate variable key.
	e.idText = "fresh"
	e.varRows = []*envVarRow{
		{ID: nextRowID(), Key: "a", Value: "1"},
		{ID: nextRowID(), Key: "a", Value: "2"},
	}
	a.saveEnvironmentDraft(ps)
	if e.err == "" || !strings.Contains(e.err, "duplicate variable key") {
		t.Fatalf("duplicate key error = %q", e.err)
	}
	if !e.open {
		t.Fatal("panel must stay open on duplicate key")
	}

	// Value without key.
	e.varRows = []*envVarRow{{ID: nextRowID(), Key: "", Value: "orphan"}}
	a.saveEnvironmentDraft(ps)
	if e.err == "" || !strings.Contains(e.err, "no key") {
		t.Fatalf("value-without-key error = %q", e.err)
	}

	// Nothing was written for any failed attempt.
	if _, ok := ps.project.Environments["fresh"]; ok {
		t.Fatal("failed create must not register an environment")
	}
}

func TestEnvEditorCancelAndDiscardDoNotWrite(t *testing.T) {
	m, _ := newTestManager(t)
	dir := writeProject(t, "Cancel")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	before := len(ps.project.Environments)

	tt := ui.NewTester(a.View, 1200, 820)
	a.openNewEnvironment()
	ps.envUI.idText = "draft"
	tt.Frame()
	if !tt.HasText("New Environment") {
		t.Fatalf("editor modal missing: %v", tt.Texts())
	}
	// Cancel on a dirty draft asks before discarding.
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("click Cancel: %v", err)
	}
	if !ps.envUI.confirmDiscard || !tt.HasText("Discard unsaved changes to this environment?") {
		t.Fatalf("dirty cancel should ask to discard; texts=%v", tt.Texts())
	}
	if err := tt.Click("Discard"); err != nil {
		t.Fatalf("click Discard: %v", err)
	}
	if ps.envUI.open {
		t.Fatal("discard should close the panel")
	}
	if len(ps.project.Environments) != before {
		t.Fatal("cancel/discard must not change the environment model")
	}
	if _, err := os.Stat(filepath.Join(dir, ".gorequest", "environments", "draft.yaml")); !os.IsNotExist(err) {
		t.Fatal("cancel/discard must not write a file")
	}
}

func TestEnvEditorProjectIsolation(t *testing.T) {
	m, _ := newTestManager(t)
	dirA := writeProject(t, "Alpha")
	dirB := writeProject(t, "Beta")
	if err := m.openProject(dirA); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	psA := a.ps
	a.openNewEnvironment()
	psA.envUI.idText = "alpha-only"
	psA.envUI.varRows = []*envVarRow{{ID: nextRowID(), Key: "k", Value: "v"}}

	// Switching to another project must not show A's draft or touch B.
	if err := m.openProject(dirB); err != nil {
		t.Fatal(err)
	}
	b := m.workspaces[1]
	psB := b.ps
	if psB == psA {
		t.Fatal("switch should change current project")
	}
	if psB.envUI != nil {
		t.Fatal("B must not inherit A's environment draft")
	}
	tt := ui.NewTester(b.View, 1200, 820)
	tt.Frame()
	if tt.HasText("New Environment") {
		t.Fatal("A's environment draft must not render for B")
	}

	// Saving A's draft writes only to A and never to B.
	a.saveEnvironmentDraft(psA)
	if _, ok := psA.project.Environments["alpha-only"]; !ok {
		t.Fatal("A's draft not saved to A")
	}
	if _, ok := psB.project.Environments["alpha-only"]; ok {
		t.Fatal("A's draft leaked into B")
	}
	if _, err := os.Stat(filepath.Join(dirB, ".gorequest", "environments", "alpha-only.yaml")); !os.IsNotExist(err) {
		t.Fatal("A's environment file must not be written under B")
	}

	// Returning to A reactivates A's state.
	if err := m.openProject(dirA); err != nil {
		t.Fatal(err)
	}
	if m.findWorkspace(psA.root) != a {
		t.Fatal("reopening A should reactivate its state")
	}
}

func TestEnvEditorBuildPreservesValuesAndSecrets(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "Build")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	ps := a.ps
	e := &environmentEditor{
		mode:   "new",
		idText: "exact",
		varRows: []*envVarRow{
			{ID: nextRowID(), Key: "url", Value: "http://h:8080/p?x=1&y=2"},
			{ID: nextRowID(), Key: "empty", Value: ""},
			{ID: nextRowID(), Key: "spaced", Value: "  keep  "},
			{ID: nextRowID(), Key: "tpl", Value: "{{baseUrl}}/v"},
			{ID: nextRowID()}, // blank ignored
		},
		secretRows: []*envSecretRow{{ID: nextRowID(), Name: "token"}},
	}
	env, err := e.build(ps)
	if err != nil {
		t.Fatal(err)
	}
	if env.Variables["url"] != "http://h:8080/p?x=1&y=2" || env.Variables["spaced"] != "  keep  " {
		t.Fatalf("values not preserved verbatim: %+v", env.Variables)
	}
	if v, ok := env.Variables["empty"]; !ok || v != "" {
		t.Fatal("empty value not preserved")
	}
	if len(env.Secrets) != 1 || env.Secrets[0] != "token" {
		t.Fatalf("secrets = %v", env.Secrets)
	}
}

func TestEnvEditorViewSmoke(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "Smoke")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	a.openNewEnvironment()
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	for _, want := range []string{"New Environment", "Environment ID", "Variables", "Secret names", "Add variable", "Add secret name", "Save", "Cancel"} {
		if !tt.HasText(want) {
			t.Errorf("environment editor missing %q; texts=%v", want, tt.Texts())
		}
	}
	// A narrow window must still build without panicking.
	tt.SetSize(760, 560)
	tt.Frame()
}
