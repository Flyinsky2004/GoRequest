package desktop

import (
	"fmt"
	"testing"

	"github.com/egoist/mygo/ui"
)

// FIX06 regression suite. It renders the real settings sheet headlessly at the
// production window sizes and asserts the compact, stable geometry the user
// asked for. All numbers are content DIPs at scale 1; Find returns window
// DIPs, so zoom-aware assertions multiply by the zoom factor.

// settingsSheetFixture builds one settings sheet over a plain window at the
// given size. Set zoom/scale/dark/language before the first assertion.
func settingsSheetFixture(t *testing.T, lang language, width, height int) (*ui.Tester, *configStore, *bool) {
	t.Helper()
	m, _ := newTestManager(t)
	m.cfg.cfg.Language = string(lang)
	// Keep the About pane's config line short: the fit checks measure layout,
	// not an unusually long temp path.
	m.cfg.path = ""
	cfg, prefs := m.cfg, m.workspacePrefs()
	open := true
	tt := ui.NewTester(func(c *ui.Context) {
		renderSettings(c, c.Theme(), cfg, prefs, &open, func(string) {})
	}, width, height)
	return tt, cfg, &open
}

// TestSettingsGeometryForWindows pins the pure sizing formula. The panel is
// roughly half of the old winH-80 sheet on a normal project window, shrinks
// proportionally on small windows, and never asks for more room than exists.
func TestSettingsGeometryForWindows(t *testing.T) {
	for _, tc := range []struct{ w, h int }{
		{1200, 820}, // usual project window (target case)
		{1440, 900}, // larger project window: absolute cap wins
		{720, 480},  // welcome window: proportional shrink
		{760, 560},  // narrow project window
		{560, 360},  // welcome window minimum
		{400, 300},  // very small window
		{300, 200},  // below the preferred floor: window wins
	} {
		t.Run(fmt.Sprintf("%dx%d", tc.w, tc.h), func(t *testing.T) {
			g := settingsGeometryFor(float32(tc.w), float32(tc.h))
			// Old FIX05 panel height was winH-80+40 = winH-40 content + chrome.
			oldPanel := float32(tc.h) - 40
			newPanel := g.height + settingsModalChrome
			if newPanel > settingsMaxContentHeight+settingsModalChrome+0.01 {
				t.Fatalf("panel %g exceeds cap %d", newPanel, settingsMaxContentHeight+settingsModalChrome)
			}
			if newPanel > float32(tc.h)+0.01 {
				t.Fatalf("panel %g exceeds window height %d", newPanel, tc.h)
			}
			if g.height < 1 {
				t.Fatalf("degenerate content height %g", g.height)
			}
			// The usual 1200x820 window is the explicit "about half" target.
			if tc.w == 1200 && tc.h == 820 && newPanel > oldPanel*0.60 {
				t.Fatalf("panel %g is not compact vs old %g", newPanel, oldPanel)
			}
			// Where the proportional target is actually in control (above the
			// minimum usable height), the sheet must not dominate the window.
			// Shorter windows instead keep the minimum usable sheet and are
			// clamped to the window; their invariant is "fits the window",
			// already asserted above (squashing the controls is not allowed).
			proportional := float32(tc.h)*settingsTargetPanelFraction - settingsModalChrome
			if proportional > settingsMinContentHeight && newPanel > float32(tc.h)*0.70 {
				t.Fatalf("panel %g dominates %d-high window", newPanel, tc.h)
			}
			t.Logf("window=%dx%d panel=%.0f (old=%.0f, %.0f%%) content=%.0f width=%.0f rail=%.0f",
				tc.w, tc.h, newPanel, oldPanel, 100*newPanel/oldPanel, g.height, g.width, g.leftW)
		})
	}
}

