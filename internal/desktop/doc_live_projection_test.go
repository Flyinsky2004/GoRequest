package desktop

import (
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"

	"gorequest/internal/docedit"
)

func observeDocStates(t *testing.T) map[*Tab]*docViewState {
	t.Helper()
	observed := map[*Tab]*docViewState{}
	prev := docLiveObserve
	docLiveObserve = func(tab *Tab, st *docViewState) {
		observed[tab] = st
	}
	t.Cleanup(func() {
		docLiveObserve = prev
	})
	return observed
}

func activeBlockEditor(t *testing.T, observed map[*Tab]*docViewState, tab *Tab) *editor.State {
	t.Helper()
	st := observed[tab]
	if st == nil || st.live == nil || st.live.block == nil || st.live.block.ed == nil {
		t.Fatal("expected active canvas block editor")
	}
	return st.live.block.ed
}

func TestDocSlashMenuIconsParseAndPaintDistinctSVGs(t *testing.T) {
	cmds := docedit.SlashCommands()
	if len(cmds) == 0 {
		t.Fatal("empty slash command catalog")
	}
	a, ps := openTestProject(t, "Slash Icons Paint")
	entry := addRequestForTest(t, ps, "auth", "Login")

	seen := make(map[*ui.SVG]string, len(cmds))
	for _, cmd := range cmds {
		svg := docSlashIcon(cmd)
		if svg == nil {
			t.Fatalf("command %q (%s) returned nil SVG icon", cmd.ID, cmd.Kind)
		}
		if prev, dup := seen[svg]; dup {
			t.Fatalf("command %q shares the exact SVG pointer of %q", cmd.ID, prev)
		}
		seen[svg] = cmd.ID
		if strings.TrimSpace(a.docSlashDescription(cmd)) == "" {
			t.Fatalf("command %q missing English description", cmd.ID)
		}
	}
	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	for _, cmd := range cmds {
		if strings.TrimSpace(a.docSlashDescription(cmd)) == "" {
			t.Fatalf("command %q missing Chinese description", cmd.ID)
		}
	}
	a.mgr.cfg.cfg.Language = string(langEnglish)

	for i, dark := range []bool{false, true} {
		rel := "docs/icons-light.md"
		if i == 1 {
			rel = "docs/icons-dark.md"
		}
		iconTab := openDocForTest(t, ps, rel, "")
		ps.activateTab(iconTab)
		tt := ui.NewTester(a.View, 1200, 820)
		tt.SetDark(dark)
		tt.Frame()
		if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
			t.Fatalf("dark=%v click empty: %v", dark, err)
		}
		tt.Frame()
		tt.Type("/")
		tt.Frame()
		tt.Advance(400 * time.Millisecond)
		tt.Frame()
		if !tt.HasText("Start typing with plain text.") || !tt.HasText("Headings in the largest font.") {
			t.Fatalf("dark=%v descriptions missing; texts=%v", dark, tt.Texts())
		}
		rowRect, ok := tt.Find("Heading 1")
		if !ok {
			t.Fatalf("dark=%v Heading 1 row missing", dark)
		}
		img := tt.Image()
		x0 := int(rowRect.X) + 8
		y0 := int(rowRect.Y + (rowRect.H-28)/2)
		base := img.RGBAAt(x0+2, y0+2)
		strokePixels := 0
		for y := y0 + 4; y < y0+24; y++ {
			for x := x0 + 4; x < x0+24; x++ {
				if img.RGBAAt(x, y) != base {
					strokePixels++
				}
			}
		}
		if strokePixels < 12 {
			t.Fatalf("dark=%v expected SVG icon strokes inside tile, got %d differing pixels", dark, strokePixels)
		}
	}

	// Verify second-level Request submenu still opens and inserts reference ID.
	tab := openDocForTest(t, ps, "docs/slash-req.md", "")
	ps.activateTab(tab)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click("Type / to insert content, @ to reference a request"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type("/request")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !tt.HasText("GET") || !tt.HasText("Login") {
		t.Fatalf("second-level request submenu missing; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	wantLink := "[@auth/Login](gorequest://request/" + entry.Request.ID + ")\n"
	if got := tab.DocEditor.Buffer.String(); got != wantLink {
		t.Fatalf("request picker inserted %q, want %q", got, wantLink)
	}
}

func TestDocCanvasHeadingEditsContentWithoutExposingHashes(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "Canvas Heading Projection")
	src := "# Top\n\n## Sub Section\n\n### Closed Title ###\n\nSetext Section\n--------------\n"
	tab := openDocForTest(t, ps, "docs/headings.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// 1. ATX H2 hides "## " in active editor and saves back "## ".
	if err := tt.Click(canvasBlockLabel(t, src, 1)); err != nil {
		t.Fatalf("click H2: %v", err)
	}
	tt.Frame()
	ed := activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "Sub Section" {
		t.Fatalf("active H2 editor text = %q, want %q (without # prefix)", got, "Sub Section")
	}
	tt.Type(" Updated")
	tt.Frame()

	// 2. Closed ATX H3 hides both leading and closing "###".
	cur := tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 2)); err != nil {
		t.Fatalf("click closed H3: %v", err)
	}
	tt.Frame()
	ed = activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "Closed Title" {
		t.Fatalf("active closed H3 editor text = %q, want %q", got, "Closed Title")
	}
	tt.Type("!")
	tt.Frame()

	// 3. Setext H2 hides underline "--------------" and preserves it on commit.
	cur = tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 3)); err != nil {
		t.Fatalf("click Setext H2: %v", err)
	}
	tt.Frame()
	ed = activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "Setext Section" {
		t.Fatalf("active Setext editor text = %q, want %q", got, "Setext Section")
	}
	tt.Type(" v2")
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()

	want := "# Top\n\n## Sub Section Updated\n\n### Closed Title! ###\n\nSetext Section v2\n--------------\n"
	disk, _, err := ps.project.Store.Read("docs/headings.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != want {
		t.Fatalf("saved markdown =\n%q\nwant:\n%q", string(disk), want)
	}

	// Raw source mode ("Edit") must preserve complete Markdown syntax.
	if err := tt.Click("Edit"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("source mode buffer = %q, want %q", got, want)
	}
	if err := tt.Click("Canvas"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tab.Dirty {
		t.Fatal("round-tripping between source and canvas after save must stay clean")
	}
}

