package desktop

import (
	"context"
	"fmt"
	"hash/fnv"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"weak"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"gorequest/internal/desktop/uikit"
	"gorequest/internal/diagram"
	"gorequest/internal/docedit"
	docmd "gorequest/internal/markdown"
)

// This file owns the native Mermaid surface: a read-only diagram card on
// the document canvas and in Markdown previews, with an explicit path into
// the existing M02 code editor for the source. The scene comes from
// internal/diagram, which also feeds the offline SVG export, so the native
// drawing and the exported file share one model. No WebView, no script, no
// network.

// docDiagramBudget bounds one native render; the diagram package also
// enforces its own node/edge/label/size budgets.
const docDiagramBudget = 300 * time.Millisecond

// docDiagramMaxHeight caps the drawing card so a very long diagram does not
// push the rest of the document off screen; docDiagramPaint scales it down.
const docDiagramMaxHeight = 900

type docDiagramEntry struct {
	scene *diagram.Scene
	diag  *diagram.Diagnostic
	ts    time.Time
}

type docDiagramStore struct {
	mu      sync.Mutex
	entries map[string]*docDiagramEntry
}

var docDiagramStores sync.Map // weak.Pointer[docViewState] -> *docDiagramStore

// docDiagramWired records that the integration patch installed the Markdown
// code hook. The wiring lives in public files (doc_live.go / documentation.go)
// and ships as docs/task-results/F03-integration.patch; unit tests that need
// the hook skip with an explicit message when it is not applied.
var docDiagramWired atomic.Bool

func docDiagramStoreFor(st *docViewState) *docDiagramStore {
	key := weak.Make(st)
	if v, ok := docDiagramStores.Load(key); ok {
		return v.(*docDiagramStore)
	}
	s := &docDiagramStore{entries: map[string]*docDiagramEntry{}}
	actual, loaded := docDiagramStores.LoadOrStore(key, s)
	if !loaded {
		runtime.AddCleanup(st, func(k weak.Pointer[docViewState]) { docDiagramStores.Delete(k) }, key)
	}
	return actual.(*docDiagramStore)
}

// docDiagramRender parses and lays out one fence, caching by source and
// font size. It never returns nil.
func docDiagramRender(store *docDiagramStore, raw string, fontSize float32) *docDiagramEntry {
	key := raw + "\x00" + fmt.Sprintf("%.2f", fontSize)
	store.mu.Lock()
	if e, ok := store.entries[key]; ok {
		e.ts = time.Now()
		store.mu.Unlock()
		return e
	}
	store.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), docDiagramBudget)
	defer cancel()
	scene, d := diagram.Render(ctx, raw, diagram.Options{FontSize: fontSize})
	e := &docDiagramEntry{scene: scene, diag: d, ts: time.Now()}

	store.mu.Lock()
	store.entries[key] = e
	if len(store.entries) > 64 {
		var oldestKey string
		var oldest time.Time
		for k, v := range store.entries {
			if oldestKey == "" || v.ts.Before(oldest) {
				oldestKey, oldest = k, v.ts
			}
		}
		delete(store.entries, oldestKey)
	}
	store.mu.Unlock()
	return e
}

