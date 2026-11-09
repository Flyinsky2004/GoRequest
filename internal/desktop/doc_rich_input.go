package desktop

// Native rich document input. All content geometry comes from TextLayout;
// delimiter bytes never consume layout space. The backing editor.State keeps
// source offsets, so existing formatting/transactions remain source based.
import (
	"slices"
	"strings"
	"unicode"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/docedit"
)

type docRichRow struct {
	layout        *ui.TextLayout
	start, end    int // UTF-16 visible range
	x, y, h       float32
	marker        string
	markerX       float32
	markerLayout  *ui.TextLayout
	task, checked bool
	sourceLine    int
}
type docRichInput struct {
	ed               *editor.State
	view             docedit.InlineView
	rows             []docRichRow
	font             ui.Font
	theme            *markdown.Theme
	width, height    float32
	bounds           ui.Rect
	align            ui.Align
	prefixes         []docedit.ListLine
	literal          bool
	viewLiteral      bool
	viewPrefixes     []docedit.ListLine
	idle             bool
	changed          func()
	structure        func(ui.InputEvent) bool
	beforeReplace    func(string) bool
	toggleTask       func(int)
	mentionInput     func(ui.InputEvent) bool
	mentionIndex     int
	mentionDismissed string
	lastText         string
	lastSource       string
	prefixKey        string
	lastFont         ui.Font
	lastTheme        markdown.Theme
	lastPrefixes     []docedit.ListLine
	lastWidth        float32
	lastAlign        ui.Align
	dragging         bool
	marked           bool
	mark             ui.TextInputRange
	preedit          string
	preselect        ui.TextInputRange
}

