package core

import (
	"strings"
	"testing"
)

func newDocProject(t *testing.T) *Project {
	t.Helper()
	dir := t.TempDir()
	p, err := NewProject(dir, "Docs")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}
	return p
}

func writeDoc(t *testing.T, p *Project, rel, src string) {
	t.Helper()
	if err := p.Store.WriteAtomic(rel, []byte(src)); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func TestExtractHeadingsLevelsTextSlugAndLines(t *testing.T) {
	src := "# Title\n\nIntro\n\n## Section One\n\n```go\n# not a heading\n```\n\n### 子标题 空格\n\n## Section One\n\nSetext\n------\n"
	hs := ExtractHeadings(src)
	want := []struct {
		level int
		text  string
		slug  string
		id    string
		line  int
	}{
		{1, "Title", "title", "title", 1},
		{2, "Section One", "section-one", "section-one", 5},
		{3, "子标题 空格", "子标题-空格", "子标题-空格", 11},
		{2, "Section One", "section-one", "section-one-1", 13},
		{2, "Setext", "setext", "setext", 15},
	}
	if len(hs) != len(want) {
		t.Fatalf("got %d headings, want %d: %+v", len(hs), len(want), hs)
	}
	for i, w := range want {
		h := hs[i]
		if h.Level != w.level || h.Text != w.text || h.Slug != w.slug || h.ID != w.id || h.Line != w.line {
			t.Errorf("heading %d = %+v, want %+v", i, h, w)
		}
		if h.Offset < 0 || h.EndOffset <= h.Offset {
			t.Errorf("heading %d offsets invalid: %d..%d", i, h.Offset, h.EndOffset)
		}
	}
}

func TestHeadingAtLineAndSlugPunctuation(t *testing.T) {
	hs := ExtractHeadings("# One\n\n## Two\n")
	if h, ok := HeadingAtLine(hs, 1); !ok || h.Text != "One" {
		t.Fatalf("line 1 heading = %+v, %v", h, ok)
	}
	if h, ok := HeadingAtLine(hs, 2); !ok || h.Text != "One" {
		t.Fatalf("line 2 should still be under One, got %+v", h)
	}
	if h, ok := HeadingAtLine(hs, 3); !ok || h.Text != "Two" {
		t.Fatalf("line 3 = %+v", h)
	}
	cases := map[string]string{
		"Hello, World!":   "hello-world",
		"Use `go` now":    "use-go-now",
		"snake_case-name": "snake_case-name",
		"中文 标题？":          "中文-标题",
		"   ":             "",
	}
	for in, want := range cases {
		if got := GitHubHeadingSlug(in); got != want {
			t.Errorf("GitHubHeadingSlug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSearchDocTextCaseWordAndUnicode(t *testing.T) {
	src := "Alpha beta alpha\n中文 搜索\nfoo_bar foo-bar\n"
	cases := []struct {
		name  string
		opts  SearchOptions
		want  int
		lines []int
	}{
		{"case-insensitive", SearchOptions{Query: "alpha"}, 2, []int{1, 1}},
		{"case-sensitive", SearchOptions{Query: "Alpha", CaseSensitive: true}, 1, []int{1}},
		{"whole-word", SearchOptions{Query: "foo", WholeWord: true}, 1, []int{3}},
		{"chinese", SearchOptions{Query: "搜索"}, 1, []int{2}},
	}
	for _, c := range cases {
		got, truncated := SearchDocText("docs/a.md", src, c.opts)
		if truncated {
			t.Errorf("%s: unexpected truncation", c.name)
		}
		if len(got) != c.want {
			t.Errorf("%s: got %d matches, want %d: %+v", c.name, len(got), c.want, got)
			continue
		}
		for i, line := range c.lines {
			if got[i].Line != line {
				t.Errorf("%s: match %d line = %d, want %d", c.name, i, got[i].Line, line)
			}
		}
	}
	got, _ := SearchDocText("docs/a.md", src, SearchOptions{Query: "alpha"})
	if got[1].Column != 12 || got[1].ByteColumn != 11 {
		t.Fatalf("second alpha position = col %d byte %d", got[1].Column, got[1].ByteColumn)
	}
	if got[0].Snippet != "Alpha beta alpha" {
		t.Fatalf("snippet = %q", got[0].Snippet)
	}
}

func TestSearchDocTextLimitAndOverlap(t *testing.T) {
	got, truncated := SearchDocText("docs/a.md", "a a a a\n", SearchOptions{Query: "a", MaxResults: 2})
	if len(got) != 2 || !truncated {
		t.Fatalf("limit: matches=%d truncated=%v", len(got), truncated)
	}
	got, truncated = SearchDocText("docs/a.md", "aaaa\n", SearchOptions{Query: "aa"})
	if len(got) != 2 || truncated {
		t.Fatalf("non-overlapping matches: %d %v", len(got), truncated)
	}
	if matches, _ := SearchDocText("docs/a.md", "anything", SearchOptions{}); matches != nil {
		t.Fatal("empty query should match nothing")
	}
}

func TestSearchDocsUsesOverridesAndReportsUnreadable(t *testing.T) {
	p := newDocProject(t)
	writeDoc(t, p, "docs/a.md", "disk text\n")
	writeDoc(t, p, "docs/b.md", "second hit\n")
	p.Docs = []*DocEntry{{RelPath: "docs/a.md"}, {RelPath: "docs/b.md"}, {RelPath: "docs/gone.md"}}

	matches, diags, truncated := p.SearchDocs(SearchOptions{
		Query:     "hit",
		Overrides: map[string]string{"docs/a.md": "memory hit\n"},
	})
	if truncated {
		t.Fatal("unexpected truncation")
	}
	var paths []string
	for _, m := range matches {
		paths = append(paths, m.RelPath)
	}
	if strings.Join(paths, ",") != "docs/a.md,docs/b.md" {
		t.Fatalf("paths = %v", paths)
	}
	if len(diags) != 1 || diags[0].RelPath != "docs/gone.md" || diags[0].Kind != "search-read" {
		t.Fatalf("diagnostics = %+v", diags)
	}
}

func TestSearchDocsTruncationIsAccurate(t *testing.T) {
	p := newDocProject(t)
	writeDoc(t, p, "docs/a.md", "hit\n")
	writeDoc(t, p, "docs/b.md", "hit\n")
	p.Docs = []*DocEntry{{RelPath: "docs/a.md"}, {RelPath: "docs/b.md"}}

	matches, _, truncated := p.SearchDocs(SearchOptions{Query: "hit", MaxResults: 2})
	if len(matches) != 2 || truncated {
		t.Fatalf("exact limit: %d matches, truncated=%v", len(matches), truncated)
	}
	writeDoc(t, p, "docs/c.md", "hit\n")
	p.Docs = append(p.Docs, &DocEntry{RelPath: "docs/c.md"})
	matches, _, truncated = p.SearchDocs(SearchOptions{Query: "hit", MaxResults: 2})
	if len(matches) != 2 || !truncated {
		t.Fatalf("over limit: %d matches, truncated=%v", len(matches), truncated)
	}
}

func TestMarkdownCompatibilityMatrixMatchesParser(t *testing.T) {
	m := MarkdownCompatibility()
	want := map[string]bool{
		"ATX headings": true, "Setext headings": true, "Paragraphs and soft breaks": true,
		"Hard line breaks": true, "Emphasis and strong": true, "Inline code": true,
		"Fenced code with language": true, "Indented code": true, "Block quotes": true,
		"Ordered lists": true, "Unordered lists": true, "Nested lists": true,
		"Thematic breaks": true, "Inline links with titles": true, "Reference links": true,
		"Angle-bracket autolinks": true, "Images": true, "HTML blocks": true,
		"Inline HTML": true, "Backslash escapes and entities": true,
		"Tables": true, "Task lists": true, "Strikethrough": true, "Bare autolinks": true,
		"Footnotes": true, "Math": true, "Dollar math": false,
		"YAML front matter": false, "Definition lists": false, "Mermaid (offline subset)": true,
	}
	if len(m) != len(want) {
		t.Fatalf("matrix has %d rows, want %d", len(m), len(want))
	}
	for _, f := range m {
		w, ok := want[f.Name]
		if !ok {
			t.Errorf("unexpected matrix row %q", f.Name)
			continue
		}
		if f.Supported != w {
			t.Errorf("%s supported = %v, want %v", f.Name, f.Supported, w)
		}
		t.Logf("%-8s %-32s supported=%v", f.Group, f.Name, f.Supported)
	}
}
