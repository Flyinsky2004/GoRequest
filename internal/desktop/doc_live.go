package desktop

import (
	"fmt"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
	"gorequest/internal/docedit"
	"gorequest/internal/docgen"
	"gorequest/internal/docmention"
	docmd "gorequest/internal/markdown"
)

// This file owns the rendered document canvas (Section 3): one vertical
// column of blocks drawn in their Markdown form, each edited in place when
// clicked. The only body text stays tab.DocEditor.Buffer; the active block
// keeps a transient editor whose text is committed back through a minimal
// source-range edit, so untouched blocks stay byte-identical and the
// document's undo stack stays the single history.
//
// The canvas is deliberately a native MyGo view: no WebView, no new
// dependency, no second rich-text model.

// docSectionCanvas is the document canvas mode. Sections 0 (split), 1
// (source) and 2 (preview) keep their existing meanings.
const docSectionCanvas = 3

// docLiveState is the canvas state of one document tab.
type docLiveState struct {
	// block is the block being edited in place, nil when the canvas is in
	// its rendered state.
	block *docBlockEdit

	// modeInit records that the first build chose the canvas as the default
	// for a document that had no explicit mode yet.
	modeInit bool

	// slash menu
	slashOpen    bool
	slashMode    int // 0 commands, 1 request picker
	slashIndex   int
	slashQuery   string
	slashItems   []docedit.SlashCommand
	slashRequest []*core.RequestEntry
	// slashDismissed keeps a closed menu closed for the query it was closed
	// on, so Escape or a click outside does not reopen it on the next frame.
	// slashDismissedSet tells an empty query ("/") from no dismissal, which
	// the zero value of slashDismissed alone cannot.
	slashDismissed    string
	slashDismissedSet bool

	canvasOpts *markdown.Options
	codeBlocks map[int]*docBlockEdit
	// keyUpIgnore is the key whose release a handled press must not handle
	// again.
	keyUpIgnore ui.Key
	// cardCache keeps a directive card's expansion until its source changes.
	cardCache map[int]docCardCacheEntry
	// mentionHooks records that U08's completion hooks were installed once
	// on the source editor.
	mentionHooks bool
	// canvasScroll keeps the canvas position across frames; scrollLine asks
	// the next frame to scroll there (outline, search, diagnostics, anchors).
	canvasScroll  ui.ScrollState
	scrollLine    int
	scrollPending bool
	// parsed caches the top-level blocks of the source they were parsed from.
	parsedSrc    string
	parsedBlocks []docedit.Block
}

// docBlockEdit is one block's transient in-place editor.
type docBlockEdit struct {
	// start and end are the block's current range in the canonical buffer.
	start, end int
	// base is the block's projected text (the content the editor shows,
	// without block markers such as "## ", "> " or "- ") as last committed
	// or adopted; the editor's buffer differing from it means there is a
	// pending local edit.
	base string
	// srcBase is the block's exact canonical source as last committed or
	// adopted, so a change of only a hidden marker (undo of a heading
	// level) is still seen as a canonical change.
	srcBase string
	// proj keeps the markers base hides and writes them back on commit.
	proj docedit.Projection
	// terminator records the block's trailing line ending ("\n", "\r\n", or
	// ""), which the commit adds back.
	terminator string
	kind       docmd.Kind
	level      int
	// synthetic marks an empty line between blocks, where there is no parsed
	// block yet.
	synthetic bool

	rich             *docRichInput
	ed               *editor.State
	focusPending     bool
	initialClick     *ui.Point
	initialWidth     float32
	codeEnterPending bool
}

type docCardCacheEntry struct {
	src, out string
}

// blocksFor returns the top-level blocks of src, parsing only when src
// changed since the last call.
func (lv *docLiveState) blocksFor(src string) []docedit.Block {
	if lv.parsedSrc != src || lv.parsedBlocks == nil {
		lv.parsedSrc = src
		lv.parsedBlocks = docedit.Parse(src)
		for start := range lv.codeBlocks {
			b, ok := docedit.BlockAt(lv.parsedBlocks, start)
			if !ok || b.Start != start || b.Kind != docmd.CodeBlock {
				delete(lv.codeBlocks, start)
			}
		}
	}
	return lv.parsedBlocks
}

