package desktop

import (
	"testing"

	"gorequest/internal/markdown"
)

// The canvas gets table blocks from internal/markdown, whose Table block
// does not carry a reliable source range: End stops at the last cell's
// text, and a table that follows a paragraph without a blank line keeps
// the paragraph's start. docTableBlock is the wiring helper that recovers
// the complete block source. These tests pin that contract.
func TestDocTableBlockFindsCompleteSource(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "after a blank line",
			src:  "intro\n\n| a | b |\n| --- | --- |\n| c | d |\n\nafter\n",
			want: "| a | b |\n| --- | --- |\n| c | d |\n",
		},
		{
			name: "after a paragraph without a blank line",
			src:  "intro\n| a | b |\n| --- | --- |\n| c | d |\n",
			want: "| a | b |\n| --- | --- |\n| c | d |\n",
		},
		{
			name: "at the end without a trailing line ending",
			src:  "| a | b |\n| --- | --- |\n| c | d |",
			want: "| a | b |\n| --- | --- |\n| c | d |",
		},
		{
			name: "empty last cell",
			src:  "| a | b |\n| --- | --- |\n|  |  |\n\nafter\n",
			want: "| a | b |\n| --- | --- |\n|  |  |\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := markdown.Parse(tc.src)
			found := false
			for _, b := range d.Blocks() {
				if b.Kind != markdown.Table {
					continue
				}
				found = true
				start, end, ok := docTableBlock(tc.src, b.Start)
				if !ok {
					t.Fatalf("docTableBlock did not find the table at %d", b.Start)
				}
				if got := tc.src[start:end]; got != tc.want {
					t.Fatalf("block source = %q, want %q", got, tc.want)
				}
			}
			if !found {
				t.Fatal("the parser reported no table block")
			}
		})
	}
}

func TestDocTableBlockRejectsNonTable(t *testing.T) {
	if _, _, ok := docTableBlock("intro\n\ntext\n", 0); ok {
		t.Fatal("docTableBlock found a table in a document without one")
	}
}
