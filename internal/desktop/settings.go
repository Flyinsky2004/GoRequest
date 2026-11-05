package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0 (MVP)"

// settingsModal shows the native settings sheet over the workspace while
// a.showSettings is true. It only exposes configuration that actually works
// (appearance) plus real application information, and closes back to the work
// area without touching any open tabs or edits.
func (a *App) settingsModal(c *ui.Context, t *ui.Theme) {
	if !a.showSettings {
		return
	}
	ui.Modal(c, &a.showSettings, func() {
		a.settingsBody(c, t)
	})
}

func (a *App) settingsBody(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(14).MinWidth(380).Children(func() {
		ui.Text(c, "Settings").FontSize(18).Bold()

		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "Appearance").FontSize(12).Bold().TextColor(t.TextMuted)
			choice := a.appearanceLabel()
			if ui.Select(c, &choice, []string{"System", "Light", "Dark"}).Width(180).Changed() {
				a.cfg.setAppearance(choice)
				a.status = "appearance: " + choice
			}
		})

		ui.Box(c).Height(1).Background(t.Border)

		ui.Column(c).Gap(3).Children(func() {
			infoRow := func(k, v string) {
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, k).Width(120).FontSize(12).TextColor(t.TextMuted).SingleLine()
					ui.Text(c, v).FontSize(12).SingleLine().Grow(1)
				})
			}
			infoRow("Version", appVersion)
			infoRow("Platform", runtime.GOOS+"/"+runtime.GOARCH)
			infoRow("Go", runtime.Version())
			infoRow("Config file", a.configLocation())
			infoRow("Recent projects", fmt.Sprintf("%d", len(a.cfg.recent())))
		})

		ui.Text(c, "Secret values come from environment variables named GOREQUEST_SECRET_<NAME>; project files keep only {{secret.name}} references.").
			FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "Done").Clicked() {
				a.showSettings = false
			}
		})
	})
}

// appearanceLabel maps the stored preference to what the Select shows.
func (a *App) appearanceLabel() string {
	switch a.cfg.appearance() {
	case "Light":
		return "Light"
	case "Dark":
		return "Dark"
	default:
		return "System"
	}
}

// configLocation returns the config path shown in settings, or a note when the
// platform provides no config directory.
func (a *App) configLocation() string {
	if a.cfg.path == "" {
		return "in-memory (no config directory)"
	}
	return displayPath(a.cfg.path)
}
