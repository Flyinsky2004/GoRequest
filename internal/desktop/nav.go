package desktop

import (
	"github.com/egoist/mygo/ui"
)

// nav draws the project work window's left rail. Unlike the old global sidebar
// it shows no cross-project recent list: only the current project's Requests
// and Documents, plus clear "Open Project…" / "Projects" entries and Settings.
func (a *App) nav(c *ui.Context, t *ui.Theme) {
	ps := a.ps
	m := a.msg()
	ui.Column(c).Width(a.navWidth).FillHeight().Background(t.Surface).Children(func() {
		// Header: app + current project, and project-switching actions.
		ui.Column(c).Padding(14, 14, 8, 14).Gap(8).Children(func() {
			ui.Text(c, "GoRequest").Bold().FontSize(17).SingleLine()
			ui.Text(c, ps.name()).FontSize(12).TextColor(t.TextMuted).SingleLine()
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, m.OpenProject).Grow(1).Tooltip(m.ChooseFolderTip).Clicked() {
					a.openProjectPicker()
				}
				if ui.Button(c, m.Projects).Tooltip(m.ProjectsTip).Clicked() {
					a.switchProject()
				}
			})
		})
		ui.Box(c).Height(1).Background(t.Border)

		ui.Scroll(c).Grow(1).FillWidth().Label(m.ProjectContents).Children(func() {
			a.projectContents(c, t)
		})

		// Fixed Settings entry.
		ui.Box(c).Height(1).Background(t.Border)
		ui.Column(c).Padding(8, 10).Children(func() {
			b := ui.Button(c, "").FillWidth().Tooltip(m.OpenSettingsTip)
			b.Children(func() {
				ui.Icon(c, iconSettings).FontSize(15).TextColor(t.TextMuted)
				ui.Text(c, m.Settings).SingleLine().Grow(1)
			})
			if b.Clicked() {
				a.showSettings = true
			}
		})
	})
}
