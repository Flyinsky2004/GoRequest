package desktop

import (
	"fmt"
	"math"
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

// ui03Before reports whether a comes before b in reading order. Elements whose
// vertical spans overlap are on the same visual line (their text baselines can
// differ inside taller controls), so they are compared left to right; otherwise
// the upper element comes first.
func ui03Before(a, b ui.Rect) bool {
	if a.Y < b.Y+b.H && b.Y < a.Y+a.H {
		return a.X < b.X
	}
	return a.Y < b.Y
}

func ui03SameX(a, b ui.Rect) bool { return math.Abs(float64(a.X-b.X)) <= 0.5 }

// Render the production panel, including the actual content views, with the
// same constrained width that a split workspace gives the response panel. The
// single tool row must read: status group first, then Body/Headers/Cookies/
// Network, then find, copy and save, with the body-only Pretty/Raw/Tree group
// last and right-aligned (U01).
func TestUI03ResponseToolRowOrderAndAnchors(t *testing.T) {
	for _, language := range []language{langEnglish, langSimplifiedChinese} {
		for _, width := range []int{320, 480, 900} {
			t.Run(fmt.Sprintf("%s/%d", language, width), func(t *testing.T) {
				m, a, tab := ui03Fixture(t)
				m.cfg.cfg.Language = string(language)
				tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, width, 600)
				msg, nm := a.msg(), a.networkMsg()

				status := ui03Rect(t, tt, "200", width)
				body := ui03Rect(t, tt, msg.BodySection, width)
				headers := ui03Rect(t, tt, msg.HeadersSection, width)
				cookies := ui03Rect(t, tt, nm.CookiesSection, width)
				network := ui03Rect(t, tt, nm.NetworkSection, width)
				pretty := ui03Rect(t, tt, msg.Pretty, width)
				copyRect := ui03Rect(t, tt, msg.CopyResponse, width)
				saveRect := ui03Rect(t, tt, msg.SaveResponse, width)

				// The status capsule is the first element of the first line.
				if !ui03Before(status, body) || status.X > body.X {
					t.Fatalf("status is not the leading element: status=%+v body=%+v", status, body)
				}
				if !ui03Before(body, headers) || !ui03Before(headers, cookies) || !ui03Before(cookies, network) {
					t.Fatalf("view switch out of order: body=%+v headers=%+v cookies=%+v network=%+v",
						body, headers, cookies, network)
				}
				// The body-only format group follows the view switch; it can
				// never push the view switch sideways.
				if !ui03Before(network, pretty) {
					t.Fatalf("body format is not after the view switch: network=%+v pretty=%+v", network, pretty)
				}
				// Find/copy/save come before the body-format switch, which ends
				// the row (right-aligned; it wraps to its own line when narrow).
				if !ui03Before(copyRect, saveRect) || !ui03Before(saveRect, pretty) {
					t.Fatalf("row actions out of order: copy=%+v save=%+v pretty=%+v", copyRect, saveRect, pretty)
				}

				// The old second-row controls are gone; the Cookie count is part
				// of the view label and zero carries no suffix.
				for _, gone := range []string{fmt.Sprintf("%s (0)", nm.CookiesSection), nm.NetworkBtn, nm.CookiesSection + " · 0"} {
					if tt.HasText(gone) {
						t.Fatalf("obsolete second-row control %q still rendered; texts=%v", gone, tt.Texts())
					}
				}

				for _, r := range []ui.Rect{body, headers, cookies, network, pretty} {
					tt.Move(r.X+r.W/2, r.Y+r.H/2)
					if tt.Cursor() != ui.CursorPointer {
						t.Fatalf("segment cursor=%v at %+v", tt.Cursor(), r)
					}
				}
				t.Logf("width=%d language=%s status=%+v body=%+v headers=%+v cookies=%+v network=%+v pretty=%+v copy=%+v save=%+v",
					width, language, status, body, headers, cookies, network, pretty, copyRect, saveRect)
			})
		}
	}
}

