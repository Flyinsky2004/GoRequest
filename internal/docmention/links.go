package docmention

import (
	"strings"

	"gorequest/internal/core"
	"gorequest/internal/docgen"
)

// LinkRef is one gorequest://request/ link found in a document, with the
// source span needed to repair it without touching its label.
type LinkRef struct {
	// ID is the request ID the link names, best effort for a malformed one.
	ID string
	// Dest is the destination exactly as written.
	Dest string
	// Label is the raw Markdown text between the brackets, the snapshot the
	// author sees.
	Label string
	// Start and End bound the whole link; DestStart and DestEnd bound the
	// destination alone.
	Start, DestStart, DestEnd, End int
	// Malformed marks a gorequest:// destination the reference grammar
	// cannot parse.
	Malformed bool
	// Found reports that the ID resolves in the owner project.
	Found bool
}

// ScanRequestLinks finds every gorequest://request/ link in src and resolves
// it against the project snapshot. Code spans and fenced blocks are excluded
// by docgen's scanner, so a sample link in a code block is not reported as
// stale. The scan is read-only and sends nothing.
func ScanRequestLinks(p *core.Project, src string) []LinkRef {
	occs := docgen.ScanDocument(src)
	out := make([]LinkRef, 0, len(occs))
	for _, occ := range occs {
		if occ.Kind != docgen.RefKindLinkRef {
			continue
		}
		ref, ok := linkSpan(src, occ)
		if !ok {
			continue
		}
		ref.Malformed = occ.Malformed
		ref.Found = !occ.Malformed && p != nil && p.Requests[ref.ID] != nil
		out = append(out, ref)
	}
	return out
}

// linkSpan locates the source span of one scanned inline link. The scanner
// reports the link's start and destination; the closing parenthesis is found
// here. A link whose text does not match the reported destination is skipped
// rather than repaired at a guessed offset.
func linkSpan(src string, occ docgen.RefOccurrence) (LinkRef, bool) {
	start := offsetOfLineCol(src, occ.Line, occ.Column)
	if start < 0 || start >= len(src) || src[start] != '[' {
		return LinkRef{}, false
	}
	closeText := strings.Index(src[start:], "](")
	if closeText < 0 {
		return LinkRef{}, false
	}
	labelEnd := start + closeText
	destStart := labelEnd + 2
	destEnd := destStart + len(occ.Dest)
	if destEnd > len(src) || src[destStart:destEnd] != occ.Dest {
		// The destination may be escaped or split; find it after the label
		// instead of trusting the arithmetic.
		rel := strings.Index(src[destStart:], occ.Dest)
		if rel < 0 {
			return LinkRef{}, false
		}
		destStart += rel
		destEnd = destStart + len(occ.Dest)
	}
	end := len(src)
	if rel := strings.IndexByte(src[destEnd:], ')'); rel >= 0 {
		end = destEnd + rel + 1
	}
	id := occ.ID
	if id == "" {
		id = strings.TrimPrefix(occ.Dest, docgen.RefLinkPrefix)
	}
	return LinkRef{
		ID:        id,
		Dest:      occ.Dest,
		Label:     src[start+1 : labelEnd],
		Start:     start,
		DestStart: destStart,
		DestEnd:   destEnd,
		End:       end,
	}, true
}

// offsetOfLineCol converts docgen's 1-based line and byte column to a byte
// offset.
func offsetOfLineCol(src string, line, col int) int {
	if line < 1 {
		line = 1
	}
	if col < 1 {
		col = 1
	}
	off := 0
	for l := 1; l < line && off < len(src); l++ {
		i := strings.IndexByte(src[off:], '\n')
		if i < 0 {
			return len(src)
		}
		off += i + 1
	}
	off += col - 1
	if off > len(src) {
		off = len(src)
	}
	return off
}
