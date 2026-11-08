package doctable

import (
	"errors"
	"strings"
	"testing"
)

func mustParse(t *testing.T, src string) *Table {
	t.Helper()
	tb, err := Parse(src)
	if err != nil {
		t.Fatalf("Parse(%q): %v", src, err)
	}
	return tb
}

func TestParseMarkdownRoundTrip(t *testing.T) {
	cases := []string{
		"| a | b |\n| --- | --- |\n| c | d |",
		"| a | b |\n| --- | --- |\n| c | d |\n",
		"a | b\n--- | ---\nc | d",
		"|a|b|\n|-|-|\n|c|d|",
		"| a | b |\r\n| --- | --- |\r\n| c | d |\r\n",
		"| a | b |\n| --- | --- |\n| c | d |\n\nnext paragraph\n",
		"| 名称 | 说明 |\n| :--- | ---: |\n| 中文 | 管道 \\| 代码 `a\\|b` |",
		"| a \\| b | c |\n| --- | --- |\n|  | d |",
		"| a | b |\n| --- | --- |\n| c |",
		"| a | b |\n| --- | --- |\n| c | d | e |",
		"|  |  |\n| --- | --- |\n|  |  |",
	}
	for _, src := range cases {
		tb := mustParse(t, src)
		if got := tb.Markdown(); got != src {
			t.Errorf("round trip of %q became %q", src, got)
		}
	}
}

func TestParseAlignment(t *testing.T) {
	tb := mustParse(t, "| a | b | c | d |\n| :--- | :---: | ---: | --- |\n| 1 | 2 | 3 | 4 |")
	want := []Align{AlignLeft, AlignCenter, AlignRight, AlignDefault}
	for i, a := range want {
		if got := tb.Align(i); got != a {
			t.Errorf("column %d alignment = %v, want %v", i, got, a)
		}
	}
}

func TestParseRejectsNonTables(t *testing.T) {
	cases := []string{
		"",
		"a",
		"a\nb",
		"# heading\n\ntext",
		"a | b\nc | d",
		"| a | b |\n| --- |\n| c | d |",
		"| a | b |\n| --x | --- |\n| c | d |",
	}
	for _, src := range cases {
		if _, err := Parse(src); !errors.Is(err, ErrNotTable) {
			t.Errorf("Parse(%q) error = %v, want ErrNotTable", src, err)
		}
	}
}

func TestParseEnforcesLimits(t *testing.T) {
	header := "| " + strings.Repeat("c | ", MaxColumns) + "\n"
	sep := "| " + strings.Repeat("--- | ", MaxColumns) + "\n"
	if _, err := Parse(header + sep); err != nil {
		t.Fatalf("a table at the column limit was rejected: %v", err)
	}
	tooManyCols := "| " + strings.Repeat("c | ", MaxColumns+1) + "\n" +
		"| " + strings.Repeat("--- | ", MaxColumns+1) + "\n"
	if _, err := Parse(tooManyCols); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a table over the column limit returned %v", err)
	}

	var b strings.Builder
	b.WriteString("| a | b |\n| --- | --- |\n")
	for i := 0; i <= MaxDataRows; i++ {
		b.WriteString("| x | y |\n")
	}
	if _, err := Parse(b.String()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("a table over the row limit returned %v", err)
	}

	huge := strings.Repeat("a", MaxSourceBytes+1)
	if _, err := Parse(huge); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("an oversized block returned %v", err)
	}
}

func TestCellTextDecodesPaddingOnly(t *testing.T) {
	tb := mustParse(t, "| a \\| b | `c\\|d` |\n| --- | --- |\n|  | x |")
	if got := tb.CellText(0, 0); got != `a \| b` {
		t.Errorf("header cell = %q", got)
	}
	if got := tb.CellText(0, 1); got != "`c\\|d`" {
		t.Errorf("code cell = %q", got)
	}
	if got := tb.CellText(1, 0); got != "" {
		t.Errorf("empty cell = %q", got)
	}
}

func TestSetCellKeepsOtherBytes(t *testing.T) {
	src := "|a|b|\n|-|-|\n|c|d|"
	tb := mustParse(t, src)
	if err := tb.SetCell(1, 1, "new"); err != nil {
		t.Fatal(err)
	}
	got := tb.Markdown()
	want := "|a|b|\n|-|-|\n|c| new |"
	if got != want {
		t.Fatalf("Markdown = %q, want %q", got, want)
	}
}

