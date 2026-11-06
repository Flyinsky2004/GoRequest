package uikit

import "github.com/egoist/mygo/ui"

// NativeSegmented retains MyGo v0.7.4's segmented geometry, theme and radio
// group semantics. A cursor on ui.Segmented's track cannot reach its clickable
// radio children, so use the public parts API to set it on each segment.
func NativeSegmented(c *ui.Context, selected *int, labels ...string) ui.Element {
	t := c.Theme()
	parts := ui.SegmentedBase(c, selected, len(labels))
	g := parts.Track
	g.AlignItems(ui.Stretch).Padding(t.Space(0.5)).Gap(t.Space(0.5)).
		Radius(t.Radius).Background(t.Surface).Border(1, t.Border)
	g.Children(func() {
		for i, label := range labels {
			s := ActionCursor(parts.Segment(i))
			s.Padding(t.Space(1), t.Space(3)).Gap(t.Space(1.5)).
				Radius(max(t.Radius-t.Space(0.5), 0)).TextColor(t.Text)
			bg := ui.Transparent
			if i == *selected {
				bg = t.Background
				if t.Dark {
					bg = t.SurfacePressed
				}
				s.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.12))
			} else if !s.IsDisabled() {
				if s.Pressed() {
					bg = t.SurfacePressed
				} else if s.Hovered() {
					bg = t.SurfaceHover
				}
			}
			s.Background(bg)
			s.Children(func() { ui.Text(c, label).SingleLine() })
		}
	})
	return g
}
