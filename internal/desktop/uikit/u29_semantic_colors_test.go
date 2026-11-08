package uikit

import (
	"image/color"
	"testing"

	"github.com/egoist/mygo/ui"
)

func isCoolHue(c ui.Color) bool  { return int(c.B) > int(c.R)+25 && int(c.G) > int(c.R) }
func isRedHue(c color.RGBA) bool { return c.R > 150 && int(c.R)-int(c.G) > 80 }

func TestU29TokensAreNotRedAndContrast(t *testing.T) {
	for _, p := range []Palette{lightPalette, darkPalette} {
		if !isCoolHue(p.Focus) || !isCoolHue(p.InputBorder) {
			t.Fatalf("dark=%v focus/input border must be cool, got %v %v", p.Dark, p.Focus, p.InputBorder)
		}
		if r := contrastOver(p.Focus, p.BG); r < 3 {
			t.Fatalf("dark=%v focus vs BG %.2f < 3", p.Dark, r)
		}
		if r := contrastOver(p.Focus, p.Sidebar); r < 3 {
			t.Fatalf("dark=%v focus vs Sidebar %.2f < 3", p.Dark, r)
		}
		if r := contrastOver(p.OnFocus, p.Focus); r < 4.5 {
			t.Fatalf("dark=%v OnFocus vs Focus %.2f < 4.5", p.Dark, r)
		}
		if p.Accent == p.Focus || p.Danger == p.Focus || p.Success == p.Danger {
			t.Fatalf("dark=%v semantic tokens collapsed", p.Dark)
		}
		// Brand accent stays red-orange.
		if !(p.Accent.R > p.Accent.B && p.Accent.R > p.Accent.G) {
			t.Fatalf("brand accent no longer red: %v", p.Accent)
		}
		if !(p.Success.G > p.Success.R && p.Success.G > p.Success.B) {
			t.Fatalf("success not green: %v", p.Success)
		}
		if !(p.Danger.R > p.Danger.G && p.Danger.R > p.Danger.B) {
			t.Fatalf("danger not red: %v", p.Danger)
		}
	}
}

func TestNativeThemeRestoresBrandInteractionColors(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th, p := themeFor(dark)
		if th.Accent != p.Accent || th.AccentText != p.OnAccent {
			t.Fatalf("dark=%v Theme.Accent (used by MyGo inputs and menus) must be the brand accent", dark)
		}
		if th.Danger != p.Danger || th.Success != p.Success {
			t.Fatalf("dark=%v danger/success mismatch", dark)
		}
	}
}

// Render actual input borders so wrappers and direct MyGo inputs both restore
// the brand accent without a later control changing their draw-time theme.
func TestFocusedInputsRestoreBrandAccent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th, p := themeFor(dark)
		v1, v2, v3 := "hello", "multi\nline", "al"
		on := true
		build := func(c *ui.Context) {
			c.SetTheme(th)
			ui.Column(c).Padding(12).Gap(10).Children(func() {
				TextInput(c, &v1).Width(200).Label("in1")
				TextArea(c, &v2).Width(200).Label("in2")
				Autocomplete(c, &v3, []string{"alpha", "alps"}).Width(200).Label("in3")
				ui.TextInput(c, &v1).Width(200).Label("plain")
				Switch(c, &on).Label("enabled")
			})
		}
		for _, label := range []string{"in1", "in2", "in3", "plain"} {
			tt := ui.NewTester(build, 260, 300)
			if err := tt.Click(label); err != nil {
				t.Fatal(err)
			}
			if !tt.Focused(label) {
				t.Fatalf("%s not focused", label)
			}
			r, _ := tt.Find(label)
			im := tt.Image()
			cool, brand := 0, 0
			for x := int(r.X); x < int(r.X+r.W); x++ {
				for _, y := range []int{int(r.Y), int(r.Y + r.H - 1)} {
					c := im.RGBAAt(x, y)
					if isRedHue(c) {
						brand++
					}
					if int(c.B) > int(c.R)+25 {
						cool++
					}
				}
			}
			if brand == 0 || cool > 0 {
				t.Fatalf("dark=%v %s focused border: brand=%d cool=%d (accent %v) rect=%v top=%v theme=%v", dark, label, brand, cool, p.Accent, r, im.RGBAAt(int(r.X+r.W/2), int(r.Y)), th.Accent)
			}
			sw, ok := tt.Find("enabled")
			if !ok || accentPixels(tt, sw, p.Success) == 0 {
				t.Fatalf("dark=%v enabled switch lost its green track", dark)
			}
		}
	}
}

func TestU29InvalidInputStaysDanger(t *testing.T) {
	th, p := themeFor(false)
	v := "x"
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(th)
		ui.Column(c).Padding(12).Children(func() {
			TextInput(c, &v).Width(200).Label("bad").Error("nope")
		})
	}, 260, 100)
	tt.Frame()
	r, _ := tt.Find("bad")
	got := tt.Image().RGBAAt(int(r.X+r.W/2), int(r.Y))
	if !isRedHue(got) {
		t.Fatalf("invalid input border should be red danger, got %v (danger %v)", got, p.Danger)
	}
}

func TestU29PrimaryButtonKeepsBrandAccent(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th, p := themeFor(dark)
		before := th.Accent
		tt := ui.NewTester(func(c *ui.Context) {
			c.SetTheme(th)
			ui.Column(c).Padding(12).AlignItems(ui.Start).Children(func() {
				PrimaryButton(c, "Go").Label("go")
			})
		}, 200, 100)
		tt.Frame()
		if th.Accent != before {
			t.Fatalf("PrimaryButton leaked theme Accent")
		}
		r, _ := tt.Find("go")
		got := tt.Image().RGBAAt(int(r.X+3), int(r.Y+r.H/2))
		if got != rgbOf(p.Accent) {
			t.Fatalf("dark=%v primary fill %v, want brand %v", dark, got, p.Accent)
		}
	}
}

func TestU29NoticeColors(t *testing.T) {
	for _, p := range []Palette{lightPalette, darkPalette} {
		fgInfo, _, _ := NoticeColors(p, NoticeInfo)
		fgWarn, _, _ := NoticeColors(p, NoticeWarning)
		fgDanger, _, _ := NoticeColors(p, NoticeDanger)
		if fgInfo == fgDanger || fgWarn == fgDanger || !isCoolHue(fgInfo) {
			t.Fatalf("dark=%v notice tones not distinct", p.Dark)
		}
		for _, fg := range []ui.Color{fgInfo, fgWarn, fgDanger} {
			if r := contrastOver(fg, p.BG); r < 3.5 {
				t.Fatalf("dark=%v notice text contrast %.2f", p.Dark, r)
			}
		}
	}
}

func TestU29EditorThemeNotRed(t *testing.T) {
	for _, dark := range []bool{false, true} {
		th, p := themeFor(dark)
		ct := EditorTheme(th)
		if ct.Cursor != p.Focus || ct.Selection != p.Selection {
			t.Fatalf("dark=%v editor cursor/selection must use Focus tokens", dark)
		}
	}
}
