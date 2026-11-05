package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// folderRel is a convenience to reach the on-disk collections dir for a test
// project root returned by NewProject.
func collectionsAbs(root string) string {
	return filepath.Join(root, ".gorequest", "collections")
}

func mustNewProject(t *testing.T, name string) (string, *Project) {
	t.Helper()
	dir := t.TempDir()
	p, err := NewProject(dir, name)
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return dir, p
}

// treeRequestIDs flattens every request reachable in the folder tree.
func treeRequestIDs(nodes []*CollectionEntry) []string {
	var out []string
	var walk func([]*CollectionEntry)
	walk = func(ns []*CollectionEntry) {
		for _, n := range ns {
			for _, r := range n.Requests {
				out = append(out, r.Request.ID)
			}
			walk(n.Children)
		}
	}
	walk(nodes)
	return out
}

func TestNewFolderPersistsAcrossReopen(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	node, err := p.NewFolder("", "Auth Stuff")
	if err != nil {
		t.Fatalf("NewFolder: %v", err)
	}
	if node.RelPath != "collections/auth-stuff" {
		t.Fatalf("folder rel = %q", node.RelPath)
	}
	if node.Name != "Auth Stuff" {
		t.Fatalf("folder display name = %q", node.Name)
	}
	// Empty folders persist because they carry a collection.yaml.
	if _, err := os.Stat(filepath.Join(collectionsAbs(dir), "auth-stuff", "collection.yaml")); err != nil {
		t.Fatalf("collection.yaml not written: %v", err)
	}

	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	found := findCollectionNode(p2.Collections, "collections/auth-stuff")
	if found == nil {
		t.Fatalf("empty folder missing after reopen; tree=%+v", p2.Collections)
	}
	if found.Name != "Auth Stuff" {
		t.Fatalf("folder name lost on reopen: %q", found.Name)
	}
}

func TestNestedFoldersAndRequests(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	if _, err := p.NewFolder("", "Users"); err != nil {
		t.Fatalf("NewFolder Users: %v", err)
	}
	if _, err := p.NewFolder("users", "Settings"); err != nil {
		t.Fatalf("NewFolder Settings: %v", err)
	}
	entry, err := p.NewRequest("users/settings", "List Settings")
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	if entry.RelPath != "collections/users/settings/list-settings.request.yaml" {
		t.Fatalf("nested request path = %q", entry.RelPath)
	}

	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	users := findCollectionNode(p2.Collections, "collections/users")
	if users == nil {
		t.Fatal("users folder missing")
	}
	settings := findCollectionNode(p2.Collections, "collections/users/settings")
	if settings == nil {
		t.Fatal("nested settings folder missing")
	}
	if len(settings.Requests) != 1 || settings.Requests[0].Request.ID != entry.Request.ID {
		t.Fatalf("nested request not attached to its folder: %+v", settings.Requests)
	}
}

func TestNewRequestRootVsFolder(t *testing.T) {
	_, p := mustNewProject(t, "Demo")
	if _, err := p.NewFolder("", "Auth"); err != nil {
		t.Fatal(err)
	}
	root, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	inFolder, err := p.NewRequest("auth", "Login")
	if err != nil {
		t.Fatal(err)
	}
	if root.RelPath != "collections/ping.request.yaml" {
		t.Fatalf("root request path = %q", root.RelPath)
	}
	if inFolder.RelPath != "collections/auth/login.request.yaml" {
		t.Fatalf("folder request path = %q", inFolder.RelPath)
	}
	if inFolder.Request.ID != "auth.login" {
		t.Fatalf("folder request id = %q", inFolder.Request.ID)
	}
	roots := p.RootRequests()
	if len(roots) != 1 || roots[0].Request.ID != root.Request.ID {
		t.Fatalf("root requests = %+v", roots)
	}
	ids := treeRequestIDs(p.Collections)
	if len(ids) != 1 || ids[0] != inFolder.Request.ID {
		t.Fatalf("tree should contain just the folder request, got %v", ids)
	}
}

