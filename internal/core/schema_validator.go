package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	stdmath "math"
	"net"
	"net/mail"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

// Default limits for offline Schema and OpenAPI $ref resolution.
const (
	DefaultMaxSchemaDepth    = 48
	DefaultMaxSchemaRefFiles = 128
	DefaultMaxSchemaBytes    = 8 << 20 // 8 MiB
)

var (
	// ErrRemoteRefBlocked is returned when a schema or OpenAPI document
	// references a remote URI (http/https/ws/ftp) while offline mode is active.
	ErrRemoteRefBlocked = errors.New("remote $ref resolution is disabled by offline security policy")
	// ErrSchemaRefCycle is returned when a circular $ref chain is detected.
	ErrSchemaRefCycle = errors.New("circular $ref detected in schema")
	// ErrSchemaPathTraversal is returned when a relative file $ref attempts to
	// escape the allowed root directory or traverse a symlink.
	ErrSchemaPathTraversal = errors.New("relative $ref escapes allowed root or traverses symlink")
)

// MissingRefDependency records a remote or missing local $ref target encountered
// during offline validation or import.
type MissingRefDependency struct {
	RefURI     string `json:"refUri" yaml:"refUri"`
	SourceFile string `json:"sourceFile,omitempty" yaml:"sourceFile,omitempty"`
	SchemaPath string `json:"schemaPath,omitempty" yaml:"schemaPath,omitempty"`
	IsRemote   bool   `json:"isRemote" yaml:"isRemote"`
	Reason     string `json:"reason" yaml:"reason"`
}

// SchemaValidationViolation describes one constraint failure on a JSON/YAML
// instance with exact JSONPath and RFC 6901 JSON Pointer locations.
type SchemaValidationViolation struct {
	InstancePath    string `json:"instancePath" yaml:"instancePath"`       // e.g. "$.data.id" or "$[0].name"
	InstancePointer string `json:"instancePointer" yaml:"instancePointer"` // e.g. "/data/id" or "/0/name"
	SchemaPath      string `json:"schemaPath" yaml:"schemaPath"`           // e.g. "#/properties/data/properties/id/type"
	Keyword         string `json:"keyword" yaml:"keyword"`                 // e.g. "type", "required", "enum"
	Expected        string `json:"expected,omitempty" yaml:"expected,omitempty"`
	Actual          string `json:"actual,omitempty" yaml:"actual,omitempty"`
	Message         string `json:"message" yaml:"message"`
}

func (v SchemaValidationViolation) String() string {
	if v.Expected != "" && v.Actual != "" {
		return fmt.Sprintf("%s (%s): %s (expected %s, got %s)", v.InstancePath, v.Keyword, v.Message, v.Expected, v.Actual)
	}
	return fmt.Sprintf("%s (%s): %s", v.InstancePath, v.Keyword, v.Message)
}

// SchemaValidationResult holds the outcome of validating an instance or HTTP
// response against a JSON Schema / OpenAPI Response Contract.
type SchemaValidationResult struct {
	Valid               bool                        `json:"valid" yaml:"valid"`
	MatchedStatus       string                      `json:"matchedStatus,omitempty" yaml:"matchedStatus,omitempty"`
	MatchedMediaType    string                      `json:"matchedMediaType,omitempty" yaml:"matchedMediaType,omitempty"`
	SchemaSource        string                      `json:"schemaSource,omitempty" yaml:"schemaSource,omitempty"`
	Violations          []SchemaValidationViolation `json:"violations,omitempty" yaml:"violations,omitempty"`
	MissingDependencies []MissingRefDependency      `json:"missingDependencies,omitempty" yaml:"missingDependencies,omitempty"`
}

// SchemaValidationOptions configures offline $ref confinement, dialect support,
// and resource bounds for schema validation.
type SchemaValidationOptions struct {
	// BaseDir is the root directory confining relative file $refs when Project
	// is nil (or when validating an external spec folder). If both Project and
	// BaseDir are empty, relative file $refs are rejected.
	BaseDir string
	// CurrentFileRel is the relative path of the document containing the root
	// schema (used to resolve sibling relative $refs).
	CurrentFileRel string
	// RootDocument is an optional full root document (such as an OpenAPI doc
	// containing #/components/schemas/...) used to resolve in-document #/ pointers.
	RootDocument map[string]any
	// AllowOAS30Nullable enables OpenAPI 3.0 `nullable: true` alongside Draft 2020-12.
	AllowOAS30Nullable bool
	// ValidateFormats enables semantic checks on `format` ("email", "uri", "uuid",
	// "date", "date-time", "ipv4", "ipv6").
	ValidateFormats bool
	MaxDepth        int
	MaxRefFiles     int
	MaxSchemaBytes  int64
}

func (o SchemaValidationOptions) effective() SchemaValidationOptions {
	if o.MaxDepth <= 0 {
		o.MaxDepth = DefaultMaxSchemaDepth
	}
	if o.MaxRefFiles <= 0 {
		o.MaxRefFiles = DefaultMaxSchemaRefFiles
	}
	if o.MaxSchemaBytes <= 0 {
		o.MaxSchemaBytes = DefaultMaxSchemaBytes
	}
	return o
}

// SchemaValidatorPort is the pure-Go port exposed by A03 for A01 Runner Core,
// assertion evaluators, and the Desktop UI to validate JSON instances and HTTP
// responses without duplicating schema validation logic.
type SchemaValidatorPort interface {
	ValidateInstance(schemaBytes []byte, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error)
	ValidateProjectSchemaFile(p *Project, schemaRelPath string, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error)
	ValidateResponseForRequest(p *Project, req *Request, statusCode int, headers []KV, body []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error)
}

// DefaultSchemaValidator is the standard offline SchemaValidatorPort implementation.
type DefaultSchemaValidator struct{}

// NewSchemaValidator returns a ready-to-use offline SchemaValidatorPort.
func NewSchemaValidator() SchemaValidatorPort {
	return DefaultSchemaValidator{}
}

// ValidateInstance validates a JSON (or YAML) instance payload against a JSON
// Schema (Draft 2020-12 / OpenAPI 3.1 / 3.2 / 3.0). It never performs network I/O.
func (DefaultSchemaValidator) ValidateInstance(schemaBytes []byte, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	return ValidateJSONSchemaBytes(schemaBytes, instanceBytes, opts)
}

