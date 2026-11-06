package uikit

import (
	"fmt"
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestUI01TabStripGeometryAndNativeScrolling(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			theme := ui.LightTheme()
			if dark {
				theme = ui.DarkTheme()
			}
			p := PaletteFor(theme)
			var state *ui.ScrollState
			closedTab := -1
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(theme)
				ui.Column(c).Fill().Children(func() {
					ui.Row(c).FillWidth().Children(func() {
						scroll := TabScroll(c, theme, func() {
							ui.Row(c).Gap(4).Children(func() {
								for i := 0; i < 8; i++ {
									_, _, closed := TabItem(c, p, i, TabSpec{Method: "GET", Title: fmt.Sprintf("标签 Request %d", i), Active: i == 0, Dirty: i == 1}, fmt.Sprint("close", i))
									if closed {
										closedTab = i
									}
								}
							})
						})
						scroll.Label("strip")
						state = ui.Local(scroll, "tab-scroll", func() ui.ScrollState { return ui.ScrollState{} })
					})
					ui.Text(c, "below").Label("below")
				})
			}, 320, 120)
			tt.Frame()
			r, _ := tt.Find("strip")
			close, ok := tt.Find("close0")
			if !ok || close.W != 24 || close.H != 24 || close.Y < r.Y+12 || close.Y+close.H > r.Y+r.H {
				t.Fatalf("close target escaped content: close=%+v scroll=%+v", close, r)
			}
			label, _ := tt.Find("GET 标签 Request 0")
			if math.Abs(float64(label.Y+label.H/2-close.Y-close.H/2)) > .5 {
				t.Fatalf("centers differ: title=%+v close=%+v", label, close)
			}
			// On an overflowing strip only the top centered 3 DIP indicator
			// is painted; the remaining hit band is visually empty.
			im := tt.Image()
			background := im.RGBAAt(319, 0)
			painted := 0
			for y := 0; y < 12; y++ {
				if im.RGBAAt(8, y) != background {
					painted++
				}
			}
			if painted < 3 || painted > 4 {
				t.Fatalf("indicator thickness=%d pixels", painted)
			}
			for _, y := range []int{0, 1, 2, 9, 10, 11} {
				if im.RGBAAt(8, y) != background {
					t.Fatalf("paint outside thin indicator at y=%d", y)
				}
			}
			if im.RGBAAt(8, 36) == background {
				t.Fatal("active underline is obscured")
			}
			below, _ := tt.Find("below")
			if r.H >= 40 || below.Y < r.Y+r.H {
				t.Fatalf("strip/content boundary: scroll=%+v below=%+v", r, below)
			}
			tt.Move(close.X+12, close.Y+12)
			if tt.Cursor() != ui.CursorPointer {
				t.Fatal("close lacks pointer cursor")
			}
			// The entire 12 DIP band, including well outside the 3 DIP paint,
			// must capture the native thumb drag without activating a tab.
			for _, y := range []float32{1, 11} {
				tt.SetSize(320, 120)
				// Reset to the left with the real scroller's tracked state.
				state.X = 0
				tt.Frame()
				before := state.X
				tt.Move(r.X+8, r.Y+y)
				tt.Press(r.X+8, r.Y+y)
				tt.Move(r.X+110, r.Y+y)
				tt.Release(r.X+110, r.Y+y)
				after := state.X
				if after <= before+10 {
					t.Fatalf("top band drag y=%v did not scroll: before=%v after=%v", y, before, after)
				}
			}
			state.X = 0
			tt.Frame()
			tt.Scroll(r.X+50, r.Y+22, 120, 0)
			if state.X <= 0 {
				t.Fatal("horizontal wheel did not scroll")
			}
			state.X = state.MaxX
			tt.Frame()
			last, _ := tt.Find("close7")
			if last.W != 24 || last.X < r.X || last.X+last.W > r.X+r.W+.5 {
				t.Fatalf("last close unreachable: %+v within %+v", last, r)
			}
			tt.SetSize(180, 120)
			state.X = state.MaxX
			tt.Frame()
			last, _ = tt.Find("close7")
			r, _ = tt.Find("strip")
			if last.W != 24 || last.X+last.W > r.X+r.W+.5 {
				t.Fatalf("narrow last close unreachable: %+v within %+v", last, r)
			}
			tt.ClickAt(last.X+12, last.Y+12)
			if closedTab != 7 {
				t.Fatal("last tab's close target did not receive click in narrow viewport")
			}
		})
	}
}

func TestUI01ShortStripHasNoIndicatorAndCloseFocus(t *testing.T) {
	theme := ui.LightTheme()
	p := PaletteFor(theme)
	clicked, closed := false, false
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Row(c).FillWidth().Children(func() {
			TabScroll(c, theme, func() {
				_, activate, close := TabItem(c, p, "one", TabSpec{Title: "中文 English", Dirty: true, Active: true}, "close")
				clicked = clicked || activate
				closed = closed || close
			})
		})
	}, 320, 80)
	tt.Frame()
	// Every pixel in the reserved band stays the background for short lists.
	im := tt.Image()
	for y := 0; y < 12; y++ {
		for x := 0; x < 320; x++ {
			if im.RGBAAt(x, y) != im.RGBAAt(319, 0) {
				t.Fatalf("unnecessary short-list indicator at %d,%d", x, y)
			}
		}
	}
	label, _ := tt.Find("中文 English •")
	close, _ := tt.Find("close")
	tt.Move(label.X+3, label.Y+label.H/2)
	if tt.Cursor() != ui.CursorPointer {
		t.Fatal("tab lacks pointer cursor")
	}
	tt.Key(0, ui.KeyTab)
	tt.Key(0, ui.KeyTab)
	if !tt.Focused("close") {
		t.Fatal("close target lost keyboard focus")
	}
	tt.Key(0, ui.KeyEnter)
	if !closed || clicked {
		t.Fatalf("keyboard close propagated activation: closed=%v clicked=%v", closed, clicked)
	}
	// Hit near the edge, outside the tiny cross, still closes independently.
	tt.ClickAt(close.X+2, close.Y+2)
	if !closed || clicked {
		t.Fatal("expanded close target failed")
	}
}

func TestUI01DisabledTabKeepsDefaultCursor(t *testing.T) {
	theme := ui.LightTheme()
	clicked := false
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Row(c).Disabled(true).Children(func() {
			_, activate, close := TabItem(c, PaletteFor(theme), "disabled", TabSpec{Title: "Disabled"}, "disabled close")
			clicked = clicked || activate || close
		})
	}, 240, 80)
	tt.Frame()
	for _, label := range []string{"Disabled", "disabled close"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing %s", label)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		if tt.Cursor() != ui.CursorDefault {
			t.Fatalf("disabled %s has interactive cursor", label)
		}
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
		if clicked {
			t.Fatal("disabled tab responded to click")
		}
	}
}
