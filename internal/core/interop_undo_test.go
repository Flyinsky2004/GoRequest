package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestImportUndoPreservesLaterDiskEditsAndCanRetry(t *testing.T) {
	p, err := NewProject(t.TempDir(), "Undo")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := StageCURLImport(p, "curl https://example.com/undo", ImportOptions{})
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := plan.Apply()
	if err != nil {
		t.Fatal(err)
	}
	var rel string
	for _, e := range p.Requests {
		rel = e.RelPath
	}
	original, _, err := p.Store.Read(rel)
	if err != nil {
		t.Fatal(err)
	}
	edited := append(append([]byte{}, original...), []byte("\n# user's later edit\n")...)
	if err := p.Store.WriteAtomic(rel, edited); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Rollback(); err == nil {
		t.Fatal("undo overwrote later disk edit")
	}
	actual, _, err := p.Store.Read(rel)
	if err != nil || string(actual) != string(edited) {
		t.Fatal("user edit not preserved")
	}
	if err := p.Store.WriteAtomic(rel, original); err != nil {
		t.Fatal(err)
	}
	if err := receipt.Rollback(); err != nil {
		t.Fatal(err)
	}
	if p.Store.Exists(rel) {
		t.Fatal("retry failed to undo import")
	}
}

func TestPortableAttachmentConflictsProtectUserFiles(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "public.txt")
	rw := stagedRootFile{relPath: "public.txt", content: []byte("imported")}
	if err := os.WriteFile(path, []byte("created after preview"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyProjectRootAttachment(root, rw); err == nil {
		t.Fatal("overwrote newly created file")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if _, _, err := applyProjectRootAttachment(root, rw); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("edited after import"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := rollbackProjectRootAttachment(root, rw); err == nil {
		t.Fatal("deleted later edit")
	}
	actual, err := os.ReadFile(path)
	if err != nil || string(actual) != "edited after import" {
		t.Fatal("attachment edit lost")
	}
	if err := os.WriteFile(path, rw.content, 0600); err != nil {
		t.Fatal(err)
	}
	if err := rollbackProjectRootAttachment(root, rw); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("attachment undo failed")
	}
}
