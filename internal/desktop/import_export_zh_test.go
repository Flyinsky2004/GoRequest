package desktop

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"

	"gorequest/internal/core"
)

func setInteropTestLanguage(t *testing.T, a *App, l language) {
	t.Helper()
	if a == nil || a.mgr == nil || a.mgr.cfg == nil {
		t.Fatal("app has no config store")
	}
	a.mgr.cfg.cfg.Language = string(l)
}

func interopTexts(tt *ui.Tester) string {
	return strings.Join(tt.Texts(), "\n")
}

func assertHasText(t *testing.T, tt *ui.Tester, want string) {
	t.Helper()
	if !tt.HasText(want) {
		t.Fatalf("missing %q; texts:\n%s", want, interopTexts(tt))
	}
}

func assertNoText(t *testing.T, tt *ui.Tester, unwanted string) {
	t.Helper()
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, unwanted) {
			t.Fatalf("unexpected %q in text %q", unwanted, txt)
		}
	}
}

// TestInteropMessagesCoverEveryField keeps both interop catalogs complete so a
// new field cannot silently render blank in one language.
func TestInteropMessagesCoverEveryField(t *testing.T) {
	typ := reflect.TypeOf(interopMessages{})
	zh := reflect.ValueOf(interopZH)
	en := reflect.ValueOf(interopEN)
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if f.Type.Kind() != reflect.String {
			continue
		}
		if strings.TrimSpace(zh.Field(i).String()) == "" {
			t.Errorf("interopZH.%s is empty", f.Name)
		}
		if strings.TrimSpace(en.Field(i).String()) == "" {
			t.Errorf("interopEN.%s is empty", f.Name)
		}
	}
}

// TestInteropChineseCURLPreviewFromRealParse drives a real cURL parse through
// the rendered import modal and checks the summary, changes, variables and loss
// diagnostics are Chinese while technical codes/paths remain verbatim.
func TestInteropChineseCURLPreviewFromRealParse(t *testing.T) {
	a, _ := openTestProject(t, "Interop ZH cURL")
	setInteropTestLanguage(t, a, langSimplifiedChinese)
	_ = a.InitFileSync()

	a.OpenImportCURLModal()
	a.Interop().CURLInput = `curl -X POST https://api.example.com/v1/charges -H "Authorization: Bearer super_secret_zh_tok_123" -H "Cookie: sid=abc" -d '{"amount":99}' -k`

	tt := ui.NewTester(a.InteropView, 1280, 860)
	tt.Frame()
	assertHasText(t, tt, "导入与变更预览")

	if err := tt.Click(a.interopMsg().PreviewButton); err != nil {
		t.Fatalf("click preview: %v", err)
	}
	tt.Frame()

	assertHasText(t, tt, "可应用")
	assertHasText(t, tt, "cURL 导入：POST https://api.example.com/v1/charges")
	assertHasText(t, tt, "待写入文件变更")
	assertHasText(t, tt, "[新建]")
	assertHasText(t, tt, "转换损失")
	assertHasText(t, tt, "不支持")                   // severity label
	assertHasText(t, tt, "curl-flag-unsupported") // stable machine code
	assertHasText(t, tt, "curl 选项 -k 未导入")
	assertHasText(t, tt, "安全脱敏")
	assertHasText(t, tt, "curl-cookie-scrubbed")
	assertHasText(t, tt, "Cookie 值已脱敏为 Secret 引用")

	// English core wording and raw secrets must not leak into the Chinese UI.
	assertNoText(t, tt, "cURL import:")
	assertNoText(t, tt, "is not stored in shared Request YAML")
	assertNoText(t, tt, "super_secret_zh_tok_123")
	assertNoText(t, tt, "sid=abc")
}

