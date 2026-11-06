package desktop

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"gopkg.in/yaml.v3"

	"gorequest/internal/core"
)

func TestDesktopOpenAPIImportMigrationStrictLossAndUndo(t *testing.T) {
	a, ps := openTestProject(t, "Desktop OpenAPI Import")
	_ = a.InitFileSync()

	tmpDir := t.TempDir()
	specFile := filepath.Join(tmpDir, "payments-openapi.yaml")
	specContent := `openapi: 3.1.0
info:
  title: Payments Service
  version: 1.2.0
servers:
  - url: https://api.payments.example.com/v1
paths:
  /charges:
    post:
      operationId: createCharge
      summary: Create a payment charge
      tags:
        - billing
      callbacks:
        onChargeComplete:
          "{$request.body#/webhookUrl}":
            post:
              responses:
                "200":
                  description: Webhook ack
      parameters:
        - name: Authorization
          in: header
          schema:
            type: string
          example: "Bearer super_secret_openapi_ui_999"
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              required: [amount]
              properties:
                amount:
                  type: integer
      responses:
        "201":
          description: Created charge
          content:
            application/json:
              schema:
                type: object
                required: [id]
                properties:
                  id:
                    type: string
`
	if err := os.WriteFile(specFile, []byte(specContent), 0o644); err != nil {
		t.Fatalf("write openapi spec: %v", err)
	}

	a.OpenOpenAPIImportFile(specFile)
	st := a.OpenAPI()
	st.TargetFolderRel = "billing"
	st.StrictLossReject = true

	tt := ui.NewTester(a.OpenAPIView, 1280, 900)
	tt.Frame()

	if !tt.HasText("OpenAPI 3.1 / 3.2 & JSON Schema Studio") {
		t.Fatalf("expected OpenAPI modal title; texts=%v", tt.Texts())
	}

	// With StrictLossReject=true and x-code-samples present, preview must be [BLOCKED]
	a.StageOpenAPIPreview()
	tt.Frame()
	if !tt.HasText("[BLOCKED]") {
		t.Fatalf("expected [BLOCKED] when StrictLossReject is enabled and lossy extension exists; texts=%v", tt.Texts())
	}

	// Disable StrictLossReject and regenerate preview
	st.StrictLossReject = false
	if err := tt.Click("Stage OpenAPI Preview"); err != nil {
		t.Fatalf("click Stage OpenAPI Preview: %v", err)
	}
	tt.Frame()

	if !tt.HasText("[READY TO APPLY]") {
		t.Fatalf("expected [READY TO APPLY] after disabling StrictLossReject; texts=%v", tt.Texts())
	}

	// Ensure raw secret never leaks in rendered UI text
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "super_secret_openapi_ui_999") {
			t.Fatalf("raw secret leaked in OpenAPI UI preview: %q", txt)
		}
	}

	// Confirm and apply OpenAPI import
	if err := tt.Click("Confirm & Apply to Project"); err != nil {
		t.Fatalf("click Confirm & Apply to Project: %v", err)
	}
	tt.Frame()

	reqEntry := ps.project.Requests["create-charge"]
	if reqEntry == nil {
		t.Fatalf("expected create-charge in project after OpenAPI apply, got %v", ps.project.Order)
	}
	// Read the persisted file directly: the baseline file-sync decode path strips
	// top-level `extensions` from the in-memory entity (see A03.md, integration step
	// for file_sync.go), but the YAML on disk must carry the contract.
	raw, rerr := os.ReadFile(filepath.Join(ps.project.Store.Root, reqEntry.RelPath))
	if rerr != nil {
		t.Fatalf("read imported request file: %v", rerr)
	}
	var onDisk core.Request
	if err := yaml.Unmarshal(raw, &onDisk); err != nil {
		t.Fatalf("parse imported request file: %v", err)
	}
	contract, err := core.GetRequestOpenAPI(&onDisk)
	if err != nil || contract == nil || contract.OperationID != "createCharge" {
		t.Fatalf("expected OperationContract persisted on disk, got %+v (err=%v)\n%s", contract, err, raw)
	}
	if strings.Contains(string(raw), "super_secret_openapi_ui_999") {
		t.Fatalf("raw secret persisted in imported request YAML:\n%s", raw)
	}

	// Undo last OpenAPI import
	if err := tt.Click("Undo Last OpenAPI Apply"); err != nil {
		t.Fatalf("click Undo Last OpenAPI Apply: %v", err)
	}
	tt.Frame()

	if _, exists := ps.project.Requests["create-charge"]; exists {
		t.Fatalf("expected create-charge removed after Undo Last OpenAPI Apply")
	}
}

