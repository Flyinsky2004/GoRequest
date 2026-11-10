package diagram

import (
	"strings"
	"testing"
)

func TestTextWidthCJKIsWiderThanLatin(t *testing.T) {
	latin := TextWidth("abcd", 14)
	cjk := TextWidth("中文字符", 14)
	if cjk <= latin {
		t.Fatalf("CJK %.1f should be wider than Latin %.1f", cjk, latin)
	}
	if got := TextWidth("", 14); got != 0 {
		t.Fatalf("empty width %v", got)
	}
}

func TestWrapLineBreaksCJKAtWidth(t *testing.T) {
	lines := wrapLine(strings.Repeat("中文测试", 20), 100, 14)
	if len(lines) < 3 {
		t.Fatalf("expected wrapping, got %d lines", len(lines))
	}
	for _, line := range lines {
		if TextWidth(line, 14) > 100 {
			t.Fatalf("line %q exceeds 100: %.1f", line, TextWidth(line, 14))
		}
	}
}

func TestWrapLinePrefersSpace(t *testing.T) {
	lines := wrapLine("alpha beta gamma delta", 90, 14)
	if len(lines) < 2 {
		t.Fatalf("expected wrapping, got %+v", lines)
	}
	for _, line := range lines {
		if strings.HasPrefix(line, " ") || strings.HasSuffix(line, " ") {
			t.Fatalf("ragged space: %q", line)
		}
	}
}

func TestSplitLabelMultilineForms(t *testing.T) {
	got := splitLabel("a\nb<br>c<br/>d")
	want := []string{"a", "b", "c", "d"}
	if len(got) != len(want) {
		t.Fatalf("split: %+v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("split[%d]=%q want %q", i, got[i], want[i])
		}
	}
}

func TestDecodeEntities(t *testing.T) {
	cases := map[string]string{
		"I #9829; you":     "I ♥ you",
		"#x2665;":          "♥",
		"#35;":             "#",
		"#bad; and #;":     "#bad; and #;",
		"no entities here": "no entities here",
		"#1114112;":        "#1114112;",
	}
	for in, want := range cases {
		if got := decodeEntities(in); got != want {
			t.Fatalf("decode(%q)=%q want %q", in, got, want)
		}
	}
}

func TestStripControlAndBidi(t *testing.T) {
	if got := stripControl("a\x00b\u202Ecd\x07"); got != "ab cd" {
		t.Fatalf("stripControl=%q", got)
	}
	if strings.ContainsRune(stripControl("x\u202Ey"), '\u202E') {
		t.Fatalf("bidi override survived")
	}
}

func TestHasHTMLTagAllowsOnlyBR(t *testing.T) {
	if hasHTMLTag("a<br>b") || hasHTMLTag("a<br/>b") || hasHTMLTag("plain") {
		t.Fatalf("br must be allowed")
	}
	for _, s := range []string{"<b>x</b>", "<script>", "<img src=x>", "a <br x>"} {
		if !hasHTMLTag(s) {
			t.Fatalf("%q should be rejected", s)
		}
	}
}
