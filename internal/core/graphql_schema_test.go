package core

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

const testGraphQLSDL = `"""Root queries."""
type Query {
  """Fetch one user."""
  user("""
  Stable identifier.
  """id: ID!): User
  legacy: String @deprecated(reason: "Use user")
}

type User implements Node {
  id: ID!
  name: String
  role: Role
  friends(first: Int = 10): [User!]!
}

interface Node {
  id: ID!
}

union SearchResult = User

enum Role {
  ADMIN
  GUEST @deprecated(reason: "No guests")
}

input UserFilter {
  name: String = "all"
  roles: [Role!]
}

type Mutation {
  rename(id: ID!, name: String!): User!
}

type Subscription {
  userChanged(id: ID!): User!
}
`

func TestGraphQLSchemaModelFromSDL(t *testing.T) {
	model, err := ParseGraphQLSchemaSDL("test.graphql", testGraphQLSDL)
	if err != nil {
		t.Fatalf("ParseGraphQLSchemaSDL: %v", err)
	}
	if model.QueryType != "Query" || model.MutationType != "Mutation" || model.SubscriptionType != "Subscription" {
		t.Fatalf("unexpected roots: %+v", model)
	}
	if model.Digest == "" || model.Digest != GraphQLSchemaDigest(testGraphQLSDL) {
		t.Fatalf("digest mismatch: %q", model.Digest)
	}

	byName := map[string]GraphQLSchemaTypeInfo{}
	for _, typ := range model.Types {
		byName[typ.Name] = typ
	}
	query, ok := byName["Query"]
	if !ok {
		t.Fatal("Query missing from explorer model")
	}
	var userField *GraphQLSchemaFieldInfo
	var legacy *GraphQLSchemaFieldInfo
	for i := range query.Fields {
		switch query.Fields[i].Name {
		case "user":
			userField = &query.Fields[i]
		case "legacy":
			legacy = &query.Fields[i]
		}
	}
	if userField == nil || userField.Type != "User" {
		t.Fatalf("user field wrong: %+v", userField)
	}
	if len(userField.Args) != 1 || userField.Args[0].Name != "id" || userField.Args[0].Type != "ID!" {
		t.Fatalf("user args wrong: %+v", userField.Args)
	}
	if !strings.Contains(userField.Description, "Fetch one user") {
		t.Fatalf("field description missing: %q", userField.Description)
	}
	if legacy == nil || !legacy.Deprecated || legacy.DeprecationReason != "Use user" {
		t.Fatalf("deprecated field wrong: %+v", legacy)
	}

	user := byName["User"]
	if len(user.Interfaces) != 1 || user.Interfaces[0] != "Node" {
		t.Fatalf("interfaces wrong: %+v", user.Interfaces)
	}
	var friends *GraphQLSchemaFieldInfo
	for i := range user.Fields {
		if user.Fields[i].Name == "friends" {
			friends = &user.Fields[i]
		}
	}
	if friends == nil || friends.Type != "[User!]!" || len(friends.Args) != 1 || friends.Args[0].DefaultValue != "10" {
		t.Fatalf("friends field wrong: %+v", friends)
	}

	role := byName["Role"]
	if len(role.EnumValues) != 2 || role.EnumValues[0].Name != "ADMIN" || !role.EnumValues[1].Deprecated {
		t.Fatalf("enum model wrong: %+v", role.EnumValues)
	}
	filter := byName["UserFilter"]
	if len(filter.InputFields) != 2 || filter.InputFields[1].Type != "[Role!]" {
		t.Fatalf("input model wrong: %+v", filter.InputFields)
	}
	union := byName["SearchResult"]
	if union.Kind != "union" || len(union.PossibleTypes) != 1 || union.PossibleTypes[0] != "User" {
		t.Fatalf("union model wrong: %+v", union)
	}
	// Built-in scalars are marked, not silently indistinguishable.
	if id := byName["ID"]; !id.Builtin || id.Kind != "scalar" {
		t.Fatalf("builtin scalar model wrong: %+v", id)
	}
}

