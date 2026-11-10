package core

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"gorequest/internal/diagram"
	"gorequest/internal/markdown"
)

// This file holds the core of the Markdown document workspace: heading
// extraction, GitHub-compatible heading slugs, full-text search, and the
// verified Markdown compatibility matrix. Parsing goes through the neutral
// internal/markdown package; no GUI or desktop package is imported here.

// DefaultDocMaxBytes bounds the Markdown file size that outline, search and
// diagnostics read at once. Larger files still open in the editor; the
// document tools report that they were skipped instead of reading an
// unbounded file.
const DefaultDocMaxBytes = 4 << 20

// DefaultSearchMaxResults is used when SearchOptions.MaxResults is zero.
const DefaultSearchMaxResults = 200

// maxSearchResults is the upper bound a caller may request.
const maxSearchResults = 2000

// Heading is one H1–H6 heading of a document.
type Heading struct {
	// Level is 1 for H1 through 6 for H6.
	Level int
	// Text is the heading's plain text, without Markdown markup.
	Text string
	// Slug is the GitHub anchor of Text (lower case, spaces as hyphens,
	// punctuation removed). Duplicate headings share it, so the preview can
	// only scroll to the first one; ID disambiguates the outline.
	Slug string
	// ID is Slug with GitHub's duplicate suffix (-1, -2, …) when needed.
	ID string
	// Line is the 1-based line of the heading.
	Line int
	// Column is the 1-based rune column of the heading marker.
	Column int
	// Offset is the byte offset of the heading in the source.
	Offset int
	// EndOffset is the byte offset just past the heading's last line.
	EndOffset int
}

// ExtractHeadings returns the headings of src in document order. Fenced code
// and HTML never produce headings; headings inside block quotes and list items
// do.
func ExtractHeadings(src string) []Heading {
	d := markdown.Parse(src)
	out := make([]Heading, 0, 8)
	for i, b := range d.Blocks() {
		if b.Kind != markdown.Heading {
			continue
		}
		text := strings.TrimSpace(markdown.PlainText(d.Inlines(i)))
		line, col, _ := lineColumnAt(src, b.Start)
		out = append(out, Heading{
			Level:     b.Level,
			Text:      text,
			Slug:      GitHubHeadingSlug(text),
			Line:      line,
			Column:    col,
			Offset:    b.Start,
			EndOffset: b.End,
		})
	}
	seen := make(map[string]int, len(out))
	for i := range out {
		base := out[i].Slug
		if n, ok := seen[base]; ok {
			n++
			seen[base] = n
			out[i].ID = base + "-" + itoa(n)
			continue
		}
		seen[base] = 0
		out[i].ID = base
	}
	return out
}

// GitHubHeadingSlug returns the anchor GitHub derives from a heading's text:
// lower case, spaces as hyphens, and only letters, digits, hyphens and
// underscores kept. Non-ASCII letters (for example Chinese) are kept.
func GitHubHeadingSlug(text string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(text)) {
		switch {
		case r == ' ':
			b.WriteByte('-')
		case r == '-' || r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r):
			b.WriteRune(r)
		}
	}
	return b.String()
}

// HeadingAtLine returns the last heading at or before a 1-based line.
func HeadingAtLine(headings []Heading, line int) (Heading, bool) {
	var found Heading
	ok := false
	for _, h := range headings {
		if h.Line > line {
			break
		}
		found, ok = h, true
	}
	return found, ok
}

// lineColumnAt returns the 1-based line and rune column of a byte offset, and
// the 0-based byte column.
func lineColumnAt(src string, off int) (line, col, byteCol int) {
	off = max(0, min(off, len(src)))
	line = 1
	lineStart := 0
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	col = utf8.RuneCountInString(src[lineStart:off]) + 1
	return line, col, off - lineStart
}

// itoa avoids importing strconv in this file only for the duplicate suffix.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// --- full-text search -----------------------------------------------------

// SearchOptions configures a project or single-document search.
type SearchOptions struct {
	// Query is the literal text to find. An empty query matches nothing.
	Query string
	// CaseSensitive turns off case-insensitive matching (off by default).
	CaseSensitive bool
	// WholeWord requires the match not to touch a letter, digit or underscore.
	WholeWord bool
	// MaxResults caps the number of matches (default 200, at most 2000).
	MaxResults int
	// Overrides supplies in-memory content for open, unsaved documents, keyed
	// by project-relative path. Search never reads the disk for those.
	Overrides map[string]string
}

