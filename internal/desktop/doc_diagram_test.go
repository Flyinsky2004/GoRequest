package desktop

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/diagram"
	"gorequest/internal/docedit"
)

const diagramDocSrc = "# 图\n\n```mermaid\nflowchart TD\n  A[开始] --> B{判断}\n  B -->|是| C[完成]\n  B -->|否| D[其他]\n```\n"

func codeBlockStart(t *testing.T, src string, index int) int {
	t.Helper()
	blocks := docedit.Parse(src)
	if index < 0 || index >= len(blocks) {
		t.Fatalf("block %d of %d", index, len(blocks))
	}
	return blocks[index].Start
}

// inkCount counts pixels in r that differ from the rectangle's corner color,
// proving the native painter actually drew the diagram rather than leaving
// an empty box.
func inkCount(tt *ui.Tester, r ui.Rect) int {
	img := tt.Image()
	cx, cy := int(r.X)+1, int(r.Y)+1
	br, bg, bb, _ := img.At(cx, cy).RGBA()
	count := 0
	for y := int(r.Y); y < int(r.Y+r.H) && y < img.Rect.Dy(); y += 2 {
		for x := int(r.X); x < int(r.X+r.W) && x < img.Rect.Dx(); x += 2 {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			if absDiff(pr, br) > 0x1000 || absDiff(pg, bg) > 0x1000 || absDiff(pb, bb) > 0x1000 {
				count++
			}
		}
	}
	return count
}

