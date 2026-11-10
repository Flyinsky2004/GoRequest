package diagram

import (
	"fmt"
	"html"
	"math"
	"strconv"
	"strings"
)

// SVGTheme is the fixed, explicit palette of an exported SVG. Every value
// is written as an attribute (no CSS variables, no external stylesheet), so
// the file renders identically in any offline viewer.
type SVGTheme struct {
	Background string
	Text       string
	NodeFill   string
	NodeStroke string
	Line       string
	NoteFill   string
	NoteStroke string
}

// LightSVGTheme is the palette used by offline document export.
func LightSVGTheme() SVGTheme {
	return SVGTheme{
		Background: "#ffffff", Text: "#1f2328",
		NodeFill: "#ffffff", NodeStroke: "#57606a",
		Line: "#57606a", NoteFill: "#fff8c5", NoteStroke: "#d4a72c",
	}
}

// DarkSVGTheme matches the application's dark canvas.
func DarkSVGTheme() SVGTheme {
	return SVGTheme{
		Background: "#0d1117", Text: "#e6edf3",
		NodeFill: "#21262d", NodeStroke: "#8b949e",
		Line: "#8b949e", NoteFill: "#3b2e00", NoteStroke: "#d4a72c",
	}
}

const svgFontFamily = "-apple-system,BlinkMacSystemFont,'Segoe UI','PingFang SC','Hiragino Sans GB','Microsoft YaHei',sans-serif"

// RenderSVG serializes one scene as a self-contained SVG document. The
// output contains no scripts, no foreignObject, no href attributes, no
// external images or fonts and no event handlers: labels are escaped text.
func RenderSVG(scene *Scene, theme SVGTheme, limits Limits) (string, *Diagnostic) {
	if scene == nil {
		return "", &Diagnostic{Message: "no diagram scene to export"}
	}
	limits = limits.effective()
	if theme.Text == "" {
		theme = LightSVGTheme()
	}
	var b strings.Builder
	b.Grow(4096)
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%s" viewBox="0 0 %s %s" role="img" aria-label="%s">`+"\n",
		num(scene.Width), num(scene.Height), num(scene.Width), num(scene.Height), esc(scene.Title))
	fmt.Fprintf(&b, "<title>%s</title>\n", esc(scene.Title))
	fmt.Fprintf(&b, "<desc>%s</desc>\n", esc("Offline Mermaid subset rendered by GoRequest: "+scene.Title))
	fmt.Fprintf(&b, "<g font-family=\"%s\" font-size=\"%s\" fill=\"%s\">\n", svgFontFamily, num(scene.FontSize), theme.Text)
	if theme.Background != "" {
		fmt.Fprintf(&b, `<rect x="0" y="0" width="%s" height="%s" fill="%s"/>`+"\n", num(scene.Width), num(scene.Height), theme.Background)
	}
	switch scene.Kind {
	case KindFlowchart:
		writeFlowSVG(&b, scene, theme)
	case KindSequence:
		writeSequenceSVG(&b, scene, theme)
	}
	b.WriteString("</g>\n</svg>\n")
	out := b.String()
	if len(out) > limits.MaxSVGBytes {
		return "", &Diagnostic{Message: fmt.Sprintf("diagram SVG is %d bytes; the limit is %d", len(out), limits.MaxSVGBytes)}
	}
	return out, nil
}

func writeFlowSVG(b *strings.Builder, scene *Scene, theme SVGTheme) {
	// Edges first so node fills cover line ends.
	for _, e := range scene.Edges {
		var pts []string
		for _, p := range e.Points {
			pts = append(pts, num(p.X)+","+num(p.Y))
		}
		width := "2"
		if e.Thick {
			width = "3.5"
		}
		dash := ""
		if e.Dotted {
			dash = ` stroke-dasharray="5 4"`
		}
		fmt.Fprintf(b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%s" stroke-linejoin="round"%s/>`+"\n",
			strings.Join(pts, " "), theme.Line, width, dash)
		if e.Arrow && len(e.Points) >= 2 {
			writeArrow(b, e.Points[len(e.Points)-2], e.Points[len(e.Points)-1], theme.Line)
		}
		if len(e.LabelLines) > 0 {
			writeLabelBox(b, e.LabelBox, e.LabelLines, scene.FontSize, theme.Background, theme.Text)
		}
	}
	for _, n := range scene.Nodes {
		writeNode(b, n, theme)
		writeTextLines(b, n.Box, n.Lines, scene.FontSize, theme.Text)
	}
}

func writeNode(b *strings.Builder, n Node, theme SVGTheme) {
	box := n.Box
	switch n.Shape {
	case ShapeDiamond:
		fmt.Fprintf(b, `<polygon points="%s,%s %s,%s %s,%s %s,%s" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
			num(box.X+box.W/2), num(box.Y), num(box.X+box.W), num(box.Y+box.H/2),
			num(box.X+box.W/2), num(box.Y+box.H), num(box.X), num(box.Y+box.H/2),
			theme.NodeFill, theme.NodeStroke)
	case ShapeCircle:
		fmt.Fprintf(b, `<ellipse cx="%s" cy="%s" rx="%s" ry="%s" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
			num(box.X+box.W/2), num(box.Y+box.H/2), num(box.W/2), num(box.H/2), theme.NodeFill, theme.NodeStroke)
	default:
		rx := float32(0)
		switch n.Shape {
		case ShapeRounded:
			rx = 8
		case ShapeStadium:
			rx = box.H / 2
		}
		if rx > 0 {
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="%s" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
				num(box.X), num(box.Y), num(box.W), num(box.H), num(rx), theme.NodeFill, theme.NodeStroke)
		} else {
			fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
				num(box.X), num(box.Y), num(box.W), num(box.H), theme.NodeFill, theme.NodeStroke)
		}
	}
}

