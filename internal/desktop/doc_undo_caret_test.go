package desktop

import (
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestM04MiddleDeleteCaret(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 Undo")
	src := "甲乙丙丁戊\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyHome)
	for i := 0; i < 3; i++ {
		tt.Key(0, ui.KeyRight)
	}
	tt.Frame()
	be := observed[tab].live.block
	before := be.ed.Selection()
	caret := be.rich.caret(3)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "甲乙丁戊\n" {
		t.Fatalf("delete=%q", got)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("undo=%q", got)
	}
	if got := be.ed.Selection(); got != before {
		t.Fatalf("undo selection=%+v want %+v; caret=%+v want %+v", got, before, be.rich.caret(0), caret)
	}
	a1, b1 := be.rich.selection()
	if a1 != 3 || b1 != 3 || be.rich.caret(b1) != caret {
		t.Fatalf("visible caret=(%d,%d) %+v want %+v", a1, b1, be.rich.caret(b1), caret)
	}
}

func TestM04CaretMatrix(t *testing.T) {
	cases := []struct {
		name, src    string
		anchor, head int
		key          ui.Key
		text         string
	}{
		{"english-backspace", "abcde\n", 3, 3, ui.KeyBackspace, ""},
		{"chinese-backspace", "甲乙丙丁戊\n", 3, 3, ui.KeyBackspace, ""},
		{"emoji-backspace", "甲😀乙丁\n", 3, 3, ui.KeyBackspace, ""},
		{"english-forward", "abcde\n", 2, 2, ui.KeyDelete, ""},
		{"emoji-forward", "甲😀乙丁\n", 1, 1, ui.KeyDelete, ""},
		{"first", "甲乙丙\n", 1, 1, ui.KeyBackspace, ""},
		{"last", "甲乙丙\n", 3, 3, ui.KeyBackspace, ""},
		{"selected-delete", "甲😀乙丁\n", 1, 4, ui.KeyBackspace, ""},
		{"reverse-delete", "甲😀乙丁\n", 4, 1, ui.KeyDelete, ""},
		{"reverse-replace", "甲😀乙丁\n", 4, 1, 0, "新😀"},
		{"selected-replace", "abcde\n", 1, 4, 0, "中文"},
		{"code-inline", "甲 `abc` 乙\n", 4, 4, ui.KeyBackspace, ""},
		{"strong", "甲 **丙丁** 乙\n", 4, 2, 0, "新😀"},
		{"link", "甲 [丙丁](https://example.com) 乙\n", 3, 3, ui.KeyBackspace, ""},
		{"softbreak", "甲😀\n`乙丁`\n", 4, 3, 0, "X"},
		{"list", "- 甲😀乙丁\n- next\n", 3, 3, ui.KeyBackspace, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			observed := observeDocStates(t)
			a, ps := openTestProject(t, "M04 Matrix")
			tab := openDocForTest(t, ps, "docs/undo.md", tc.src)
			tt := ui.NewTester(a.View, 1200, 820)
			tt.Frame()
			if err := tt.Click(canvasBlockLabel(t, tc.src, 0)); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			be := observed[tab].live.block
			be.rich.selectVisible(tc.anchor, tc.head)
			tt.Frame()
			before := be.ed.Selection()
			beforeCaret, ok := tt.TextCaret()
			if !ok {
				t.Fatal("no caret before edit")
			}
			if tc.key != 0 {
				tt.Key(0, tc.key)
			} else {
				tt.Type(tc.text)
			}
			tt.Frame()
			changed := tab.DocEditor.Buffer.String()
			after := be.ed.Selection()
			if changed == tc.src {
				t.Fatal("edit did not change text")
			}
			afterCaret, ok := tt.TextCaret()
			if !ok {
				t.Fatal("no caret after edit")
			}
			for _, mods := range []ui.Modifiers{ui.Cmd, ui.Ctrl} {
				tt.Key(mods, ui.KeyZ)
				for frame := 0; frame < 3; frame++ {
					tt.Frame()
					be = observed[tab].live.block
					if be == nil || tab.DocEditor.Buffer.String() != tc.src || be.ed.Selection() != before {
						t.Fatalf("undo frame %d source=%q selection=%+v want %+v", frame, tab.DocEditor.Buffer.String(), be.ed.Selection(), before)
					}
					caret, ok := tt.TextCaret()
					if !caretPainted(tt) {
						t.Fatal("undo caret not painted")
					}
					if !ok || caret != beforeCaret {
						t.Fatalf("undo caret %+v want %+v", caret, beforeCaret)
					}
				}
				tt.Key(mods|ui.Shift, ui.KeyZ)
				tt.Frame()
				tt.Frame()
				be = observed[tab].live.block
				if tab.DocEditor.Buffer.String() != changed || be.ed.Selection() != after {
					t.Fatalf("redo source=%q selection=%+v want %+v", tab.DocEditor.Buffer.String(), be.ed.Selection(), after)
				}
				caret, ok := tt.TextCaret()
				if !ok || caret != afterCaret {
					t.Fatalf("redo caret %+v want %+v", caret, afterCaret)
				}
			}
		})
	}
}