func TestDesktopOpenAPIExternalSpecModeAndThreeWaySync(t *testing.T) {
	a, ps := openTestProject(t, "Desktop OpenAPI Sync")
	_ = a.InitFileSync()

	specFile := filepath.Join(ps.project.Store.Root, "external-api.yaml")
	specV1 := `openapi: 3.1.0
info:
  title: Accounts API
  version: 1.0.0
servers:
  - url: https://api.accounts.example.com
paths:
  /accounts/{id}:
    get:
      operationId: getAccount
      summary: Get account by ID
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "200":
          description: OK
`
	if err := os.WriteFile(specFile, []byte(specV1), 0o644); err != nil {
		t.Fatalf("write spec v1: %v", err)
	}

	a.OpenOpenAPIImportFile(specFile)
	st := a.OpenAPI()
	st.SourceModeIdx = 1 // External Spec Mode
	a.StageOpenAPIPreview()
	a.ConfirmAndApplyOpenAPI()

	reqEntry := ps.project.Requests["get-account"]
	if reqEntry == nil {
		t.Fatalf("expected get-account imported, got %v", ps.project.Order)
	}

	for _, tab := range append([]*Tab(nil), ps.tabs...) {
		ps.removeTab(tab)
	}

	// Simulate local non-conflicting customization (e.g. custom execution header and timeout)
	reqEntry.Request.Timeout = "15s"
	reqEntry.Request.HTTP.Headers = append(reqEntry.Request.HTTP.Headers, core.KV{Name: "X-Local-Debug", Value: "1"})
	contract, err := core.GetRequestOpenAPI(reqEntry.Request)
	if err != nil {
		t.Fatalf("GetRequestOpenAPI: %v", err)
	}
	if _, err := core.SaveRequestWithOpenAPI(ps.project, reqEntry, reqEntry.Request, contract, reqEntry.BaselineDigest); err != nil {
		t.Fatalf("save local customization: %v", err)
	}

	// Update external spec on disk (v2 adds a query param and updates summary)
	specV2 := `openapi: 3.1.0
info:
  title: Accounts API
  version: 1.1.0
servers:
  - url: https://api.accounts.example.com
paths:
  /accounts/{id}:
    get:
      operationId: getAccount
      summary: Get account details v2
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: expand
          in: query
          schema:
            type: boolean
      responses:
        "200":
          description: Account details
`
	if err := os.WriteFile(specFile, []byte(specV2), 0o644); err != nil {
		t.Fatalf("write spec v2: %v", err)
	}

	st.SyncActionIdx = 1 // 3-Way Sync Preview
	tt := ui.NewTester(a.OpenAPIView, 1280, 900)
	tt.Frame()

	if err := tt.Click("Stage OpenAPI Preview"); err != nil {
		t.Fatalf("click Stage OpenAPI Preview for 3-way sync: %v", err)
	}
	tt.Frame()

	if st.SyncPreview == nil || st.StagedPlan == nil || !st.StagedPlan.Preview().CanApply {
		t.Fatalf("expected 3-way sync preview ready to apply, err=%q", st.ImportErr)
	}

	if err := tt.Click("Confirm & Apply to Project"); err != nil {
		t.Fatalf("apply 3-way sync: %v", err)
	}
	tt.Frame()

	updated := ps.project.Requests["get-account"]
	if updated == nil {
		t.Fatalf("expected get-account after sync")
	}
	if updated.Request.Timeout != "15s" {
		t.Fatalf("expected local Timeout=15s preserved across 3-way sync, got %q", updated.Request.Timeout)
	}
	hasLocalHeader := false
	for _, h := range updated.Request.HTTP.Headers {
		if h.Name == "X-Local-Debug" && h.Value == "1" {
			hasLocalHeader = true
			break
		}
	}
	if !hasLocalHeader {
		t.Fatalf("expected local X-Local-Debug header preserved across 3-way sync, got %+v", updated.Request.HTTP.Headers)
	}
}

