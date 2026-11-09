package docedit

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui/markdown/parser"
)

// InlineRun is an editable content range. Delimiters and link destinations
// are absent from the visible string, but remain in Source.
type InlineRun struct {
	Start, End                     int // visible UTF-8 bytes
	SourceStart, SourceEnd         int
	Code, Strong, Emphasis, Strike bool
	Link                           string
	SoftBreak                      bool
}

type InlineView struct {
	Source, Text string
	Runs         []InlineRun
}

// NewInlineView uses the non-streaming Markdown AST: incomplete and escaped
// markers stay literal. The parser reports absolute source bytes, including
// delimiters. Content ranges below are retained separately, never serialized.
func NewInlineView(src string) InlineView {
	return newInlineView(src, false)
}

func newInlineView(src string, retainLines bool) InlineView {
	v := InlineView{Source: src}
	var visible strings.Builder
	d := parser.Parse(src)
	at := 0
	var emit func(int, int, InlineRun)
	emit = func(a, b int, style InlineRun) {
		a = max(at, min(a, len(src)))
		b = max(a, min(b, len(src)))
		if b == a {
			return
		}
		style.Start = visible.Len()
		style.SourceStart = a
		style.SourceEnd = b
		visible.WriteString(src[a:b])
		style.End = visible.Len()
		v.Runs = append(v.Runs, style)
		at = b
	}
	var walk func([]parser.Inline, int, int, InlineRun)
	walk = func(in []parser.Inline, start, end int, style InlineRun) {
		for i := start; i < end; {
			n := in[i]
			next := max(i+1, n.Next)
			if n.Start < at || n.End > len(src) {
				i = next
				continue
			}
			if n.Kind != parser.SoftBreak && n.Kind != parser.LineBreak {
				emit(at, n.Start, style)
			}
			s := style
			switch n.Kind {
			case parser.Strong, parser.Emphasis, parser.Strikethrough, parser.Link:
				s.Strong = s.Strong || n.Kind == parser.Strong
				s.Emphasis = s.Emphasis || n.Kind == parser.Emphasis
				s.Strike = s.Strike || n.Kind == parser.Strikethrough
				if n.Kind == parser.Link {
					s.Link = n.Dest
				}
				if next > i+1 {
					at = in[i+1].Start
					walk(in, i+1, next, s)
					at = n.End
				} else {
					emit(n.Start, n.End, style)
				}
			case parser.SoftBreak, parser.LineBreak:
				// Break syntax belongs to this retained normalization run. It
				// is never serialized unless the user actually replaces it.
				a := n.Start
				// The parser excludes hard-break trailing spaces from its
				// Text node. Include those source bytes in the break run.
				for a > at && (src[a-1] == ' ' || src[a-1] == '\r') {
					a--
				}
				emit(at, a, style)

				b := n.End
				for b < len(src) && (src[b] == ' ' || src[b] == '\t') {
					b++
				}
				run := s
				run.SoftBreak = n.Kind == parser.SoftBreak
				run.Start, run.End = visible.Len(), visible.Len()+1
				run.SourceStart, run.SourceEnd = a, b
				if n.Kind == parser.LineBreak || retainLines {
					visible.WriteByte('\n')
				} else {
					visible.WriteByte(' ')
				}
				v.Runs = append(v.Runs, run)
				at = b
			case parser.Code:
				raw := src[n.Start:n.End]
				ticks := 0
				for ticks < len(raw) && raw[ticks] == '`' {
					ticks++
				}
				a, b := n.Start+ticks, n.End-ticks
				// CommonMark normalizes code newlines to spaces and removes
				// one padding space around nonblank code. Keep a separate
				// source span for each normalization, including CRLF.
				normalized := strings.ReplaceAll(strings.ReplaceAll(src[a:b], "\r\n", " "), "\n", " ")
				if len(normalized) >= 2 && normalized[0] == ' ' && normalized[len(normalized)-1] == ' ' && strings.Trim(normalized, " ") != "" {
					if strings.HasPrefix(src[a:b], "\r\n") {
						a += 2
					} else {
						a++
					}
					if strings.HasSuffix(src[a:b], "\r\n") {
						b -= 2
					} else {
						b--
					}
				}
				at = a
				s.Code = true
				for at < b {
					nl := strings.IndexByte(src[at:b], '\n')
					if nl < 0 {
						emit(at, b, s)
						break
					}
					end := at + nl
					start := end
					if end > at && src[end-1] == '\r' {
						start--
					}
					emit(at, start, s)
					run := s
					run.Start = visible.Len()
					run.End = run.Start + 1
					run.SourceStart = start
					run.SourceEnd = end + 1
					visible.WriteByte(' ')
					v.Runs = append(v.Runs, run)
					at = end + 1
				}
				at = n.End
			default:
				// Escapes/entities keep their exact source spelling in this first
				// bounded view; no ambiguous decoded-to-source replacement occurs.
				emit(n.Start, n.End, s)
			}
			i = next
		}
	}
	for i := 0; i < d.Len(); i++ {
		in := d.Inlines(i)
		if len(in) > 0 {
			walk(in, 0, len(in), InlineRun{})
		}
	}
	emit(at, len(src), InlineRun{})
	v.Text = visible.String()
	return v
}