// ValidateProjectSchemaFile loads schemaRelPath from p.Store (confined to .gorequest/)
// and validates instanceBytes against it.
func (DefaultSchemaValidator) ValidateProjectSchemaFile(p *Project, schemaRelPath string, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	if p == nil || p.Store == nil {
		return nil, configErrorf("project or store is nil")
	}
	opts = opts.effective()
	if opts.BaseDir == "" {
		opts.BaseDir = p.Dir
	}
	cleanRel := filepath.ToSlash(strings.TrimSpace(schemaRelPath))
	opts.CurrentFileRel = cleanRel
	schemaBytes, _, err := p.Store.ReadBounded(cleanRel, opts.MaxSchemaBytes)
	if err != nil {
		return nil, fmt.Errorf("read schema %q: %w", cleanRel, err)
	}
	res, err := ValidateJSONSchemaBytes(schemaBytes, instanceBytes, opts)
	if res != nil && res.SchemaSource == "" {
		res.SchemaSource = cleanRel
	}
	return res, err
}

// ValidateResponseForRequest selects the response contract Schema by Operation,
// status code, and Content-Type media type (falling back to any `type: schema`
// assertion on req.Assert) and validates the actual HTTP response headers and
// body, returning exact instance paths on mismatch.
func (DefaultSchemaValidator) ValidateResponseForRequest(p *Project, req *Request, statusCode int, headers []KV, body []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	return ValidateHTTPResponseContract(p, req, statusCode, headers, body, opts)
}

// ValidateJSONSchemaBytes parses schemaBytes and instanceBytes and runs full
// offline validation.
func ValidateJSONSchemaBytes(schemaBytes []byte, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	opts = opts.effective()
	if int64(len(schemaBytes)) > opts.MaxSchemaBytes {
		return nil, configErrorf("schema exceeds size limit (%d > %d bytes)", len(schemaBytes), opts.MaxSchemaBytes)
	}
	schemaNode, err := decodeStructuredValue(schemaBytes)
	if err != nil {
		return nil, fmt.Errorf("parse schema: %w", err)
	}
	instanceVal, err := decodeJSONInstancePreservingNumbers(instanceBytes)
	if err != nil {
		return nil, fmt.Errorf("parse JSON instance: %w", err)
	}

	rootDoc := opts.RootDocument
	if rootDoc == nil {
		if m, ok := schemaNode.(map[string]any); ok {
			rootDoc = m
		}
	}

	ctx := &schemaEvalContext{
		opts:       opts,
		rootDoc:    rootDoc,
		currentRel: opts.CurrentFileRel,
		fileCache:  map[string]any{},
		activeRefs: map[string]bool{},
	}
	ctx.validateValue(schemaNode, instanceVal, "$", "", "#")
	if ctx.fatalErr != nil {
		return &SchemaValidationResult{
			Valid:               false,
			Violations:          ctx.violations,
			MissingDependencies: ctx.missingDeps,
		}, ctx.fatalErr
	}
	return &SchemaValidationResult{
		Valid:               len(ctx.violations) == 0 && len(ctx.missingDeps) == 0,
		Violations:          ctx.violations,
		MissingDependencies: ctx.missingDeps,
	}, nil
}

// ValidateParsedSchema validates an already-decoded instance value against a
// decoded schema value (map[string]any or bool).
func ValidateParsedSchema(schemaNode any, instanceVal any, opts SchemaValidationOptions) (*SchemaValidationResult, error) {
	opts = opts.effective()
	rootDoc := opts.RootDocument
	if rootDoc == nil {
		if m, ok := schemaNode.(map[string]any); ok {
			rootDoc = m
		}
	}
	ctx := &schemaEvalContext{
		opts:       opts,
		rootDoc:    rootDoc,
		currentRel: opts.CurrentFileRel,
		fileCache:  map[string]any{},
		activeRefs: map[string]bool{},
	}
	ctx.validateValue(schemaNode, instanceVal, "$", "", "#")
	if ctx.fatalErr != nil {
		return &SchemaValidationResult{
			Valid:               false,
			Violations:          ctx.violations,
			MissingDependencies: ctx.missingDeps,
		}, ctx.fatalErr
	}
	return &SchemaValidationResult{
		Valid:               len(ctx.violations) == 0 && len(ctx.missingDeps) == 0,
		Violations:          ctx.violations,
		MissingDependencies: ctx.missingDeps,
	}, nil
}

type schemaEvalContext struct {
	opts        SchemaValidationOptions
	rootDoc     map[string]any
	currentRel  string
	fileCache   map[string]any
	activeRefs  map[string]bool
	violations  []SchemaValidationViolation
	missingDeps []MissingRefDependency
	fatalErr    error
	depth       int
}

func (c *schemaEvalContext) cloneSubContext() *schemaEvalContext {
	return &schemaEvalContext{
		opts:       c.opts,
		rootDoc:    c.rootDoc,
		currentRel: c.currentRel,
		fileCache:  c.fileCache,
		activeRefs: mapsCloneBool(c.activeRefs),
		depth:      c.depth,
	}
}