func TestDocCanvasHeadingLeadingTrailingEnterAndBackspace(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "Heading Enter Backspace")
	src := "# Top\n\n### Deep Heading\n"
	tab := openDocForTest(t, ps, "docs/enter-backspace.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// Click H3, move to offset 0, press Enter: heading must stay H3!
	if err := tt.Click(canvasBlockLabel(t, src, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "# Top\n\n\n### Deep Heading\n" {
		t.Fatalf("leading Enter in H3 = %q, want H3 level preserved", got)
	}
	ed := activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "Deep Heading" {
		t.Fatalf("active editor after leading Enter = %q, want %q", got, "Deep Heading")
	}

	// Undo restores exact original document.
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("undo leading Enter = %q, want %q", got, src)
	}

	// Trailing Enter at end of H3 opens a blank paragraph below; immediate
	// Backspace collapses the blank line back to the end of H3.
	ed = activeBlockEditor(t, observed, tab)
	ed.Select(len(ed.Buffer.String()), len(ed.Buffer.String()))
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !observed[tab].live.block.synthetic {
		t.Fatal("expected synthetic blank block after trailing Enter on heading")
	}
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("Backspace on trailing blank line = %q, want %q", got, src)
	}
	ed = activeBlockEditor(t, observed, tab)
	if ed.Buffer.String() != "Deep Heading" || ed.Cursor() != len("Deep Heading") {
		t.Fatalf("caret after collapsing blank line: text=%q cursor=%d", ed.Buffer.String(), ed.Cursor())
	}

	// Backspace at offset 0 of projected H3:
	// 1st Backspace strips "### " to plain paragraph; 2nd merges into "# Top".
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "# Top\n\nDeep Heading\n" {
		t.Fatalf("1st Backspace at start of H3 = %q, want plain paragraph", got)
	}
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "# TopDeep Heading\n" {
		t.Fatalf("2nd Backspace at start of paragraph = %q, want merged into # Top", got)
	}
	ed = activeBlockEditor(t, observed, tab)
	if ed.Buffer.String() != "TopDeep Heading" || ed.Cursor() != len("Top") {
		t.Fatalf("merged heading projection = %q cursor=%d", ed.Buffer.String(), ed.Cursor())
	}

	// Undo twice + Redo twice round-trips cleanly.
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != src {
		t.Fatalf("two undos = %q, want %q", got, src)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "# Top\n\nDeep Heading\n" {
		t.Fatalf("first redo = %q", got)
	}
	tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "# TopDeep Heading\n" {
		t.Fatalf("second redo = %q", got)
	}
}

func TestDocCanvasCrossLineSelectionAndListQuoteProjection(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "Cross Line Selection")
	src := "3. alpha\n4. beta\n5. gamma\n\n- [ ] todo\n- [x] done\n\n>> quote one\n>> quote two\n"
	tab := openDocForTest(t, ps, "docs/lists-quotes.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// 1. Ordered list hides "3. "/"4. "/"5. ", preserves cross-line selection,
	// and renumbers from 3 on cross-line replacement.
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ed := activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "alpha\nbeta\ngamma" {
		t.Fatalf("ordered list projected text = %q", got)
	}
	// Select across lines 2 and 3 ("ta\ngam") with directional Anchor > Head,
	// verify frame render preserves directional selection, then type replacement.
	selStart := strings.Index(ed.Buffer.String(), "ta\ngam")
	selEnd := selStart + len("ta\ngam")
	ed.SetSelections(editor.Selection{Anchor: selEnd, Head: selStart})
	tt.Frame()
	if gotSel := activeBlockEditor(t, observed, tab).Selection(); gotSel.Anchor != selEnd || gotSel.Head != selStart {
		t.Fatalf("cross-line selection lost across frame: got %+v, want Anchor=%d Head=%d", gotSel, selEnd, selStart)
	}
	tt.Type("TA-GAM")
	tt.Frame()
	if !strings.HasPrefix(tab.DocEditor.Buffer.String(), "3. alpha\n4. beTA-GAMma\n\n") {
		t.Fatalf("cross-line edit in ordered list = %q", tab.DocEditor.Buffer.String())
	}

	// 2. Task list hides "- [ ] " / "- [x] ", preserves checked state on edit,
	// and opens a new unchecked "- [ ] " item on Enter.
	cur := tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ed = activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "todo\ndone" {
		t.Fatalf("task list projected text = %q", got)
	}
	ed.Select(len(ed.Buffer.String()), len(ed.Buffer.String()))
	tt.Type("!")
	tt.Key(0, ui.KeyEnter)
	tt.Type("next")
	tt.Frame()
	if !strings.Contains(tab.DocEditor.Buffer.String(), "- [ ] todo\n- [x] done!\n- [ ] next\n") {
		t.Fatalf("task list edit = %q", tab.DocEditor.Buffer.String())
	}

	// 3. Nested blockquote ">> " hides markers and preserves depth across Enter.
	cur = tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 2)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ed = activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "quote one\nquote two" {
		t.Fatalf("quote projected text = %q", got)
	}
	ed.Select(len(ed.Buffer.String()), len(ed.Buffer.String()))
	tt.Key(0, ui.KeyEnter)
	tt.Type("quote three")
	tt.Frame()
	if !strings.HasSuffix(tab.DocEditor.Buffer.String(), ">> quote one\n>> quote two\n>> quote three\n") {
		t.Fatalf("nested quote edit = %q", tab.DocEditor.Buffer.String())
	}
}

