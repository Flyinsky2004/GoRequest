package docedit

import (
	"strings"
	"testing"

	"gorequest/internal/markdown"
)

func TestParseTopLevelBlocksKeepMarkers(t *testing.T) {
	src := "# Title\n\nParagraph **bold**.\n\n- one\n- two\n\n> quoted\n\n| A | B |\n| --- | --- |\n| c | d |\n\n```go\nvar a = 1\n```\n\n---\n"
	blocks := Parse(src)
	var kinds []markdown.Kind
	for _, b := range blocks {
		kinds = append(kinds, b.Kind)
	}
	want := []markdown.Kind{markdown.Heading, markdown.Paragraph, markdown.List, markdown.Quote, markdown.Table, markdown.CodeBlock, markdown.ThematicBreak}
	if len(kinds) != len(want) {
		t.Fatalf("blocks = %v, want %v", kinds, want)
	}
	for i := range want {
		if kinds[i] != want[i] {
			t.Fatalf("block %d = %v, want %v (all %v)", i, kinds[i], want[i], kinds)
		}
	}
	for _, b := range blocks {
		if b.Start < 0 || b.End > len(src) || b.Start >= b.End {
			t.Fatalf("bad range %d..%d for %q", b.Start, b.End, b.Source)
		}
		if src[b.Start:b.End] != b.Source {
			t.Fatalf("source mismatch at %d..%d", b.Start, b.End)
		}
	}
	// A list's range includes its bullets; a quote's includes its marker; a
	// table's includes its pipes.
	if !strings.HasPrefix(blocks[2].Source, "- one") {
		t.Fatalf("list source = %q", blocks[2].Source)
	}
	if !strings.HasPrefix(blocks[3].Source, "> quoted") {
		t.Fatalf("quote source = %q", blocks[3].Source)
	}
	if !strings.HasPrefix(blocks[4].Source, "| A | B |") {
		t.Fatalf("table source = %q", blocks[4].Source)
	}
	if blocks[5].Source != "```go\nvar a = 1\n```\n" {
		t.Fatalf("code source = %q", blocks[5].Source)
	}
	if blocks[6].Source != "---\n" {
		t.Fatalf("rule source = %q", blocks[6].Source)
	}
}

func TestParseNestedContainersAreNotDuplicated(t *testing.T) {
	src := "- item one\n  - nested\n- item two\n\n> quote\n>\n> second\n"
	blocks := Parse(src)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %d %+v, want list and quote", len(blocks), blocks)
	}
	if blocks[0].Kind != markdown.List || !strings.Contains(blocks[0].Source, "nested") {
		t.Fatalf("list = %+v", blocks[0])
	}
	if blocks[1].Kind != markdown.Quote || !strings.Contains(blocks[1].Source, "second") {
		t.Fatalf("quote = %+v", blocks[1])
	}
}

func TestEditTextAndCommitText(t *testing.T) {
	b := Block{Source: "# Title\n"}
	if got := b.EditText(); got != "# Title" {
		t.Fatalf("edit text = %q", got)
	}
	if got := b.CommitText("# Title"); got != "# Title\n" {
		t.Fatalf("commit = %q", got)
	}
	if got := b.CommitText("# Title\n"); got != "# Title\n\n" {
		t.Fatalf("typed newline should split: %q", got)
	}
}

func TestBlockAtAndNeighbours(t *testing.T) {
	src := "one\n\ntwo\n\nthree\n"
	blocks := Parse(src)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %d", len(blocks))
	}
	if b, ok := BlockAt(blocks, 0); !ok || b.Source != "one\n" {
		t.Fatalf("block at 0 = %+v %v", b, ok)
	}
	if _, ok := BlockAt(blocks, 4); ok {
		t.Fatal("blank line should have no block")
	}
	if b, ok := PreviousBlock(blocks, blocks[1].Start); !ok || b.Source != "one\n" {
		t.Fatalf("previous = %+v %v", b, ok)
	}
	if b, ok := NextBlock(blocks, blocks[1].Start); !ok || b.Source != "two\n" {
		t.Fatalf("next = %+v %v", b, ok)
	}
}