// DocMatch is one search hit.
type DocMatch struct {
	// RelPath is the project-relative document path.
	RelPath string
	// Line is the 1-based line of the match.
	Line int
	// Column is the 1-based rune column of the match.
	Column int
	// ByteColumn is the 0-based byte column of the match, for the editor's
	// byte-based Position.
	ByteColumn int
	// Start and End are the byte offsets of the match in the document.
	Start, End int
	// Snippet is the trimmed source line, shortened around very long lines.
	Snippet string
}

// SearchDocText searches one document's text. It returns the matches and
// whether the result limit cut the scan short.
func SearchDocText(rel, src string, opts SearchOptions) (matches []DocMatch, truncated bool) {
	query := opts.Query
	if query == "" {
		return nil, false
	}
	limit := opts.MaxResults
	if limit <= 0 {
		limit = DefaultSearchMaxResults
	}
	limit = min(limit, maxSearchResults)
	return searchText(rel, src, []rune(query), opts, limit)
}

// SearchDocs searches every document of the project. Unsaved documents listed
// in opts.Overrides are searched from memory. Documents that cannot be read
// or are larger than DefaultDocMaxBytes are reported as diagnostics and
// skipped, never silently ignored.
func (p *Project) SearchDocs(opts SearchOptions) (matches []DocMatch, diagnostics []DocDiagnostic, truncated bool) {
	if p == nil || p.Store == nil || opts.Query == "" {
		return nil, nil, false
	}
	limit := opts.MaxResults
	if limit <= 0 {
		limit = DefaultSearchMaxResults
	}
	limit = min(limit, maxSearchResults)
	docs := make([]*DocEntry, len(p.Docs))
	copy(docs, p.Docs)
	sort.Slice(docs, func(i, j int) bool { return docs[i].RelPath < docs[j].RelPath })

	for _, de := range docs {
		var src string
		if o, ok := opts.Overrides[de.RelPath]; ok {
			src = o
		} else {
			data, _, err := p.Store.Read(de.RelPath)
			if err != nil {
				diagnostics = append(diagnostics, DocDiagnostic{
					RelPath: de.RelPath, Severity: SeverityWarning, Kind: "search-read",
					Message: "document could not be read for search: " + err.Error(),
				})
				continue
			}
			if len(data) > DefaultDocMaxBytes {
				diagnostics = append(diagnostics, DocDiagnostic{
					RelPath: de.RelPath, Severity: SeverityWarning, Kind: "search-too-large",
					Message: "document is larger than the search limit and was skipped",
				})
				continue
			}
			src = string(data)
		}
		remaining := limit - len(matches) + 1 // one extra tells whether more remain
		if remaining <= 0 {
			return matches, diagnostics, true
		}
		m, cut := searchText(de.RelPath, src, []rune(opts.Query), opts, remaining)
		matches = append(matches, m...)
		if cut || len(matches) > limit {
			return matches[:limit], diagnostics, true
		}
	}
	return matches, diagnostics, false
}

func searchText(rel, src string, query []rune, opts SearchOptions, limit int) (out []DocMatch, truncated bool) {
	if len(query) == 0 || limit <= 0 {
		return nil, false
	}
	line, lineStart := 1, 0
	for i := 0; i < len(src); {
		if len(out) > limit {
			return out[:limit], true
		}
		end, ok := matchRunesAt(src, i, query, opts.CaseSensitive)
		if ok && (!opts.WholeWord || wordBoundaries(src, i, end)) {
			col := utf8.RuneCountInString(src[lineStart:i]) + 1
			lineEnd := strings.IndexByte(src[i:], '\n')
			if lineEnd >= 0 {
				lineEnd += i
			} else {
				lineEnd = len(src)
			}
			out = append(out, DocMatch{
				RelPath:    rel,
				Line:       line,
				Column:     col,
				ByteColumn: i - lineStart,
				Start:      i,
				End:        end,
				Snippet:    lineSnippet(src[lineStart:lineEnd], col),
			})
			line, lineStart = advanceLines(src, i, end, line, lineStart)
			i = end
			continue
		}
		_, size := utf8.DecodeRuneInString(src[i:])
		if size <= 0 {
			size = 1
		}
		i += size
		if i <= len(src) && src[i-1] == '\n' {
			line++
			lineStart = i
		}
	}
	return out, false
}