func TestFolderDuplicateAndCrossParent(t *testing.T) {
	_, p := mustNewProject(t, "Demo")
	if _, err := p.NewFolder("", "Users"); err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewFolder("", "Users"); err == nil {
		t.Fatal("same-parent duplicate folder should be refused")
	}
	// A folder with the same name under a different parent is allowed.
	if _, err := p.NewFolder("users", "Users"); err != nil {
		t.Fatalf("cross-parent same name should be allowed: %v", err)
	}
}

func TestInvalidFolderNamesRejected(t *testing.T) {
	_, p := mustNewProject(t, "Demo")
	for _, name := range []string{"", "   ", ".", "..", "a/b", `a\b`, "/abs"} {
		if _, err := p.NewFolder("", name); err == nil {
			t.Fatalf("folder name %q should be rejected", name)
		}
	}
}

func TestSymlinkEscapeRejected(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(collectionsAbs(dir), "evil")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := p.NewFolder("evil", "Sub"); err == nil {
		t.Fatal("creating a folder through a symlink escape should be refused")
	}
	if _, err := p.NewRequest("evil", "X"); err == nil {
		t.Fatal("creating a request through a symlink escape should be refused")
	}
}

func TestNewRequestRejectsTraversalFolder(t *testing.T) {
	_, p := mustNewProject(t, "Demo")
	for _, rel := range []string{"../evil", "a/../../b", "/abs"} {
		if _, err := p.NewRequest(rel, "X"); err == nil {
			t.Fatalf("NewRequest folder %q should be rejected", rel)
		}
	}
}

func TestMoveRequestSuccessKeepsIDAndUpdatesTree(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	entry, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	originalID := entry.Request.ID
	if _, err := p.NewFolder("", "Health"); err != nil {
		t.Fatal(err)
	}
	if err := p.MoveRequest(entry, "health"); err != nil {
		t.Fatalf("MoveRequest: %v", err)
	}
	if entry.Request.ID != originalID {
		t.Fatalf("request id changed on move: %q -> %q", originalID, entry.Request.ID)
	}
	if entry.RelPath != "collections/health/ping.request.yaml" {
		t.Fatalf("moved rel = %q", entry.RelPath)
	}
	// Old file gone, new file present.
	if _, err := os.Stat(filepath.Join(collectionsAbs(dir), "ping.request.yaml")); !os.IsNotExist(err) {
		t.Fatalf("source file should be gone, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(collectionsAbs(dir), "health", "ping.request.yaml")); err != nil {
		t.Fatalf("target file missing: %v", err)
	}
	// Tree reflects the new location with no duplicate.
	health := findCollectionNode(p.Collections, "collections/health")
	if health == nil || len(health.Requests) != 1 || health.Requests[0] != entry {
		t.Fatalf("tree not updated after move: %+v", health)
	}
	if len(p.RootRequests()) != 0 {
		t.Fatalf("request still listed at root after move: %+v", p.RootRequests())
	}
}

func TestMoveRequestRefusesExistingTarget(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	if _, err := p.NewFolder("", "Health"); err != nil {
		t.Fatal(err)
	}
	// Put a request named the same in the folder, and another at the root.
	if _, err := p.NewRequest("health", "Ping"); err != nil {
		t.Fatal(err)
	}
	rootEntry, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(collectionsAbs(dir), "ping.request.yaml"))
	if err := p.MoveRequest(rootEntry, "health"); err == nil {
		t.Fatal("move onto an existing file should be refused")
	}
	// Source intact.
	after, err := os.ReadFile(filepath.Join(collectionsAbs(dir), "ping.request.yaml"))
	if err != nil {
		t.Fatalf("source file lost after refused move: %v", err)
	}
	if string(before) != string(after) {
		t.Fatal("source content changed after refused move")
	}
}

func TestMoveRequestMissingTargetKeepsSource(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	entry, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	if err := p.MoveRequest(entry, "does-not-exist"); err == nil {
		t.Fatal("move to a missing folder should be refused")
	}
	if entry.RelPath != "collections/ping.request.yaml" {
		t.Fatalf("entry path changed after failed move: %q", entry.RelPath)
	}
	if _, err := os.Stat(filepath.Join(collectionsAbs(dir), "ping.request.yaml")); err != nil {
		t.Fatalf("source removed after failed move: %v", err)
	}
}

