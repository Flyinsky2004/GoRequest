package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/desktop/uikit"
)

// headerView renders only the workspace top bar, so these tests observe the
// header's own controls without the request body's save entry or the document
// toolbar's Save Doc button. It applies the app theme first, as App.View does,
// so the drawn popup uses the same palette-derived theme as the trigger.
func headerView(a *App, ps *projectState) func(*ui.Context) {
	return func(c *ui.Context) {
		a.applyTheme(c)
		a.workspaceHeader(c, c.Theme(), ps)
	}
}

func newHeaderTester(a *App, ps *projectState) *ui.Tester {
	return ui.NewTester(headerView(a, ps), 1000, 420)
}

// TestHeaderEnvMenuIsDrawnInWindow checks that the environment selector opens a
// menu drawn inside the window (not the system's native menu), that it keeps
// the preview/switch/edit/new items, and that Escape and a press outside close
// it.
func TestHeaderEnvMenuIsDrawnInWindow(t *testing.T) {
	a, ps := openTestProject(t, "HeaderEnvMenu")
	m := a.msg()
	tt := newHeaderTester(a, ps)
	tt.Frame()

	if err := tt.Click("dev"); err != nil {
		t.Fatalf("click env selector: %v", err)
	}
	tt.Frame()

	if got := tt.Menu(); got != nil {
		t.Fatalf("the system showed a native menu: %v", got)
	}
	for _, want := range []string{
		"baseUrl = http://127.0.0.1:4010", // disabled preview row
		"(none)",
		m.EditEnv,
		m.NewEnv,
	} {
		if !tt.HasText(want) {
			t.Fatalf("drawn env menu missing %q; texts=%v", want, tt.Texts())
		}
	}

	// Escape closes the top level.
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if tt.HasText(m.EditEnv) {
		t.Fatalf("Escape left the env menu open; texts=%v", tt.Texts())
	}

	// A press outside closes it too.
	if err := tt.Click("dev"); err != nil {
		t.Fatalf("reopen env selector: %v", err)
	}
	tt.Frame()
	if !tt.HasText(m.EditEnv) {
		t.Fatal("env menu did not reopen")
	}
	tt.ClickAt(4, 400)
	tt.Frame()
	if tt.HasText(m.EditEnv) {
		t.Fatalf("a press outside left the env menu open; texts=%v", tt.Texts())
	}
}

// TestHeaderEnvMenuChoiceStillSwitches checks that choosing an item from the
// drawn menu runs the same action as the native menu did.
func TestHeaderEnvMenuChoiceStillSwitches(t *testing.T) {
	a, ps := openTestProject(t, "HeaderEnvChoice")
	tt := newHeaderTester(a, ps)
	tt.Frame()
	if ps.envSel != "dev" {
		t.Fatalf("expected the seeded env selected, got %q", ps.envSel)
	}
	if err := tt.Click("dev"); err != nil {
		t.Fatalf("click env selector: %v", err)
	}
	tt.Frame()
	if err := tt.Click("(none)"); err != nil {
		t.Fatalf("choose (none): %v", err)
	}
	tt.Frame()
	if ps.envSel != "(none)" {
		t.Fatalf("choosing (none) left envSel = %q", ps.envSel)
	}
	if a.status != translate(a.msg().SwitchedTo, "(none)") {
		t.Fatalf("unexpected status after switching env: %q", a.status)
	}
}