func mapsCloneBool(m map[string]bool) map[string]bool {
	out := make(map[string]bool, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func (c *schemaEvalContext) addViolation(instPath, instPtr, schemaPath, keyword, expected, actual, msg string) {
	c.violations = append(c.violations, SchemaValidationViolation{
		InstancePath:    instPath,
		InstancePointer: instPtr,
		SchemaPath:      schemaPath,
		Keyword:         keyword,
		Expected:        expected,
		Actual:          actual,
		Message:         msg,
	})
}

func (c *schemaEvalContext) validateValue(schemaNode any, inst any, instPath, instPtr, schemaPath string) {
	if c.fatalErr != nil {
		return
	}
	c.depth++
	defer func() { c.depth-- }()
	if c.depth > c.opts.MaxDepth {
		c.fatalErr = configErrorf("schema evaluation exceeded maximum depth (%d) at %s", c.opts.MaxDepth, schemaPath)
		return
	}

	switch s := schemaNode.(type) {
	case nil:
		return
	case bool:
		if !s {
			c.addViolation(instPath, instPtr, schemaPath, "booleanSchema", "never valid (false schema)", describeInstanceType(inst), "value disallowed by false schema")
		}
		return
	case map[string]any:
		c.validateSchemaMap(s, inst, instPath, instPtr, schemaPath)
	default:
		c.fatalErr = configErrorf("invalid schema node type %T at %s", schemaNode, schemaPath)
	}
}

func (c *schemaEvalContext) validateSchemaMap(s map[string]any, inst any, instPath, instPtr, schemaPath string) {
	// 1. Resolve $ref first (Draft 2020-12 / OAS 3.1 allows sibling keywords alongside $ref!).
	if refRaw, hasRef := s["$ref"].(string); hasRef && strings.TrimSpace(refRaw) != "" {
		refStr := strings.TrimSpace(refRaw)
		canonicalKey := c.currentRel + "::" + refStr
		if c.activeRefs[canonicalKey] {
			c.fatalErr = fmt.Errorf("%w: %s", ErrSchemaRefCycle, refStr)
			c.addViolation(instPath, instPtr, schemaPath+"/$ref", "$ref", "acyclic $ref", refStr, fmt.Sprintf("circular $ref detected: %s", refStr))
			return
		}
		resolvedTarget, newRoot, newRel, err := c.resolveRefTarget(refStr, schemaPath+"/$ref")
		if err != nil {
			if errors.Is(err, ErrRemoteRefBlocked) || errors.Is(err, ErrSchemaPathTraversal) || errors.Is(err, ErrSchemaRefCycle) {
				if c.fatalErr == nil {
					c.fatalErr = err
				}
			} else if c.fatalErr == nil {
				c.fatalErr = err
			}
			c.addViolation(instPath, instPtr, schemaPath+"/$ref", "$ref", "resolvable offline $ref", refStr, err.Error())
			return
		}
		prevRoot := c.rootDoc
		prevRel := c.currentRel
		c.activeRefs[canonicalKey] = true
		if newRoot != nil {
			c.rootDoc = newRoot
		}
		c.currentRel = newRel
		c.validateValue(resolvedTarget, inst, instPath, instPtr, refStr)
		c.rootDoc = prevRoot
		c.currentRel = prevRel
		delete(c.activeRefs, canonicalKey)
	}

	// 2. Check OpenAPI 3.0 nullable: true when enabled or present
	if nullable, ok := s["nullable"].(bool); ok && nullable && inst == nil {
		return
	}

	// 3. Check `type` (single string or array of strings in Draft 2020-12 / OAS 3.1)
	if typeRaw, hasType := s["type"]; hasType {
		allowedTypes := extractAllowedTypes(typeRaw)
		if len(allowedTypes) > 0 {
			actualType := describeInstanceType(inst)
			if !matchesAnyJSONType(inst, actualType, allowedTypes) {
				expectedDesc := strings.Join(allowedTypes, " | ")
				c.addViolation(
					instPath,
					instPtr,
					schemaPath+"/type",
					"type",
					expectedDesc,
					actualType,
					fmt.Sprintf("expected type %s at %s, got %s", expectedDesc, instPath, actualType),
				)
				return
			}
		}
	}

	// 4. `const`
	if constVal, hasConst := s["const"]; hasConst {
		if !jsonValuesEqual(inst, constVal) {
			c.addViolation(
				instPath,
				instPtr,
				schemaPath+"/const",
				"const",
				formatCompactJSONValue(constVal),
				formatCompactJSONValue(inst),
				fmt.Sprintf("value at %s must equal const %s", instPath, formatCompactJSONValue(constVal)),
			)
		}
	}

	// 5. `enum`
	if enumSlice, ok := s["enum"].([]any); ok && len(enumSlice) > 0 {
		matched := false
		var allowedStrs []string
		for _, item := range enumSlice {
			allowedStrs = append(allowedStrs, formatCompactJSONValue(item))
			if jsonValuesEqual(inst, item) {
				matched = true
				break
			}
		}
		if !matched {
			c.addViolation(
				instPath,
				instPtr,
				schemaPath+"/enum",
				"enum",
				"["+strings.Join(allowedStrs, ", ")+"]",
				formatCompactJSONValue(inst),
				fmt.Sprintf("value at %s is not one of allowed enum values", instPath),
			)
		}
	}

	// 6. Type-specific constraints
	switch v := inst.(type) {
	case string:
		c.validateStringConstraints(s, v, instPath, instPtr, schemaPath)
	case json.Number, float64, int, int64:
		if f, ok := toFloat64(v); ok {
			c.validateNumberConstraints(s, f, instPath, instPtr, schemaPath)
		}
	case []any:
		c.validateArrayConstraints(s, v, instPath, instPtr, schemaPath)
	case map[string]any:
		c.validateObjectConstraints(s, v, instPath, instPtr, schemaPath)
	}

	// 7. Combinators: allOf, anyOf, oneOf, not, if/then/else, discriminator
	c.validateCombinators(s, inst, instPath, instPtr, schemaPath)
}

func (c *schemaEvalContext) validateStringConstraints(s map[string]any, val string, instPath, instPtr, schemaPath string) {
	runeLen := utf8.RuneCountInString(val)
	if minL, ok := toInt(s["minLength"]); ok && runeLen < minL {
		c.addViolation(instPath, instPtr, schemaPath+"/minLength", "minLength", fmt.Sprintf(">= %d chars", minL), fmt.Sprintf("%d chars", runeLen), fmt.Sprintf("string length %d is less than minLength %d", runeLen, minL))
	}
	if maxL, ok := toInt(s["maxLength"]); ok && runeLen > maxL {
		c.addViolation(instPath, instPtr, schemaPath+"/maxLength", "maxLength", fmt.Sprintf("<= %d chars", maxL), fmt.Sprintf("%d chars", runeLen), fmt.Sprintf("string length %d exceeds maxLength %d", runeLen, maxL))
	}
	if pat, ok := s["pattern"].(string); ok && pat != "" {
		re, err := regexp.Compile(pat)
		if err != nil {
			c.addViolation(instPath, instPtr, schemaPath+"/pattern", "pattern", pat, val, fmt.Sprintf("invalid schema pattern %q: %v", pat, err))
		} else if !re.MatchString(val) {
			c.addViolation(instPath, instPtr, schemaPath+"/pattern", "pattern", fmt.Sprintf("matching /%s/", pat), fmt.Sprintf("%q", val), fmt.Sprintf("string does not match pattern %q", pat))
		}
	}
	if c.opts.ValidateFormats {
		if fmtName, ok := s["format"].(string); ok && fmtName != "" {
			if !checkStringFormat(fmtName, val) {
				c.addViolation(instPath, instPtr, schemaPath+"/format", "format", fmtName, fmt.Sprintf("%q", val), fmt.Sprintf("string does not match format %q", fmtName))
			}
		}
	}
}

func (c *schemaEvalContext) validateNumberConstraints(s map[string]any, val float64, instPath, instPtr, schemaPath string) {
	actualStr := strconv.FormatFloat(val, 'f', -1, 64)
	// Draft-04 / OAS 3.0 boolean exclusiveMinimum/Maximum vs Draft 2020-12 numeric exclusiveMinimum/Maximum
	exclMinBool, _ := s["exclusiveMinimum"].(bool)
	exclMaxBool, _ := s["exclusiveMaximum"].(bool)

	if minVal, ok := toFloat64(s["minimum"]); ok {
		if exclMinBool {
			if !(val > minVal) {
				c.addViolation(instPath, instPtr, schemaPath+"/minimum", "exclusiveMinimum", fmt.Sprintf("> %v", minVal), actualStr, fmt.Sprintf("number %s must be strictly greater than %v", actualStr, minVal))
			}
		} else if val < minVal {
			c.addViolation(instPath, instPtr, schemaPath+"/minimum", "minimum", fmt.Sprintf(">= %v", minVal), actualStr, fmt.Sprintf("number %s is less than minimum %v", actualStr, minVal))
		}
	}
	if exclMinNum, ok := toFloat64(s["exclusiveMinimum"]); ok {
		if !(val > exclMinNum) {
			c.addViolation(instPath, instPtr, schemaPath+"/exclusiveMinimum", "exclusiveMinimum", fmt.Sprintf("> %v", exclMinNum), actualStr, fmt.Sprintf("number %s must be strictly greater than %v", actualStr, exclMinNum))
		}
	}

	if maxVal, ok := toFloat64(s["maximum"]); ok {
		if exclMaxBool {
			if !(val < maxVal) {
				c.addViolation(instPath, instPtr, schemaPath+"/maximum", "exclusiveMaximum", fmt.Sprintf("< %v", maxVal), actualStr, fmt.Sprintf("number %s must be strictly less than %v", actualStr, maxVal))
			}
		} else if val > maxVal {
			c.addViolation(instPath, instPtr, schemaPath+"/maximum", "maximum", fmt.Sprintf("<= %v", maxVal), actualStr, fmt.Sprintf("number %s exceeds maximum %v", actualStr, maxVal))
		}
	}
	if exclMaxNum, ok := toFloat64(s["exclusiveMaximum"]); ok {
		if !(val < exclMaxNum) {
			c.addViolation(instPath, instPtr, schemaPath+"/exclusiveMaximum", "exclusiveMaximum", fmt.Sprintf("< %v", exclMaxNum), actualStr, fmt.Sprintf("number %s must be strictly less than %v", actualStr, exclMaxNum))
		}
	}

	if mult, ok := toFloat64(s["multipleOf"]); ok && mult > 0 {
		quotient := val / mult
		if stdmath.Abs(quotient-stdmath.Round(quotient)) > 1e-9 {
			c.addViolation(instPath, instPtr, schemaPath+"/multipleOf", "multipleOf", fmt.Sprintf("multiple of %v", mult), actualStr, fmt.Sprintf("number %s is not a multiple of %v", actualStr, mult))
		}
	}
}

func (c *schemaEvalContext) validateArrayConstraints(s map[string]any, arr []any, instPath, instPtr, schemaPath string) {
	if minI, ok := toInt(s["minItems"]); ok && len(arr) < minI {
		c.addViolation(instPath, instPtr, schemaPath+"/minItems", "minItems", fmt.Sprintf(">= %d items", minI), fmt.Sprintf("%d items", len(arr)), fmt.Sprintf("array length %d is less than minItems %d", len(arr), minI))
	}
	if maxI, ok := toInt(s["maxItems"]); ok && len(arr) > maxI {
		c.addViolation(instPath, instPtr, schemaPath+"/maxItems", "maxItems", fmt.Sprintf("<= %d items", maxI), fmt.Sprintf("%d items", len(arr)), fmt.Sprintf("array length %d exceeds maxItems %d", len(arr), maxI))
	}
	if uniq, ok := s["uniqueItems"].(bool); ok && uniq {
		for i := 0; i < len(arr); i++ {
			for j := i + 1; j < len(arr); j++ {
				if jsonValuesEqual(arr[i], arr[j]) {
					c.addViolation(instPath, instPtr, schemaPath+"/uniqueItems", "uniqueItems", "unique elements", fmt.Sprintf("duplicate at [%d] and [%d]", i, j), fmt.Sprintf("duplicate array items at indices %d and %d", i, j))
					i = len(arr)
					break
				}
			}
		}
	}

	prefixCount := 0
	if prefixItems, ok := s["prefixItems"].([]any); ok {
		prefixCount = len(prefixItems)
		for i, itemSchema := range prefixItems {
			if i >= len(arr) {
				break
			}
			elemPath := fmt.Sprintf("%s[%d]", instPath, i)
			elemPtr := fmt.Sprintf("%s/%d", instPtr, i)
			c.validateValue(itemSchema, arr[i], elemPath, elemPtr, fmt.Sprintf("%s/prefixItems/%d", schemaPath, i))
		}
	}

	if itemsSchema, hasItems := s["items"]; hasItems {
		for i := prefixCount; i < len(arr); i++ {
			elemPath := fmt.Sprintf("%s[%d]", instPath, i)
			elemPtr := fmt.Sprintf("%s/%d", instPtr, i)
			c.validateValue(itemsSchema, arr[i], elemPath, elemPtr, schemaPath+"/items")
		}
	}

	if containsSchema, hasContains := s["contains"]; hasContains {
		matchedAny := false
		for i, elem := range arr {
			sub := c.cloneSubContext()
			sub.validateValue(containsSchema, elem, fmt.Sprintf("%s[%d]", instPath, i), fmt.Sprintf("%s/%d", instPtr, i), schemaPath+"/contains")
			if len(sub.violations) == 0 && sub.fatalErr == nil {
				matchedAny = true
				break
			}
		}
		if !matchedAny {
			c.addViolation(instPath, instPtr, schemaPath+"/contains", "contains", "at least one matching item", fmt.Sprintf("%d items matched 0", len(arr)), "array does not contain any item matching 'contains' schema")
		}
	}
}

func (c *schemaEvalContext) validateObjectConstraints(s map[string]any, obj map[string]any, instPath, instPtr, schemaPath string) {
	if minP, ok := toInt(s["minProperties"]); ok && len(obj) < minP {
		c.addViolation(instPath, instPtr, schemaPath+"/minProperties", "minProperties", fmt.Sprintf(">= %d properties", minP), fmt.Sprintf("%d properties", len(obj)), fmt.Sprintf("object has %d properties, fewer than minProperties %d", len(obj), minP))
	}
	if maxP, ok := toInt(s["maxProperties"]); ok && len(obj) > maxP {
		c.addViolation(instPath, instPtr, schemaPath+"/maxProperties", "maxProperties", fmt.Sprintf("<= %d properties", maxP), fmt.Sprintf("%d properties", len(obj)), fmt.Sprintf("object has %d properties, exceeding maxProperties %d", len(obj), maxP))
	}

	// Required properties
	for _, reqProp := range extractStringSlice(s["required"]) {
		if _, exists := obj[reqProp]; !exists {
			propPath := appendJSONPathKey(instPath, reqProp)
			propPtr := appendJSONPointerKey(instPtr, reqProp)
			c.addViolation(propPath, propPtr, schemaPath+"/required", "required", fmt.Sprintf("property %q present", reqProp), "missing", fmt.Sprintf("missing required property %q at %s", reqProp, instPath))
		}
	}

	// dependentRequired
	if depReq, ok := s["dependentRequired"].(map[string]any); ok {
		keys := sortedMapKeys(depReq)
		for _, trigger := range keys {
			if _, present := obj[trigger]; !present {
				continue
			}
			for _, needed := range extractStringSlice(depReq[trigger]) {
				if _, ok := obj[needed]; !ok {
					propPath := appendJSONPathKey(instPath, needed)
					propPtr := appendJSONPointerKey(instPtr, needed)
					c.addViolation(propPath, propPtr, schemaPath+"/dependentRequired/"+escapeJSONPointerToken(trigger), "dependentRequired", fmt.Sprintf("property %q required when %q is present", needed, trigger), "missing", fmt.Sprintf("property %q is required when %q is present", needed, trigger))
				}
			}
		}
	}

	// dependentSchemas
	if depSchemas, ok := s["dependentSchemas"].(map[string]any); ok {
		for _, trigger := range sortedMapKeys(depSchemas) {
			if _, present := obj[trigger]; present {
				c.validateValue(depSchemas[trigger], obj, instPath, instPtr, schemaPath+"/dependentSchemas/"+escapeJSONPointerToken(trigger))
			}
		}
	}

	// propertyNames
	if propNamesSchema, hasPN := s["propertyNames"]; hasPN {
		for _, k := range sortedMapKeys(obj) {
			c.validateValue(propNamesSchema, k, appendJSONPathKey(instPath, k), appendJSONPointerKey(instPtr, k), schemaPath+"/propertyNames")
		}
	}

	propsMap, _ := s["properties"].(map[string]any)
	patPropsMap, _ := s["patternProperties"].(map[string]any)
	var compiledPatterns []struct {
		pat    string
		re     *regexp.Regexp
		schema any
	}
	for _, pat := range sortedMapKeys(patPropsMap) {
		if re, err := regexp.Compile(pat); err == nil {
			compiledPatterns = append(compiledPatterns, struct {
				pat    string
				re     *regexp.Regexp
				schema any
			}{pat: pat, re: re, schema: patPropsMap[pat]})
		}
	}

	addlProps, hasAddl := s["additionalProperties"]

	for _, k := range sortedMapKeys(obj) {
		val := obj[k]
		childPath := appendJSONPathKey(instPath, k)
		childPtr := appendJSONPointerKey(instPtr, k)
		matched := false

		if propsMap != nil {
			if propSchema, ok := propsMap[k]; ok {
				matched = true
				c.validateValue(propSchema, val, childPath, childPtr, schemaPath+"/properties/"+escapeJSONPointerToken(k))
			}
		}
		for _, cp := range compiledPatterns {
			if cp.re.MatchString(k) {
				matched = true
				c.validateValue(cp.schema, val, childPath, childPtr, schemaPath+"/patternProperties/"+escapeJSONPointerToken(cp.pat))
			}
		}
		if !matched && hasAddl {
			switch ap := addlProps.(type) {
			case bool:
				if !ap {
					c.addViolation(childPath, childPtr, schemaPath+"/additionalProperties", "additionalProperties", "no additional properties", fmt.Sprintf("unexpected property %q", k), fmt.Sprintf("additional property %q is not allowed at %s", k, instPath))
				}
			case map[string]any:
				c.validateValue(ap, val, childPath, childPtr, schemaPath+"/additionalProperties")
			}
		}
	}

	// OpenAPI discriminator check
	if disc, ok := s["discriminator"].(map[string]any); ok {
		if propName, ok := disc["propertyName"].(string); ok && strings.TrimSpace(propName) != "" {
			rawDiscVal, exists := obj[propName]
			if !exists {
				c.addViolation(appendJSONPathKey(instPath, propName), appendJSONPointerKey(instPtr, propName), schemaPath+"/discriminator/propertyName", "discriminator", fmt.Sprintf("discriminator property %q", propName), "missing", fmt.Sprintf("missing OpenAPI discriminator property %q", propName))
			} else if discStr, ok := rawDiscVal.(string); !ok || strings.TrimSpace(discStr) == "" {
				c.addViolation(appendJSONPathKey(instPath, propName), appendJSONPointerKey(instPtr, propName), schemaPath+"/discriminator/propertyName", "discriminator", "non-empty string", describeInstanceType(rawDiscVal), fmt.Sprintf("discriminator property %q must be a non-empty string", propName))
			} else if mapping, ok := disc["mapping"].(map[string]any); ok && len(mapping) > 0 {
				if targetRef, ok := mapping[discStr].(string); ok && targetRef != "" {
					c.validateSchemaMap(map[string]any{"$ref": targetRef}, obj, instPath, instPtr, schemaPath+"/discriminator/mapping/"+escapeJSONPointerToken(discStr))
				}
			}
		}
	}
}

func (c *schemaEvalContext) validateCombinators(s map[string]any, inst any, instPath, instPtr, schemaPath string) {
	// allOf: every sub-schema must be valid
	if allOf, ok := s["allOf"].([]any); ok {
		for i, subSchema := range allOf {
			c.validateValue(subSchema, inst, instPath, instPtr, fmt.Sprintf("%s/allOf/%d", schemaPath, i))
		}
	}

	// anyOf: at least one sub-schema must be valid
	if anyOf, ok := s["anyOf"].([]any); ok && len(anyOf) > 0 {
		matched := false
		for i, subSchema := range anyOf {
			sub := c.cloneSubContext()
			sub.validateValue(subSchema, inst, instPath, instPtr, fmt.Sprintf("%s/anyOf/%d", schemaPath, i))
			if sub.fatalErr != nil && c.fatalErr == nil {
				c.fatalErr = sub.fatalErr
			}
			c.missingDeps = append(c.missingDeps, sub.missingDeps...)
			if len(sub.violations) == 0 && sub.fatalErr == nil {
				matched = true
				break
			}
		}
		if !matched {
			c.addViolation(instPath, instPtr, schemaPath+"/anyOf", "anyOf", "matches at least 1 branch in anyOf", describeInstanceType(inst), fmt.Sprintf("value at %s did not match any branch in anyOf (%d branches)", instPath, len(anyOf)))
		}
	}

	// oneOf: exactly one sub-schema must be valid
	if oneOf, ok := s["oneOf"].([]any); ok && len(oneOf) > 0 {
		matchCount := 0
		for i, subSchema := range oneOf {
			sub := c.cloneSubContext()
			sub.validateValue(subSchema, inst, instPath, instPtr, fmt.Sprintf("%s/oneOf/%d", schemaPath, i))
			if sub.fatalErr != nil && c.fatalErr == nil {
				c.fatalErr = sub.fatalErr
			}
			c.missingDeps = append(c.missingDeps, sub.missingDeps...)
			if len(sub.violations) == 0 && sub.fatalErr == nil {
				matchCount++
			}
		}
		if matchCount != 1 {
			c.addViolation(instPath, instPtr, schemaPath+"/oneOf", "oneOf", "matches exactly 1 branch in oneOf", fmt.Sprintf("matched %d of %d branches", matchCount, len(oneOf)), fmt.Sprintf("value at %s matched %d branches in oneOf (expected exactly 1)", instPath, matchCount))
		}
	}

	// not: sub-schema must NOT be valid
	if notSchema, hasNot := s["not"]; hasNot {
		sub := c.cloneSubContext()
		sub.validateValue(notSchema, inst, instPath, instPtr, schemaPath+"/not")
		if len(sub.violations) == 0 && sub.fatalErr == nil {
			c.addViolation(instPath, instPtr, schemaPath+"/not", "not", "does not match 'not' schema", formatCompactJSONValue(inst), fmt.Sprintf("value at %s must not match 'not' schema", instPath))
		}
	}

	// if / then / else
	if ifSchema, hasIf := s["if"]; hasIf {
		sub := c.cloneSubContext()
		sub.validateValue(ifSchema, inst, instPath, instPtr, schemaPath+"/if")
		if len(sub.violations) == 0 && sub.fatalErr == nil {
			if thenSchema, hasThen := s["then"]; hasThen {
				c.validateValue(thenSchema, inst, instPath, instPtr, schemaPath+"/then")
			}
		} else {
			if elseSchema, hasElse := s["else"]; hasElse {
				c.validateValue(elseSchema, inst, instPath, instPtr, schemaPath+"/else")
			}
		}
	}
}

func (c *schemaEvalContext) resolveRefTarget(refStr string, schemaPath string) (target any, newRoot map[string]any, newRel string, err error) {
	if isRemoteURI(refStr) {
		c.missingDeps = append(c.missingDeps, MissingRefDependency{
			RefURI:     refStr,
			SourceFile: c.currentRel,
			SchemaPath: schemaPath,
			IsRemote:   true,
			Reason:     "remote $ref is blocked by default offline policy",
		})
		return nil, nil, "", fmt.Errorf("%w: %q", ErrRemoteRefBlocked, refStr)
	}

	filePart := refStr
	fragPart := ""
	if idx := strings.IndexByte(refStr, '#'); idx >= 0 {
		filePart = refStr[:idx]
		fragPart = refStr[idx+1:]
	}

	docRoot := c.rootDoc
	targetRel := c.currentRel

	if filePart != "" {
		loadedDoc, resolvedRel, lerr := c.loadRelativeRefFile(filePart, schemaPath)
		if lerr != nil {
			return nil, nil, "", lerr
		}
		targetRel = resolvedRel
		if m, ok := loadedDoc.(map[string]any); ok {
			docRoot = m
		} else if fragPart == "" || fragPart == "/" {
			return loadedDoc, nil, targetRel, nil
		} else {
			return nil, nil, "", fmt.Errorf("referenced file %q root is not an object for fragment #%s", filePart, fragPart)
		}
	}

	if fragPart == "" || fragPart == "/" {
		if docRoot == nil {
			return nil, nil, "", fmt.Errorf("empty root document for $ref %q", refStr)
		}
		return docRoot, docRoot, targetRel, nil
	}

	resolved, perr := evaluateJSONPointer(docRoot, fragPart)
	if perr != nil {
		return nil, nil, "", fmt.Errorf("resolve $ref %q: %w", refStr, perr)
	}
	return resolved, docRoot, targetRel, nil
}

func (c *schemaEvalContext) loadRelativeRefFile(relRef string, schemaPath string) (any, string, error) {
	if strings.TrimSpace(c.opts.BaseDir) == "" {
		c.missingDeps = append(c.missingDeps, MissingRefDependency{
			RefURI:     relRef,
			SourceFile: c.currentRel,
			SchemaPath: schemaPath,
			IsRemote:   false,
			Reason:     "no offline base directory configured for external file $ref",
		})
		return nil, "", fmt.Errorf("external file $ref %q cannot be resolved without BaseDir", relRef)
	}
	if strings.ContainsRune(relRef, '\\') || filepath.IsAbs(relRef) || strings.HasPrefix(relRef, "/") {
		return nil, "", fmt.Errorf("%w: %q", ErrSchemaPathTraversal, relRef)
	}
	if len(relRef) >= 2 && ((relRef[0] >= 'a' && relRef[0] <= 'z') || (relRef[0] >= 'A' && relRef[0] <= 'Z')) && relRef[1] == ':' {
		return nil, "", fmt.Errorf("%w: Windows drive path %q not allowed", ErrSchemaPathTraversal, relRef)
	}

	baseRelDir := path.Dir(filepath.ToSlash(c.currentRel))
	if baseRelDir == "." {
		baseRelDir = ""
	}
	joinedRel := path.Clean(path.Join(baseRelDir, relRef))
	if joinedRel == ".." || strings.HasPrefix(joinedRel, "../") || strings.HasPrefix(joinedRel, "/") {
		return nil, "", fmt.Errorf("%w: %q escapes root", ErrSchemaPathTraversal, relRef)
	}

	if cached, ok := c.fileCache[joinedRel]; ok {
		return cached, joinedRel, nil
	}
	if len(c.fileCache) >= c.opts.MaxRefFiles {
		return nil, "", configErrorf("exceeded maximum referenced schema files (%d)", c.opts.MaxRefFiles)
	}

	absPath, err := resolveSafeOfflineFile(c.opts.BaseDir, joinedRel)
	if err != nil {
		c.missingDeps = append(c.missingDeps, MissingRefDependency{
			RefURI:     relRef,
			SourceFile: c.currentRel,
			SchemaPath: schemaPath,
			IsRemote:   false,
			Reason:     err.Error(),
		})
		return nil, "", err
	}

	info, err := os.Lstat(absPath)
	if err != nil {
		c.missingDeps = append(c.missingDeps, MissingRefDependency{
			RefURI:     relRef,
			SourceFile: c.currentRel,
			SchemaPath: schemaPath,
			IsRemote:   false,
			Reason:     err.Error(),
		})
		return nil, "", fmt.Errorf("missing offline $ref file %q: %w", joinedRel, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, "", fmt.Errorf("%w: %q is not a regular file", ErrSchemaPathTraversal, joinedRel)
	}
	if info.Size() > c.opts.MaxSchemaBytes {
		return nil, "", configErrorf("referenced file %q exceeds size limit (%d > %d bytes)", joinedRel, info.Size(), c.opts.MaxSchemaBytes)
	}

	rawBytes, err := os.ReadFile(absPath)
	if err != nil {
		return nil, "", err
	}
	parsed, err := decodeStructuredValue(rawBytes)
	if err != nil {
		return nil, "", fmt.Errorf("parse referenced file %q: %w", joinedRel, err)
	}
	c.fileCache[joinedRel] = parsed
	return parsed, joinedRel, nil
}

// resolveSafeOfflineFile ensures relPath stays strictly inside baseDir with no
// symlinks along any path segment.
func resolveSafeOfflineFile(baseDir, relPath string) (string, error) {
	absBase, err := filepath.Abs(baseDir)
	if err != nil {
		return "", err
	}
	cleanRel := filepath.Clean(filepath.FromSlash(relPath))
	if cleanRel == ".." || strings.HasPrefix(cleanRel, ".."+string(filepath.Separator)) || filepath.IsAbs(cleanRel) {
		return "", fmt.Errorf("%w: %q", ErrSchemaPathTraversal, relPath)
	}
	parts := strings.Split(filepath.ToSlash(cleanRel), "/")
	cur := absBase
	for _, seg := range parts {
		if seg == "" || seg == "." {
			continue
		}
		if seg == ".." {
			return "", fmt.Errorf("%w: %q", ErrSchemaPathTraversal, relPath)
		}
		cur = filepath.Join(cur, seg)
		st, lerr := os.Lstat(cur)
		if lerr != nil {
			if os.IsNotExist(lerr) {
				continue
			}
			return "", lerr
		}
		if st.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w: symlink segment %q", ErrSchemaPathTraversal, seg)
		}
	}
	return cur, nil
}

func isRemoteURI(ref string) bool {
	lower := strings.ToLower(strings.TrimSpace(ref))
	for _, prefix := range []string{"http://", "https://", "ws://", "wss://", "ftp://", "file://"} {
		if strings.HasPrefix(lower, prefix) {
			return true
		}
	}
	return false
}

func evaluateJSONPointer(root any, pointer string) (any, error) {
	if root == nil {
		return nil, errors.New("nil root document")
	}
	ptr := strings.TrimPrefix(pointer, "#")
	if ptr == "" || ptr == "/" {
		return root, nil
	}
	if !strings.HasPrefix(ptr, "/") {
		return nil, fmt.Errorf("invalid JSON pointer %q (must start with '/')", pointer)
	}
	tokens := strings.Split(ptr[1:], "/")
	cur := root
	for _, rawTok := range tokens {
		tok := unescapeJSONPointerToken(rawTok)
		switch node := cur.(type) {
		case map[string]any:
			next, ok := node[tok]
			if !ok {
				return nil, fmt.Errorf("pointer segment %q not found", tok)
			}
			cur = next
		case []any:
			idx, err := strconv.Atoi(tok)
			if err != nil || idx < 0 || idx >= len(node) {
				return nil, fmt.Errorf("array index %q out of bounds", tok)
			}
			cur = node[idx]
		default:
			return nil, fmt.Errorf("cannot traverse into %T at segment %q", cur, tok)
		}
	}
	return cur, nil
}

func unescapeJSONPointerToken(tok string) string {
	if unesc, err := url.PathUnescape(tok); err == nil {
		tok = unesc
	}
	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")
	return tok
}

func escapeJSONPointerToken(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}

var simpleIdentRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func appendJSONPathKey(base, key string) string {
	if simpleIdentRe.MatchString(key) {
		return base + "." + key
	}
	escaped := strings.ReplaceAll(key, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `'`, `\'`)
	return fmt.Sprintf("%s['%s']", base, escaped)
}

func appendJSONPointerKey(base, key string) string {
	return base + "/" + escapeJSONPointerToken(key)
}

func extractAllowedTypes(raw any) []string {
	switch v := raw.(type) {
	case string:
		if strings.TrimSpace(v) != "" {
			return []string{strings.TrimSpace(v)}
		}
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				out = append(out, strings.TrimSpace(s))
			}
		}
		return out
	case []string:
		return slices.Clone(v)
	}
	return nil
}

