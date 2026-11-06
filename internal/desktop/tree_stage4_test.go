package desktop

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// These tests drive the real rendered sidebar (§3): the row context menus, the
// in-place rename input, undoable deletes and dragging a request into a folder.

// TestTreeContextMenusThroughView proves each row kind offers its §3 actions
// through a right-click menu drawn by the real view.
func TestTreeContextMenusThroughView(t *testing.T) {
	a, ps := openTestProject(t, "Menus")
	if _, err := ps.project.NewRequest("", "Alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.project.NewFolder("", "Group"); err != nil {
		t.Fatal(err)
	}
	if _, err := ps.project.NewDoc("Notes"); err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	// Request row.
	if err := tt.RightClick("Alpha"); err != nil {
		t.Fatalf("right-click request: %v", err)
	}
	for _, want := range []string{"Open", "New Request Here", "Rename", "Duplicate Request", "Move To…", "Delete"} {
		if !tt.HasText(want) {
			t.Fatalf("request menu missing %q; texts=%v", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Folder row.
	if err := tt.RightClick("Group"); err != nil {
		t.Fatalf("right-click folder: %v", err)
	}
	for _, want := range []string{"Open", "New Request Here", "Rename", "Move To…", "Delete"} {
		if !tt.HasText(want) {
			t.Fatalf("folder menu missing %q; texts=%v", want, tt.Texts())
		}
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()

	// Document row.
	if err := tt.RightClick("notes.md"); err != nil {
		t.Fatalf("right-click document: %v", err)
	}
	for _, want := range []string{"Open", "Rename", "Delete"} {
		if !tt.HasText(want) {
			t.Fatalf("document menu missing %q; texts=%v", want, tt.Texts())
		}
	}
}

// TestRenameRequestInPlace proves F2-style renaming opens a real text input and
// Enter commits the new name to the file while keeping the stable ID.
func TestRenameRequestInPlace(t *testing.T) {
	a, ps := openTestProject(t, "Rename")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	ps.sel = "req:" + e.Request.ID
	oldID := e.Request.ID

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	a.beginRenameTreeRow()
	tt.Frame()
	if ps.renaming != "req:"+oldID {
		t.Fatalf("rename did not start on the selected row: %q", ps.renaming)
	}
	if active, _ := tt.TextInputActive(); !active {
		t.Fatal("the rename input should have the keyboard focus")
	}
	if !tt.Focused("Rename") {
		t.Fatalf("the rename input is not focused; texts=%v", tt.Texts())
	}

	// Select the old name, type the new one, and commit with Enter.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("Renamed")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	if e.Request.Name != "Renamed" {
		t.Fatalf("name = %q, want Renamed (status %q)", e.Request.Name, a.status)
	}
	if e.Request.ID != oldID {
		t.Fatalf("rename changed the stable ID: %q", e.Request.ID)
	}
	if ps.renaming != "" {
		t.Fatalf("rename input still open: %q", ps.renaming)
	}
	data, _, readErr := ps.project.Store.Read(e.RelPath)
	if readErr != nil {
		t.Fatalf("read renamed request: %v", readErr)
	}
	if !strings.Contains(string(data), "name: Renamed") {
		t.Fatalf("renamed name not persisted: %s", data)
	}
	tt.Frame()
	if !tt.HasText("Renamed") {
		t.Fatalf("renamed row not shown; texts=%v", tt.Texts())
	}
}

// TestRenameFolderInPlace proves renaming a folder moves its directory and
// updates the display name without losing its requests.
func TestRenameFolderInPlace(t *testing.T) {
	a, ps := openTestProject(t, "RenameFolder")
	node, err := ps.project.NewFolder("", "Old")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ps.project.NewRequest("old", "Ping"); err != nil {
		t.Fatal(err)
	}
	ps.expanded[node.RelPath] = true

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	ps.sel = "folder:" + node.RelPath
	a.beginRenameTreeRow()
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("New Name")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()

	moved := findFolderInTree(ps, "collections/new-name")
	if moved == nil || moved.Name != "New Name" {
		t.Fatalf("folder not renamed; tree=%+v", ps.project.Collections)
	}
	if ps.sel != "folder:collections/new-name" {
		t.Fatalf("selection after rename = %q", ps.sel)
	}
	if !ps.project.Store.Exists("collections/new-name/collection.yaml") {
		t.Fatal("collection.yaml not written at the new path")
	}
	if len(moved.Requests) != 1 || moved.Requests[0].RelPath != "collections/new-name/ping.request.yaml" {
		t.Fatalf("requests not moved with the folder: %+v", moved.Requests)
	}
}

// TestDeleteRequestUndo proves a deletion happens immediately, advertises undo
// in the status line, and that the pending undo restores the file and entry.
func TestDeleteRequestUndo(t *testing.T) {
	a, ps := openTestProject(t, "Delete")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	ps.sel = "req:" + e.Request.ID
	a.deleteTreeRow("req:" + e.Request.ID)

	if _, ok := ps.project.Requests[e.Request.ID]; ok {
		t.Fatal("request should be deleted immediately")
	}
	if ps.project.Store.Exists(e.RelPath) {
		t.Fatal("request file should be removed")
	}
	if a.undo == nil {
		t.Fatal("delete should offer undo")
	}
	if !strings.Contains(a.status, "Alpha") {
		t.Fatalf("toast should name the deleted request: %q", a.status)
	}
	if !a.undoLast() {
		t.Fatal("undo should be available immediately")
	}
	if _, ok := ps.project.Requests[e.Request.ID]; !ok {
		t.Fatal("undo should restore the request entry")
	}
	if !ps.project.Store.Exists(e.RelPath) {
		t.Fatal("undo should restore the request file")
	}
}

// TestDeleteFolderUndo proves folder deletion reports how many requests it
// held and that undo brings the whole subtree back.
func TestDeleteFolderUndo(t *testing.T) {
	a, ps := openTestProject(t, "DeleteFolder")
	node, err := ps.project.NewFolder("", "Group")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"One", "Two"} {
		if _, err := ps.project.NewRequest("group", name); err != nil {
			t.Fatal(err)
		}
	}
	a.deleteTreeRow("folder:" + node.RelPath)

	if findFolderInTree(ps, "collections/group") != nil {
		t.Fatal("folder should be deleted immediately")
	}
	if a.undo == nil || !strings.Contains(a.status, "2 Requests") {
		t.Fatalf("toast should name the request count: %q", a.status)
	}
	if !a.undoLast() {
		t.Fatal("undo should restore the deleted folder")
	}
	restored := findFolderInTree(ps, "collections/group")
	if restored == nil {
		t.Fatal("folder not restored in the tree")
	}
	if len(restored.Requests) != 2 {
		t.Fatalf("restored folder has %d requests, want 2", len(restored.Requests))
	}
}

// TestRenameDocInPlace proves renaming a document moves its file and keeps the
// row selection on the new path.
func TestRenameDocInPlace(t *testing.T) {
	a, ps := openTestProject(t, "DocRename")
	d, err := ps.project.NewDoc("Notes")
	if err != nil {
		t.Fatal(err)
	}
	oldRel := d.RelPath
	ps.sel = "doc:" + oldRel
	a.beginRenameTreeRow()
	ps.renameText = "Journal"
	a.commitRename()

	if d.RelPath != "docs/journal.md" {
		t.Fatalf("document path = %q, want docs/journal.md", d.RelPath)
	}
	if ps.project.Store.Exists(oldRel) {
		t.Fatal("old document file should be gone")
	}
	if !ps.project.Store.Exists("docs/journal.md") {
		t.Fatal("renamed document file missing")
	}
	if ps.sel != "doc:docs/journal.md" {
		t.Fatalf("selection = %q", ps.sel)
	}
}

// TestDeleteThroughContextMenu proves choosing Delete in a row's menu runs the
// same undoable delete path as the shortcut-free API.
func TestDeleteThroughContextMenu(t *testing.T) {
	a, ps := openTestProject(t, "MenuDelete")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	if err := tt.RightClick("Alpha"); err != nil {
		t.Fatalf("right-click request: %v", err)
	}
	if err := tt.Click("Delete"); err != nil {
		t.Fatalf("choose Delete: %v", err)
	}
	tt.Frame()
	if _, ok := ps.project.Requests[e.Request.ID]; ok {
		t.Fatal("choosing Delete should remove the request")
	}
	if a.undo == nil {
		t.Fatal("Delete from the menu should offer undo")
	}
}

// TestDragRequestIntoFolder proves dragging a request row onto a folder row
// moves the request file into that folder.
func TestDragRequestIntoFolder(t *testing.T) {
	a, ps := openTestProject(t, "Drag")
	e, err := ps.project.NewRequest("", "Alpha")
	if err != nil {
		t.Fatal(err)
	}
	node, err := ps.project.NewFolder("", "Group")
	if err != nil {
		t.Fatal(err)
	}
	ps.expanded[node.RelPath] = true

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()
	src, ok := tt.Find("Alpha")
	if !ok {
		t.Fatalf("request row not found; texts=%v", tt.Texts())
	}
	dst, ok := tt.Find("Group")
	if !ok {
		t.Fatalf("folder row not found; texts=%v", tt.Texts())
	}
	sx, sy := src.X+10, src.Y+src.H/2
	dx, dy := dst.X+10, dst.Y+dst.H/2
	tt.Press(sx, sy)
	for i := 1; i <= 6; i++ {
		f := float32(i) / 6
		tt.Move(sx+(dx-sx)*f, sy+(dy-sy)*f)
	}
	tt.Release(dx, dy)
	tt.Frame()

	if got := filepath.ToSlash(filepath.Dir(e.RelPath)); got != "collections/group" {
		t.Fatalf("dragged request lives in %q, want collections/group", got)
	}
}