// docDiagramCodeBlock draws a mermaid code block. It returns false for any
// other language so the existing code-block path is untouched. When the
// block is being edited in place it delegates to docCanvasCodeBlock, which
// keeps M02 input, selection, save and M04 undo behaviour exactly.
func (a *App) docDiagramCodeBlock(c *ui.Context, t *ui.Theme, tab *Tab, st *docViewState, b docedit.Block, be *docBlockEdit) bool {
	if be != nil {
		return false
	}
	raw := b.EditText()
	start := b.Start
	if !strings.EqualFold(docCodeLanguage(raw), "mermaid") {
		return false
	}

	source := docDiagramSource(raw)
	th := a.docMarkdownTheme(t)
	fs := th.FontSize
	p := uikit.PaletteFor(t)
	entry := docDiagramRender(docDiagramStoreFor(st), source, fs)
	colors := docDiagramColorsFor(p)

	host := ui.Column(c).FillWidth().Padding(2, 8).Gap(4).Key(fmt.Sprintf("doc-diagram:%d", start))
	host.Label(docBlockLabel(docedit.Block{Start: start, Kind: docmd.CodeBlock}))
	host.Children(func() {
		ui.Column(c).FillWidth().Background(th.CodeBlockBackground.Over(p.BG)).Border(1, th.CodeBlockBorder).Radius(th.CodeBlockRadius).Clip().Children(func() {
			ui.Row(c).FillWidth().AlignItems(ui.Center).Padding(th.FontSize*.35, th.FontSize*.4, 0, th.FontSize*.7).Children(func() {
				ui.Text(c, "mermaid").FontSize(th.FontSize * .8).TextColor(p.Muted).Unselectable()
				ui.Spacer(c)
				a.docDiagramToggle(c, t, p, tab, st, start, false)
				copyLabel := "复制源码"
				if a.interopLanguage() == langEnglish {
					copyLabel = "Copy source"
				}
				copy := ui.ButtonBase(c).Label("doc-diagram-copy").Padding(th.FontSize*.2, th.FontSize*.45).Radius(5).Gap(th.FontSize * .3)
				if copy.Hovered() {
					copy.Background(th.Text.Alpha(.08))
				}
				copy.Children(func() {
					ui.Text(c, copyLabel).FontSize(th.FontSize * .8).TextColor(p.Muted)
				})
				if copy.Clicked() {
					c.WriteClipboard(source)
				}
			})
			if entry.diag != nil {
				notice := fmt.Sprintf("%s (%s)", a.docDiagramUnsupportedText(), entry.diag.Error())
				ui.Text(c, notice).FontSize(th.FontSize*.82).TextColor(p.Warning).Padding(2, th.FontSize*.7)
				// The full source stays visible and editable; never a
				// partial drawing that hides what was not understood.
				ui.Text(c, strings.TrimRight(source, "\r\n")).Font("monospace").FontSize(th.FontSize*.78).TextColor(p.Muted).Padding(2, th.FontSize*.7, th.FontSize*.6, th.FontSize*.7)
				return
			}
			scene := entry.scene
			if scene == nil || scene.Width <= 0 || scene.Height <= 0 {
				ui.Text(c, a.docDiagramUnsupportedText()).FontSize(th.FontSize*.82).TextColor(p.Warning).Padding(2, th.FontSize*.7)
				return
			}
			// Size the card to the scene instead of filling the width: a tall
			// flowchart in a full-width aspect box would leave a huge empty
			// card. The card is centred in a full-width row so a narrow
			// drawing does not hug the left edge, and MaxWidthPercent keeps a
			// wide diagram inside a narrow window (docDiagramPaint scales it).
			h := scene.Height + 12
			if h > docDiagramMaxHeight {
				h = docDiagramMaxHeight
			}
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				ui.Box(c).Width(scene.Width+16).MaxWidthPercent(100).Height(h).Padding(6, 8).Key(fmt.Sprintf("doc-diagram-view:%d", start)).
					Label(fmt.Sprintf("doc-diagram-view:%d", start)).
					Draw(func(painter *ui.Painter, r ui.Rect) {
						docDiagramPaint(painter, r, scene, colors)
					})
			})
		})
	})
	return true
}

// docDiagramEditToolbar sits above the standard in-place code editor of a
// mermaid block and offers the explicit way back to the drawing.
func (a *App) docDiagramEditToolbar(c *ui.Context, t *ui.Theme, tab *Tab, st *docViewState, be *docBlockEdit) {
	if be == nil || be.kind != docmd.CodeBlock {
		return
	}
	if !strings.EqualFold(docCodeLanguage(be.srcBase), "mermaid") {
		return
	}
	p := uikit.PaletteFor(t)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(0, 2, 2, 2).Children(func() {
		a.docDiagramToggle(c, t, p, tab, st, be.start, true)
	})
}

