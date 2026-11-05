// Package uikit is GoRequest's design system: the single source of colors,
// motion timings, icon rules and the primitive controls the workbench views
// compose. Pages must not pick raw colors, radii, shadows or animation
// parameters; they call these tokens and components instead.
//
// The palette is derived from a ui.Theme so light/dark/system all work, but
// the exact values are fixed here (see §1 of the design spec), never invented
// per page.
package uikit

import (
	"strings"

	"github.com/egoist/mygo/ui"
)

// Palette is the whole color vocabulary of the interface. It is the only
// place raw color values live; views read these fields (or the method/status
// accessors below) and never call ui.Hex/ui.RGBA directly.
type Palette struct {
	Dark bool

	// Surfaces and text: text has three alpha steps (Text/Muted/Faint); there
	// is no second text hue. Functional colors (methods, statuses, syntax) are
	// separate tokens below.
	BG       ui.Color // window background
	Sidebar  ui.Color // left rail / panel face
	Text     ui.Color // primary text
	Muted    ui.Color // secondary text (61% gray)
	Faint    ui.Color // faintest text (32% gray)
	Hairline ui.Color // 0.5–1px separators / borders
	Hover    ui.Color // row/control hover wash
	Press    ui.Color // row/control press wash

	Accent   ui.Color // brand accent (red-orange of the reference theme)
	OnAccent ui.Color // text on accent

	// CardShade is the single shadow color; shadows are always soft and use
	// only this (never colored/hard shadows).
	CardShade ui.Color
}

// Light palette values, copied verbatim from §1.1.
var lightPalette = Palette{
	Dark:      false,
	BG:        ui.Hex("#ffffff"),
	Sidebar:   ui.Hex("#f4f4f6"),
	Text:      ui.Hex("#1d1d1f"),
	Muted:     ui.RGBA(60, 60, 67, 0.62),
	Faint:     ui.RGBA(60, 60, 67, 0.32),
	Hairline:  ui.RGBA(0, 0, 0, 0.07),
	Hover:     ui.RGBA(0, 0, 0, 0.04),
	Press:     ui.RGBA(0, 0, 0, 0.08),
	Accent:    ui.Oklch(0.6, 0.21, 24),
	OnAccent:  ui.Hex("#ffffff"),
	CardShade: ui.RGBA(0, 0, 0, 0.16),
}

// Dark palette values, copied verbatim from §1.1.
var darkPalette = Palette{
	Dark:      true,
	BG:        ui.Hex("#141416"),
	Sidebar:   ui.Hex("#1c1c1f"),
	Text:      ui.Hex("#f5f5f7"),
	Muted:     ui.RGBA(235, 235, 245, 0.56),
	Faint:     ui.RGBA(235, 235, 245, 0.30),
	Hairline:  ui.RGBA(255, 255, 255, 0.07),
	Hover:     ui.RGBA(255, 255, 255, 0.06),
	Press:     ui.RGBA(255, 255, 255, 0.10),
	Accent:    ui.Oklch(0.66, 0.2, 22),
	OnAccent:  ui.Hex("#ffffff"),
	CardShade: ui.RGBA(0, 0, 0, 0.45),
}

// PaletteFor returns the palette for a theme's appearance. A nil theme is
// treated as light.
func PaletteFor(t *ui.Theme) Palette {
	if t != nil && t.Dark {
		return darkPalette
	}
	return lightPalette
}

// methodOklch holds each HTTP method's base color as L, C, H. The dark step is
// base L+0.04, C-0.01 (H unchanged), per §1.2.
type methodOklch struct {
	l, c, h float32
}

var methodTable = map[string]methodOklch{
	"GET":     {0.64, 0.17, 155},
	"POST":    {0.68, 0.17, 75},
	"PUT":     {0.62, 0.15, 250},
	"PATCH":   {0.62, 0.16, 310},
	"DELETE":  {0.60, 0.20, 15},
	"HEAD":    {0.64, 0.12, 190},
	"OPTIONS": {0.62, 0.12, 230},
}

// MethodColor returns the functional color for an HTTP method. Unknown or
// custom methods get the faint gray, never a rainbow color. Methods are only
// ever rendered as bold monospace text or a 10%-alpha capsule (§1.2), never a
// large fill.
func MethodColor(method string, p Palette) ui.Color {
	oc, ok := methodTable[strings.ToUpper(strings.TrimSpace(method))]
	if !ok {
		return p.Faint
	}
	if p.Dark {
		return ui.Oklch(oc.l+0.04, oc.c-0.01, oc.h)
	}
	return ui.Oklch(oc.l, oc.c, oc.h)
}

// StatusColor returns the color for a response status code: 2xx=GET green,
// 3xx=PUT blue, 4xx=POST amber, 5xx=DELETE red. Anything outside 200–599 is
// unknown and gets the faint gray (a network failure is signalled by the accent
// retry affordance, not by coloring a status code it never had).
func StatusColor(code int, p Palette) ui.Color {
	switch {
	case code >= 200 && code < 300:
		return MethodColor("GET", p)
	case code >= 300 && code < 400:
		return MethodColor("PUT", p)
	case code >= 400 && code < 500:
		return MethodColor("POST", p)
	case code >= 500 && code < 600:
		return MethodColor("DELETE", p)
	}
	return p.Faint
}

// StatusPillColor is the text/capsule color for a status code, and whether the
// code is a known class. Callers draw the pill with color.Alpha(0.10) as the
// background and the color as the text.
func StatusPillColor(code int, p Palette) (ui.Color, bool) {
	switch {
	case code >= 200 && code < 600:
		return StatusColor(code, p), true
	}
	return p.Faint, false
}

// Theme builds a ui.Theme whose surfaces, text and accent come from the
// palette, so built-in widgets match the kit. Radius is left at the workbench
// weight (8) and Spacing at 4; component-specific radii are set explicitly by
// the components, never inherited from here.
func Theme(p Palette, dark bool) *ui.Theme {
	var t *ui.Theme
	if dark {
		t = ui.DarkTheme()
	} else {
		t = ui.LightTheme()
	}
	black := ui.RGB(0, 0, 0)
	white := ui.RGB(255, 255, 255)

	t.Background = p.BG
	t.Surface = p.Sidebar
	t.SurfaceHover = p.BG.Mix(p.Text, 0.06)
	t.SurfacePressed = p.BG.Mix(p.Text, 0.10)
	t.Border = p.Hairline
	t.Text = p.Text
	t.TextMuted = p.Muted
	t.Accent = p.Accent
	t.AccentText = p.OnAccent
	if dark {
		t.AccentHover = p.Accent.Mix(white, 0.15)
		t.AccentPressed = p.Accent.Mix(black, 0.20)
	} else {
		t.AccentHover = p.Accent.Mix(black, 0.12)
		t.AccentPressed = p.Accent.Mix(black, 0.25)
	}
	t.Selection = p.Accent.Alpha(0.3) // §1.1
	t.Focus = p.Accent.Alpha(0.55)
	t.Danger = MethodColor("DELETE", p)
	t.Warning = MethodColor("POST", p)
	t.Success = MethodColor("GET", p)
	t.Radius = 8
	t.Spacing = 4
	return t
}
