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
// Active separates "the header's first cell" from "no cell focused": the
// zero value is the latter, because ui.FocusBind reserves the zero value
// for "no member has the focus".
type docTableCell struct {
	Row, Col int
	Active   bool
}

// The table's metrics are fixed here so every table in a document shares
// one rhythm: a 30 DIP body row, a slightly taller header, 10 DIP side
// padding, and column widths measured in character units and clamped so a
// long cell cannot push every other column off the canvas.
const (
	docTableCellPadX     float32 = 10
	docTableCellMinWidth float32 = 88
	docTableCellMaxWidth float32 = 360
	docTableCellCharW    float32 = 7.2
	docTableRowHeight    float32 = 30
	docTableHeaderHeight float32 = 32
	docTableToolbarH     float32 = 32
)

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
	// gridWidth is the measured width of the table's frame in the
	// previous frame; 0 before the first layout. It decides whether the
	// sideways-scroll hint is shown.
	gridWidth float32
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
	active := st.focus
	root.Children(func() {
		ui.Column(c).FillWidth().Background(p.BG).Children(func() {
			st.toolbar(c, p, active, replace)
			ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
			st.grid(c, p, active, replace)
			if st.notice != "" {
				st.noticeRow(c, t, p)
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
	column := make([]string, rows)
	for col := 0; col < cols; col++ {
		for r := 0; r < rows; r++ {
			column[r] = st.values[r][col]
		}
		st.widths[col] = docTableColumnWidth(column)
	}
}

// docTableColumnWidth picks a column's width from its widest cell. The
// header is column[0]. Widths are in character units (a CJK rune counts
// two), clamped to the table's minimum and maximum, so one long Chinese
// cell widens its own column but never squeezes the rest of the table.
func docTableColumnWidth(column []string) float32 {
	units := 4
	for _, s := range column {
		if u := docTableUnits(s); u > units {
			units = u
		}
	}
	w := 2*docTableCellPadX + 4 + float32(units)*docTableCellCharW
	if w < docTableCellMinWidth {
		w = docTableCellMinWidth
	}
	if w > docTableCellMaxWidth {
		w = docTableCellMaxWidth
	}
	return w
}

// docTableTextAlign maps a GFM column alignment to the native text
// alignment of its cells. The unmarked `---` column reads left to right
// like body text.
func docTableTextAlign(a doctable.Align) ui.Align {
	switch a {
	case doctable.AlignCenter:
		return ui.Center
	case doctable.AlignRight:
		return ui.End
	}
	return ui.Start
}

// rowHeight returns the height of row r; the header is slightly taller.
func (st *docTableState) rowHeight(r int) float32 {
	if r == 0 {
		return docTableHeaderHeight
	}
	return docTableRowHeight
}

// totalWidth is the width of the whole grid, separators included.
func (st *docTableState) totalWidth() float32 {
	var total float32
	for i, w := range st.widths {
		if i > 0 {
			total++
		}
		total += w
	}
	return total
}

// emit hands the block's new Markdown to the caller. The caller applies
// it as one transaction, so one toolbar action or one paste is one undo.
func (st *docTableState) emit(replace func(string)) {
	md := st.table.Markdown()
	st.src = md
	st.notice = ""
	replace(md)
}

// valid reports whether cell exists in the model and is focused.
func (st *docTableState) valid(cell docTableCell) bool {
	return cell.Active &&
		cell.Row >= 0 && cell.Row < st.table.RowCount() &&
		cell.Col >= 0 && cell.Col < st.table.Columns()
}

// docTableButton applies the disabled look to a toolbar control: it stops
// taking input and fades, so an unavailable action is visible rather than
// only silently inert.
func docTableButton(e ui.Element, disabled bool) ui.Element {
	e.Disabled(disabled)
	if disabled {
		e.Opacity(0.4)
	}
	return e
}

// toolbar draws the structural controls: add and delete a row or a
// column, and the alignment of the active column. They use the app's
// themed icon buttons; the active alignment is a round button. Controls
// that cannot act on the current selection are disabled rather than
// silently doing nothing.
func (st *docTableState) toolbar(c *ui.Context, p uikit.Palette, active docTableCell, replace func(string)) {
	hasCell := st.valid(active)
	row, col := active.Row, active.Col
	alignCol := 0
	if hasCell {
		alignCol = col
	}
	ui.Row(c).FillWidth().Height(docTableToolbarH).Gap(2).AlignItems(ui.Center).
		KeepFocus().Label("Table tools").Children(func() {
		ui.Text(c, "表格").FontSize(11).FontWeight(600).TextColor(p.Muted).MarginEnd(4)

		addRow := docTableButton(uikit.IconButton(c, p, docTableIconRowAdd, "添加行 / Add row").Label("Add row").Shrink(0),
			st.table.DataRows() >= doctable.MaxDataRows)
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
			st.focus = docTableCell{Row: at, Col: 0, Active: true}
			st.emit(replace)
		}

		delRow := docTableButton(uikit.IconButton(c, p, docTableIconRowDelete, "删除行 / Delete row").Label("Delete row").Shrink(0),
			!hasCell || row < 1)
		if delRow.Clicked() {
			if err := st.table.DeleteRow(row); err != nil {
				st.notice = docTableNotice(err)
				return
			}
			st.rebuild()
			if row >= st.table.RowCount() {
				row = st.table.RowCount() - 1
			}
			st.focus = docTableCell{Row: row, Col: col, Active: true}
			st.emit(replace)
		}

		addCol := docTableButton(uikit.IconButton(c, p, docTableIconColAdd, "添加列 / Add column").Label("Add column").Shrink(0),
			st.table.Columns() >= doctable.MaxColumns)
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
			st.focus = docTableCell{Row: focusRow, Col: at, Active: true}
			st.emit(replace)
		}

		delCol := docTableButton(uikit.IconButton(c, p, docTableIconColDelete, "删除列 / Delete column").Label("Delete column").Shrink(0),
			!hasCell || st.table.Columns() <= 1)
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
			st.focus = docTableCell{Row: row, Col: col, Active: true}
			st.emit(replace)
		}

		ui.Box(c).Width(1).Height(16).Background(p.Hairline).MarginX(4)

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
			if hasCell && st.table.Align(alignCol) == a.align {
				btn = uikit.RoundButton(c, p, a.icon, a.tip)
			} else {
				btn = uikit.IconButton(c, p, a.icon, a.tip)
			}
			btn = docTableButton(btn.Label(a.label).Shrink(0), !hasCell)
			if btn.Clicked() && st.table.Align(alignCol) != a.align {
				if err := st.table.SetAlign(alignCol, a.align); err != nil {
					st.notice = docTableNotice(err)
					continue
				}
				st.emit(replace)
			}
		}

		ui.Box(c).Grow(1)
		if hasCell {
			ui.Text(c, fmt.Sprintf("R%dC%d", row+1, col+1)).FontSize(11).TextColor(p.Faint).
				SingleLine().Tooltip("当前单元格 / Active cell")
		}
	})
}

