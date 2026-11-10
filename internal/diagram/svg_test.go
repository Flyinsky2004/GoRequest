package diagram

import (
	"context"
	"strings"
	"testing"
)

func mustSVG(t *testing.T, src string) (string, *Scene) {
	t.Helper()
	s := mustScene(t, src)
	svg, d := RenderSVG(s, LightSVGTheme(), DefaultLimits)
	if d != nil {
		t.Fatalf("RenderSVG: %v", d)
	}
	return svg, s
}

func TestSVGFlowchartSelfContained(t *testing.T) {
	svg, s := mustSVG(t, "flowchart TD\n A[\"中文 & 不是标签\"] -->|是| B{判断}\n B -.-> C((圆))\n")
	if !strings.HasPrefix(svg, "<svg ") || !strings.HasSuffix(svg, "</svg>\n") {
		t.Fatalf("svg wrapper: %.80q", svg)
	}
	if !strings.Contains(svg, `role="img"`) || !strings.Contains(svg, "<title>flowchart TD</title>") {
		t.Fatalf("accessible wrapper missing")
	}
	if !strings.Contains(svg, num(s.Width)) || !strings.Contains(svg, num(s.Height)) {
		t.Fatalf("intrinsic size missing")
	}
	for _, bad := range []string{"<script", "javascript:", "href=", "xlink", "foreignObject", "url(", "@import", "<image"} {
		if strings.Contains(strings.ToLower(svg), bad) {
			t.Fatalf("unsafe or external construct %q in SVG: %s", bad, svg)
		}
	}
	if !strings.Contains(svg, "中文 &amp; 不是标签") {
		t.Fatalf("escaped CJK label missing: %s", svg)
	}
	if !strings.Contains(svg, "<polygon") || !strings.Contains(svg, "<ellipse") {
		t.Fatalf("shapes missing: %s", svg)
	}
	if !strings.Contains(svg, "stroke-dasharray") {
		t.Fatalf("dotted edge style missing: %s", svg)
	}
}

func TestSVGSequenceSelfContained(t *testing.T) {
	svg, _ := mustSVG(t, "sequenceDiagram\n participant A as 客户端\n participant B as 服务端\n Note over A,B: 说明\n A->>B: 请求\n B-->>A: 响应\n A->>A: 自调用\n")
	for _, want := range []string{"客户端", "服务端", "说明", "请求", "响应", "自调用", "<polyline"} {
		if !strings.Contains(svg, want) {
			t.Fatalf("sequence SVG misses %q", want)
		}
	}
	for _, bad := range []string{"<script", "href=", "foreignObject"} {
		if strings.Contains(strings.ToLower(svg), bad) {
			t.Fatalf("unsafe construct %q: %s", bad, svg)
		}
	}
}

func TestSVGSizeLimit(t *testing.T) {
	s := mustScene(t, "flowchart TD\n A --> B\n")
	if _, d := RenderSVG(s, LightSVGTheme(), Limits{MaxSVGBytes: 10}); d == nil || !strings.Contains(d.Message, "SVG is") {
		t.Fatalf("SVG byte budget: %v", d)
	}
}

func TestSVGDarkThemeUsesExplicitColors(t *testing.T) {
	s := mustScene(t, "flowchart LR\n A --> B\n")
	svg, d := RenderSVG(s, DarkSVGTheme(), DefaultLimits)
	if d != nil {
		t.Fatal(d)
	}
	if !strings.Contains(svg, DarkSVGTheme().Background) || !strings.Contains(svg, DarkSVGTheme().Text) {
		t.Fatalf("dark theme colors missing: %s", svg)
	}
}

func TestSecurityNoScriptRenderedFromText(t *testing.T) {
	// Angle brackets are rejected at parse time; a rejected diagram keeps
	// its source and never reaches the SVG serializer.
	_, d := Render(context.Background(), "flowchart TD\n A[\"<img src=x onerror=alert(1)>\"]\n", Options{})
	if d == nil || !strings.Contains(d.Message, "HTML") {
		t.Fatalf("HTML label not rejected: %v", d)
	}
	_, d = Render(context.Background(), "flowchart TD\n A[\"javascript:alert(1)\"]\n", Options{})
	if d != nil {
		t.Fatalf("plain text label must parse: %v", d)
	}
	s, _ := Render(context.Background(), "flowchart TD\n A[\"javascript:alert(1)\"]\n", Options{})
	svg, d := RenderSVG(s, LightSVGTheme(), DefaultLimits)
	if d != nil {
		t.Fatal(d)
	}
	if strings.Contains(svg, "href") || strings.Contains(svg, "<a ") {
		t.Fatalf("label must never become a link: %s", svg)
	}
}
