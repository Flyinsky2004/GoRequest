// Package docedit is the pure source model behind the document canvas.
//
// The canvas renders one Markdown document as a vertical list of blocks and
// edits each block in place. The only body text is the document's Markdown
// source; this package turns that source into top-level block ranges whose
// replacement is a minimal, byte-preserving edit of the whole document.
//
// It deliberately has no dependency on the desktop toolkit: parsing never
// resolves links, touches the network or executes anything.
package docedit

import (
	"regexp"
	"sort"
	"strings"

	mdparser "github.com/egoist/mygo/ui/markdown/parser"

	"gorequest/internal/doctable"
	"gorequest/internal/markdown"
)

// Block is one top-level Markdown block of a document.
//
// Start and End are byte offsets into the source. The range is expanded to
// whole lines: Start is the beginning of the block's first line and End is
// just past the newline that ends its last line. The expansion keeps list and
// quote markers, fence lines, table pipes and the block's own line ending in
// the range, so replacing the range never leaves a stray marker behind.
type Block struct {
	Kind  markdown.Kind
	Level int
	Start int
	End   int
	// Source is the exact source text of the block, including its final
	// newline when the document has one.
	Source string
	// FrontMatter marks the leading YAML metadata block. It is kept as one
	// verbatim source block so its fences are never read as a rule and a
	// setext heading.
	FrontMatter bool
}

// EditText returns the block's text without the single line terminator that
// belongs to the block boundary. The canvas binds this text (or its content
// projection) to its in-place editor; CommitText adds the terminator back.
func (b Block) EditText() string {
	return trimOneNewline(b.Source)
}

// Terminator returns the block's trailing line ending ("\r\n", "\n", or ""
// when the block reaches EOF without a final newline).
func (b Block) Terminator() string {
	if strings.HasSuffix(b.Source, "\r\n") {
		return "\r\n"
	}
	if strings.HasSuffix(b.Source, "\n") {
		return "\n"
	}
	return ""
}

// HasTerminator reports whether the block's source ends with a newline.
func (b Block) HasTerminator() bool { return b.Terminator() != "" }

// CommitText turns the editor's text back into replacement source: the
// editor never shows the block-boundary newline, so the exact terminator the
// block had ("\n", "\r\n", or "") is added back. A newline the user typed at
// the end is content and is kept before that terminator, which splits the
// block as Markdown expects.
func (b Block) CommitText(edit string) string {
	return edit + b.Terminator()
}

// Parse returns the document's top-level blocks in source order.
//
// The underlying parser flattens container blocks and their children into one
// list. Parse keeps only the outermost block of each container (a list, a
// quote, a table, a footnote definition) so the canvas never draws a child
// twice; nested paragraphs, items, rows and cells stay part of their
// container's source.
func Parse(src string) []Block {
	fmEnd := FrontMatterEnd(src)
	body := src[fmEnd:]
	doc := markdown.Parse(body)
	raw := doc.Blocks()
	// Goldmark container spans can stop at the first paragraph when a list
	// ends in fenced code. The native parser includes container delimiters;
	// use its outer list ranges so descendants never become duplicate canvas
	// blocks and unsupported nested structures remain one lossless source block.
	native := mdparser.Parse(body)
	listEnds := map[int]int{}
	for i := 0; i < native.Len(); i++ {
		b := native.Block(i)
		if b.Kind == mdparser.List && b.Parent == 0 {
			start := LineStart(body, b.Start)
			listEnds[start] = LineEnd(body, max(start, b.End-1))
		}
	}
	out := make([]Block, 0, len(raw)+1)
	if fmEnd > 0 {
		out = append(out, Block{
			Kind:        markdown.HTMLBlock,
			Start:       0,
			End:         fmEnd,
			Source:      src[:fmEnd],
			FrontMatter: true,
		})
	}
	var containers []Block
	for _, b := range raw {
		start, end := expandRange(body, b.Start, b.End)
		if b.Kind == markdown.List {
			if nativeEnd, ok := listEnds[start]; ok {
				end = max(end, nativeEnd)
			}
		}
		if b.Kind == markdown.Table {
			// A table converted from a paragraph may inherit the paragraph's
			// start. Find its header and delimiter within the reported range.
			for pos := start; pos < end; {
				next := LineEnd(body, pos)
				if next >= end {
					break
				}
				if _, err := doctable.Parse(body[pos:LineEnd(body, next)]); err == nil {
					start = pos
					break
				}
				pos = next
			}
		}
		if b.Kind == markdown.CodeBlock && b.Fenced {
			end = fencedCodeEnd(body, start, end)
		}
		if b.Kind == markdown.Heading {
			end = setextHeadingEnd(body, start, end)
		}
		if b.Kind == markdown.Quote {
			end = quoteBlockEnd(body, end)
		}
		absStart, absEnd := fmEnd+start, fmEnd+end
		for len(containers) > 0 && absStart >= containers[len(containers)-1].End {
			containers = containers[:len(containers)-1]
		}
		if len(containers) > 0 {
			top := containers[len(containers)-1]
			if absStart >= top.Start && absEnd <= top.End {
				continue
			}
		}
		blk := Block{Kind: b.Kind, Level: b.Level, Start: absStart, End: absEnd, Source: src[absStart:absEnd]}
		out = append(out, blk)
		if isContainer(b.Kind) {
			containers = append(containers, blk)
		}
	}
	// Footnote definitions are relocated by the parser; source order is what
	// the canvas draws.
	sort.SliceStable(out, func(i, j int) bool { return out[i].Start < out[j].Start })
	return out
}

