package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// This file draws the open-tab strip above the workspace and the "Open Tabs"
// menu that lists every open request/document for this project. Tabs keep direct
// click switching plus a per-tab close button; right-clicking a tab opens a
// VSCode-like close menu for that tab, and the menu offers a full list with
// switch/close and the close-current/others/all actions.

// tabDisplayLabel is the visible label of a tab: the request name, prefixed by
// its HTTP method when known. Dirty tabs add a visible marker.
func tabDisplayLabel(tab *Tab) string {
	if tab == nil {
		return ""
	}
	label := tab.Title
	if tab.Kind == "request" && tab.Method != "" {
		label = tab.Method + " " + tab.Title
	}
	if tab.Dirty {
		label += " •"
	}
	return label
}

// tabStripMethodColor colors the protocol tag. WebSocket tabs use the neutral
// text color instead of the unknown-method faint gray, so the WS tag stays
// readable beside HTTP method tags.
func tabStripMethodColor(tab *Tab, p uikit.Palette) ui.Color {
	if isWebSocketTab(tab) {
		return wsTabTagColor(p)
	}
	return uikit.MethodColor(tab.Method, p)
}

// tabBar draws the horizontally scrolling tab strip with a trailing "Open Tabs"
// menu, so a narrow window never squeezes the URL/editor area and the active tab
// stays reachable.
func (a *App) tabBar(c *ui.Context, t *ui.Theme, ps *projectState) {
	p := uikit.PaletteFor(t)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(0, 8).
		Background(p.BG).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		uikit.TabScroll(c, t, func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				for _, tab := range ps.tabs {
					a.tabItem(c, t, ps, tab)
				}
			})
		})
		a.openedMenu(c, t, ps)
	})
}

// tabItem draws one tab: a method-colored tag, a truncated title, an unsaved
// dot or a close × on hover, and the active tab's accent underline. The full
// label and the close label are the accessibility identities the strip's
// actions (and tests) use.
//
// The strip is built inline instead of through uikit.TabItem so the activation
// area can answer DoubleClicked (in-place rename, §4) directly, and so the row
// can answer middle clicks (close) and the secondary button (a per-tab close
// menu) across the whole tab.
func (a *App) tabItem(c *ui.Context, t *ui.Theme, ps *projectState, tab *Tab) {
	p := uikit.PaletteFor(t)
	if tab.renaming {
		a.tabRenameField(c, p, tab)
		return
	}
	active := ps.activeTab() == tab
	outer := ui.Row(c).Key("tab:"+tabIdentity(tab)).Height(uikit.TabHeight).Radius(8).Padding(0, 6).Gap(4).
		AlignItems(ui.Center).MaxWidth(180).Clip()
	hovered := outer.Hovered()
	switch {
	case active:
		outer.Background(p.BG)
	case hovered:
		outer.Background(p.Hover)
	default:
		outer.Background(ui.Transparent)
	}
	titleColor := p.Muted
	if active {
		titleColor = p.Text
	}
	label := tabDisplayLabel(tab)
	closeLabel := translate(a.msg().CloseTabTip, tabIdentity(tab))

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
			ps.activateTab(tab)
		case 2:
			a.startClose(ps, []*Tab{tab})
			return true
		}
		return false
	})
	outer.ContextMenu(func(m *ui.Menu) { a.tabContextMenu(m, ps, tab) })

	var clicked, closeClicked bool
	outer.Children(func() {
		act := ui.ButtonBase(c).Grow(1).Height(uikit.TabHeight - 2).Gap(6).PaddingX(2)
		if !act.IsDisabled() {
			act.Cursor(ui.CursorPointer)
		}
		act.Children(func() {
			if tab.Kind == "request" && tab.Method != "" {
				ui.Text(c, tab.Method).Font("monospace").FontWeight(700).FontSize(11).
					TextColor(tabStripMethodColor(tab, p)).Width(38).SingleLine()
			}
			ui.Text(c, tab.Title).FontSize(13).TextColor(titleColor).Grow(1).SingleLine()
		})
		act.Label(label)
		if act.DoubleClicked() {
			// Double-clicking the name edits it in place; the tab also becomes
			// active so the edit visibly belongs to what is on screen.
			beginTabRename(ps, tab)
		} else if act.Clicked() {
			clicked = true
		}
		if uikit.TabClose(c, p, closeLabel, tab.Dirty, hovered).Clicked() {
			closeClicked = true
		}
		if active {
			ui.Box(c).Absolute().Bottom(0).Left(0).Right(0).Height(2).Background(p.Accent)
		}
	})

	if closeClicked {
		clicked = false
		a.startClose(ps, []*Tab{tab})
		return
	}
	if clicked {
		ps.activateTab(tab)
	}
}

