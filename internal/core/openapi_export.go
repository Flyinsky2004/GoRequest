package core

import (
	"bytes"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
)

// OpenAPIExportOptions configures exporting a GoRequest Project to an OpenAPI
// 3.1.2 or 3.2.1 specification in YAML or JSON format.
type OpenAPIExportOptions struct {
	TargetVersion   string // "3.1.2" (default) or "3.2.1"
	Format          string // "yaml" (default) or "json"
	Strict          bool   // When true and !AllowLossy, reject export if 3.2 features would be lost in 3.1
	AllowLossy      bool   // Explicitly allow downgrading/dropping 3.2-only constructs when targeting 3.1
	IncludeExamples bool   // Include request/response/parameter examples (scrubbed via Redactor)
	EnvID           string // Optional environment for non-secret server URL metadata
	Redactor        *Redactor
}

func (o OpenAPIExportOptions) normalized() OpenAPIExportOptions {
	v := strings.TrimSpace(o.TargetVersion)
	switch {
	case v == "" || v == "3.1" || strings.HasPrefix(v, "3.1."):
		o.TargetVersion = OpenAPIVersion31
	case v == "3.2" || strings.HasPrefix(v, "3.2."):
		o.TargetVersion = OpenAPIVersion32
	}
	f := strings.ToLower(strings.TrimSpace(o.Format))
	if f == "" {
		f = "yaml"
	}
	o.Format = f
	return o
}

// CompatibilityIssue describes a single version compatibility or downgrade item
// when exporting or converting between OpenAPI 3.2 and 3.1.
type CompatibilityIssue struct {
	Feature    string `json:"feature" yaml:"feature"`
	Location   string `json:"location" yaml:"location"`
	Resolution string `json:"resolution" yaml:"resolution"` // "rejected" | "downgraded" | "omitted"
	Message    string `json:"message" yaml:"message"`
}

// CompatibilityReport summarizes version compatibility between the project's
// contracts (or a 3.2 specification) and the requested target OpenAPI version.
type CompatibilityReport struct {
	SourceVersion string               `json:"sourceVersion" yaml:"sourceVersion"`
	TargetVersion string               `json:"targetVersion" yaml:"targetVersion"`
	Compatible    bool                 `json:"compatible" yaml:"compatible"`
	Lossy         bool                 `json:"lossy" yaml:"lossy"`
	Issues        []CompatibilityIssue `json:"issues,omitempty" yaml:"issues,omitempty"`
}

// OperationCoverageDetail reports documentation and contract completeness for
// a single exportable HTTP operation.
type OperationCoverageDetail struct {
	RequestID          string   `json:"requestId" yaml:"requestId"`
	Name               string   `json:"name" yaml:"name"`
	Method             string   `json:"method" yaml:"method"`
	Path               string   `json:"path" yaml:"path"`
	HasDescription     bool     `json:"hasDescription" yaml:"hasDescription"`
	HasResponseSchema  bool     `json:"hasResponseSchema" yaml:"hasResponseSchema"`
	HasExamples        bool     `json:"hasExamples" yaml:"hasExamples"`
	HasTestAssertions  bool     `json:"hasTestAssertions" yaml:"hasTestAssertions"`
	DeclaredStatuses   []string `json:"declaredStatuses,omitempty" yaml:"declaredStatuses,omitempty"`
	DeclaredMediaTypes []string `json:"declaredMediaTypes,omitempty" yaml:"declaredMediaTypes,omitempty"`
}

// NotApplicableCoverageItem records a non-HTTP request (GraphQL, gRPC, WebSocket)
// excluded from OpenAPI REST coverage and export (openapi-auth.md §2).
type NotApplicableCoverageItem struct {
	RequestID string `json:"requestId" yaml:"requestId"`
	Name      string `json:"name" yaml:"name"`
	Protocol  string `json:"protocol" yaml:"protocol"`
	Reason    string `json:"reason" yaml:"reason"`
}

// OpenAPICoverageReport summarizes contract documentation completeness across
// a GoRequest project using exportable HTTP operations as the denominator.
// Documentation coverage and runtime test assertion presence are tracked
// separately and never conflated (openapi-auth.md §2, acceptance.md §7.1).
type OpenAPICoverageReport struct {
	TotalRequests             int                         `json:"totalRequests" yaml:"totalRequests"`
	ExportableHTTPOperations  int                         `json:"exportableHttpOperations" yaml:"exportableHttpOperations"`
	NotApplicableCount        int                         `json:"notApplicableCount" yaml:"notApplicableCount"`
	WithDescriptionCount      int                         `json:"withDescriptionCount" yaml:"withDescriptionCount"`
	WithResponseSchemaCount   int                         `json:"withResponseSchemaCount" yaml:"withResponseSchemaCount"`
	WithExamplesCount         int                         `json:"withExamplesCount" yaml:"withExamplesCount"`
	WithTestAssertionsCount   int                         `json:"withTestAssertionsCount" yaml:"withTestAssertionsCount"`
	DescriptionCoveragePct    float64                     `json:"descriptionCoveragePct" yaml:"descriptionCoveragePct"`
	ResponseSchemaCoveragePct float64                     `json:"responseSchemaCoveragePct" yaml:"responseSchemaCoveragePct"`
	ExamplesCoveragePct       float64                     `json:"examplesCoveragePct" yaml:"examplesCoveragePct"`
	TestAssertionsCoveragePct float64                     `json:"testAssertionsCoveragePct" yaml:"testAssertionsCoveragePct"`
	Operations                []OperationCoverageDetail   `json:"operations,omitempty" yaml:"operations,omitempty"`
	NotApplicableRequests     []NotApplicableCoverageItem `json:"notApplicableRequests,omitempty" yaml:"notApplicableRequests,omitempty"`
}

// OpenAPIExportResult holds the serialized OpenAPI specification together with
// its compatibility and coverage reports.
type OpenAPIExportResult struct {
	TargetVersion string                `json:"targetVersion" yaml:"targetVersion"`
	Format        string                `json:"format" yaml:"format"`
	Bytes         []byte                `json:"-" yaml:"-"`
	Compatibility CompatibilityReport   `json:"compatibility" yaml:"compatibility"`
	Coverage      OpenAPICoverageReport `json:"coverage" yaml:"coverage"`
	Warnings      []Issue               `json:"warnings,omitempty" yaml:"warnings,omitempty"`
}

var serverVarTemplateRe = regexp.MustCompile(`^\{\{([A-Za-z0-9_.-]+)\}\}(/.*)?$`)

func isHTTPProtocol(proto string) bool {
	s := strings.ToLower(strings.TrimSpace(proto))
	return s == "" || s == "http"
}

