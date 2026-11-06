package core

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestChangeSetPreviewApplyAndRollbackOnFailure(t *testing.T) {
	root := t.TempDir()
	s := NewStore(root)

	origA := []byte("schemaVersion: 1\nid: a\nname: A\nhttp:\n  method: GET\n  url: http://h/a\n")
	origB := []byte("schemaVersion: 1\nid: b\nname: B\nhttp:\n  method: GET\n  url: http://h/b\n")
	if err := s.WriteAtomic("collections/a.request.yaml", origA); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteAtomic("collections/b.request.yaml", origB); err != nil {
		t.Fatal(err)
	}
	_, digA, _ := s.Read("collections/a.request.yaml")
	_, digB, _ := s.Read("collections/b.request.yaml")

	updatedA := []byte("schemaVersion: 1\nid: a\nname: A2\nhttp:\n  method: POST\n  url: http://h/a2\n")
	createdC := []byte("# Doc C\n\nHello.\n")

	// 1. Fault-injected failure mid-Apply MUST roll back all earlier steps!
	csFail := s.NewChangeSet().
		Update("collections/a.request.yaml", updatedA, digA).
		Create("docs/c.md", createdC).
		Move("collections/b.request.yaml", "collections/sub/b.request.yaml", digB, nil)
	csFail.beforeWriteStep = func(stepIndex int, rel string) error {
		if stepIndex == 2 {
			return fmt.Errorf("simulated disk full at step %d (%s)", stepIndex, rel)
		}
		return nil
	}

	previews, err := csFail.Preview()
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(previews) != 3 {
		t.Fatalf("expected 3 previews, got %d", len(previews))
	}
	if _, err := csFail.Apply(); err == nil {
		t.Fatal("expected injected error on Apply")
	}

	// Verify exact pre-apply state restored on disk!
	gotA, _, err := s.Read("collections/a.request.yaml")
	if err != nil || string(gotA) != string(origA) {
		t.Fatalf("a.request.yaml not rolled back: %q (err=%v)", gotA, err)
	}
	gotB, _, err := s.Read("collections/b.request.yaml")
	if err != nil || string(gotB) != string(origB) {
		t.Fatalf("b.request.yaml not rolled back: %q (err=%v)", gotB, err)
	}
	if s.Exists("docs/c.md") || s.Exists("collections/sub/b.request.yaml") {
		t.Fatal("created/moved targets remained on disk after rollback")
	}

	// 2. Clean Apply succeeds, and explicit Receipt.Rollback() restores everything.
	csOK := s.NewChangeSet().
		Update("collections/a.request.yaml", updatedA, digA).
		Create("docs/c.md", createdC).
		Move("collections/b.request.yaml", "collections/sub/b.request.yaml", digB, nil)

	receipt, err := csOK.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if s.Exists("collections/b.request.yaml") || !s.Exists("collections/sub/b.request.yaml") || !s.Exists("docs/c.md") {
		t.Fatal("expected changes applied on disk")
	}
	if err := receipt.Rollback(); err != nil {
		t.Fatalf("Receipt.Rollback: %v", err)
	}
	gotA2, _, _ := s.Read("collections/a.request.yaml")
	gotB2, _, _ := s.Read("collections/b.request.yaml")
	if string(gotA2) != string(origA) || string(gotB2) != string(origB) || s.Exists("docs/c.md") || s.Exists("collections/sub/b.request.yaml") {
		t.Fatal("explicit Rollback did not restore original project files")
	}
}

func TestStoreDanglingAndEscapingSymlinkAndConflictMarkers(t *testing.T) {
	outer := t.TempDir()
	root := filepath.Join(outer, ".gorequest")
	if err := os.MkdirAll(filepath.Join(root, "collections"), 0o755); err != nil {
		t.Fatal(err)
	}
	s := NewStore(root)

	// 1. Active symlink escaping Root.
	secretOutside := filepath.Join(outer, "outside.yaml")
	if err := os.WriteFile(secretOutside, []byte("secret: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	escapeLink := filepath.Join(root, "collections", "escape.request.yaml")
	if err := os.Symlink(secretOutside, escapeLink); err != nil {
		t.Skipf("symlink not supported on host: %v", err)
	}
	if _, _, err := s.Read("collections/escape.request.yaml"); err == nil {
		t.Fatal("expected escaping symlink to be rejected")
	}

	// 2. Dangling symlink pointing to non-existent file outside Root!
	danglingLink := filepath.Join(root, "collections", "dangling.request.yaml")
	if err := os.Symlink(filepath.Join(outer, "nonexistent-target.yaml"), danglingLink); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResolvePath("collections/dangling.request.yaml"); err == nil {
		t.Fatal("expected dangling symlink to be rejected")
	}
	if err := s.WriteAtomic("collections/dangling.request.yaml", []byte("x: 1\n")); err == nil {
		t.Fatal("expected WriteAtomic through dangling symlink to be rejected")
	}

	// 3. Git conflict markers detection in ChangeSet and DetectGitConflictMarkers.
	conflictedYAML := []byte("schemaVersion: 1\nid: x\n<<<<<<< HEAD\nname: Mine\n=======\nname: Theirs\n>>>>>>> main\n")
	line, has := DetectGitConflictMarkers(conflictedYAML)
	if !has || line != 3 {
		t.Fatalf("DetectGitConflictMarkers = (%d, %v), want (3, true)", line, has)
	}
	cs := s.NewChangeSet().Create("collections/conflicted.request.yaml", conflictedYAML)
	if _, err := cs.Preview(); err == nil {
		t.Fatal("expected ChangeSet.Preview to reject unresolved Git conflict markers in YAML")
	}
}
