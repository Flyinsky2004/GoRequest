package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// assertNoHorizontalOverflow renders nothing itself; it fails if any laid-out
// text extends past the left or right window edge. Widths are DIPs at scale 1.
func assertNoHorizontalOverflow(t *testing.T, tt *ui.Tester, width int) {
	t.Helper()
	seen := make(map[string]bool)
	for _, s := range tt.Texts() {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		r, ok := tt.Find(s)
		if !ok {
			continue
		}
		if r.X < -0.5 || r.X+r.W > float32(width)+0.5 {
			t.Errorf("text %q overflows horizontally: X=%.1f W=%.1f window=%d", s, r.X, r.W, width)
		}
	}
}

// Each category's content builds head-lessly at narrow widths without a panic
// and without horizontal overflow. This exercises the right pane in isolation.
func TestSettingsCategoryContentNarrow(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		for _, width := range []int{340, 420, 520} {
			for cat := 0; cat < int(settingsCategoryCount); cat++ {
				lang, width, cat := lang, width, cat
				t.Run(fmt.Sprintf("%s/w%d/cat%d", string(lang), width, cat), func(t *testing.T) {
					m, _ := newTestManager(t)
					m.cfg.cfg.Language = string(lang)
					// Keep the About pane's config line short so the fit check
					// measures layout, not an unusually long temp path.
					m.cfg.path = ""
					cfg, prefs := m.cfg, m.workspacePrefs()
					msgs := messagesFor(cfg.language())
					catv := settingsCategory(cat)
					tt := ui.NewTester(func(c *ui.Context) {
						p := uikit.PaletteFor(c.Theme())
						ui.Column(c).Width(float32(width)).Children(func() {
							settingsCategoryContent(c, p, msgs, catv, cfg, prefs, func(string) {})
						})
					}, width, 900)
					assertNoHorizontalOverflow(t, tt, width)
				})
			}
		}
	}
}

// The full modal builds at narrow widths, shows the Appearance pane by default,
// and switches to the About pane after the rail's About item is clicked. The
// switch keeps everything within the window.
func TestSettingsBodyRailSwitchesCategory(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		lang := lang
		t.Run(string(lang), func(t *testing.T) {
			m, _ := newTestManager(t)
			m.cfg.cfg.Language = string(lang)
			m.cfg.path = ""
			cfg, prefs := m.cfg, m.workspacePrefs()
			msgs := messagesFor(cfg.language())
			open := true
			width := 540
			tt := ui.NewTester(func(c *ui.Context) {
				renderSettings(c, c.Theme(), cfg, prefs, &open, func(string) {})
			}, width, 640)

			if !tt.HasText(msgs.AppearanceSystem) {
				t.Fatalf("default Appearance pane not shown: %v", tt.Texts())
			}
			assertNoHorizontalOverflow(t, tt, width)

			aboutLabel := settingsCategoryTitles(cfg.language())[int(settingsCategoryAbout)]
			if err := tt.Click(aboutLabel); err != nil {
				t.Fatalf("click rail %q: %v", aboutLabel, err)
			}
			tt.Frame()
			if tt.HasText(msgs.AppearanceSystem) {
				t.Fatalf("Appearance pane still shown after switching to About")
			}
			if !tt.HasText(msgs.VersionLabel) {
				t.Fatalf("About pane not shown after switch: %v", tt.Texts())
			}
			assertNoHorizontalOverflow(t, tt, width)
		})
	}
}

// The relocated Startup checkbox still reads and writes the stored preference,
// preserving the original behaviour despite the new layout.
func TestSettingsStartupToggleWritesConfig(t *testing.T) {
	m, _ := newTestManager(t)
	cfg, prefs := m.cfg, m.workspacePrefs()
	msgs := messagesFor(cfg.language())
	before := cfg.restoreLastEnabled()
	width := 420
	tt := ui.NewTester(func(c *ui.Context) {
		p := uikit.PaletteFor(c.Theme())
		ui.Column(c).Width(float32(width)).Children(func() {
			settingsCategoryContent(c, p, msgs, settingsCategoryStartup, cfg, prefs, func(string) {})
		})
	}, width, 400)
	if err := tt.Click(msgs.RestoreLast); err != nil {
		t.Fatalf("click startup checkbox: %v", err)
	}
	if got := cfg.restoreLastEnabled(); got == before {
		t.Fatalf("startup preference unchanged after click: before=%v after=%v", before, got)
	}
}