func TestDocCanvasSlashOffsetMappingInProjectedBlocks(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "Slash Projected Offset")
	entry := addRequestForTest(t, ps, "auth", "Login")
	src := "## Old\n\nLegacy\n======\n\n- first\n- second\n"
	tab := openDocForTest(t, ps, "docs/slash-proj.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// 1. Block-conversion slash "/h4 Fresh Title" inside projected "## Old":
	// replaces "## " with "#### " and selects "Fresh Title" in projected coords.
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Command("selectAll")
	tt.Type("/h4 Fresh Title")
	tt.Frame()
	if !tt.HasText("Heading 4") {
		t.Fatalf("slash menu missing inside projected heading; texts=%v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.HasPrefix(tab.DocEditor.Buffer.String(), "#### Fresh Title\n") {
		t.Fatalf("heading conversion = %q", tab.DocEditor.Buffer.String())
	}
	ed := activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "Fresh Title" {
		t.Fatalf("projected editor after /h4 = %q, want %q", got, "Fresh Title")
	}
	// Typing immediately replaces the selected "Fresh Title" in projected coords.
	tt.Type("Renamed")
	tt.Frame()
	if !strings.HasPrefix(tab.DocEditor.Buffer.String(), "#### Renamed\n") {
		t.Fatalf("typing over slash selection = %q", tab.DocEditor.Buffer.String())
	}

	// 2. Inline slash "/link API Guide" inside projected heading keeps "#### "
	// and selects "url" in projected coordinates.
	tt.Command("selectAll")
	tt.Type("/link API Guide")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.HasPrefix(tab.DocEditor.Buffer.String(), "#### [API Guide](url)\n") {
		t.Fatalf("inline /link inside heading = %q", tab.DocEditor.Buffer.String())
	}
	tt.Type("https://example.com")
	tt.Frame()
	if !strings.HasPrefix(tab.DocEditor.Buffer.String(), "#### [API Guide](https://example.com)\n") {
		t.Fatalf("typing URL over /link selection = %q", tab.DocEditor.Buffer.String())
	}

	// 3. Converting Setext heading via "/h3 Modern" removes "======" cleanly.
	cur := tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Command("selectAll")
	tt.Type("/h3 Modern")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if strings.Contains(tab.DocEditor.Buffer.String(), "======") || !strings.Contains(tab.DocEditor.Buffer.String(), "\n\n### Modern\n\n") {
		t.Fatalf("setext slash conversion left underline or failed: %q", tab.DocEditor.Buffer.String())
	}

	// 4. "/request" on line 2 of projected list keeps "- " bullet on both lines.
	cur = tab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 2)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ed = activeBlockEditor(t, observed, tab)
	line2Start := strings.Index(ed.Buffer.String(), "second")
	ed.Select(line2Start, len(ed.Buffer.String()))
	tt.Type("/request")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	wantTail := "- first\n- [@auth/Login](gorequest://request/" + entry.Request.ID + ")\n"
	if !strings.HasSuffix(tab.DocEditor.Buffer.String(), wantTail) {
		t.Fatalf("list /request result = %q, want suffix %q", tab.DocEditor.Buffer.String(), wantTail)
	}
}

