package desktop

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docedit"
	docmd "gorequest/internal/markdown"
)

// Integration hooks for the parallel U07 (tables) and U08 (mentions) modules.
// All modules are now wired. Variables let the renderer contract be exercised
// independently in tests; the defaults are the production implementations.
var (
	// docTableRenderer, when set by desktop/doc_table*.go (U07), renders an
	// editable GFM table for one complete table block. source is exactly the
	// block's Markdown; replace receives the block's new Markdown and the
	// canvas applies it as one minimal source-range transaction (undoable).
	docTableRenderer = renderDocTable

	// installDocMentionHooks, when set by desktop/doc_mentions*.go (U08),
	// composes completion/hover callbacks onto the active editor. It must
	// wrap the editor's existing OnInput instead of replacing it.
	installDocMentionHooks = (*App).installDocMentionHooks

	// renderDocMentionTools, when set by U08, draws optional mention tools
	// beside the active editor.
	renderDocMentionTools = (*App).renderDocMentionTools
)

// --- block lifecycle ------------------------------------------------------

// syncDocBlock reconciles the active block's transient editor with the
// canonical buffer before a frame draws it. It commits a pending local edit,
// adopts an external change (undo, another command), or drops a block that no
// longer exists.
func (a *App) syncDocBlock(tab *Tab, st *docViewState) {
	lv := st.live
	if lv == nil || lv.block == nil {
		return
	}
	be := lv.block
	if be.ed == nil {
		lv.block = nil
		return
	}
	src := tab.DocEditor.Buffer.String()
	blocks := lv.blocksFor(src)
	local := be.ed.Buffer.String()

	if be.synthetic {
		if be.start < 0 || be.start > len(src) || be.start != be.end {
			lv.block = nil
			return
		}
		lineEnd := docedit.LineEnd(src, be.start)
		if strings.TrimSpace(src[be.start:lineEnd]) != "" {
			if b, ok := docedit.BlockAt(blocks, be.start); ok {
				be.synthetic = false
				be.start, be.end = b.Start, b.End
				be.base = b.EditText()
				be.terminator = b.HasTerminator()
				be.kind, be.level = b.Kind, b.Level
				if local != be.base {
					be.ed.Buffer.Reset(be.base)
					be.ed.Select(0, 0)
				}
				return
			}
			lv.block = nil
			return
		}
		if local != "" {
			a.commitDocBlock(tab, st)
		}
		return
	}

	b, ok := docedit.BlockAt(blocks, be.start)
	if !ok {
		lv.block = nil
		return
	}
	be.start, be.end = b.Start, b.End
	canon := b.EditText()
	if canon == be.base {
		if local != be.base {
			a.commitDocBlock(tab, st)
		}
		return
	}
	// The canonical block changed under the editor: undo, a slash command,
	// or another view. Canonical wins; the local edit was already committed
	// when it mattered.
	caret := be.ed.Cursor()
	be.base = canon
	be.terminator = b.HasTerminator()
	be.kind, be.level = b.Kind, b.Level
	be.synthetic = false
	if local != canon {
		be.ed.Buffer.Reset(canon)
		off := max(0, min(caret, len(canon)))
		be.ed.Select(off, off)
	}
}

// commitDocBlock writes the active editor's text back into the canonical
// buffer as one minimal source-range edit. Untouched blocks keep every byte.
func (a *App) commitDocBlock(tab *Tab, st *docViewState) {
	lv := st.live
	if lv == nil || lv.block == nil {
		return
	}
	be := lv.block
	if be.ed == nil {
		return
	}
	local := be.ed.Buffer.String()
	if local == be.base {
		return
	}
	caret := be.ed.Cursor()
	if be.synthetic {
		tab.DocEditor.Buffer.Edit(editor.Edit{Start: be.start, End: be.end, Text: local})
	} else {
		newText := local
		if be.terminator {
			newText += "\n"
		}
		tab.DocEditor.Buffer.Edit(editor.Edit{Start: be.start, End: be.end, Text: newText})
	}
	a.relocateDocBlock(tab, st, be.start+caret)
}