func TestSetCellEscapesPipes(t *testing.T) {
	tb := mustParse(t, "| a |\n| --- |\n| b |")
	if err := tb.SetCell(1, 0, "x|y"); err != nil {
		t.Fatal(err)
	}
	if got := tb.Markdown(); !strings.Contains(got, `x\|y`) {
		t.Fatalf("a bare pipe was not escaped: %q", got)
	}
	// The escaped pipe must not split the row: parsing the output again
	// still sees one column.
	again := mustParse(t, tb.Markdown())
	if again.CellText(1, 0) != `x\|y` {
		t.Fatalf("escaped pipe round trip = %q", again.CellText(1, 0))
	}
}

func TestSetCellKeepsExistingEscapes(t *testing.T) {
	tb := mustParse(t, "| a |\n| --- |\n| b |")
	if err := tb.SetCell(1, 0, `already \| escaped \\ too`); err != nil {
		t.Fatal(err)
	}
	if got := tb.Markdown(); !strings.Contains(got, `already \| escaped \\ too`) {
		t.Fatalf("existing escapes changed: %q", got)
	}
}

func TestSetCellRejectsNewlineAndOversize(t *testing.T) {
	tb := mustParse(t, "| a |\n| --- |\n| b |")
	if err := tb.SetCell(1, 0, "a\nb"); !errors.Is(err, ErrNewline) {
		t.Fatalf("newline error = %v", err)
	}
	if err := tb.SetCell(1, 0, strings.Repeat("x", MaxCellBytes+1)); !errors.Is(err, ErrCellTooLarge) {
		t.Fatalf("oversize error = %v", err)
	}
	if err := tb.SetCell(9, 0, "x"); !errors.Is(err, ErrRange) {
		t.Fatalf("range error = %v", err)
	}
}

func TestRowsAndColumns(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tb.InsertRow(2); err != nil {
		t.Fatal(err)
	}
	if tb.RowCount() != 3 || tb.DataRows() != 2 {
		t.Fatalf("rows = %d, data = %d", tb.RowCount(), tb.DataRows())
	}
	if err := tb.DeleteRow(1); err != nil {
		t.Fatal(err)
	}
	if tb.RowCount() != 2 {
		t.Fatalf("rows after delete = %d", tb.RowCount())
	}
	if err := tb.DeleteRow(0); !errors.Is(err, ErrRange) {
		t.Fatalf("deleting the header returned %v", err)
	}

	if err := tb.InsertColumn(1); err != nil {
		t.Fatal(err)
	}
	if tb.Columns() != 3 {
		t.Fatalf("columns = %d", tb.Columns())
	}
	if err := tb.DeleteColumn(1); err != nil {
		t.Fatal(err)
	}
	if tb.Columns() != 2 {
		t.Fatalf("columns after delete = %d", tb.Columns())
	}
	if err := tb.DeleteColumn(0); err != nil {
		t.Fatal(err)
	}
	if err := tb.DeleteColumn(0); !errors.Is(err, ErrLastColumn) {
		t.Fatalf("deleting the last column returned %v", err)
	}
}

func TestInsertColumnKeepsCellsAligned(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| 1 | 2 |")
	if err := tb.InsertColumn(1); err != nil {
		t.Fatal(err)
	}
	if err := tb.SetCell(1, 1, "mid"); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if got := again.CellText(1, 0); got != "1" {
		t.Errorf("left cell = %q", got)
	}
	if got := again.CellText(1, 1); got != "mid" {
		t.Errorf("inserted cell = %q", got)
	}
	if got := again.CellText(1, 2); got != "2" {
		t.Errorf("right cell = %q", got)
	}
}

func TestSetAlignRewritesSeparator(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| 1 | 2 |")
	if err := tb.SetAlign(0, AlignCenter); err != nil {
		t.Fatal(err)
	}
	if err := tb.SetAlign(1, AlignRight); err != nil {
		t.Fatal(err)
	}
	got := tb.Markdown()
	want := "| a | b |\n| :---: | ---: |\n| 1 | 2 |"
	if got != want {
		t.Fatalf("Markdown = %q, want %q", got, want)
	}
}

