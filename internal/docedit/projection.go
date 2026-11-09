package docedit

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/egoist/mygo/ui/markdown/parser"

	"gorequest/internal/markdown"
)

// A Projection is the content view of one block: the text the in-place
// editor shows and edits, without the block's own syntax markers.
//
// The canonical Markdown stays the only stored text. A projection keeps the
// markers it hid (the "## " of a heading, the "> " of each quote line, the
// "- " of each list item) and puts them back in Commit, so saving still
// writes correct Markdown and a heading never loses its level. Blocks that
// cannot be projected safely (tables, HTML, rules, front matter, lists
// containing non-text blocks, complex or lazy quotes, ...) report Active() == false
// and are edited as their exact source.
type Projection struct {
	mode projMode
	// text is the LF-normalized projected text shown in the editor.
	text string
	// eol is "\r\n" when the block's source used CRLF, else "\n".
	eol string

	// heading: prefix + content + suffix is the source heading. For setext
	// headings, suffix holds eol + underline.
	prefix, suffix string
	setextLevel    int
	fence          bool

	// lines: one entry per source line for Quote and List blocks.
	lines []projLine
}

type projMode int

const (
	projNone projMode = iota
	projHeading
	projLines
)

type projLine struct {
	prefix string
	text   string
}

var (
	atxOpenRe         = regexp.MustCompile(`^( {0,3}#{1,6})(.*)$`)
	onlyHashesRe      = regexp.MustCompile(`^#+[ \t]*$`)
	closingAtxRe      = regexp.MustCompile(`([ \t]+#+[ \t]*)$`)
	trailingHashRe    = regexp.MustCompile(`(^|[ \t])(#+[ \t]*)$`)
	escapedTailHashRe = regexp.MustCompile(`(^|[ \t])\\(#+[ \t]*)$`)
	setextRuleRe      = regexp.MustCompile(`^ {0,3}(=+|-+)[ \t]*$`)
	thematicLineRe    = regexp.MustCompile(`^ {0,3}(?:-[ \t]*-[ \t]*-[-\t ]*|\*[ \t]*\*[ \t]*\*[*\t ]*|_[ \t]*_[ \t]*_[_\t ]*)$`)

	// Source prefix recognition follows an already parsed list; AST metadata
	// supplies item depth and numbering for its native marker gutter.
	flatItemRe     = regexp.MustCompile(`^(?:[-*+]|\d{1,9}[.)])(?:[ \t]+(?:\[[ xX]\](?:[ \t]+|$))?|$)`)
	orderedPartsRe = regexp.MustCompile(`^(\d{1,9})([.)])(.*)$`)
	checkedTaskRe  = regexp.MustCompile(`\[[xX]\]`)
)

// NewProjection projects block b. The result is always valid: a block that
// cannot be projected yields an identity projection whose Text is the
// LF-normalized EditText and whose Commit restores the block's line endings.
func NewProjection(b Block) Projection {
	eol := "\n"
	if strings.Contains(b.Source, "\r\n") {
		eol = "\r\n"
	}
	src := b.EditText()
	if eol == "\r\n" {
		src = strings.ReplaceAll(src, "\r\n", "\n")
	}
	src = strings.TrimSuffix(src, "\r")
	ident := Projection{mode: projNone, text: src, eol: eol}
	if b.FrontMatter {
		return ident
	}
	switch b.Kind {
	case markdown.Heading:
		if p, ok := projectHeading(src, eol); ok {
			return p
		}
	case markdown.Quote:
		if p, ok := projectQuote(src, eol); ok {
			return p
		}
	case markdown.CodeBlock:
		if p, ok := projectFence(src, eol); ok {
			return p
		}
	case markdown.List:
		if p, ok := projectList(src, eol); ok {
			return p
		}
	}
	return ident
}