// Preserve the old payload-state coverage: text, large, binary and long-status
// responses must all keep the status group leading, the view switch in order
// and every control inside the panel, while the body-only format group follows
// the response kind.
func TestUI03ResponseToolRowPayloadStates(t *testing.T) {
	for _, width := range []int{320, 900} {
		for _, payload := range []string{"text", "large", "binary", "long-status"} {
			t.Run(fmt.Sprintf("%d/%s", width, payload), func(t *testing.T) {
				_, a, tab := ui03Fixture(t)
				statusText := "200"
				switch payload {
				case "text":
					tab.Resp.Pretty = ""
					tab.Resp.Body = []byte("plain text")
				case "large":
					tab.Resp.BodySize = largeResponseThreshold + 1
					tab.RespRawIdx = 2
				case "binary":
					tab.Resp.IsBinary = true
					tab.Resp.ContentType = "application/octet-stream"
				case "long-status":
					statusText = "503"
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
				tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, width, 600)
				msg, nm := a.msg(), a.networkMsg()

				status := ui03Rect(t, tt, statusText, width)
				cookies := ui03Rect(t, tt, nm.CookiesSection, width)
				network := ui03Rect(t, tt, nm.NetworkSection, width)
				if !ui03Before(status, cookies) || status.X > cookies.X {
					t.Fatalf("status not leading: status=%+v cookies=%+v", status, cookies)
				}
				if !ui03Before(cookies, network) {
					t.Fatalf("view switch out of order: cookies=%+v network=%+v", cookies, network)
				}
				copyRect := ui03Rect(t, tt, msg.CopyResponse, width)
				saveRect := ui03Rect(t, tt, msg.SaveResponse, width)
				if !ui03Before(network, copyRect) || !ui03Before(copyRect, saveRect) {
					t.Fatalf("actions out of order: network=%+v copy=%+v save=%+v", network, copyRect, saveRect)
				}
				_, hasTree := tt.Find(nm.TreeMode)
				if payload == "binary" {
					if _, hasPretty := tt.Find(msg.Pretty); hasPretty {
						t.Fatal("binary response exposes a body format group")
					}
				} else {
					pretty := ui03Rect(t, tt, msg.Pretty, width)
					if !ui03Before(network, pretty) {
						t.Fatalf("format group not after the view switch: network=%+v pretty=%+v", network, pretty)
					}
					if (payload == "text" || payload == "large") && hasTree {
						t.Fatal("non-JSON/large response exposes Tree mode")
					}
				}
			})
		}
	}
}

// The Cookie view label carries the project Cookie Jar count only when it is
// non-zero, so an empty jar reads as the plain view name.
func TestUI03CookieCountSuffix(t *testing.T) {
	_, a, tab := ui03Fixture(t)
	jar := a.cookieJarFor(a.ps)
	for _, name := range []string{"a", "b"} {
		if err := jar.Upsert(core.CookieEntry{Domain: "api.example.com", Path: "/", Name: name, Value: "1"}); err != nil {
			t.Fatal(err)
		}
	}
	tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, 480, 600)
	nm := a.networkMsg()
	if !tt.HasText(nm.CookiesSection + " · 2") {
		t.Fatalf("Cookie view missing count suffix; texts=%v", tt.Texts())
	}
	if tt.HasText(nm.CookiesSection + " · 0") {
		t.Fatalf("zero count must carry no suffix; texts=%v", tt.Texts())
	}
	jar.Clear()
	tt.Frame()
	if !tt.HasText(nm.CookiesSection) || tt.HasText(nm.CookiesSection+" · 0") {
		t.Fatalf("empty jar label wrong; texts=%v", tt.Texts())
	}
}

// Switching views must not move the view switch or the status capsule: the
// body-only format group sits after them, so it cannot push them sideways, and
// top alignment keeps the first line stable when the row height changes.
func TestUI03ViewSwitchStaysAnchored(t *testing.T) {
	for _, width := range []int{320, 480, 900} {
		t.Run(fmt.Sprintf("%d", width), func(t *testing.T) {
			_, a, tab := ui03Fixture(t)
			tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, width, 600)
			msg, nm := a.msg(), a.networkMsg()
			labels := []string{msg.BodySection, msg.HeadersSection, nm.CookiesSection, nm.NetworkSection}
			anchors := make([]ui.Rect, len(labels))
			for i, label := range labels {
				anchors[i] = ui03Rect(t, tt, label, width)
			}
			status := ui03Rect(t, tt, "200", width)
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
					if !ui03SameX(r, anchors[i]) || math.Abs(float64(r.Y-anchors[i].Y)) > 0.5 {
						t.Fatalf("section=%d moved %s: %+v -> %+v", section, l, anchors[i], r)
					}
				}
				if got := ui03Rect(t, tt, "200", width); got != status {
					t.Fatalf("section=%d moved the status capsule: %+v -> %+v", section, status, got)
				}
				if section != 0 && tt.HasText(msg.Pretty) {
					t.Fatal("body format outside body section")
				}
			}
		})
	}
}

