package runner

import (
	"strings"
	"testing"

	"gorequest/internal/core"
)

func schemaContractFixture(t *testing.T, op *core.OperationContract) (*ProjectSchemaAdapter, *core.Project) {
	t.Helper()
	p, _ := newFixtureProject(t, map[string]string{
		"collections/a/r.request.yaml": reqYAML("r", "GET", "/", ""),
	})
	entry := p.Requests[p.Order[0]]
	if err := core.SetRequestOpenAPI(entry.Request, op); err != nil {
		t.Fatal(err)
	}
	return NewProjectSchemaAdapter(p), p
}

func multiMediaOperation() *core.OperationContract {
	return &core.OperationContract{
		OperationID: "multi",
		Responses: map[string]core.ResponseContract{
			"200": {
				Headers: map[string]core.HeaderContract{
					"X-Rate-Limit": {Required: true, Schema: map[string]any{"type": "integer", "minimum": 0}},
					"X-Mode":       {Required: false, Schema: map[string]any{"type": "string", "enum": []any{"fast", "slow"}}},
				},
				Content: map[string]core.MediaTypeContract{
					"application/json":         {Schema: map[string]any{"type": "object", "properties": map[string]any{"ok": map[string]any{"type": "boolean"}}, "required": []any{"ok"}}},
					"text/plain":               {Schema: map[string]any{"type": "string", "minLength": 2}},
					"application/octet-stream": {},
				},
			},
			"204": {},
		},
	}
}

func TestSchemaContractHeadersAndMultiMedia(t *testing.T) {
	a, _ := schemaContractFixture(t, multiMediaOperation())
	ref := SchemaRef{Operation: "multi", Status: 200}
	if err := a.PreflightSchema(ref); err != nil {
		t.Fatal(err)
	}
	rate := [2]string{"X-Rate-Limit", "7"}
	cases := []struct {
		name    string
		headers [][2]string
		body    []byte
		issues  int
		path    string
	}{
		{"json ok", [][2]string{rate, {"Content-Type", "application/json; charset=utf-8"}}, []byte(`{"ok":true}`), 0, ""},
		{"json violation", [][2]string{rate, {"Content-Type", "application/json"}}, []byte(`{"ok":"nope"}`), 1, "$.ok"},
		{"text ok", [][2]string{rate, {"Content-Type", "text/plain; charset=utf-8"}}, []byte("hello"), 0, ""},
		{"text boundary", [][2]string{rate, {"Content-Type", "text/plain"}}, []byte("x"), 1, "$"},
		{"text invalid utf8", [][2]string{rate, {"Content-Type", "text/plain"}}, []byte{0xff, 0xfe}, 1, "$"},
		{"binary boundary", [][2]string{rate, {"Content-Type", "application/octet-stream"}}, []byte{0x00, 0xff, 0x10}, 0, ""},
		{"undeclared media", [][2]string{rate, {"Content-Type", "application/xml"}}, []byte(`<x/>`), 1, "$.headers.Content-Type"},
		{"missing content type", [][2]string{rate}, []byte(`{}`), 1, "$.headers.Content-Type"},
		{"missing required header", [][2]string{{"Content-Type", "application/json"}}, []byte(`{"ok":true}`), 1, "$.headers.X-Rate-Limit"},
		{"bad integer header", [][2]string{{"Content-Type", "application/json"}, {"X-Rate-Limit", "abc"}}, []byte(`{"ok":true}`), 1, "$.headers.X-Rate-Limit"},
		{"enum header", [][2]string{rate, {"Content-Type", "application/json"}, {"X-Mode", "other"}}, []byte(`{"ok":true}`), 1, "$.headers.X-Mode"},
		{"optional header absent", [][2]string{rate, {"Content-Type", "application/json"}}, []byte(`{"ok":true}`), 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues, err := a.ValidateSchema(tc.body, SchemaRef{Operation: "multi", Status: 200, Headers: tc.headers})
			if err != nil {
				t.Fatal(err)
			}
			if len(issues) != tc.issues {
				t.Fatalf("issues = %+v", issues)
			}
			if tc.path != "" && issues[0].InstancePath != tc.path {
				t.Fatalf("path = %q want %q", issues[0].InstancePath, tc.path)
			}
			for _, issue := range issues {
				if strings.Contains(issue.Message, "hello") || strings.Contains(issue.Message, "nope") || strings.Contains(issue.Message, "other") {
					t.Fatalf("schema issue echoed a value: %+v", issue)
				}
			}
		})
	}
}

func TestSchemaContractNoContentResponse(t *testing.T) {
	a, _ := schemaContractFixture(t, multiMediaOperation())
	ref := SchemaRef{Operation: "multi", Status: 204}
	if issues, err := a.ValidateSchema(nil, ref); err != nil || len(issues) != 0 {
		t.Fatalf("empty 204: %+v %v", issues, err)
	}
	if issues, err := a.ValidateSchema([]byte("body"), ref); err != nil || len(issues) == 0 {
		t.Fatalf("body against 204 contract: %+v %v", issues, err)
	}
}

