package sdkgen

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// TypeKind classifies a value type inside the intermediate model.
type TypeKind int

const (
	// TypeAny is a schema with no constraints ({}), rendered as raw JSON.
	TypeAny TypeKind = iota
	TypeString
	TypeInt
	TypeNumber
	TypeBool
	TypeArray
	TypeMap
	TypeModel
)

// Type is a reference into the intermediate type model. Nullable is a separate
// dimension from required: it records that JSON null is part of the value space,
// not that a property may be absent.
type Type struct {
	Kind     TypeKind
	Nullable bool
	// Ref names the entry in the model registry when Kind == TypeModel.
	Ref string
	// Items is the element type of an array.
	Items *Type
	// MapValue is the value type of a free-form object or typed
	// additionalProperties map.
	MapValue *Type
}

func (t *Type) String() string {
	if t == nil {
		return "<nil>"
	}
	s := ""
	switch t.Kind {
	case TypeAny:
		s = "any"
	case TypeString:
		s = "string"
	case TypeInt:
		s = "integer"
	case TypeNumber:
		s = "number"
	case TypeBool:
		s = "boolean"
	case TypeArray:
		s = "[]" + t.Items.String()
	case TypeMap:
		s = "map[string]" + t.MapValue.String()
	case TypeModel:
		s = t.Ref
	}
	if t.Nullable {
		s += "|null"
	}
	return s
}

// ModelKind classifies a named generated type.
type ModelKind int

const (
	ModelObject ModelKind = iota
	ModelEnum
	ModelUnion
	ModelAlias
)

func (k ModelKind) String() string {
	switch k {
	case ModelObject:
		return "object"
	case ModelEnum:
		return "enum"
	case ModelUnion:
		return "union"
	case ModelAlias:
		return "alias"
	default:
		return "unknown"
	}
}

// Property is one object member.
type Property struct {
	JSONName    string
	Name        string // exported Go field / TypeScript property identifier
	Description string
	Type        *Type
	Required    bool
}

// EnumValue is one enum member with its JSON literal preserved.
type EnumValue struct {
	Name  string
	Value string // JSON literal text (quoted string or number)
}

// UnionVariant is one discriminator-mapped variant of a union.
type UnionVariant struct {
	// Value is the discriminator value that selects this variant.
	Value string
	// Model names the object model for this variant.
	Model string
}

// Model is one named generated type.
type Model struct {
	Name        string
	Kind        ModelKind
	Description string
	// Origin identifies the contract location for deterministic diagnostics.
	Origin string
	// Request marks models used by a request body, which get a Validate method.
	Request bool

	// Object models.
	Properties []Property
	// Additional is nil when additionalProperties is false; TypeAny when it is
	// true or an unconstrained schema; a typed value otherwise.
	Additional *Type

	// Enum models.
	EnumKind   TypeKind
	EnumValues []EnumValue

	// Union models.
	Discriminator string
	Variants      []UnionVariant

	// Alias models.
	Alias *Type
}

// ModelSummary is the public, UI-facing description of a generated type.
type ModelSummary struct {
	Name   string
	Kind   string
	Origin string
}

// schemaNode is the JSON Schema subset consumed by the type compiler.
type schemaNode struct {
	Ref                  string                     `json:"$ref"`
	Type                 any                        `json:"type"`
	Format               string                     `json:"format"`
	Title                string                     `json:"title"`
	Description          string                     `json:"description"`
	Properties           map[string]json.RawMessage `json:"properties"`
	Required             []string                   `json:"required"`
	Items                json.RawMessage            `json:"items"`
	Enum                 []any                      `json:"enum"`
	OneOf                []json.RawMessage          `json:"oneOf"`
	AnyOf                []json.RawMessage          `json:"anyOf"`
	AllOf                []json.RawMessage          `json:"allOf"`
	Nullable             bool                       `json:"nullable"`
	AdditionalProperties json.RawMessage            `json:"additionalProperties"`
	Discriminator        *rawDiscriminator          `json:"discriminator"`
}

type rawDiscriminator struct {
	PropertyName string            `json:"propertyName"`
	Mapping      map[string]string `json:"mapping"`
}

