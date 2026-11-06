package desktop

import (
	"fmt"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/core"
)

func ui03Fixture(t *testing.T) (*Manager, *App, *Tab) {
	t.Helper()
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "UI03")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	tab := &Tab{Owner: a.ps, Resp: &core.ExecResult{Status: 200, Proto: "HTTP/1.1", Body: []byte(`{"ok":true}`), Pretty: "{\n  \"ok\": true\n}", BodySize: 11, Headers: [][2]string{{"X-Test", "visible header"}}}}
	return m, a, tab
}

func ui03Rect(t *testing.T, tt *ui.Tester, label string, width int) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok {
		t.Fatalf("missing %q; texts=%v", label, tt.Texts())
	}
	if r.X < 0 || r.Y < 0 || r.X+r.W > float32(width)+0.5 || r.Y+r.H > 600 {
		t.Fatalf("%q outside %d×600: %+v", label, width, r)
	}
	return r
}

// Render the production panel, including the actual content views, with the
// same constrained width that a split workspace gives the response panel.
func TestUI03ResponseNavigationAnchors(t *testing.T) {
	for _, language := range []language{langEnglish, langSimplifiedChinese} {
		for _, width := range []int{320, 480, 900} {
			t.Run(fmt.Sprintf("%s/%d", language, width), func(t *testing.T) {
				m, a, tab := ui03Fixture(t)
				m.cfg.cfg.Language = string(language)
				tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, width, 600)
				msg, nm := a.msg(), a.networkMsg()
				labels := []string{msg.BodySection, msg.HeadersSection, nm.CookiesSection, nm.NetworkSection}
				anchors := make([]ui.Rect, len(labels))
				for i, label := range labels {
					anchors[i] = ui03Rect(t, tt, label, width)
				}
				for _, payload := range []string{"json", "text", "large", "binary", "long-status"} {
					switch payload {
					case "text":
						tab.Resp.Pretty = ""
						tab.Resp.Body = []byte("plain text")
					case "large":
						tab.Resp.Pretty = `{"ok":true}`
						tab.Resp.BodySize = largeResponseThreshold + 1
						tab.RespRawIdx = 2
					case "binary":
						tab.Resp.IsBinary = true
						tab.Resp.ContentType = "application/octet-stream"
					case "long-status":
						tab.Resp.IsBinary = false
						tab.Resp.Status = 503
						tab.Resp.Duration = 999999 * time.Millisecond
						tab.Resp.BodySize = 999999999
						tab.Resp.TransferSize = 999999
						tab.Resp.Decompressed = true
						tab.Resp.ContentEncoding = "gzip"
						tab.Resp.Redirects = make([]core.RedirectHop, 12)
						tab.Resp.Truncated = true
						tab.Resp.Trace = &core.NetworkTrace{TLSInsecureSkipVerify: true}
					}
					tab.respVersion++
					for section, label := range labels {
						tt.Frame()
						if err := tt.Click(label); err != nil {
							t.Fatal(err)
						}
						if tab.RespSection != section {
							t.Fatalf("%s click selected %d", label, tab.RespSection)
						}
						for i, l := range labels {
							r := ui03Rect(t, tt, l, width)
							if r != anchors[i] {
								t.Fatalf("%s section=%d moved %s: %+v -> %+v", payload, section, l, anchors[i], r)
							}
							tt.Move(r.X+r.W/2, r.Y+r.H/2)
							if tt.Cursor() != ui.CursorPointer {
								t.Fatalf("%s cursor=%v", l, tt.Cursor())
							}
						}
						// Every action remains inside the constrained panel;
						// settings cannot share/overlap the navigation line.
						copyRect := ui03Rect(t, tt, msg.CopyResponse, width)
						saveRect := ui03Rect(t, tt, msg.SaveResponse, width)
						jarRect := ui03Rect(t, tt, fmt.Sprintf(nm.CookieJarBtnFmt, 0), width)
						networkRect := ui03Rect(t, tt, nm.NetworkBtn, width)
						if jarRect.Y < copyRect.Y+copyRect.H || networkRect.Y < saveRect.Y+saveRect.H {
							t.Fatal("status/settings overlap response actions")
						}
						if section != 0 && tt.HasText(msg.Pretty) {
							t.Fatal("body formatting outside body section")
						}
					}
				}
				t.Logf("width=%d language=%s category label bounds=%+v", width, language, anchors)
			})
		}
	}
}

