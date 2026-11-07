package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// docTableHarness renders one table block in a headless window and
// records every block Markdown handed to replace.
type docTableHarness struct {
	source  string
	emitted []string
}

func (h *docTableHarness) view(c *ui.Context) {
	root := ui.Column(c).Label("table-block")
	renderDocTable(c, c.Theme(), root, h.source, func(md string) {
		h.source = md
		h.emitted = append(h.emitted, md)
	})
}

func (h *docTableHarness) last(t *testing.T) string {
	t.Helper()
	if len(h.emitted) == 0 {
		t.Fatal("the table never handed a replacement to the caller")
	}
	return h.emitted[len(h.emitted)-1]
}

func newDocTableHarness(t *testing.T, source string) (*docTableHarness, *ui.Tester) {
	t.Helper()
	h := &docTableHarness{source: source}
	tt := ui.NewTester(h.view, 900, 600)
	tt.Frame()
	return h, tt
}

func TestDocTableRendersAndEditsACell(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if !tt.HasText("Table tools") {
		t.Fatalf("the table toolbar was not drawn: %v", tt.Texts())
	}
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("X")
	tt.Frame()

	got := h.last(t)
	want := "| a | b |\n| --- | --- |\n| X | d |"
	if got != want {
		t.Fatalf("edited table = %q, want %q", got, want)
	}
	if h.source != got {
		t.Fatalf("the harness source was not replaced: %q", h.source)
	}
}

func TestDocTableKeepsUntouchedBytes(t *testing.T) {
	source := "|a|b|\n|-|-|\n|c|d|"
	h, tt := newDocTableHarness(t, source)
	if err := tt.Click("Table cell 2,2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("z")
	tt.Frame()

	got := h.last(t)
	if !strings.HasPrefix(got, "|a|b|\n|-|-|\n|c|") {
		t.Fatalf("untouched header, delimiter or cell changed: %q", got)
	}
	if !strings.Contains(got, "z") {
		t.Fatalf("the edit is missing: %q", got)
	}
}

func TestDocTableTabAddsARowAtTheEnd(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()

	got := h.last(t)
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("Tab on the last cell produced %d lines: %q", len(lines), got)
	}
	if lines[3] != "|  |  |" {
		t.Fatalf("the added row = %q", lines[3])
	}
	if !tt.Focused("Table cell 3,1") {
		t.Fatalf("Tab did not focus the new row's first cell: %v", tt.Texts())
	}
}

func TestDocTableShiftTabMovesBack(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if len(h.emitted) != 0 {
		t.Fatalf("Shift+Tab changed the table: %q", h.last(t))
	}
	if !tt.Focused("Table cell 2,1") {
		t.Fatalf("Shift+Tab did not move to the previous cell")
	}
}

func TestDocTableEscapeLeavesTheCell(t *testing.T) {
	_, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	if !tt.Focused("Table cell 2,1") {
		t.Fatal("the cell did not take the focus")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.Focused("Table cell 2,1") {
		t.Fatal("Escape kept the cell focused")
	}
}

func TestDocTableEnterDoesNotMoveTheFocus(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if len(h.emitted) != 0 {
		t.Fatalf("Enter changed the table: %q", h.last(t))
	}
	if !tt.Focused("Table cell 2,1") {
		t.Fatal("Enter moved the focus; an input method's composition Enter must not")
	}
}

func TestDocTablePasteTSVIsOneReplacement(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.SetClipboard("x\ty\n1\t2")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()

	if len(h.emitted) != 1 {
		t.Fatalf("a paste made %d replacements, want 1", len(h.emitted))
	}
	got := h.last(t)
	for _, want := range []string{"x", "y", "1", "2"} {
		if !strings.Contains(got, want) {
			t.Fatalf("pasted cell %q missing from %q", want, got)
		}
	}
	lines := strings.Split(got, "\n")
	if len(lines) != 4 {
		t.Fatalf("pasted table has %d lines: %q", len(lines), got)
	}
}

func TestDocTablePasteRefusesNonRectangular(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.SetClipboard("x\ty\n1")
	tt.Key(ui.Cmd, ui.KeyV)
	tt.Frame()

	if len(h.emitted) != 0 {
		t.Fatalf("a refused paste changed the table: %q", h.last(t))
	}
	if !tt.HasText("不是矩形") {
		t.Fatalf("the refusal was not reported: %v", tt.Texts())
	}
}

func TestDocTableToolbarAddsAndDeletes(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Add row"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if lines := strings.Split(h.last(t), "\n"); len(lines) != 4 {
		t.Fatalf("Add row produced %q", h.last(t))
	}

	if err := tt.Click("Add column"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := h.last(t); !strings.Contains(got, "| c |  | d |") {
		t.Fatalf("Add column produced %q", got)
	}

	if err := tt.Click("Delete column"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := h.last(t); strings.Contains(got, "| c |  | d |") {
		t.Fatalf("Delete column produced %q", got)
	}
}

func TestDocTableToolbarAligns(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Align center"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := h.last(t)
	if !strings.Contains(got, "| :---: | --- |") {
		t.Fatalf("Align center produced %q", got)
	}
}

func TestDocTableUnsupportedSourceDrawsNothing(t *testing.T) {
	rendered := true
	tt := ui.NewTester(func(c *ui.Context) {
		root := ui.Column(c).Label("table-block")
		rendered = renderDocTable(c, c.Theme(), root, "not a table", func(string) {})
	}, 400, 200)
	tt.Frame()
	if rendered {
		t.Fatal("a non-table block was reported as rendered")
	}
	if tt.HasText("Table tools") {
		t.Fatal("a non-table block drew table controls")
	}
}

func TestDocTableChineseCell(t *testing.T) {
	h, tt := newDocTableHarness(t, "| 名称 | 说明 |\n| --- | --- |\n| 登录 | 用户名 |")
	if err := tt.Click("Table cell 2,2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("密码")
	tt.Frame()
	got := h.last(t)
	if !strings.Contains(got, "密码") {
		t.Fatalf("Chinese text was lost: %q", got)
	}
	if !strings.HasPrefix(got, "| 名称 | 说明 |\n| --- | --- |") {
		t.Fatalf("Chinese header changed: %q", got)
	}
}
