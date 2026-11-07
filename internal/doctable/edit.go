package doctable

import (
	"fmt"
	"strings"
)

// SetCell replaces one cell's text. Row 0 is the header. The text is
// inline Markdown source: a bare pipe is escaped on output and existing
// `\|` / `\\` escapes are kept. A line break is refused because a cell is
// one source line.
func (t *Table) SetCell(row, col int, text string) error {
	if row < 0 || row >= len(t.rows) || col < 0 || col >= t.columns {
		return ErrRange
	}
	if strings.ContainsAny(text, "\r\n") {
		return ErrNewline
	}
	if len(text) > MaxCellBytes {
		return fmt.Errorf("%w: %d bytes", ErrCellTooLarge, len(text))
	}
	r := &t.rows[row]
	c := &r.cells[col]
	c.text = text
	c.dirty = true
	if !c.present {
		// The raw line has no span for this cell, so the row is written
		// from the model instead of splicing.
		r.rebuilt = true
	}
	return nil
}

// InsertRow inserts an empty data row at index at, which may be 1 through
// RowCount; the header cannot be displaced.
func (t *Table) InsertRow(at int) error {
	if at < 1 || at > len(t.rows) {
		return ErrRange
	}
	if len(t.rows)-1 >= MaxDataRows {
		return fmt.Errorf("%w: %d data rows", ErrTooLarge, len(t.rows)-1)
	}
	r := row{cells: make([]cell, t.columns), rebuilt: true}
	t.rows = append(t.rows, row{})
	copy(t.rows[at+1:], t.rows[at:])
	t.rows[at] = r
	return nil
}

// DeleteRow deletes the data row at index at; the header cannot be
// deleted.
func (t *Table) DeleteRow(at int) error {
	if at < 1 || at >= len(t.rows) {
		return ErrRange
	}
	t.rows = append(t.rows[:at], t.rows[at+1:]...)
	return nil
}

// InsertColumn inserts an empty column at index at, which may be 0 through
// Columns.
func (t *Table) InsertColumn(at int) error {
	if at < 0 || at > t.columns {
		return ErrRange
	}
	if t.columns >= MaxColumns {
		return fmt.Errorf("%w: %d columns", ErrTooLarge, t.columns)
	}
	for i := range t.rows {
		r := &t.rows[i]
		r.cells = append(r.cells, cell{})
		copy(r.cells[at+1:], r.cells[at:])
		r.cells[at] = cell{}
		r.rebuilt = true
	}
	t.align = append(t.align, AlignDefault)
	copy(t.align[at+1:], t.align[at:])
	t.align[at] = AlignDefault
	t.columns++
	t.sepDirty = true
	return nil
}

// DeleteColumn deletes the column at index at. A table keeps at least one
// column.
func (t *Table) DeleteColumn(at int) error {
	if at < 0 || at >= t.columns {
		return ErrRange
	}
	if t.columns <= 1 {
		return ErrLastColumn
	}
	for i := range t.rows {
		r := &t.rows[i]
		r.cells = append(r.cells[:at], r.cells[at+1:]...)
		r.rebuilt = true
	}
	t.align = append(t.align[:at], t.align[at+1:]...)
	t.columns--
	t.sepDirty = true
	return nil
}

// SetAlign sets a column's alignment.
func (t *Table) SetAlign(col int, a Align) error {
	if col < 0 || col >= t.columns {
		return ErrRange
	}
	if a > AlignRight {
		return ErrRange
	}
	if t.align[col] == a {
		return nil
	}
	t.align[col] = a
	t.sepDirty = true
	return nil
}

// PasteTSV writes a rectangular tab-separated block starting at the cell
// (row, col). Rows are separated by line breaks and cells by tabs; a
// single trailing line break is ignored. The table grows to the right and
// at the bottom as needed, within the limits, and every cell is written
// by the one call so a caller can commit the result as one transaction.
func (t *Table) PasteTSV(text string, row, col int) error {
	if row < 0 || row >= len(t.rows) || col < 0 || col >= t.columns {
		return ErrRange
	}
	grid, err := parseTSV(text)
	if err != nil {
		return err
	}
	height, width := len(grid), len(grid[0])
	if row+height-1 > MaxDataRows {
		return fmt.Errorf("%w: %d data rows", ErrTooLarge, row+height-1)
	}
	if col+width > MaxColumns {
		return fmt.Errorf("%w: %d columns", ErrTooLarge, col+width)
	}
	for t.columns < col+width {
		if err := t.InsertColumn(t.columns); err != nil {
			return err
		}
	}
	for len(t.rows) < row+height {
		if err := t.InsertRow(len(t.rows)); err != nil {
			return err
		}
	}
	for i := range grid {
		for j := range grid[i] {
			if err := t.SetCell(row+i, col+j, grid[i][j]); err != nil {
				return err
			}
		}
	}
	return nil
}

// parseTSV splits a pasted block and checks that it is rectangular and
// within the limits. Trailing line breaks are ignored, as spreadsheet
// copies usually carry one.
func parseTSV(text string) ([][]string, error) {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.TrimRight(text, "\n")
	if text == "" {
		return nil, ErrEmptyPaste
	}
	lines := strings.Split(text, "\n")
	if len(lines) > MaxDataRows+1 {
		return nil, fmt.Errorf("%w: %d rows", ErrTooLarge, len(lines))
	}
	grid := make([][]string, len(lines))
	width := -1
	for i, line := range lines {
		fields := strings.Split(line, "\t")
		if width < 0 {
			width = len(fields)
			if width > MaxColumns {
				return nil, fmt.Errorf("%w: %d columns", ErrTooLarge, width)
			}
		} else if len(fields) != width {
			return nil, ErrNotRectangular
		}
		for _, s := range fields {
			if len(s) > MaxCellBytes {
				return nil, fmt.Errorf("%w: %d bytes", ErrCellTooLarge, len(s))
			}
		}
		grid[i] = fields
	}
	return grid, nil
}
