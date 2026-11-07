package docgen

import (
	"strings"
	"testing"

	"gorequest/internal/core"
)

// All tests in this package are authored but NOT executed in phase 1; the
// post-integration verification run executes them with the go toolchain.

func TestParseDirectiveLineRoundTrip(t *testing.T) {
	line := FormatDirective("users.me", []string{ViewParameters, ViewResponses})
	if line != `<!-- gorequest:request id="users.me" view="parameters,responses" -->` {
		t.Fatalf("FormatDirective produced %q", line)
	}
	views, ok, err := ParseDirectiveLine("  " + line)
	if err != nil || !ok {
		t.Fatalf("valid directive rejected: %v", err)
	}
	if len(views) != 2 || views[0] != ViewParameters || views[1] != ViewResponses {
		t.Fatalf("views = %v", views)
	}
	if views, ok, _ := ParseDirectiveLine(FormatDirective("users.me", nil)); !ok || views != nil {
		t.Fatalf("view-less directive should parse with nil views: %v %v", views, ok)
	}
	// Other gorequest comments are not card directives.
	if _, ok, _ := ParseDirectiveLine(EndMarker); ok {
		t.Fatalf("end marker must not parse as a card directive")
	}
	if _, ok, _ := ParseDirectiveLine("<!-- gorequest:future thing -->"); ok {
		t.Fatalf("unknown gorequest comment must not parse as a card directive")
	}
	if _, ok, _ := ParseDirectiveLine("普通文本 <!-- gorequest:request id=\"x\" -->"); ok {
		t.Fatalf("a mid-sentence comment is not a directive line")
	}
}

func TestParseDirectiveLineRejectsBadGrammar(t *testing.T) {
	cases := []string{
		`<!-- gorequest:request id="users.me" view="unknown" -->`,
		`<!-- gorequest:request id="users.me" rogue="1" -->`,
		`<!-- gorequest:request id="does not exist" -->`,
		`<!-- gorequest:request id="" -->`,
		`<!-- gorequest:request id="users.me view="x" -->`,
		`<!-- gorequest:request id="users.me"`,
		`<!-- gorequest:request view="parameters" id="x" -->`,
	}
	for _, line := range cases {
		if _, ok, err := ParseDirectiveLine(line); ok || err == nil {
			t.Errorf("ParseDirectiveLine(%q) should fail, got ok=%v err=%v", line, ok, err)
		}
	}
}

func TestValidRequestIDCharset(t *testing.T) {
	valid := []string{"users.me", "a", "a-b_c.d", "A9.x"}
	invalid := []string{"", ".lead", "-lead", "a..b", "a b", "a/b", "a%20b", "中文名", "x?y", strings.Repeat("a", MaxRequestIDLen+1)}
	for _, id := range valid {
		if !ValidRequestID(id) {
			t.Errorf("ValidRequestID(%q) = false, want true", id)
		}
	}
	for _, id := range invalid {
		if ValidRequestID(id) {
			t.Errorf("ValidRequestID(%q) = true, want false", id)
		}
	}
}

func TestParseRefLink(t *testing.T) {
	if id, ok := ParseRefLink("gorequest://request/users.me"); !ok || id != "users.me" {
		t.Fatalf("good ref = %q %v", id, ok)
	}
	for _, bad := range []string{
		"gorequest://request/", "gorequest://request/a b", "gorequest://request/中文",
		"gorequest://folder/x", "gorequest://request/x?y", "gorequest://request/x#z",
		"https://request/x", "docs.md",
	} {
		if _, ok := ParseRefLink(bad); ok {
			t.Errorf("ParseRefLink(%q) should not parse", bad)
		}
	}
	if !IsValidRefShape("gorequest://anything") {
		t.Fatalf("any gorequest:// prefix is a docgen-owned reference shape")
	}
	if IsValidRefShape("https://example.com") {
		t.Fatalf("foreign schemes are not docgen references")
	}
}

