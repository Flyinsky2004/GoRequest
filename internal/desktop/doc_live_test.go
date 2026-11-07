package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/docedit"
)

func canvasBlockLabel(t *testing.T, src string, index int) string {
	t.Helper()
	blocks := docedit.Parse(src)
	if index < 0 || index >= len(blocks) {
		t.Fatalf("block %d of %d", index, len(blocks))
	}
	return docBlockLabel(blocks[index])
}

func TestDocCanvasIsDefaultAndEditsBlockInPlace(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Edit")
	src := "# Title\n\nBody text.\n\n- one\n- two\n"
	tab := openDocForTest(t, ps, "docs/canvas.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if tab.Section != docSectionCanvas {
		t.Fatalf("new document Section = %d, want canvas", tab.Section)
	}
	if !tt.HasText("Title") || !tt.HasText("Body text.") || !tt.HasText("one") {
		t.Fatalf("rendered blocks missing; texts=%v", tt.Texts())
	}
	if err := tt.Click(canvasBlockLabel(t, src, 1)); err != nil {
		t.Fatalf("click paragraph: %v", err)
	}
	tt.Frame()
	tt.Type(" more")
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.Contains(got, "Body text. more") {
		t.Fatalf("in-place edit not committed: %q", got)
	}
	if !strings.HasPrefix(got, "# Title\n") || !strings.Contains(got, "- one\n- two\n") {
		t.Fatalf("untouched blocks changed: %q", got)
	}
}

