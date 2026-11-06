package core

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestNetworkResultSanitizerCoversNewMetadata(t *testing.T) {
	red := NewRedactor()
	red.RegisterSecret("network-secret")
	raw := &ExecResult{
		FinalURL:  "https://example.com/?key=network-secret",
		Redirects: []RedirectHop{{FromURL: "https://example.com/network-secret", ToURL: "https://user:unregistered-password@example.com/"}},
		Cookies:   []CookieEntry{{Name: "sid", Value: "unregistered-cookie"}},
		Trace:     &NetworkTrace{ProxyUsed: "http://user:proxy-password@localhost:8080"},
	}
	safe := red.SanitizeExecResult(raw)
	data, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"network-secret", "unregistered-password", "unregistered-cookie", "proxy-password"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("metadata leaked %s", secret)
		}
	}
	if raw.Cookies[0].Value != "unregistered-cookie" || raw.Redirects[0].FromURL != "https://example.com/network-secret" || raw.Trace.ProxyUsed != "http://user:proxy-password@localhost:8080" {
		t.Fatal("sanitizer modified live network state")
	}
}
