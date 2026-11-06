package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
)

func TestDesktopImportModalCURLPreviewApplyAndUndo(t *testing.T) {
	a, ps := openTestProject(t, "Desktop Interop CURL")
	_ = a.InitFileSync()

	a.OpenImportCURLModal()
	st := a.Interop()
	st.ImportTargetFolder = "payments"
	st.CURLInput = `curl -X POST https://api.example.com/v1/charges -H "Authorization: Bearer super_secret_ui_tok_123" -d '{"amount":99}' -k`

	tt := ui.NewTester(a.InteropView, 1280, 860)
	tt.Frame()

	if !tt.HasText("Import & Interoperability Preview") {
		t.Fatalf("import modal title not rendered; texts=%v", tt.Texts())
	}

	if err := tt.Click("Generate Preview"); err != nil {
		t.Fatalf("click Generate Preview: %v", err)
	}
	tt.Frame()

	if !tt.HasText("[READY TO APPLY]") {
		t.Fatalf("expected [READY TO APPLY] badge; texts=%v", tt.Texts())
	}
	// Ensure raw secret never appears anywhere in rendered UI texts
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "super_secret_ui_tok_123") {
			t.Fatalf("raw secret leaked in UI text: %q", txt)
		}
	}

	// Apply the import
	if err := tt.Click("Confirm & Apply Import"); err != nil {
		t.Fatalf("click Confirm & Apply Import: %v", err)
	}
	tt.Frame()

	reqEntry := ps.project.Requests["req-post-v1-charges"]
	if reqEntry == nil {
		t.Fatalf("expected req-post-v1-charges in project after Apply, got %v", ps.project.Order)
	}
	active := ps.activeTab()
	if active == nil || active.Kind != "request" || active.Entry == nil || active.Entry.Request.ID != "req-post-v1-charges" {
		t.Fatalf("expected imported request opened as active tab, got %+v", active)
	}

	// Undo last import via button in modal
	if err := tt.Click("Undo Last Import"); err != nil {
		t.Fatalf("click Undo Last Import: %v", err)
	}
	tt.Frame()

	if _, exists := ps.project.Requests["req-post-v1-charges"]; exists {
		t.Fatalf("expected req-post-v1-charges removed after Undo Last Import")
	}
	if ps.project.Store.Exists("collections/payments/req-post-v1-charges.request.yaml") {
		t.Fatalf("expected request file removed from disk after Undo Last Import")
	}
}

func TestDesktopImportModalPostmanConflictPolicyAndOpenAPITriage(t *testing.T) {
	a, ps := openTestProject(t, "Desktop Interop Postman")
	_ = a.InitFileSync()

	tmpDir := t.TempDir()
	pmFile := filepath.Join(tmpDir, "orders.postman_collection.json")
	pmContent := `{
  "info": {
    "name": "Orders API",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "item": [
    {
      "name": "List Orders",
      "event": [
        {"listen": "test", "script": {"exec": ["pm.test('ok', function(){});"]}}
      ],
      "request": {
        "method": "GET",
        "url": "https://api.example.com/orders"
      }
    }
  ]
}`
	if err := os.WriteFile(pmFile, []byte(pmContent), 0o644); err != nil {
		t.Fatalf("write postman file: %v", err)
	}

	a.OpenImportFileModal(pmFile)
	tt := ui.NewTester(a.InteropView, 1280, 860)
	tt.Frame()

	if !tt.HasText("[READY TO APPLY]") {
		t.Fatalf("expected [READY TO APPLY] for initial Postman preview; texts=%v", tt.Texts())
	}
	if err := tt.Click("Confirm & Apply Import"); err != nil {
		t.Fatalf("apply first Postman import: %v", err)
	}
	tt.Frame()

	if _, ok := ps.project.Requests["req-list-orders"]; !ok {
		t.Fatalf("expected req-list-orders after Postman import")
	}

	// Preview the same file again: default policy is Block on Conflict -> [BLOCKED]
	if err := tt.Click("Generate Preview"); err != nil {
		t.Fatalf("click Generate Preview second time: %v", err)
	}
	tt.Frame()
	if !tt.HasText("[BLOCKED]") {
		t.Fatalf("expected [BLOCKED] on duplicate import under default fail policy; texts=%v", tt.Texts())
	}

	// Switch conflict policy to "Copy with New ID"
	if err := tt.Click("Copy with New ID"); err != nil {
		t.Fatalf("click Copy with New ID: %v", err)
	}
	tt.Frame()
	if !tt.HasText("[READY TO APPLY]") {
		t.Fatalf("expected [READY TO APPLY] after switching to Copy with New ID; texts=%v", tt.Texts())
	}
	if err := tt.Click("Confirm & Apply Import"); err != nil {
		t.Fatalf("apply duplicate copy import: %v", err)
	}
	tt.Frame()
	if _, ok := ps.project.Requests["req-list-orders-copy"]; !ok {
		t.Fatalf("expected req-list-orders-copy created; got %v", ps.project.Order)
	}

	// Now preview an OpenAPI 3.1 file and verify A03 routing notice
	oaFile := filepath.Join(tmpDir, "openapi.yaml")
	if err := os.WriteFile(oaFile, []byte("openapi: 3.1.0\ninfo:\n  title: Demo\n  version: 1.0.0\npaths: {}\n"), 0o644); err != nil {
		t.Fatalf("write openapi file: %v", err)
	}
	a.OpenImportFileModal(oaFile)
	tt.Frame()
	if !tt.HasText("[BLOCKED]") {
		t.Fatalf("expected [BLOCKED] for OpenAPI file; texts=%v", tt.Texts())
	}
	foundA03 := false
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "openapi-routed-to-a03") || strings.Contains(txt, "A03") {
			foundA03 = true
			break
		}
	}
	if !foundA03 {
		t.Fatalf("expected A03 routing diagnostic in UI; texts=%v", tt.Texts())
	}
}

