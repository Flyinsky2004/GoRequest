package core

import (
	"archive/zip"
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCURLImportPreviewApplyRollbackAndSecretScrubbing(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewProject(tmp, "CURL Test Project")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	rawCURL := `curl -X POST 'https://api.example.com/v1/orders?region=ap&tag=a&tag=b' \
  -H 'Authorization: Bearer sk_live_super_secret_987654' \
  -H 'X-Api-Key: ak_secret_11223344' \
  -H 'Content-Type: application/json' \
  -d '{"item":"book","qty":2}' \
  --compressed -k`

	plan, err := StageCURLImport(p, rawCURL, ImportOptions{
		TargetFolderRel:    "orders",
		DefaultRequestName: "Create Order",
	})
	if err != nil {
		t.Fatalf("StageCURLImport: %v", err)
	}

	prev := plan.Preview()
	if !prev.CanApply {
		t.Fatalf("expected CanApply=true, got false (conflicts=%v, losses=%v)", prev.Conflicts, prev.Losses)
	}
	if prev.Format != FormatCURL {
		t.Fatalf("expected format %q, got %q", FormatCURL, prev.Format)
	}
	// Verify raw secrets never appear in summary or loss messages
	if strings.Contains(prev.Summary, "sk_live_super_secret_987654") || strings.Contains(prev.Summary, "ak_secret_11223344") {
		t.Fatalf("raw secret leaked in preview summary: %s", prev.Summary)
	}
	if len(prev.SecretBindings) != 2 {
		t.Fatalf("expected 2 secret bindings, got %v", prev.SecretBindings)
	}
	hasInsecureLoss := false
	for _, l := range prev.Losses {
		if l.Code == "curl-flag-unsupported" && l.Field == "-k" {
			hasInsecureLoss = true
		}
		if strings.Contains(l.Message, "sk_live_super_secret_987654") || strings.Contains(l.Message, "ak_secret_11223344") {
			t.Fatalf("raw secret leaked in loss message: %+v", l)
		}
	}
	if !hasInsecureLoss {
		t.Fatalf("expected unsupported -k loss item, got %+v", prev.Losses)
	}

	// Verify nothing written to disk before Apply()
	reqRel := "collections/orders/req-create-order.request.yaml"
	if p.Store.Exists(reqRel) {
		t.Fatalf("request file %s should not exist on disk before Apply()", reqRel)
	}

	receipt, err := plan.Apply()
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Verify file exists on disk and contains {{secret.*}} references, NOT plaintext secrets
	rawDisk, _, err := p.Store.Read(reqRel)
	if err != nil {
		t.Fatalf("read applied request: %v", err)
	}
	diskStr := string(rawDisk)
	if strings.Contains(diskStr, "sk_live_super_secret_987654") || strings.Contains(diskStr, "ak_secret_11223344") {
		t.Fatalf("raw secret written to request YAML on disk:\n%s", diskStr)
	}
	if !strings.Contains(diskStr, "{{secret.bearer_token}}") {
		t.Fatalf("expected {{secret.bearer_token}} in request YAML:\n%s", diskStr)
	}

	// Verify in-memory project was updated with the new request and secret slot declarations
	entry, ok := p.Requests["req-create-order"]
	if !ok || entry == nil {
		t.Fatalf("expected req-create-order in p.Requests after Apply()")
	}
	if entry.Request.HTTP.Method != "POST" || entry.Request.HTTP.URL != "https://api.example.com/v1/orders" {
		t.Fatalf("unexpected HTTP method/URL: %+v", entry.Request.HTTP)
	}
	if len(entry.Request.HTTP.Query) != 3 {
		t.Fatalf("expected 3 query params (preserving duplicate tag), got %+v", entry.Request.HTTP.Query)
	}

	// Test ExportRedactedCURL on the imported request
	curlOut, err := ExportRedactedCURL(p, entry, "dev", MapSecrets{
		"bearer_token": "sk_live_super_secret_987654",
		"x_api_key":    "ak_secret_11223344",
	})
	if err != nil {
		t.Fatalf("ExportRedactedCURL: %v", err)
	}
	if strings.Contains(curlOut.Command, "sk_live_super_secret_987654") || strings.Contains(curlOut.Command, "ak_secret_11223344") {
		t.Fatalf("ExportRedactedCURL leaked secret value:\n%s", curlOut.Command)
	}
	if !strings.Contains(curlOut.Command, "{{secret.bearer_token}}") {
		t.Fatalf("expected {{secret.bearer_token}} placeholder in exported curl:\n%s", curlOut.Command)
	}

	// Rollback and verify both disk and in-memory state are restored
	if err := receipt.Rollback(); err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if p.Store.Exists(reqRel) {
		t.Fatalf("request file %s still exists after Rollback()", reqRel)
	}
	if _, stillThere := p.Requests["req-create-order"]; stillThere {
		t.Fatalf("req-create-order still in p.Requests after Rollback()")
	}
}

func TestCURLMultipartGetBasicAuthAndShellInjectionBlocking(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewProject(tmp, "CURL Forms")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	// 1. Multipart form + Basic auth + host file (@/etc/passwd converted to local:passwd)
	cmdForm := `curl -u admin:top_secret_pw -F "title=Report" -F "upload=@/var/private/invoice.pdf;type=application/pdf" https://files.example.com/upload`
	plan, err := StageCURLImport(p, cmdForm, ImportOptions{})
	if err != nil {
		t.Fatalf("StageCURLImport multipart: %v", err)
	}
	prev := plan.Preview()
	if !prev.CanApply {
		t.Fatalf("expected CanApply=true for multipart curl, got false")
	}
	if len(prev.LocalFileSlots) != 1 || prev.LocalFileSlots[0] != "invoice" {
		t.Fatalf("expected local file slot [invoice], got %v", prev.LocalFileSlots)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatalf("Apply multipart: %v", err)
	}
	entry := p.Requests[plan.ImportedRequestIDs()[0]]
	if entry == nil || entry.Request.Auth == nil || entry.Request.Auth.Inline == nil {
		t.Fatalf("expected inline basic auth on imported request")
	}
	if entry.Request.Auth.Inline.Password != "{{secret.basic_password}}" {
		t.Fatalf("expected basic password to be {{secret.basic_password}}, got %q", entry.Request.Auth.Inline.Password)
	}
	tb, err := InspectBody(entry.Request.HTTP.Body)
	if err != nil || tb.Kind != BodyFormData || len(tb.MultipartParts) != 2 {
		t.Fatalf("expected form-data body with 2 parts, got %+v (err=%v)", tb, err)
	}
	if tb.MultipartParts[1].LocalFile != "invoice" || tb.MultipartParts[1].File != "" {
		t.Fatalf("expected host file converted to LocalFile='invoice', got %+v", tb.MultipartParts[1])
	}

	// 2. -G / --get with --data-urlencode
	cmdGet := `curl -G https://search.example.com/items -d "category=books" --data-urlencode "q=go lang"`
	parsedGet, err := ParseCURLCommand(cmdGet, ImportOptions{})
	if err != nil {
		t.Fatalf("ParseCURLCommand -G: %v", err)
	}
	if parsedGet.Request.HTTP.Method != "GET" || len(parsedGet.Request.HTTP.Query) != 2 {
		t.Fatalf("expected GET with 2 query params, got method=%s query=%+v", parsedGet.Request.HTTP.Method, parsedGet.Request.HTTP.Query)
	}

	// 3. Shell injection constructs must NEVER execute and must block Apply
	markerFile := filepath.Join(tmp, "pwned_marker")
	injections := []string{
		"curl https://example.com/$(touch " + markerFile + ")",
		"curl \"https://example.com/`touch " + markerFile + "`\"",
		"curl https://example.com ; touch " + markerFile,
		"curl https://example.com | sh",
		"curl https://example.com > " + markerFile,
	}
	for _, inj := range injections {
		injPlan, err := StageCURLImport(p, inj, ImportOptions{})
		if err != nil {
			t.Fatalf("StageCURLImport injection %q unexpected error: %v", inj, err)
		}
		if injPlan.Preview().CanApply {
			t.Fatalf("expected CanApply=false for shell injection %q", inj)
		}
		if _, err := injPlan.Apply(); !errors.Is(err, ErrImportBlocked) {
			t.Fatalf("expected ErrImportBlocked on Apply for %q, got %v", inj, err)
		}
		if _, statErr := os.Stat(markerFile); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("shell command was executed! marker file exists for %q", inj)
		}
	}
}