func projectHeading(src, eol string) (Projection, bool) {
	lines := strings.Split(src, "\n")
	first := lines[0]
	if len(lines) == 1 {
		m := atxOpenRe.FindStringSubmatch(first)
		if m == nil {
			return Projection{}, false
		}
		hashes, rem := m[1], m[2]
		if rem == "" {
			return Projection{mode: projHeading, text: "", eol: eol, prefix: hashes}, true
		}
		if rem[0] != ' ' && rem[0] != '\t' {
			return Projection{}, false
		}
		wsLen := 0
		for wsLen < len(rem) && (rem[wsLen] == ' ' || rem[wsLen] == '\t') {
			wsLen++
		}
		prefix := hashes + rem[:wsLen]
		afterWS := rem[wsLen:]
		if afterWS == "" {
			return Projection{mode: projHeading, text: "", eol: eol, prefix: prefix}, true
		}
		if onlyHashesRe.MatchString(afterWS) {
			return Projection{mode: projHeading, text: "", eol: eol, prefix: prefix, suffix: afterWS}, true
		}
		if idx := closingAtxRe.FindStringSubmatchIndex(afterWS); idx != nil {
			// A backslash directly before the spaces does not affect the
			// closing hash sequence; a backslash before '#' never matches
			// closingAtxRe because '#' must follow whitespace.
			return Projection{
				mode:   projHeading,
				text:   afterWS[:idx[2]],
				eol:    eol,
				prefix: prefix,
				suffix: afterWS[idx[2]:],
			}, true
		}
		return Projection{
			mode:   projHeading,
			text:   unescapeClosingHashes(afterWS),
			eol:    eol,
			prefix: prefix,
		}, true
	}
	if len(lines) == 2 {
		under := lines[1]
		m := setextRuleRe.FindStringSubmatch(under)
		if m == nil || strings.TrimSpace(first) == "" || strings.HasPrefix(strings.TrimLeft(first, " \t"), "#") {
			return Projection{}, false
		}
		level := 2
		if m[1][0] == '=' {
			level = 1
		}
		return Projection{
			mode:        projHeading,
			text:        first,
			eol:         eol,
			suffix:      eol + under,
			setextLevel: level,
		}, true
	}
	return Projection{}, false
}

func projectQuote(src, eol string) (Projection, bool) {
	raw := strings.Split(src, "\n")
	lines := make([]projLine, len(raw))
	texts := make([]string, len(raw))
	wantDepth := 0
	for i, l := range raw {
		pre, depth, ok := matchQuotePrefix(l)
		if !ok {
			return Projection{}, false
		}
		if i == 0 {
			wantDepth = depth
		} else if depth != wantDepth {
			return Projection{}, false
		}
		rest := l[len(pre):]
		if quoteInnerStartsBlock(rest, i > 0 && strings.TrimSpace(texts[i-1]) != "") {
			return Projection{}, false
		}
		lines[i] = projLine{prefix: pre, text: rest}
		texts[i] = rest
	}
	return Projection{mode: projLines, text: strings.Join(texts, "\n"), eol: eol, lines: lines}, true
}

func matchQuotePrefix(line string) (string, int, bool) {
	pos := 0
	depth := 0
	for pos < len(line) {
		sp := 0
		for pos+sp < len(line) && line[pos+sp] == ' ' && sp < 3 {
			sp++
		}
		if pos+sp >= len(line) || line[pos+sp] != '>' {
			break
		}
		pos += sp + 1
		depth++
		if pos < len(line) && line[pos] == ' ' {
			pos++
		}
	}
	if depth == 0 {
		return "", 0, false
	}
	return line[:pos], depth, true
}

func quoteInnerStartsBlock(rest string, afterText bool) bool {
	if strings.HasPrefix(rest, "    ") || strings.HasPrefix(rest, "\t") {
		return true
	}
	t := strings.TrimLeft(rest, " \t")
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") || strings.HasPrefix(t, "|") {
		return true
	}
	if thematicLineRe.MatchString(rest) || flatItemRe.MatchString(t) {
		return true
	}
	if afterText && setextRuleRe.MatchString(rest) {
		return true
	}
	return false
}

// ListLine describes the source prefix of one visible logical line. A blank
// Marker denotes a continuation/loose line; Indent still aligns wrapped text.
type ListLine struct {
	Prefix, Marker   string
	Indent           int
	Task, Checked    bool
	Depth, List      int
	GapBefore, Loose bool
}

func (p Projection) ListLines(text string) []ListLine {
	r := p.render(normalizeLF(text))
	lines := strings.Split(r.src, p.lineEnding())
	out := make([]ListLine, len(lines))
	for i, l := range lines {
		if i >= len(r.pre) {
			break
		}
		pre := l[:min(r.pre[i], len(l))]
		out[i].Prefix = pre
		trimmed := strings.TrimLeft(pre, " \t")
		out[i].Indent = len(pre) - len(trimmed)
		if m := flatItemRe.FindString(trimmed); m != "" {
			marker := strings.Fields(m)
			if len(marker) > 0 {
				out[i].Marker = marker[0]
			}
			out[i].Task = strings.Contains(m, "[ ]") || strings.Contains(m, "[x]") || strings.Contains(m, "[X]")
			out[i].Checked = checkedTaskRe.MatchString(m)
		}
	}
	// Derive nesting, item starts and visible numbering from the real AST.
	// Source indentation can vary (tabs, padded markers); it is not a depth.
	d := parser.Parse(r.src)
	counts := map[int]int{}
	for i := 0; i < d.Len(); i++ {
		item := d.Block(i)
		if item.Kind != parser.Item {
			continue
		}
		parent := d.Block(item.Parent)
		depth := 0
		for p := parent.Parent; p > 0; p = d.Block(p).Parent {
			if d.Block(p).Kind == parser.List {
				depth++
			}
		}
		first := strings.Count(r.src[:item.Start], "\n")
		last := strings.Count(r.src[:item.End], "\n")
		for line := first; line <= last && line < len(out); line++ {
			out[line].Depth = depth
			out[line].List = item.Parent
		}
		if first < len(out) {
			out[first].GapBefore = counts[item.Parent] > 0
			out[first].Loose = !parent.Tight
			if parent.Ordered {
				out[first].Marker = strconv.Itoa(parent.Number+counts[item.Parent]) + string(parent.Marker)
			}
		}
		counts[item.Parent]++
	}
	return out
}