func writeSequenceSVG(b *strings.Builder, scene *Scene, theme SVGTheme) {
	lifelineBottom := scene.Height - marginDIP
	for _, p := range scene.Participants {
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1" stroke-dasharray="5 5"/>`+"\n",
			num(p.CenterX), num(p.Box.Y+p.Box.H), num(p.CenterX), num(lifelineBottom), theme.Line)
	}
	for _, n := range scene.Notes {
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="4" fill="%s" stroke="%s" stroke-width="1"/>`+"\n",
			num(n.Box.X), num(n.Box.Y), num(n.Box.W), num(n.Box.H), theme.NoteFill, theme.NoteStroke)
		writeTextLines(b, n.Box, n.Lines, scene.FontSize, theme.Text)
	}
	for _, m := range scene.Messages {
		width := "1.5"
		dash := ""
		if m.Dotted {
			dash = ` stroke-dasharray="5 4"`
		}
		if m.Self && len(m.Loop) > 1 {
			var pts []string
			for _, p := range m.Loop {
				pts = append(pts, num(p.X)+","+num(p.Y))
			}
			fmt.Fprintf(b, `<polyline points="%s" fill="none" stroke="%s" stroke-width="%s"%s/>`+"\n",
				strings.Join(pts, " "), theme.Line, width, dash)
			writeMessageHead(b, m, theme.Line, m.Loop[len(m.Loop)-2], m.Loop[len(m.Loop)-1])
		} else {
			fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="%s"%s/>`+"\n",
				num(m.FromX), num(m.Y), num(m.ToX), num(m.Y), theme.Line, width, dash)
			dir := float32(1)
			if m.ToX < m.FromX {
				dir = -1
			}
			writeMessageHead(b, m, theme.Line, Point{X: m.ToX - dir*1, Y: m.Y}, Point{X: m.ToX, Y: m.Y})
		}
		if len(m.LabelLines) > 0 {
			writeLabelBox(b, m.LabelBox, m.LabelLines, scene.FontSize, theme.Background, theme.Text)
		}
	}
	for _, p := range scene.Participants {
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="4" fill="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
			num(p.Box.X), num(p.Box.Y), num(p.Box.W), num(p.Box.H), theme.NodeFill, theme.NodeStroke)
		writeTextLines(b, p.Box, p.Lines, scene.FontSize, theme.Text)
	}
}

func writeMessageHead(b *strings.Builder, m Message, color string, from, to Point) {
	switch m.Head {
	case ArrowHead:
		writeArrow(b, from, to, color)
	case ArrowCross:
		size := float32(5)
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
			num(to.X-size), num(to.Y-size), num(to.X+size), num(to.Y+size), color)
		fmt.Fprintf(b, `<line x1="%s" y1="%s" x2="%s" y2="%s" stroke="%s" stroke-width="1.5"/>`+"\n",
			num(to.X-size), num(to.Y+size), num(to.X+size), num(to.Y-size), color)
	}
}

// writeArrow draws a filled arrowhead at tip, oriented from tail to tip.
func writeArrow(b *strings.Builder, tail, tip Point, color string) {
	dx, dy := tip.X-tail.X, tip.Y-tail.Y
	l := vecLen(dx, dy)
	if l == 0 {
		return
	}
	ux, uy := dx/l, dy/l
	nx, ny := -uy, ux
	size := float32(8)
	half := float32(4)
	p1 := Point{X: tip.X - ux*size + nx*half, Y: tip.Y - uy*size + ny*half}
	p2 := Point{X: tip.X - ux*size - nx*half, Y: tip.Y - uy*size - ny*half}
	fmt.Fprintf(b, `<polygon points="%s,%s %s,%s %s,%s" fill="%s"/>`+"\n",
		num(tip.X), num(tip.Y), num(p1.X), num(p1.Y), num(p2.X), num(p2.Y), color)
}

func writeLabelBox(b *strings.Builder, box Rect, lines []string, fontSize float32, bg, fg string) {
	if box.W <= 0 {
		return
	}
	if bg != "" {
		fmt.Fprintf(b, `<rect x="%s" y="%s" width="%s" height="%s" rx="3" fill="%s"/>`+"\n",
			num(box.X), num(box.Y), num(box.W), num(box.H), bg)
	}
	writeTextLines(b, box, lines, fontSize, fg)
}

// writeTextLines centers lines in box, with a fixed baseline offset so the
// SVG and the native painter use the same geometry.
func writeTextLines(b *strings.Builder, box Rect, lines []string, fontSize float32, color string) {
	if len(lines) == 0 {
		return
	}
	lh := LineHeight(fontSize)
	top := box.Y + (box.H-float32(len(lines))*lh)/2
	cx := box.X + box.W/2
	for i, line := range lines {
		if line == "" {
			continue
		}
		y := top + float32(i)*lh + lh/2 + fontSize*0.34
		fmt.Fprintf(b, `<text x="%s" y="%s" text-anchor="middle" fill="%s">%s</text>`+"\n",
			num(cx), num(y), color, esc(line))
	}
}

func vecLen(x, y float32) float32 {
	return float32(math.Hypot(float64(x), float64(y)))
}

func num(v float32) string {
	return strconv.FormatFloat(float64(v), 'f', 1, 32)
}

func esc(s string) string { return html.EscapeString(stripControl(s)) }