func docInlineSpans(v docedit.InlineView, th *markdown.Theme) []ui.Span {
	spans := make([]ui.Span, 0, len(v.Runs))
	for _, r := range v.Runs {
		sp := ui.Span{Text: v.Text[r.Start:r.End]}
		if r.Strong {
			sp.Weight = th.StrongWeight
			sp.Color = th.Strong
		}
		sp.Italic = r.Emphasis
		sp.Strikethrough = r.Strike
		if r.Link != "" {
			sp.Color = th.Link
			sp.Weight = max(sp.Weight, th.LinkWeight)
			sp.Underline = true
		}
		if r.Code {
			sp.Font = th.CodeFont
			sp.Size = th.FontSize * th.CodeSize
			sp.Background = th.CodeBackground
			sp.Color = th.Code
			sp.Radius = th.FontSize * 0.2
		}
		spans = append(spans, sp)
	}
	return spans
}
func (w *docRichInput) refresh() {
	if w.marked {
		return
	}
	src := w.ed.Buffer.String()
	if src == w.view.Source && w.view.Runs != nil && w.literal == w.viewLiteral && slices.Equal(w.prefixes, w.viewPrefixes) {
		return
	}
	w.viewLiteral = w.literal
	w.viewPrefixes = slices.Clone(w.prefixes)
	if w.literal {
		w.view = docedit.InlineView{Source: src, Text: src, Runs: []docedit.InlineRun{{End: len(src), SourceEnd: len(src)}}}
	} else {
		if len(w.prefixes) > 0 {
			w.view = docedit.NewListInlineView(src, w.prefixes)
		} else {
			w.view = docedit.NewInlineView(src)
		}
	}
}
func (w *docRichInput) display() string {
	if !w.marked {
		return w.view.Text
	}
	s := w.view.Text
	a, b := ui.UTF16ByteOffset(s, w.mark.Start), ui.UTF16ByteOffset(s, w.mark.End)
	return s[:a] + w.preedit + s[b:]
}
func (w *docRichInput) layout(width float32) {
	w.refresh()
	text := w.display()
	style := *w.theme
	style.CodeColors = nil
	if w.rows != nil && text == w.lastText && w.view.Source == w.lastSource && w.font == w.lastFont && style == w.lastTheme && width == w.lastWidth && w.align == w.lastAlign && slices.Equal(w.prefixes, w.lastPrefixes) {
		return
	}
	w.lastText = text
	w.lastSource = w.view.Source
	w.lastFont = w.font
	w.lastTheme = style
	w.lastWidth = width
	w.lastAlign = w.align
	w.lastPrefixes = slices.Clone(w.prefixes)
	w.width = max(1, width)
	w.rows = nil
	w.height = 0
	spans := w.styledSpans()
	gutters := map[int]float32{}
	for _, meta := range w.prefixes {
		width := w.theme.ListIndent * w.font.Size
		if meta.Marker != "" && !meta.Task && strings.ContainsAny(meta.Marker, "0123456789") {
			mw, _ := ui.ShapeText(meta.Marker, w.font, 0).Size()
			width = max(width, mw+w.font.Size*0.5)
		}
		gutters[meta.List] = max(gutters[meta.List], width)
	}
	depthWidths := map[int]float32{}
	byteStart, units := 0, 0
	lines := strings.Split(text, "\n")
	if len(w.prefixes) == 0 {
		lines = []string{text}
	}
	for _, line := range lines {
		sourceByte := byteStart
		if w.marked {
			a, b := ui.UTF16ByteOffset(w.view.Text, w.mark.Start), ui.UTF16ByteOffset(w.view.Text, w.mark.End)
			if sourceByte > a {
				if sourceByte < a+len(w.preedit) {
					sourceByte = a
				} else {
					sourceByte += b - a - len(w.preedit)
				}
			}
		}
		i := strings.Count(w.view.Source[:w.view.SourceOffset(sourceByte)], "\n")
		var runs []ui.Span
		offset := 0
		for _, sp := range spans {
			a, b := max(byteStart, offset), min(byteStart+len(line), offset+len(sp.Text))
			if b > a {
				cp := sp
				cp.Text = sp.Text[a-offset : b-offset]
				runs = append(runs, cp)
			}
			offset += len(sp.Text)
		}
		if len(runs) == 0 {
			runs = []ui.Span{{Text: line}}
		}
		row := docRichRow{start: units, end: units + ui.UTF16Len(line), y: w.height, sourceLine: i}
		if i < len(w.prefixes) {
			meta := w.prefixes[i]
			row.marker = meta.Marker
			row.task = meta.Task
			row.checked = meta.Checked
			gutter := gutters[meta.List]
			depthWidths[meta.Depth] = gutter
			for depth := 0; depth <= meta.Depth; depth++ {
				row.x += depthWidths[depth]
			}
			if row.marker != "" {
				marker := row.marker
				if row.task {
					marker = "☐"
					if row.checked {
						marker = "☑"
					}
				} else if !strings.ContainsAny(marker, "0123456789") {
					marker = []string{"•", "◦", "▪"}[min(meta.Depth, 2)]
				}
				row.markerLayout = ui.ShapeText(marker, w.font, 0)
				markerWidth, _ := row.markerLayout.Size()
				row.markerX = row.x - w.font.Size*0.4 - markerWidth
				if !strings.ContainsAny(row.marker, "0123456789") {
					row.markerX = row.x - gutter + (gutter-w.font.Size*0.5-markerWidth)/2
				}
			}
			if i > 0 && meta.Marker != "" && meta.GapBefore && strings.TrimSpace(strings.Split(w.view.Source, "\n")[i-1]) != "" {
				gap := w.theme.ItemSpacing
				if meta.Loose {
					gap = w.theme.BlockSpacing
				}
				w.height += gap * w.font.Size
				row.y = w.height
			}
		}
		row.layout = ui.ShapeRichTextWith(runs, w.font, ui.TextShape{Width: max(1, w.width-row.x), Align: w.align})
		_, row.h = row.layout.Size()
		row.h = max(row.h, w.font.Size*w.font.LineHeight)
		w.height += row.h
		w.rows = append(w.rows, row)
		units = row.end + 1
		byteStart += len(line) + 1
	}
}
func (w *docRichInput) caret(index int) ui.Rect {
	for i, r := range w.rows {
		if index <= r.end || i == len(w.rows)-1 {
			b := r.layout.Caret(max(0, index-r.start))
			b.X += r.x
			b.Y += r.y
			return b
		}
	}
	return ui.Rect{W: 1, H: w.font.Size * w.font.LineHeight}
}
func (w *docRichInput) selection() (int, int) {
	if w.marked {
		return w.mark.Start + w.preselect.Start, w.mark.Start + w.preselect.End
	}
	s := w.ed.Selection()
	return ui.UTF16Len(w.view.Text[:w.view.VisibleOffset(s.Anchor)]), ui.UTF16Len(w.view.Text[:w.view.VisibleOffset(s.Head)])
}
func (w *docRichInput) selectVisible(a, b int) {
	text := w.view.Text
	va, vb := ui.UTF16ByteOffset(text, a), ui.UTF16ByteOffset(text, b)
	if a == b {
		off := w.view.SourceOffset(va)
		w.ed.Select(off, off)
		return
	}
	start, end := w.view.SourceRange(min(va, vb), max(va, vb))
	if a > b {
		w.ed.Select(end, start)
	} else {
		w.ed.Select(start, end)
	}
}
func (w *docRichInput) replace(r ui.TextInputRange, text string) {
	if w.beforeReplace != nil && w.beforeReplace(text) {
		return
	}
	w.refresh()
	a, b := ui.UTF16ByteOffset(w.view.Text, r.Start), ui.UTF16ByteOffset(w.view.Text, r.End)
	src, caret := w.view.Replace(a, b, text)
	old := w.ed.Buffer.String()
	// Slash/link tools may select a hidden destination. Its exact source
	// range remains authoritative even though both visible edges coincide.
	selected := w.ed.Selection()
	if r == w.Selection().Range {
		src = old[:selected.Start()] + text + old[selected.End():]
		caret = selected.Start() + len(text)
	}
	// One minimal local source edit; the owner commits one canonical transaction.
	start, end, replacement := docedit.MinimalChange(old, src)
	w.ed.Buffer.Edit(editor.Edit{Start: start, End: end, Text: replacement})
	w.ed.Select(caret, caret)
	if w.changed != nil {
		w.changed()
	}
	w.refresh()
	w.layout(w.width)
}
func (w *docRichInput) TextForRange(r ui.TextInputRange) (string, ui.TextInputRange) {
	s := w.display()
	a, b := ui.UTF16ByteOffset(s, r.Start), ui.UTF16ByteOffset(s, r.End)
	b = max(a, b)
	return s[a:b], ui.TextInputRange{Start: ui.UTF16Len(s[:a]), End: ui.UTF16Len(s[:b])}
}
func (w *docRichInput) Selection() ui.TextInputSelection {
	a, b := w.selection()
	return ui.TextInputSelection{Range: ui.TextInputRange{Start: min(a, b), End: max(a, b)}, Reversed: b < a}
}
func (w *docRichInput) MarkedRange() (ui.TextInputRange, bool) {
	return ui.TextInputRange{Start: w.mark.Start, End: w.mark.Start + ui.UTF16Len(w.preedit)}, w.marked
}
func (w *docRichInput) ReplaceText(r *ui.TextInputRange, s string) {
	if !w.marked && w.beforeReplace != nil && w.beforeReplace(s) {
		return
	}
	target := w.Selection().Range
	if w.marked {
		target = w.mark
	}
	if r != nil {
		target = *r
		if w.marked { // native range is in the preedit overlay
			end := w.mark.Start + ui.UTF16Len(w.preedit)
			if target.Start >= w.mark.Start && target.End <= end {
				target = w.mark
			}
		}
	}
	w.marked = false
	w.preedit = ""
	w.replace(target, s)
}
func (w *docRichInput) SetMarkedText(r *ui.TextInputRange, s string, selected ui.TextInputRange) {
	if !w.marked {
		w.mark = w.Selection().Range
		if r != nil {
			w.mark = *r
		}
	}
	w.marked = true
	w.preedit = s
	w.preselect = selected
	w.layout(w.width)
}
func (w *docRichInput) UnmarkText() {
	if w.marked {
		s := w.preedit
		r := w.mark
		w.marked = false
		w.preedit = ""
		w.replace(r, s)
	}
}
func (w *docRichInput) BoundsForRange(r ui.TextInputRange) (ui.Rect, ui.TextInputRange, bool) {
	_, actual := w.TextForRange(r)
	bounds := w.caret(actual.Start)
	if actual.End > actual.Start {
		found := false
		for _, row := range w.rows {
			if actual.Start > row.end || actual.End < row.start {
				continue
			}
			ranges := row.layout.SelectionRects(ui.TextInputRange{Start: max(0, actual.Start-row.start), End: min(row.end-row.start, actual.End-row.start)})
			for _, box := range ranges {
				box.X += row.x
				box.Y += row.y
				if !found {
					bounds = box
					found = true
				} else {
					right, bottom := max(bounds.X+bounds.W, box.X+box.W), max(bounds.Y+bounds.H, box.Y+box.H)
					bounds.X = min(bounds.X, box.X)
					bounds.Y = min(bounds.Y, box.Y)
					bounds.W = right - bounds.X
					bounds.H = bottom - bounds.Y
				}
			}
		}
	}
	return bounds, actual, len(w.rows) > 0
}
func (w *docRichInput) IndexForPoint(p ui.Point) (int, bool) {
	for i, r := range w.rows {
		if p.Y < r.y+r.h || i == len(w.rows)-1 {
			return r.start + r.layout.IndexAt(ui.Point{X: p.X - r.x, Y: p.Y - r.y}), true
		}
	}
	return 0, false
}
func (w *docRichInput) input(c ui.Services, ev ui.InputEvent) bool {
	if w.marked {
		return false
	} // candidate keys belong to native input services
	if ev.Kind == ui.InputPointerDown && ev.Button == 0 {
		for _, row := range w.rows {
			if row.task && ev.Y >= row.y && ev.Y < row.y+row.h && ev.X < row.x && w.toggleTask != nil {
				w.toggleTask(row.sourceLine)
				return true
			}
		}
		at, _ := w.IndexForPoint(ui.Point{X: ev.X, Y: ev.Y})
		if ev.Mods&(ui.Cmd|ui.Ctrl) != 0 && w.ed.OnDefinition != nil {
			w.ed.OnDefinition(w.view.SourceOffset(ui.UTF16ByteOffset(w.view.Text, at)))
			return true
		}
		a, _ := w.selection()
		if ev.Mods&ui.Shift == 0 {
			a = at
		}
		if ev.Clicks >= 3 {
			for _, row := range w.rows {
				if at >= row.start && at <= row.end {
					a = row.start
					at = row.end
					break
				}
			}
		} else if ev.Clicks == 2 {
			a = w.wordBoundary(at, -1)
			at = w.wordBoundary(at, 1)
		}
		w.selectVisible(a, at)
		w.dragging = true
		return true
	}
	if ev.Kind == ui.InputPointerMove && w.dragging {
		at, _ := w.IndexForPoint(ui.Point{X: ev.X, Y: ev.Y})
		a, _ := w.selection()
		w.selectVisible(a, at)
		return true
	}
	if ev.Kind == ui.InputPointerUp {
		w.dragging = false
		return true
	}
	if w.mentionInput != nil && w.mentionInput(ev) {
		return true
	}
	if w.structure != nil && w.structure(ev) {
		return true
	}
	if w.ed.OnInput != nil && w.ed.OnInput(ev) {
		w.refresh()
		return true
	}
	a, b := w.selection()
	sel := ui.TextInputRange{Start: min(a, b), End: max(a, b)}
	switch ev.Kind {
	case ui.InputText:
		w.replace(sel, ev.Text)
		return true
	case ui.InputCommand:
		switch ev.Text {
		case "selectAll":
			w.selectVisible(0, ui.UTF16Len(w.view.Text))
			return true
		case "copy", "cut":
			s, _ := w.TextForRange(sel)
			c.WriteClipboard(s)
			if ev.Text == "cut" {
				w.replace(sel, "")
			}
			return true
		case "paste", "pasteAndMatchStyle":
			w.replace(sel, c.ReadClipboard())
			return true
		case "delete":
			w.replace(sel, "")
			return true
		}
	case ui.InputSelect:
		runes := []rune(w.view.Text)
		aa := string(runes[:max(0, min(ev.Start, len(runes)))])
		bb := string(runes[:max(0, min(ev.End, len(runes)))])
		w.selectVisible(ui.UTF16Len(aa), ui.UTF16Len(bb))
		return true
	case ui.InputKeyDown:
		if ev.Mods == ui.Cmd || ev.Mods == ui.Ctrl {
			switch ev.Key {
			case ui.KeyA:
				w.selectVisible(0, ui.UTF16Len(w.view.Text))
				return true
			case ui.KeyC, ui.KeyX:
				s, _ := w.TextForRange(sel)
				c.WriteClipboard(s)
				if ev.Key == ui.KeyX {
					w.replace(sel, "")
				}
				return true
			case ui.KeyV:
				w.replace(sel, c.ReadClipboard())
				return true
			}
		}
		switch ev.Key {
		case ui.KeyEnter:
			w.replace(sel, "\n")
			return true
		case ui.KeyBackspace, ui.KeyDelete:
			if sel.Start == sel.End {
				if ev.Key == ui.KeyBackspace {
					sel.Start = w.boundary(b, -1)
					if ev.Mods&ui.Alt != 0 {
						sel.Start = w.wordBoundary(b, -1)
					}
				} else {
					sel.End = w.boundary(b, 1)
					if ev.Mods&ui.Alt != 0 {
						sel.End = w.wordBoundary(b, 1)
					}
				}
			}
			w.replace(sel, "")
			return true
		case ui.KeyLeft, ui.KeyRight, ui.KeyUp, ui.KeyDown, ui.KeyHome, ui.KeyEnd:
			at := b
			switch ev.Key {
			case ui.KeyLeft:
				at = w.boundary(b, -1)
				if ev.Mods&ui.Alt != 0 {
					at = w.wordBoundary(b, -1)
				}
			case ui.KeyRight:
				at = w.boundary(b, 1)
				if ev.Mods&ui.Alt != 0 {
					at = w.wordBoundary(b, 1)
				}
			case ui.KeyUp, ui.KeyDown:
				r := w.caret(b)
				dy := w.font.Size * w.font.LineHeight
				if ev.Key == ui.KeyUp {
					dy = -dy
				}
				at, _ = w.IndexForPoint(ui.Point{X: r.X, Y: r.Y + r.H/2 + dy})
			case ui.KeyHome:
				for _, r := range w.rows {
					if b >= r.start && b <= r.end {
						at = r.start
						break
					}
				}
			case ui.KeyEnd:
				for _, r := range w.rows {
					if b >= r.start && b <= r.end {
						at = r.end
						break
					}
				}
			}
			if ev.Mods&ui.Cmd != 0 && (ev.Key == ui.KeyLeft || ev.Key == ui.KeyRight) {
				for _, row := range w.rows {
					if b >= row.start && b <= row.end {
						at = row.start
						if ev.Key == ui.KeyRight {
							at = row.end
						}
						break
					}
				}
			}
			if ev.Mods&ui.Shift == 0 {
				a = at
			}
			w.selectVisible(a, at)
			return true
		}
	}
	return false
}
func (w *docRichInput) boundary(at, dir int) int {
	for i, r := range w.rows {
		if at <= r.end {
			if dir < 0 && at == r.start && i > 0 {
				return w.rows[i-1].end
			}
			if dir > 0 && at == r.end && i+1 < len(w.rows) {
				return w.rows[i+1].start
			}
			if dir < 0 {
				return r.start + r.layout.PreviousBoundary(at-r.start)
			}
			return r.start + r.layout.NextBoundary(at-r.start)
		}
	}
	return at
}

