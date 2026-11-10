package core

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// graphQLIntrospectionQuery is the classic introspection query (graphql-js
// shape) restricted to fields the specification has declared for years. It
// intentionally avoids newer fields so a server that only implements the
// standard __schema/__type surface still answers it.
const graphQLIntrospectionQuery = `query IntrospectionQuery {
  __schema {
    queryType { name }
    mutationType { name }
    subscriptionType { name }
    types { ...FullType }
  }
}

fragment FullType on __Type {
  kind
  name
  description
  fields(includeDeprecated: true) {
    name
    description
    args { ...InputValue }
    type { ...TypeRef }
    isDeprecated
    deprecationReason
  }
  inputFields { ...InputValue }
  interfaces { ...TypeRef }
  enumValues(includeDeprecated: true) {
    name
    description
    isDeprecated
    deprecationReason
  }
  possibleTypes { ...TypeRef }
}

fragment InputValue on __InputValue {
  name
  description
  type { ...TypeRef }
  defaultValue
}

fragment TypeRef on __Type {
  kind
  name
  ofType {
    kind
    name
    ofType {
      kind
      name
      ofType {
        kind
        name
        ofType {
          kind
          name
          ofType {
            kind
            name
            ofType {
              kind
              name
              ofType { kind name }
            }
          }
        }
      }
    }
  }
}
`

// GraphQLIntrospectionDocument returns the exact query text the desktop sends
// after the user explicitly asks to introspect. It is a normal GraphQL
// document, so it travels through the existing HTTP plan and never bypasses
// auth, proxy, TLS, cancellation or target policy.
func GraphQLIntrospectionDocument() string { return graphQLIntrospectionQuery }

// GraphQLIntrospectionRef builds a graphql-protocol request referencing the
// introspection query while reusing the source request's URL/headers/auth.
// The caller decides when to execute it; nothing runs automatically.
func GraphQLIntrospectionRef(source *Request) (*Request, error) {
	if source == nil || source.HTTP == nil {
		return nil, configErrorf("introspection requires a graphql request with an http target")
	}
	derived := *source
	derived.Protocol = ProtocolGraphQL
	derived.GraphQL = &GraphQLDef{Document: graphQLIntrospectionQuery, Variables: "{}"}
	derived.WS = nil
	httpDef := *source.HTTP
	httpDef.Method = "POST"
	// The derived body is the introspection envelope built by the graphql
	// planner; never inherit a user body.
	httpDef.Body = nil
	derived.HTTP = &httpDef
	derived.Assert = nil
	return &derived, nil
}

