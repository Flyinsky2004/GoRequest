package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

// This file draws the open-tab strip above the workspace and the "Open Tabs"
// menu that lists every open request/document for this project. Tabs keep direct
// click switching plus a per-tab close button; the menu offers a full list with
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

// tabBar draws the horizontally scrolling tab strip with a trailing "Open Tabs"
// menu, so a narrow window never squeezes the URL/editor area and the active tab
// stays reachable.
func (a *App) tabBar(c *ui.Context, t *ui.Theme, ps *projectState) {
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(4, 6).
		BorderWidth(0, 0, 1, 0).BorderColor(t.Border).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).Height(34).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				for _, tab := range ps.tabs {
					a.tabItem(c, t, ps, tab)
				}
			})
		})
		a.openedMenu(c, t, ps)
	})
}

// tabItem draws one tab: a clickable label and a close button. The active tab is
// styled distinctly. A stable key and unique close label keep interaction on the
// intended tab even as neighbours are added or removed.
func (a *App) tabItem(c *ui.Context, t *ui.Theme, ps *projectState, tab *Tab) {
	active := ps.activeTab() == tab
	bg := t.Background
	if active {
		bg = t.Surface
	}
	ui.Row(c).Key("tab:"+tabIdentity(tab)).AlignItems(ui.Center).Gap(2).
		Padding(3, 6).Radius(6).Background(bg).Children(func() {
		m := a.msg()
		label := tabDisplayLabel(tab)
		b := ui.Button(c, label)
		if active {
			b.Bold()
		}
		if b.Clicked() {
			ps.activateTab(tab)
		}
		closeLabel := translate(m.CloseTabTip, tabIdentity(tab))
		if ui.Button(c, "✕").Label(closeLabel).Tooltip(translate(m.CloseTabTip, tab.Title)).Clicked() {
			a.startClose(ps, []*Tab{tab})
		}
	})
}

// openedMenu draws the "Open Tabs" menu button. It lists each open tab with
// Switch/Close and the bulk close actions, and also provides a discoverable
// home for closing when many tabs are open.
func (a *App) openedMenu(c *ui.Context, t *ui.Theme, ps *projectState) {
	if len(ps.tabs) == 0 {
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
	})
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