func TestPostmanV21AndV20ImportExportAndVersionMatrix(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewProject(tmp, "Postman Project")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	// 1. Postman v2.1.0 Collection with nested folders, scripts, secret vars, auth, and bodies
	postmanV21 := `{
  "info": {
    "_postman_id": "col-123",
    "name": "Payment Service",
    "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"
  },
  "variable": [
    {"key": "baseUrl", "value": "https://pay.example.com", "type": "default"},
    {"key": "apiSecret", "value": "pm_secret_val_999", "type": "secret"}
  ],
  "auth": {
    "type": "bearer",
    "bearer": [{"key": "token", "value": "{{apiSecret}}", "type": "string"}]
  },
  "event": [
    {
      "listen": "prerequest",
      "script": {"type": "text/javascript", "exec": ["pm.environment.set('x', '1');"]}
    }
  ],
  "item": [
    {
      "name": "Charges",
      "auth": {
        "type": "apikey",
        "apikey": [
          {"key": "key", "value": "X-Charge-Key", "type": "string"},
          {"key": "value", "value": "inline_charge_secret_888", "type": "string"},
          {"key": "in", "value": "header", "type": "string"}
        ]
      },
      "item": [
        {
          "name": "Create Charge",
          "event": [
            {
              "listen": "test",
              "script": {"type": "text/javascript", "exec": ["pm.test('status 200', function () {});"]}
            }
          ],
          "request": {
            "method": "POST",
            "header": [
              {"key": "Content-Type", "value": "application/json"},
              {"key": "X-Debug", "value": "1", "disabled": true}
            ],
            "url": {
              "raw": "{{baseUrl}}/v1/charges?dryRun=false",
              "query": [
                {"key": "dryRun", "value": "false"},
                {"key": "legacy", "value": "1", "disabled": true}
              ]
            },
            "body": {
              "mode": "raw",
              "raw": "{\"amount\": 500, \"currency\": \"USD\"}",
              "options": {"raw": {"language": "json"}}
            }
          },
          "response": [{"name": "200 OK"}]
        },
        {
          "name": "Upload Receipt",
          "request": {
            "method": "POST",
            "url": "{{baseUrl}}/v1/receipts",
            "auth": {
              "type": "oauth2"
            },
            "body": {
              "mode": "formdata",
              "formdata": [
                {"key": "note", "value": "paid", "type": "text"},
                {"key": "receipt", "src": "/Users/alice/Desktop/receipt.png", "type": "file"}
              ]
            }
          }
        }
      ]
    }
  ]
}`

	plan, err := StagePostmanCollectionImport(p, []byte(postmanV21), ImportOptions{})
	if err != nil {
		t.Fatalf("StagePostmanCollectionImport v2.1: %v", err)
	}
	prev := plan.Preview()
	if !prev.CanApply {
		t.Fatalf("expected CanApply=true for v2.1 collection, got conflicts=%v", prev.Conflicts)
	}
	if prev.FormatVersion != "v2.1.0" {
		t.Fatalf("expected FormatVersion=v2.1.0, got %q", prev.FormatVersion)
	}

	// Verify losses include scripts (never executed), saved response, oauth2 downgrade, and file slot
	lossCodes := map[string]int{}
	for _, l := range prev.Losses {
		lossCodes[l.Code]++
		if strings.Contains(l.Message, "pm_secret_val_999") || strings.Contains(l.Message, "inline_charge_secret_888") {
			t.Fatalf("raw secret leaked in loss message: %+v", l)
		}
	}
	if lossCodes["postman-script-ignored"] != 2 {
		t.Fatalf("expected 2 postman-script-ignored items, got %d (%+v)", lossCodes["postman-script-ignored"], prev.Losses)
	}
	if lossCodes["postman-saved-response-ignored"] != 1 {
		t.Fatalf("expected 1 postman-saved-response-ignored, got %+v", prev.Losses)
	}
	if lossCodes["postman-auth-unsupported"] != 1 {
		t.Fatalf("expected 1 postman-auth-unsupported (oauth2), got %+v", prev.Losses)
	}
	if lossCodes["postman-file-to-local-slot"] != 1 {
		t.Fatalf("expected 1 postman-file-to-local-slot, got %+v", prev.Losses)
	}

	if _, err := plan.Apply(); err != nil {
		t.Fatalf("Apply v2.1: %v", err)
	}

	// Verify environment created from collection variables has baseUrl in Variables and apisecret in Secrets
	colEnv := p.Environments["payment-service-env"]
	if colEnv == nil {
		t.Fatalf("expected payment-service-env in p.Environments")
	}
	if colEnv.Variables["baseUrl"] != "https://pay.example.com" {
		t.Fatalf("unexpected baseUrl variable: %+v", colEnv.Variables)
	}
	if _, leaked := colEnv.Variables["apiSecret"]; leaked {
		t.Fatalf("secret variable apiSecret must not be stored in plain Variables map")
	}
	if !strings.Contains(strings.Join(colEnv.Secrets, ","), "apisecret") {
		t.Fatalf("expected apisecret in Environment.Secrets, got %v", colEnv.Secrets)
	}

	// Verify folder auth on collections/payment-service and collections/payment-service/charges
	rootAuth := p.FolderAuth("payment-service")
	if rootAuth == nil || rootAuth.Inline == nil || rootAuth.Inline.Token != "{{secret.apisecret}}" {
		t.Fatalf("expected root folder bearer auth rewritten to {{secret.apisecret}}, got %+v", rootAuth)
	}
	chargesAuth := p.FolderAuth("payment-service/charges")
	if chargesAuth == nil || chargesAuth.Inline == nil || chargesAuth.Inline.Value != "{{secret.x_charge_key}}" {
		t.Fatalf("expected charges folder apikey value {{secret.x_charge_key}}, got %+v", chargesAuth)
	}

	// 2. Test Postman v2.0.0 Collection import
	postmanV20 := `{
  "info": {
    "name": "Legacy V20",
    "schema": "https://schema.getpostman.com/json/collection/v2.0.0/collection.json"
  },
  "item": [
    {
      "name": "Ping",
      "request": {
        "url": "https://api.example.com/ping?v=2",
        "method": "GET",
        "header": "Accept: application/json\nX-Custom: hello",
        "auth": {
          "type": "basic",
          "basic": {"username": "bob", "password": "v20_password_secret"}
        }
      }
    }
  ]
}`
	planV20, err := StagePostmanCollectionImport(p, []byte(postmanV20), ImportOptions{})
	if err != nil {
		t.Fatalf("StagePostmanCollectionImport v2.0: %v", err)
	}
	if !planV20.Preview().CanApply || planV20.Preview().FormatVersion != "v2.0.0" {
		t.Fatalf("expected valid v2.0.0 preview, got %+v", planV20.Preview())
	}
	if _, err := planV20.Apply(); err != nil {
		t.Fatalf("Apply v2.0: %v", err)
	}
	pingEntry := p.Requests["req-ping"]
	if pingEntry == nil || pingEntry.Request.Auth == nil || pingEntry.Request.Auth.Inline.Username != "bob" {
		t.Fatalf("expected req-ping with basic auth bob, got %+v", pingEntry)
	}

	// 3. Test Postman v1.0 legacy collection is rejected with explicit version diagnostic
	postmanV10 := `{
  "id": "legacy-v1-id",
  "name": "Old V1 Collection",
  "order": ["req-1"],
  "requests": [{"id": "req-1", "name": "Old", "url": "http://example.com", "method": "GET"}]
}`
	planV10, err := StagePostmanCollectionImport(p, []byte(postmanV10), ImportOptions{})
	if err != nil {
		t.Fatalf("StagePostmanCollectionImport v1.0: %v", err)
	}
	if planV10.Preview().CanApply || planV10.Preview().FormatVersion != "v1.0" {
		t.Fatalf("expected v1.0 collection to be blocked with FormatVersion=v1.0, got %+v", planV10.Preview())
	}
	if len(planV10.Preview().Losses) == 0 || planV10.Preview().Losses[0].Code != "postman-v1-unsupported" {
		t.Fatalf("expected postman-v1-unsupported diagnostic, got %+v", planV10.Preview().Losses)
	}

	// 4. Test Postman Environment import & export
	pmEnv := `{
  "name": "Staging Env",
  "_postman_variable_scope": "environment",
  "values": [
    {"key": "host", "value": "staging.example.com", "type": "default", "enabled": true},
    {"key": "db_password", "value": "dont_save_me_123", "type": "secret", "enabled": true},
    {"key": "old_var", "value": "ignored", "type": "default", "enabled": false}
  ]
}`
	envPlan, err := StagePostmanEnvironmentImport(p, []byte(pmEnv), ImportOptions{})
	if err != nil {
		t.Fatalf("StagePostmanEnvironmentImport: %v", err)
	}
	if _, err := envPlan.Apply(); err != nil {
		t.Fatalf("Apply env: %v", err)
	}
	stEnv := p.Environments["staging-env"]
	if stEnv == nil || stEnv.Variables["host"] != "staging.example.com" {
		t.Fatalf("expected staging-env with host variable, got %+v", stEnv)
	}
	if _, hasSecVal := stEnv.Variables["db_password"]; hasSecVal {
		t.Fatalf("secret db_password value leaked into Variables map")
	}

	exportedCol, _, err := ExportPostmanCollection(p, PostmanExportOptions{Scope: ScopeProject})
	if err != nil {
		t.Fatalf("ExportPostmanCollection: %v", err)
	}
	if strings.Contains(string(exportedCol), "pm_secret_val_999") || strings.Contains(string(exportedCol), "inline_charge_secret_888") {
		t.Fatalf("exported Postman collection leaked raw secret")
	}

	exportedEnv, _, err := ExportPostmanEnvironment(p, "staging-env")
	if err != nil {
		t.Fatalf("ExportPostmanEnvironment: %v", err)
	}
	if strings.Contains(string(exportedEnv), "dont_save_me_123") {
		t.Fatalf("exported Postman environment leaked secret value")
	}
}

