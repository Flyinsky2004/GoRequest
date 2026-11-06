package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Supported OpenAPI / Swagger specification version constants.
const (
	OpenAPIVersion31 = "3.1.2"
	OpenAPIVersion32 = "3.2.1"
	OpenAPIVersion30 = "3.0.3"
	SwaggerVersion20 = "2.0"
)

// SourceMode identifies whether the GoRequest project or an external OpenAPI
// specification is the primary contract source of truth (openapi-auth.md §2).
type SourceMode string

const (
	SourceModeNative       SourceMode = "native"
	SourceModeExternalSpec SourceMode = "external-spec"
)

var (
	// ErrOpenAPILossyRejected indicates that strict export or import rejected a
	// specification because one or more constructs would be lost or downgraded.
	ErrOpenAPILossyRejected = errors.New("openapi specification or export contains lossy/incompatible constructs rejected in strict mode")
	// ErrOpenAPIValidationFailed indicates that a specification failed structural,
	// version, parameter, or offline $ref validation.
	ErrOpenAPIValidationFailed = errors.New("openapi specification validation failed")
)

// OpenAPIContact represents the OpenAPI Info.contact object.
type OpenAPIContact struct {
	Name  string `json:"name,omitempty" yaml:"name,omitempty"`
	URL   string `json:"url,omitempty" yaml:"url,omitempty"`
	Email string `json:"email,omitempty" yaml:"email,omitempty"`
}

// OpenAPILicense represents the OpenAPI Info.license object (including 3.1/3.2 SPDX identifier).
type OpenAPILicense struct {
	Name       string `json:"name" yaml:"name"`
	Identifier string `json:"identifier,omitempty" yaml:"identifier,omitempty"`
	URL        string `json:"url,omitempty" yaml:"url,omitempty"`
}

// OpenAPIExternalDocs represents an OpenAPI externalDocs reference.
type OpenAPIExternalDocs struct {
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
	URL         string `json:"url" yaml:"url"`
}

// OpenAPIServerVariable represents a template variable inside an OpenAPI Server URL.
type OpenAPIServerVariable struct {
	Enum        []string `json:"enum,omitempty" yaml:"enum,omitempty"`
	Default     string   `json:"default" yaml:"default"`
	Description string   `json:"description,omitempty" yaml:"description,omitempty"`
}

// OpenAPIServer represents an OpenAPI Server object (including 3.2 `name`).
type OpenAPIServer struct {
	URL         string                           `json:"url" yaml:"url"`
	Description string                           `json:"description,omitempty" yaml:"description,omitempty"`
	Name        string                           `json:"name,omitempty" yaml:"name,omitempty"` // OpenAPI 3.2+
	Variables   map[string]OpenAPIServerVariable `json:"variables,omitempty" yaml:"variables,omitempty"`
}

// OpenAPITag represents an OpenAPI Tag object (including 3.2 `summary`, `parent`, `kind`).
type OpenAPITag struct {
	Name         string               `json:"name" yaml:"name"`
	Summary      string               `json:"summary,omitempty" yaml:"summary,omitempty"` // OpenAPI 3.2+
	Description  string               `json:"description,omitempty" yaml:"description,omitempty"`
	Parent       string               `json:"parent,omitempty" yaml:"parent,omitempty"` // OpenAPI 3.2+
	Kind         string               `json:"kind,omitempty" yaml:"kind,omitempty"`     // OpenAPI 3.2+
	ExternalDocs *OpenAPIExternalDocs `json:"externalDocs,omitempty" yaml:"externalDocs,omitempty"`
}