// relocateDocBlock points the active editor at the block that now holds
// canonicalCaret, resetting its transient text when the block split, merged
// or was replaced. A caret on a blank line becomes a synthetic empty block.
func (a *App) relocateDocBlock(tab *Tab, st *docViewState, canonicalCaret int) {
	be := st.live.block
	if be == nil {
		return
	}
	src := tab.DocEditor.Buffer.String()
	blocks := docLiveFor(st).blocksFor(src)
	if b, ok := docBlockAtCaret(blocks, canonicalCaret); ok {
		moved := be.synthetic || be.start != b.Start || be.end != b.End || be.ed.Buffer.String() != b.EditText()
		be.synthetic = false
		be.start, be.end = b.Start, b.End
		be.base = b.EditText()
		be.terminator = b.HasTerminator()
		be.kind, be.level = b.Kind, b.Level
		if be.ed.Buffer.String() != be.base {
			be.ed.Buffer.Reset(be.base)
		}
		off := max(0, min(canonicalCaret-b.Start, len(be.base)))
		be.ed.Select(off, off)
		if moved {
			be.focusPending = true
		}
		return
	}
	off := docedit.LineStart(src, canonicalCaret)
	lineEnd := docedit.LineEnd(src, off)
	if strings.TrimSpace(src[off:lineEnd]) == "" {
		be.synthetic = true
		be.start, be.end = off, off
		be.base = ""
		be.terminator = false
		be.kind = docmd.Paragraph
		be.level = 0
		if be.ed.Buffer.String() != "" {
			be.ed.Buffer.Reset("")
		}
		be.ed.Select(0, 0)
		be.focusPending = true
		return
	}
	st.live.block = nil
}

// activateDocBlock opens the in-place editor for b. clickY, relative to the
// rendered block, puts the caret near the line clicked.
func (a *App) activateDocBlock(t *ui.Theme, tab *Tab, st *docViewState, b docedit.Block, clickY float32) {
	lv := docLiveFor(st)
	if lv.block != nil {
		a.commitDocBlock(tab, st)
	}
	be := &docBlockEdit{
		start: b.Start, end: b.End,
		base: b.EditText(), terminator: b.HasTerminator(),
		kind: b.Kind, level: b.Level,
		focusPending: true,
	}
	be.ed = a.newDocBlockEditor(t, tab, st, be, be.base)
	line := 0
	if clickY > 0 {
		line = int(clickY / a.docBlockLineHeight(t, be.kind, be.level))
	}
	line = max(0, min(line, strings.Count(be.base, "\n")))
	off := docLineEndOffset(be.base, line)
	be.ed.Select(off, off)
	lv.block = be
	lv.slashOpen = false
	lv.slashMode = 0
	lv.slashIndex = 0
	lv.slashDismissed = ""
}

// activateDocSynthetic opens an empty in-place block at off, the start of a
// blank line.
func (a *App) activateDocSynthetic(t *ui.Theme, tab *Tab, st *docViewState, off int) {
	lv := docLiveFor(st)
	if lv.block != nil {
		a.commitDocBlock(tab, st)
	}
	be := &docBlockEdit{
		start: off, end: off, base: "",
		kind: docmd.Paragraph, synthetic: true,
		focusPending: true,
	}
	be.ed = a.newDocBlockEditor(t, tab, st, be, "")
	lv.block = be
	lv.slashOpen = false
	lv.slashMode = 0
	lv.slashIndex = 0
	lv.slashDismissed = ""
}

// docCanvasAppendOffset returns the offset at which a new final block starts,
// adding the blank line Markdown needs to open a new block.
func (a *App) docCanvasAppendOffset(tab *Tab) int {
	src := tab.DocEditor.Buffer.String()
	switch {
	case src == "":
		return 0
	case strings.HasSuffix(src, "\n\n"):
		return len(src)
	case strings.HasSuffix(src, "\n"):
		tab.DocEditor.Buffer.Edit(editor.Edit{Start: len(src), End: len(src), Text: "\n"})
		return len(src) + 1
	default:
		tab.DocEditor.Buffer.Edit(editor.Edit{Start: len(src), End: len(src), Text: "\n\n"})
		return len(src) + 2
	}
}

