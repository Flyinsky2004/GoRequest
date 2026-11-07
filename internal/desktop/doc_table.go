package desktop

import (
	"errors"
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/doctable"
)

// This file renders one GFM table block of the document canvas as an
// editable native table. It owns no document state: the caller passes the
// block's source and a replace callback, and the callback receives only
// the block's new Markdown, so the canvas keeps the buffer, the version,
// the dirty flag and the undo transaction.
//
// The state of one table lives on the element the caller passes as root
// (ui.Local), so the canvas must pass one element per table block. Two
// tables never share a cell being edited, a column width or a notice.

// docTableKey names the per-block state in ui.Local.
type docTableKey struct{}

// docTableCell identifies one cell for focus binding and the toolbar.
type docTableCell struct {
	Row, Col int
}

// docTableState is what one table block keeps between frames.
type docTableState struct {
	// src is the source last parsed or handed to the synchronous replace
	// callback. Any other source is an external edit or undo and must be
	// reparsed, even when it equals the original text.
	src    string
	table  *doctable.Table
	values [][]string
	widths []float32
	focus  docTableCell
	notice string
}

// renderDocTable renders a complete GFM table block as an editable table
// inside root and returns whether the block was supported. source is the
// whole block; replace receives the block's new Markdown after an edit and
// the caller applies it as one buffer transaction. The caller owns the
// document buffer, its version, the dirty flag and undo; this renderer
// only returns the new block text.
//
// root must be the element of this table block, so ui.Local isolates the
// table's state. A block that is not a GFM table, or one beyond
// doctable's limits, returns false and draws nothing, which lets the
// canvas fall back to showing it as source.
func renderDocTable(c *ui.Context, t *ui.Theme, root ui.Element, source string, replace func(string)) bool {
	if c == nil || replace == nil || !root.Valid() {
		return false
	}
	if t == nil {
		t = c.Theme()
	}
	st := ui.Local(root, docTableKey{}, func() docTableState { return docTableState{} })
	if st == nil {
		return false
	}
	if st.table == nil || source != st.src {
		tb, err := doctable.Parse(source)
		if err != nil {
			return false
		}
		st.table = tb
		st.src = source
		st.focus = docTableCell{}
		st.notice = ""
		st.rebuild()
	}

	p := uikit.PaletteFor(t)
	active := ui.FocusedValue(c, &st.focus)
	root.Children(func() {
		ui.Column(c).FillWidth().Children(func() {
			st.toolbar(c, p, active, replace)
			st.grid(c, p, active, replace)
			if st.notice != "" {
				ui.Text(c, st.notice).FontSize(12).TextColor(t.Danger)
			}
		})
	})
	return true
}

// rebuild reads the model into the strings the inputs edit and the
// column widths. It runs when the source is parsed and after a structural
// edit.
func (st *docTableState) rebuild() {
	rows, cols := st.table.RowCount(), st.table.Columns()
	st.values = make([][]string, rows)
	for r := 0; r < rows; r++ {
		st.values[r] = make([]string, cols)
		for col := 0; col < cols; col++ {
			st.values[r][col] = st.table.CellText(r, col)
		}
	}
	st.widths = make([]float32, cols)
	for col := 0; col < cols; col++ {
		units := 4
		for r := 0; r < rows; r++ {
			if u := docTableUnits(st.table.CellText(r, col)); u > units {
				units = u
			}
		}
		w := float32(32 + units*7)
		if w < 96 {
			w = 96
		}
		if w > 320 {
			w = 320
		}
		st.widths[col] = w
	}
}

// emit hands the block's new Markdown to the caller. The caller applies
// it as one transaction, so one toolbar action or one paste is one undo.
func (st *docTableState) emit(replace func(string)) {
	md := st.table.Markdown()
	st.src = md
	st.notice = ""
	replace(md)
}

// valid reports whether cell exists in the model.
func (st *docTableState) valid(cell docTableCell) bool {
	return cell.Row >= 0 && cell.Row < st.table.RowCount() &&
		cell.Col >= 0 && cell.Col < st.table.Columns()
}