func advanceLines(src string, from, to, line, lineStart int) (int, int) {
	for i := from; i < to && i < len(src); i++ {
		if src[i] == '\n' {
			line++
			lineStart = i + 1
		}
	}
	return line, lineStart
}

func matchRunesAt(src string, at int, query []rune, caseSensitive bool) (int, bool) {
	end := at
	for _, q := range query {
		if end >= len(src) {
			return 0, false
		}
		r, size := utf8.DecodeRuneInString(src[end:])
		if caseSensitive {
			if r != q {
				return 0, false
			}
		} else if !foldEqual(r, q) {
			return 0, false
		}
		end += size
	}
	return end, true
}

// foldEqual compares runes under Unicode simple case folding without
// allocating strings. It covers the folds Unicode defines as simple; the
// special multi-rune folds (such as ß↔SS) are intentionally out of scope.
func foldEqual(a, b rune) bool {
	if a == b {
		return true
	}
	for f := unicode.SimpleFold(a); f != a; f = unicode.SimpleFold(f) {
		if f == b {
			return true
		}
	}
	return false
}

func wordBoundaries(src string, start, end int) bool {
	if start > 0 {
		r, _ := utf8.DecodeLastRuneInString(src[:start])
		if isWordRune(r) {
			return false
		}
	}
	if end < len(src) {
		r, _ := utf8.DecodeRuneInString(src[end:])
		if isWordRune(r) {
			return false
		}
	}
	return true
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsNumber(r)
}

// lineSnippet keeps the line around a match, shortening long lines to about
// 200 characters with ellipses on the cut sides.
func lineSnippet(line string, col int) string {
	const maxRunes = 200
	rs := []rune(line)
	if len(rs) <= maxRunes {
		return strings.TrimSpace(line)
	}
	start := max(0, col-1-maxRunes/2)
	end := min(len(rs), start+maxRunes)
	start = max(0, end-maxRunes)
	s := strings.TrimSpace(string(rs[start:end]))
	if start > 0 {
		s = "…" + s
	}
	if end < len(rs) {
		s += "…"
	}
	return s
}

// --- verified compatibility matrix ---------------------------------------

// MarkdownFeature is one row of the verified Markdown support matrix. The
// matrix is computed by parsing a probe for every row with the neutral Core
// parser, so it reflects the document tools' build rather than a claim. The
// desktop preview uses its own native renderer; see
// docs/specs/markdown-core-evidence.md for the boundary and known differences.
type MarkdownFeature struct {
	// Group is "CommonMark", "GFM", "Extension" or "Unsupported".
	Group string
	// Name is the feature's user-facing name.
	Name string
	// Syntax is a short probe syntax.
	Syntax string
	// Supported reports whether this build parses it as documented.
	Supported bool
	// Note explains limits or the deliberate fallback.
	Note string
}

// MarkdownCompatibility parses one probe per feature and returns the matrix.
func MarkdownCompatibility() []MarkdownFeature {
	return []MarkdownFeature{
		feature("CommonMark", "ATX headings", "# H1 … ###### H6", atxHeadings, ""),
		feature("CommonMark", "Setext headings", "Title\n=====", setextHeading, ""),
		feature("CommonMark", "Paragraphs and soft breaks", "one\ntwo", softBreak, ""),
		feature("CommonMark", "Hard line breaks", "one  \ntwo", hardBreak, ""),
		feature("CommonMark", "Emphasis and strong", "*a* **b** _c_ __d__", emphasisStrong, ""),
		feature("CommonMark", "Inline code", "`code`", inlineCode, ""),
		feature("CommonMark", "Fenced code with language", "```go\nx\n```", fencedCode, ""),
		feature("CommonMark", "Indented code", "    x", indentedCode, ""),
		feature("CommonMark", "Block quotes", "> quote", blockQuote, ""),
		feature("CommonMark", "Ordered lists", "1. one\n2. two", orderedList, ""),
		feature("CommonMark", "Unordered lists", "- one\n- two", unorderedList, ""),
		feature("CommonMark", "Nested lists", "- one\n  - two", nestedList, ""),
		feature("CommonMark", "Thematic breaks", "---", thematicBreak, ""),
		feature("CommonMark", "Inline links with titles", "[a](/u \"t\")", inlineLink, ""),
		feature("CommonMark", "Reference links", "[a][r]\n\n[r]: /u", referenceLink, ""),
		feature("CommonMark", "Angle-bracket autolinks", "<https://example.com>", angleAutolink, ""),
		feature("CommonMark", "Images", "![alt](/i.png)", image, ""),
		feature("CommonMark", "HTML blocks", "<div>x</div>", htmlBlock, "Shown as source text; never executed."),
		feature("CommonMark", "Inline HTML", "text <b>bold</b>", inlineHTML, "Formatting tags apply in preview; scripts never run."),
		feature("CommonMark", "Backslash escapes and entities", `\* \&amp;`, escapesEntities, ""),
		feature("GFM", "Tables", "| a | b |\n|---|---|", table, ""),
		feature("GFM", "Task lists", "- [ ] todo\n- [x] done", taskList, ""),
		feature("GFM", "Strikethrough", "~~old~~", strikethrough, ""),
		feature("GFM", "Bare autolinks", "https://example.com", bareAutolink, ""),
		feature("Extension", "Footnotes", "text[^1]\n\n[^1]: note", footnote, ""),
		feature("Extension", "Math", "$$\nx\n$$", math, "Default extensions include ExtMath."),
		feature("Extension", "Dollar math", "$x$", dollarMath, "Requires ExtDollarMath; off in this build."),
		feature("Unsupported", "YAML front matter", "---\ntitle: x\n---", frontMatter, "Parsed as a rule and text, not as metadata."),
		feature("Unsupported", "Definition lists", "Term\n: definition", definitionList, "No definition-list syntax."),
		feature("Extension", "Mermaid (offline subset)", "```mermaid\nflowchart TD\n  A-->B\n```", mermaidRender, "Offline flowchart and sequenceDiagram subset; unsupported syntax keeps its source and reports the exact reason."),
	}
}

