package uikit

import "github.com/egoist/mygo/ui"

// iconHoverBG is the neutral hover wash shared by icon/round buttons (§5):
// RGBA(128,128,128,0.14), animated over 120ms.
var iconHoverBG = ui.RGBA(128, 128, 128, 0.14)

// IconButton is a borderless icon control: the hit target is icon + 16 on each
// side (a 32×32 box at the default 16 icon), radius 999. Hovering washes the
// background with iconHoverBG (120ms); pressing scales to 0.88 with Snappy.
// Read .Clicked() on the returned element.
func IconButton(c *ui.Context, p Palette, ic *ui.SVG, tooltip string) ui.Element {
	return iconButton(c, p, ic, tooltip, false)
}

// RoundButton is the contained variant of IconButton: same geometry, motion and
// hover wash, plus a resting subtle wash (the palette's Hover) and a hairline
// ring so it reads as a round button rather than a bare icon.
func RoundButton(c *ui.Context, p Palette, ic *ui.SVG, tooltip string) ui.Element {
	return iconButton(c, p, ic, tooltip, true)
}

func iconButton(c *ui.Context, p Palette, ic *ui.SVG, tooltip string, round bool) ui.Element {
	side := IconSizeDefault + 16
	e := ActionCursor(ui.Box(c).Size(side, side).Radius(999).Center())

	hovered := e.Hovered()
	scale := float32(1)
	if e.Pressed() {
		scale = ScaleIconPress
	}
	e.AnimatePose(ui.Pose{Scale: scale}, ui.Snappy)

	var bg ui.Color = ui.Transparent
	switch {
	case hovered:
		bg = iconHoverBG
	case round:
		bg = p.Hover
	}
	e.Background(e.AnimateColor("icon-bg", bg, AnimHover))
	if round {
		e.BorderWidth(1).BorderColor(p.Hairline)
	}

	e.Children(func() {
		ui.Icon(c, ic).FontSize(IconSizeDefault).TextColor(p.Text)
	})
	if tooltip != "" {
		e.Tooltip(tooltip)
	}
	return e
}

// PillStyle picks a pill button's fill: primary (accent) or secondary (the
// hover wash as its resting face).
type PillStyle int

const (
	PillPrimary PillStyle = iota
	PillSecondary
)

// PillButton is the capsule button (§5): 36 high, 18 horizontal padding, 14px
// icon + 14/600 text, radius 999. Hovering mixes the fill 12% toward white over
// 120ms; pressing scales to 0.96.
func PillButton(c *ui.Context, p Palette, style PillStyle, label string, ic *ui.SVG) ui.Element {
	return pillButton(c, p, style, label, ic, 36, 999)
}

// SendPill is the taller primary pill used by the URL group (§3): 40 high,
// radius 10, accent fill, white 14/600 text. Its icon morph is added in a later
// stage; here it is only the size/shape variant.
func SendPill(c *ui.Context, p Palette, label string, ic *ui.SVG) ui.Element {
	return pillButton(c, p, PillPrimary, label, ic, 40, 10)
}

func pillButton(c *ui.Context, p Palette, style PillStyle, label string, ic *ui.SVG, height, radius float32) ui.Element {
	base, fg := p.Accent, p.OnAccent
	if style == PillSecondary {
		base, fg = p.Hover, p.Text
	}

	e := ActionCursor(ui.Row(c).Height(height).Radius(radius).Padding(0, 18).Gap(6).AlignItems(ui.Center))

	hovered := e.Hovered()
	scale := float32(1)
	if e.Pressed() {
		scale = ScalePillPress
	}
	e.AnimatePose(ui.Pose{Scale: scale}, ui.Snappy)

	bg := base
	if hovered {
		bg = base.Mix(ui.RGB(255, 255, 255), 0.12)
	}
	e.Background(e.AnimateColor("pill-bg", bg, AnimHover))

	if ic != nil {
		ui.Icon(c, ic).FontSize(14).TextColor(fg)
	}
	ui.Text(c, label).FontSize(14).FontWeight(600).TextColor(fg).SingleLine()
	return e
}

// Segmented is the AppKit-style segmented control (§5): 24 high, 2 inset,
// radius 6, a sliding capsule (light: white with a soft shadow; dark: white at
// 24%). The capsule slides with the shared spring and the hairline dividers
// adjacent to it hide. labels are the segment titles; *selected is the chosen
// index. The returned bool reports a change this frame.
//
// It is the original-signature entry point of Segments; new callers that need
// optional icons, badges or disabled segments use Segments with TabEntry
// values.
func Segmented(c *ui.Context, p Palette, selected *int, labels []string) (ui.Element, bool) {
	return Segments(c, p, selected, entriesFromLabels(labels))
}

// SimpleSegmented is a small two-or-more-way switch built from ordinary layout
// (no absolutely positioned capsule), so it stays aligned at any explicit
// width: a rounded track with one rounded cell per label, the chosen cell
// filled with the capsule color over a 200ms fade. It shares the radio-group
// keyboard and disabled semantics of Segments. The returned bool reports a
// change this frame.
func SimpleSegmented(c *ui.Context, p Palette, selected *int, labels []string) (ui.Element, bool) {
	return segmentSet(c, p, selected, entriesFromLabels(labels), false)
}

func trackColor(p Palette) ui.Color {
	if p.Dark {
		return ui.RGBA(255, 255, 255, 0.08)
	}
	return ui.RGBA(0, 0, 0, 0.06)
}

func capsuleColor(p Palette) ui.Color {
	if p.Dark {
		return ui.RGBA(255, 255, 255, 0.24)
	}
	return ui.RGB(255, 255, 255)
}

func absf(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
