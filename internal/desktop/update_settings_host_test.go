package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// An already-open settings sheet in another window may render before the
// window selected by the update dialog. It must keep its own selected page.
func TestUpdateAboutNavigationStaysWithSelectedWindow(t *testing.T) {
	for _, project := range []bool{false, true} {
		name := "welcome"
		if project {
			name = "project"
		}
		t.Run(name, func(t *testing.T) {
			installed := "0.0.1-dev"
			clock := fixedClock()
			m, u, _ := newUpdateTestManager(t, &fakeUpdateChecker{}, &fakeUpdateDownloader{}, clock.Now, &installed, func(string) error { return nil })
			var target *ui.Tester
			var host window
			if project {
				if err := m.openProject(writeProject(t, "AboutHost")); err != nil {
					t.Fatal(err)
				}
				a := m.workspaces[0]
				host = a.win
				target = ui.NewTester(a.View, 960, 720)
			} else {
				m.showWelcome("")
				host = m.welcome.win
				target = ui.NewTester(m.welcome.view, 720, 640)
			}
			otherHost := &fakeWindow{shown: true}
			open := true
			other := ui.NewTester(func(c *ui.Context) {
				applyAppearance(c, m.cfg)
				renderSettingsWithUpdates(c, c.Theme(), m.cfg, m.workspacePrefs(), u, otherHost, &open, func(string) {})
			}, 720, 640)
			other.Frame()
			u.openSettings()
			if u.aboutHost != host {
				t.Fatal("About request did not target the selected window")
			}
			other.Frame()
			if other.HasText(aboutContactEmail) || u.aboutHost != host {
				t.Fatal("another settings window consumed the About request")
			}
			target.Frame()
			target.Frame()
			if !target.HasText(aboutContactEmail) || !target.HasText(updateMessagesEn.CheckNow) {
				t.Fatalf("target did not open About with updates: %v", target.Texts())
			}
			if u.aboutHost != nil {
				t.Fatal("target did not consume its About request")
			}
			other.Frame()
			if other.HasText(aboutContactEmail) {
				t.Fatal("other window's selected settings page changed")
			}
		})
	}
}
