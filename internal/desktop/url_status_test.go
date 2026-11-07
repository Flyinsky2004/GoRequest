package desktop

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
	"gorequest/internal/desktop/uikit"
)

// U02: the request line must show the last send's outcome instead of the
// red-orange accent MyGo's TextInput paints while focused. These tests pin the
// state classification, the palette mapping, and the rendered border/focus
// behavior; they do not re-test the surrounding layout.

func TestURLSendStateOf(t *testing.T) {
	for _, tc := range []struct {
		name string
		tab  *Tab
		want urlSendState
	}{
		{"nil tab", nil, urlSendIdle},
		{"new tab", &Tab{}, urlSendIdle},
		{"sending", &Tab{Sending: true}, urlSendSending},
		{"sending wins over previous result", &Tab{Sending: true, Resp: &core.ExecResult{Status: 500}}, urlSendSending},
		{"200", &Tab{Resp: &core.ExecResult{Status: 200}}, urlSendSuccess},
		{"204", &Tab{Resp: &core.ExecResult{Status: 204}}, urlSendSuccess},
		{"302", &Tab{Resp: &core.ExecResult{Status: 302}}, urlSendSuccess},
		{"400", &Tab{Resp: &core.ExecResult{Status: 400}}, urlSendFailure},
		{"500", &Tab{Resp: &core.ExecResult{Status: 500}}, urlSendFailure},
		{"timeout", &Tab{RespErr: "context deadline exceeded"}, urlSendFailure},
		{"network", &Tab{RespErr: "dial tcp 127.0.0.1:1: connect: connection refused"}, urlSendFailure},
		{"tls", &Tab{RespErr: "net/http: TLS handshake timeout"}, urlSendFailure},
		{"canceled", &Tab{RespErr: "context canceled"}, urlSendCanceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := urlSendStateOf(tc.tab); got != tc.want {
				t.Fatalf("urlSendStateOf(%+v) = %v, want %v", tc.tab, got, tc.want)
			}
		})
	}
}

// TestURLSendStateRepeatedSends walks the same tab through success, HTTP
// failure, cancel, and success again: each completion must replace the previous
// state rather than leaving the first outcome stuck.
func TestURLSendStateRepeatedSends(t *testing.T) {
	tab := &Tab{}
	steps := []struct {
		name  string
		setup func()
		want  urlSendState
	}{
		{"success", func() { tab.Resp = &core.ExecResult{Status: 200} }, urlSendSuccess},
		{"http failure", func() { tab.Resp = &core.ExecResult{Status: 503} }, urlSendFailure},
		{"cancel", func() { tab.Resp = nil; tab.RespErr = "context canceled" }, urlSendCanceled},
		{"success again", func() { tab.RespErr = ""; tab.Resp = &core.ExecResult{Status: 201} }, urlSendSuccess},
	}
	for _, step := range steps {
		step.setup()
		if got := urlSendStateOf(tab); got != step.want {
			t.Fatalf("%s: state = %v, want %v", step.name, got, step.want)
		}
	}
}

func TestURLStatusBorderColor(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    uikit.Palette
	}{
		{"light", uikit.PaletteFor(nil)},
		{"dark", uikit.PaletteFor(ui.DarkTheme())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := tc.p
			for _, c := range []struct {
				state urlSendState
				want  ui.Color
			}{
				{urlSendIdle, p.Hairline},
				{urlSendSending, p.Muted},
				{urlSendSuccess, uikit.MethodColor("GET", p)},
				{urlSendFailure, uikit.MethodColor("DELETE", p)},
				{urlSendCanceled, p.Muted},
			} {
				if got := urlStatusBorderColor(c.state, p); got != c.want {
					t.Fatalf("state %v: color = %+v, want %+v", c.state, got, c.want)
				}
			}
			// Success and failure must be visibly different, not two shades of
			// the same accent.
			success := urlStatusBorderColor(urlSendSuccess, p)
			failure := urlStatusBorderColor(urlSendFailure, p)
			if success == failure {
				t.Fatal("success and failure share one border color")
			}
			// Neither terminal color may be the accent that caused the bug.
			accent := p.Accent
			if success == accent || failure == accent {
				t.Fatal("a send state still uses the focus accent")
			}
		})
	}
}

// TestURLGroupBorderReflectsSendState renders the real urlGroup and samples the
// group's own border pixel. The inner URL input must not repaint it with the
// focus accent.
func TestURLGroupBorderReflectsSendState(t *testing.T) {
	p := uikit.PaletteFor(nil)
	for _, tc := range []struct {
		name  string
		setup func(*Tab)
		want  ui.Color
	}{
		{"idle", func(*Tab) {}, p.Hairline},
		{"sending", func(tab *Tab) { tab.Sending = true }, p.Muted},
		{"success", func(tab *Tab) { tab.Resp = &core.ExecResult{Status: 200} }, uikit.MethodColor("GET", p)},
		{"http failure", func(tab *Tab) { tab.Resp = &core.ExecResult{Status: 500} }, uikit.MethodColor("DELETE", p)},
		{"network failure", func(tab *Tab) { tab.RespErr = "dial tcp: connection refused" }, uikit.MethodColor("DELETE", p)},
		{"cancel", func(tab *Tab) { tab.RespErr = "context canceled" }, p.Muted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, ps := openTestProject(t, "URLStatus-"+tc.name)
			tab := openRequestTabs(t, ps, "Req")[0]
			tab.Method = "GET"
			tab.URL = "https://example.com/api"
			tc.setup(tab)

			tt := ui.NewTester(func(c *ui.Context) {
				ui.Column(c).Padding(20).Children(func() {
					a.urlGroup(c, c.Theme(), tab)
				})
			}, 1000, 200)
			tt.Frame()

			grp, ok := tt.Find("request URL group")
			if !ok {
				t.Fatalf("request URL group not found; texts=%v", tt.Texts())
			}
			got := tt.Image().RGBAAt(int(grp.X+grp.W/2), int(grp.Y))
			want := rgbOf(tc.want.Over(p.BG))
			if !colorClose(got, want, 10) {
				t.Fatalf("group border = %+v, want %+v (state %s)", got, want, tc.name)
			}
		})
	}
}