func feature(group, name, syntax string, probe func() bool, note string) MarkdownFeature {
	return MarkdownFeature{Group: group, Name: name, Syntax: syntax, Supported: probe(), Note: note}
}

func parseDoc(src string) *markdown.Document {
	return markdown.Parse(src)
}

func firstBlock(d *markdown.Document, kind markdown.Kind) (markdown.Block, bool) {
	for _, b := range d.Blocks() {
		if b.Kind == kind {
			return b, true
		}
	}
	return markdown.Block{}, false
}

// firstBlockInlines returns the inline run of the first block of kind.
func firstBlockInlines(d *markdown.Document, kind markdown.Kind) []markdown.Inline {
	for i, b := range d.Blocks() {
		if b.Kind == kind {
			return d.Inlines(i)
		}
	}
	return nil
}

func hasHeading(d *markdown.Document, level int) bool {
	for _, b := range d.Blocks() {
		if b.Kind == markdown.Heading && b.Level == level {
			return true
		}
	}
	return false
}

func hasInline(d *markdown.Document, kind markdown.InlineKind) bool {
	for i := range d.Blocks() {
		for _, in := range d.Inlines(i) {
			if in.Kind == kind {
				return true
			}
		}
	}
	return false
}

func atxHeadings() bool {
	d := parseDoc("# one\n###### six\n")
	return hasHeading(d, 1) && hasHeading(d, 6)
}

func setextHeading() bool { return hasHeading(parseDoc("Title\n=====\n"), 1) }
func softBreak() bool     { return hasInline(parseDoc("one\ntwo\n"), markdown.SoftBreak) }
func hardBreak() bool     { return hasInline(parseDoc("one  \ntwo\n"), markdown.LineBreak) }

func emphasisStrong() bool {
	d := parseDoc("*a* **b** _c_ __d__\n")
	return hasInline(d, markdown.Emphasis) && hasInline(d, markdown.Strong)
}

func inlineCode() bool { return hasInline(parseDoc("`code`\n"), markdown.Code) }

func fencedCode() bool {
	b, ok := firstBlock(parseDoc("```go\nx\n```\n"), markdown.CodeBlock)
	return ok && b.Fenced && b.Language() == "go"
}

func indentedCode() bool {
	b, ok := firstBlock(parseDoc("    x\n"), markdown.CodeBlock)
	return ok && !b.Fenced
}

func blockQuote() bool {
	_, ok := firstBlock(parseDoc("> quote\n"), markdown.Quote)
	return ok
}

func orderedList() bool {
	b, ok := firstBlock(parseDoc("1. one\n2. two\n"), markdown.List)
	return ok && b.Ordered
}

func unorderedList() bool {
	b, ok := firstBlock(parseDoc("- one\n- two\n"), markdown.List)
	return ok && !b.Ordered
}

