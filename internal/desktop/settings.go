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
func renderSettings(c *ui.Context, t *ui.Theme, cfg *configStore, prefs *workspacePrefsStore, open *bool, setStatus func(string)) {
	if !*open {
		return
	}
	ui.Modal(c, open, func() {
		settingsBody(c, t, cfg, prefs, open, setStatus)
	})
}

// settingsSelectionKey identifies each window's chosen settings category so the
// left-rail choice persists across frames without widening renderSettings' or
// settingsBody's signature.
type settingsSelectionKey struct{}

// settingsBody draws the two-pane settings sheet (FIX05): a left rail of
// categories and a right pane showing only the chosen category's settings. The
// pane sizing is derived from the window so a narrow window shrinks instead of
// overflowing horizontally; the rail and content row are clipped as a hard
// guarantee.
func settingsBody(c *ui.Context, t *ui.Theme, cfg *configStore, prefs *workspacePrefsStore, open *bool, setStatus func(string)) {
	m := messagesFor(cfg.language())
	p := uikit.PaletteFor(t)
	titles := settingsCategoryTitles(cfg.language())

	// The modal panel is capped at window-40 with 20 DIP padding per side, so a
	// window-80 content width always fits inside it. A small floor keeps the
	// controls usable on very small windows; clipping prevents any spill.
	winW, winH := c.Size()
	width := winW - 80
	if width > 640 {
		width = 640
	}
	if width < 200 {
		width = 200
	}
	height := winH - 80
	if height < 240 {
		height = 240
	}
	leftW := width * 0.30
	if leftW < 104 {
		leftW = 104
	}
	if leftW > 168 {
		leftW = 168
	}
	rowH := height - 92
	if rowH < 160 {
		rowH = 160
	}

	root := ui.Column(c).Gap(12).Width(width)
	selected := ui.Local(root, settingsSelectionKey{}, func() int { return int(settingsCategoryAppearance) })
	root.Children(func() {
		ui.Text(c, m.SettingsTitle).FontSize(17).FontWeight(700).TextColor(p.Text)

		ui.Row(c).Gap(12).FillWidth().Height(rowH).Clip().Children(func() {
			settingsCategoryRail(c, p, titles, selected, leftW)
			ui.Scroll(c).Grow(1).FillHeight().Children(func() {
				ui.Column(c).Gap(10).FillWidth().Padding(0, 2).Children(func() {
					settingsCategoryContent(c, p, m, settingsCategory(*selected), cfg, prefs, setStatus)
				})
			})
		})

		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			if uikit.ActionCursor(ui.PrimaryButton(c, m.Done)).Clicked() {
				*open = false
			}
		})
	})
}

// settingsCategoryRail draws the left category list. Each row is a clickable,
// left-aligned label: the chosen one is filled with the accent color, a hovered
// one with the hover wash. Selection is written back through *selected.
func settingsCategoryRail(c *ui.Context, p uikit.Palette, titles []string, selected *int, width float32) {
	rail := ui.Column(c).Width(width).FillHeight().Radius(10).Background(p.Sidebar).
		BorderWidth(1).BorderColor(p.Hairline).Padding(6).Gap(2).Clip()
	rail.Children(func() {
		for i, title := range titles {
			i := i
			row := uikit.ActionCursor(ui.Row(c).FillWidth().Height(30).Radius(7).
				AlignItems(ui.Center).Padding(0, 10))
			bg := ui.Transparent
			textColor := p.Text
			switch {
			case i == *selected:
				bg = p.Accent
				textColor = p.OnAccent
			case row.Hovered():
				bg = p.Hover
			}
			row.Background(bg)
			row.Children(func() {
				ui.Text(c, title).FontSize(13).TextColor(textColor).SingleLine()
			})
			if row.Clicked() {
				*selected = i
			}
		}
	})
}

