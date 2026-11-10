package diagram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func mustScene(t *testing.T, src string) *Scene {
	t.Helper()
	s, d := Render(context.Background(), src, Options{})
	if d != nil {
		t.Fatalf("Render(%q): %v", src, d)
	}
	return s
}

func TestFlowchartTopDownGeometry(t *testing.T) {
	s := mustScene(t, "flowchart TD\n  A[开始] --> B{判断?}\n  B -->|是| C[完成 😀]\n  B -->|否| D[其他]\n")
	if s.Kind != KindFlowchart || len(s.Nodes) != 4 || len(s.Edges) != 3 {
		t.Fatalf("scene shape wrong: %+v", s)
	}
	byID := map[string]Node{}
	for _, n := range s.Nodes {
		byID[n.ID] = n
	}
	a, b, c, d := byID["A"], byID["B"], byID["C"], byID["D"]
	if !(a.Box.Y < b.Box.Y && b.Box.Y < c.Box.Y) {
		t.Fatalf("TD layers not ordered: A=%v B=%v C=%v", a.Box, b.Box, c.Box)
	}
	if c.Box.Y != d.Box.Y {
		t.Fatalf("siblings on different layers: %v %v", c.Box, d.Box)
	}
	if c.Box.X == d.Box.X {
		t.Fatalf("siblings overlap on one column: %v %v", c.Box, d.Box)
	}
	// The first edge runs from A's bottom border to B's top border.
	e := s.Edges[0]
	if len(e.Points) != 2 {
		t.Fatalf("edge routing: %+v", e.Points)
	}
	if e.Points[0].Y != a.Box.Y+a.Box.H {
		t.Fatalf("edge starts at %v, want A bottom %.1f", e.Points[0], a.Box.Y+a.Box.H)
	}
	if e.Points[1].Y != b.Box.Y {
		t.Fatalf("arrow ends at %v, want B top %.1f", e.Points[1], b.Box.Y)
	}
	// The labelled edge carries a visible label box.
	var labelled *Edge
	for i := range s.Edges {
		if s.Edges[i].Label == "是" {
			labelled = &s.Edges[i]
		}
	}
	if labelled == nil || labelled.LabelBox.W <= 0 {
		t.Fatalf("edge label missing: %+v", s.Edges)
	}
}

func TestFlowchartDirections(t *testing.T) {
	td := mustScene(t, "flowchart TD\n A --> B\n")
	lr := mustScene(t, "flowchart LR\n A --> B\n")
	bt := mustScene(t, "flowchart BT\n A --> B\n")
	rl := mustScene(t, "flowchart RL\n A --> B\n")
	if !(nodeByID(td, "A").Box.Y < nodeByID(td, "B").Box.Y) {
		t.Fatalf("TD: %v %v", nodeByID(td, "A").Box, nodeByID(td, "B").Box)
	}
	if !(nodeByID(lr, "A").Box.X < nodeByID(lr, "B").Box.X) {
		t.Fatalf("LR: %v %v", nodeByID(lr, "A").Box, nodeByID(lr, "B").Box)
	}
	if !(nodeByID(bt, "A").Box.Y > nodeByID(bt, "B").Box.Y) {
		t.Fatalf("BT: %v %v", nodeByID(bt, "A").Box, nodeByID(bt, "B").Box)
	}
	if !(nodeByID(rl, "A").Box.X > nodeByID(rl, "B").Box.X) {
		t.Fatalf("RL: %v %v", nodeByID(rl, "A").Box, nodeByID(rl, "B").Box)
	}
}

func nodeByID(s *Scene, id string) Node {
	for _, n := range s.Nodes {
		if n.ID == id {
			return n
		}
	}
	return Node{}
}

func TestFlowchartCycleRoutesOutside(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A --> B\n B --> C\n C --> A\n")
	if len(s.Edges) != 3 {
		t.Fatalf("edges: %+v", s.Edges)
	}
	back := s.Edges[2]
	if len(back.Points) < 4 {
		t.Fatalf("back edge not routed around the graph: %+v", back.Points)
	}
	var maxRight float32
	for _, n := range s.Nodes {
		if n.Box.X+n.Box.W > maxRight {
			maxRight = n.Box.X + n.Box.W
		}
	}
	if back.Points[1].X <= maxRight {
		t.Fatalf("back route x=%.1f is not outside nodes (right=%.1f)", back.Points[1].X, maxRight)
	}
	a := nodeByID(s, "A")
	last := back.Points[len(back.Points)-1]
	if last.X != a.Box.X+a.Box.W || last.Y < a.Box.Y || last.Y > a.Box.Y+a.Box.H {
		t.Fatalf("back arrow does not meet A's border: %+v A=%v", last, a.Box)
	}
	if back.Points[1].X >= s.Width || last.X > s.Width {
		t.Fatalf("back route leaves the scene: width=%.1f points=%+v", s.Width, back.Points)
	}
}

