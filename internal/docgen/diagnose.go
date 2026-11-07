package docgen

import (
	"fmt"
	"sort"
	"strings"

	"gorequest/internal/core"
)

// Diagnostic kinds produced by docgen for document references. They extend
// the B09 document diagnostics with request-reference facts and re-use
// core.DocDiagnostic so the Problems panel and the export preview share one
// shape.
const (
	// DiagMissingRequest marks a reference whose request ID is not in the
	// project (severity error).
	DiagMissingRequest = "docgen-missing-request"
	// DiagBadReference marks a reference that violates the reference grammar
	// (severity error).
	DiagBadReference = "docgen-bad-reference"
	// DiagRefNoCard marks an inline request link whose document has no card
	// directive for that request, so an HTML export anchor for it will not
	// resolve (severity info).
	DiagRefNoCard = "docgen-ref-no-card"
	// DiagStrayEnd marks an end marker that closes nothing (severity
	// warning).
	DiagStrayEnd = "docgen-stray-end"
	// DiagTooManyRefs reports a document above MaxCardDirectivesPerDoc
	// (severity error).
	DiagTooManyRefs = "docgen-too-many-refs"
	// DiagScrubbed reports that deriving a card masked or blocked example
	// values (severity warning).
	DiagScrubbed = "docgen-scrubbed"
)

// DiagnoseRefs converts the reference facts of one document into positioned
// diagnostics. exp may be nil (facts are then recomputed by scanning src).
func DiagnoseRefs(project *core.Project, docRel, src string, exp *Expansion) []core.DocDiagnostic {
	if exp == nil {
		exp = &Expansion{Refs: ScanDocument(src)}
	}
	out := make([]core.DocDiagnostic, 0, len(exp.Refs)+4)

	// Which request IDs have a card directive in this document?
	cardIDs := map[string]bool{}
	for _, r := range exp.Refs {
		if r.Kind == RefKindDirective && !r.Malformed {
			cardIDs[r.ID] = true
		}
	}

	for _, r := range exp.Refs {
		switch {
		case r.Malformed:
			out = append(out, core.DocDiagnostic{
				RelPath: docRel, Line: r.Line, Column: r.Column,
				Severity: core.SeverityError, Kind: DiagBadReference, Dest: r.Dest,
				Message: fmt.Sprintf("malformed gorequest reference: %s (%s)", r.Dest, malformedDetail(r)),
			})
		case requestEntryFor(project, r.ID) == nil:
			out = append(out, core.DocDiagnostic{
				RelPath: docRel, Line: r.Line, Column: r.Column,
				Severity: core.SeverityError, Kind: DiagMissingRequest, Dest: r.ID,
				Message: fmt.Sprintf("request %q is not in this project; the reference will not resolve", r.ID),
			})
		case r.Kind == RefKindLinkRef && !cardIDs[r.ID]:
			out = append(out, core.DocDiagnostic{
				RelPath: docRel, Line: r.Line, Column: r.Column,
				Severity: core.SeverityInfo, Kind: DiagRefNoCard, Dest: r.ID,
				Message: fmt.Sprintf("no card directive for %q in this document; the exported anchor %q will not resolve", r.ID, cardAnchor(r.ID)),
			})
		}
	}

	for _, line := range exp.StrayEndLines {
		out = append(out, core.DocDiagnostic{
			RelPath: docRel, Line: line, Column: 1,
			Severity: core.SeverityWarning, Kind: DiagStrayEnd, Dest: EndMarker,
			Message: "end marker without an open generated block; remove it or a card directive above it",
		})
	}
	if exp.Truncated {
		out = append(out, core.DocDiagnostic{
			RelPath: docRel, Line: 1, Column: 1,
			Severity: core.SeverityError, Kind: DiagTooManyRefs, Dest: "",
			Message: fmt.Sprintf("more than %d card directives; derivation stopped early", MaxCardDirectivesPerDoc),
		})
	}
	for _, r := range exp.Refs {
		if r.Kind != RefKindDirective || r.Malformed {
			continue
		}
		stats, ok := exp.ScrubByID[r.ID]
		if !ok {
			continue
		}
		out = append(out, core.DocDiagnostic{
			RelPath: docRel, Line: r.Line, Column: r.Column,
			Severity: core.SeverityWarning, Kind: DiagScrubbed, Dest: r.ID,
			Message: fmt.Sprintf("card for %q masked %d value(s) and blocked %d value(s) while deriving", r.ID, stats.Masked, stats.Blocked),
		})
	}
	sortDocDiags(out)
	return out
}

func malformedDetail(r RefOccurrence) string {
	switch {
	case strings.Contains(r.Dest, "gorequest://") && !strings.HasPrefix(r.Dest, RefLinkPrefix):
		return "only gorequest://request/<id> is supported"
	case strings.HasPrefix(r.Dest, RefLinkPrefix):
		return "the ID must be ASCII letters, digits, dot, underscore or hyphen"
	default:
		return "a card directive must be <!-- gorequest:request id=\"…\" view=\"…\" --> on one line"
	}
}

// MergeDocDiagnostics combines B09 document diagnostics with docgen reference
// diagnostics. Findings about gorequest:// destinations (which docgen owns)
// are dropped from the base list so the same merge stays correct before and
// after the core-side reference integration lands.
func MergeDocDiagnostics(base, refs []core.DocDiagnostic) []core.DocDiagnostic {
	out := make([]core.DocDiagnostic, 0, len(base)+len(refs))
	for _, d := range base {
		if strings.HasPrefix(d.Dest, "gorequest://") {
			continue
		}
		out = append(out, d)
	}
	out = append(out, refs...)
	sortDocDiags(out)
	return out
}

func sortDocDiags(out []core.DocDiagnostic) {
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Line == out[j].Line {
			return out[i].Column < out[j].Column
		}
		return out[i].Line < out[j].Line
	})
}

// cardAnchor is the in-page anchor the HTML export assigns to a derived
// card. It must match render.go's anchor emission.
func cardAnchor(id string) string {
	return "docgen-request-" + strings.ReplaceAll(id, ".", "-")
}
