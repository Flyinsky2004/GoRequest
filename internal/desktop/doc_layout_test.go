package desktop

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/docedit"
)

func TestM03ReadmeFocusLayout(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M03 Layout")
	data, err := os.ReadFile("../../examples/sample-project/.gorequest/docs/README.md")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "`POST /echo` 携带")
	end := strings.Index(string(data)[start:], "\n\n") + start
	src := string(data)[start:end] + "\n\n## after\n"
	tab := openDocForTest(t, ps, "docs/layout.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	label := canvasBlockLabel(t, src, 0)
	before, _ := tt.Find(label)
	afterLabel := canvasBlockLabel(t, src, 1)
	next, _ := tt.Find(afterLabel)
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	active, _ := tt.Find("doc-canvas-editor")
	nextActive, _ := tt.Find(afterLabel)
	t.Logf("idle=%v active=%v next=%v -> %v rows=%d", before, active, next, nextActive, len(observed[tab].live.block.rich.rows))
	if next.Y != nextActive.Y {
		t.Fatalf("focus moved following heading: %v -> %v", next, nextActive)
	}
}

type m03Layout struct {
	Bounds       ui.Rect
	Lines        []ui.TextLine
	Starts, Ends []int
	Origins      []ui.Point
}

func m03Snapshot(w *docRichInput, b ui.Rect) m03Layout {
	b.H = float32(math.Round(float64(b.H)*1024) / 1024) // only layout-coordinate rounding
	s := m03Layout{Bounds: b}
	for _, r := range w.rows {
		s.Starts = append(s.Starts, r.start)
		s.Ends = append(s.Ends, r.end)
		s.Origins = append(s.Origins, ui.Point{X: r.x, Y: r.y})
		s.Lines = append(s.Lines, r.layout.Lines()...)
	}
	return s
}
func m03Readme(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../examples/sample-project/.gorequest/docs/README.md")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(data), "`POST /echo` 携带")
	end := strings.Index(string(data)[start:], "\n\n") + start
	return string(data)[start:end]
}
func TestM03FocusFramesAndScreenshots(t *testing.T) {
	dir, err := os.MkdirTemp("", "gorequest-M03-visuals-")
	if err != nil {
		t.Fatal(err)
	}
	t.Log(dir)
	fixtures := []struct{ name, text string }{
		{"readme", m03Readme(t)},
		{"plain", "中文😀 plain paragraph " + strings.Repeat("临界换行 text，", 20)},
		{"mixed", "**中文😀 strong** [link](https://example.com) 与 `code` 相邻。\n第二条源码 softbreak\n第三条。"},
		{"hard", "第一行  \n第二行\\\n第三行"},
		{"crlf", "中文😀 `code`\r\nsoft continuation\r\nend"},
		{"quote", "> 引用 **中文😀**\n> soft continuation\n>\n> second paragraph"},
		{"list", "3) **parent**\n   continuation\n   - child `代码`\n     soft continuation\n\n4) loose item"},
	}
	for level := 1; level <= 6; level++ {
		fixtures = append(fixtures, struct{ name, text string }{fmt.Sprintf("heading%d", level), strings.Repeat("#", level) + " 标题 `code` **strong**"})
	}
	for _, fixture := range fixtures {
		for _, dark := range []bool{false, true} {
			for _, percent := range []int{100, 150} {
				for _, win := range [][2]int{{1200, 820}, {1024, 640}} {
					name := fmt.Sprintf("%s-%v-%d-%dx%d", fixture.name, dark, percent, win[0], win[1])
					t.Run(name, func(t *testing.T) {
						observed := observeDocStates(t)
						a, ps := openTestProject(t, "M03 Frames")
						if err := a.mgr.workspacePrefs().setMarkdownPolicy(markdownPreferences{FontPercent: percent, Padding: 24, LinePercent: 170}); err != nil {
							t.Fatal(err)
						}
						sep := "\n"
						if strings.Contains(fixture.text, "\r\n") {
							sep = "\r\n"
						}
						src := fixture.text + sep + sep + "## after" + sep
						tab := openDocForTest(t, ps, "docs/frames.md", src)
						tt := ui.NewTester(a.View, win[0], win[1])
						tt.SetDark(dark)
						tt.Frame()
						tt.Frame()
						st := observed[tab]
						be := st.live.textBlocks[0]
						if be == nil || be.rich == nil {
							t.Fatal("missing shared native layout")
						}
						w := be.rich
						label := canvasBlockLabel(t, src, 0)
						nextLabel := canvasBlockLabel(t, src, 1)
						next, _ := tt.Find(nextLabel)
						bounds, _ := tt.Find("doc-text:0")
						want := m03Snapshot(w, bounds)
						imageBefore := tt.Image()
						saveM01Image(t, tt, filepath.Join(dir, name+"-idle.png"))
						var frames int
						docRichLayoutObserve = func(got *docRichInput, b ui.Rect) {
							if got != w {
								return
							}
							frames++
							if snap := m03Snapshot(got, b); !reflect.DeepEqual(snap, want) {
								t.Errorf("frame %d text layout changed:\n%+v\n%+v", frames, want, snap)
							}
						}
						defer func() { docRichLayoutObserve = nil }()
						// Target an actual glyph, using the idle layout and its exact source mapping.
						at := 2
						caret := w.caret(at)
						expected := w.view.SourceOffset(ui.UTF16ByteOffset(w.view.Text, at))
						tt.ClickAt(bounds.X+caret.X, bounds.Y+caret.Y+caret.H/2)
						tt.Frame()
						if st.live.block != be || !tt.Focused("doc-canvas-editor") {
							t.Fatalf("click did not activate shared input %s", label)
						}
						if be.ed.Cursor() != expected {
							t.Fatalf("first click offset=%d want %d", be.ed.Cursor(), expected)
						}
						if nextActive, _ := tt.Find(nextLabel); nextActive != next {
							t.Fatalf("next heading shifted %v => %v", next, nextActive)
						}
						if tab.Dirty || tab.DocEditor.Buffer.CanUndo() || tab.DocEditor.Buffer.String() != strings.ReplaceAll(src, "\r\n", "\n") {
							t.Fatal("focus changed source/history")
						}
						saveM01Image(t, tt, filepath.Join(dir, name+"-active.png"))
						// Painted glyph pixels must stay put too, beyond equal host bounds and
						// retained line ends/baselines. Only the real caret may differ.
						imageAfter := tt.Image()
						caret, _ = tt.TextCaret()
						changed := 0
						for y := int(bounds.Y); y < int(bounds.Y+bounds.H); y++ {
							for x := int(bounds.X); x < int(bounds.X+bounds.W); x++ {
								if float32(x) >= caret.X-2 && float32(x) <= caret.X+caret.W+2 && float32(y) >= caret.Y-2 && float32(y) <= caret.Y+caret.H+2 {
									continue
								}
								if imageBefore.RGBAAt(x, y) != imageAfter.RGBAAt(x, y) {
									changed++
								}
							}
						}
						if changed != 0 {
							t.Errorf("%d non-caret text pixels changed on focus", changed)
						}
						tt.Key(0, ui.KeyEscape)
						tt.Frame()
						if st.live.block != nil {
							t.Fatal("escape did not blur")
						}
						tt.ClickAt(bounds.X+caret.W+10, bounds.Y+want.Lines[0].Rect.H/2)
						tt.Frame()
						if frames < 3 {
							t.Fatal("did not inspect focus/blur/refocus frames")
						}
						if tab.Dirty || tab.DocEditor.Buffer.CanUndo() {
							t.Fatal("blur/refocus changed history")
						}
						if err := a.saveTabSynced(tab); err != nil {
							t.Fatal(err)
						}
						disk, _, err := ps.project.Store.Read(tab.DocRel)
						if err != nil || string(disk) != src {
							t.Fatal("save changed untouched bytes", err)
						}
					})
				}
			}
		}
	}
}