func absDiff(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

// inkBounds returns the bounding box of pixels inside r that differ from the
// rectangle's corner colour. It proves the drawing fills the card rather than
// sitting in a mostly empty box.
func inkBounds(tt *ui.Tester, r ui.Rect) (ui.Rect, int) {
	img := tt.Image()
	cx, cy := int(r.X)+1, int(r.Y)+1
	br, bg, bb, _ := img.At(cx, cy).RGBA()
	minX, minY := int(r.X+r.W), int(r.Y+r.H)
	maxX, maxY := -1, -1
	count := 0
	for y := int(r.Y); y < int(r.Y+r.H) && y < img.Rect.Dy(); y++ {
		for x := int(r.X); x < int(r.X+r.W) && x < img.Rect.Dx(); x++ {
			pr, pg, pb, _ := img.At(x, y).RGBA()
			if absDiff(pr, br) > 0x1000 || absDiff(pg, bg) > 0x1000 || absDiff(pb, bb) > 0x1000 {
				count++
				minX, minY = min(minX, x), min(minY, y)
				maxX, maxY = max(maxX, x), max(maxY, y)
			}
		}
	}
	if count == 0 {
		return ui.Rect{}, 0
	}
	return ui.Rect{X: float32(minX), Y: float32(minY), W: float32(maxX - minX + 1), H: float32(maxY - minY + 1)}, count
}

func TestDocDiagramCanvasRendersAndTogglesSource(t *testing.T) {
	a, ps := openTestProject(t, "Mermaid Canvas")
	tab := openDocForTest(t, ps, "docs/diagram.md", diagramDocSrc)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	if tab.Section != docSectionCanvas {
		t.Fatalf("Section = %d, want canvas", tab.Section)
	}
	if !docDiagramWired.Load() {
		t.Skip("requires docs/task-results/F03-integration.patch (public doc_live/documentation hooks)")
	}

	start := codeBlockStart(t, diagramDocSrc, 1)
	host := canvasBlockLabel(t, diagramDocSrc, 1)
	if _, ok := tt.Find(host); !ok {
		t.Fatalf("diagram block missing; texts=%v", tt.Texts())
	}
	viewLabel := fmt.Sprintf("doc-diagram-view:%d", start)
	box, ok := tt.Find(viewLabel)
	if !ok {
		t.Fatalf("diagram view missing; texts=%v", tt.Texts())
	}
	if box.W <= 0 || box.H <= 0 {
		t.Fatalf("diagram view has no geometry: %+v", box)
	}
	if got := inkCount(tt, box); got < 20 {
		t.Fatalf("native diagram drew almost nothing: %d ink pixels", got)
	}

	// The source entry point is explicit and opens the M02 editor.
	if err := tt.Click("doc-diagram-edit-btn"); err != nil {
		t.Fatalf("edit button: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("doc-canvas-editor"); !ok {
		t.Fatalf("source editor did not open; texts=%v", tt.Texts())
	}
	if _, ok := tt.Find("doc-diagram-view-btn"); !ok {
		t.Fatalf("no explicit way back to the drawing while editing; texts=%v", tt.Texts())
	}

	// Back to the diagram; the editor disappears and the drawing returns.
	if err := tt.Click("doc-diagram-view-btn"); err != nil {
		t.Fatalf("view button: %v", err)
	}
	tt.Frame()
	tt.Frame()
	if _, ok := tt.Find("doc-canvas-editor"); ok {
		t.Fatalf("editor stayed open after returning to the diagram")
	}
	if _, ok := tt.Find(viewLabel); !ok {
		t.Fatalf("diagram did not return after editing")
	}
}

func TestDocDiagramPreviewHook(t *testing.T) {
	a, ps := openTestProject(t, "Mermaid Preview")
	tab := openDocForTest(t, ps, "docs/diagram-preview.md", diagramDocSrc)
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	if !docDiagramWired.Load() {
		t.Skip("requires docs/task-results/F03-integration.patch (public doc_live/documentation hooks)")
	}
	box, ok := tt.Find("doc-diagram-preview")
	if !ok {
		t.Fatalf("preview diagram missing; texts=%v", tt.Texts())
	}
	if got := inkCount(tt, box); got < 20 {
		t.Fatalf("preview diagram drew almost nothing: %d ink pixels", got)
	}
	// The code block itself is still present (copy/highlight do not regress).
	if _, ok := tt.Find("markdown-preview-content"); !ok {
		t.Fatalf("preview content missing")
	}
	if !tt.HasText("flowchart TD") {
		t.Fatalf("source code block missing from preview; texts=%v", tt.Texts())
	}
}

func TestDocDiagramDarkAndScaledLayouts(t *testing.T) {
	a, ps := openTestProject(t, "Mermaid Themes")
	openDocForTest(t, ps, "docs/diagram-theme.md", diagramDocSrc)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	if !docDiagramWired.Load() {
		t.Skip("requires docs/task-results/F03-integration.patch (public doc_live/documentation hooks)")
	}
	dir, err := os.MkdirTemp("", "gorequest-F03-visuals-")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("screenshots: %s", dir)
	for _, dark := range []bool{false, true} {
		for _, percent := range []int{100, 150} {
			for _, win := range [][2]int{{1200, 820}, {760, 820}} {
				if err := a.mgr.workspacePrefs().setMarkdownPolicy(markdownPreferences{FontPercent: percent, Padding: 24, LinePercent: 170}); err != nil {
					t.Fatal(err)
				}
				tt.SetDark(dark)
				tt.SetSize(win[0], win[1])
				tt.Frame()
				tt.Frame()
				start := codeBlockStart(t, diagramDocSrc, 1)
				box, ok := tt.Find(fmt.Sprintf("doc-diagram-view:%d", start))
				if !ok {
					t.Fatalf("dark=%v percent=%d win=%v: diagram missing; texts=%v", dark, percent, win, tt.Texts())
				}
				if box.W > float32(win[0]) {
					t.Fatalf("dark=%v percent=%d win=%v: diagram width %.0f exceeds window", dark, percent, win, box.W)
				}
				if got := inkCount(tt, box); got < 10 {
					t.Fatalf("dark=%v percent=%d win=%v: empty drawing (%d ink)", dark, percent, win, got)
				}
				// Geometry: the card must hug the drawing, not span the window
				// with a large empty region (the pre-fix FillWidth card did).
				// The scene carries ~10% padding per side, so ink fills ~80% of
				// a correctly sized card but only ~16% of a full-width one.
				if box.W > float32(win[0])*0.6 {
					t.Fatalf("dark=%v percent=%d win=%v: card %.0f fills the window", dark, percent, win, box.W)
				}
				ib, _ := inkBounds(tt, box)
				if ib.W < box.W*0.7 {
					t.Fatalf("dark=%v percent=%d win=%v: drawing %.0f wide in a %.0f card (empty card)", dark, percent, win, ib.W, box.W)
				}
				if box.H > docDiagramMaxHeight+20 {
					t.Fatalf("dark=%v percent=%d win=%v: card height %.0f exceeds the cap", dark, percent, win, box.H)
				}
				if ib.H < (box.H-20)*0.7 {
					t.Fatalf("dark=%v percent=%d win=%v: drawing %.0f high in a %.0f card (empty card)", dark, percent, win, ib.H, box.H)
				}
				saveDocDiagramImage(t, tt, filepath.Join(dir, fmt.Sprintf("diagram-dark%v-%d%%-%dx%d.png", dark, percent, win[0], win[1])))
			}
		}
	}
	tt.SetSize(1200, 820)
	tt.SetDark(false)
}

func saveDocDiagramImage(t *testing.T, tt *ui.Tester, path string) {
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

func TestDocDiagramUnsupportedShowsSourceNotPartialDrawing(t *testing.T) {
	src := "# 图\n\n```mermaid\nflowchart TD\n  subgraph S\n  A --> B\n  end\n```\n"
	a, ps := openTestProject(t, "Mermaid Unsupported")
	openDocForTest(t, ps, "docs/diagram-bad.md", src)
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tt.Frame()
	if !docDiagramWired.Load() {
		t.Skip("requires docs/task-results/F03-integration.patch (public doc_live/documentation hooks)")
	}
	start := codeBlockStart(t, src, 1)
	if _, ok := tt.Find(fmt.Sprintf("doc-diagram-view:%d", start)); ok {
		t.Fatalf("unsupported syntax must not draw a partial figure")
	}
	if _, ok := tt.Find("doc-diagram-edit-btn"); !ok {
		t.Fatalf("unsupported block lost its source entry; texts=%v", tt.Texts())
	}
	if !tt.HasText("subgraph S") {
		t.Fatalf("full source not shown; texts=%v", tt.Texts())
	}
}

func TestDocDiagramCJKMeasuredTextFitsScene(t *testing.T) {
	scene, d := diagram.Render(context.Background(),
		"flowchart TD\n A[中文标签😀] --> B[\"很长的第一行<br>很长的第二行\"]\n", diagram.Options{FontSize: 14})
	if d != nil {
		t.Fatal(d)
	}
	for _, n := range scene.Nodes {
		for _, line := range n.Lines {
			w, h := ui.ShapeText(line, ui.Font{Size: 14}, 0).Size()
			if w > n.Box.W {
				t.Fatalf("shaped width %.1f of %q exceeds node width %.1f", w, line, n.Box.W)
			}
			if h > n.Box.H {
				t.Fatalf("shaped height %.1f of %q exceeds node height %.1f", h, line, n.Box.H)
			}
		}
	}
}

func TestDocDiagramSceneCacheIsStableAndSourceKeyed(t *testing.T) {
	st := &docViewState{}
	store := docDiagramStoreFor(st)
	src := "flowchart LR\n A --> B\n"
	e1 := docDiagramRender(store, src, 14)
	e2 := docDiagramRender(store, src, 14)
	if e1 != e2 {
		t.Fatalf("cache miss for identical source/font size")
	}
	e3 := docDiagramRender(store, src, 18)
	if e3 == e1 {
		t.Fatalf("font-size change must produce a new layout")
	}
	e4 := docDiagramRender(store, "flowchart LR\n A --> C\n", 14)
	if e4.scene == e1.scene {
		t.Fatalf("source change must produce a new scene")
	}
}

func TestDocDiagramNativePaintWithoutWiring(t *testing.T) {
	scene, d := diagram.Render(context.Background(), "flowchart TD\n A[开始] --> B{判断}\n B -->|是| C[完成]\n", diagram.Options{FontSize: 14})
	if d != nil {
		t.Fatal(d)
	}
	colors := docDiagramColorsFor(uikit.PaletteFor(nil))
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Box(c).FillWidth().Height(float32(320)).Draw(func(p *ui.Painter, r ui.Rect) {
			docDiagramPaint(p, r, scene, colors)
		})
	}, 700, 360)
	tt.Frame()
	tt.Frame()
	if got := inkCount(tt, ui.Rect{X: 0, Y: 0, W: 700, H: 340}); got < 50 {
		t.Fatalf("native painter drew too little: %d ink pixels", got)
	}
}
