package uikit

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
)

// linearChannel converts one sRGB channel (0–1) to its linear-light value per
// WCAG 2.x.
func linearChannel(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// relLuminance returns the WCAG relative luminance of an opaque color.
func relLuminance(c ui.Color) float64 {
	r := linearChannel(float64(c.R) / 255)
	g := linearChannel(float64(c.G) / 255)
	b := linearChannel(float64(c.B) / 255)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

// contrastOver composites fg over an opaque bg and returns the WCAG contrast
// ratio between them.
func contrastOver(fg, bg ui.Color) float64 {
	composited := fg.Over(bg)
	l1 := relLuminance(composited)
	l2 := relLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

// TestSyntaxPunctuationReadable verifies the JSON punctuation/operator color is
// no longer the too-faint Faint token, sits between Text and Muted, and stays
// clearly readable over the background of both the light and dark themes.
// WCAG AA for normal text is 4.5:1; we hold the separators to that bar so they
// are unambiguous but still softer than the near-full-contrast keys.
func TestSyntaxPunctuationReadable(t *testing.T) {
	cases := []struct {
		name string
		p    Palette
		th   *ui.Theme
	}{
		{"light", lightPalette, Theme(lightPalette, false)},
		{"dark", darkPalette, Theme(darkPalette, true)},
	}
	for _, tc := range cases {
		ct := EditorTheme(tc.th)
		punct := ct.Tokens[editor.TokenPunctuation].Color
		oper := ct.Tokens[editor.TokenOperator].Color

		if punct != oper {
			t.Fatalf("%s: punctuation %v and operator %v should share a color", tc.name, punct, oper)
		}
		if punct == tc.p.Faint {
			t.Fatalf("%s: punctuation should no longer be Faint", tc.name)
		}
		if punct == tc.p.Text {
			t.Fatalf("%s: punctuation should be softer than the key/Text color", tc.name)
		}

		// Still a gray step between Text and Muted: lighter than keys, darker
		// than the muted secondary text once each is composited over BG.
		punctLum := relLuminance(punct.Over(tc.p.BG))
		textLum := relLuminance(tc.p.Text.Over(tc.p.BG))
		mutedLum := relLuminance(tc.p.Muted.Over(tc.p.BG))

		ratio := contrastOver(punct, tc.p.BG)
		if ratio < 4.5 {
			t.Fatalf("%s: punctuation contrast %.2f:1 over BG is below 4.5:1", tc.name, ratio)
		}
		if !(ratio > contrastOver(tc.p.Faint, tc.p.BG)) {
			t.Fatalf("%s: punctuation must be more readable than Faint", tc.name)
		}

		// Between: for light, Text is dark (low lum) and Muted lighter; for
		// dark, Text is bright (high lum) and Muted dimmer. Assert punctuation
		// lies strictly between the two luminances.
		lo, hi := textLum, mutedLum
		if lo > hi {
			lo, hi = hi, lo
		}
		if !(punctLum > lo && punctLum < hi) {
			t.Fatalf("%s: punctuation luminance %.4f not strictly between Text %.4f and Muted %.4f",
				tc.name, punctLum, textLum, mutedLum)
		}

		t.Logf("%s: punctuation contrast %.2f:1 (Faint %.2f:1)", tc.name, ratio, contrastOver(tc.p.Faint, tc.p.BG))
	}
}

// TestSyntaxExistingMappingsIntact locks the key/string/number/constant/tag
// colors so the punctuation fix cannot drift the other token mappings.
func TestSyntaxExistingMappingsIntact(t *testing.T) {
	for _, p := range []Palette{lightPalette, darkPalette} {
		th := Theme(p, p.Dark)
		ct := EditorTheme(th)
		if got := ct.Tokens[editor.TokenProperty].Color; got != p.Text {
			t.Fatalf("key/property should stay Text: got %v want %v", got, p.Text)
		}
		if got := ct.Tokens[editor.TokenNumber].Color; got != MethodColor("PUT", p) {
			t.Fatalf("number should stay PUT blue")
		}
		if got := ct.Tokens[editor.TokenConstant].Color; got != MethodColor("PATCH", p) {
			t.Fatalf("boolean/null should stay PATCH purple")
		}
		if got := ct.Tokens[editor.TokenTag].Color; got != MethodColor("PUT", p) {
			t.Fatalf("XML tag should stay PUT blue")
		}
		if got := ct.Tokens[editor.TokenString].Color; got != MethodColor("GET", p).Mix(p.Muted, 0.20) {
			t.Fatalf("string should stay GET green mixed toward Muted")
		}
	}
}