func TestMoveRequestDetectsExternalEdit(t *testing.T) {
	dir, p := mustNewProject(t, "Demo")
	entry, err := p.NewRequest("", "Ping")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := p.NewFolder("", "Health"); err != nil {
		t.Fatal(err)
	}
	// Simulate an external modification after load.
	if err := os.WriteFile(filepath.Join(collectionsAbs(dir), "ping.request.yaml"), []byte("changed on disk\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err = p.MoveRequest(entry, "health")
	if !IsConflict(err) {
		t.Fatalf("expected conflict for externally edited source, got %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(collectionsAbs(dir), "ping.request.yaml")); statErr != nil {
		t.Fatalf("source removed after conflict: %v", statErr)
	}
}

func TestTreeShowsDirectoriesWithoutCollectionYAML(t *testing.T) {
	dir, _ := mustNewProject(t, "Demo")
	// A legacy tree created before collection.yaml existed: nested dirs and a
	// request, with no collection.yaml anywhere.
	deep := filepath.Join(collectionsAbs(dir), "legacy", "sub", "deep")
	if err := os.MkdirAll(deep, 0o755); err != nil {
		t.Fatal(err)
	}
	req := "schemaVersion: 1\nid: legacy.deep.ping\nname: Deep Ping\nhttp:\n  method: GET\n  url: http://h/ping\n"
	if err := os.WriteFile(filepath.Join(deep, "ping.request.yaml"), []byte(req), 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"collections/legacy", "collections/legacy/sub", "collections/legacy/sub/deep"} {
		if findCollectionNode(p.Collections, rel) == nil {
			t.Fatalf("synthetic folder %q missing from tree", rel)
		}
	}
	node := findCollectionNode(p.Collections, "collections/legacy/sub/deep")
	if len(node.Requests) != 1 || node.Requests[0].Request.ID != "legacy.deep.ping" {
		t.Fatalf("request not attached under legacy dirs: %+v", node.Requests)
	}
}

func TestCollectionVarsInheritedImmediately(t *testing.T) {
	dir, _ := mustNewProject(t, "Demo")
	// Write a folder with collection variables, then reopen so it is loaded.
	collDir := filepath.Join(collectionsAbs(dir), "users")
	if err := os.MkdirAll(collDir, 0o755); err != nil {
		t.Fatal(err)
	}
	col := &Collection{SchemaVersion: 1, ID: "users", Name: "Users", Variables: map[string]string{"team": "core", "baseUrl": "http://users"}}
	data, err := encodeYAML(col)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(collDir, "collection.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	p, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := p.NewRequest("users", "Me")
	if err != nil {
		t.Fatal(err)
	}
	if entry.CollectionVars["team"] != "core" || entry.CollectionVars["baseUrl"] != "http://users" {
		t.Fatalf("collection vars not inherited immediately: %+v", entry.CollectionVars)
	}
	vars, _, err := p.ResolveVars(entry, "")
	if err != nil {
		t.Fatal(err)
	}
	if vars["team"] != "core" {
		t.Fatalf("resolved vars lack inherited collection var: %+v", vars)
	}

	// Moving into another folder updates the inherited vars.
	if _, err := p.NewFolder("", "Archive"); err != nil {
		t.Fatal(err)
	}
	if err := p.MoveRequest(entry, "archive"); err != nil {
		t.Fatal(err)
	}
	if _, ok := entry.CollectionVars["team"]; ok {
		t.Fatalf("stale collection vars after move: %+v", entry.CollectionVars)
	}

	// Reopen confirms the request persisted at its new location.
	p2, err := OpenProject(dir)
	if err != nil {
		t.Fatal(err)
	}
	got := findCollectionNode(p2.Collections, "collections/archive")
	if got == nil || len(got.Requests) != 1 {
		t.Fatalf("moved request missing after reopen: %+v", got)
	}
	if _, ok := got.Requests[0].CollectionVars["team"]; ok {
		t.Fatalf("moved request should no longer inherit users vars: %+v", got.Requests[0].CollectionVars)
	}
	if !strings.Contains(got.Requests[0].RelPath, "archive/me.request.yaml") {
		t.Fatalf("unexpected rel path: %q", got.Requests[0].RelPath)
	}
}
