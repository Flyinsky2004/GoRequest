package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vektah/gqlparser/v2/gqlerror"
	"github.com/vektah/gqlparser/v2/parser"
	"github.com/vektah/gqlparser/v2/validator"
)

// GraphQL SDL bounds. A schema is a user-selected file (local or project
// relative), so every dimension is bounded before parsing: bytes, tokens,
// types, per-type fields, arguments and enum values. Descriptions are
// truncated (never silently dropped) instead of rejecting a valid schema.
const (
	MaxGraphQLSchemaBytes            = 2 << 20
	MaxGraphQLSchemaTokens           = 200000
	MaxGraphQLSchemaTypes            = 5000
	MaxGraphQLSchemaFieldsPerType    = 2000
	MaxGraphQLSchemaArgsPerField     = 256
	MaxGraphQLSchemaEnumValues       = 2000
	MaxGraphQLSchemaDescriptionBytes = 4096
	MaxGraphQLIntrospectionBytes     = 8 << 20
)

// GraphQLSchemaExtensionKey is the request.Extensions key that stores the
// explicit local SDL reference. Nothing is fetched or loaded implicitly; the
// reference is only a pointer the user chose.
const GraphQLSchemaExtensionKey = "graphqlSchema"

const graphQLIntrospectionTypePrefix = "__"

// GraphQLSchemaRef is the persisted schema reference. Digest is the SHA-256 of
// the exact SDL bytes the user validated, so a changed file is visible instead
// of silently changing validation results.
type GraphQLSchemaRef struct {
	Path   string `json:"path" yaml:"path"`
	Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
}

// GraphQLDiagnostic is one parse or semantic validation error with a source
// location when gqlparser could provide one.
type GraphQLDiagnostic struct {
	Message string `json:"message"`
	Line    int    `json:"line,omitempty"`
	Column  int    `json:"column,omitempty"`
}

// String renders the diagnostic as "line:column: message".
func (d GraphQLDiagnostic) String() string {
	if d.Line > 0 {
		return fmt.Sprintf("line %d, column %d: %s", d.Line, d.Column, d.Message)
	}
	return d.Message
}

// GraphQLSchemaModel is the UI-independent Schema Explorer model plus the
// parsed schema used for offline document validation. Types are sorted by name
// and fields by name so rendering is stable across loads.
type GraphQLSchemaModel struct {
	SourcePath       string
	Digest           string
	Bytes            int
	QueryType        string
	MutationType     string
	SubscriptionType string
	Types            []GraphQLSchemaTypeInfo
	Truncated        bool
	Warnings         []string

	schema *ast.Schema
}

// GraphQLSchemaTypeInfo describes one type for the explorer.
type GraphQLSchemaTypeInfo struct {
	Name                 string
	Kind                 string
	Description          string
	DescriptionTruncated bool
	Builtin              bool
	Deprecated           bool
	DeprecationReason    string
	Interfaces           []string
	PossibleTypes        []string
	Fields               []GraphQLSchemaFieldInfo
	InputFields          []GraphQLSchemaInputValue
	EnumValues           []GraphQLSchemaEnumValue
	FieldsTruncated      bool
}

// GraphQLSchemaFieldInfo describes one output field.
type GraphQLSchemaFieldInfo struct {
	Name              string
	Description       string
	Type              string
	Deprecated        bool
	DeprecationReason string
	Args              []GraphQLSchemaInputValue
}

// GraphQLSchemaInputValue describes an argument or input-object field.
type GraphQLSchemaInputValue struct {
	Name              string
	Description       string
	Type              string
	DefaultValue      string
	Deprecated        bool
	DeprecationReason string
}

// GraphQLSchemaEnumValue describes one enum value.
type GraphQLSchemaEnumValue struct {
	Name              string
	Description       string
	Deprecated        bool
	DeprecationReason string
}

// LoadGraphQLSchemaFile reads an explicit absolute schema path with the SDL
// size bound enforced before the whole file is retained.
func LoadGraphQLSchemaFile(absPath string) (*GraphQLSchemaModel, error) {
	path := strings.TrimSpace(absPath)
	if path == "" {
		return nil, configErrorf("graphql schema path is empty")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, configErrorf("graphql schema %q: %v", path, err)
	}
	defer f.Close()

	limited := io.LimitReader(f, MaxGraphQLSchemaBytes+1)
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, configErrorf("graphql schema %q: %v", path, err)
	}
	if len(data) > MaxGraphQLSchemaBytes {
		return nil, configErrorf("graphql schema exceeds %d bytes", MaxGraphQLSchemaBytes)
	}
	model, err := ParseGraphQLSchemaSDL(path, string(data))
	if err != nil {
		return nil, err
	}
	return model, nil
}

