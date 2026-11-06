package desktop

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	"github.com/egoist/mygo/ui"
	"gorequest/internal/desktop/uikit"
)

// The bars are injected native API fixtures, not measurements of a live Mac.
// Include full screen, a usual compact band, and taller AppKit bands.
func TestBrandChromeGeometry(t *testing.T) {
	for _, width := range []int{220, 280, 380} {
		for _, band := range []float32{0, 22, 28, 38} {
			for _, zoom := range []float32{0.8, 1, 1.5} {
				for _, dark := range []bool{false, true} {
					t.Run(fmt.Sprintf("w%d/band%g/zoom%g/dark%v", width, band, zoom, dark), func(t *testing.T) {
						a := &App{}
						tt := ui.NewTester(func(c *ui.Context) {
							ui.Column(c).Width(float32(width)).Children(func() {
								a.navHeader(c, uikit.PaletteFor(c.Theme()))
								ui.Box(c).Height(1).FillWidth().Label("brand separator")
							})
						}, int(math.Ceil(float64(float32(width)*zoom))), 180)
						tt.SetTitleBar(ui.TitleBar{Height: band, Left: 70})
						tt.SetZoom(zoom)
						tt.SetScale(2)
						tt.SetDark(dark)
						tt.SetLocale("zh-CN")
						r, ok := tt.Find("GoRequest")
						if !ok {
							t.Fatal("missing wordmark")
						}
						line, ok := tt.Find("brand separator")
						if !ok {
							t.Fatal("missing separator")
						}
						safe := float32(0)
						if runtime.GOOS == "darwin" {
							safe = band
						}
						above, below := r.Y-safe, line.Y-r.Y-r.H
						if above < 2*zoom || below < 2*zoom {
							t.Fatalf("wordmark overlaps/presses safe band or separator: text=%+v line=%+v safe=%g gaps=%g/%g", r, line, safe, above, below)
						}
						if runtime.GOOS == "darwin" && float32(math.Abs(float64(above-below))) > 1.1*zoom {
							t.Fatalf("unbalanced wordmark: above=%g below=%g", above, below)
						}
						if r.X < 11*zoom || r.X+r.W > float32(width-12)*zoom+0.1 {
							t.Fatalf("wordmark outside sidebar: %+v width=%d zoom=%g", r, width, zoom)
						}
						// A compact band must not displace Requests/search below the old header.
						if (runtime.GOOS != "darwin" || band/zoom <= 22) && math.Abs(float64(line.Y-52*zoom)) > 0.1 {
							t.Fatalf("compact header moved downstream content: separator=%+v", line)
						}
						if width == 220 && band == 22 && zoom == 1 && !dark {
							shaped := ui.ShapeText("GoRequest", ui.Font{Size: 17, Weight: 700}, r.W+1)
							t.Logf("text=%+v separatorY=%g safeBand=%g gaps=%g/%g shapedLines=%+v", r, line.Y, safe, above, below, shaped.Lines())
						}
					})
				}
			}
		}
	}
}

// Exercise the actual sidebar below the header, in both languages, without
// opening windows or touching the user's preferences.
func TestBrandChromeSidebarControls(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		for _, width := range []float32{220, 380} {
			t.Run(fmt.Sprintf("%s/%g", lang, width), func(t *testing.T) {
				m, _ := newTestManager(t)
				m.cfg.cfg.Language = string(lang)
				if err := m.openProject(writeProject(t, "Brand chrome")); err != nil {
					t.Fatal(err)
				}
				a := m.workspaces[0]
				a.navWidth = width
				a.filterFocus = true
				tt := ui.NewTester(func(c *ui.Context) { a.nav(c, c.Theme()) }, int(width), 640)
				tt.SetTitleBar(ui.TitleBar{Height: 22, Left: 70})
				brand, _ := tt.Find("GoRequest")
				requests, ok := tt.Find(a.msg().RequestsGroup)
				if !ok || requests.Y <= brand.Y+brand.H {
					t.Fatalf("Requests obscured: brand=%+v requests=%+v found=%v", brand, requests, ok)
				}
				tt.Type("中文")
				if a.filterQuery != "中文" {
					t.Fatalf("search input unavailable: %q", a.filterQuery)
				}
				if err := tt.Click(a.msg().Settings); err != nil {
					t.Fatal(err)
				}
				if !a.showSettings {
					t.Fatal("settings click unavailable")
				}
			})
		}
	}
}
