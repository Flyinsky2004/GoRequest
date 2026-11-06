package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

// hasExactLabel reports whether the last frame shows a text node exactly equal
// to s. Unlike Tester.HasText (substring), this ignores the project path/window
// title text, which can incidentally contain an English word.
func hasExactLabel(tt *ui.Tester, s string) bool {
	for _, txt := range tt.Texts() {
		if txt == s {
			return true
		}
	}
	return false
}

// TestMessagesCoverEveryField is the guard referenced in i18n.go: every string
// field of the messages struct must be filled in BOTH catalogs. A new field
// with a forgotten translation becomes a compile-visible empty string here
// instead of silently rendering blank in one language.
func TestMessagesCoverEveryField(t *testing.T) {
	typ := reflect.TypeOf(messages{})
	zh := reflect.ValueOf(messagesZh)
	en := reflect.ValueOf(messagesEn)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		if strings.TrimSpace(zh.Field(i).String()) == "" {
			t.Errorf("messagesZh.%s is empty", f.Name)
		}
		if strings.TrimSpace(en.Field(i).String()) == "" {
			t.Errorf("messagesEn.%s is empty", f.Name)
		}
	}
}

// TestChineseCatalogIsActuallyTranslated spot-checks that the Chinese catalog
// is a real translation rather than a copy of the English one.
func TestChineseCatalogIsActuallyTranslated(t *testing.T) {
	checks := []struct {
		name       string
		zh, en     string
		wantSubstr string
	}{
		{"Settings", messagesZh.Settings, messagesEn.Settings, "设置"},
		{"OpenProject", messagesZh.OpenProject, messagesEn.OpenProject, "打开"},
		{"RequestsGroup", messagesZh.RequestsGroup, messagesEn.RequestsGroup, "请求"},
		{"LanguageLabel", messagesZh.LanguageLabel, messagesEn.LanguageLabel, "语言"},
		{"NewRequest", messagesZh.NewRequest, messagesEn.NewRequest, "新建"},
	}
	for _, c := range checks {
		if c.zh == c.en {
			t.Errorf("%s: Chinese catalog equals English (%q)", c.name, c.zh)
		}
		if !strings.Contains(c.zh, c.wantSubstr) {
			t.Errorf("%s: Chinese catalog %q does not contain %q", c.name, c.zh, c.wantSubstr)
		}
	}
}

func TestDefaultLanguageIsSimplifiedChinese(t *testing.T) {
	if got := defaultLanguage(); got != langSimplifiedChinese {
		t.Fatalf("defaultLanguage() = %q, want %q", got, langSimplifiedChinese)
	}
	// A fresh, empty config has no stored language and must resolve to Chinese.
	if got := newConfigStore("").language(); got != langSimplifiedChinese {
		t.Fatalf("empty config language = %q, want Simplified Chinese", got)
	}
}