var yamlKeyLineRe = regexp.MustCompile(`^[^:\s#][^:\r\n]*:(?:[ \t]|$)`)

// FrontMatterEnd returns the end offset (just past the closing fence line)
// of a leading YAML front matter block, or 0 when the document has none.
//
// The block must open at byte 0 (after an optional UTF-8 BOM) with "---",
// must not start with a blank line, must close with "---" or "...", and its
// interior lines must look like YAML metadata (empty front matter or at least
// one top-level "key:" entry, with no unindented prose paragraphs), so a
// document that merely starts with a thematic break "---" is never mistaken
// for metadata.
func FrontMatterEnd(src string) int {
	first := LineEnd(src, 0)
	opener := strings.TrimPrefix(src[:first], "\uFEFF")
	if !strings.HasSuffix(opener, "\n") {
		return 0
	}
	if strings.TrimRight(opener, " \t\r\n") != "---" {
		return 0
	}
	if first >= len(src) {
		return 0
	}
	secondEnd := LineEnd(src, first)
	if strings.TrimSpace(src[first:secondEnd]) == "" {
		return 0
	}
	hasKey := false
	lineCount := 0
	for pos := first; pos < len(src); {
		next := LineEnd(src, pos)
		rawLine := strings.TrimRight(src[pos:next], "\r\n")
		trimmedRight := strings.TrimRight(rawLine, " \t")
		if trimmedRight == "---" || trimmedRight == "..." {
			if lineCount == 0 || hasKey {
				return next
			}
			return 0
		}
		lineCount++
		switch {
		case strings.TrimSpace(rawLine) == "":
		case strings.HasPrefix(strings.TrimLeft(rawLine, " \t"), "#"):
		case rawLine[0] == ' ' || rawLine[0] == '\t':
		case strings.HasPrefix(rawLine, "- ") || strings.HasPrefix(rawLine, "-\t") || trimmedRight == "-":
		case yamlKeyLineRe.MatchString(rawLine):
			hasKey = true
		default:
			return 0
		}
		pos = next
	}
	return 0
}

// isContainer reports whether kind owns the blocks that follow it in the
// flattened parse.
func isContainer(kind markdown.Kind) bool {
	switch kind {
	case markdown.Quote, markdown.List, markdown.Item, markdown.Table,
		markdown.TableRow, markdown.TableCell, markdown.FootnoteDefinition:
		return true
	}
	return false
}

// expandRange grows the parser's (often inline-anchored) range to whole
// lines. The parser reports a paragraph's Start at its first inline and its
// End at its last inline, which would leave a quote marker or a list bullet
// outside the range.
func expandRange(src string, start, end int) (int, int) {
	start = max(0, min(start, len(src)))
	end = max(start, min(end, len(src)))
	start = LineStart(src, start)
	last := end
	if last > start {
		last--
	}
	end = LineEnd(src, last)
	if end < start {
		end = start
	}
	return start, end
}

