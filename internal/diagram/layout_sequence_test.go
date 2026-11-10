package diagram

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestSequenceGeometryAndOrder(t *testing.T) {
	src := "sequenceDiagram\n" +
		"  participant A as 客户端\n" +
		"  actor B as 服务端\n" +
		"  Note over A,B: 建立连接\n" +
		"  A->>B: 请求\n" +
		"  B-->>A: 响应<br>完成\n" +
		"  C->>A: 隐式参与者\n"
	s := mustScene(t, src)
	if s.Kind != KindSequence || len(s.Participants) != 3 {
		t.Fatalf("participants: %+v", s.Participants)
	}
	if s.Participants[0].ID != "A" || s.Participants[1].ID != "B" || s.Participants[2].ID != "C" {
		t.Fatalf("explicit participants must come first, then first mention: %+v", s.Participants)
	}
	if s.Participants[0].Lines[0] != "客户端" || s.Participants[1].Lines[0] != "服务端" {
		t.Fatalf("aliases: %+v", s.Participants)
	}
	if len(s.Notes) != 1 || len(s.Messages) != 3 {
		t.Fatalf("rows: notes=%d messages=%d", len(s.Notes), len(s.Messages))
	}
	n := s.Notes[0]
	if n.Box.X < 0 || n.Box.X+n.Box.W > s.Width || n.Box.Y < 0 || n.Box.Y+n.Box.H > s.Height {
		t.Fatalf("note outside scene: %+v scene=%.0fx%.0f", n.Box, s.Width, s.Height)
	}
	if !(n.Box.Y < s.Messages[0].Y && s.Messages[0].Y < s.Messages[1].Y && s.Messages[1].Y < s.Messages[2].Y) {
		t.Fatalf("source order not preserved: note=%v msgs=%v %v %v", n.Box.Y, s.Messages[0].Y, s.Messages[1].Y, s.Messages[2].Y)
	}
	first := s.Messages[0]
	if first.Head != ArrowHead || first.Dotted {
		t.Fatalf("A->>B must be solid with a filled head: %+v", first)
	}
	if !s.Messages[1].Dotted || s.Messages[1].Head != ArrowHead {
		t.Fatalf("B-->>A must be dotted with a head: %+v", s.Messages[1])
	}
	if len(s.Messages[1].LabelLines) != 2 {
		t.Fatalf("br line break: %+v", s.Messages[1].LabelLines)
	}
	// Arrow spans the two lane centers.
	if first.FromX != s.Participants[0].CenterX || first.ToX != s.Participants[1].CenterX {
		t.Fatalf("arrow does not connect lane centers: %+v", first)
	}
}

func TestSequenceArrowKindsAndCross(t *testing.T) {
	s := mustScene(t, "sequenceDiagram\n A-xB: cross\n B--xA: dashed cross\n A->B: open\n B-->A: dotted open\n")
	if len(s.Messages) != 4 {
		t.Fatalf("messages: %+v", s.Messages)
	}
	if s.Messages[0].Head != ArrowCross || s.Messages[0].Dotted {
		t.Fatalf("solid cross: %+v", s.Messages[0])
	}
	if s.Messages[1].Head != ArrowCross || !s.Messages[1].Dotted {
		t.Fatalf("dashed cross: %+v", s.Messages[1])
	}
	if s.Messages[2].Head != ArrowNone || s.Messages[2].Dotted {
		t.Fatalf("open solid: %+v", s.Messages[2])
	}
	if s.Messages[3].Head != ArrowNone || !s.Messages[3].Dotted {
		t.Fatalf("open dotted: %+v", s.Messages[3])
	}
}

func TestSequenceSelfMessageAndAutonumber(t *testing.T) {
	s := mustScene(t, "sequenceDiagram\n autonumber\n A->>A: 自己处理\n A->>B: 继续\n")
	if len(s.Messages) != 2 {
		t.Fatalf("messages: %+v", s.Messages)
	}
	m := s.Messages[0]
	if !m.Self || len(m.Loop) != 4 {
		t.Fatalf("self message loop: %+v", m)
	}
	if m.Loop[1].X <= m.FromX {
		t.Fatalf("self loop must leave the lane: %+v", m.Loop)
	}
	if !strings.HasPrefix(m.LabelLines[0], "1.") {
		t.Fatalf("autonumber missing: %+v", m.LabelLines)
	}
	if !strings.HasPrefix(s.Messages[1].LabelLines[0], "2.") {
		t.Fatalf("autonumber continuation: %+v", s.Messages[1].LabelLines)
	}
	if s.Width <= m.Loop[1].X {
		t.Fatalf("self loop outside scene width %.1f", s.Width)
	}
}