// ComputeOpenAPICoverage calculates the OpenAPI documentation & contract
// coverage report for p without mutating project state.
func ComputeOpenAPICoverage(p *Project) (*OpenAPICoverageReport, error) {
	if p == nil {
		return nil, configErrorf("project is nil")
	}
	report := &OpenAPICoverageReport{}
	reqIDs := sortedMapKeys(p.Requests)
	report.TotalRequests = len(reqIDs)

	for _, id := range reqIDs {
		entry := p.Requests[id]
		if entry == nil || entry.Request == nil {
			continue
		}
		req := entry.Request
		if !isHTTPProtocol(req.Protocol) || req.HTTP == nil {
			proto := strings.TrimSpace(req.Protocol)
			if proto == "" {
				proto = "non-http"
			}
			report.NotApplicableRequests = append(report.NotApplicableRequests, NotApplicableCoverageItem{
				RequestID: req.ID,
				Name:      req.Name,
				Protocol:  proto,
				Reason:    fmt.Sprintf("protocol %q is not an OpenAPI HTTP operation", proto),
			})
			continue
		}

		contract, _ := GetRequestOpenAPI(req)
		method := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
		if method == "" {
			method = "GET"
		}
		opPath := deriveOperationPath(req, contract)

		hasDesc := false
		if contract != nil && (strings.TrimSpace(contract.Summary) != "" || strings.TrimSpace(contract.Description) != "") {
			hasDesc = true
		}
		if !hasDesc && len(req.Documentation) > 0 && p.Store != nil {
			for _, docRel := range req.Documentation {
				if p.Store.Exists(docRel) {
					if b, _, err := p.Store.ReadBounded(docRel, 256*1024); err == nil && len(bytes.TrimSpace(b)) > 0 {
						hasDesc = true
						break
					}
				}
			}
		}

		hasRespSchema := false
		var statuses []string
		mediaSet := map[string]bool{}
		if contract != nil && len(contract.Responses) > 0 {
			statuses = sortedMapKeys(contract.Responses)
			for _, st := range statuses {
				rc := contract.Responses[st]
				for _, mt := range sortedMapKeys(rc.Content) {
					mediaSet[mt] = true
					mc := rc.Content[mt]
					if len(mc.Schema) > 0 || strings.TrimSpace(mc.SchemaRef) != "" || len(mc.ItemSchema) > 0 {
						hasRespSchema = true
					}
				}
			}
		}
		if !hasRespSchema && findSchemaAssertionPath(req.Assert) != "" {
			hasRespSchema = true
		}

		hasExamples := hasOperationExamples(req, contract)
		hasAssertions := len(req.Assert) > 0

		report.ExportableHTTPOperations++
		if hasDesc {
			report.WithDescriptionCount++
		}
		if hasRespSchema {
			report.WithResponseSchemaCount++
		}
		if hasExamples {
			report.WithExamplesCount++
		}
		if hasAssertions {
			report.WithTestAssertionsCount++
		}

		report.Operations = append(report.Operations, OperationCoverageDetail{
			RequestID:          req.ID,
			Name:               req.Name,
			Method:             method,
			Path:               opPath,
			HasDescription:     hasDesc,
			HasResponseSchema:  hasRespSchema,
			HasExamples:        hasExamples,
			HasTestAssertions:  hasAssertions,
			DeclaredStatuses:   statuses,
			DeclaredMediaTypes: sortedMapKeys(mediaSet),
		})
	}

	report.NotApplicableCount = len(report.NotApplicableRequests)
	if report.ExportableHTTPOperations > 0 {
		denom := float64(report.ExportableHTTPOperations)
		report.DescriptionCoveragePct = float64(report.WithDescriptionCount) * 100.0 / denom
		report.ResponseSchemaCoveragePct = float64(report.WithResponseSchemaCount) * 100.0 / denom
		report.ExamplesCoveragePct = float64(report.WithExamplesCount) * 100.0 / denom
		report.TestAssertionsCoveragePct = float64(report.WithTestAssertionsCount) * 100.0 / denom
	}
	return report, nil
}

// ExportOpenAPI exports p to an OpenAPI 3.1.2 or 3.2.1 YAML/JSON specification.
func ExportOpenAPI(p *Project, opts OpenAPIExportOptions) (*OpenAPIExportResult, error) {
	if p == nil || p.File == nil {
		return nil, configErrorf("project is nil")
	}
	opts = opts.normalized()
	if opts.TargetVersion != OpenAPIVersion31 && opts.TargetVersion != OpenAPIVersion32 {
		return nil, configErrorf("unsupported OpenAPI export target version %q (supported: %s, %s)", opts.TargetVersion, OpenAPIVersion31, OpenAPIVersion32)
	}
	if opts.Format != "yaml" && opts.Format != "json" {
		return nil, configErrorf("unsupported OpenAPI export format %q (supported: yaml, json)", opts.Format)
	}

	red := opts.Redactor
	if red == nil {
		red = NewRedactor()
	}

	cov, err := ComputeOpenAPICoverage(p)
	if err != nil {
		return nil, err
	}

	meta, err := GetProjectOpenAPI(p.File)
	if err != nil {
		return nil, err
	}
	if meta == nil {
		meta = &ProjectOpenAPIMetadata{}
	}

	isTarget31 := strings.HasPrefix(opts.TargetVersion, "3.1.")
	strictReject := opts.Strict && !opts.AllowLossy

	compat := CompatibilityReport{
		SourceVersion: meta.DefaultTargetVersion,
		TargetVersion: opts.TargetVersion,
		Compatible:    true,
	}
	if compat.SourceVersion == "" {
		compat.SourceVersion = OpenAPIVersion31
	}

	recordCompatIssue := func(feature, location, downgradedResolution, msg string) {
		compat.Lossy = true
		res := downgradedResolution
		if strictReject {
			res = "rejected"
			compat.Compatible = false
		}
		compat.Issues = append(compat.Issues, CompatibilityIssue{
			Feature:    feature,
			Location:   location,
			Resolution: res,
			Message:    msg,
		})
	}

	doc := map[string]any{
		"openapi": opts.TargetVersion,
	}

	// 1. $self (OpenAPI 3.2+)
	if strings.TrimSpace(meta.SelfURI) != "" {
		if isTarget31 {
			recordCompatIssue("$self", "$self", "omitted", "OpenAPI 3.2 top-level '$self' URI is not supported in OpenAPI 3.1")
		} else {
			doc["$self"] = red.RedactString(strings.TrimSpace(meta.SelfURI))
		}
	}

	// 2. info object
	apiVer := strings.TrimSpace(meta.APIVersion)
	if apiVer == "" {
		apiVer = "1.0.0"
	}
	title := strings.TrimSpace(p.File.Name)
	if title == "" {
		title = "GoRequest API"
	}
	infoObj := map[string]any{
		"title":   red.RedactString(title),
		"version": red.RedactString(apiVer),
	}
	if strings.TrimSpace(meta.Summary) != "" {
		infoObj["summary"] = red.RedactString(meta.Summary)
	}
	if strings.TrimSpace(meta.Description) != "" {
		infoObj["description"] = red.RedactString(meta.Description)
	}
	if strings.TrimSpace(meta.TermsOfService) != "" {
		infoObj["termsOfService"] = red.RedactString(meta.TermsOfService)
	}
	if meta.Contact != nil && (meta.Contact.Name != "" || meta.Contact.URL != "" || meta.Contact.Email != "") {
		cObj := map[string]any{}
		if meta.Contact.Name != "" {
			cObj["name"] = red.RedactString(meta.Contact.Name)
		}
		if meta.Contact.URL != "" {
			cObj["url"] = red.RedactString(meta.Contact.URL)
		}
		if meta.Contact.Email != "" {
			cObj["email"] = red.RedactString(meta.Contact.Email)
		}
		infoObj["contact"] = cObj
	}
	if meta.License != nil && meta.License.Name != "" {
		lObj := map[string]any{
			"name": red.RedactString(meta.License.Name),
		}
		if meta.License.Identifier != "" {
			lObj["identifier"] = red.RedactString(meta.License.Identifier)
		} else if meta.License.URL != "" {
			lObj["url"] = red.RedactString(meta.License.URL)
		}
		infoObj["license"] = lObj
	}
	doc["info"] = infoObj

	// 3. servers
	servers := buildExportServers(p, meta, opts.EnvID, isTarget31, red, recordCompatIssue)
	if len(servers) > 0 {
		doc["servers"] = servers
	}

	// 4. tags
	tags := buildExportTags(p, meta, isTarget31, red, recordCompatIssue)
	if len(tags) > 0 {
		doc["tags"] = tags
	}

	// 5. webhooks
	if len(meta.Webhooks) > 0 {
		doc["webhooks"] = scrubAnyExampleValue(meta.Webhooks, red)
	}

	// 6. paths & operations
	pathsMap := map[string]any{}
	schemaFileToComponentName := loadProjectComponentSchemasMap(p)
	authIDToSchemeName := buildAuthIDToSchemeNameMap(p, meta)

	var warnings []Issue
	for _, nitem := range cov.NotApplicableRequests {
		warnings = append(warnings, Issue{
			Level:   "warning",
			Code:    "openapi-protocol-not-applicable",
			Path:    nitem.RequestID,
			Message: fmt.Sprintf("Skipped request %q (%s): non-HTTP protocol is not applicable to OpenAPI paths", nitem.Name, nitem.Protocol),
		})
	}

	reqIDs := sortedMapKeys(p.Requests)
	for _, id := range reqIDs {
		entry := p.Requests[id]
		if entry == nil || entry.Request == nil {
			continue
		}
		req := entry.Request
		if !isHTTPProtocol(req.Protocol) || req.HTTP == nil {
			continue
		}

		contract, cerr := GetRequestOpenAPI(req)
		if cerr != nil {
			return nil, cerr
		}

		methodUpper := strings.ToUpper(strings.TrimSpace(req.HTTP.Method))
		if methodUpper == "" {
			methodUpper = "GET"
		}
		methodLower := strings.ToLower(methodUpper)
		opPath := deriveOperationPath(req, contract)
		locPrefix := fmt.Sprintf("paths.%s.%s", opPath, methodLower)

		if isTarget31 {
			if methodLower == "query" {
				recordCompatIssue("PathItem.query", locPrefix, "omitted", fmt.Sprintf("HTTP QUERY operation %q on %s requires OpenAPI 3.2", req.ID, opPath))
				continue
			}
			if !slices.Contains(openAPI31StandardMethods, methodLower) {
				recordCompatIssue("PathItem.additionalOperations", fmt.Sprintf("paths.%s.additionalOperations.%s", opPath, methodUpper), "omitted", fmt.Sprintf("custom HTTP method %q (%s) requires OpenAPI 3.2 additionalOperations", methodUpper, req.ID))
				continue
			}
		}

		opMap := buildExportOperationObject(
			p, req, contract, opPath, locPrefix,
			isTarget31, opts.IncludeExamples,
			schemaFileToComponentName, authIDToSchemeName,
			red, recordCompatIssue,
		)

		pathItem, ok := pathsMap[opPath].(map[string]any)
		if !ok {
			pathItem = map[string]any{}
			pathsMap[opPath] = pathItem
		}

		if slices.Contains(openAPI31StandardMethods, methodLower) || (!isTarget31 && methodLower == "query") {
			pathItem[methodLower] = opMap
		} else {
			addOps, ok := pathItem["additionalOperations"].(map[string]any)
			if !ok {
				addOps = map[string]any{}
				pathItem["additionalOperations"] = addOps
			}
			addOps[methodUpper] = opMap
		}
	}

	doc["paths"] = pathsMap

	// 7. components (schemas + securitySchemes)
	components := map[string]any{}
	compSchemas := buildExportComponentSchemas(p, schemaFileToComponentName, red)
	if len(compSchemas) > 0 {
		components["schemas"] = compSchemas
	}
	compSec := buildExportSecuritySchemes(p, meta, isTarget31, red, recordCompatIssue)
	if len(compSec) > 0 {
		components["securitySchemes"] = compSec
	}
	if len(components) > 0 {
		doc["components"] = components
	}

	// Check strict compatibility gate
	if strictReject && compat.Lossy {
		return &OpenAPIExportResult{
			TargetVersion: opts.TargetVersion,
			Format:        opts.Format,
			Compatibility: compat,
			Coverage:      *cov,
			Warnings:      warnings,
		}, fmt.Errorf("%w: %d OpenAPI 3.2 construct(s) are incompatible with target %s", ErrOpenAPILossyRejected, len(compat.Issues), opts.TargetVersion)
	}

	var outBytes []byte
	if opts.Format == "json" {
		outBytes, err = marshalPrettyJSON(doc)
	} else {
		outBytes, err = encodeYAML(doc)
	}
	if err != nil {
		return nil, fmt.Errorf("serialize OpenAPI %s (%s): %w", opts.TargetVersion, opts.Format, err)
	}

	return &OpenAPIExportResult{
		TargetVersion: opts.TargetVersion,
		Format:        opts.Format,
		Bytes:         outBytes,
		Compatibility: compat,
		Coverage:      *cov,
		Warnings:      warnings,
	}, nil
}

