package runner

import (
	"context"
	"encoding/json"
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
	ref.Headers = [][2]string{{"Content-Type", "application/json"}}
	if issues, e = a.ValidateSchema([]byte(`{}`), ref); e != nil || len(issues) != 0 {
		t.Fatal(issues, e)
	}
	op.Responses["200"] = core.ResponseContract{}
	_ = core.SetRequestOpenAPI(req, op)
	a = NewProjectSchemaAdapter(p)
	if err := a.PreflightSchema(ref); err != nil {
		t.Fatalf("an explicitly empty response contract should preflight: %v", err)
	}
	if issues, e = a.ValidateSchema([]byte(`{}`), ref); e != nil || len(issues) == 0 {
		t.Fatalf("a body against a no-content contract must fail: %+v %v", issues, e)
	}
	if issues, e = a.ValidateSchema(nil, ref); e != nil || len(issues) != 0 {
		t.Fatalf("an empty body against a no-content contract should pass: %+v %v", issues, e)
	}
}

func TestPreparedRunnerRunDoesNotReturnTypedNil(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{"collections/a/r.request.yaml": reqYAML("r", "GET", "/", "")})
	suite, err := ParseSuite(suiteYAML("steps:\n  - {id: r, request: r}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r := New(p, suite, Options{Executor: execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
		return fixtureResult(`{}`, 200), nil
	})})
	if err := r.Prepare(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := r.Prepare(context.Background()); err != nil {
		t.Fatalf("successful cached Prepare returned an error: %v", err)
	}
	result, err := r.Run(context.Background())
	if err != nil || result.Status != StatusPassed {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestRunnerOAuthAndProductionArePreflightErrors(t *testing.T) {
	for _, mode := range []string{"oauth2", "oidc", "production"} {
		t.Run(mode, func(t *testing.T) {
			p, _ := newFixtureProject(t, map[string]string{"collections/a/one.request.yaml": reqYAML("one", "GET", "/first", ""), "collections/a/two.request.yaml": reqYAML("two", "POST", "/second", "")})
			if mode != "production" {
				for _, e := range p.Requests {
					if e.Request.ID == "two" {
						e.Request.Auth = &core.AuthRef{Mode: "inline", Inline: &core.AuthScheme{Type: mode, OAuth: &core.OAuthConfig{GrantType: "client_credentials", ClientID: "fixture", TokenURL: "http://127.0.0.1/token"}}}
					}
				}
			}
			suite, err := ParseSuite(suiteYAML("steps:\n  - {id: one, request: one}\n  - {id: two, request: two, needs: [one]}\n"))
			if err != nil {
				t.Fatal(err)
			}
			opts := Options{Environment: "local"}
			if mode == "production" {
				opts.ExecuteOptions = core.ExecuteOptions{HasTargetPolicy: true, TargetPolicy: core.MergeTargetPolicy(&core.DeviceTargetPolicy{ProductionHosts: []string{"fixture.invalid"}}, nil)}
			}
			calls := 0
			opts.Executor = execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
				calls++
				return fixtureResult(`{}`, 200), nil
			})
			result, err := New(p, suite, opts).Run(context.Background())
			if err == nil || result.Status != StatusConfigError || calls != 0 {
				t.Fatalf("%+v %v calls=%d", result, err, calls)
			}
			if mode != "production" && !strings.Contains(result.Failure.Message, "OAuth/OIDC") {
				t.Fatal("missing explicit OAuth limitation", result.Failure)
			}
		})
	}
}

