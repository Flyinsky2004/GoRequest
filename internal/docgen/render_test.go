package docgen

import (
	"strings"
	"testing"
)

// All tests in this package are authored but NOT executed in phase 1; the
// post-integration verification run executes them with the go toolchain.

func TestRenderHTMLHeadingAnchorsAndCards(t *testing.T) {
	src := "# 标题\n\n正文。\n\n#### `users.me` — GET /users/me\n\n正文。\n"
	out := RenderHTML(src, RenderOptions{Cards: []CardSite{{RequestID: "users.me", HeadingLine: 5}}})
	if !strings.Contains(out, `<h4 id="docgen-request-users-me" class="docgen-card">`) {
		t.Fatalf("card anchor missing: %s", out)
	}
	if !strings.Contains(out, `<h1 id="标题">`) {
		t.Fatalf("slug anchor missing: %s", out)
	}
}

func TestRenderHTMLLinkClasses(t *testing.T) {
	src := "" +
		"[卡](gorequest://request/users.me)\n\n" +
		"[坏](gorequest://request/a b)\n\n" +
		"[外](https://example.com/x)\n\n" +
		"[危](javascript:alert(1))\n\n" +
		"[下](other.md#锚)\n"
	out := RenderHTML(src, RenderOptions{})
	if !strings.Contains(out, `<a href="#docgen-request-users-me">卡</a>`) {
		t.Fatalf("gorequest link not anchored: %s", out)
	}
	if !strings.Contains(out, `class="docgen-badref"`) || strings.Contains(out, `href="gorequest://request/a b"`) {
		t.Fatalf("malformed gorequest link must not produce an href: %s", out)
	}
	if !strings.Contains(out, `<a href="https://example.com/x">外</a>`) {
		t.Fatalf("external link lost: %s", out)
	}
	if strings.Contains(out, "javascript:") {
		t.Fatalf("dangerous scheme leaked: %s", out)
	}
	if !strings.Contains(out, `<a href="other.html#%E9%94%9A">下</a>`) {
		t.Fatalf("doc link not converted to .html: %s", out)
	}
}

func TestRenderHTMLImages(t *testing.T) {
	src := "![本地图](assets/local.png)\n\n![远程图](https://example.com/r.png)\n\n![缺失](assets/gone.png)\n"
	remote := 0
	out := RenderHTML(src, RenderOptions{
		ResolveImage: func(sourceRel, dest string) (string, bool, string) {
			switch dest {
			case "assets/local.png":
				return "docs/assets/local.png", true, ""
			default:
				remote++
				return "", false, "attachment not available; alt text only"
			}
		},
	})
	if !strings.Contains(out, `<img src="docs/assets/local.png" alt="本地图">`) {
		t.Fatalf("local image wrong: %s", out)
	}
	if strings.Contains(out, `src="http`) {
		t.Fatalf("remote image must never embed a URL: %s", out)
	}
	if strings.Count(out, "docgen-remote-img") != 2 {
		t.Fatalf("missing/remote images must each carry a note: %s", out)
	}
	if !strings.Contains(out, "<em>[远程图]</em>") || !strings.Contains(out, "<em>[缺失]</em>") {
		t.Fatalf("alt text degraded: %s", out)
	}
}

func TestRenderHTMLTablesAndLists(t *testing.T) {
	src := "" +
		"| 名称 | 值 |\n| --- | ---: |\n| a | 1 |\n\n" +
		"- 甲\n- 乙\n  - 乙一\n- {{secret.x}}\n\n" +
		"- [x] 完成\n- [ ] 待办\n"
	out := RenderHTML(src, RenderOptions{})
	if !strings.Contains(out, "<th>名称</th>") || !strings.Contains(out, `align="right"`) {
		t.Fatalf("table header/alignment wrong: %s", out)
	}
	if strings.Count(out, "<td>a</td>") != 1 {
		t.Fatalf("table cell wrong: %s", out)
	}
	// Nested list: the inner list is inside the li of 乙.
	if !strings.Contains(out, "<li>\n乙\n<ul>\n<li>乙一</li>\n</ul>\n</li>") {
		t.Fatalf("nested list structure wrong: %s", out)
	}
	if !strings.Contains(out, "&#9745;") || !strings.Contains(out, "&#9744;") {
		t.Fatalf("task glyphs missing: %s", out)
	}
}

func TestRenderHTMLMermaidAndHTMLBlockStaysInert(t *testing.T) {
	src := "before\n\n<script>alert(1)</script>\n\n```mermaid\ngraph TD\n\tA-->B\n```\n\nafter\n"
	out := RenderHTML(src, RenderOptions{})
	if strings.Contains(out, "<script>") {
		t.Fatalf("script tag leaked: %s", out)
	}
	if !strings.Contains(out, "&lt;script&gt;alert(1)&lt;/script&gt;") {
		t.Fatalf("HTML block must be shown escaped as source: %s", out)
	}
	if !strings.Contains(out, "docgen-mermaid-note") {
		t.Fatalf("mermaid warning missing: %s", out)
	}
	if !strings.Contains(out, "graph TD") || !strings.Contains(out, "language-mermaid") {
		t.Fatalf("mermaid source must be kept verbatim: %s", out)
	}
}

func TestRenderHTMLDocumentWrapper(t *testing.T) {
	body := "<p>内容</p>\n"
	out := RenderHTMLDocument("文档 标题", body)
	if !strings.HasPrefix(out, "<!doctype html>") || !strings.Contains(out, `<meta charset="utf-8">`) {
		t.Fatalf("document wrapper wrong: %s", out)
	}
	if strings.Contains(out, "<link") || strings.Contains(out, "<script") {
		t.Fatalf("external dependencies leaked into the wrapper: %s", out)
	}
	if !strings.Contains(out, "<title>文档 标题</title>") {
		t.Fatalf("escaped title missing: %s", out)
	}
}