// LoadGraphQLSchemaProjectFile loads a project-relative SDL through the same
// confined opener used for body uploads. The opener rejects traversal and
// symlink escapes; this function only adds the schema bounds.
func LoadGraphQLSchemaProjectFile(ctx context.Context, opener ProjectFileOpener, rel string) (*GraphQLSchemaModel, error) {
	if opener == nil {
		return nil, configErrorf("graphql schema %q requires a project file opener", rel)
	}
	opened, err := opener.OpenProjectFile(ctx, rel)
	if err != nil {
		return nil, err
	}
	if opened == nil || opened.Reader == nil {
		return nil, configErrorf("graphql schema %q could not be opened", rel)
	}
	defer opened.Reader.Close()
	if opened.Size > MaxGraphQLSchemaBytes {
		return nil, configErrorf("graphql schema exceeds %d bytes", MaxGraphQLSchemaBytes)
	}
	data, err := io.ReadAll(io.LimitReader(opened.Reader, MaxGraphQLSchemaBytes+1))
	if err != nil {
		return nil, configErrorf("graphql schema %q: %v", rel, err)
	}
	if len(data) > MaxGraphQLSchemaBytes {
		return nil, configErrorf("graphql schema exceeds %d bytes", MaxGraphQLSchemaBytes)
	}
	model, err := ParseGraphQLSchemaSDL(rel, string(data))
	if err != nil {
		return nil, err
	}
	return model, nil
}

// ParseGraphQLSchemaSDL parses and validates one SDL document offline. The
// GraphQL prelude (built-in scalars and directives) is merged in explicitly;
// no network or introspection is involved.
func ParseGraphQLSchemaSDL(name, sdl string) (*GraphQLSchemaModel, error) {
	if len(sdl) > MaxGraphQLSchemaBytes {
		return nil, configErrorf("graphql schema exceeds %d bytes", MaxGraphQLSchemaBytes)
	}
	if strings.TrimSpace(sdl) == "" {
		return nil, configErrorf("graphql schema is empty")
	}
	userDoc, err := parser.ParseSchemasWithLimit(MaxGraphQLSchemaTokens, &ast.Source{
		Name:  schemaSourceName(name),
		Input: sdl,
	})
	if err != nil {
		return nil, graphQLSchemaError("schema", err)
	}
	prelude, err := parser.ParseSchema(validator.Prelude)
	if err != nil {
		return nil, configErrorf("graphql prelude: %v", err)
	}
	prelude.Merge(userDoc)
	schema, err := validator.ValidateSchemaDocument(prelude)
	if err != nil {
		return nil, graphQLSchemaError("schema", err)
	}

	model := &GraphQLSchemaModel{
		SourcePath: name,
		Digest:     GraphQLSchemaDigest(sdl),
		Bytes:      len(sdl),
	}
	if schema.Query != nil {
		model.QueryType = schema.Query.Name
	}
	if schema.Mutation != nil {
		model.MutationType = schema.Mutation.Name
	}
	if schema.Subscription != nil {
		model.SubscriptionType = schema.Subscription.Name
	}
	model.buildTypeModels(schema)
	model.schema = schema
	return model, nil
}

// GraphQLSchemaDigest returns the lowercase SHA-256 hex digest of exact SDL
// bytes, used to detect an edited schema between validation and execution.
func GraphQLSchemaDigest(sdl string) string {
	sum := sha256.Sum256([]byte(sdl))
	return hex.EncodeToString(sum[:])
}

func schemaSourceName(name string) string {
	if strings.TrimSpace(name) == "" {
		return "schema.graphql"
	}
	return name
}

// graphQLSchemaError converts gqlparser errors into *ConfigError, preserving
// the first source location so the UI can point at the schema line.
func graphQLSchemaError(what string, err error) error {
	if err == nil {
		return nil
	}
	var ge *gqlerror.Error
	if errors.As(err, &ge) {
		if len(ge.Locations) > 0 {
			loc := ge.Locations[0]
			return configErrorf("%s error at line %d, column %d: %s", what, loc.Line, loc.Column, ge.Message)
		}
		return configErrorf("%s error: %s", what, ge.Message)
	}
	return configErrorf("%s error: %v", what, err)
}

