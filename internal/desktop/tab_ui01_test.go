package desktop

import (
	"math"
	"testing"

	"github.com/egoist/mygo/ui"
)

// Exercise the production inline implementation as well as the uikit example.
func TestUI01InlineTabGeometryAndCloseProtection(t *testing.T) {
	a, ps := openTestProject(t, "UI01")
	tabs := openRequestTabs(t, ps, "中文名称", "English request", "Other")
	tabs[0].Dirty = true
	theme := ui.LightTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Fill().Children(func() {
			a.tabBar(c, theme, ps)
			ui.Text(c, "content boundary")
		})
	}, 760, 100)
	tt.Frame()
	for _, tab := range tabs {
		label, ok := tt.Find(tabDisplayLabel(tab))
		close, found := tt.Find(translate(a.msg().CloseTabTip, tabIdentity(tab)))
		if !ok || !found || close.W != 24 || close.H != 24 || label.Y < 12 ||
			math.Abs(float64(label.Y+label.H/2-close.Y-close.H/2)) > .5 {
			t.Fatalf("inline geometry: label=%+v close=%+v", label, close)
		}
		title, titleFound := tt.Find(tab.Title)
		if !titleFound || math.Abs(float64(title.Y+title.H/2-close.Y-close.H/2)) > .5 {
			t.Fatalf("text and close centers differ: title=%+v close=%+v", title, close)
		}
		boundary, _ := tt.Find("content boundary")
		if close.Y+close.H > boundary.Y {
			t.Fatalf("close overlaps content: %+v boundary=%+v", close, boundary)
		}
	}
	first := tabs[0]
	close, _ := tt.Find(translate(a.msg().CloseTabTip, tabIdentity(first)))
	tt.Move(close.X+12, close.Y+12)
	if tt.Cursor() != ui.CursorPointer {
		t.Fatal("production close lacks pointer")
	}
	// Closing an inactive dirty tab must invoke the existing protection flow,
	// never activate it or remove it immediately.
	active := ps.activeTab()
	tt.ClickAt(close.X+2, close.Y+2)
	if ps.indexOfTab(first) < 0 || a.closeFlow == nil || a.closeFlow.dirty != first || ps.activeTab() != active {
		t.Fatal("inline close bypassed dirty protection or activated the tab")
	}
}

func TestUI01RenameFieldKeepsTextCursorAndBounds(t *testing.T) {
	a, ps := openTestProject(t, "UI01Rename")
	tab := openRequestTabs(t, ps, "Rename me")[0]
	beginTabRename(ps, tab)
	theme := ui.LightTheme()
	tt := ui.NewTester(func(c *ui.Context) {
		c.SetTheme(theme)
		ui.Column(c).Fill().Children(func() {
			a.tabBar(c, theme, ps)
			ui.Text(c, "editor boundary")
		})
	}, 320, 100)
	tt.Frame()
	field, ok := tt.Find(a.msg().Rename)
	below, _ := tt.Find("editor boundary")
	if !ok || field.Y < 12 || field.Y+field.H > below.Y {
		t.Fatalf("rename field escaped strip: %+v below=%+v", field, below)
	}
	tt.Move(field.X+field.W/2, field.Y+field.H/2)
	if tt.Cursor() != ui.CursorText {
		t.Fatal("rename field lost text cursor")
	}
	tt.Key(0, ui.KeyEscape)
	if tab.renaming {
		t.Fatal("rename no longer cancels with Escape")
	}
}
