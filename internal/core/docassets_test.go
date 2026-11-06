package core

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveAndReadDocAsset(t *testing.T) {
	p := newLinkProject(t)
	writeDoc(t, p, "docs/assets/图片 one.png", "PNGDATA")
	info, err := p.ResolveDocAsset("docs/guide.md", "assets/%E5%9B%BE%E7%89%87%20one.png")
	if err != nil {
		t.Fatal(err)
	}
	if info.RelPath != "docs/assets/图片 one.png" || info.MediaType != "image/png" || info.Size != 7 {
		t.Fatalf("info = %+v", info)
	}
	data, got, err := p.ReadDocAsset("docs/guide.md", "assets/图片%20one.png")
	if err != nil || string(data) != "PNGDATA" || got.Size != 7 {
		t.Fatalf("read = %q %+v %v", data, got, err)
	}
	if svg, err := p.ResolveDocAsset("docs/guide.md", "assets/pic.svg"); err == nil || svg.IsSVG {
		t.Fatalf("missing svg should not resolve: %+v %v", svg, err)
	}
}

func TestResolveDocAssetRejectsUnsafe(t *testing.T) {
	p := newLinkProject(t)
	writeDoc(t, p, "docs/assets/file.exe", "MZ")
	writeDoc(t, p, "docs/assets/big.png", "x")
	if err := os.Truncate(filepath.Join(p.Dir, "docs", "assets", "big.png"), DefaultAssetMaxBytes+1); err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"https://example.com/x.png":  AssetProblemRemote,
		"data:image/png;base64,AAAA": AssetProblemRemote,
		"assets/file.exe":            AssetProblemUnsupported,
		"assets/nope.png":            AssetProblemMissing,
		"../../outside.png":          AssetProblemOutside,
		"/etc/passwd":                AssetProblemOutside,
		"%2Fetc%2Fpasswd":            AssetProblemOutside,
		"assets/big.png":             AssetProblemTooLarge,
		"assets":                     AssetProblemUnsupported,
	}
	for ref, want := range cases {
		_, err := p.ResolveDocAsset("docs/guide.md", ref)
		if err == nil {
			t.Errorf("%q: expected %s", ref, want)
			continue
		}
		if got := AssetKind(err); got != want {
			t.Errorf("%q: kind = %q, want %q (%v)", ref, got, want, err)
		}
	}

	// A symlink, even one inside the project, is refused.
	if err := os.Symlink(filepath.Join(p.Dir, "docs", "assets", "pic.png"),
		filepath.Join(p.Dir, "docs", "assets", "alias.png")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	_, err := p.ResolveDocAsset("docs/guide.md", "assets/alias.png")
	if got := AssetKind(err); got != AssetProblemSymlink {
		t.Fatalf("in-project symlink kind = %q (%v)", got, err)
	}
}

func TestImportDocAssetCopiesWithoutOverwrite(t *testing.T) {
	p := newLinkProject(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "My Pic.png")
	if err := os.WriteFile(src, []byte("FIRST"), 0o600); err != nil {
		t.Fatal(err)
	}
	imp, err := p.ImportDocAsset("docs/guide.md", src, "")
	if err != nil {
		t.Fatal(err)
	}
	if imp.RelPath != "docs/assets/My Pic.png" || imp.RefPath != "assets/My Pic.png" {
		t.Fatalf("import = %+v", imp)
	}
	if imp.Markdown != "![My Pic](assets/My%20Pic.png)" {
		t.Fatalf("markdown = %q", imp.Markdown)
	}
	if b, _, err := p.Store.Read(imp.RelPath); err != nil || string(b) != "FIRST" {
		t.Fatalf("stored = %q %v", b, err)
	}

	if err := os.WriteFile(src, []byte("SECOND"), 0o600); err != nil {
		t.Fatal(err)
	}
	second, err := p.ImportDocAsset("docs/guide.md", src, "docs/assets")
	if err != nil {
		t.Fatal(err)
	}
	if second.RelPath != "docs/assets/My Pic-2.png" {
		t.Fatalf("second import = %+v", second)
	}
	if b, _, _ := p.Store.Read(imp.RelPath); string(b) != "FIRST" {
		t.Fatalf("first import was overwritten: %q", b)
	}
}