// settingsCategoryContent draws the settings belonging to one category. It
// preserves every control's original read/write behaviour and status feedback;
// only the surrounding layout changed.
func settingsCategoryContent(c *ui.Context, p uikit.Palette, m *messages, cat settingsCategory, cfg *configStore, prefs *workspacePrefsStore, setStatus func(string)) {
	switch cat {
	case settingsCategoryAppearance:
		settingsGroup(c, p, m.AppearanceLabel, func() {
			choice := appearanceDisplay(m, cfg.appearance())
			if uikit.ActionCursor(ui.Select(c, &choice, appearanceChoices(m))).Width(150).Changed() {
				cfg.setAppearance(appearanceCanonical(m, choice))
				if setStatus != nil {
					setStatus(translate(m.AppearanceStatus, appearanceDisplay(m, cfg.appearance())))
				}
			}
		})

	case settingsCategoryLanguage:
		settingsGroup(c, p, m.LanguageLabel, func() {
			choice := languageLabel(cfg.language())
			if uikit.ActionCursor(ui.Select(c, &choice, []string{languageLabelChinese, languageLabelEnglish})).Width(150).Changed() {
				cfg.setLanguage(languageFromLabel(choice))
				if setStatus != nil {
					langMsgs := messagesFor(cfg.language())
					setStatus(translate(langMsgs.LanguageStatus, languageLabel(cfg.language())))
				}
			}
		})

	case settingsCategoryStartup:
		settingsGroup(c, p, m.StartupLabel, func() {
			restore := cfg.restoreLastEnabled()
			if uikit.ActionCursor(ui.Checkbox(c, &restore, m.RestoreLast)).Changed() {
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

	case settingsCategoryWorkspace:
		if prefs != nil {
			prefs.settingsGroups(c, p, m, setStatus)
		}

	case settingsCategoryMarkdown:
		if prefs != nil {
			prefs.markdownSettingsGroup(c, p, cfg.language(), setStatus)
		}

	case settingsCategoryHistory:
		settingsGroup(c, p, m.HistorySettingsLabel, func() {
			ui.Column(c).Gap(8).Children(func() {
				snap := cfg.historySaveSnapshots()
				if uikit.ActionCursor(ui.Checkbox(c, &snap, m.HistorySaveSnapshots)).Changed() {
					cfg.setHistorySaveSnapshots(snap)
					if !snap {
						// Bodies cannot be captured when snapshots are off.
						cfg.setHistorySaveBodies(false)
					}
				}
				bodies := cfg.historySaveBodies()
				cb := uikit.ActionCursor(ui.Checkbox(c, &bodies, m.HistorySaveBodies).Disabled(!snap))
				if snap && cb.Changed() {
					cfg.setHistorySaveBodies(bodies)
				}
				ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
					ui.Text(c, m.HistoryRetentionLabel).Grow(1).FontSize(12).TextColor(p.Muted).SingleLine()
					choices := historyRetentionChoices(m)
					choice := historyRetentionLabel(m, cfg.historyRetentionDays())
					if uikit.ActionCursor(ui.Select(c, &choice, choices)).Width(150).Changed() {
						cfg.setHistoryRetentionDays(historyRetentionValue(choices, choice))
					}
				})
				ui.Text(c, m.HistorySettingsNote).FontSize(11).TextColor(p.Faint)
			})
		})

	case settingsCategoryAbout:
		settingsAbout(c, p, m, cfg)
	}
}

// settingsAbout draws real application information (no configurable controls),
// matching the original version block.
func settingsAbout(c *ui.Context, p uikit.Palette, m *messages, cfg *configStore) {
	titles := settingsCategoryTitles(cfg.language())
	ui.Column(c).Gap(10).FillWidth().Children(func() {
		ui.Text(c, titles[int(settingsCategoryAbout)]).FontSize(12).FontWeight(600).TextColor(p.Muted).LetterSpacing(0.4)
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

// historyRetentionChoices returns the localized retention options in display
// order; historyRetentionValues is the parallel canonical value list.
func historyRetentionChoices(m *messages) []string {
	return []string{m.HistoryRetention30, m.HistoryRetention90, m.HistoryRetention365, m.HistoryRetentionForever}
}

func historyRetentionValues() []int { return []int{30, 90, 365, -1} }

// historyRetentionLabel maps the stored days value to its localized label,
// falling back to the 90-day label for an unknown value.
func historyRetentionLabel(m *messages, days int) string {
	for i, v := range historyRetentionValues() {
		if v == days {
			return historyRetentionChoices(m)[i]
		}
	}
	return m.HistoryRetention90
}

// historyRetentionValue maps a chosen label back to its canonical days value.
func historyRetentionValue(choices []string, choice string) int {
	values := historyRetentionValues()
	for i, label := range choices {
		if label == choice {
			return values[i]
		}
	}
	return 90
}
