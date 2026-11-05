package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"
)

const appVersion = "0.1.0 (MVP)"

// applyAppearance applies the stored appearance preference for the frame. When
// the preference is System (or unset) the frame keeps the system theme. The
// stored value stays a stable English token ("System"/"Light"/"Dark") so an
// existing config keeps working; only its display label is localized.
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
// appearance, interface language and the startup restore preference — plus real
// application info.
func renderSettings(c *ui.Context, t *ui.Theme, cfg *configStore, open *bool, setStatus func(string)) {
	if !*open {
		return
	}
	ui.Modal(c, open, func() {
		settingsBody(c, t, cfg, open, setStatus)
	})
}

func settingsBody(c *ui.Context, t *ui.Theme, cfg *configStore, open *bool, setStatus func(string)) {
	m := messagesFor(cfg.language())
	ui.Column(c).Gap(14).MinWidth(380).Children(func() {
		ui.Text(c, m.SettingsTitle).FontSize(18).Bold()

		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, m.AppearanceLabel).FontSize(12).Bold().TextColor(t.TextMuted)
			choice := appearanceDisplay(m, cfg.appearance())
			if ui.Select(c, &choice, appearanceChoices(m)).Width(180).Changed() {
				cfg.setAppearance(appearanceCanonical(m, choice))
				if setStatus != nil {
					setStatus(translate(m.AppearanceStatus, appearanceDisplay(m, cfg.appearance())))
				}
			}
		})

		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, m.LanguageLabel).FontSize(12).Bold().TextColor(t.TextMuted)
			choice := languageLabel(cfg.language())
			if ui.Select(c, &choice, []string{languageLabelChinese, languageLabelEnglish}).Width(180).Changed() {
				cfg.setLanguage(languageFromLabel(choice))
				if setStatus != nil {
					// Confirm in the newly selected language so the status line
					// matches the interface the user just switched to.
					langMsgs := messagesFor(cfg.language())
					setStatus(translate(langMsgs.LanguageStatus, languageLabel(cfg.language())))
				}
			}
		})

		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, m.StartupLabel).FontSize(12).Bold().TextColor(t.TextMuted)
			restore := cfg.restoreLastEnabled()
			if ui.Checkbox(c, &restore, m.RestoreLast).Changed() {
				cfg.setRestoreLast(restore)
				if setStatus != nil {
					if restore {
						setStatus(m.StartupOn)
					} else {
						setStatus(m.StartupOff)
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
			infoRow(m.VersionLabel, appVersion)
			infoRow(m.PlatformLabel, runtime.GOOS+"/"+runtime.GOARCH)
			infoRow(m.GoLabel, runtime.Version())
			infoRow(m.ConfigFileLabel, configLocation(m, cfg))
			infoRow(m.RecentProjectsLabel, fmt.Sprintf("%d", len(cfg.recent())))
		})

		ui.Text(c, m.SecretNote).FontSize(12).TextColor(t.TextMuted)

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, m.Done).Clicked() {
				*open = false
			}
		})
	})
}

// appearanceChoices returns the localized appearance options in display order.
func appearanceChoices(m *messages) []string {
	return []string{m.AppearanceSystem, m.AppearanceLight, m.AppearanceDark}
}

// appearanceDisplay maps a stored appearance token to its localized label.
func appearanceDisplay(m *messages, v string) string {
	switch v {
	case "Light":
		return m.AppearanceLight
	case "Dark":
		return m.AppearanceDark
	default:
		return m.AppearanceSystem
	}
}

// appearanceCanonical maps a localized appearance label back to the stable
// stored token, so the persisted value is language-independent.
func appearanceCanonical(m *messages, label string) string {
	switch label {
	case m.AppearanceLight:
		return "Light"
	case m.AppearanceDark:
		return "Dark"
	default:
		return "System"
	}
}

// configLocation returns the config path shown in settings, or a note when the
// platform provides none.
func configLocation(m *messages, cfg *configStore) string {
	if cfg.path == "" {
		return m.InMemoryConfig
	}
	return displayPath(cfg.path)
}