func TestDocCanvasFrontMatterProtectedFromSetextProjectionAndMerge(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "FrontMatter Protection")
	entry := addRequestForTest(t, ps, "auth", "Login")
	fm := "---\ntitle: Payments API\nversion: 2\n---\n"
	body := "\n## Overview\n\nSee [Guide][g1] and [@auth/login](gorequest://request/" + entry.Request.ID + ").\n\n[g1]: ./guide.md\n"
	src := fm + body
	tab := openDocForTest(t, ps, "docs/frontmatter.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()

	// Frontmatter renders as explicit verbatim card, never projected as Setext H2.
	if !tt.HasText("YAML front matter") || !tt.HasText("doc-block:frontmatter:0:source") {
		t.Fatalf("frontmatter source card missing; texts=%v", tt.Texts())
	}
	st := observed[tab]
	if st == nil {
		t.Fatal("missing docViewState")
	}
	if len(st.headings) != 1 || st.headings[0].Text != "Overview" || st.headings[0].Level != 2 {
		t.Fatalf("outline headings leaked frontmatter: %+v", st.headings)
	}

	// Click "## Overview": 1st Backspace at offset 0 unprojects to "Overview",
	// 2nd Backspace refuses to merge onto YAML frontmatter.
	if err := tt.Click(canvasBlockLabel(t, src, 1)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyHome)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()

	got := tab.DocEditor.Buffer.String()
	if !strings.HasPrefix(got, fm+"\nOverview\n") {
		t.Fatalf("frontmatter corrupted or merged: %q", got)
	}
	if !strings.Contains(got, "[Guide][g1]") || !strings.Contains(got, "[g1]: ./guide.md") ||
		!strings.Contains(got, "gorequest://request/"+entry.Request.ID) {
		t.Fatalf("reference links or request IDs altered: %q", got)
	}
}