// TestSettingsSheetFrameIsCompactAndInWindow measures the real modal frame via
// the rendered title and Done button. It must fit entirely inside the window,
// use at most ~55% of a normal project window's height, and never hide Done.
func TestSettingsSheetFrameIsCompactAndInWindow(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		for _, tc := range []struct{ w, h int }{
			{1200, 820}, {720, 480}, {760, 560}, {560, 360}, {400, 300}, {300, 200},
		} {
			lang, tc := lang, tc
			t.Run(fmt.Sprintf("%s/%dx%d", lang, tc.w, tc.h), func(t *testing.T) {
				tt, cfg, _ := settingsSheetFixture(t, lang, tc.w, tc.h)
				m := messagesFor(cfg.language())
				title, ok := tt.Find(m.SettingsTitle)
				if !ok {
					t.Fatalf("missing title; texts=%v", tt.Texts())
				}
				done, ok := tt.Find(m.Done)
				if !ok {
					t.Fatalf("missing Done; texts=%v", tt.Texts())
				}
				// Frame top = title top - panel padding; bottom = Done bottom
				// + panel padding (20 DIPs each, from MyGo coreModal).
				pad := float32(settingsModalChrome) / 2
				frameTop, frameBottom := title.Y-pad, done.Y+done.H+pad
				frameH := frameBottom - frameTop
				if frameTop < -0.5 || frameBottom > float32(tc.h)+0.5 {
					t.Fatalf("frame [%.1f, %.1f] outside window height %d", frameTop, frameBottom, tc.h)
				}
				if done.H <= 0 || done.W <= 0 {
					t.Fatalf("Done not actually visible: %+v", done)
				}
				if err := tt.Click(m.Done); err != nil {
					t.Fatalf("Done not clickable: %v", err)
				}
				if tc.h >= 820 && frameH > float32(tc.h)*0.60 {
					t.Fatalf("frame %.0f not compact on %d-high window", frameH, tc.h)
				}
				assertNoHorizontalOverflow(t, tt, tc.w)
			})
		}
	}
}

// TestSettingsSheetHeightStableAcrossCategories is the no-jump guarantee: the
// geometry depends only on the window, so switching between a short and a long
// category keeps the title and Done at exactly the same position. It also
// proves the right pane scrolls (a long Workspace pane can be scrolled while
// Done stays put) and that switching categories keeps every control reachable.
func TestSettingsSheetHeightStableAcrossCategories(t *testing.T) {
	tt, cfg, _ := settingsSheetFixture(t, langEnglish, 1200, 820)
	m := messagesFor(cfg.language())
	titles := settingsCategoryTitles(cfg.language())

	read := func() (titleY, doneY float32) {
		t.Helper()
		title, ok := tt.Find(m.SettingsTitle)
		if !ok {
			t.Fatalf("title lost; texts=%v", tt.Texts())
		}
		done, ok := tt.Find(m.Done)
		if !ok {
			t.Fatalf("Done lost; texts=%v", tt.Texts())
		}
		return title.Y, done.Y
	}
	baseTitle, baseDone := read()

	for _, cat := range []settingsCategory{
		settingsCategoryWorkspace, settingsCategoryAbout, settingsCategoryHistory, settingsCategoryAppearance,
	} {
		label := titles[int(cat)]
		if err := tt.Click(label); err != nil {
			t.Fatalf("click category %q: %v", label, err)
		}
		tt.Frame()
		titleY, doneY := read()
		if titleY != baseTitle || doneY != baseDone {
			t.Fatalf("sheet jumped on %q: title %.1f->%.1f Done %.1f->%.1f",
				label, baseTitle, titleY, baseDone, doneY)
		}
		assertNoHorizontalOverflow(t, tt, 1200)
	}

	// The right pane is independently scrollable: a long Workspace pane moves
	// its own content while Done and the rail stay where they are.
	if err := tt.Click(titles[int(settingsCategoryWorkspace)]); err != nil {
		t.Fatalf("open Workspace: %v", err)
	}
	tt.Frame()
	before, ok := tt.Find(m.ShortcutsSection)
	if !ok {
		t.Fatalf("missing Workspace bottom section; texts=%v", tt.Texts())
	}
	tt.Move(700, 400)
	tt.Scroll(700, 400, 0, 260)
	tt.Frame()
	after, ok := tt.Find(m.ShortcutsSection)
	if !ok {
		t.Fatalf("lost Workspace bottom section after scroll")
	}
	if after.Y >= before.Y-20 {
		t.Fatalf("right pane did not scroll: before=%+v after=%+v", before, after)
	}
	titleY, doneY := read()
	if titleY != baseTitle || doneY != baseDone {
		t.Fatalf("scrolling content moved the frame: title %.1f Done %.1f", titleY, doneY)
	}
}