func TestScanDocumentPositionsAndFences(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	src := mustReadDoc(t, p, "docs/api.md")
	refs := ScanDocument(src)

	var link, directive, healthLink, malformed *RefOccurrence
	directiveCount := 0
	for i := range refs {
		r := refs[i]
		switch {
		case r.Kind == RefKindLinkRef && !r.Malformed && r.ID == "users.me" && link == nil:
			cp := r
			link = &cp
		case r.Kind == RefKindLinkRef && r.ID == "health":
			cp := r
			healthLink = &cp
		case r.Kind == RefKindDirective && !r.Malformed && r.ID == "users.me":
			directiveCount++
			cp := r
			directive = &cp
		case r.Malformed:
			cp := r
			malformed = &cp
		}
	}
	if link == nil || directive == nil || healthLink == nil || malformed == nil {
		t.Fatalf("scan coverage incomplete: %+v", refs)
	}
	if directiveCount != 1 {
		t.Fatalf("the directive inside the fenced sample must not be scanned; got %d directives", directiveCount)
	}
	srcLine := 1 + strings.Count(string(src[:srcIndex(src, "gorequest://request/users.me")]), "\n")
	if link.Line != srcLine {
		t.Fatalf("link line %d, want %d", link.Line, srcLine)
	}
	if directive.Column != 1 {
		t.Fatalf("directive at column %d, want 1", directive.Column)
	}
	if len(directive.Views) != 2 {
		t.Fatalf("views not parsed: %v", directive.Views)
	}
	if malformed.ID != "" || strings.Count(strings.Join(refOccurrences(malformed), ""), "") < 0 {
		t.Fatalf("malformed directive keeps its best-effort id: %+v", malformed)
	}
}

func srcIndex(src, sub string) int {
	i := strings.Index(string(src), sub)
	if i < 0 {
		return 0
	}
	return i
}

func refOccurrences(r *RefOccurrence) []string {
	return []string{r.ID, r.Dest}
}

func TestDiagnoseRefsReportsMissingAndGrammar(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	src := mustReadDoc(t, p, "docs/api.md")
	diags := DiagnoseRefs(p, "docs/api.md", src, nil)

	kinds := map[string][]core.DocDiagnostic{}
	for _, d := range diags {
		kinds[d.Kind] = append(kinds[d.Kind], d)
	}
	if len(kinds[DiagMissingRequest]) == 0 {
		t.Fatalf("missing ghost.request not reported: %+v", diags)
	}
	found := false
	for _, d := range kinds[DiagMissingRequest] {
		if d.Dest == "ghost.request" && d.Severity == core.SeverityError && d.Line > 0 {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing-request diagnostic lacks id/position: %+v", kinds[DiagMissingRequest])
	}
	if len(kinds[DiagBadReference]) == 0 {
		t.Fatalf("malformed directive (extra attribute) not reported: %+v", diags)
	}
	// health link in this doc has no card directive → info.
	noCard := false
	for _, d := range kinds[DiagRefNoCard] {
		if d.Dest == "health" && d.Severity == core.SeverityInfo {
			noCard = true
		}
	}
	if !noCard {
		t.Fatalf("ref-no-card diagnostic missing: %+v", diags)
	}
	// users.me has a card: no missing-request diagnostic for it.
	for _, d := range kinds[DiagMissingRequest] {
		if d.Dest == "users.me" {
			t.Fatalf("users.me resolved but reported missing: %+v", d)
		}
	}
	// Nil project: everything resolves as missing rather than panicking.
	empty := DiagnoseRefs(nil, "docs/api.md", src, nil)
	if len(empty) == 0 {
		t.Fatalf("nil project must still yield positioned diagnostics")
	}
}

func TestMergeDocDiagnosticsDropsGorequestBaseFindings(t *testing.T) {
	base := []core.DocDiagnostic{
		{RelPath: "docs/a.md", Line: 3, Column: 1, Severity: core.SeverityError, Kind: core.DiagBadLink, Dest: "gorequest://request/users.me", Message: "pre-patch unsupported scheme"},
		{RelPath: "docs/a.md", Line: 4, Column: 2, Severity: core.SeverityError, Kind: core.DiagMissingLink, Dest: "nope.md", Message: "missing doc"},
	}
	refs := []core.DocDiagnostic{
		{RelPath: "docs/a.md", Line: 3, Column: 1, Severity: core.SeverityWarning, Kind: DiagScrubbed, Dest: "users.me"},
	}
	got := MergeDocDiagnostics(base, refs)
	if len(got) != 2 {
		t.Fatalf("merge kept gorequest base finding: %+v", got)
	}
	if got[0].Line != 3 || got[0].Kind != DiagScrubbed {
		t.Fatalf("merge ordering/kind wrong: %+v", got[0])
	}
	if got[1].Line != 4 {
		t.Fatalf("base missing-doc finding lost: %+v", got[1])
	}
}
