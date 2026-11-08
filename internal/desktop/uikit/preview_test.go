package uikit

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// --- token unit tests ----------------------------------------------------

func TestPaletteForAppearance(t *testing.T) {
	if got := PaletteFor(nil); got != lightPalette {
		t.Fatalf("nil theme should be the light palette")
	}
	if got := PaletteFor(ui.DarkTheme()); got.Dark != true {
		t.Fatalf("dark theme should give the dark palette")
	}
	if lightPalette.Accent == darkPalette.Accent {
		t.Fatalf("light and dark accents should differ")
	}
	// Text hierarchy stays a single hue at three alpha steps: alpha must strictly
	// increase faint < muted < text.
	if !(lightPalette.Faint.A < lightPalette.Muted.A && lightPalette.Muted.A < lightPalette.Text.A) {
		t.Fatalf("light text alpha steps not ordered")
	}
	if !(darkPalette.Faint.A < darkPalette.Muted.A && darkPalette.Muted.A < darkPalette.Text.A) {
		t.Fatalf("dark text alpha steps not ordered")
	}
}

func TestMethodColorsDistinct(t *testing.T) {
	p := lightPalette
	ms := []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS"}
	seen := map[ui.Color]string{}
	for _, m := range ms {
		c := MethodColor(m, p)
		if prev, dup := seen[c]; dup {
			t.Fatalf("%s and %s share a color", m, prev)
		}
		seen[c] = m
	}
	// Unknown methods are faint, not a rainbow color.
	if MethodColor("CUSTOM", p) != p.Faint {
		t.Fatalf("custom method should be faint")
	}
	// Dark step is brighter (higher L) but still distinct from the light one.
	if MethodColor("GET", darkPalette) == MethodColor("GET", lightPalette) {
		t.Fatalf("dark method color should differ from light")
	}
}

func TestStatusColorMapping(t *testing.T) {
	p := lightPalette
	cases := []struct {
		code int
		want ui.Color
	}{
		{200, MethodColor("GET", p)},
		{204, MethodColor("GET", p)},
		{301, MethodColor("PUT", p)},
		{404, MethodColor("POST", p)},
		{500, MethodColor("DELETE", p)},
	}
	for _, c := range cases {
		if got := StatusColor(c.code, p); got != c.want {
			t.Fatalf("StatusColor(%d) mismatch", c.code)
		}
	}
	if _, ok := StatusPillColor(0, p); ok {
		t.Fatalf("0 is not a known status class")
	}
}

func TestThemeFromPalette(t *testing.T) {
	p := lightPalette
	th := Theme(p, false)
	if th.Background != p.BG || th.Text != p.Text || th.TextMuted != p.Muted {
		t.Fatalf("theme surfaces/text should follow the palette")
	}
	if th.Accent != p.Accent || th.AccentText != p.OnAccent {
		t.Fatalf("theme interactive accent should follow palette Accent/OnAccent")
	}
	if th.Selection != p.Accent.Alpha(0.3) {
		t.Fatalf("native selection should follow the original brand accent")
	}
	if th.Focus != p.Accent.Alpha(0.55) {
		t.Fatalf("native focus ring should follow the original brand accent")
	}
}

// --- headless preview render ---------------------------------------------

const jsonSample = `{
  "id": 1024,
  "name": "GoRequest",
  "active": true,
  "tags": ["api", "native"],
  "meta": null,
  "score": 98.5,
  "url": "https://example.com"
}`

func sectionLabel(c *ui.Context, p Palette, s string) {
	ui.Text(c, s).FontSize(12).FontWeight(600).TextColor(p.Muted).LetterSpacing(0.4)
}

func statusPill(c *ui.Context, p Palette, code int) {
	col, ok := StatusPillColor(code, p)
	if !ok {
		col = p.Faint
	}
	ui.Row(c).Padding(1, 6).Radius(999).Background(col.Alpha(0.10)).Children(func() {
		ui.Textf(c, "%d", code).FontSize(11).FontWeight(800).FontFeatures("tnum").LetterSpacing(0.4).TextColor(col)
	})
}

