package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

// caretPainted reports whether the focused canvas editor painted its caret
// near the input method's caret. The caret is the canvas theme's accent, an
// opaque red-orange; the check looks for that color in the few pixels where
// the editor draws the bar, so it tests the real focus and paint path rather
// than a stand-in cursor.
func caretPainted(tt *ui.Tester) bool {
	caret, ok := tt.TextCaret()
	if !ok {
		return false
	}
	img := tt.Image()
	cx, cy := int(caret.X), int(caret.Y)
	for y := cy; y < cy+int(caret.H); y++ {
		for x := cx - 3; x <= cx+3; x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			c := img.RGBAAt(x, y)
			if c.R > 150 && int(c.R)-int(c.G) > 80 && int(c.R)-int(c.B) > 80 {
				return true
			}
		}
	}
	return false
}

func TestDocCanvasCaretPaintedAfterClick(t *testing.T) {
	a, ps := openTestProject(t, "Caret Click")
	src := "one\n\ntwo\n"
	openDocForTest(t, ps, "docs/caret.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !caretPainted(tt) {
		t.Fatalf("no caret after clicking a block; texts=%v", tt.Texts())
	}
}

func TestDocCanvasCaretPaintedInDarkTheme(t *testing.T) {
	a, ps := openTestProject(t, "Caret Dark")
	src := "one\n"
	openDocForTest(t, ps, "docs/dark.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.SetDark(true)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !caretPainted(tt) {
		t.Fatalf("no caret in the dark theme; texts=%v", tt.Texts())
	}
}

func TestDocCanvasCaretPaintedAfterBlockSwitch(t *testing.T) {
	a, ps := openTestProject(t, "Caret Switch")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/switch.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("A")
	tt.Frame()
	// The first block grew, so the second block now starts later.
	cur := tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !caretPainted(tt) {
		t.Fatalf("no caret after switching blocks; texts=%v", tt.Texts())
	}
	if got := tab.DocEditor.Buffer.String(); got != "oneA\n\ntwo\n" {
		t.Fatalf("switching blocks lost the edit: %q", got)
	}
}

func TestDocCanvasCaretPaintedOnNewLine(t *testing.T) {
	a, ps := openTestProject(t, "Caret Newline")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/newline.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !caretPainted(tt) {
		t.Fatalf("no caret on the new line; texts=%v", tt.Texts())
	}
	tt.Type("x")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); !strings.Contains(got, "\nx\n") {
		t.Fatalf("typing on the new line = %q", got)
	}
}

func TestDocCanvasSlashOpensOnNewLineWithoutLeavingTheBlock(t *testing.T) {
	a, ps := openTestProject(t, "Slash Newline")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/slashnew.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	// The first character of the new line opens the menu at once, before any
	// query is typed and without leaving the block.
	tt.Type("/")
	tt.Frame()
	if !tt.HasText("Insert block") {
		t.Fatalf("slash menu did not open on the new line; texts=%v", tt.Texts())
	}
	if !tt.HasText("Heading 2") || !tt.HasText("Table") || !tt.HasText("Divider") {
		t.Fatalf("menu entries missing; texts=%v", tt.Texts())
	}
	for _, entry := range []string{"Heading 1", "Heading 3", "Text", "Bulleted list", "Quote", "Code block"} {
		if !tt.HasText(entry) {
			t.Fatalf("menu entry %q missing; texts=%v", entry, tt.Texts())
		}
	}
	tt.Type("h2")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "one\n## \n\ntwo\n" {
		t.Fatalf("slash on the new line = %q, want %q", got, "one\n## \n\ntwo\n")
	}
	if !caretPainted(tt) {
		t.Fatalf("the caret is not visible after the conversion; texts=%v", tt.Texts())
	}
	// The caret sits after the heading marker, so typing continues the
	// heading rather than the old query.
	tt.Type("T")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "one\n## T\n\ntwo\n" {
		t.Fatalf("typing after the conversion = %q", got)
	}
}

