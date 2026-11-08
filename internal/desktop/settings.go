package desktop

import (
	"fmt"
	"runtime"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

const appVersion = "0.1.0 (MVP)"

// appearanceDark resolves whether the frame should use the dark palette. An
// explicit Light/Dark choice wins; otherwise it follows the desktop's current
// appearance, read from the theme the frame started with.
func appearanceDark(c *ui.Context, cfg *configStore) bool {
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
	return dark
}

// applyAppearance applies the stored appearance preference for the frame. When
// the preference is System (or unset) the frame keeps the system theme. The
// stored value stays a stable English token ("System"/"Light"/"Dark") so an
// existing config keeps working; only its display label is localized.
func applyAppearance(c *ui.Context, cfg *configStore) {
	dark := appearanceDark(c, cfg)
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

const (
	// settingsWindowInset is the room ui.Modal reserves around the sheet's
	// content in both directions (MyGo v0.7.4 coreModal): 40 DIPs of outer
	// margin plus 20 DIPs of panel padding on each side.
	settingsWindowInset = 80
	// settingsModalChrome is the modal panel's own vertical padding (20 DIPs
	// on each side), added on top of the content height below.
	settingsModalChrome = 40
	// settingsMaxContentHeight caps the sheet at 380 content DIPs, i.e. a
	// 420 DIP panel. FIX05 reserved winH-80 DIPs for the whole sheet, so a
	// 1200×820 project window showed a ~780 DIP panel that nearly filled the
	// window. The new panel is ~54% of that while staying comfortably usable.
	settingsMaxContentHeight = 380
	// settingsMinContentHeight is the preferred floor (280 DIP panel). Even at
	// this height both panes scroll rather than squash their controls.
	settingsMinContentHeight = 240
	// settingsTargetPanelFraction aims the panel at 55% of the window height;
	// the absolute cap wins on tall windows and the floor on short ones.
	settingsTargetPanelFraction = 0.55
	settingsMaxSheetWidth       = 640
	settingsMinSheetWidth       = 200
	settingsRailMinWidth        = 120
	settingsRailMaxWidth        = 180
	settingsRailWidthFraction   = 0.34
)

// settingsGeometry is the sheet's size for one window. It is a pure function
// of the window size: the height never depends on the selected category or on
// how tall its content is, so switching categories cannot make the sheet jump.
type settingsGeometry struct {
	width  float32
	height float32
	leftW  float32
}

func settingsGeometryFor(winW, winH float32) settingsGeometry {
	width := winW - settingsWindowInset
	if width > settingsMaxSheetWidth {
		width = settingsMaxSheetWidth
	}
	if width < settingsMinSheetWidth {
		width = settingsMinSheetWidth
	}
	leftW := width * settingsRailWidthFraction
	if leftW < settingsRailMinWidth {
		leftW = settingsRailMinWidth
	}
	if leftW > settingsRailMaxWidth {
		leftW = settingsRailMaxWidth
	}

	// Available content height inside the modal panel.
	room := winH - settingsWindowInset
	// Preferred panel: a fraction of the window for small windows (so the
	// sheet never dominates a 480 DIP welcome window), capped at an absolute
	// maximum for tall windows (the FIX06 "about half" request).
	height := winH*settingsTargetPanelFraction - settingsModalChrome
	if height > settingsMaxContentHeight {
		height = settingsMaxContentHeight
	}
	if height < settingsMinContentHeight {
		height = settingsMinContentHeight
	}
	// A window shorter than the preferred floor must still contain the sheet;
	// never let a hard minimum push Done out of the window.
	if room > 0 && height > room {
		height = room
	}
	return settingsGeometry{width: width, height: height, leftW: leftW}
}

// settingsBody draws the two-pane settings sheet (FIX05): a left rail of
// categories and a right pane showing only the chosen category's settings.
// FIX06 gives the root column a fixed, capped height derived only from the
// window. The two-pane row takes the remaining space through Grow, so a
// larger title or Done button (theme / enlarged text) shrinks the scrollable
// panes instead of pushing Done out of the window. Both panes scroll
// independently and Done lives outside them, always visible.
func settingsBody(c *ui.Context, t *ui.Theme, cfg *configStore, prefs *workspacePrefsStore, open *bool, setStatus func(string)) {
	m := messagesFor(cfg.language())
	p := uikit.PaletteFor(t)
	titles := settingsCategoryTitles(cfg.language())

	winW, winH := c.Size()
	g := settingsGeometryFor(winW, winH)

	root := ui.Column(c).Gap(12).Width(g.width).Height(g.height)
	selected := ui.Local(root, settingsSelectionKey{}, func() int { return int(settingsCategoryAppearance) })
	root.Children(func() {
		ui.Text(c, m.SettingsTitle).FontSize(17).FontWeight(700).TextColor(p.Text).Shrink(0)

		ui.Row(c).Gap(12).FillWidth().Grow(1).Clip().Children(func() {
			settingsCategoryRail(c, p, titles, selected, g.leftW)
			ui.Scroll(c).Grow(1).FillHeight().Children(func() {
				ui.Column(c).Gap(10).FillWidth().Padding(0, 2).Children(func() {
					settingsCategoryContent(c, p, m, settingsCategory(*selected), cfg, prefs, setStatus)
				})
			})
		})

		ui.Row(c).Gap(8).Justify(ui.End).Shrink(0).Children(func() {
			done := uikit.ActionCursor(ui.PrimaryButton(c, ""))
			done.Label(m.Done)
			done.Children(func() {
				if settingsIconDone != nil {
					ui.Icon(c, settingsIconDone).FontSize(14).TextColor(p.OnAccent).Shrink(0)
				}
				ui.Text(c, m.Done).TextColor(p.OnAccent).SingleLine()
			})
			if done.Clicked() {
				*open = false
			}
		})
	})
}

// settingsCategoryRail draws the left category list inside a scrollable card.
// The card keeps its FIX05 look; only its list scrolls, so a long category
// list (or a short window / larger text) keeps every category reachable
// instead of clipping the ones past the bottom. Each row is a focusable
// ButtonBase, so Tab reaches the categories and Enter/Space chooses one; its
// icon and label share the selected, hover and pressed colors.
func settingsCategoryRail(c *ui.Context, p uikit.Palette, titles []string, selected *int, width float32) {
	rail := ui.Column(c).Width(width).FillHeight().Radius(10).Background(p.Sidebar).
		BorderWidth(1).BorderColor(p.Hairline).Padding(6).Clip()
	rail.Children(func() {
		ui.Scroll(c).Grow(1).FillWidth().Children(func() {
			ui.Column(c).Gap(2).FillWidth().Children(func() {
				for i, title := range titles {
					i := i
					row := uikit.ActionCursor(ui.ButtonBase(c).FillWidth().Height(30).Radius(7).
						AlignItems(ui.Center).Padding(0, 8).Gap(7))
					row.Label(title)
					bg := ui.Transparent
					textColor := p.Text
					iconColor := p.Muted
					switch {
					case i == *selected:
						bg = p.Accent
						textColor = p.OnAccent
						iconColor = p.OnAccent
					case row.Pressed():
						bg = p.Press
						iconColor = p.Text
					case row.Hovered():
						bg = p.Hover
						iconColor = p.Text
					}
					row.Background(bg)
					row.Children(func() {
						if ic := settingsCategoryIcon(settingsCategory(i)); ic != nil {
							ui.Icon(c, ic).FontSize(14).TextColor(iconColor).Shrink(0)
						}
						ui.Text(c, title).FontSize(13).TextColor(textColor).SingleLine().Grow(1)
					})
					if row.Clicked() {
						*selected = i
					}
				}
			})
		})
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
			if uikit.ActionCursor(uikit.Select(c, &choice, appearanceChoices(m), uikit.IconMap(map[string]*ui.SVG{
				m.AppearanceSystem: uikit.IconMonitor,
				m.AppearanceLight:  uikit.IconSun,
				m.AppearanceDark:   uikit.IconMoon,
			}))).Width(150).Changed() {
				cfg.setAppearance(appearanceCanonical(m, choice))
				if setStatus != nil {
					setStatus(translate(m.AppearanceStatus, appearanceDisplay(m, cfg.appearance())))
				}
			}
		})

	case settingsCategoryLanguage:
		settingsGroup(c, p, m.LanguageLabel, func() {
			choice := languageLabel(cfg.language())
			if uikit.ActionCursor(uikit.Select(c, &choice, []string{languageLabelChinese, languageLabelEnglish}, uikit.SameIcon(uikit.IconGlobe))).Width(150).Changed() {
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
					if uikit.ActionCursor(uikit.Select(c, &choice, choices, uikit.SameIcon(uikit.IconClock))).Width(150).Changed() {
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
