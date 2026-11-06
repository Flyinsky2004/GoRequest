// Package markdown is the neutral Markdown parser used by the document core.
//
// It parses CommonMark plus the GitHub-flavoured extensions the document
// workbench relies on (tables, task lists, strikethrough, bare autolinks and
// footnotes) and the math syntax of the previous parser ($$ blocks, $$, \(…\)
// and \[…\] inlines). The package exposes a small flattened document model:
// blocks in source order and, for the blocks that carry them, inline runs in
// source order. Positions are byte offsets into the source.
//
// It deliberately has no dependency on the desktop toolkit. Parsing never
// resolves links, touches the network or executes anything.
package markdown

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// Kind is a block kind.
type Kind int

// Block kinds. The set mirrors the block semantics the document workbench
// consumes; container-only nodes without document meaning are not exposed.
const (
	Paragraph Kind = iota
	Heading
	CodeBlock
	Quote
	List
	Item
	ThematicBreak
	HTMLBlock
	Table
	TableRow
	TableCell
	FootnoteDefinition
	MathBlock
)

// TaskState is the checkbox state of a list item.
type TaskState int

// Task list states.
const (
	TaskNone TaskState = iota
	TaskUnchecked
	TaskChecked
)

// Alignment is a table column alignment.
type Alignment int

// Table alignments.
const (
	AlignNone Alignment = iota
	AlignLeft
	AlignRight
	AlignCenter
)

// InlineKind is an inline kind.
type InlineKind int

// Inline kinds.
const (
	Text InlineKind = iota
	SoftBreak
	LineBreak
	Emphasis
	Strong
	Code
	Link
	Image
	HTML
	Strikethrough
	FootnoteRef
	Math
)

// Block is one block of a document.
type Block struct {
	Kind Kind
	// Level is the heading level, 1 through 6.
	Level int
	// Ordered reports whether a List is ordered.
	Ordered bool
	// Fenced reports whether a CodeBlock used a fence.
	Fenced bool
	// Info is the fenced code block's info string (usually a language).
	Info string
	// Task is the list item checkbox state.
	Task TaskState
	// Align is the column alignment of a Table.
	Align []Alignment
	// Start and End are byte offsets into the source.
	Start, End int
}

// Language returns the first word of a fenced code block's info string.
func (b Block) Language() string {
	if b.Info == "" {
		return ""
	}
	if i := strings.IndexAny(b.Info, " \t"); i >= 0 {
		return b.Info[:i]
	}
	return b.Info
}

// Inline is one inline element.
type Inline struct {
	Kind InlineKind
	// Text is the plain text of Text, Code, HTML, FootnoteRef and Math.
	Text string
	// Dest and Title are the destination and title of Link and Image.
	Dest, Title string
	// Auto reports whether a Link came from an autolink.
	Auto bool
	// Start and End are byte offsets into the source.
	Start, End int
}

// Document is a parsed Markdown document.
type Document struct {
	src     string
	blocks  []Block
	inlines [][]Inline
}

// Blocks returns the document's blocks in source order.
func (d *Document) Blocks() []Block { return d.blocks }

// Inlines returns the inline run of block i, or nil.
func (d *Document) Inlines(i int) []Inline {
	if i < 0 || i >= len(d.inlines) {
		return nil
	}
	return d.inlines[i]
}

// PlainText returns the text content of an inline run, without markup. It
// matches the document workbench's previous parser: text, code, raw HTML and
// math count, line breaks become a space, and a footnote reference is written
// as [^label].
func PlainText(in []Inline) string {
	var b strings.Builder
	for _, n := range in {
		switch n.Kind {
		case Text, Code, HTML, Math:
			b.WriteString(n.Text)
		case SoftBreak, LineBreak:
			b.WriteByte(' ')
		case FootnoteRef:
			b.WriteString("[^" + n.Text + "]")
		}
	}
	return b.String()
}

// defaultMarkdown is immutable after package initialisation and safe for
// concurrent parsing.
var defaultMarkdown = goldmark.New(
	goldmark.WithExtensions(extension.GFM, extension.Footnote, mathExtension{}),
)

// Parse parses src. It never fails: malformed Markdown is text.
func Parse(src string) *Document {
	root := defaultMarkdown.Parser().Parse(
		text.NewReader([]byte(src)),
		parser.WithContext(parser.NewContext()),
	)
	d := &Document{src: src}
	walkBlocks(d, root)
	return d
}