// typeCompiler builds the named model registry for one contract.
type typeCompiler struct {
	doc      *specDoc
	models   map[string]*Model
	order    []string
	byOrigin map[string]string
	names    *nameAllocator
	inFlight map[string]bool
	diags    []Diagnostic
}

// compilerSnapshot restores the compiler when an operation fails and the
// caller has opted into generating only the supported subset.
type compilerSnapshot struct {
	order    int
	models   map[string]*Model
	byOrigin map[string]string
	inFlight map[string]bool
	names    *nameAllocator
	diags    int
}

func newTypeCompiler(doc *specDoc, names *nameAllocator) *typeCompiler {
	return &typeCompiler{
		doc:      doc,
		models:   map[string]*Model{},
		byOrigin: map[string]string{},
		names:    names,
		inFlight: map[string]bool{},
	}
}

func (c *typeCompiler) mark() compilerSnapshot {
	models := make(map[string]*Model, len(c.models))
	for k, v := range c.models {
		models[k] = v
	}
	origins := make(map[string]string, len(c.byOrigin))
	for k, v := range c.byOrigin {
		origins[k] = v
	}
	flight := make(map[string]bool, len(c.inFlight))
	for k, v := range c.inFlight {
		flight[k] = v
	}
	return compilerSnapshot{
		order:    len(c.order),
		models:   models,
		byOrigin: origins,
		inFlight: flight,
		names:    c.names.clone(),
		diags:    len(c.diags),
	}
}

func (c *typeCompiler) rollback(s compilerSnapshot) {
	c.order = c.order[:s.order]
	c.models = s.models
	c.byOrigin = s.byOrigin
	c.inFlight = s.inFlight
	c.names = s.names
	c.diags = c.diags[:s.diags]
}

// sortedModels returns models ordered by name for deterministic output.
func (c *typeCompiler) sortedModels() []*Model {
	names := make([]string, 0, len(c.models))
	for name := range c.models {
		names = append(names, name)
	}
	sort.Strings(names)
	out := make([]*Model, 0, len(names))
	for _, name := range names {
		out = append(out, c.models[name])
	}
	return out
}

func (c *typeCompiler) summaries() []ModelSummary {
	models := c.sortedModels()
	out := make([]ModelSummary, 0, len(models))
	for _, m := range models {
		out = append(out, ModelSummary{Name: m.Name, Kind: m.Kind.String(), Origin: m.Origin})
	}
	return out
}

func (c *typeCompiler) model(name string) *Model { return c.models[name] }

func (c *typeCompiler) modelKind(name string) ModelKind {
	if m := c.models[name]; m != nil {
		return m.Kind
	}
	return ModelAlias
}

// modelKinds snapshots every model name to its kind for the backends.
func (c *typeCompiler) modelKinds() map[string]ModelKind {
	out := make(map[string]ModelKind, len(c.models))
	for name, m := range c.models {
		out[name] = m.Kind
	}
	return out
}

func (c *typeCompiler) addDiagnostic(severity, code, location, message string) {
	c.diags = append(c.diags, Diagnostic{Severity: severity, Code: code, Location: location, Message: message})
}

const maxSchemaDepth = 64

// compileRoot compiles a schema that starts a named position (request body or
// response body) and guarantees a named model for it.
func (c *typeCompiler) compileRoot(raw json.RawMessage, baseName, origin string, request bool) (*Type, error) {
	t, err := c.compileSchema(raw, baseName, false, origin, request, 0)
	if err != nil {
		return nil, err
	}
	if t.Kind == TypeModel {
		if request {
			if m := c.models[t.Ref]; m != nil {
				m.Request = true
			}
		}
		return t, nil
	}
	// Wrap primitive/array/map/any roots in a named alias so generated request
	// and response types are addressable in both languages.
	name := c.names.Allocate(baseName)
	c.register(&Model{Name: name, Kind: ModelAlias, Alias: t, Origin: origin, Request: request})
	return &Type{Kind: TypeModel, Ref: name, Nullable: t.Nullable}, nil
}

