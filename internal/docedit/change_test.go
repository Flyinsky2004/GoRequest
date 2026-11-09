package docedit

import (
	"testing"
	"unicode/utf8"
)

func TestMinimalChangeKeepsUTF8AndUntouchedSyntax(t *testing.T) {
	for _, pair := range [][2]string{{"**中文😀** [x](url)\r\n", "**中新文😁** [x](url)\r\n"}, {"abc", "abc"}, {"😀a", "😁a"}, {"abc", ""}, {"", "中文"}} {
		a, b, s := MinimalChange(pair[0], pair[1])
		if !utf8.ValidString(pair[0][:a]) || !utf8.ValidString(s) || !utf8.ValidString(pair[0][b:]) {
			t.Fatal("split a rune")
		}
		if got := pair[0][:a] + s + pair[0][b:]; got != pair[1] {
			t.Fatalf("got %q", got)
		}
	}
}