func walkBlocks(d *Document, parent ast.Node) {
	for n := parent.FirstChild(); n != nil; n = n.NextSibling() {
		kind, ok := blockKind(n)
		if !ok {
			// Containers without document meaning (document, footnote list)
			// are transparent: their children keep source order.
			walkBlocks(d, n)
			continue
		}
		b := Block{Kind: kind, Start: nodeStart(n), End: nodeEnd(n)}
		switch v := n.(type) {
		case *ast.Heading:
			b.Level = v.Level
			b.Start = n.Pos()
		case *ast.List:
			b.Ordered = v.IsOrdered()
		case *ast.ListItem:
			b.Task = listItemTask(v)
		case *ast.FencedCodeBlock:
			b.Fenced = true
			if v.Info != nil {
				b.Info = string(v.Info.Segment.Value([]byte(d.src)))
			}
		case *east.Table:
			b.Align = make([]Alignment, len(v.Alignments))
			for i, a := range v.Alignments {
				b.Align[i] = alignment(a)
			}
		case *east.Footnote:
			// The old parser placed the definition where it appeared; the
			// reference label is the meaningful part for callers.
			b.Start = footnoteStart(n, b.Start)
		case *mathBlockNode:
			b.Start, b.End = v.start, v.end
		}
		if b.End < b.Start {
			b.End = b.Start
		}
		idx := len(d.blocks)
		d.blocks = append(d.blocks, b)
		d.inlines = append(d.inlines, nil)
		if carriesInlines(n) {
			d.inlines[idx] = collectInlines([]byte(d.src), n)
		} else if _, isMath := n.(*mathBlockNode); !isMath {
			walkBlocks(d, n)
		}
	}
}

func blockKind(n ast.Node) (Kind, bool) {
	switch n.(type) {
	case *ast.Paragraph, *ast.TextBlock:
		return Paragraph, true
	case *ast.Heading:
		return Heading, true
	case *ast.FencedCodeBlock, *ast.CodeBlock:
		return CodeBlock, true
	case *ast.Blockquote:
		return Quote, true
	case *ast.List:
		return List, true
	case *ast.ListItem:
		return Item, true
	case *ast.ThematicBreak:
		return ThematicBreak, true
	case *ast.HTMLBlock:
		return HTMLBlock, true
	case *east.Table:
		return Table, true
	case *east.TableHeader, *east.TableRow:
		return TableRow, true
	case *east.TableCell:
		return TableCell, true
	case *east.Footnote:
		return FootnoteDefinition, true
	case *mathBlockNode:
		return MathBlock, true
	}
	return 0, false
}

func alignment(a east.Alignment) Alignment {
	switch a {
	case east.AlignLeft:
		return AlignLeft
	case east.AlignRight:
		return AlignRight
	case east.AlignCenter:
		return AlignCenter
	}
	return AlignNone
}

func carriesInlines(n ast.Node) bool {
	switch n.(type) {
	case *ast.Heading, *ast.Paragraph, *ast.TextBlock, *east.TableCell:
		return true
	}
	return false
}

func listItemTask(n *ast.ListItem) TaskState {
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		switch v := c.(type) {
		case *east.TaskCheckBox:
			if v.IsChecked {
				return TaskChecked
			}
			return TaskUnchecked
		case *ast.TextBlock, *ast.Paragraph:
			for g := c.FirstChild(); g != nil; g = g.NextSibling() {
				if cb, ok := g.(*east.TaskCheckBox); ok {
					if cb.IsChecked {
						return TaskChecked
					}
					return TaskUnchecked
				}
			}
		}
	}
	return TaskNone
}

func footnoteStart(n ast.Node, fallback int) int {
	// Footnote definitions are relocated by the extension; keep their first
	// child's position so diagnostics still point inside the document.
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if c.Type() == ast.TypeBlock {
			return nodeStart(c)
		}
	}
	return fallback
}

func nodeStart(n ast.Node) int {
	if p := n.Pos(); p >= 0 {
		return p
	}
	if lines := n.Lines(); lines != nil && lines.Len() > 0 {
		return lines.At(0).Start
	}
	if inl := firstInlinePosition(n); inl >= 0 {
		return inl
	}
	return 0
}

func nodeEnd(n ast.Node) int {
	if lines := n.Lines(); lines != nil && lines.Len() > 0 {
		return lines.At(lines.Len() - 1).Stop
	}
	last := -1
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if entering {
			if p := inlineEnd(c); p > last {
				last = p
			}
		}
		return ast.WalkContinue, nil
	})
	if last >= 0 {
		return last
	}
	return max(0, n.Pos())
}