// ExportOpenAPIToFile exports p to an OpenAPI 3.1.2 or 3.2.1 file on disk using
// atomic write semantics and overwrite protection.
func ExportOpenAPIToFile(p *Project, targetPath string, overwrite bool, opts OpenAPIExportOptions) (*OpenAPIExportResult, error) {
	if strings.TrimSpace(targetPath) == "" {
		return nil, configErrorf("export target path is empty")
	}
	res, err := ExportOpenAPI(p, opts)
	if err != nil {
		return res, err
	}
	if err := writeAtomicExternalFile(targetPath, res.Bytes, overwrite); err != nil {
		return res, err
	}
	return res, nil
}

// DowngradeOpenAPI32To31 inspects a serialized OpenAPI 3.2 specification and
// downgrades it to OpenAPI 3.1.2 (or rejects in strict mode if 3.2-specific
// constructs are present), returning a detailed CompatibilityReport.
func DowngradeOpenAPI32To31(specBytes []byte, format string, strict bool) ([]byte, *CompatibilityReport, error) {
	rawVal, err := decodeStructuredValue(specBytes)
	if err != nil {
		return nil, nil, fmt.Errorf("parse OpenAPI specification: %w", err)
	}
	doc, ok := rawVal.(map[string]any)
	if !ok {
		return nil, nil, fmt.Errorf("OpenAPI root document must be an object")
	}

	srcVer := stringField(doc, "openapi")
	if srcVer == "" {
		srcVer = OpenAPIVersion32
	}
	report := &CompatibilityReport{
		SourceVersion: srcVer,
		TargetVersion: OpenAPIVersion31,
		Compatible:    true,
	}

	record := func(feature, loc, downgradedRes, msg string) {
		report.Lossy = true
		res := downgradedRes
		if strict {
			res = "rejected"
			report.Compatible = false
		}
		report.Issues = append(report.Issues, CompatibilityIssue{
			Feature:    feature,
			Location:   loc,
			Resolution: res,
			Message:    msg,
		})
	}

	doc["openapi"] = OpenAPIVersion31
	if selfURI := stringField(doc, "$self"); selfURI != "" {
		record("$self", "$self", "omitted", "OpenAPI 3.2 '$self' is not supported in OpenAPI 3.1")
		delete(doc, "$self")
	}

	if srvs, ok := doc["servers"].([]any); ok {
		for i, item := range srvs {
			if sm, ok := item.(map[string]any); ok && stringField(sm, "name") != "" {
				record("Server.name", fmt.Sprintf("servers[%d].name", i), "omitted", "Server.name is an OpenAPI 3.2 field")
				delete(sm, "name")
			}
		}
	}

	if tags, ok := doc["tags"].([]any); ok {
		for i, item := range tags {
			tm, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, f := range []string{"summary", "parent", "kind"} {
				if stringField(tm, f) != "" {
					record("Tag."+f, fmt.Sprintf("tags[%d].%s", i, f), "omitted", fmt.Sprintf("Tag.%s is an OpenAPI 3.2 field", f))
					delete(tm, f)
				}
			}
		}
	}

	if paths, ok := doc["paths"].(map[string]any); ok {
		for _, pKey := range sortedMapKeys(paths) {
			pi, ok := paths[pKey].(map[string]any)
			if !ok {
				continue
			}
			if _, hasQuery := pi["query"]; hasQuery {
				record("PathItem.query", fmt.Sprintf("paths.%s.query", pKey), "omitted", "PathItem.query requires OpenAPI 3.2")
				delete(pi, "query")
			}
			if addOps, ok := pi["additionalOperations"].(map[string]any); ok && len(addOps) > 0 {
				for _, m := range sortedMapKeys(addOps) {
					record("PathItem.additionalOperations", fmt.Sprintf("paths.%s.additionalOperations.%s", pKey, m), "omitted", fmt.Sprintf("custom method %q in additionalOperations requires OpenAPI 3.2", m))
				}
				delete(pi, "additionalOperations")
			}
			downgrade32OperationsInPathItem(pi, "paths."+pKey, record)
		}
	}

	if comps, ok := doc["components"].(map[string]any); ok {
		if secMap, ok := comps["securitySchemes"].(map[string]any); ok {
			for _, sName := range sortedMapKeys(secMap) {
				sm, ok := secMap[sName].(map[string]any)
				if !ok {
					continue
				}
				loc := "components.securitySchemes." + sName
				if b, ok := sm["deprecated"].(bool); ok && b {
					record("SecurityScheme.deprecated", loc+".deprecated", "omitted", "SecurityScheme.deprecated requires OpenAPI 3.2")
					delete(sm, "deprecated")
				}
				if stringField(sm, "oauth2MetadataUrl") != "" {
					record("SecurityScheme.oauth2MetadataUrl", loc+".oauth2MetadataUrl", "omitted", "SecurityScheme.oauth2MetadataUrl requires OpenAPI 3.2")
					delete(sm, "oauth2MetadataUrl")
				}
				if flows, ok := sm["flows"].(map[string]any); ok {
					if _, hasDev := flows["deviceAuthorization"]; hasDev {
						record("OAuthFlows.deviceAuthorization", loc+".flows.deviceAuthorization", "omitted", "OAuth2 deviceAuthorization flow requires OpenAPI 3.2")
						delete(flows, "deviceAuthorization")
					}
				}
			}
		}
	}

	if strict && report.Lossy {
		return nil, report, fmt.Errorf("%w: %d OpenAPI 3.2 construct(s) cannot be represented in OpenAPI 3.1.2", ErrOpenAPILossyRejected, len(report.Issues))
	}

	f := strings.ToLower(strings.TrimSpace(format))
	var out []byte
	if f == "json" {
		out, err = marshalPrettyJSON(doc)
	} else {
		out, err = encodeYAML(doc)
	}
	if err != nil {
		return nil, report, err
	}
	return out, report, nil
}