// OpenAPIExampleObject represents a reusable or media-type Example object
// (including 3.2 `dataValue` and `serializedValue`).
type OpenAPIExampleObject struct {
	Summary         string `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description     string `json:"description,omitempty" yaml:"description,omitempty"`
	Value           any    `json:"value,omitempty" yaml:"value,omitempty"`
	DataValue       any    `json:"dataValue,omitempty" yaml:"dataValue,omitempty"`             // OpenAPI 3.2+
	SerializedValue string `json:"serializedValue,omitempty" yaml:"serializedValue,omitempty"` // OpenAPI 3.2+
	ExternalValue   string `json:"externalValue,omitempty" yaml:"externalValue,omitempty"`
}

// OpenAPISecuritySchemeMeta stores extra OpenAPI 3.1/3.2 securityScheme metadata
// (such as 3.2 `deprecated`, `oauth2MetadataUrl`, `flows.deviceAuthorization`,
// or OpenID Connect discovery URL) without ever storing secret values.
type OpenAPISecuritySchemeMeta struct {
	Type              string         `json:"type" yaml:"type"`
	Description       string         `json:"description,omitempty" yaml:"description,omitempty"`
	Name              string         `json:"name,omitempty" yaml:"name,omitempty"`
	In                string         `json:"in,omitempty" yaml:"in,omitempty"`
	Scheme            string         `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	BearerFormat      string         `json:"bearerFormat,omitempty" yaml:"bearerFormat,omitempty"`
	OpenIDConnectURL  string         `json:"openIdConnectUrl,omitempty" yaml:"openIdConnectUrl,omitempty"`
	OAuth2MetadataURL string         `json:"oauth2MetadataUrl,omitempty" yaml:"oauth2MetadataUrl,omitempty"` // OpenAPI 3.2+
	Deprecated        bool           `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`               // OpenAPI 3.2+
	Flows             map[string]any `json:"flows,omitempty" yaml:"flows,omitempty"`
}

// OperationParam describes an explicit OpenAPI parameter contract on a Request
// (path, query, header, or cookie).
type OperationParam struct {
	Name        string                          `json:"name" yaml:"name"`
	In          string                          `json:"in" yaml:"in"` // path | query | header | cookie
	Description string                          `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                            `json:"required,omitempty" yaml:"required,omitempty"`
	Deprecated  bool                            `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Style       string                          `json:"style,omitempty" yaml:"style,omitempty"`
	Explode     *bool                           `json:"explode,omitempty" yaml:"explode,omitempty"`
	Schema      map[string]any                  `json:"schema,omitempty" yaml:"schema,omitempty"`
	SchemaRef   string                          `json:"schemaRef,omitempty" yaml:"schemaRef,omitempty"`
	Example     any                             `json:"example,omitempty" yaml:"example,omitempty"`
	Examples    map[string]OpenAPIExampleObject `json:"examples,omitempty" yaml:"examples,omitempty"`
}

// MediaTypeContract describes one media type entry (e.g. "application/json") in
// a requestBody or response contract.
type MediaTypeContract struct {
	Schema         map[string]any                  `json:"schema,omitempty" yaml:"schema,omitempty"`
	SchemaRef      string                          `json:"schemaRef,omitempty" yaml:"schemaRef,omitempty"`
	Example        any                             `json:"example,omitempty" yaml:"example,omitempty"`
	Examples       map[string]OpenAPIExampleObject `json:"examples,omitempty" yaml:"examples,omitempty"`
	Encoding       map[string]any                  `json:"encoding,omitempty" yaml:"encoding,omitempty"`
	ItemSchema     map[string]any                  `json:"itemSchema,omitempty" yaml:"itemSchema,omitempty"`         // OpenAPI 3.2+
	ItemEncoding   map[string]any                  `json:"itemEncoding,omitempty" yaml:"itemEncoding,omitempty"`     // OpenAPI 3.2+
	PrefixEncoding []map[string]any                `json:"prefixEncoding,omitempty" yaml:"prefixEncoding,omitempty"` // OpenAPI 3.2+
}

// OperationRequestBody describes the OpenAPI requestBody contract for an operation,
// supporting multiple content media types.
type OperationRequestBody struct {
	Description string                       `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool                         `json:"required,omitempty" yaml:"required,omitempty"`
	Content     map[string]MediaTypeContract `json:"content,omitempty" yaml:"content,omitempty"`
}

// HeaderContract describes a declared response header in an OpenAPI Response object.
type HeaderContract struct {
	Description string         `json:"description,omitempty" yaml:"description,omitempty"`
	Required    bool           `json:"required,omitempty" yaml:"required,omitempty"`
	Deprecated  bool           `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	Schema      map[string]any `json:"schema,omitempty" yaml:"schema,omitempty"`
	SchemaRef   string         `json:"schemaRef,omitempty" yaml:"schemaRef,omitempty"`
	Example     any            `json:"example,omitempty" yaml:"example,omitempty"`
}