func describeInstanceType(v any) string {
	switch n := v.(type) {
	case nil:
		return "null"
	case bool:
		return "boolean"
	case string:
		return "string"
	case json.Number:
		if isIntegralJSONNumber(n) {
			return "integer"
		}
		return "number"
	case int, int64, int32:
		return "integer"
	case float64:
		if !stdmath.IsNaN(n) && !stdmath.IsInf(n, 0) && stdmath.Trunc(n) == n {
			return "integer"
		}
		return "number"
	case []any:
		return "array"
	case map[string]any:
		return "object"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func matchesAnyJSONType(inst any, actualType string, allowed []string) bool {
	for _, exp := range allowed {
		if exp == actualType {
			return true
		}
		// In JSON Schema, every "integer" is also a valid "number"
		if exp == "number" && actualType == "integer" {
			return true
		}
		if exp == "integer" {
			if f, ok := toFloat64(inst); ok && !stdmath.IsNaN(f) && !stdmath.IsInf(f, 0) && stdmath.Trunc(f) == f {
				return true
			}
		}
	}
	return false
}

func isIntegralJSONNumber(n json.Number) bool {
	s := string(n)
	if !strings.ContainsAny(s, ".eE") {
		if _, err := strconv.ParseInt(s, 10, 64); err == nil {
			return true
		}
	}
	if f, err := n.Float64(); err == nil && !stdmath.IsNaN(f) && !stdmath.IsInf(f, 0) && stdmath.Trunc(f) == f {
		return true
	}
	return false
}

func toFloat64(v any) (float64, bool) {
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case float32:
		return float64(n), true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int64:
		return int(n), true
	case float64:
		if stdmath.Trunc(n) == n {
			return int(n), true
		}
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return int(i), true
		}
		if f, err := n.Float64(); err == nil && stdmath.Trunc(f) == f {
			return int(f), true
		}
	}
	return 0, false
}

