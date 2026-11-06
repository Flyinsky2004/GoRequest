package uikit

import (
	"github.com/egoist/mygo/plugins/glass"
	"github.com/egoist/mygo/ui"
)

// ScrollEdges draws the hard scroll-edge material (§3) over a scroll container's
// top and bottom, shown only while content is scrolled beneath or above it — so
// at rest there is no visible band. state is the container's TrackScroll state;
// it fades each edge in/out over 120ms. Place it as a sibling after the scroll
// inside the same box; it passes the pointer through.
func ScrollEdges(c *ui.Context, p Palette, state *ui.ScrollState) {
	topOp := float32(0)
	botOp := float32(0)
	if state != nil {
		if state.Y > 1 {
			topOp = 1
		}
		if state.Y < state.MaxY-1 {
			botOp = 1
		}
	}

	top := ui.Box(c).Absolute().Top(0).Left(0).Right(0).Height(18).PassThrough().
		Material(glass.ScrollEdge{Hard: true, Background: p.BG})
	top.Opacity(top.AnimateTo("scroll-edge-top", topOp, AnimHover))

	bot := ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(18).PassThrough().
		Material(glass.ScrollEdge{Hard: true, Bottom: true, Background: p.BG})
	bot.Opacity(bot.AnimateTo("scroll-edge-bottom", botOp, AnimHover))
}
