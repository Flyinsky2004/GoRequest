package core

import (
	"strings"
	"testing"
)

func TestUnicodeFolderCreateRenameAndReopen(t *testing.T) {
	root, p := mustNewProject(t, "Unicode Folders")
	node, err := p.NewFolder("", "中文")
	if err != nil {
		t.Fatal(err)
	}
	req, err := p.NewRequest("中文", "Login")
	if err != nil {
		t.Fatal(err)
	}
	id := req.Request.ID
	renamed, err := p.RenameFolder(node, "认证接口")
	if err != nil {
		t.Fatal(err)
	}
	if renamed.RelPath != "collections/认证接口" {
		t.Fatalf("path %q", renamed.RelPath)
	}
	reopened, err := OpenProject(root)
	if err != nil {
		t.Fatal(err)
	}
	if n := findCollectionNode(reopened.Collections, "collections/认证接口"); n == nil || n.Name != "认证接口" {
		t.Fatalf("folder missing: %+v", n)
	}
	if e := reopened.Requests[id]; e == nil || !strings.HasPrefix(e.RelPath, "collections/认证接口/") {
		t.Fatalf("request identity or path lost: %+v", e)
	}
	for _, name := range []string{"../中文", "中文/子目录", "中文\\子目录"} {
		if _, err := p.NewFolder("", name); err == nil {
			t.Fatalf("accepted unsafe name %q", name)
		}
	}
	if _, err := p.NewFolder("", "认证接口"); err == nil {
		t.Fatal("overwrote existing Unicode folder")
	}
}