// ResponseContract describes one status code entry ("200", "201", "400", "default")
// in an Operation's responses map, supporting multiple media types and headers.
type ResponseContract struct {
	Description string                       `json:"description" yaml:"description"`
	Headers     map[string]HeaderContract    `json:"headers,omitempty" yaml:"headers,omitempty"`
	Content     map[string]MediaTypeContract `json:"content,omitempty" yaml:"content,omitempty"`
}

// ContractSourceRef records the external specification lineage for an operation
// when imported in External Spec Mode (`external-spec`), enabling 3-way sync preview.
type ContractSourceRef struct {
	SpecPath         string            `json:"specPath,omitempty" yaml:"specPath,omitempty"`
	SpecDigest       string            `json:"specDigest,omitempty" yaml:"specDigest,omitempty"`
	SpecVersion      string            `json:"specVersion,omitempty" yaml:"specVersion,omitempty"`
	Path             string            `json:"path" yaml:"path"`
	Method           string            `json:"method" yaml:"method"`
	OperationID      string            `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	BaselineOpDigest string            `json:"baselineOpDigest,omitempty" yaml:"baselineOpDigest,omitempty"`
	BaselineSummary  string            `json:"baselineSummary,omitempty" yaml:"baselineSummary,omitempty"`
	BaselineParams   []string          `json:"baselineParams,omitempty" yaml:"baselineParams,omitempty"`
	BaselineStatuses []string          `json:"baselineStatuses,omitempty" yaml:"baselineStatuses,omitempty"`
	BaselineQuery    map[string]string `json:"baselineQuery,omitempty" yaml:"baselineQuery,omitempty"`
	BaselineHeaders  map[string]string `json:"baselineHeaders,omitempty" yaml:"baselineHeaders,omitempty"`
	BaselineBodyType string            `json:"baselineBodyType,omitempty" yaml:"baselineBodyType,omitempty"`
	BaselineBodyHash string            `json:"baselineBodyHash,omitempty" yaml:"baselineBodyHash,omitempty"`
}

// OperationContract holds operation-level OpenAPI 3.1/3.2 contract metadata on
// a GoRequest Request (persisted under `extensions.openapi`).
type OperationContract struct {
	OperationID  string                      `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	Summary      string                      `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description  string                      `json:"description,omitempty" yaml:"description,omitempty"`
	Deprecated   bool                        `json:"deprecated,omitempty" yaml:"deprecated,omitempty"`
	PathTemplate string                      `json:"pathTemplate,omitempty" yaml:"pathTemplate,omitempty"`
	Parameters   []OperationParam            `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBody  *OperationRequestBody       `json:"requestBody,omitempty" yaml:"requestBody,omitempty"`
	Responses    map[string]ResponseContract `json:"responses,omitempty" yaml:"responses,omitempty"`
	Servers      []OpenAPIServer             `json:"servers,omitempty" yaml:"servers,omitempty"`
	Security     []map[string][]string       `json:"security,omitempty" yaml:"security,omitempty"`
	ExternalDocs *OpenAPIExternalDocs        `json:"externalDocs,omitempty" yaml:"externalDocs,omitempty"`
	Callbacks    map[string]any              `json:"callbacks,omitempty" yaml:"callbacks,omitempty"`
	Source       *ContractSourceRef          `json:"source,omitempty" yaml:"source,omitempty"`
}

// ExternalSpecState tracks the imported external OpenAPI specification at the
// project level when Mode == SourceModeExternalSpec.
type ExternalSpecState struct {
	SourcePath        string            `json:"sourcePath" yaml:"sourcePath"`
	ContentDigest     string            `json:"contentDigest" yaml:"contentDigest"`
	SpecVersion       string            `json:"specVersion" yaml:"specVersion"`
	OperationMappings map[string]string `json:"operationMappings,omitempty" yaml:"operationMappings,omitempty"` // operationKey (METHOD /path or operationId) -> request ID
}

