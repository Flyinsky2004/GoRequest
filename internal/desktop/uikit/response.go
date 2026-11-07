package uikit

import (
	"time"

	"github.com/egoist/mygo/ui"
)

// StatusPill renders a response status code as a capsule (§1.3): the class color
// as text on a 10%-alpha fill of the same color, tabular figures.
func StatusPill(c *ui.Context, p Palette, code int) ui.Element {
	col, ok := StatusPillColor(code, p)
	if !ok {
		col = p.Muted
	}
	r := ui.Row(c).Padding(1, 8).Radius(999).Background(col.Alpha(0.10))
	r.Children(func() {
		ui.Textf(c, "%d", code).FontSize(11).FontWeight(800).FontFeatures("tnum").LetterSpacing(0.4).TextColor(col)
	})
	return r
}

// SendProgress is the indeterminate 2px accent progress line under the request
// area while a request is in flight (§6.2): a 30%-wide band sweeping back and
// forth over 1.2s (EaseInOut), on a faint accent track.
func SendProgress(c *ui.Context, p Palette) ui.Element {
	track := ui.Box(c).Height(2).FillWidth().Background(p.Accent.Alpha(0.15))
	track.Children(func() {
		pos := track.Keyframes("send-progress",
			[]ui.Keyframe{{At: 0, Value: 0}, {At: 1200 * time.Millisecond, Value: 70}},
			ui.KeyframeOptions{Repeat: ui.RepeatForever, Alternate: true})
		ui.Box(c).Absolute().LeftPercent(pos).WidthPercent(30).Top(0).Bottom(0).Background(p.Accent)
	})
	return track
}

// SkeletonLines draws n shimmer-shaped placeholder lines (§6.6), their widths
// taken from a fixed fake-percentage sequence so they read as ragged code. The
// blocks are static (no repeating timer); a view shows them only while loading.
func SkeletonLines(c *ui.Context, p Palette, n int) {
	widths := []float32{62, 44, 78, 53, 70, 38, 66, 49, 82, 57, 41, 73}
	ui.Column(c).Gap(9).FillWidth().Padding(10, 4).Children(func() {
		for i := 0; i < n; i++ {
			ui.Box(c).Height(10).WidthPercent(widths[i%len(widths)]).Radius(4).
				Background(p.Muted.Alpha(0.18))
		}
	})
}

// sendButtonColors are the Send control's three state fills (§1.1/§5), derived
// from the Palette alone so the control needs no theme lookup.
//
// Every state mixes the accent toward black, never toward white and never as a
// translucent wash. The label and both icons are the fixed white p.OnAccent, so
// darkening the fill only ever improves their contrast; lightening it (as a
// generic accent-hover token does in dark mode) would push white text below
// 4.5:1. The three steps are far enough apart to read as distinct states in
// both themes: light 12%/25% toward black, dark 15%/32% (dark starts lighter,
// so it needs a slightly deeper mix for the same perceived step). Rest is the
// unmodified p.Accent token.
func sendButtonColors(p Palette) (rest, hover, pressed ui.Color) {
	black := ui.RGB(0, 0, 0)
	if p.Dark {
		return p.Accent, p.Accent.Mix(black, 0.15), p.Accent.Mix(black, 0.32)
	}
	return p.Accent, p.Accent.Mix(black, 0.12), p.Accent.Mix(black, 0.25)
}

// SendButton is the Send control (§3, morphing per §6.2): a 40×104 accent pill
// whose icon morphs between a paper plane (Send) and an × (Cancel) with the
// 340ms/0.30 icon spring — the two icons cross-scale and cross-fade rather than
// hard-cutting — with the label switching between send and cancel.
//
// It also has the full pointer-state feedback (§5): resting accent, a distinct
// hover fill, and a darker/tighter pressed fill plus the 0.97 primary press
// scale. Both functional states (Send and Cancel) get the same states. Hover and
// press are visual only — the action still fires on release through Clicked(),
// never on a press or a hover. Hovered/Pressed and the explicit IsDisabled
// guard keep a disabled button (or one under a disabled ancestor) at its
// resting face with no press scale and no action. The color and pose animate
// over the shared 120ms hover beat and settle: no frame is requested once they
// reach their target, so an idle button costs no redraws.
func SendButton(c *ui.Context, p Palette, sending bool, label string) (ui.Element, bool) {
	rest, hover, pressed := sendButtonColors(p)

	e := ActionCursor(ui.Row(c).Height(40).Width(104).Radius(10).Center())

	// A disabled control (itself or under a disabled ancestor) must never show
	// a clickable state color or press feedback. Hovered/Pressed already report
	// false for one, and IsDisabled is checked explicitly as well so the resting
	// face is kept even if the disable is applied by a wrapper around the build.
	// As with ActionCursor, a caller disabling the returned element itself must
	// do so before applying further state to it (see UI02.md).
	hovered, down := e.Hovered(), e.Pressed()
	bg := rest
	switch {
	case e.IsDisabled():
		bg = rest
	case down:
		bg = pressed
	case hovered:
		bg = hover
	}
	e.Background(e.AnimateColor("send-bg", bg, AnimHover))

	scale := float32(1)
	if down && !e.IsDisabled() {
		scale = ScalePrimaryPress
	}
	e.AnimatePose(ui.Pose{Scale: scale}, ui.Snappy)

	var clicked bool
	e.Children(func() {
		icon := ui.Box(c).Size(16, 16)
		icon.Children(func() {
			so := icon.AnimateTo("send-op", boolF(!sending), SpringIconMorph)
			co := icon.AnimateTo("cancel-op", boolF(sending), SpringIconMorph)
			ss := icon.AnimateTo("send-sc", tern(sending, 0.4, 1), SpringIconMorph)
			cs := icon.AnimateTo("cancel-sc", tern(sending, 1, 0.4), SpringIconMorph)
			ui.Icon(c, IconSend).FontSize(14).TextColor(p.OnAccent).
				Opacity(so).AnimatePose(ui.Pose{Scale: ss}, SpringIconMorph)
			ui.Icon(c, IconClose).FontSize(14).TextColor(p.OnAccent).Absolute().
				Opacity(co).AnimatePose(ui.Pose{Scale: cs}, SpringIconMorph)
		})
		ui.Text(c, label).FontSize(14).FontWeight(600).TextColor(p.OnAccent).SingleLine()
	})
	if e.Clicked() {
		clicked = true
	}
	return e, clicked
}

func boolF(b bool) float32 {
	if b {
		return 1
	}
	return 0
}

func tern(cond bool, a, b float32) float32 {
	if cond {
		return a
	}
	return b
}
