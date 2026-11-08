package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// TestSettingsIconsParse proves every settings icon is valid SVG at startup and
// that the category table has exactly one entry per category. A nil icon would
// silently disappear from the sheet, so this is the first regression guard.
func TestSettingsIconsParse(t *testing.T) {
	icons := settingsCategoryIcons()
	if len(icons) != int(settingsCategoryCount) {
		t.Fatalf("category icons = %d, want %d", len(icons), settingsCategoryCount)
	}
	for i, ic := range icons {
		if ic == nil {
			t.Fatalf("category icon %d (%v) failed to parse", i, settingsCategory(i))
		}
	}
	if settingsIconDone == nil {
		t.Fatal("Done icon failed to parse")
	}
	if settingsIconReset == nil {
		t.Fatal("Reset icon failed to parse")
	}

	want := map[settingsCategory]*ui.SVG{
		settingsCategoryAppearance: settingsIconAppearance,
		settingsCategoryLanguage:   settingsIconLanguage,
		settingsCategoryStartup:    settingsIconStartup,
		settingsCategoryWorkspace:  settingsIconWorkspace,
		settingsCategoryMarkdown:   settingsIconMarkdown,
		settingsCategoryHistory:    settingsIconHistory,
		settingsCategoryAbout:      settingsIconAbout,
	}
	for cat, ic := range want {
		if got := settingsCategoryIcon(cat); got != ic {
			t.Errorf("settingsCategoryIcon(%v) = %p, want %p", cat, got, ic)
		}
	}
	if settingsCategoryIcon(settingsCategoryCount) != nil {
		t.Error("out-of-range category returned an icon")
	}
	if settingsCategoryIcon(settingsCategory(-1)) != nil {
		t.Error("negative category returned an icon")
	}
}

// TestSettingsRailKeyboardFocusAndActivation keeps the rail's accessibility
// contract: every category is a focusable ButtonBase reached with Tab, and
// Space chooses the focused category. The visible label and the accessible
// name stay the same text.
func TestSettingsRailKeyboardFocusAndActivation(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		lang := lang
		t.Run(string(lang), func(t *testing.T) {
			tt, cfg, _ := settingsSheetFixture(t, lang, 760, 560)
			titles := settingsCategoryTitles(cfg.language())
			msgs := messagesFor(cfg.language())

			// The sheet opens with the first category focused; Tab moves along
			// the rail, and Space chooses the focused category.
			if !tt.Focused(titles[0]) {
				t.Fatalf("sheet did not focus the first category %q", titles[0])
			}
			tt.Key(0, ui.KeyTab)
			tt.Frame()
			if !tt.Focused(titles[1]) {
				t.Fatalf("Tab did not move focus to category %q", titles[1])
			}

			tt.Key(0, ui.KeySpace)
			tt.Frame()
			if !tt.HasText(msgs.LanguageLabel) {
				t.Fatalf("Space on %q did not open the Language pane: %v", titles[1], tt.Texts())
			}
			if !tt.Focused(titles[1]) {
				t.Fatalf("choosing %q lost the keyboard focus", titles[1])
			}
		})
	}
}

// TestSettingsCategoriesRenderWithIconsInBothThemes checks the sheet still
// exposes every category and Done with the icons wired in, in light and dark
// themes. Icon colors come from the palette, so this also proves the build does
// not depend on one theme.
func TestSettingsCategoriesRenderWithIconsInBothThemes(t *testing.T) {
	for _, dark := range []bool{false, true} {
		dark := dark
		t.Run(fmt.Sprintf("dark=%v", dark), func(t *testing.T) {
			tt, cfg, _ := settingsSheetFixture(t, langEnglish, 760, 560)
			tt.SetDark(dark)
			tt.Frame()
			for _, title := range settingsCategoryTitles(cfg.language()) {
				if _, ok := tt.Find(title); !ok {
					t.Fatalf("category %q missing in dark=%v", title, dark)
				}
			}
			done, ok := tt.Find(messagesFor(cfg.language()).Done)
			if !ok || done.H <= 0 || done.W <= 0 {
				t.Fatalf("Done missing or invisible in dark=%v: %+v", dark, done)
			}
		})
	}
}