// compileSchema maps one schema to the intermediate type model. When exactName
// is true the caller has already reserved name and the named model must use it.
func (c *typeCompiler) compileSchema(raw json.RawMessage, name string, exactName bool, origin string, request bool, depth int) (*Type, error) {
	if depth > maxSchemaDepth {
		return nil, fmt.Errorf("schema nesting exceeds %d levels", maxSchemaDepth)
	}
	if len(raw) == 0 {
		return &Type{Kind: TypeAny}, nil
	}
	var node schemaNode
	if err := json.Unmarshal(raw, &node); err != nil {
		return nil, fmt.Errorf("schema is not an object")
	}

	if ref := strings.TrimSpace(node.Ref); ref != "" {
		return c.compileRef(ref, request, depth)
	}

	if node.Nullable {
		return nil, fmt.Errorf("`nullable` is an OpenAPI 3.0 construct and is not valid in OpenAPI 3.1/3.2; use type [..., \"null\"]")
	}

	types, err := schemaTypesOf(&node)
	if err != nil {
		return nil, err
	}
	nullable := false
	nonNull := make([]string, 0, len(types))
	for _, t := range types {
		if t == "null" {
			nullable = true
			continue
		}
		nonNull = append(nonNull, t)
	}
	if len(nonNull) == 0 && nullable {
		// A schema that only admits null carries no value type.
		return &Type{Kind: TypeAny, Nullable: true}, nil
	}
	if len(nonNull) > 1 {
		return nil, fmt.Errorf("multi-type schema %v is not supported; use oneOf/anyOf with a discriminator", nonNull)
	}
	typ := ""
	if len(nonNull) == 1 {
		typ = nonNull[0]
	}

	// Composition.
	if len(node.OneOf) > 0 || len(node.AnyOf) > 0 {
		return c.compileUnion(&node, name, exactName, origin, request, depth)
	}
	if len(node.AllOf) > 0 {
		return c.compileAllOf(&node, name, exactName, origin, request, depth)
	}
	if len(node.Enum) > 0 {
		return c.compileEnum(&node, typ, nullable, name, exactName, origin, request)
	}

	if typ == "" {
		// Unconstrained, or inferred from structure.
		switch {
		case len(node.Properties) > 0 || len(node.AdditionalProperties) > 0:
			typ = "object"
		case len(node.Items) > 0:
			typ = "array"
		default:
			return &Type{Kind: TypeAny, Nullable: nullable}, nil
		}
	}

	switch typ {
	case "string":
		return &Type{Kind: TypeString, Nullable: nullable}, nil
	case "integer":
		return &Type{Kind: TypeInt, Nullable: nullable}, nil
	case "number":
		return &Type{Kind: TypeNumber, Nullable: nullable}, nil
	case "boolean":
		return &Type{Kind: TypeBool, Nullable: nullable}, nil
	case "array":
		if len(node.Items) == 0 {
			return nil, fmt.Errorf("array schema has no items")
		}
		item, err := c.compileSchema(node.Items, name+"Item", false, origin+".items", request, depth+1)
		if err != nil {
			return nil, err
		}
		return &Type{Kind: TypeArray, Items: item, Nullable: nullable}, nil
	case "object":
		return c.compileObject(&node, name, exactName, origin, request, depth)
	default:
		return nil, fmt.Errorf("schema type %q is not supported", typ)
	}
}

// compileRef resolves a local component schema reference. Cycles are allowed:
// an in-flight reference resolves to the named placeholder instead of recursing.
func (c *typeCompiler) compileRef(ref string, request bool, depth int) (*Type, error) {
	componentName, err := componentSchemaName(ref)
	if err != nil {
		return nil, err
	}
	origin := "#/components/schemas/" + componentName
	if name, ok := c.byOrigin[origin]; ok {
		return &Type{Kind: TypeModel, Ref: name}, nil
	}
	raw, ok := c.doc.Components.Schemas[componentName]
	if !ok {
		return nil, fmt.Errorf("reference %q does not resolve to a component schema", ref)
	}
	name := c.names.Allocate(exportedIdentifier(componentName))
	c.byOrigin[origin] = name
	c.inFlight[origin] = true
	c.register(&Model{Name: name, Kind: ModelObject, Origin: origin, Request: request})
	t, err := c.compileSchema(raw, name, true, origin, request, depth+1)
	delete(c.inFlight, origin)
	if err != nil {
		return nil, fmt.Errorf("schema %q: %w", componentName, err)
	}
	if t.Kind != TypeModel {
		// A component that is an alias or primitive becomes a named alias.
		alias := c.models[name]
		alias.Kind = ModelAlias
		alias.Alias = t
		return &Type{Kind: TypeModel, Ref: name, Nullable: t.Nullable}, nil
	}
	if t.Ref != name {
		// The component schema was itself a ref: collapse to the target model.
		delete(c.models, name)
		c.byOrigin[origin] = t.Ref
		return t, nil
	}
	if request {
		c.models[name].Request = true
	}
	return &Type{Kind: TypeModel, Ref: name}, nil
}