func TestOpenAPIAndSwaggerRoutedToA03Diagnostic(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewProject(tmp, "OpenAPI Triage")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	openAPIYAML := []byte("openapi: 3.1.0\ninfo:\n  title: Orders API\n  version: 1.0.0\npaths: {}\n")
	planOA, err := StageAutoImport(p, openAPIYAML, "orders-openapi.yaml", ImportOptions{})
	if err != nil {
		t.Fatalf("StageAutoImport OpenAPI: %v", err)
	}
	prevOA := planOA.Preview()
	if prevOA.CanApply {
		t.Fatalf("expected CanApply=false for OpenAPI spec")
	}
	if prevOA.Format != FormatOpenAPI || prevOA.FormatVersion != "3.1.0" {
		t.Fatalf("unexpected format/version: %s / %s", prevOA.Format, prevOA.FormatVersion)
	}
	if len(prevOA.Losses) == 0 || prevOA.Losses[0].Code != "openapi-routed-to-a03" {
		t.Fatalf("expected openapi-routed-to-a03 diagnostic, got %+v", prevOA.Losses)
	}
	if _, err := planOA.Apply(); !errors.Is(err, ErrOpenAPIRoutedToA03) {
		t.Fatalf("expected ErrOpenAPIRoutedToA03 on Apply, got %v", err)
	}

	swaggerJSON := []byte(`{"swagger": "2.0", "info": {"title": "Legacy", "version": "1.0"}, "paths": {}}`)
	planSW, err := StageAutoImport(p, swaggerJSON, "swagger.json", ImportOptions{})
	if err != nil {
		t.Fatalf("StageAutoImport Swagger: %v", err)
	}
	if planSW.Preview().CanApply || planSW.Preview().Format != FormatSwagger2 {
		t.Fatalf("expected blocked FormatSwagger2 preview, got %+v", planSW.Preview())
	}
}