// ProjectOpenAPIMetadata holds project-level OpenAPI 3.1/3.2 contract metadata
// (persisted under `project.yaml` `extensions.openapi`).
type ProjectOpenAPIMetadata struct {
	Mode                 SourceMode                           `json:"mode,omitempty" yaml:"mode,omitempty"`
	DefaultTargetVersion string                               `json:"defaultTargetVersion,omitempty" yaml:"defaultTargetVersion,omitempty"`
	APIVersion           string                               `json:"apiVersion,omitempty" yaml:"apiVersion,omitempty"`
	Summary              string                               `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description          string                               `json:"description,omitempty" yaml:"description,omitempty"`
	SelfURI              string                               `json:"selfUri,omitempty" yaml:"selfUri,omitempty"` // OpenAPI 3.2 `$self`
	TermsOfService       string                               `json:"termsOfService,omitempty" yaml:"termsOfService,omitempty"`
	Contact              *OpenAPIContact                      `json:"contact,omitempty" yaml:"contact,omitempty"`
	License              *OpenAPILicense                      `json:"license,omitempty" yaml:"license,omitempty"`
	Servers              []OpenAPIServer                      `json:"servers,omitempty" yaml:"servers,omitempty"`
	Tags                 []OpenAPITag                         `json:"tags,omitempty" yaml:"tags,omitempty"`
	Webhooks             map[string]any                       `json:"webhooks,omitempty" yaml:"webhooks,omitempty"`
	SecuritySchemes      map[string]OpenAPISecuritySchemeMeta `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
	ExternalSpec         *ExternalSpecState                   `json:"externalSpec,omitempty" yaml:"externalSpec,omitempty"`
}

// GetRequestOpenAPI extracts the typed *OperationContract from req.Extensions["openapi"].
// Returns nil, nil when no OpenAPI metadata is attached to the request.
func GetRequestOpenAPI(req *Request) (*OperationContract, error) {
	if req == nil || len(req.Extensions) == 0 {
		return nil, nil
	}
	raw, ok := req.Extensions["openapi"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal request extensions.openapi: %w", err)
	}
	var contract OperationContract
	if err := yaml.Unmarshal(b, &contract); err != nil {
		return nil, fmt.Errorf("unmarshal request extensions.openapi: %w", err)
	}
	return &contract, nil
}

// SetRequestOpenAPI stores contract into req.Extensions["openapi"] as a clean
// YAML-compatible map so standard Request serialization and YAML preservation
// write it cleanly. Passing nil removes the "openapi" extension key.
func SetRequestOpenAPI(req *Request, contract *OperationContract) error {
	if req == nil {
		return configErrorf("request is nil")
	}
	if contract == nil {
		if req.Extensions != nil {
			delete(req.Extensions, "openapi")
			if len(req.Extensions) == 0 {
				req.Extensions = nil
			}
		}
		return nil
	}
	b, err := yaml.Marshal(contract)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return err
	}
	if len(m) == 0 {
		if req.Extensions != nil {
			delete(req.Extensions, "openapi")
			if len(req.Extensions) == 0 {
				req.Extensions = nil
			}
		}
		return nil
	}
	if req.Extensions == nil {
		req.Extensions = map[string]any{}
	}
	req.Extensions["openapi"] = m
	return nil
}

// GetProjectOpenAPI extracts the typed *ProjectOpenAPIMetadata from pf.Extensions["openapi"].
func GetProjectOpenAPI(pf *ProjectFile) (*ProjectOpenAPIMetadata, error) {
	if pf == nil || len(pf.Extensions) == 0 {
		return nil, nil
	}
	raw, ok := pf.Extensions["openapi"]
	if !ok || raw == nil {
		return nil, nil
	}
	b, err := yaml.Marshal(raw)
	if err != nil {
		return nil, fmt.Errorf("marshal project extensions.openapi: %w", err)
	}
	var meta ProjectOpenAPIMetadata
	if err := yaml.Unmarshal(b, &meta); err != nil {
		return nil, fmt.Errorf("unmarshal project extensions.openapi: %w", err)
	}
	return &meta, nil
}

