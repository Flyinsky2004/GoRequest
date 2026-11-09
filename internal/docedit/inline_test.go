package docedit

import (
	"strings"
	"testing"
)

func TestInlineViewSourceRanges(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"a `中文😀` and **bold** [name](https://x)", "a 中文😀 and bold name"},
		{"``a ` b``", "a ` b"}, {"`a\nb`", "a b"}, {"`\r\na\r\n`", "a"}, {"` code `", "code"},
		{"unclosed `a **b", "unclosed `a **b"},
		{"\\`literal\\`", "\\`literal\\`"},
		{"***both***", "both"},
	} {
		v := NewInlineView(tc.src)
		if v.Text != tc.want {
			t.Errorf("%q => %q, want %q; runs=%+v", tc.src, v.Text, tc.want, v.Runs)
		}
		for _, r := range v.Runs {
			for off := r.Start; off < r.End; off++ {
				if got := v.VisibleOffset(v.SourceOffset(off)); got != off {
					t.Fatalf("offset %d => %d", off, got)
				}
			}
		}
	}
}
func TestInlineEditPreservesDestinationsAndDelimiters(t *testing.T) {
	v := NewInlineView("before **中文** [label](<https://x> \"title\") after\r\n")
	s, _ := v.Replace(len("before "), len("before 中文"), "新😀")
	if want := "before **新😀** [label](<https://x> \"title\") after\r\n"; s != want {
		t.Fatalf("%q != %q", s, want)
	}
}

func TestM03BreakSemanticsAndOffsets(t *testing.T) {
	for _, tc := range []struct{ src, want string }{
		{"中文😀\n`query` next\nend", "中文😀 query next end"},
		{"a\r\nb", "a b"}, {"a  \nb", "a\nb"}, {"a\\\nb", "a\nb"},
		{"a  \r\nb", "a\nb"}, {"a\n\nb", "a\n\nb"}, {"`a\nb`", "a b"},
		{"**a\nb**", "a b"}, {"`a  `\nb", "a   b"}, {"a\n  b", "a b"},
	} {
		v := NewInlineView(tc.src)
		if v.Text != tc.want {
			t.Errorf("%q => %q want %q (%+v)", tc.src, v.Text, tc.want, v.Runs)
		}
		for _, r := range v.Runs {
			if v.VisibleOffset(r.SourceStart) != r.Start || v.VisibleOffset(r.SourceEnd) != r.End {
				t.Fatalf("bad mapping %+v", r)
			}
		}
		if v.Source != tc.src {
			t.Fatal("projection rewrote source")
		}
	}
	v := NewInlineView("中文😀\r\nnext")
	start := len("中文😀")
	got, caret := v.Replace(start, start+1, "")
	if got != "中文😀next" || caret != start {
		t.Fatalf("delete break %q %d", got, caret)
	}
	got, _ = v.Replace(start, start+1, " /@ ")
	if got != "中文😀 /@ next" {
		t.Fatal(got)
	}
}

func TestM03BreakReplacementPreservesAdjacentDelimiters(t *testing.T) {
	for _, src := range []string{"`first`\n`second`", "**first**\n**second**", "[first](https://x)\r\n`second`"} {
		v := NewInlineView(src)
		at := len("first")
		got, _ := v.Replace(at, at+1, " ")
		want := strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", " "), "\n", " ")
		if got != want {
			t.Fatalf("%q => %q want %q", src, got, want)
		}
	}
}
