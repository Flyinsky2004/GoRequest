package runner

import (
	"context"
	"gorequest/internal/core"
	"strings"
	"testing"
)

func TestSchemaAdapterFrozenOfflineAndPrecise(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{"collections/a/r.request.yaml": reqYAML("r", "GET", "/", assertExtra("schema: {ref: schemas/a.yaml}"))})
	put := func(file, body string) {
		t.Helper()
		if e := p.Store.WriteAtomic(file, []byte(body)); e != nil {
			t.Fatal(e)
		}
	}
	put("schemas/a.yaml", `type: object
properties:
  data: {$ref: b.yaml#/$defs/value}
required: [data]
`)
	put("schemas/b.yaml", `$defs:
  value:
    type: object
    properties:
      id: {type: integer}
    required: [id]
`)
	a := NewProjectSchemaAdapter(p)
	ref := SchemaRef{Ref: "schemas/a.yaml"}
	if e := a.PreflightSchema(ref); e != nil {
		t.Fatal(e)
	}
	put("schemas/b.yaml", "false")
	issues, e := a.ValidateSchema([]byte(`{"data":{"id":"private-value"}}`), ref)
	if e != nil || len(issues) != 1 || issues[0].InstancePath != "$.data.id" || strings.Contains(issues[0].Message, "private-value") {
		t.Fatalf("%+v %v", issues, e)
	}
	if issues, e = a.ValidateSchema([]byte(`{"data":{"id":1}}`), ref); e != nil || len(issues) != 0 {
		t.Fatalf("%+v %v", issues, e)
	}
}
func TestSchemaAdapterPreflightAllBranchesZeroRequests(t *testing.T) {
	for _, schema := range []string{`type: invalid`, `type: object
properties: {unused: {$ref: missing.yaml}}`, `type: object
properties: {unused: {pattern: '['}}`, `$ref: https://example.invalid/schema`, `$ref: ../outside.yaml`, `properties: {unused: {type: 123}}`, `null`, `items: {$ref: '#/items'}`, `unknownKeyword: true`} {
		t.Run(schema, func(t *testing.T) {
			p, _ := newFixtureProject(t, map[string]string{"collections/a/r.request.yaml": reqYAML("r", "GET", "/", assertExtra("schema: {ref: schemas/a.yaml}"))})
			if e := p.Store.WriteAtomic("schemas/a.yaml", []byte(schema)); e != nil {
				t.Fatal(e)
			}
			s, e := ParseSuite(suiteYAML("steps:\n  - {id: r, request: r}\n"))
			if e != nil {
				t.Fatal(e)
			}
			count := 0
			r := New(p, s, Options{Schema: NewProjectSchemaAdapter(p), Executor: execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
				count++
				return fixtureResult(`{}`, 200), nil
			})})
			result, e := r.Run(context.Background())
			if e == nil || result.Status != StatusConfigError || count != 0 {
				t.Fatalf("%v %+v calls=%d", e, result, count)
			}
		})
	}
}
func TestSchemaAdapterOperation(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{"collections/a/r.request.yaml": reqYAML("r", "GET", "/", "")})
	req := p.Requests[p.Order[0]].Request
	op := &core.OperationContract{OperationID: "getUser", Responses: map[string]core.ResponseContract{"200": {Content: map[string]core.MediaTypeContract{"application/json": {Schema: map[string]any{"type": "object"}}}}}}
	if e := core.SetRequestOpenAPI(req, op); e != nil {
		t.Fatal(e)
	}
	a := NewProjectSchemaAdapter(p)
	ref := SchemaRef{Operation: "getUser", Status: 404}
	if e := a.PreflightSchema(ref); e != nil {
		t.Fatal(e)
	}
	issues, e := a.ValidateSchema([]byte(`{}`), ref)
	if e != nil || len(issues) == 0 {
		t.Fatal(issues, e)
	}
	ref.Status = 200
	if issues, e = a.ValidateSchema([]byte(`{}`), ref); e != nil || len(issues) != 0 {
		t.Fatal(issues, e)
	}
	op.Responses["200"] = core.ResponseContract{}
	_ = core.SetRequestOpenAPI(req, op)
	if NewProjectSchemaAdapter(p).PreflightSchema(ref) == nil {
		t.Fatal("missing schema passed")
	}
}
