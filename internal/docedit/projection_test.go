package docedit

import (
	"strings"
	"testing"

	"gorequest/internal/markdown"
)

func projOf(t *testing.T, src string) (Block, Projection) {
	t.Helper()
	bs := Parse(src)
	if len(bs) == 0 {
		t.Fatalf("no blocks in %q", src)
	}
	return bs[0], NewProjection(bs[0])
}

func TestProjectionHidesBlockMarkersAndRoundTrips(t *testing.T) {
	cases := []struct {
		src, text string
	}{
		{"# Title\n", "Title"},
		{"###### Deep 6\n", "Deep 6"},
		{"### 标题 内容\n", "标题 内容"},
		{"## Closed ##\n", "Closed"},
		{"##\n", ""},
		{"## \n", ""},
		{"## ###\n", ""},
		{`## C \#` + "\n", "C #"},
		{"## Tail\r\n", "Tail"},
		{"## NoNewline", "NoNewline"},
		{"Setext\n======\n", "Setext"},
		{"SetextCRLF\r\n======\r\n", "SetextCRLF"},
		{"Sub\n---\n", "Sub"},
		{"> quote a\n> quote b\n", "quote a\nquote b"},
		{"> para a\n>\n> para b\n", "para a\n\npara b"},
		{">> level two a\n>> level two b\n", "level two a\nlevel two b"},
		{"> > spaced two a\n> > spaced two b\n", "spaced two a\nspaced two b"},
		{"> crlf a\r\n> crlf b\r\n", "crlf a\ncrlf b"},
		{"- a\n- b\n", "a\nb"},
		{"- crlf 1\r\n- crlf 2\r\n", "crlf 1\ncrlf 2"},
		{"1. one\n2. two\n", "one\ntwo"},
		{"3) three\n4) four", "three\nfour"},
		{"- [x] done\n- [ ] todo\n", "done\ntodo"},
		{"- [ ]\n- [x] ok\n", "\nok"},
	}
	for _, tc := range cases {
		b, p := projOf(t, tc.src)
		if !p.Active() {
			t.Errorf("%q should project", tc.src)
			continue
		}
		if p.Text() != tc.text {
			t.Errorf("%q text = %q, want %q", tc.src, p.Text(), tc.text)
			continue
		}
		if strings.Contains(p.Text(), "\r") {
			t.Errorf("%q projected text leaked CR: %q", tc.src, p.Text())
		}
		if got := b.CommitText(p.Commit(p.Text())); got != tc.src {
			t.Errorf("%q full round-trip = %q, want %q", tc.src, got, tc.src)
		}
		// Every caret position survives text -> source -> text.
		for off := 0; off <= len(p.Text()); off++ {
			s := p.SourceOffset(p.Text(), off)
			if back := NewProjection(b).ProjectedOffset(s); back != off {
				t.Errorf("%q caret %d -> src %d -> %d", tc.src, off, s, back)
			}
		}
	}
}

