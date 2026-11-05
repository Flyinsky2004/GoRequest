package desktop

import (
	"github.com/egoist/mygo/ui"
)

// nav draws the project work window's left rail. Unlike the old global sidebar
// it shows no cross-project recent list: only the current project's Requests
// and Documents, plus clear "Open Project…" / "Projects" entries and Settings.
func (a *App) nav(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	ui.Column(c).Width(a.navWidth).FillHeight().Background(t.Surface).Children(func() {
		// Header: app + current project, and project-switching actions.
		ui.Column(c).Padding(14, 14, 8, 14).Gap(8).Children(func() {
			ui.Text(c, "GoRequest").Bold().FontSize(17).SingleLine()
			ui.Text(c, ps.name()).FontSize(12).TextColor(t.TextMuted).SingleLine()
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Open Project…").Grow(1).Tooltip("Choose a folder containing .gorequest/").Clicked() {
					a.openProjectPicker()
				}
				if ui.Button(c, "Projects").Tooltip("Show the project chooser").Clicked() {
					a.switchProject()
				}
			})
		})
		ui.Box(c).Height(1).Background(t.Border)

		// Current project contents in their own scroll area.
		cs := ui.Sidebar(c, &ps.sel, func() {
			ui.SidebarSection(c, "Requests", nil, func() {
				reqs := allRequests(ps.project)
				if len(reqs) == 0 {
					ui.Text(c, "No requests").FontSize(12).TextColor(t.TextMuted).Padding(2, 8)
				}
				for _, re := range reqs {
					ui.SidebarItem(c, "req:"+re.Request.ID, nil, re.Request.Name)
				}
			})
			ui.SidebarSection(c, "Documents", nil, func() {
				if len(ps.project.Docs) == 0 {
					ui.Text(c, "No documents").FontSize(12).TextColor(t.TextMuted).Padding(2, 8)
				}
				for _, d := range ps.project.Docs {
					ui.SidebarItem(c, "doc:"+d.RelPath, nil, d.Name)
				}
			})
		}).Grow(1).FillWidth().Label("Project contents")
		if cs.Changed() {
			if err := ps.openSelection(ps.sel); err != nil {
				a.status = err.Error()
			}
		}

		// Fixed Settings entry.
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
