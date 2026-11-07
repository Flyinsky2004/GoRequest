// Package docgen derives API documentation from GoRequest project requests.
//
// It is deliberately read-only: a request definition is consumed exactly as
// stored, references resolve against the in-memory project snapshot, and no
// step of resolution, preview or export sends a request, resolves variables
// against a live environment or reads the local secret provider.
//
// Reference syntax (v1, constrained and round-trippable):
//
//	[打开请求](gorequest://request/users.me)                       … inline link
//	<!-- gorequest:request id="users.me" view="parameters" -->    … card directive
package docgen

import (
	"fmt"
	"sort"
	"strings"

	"gorequest/internal/markdown"
)

// RefLinkPrefix is the URI form of an inline request reference.
const RefLinkPrefix = "gorequest://request/"

// MaxRequestIDLen bounds the ID accepted inside a reference. Project request
// IDs are slugs of collections and names, far below the bound.
const MaxRequestIDLen = 128

// Directive prefix/suffix delimit a card directive comment.
const (
	DirectivePrefix = "<!-- gorequest:request "
	DirectiveSuffix = " -->"
	// BeginMarker opens a derived (generated) block; docgen emits it
	// directly below a card directive.
	BeginMarker = "<!-- gorequest:begin -->"
	// EndMarker closes a derived block. Content between BeginMarker and the
	// next code-region-aware EndMarker is owned by docgen and replaced on
	// every derivation; both markers are inert comments to plain readers,
	// and a re-derivation expects them in this order only.
	EndMarker = "<!-- gorequest:end -->"
)

// View tokens select which card sections a directive derives. Unknown tokens
// are rejected so a renamed section can never silently drop content from
// published documents.
const (
	ViewCard       = "card"
	ViewParameters = "parameters"
	ViewResponses  = "responses"
)

// AllViews is the full view token set, in canonical order.
var AllViews = []string{ViewCard, ViewParameters, ViewResponses}

// ValidRequestID reports whether id may appear inside a reference. The
// charset matches what project tooling generates (core slugs and OpenAPI
// sanitized operation IDs): ASCII letters, digits, dot, underscore, hyphen.
// IDs outside the charset remain usable inside GoRequest but are reported
// unreachable from documents instead of being half-parsed.
func ValidRequestID(id string) bool {
	if id == "" || len(id) > MaxRequestIDLen || strings.Contains(id, "..") {
		return false
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '.' || c == '_' || c == '-':
		default:
			return false
		}
	}
	c := id[0]
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
}

// ParseRefLink parses a gorequest://request/<id> destination. ok=false means
// dest is not a request reference. A gorequest:// destination with a
// different shape is a malformed reference, reported via IsValidRefShape.
func ParseRefLink(dest string) (id string, ok bool) {
	if !strings.HasPrefix(dest, RefLinkPrefix) {
		return "", false
	}
	rest := dest[len(RefLinkPrefix):]
	if rest == "" || strings.ContainsAny(rest, " \t\r\n/?#%\\") {
		return "", false
	}
	if !ValidRequestID(rest) {
		return "", false
	}
	return rest, true
}

// IsValidRefShape reports whether dest is a gorequest:// reference shape,
// independent of grammar details. Used to tell "not our reference at all"
// (handled elsewhere) from "our reference that cannot be used" (a document
// error).
func IsValidRefShape(dest string) bool {
	return strings.HasPrefix(dest, "gorequest://")
}

// RefKind tells what a scanned occurrence is.
type RefKind string

const (
	RefKindLinkRef   RefKind = "link"
	RefKindDirective RefKind = "directive"
)

// RefOccurrence is one request reference found in a document.
type RefOccurrence struct {
	Kind RefKind
	ID   string
	// Views are the parsed directive view tokens (directives only).
	Views []string
	// Line and Column are 1-based source coordinates.
	Line   int
	Column int
	// Dest is the raw destination (links) or directive line.
	Dest string
	// Malformed marks a recognized-but-unusable reference; it is reported,
	// never expanded.
	Malformed bool
}

