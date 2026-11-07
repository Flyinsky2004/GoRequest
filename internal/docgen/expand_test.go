package docgen

import (
	"strings"
	"testing"

	"gorequest/internal/core"
)

// All tests in this package are authored but NOT executed in phase 1; the
// post-integration verification run executes them with the go toolchain.

func TestExpandDocInsertsMarkedBlocksAndKeepsDirectives(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	src := mustReadDoc(t, p, "docs/api.md")
	exp, err := ExpandDoc(p, src, CardOptions{Labels: ChineseCardLabels()})
	if err != nil {
		t.Fatalf("ExpandDoc: %v", err)
	}
	if !exp.Changed {
		t.Fatalf("expansion must change a doc with directives")
	}
	if !strings.Contains(exp.Markdown, "#### `users.me` — GET /users/me") {
		t.Fatalf("card heading missing:\n%s", exp.Markdown)
	}
	if !strings.Contains(exp.Markdown, "**参数**") || !strings.Contains(exp.Markdown, "**响应**") {
		t.Fatalf("view-filtered sections missing:\n%s", exp.Markdown)
	}
	// Every generated block is delimited by BeginMarker and EndMarker.
	if got := strings.Count(exp.Markdown, BeginMarker); got != 2 {
		t.Fatalf("begin markers = %d (missing request + card), want 2:\n%s", got, exp.Markdown)
	}
	// The ghost directive yields a visible, honest note.
	if !strings.Contains(exp.Markdown, "`ghost.request`") ||
		!strings.Contains(exp.Markdown, missingRequestNote()) {
		t.Fatalf("missing request note lost:\n%s", exp.Markdown)
	}
	// The malformed directive line survives verbatim.
	if !strings.Contains(exp.Markdown, `extra="oops"`) {
		t.Fatalf("malformed directive dropped")
	}
	// The fenced pseudo-reference stayed inert.
	if strings.Count(exp.Markdown, "#### `users.me` — ") != 1 {
		t.Fatalf("pseudo-reference inside the sample produced a second card")
	}
	// Original directive lines still exist in the expansion.
	for _, want := range []string{
		`<!-- gorequest:request id="users.me" view="parameters,responses" -->`,
		`<!-- gorequest:request id="ghost.request" -->`,
	} {
		if !strings.Contains(exp.Markdown, want) {
			t.Fatalf("directive lost: %s", want)
		}
	}
}

func TestExpandDocIsIdempotent(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	src := mustReadDoc(t, p, "docs/api.md")
	first, err := ExpandDoc(p, src, CardOptions{})
	if err != nil {
		t.Fatalf("first expand: %v", err)
	}
	second, err := ExpandDoc(p, first.Markdown, CardOptions{})
	if err != nil {
		t.Fatalf("second expand: %v", err)
	}
	if second.Markdown != first.Markdown {
		t.Fatalf("expansion is not idempotent:\n--- first ---\n%s\n--- second ---\n%s", first.Markdown, second.Markdown)
	}
	if second.Changed {
		t.Fatalf("second expansion must not report changes")
	}
	// The stray-end set is identical on both passes: stale blocks are
	// consumed, and the fixture's deliberate stray end marker stays put.
	if len(second.StrayEndLines) != len(first.StrayEndLines) {
		t.Fatalf("second pass stray-end count %v must equal first pass %v",
			second.StrayEndLines, first.StrayEndLines)
	}
}

func TestDiagnoseRefsStrayEndLines(t *testing.T) {
	src := "文本\n\n<!-- gorequest:end -->\n\n" +
		"<!-- gorequest:request id=\"x\" -->\n<!-- gorequest:begin -->\n生成\n<!-- gorequest:end -->\n"
	p := newDocgenProject(t)
	exp, err := ExpandDoc(p, src, CardOptions{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	// Line 3 is stray; the second block's marker closes a real block.
	if len(exp.StrayEndLines) != 1 || exp.StrayEndLines[0] != 3 {
		t.Fatalf("stray ends: %v", exp.StrayEndLines)
	}
	diags := DiagnoseRefs(p, "docs/x.md", src, exp)
	found := false
	for _, d := range diags {
		if d.Kind == DiagStrayEnd && d.Line == 3 {
			found = true
		}
	}
	if !found {
		t.Fatalf("stray end diagnostic missing: %+v", diags)
	}
	// Re-expansion converges and keeps the stray marker untouched.
	again, err := ExpandDoc(p, exp.Markdown, CardOptions{})
	if err != nil {
		t.Fatalf("re-expand: %v", err)
	}
	if again.Markdown != exp.Markdown {
		t.Fatalf("re-expansion drifted")
	}
}

func TestExpandDocUpdatesAfterRequestChange(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	src := mustReadDoc(t, p, "docs/api.md")
	before, err := ExpandDoc(p, src, CardOptions{})
	if err != nil {
		t.Fatalf("expand before: %v", err)
	}
	if strings.Contains(before.Markdown, "/v2/users/me") {
		t.Fatalf("stale URL in first expansion")
	}
	entry := p.Requests["users.me"]
	entry.Request.HTTP.URL = "{{baseUrl}}/v2/users/me"
	if _, err := p.SaveRequest(entry, entry.Request, entry.BaselineDigest); err != nil {
		t.Fatalf("SaveRequest: %v", err)
	}
	after, err := ExpandDoc(p, src, CardOptions{})
	if err != nil {
		t.Fatalf("expand after: %v", err)
	}
	if !strings.Contains(after.Markdown, "/v2/users/me") {
		t.Fatalf("definition change did not propagate to derivation")
	}
	// Deriving over an already-expanded document converges.
	again, err := ExpandDoc(p, after.Markdown, CardOptions{})
	if err != nil {
		t.Fatalf("expand expanded: %v", err)
	}
	if again.Markdown != after.Markdown {
		t.Fatalf("re-derivation over expanded text drifted")
	}
}

func TestExpandDocCardSitesPointAtHeadings(t *testing.T) {
	p := newDocgenProjectWithDocs(t)
	src := mustReadDoc(t, p, "docs/api.md")
	exp, err := ExpandDoc(p, src, CardOptions{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if len(exp.Cards) != 1 || exp.Cards[0].RequestID != "users.me" {
		t.Fatalf("cards: %+v", exp.Cards)
	}
	site := exp.Cards[0]
	lines := strings.Split(exp.Markdown, "\n")
	if site.HeadingLine < 1 || site.HeadingLine > len(lines) {
		t.Fatalf("heading line out of range: %d", site.HeadingLine)
	}
	if !strings.HasPrefix(lines[site.HeadingLine-1], "#### `users.me`") {
		t.Fatalf("heading line %d does not start the card: %q", site.HeadingLine, lines[site.HeadingLine-1])
	}
}

func TestExpandDocNilProjectStillDerivesNotes(t *testing.T) {
	exp, err := ExpandDoc(nil, "<!-- gorequest:request id=\"users.me\" -->\n", CardOptions{})
	if err != nil {
		t.Fatalf("expand: %v", err)
	}
	if !strings.Contains(exp.Markdown, "`users.me`") || !strings.Contains(exp.Markdown, BeginMarker) {
		t.Fatalf("nil project must produce a visible note block:\n%s", exp.Markdown)
	}
}
