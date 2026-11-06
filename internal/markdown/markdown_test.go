package markdown

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

func headingText(d *Document, i int) string {
	return strings.TrimSpace(PlainText(d.Inlines(i)))
}

func collect(d *Document, kind Kind) []Block {
	var out []Block
	for _, b := range d.Blocks() {
		if b.Kind == kind {
			out = append(out, b)
		}
	}
	return out
}

func inlinesOfKind(d *Document, kind InlineKind) []Inline {
	var out []Inline
	for i := range d.Blocks() {
		for _, in := range d.Inlines(i) {
			if in.Kind == kind {
				out = append(out, in)
			}
		}
	}
	return out
}

func TestHeadingsAcrossContainers(t *testing.T) {
	src := "# One\n\n```md\n# fenced\n```\n\n> ## Quoted\n\n- ### Listed\n\nSetext\n======\n\n# 中文 标题\n"
	d := Parse(src)

	var got []struct {
		level int
		text  string
	}
	for i, b := range d.Blocks() {
		if b.Kind == Heading {
			got = append(got, struct {
				level int
				text  string
			}{b.Level, headingText(d, i)})
		}
	}
	want := []struct {
		level int
		text  string
	}{
		{1, "One"},
		{2, "Quoted"},
		{3, "Listed"},
		{1, "Setext"},
		{1, "中文 标题"},
	}
	if len(got) != len(want) {
		t.Fatalf("headings = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("heading %d = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestHeadingPlainText(t *testing.T) {
	src := "## `code` *em* **strong** [link](/u) ![alt](/i.png) <b>x</b>\n"
	d := Parse(src)
	heads := collect(d, Heading)
	if len(heads) != 1 {
		t.Fatalf("headings = %+v", heads)
	}
	for i, b := range d.Blocks() {
		if b.Kind != Heading {
			continue
		}
		got := headingText(d, i)
		want := "code em strong link alt <b>x</b>"
		if got != want {
			t.Fatalf("plain text = %q, want %q", got, want)
		}
	}
}

func TestInlineLinksImagesAutolinksAndCode(t *testing.T) {
	src := "[a](/u \"t\") [r][ref] <https://e.com> https://bare.example ![alt](/i.png) `c` *em* **st** ~~del~~ <b>x</b>\n\n[ref]: /ref\n"
	d := Parse(src)

	var links []Inline
	for i := range d.Blocks() {
		for _, in := range d.Inlines(i) {
			if in.Kind == Link {
				links = append(links, in)
			}
		}
	}
	if len(links) != 4 {
		t.Fatalf("links = %+v", links)
	}
	wantDest := []string{"/u", "/ref", "https://e.com", "https://bare.example"}
	wantAuto := []bool{false, false, true, true}
	for i := range links {
		if links[i].Dest != wantDest[i] || links[i].Auto != wantAuto[i] {
			t.Errorf("link %d = %+v, want dest %q auto %v", i, links[i], wantDest[i], wantAuto[i])
		}
		if links[i].Start < 0 || links[i].End <= links[i].Start {
			t.Errorf("link %d offsets = %d..%d", i, links[i].Start, links[i].End)
		}
	}
	if links[0].Title != "t" {
		t.Errorf("inline link title = %q", links[0].Title)
	}

	imgs := inlinesOfKind(d, Image)
	if len(imgs) != 1 || imgs[0].Dest != "/i.png" {
		t.Errorf("images = %+v", imgs)
	}
	for kind, name := range map[InlineKind]string{
		Code: "code", Emphasis: "emphasis", Strong: "strong",
		Strikethrough: "strikethrough", HTML: "html",
	} {
		if got := inlinesOfKind(d, kind); len(got) == 0 {
			t.Errorf("missing %s inline", name)
		}
	}
}

func TestEscapesAndEntitiesResolve(t *testing.T) {
	d := Parse("# a \\* \\&amp; &#42; &amp;\n")
	heads := collect(d, Heading)
	if len(heads) != 1 {
		t.Fatalf("headings = %+v", heads)
	}
	for i, b := range d.Blocks() {
		if b.Kind == Heading {
			if got, want := headingText(d, i), "a * &amp; * &"; got != want {
				t.Errorf("plain text = %q, want %q", got, want)
			}
		}
	}

	cases := map[string]string{
		"[a](/u&#95;x)\n":    "/u_x",
		"[a](</v\\_y>)\n":    "/v_y",
		"[a](/w&amp;z)\n":    "/w&z",
		"[a](/plain-path)\n": "/plain-path",
	}
	for src, want := range cases {
		d := Parse(src)
		links := inlinesOfKind(d, Link)
		if len(links) != 1 || links[0].Dest != want {
			t.Errorf("%q: links = %+v, want dest %q", src, links, want)
		}
	}
}

func TestGFMBlocks(t *testing.T) {
	src := "| a | b | c |\n|:--|--:|:-:|\n| 1 | 2 | 3 |\n\n" +
		"- [ ] todo\n- [x] done\n\n" +
		"1. one\n2. two\n\n" +
		"- one\n  - two\n\n" +
		"```go\nx\n```\n\n    code\n\n---\n"
	d := Parse(src)

	tables := collect(d, Table)
	if len(tables) != 1 {
		t.Fatalf("tables = %+v", tables)
	}
	wantAlign := []Alignment{AlignLeft, AlignRight, AlignCenter}
	if len(tables[0].Align) != len(wantAlign) {
		t.Fatalf("align = %+v", tables[0].Align)
	}
	for i := range wantAlign {
		if tables[0].Align[i] != wantAlign[i] {
			t.Errorf("align %d = %v, want %v", i, tables[0].Align[i], wantAlign[i])
		}
	}

	var tasks []TaskState
	lists := 0
	for _, b := range d.Blocks() {
		if b.Kind == List {
			lists++
		}
		if b.Kind == Item && b.Task != TaskNone {
			tasks = append(tasks, b.Task)
		}
	}
	if fmt.Sprint(tasks) != fmt.Sprint([]TaskState{TaskUnchecked, TaskChecked}) {
		t.Errorf("tasks = %v", tasks)
	}
	if lists < 3 {
		t.Errorf("lists = %d, want ordered, unordered and nested", lists)
	}
	ordered := 0
	for _, b := range collect(d, List) {
		if b.Ordered {
			ordered++
		}
	}
	if ordered != 1 {
		t.Errorf("ordered lists = %d", ordered)
	}

	codes := collect(d, CodeBlock)
	if len(codes) != 2 {
		t.Fatalf("code blocks = %+v", codes)
	}
	if !codes[0].Fenced || codes[0].Language() != "go" {
		t.Errorf("fenced code = %+v", codes[0])
	}
	if codes[1].Fenced || codes[1].Language() != "" {
		t.Errorf("indented code = %+v", codes[1])
	}
	if len(collect(d, ThematicBreak)) != 1 {
		t.Errorf("thematic breaks = %+v", collect(d, ThematicBreak))
	}
}

func TestFootnotes(t *testing.T) {
	d := Parse("text[^note]\n\n[^note]: def\n")
	if _, ok := firstOfKind(d, FootnoteDefinition); !ok {
		t.Fatal("missing footnote definition block")
	}
	refs := inlinesOfKind(d, FootnoteRef)
	if len(refs) != 1 || refs[0].Text != "note" {
		t.Fatalf("footnote refs = %+v", refs)
	}
	if refs[0].End <= refs[0].Start {
		t.Errorf("footnote ref offsets = %d..%d", refs[0].Start, refs[0].End)
	}
}

func TestMathBlocksAndInlines(t *testing.T) {
	src := "$$\nx\n$$\n\ntext \\(y\\) and \\[z\\] and $$w$$\n\n# not math\n\n$single$\n"
	d := Parse(src)
	if _, ok := firstOfKind(d, MathBlock); !ok {
		t.Fatal("missing math block")
	}
	maths := inlinesOfKind(d, Math)
	if len(maths) != 3 {
		t.Fatalf("math inlines = %+v", maths)
	}
	for i, want := range []string{"y", "z", "w"} {
		if maths[i].Text != want {
			t.Errorf("math %d = %q, want %q", i, maths[i].Text, want)
		}
	}
	// Single-dollar math stays text and the heading after the block is parsed.
	if len(collect(d, Heading)) != 1 {
		t.Errorf("headings = %+v", collect(d, Heading))
	}

	// A heading inside a $$ block is source, not a heading.
	d = Parse("# real\n\n$$\n# not heading\n$$\n")
	heads := collect(d, Heading)
	if len(heads) != 1 {
		t.Fatalf("headings in math = %+v", heads)
	}
}

func TestBreaks(t *testing.T) {
	d := Parse("one\ntwo\n\nthree  \nfour\n")
	if len(inlinesOfKind(d, SoftBreak)) != 1 {
		t.Errorf("soft breaks = %+v", inlinesOfKind(d, SoftBreak))
	}
	if len(inlinesOfKind(d, LineBreak)) != 1 {
		t.Errorf("hard breaks = %+v", inlinesOfKind(d, LineBreak))
	}
}

func TestEscapesEntitiesAndLanguageBoundaries(t *testing.T) {
	cases := map[string]string{
		`a\!b`:      "a!b",
		`a\;b`:      "a;b",
		`a\{b`:      "a{b",
		`a\x`:       `a\x`,
		`&#x2A;`:    "*",
		`&#42;`:     "*",
		`&#0;`:      "�",
		`&amp;`:     "&",
		`&unknown;`: "&unknown;",
	}
	for raw, want := range cases {
		if got := resolveText([]byte(raw)); got != want {
			t.Errorf("resolveText(%q) = %q, want %q", raw, got, want)
		}
	}
	if got := (Block{Info: "go extra"}).Language(); got != "go" {
		t.Errorf("Language(go extra) = %q", got)
	}
	if got := (Block{Info: "  "}).Language(); got != "" {
		t.Errorf("Language(spaces) = %q", got)
	}
}

func TestFootnoteLabelGuards(t *testing.T) {
	for src, want := range map[string]string{
		"[^note]": "note",
		"[^":      "0",
		"note]":   "0",
		"":        "0",
	} {
		if got := footnoteLabel([]byte(src), 0); got != want {
			t.Errorf("footnoteLabel(%q) = %q, want %q", src, got, want)
		}
	}
	if got := footnoteLabel([]byte("x[^1]"), 10); got != "10" {
		t.Errorf("out-of-range label = %q", got)
	}
}

func TestOffsetsStayInsideSource(t *testing.T) {
	src := "# Title\n\n[gone](missing.md)\n\n```mermaid\ngraph TD\n```\n\n<div>x</div>\n"
	d := Parse(src)
	for i, b := range d.Blocks() {
		if b.Start < 0 || b.End < b.Start || b.End > len(src) {
			t.Errorf("block %d (%v) offsets = %d..%d", i, b.Kind, b.Start, b.End)
		}
		for _, in := range d.Inlines(i) {
			if in.Start < 0 || in.End < in.Start || in.End > len(src) {
				t.Errorf("block %d inline %v offsets = %d..%d", i, in.Kind, in.Start, in.End)
			}
		}
	}
	// The code block starts at its opening fence, so diagnostics report the
	// mermaid line rather than the first content line.
	codes := collect(d, CodeBlock)
	if len(codes) != 1 || !strings.HasPrefix(src[codes[0].Start:], "```mermaid") {
		t.Errorf("code block start = %+v", codes)
	}
}

func TestMalformedInputIsText(t *testing.T) {
	for _, src := range []string{
		"", "#", "# \n", "####### seven\n", "```\nunclosed", "$$\nunclosed", "\x00\x01", "[x](", "![x](",
		"Term\r\n: definition\r\n", "- [ ]\r\n", "| a |\r\n|---|\r\n| b |\r\n",
		"# 中文 空格\r\n\r\n[链接](其他%20文档.md)\r\n",
	} {
		d := Parse(src)
		for i, b := range d.Blocks() {
			if b.Start < 0 || b.End < b.Start || b.Start > len(src) {
				t.Errorf("%q: block %d offsets = %d..%d", src, i, b.Start, b.End)
			}
		}
	}
}

func TestParseConcurrent(t *testing.T) {
	src := "# 标题\n\n- [x] done\n\n| a | b |\n|---|---|\n\n$$\nx\n$$\n"
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 20; j++ {
				d := Parse(src)
				if len(collect(d, Heading)) != 1 {
					t.Errorf("concurrent parse lost heading")
					return
				}
			}
		}()
	}
	wg.Wait()
}

func firstOfKind(d *Document, kind Kind) (Block, bool) {
	for _, b := range d.Blocks() {
		if b.Kind == kind {
			return b, true
		}
	}
	return Block{}, false
}
