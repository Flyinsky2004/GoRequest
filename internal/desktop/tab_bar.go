package desktop

import (
	"fmt"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
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
	p := uikit.PaletteFor(t)
	ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Padding(0, 8).
		Background(p.BG).BorderWidth(0, 0, 1, 0).BorderColor(p.Hairline).Children(func() {
		ui.ScrollHorizontal(c).Grow(1).Height(40).Children(func() {
			ui.Row(c).Gap(4).AlignItems(ui.Center).Children(func() {
				for _, tab := range ps.tabs {
					a.tabItem(c, t, ps, tab)
				}
			})
		})
		a.openedMenu(c, t, ps)
	})
}

// tabItem draws one tab through the kit's TabItem: a method-colored tag, a
// truncated title, an unsaved dot or a close × on hover, and the active
// tab's accent underline. The full label and the close label are the
// accessibility identities the strip's actions (and tests) use.
func (a *App) tabItem(c *ui.Context, t *ui.Theme, ps *projectState, tab *Tab) {
	p := uikit.PaletteFor(t)
	m := a.msg()
	spec := uikit.TabSpec{
		Title:  tab.Title,
		Active: ps.activeTab() == tab,
		Dirty:  tab.Dirty,
	}
	if tab.Kind == "request" {
		spec.Method = tab.Method
	}
	closeLabel := translate(m.CloseTabTip, tabIdentity(tab))
	_, clicked, closeClicked := uikit.TabItem(c, p, "tab:"+tabIdentity(tab), spec, closeLabel)
	if closeClicked {
		a.startClose(ps, []*Tab{tab})
		return
	}
	if clicked {
		ps.activateTab(tab)
	}
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
