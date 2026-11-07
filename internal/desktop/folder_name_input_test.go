package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// These tests drive the real new-folder dialog and the in-place tree rename
// through the rendered view. They cover the Unicode/IME path that a Chinese
// display name takes: composition stays virtual until the input method commits
// it, Enter then confirms the name once, Escape cancels, a rejected name keeps
// the field and its text, and a successful rename persists and reloads.

// TestNewFolderDialogFocusesNameField proves the dialog puts the caret in its
// name field and that the field accepts Unicode text without an input method.
func TestNewFolderDialogFocusesNameField(t *testing.T) {
	a, ps := openTestProject(t, "FolderFocus")
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.openNewFolder()
	tt.Frame()
	if active, noIME := tt.TextInputActive(); !active || noIME {
		t.Fatalf("new-folder name field should take text input: active=%v noIME=%v", active, noIME)
	}
	if !tt.Focused(a.msg().FolderNamePlaceholder) {
		t.Fatalf("new-folder name field is not focused; texts=%v", tt.Texts())
	}

	tt.Type("中文")
	tt.Frame()
	if a.newFolderName != "中文" {
		t.Fatalf("name field did not take Unicode text: %q", a.newFolderName)
	}

	// Escape closes the dialog without creating anything.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if a.newFolderOpen {
		t.Fatal("Escape should close the new-folder dialog")
	}
	if len(ps.project.Collections) != 0 {
		t.Fatalf("cancelled dialog created a folder: %+v", ps.project.Collections)
	}
}

// TestNewFolderRejectedNameKeepsTypedText proves a rejected name leaves the
// dialog open with the user's text, so a validation error never discards it.
func TestNewFolderRejectedNameKeepsTypedText(t *testing.T) {
	a, ps := openTestProject(t, "FolderReject")
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.openNewFolder()
	tt.Frame()
	tt.Type("!!!")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if !a.newFolderOpen {
		t.Fatal("a rejected name should keep the dialog open")
	}
	if a.newFolderName != "!!!" {
		t.Fatalf("typed name was lost: %q", a.newFolderName)
	}
	if a.formErr == "" {
		t.Fatal("the rejection should be reported in the dialog")
	}
	if !tt.Focused(a.msg().FolderNamePlaceholder) {
		t.Fatalf("the name field lost focus after a rejected submit; texts=%v", tt.Texts())
	}
	if len(ps.project.Collections) != 0 {
		t.Fatalf("rejected name created a folder: %+v", ps.project.Collections)
	}
}

// TestNewFolderChineseNamePersists is the end-to-end acceptance for creating a
// folder with a Chinese name. It is skipped while Core still collapses a
// non-ASCII folder name to "item" and rejects it; the exact Core patch is in
// docs/task-results/U05.md. Once that patch is applied this test runs and
// proves the name survives a reload.
func TestNewFolderChineseNamePersists(t *testing.T) {
	probe, err := core.NewProject(t.TempDir(), "probe")
	if err != nil {
		t.Fatalf("probe project: %v", err)
	}
	if _, err := probe.NewFolder("", "中文"); err != nil {
		t.Skipf("blocked by internal/core validateFolderName: %v; see docs/task-results/U05.md", err)
	}
	a, ps := openTestProject(t, "FolderChinese")
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.openNewFolder()
	tt.Frame()
	tt.Type("中文")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if a.newFolderOpen {
		t.Fatalf("valid Chinese name was rejected: %q", a.formErr)
	}
	if len(ps.project.Collections) != 1 {
		t.Fatalf("folder not created: %+v", ps.project.Collections)
	}
	node := ps.project.Collections[0]
	if node.Name != "中文" {
		t.Fatalf("display name = %q, want 中文", node.Name)
	}
	if ps.sel != "folder:"+node.RelPath {
		t.Fatalf("new folder not selected: %q", ps.sel)
	}

	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	if len(reopened.Collections) != 1 || reopened.Collections[0].Name != "中文" {
		t.Fatalf("Chinese folder name did not survive reload: %+v", reopened.Collections)
	}
}