func jsonValuesEqual(a, b any) bool {
	if fa, okA := toFloat64(a); okA {
		if fb, okB := toFloat64(b); okB {
			return fa == fb
		}
		return false
	}
	switch va := a.(type) {
	case nil:
		return b == nil
	case bool:
		vb, ok := b.(bool)
		return ok && va == vb
	case string:
		vb, ok := b.(string)
		return ok && va == vb
	case []any:
		vb, ok := b.([]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for i := range va {
			if !jsonValuesEqual(va[i], vb[i]) {
				return false
			}
		}
		return true
	case map[string]any:
		vb, ok := b.(map[string]any)
		if !ok || len(va) != len(vb) {
			return false
		}
		for k, valA := range va {
			valB, exists := vb[k]
			if !exists || !jsonValuesEqual(valA, valB) {
				return false
			}
		}
		return true
	default:
		return reflect.DeepEqual(a, b)
	}
}

func formatCompactJSONValue(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func decodeJSONInstancePreservingNumbers(data []byte) (any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty JSON payload")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	dec.UseNumber()
	var val any
	if err := dec.Decode(&val); err != nil {
		return nil, err
	}
	if dec.More() {
		return nil, errors.New("unexpected trailing tokens after JSON value")
	}
	return val, nil
}

func decodeStructuredValue(data []byte) (any, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty document")
	}
	if trimmed[0] == '{' || trimmed[0] == '[' || bytes.Equal(trimmed, []byte("true")) || bytes.Equal(trimmed, []byte("false")) {
		if val, err := decodeJSONInstancePreservingNumbers(trimmed); err == nil {
			return normalizeStructuredNode(val), nil
		}
	}
	var node yaml.Node
	dec := yaml.NewDecoder(bytes.NewReader(trimmed))
	if err := dec.Decode(&node); err != nil {
		return nil, err
	}
	var raw any
	if err := node.Decode(&raw); err != nil {
		return nil, err
	}
	return normalizeStructuredNode(raw), nil
}

func normalizeStructuredNode(v any) any {
	switch n := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			out[k] = normalizeStructuredNode(val)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(n))
		for k, val := range n {
			out[fmt.Sprintf("%v", k)] = normalizeStructuredNode(val)
		}
		return out
	case []any:
		out := make([]any, len(n))
		for i, elem := range n {
			out[i] = normalizeStructuredNode(elem)
		}
		return out
	default:
		return v
	}
}