// SetProjectOpenAPI stores meta into pf.Extensions["openapi"].
func SetProjectOpenAPI(pf *ProjectFile, meta *ProjectOpenAPIMetadata) error {
	if pf == nil {
		return configErrorf("project file is nil")
	}
	if meta == nil {
		if pf.Extensions != nil {
			delete(pf.Extensions, "openapi")
			if len(pf.Extensions) == 0 {
				pf.Extensions = nil
			}
		}
		return nil
	}
	b, err := yaml.Marshal(meta)
	if err != nil {
		return err
	}
	var m map[string]any
	if err := yaml.Unmarshal(b, &m); err != nil {
		return err
	}
	if len(m) == 0 {
		if pf.Extensions != nil {
			delete(pf.Extensions, "openapi")
			if len(pf.Extensions) == 0 {
				pf.Extensions = nil
			}
		}
		return nil
	}
	if pf.Extensions == nil {
		pf.Extensions = map[string]any{}
	}
	pf.Extensions["openapi"] = m
	return nil
}

// SaveRequestWithOpenAPI updates req with contract and writes it back to
// entry.RelPath with baseline conflict checking and comment/order preservation
// (patching the top-level `extensions:` AST node even when only `extensions`
// changed).
func SaveRequestWithOpenAPI(p *Project, entry *RequestEntry, req *Request, contract *OperationContract, baseline string) (string, error) {
	if p == nil || p.Store == nil {
		return "", configErrorf("project or store is nil")
	}
	if entry == nil {
		return "", configErrorf("request entry is nil")
	}
	if req == nil {
		return "", configErrorf("request is nil")
	}
	cp := *req
	if cp.Extensions != nil {
		extCopy := make(map[string]any, len(cp.Extensions))
		for k, v := range cp.Extensions {
			extCopy[k] = v
		}
		cp.Extensions = extCopy
	}
	if err := SetRequestOpenAPI(&cp, contract); err != nil {
		return "", err
	}

	var diskBytes []byte
	if baseline != "" {
		b, diskDigest, err := p.Store.ReadBounded(entry.RelPath, p.Store.EffectiveLimits().MaxYAMLBytes)
		if err != nil {
			if isNotExistErr(err) {
				return "", &ConflictError{Path: entry.RelPath, Baseline: baseline, Disk: ""}
			}
			return "", err
		}
		if diskDigest != baseline {
			return "", &ConflictError{Path: entry.RelPath, Baseline: baseline, Disk: diskDigest}
		}
		diskBytes = b
	} else if p.Store.Exists(entry.RelPath) {
		_, diskDigest, rerr := p.Store.ReadBounded(entry.RelPath, p.Store.EffectiveLimits().MaxYAMLBytes)
		if rerr == nil {
			return "", &ConflictError{Path: entry.RelPath, Baseline: "", Disk: diskDigest}
		}
	}

	merged, err := MergeRequestYAML(diskBytes, &cp)
	if err != nil {
		return "", err
	}
	merged, err = patchTopLevelExtensionsAST(merged, cp.Extensions)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked(entry.RelPath, merged, baseline); err != nil {
		return "", err
	}
	newDigest := Digest(merged)
	if cp.SchemaVersion == 0 {
		cp.SchemaVersion = SupportedSchemaVersion
	}
	entry.BaselineDigest = newDigest
	entry.Request = &cp
	p.Requests[cp.ID] = entry
	p.revalidate()
	return newDigest, nil
}

// SaveProjectOpenAPIMetadata writes updated ProjectOpenAPIMetadata into
// project.yaml with baseline checking and comment preservation.
func SaveProjectOpenAPIMetadata(p *Project, meta *ProjectOpenAPIMetadata, baseline string) (string, error) {
	if p == nil || p.Store == nil || p.File == nil {
		return "", configErrorf("project or store is nil")
	}
	diskBytes, diskDigest, err := p.Store.ReadBounded("project.yaml", p.Store.EffectiveLimits().MaxYAMLBytes)
	if err != nil {
		return "", err
	}
	if baseline != "" && diskDigest != baseline {
		return "", &ConflictError{Path: "project.yaml", Baseline: baseline, Disk: diskDigest}
	}
	cp := *p.File
	if cp.Extensions != nil {
		extCopy := make(map[string]any, len(cp.Extensions))
		for k, v := range cp.Extensions {
			extCopy[k] = v
		}
		cp.Extensions = extCopy
	}
	if err := SetProjectOpenAPI(&cp, meta); err != nil {
		return "", err
	}
	merged, err := MergeProjectFileYAML(diskBytes, &cp)
	if err != nil {
		return "", err
	}
	merged, err = patchTopLevelExtensionsAST(merged, cp.Extensions)
	if err != nil {
		return "", err
	}
	if err := p.Store.SaveChecked("project.yaml", merged, diskDigest); err != nil {
		return "", err
	}
	p.File = &cp
	return Digest(merged), nil
}