// TestSettingsDoneStaysVisibleWithIcons is the narrow-window guard: the icons
// added to the categories and to Done must not push Done out of the sheet for
// either language, at narrow widths, or with enlarged text. It also clicks Done
// to prove the new icon+text button still dismisses the sheet.
func TestSettingsDoneStaysVisibleWithIcons(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		for _, tc := range []struct {
			name     string
			w, h     int
			fontSize float32
		}{
			{"narrow/360", 360, 320, 0},
			{"narrow/420", 420, 360, 0},
			{"welcome/560", 560, 360, 0},
			{"bigfont/420", 420, 360, 26},
			{"bigfont/560", 560, 360, 26},
		} {
			lang, tc := lang, tc
			t.Run(fmt.Sprintf("%s/%s", lang, tc.name), func(t *testing.T) {
				m, _ := newTestManager(t)
				m.cfg.cfg.Language = string(lang)
				m.cfg.path = ""
				cfg, prefs := m.cfg, m.workspacePrefs()
				msgs := messagesFor(cfg.language())
				open := true
				tt := ui.NewTester(func(c *ui.Context) {
					th := c.Theme()
					if tc.fontSize > 0 {
						copy := *th
						copy.FontSize = tc.fontSize
						c.SetTheme(&copy)
						th = &copy
					}
					renderSettings(c, th, cfg, prefs, &open, func(string) {})
				}, tc.w, tc.h)

				done, ok := tt.Find(msgs.Done)
				if !ok {
					t.Fatalf("Done missing; texts=%v", tt.Texts())
				}
				if done.H <= 0 || done.W <= 0 {
					t.Fatalf("Done invisible: %+v", done)
				}
				if done.Y < -0.5 || done.Y+done.H > float32(tc.h)+0.5 {
					t.Fatalf("Done outside window: %+v window=%dx%d", done, tc.w, tc.h)
				}
				if err := tt.Click(msgs.Done); err != nil {
					t.Fatalf("Done not clickable: %v", err)
				}
				if open {
					t.Fatal("Done did not dismiss the sheet")
				}
			})
		}
	}
}

// TestMarkdownResetKeepsIconAndAccessibleName keeps the Markdown reset action
// reachable by its visible label after gaining an icon, and proves it still
// restores the defaults.
func TestMarkdownResetKeepsIconAndAccessibleName(t *testing.T) {
	s := newWorkspacePrefsStore("")
	changed := 0
	s.markdownChanged = func() { changed++ }
	m := markdownSettingsMessagesFor(langEnglish)
	tt := ui.NewTester(func(c *ui.Context) {
		s.markdownSettingsGroup(c, uikit.PaletteFor(c.Theme()), langEnglish, nil)
	}, 520, 360)
	tt.Frame()

	// Change a value first so Reset has something real to restore.
	if err := tt.Click("100%"); err != nil {
		t.Fatalf("open font choices: %v", err)
	}
	if err := tt.Click("125%"); err != nil {
		t.Fatalf("choose 125%%: %v", err)
	}
	tt.Frame()
	if got := s.markdownPolicy().FontPercent; got != 125 {
		t.Fatalf("precondition: font percent = %d, want 125", got)
	}

	if _, ok := tt.Find(m.Reset); !ok {
		t.Fatalf("Reset action missing; texts=%v", tt.Texts())
	}
	if err := tt.Click(m.Reset); err != nil {
		t.Fatalf("click Reset: %v", err)
	}
	tt.Frame()
	if got, want := s.markdownPolicy(), defaultMarkdownPreferences(); got != want {
		t.Fatalf("Reset did not restore defaults: got %+v want %+v", got, want)
	}
	if changed == 0 {
		t.Fatal("Reset did not notify the preview owner")
	}
}