// TestInteropChinesePostmanPreviewConflictAndHotSwitch imports a real Postman
// collection, renders Chinese diagnostics, triggers a real conflict, and then
// switches back to English without losing the original semantics.
func TestInteropChinesePostmanPreviewConflictAndHotSwitch(t *testing.T) {
	a, ps := openTestProject(t, "Interop ZH Postman")
	setInteropTestLanguage(t, a, langSimplifiedChinese)
	_ = a.InitFileSync()

	tmpDir := t.TempDir()
	pmFile := filepath.Join(tmpDir, "orders.postman_collection.json")
	pmContent := `{
  "info": {
    "name": "订单 API",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "item": [
    {
      "name": "列出订单",
      "event": [
        {"listen": "test", "script": {"exec": ["pm.test('ok', function(){});"]}}
      ],
      "response": [{"name": "ok"}],
      "request": {"method": "GET", "url": "https://api.example.com/orders"}
    }
  ]
}`
	if err := os.WriteFile(pmFile, []byte(pmContent), 0o644); err != nil {
		t.Fatalf("write postman file: %v", err)
	}

	a.OpenImportFileModal(pmFile)
	tt := ui.NewTester(a.InteropView, 1280, 860)
	tt.Frame()

	assertHasText(t, tt, "可应用")
	assertHasText(t, tt, "Postman 集合（v2.1.0）")
	assertHasText(t, tt, "项文件变更")
	assertHasText(t, tt, "项损失/警告")
	assertHasText(t, tt, "postman-script-ignored")
	assertHasText(t, tt, "Postman test 脚本")
	assertHasText(t, tt, "postman-saved-response-ignored")
	assertHasText(t, tt, "已省略")
	assertNoText(t, tt, "Postman Collection (v2.1.0)")
	assertNoText(t, tt, "was not imported and never executed")

	if err := tt.Click(a.interopMsg().ConfirmApplyButton); err != nil {
		t.Fatalf("apply postman import: %v", err)
	}
	tt.Frame()
	if _, ok := ps.project.Requests["req-request-1"]; !ok {
		t.Fatalf("expected req-request-1 after import; order=%v", ps.project.Order)
	}

	// Re-preview the same file: default fail policy produces a real conflict.
	if err := tt.Click(a.interopMsg().PreviewButton); err != nil {
		t.Fatalf("click preview again: %v", err)
	}
	tt.Frame()
	assertHasText(t, tt, "已阻止")
	assertHasText(t, tt, "阻塞冲突")
	assertHasText(t, tt, "请求")
	assertHasText(t, tt, "已存在于")
	assertNoText(t, tt, "already exists at")

	// Hot switch to English: the same frame must render the original English
	// catalog and core wording.
	setInteropTestLanguage(t, a, langEnglish)
	tt.Frame()
	assertHasText(t, tt, "BLOCKED")
	assertHasText(t, tt, "Blocking Conflicts")
	assertHasText(t, tt, "already exists at")
	assertHasText(t, tt, "Postman Collection (v2.1.0)")
	assertNoText(t, tt, "已存在于")
}

// TestInteropChinesePortableBundleFromRealExport exports a real portable bundle
// containing a logical local-file slot, imports it into a second project in
// Chinese, and checks the localized summary and loss diagnostics.
func TestInteropChinesePortableBundleFromRealExport(t *testing.T) {
	src, srcPS := openTestProject(t, "Interop ZH Bundle Source")
	_ = src.InitFileSync()

	reqYAML := []byte(`schemaVersion: 1
id: req-upload
name: Upload
protocol: http
http:
  method: POST
  url: "https://api.example.com/upload"
  body:
    type: form-data
    value:
      - name: privateDoc
        kind: file
        localFile: private_cert
`)
	if err := srcPS.project.Store.WriteAtomic("collections/req-upload.request.yaml", reqYAML); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := core.ReloadProjectInMemory(srcPS.project); err != nil {
		t.Fatalf("reload source project: %v", err)
	}

	bundlePath := filepath.Join(t.TempDir(), "workspace.gorequest-bundle.zip")
	if _, _, err := core.ExportPortableBundleToFile(srcPS.project, bundlePath, true, core.PortableExportOptions{Scope: core.ScopeProject}); err != nil {
		t.Fatalf("export portable bundle: %v", err)
	}

	a, _ := openTestProject(t, "Interop ZH Bundle Target")
	setInteropTestLanguage(t, a, langSimplifiedChinese)
	_ = a.InitFileSync()

	a.OpenImportFileModal(bundlePath)
	// The target project already has its own dev environment and README doc;
	// rename-on-conflict keeps the preview applicable while exercising the
	// localized mapping/action rendering.
	a.Interop().ConflictPolicyIdx = 1
	a.StageImportPreview()

	tt := ui.NewTester(a.InteropView, 1280, 860)
	tt.Frame()

	assertHasText(t, tt, "可应用")
	assertHasText(t, tt, "便携包（")
	assertHasText(t, tt, "项文件变更")
	assertHasText(t, tt, "portable-local-slot-binding-required")
	assertHasText(t, tt, "便携包引用了逻辑文件槽位 local:private_cert")
	assertHasText(t, tt, "发送前请在本机绑定本地文件")
	assertNoText(t, tt, "Portable Bundle (")
	assertNoText(t, tt, "bind a local file on this device before sending")
}