// ParseViews parses and validates the view attribute of a directive. Empty
// means the default (all sections).
func ParseViews(raw string) ([]string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	seen := map[string]bool{}
	var out []string
	for _, tok := range strings.Split(raw, ",") {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			return nil, fmt.Errorf("empty view token")
		}
		switch tok {
		case ViewCard, ViewParameters, ViewResponses:
		default:
			return nil, fmt.Errorf("unknown view %q (supported: %s)", tok, strings.Join(AllViews, ", "))
		}
		if !seen[tok] {
			seen[tok] = true
			out = append(out, tok)
		}
	}
	return out, nil
}

// FormatViews serializes view tokens back to attribute form.
func FormatViews(views []string) string {
	return strings.Join(views, ",")
}

// FormatDirective builds the canonical single-line directive.
func FormatDirective(id string, views []string) string {
	d := DirectivePrefix + `id="` + id + `"`
	if len(views) > 0 {
		d += ` view="` + FormatViews(views) + `"`
	}
	return d + DirectiveSuffix
}

// FormatRefLink builds the inline link reference form; name is the request
// name used as label when known.
func FormatRefLink(id, name string) string {
	label := id
	if name != "" {
		label = name
	}
	return "[" + MarkdownCell(label) + "](" + RefLinkPrefix + id + ")"
}

// ParseDirectiveLine parses one source line as a card directive. It returns
// (nil, false, nil) when the line is not a directive comment at all and
// (views, false, err) when the line has the reference shape but violates the
// grammar. The grammar is deliberately strict: the whole trimmed line must
// be the comment; attributes appear in order (id first, optional view
// second); values are double-quoted without escapes; no further attributes.
func ParseDirectiveLine(line string) ([]string, bool, error) {
	trimmed := strings.TrimSpace(line)
	if !strings.HasPrefix(trimmed, DirectivePrefix) || trimmed == DirectivePrefix {
		// Other gorequest comments (the markers, future forms) are not card
		// directives; only the complete gorequest:request prefix enters
		// grammar validation.
		return nil, false, nil
	}
	rest, ok := strings.CutSuffix(trimmed, DirectiveSuffix)
	if !ok {
		return nil, false, fmt.Errorf("directive line is missing the %s terminator", DirectiveSuffix)
	}
	rest = rest[len(DirectivePrefix):]
	const idAttr = `id="`
	if !strings.HasPrefix(rest, idAttr) {
		return nil, false, fmt.Errorf("directive must start with the id attribute")
	}
	id, rem, ok := cutQuoted(rest[len(idAttr):])
	if !ok {
		return nil, false, fmt.Errorf("directive id attribute is missing its closing quote")
	}
	if id == "" {
		return nil, false, fmt.Errorf("directive id attribute is empty")
	}
	views := ""
	rem = strings.TrimSpace(rem)
	if strings.HasPrefix(rem, `view="`) {
		v, tail, ok := cutQuoted(strings.TrimPrefix(rem, `view="`))
		if !ok {
			return nil, false, fmt.Errorf("directive view attribute is missing its closing quote")
		}
		views = v
		rem = strings.TrimSpace(tail)
	}
	if rem != "" {
		return nil, false, fmt.Errorf("directive has unexpected trailing content %q", rem)
	}
	parsed, err := ParseViews(views)
	if err != nil {
		return nil, false, err
	}
	if !ValidRequestID(id) {
		return nil, false, fmt.Errorf("directive id %q is not a referenceable request ID", id)
	}
	return parsed, true, nil
}

func cutQuoted(s string) (value, rest string, ok bool) {
	end := strings.IndexByte(s, '"')
	if end < 0 {
		return "", "", false
	}
	return s[:end], s[end+1:], true
}

