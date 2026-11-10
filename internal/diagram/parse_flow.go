package diagram

import (
	"strings"
)

// parsedLine is one physical source line and its 1-based number.
type parsedLine struct {
	num  int
	text string
}

func sourceLines(src string) []parsedLine {
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = strings.ReplaceAll(src, "\r", "\n")
	raw := strings.Split(src, "\n")
	out := make([]parsedLine, 0, len(raw))
	for i, line := range raw {
		out = append(out, parsedLine{num: i + 1, text: line})
	}
	return out
}

type stmtFragment struct {
	col  int // 0-based rune offset in the line
	text string
}

// splitStatements splits one flowchart line on semicolons outside quotes and
// bracket pairs.
func splitStatements(text string) []stmtFragment {
	runes := []rune(text)
	var out []stmtFragment
	start := 0
	depth := 0
	quote := false
	for i, r := range runes {
		switch {
		case quote:
			if r == '"' {
				quote = false
			}
		case r == '"':
			quote = true
		case r == '[' || r == '(' || r == '{':
			depth++
		case r == ']' || r == ')' || r == '}':
			if depth > 0 {
				depth--
			}
		case r == ';' && depth == 0:
			out = append(out, stmtFragment{start, string(runes[start:i])})
			start = i + 1
		}
	}
	out = append(out, stmtFragment{start, string(runes[start:])})
	return out
}

// parseFlowchart parses the supported flowchart subset.
func parseFlowchart(lines []parsedLine, limits Limits) (*flowchart, *Diagnostic) {
	fc := &flowchart{dir: TopDown, index: map[string]*fnode{}}
	header := false
	for _, line := range lines {
		trimmed := strings.TrimSpace(line.text)
		if strings.HasPrefix(trimmed, "%%{") {
			return nil, diagAt(line.num, 1, "Mermaid init directives are not supported by the offline renderer")
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
			continue
		}
		if !header {
			fields := strings.Fields(trimmed)
			kw := strings.ToLower(fields[0])
			if kw != "flowchart" && kw != "graph" {
				return nil, diagAt(line.num, 1, "unsupported diagram type %q; the offline renderer supports flowchart, graph and sequenceDiagram", fields[0])
			}
			if len(fields) > 2 {
				return nil, diagAt(line.num, 1, "unexpected text after the flowchart direction")
			}
			if len(fields) == 2 {
				switch strings.ToUpper(fields[1]) {
				case "TD", "TB":
					fc.dir = TopDown
				case "BT":
					fc.dir = BottomUp
				case "LR":
					fc.dir = LeftRight
				case "RL":
					fc.dir = RightLeft
				default:
					return nil, diagAt(line.num, 1, "unsupported flowchart direction %q (supported: TD, TB, BT, LR, RL)", fields[1])
				}
			}
			header = true
			continue
		}
		for _, stmt := range splitStatements(line.text) {
			trimmed = strings.TrimSpace(stmt.text)
			if trimmed == "" || strings.HasPrefix(trimmed, "%%") {
				continue
			}
			col := stmt.col + leadingSpaceRunes(stmt.text)
			if d := checkFlowDirective(trimmed, line.num, col+1); d != nil {
				return nil, d
			}
			if d := parseFlowStatement(fc, trimmed, line.num, col, limits); d != nil {
				return nil, d
			}
		}
	}
	if !header {
		return nil, diagAt(1, 1, "missing flowchart declaration (expected \"flowchart TD\" or \"graph LR\")")
	}
	if len(fc.nodes) == 0 {
		return nil, diagAt(1, 1, "the flowchart declares no nodes")
	}
	return fc, nil
}

func leadingSpaceRunes(s string) int {
	n := 0
	for _, r := range s {
		if r != ' ' && r != '\t' {
			break
		}
		n++
	}
	return n
}

var rejectedFlowDirectives = map[string]string{
	"subgraph":  "subgraphs are not supported by the offline flowchart renderer",
	"end":       "subgraphs and blocks are not supported by the offline flowchart renderer",
	"class":     "class declarations are not supported; styles are ignored rather than half-applied",
	"classdef":  "classDef declarations are not supported; styles are ignored rather than half-applied",
	"style":     "style declarations are not supported; styles are ignored rather than half-applied",
	"linkstyle": "linkStyle declarations are not supported",
	"click":     "click actions and links are disabled: the offline renderer never emits interactive URLs",
	"direction": "nested direction statements are not supported",
	"acctitle":  "accessibility directives are not supported",
	"accdescr":  "accessibility directives are not supported",
}