func TestPasteTSVExpandsAndWrites(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tb.PasteTSV("x\ty\n1\t2\n", 1, 0); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if again.RowCount() != 3 || again.Columns() != 2 {
		t.Fatalf("table is %d rows × %d columns", again.RowCount(), again.Columns())
	}
	want := [][]string{{"x", "y"}, {"1", "2"}}
	for r := range want {
		for c := range want[r] {
			if got := again.CellText(r+1, c); got != want[r][c] {
				t.Errorf("cell %d,%d = %q, want %q", r+1, c, got, want[r][c])
			}
		}
	}
}

func TestPasteTSVExpandsRightAndDown(t *testing.T) {
	tb := mustParse(t, "| a |\n| --- |\n| b |")
	if err := tb.PasteTSV("1\t2\t3\n4\t5\t6", 1, 0); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if again.Columns() != 3 {
		t.Fatalf("columns = %d", again.Columns())
	}
	if got := again.CellText(1, 2); got != "3" {
		t.Errorf("cell 1,2 = %q", got)
	}
	if got := again.CellText(2, 0); got != "4" {
		t.Errorf("cell 2,0 = %q", got)
	}
}

func TestPasteTSVRejectsNonRectangular(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| c | d |")
	before := tb.Markdown()
	if err := tb.PasteTSV("1\t2\n3", 1, 0); !errors.Is(err, ErrNotRectangular) {
		t.Fatalf("error = %v", err)
	}
	if got := tb.Markdown(); got != before {
		t.Fatalf("a refused paste changed the table: %q", got)
	}
}

func TestPasteTSVRejectsBeyondLimits(t *testing.T) {
	tb := mustParse(t, "| a |\n| --- |\n| b |")
	wide := strings.Repeat("x\t", MaxColumns) + "x"
	if err := tb.PasteTSV(wide, 1, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("wide paste error = %v", err)
	}
	tall := strings.Repeat("x\n", MaxDataRows+2)
	if err := tb.PasteTSV(tall, 1, 0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("tall paste error = %v", err)
	}
	if err := tb.PasteTSV("", 1, 0); !errors.Is(err, ErrEmptyPaste) {
		t.Fatalf("empty paste error = %v", err)
	}
}

func TestPasteTSVEscapesPipesAndKeepsChinese(t *testing.T) {
	tb := mustParse(t, "| a |\n| --- |\n| b |")
	if err := tb.PasteTSV("中文|管道\t第二格", 1, 0); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if got := again.CellText(1, 0); got != `中文\|管道` {
		t.Errorf("piped Chinese cell = %q", got)
	}
	if got := again.CellText(1, 1); got != "第二格" {
		t.Errorf("Chinese cell = %q", got)
	}
}

func TestRaggedRowIsPreservedUntilEdited(t *testing.T) {
	src := "| a | b |\n| --- | --- |\n| c |"
	tb := mustParse(t, src)
	if got := tb.Markdown(); got != src {
		t.Fatalf("untouched ragged row changed: %q", got)
	}
	if err := tb.SetCell(1, 1, "d"); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if got := again.CellText(1, 1); got != "d" {
		t.Fatalf("padded cell = %q", got)
	}
}

func TestExtraCellsAreIgnoredUntilRebuilt(t *testing.T) {
	src := "| a | b |\n| --- | --- |\n| c | d | e |"
	tb := mustParse(t, src)
	if got := tb.Markdown(); got != src {
		t.Fatalf("untouched extra cell changed: %q", got)
	}
	if err := tb.InsertRow(2); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if again.Columns() != 2 {
		t.Fatalf("columns = %d", again.Columns())
	}
}

func TestParseSinglePipeRowDoesNotPanic(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n | ")
	if tb.RowCount() != 2 {
		t.Fatalf("rows = %d", tb.RowCount())
	}
	if got := tb.CellText(1, 0); got != "" {
		t.Fatalf("single-pipe row cell = %q", got)
	}
}

func TestRebuiltEmptyRowKeepsColumnCount(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tb.InsertRow(2); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	if again.Columns() != 2 || again.RowCount() != 3 {
		t.Fatalf("table is %d rows × %d columns", again.RowCount(), again.Columns())
	}
	if got := again.CellText(2, 1); got != "" {
		t.Fatalf("empty rebuilt cell = %q", got)
	}
}

