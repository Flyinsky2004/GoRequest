package docedit

import "testing"

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