func downgrade32OperationsInPathItem(pi map[string]any, pathLoc string, record func(feature, loc, res, msg string)) {
	for _, m := range openAPI31StandardMethods {
		op, ok := pi[m].(map[string]any)
		if !ok {
			continue
		}
		opLoc := pathLoc + "." + m
		if rb, ok := op["requestBody"].(map[string]any); ok {
			if content, ok := rb["content"].(map[string]any); ok {
				downgrade32ContentMap(content, opLoc+".requestBody.content", record)
			}
		}
		if resps, ok := op["responses"].(map[string]any); ok {
			for _, st := range sortedMapKeys(resps) {
				if rm, ok := resps[st].(map[string]any); ok {
					if content, ok := rm["content"].(map[string]any); ok {
						downgrade32ContentMap(content, fmt.Sprintf("%s.responses.%s.content", opLoc, st), record)
					}
				}
			}
		}
	}
}

func downgrade32ContentMap(content map[string]any, locPrefix string, record func(feature, loc, res, msg string)) {
	for _, mt := range sortedMapKeys(content) {
		mm, ok := content[mt].(map[string]any)
		if !ok {
			continue
		}
		mtLoc := locPrefix + "." + mt
		for _, k := range []string{"itemSchema", "itemEncoding", "prefixEncoding"} {
			if _, exists := mm[k]; exists {
				record("MediaType."+k, mtLoc+"."+k, "omitted", fmt.Sprintf("MediaType.%s requires OpenAPI 3.2", k))
				delete(mm, k)
			}
		}
		if exs, ok := mm["examples"].(map[string]any); ok {
			for _, exName := range sortedMapKeys(exs) {
				exObj, ok := exs[exName].(map[string]any)
				if !ok {
					continue
				}
				exLoc := mtLoc + ".examples." + exName
				if dv, hasDV := exObj["dataValue"]; hasDV {
					record("Example.dataValue", exLoc+".dataValue", "downgraded", "Downgraded OpenAPI 3.2 Example.dataValue to Example.value")
					if _, hasVal := exObj["value"]; !hasVal {
						exObj["value"] = dv
					}
					delete(exObj, "dataValue")
				}
				if sv, hasSV := exObj["serializedValue"]; hasSV {
					record("Example.serializedValue", exLoc+".serializedValue", "downgraded", "Downgraded OpenAPI 3.2 Example.serializedValue to Example.value")
					if _, hasVal := exObj["value"]; !hasVal {
						exObj["value"] = sv
					}
					delete(exObj, "serializedValue")
				}
			}
		}
	}
}

func buildExportServers(
	p *Project,
	meta *ProjectOpenAPIMetadata,
	envID string,
	isTarget31 bool,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) []any {
	var out []any
	if len(meta.Servers) > 0 {
		for i, srv := range meta.Servers {
			sMap := serializeOpenAPIServer(srv, fmt.Sprintf("servers[%d]", i), isTarget31, red, recordCompat)
			out = append(out, sMap)
		}
		return out
	}

	targetEnv := strings.TrimSpace(envID)
	if targetEnv == "" {
		targetEnv = defaultProjectEnvID(p)
	}
	if targetEnv != "" && p != nil {
		if env, ok := p.Environments[targetEnv]; ok && env != nil {
			for _, k := range []string{"baseUrl", "base_url"} {
				if val := strings.TrimSpace(env.Variables[k]); val != "" && !slices.Contains(env.Secrets, k) {
					out = append(out, map[string]any{
						"url":         red.RedactString(val),
						"description": fmt.Sprintf("Environment %s", env.ID),
					})
					return out
				}
			}
		}
	}
	return out
}

func serializeOpenAPIServer(
	srv OpenAPIServer,
	loc string,
	isTarget31 bool,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	sMap := map[string]any{
		"url": red.RedactString(srv.URL),
	}
	if srv.Description != "" {
		sMap["description"] = red.RedactString(srv.Description)
	}
	if srv.Name != "" {
		if isTarget31 {
			recordCompat("Server.name", loc+".name", "omitted", fmt.Sprintf("Server.name %q requires OpenAPI 3.2", srv.Name))
		} else {
			sMap["name"] = red.RedactString(srv.Name)
		}
	}
	if len(srv.Variables) > 0 {
		varsMap := map[string]any{}
		for _, vName := range sortedMapKeys(srv.Variables) {
			sv := srv.Variables[vName]
			vm := map[string]any{
				"default": red.RedactString(sv.Default),
			}
			if len(sv.Enum) > 0 {
				enums := make([]string, len(sv.Enum))
				for idx, e := range sv.Enum {
					enums[idx] = red.RedactString(e)
				}
				vm["enum"] = enums
			}
			if sv.Description != "" {
				vm["description"] = red.RedactString(sv.Description)
			}
			varsMap[vName] = vm
		}
		sMap["variables"] = varsMap
	}
	return sMap
}