func firstInlinePosition(n ast.Node) int {
	pos := -1
	_ = ast.Walk(n, func(c ast.Node, entering bool) (ast.WalkStatus, error) {
		if pos >= 0 {
			return ast.WalkStop, nil
		}
		if entering && c.Type() == ast.TypeInline {
			if p := inlineStart(c); p >= 0 {
				pos = p
				return ast.WalkStop, nil
			}
		}
		return ast.WalkContinue, nil
	})
	return pos
}

func inlineStart(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Start
	case *ast.RawHTML:
		if v.Segments != nil && v.Segments.Len() > 0 {
			return v.Segments.At(0).Start
		}
	case *mathInlineNode:
		return v.start
	}
	return n.Pos()
}

func inlineEnd(n ast.Node) int {
	switch v := n.(type) {
	case *ast.Text:
		return v.Segment.Stop
	case *ast.RawHTML:
		if v.Segments != nil && v.Segments.Len() > 0 {
			return v.Segments.At(v.Segments.Len() - 1).Stop
		}
	case *mathInlineNode:
		return v.end
	}
	return 0
}

// collectInlines flattens the inline descendants of block into source order.
func collectInlines(src []byte, block ast.Node) []Inline {
	out := make([]Inline, 0, 8)
	var add func(n ast.Node)
	add = func(n ast.Node) {
		for c := n.FirstChild(); c != nil; c = c.NextSibling() {
			switch v := c.(type) {
			case *ast.Text:
				if v.Segment.Len() > 0 {
					out = append(out, Inline{
						Kind:  Text,
						Text:  resolveText(v.Segment.Value(src)),
						Start: v.Segment.Start,
						End:   v.Segment.Stop,
					})
				}
				if v.SoftLineBreak() {
					out = append(out, breakInline(SoftBreak, v.Segment.Stop))
				} else if v.HardLineBreak() {
					out = append(out, breakInline(LineBreak, v.Segment.Stop))
				}
			case *ast.String:
				pos := v.Pos()
				if pos < 0 {
					pos = 0
				}
				out = append(out, Inline{Kind: Text, Text: string(v.Value), Start: pos, End: pos + len(v.Value)})
			case *ast.Emphasis:
				k := Emphasis
				if v.Level >= 2 {
					k = Strong
				}
				out = append(out, Inline{Kind: k, Start: v.Pos(), End: inlineSpanEnd(c, v.Pos())})
				add(c)
			case *ast.CodeSpan:
				out = append(out, Inline{Kind: Code, Text: codeSpanText(src, v), Start: v.Pos(), End: inlineSpanEnd(c, v.Pos())})
			case *ast.Link:
				out = append(out, Inline{
					Kind:  Link,
					Dest:  resolveText(v.Destination),
					Title: resolveText(v.Title),
					Start: v.Pos(),
					End:   inlineSpanEnd(c, v.Pos()),
				})
				add(c)
			case *ast.Image:
				out = append(out, Inline{
					Kind:  Image,
					Dest:  resolveText(v.Destination),
					Title: resolveText(v.Title),
					Start: v.Pos(),
					End:   inlineSpanEnd(c, v.Pos()),
				})
				add(c)
			case *ast.AutoLink:
				dest := string(v.URL(src))
				end := autoLinkEnd(src, v.Pos(), dest)
				out = append(out, Inline{Kind: Link, Auto: true, Dest: dest, Start: v.Pos(), End: end})
				out = append(out, Inline{Kind: Text, Text: string(v.Label(src)), Start: v.Pos(), End: end})
			case *ast.RawHTML:
				raw, start, end := rawHTMLText(v, src)
				out = append(out, Inline{Kind: HTML, Text: raw, Start: start, End: end})
			case *east.Strikethrough:
				out = append(out, Inline{Kind: Strikethrough, Start: v.Pos(), End: inlineSpanEnd(c, v.Pos())})
				add(c)
			case *east.FootnoteLink:
				label := footnoteLabel(src, v.Pos())
				out = append(out, Inline{
					Kind:  FootnoteRef,
					Text:  label,
					Start: v.Pos(),
					End:   v.Pos() + len(label) + 3,
				})
			case *east.FootnoteBacklink:
				// Generated by the footnote extension; not source content.
			case *mathInlineNode:
				out = append(out, Inline{Kind: Math, Text: v.text, Start: v.start, End: v.end})
			default:
				if c.Type() == ast.TypeInline {
					add(c)
				}
			}
		}
	}
	add(block)
	return out
}