// docRichLayoutObserve is a test-only observer of final paint geometry.
var docRichLayoutObserve func(*docRichInput, ui.Rect)

func (w *docRichInput) element(c *ui.Context, label string) ui.Element {
	paragraph := len(w.prefixes) == 0
	var e ui.Element
	if paragraph {
		w.refresh()
		e = ui.Column(c).FillWidth().Children(func() {
			ui.RichText(c, w.styledSpans()...).FillWidth().MinHeight(w.font.Size * w.font.LineHeight).Font(w.font.Family).FontSize(w.font.Size).FontWeight(w.font.Weight).LineHeight(w.font.LineHeight).TextAlign(w.align).TextColor(w.theme.Text)
		})
	} else {
		e = ui.Column(c).FillWidth()
	}
	e = e.Label(label)
	if !w.idle {
		e = e.Focusable().Role(ui.RoleTextField)
	}
	width := e.Bounds().W
	if width <= 0 {
		width = w.width
	}
	if width <= 0 {
		width = 1
	}
	w.layout(width)
	services := c.Services()
	if !paragraph {
		e.Children(func() {
			spans := w.styledSpans()
			bottom := float32(0)
			for _, row := range w.rows {
				a, b := ui.UTF16ByteOffset(w.display(), row.start), ui.UTF16ByteOffset(w.display(), row.end)
				var runs []ui.Span
				offset := 0
				for _, sp := range spans {
					start, end := max(a, offset), min(b, offset+len(sp.Text))
					if end > start {
						cp := sp
						cp.Text = sp.Text[start-offset : end-offset]
						runs = append(runs, cp)
					}
					offset += len(sp.Text)
				}
				ui.RichText(c, runs...).FillWidth().PaddingStart(row.x).Margin(row.y-bottom, 0, 0, 0).MinHeight(w.font.Size * w.font.LineHeight).Font(w.font.Family).FontSize(w.font.Size).FontWeight(w.font.Weight).LineHeight(w.font.LineHeight).TextColor(w.theme.Text)
				bottom = row.y + row.h
			}
		})
	}
	if !w.idle {
		e.HandleTextInput(w).HandleInput(func(ev ui.InputEvent) bool { return w.input(services, ev) })
	}
	e.Draw(func(p *ui.Painter, b ui.Rect) {
		w.layout(b.W)
		w.bounds = b
		if docRichLayoutObserve != nil {
			docRichLayoutObserve(w, b)
		}
		a, z := w.selection()
		if w.idle {
			a, z = 0, 0
		}
		rg := ui.TextInputRange{Start: min(a, z), End: max(a, z)}
		for _, r := range w.rows {
			for _, rect := range r.layout.SelectionRects(ui.TextInputRange{Start: max(0, rg.Start-r.start), End: max(0, min(r.end-r.start, rg.End-r.start))}) {
				if rg.End > r.start && rg.Start < r.end {
					rect.X += b.X + r.x
					rect.Y += b.Y + r.y
					p.Fill(rect, w.ed.Theme.Selection, 0)
				}
			}
			if r.markerLayout != nil {
				p.TextLayout(r.markerLayout, b.X+max(0, r.markerX), b.Y+r.y, w.theme.Muted)
			}
		}
		if !w.idle && e.Focused() {
			caret := w.caret(z)
			caret.X += b.X
			caret.Y += b.Y
			caret.W = 1.5
			p.Fill(caret, w.ed.Theme.Cursor, 0)
		}
	})
	return e
}

