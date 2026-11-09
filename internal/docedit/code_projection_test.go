package docedit

import (
	"strings"
	"testing"
)

func TestM02CodeSourceRoundTrip(t *testing.T) {
	for _, src := range []string{"```\n```\n", "````go title=x  \r\n中文😀\r\n\r\n````` \t\r\n", "~~~js\nx\n~~~", "```go\nunfinished\n", "```\n", "```", "    one\n    two\n", "\tone\n\ttwo\n", "    ```go\n    literal\n", "  ````go attr=x\r\n  one\r\n    two\r\n  ````\r\n"} {
		b, p := projOf(t, src)
		if !p.Active() {
			t.Fatalf("code not projected: %q", src)
		}
		if got := b.CommitText(p.Commit(p.Text())); got != src {
			t.Fatalf("round trip %q => %q", src, got)
		}
		for off := 0; off <= len(p.Text()); off++ {
			if got := p.ProjectedOffset(p.SourceOffset(p.Text(), off)); got != off {
				t.Fatalf("offset %q %d => %d", src, off, got)
			}
		}
	}
}

func TestM02FenceCollision(t *testing.T) {
	for _, ch := range []string{"`", "~"} {
		for _, eol := range []string{"\n", "\r\n"} {
			fence := strings.Repeat(ch, 4)
			src := fence + "go attr=keep  " + eol + "old" + eol + fence + "  " + eol
			b, p := projOf(t, src)
			body := "中文😀\n" + strings.Repeat(ch, 6) + "\nlast\n"
			got := b.CommitText(p.Commit(body))
			want := strings.Repeat(ch, 7) + "go attr=keep  " + eol + strings.ReplaceAll(body, "\n", eol) + eol + strings.Repeat(ch, 7) + "  " + eol
			if got != want {
				t.Fatalf("collision %q => %q want %q", src, got, want)
			}
			blocks := Parse(got + eol + "after" + eol)
			if len(blocks) != 2 || NewProjection(blocks[0]).Text() != body {
				t.Fatalf("collision changed AST/body: %+v", blocks)
			}
			for _, safe := range []string{"````x", "    ````", "\t````", "~~~ x"} {
				if result := p.Commit(safe); !strings.HasPrefix(result, fence+"go") {
					t.Fatalf("unnecessary growth: %q", result)
				}
			}
		}
	}
}
