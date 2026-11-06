package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

const docGuideSrc = "# Guide\n\n## Install\n\nBody text.\n\n## Usage\n\nMore text.\n"

func openDocForTest(t *testing.T, ps *projectState, rel, src string) *Tab {
	t.Helper()
	if err := ps.project.Store.WriteAtomic(rel, []byte(src)); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
	found := false
	for _, d := range ps.project.Docs {
		if d.RelPath == rel {
			found = true
		}
	}
	if !found {
		ps.project.Docs = append(ps.project.Docs, &core.DocEntry{RelPath: rel, Name: filepath.Base(rel)})
	}
	if err := ps.openDoc(rel); err != nil {
		t.Fatalf("openDoc: %v", err)
	}
	return ps.activeTab()
}

func TestDocOutlineClickMovesEditorAndPreview(t *testing.T) {
	a, ps := openTestProject(t, "Docs Outline")
	tab := openDocForTest(t, ps, "docs/guide.md", docGuideSrc)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Outline"); err != nil {
		t.Fatalf("open outline: %v", err)
	}
	tt.Frame()
	if !tt.HasText("H2 Install") {
		t.Fatalf("outline rows missing; texts=%v", tt.Texts())
	}
	if err := tt.Click("H2 Install"); err != nil {
		t.Fatalf("click heading: %v", err)
	}
	tt.Frame()
	if got := tab.DocEditor.CursorPos(); got.Line != 2 {
		t.Fatalf("cursor line = %d, want 2", got.Line)
	}
	// Switching to preview must not panic and must keep the anchor state.
	if err := tt.Click("Preview"); err != nil {
		t.Fatalf("preview: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Install") {
		t.Fatalf("preview did not render; texts=%v", tt.Texts())
	}
}

func TestDocSearchFindsAndJumpsWithinDocument(t *testing.T) {
	a, ps := openTestProject(t, "Docs Search")
	tab := openDocForTest(t, ps, "docs/guide.md", docGuideSrc)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Search"); err != nil {
		t.Fatalf("open search: %v", err)
	}
	tt.Frame()
	if err := tt.Click("Find in documents…"); err != nil {
		t.Fatalf("focus search input: %v", err)
	}
	tt.Type("Usage")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("docs/guide.md:7") {
		t.Fatalf("search result missing; texts=%v", tt.Texts())
	}
	// Jump to the hit.
	if err := tt.Click("docs/guide.md:7  ## Usage"); err != nil {
		t.Fatalf("click hit: %v", err)
	}
	tt.Frame()
	if got := tab.DocEditor.CursorPos(); got.Line != 6 {
		t.Fatalf("cursor line = %d, want 6", got.Line)
	}
}

func TestDocSearchAcrossDocumentsOpensTargetTab(t *testing.T) {
	a, ps := openTestProject(t, "Docs Project Search")
	tab := openDocForTest(t, ps, "docs/guide.md", docGuideSrc)
	openDocForTest(t, ps, "docs/other page.md", "# Other\n\n## Details\n\nneedle here\n")
	ps.activateTab(tab)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Search"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("All documents"); err != nil {
		t.Fatalf("scope: %v", err)
	}
	if err := tt.Click("Find in documents…"); err != nil {
		t.Fatal(err)
	}
	tt.Type("needle")
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("docs/other page.md:5") {
		t.Fatalf("cross-doc hit missing; texts=%v", tt.Texts())
	}
	if err := tt.Click("docs/other page.md:5  needle here"); err != nil {
		t.Fatalf("click hit: %v", err)
	}
	tt.Frame()
	active := ps.activeTab()
	if active == nil || active.DocRel != "docs/other page.md" {
		t.Fatalf("active doc = %+v", active)
	}
	if got := active.DocEditor.CursorPos(); got.Line != 4 {
		t.Fatalf("cursor line = %d, want 4", got.Line)
	}
}

func TestDocPreviewExternalLinkOpensOnlyOnClick(t *testing.T) {
	a, ps := openTestProject(t, "Docs Links")
	tab := openDocForTest(t, ps, "docs/links.md", "See [Example](https://example.com/docs) now.\n")
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Fatalf("opening a document opened URLs: %v", got)
	}
	if err := tt.Click("Example"); err != nil {
		t.Fatalf("click external link: %v", err)
	}
	tt.Frame()
	got := tt.OpenedURLs()
	if len(got) != 1 || got[0] != "https://example.com/docs" {
		t.Fatalf("opened URLs = %v", got)
	}
}

