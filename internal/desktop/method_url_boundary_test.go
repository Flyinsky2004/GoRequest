package desktop

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// R4: the method segment and the URL input must read as separate controls at
// rest and on hover. The old 22-high Hairline (7% alpha) was nearly invisible
// against the input's surface; these tests pin the geometry and the contrast
// of the replacement, using ui.NewTester and palette math, not screenshots.

func relativeLuminance(c ui.Color) float64 {
	lin := func(v uint8) float64 {
		s := float64(v) / 255
		if s <= 0.03928 {
			return s / 12.92
		}
		return math.Pow((s+0.055)/1.055, 2.4)
	}
	return 0.2126*lin(c.R) + 0.7152*lin(c.G) + 0.0722*lin(c.B)
}

func contrastRatio(a, b ui.Color) float64 {
	la, lb := relativeLuminance(a), relativeLuminance(b)
	if la < lb {
		la, lb = lb, la
	}
	return (la + 0.05) / (lb + 0.05)
}

func TestMethodURLDividerColorIsVisibleInBothThemes(t *testing.T) {
	for _, tc := range []struct {
		name string
		p    uikit.Palette
	}{
		{"light", uikit.PaletteFor(nil)},
		{"dark", uikit.PaletteFor(ui.DarkTheme())},
	} {
		t.Run(tc.name, func(t *testing.T) {
			divider := methodURLDividerColor(tc.p).Over(tc.p.BG)
			got := contrastRatio(divider, tc.p.BG)
			if got < 1.5 {
				t.Fatalf("divider contrast against the group background = %.2f, want >= 1.5", got)
			}
			// The old Hairline must be measurably weaker; otherwise the fix did
			// not address the reported cause.
			old := tc.p.Hairline.Over(tc.p.BG)
			if oldRatio := contrastRatio(old, tc.p.BG); oldRatio >= got {
				t.Fatalf("old Hairline (%.2f) is not weaker than the new divider (%.2f)", oldRatio, got)
			}
		})
	}
}

func TestMethodURLBoundaryGeometryAndHover(t *testing.T) {
	a, ps := openTestProject(t, "MethodBoundary")
	tab := openRequestTabs(t, ps, "Req")[0]
	tab.Method = "GET"
	tab.URL = "https://example.com/api"

	tt := ui.NewTester(func(c *ui.Context) {
		a.urlGroup(c, c.Theme(), tab)
	}, 1000, 200)
	tt.Frame()

	method, ok := tt.Find("GET")
	if !ok {
		t.Fatalf("method label not found; texts=%v", tt.Texts())
	}
	sep, ok := tt.Find("method separator")
	if !ok {
		t.Fatalf("method separator not found; texts=%v", tt.Texts())
	}
	urlRect, ok := tt.Find("request URL")
	if !ok {
		t.Fatalf("URL input not found; texts=%v", tt.Texts())
	}

	if sep.W < 1 || sep.H < 24 {
		t.Fatalf("separator too small to read: %+v", sep)
	}
	if sep.X < method.X+method.W-1 {
		t.Fatalf("separator overlaps the method label: sep=%+v method=%+v", sep, method)
	}
	if sep.X+sep.W > urlRect.X+1 {
		t.Fatalf("separator does not sit between method and URL: sep=%+v url=%+v", sep, urlRect)
	}
	if sep.H < urlRect.H*0.5 {
		t.Fatalf("separator does not span the row: sep=%+v url=%+v", sep, urlRect)
	}

	// Hovering the method segment must visibly darken it (existing Press wash),
	// so the boundary stays clear on hover too.
	sx := int(sep.X) - 6
	sy := int(sep.Y + sep.H/2)
	before := tt.Image().RGBAAt(sx, sy)
	tt.Move(method.X+method.W/2, method.Y+method.H/2)
	tt.Frame()
	after := tt.Image().RGBAAt(sx, sy)
	if relativeLuminance(ui.RGB(after.R, after.G, after.B)) >= relativeLuminance(ui.RGB(before.R, before.G, before.B)) {
		t.Fatalf("hover did not darken the method segment: before=%v after=%v", before, after)
	}
}
