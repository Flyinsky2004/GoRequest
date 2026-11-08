package uikit

import "github.com/egoist/mygo/ui"

// TabEntry is one item of a themed tab strip or segmented control. Icon and
// Badge are optional: an icon sits before the label in the text color, and a
// badge is a short trailing count or status in the faint color (for example
// "3" for "Headers (3)"). Disabled entries keep their place and accessible
// name but take no pointer or keyboard input.
type TabEntry struct {
	Label    string
	Badge    string
	Icon     *ui.SVG
	Disabled bool
}

// Tabs renders the themed tab strip used for request sections and other
// top-level views: 13px labels, an optional 14px icon and trailing badge, a 2px
// accent underline that slides between tabs with AnimTabIndicator, and a
// hairline along the bottom edge. The selected tab is 600 weight in the text
// color, a hovered tab lifts to the text color, and a disabled tab stays faint
// and takes no input.
//
// The strip is one keyboard stop (FocusGroup): Tab enters at the selected tab,
// Left/Right and Home/End move the focus, and Enter/Space choose the focused
// tab. Assistive technology sees a tab list of tabs. The returned bool reports
// a selection change this frame.
//
// It is the unified implementation behind UnderlineTabs, which keeps its
// original signature for existing callers.
func Tabs(c *ui.Context, p Palette, selected *int, entries []TabEntry) (ui.Element, bool) {
	return tabSet(c, p, selected, entries)
}

func tabSet(c *ui.Context, p Palette, selected *int, entries []TabEntry) (ui.Element, bool) {
	row := ui.Row(c).Height(TabRowHeight).AlignItems(ui.Center).Gap(2).
		BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).
		Role(ui.RoleTabList).FocusGroup(ui.Horizontal)
	n := len(entries)
	if n == 0 {
		return row, false
	}
	if *selected < 0 {
		*selected = 0
	}
	if *selected >= n {
		*selected = n - 1
	}
	rowBounds := row.Bounds() // previous frame

	els := make([]ui.Element, n)
	var changed bool
	row.Children(func() {
		for i, entry := range entries {
			i, entry := i, entry
			sel := i == *selected
			b := ui.ButtonBase(c).Height(TabRowHeight).Padding(0, 10).Gap(4).AlignItems(ui.Center)
			if entry.Disabled {
				b.Disabled(true)
			}
			b = ActionCursor(b).Role(ui.RoleTab).Checked(sel)
			b.Children(func() {
				col, weight := p.Muted, 500
				switch {
				case sel:
					col, weight = p.Text, 600
				case entry.Disabled:
					col = p.Faint
				case b.Hovered():
					col = p.Text
				}
				col = b.AnimateColor("tab-fg", col, AnimTabHover)
				if entry.Icon != nil {
					ui.Icon(c, entry.Icon).FontSize(14).TextColor(col)
				}
				ui.Text(c, entry.Label).FontSize(13).FontWeight(weight).TextColor(col).SingleLine()
				if entry.Badge != "" {
					ui.Text(c, entry.Badge).FontSize(11).TextColor(p.Faint).SingleLine()
				}
			})
			b.Label(entry.Label)
			els[i] = b
			if b.Clicked() && !entry.Disabled && i != *selected {
				*selected = i
				changed = true
			}
		}

		// The indicator is positioned from the previous frame's tab
		// rectangles, so a change animates from where the underline was.
		var tx, tw float32
		if r := els[*selected].Bounds(); r.W > 0 {
			tx, tw = r.X-rowBounds.X, r.W
		}
		ix := row.AnimateTo("tab-indicator-x", tx, AnimTabIndicator)
		iw := row.AnimateTo("tab-indicator-w", tw, AnimTabIndicator)
		ui.Box(c).Absolute().Bottom(0).Left(ix).Width(iw).Height(2).Background(p.Accent)
	})
	return row, changed
}

// TabRowHeight is the height of an underline tab row and of one tab in it.
const TabRowHeight float32 = 40

// Segments renders a themed segmented control from entries: a 24-high rounded
// track with a capsule that slides under the chosen segment with
// AnimTabIndicator. Segments are a radio group: a click chooses one, the arrows
// choose as they move the focus, and Enter/Space choose the focused segment.
// Icons and badges follow TabEntry; disabled segments are skipped.
//
// It is the unified implementation behind Segmented and SimpleSegmented, which
// keep their original signatures.
func Segments(c *ui.Context, p Palette, selected *int, entries []TabEntry) (ui.Element, bool) {
	return segmentSet(c, p, selected, entries, true)
}