// toolbar draws the structural controls: add and delete a row or a
// column, and the alignment of the active column. They use the app's
// themed icon buttons; the active alignment is a round button.
func (st *docTableState) toolbar(c *ui.Context, p uikit.Palette, active docTableCell, replace func(string)) {
	hasCell := st.valid(active)
	row, col := active.Row, active.Col
	alignCol := 0
	if hasCell {
		alignCol = col
	}
	ui.Row(c).Gap(4).PaddingY(2).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Label("Table tools").Children(func() {
		addRow := uikit.IconButton(c, p, docTableIconRowAdd, "添加行 / Add row").Label("Add row")
		if addRow.Clicked() {
			at := st.table.RowCount()
			if hasCell && row >= 1 {
				at = row + 1
			}
			if err := st.table.InsertRow(at); err != nil {
				st.notice = docTableNotice(err)
				return
			}
			st.rebuild()
			st.focus = docTableCell{Row: at, Col: 0}
			st.emit(replace)
		}

		delRow := uikit.IconButton(c, p, docTableIconRowDelete, "删除行 / Delete row").Label("Delete row")
		if !hasCell || row < 1 {
			delRow.Disabled(true)
		}
		if delRow.Clicked() {
			if err := st.table.DeleteRow(row); err != nil {
				st.notice = docTableNotice(err)
				return
			}
			st.rebuild()
			if row >= st.table.RowCount() {
				row = st.table.RowCount() - 1
			}
			st.focus = docTableCell{Row: row, Col: col}
			st.emit(replace)
		}

		addCol := uikit.IconButton(c, p, docTableIconColAdd, "添加列 / Add column").Label("Add column")
		if addCol.Clicked() {
			at := st.table.Columns()
			if hasCell {
				at = col + 1
			}
			if err := st.table.InsertColumn(at); err != nil {
				st.notice = docTableNotice(err)
				return
			}
			st.rebuild()
			focusRow := 0
			if hasCell {
				focusRow = row
			}
			st.focus = docTableCell{Row: focusRow, Col: at}
			st.emit(replace)
		}

		delCol := uikit.IconButton(c, p, docTableIconColDelete, "删除列 / Delete column").Label("Delete column")
		if !hasCell || st.table.Columns() <= 1 {
			delCol.Disabled(true)
		}
		if delCol.Clicked() {
			if err := st.table.DeleteColumn(col); err != nil {
				st.notice = docTableNotice(err)
				return
			}
			st.rebuild()
			if col >= st.table.Columns() {
				col = st.table.Columns() - 1
			}
			if row >= st.table.RowCount() {
				row = st.table.RowCount() - 1
			}
			st.focus = docTableCell{Row: row, Col: col}
			st.emit(replace)
		}

		aligns := []struct {
			align doctable.Align
			icon  *ui.SVG
			label string
			tip   string
		}{
			{doctable.AlignLeft, docTableIconAlignLeft, "Align left", "左对齐 / Align left"},
			{doctable.AlignCenter, docTableIconAlignCenter, "Align center", "居中 / Align center"},
			{doctable.AlignRight, docTableIconAlignRight, "Align right", "右对齐 / Align right"},
		}
		for _, a := range aligns {
			var btn ui.Element
			if st.table.Align(alignCol) == a.align {
				btn = uikit.RoundButton(c, p, a.icon, a.tip)
			} else {
				btn = uikit.IconButton(c, p, a.icon, a.tip)
			}
			btn = btn.Label(a.label)
			if btn.Clicked() {
				if err := st.table.SetAlign(alignCol, a.align); err != nil {
					st.notice = docTableNotice(err)
					return
				}
				st.emit(replace)
			}
		}
	})
}

// grid draws the rows in a horizontally scrolling column, so a wide table
// scrolls inside the canvas instead of widening it.
func (st *docTableState) grid(c *ui.Context, p uikit.Palette, active docTableCell, replace func(string)) {
	var total float32
	for _, w := range st.widths {
		total += w
	}
	ui.ScrollHorizontal(c).FillWidth().Label("Table grid").Children(func() {
		ui.Column(c).MinWidth(total).Children(func() {
			for r := 0; r < st.table.RowCount(); r++ {
				ui.Row(c).Children(func() {
					for col := 0; col < st.table.Columns(); col++ {
						st.cell(c, p, r, col, active, replace)
					}
				})
			}
		})
	})
}

// cell draws one editable cell. The header is bold and shaded; the
// focused cell carries the accent border. The input edits the cell's
// inline Markdown source, which keeps bold, code and links in the cell.
func (st *docTableState) cell(c *ui.Context, p uikit.Palette, row, col int, active docTableCell, replace func(string)) {
	box := ui.Box(c).Width(st.widths[col]).PaddingX(8).PaddingY(3)
	if row == 0 {
		box.Background(p.Hover)
	}
	if active.Row == row && active.Col == col {
		box.BorderWidth(1).BorderColor(p.Accent)
	} else {
		box.BorderWidth(0, 1, 1, 0).BorderColor(p.Hairline)
	}
	box.Children(func() {
		in := ui.TextInputBase(c, &st.values[row][col]).FillWidth().FontSize(13).TextColor(p.Text).FocusRing(false)
		if row == 0 {
			in.FontWeight(600)
		}
		in.FocusBind(&st.focus, docTableCell{Row: row, Col: col})
		in.Label(fmt.Sprintf("Table cell %d,%d", row+1, col+1))
		in.OnChange(func() {
			if err := st.table.SetCell(row, col, st.values[row][col]); err != nil {
				st.notice = docTableNotice(err)
				return
			}
			st.emit(replace)
		})
		in.OnShortcut(0, ui.KeyTab, func() { st.tabFrom(row, col, 1, replace) })
		in.OnShortcut(ui.Shift, ui.KeyTab, func() { st.tabFrom(row, col, -1, replace) })
		in.OnShortcut(0, ui.KeyEscape, func() { st.focus = docTableCell{} })
		// Enter is deliberately a no-op: an input method uses it to
		// confirm a composition, so it must never move the focus out of
		// the cell while Chinese text is being composed.
		in.OnPaste(func(text string) bool { return st.paste(text, row, col, replace) })
	})
}

