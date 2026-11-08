package desktop

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// openChineseTestProject opens a fresh project whose interface language is
// Simplified Chinese. newTestManager opts into English for the older tests, so
// this helper flips the in-memory preference before the first frame.
func openChineseTestProject(t *testing.T, name string) (*App, *projectState) {
	t.Helper()
	m, _ := newTestManager(t)
	m.cfg.cfg.Language = string(langSimplifiedChinese)
	dir := writeProject(t, name)
	if err := m.openProject(dir); err != nil {
		t.Fatalf("openProject: %v", err)
	}
	a := m.workspaces[0]
	return a, a.ps
}

// projectMenuChineseLabels lists the Simplified Chinese labels every
// U21-owned project-menu group must show. The interop entries are owned by U22
// and deliberately excluded here.
func projectMenuChineseLabels() []string {
	return []string{
		"打开项目…",
		"新建文档",
		"移动…",
		"请求代码片段…",
		"OpenAPI / Swagger 导入与同步…",
		"导出 OpenAPI 3.1 / 3.2 与覆盖率…",
		"OpenAPI 契约与 Schema 校验器…",
		"撤销上次 OpenAPI 变更",
		"集合 / Suite 运行器",
		"本地 Mock 服务...",
		"性能诊断与采样…",
		"管理鉴权方案",
		"文件夹鉴权",
		"秘密与存储",
		"诊断",
		"复制项目路径",
		"关闭项目",
	}
}

// TestProjectMenuIsChineseForEveryAppendedGroup is the U21 acceptance test: the
// project-name dropdown and every appended Build*ProjectMenuItems group render
// in Simplified Chinese, not just the trigger button.
func TestProjectMenuIsChineseForEveryAppendedGroup(t *testing.T) {
	a, ps := openChineseTestProject(t, "中文菜单项目")
	tt := ui.NewTester(a.View, 1200, 900)
	tt.Frame()

	if err := tt.Click(ps.name()); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	tt.Frame()
	if got := tt.Menu(); got != nil {
		t.Fatalf("the system showed a native menu: %v", got)
	}

	for _, want := range projectMenuChineseLabels() {
		if !tt.HasText(want) {
			t.Errorf("Chinese project menu missing %q; texts=%v", want, tt.Texts())
		}
	}

	// The English counterparts of the same groups must be gone.
	for _, unwanted := range []string{
		"Open Project…",
		"New Doc",
		"Move…",
		"Request Code Snippet…",
		"OpenAPI / Swagger Import & Sync…",
		"Collection / Suite Runner",
		"Local Mock Server...",
		"Performance Diagnostics…",
		"Manage auth schemes",
		"Copy Project Path",
		"Close Project",
	} {
		if hasExactLabel(tt, unwanted) {
			t.Errorf("Chinese project menu still shows English %q; texts=%v", unwanted, tt.Texts())
		}
	}
}

// TestProjectMenuSwitchesLanguageImmediately changes the persisted preference
// while the dropdown is open and checks the next frame is English without
// reopening the window.
func TestProjectMenuSwitchesLanguageImmediately(t *testing.T) {
	a, ps := openChineseTestProject(t, "菜单切换")
	tt := ui.NewTester(a.View, 1200, 900)
	tt.Frame()

	if err := tt.Click(ps.name()); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	tt.Frame()
	if !tt.HasText("性能诊断与采样…") {
		t.Fatalf("Chinese menu missing performance entry; texts=%v", tt.Texts())
	}

	a.mgr.cfg.cfg.Language = string(langEnglish)
	tt.Frame()
	if !tt.HasText("Performance Diagnostics…") {
		t.Fatalf("English menu missing performance entry after switch; texts=%v", tt.Texts())
	}
	if hasExactLabel(tt, "性能诊断与采样…") {
		t.Fatalf("Chinese entry survived the language switch; texts=%v", tt.Texts())
	}

	a.mgr.cfg.cfg.Language = string(langSimplifiedChinese)
	tt.Frame()
	if !tt.HasText("性能诊断与采样…") {
		t.Fatalf("Chinese menu did not return after switching back; texts=%v", tt.Texts())
	}
}

// TestProjectMenuChineseActionsStillWork proves the translated entries are not
// inert: choosing the Chinese performance entry opens the diagnostics modal
// with its Chinese controls.
func TestProjectMenuChineseActionsStillWork(t *testing.T) {
	a, ps := openChineseTestProject(t, "菜单动作")
	tt := ui.NewTester(a.View, 1200, 900)
	tt.Frame()

	if err := tt.Click(ps.name()); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	tt.Frame()
	if err := tt.Click("性能诊断与采样…"); err != nil {
		t.Fatalf("choose Chinese performance entry: %v", err)
	}
	tt.Frame()
	if !tt.HasText("运行保存快照诊断") {
		t.Fatalf("Chinese performance modal did not open; texts=%v", tt.Texts())
	}
	if hasExactLabel(tt, "Run Diagnostics Snapshot") {
		t.Fatalf("performance modal rendered English in Chinese mode; texts=%v", tt.Texts())
	}
}

// TestProjectMenuEnglishIsPreserved keeps the English catalog reachable: the
// same groups still render their English labels when English is selected.
func TestProjectMenuEnglishIsPreserved(t *testing.T) {
	a, ps := openTestProject(t, "EnglishMenu")
	tt := ui.NewTester(a.View, 1200, 900)
	tt.Frame()

	if err := tt.Click(ps.name()); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	tt.Frame()
	for _, want := range []string{
		"Open Project…",
		"New Doc",
		"Request Code Snippet…",
		"OpenAPI / Swagger Import & Sync…",
		"Collection / Suite Runner",
		"Local Mock Server...",
		"Performance Diagnostics…",
		"Manage auth schemes",
		"Copy Project Path",
		"Close Project",
	} {
		if !tt.HasText(want) {
			t.Errorf("English project menu missing %q; texts=%v", want, tt.Texts())
		}
	}
}
