package markdown

import (
	"bytes"
	"strings"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// The document workbench presents math source rather than rendering it. The
// previous parser recognised a $$ block and $$, \(…\), \[…\] inlines; this
// extension keeps those spans as Math nodes so headings, plain text and the
// compatibility matrix behave the same. Single-dollar math and rendering stay
// out of scope.

var (
	kindMathBlock  = ast.NewNodeKind("MathBlock")
	kindMathInline = ast.NewNodeKind("MathInline")
)

type mathBlockNode struct {
	ast.BaseBlock
	start, end int
}

func (n *mathBlockNode) Kind() ast.NodeKind { return kindMathBlock }
func (n *mathBlockNode) Pos() int           { return n.start }
func (n *mathBlockNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mathInlineNode struct {
	ast.BaseInline
	text       string
	start, end int
}

func (n *mathInlineNode) Kind() ast.NodeKind { return kindMathInline }
func (n *mathInlineNode) Pos() int           { return n.start }
func (n *mathInlineNode) Dump(source []byte, level int) {
	ast.DumpHelper(n, source, level, nil, nil)
}

type mathExtension struct{}

func (mathExtension) Extend(m goldmark.Markdown) {
	m.Parser().AddOptions(
		parser.WithBlockParsers(util.Prioritized(&mathBlockParser{}, 150)),
		parser.WithInlineParsers(util.Prioritized(&mathInlineParser{}, 50)),
	)
}

type mathBlockParser struct{}

func (b *mathBlockParser) Trigger() []byte { return []byte{'$'} }

func (b *mathBlockParser) Open(parent ast.Node, reader text.Reader, pc parser.Context) (ast.Node, parser.State) {
	line, seg := reader.PeekLine()
	i := leadingSpaces(line)
	if i > 3 || !isFence(line[i:]) {
		return nil, parser.NoChildren
	}
	reader.AdvanceToEOL()
	return &mathBlockNode{start: seg.Start + i, end: seg.Stop}, parser.NoChildren
}

func (b *mathBlockParser) Continue(node ast.Node, reader text.Reader, pc parser.Context) parser.State {
	line, seg := reader.PeekLine()
	i := leadingSpaces(line)
	if i <= 3 && isFence(line[i:]) {
		if n, ok := node.(*mathBlockNode); ok {
			n.end = seg.Stop
		}
		reader.AdvanceToEOL()
		return parser.Close
	}
	if n, ok := node.(*mathBlockNode); ok {
		n.end = seg.Stop
	}
	reader.AdvanceToEOL()
	return parser.Continue | parser.NoChildren
}

func (b *mathBlockParser) Close(node ast.Node, reader text.Reader, pc parser.Context) {}

func (b *mathBlockParser) CanInterruptParagraph() bool { return true }

func (b *mathBlockParser) CanAcceptIndentedLine() bool { return false }

func leadingSpaces(line []byte) int {
	i := 0
	for i < len(line) && line[i] == ' ' {
		i++
	}
	return i
}

// isFence reports whether line is a $$ fence with nothing else on it.
func isFence(line []byte) bool {
	return bytes.HasPrefix(line, []byte("$$")) &&
		strings.TrimSpace(string(line[2:])) == ""
}

type mathInlineParser struct{}

func (p *mathInlineParser) Trigger() []byte { return []byte{'$', '\\'} }

func (p *mathInlineParser) Parse(parent ast.Node, reader text.Reader, pc parser.Context) ast.Node {
	line, seg := reader.PeekLine()
	open, close := mathDelimiters(line)
	if open == 0 {
		return nil
	}
	rest := line[open:]
	end := bytes.Index(rest, []byte(close))
	if end < 0 {
		return nil
	}
	node := &mathInlineNode{
		text:  string(rest[:end]),
		start: seg.Start,
		end:   seg.Start + open + end + len(close),
	}
	reader.Advance(open + end + len(close))
	return node
}

// mathDelimiters returns the opening delimiter length and the closing
// delimiter for the current line, or 0 when the line starts with something
// else.
func mathDelimiters(line []byte) (int, string) {
	switch {
	case bytes.HasPrefix(line, []byte(`\(`)):
		return 2, `\)`
	case bytes.HasPrefix(line, []byte(`\[`)):
		return 2, `\]`
	case bytes.HasPrefix(line, []byte("$$")):
		return 2, "$$"
	}
	return 0, ""
}
