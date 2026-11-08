package uikit

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func themeFor(dark bool) (*ui.Theme, Palette) {
	if dark {
		return Theme(darkPalette, true), darkPalette
	}
	return Theme(lightPalette, false), lightPalette
}

// accentPixels counts exact accent pixels in a rectangle of the frame.
func accentPixels(tt *ui.Tester, r ui.Rect, col ui.Color) int {
	im := tt.Image()
	n := 0
	for y := int(r.Y); y < int(r.Y+r.H); y++ {
		for x := int(r.X); x < int(r.X+r.W); x++ {
			if x < 0 || y < 0 || x >= im.Bounds().Dx() || y >= im.Bounds().Dy() {
				continue
			}
			if c := im.RGBAAt(x, y); c.R == col.R && c.G == col.G && c.B == col.B && c.A == col.A {
				n++
			}
		}
	}
	return n
}

func TestThemedTabsIconsBadgesStatesAndIndicator(t *testing.T) {
	for _, dark := range []bool{false, true} {
		t.Run(fmt.Sprint("dark=", dark), func(t *testing.T) {
			th, p := themeFor(dark)
			selected := 0
			changes := 0
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(th)
				ui.Column(c).Padding(8).Children(func() {
					row, ch := Tabs(c, p, &selected, []TabEntry{
						{Label: "Params", Badge: "3", Icon: IconList},
						{Label: "Headers", Icon: IconLayers},
						{Label: "Locked", Icon: IconLock, Disabled: true},
					})
					row.Label("tabs")
					if ch {
						changes++
					}
				})
			}, 440, 120)

			for _, s := range []string{"Params", "Headers", "Locked", "3"} {
				if _, ok := tt.Find(s); !ok {
					t.Fatalf("missing %q", s)
				}
			}
			// Disabled tabs keep their place but take no pointer input.
			locked, _ := tt.Find("Locked")
			tt.Move(locked.X+locked.W/2, locked.Y+locked.H/2)
			if tt.Cursor() != ui.CursorDefault {
				t.Fatal("disabled tab has an interactive cursor")
			}
			tt.ClickAt(locked.X+locked.W/2, locked.Y+locked.H/2)
			if selected != 0 {
				t.Fatalf("disabled tab changed selection to %d", selected)
			}

			// A click selects and slides the underline under the chosen tab.
			if err := tt.Click("Headers"); err != nil {
				t.Fatal(err)
			}
			if selected != 1 || changes != 1 {
				t.Fatalf("click: selected=%d changes=%d", selected, changes)
			}
			tt.Advance(600 * time.Millisecond)
			tt.Frame()
			row, _ := tt.Find("tabs")
			band := ui.Rect{X: row.X, Y: row.Y + row.H - 3, W: row.W, H: 3}
			headers, _ := tt.Find("Headers")
			params, _ := tt.Find("Params")
			underHeaders := accentPixels(tt, ui.Rect{X: headers.X, Y: band.Y, W: headers.W, H: band.H}, p.Accent)
			underParams := accentPixels(tt, ui.Rect{X: params.X, Y: band.Y, W: params.W, H: band.H}, p.Accent)
			if underHeaders == 0 {
				t.Fatal("indicator is not under the selected tab")
			}
			if underParams != 0 {
				t.Fatalf("stale indicator left under the previous tab: %d px", underParams)
			}

			// The strip is one keyboard stop: Tab enters at the selected tab,
			// arrows move the focus (skipping disabled tabs), Enter chooses.
			tt.ClickAt(430, 110) // clear the pointer focus on empty space
			tt.Key(0, ui.KeyTab)
			if !tt.Focused("Headers") {
				t.Fatal("Tab did not enter the tab list at the selected tab")
			}
			tt.Key(0, ui.KeyRight)
			if !tt.Focused("Params") {
				t.Fatal("Right did not skip the disabled tab and wrap to the first")
			}
			tt.Key(0, ui.KeyEnter)
			if selected != 0 {
				t.Fatalf("Enter did not choose the focused tab: selected=%d", selected)
			}
			tt.Advance(time.Second)
			tt.Frame()
			if tt.FramePending() {
				t.Fatal("tab strip keeps repainting after the animation settles")
			}
		})
	}
}

func TestThemedSegmentsKeyboardDisabledAndThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, simple := range []bool{false, true} {
			t.Run(fmt.Sprintf("dark=%v/simple=%v", dark, simple), func(t *testing.T) {
				th, p := themeFor(dark)
				selected := 0
				tt := ui.NewTester(func(c *ui.Context) {
					c.SetTheme(th)
					ui.Column(c).Padding(8).Children(func() {
						var e ui.Element
						if simple {
							e, _ = SimpleSegmented(c, p, &selected, []string{"One", "Two", "Three"})
						} else {
							e, _ = Segments(c, p, &selected, []TabEntry{
								{Label: "One", Icon: IconList},
								{Label: "Two", Badge: "2"},
								{Label: "Locked", Disabled: true},
							})
						}
						e.Label("segments")
					})
				}, 380, 100)

				if err := tt.Click("Two"); err != nil {
					t.Fatal(err)
				}
				if selected != 1 {
					t.Fatalf("click selected=%d", selected)
				}
				if !simple {
					locked, _ := tt.Find("Locked")
					tt.Move(locked.X+locked.W/2, locked.Y+locked.H/2)
					if tt.Cursor() != ui.CursorDefault {
						t.Fatal("disabled segment has an interactive cursor")
					}
					tt.ClickAt(locked.X+locked.W/2, locked.Y+locked.H/2)
					if selected != 1 {
						t.Fatalf("disabled segment changed selection to %d", selected)
					}
				}
				// Radio semantics: arrows choose as they move the focus.
				tt.Key(0, ui.KeyTab)
				if !tt.Focused("Two") {
					t.Fatal("Tab did not enter the segment group at the chosen segment")
				}
				tt.Key(0, ui.KeyLeft)
				if selected != 0 {
					t.Fatalf("Left did not choose the previous segment: %d", selected)
				}
				tt.Advance(time.Second)
				tt.Frame()
				if tt.FramePending() {
					t.Fatal("segments keep repainting after the animation settles")
				}
			})
		}
	}
}