// newDocBlockEditor builds the transient editor of one block. It shares the
// document's input method handling, but not its buffer: the canonical buffer
// stays the only body text.
func (a *App) newDocBlockEditor(t *ui.Theme, tab *Tab, st *docViewState, be *docBlockEdit, text string) *editor.State {
	ed := &editor.State{
		Buffer:          editor.NewBuffer(text),
		Font:            a.docBlockFont(t, be.kind, be.level),
		LineHeight:      a.docBlockLineHeight(t, be.kind, be.level),
		TabSize:         4,
		SoftWrap:        true,
		LineNumbers:     editor.LineNumbersOff,
		NoFolding:       true,
		NoIndentGuides:  true,
		NoActiveLine:    true,
		NoScrollPastEnd: true,
		NoAutoClose:     true,
		FitContent:      true,
		Label:           "doc-canvas-block",
		Theme:           a.docCanvasEditorTheme(t, be.kind),
	}
	ed.OnInput = func(ev ui.InputEvent) bool { return a.docBlockInput(tab, st, be, ev) }
	if installDocMentionHooks != nil {
		installDocMentionHooks(a, tab, ed)
	}
	return ed
}

// syncDocBlockTheme keeps the transient editor's font and colors in step
// with the block kind and the window theme.
func (a *App) syncDocBlockTheme(be *docBlockEdit, t *ui.Theme) {
	if be == nil || be.ed == nil {
		return
	}
	be.ed.Font = a.docBlockFont(t, be.kind, be.level)
	be.ed.LineHeight = a.docBlockLineHeight(t, be.kind, be.level)
	be.ed.Theme = a.docCanvasEditorTheme(t, be.kind)
}

// docBlockFont returns the editor font that matches the rendered block.
func (a *App) docBlockFont(t *ui.Theme, kind docmd.Kind, level int) ui.Font {
	th := a.docMarkdownTheme(t)
	f := ui.Font{Family: th.Font, Size: th.FontSize, Weight: 400}
	if f.Family == "" {
		f.Family = "system-ui"
	}
	switch kind {
	case docmd.CodeBlock:
		f.Family = th.CodeFont
		if f.Family == "" {
			f.Family = "monospace"
		}
		f.Size = th.FontSize * th.CodeSize
	case docmd.Heading:
		i := max(0, min(level-1, 5))
		f.Size = th.FontSize * th.Headings[i].Size
		f.Weight = th.Headings[i].Weight
	}
	return f
}

// docBlockLineHeight is the editor's line height, a multiple of the font
// size, matching the rendered block's leading.
func (a *App) docBlockLineHeight(t *ui.Theme, kind docmd.Kind, _ int) float32 {
	th := a.docMarkdownTheme(t)
	if kind == docmd.CodeBlock {
		return th.CodeLineHeight
	}
	return th.LineHeight
}

// docMarkdownTheme resolves the user's Markdown preferences against the
// default theme, so the canvas and its in-place editors share one scale.
func (a *App) docMarkdownTheme(t *ui.Theme) *markdown.Theme {
	th := markdown.DefaultTheme(t)
	if a.mgr != nil {
		pol := a.mgr.workspacePrefs().markdownPolicy().theme(t)
		if pol.FontSize > 0 {
			th.FontSize = pol.FontSize
		}
		if pol.LineHeight > 0 {
			th.LineHeight = pol.LineHeight
		}
	}
	return th
}

// docCanvasEditorTheme colors a block editor so it sits invisibly on the
// canvas, with the app's selection and caret colors.
func (a *App) docCanvasEditorTheme(t *ui.Theme, kind docmd.Kind) *editor.Theme {
	p := uikit.PaletteFor(t)
	ct := editor.DefaultTheme(t)
	ct.Background = p.BG
	ct.Text = p.Text
	ct.ActiveLine = p.BG
	ct.LineNumber = p.BG
	ct.ActiveLineNumber = p.BG
	ct.IndentGuide = p.BG
	ct.ActiveIndentGuide = p.BG
	ct.WrapGuide = p.BG
	ct.Whitespace = p.BG
	ct.Selection = p.Accent.Alpha(0.25)
	ct.InactiveSelection = p.Accent.Alpha(0.14)
	ct.Cursor = p.Accent
	if kind == docmd.Quote {
		ct.Text = p.Muted
	}
	return ct
}

// docLineEndOffset returns the byte offset just past line in text, where a
// click on the block puts the caret so typing continues that line.
func docLineEndOffset(text string, line int) int {
	start := docLineOffset(text, line)
	if i := strings.IndexByte(text[start:], '\n'); i >= 0 {
		return start + i
	}
	return len(text)
}

