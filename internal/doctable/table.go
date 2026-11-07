// Package doctable models one GitHub-flavoured Markdown table block as a
// small editable grid.
//
// The model keeps the block's source bytes for every cell it does not
// change, so a cell edit rewrites only that cell: the table's delimiter
// style, its alignment row, the padding of untouched cells and the raw
// text of untouched rows stay byte for byte as they were. Structural
// edits (a row or a column added or removed) rebuild the affected rows
// from the model, which is the only place where formatting is
// normalised.
//
// The package has no dependency on the desktop toolkit, never resolves
// links and never touches the network. It is bounded: a block larger than
// MaxSourceBytes, a table with more than MaxDataRows data rows or more
// than MaxColumns columns is rejected with ErrTooLarge, so a caller can
// fall back to showing the block as source.
package doctable

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Limits bound one table so a pathological block cannot make the editor
// allocate without end. They are the minimum the design asks for.
const (
	// MaxDataRows is the most data rows a table may have; the header row
	// is not counted.
	MaxDataRows = 200
	// MaxColumns is the most columns a table may have.
	MaxColumns = 30
	// MaxSourceBytes is the largest block source accepted.
	MaxSourceBytes = 1 << 20
	// MaxCellBytes is the largest text one cell may hold.
	MaxCellBytes = 64 << 10
)

// Errors returned by Parse and the edit methods. Callers distinguish them
// with errors.Is.
var (
	// ErrNotTable reports source that is not a GFM table block.
	ErrNotTable = errors.New("doctable: not a GFM table block")
	// ErrTooLarge reports a block, row or column beyond the limits.
	ErrTooLarge = errors.New("doctable: table exceeds the size limit")
	// ErrRange reports an index outside the table.
	ErrRange = errors.New("doctable: index out of range")
	// ErrLastColumn reports deleting the only column.
	ErrLastColumn = errors.New("doctable: a table keeps at least one column")
	// ErrNewline reports text with a line break, which a cell cannot hold.
	ErrNewline = errors.New("doctable: a cell cannot contain a line break")
	// ErrCellTooLarge reports text beyond MaxCellBytes.
	ErrCellTooLarge = errors.New("doctable: cell exceeds the size limit")
	// ErrNotRectangular reports a paste whose rows have different widths.
	ErrNotRectangular = errors.New("doctable: pasted data is not rectangular")
	// ErrEmptyPaste reports a paste with no cells.
	ErrEmptyPaste = errors.New("doctable: nothing to paste")
)

// Align is a column alignment, as the delimiter row spells it.
type Align uint8

// Alignments. AlignDefault is the unmarked `---` column.
const (
	AlignDefault Align = iota
	AlignLeft
	AlignCenter
	AlignRight
)

// Marker returns the delimiter cell that spells the alignment.
func (a Align) Marker() string {
	switch a {
	case AlignLeft:
		return ":---"
	case AlignCenter:
		return ":---:"
	case AlignRight:
		return "---:"
	}
	return "---"
}

// cell is one cell of a row.
type cell struct {
	// raw is the exact source text between the cell's delimiters,
	// padding included. It is empty for a cell added by an edit.
	raw string
	// text is the cell's inline Markdown source, trimmed of padding.
	text string
	// start and end delimit the raw text in the row's raw line.
	start, end int
	// present reports whether the cell exists in the raw line. A row
	// shorter than the header has padding cells that do not.
	present bool
	// dirty reports that text changed and the raw cell must be rewritten.
	dirty bool
}

// row is one line of the table: the header, a data row, or a row added by
// an edit.
type row struct {
	cells []cell
	// raw is the original line, empty for a row added by an edit.
	raw string
	// rebuilt reports that the row must be written from cells, because
	// its structure changed or it has no raw line.
	rebuilt bool
}

// Table is one parsed GFM table block.
type Table struct {
	rows     []row
	align    []Align
	sepRaw   string
	sepDirty bool
	leading  bool
	trailing bool
	eol      string
	tail     string
	columns  int
}

// rawCell is one cell as split from a source line.
type rawCell struct {
	raw        string
	start, end int
}

