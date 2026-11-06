package uikit

import (
	"bytes"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
)

func ui02Rect(t *testing.T, tt *ui.Tester, label string) ui.Rect {
	t.Helper()
	r, ok := tt.Find(label)
	if !ok || r.W <= 0 || r.H <= 0 {
		t.Fatalf("missing target %q: %+v", label, r)
	}
	return r
}

func ui02Hover(t *testing.T, tt *ui.Tester, x, y float32, want ui.Cursor) {
	t.Helper()
	tt.Move(x, y)
	if got := tt.Cursor(); got != want {
		t.Fatalf("hover (%g, %g): cursor %v, want %v", x, y, got, want)
	}
}

// Exercise the actual Send/Cancel control, including its overlapping animated
// icons. Every interior point must behave alike, not only the outer rectangle.
func TestUI02SendCursorAndHitTarget(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, sending := range []bool{false, true} {
			p := lightPalette
			if dark {
				p = darkPalette
			}
			clicks := 0
			tt := ui.NewTester(func(c *ui.Context) {
				c.SetTheme(Theme(p, dark))
				ui.Column(c).Padding(12).AlignItems(ui.Start).Children(func() {
					e, clicked := SendButton(c, p, sending, "Action")
					e.Label("send-target")
					if clicked {
						clicks++
					}
				})
			}, 180, 100)
			r := ui02Rect(t, tt, "send-target")
			if r.W != 104 || r.H != 40 {
				t.Fatalf("send geometry changed: %+v", r)
			}
			// Grid crosses both icon layers, text, and padding within the radius.
			for y := float32(10); y < r.H-8; y += 5 {
				for x := float32(10); x < r.W-8; x += 5 {
					ui02Hover(t, tt, r.X+x, r.Y+y, ui.CursorPointer)
				}
			}
			text := ui02Rect(t, tt, "Action")
			for _, pt := range [][2]float32{{r.X + 20, r.Y + 20}, {text.X + text.W/2, text.Y + text.H/2}, {r.X + r.W - 12, r.Y + 20}} {
				tt.ClickAt(pt[0], pt[1])
			}
			if clicks != 3 {
				t.Fatalf("icon/text/padding clicks = %d, want 3", clicks)
			}
			ui02Hover(t, tt, r.X+r.W+4, r.Y+20, ui.CursorDefault)
		}
	}
}

func TestUI02CommonCursorDisabledInputAndKeyboard(t *testing.T) {
	disabled, parentDisabled := false, false
	clicks, selected, simple, underline := 0, 0, 0, 0
	input := "editable"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Padding(8).Gap(8).AlignItems(ui.Start).Children(func() {
			ui.Box(c).Disabled(parentDisabled).Children(func() {
				e := ActionCursor(ui.Button(c, "Native action").Disabled(disabled))
				if e.Clicked() {
					clicks++
				}
			})
			IconButton(c, lightPalette, IconCopy, "Copy").Label("icon").Clicked()
			RoundButton(c, lightPalette, IconPlus, "Add").Label("round").Clicked()
			PillButton(c, lightPalette, PillSecondary, "Save", IconFile).Label("pill").Clicked()
			SendPill(c, lightPalette, "Send pill", IconSend).Label("send-pill").Clicked()
			NativeSegmented(c, &selected, "One", "Two")
			SimpleSegmented(c, lightPalette, &simple, []string{"Alpha", "Beta"})
			UnderlineTabs(c, lightPalette, &underline, []string{"Body", "Headers"}, nil)
			ui.TextInput(c, &input).Width(140).Label("editor")
			ui.Text(c, "Static label")
		})
	}, 220, 600)
	for _, label := range []string{"Native action", "icon", "round", "pill", "send-pill", "One", "Two", "Alpha", "Beta", "Body", "Headers"} {
		r := ui02Rect(t, tt, label)
		ui02Hover(t, tt, r.X+r.W/2, r.Y+r.H/2, ui.CursorPointer)
	}
	for _, label := range []string{"Two", "Beta", "Headers"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if selected != 1 || simple != 1 || underline != 1 {
		t.Fatalf("switches: %d %d %d", selected, simple, underline)
	}
	for _, label := range []string{"editor", "Static label"} {
		r := ui02Rect(t, tt, label)
		want := ui.CursorDefault
		if label == "editor" {
			want = ui.CursorText
		}
		ui02Hover(t, tt, r.X+r.W/2, r.Y+r.H/2, want)
	}
	for _, inherited := range []bool{false, true} {
		disabled, parentDisabled = !inherited, inherited
		tt.Frame()
		r := ui02Rect(t, tt, "Native action")
		ui02Hover(t, tt, r.X+r.W/2, r.Y+r.H/2, ui.CursorDefault)
		tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	}
	if clicks != 0 {
		t.Fatalf("disabled clicked %d times", clicks)
	}
	disabled, parentDisabled = false, false
	tt.Frame()
	r := ui02Rect(t, tt, "Native action")
	ui02Hover(t, tt, r.X+r.W/2, r.Y+r.H/2, ui.CursorPointer)
	if err := tt.Click("Native action"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeySpace)
	tt.Key(0, ui.KeyEnter)
	if clicks != 3 {
		t.Fatalf("pointer/Space/Enter clicks = %d", clicks)
	}
	// Allow pressed/hover motion to settle; cursor changes must not resize it.
	tt.Advance(time.Second)
	if got := ui02Rect(t, tt, "Native action"); got.W != r.W || got.H != r.H {
		t.Fatalf("hover/press shifted geometry: %+v -> %+v", r, got)
	}
	tt.SetSize(180, 600)
	r = ui02Rect(t, tt, "Native action")
	ui02Hover(t, tt, r.X+r.W/2, r.Y+r.H/2, ui.CursorPointer)
	ui02Hover(t, tt, 179, 599, ui.CursorDefault)
}