func TestSchemaAdapterStrictDocumentAndSecretMetadata(t *testing.T) {
	for _, schema := range []string{"type: string\n---\ntype: number", "type: object\ntype: string", "const: .nan", "title: 123", "type: number|integer", "type: [string, string]", "required: [id, id]"} {
		p, _ := newFixtureProject(t, map[string]string{})
		_ = p.Store.WriteAtomic("schema.yaml", []byte(schema))
		if NewProjectSchemaAdapter(p).PreflightSchema(SchemaRef{Ref: "schema.yaml"}) == nil {
			t.Fatalf("invalid configuration accepted: %s", schema)
		}
	}
	p, _ := newFixtureProject(t, map[string]string{})
	_ = p.Store.WriteAtomic("schema.yaml", []byte("const: schema-secret-value-719"))
	a := NewProjectSchemaAdapter(p)
	ref := SchemaRef{Ref: "schema.yaml"}
	issues, err := a.ValidateSchema([]byte(`"instance-secret-value-918"`), ref)
	if err != nil || len(issues) != 1 {
		t.Fatal(issues, err)
	}
	b, _ := json.Marshal(issues)
	if strings.Contains(string(b), "secret-value") {
		t.Fatal("schema or instance secret leaked")
	}
	digest := a.SchemaSnapshotDigest()
	_ = p.Store.WriteAtomic("schema.yaml", []byte("type: string"))
	bAdapter := NewProjectSchemaAdapter(p)
	if err := bAdapter.PreflightSchema(ref); err != nil {
		t.Fatal(err)
	}
	if digest == bAdapter.SchemaSnapshotDigest() {
		t.Fatal("schema change did not alter snapshot digest")
	}
}

func TestSchemaAdapterBoundsExpandedReferences(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{})
	b, _ := json.Marshal(map[string]any{"const": strings.Repeat("x", 1<<20)})
	_ = p.Store.WriteAtomic("large.json", b)
	_ = p.Store.WriteAtomic("root.yaml", []byte("type: object\nproperties:\n  a: {$ref: large.json}\n  b: {$ref: large.json}\n"))
	if err := NewProjectSchemaAdapter(p).PreflightSchema(SchemaRef{Ref: "root.yaml"}); err == nil {
		t.Fatal("expanded reference representation exceeded budget")
	}
}

func TestRunnerReportsMaskDatasetAndSavedMetadata(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{"collections/a/r.request.yaml": reqYAML("r", "GET", "/", "")})
	secret := "bound-secret-report-917"
	password := "dataset-password-private-712"
	p.File.Name = "Project " + secret
	for _, entry := range p.Requests {
		entry.Request.Name = "Request " + secret
	}
	_ = p.Store.WriteAtomic("data.json", []byte(`[{"password":"`+password+`","user":"`+secret+`"}]`))
	suite, err := ParseSuite(suiteYAML("dataset: {format: json, file: data.json}\nsteps:\n  - {id: r, request: r}\n"))
	if err != nil {
		t.Fatal(err)
	}
	opts := Options{Secrets: core.MapSecrets{"bound": secret}, Executor: execFunc(func(context.Context, *core.Plan, core.ExecuteOptions) (*core.ExecResult, error) {
		return fixtureResult(`{}`, 200), nil
	})}
	result, err := New(p, suite, opts).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0].Data["password"] != "***" || result.Rows[0].Data["user"] != "***" {
		t.Fatal("raw dataset in returned UI result")
	}
	for _, marshal := range []func(*RunResult) ([]byte, error){MarshalJSONReport, MarshalJUnitReport} {
		b, err := marshal(result)
		if err != nil || strings.Contains(string(b), secret) || strings.Contains(string(b), password) {
			t.Fatal("secret in report", err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	canceled, _ := New(p, suite, opts).Run(ctx)
	if strings.Contains(canceled.ProjectName, secret) {
		t.Fatal("canceled metadata leaked a bound secret")
	}
}

func TestSchemaAdapterDepthLimitIsConfigurationError(t *testing.T) {
	p, _ := newFixtureProject(t, map[string]string{})
	var node any = true
	for i := 0; i < core.DefaultMaxSchemaDepth; i++ {
		node = map[string]any{"not": node}
	}
	b, _ := json.Marshal(node)
	_ = p.Store.WriteAtomic("deep.json", b)
	if err := NewProjectSchemaAdapter(p).PreflightSchema(SchemaRef{Ref: "deep.json"}); err == nil {
		t.Fatal("engine-depth overflow deferred to response validation")
	}
}