func checkFlowDirective(stmt string, line, col int) *Diagnostic {
	word := stmt
	if i := strings.IndexAny(word, " \t"); i >= 0 {
		word = word[:i]
	}
	if msg, bad := rejectedFlowDirectives[strings.ToLower(word)]; bad {
		return diagAt(line, col, "%s", msg)
	}
	return nil
}

type nodeRef struct {
	id       string
	label    string
	hasLabel bool
	shape    Shape
	col      int
}

type linkStyle struct {
	dotted bool
	thick  bool
	arrow  bool
}

// parseFlowStatement parses one node group, optionally followed by a chain
// of links and further groups.
func parseFlowStatement(fc *flowchart, stmt string, line, col int, limits Limits) *Diagnostic {
	runes := []rune(stmt)
	group, next, d := parseFlowGroup(fc, runes, 0, line, col, limits)
	if d != nil {
		return d
	}
	pos := next
	for {
		pos = skipSpaces(runes, pos)
		if pos >= len(runes) {
			return nil
		}
		style, label, hasLabel, next, d := parseFlowLink(runes, pos, line, col, limits)
		if d != nil {
			return d
		}
		pos = skipSpaces(runes, next)
		targets, next, d := parseFlowGroup(fc, runes, pos, line, col, limits)
		if d != nil {
			return d
		}
		for _, src := range group {
			for _, dst := range targets {
				if len(fc.edges) >= limits.MaxEdges {
					return diagAt(line, col+pos+1, "the diagram exceeds the %d-edge limit", limits.MaxEdges)
				}
				fc.edges = append(fc.edges, fedge{
					from: src, to: dst,
					label: label, hasLabel: hasLabel,
					dotted: style.dotted, thick: style.thick, arrow: style.arrow,
					line: line, col: src.col,
				})
			}
		}
		group = targets
		pos = next
	}
}

func parseFlowGroup(fc *flowchart, runes []rune, pos, line, col int, limits Limits) ([]*fnode, int, *Diagnostic) {
	var out []*fnode
	for {
		pos = skipSpaces(runes, pos)
		ref, next, d := parseNodeRef(runes, pos, line, col, limits)
		if d != nil {
			return nil, pos, d
		}
		pos = next
		n, existed := fc.index[ref.id]
		if !existed {
			if len(fc.nodes) >= limits.MaxNodes {
				return nil, pos, diagAt(line, col+ref.col, "the diagram exceeds the %d-node limit", limits.MaxNodes)
			}
			n = &fnode{id: ref.id, line: line, col: ref.col, shape: ref.shape}
			fc.index[ref.id] = n
			fc.nodes = append(fc.nodes, n)
		}
		if ref.hasLabel {
			n.label = ref.label
			n.hasLabel = true
			n.shape = ref.shape
		}
		out = append(out, n)
		pos = skipSpaces(runes, pos)
		if pos < len(runes) && runes[pos] == '&' {
			pos++
			continue
		}
		return out, pos, nil
	}
}

func parseNodeRef(runes []rune, pos, line, col int, limits Limits) (nodeRef, int, *Diagnostic) {
	start := pos
	for pos < len(runes) && isIDRune(runes[pos]) {
		pos++
	}
	if pos == start {
		if pos < len(runes) {
			return nodeRef{}, pos, diagAt(line, col+pos+1, "expected a node ID, found %q", string(runes[pos]))
		}
		return nodeRef{}, pos, diagAt(line, col+pos+1, "expected a node ID")
	}
	ref := nodeRef{id: string(runes[start:pos]), shape: ShapeRect, col: start + 1}
	// A space may separate an ID from its shape, but never from an edge.
	afterID := pos
	pos = skipSpaces(runes, pos)
	if pos < len(runes) && isShapeStart(runes, pos) {
		shape, label, hasLabel, next, d := parseNodeShape(runes, pos, line, col, limits)
		if d != nil {
			return ref, pos, d
		}
		if shape != -1 {
			ref.shape = shape
			ref.label = label
			ref.hasLabel = hasLabel
			pos = next
		}
	} else {
		pos = afterID
	}
	return ref, pos, nil
}

func isShapeStart(runes []rune, pos int) bool {
	if pos >= len(runes) {
		return false
	}
	switch runes[pos] {
	case '[', '(', '{', '>', '@':
		return true
	}
	return false
}