func breakInline(k InlineKind, at int) Inline {
	return Inline{Kind: k, Start: at, End: at}
}

func inlineSpanEnd(n ast.Node, fallback int) int {
	last := fallback
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if e := inlineEnd(c); e > last {
			last = e
		}
		if e := inlineSpanEnd(c, last); e > last {
			last = e
		}
	}
	return last
}

func autoLinkEnd(src []byte, start int, label string) int {
	if start < 0 || start >= len(src) {
		return max(0, start)
	}
	if src[start] == '<' {
		if i := bytes.IndexByte(src[start:], '>'); i >= 0 {
			return start + i + 1
		}
	}
	return start + len(label)
}

func rawHTMLText(n *ast.RawHTML, src []byte) (string, int, int) {
	if n.Segments == nil || n.Segments.Len() == 0 {
		return "", n.Pos(), n.Pos()
	}
	return string(n.Segments.Value(src)), n.Segments.At(0).Start, n.Segments.At(n.Segments.Len() - 1).Stop
}

func codeSpanText(src []byte, n *ast.CodeSpan) string {
	var b strings.Builder
	for c := n.FirstChild(); c != nil; c = c.NextSibling() {
		if t, ok := c.(*ast.Text); ok {
			b.Write(t.Segment.Value(src))
		}
	}
	return b.String()
}

func footnoteLabel(src []byte, start int) string {
	if start < 0 || start+2 > len(src) || src[start] != '[' || src[start+1] != '^' {
		return strconv.Itoa(start)
	}
	if i := bytes.IndexByte(src[start+2:], ']'); i >= 0 {
		return string(src[start+2 : start+2+i])
	}
	return strconv.Itoa(start)
}

// resolveText resolves the backslash escapes and entity references of plain
// text the same way the previous parser did: escaped punctuation is literal
// (so "\&amp;" stays "&amp;"), while unescaped numeric and named entities are
// decoded.
func resolveText(raw []byte) string {
	if !bytes.ContainsAny(raw, `\&`) {
		return string(raw)
	}
	var b strings.Builder
	b.Grow(len(raw))
	for i := 0; i < len(raw); {
		c := raw[i]
		if c == '\\' && i+1 < len(raw) && isPunct(raw[i+1]) {
			b.WriteByte(raw[i+1])
			i += 2
			continue
		}
		if c == '&' {
			if n, ok := entityAt(raw, i); ok {
				b.WriteString(n.value)
				i = n.next
				continue
			}
		}
		b.WriteByte(c)
		i++
	}
	return b.String()
}

func isPunct(c byte) bool {
	switch {
	case c >= '!' && c <= '/':
		return true
	case c >= ':' && c <= '@':
		return true
	case c >= '[' && c <= '`':
		return true
	case c >= '{' && c <= '~':
		return true
	}
	return false
}

type entity struct {
	value string
	next  int
}

func entityAt(src []byte, at int) (entity, bool) {
	// Decimal and hexadecimal numeric references.
	if at+2 < len(src) && src[at+1] == '#' {
		i := at + 2
		base := 10
		if i < len(src) && (src[i] == 'x' || src[i] == 'X') {
			base = 16
			i++
		}
		start := i
		for i < len(src) && isDigit(src[i], base) {
			i++
		}
		if i > start && i < len(src) && src[i] == ';' {
			if v, err := strconv.ParseUint(string(src[start:i]), base, 32); err == nil {
				return entity{value: string(validRune(rune(v))), next: i + 1}, true
			}
		}
		return entity{}, false
	}
	// Named references use the HTML5 table of the parser dependency.
	i := at + 1
	for i < len(src) && (src[i] >= 'a' && src[i] <= 'z' || src[i] >= 'A' && src[i] <= 'Z' || src[i] >= '0' && src[i] <= '9') {
		i++
	}
	if i > at+1 && i < len(src) && src[i] == ';' {
		if r, ok := decodeNamedEntity(string(src[at+1 : i])); ok {
			return entity{value: r, next: i + 1}, true
		}
	}
	return entity{}, false
}

func isDigit(c byte, base int) bool {
	if c >= '0' && c <= '9' {
		return true
	}
	if base == 16 && (c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
		return true
	}
	return false
}

func validRune(r rune) rune {
	if r == 0 || r > 0x10ffff || (r >= 0xd800 && r <= 0xdfff) {
		return '�'
	}
	return r
}

func decodeNamedEntity(name string) (string, bool) {
	e, ok := util.LookUpHTML5EntityByName(name)
	if !ok {
		return "", false
	}
	return string(e.Characters), true
}