func TestDocCanvasChineseIMENoFinalNewlineCRLFFallbacks(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "IME NoNewline CRLF Fallbacks")

	// 1. Chinese IME composition + no-final-newline preservation on projected H2.
	noNL := "## 标题"
	tab := openDocForTest(t, ps, "docs/nonl.md", noNL)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, noNL, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ed := activeBlockEditor(t, observed, tab)
	if got := ed.Buffer.String(); got != "标题" {
		t.Fatalf("projected Chinese heading = %q, want %q", got, "标题")
	}
	tt.Compose("内容", len("内容"))
	tt.Frame()
	if !caretPainted(tt) {
		t.Fatal("caret lost during Chinese IME composition in projected heading")
	}
	tt.Type("内容")
	tt.Key(ui.Cmd, ui.KeyS)
	tt.Frame()
	disk, _, err := ps.project.Store.Read("docs/nonl.md")
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != "## 标题内容" {
		t.Fatalf("saved no-final-newline Chinese heading = %q, want exact %q", string(disk), "## 标题内容")
	}

	// 2. CRLF document opens clean, keeps CRLF line-ending metadata on the
	// buffer, and projects headings/quotes without exposing markers.
	crlfTab := openDocForTest(t, ps, "docs/crlf-canvas.md", "## Win Heading\r\n\r\n> Win Quote\r\n")
	ps.activateTab(crlfTab)
	tt.Frame()
	if crlfTab.Dirty {
		t.Fatal("freshly opened CRLF document must not be dirty on canvas")
	}
	if crlfTab.DocEditor.Buffer.LineEnding() != "\r\n" {
		t.Fatalf("CRLF buffer LineEnding = %q, want CRLF", crlfTab.DocEditor.Buffer.LineEnding())
	}
	cur := crlfTab.DocEditor.Buffer.String()
	if err := tt.Click(canvasBlockLabel(t, cur, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if got := activeBlockEditor(t, observed, crlfTab).Buffer.String(); got != "Win Heading" {
		t.Fatalf("projected CRLF heading = %q, want %q", got, "Win Heading")
	}

	// 3. Complex blocks (fenced code, nested list, mixed-depth quote) fall back
	// to lossless source in the active editor.
	complexSrc := "```go\nfmt.Println(1)\n```\n\n- parent\n  - child\n\n> outer\n>> inner\n"
	complexTab := openDocForTest(t, ps, "docs/complex.md", complexSrc)
	ps.activateTab(complexTab)
	tt.Frame()
	for idx, wantRaw := range []string{
		"```go\nfmt.Println(1)\n```",
		"- parent\n  - child",
		"> outer\n>> inner",
	} {
		if err := tt.Click(canvasBlockLabel(t, complexSrc, idx)); err != nil {
			t.Fatalf("click complex block %d: %v", idx, err)
		}
		tt.Frame()
		if got := activeBlockEditor(t, observed, complexTab).Buffer.String(); got != wantRaw {
			t.Fatalf("complex block %d buffer = %q, want lossless source %q", idx, got, wantRaw)
		}
	}
}