func TestTabContentRapidSwitchSinglePageKeepsEditAndSettles(t *testing.T) {
	selected := 0
	edits := [3]string{}
	tt := ui.NewTester(func(c *ui.Context) {
		th, p := themeFor(false)
		c.SetTheme(th)
		ui.Column(c).Padding(8).Gap(6).Children(func() {
			Tabs(c, p, &selected, []TabEntry{{Label: "A"}, {Label: "B"}, {Label: "C"}})
			TabContent(c, selected, func(i int) {
				ui.Column(c).Gap(4).Children(func() {
					ui.Textf(c, "page %d", i)
					ui.TextInput(c, &edits[i]).Label("editor")
				})
			})
		})
	}, 380, 220)

	if err := tt.Click("editor"); err != nil {
		t.Fatal(err)
	}
	tt.Type("hello")
	// Rapid consecutive switches: every frame builds exactly one page, and
	// each switch retargets the same transition instead of stacking pages.
	for _, label := range []string{"B", "C", "B", "A"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if edits[0] != "hello" {
		t.Fatalf("edit lost across switches: %q", edits[0])
	}
	pages := 0
	for _, s := range tt.Texts() {
		if strings.HasPrefix(s, "page ") {
			pages++
		}
	}
	if pages != 1 {
		t.Fatalf("stacked pages: %d page labels visible", pages)
	}
	if !tt.HasText("page 0") {
		t.Fatal("final selection did not render its page")
	}
	tt.Advance(2 * time.Second)
	tt.Frame()
	if tt.FramePending() {
		t.Fatal("content keeps repainting after the transition settles")
	}
}

func TestTabContentReduceMotionArrivesAtOnce(t *testing.T) {
	for _, reduce := range []bool{true, false} {
		t.Run(fmt.Sprint("reduce=", reduce), func(t *testing.T) {
			selected := 0
			tt := ui.NewTester(func(c *ui.Context) {
				th, p := themeFor(false)
				c.SetTheme(th)
				ui.Column(c).Padding(8).Children(func() {
					Tabs(c, p, &selected, []TabEntry{{Label: "A"}, {Label: "B"}})
					TabContent(c, selected, func(i int) { ui.Textf(c, "page %d", i) })
				})
			}, 320, 160)
			tt.SetPreferences(ui.Preferences{ReduceMotion: reduce})
			if err := tt.Click("B"); err != nil {
				t.Fatal(err)
			}
			// 150 ms is past the 120 ms hover/selection color wash but before a
			// 200 ms content transition would finish, so the leftover request
			// tells whether the content itself still animates.
			tt.Advance(150 * time.Millisecond)
			tt.Frame()
			if reduce && tt.FramePending() {
				t.Fatal("reduced motion still animates the content transition")
			}
			if !reduce && !tt.FramePending() {
				t.Fatal("content transition did not animate without reduced motion")
			}
			if !tt.HasText("page 1") {
				t.Fatal("the new page did not render")
			}
		})
	}
}

func TestTabItemActionsOptionalChrome(t *testing.T) {
	activated, closed, renamed, middle := 0, 0, 0, 0
	menuItems := []string{}
	tt := ui.NewTester(func(c *ui.Context) {
		th, p := themeFor(false)
		c.SetTheme(th)
		ui.Row(c).Padding(8).Children(func() {
			TabItemWithActions(c, p, "tab", TabSpec{
				Title: "Doc", Icon: IconFile, Badge: "2", Active: true,
			}, "close", TabItemActions{
				OnActivate:    func() { activated++ },
				OnClose:       func() { closed++ },
				OnDoubleClick: func() { renamed++ },
				OnMiddleClick: func() { middle++ },
				OnContextMenu: func(m *ui.Menu) {
					menuItems = []string{"Close"}
					m.Item("Close")
				},
			})
		})
	}, 320, 80)
	if !tt.HasText("2") {
		t.Fatal("badge did not render")
	}
	if err := tt.Click("Doc"); err != nil {
		t.Fatal(err)
	}
	if activated != 1 {
		t.Fatalf("click activations=%d", activated)
	}
	r, _ := tt.Find("Doc")
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	if renamed != 1 {
		t.Fatalf("double-click renames=%d", renamed)
	}
	if err := tt.RightClick("Doc"); err != nil {
		t.Fatal(err)
	}
	if len(menuItems) != 1 || len(tt.Menu()) != 1 {
		t.Fatalf("context menu not built: items=%v menu=%v", menuItems, tt.Menu())
	}
	tt.CloseMenu()
	if err := tt.Click("close"); err != nil {
		t.Fatal(err)
	}
	if closed != 1 {
		t.Fatalf("close clicks=%d", closed)
	}
}

func TestTabItemDisabledTakesNoInput(t *testing.T) {
	activated, closed := 0, 0
	tt := ui.NewTester(func(c *ui.Context) {
		th, p := themeFor(true)
		c.SetTheme(th)
		ui.Row(c).Padding(8).Children(func() {
			TabItemWithActions(c, p, "tab", TabSpec{Title: "Off", Disabled: true}, "close", TabItemActions{
				OnActivate: func() { activated++ },
				OnClose:    func() { closed++ },
			})
		})
	}, 260, 80)
	for _, label := range []string{"Off", "close"} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing %q", label)
		}
		tt.Move(r.X+r.W/2, r.Y+r.H/2)
		if tt.Cursor() != ui.CursorDefault {
			t.Fatalf("disabled %q has an interactive cursor", label)
		}
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	}
	if activated != 0 || closed != 0 {
		t.Fatalf("disabled tab responded: activate=%d close=%d", activated, closed)
	}
}