func extractStringSlice(raw any) []string {
	switch v := raw.(type) {
	case []string:
		return slices.Clone(v)
	case []any:
		var out []string
		for _, item := range v {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

func sortedMapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sortStrings(keys)
	return keys
}

var (
	uuidFormatRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	dateFormatRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

func checkStringFormat(formatName, val string) bool {
	switch strings.ToLower(strings.TrimSpace(formatName)) {
	case "email":
		_, err := mail.ParseAddress(val)
		return err == nil
	case "uri", "url":
		u, err := url.Parse(val)
		return err == nil && u.Scheme != "" && u.Host != ""
	case "uuid":
		return uuidFormatRe.MatchString(val)
	case "date":
		if !dateFormatRe.MatchString(val) {
			return false
		}
		_, err := time.Parse("2006-01-02", val)
		return err == nil
	case "date-time":
		if _, err := time.Parse(time.RFC3339, val); err == nil {
			return true
		}
		_, err := time.Parse(time.RFC3339Nano, val)
		return err == nil
	case "ipv4":
		ip := net.ParseIP(val)
		return ip != nil && ip.To4() != nil
	case "ipv6":
		ip := net.ParseIP(val)
		return ip != nil && ip.To4() == nil && strings.Contains(val, ":")
	default:
		return true
	}
}