func patchTopLevelExtensionsAST(yamlBytes []byte, extensions map[string]any) ([]byte, error) {
	var doc yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(yamlBytes))
	if err := dec.Decode(&doc); err != nil {
		return nil, err
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 || doc.Content[0].Kind != yaml.MappingNode {
		return yamlBytes, nil
	}
	root := doc.Content[0]
	if len(extensions) == 0 {
		filtered := make([]*yaml.Node, 0, len(root.Content))
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "extensions" {
				continue
			}
			filtered = append(filtered, root.Content[i], root.Content[i+1])
		}
		root.Content = filtered
		return encodeDocumentNode(&doc)
	}

	extDoc, err := marshalStructToNode(extensions)
	if err != nil {
		return nil, err
	}
	extNode := cloneYAMLNode(extDoc.Content[0], true)
	replaced := false
	for i := 0; i+1 < len(root.Content); i += 2 {
		if root.Content[i].Value == "extensions" {
			copyNodeComments(extNode, root.Content[i+1])
			root.Content[i+1] = extNode
			replaced = true
			break
		}
	}
	if !replaced {
		root.Content = append(root.Content,
			&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "extensions"},
			extNode,
		)
	}
	return encodeDocumentNode(&doc)
}

// ValidateHTTPResponseContract selects the response contract Schema by
// Operation, HTTP status code, and Content-Type media type (or fallback
// `req.Assert` schema entry) and validates the response headers and body.
func ValidateHTTPResponseContract(p *Project, req *Request, statusCode int, headers []KV, body []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	if req == nil {
		return nil, configErrorf("request is nil")
	}
	opts = opts.effective()
	if p != nil && opts.BaseDir == "" {
		opts.BaseDir = p.Dir
	}

	contract, err := GetRequestOpenAPI(req)
	if err != nil {
		return nil, err
	}

	contentType := extractMediaTypeFromHeaders(headers)
	if contentType == "" {
		contentType = "application/json"
	}

	// 1. Try matching from OperationContract.Responses
	if contract != nil && len(contract.Responses) > 0 {
		matchedStatusKey, respContract, foundStatus := matchResponseStatusContract(contract.Responses, statusCode)
		if !foundStatus {
			return &SchemaValidationResult{
				Valid:            false,
				MatchedStatus:    "",
				MatchedMediaType: contentType,
				Violations: []SchemaValidationViolation{
					{
						InstancePath:    "$.status",
						InstancePointer: "/status",
						SchemaPath:      "#/responses",
						Keyword:         "status",
						Expected:        strings.Join(sortedMapKeys(contract.Responses), " | "),
						Actual:          strconv.Itoa(statusCode),
						Message:         fmt.Sprintf("HTTP status %d is not declared in operation responses (%s)", statusCode, strings.Join(sortedMapKeys(contract.Responses), ", ")),
					},
				},
			}, nil
		}

		var violations []SchemaValidationViolation
		var missingDeps []MissingRefDependency

		// Check required response headers if declared in ResponseContract.Headers
		for _, hName := range sortedMapKeys(respContract.Headers) {
			hSpec := respContract.Headers[hName]
			actualVal, present := findHeaderValueCaseInsensitive(headers, hName)
			if hSpec.Required && !present {
				violations = append(violations, SchemaValidationViolation{
					InstancePath:    appendJSONPathKey("$.headers", hName),
					InstancePointer: appendJSONPointerKey("/headers", hName),
					SchemaPath:      "#/responses/" + matchedStatusKey + "/headers/" + escapeJSONPointerToken(hName),
					Keyword:         "required",
					Expected:        fmt.Sprintf("header %q present", hName),
					Actual:          "missing",
					Message:         fmt.Sprintf("missing required response header %q", hName),
				})
				continue
			}
			if present && len(hSpec.Schema) > 0 {
				coerced := coerceHeaderScalarForSchema(actualVal, hSpec.Schema)
				hRes, herr := ValidateParsedSchema(hSpec.Schema, coerced, opts)
				if herr != nil {
					return nil, herr
				}
				for _, v := range hRes.Violations {
					v.InstancePath = appendJSONPathKey("$.headers", hName)
					v.InstancePointer = appendJSONPointerKey("/headers", hName)
					violations = append(violations, v)
				}
			}
		}

		if len(respContract.Content) > 0 {
			matchedMT, mtContract, foundMT := matchMediaTypeContract(respContract.Content, contentType)
			if !foundMT {
				violations = append(violations, SchemaValidationViolation{
					InstancePath:    "$.contentType",
					InstancePointer: "/contentType",
					SchemaPath:      "#/responses/" + matchedStatusKey + "/content",
					Keyword:         "content",
					Expected:        strings.Join(sortedMapKeys(respContract.Content), " | "),
					Actual:          contentType,
					Message:         fmt.Sprintf("response Content-Type %q is not declared in #%s responses (expected %s)", contentType, matchedStatusKey, strings.Join(sortedMapKeys(respContract.Content), ", ")),
				})
				return &SchemaValidationResult{
					Valid:            false,
					MatchedStatus:    matchedStatusKey,
					MatchedMediaType: contentType,
					Violations:       violations,
				}, nil
			}

			bodyRes, berr := validateBodyAgainstMediaTypeContract(p, mtContract, body, opts)
			if bodyRes != nil {
				violations = append(violations, bodyRes.Violations...)
				missingDeps = append(missingDeps, bodyRes.MissingDependencies...)
			}
			if berr != nil {
				return &SchemaValidationResult{
					Valid:               false,
					MatchedStatus:       matchedStatusKey,
					MatchedMediaType:    matchedMT,
					SchemaSource:        mtContract.SchemaRef,
					Violations:          violations,
					MissingDependencies: missingDeps,
				}, berr
			}
			src := mtContract.SchemaRef
			if src == "" {
				src = fmt.Sprintf("responses[%s].content[%s].schema", matchedStatusKey, matchedMT)
			}
			return &SchemaValidationResult{
				Valid:               len(violations) == 0 && len(missingDeps) == 0,
				MatchedStatus:       matchedStatusKey,
				MatchedMediaType:    matchedMT,
				SchemaSource:        src,
				Violations:          violations,
				MissingDependencies: missingDeps,
			}, nil
		}

		return &SchemaValidationResult{
			Valid:            len(violations) == 0,
			MatchedStatus:    matchedStatusKey,
			MatchedMediaType: contentType,
			Violations:       violations,
		}, nil
	}

	// 2. Fallback: look for `type: schema` assertion in req.Assert
	if schemaRel := findSchemaAssertionPath(req.Assert); schemaRel != "" {
		res, err := DefaultSchemaValidator{}.ValidateProjectSchemaFile(p, schemaRel, body, opts)
		if res != nil {
			res.MatchedStatus = strconv.Itoa(statusCode)
			res.MatchedMediaType = contentType
		}
		return res, err
	}

	return nil, configErrorf("request %q has no response schema contract or schema assertion configured", req.ID)
}