// docDiagramToggle renders 编辑源码 / 查看图表.
func (a *App) docDiagramToggle(c *ui.Context, t *ui.Theme, p uikit.Palette, tab *Tab, st *docViewState, start int, editing bool) {
	lv := docLiveFor(st)
	zh := a.interopLanguage() != langEnglish
	label, id := "查看图表", "doc-diagram-view-btn"
	if !zh {
		label = "View diagram"
	}
	if !editing {
		label, id = "编辑源码", "doc-diagram-edit-btn"
		if !zh {
			label = "Edit source"
		}
	}
	btn := ui.ButtonBase(c).Label(id).Padding(2, 6).Radius(5).Gap(4)
	btn.Children(func() {
		ui.Text(c, label).FontSize(11).TextColor(p.Muted)
	})
	if !btn.Clicked() {
		return
	}
	if editing {
		a.commitDocBlock(tab, st)
		lv.block = nil
		lv.slashOpen = false
		c.Invalidate()
		return
	}
	blocks := lv.blocksFor(tab.DocEditor.Buffer.String())
	for _, block := range blocks {
		if block.Start == start {
			a.activateDocBlock(t, tab, st, block, 0)
			c.Invalidate()
			return
		}
	}
}

// installDocDiagramCode adds the mermaid renderer to a Markdown view's code
// hook map. The default code block stays below the drawing, so syntax
// colour, copy and the source itself do not regress.
func (a *App) installDocDiagramCode(o *markdown.Options, st *docViewState) {
	docDiagramWired.Store(true)
	if o.Code == nil {
		o.Code = map[string]func(*ui.Context, *markdown.CodeBlockView){}
	}
	o.Code["mermaid"] = func(c *ui.Context, b *markdown.CodeBlockView) {
		a.docDiagramPreview(c, st, b)
	}
}

func (a *App) docDiagramPreview(c *ui.Context, st *docViewState, b *markdown.CodeBlockView) {
	th := a.docMarkdownTheme(c.Theme())
	p := uikit.PaletteFor(c.Theme())
	entry := docDiagramRender(docDiagramStoreFor(st), b.Code(), th.FontSize)
	colors := docDiagramColorsFor(p)
	ui.Column(c).FillWidth().Gap(4).Children(func() {
		if entry.diag != nil {
			ui.Text(c, fmt.Sprintf("%s (%s)", a.docDiagramUnsupportedText(), entry.diag.Error())).FontSize(th.FontSize * .82).TextColor(p.Warning)
		} else if entry.scene != nil && entry.scene.Width > 0 && entry.scene.Height > 0 {
			scene := entry.scene
			// Same rule as the canvas card: size the box to the scene and
			// centre it, instead of a full-width aspect box that turns a tall
			// flowchart into a mostly empty panel.
			h := scene.Height + 8
			if h > docDiagramMaxHeight {
				h = docDiagramMaxHeight
			}
			ui.Row(c).FillWidth().Justify(ui.Center).Children(func() {
				ui.Box(c).Width(scene.Width+8).MaxWidthPercent(100).Height(h).Padding(4, 0).Label("doc-diagram-preview").
					Draw(func(painter *ui.Painter, r ui.Rect) {
						docDiagramPaint(painter, r, scene, colors)
					})
			})
		}
		markdown.DefaultCodeBlock(c, b)
	})
}