func buildExportTags(
	p *Project,
	meta *ProjectOpenAPIMetadata,
	isTarget31 bool,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) []any {
	seen := map[string]bool{}
	var out []any

	for i, t := range meta.Tags {
		if strings.TrimSpace(t.Name) == "" {
			continue
		}
		seen[t.Name] = true
		tm := map[string]any{
			"name": red.RedactString(t.Name),
		}
		if t.Description != "" {
			tm["description"] = red.RedactString(t.Description)
		}
		if t.Summary != "" {
			if isTarget31 {
				recordCompat("Tag.summary", fmt.Sprintf("tags[%d].summary", i), "omitted", fmt.Sprintf("Tag.summary on tag %q requires OpenAPI 3.2", t.Name))
			} else {
				tm["summary"] = red.RedactString(t.Summary)
			}
		}
		if t.Parent != "" {
			if isTarget31 {
				recordCompat("Tag.parent", fmt.Sprintf("tags[%d].parent", i), "omitted", fmt.Sprintf("Tag.parent (%q) on tag %q requires OpenAPI 3.2", t.Parent, t.Name))
			} else {
				tm["parent"] = red.RedactString(t.Parent)
			}
		}
		if t.Kind != "" {
			if isTarget31 {
				recordCompat("Tag.kind", fmt.Sprintf("tags[%d].kind", i), "omitted", fmt.Sprintf("Tag.kind (%q) on tag %q requires OpenAPI 3.2", t.Kind, t.Name))
			} else {
				tm["kind"] = red.RedactString(t.Kind)
			}
		}
		if t.ExternalDocs != nil && t.ExternalDocs.URL != "" {
			ed := map[string]any{"url": red.RedactString(t.ExternalDocs.URL)}
			if t.ExternalDocs.Description != "" {
				ed["description"] = red.RedactString(t.ExternalDocs.Description)
			}
			tm["externalDocs"] = ed
		}
		out = append(out, tm)
	}

	// Collect any additional request tags not already declared at top-level
	extraTags := map[string]bool{}
	for _, id := range sortedMapKeys(p.Requests) {
		entry := p.Requests[id]
		if entry == nil || entry.Request == nil || !isHTTPProtocol(entry.Request.Protocol) {
			continue
		}
		for _, tag := range entry.Request.Tags {
			tag = strings.TrimSpace(tag)
			if tag != "" && !seen[tag] {
				extraTags[tag] = true
			}
		}
	}
	for _, tag := range sortedMapKeys(extraTags) {
		out = append(out, map[string]any{
			"name": red.RedactString(tag),
		})
	}
	return out
}

func buildExportOperationObject(
	p *Project,
	req *Request,
	contract *OperationContract,
	opPath string,
	locPrefix string,
	isTarget31 bool,
	includeExamples bool,
	schemaFileToComponentName map[string]string,
	authIDToSchemeName map[string]string,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	opMap := map[string]any{}

	opID := req.ID
	if contract != nil && strings.TrimSpace(contract.OperationID) != "" {
		opID = strings.TrimSpace(contract.OperationID)
	}
	opMap["operationId"] = red.RedactString(opID)

	summary := strings.TrimSpace(req.Name)
	if contract != nil && strings.TrimSpace(contract.Summary) != "" {
		summary = strings.TrimSpace(contract.Summary)
	}
	if summary != "" {
		opMap["summary"] = red.RedactString(summary)
	}

	desc := ""
	if contract != nil && strings.TrimSpace(contract.Description) != "" {
		desc = contract.Description
	} else if len(req.Documentation) > 0 && p != nil && p.Store != nil {
		for _, docRel := range req.Documentation {
			if p.Store.Exists(docRel) {
				if b, _, err := p.Store.ReadBounded(docRel, 256*1024); err == nil {
					desc = extractDocBodyAfterHeading(string(b), summary)
					if desc != "" {
						break
					}
				}
			}
		}
	}
	if strings.TrimSpace(desc) != "" {
		opMap["description"] = red.RedactString(desc)
	}

	if len(req.Tags) > 0 {
		tags := make([]string, 0, len(req.Tags))
		for _, t := range req.Tags {
			if strings.TrimSpace(t) != "" {
				tags = append(tags, red.RedactString(strings.TrimSpace(t)))
			}
		}
		if len(tags) > 0 {
			opMap["tags"] = tags
		}
	}

	if contract != nil && contract.Deprecated {
		opMap["deprecated"] = true
	}

	if contract != nil && contract.ExternalDocs != nil && contract.ExternalDocs.URL != "" {
		ed := map[string]any{"url": red.RedactString(contract.ExternalDocs.URL)}
		if contract.ExternalDocs.Description != "" {
			ed["description"] = red.RedactString(contract.ExternalDocs.Description)
		}
		opMap["externalDocs"] = ed
	}

	if contract != nil && len(contract.Servers) > 0 {
		var opSrvs []any
		for i, srv := range contract.Servers {
			opSrvs = append(opSrvs, serializeOpenAPIServer(srv, fmt.Sprintf("%s.servers[%d]", locPrefix, i), isTarget31, red, recordCompat))
		}
		opMap["servers"] = opSrvs
	}

	// Parameters (merge contract.Parameters + path template params + req.HTTP.Query / Headers)
	params := buildExportParameters(req, contract, opPath, locPrefix, isTarget31, includeExamples, schemaFileToComponentName, red, recordCompat)
	if len(params) > 0 {
		opMap["parameters"] = params
	}

	// RequestBody
	reqBody := buildExportRequestBody(req, contract, locPrefix+".requestBody", isTarget31, includeExamples, schemaFileToComponentName, red, recordCompat)
	if len(reqBody) > 0 {
		opMap["requestBody"] = reqBody
	}

	// Responses
	opMap["responses"] = buildExportResponses(req, contract, locPrefix+".responses", isTarget31, includeExamples, schemaFileToComponentName, red, recordCompat)

	// Security
	if contract != nil && contract.Security != nil {
		if len(contract.Security) == 0 {
			opMap["security"] = []any{}
		} else {
			secList := make([]any, 0, len(contract.Security))
			for _, secMap := range contract.Security {
				m := map[string]any{}
				for _, k := range sortedMapKeys(secMap) {
					scopes := secMap[k]
					if scopes == nil {
						scopes = []string{}
					}
					m[k] = scopes
				}
				secList = append(secList, m)
			}
			opMap["security"] = secList
		}
	} else if req.Auth != nil {
		switch req.Auth.Mode {
		case "none":
			opMap["security"] = []any{}
		case "ref":
			schemeName := authIDToSchemeName[req.Auth.Ref]
			if schemeName == "" {
				schemeName = req.Auth.Ref
			}
			if schemeName != "" {
				opMap["security"] = []any{
					map[string]any{schemeName: []string{}},
				}
			}
		}
	}

	if contract != nil && len(contract.Callbacks) > 0 {
		opMap["callbacks"] = scrubAnyExampleValue(contract.Callbacks, red)
	}

	return opMap
}

func buildExportParameters(
	req *Request,
	contract *OperationContract,
	opPath string,
	locPrefix string,
	isTarget31 bool,
	includeExamples bool,
	schemaFileToComponentName map[string]string,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) []any {
	var out []any
	seen := map[string]bool{} // in + ":" + lower(name)

	pathVars := extractTemplateParams(opPath)
	pathVarSet := map[string]bool{}
	for _, pv := range pathVars {
		pathVarSet[pv] = true
	}

	if contract != nil {
		for idx, prm := range contract.Parameters {
			inLoc := strings.ToLower(strings.TrimSpace(prm.In))
			name := strings.TrimSpace(prm.Name)
			if inLoc == "" || name == "" {
				continue
			}
			if inLoc == "path" && !pathVarSet[name] {
				continue
			}
			key := inLoc + ":" + strings.ToLower(name)
			seen[key] = true

			pMap := map[string]any{
				"name": red.RedactString(name),
				"in":   inLoc,
			}
			if prm.Description != "" {
				pMap["description"] = red.RedactString(prm.Description)
			}
			if inLoc == "path" || prm.Required {
				pMap["required"] = true
			}
			if prm.Deprecated {
				pMap["deprecated"] = true
			}
			if prm.Style != "" {
				pMap["style"] = prm.Style
			}
			if prm.Explode != nil {
				pMap["explode"] = *prm.Explode
			}

			schemaMap := exportSchemaOrRef(prm.Schema, prm.SchemaRef, schemaFileToComponentName, red)
			if len(schemaMap) == 0 {
				schemaMap = map[string]any{"type": "string"}
			}
			pMap["schema"] = schemaMap

			if includeExamples {
				if prm.Example != nil && !isTemplatePlaceholderString(prm.Example) {
					pMap["example"] = scrubAnyExampleValue(prm.Example, red)
				}
				if len(prm.Examples) > 0 {
					exMap := exportExamplesMap(prm.Examples, fmt.Sprintf("%s.parameters[%d].examples", locPrefix, idx), isTarget31, red, recordCompat)
					if len(exMap) > 0 {
						pMap["examples"] = exMap
					}
				}
			}
			out = append(out, pMap)
		}
	}

	// Ensure every `{param}` in the path template has a corresponding `in: path, required: true` parameter
	for _, pv := range pathVars {
		key := "path:" + strings.ToLower(pv)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, map[string]any{
			"name":     pv,
			"in":       "path",
			"required": true,
			"schema":   map[string]any{"type": "string"},
		})
	}

	// Include any extra query/header parameters from req.HTTP that weren't in contract.Parameters
	if req != nil && req.HTTP != nil {
		for _, q := range req.HTTP.Query {
			name := strings.TrimSpace(q.Name)
			if name == "" || !q.IsEnabled() {
				continue
			}
			key := "query:" + strings.ToLower(name)
			if seen[key] {
				continue
			}
			seen[key] = true
			pMap := map[string]any{
				"name":   red.RedactString(name),
				"in":     "query",
				"schema": map[string]any{"type": "string"},
			}
			if includeExamples && q.Value != "" && !strings.Contains(q.Value, "{{") && !looksSensitiveFieldName(name) {
				pMap["example"] = red.RedactString(q.Value)
			}
			out = append(out, pMap)
		}
		for _, h := range req.HTTP.Headers {
			name := strings.TrimSpace(h.Name)
			if name == "" || !h.IsEnabled() {
				continue
			}
			lower := strings.ToLower(name)
			if lower == "content-type" || lower == "accept" || lower == "authorization" {
				continue
			}
			key := "header:" + lower
			if seen[key] {
				continue
			}
			seen[key] = true
			pMap := map[string]any{
				"name":   red.RedactString(name),
				"in":     "header",
				"schema": map[string]any{"type": "string"},
			}
			if includeExamples && h.Value != "" && !strings.Contains(h.Value, "{{") && !looksSensitiveFieldName(name) {
				pMap["example"] = red.RedactString(h.Value)
			}
			out = append(out, pMap)
		}
	}

	return out
}

