package uikit

import "github.com/egoist/mygo/ui"

// TabSpec describes one request/document tab for TabItem.
type TabSpec struct {
	// Method is the HTTP method shown as a small colored monospace tag; empty
	// for a document tab.
	Method string
	// MethodColor overrides the default HTTP method color for other protocols.
	MethodColor *ui.Color
	Title       string
	Active      bool
	Dirty       bool
	// Icon is an optional leading icon, drawn in the tab's text color, for
	// document or protocol tabs that are not identified by a method.
	Icon *ui.SVG
	// Badge is an optional short trailing count or status in the faint color.
	Badge string
	// Disabled keeps the tab visible and named but takes no pointer or keyboard
	// input.
	Disabled bool
}

// TabItemActions are the optional behaviors of a request tab strip. Nil
// callbacks are ignored. OnDoubleClick takes precedence over OnActivate for a
// double click, as in-place rename does.
type TabItemActions struct {
	OnActivate    func()
	OnClose       func()
	OnDoubleClick func()
	OnMiddleClick func()
	OnContextMenu func(*ui.Menu)
}

// TabItem renders one tab of the request strip (§4): a method-colored monospace
// tag, a truncated title, an unsaved amber dot and a 2px accent underline on the
// active tab. Unselected tabs are borderless and transparent, gaining only a
// hover wash, so the strip reads as rows rather than buttons.
//
// The close × is always in the tree (so assistive technology and the headless
// tester can reach its stable label) but is drawn transparent unless the tab is
// hovered or its close button has keyboard focus. A dirty tab shows its amber
// dot instead while not hovered, and the dot turns into the × as the pointer
// enters, exactly as §4 describes.
//
// TabItem is the original-signature entry point of TabItemWithActions; callers
// that need double-click rename, middle-click close or a context menu use that
// function directly.
func TabItem(c *ui.Context, p Palette, key any, s TabSpec, closeLabel string) (ui.Element, bool, bool) {
	var clicked, closeClicked bool
	outer := TabItemWithActions(c, p, key, s, closeLabel, TabItemActions{
		OnActivate: func() { clicked = true },
		OnClose:    func() { closeClicked = true },
	})
	if closeClicked {
		clicked = false
	}
	return outer, clicked, closeClicked
}

// TabItemWithActions renders one request/document tab with the optional strip
// behaviors: activation, close, double-click rename, middle-click close and a
// context menu. The tab keeps the method tag, dirty dot/× swap, active accent
// underline and the native click, focus and keyboard semantics of its button.
func TabItemWithActions(c *ui.Context, p Palette, key any, s TabSpec, closeLabel string, a TabItemActions) ui.Element {
	outer := ui.Row(c).Key(key).Height(TabHeight).Radius(8).Padding(0, 6).Gap(4).
		AlignItems(ui.Center).MaxWidth(180).Clip()
	if s.Disabled {
		outer.Disabled(true)
	}
	hovered := outer.Hovered() && !s.Disabled
	switch {
	case s.Active:
		outer.Background(p.BG)
	case hovered:
		outer.Background(p.Hover)
	default:
		outer.Background(ui.Transparent)
	}
	titleColor := p.Muted
	if s.Active {
		titleColor = p.Text
	}
	if s.Disabled {
		titleColor = p.Faint
	}

	label := s.Title
	if s.Method != "" {
		label = s.Method + " " + s.Title
	}
	if s.Dirty {
		label += " •"
	}

	if !s.Disabled {
		// The whole tab answers pointer input: the secondary button targets the
		// tab (activating it, as VSCode does) before its context menu opens, and
		// the middle button closes it like a browser. MyGo reports the pointer's
		// button in InputEvent (1 = secondary, 2 = middle).
		outer.HandleInput(func(ev ui.InputEvent) bool {
			if ev.Kind != ui.InputPointerDown {
				return false
			}
			switch ev.Button {
			case 1:
				if a.OnActivate != nil {
					a.OnActivate()
				}
			case 2:
				if a.OnMiddleClick != nil {
					a.OnMiddleClick()
					return true
				}
			}
			return false
		})
	}
	if a.OnContextMenu != nil && !s.Disabled {
		outer.PopoverMenu(a.OnContextMenu)
	}

	outer.Children(func() {
		act := ui.ButtonBase(c).Grow(1).Height(TabHeight - 2).Gap(6).PaddingX(2)
		if s.Disabled {
			act.Disabled(true)
		}
		act = ActionCursor(act).Role(ui.RoleTab).Checked(s.Active)
		act.Children(func() {
			if s.Icon != nil {
				ui.Icon(c, s.Icon).FontSize(14).TextColor(titleColor)
			}
			if s.Method != "" {
				color := MethodColor(s.Method, p)
				if s.MethodColor != nil {
					color = *s.MethodColor
				}
				ui.Text(c, s.Method).Font("monospace").FontWeight(700).FontSize(11).
					TextColor(color).Width(38).SingleLine()
			}
			ui.Text(c, s.Title).FontSize(13).TextColor(titleColor).Grow(1).SingleLine()
			if s.Badge != "" {
				ui.Text(c, s.Badge).FontSize(11).TextColor(p.Faint).SingleLine()
			}
		})
		act.Label(label)
		switch {
		case act.DoubleClicked():
			if a.OnDoubleClick != nil {
				a.OnDoubleClick()
			} else if a.OnActivate != nil {
				a.OnActivate()
			}
		case act.Clicked():
			if a.OnActivate != nil {
				a.OnActivate()
			}
		}

		if TabClose(c, p, closeLabel, s.Dirty, hovered, s.Disabled).Clicked() {
			if a.OnClose != nil {
				a.OnClose()
			}
		}
		if s.Active {
			ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(2).Background(p.Accent)
		}
	})
	return outer
}

// TabHeight leaves a 24 DIP close target inside the compact strip.
const TabHeight float32 = 26

// TabClose keeps the status dot and vector cross on the same geometric center.
// ButtonBase retains native keyboard, focus and accessibility behavior without
// the default Button padding overflowing the old 16 DIP status slot. The
// optional disabled argument keeps the control out of the focus order and
// pointer input for a disabled tab.
func TabClose(c *ui.Context, p Palette, label string, dirty, hovered bool, disabled ...bool) ui.Element {
	b := ui.ButtonBase(c).Size(24, 24).Radius(4).Label(label)
	if len(disabled) > 0 && disabled[0] {
		b.Disabled(true)
	}
	b = ActionCursor(b)
	visible := hovered || b.Focused()
	if b.Hovered() || b.Pressed() {
		b.Background(p.Hover)
	}
	b.Draw(func(painter *ui.Painter, r ui.Rect) {
		x, y := r.X+r.W/2, r.Y+r.H/2
		if visible {
			var cross ui.Path
			cross.MoveTo(x-3, y-3).LineTo(x+3, y+3)
			cross.MoveTo(x+3, y-3).LineTo(x-3, y+3)
			painter.StrokePath(&cross, 1.5, p.Muted)
		} else if dirty {
			painter.Fill(ui.Rect{X: x - 3.5, Y: y - 3.5, W: 7, H: 7}, MethodColor("POST", p), 3.5)
		}
	})
	return b
}