func componentSchemaName(ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	const prefix = "#/components/schemas/"
	if !strings.HasPrefix(ref, prefix) {
		return "", fmt.Errorf("reference %q is not a local component schema reference", ref)
	}
	name := strings.TrimPrefix(ref, prefix)
	if name == "" || strings.ContainsAny(name, "/\\") || strings.Contains(name, "..") {
		return "", fmt.Errorf("reference %q has an invalid component name", ref)
	}
	return name, nil
}

// schemaTypesOf normalizes the `type` keyword and infers enum types.
func schemaTypesOf(node *schemaNode) ([]string, error) {
	raw := schemaTypes(node.Type)
	if len(raw) == 0 {
		if len(node.Enum) > 0 {
			inferred := inferEnumTypes(node.Enum)
			if inferred == nil {
				return nil, fmt.Errorf("enum values mix incompatible JSON types")
			}
			return inferred, nil
		}
		return nil, nil
	}
	out := make([]string, 0, len(raw))
	for _, t := range raw {
		out = append(out, strings.ToLower(strings.TrimSpace(t)))
	}
	return out, nil
}

func (c *typeCompiler) compileObject(node *schemaNode, name string, exactName bool, origin string, request bool, depth int) (*Type, error) {
	if exactName {
		// The name is already reserved by compileRef; reuse the placeholder.
		if m := c.models[name]; m == nil {
			c.register(&Model{Name: name, Kind: ModelObject, Origin: origin, Request: request})
		}
	} else {
		name = c.names.Allocate(name)
	}
	props, err := c.compileProperties(node, name, origin, request, depth, false)
	if err != nil {
		return nil, err
	}
	additional, err := c.compileAdditional(node.AdditionalProperties, name+"Additional", origin, request, depth)
	if err != nil {
		return nil, err
	}
	// An object with no declared members and a value schema is represented as a
	// map instead of an empty wrapper struct.
	if len(props) == 0 && additional != nil {
		mapType := &Type{Kind: TypeMap, MapValue: additional}
		if exactName {
			m := c.models[name]
			m.Kind = ModelAlias
			m.Alias = mapType
			m.Properties = nil
			m.Additional = nil
			return &Type{Kind: TypeModel, Ref: name}, nil
		}
		return mapType, nil
	}
	if !exactName {
		c.register(&Model{Name: name, Kind: ModelObject, Origin: origin, Request: request, Description: node.Description})
	}
	m := c.models[name]
	if m.Description == "" {
		m.Description = node.Description
	}
	m.Properties = props
	m.Additional = additional
	return &Type{Kind: TypeModel, Ref: name}, nil
}

// compileProperties compiles an object's declared properties. When relaxed is
// true (allOf merge), required names outside properties are not rejected here.
func (c *typeCompiler) compileProperties(node *schemaNode, modelName, origin string, request bool, depth int, relaxed bool) ([]Property, error) {
	required := map[string]bool{}
	for _, r := range node.Required {
		required[r] = true
	}
	names := make([]string, 0, len(node.Properties))
	for pn := range node.Properties {
		names = append(names, pn)
	}
	sort.Strings(names)
	props := make([]Property, 0, len(names))
	for _, pn := range names {
		propType, err := c.compileSchema(node.Properties[pn], modelName+exportedIdentifier(pn), false, origin+".properties."+pn, request, depth+1)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", pn, err)
		}
		props = append(props, Property{
			JSONName:    pn,
			Type:        propType,
			Required:    required[pn],
			Description: propertyDescription(node.Properties[pn]),
		})
	}
	if !relaxed {
		for r := range required {
			if _, ok := node.Properties[r]; !ok {
				return nil, fmt.Errorf("required property %q is not defined in properties", r)
			}
		}
	}
	assignPropertyFieldNames(props)
	return props, nil
}

