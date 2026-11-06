package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLinkProject(t *testing.T) *Project {
	t.Helper()
	p := newDocProject(t)
	writeDoc(t, p, "docs/guide.md", "# Guide\n\n## Install\n\nText.\n\n## 中文 标题\n")
	writeDoc(t, p, "docs/other page.md", "# Other\n\n## Details\n")
	writeDoc(t, p, "docs/sub/child.md", "# Child\n\n## Deep Section\n")
	writeDoc(t, p, "docs/assets/pic.png", "\x89PNG\r\n\x1a\n")
	return p
}

func TestResolveDocLinkAnchors(t *testing.T) {
	p := newLinkProject(t)
	tgt, err := p.ResolveDocLink("docs/guide.md", "#install")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Kind != LinkAnchor || tgt.Slug != "install" || tgt.Line != 3 || tgt.RelPath != "docs/guide.md" {
		t.Fatalf("anchor target = %+v", tgt)
	}
	// Percent-encoded Chinese anchor.
	tgt, err = p.ResolveDocLink("docs/guide.md", "#%E4%B8%AD%E6%96%87-%E6%A0%87%E9%A2%98")
	if err != nil || tgt.Slug != "中文-标题" || tgt.Line != 7 {
		t.Fatalf("chinese anchor = %+v, %v", tgt, err)
	}
}

func TestResolveDocLinkRelativeDocuments(t *testing.T) {
	p := newLinkProject(t)
	tgt, err := p.ResolveDocLink("docs/guide.md", "other%20page.md#details")
	if err != nil {
		t.Fatal(err)
	}
	if tgt.Kind != LinkDoc || tgt.RelPath != "docs/other page.md" || tgt.Slug != "details" || tgt.Line != 3 {
		t.Fatalf("relative doc = %+v", tgt)
	}
	tgt, err = p.ResolveDocLink("docs/guide.md", "sub/child.md")
	if err != nil || tgt.RelPath != "docs/sub/child.md" || tgt.Kind != LinkDoc {
		t.Fatalf("nested doc = %+v, %v", tgt, err)
	}
	tgt, err = p.ResolveDocLink("docs/sub/child.md", "../guide.md#install")
	if err != nil || tgt.RelPath != "docs/guide.md" || tgt.Line != 3 {
		t.Fatalf("parent-relative doc = %+v, %v", tgt, err)
	}
}

func TestResolveDocLinkExternalOnly(t *testing.T) {
	p := newLinkProject(t)
	for _, dest := range []string{"https://example.com/a?b#c", "http://localhost:8080/", "mailto:dev@example.com"} {
		tgt, err := p.ResolveDocLink("docs/guide.md", dest)
		if err != nil || tgt.Kind != LinkExternal || tgt.URL != dest {
			t.Errorf("%s = %+v, %v", dest, tgt, err)
		}
	}
}

func TestResolveDocLinkRejectsUnsafeDestinations(t *testing.T) {
	p := newLinkProject(t)
	cases := map[string]string{
		"javascript:alert(1)":     LinkProblemDangerousScheme,
		"data:text/html,<b>x</b>": LinkProblemDangerousScheme,
		"file:///etc/passwd":      LinkProblemDangerousScheme,
		"vbscript:msgbox(1)":      LinkProblemDangerousScheme,
		"../../../etc/passwd":     LinkProblemOutsideProject,
		"/etc/passwd":             LinkProblemAbsolutePath,
		"%2Fetc%2Fpasswd":         LinkProblemAbsolutePath,
		"//example.com/x":         LinkProblemUnsupportedScheme,
		"missing.md":              LinkProblemMissingDocument,
		"#nope":                   LinkProblemMissingAnchor,
		"other%20page.md#nope":    LinkProblemMissingAnchor,
		"run.exe":                 LinkProblemUnsupportedAsset,
		"":                        LinkProblemEmpty,
		"a\\b":                    LinkProblemDangerousScheme,
	}
	for dest, want := range cases {
		_, err := p.ResolveDocLink("docs/guide.md", dest)
		if err == nil {
			t.Errorf("%q: expected %s", dest, want)
			continue
		}
		if got := ProblemKind(err); got != want {
			t.Errorf("%q: kind = %q, want %q (%v)", dest, got, want, err)
		}
	}
}

func TestResolveDocLinkAssetAndSymlinkEscape(t *testing.T) {
	p := newLinkProject(t)
	tgt, err := p.ResolveDocLink("docs/guide.md", "assets/pic.png")
	if err != nil || tgt.Kind != LinkAsset || tgt.RelPath != "docs/assets/pic.png" {
		t.Fatalf("asset link = %+v, %v", tgt, err)
	}

	outside := filepath.Join(t.TempDir(), "secret.png")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(p.Dir, "docs", "assets", "escape.png")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err = p.ResolveDocLink("docs/guide.md", "assets/escape.png")
	if got := ProblemKind(err); got != LinkProblemOutsideProject {
		t.Fatalf("symlink escape kind = %q (%v)", got, err)
	}
}

func TestDiagnoseDocReportsPositionedFindings(t *testing.T) {
	p := newLinkProject(t)
	src := strings.Join([]string{
		"# Guide",
		"",
		"[gone](missing.md)",
		"![absent](assets/nope.png)",
		"[bad](javascript:alert(1))",
		"![remote](https://example.com/x.png)",
		"```mermaid",
		"graph TD",
		"```",
		"<script>alert(1)</script>",
		"",
	}, "\n")
	diags := p.DiagnoseDoc("docs/guide.md", src)
	byKind := map[string][]DocDiagnostic{}
	for _, d := range diags {
		byKind[d.Kind] = append(byKind[d.Kind], d)
	}
	if len(byKind[DiagMissingLink]) != 1 || byKind[DiagMissingLink][0].Line != 3 {
		t.Errorf("missing link: %+v", byKind[DiagMissingLink])
	}
	if len(byKind[DiagMissingAsset]) < 1 || byKind[DiagMissingAsset][0].Line != 4 {
		t.Errorf("missing asset: %+v", byKind[DiagMissingAsset])
	}
	if len(byKind[DiagDangerousScheme]) != 1 || byKind[DiagDangerousScheme][0].Line != 5 {
		t.Errorf("dangerous: %+v", byKind[DiagDangerousScheme])
	}
	if len(byKind[DiagRemoteImage]) != 1 || byKind[DiagRemoteImage][0].Line != 6 {
		t.Errorf("remote image: %+v", byKind[DiagRemoteImage])
	}
	if len(byKind[DiagMermaid]) != 1 || byKind[DiagMermaid][0].Line != 7 {
		t.Errorf("mermaid: %+v", byKind[DiagMermaid])
	}
	if len(byKind[DiagHTMLNotExecuted]) < 1 {
		t.Errorf("html: %+v", byKind[DiagHTMLNotExecuted])
	}
	// Sorted by line.
	for i := 1; i < len(diags); i++ {
		if diags[i-1].Line > diags[i].Line {
			t.Fatalf("diagnostics not sorted: %+v", diags)
		}
	}
}

func TestDiagnoseDocAcceptsValidLinksAndAnchors(t *testing.T) {
	p := newLinkProject(t)
	src := "[ok](other%20page.md#details)\n[anchor](#install)\n![pic](assets/pic.png)\n"
	if diags := p.DiagnoseDoc("docs/guide.md", src); len(diags) != 0 {
		t.Fatalf("valid links produced diagnostics: %+v", diags)
	}
}
