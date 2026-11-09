package docedit

import "unicode/utf8"

// MinimalChange returns one UTF-8-safe source replacement. It keeps unchanged
// prefixes and suffixes outside the buffer transaction as well as preserving
// their bytes in the resulting Markdown.
func MinimalChange(before, after string) (start, end int, text string) {
	for start < len(before) && start < len(after) && before[start] == after[start] {
		start++
	}
	for start > 0 && ((start < len(before) && !utf8.RuneStart(before[start])) || (start < len(after) && !utf8.RuneStart(after[start]))) {
		start--
	}
	suffix := 0
	for suffix < len(before)-start && suffix < len(after)-start && before[len(before)-1-suffix] == after[len(after)-1-suffix] {
		suffix++
	}
	for suffix > 0 && ((len(before)-suffix < len(before) && !utf8.RuneStart(before[len(before)-suffix])) || (len(after)-suffix < len(after) && !utf8.RuneStart(after[len(after)-suffix]))) {
		suffix--
	}
	return start, len(before) - suffix, after[start : len(after)-suffix]
}
