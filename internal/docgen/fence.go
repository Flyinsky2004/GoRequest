package docgen

import "strings"

// fenceTracker implements the shared code-region rule of directives and end
// markers: a reference or a marker inside a fenced code block (or a line
// indented by four or more spaces, treated as indented code) is never
// recognized and never consumed. Every docgen scanner derives the rule from
// this one struct so line scans cannot drift apart.
type fenceTracker struct {
	open bool
	ch   byte
	n    int
}

// withLine updates the tracker for one raw line and reports whether the line
// is inside a code region and must be ignored for reference scanning.
func (f *fenceTracker) withLine(line string) bool {
	trimmedRight := strings.TrimRight(line, "\r\n")
	if f.open {
		if closesFence(trimmedRight, f.ch, f.n) {
			f.open = false
		}
		return true
	}
	if n, ch, ok := opensFence(trimmedRight); ok {
		f.open, f.ch, f.n = true, ch, n
		return true
	}
	if leadingSpaces(trimmedRight) >= 4 {
		// Approximation of indented code: inside a list item an indented
		// continuation is content, but treating it as code only fails
		// safely (a reference there is not expanded and not reported).
		return true
	}
	return false
}

// closesFence reports whether the line closes the currently open fence.
func closesFence(line string, ch byte, n int) bool {
	t := strings.TrimSpace(line)
	count := 0
	for count < len(t) && t[count] == ch {
		count++
	}
	return count >= n && strings.TrimSpace(t[count:]) == ""
}

// opensFence reports whether the line opens a fenced code block.
func opensFence(line string) (length int, ch byte, ok bool) {
	t := strings.TrimLeft(line, " ")
	if len(t) < 3 {
		return 0, 0, false
	}
	c := t[0]
	if c != '`' && c != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(t) && t[n] == c {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	return n, c, true
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && (s[n] == ' ' || s[n] == '\t') {
		n++
	}
	return n
}
