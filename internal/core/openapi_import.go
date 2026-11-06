package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// OpenAPISpecKind classifies the parsed specification family.
type OpenAPISpecKind string

const (
	SpecKindUnknown   OpenAPISpecKind = "unknown"
	SpecKindSwagger20 OpenAPISpecKind = "swagger-2.0"
	SpecKindOpenAPI30 OpenAPISpecKind = "openapi-3.0"
	SpecKindOpenAPI31 OpenAPISpecKind = "openapi-3.1"
	SpecKindOpenAPI32 OpenAPISpecKind = "openapi-3.2"
)

// Standard HTTP methods permitted as fixed fields on an OpenAPI 3.1 PathItem.
var openAPI31StandardMethods = []string{
	"get", "put", "post", "delete", "options", "head", "patch", "trace",
}

// Standard HTTP methods permitted as fixed fields on an OpenAPI 3.2 PathItem
// (adds "query" plus `additionalOperations` map for custom methods).
var openAPI32StandardMethods = []string{
	"get", "put", "post", "delete", "options", "head", "patch", "trace", "query",
}

// OpenAPIValidationOptions configures offline validation of an OpenAPI or
// Swagger specification.
type OpenAPIValidationOptions struct {
	BaseDir        string
	CurrentFileRel string
	MaxDepth       int
	MaxRefFiles    int
	MaxSpecBytes   int64
}

func (o OpenAPIValidationOptions) effective() OpenAPIValidationOptions {
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxSchemaDepth
	}
	if o.MaxRefFiles <= 0 {
		o.MaxRefFiles = DefaultMaxSchemaRefFiles
	}
	if o.MaxSpecBytes <= 0 {
		o.MaxSpecBytes = DefaultMaxSchemaBytes
	}
	return o
}