func TestProjectionKeepsHeadingSemanticsWhenEdited(t *testing.T) {
	b, p := projOf(t, "### Old\n")
	if got := b.CommitText(p.Commit("新标题")); got != "### 新标题\n" {
		t.Fatalf("got %q", got)
	}
	if got := p.Commit(""); got != "### " {
		t.Fatalf("empty heading got %q", got)
	}
	// Bare empty ATX heading "##\n" gains a separating space when text is added.
	_, bare := projOf(t, "##\n")
	if got := bare.Commit("Added"); got != "## Added" {
		t.Fatalf("bare ATX commit got %q", got)
	}
	// Leading Enter pushes the heading down without losing its '#' level.
	if got := p.Commit("\nOld"); got != "\n### Old" {
		t.Fatalf("leading Enter got %q, want %q", got, "\n### Old")
	}
	if off := p.SourceOffset("\nOld", 1); off != len("\n### ") {
		t.Fatalf("leading Enter caret = %d, want %d", off, len("\n### "))
	}
	// Middle / trailing Enter ends the heading and opens a paragraph below.
	if got := p.Commit("Head\nbody"); got != "### Head\n\nbody" {
		t.Fatalf("split got %q", got)
	}
	if off := p.SourceOffset("Head\nbody", 7); off != len("### Head\n\nbo") {
		t.Fatalf("split offset %d", off)
	}
	if got := p.Commit("Old\n"); got != "### Old\n\n" {
		t.Fatalf("trailing Enter got %q", got)
	}
	// A typed trailing '#' stays literal text, and typing '#1' drops the escape.
	if got := p.Commit("C #"); got != `### C \#` {
		t.Fatalf("escape got %q", got)
	}
	_, hashProj := projOf(t, "### \\#\n")
	if hashProj.Text() != "#" {
		t.Fatalf("escaped lone hash text = %q", hashProj.Text())
	}
	if got := hashProj.Commit("#1"); got != "### #1" {
		t.Fatalf("continuing '#1' got %q", got)
	}

	// Setext headings keep their underline, CRLF and leading/trailing Enter.
	sb, sp := projOf(t, "Title\r\n=====\r\n")
	if got := sb.CommitText(sp.Commit("Next")); got != "Next\r\n=====\r\n" {
		t.Fatalf("setext CRLF got %q", got)
	}
	if got := sp.Commit("\nTitle"); got != "\r\nTitle\r\n=====" {
		t.Fatalf("setext leading Enter got %q", got)
	}
	if got := sp.Commit("Title\nBody"); got != "Title\r\n=====\r\n\r\nBody" {
		t.Fatalf("setext split got %q", got)
	}
	if got := sp.Commit(""); got != "# " {
		t.Fatalf("empty setext got %q", got)
	}
}

func TestProjectionListAndQuoteEditsPreserveLevelsNumbersAndCRLF(t *testing.T) {
	// Single and nested uniform quote levels.
	_, q2 := projOf(t, ">> deep a\n>> deep b\n")
	if got := q2.Commit("deep a\ndeep mid\ndeep b"); got != ">> deep a\n>> deep mid\n>> deep b" {
		t.Fatalf("nested quote got %q", got)
	}
	_, qBlank := projOf(t, "> para 1\n>\n> para 2\n")
	if got := qBlank.Commit("para 1\nfilled\npara 2"); got != "> para 1\n> filled\n> para 2" {
		t.Fatalf("quote blank line fill got %q", got)
	}

	// Ordered list: text edits keep exact markers; structural edits renumber
	// consecutively from the starting number.
	_, ord := projOf(t, "3. alpha\n4. beta\n5. gamma\n")
	if got := ord.Commit("alpha!\nbeta\ngamma"); got != "3. alpha!\n4. beta\n5. gamma" {
		t.Fatalf("ordered text edit got %q", got)
	}
	if got := ord.Commit("alpha\nmid\nbeta\ngamma"); got != "3. alpha\n4. mid\n5. beta\n6. gamma" {
		t.Fatalf("ordered middle insert got %q", got)
	}
	if got := ord.Commit("\nalpha\nbeta\ngamma"); got != "3. \n4. alpha\n5. beta\n6. gamma" {
		t.Fatalf("ordered leading Enter got %q", got)
	}
	if got := ord.Commit("merged\ngamma"); got != "3. merged\n4. gamma" {
		t.Fatalf("ordered cross-line delete got %q", got)
	}

	// Intentional all-ones ordered list stays all-ones on insert.
	_, ones := projOf(t, "1. a\n1. b\n")
	if got := ones.Commit("a\nb\nc"); got != "1. a\n1. b\n1. c" {
		t.Fatalf("all-ones ordered list got %q", got)
	}

	// Task list: leading Enter and trailing Enter start unchecked while
	// keeping existing checked items.
	_, task := projOf(t, "- [x] done\n- [ ] todo\n")
	if got := task.Commit("todo"); got != "- [ ] todo" {
		t.Fatalf("task delete got %q", got)
	}
	if got := task.Commit("\ndone\ntodo"); got != "- [ ] \n- [x] done\n- [ ] todo" {
		t.Fatalf("task leading Enter got %q", got)
	}
	if got := task.Commit("done\ntodo\nmore"); got != "- [x] done\n- [ ] todo\n- [ ] more" {
		t.Fatalf("task append got %q", got)
	}

	// CRLF + no-final-newline list round-trip on edit.
	lb, lp := projOf(t, "1. first\r\n2. second")
	if got := lb.CommitText(lp.Commit("first\nmid\nsecond")); got != "1. first\r\n2. mid\r\n3. second" {
		t.Fatalf("CRLF no-final-newline list got %q", got)
	}
}