func TestSequenceNoteSidesShiftIntoView(t *testing.T) {
	s := mustScene(t, "sequenceDiagram\n participant A\n participant B\n Note left of A: 左侧注释\n Note right of B: 右侧注释\n A->>B: hi\n")
	if len(s.Notes) != 2 {
		t.Fatalf("notes: %+v", s.Notes)
	}
	left, right := s.Notes[0], s.Notes[1]
	if left.Box.X < 0 {
		t.Fatalf("left note shifted outside: %+v", left.Box)
	}
	if right.Box.X < s.Participants[1].CenterX {
		t.Fatalf("right note not on the right side: %+v", right.Box)
	}
	if right.Box.X+right.Box.W > s.Width {
		t.Fatalf("right note outside scene: %+v scene=%.1f", right.Box, s.Width)
	}
	if left.Side != noteLeft || right.Side != noteRight {
		t.Fatalf("note sides: %+v %+v", left, right)
	}
}

func TestSequenceEntitiesAndComments(t *testing.T) {
	s := mustScene(t, "%% comment\nsequenceDiagram\n%% another\n A->>B: I #9829; you\n")
	if got := s.Messages[0].LabelLines[0]; got != "I ♥ you" {
		t.Fatalf("entity decoding: %q", got)
	}
}

func TestSequenceErrors(t *testing.T) {
	cases := []struct {
		name, src string
		line, col int
		contains  string
	}{
		{"missing header", "A->>B: hi\n", 1, 1, "unsupported diagram type"},
		{"loop", "sequenceDiagram\n loop every\n A->>B: hi\n end\n", 2, 2, "loop"},
		{"activate", "sequenceDiagram\n A->>B: hi\n activate B\n", 3, 2, "activation"},
		{"stereotype", "sequenceDiagram\n participant A@{\"type\":\"boundary\"}\n", 2, 14, "stereotype"},
		{"bidirectional", "sequenceDiagram\n A<<->>B: hi\n", 2, 2, "bidirectional"},
		{"async", "sequenceDiagram\n A-)B: hi\n", 2, 2, "asynchronous"},
		{"central", "sequenceDiagram\n A->>()B: hi\n", 2, 2, "central-lifeline"},
		{"no colon", "sequenceDiagram\n A->>B hi\n", 2, 2, "expected a message"},
		{"no arrow", "sequenceDiagram\n A B: hi\n", 2, 2, "expected an arrow"},
		{"create", "sequenceDiagram\n A->>B: hi\n create participant C\n", 3, 2, "create"},
		{"autonumber args", "sequenceDiagram\n autonumber 10 10\n", 2, 2, "autonumber arguments"},
		{"html", "sequenceDiagram\n A->>B: <script>x</script>\n", 2, 8, "HTML"},
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
			if tc.line > 0 && (d.Line != tc.line || d.Column != tc.col) {
				t.Fatalf("diagnostic at %d:%d, want %d:%d (%v)", d.Line, d.Column, tc.line, tc.col, d)
			}
			if !strings.Contains(d.Message, tc.contains) {
				t.Fatalf("message %q does not contain %q", d.Message, tc.contains)
			}
		})
	}
}

func TestSequenceBudgets(t *testing.T) {
	var b strings.Builder
	b.WriteString("sequenceDiagram\n")
	for i := 0; i < 21; i++ {
		b.WriteString("participant P" + itoa(i) + "\n")
	}
	if _, d := Render(context.Background(), b.String(), Options{}); d == nil || !strings.Contains(d.Message, "participant limit") {
		t.Fatalf("participant budget: %v", d)
	}
	var rows strings.Builder
	rows.WriteString("sequenceDiagram\n A->>B: hi\n")
	for i := 0; i < 300; i++ {
		rows.WriteString(" A->>B: more\n")
	}
	if _, d := Render(context.Background(), rows.String(), Options{}); d == nil || !strings.Contains(d.Message, "row limit") {
		t.Fatalf("row budget: %v", d)
	}
	if _, d := Render(context.Background(), "sequenceDiagram\n A->>B: hi\n", Options{Limits: Limits{Timeout: time.Nanosecond}}); d == nil || !strings.Contains(d.Message, "time budget") && !strings.Contains(d.Message, "canceled") {
		t.Fatalf("timeout: %v", d)
	}
}

func TestSequenceMultilineLongLabelWraps(t *testing.T) {
	src := "sequenceDiagram\n participant A\n participant B\n A->>B: " + strings.Repeat("长文本消息", 10) + "\n"
	s := mustScene(t, src)
	m := s.Messages[0]
	if len(m.LabelLines) < 2 {
		t.Fatalf("long message did not wrap: %+v", m.LabelLines)
	}
	if m.LabelBox.W > s.Width {
		t.Fatalf("wrapped label wider than scene: %+v", m.LabelBox)
	}
	for _, line := range m.LabelLines {
		if TextWidth(line, s.FontSize) > 320 {
			t.Fatalf("line exceeds wrap width: %q", line)
		}
	}
}