func projectList(src, eol string) (Projection, bool) {
	parsed := parser.Parse(src)
	for i := 0; i < parsed.Len(); i++ {
		switch parsed.Block(i).Kind {
		case parser.CodeBlock, parser.HTMLBlock, parser.Table, parser.Heading, parser.Quote, parser.ThematicBreak:
			return Projection{}, false
		}
	}

	raw := strings.Split(src, "\n")
	lines := make([]projLine, len(raw))
	texts := make([]string, len(raw))
	for i, l := range raw {
		indent := len(l) - len(strings.TrimLeft(l, " \t"))
		pre := flatItemRe.FindString(l[indent:])
		if pre != "" {
			pre = l[:indent] + pre
		} else {
			// Continuations and blank lines belong to the already parsed list.
			pre = l[:indent]
			if i == 0 {
				return Projection{}, false
			}
		}
		rest := l[len(pre):]
		// Embedded block structures need their own adapters; report the source
		// fallback explicitly instead of stripping their significant indentation.
		if listInnerStartsBlock(rest) {
			return Projection{}, false
		}
		lines[i] = projLine{prefix: pre, text: rest}
		texts[i] = rest
	}
	return Projection{mode: projLines, text: strings.Join(texts, "\n"), eol: eol, lines: lines}, true
}

func projectFence(src, eol string) (Projection, bool) {
	lines := strings.Split(src, "\n")
	if len(lines) < 2 {
		return Projection{}, false
	}
	open := strings.TrimLeft(lines[0], " ")
	if len(open) < 3 || (open[0] != '`' && open[0] != '~') {
		return Projection{}, false
	}
	n := 0
	for n < len(open) && open[n] == open[0] {
		n++
	}
	close := strings.TrimSpace(lines[len(lines)-1])
	if len(close) < n || strings.Trim(close, string(open[0])) != "" {
		return Projection{}, false
	}
	return Projection{mode: projHeading, text: strings.Join(lines[1:len(lines)-1], "\n"), eol: eol, prefix: lines[0] + eol, suffix: eol + lines[len(lines)-1], fence: true}, true
}

func listInnerStartsBlock(rest string) bool {
	if strings.HasPrefix(rest, "    ") || strings.HasPrefix(rest, "\t") {
		return true
	}
	t := strings.TrimLeft(rest, " \t")
	if t == "" {
		return false
	}
	if strings.HasPrefix(t, "#") || strings.HasPrefix(t, "```") || strings.HasPrefix(t, "~~~") || strings.HasPrefix(t, ">") || strings.HasPrefix(t, "|") {
		return true
	}
	return thematicLineRe.MatchString(rest)
}

// Active reports whether the block hides syntax markers.
func (p Projection) Active() bool { return p.mode != projNone }

// Text is the projected text the editor shows.
func (p Projection) Text() string { return p.text }

func (p Projection) lineEnding() string {
	if p.eol != "" {
		return p.eol
	}
	return "\n"
}

// rendering is the source built from edited projected text, with the line
// geometry offset mapping needs.
type rendering struct {
	src string
	// projStart[i] / srcStart[i] are the line starts; pre[i] is the prefix
	// length of that line in src.
	projStart, srcStart, pre []int
}

// Commit returns the block source (without the block-boundary newline) for
// the edited projected text.
func (p Projection) Commit(text string) string {
	text = normalizeLF(text)
	return p.render(text).src
}

