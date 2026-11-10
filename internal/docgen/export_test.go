package docgen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorequest/internal/core"
)

// All tests in this package are authored but NOT executed in phase 1; the
// post-integration verification run executes them with the go toolchain.

func freshTarget(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "export")
}

func baseOpts(target string) ExportOptions {
	return ExportOptions{
		Format:        ExportMarkdown,
		TargetDir:     target,
		IncludeAssets: true,
		Labels:        ChineseCardLabels(),
	}
}

func TestPreviewMarkdownSetPlansFilesWithoutWriting(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	prev := PreviewExport(p, baseOpts(target))
	if prev.Err != nil {
		t.Fatalf("preview: %v", prev.Err)
	}
	if len(prev.Files) == 0 {
		t.Fatalf("no files planned: %+v", prev)
	}
	paths := map[string]bool{}
	for _, f := range prev.Files {
		paths[f.TargetPath] = true
	}
	for _, want := range []string{"docs/api.md", "docs/guide.md", "docs/assets/图标 example.png", "README.md"} {
		if !paths[want] {
			t.Fatalf("planned set misses %s: %+v", want, prev.Files)
		}
	}
	// Preview must not write.
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("preview wrote to the target directory")
	}
	if len(prev.Snippets["docs/api.md"]) == 0 {
		t.Fatalf("document snippet missing")
	}
	// The markdown set keeps expanded cards.
	if !strings.Contains(prev.Snippets["docs/api.md"], "#### `users.me`") {
		t.Fatalf("snippet does not show the derived card")
	}
	if !prev.HasErrors() {
		t.Fatalf("the fixture contains a missing doc/missing request and must report errors")
	}
	kinds := map[string]int{}
	for _, d := range prev.Diagnostics {
		kinds[d.Kind]++
	}
	if kinds[DiagMissingRequest] == 0 || kinds[core.DiagMissingLink] == 0 || kinds[core.DiagMissingAsset] == 0 || kinds[DiagRefNoCard] == 0 {
		t.Fatalf("missing categories in preview: %+v", kinds)
	}
}

func TestPreviewHTMLSetPlansAssetsAndEscapes(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)
	opts.Format = ExportHTML
	prev := PreviewExport(p, opts)
	if prev.Err != nil {
		t.Fatalf("preview: %v", prev.Err)
	}
	paths := map[string]bool{}
	for _, f := range prev.Files {
		paths[f.TargetPath] = true
	}
	if !paths["docs/api.html"] || !paths["index.html"] {
		t.Fatalf("html set misses pages: %+v", prev.Files)
	}
	if !paths["docs/assets/图标 example.png"] {
		t.Fatalf("local attachment not collected: %+v", prev.Files)
	}
	sum := prev.Snippets["docs/api.html"]
	if strings.Contains(sum, "<script") || strings.Contains(sum, "http-equiv") {
		t.Fatalf("exported page must not contain scripts or meta refresh")
	}
	if strings.Contains(sum, " src=\"http") {
		t.Fatalf("remote image leaked into export")
	}
}

func TestRunExportWritesAtomicallyAndReRuns(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)

	res, err := RunExport(p, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Written) == 0 || len(res.Conflicts) != 0 {
		t.Fatalf("first run: %+v / conflicts %+v", res.Written, res.Conflicts)
	}
	data, err := os.ReadFile(filepath.Join(target, "docs", "api.md"))
	if err != nil || !strings.HasPrefix(string(data), GeneratedFileMarker) {
		t.Fatalf("exported api.md wrong: %v %q", err, string(data[:min(40, len(data))]))
	}
	if !strings.Contains(string(data), "#### `users.me`") {
		t.Fatalf("exported markdown lacks the derived card")
	}
	if !strings.Contains(string(data), secretTemplateRef) {
		t.Fatalf("exported markdown dropped the authored template reference")
	}
	if strings.Contains(string(data), "8q6v2x0z4k9d7f3n1m5y") {
		t.Fatalf("exported markdown leaked the credential-shaped example value")
	}

	// Re-run without overwrite: everything conflicts, nothing changes.
	res2, err := RunExport(p, opts)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	if len(res2.Written) != 0 || len(res2.Conflicts) == 0 {
		t.Fatalf("second run without overwrite: written=%d conflicts=%d", len(res2.Written), len(res2.Conflicts))
	}
	// Re-run with overwrite: docgen files carry the marker and update.
	opts.AllowOverwrite = true
	res3, err := RunExport(p, opts)
	if err != nil {
		t.Fatalf("third run: %v", err)
	}
	if len(res3.Written) != len(res.Written) {
		t.Fatalf("overwriting docgen files failed: %+v", res3.Conflicts)
	}
}