func TestFlowchartSelfLoop(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A --> A\n")
	if len(s.Edges) != 1 || len(s.Edges[0].Points) != 4 {
		t.Fatalf("self loop: %+v", s.Edges)
	}
	if s.Width <= nodeByID(s, "A").Box.X+nodeByID(s, "A").Box.W {
		t.Fatalf("self loop not inside the scene width: %.1f", s.Width)
	}
}

func TestFlowchartLongChain(t *testing.T) {
	var b strings.Builder
	b.WriteString("flowchart TD\n")
	for i := 0; i < 150; i++ {
		b.WriteString("N")
		b.WriteString(itoa(i))
		b.WriteString(" --> N")
		b.WriteString(itoa(i + 1))
		b.WriteString("\n")
	}
	s, d := Render(context.Background(), b.String(), Options{Limits: Limits{MaxHeight: 40000}})
	if d != nil {
		t.Fatal(d)
	}
	if len(s.Nodes) != 151 || len(s.Edges) != 150 {
		t.Fatalf("long chain: nodes=%d edges=%d", len(s.Nodes), len(s.Edges))
	}
	ys := map[float32]bool{}
	for _, n := range s.Nodes {
		ys[n.Box.Y] = true
	}
	if len(ys) != 151 {
		t.Fatalf("long chain did not lay out one layer per node: %d distinct y", len(ys))
	}
}

func TestFlowchartDuplicateLabelsAndDeclarations(t *testing.T) {
	s := mustScene(t, "flowchart LR\n A[Start] --> B[Start]\n A[Renamed] --> C\n")
	if len(s.Nodes) != 3 {
		t.Fatalf("nodes: %+v", s.Nodes)
	}
	if nodeByID(s, "A").Lines[0] != "Renamed" {
		t.Fatalf("later label declaration must win: %+v", nodeByID(s, "A").Lines)
	}
	if nodeByID(s, "B").Lines[0] != "Start" {
		t.Fatalf("duplicate label text must stay on its own node: %+v", nodeByID(s, "B").Lines)
	}
}

func TestFlowchartMultilineCJKEmoji(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A[\"第一行<br>第二行<br>😀 done\"] --> B\n")
	a := nodeByID(s, "A")
	if len(a.Lines) != 3 {
		t.Fatalf("multiline label: %+v", a.Lines)
	}
	want := float32(0)
	for _, line := range a.Lines {
		if w := TextWidth(line, s.FontSize); w > want {
			want = w
		}
	}
	if a.Box.W < want {
		t.Fatalf("CJK box %.1f narrower than text %.1f", a.Box.W, want)
	}
	if a.Box.H < float32(len(a.Lines))*LineHeight(s.FontSize) {
		t.Fatalf("multi-line box too short: %.1f", a.Box.H)
	}
}

func TestFlowchartQuotedLabelWithBrackets(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A[\"link] text\"] --> B\n")
	if got := nodeByID(s, "A").Lines[0]; got != "link] text" {
		t.Fatalf("quoted label: %q", got)
	}
}

func TestFlowchartErrors(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
		contains  string
	}{
		{"type", "pie\n A : 1\n", 1, 1, "unsupported diagram type"},
		{"direction", "flowchart XX\n A\n", 1, 1, "direction"},
		{"subgraph", "flowchart TD\n subgraph S\n A\n", 2, 2, "subgraph"},
		{"click", "flowchart TD\n A\n click A \"javascript:alert(1)\"\n", 3, 2, "click"},
		{"inline label", "flowchart TD\n A -- yes --> B\n", 2, 4, "inline edge labels"},
		{"html label", "flowchart TD\n A[\"<script>alert(1)</script>\"] --> B\n", 2, 4, "HTML"},
		{"md string", "flowchart TD\n A[\"`x`\"] --> B\n", 2, 4, "Markdown strings"},
		{"shape", "flowchart TD\n A[[x]] --> B\n", 2, 3, "subroutine"},
		{"no target", "flowchart TD\n A -->\n", 2, 7, "expected a node"},
		{"unterminated", "flowchart TD\n A[x --> B\n", 2, 4, "unterminated"},
		{"init", "%%{init: {}}%%\nflowchart TD\n A\n", 1, 1, "init directives"},
		{"bidirectional", "flowchart TD\n A <--> B\n", 2, 4, "bidirectional"},
		{"dotted open", "flowchart TD\n A -.- B\n", 2, 0, ""}, // supported; see below
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, d := Render(context.Background(), tc.src, Options{})
			if tc.contains == "" {
				if d != nil {
					t.Fatalf("expected support, got %v", d)
				}
				return
			}
			if d == nil {
				t.Fatalf("expected a diagnostic")
			}
			if d.Line != tc.line || d.Column != tc.col {
				t.Fatalf("diagnostic at %d:%d, want %d:%d (%v)", d.Line, d.Column, tc.line, tc.col, d)
			}
			if !strings.Contains(d.Message, tc.contains) {
				t.Fatalf("message %q does not contain %q", d.Message, tc.contains)
			}
		})
	}
}