func TestDocPreviewBlocksDangerousLink(t *testing.T) {
	a, ps := openTestProject(t, "Docs Bad Link")
	tab := openDocForTest(t, ps, "docs/bad.md", "See [Bad](javascript:alert(1)) here.\n")
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Bad"); err != nil {
		t.Fatalf("click dangerous link: %v", err)
	}
	tt.Frame()
	if got := tt.OpenedURLs(); len(got) != 0 {
		t.Fatalf("dangerous link opened a URL: %v", got)
	}
	if !strings.Contains(a.Status(), "Blocked") {
		t.Fatalf("status = %q, want a blocked-link message", a.Status())
	}
}

func TestDocPreviewRelativeLinkOpensTab(t *testing.T) {
	a, ps := openTestProject(t, "Docs Relative Link")
	tab := openDocForTest(t, ps, "docs/guide.md", "See [Other](other%20page.md#details) now.\n")
	openDocForTest(t, ps, "docs/other page.md", "# Other\n\n## Details\n\nText.\n")
	ps.activateTab(tab)
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Other"); err != nil {
		t.Fatalf("click relative link: %v", err)
	}
	tt.Frame()
	active := ps.activeTab()
	if active == nil || active.DocRel != "docs/other page.md" {
		t.Fatalf("active doc = %+v", active)
	}
	if got := active.DocEditor.CursorPos(); got.Line != 2 {
		t.Fatalf("cursor line = %d, want 2", got.Line)
	}
}

func TestDocBareHTMLIsShownAsTextNotExecuted(t *testing.T) {
	a, ps := openTestProject(t, "Docs HTML")
	tab := openDocForTest(t, ps, "docs/html.md", "<script>alert('x')</script>\n")
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("script") {
		t.Fatalf("HTML source should be visible as text; texts=%v", tt.Texts())
	}
	if len(tt.OpenedURLs()) != 0 {
		t.Fatalf("HTML content opened a URL: %v", tt.OpenedURLs())
	}
}

func TestDocDirtySyncAndCmdSSaves(t *testing.T) {
	a, ps := openTestProject(t, "Docs Save")
	tab := openDocForTest(t, ps, "docs/guide.md", docGuideSrc)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if tab.Dirty {
		t.Fatal("fresh doc should be clean")
	}
	tab.DocEditor.Select(0, 0)
	tab.DocEditor.Insert("# Changed\n\n")
	tt.Frame()
	if !tab.Dirty {
		t.Fatal("editing the buffer should mark the tab dirty")
	}
	// The document shortcut is scoped to the document area, so give the
	// editor the focus as a user would ("Save" and ⌘S act on the doc pane).
	tt.ClickAt(500, 300)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()
	if tab.Dirty {
		t.Fatal("Cmd+S should clear the dirty flag")
	}
	disk, _, err := ps.project.Store.Read("docs/guide.md")
	if err != nil || !strings.HasPrefix(string(disk), "# Changed") {
		t.Fatalf("saved content = %q, %v", disk, err)
	}
}

func TestDocCRLFOpensClean(t *testing.T) {
	a, ps := openTestProject(t, "Docs CRLF")
	tab := openDocForTest(t, ps, "docs/crlf.md", "# One\r\n\r\nText.\r\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if tab.Dirty {
		t.Fatal("the editor normalizes CRLF, but a freshly opened document must stay clean")
	}
	if got := tab.DocEditor.Buffer.String(); got != "# One\n\nText.\n" {
		t.Fatalf("buffer = %q", got)
	}
}

