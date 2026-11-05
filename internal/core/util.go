package core

import (
	"regexp"
	"sort"
	"strings"
)

// sortStrings sorts a string slice in place.
func sortStrings(s []string) { sort.Strings(s) }

var slugUnsafe = regexp.MustCompile(`[^a-z0-9]+`)

// Slug converts a display name into a lower-case, dash-separated identifier
// suitable for a request/project id prefix.
func Slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = slugUnsafe.ReplaceAllString(s, "-")
	s = strings.Trim(s, "-")
	if s == "" {
		s = "item"
	}
	return s
}