func TestCRLFPreservedAfterEdit(t *testing.T) {
	tb := mustParse(t, "| a | b |\r\n| --- | --- |\r\n| c | d |\r\n")
	if err := tb.SetCell(1, 0, "x"); err != nil {
		t.Fatal(err)
	}
	got := tb.Markdown()
	if strings.Count(got, "\r\n") != 3 {
		t.Fatalf("CRLF line endings changed: %q", got)
	}
	if strings.Contains(strings.ReplaceAll(got, "\r\n", ""), "\n") {
		t.Fatalf("a bare LF appeared: %q", got)
	}
}

func TestFormatWithoutBoundaryPipes(t *testing.T) {
	tb := mustParse(t, "a | b\n--- | ---\nc | d")
	if err := tb.SetAlign(0, AlignLeft); err != nil {
		t.Fatal(err)
	}
	got := tb.Markdown()
	want := "a | b\n:--- | ---\nc | d"
	if got != want {
		t.Fatalf("Markdown = %q, want %q", got, want)
	}
}

func TestPasteTSVAtOffsetKeepsSurroundingCells(t *testing.T) {
	tb := mustParse(t, "| a | b | c |\n| --- | --- | --- |\n| 1 | 2 | 3 |\n| 4 | 5 | 6 |")
	if err := tb.PasteTSV("x\ty", 1, 1); err != nil {
		t.Fatal(err)
	}
	again := mustParse(t, tb.Markdown())
	want := [][]string{
		{"1", "x", "y"},
		{"4", "5", "6"},
	}
	for r := range want {
		for c := range want[r] {
			if got := again.CellText(r+1, c); got != want[r][c] {
				t.Errorf("cell %d,%d = %q, want %q", r+1, c, got, want[r][c])
			}
		}
	}
}

func TestPasteTSVNormalizesCRLFToTheTableEOL(t *testing.T) {
	tb := mustParse(t, "| a | b |\n| --- | --- |\n| c | d |")
	if err := tb.PasteTSV("x\ty\r\n1\t2\r\n", 1, 0); err != nil {
		t.Fatal(err)
	}
	got := tb.Markdown()
	if strings.ContainsRune(got, '\r') {
		t.Fatalf("a CRLF paste wrote a carriage return: %q", got)
	}
	again := mustParse(t, got)
	if again.CellText(1, 0) != "x" || again.CellText(2, 1) != "2" {
		t.Fatalf("pasted cells = %q, %q", again.CellText(1, 0), again.CellText(2, 1))
	}
}

func TestSetAlignToTheSameValueKeepsSeparatorBytes(t *testing.T) {
	src := "| a | b |\n| :--- | ---: |\n| c | d |"
	tb := mustParse(t, src)
	if err := tb.SetAlign(0, AlignLeft); err != nil {
		t.Fatal(err)
	}
	if err := tb.SetAlign(1, AlignRight); err != nil {
		t.Fatal(err)
	}
	if got := tb.Markdown(); got != src {
		t.Fatalf("an unchanged alignment rewrote the table: %q", got)
	}
}

func TestEditKeepsFollowingText(t *testing.T) {
	src := "| a |\n| --- |\n| b |\n\nafter\n"
	tb := mustParse(t, src)
	if err := tb.SetCell(1, 0, "x"); err != nil {
		t.Fatal(err)
	}
	got := tb.Markdown()
	if !strings.HasSuffix(got, "\n\nafter\n") {
		t.Fatalf("the text after the table changed: %q", got)
	}
}

func TestStructuralEditsRespectTheLimits(t *testing.T) {
	header := "| " + strings.Repeat("c | ", MaxColumns) + "\n"
	sep := "| " + strings.Repeat("--- | ", MaxColumns) + "\n"
	tb := mustParse(t, header+sep)
	if err := tb.InsertColumn(0); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("InsertColumn at the limit returned %v", err)
	}

	var b strings.Builder
	b.WriteString("| a | b |\n| --- | --- |\n")
	for i := 0; i < MaxDataRows; i++ {
		b.WriteString("| x | y |\n")
	}
	tall := mustParse(t, b.String())
	if err := tall.InsertRow(tall.RowCount()); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("InsertRow at the limit returned %v", err)
	}
}