func docLiveFor(st *docViewState) *docLiveState {
	if st.live == nil {
		st.live = &docLiveState{}
	}
	return st.live
}

// docLiveObserve is an optional test observer notified after each canvas
// frame reconciles state; nil in production.
var docLiveObserve func(tab *Tab, st *docViewState)

// docCanvasView draws the rendered canvas and its in-place editor.
func (a *App) docCanvasView(c *ui.Context, t *ui.Theme, root ui.Element, tab *Tab, st *docViewState) {
	dm := a.docLiveMsg()
	p := uikit.PaletteFor(t)
	lv := docLiveFor(st)

	a.syncDocBlock(tab, st)
	src := tab.DocEditor.Buffer.String()
	blocks := lv.blocksFor(src)
	a.refreshDocSlash(tab, st, blocks)

	opts := a.docCanvasMarkdownOptions(c, root, tab, st)
	// Diagnose the whole document even when no block editor is active.
	a.renderDocMentionTools(c, t, root, tab, tab.DocEditor)

	if lv.scrollPending {
		th := a.docMarkdownTheme(t)
		lineH := th.FontSize * th.LineHeight
		lv.canvasScroll.Y = float32(lv.scrollLine) * lineH * 1.15
		lv.scrollPending = false
	}
	ui.Scroll(c).Grow(1).FillWidth().TrackScroll(&lv.canvasScroll).Children(func() {
		canvas := ui.Column(c).FillWidth().Padding(18, 40).Gap(2)
		// Canvas blocks and table cells share the canonical document history.
		canvas.OnShortcut(ui.Cmd, ui.KeyZ, func() {
			if lv.block != nil {
				a.docBlockUndo(tab, st, false)
			} else {
				tab.DocEditor.Buffer.Undo()
				tab.Dirty = tab.DocEditor.Buffer.Modified()
			}
		})
		canvas.OnShortcut(ui.Cmd|ui.Shift, ui.KeyZ, func() {
			if lv.block != nil {
				a.docBlockUndo(tab, st, true)
			} else {
				tab.DocEditor.Buffer.Redo()
				tab.Dirty = tab.DocEditor.Buffer.Modified()
			}
		})
		if canvas.Clicked() {
			a.commitDocBlock(tab, st)
			lv.block = nil
			lv.slashOpen = false
		}
		canvas.Children(func() {
			if len(blocks) == 0 && lv.block == nil {
				a.docCanvasEmptyHint(c, t, p, tab, st, dm)
				return
			}
			syntheticDrawn := false
			for i := 0; i < len(blocks); i++ {
				b := blocks[i]
				if lv.block != nil && lv.block.synthetic && !syntheticDrawn && lv.block.start <= b.Start {
					a.docCanvasActiveBlock(c, t, tab, st, dm)
					syntheticDrawn = true
				}
				if lv.block != nil && !lv.block.synthetic && lv.block.start == b.Start {
					a.docCanvasActiveBlock(c, t, tab, st, dm)
					continue
				}
				if spanEnd, ok := docDirectiveSpan(src, b); ok {
					a.docCanvasDirectiveCard(c, t, p, tab, st, b, spanEnd, opts, dm)
					for i+1 < len(blocks) && blocks[i+1].Start < spanEnd {
						i++
					}
					continue
				}
				a.docCanvasRenderedBlock(c, t, p, tab, st, b, opts, dm)
			}
			if lv.block != nil && lv.block.synthetic && !syntheticDrawn {
				a.docCanvasActiveBlock(c, t, tab, st, dm)
			}
			if lv.block == nil {
				a.docCanvasAddArea(c, t, p, tab, st, dm)
			}
		})
	})
	if docLiveObserve != nil {
		docLiveObserve(tab, st)
	}
}

// docCanvasEmptyHint is the empty document's invitation.
func (a *App) docCanvasEmptyHint(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, dm *docLiveMessages) {
	row := ui.Column(c).FillWidth().Padding(10, 8).Radius(6)
	if row.Hovered() {
		row.Background(p.Hover)
	}
	row.Children(func() {
		ui.Text(c, dm.EmptyHint).FontSize(14).TextColor(p.Faint)
	})
	row.Label(dm.EmptyHint)
	if row.Clicked() {
		a.activateDocSynthetic(t, tab, st, 0)
	}
}

