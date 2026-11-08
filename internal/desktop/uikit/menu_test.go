package uikit

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// TestMenuButtonDrawsInWindow checks the shared drop-down entry point: the
// menu is drawn inside the window (no platform menu), it keeps Checked,
// Disabled, separators and submenus, and choosing an item runs its action.
func TestMenuButtonDrawsInWindow(t *testing.T) {
	chosen := ""
	tt := ui.NewTester(func(c *ui.Context) {
		MenuButton(c, "Actions", func(m *ui.Menu) {
			if m.Item("Alpha").Checked(true).Chosen() {
				chosen = "Alpha"
			}
			m.Item("Beta").Disabled(true)
			m.Separator()
			m.Submenu("More", func(sm *ui.Menu) {
				if sm.Item("Gamma").Chosen() {
					chosen = "Gamma"
				}
			})
		}).Label("actions")
	}, 420, 340)
	tt.Frame()

	if err := tt.Click("actions"); err != nil {
		t.Fatalf("click menu trigger: %v", err)
	}
	tt.Frame()
	if got := tt.Menu(); got != nil {
		t.Fatalf("the system showed a native menu: %v", got)
	}
	for _, want := range []string{"Alpha", "Beta", "More"} {
		if !tt.HasText(want) {
			t.Fatalf("drawn menu missing %q; texts=%v", want, tt.Texts())
		}
	}

	if err := tt.Click("Alpha"); err != nil {
		t.Fatalf("choose Alpha: %v", err)
	}
	tt.Frame()
	if chosen != "Alpha" {
		t.Fatalf("choosing Alpha reported %q", chosen)
	}

	// A submenu stays a submenu in the drawn menu.
	if err := tt.Click("actions"); err != nil {
		t.Fatalf("reopen menu trigger: %v", err)
	}
	tt.Frame()
	if err := tt.Click("More"); err != nil {
		t.Fatalf("open submenu: %v", err)
	}
	tt.Frame()
	if !tt.HasText("Gamma") {
		t.Fatalf("submenu did not draw; texts=%v", tt.Texts())
	}
	if err := tt.Click("Gamma"); err != nil {
		t.Fatalf("choose Gamma: %v", err)
	}
	tt.Frame()
	if chosen != "Gamma" {
		t.Fatalf("choosing Gamma reported %q", chosen)
	}
}

// TestMenuButtonKeyboardAndDismissal checks the drawn menu's keyboard model
// and that Escape and a press outside close it.
func TestMenuButtonKeyboardAndDismissal(t *testing.T) {
	chosen := ""
	tt := ui.NewTester(func(c *ui.Context) {
		MenuButton(c, "Actions", func(m *ui.Menu) {
			if m.Item("Alpha").Chosen() {
				chosen = "Alpha"
			}
			if m.Item("Beta").Chosen() {
				chosen = "Beta"
			}
		}).Label("actions")
	}, 420, 340)
	tt.Frame()

	if err := tt.Click("actions"); err != nil {
		t.Fatalf("click menu trigger: %v", err)
	}
	tt.Frame()
	// A mouse-opened menu starts with no highlight; the first Down moves to
	// Alpha and the second to Beta.
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if chosen != "Beta" {
		t.Fatalf("keyboard selection reported %q, want Beta", chosen)
	}

	if err := tt.Click("actions"); err != nil {
		t.Fatalf("reopen menu trigger: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText("Alpha") {
		t.Fatalf("Escape left the menu open; texts=%v", tt.Texts())
	}

	if err := tt.Click("actions"); err != nil {
		t.Fatalf("reopen menu trigger: %v", err)
	}
	tt.Frame()
	tt.ClickAt(4, 320)
	tt.Frame()
	if tt.HasText("Alpha") {
		t.Fatalf("a press outside left the menu open; texts=%v", tt.Texts())
	}
}

// TestSelectTriggerMatchesMenuButton checks that the Select trigger and the
// shared menu trigger share one geometry and surface, so the two kinds of
// drop-down read as one control family.
func TestSelectTriggerMatchesMenuButton(t *testing.T) {
	choice := "Alpha"
	tt := ui.NewTester(func(c *ui.Context) {
		MenuButton(c, "Actions", func(m *ui.Menu) {}).Label("menu-trigger")
		Select(c, &choice, []string{"Alpha", "Beta"}, nil).Label("select-trigger")
	}, 420, 200)
	tt.Frame()

	menu, ok := tt.Find("menu-trigger")
	if !ok {
		t.Fatal("menu trigger not found")
	}
	sel, ok := tt.Find("select-trigger")
	if !ok {
		t.Fatal("select trigger not found")
	}
	if menu.H != sel.H {
		t.Fatalf("trigger heights differ: menu=%v select=%v", menu.H, sel.H)
	}
	p := PaletteFor(&ui.Theme{})
	img := tt.Image()
	for _, box := range []struct {
		name string
		r    ui.Rect
	}{{"menu", menu}, {"select", sel}} {
		got := img.RGBAAt(int(box.r.X)+3, int(box.r.Y+box.r.H/2))
		if got.R != p.Sidebar.R || got.G != p.Sidebar.G || got.B != p.Sidebar.B {
			t.Fatalf("%s trigger background = %v, want palette Sidebar %v", box.name, got, p.Sidebar)
		}
	}
}
