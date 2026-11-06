package core

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func createOpenAPITestProject(t *testing.T) *Project {
	t.Helper()
	tmp := t.TempDir()
	gq := filepath.Join(tmp, ".gorequest")
	for _, sub := range []string{"collections", "environments", "auth", "docs", "schemas"} {
		if err := os.MkdirAll(filepath.Join(gq, sub), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", sub, err)
		}
	}
	projYAML := "schemaVersion: 1\nid: proj-openapi-test\nname: OpenAPI Test Project\n"
	if err := os.WriteFile(filepath.Join(gq, "project.yaml"), []byte(projYAML), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	envYAML := "# Dev environment comment\nschemaVersion: 1\nid: dev\nvariables:\n  baseUrl: http://127.0.0.1:8080\n"
	if err := os.WriteFile(filepath.Join(gq, "environments", "dev.yaml"), []byte(envYAML), 0o644); err != nil {
		t.Fatalf("write dev.yaml: %v", err)
	}
	p, err := OpenProject(tmp)
	if err != nil {
		t.Fatalf("OpenProject: %v", err)
	}
	return p
}

func TestOpenAPI31ImportExportRoundTripAndFidelity(t *testing.T) {
	p := createOpenAPITestProject(t)

	spec31YAML := `openapi: 3.1.2
info:
  title: Pet & Order Service
  summary: Core Pet & Order REST Contract
  description: Multi-response, multi-media-type OpenAPI 3.1.2 contract.
  version: 2.4.0
  contact:
    name: API Team
    email: api@example.com
  license:
    name: Apache 2.0
    identifier: Apache-2.0
servers:
  - url: https://api.example.com/v2
    description: Production API
tags:
  - name: users
    description: User management operations
paths:
  /users/{userId}:
    parameters:
      - name: userId
        in: path
        required: true
        description: Unique user identifier
        schema:
          type: string
          minLength: 3
        example: usr_123
    get:
      operationId: getUserById
      summary: Get user by ID
      description: Fetches a single user profile or problem details.
      tags:
        - users
      parameters:
        - name: verbose
          in: query
          required: false
          schema:
            type: boolean
            default: false
        - name: X-Trace-Mode
          in: header
          required: false
          schema:
            type: string
          example: debug
      responses:
        "200":
          description: User found
          headers:
            X-Rate-Limit:
              description: Remaining quota
              required: true
              schema:
                type: integer
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/User"
              example:
                id: 42
                email: alice@example.com
                nickname: null
            application/xml:
              schema:
                type: string
        "400":
          description: Invalid user ID supplied
          content:
            application/problem+json:
              schema:
                $ref: "#/components/schemas/Problem"
        "404":
          description: User not found
    post:
      operationId: updateUserAvatar
      summary: Upload user avatar
      tags:
        - users
      security:
        - bearerAuth: []
      requestBody:
        required: true
        description: Avatar payload in JSON or multipart form
        content:
          application/json:
            schema:
              $ref: "#/components/schemas/User"
          multipart/form-data:
            schema:
              type: object
              properties:
                caption:
                  type: string
                avatar:
                  type: string
                  format: binary
      responses:
        "201":
          description: Avatar updated
          content:
            application/json:
              schema:
                $ref: "#/components/schemas/User"
components:
  schemas:
    User:
      type: object
      required:
        - id
        - email
      properties:
        id:
          type: integer
          minimum: 1
        email:
          type: string
          format: email
        nickname:
          type:
            - string
            - "null"
    Problem:
      type: object
      required:
        - title
        - status
      properties:
        title:
          type: string
        status:
          type: integer
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
      bearerFormat: JWT
    apiKeyHeader:
      type: apiKey
      in: header
      name: X-API-Key
`

	plan, err := StageOpenAPIImport(p, []byte(spec31YAML), "pet-service.yaml", OpenAPIImportOptions{
		GroupByTag: true,
		InteropOptions: ImportOptions{
			ConflictPolicy: ConflictOverwrite,
		},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport: %v", err)
	}
	prev := plan.Preview()
	if !prev.CanApply {
		t.Fatalf("expected CanApply=true, got false; conflicts=%v, losses=%v", prev.Conflicts, prev.Losses)
	}

	// Verify transactional Apply -> Rollback -> Re-apply
	receipt, err := plan.Apply()
	if err != nil {
		t.Fatalf("plan.Apply: %v", err)
	}
	if _, ok := p.Requests["get-user-by-id"]; !ok {
		t.Fatalf("expected request get-user-by-id in project after Apply, got %v", sortedMapKeys(p.Requests))
	}
	if err := receipt.Rollback(); err != nil {
		t.Fatalf("receipt.Rollback: %v", err)
	}
	if _, ok := p.Requests["get-user-by-id"]; ok {
		t.Fatalf("expected request get-user-by-id removed after Rollback")
	}

	// Re-stage and Apply permanently
	plan2, err := StageOpenAPIImport(p, []byte(spec31YAML), "pet-service.yaml", OpenAPIImportOptions{
		GroupByTag: true,
		InteropOptions: ImportOptions{
			ConflictPolicy: ConflictOverwrite,
		},
	})
	if err != nil {
		t.Fatalf("re-stage: %v", err)
	}
	if _, err := plan2.Apply(); err != nil {
		t.Fatalf("re-apply: %v", err)
	}

	// Check imported request contract metadata (multi-status, multi-media-type, headers, parameters)
	getReqEntry := p.Requests["get-user-by-id"]
	if getReqEntry == nil || getReqEntry.Request == nil {
		t.Fatalf("missing get-user-by-id request")
	}
	contract, err := GetRequestOpenAPI(getReqEntry.Request)
	if err != nil || contract == nil {
		t.Fatalf("GetRequestOpenAPI: err=%v, contract=%v", err, contract)
	}
	if contract.OperationID != "getUserById" || contract.PathTemplate != "/users/{userId}" {
		t.Fatalf("unexpected operationId/pathTemplate: %+v", contract)
	}
	if len(contract.Responses) != 3 {
		t.Fatalf("expected 3 response status contracts (200, 400, 404), got %d", len(contract.Responses))
	}
	if len(contract.Responses["200"].Content) != 2 {
		t.Fatalf("expected 2 media types under 200 response, got %d", len(contract.Responses["200"].Content))
	}
	if _, hasRateHdr := contract.Responses["200"].Headers["X-Rate-Limit"]; !hasRateHdr {
		t.Fatalf("expected X-Rate-Limit header in 200 response contract")
	}

	// Check POST request has multi-media-type requestBody (application/json + multipart/form-data)
	postReqEntry := p.Requests["update-user-avatar"]
	if postReqEntry == nil || postReqEntry.Request == nil {
		t.Fatalf("missing update-user-avatar request")
	}
	postContract, err := GetRequestOpenAPI(postReqEntry.Request)
	if err != nil || postContract == nil || postContract.RequestBody == nil {
		t.Fatalf("missing post requestBody contract: %v", err)
	}
	if len(postContract.RequestBody.Content) != 2 {
		t.Fatalf("expected 2 media types in requestBody contract, got %d", len(postContract.RequestBody.Content))
	}

	// Check SaveRequestWithOpenAPI preserves YAML comments & enforces baseline digest check
	rawReqBytes, _, err := p.Store.Read(getReqEntry.RelPath)
	if err != nil {
		t.Fatalf("read request YAML: %v", err)
	}
	commentedBytes := []byte("# Top-level request comment\n" + string(rawReqBytes))
	if err := p.Store.WriteAtomic(getReqEntry.RelPath, commentedBytes); err != nil {
		t.Fatalf("write commented request YAML: %v", err)
	}
	getReqEntry.BaselineDigest = Digest(commentedBytes)

	contract.Summary = "Updated Get User By ID Summary"
	newDigest, err := SaveRequestWithOpenAPI(p, getReqEntry, getReqEntry.Request, contract, getReqEntry.BaselineDigest)
	if err != nil {
		t.Fatalf("SaveRequestWithOpenAPI: %v", err)
	}
	savedBytes, _, _ := p.Store.Read(getReqEntry.RelPath)
	if !strings.Contains(string(savedBytes), "# Top-level request comment") {
		t.Fatalf("expected comment preserved after SaveRequestWithOpenAPI, got:\n%s", string(savedBytes))
	}
	if !strings.Contains(string(savedBytes), "Updated Get User By ID Summary") {
		t.Fatalf("expected updated contract summary persisted in YAML, got:\n%s", string(savedBytes))
	}
	// Stale baseline must fail with ConflictError
	if _, err := SaveRequestWithOpenAPI(p, getReqEntry, getReqEntry.Request, contract, "stale-digest"); !IsConflict(err) {
		t.Fatalf("expected ConflictError on stale baseline, got %v", err)
	}
	_ = newDigest

	// Export to OpenAPI 3.1.2 YAML and JSON, then re-import into a fresh project
	expYAML, err := ExportOpenAPI(p, OpenAPIExportOptions{
		TargetVersion:   OpenAPIVersion31,
		Format:          "yaml",
		Strict:          true,
		IncludeExamples: true,
	})
	if err != nil {
		t.Fatalf("ExportOpenAPI 3.1 YAML: %v", err)
	}
	if !expYAML.Compatibility.Compatible || expYAML.Compatibility.Lossy {
		t.Fatalf("expected lossless 3.1 export compatibility, got %+v", expYAML.Compatibility)
	}

	expJSON, err := ExportOpenAPI(p, OpenAPIExportOptions{
		TargetVersion:   OpenAPIVersion31,
		Format:          "json",
		Strict:          true,
		IncludeExamples: true,
	})
	if err != nil {
		t.Fatalf("ExportOpenAPI 3.1 JSON: %v", err)
	}

	// Re-import exported JSON into a second project and verify semantic equivalence
	p2 := createOpenAPITestProject(t)
	reImportPlan, err := StageOpenAPIImport(p2, expJSON.Bytes, "exported.json", OpenAPIImportOptions{
		InteropOptions: ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("re-import exported JSON: %v", err)
	}
	if !reImportPlan.Preview().CanApply {
		t.Fatalf("expected exported 3.1 JSON to re-import cleanly, conflicts=%v, losses=%v", reImportPlan.Preview().Conflicts, reImportPlan.Preview().Losses)
	}
	if _, err := reImportPlan.Apply(); err != nil {
		t.Fatalf("re-import Apply: %v", err)
	}
	if len(p2.Requests) != 2 {
		t.Fatalf("expected 2 requests after round-trip, got %d", len(p2.Requests))
	}
	reContract, err := GetRequestOpenAPI(p2.Requests["get-user-by-id"].Request)
	if err != nil || reContract == nil {
		t.Fatalf("re-imported GetRequestOpenAPI: %v", err)
	}
	if len(reContract.Responses) != 3 || len(reContract.Responses["200"].Content) != 2 {
		t.Fatalf("expected multi-status and multi-content preserved after round-trip, got %+v", reContract.Responses)
	}
}

func TestOpenAPI32MatrixAndStrictDowngradeBoundary(t *testing.T) {
	// 1. Verify that an OpenAPI 3.1 document claiming 3.2 constructs is REJECTED (never masquerading 3.1 as 3.2)
	invalid31With32Constructs := `openapi: 3.1.2
$self: https://api.example.com/openapi.yaml
info:
  title: Fake 3.1 using 3.2 features
  version: 1.0.0
servers:
  - url: https://api.example.com
    name: prod-cluster
tags:
  - name: search
    parent: root
    kind: nav
paths:
  /search:
    query:
      operationId: querySearch
      responses:
        "200":
          description: OK
    additionalOperations:
      PURGE:
        operationId: purgeSearchCache
        responses:
          "204":
            description: Purged
`
	val31, err := ValidateOpenAPISpec([]byte(invalid31With32Constructs), OpenAPIValidationOptions{})
	if err != nil {
		t.Fatalf("ValidateOpenAPISpec: %v", err)
	}
	if val31.Valid {
		t.Fatalf("expected OpenAPI 3.1.2 document containing 3.2 constructs ($self, Server.name, Tag.parent, PathItem.query, additionalOperations) to fail validation")
	}

	// 2. Verify that an OpenAPI 3.2 document placing standard method GET inside additionalOperations is rejected
	invalid32DuplicateStandard := `openapi: 3.2.1
info:
  title: Invalid 3.2 additionalOperations
  version: 1.0.0
paths:
  /items:
    additionalOperations:
      GET:
        operationId: illegalGetInAdditionalOps
        responses:
          "200":
            description: OK
`
	val32Bad, err := ValidateOpenAPISpec([]byte(invalid32DuplicateStandard), OpenAPIValidationOptions{})
	if err != nil {
		t.Fatalf("ValidateOpenAPISpec: %v", err)
	}
	if val32Bad.Valid {
		t.Fatalf("expected standard method GET inside additionalOperations to be rejected in OpenAPI 3.2")
	}

	// 3. Valid OpenAPI 3.2.1 specification exercising the full 3.2 feature matrix
	valid32YAML := `openapi: 3.2.1
$self: https://api.example.com/v3/openapi.yaml
info:
  title: NextGen 3.2 Service
  summary: Official OpenAPI 3.2.1 Contract
  version: 3.2.0
servers:
  - url: https://api.example.com/v3
    name: primary-edge
    description: Primary Edge Cluster
tags:
  - name: catalog
    summary: Catalog Hierarchy
    parent: commerce
    kind: nav
paths:
  /catalog/search:
    query:
      operationId: queryCatalog
      summary: Structured QUERY search
      tags:
        - catalog
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                filter:
                  type: string
            examples:
              structuredFilter:
                summary: Structured 3.2 example
                dataValue:
                  filter: active
                serializedValue: '{"filter":"active"}'
      responses:
        "200":
          description: Search results
          content:
            application/json:
              schema:
                type: object
                properties:
                  total:
                    type: integer
            text/event-stream:
              itemSchema:
                type: object
                properties:
                  chunk:
                    type: string
    additionalOperations:
      PURGE:
        operationId: purgeCatalogCache
        summary: Custom PURGE cache invalidation
        tags:
          - catalog
        responses:
          "204":
            description: Cache purged
components:
  securitySchemes:
    oauthDevice:
      type: oauth2
      deprecated: true
      oauth2MetadataUrl: https://auth.example.com/.well-known/oauth-authorization-server
      flows:
        deviceAuthorization:
          deviceAuthorizationUrl: https://auth.example.com/oauth/device
          tokenUrl: https://auth.example.com/oauth/token
          scopes:
            catalog:read: Read catalog
`

	p := createOpenAPITestProject(t)

	// With StrictLossReject=true, streaming itemSchema + oauth2 loss items must block import
	strictPlan, err := StageOpenAPIImport(p, []byte(valid32YAML), "nextgen-3.2.yaml", OpenAPIImportOptions{
		StrictLossReject: true,
		InteropOptions:   ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport strict: %v", err)
	}
	if strictPlan.Preview().CanApply {
		t.Fatalf("expected StrictLossReject=true to block import when 3.2 streaming itemSchema / OAuth2 loss items exist")
	}

	// With StrictLossReject=false, import succeeds and preserves 3.2 constructs + loss diagnostics
	plan32, err := StageOpenAPIImport(p, []byte(valid32YAML), "nextgen-3.2.yaml", OpenAPIImportOptions{
		StrictLossReject: false,
		InteropOptions:   ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport 3.2: %v", err)
	}
	if !plan32.Preview().CanApply {
		t.Fatalf("expected 3.2 import CanApply=true with StrictLossReject=false, conflicts=%v", plan32.Preview().Conflicts)
	}
	if _, err := plan32.Apply(); err != nil {
		t.Fatalf("plan32.Apply: %v", err)
	}

	// Verify both QUERY and custom method PURGE requests were created with their HTTP methods intact
	qReq := p.Requests["query-catalog"]
	if qReq == nil || qReq.Request.HTTP.Method != "QUERY" {
		t.Fatalf("expected QUERY request 'query-catalog', got %+v", qReq)
	}
	purgeReq := p.Requests["purge-catalog-cache"]
	if purgeReq == nil || purgeReq.Request.HTTP.Method != "PURGE" {
		t.Fatalf("expected PURGE request 'purge-catalog-cache', got %+v", purgeReq)
	}

	// 4. Export as OpenAPI 3.2.1: must succeed losslessly and preserve all 3.2 constructs
	exp32, err := ExportOpenAPI(p, OpenAPIExportOptions{
		TargetVersion:   OpenAPIVersion32,
		Format:          "yaml",
		Strict:          true,
		IncludeExamples: true,
	})
	if err != nil {
		t.Fatalf("ExportOpenAPI 3.2.1: %v", err)
	}
	if !exp32.Compatibility.Compatible || exp32.Compatibility.Lossy {
		t.Fatalf("expected lossless 3.2.1 export, got %+v", exp32.Compatibility)
	}
	exp32Text := string(exp32.Bytes)
	for _, needle := range []string{
		"openapi: 3.2.1",
		"$self: https://api.example.com/v3/openapi.yaml",
		"name: primary-edge",
		"parent: commerce",
		"kind: nav",
		"query:",
		"additionalOperations:",
		"PURGE:",
		"dataValue:",
		"serializedValue:",
		"itemSchema:",
		"oauth2MetadataUrl:",
		"deviceAuthorization:",
	} {
		if !strings.Contains(exp32Text, needle) {
			t.Fatalf("expected exported 3.2.1 YAML to contain %q, got:\n%s", needle, exp32Text)
		}
	}

	// 5. Export the 3.2 project to TargetVersion 3.1.2 with Strict=true, AllowLossy=false -> MUST REJECT with ErrOpenAPILossyRejected
	exp31Strict, err := ExportOpenAPI(p, OpenAPIExportOptions{
		TargetVersion:   OpenAPIVersion31,
		Format:          "yaml",
		Strict:          true,
		AllowLossy:      false,
		IncludeExamples: true,
	})
	if !errors.Is(err, ErrOpenAPILossyRejected) {
		t.Fatalf("expected ErrOpenAPILossyRejected when exporting 3.2 features to 3.1 in strict mode, got %v", err)
	}
	if exp31Strict == nil || exp31Strict.Compatibility.Compatible || len(exp31Strict.Compatibility.Issues) < 6 {
		t.Fatalf("expected detailed CompatibilityReport issues on rejected 3.2->3.1 export, got %+v", exp31Strict)
	}

	// 6. Export to 3.1.2 with AllowLossy=true -> succeeds and downgrades cleanly with CompatibilityReport
	exp31Lossy, err := ExportOpenAPI(p, OpenAPIExportOptions{
		TargetVersion:   OpenAPIVersion31,
		Format:          "yaml",
		Strict:          true,
		AllowLossy:      true,
		IncludeExamples: true,
	})
	if err != nil {
		t.Fatalf("ExportOpenAPI 3.1 AllowLossy: %v", err)
	}
	if !exp31Lossy.Compatibility.Lossy || len(exp31Lossy.Compatibility.Issues) == 0 {
		t.Fatalf("expected Lossy=true with compatibility issues recorded")
	}

	// Also test direct DowngradeOpenAPI32To31 helper in strict vs non-strict mode
	if _, rep, err := DowngradeOpenAPI32To31(exp32.Bytes, "yaml", true); !errors.Is(err, ErrOpenAPILossyRejected) || rep == nil {
		t.Fatalf("expected DowngradeOpenAPI32To31 strict=true to reject with ErrOpenAPILossyRejected, got %v", err)
	}
	downgradedBytes, rep, err := DowngradeOpenAPI32To31(exp32.Bytes, "yaml", false)
	if err != nil || rep == nil || !rep.Lossy {
		t.Fatalf("expected DowngradeOpenAPI32To31 strict=false to succeed with lossy report, err=%v", err)
	}
	valDowngraded, err := ValidateOpenAPISpec(downgradedBytes, OpenAPIValidationOptions{})
	if err != nil || !valDowngraded.Valid || valDowngraded.Kind != SpecKindOpenAPI31 {
		t.Fatalf("expected downgraded 3.1.2 spec to validate as valid OpenAPI 3.1, got report=%+v, err=%v", valDowngraded, err)
	}
}

func TestSwagger20AndOpenAPI30MigrationAndStrictLossReject(t *testing.T) {
	swagger2JSON := `{
  "swagger": "2.0",
  "info": {
    "title": "Legacy Billing API",
    "version": "1.2.0"
  },
  "host": "billing.example.com",
  "basePath": "/v1",
  "schemes": ["https"],
  "consumes": ["application/json"],
  "produces": ["application/json"],
  "securityDefinitions": {
    "apiKeyAuth": {
      "type": "apiKey",
      "name": "X-Billing-Key",
      "in": "header"
    },
    "basicAuth": {
      "type": "basic"
    }
  },
  "paths": {
    "/invoices/{invoiceId}": {
      "post": {
        "operationId": "payInvoice",
        "summary": "Pay an invoice",
        "security": [{"apiKeyAuth": []}],
        "parameters": [
          {
            "name": "invoiceId",
            "in": "path",
            "required": true,
            "type": "string"
          },
          {
            "name": "tags",
            "in": "query",
            "type": "array",
            "collectionFormat": "pipes",
            "items": {"type": "string"}
          },
          {
            "name": "payload",
            "in": "body",
            "required": true,
            "schema": {"$ref": "#/definitions/PaymentRequest"}
          }
        ],
        "responses": {
          "200": {
            "description": "Receipt",
            "schema": {"$ref": "#/definitions/PaymentReceipt"}
          }
        }
      }
    }
  },
  "definitions": {
    "PaymentRequest": {
      "type": "object",
      "required": ["amount"],
      "properties": {
        "amount": {
          "type": "number",
          "minimum": 0,
          "exclusiveMinimum": true
        },
        "note": {
          "type": "string",
          "x-nullable": true
        }
      }
    },
    "PaymentReceipt": {
      "type": "object",
      "required": ["receiptId"],
      "properties": {
        "receiptId": {"type": "string"}
      }
    }
  }
}`

	p := createOpenAPITestProject(t)

	// StrictLossReject=true blocks because `collectionFormat: pipes` is a loss warning
	strictPlan, err := StageOpenAPIImport(p, []byte(swagger2JSON), "swagger2.json", OpenAPIImportOptions{
		StrictLossReject: true,
		InteropOptions:   ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport swagger2 strict: %v", err)
	}
	if strictPlan.Preview().CanApply {
		t.Fatalf("expected StrictLossReject=true to block Swagger 2.0 import with collectionFormat: pipes loss")
	}

	// Normal migration succeeds and converts host/basePath/schemes, in:body, definitions, x-nullable, exclusiveMinimum
	plan, err := StageOpenAPIImport(p, []byte(swagger2JSON), "swagger2.json", OpenAPIImportOptions{
		StrictLossReject: false,
		InteropOptions:   ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport swagger2: %v", err)
	}
	if !plan.Preview().CanApply {
		t.Fatalf("expected Swagger 2.0 migration CanApply=true, conflicts=%v", plan.Preview().Conflicts)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatalf("Swagger 2.0 Apply: %v", err)
	}

	if env := p.Environments["dev"]; env == nil || env.Variables["baseUrl"] != "https://billing.example.com/v1" {
		t.Fatalf("expected Swagger 2.0 host/basePath/schemes migrated to baseUrl=https://billing.example.com/v1, got %+v", p.Environments["dev"])
	}
	payReq := p.Requests["pay-invoice"]
	if payReq == nil || payReq.Request.HTTP == nil || payReq.Request.HTTP.Body == nil {
		t.Fatalf("expected pay-invoice request with migrated body")
	}
	if payReq.Request.Auth == nil || payReq.Request.Auth.Mode != "ref" || payReq.Request.Auth.Ref != "apikeyauth" {
		t.Fatalf("expected pay-invoice auth ref to apikeyauth, got %+v", payReq.Request.Auth)
	}

	// Verify migrated PaymentRequest schema converted boolean exclusiveMinimum: true + minimum: 0 -> numeric exclusiveMinimum: 0 and x-nullable -> ["string", "null"]
	schemaBytes, _, err := p.Store.Read("schemas/paymentrequest.schema.json")
	if err != nil {
		t.Fatalf("read migrated schema: %v", err)
	}
	if !strings.Contains(string(schemaBytes), `"exclusiveMinimum": 0`) || !strings.Contains(string(schemaBytes), `"null"`) {
		t.Fatalf("expected numeric exclusiveMinimum and null type union in migrated schema, got:\n%s", string(schemaBytes))
	}
}

func TestOfflineMultiFileRefSandboxAndSecurityBoundaries(t *testing.T) {
	specDir := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideDir, "secret.yaml"), []byte("type: string\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}

	if err := os.MkdirAll(filepath.Join(specDir, "common"), 0o755); err != nil {
		t.Fatalf("mkdir common: %v", err)
	}
	commonSchema := `components:
  schemas:
    Address:
      type: object
      required:
        - city
      properties:
        city:
          type: string
`
	if err := os.WriteFile(filepath.Join(specDir, "common", "models.yaml"), []byte(commonSchema), 0o644); err != nil {
		t.Fatalf("write common/models.yaml: %v", err)
	}

	// 1. Valid multi-file relative $ref
	mainSpec := `openapi: 3.1.2
info:
  title: Multi-file Spec
  version: 1.0.0
paths:
  /addresses:
    get:
      operationId: listAddresses
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "./common/models.yaml#/components/schemas/Address"
`
	mainPath := filepath.Join(specDir, "openapi.yaml")
	if err := os.WriteFile(mainPath, []byte(mainSpec), 0o644); err != nil {
		t.Fatalf("write main openapi.yaml: %v", err)
	}

	p := createOpenAPITestProject(t)
	plan, err := StageOpenAPIFileImport(p, mainPath, OpenAPIImportOptions{
		InteropOptions: ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIFileImport: %v", err)
	}
	if !plan.Preview().CanApply {
		t.Fatalf("expected valid multi-file relative $ref import to succeed, conflicts=%v", plan.Preview().Conflicts)
	}

	// 2. Remote http/https $ref is blocked offline and recorded in MissingDependencies
	remoteRefSpec := `openapi: 3.1.2
info:
  title: Remote Ref Spec
  version: 1.0.0
paths:
  /remote:
    get:
      operationId: getRemote
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "https://example.com/schemas/external.json#/User"
`
	valRemote, err := ValidateOpenAPISpec([]byte(remoteRefSpec), OpenAPIValidationOptions{BaseDir: specDir})
	if err != nil {
		t.Fatalf("ValidateOpenAPISpec remote: %v", err)
	}
	if valRemote.Valid || len(valRemote.MissingDependencies) != 1 || !valRemote.MissingDependencies[0].IsRemote {
		t.Fatalf("expected remote $ref blocked with 1 remote MissingDependency, got %+v", valRemote)
	}

	// 3. Path traversal `../` $ref is rejected
	traversalSpec := `openapi: 3.1.2
info:
  title: Traversal Spec
  version: 1.0.0
paths:
  /escape:
    get:
      operationId: getEscape
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "../secret.yaml"
`
	valTrav, err := ValidateOpenAPISpec([]byte(traversalSpec), OpenAPIValidationOptions{BaseDir: specDir})
	if err != nil {
		t.Fatalf("ValidateOpenAPISpec traversal: %v", err)
	}
	if valTrav.Valid {
		t.Fatalf("expected ../ path traversal $ref to fail validation")
	}

	// 4. Symlink escape inside specDir is rejected
	symlinkPath := filepath.Join(specDir, "common", "escaped-link.yaml")
	if err := os.Symlink(filepath.Join(outsideDir, "secret.yaml"), symlinkPath); err == nil {
		symlinkSpec := `openapi: 3.1.2
info:
  title: Symlink Spec
  version: 1.0.0
paths:
  /symlink:
    get:
      operationId: getSymlink
      responses:
        "200":
          description: OK
          content:
            application/json:
              schema:
                $ref: "./common/escaped-link.yaml"
`
		valSym, err := ValidateOpenAPISpec([]byte(symlinkSpec), OpenAPIValidationOptions{BaseDir: specDir})
		if err != nil {
			t.Fatalf("ValidateOpenAPISpec symlink: %v", err)
		}
		if valSym.Valid {
			t.Fatalf("expected symlink $ref to fail validation")
		}
	}

	// 5. Circular $ref detection during runtime schema validation
	cycleSchema := `{
  "$defs": {
    "LoopA": {"$ref": "#/$defs/LoopB"},
    "LoopB": {"$ref": "#/$defs/LoopA"}
  },
  "$ref": "#/$defs/LoopA"
}`
	resCycle, err := DefaultSchemaValidator{}.ValidateInstance([]byte(cycleSchema), []byte(`{"x":1}`), SchemaValidationOptions{})
	if !errors.Is(err, ErrSchemaRefCycle) || resCycle == nil || resCycle.Valid {
		t.Fatalf("expected ErrSchemaRefCycle on circular $ref, got res=%+v, err=%v", resCycle, err)
	}
}

func TestResponseSchemaValidatorAndExactInstancePaths(t *testing.T) {
	p := createOpenAPITestProject(t)

	schemaJSON := `{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["data", "items"],
  "additionalProperties": false,
  "dependentRequired": {
    "creditCard": ["billingAddress"]
  },
  "properties": {
    "data": {
      "type": "object",
      "required": ["id", "email", "status"],
      "properties": {
        "id": {"type": "integer", "minimum": 10},
        "email": {"type": "string", "format": "email"},
        "status": {"type": "string", "enum": ["active", "pending"]}
      }
    },
    "items": {
      "type": "array",
      "minItems": 1,
      "uniqueItems": true,
      "prefixItems": [
        {"type": "string", "minLength": 2}
      ],
      "items": {"type": "integer"}
    },
    "creditCard": {"type": "string"},
    "billingAddress": {"type": "string"},
    "choice": {
      "oneOf": [
        {"type": "integer", "multipleOf": 5},
        {"type": "string", "pattern": "^opt-[0-9]+$"}
      ]
    }
  }
}`
	if err := p.Store.WriteAtomic("schemas/order.schema.json", []byte(schemaJSON)); err != nil {
		t.Fatalf("write order.schema.json: %v", err)
	}

	req := &Request{
		SchemaVersion: 1,
		ID:            "get-order",
		Name:          "Get Order",
		Protocol:      "http",
		HTTP: &HTTPDef{
			Method: "GET",
			URL:    "{{baseUrl}}/orders/1",
		},
	}
	contract := &OperationContract{
		OperationID:  "getOrder",
		PathTemplate: "/orders/{id}",
		Responses: map[string]ResponseContract{
			"200": {
				Description: "Order response",
				Headers: map[string]HeaderContract{
					"X-Rate-Limit": {
						Required: true,
						Schema:   map[string]any{"type": "integer"},
					},
				},
				Content: map[string]MediaTypeContract{
					"application/json": {
						SchemaRef: "schemas/order.schema.json",
					},
				},
			},
			"400": {
				Description: "Problem response",
				Content: map[string]MediaTypeContract{
					"application/problem+json": {
						Schema: map[string]any{
							"type":     "object",
							"required": []any{"title", "detail"},
							"properties": map[string]any{
								"title":  map[string]any{"type": "string"},
								"detail": map[string]any{"type": "string"},
							},
						},
					},
				},
			},
		},
	}
	if err := SetRequestOpenAPI(req, contract); err != nil {
		t.Fatalf("SetRequestOpenAPI: %v", err)
	}

	// 1. Valid 200 JSON response + valid required header
	validBody := `{"data":{"id":42,"email":"user@example.com","status":"active"},"items":["ab",1,2],"choice":15}`
	resOK, err := ValidateHTTPResponseContract(p, req, 200, []KV{
		{Name: "Content-Type", Value: "application/json; charset=utf-8"},
		{Name: "X-Rate-Limit", Value: "100"},
	}, []byte(validBody), SchemaValidationOptions{ValidateFormats: true})
	if err != nil {
		t.Fatalf("ValidateHTTPResponseContract valid: %v", err)
	}
	if !resOK.Valid || len(resOK.Violations) != 0 {
		t.Fatalf("expected valid response, got violations=%+v", resOK.Violations)
	}

	// 2. Invalid 200 JSON response: check exact instancePath (`$.data.id`, `$.items[0]`, `$.billingAddress`, `$.headers['X-Rate-Limit']`, etc.)
	invalidBody := `{"data":{"id":"not-an-int","email":"bad-email","status":"archived"},"items":["a",5,5],"creditCard":"4111","extraField":true,"choice":7}`
	resBad, err := ValidateHTTPResponseContract(p, req, 200, []KV{
		{Name: "Content-Type", Value: "application/json"},
	}, []byte(invalidBody), SchemaValidationOptions{ValidateFormats: true})
	if err != nil {
		t.Fatalf("ValidateHTTPResponseContract invalid: %v", err)
	}
	if resBad.Valid {
		t.Fatalf("expected invalid response")
	}

	pathsFound := map[string]SchemaValidationViolation{}
	for _, v := range resBad.Violations {
		pathsFound[v.InstancePath] = v
	}
	for _, expectedPath := range []string{
		"$.headers['X-Rate-Limit']",
		"$.data.id",
		"$.data.email",
		"$.data.status",
		"$.items[0]",
		"$.items",
		"$.billingAddress",
		"$.extraField",
		"$.choice",
	} {
		if _, ok := pathsFound[expectedPath]; !ok {
			t.Fatalf("expected violation at exact instancePath %q, got violations: %+v", expectedPath, resBad.Violations)
		}
	}
	if pathsFound["$.data.id"].Expected != "integer" || pathsFound["$.data.id"].Actual != "string" {
		t.Fatalf("expected $.data.id violation Expected=integer Actual=string, got %+v", pathsFound["$.data.id"])
	}

	// 3. Test 400 status + application/problem+json media type selection
	res400, err := ValidateHTTPResponseContract(p, req, 400, []KV{
		{Name: "Content-Type", Value: "application/problem+json"},
	}, []byte(`{"title":"Bad Request"}`), SchemaValidationOptions{})
	if err != nil {
		t.Fatalf("ValidateHTTPResponseContract 400: %v", err)
	}
	if res400.Valid || len(res400.Violations) != 1 || res400.Violations[0].InstancePath != "$.detail" {
		t.Fatalf("expected missing $.detail on 400 problem+json response, got %+v", res400)
	}
}

func TestOpenAPICoverageReportAndNonHTTPExclusion(t *testing.T) {
	p := createOpenAPITestProject(t)

	// Add 2 HTTP requests and 2 non-HTTP requests (graphql, grpc)
	httpFull := &Request{
		SchemaVersion: 1,
		ID:            "http-documented",
		Name:          "Documented HTTP Op",
		Protocol:      "http",
		HTTP:          &HTTPDef{Method: "GET", URL: "{{baseUrl}}/items"},
		Assert: []yaml.Node{
			{Kind: yaml.MappingNode, Content: []*yaml.Node{
				{Kind: yaml.ScalarNode, Value: "type"},
				{Kind: yaml.ScalarNode, Value: "status"},
			}},
		},
	}
	_ = SetRequestOpenAPI(httpFull, &OperationContract{
		OperationID:  "listItems",
		Summary:      "List items",
		Description:  "Full contract with schema and example",
		PathTemplate: "/items",
		Responses: map[string]ResponseContract{
			"200": {
				Description: "OK",
				Content: map[string]MediaTypeContract{
					"application/json": {
						Schema:  map[string]any{"type": "array"},
						Example: []any{"a"},
					},
				},
			},
		},
	})
	p.Requests[httpFull.ID] = &RequestEntry{Request: httpFull, RelPath: "collections/http-documented.request.yaml"}

	httpBare := &Request{
		SchemaVersion: 1,
		ID:            "http-bare",
		Name:          "Bare HTTP Op",
		Protocol:      "http",
		HTTP:          &HTTPDef{Method: "POST", URL: "{{baseUrl}}/items"},
	}
	p.Requests[httpBare.ID] = &RequestEntry{Request: httpBare, RelPath: "collections/http-bare.request.yaml"}

	gqlReq := &Request{
		SchemaVersion: 1,
		ID:            "gql-query",
		Name:          "GraphQL Query",
		Protocol:      "graphql",
	}
	p.Requests[gqlReq.ID] = &RequestEntry{Request: gqlReq, RelPath: "collections/gql-query.request.yaml"}

	grpcReq := &Request{
		SchemaVersion: 1,
		ID:            "grpc-call",
		Name:          "gRPC Unary Call",
		Protocol:      "grpc",
	}
	p.Requests[grpcReq.ID] = &RequestEntry{Request: grpcReq, RelPath: "collections/grpc-call.request.yaml"}

	cov, err := ComputeOpenAPICoverage(p)
	if err != nil {
		t.Fatalf("ComputeOpenAPICoverage: %v", err)
	}
	if cov.TotalRequests != 4 || cov.ExportableHTTPOperations != 2 || cov.NotApplicableCount != 2 {
		t.Fatalf("expected Total=4, ExportableHTTP=2, NotApplicable=2; got %+v", cov)
	}
	if cov.WithDescriptionCount != 1 || cov.WithResponseSchemaCount != 1 || cov.WithExamplesCount != 1 || cov.WithTestAssertionsCount != 1 {
		t.Fatalf("expected 1/2 (50%%) across coverage metrics, got %+v", cov)
	}
	if cov.DescriptionCoveragePct != 50.0 || cov.ResponseSchemaCoveragePct != 50.0 {
		t.Fatalf("expected 50%% coverage denominator based on exportable HTTP ops, got desc=%v schema=%v", cov.DescriptionCoveragePct, cov.ResponseSchemaCoveragePct)
	}

	// Verify ExportOpenAPI skips non-HTTP requests and emits info warnings without fake REST paths
	exp, err := ExportOpenAPI(p, OpenAPIExportOptions{TargetVersion: OpenAPIVersion31, Format: "yaml"})
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	if len(exp.Warnings) != 2 {
		t.Fatalf("expected 2 non-HTTP protocol warnings, got %d", len(exp.Warnings))
	}
	if strings.Contains(string(exp.Bytes), "gql-query") || strings.Contains(string(exp.Bytes), "grpc-call") {
		t.Fatalf("non-HTTP requests must not be exported as fake OpenAPI paths:\n%s", string(exp.Bytes))
	}
}

func TestExternalSpecModeAndThreeWaySyncPreview(t *testing.T) {
	p := createOpenAPITestProject(t)

	specV1 := `openapi: 3.1.2
info:
  title: Sync Service
  version: 1.0.0
paths:
  /users/{id}:
    get:
      operationId: getUser
      summary: Get user v1
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: format
          in: query
          schema:
            type: string
            default: summary
      responses:
        "200":
          description: OK
  /users:
    post:
      operationId: createUser
      summary: Create user v1
      parameters:
        - name: mode
          in: query
          schema:
            type: string
            default: normal
      responses:
        "201":
          description: Created
`
	planV1, err := StageOpenAPIImport(p, []byte(specV1), "sync.yaml", OpenAPIImportOptions{
		SourceMode:     SourceModeExternalSpec,
		SourceFileRel:  "sync.yaml",
		InteropOptions: ImportOptions{ConflictPolicy: ConflictOverwrite},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport v1: %v", err)
	}
	if _, err := planV1.Apply(); err != nil {
		t.Fatalf("Apply v1: %v", err)
	}

	// Local edits:
	// 1) On `get-user`: add local execution Timeout="15s", Variables={"region":"ap"}, and bind query `format={{outputFormat}}` (non-conflicting with upstream summary change)
	getEntry := p.Requests["get-user"]
	getEntry.Request.Timeout = "15s"
	getEntry.Request.Variables = map[string]string{"region": "ap"}
	getEntry.Request.HTTP.Query = []KV{{Name: "format", Value: "{{outputFormat}}", Enabled: BoolPtr(true)}}
	getContract, _ := GetRequestOpenAPI(getEntry.Request)
	if _, err := SaveRequestWithOpenAPI(p, getEntry, getEntry.Request, getContract, getEntry.BaselineDigest); err != nil {
		t.Fatalf("save local get-user edits: %v", err)
	}

	// 2) On `create-user`: modify `mode` query locally to `fast` while upstream v2 modifies `mode` default to `strict` -> 3-way conflict!
	createEntry := p.Requests["create-user"]
	createEntry.Request.HTTP.Query = []KV{{Name: "mode", Value: "fast", Enabled: BoolPtr(true)}}
	createContract, _ := GetRequestOpenAPI(createEntry.Request)
	if _, err := SaveRequestWithOpenAPI(p, createEntry, createEntry.Request, createContract, createEntry.BaselineDigest); err != nil {
		t.Fatalf("save local create-user edits: %v", err)
	}

	specV2 := `openapi: 3.1.2
info:
  title: Sync Service
  version: 1.1.0
paths:
  /users/{id}:
    get:
      operationId: getUser
      summary: Get user v2 with expanded profile
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
        - name: format
          in: query
          schema:
            type: string
            default: summary
      responses:
        "200":
          description: OK
    delete:
      operationId: deleteUser
      summary: Delete user
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
      responses:
        "204":
          description: Deleted
  /users:
    post:
      operationId: createUser
      summary: Create user v2
      parameters:
        - name: mode
          in: query
          schema:
            type: string
            default: strict
      responses:
        "201":
          description: Created
`

	// Stage 3-way sync with ConflictFail: must detect 1 added (deleteUser), 1 updated (getUser), 1 conflict (createUser), and block CanApply
	syncPlanFail, syncPrev, err := StageOpenAPISync(p, []byte(specV2), "sync.yaml", OpenAPIImportOptions{
		SourceMode:     SourceModeExternalSpec,
		SourceFileRel:  "sync.yaml",
		InteropOptions: ImportOptions{ConflictPolicy: ConflictFail},
	})
	if err != nil {
		t.Fatalf("StageOpenAPISync fail policy: %v", err)
	}
	if syncPrev.AddedCount != 1 || syncPrev.UpdatedCount != 1 || syncPrev.ConflictCount != 1 {
		t.Fatalf("expected Added=1, Updated=1, Conflict=1; got %+v", syncPrev)
	}
	if syncPlanFail.Preview().CanApply {
		t.Fatalf("expected CanApply=false when 3-way conflict exists under ConflictFail")
	}

	// Stage 3-way sync with ConflictSkip: skips conflicting `createUser`, applies `getUser` update + `deleteUser` addition, and preserves local Timeout/Variables/query binding on `getUser`!
	syncPlanSkip, _, err := StageOpenAPISync(p, []byte(specV2), "sync.yaml", OpenAPIImportOptions{
		SourceMode:     SourceModeExternalSpec,
		SourceFileRel:  "sync.yaml",
		InteropOptions: ImportOptions{ConflictPolicy: ConflictSkip},
	})
	if err != nil {
		t.Fatalf("StageOpenAPISync skip policy: %v", err)
	}
	if !syncPlanSkip.Preview().CanApply {
		t.Fatalf("expected CanApply=true under ConflictSkip, conflicts=%v", syncPlanSkip.Preview().Conflicts)
	}
	syncReceipt, err := syncPlanSkip.Apply()
	if err != nil {
		t.Fatalf("syncPlanSkip.Apply: %v", err)
	}

	// Verify `delete-user` was added, `get-user` updated with preserved local fields, and `create-user` kept local `fast`
	if _, ok := p.Requests["delete-user"]; !ok {
		t.Fatalf("expected delete-user created by sync")
	}
	updatedGet := p.Requests["get-user"].Request
	if updatedGet.Name != "Get user v2 with expanded profile" {
		t.Fatalf("expected get-user summary updated from upstream v2, got %q", updatedGet.Name)
	}
	if updatedGet.Timeout != "15s" || updatedGet.Variables["region"] != "ap" {
		t.Fatalf("expected local Timeout and Variables preserved on get-user, got timeout=%q vars=%v", updatedGet.Timeout, updatedGet.Variables)
	}
	if len(updatedGet.HTTP.Query) == 0 || updatedGet.HTTP.Query[0].Value != "{{outputFormat}}" {
		t.Fatalf("expected local {{outputFormat}} query binding preserved on get-user, got %+v", updatedGet.HTTP.Query)
	}
	if p.Requests["create-user"].Request.HTTP.Query[0].Value != "fast" {
		t.Fatalf("expected skipped conflicting create-user to retain local 'fast' value")
	}

	// Rollback sync receipt and verify project returns to v1 state
	if err := syncReceipt.Rollback(); err != nil {
		t.Fatalf("syncReceipt.Rollback: %v", err)
	}
	if _, ok := p.Requests["delete-user"]; ok {
		t.Fatalf("expected delete-user removed after sync Rollback")
	}
}

func TestOpenAPISecretRedactionInImportExportAndPreview(t *testing.T) {
	p := createOpenAPITestProject(t)
	red := NewRedactor()
	rawSecret := "sk_live_SUPER_SECRET_TOKEN_998877"
	red.RegisterSecret(rawSecret)

	specWithLeak := `openapi: 3.1.2
info:
  title: Secret Leak Test
  description: "Contains leaked token sk_live_SUPER_SECRET_TOKEN_998877 in description"
  version: 1.0.0
paths:
  /login:
    post:
      operationId: loginUser
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
            example:
              username: admin
              password: "my_inline_password_12345"
              token: "sk_live_SUPER_SECRET_TOKEN_998877"
      responses:
        "200":
          description: OK
`
	plan, err := StageOpenAPIImport(p, []byte(specWithLeak), "leak.yaml", OpenAPIImportOptions{
		InteropOptions: ImportOptions{
			ConflictPolicy: ConflictOverwrite,
			Redactor:       red,
		},
	})
	if err != nil {
		t.Fatalf("StageOpenAPIImport: %v", err)
	}
	if _, err := plan.Apply(); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	exp, err := ExportOpenAPI(p, OpenAPIExportOptions{
		TargetVersion:   OpenAPIVersion31,
		Format:          "yaml",
		IncludeExamples: true,
		Redactor:        red,
	})
	if err != nil {
		t.Fatalf("ExportOpenAPI: %v", err)
	}
	if strings.Contains(string(exp.Bytes), rawSecret) || strings.Contains(string(exp.Bytes), "my_inline_password_12345") {
		t.Fatalf("expected raw secrets scrubbed from exported OpenAPI spec, got:\n%s", string(exp.Bytes))
	}
}