func TestM04HistoryCrossBlockAndBranch(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 History")
	src := "甲乙丙丁\n\nsecond\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	be.rich.selectVisible(3, 3)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	first := tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, first, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be = observed[tab].live.block
	be.rich.selectVisible(2, 2)
	tt.Type("😀")
	tt.Frame()
	second := tab.DocEditor.Buffer.String()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	be = observed[tab].live.block
	if be.start != len("甲乙丁\n\n") || be.ed.Cursor() != 2 {
		t.Fatalf("second block undo: %d %d", be.start, be.ed.Cursor())
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	be = observed[tab].live.block
	if tab.DocEditor.Buffer.String() != src || be.start != 0 || be.ed.Cursor() != 9 {
		t.Fatal("undo did not return to first block deletion")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != second {
		t.Fatal("cross block redo lost history")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	tt.Type("新")
	tt.Frame()
	branch := tab.DocEditor.Buffer.String()
	if tab.DocEditor.Buffer.CanRedo() {
		t.Fatal("new edit did not clear canonical redo")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != branch {
		t.Fatal("stale redo restored old branch")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != first {
		t.Fatal("new branch undo failed")
	}
	// Navigate elsewhere without an edit: undo must follow the transaction target.
	if err := tt.Click(canvasBlockLabel(t, first, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if observed[tab].live.block.start != 0 || observed[tab].live.block.ed.Cursor() != 9 {
		t.Fatal("undo stayed in the clicked block")
	}
}

func TestM04NativeCodeAndComposition(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 Code")
	src := "```go\n甲😀乙丁\n```\n\nafter\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	bounds, ok := tt.Find("doc-canvas-editor")
	if !ok {
		t.Fatal("no code editor")
	}
	tt.ClickAt(bounds.X+4, bounds.Y+2)
	tt.Frame()
	be := observed[tab].live.block
	be.ed.Select(len("甲😀乙"), len("甲"))
	tt.Frame()
	before := be.ed.Selection()
	tt.Type("新😀")
	tt.Frame()
	after := be.ed.Selection()
	changed := tab.DocEditor.Buffer.String()
	tt.Key(ui.Ctrl, ui.KeyZ)
	tt.Frame()
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src || be.ed.Selection() != before {
		t.Fatalf("native undo source=%q selection=%+v want %+v", tab.DocEditor.Buffer.String(), be.ed.Selection(), before)
	}
	tt.Key(ui.Ctrl|ui.Shift, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != changed || be.ed.Selection() != after {
		t.Fatal("native redo selection")
	}
	// Native preedit must not enter canonical history. A confirmed composition
	// retains the selection from the frame before the native overlay began.
	be.ed.Select(len("新😀"), len("新😀"))
	tt.Frame()
	before = be.ed.Selection()
	tt.Compose("zhong", 5)
	tt.Frame()
	tt.Compose("中文", 2)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != changed {
		t.Fatal("preedit entered canonical buffer")
	}
	tt.Compose("", 0)
	tt.Type("中文")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != changed || be.ed.Selection() != before {
		t.Fatalf("composition undo source=%q selection=%+v want %+v", tab.DocEditor.Buffer.String(), be.ed.Selection(), before)
	}
}

func TestM04UnknownTransactionAndMerge(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 External")
	src := "one\n\ntwo\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyHome)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != "onetwo\n" {
		t.Fatal("merge failed")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	be := observed[tab].live.block
	if tab.DocEditor.Buffer.String() != src || be.start != 5 || be.ed.Cursor() != 0 {
		t.Fatalf("merge undo target=%d,%d", be.start, be.ed.Cursor())
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if be.start != 0 || be.ed.Cursor() != 3 {
		t.Fatal("merge redo target")
	}
	// Simulate a source/format/table transaction with no canvas caret metadata.
	tab.DocEditor.Buffer.Edit(editor.Edit{Start: 1, End: 2, Text: "😀"})
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != "onetwo\n" || observed[tab].live.block.ed.Cursor() != 2 {
		t.Fatal("unknown undo did not use changed source range")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != "o😀etwo\n" || observed[tab].live.block.ed.Cursor() != len("o😀") {
		t.Fatal("unknown redo not UTF-8 safe")
	}
}

func TestM04RichCompositionAndFormat(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 IME")
	src := "甲😀乙丁\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	be.rich.selectVisible(4, 1)
	tt.Frame()
	before := be.ed.Selection()
	tt.Compose("zhong", 5)
	tt.Frame()
	tt.Compose("中文", 2)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src || tab.DocEditor.Buffer.CanUndo() {
		t.Fatal("rich preedit entered history")
	}
	tt.Compose("", 0)
	tt.Type("中文")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src || be.ed.Selection() != before {
		t.Fatal("rich composition undo lost directed selection")
	}
	// Formatting must capture its own current selection, not the selection of
	// the previous input transaction or a sorted replacement range.
	be.rich.selectVisible(4, 3)
	tt.Command("bold")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src || be.ed.Selection() != (editor.Selection{Anchor: len("甲😀乙"), Head: len("甲😀")}) {
		t.Fatalf("format undo selection=%+v", be.ed.Selection())
	}
}

func TestM04CaretMetadataBoundAndInvalidation(t *testing.T) {
	buf := editor.NewBuffer("abc")
	h := docCaretHistory{}
	for i := 0; i < 1100; i++ {
		h.sync(buf)
		buf.Edit(editor.Edit{Start: buf.Len(), End: buf.Len(), Text: "x"})
		h.record(buf, editor.Selection{Anchor: buf.Len() - 1, Head: buf.Len() - 1}, editor.Selection{Anchor: buf.Len(), Head: buf.Len()})
		if h.count > 512 {
			t.Fatal("unbounded caret metadata")
		}
	}
	count := 0
	for node := h.current; node.undo != nil; node = node.undo.from {
		count++
		if count > 512 {
			t.Fatal("unbounded linked metadata")
		}
	}
	buf.Edit(editor.Edit{Start: 0, End: 1, Text: "z"})
	h.sync(buf)
	if h.current.undo != nil || h.current.redo != nil {
		t.Fatal("external edit reused stale metadata")
	}
	buf.Reset("abc")
	h.sync(buf)
	if h.count != 0 {
		t.Fatal("reset retained metadata")
	}
	other := editor.NewBuffer("abc")
	h.sync(other)
	if h.buffer != other || h.current.undo != nil {
		t.Fatal("buffer replacement reused metadata")
	}
}

func TestM04PendingDeleteAndRepeatedTransitions(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 Pending")
	src := "abcde\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	// Same text transition, different original caret: backspace versus delete.
	be.rich.selectVisible(3, 3)
	tt.Key(0, ui.KeyBackspace)
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if be.ed.Cursor() != 3 || tab.DocEditor.Buffer.String() != src {
		t.Fatal("pending backspace undo")
	}
	be.rich.selectVisible(2, 2)
	tt.Key(0, ui.KeyDelete)
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if be.ed.Cursor() != 2 || tab.DocEditor.Buffer.String() != src {
		t.Fatal("repeated transition confused original caret")
	}
	// Two deletions with independent canonical steps.
	be.rich.selectVisible(4, 4)
	tt.Key(0, ui.KeyBackspace)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != "abce\n" || be.ed.Cursor() != 3 {
		t.Fatal("first consecutive undo")
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src || be.ed.Cursor() != 4 {
		t.Fatal("second consecutive undo")
	}
}

func TestM04DraggedReverseSelection(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 Drag")
	src := "甲😀 `乙丙` 丁\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	w := be.rich
	start, end := w.caret(6), w.caret(1)
	tt.Advance(time.Second)
	tt.Press(w.bounds.X+start.X, w.bounds.Y+start.Y+start.H/2)
	tt.Move(w.bounds.X+end.X, w.bounds.Y+end.Y+end.H/2)
	tt.Release(w.bounds.X+end.X, w.bounds.Y+end.Y+end.H/2)
	tt.Frame()
	before := be.ed.Selection()
	if before.Anchor <= before.Head {
		t.Fatalf("drag not reversed: %+v", before)
	}
	tt.Type("新😀")
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src || be.ed.Selection() != before || !caretPainted(tt) {
		t.Fatalf("drag undo selection=%+v want %+v", be.ed.Selection(), before)
	}
}

func TestM04SourceModeTransactionFallback(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M04 Source")
	src := "abcde\n"
	tab := openDocForTest(t, ps, "docs/undo.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("X")
	tt.Frame()
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tab.DocEditor.Select(1, 2)
	tab.DocEditor.Insert("😀") // public source editor transaction, another owner
	tt.Frame()
	changed := tab.DocEditor.Buffer.String()
	if err := tt.Click("Canvas"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, changed, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != "abcdeX\n" || observed[tab].live.block.ed.Cursor() != 2 {
		t.Fatal("source undo replayed stale canvas selection")
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != changed || observed[tab].live.block.ed.Cursor() != len("a😀") {
		t.Fatal("source redo fallback")
	}
}
