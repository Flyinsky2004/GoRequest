package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestSettingsCategoriesHaveNoUpdatesEntry(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		titles := settingsCategoryTitles(lang)
		if len(titles) != int(settingsCategoryCount) {
			t.Fatalf("%s titles = %d, want %d", lang, len(titles), settingsCategoryCount)
		}
		for _, title := range titles {
			if title == "Updates" || title == "更新" {
				t.Fatalf("%s rail still has standalone update category %q", lang, title)
			}
		}
	}
}

func TestAboutShowsDeveloperAndUpdateControls(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		lang := lang
		t.Run(string(lang), func(t *testing.T) {
			installed := "0.0.1-dev"
			clock := fixedClock()
			m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
			m.cfg.cfg.Language = string(lang)
			m.showWelcome("")
			tt := ui.NewTester(m.welcome.view, 720, 640)
			tt.Frame()
			ms := messagesFor(lang)
			if err := tt.Click(ms.SettingsTitle); err != nil {
				_ = err // title is not clickable; open via the real entry below
			}
			m.welcome.showSettings = true
			tt.Frame()
			if err := tt.Click(settingsCategoryTitles(lang)[settingsCategoryAbout]); err != nil {
				t.Fatalf("open About: %v", err)
			}
			tt.Frame()
			um := updateMessagesFor(lang)
			for _, want := range []string{aboutDeveloperName, aboutContactEmail, aboutProjectURL, um.AutoCheckLabel, um.NeverNotifyLabel, um.CheckNow, "0.0.1-dev"} {
				if !tt.HasText(want) {
					t.Fatalf("About missing %q; texts=%v", want, tt.Texts())
				}
			}
			_ = u
		})
	}
}

func TestUpdateDialogSettingsOpensAbout(t *testing.T) {
	installed := "0.0.1-dev"
	clock := fixedClock()
	m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
	m.showWelcome("")
	u.openSettings()
	tt := ui.NewTester(m.welcome.view, 720, 640)
	tt.Frame()
	tt.Frame()
	if !tt.HasText(updateMessagesEn.CheckNow) || !tt.HasText(aboutContactEmail) {
		t.Fatalf("Settings from update dialog did not land on About; texts=%v", tt.Texts())
	}
}