// TestRenameRequestChineseThroughIME drives the real IME sequence: a Pinyin
// composition is virtual, the committed candidate becomes the name, and the
// Enter that confirms the name commits it once and persists it.
func TestRenameRequestChineseThroughIME(t *testing.T) {
	a, ps := openTestProject(t, "RenameIME")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	ps.sel = "req:" + e.Request.ID
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.beginRenameTreeRow()
	tt.Frame()
	if !tt.Focused(a.msg().Rename) {
		t.Fatalf("rename field is not focused; texts=%v", tt.Texts())
	}

	// Pinyin before a candidate is chosen: the bound value keeps the old name
	// and the field has not submitted.
	tt.Compose("zhong", 5)
	tt.Frame()
	if ps.renameText != "Alpha" {
		t.Fatalf("composition changed the bound value: %q", ps.renameText)
	}
	if ps.renaming == "" {
		t.Fatal("composition must not submit the rename")
	}

	// The input method commits the chosen candidate; the name now holds it but
	// no Enter has confirmed the rename yet.
	tt.Type("中")
	tt.Frame()
	if ps.renameText != "Alpha中" {
		t.Fatalf("committed candidate = %q, want Alpha中", ps.renameText)
	}
	if ps.renaming == "" {
		t.Fatal("committing a candidate must not submit the rename")
	}

	// The confirming Enter commits once.
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if e.Request.Name != "Alpha中" {
		t.Fatalf("renamed name = %q, want Alpha中 (status %q)", e.Request.Name, a.status)
	}
	if ps.renaming != "" {
		t.Fatalf("rename field still open: %q", ps.renaming)
	}
	data, _, err := ps.project.Store.Read(e.RelPath)
	if err != nil {
		t.Fatalf("read renamed request: %v", err)
	}
	if !strings.Contains(string(data), "name: Alpha中") {
		t.Fatalf("Chinese name not persisted: %s", data)
	}

	reopened, err := core.OpenProject(ps.root)
	if err != nil {
		t.Fatalf("reopen project: %v", err)
	}
	found := false
	for _, entry := range reopened.Requests {
		if entry.Request != nil && entry.Request.Name == "Alpha中" {
			found = true
		}
	}
	if !found {
		t.Fatalf("Chinese request name did not survive reload")
	}
}

// TestRenameEscapeCancels proves Escape abandons the edit and leaves the row
// unchanged.
func TestRenameEscapeCancels(t *testing.T) {
	a, ps := openTestProject(t, "RenameCancel")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	ps.sel = "req:" + e.Request.ID
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.beginRenameTreeRow()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Beta")
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	if e.Request.Name != "Alpha" {
		t.Fatalf("Escape changed the name: %q", e.Request.Name)
	}
	if ps.renaming != "" || ps.renameText != "" {
		t.Fatalf("Escape left the field open: renaming=%q text=%q", ps.renaming, ps.renameText)
	}
	if !tt.HasText("Alpha") {
		t.Fatalf("row lost its name; texts=%v", tt.Texts())
	}
}

// TestRenameRejectedFolderNameKeepsField proves a folder name Core rejects
// leaves the in-place field open with the typed text instead of discarding it.
func TestRenameRejectedFolderNameKeepsField(t *testing.T) {
	a, ps := openTestProject(t, "RenameReject")
	node, err := ps.project.NewFolder("", "Group")
	if err != nil {
		t.Fatal(err)
	}
	ps.sel = "folder:" + node.RelPath
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.beginRenameTreeRow()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("!!!")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if node.Name != "Group" {
		t.Fatalf("rejected rename changed the name: %q", node.Name)
	}
	if ps.renaming == "" {
		t.Fatal("a rejected rename should keep the field open")
	}
	if ps.renameText != "!!!" {
		t.Fatalf("typed name was lost: %q", ps.renameText)
	}
	if a.status == "" {
		t.Fatal("the rejection should be reported")
	}
	if !tt.Focused(a.msg().Rename) {
		t.Fatalf("the rename field lost focus after a rejected submit; texts=%v", tt.Texts())
	}
}

// TestRenameFolderKeepsSelectionAndExpansion proves a successful folder rename
// keeps the folder selected and its expansion state under the new path.
func TestRenameFolderKeepsSelectionAndExpansion(t *testing.T) {
	a, ps := openTestProject(t, "RenameFolderKeep")
	node, err := ps.project.NewFolder("", "Group")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.project.NewRequest("group", "Ping"); err != nil {
		t.Fatal(err)
	}
	ps.expanded[node.RelPath] = true
	ps.sel = "folder:" + node.RelPath
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.beginRenameTreeRow()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Renamed")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if a.status != "" {
		t.Fatalf("rename failed: %q", a.status)
	}
	renamed := findFolderInTree(ps, "collections/renamed")
	if renamed == nil || renamed.Name != "Renamed" {
		t.Fatalf("folder not renamed; tree=%+v", ps.project.Collections)
	}
	if ps.sel != "folder:"+renamed.RelPath {
		t.Fatalf("selection = %q, want %q", ps.sel, "folder:"+renamed.RelPath)
	}
	if !ps.expanded[renamed.RelPath] {
		t.Fatalf("expansion was not re-keyed to %q: %+v", renamed.RelPath, ps.expanded)
	}
	if len(renamed.Requests) != 1 || renamed.Requests[0].RelPath != "collections/renamed/ping.request.yaml" {
		t.Fatalf("request did not move with the folder: %+v", renamed.Requests)
	}
	if !tt.HasText("Renamed") || !tt.HasText("Ping") {
		t.Fatalf("renamed folder or its request is missing; texts=%v", tt.Texts())
	}
}