func TestPortableBundleExportImportDependenciesConflictsAndRollback(t *testing.T) {
	srcDir := t.TempDir()
	srcProj, err := NewProject(srcDir, "Source Workspace")
	if err != nil {
		t.Fatalf("NewProject src: %v", err)
	}

	// Create reusable auth scheme, markdown doc with embedded asset, and public root attachment
	authYAML := []byte("schemaVersion: 1\nid: orders-bearer\ntype: bearer\ntoken: \"{{secret.orders_token}}\"\n")
	if err := srcProj.Store.WriteAtomic("auth/orders-bearer.auth.yaml", authYAML); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	assetBytes := []byte("fake-png-binary-content")
	if err := srcProj.Store.WriteAtomic("assets/arch.png", assetBytes); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	docMD := []byte("# Orders Guide\n\n![Architecture](../assets/arch.png)\n")
	if err := srcProj.Store.WriteAtomic("docs/orders.md", docMD); err != nil {
		t.Fatalf("write doc: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(srcDir, "testdata"), 0o755); err != nil {
		t.Fatalf("mkdir testdata: %v", err)
	}
	samplePayload := []byte(`{"sku":"ABC-123"}`)
	if err := os.WriteFile(filepath.Join(srcDir, "testdata", "payload.json"), samplePayload, 0o644); err != nil {
		t.Fatalf("write payload.json: %v", err)
	}
	// Also write a private local-files.json inside .gorequest to verify it is NEVER exported
	if err := os.WriteFile(filepath.Join(srcProj.Dir, "local-files.json"), []byte(`{"slots":{"private_cert":"/Users/host/.ssh/id_rsa"}}`), 0o600); err != nil {
		t.Fatalf("write local-files.json: %v", err)
	}

	reqYAML := []byte(`schemaVersion: 1
id: req-create-order
name: Create Order
protocol: http
auth:
  ref: orders-bearer
documentation:
  - docs/orders.md
http:
  method: POST
  url: "{{baseUrl}}/orders"
  body:
    type: form-data
    value:
      - name: meta
        kind: text
        value: ok
      - name: privateDoc
        kind: file
        localFile: private_cert
`)
	if err := srcProj.Store.WriteAtomic("collections/orders/req-create-order.request.yaml", reqYAML); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := ReloadProjectInMemory(srcProj); err != nil {
		t.Fatalf("ReloadProjectInMemory: %v", err)
	}

	// 1. Export Single Request Portable Bundle
	bundleBytes, manifest, _, err := ExportPortableBundle(srcProj, PortableExportOptions{
		Scope:         ScopeRequest,
		TargetRelPath: "collections/orders/req-create-order.request.yaml",
	})
	if err != nil {
		t.Fatalf("ExportPortableBundle request: %v", err)
	}
	if manifest.BundleKind != ScopeRequest {
		t.Fatalf("expected BundleKind=request, got %s", manifest.BundleKind)
	}
	if !strings.Contains(strings.Join(manifest.RequiredSecrets, ","), "orders_token") {
		t.Fatalf("expected orders_token in RequiredSecrets, got %v", manifest.RequiredSecrets)
	}
	if !strings.Contains(strings.Join(manifest.RequiredLocalSlots, ","), "private_cert") {
		t.Fatalf("expected private_cert in RequiredLocalSlots, got %v", manifest.RequiredLocalSlots)
	}
	// Verify host path `/Users/host/.ssh/id_rsa` is nowhere in the bundle zip!
	if bytes.Contains(bundleBytes, []byte("id_rsa")) || bytes.Contains(bundleBytes, []byte("local-files.json")) {
		t.Fatalf("portable bundle leaked local-files.json or host path!")
	}

	// 2. Import into a destination project
	dstDir := t.TempDir()
	dstProj, err := NewProject(dstDir, "Dest Workspace")
	if err != nil {
		t.Fatalf("NewProject dst: %v", err)
	}

	importPlan, err := StagePortableBundleImport(dstProj, bundleBytes, ImportOptions{
		ConflictPolicy: ConflictFail,
	})
	if err != nil {
		t.Fatalf("StagePortableBundleImport: %v", err)
	}
	if !importPlan.Preview().CanApply {
		t.Fatalf("expected CanApply=true on clean dest project, got conflicts=%v", importPlan.Preview().Conflicts)
	}
	if _, err := importPlan.Apply(); err != nil {
		t.Fatalf("Apply portable import: %v", err)
	}

	// Verify request, referenced auth scheme, doc, and asset were all imported!
	if _, ok := dstProj.Requests["req-create-order"]; !ok {
		t.Fatalf("expected req-create-order in dstProj")
	}
	if _, ok := dstProj.AuthSchemes["orders-bearer"]; !ok {
		t.Fatalf("expected orders-bearer auth scheme carried with request")
	}
	if !dstProj.Store.Exists("docs/orders.md") || !dstProj.Store.Exists("assets/arch.png") {
		t.Fatalf("expected docs/orders.md and assets/arch.png carried with request")
	}

	// 3. Re-importing with ConflictFail must block Apply
	dupFailPlan, err := StagePortableBundleImport(dstProj, bundleBytes, ImportOptions{
		ConflictPolicy: ConflictFail,
	})
	if err != nil {
		t.Fatalf("StagePortableBundleImport dup fail: %v", err)
	}
	if dupFailPlan.Preview().CanApply || len(dupFailPlan.Preview().Conflicts) == 0 {
		t.Fatalf("expected ConflictFail to block duplicate import, got %+v", dupFailPlan.Preview())
	}

	// 4. Re-importing with ConflictRenameNewID allocates new IDs and rewires auth.ref & documentation!
	dupRenamePlan, err := StagePortableBundleImport(dstProj, bundleBytes, ImportOptions{
		ConflictPolicy: ConflictRenameNewID,
	})
	if err != nil {
		t.Fatalf("StagePortableBundleImport dup rename: %v", err)
	}
	if !dupRenamePlan.Preview().CanApply {
		t.Fatalf("expected ConflictRenameNewID to be applicable, got %+v", dupRenamePlan.Preview())
	}
	renameReceipt, err := dupRenamePlan.Apply()
	if err != nil {
		t.Fatalf("Apply dup rename: %v", err)
	}
	copiedReq := dstProj.Requests["req-create-order-copy"]
	if copiedReq == nil {
		t.Fatalf("expected req-create-order-copy after ConflictRenameNewID")
	}
	if copiedReq.Request.Auth == nil || copiedReq.Request.Auth.Ref != "orders-bearer-copy" {
		t.Fatalf("expected copied request auth.ref rewired to orders-bearer-copy, got %+v", copiedReq.Request.Auth)
	}
	if len(copiedReq.Request.Documentation) != 1 || copiedReq.Request.Documentation[0] != "docs/orders-copy.md" {
		t.Fatalf("expected copied request documentation rewired to docs/orders-copy.md, got %v", copiedReq.Request.Documentation)
	}

	// Rollback the duplicate copy import and verify the original remains intact
	if err := renameReceipt.Rollback(); err != nil {
		t.Fatalf("Rollback renameReceipt: %v", err)
	}
	if _, stillCopied := dstProj.Requests["req-create-order-copy"]; stillCopied {
		t.Fatalf("expected req-create-order-copy removed after Rollback")
	}
	if _, origStill := dstProj.Requests["req-create-order"]; !origStill {
		t.Fatalf("expected original req-create-order preserved after Rollback")
	}

	// 5. Mid-apply fault injection leaves zero partial files
	faultPlan, err := StagePortableBundleImport(dstProj, bundleBytes, ImportOptions{
		ConflictPolicy: ConflictRenameNewID,
	})
	if err != nil {
		t.Fatalf("StagePortableBundleImport fault: %v", err)
	}
	faultPlan.SetBeforeWriteFaultHook(func(stepIndex int, rel string) error {
		if stepIndex == 2 {
			return errors.New("simulated disk write failure at step 2")
		}
		return nil
	})
	if _, err := faultPlan.Apply(); err == nil {
		t.Fatalf("expected fault injection to fail Apply")
	}
	if dstProj.Store.Exists("auth/orders-bearer-copy.auth.yaml") || dstProj.Store.Exists("docs/orders-copy.md") {
		t.Fatalf("partial files from failed Apply were not rolled back!")
	}

	// 6. Full Project Export & ImportPortableProjectToDir
	fullBytes, fullManifest, _, err := ExportPortableBundle(srcProj, PortableExportOptions{Scope: ScopeProject})
	if err != nil {
		t.Fatalf("ExportPortableBundle project: %v", err)
	}
	if fullManifest.BundleKind != ScopeProject {
		t.Fatalf("expected ScopeProject manifest")
	}
	standaloneDir := filepath.Join(t.TempDir(), "standalone-imported")
	standaloneProj, _, err := ImportPortableProjectToDir(standaloneDir, fullBytes, DefaultArchiveLimits())
	if err != nil {
		t.Fatalf("ImportPortableProjectToDir: %v", err)
	}
	if _, ok := standaloneProj.Requests["req-create-order"]; !ok {
		t.Fatalf("expected req-create-order in standalone imported project")
	}
}

func TestPortableArchiveSecurityHardening(t *testing.T) {
	tmp := t.TempDir()
	p, err := NewProject(tmp, "Archive Security")
	if err != nil {
		t.Fatalf("NewProject: %v", err)
	}

	buildCustomZip := func(entries map[string][]byte, modeOverride map[string]os.FileMode) []byte {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		for name, content := range entries {
			hdr := &zip.FileHeader{
				Name:   name,
				Method: zip.Deflate,
			}
			if m, ok := modeOverride[name]; ok {
				hdr.SetMode(m)
			} else {
				hdr.SetMode(0o644)
			}
			w, err := zw.CreateHeader(hdr)
			if err != nil {
				t.Fatalf("CreateHeader %s: %v", name, err)
			}
			if _, err := w.Write(content); err != nil {
				t.Fatalf("Write %s: %v", name, err)
			}
		}
		_ = zw.Close()
		return buf.Bytes()
	}

	validManifest := []byte("schemaVersion: 1\nbundleKind: request\nsourceProjectName: Test\n")

	// 1. Path traversal entries (`..`, leading `/`, backslash, Windows drive)
	traversalNames := []string{
		"../../etc/passwd",
		".gorequest/../../evil.yaml",
		"/etc/passwd",
		`.gorequest\collections\req.request.yaml`,
		"C:/Windows/win.ini",
	}
	for _, badName := range traversalNames {
		zb := buildCustomZip(map[string][]byte{
			portableManifestName: validManifest,
			badName:              []byte("evil"),
		}, nil)
		if _, err := StagePortableBundleImport(p, zb, ImportOptions{}); err == nil {
			t.Fatalf("expected path traversal %q to be rejected", badName)
		}
	}

	// 2. Symlink entry inside zip archive
	symlinkZip := buildCustomZip(map[string][]byte{
		portableManifestName:              validManifest,
		".gorequest/docs/ evil_link.md": []byte("/etc/passwd"),
	}, map[string]os.FileMode{
		".gorequest/docs/ evil_link.md": os.ModeSymlink | 0o777,
	})
	if _, err := StagePortableBundleImport(p, symlinkZip, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("expected symlink entry to be rejected, got %v", err)
	}

	// 3. Forbidden private state file (`local-files.json`, `cookies.json`)
	forbiddenZip := buildCustomZip(map[string][]byte{
		portableManifestName:          validManifest,
		".gorequest/local-files.json": []byte("{}"),
	}, nil)
	if _, err := StagePortableBundleImport(p, forbiddenZip, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "forbidden") {
		t.Fatalf("expected local-files.json in archive to be rejected, got %v", err)
	}

	// 4. Unresolved Git conflict markers inside archived YAML
	conflictZip := buildCustomZip(map[string][]byte{
		portableManifestName: validManifest,
		".gorequest/collections/req-bad.request.yaml": []byte("schemaVersion: 1\n<<<<<<< HEAD\nid: a\n=======\nid: b\n>>>>>>> branch\n"),
	}, nil)
	if _, err := StagePortableBundleImport(p, conflictZip, ImportOptions{}); err == nil || !strings.Contains(err.Error(), "conflict markers") {
		t.Fatalf("expected Git conflict markers to be rejected, got %v", err)
	}

	// 5. Zip bomb: high compression ratio / entry byte limit / entry count limit
	bigZeros := bytes.Repeat([]byte{'A'}, 256*1024) // 256 KiB of 'A' compresses to a few hundred bytes (>100:1 ratio)
	bombZip := buildCustomZip(map[string][]byte{
		portableManifestName:                  validManifest,
		".gorequest/docs/giant-bomb-doc.md": bigZeros,
	}, nil)
	if _, err := StagePortableBundleImport(p, bombZip, ImportOptions{
		ArchiveLimits: ArchiveLimits{
			MaxCompressionRatio: 20,
		},
	}); err == nil || !strings.Contains(err.Error(), "compression ratio") {
		t.Fatalf("expected zip bomb compression ratio check to reject archive, got %v", err)
	}

	// 6. Entry count limit
	if _, err := StagePortableBundleImport(p, buildCustomZip(map[string][]byte{
		portableManifestName:       validManifest,
		".gorequest/docs/doc1.md": []byte("# 1\n"),
		".gorequest/docs/doc2.md": []byte("# 2\n"),
	}, nil), ImportOptions{
		ArchiveLimits: ArchiveLimits{MaxEntryCount: 2},
	}); err == nil || !strings.Contains(err.Error(), "entry count") {
		t.Fatalf("expected entry count limit to reject archive, got %v", err)
	}
}