// buildTypeModels fills the explorer model from the validated schema.
func (m *GraphQLSchemaModel) buildTypeModels(schema *ast.Schema) {
	names := make([]string, 0, len(schema.Types))
	for name := range schema.Types {
		if strings.HasPrefix(name, graphQLIntrospectionTypePrefix) {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) > MaxGraphQLSchemaTypes {
		names = names[:MaxGraphQLSchemaTypes]
		m.Truncated = true
		m.Warnings = append(m.Warnings, fmt.Sprintf("schema has more than %d types; the explorer is truncated", MaxGraphQLSchemaTypes))
	}
	for _, name := range names {
		def := schema.Types[name]
		if def == nil {
			continue
		}
		m.Types = append(m.Types, buildTypeInfo(def))
	}
}

func buildTypeInfo(def *ast.Definition) GraphQLSchemaTypeInfo {
	info := GraphQLSchemaTypeInfo{
		Name:       def.Name,
		Kind:       graphQLKindName(def.Kind),
		Builtin:    def.BuiltIn,
		Interfaces: sortedCopy(def.Interfaces),
		PossibleTypes: func() []string {
			if def.Kind != ast.Union {
				return nil
			}
			return sortedCopy(def.Types)
		}(),
	}
	info.Description = truncateGraphQLDescription(def.Description, &info.DescriptionTruncated)
	info.Deprecated, info.DeprecationReason = graphQLDeprecation(def.Directives)

	if def.Kind == ast.Object || def.Kind == ast.Interface {
		fields := make(ast.FieldList, 0, len(def.Fields))
		for _, f := range def.Fields {
			if strings.HasPrefix(f.Name, graphQLIntrospectionTypePrefix) {
				continue // __schema/__type are prelude introspection fields, not user schema.
			}
			fields = append(fields, f)
		}
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		if len(fields) > MaxGraphQLSchemaFieldsPerType {
			fields = fields[:MaxGraphQLSchemaFieldsPerType]
			info.FieldsTruncated = true
		}
		for _, f := range fields {
			info.Fields = append(info.Fields, buildFieldInfo(f))
		}
	}
	if def.Kind == ast.InputObject {
		fields := append(ast.FieldList(nil), def.Fields...)
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		if len(fields) > MaxGraphQLSchemaFieldsPerType {
			fields = fields[:MaxGraphQLSchemaFieldsPerType]
			info.FieldsTruncated = true
		}
		for _, f := range fields {
			info.InputFields = append(info.InputFields, buildInputValue(f.Description, f.Name, f.Type, f.DefaultValue, f.Directives))
		}
	}
	if def.Kind == ast.Enum {
		values := append(ast.EnumValueList(nil), def.EnumValues...)
		sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
		if len(values) > MaxGraphQLSchemaEnumValues {
			values = values[:MaxGraphQLSchemaEnumValues]
			info.FieldsTruncated = true
		}
		for _, v := range values {
			deprecated, reason := graphQLDeprecation(v.Directives)
			var truncated bool
			info.EnumValues = append(info.EnumValues, GraphQLSchemaEnumValue{
				Name:              v.Name,
				Description:       truncateGraphQLDescription(v.Description, &truncated),
				Deprecated:        deprecated,
				DeprecationReason: reason,
			})
		}
	}
	return info
}

func buildFieldInfo(f *ast.FieldDefinition) GraphQLSchemaFieldInfo {
	info := GraphQLSchemaFieldInfo{
		Name:        f.Name,
		Description: truncateGraphQLDescription(f.Description, new(bool)),
		Type:        graphQLTypeString(f.Type),
	}
	info.Deprecated, info.DeprecationReason = graphQLDeprecation(f.Directives)
	args := append(ast.ArgumentDefinitionList(nil), f.Arguments...)
	sort.Slice(args, func(i, j int) bool { return args[i].Name < args[j].Name })
	if len(args) > MaxGraphQLSchemaArgsPerField {
		args = args[:MaxGraphQLSchemaArgsPerField]
	}
	for _, a := range args {
		info.Args = append(info.Args, buildInputValue(a.Description, a.Name, a.Type, a.DefaultValue, a.Directives))
	}
	return info
}

func buildInputValue(desc, name string, typ *ast.Type, def *ast.Value, directives ast.DirectiveList) GraphQLSchemaInputValue {
	var truncated bool
	out := GraphQLSchemaInputValue{
		Name:        name,
		Description: truncateGraphQLDescription(desc, &truncated),
		Type:        graphQLTypeString(typ),
	}
	if def != nil {
		out.DefaultValue = def.String()
	}
	out.Deprecated, out.DeprecationReason = graphQLDeprecation(directives)
	return out
}

// graphQLDeprecation extracts @deprecated and its human reason.
func graphQLDeprecation(directives ast.DirectiveList) (bool, string) {
	d := directives.ForName("deprecated")
	if d == nil {
		return false, ""
	}
	reason := "No longer supported"
	if arg := d.Arguments.ForName("reason"); arg != nil && arg.Value != nil && strings.TrimSpace(arg.Value.Raw) != "" {
		reason = arg.Value.Raw
	}
	return true, reason
}

func graphQLKindName(kind ast.DefinitionKind) string {
	switch kind {
	case ast.Scalar:
		return "scalar"
	case ast.Object:
		return "object"
	case ast.Interface:
		return "interface"
	case ast.Union:
		return "union"
	case ast.Enum:
		return "enum"
	case ast.InputObject:
		return "input"
	default:
		return strings.ToLower(string(kind))
	}
}

func graphQLTypeString(t *ast.Type) string {
	if t == nil {
		return ""
	}
	return t.String()
}

func sortedCopy(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	sort.Strings(out)
	return out
}

// truncateGraphQLDescription bounds a description on a rune boundary and
// reports whether it was shortened.
func truncateGraphQLDescription(s string, truncated *bool) string {
	if len(s) <= MaxGraphQLSchemaDescriptionBytes {
		return s
	}
	if truncated != nil {
		*truncated = true
	}
	cut := MaxGraphQLSchemaDescriptionBytes
	for cut > 0 && !isUTF8RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + "…"
}

func isUTF8RuneStart(b byte) bool { return b&0xC0 != 0x80 }

// ValidateDocument validates a document against this schema fully offline. It
// always returns the parsed operation inventory; diagnostics are ordered as
// gqlparser reported them. An operationName selection failure is returned as
// an error even when semantic validation also produced diagnostics.
func (m *GraphQLSchemaModel) ValidateDocument(document, operationName string) (*GraphQLAnalysis, []GraphQLDiagnostic, error) {
	doc, analysis, err := parseGraphQLDocument(document)
	if err != nil {
		return analysis, diagnosticsFromGraphQLError(err), err
	}
	var diagnostics []GraphQLDiagnostic
	if m != nil && m.schema != nil {
		diagnostics = graphQLDiagnostics(validator.Validate(m.schema, doc))
	}
	selErr := selectGraphQLOperation(analysis, operationName)
	if selErr != nil {
		return analysis, diagnostics, selErr
	}
	if len(diagnostics) > 0 {
		return analysis, diagnostics, configErrorf("graphql document does not match the schema: %s", diagnostics[0].String())
	}
	return analysis, nil, nil
}

func graphQLDiagnostics(errs gqlerror.List) []GraphQLDiagnostic {
	out := make([]GraphQLDiagnostic, 0, len(errs))
	for _, err := range errs {
		d := GraphQLDiagnostic{Message: err.Message}
		if len(err.Locations) > 0 {
			d.Line = err.Locations[0].Line
			d.Column = err.Locations[0].Column
		}
		out = append(out, d)
	}
	return out
}

// diagnosticsFromGraphQLError converts a *ConfigError produced by the parser
// back into a one-line diagnostic list for UI display.
func diagnosticsFromGraphQLError(err error) []GraphQLDiagnostic {
	if err == nil {
		return nil
	}
	return []GraphQLDiagnostic{{Message: err.Error()}}
}

// GraphQLSchemaRefFromRequest reads the explicit schema reference from the
// request's extension bag. It never resolves or loads the path.
func GraphQLSchemaRefFromRequest(req *Request) (GraphQLSchemaRef, bool) {
	if req == nil || req.Extensions == nil {
		return GraphQLSchemaRef{}, false
	}
	raw, ok := req.Extensions[GraphQLSchemaExtensionKey]
	if !ok || raw == nil {
		return GraphQLSchemaRef{}, false
	}
	switch v := raw.(type) {
	case GraphQLSchemaRef:
		return normalizeGraphQLSchemaRef(v), true
	case map[string]any:
		ref := GraphQLSchemaRef{Path: stringValue(v["path"]), Digest: stringValue(v["digest"])}
		ref = normalizeGraphQLSchemaRef(ref)
		return ref, ref.Path != ""
	case map[any]any:
		ref := GraphQLSchemaRef{Path: stringValue(v["path"]), Digest: stringValue(v["digest"])}
		ref = normalizeGraphQLSchemaRef(ref)
		return ref, ref.Path != ""
	case string:
		ref := normalizeGraphQLSchemaRef(GraphQLSchemaRef{Path: v})
		return ref, ref.Path != ""
	default:
		return GraphQLSchemaRef{}, false
	}
}

// SetGraphQLSchemaRef stores (or clears, for an empty path) the schema
// reference in the request extension bag.
func SetGraphQLSchemaRef(req *Request, ref GraphQLSchemaRef) {
	if req == nil {
		return
	}
	ref = normalizeGraphQLSchemaRef(ref)
	if req.Extensions == nil {
		req.Extensions = map[string]any{}
	}
	if ref.Path == "" {
		delete(req.Extensions, GraphQLSchemaExtensionKey)
		return
	}
	req.Extensions[GraphQLSchemaExtensionKey] = map[string]any{
		"path":   ref.Path,
		"digest": ref.Digest,
	}
}

func normalizeGraphQLSchemaRef(ref GraphQLSchemaRef) GraphQLSchemaRef {
	ref.Path = strings.TrimSpace(ref.Path)
	ref.Digest = strings.TrimSpace(ref.Digest)
	return ref
}

func stringValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}