// docDiagramSource strips the fence lines from a block's source.
func docDiagramSource(raw string) string {
	lines := strings.Split(strings.ReplaceAll(strings.TrimRight(raw, "\r\n"), "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return ""
	}
	lines = lines[1:]
	if len(lines) > 0 {
		last := strings.TrimSpace(lines[len(lines)-1])
		if strings.HasPrefix(last, "```") || strings.HasPrefix(last, "~~~") {
			lines = lines[:len(lines)-1]
		}
	}
	return strings.Join(lines, "\n")
}

func (a *App) docDiagramUnsupportedText() string {
	if a.interopLanguage() == langEnglish {
		return "This Mermaid diagram is outside the supported offline subset (flowchart, sequenceDiagram); the source is kept"
	}
	return "该 Mermaid 图超出离线支持子集（flowchart、sequenceDiagram），已保留源码"
}

// docDiagramColors is the native palette of a diagram.
type docDiagramColors struct {
	text, nodeFill, nodeStroke, line, noteFill, noteStroke ui.Color
}

func docDiagramColorsFor(p uikit.Palette) docDiagramColors {
	return docDiagramColors{
		text:       p.Text,
		nodeFill:   p.Sidebar,
		nodeStroke: p.Muted,
		line:       p.Muted,
		noteFill:   p.Warning.Alpha(.12),
		noteStroke: p.Warning,
	}
}

// docDiagramPaint draws one scene into r, scaling down to fit. Every glyph
// size, line width and arrowhead scales with the drawing.
func docDiagramPaint(p *ui.Painter, r ui.Rect, s *diagram.Scene, col docDiagramColors) {
	if s == nil || s.Width <= 0 || s.Height <= 0 || r.W <= 0 {
		return
	}
	scale := float32(1)
	if r.W < s.Width {
		scale = r.W / s.Width
	}
	if r.H > 0 && s.Height*scale > r.H {
		scale = r.H / s.Height
	}
	if scale <= 0 {
		return
	}
	// Center the drawing inside the card: the card may be wider than the
	// scene (MaxWidth only caps it) and a left-aligned drawing looks clipped.
	ox := r.X + (r.W-s.Width*scale)/2
	oy := r.Y
	if s.Height*scale < r.H {
		oy = r.Y + (r.H-s.Height*scale)/2
	}
	x := func(v float32) float32 { return ox + v*scale }
	y := func(v float32) float32 { return oy + v*scale }
	rect := func(b diagram.Rect) ui.Rect {
		return ui.Rect{X: x(b.X), Y: y(b.Y), W: b.W * scale, H: b.H * scale}
	}
	fs := s.FontSize * scale
	th := diagram.LineHeight(s.FontSize) * scale

	nodeText := func(b diagram.Rect, lines []string) {
		if len(lines) == 0 {
			return
		}
		cx := x(b.X + b.W/2)
		top := y(b.Y + (b.H-float32(len(lines))*diagram.LineHeight(s.FontSize))/2)
		for i, line := range lines {
			if line == "" {
				continue
			}
			w := diagram.TextWidth(line, s.FontSize) * scale
			p.Text(cx-w/2, top+float32(i)*th, line, fs, col.text)
		}
	}
	edgeColor := col.line
	for _, e := range s.Edges {
		path := &ui.Path{}
		for i, pt := range e.Points {
			if i == 0 {
				path.MoveTo(x(pt.X), y(pt.Y))
			} else {
				path.LineTo(x(pt.X), y(pt.Y))
			}
		}
		if e.Dotted {
			path.Dash(0, 5*scale, 4*scale)
		}
		width := float32(1.5) * scale
		if e.Thick {
			width = 3 * scale
		}
		p.StrokePath(path, width, edgeColor)
		if e.Arrow && len(e.Points) >= 2 {
			docDiagramArrow(p, e.Points[len(e.Points)-2], e.Points[len(e.Points)-1], scale, x, y, edgeColor)
		}
		if len(e.LabelLines) > 0 {
			box := e.LabelBox
			p.Fill(rect(box), col.nodeFill, 3*scale)
			nodeText(box, e.LabelLines)
		}
	}
	for _, n := range s.Nodes {
		box := rect(n.Box)
		switch n.Shape {
		case diagram.ShapeDiamond:
			path := &ui.Path{}
			path.MoveTo(box.X+box.W/2, box.Y)
			path.LineTo(box.X+box.W, box.Y+box.H/2)
			path.LineTo(box.X+box.W/2, box.Y+box.H)
			path.LineTo(box.X, box.Y+box.H/2)
			path.Close()
			p.FillPath(path, col.nodeFill)
			p.StrokePath(path, 1.5*scale, col.nodeStroke)
		case diagram.ShapeCircle:
			p.Fill(box, col.nodeFill, box.H/2)
			p.Stroke(box, col.nodeStroke, box.H/2, 1.5*scale)
		default:
			radius := float32(0)
			switch n.Shape {
			case diagram.ShapeRounded:
				radius = 8 * scale
			case diagram.ShapeStadium:
				radius = box.H / 2
			}
			p.Fill(box, col.nodeFill, radius)
			p.Stroke(box, col.nodeStroke, radius, 1.5*scale)
		}
		nodeText(n.Box, n.Lines)
	}

	lifelineBottom := s.Height - 20
	for _, part := range s.Participants {
		path := &ui.Path{}
		path.MoveTo(x(part.CenterX), y(part.Box.Y+part.Box.H))
		path.LineTo(x(part.CenterX), y(lifelineBottom))
		path.Dash(0, 5*scale, 5*scale)
		p.StrokePath(path, scale, edgeColor)
	}
	for _, n := range s.Notes {
		box := rect(n.Box)
		p.Fill(box, col.noteFill, 4*scale)
		p.Stroke(box, col.noteStroke, 4*scale, scale)
		nodeText(n.Box, n.Lines)
	}
	for _, m := range s.Messages {
		path := &ui.Path{}
		if m.Self && len(m.Loop) > 1 {
			for i, pt := range m.Loop {
				if i == 0 {
					path.MoveTo(x(pt.X), y(pt.Y))
				} else {
					path.LineTo(x(pt.X), y(pt.Y))
				}
			}
		} else {
			path.MoveTo(x(m.FromX), y(m.Y))
			path.LineTo(x(m.ToX), y(m.Y))
		}
		if m.Dotted {
			path.Dash(0, 5*scale, 4*scale)
		}
		p.StrokePath(path, 1.5*scale, edgeColor)
		docDiagramMessageHead(p, m, scale, x, y, edgeColor)
		if len(m.LabelLines) > 0 {
			p.Fill(rect(m.LabelBox), col.nodeFill, 3*scale)
			nodeText(m.LabelBox, m.LabelLines)
		}
	}
	for _, part := range s.Participants {
		box := rect(part.Box)
		p.Fill(box, col.nodeFill, 4*scale)
		p.Stroke(box, col.nodeStroke, 4*scale, 1.5*scale)
		nodeText(part.Box, part.Lines)
	}
}

func docDiagramMessageHead(p *ui.Painter, m diagram.Message, scale float32, x, y func(float32) float32, color ui.Color) {
	var from, to diagram.Point
	if m.Self && len(m.Loop) >= 2 {
		from, to = m.Loop[len(m.Loop)-2], m.Loop[len(m.Loop)-1]
	} else {
		dir := float32(1)
		if m.ToX < m.FromX {
			dir = -1
		}
		from = diagram.Point{X: m.ToX - dir, Y: m.Y}
		to = diagram.Point{X: m.ToX, Y: m.Y}
	}
	switch m.Head {
	case diagram.ArrowHead:
		docDiagramArrow(p, from, to, scale, x, y, color)
	case diagram.ArrowCross:
		size := 5 * scale
		p.StrokePolyline([][2]float32{{x(to.X) - size, y(to.Y) - size}, {x(to.X) + size, y(to.Y) + size}}, 1.5*scale, color)
		p.StrokePolyline([][2]float32{{x(to.X) - size, y(to.Y) + size}, {x(to.X) + size, y(to.Y) - size}}, 1.5*scale, color)
	}
}

func docDiagramArrow(p *ui.Painter, tail, tip diagram.Point, scale float32, x, y func(float32) float32, color ui.Color) {
	dx, dy := tip.X-tail.X, tip.Y-tail.Y
	l := sqrt32(dx*dx + dy*dy)
	if l == 0 {
		return
	}
	ux, uy := dx/l, dy/l
	nx, ny := -uy, ux
	size, half := 8*scale, 4*scale
	path := &ui.Path{}
	path.MoveTo(x(tip.X), y(tip.Y))
	path.LineTo(x(tip.X-ux*size+nx*half), y(tip.Y-uy*size+ny*half))
	path.LineTo(x(tip.X-ux*size-nx*half), y(tip.Y-uy*size-ny*half))
	path.Close()
	p.FillPath(path, color)
}

func sqrt32(v float32) float32 {
	if v <= 0 {
		return 0
	}
	// Newton steps are enough for the arrow direction; the scene already
	// bounded the values.
	x := v
	for i := 0; i < 12; i++ {
		x = (x + v/x) / 2
	}
	return x
}

// docDiagramFingerprint is a stable short hash used by tests to assert the
// cache key does not depend on the pointer identity of a view state.
func docDiagramFingerprint(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}