// OpenAPIValidationReport summarizes structural, version, path-template,
// duplicate-operationId, security, and offline $ref validation of a spec.
type OpenAPIValidationReport struct {
	Valid               bool                   `json:"valid" yaml:"valid"`
	Kind                OpenAPISpecKind        `json:"kind" yaml:"kind"`
	Version             string                 `json:"version" yaml:"version"`
	Title               string                 `json:"title,omitempty" yaml:"title,omitempty"`
	APIVersion          string                 `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	OperationCount      int                    `json:"operationCount" yaml:"operationCount"`
	Issues              []Issue                `json:"issues,omitempty" yaml:"issues,omitempty"`
	Losses              []LossItem             `json:"losses,omitempty" yaml:"losses,omitempty"`
	MissingDependencies []MissingRefDependency `json:"missingDependencies,omitempty" yaml:"missingDependencies,omitempty"`
	Features32Used      []string               `json:"features32Used,omitempty" yaml:"features32Used,omitempty"`
}

// OpenAPIImportOptions configures OpenAPI 3.1 / 3.2 / 3.0 & Swagger 2.0 import.
type OpenAPIImportOptions struct {
	InteropOptions   ImportOptions
	BaseDir          string
	SourceFileRel    string
	SourceMode       SourceMode // SourceModeNative (default) or SourceModeExternalSpec
	StrictLossReject bool       // Reject import (CanApply=false) when any lossy/unsupported feature is present
	GroupByTag       bool       // Create subfolder per primary tag under TargetFolderRel
}

func (o OpenAPIImportOptions) normalized() OpenAPIImportOptions {
	o.InteropOptions = o.InteropOptions.normalized()
	if o.SourceMode == "" {
		o.SourceMode = SourceModeNative
	}
	return o
}

var (
	pathTemplateParamRe = regexp.MustCompile(`\{([^{}]+)\}`)
	camelBoundaryRe     = regexp.MustCompile(`([a-z0-9])([A-Z])`)
)

func sanitizeOpenAPISlug(raw, fallback string) string {
	s := camelBoundaryRe.ReplaceAllString(strings.TrimSpace(raw), "${1}-${2}")
	return sanitizeSlug(s, fallback)
}

// ClassifyOpenAPIVersion determines the OpenAPISpecKind from raw version strings.
func ClassifyOpenAPIVersion(openapiVer, swaggerVer string) (OpenAPISpecKind, string, error) {
	oa := strings.TrimSpace(openapiVer)
	sw := strings.TrimSpace(swaggerVer)
	if sw != "" {
		if sw == "2.0" {
			return SpecKindSwagger20, sw, nil
		}
		return SpecKindUnknown, sw, fmt.Errorf("unsupported Swagger version %q (only Swagger 2.0 is supported for migration)", sw)
	}
	if oa == "" {
		return SpecKindUnknown, "", errors.New("missing 'openapi' or 'swagger' version field")
	}
	switch {
	case strings.HasPrefix(oa, "3.0."):
		return SpecKindOpenAPI30, oa, nil
	case strings.HasPrefix(oa, "3.1."):
		return SpecKindOpenAPI31, oa, nil
	case strings.HasPrefix(oa, "3.2."):
		return SpecKindOpenAPI32, oa, nil
	default:
		return SpecKindUnknown, oa, fmt.Errorf("unsupported OpenAPI version %q (supported: 3.1.x, 3.2.x, 3.0.x, and Swagger 2.0)", oa)
	}
}

// ValidateOpenAPISpec parses and validates an OpenAPI 3.1 / 3.2 / 3.0 or
// Swagger 2.0 document (JSON or YAML), resolving local and multi-file relative
// $refs offline and checking version-specific constraints.
func ValidateOpenAPISpec(data []byte, opts OpenAPIValidationOptions) (*OpenAPIValidationReport, error) {
	opts = opts.effective()
	if int64(len(data)) > opts.MaxSpecBytes {
		return nil, configErrorf("specification exceeds size limit (%d > %d bytes)", len(data), opts.MaxSpecBytes)
	}
	rawNode, err := decodeStructuredValue(data)
	if err != nil {
		return nil, fmt.Errorf("parse specification: %w", err)
	}
	root, ok := rawNode.(map[string]any)
	if !ok || len(root) == 0 {
		return nil, errors.New("specification root must be a JSON/YAML object")
	}

	oaVer, _ := root["openapi"].(string)
	swVer, _ := root["swagger"].(string)
	kind, ver, verr := ClassifyOpenAPIVersion(oaVer, swVer)

	report := &OpenAPIValidationReport{
		Kind:    kind,
		Version: ver,
	}
	addErr := func(code, p, msg string) {
		report.Issues = append(report.Issues, Issue{Level: "error", Code: code, Path: p, Message: msg})
	}
	addWarn := func(code, p, msg string) {
		report.Issues = append(report.Issues, Issue{Level: "warning", Code: code, Path: p, Message: msg})
	}
	addLoss := func(code string, sev LossSeverity, p, field, msg string) {
		report.Losses = append(report.Losses, LossItem{
			Code:       code,
			Severity:   sev,
			SourcePath: p,
			Field:      field,
			Message:    msg,
		})
	}
	mark32 := func(feat string) {
		if !slices.Contains(report.Features32Used, feat) {
			report.Features32Used = append(report.Features32Used, feat)
		}
	}

	if verr != nil {
		addErr("openapi-version-unsupported", "$", verr.Error())
		report.Valid = false
		return report, nil
	}

	// Validate info object
	infoMap, _ := root["info"].(map[string]any)
	if infoMap == nil {
		addErr("openapi-info-missing", "$.info", "missing required 'info' object")
	} else {
		title, _ := infoMap["title"].(string)
		apiVer, _ := infoMap["version"].(string)
		report.Title = strings.TrimSpace(title)
		report.APIVersion = strings.TrimSpace(apiVer)
		if report.Title == "" {
			addErr("openapi-info-title", "$.info.title", "info.title is required and must be non-empty")
		}
		if report.APIVersion == "" {
			addErr("openapi-info-version", "$.info.version", "info.version is required and must be non-empty")
		}
		if lic, ok := infoMap["license"].(map[string]any); ok {
			idStr, _ := lic["identifier"].(string)
			urlStr, _ := lic["url"].(string)
			if idStr != "" && urlStr != "" {
				addErr("openapi-license-conflict", "$.info.license", "info.license cannot specify both 'identifier' and 'url' in OpenAPI 3.1/3.2")
			}
		}
	}

	// Check 3.2 root $self
	if selfURI, ok := root["$self"].(string); ok && strings.TrimSpace(selfURI) != "" {
		mark32("$self")
		if kind != SpecKindOpenAPI32 {
			addErr("openapi-3.2-feature-in-3.1", "$.$self", fmt.Sprintf("root '$self' requires OpenAPI 3.2.x, but document declares %s", ver))
		}
	}

	// Check servers for 3.2 `name`
	if servers, ok := root["servers"].([]any); ok {
		for i, srvRaw := range servers {
			if srv, ok := srvRaw.(map[string]any); ok {
				if name, ok := srv["name"].(string); ok && strings.TrimSpace(name) != "" {
					mark32("Server.name")
					if kind != SpecKindOpenAPI32 {
						addErr("openapi-3.2-feature-in-3.1", fmt.Sprintf("$.servers[%d].name", i), fmt.Sprintf("Server 'name' requires OpenAPI 3.2.x, but document declares %s", ver))
					}
				}
			}
		}
	}

	// Check tags for 3.2 `summary`, `parent`, `kind`
	if tags, ok := root["tags"].([]any); ok {
		for i, tagRaw := range tags {
			if tag, ok := tagRaw.(map[string]any); ok {
				for _, f32 := range []string{"summary", "parent", "kind"} {
					if v, ok := tag[f32].(string); ok && strings.TrimSpace(v) != "" {
						mark32("Tag." + f32)
						if kind != SpecKindOpenAPI32 {
							addErr("openapi-3.2-feature-in-3.1", fmt.Sprintf("$.tags[%d].%s", i, f32), fmt.Sprintf("Tag '%s' requires OpenAPI 3.2.x, but document declares %s", f32, ver))
						}
					}
				}
			}
		}
	}

	// Check webhooks (3.1 / 3.2)
	if webhooks, ok := root["webhooks"].(map[string]any); ok && len(webhooks) > 0 {
		if kind == SpecKindSwagger20 || kind == SpecKindOpenAPI30 {
			addErr("openapi-webhooks-version", "$.webhooks", "top-level 'webhooks' requires OpenAPI 3.1+")
		}
		for _, whName := range sortedMapKeys(webhooks) {
			addLoss("openapi-webhook-unsupported", LossUnsupported, "$.webhooks."+whName, "webhooks", fmt.Sprintf("webhook %q is a server-push callback contract and cannot be executed as a client HTTP request in 1.0-A", whName))
		}
	}

	// Check security schemes
	declaredSecSchemes := map[string]string{}
	if kind == SpecKindSwagger20 {
		if secDefs, ok := root["securityDefinitions"].(map[string]any); ok {
			for _, name := range sortedMapKeys(secDefs) {
				if sm, ok := secDefs[name].(map[string]any); ok {
					t, _ := sm["type"].(string)
					declaredSecSchemes[name] = strings.ToLower(strings.TrimSpace(t))
					if declaredSecSchemes[name] == "oauth2" {
						addLoss("swagger2-oauth2-routed-to-a04", LossUnsupported, "$.securityDefinitions."+name, "type", fmt.Sprintf("Swagger 2.0 oauth2 securityDefinition %q requires A04 OAuth2 execution; metadata preserved", name))
					}
				}
			}
		}
	} else {
		if comp, ok := root["components"].(map[string]any); ok {
			if secSchemes, ok := comp["securitySchemes"].(map[string]any); ok {
				for _, name := range sortedMapKeys(secSchemes) {
					sm, ok := secSchemes[name].(map[string]any)
					if !ok {
						continue
					}
					t, _ := sm["type"].(string)
					declaredSecSchemes[name] = strings.ToLower(strings.TrimSpace(t))
					for _, f32 := range []string{"oauth2MetadataUrl", "deprecated"} {
						if _, has := sm[f32]; has {
							mark32("SecurityScheme." + f32)
							if kind != SpecKindOpenAPI32 {
								addErr("openapi-3.2-feature-in-3.1", "$.components.securitySchemes."+name+"."+f32, fmt.Sprintf("SecurityScheme '%s' requires OpenAPI 3.2.x, but document declares %s", f32, ver))
							}
						}
					}
					if flows, ok := sm["flows"].(map[string]any); ok {
						if _, hasDev := flows["deviceAuthorization"]; hasDev {
							mark32("SecurityScheme.flows.deviceAuthorization")
							if kind != SpecKindOpenAPI32 {
								addErr("openapi-3.2-feature-in-3.1", "$.components.securitySchemes."+name+".flows.deviceAuthorization", fmt.Sprintf("OAuth2 'deviceAuthorization' flow requires OpenAPI 3.2.x, but document declares %s", ver))
							}
						}
					}
					if declaredSecSchemes[name] == "oauth2" || declaredSecSchemes[name] == "openidconnect" || declaredSecSchemes[name] == "mutualtls" {
						addLoss("openapi-advanced-auth-routed-to-a04", LossUnsupported, "$.components.securitySchemes."+name, "type", fmt.Sprintf("securityScheme %q (%s) runtime execution belongs to A04/B04; contract metadata preserved", name, declaredSecSchemes[name]))
					}
				}
			}
		}
	}

	// Setup $ref resolver context to verify all $refs in the document
	refCtx := &schemaEvalContext{
		opts: SchemaValidationOptions{
			BaseDir:        opts.BaseDir,
			CurrentFileRel: opts.CurrentFileRel,
			RootDocument:   root,
			MaxDepth:       opts.MaxDepth,
			MaxRefFiles:    opts.MaxRefFiles,
			MaxSchemaBytes: opts.MaxSpecBytes,
		},
		rootDoc:    root,
		currentRel: opts.CurrentFileRel,
		fileCache:  map[string]any{},
		activeRefs: map[string]bool{},
	}
	verifyAllRefsInNode(refCtx, root, "$", addErr)
	report.MissingDependencies = append(report.MissingDependencies, refCtx.missingDeps...)

	// Validate paths & operations
	pathsMap, hasPaths := root["paths"].(map[string]any)
	hasWebhooks := false
	if wh, ok := root["webhooks"].(map[string]any); ok && len(wh) > 0 {
		hasWebhooks = true
	}
	hasComponents := false
	if comp, ok := root["components"].(map[string]any); ok && len(comp) > 0 {
		hasComponents = true
	}
	if !hasPaths && !(kind == SpecKindOpenAPI31 || kind == SpecKindOpenAPI32) {
		addErr("openapi-paths-missing", "$.paths", "missing required 'paths' object")
	} else if !hasPaths && !hasWebhooks && !hasComponents {
		addErr("openapi-paths-missing", "$", "OpenAPI 3.1/3.2 document must contain at least one of 'paths', 'webhooks', or 'components'")
	}

	seenOperationIDs := map[string]string{}
	for _, rawPath := range sortedMapKeys(pathsMap) {
		pathJSONPtr := "$.paths[" + rawPath + "]"
		if !strings.HasPrefix(rawPath, "/") {
			addErr("openapi-path-slash", pathJSONPtr, fmt.Sprintf("path template %q must start with '/'", rawPath))
		}
		pathItemRaw := pathsMap[rawPath]
		pathItem, ok := derefMapNode(refCtx, pathItemRaw, pathJSONPtr)
		if !ok {
			continue
		}

		pathLevelParams := collectNormalizedParameters(refCtx, pathItem["parameters"], pathJSONPtr+".parameters", kind, addErr, addLoss, mark32)

		// Inspect operations on PathItem
		opsOnPath := extractPathItemOperations(pathItem, kind, ver, pathJSONPtr, addErr, mark32)
		for _, opEntry := range opsOnPath {
			report.OperationCount++
			opLoc := fmt.Sprintf("%s.%s", pathJSONPtr, strings.ToLower(opEntry.method))
			opMap := opEntry.op

			if opID, ok := opMap["operationId"].(string); ok && strings.TrimSpace(opID) != "" {
				opID = strings.TrimSpace(opID)
				if prevLoc, dup := seenOperationIDs[opID]; dup {
					addErr("openapi-duplicate-operation-id", opLoc+".operationId", fmt.Sprintf("duplicate operationId %q (already defined at %s)", opID, prevLoc))
				} else {
					seenOperationIDs[opID] = opLoc
				}
			}

			opParams := collectNormalizedParameters(refCtx, opMap["parameters"], opLoc+".parameters", kind, addErr, addLoss, mark32)
			mergedParams := mergePathAndOpParameters(pathLevelParams, opParams)

			// Validate path template placeholders vs `in: path` parameters
			validatePathTemplateParameters(rawPath, mergedParams, opLoc, addErr, addWarn)

			// Validate security requirements reference declared securitySchemes
			validateSecurityRefs(opMap["security"], declaredSecSchemes, opLoc+".security", addErr)

			// Check callbacks / links loss items
			if cbs, ok := opMap["callbacks"].(map[string]any); ok && len(cbs) > 0 {
				addLoss("openapi-callback-unsupported", LossUnsupported, opLoc+".callbacks", "callbacks", "OpenAPI callbacks are asynchronous webhook contracts and are not executable as unary client requests")
			}

			// Inspect requestBody media types (including 3.2 itemSchema/itemEncoding/prefixEncoding)
			if rbRaw, hasRB := opMap["requestBody"]; hasRB {
				if rbMap, ok := derefMapNode(refCtx, rbRaw, opLoc+".requestBody"); ok {
					inspectMediaTypeMap(rbMap["content"], opLoc+".requestBody.content", kind, ver, addErr, addLoss, mark32)
				}
			}

			// Inspect responses
			respMap, _ := opMap["responses"].(map[string]any)
			if len(respMap) == 0 {
				addWarn("openapi-responses-empty", opLoc+".responses", "operation declares no responses")
			} else {
				for _, statusKey := range sortedMapKeys(respMap) {
					rLoc := fmt.Sprintf("%s.responses[%s]", opLoc, statusKey)
					if rObj, ok := derefMapNode(refCtx, respMap[statusKey], rLoc); ok {
						inspectMediaTypeMap(rObj["content"], rLoc+".content", kind, ver, addErr, addLoss, mark32)
						if links, ok := rObj["links"].(map[string]any); ok && len(links) > 0 {
							addLoss("openapi-response-links-unsupported", LossUnsupported, rLoc+".links", "links", "OpenAPI response links are recorded as loss items and not auto-wired into Suite DAGs")
						}
					}
				}
			}
		}
	}

	validateSecurityRefs(root["security"], declaredSecSchemes, "$.security", addErr)

	report.Valid = ErrorCount(report.Issues) == 0 && len(report.MissingDependencies) == 0
	return report, nil
}

type pathOperationEntry struct {
	method string // uppercase HTTP method
	op     map[string]any
}

func extractPathItemOperations(
	pathItem map[string]any,
	kind OpenAPISpecKind,
	ver string,
	pathJSONPtr string,
	addErr func(code, p, msg string),
	mark32 func(feat string),
) []pathOperationEntry {
	var out []pathOperationEntry
	for _, m := range openAPI31StandardMethods {
		if rawOp, ok := pathItem[m]; ok {
			if opMap, ok := rawOp.(map[string]any); ok {
				out = append(out, pathOperationEntry{method: strings.ToUpper(m), op: opMap})
			}
		}
	}
	if rawQueryOp, hasQuery := pathItem["query"]; hasQuery {
		mark32("PathItem.query")
		if kind != SpecKindOpenAPI32 {
			addErr("openapi-3.2-feature-in-3.1", pathJSONPtr+".query", fmt.Sprintf("PathItem 'query' operation requires OpenAPI 3.2.x, but document declares %s", ver))
		}
		if opMap, ok := rawQueryOp.(map[string]any); ok {
			out = append(out, pathOperationEntry{method: "QUERY", op: opMap})
		}
	}
	if rawAddl, hasAddl := pathItem["additionalOperations"]; hasAddl {
		mark32("PathItem.additionalOperations")
		if kind != SpecKindOpenAPI32 {
			addErr("openapi-3.2-feature-in-3.1", pathJSONPtr+".additionalOperations", fmt.Sprintf("PathItem 'additionalOperations' requires OpenAPI 3.2.x, but document declares %s", ver))
		}
		if addlMap, ok := rawAddl.(map[string]any); ok {
			for _, customMeth := range sortedMapKeys(addlMap) {
				lowerMeth := strings.ToLower(strings.TrimSpace(customMeth))
				if slices.Contains(openAPI32StandardMethods, lowerMeth) {
					addErr("openapi-3.2-additional-operation-duplicate-standard", pathJSONPtr+".additionalOperations."+customMeth, fmt.Sprintf("standard method %q must not appear inside additionalOperations in OpenAPI 3.2", customMeth))
					continue
				}
				if err := ValidateHTTPMethod(strings.ToUpper(customMeth)); err != nil {
					addErr("openapi-invalid-custom-method", pathJSONPtr+".additionalOperations."+customMeth, err.Error())
					continue
				}
				if opMap, ok := addlMap[customMeth].(map[string]any); ok {
					out = append(out, pathOperationEntry{method: strings.ToUpper(strings.TrimSpace(customMeth)), op: opMap})
				}
			}
		}
	}
	return out
}

func inspectMediaTypeMap(
	rawContent any,
	loc string,
	kind OpenAPISpecKind,
	ver string,
	addErr func(code, p, msg string),
	addLoss func(code string, sev LossSeverity, p, field, msg string),
	mark32 func(feat string),
) {
	contentMap, ok := rawContent.(map[string]any)
	if !ok {
		return
	}
	for _, mtName := range sortedMapKeys(contentMap) {
		mtObj, ok := contentMap[mtName].(map[string]any)
		if !ok {
			continue
		}
		mtLoc := fmt.Sprintf("%s[%s]", loc, mtName)
		for _, streamField := range []string{"itemSchema", "itemEncoding", "prefixEncoding"} {
			if _, has := mtObj[streamField]; has {
				mark32("MediaType." + streamField)
				if kind != SpecKindOpenAPI32 {
					addErr("openapi-3.2-feature-in-3.1", mtLoc+"."+streamField, fmt.Sprintf("MediaType '%s' requires OpenAPI 3.2.x, but document declares %s", streamField, ver))
				}
				addLoss(
					"openapi-3.2-streaming-item-schema-unsupported",
					LossUnsupported,
					mtLoc+"."+streamField,
					streamField,
					fmt.Sprintf("OpenAPI 3.2 streaming media type construct %q on %q cannot be executed as a unary HTTP assertion in 1.0-A (streaming belongs to 1.0-B); preserved as metadata only", streamField, mtName),
				)
			}
		}
		inspectExamplesMap(mtObj["examples"], mtLoc+".examples", kind, ver, addErr, mark32)
	}
}

func inspectExamplesMap(
	rawExamples any,
	loc string,
	kind OpenAPISpecKind,
	ver string,
	addErr func(code, p, msg string),
	mark32 func(feat string),
) {
	exMap, ok := rawExamples.(map[string]any)
	if !ok {
		return
	}
	for _, exName := range sortedMapKeys(exMap) {
		exObj, ok := exMap[exName].(map[string]any)
		if !ok {
			continue
		}
		for _, f32 := range []string{"dataValue", "serializedValue"} {
			if _, has := exObj[f32]; has {
				mark32("Example." + f32)
				if kind != SpecKindOpenAPI32 {
					addErr("openapi-3.2-feature-in-3.1", loc+"."+exName+"."+f32, fmt.Sprintf("Example '%s' requires OpenAPI 3.2.x, but document declares %s", f32, ver))
				}
			}
		}
	}
}

func verifyAllRefsInNode(ctx *schemaEvalContext, node any, curPath string, addErr func(code, p, msg string)) {
	switch n := node.(type) {
	case map[string]any:
		if refRaw, ok := n["$ref"].(string); ok && strings.TrimSpace(refRaw) != "" {
			refStr := strings.TrimSpace(refRaw)
			canon := ctx.currentRel + "::" + refStr
			if ctx.activeRefs[canon] {
				return
			}
			target, newRoot, newRel, err := ctx.resolveRefTarget(refStr, curPath+".$ref")
			if err != nil {
				code := "openapi-ref-unresolved"
				if errors.Is(err, ErrRemoteRefBlocked) {
					code = "openapi-remote-ref-blocked"
				} else if errors.Is(err, ErrSchemaPathTraversal) {
					code = "openapi-ref-traversal-blocked"
				}
				addErr(code, curPath+".$ref", err.Error())
				return
			}
			prevRoot := ctx.rootDoc
			prevRel := ctx.currentRel
			ctx.activeRefs[canon] = true
			if newRoot != nil {
				ctx.rootDoc = newRoot
			}
			ctx.currentRel = newRel
			verifyAllRefsInNode(ctx, target, curPath+"->"+refStr, addErr)
			ctx.rootDoc = prevRoot
			ctx.currentRel = prevRel
			delete(ctx.activeRefs, canon)
		}
		for _, k := range sortedMapKeys(n) {
			if k == "$ref" {
				continue
			}
			verifyAllRefsInNode(ctx, n[k], curPath+"."+k, addErr)
		}
	case []any:
		for i, elem := range n {
			verifyAllRefsInNode(ctx, elem, fmt.Sprintf("%s[%d]", curPath, i), addErr)
		}
	}
}

func derefMapNode(ctx *schemaEvalContext, raw any, loc string) (map[string]any, bool) {
	m, ok := raw.(map[string]any)
	if !ok || m == nil {
		return nil, false
	}
	refStr, hasRef := m["$ref"].(string)
	if !hasRef || strings.TrimSpace(refStr) == "" {
		return m, true
	}
	target, _, _, err := ctx.resolveRefTarget(strings.TrimSpace(refStr), loc+".$ref")
	if err != nil {
		return m, true
	}
	targetMap, ok := target.(map[string]any)
	if !ok {
		return m, true
	}
	// Merge sibling fields over referenced map (OpenAPI 3.1/3.2 allows siblings next to $ref)
	merged := make(map[string]any, len(targetMap)+len(m))
	for k, v := range targetMap {
		merged[k] = v
	}
	for k, v := range m {
		if k == "$ref" {
			continue
		}
		merged[k] = v
	}
	return merged, true
}

func collectNormalizedParameters(
	ctx *schemaEvalContext,
	rawParams any,
	loc string,
	kind OpenAPISpecKind,
	addErr func(code, p, msg string),
	addLoss func(code string, sev LossSeverity, p, field, msg string),
	mark32 func(feat string),
) []OperationParam {
	list, ok := rawParams.([]any)
	if !ok {
		return nil
	}
	var out []OperationParam
	seen := map[string]bool{}
	for i, item := range list {
		pLoc := fmt.Sprintf("%s[%d]", loc, i)
		pm, ok := derefMapNode(ctx, item, pLoc)
		if !ok {
			continue
		}
		name, _ := pm["name"].(string)
		inLoc, _ := pm["in"].(string)
		name = strings.TrimSpace(name)
		inLoc = strings.ToLower(strings.TrimSpace(inLoc))
		if name == "" || inLoc == "" {
			addErr("openapi-param-invalid", pLoc, "parameter must have non-empty 'name' and 'in'")
			continue
		}
		key := inLoc + ":" + name
		if seen[key] {
			addErr("openapi-param-duplicate", pLoc, fmt.Sprintf("duplicate parameter %q in %q", name, inLoc))
			continue
		}
		seen[key] = true

		// Swagger 2.0 `in: body` and `in: formData` are handled by requestBody migration, not OperationParam
		if kind == SpecKindSwagger20 && (inLoc == "body" || inLoc == "formdata") {
			if colFmt, ok := pm["collectionFormat"].(string); ok && (colFmt == "pipes" || colFmt == "tsv" || colFmt == "ssv") {
				addLoss("swagger2-collection-format-loss", LossWarning, pLoc, "collectionFormat", fmt.Sprintf("Swagger 2.0 collectionFormat %q on parameter %q migrated to standard parameter style", colFmt, name))
			}
			continue
		}
		if inLoc != "path" && inLoc != "query" && inLoc != "header" && inLoc != "cookie" {
			addErr("openapi-param-in", pLoc, fmt.Sprintf("unsupported parameter location %q", inLoc))
			continue
		}
		reqBool, _ := pm["required"].(bool)
		if inLoc == "path" && !reqBool {
			addErr("openapi-path-param-not-required", pLoc, fmt.Sprintf("path parameter %q must have required: true", name))
		}
		if colFmt, ok := pm["collectionFormat"].(string); ok && (colFmt == "pipes" || colFmt == "tsv" || colFmt == "ssv") {
			addLoss("swagger2-collection-format-loss", LossWarning, pLoc, "collectionFormat", fmt.Sprintf("Swagger 2.0 collectionFormat %q on parameter %q migrated to standard parameter style", colFmt, name))
		}

		desc, _ := pm["description"].(string)
		dep, _ := pm["deprecated"].(bool)
		style, _ := pm["style"].(string)
		var explodePtr *bool
		if expVal, ok := pm["explode"].(bool); ok {
			explodePtr = BoolPtr(expVal)
		}

		var schemaMap map[string]any
		var schemaRef string
		if sRaw, ok := pm["schema"].(map[string]any); ok {
			if r, ok := sRaw["$ref"].(string); ok && len(sRaw) == 1 {
				schemaRef = rewriteSwaggerDefinitionRef(r)
			}
			schemaMap = normalizeSchemaTo31(ctx, sRaw)
		} else if kind == SpecKindSwagger20 {
			schemaMap = extractSwagger2ParamSchema(pm)
		}

		inspectExamplesMap(pm["examples"], pLoc+".examples", kind, "", addErr, mark32)

		out = append(out, OperationParam{
			Name:        name,
			In:          inLoc,
			Description: desc,
			Required:    reqBool || inLoc == "path",
			Deprecated:  dep,
			Style:       style,
			Explode:     explodePtr,
			Schema:      schemaMap,
			SchemaRef:   schemaRef,
			Example:     pm["example"],
			Examples:    parseExampleObjectsMap(pm["examples"]),
		})
	}
	return out
}

func mergePathAndOpParameters(pathParams, opParams []OperationParam) []OperationParam {
	byKey := map[string]int{}
	out := make([]OperationParam, 0, len(pathParams)+len(opParams))
	for _, p := range pathParams {
		k := p.In + ":" + p.Name
		byKey[k] = len(out)
		out = append(out, p)
	}
	for _, p := range opParams {
		k := p.In + ":" + p.Name
		if idx, exists := byKey[k]; exists {
			out[idx] = p
		} else {
			byKey[k] = len(out)
			out = append(out, p)
		}
	}
	return out
}

func validatePathTemplateParameters(rawPath string, params []OperationParam, opLoc string, addErr, addWarn func(code, p, msg string)) {
	matches := pathTemplateParamRe.FindAllStringSubmatch(rawPath, -1)
	templateNames := map[string]bool{}
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name != "" {
			templateNames[name] = true
		}
	}
	declaredPathParams := map[string]bool{}
	for _, p := range params {
		if p.In == "path" {
			declaredPathParams[p.Name] = true
			if !templateNames[p.Name] {
				addErr("openapi-path-param-undeclared-in-url", opLoc, fmt.Sprintf("path parameter %q is not present in URL path template %q", p.Name, rawPath))
			}
		}
	}
	for tName := range templateNames {
		if !declaredPathParams[tName] {
			addErr("openapi-path-template-missing-param", opLoc, fmt.Sprintf("URL path template placeholder {%s} in %q has no corresponding 'in: path' parameter definition", tName, rawPath))
		}
	}
}

func validateSecurityRefs(rawSec any, declared map[string]string, loc string, addErr func(code, p, msg string)) {
	secSlice, ok := rawSec.([]any)
	if !ok {
		return
	}
	for i, item := range secSlice {
		reqMap, ok := item.(map[string]any)
		if !ok {
			continue
		}
		for _, schemeName := range sortedMapKeys(reqMap) {
			if _, exists := declared[schemeName]; !exists {
				addErr("openapi-security-scheme-undefined", fmt.Sprintf("%s[%d].%s", loc, i, schemeName), fmt.Sprintf("security requirement references undeclared securityScheme %q", schemeName))
			}
		}
	}
}

// StageOpenAPIFileImport reads an OpenAPI 3.1/3.2/3.0 or Swagger 2.0 file from
// disk (setting BaseDir to its parent directory for offline multi-file $ref
// resolution) and stages a previewable, reversible ImportPlan.
func StageOpenAPIFileImport(p *Project, filePath string, opts OpenAPIImportOptions) (*ImportPlan, error) {
	cleanPath := strings.TrimSpace(filePath)
	if cleanPath == "" {
		return nil, errors.New("empty OpenAPI specification file path")
	}
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("read OpenAPI file: %w", err)
	}
	if opts.BaseDir == "" {
		opts.BaseDir = filepath.Dir(cleanPath)
	}
	if opts.SourceFileRel == "" {
		opts.SourceFileRel = filepath.Base(cleanPath)
	}
	return StageOpenAPIImport(p, data, filepath.Base(cleanPath), opts)
}

// StageOpenAPIImport stages an OpenAPI 3.1, 3.2, 3.0, or Swagger 2.0 specification
// into a GoRequest Project using B08's ImportPlan / B01's ChangeSet / B05's Redactor.
func StageOpenAPIImport(p *Project, data []byte, filename string, opts OpenAPIImportOptions) (*ImportPlan, error) {
	if p == nil || p.Store == nil {
		return nil, errors.New("target project is nil")
	}
	opts = opts.normalized()
	red := opts.InteropOptions.Redactor
	if red == nil {
		red = NewRedactor()
		opts.InteropOptions.Redactor = red
	}

	valReport, err := ValidateOpenAPISpec(data, OpenAPIValidationOptions{
		BaseDir:        opts.BaseDir,
		CurrentFileRel: opts.SourceFileRel,
	})
	if err != nil {
		return nil, err
	}

	interpFormat := FormatOpenAPI
	if valReport.Kind == SpecKindSwagger20 {
		interpFormat = FormatSwagger2
	}

	var losses []LossItem
	losses = append(losses, valReport.Losses...)
	var conflicts []string

	// Convert validation errors and missing offline $ref dependencies into blocking diagnostics
	for _, dep := range valReport.MissingDependencies {
		code := "openapi-missing-local-ref"
		if dep.IsRemote {
			code = "openapi-remote-ref-blocked"
		}
		msg := fmt.Sprintf("Unresolved offline $ref %q at %s: %s", dep.RefURI, dep.SchemaPath, dep.Reason)
		losses = append(losses, LossItem{
			Code:       code,
			Severity:   LossBlocked,
			SourcePath: dep.SourceFile,
			Field:      dep.SchemaPath,
			Message:    msg,
		})
		conflicts = append(conflicts, msg)
	}
	for _, iss := range valReport.Issues {
		if iss.Level == "error" {
			losses = append(losses, LossItem{
				Code:       iss.Code,
				Severity:   LossBlocked,
				SourcePath: iss.Path,
				Message:    iss.Message,
			})
			conflicts = append(conflicts, fmt.Sprintf("%s: %s", iss.Path, iss.Message))
		} else {
			losses = append(losses, LossItem{
				Code:       iss.Code,
				Severity:   LossWarning,
				SourcePath: iss.Path,
				Message:    iss.Message,
			})
		}
	}

	if !valReport.Valid {
		preview := ImportPreview{
			Format:         interpFormat,
			FormatVersion:  valReport.Version,
			Summary:        fmt.Sprintf("Blocked %s (%s) import: %d validation/ref error(s)", valReport.Kind, valReport.Version, len(conflicts)),
			ConflictPolicy: opts.InteropOptions.ConflictPolicy,
			Losses:         losses,
			Conflicts:      conflicts,
			CanApply:       false,
		}
		return &ImportPlan{
			project:  p,
			preview:  preview,
			redactor: red,
		}, nil
	}

	rawNode, _ := decodeStructuredValue(data)
	root := rawNode.(map[string]any)
	refCtx := &schemaEvalContext{
		opts: SchemaValidationOptions{
			BaseDir:        opts.BaseDir,
			CurrentFileRel: opts.SourceFileRel,
			RootDocument:   root,
		}.effective(),
		rootDoc:    root,
		currentRel: opts.SourceFileRel,
		fileCache:  map[string]any{},
		activeRefs: map[string]bool{},
	}

	if valReport.Kind == SpecKindSwagger20 {
		losses = append(losses, LossItem{
			Code:       "swagger-2.0-migrated-to-openapi-3.1",
			Severity:   LossInfo,
			SourcePath: filename,
			Message:    "Swagger 2.0 specification migrated to GoRequest HTTP requests, OpenAPI 3.1 operation contracts, and JSON Schema Draft 2020-12 schemas.",
		})
	} else if valReport.Kind == SpecKindOpenAPI30 {
		losses = append(losses, LossItem{
			Code:       "openapi-3.0-normalized-to-3.1",
			Severity:   LossInfo,
			SourcePath: filename,
			Message:    fmt.Sprintf("OpenAPI %s schemas normalized to OpenAPI 3.1 / JSON Schema Draft 2020-12 (nullable converted to type union).", valReport.Version),
		})
	}

	// If StrictLossReject is enabled and there are any LossUnsupported or LossWarning migration losses, block apply
	strictBlocked := false
	if opts.StrictLossReject {
		for i := range losses {
			if losses[i].Severity == LossUnsupported || losses[i].Severity == LossWarning || losses[i].Severity == LossBlocked {
				losses[i].Severity = LossBlocked
				conflicts = append(conflicts, fmt.Sprintf("[strict-loss-reject] %s (%s): %s", losses[i].Code, losses[i].SourcePath, losses[i].Message))
				strictBlocked = true
			}
		}
	}

	cs := p.Store.NewChangeSet()
	usedIDs := map[string]bool{}
	usedPaths := map[string]bool{}
	var idMappings []IDMapping
	var varNotes []VariableSourceNote
	var secretSlots []string
	var importedReqIDs []string

	addSecretSlot := func(slot string) {
		if slot != "" && !slices.Contains(secretSlots, slot) {
			secretSlots = append(secretSlots, slot)
		}
	}

	targetFolderDir, err := normalizeTargetCollectionFolder(opts.InteropOptions.TargetFolderRel)
	if err != nil {
		return nil, err
	}

	// 1. Stage reusable component schemas / Swagger definitions into `.gorequest/schemas/<slug>.schema.json`
	schemaRefToRelPath := map[string]string{}
	rawSchemasMap := extractRootSchemasMap(root, valReport.Kind)
	for _, schemaName := range sortedMapKeys(rawSchemasMap) {
		rawSchemaObj, ok := derefMapNode(refCtx, rawSchemasMap[schemaName], "#/components/schemas/"+schemaName)
		if !ok {
			continue
		}
		normSchema := normalizeSchemaForProjectFile(refCtx, rawSchemaObj, rawSchemasMap)
		if _, hasSchemaURI := normSchema["$schema"]; !hasSchemaURI {
			normSchema["$schema"] = "https://json-schema.org/draft/2020-12/schema"
		}
		if _, hasTitle := normSchema["title"]; !hasTitle {
			normSchema["title"] = schemaName
		}
		schemaSlug := sanitizeSlug(schemaName, "schema")
		schemaRel := path.Join("schemas", schemaSlug+".schema.json")
		schemaBytes, err := marshalPrettyJSON(normSchema)
		if err != nil {
			return nil, err
		}
		errMsg := stageFileWithPolicy(p, cs, opts.InteropOptions.ConflictPolicy, "schema", schemaSlug, schemaRel, schemaBytes, usedPaths, &idMappings)
		if errMsg != "" {
			conflicts = append(conflicts, errMsg)
		}
		schemaRefToRelPath["#/components/schemas/"+schemaName] = schemaRel
		schemaRefToRelPath["#/definitions/"+schemaName] = schemaRel
	}

	// 2. Stage reusable security schemes into `.gorequest/auth/<id>.auth.yaml`
	secSchemesMeta := map[string]OpenAPISecuritySchemeMeta{}
	schemeNameToAuthID := map[string]string{}
	rawSecSchemes := extractRootSecuritySchemesMap(root, valReport.Kind)
	for _, secName := range sortedMapKeys(rawSecSchemes) {
		sm, ok := derefMapNode(refCtx, rawSecSchemes[secName], "#/components/securitySchemes/"+secName)
		if !ok {
			continue
		}
		meta := parseSecuritySchemeMeta(sm)
		secSchemesMeta[secName] = meta

		authScheme, secretSlot, supported := buildGoRequestAuthScheme(secName, meta)
		if !supported {
			continue
		}
		if secretSlot != "" {
			addSecretSlot(secretSlot)
			varNotes = append(varNotes, VariableSourceNote{
				Name:          secretSlot,
				Scope:         "request-auth",
				Source:        "openapi-security-scheme",
				IsSecret:      true,
				MappedRef:     "{{secret." + secretSlot + "}}",
				MaskedPreview: "***",
			})
		}
		desiredID := sanitizeSlug(secName, "auth")
		finalID, finalRel, action, baseline, cfMsg := resolveEntityCollision(
			p,
			opts.InteropOptions.ConflictPolicy,
			"auth",
			desiredID,
			"auth",
			".auth.yaml",
			usedIDs,
			usedPaths,
		)
		authScheme.ID = finalID
		schemeNameToAuthID[secName] = finalID
		idMappings = append(idMappings, IDMapping{
			EntityKind: "auth",
			SourceID:   secName,
			TargetID:   finalID,
			SourcePath: "#/components/securitySchemes/" + secName,
			TargetPath: finalRel,
			Action:     action,
			Renamed:    finalID != desiredID,
		})
		if cfMsg != "" {
			conflicts = append(conflicts, cfMsg)
			continue
		}
		if action == "skip" {
			continue
		}
		authBytes, err := encodeYAML(authScheme)
		if err != nil {
			return nil, err
		}
		if action == "update" {
			cs.Update(finalRel, authBytes, baseline)
		} else {
			cs.Create(finalRel, authBytes)
		}
	}

	// 3. Extract servers & stage Environment (`baseUrl` + secret declarations)
	servers := extractOpenAPIServers(root, valReport.Kind)
	baseURL := "http://127.0.0.1:4010"
	if len(servers) > 0 && strings.TrimSpace(servers[0].URL) != "" {
		baseURL = strings.TrimRight(strings.TrimSpace(servers[0].URL), "/")
	}
	envID := strings.TrimSpace(opts.InteropOptions.TargetEnvID)
	if envID == "" {
		envID = defaultProjectEnvID(p)
		if envID == "" {
			envID = "dev"
		}
	}
	if err := stageOpenAPIEnvironment(p, cs, envID, baseURL, secretSlots, opts.InteropOptions.ConflictPolicy, usedIDs, usedPaths, &idMappings, &varNotes, &conflicts); err != nil {
		return nil, err
	}

	// 4. Stage Operations -> `.gorequest/collections/.../*.request.yaml`
	globalSecReqs := parseSecurityRequirementSlice(root["security"])
	globalConsumes := extractStringSlice(root["consumes"])
	globalProduces := extractStringSlice(root["produces"])
	opMappingTable := map[string]string{}

	pathsMap, _ := root["paths"].(map[string]any)
	for _, rawPath := range sortedMapKeys(pathsMap) {
		pathJSONPtr := "$.paths[" + rawPath + "]"
		pathItem, ok := derefMapNode(refCtx, pathsMap[rawPath], pathJSONPtr)
		if !ok {
			continue
		}
		pathParams := collectNormalizedParameters(refCtx, pathItem["parameters"], pathJSONPtr+".parameters", valReport.Kind, func(_, _, _ string) {}, func(_ string, _ LossSeverity, _, _, _ string) {}, func(_ string) {})
		opsOnPath := extractPathItemOperations(pathItem, valReport.Kind, valReport.Version, pathJSONPtr, func(_, _, _ string) {}, func(_ string) {})

		for _, opEntry := range opsOnPath {
			method := opEntry.method
			opMap := opEntry.op
			opLoc := fmt.Sprintf("%s.%s", pathJSONPtr, strings.ToLower(method))

			opID, _ := opMap["operationId"].(string)
			opID = strings.TrimSpace(opID)
			summary, _ := opMap["summary"].(string)
			description, _ := opMap["description"].(string)
			deprecated, _ := opMap["deprecated"].(bool)
			tags := extractStringSlice(opMap["tags"])

			reqName := strings.TrimSpace(summary)
			if reqName == "" {
				if opID != "" {
					reqName = opID
				} else {
					reqName = method + " " + rawPath
				}
			}

			desiredReqID := sanitizeOpenAPISlug(opID, "")
			if desiredReqID == "" {
				desiredReqID = "req-" + sanitizeOpenAPISlug(strings.ToLower(method)+"-"+rawPath, "operation")
			}

			reqDir := targetFolderDir
			if opts.GroupByTag && len(tags) > 0 {
				tagFolder := sanitizeSlug(tags[0], "")
				if tagFolder != "" {
					reqDir = path.Join(targetFolderDir, tagFolder)
					colRel := path.Join(reqDir, "collection.yaml")
					if !usedPaths[colRel] && !p.Store.Exists(colRel) {
						col := Collection{
							SchemaVersion: SupportedSchemaVersion,
							ID:            tagFolder,
							Name:          tags[0],
						}
						if colBytes, err := encodeYAML(&col); err == nil {
							cs.Create(colRel, colBytes)
							usedPaths[colRel] = true
						}
					}
				}
			}

			finalReqID, finalReqRel, action, baseline, cfMsg := resolveEntityCollision(
				p,
				opts.InteropOptions.ConflictPolicy,
				"request",
				desiredReqID,
				reqDir,
				".request.yaml",
				usedIDs,
				usedPaths,
			)
			srcID := opID
			if srcID == "" {
				srcID = method + " " + rawPath
			}
			idMappings = append(idMappings, IDMapping{
				EntityKind: "request",
				SourceID:   srcID,
				TargetID:   finalReqID,
				SourcePath: opLoc,
				TargetPath: finalReqRel,
				Action:     action,
				Renamed:    finalReqID != desiredReqID,
			})
			if cfMsg != "" {
				conflicts = append(conflicts, cfMsg)
				continue
			}
			if action == "skip" {
				continue
			}

			opParams := collectNormalizedParameters(refCtx, opMap["parameters"], opLoc+".parameters", valReport.Kind, func(_, _, _ string) {}, func(_ string, _ LossSeverity, _, _, _ string) {}, func(_ string) {})
			mergedParams := mergePathAndOpParameters(pathParams, opParams)

			// Build Query & Header KVs from parameters
			var queryKVs []KV
			var headerKVs []KV
			for pi := range mergedParams {
				scrubSensitiveParamExamples(&mergedParams[pi], red)
			}
			for _, prm := range mergedParams {
				valStr := synthesizeParamValueString(prm, red)
				enabled := prm.Required || valStr != ""
				switch prm.In {
				case "query":
					queryKVs = append(queryKVs, KV{Name: prm.Name, Value: valStr, Enabled: BoolPtr(enabled)})
				case "header":
					headerKVs = append(headerKVs, KV{Name: prm.Name, Value: valStr, Enabled: BoolPtr(enabled)})
				}
			}

			// Build RequestBody & HTTP Body
			opReqBody, httpBody, bodyMediaType := buildOperationRequestBody(refCtx, opMap, valReport.Kind, globalConsumes, schemaRefToRelPath, red)
			if bodyMediaType != "" && !hasHeaderCaseInsensitive(headerKVs, "Content-Type") {
				headerKVs = append([]KV{{Name: "Content-Type", Value: bodyMediaType, Enabled: BoolPtr(true)}}, headerKVs...)
			}

			// Build Responses map & optional schema assertion
			opResponses, primary2xxStatus, primarySchemaRel, primaryMediaType := buildOperationResponses(refCtx, opMap["responses"], valReport.Kind, globalProduces, schemaRefToRelPath, red)
			if primaryMediaType != "" && !hasHeaderCaseInsensitive(headerKVs, "Accept") {
				headerKVs = append(headerKVs, KV{Name: "Accept", Value: primaryMediaType, Enabled: BoolPtr(true)})
			}

			// Build AuthRef from operation or global security requirements
			opSecReqs := globalSecReqs
			if _, hasOpSec := opMap["security"]; hasOpSec {
				opSecReqs = parseSecurityRequirementSlice(opMap["security"])
			}
			authRef := resolveAuthRefForSecurity(opSecReqs, schemeNameToAuthID)

			// Build Assert nodes (status + schema assertion when available)
			var assertNodes []yaml.Node
			if primary2xxStatus > 0 {
				if n, err := marshalMapToYAMLNode(map[string]any{"type": "status", "equals": primary2xxStatus}); err == nil {
					assertNodes = append(assertNodes, *n)
				}
			}
			if primarySchemaRel != "" {
				if n, err := marshalMapToYAMLNode(map[string]any{"type": "schema", "schema": primarySchemaRel}); err == nil {
					assertNodes = append(assertNodes, *n)
				}
			}

			// Build OperationContract
			contract := &OperationContract{
				OperationID:  opID,
				Summary:      summary,
				Description:  description,
				Deprecated:   deprecated,
				PathTemplate: rawPath,
				Parameters:   mergedParams,
				RequestBody:  opReqBody,
				Responses:    opResponses,
				Servers:      parseOpenAPIServersSlice(opMap["servers"]),
				Security:     opSecReqs,
				ExternalDocs: parseExternalDocs(opMap["externalDocs"]),
			}
			if cbs, ok := opMap["callbacks"].(map[string]any); ok && len(cbs) > 0 {
				contract.Callbacks = cbs
			}

			var docRefs []string
			if strings.TrimSpace(description) != "" {
				docRel := path.Join("docs", finalReqID+".md")
				docContent := fmt.Sprintf("# %s\n\n%s\n", reqName, red.RedactString(strings.TrimSpace(description)))
				_ = stageFileWithPolicy(p, cs, opts.InteropOptions.ConflictPolicy, "doc", finalReqID, docRel, []byte(docContent), usedPaths, &idMappings)
				docRefs = []string{docRel}
			}

			urlStr := "{{baseUrl}}" + rawPath
			req := &Request{
				SchemaVersion: SupportedSchemaVersion,
				ID:            finalReqID,
				Name:          reqName,
				Protocol:      "http",
				Tags:          tags,
				HTTP: &HTTPDef{
					Method:  method,
					URL:     urlStr,
					Query:   queryKVs,
					Headers: headerKVs,
					Body:    httpBody,
				},
				Auth:          authRef,
				Assert:        assertNodes,
				Documentation: docRefs,
			}

			if opts.SourceMode == SourceModeExternalSpec {
				contract.Source = buildContractSourceRef(opts.SourceFileRel, Digest(data), valReport.Version, rawPath, method, opID, req, contract)
			}
			if err := SetRequestOpenAPI(req, contract); err != nil {
				return nil, err
			}

			reqBytes, err := p.EncodeRequest(req)
			if err != nil {
				return nil, err
			}
			if action == "update" {
				cs.Update(finalReqRel, reqBytes, baseline)
			} else {
				cs.Create(finalReqRel, reqBytes)
			}
			importedReqIDs = append(importedReqIDs, finalReqID)
			opMappingKey := method + " " + rawPath
			if opID != "" {
				opMappingKey = opID
			}
			opMappingTable[opMappingKey] = finalReqID
		}
	}

	// 5. Update project.yaml with ProjectOpenAPIMetadata
	if p.File != nil && p.Store.Exists("project.yaml") {
		projBytes, projDigest, rerr := p.Store.Read("project.yaml")
		if rerr == nil {
			targetDefVer := OpenAPIVersion31
			if valReport.Kind == SpecKindOpenAPI32 {
				targetDefVer = OpenAPIVersion32
			}
			infoMap, _ := root["info"].(map[string]any)
			projMeta := &ProjectOpenAPIMetadata{
				Mode:                 opts.SourceMode,
				DefaultTargetVersion: targetDefVer,
				APIVersion:           valReport.APIVersion,
				Summary:              stringField(infoMap, "summary"),
				Description:          stringField(infoMap, "description"),
				SelfURI:              stringField(root, "$self"),
				TermsOfService:       stringField(infoMap, "termsOfService"),
				Contact:              parseContact(infoMap["contact"]),
				License:              parseLicense(infoMap["license"]),
				Servers:              servers,
				Tags:                 parseOpenAPITags(root["tags"]),
				SecuritySchemes:      secSchemesMeta,
			}
			if wh, ok := root["webhooks"].(map[string]any); ok && len(wh) > 0 {
				projMeta.Webhooks = wh
			}
			if opts.SourceMode == SourceModeExternalSpec {
				srcPath := opts.SourceFileRel
				if srcPath == "" {
					srcPath = filename
				}
				projMeta.ExternalSpec = &ExternalSpecState{
					SourcePath:        srcPath,
					ContentDigest:     Digest(data),
					SpecVersion:       valReport.Version,
					OperationMappings: opMappingTable,
				}
			}
			pfCopy := *p.File
			if pfCopy.Extensions != nil {
				extClone := make(map[string]any, len(pfCopy.Extensions))
				for k, v := range pfCopy.Extensions {
					extClone[k] = v
				}
				pfCopy.Extensions = extClone
			}
			if err := SetProjectOpenAPI(&pfCopy, projMeta); err == nil {
				if mergedProj, err := MergeProjectFileYAML(projBytes, &pfCopy); err == nil {
					if patchedProj, err := patchTopLevelExtensionsAST(mergedProj, pfCopy.Extensions); err == nil {
						cs.Update("project.yaml", patchedProj, projDigest)
					}
				}
			}
		}
	}

	previews, perr := cs.Preview()
	if perr != nil {
		conflicts = append(conflicts, perr.Error())
	}

	canApply := len(conflicts) == 0 && !strictBlocked && len(previews) > 0
	summaryMsg := fmt.Sprintf("Staged %s (%s) %q: %d operation(s), %d schema(s), %d file change(s)",
		valReport.Kind, valReport.Version, valReport.Title, len(importedReqIDs), len(rawSchemasMap), len(previews))
	if !canApply {
		summaryMsg = fmt.Sprintf("Blocked %s (%s) import: %d conflict/loss block(s)", valReport.Kind, valReport.Version, len(conflicts))
	}

	preview := ImportPreview{
		Format:         interpFormat,
		FormatVersion:  valReport.Version,
		Summary:        summaryMsg,
		ConflictPolicy: opts.InteropOptions.ConflictPolicy,
		Changes:        previews,
		IDMappings:     idMappings,
		Variables:      varNotes,
		SecretBindings: secretSlots,
		Losses:         losses,
		Conflicts:      conflicts,
		CanApply:       canApply,
	}

	return &ImportPlan{
		project:      p,
		changeSet:    cs,
		preview:      preview,
		redactor:     red,
		importedReqs: importedReqIDs,
	}, nil
}

// --- Schema & Operation conversion helpers -------------------------------

func extractRootSchemasMap(root map[string]any, kind OpenAPISpecKind) map[string]any {
	if kind == SpecKindSwagger20 {
		if defs, ok := root["definitions"].(map[string]any); ok {
			return defs
		}
		return nil
	}
	if comp, ok := root["components"].(map[string]any); ok {
		if schemas, ok := comp["schemas"].(map[string]any); ok {
			return schemas
		}
	}
	return nil
}

func extractRootSecuritySchemesMap(root map[string]any, kind OpenAPISpecKind) map[string]any {
	if kind == SpecKindSwagger20 {
		if defs, ok := root["securityDefinitions"].(map[string]any); ok {
			return defs
		}
		return nil
	}
	if comp, ok := root["components"].(map[string]any); ok {
		if ss, ok := comp["securitySchemes"].(map[string]any); ok {
			return ss
		}
	}
	return nil
}

func rewriteSwaggerDefinitionRef(ref string) string {
	if strings.HasPrefix(ref, "#/definitions/") {
		return "#/components/schemas/" + strings.TrimPrefix(ref, "#/definitions/")
	}
	return ref
}

func normalizeSchemaTo31(ctx *schemaEvalContext, raw map[string]any) map[string]any {
	if raw == nil {
		return nil
	}
	out := make(map[string]any, len(raw))
	for k, v := range raw {
		switch k {
		case "$ref":
			if refStr, ok := v.(string); ok {
				if !strings.HasPrefix(refStr, "#") && !isRemoteURI(refStr) {
					// Resolve external relative file $ref inline so project schema is self-contained
					if target, _, _, err := ctx.resolveRefTarget(refStr, "$ref"); err == nil {
						if targetMap, ok := target.(map[string]any); ok {
							return normalizeSchemaTo31(ctx, targetMap)
						}
					}
				}
				out["$ref"] = rewriteSwaggerDefinitionRef(refStr)
				continue
			}
		case "nullable", "x-nullable":
			continue
		case "exclusiveMinimum":
			if b, ok := v.(bool); ok {
				if b {
					if minVal, hasMin := raw["minimum"]; hasMin {
						out["exclusiveMinimum"] = minVal
					}
				}
				continue
			}
		case "exclusiveMaximum":
			if b, ok := v.(bool); ok {
				if b {
					if maxVal, hasMax := raw["maximum"]; hasMax {
						out["exclusiveMaximum"] = maxVal
					}
				}
				continue
			}
		case "minimum":
			if b, ok := raw["exclusiveMinimum"].(bool); ok && b {
				continue
			}
		case "maximum":
			if b, ok := raw["exclusiveMaximum"].(bool); ok && b {
				continue
			}
		}

		switch child := v.(type) {
		case map[string]any:
			if k == "properties" || k == "patternProperties" || k == "$defs" || k == "definitions" {
				propOut := make(map[string]any, len(child))
				for pk, pv := range child {
					if pm, ok := pv.(map[string]any); ok {
						propOut[pk] = normalizeSchemaTo31(ctx, pm)
					} else {
						propOut[pk] = pv
					}
				}
				out[k] = propOut
			} else {
				out[k] = normalizeSchemaTo31(ctx, child)
			}
		case []any:
			arrOut := make([]any, len(child))
			for i, elem := range child {
				if em, ok := elem.(map[string]any); ok {
					arrOut[i] = normalizeSchemaTo31(ctx, em)
				} else {
					arrOut[i] = elem
				}
			}
			out[k] = arrOut
		default:
			out[k] = v
		}
	}

	// Convert OpenAPI 3.0 `nullable: true` or Swagger 2.0 `x-nullable: true` into 3.1 type array
	isNullable := false
	if nb, ok := raw["nullable"].(bool); ok && nb {
		isNullable = true
	}
	if xnb, ok := raw["x-nullable"].(bool); ok && xnb {
		isNullable = true
	}
	if isNullable {
		types := extractAllowedTypes(out["type"])
		if len(types) > 0 && !slices.Contains(types, "null") {
			types = append(types, "null")
			out["type"] = types
		}
	}
	return out
}

func normalizeSchemaForProjectFile(ctx *schemaEvalContext, raw map[string]any, allDefs map[string]any) map[string]any {
	norm := normalizeSchemaTo31(ctx, raw)
	// Attach referenced #/components/schemas/<Name> under $defs so standalone `.schema.json` files validate self-contained
	referenced := map[string]map[string]any{}
	collectLocalComponentRefs(ctx, norm, allDefs, referenced)
	if len(referenced) > 0 {
		defs := map[string]any{}
		if existing, ok := norm["$defs"].(map[string]any); ok {
			for k, v := range existing {
				defs[k] = v
			}
		}
		for name, defSchema := range referenced {
			defs[name] = rewriteComponentRefsToDefs(defSchema)
		}
		norm = rewriteComponentRefsToDefs(norm)
		norm["$defs"] = defs
	}
	return norm
}

func collectLocalComponentRefs(ctx *schemaEvalContext, node any, allDefs map[string]any, out map[string]map[string]any) {
	switch n := node.(type) {
	case map[string]any:
		if refStr, ok := n["$ref"].(string); ok {
			name := ""
			if strings.HasPrefix(refStr, "#/components/schemas/") {
				name = strings.TrimPrefix(refStr, "#/components/schemas/")
			} else if strings.HasPrefix(refStr, "#/definitions/") {
				name = strings.TrimPrefix(refStr, "#/definitions/")
			}
			if name != "" && out[name] == nil && allDefs != nil {
				if targetRaw, ok := allDefs[name].(map[string]any); ok {
					normTarget := normalizeSchemaTo31(ctx, targetRaw)
					out[name] = normTarget
					collectLocalComponentRefs(ctx, normTarget, allDefs, out)
				}
			}
		}
		for _, v := range n {
			collectLocalComponentRefs(ctx, v, allDefs, out)
		}
	case []any:
		for _, elem := range n {
			collectLocalComponentRefs(ctx, elem, allDefs, out)
		}
	}
}

func rewriteComponentRefsToDefs(node map[string]any) map[string]any {
	if node == nil {
		return nil
	}
	out := make(map[string]any, len(node))
	for k, v := range node {
		if k == "$ref" {
			if s, ok := v.(string); ok {
				if strings.HasPrefix(s, "#/components/schemas/") {
					out[k] = "#/$defs/" + strings.TrimPrefix(s, "#/components/schemas/")
					continue
				}
				if strings.HasPrefix(s, "#/definitions/") {
					out[k] = "#/$defs/" + strings.TrimPrefix(s, "#/definitions/")
					continue
				}
			}
		}
		switch child := v.(type) {
		case map[string]any:
			out[k] = rewriteComponentRefsToDefs(child)
		case []any:
			arr := make([]any, len(child))
			for i, elem := range child {
				if em, ok := elem.(map[string]any); ok {
					arr[i] = rewriteComponentRefsToDefs(em)
				} else {
					arr[i] = elem
				}
			}
			out[k] = arr
		default:
			out[k] = v
		}
	}
	return out
}

func extractSwagger2ParamSchema(pm map[string]any) map[string]any {
	s := map[string]any{}
	for _, k := range []string{"type", "format", "enum", "default", "minimum", "maximum", "minLength", "maxLength", "pattern", "items", "minItems", "maxItems", "uniqueItems"} {
		if v, ok := pm[k]; ok {
			s[k] = v
		}
	}
	if xnb, ok := pm["x-nullable"].(bool); ok && xnb {
		if tStr, ok := s["type"].(string); ok && tStr != "" {
			s["type"] = []string{tStr, "null"}
		}
	}
	if len(s) == 0 {
		return nil
	}
	return s
}

func parseExampleObjectsMap(raw any) map[string]OpenAPIExampleObject {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	out := make(map[string]OpenAPIExampleObject, len(m))
	for _, k := range sortedMapKeys(m) {
		em, ok := m[k].(map[string]any)
		if !ok {
			continue
		}
		out[k] = OpenAPIExampleObject{
			Summary:         stringField(em, "summary"),
			Description:     stringField(em, "description"),
			Value:           em["value"],
			DataValue:       em["dataValue"],
			SerializedValue: stringField(em, "serializedValue"),
			ExternalValue:   stringField(em, "externalValue"),
		}
	}
	return out
}

func parseSecuritySchemeMeta(sm map[string]any) OpenAPISecuritySchemeMeta {
	flows, _ := sm["flows"].(map[string]any)
	dep, _ := sm["deprecated"].(bool)
	return OpenAPISecuritySchemeMeta{
		Type:              stringField(sm, "type"),
		Description:       stringField(sm, "description"),
		Name:              stringField(sm, "name"),
		In:                stringField(sm, "in"),
		Scheme:            stringField(sm, "scheme"),
		BearerFormat:      stringField(sm, "bearerFormat"),
		OpenIDConnectURL:  stringField(sm, "openIdConnectUrl"),
		OAuth2MetadataURL: stringField(sm, "oauth2MetadataUrl"),
		Deprecated:        dep,
		Flows:             flows,
	}
}

func buildGoRequestAuthScheme(secName string, meta OpenAPISecuritySchemeMeta) (*AuthScheme, string, bool) {
	t := strings.ToLower(strings.TrimSpace(meta.Type))
	slotBase := sanitizeSecretSlotName(secName, "api_auth")
	switch t {
	case "http":
		scheme := strings.ToLower(strings.TrimSpace(meta.Scheme))
		switch scheme {
		case "bearer":
			slot := slotBase + "_token"
			return &AuthScheme{
				SchemaVersion: SupportedSchemaVersion,
				ID:            sanitizeSlug(secName, "bearer"),
				Type:          "bearer",
				Token:         "{{secret." + slot + "}}",
			}, slot, true
		case "basic":
			slot := slotBase + "_password"
			return &AuthScheme{
				SchemaVersion: SupportedSchemaVersion,
				ID:            sanitizeSlug(secName, "basic"),
				Type:          "basic",
				Username:      "{{username}}",
				Password:      "{{secret." + slot + "}}",
			}, slot, true
		}
	case "basic": // Swagger 2.0 `type: basic`
		slot := slotBase + "_password"
		return &AuthScheme{
			SchemaVersion: SupportedSchemaVersion,
			ID:            sanitizeSlug(secName, "basic"),
			Type:          "basic",
			Username:      "{{username}}",
			Password:      "{{secret." + slot + "}}",
		}, slot, true
	case "apikey":
		slot := slotBase + "_key"
		inLoc := NormalizeAPIKeyLocation(meta.In)
		keyName := strings.TrimSpace(meta.Name)
		if keyName == "" {
			keyName = "X-API-Key"
		}
		return &AuthScheme{
			SchemaVersion: SupportedSchemaVersion,
			ID:            sanitizeSlug(secName, "apikey"),
			Type:          "apikey",
			Name:          keyName,
			In:            inLoc,
			Value:         "{{secret." + slot + "}}",
		}, slot, true
	}
	return nil, "", false
}

func extractOpenAPIServers(root map[string]any, kind OpenAPISpecKind) []OpenAPIServer {
	if kind == SpecKindSwagger20 {
		host := strings.TrimSpace(stringField(root, "host"))
		basePath := strings.TrimSpace(stringField(root, "basePath"))
		schemes := extractStringSlice(root["schemes"])
		scheme := "https"
		if len(schemes) > 0 && strings.TrimSpace(schemes[0]) != "" {
			scheme = strings.ToLower(strings.TrimSpace(schemes[0]))
		}
		if host == "" {
			host = "127.0.0.1:4010"
			if len(schemes) == 0 {
				scheme = "http"
			}
		}
		if basePath == "/" {
			basePath = ""
		} else if basePath != "" && !strings.HasPrefix(basePath, "/") {
			basePath = "/" + basePath
		}
		return []OpenAPIServer{{
			URL:         fmt.Sprintf("%s://%s%s", scheme, host, basePath),
			Description: "Migrated from Swagger 2.0 host/basePath/schemes",
		}}
	}
	return parseOpenAPIServersSlice(root["servers"])
}

func parseOpenAPIServersSlice(raw any) []OpenAPIServer {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	var out []OpenAPIServer
	for _, item := range list {
		sm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		srv := OpenAPIServer{
			URL:         stringField(sm, "url"),
			Description: stringField(sm, "description"),
			Name:        stringField(sm, "name"),
		}
		if varsMap, ok := sm["variables"].(map[string]any); ok && len(varsMap) > 0 {
			srv.Variables = make(map[string]OpenAPIServerVariable, len(varsMap))
			for _, vk := range sortedMapKeys(varsMap) {
				if vm, ok := varsMap[vk].(map[string]any); ok {
					srv.Variables[vk] = OpenAPIServerVariable{
						Enum:        extractStringSlice(vm["enum"]),
						Default:     stringField(vm, "default"),
						Description: stringField(vm, "description"),
					}
				}
			}
		}
		out = append(out, srv)
	}
	return out
}

func parseOpenAPITags(raw any) []OpenAPITag {
	list, ok := raw.([]any)
	if !ok || len(list) == 0 {
		return nil
	}
	var out []OpenAPITag
	for _, item := range list {
		tm, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringField(tm, "name")
		if name == "" {
			continue
		}
		out = append(out, OpenAPITag{
			Name:         name,
			Summary:      stringField(tm, "summary"),
			Description:  stringField(tm, "description"),
			Parent:       stringField(tm, "parent"),
			Kind:         stringField(tm, "kind"),
			ExternalDocs: parseExternalDocs(tm["externalDocs"]),
		})
	}
	return out
}

func parseExternalDocs(raw any) *OpenAPIExternalDocs {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	u := stringField(m, "url")
	if u == "" {
		return nil
	}
	return &OpenAPIExternalDocs{
		Description: stringField(m, "description"),
		URL:         u,
	}
}

func parseContact(raw any) *OpenAPIContact {
	m, ok := raw.(map[string]any)
	if !ok || len(m) == 0 {
		return nil
	}
	return &OpenAPIContact{
		Name:  stringField(m, "name"),
		URL:   stringField(m, "url"),
		Email: stringField(m, "email"),
	}
}

func parseLicense(raw any) *OpenAPILicense {
	m, ok := raw.(map[string]any)
	if !ok {
		return nil
	}
	name := stringField(m, "name")
	if name == "" {
		return nil
	}
	return &OpenAPILicense{
		Name:       name,
		Identifier: stringField(m, "identifier"),
		URL:        stringField(m, "url"),
	}
}

func stageOpenAPIEnvironment(
	p *Project,
	cs *ChangeSet,
	envID string,
	baseURL string,
	secretSlots []string,
	policy ConflictPolicy,
	usedIDs map[string]bool,
	usedPaths map[string]bool,
	idMappings *[]IDMapping,
	varNotes *[]VariableSourceNote,
	conflicts *[]string,
) error {
	env := Environment{
		SchemaVersion: SupportedSchemaVersion,
		ID:            envID,
		Variables:     map[string]string{"baseUrl": baseURL},
		Secrets:       slices.Clone(secretSlots),
	}
	if existing, ok := p.Environments[envID]; ok && existing != nil {
		for k, v := range existing.Variables {
			if k != "baseUrl" {
				env.Variables[k] = v
			}
		}
		for _, s := range existing.Secrets {
			if !slices.Contains(env.Secrets, s) {
				env.Secrets = append(env.Secrets, s)
			}
		}
	}
	*varNotes = append(*varNotes, VariableSourceNote{
		Name:          "baseUrl",
		Scope:         "environment",
		Source:        "openapi-servers",
		TargetEnvID:   envID,
		IsSecret:      false,
		MappedRef:     "{{baseUrl}}",
		MaskedPreview: baseURL,
	})

	envRel := environmentRelPath(envID)
	if existingEntry, ok := p.EnvEntries[envID]; ok && existingEntry != nil && existingEntry.RelPath != "" {
		envRel = existingEntry.RelPath
		envBytes, err := encodeYAML(&env)
		if err != nil {
			return err
		}
		if diskBytes, diskDigest, rerr := p.Store.Read(envRel); rerr == nil {
			if merged, merr := MergeEnvironmentYAML(diskBytes, &env); merr == nil {
				envBytes = merged
			}
			cs.Update(envRel, envBytes, diskDigest)
			usedIDs[envID] = true
			usedPaths[envRel] = true
			*idMappings = append(*idMappings, IDMapping{
				EntityKind: "environment",
				SourceID:   "servers",
				TargetID:   envID,
				TargetPath: envRel,
				Action:     "update",
			})
			return nil
		}
	}

	envBytes, err := encodeYAML(&env)
	if err != nil {
		return err
	}
	errMsg := stageFileWithPolicy(p, cs, policy, "environment", envID, envRel, envBytes, usedPaths, idMappings)
	if errMsg != "" {
		*conflicts = append(*conflicts, errMsg)
	}
	return nil
}

func stageFileWithPolicy(
	p *Project,
	cs *ChangeSet,
	policy ConflictPolicy,
	entityKind string,
	entityID string,
	relPath string,
	content []byte,
	usedPaths map[string]bool,
	idMappings *[]IDMapping,
) string {
	exists := false
	digest := ""
	if p != nil && p.Store != nil && p.Store.Exists(relPath) {
		if _, d, err := p.Store.Read(relPath); err == nil {
			exists = true
			digest = d
		}
	}
	if !exists && !usedPaths[relPath] {
		usedPaths[relPath] = true
		cs.Create(relPath, content)
		*idMappings = append(*idMappings, IDMapping{
			EntityKind: entityKind,
			SourceID:   entityID,
			TargetID:   entityID,
			TargetPath: relPath,
			Action:     "create",
		})
		return ""
	}
	switch policy {
	case ConflictOverwrite:
		if exists && !usedPaths[relPath] {
			usedPaths[relPath] = true
			cs.Update(relPath, content, digest)
			*idMappings = append(*idMappings, IDMapping{
				EntityKind: entityKind,
				SourceID:   entityID,
				TargetID:   entityID,
				TargetPath: relPath,
				Action:     "update",
			})
			return ""
		}
	case ConflictSkip:
		*idMappings = append(*idMappings, IDMapping{
			EntityKind: entityKind,
			SourceID:   entityID,
			TargetID:   entityID,
			TargetPath: relPath,
			Action:     "skip",
		})
		return ""
	case ConflictRenameNewID:
		ext := path.Ext(relPath)
		stem := strings.TrimSuffix(relPath, ext)
		for i := 2; i < 1000; i++ {
			candRel := fmt.Sprintf("%s-copy-%d%s", stem, i, ext)
			if i == 2 {
				candRel = stem + "-copy" + ext
			}
			if !usedPaths[candRel] && (p == nil || p.Store == nil || !p.Store.Exists(candRel)) {
				usedPaths[candRel] = true
				cs.Create(candRel, content)
				*idMappings = append(*idMappings, IDMapping{
					EntityKind: entityKind,
					SourceID:   entityID,
					TargetID:   entityID + "-copy",
					TargetPath: candRel,
					Action:     "rename",
					Renamed:    true,
				})
				return ""
			}
		}
	}
	return fmt.Sprintf("%s %q already exists at %s", entityKind, entityID, relPath)
}

func buildOperationRequestBody(
	ctx *schemaEvalContext,
	opMap map[string]any,
	kind OpenAPISpecKind,
	globalConsumes []string,
	schemaRefToRelPath map[string]string,
	red *Redactor,
) (*OperationRequestBody, *Body, string) {
	if kind == SpecKindSwagger20 {
		return buildSwagger2OperationRequestBody(ctx, opMap, globalConsumes, schemaRefToRelPath, red)
	}
	rbRaw, hasRB := opMap["requestBody"]
	if !hasRB {
		return nil, nil, ""
	}
	rbMap, ok := derefMapNode(ctx, rbRaw, "requestBody")
	if !ok {
		return nil, nil, ""
	}
	desc := stringField(rbMap, "description")
	reqBool, _ := rbMap["required"].(bool)
	contentMap, _ := rbMap["content"].(map[string]any)
	if len(contentMap) == 0 {
		return nil, nil, ""
	}

	contractRB := &OperationRequestBody{
		Description: desc,
		Required:    reqBool,
		Content:     make(map[string]MediaTypeContract, len(contentMap)),
	}

	var primaryMT string
	var primaryBody *Body

	for _, mtName := range sortedMapKeys(contentMap) {
		mtObj, ok := contentMap[mtName].(map[string]any)
		if !ok {
			continue
		}
		mtContract := parseMediaTypeContract(ctx, mtObj, schemaRefToRelPath, red)
		contractRB.Content[mtName] = mtContract
		if primaryMT == "" {
			primaryMT = mtName
			primaryBody = buildHTTPBodyFromMediaType(mtName, mtContract)
		}
	}
	return contractRB, primaryBody, primaryMT
}

func buildSwagger2OperationRequestBody(
	ctx *schemaEvalContext,
	opMap map[string]any,
	globalConsumes []string,
	schemaRefToRelPath map[string]string,
	red *Redactor,
) (*OperationRequestBody, *Body, string) {
	params, ok := opMap["parameters"].([]any)
	if !ok {
		return nil, nil, ""
	}
	consumes := extractStringSlice(opMap["consumes"])
	if len(consumes) == 0 {
		consumes = globalConsumes
	}
	defaultMT := "application/json"
	if len(consumes) > 0 && strings.TrimSpace(consumes[0]) != "" {
		defaultMT = strings.TrimSpace(consumes[0])
	}

	var formProps = map[string]any{}
	var formRequired []string
	hasForm := false
	isMultipart := strings.Contains(strings.ToLower(defaultMT), "multipart")

	for _, rawP := range params {
		pm, ok := derefMapNode(ctx, rawP, "parameters")
		if !ok {
			continue
		}
		inLoc := strings.ToLower(stringField(pm, "in"))
		switch inLoc {
		case "body":
			reqBool, _ := pm["required"].(bool)
			desc := stringField(pm, "description")
			schemaRaw, _ := pm["schema"].(map[string]any)
			mtContract := parseMediaTypeContract(ctx, map[string]any{"schema": schemaRaw, "example": pm["x-example"]}, schemaRefToRelPath, red)
			rb := &OperationRequestBody{
				Description: desc,
				Required:    reqBool,
				Content:     map[string]MediaTypeContract{defaultMT: mtContract},
			}
			return rb, buildHTTPBodyFromMediaType(defaultMT, mtContract), defaultMT
		case "formdata":
			hasForm = true
			pName := stringField(pm, "name")
			pType := stringField(pm, "type")
			if pType == "file" {
				isMultipart = true
				formProps[pName] = map[string]any{"type": "string", "format": "binary"}
			} else {
				formProps[pName] = extractSwagger2ParamSchema(pm)
			}
			if r, _ := pm["required"].(bool); r {
				formRequired = append(formRequired, pName)
			}
		}
	}
	if hasForm {
		mt := "application/x-www-form-urlencoded"
		if isMultipart {
			mt = "multipart/form-data"
		}
		schema := map[string]any{
			"type":       "object",
			"properties": formProps,
		}
		if len(formRequired) > 0 {
			schema["required"] = formRequired
		}
		mtContract := MediaTypeContract{Schema: schema}
		rb := &OperationRequestBody{
			Required: len(formRequired) > 0,
			Content:  map[string]MediaTypeContract{mt: mtContract},
		}
		return rb, buildHTTPBodyFromMediaType(mt, mtContract), mt
	}
	return nil, nil, ""
}

func buildOperationResponses(
	ctx *schemaEvalContext,
	rawResponses any,
	kind OpenAPISpecKind,
	globalProduces []string,
	schemaRefToRelPath map[string]string,
	red *Redactor,
) (map[string]ResponseContract, int, string, string) {
	respMap, ok := rawResponses.(map[string]any)
	if !ok || len(respMap) == 0 {
		return nil, 0, "", ""
	}
	out := make(map[string]ResponseContract, len(respMap))
	var primary2xx int
	var primarySchemaRel string
	var primaryMediaType string

	defaultProduce := "application/json"
	if len(globalProduces) > 0 && strings.TrimSpace(globalProduces[0]) != "" {
		defaultProduce = strings.TrimSpace(globalProduces[0])
	}

	for _, statusKey := range sortedMapKeys(respMap) {
		rObj, ok := derefMapNode(ctx, respMap[statusKey], "responses."+statusKey)
		if !ok {
			continue
		}
		rc := ResponseContract{
			Description: stringField(rObj, "description"),
		}

		// Headers
		if hdrsMap, ok := rObj["headers"].(map[string]any); ok && len(hdrsMap) > 0 {
			rc.Headers = make(map[string]HeaderContract, len(hdrsMap))
			for _, hName := range sortedMapKeys(hdrsMap) {
				if hm, ok := derefMapNode(ctx, hdrsMap[hName], "headers."+hName); ok {
					var hSchema map[string]any
					if sm, ok := hm["schema"].(map[string]any); ok {
						hSchema = normalizeSchemaTo31(ctx, sm)
					} else if kind == SpecKindSwagger20 {
						hSchema = extractSwagger2ParamSchema(hm)
					}
					reqB, _ := hm["required"].(bool)
					depB, _ := hm["deprecated"].(bool)
					rc.Headers[hName] = HeaderContract{
						Description: stringField(hm, "description"),
						Required:    reqB,
						Deprecated:  depB,
						Schema:      hSchema,
						Example:     hm["example"],
					}
				}
			}
		}

		// Content media types
		if contentMap, ok := rObj["content"].(map[string]any); ok && len(contentMap) > 0 {
			rc.Content = make(map[string]MediaTypeContract, len(contentMap))
			for _, mtName := range sortedMapKeys(contentMap) {
				if mtObj, ok := contentMap[mtName].(map[string]any); ok {
					mtContract := parseMediaTypeContract(ctx, mtObj, schemaRefToRelPath, red)
					rc.Content[mtName] = mtContract
				}
			}
		} else if kind == SpecKindSwagger20 {
			// Migrate Swagger 2.0 `responses[status].schema` and `examples` into `content`
			rc.Content = map[string]MediaTypeContract{}
			if sRaw, hasS := rObj["schema"].(map[string]any); hasS {
				mtContract := parseMediaTypeContract(ctx, map[string]any{"schema": sRaw}, schemaRefToRelPath, red)
				if exMap, ok := rObj["examples"].(map[string]any); ok {
					if exVal, ok := exMap[defaultProduce]; ok {
						mtContract.Example = scrubAnyExampleValue(exVal, red)
					}
				}
				rc.Content[defaultProduce] = mtContract
			}
		}

		out[statusKey] = rc

		if code, err := strconv.Atoi(statusKey); err == nil && code >= 200 && code < 300 && primary2xx == 0 {
			primary2xx = code
			for _, mtName := range sortedMapKeys(rc.Content) {
				mtc := rc.Content[mtName]
				if primaryMediaType == "" {
					primaryMediaType = mtName
				}
				if mtc.SchemaRef != "" && strings.HasPrefix(mtc.SchemaRef, "schemas/") {
					primarySchemaRel = mtc.SchemaRef
					break
				}
			}
		}
	}
	return out, primary2xx, primarySchemaRel, primaryMediaType
}

func parseMediaTypeContract(
	ctx *schemaEvalContext,
	mtObj map[string]any,
	schemaRefToRelPath map[string]string,
	red *Redactor,
) MediaTypeContract {
	var schemaMap map[string]any
	var schemaRef string
	if sRaw, ok := mtObj["schema"].(map[string]any); ok {
		if r, ok := sRaw["$ref"].(string); ok {
			rewritten := rewriteSwaggerDefinitionRef(strings.TrimSpace(r))
			if rel, mapped := schemaRefToRelPath[rewritten]; mapped {
				schemaRef = rel
			} else {
				schemaRef = rewritten
			}
		}
		schemaMap = normalizeSchemaTo31(ctx, sRaw)
	}
	var itemSchemaMap map[string]any
	if isRaw, ok := mtObj["itemSchema"].(map[string]any); ok {
		itemSchemaMap = normalizeSchemaTo31(ctx, isRaw)
	}
	var itemEncMap map[string]any
	if ieRaw, ok := mtObj["itemEncoding"].(map[string]any); ok {
		itemEncMap = ieRaw
	}
	var prefixEncSlice []map[string]any
	if peRaw, ok := mtObj["prefixEncoding"].([]any); ok {
		for _, elem := range peRaw {
			if em, ok := elem.(map[string]any); ok {
				prefixEncSlice = append(prefixEncSlice, em)
			}
		}
	}
	encMap, _ := mtObj["encoding"].(map[string]any)

	return MediaTypeContract{
		Schema:         schemaMap,
		SchemaRef:      schemaRef,
		Example:        scrubAnyExampleValue(mtObj["example"], red),
		Examples:       parseExampleObjectsMap(mtObj["examples"]),
		Encoding:       encMap,
		ItemSchema:     itemSchemaMap,
		ItemEncoding:   itemEncMap,
		PrefixEncoding: prefixEncSlice,
	}
}

func buildHTTPBodyFromMediaType(mediaType string, mt MediaTypeContract) *Body {
	normMT := normalizeMediaType(mediaType)
	sampleVal := mt.Example
	if sampleVal == nil && len(mt.Examples) > 0 {
		for _, k := range sortedMapKeys(mt.Examples) {
			ex := mt.Examples[k]
			if ex.Value != nil {
				sampleVal = ex.Value
				break
			}
			if ex.DataValue != nil {
				sampleVal = ex.DataValue
				break
			}
		}
	}
	if sampleVal == nil && len(mt.Schema) > 0 {
		sampleVal = synthesizeExampleFromSchema(mt.Schema, 0)
	}

	switch {
	case strings.Contains(normMT, "json"):
		if sampleVal == nil {
			sampleVal = map[string]any{}
		}
		node, err := marshalMapToYAMLNode(sampleVal)
		if err != nil {
			return nil
		}
		return &Body{Type: "json", Value: node}
	case strings.Contains(normMT, "x-www-form-urlencoded"):
		var kvs []map[string]any
		if m, ok := sampleVal.(map[string]any); ok {
			for _, k := range sortedMapKeys(m) {
				kvs = append(kvs, map[string]any{
					"name":    k,
					"value":   fmt.Sprintf("%v", m[k]),
					"enabled": true,
				})
			}
		}
		node, _ := marshalMapToYAMLNode(kvs)
		return &Body{Type: "urlencoded", Value: node}
	case strings.Contains(normMT, "multipart"):
		var parts []map[string]any
		if m, ok := sampleVal.(map[string]any); ok {
			for _, k := range sortedMapKeys(m) {
				parts = append(parts, map[string]any{
					"name":    k,
					"kind":    "text",
					"value":   fmt.Sprintf("%v", m[k]),
					"enabled": true,
				})
			}
		}
		node, _ := marshalMapToYAMLNode(parts)
		return &Body{Type: "form-data", Value: node}
	case strings.Contains(normMT, "xml"):
		strVal := "<root></root>"
		if s, ok := sampleVal.(string); ok && s != "" {
			strVal = s
		}
		return &Body{Type: "xml", Value: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strVal}}
	default:
		strVal := ""
		if sampleVal != nil {
			strVal = fmt.Sprintf("%v", sampleVal)
		}
		return &Body{Type: "text", Value: &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: strVal}}
	}
}

func synthesizeExampleFromSchema(schema map[string]any, depth int) any {
	if schema == nil || depth > 5 {
		return nil
	}
	if ex, ok := schema["example"]; ok && ex != nil {
		return ex
	}
	if exs, ok := schema["examples"].([]any); ok && len(exs) > 0 {
		return exs[0]
	}
	if def, ok := schema["default"]; ok && def != nil {
		return def
	}
	if enums, ok := schema["enum"].([]any); ok && len(enums) > 0 {
		return enums[0]
	}
	types := extractAllowedTypes(schema["type"])
	primaryType := ""
	for _, t := range types {
		if t != "null" {
			primaryType = t
			break
		}
	}
	if primaryType == "" && schema["properties"] != nil {
		primaryType = "object"
	}
	switch primaryType {
	case "object":
		out := map[string]any{}
		if props, ok := schema["properties"].(map[string]any); ok {
			for _, k := range sortedMapKeys(props) {
				if pm, ok := props[k].(map[string]any); ok {
					out[k] = synthesizeExampleFromSchema(pm, depth+1)
				}
			}
		}
		return out
	case "array":
		if items, ok := schema["items"].(map[string]any); ok {
			elem := synthesizeExampleFromSchema(items, depth+1)
			if elem != nil {
				return []any{elem}
			}
		}
		return []any{}
	case "integer", "number":
		return 1
	case "boolean":
		return true
	case "string":
		return "string"
	default:
		return nil
	}
}

// scrubSensitiveParamExamples masks literal example values of credential-like
// parameters (Authorization, Cookie, *-token, *-api-key, ...) so real secrets
// copied into a spec never reach request YAML, contracts, or previews.
func scrubSensitiveParamExamples(prm *OperationParam, red *Redactor) {
	if prm == nil || red == nil || !sensitiveParamName(prm.Name) {
		return
	}
	mask := func(v any) any {
		s, ok := v.(string)
		if !ok || strings.TrimSpace(s) == "" || strings.Contains(s, "{{") {
			return v
		}
		red.RegisterSecret(s)
		if i := strings.IndexByte(s, ' '); i > 0 {
			red.RegisterSecret(strings.TrimSpace(s[i+1:]))
		}
		return "***"
	}
	prm.Example = mask(prm.Example)
	for k, ex := range prm.Examples {
		ex.Value = mask(ex.Value)
		ex.DataValue = mask(ex.DataValue)
		if ex.SerializedValue != "" {
			if m, ok := mask(ex.SerializedValue).(string); ok {
				ex.SerializedValue = m
			}
		}
		prm.Examples[k] = ex
	}
}

func sensitiveParamName(name string) bool {
	lower := strings.ToLower(strings.TrimSpace(name))
	switch lower {
	case "authorization", "proxy-authorization", "cookie", "set-cookie":
		return true
	}
	for _, tok := range []string{"token", "secret", "password", "passwd", "api-key", "api_key", "apikey", "session"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func synthesizeParamValueString(prm OperationParam, red *Redactor) string {
	if prm.Example != nil {
		s := fmt.Sprintf("%v", scrubAnyExampleValue(prm.Example, red))
		return s
	}
	if len(prm.Examples) > 0 {
		for _, k := range sortedMapKeys(prm.Examples) {
			ex := prm.Examples[k]
			if ex.Value != nil {
				return fmt.Sprintf("%v", scrubAnyExampleValue(ex.Value, red))
			}
			if ex.DataValue != nil {
				return fmt.Sprintf("%v", scrubAnyExampleValue(ex.DataValue, red))
			}
			if ex.SerializedValue != "" {
				return ex.SerializedValue
			}
		}
	}
	if prm.Schema != nil {
		if def, ok := prm.Schema["default"]; ok && def != nil {
			return fmt.Sprintf("%v", def)
		}
	}
	return ""
}

func scrubAnyExampleValue(v any, red *Redactor) any {
	if v == nil || red == nil {
		return v
	}
	switch val := v.(type) {
	case string:
		return red.RedactString(val)
	case map[string]any:
		out := make(map[string]any, len(val))
		for k, child := range val {
			if looksSensitiveFieldName(k) {
				if s, ok := child.(string); ok && s != "" && !strings.HasPrefix(s, "{{") {
					red.RegisterSecret(s)
					out[k] = "***"
					continue
				}
			}
			out[k] = scrubAnyExampleValue(child, red)
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = scrubAnyExampleValue(elem, red)
		}
		return out
	default:
		return v
	}
}

func looksSensitiveFieldName(key string) bool {
	lower := strings.ToLower(strings.TrimSpace(key))
	for _, tok := range []string{"password", "secret", "access_token", "refresh_token", "client_secret", "api_key", "apikey", "private_key"} {
		if strings.Contains(lower, tok) {
			return true
		}
	}
	return false
}

func parseSecurityRequirementSlice(raw any) []map[string][]string {
	list, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string][]string, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}
		entry := make(map[string][]string, len(m))
		for _, k := range sortedMapKeys(m) {
			entry[k] = extractStringSlice(m[k])
		}
		out = append(out, entry)
	}
	return out
}

func resolveAuthRefForSecurity(secReqs []map[string][]string, schemeNameToAuthID map[string]string) *AuthRef {
	if secReqs != nil && len(secReqs) == 0 {
		// Explicit `security: []` clears inherited auth
		return &AuthRef{Mode: "none"}
	}
	for _, reqMap := range secReqs {
		for _, schemeName := range sortedMapKeys(reqMap) {
			if authID, ok := schemeNameToAuthID[schemeName]; ok && authID != "" {
				return &AuthRef{Mode: "ref", Ref: authID}
			}
		}
	}
	return nil
}

func hasHeaderCaseInsensitive(headers []KV, name string) bool {
	for _, h := range headers {
		if strings.EqualFold(strings.TrimSpace(h.Name), strings.TrimSpace(name)) {
			return true
		}
	}
	return false
}

func marshalMapToYAMLNode(v any) (*yaml.Node, error) {
	b, err := yaml.Marshal(v)
	if err != nil {
		return nil, err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
		return cloneYAMLNode(doc.Content[0], true), nil
	}
	return &doc, nil
}

func marshalPrettyJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func stringField(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if s, ok := m[key].(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func buildContractSourceRef(
	specPath, specDigest, specVersion, rawPath, method, opID string,
	req *Request,
	contract *OperationContract,
) *ContractSourceRef {
	var paramKeys []string
	if contract != nil {
		for _, p := range contract.Parameters {
			paramKeys = append(paramKeys, p.In+":"+p.Name)
		}
		sortStrings(paramKeys)
	}
	var statuses []string
	if contract != nil {
		statuses = sortedMapKeys(contract.Responses)
	}
	qMap := map[string]string{}
	hMap := map[string]string{}
	bodyType := ""
	bodyHash := ""
	if req != nil && req.HTTP != nil {
		for _, kv := range req.HTTP.Query {
			qMap[kv.Name] = kv.Value
		}
		for _, kv := range req.HTTP.Headers {
			hMap[kv.Name] = kv.Value
		}
		if req.HTTP.Body != nil {
			bodyType = req.HTTP.Body.Type
			bodyHash = Digest([]byte(BodyEditText(req.HTTP.Body)))
		}
	}
	opSnap, _ := json.Marshal(map[string]any{
		"path":     rawPath,
		"method":   method,
		"opId":     opID,
		"summary":  contract.Summary,
		"params":   paramKeys,
		"statuses": statuses,
		"bodyType": bodyType,
	})
	return &ContractSourceRef{
		SpecPath:         specPath,
		SpecDigest:       specDigest,
		SpecVersion:      specVersion,
		Path:             rawPath,
		Method:           method,
		OperationID:      opID,
		BaselineOpDigest: Digest(opSnap),
		BaselineSummary:  contract.Summary,
		BaselineParams:   paramKeys,
		BaselineStatuses: statuses,
		BaselineQuery:    qMap,
		BaselineHeaders:  hMap,
		BaselineBodyType: bodyType,
		BaselineBodyHash: bodyHash,
	}
}