func TestDesktopOpenAPIExport31Vs32StrictDowngradeAndCoverageReport(t *testing.T) {
	a, ps := openTestProject(t, "Desktop OpenAPI Export")
	_ = a.InitFileSync()

	// Add an HTTP request using OpenAPI 3.2 QUERY method and a non-HTTP request (grpc)
	queryReq := &core.Request{
		SchemaVersion: 1,
		ID:            "req-search-query",
		Name:          "Search via HTTP QUERY",
		Protocol:      "http",
		HTTP: &core.HTTPDef{
			Method: "QUERY",
			URL:    "https://api.example.com/v1/search",
		},
	}
	queryEntry := &core.RequestEntry{
		RelPath: "collections/search.request.yaml",
		Request: queryReq,
	}
	queryContract := &core.OperationContract{
		OperationID:  "querySearch",
		PathTemplate: "/v1/search",
		Summary:      "Structured search using HTTP QUERY",
		Responses: map[string]core.ResponseContract{
			"200": {Description: "Search results"},
		},
	}
	if _, err := core.SaveRequestWithOpenAPI(ps.project, queryEntry, queryReq, queryContract, ""); err != nil {
		t.Fatalf("save query request: %v", err)
	}
	ps.project.Requests[queryEntry.Request.ID] = queryEntry
	ps.project.Order = append(ps.project.Order, queryEntry.Request.ID)

	grpcEntry := &core.RequestEntry{
		RelPath: "collections/grpc-ping.request.yaml",
		Request: &core.Request{
			SchemaVersion: 1,
			ID:            "req-grpc-ping",
			Name:          "gRPC Health Check",
			Protocol:      "grpc",
			HTTP: &core.HTTPDef{
				Method: "POST",
				URL:    "grpc://localhost:50051/health.Health/Check",
			},
		},
	}
	ps.project.Requests[grpcEntry.Request.ID] = grpcEntry
	ps.project.Order = append(ps.project.Order, grpcEntry.Request.ID)

	a.OpenOpenAPIModal(OpenAPITabExportCoverage)
	st := a.OpenAPI()
	tt := ui.NewTester(a.OpenAPIView, 1280, 900)
	tt.Frame()

	// Default target is OpenAPI 3.1.2 with ExportStrict=true and ExportAllowLossy=false:
	// should reject QUERY method downgrade
	if !strings.Contains(st.ExportErr, "lossy") && !strings.Contains(st.ExportErr, "3.2") {
		t.Fatalf("expected strict 3.2 -> 3.1 downgrade error for QUERY method, got %q", st.ExportErr)
	}

	// Switch to OpenAPI 3.2.1: should succeed without loss
	if err := tt.Click("OpenAPI 3.2.1"); err != nil {
		t.Fatalf("click OpenAPI 3.2.1: %v", err)
	}
	tt.Frame()
	if st.ExportErr != "" {
		t.Fatalf("expected clean OpenAPI 3.2.1 export, got err=%q", st.ExportErr)
	}
	if !strings.Contains(st.ExportPreviewText, "3.2.1") {
		t.Fatalf("expected 3.2.1 in exported YAML, got:\n%s", st.ExportPreviewText)
	}

	// Verify Coverage Report excludes non-HTTP request from denominator
	if st.CoverageReport == nil {
		t.Fatalf("expected CoverageReport in OpenAPIState")
	}
	if st.CoverageReport.NotApplicableCount != 1 {
		t.Fatalf("expected 1 excluded non-HTTP request (grpc), got %d", st.CoverageReport.NotApplicableCount)
	}

	// Export to file and verify overwrite protection
	outPath := filepath.Join(t.TempDir(), "exported-openapi-3.2.yaml")
	st.ExportOutputPath = outPath
	if err := tt.Click("Export OpenAPI File"); err != nil {
		t.Fatalf("click Export OpenAPI File: %v", err)
	}
	tt.Frame()
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected exported spec file at %s: %v", outPath, err)
	}
}