func validateBodyAgainstMediaTypeContract(p *Project, mt MediaTypeContract, body []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	if mt.SchemaRef != "" && len(mt.Schema) == 0 {
		ref := strings.TrimSpace(mt.SchemaRef)
		if !strings.HasPrefix(ref, "#") && !isRemoteURI(ref) && p != nil && p.Store != nil {
			filePart := ref
			fragPart := ""
			if idx := strings.IndexByte(ref, '#'); idx >= 0 {
				filePart = ref[:idx]
				fragPart = ref[idx+1:]
			}
			cleanFile := filepath.ToSlash(path.Clean(filePart))
			if fragPart == "" {
				return DefaultSchemaValidator{}.ValidateProjectSchemaFile(p, cleanFile, body, opts)
			}
			schemaBytes, _, err := p.Store.ReadBounded(cleanFile, opts.MaxSchemaBytes)
			if err != nil {
				return nil, fmt.Errorf("read referenced schema %q: %w", cleanFile, err)
			}
			docVal, err := decodeStructuredValue(schemaBytes)
			if err != nil {
				return nil, err
			}
			docMap, _ := docVal.(map[string]any)
			opts.RootDocument = docMap
			opts.CurrentFileRel = cleanFile
			instVal, err := decodeJSONInstancePreservingNumbers(body)
			if err != nil {
				return nil, fmt.Errorf("parse JSON response body: %w", err)
			}
			return ValidateParsedSchema(map[string]any{"$ref": "#" + fragPart}, instVal, opts)
		}
		instVal, err := decodeJSONInstancePreservingNumbers(body)
		if err != nil {
			return nil, fmt.Errorf("parse JSON response body: %w", err)
		}
		return ValidateParsedSchema(map[string]any{"$ref": ref}, instVal, opts)
	}
	if len(mt.Schema) > 0 {
		instVal, err := decodeJSONInstancePreservingNumbers(body)
		if err != nil {
			return nil, fmt.Errorf("parse JSON response body: %w", err)
		}
		return ValidateParsedSchema(mt.Schema, instVal, opts)
	}
	return &SchemaValidationResult{Valid: true}, nil
}