func extractTemplateParams(opPath string) []string {
	matches := pathTemplateParamRe.FindAllStringSubmatch(opPath, -1)
	var out []string
	seen := map[string]bool{}
	for _, m := range matches {
		name := strings.TrimSpace(m[1])
		if name != "" && !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	return out
}

func buildExportRequestBody(
	req *Request,
	contract *OperationContract,
	locPrefix string,
	isTarget31 bool,
	includeExamples bool,
	schemaFileToComponentName map[string]string,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	if contract != nil && contract.RequestBody != nil && len(contract.RequestBody.Content) > 0 {
		rbMap := map[string]any{}
		if contract.RequestBody.Description != "" {
			rbMap["description"] = red.RedactString(contract.RequestBody.Description)
		}
		if contract.RequestBody.Required {
			rbMap["required"] = true
		}
		rbMap["content"] = exportContentMap(
			contract.RequestBody.Content,
			locPrefix+".content",
			isTarget31,
			includeExamples,
			schemaFileToComponentName,
			red,
			recordCompat,
		)
		return rbMap
	}

	if req == nil || req.HTTP == nil || req.HTTP.Body == nil {
		return nil
	}
	tb, _ := InspectBody(req.HTTP.Body)
	if tb == nil || tb.Kind == BodyNone || tb.Kind == "" {
		return nil
	}

	contentMap := map[string]any{}
	switch tb.Kind {
	case BodyJSON:
		mtObj := map[string]any{
			"schema": map[string]any{"type": "object"},
		}
		if includeExamples && tb.JSONNode != nil {
			var val any
			if err := tb.JSONNode.Decode(&val); err == nil && val != nil {
				mtObj["example"] = scrubAnyExampleValue(val, red)
			}
		}
		contentMap["application/json"] = mtObj
	case BodyURLEncoded:
		props := map[string]any{}
		for _, f := range tb.FormFields {
			if strings.TrimSpace(f.Name) != "" {
				props[f.Name] = map[string]any{"type": "string"}
			}
		}
		contentMap["application/x-www-form-urlencoded"] = map[string]any{
			"schema": map[string]any{
				"type":       "object",
				"properties": props,
			},
		}
	case BodyFormData:
		props := map[string]any{}
		for _, p := range tb.MultipartParts {
			if strings.TrimSpace(p.Name) == "" {
				continue
			}
			if p.EffectiveKind() == "file" {
				props[p.Name] = map[string]any{
					"type":   "string",
					"format": "binary",
				}
			} else {
				props[p.Name] = map[string]any{"type": "string"}
			}
		}
		contentMap["multipart/form-data"] = map[string]any{
			"schema": map[string]any{
				"type":       "object",
				"properties": props,
			},
		}
	default:
		contentMap["text/plain"] = map[string]any{
			"schema": map[string]any{"type": "string"},
		}
	}

	return map[string]any{
		"content": contentMap,
	}
}

func buildExportResponses(
	req *Request,
	contract *OperationContract,
	locPrefix string,
	isTarget31 bool,
	includeExamples bool,
	schemaFileToComponentName map[string]string,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	resps := map[string]any{}
	if contract != nil && len(contract.Responses) > 0 {
		for _, statusKey := range sortedMapKeys(contract.Responses) {
			rc := contract.Responses[statusKey]
			desc := strings.TrimSpace(rc.Description)
			if desc == "" {
				desc = "Response " + statusKey
			}
			rObj := map[string]any{
				"description": red.RedactString(desc),
			}
			if len(rc.Headers) > 0 {
				hdrs := map[string]any{}
				for _, hName := range sortedMapKeys(rc.Headers) {
					hc := rc.Headers[hName]
					hm := map[string]any{}
					if hc.Description != "" {
						hm["description"] = red.RedactString(hc.Description)
					}
					if hc.Required {
						hm["required"] = true
					}
					if hc.Deprecated {
						hm["deprecated"] = true
					}
					sm := exportSchemaOrRef(hc.Schema, hc.SchemaRef, schemaFileToComponentName, red)
					if len(sm) == 0 {
						sm = map[string]any{"type": "string"}
					}
					hm["schema"] = sm
					if includeExamples && hc.Example != nil {
						hm["example"] = scrubAnyExampleValue(hc.Example, red)
					}
					hdrs[hName] = hm
				}
				rObj["headers"] = hdrs
			}
			if len(rc.Content) > 0 {
				rObj["content"] = exportContentMap(
					rc.Content,
					fmt.Sprintf("%s.%s.content", locPrefix, statusKey),
					isTarget31,
					includeExamples,
					schemaFileToComponentName,
					red,
					recordCompat,
				)
			}
			resps[statusKey] = rObj
		}
		return resps
	}

	// Fallback: check if req.Assert references a schema file, otherwise emit a minimal 200 response
	resp200 := map[string]any{
		"description": "OK",
	}
	if schemaRel := findSchemaAssertionPath(req.Assert); schemaRel != "" {
		sm := exportSchemaOrRef(nil, schemaRel, schemaFileToComponentName, red)
		if len(sm) > 0 {
			resp200["content"] = map[string]any{
				"application/json": map[string]any{
					"schema": sm,
				},
			}
		}
	}
	resps["200"] = resp200
	return resps
}

func exportContentMap(
	content map[string]MediaTypeContract,
	locPrefix string,
	isTarget31 bool,
	includeExamples bool,
	schemaFileToComponentName map[string]string,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	out := map[string]any{}
	for _, mtKey := range sortedMapKeys(content) {
		mc := content[mtKey]
		mtLoc := locPrefix + "." + mtKey
		mObj := map[string]any{}

		sm := exportSchemaOrRef(mc.Schema, mc.SchemaRef, schemaFileToComponentName, red)
		if len(sm) > 0 {
			mObj["schema"] = sm
		}
		if len(mc.Encoding) > 0 {
			mObj["encoding"] = scrubAnyExampleValue(mc.Encoding, red)
		}
		if len(mc.ItemSchema) > 0 {
			if isTarget31 {
				recordCompat("MediaType.itemSchema", mtLoc+".itemSchema", "omitted", fmt.Sprintf("MediaType.itemSchema on %s requires OpenAPI 3.2", mtKey))
			} else {
				mObj["itemSchema"] = rewriteInternalSchemaRefs(mc.ItemSchema, schemaFileToComponentName, red)
			}
		}
		if len(mc.ItemEncoding) > 0 {
			if isTarget31 {
				recordCompat("MediaType.itemEncoding", mtLoc+".itemEncoding", "omitted", fmt.Sprintf("MediaType.itemEncoding on %s requires OpenAPI 3.2", mtKey))
			} else {
				mObj["itemEncoding"] = scrubAnyExampleValue(mc.ItemEncoding, red)
			}
		}
		if len(mc.PrefixEncoding) > 0 {
			if isTarget31 {
				recordCompat("MediaType.prefixEncoding", mtLoc+".prefixEncoding", "omitted", fmt.Sprintf("MediaType.prefixEncoding on %s requires OpenAPI 3.2", mtKey))
			} else {
				mObj["prefixEncoding"] = scrubAnyExampleValue(mc.PrefixEncoding, red)
			}
		}

		if includeExamples {
			if mc.Example != nil && !isTemplatePlaceholderString(mc.Example) {
				mObj["example"] = scrubAnyExampleValue(mc.Example, red)
			}
			if len(mc.Examples) > 0 {
				exMap := exportExamplesMap(mc.Examples, mtLoc+".examples", isTarget31, red, recordCompat)
				if len(exMap) > 0 {
					mObj["examples"] = exMap
				}
			}
		}
		out[mtKey] = mObj
	}
	return out
}

func exportExamplesMap(
	examples map[string]OpenAPIExampleObject,
	locPrefix string,
	isTarget31 bool,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	out := map[string]any{}
	for _, exName := range sortedMapKeys(examples) {
		ex := examples[exName]
		exLoc := locPrefix + "." + exName
		em := map[string]any{}
		if ex.Summary != "" {
			em["summary"] = red.RedactString(ex.Summary)
		}
		if ex.Description != "" {
			em["description"] = red.RedactString(ex.Description)
		}
		if ex.ExternalValue != "" {
			em["externalValue"] = red.RedactString(ex.ExternalValue)
		}
		if ex.Value != nil {
			em["value"] = scrubAnyExampleValue(ex.Value, red)
		}
		if ex.DataValue != nil {
			if isTarget31 {
				recordCompat("Example.dataValue", exLoc+".dataValue", "downgraded", fmt.Sprintf("Example %q uses OpenAPI 3.2 'dataValue'", exName))
				if _, hasVal := em["value"]; !hasVal {
					em["value"] = scrubAnyExampleValue(ex.DataValue, red)
				}
			} else {
				em["dataValue"] = scrubAnyExampleValue(ex.DataValue, red)
			}
		}
		if ex.SerializedValue != "" {
			if isTarget31 {
				recordCompat("Example.serializedValue", exLoc+".serializedValue", "downgraded", fmt.Sprintf("Example %q uses OpenAPI 3.2 'serializedValue'", exName))
				if _, hasVal := em["value"]; !hasVal {
					em["value"] = red.RedactString(ex.SerializedValue)
				}
			} else {
				em["serializedValue"] = red.RedactString(ex.SerializedValue)
			}
		}
		out[exName] = em
	}
	return out
}

func exportSchemaOrRef(
	inlineSchema map[string]any,
	schemaRef string,
	schemaFileToComponentName map[string]string,
	red *Redactor,
) map[string]any {
	ref := strings.TrimSpace(schemaRef)
	if ref != "" {
		cleanRef := filepath.ToSlash(path.Clean(strings.TrimPrefix(ref, ".gorequest/")))
		if compName, ok := schemaFileToComponentName[cleanRef]; ok {
			return map[string]any{
				"$ref": "#/components/schemas/" + compName,
			}
		}
		if strings.HasPrefix(ref, "#/components/schemas/") {
			return map[string]any{"$ref": ref}
		}
	}
	if len(inlineSchema) > 0 {
		return rewriteInternalSchemaRefs(inlineSchema, schemaFileToComponentName, red)
	}
	if ref != "" {
		return map[string]any{"$ref": ref}
	}
	return nil
}

func rewriteInternalSchemaRefs(
	v any,
	schemaFileToComponentName map[string]string,
	red *Redactor,
) map[string]any {
	m, ok := v.(map[string]any)
	if !ok {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, child := range m {
		if k == "$ref" {
			if refStr, ok := child.(string); ok {
				trimmed := strings.TrimSpace(refStr)
				if strings.HasPrefix(trimmed, "#/$defs/") {
					out[k] = "#/components/schemas/" + strings.TrimPrefix(trimmed, "#/$defs/")
					continue
				}
				clean := filepath.ToSlash(path.Clean(strings.TrimPrefix(trimmed, ".gorequest/")))
				if compName, found := schemaFileToComponentName[clean]; found {
					out[k] = "#/components/schemas/" + compName
					continue
				}
			}
		}
		out[k] = rewriteInternalSchemaRefsAny(child, schemaFileToComponentName, red)
	}
	return out
}

func rewriteInternalSchemaRefsAny(
	v any,
	schemaFileToComponentName map[string]string,
	red *Redactor,
) any {
	switch val := v.(type) {
	case map[string]any:
		return rewriteInternalSchemaRefs(val, schemaFileToComponentName, red)
	case []any:
		out := make([]any, len(val))
		for i, elem := range val {
			out[i] = rewriteInternalSchemaRefsAny(elem, schemaFileToComponentName, red)
		}
		return out
	case string:
		if red != nil {
			return red.RedactString(val)
		}
		return val
	default:
		return val
	}
}

func loadProjectComponentSchemasMap(p *Project) map[string]string {
	out := map[string]string{}
	if p == nil || p.Store == nil {
		return out
	}
	files, err := p.Store.List("schemas", func(name string) bool {
		return strings.HasSuffix(name, ".schema.json")
	})
	if err != nil {
		return out
	}
	for _, rel := range files {
		compName := strings.TrimSuffix(path.Base(rel), ".schema.json")
		if b, _, rerr := p.Store.ReadBounded(rel, DefaultMaxSchemaBytes); rerr == nil {
			if parsed, perr := decodeStructuredValue(b); perr == nil {
				if m, ok := parsed.(map[string]any); ok {
					if title := stringField(m, "title"); title != "" {
						compName = title
					}
				}
			}
		}
		out[rel] = compName
	}
	return out
}

func buildExportComponentSchemas(
	p *Project,
	schemaFileToComponentName map[string]string,
	red *Redactor,
) map[string]any {
	out := map[string]any{}
	if p == nil || p.Store == nil {
		return out
	}
	for _, rel := range sortedMapKeys(schemaFileToComponentName) {
		compName := schemaFileToComponentName[rel]
		b, _, err := p.Store.ReadBounded(rel, DefaultMaxSchemaBytes)
		if err != nil {
			continue
		}
		parsed, err := decodeStructuredValue(b)
		if err != nil {
			continue
		}
		m, ok := parsed.(map[string]any)
		if !ok {
			continue
		}
		rewritten := rewriteInternalSchemaRefs(m, schemaFileToComponentName, red)
		delete(rewritten, "$schema")
		delete(rewritten, "$defs")
		out[compName] = rewritten
	}
	return out
}

func buildAuthIDToSchemeNameMap(p *Project, meta *ProjectOpenAPIMetadata) map[string]string {
	out := map[string]string{}
	if meta != nil {
		for _, schemeName := range sortedMapKeys(meta.SecuritySchemes) {
			authID := sanitizeSlug(schemeName, "auth")
			out[authID] = schemeName
			out[schemeName] = schemeName
		}
	}
	if p != nil {
		for _, id := range sortedMapKeys(p.AuthSchemes) {
			if _, exists := out[id]; !exists {
				out[id] = id
			}
		}
	}
	return out
}

func buildExportSecuritySchemes(
	p *Project,
	meta *ProjectOpenAPIMetadata,
	isTarget31 bool,
	red *Redactor,
	recordCompat func(feature, loc, res, msg string),
) map[string]any {
	out := map[string]any{}
	exportedAuthIDs := map[string]bool{}

	if meta != nil && len(meta.SecuritySchemes) > 0 {
		for _, schemeName := range sortedMapKeys(meta.SecuritySchemes) {
			sm := meta.SecuritySchemes[schemeName]
			exportedAuthIDs[sanitizeSlug(schemeName, "auth")] = true
			exportedAuthIDs[schemeName] = true
			loc := "components.securitySchemes." + schemeName

			sObj := map[string]any{
				"type": sm.Type,
			}
			if sm.Description != "" {
				sObj["description"] = red.RedactString(sm.Description)
			}
			if sm.Name != "" {
				sObj["name"] = red.RedactString(sm.Name)
			}
			if sm.In != "" {
				sObj["in"] = sm.In
			}
			if sm.Scheme != "" {
				sObj["scheme"] = sm.Scheme
			}
			if sm.BearerFormat != "" {
				sObj["bearerFormat"] = red.RedactString(sm.BearerFormat)
			}
			if sm.OpenIDConnectURL != "" {
				sObj["openIdConnectUrl"] = red.RedactString(sm.OpenIDConnectURL)
			}
			if sm.Deprecated {
				if isTarget31 {
					recordCompat("SecurityScheme.deprecated", loc+".deprecated", "omitted", fmt.Sprintf("SecurityScheme %q 'deprecated' field requires OpenAPI 3.2", schemeName))
				} else {
					sObj["deprecated"] = true
				}
			}
			if sm.OAuth2MetadataURL != "" {
				if isTarget31 {
					recordCompat("SecurityScheme.oauth2MetadataUrl", loc+".oauth2MetadataUrl", "omitted", fmt.Sprintf("SecurityScheme %q 'oauth2MetadataUrl' field requires OpenAPI 3.2", schemeName))
				} else {
					sObj["oauth2MetadataUrl"] = red.RedactString(sm.OAuth2MetadataURL)
				}
			}
			if len(sm.Flows) > 0 {
				flowsCopy := make(map[string]any, len(sm.Flows))
				for _, fk := range sortedMapKeys(sm.Flows) {
					if fk == "deviceAuthorization" && isTarget31 {
						recordCompat("OAuthFlows.deviceAuthorization", loc+".flows.deviceAuthorization", "omitted", fmt.Sprintf("SecurityScheme %q 'flows.deviceAuthorization' requires OpenAPI 3.2", schemeName))
						continue
					}
					flowsCopy[fk] = scrubAnyExampleValue(sm.Flows[fk], red)
				}
				sObj["flows"] = flowsCopy
			}
			out[schemeName] = sObj
		}
	}

	if p != nil {
		for _, authID := range sortedMapKeys(p.AuthSchemes) {
			if exportedAuthIDs[authID] {
				continue
			}
			ac := p.AuthSchemes[authID]
			if ac == nil {
				continue
			}
			sObj := convertAuthSchemeToOpenAPISecurityScheme(ac, red)
			if len(sObj) > 0 {
				out[ac.ID] = sObj
			}
		}
	}
	return out
}

func convertAuthSchemeToOpenAPISecurityScheme(ac *AuthScheme, red *Redactor) map[string]any {
	if ac == nil {
		return nil
	}
	// Never resolve or export actual secret values — only structural scheme metadata.
	switch strings.ToLower(strings.TrimSpace(ac.Type)) {
	case "bearer":
		return map[string]any{
			"type":   "http",
			"scheme": "bearer",
		}
	case "basic":
		return map[string]any{
			"type":   "http",
			"scheme": "basic",
		}
	case "apikey":
		inLoc := NormalizeAPIKeyLocation(ac.In)
		keyName := strings.TrimSpace(ac.Name)
		if keyName == "" {
			keyName = "X-API-Key"
		}
		return map[string]any{
			"type": "apiKey",
			"in":   inLoc,
			"name": red.RedactString(keyName),
		}
	default:
		return nil
	}
}

func deriveOperationPath(req *Request, contract *OperationContract) string {
	if contract != nil && strings.TrimSpace(contract.PathTemplate) != "" {
		p := strings.TrimSpace(contract.PathTemplate)
		if !strings.HasPrefix(p, "/") {
			p = "/" + p
		}
		return p
	}
	if req == nil || req.HTTP == nil {
		return "/"
	}
	rawURL := strings.TrimSpace(req.HTTP.URL)
	if rawURL == "" {
		return "/"
	}
	if m := serverVarTemplateRe.FindStringSubmatch(rawURL); len(m) == 3 {
		rest := m[2]
		if rest == "" {
			rest = "/"
		}
		return convertMustacheVarsToOpenAPITemplate(rest)
	}
	if strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "https://") {
		if u, err := url.Parse(rawURL); err == nil && u.Path != "" {
			return convertMustacheVarsToOpenAPITemplate(u.Path)
		}
	}
	if idx := strings.IndexByte(rawURL, '?'); idx >= 0 {
		rawURL = rawURL[:idx]
	}
	if !strings.HasPrefix(rawURL, "/") {
		rawURL = "/" + rawURL
	}
	return convertMustacheVarsToOpenAPITemplate(rawURL)
}

var mustacheVarRe = regexp.MustCompile(`\{\{([A-Za-z0-9_.-]+)\}\}`)

func convertMustacheVarsToOpenAPITemplate(rawPath string) string {
	return mustacheVarRe.ReplaceAllString(rawPath, "{$1}")
}

func hasOperationExamples(req *Request, contract *OperationContract) bool {
	if contract != nil {
		for _, prm := range contract.Parameters {
			if prm.Example != nil || len(prm.Examples) > 0 {
				return true
			}
		}
		if contract.RequestBody != nil {
			for _, mt := range contract.RequestBody.Content {
				if mt.Example != nil || len(mt.Examples) > 0 {
					return true
				}
			}
		}
		for _, rc := range contract.Responses {
			for _, mt := range rc.Content {
				if mt.Example != nil || len(mt.Examples) > 0 {
					return true
				}
			}
		}
	}
	if req != nil && req.HTTP != nil && req.HTTP.Body != nil && req.HTTP.Body.Value != nil {
		return true
	}
	return false
}

func isTemplatePlaceholderString(v any) bool {
	s, ok := v.(string)
	return ok && strings.HasPrefix(strings.TrimSpace(s), "{{") && strings.HasSuffix(strings.TrimSpace(s), "}}")
}

func extractDocBodyAfterHeading(mdText string, heading string) string {
	trimmed := strings.TrimSpace(mdText)
	if strings.HasPrefix(trimmed, "# ") {
		if nl := strings.IndexByte(trimmed, '\n'); nl >= 0 {
			firstLine := strings.TrimSpace(strings.TrimPrefix(trimmed[:nl], "#"))
			if firstLine == strings.TrimSpace(heading) || firstLine != "" {
				return strings.TrimSpace(trimmed[nl+1:])
			}
		}
	}
	return trimmed
}

func writeAtomicExternalFile(targetPath string, data []byte, overwrite bool) error {
	cleanTarget := filepath.Clean(targetPath)
	if !overwrite {
		if _, err := os.Stat(cleanTarget); err == nil {
			return fmt.Errorf("target file %q already exists (enable overwrite to replace)", cleanTarget)
		}
	}
	parentDir := filepath.Dir(cleanTarget)
	if err := os.MkdirAll(parentDir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parentDir, ".gorequest-openapi-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	cleanup := func() {
		_ = os.Remove(tmpName)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpName, cleanTarget); err != nil {
		cleanup()
		return err
	}
	return nil
}