// Compare against the actual upstream control rather than an expected copy of
// the implementation: native pixels and geometry must stay identical.
func TestUI02NativeSegmentedMatchesMyGo(t *testing.T) {
	for _, dark := range []bool{false, true} {
		for _, density := range []bool{false, true} {
			p := lightPalette
			if dark {
				p = darkPalette
			}
			build := func(pointer bool) *ui.Tester {
				index := 0
				return ui.NewTester(func(c *ui.Context) {
					c.SetTheme(Theme(p, density))
					ui.Column(c).Padding(8).AlignItems(ui.Start).Children(func() {
						if pointer {
							NativeSegmented(c, &index, "First", "Second")
						} else {
							ui.Segmented(c, &index, "First", "Second")
						}
					})
				}, 220, 80)
			}
			native, pointer := build(false), build(true)
			for _, state := range []string{"rest", "hover", "pressed", "selected"} {
				r := ui02Rect(t, native, "Second")
				x, y := r.X+r.W/2, r.Y+r.H/2
				for _, tt := range []*ui.Tester{native, pointer} {
					switch state {
					case "hover":
						tt.Move(x, y)
					case "pressed":
						tt.Press(x, y)
					case "selected":
						tt.Release(x, y)
					}
				}
				if !bytes.Equal(native.Image().Pix, pointer.Image().Pix) {
					t.Fatalf("native pixels differ: dark=%v compact=%v state=%s", dark, density, state)
				}
				for _, label := range []string{"First", "Second"} {
					if a, b := ui02Rect(t, native, label), ui02Rect(t, pointer, label); a != b {
						t.Fatalf("native bounds differ: %+v %+v", a, b)
					}
				}
			}
			// Native roving focus and arrow selection remain implemented by MyGo.
			pointer.Key(0, ui.KeyLeft)
			if !pointer.Focused("First") {
				t.Fatal("Left did not move radio focus")
			}
		}
	}
}

func TestUI02ScrollbarsAndNestedInputKeepTheirCursor(t *testing.T) {
	input := "Text"
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Children(func() {
			outer := ActionCursor(ui.Row(c).Width(180).Height(40))
			outer.Children(func() { ui.TextInput(c, &input).Width(100).Label("nested-input") })
			outer.Clicked()
			ui.Scroll(c).Size(120, 80).Scrollbars(true).Children(func() {
				ActionCursor(ui.Button(c, "Scrollable action")).Width(120).Height(180)
			})
		})
	}, 220, 180)
	r := ui02Rect(t, tt, "nested-input")
	ui02Hover(t, tt, r.X+10, r.Y+r.H/2, ui.CursorText)
	ui02Hover(t, tt, 170, 20, ui.CursorPointer)
	ui02Hover(t, tt, 60, 65, ui.CursorPointer)
	ui02Hover(t, tt, 118, 65, ui.CursorDefault)
	tt.Press(118, 65)
	ui02Hover(t, tt, 118, 80, ui.CursorDefault)
	tt.Release(118, 80)
	ui02Hover(t, tt, 210, 170, ui.CursorDefault)
}