// TestInteropUnknownDiagnosticsKeepDetailWithChineseClass checks that unknown
// loss codes and core errors keep their original technical detail while gaining
// a Chinese classification, instead of leaving a bare English sentence.
func TestInteropUnknownDiagnosticsKeepDetailWithChineseClass(t *testing.T) {
	a, _ := openTestProject(t, "Interop ZH Unknown")
	setInteropTestLanguage(t, a, langSimplifiedChinese)

	line := a.displayLossLine(core.LossItem{
		Code:     "vendor-custom-note",
		Severity: core.LossWarning,
		Message:  "vendor parser skipped an unknown construct",
	})
	if !strings.Contains(line, "警告") || !strings.Contains(line, "vendor-custom-note") {
		t.Fatalf("loss line missing localized severity/code: %q", line)
	}
	if !strings.Contains(line, "未收录的转换诊断") || !strings.Contains(line, "vendor parser skipped an unknown construct") {
		t.Fatalf("unknown loss detail not preserved behind Chinese classification: %q", line)
	}

	impErr := a.displayImportError("Read import file: open /tmp/x.json: no such file or directory")
	if !strings.HasPrefix(impErr, "读取导入文件失败：") || !strings.Contains(impErr, "no such file or directory") {
		t.Fatalf("import error not localized while preserving detail: %q", impErr)
	}

	unknown := a.displayImportError("vendor importer exploded")
	if !strings.HasPrefix(unknown, "导入失败：") || !strings.Contains(unknown, "vendor importer exploded") {
		t.Fatalf("unknown import error lost detail/classification: %q", unknown)
	}

	expErr := a.displayExportError("export target \"/tmp/a.zip\" already exists")
	if !strings.Contains(expErr, "导出目标") || !strings.Contains(expErr, "/tmp/a.zip") {
		t.Fatalf("export error not localized: %q", expErr)
	}

	// English mode must return the original core text unchanged.
	setInteropTestLanguage(t, a, langEnglish)
	if got := a.displayLossLine(core.LossItem{Code: "vendor-custom-note", Severity: core.LossWarning, Message: "vendor parser skipped an unknown construct"}); !strings.Contains(got, "vendor parser skipped an unknown construct") || strings.Contains(got, "未收录") {
		t.Fatalf("English loss line changed: %q", got)
	}
	if got := a.displayImportError("Read import file: boom"); got != "Read import file: boom" {
		t.Fatalf("English import error changed: %q", got)
	}
}

// TestInteropMenusFollowLanguage checks the real project and "+" menus render
// the complete Chinese import/export labels and switch back to English.
func TestInteropMenusFollowLanguage(t *testing.T) {
	a, ps := openTestProject(t, "Interop ZH Menus")
	setInteropTestLanguage(t, a, langSimplifiedChinese)

	tt := newHeaderTester(a, ps)
	tt.Frame()
	if err := tt.Click(ps.name()); err != nil {
		t.Fatalf("open project menu: %v", err)
	}
	tt.Frame()
	for _, want := range []string{
		"导入 (cURL / Postman / 便携包)…",
		"从 cURL 导入…",
		"导出 / 便携包…",
		"复制为脱敏 cURL…",
		"撤销上次导入",
	} {
		assertHasText(t, tt, want)
	}
	assertNoText(t, tt, "Import (cURL / Postman / Bundle)…")

	// "+" menu carries the Chinese import entries too.
	setInteropTestLanguage(t, a, langEnglish)
	tt2 := newHeaderTester(a, ps)
	tt2.Frame()
	if err := tt2.Click("+"); err != nil {
		t.Fatalf("open + menu: %v", err)
	}
	tt2.Frame()
	assertHasText(t, tt2, "Import from cURL…")
	assertHasText(t, tt2, "Import (cURL / Postman / Bundle)…")
	assertNoText(t, tt2, "从 cURL 导入…")
}