// grid draws the rows in a horizontally scrolling frame, so a wide table
// scrolls inside the canvas instead of widening it. The frame carries the
// table's outer border; the rows draw only the inner hairlines.
func (st *docTableState) grid(c *ui.Context, p uikit.Palette, active docTableCell, replace func(string)) {
	total := st.totalWidth()
	frame := ui.Box(c).FillWidth().MarginY(4).
		BorderWidth(1).BorderColor(p.Hairline).Radius(6).Clip()
	frame.Children(func() {
		ui.ScrollHorizontal(c).FillWidth().Scrollbars(true).ScrollbarInsets(0, 2, 2, 0).Label("Table grid").Children(func() {
			ui.Column(c).MinWidth(total).Children(func() {
				for r := 0; r < st.table.RowCount(); r++ {
					if r > 0 {
						ui.Box(c).FillWidth().Height(1).Background(p.Hairline)
					}
					st.row(c, p, r, active, replace)
				}
			})
		})
	})
	if w := frame.Bounds().W; w > 0 {
		st.gridWidth = w
	}
	if st.gridWidth > 0 && total > st.gridWidth-2 {
		st.scrollHint(c, p)
	}
}

// row draws one row: its cells and the hairlines between them.
func (st *docTableState) row(c *ui.Context, p uikit.Palette, r int, active docTableCell, replace func(string)) {
	ui.Row(c).Children(func() {
		for col := 0; col < st.table.Columns(); col++ {
			if col > 0 {
				ui.Box(c).Width(1).Height(st.rowHeight(r)).Background(p.Hairline)
			}
			st.cell(c, p, r, col, active, replace)
		}
	})
}