// Parse parses one complete GFM table block. src may end with a line
// ending and may carry the blank lines that follow the table; both are
// preserved by Markdown. A block that is not a table, or one beyond the
// limits, returns an error.
func Parse(src string) (*Table, error) {
	if len(src) > MaxSourceBytes {
		return nil, fmt.Errorf("%w: %d bytes", ErrTooLarge, len(src))
	}
	lines, eol, tail := splitBlock(src)
	if len(lines) < 2 {
		return nil, ErrNotTable
	}
	header, leading, trailing, hasPipe := splitRow(lines[0])
	if !hasPipe || len(header) == 0 {
		return nil, ErrNotTable
	}
	sep, _, _, _ := splitRow(lines[1])
	if len(sep) != len(header) {
		return nil, ErrNotTable
	}
	align := make([]Align, len(sep))
	for i := range sep {
		a, ok := parseAlign(sep[i].raw)
		if !ok {
			return nil, ErrNotTable
		}
		align[i] = a
	}
	columns := len(header)
	if columns > MaxColumns {
		return nil, fmt.Errorf("%w: %d columns", ErrTooLarge, columns)
	}
	if len(lines)-2 > MaxDataRows {
		return nil, fmt.Errorf("%w: %d data rows", ErrTooLarge, len(lines)-2)
	}
	t := &Table{
		align:    align,
		sepRaw:   lines[1],
		leading:  leading,
		trailing: trailing,
		eol:      eol,
		tail:     tail,
		columns:  columns,
	}
	t.rows = make([]row, 0, len(lines)-1)
	t.rows = append(t.rows, newRow(lines[0], header, columns))
	for _, line := range lines[2:] {
		cells, _, _, _ := splitRow(line)
		t.rows = append(t.rows, newRow(line, cells, columns))
	}
	return t, nil
}

// newRow builds a row from a source line and its split cells. Cells the
// line does not have are padding cells; cells beyond the header's column
// count are ignored, as GFM ignores them.
func newRow(line string, raw []rawCell, columns int) row {
	r := row{raw: line, cells: make([]cell, columns)}
	n := len(raw)
	if n > columns {
		n = columns
	}
	for i := 0; i < n; i++ {
		r.cells[i] = cell{
			raw:     raw[i].raw,
			text:    strings.TrimSpace(raw[i].raw),
			start:   raw[i].start,
			end:     raw[i].end,
			present: true,
		}
	}
	return r
}

// splitBlock splits src into the table's lines, the line ending it uses
// and the tail from the end of the last table line. The table ends at the
// first blank line after the delimiter row; blank lines and anything
// after them stay in the tail.
func splitBlock(src string) (lines []string, eol, tail string) {
	spans := splitSpans(src)
	k := len(spans)
	for i := 2; i < len(spans); i++ {
		if strings.TrimSpace(src[spans[i].start:spans[i].end]) == "" {
			k = i
			break
		}
	}
	lines = make([]string, k)
	for i := 0; i < k; i++ {
		lines[i] = src[spans[i].start:spans[i].end]
	}
	if k > 0 {
		tail = src[spans[k-1].end:]
	}
	eol = "\n"
	if k > 0 && src[spans[0].end:spans[0].next] == "\r\n" {
		eol = "\r\n"
	}
	return lines, eol, tail
}

// lineSpan locates one line: start and end delimit its text without the
// line ending, next is the start of the following line.
type lineSpan struct {
	start, end, next int
}

func splitSpans(src string) []lineSpan {
	var out []lineSpan
	for i := 0; i < len(src); {
		j := strings.IndexByte(src[i:], '\n')
		if j < 0 {
			out = append(out, lineSpan{i, len(src), len(src)})
			break
		}
		end := i + j
		if end > i && src[end-1] == '\r' {
			end--
		}
		out = append(out, lineSpan{i, end, i + j + 1})
		i += j + 1
	}
	return out
}

// splitRow splits one line into cells. leading and trailing report whether
// the line has a boundary pipe on that side; hasPipe reports whether the
// line has any unescaped pipe at all.
func splitRow(line string) (cells []rawCell, leading, trailing, hasPipe bool) {
	pipes := pipePositions(line)
	hasPipe = len(pipes) > 0
	first, last := 0, len(line)
	if hasPipe {
		if strings.TrimSpace(line[:pipes[0]]) == "" {
			leading = true
			first = pipes[0] + 1
		}
		if strings.TrimSpace(line[pipes[len(pipes)-1]+1:]) == "" {
			trailing = true
			last = pipes[len(pipes)-1]
		}
	}
	if first > last {
		// One pipe with only spaces around it is both boundaries; the row
		// has one empty cell.
		first = last
	}
	start := first
	for _, p := range pipes {
		if p < first || p >= last {
			continue
		}
		cells = append(cells, rawCell{raw: line[start:p], start: start, end: p})
		start = p + 1
	}
	cells = append(cells, rawCell{raw: line[start:last], start: start, end: last})
	return cells, leading, trailing, hasPipe
}

