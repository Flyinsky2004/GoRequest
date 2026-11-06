package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

// These tests cover FIX02: after a request or folder is moved, the sidebar tree
// must reflect the new location immediately (target contains the node, the old
// location no longer does, RelPath is correct) without reopening the project.
//
// They also guard the repaint contract: context-menu and drag moves run from a
// native menu callback / drop outside the frame that draws the tree, so the
// action must ask the window for a fresh frame. Without that request the model
// changes but the sidebar keeps showing the stale location until unrelated input
// repaints it — the reported "must close and reopen the project" symptom.

// containsEntry reports whether entries holds target (by pointer identity).
func containsEntry(entries []*core.RequestEntry, target *core.RequestEntry) bool {
	for _, e := range entries {
		if e == target {
			return true
		}
	}
	return false
}

// anyFolderContains reports whether any folder subtree holds target.
func anyFolderContains(nodes []*core.CollectionEntry, target *core.RequestEntry) bool {
	for _, n := range nodes {
		if containsEntry(n.Requests, target) {
			return true
		}
		if anyFolderContains(n.Children, target) {
			return true
		}
	}
	return false
}

// TestMoveRequestRefreshesTreeImmediately proves moving a request (the
// context-menu "Move To…" path, which calls moveRequestTo) updates the in-memory
// tree and requests exactly one repaint, so the sidebar refreshes in place.
func TestMoveRequestRefreshesTreeImmediately(t *testing.T) {
	a, ps := openTestProject(t, "MoveRequestRefresh")
	if _, err := ps.project.NewFolder("", "Archive"); err != nil {
		t.Fatal(err)
	}
	e, err := ps.project.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}

	fw := &fakeWindow{}
	a.win = fw
	before := fw.updates

	a.moveRequestTo(e, "archive")

	if got := filepath.ToSlash(e.RelPath); got != "collections/archive/ping.request.yaml" {
		t.Fatalf("moved request RelPath = %q, want collections/archive/ping.request.yaml", got)
	}
	target := findFolderInTree(ps, "collections/archive")
	if target == nil || !containsEntry(target.Requests, e) {
		t.Fatalf("target folder does not hold the moved request; tree=%+v", ps.project.Collections)
	}
	if anyFolderContains(ps.project.Collections, e) && !containsEntry(target.Requests, e) {
		t.Fatal("request still appears in a non-target folder")
	}
	for _, r := range ps.project.RootRequests() {
		if r == e {
			t.Fatal("moved request still appears at the collections root")
		}
	}
	if ps.sel != "req:"+e.Request.ID {
		t.Fatalf("selection after move = %q, want req:%s", ps.sel, e.Request.ID)
	}
	if !ps.expanded["collections/archive"] {
		t.Fatal("target folder chain should be expanded so the moved row is visible")
	}
	if fw.updates != before+1 {
		t.Fatalf("move requested %d repaints, want exactly 1 (before=%d after=%d)", fw.updates-before, before, fw.updates)
	}
}

// TestMoveRequestByIDRefreshesTreeImmediately covers the drag-and-drop path
// (moveRequestByID), which must behave like the context-menu move.
func TestMoveRequestByIDRefreshesTreeImmediately(t *testing.T) {
	a, ps := openTestProject(t, "MoveByIDRefresh")
	if _, err := ps.project.NewFolder("", "Archive"); err != nil {
		t.Fatal(err)
	}
	e, err := ps.project.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}

	fw := &fakeWindow{}
	a.win = fw
	before := fw.updates

	a.moveRequestByID(e.Request.ID, "archive")

	if got := filepath.ToSlash(filepath.Dir(e.RelPath)); got != "collections/archive" {
		t.Fatalf("dragged request lives in %q, want collections/archive", got)
	}
	target := findFolderInTree(ps, "collections/archive")
	if target == nil || !containsEntry(target.Requests, e) {
		t.Fatalf("target folder does not hold the dragged request; tree=%+v", ps.project.Collections)
	}
	if fw.updates != before+1 {
		t.Fatalf("moveRequestByID requested %d repaints, want exactly 1", fw.updates-before)
	}
}

// TestMoveFolderRefreshesTreeImmediately proves moving a folder reparents it and
// its requests in memory and requests a repaint, with no reopen required.
func TestMoveFolderRefreshesTreeImmediately(t *testing.T) {
	a, ps := openTestProject(t, "MoveFolderRefresh")
	if _, err := ps.project.NewFolder("", "Dest"); err != nil {
		t.Fatal(err)
	}
	group, err := ps.project.NewFolder("", "Group")
	if err != nil {
		t.Fatal(err)
	}
	req, err := ps.project.NewRequest("group", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	ps.expanded[group.RelPath] = true

	fw := &fakeWindow{}
	a.win = fw
	before := fw.updates

	a.moveFolderTo(group, "dest")

	if findFolderInTree(ps, "collections/group") != nil {
		t.Fatal("folder still present at its old location after move")
	}
	moved := findFolderInTree(ps, "collections/dest/group")
	if moved == nil {
		t.Fatalf("folder not found at collections/dest/group; tree=%+v", ps.project.Collections)
	}
	if got := filepath.ToSlash(req.RelPath); got != "collections/dest/group/ping.request.yaml" {
		t.Fatalf("request inside moved folder has RelPath %q, want collections/dest/group/ping.request.yaml", got)
	}
	if len(moved.Requests) != 1 || moved.Requests[0] != req {
		t.Fatalf("moved folder does not hold its request; requests=%+v", moved.Requests)
	}
	if ps.sel != "folder:collections/dest/group" {
		t.Fatalf("selection after folder move = %q", ps.sel)
	}
	if !ps.expanded["collections/dest/group"] {
		t.Fatal("moved folder should be expanded and selected")
	}
	if fw.updates != before+1 {
		t.Fatalf("folder move requested %d repaints, want exactly 1", fw.updates-before)
	}
}

// TestMoveRequestRefreshesRenderedTree proves the rendered sidebar shows the
// request under its new folder and no longer at the root after a move, with no
// project reopen — the exact user-visible behaviour FIX02 asks for.
func TestMoveRequestRefreshesRenderedTree(t *testing.T) {
	a, ps := openTestProject(t, "MoveRenderRefresh")
	if _, err := ps.project.NewFolder("", "Archive"); err != nil {
		t.Fatal(err)
	}
	e, err := ps.project.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}

	tt := ui.NewTester(a.View, 1100, 820)
	tt.Frame()

	a.moveRequestTo(e, "archive")
	tt.Frame()

	if !tt.HasText("Ping") {
		t.Fatalf("moved request not visible in the tree; texts=%v", tt.Texts())
	}
	// The request now lives inside the Archive folder, which the move expanded.
	if !ps.expanded["collections/archive"] {
		t.Fatal("archive folder should be expanded after the move")
	}
	target := findFolderInTree(ps, "collections/archive")
	if target == nil || !containsEntry(target.Requests, e) {
		t.Fatal("rendered tree does not place the request under Archive")
	}
}