func TestDocCanvasSlashConvertsBlockInOneUndoStep(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Slash")
	tab := openDocForTest(t, ps, "docs/slash.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatalf("click empty hint: %v", err)
	}
	tt.Frame()
	tt.Type("/h2 Hello")
	tt.Frame()
	if !tt.HasText("Heading 2") {
		t.Fatalf("slash menu missing; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## Hello\n" {
		t.Fatalf("slash conversion = %q, want %q", got, "## Hello\n")
	}
	// One undo takes the whole conversion back to the query; a second takes
	// back the typing that made the block.
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "/h2 Hello" {
		t.Fatalf("undo after slash = %q, want the query", got)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "" {
		t.Fatalf("second undo = %q, want empty", got)
	}
}

func TestDocCanvasUndoIsTheDocumentHistory(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Undo")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("X")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "oneX\n\ntwo\n" {
		t.Fatalf("typed = %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "one\n\ntwo\n" {
		t.Fatalf("undo = %q", got)
	}
}

func TestDocCanvasModeSwitchFlushesPendingEdit(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Flush")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/flush.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("X")
	// Switch to source without an intervening frame: the mode switch must
	// flush the pending block edit first.
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.Section != 1 {
		t.Fatalf("Section = %d, want source", tab.Section)
	}
	if got := tab.DocEditor.Buffer.String(); got != "oneX\n\ntwo\n" {
		t.Fatalf("mode switch lost the edit: %q", got)
	}
}

func TestDocCanvasMergeBlocks(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Merge")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/merge.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "onetwo\n" {
		t.Fatalf("merge = %q, want %q", got, "onetwo\n")
	}
}

func TestDocCanvasUnknownBlockIsSourceFallback(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Source Block")
	src := "<div>raw html</div>\n\npara\n"
	tab := openDocForTest(t, ps, "docs/html.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("Source block") {
		t.Fatalf("source fallback missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("<div>raw html</div>") {
		t.Fatalf("raw source missing; texts=%v", tt.Texts())
	}
	// Editing the fallback edits the raw source, not a rewritten block.
	if err := tt.Click(canvasBlockLabel(t, src, 0) + ":source"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("!")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); !strings.Contains(got, "<div>raw html</div>!") {
		t.Fatalf("source edit = %q", got)
	}
}

func TestDocCanvasSlashRequestPickerInsertsStableLink(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Request")
	entry := addRequestForTest(t, ps, "auth", "用户登录")
	tab := openDocForTest(t, ps, "docs/req.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/request")
	tt.Frame()
	tt.Key(0, ui.KeyEnter) // choose the request entry
	tt.Frame()
	if !tt.HasText("用户登录") {
		t.Fatalf("request picker missing; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter) // choose the request
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	want := "(gorequest://request/" + entry.Request.ID + ")"
	if !strings.Contains(got, want) {
		t.Fatalf("request link = %q, want %q", got, want)
	}
	if !strings.Contains(got, "@") {
		t.Fatalf("visible @ label missing: %q", got)
	}
}

func TestDocCanvasTableHookReplacesBlockWithOneEdit(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Table Hook")
	src := "| A | B |\n| --- | --- |\n| c | d |\n\npara\n"
	tab := openDocForTest(t, ps, "docs/table.md", src)
	old := docTableRenderer
	defer func() { docTableRenderer = old }()
	called := false
	docTableRenderer = func(c *ui.Context, th *ui.Theme, root ui.Element, source string, replace func(string)) bool {
		called = true
		if !strings.HasPrefix(source, "| A | B |") {
			t.Errorf("table source = %q", source)
		}
		replace("| A | B |\n| --- | --- |\n| c | d |\n| e | f |\n")
		return true
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !called {
		t.Fatal("table hook not called")
	}
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.Contains(got, "| e | f |") {
		t.Fatalf("table replacement = %q", got)
	}
	if !strings.Contains(got, "para\n") {
		t.Fatalf("following block changed: %q", got)
	}
	// The replacement is one undo step.
	tab.DocEditor.Buffer.Undo()
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("undo of table edit = %q, want %q", got, src)
	}
}

func TestDocCanvasCompositionCommitsChineseText(t *testing.T) {
	a, ps := openTestProject(t, "Canvas IME")
	src := "hello\n"
	tab := openDocForTest(t, ps, "docs/ime.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Compose("中文", 2)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); !strings.Contains(got, "中文") {
		t.Fatalf("composition not committed: %q", got)
	}
}

// addRequestForTest adds a request to the project and its in-memory index.
func addRequestForTest(t *testing.T, ps *projectState, folder, name string) *core.RequestEntry {
	t.Helper()
	req := &core.Request{
		SchemaVersion: core.SupportedSchemaVersion,
		ID:            folder + ".login",
		Name:          name,
		HTTP:          &core.HTTPDef{Method: "GET", URL: "https://example.com/" + folder},
	}
	entry := &core.RequestEntry{Request: req, RelPath: "collections/" + folder + "/" + name + ".yaml"}
	ps.project.Requests[req.ID] = entry
	return entry
}

func TestDocCanvasSaveFlushesPendingKeystroke(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Save")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/save.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("X")
	// Save without an intervening frame: the shortcut must flush first.
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()
	disk, _, err := ps.project.Store.Read("docs/save.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != "oneX\n\ntwo\n" {
		t.Fatalf("saved = %q, want the pending keystroke", disk)
	}
	if tab.Dirty {
		t.Fatal("save should clear dirty")
	}
}

func TestDocCanvasEnterSplitsAndKeepsTypingInNewBlock(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Split")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/split.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Type("new")
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.Contains(got, "new") || !strings.Contains(got, "two\n") {
		t.Fatalf("split/typing = %q", got)
	}
	if !strings.HasPrefix(got, "one\n") {
		t.Fatalf("first block changed: %q", got)
	}
}

func TestDocCanvasDocToDocSwitchFlushesEachTab(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Tabs")
	one := openDocForTest(t, ps, "docs/one.md", "one\n")
	two := openDocForTest(t, ps, "docs/two.md", "two\n")
	ps.activateTab(one)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, "one\n", 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("A")
	// Switch to the other document without a frame; the state prune flushes.
	ps.activateTab(two)
	tt.Frame()
	if got := one.DocEditor.Buffer.String(); got != "oneA\n" {
		t.Fatalf("first tab lost its edit: %q", got)
	}
	if got := two.DocEditor.Buffer.String(); got != "two\n" {
		t.Fatalf("second tab changed: %q", got)
	}
}

func TestDocCanvasSlashDoesNotOpenInCodeOrPath(t *testing.T) {
	a, ps := openTestProject(t, "Canvas No Slash")
	src := "```\n/usr/local\n```\n\n/usr/local\n"
	openDocForTest(t, ps, "docs/path.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	if tt.HasText("Insert block") {
		t.Fatalf("slash opened in a code block; texts=%v", tt.Texts())
	}
	// A paragraph that is a path is not a slash query either.
	pathTab := openDocForTest(t, ps, "docs/path2.md", "/usr/local\n")
	ps.activateTab(pathTab)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, "/usr/local\n", 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	if tt.HasText("Insert block") {
		t.Fatalf("slash opened in a path; texts=%v", tt.Texts())
	}
}

func TestDocCanvasChineseLabels(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Chinese")
	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	openDocForTest(t, ps, "docs/zh.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("输入 / 插入内容，输入 @ 引用接口") {
		t.Fatalf("Chinese empty hint missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("文档") {
		t.Fatalf("Chinese canvas segment missing; texts=%v", tt.Texts())
	}
	if err := tt.Click("输入 / 插入内容，输入 @ 引用接口"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/h2")
	tt.Frame()
	if !tt.HasText("标题 2") {
		t.Fatalf("Chinese slash label missing; texts=%v", tt.Texts())
	}
}

func TestDocCanvasDirectiveCardIsReadOnly(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Card")
	entry := addRequestForTest(t, ps, "auth", "登录")
	src := "<!-- gorequest:request id=\"" + entry.Request.ID + "\" -->\n\n<!-- gorequest:begin -->\nold derived\n<!-- gorequest:end -->\n\npara\n"
	tab := openDocForTest(t, ps, "docs/card.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("Request card") {
		t.Fatalf("card label missing; texts=%v", tt.Texts())
	}
	if !tt.HasText("para") {
		t.Fatalf("following block missing; texts=%v", tt.Texts())
	}
	// The derived text is not a separate editable block.
	if tt.HasText("doc-block:paragraph:70") {
		t.Fatalf("derived content drawn as an editable block; texts=%v", tt.Texts())
	}
	// Editing the card edits the directive line only.
	if err := tt.Click("doc-block:html:0:card"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type(" ")
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.HasPrefix(got, "<!-- gorequest:request") || !strings.Contains(got, "old derived") {
		t.Fatalf("card edit changed the derived text: %q", got)
	}
}

func TestDocCanvasRendersLightDarkAndTwoSizes(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Render")
	src := "# Title\n\nBody **bold**.\n\n- one\n- two\n\n| A | B |\n| --- | --- |\n| c | d |\n"
	openDocForTest(t, ps, "docs/render.md", src)
	for _, dark := range []bool{false, true} {
		for _, size := range [][2]int{{1200, 820}, {1024, 640}} {
			tt := ui.NewTester(a.View, size[0], size[1])
			tt.SetDark(dark)
			tt.Frame()
			if !tt.HasText("Title") || !tt.HasText("one") || !tt.HasText("c") {
				t.Fatalf("dark=%v size=%v missing blocks; texts=%v", dark, size, tt.Texts())
			}
		}
	}
}

func TestDocCanvasRequestLinkOpensExistingTabWithoutSending(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Link")
	entry := addRequestForTest(t, ps, "auth", "登录")
	src := "[@auth/login](gorequest://request/" + entry.Request.ID + ")\n"
	openDocForTest(t, ps, "docs/link.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("@auth/login") {
		t.Fatalf("link label missing; texts=%v", tt.Texts())
	}
	r, ok := tt.Find("@auth/login")
	if !ok {
		t.Fatalf("link label missing; texts=%v", tt.Texts())
	}
	// The label covers the whole paragraph; the link text is at its start.
	tt.ClickAt(r.X+8, r.Y+r.H/2)
	tt.Frame()
	active := ps.activeTab()
	if active == nil || active.Kind != "request" {
		t.Fatalf("link did not open the request tab: %+v", active)
	}
	if active.Sending || active.Resp != nil {
		t.Fatal("opening a reference must not send the request")
	}
	// Opening again focuses the existing tab instead of duplicating it.
	before := len(ps.tabs)
	ps.openRequest(entry)
	if len(ps.tabs) != before {
		t.Fatalf("request tab duplicated: %d -> %d", before, len(ps.tabs))
	}
}

func TestDocCanvasSlashRedo(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Redo")
	tab := openDocForTest(t, ps, "docs/redo.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/h2 Hi")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## Hi\n" {
		t.Fatalf("conversion = %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "/h2 Hi" {
		t.Fatalf("undo = %q", got)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## Hi\n" {
		t.Fatalf("redo = %q", got)
	}
}

func TestDocSourceCmdClickOpensReference(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Source Link")
	entry := addRequestForTest(t, ps, "auth", "登录")
	src := "see [@auth/login](gorequest://request/" + entry.Request.ID + ")\n"
	tab := openDocForTest(t, ps, "docs/srclink.md", src)
	tab.Section = 1
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	off := strings.Index(src, "@auth/login")
	a.docDefinitionAt(tab, off)
	tt.Frame()
	if active := ps.activeTab(); active == nil || active.Kind != "request" {
		t.Fatalf("source Cmd+click did not open the request: %+v", active)
	}
}

func TestDocCanvasModeRoundTripDoesNotRewriteSource(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Round Trip")
	src := "# T\n\n- a\n  - b\n\n> q\n\n```go\nx\n```\n\n| A | B |\n|---|---|\n| c | d |\n\n<div>x</div>\n"
	tab := openDocForTest(t, ps, "docs/round.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("Canvas"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("mode round trip rewrote the source:\n got %q\nwant %q", got, src)
	}
	if tab.Dirty {
		t.Fatal("a mode round trip must not dirty the document")
	}
}

func TestDocCanvasSlashKeepsTextAndSelection(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Slash Selection")
	tab := openDocForTest(t, ps, "docs/sel.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/h2 Hello")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## Hello\n" {
		t.Fatalf("conversion = %q", got)
	}
	// The command keeps "Hello" selected, so typing replaces it.
	tt.Type("World")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## World\n" {
		t.Fatalf("selection not kept: %q", got)
	}
}

func TestDocCanvasSlashEscapeCancels(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Slash Escape")
	tab := openDocForTest(t, ps, "docs/esc.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/h2")
	tt.Frame()
	if !tt.HasText("Heading 2") {
		t.Fatalf("menu missing; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Heading 2") {
		t.Fatalf("Escape did not close the menu; texts=%v", tt.Texts())
	}
	if got := tab.DocEditor.Buffer.String(); got != "/h2" {
		t.Fatalf("Escape changed the block: %q", got)
	}
}

func TestDocCanvasSlashRequestPickerReportsInvalidIDs(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Invalid ID")
	ps.project.Requests["bad.中文"] = &core.RequestEntry{
		Request: &core.Request{SchemaVersion: core.SupportedSchemaVersion, ID: "bad.中文", Name: "中文接口", HTTP: &core.HTTPDef{Method: "GET", URL: "https://example.com"}},
		RelPath: "collections/bad/中文接口.yaml",
	}
	openDocForTest(t, ps, "docs/bad.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/request")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("Some requests have IDs that documents cannot reference") {
		t.Fatalf("invalid-ID note missing; texts=%v", tt.Texts())
	}
}

func TestDocCanvasTableHookOwnsClicks(t *testing.T) {
	a, ps := openTestProject(t, "Canvas Table Click")
	src := "| A | B |\n| --- | --- |\n| c | d |\n"
	openDocForTest(t, ps, "docs/tableclick.md", src)
	old := docTableRenderer
	defer func() { docTableRenderer = old }()
	docTableRenderer = func(c *ui.Context, th *ui.Theme, root ui.Element, source string, replace func(string)) bool {
		ui.Text(c, "smart table").FontSize(12)
		return true
	}
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("smart table"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("doc-canvas-editor") {
		t.Fatalf("clicking a handled table opened the raw source editor; texts=%v", tt.Texts())
	}
}
