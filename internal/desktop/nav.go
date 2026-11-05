package desktop

import (
	"path/filepath"

	"github.com/egoist/mygo/ui"
)

// nav draws the always-present, full-height left navigation rail: the app name
// and Open Project action, the scrollable recent-project list, the current
// project's requests and documents, and a fixed Settings entry at the bottom.
func (a *App) nav(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Width(a.navWidth).FillHeight().Background(t.Surface).Children(func() {
		// Header: name and the Open Project action.
		ui.Column(c).Padding(14, 14, 8, 14).Gap(10).Children(func() {
			ui.Text(c, "GoRequest").Bold().FontSize(17).SingleLine()
			if ui.PrimaryButton(c, "Open Project…").FillWidth().Tooltip("Choose a folder containing .gorequest/").Clicked() {
				a.pickProject()
			}
		})
		ui.Box(c).Height(1).Background(t.Border)

		// Project list, independently scrollable like a chat client's sessions.
		ui.Row(c).Padding(10, 14, 4, 14).Children(func() {
			ui.Text(c, "Projects").FontSize(11).Bold().TextColor(t.TextMuted).SingleLine()
		})
		sb := ui.Sidebar(c, &a.projectSel, func() {
			projects := a.cfg.recent()
			if len(projects) == 0 {
				ui.Text(c, "No projects yet. Open one above.").FontSize(12).TextColor(t.TextMuted).Padding(2, 8)
				return
			}
			for _, rp := range projects {
				ui.SidebarItem(c, "project:"+rp.Path, nil, a.projectLabel(rp))
			}
		}).Grow(1).FillWidth().Label("Projects")
		if sb.Changed() {
			a.chooseProject()
		}

		// Current project contents in their own scroll area, clearly separated.
		if a.current != nil {
			ui.Box(c).Height(1).Background(t.Border)
			ui.Row(c).Padding(10, 14, 4, 14).Children(func() {
				ui.Text(c, a.current.name()).FontSize(11).Bold().TextColor(t.TextMuted).SingleLine()
			})
			ui.Scroll(c).Grow(1).FillWidth().Label("Project contents").Children(func() {
				a.projectContents(c, t)
			})
		}

		// Fixed Settings entry, separated from the scrolling lists.
		ui.Box(c).Height(1).Background(t.Border)
		ui.Column(c).Padding(8, 10).Children(func() {
			b := ui.Button(c, "").FillWidth().Tooltip("Open settings")
			b.Children(func() {
				ui.Icon(c, iconSettings).FontSize(15).TextColor(t.TextMuted)
				ui.Text(c, "Settings").SingleLine().Grow(1)
			})
			if b.Clicked() {
				a.showSettings = true
			}
		})
	})
}

// projectLabel returns the sidebar label for a recent project, adding a muted
// path suffix when two projects share a name.
func (a *App) projectLabel(rp recentProject) string {
	name := a.projectName(rp)
	if a.duplicateName(name) {
		return name + "  ·  " + displayPath(rp.Path)
	}
	return name
}

func (a *App) projectName(rp recentProject) string {
	if ps := a.findProject(rp.Path); ps != nil {
		return ps.name()
	}
	if rp.Name != "" {
		return rp.Name
	}
	return filepath.Base(rp.Path)
}

func (a *App) duplicateName(name string) bool {
	count := 0
	for _, rp := range a.cfg.recent() {
		if a.projectName(rp) == name {
			count++
		}
	}
	return count > 1
}