func TestImportDocAssetRelativeToSubdirectoryAndSanitizing(t *testing.T) {
	p := newLinkProject(t)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "图片 (1).png")
	if err := os.WriteFile(src, []byte("X"), 0o600); err != nil {
		t.Fatal(err)
	}
	imp, err := p.ImportDocAsset("docs/sub/child.md", src, "docs/assets")
	if err != nil {
		t.Fatal(err)
	}
	if imp.RelPath != "docs/assets/图片 -1-.png" && imp.RelPath != "docs/assets/图片 -1.png" {
		t.Fatalf("sanitized name = %q", imp.RelPath)
	}
	if imp.RefPath != "../assets/"+strings.TrimPrefix(imp.RelPath, "docs/assets/") {
		t.Fatalf("ref from subdir = %q", imp.RefPath)
	}
	if !strings.HasPrefix(imp.Markdown, "![") || !strings.Contains(imp.Markdown, "../assets/") {
		t.Fatalf("markdown = %q", imp.Markdown)
	}

	// Non-image whitelisted files become normal links.
	txt := filepath.Join(srcDir, "notes.txt")
	if err := os.WriteFile(txt, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	imp, err = p.ImportDocAsset("docs/guide.md", txt, "")
	if err != nil {
		t.Fatal(err)
	}
	if imp.Markdown != "[notes](assets/notes.txt)" {
		t.Fatalf("text attachment markdown = %q", imp.Markdown)
	}
}

func TestImportDocAssetRejectsBadInputs(t *testing.T) {
	p := newLinkProject(t)
	srcDir := t.TempDir()
	exe := filepath.Join(srcDir, "run.exe")
	if err := os.WriteFile(exe, []byte("MZ"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := p.ImportDocAsset("docs/guide.md", exe, ""); AssetKind(err) != AssetProblemUnsupported {
		t.Fatalf("exe: %v", err)
	}
	png := filepath.Join(srcDir, "ok.png")
	if err := os.WriteFile(png, []byte("P"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"collections", "../outside", "/tmp/assets", "docs/../../x"} {
		if _, err := p.ImportDocAsset("docs/guide.md", png, dir); err == nil {
			t.Errorf("target %q should be rejected", dir)
		}
	}
	if err := os.Symlink(png, filepath.Join(srcDir, "link.png")); err != nil {
		t.Skipf("symlink unsupported: %v", err)
	}
	if _, err := p.ImportDocAsset("docs/guide.md", filepath.Join(srcDir, "link.png"), ""); AssetKind(err) != AssetProblemSymlink {
		t.Fatalf("symlink source: %v", err)
	}
}

func TestMarkdownDestinationEscaping(t *testing.T) {
	cases := map[string]string{
		"assets/a b.png":  "assets/a%20b.png",
		"assets/a(1).png": "assets/a%281%29.png",
		"assets/100%.png": "assets/100%25.png",
		"assets/x#y?.png": "assets/x%23y%3F.png",
		"assets/图片 一.png": "assets/图片%20一.png",
	}
	for in, want := range cases {
		if got := MarkdownDest(in); got != want {
			t.Errorf("MarkdownDest(%q) = %q, want %q", in, got, want)
		}
	}
	if got := MarkdownImageRef("a]b", "x.png"); got != `![a\]b](x.png)` {
		t.Errorf("image ref = %q", got)
	}
	if got := MarkdownLinkRef("a[b]", "x.md"); got != `[a\[b\]](x.md)` {
		t.Errorf("link ref = %q", got)
	}
}

func TestAllowedAssetExtsIsStable(t *testing.T) {
	exts := AllowedAssetExts()
	if len(exts) == 0 || exts[0] != "bmp" {
		t.Fatalf("exts not sorted: %v", exts)
	}
	imgs := ImageAssetExts()
	for _, ext := range imgs {
		if ext == "pdf" || ext == "zip" {
			t.Fatalf("non-image in image filters: %v", imgs)
		}
	}
}