func TestProjectionFallsBackToSource(t *testing.T) {
	for _, src := range []string{
		"```go\n# not a heading\n```\n",
		"| a | b |\n| - | - |\n| 1 | 2 |\n",
		"<div>\n# x\n</div>\n",
		"---\n",
		"- a\n  - nested\n",
		"- a\n  continuation\n",
		"- a\n\n- loose\n",
		"> lazy\ncontinuation\n",
		"> outer\n>> mixed inner\n",
		"> # Heading inside quote\n",
		"> Title\n> ---\n",
		"Multi\nline\n=====\n",
		"Plain **bold** [link](https://example.com) text\n",
	} {
		b, p := projOf(t, src)
		if p.Active() {
			t.Errorf("%q must stay source", src)
		}
		if got := b.CommitText(p.Commit(p.Text())); got != src {
			t.Errorf("%q identity round-trip = %q, want %q", src, got, src)
		}
	}
}

func TestFrontMatterIsNeverMisreadAsSetextHeading(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		fm       string
		bodyKind markdown.Kind
		bodySrc  string
	}{
		{
			name:     "standard yaml",
			src:      "---\ntitle: API\ntags:\n  - auth\n---\n\n# Head\n",
			fm:       "---\ntitle: API\ntags:\n  - auth\n---\n",
			bodyKind: markdown.Heading,
			bodySrc:  "# Head\n",
		},
		{
			name:     "dots closer without blank line",
			src:      "---\ntitle: Dots\n...\nBody right after\n",
			fm:       "---\ntitle: Dots\n...\n",
			bodyKind: markdown.Paragraph,
			bodySrc:  "Body right after\n",
		},
		{
			name:     "crlf front matter",
			src:      "---\r\ntitle: Win\r\n---\r\n\r\n## Sub\r\n",
			fm:       "---\r\ntitle: Win\r\n---\r\n",
			bodyKind: markdown.Heading,
			bodySrc:  "## Sub\r\n",
		},
		{
			name:     "empty front matter",
			src:      "---\n---\n\nPara\n",
			fm:       "---\n---\n",
			bodyKind: markdown.Paragraph,
			bodySrc:  "Para\n",
		},
		{
			name:     "utf8 bom",
			src:      "\uFEFF---\ntitle: BOM\n---\n\nPara\n",
			fm:       "\uFEFF---\ntitle: BOM\n---\n",
			bodyKind: markdown.Paragraph,
			bodySrc:  "Para\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			bs := Parse(tc.src)
			if len(bs) != 2 {
				t.Fatalf("blocks = %#v", bs)
			}
			if !bs[0].FrontMatter || bs[0].Kind != markdown.HTMLBlock || bs[0].Source != tc.fm {
				t.Fatalf("front matter block = %#v, want %q", bs[0], tc.fm)
			}
			if NewProjection(bs[0]).Active() {
				t.Fatal("front matter must never be projected")
			}
			if bs[1].Kind != tc.bodyKind || bs[1].Source != tc.bodySrc {
				t.Fatalf("body block = %#v, want %v %q", bs[1], tc.bodyKind, tc.bodySrc)
			}
		})
	}

	// Non-metadata thematic breaks must NOT be swallowed as front matter.
	for _, nonFM := range []string{
		"---\ntitle: unclosed\n",
		"---\n\ntitle: blank-after-opener\n---\n",
		"---\nJust a regular prose paragraph between two rules.\n\n---\n",
	} {
		if got := FrontMatterEnd(nonFM); got != 0 {
			t.Errorf("FrontMatterEnd(%q) = %d, want 0", nonFM, got)
		}
	}
}