// TestURLGroupBorderUpdatesOnRepeatedSend keeps one rendered group and walks
// it through sending, success, HTTP failure, and cancel. Each frame must show
// the new state; a previous result may not stick.
func TestURLGroupBorderUpdatesOnRepeatedSend(t *testing.T) {
	p := uikit.PaletteFor(nil)
	a, ps := openTestProject(t, "URLStatusRepeat")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Method = "GET"
	tab.URL = "https://example.com/api"

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			a.urlGroup(c, c.Theme(), tab)
		})
	}, 1000, 200)
	tt.Frame()
	grp, ok := tt.Find("request URL group")
	if !ok {
		t.Fatalf("request URL group not found; texts=%v", tt.Texts())
	}
	border := func() color.RGBA {
		return tt.Image().RGBAAt(int(grp.X+grp.W/2), int(grp.Y))
	}

	steps := []struct {
		name  string
		setup func()
		want  ui.Color
	}{
		{"sending", func() { tab.Sending = true }, p.Muted},
		{"success", func() { tab.Sending = false; tab.Resp = &core.ExecResult{Status: 200} }, uikit.MethodColor("GET", p)},
		{"http failure", func() { tab.Resp = &core.ExecResult{Status: 503} }, uikit.MethodColor("DELETE", p)},
		{"cancel", func() { tab.Resp = nil; tab.RespErr = "context canceled" }, p.Muted},
		{"success again", func() { tab.RespErr = ""; tab.Resp = &core.ExecResult{Status: 201} }, uikit.MethodColor("GET", p)},
	}
	for _, step := range steps {
		step.setup()
		tt.Frame()
		if got, want := border(), rgbOf(step.want.Over(p.BG)); !colorClose(got, want, 10) {
			t.Fatalf("%s: border = %+v, want %+v", step.name, got, want)
		}
	}
}

// TestURLGroupFocusKeepsStatusAndRing focuses the URL input and checks three
// things at once: the group border keeps the success color, the input's own
// edge is not the accent border MyGo used to paint, and a focus ring still
// appears outside the group.
func TestURLGroupFocusKeepsStatusAndRing(t *testing.T) {
	p := uikit.PaletteFor(nil)
	a, ps := openTestProject(t, "URLStatusFocus")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Method = "GET"
	tab.URL = "https://example.com/api"
	tab.Resp = &core.ExecResult{Status: 200}

	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(20).Children(func() {
			a.urlGroup(c, c.Theme(), tab)
		})
	}, 1000, 200)
	tt.Frame()

	grp, ok := tt.Find("request URL group")
	if !ok {
		t.Fatalf("request URL group not found; texts=%v", tt.Texts())
	}
	urlRect, ok := tt.Find("request URL")
	if !ok {
		t.Fatalf("URL input not found; texts=%v", tt.Texts())
	}

	ringX, ringY := int(grp.X+grp.W/2), int(grp.Y)-2
	beforeRing := tt.Image().RGBAAt(ringX, ringY)

	tt.ClickAt(urlRect.X+urlRect.W/2, urlRect.Y+urlRect.H/2)
	tt.Frame()

	// The status border survives focus.
	got := tt.Image().RGBAAt(int(grp.X+grp.W/2), int(grp.Y))
	want := rgbOf(uikit.MethodColor("GET", p).Over(p.BG))
	if !colorClose(got, want, 10) {
		t.Fatalf("focused group border = %+v, want success green %+v", got, want)
	}

	// The input itself has no accent border: its top edge is the Sidebar
	// surface, not the theme accent.
	inputTop := tt.Image().RGBAAt(int(urlRect.X+urlRect.W/2), int(urlRect.Y))
	accent := rgbOf(p.Accent.Over(p.Sidebar))
	if colorClose(inputTop, accent, 10) {
		t.Fatalf("URL input still paints an accent border: %+v", inputTop)
	}

	// Focus remains visible as a neutral ring outside the group, not the
	// accent red-orange that made the field look like an error.
	afterRing := tt.Image().RGBAAt(ringX, ringY)
	if beforeRing == afterRing {
		t.Fatalf("focus ring not visible above the group: %+v", afterRing)
	}
	if wantRing := rgbOf(p.Muted.Over(p.BG)); !colorClose(afterRing, wantRing, 20) {
		t.Fatalf("focus ring = %+v, want neutral %+v", afterRing, wantRing)
	}
	if accentRing := rgbOf(p.Accent.Over(p.BG)); colorClose(afterRing, accentRing, 10) {
		t.Fatalf("focus ring still uses the accent red-orange: %+v", afterRing)
	}
}

func rgbOf(c ui.Color) color.RGBA {
	return color.RGBA{R: c.R, G: c.G, B: c.B, A: c.A}
}

func colorClose(a, b color.RGBA, tol int) bool {
	diff := func(x, y uint8) int {
		if x > y {
			return int(x - y)
		}
		return int(y - x)
	}
	return diff(a.R, b.R) <= tol && diff(a.G, b.G) <= tol && diff(a.B, b.B) <= tol
}