func TestRunExportNeverOverwritesManualFiles(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)
	manual := "我的手工文档，不许覆盖。"

	// A manual file at every planned target.
	prev := PreviewExport(p, opts)
	for _, f := range prev.Files {
		abs := filepath.Join(target, filepath.FromSlash(f.TargetPath))
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(abs, []byte(manual), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	opts.AllowOverwrite = true // even with overwrite permission…
	res, err := RunExport(p, opts)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(res.Written) != 0 {
		t.Fatalf("manual files were overwritten: %+v", res.Written)
	}
	data, _ := os.ReadFile(filepath.Join(target, "README.md"))
	if string(data) != manual {
		t.Fatalf("README.md was replaced: %q", string(data))
	}
}

func TestExportRefusesTargetInsideProjectStore(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	inside := filepath.Join(p.Dir, "docs-export")
	res := PreviewExport(p, baseOpts(inside))
	if res.Err == nil || !strings.Contains(res.Err.Error(), "project store") {
		t.Fatalf("export into the store must be refused: %v", res.Err)
	}
}

func TestExportSymlinkAndTraversalProtection(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	// docs/out inside the target is a symlink pointing OUT; writing through
	// it must be refused.
	escaped := filepath.Join(t.TempDir(), "escape.md")
	if err := os.WriteFile(escaped, []byte("escaped"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(escaped, filepath.Join(target, "docs")); err != nil {
		t.Skip("symlinks unavailable on this platform")
	}
	opts := baseOpts(target)
	if _, err := RunExport(p, opts); err == nil {
		t.Fatalf("writing through a symlinked target directory must fail")
	}
}

func TestExportLimitsBoundTheSet(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)
	opts.Limits = ExportLimits{MaxFiles: 2, MaxTotalBytes: 300000}
	prev := PreviewExport(p, opts)
	if prev.Err != nil {
		t.Fatalf("preview: %v", prev.Err)
	}
	if !prev.TruncatedFiles {
		t.Fatalf("file limit did not truncate the plan: %+v", prev.Files)
	}
}

func TestExportSingleDocumentDiagnosticsForOutsideLinks(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)
	opts.Docs = []string{"docs/api.md"}
	prev := PreviewExport(p, opts)
	if prev.Err != nil {
		t.Fatalf("preview: %v", prev.Err)
	}
	found := false
	for _, d := range prev.Diagnostics {
		if d.Kind == DiagOutsideSet && d.Dest == "docs/guide.md" {
			found = true
		}
	}
	if !found {
		t.Fatalf("outside-set doc link not reported: %+v", prev.Diagnostics)
	}
}

func TestPreviewNilProjectFailsCleanly(t *testing.T) {
	prev := PreviewExport(nil, baseOpts(t.TempDir()))
	if prev.Err == nil {
		t.Fatalf("nil project must fail the preview")
	}
	if len(prev.Diagnostics) != 0 {
		t.Fatalf("nil project must set Err, not diagnostics: %+v", prev.Diagnostics)
	}
}

func findDiagramFiles(files []ExportFile) []ExportFile {
	var out []ExportFile
	for _, f := range files {
		if strings.HasPrefix(f.TargetPath, "diagrams/") && strings.HasSuffix(f.TargetPath, ".svg") {
			out = append(out, f)
		}
	}
	return out
}

func TestMarkdownExportCarriesStandaloneDiagramSVG(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)
	prev := PreviewExport(p, opts)
	if prev.Err != nil {
		t.Fatalf("preview: %v", prev.Err)
	}
	diagrams := findDiagramFiles(prev.Files)
	if len(diagrams) != 1 {
		t.Fatalf("expected one standalone SVG, got %+v", prev.Files)
	}
	if _, err := RunExport(p, opts); err != nil {
		t.Fatalf("export: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(target, filepath.FromSlash(diagrams[0].TargetPath)))
	if err != nil {
		t.Fatalf("diagram SVG not written: %v", err)
	}
	svg := string(data)
	if !strings.HasPrefix(svg, GeneratedFileMarker) || !strings.Contains(svg, "<svg ") {
		t.Fatalf("SVG asset is not a generated, self-contained SVG: %.120q", svg)
	}
	for _, bad := range []string{"<script", "href=", "foreignObject", "url("} {
		if strings.Contains(strings.ToLower(svg), bad) {
			t.Fatalf("unsafe SVG construct %q: %s", bad, svg)
		}
	}
	// The Markdown document keeps the Mermaid source verbatim.
	doc, err := os.ReadFile(filepath.Join(target, "docs/api.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(doc), "```mermaid") || !strings.Contains(string(doc), "graph TD") {
		t.Fatalf("Markdown export must preserve the Mermaid source: %s", doc)
	}
}

func TestHTMLExportEmbedsDiagramAndStillWritesSVG(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	target := freshTarget(t)
	opts := baseOpts(target)
	opts.Format = ExportHTML
	prev := PreviewExport(p, opts)
	if prev.Err != nil {
		t.Fatalf("preview: %v", prev.Err)
	}
	if len(findDiagramFiles(prev.Files)) != 1 {
		t.Fatalf("HTML set must carry the standalone SVG too: %+v", prev.Files)
	}
	if _, err := RunExport(p, opts); err != nil {
		t.Fatalf("export: %v", err)
	}
	page, err := os.ReadFile(filepath.Join(target, "docs/api.html"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(page), `class="docgen-diagram"`) || !strings.Contains(string(page), "<svg ") {
		t.Fatalf("HTML page does not embed the diagram: %s", page)
	}
	if strings.Contains(strings.ToLower(string(page)), "<script") {
		t.Fatalf("HTML export must stay script-free")
	}
}