func TestM03CriticalWrapWidth(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M03 Threshold")
	src := strings.Repeat("中文 `code` alpha ", 8) + "end\n\n## next\n"
	tab := openDocForTest(t, ps, "docs/threshold.md", src)
	tt := ui.NewTester(a.View, 1500, 820)
	tt.Frame()
	tt.Frame()
	st := observed[tab]
	w := st.live.textBlocks[0].rich
	natural, _ := ui.ShapeRichTextWith(docInlineSpans(w.view, w.theme), w.font, ui.TextShape{}).Size()
	for _, delta := range []float32{-1, 1} {
		tt.SetSize(1500+int(math.Round(float64(natural+delta-w.width))), 820)
		tt.Frame()
		bounds, _ := tt.Find("doc-text:0")
		want := m03Snapshot(w, bounds)
		count := len(w.rows[0].layout.Lines())
		if (delta < 0 && count != 2) || (delta > 0 && count != 1) {
			t.Fatalf("width=%g natural=%g delta=%g lines=%d", w.width, natural, delta, count)
		}
		if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		active, _ := tt.Find("doc-canvas-editor")
		if got := m03Snapshot(w, active); !reflect.DeepEqual(got, want) {
			t.Fatalf("critical width moved glyphs: %+v => %+v", want, got)
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
		// Reset to the measured starting window before deriving the next width.
		tt.SetSize(1500, 820)
		tt.Frame()
	}
}

func TestM03SoftBreakEditSaveAndHistory(t *testing.T) {
	for _, eol := range []string{"\n", "\r\n"} {
		t.Run(fmt.Sprintf("eol%q", eol), func(t *testing.T) {
			observed := observeDocStates(t)
			a, ps := openTestProject(t, "M03 Edit")
			src := strings.Join([]string{"中文😀 source", "second `code`", "third", "", "## after", ""}, eol)
			tab := openDocForTest(t, ps, "docs/edit.md", src)
			tt := ui.NewTester(a.View, 1200, 820)
			tt.Frame()
			if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			w := observed[tab].live.block.rich
			at := ui.UTF16Len("中文😀 source")
			w.selectVisible(at, at+1)
			tt.Key(0, ui.KeyBackspace)
			tt.Frame()
			want := strings.Replace(src, "source"+eol+"second", "sourcesecond", 1)
			if got := tab.DocEditor.Buffer.String(); got != strings.ReplaceAll(want, "\r\n", "\n") {
				t.Fatalf("delete normalized break=%q want %q", got, want)
			}
			if err := a.saveTabSynced(tab); err != nil {
				t.Fatal(err)
			}
			disk, _, err := ps.project.Store.Read(tab.DocRel)
			if err != nil || string(disk) != want {
				t.Fatal("save lost replacement", err)
			}
			// A fresh parse of the saved source must retain the same paragraph content.
			reopened := docedit.NewProjection(docedit.Parse(string(disk))[0])
			if docedit.NewInlineView(reopened.Text()).Text != w.view.Text {
				t.Fatal("reopen changed visible text")
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			tt.Frame()
			if tab.DocEditor.Buffer.String() != strings.ReplaceAll(src, "\r\n", "\n") {
				t.Fatal("undo did not restore original source bytes")
			}
			tt.Key(ui.Cmd|ui.Shift, ui.KeyZ)
			tt.Frame()
			if tab.DocEditor.Buffer.String() != strings.ReplaceAll(want, "\r\n", "\n") {
				t.Fatal("redo did not restore minimal replacement")
			}
			tt.Key(ui.Cmd, ui.KeyZ)
			tt.Frame()
			w = observed[tab].live.block.rich
			w.selectVisible(at, at+1)
			tt.Type(" /@ ")
			tt.Frame()
			want = strings.Replace(src, "source"+eol+"second", "source /@ second", 1)
			if got := tab.DocEditor.Buffer.String(); got != strings.ReplaceAll(want, "\r\n", "\n") {
				t.Fatalf("replace source break %q", got)
			}
		})
	}
}

func TestM03ScrollAnchorAndResize(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M03 Scroll")
	prefix := strings.Repeat("prefix paragraph 中文\n\n", 24)
	src := prefix + m03Readme(t) + "\n\n## after\n" + strings.Repeat("tail paragraph\n\n", 20)
	tab := openDocForTest(t, ps, "docs/scroll.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	st := observed[tab]
	tt.Scroll(800, 500, 0, 400)
	tt.Frame()
	if st.live.canvasScroll.Y <= 0 {
		t.Fatal("real canvas scroll was not exercised")
	}
	b := docedit.Parse(src)[24]
	label := docBlockLabel(b)
	bounds, _ := tt.Find(label)
	if bounds.Y < 150 || bounds.Y+bounds.H > 800 {
		t.Fatalf("target outside viewport: %v", bounds)
	}
	anchor := st.live.canvasScroll.Y
	w := st.live.textBlocks[b.Start].rich
	before := m03Snapshot(w, ui.Rect{X: bounds.X + 8, Y: bounds.Y + 2, W: bounds.W - 16, H: w.height})
	tt.ClickAt(bounds.X+10, bounds.Y+8)
	tt.Frame()
	if st.live.block == nil || st.live.block.start != b.Start {
		t.Fatal("did not activate scrolled paragraph")
	}
	if st.live.canvasScroll.Y != anchor {
		t.Fatalf("scroll anchor shifted %g -> %g", anchor, st.live.canvasScroll.Y)
	}
	active, _ := tt.Find("doc-canvas-editor")
	if got := m03Snapshot(w, active); !reflect.DeepEqual(got, before) {
		t.Fatalf("scrolled text moved %+v => %+v", before, got)
	}
	tt.SetSize(1024, 640)
	tt.Frame()
	width := w.width
	if width != active.W-176 {
		t.Fatalf("resize retained stale width %g", width)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	idle, _ := tt.Find(fmt.Sprintf("doc-text:%d", b.Start))
	if idle.W != width {
		t.Fatal("blur after resize changed available width")
	}
}

func TestM03ListPreeditRetainsFollowingItemGeometry(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M03 Preedit")
	src := "- first\n  continuation\n- second\n"
	tab := openDocForTest(t, ps, "docs/preedit.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	w := observed[tab].live.block.rich
	w.selectVisible(0, 0)
	tt.Compose("长长长长长长长长长长长长中文😀", 15)
	tt.Frame()
	if len(w.rows) != 2 || w.rows[1].sourceLine != 2 || w.rows[1].marker != "-" {
		t.Fatalf("preedit shifted next item source mapping: %+v", w.rows)
	}
	if tab.Dirty || tab.DocEditor.Buffer.String() != src {
		t.Fatal("composition wrote source")
	}
	tt.Type("新")
	tt.Frame()
	if tab.DocEditor.Buffer.String() != "- 新first\n  continuation\n- second\n" {
		t.Fatal(tab.DocEditor.Buffer.String())
	}
}

func TestM03NativeBreakSelectionPreservesAdjacentSyntax(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M03 Boundaries")
	src := "`first`\n**second**\nthird\n"
	tab := openDocForTest(t, ps, "docs/boundaries.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, src, 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	w := observed[tab].live.block.rich
	w.selectVisible(5, 6)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "`first`**second**\nthird\n" {
		t.Fatalf("selection ate hidden delimiter: %q", got)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src {
		t.Fatal("delimiter undo failed")
	}
	w = observed[tab].live.block.rich
	w.selectVisible(5, 5)
	tt.Key(0, ui.KeyDelete)
	tt.Frame()
	if got := tab.DocEditor.Buffer.String(); got != "`first`**second**\nthird\n" {
		t.Fatalf("delete ate hidden delimiter: %q", got)
	}
}