// pipePositions returns the offsets of the line's unescaped pipes. A
// backslash escapes a pipe and another backslash; any other backslash is
// an ordinary character of the cell's inline Markdown.
func pipePositions(line string) []int {
	var pos []int
	for i := 0; i < len(line); {
		switch line[i] {
		case '\\':
			if i+1 < len(line) && (line[i+1] == '\\' || line[i+1] == '|') {
				i += 2
				continue
			}
			i++
		case '|':
			pos = append(pos, i)
			i++
		default:
			i++
		}
	}
	return pos
}

// parseAlign parses one delimiter cell.
func parseAlign(raw string) (Align, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return AlignDefault, false
	}
	left := strings.HasPrefix(s, ":")
	right := strings.HasSuffix(s, ":")
	body := s
	if left {
		body = body[1:]
	}
	if right && len(body) > 0 {
		body = body[:len(body)-1]
	}
	if body == "" {
		return AlignDefault, false
	}
	for i := 0; i < len(body); i++ {
		if body[i] != '-' {
			return AlignDefault, false
		}
	}
	switch {
	case left && right:
		return AlignCenter, true
	case left:
		return AlignLeft, true
	case right:
		return AlignRight, true
	}
	return AlignDefault, true
}

// Markdown returns the block's Markdown. Untouched cells and rows keep
// their source bytes; the block's trailing blank lines are preserved.
func (t *Table) Markdown() string {
	lines := make([]string, 0, len(t.rows)+1)
	lines = append(lines, t.rowMarkdown(&t.rows[0]))
	lines = append(lines, t.separator())
	for i := 1; i < len(t.rows); i++ {
		lines = append(lines, t.rowMarkdown(&t.rows[i]))
	}
	return strings.Join(lines, t.eol) + t.tail
}

// rowMarkdown writes one row. A rebuilt row is formatted from its cells;
// otherwise only the edited cells are spliced into the original line, so
// the delimiters and the untouched cells keep their bytes.
func (t *Table) rowMarkdown(r *row) string {
	if r.rebuilt || r.raw == "" {
		texts := make([]string, len(r.cells))
		for i := range r.cells {
			texts[i] = encodeCell(r.cells[i].text)
		}
		return t.formatCells(texts)
	}
	type edit struct {
		start, end int
		text       string
	}
	var edits []edit
	for i := range r.cells {
		c := &r.cells[i]
		if c.dirty && c.present {
			edits = append(edits, edit{c.start, c.end, " " + encodeCell(c.text) + " "})
		}
	}
	if len(edits) == 0 {
		return r.raw
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	line := r.raw
	for _, e := range edits {
		line = line[:e.start] + e.text + line[e.end:]
	}
	return line
}

// separator writes the delimiter row, keeping its source when the
// alignments did not change.
func (t *Table) separator() string {
	if !t.sepDirty && t.sepRaw != "" {
		return t.sepRaw
	}
	markers := make([]string, len(t.align))
	for i, a := range t.align {
		markers[i] = a.Marker()
	}
	return t.formatCells(markers)
}

// formatCells writes cells in the table's delimiter style.
func (t *Table) formatCells(texts []string) string {
	var b strings.Builder
	if t.leading {
		b.WriteString("| ")
	}
	for i, s := range texts {
		if i > 0 {
			b.WriteString(" | ")
		}
		b.WriteString(s)
	}
	if t.trailing {
		b.WriteString(" |")
	}
	return b.String()
}

// encodeCell escapes the pipes of a cell's text so they cannot split the
// row. Existing `\|` and `\\` escapes are left as they are, because the
// cell's text is inline Markdown source; a bare pipe becomes `\|`.
func encodeCell(s string) string {
	if !strings.ContainsAny(s, "\\|") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s) + 4)
	for i := 0; i < len(s); {
		if s[i] == '\\' && i+1 < len(s) && (s[i+1] == '\\' || s[i+1] == '|') {
			b.WriteString(s[i : i+2])
			i += 2
			continue
		}
		if s[i] == '|' {
			b.WriteString(`\|`)
			i++
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// Columns returns the table's column count.
func (t *Table) Columns() int { return t.columns }

// RowCount returns the number of rows, the header included.
func (t *Table) RowCount() int { return len(t.rows) }

// DataRows returns the number of data rows, the header excluded.
func (t *Table) DataRows() int { return len(t.rows) - 1 }

// Align returns the alignment of a column.
func (t *Table) Align(col int) Align {
	if col < 0 || col >= len(t.align) {
		return AlignDefault
	}
	return t.align[col]
}

// CellText returns a cell's inline Markdown source, trimmed of padding.
// Row 0 is the header.
func (t *Table) CellText(row, col int) string {
	if row < 0 || row >= len(t.rows) || col < 0 || col >= t.columns {
		return ""
	}
	return t.rows[row].cells[col].text
}
