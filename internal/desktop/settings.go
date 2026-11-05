package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0 (MVP)"

// applyAppearance applies the stored appearance preference for the frame. When
// the preference is System (or unset) the frame keeps the system theme.
func applyAppearance(c *ui.Context, cfg *configStore) {
	switch cfg.appearance() {
	case "Light":
		c.SetTheme(ui.LightTheme())
	case "Dark":
		c.SetTheme(ui.DarkTheme())
	}
}

// renderSettings shows the native settings sheet over any window (welcome or
// project) while *open is true. It exposes configuration that actually works —
// appearance and the startup restore preference — plus real application info.
func renderSettings(c *ui.Context, t *ui.Theme, cfg *configStore, open *bool, setStatus func(string)) {
	if !*open {
		return
	}
	ui.Modal(c, open, func() {
		settingsBody(c, t, cfg, open, setStatus)
	})
}

func settingsBody(c *ui.Context, t *ui.Theme, cfg *configStore, open *bool, setStatus func(string)) {
	ui.Column(c).Gap(14).MinWidth(380).Children(func() {
		ui.Text(c, "Settings").FontSize(18).Bold()

		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "Appearance").FontSize(12).Bold().TextColor(t.TextMuted)
			choice := appearanceLabel(cfg)
			if ui.Select(c, &choice, []string{"System", "Light", "Dark"}).Width(180).Changed() {
				cfg.setAppearance(choice)
				if setStatus != nil {
					setStatus("appearance: " + choice)
				}
			}
		})

		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, "Startup").FontSize(12).Bold().TextColor(t.TextMuted)
			restore := cfg.restoreLastEnabled()
			if ui.Checkbox(c, &restore, "Restore last opened project on startup").Changed() {
				cfg.setRestoreLast(restore)
				if setStatus != nil {
					if restore {
						setStatus("startup: restore last project on")
					} else {
						setStatus("startup: always show project chooser")
					}
				}
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
			infoRow("Config file", configLocation(cfg))
			infoRow("Recent projects", fmt.Sprintf("%d", len(cfg.recent())))
		})

		ui.Text(c, "Secret values come from environment variables named GOREQUEST_SECRET_<NAME>; project files keep only {{secret.name}} references.").
			FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, "Done").Clicked() {
				*open = false
			}
		})
	})
}

// appearanceLabel maps the stored preference to what the Select shows.
func appearanceLabel(cfg *configStore) string {
	switch cfg.appearance() {
	case "Light":
		return "Light"
	case "Dark":
		return "Dark"
	default:
		return "System"
	}
}

// configLocation returns the config path shown in settings, or a note when the
// platform provides none.
func configLocation(cfg *configStore) string {
	if cfg.path == "" {
		return "in-memory (no config directory)"
	}
	return displayPath(cfg.path)
}