// docCanvasAddArea is the clickable room below the last block.
func (a *App) docCanvasAddArea(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, dm *docLiveMessages) {
	row := ui.Row(c).FillWidth().Height(42).Padding(0, 8).AlignItems(ui.Center).Radius(6)
	if row.Hovered() {
		row.Background(p.Hover)
	}
	row.Children(func() {
		ui.Text(c, dm.AddBlock).FontSize(12).TextColor(p.Faint)
	})
	row.Label(dm.AddBlock)
	if row.Clicked() {
		a.activateDocSynthetic(t, tab, st, a.docCanvasAppendOffset(tab))
	}
}

// docCanvasRenderedBlock draws one block in its Markdown form and activates
// its in-place editor on a click.
func (a *App) docCanvasRenderedBlock(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, b docedit.Block, opts *markdown.Options, dm *docLiveMessages) {
	if b.Kind == docmd.CodeBlock {
		a.docCanvasCodeBlock(c, t, tab, st, b, nil)
		return
	}
	if docBlockIsSourceFallback(b) {
		a.docCanvasSourceBlock(c, t, p, tab, st, b, dm)
		return
	}
	row := ui.Column(c).FillWidth().Padding(2, 8).Radius(4).Key(fmt.Sprintf("doc-block:%d", b.Start))
	if row.Hovered() {
		row.Background(p.Hover)
	}
	handled := false
	row.Children(func() {
		if b.Kind == docmd.ThematicBreak {
			ui.Box(c).FillWidth().Height(1).Background(p.Hairline).MarginY(8)
			return
		}
		if b.Kind == docmd.Table && docTableRenderer != nil {
			if docTableRenderer(c, t, row, b.Source, func(newSource string) {
				a.replaceDocBlockSource(tab, st, b, newSource)
			}, a.docMarkdownTheme(t)) {
				// U07 owns the table's cells; a click there edits the cell,
				// not the whole table's raw source.
				handled = true
				return
			}
		}
		if b.Kind == docmd.Paragraph {
			refs := docmention.ScanRequestLinks(tabProject(tab), b.Source)
			if len(refs) == 1 && strings.TrimSpace(b.Source[:refs[0].Start]) == "" && strings.TrimSpace(b.Source[refs[0].End:]) == "" {
				a.docMentionChip(c, t, tab, refs[0].Label, refs[0].Dest)
				handled = true
				return
			}
		}
		markdown.View(c, b.Source, opts).FillWidth()
	})
	row.Label(docBlockLabel(b))
	if !handled && row.Clicked() {
		x, y, _ := row.PointerPosition()
		a.activateDocBlock(t, tab, st, b, y, x, row.Bounds().W)
	}
}

// docCanvasSourceBlock is the explicit source fallback for blocks the canvas
// does not render richly (HTML, footnotes, math and unknown extensions). The
// source is shown verbatim and editing it edits exactly that source.
func (a *App) docCanvasSourceBlock(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, b docedit.Block, dm *docLiveMessages) {
	card := ui.Column(c).FillWidth().Padding(8, 10).Gap(4).Radius(6).
		Background(p.Sidebar).Border(1, p.Hairline).Key(fmt.Sprintf("doc-source:%d", b.Start))
	if card.Hovered() {
		card.Background(p.Hover)
	}
	card.Children(func() {
		caption := dm.SourceBlock
		if b.FrontMatter {
			caption = dm.FrontMatter
		}
		ui.Text(c, caption).FontSize(11).TextColor(p.Muted)
		ui.Text(c, strings.TrimRight(b.Source, "\r\n")).Font("monospace").FontSize(12).TextColor(p.Text)
	})
	card.Label(docBlockLabel(b) + ":source")
	if card.Clicked() {
		a.activateDocBlock(t, tab, st, b, 0)
	}
}