func TestDocImportInsertsReferenceAndNeverOverwrites(t *testing.T) {
	a, ps := openTestProject(t, "Docs Import")
	tab := openDocForTest(t, ps, "docs/guide.md", docGuideSrc)
	srcDir := t.TempDir()
	src := filepath.Join(srcDir, "My Pic.png")
	if err := os.WriteFile(src, []byte("PNG"), 0o600); err != nil {
		t.Fatal(err)
	}
	a.importDocAsset(tab, src, "docs/assets")
	if !strings.Contains(tab.DocEditor.Buffer.String(), "![My Pic](assets/My%20Pic.png)") {
		t.Fatalf("buffer = %q", tab.DocEditor.Buffer.String())
	}
	if _, _, err := ps.project.Store.Read("docs/assets/My Pic.png"); err != nil {
		t.Fatalf("imported asset missing: %v", err)
	}
	a.importDocAsset(tab, src, "docs/assets")
	if !strings.Contains(tab.DocEditor.Buffer.String(), "assets/My%20Pic-2.png") {
		t.Fatalf("second import should use a suffixed name: %q", tab.DocEditor.Buffer.String())
	}
	if b, _, _ := ps.project.Store.Read("docs/assets/My Pic.png"); string(b) != "PNG" {
		t.Fatalf("first import was overwritten: %q", b)
	}
}

func TestDocFormattingHelpers(t *testing.T) {
	_, ps := openTestProject(t, "Docs Format")
	tab := openDocForTest(t, ps, "docs/guide.md", "word\n")
	tab.DocEditor.Select(0, 4)
	wrapDocSelection(tab, "**", "**")
	if got := tab.DocEditor.Buffer.String(); got != "**word**\n" {
		t.Fatalf("bold result = %q", got)
	}
	tab.DocEditor.Select(0, len("**word**"))
	insertDocLink(tab)
	if got := tab.DocEditor.Buffer.String(); !strings.HasPrefix(got, "[**word**](url)") {
		t.Fatalf("link result = %q", got)
	}
	if sel := tab.DocEditor.Selection(); tab.DocEditor.Buffer.Slice(sel.Start(), sel.End()) != "url" {
		t.Fatalf("link placeholder not selected: %q", tab.DocEditor.Buffer.Slice(sel.Start(), sel.End()))
	}
	insertDocTemplate(tab, "```\n", "\n```\n")
	if !strings.Contains(tab.DocEditor.Buffer.String(), "```\n") {
		t.Fatalf("template result = %q", tab.DocEditor.Buffer.String())
	}
}

func TestDocProblemsPanelLocatesMissingAttachment(t *testing.T) {
	a, ps := openTestProject(t, "Docs Problems")
	tab := openDocForTest(t, ps, "docs/broken.md", "# Guide\n\n![x](assets/nope.png)\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("Problems (1)") {
		t.Fatalf("problems count missing; texts=%v", tt.Texts())
	}
	if err := tt.Click("Problems (1)"); err != nil {
		t.Fatalf("open problems: %v", err)
	}
	tt.Frame()
	label := "3:1  Missing attachment: assets/nope.png"
	if !tt.HasText(label) {
		t.Fatalf("diagnostic row missing; texts=%v", tt.Texts())
	}
	if err := tt.Click(label); err != nil {
		t.Fatalf("click diagnostic: %v", err)
	}
	tt.Frame()
	if got := tab.DocEditor.CursorPos(); got.Line != 2 {
		t.Fatalf("cursor line = %d, want 2", got.Line)
	}
}

func TestDocRemoteImageIsNotFetchedOnOpen(t *testing.T) {
	a, ps := openTestProject(t, "Docs Remote Image")
	tab := openDocForTest(t, ps, "docs/remote.md", "![r](https://example.com/x.png)\n")
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if len(tt.OpenedURLs()) != 0 {
		t.Fatalf("a remote image opened a URL: %v", tt.OpenedURLs())
	}
	// The alt text stays visible as the placeholder.
	if !tt.HasText("r") {
		t.Fatalf("image placeholder missing; texts=%v", tt.Texts())
	}
}

func TestDocViewStateIsPerTab(t *testing.T) {
	a, ps := openTestProject(t, "Docs State")
	one := openDocForTest(t, ps, "docs/one.md", "# One\n\n## A\n")
	two := openDocForTest(t, ps, "docs/two.md", "# Two\n\n## B\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	// Open the outline on tab two only.
	if err := tt.Click("Outline"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("H2 B") {
		t.Fatalf("tab two outline missing: %v", tt.Texts())
	}
	ps.activateTab(one)
	tt.Frame()
	if tt.HasText("H2 B") {
		t.Fatalf("outline state leaked into another tab: %v", tt.Texts())
	}
	ps.activateTab(two)
	tt.Frame()
	if !tt.HasText("H2 B") {
		t.Fatalf("tab two lost its outline state: %v", tt.Texts())
	}
}