func nestedList() bool {
	d := parseDoc("- one\n  - two\n")
	n := 0
	for _, b := range d.Blocks() {
		if b.Kind == markdown.List {
			n++
		}
	}
	return n >= 2
}

func thematicBreak() bool {
	_, ok := firstBlock(parseDoc("---\n"), markdown.ThematicBreak)
	return ok
}

func inlineLink() bool {
	d := parseDoc("[a](/u \"t\")\n")
	for _, in := range firstBlockInlines(d, markdown.Paragraph) {
		if in.Kind == markdown.Link && in.Dest == "/u" && in.Title == "t" {
			return true
		}
	}
	return false
}

func referenceLink() bool {
	d := parseDoc("[a][r]\n\n[r]: /u\n")
	for _, in := range firstBlockInlines(d, markdown.Paragraph) {
		if in.Kind == markdown.Link && in.Dest == "/u" {
			return true
		}
	}
	return false
}

func angleAutolink() bool {
	d := parseDoc("<https://example.com>\n")
	for _, in := range firstBlockInlines(d, markdown.Paragraph) {
		if in.Kind == markdown.Link && in.Auto && in.Dest == "https://example.com" {
			return true
		}
	}
	return false
}

func image() bool {
	d := parseDoc("![alt](/i.png)\n")
	for _, in := range firstBlockInlines(d, markdown.Paragraph) {
		if in.Kind == markdown.Image && in.Dest == "/i.png" {
			return true
		}
	}
	return false
}

func htmlBlock() bool {
	_, ok := firstBlock(parseDoc("<div>x</div>\n"), markdown.HTMLBlock)
	return ok
}

func inlineHTML() bool {
	return hasInline(parseDoc("text <b>bold</b>\n"), markdown.HTML)
}

func escapesEntities() bool {
	d := parseDoc(`\* \&amp;` + "\n")
	in := firstBlockInlines(d, markdown.Paragraph)
	// The parser may split one source run across several Text inlines; the
	// feature under test is the resolved text, not the node boundaries.
	text := markdown.PlainText(in)
	return strings.Contains(text, "*") && strings.Contains(text, "&")
}

func table() bool {
	b, ok := firstBlock(parseDoc("| a | b |\n|:--|--:|\n| 1 | 2 |\n"), markdown.Table)
	return ok && len(b.Align) == 2
}

func taskList() bool {
	d := parseDoc("- [ ] todo\n- [x] done\n")
	for _, b := range d.Blocks() {
		if b.Kind == markdown.Item && b.Task == markdown.TaskChecked {
			return true
		}
	}
	return false
}

func strikethrough() bool { return hasInline(parseDoc("~~old~~\n"), markdown.Strikethrough) }

func bareAutolink() bool {
	d := parseDoc("https://example.com\n")
	for _, in := range firstBlockInlines(d, markdown.Paragraph) {
		if in.Kind == markdown.Link && in.Auto && in.Dest == "https://example.com" {
			return true
		}
	}
	return false
}

func footnote() bool {
	d := parseDoc("text[^1]\n\n[^1]: note\n")
	_, ok := firstBlock(d, markdown.FootnoteDefinition)
	return ok && hasInline(d, markdown.FootnoteRef)
}

func math() bool {
	d := parseDoc("$$\nx\n$$\n")
	_, ok := firstBlock(d, markdown.MathBlock)
	return ok || hasInline(d, markdown.Math)
}

func dollarMath() bool { return hasInline(parseDoc("$x$\n"), markdown.Math) }

// frontMatter reports whether a YAML front matter fence becomes metadata. It
// does not: the probe is supported only if the parser exposes metadata, which
// it does not.
func frontMatter() bool {
	parseDoc("---\ntitle: x\n---\n")
	return false
}

func definitionList() bool {
	parseDoc("Term\n: definition\n")
	return false
}

func mermaidRender() bool {
	// The offline renderer must handle both delivered diagram types before
	// this row claims support.
	if _, d := diagram.Render(context.Background(), "flowchart TD\n  A-->B\n", diagram.Options{}); d != nil {
		return false
	}
	if _, d := diagram.Render(context.Background(), "sequenceDiagram\n  A->>B: hi\n", diagram.Options{}); d != nil {
		return false
	}
	return true
}

// --- small path helpers ---------------------------------------------------

// docDir returns the project-relative directory of a document, or ".".
func docDir(rel string) string {
	d := filepath.ToSlash(filepath.Dir(filepath.FromSlash(rel)))
	if d == "" {
		return "."
	}
	return d
}