// tabContextMenu fills a tab's right-click menu with the VSCode-like close
// actions. The menu targets tab explicitly instead of the active tab, so it
// stays correct when opened by keyboard on a tab that is not active; the
// pointer path already activates the tab before the menu opens. Actions that
// would do nothing are disabled rather than hidden, so the menu keeps a stable
// shape. Every close goes through startClose, so dirty tabs still pause for
// Save/Discard/Cancel.
func (a *App) tabContextMenu(m *ui.Menu, ps *projectState, tab *Tab) {
	ps.activateTab(tab)
	msgs := a.msg()
	i := ps.indexOfTab(tab)
	if m.Item(msgs.CloseTabMenu).Chosen() {
		a.startClose(ps, []*Tab{tab})
	}
	if m.Item(msgs.CloseOthers).Disabled(len(ps.tabs) <= 1).Chosen() {
		a.closeOthers()
	}
	if m.Item(msgs.CloseRight).Disabled(i < 0 || i >= len(ps.tabs)-1).Chosen() {
		a.closeRight(ps, tab)
	}
	if m.Item(msgs.CloseAll).Chosen() {
		a.closeAll()
	}
}

// tabRenameField draws the in-place rename editor over a tab: the method tag
// stays, the title becomes a text field bound to the tab's rename draft. Enter
// commits and Esc cancels.
func (a *App) tabRenameField(c *ui.Context, p uikit.Palette, tab *Tab) {
	ui.Row(c).Key("tab:"+tabIdentity(tab)).Height(uikit.TabHeight).Radius(8).Padding(0, 6).Gap(4).
		AlignItems(ui.Center).MaxWidth(180).Background(p.BG).Children(func() {
		if tab.Kind == "request" && tab.Method != "" {
			ui.Text(c, tab.Method).Font("monospace").FontWeight(700).FontSize(11).
				TextColor(tabStripMethodColor(tab, p)).Width(38).SingleLine()
		}
		in := ui.TextInput(c, &tab.renameText).Grow(1).Height(24).FontSize(13).AutoFocus()
		in.Label(a.msg().Rename)
		in.OnShortcut(0, ui.KeyEscape, func() { cancelTabRename(tab) })
		if in.Submitted() {
			commitTabRename(tab)
		}
	})
}

// openedMenu draws the "Open Tabs" menu button. It lists each open tab with
// Switch/Close and the bulk close actions, and also provides a discoverable
// home for closing when many tabs are open. With only one or two tabs the strip
// itself reaches everything, so the menu is not rendered at all.
func (a *App) openedMenu(c *ui.Context, t *ui.Theme, ps *projectState) {
	if len(ps.tabs) <= 2 {
		return
	}
	labels := uniqueTabLabels(ps)
	msgs := a.msg()
	ui.MenuButton(c, fmt.Sprintf(msgs.OpenTabsMenu, len(ps.tabs)), func(m *ui.Menu) {
		for i, tab := range ps.tabs {
			label := labels[i]
			activeTab := ps.tabs[ps.active]
			m.Submenu(label, func(sm *ui.Menu) {
				if sm.Item(msgs.SwitchTab).Checked(tab == activeTab).Chosen() {
					ps.activateTab(tab)
				}
				if sm.Item(msgs.CloseTabMenu).Chosen() {
					a.startClose(ps, []*Tab{tab})
				}
			})
		}
		m.Separator()
		if m.Item(msgs.CloseCurrent).Chosen() {
			a.closeCurrent()
		}
		if m.Item(msgs.CloseOthers).Chosen() {
			a.closeOthers()
		}
		if m.Item(msgs.CloseAll).Chosen() {
			a.closeAll()
		}
	}).Cursor(ui.CursorPointer)
}

// uniqueTabLabels returns one menu label per tab, disambiguating duplicate
// names with a numeric suffix so every menu entry can be chosen unambiguously.
func uniqueTabLabels(ps *projectState) []string {
	labels := make([]string, len(ps.tabs))
	seen := map[string]int{}
	for i, tab := range ps.tabs {
		base := tabDisplayLabel(tab)
		seen[base]++
		if n := seen[base]; n > 1 {
			base = fmt.Sprintf("%s (%d)", base, n)
		}
		labels[i] = base
	}
	return labels
}