func TestUI03ResponseToolbarInteractionAndUnavailable(t *testing.T) {
	_, a, tab := ui03Fixture(t)
	tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, 480, 600)
	m, nm := a.msg(), a.networkMsg()
	if err := tt.Click(m.Raw); err != nil {
		t.Fatal(err)
	}
	if tab.RespRawIdx != 1 {
		t.Fatal("raw mode not selected")
	}
	if err := tt.Click(m.CopyResponse); err != nil {
		t.Fatal(err)
	}
	if tt.Clipboard() != string(tab.Resp.Body) {
		t.Fatal("raw copy changed payload")
	}
	if err := tt.Click(nm.TreeMode); err != nil {
		t.Fatal(err)
	}
	if tab.RespRawIdx != 2 || !tt.HasText(nm.ExpandAllBtn) {
		t.Fatal("tree mode unavailable")
	}
	if err := tt.Click(m.FindPlaceholder); err != nil {
		t.Fatal(err)
	}
	if !tab.respFindOpen {
		t.Fatal("search not opened")
	}
	// The segmented group retains MyGo radio keyboard navigation.
	if err := tt.Click(m.BodySection); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyRight)
	if tab.RespSection != 1 {
		t.Fatalf("keyboard did not select headers: %d", tab.RespSection)
	}
	if !tt.HasText("visible header") {
		t.Fatal("header content missing")
	}
	for _, state := range []string{"sending", "error", "empty"} {
		tab.Sending = state == "sending"
		tab.RespErr = ""
		if state == "error" {
			tab.RespErr = "UI03 failed"
		}
		if state == "empty" {
			tab.Resp = nil
		}
		tt.Frame()
		for _, label := range []string{m.BodySection, m.Pretty, m.CopyResponse, m.SaveResponse} {
			if _, ok := tt.Find(label); ok {
				t.Fatalf("%s exposes response action %s", state, label)
			}
		}
		if state == "error" && !tt.HasText(m.Retry) {
			t.Fatal("retry missing")
		}
		for _, label := range []string{fmt.Sprintf(nm.CookieJarBtnFmt, 0), nm.NetworkBtn} {
			r := ui03Rect(t, tt, label, 480)
			tt.Move(r.X+r.W/2, r.Y+r.H/2)
			if tt.Cursor() != ui.CursorPointer {
				t.Fatalf("%s cursor=%v", label, tt.Cursor())
			}
		}
	}
}

// Check actual painted hover feedback, rather than merely checking a style
// assignment, in both native theme variants.
func TestUI03ResponseSegmentHoverPixels(t *testing.T) {
	for _, dark := range []bool{false, true} {
		_, a, tab := ui03Fixture(t)
		tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, 480, 600)
		tt.SetDark(dark)
		tt.Move(470, 590)
		r := ui03Rect(t, tt, a.msg().HeadersSection, 480)
		before := append([]byte(nil), tt.Image().Pix...)
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		after := tt.Image()
		changed := false
		for y := int(r.Y) - 3; y < int(r.Y+r.H)+3; y++ {
			for x := int(r.X) - 3; x < int(r.X+r.W)+3; x++ {
				offset := y*after.Stride + x*4
				if before[offset] != after.Pix[offset] || before[offset+1] != after.Pix[offset+1] || before[offset+2] != after.Pix[offset+2] {
					changed = true
				}
			}
		}
		if !changed {
			t.Fatalf("dark=%v: segment has no painted hover feedback", dark)
		}
		if tab.RespSection != 0 {
			t.Fatal("hover changed selection")
		}
	}
}

func TestUI03ResponseAnchorsInWorkspace(t *testing.T) {
	a, ps := openTestProject(t, "UI03 workspace")
	tab := openRequestTabs(t, ps, "UI03 request")[0]
	tab.Resp = &core.ExecResult{Status: 200, Proto: "HTTP/2", Body: []byte("ok"), BodySize: 2}
	for _, width := range []int{760, 1200} {
		tt := ui.NewTester(a.View, width, 820)
		tt.Advance(5 * time.Second)
		r, ok := tt.Find(a.networkMsg().CookiesSection)
		if !ok {
			t.Fatal("workspace response switch missing")
		}
		for _, label := range []string{a.networkMsg().CookiesSection, a.networkMsg().NetworkSection} {
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			next, _ := tt.Find(a.networkMsg().CookiesSection)
			if next != r {
				t.Fatalf("workspace %d: navigation moved %+v -> %+v", width, r, next)
			}
		}
		t.Logf("workspace width=%d Cookies label bounds=%+v", width, r)
	}
}