// cell draws one editable cell. The header is bold on a shaded band; a
// data cell stays clean until hovered. The active cell gets an accent
// tint and a 1.5 DIP ring drawn above the input, so its editing state is
// unmistakable without shifting the text by a border width.
func (st *docTableState) cell(c *ui.Context, p uikit.Palette, row, col int, active docTableCell, replace func(string)) {
	isActive := active.Active && active.Row == row && active.Col == col
	box := ui.Box(c).Width(st.widths[col]).Height(st.rowHeight(row)).
		PaddingX(docTableCellPadX).Center()
	switch {
	case isActive:
		box.Background(p.Accent.Alpha(0.07))
	case row == 0:
		box.Background(p.Hover)
	case box.Hovered():
		box.Background(p.Hover)
	}
	box.Children(func() {
		in := ui.TextInputBase(c, &st.values[row][col]).FillWidth().FontSize(13).
			TextColor(p.Text).FocusRing(false)
		in.TextAlign(docTableTextAlign(st.table.Align(col)))
		if row == 0 {
			in.FontWeight(600)
		}
		in.FocusBind(&st.focus, docTableCell{Row: row, Col: col, Active: true})
		in.Label(fmt.Sprintf("Table cell %d,%d", row+1, col+1))
		in.OnChange(func() { st.setCell(row, col, replace) })
		in.OnShortcut(0, ui.KeyTab, func() { st.tabFrom(row, col, 1, replace) })
		in.OnShortcut(ui.Shift, ui.KeyTab, func() { st.tabFrom(row, col, -1, replace) })
		in.OnShortcut(0, ui.KeyEscape, func() { st.focus = docTableCell{} })
		// Enter is deliberately a no-op: an input method uses it to
		// confirm a composition, so it must never move the focus out of
		// the cell while Chinese text is being composed.
		in.OnPaste(func(text string) bool { return st.paste(text, row, col, replace) })
		if isActive {
			ui.Box(c).Absolute().Left(0).Top(0).Right(0).Bottom(0).
				BorderWidth(1.5).BorderColor(p.Accent).Radius(3).PassThrough()
		}
	})
}

// setCell writes one cell's input back to the model. A refused value
// (an oversized cell) is reverted to the model's text, so the input can
// never show text the document does not hold.
func (st *docTableState) setCell(row, col int, replace func(string)) {
	if err := st.table.SetCell(row, col, st.values[row][col]); err != nil {
		st.notice = docTableNotice(err)
		st.values[row][col] = st.table.CellText(row, col)
		return
	}
	st.emit(replace)
}

// scrollHint says the table is wider than the canvas and scrolls
// sideways. It appears only after a layout measured a real overflow, so a
// first frame cannot flash it.
func (st *docTableState) scrollHint(c *ui.Context, p uikit.Palette) {
	ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).PaddingY(2).Label("Table scroll hint").Children(func() {
		ui.Icon(c, docTableIconScroll).FontSize(12).TextColor(p.Muted)
		ui.Text(c, "横向滚动查看更多列 / Scroll sideways for more columns").
			FontSize(12).TextColor(p.Muted).SingleLine()
	})
}

// noticeRow shows the table's last refusal beside a warning icon.
func (st *docTableState) noticeRow(c *ui.Context, t *ui.Theme, p uikit.Palette) {
	ui.Row(c).FillWidth().Gap(4).AlignItems(ui.Center).PaddingY(2).Label("Table notice").Children(func() {
		ui.Icon(c, docTableIconWarning).FontSize(12).TextColor(t.Danger)
		ui.Text(c, st.notice).FontSize(12).TextColor(t.Danger).SingleLine()
	})
}

// tabFrom moves the focus one cell in dir. Tab on the last cell adds a
// data row, within the limit.
func (st *docTableState) tabFrom(row, col, dir int, replace func(string)) {
	st.notice = ""
	cols := st.table.Columns()
	total := st.table.RowCount() * cols
	idx := row*cols + col + dir
	switch {
	case idx < 0:
		st.focus = docTableCell{Row: 0, Col: 0, Active: true}
	case idx >= total:
		if err := st.table.InsertRow(st.table.RowCount()); err != nil {
			st.notice = docTableNotice(err)
			return
		}
		st.rebuild()
		st.focus = docTableCell{Row: st.table.RowCount() - 1, Col: 0, Active: true}
		st.emit(replace)
	default:
		st.focus = docTableCell{Row: idx / cols, Col: idx % cols, Active: true}
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