// LineColAt converts a byte offset in src into 1-based line and byte column.
func LineColAt(src string, off int) (line, col int) {
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	line, col = 1, 1
	for i := 0; i < off; i++ {
		if src[i] == '\n' {
			line++
			col = 1
			continue
		}
		col++
	}
	return line, col
}

// ScanDocument finds every docgen reference in src.
//
// Directives are found by a code-region-aware line scan because the grammar
// pins a directive to a whole trimmed line; inline links are found through
// the project's existing Markdown parser (internal/markdown) so links inside
// code spans, fences, or not links at all are never misreported. Code-region
// detection is shared with the expander via fenceTracker: a line inside a
// fenced block or indented by four or more spaces is ignored — the safe
// direction of every ambiguity.
func ScanDocument(src string) []RefOccurrence {
	var out []RefOccurrence
	seen := map[int]bool{} // byte offsets of directive lines, already handled

	// Directive lines: line scan, skipping code regions.
	var fence fenceTracker
	lineNo := 0
	for line := range strings.Lines(src) {
		lineNo++
		trimmedRight := strings.TrimRight(line, "\r\n")
		if fence.withLine(trimmedRight) {
			continue
		}
		offset := segmentOffset(src, line)
		if views, ok, err := ParseDirectiveLine(trimmedRight); ok || err != nil {
			seen[offset] = true
			occ := RefOccurrence{Kind: RefKindDirective, Line: lineNo, Column: 1 + leadingSpaces(trimmedRight), Dest: trimmedRight}
			if err != nil {
				occ.Malformed = true
				occ.ID = malformedDirectiveID(trimmedRight)
			} else {
				occ.ID = directiveID(trimmedRight)
				if occ.ID == "" {
					occ.Malformed = true
				} else {
					occ.Views = views
				}
			}
			out = append(out, occ)
		}
	}

	// Inline link references through the parser.
	d := markdown.Parse(src)
	for i := range d.Blocks() {
		for _, inl := range d.Inlines(i) {
			if inl.Kind != markdown.Link && inl.Kind != markdown.Image {
				continue
			}
			if !IsValidRefShape(inl.Dest) {
				continue
			}
			line, col := LineColAt(src, inl.Start)
			if seen[inl.Start] {
				continue
			}
			id, ok := ParseRefLink(inl.Dest)
			occ := RefOccurrence{Kind: RefKindLinkRef, Line: line, Column: col, Dest: inl.Dest}
			if !ok {
				occ.Malformed = true
				occ.ID = strings.TrimPrefix(inl.Dest, RefLinkPrefix)
			} else {
				occ.ID = id
			}
			out = append(out, occ)
		}
	}
	sortOccurrences(out)
	return out
}

// malformedDirectiveID extracts a best-effort ID for diagnostics.
func malformedDirectiveID(line string) string {
	rest, ok := strings.CutPrefix(strings.TrimSpace(line), DirectivePrefix)
	if !ok {
		return ""
	}
	if i := strings.IndexByte(rest, '"'); i >= 0 {
		if j := strings.IndexByte(rest[i+1:], '"'); j >= 0 {
			return rest[i+1 : i+1+j]
		}
	}
	return ""
}

func directiveID(line string) string {
	rest, _ := strings.CutPrefix(strings.TrimSpace(line), DirectivePrefix)
	id, _, ok := cutQuoted(strings.TrimPrefix(rest, `id="`))
	if !ok {
		return ""
	}
	return id
}

// segmentOffset returns the byte offset of line (a subslice produced by
// strings.Lines) within src.
func segmentOffset(src, line string) int {
	n := len(src) - len(line)
	if n < 0 {
		if n = strings.Index(src, line); n < 0 {
			return 0
		}
	}
	return n
}

func sortOccurrences(occs []RefOccurrence) {
	sort.SliceStable(occs, func(i, j int) bool {
		if occs[i].Line == occs[j].Line {
			return occs[i].Column < occs[j].Column
		}
		return occs[i].Line < occs[j].Line
	})
}