func TestSlashQuery(t *testing.T) {
	cases := []struct {
		text  string
		query string
		ok    bool
	}{
		{"/", "", true},
		{"/h1", "h1", true},
		{"/h1 Hello", "h1 Hello", true},
		{"- /h1", "h1", true},
		{"1. /table", "table", true},
		{"  /code go", "code go", true},
		{"/usr/local", "", false},
		{"https://example.com", "", false},
		{"hello /h1", "", false},
		{"/h1\nmore", "", false},
		{"- /h1\n- /h2", "", false},
	}
	for _, c := range cases {
		got, ok := SlashQuery(c.text)
		if got != c.query || ok != c.ok {
			t.Errorf("SlashQuery(%q) = %q,%v want %q,%v", c.text, got, ok, c.query, c.ok)
		}
	}
}

func TestFilterSlashByChineseAndEnglish(t *testing.T) {
	if got := FilterSlash("标题 2"); len(got) != 1 || got[0].ID != "h2" {
		t.Fatalf("Chinese filter = %+v", got)
	}
	if got := FilterSlash("head 2"); len(got) != 1 || got[0].ID != "h2" {
		t.Fatalf("English filter = %+v", got)
	}
	if got := FilterSlash("table"); len(got) != 1 || got[0].ID != "table" {
		t.Fatalf("table filter = %+v", got)
	}
	if got := FilterSlash("zzz"); len(got) != 0 {
		t.Fatalf("no-match filter = %+v", got)
	}
}

func TestApplySlash(t *testing.T) {
	byID := map[string]SlashCommand{}
	for _, c := range SlashCommands() {
		byID[c.ID] = c
	}
	if got := ApplySlash(byID["h2"], "Hello"); got.Text != "## Hello" || got.SelStart != 3 || got.SelEnd != 8 {
		t.Fatalf("heading = %+v", got)
	}
	if got := ApplySlash(byID["code"], "go"); got.Text != "```go\n\n```" || got.SelStart != 6 {
		t.Fatalf("code = %+v", got)
	}
	if got := ApplySlash(byID["table"], ""); !strings.HasPrefix(got.Text, "| Column | Column |") || strings.Count(got.Text, "\n| Cell | Cell |") != 2 {
		t.Fatalf("table = %q", got.Text)
	}
	if got := SlashRest("/h2 Hello world", byID["h2"]); got != "Hello world" {
		t.Fatalf("rest = %q", got)
	}
}

func TestParseSetextHeadingIncludesUnderline(t *testing.T) {
	src := "Title\n=====\n\nbody\n"
	blocks := Parse(src)
	if len(blocks) != 2 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[0].Kind != markdown.Heading || blocks[0].Level != 1 {
		t.Fatalf("heading = %+v", blocks[0])
	}
	if blocks[0].Source != "Title\n=====\n" {
		t.Fatalf("setext source = %q", blocks[0].Source)
	}
}

func TestParseUnclosedFenceRunsToEOF(t *testing.T) {
	src := "```go\ncode\n"
	blocks := Parse(src)
	if len(blocks) != 1 || blocks[0].Source != src {
		t.Fatalf("blocks = %+v", blocks)
	}
}

func TestParseFootnoteAndMathAreSourceFallbacks(t *testing.T) {
	src := "text[^1]\n\n[^1]: note\n\n$$\nx = 1\n$$\n"
	blocks := Parse(src)
	if len(blocks) != 3 {
		t.Fatalf("blocks = %+v", blocks)
	}
	if blocks[1].Kind != markdown.FootnoteDefinition || !strings.Contains(blocks[1].Source, "note") {
		t.Fatalf("footnote = %+v", blocks[1])
	}
	if blocks[2].Kind != markdown.MathBlock || blocks[2].Source != "$$\nx = 1\n$$\n" {
		t.Fatalf("math = %+v", blocks[2])
	}
}

func TestSlashRestStripsCommandNames(t *testing.T) {
	byID := map[string]SlashCommand{}
	for _, c := range SlashCommands() {
		byID[c.ID] = c
	}
	cases := []struct {
		cmd   string
		query string
		want  string
	}{
		{"h2", "h2 Hello", "Hello"},
		{"h2", "标题 2", ""},
		{"h2", "heading 2", ""},
		{"h2", "title Hello", "Hello"},
		{"h2", "h2", ""},
		{"table", "table", ""},
		{"table", "table My grid", "My grid"},
	}
	for _, c := range cases {
		if got := SlashRest(c.query, byID[c.cmd]); got != c.want {
			t.Errorf("SlashRest(%q, %s) = %q, want %q", c.query, c.cmd, got, c.want)
		}
	}
}