func TestDesktopExportModalRedactedCURLPortableBundleAndOverwriteGuard(t *testing.T) {
	a, ps := openTestProject(t, "Desktop Export Source")
	_ = a.InitFileSync()

	// Create a request with a secret bearer token via cURL import first
	a.OpenImportCURLModal()
	a.Interop().CURLInput = `curl -X POST https://api.example.com/v1/users -H "Authorization: Bearer secret_export_tok_555" -d '{"user":"bob"}'`
	a.StageImportPreview()
	a.ConfirmAndApplyImport()
	a.Interop().ImportOpen = false

	// Open Export modal in Redacted cURL mode
	a.OpenExportCURLForActiveRequest()
	tt := ui.NewTester(a.InteropView, 1280, 860)
	tt.Frame()

	if !tt.HasText("Export & Portable Package") {
		t.Fatalf("export modal title missing; texts=%v", tt.Texts())
	}
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "secret_export_tok_555") {
			t.Fatalf("raw secret leaked in Export modal preview: %q", txt)
		}
	}
	if err := tt.Click("Copy Redacted Output"); err != nil {
		t.Fatalf("click Copy Redacted Output: %v", err)
	}
	tt.Frame()

	// Switch to Project Bundle export and write to a file
	if err := tt.Click("Project Bundle"); err != nil {
		t.Fatalf("click Project Bundle: %v", err)
	}
	tt.Frame()

	bundlePath := filepath.Join(t.TempDir(), "workspace.gorequest-bundle.zip")
	a.Interop().ExportOutputPath = bundlePath
	if err := tt.Click("Export to File"); err != nil {
		t.Fatalf("click Export to File: %v", err)
	}
	tt.Frame()

	if _, err := os.Stat(bundlePath); err != nil {
		t.Fatalf("expected bundle file written at %s: %v", bundlePath, err)
	}

	// Second click without Allow Overwrite must report an error and not silently overwrite
	if err := tt.Click("Export to File"); err != nil {
		t.Fatalf("click Export to File second time: %v", err)
	}
	tt.Frame()
	if !strings.Contains(a.Interop().ExportErr, "already exists") {
		t.Fatalf("expected overwrite guard error, got %q", a.Interop().ExportErr)
	}

	// Enable overwrite and export again
	a.Interop().ExportOverwrite = true
	if err := tt.Click("Export to File"); err != nil {
		t.Fatalf("click Export to File with overwrite: %v", err)
	}
	tt.Frame()
	if a.Interop().ExportErr != "" {
		t.Fatalf("unexpected error with ExportOverwrite=true: %s", a.Interop().ExportErr)
	}

	// Verify the exported bundle imports cleanly into a second project
	a2, ps2 := openTestProject(t, "Desktop Export Target")
	a2.OpenImportFileModal(bundlePath)
	a2.Interop().ConflictPolicyIdx = 2 // Overwrite default dev env
	a2.StageImportPreview()
	a2.ConfirmAndApplyImport()
	if _, ok := ps2.project.Requests["req-post-v1-users"]; !ok {
		t.Fatalf("expected req-post-v1-users imported into second project from bundle, got %v (orig=%v)", ps2.project.Order, ps.project.Order)
	}
}