func assignPropertyFieldNames(props []Property) {
	fields := assignFieldNames(propertyJSONNames(props))
	for i := range props {
		props[i].Name = fields[i]
	}
}

func propertyJSONNames(props []Property) []string {
	out := make([]string, len(props))
	for i, p := range props {
		out[i] = p.JSONName
	}
	return out
}

func propertyDescription(raw json.RawMessage) string {
	var node struct {
		Description string `json:"description"`
	}
	if err := json.Unmarshal(raw, &node); err != nil {
		return ""
	}
	return strings.TrimSpace(node.Description)
}

// compileAdditional interprets the additionalProperties keyword. Absent means
// true per JSON Schema 2020-12, so unknown members are preserved.
func (c *typeCompiler) compileAdditional(raw json.RawMessage, baseName, origin string, request bool, depth int) (*Type, error) {
	trimmed := strings.TrimSpace(string(raw))
	switch trimmed {
	case "":
		return &Type{Kind: TypeAny}, nil
	case "false":
		return nil, nil
	case "true", "{}":
		return &Type{Kind: TypeAny}, nil
	}
	var probe any
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, fmt.Errorf("additionalProperties is not a schema")
	}
	if _, ok := probe.(map[string]any); !ok {
		return nil, fmt.Errorf("additionalProperties must be a boolean or schema")
	}
	t, err := c.compileSchema(raw, baseName, false, origin+".additionalProperties", request, depth+1)
	if err != nil {
		return nil, fmt.Errorf("additionalProperties: %w", err)
	}
	return t, nil
}

func (c *typeCompiler) compileEnum(node *schemaNode, typ string, nullable bool, name string, exactName bool, origin string, request bool) (*Type, error) {
	kind := TypeString
	switch typ {
	case "", "string":
		kind = TypeString
	case "integer", "number":
		kind = TypeInt
	default:
		return nil, fmt.Errorf("enum of type %q is not supported", typ)
	}
	values := make([]any, 0, len(node.Enum))
	sawNull := false
	for _, v := range node.Enum {
		if v == nil {
			sawNull = true
			continue
		}
		switch kind {
		case TypeString:
			if _, ok := v.(string); !ok {
				return nil, fmt.Errorf("enum mixes string and non-string values")
			}
		case TypeInt:
			switch v.(type) {
			case float64, json.Number:
			default:
				return nil, fmt.Errorf("enum mixes numeric and non-numeric values")
			}
		}
		values = append(values, v)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("enum has no non-null values")
	}
	if !exactName {
		name = c.names.Allocate(name)
	}
	m := &Model{Name: name, Kind: ModelEnum, EnumKind: kind, Origin: origin, Request: request, Description: node.Description}
	used := map[string]bool{}
	for _, v := range values {
		ev := EnumValue{}
		switch kind {
		case TypeString:
			s := v.(string)
			ev.Value = strconv.Quote(s)
			ev.Name = exportEnumName(s)
		case TypeInt:
			switch n := v.(type) {
			case float64:
				ev.Value = strconv.FormatInt(int64(n), 10)
			case json.Number:
				ev.Value = n.String()
			}
			ev.Name = "Value" + strings.ReplaceAll(ev.Value, "-", "Neg")
		}
		base := ev.Name
		if base == "" {
			base = "Value"
		}
		candidate := base
		for n := 2; used[strings.ToLower(candidate)]; n++ {
			candidate = base + strconv.Itoa(n)
		}
		used[strings.ToLower(candidate)] = true
		ev.Name = candidate
		m.EnumValues = append(m.EnumValues, ev)
	}
	c.register(m)
	return &Type{Kind: TypeModel, Ref: name, Nullable: nullable || sawNull}, nil
}

