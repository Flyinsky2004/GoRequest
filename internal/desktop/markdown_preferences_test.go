package desktop

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
	"gorequest/internal/desktop/uikit"
)

func TestMarkdownPreferencesCompatibilityAndRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "workspace.json")
	for _, raw := range []string{`{}`, `{"editorFontSize":18}`, `{"markdown":{"fontPercent":-1,"padding":999,"linePercent":1}}`} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		s := newWorkspacePrefsStore(path)
		if got := s.markdownPolicy(); got != defaultMarkdownPreferences() {
			t.Fatal(got)
		}
	}
	s := newWorkspacePrefsStore(path)
	s.setFontSize(18)
	s.setAutoSaveEnabled(true)
	want := markdownPreferences{125, 32, 180}
	if err := s.setMarkdownPolicy(want); err != nil {
		t.Fatal(err)
	}
	reloaded := newWorkspacePrefsStore(path)
	if reloaded.markdownPolicy() != want || reloaded.fontSize() != 18 || !reloaded.autoSaveEnabled() {
		t.Fatal("round trip lost preferences")
	}
	reloaded.setTabSize(8)
	if newWorkspacePrefsStore(path).markdownPolicy() != want {
		t.Fatal("B02 save lost Markdown preferences")
	}
	if err := s.setMarkdownPolicy(markdownPreferences{201, 24, 150}); err == nil {
		t.Fatal("invalid input accepted")
	}
	if err := s.setMarkdownPolicy(defaultMarkdownPreferences()); err != nil {
		t.Fatal(err)
	}
	if newWorkspacePrefsStore(path).markdownPolicy() != defaultMarkdownPreferences() {
		t.Fatal("reset failed")
	}
}