// fencedCodeEnd extends a fenced code block's range past its closing fence.
// Goldmark reports only the content lines of a FencedCodeBlock, so the fence
// lines would otherwise fall outside the block and a replacement would leave
// the closing fence behind.
func fencedCodeEnd(src string, start, end int) int {
	openEnd := LineEnd(src, start)
	open := strings.TrimLeft(src[start:openEnd], " \t")
	if len(open) < 3 || (open[0] != '`' && open[0] != '~') {
		return end
	}
	n := 0
	for n < len(open) && open[n] == open[0] {
		n++
	}
	if n < 3 {
		return end
	}
	pos := openEnd
	for pos < len(src) {
		next := LineEnd(src, pos)
		if fenceClosingLength(strings.TrimRight(src[pos:next], "\r\n"), open[0]) >= n {
			return next
		}
		pos = next
	}
	return max(end, len(src))
}

// quoteBlockEnd extends a block quote's range over any trailing '>' lines
// (such as a setext underline "> ---", a closing fence "> ```", or a trailing
// blank ">") that Goldmark's inline-anchored span stops before.
func quoteBlockEnd(src string, end int) int {
	for end < len(src) {
		next := LineEnd(src, end)
		raw := src[end:next]
		sp := 0
		for sp < len(raw) && raw[sp] == ' ' && sp < 4 {
			sp++
		}
		if sp > 3 || sp >= len(raw) || raw[sp] != '>' {
			break
		}
		end = next
	}
	return end
}

// setextHeadingEnd extends a setext heading's range over its underline. The
// parser reports the heading's text lines only, so "Title\n=====\n" would
// otherwise leave the underline behind when the heading is replaced.
func setextHeadingEnd(src string, start, end int) int {
	first := strings.TrimLeft(src[start:LineEnd(src, start)], " \t")
	if strings.HasPrefix(first, "#") {
		return end // ATX heading: no underline
	}
	if end >= len(src) {
		return end
	}
	line := strings.TrimSpace(src[end:LineEnd(src, end)])
	if line == "" {
		return end
	}
	all := true
	for _, r := range line {
		if r != '=' && r != '-' {
			all = false
			break
		}
	}
	if all {
		return LineEnd(src, end)
	}
	return end
}

// LineStart returns the byte just after the previous newline at or before
// off.
func LineStart(src string, off int) int {
	off = max(0, min(off, len(src)))
	if i := strings.LastIndexByte(src[:off], '\n'); i >= 0 {
		return i + 1
	}
	return 0
}

// LineEnd returns the byte just past the newline that ends the line holding
// off, or len(src) when the line has no newline.
func LineEnd(src string, off int) int {
	off = max(0, min(off, len(src)))
	if i := strings.IndexByte(src[off:], '\n'); i >= 0 {
		return off + i + 1
	}
	return len(src)
}

// LineAt returns the zero-based line number of off.
func LineAt(src string, off int) int {
	off = max(0, min(off, len(src)))
	return strings.Count(src[:off], "\n")
}

// BlockAt returns the block that holds off, preferring the block that starts
// exactly at off. When off is in a blank line between blocks it reports
// false: the canvas then edits a synthetic empty block there.
func BlockAt(blocks []Block, off int) (Block, bool) {
	for _, b := range blocks {
		if off >= b.Start && off < b.End {
			return b, true
		}
		if off == b.Start && b.Start == b.End {
			return b, true
		}
	}
	return Block{}, false
}

// BlockIndexAt returns the index of the block that holds off.
func BlockIndexAt(blocks []Block, off int) (int, bool) {
	for i, b := range blocks {
		if off >= b.Start && off < b.End {
			return i, true
		}
	}
	return 0, false
}

// PreviousBlock returns the block immediately before off, if any.
func PreviousBlock(blocks []Block, off int) (Block, bool) {
	var prev Block
	found := false
	for _, b := range blocks {
		if b.End <= off {
			prev, found = b, true
			continue
		}
		break
	}
	return prev, found
}

// NextBlock returns the first block that starts at or after off.
func NextBlock(blocks []Block, off int) (Block, bool) {
	for _, b := range blocks {
		if b.Start >= off {
			return b, true
		}
	}
	return Block{}, false
}

// trimOneNewline removes a single trailing "\n" (and a preceding "\r" left by
// an external file) from s.
func trimOneNewline(s string) string {
	if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
		if strings.HasSuffix(s, "\r") {
			s = s[:len(s)-1]
		}
	}
	return s
}
