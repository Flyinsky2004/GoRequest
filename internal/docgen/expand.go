package docgen

import (
	"fmt"
	"strings"

	"gorequest/internal/core"
)

// Expansion is the result of deriving the card blocks of one document.
type Expansion struct {
	// Source is the input text.
	Source string
	// Markdown is the expanded text: every well-formed card directive is
	// followed by a freshly derived block delimited by the end marker.
	// Lines are joined with LF, matching the document workbench's
	// normalization.
	Markdown string
	// Changed reports whether a derived block was inserted or replaced.
	Changed bool
	// Cards locates each derived card in the expanded text.
	Cards []CardSite
	// ScrubByID carries the scrub statistics of every derived card, keyed by
	// request ID, so diagnostics can report masked example values.
	ScrubByID map[string]ScrubStats
	// Refs are all request references found in the source.
	Refs []RefOccurrence
	// StrayEndLines lists 1-based source lines of end markers that did not
	// close a generated block (kept as-is and reported by DiagnoseRefs).
	StrayEndLines []int
	// Truncated reports that the document exceeded MaxCardDirectivesPerDoc
	// and derivation stopped early; DiagnoseRefs reports it.
	Truncated bool
}

// CardSite locates a derived card in expanded Markdown.
type CardSite struct {
	RequestID   string
	HeadingLine int // 1-based line of the card heading in Markdown
}

// MaxCardDirectivesPerDoc bounds derivation work per document.
const MaxCardDirectivesPerDoc = 100

// ExpandDoc derives the card blocks of src. Requests are resolved through
// project; a nil project resolves nothing and every directive yields a
// visible "missing request" note instead of silently disappearing.
//
// The derived block between a card directive and its end marker is owned by
// docgen: re-derivation consumes a previous block wholesale and replaces it,
// which makes ExpandDoc idempotent (ExpandDoc(p, ExpandDoc(p, src).Markdown)
// equals ExpandDoc(p, src)) and keeps the directive itself the single
// authored source of the derived content. Directives and markers inside code
// regions are inert: they are never recognized and never consumed.
func ExpandDoc(project *core.Project, src string, opts CardOptions) (*Expansion, error) {
	refs := ScanDocument(src)
	exp := &Expansion{Source: src, Refs: refs, ScrubByID: map[string]ScrubStats{}}
	if opts.Labels.Interface == "" {
		opts.Labels = EnglishCardLabels()
	}

	lines := strings.Split(src, "\n")
	out := make([]string, 0, len(lines)+64)
	changed := false
	directives := 0
	var fence fenceTracker

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmedRight := strings.TrimRight(line, "\r\n")
		trimmed := strings.TrimSpace(trimmedRight)

		if fence.withLine(trimmedRight) {
			out = append(out, line)
			continue
		}

		views, isDirective, dirErr := ParseDirectiveLine(trimmedRight)
		if isDirective || dirErr != nil {
			out = append(out, line)
			if dirErr != nil {
				// Leave a malformed directive untouched; DiagnoseRefs
				// reports it at its line.
				continue
			}
			directives++
			if directives > MaxCardDirectivesPerDoc {
				exp.Truncated = true
				continue
			}
			cardLines, site, scrub := renderDirectiveBlock(project, trimmed, views, opts, len(out))
			out = append(out, cardLines...)
			changed = true
			if site != nil {
				exp.Cards = append(exp.Cards, *site)
			}
			if scrub.Masked > 0 || scrub.Blocked > 0 {
				if id := directiveID(trimmed); id != "" {
					exp.ScrubByID[id] = scrub
				}
			}
			// Consume a previous run's generated block, if one follows. This
			// is the round-trip: the stale block is replaced, never
			// duplicated.
			if end := generatedBlockEnd(lines, i+1); end > i {
				for j := i + 1; j <= end; j++ {
					// Keep the shared fence state in sync for the consumed
					// lines so code regions stay balanced afterwards.
					fence.withLine(strings.TrimRight(lines[j], "\r\n"))
				}
				i = end
			}
			continue
		}

		if trimmed == EndMarker {
			// A stray end marker without an open generated block: keep it so
			// the author sees and removes it; DiagnoseRefs reports it.
			exp.StrayEndLines = append(exp.StrayEndLines, i+1)
		}
		out = append(out, line)
	}

	exp.Markdown = strings.Join(out, "\n")
	exp.Changed = changed && exp.Markdown != src
	return exp, nil
}

// generatedBlockEnd returns the index of the end-marker line closing the
// generated block that begins at start with an explicit BeginMarker line, or
// start-1 when start is not a BeginMarker or no matching marker exists before
// the next directive or the end of input. A block that was never opened by
// BeginMarker is user content and is never consumed. Markers inside code
// regions (a fenced example quoting the syntax) do not close the block.
func generatedBlockEnd(lines []string, start int) int {
	if start >= len(lines) || strings.TrimSpace(strings.TrimRight(lines[start], "\r\n")) != BeginMarker {
		return start - 1
	}
	var fence fenceTracker
	for i := start + 1; i < len(lines); i++ {
		t := strings.TrimRight(lines[i], "\r\n")
		if fence.withLine(t) {
			continue
		}
		trimmed := strings.TrimSpace(t)
		if trimmed == EndMarker {
			return i
		}
		if _, isDirective, _ := ParseDirectiveLine(t); isDirective {
			return start - 1
		}
	}
	return start - 1
}

// renderDirectiveBlock builds the block that follows a directive line: the
// derived card, or an explicit missing-request note. The block is always
// delimited by the end marker so re-derivation can replace it.
func renderDirectiveBlock(project *core.Project, directive string, views []string, opts CardOptions, dirLineOut int) ([]string, *CardSite, ScrubStats) {
	id := directiveID(directive)
	block := make([]string, 0, 32)
	var site *CardSite
	var scrub ScrubStats
	entry := requestEntryFor(project, id)
	if entry == nil {
		note := fmt.Sprintf("> **%s `%s`**: %s", opts.Labels.Interface, id, missingRequestNote())
		block = append(block, BeginMarker, note, "")
	} else if card, err := BuildRequestCard(project, entry, opts); err != nil {
		note := fmt.Sprintf("> **%s `%s`**: %s", opts.Labels.Interface, id, err.Error())
		block = append(block, BeginMarker, note, "")
	} else {
		md := BuildCardMarkdown(card, views)
		block = append(block, BeginMarker)
		block = append(block, strings.Split(strings.TrimRight(md, "\n"), "\n")...)
		// out ends with the directive at 1-based line dirLineOut; the begin
		// marker and the card heading follow next.
		site = &CardSite{RequestID: id, HeadingLine: dirLineOut + 2}
		scrub = card.Scrub
	}
	block = append(block, "", EndMarker)
	return block, site, scrub
}

// requestEntryFor resolves a request ID against the project map.
func requestEntryFor(project *core.Project, id string) *core.RequestEntry {
	if project == nil || project.Requests == nil {
		return nil
	}
	entry, _ := project.Requests[id]
	return entry
}

// missingRequestNote is the visible note emitted when a card directive points
// at a request that is not in the project.
func missingRequestNote() string {
	return "no request with this ID is in this project — card not derived (update the reference or rename the request)."
}

// ExpandedSourceHasRefs reports whether src contains anything docgen would
// change; cheap check for preview hot paths.
func ExpandedSourceHasRefs(src string) bool {
	return strings.Contains(src, "<!-- gorequest:") || strings.Contains(src, "gorequest://")
}