func TestSchemaContractStrictPreflight(t *testing.T) {
	for name, op := range map[string]*core.OperationContract{
		"unsupported media":          {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Content: map[string]core.MediaTypeContract{"application/xml": {Schema: map[string]any{"type": "string"}}}}}},
		"binary with schema":         {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Content: map[string]core.MediaTypeContract{"application/octet-stream": {Schema: map[string]any{"type": "string"}}}}}},
		"text non-string schema":     {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Content: map[string]core.MediaTypeContract{"text/plain": {Schema: map[string]any{"type": "object"}}}}}},
		"json without schema":        {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Content: map[string]core.MediaTypeContract{"application/json": {}}}}},
		"header object schema":       {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Headers: map[string]core.HeaderContract{"X": {Schema: map[string]any{"type": "object"}}}, Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}}}}},
		"header untyped schema":      {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Headers: map[string]core.HeaderContract{"X": {Schema: map[string]any{"minimum": 1}}}, Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}}}}},
		"header schema and ref":      {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Headers: map[string]core.HeaderContract{"X": {SchemaRef: "s.yaml", Schema: map[string]any{"type": "string"}}}, Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}}}}},
		"duplicate normalized media": {OperationID: "x", Responses: map[string]core.ResponseContract{"200": {Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}, "Application/JSON": {Schema: map[string]any{"type": "object"}}}}}},
		"bad status key":             {OperationID: "x", Responses: map[string]core.ResponseContract{"2xx": {Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}}}}},
	} {
		t.Run(name, func(t *testing.T) {
			a, _ := schemaContractFixture(t, op)
			if err := a.PreflightSchema(SchemaRef{Operation: "x", Status: 200}); err == nil {
				t.Fatal("unsupported response contract passed preflight")
			}
		})
	}
}

func TestSchemaContractHeaderRefResolvedOffline(t *testing.T) {
	a, p := schemaContractFixture(t, &core.OperationContract{
		OperationID: "hdr",
		Responses: map[string]core.ResponseContract{"200": {
			Headers: map[string]core.HeaderContract{"X-Limit": {Required: true, SchemaRef: "schemas/header.yaml"}},
			Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}},
		}},
	})
	if err := p.Store.WriteAtomic("schemas/header.yaml", []byte("type: integer\nminimum: 1\n")); err != nil {
		t.Fatal(err)
	}
	ref := SchemaRef{Operation: "hdr", Status: 200}
	if err := a.PreflightSchema(ref); err != nil {
		t.Fatal(err)
	}
	issues, err := a.ValidateSchema([]byte(`{}`), SchemaRef{Operation: "hdr", Status: 200, Headers: [][2]string{{"Content-Type", "application/json"}, {"X-Limit", "0"}}})
	if err != nil || len(issues) != 1 || issues[0].InstancePath != "$.headers.X-Limit" {
		t.Fatalf("%+v %v", issues, err)
	}
	// The frozen bytes remain usable after the file changes, and a deleted
	// reference fails preflight for a fresh adapter.
	if err := p.Store.WriteAtomic("schemas/header.yaml", []byte("type: string\n")); err != nil {
		t.Fatal(err)
	}
	if issues, err = a.ValidateSchema([]byte(`{}`), SchemaRef{Operation: "hdr", Status: 200, Headers: [][2]string{{"Content-Type", "application/json"}, {"X-Limit", "0"}}}); err != nil || len(issues) != 1 {
		t.Fatalf("frozen header schema was not retained: %+v %v", issues, err)
	}
	_ = p.Store.WriteAtomic("schemas/header.yaml", []byte("type: string"))
	bad, _ := schemaContractFixture(t, &core.OperationContract{
		OperationID: "hdr",
		Responses: map[string]core.ResponseContract{"200": {
			Headers: map[string]core.HeaderContract{"X-Limit": {SchemaRef: "schemas/missing.yaml"}},
		}},
	})
	if err := bad.PreflightSchema(SchemaRef{Operation: "hdr", Status: 200}); err == nil {
		t.Fatal("missing header schema reference passed preflight")
	}
}

func TestSchemaContractSnapshotIncludesHeadersAndMedia(t *testing.T) {
	a, _ := schemaContractFixture(t, multiMediaOperation())
	ref := SchemaRef{Operation: "multi", Status: 200}
	if err := a.PreflightSchema(ref); err != nil {
		t.Fatal(err)
	}
	first := a.SchemaSnapshotDigest()
	op := multiMediaOperation()
	op.Responses["200"].Headers["X-Rate-Limit"] = core.HeaderContract{Required: true, Schema: map[string]any{"type": "number", "minimum": 0}}
	b, _ := schemaContractFixture(t, op)
	if err := b.PreflightSchema(ref); err != nil {
		t.Fatal(err)
	}
	if first == b.SchemaSnapshotDigest() {
		t.Fatal("header contract change did not alter the snapshot digest")
	}
}