// tabFrom moves the focus one cell in dir. Tab on the last cell adds a
// data row, within the limit.
func (st *docTableState) tabFrom(row, col, dir int, replace func(string)) {
	cols := st.table.Columns()
	total := st.table.RowCount() * cols
	idx := row*cols + col + dir
	switch {
	case idx < 0:
		st.focus = docTableCell{Row: 0, Col: 0}
	case idx >= total:
		if err := st.table.InsertRow(st.table.RowCount()); err != nil {
			st.notice = docTableNotice(err)
			return
		}
		st.rebuild()
		st.focus = docTableCell{Row: st.table.RowCount() - 1, Col: 0}
		st.emit(replace)
	default:
		st.focus = docTableCell{Row: idx / cols, Col: idx % cols}
	}
}

// paste takes a clipboard block that spans cells. A single-line paste
// without a tab stays the input's own insertion; a tab- or line-separated
// block is written as one rectangular paste, and one replace call. A
// block that is not rectangular or is beyond the limits is refused and
// reported, leaving the table as it was.
func (st *docTableState) paste(text string, row, col int, replace func(string)) bool {
	if !strings.ContainsAny(text, "\t\n\r") {
		return false
	}
	if err := st.table.PasteTSV(text, row, col); err != nil {
		st.notice = docTableNotice(err)
		return true
	}
	st.rebuild()
	st.emit(replace)
	return true
}

// docTableBlock locates the GFM table block at or after pos and returns
// its source range. The canvas uses it because the Markdown parser's
// Table block is not a reliable range: its End stops at the last cell's
// text, and a table that follows a paragraph without a blank line keeps
// the paragraph's start. pos is the block's Start; the returned range
// runs from the header line through the last table line's line ending,
// which is exactly what renderDocTable parses.
func docTableBlock(src string, pos int) (start, end int, ok bool) {
	if pos < 0 || pos > len(src) {
		return 0, 0, false
	}
	lineStart := strings.LastIndexByte(src[:pos], '\n') + 1
	for i := lineStart; i < len(src); {
		lineEnd := strings.IndexByte(src[i:], '\n')
		next := len(src)
		if lineEnd >= 0 {
			next = i + lineEnd + 1
		}
		if i > 0 {
			prevStart := strings.LastIndexByte(src[:i-1], '\n') + 1
			if _, err := doctable.Parse(src[prevStart:i] + src[i:next]); err == nil {
				return prevStart, docTableBlockEnd(src, prevStart), true
			}
		}
		i = next
	}
	return 0, 0, false
}

// docTableBlockEnd returns the end of the table block that starts at
// start: the first blank line, or the end of the source.
func docTableBlockEnd(src string, start int) int {
	end := start
	for end < len(src) {
		i := strings.IndexByte(src[end:], '\n')
		if i < 0 {
			return len(src)
		}
		if strings.TrimSpace(src[end:end+i]) == "" {
			return end
		}
		end += i + 1
	}
	return end
}

// docTableUnits approximates a text's display width in character units:
// a CJK or other wide rune counts two.
func docTableUnits(s string) int {
	n := 0
	for _, r := range s {
		if r >= 0x2e80 {
			n += 2
		} else {
			n++
		}
	}
	return n
}

// docTableNotice turns a model error into a short message the table
// shows beside its controls.
func docTableNotice(err error) string {
	switch {
	case errors.Is(err, doctable.ErrNotRectangular):
		return "粘贴内容不是矩形 / pasted data is not rectangular"
	case errors.Is(err, doctable.ErrTooLarge):
		return "超出表格上限（200 行 × 30 列）/ table limit reached (200 rows × 30 columns)"
	case errors.Is(err, doctable.ErrCellTooLarge):
		return "单元格内容过大 / cell is too large"
	case errors.Is(err, doctable.ErrNewline):
		return "单元格不能包含换行 / a cell cannot contain a line break"
	case errors.Is(err, doctable.ErrLastColumn):
		return "至少保留一列 / a table keeps at least one column"
	case errors.Is(err, doctable.ErrEmptyPaste):
		return "没有可粘贴的表格数据 / nothing to paste"
	}
	return err.Error()
}