// SourceOffset maps a caret offset in the projected text to the matching
// offset in Commit(text).
func (p Projection) SourceOffset(text string, off int) int {
	off = max(0, min(off, len(text)))
	if strings.Contains(text, "\r\n") {
		off = len(normalizeLF(text[:off]))
		text = normalizeLF(text)
	}
	if p.mode == projNone && p.lineEnding() == "\n" {
		return off
	}
	r := p.render(text)
	j := strings.Count(text[:off], "\n")
	j = min(j, len(r.projStart)-1)
	return r.srcStart[j] + r.pre[j] + (off - r.projStart[j])
}

// ProjectedOffset maps an offset in the block's source (relative to the
// block's start) to the matching offset in Text.
func (p Projection) ProjectedOffset(off int) int {
	if p.mode == projNone && p.lineEnding() == "\n" {
		return max(0, min(off, len(p.text)))
	}
	r := p.render(p.text)
	off = max(0, min(off, len(r.src)))
	j := 0
	for i := range r.srcStart {
		if r.srcStart[i] <= off {
			j = i
		}
	}
	col := max(0, off-r.srcStart[j]-r.pre[j])
	// The caret cannot sit past the end of its line's text.
	end := len(p.text)
	if j+1 < len(r.projStart) {
		end = r.projStart[j+1] - 1
	}
	return min(r.projStart[j]+col, end)
}

func normalizeLF(s string) string {
	if strings.Contains(s, "\r\n") {
		s = strings.ReplaceAll(s, "\r\n", "\n")
	}
	return s
}

func (p Projection) render(text string) rendering {
	switch p.mode {
	case projHeading:
		return p.renderHeading(text)
	case projLines:
		return p.renderLines(text)
	}
	eol := p.lineEnding()
	if eol == "\n" {
		return rendering{src: text, projStart: []int{0}, srcStart: []int{0}, pre: []int{0}}
	}
	raw := strings.Split(text, "\n")
	var sb strings.Builder
	r := rendering{
		projStart: make([]int, len(raw)),
		srcStart:  make([]int, len(raw)),
		pre:       make([]int, len(raw)),
	}
	pos := 0
	for i, l := range raw {
		if i > 0 {
			sb.WriteString(eol)
		}
		r.projStart[i] = pos
		r.srcStart[i] = sb.Len()
		sb.WriteString(l)
		pos += len(l) + 1
	}
	r.src = sb.String()
	return r
}

func (p Projection) renderHeading(text string) rendering {
	if p.fence {
		r := Projection{eol: p.eol}.render(text)
		for i := range r.srcStart {
			r.srcStart[i] += len(p.prefix)
		}
		r.src = p.prefix + r.src + p.suffix
		return r
	}
	eol := p.lineEnding()
	first, rest, more := strings.Cut(text, "\n")
	// Pressing Enter at the very start of a non-empty heading pushes the
	// heading down by inserting a blank line above it instead of leaving an
	// empty "#" marker behind and demoting the heading text to a paragraph.
	if more && first == "" && strings.TrimSpace(rest) != "" {
		sub := p.renderHeading(rest)
		r := rendering{
			src:       eol + sub.src,
			projStart: make([]int, 0, len(sub.projStart)+1),
			srcStart:  make([]int, 0, len(sub.srcStart)+1),
			pre:       make([]int, 0, len(sub.pre)+1),
		}
		r.projStart = append(r.projStart, 0)
		r.srcStart = append(r.srcStart, 0)
		r.pre = append(r.pre, 0)
		for i := range sub.projStart {
			r.projStart = append(r.projStart, sub.projStart[i]+1)
			r.srcStart = append(r.srcStart, sub.srcStart[i]+len(eol))
			r.pre = append(r.pre, sub.pre[i])
		}
		return r
	}
	prefix, suffix := p.prefix, p.suffix
	if p.setextLevel > 0 && strings.TrimSpace(first) == "" {
		// An empty setext heading is not Markdown; keep the level as ATX.
		prefix = strings.Repeat("#", p.setextLevel) + " "
		suffix = ""
	}
	if p.setextLevel == 0 && first != "" && prefix != "" && !strings.HasSuffix(prefix, " ") && !strings.HasSuffix(prefix, "\t") {
		prefix += " "
	}
	if suffix == "" && p.setextLevel == 0 {
		first = escapeClosingHashes(first)
	}
	head := prefix + first + suffix
	r := rendering{
		projStart: []int{0},
		srcStart:  []int{0},
		pre:       []int{len(prefix)},
	}
	if !more {
		r.src = head
		return r
	}
	// A heading is one line: what follows a newline typed inside or at the
	// end of it becomes the paragraph after it.
	restLines := strings.Split(rest, "\n")
	restSrc := strings.Join(restLines, eol)
	sep := eol + eol
	r.src = head + sep + restSrc
	pos := len(first) + 1
	spos := len(head) + len(sep)
	r.projStart = append(r.projStart, pos)
	r.srcStart = append(r.srcStart, spos)
	r.pre = append(r.pre, 0)
	for _, l := range restLines[:len(restLines)-1] {
		pos += len(l) + 1
		spos += len(l) + len(eol)
		r.projStart = append(r.projStart, pos)
		r.srcStart = append(r.srcStart, spos)
		r.pre = append(r.pre, 0)
	}
	return r
}

