//go:build ui04_integration

package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// Apply docs/task-results/UI04-integration.patch, then run:
// go test -tags ui04_integration ./internal/desktop -run TestWindowDrag -v
func TestWindowDragWiredViews(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "中文项目")); err != nil {
		t.Fatal(err)
	}
	a := m.workspaces[0]
	tt := ui.NewTester(a.View, 1200, 820)
	if !windowDragTarget(tt, 200, 10) {
		t.Fatal("brand drag registration missing; apply UI04-integration.patch")
	}
	for _, width := range []int{1024, 1200, 1600} {
		for _, scale := range []float32{1, 2} {
			tt.SetSize(width, 820)
			tt.SetScale(scale)
			tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 78})
			tt.Frame()
			if !windowDragTarget(tt, 200, 5) {
				t.Fatal("brand blank does not drag")
			}
			if windowDragTarget(tt, 20, 15) {
				t.Fatal("traffic lights covered")
			}
			for _, label := range []string{"中文项目", "New Request", a.ps.envSel, "Settings"} {
				if windowDragAtLabel(t, tt, label) {
					t.Fatalf("control %q drags", label)
				}
			}
			if err := tt.Click("中文项目"); err != nil {
				t.Fatal(err)
			}
			if len(tt.Menu()) == 0 {
				t.Fatal("project menu no longer opens")
			}
			tt.CloseMenu()
			project, _ := tt.Find("中文项目")
			env, _ := tt.Find("Env")
			x := (project.X + project.W + env.X) / 2
			if !windowDragTarget(tt, x, 2) || !windowDragTarget(tt, x, 43) {
				t.Fatal("toolbar padding does not drag")
			}
			a.sidebarHidden = true
			tt.Frame()
			if windowDragTarget(tt, 20, 15) {
				t.Fatal("hidden-sidebar title controls covered")
			}
			a.sidebarHidden = false
			tt.Frame()
		}
	}
	m.showWelcome("")
	welcome := ui.NewTester(m.welcome.view, 560, 360)
	if !windowDragTarget(welcome, 400, 8) {
		t.Fatal("welcome brand blank does not drag")
	}
	for _, label := range []string{"Open Project…", "Settings"} {
		if windowDragAtLabel(t, welcome, label) {
			t.Fatalf("welcome %q drags", label)
		}
	}
	if err := welcome.Click("Settings"); err != nil {
		t.Fatal(err)
	}
	if !m.welcome.showSettings {
		t.Fatal("welcome settings did not open")
	}
	if windowDragTarget(welcome, 400, 8) {
		t.Fatal("modal did not block background drag")
	}
}

func TestWindowDragPreservesTabsAndSplitter(t *testing.T) {
	a, ps := openTestProject(t, "拖窗回归")
	tabs := openRequestTabs(t, ps, "甲", "乙")
	tt := ui.NewTester(a.View, 1200, 820)
	tt.SetTitleBar(ui.TitleBar{Height: 28, Left: 78})
	tt.Frame()
	for _, tab := range tabs {
		for _, label := range []string{tabDisplayLabel(tab), translate(a.msg().CloseTabTip, tabIdentity(tab))} {
			if windowDragAtLabel(t, tt, label) {
				t.Fatalf("tab control %q routes to window drag", label)
			}
		}
	}
	if err := tt.Click(tabDisplayLabel(tabs[0])); err != nil {
		t.Fatal(err)
	}
	if ps.activeTab() != tabs[0] {
		t.Fatal("tab selection lost")
	}
	closeLabel := translate(a.msg().CloseTabTip, tabIdentity(tabs[1]))
	if err := tt.Click(closeLabel); err != nil {
		t.Fatal(err)
	}
	if len(ps.tabs) != 1 {
		t.Fatal("tab close lost")
	}
	before := a.navWidth
	if windowDragTarget(tt, before, 240) {
		t.Fatal("splitter routes to window drag")
	}
	tt.Press(before, 240)
	tt.Move(before+24, 240)
	tt.Release(before+24, 240)
	if a.navWidth <= before {
		t.Fatal("sidebar splitter no longer resizes")
	}
	doubleClickTab(t, tt, tabs[0])
	if !tabs[0].renaming {
		t.Fatal("tab double-click rename lost")
	}
	tt.Key(0, ui.KeyEscape)
	if tabs[0].renaming {
		t.Fatal("rename cancel lost")
	}
}