func TestDocCanvasSlashOnLaterLineOfOneBlock(t *testing.T) {
	a, ps := openTestProject(t, "Slash Later Line")
	src := "first\n/h2 Hello\n"
	tab := openDocForTest(t, ps, "docs/later.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	// Move to the second line of the same paragraph: the menu judges the
	// caret's line, not the block's first line.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	if !tt.HasText("Insert block") {
		t.Fatalf("menu missing on the later line; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "first\n## Hello\n" {
		t.Fatalf("later-line conversion = %q, want %q", got, "first\n## Hello\n")
	}
	// The first line stays byte-identical.
	if !strings.HasPrefix(tab.DocEditor.Buffer.String(), "first\n") {
		t.Fatalf("the first line changed: %q", tab.DocEditor.Buffer.String())
	}
}

func TestDocCanvasSlashEmptyDocumentAndTable(t *testing.T) {
	a, ps := openTestProject(t, "Slash Empty Table")
	tab := openDocForTest(t, ps, "docs/table.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	if !tt.HasText("Insert block") {
		t.Fatalf("menu missing in an empty document; texts=%v", tt.Texts())
	}
	if !caretPainted(tt) {
		t.Fatalf("no caret in an empty document; texts=%v", tt.Texts())
	}
	tt.Type("table")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.HasPrefix(got, "| Column | Column |\n| --- | --- |") {
		t.Fatalf("table command = %q", got)
	}
	// Leaving the block renders the generated table through renderDocTable.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if !tt.HasText("Table cell 1,1") {
		t.Fatalf("generated table not rendered; texts=%v", tt.Texts())
	}
}

func TestDocCanvasSlashMouseChoose(t *testing.T) {
	a, ps := openTestProject(t, "Slash Mouse")
	tab := openDocForTest(t, ps, "docs/mouse.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	// Wait for the real menu to finish its enter transition using the tester clock.
	tt.Advance(400 * time.Millisecond)
	if err := tt.Click("Heading 2"); err != nil {
		t.Fatalf("click menu row: %v", err)
	}
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## \n" {
		t.Fatalf("mouse choose = %q, want %q", got, "## \n")
	}
	if !caretPainted(tt) {
		t.Fatalf("the caret is not visible after a mouse choose; texts=%v", tt.Texts())
	}
}

func TestDocCanvasSlashDoesNotOpenInMiddleOfLine(t *testing.T) {
	a, ps := openTestProject(t, "Slash Middle")
	src := "a/b\n"
	openDocForTest(t, ps, "docs/middle.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	if tt.HasText("Insert block") {
		t.Fatalf("slash opened after a non-first character; texts=%v", tt.Texts())
	}
	// A URL line does not start with a slash either.
	urlTab := openDocForTest(t, ps, "docs/url.md", "https://example.com/a\n")
	ps.activateTab(urlTab)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, "https://example.com/a\n", 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	if tt.HasText("Insert block") {
		t.Fatalf("slash opened in a URL; texts=%v", tt.Texts())
	}
}

func TestDocCanvasSlashCompositionKeepsFocusAndDoesNotChoose(t *testing.T) {
	a, ps := openTestProject(t, "Slash IME")
	tab := openDocForTest(t, ps, "docs/ime.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	if !tt.HasText("Insert block") {
		t.Fatalf("menu missing; texts=%v", tt.Texts())
	}
	// Composing must not take the focus from the editor or choose an entry.
	tt.Compose("中文", 2)
	tt.Frame()
	active, _ := tt.TextInputActive()
	if !active {
		t.Fatal("composition took the focus from the editor")
	}
	if !caretPainted(tt) {
		t.Fatalf("the caret is not visible while composing; texts=%v", tt.Texts())
	}
	tt.Type("中文")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); !strings.Contains(got, "中文") {
		t.Fatalf("composition not committed: %q", got)
	}
}