func TestParseLanguage(t *testing.T) {
	cases := []struct {
		in   string
		want language
	}{
		{"", langSimplifiedChinese},
		{"   ", langSimplifiedChinese},
		{"zh", langSimplifiedChinese},
		{"zh-CN", langSimplifiedChinese},
		{"zh-cn", langSimplifiedChinese},
		{"ZH-CN", langSimplifiedChinese},
		{"en", langEnglish},
		{"EN", langEnglish},
		{"en-US", langEnglish},
		{"en-gb", langEnglish},
		{"English", langEnglish},
		{"french", langSimplifiedChinese},  // unsupported -> default
		{"klingon", langSimplifiedChinese}, // unsupported -> default
	}
	for _, c := range cases {
		if got := parseLanguage(c.in); got != c.want {
			t.Errorf("parseLanguage(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestLanguageLabelRoundTrip(t *testing.T) {
	for _, l := range []language{langSimplifiedChinese, langEnglish} {
		label := languageLabel(l)
		if got := languageFromLabel(label); got != l {
			t.Errorf("languageFromLabel(languageLabel(%q)) = %q", l, got)
		}
	}
	// Labels are shown in their own script, independent of the current language.
	if languageLabel(langEnglish) != languageLabelEnglish {
		t.Error("English label should be \"English\"")
	}
	if languageLabel(langSimplifiedChinese) != languageLabelChinese {
		t.Error("Chinese label should be \"简体中文\"")
	}
	// An unrecognized label falls back to Chinese.
	if got := languageFromLabel("Deutsch"); got != langSimplifiedChinese {
		t.Errorf("unknown label -> %q, want Chinese", got)
	}
}

func TestMessagesForFallback(t *testing.T) {
	if messagesFor(langEnglish) != &messagesEn {
		t.Error("English should select messagesEn")
	}
	if messagesFor(langSimplifiedChinese) != &messagesZh {
		t.Error("Chinese should select messagesZh")
	}
	// Anything unknown/empty falls back to the default Chinese catalog.
	if messagesFor("") != &messagesZh || messagesFor("xx") != &messagesZh {
		t.Error("unknown language should fall back to messagesZh")
	}
	// englishMessages (used by pure helpers) really is English.
	if englishMessages.Settings != "Settings" {
		t.Errorf("englishMessages.Settings = %q", englishMessages.Settings)
	}
}

// TestConfigLanguageDefaultAndPersist checks that the language choice survives
// a restart and is written to the config file as a stable token.
func TestConfigLanguageDefaultAndPersist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	s := newConfigStore(path)
	if got := s.language(); got != langSimplifiedChinese {
		t.Fatalf("new config language = %q, want Chinese", got)
	}
	s.setLanguage(langEnglish)
	if got := newConfigStore(path).language(); got != langEnglish {
		t.Fatalf("language not persisted: %q", got)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"language": "en"`) {
		t.Fatalf("config file missing persisted language: %s", data)
	}
	// An unsupported value is normalized back to Chinese when stored.
	s.setLanguage(language("french"))
	if got := newConfigStore(path).language(); got != langSimplifiedChinese {
		t.Fatalf("unsupported language should normalize to Chinese, got %q", got)
	}
}

// TestConfigLanguageBackwardCompatible ensures an older config without a
// language field (or with an unknown one) loads as Simplified Chinese instead
// of failing or dumping the user into English.
func TestConfigLanguageBackwardCompatible(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	old := `{
  "appearance": "Dark",
  "recentProjects": [{"path": "/proj/a", "name": "A"}]
}`
	if err := os.WriteFile(path, []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	s := newConfigStore(path)
	if got := s.language(); got != langSimplifiedChinese {
		t.Fatalf("old config without language = %q, want Chinese", got)
	}
	// Existing unrelated fields are preserved through the upgrade.
	if s.appearance() != "Dark" || len(s.recent()) != 1 {
		t.Fatalf("old config fields lost: appearance=%q recents=%v", s.appearance(), s.recent())
	}

	unknown := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(unknown, []byte(`{"language": "de"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := newConfigStore(unknown).language(); got != langSimplifiedChinese {
		t.Fatalf("unknown stored language = %q, want Chinese fallback", got)
	}
}

// TestNewTestManagerUsesEnglish guards the rule that the pre-existing tests keep
// their English wording: the shared test constructor opts into English in memory.
func TestNewTestManagerUsesEnglish(t *testing.T) {
	m, _ := newTestManager(t)
	if got := m.cfg.language(); got != langEnglish {
		t.Fatalf("newTestManager language = %q, want English (old tests assert English wording)", got)
	}
}

// TestChineseWelcomeAndWorkspaceView renders the real views with Simplified
// Chinese and asserts the main UI is Chinese, not English. User-facing controls
// (brand excluded) must come from the catalog.
func TestChineseWelcomeAndWorkspaceView(t *testing.T) {
	m, _ := newTestManager(t)
	m.cfg.cfg.Language = string(langSimplifiedChinese) // in-memory, no disk write
	m.showWelcome("")

	wt := ui.NewTester(m.welcome.view, 720, 480)
	if !wt.HasText("GoRequest") {
		t.Fatalf("brand missing; texts=%v", wt.Texts())
	}
	for _, want := range []string{"打开项目…", "最近项目", "设置"} {
		if !wt.HasText(want) {
			t.Errorf("Chinese welcome missing %q; texts=%v", want, wt.Texts())
		}
	}
	for _, unwanted := range []string{"Open Project…", "Recent Projects", "Settings"} {
		if hasExactLabel(wt, unwanted) {
			t.Errorf("welcome should not show English %q; texts=%v", unwanted, wt.Texts())
		}
	}

	dir := writeProject(t, "中文项目")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	tt := ui.NewTester(app.View, 1200, 820)
	for _, want := range []string{"请求", "文档", "设置", "新建请求"} {
		if !tt.HasText(want) {
			t.Errorf("Chinese workspace missing %q; texts=%v", want, tt.Texts())
		}
	}
	// Close Project and New Doc live in the project menu now.
	if err := tt.Click("中文项目"); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	menuItems := tt.Menu()
	hasClose, hasNewDoc := false, false
	for _, it := range menuItems {
		if it == "关闭项目" {
			hasClose = true
		}
		if it == "新建文档" {
			hasNewDoc = true
		}
	}
	if !hasClose || !hasNewDoc {
		t.Errorf("Chinese project menu missing items: %v", menuItems)
	}
	tt.CloseMenu()
	for _, unwanted := range []string{"Requests", "Documents", "Close Project", "New Request"} {
		if hasExactLabel(tt, unwanted) {
			t.Errorf("workspace should not show English %q; texts=%v", unwanted, tt.Texts())
		}
	}

	// Settings sheet is Chinese too, including the language control.
	if err := tt.Click("设置"); err != nil {
		t.Fatalf("open settings: %v", err)
	}
	for _, want := range []string{"外观", "语言", "启动", "完成"} {
		if !tt.HasText(want) {
			t.Errorf("Chinese settings missing %q; texts=%v", want, tt.Texts())
		}
	}
}

// TestSwitchLanguageViaSettingsPersistsAndUpdatesImmediately drives the real
// language Select in the settings sheet: choosing 简体中文 from English updates
// the UI on the next frame and persists the choice to disk.
func TestSwitchLanguageViaSettingsPersistsAndUpdatesImmediately(t *testing.T) {
	m, _ := newTestManager(t) // English
	dir := writeProject(t, "Switch")
	if err := m.openProject(dir); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	app.showSettings = true

	tt := ui.NewTester(app.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("Settings") || !tt.HasText("Language") {
		t.Fatalf("English settings missing controls; texts=%v", tt.Texts())
	}
	// The language control lives under its own category in the left-rail
	// settings layout (FIX05).
	if err := tt.Click("Language"); err != nil {
		t.Fatalf("open language category: %v", err)
	}
	tt.Frame()
	if !tt.HasText("English") {
		t.Fatalf("English settings missing language control; texts=%v", tt.Texts())
	}

	// Open the language select and choose the Chinese option.
	if err := tt.Click("English"); err != nil {
		t.Fatalf("open language select: %v", err)
	}
	if err := tt.Click("简体中文"); err != nil {
		t.Fatalf("choose Chinese: %v", err)
	}

	if got := m.cfg.language(); got != langSimplifiedChinese {
		t.Fatalf("language after switch = %q, want Chinese", got)
	}
	// Persisted to the shared config file.
	if got := newConfigStore(m.cfg.path).language(); got != langSimplifiedChinese {
		t.Fatalf("switched language not persisted: %q", got)
	}
	// The very next frame already renders Chinese (no restart).
	tt.Frame()
	if !tt.HasText("语言") || !tt.HasText("设置") {
		t.Fatalf("UI did not switch to Chinese immediately; texts=%v", tt.Texts())
	}
	if hasExactLabel(tt, "Language") || hasExactLabel(tt, "Settings") {
		t.Fatalf("old English labels still shown after switch; texts=%v", tt.Texts())
	}
}

// TestLanguageChangeSharedAcrossProjectWindows verifies that multiple project
// windows read the one shared config, so a language change applies to all of
// them without touching their content.
func TestLanguageChangeSharedAcrossProjectWindows(t *testing.T) {
	m, _ := newTestManager(t) // English
	dirA := writeProject(t, "Alpha")
	dirB := writeProject(t, "Beta")
	if err := m.openProject(dirA); err != nil {
		t.Fatal(err)
	}
	if err := m.openProject(dirB); err != nil {
		t.Fatal(err)
	}
	if len(m.workspaces) != 2 {
		t.Fatalf("expected two project windows, got %d", len(m.workspaces))
	}
	render := func(app *App) *ui.Tester {
		tt := ui.NewTester(app.View, 1200, 820)
		tt.Frame()
		return tt
	}

	// Both windows are English to start.
	for _, app := range m.workspaces {
		if tt := render(app); !tt.HasText("Requests") || tt.HasText("请求") {
			t.Fatalf("window should start in English; texts=%v", tt.Texts())
		}
	}

	// Switch the shared config to Chinese: every window follows.
	m.cfg.setLanguage(langSimplifiedChinese)
	for _, app := range m.workspaces {
		if tt := render(app); !tt.HasText("请求") || tt.HasText("Requests") {
			t.Fatalf("window did not follow shared language change; texts=%v", tt.Texts())
		}
	}

	// And back to English.
	m.cfg.setLanguage(langEnglish)
	for _, app := range m.workspaces {
		if tt := render(app); !tt.HasText("Requests") || tt.HasText("请求") {
			t.Fatalf("window did not follow switch back to English; texts=%v", tt.Texts())
		}
	}
}

// TestLanguageChangePreservesDirtyTabsAndEnvDraft ensures switching language is
// purely a presentation change: it must not reload the project or discard dirty
// request tabs or an open environment draft.
func TestLanguageChangePreservesDirtyTabsAndEnvDraft(t *testing.T) {
	m, _ := newTestManager(t)
	if err := m.openProject(writeProject(t, "Stateful")); err != nil {
		t.Fatal(err)
	}
	app := m.workspaces[0]
	ps := app.ps

	entry, err := ps.newRequest()
	if err != nil {
		t.Fatal(err)
	}
	tab := ps.activeTab()
	tab.Dirty = true
	tabsBefore := len(ps.tabs)

	app.openNewEnvironment()
	app.ps.envUI.idText = "draft-env"

	// Switch interface language to Chinese.
	m.cfg.setLanguage(langSimplifiedChinese)

	// Unchanged business state.
	if len(ps.tabs) != tabsBefore {
		t.Fatalf("tabs changed on language switch: %d -> %d", tabsBefore, len(ps.tabs))
	}
	if ps.activeTab() != tab || tab.Entry != entry || !tab.Dirty {
		t.Fatal("language switch dropped the dirty request tab")
	}
	if ps.envUI == nil || !ps.envUI.open || ps.envUI.idText != "draft-env" {
		t.Fatalf("language switch dropped the environment draft: %+v", ps.envUI)
	}

	// The next frame re-renders the still-open draft in Chinese.
	tt := ui.NewTester(app.View, 1200, 820)
	tt.Frame()
	if !tt.HasText("新建环境") {
		t.Fatalf("environment editor not rendered in Chinese; texts=%v", tt.Texts())
	}
	if !ps.envUI.open || ps.envUI.idText != "draft-env" {
		t.Fatal("draft must stay intact after re-render")
	}
}