// docLineOffset returns the byte offset of line in text.
func docLineOffset(text string, line int) int {
	if line <= 0 {
		return 0
	}
	off := 0
	for i := 0; i < line; i++ {
		j := strings.IndexByte(text[off:], '\n')
		if j < 0 {
			return len(text)
		}
		off += j + 1
	}
	return off
}

// --- input ----------------------------------------------------------------

// docBlockInput handles the keys the in-place editor must keep for the
// canvas: global undo/redo, block merge, slash menu navigation and escape.
// Everything else goes to the editor, whose input method composition is
// untouched (composition keys never reach OnInput).
func (a *App) docBlockInput(tab *Tab, st *docViewState, be *docBlockEdit, ev ui.InputEvent) bool {
	lv := st.live
	if lv == nil || lv.block != be {
		return false
	}
	// Push the text typed so far into the canonical buffer before this event
	// edits it, so a save or an external refresh that lands between frames
	// sees every keystroke but the one being handled.
	if docInputEdits(ev) && tab.DocEditor.Buffer.Len() <= 256<<10 {
		a.commitDocBlock(tab, st)
	}
	// The transient edit is not in the canonical buffer until the next frame.
	// Mark the tab dirty as the key comes, so an external refresh in that
	// window goes through the conflict flow instead of replacing the edit.
	if ev.Kind == ui.InputText {
		tab.Dirty = true
	}
	if ev.Kind == ui.InputKeyDown {
		switch ev.Key {
		case ui.KeyBackspace, ui.KeyDelete, ui.KeyEnter:
			tab.Dirty = true
		}
	}
	if ev.Kind == ui.InputCommand {
		switch ev.Text {
		case "undo":
			a.docBlockUndo(tab, st, false)
			return true
		case "redo":
			a.docBlockUndo(tab, st, true)
			return true
		case "bold":
			a.docBlockWrap(be, "**", "**")
			return true
		case "italic":
			a.docBlockWrap(be, "*", "*")
			return true
		}
	}
	// Menu keys are taken on the press when the editor sees it, so Enter does
	// not also insert a newline. Some keys are claimed by the window before
	// the editor (⌘Z is the app's toast undo); for those only the release
	// arrives, so the same handling runs there as a fallback.
	if ev.Kind == ui.InputKeyUp {
		if lv.keyUpIgnore == ev.Key {
			lv.keyUpIgnore = 0
			return false
		}
		if lv.slashOpen {
			switch ev.Key {
			case ui.KeyUp:
				a.docSlashMove(st, -1)
				return true
			case ui.KeyDown:
				a.docSlashMove(st, 1)
				return true
			case ui.KeyEnter:
				a.docSlashChoose(tab, st)
				return true
			case ui.KeyEscape:
				lv.slashDismissed = lv.slashQuery
				lv.slashOpen = false
				lv.slashMode = 0
				return true
			}
		}
		if ev.Mods == 0 && ev.Key == ui.KeyEscape {
			a.commitDocBlock(tab, st)
			lv.block = nil
			return true
		}
		return false
	}
	if ev.Kind != ui.InputKeyDown {
		return false
	}
	if lv.slashOpen {
		switch ev.Key {
		case ui.KeyUp:
			lv.keyUpIgnore = ev.Key
			a.docSlashMove(st, -1)
			return true
		case ui.KeyDown:
			lv.keyUpIgnore = ev.Key
			a.docSlashMove(st, 1)
			return true
		case ui.KeyEnter:
			lv.keyUpIgnore = ev.Key
			a.docSlashChoose(tab, st)
			return true
		case ui.KeyEscape:
			lv.keyUpIgnore = ev.Key
			lv.slashDismissed = lv.slashQuery
			lv.slashOpen = false
			lv.slashMode = 0
			return true
		}
	}
	if ev.Mods == 0 && ev.Key == ui.KeyEscape {
		lv.keyUpIgnore = ev.Key
		a.commitDocBlock(tab, st)
		lv.block = nil
		return true
	}
	switch {
	case ev.Mods == ui.Cmd && ev.Key == ui.KeyZ:
		a.docBlockUndo(tab, st, false)
		return true
	case ev.Mods == (ui.Cmd|ui.Shift) && ev.Key == ui.KeyZ:
		a.docBlockUndo(tab, st, true)
		return true
	case ev.Mods == ui.Cmd && ev.Key == ui.KeyB:
		a.docBlockWrap(be, "**", "**")
		return true
	case ev.Mods == ui.Cmd && ev.Key == ui.KeyI:
		a.docBlockWrap(be, "*", "*")
		return true
	}
	if ev.Mods == 0 && ev.Key == ui.KeyBackspace {
		sel := be.ed.Selection()
		if sel.Start() == 0 && sel.End() == 0 {
			return a.docMergePrevious(tab, st)
		}
	}
	if ev.Mods == 0 && ev.Key == ui.KeyDelete {
		sel := be.ed.Selection()
		if sel.Start() == sel.End() && sel.Head == be.ed.Buffer.Len() {
			return a.docMergeNext(tab, st)
		}
	}
	return false
}