func TestDocCanvasSlashSaveAndUndo(t *testing.T) {
	a, ps := openTestProject(t, "Slash Save")
	tab := openDocForTest(t, ps, "docs/slashsave.md", "")
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
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()
	disk, _, err := ps.project.Store.Read("docs/slashsave.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != "## Hi\n" {
		t.Fatalf("saved = %q", disk)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "/h2 Hi" {
		t.Fatalf("undo after save = %q", got)
	}
}

func TestDocCanvasSlashChineseFilter(t *testing.T) {
	a, ps := openTestProject(t, "Slash Chinese Filter")
	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	openDocForTest(t, ps, "docs/zhfilter.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("输入 / 插入内容，输入 @ 引用接口"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/标题")
	tt.Frame()
	if !tt.HasText("标题 2") {
		t.Fatalf("Chinese query did not find Heading 2; texts=%v", tt.Texts())
	}
	if tt.HasText("表格") {
		t.Fatalf("Chinese query did not filter; texts=%v", tt.Texts())
	}
}

func TestDocCanvasSlashMenuBelowCaret(t *testing.T) {
	a, ps := openTestProject(t, "Slash Placement")
	openDocForTest(t, ps, "docs/place.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	caret, ok := tt.TextCaret()
	if !ok {
		t.Fatal("no caret")
	}
	menu, ok := tt.Find("Insert block")
	if !ok {
		t.Fatalf("menu missing; texts=%v", tt.Texts())
	}
	if menu.Y < caret.Y+caret.H-1 {
		t.Fatalf("menu at y=%v covers the caret at y=%v h=%v", menu.Y, caret.Y, caret.H)
	}
}

func TestDocCanvasSlashEscapeKeepsCaret(t *testing.T) {
	a, ps := openTestProject(t, "Slash Escape Caret")
	tab := openDocForTest(t, ps, "docs/esccaret.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/h2")
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Insert block") {
		t.Fatalf("Escape did not close the menu; texts=%v", tt.Texts())
	}
	if got := tab.DocEditor.Buffer.String(); got != "/h2" {
		t.Fatalf("Escape changed the block: %q", got)
	}
	if !caretPainted(tt) {
		t.Fatalf("the caret is not visible after Escape; texts=%v", tt.Texts())
	}
}

func TestDocCanvasSlashKeepsBlockAfterConversion(t *testing.T) {
	a, ps := openTestProject(t, "Slash Keep Block")
	src := "before\n\nafter\n"
	tab := openDocForTest(t, ps, "docs/keep.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type(" /h2")
	tt.Frame()
	// A slash in the middle of the line is text, not a command.
	if tt.HasText("Insert block") {
		t.Fatalf("slash opened mid-line; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); !strings.Contains(got, "before /h2") {
		t.Fatalf("mid-line slash text lost: %q", got)
	}
}

func TestDocCanvasSlashArrowKeysChoose(t *testing.T) {
	a, ps := openTestProject(t, "Slash Arrows")
	tab := openDocForTest(t, ps, "docs/arrows.md", "")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/")
	tt.Frame()
	// The first entry is a paragraph; Down twice moves to Heading 2.
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "## \n" {
		t.Fatalf("arrow-key choose = %q, want %q", got, "## \n")
	}
}

func TestDocCanvasSlashOnListLine(t *testing.T) {
	a, ps := openTestProject(t, "Slash List")
	src := "- /h2\n- two\n"
	tab := openDocForTest(t, ps, "docs/list.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	r, ok := tt.Find(canvasBlockLabel(t, src, 0))
	if !ok {
		t.Fatalf("list block missing; texts=%v", tt.Texts())
	}
	// Click the first line, where the slash query is.
	tt.ClickAt(r.X+8, r.Y+8)
	tt.Frame()
	if !tt.HasText("Insert block") {
		t.Fatalf("menu missing on a list line; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.HasPrefix(got, "## \n") || !strings.Contains(got, "- two") {
		t.Fatalf("list-line conversion = %q", got)
	}
}

func TestDocCanvasSelectionPainted(t *testing.T) {
	a, ps := openTestProject(t, "Selection Paint")
	src := "one two\n"
	openDocForTest(t, ps, "docs/sel.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	if selectionPainted(tt) {
		t.Fatal("the accent fill exists before any selection")
	}
	for i := 0; i < 3; i++ {
		tt.Key(ui.Shift, ui.KeyRight)
		tt.Frame()
	}
	if !selectionPainted(tt) {
		t.Fatalf("selection not painted; texts=%v", tt.Texts())
	}
}

// selectionPainted reports whether the editor shows the accent at the
// selection's alpha, the pink it fills a selection with, on the line left of
// the caret: where Shift+Right leaves the text just selected.
func selectionPainted(tt *ui.Tester) bool {
	caret, ok := tt.TextCaret()
	if !ok {
		return false
	}
	img := tt.Image()
	x0, x1 := int(caret.X)-80, int(caret.X)+3
	y0, y1 := int(caret.Y), int(caret.Y)+int(caret.H)
	for y := y0; y < y1; y++ {
		for x := x0; x <= x1; x++ {
			if x < 0 || y < 0 || x >= img.Bounds().Dx() || y >= img.Bounds().Dy() {
				continue
			}
			c := img.RGBAAt(x, y)
			if c.R > 235 && c.G > 180 && c.G < 230 && c.B > 180 && c.B < 230 &&
				int(c.R)-int(c.G) > 18 && int(c.R)-int(c.B) > 18 {
				return true
			}
		}
	}
	return false
}

func TestDocCanvasCaretPaintedWhileScrolling(t *testing.T) {
	a, ps := openTestProject(t, "Caret Scroll")
	var b strings.Builder
	for i := 0; i < 40; i++ {
		b.WriteString("block ")
		b.WriteByte(byte('a' + i%26))
		b.WriteString("\n\n")
	}
	src := b.String()
	tab := openDocForTest(t, ps, "docs/scroll.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 2)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Wheel(600, 400, 0, 10)
	tt.Frame()
	if !caretPainted(tt) {
		t.Fatalf("the caret is not visible after scrolling; texts=%v", tt.Texts())
	}
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("scrolling changed the document: %q", got)
	}
}