// introspectionEnvelope is the subset of the introspection response this
// package understands. Unknown fields are ignored so server extensions do not
// break the parse.
type introspectionEnvelope struct {
	Data struct {
		Schema *introspectionSchema `json:"__schema"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type introspectionSchema struct {
	QueryType        *introspectionNamedType `json:"queryType"`
	MutationType     *introspectionNamedType `json:"mutationType"`
	SubscriptionType *introspectionNamedType `json:"subscriptionType"`
	Types            []introspectionType     `json:"types"`
}

type introspectionNamedType struct {
	Name string `json:"name"`
}

type introspectionType struct {
	Kind          string                 `json:"kind"`
	Name          string                 `json:"name"`
	Description   *string                `json:"description"`
	Fields        []introspectionField   `json:"fields"`
	InputFields   []introspectionInput   `json:"inputFields"`
	Interfaces    []introspectionTypeRef `json:"interfaces"`
	EnumValues    []introspectionEnum    `json:"enumValues"`
	PossibleTypes []introspectionTypeRef `json:"possibleTypes"`
}

type introspectionField struct {
	Name              string               `json:"name"`
	Description       *string              `json:"description"`
	Args              []introspectionInput `json:"args"`
	Type              introspectionTypeRef `json:"type"`
	IsDeprecated      bool                 `json:"isDeprecated"`
	DeprecationReason *string              `json:"deprecationReason"`
}

type introspectionInput struct {
	Name         string               `json:"name"`
	Description  *string              `json:"description"`
	Type         introspectionTypeRef `json:"type"`
	DefaultValue *string              `json:"defaultValue"`
}

type introspectionEnum struct {
	Name              string  `json:"name"`
	Description       *string `json:"description"`
	IsDeprecated      bool    `json:"isDeprecated"`
	DeprecationReason *string `json:"deprecationReason"`
}

type introspectionTypeRef struct {
	Kind   string                `json:"kind"`
	Name   *string               `json:"name"`
	OfType *introspectionTypeRef `json:"ofType"`
}

// ParseGraphQLIntrospection converts an introspection response body into
// deterministic SDL text and a Schema Explorer model. The generated SDL is
// parsed and validated with the local SDL pipeline before it is returned, so a
// saved introspection snapshot is exactly as trustworthy as a hand-written SDL
// file and can be used offline later.
//
// Introspection being disabled is a distinct, honest error: the response is
// parsed, `data.__schema` is absent, and nothing is invented.
func ParseGraphQLIntrospection(body []byte) (string, *GraphQLSchemaModel, error) {
	if len(body) > MaxGraphQLIntrospectionBytes {
		return "", nil, configErrorf("graphql introspection response exceeds %d bytes", MaxGraphQLIntrospectionBytes)
	}
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return "", nil, configErrorf("graphql introspection response is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(trimmed))
	var env introspectionEnvelope
	if err := dec.Decode(&env); err != nil {
		return "", nil, configErrorf("graphql introspection response is not valid JSON: %v", err)
	}
	if env.Data.Schema == nil {
		if len(env.Errors) > 0 {
			return "", nil, configErrorf("graphql introspection was rejected: %s", env.Errors[0].Message)
		}
		return "", nil, configErrorf("graphql introspection returned no __schema (introspection may be disabled)")
	}
	sdl, err := introspectionSDL(env.Data.Schema)
	if err != nil {
		return "", nil, err
	}
	model, err := ParseGraphQLSchemaSDL("introspection", sdl)
	if err != nil {
		return "", nil, configErrorf("introspection produced an invalid schema: %v", err)
	}
	return sdl, model, nil
}

// introspectionSDL renders the standard SDL shape for the returned types.
func introspectionSDL(schema *introspectionSchema) (string, error) {
	types := append([]introspectionType(nil), schema.Types...)
	sort.Slice(types, func(i, j int) bool { return types[i].Name < types[j].Name })
	if len(types) > MaxGraphQLSchemaTypes {
		return "", configErrorf("graphql introspection returned more than %d types", MaxGraphQLSchemaTypes)
	}

	var b strings.Builder
	b.WriteString("# Generated from an explicit introspection request. Do not edit by hand.\n")
	if root := introspectionRootBlock(schema); root != "" {
		b.WriteString(root)
	}
	for _, t := range types {
		if strings.TrimSpace(t.Name) == "" || strings.HasPrefix(t.Name, graphQLIntrospectionTypePrefix) {
			continue
		}
		if isIntrospectionBuiltinScalar(t.Name) {
			continue
		}
		block, err := introspectionTypeSDL(t)
		if err != nil {
			return "", err
		}
		if block == "" {
			continue
		}
		b.WriteString("\n")
		b.WriteString(block)
	}
	return b.String(), nil
}

func introspectionRootBlock(schema *introspectionSchema) string {
	name := func(t *introspectionNamedType) string {
		if t == nil {
			return ""
		}
		return strings.TrimSpace(t.Name)
	}
	q, m, s := name(schema.QueryType), name(schema.MutationType), name(schema.SubscriptionType)
	if q == "" {
		q = "Query"
	}
	custom := q != "Query" || (m != "" && m != "Mutation") || (s != "" && s != "Subscription")
	if !custom {
		return ""
	}
	var b strings.Builder
	b.WriteString("schema {\n")
	if q != "" {
		fmt.Fprintf(&b, "  query: %s\n", q)
	}
	if m != "" {
		fmt.Fprintf(&b, "  mutation: %s\n", m)
	}
	if s != "" {
		fmt.Fprintf(&b, "  subscription: %s\n", s)
	}
	b.WriteString("}\n")
	return b.String()
}

func introspectionTypeSDL(t introspectionType) (string, error) {
	var b strings.Builder
	writeDescription(&b, t.Description, "")
	switch strings.ToUpper(t.Kind) {
	case "SCALAR":
		fmt.Fprintf(&b, "scalar %s\n", t.Name)
		return b.String(), nil
	case "OBJECT", "INTERFACE":
		keyword := "type"
		if strings.EqualFold(t.Kind, "INTERFACE") {
			keyword = "interface"
		}
		fmt.Fprintf(&b, "%s %s", keyword, t.Name)
		if ifaces := typeRefNames(t.Interfaces); len(ifaces) > 0 {
			fmt.Fprintf(&b, " implements %s", strings.Join(ifaces, " & "))
		}
		b.WriteString(" {\n")
		fields := append([]introspectionField(nil), t.Fields...)
		sort.Slice(fields, func(i, j int) bool { return fields[i].Name < fields[j].Name })
		if len(fields) > MaxGraphQLSchemaFieldsPerType {
			return "", configErrorf("graphql introspection type %s has more than %d fields", t.Name, MaxGraphQLSchemaFieldsPerType)
		}
		for _, f := range fields {
			writeDescription(&b, f.Description, "  ")
			fmt.Fprintf(&b, "  %s%s: %s%s\n", f.Name, introspectionArgs(f.Args), typeRefSDL(f.Type), deprecatedSDL(f.IsDeprecated, f.DeprecationReason))
		}
		b.WriteString("}\n")
		return b.String(), nil
	case "UNION":
		fmt.Fprintf(&b, "union %s = %s\n", t.Name, strings.Join(typeRefNames(t.PossibleTypes), " | "))
		return b.String(), nil
	case "ENUM":
		fmt.Fprintf(&b, "enum %s {\n", t.Name)
		values := append([]introspectionEnum(nil), t.EnumValues...)
		sort.Slice(values, func(i, j int) bool { return values[i].Name < values[j].Name })
		if len(values) > MaxGraphQLSchemaEnumValues {
			return "", configErrorf("graphql introspection enum %s has more than %d values", t.Name, MaxGraphQLSchemaEnumValues)
		}
		for _, v := range values {
			writeDescription(&b, v.Description, "  ")
			fmt.Fprintf(&b, "  %s%s\n", v.Name, deprecatedSDL(v.IsDeprecated, v.DeprecationReason))
		}
		b.WriteString("}\n")
		return b.String(), nil
	case "INPUT_OBJECT":
		fmt.Fprintf(&b, "input %s {\n", t.Name)
		inputs := append([]introspectionInput(nil), t.InputFields...)
		sort.Slice(inputs, func(i, j int) bool { return inputs[i].Name < inputs[j].Name })
		if len(inputs) > MaxGraphQLSchemaFieldsPerType {
			return "", configErrorf("graphql introspection input %s has more than %d fields", t.Name, MaxGraphQLSchemaFieldsPerType)
		}
		for _, in := range inputs {
			writeDescription(&b, in.Description, "  ")
			fmt.Fprintf(&b, "  %s: %s%s\n", in.Name, typeRefSDL(in.Type), defaultValueSDL(in.DefaultValue))
		}
		b.WriteString("}\n")
		return b.String(), nil
	default:
		return "", nil
	}
}

func introspectionArgs(args []introspectionInput) string {
	if len(args) == 0 {
		return ""
	}
	if len(args) > MaxGraphQLSchemaArgsPerField {
		args = args[:MaxGraphQLSchemaArgsPerField]
	}
	parts := make([]string, 0, len(args))
	for _, a := range args {
		parts = append(parts, a.Name+": "+typeRefSDL(a.Type)+defaultValueSDL(a.DefaultValue))
	}
	return "(" + strings.Join(parts, ", ") + ")"
}

func typeRefSDL(t introspectionTypeRef) string {
	switch strings.ToUpper(t.Kind) {
	case "NON_NULL":
		if t.OfType != nil {
			return typeRefSDL(*t.OfType) + "!"
		}
		return "Unknown!"
	case "LIST":
		if t.OfType != nil {
			return "[" + typeRefSDL(*t.OfType) + "]"
		}
		return "[Unknown]"
	default:
		if t.Name == nil {
			return "Unknown"
		}
		return *t.Name
	}
}

func typeRefNames(refs []introspectionTypeRef) []string {
	out := make([]string, 0, len(refs))
	for _, ref := range refs {
		if ref.Name != nil && strings.TrimSpace(*ref.Name) != "" {
			out = append(out, *ref.Name)
		}
	}
	sort.Strings(out)
	return out
}

func writeDescription(b *strings.Builder, desc *string, indent string) {
	if desc == nil || strings.TrimSpace(*desc) == "" {
		return
	}
	b.WriteString(indent)
	b.WriteString(graphQLDescriptionLiteral(strings.TrimSpace(*desc)))
	b.WriteString("\n")
}

// graphQLDescriptionLiteral renders a description as a GraphQL string literal
// (single line) or block string (multi-line).
func graphQLDescriptionLiteral(s string) string {
	if !strings.Contains(s, "\n") {
		return strconvQuote(s)
	}
	escaped := strings.ReplaceAll(s, `\"""`, `\\"""`)
	escaped = strings.ReplaceAll(escaped, `"""`, `\"""`)
	return "\"\"\"\n" + escaped + "\n\"\"\""
}

// strconvQuote quotes a single-line string with GraphQL's escape rules.
func strconvQuote(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '"':
			b.WriteString(`\"`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func deprecatedSDL(deprecated bool, reason *string) string {
	if !deprecated {
		return ""
	}
	if reason == nil || strings.TrimSpace(*reason) == "" || strings.TrimSpace(*reason) == "No longer supported" {
		return " @deprecated"
	}
	return " @deprecated(reason: " + strconvQuote(*reason) + ")"
}

func defaultValueSDL(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return ""
	}
	return " = " + *value
}

func isIntrospectionBuiltinScalar(name string) bool {
	switch name {
	case "String", "Int", "Float", "Boolean", "ID":
		return true
	}
	return false
}
