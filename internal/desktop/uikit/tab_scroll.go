package uikit

import "github.com/egoist/mygo/ui"

// TabScroll reserves the top 12 DIPs for the horizontal scrollbar. Its native
// 6 DIP thumb and 12 DIP track remain the hit geometry, while only a centered
// 3 DIP indicator is painted. The scope changes no other application scroller.
// Wheel, focus reveal, track paging and captured thumb dragging stay native.
func TabScroll(c *ui.Context, theme *ui.Theme, children func()) ui.Element {
	const trackHeight float32 = 12
	local := *theme
	local.ScrollbarWidth = 6
	local.Scrollbar = ui.Transparent
	var scroll ui.Element
	ui.Themed(c, &local).Grow(1).Height(trackHeight + TabHeight).Background(ui.Transparent).Children(func() {
		scroll = ui.ScrollHorizontal(c).Fill().Padding(trackHeight, 0, 0, 0).
			ScrollbarInsets(0, 0, TabHeight, 0)
		state := ui.Local(scroll, "tab-scroll", func() ui.ScrollState { return ui.ScrollState{} })
		scroll.TrackScroll(state).Children(children)
		// The native ScrollState is updated by layout before this paint callback,
		// so the indicator also follows wheel events and viewport size changes.
		scroll.DrawOver(func(p *ui.Painter, r ui.Rect) {
			bar := ui.Scrollbar{Box: r, Horizontal: true, View: r.W, Content: r.W + state.MaxX, Offset: state.X}
			if !bar.Scrolls() {
				return
			}
			x, width := bar.Thumb()
			p.Fill(ui.Rect{X: x, Y: r.Y + 4.5, W: width, H: 3}, theme.Scrollbar, 1.5)
		})
	})
	return scroll
}