func TestMarkdownSaveFailureIsVisibleAndRetainsPolicy(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(blocker, []byte("block"), 0600); err != nil {
		t.Fatal(err)
	}
	s := newWorkspacePrefsStore(filepath.Join(blocker, "workspace.json"))
	view := func(c *ui.Context) { s.markdownSettingsGroup(c, uikit.PaletteFor(c.Theme()), langEnglish, nil) }
	tt := ui.NewTester(view, 520, 360)
	tt.Frame()
	if err := tt.Click("Reset Markdown defaults"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !tt.HasText("Could not save Markdown preferences:") {
		t.Fatal(tt.Texts())
	}
	if err := s.setMarkdownPolicy(markdownPreferences{150, 32, 180}); err == nil {
		t.Fatal("write should fail")
	}
	if s.markdownPolicy() != defaultMarkdownPreferences() {
		t.Fatal("failed save changed effective policy")
	}
}

func TestMarkdownPreviewInsetsAndScaling(t *testing.T) {
	for _, width := range []float32{240, 400, 1000} {
		for _, scale := range []int{75, 100, 200} {
			a, _ := openTestProject(t, "Markdown geometry")
			policy := markdownPreferences{scale, 24, 180}
			if err := a.mgr.workspacePrefs().setMarkdownPolicy(policy); err != nil {
				t.Fatal(err)
			}
			opts := &markdown.Options{}
			tt := ui.NewTester(func(c *ui.Context) {
				opts.Theme = policy.theme(c.Theme())
				a.markdownPreview(c, "中文正文 English\n", opts)
			}, int(width), 400)
			tt.Frame()
			outer, ok := tt.Find("markdown-preview-insets")
			if !ok {
				t.Fatal("insets missing")
			}
			inner, ok := tt.Find("markdown-preview-content")
			if !ok {
				t.Fatal("content missing")
			}
			if math.Abs(float64(inner.X-outer.X-24)) > 0.01 || math.Abs(float64(inner.Y-outer.Y-24)) > 0.01 || math.Abs(float64(outer.X+outer.W-inner.X-inner.W-24)) > 0.01 || math.Abs(float64(outer.Y+outer.H-inner.Y-inner.H-24)) > 0.01 {
				t.Fatalf("insets width %v scale %v: %v %v", width, scale, outer, inner)
			}
			if inner.W != width-48 || inner.H <= 0 {
				t.Fatalf("content bounds: %v", inner)
			}
			if !tt.HasText("中文正文") {
				t.Fatal("Chinese text missing")
			}
		}
	}
}

func TestMarkdownExistingPreviewUpdatesWithoutLosingState(t *testing.T) {
	a, ps := openTestProject(t, "Markdown update")
	tab := openDocForTest(t, ps, "docs/read.md", "# 标题\n\n正文 text\n\n- list\n\n> quote\n\n| A | B |\n|---|---|\n| cell | 中文 |\n\n```go\nvar a = 1\n```\n")
	tab.Section = 2
	tt := ui.NewTester(a.View, 1200, 820)
	tt.Frame()
	tab.DocEditor.Insert("unsaved ")
	tt.Frame()
	editor := tab.DocEditor
	source := editor.Buffer.String()
	cursor := editor.CursorPos()
	font := editor.Font
	before, ok := tt.Find("markdown-preview-content")
	if !ok {
		t.Fatal("no preview")
	}
	if err := a.mgr.workspacePrefs().setMarkdownPolicy(markdownPreferences{200, 32, 220}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	after, _ := tt.Find("markdown-preview-content")
	if after.H <= before.H {
		t.Fatalf("existing preview did not reflow: %v %v", before, after)
	}
	if ps.activeTab() != tab || tab.DocEditor != editor || editor.Buffer.String() != source || editor.CursorPos() != cursor || !tab.Dirty || editor.Font != font {
		t.Fatal("preview update changed editor/tab state")
	}
	tab.Section = 0
	tt.Frame()
	if !tt.HasText("正文") {
		t.Fatal("split preview missing")
	}
}

func TestMarkdownStyleChangePreservesScroll(t *testing.T) {
	a, _ := openTestProject(t, "Markdown scroll")
	var scroll ui.ScrollState
	opts := &markdown.Options{}
	source := strings.Repeat("正文 English paragraph\n\n", 100)
	tt := ui.NewTester(func(c *ui.Context) {
		opts.Theme = a.mgr.workspacePrefs().markdownPolicy().theme(c.Theme())
		ui.Scroll(c).Fill().TrackScroll(&scroll).Children(func() { a.markdownPreview(c, source, opts) })
	}, 400, 300)
	tt.Frame()
	scroll.Y = 250
	tt.Frame()
	if scroll.Y != 250 {
		t.Fatal("scroll setup failed")
	}
	if err := a.mgr.workspacePrefs().setMarkdownPolicy(markdownPreferences{125, 32, 180}); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if scroll.Y != 250 {
		t.Fatalf("style update reset scroll: %v", scroll.Y)
	}
}

func TestMarkdownSettingsControlsPersistAndReset(t *testing.T) {
	for _, lang := range []language{langEnglish, langSimplifiedChinese} {
		s := newWorkspacePrefsStore(filepath.Join(t.TempDir(), "workspace.json"))
		m := markdownSettingsMessagesFor(lang)
		tt := ui.NewTester(func(c *ui.Context) { s.markdownSettingsGroup(c, uikit.PaletteFor(c.Theme()), lang, nil) }, 520, 360)
		tt.Frame()
		if err := tt.Click("100%"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("125%"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if s.markdownPolicy().FontPercent != 125 {
			t.Fatal("font selection did not apply")
		}
		if err := tt.Click("24"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("32"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if err := tt.Click("150%"); err != nil {
			t.Fatal(err)
		}
		if err := tt.Click("180%"); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if newWorkspacePrefsStore(s.path).markdownPolicy() != (markdownPreferences{125, 32, 180}) {
			t.Fatal("controls did not persist")
		}
		if err := tt.Click(m.Reset); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
		if newWorkspacePrefsStore(s.path).markdownPolicy() != defaultMarkdownPreferences() {
			t.Fatal("reset control did not persist")
		}
	}
}

func TestMarkdownSettingsRedrawOpenProjectWindows(t *testing.T) {
	m, ff := newTestManager(t)
	if err := m.openProject(writeProject(t, "First")); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(writeProject(t, "Second")); err != nil {
		t.Fatal(err)
	}
	s := m.workspacePrefs()
	// Same binding supplied by the integration patch; keep the owner test useful
	// before and after the public shell is wired.
	s.markdownChanged = m.invalidateMarkdownPreviews
	before := []int{ff.created[0].updates, ff.created[1].updates}
	tt := ui.NewTester(func(c *ui.Context) { s.markdownSettingsGroup(c, uikit.PaletteFor(c.Theme()), langEnglish, nil) }, 520, 360)
	tt.Frame()
	if err := tt.Click("Reset Markdown defaults"); err != nil {
		t.Fatal(err)
	}
	if ff.created[0].updates <= before[0] || ff.created[1].updates <= before[1] {
		t.Fatal("live windows were not redrawn")
	}
}

func TestMarkdownPolicyNormalizesEachFieldIndependently(t *testing.T) {
	got := (markdownPreferences{125, -4, 180}).normalized()
	if got != (markdownPreferences{125, 24, 180}) {
		t.Fatal(got)
	}
	for _, p := range []markdownPreferences{{75, 8, 120}, {200, 64, 220}} {
		if p.normalized() != p {
			t.Fatal("valid boundary rejected")
		}
		theme := p.theme(&ui.Theme{FontSize: 16})
		if theme.FontSize != 16*float32(p.FontPercent)/100 || theme.LineHeight != float32(p.LinePercent)/100 {
			t.Fatal("theme does not reflect policy")
		}
	}
}