// TestSettingsRailScrollsToEveryCategory proves the left rail stays fully
// reachable on a short window: the last category starts clipped, becomes
// visible after scrolling the rail, and remains clickable to switch panes.
// It also checks the two panes are independent (scrolling the right pane must
// not scroll the rail).
func TestSettingsRailScrollsToEveryCategory(t *testing.T) {
	tt, cfg, _ := settingsSheetFixture(t, langEnglish, 560, 360)
	m := messagesFor(cfg.language())
	titles := settingsCategoryTitles(cfg.language())
	last := titles[len(titles)-1]

	before, ok := tt.Find(last)
	if !ok {
		t.Fatalf("missing last category %q; texts=%v", last, tt.Texts())
	}
	if before.H > 0 {
		t.Fatalf("expected %q to start below the compact rail fold, got %+v", last, before)
	}

	// Switching to a long content pane and scrolling the right side must not
	// scroll the rail (independence).
	if err := tt.Click(titles[int(settingsCategoryWorkspace)]); err != nil {
		t.Fatalf("click Workspace: %v", err)
	}
	tt.Frame()
	tt.Move(400, 200)
	tt.Scroll(400, 200, 0, 500)
	tt.Frame()
	if r, _ := tt.Find(last); r.H > 0 {
		t.Fatalf("right-pane scroll leaked into the rail: %+v", r)
	}

	// The rail itself scrolls to reveal the last category.
	tt.Move(100, 200)
	tt.Scroll(100, 200, 0, 220)
	tt.Frame()
	r, ok := tt.Find(last)
	if !ok || r.H <= 0 {
		t.Fatalf("last category %q still unreachable after rail scroll: %+v", last, r)
	}
	if err := tt.Click(last); err != nil {
		t.Fatalf("last category %q not clickable: %v", last, err)
	}
	tt.Frame()
	if !tt.HasText(m.VersionLabel) {
		t.Fatalf("clicking %q did not open About: texts=%v", last, tt.Texts())
	}
	assertNoHorizontalOverflow(t, tt, 560)
}

// TestSettingsSheetEnlargedTextAndZoom keeps Done inside the window when text
// metrics grow (enlarged base font) and under content zoom. The fixed height
// must absorb the larger controls by shrinking the scrollable panes, not by
// pushing Done out of the sheet.
func TestSettingsSheetEnlargedTextAndZoom(t *testing.T) {
	for _, tc := range []struct {
		name     string
		w, h     int
		zoom     float32
		fontSize float32
	}{
		{"bigfont/project", 1200, 820, 1, 26},
		{"bigfont/welcome", 720, 480, 1, 26},
		{"zoom2/welcome", 720, 480, 2, 0},
		{"zoom1.5/project", 1200, 820, 1.5, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, _ := newTestManager(t)
			m.cfg.cfg.Language = string(langEnglish)
			m.cfg.path = ""
			cfg, prefs := m.cfg, m.workspacePrefs()
			open := true
			winW := int(float32(tc.w) * tc.zoom)
			winH := int(float32(tc.h) * tc.zoom)
			tt := ui.NewTester(func(c *ui.Context) {
				th := c.Theme()
				if tc.fontSize > 0 {
					copy := *th
					copy.FontSize = tc.fontSize
					c.SetTheme(&copy)
					th = &copy
				}
				renderSettings(c, th, cfg, prefs, &open, func(string) {})
			}, winW, winH)
			if tc.zoom != 1 {
				tt.SetZoom(tc.zoom)
			}
			msgs := messagesFor(cfg.language())
			title, ok := tt.Find(msgs.SettingsTitle)
			if !ok {
				t.Fatalf("missing title; texts=%v", tt.Texts())
			}
			done, ok := tt.Find(msgs.Done)
			if !ok {
				t.Fatalf("missing Done; texts=%v", tt.Texts())
			}
			pad := float32(settingsModalChrome) / 2 * tc.zoom
			frameTop, frameBottom := title.Y-pad, done.Y+done.H+pad
			if frameTop < -1 || frameBottom > float32(winH)+1 {
				t.Fatalf("enlarged/zoomed frame [%.1f, %.1f] outside window %d", frameTop, frameBottom, winH)
			}
			if done.H <= 0 || done.W <= 0 {
				t.Fatalf("Done hidden by enlarged content: %+v", done)
			}
			if err := tt.Click(msgs.Done); err != nil {
				t.Fatalf("Done not clickable with enlarged content: %v", err)
			}
			if open {
				t.Fatal("Done did not dismiss the sheet")
			}
			t.Logf("frame=[%.0f, %.0f] of %d; Done=%+v", frameTop, frameBottom, winH, done)
		})
	}
}
