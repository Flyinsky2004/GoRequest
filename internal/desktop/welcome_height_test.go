package desktop

import (
	"path/filepath"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/platform"
)

// recordingFactory captures the window specs the manager asks for, so the
// welcome-height tests can drive the real default size through the headless
// tester instead of hard-coding a size that could drift from showWelcome.
type recordingFactory struct {
	specs []windowSpec
}

func (rf *recordingFactory) new(s windowSpec) window {
	rf.specs = append(rf.specs, s)
	return &fakeWindow{title: s.Title}
}

func newRecordingTestManager(t *testing.T) (*Manager, *recordingFactory) {
	t.Helper()
	ff := &recordingFactory{}
	m := &Manager{
		cfg:       newConfigStore(filepath.Join(t.TempDir(), "config.json")),
		secrets:   platform.NewEnvSecrets(),
		newWindow: ff.new,
	}
	m.cfg.cfg.Language = string(langEnglish)
	return m, ff
}

// welcomeSheetTester opens the welcome window at the given size and then the
// new-project sheet, returning the tester and the project messages.
func welcomeSheetTester(t *testing.T, m *Manager, w, h int) (*ui.Tester, *welcomeProjectMessages) {
	t.Helper()
	m.showWelcome("")
	tt := ui.NewTester(m.welcome.view, w, h)
	tt.Frame()
	if err := tt.Click("New Project…"); err != nil {
		t.Fatalf("open new project sheet: %v", err)
	}
	tt.Frame()
	return tt, m.welcome.projectMsg()
}

// sheetBodyGap is the vertical space between the last body line and the
// always-visible footer. A taller sheet keeps this gap larger instead of
// stretching or compressing the controls.
func sheetBodyGap(t *testing.T, tt *ui.Tester, pm *welcomeProjectMessages) float32 {
	t.Helper()
	hint, ok := tt.Find(pm.KeyboardHint)
	if !ok {
		t.Fatalf("keyboard hint missing; texts=%v", tt.Texts())
	}
	footer, ok := tt.Find("Cancel")
	if !ok {
		t.Fatalf("Cancel missing; texts=%v", tt.Texts())
	}
	return footer.Y - (hint.Y + hint.H)
}

// TestWelcomeDefaultWindowFitsNewProjectSheet drives the actual default window
// size from showWelcome through the headless tester and checks that the sheet
// shows every required control without the body having to scroll. This is the
// regression for the old 480-DIP default, where the sheet was capped at 400
// DIPs and the last rows were pushed out of view.
func TestWelcomeDefaultWindowFitsNewProjectSheet(t *testing.T) {
	m, ff := newRecordingTestManager(t)
	m.showWelcome("")
	if len(ff.specs) != 1 {
		t.Fatalf("created %d windows, want 1", len(ff.specs))
	}
	spec := ff.specs[0]

	tt, pm := welcomeSheetTester(t, m, spec.Width, spec.Height)
	for _, label := range []string{
		pm.ModeIndependent, pm.ModeExisting,
		"Name", "Location", pm.FinalPathLabel,
		"Cancel", "Create",
	} {
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("%q missing at default %d×%d; texts=%v", label, spec.Width, spec.Height, tt.Texts())
		}
		if r.X < 0 || r.Y < 0 || r.X+r.W > float32(spec.Width) || r.Y+r.H > float32(spec.Height) {
			t.Fatalf("%q is clipped at default %d×%d: %+v", label, spec.Width, spec.Height, r)
		}
	}

	// No scrolling at the default size: the last body line is rendered at its
	// full line height (the old 480-DIP default clipped it) and keeps clear
	// space above the footer instead of being squeezed against it.
	ref, ok := tt.Find(m.msg().NewProjectFolderHint)
	if !ok {
		t.Fatalf("folder hint missing; texts=%v", tt.Texts())
	}
	hint, ok := tt.Find(pm.KeyboardHint)
	if !ok {
		t.Fatalf("keyboard hint missing; texts=%v", tt.Texts())
	}
	if hint.H < ref.H-0.5 {
		t.Fatalf("last body line is clipped at default %d×%d: height %.2f, one line is %.2f", spec.Width, spec.Height, hint.H, ref.H)
	}
	if gap := sheetBodyGap(t, tt, pm); gap < 40 {
		t.Fatalf("default sheet is cramped against the footer (gap %.1f); the welcome window is too short", gap)
	}

	left, _ := tt.Find(pm.ModeIndependent)
	right, _ := tt.Find(pm.ModeExisting)
	if left.X >= right.X {
		t.Fatalf("mode cards should stay side by side: left=%+v right=%+v", left, right)
	}
}