func exportEnumName(v string) string {
	name := exportedIdentifier(v)
	if name == "" || name == "Op" {
		return "Value"
	}
	return name
}

func (c *typeCompiler) compileAllOf(node *schemaNode, name string, exactName bool, origin string, request bool, depth int) (*Type, error) {
	if !exactName {
		name = c.names.Allocate(name)
	}
	m := c.models[name]
	if m == nil {
		m = &Model{Name: name, Kind: ModelObject, Origin: origin, Request: request, Description: node.Description}
		c.register(m)
	}
	if m.Description == "" {
		m.Description = node.Description
	}

	merged := map[string]Property{}
	required := map[string]bool{}
	var additional *Type
	for i, member := range node.AllOf {
		mt, err := c.compileSchema(member, name+"Part"+strconv.Itoa(i+1), false, fmt.Sprintf("%s.allOf[%d]", origin, i), request, depth+1)
		if err != nil {
			return nil, fmt.Errorf("allOf[%d]: %w", i, err)
		}
		if mt.Kind != TypeModel {
			return nil, fmt.Errorf("allOf[%d] is not an object model", i)
		}
		if mt.Ref == name {
			return nil, fmt.Errorf("allOf contains a recursive reference to %q", name)
		}
		src := c.models[mt.Ref]
		if src == nil || src.Kind != ModelObject {
			return nil, fmt.Errorf("allOf[%d] is not an object model", i)
		}
		for _, p := range src.Properties {
			if prev, ok := merged[p.JSONName]; ok {
				if !typesEqual(prev.Type, p.Type) {
					return nil, fmt.Errorf("allOf property %q has conflicting types", p.JSONName)
				}
				prev.Required = prev.Required || p.Required
				merged[p.JSONName] = prev
				continue
			}
			merged[p.JSONName] = p
		}
		for _, p := range src.Properties {
			if p.Required {
				required[p.JSONName] = true
			}
		}
		if src.Additional != nil {
			if additional != nil && !typesEqual(additional, src.Additional) {
				return nil, fmt.Errorf("allOf members declare conflicting additionalProperties")
			}
			additional = src.Additional
		}
	}
	// Own properties (allOf plus siblings).
	for _, p := range node.Required {
		required[p] = true
	}
	ownNames := make([]string, 0, len(node.Properties))
	for pn := range node.Properties {
		ownNames = append(ownNames, pn)
	}
	sort.Strings(ownNames)
	for _, pn := range ownNames {
		propType, err := c.compileSchema(node.Properties[pn], name+exportedIdentifier(pn), false, origin+".properties."+pn, request, depth+1)
		if err != nil {
			return nil, fmt.Errorf("property %q: %w", pn, err)
		}
		prop := Property{
			JSONName:    pn,
			Type:        propType,
			Required:    required[pn],
			Description: propertyDescription(node.Properties[pn]),
		}
		if prev, ok := merged[pn]; ok {
			if !typesEqual(prev.Type, prop.Type) {
				return nil, fmt.Errorf("allOf property %q has conflicting types", pn)
			}
			prev.Required = prev.Required || prop.Required
			merged[pn] = prev
			continue
		}
		merged[pn] = prop
	}
	for r := range required {
		if _, ok := merged[r]; !ok {
			return nil, fmt.Errorf("required property %q is not defined by the merged allOf object", r)
		}
	}
	ownAdditional, err := c.compileAdditional(node.AdditionalProperties, name+"Additional", origin, request, depth)
	if err != nil {
		return nil, err
	}
	if string(node.AdditionalProperties) != "" {
		if additional != nil && !typesEqual(additional, ownAdditional) {
			return nil, fmt.Errorf("schema and allOf members declare conflicting additionalProperties")
		}
		additional = ownAdditional
	}
	keys := make([]string, 0, len(merged))
	for k := range merged {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	props := make([]Property, 0, len(keys))
	for _, k := range keys {
		p := merged[k]
		p.Required = required[k]
		props = append(props, p)
	}
	assignPropertyFieldNames(props)
	m.Properties = props
	m.Additional = additional
	return &Type{Kind: TypeModel, Ref: name}, nil
}

func (c *typeCompiler) compileUnion(node *schemaNode, name string, exactName bool, origin string, request bool, depth int) (*Type, error) {
	variants := node.OneOf
	kindName := "oneOf"
	if len(variants) == 0 {
		variants = node.AnyOf
		kindName = "anyOf"
	}
	if node.Discriminator == nil || strings.TrimSpace(node.Discriminator.PropertyName) == "" {
		return nil, fmt.Errorf("%s requires a discriminator (propertyName) to generate an accurate tagged union", kindName)
	}
	if len(variants) == 0 {
		return nil, fmt.Errorf("%s has no variants", kindName)
	}
	if !exactName {
		name = c.names.Allocate(name)
	}
	m := c.models[name]
	if m == nil || m.Kind != ModelUnion {
		m = &Model{Name: name, Kind: ModelUnion, Origin: origin, Request: request,
			Description: node.Description, Discriminator: node.Discriminator.PropertyName}
		c.register(m)
	}

	valueFor := func(refName string) string {
		ref := "#/components/schemas/" + refName
		// The spec maps discriminator value -> schema reference.
		for value, target := range node.Discriminator.Mapping {
			if target == ref || target == refName {
				return value
			}
		}
		// Tolerate the inverse spelling used by some tools.
		if v, ok := node.Discriminator.Mapping[ref]; ok {
			return v
		}
		if v, ok := node.Discriminator.Mapping[refName]; ok {
			return v
		}
		return refName
	}
	seen := map[string]bool{}
	for i, raw := range variants {
		var vn schemaNode
		if err := json.Unmarshal(raw, &vn); err != nil {
			return nil, fmt.Errorf("%s[%d] is not a schema object", kindName, i)
		}
		if strings.TrimSpace(vn.Ref) == "" {
			return nil, fmt.Errorf("%s[%d] must be a local component reference", kindName, i)
		}
		compName, err := componentSchemaName(vn.Ref)
		if err != nil {
			return nil, err
		}
		vt, err := c.compileSchema(raw, name+"Variant"+strconv.Itoa(i+1), false, fmt.Sprintf("%s.%s[%d]", origin, kindName, i), request, depth+1)
		if err != nil {
			return nil, fmt.Errorf("%s[%d]: %w", kindName, i, err)
		}
		if vt.Kind != TypeModel {
			return nil, fmt.Errorf("%s[%d] does not resolve to an object model", kindName, i)
		}
		target := c.models[vt.Ref]
		if target == nil || target.Kind != ModelObject {
			return nil, fmt.Errorf("%s[%d] does not resolve to an object model", kindName, i)
		}
		if !hasJSONProperty(target, node.Discriminator.PropertyName) {
			return nil, fmt.Errorf("%s variant %q does not declare discriminator property %q", kindName, compName, node.Discriminator.PropertyName)
		}
		value := valueFor(compName)
		if seen[value] {
			return nil, fmt.Errorf("%s has duplicate discriminator value %q", kindName, value)
		}
		seen[value] = true
		m.Variants = append(m.Variants, UnionVariant{Value: value, Model: vt.Ref})
	}
	sort.SliceStable(m.Variants, func(i, j int) bool { return m.Variants[i].Value < m.Variants[j].Value })
	return &Type{Kind: TypeModel, Ref: name}, nil
}

func hasJSONProperty(m *Model, name string) bool {
	for _, p := range m.Properties {
		if p.JSONName == name {
			return true
		}
	}
	return false
}

func typesEqual(a, b *Type) bool {
	if a == nil || b == nil {
		return a == b
	}
	if a.Kind != b.Kind || a.Nullable != b.Nullable || a.Ref != b.Ref {
		return false
	}
	if !typesEqual(a.Items, b.Items) || !typesEqual(a.MapValue, b.MapValue) {
		return false
	}
	return true
}

func (c *typeCompiler) register(m *Model) {
	if m == nil || m.Name == "" {
		return
	}
	if _, exists := c.models[m.Name]; !exists {
		c.order = append(c.order, m.Name)
	}
	c.models[m.Name] = m
}