// docCanvasDirectiveCard draws a derived request card as a read-only view of
// its expansion. Editing the card edits the directive line, never the
// derived text.
func (a *App) docCanvasDirectiveCard(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, b docedit.Block, spanEnd int, opts *markdown.Options, dm *docLiveMessages) {
	src := tab.DocEditor.Buffer.String()
	span := src[b.Start:min(spanEnd, len(src))]
	lv := docLiveFor(st)
	if lv.cardCache == nil {
		lv.cardCache = map[int]docCardCacheEntry{}
	}
	expanded := span
	if cached, ok := lv.cardCache[b.Start]; ok && cached.src == span {
		expanded = cached.out
	} else if tab.Owner != nil && tab.Owner.project != nil {
		if exp, err := docgen.ExpandDoc(tab.Owner.project, span, docgen.CardOptions{Labels: a.cardLabelsFor()}); err == nil && exp.Changed {
			expanded = exp.Markdown
		}
		lv.cardCache[b.Start] = docCardCacheEntry{src: span, out: expanded}
	}
	card := ui.Column(c).FillWidth().Padding(8, 10).Gap(4).Radius(6).
		Background(p.Sidebar).Border(1, p.Hairline).Key(fmt.Sprintf("doc-card:%d", b.Start))
	card.Children(func() {
		ui.Text(c, dm.DerivedCard).FontSize(11).TextColor(p.Muted)
		markdown.View(c, expanded, opts).FillWidth()
	})
	card.Label(docBlockLabel(b) + ":card")
	if card.Clicked() {
		a.activateDocBlock(t, tab, st, b, 0)
	}
}

// docCanvasActiveBlock draws the transient in-place editor of the active
// block, followed by the slash menu when it is open.
func (a *App) docCanvasActiveBlock(c *ui.Context, t *ui.Theme, tab *Tab, st *docViewState, dm *docLiveMessages) {
	be := st.live.block
	if be == nil || be.ed == nil {
		return
	}
	a.syncDocBlockTheme(be, t)
	if be.kind == docmd.CodeBlock {
		a.docCanvasCodeBlock(c, t, tab, st, docedit.Block{}, be)
		return
	}
	// The editor must be keyed by an element around it, not by Key on the
	// editor itself: editor.View reads its focus at the start of its build,
	// before a Key applied to the returned element would give it the id
	// Focus sets. A Key after the build would leave the editor reading a
	// different node's focus every frame, so its caret never drew. The
	// keyed host keeps the editor's own id stable across frames, so Focus
	// and Focused agree and the real caret shows.
	host := ui.Column(c).FillWidth().Padding(2, 8).Key("doc-canvas-editor-host")
	if be.kind == docmd.Quote && be.proj.Active() {
		p := uikit.PaletteFor(t)
		host.BorderWidth(0, 0, 0, 3).BorderColor(p.Hairline).Padding(0, 10)
	}
	host.Children(func() {
		var view ui.Element
		richNeeded := be.rich != nil || be.kind == docmd.List
		if !richNeeded {
			inline := docedit.NewInlineView(be.ed.Buffer.String())
			richNeeded = inline.Text != inline.Source
		}
		if be.kind == docmd.List && !be.proj.Active() {
			ui.Text(c, dm.SourceBlock).FontSize(11).TextColor(t.TextMuted)
			richNeeded = false
			be.rich = nil
		}
		if richNeeded {
			if be.rich == nil {
				be.rich = &docRichInput{ed: be.ed}
			}
			w := be.rich
			w.font = be.ed.Font
			w.font.LineHeight = be.ed.LineHeight
			w.theme = a.docMarkdownTheme(t)
			w.literal = be.kind == docmd.CodeBlock
			if be.kind == docmd.List && be.proj.Active() {
				key := be.srcBase + "\x00" + be.ed.Buffer.String()
				if w.prefixKey != key {
					w.prefixes = be.proj.ListLines(be.ed.Buffer.String())
					w.prefixKey = key
				}
			} else {
				w.prefixes = nil
				w.prefixKey = ""
			}
			w.changed = func() { tab.Dirty = true; a.commitDocBlock(tab, st) }
			w.toggleTask = func(line int) { a.docRichToggleTask(tab, st, be, line) }
			w.mentionInput = func(ev ui.InputEvent) bool { return a.docRichMentionInput(tab, st, be, ev) }
			w.structure = func(ev ui.InputEvent) bool { return a.docRichListInput(tab, st, be, ev) }
			if be.initialClick != nil {
				w.width = max(1, be.initialWidth-16)
			}
			view = w.element(c, "doc-canvas-editor")
			if be.initialClick != nil {
				pt := *be.initialClick
				pt.X -= 8
				pt.Y -= 2
				index, _ := w.IndexForPoint(pt)
				w.selectVisible(index, index)
				be.initialClick = nil
			}
		} else {
			view = editor.View(c, be.ed).FillWidth()
		}
		if be.focusPending {
			view.Focus()
			be.focusPending = false
			// Focus takes effect on the next frame, when the editor first
			// paints its caret; ask for it so a click that only focuses does
			// not wait for the next keystroke.
			c.Invalidate()
		}
		view.Label("doc-canvas-editor")
		if be.rich != nil {
			a.docRichMentionMenu(c, t, tab, st, be)
		}
	})
	if st.live.slashOpen {
		a.docSlashMenu(c, t, tab, st, dm)
	}
}