func TestGraphQLSchemaOfflineValidation(t *testing.T) {
	model, err := ParseGraphQLSchemaSDL("test.graphql", testGraphQLSDL)
	if err != nil {
		t.Fatal(err)
	}
	good := "query One { user(id: \"1\") { id name } }"
	analysis, diags, err := model.ValidateDocument(good, "")
	if err != nil || len(diags) != 0 || analysis.Selected.Type != "query" {
		t.Fatalf("valid document rejected: analysis=%+v diags=%+v err=%v", analysis, diags, err)
	}

	bad := "query One { user(id: 1) { nope } }"
	analysis, diags, err = model.ValidateDocument(bad, "")
	if err == nil || len(diags) == 0 {
		t.Fatalf("invalid document accepted: analysis=%+v", analysis)
	}
	if diags[0].Line == 0 || !strings.Contains(diags[0].String(), "line") {
		t.Fatalf("diagnostic lacks source location: %+v", diags[0])
	}

	// A subscription validates against the SDL offline and is analyzable by
	// the subscription workspace, while AnalyzeGraphQL keeps rejecting it for
	// the HTTP executor.
	sub := "subscription Watch { userChanged(id: \"1\") { id } }"
	analysis, diags, err = model.ValidateDocument(sub, "")
	if err != nil || len(diags) != 0 || analysis.Selected.Type != "subscription" {
		t.Fatalf("subscription validation failed: %+v %+v %v", analysis, diags, err)
	}
	if _, err := AnalyzeGraphQL(sub, ""); err == nil {
		t.Fatal("HTTP analysis must keep rejecting subscriptions")
	}
	if got, err := AnalyzeGraphQLDocument(sub, ""); err != nil || got.Selected.Type != "subscription" {
		t.Fatalf("AnalyzeGraphQLDocument must accept subscriptions: %+v %v", got, err)
	}
}

func TestGraphQLSchemaDigestChangesAndBounds(t *testing.T) {
	model, err := ParseGraphQLSchemaSDL("a.graphql", "type Query { a: String }")
	if err != nil {
		t.Fatal(err)
	}
	other, err := ParseGraphQLSchemaSDL("a.graphql", "type Query { a: Int }")
	if err != nil {
		t.Fatal(err)
	}
	if model.Digest == other.Digest {
		t.Fatal("changed SDL must change the digest")
	}
	if _, err := ParseGraphQLSchemaSDL("empty.graphql", "   "); err == nil {
		t.Fatal("empty schema must be rejected")
	}
	if _, err := ParseGraphQLSchemaSDL("bad.graphql", "type Query { "); err == nil || !strings.Contains(err.Error(), "line") {
		t.Fatalf("bad schema must report a location: %v", err)
	}
	if _, err := ParseGraphQLSchemaSDL("huge.graphql", strings.Repeat(" ", MaxGraphQLSchemaBytes+1)); err == nil {
		t.Fatal("oversized schema must be rejected before parsing")
	}
	// Long descriptions are truncated and recorded, not rejected.
	long := `"""` + strings.Repeat("x", MaxGraphQLSchemaDescriptionBytes+50) + `""" type Query { a: String }`
	truncated, err := ParseGraphQLSchemaSDL("long.graphql", long)
	if err != nil {
		t.Fatal(err)
	}
	if len(truncated.Types) == 0 {
		t.Fatal("long schema produced no types")
	}
	var longQuery *GraphQLSchemaTypeInfo
	for i := range truncated.Types {
		if truncated.Types[i].Name == "Query" {
			longQuery = &truncated.Types[i]
		}
	}
	if longQuery == nil || !longQuery.DescriptionTruncated || len(longQuery.Description) <= MaxGraphQLSchemaDescriptionBytes {
		t.Fatalf("long description was not reported as truncated: %+v", longQuery)
	}
}

func TestGraphQLSchemaRefExtensionRoundTrip(t *testing.T) {
	req := &Request{Protocol: ProtocolGraphQL}
	if _, ok := GraphQLSchemaRefFromRequest(req); ok {
		t.Fatal("no reference expected")
	}
	SetGraphQLSchemaRef(req, GraphQLSchemaRef{Path: "  schemas/api.graphql ", Digest: " abc "})
	ref, ok := GraphQLSchemaRefFromRequest(req)
	if !ok || ref.Path != "schemas/api.graphql" || ref.Digest != "abc" {
		t.Fatalf("reference round trip failed: %+v ok=%v", ref, ok)
	}
	SetGraphQLSchemaRef(req, GraphQLSchemaRef{})
	if _, ok := GraphQLSchemaRefFromRequest(req); ok {
		t.Fatal("cleared reference must disappear")
	}
}

func TestLoadGraphQLSchemaProjectFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".gorequest", "schemas")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "api.graphql"), []byte(testGraphQLSDL), 0o644); err != nil {
		t.Fatal(err)
	}
	opener := NewDirFileOpener(filepath.Join(root, ".gorequest"))
	model, err := LoadGraphQLSchemaProjectFile(context.Background(), opener, "schemas/api.graphql")
	if err != nil {
		t.Fatalf("load project schema: %v", err)
	}
	if model.SourcePath != "schemas/api.graphql" || model.QueryType != "Query" {
		t.Fatalf("unexpected model: %+v", model)
	}
	if _, err := LoadGraphQLSchemaProjectFile(context.Background(), opener, "../outside.graphql"); err == nil {
		t.Fatal("path traversal must be rejected by the confined opener")
	}
}

const testIntrospectionBody = `{
  "data": {
    "__schema": {
      "queryType": {"name": "Query"},
      "mutationType": null,
      "subscriptionType": {"name": "Subscription"},
      "types": [
        {"kind":"OBJECT","name":"Query","description":null,"fields":[
          {"name":"user","description":"Fetch one user","args":[
            {"name":"id","description":null,"type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"ID","ofType":null}},"defaultValue":null}
          ],"type":{"kind":"OBJECT","name":"User","ofType":null},"isDeprecated":false,"deprecationReason":null}
        ],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null},
        {"kind":"OBJECT","name":"User","description":null,"fields":[
          {"name":"id","description":null,"args":[],"type":{"kind":"SCALAR","name":"ID","ofType":null},"isDeprecated":false,"deprecationReason":null},
          {"name":"legacy","description":null,"args":[],"type":{"kind":"SCALAR","name":"String","ofType":null},"isDeprecated":true,"deprecationReason":"Use id"}
        ],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null},
        {"kind":"SCALAR","name":"ID","description":null,"fields":null,"inputFields":null,"interfaces":null,"enumValues":null,"possibleTypes":null},
        {"kind":"SCALAR","name":"String","description":null,"fields":null,"inputFields":null,"interfaces":null,"enumValues":null,"possibleTypes":null},
        {"kind":"OBJECT","name":"Subscription","description":null,"fields":[
          {"name":"ticks","description":null,"args":[],"type":{"kind":"NON_NULL","name":null,"ofType":{"kind":"SCALAR","name":"Int","ofType":null}},"isDeprecated":false,"deprecationReason":null}
        ],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null},
        {"kind":"OBJECT","name":"__Type","description":"introspection","fields":[],"inputFields":null,"interfaces":[],"enumValues":null,"possibleTypes":null}
      ]
    }
  }
}`

func TestParseGraphQLIntrospection(t *testing.T) {
	sdl, model, err := ParseGraphQLIntrospection([]byte(testIntrospectionBody))
	if err != nil {
		t.Fatalf("ParseGraphQLIntrospection: %v", err)
	}
	if strings.Contains(sdl, "__Type") {
		t.Fatalf("introspection types must not leak into SDL:\n%s", sdl)
	}
	if model.QueryType != "Query" || model.SubscriptionType != "Subscription" || model.MutationType != "" {
		t.Fatalf("unexpected roots: %+v", model)
	}
	byName := map[string]GraphQLSchemaTypeInfo{}
	for _, typ := range model.Types {
		byName[typ.Name] = typ
	}
	var legacy *GraphQLSchemaFieldInfo
	for i := range byName["User"].Fields {
		if byName["User"].Fields[i].Name == "legacy" {
			legacy = &byName["User"].Fields[i]
		}
	}
	if legacy == nil || !legacy.Deprecated || legacy.DeprecationReason != "Use id" {
		t.Fatalf("introspected deprecation lost: %+v", legacy)
	}
	var userField *GraphQLSchemaFieldInfo
	for i := range byName["Query"].Fields {
		if byName["Query"].Fields[i].Name == "user" {
			userField = &byName["Query"].Fields[i]
		}
	}
	if userField == nil || userField.Type != "User" || len(userField.Args) != 1 || userField.Args[0].Type != "ID!" {
		t.Fatalf("introspected field lost shape: %+v", userField)
	}
	// The generated SDL is itself valid, so it can be saved and re-used
	// offline against the same operations.
	analysis, diags, err := model.ValidateDocument(`query Q { user(id: "1") { id } }`, "")
	if err != nil || len(diags) != 0 || analysis.Selected.Name != "Q" {
		t.Fatalf("generated SDL does not validate documents: %+v %+v %v", analysis, diags, err)
	}
	if model.Digest != GraphQLSchemaDigest(sdl) {
		t.Fatal("introspection digest must cover the saved SDL")
	}
}