func (w *docRichInput) wordBoundary(at, dir int) int {
	runes := []rune(w.view.Text)
	pos := len([]rune(w.view.Text[:ui.UTF16ByteOffset(w.view.Text, at)]))
	word := func(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }
	if dir < 0 {
		for pos > 0 && !word(runes[pos-1]) {
			pos--
		}
		for pos > 0 && word(runes[pos-1]) {
			pos--
		}
	} else {
		for pos < len(runes) && !word(runes[pos]) {
			pos++
		}
		for pos < len(runes) && word(runes[pos]) {
			pos++
		}
	}
	return ui.UTF16Len(string(runes[:pos]))
}

// styledSpans splices preedit through retained styles without parsing it as
// Markdown or publishing a temporary edit to the document buffer.
func (w *docRichInput) styledSpans() []ui.Span {
	spans := docInlineSpans(w.view, w.theme)
	if !w.marked {
		return spans
	}
	start, end := ui.UTF16ByteOffset(w.view.Text, w.mark.Start), ui.UTF16ByteOffset(w.view.Text, w.mark.End)
	var before, after []ui.Span
	inherited := ui.Span{Text: w.preedit}
	offset := 0
	for _, sp := range spans {
		last := offset + len(sp.Text)
		if offset < start {
			cp := sp
			cp.Text = sp.Text[:min(len(sp.Text), start-offset)]
			before = append(before, cp)
		}
		if last > end {
			cp := sp
			cp.Text = sp.Text[max(0, end-offset):]
			after = append(after, cp)
		}
		offset = last
	}
	caret := w.ed.Cursor()
	for _, run := range w.view.Runs {
		if caret >= run.SourceStart && caret <= run.SourceEnd {
			inherited = docInlineSpans(docedit.InlineView{Text: w.view.Text, Runs: []docedit.InlineRun{run}}, w.theme)[0]
			inherited.Text = w.preedit
			if run.Code || run.Strong || run.Emphasis {
				break
			}
		}
	}
	return append(append(before, inherited), after...)
}