// escapeClosingHashes keeps a literal trailing "#" typed into an ATX heading
// from being read as its closing sequence.
func escapeClosingHashes(s string) string {
	m := trailingHashRe.FindStringSubmatchIndex(s)
	if m == nil {
		return s
	}
	hashStart := m[4]
	return s[:hashStart] + `\` + s[hashStart:]
}

func unescapeClosingHashes(s string) string {
	m := escapedTailHashRe.FindStringSubmatchIndex(s)
	if m == nil {
		return s
	}
	slashIdx := m[4] - 1
	return s[:slashIdx] + s[slashIdx+1:]
}

func (p Projection) renderLines(text string) rendering {
	eol := p.lineEnding()
	old := make([]string, len(p.lines))
	for i, l := range p.lines {
		old[i] = l.text
	}
	nw := strings.Split(text, "\n")
	pre := 0
	for pre < len(old) && pre < len(nw) && old[pre] == nw[pre] {
		pre++
	}
	suf := 0
	for suf < len(old)-pre && suf < len(nw)-pre && old[len(old)-1-suf] == nw[len(nw)-1-suf] {
		suf++
	}
	prefixes := make([]string, len(nw))
	midOld := len(old) - pre - suf
	for j := range nw {
		switch {
		case j < pre:
			prefixes[j] = p.lines[j].prefix
		case j >= len(nw)-suf:
			prefixes[j] = p.lines[len(old)-(len(nw)-j)].prefix
		case j-pre < midOld:
			prefixes[j] = p.lines[pre+(j-pre)].prefix
		case j > 0:
			prefixes[j] = freshLinePrefix(prefixes[j-1])
		default:
			prefixes[j] = freshLinePrefix(p.lines[0].prefix)
		}
	}
	if len(nw) != len(old) {
		renumberOrderedPrefixes(p.lines, prefixes)
	}
	for j, l := range nw {
		if l != "" && prefixes[j] != "" && !strings.HasSuffix(prefixes[j], " ") && !strings.HasSuffix(prefixes[j], "\t") {
			prefixes[j] += " "
		}
	}
	var sb strings.Builder
	r := rendering{
		projStart: make([]int, len(nw)),
		srcStart:  make([]int, len(nw)),
		pre:       make([]int, len(nw)),
	}
	pos := 0
	for j, l := range nw {
		if j > 0 {
			sb.WriteString(eol)
		}
		r.projStart[j] = pos
		r.srcStart[j] = sb.Len()
		r.pre[j] = len(prefixes[j])
		sb.WriteString(prefixes[j])
		sb.WriteString(l)
		pos += len(l) + 1
	}
	r.src = sb.String()
	return r
}

// freshLinePrefix builds the marker for a newly inserted line: task checkboxes
// start unchecked and bare markers receive a trailing space.
func freshLinePrefix(prefix string) string {
	out := checkedTaskRe.ReplaceAllString(prefix, "[ ]")
	if out != "" && !strings.HasSuffix(out, " ") && !strings.HasSuffix(out, "\t") {
		out += " "
	}
	return out
}

// renumberOrderedPrefixes keeps ordered-list numbers consecutive from the
// list's starting number when items are inserted or removed, while leaving
// intentional constant numbering (such as all "1.") untouched.
func renumberOrderedPrefixes(orig []projLine, prefixes []string) {
	if len(orig) == 0 {
		return
	}
	first := orderedPartsRe.FindStringSubmatch(orig[0].prefix)
	if first == nil {
		return
	}
	startNum, err := strconv.Atoi(first[1])
	if err != nil {
		return
	}
	constant := len(orig) >= 2
	for i := 1; i < len(orig) && constant; i++ {
		m := orderedPartsRe.FindStringSubmatch(orig[i].prefix)
		if m == nil || m[1] != first[1] {
			constant = false
		}
	}
	for j, pre := range prefixes {
		m := orderedPartsRe.FindStringSubmatch(pre)
		if m == nil {
			continue
		}
		num := startNum
		if !constant {
			num = startNum + j
		}
		tail := m[3]
		if tail == "" {
			tail = " "
		}
		prefixes[j] = strconv.Itoa(num) + m[2] + tail
	}
}