// docInputEdits reports whether an input event changes the text, so the
// canvas commits the pending block text before it.
func docInputEdits(ev ui.InputEvent) bool {
	switch ev.Kind {
	case ui.InputText:
		return true
	case ui.InputCommand:
		switch ev.Text {
		case "paste", "pasteAndMatchStyle", "cut", "delete":
			return true
		}
	case ui.InputKeyDown:
		switch ev.Key {
		case ui.KeyBackspace, ui.KeyDelete, ui.KeyEnter:
			return true
		}
	}
	return false
}

// docBlockUndo routes undo and redo to the document's single history.
func (a *App) docBlockUndo(tab *Tab, st *docViewState, redo bool) {
	be := st.live.block
	if be == nil {
		return
	}
	a.commitDocBlock(tab, st)
	buf := tab.DocEditor.Buffer
	var ok bool
	if redo {
		ok = buf.Redo()
	} else {
		ok = buf.Undo()
	}
	if !ok {
		return
	}
	src := buf.String()
	blocks := docLiveFor(st).blocksFor(src)
	if b, found := docedit.BlockAt(blocks, be.start); found {
		be.synthetic = false
		be.start, be.end = b.Start, b.End
		be.base = b.EditText()
		be.terminator = b.HasTerminator()
		be.kind, be.level = b.Kind, b.Level
		be.ed.Buffer.Reset(be.base)
		be.ed.Select(0, 0)
		be.focusPending = true
		return
	}
	st.live.block = nil
}

// docMergePrevious joins the active block into the block before it, as
// Backspace at the block's start does in a block editor.
func (a *App) docMergePrevious(tab *Tab, st *docViewState) bool {
	be := st.live.block
	if be == nil || be.synthetic {
		return false
	}
	a.commitDocBlock(tab, st)
	src := tab.DocEditor.Buffer.String()
	blocks := docLiveFor(st).blocksFor(src)
	cur, ok := docedit.BlockAt(blocks, be.start)
	if !ok {
		return false
	}
	prev, ok := docedit.PreviousBlock(blocks, cur.Start)
	if !ok {
		return false
	}
	merged := prev.EditText() + cur.EditText()
	if cur.HasTerminator() {
		merged += "\n"
	}
	tab.DocEditor.Buffer.Edit(editor.Edit{Start: prev.Start, End: cur.End, Text: merged})
	a.relocateDocBlock(tab, st, prev.Start+len(prev.EditText()))
	if st.live.block != nil {
		st.live.block.focusPending = true
	}
	return true
}

// docMergeNext joins the block after the active one into it, as Delete at
// the block's end does.
func (a *App) docMergeNext(tab *Tab, st *docViewState) bool {
	be := st.live.block
	if be == nil || be.synthetic {
		return false
	}
	a.commitDocBlock(tab, st)
	src := tab.DocEditor.Buffer.String()
	blocks := docLiveFor(st).blocksFor(src)
	cur, ok := docedit.BlockAt(blocks, be.start)
	if !ok {
		return false
	}
	next, ok := docedit.NextBlock(blocks, cur.End)
	if !ok {
		return false
	}
	merged := cur.EditText() + next.EditText()
	if next.HasTerminator() {
		merged += "\n"
	}
	tab.DocEditor.Buffer.Edit(editor.Edit{Start: cur.Start, End: next.End, Text: merged})
	a.relocateDocBlock(tab, st, cur.Start+len(cur.EditText()))
	if st.live.block != nil {
		st.live.block.focusPending = true
	}
	return true
}