func TestDesktopOpenAPIContractInspectorAndLiveResponseSchemaValidator(t *testing.T) {
	a, ps := openTestProject(t, "Desktop OpenAPI Validator")
	_ = a.InitFileSync()

	// Create and open an HTTP request tab
	req := &core.Request{
		SchemaVersion: 1,
		ID:            "req-get-order",
		Name:          "Get Order",
		Protocol:      "http",
		HTTP: &core.HTTPDef{
			Method: "GET",
			URL:    "https://api.example.com/v1/orders/42",
		},
	}
	reqEntry := &core.RequestEntry{
		RelPath: "collections/get-order.request.yaml",
		Request: req,
	}
	if _, err := core.SaveRequestWithOpenAPI(ps.project, reqEntry, req, nil, ""); err != nil {
		t.Fatalf("save initial request: %v", err)
	}
	ps.project.Requests[reqEntry.Request.ID] = reqEntry
	ps.project.Order = append(ps.project.Order, reqEntry.Request.ID)
	ps.openRequest(reqEntry)

	// Open the Contract & Response Validator modal tab
	a.OpenOpenAPIModal(OpenAPITabContractValidator)
	st := a.OpenAPI()
	st.ContractOpID = "getOrder"
	st.ContractPathTmpl = "/v1/orders/{id}"
	st.ContractSummary = "Fetch a single order"
	st.DraftStatusKey = "200"
	st.DraftMediaType = "application/json"
	st.DraftSchemaJSON = `{
  "type": "object",
  "required": ["data"],
  "properties": {
    "data": {
      "type": "object",
      "required": ["id"],
      "properties": {
        "id": {"type": "integer"}
      }
    }
  }
}`

	tt := ui.NewTester(a.OpenAPIView, 1280, 900)
	tt.Frame()

	// Stage the 200 response contract and save to request YAML
	if err := tt.Click("Stage Response Contract Entry"); err != nil {
		t.Fatalf("click Stage Response Contract Entry: %v", err)
	}
	tt.Frame()

	if err := tt.Click("Save Operation Contract to Request YAML"); err != nil {
		t.Fatalf("click Save Operation Contract to Request YAML: %v", err)
	}
	tt.Frame()

	// Simulate a live HTTP 200 response on the active tab where $.data.id is a string instead of integer
	activeTab := ps.activeTab()
	if activeTab == nil {
		t.Fatalf("expected active request tab")
	}
	activeTab.Resp = &core.ExecResult{
		Status:  200,
		Headers: [][2]string{{"Content-Type", "application/json; charset=utf-8"}},
		Body:    []byte(`{"data":{"id":"wrong-type-string"}}`),
	}

	if err := tt.Click("Load Latest Live Response"); err != nil {
		t.Fatalf("click Load Latest Live Response: %v", err)
	}
	tt.Frame()

	if err := tt.Click("Validate Against Contract Schema"); err != nil {
		t.Fatalf("click Validate Against Contract Schema: %v", err)
	}
	tt.Frame()

	if !tt.HasText("[SCHEMA VIOLATIONS]") {
		t.Fatalf("expected [SCHEMA VIOLATIONS] badge; texts=%v", tt.Texts())
	}
	foundInstancePath := false
	for _, txt := range tt.Texts() {
		if strings.Contains(txt, "$.data.id") && strings.Contains(txt, "/data/id") {
			foundInstancePath = true
			break
		}
	}
	if !foundInstancePath {
		t.Fatalf("expected exact instance path $.data.id (/data/id) in validation errors; texts=%v", tt.Texts())
	}

	// Now fix the payload so $.data.id is an integer and validate again
	st.TestBodyJSON = `{"data":{"id":42}}`
	if err := tt.Click("Validate Against Contract Schema"); err != nil {
		t.Fatalf("click Validate Against Contract Schema (valid payload): %v", err)
	}
	tt.Frame()

	if !tt.HasText("[SCHEMA VALID]") {
		t.Fatalf("expected [SCHEMA VALID] badge; texts=%v", tt.Texts())
	}
}
