package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

const appVersion = "0.1.0 (MVP)"

// applyAppearance applies the stored appearance preference for the frame. When
// the preference is System (or unset) the frame keeps the system theme. The
// stored value stays a stable English token ("System"/"Light"/"Dark") so an
// existing config keeps working; only its display label is localized.
func applyAppearance(c *ui.Context, cfg *configStore) {
	// Resolve the appearance: an explicit Light/Dark choice wins; otherwise
	// follow the desktop's current appearance, read from the theme the frame
	// started with.
	dark := false
	if th := c.Theme(); th != nil && th.Dark {
		dark = true
	}
	switch cfg.appearance() {
	case "Light":
		dark = false
	case "Dark":
		dark = true
	}
	p := uikit.PaletteFor(&ui.Theme{Dark: dark})
	c.SetTheme(uikit.Theme(p, dark))
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
	p := uikit.PaletteFor(t)
	ui.Column(c).Gap(10).MinWidth(520).Children(func() {
		ui.Text(c, m.SettingsTitle).FontSize(17).FontWeight(700).TextColor(p.Text)

		settingsGroup(c, p, m.AppearanceLabel, func() {
			choice := appearanceDisplay(m, cfg.appearance())
			if ui.Select(c, &choice, appearanceChoices(m)).Width(180).Changed() {
				cfg.setAppearance(appearanceCanonical(m, choice))
				if setStatus != nil {
					setStatus(translate(m.AppearanceStatus, appearanceDisplay(m, cfg.appearance())))
				}
			}
		})

		settingsGroup(c, p, m.LanguageLabel, func() {
			choice := languageLabel(cfg.language())
			if ui.Select(c, &choice, []string{languageLabelChinese, languageLabelEnglish}).Width(180).Changed() {
				cfg.setLanguage(languageFromLabel(choice))
				if setStatus != nil {
					langMsgs := messagesFor(cfg.language())
					setStatus(translate(langMsgs.LanguageStatus, languageLabel(cfg.language())))
				}
			}
		})

		settingsGroup(c, p, m.StartupLabel, func() {
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

		ui.Box(c).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Background(p.Sidebar).Padding(4, 12).FillWidth().Children(func() {
			ui.Column(c).Gap(2).Children(func() {
				infoRow := func(k, v string) {
					ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
						ui.Text(c, k).Width(120).FontSize(12).TextColor(p.Muted).SingleLine()
						ui.Text(c, v).FontSize(12).SingleLine().Grow(1).TextColor(p.Text)
					})
				}
				infoRow(m.VersionLabel, appVersion)
				infoRow(m.PlatformLabel, runtime.GOOS+"/"+runtime.GOARCH)
				infoRow(m.GoLabel, runtime.Version())
				infoRow(m.ConfigFileLabel, configLocation(m, cfg))
				infoRow(m.RecentProjectsLabel, fmt.Sprintf("%d", len(cfg.recent())))
			})
		})

		ui.Text(c, m.SecretNote).FontSize(12).TextColor(p.Muted)

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if ui.PrimaryButton(c, m.Done).Clicked() {
				*open = false
			}
		})
	})
}

// settingsGroup draws one macOS-style settings group: a 12/600 muted title over
// a card face (radius 12, hairline edge) holding the row's control.
func settingsGroup(c *ui.Context, p uikit.Palette, title string, body func()) {
	ui.Column(c).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(12).FontWeight(600).TextColor(p.Muted).LetterSpacing(0.4)
		ui.Box(c).Radius(12).BorderWidth(1).BorderColor(p.Hairline).Background(p.Sidebar).
			Padding(8, 12).FillWidth().Children(body)
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