func TestParseGraphQLIntrospectionFailures(t *testing.T) {
	if _, _, err := ParseGraphQLIntrospection([]byte(`{"data":{"__schema":null},"errors":[{"message":"introspection is disabled"}]}`)); err == nil || !strings.Contains(err.Error(), "introspection is disabled") {
		t.Fatalf("disabled introspection must be reported honestly: %v", err)
	}
	if _, _, err := ParseGraphQLIntrospection([]byte(`{"data":{}}`)); err == nil || !strings.Contains(err.Error(), "__schema") {
		t.Fatalf("missing schema must be reported: %v", err)
	}
	if _, _, err := ParseGraphQLIntrospection([]byte(`not json`)); err == nil {
		t.Fatal("invalid JSON must be rejected")
	}
	if _, _, err := ParseGraphQLIntrospection(make([]byte, MaxGraphQLIntrospectionBytes+1)); err == nil {
		t.Fatal("oversized introspection must be rejected")
	}
}

func TestGraphQLIntrospectionRefKeepsTargetAndDropsBody(t *testing.T) {
	src := &Request{
		Protocol: ProtocolGraphQL,
		HTTP: &HTTPDef{
			Method:  "GET",
			URL:     "https://example.test/graphql",
			Headers: []KV{{Name: "X-Test", Value: "1"}},
			Body:    &Body{Type: "json"},
		},
		GraphQL: &GraphQLDef{Document: "query Q { a }"},
		Assert:  []yaml.Node{{Kind: yaml.ScalarNode, Value: "kept-out"}},
	}
	derived, err := GraphQLIntrospectionRef(src)
	if err != nil {
		t.Fatal(err)
	}
	if derived == src || derived.HTTP == src.HTTP {
		t.Fatal("derived request must be a copy")
	}
	if derived.HTTP.Method != "POST" || derived.HTTP.Body != nil || derived.HTTP.URL != src.HTTP.URL {
		t.Fatalf("derived http wrong: %+v", derived.HTTP)
	}
	if derived.GraphQL == nil || !strings.Contains(derived.GraphQL.Document, "__schema") {
		t.Fatal("derived graphql document must be the introspection query")
	}
	if len(derived.Assert) != 0 {
		t.Fatal("derived introspection request must not inherit source assertions")
	}
	if src.Protocol != ProtocolGraphQL || src.GraphQL.Document != "query Q { a }" {
		t.Fatal("source request must not be mutated")
	}
}

func TestGraphQLSyntaxDepthBound(t *testing.T) {
	query := "query { " + strings.Repeat("a {", MaxGraphQLSyntaxDepth) + "a" + strings.Repeat("}", MaxGraphQLSyntaxDepth) + " }"
	if _, err := AnalyzeGraphQLDocument(query, ""); err == nil {
		t.Fatal("deep selection accepted")
	}
	schema := "type Query { field: " + strings.Repeat("[", MaxGraphQLSyntaxDepth) + "String" + strings.Repeat("]", MaxGraphQLSyntaxDepth) + " }"
	if _, err := ParseGraphQLSchemaSDL("deep", schema); err == nil {
		t.Fatal("deep SDL accepted")
	}
	if _, err := ParseGraphQLSchemaSDL("quoted", `type Query { field(arg: String = "[[[{{{((("): String }`); err != nil {
		t.Fatalf("string delimiters counted: %v", err)
	}
}