// parseNodeShape parses a node's bracket suffix. shape -1 means no suffix.
func parseNodeShape(runes []rune, pos, line, col int, limits Limits) (Shape, string, bool, int, *Diagnostic) {
	rest := string(runes[pos:])
	switch {
	case strings.HasPrefix(rest, "((("):
		return 0, "", false, pos, diagAt(line, col+pos+1, "double-circle nodes are not supported by the offline flowchart renderer")
	case strings.HasPrefix(rest, "(("):
		label, end, d := readDelimited(runes, pos+2, "))", line, col, limits)
		if d != nil {
			return 0, "", false, pos, d
		}
		return ShapeCircle, label, true, end, nil
	case strings.HasPrefix(rest, "(["):
		label, end, d := readDelimited(runes, pos+2, "])", line, col, limits)
		if d != nil {
			return 0, "", false, pos, d
		}
		return ShapeStadium, label, true, end, nil
	case strings.HasPrefix(rest, "[["):
		return 0, "", false, pos, diagAt(line, col+pos+1, "subroutine nodes are not supported by the offline flowchart renderer")
	case strings.HasPrefix(rest, "[("):
		return 0, "", false, pos, diagAt(line, col+pos+1, "cylinder nodes are not supported by the offline flowchart renderer")
	case strings.HasPrefix(rest, "{{"):
		return 0, "", false, pos, diagAt(line, col+pos+1, "hexagon nodes are not supported by the offline flowchart renderer")
	case strings.HasPrefix(rest, "[/"), strings.HasPrefix(rest, "[\\"):
		return 0, "", false, pos, diagAt(line, col+pos+1, "parallelogram and trapezoid nodes are not supported by the offline flowchart renderer")
	case strings.HasPrefix(rest, "@{"):
		return 0, "", false, pos, diagAt(line, col+pos+1, "the @{ shape } syntax is not supported; use [ ], ( ), ([ ]), (( )) or { }")
	case rest[0] == '>':
		return 0, "", false, pos, diagAt(line, col+pos+1, "asymmetric nodes are not supported by the offline flowchart renderer")
	case rest[0] == '[':
		label, end, d := readDelimited(runes, pos+1, "]", line, col, limits)
		if d != nil {
			return 0, "", false, pos, d
		}
		return ShapeRect, label, true, end, nil
	case rest[0] == '(':
		label, end, d := readDelimited(runes, pos+1, ")", line, col, limits)
		if d != nil {
			return 0, "", false, pos, d
		}
		return ShapeRounded, label, true, end, nil
	case rest[0] == '{':
		label, end, d := readDelimited(runes, pos+1, "}", line, col, limits)
		if d != nil {
			return 0, "", false, pos, d
		}
		return ShapeDiamond, label, true, end, nil
	}
	return -1, "", false, pos, nil
}

// readDelimited reads a label up to closer, honoring double quotes, then
// validates it against the label rules.
func readDelimited(runes []rune, pos int, closer string, line, col int, limits Limits) (string, int, *Diagnostic) {
	closing := []rune(closer)
	start := pos
	quote := false
	for i := pos; i < len(runes); i++ {
		if quote {
			if runes[i] == '"' {
				quote = false
			}
			continue
		}
		if runes[i] == '"' {
			quote = true
			continue
		}
		if hasRunesAt(runes, i, closing) {
			raw := strings.TrimSpace(string(runes[start:i]))
			label, d := normalizeLabel(raw, line, col+start+1, limits)
			if d != nil {
				return "", 0, d
			}
			return label, i + len(closing), nil
		}
	}
	return "", 0, diagAt(line, col+pos+1, "unterminated node label; expected %q", closer)
}

func hasRunesAt(runes []rune, i int, want []rune) bool {
	if i+len(want) > len(runes) {
		return false
	}
	for j, r := range want {
		if runes[i+j] != r {
			return false
		}
	}
	return true
}

// normalizeLabel strips optional quotes, rejects Markdown strings and HTML,
// and enforces the label budgets.
func normalizeLabel(raw string, line, col int, limits Limits) (string, *Diagnostic) {
	if len(raw) >= 2 && strings.HasPrefix(raw, "\"") && strings.HasSuffix(raw, "\"") {
		raw = raw[1 : len(raw)-1]
	}
	if strings.HasPrefix(raw, "`") {
		return "", diagAt(line, col, "Markdown strings in labels are not supported; use plain text with <br> for line breaks")
	}
	return validateLabel(raw, line, col, limits)
}

func validateLabel(raw string, line, col int, limits Limits) (string, *Diagnostic) {
	raw = stripControl(raw)
	if hasHTMLTag(raw) {
		return "", diagAt(line, col, "HTML is not supported in labels except <br> line breaks")
	}
	if n := countRunes(raw); n > limits.MaxLabelRunes {
		return "", diagAt(line, col, "label is %d code points; the limit is %d", n, limits.MaxLabelRunes)
	}
	if n := len(splitLabel(raw)); n > limits.MaxLabelLines {
		return "", diagAt(line, col, "label has %d lines; the limit is %d", n, limits.MaxLabelLines)
	}
	return raw, nil
}

