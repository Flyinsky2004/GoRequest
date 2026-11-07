package docmention

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Trigger is one @ completion being typed: Start is the byte offset of the
// '@' and Query the text between it and the caret.
type Trigger struct {
	Start int
	Query string
}

// FindTrigger reports whether a mention is being typed at offset. The '@'
// must be the nearest non-query rune before offset, must start a mention
// (not an email, an escaped \@, a code span, a fenced block or a link
// destination), and everything after it up to offset must be query text.
func FindTrigger(src string, offset int) (Trigger, bool) {
	offset = clamp(offset, len(src))
	i := offset
	for i > 0 {
		r, size := utf8.DecodeLastRuneInString(src[:i])
		if !IsQueryRune(r) {
			break
		}
		i -= size
	}
	if i == 0 {
		return Trigger{}, false
	}
	r, size := utf8.DecodeLastRuneInString(src[:i])
	if r != '@' {
		return Trigger{}, false
	}
	at := i - size
	if !CanStart(src, at) {
		return Trigger{}, false
	}
	return Trigger{Start: at, Query: src[at+1 : offset]}, true
}

// CanStart reports whether the '@' at at may open a mention. src[at] must be
// the '@' itself; use CanStartAt when the character is about to be typed.
func CanStart(src string, at int) bool {
	if at < 0 || at >= len(src) || src[at] != '@' {
		return false
	}
	return CanStartAt(src, at)
}

// CanStartAt checks the context before at, where an '@' is typed or about to
// be inserted. It rejects email local parts, escaped \@, code and link
// destinations.
func CanStartAt(src string, at int) bool {
	if at < 0 || at > len(src) {
		return false
	}
	if InCodeOrLinkDest(src, at) {
		return false
	}
	if at == 0 {
		return true
	}
	r, _ := utf8.DecodeLastRuneInString(src[:at])
	if r == '\\' && backslashesBefore(src, at)%2 == 1 {
		return false
	}
	// An email's local part: user@example.com must stay plain text.
	return !isEmailLocalRune(r)
}

// IsQueryRune reports whether r may continue a mention query. Whitespace and
// sentence punctuation end it; the characters a folder path, an ID or an
// address contains continue it.
func IsQueryRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '-', '.', '/', ':', '?', '&', '=', '%', '#', '~', '+', '{', '}':
		return true
	}
	return false
}

// InCodeOrLinkDest reports whether offset is inside a fenced code block, an
// inline code span, a link or image destination, or an autolink. The scan is
// deliberately conservative: a mention that would be ambiguous is not
// offered, and plain text is unaffected.
func InCodeOrLinkDest(src string, offset int) bool {
	offset = clamp(offset, len(src))
	lineStart := strings.LastIndexByte(src[:offset], '\n') + 1
	if inFence(src, lineStart) {
		return true
	}
	line := src[lineStart:offset]
	if strings.Count(line, "`")%2 == 1 {
		return true
	}
	if i := strings.LastIndex(line, "]("); i >= 0 && !strings.Contains(line[i+2:], ")") {
		return true
	}
	if i := strings.LastIndexByte(line, '<'); i >= 0 && !strings.Contains(line[i:], ">") {
		return true
	}
	return false
}

// inFence reports whether the line starting at lineStart is inside a fenced
// code block opened earlier. Fences are CommonMark's: three or more
// backticks or tildes, closed by the same character with at least as many.
func inFence(src string, lineStart int) bool {
	in := false
	var fence rune
	fenceLen := 0
	for pos := 0; pos < lineStart && pos < len(src); {
		end := strings.IndexByte(src[pos:], '\n')
		var line string
		if end < 0 {
			line = src[pos:]
			pos = len(src)
		} else {
			line = src[pos : pos+end]
			pos += end + 1
		}
		char, n, ok := fenceMarker(strings.TrimLeft(line, " \t"))
		if !ok {
			continue
		}
		if !in {
			in, fence, fenceLen = true, char, n
			continue
		}
		if char == fence && n >= fenceLen {
			in = false
		}
	}
	return in
}

func fenceMarker(line string) (rune, int, bool) {
	if line == "" {
		return 0, 0, false
	}
	char := rune(line[0])
	if char != '`' && char != '~' {
		return 0, 0, false
	}
	n := 0
	for n < len(line) && rune(line[n]) == char {
		n++
	}
	if n < 3 {
		return 0, 0, false
	}
	return char, n, true
}

func backslashesBefore(src string, at int) int {
	n := 0
	for i := at; i > 0; {
		r, size := utf8.DecodeLastRuneInString(src[:i])
		if r != '\\' {
			break
		}
		n++
		i -= size
	}
	return n
}

func isEmailLocalRune(r rune) bool {
	if unicode.IsLetter(r) || unicode.IsDigit(r) {
		return true
	}
	switch r {
	case '_', '.', '%', '+', '-':
		return true
	}
	return false
}

func clamp(v, n int) int {
	if v < 0 {
		return 0
	}
	if v > n {
		return n
	}
	return v
}
