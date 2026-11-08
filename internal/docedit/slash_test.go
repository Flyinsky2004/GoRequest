package docedit

import "testing"

func TestSlashQueryAtJudgesTheCaretsLine(t *testing.T) {
	cases := []struct {
		name  string
		text  string
		caret int
		query string
		ok    bool
	}{
		{"first line", "/h2 Hello", 9, "h2 Hello", true},
		{"later line", "first\n/h2 Hello", len("first\n/h2 Hello"), "h2 Hello", true},
		{"later line after slash", "first\n/h2", len("first\n/"), "h2", true},
		{"caret before slash", "first\n/h2", len("first\n"), "", false},
		{"caret on slash", "first\n/h2", len("first\n"), "", false},
		{"middle of line", "a/b", 3, "", false},
		{"path", "/usr/local", len("/usr/local"), "", false},
		{"url", "https://example.com/a", len("https://example.com/a"), "", false},
		{"blank line after query", "first\n/h2\nsecond", len("first\n/h2"), "h2", true},
		{"crlf line", "first\r\n/h2\r\n", len("first\r\n/h2"), "h2", true},
		{"list marker", "- /h1", len("- /h1"), "h1", true},
		{"empty", "", 0, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := SlashQueryAt(c.text, c.caret)
			if got != c.query || ok != c.ok {
				t.Fatalf("SlashQueryAt(%q, %d) = %q,%v want %q,%v", c.text, c.caret, got, ok, c.query, c.ok)
			}
		})
	}
}

func TestSlashQueryLineAcceptsOneLineOnly(t *testing.T) {
	if q, ok := SlashQueryLine("/h3 Title"); !ok || q != "h3 Title" {
		t.Fatalf("line = %q,%v", q, ok)
	}
	if _, ok := SlashQueryLine("/h3\nmore"); ok {
		t.Fatal("a multi-line string is not one logical line")
	}
}