// TestWelcomeNewProjectSheetGrowsOnLargeWindow verifies that enlarging the
// welcome window actually gives the sheet more vertical room (rather than the
// old fixed 460-DIP cap), and that the growth stops at the designed cap so the
// panel never exceeds the window.
func TestWelcomeNewProjectSheetGrowsOnLargeWindow(t *testing.T) {
	m, _ := newRecordingTestManager(t)

	const defW, defH = 720, 640
	def, pm := welcomeSheetTester(t, m, defW, defH)
	defGap := sheetBodyGap(t, def, pm)

	// Reuse the same open sheet state in a larger window.
	large := ui.NewTester(m.welcome.view, 900, 760)
	large.Frame()
	largeGap := sheetBodyGap(t, large, pm)
	if largeGap <= defGap+20 {
		t.Fatalf("large window did not give the sheet more room: default gap %.1f, large gap %.1f", defGap, largeGap)
	}

	// Past the cap the sheet stops growing, so the extra window height goes to
	// the surrounding modal margin instead of an unbounded panel.
	huge := ui.NewTester(m.welcome.view, 900, 1200)
	huge.Frame()
	hugeGap := sheetBodyGap(t, huge, pm)
	if diff := hugeGap - largeGap; diff > 1 || diff < -1 {
		t.Fatalf("sheet grew past its height cap: 760-high gap %.1f, 1200-high gap %.1f", largeGap, hugeGap)
	}
}

// TestWelcomeNewProjectSmallWindowScrollsWithReachableFooter keeps the short
// screen contract: the body overflows into a scroll region, while the footer
// stays on screen and Cancel remains clickable.
func TestWelcomeNewProjectSmallWindowScrollsWithReachableFooter(t *testing.T) {
	m, _ := newRecordingTestManager(t)
	const w, h = 560, 360
	tt, pm := welcomeSheetTester(t, m, w, h)

	hint, ok := tt.Find(pm.KeyboardHint)
	if !ok {
		t.Fatalf("keyboard hint missing; texts=%v", tt.Texts())
	}
	cancel, ok := tt.Find("Cancel")
	if !ok {
		t.Fatalf("Cancel missing; texts=%v", tt.Texts())
	}
	if cancel.Y+cancel.H > h {
		t.Fatalf("Cancel is clipped at %d×%d: %+v", w, h, cancel)
	}
	if hint.Y+hint.H <= cancel.Y {
		t.Fatalf("small sheet body should scroll, but the hint already fits: hint=%+v footer=%+v", hint, cancel)
	}

	// Scrolling the body reveals the hint while the footer stays put.
	tt.Scroll(w/2, h/2, 0, 400)
	tt.Frame()
	scrolled, _ := tt.Find(pm.KeyboardHint)
	movedFooter, _ := tt.Find("Cancel")
	if scrolled.Y >= hint.Y {
		t.Fatalf("body did not scroll: hint y %.1f → %.1f", hint.Y, scrolled.Y)
	}
	if movedFooter.Y != cancel.Y {
		t.Fatalf("footer moved with the body: %.1f → %.1f", cancel.Y, movedFooter.Y)
	}
	if scrolled.Y+scrolled.H > movedFooter.Y {
		t.Fatalf("hint still hidden under the footer after scrolling: hint=%+v footer=%+v", scrolled, movedFooter)
	}
	if err := tt.Click("Cancel"); err != nil {
		t.Fatalf("Cancel must stay clickable in a small window: %v", err)
	}
	tt.Frame()
	if m.welcome.newProjOpen {
		t.Fatal("Cancel did not close the sheet")
	}
}