// parseFlowLink parses one edge operator and its optional |label|.
func parseFlowLink(runes []rune, pos, line, col int, limits Limits) (linkStyle, string, bool, int, *Diagnostic) {
	rest := string(runes[pos:])
	reject := func(format string, args ...any) *Diagnostic {
		return diagAt(line, col+pos+1, format, args...)
	}
	switch {
	case strings.HasPrefix(rest, "~~~"):
		return linkStyle{}, "", false, pos, reject("invisible links (~~~) are not supported by the offline renderer")
	case strings.HasPrefix(rest, "o--o"), strings.HasPrefix(rest, "x--x"):
		return linkStyle{}, "", false, pos, reject("circle/cross end links are not supported by the offline renderer")
	case strings.HasPrefix(rest, "--o"), strings.HasPrefix(rest, "--x"):
		return linkStyle{}, "", false, pos, reject("circle/cross end links are not supported by the offline renderer")
	case strings.HasPrefix(rest, "<-->"):
		return linkStyle{}, "", false, pos, reject("bidirectional links are not supported by the offline renderer")
	case strings.HasPrefix(rest, "<--"):
		return linkStyle{}, "", false, pos, reject("left-pointing links are not supported; write the edge from source to target")
	case len(rest) >= 2 && strings.Contains(rest[:min(len(rest), 8)], "@"):
		return linkStyle{}, "", false, pos, reject("edge IDs and their properties are not supported")
	}

	var style linkStyle
	var n int
	switch {
	case strings.HasPrefix(rest, "-.->"):
		style, n = linkStyle{dotted: true, arrow: true}, 4
	case strings.HasPrefix(rest, "-.-"):
		style, n = linkStyle{dotted: true}, 3
	case strings.HasPrefix(rest, "-->"):
		style, n = linkStyle{arrow: true}, 3
	case strings.HasPrefix(rest, "---"):
		style, n = linkStyle{}, 3
	case strings.HasPrefix(rest, "==>"):
		style, n = linkStyle{thick: true, arrow: true}, 3
	case strings.HasPrefix(rest, "==="):
		style, n = linkStyle{thick: true}, 3
	case strings.HasPrefix(rest, "--"), strings.HasPrefix(rest, "-."), strings.HasPrefix(rest, "=="):
		return linkStyle{}, "", false, pos, reject("inline edge labels are not supported; use |label| after the arrow")
	default:
		return linkStyle{}, "", false, pos, reject("expected an edge (--> , --- , -.-> , ==> ) or '&' after the node")
	}
	pos += n
	pos = skipSpaces(runes, pos)
	if pos < len(runes) && runes[pos] == '|' {
		start := pos + 1
		end := start
		for end < len(runes) && runes[end] != '|' {
			end++
		}
		if end >= len(runes) {
			return linkStyle{}, "", false, pos, reject("unterminated edge label; expected a closing |")
		}
		label, d := validateLabel(strings.TrimSpace(string(runes[start:end])), line, col+start+1, limits)
		if d != nil {
			return linkStyle{}, "", false, pos, d
		}
		pos = end + 1
		return style, label, true, pos, nil
	}
	return style, "", false, pos, nil
}

func skipSpaces(runes []rune, pos int) int {
	for pos < len(runes) && (runes[pos] == ' ' || runes[pos] == '\t') {
		pos++
	}
	return pos
}

// isIDRune accepts the characters the offline renderer allows in node and
// participant IDs. '-' is excluded because it starts an edge operator; IDs
// that need it can be quoted only in full Mermaid, which this subset does
// not claim.
func isIDRune(r rune) bool {
	switch {
	case r == '_' || r == '.':
		return true
	case r >= '0' && r <= '9':
		return true
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		return true
	case r > 0x7F:
		return isLetter(r)
	}
	return false
}

func isLetter(r rune) bool {
	switch {
	case r >= 0x00C0 && r <= 0x024F, // Latin-1 supplement, Latin extended
		r >= 0x0370 && r <= 0x03FF, // Greek
		r >= 0x0400 && r <= 0x04FF, // Cyrillic
		r >= 0x3040 && r <= 0x30FF, // kana
		r >= 0x4E00 && r <= 0x9FFF, // Han
		r >= 0xAC00 && r <= 0xD7A3: // hangul
		return true
	}
	return false
}
