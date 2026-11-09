package desktop

import (
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/doctable"
)

// docTableHarness renders one table block in a headless window and
// records every block Markdown handed to replace. history is a minimal
// undo stack: each replacement pushes the source it replaced, so undo
// puts the document back exactly as the canvas's buffer would.
type docTableHarness struct {
	source  string
	emitted []string
	history []string
}

func (h *docTableHarness) view(c *ui.Context) {
	root := ui.Column(c).FillWidth().Label("table-block")
	renderDocTable(c, c.Theme(), root, h.source, func(md string) {
		h.history = append(h.history, h.source)
		h.source = md
		h.emitted = append(h.emitted, md)
	})
}

func (h *docTableHarness) undo() {
	if len(h.history) == 0 {
		return
	}
	h.source = h.history[len(h.history)-1]
	h.history = h.history[:len(h.history)-1]
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

// --- layout and interaction polish ---------------------------------------

func newDocTableHarnessSize(t *testing.T, source string, w, h int) (*docTableHarness, *ui.Tester) {
	t.Helper()
	harness := &docTableHarness{source: source}
	tt := ui.NewTester(harness.view, w, h)
	tt.Frame()
	return harness, tt
}

func TestDocTableColumnWidthMeasuresCJKAndClamps(t *testing.T) {
	ascii := docTableColumnWidth([]string{"abcdefghij", "x"})
	cjk := docTableColumnWidth([]string{"用户名密码登录", "x"})
	if cjk <= ascii {
		t.Fatalf("a CJK column (%v) is not wider than an ASCII one (%v)", cjk, ascii)
	}
	th := markdown.DefaultTheme(ui.LightTheme())
	pad := 2*th.FontSize*th.TableCellPadding[0] + 1
	if got := docTableColumnWidth([]string{""}); got != pad {
		t.Fatalf("empty column=%v, padding=%v", got, pad)
	}
	long := strings.Repeat("很长的中文内容", 40)
	if got := docTableColumnWidth([]string{long}); got != th.FontSize*th.TableMaxColumn+pad {
		t.Fatalf("maximum width=%v", got)
	}
}

func TestDocTableAlignMapping(t *testing.T) {
	cases := map[doctable.Align]ui.Align{
		doctable.AlignDefault: ui.Start,
		doctable.AlignLeft:    ui.Start,
		doctable.AlignCenter:  ui.Center,
		doctable.AlignRight:   ui.End,
	}
	for in, want := range cases {
		if got := docTableTextAlign(in); got != want {
			t.Errorf("docTableTextAlign(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestDocTableWideTableShowsScrollHint(t *testing.T) {
	h, tt := newDocTableHarnessSize(t, "| header a | header b | header c | header d | header e | header f |\n| --- | --- | --- | --- | --- | --- |\n| 1 | 2 | 3 | 4 | 5 | 6 |", 360, 400)
	tt.Frame() // the first frame measures the frame; the hint follows it
	if !tt.HasText("横向滚动") {
		t.Fatalf("a table wider than the canvas did not show its scroll hint: %v", tt.Texts())
	}
	if len(h.emitted) != 0 {
		t.Fatalf("drawing the hint changed the table: %q", h.last(t))
	}

	tt.SetSize(900, 500)
	tt.Frame()
	tt.Frame()
	if tt.HasText("横向滚动") {
		t.Fatalf("the scroll hint stayed after the table fit: %v", tt.Texts())
	}
}

func TestDocTableNarrowTableHasNoScrollHint(t *testing.T) {
	_, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	tt.Frame()
	if tt.HasText("横向滚动") {
		t.Fatalf("a table that fits showed a scroll hint: %v", tt.Texts())
	}
}

func TestDocTableActiveCellIndicator(t *testing.T) {
	_, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("R2C1") {
		t.Fatalf("the toolbar did not name the active cell: %v", tt.Texts())
	}
	if !tt.Focused("Table cell 2,1") {
		t.Fatal("clicking the cell did not focus its input")
	}
}

func TestDocTableAlignmentIsDisabledWithoutACell(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Align center"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(h.emitted) != 0 {
		t.Fatalf("alignment with no active cell changed the table: %q", h.last(t))
	}
	if tt.HasText("R1C1") || tt.HasText("R2C1") {
		t.Fatal("an active cell was reported without one being clicked")
	}
}

func TestDocTableDeleteColumnIsDisabledAtOneColumn(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a |\n| --- |\n| b |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Delete column"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(h.emitted) != 0 {
		t.Fatalf("deleting the only column changed the table: %q", h.last(t))
	}
}

func TestDocTableHeaderEditKeepsDelimiter(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 1,2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("标题")
	tt.Frame()
	got := h.last(t)
	if !strings.Contains(got, "| a | 标题 |") {
		t.Fatalf("header edit = %q", got)
	}
	if !strings.Contains(got, "| --- | --- |") {
		t.Fatalf("header edit changed the delimiter row: %q", got)
	}
	if !strings.Contains(got, "| c | d |") {
		t.Fatalf("header edit changed a data row: %q", got)
	}
}

func TestDocTableTabMovesWithinARowWithoutEmitting(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 1,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if len(h.emitted) != 0 {
		t.Fatalf("Tab within a row changed the table: %q", h.last(t))
	}
	if !tt.Focused("Table cell 1,2") {
		t.Fatal("Tab did not move to the next cell")
	}
}

func TestDocTableStructuralActionsEmitOnceEach(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	actions := []string{"Add row", "Add column", "Delete column", "Delete row"}
	for i, action := range actions {
		if err := tt.Click(action); err != nil {
			t.Fatalf("%s: %v", action, err)
		}
		tt.Frame()
		if len(h.emitted) != i+1 {
			t.Fatalf("%s made %d replacements, want %d", action, len(h.emitted), i+1)
		}
		if _, err := doctable.Parse(h.last(t)); err != nil {
			t.Fatalf("%s produced invalid Markdown: %v\n%q", action, err, h.last(t))
		}
	}
}

func TestDocTableToolbarFocusesTheNewRow(t *testing.T) {
	_, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Add row"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.Focused("Table cell 3,1") {
		t.Fatalf("Add row did not focus the new row's first cell: %v", tt.Texts())
	}
}

func TestDocTablePipeAndUnicodeRoundTrip(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("中文|管道")
	tt.Frame()

	got := h.last(t)
	if !strings.Contains(got, `中文\|管道`) {
		t.Fatalf("the pipe was not escaped: %q", got)
	}
	tb, err := doctable.Parse(got)
	if err != nil {
		t.Fatalf("the edited table no longer parses: %v\n%q", err, got)
	}
	if tb.Columns() != 2 {
		t.Fatalf("the escaped pipe split the row: %d columns\n%q", tb.Columns(), got)
	}
	if tb.CellText(1, 0) != `中文\|管道` {
		t.Fatalf("cell text = %q", tb.CellText(1, 0))
	}
	if tb.CellText(1, 1) != "d" {
		t.Fatalf("the neighbouring cell changed: %q", tb.CellText(1, 1))
	}
}

func TestDocTableLongChineseCellKeepsStructure(t *testing.T) {
	long := strings.Repeat("很长的中文内容", 20)
	h, tt := newDocTableHarness(t, "| 名称 | 说明 |\n| --- | --- |\n| 登录 | 用户名 |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(long)
	tt.Frame()

	got := h.last(t)
	if !strings.Contains(got, long) {
		t.Fatal("a long Chinese cell lost text")
	}
	if !strings.HasPrefix(got, "| 名称 | 说明 |\n| --- | --- |") {
		t.Fatalf("a long cell changed the header or delimiter: %q", got)
	}
	tb, err := doctable.Parse(got)
	if err != nil {
		t.Fatalf("a long cell produced invalid Markdown: %v", err)
	}
	if tb.Columns() != 2 {
		t.Fatalf("a long cell changed the column count to %d", tb.Columns())
	}
}

func TestDocTableExternalChangeRefreshesTheCellCache(t *testing.T) {
	cases := []struct {
		name     string
		external string
		want     string
	}{
		{
			name:     "undo to the original",
			external: "| a | b |\n| --- | --- |\n| c | d |",
			want:     "| a | b |\n| --- | --- |\n| c | W |",
		},
		{
			name:     "another version",
			external: "| a | b |\n| --- | --- |\n| Y | Z |",
			want:     "| a | b |\n| --- | --- |\n| Y | W |",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
			if err := tt.Click("Table cell 2,1"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("X")
			tt.Frame()
			if got := h.last(t); got != "| a | b |\n| --- | --- |\n| X | d |" {
				t.Fatalf("first edit = %q", got)
			}

			// The document changed under the table: an undo or an edit in
			// another view. The next frame must adopt it, not keep the
			// cached X.
			h.source = tc.external
			tt.Frame()

			if err := tt.Click("Table cell 2,2"); err != nil {
				t.Fatal(err)
			}
			tt.Key(ui.Cmd, ui.KeyA)
			tt.Type("W")
			tt.Frame()
			if got := h.last(t); got != tc.want {
				t.Fatalf("edit after an external change = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDocTableUndoRestoresAndRefreshes(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("X")
	tt.Frame()
	if got := h.last(t); got != "| a | b |\n| --- | --- |\n| X | d |" {
		t.Fatalf("edit = %q", got)
	}

	// Undo the replacement as the canvas's buffer would.
	h.undo()
	tt.Frame()

	if err := tt.Click("Table cell 2,2"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("W")
	tt.Frame()
	want := "| a | b |\n| --- | --- |\n| c | W |"
	if got := h.last(t); got != want {
		t.Fatalf("edit after undo = %q, want %q", got, want)
	}
}

func TestDocTableAlignsTheActiveColumn(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tt.Click("Table cell 2,2"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Align center"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := h.last(t)
	if !strings.Contains(got, "| --- | :---: |") {
		t.Fatalf("Align center changed the wrong column: %q", got)
	}
}

func TestDocTableDeletesTheActiveRow(t *testing.T) {
	h, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |\n| e | f |")
	if err := tt.Click("Table cell 3,1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Delete row"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	got := h.last(t)
	if strings.Contains(got, "| e | f |") {
		t.Fatalf("Delete row kept the active row: %q", got)
	}
	if !strings.Contains(got, "| c | d |") {
		t.Fatalf("Delete row removed another row: %q", got)
	}
}

func TestDocTableAddColumnIsDisabledAtTheLimit(t *testing.T) {
	header := "| " + strings.Repeat("c | ", doctable.MaxColumns) + "\n"
	sep := "| " + strings.Repeat("--- | ", doctable.MaxColumns) + "\n"
	h, tt := newDocTableHarness(t, header+sep)
	if err := tt.Click("Table cell 1,1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("Add column"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(h.emitted) != 0 {
		t.Fatalf("Add column changed a table at the limit: %q", h.last(t))
	}
}

func TestDocTableDisabledControlsFade(t *testing.T) {
	_, tt := newDocTableHarness(t, "| a | b |\n| --- | --- |\n| c | d |")
	darkest := func(label string) int {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("no control labeled %q", label)
		}
		img := tt.Image()
		best := 255
		for y := int(r.Y); y < int(r.Y+r.H); y++ {
			for x := int(r.X); x < int(r.X+r.W); x++ {
				i := img.PixOffset(x, y)
				if v := int(img.Pix[i]); v < best {
					best = v
				}
			}
		}
		return best
	}

	disabled := darkest("Align center")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	enabled := darkest("Align center")
	if disabled <= enabled+20 {
		t.Fatalf("a disabled control is not visibly faded: disabled=%d enabled=%d", disabled, enabled)
	}
}