func previewView(c *ui.Context, p Palette, th *ui.Theme) {
	ui.Column(c).Fill().Padding(20).Gap(16).Background(p.BG).Children(func() {
		ui.Text(c, "GoRequest · design kit preview").FontSize(17).FontWeight(700).TextColor(p.Text)

		sectionLabel(c, p, "HTTP METHOD COLORS")
		ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
			for _, m := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS", "CUSTOM"} {
				ui.Text(c, m).Font("monospace").FontWeight(700).FontSize(12.5).TextColor(MethodColor(m, p))
			}
		})

		sectionLabel(c, p, "STATUS PILLS")
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			for _, code := range []int{200, 201, 301, 400, 404, 500} {
				statusPill(c, p, code)
			}
		})

		sectionLabel(c, p, "COMPONENTS 1–4")
		ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
			IconButton(c, p, IconSearch, "Search")
			RoundButton(c, p, IconPlus, "New")
			PillButton(c, p, PillPrimary, "Send", IconSend)
			PillButton(c, p, PillSecondary, "Cancel", nil)
			seg := 1
			_, _ = Segmented(c, p, &seg, []string{"Body", "Headers", "Cookies"})
		})

		sectionLabel(c, p, "JSON SYNTAX HIGHLIGHT")
		st := &editor.State{Buffer: editor.NewBuffer(jsonSample), ReadOnly: true, Theme: EditorTheme(th)}
		editor.View(c, st).FillWidth().Height(170)
	})
}

// TestRenderPreview renders the light and dark palettes headlessly. When
// PREVIEW_OUT is set it writes <dir>/light.png and <dir>/dark.png for visual
// inspection; otherwise it only checks the render succeeds.
func TestRenderPreview(t *testing.T) {
	out := os.Getenv("PREVIEW_OUT")
	for _, dark := range []bool{false, true} {
		name := "light"
		th := Theme(lightPalette, false)
		if dark {
			name = "dark"
			th = Theme(darkPalette, true)
		}
		p := PaletteFor(th)
		img := ui.Render(func(c *ui.Context) {
			c.SetTheme(th)
			previewView(c, p, th)
		}, 840, 640, 2)
		if img == nil {
			t.Fatalf("%s: render returned nil", name)
		}
		// Objective sanity checks that the frame actually drew content: the
		// image has many distinct colors, the top-left corner is the palette
		// background, and the accent fill (the Send pill) covers many pixels.
		uniq := map[uint32]struct{}{}
		var accentPix int
		for i := 0; i < len(img.Pix); i += 4 {
			r, g, b, a := img.Pix[i], img.Pix[i+1], img.Pix[i+2], img.Pix[i+3]
			uniq[uint32(r)<<24|uint32(g)<<16|uint32(b)<<8|uint32(a)] = struct{}{}
			if r == p.Accent.R && g == p.Accent.G && b == p.Accent.B && a == p.Accent.A {
				accentPix++
			}
		}
		corner := img.RGBAAt(2, 2)
		if got := (ui.Color{R: corner.R, G: corner.G, B: corner.B, A: corner.A}); got != p.BG {
			t.Fatalf("%s: corner %v is not palette BG %v", name, got, p.BG)
		}
		if len(uniq) < 50 {
			t.Fatalf("%s: only %d distinct colors — frame looks blank", name, len(uniq))
		}
		if accentPix < 100 {
			t.Fatalf("%s: accent fill only %d pixels — primary pill did not draw", name, accentPix)
		}
		t.Logf("%s: %dx%d, %d distinct colors, %d accent px", name, img.Bounds().Dx(), img.Bounds().Dy(), len(uniq), accentPix)
		if out != "" {
			if err := os.MkdirAll(out, 0o755); err != nil {
				t.Fatalf("mkdir preview: %v", err)
			}
			f, err := os.Create(filepath.Join(out, name+".png"))
			if err != nil {
				t.Fatalf("create png: %v", err)
			}
			if err := png.Encode(f, img); err != nil {
				f.Close()
				t.Fatalf("encode png: %v", err)
			}
			f.Close()
		}
	}
}