func segmentSet(c *ui.Context, p Palette, selected *int, entries []TabEntry, slide bool) (ui.Element, bool) {
	n := len(entries)
	if n == 0 {
		return ui.Row(c), false
	}
	parts := ui.SegmentedBase(c, selected, n)
	track := parts.Track
	track.Height(24).Radius(6).Padding(2).Background(trackColor(p))
	if slide {
		track.Gap(0)
	} else {
		track.Gap(2)
	}
	segW := float32(100) / float32(n)
	pos := track.AnimateTo("seg-pos", float32(*selected), AnimTabIndicator)

	track.Children(func() {
		if slide {
			capsule := ui.Box(c).Absolute().Top(0).Bottom(0).
				LeftPercent(pos * segW).WidthPercent(segW).Radius(4).Background(capsuleColor(p))
			capsule.Children(func() {
				// Inner tight shadow gives the light capsule its two-layer look.
				inner := ui.Box(c).Fill().Radius(4).Background(capsuleColor(p))
				if !p.Dark {
					inner.Shadow(0, 1, 2, 0, ui.RGBA(0, 0, 0, 0.10))
				}
				capsule.Shadow(0, 0, 1, 0, ui.RGBA(0, 0, 0, 0.06))
			})
			// Hairline dividers between segments fade next to the capsule.
			for i := 1; i < n; i++ {
				op := float32(1)
				if absf(pos-float32(i)) < 0.5 || absf(pos-float32(i-1)) < 0.5 {
					op = 0
				}
				d := ui.Box(c).Absolute().Top(5).Height(10).Width(1).
					LeftPercent(float32(i) * segW).Background(p.Hairline)
				d.Opacity(d.AnimateTo("seg-div", op, AnimTabHover))
			}
		}

		for i, entry := range entries {
			i, entry := i, entry
			sel := i == *selected
			s := parts.Segment(i).Grow(1).FillHeight().Gap(4).Radius(4)
			if !slide {
				s.Padding(0, 6)
			}
			if entry.Disabled {
				s.Disabled(true)
			}
			s = ActionCursor(s)
			col, weight := p.Muted, 500
			switch {
			case sel:
				col, weight = p.Text, 600
			case entry.Disabled:
				col = p.Faint
			case s.Hovered():
				col = p.Text
			}
			s.TextColor(col)
			if !slide {
				face := ui.Transparent
				switch {
				case sel:
					face = capsuleColor(p)
				case s.Pressed():
					face = p.Press
				case s.Hovered():
					face = p.Hover
				}
				s.Background(s.AnimateColor("seg-face", face, AnimTabContent))
			}
			s.Children(func() {
				if entry.Icon != nil {
					ui.Icon(c, entry.Icon).FontSize(14)
				}
				ui.Text(c, entry.Label).FontSize(13).FontWeight(weight).SingleLine()
				if entry.Badge != "" {
					ui.Text(c, entry.Badge).FontSize(11).TextColor(p.Faint).SingleLine()
				}
			})
			s.Label(entry.Label)
		}
	})
	return track, track.Changed()
}

// TabContent renders the page of the selected tab with the shared content
// transition: a 200ms fade and a 4 DIP rise (AnimTabContent). Only the selected
// page is built, so switching retargets the same two animation channels and can
// never stack an old page over the new one; rapid switches therefore always
// settle on the last selection.
//
// children must build the page for the index it receives and keep editable
// state outside the view (as the request tabs do), so a switch away and back
// keeps the edit. The transition stops asking for frames once it settles, and
// ReduceMotion makes it arrive at once.
func TabContent(c *ui.Context, selected int, children func(selected int)) ui.Element {
	e := ui.Box(c).FillWidth()
	st := ui.Local(e, "tab-content", func() tabContentState { return tabContentState{} })
	if !st.started {
		st.started = true
		st.selected = selected
		e.Jump("tab-fade", 1)
		e.Jump("tab-rise", 0)
	} else if st.selected != selected {
		st.selected = selected
		e.Jump("tab-fade", 0)
		e.Jump("tab-rise", TabContentRise)
	}
	fade := e.AnimateTo("tab-fade", 1, AnimTabContent)
	rise := e.AnimateTo("tab-rise", 0, AnimTabContent)
	e.Opacity(fade).Translate(0, rise)
	e.Children(func() { children(selected) })
	return e
}

type tabContentState struct {
	started  bool
	selected int
}

// entriesFromLabels builds plain entries for the compatibility wrappers.
func entriesFromLabels(labels []string) []TabEntry {
	entries := make([]TabEntry, len(labels))
	for i, label := range labels {
		entries[i].Label = label
	}
	return entries
}
