package desktop

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

func TestM02NativeCodeEditing(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M02 Native")
	src := "```golang title=keep\nfunc main() {\n\tprintln(1)\n}\n```\n\nafter\n"
	tab := openDocForTest(t, ps, "docs/code.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	before, _ := tt.Find("doc-canvas-editor")
	tt.ClickAt(before.X+4, before.Y+2)
	tt.Frame()
	tt.Frame()
	be := observed[tab].live.block
	if be == nil || be.rich != nil || be.ed.Buffer.Language() != editor.LanguageNamed("go") {
		t.Fatal("code did not activate native language editor")
	}
	if be.ed.Cursor() != 0 {
		t.Fatalf("first click moved to %d", be.ed.Cursor())
	}
	after, _ := tt.Find("doc-canvas-editor")
	if before != after {
		t.Fatalf("focus moved body: %v => %v", before, after)
	}
	be.ed.Select(len("func main() {\n\tprintln(1)"), len("func main() {\n\tprintln(1)"))
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if !strings.Contains(be.ed.Buffer.String(), "println(1)\n\t\n}") {
		t.Fatalf("Enter lost indentation: %q", be.ed.Buffer.String())
	}
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !strings.Contains(be.ed.Buffer.String(), "println(1)\n\t\t\n}") {
		t.Fatalf("Tab = %q", be.ed.Buffer.String())
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if !strings.Contains(be.ed.Buffer.String(), "println(1)\n\t\n}") {
		t.Fatalf("Shift Tab = %q", be.ed.Buffer.String())
	}
	be.ed.Select(0, be.ed.Buffer.Len())
	tt.Frame()
	original := be.ed.Buffer.String()
	tt.Key(ui.Cmd, ui.KeyB)
	tt.Key(ui.Cmd, ui.KeyI)
	tt.Command("bold")
	tt.Frame()
	if be.ed.Buffer.String() != original {
		t.Fatal("format command polluted code")
	}
	tt.Type("# * / @ ` [x](url)\n```\n中文😀")
	tt.Frame()
	got := tab.DocEditor.Buffer.String()
	if !strings.HasPrefix(got, "````golang title=keep\n") || !strings.Contains(got, "\n```\n中文😀\n````\n\nafter\n") {
		t.Fatalf("collision corrupted document: %q", got)
	}
	if observed[tab].live.slashOpen || be.ed.OnCompletion != nil {
		t.Fatal("code installed prose completion")
	}
	a.docBlockUndo(tab, observed[tab], false)
	tt.Frame()
	if tab.DocEditor.Buffer.String() == got {
		t.Fatal("undo did not change canonical buffer")
	}
	a.docBlockUndo(tab, observed[tab], true)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != got {
		t.Fatal("redo lost collision transaction")
	}
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	saved, _, err := ps.project.Store.Read(tab.DocRel)
	if err != nil || string(saved) != got {
		t.Fatalf("save = %q %v", saved, err)
	}
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if observed[tab].live.block != nil {
		t.Fatal("Escape did not exit")
	}
}

func TestM02CodeThemeFocusScreenshots(t *testing.T) {
	dir, err := os.MkdirTemp("", "gorequest-M02-visuals-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("code images: %s", dir)
	for _, dark := range []bool{false, true} {
		for _, zoom := range []float32{1, 1.5} {
			for _, win := range [][2]int{{1200, 820}, {1024, 640}} {
				observed := observeDocStates(t)
				a, ps := openTestProject(t, "M02 Visual")
				// The same preferences used by the product's document zoom.
				if err := a.mgr.workspacePrefs().setMarkdownPolicy(markdownPreferences{FontPercent: int(100 * zoom), Padding: 24, LinePercent: 170}); err != nil {
					t.Fatal(err)
				}
				src := "```go\npackage main\n\nfunc main() {\n\tprintln(\"中文 code\", 42) // comment\n}\n```\n"
				tab := openDocForTest(t, ps, "docs/visual.md", src)
				tt := ui.NewTester(a.View, win[0], win[1])
				tt.SetDark(dark)
				tt.Frame()
				tt.Frame()
				before, _ := tt.Find("doc-canvas-editor")
				name := fmt.Sprintf("%v-%g-%dx%d", dark, zoom, win[0], win[1])
				saveM01Image(t, tt, filepath.Join(dir, name+"-idle.png"))
				tt.ClickAt(before.X+5, before.Y+3)
				tt.Frame()
				tt.Frame()
				be := observed[tab].live.block
				if be == nil {
					t.Fatal("no active code editor")
				}
				after, _ := tt.Find("doc-canvas-editor")
				if before != after {
					t.Fatalf("body shifted %v => %v", before, after)
				}
				if _, ok := tt.TextCaret(); !ok {
					t.Fatal("no native caret")
				}
				th := a.docMarkdownTheme(ttTheme(dark))
				if be.ed.Theme.Tokens != th.CodeColors.Tokens {
					t.Fatal("syntax colors differ from preview")
				}
				// Inspect painted pixels too: assigning a lexer/theme alone is
				// insufficient evidence that the active editor renders syntax color.
				color := th.CodeColors.TokenColor(editor.TokenKeyword)
				pixels := 0
				img := tt.Image()
				for y := int(after.Y); y < int(after.Y+after.H); y++ {
					for x := int(after.X); x < int(after.X+after.W); x++ {
						p := img.RGBAAt(x, y)
						if p.R == color.R && p.G == color.G && p.B == color.B {
							pixels++
						}
					}
				}
				if pixels < 3 {
					t.Fatalf("keyword color not painted: %v (%d pixels)", color, pixels)
				}
				saveM01Image(t, tt, filepath.Join(dir, name+"-active.png"))
			}
		}
	}
}
func ttTheme(dark bool) *ui.Theme {
	if dark {
		return ui.DarkTheme()
	}
	return ui.LightTheme()
}

func TestM02CodeScrollCopyCompositionAndBoundaries(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M02 Scroll")
	body := "const value = \"" + strings.Repeat("abcdefgh", 70) + "\"\nsecond line\nlast line"
	src := "```js\n" + body + "\n```\n\nafter\n"
	tab := openDocForTest(t, ps, "docs/scroll.md", src)
	tt := ui.NewTester(a.View, 1024, 640)
	tt.Frame()
	tt.Frame()
	if err := tt.Click("doc-code-copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Clipboard() != body || tab.Dirty || observed[tab].live.block != nil {
		t.Fatal("copy activated/dirtied or copied fences")
	}
	head, _ := tt.Find("doc-code-header")
	tt.ClickAt(head.X+15, head.Y+5)
	tt.Frame()
	if observed[tab].live.block != nil {
		t.Fatal("language header activated code")
	}
	r, _ := tt.Find("doc-canvas-editor")
	tt.ClickAt(r.X+5, r.Y+3)
	tt.Frame()
	tt.Frame()
	be := observed[tab].live.block
	if be == nil {
		t.Fatal("no code editor")
	}
	be.ed.Select(0, 0)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src {
		t.Fatal("start Backspace removed fence")
	}
	be.ed.Select(len(body), len(body))
	tt.Frame()
	tt.Key(0, ui.KeyDelete)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src {
		t.Fatal("end Delete merged prose")
	}
	be.ed.Select(0, 0)
	tt.Frame()
	tt.Scroll(r.X+50, r.Y+5, 400, 0)
	tt.Frame()
	tt.ClickAt(r.X+5, r.Y+3)
	tt.Frame()
	if be.ed.Cursor() < 10 {
		t.Fatalf("scrolled hit ignored scroll: %d", be.ed.Cursor())
	}
	// Native keyboard reveal brings the selected position back into view.
	be.ed.Select(0, 0)
	tt.Frame()
	tt.Compose("zhong", 5)
	tt.Frame()
	tt.Compose("中文", 2)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src {
		t.Fatal("preedit changed canonical text")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src {
		t.Fatal("candidate Enter inserted newline")
	}
	tt.Type("中文")
	tt.Compose("", 0)
	tt.Frame()
	if !strings.Contains(tab.DocEditor.Buffer.String(), "\n中文const") {
		t.Fatalf("composition commit = %q", tab.DocEditor.Buffer.String())
	}
	committed := tab.DocEditor.Buffer.String()
	a.docBlockUndo(tab, observed[tab], false)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != src {
		t.Fatal("composition was not one canonical undo step")
	}
	a.docBlockUndo(tab, observed[tab], true)
	tt.Frame()
	if tab.DocEditor.Buffer.String() != committed {
		t.Fatal("composition redo lost commit")
	}
	be.ed.Select(0, be.ed.Buffer.Len())
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !strings.HasPrefix(be.ed.Buffer.String(), "    中文const") || !strings.Contains(be.ed.Buffer.String(), "\n    second") {
		t.Fatalf("multiline indent %q", be.ed.Buffer.String())
	}
	tt.Key(ui.Shift, ui.KeyTab)
	tt.Frame()
	if strings.HasPrefix(be.ed.Buffer.String(), " ") {
		t.Fatal("multiline outdent failed")
	}
	if err := tt.Click("doc-code-copy"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.Clipboard() != be.ed.Buffer.String() {
		t.Fatal("active copy lost pending input")
	}
	if len(tt.OpenedURLs()) != 0 {
		t.Fatal("code opened a URL")
	}
	be.ed.Select(be.ed.Buffer.Len(), be.ed.Buffer.Len())
	be.ed.Insert("last stroke")
	if err := a.saveTabSynced(tab); err != nil {
		t.Fatal(err)
	}
	saved, _, err := ps.project.Store.Read(tab.DocRel)
	if err != nil || !strings.Contains(string(saved), "last stroke") {
		t.Fatal("save lost unframed input")
	}
	be.ed.Insert(" mode stroke")
	a.flushDocCanvas(tab, observed[tab])
	if !strings.Contains(tab.DocEditor.Buffer.String(), " mode stroke") {
		t.Fatal("mode flush lost last input")
	}

}

func TestM02NativeCompositionContract(t *testing.T) {
	// This deliberately exercises the locked MyGo composition bridge on a
	// zero state too; a dependency layout change must fail this contract.
	if docCodeComposing(&editor.State{}) {
		t.Fatal("zero editor is composing")
	}
}

func TestM02CodeCreationEmptyIndentedAndUnknown(t *testing.T) {
	for _, src := range []string{"```\n```\n", "```go\n", "~~~unknown attr=x\nx\n~~~", "    one\n    two\n"} {
		observed := observeDocStates(t)
		a, ps := openTestProject(t, "M02 Sources")
		tab := openDocForTest(t, ps, "docs/source.md", src)
		tt := ui.NewTester(a.View, 1200, 820)
		tt.Frame()
		tt.Frame()
		r, ok := tt.Find("doc-canvas-editor")
		if !ok {
			t.Fatalf("no editor for %q", src)
		}
		tt.ClickAt(r.X+5, r.Y+2)
		tt.Frame()
		tt.Frame()
		be := observed[tab].live.block
		if be == nil {
			t.Fatalf("no active editor for %q", src)
		}
		if tab.DocEditor.Buffer.String() != src {
			t.Fatalf("activation mutated %q", src)
		}
		be.ed.Select(0, 0)
		tt.Frame()
		tt.Type("中")
		tt.Frame()
		if !strings.HasPrefix(be.ed.Buffer.String(), "中") {
			t.Fatalf("edit lost for %q", src)
		}
		if err := a.saveTabSynced(tab); err != nil {
			t.Fatal(err)
		}
	}
	// Start with a normal prose input, then create an open fence by typing.
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M02 Creation")
	tab := openDocForTest(t, ps, "docs/create.md", "plain\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	if err := tt.Click(canvasBlockLabel(t, "plain\n", 0)); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	be := observed[tab].live.block
	be.ed.Select(0, be.ed.Buffer.Len())
	tt.Frame()
	tt.Type("```go")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	tt.Type("func main() {}")
	tt.Frame()
	be = observed[tab].live.block
	if be == nil || be.rich != nil || be.ed.SoftWrap || be.ed.Buffer.Language() != editor.LanguageNamed("go") || be.ed.OnCompletion != nil {
		t.Fatal("new fence kept prose editor/hooks")
	}
	if tab.DocEditor.Buffer.String() != "```go\nfunc main() {}\n" {
		t.Fatal("new code body lost")
	}
}

func TestM02CodeLanguageAliasesAndIndentStyle(t *testing.T) {
	for _, name := range []string{"go", "golang", "json", "js", "javascript", "unknown", ""} {
		if got := docCodeLanguage("````" + name + " attr=x"); name != "" && got != name {
			t.Fatalf("language %q => %q", name, got)
		}
	}
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M02 Indent")
	tab := openDocForTest(t, ps, "docs/indent.md", "```json\n{\n  \"x\": 1\n}\n```\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	r, _ := tt.Find("doc-canvas-editor")
	tt.ClickAt(r.X+5, r.Y+3)
	tt.Frame()
	tt.Frame()
	be := observed[tab].live.block
	if be.ed.TabSize != 2 || be.ed.Buffer.Language() != editor.LanguageNamed("json") {
		t.Fatal("lost JSON indent/language")
	}
	be.ed.Select(0, 0)
	tt.Frame()
	tt.Key(0, ui.KeyTab)
	tt.Frame()
	if !strings.HasPrefix(be.ed.Buffer.String(), "  {") {
		t.Fatalf("did not retain space unit: %q", be.ed.Buffer.String())
	}
	observed[tab].live.canvasOpts.WrapCode = true
	tt.Frame()
	if !be.ed.SoftWrap {
		t.Fatal("WrapCode was ignored")
	}
}

func TestM02CodeLineHitsDragAndKeyboardSelection(t *testing.T) {
	observed := observeDocStates(t)
	a, ps := openTestProject(t, "M02 Hits")
	tab := openDocForTest(t, ps, "docs/hits.md", "```\nzero\nmiddle\nlast\n```\n")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	r, _ := tt.Find("doc-canvas-editor")
	for line, off := range []int{0, 5, 12} {
		idle := observed[tab].live.codeBlocks[0]
		y := r.Y + float32(idle.ed.LineTop(line)) + 2
		tt.ClickAt(r.X+5, y)
		tt.Frame()
		tt.Frame()
		be := observed[tab].live.block
		if be == nil || be.ed.Cursor() != off {
			t.Fatalf("line %d hit = %+v", line, be)
		}
		tt.Key(0, ui.KeyEscape)
		tt.Frame()
	}
	tt.ClickAt(r.X+5, r.Y+2)
	tt.Frame()
	tt.Frame()
	be := observed[tab].live.block
	tt.Advance(time.Second) // begin an ordinary drag, outside the double-click interval
	tt.Press(r.X+5, r.Y+2)
	tt.Move(r.X+5, r.Y+float32(be.ed.LineTop(2))+2)
	tt.Release(r.X+5, r.Y+float32(be.ed.LineTop(2))+2)
	tt.Frame()
	if be.ed.SelectedText() != "zero\nmiddle\n" {
		t.Fatalf("drag = %q", be.ed.SelectedText())
	}
	be.ed.Select(0, 0)
	tt.Frame()
	tt.Key(ui.Shift, ui.KeyRight)
	tt.Frame()
	if be.ed.SelectedText() != "z" {
		t.Fatalf("keyboard selection = %q", be.ed.SelectedText())
	}
}

func TestM02CompositionMarksDirtyOnlyOnCommit(t *testing.T) {
	a, ps := openTestProject(t, "M02 Composition Dirty")
	src := "```go\nword\n```\n"
	tab := openDocForTest(t, ps, "docs/compose.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	r, _ := tt.Find("doc-canvas-editor")
	tt.ClickAt(r.X+5, r.Y+2)
	tt.Frame()
	tt.Frame()
	tt.Compose("zhong", 5)
	tt.Frame()
	if tab.Dirty || tab.DocEditor.Buffer.String() != src {
		t.Fatal("preedit dirtied source")
	}
	tt.Type("中")
	tt.Compose("", 0)
	tt.Frame()
	if !tab.Dirty || tab.DocEditor.Buffer.String() != "```go\n中word\n```\n" {
		t.Fatal("native composition commit did not mark document dirty")
	}
}
