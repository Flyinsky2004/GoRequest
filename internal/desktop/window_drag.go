package desktop

import "github.com/egoist/mygo/ui"

// windowDragRegion is only for title/brand areas without controls. MyGo starts
// the platform drag on pointer-down and delegates title-bar double clicks to
// the platform. Do not add Clicked, TrackPointer or a custom gesture handler:
// those flags would take precedence over DragWindow in MyGo's input routing.
func windowDragRegion(c *ui.Context) ui.Element {
	return ui.Column(c).DragWindow()
}

// windowDragBrand adds a handle inside the existing brand header without
// changing its layout (owned by UI05). Keep the entire native control inset
// clear, even when it is larger than the layout's usual macOS safe area.
// Call inside the brand header only, never on the sidebar or application root.
func windowDragBrand(c *ui.Context, left float32) {
	bar := c.TitleBar()
	windowDragRegion(c).Absolute().Top(0).Bottom(0).
		Left(max(left, bar.Left)).Right(bar.Right)
}

// windowDragSpacer replaces just the flexible blank between toolbar groups.
// Its handle includes the row's vertical padding, but never the neighbouring
// controls, gaps or bottom border. It consumes no extra layout space.
func windowDragSpacer(c *ui.Context, padding float32) {
	ui.Box(c).Grow(1).AlignSelf(ui.Stretch).Children(func() {
		windowDragRegion(c).Absolute().Left(0).Right(0).
			Top(-padding).Bottom(-padding)
	})
}

// windowDragInsets keeps toolbar controls and its blank drag handle out of
// native title buttons on every platform. With the sidebar visible, its brand
// header already reserves the left controls. Ordinary decorated windows and
// fullscreen report zero insets and keep the existing 12-DIP toolbar padding.
func windowDragInsets(c *ui.Context, sidebarHidden bool) (left, right float32) {
	bar := c.TitleBar()
	left, right = 12, max(float32(12), bar.Right)
	if sidebarHidden {
		left = max(left, bar.Left)
	}
	return
}
