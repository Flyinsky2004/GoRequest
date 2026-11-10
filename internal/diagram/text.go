package diagram

import (
	"strings"
	"unicode"
)

// Text metrics are font-independent on purpose: the native canvas and the
// static SVG export share one scene, so both must agree on how wide a label
// is. The factors are deliberately generous for CJK and emoji so shaped
// glyphs fit the boxes the scene reserved.
const (
	cjkAdvance       = 1.10 // em per rune for wide glyphs
	latinAdvance     = 0.62
	tabAdvance       = 4.0
	lineHeightFactor = 1.25
)

// isWideRune reports whether r occupies a full em in typical CJK fonts:
// Han, kana, hangul, CJK punctuation, fullwidth forms and emoji.
func isWideRune(r rune) bool {
	switch {
	case r >= 0x1100 && r <= 0x115F, // Hangul Jamo
		r >= 0x2E80 && r <= 0xA4CF, // CJK radicals .. Yi
		r >= 0xAC00 && r <= 0xD7A3, // Hangul syllables
		r >= 0xF900 && r <= 0xFAFF, // CJK compatibility ideographs
		r >= 0xFE30 && r <= 0xFE4F, // CJK compatibility forms
		r >= 0xFF00 && r <= 0xFF60, // fullwidth forms
		r >= 0xFFE0 && r <= 0xFFE6,
		r >= 0x1F000 && r <= 0x1FAFF, // emoji and symbols
		r >= 0x20000 && r <= 0x3FFFD:
		return true
	}
	return false
}

// TextWidth returns the deterministic advance width of one line of text at
// the given font size.
func TextWidth(s string, fontSize float32) float32 {
	var w float32
	for _, r := range s {
		switch {
		case r == '\t':
			w += tabAdvance * latinAdvance * fontSize
		case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
			// Combining marks add no advance.
		case isWideRune(r):
			w += cjkAdvance * fontSize
		default:
			w += latinAdvance * fontSize
		}
	}
	return w
}

// LineHeight returns the line height used by the deterministic layout.
func LineHeight(fontSize float32) float32 { return fontSize * lineHeightFactor }

// stripControl removes C0/C1 control characters except tab and newline and
// turns bidi overrides into spaces, so a label cannot reorder the rest of
// the drawing or smuggle terminal control sequences into exported text.
func stripControl(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			b.WriteRune(r)
		case r < 0x20 || (r >= 0x7F && r <= 0x9F):
			// Drop.
		case r == 0x202A || r == 0x202B || r == 0x202C || r == 0x202D ||
			r == 0x202E || r == 0x2066 || r == 0x2067 || r == 0x2068 ||
			r == 0x2069:
			b.WriteRune(' ')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// splitLabel lines breaks a label on newlines and the <br>/<br/> tags that
// Mermaid documents for multiline text. The caller has already rejected any
// other HTML.
func splitLabel(label string) []string {
	label = strings.ReplaceAll(label, "\r\n", "\n")
	label = strings.ReplaceAll(label, "\r", "\n")
	replacer := strings.NewReplacer("<br/>", "\n", "<br />", "\n", "<br>", "\n", "<BR/>", "\n", "<BR>", "\n")
	label = replacer.Replace(label)
	parts := strings.Split(label, "\n")
	for i := range parts {
		parts[i] = strings.TrimSpace(parts[i])
	}
	return parts
}

// countRunes counts code points, the unit of the label budget.
func countRunes(s string) int { return len([]rune(s)) }

// wrapLine greedily wraps one line at maxWidth, breaking between runes. CJK
// text may break anywhere; Latin text prefers the last space so words stay
// whole when they fit.
func wrapLine(line string, maxWidth, fontSize float32) []string {
	if line == "" {
		return []string{""}
	}
	if maxWidth <= 0 || TextWidth(line, fontSize) <= maxWidth {
		return []string{line}
	}
	var out []string
	runes := []rune(line)
	start := 0
	for start < len(runes) {
		end := start
		w := float32(0)
		lastSpace := -1
		for end < len(runes) {
			rw := runeWidth(runes[end], fontSize)
			if w+rw > maxWidth && end > start {
				break
			}
			if runes[end] == ' ' {
				lastSpace = end
			}
			w += rw
			end++
		}
		breakAt := end
		if end < len(runes) && lastSpace > start {
			breakAt = lastSpace + 1
		}
		out = append(out, strings.TrimRight(string(runes[start:breakAt]), " "))
		if breakAt <= start {
			breakAt = start + 1
		}
		start = breakAt
	}
	return out
}

func runeWidth(r rune, fontSize float32) float32 {
	switch {
	case r == '\t':
		return tabAdvance * latinAdvance * fontSize
	case unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r):
		return 0
	case isWideRune(r):
		return cjkAdvance * fontSize
	}
	return latinAdvance * fontSize
}

// wrapLabel splits and wraps a label into the lines the scene draws.
func wrapLabel(label string, maxWidth, fontSize float32, maxLines int) []string {
	var out []string
	for _, line := range splitLabel(label) {
		out = append(out, wrapLine(line, maxWidth, fontSize)...)
		if maxLines > 0 && len(out) > maxLines {
			return out[:maxLines+1]
		}
	}
	return out
}

// labelWidth returns the widest line width.
func labelWidth(lines []string, fontSize float32) float32 {
	var w float32
	for _, line := range lines {
		if lw := TextWidth(line, fontSize); lw > w {
			w = lw
		}
	}
	return w
}

// decodeEntities decodes the numeric character references Mermaid documents
// for sequence text (#9829;, #x2665;) and leaves every other form literal.
func decodeEntities(s string) string {
	if !strings.ContainsRune(s, '#') {
		return s
	}
	var b strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); i++ {
		if runes[i] != '#' {
			b.WriteRune(runes[i])
			continue
		}
		j := i + 1
		base := 10
		if j < len(runes) && (runes[j] == 'x' || runes[j] == 'X') {
			base = 16
			j++
		}
		start := j
		var v int64
		for j < len(runes) {
			d := digit(runes[j], base)
			if d < 0 {
				break
			}
			v = v*int64(base) + int64(d)
			if v > 0x10FFFF {
				break
			}
			j++
		}
		if j > start && j < len(runes) && runes[j] == ';' && v > 0 && v <= 0x10FFFF {
			b.WriteRune(rune(v))
			i = j
			continue
		}
		b.WriteRune('#')
	}
	return b.String()
}

func digit(r rune, base int) int {
	switch {
	case r >= '0' && r <= '9':
		return int(r - '0')
	case base == 16 && r >= 'a' && r <= 'f':
		return int(r-'a') + 10
	case base == 16 && r >= 'A' && r <= 'F':
		return int(r-'A') + 10
	}
	return -1
}

// hasHTMLTag reports whether s contains an HTML tag other than the <br>
// forms splitLabel accepts. Labels are plain text; anything else is refused
// rather than rendered or silently dropped.
func hasHTMLTag(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] != '<' {
			continue
		}
		rest := strings.ToLower(s[i:])
		if strings.HasPrefix(rest, "<br>") || strings.HasPrefix(rest, "<br/>") ||
			strings.HasPrefix(rest, "<br />") {
			continue
		}
		return true
	}
	return false
}