func matchResponseStatusContract(responses map[string]ResponseContract, statusCode int) (string, ResponseContract, bool) {
	exact := strconv.Itoa(statusCode)
	if rc, ok := responses[exact]; ok {
		return exact, rc, true
	}
	classUpper := fmt.Sprintf("%dXX", statusCode/100)
	if rc, ok := responses[classUpper]; ok {
		return classUpper, rc, true
	}
	classLower := fmt.Sprintf("%dxx", statusCode/100)
	if rc, ok := responses[classLower]; ok {
		return classLower, rc, true
	}
	if rc, ok := responses["default"]; ok {
		return "default", rc, true
	}
	return "", ResponseContract{}, false
}

func matchMediaTypeContract(content map[string]MediaTypeContract, actualContentType string) (string, MediaTypeContract, bool) {
	baseMT := normalizeMediaType(actualContentType)
	if mt, ok := content[baseMT]; ok {
		return baseMT, mt, true
	}
	for k, mt := range content {
		if normalizeMediaType(k) == baseMT {
			return k, mt, true
		}
	}
	// Structured syntax suffix match, e.g., application/problem+json matches application/json
	if strings.HasSuffix(baseMT, "+json") {
		if mt, ok := content["application/json"]; ok {
			return "application/json", mt, true
		}
	}
	if mt, ok := content["*/*"]; ok {
		return "*/*", mt, true
	}
	return "", MediaTypeContract{}, false
}

func normalizeMediaType(raw string) string {
	s := strings.TrimSpace(raw)
	if idx := strings.IndexByte(s, ';'); idx >= 0 {
		s = s[:idx]
	}
	return strings.ToLower(strings.TrimSpace(s))
}

func extractMediaTypeFromHeaders(headers []KV) string {
	val, ok := findHeaderValueCaseInsensitive(headers, "Content-Type")
	if !ok {
		return ""
	}
	return normalizeMediaType(val)
}

func findHeaderValueCaseInsensitive(headers []KV, target string) (string, bool) {
	for _, kv := range headers {
		if !kv.IsEnabled() {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(kv.Name), strings.TrimSpace(target)) {
			return kv.Value, true
		}
	}
	return "", false
}

func coerceHeaderScalarForSchema(val string, schema map[string]any) any {
	allowed := extractAllowedTypes(schema["type"])
	for _, t := range allowed {
		switch t {
		case "integer":
			if _, err := strconv.ParseInt(strings.TrimSpace(val), 10, 64); err == nil {
				return json.Number(strings.TrimSpace(val))
			}
		case "number":
			if _, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
				return json.Number(strings.TrimSpace(val))
			}
		case "boolean":
			if b, err := strconv.ParseBool(strings.TrimSpace(val)); err == nil {
				return b
			}
		}
	}
	return val
}

func findSchemaAssertionPath(assertNodes []yaml.Node) string {
	for _, n := range assertNodes {
		var m map[string]any
		if err := n.Decode(&m); err == nil {
			t, _ := m["type"].(string)
			if strings.EqualFold(strings.TrimSpace(t), "schema") {
				if s, ok := m["schema"].(string); ok && strings.TrimSpace(s) != "" {
					return strings.TrimSpace(s)
				}
			}
		}
	}
	return ""
}