// SourceOffset chooses the content side of hidden delimiter boundaries.
// At the end of a run it stays before the closing delimiter, so typing at
// the end of code or a link edits its content rather than its destination.
func (v InlineView) SourceOffset(off int) int {
	off = max(0, min(off, len(v.Text)))
	for _, r := range v.Runs {
		if (r.Code || r.Strong || r.Emphasis || r.Link != "") && off >= r.Start && off <= r.End {
			if off == r.End {
				return r.SourceEnd
			}
			if r.SourceEnd-r.SourceStart != r.End-r.Start {
				return r.SourceStart
			}
			return r.SourceStart + off - r.Start
		}
	}
	for _, r := range v.Runs {
		if off >= r.Start && off <= r.End {
			if off == r.End {
				return r.SourceEnd
			}
			if r.SourceEnd-r.SourceStart != r.End-r.Start {
				return r.SourceStart
			}
			return r.SourceStart + off - r.Start
		}
	}
	return min(off, len(v.Source))
}
func (v InlineView) VisibleOffset(off int) int {
	off = max(0, min(off, len(v.Source)))
	for _, r := range v.Runs {
		if off < r.SourceStart {
			return r.Start
		}
		if off <= r.SourceEnd {
			if off == r.SourceEnd {
				return r.End
			}
			if r.SourceEnd-r.SourceStart != r.End-r.Start {
				return r.Start
			}
			return r.Start + off - r.SourceStart
		}
	}
	return len(v.Text)
}

// SourceRange uses inward affinity for a nonempty visible selection:
// adjacent hidden opening/closing delimiters belong to the unselected run.
func (v InlineView) SourceRange(start, end int) (int, int) {
	a, b := v.SourceOffset(start), v.SourceOffset(end)
	if end > start {
		for _, r := range v.Runs {
			if start == r.Start {
				a = r.SourceStart
				break
			}
		}
	}
	if end > start {
		for _, r := range v.Runs {
			if end == r.End {
				b = r.SourceEnd
				break
			}
		}
	}
	return a, b
}

// Replace edits only the selected visible content; untouched syntax and
// destination bytes survive. A selection crossing runs removes the source
// interval between them as well, which deliberately joins their structures.
func (v InlineView) Replace(start, end int, text string) (string, int) {
	a, b := v.SourceRange(start, end)
	if b < a {
		a, b = b, a
	}
	// Clamp externally supplied offsets to UTF-8 boundaries.
	for a > 0 && a < len(v.Source) && !utf8.RuneStart(v.Source[a]) {
		a--
	}
	for b < len(v.Source) && !utf8.RuneStart(v.Source[b]) {
		b++
	}
	return v.Source[:a] + text + v.Source[b:], a + len(text)
}

// NewListInlineView joins only soft continuations within an item. Item and
// paragraph boundaries remain newlines; literal code spans keep their spaces.
func NewListInlineView(src string, lines []ListLine) InlineView {
	v := newInlineView(src, true)
	text := []byte(v.Text)
	for _, r := range v.Runs {
		if !r.SoftBreak {
			continue
		}
		next := strings.Count(src[:r.SourceEnd], "\n")
		if next > 0 && next < len(lines) && lines[next].Marker == "" {
			text[r.Start] = ' '
		}
	}
	v.Text = string(text)
	return v
}
