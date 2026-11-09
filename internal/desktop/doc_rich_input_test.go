package desktop

import (
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/egoist/mygo/ui/markdown"
	"gorequest/internal/docedit"
)

func TestM01ActiveRichEditingGeometryAndRoundTrip(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M01 Native")
	src := "- [ ] parent `中文😀` **bold** [label](https://example.com)\n  - child\n    continuation\n\n- loose\n\nafter\n"
	tab := openDocForTest(t, ps, "docs/rich.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	w := be.rich
	if w == nil || len(w.rows) < 4 {
		t.Fatalf("no structural native layout: %+v", w)
	}
	if strings.Contains(w.view.Text, "`") || strings.Contains(w.view.Text, "**") || strings.Contains(w.view.Text, "https://") {
		t.Fatal(w.view.Text)
	}
	if !w.rows[0].task || w.rows[1].x <= w.rows[0].x || w.rows[2].marker != "" {
		t.Fatalf("lost list geometry: %+v", w.rows)
	}
	var code docedit.InlineRun
	for _, run := range w.view.Runs {
		if run.Code {
			code = run
		}
	}
	if code.End == 0 {
		t.Fatal("missing code run")
	}
	spans := docInlineSpans(w.view, w.theme)
	found := false
	for _, sp := range spans {
		if sp.Text == "中文😀" {
			found = true
			if sp.Font != w.theme.CodeFont || sp.Size != w.theme.FontSize*w.theme.CodeSize || sp.Background != w.theme.CodeBackground {
				t.Fatalf("wrong native code style: %+v", sp)
			}
		}
	}
	if !found {
		t.Fatal("missing shaped code")
	}
	// Hit-test the same retained geometry the focused editor paints.
	index := ui.UTF16Len(w.view.Text[:code.Start]) + 1
	caret := w.caret(index)
	hit, _ := w.IndexForPoint(ui.Point{X: caret.X, Y: caret.Y + caret.H/2})
	if hit != index {
		t.Fatalf("caret/hit-test mismatch: %d != %d", hit, index)
	}
	bounds, ok := tt.Find("doc-canvas-editor")
	if !ok {
		t.Fatal("missing editor")
	}
	tt.ClickAt(bounds.X+caret.X, bounds.Y+caret.Y+caret.H/2)
	tt.Type("新")
	tt.Frame()
	want := strings.Replace(src, "中文😀", "中新文😀", 1)
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("native input changed source: %q", got)
	}
	// The task glyph uses the same row geometry; one click changes only [ ].
	tt.ClickAt(bounds.X+w.rows[0].x-5, bounds.Y+w.rows[0].h/2)
	tt.Frame()
	want = strings.Replace(want, "[ ]", "[x]", 1)
	if got := tab.DocEditor.Buffer.String(); got != want {
		t.Fatalf("checkbox: %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != strings.Replace(want, "[x]", "[ ]", 1) {
		t.Fatalf("checkbox undo: %q", got)
	}
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	disk, _, err := ps.project.Store.Read(tab.DocRel)
	if err != nil {
		t.Fatal(err)
	}
	if string(disk) != tab.DocEditor.Buffer.String() {
		t.Fatal("save lost last input")
	}
}

func TestM01NativeListStructureAndInlineMention(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M01 Structure")
	req := addMentionRequest(t, ps, "auth", "用户登录", "GET", "/login")
	src := "3) **one**\n4) two\n"
	tab := openDocForTest(t, ps, "docs/structure.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	be.ed.Select(be.ed.Buffer.Len(), be.ed.Buffer.Len())
	tt.Key(0, ui.KeyEnter)
	tt.Type("three")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "3) **one**\n4) two\n5) three\n" {
		t.Fatalf("continuation=%q", got)
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !strings.Contains(tab.DocEditor.Buffer.String(), "  5) three") {
		t.Fatalf("indent=%q", tab.DocEditor.Buffer.String())
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if strings.Contains(tab.DocEditor.Buffer.String(), "  5)") {
		t.Fatal("outdent failed")
	}
	tt.Type(" @")
	tt.Frame()
	if !tt.HasText("用户登录") {
		t.Fatalf("native mention missing: %v", tt.Texts())
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.Contains(tab.DocEditor.Buffer.String(), "gorequest://request/"+req.Request.ID) {
		t.Fatalf("mention=%q", tab.DocEditor.Buffer.String())
	}
}

func TestM01NativeCompositionDoesNotReparseOrCommitPreedit(t *testing.T) {
	ed := &editor.State{Buffer: editor.NewBuffer("`中文😀`"), Theme: editor.DefaultTheme(ui.LightTheme())}
	w := &docRichInput{ed: ed, font: ui.Font{Size: 14, LineHeight: 1.5}, theme: markdown.DefaultTheme(ui.LightTheme())}
	w.layout(240)
	w.selectVisible(1, 1)
	original := ed.Buffer.String()
	originalView := w.view
	w.SetMarkedText(nil, "新`文", ui.TextInputRange{Start: 3, End: 3})
	styles := w.styledSpans()
	codePreedit := false
	for _, sp := range styles {
		if sp.Text == "新`文" && sp.Font == w.theme.CodeFont && sp.Background == w.theme.CodeBackground {
			codePreedit = true
		}
	}
	if !codePreedit {
		t.Fatal("preedit lost inline code style")
	}
	if ed.Buffer.String() != original || w.view.Text != originalView.Text {
		t.Fatal("preedit reparsed canonical input")
	}
	if r, ok := w.MarkedRange(); !ok || r.End-r.Start != 3 {
		t.Fatalf("marked range %+v %v", r, ok)
	}
	if w.input(ui.Services{}, ui.InputEvent{Kind: ui.InputKeyDown, Key: ui.KeyEnter}) {
		t.Fatal("candidate Enter consumed")
	}
	w.ReplaceText(nil, "新文")
	if got := ed.Buffer.String(); got != "`中新文文😀`" {
		t.Fatalf("composition commit=%q", got)
	}
}

func TestM01TableThemeFocusGeometryAndScreenshots(t *testing.T) {
	dir, err := os.MkdirTemp("", "gorequest-M01-visuals-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("native focus images: %s", dir)
	for _, dark := range []bool{false, true} {
		for _, size := range []float32{14, 21} {
			for _, window := range [][2]int{{1200, 820}, {1024, 640}} {
				fontSize := size
				source := "| **标题** | Code |\n| --- | ---: |\n| 中文长文本保持一致 | `code span` |"
				var state *docTableState
				var th *markdown.Theme
				view := func(c *ui.Context) {
					th = markdown.DefaultTheme(c.Theme())
					th.FontSize = fontSize
					th.LineHeight = 1.7
					root := ui.Column(c).FillWidth().Padding(24).Label("document")
					root.Children(func() {
						markdown.View(c, source, &markdown.Options{Theme: th}).FillWidth().Label("preview")
						table := ui.Column(c).FillWidth()
						renderDocTableStyled(c, c.Theme(), table, source, func(s string) { source = s }, th)
						state = ui.Local(table, docTableKey{}, func() docTableState { return docTableState{} })
					})
				}
				tt := ui.NewTester(view, window[0], window[1])
				tt.SetDark(dark)
				tt.Frame()
				tt.Frame()
				w := state.rich[1][1]
				width := state.widths[1]
				height := state.rowHeight(1)
				before, _ := tt.Find("Table cell 2,2")
				saveM01Image(t, tt, filepath.Join(dir, fmt.Sprintf("%v-%g-%dx%d-idle.png", dark, size, window[0], window[1])))
				if err := tt.Click("Table cell 2,2"); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				after, _ := tt.Find("Table cell 2,2")
				if before != after || width != state.widths[1] || height != state.rowHeight(1) || w != state.rich[1][1] {
					t.Fatalf("focus changed layout: %+v => %+v", before, after)
				}
				if w.font.Size != th.FontSize || w.font.LineHeight != th.LineHeight || w.theme.Font != th.Font {
					t.Fatal("table lost document typography")
				}
				if !tt.Focused("Table cell 2,2") {
					t.Fatal("cell not focused")
				}
				if _, ok := tt.TextCaret(); !ok {
					t.Fatal("no native caret")
				}
				saveM01Image(t, tt, filepath.Join(dir, fmt.Sprintf("%v-%g-%dx%d-focused.png", dark, size, window[0], window[1])))
				// A style change invalidates widths, while preserving input identity/selection.
				selection := w.ed.Selection()
				fontSize *= 1.25
				tt.Frame()
				if w != state.rich[1][1] || selection != w.ed.Selection() || w.font.Size != fontSize || state.widths[1] == width {
					t.Fatal("metrics invalidation reset input")
				}
			}
		}
	}
}
func saveM01Image(t *testing.T, tt *ui.Tester, path string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := png.Encode(f, tt.Image()); err != nil {
		t.Fatal(err)
	}
}

func TestM01TypingCompletedSyntaxKeepsSourceCaretAffinity(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M01 Affinity")
	tab := openDocForTest(t, ps, "docs/affinity.md", "plain\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, "plain\n", 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Type(" `code`")
	tt.Frame()
	tt.Type(" after")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "plain `code` after\n" {
		t.Fatalf("closed delimiter affinity=%q", got)
	}
	if w := observed[tab].live.block.rich; w == nil || w.view.Text != "plain code after" {
		t.Fatal("completed inline did not become native rich editing")
	}
}

func TestM01ActiveListThemeScreenshots(t *testing.T) {
	observed := observeDocStates(t)
	dir, err := os.MkdirTemp("", "gorequest-M01-list-visuals-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("active list images: %s", dir)
	for _, dark := range []bool{false, true} {
		for _, percent := range []int{100, 150} {
			for _, window := range [][2]int{{1200, 820}, {1024, 640}} {
				a, ps := openTestProject(t, "M01 List Visual")
				if err := a.mgr.workspacePrefs().setMarkdownPolicy(markdownPreferences{FontPercent: percent, Padding: 24, LinePercent: 170}); err != nil {
					t.Fatal(err)
				}
				src := "- [ ] 正在编辑 `inline code` 和 **粗体** [链接](https://example.com)\n  - 嵌套列表：" + strings.Repeat("自动换行后仍与正文对齐。", 12) + "\n    续行内容\n- [x] 已完成\n"
				tab := openDocForTest(t, ps, "docs/list.md", src)
				tt := ui.NewTester(a.View, window[0], window[1])
				tt.SetDark(dark)
				tt.Frame()
				if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				w := observed[tab].live.block.rich
				if w == nil || !tt.Focused("doc-canvas-editor") {
					t.Fatal("active native editor missing")
				}
				if len(w.rows[1].layout.Lines()) < 2 {
					t.Fatal("wrapping was not exercised")
				}
				continuation := strings.Index(w.view.Text, "续行内容")
				if continuation < 0 || continuation >= ui.UTF16ByteOffset(w.view.Text, w.rows[1].end) || w.rows[1].sourceLine != 1 {
					t.Fatal("soft continuation is not in the nested item's native layout")
				}
				for _, line := range w.rows[1].layout.Lines() {
					if line.Rect.X < 0 {
						t.Fatal("wrapped continuation escapes the item gutter")
					}
				}
				saveM01Image(t, tt, filepath.Join(dir, fmt.Sprintf("%v-%d-%dx%d-active.png", dark, percent, window[0], window[1])))
			}
		}
	}
}

func TestM01FirstActivationHitTestAndFormatReflow(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M01 Initial Hit")
	src := "before `code` after\n"
	tab := openDocForTest(t, ps, "docs/hit.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	block, _ := tt.Find(canvasBlockLabel(t, src, 0))
	// Place the first click inside code using the same native span metrics.
	th := a.docMarkdownTheme(ui.LightTheme())
	font := a.docBlockFont(ui.LightTheme(), docedit.Parse(src)[0].Kind, 0)
	font.LineHeight = th.LineHeight
	geometry := ui.ShapeRichText(docInlineSpans(docedit.NewInlineView(strings.TrimSuffix(src, "\n")), th), font, block.W-16)
	pt := geometry.Caret(ui.UTF16Len("before co"))
	tt.ClickAt(block.X+8+pt.X, block.Y+2+pt.Y+pt.H/2)
	tt.Frame()
	be := observed[tab].live.block
	if got := be.ed.Cursor(); got != len("before `co") {
		t.Fatalf("first click source caret=%d, want %d", got, len("before `co"))
	}
	tt.Type("X")
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "before `coXde` after\n" {
		t.Fatalf("first click input=%q", got)
	}
	// Inline formatting changes style without changing visible text. The
	// retained layout must invalidate on source/run changes, not just text.
	be.ed.Select(0, len("before"))
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Frame()
	if len(be.rich.view.Runs) == 0 || !be.rich.view.Runs[0].Strong {
		t.Fatal("format lost strong run")
	}
	if be.rich.lastSource != be.ed.Buffer.String() {
		t.Fatal("styled geometry did not invalidate")
	}
}

func TestM01TableRichInputPreservesCodeAndLayout(t *testing.T) {
	h, tt := newDocTableHarness(t, "| title |\n| --- |\n| `中文😀` |")
	if err := tt.Click("Table cell 2,1"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("更新😀")
	tt.Frame()
	if h.source != "| title |\n| --- |\n| `更新😀` |" {
		t.Fatalf("code cell source=%q", h.source)
	}
}

func TestM01ListBoundaryActions(t *testing.T) {
	for _, tc := range []struct {
		source string
		caret  int
		mods   ui.Modifiers
		key    ui.Key
		want   string
	}{
		{"- first\n- \n", len("first\n"), 0, ui.KeyEnter, "- first\n\n"},
		{"- first\n- second\n", 0, 0, ui.KeyBackspace, "first\n- second\n"},
		{"- first\n- second\n  - child\n    continuation\n", len("first\nsecond"), 0, ui.KeyTab, "- first\n  - second\n    - child\n      continuation\n"},
	} {
		t.Run(fmt.Sprintf("%d-%d", tc.key, tc.caret), func(t *testing.T) {
			observed := observeDocStates(t)
			a, ps := openTestProject(t, "M01 Boundaries")
			tab := openDocForTest(t, ps, "docs/boundary.md", tc.source)
			tt := ui.NewTester(a.View, 1200, 820)
			tt.Frame()
			if err := tt.Click(canvasBlockLabel(t, tc.source, 0)); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			be := observed[tab].live.block
			be.ed.Select(tc.caret, tc.caret)
			tt.Key(tc.mods, tc.key)
			tt.Frame()
			if got := tab.DocEditor.Buffer.String(); got != tc.want {
				t.Fatalf("structure=%q, want %q", got, tc.want)
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			tt.Frame()
			if got := tab.DocEditor.Buffer.String(); got != tc.source {
				t.Fatalf("structure undo=%q", got)
			}
		})
	}
}

func TestM01LongOrderedMarkersReserveRealGutterWidth(t *testing.T) {
	src := "123456789) **first**\n123456790) second\n"
	projection := docedit.NewProjection(docedit.Parse(src)[0])
	th := markdown.DefaultTheme(ui.LightTheme())
	w := &docRichInput{ed: &editor.State{Buffer: editor.NewBuffer(projection.Text())}, font: ui.Font{Size: th.FontSize, LineHeight: th.LineHeight}, theme: th, prefixes: projection.ListLines(projection.Text())}
	w.layout(500)
	for _, row := range w.rows {
		markerWidth, _ := row.markerLayout.Size()
		if row.x < markerWidth+th.FontSize*0.4 {
			t.Fatalf("number overlaps editable content: gutter %v, marker %v", row.x, markerWidth)
		}
	}
	if w.rows[0].x != w.rows[1].x {
		t.Fatal("ordered items do not share gutter geometry")
	}
}