// docCanvasMarkdownOptions returns the canvas's Markdown options. The canvas
// shares the document's image loader and link handler, but leaves text
// selection to the block wrapper's click target so a click activates the
// block instead of starting a selection.
func (a *App) docCanvasMarkdownOptions(c *ui.Context, root ui.Element, tab *Tab, st *docViewState) *markdown.Options {
	lv := docLiveFor(st)
	if lv.canvasOpts == nil {
		lv.canvasOpts = &markdown.Options{}
	}
	o := lv.canvasOpts
	o.Theme = a.mgr.workspacePrefs().markdownPolicy().theme(c.Theme())
	o.ShareSelection = true
	o.LoadImage = func(ref string) (ui.ImageSource, error) {
		data, info, err := tab.Owner.project.ReadDocAsset(tab.DocRel, ref)
		if err != nil {
			return nil, err
		}
		if info.IsSVG {
			return ui.ParseSVG(data)
		}
		return ui.DecodeBitmap(data)
	}
	o.OnLink = func(dest string) { a.handleDocLink(c, root, tab, st, dest) }
	o.Anchor = ""
	o.AnchorVersion = 0
	return o
}

// docBlockIsSourceFallback reports whether the canvas must show the block as
// explicit source rather than a rendered form.
func docBlockIsSourceFallback(b docedit.Block) bool {
	if b.FrontMatter {
		return true
	}
	switch b.Kind {
	case docmd.List:
		return !docedit.NewProjection(b).Active()
	case docmd.HTMLBlock, docmd.FootnoteDefinition, docmd.MathBlock:
		return true
	}
	return false
}

// docBlockLabel is the stable accessibility identity of a rendered block.
func docBlockLabel(b docedit.Block) string {
	return fmt.Sprintf("doc-block:%s:%d", docBlockKindName(b), b.Start)
}

func docBlockKindName(b docedit.Block) string {
	switch b.Kind {
	case docmd.Paragraph:
		return "paragraph"
	case docmd.Heading:
		return "heading"
	case docmd.CodeBlock:
		return "code"
	case docmd.Quote:
		return "quote"
	case docmd.List:
		return "list"
	case docmd.ThematicBreak:
		return "rule"
	case docmd.HTMLBlock:
		if b.FrontMatter {
			return "frontmatter"
		}
		return "html"
	case docmd.Table:
		return "table"
	case docmd.FootnoteDefinition:
		return "footnote"
	case docmd.MathBlock:
		return "math"
	}
	return "block"
}

// docDirectiveSpan returns the end of a derived card's source span: the
// directive line plus the generated block between the begin and end markers
// that follows it. It reports false when the block is not a card directive.
func docDirectiveSpan(src string, b docedit.Block) (int, bool) {
	first := b.Source
	if i := strings.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if _, ok, err := docgen.ParseDirectiveLine(first); !ok || err != nil {
		return 0, false
	}
	end := b.End
	pos := end
	for pos < len(src) {
		next := docedit.LineEnd(src, pos)
		trimmed := strings.TrimSpace(src[pos:next])
		if trimmed == "" {
			pos = next
			continue
		}
		if trimmed != docgen.BeginMarker {
			// The directive has no generated block directly after it.
			return end, true
		}
		break
	}
	for pos < len(src) {
		next := docedit.LineEnd(src, pos)
		if strings.TrimSpace(src[pos:next]) == docgen.EndMarker {
			return next, true
		}
		pos = next
	}
	return end, true
}