// TestHeaderEnvMenuKeyboardSelects checks that the drawn menu keeps keyboard
// navigation and Enter selection: Down moves to the first choosable item (the
// disabled preview rows are skipped) and Enter chooses it.
func TestHeaderEnvMenuKeyboardSelects(t *testing.T) {
	a, ps := openTestProject(t, "HeaderEnvKeys")
	tt := newHeaderTester(a, ps)
	tt.Frame()
	if err := tt.Click("dev"); err != nil {
		t.Fatalf("click env selector: %v", err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyDown)
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if ps.envSel != "(none)" {
		t.Fatalf("keyboard selection left envSel = %q, want (none)", ps.envSel)
	}
}

// TestHeaderNewMenuIsDrawnInWindow checks that the "+" menu is drawn in the
// window and keeps every original entry, including the interop entries.
func TestHeaderNewMenuIsDrawnInWindow(t *testing.T) {
	a, ps := openTestProject(t, "HeaderNewMenu")
	m := a.msg()
	im := a.interopMsg()
	tt := newHeaderTester(a, ps)
	tt.Frame()

	if err := tt.Click("+"); err != nil {
		t.Fatalf("click + menu: %v", err)
	}
	tt.Frame()

	if got := tt.Menu(); got != nil {
		t.Fatalf("the system showed a native menu: %v", got)
	}
	for _, want := range []string{
		m.NewRequest,
		m.NewFolder,
		m.NewDoc,
		m.NewEnv,
		im.ImportCURLMenuLabel,
		im.ImportMenuLabel,
	} {
		if !tt.HasText(want) {
			t.Fatalf("drawn + menu missing %q; texts=%v", want, tt.Texts())
		}
	}

	// Choosing an item still runs its action.
	if err := tt.Click(m.NewFolder); err != nil {
		t.Fatalf("choose New Folder: %v", err)
	}
	tt.Frame()
	if !a.newFolderOpen {
		t.Fatal("New Folder from the drawn + menu did not open the folder form")
	}
}

// TestHeaderMenusFollowLightAndDarkTheme checks that both drawn menus render in
// both appearances and that the popup surface is the palette background, not a
// fixed system color.
func TestHeaderMenusFollowLightAndDarkTheme(t *testing.T) {
	for _, dark := range []bool{false, true} {
		name := "light"
		if dark {
			name = "dark"
		}
		t.Run(name, func(t *testing.T) {
			a, ps := openTestProject(t, "HeaderTheme"+name)
			tt := newHeaderTester(a, ps)
			tt.SetDark(dark)
			tt.Frame()
			p := uikit.PaletteFor(&ui.Theme{Dark: dark})

			// The trigger uses the palette's sidebar face in both themes.
			trigger, ok := tt.Find("dev")
			if !ok {
				t.Fatalf("env trigger missing; texts=%v", tt.Texts())
			}
			got := tt.Image().RGBAAt(int(trigger.X)+3, int(trigger.Y+trigger.H/2))
			want := p.Sidebar
			if got.R != want.R || got.G != want.G || got.B != want.B {
				t.Fatalf("env trigger background = %v, want palette Sidebar %v", got, want)
			}

			if err := tt.Click("dev"); err != nil {
				t.Fatalf("click env selector: %v", err)
			}
			tt.Frame()
			if got := tt.Menu(); got != nil {
				t.Fatalf("the system showed a native menu in the %s theme: %v", name, got)
			}

			// The drawn popup panel uses the theme background (palette BG).
			item, ok := tt.Find("(none)")
			if !ok {
				t.Fatalf("drawn menu item missing in the %s theme; texts=%v", name, tt.Texts())
			}
			px := tt.Image().RGBAAt(int(item.X)-2, int(item.Y+item.H/2))
			wantBG := p.BG
			if px.R != wantBG.R || px.G != wantBG.G || px.B != wantBG.B {
				t.Fatalf("%s popup panel = %v, want palette BG %v", name, px, wantBG)
			}
		})
	}
}

// TestHeaderSaveControlVisibilityAndSave checks the relocated save button: it
// is absent while the active request is clean, present while it is dirty,
// saves through the synced path, and disappears once the save succeeds.
func TestHeaderSaveControlVisibilityAndSave(t *testing.T) {
	a, ps := openTestProject(t, "HeaderPersist")
	m := a.msg()
	tabs := openRequestTabs(t, ps, "Alpha", "Beta")
	alpha, beta := tabs[0], tabs[1]
	tt := newHeaderTester(a, ps)
	tt.Frame()

	if tt.HasText(m.Save) {
		t.Fatalf("save control shown for a clean tab; texts=%v", tt.Texts())
	}

	alpha.URL = "https://example.com/dirty"
	alpha.Dirty = true
	ps.activateTab(alpha)
	tt.Frame()
	if !tt.HasText(m.Save) {
		t.Fatalf("save control missing for a dirty tab; texts=%v", tt.Texts())
	}
	// The save button sits right of the environment selector, in the same row.
	env, ok := tt.Find("dev")
	if !ok {
		t.Fatalf("env selector missing; texts=%v", tt.Texts())
	}
	save, ok := tt.Find(m.Save)
	if !ok {
		t.Fatalf("save button missing; texts=%v", tt.Texts())
	}
	if save.X < env.X+env.W-1 {
		t.Fatalf("save button is not right of the env selector: env=%+v save=%+v", env, save)
	}
	saveMid, envMid := save.Y+save.H/2, env.Y+env.H/2
	if saveMid < env.Y || saveMid > env.Y+env.H {
		t.Fatalf("save button left the env row: env=%+v save=%+v", env, save)
	}
	if envMid < save.Y || envMid > save.Y+save.H {
		t.Fatalf("env selector and save button are not in one row: env=%+v save=%+v", env, save)
	}

	if err := tt.Click(m.Save + " ⌘S"); err != nil {
		t.Fatalf("click header save: %v", err)
	}
	tt.Frame()
	if alpha.Dirty {
		t.Fatalf("header save did not clear the dirty flag; status=%q", a.status)
	}
	if a.status == "" {
		t.Fatal("header save did not report a status")
	}
	if tt.HasText(m.Save) {
		t.Fatalf("save control still shown after saving; texts=%v", tt.Texts())
	}

	// Switching tabs must reflect each tab's own state.
	ps.activateTab(beta)
	tt.Frame()
	if tt.HasText(m.Save) {
		t.Fatalf("save control shown for a clean second tab; texts=%v", tt.Texts())
	}
	beta.URL = "https://example.com/beta"
	beta.Dirty = true
	tt.Frame()
	if !tt.HasText(m.Save) {
		t.Fatalf("save control missing for a dirty second tab; texts=%v", tt.Texts())
	}
	ps.activateTab(alpha)
	tt.Frame()
	if tt.HasText(m.Save) {
		t.Fatalf("save control leaked from the dirty tab to the clean one; texts=%v", tt.Texts())
	}
	ps.activateTab(beta)
	tt.Frame()
	if !tt.HasText(m.Save) {
		t.Fatalf("save control missing after switching back to the dirty tab; texts=%v", tt.Texts())
	}
}

// TestHeaderSaveControlTracksDocumentDirtyState checks the document save
// semantics: a document tab mirrors its editor buffer's Modified state into
// Tab.Dirty, so the header save appears only while the document is modified and
// saves it through the same synced path.
func TestHeaderSaveControlTracksDocumentDirtyState(t *testing.T) {
	a, ps := openTestProject(t, "HeaderPersistDoc")
	m := a.msg()
	tab := openDocForTest(t, ps, "docs/guide.md", docGuideSrc)
	tt := newHeaderTester(a, ps)
	tt.Frame()

	if tt.HasText(m.Save) {
		t.Fatalf("save control shown for a clean document; texts=%v", tt.Texts())
	}

	tab.DocEditor.Buffer.Set("edited\n" + docGuideSrc)
	tab.Dirty = tab.DocEditor.Buffer.Modified()
	if !tab.Dirty {
		t.Fatal("editing the document buffer did not mark it dirty")
	}
	tt.Frame()
	if !tt.HasText(m.Save) {
		t.Fatalf("save control missing for a modified document; texts=%v", tt.Texts())
	}

	if err := tt.Click(m.Save + " ⌘S"); err != nil {
		t.Fatalf("click header save for doc: %v", err)
	}
	tt.Frame()
	if tab.Dirty {
		t.Fatalf("header save did not clear the document dirty flag; status=%q", a.status)
	}
	if tt.HasText(m.Save) {
		t.Fatalf("save control still shown after saving the document; texts=%v", tt.Texts())
	}
}
