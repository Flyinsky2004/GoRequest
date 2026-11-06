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

// SendButton is the Send control (§3, morphing per §6.2): a 40×104 accent pill
// whose icon morphs between a paper plane (Send) and an × (Cancel) with the
// 340ms/0.30 icon spring — the two icons cross-scale and cross-fade rather than
// hard-cutting — with the label switching between send and cancel.
func SendButton(c *ui.Context, p Palette, sending bool, label string) (ui.Element, bool) {
	e := ui.Row(c).Height(40).Width(104).Radius(10).Center().Background(p.Accent)
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