// The Cookie Jar entry lives inside the Cookies view and the Network/TLS
// settings entry inside the Network view; both stay reachable after the
// second tool row is removed.
func TestUI03CookieAndNetworkEntriesLiveInViews(t *testing.T) {
	_, a, tab := ui03Fixture(t)
	// Wide enough for the 680-DIP Cookie Jar modal to keep its Close button
	// inside the test window.
	tt := ui.NewTester(func(c *ui.Context) { a.responseView(c, c.Theme(), tab) }, 900, 600)
	msg, nm := a.msg(), a.networkMsg()

	if err := tt.Click(nm.CookiesSection); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(nm.OpenCookieJarBtn) {
		t.Fatalf("Cookie view has no Cookie Jar entry; texts=%v", tt.Texts())
	}
	if err := tt.Click(nm.OpenCookieJarBtn); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(nm.CookieJarTitle) {
		t.Fatalf("Cookie Jar modal did not open; texts=%v", tt.Texts())
	}
	if err := tt.Click(msg.Close); err != nil {
		t.Fatal(err)
	}

	if err := tt.Click(nm.NetworkSection); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(nm.NetworkBtn) {
		t.Fatalf("Network view has no settings entry; texts=%v", tt.Texts())
	}
	if err := tt.Click(nm.NetworkBtn); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText(nm.NetworkModalTitle) {
		t.Fatalf("Network settings modal did not open; texts=%v", tt.Texts())
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
		for _, label := range []string{m.BodySection, m.Pretty, m.CopyResponse, m.SaveResponse, nm.NetworkBtn} {
			if _, ok := tt.Find(label); ok {
				t.Fatalf("%s exposes response action %s", state, label)
			}
		}
		if state == "error" && !tt.HasText(m.Retry) {
			t.Fatal("retry missing")
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

// The two acceptance window sizes must keep the whole tool row inside the
// window with the status group leading, even though the response panel is
// narrower than the window.
func TestUI03ResponseToolRowAtAcceptanceSizes(t *testing.T) {
	for _, size := range []struct{ w, h int }{{1200, 820}, {1024, 640}} {
		t.Run(fmt.Sprintf("%dx%d", size.w, size.h), func(t *testing.T) {
			a, ps := openTestProject(t, fmt.Sprintf("UI03 %dx%d", size.w, size.h))
			tab := openRequestTabs(t, ps, "Acceptance")[0]
			tab.Resp = &core.ExecResult{
				Status: 200, Proto: "HTTP/1.1", Duration: 42 * time.Millisecond,
				Body:     []byte(`{"user":"alice","token":"demo"}`),
				Pretty:   "{\n  \"user\": \"alice\",\n  \"token\": \"demo\"\n}",
				BodySize: 34,
			}
			tab.respVersion = 1
			tt := ui.NewTester(a.View, size.w, size.h)
			tt.Frame()
			msg, nm := a.msg(), a.networkMsg()
			status, ok := tt.Find("200")
			if !ok {
				t.Fatalf("status missing; texts=%v", tt.Texts())
			}
			// "Body"/"Headers" also name request-editor tabs, so the unique
			// response labels anchor this check; the isolated panel test covers
			// the full Body/Headers/Cookies/Network order.
			for _, label := range []string{nm.CookiesSection, nm.NetworkSection, msg.Pretty, msg.CopyResponse, msg.SaveResponse} {
				r, ok := tt.Find(label)
				if !ok {
					t.Fatalf("%q missing; texts=%v", label, tt.Texts())
				}
				if r.X < 0 || r.X+r.W > float32(size.w)+0.5 || r.Y < 0 || r.Y+r.H > float32(size.h)+0.5 {
					t.Fatalf("%q outside %dx%d: %+v", label, size.w, size.h, r)
				}
			}
			cookies, _ := tt.Find(nm.CookiesSection)
			if !ui03Before(status, cookies) || status.X > cookies.X {
				t.Fatalf("status not leading: status=%+v cookies=%+v", status, cookies)
			}
		})
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
		status, ok := tt.Find("200")
		if !ok {
			t.Fatal("workspace response status missing")
		}
		if !ui03Before(status, r) {
			t.Fatalf("workspace %d: status is not leading: status=%+v cookies=%+v", width, status, r)
		}
		for _, label := range []string{a.networkMsg().CookiesSection, a.networkMsg().NetworkSection} {
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			next, _ := tt.Find(a.networkMsg().CookiesSection)
			if !ui03SameX(next, r) || math.Abs(float64(next.Y-r.Y)) > 0.5 {
				t.Fatalf("workspace %d: navigation moved %+v -> %+v", width, r, next)
			}
		}
		t.Logf("workspace width=%d status=%+v Cookies label bounds=%+v", width, status, r)
	}
}
