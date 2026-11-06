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
	e := ui.Box(c).Size(side, side).Radius(999).Center()

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

	e := ui.Row(c).Height(height).Radius(radius).Padding(0, 18).Gap(6).AlignItems(ui.Center)

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
// radius 6, a sliding white capsule (light: white with a soft double shadow;
// dark: white at 24%). The capsule slides with the shared SpringSlide spring and
// the hairline dividers adjacent to it hide. labels are the segment titles;
// *selected is the chosen index. The returned bool reports a change this frame.
func Segmented(c *ui.Context, p Palette, selected *int, labels []string) (ui.Element, bool) {
	n := len(labels)
	segW := float32(100) / float32(n)

	track := ui.Box(c).Height(24).Radius(6).Padding(2).Background(trackColor(p))

	if *selected < 0 {
		*selected = 0
	}
	if *selected >= n {
		*selected = n - 1
	}
	pos := track.AnimateTo("seg-pos", float32(*selected), SpringSlide)

	var changed bool
	track.Children(func() {
		// Sliding capsule, behind everything.
		capLeft := pos * segW
		capsule := ui.Box(c).Absolute().Top(0).Bottom(0).
			LeftPercent(capLeft).WidthPercent(segW).Radius(4).Background(capsuleColor(p))
		capsule.Children(func() {
			// Inner tight shadow gives the light capsule its two-layer look.
			inner := ui.Box(c).Fill().Radius(4).Background(capsuleColor(p))
			if !p.Dark {
				inner.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.10))
			}
			capsule.Shadow(0, 0, 1, 0, ui.RGBA(0, 0, 0, 0.06))
		})

		// Hairline dividers between segments; those next to the chosen segment
		// fade out.
		for i := 1; i < n; i++ {
			op := float32(1)
			if absf(pos-float32(i)) < 0.5 || absf(pos-float32(i-1)) < 0.5 {
				op = 0
			}
			d := ui.Box(c).Absolute().Top(5).Height(10).Width(1).
				LeftPercent(float32(i) * segW).Background(p.Hairline)
			d.Opacity(d.AnimateTo("seg-div", op, AnimHover))
		}

		// Labels on top. The row fills the track so its cells line up with the
		// percentage-positioned capsule; a shrink-to-fit row would leave the
		// labels bunched under the capsule.
		ui.Row(c).FillWidth().FillHeight().Children(func() {
			for i, label := range labels {
				i := i
				cell := ui.Box(c).Grow(1).FillHeight().Center()
				col := p.Muted
				hovered := cell.Hovered()
				if i == *selected {
					col = p.Text
				} else if hovered {
					col = p.Text
				}
				weight := 500
				if i == *selected {
					weight = 600
				}
				ui.Text(c, label).FontSize(13).FontWeight(weight).TextColor(col).SingleLine()
				if cell.Clicked() && i != *selected {
					*selected = i
					changed = true
				}
			}
		})
	})
	return track, changed
}

// SimpleSegmented is a small two-or-more-way switch built from ordinary
// layout (no absolutely positioned capsule), so it stays aligned at any
// explicit width: a rounded track with one rounded cell per label, the chosen
// cell filled with the capsule color. The returned bool reports a change.
func SimpleSegmented(c *ui.Context, p Palette, selected *int, labels []string) (ui.Element, bool) {
	n := len(labels)
	if *selected < 0 {
		*selected = 0
	}
	if *selected >= n {
		*selected = n - 1
	}
	track := ui.Row(c).Height(24).Radius(6).Padding(2).Gap(2).Background(trackColor(p))
	var changed bool
	track.Children(func() {
		for i, label := range labels {
			i := i
			cell := ui.Box(c).Grow(1).FillHeight().Radius(4).Center()
			if i == *selected {
				cell.Background(capsuleColor(p))
			}
			cell.Children(func() {
				col, weight := p.Muted, 500
				if i == *selected {
					col, weight = p.Text, 600
				} else if cell.Hovered() {
					col = p.Text
				}
				ui.Text(c, label).FontSize(13).FontWeight(weight).TextColor(col).SingleLine()
			})
			if cell.Clicked() && i != *selected {
				*selected = i
				changed = true
			}
		}
	})
	return track, changed
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