func TestFlowchartBudgetsAndCancellation(t *testing.T) {
	var nodes strings.Builder
	nodes.WriteString("flowchart TD\n")
	for i := 0; i < 201; i++ {
		nodes.WriteString("N" + itoa(i) + "\n")
	}
	if _, d := Render(context.Background(), nodes.String(), Options{}); d == nil || !strings.Contains(d.Message, "node limit") {
		t.Fatalf("node budget not enforced: %v", d)
	}

	var edges strings.Builder
	edges.WriteString("flowchart TD\n A --> B\n B --> C\n C --> A\n")
	if _, d := Render(context.Background(), edges.String(), Options{Limits: Limits{MaxEdges: 2}}); d == nil || !strings.Contains(d.Message, "edge limit") {
		t.Fatalf("edge budget not enforced: %v", d)
	}

	long := strings.Repeat("中", 201)
	if _, d := Render(context.Background(), "flowchart TD\n A["+long+"]\n", Options{}); d == nil || !strings.Contains(d.Message, "code points") {
		t.Fatalf("label budget not enforced: %v", d)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, d := Render(ctx, "flowchart TD\n A --> B\n", Options{}); d == nil || !strings.Contains(d.Message, "canceled") {
		t.Fatalf("cancellation not reported: %v", d)
	}

	if _, d := Render(context.Background(), "flowchart TD\n A --> B\n", Options{Limits: Limits{Timeout: time.Nanosecond}}); d == nil || !strings.Contains(d.Message, "time budget") {
		t.Fatalf("timeout not reported: %v", d)
	}

	if _, d := Render(context.Background(), strings.Repeat("A", 70<<10), Options{}); d == nil || !strings.Contains(d.Message, "64 KiB") {
		t.Fatalf("source budget not enforced: %v", d)
	}

	if _, d := Render(context.Background(), "flowchart TD\n A --> B\n", Options{Limits: Limits{MaxWidth: 100}}); d == nil || !strings.Contains(d.Message, "limit is") {
		t.Fatalf("output size budget not enforced: %v", d)
	}
}

func TestFlowchartEdgeStyles(t *testing.T) {
	s := mustScene(t, "flowchart LR\n A -.-> B\n B ==> C\n C --- D\n A -->|go| D\n")
	if len(s.Edges) != 4 {
		t.Fatalf("edges: %+v", s.Edges)
	}
	if !s.Edges[0].Dotted || !s.Edges[0].Arrow {
		t.Fatalf("dotted arrow: %+v", s.Edges[0])
	}
	if !s.Edges[1].Thick || !s.Edges[1].Arrow {
		t.Fatalf("thick arrow: %+v", s.Edges[1])
	}
	if s.Edges[2].Arrow {
		t.Fatalf("open link must have no arrowhead: %+v", s.Edges[2])
	}
	if s.Edges[3].Label != "go" || s.Edges[3].LabelBox.W <= 0 {
		t.Fatalf("pipe label: %+v", s.Edges[3])
	}
}

func TestFlowchartGroupsAndSemicolons(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A & B --> C & D; C --> E\n")
	if len(s.Nodes) != 5 || len(s.Edges) != 5 {
		t.Fatalf("groups: nodes=%d edges=%d", len(s.Nodes), len(s.Edges))
	}
}

func TestFlowchartSceneBounds(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A[很长很长很长的中文标签用于换行测试] --> B\n B --> C\n")
	for _, n := range s.Nodes {
		if n.Box.X < 0 || n.Box.Y < 0 || n.Box.X+n.Box.W > s.Width || n.Box.Y+n.Box.H > s.Height {
			t.Fatalf("node outside scene: %v scene=%fx%f", n.Box, s.Width, s.Height)
		}
	}
	for _, e := range s.Edges {
		for _, p := range e.Points {
			if p.X < 0 || p.Y < 0 || p.X > s.Width || p.Y > s.Height {
				t.Fatalf("edge point outside scene: %+v scene=%fx%f", p, s.Width, s.Height)
			}
		}
		if e.LabelBox.W > 0 && (e.LabelBox.X < 0 || e.LabelBox.Y < 0 || e.LabelBox.X+e.LabelBox.W > s.Width || e.LabelBox.Y+e.LabelBox.H > s.Height) {
			t.Fatalf("label outside scene: %+v scene=%fx%f", e.LabelBox, s.Width, s.Height)
		}
	}
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [8]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
