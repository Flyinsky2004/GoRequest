package sdkgen

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

// richSpec exercises objects, required/nullable/optional, enums, arrays,
// additionalProperties, local refs, recursion, allOf, discriminator unions,
// multi-status responses and CJK names.
const richSpec = `{
  "openapi": "3.1.2",
  "info": {"title": "宠物 API", "version": "2.0.0"},
  "components": {
    "schemas": {
      "Owner": {
        "type": "object",
        "required": ["name"],
        "properties": {
          "name": {"type": "string"},
          "address": {"type": "object", "properties": {"city": {"type": "string"}}}
        }
      },
      "Pet": {
        "type": "object",
        "required": ["id", "name", "nickname"],
        "properties": {
          "id": {"type": "string"},
          "name": {"type": "string"},
          "type": {"type": "string"},
          "additionalProperties": {"type": "string"},
          "tag": {"type": ["string", "null"]},
          "nickname": {"type": ["string", "null"]},
          "status": {"type": "string", "enum": ["available", "pending", "sold"]},
          "age": {"type": "integer"},
          "metadata": {"type": "object", "additionalProperties": {"type": "string"}},
          "owner": {"$ref": "#/components/schemas/Owner"},
          "friends": {"type": "array", "items": {"$ref": "#/components/schemas/Pet"}}
        }
      },
      "Cat": {
        "type": "object",
        "required": ["kind"],
        "properties": {"kind": {"type": "string", "enum": ["cat"]}, "meow": {"type": "boolean"}}
      },
      "Dog": {
        "type": "object",
        "required": ["kind"],
        "properties": {"kind": {"type": "string", "enum": ["dog"]}, "bark": {"type": "boolean"}}
      },
      "PetUnion": {
        "oneOf": [
          {"$ref": "#/components/schemas/Cat"},
          {"$ref": "#/components/schemas/Dog"}
        ],
        "discriminator": {
          "propertyName": "kind",
          "mapping": {"cat": "#/components/schemas/Cat", "dog": "#/components/schemas/Dog"}
        }
      },
      "APIErrorBody": {
        "type": "object",
        "required": ["code"],
        "properties": {"code": {"type": "integer"}, "message": {"type": "string"}}
      },
      "BaseAudit": {
        "type": "object",
        "required": ["createdAt"],
        "properties": {"createdAt": {"type": "string"}}
      },
      "PetAudit": {
        "allOf": [
          {"$ref": "#/components/schemas/BaseAudit"},
          {"type": "object", "required": ["updatedAt"], "properties": {"updatedAt": {"type": "string"}, "note": {"type": "string"}}}
        ]
      },
      "Tags": {"type": "array", "items": {"type": "string"}},
      "ClosedThing": {
        "type": "object",
        "additionalProperties": false,
        "required": ["name"],
        "properties": {"name": {"type": "string"}}
      }
    }
  },
  "paths": {
    "/pets/{petId}": {
      "get": {
        "operationId": "getPet",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "tags", "in": "query", "schema": {"type": "array", "items": {"type": "string"}}},
          {"name": "status", "in": "query", "schema": {"type": "string", "enum": ["available", "sold"]}},
          {"name": "X-Trace", "in": "header", "schema": {"type": "string"}}
        ],
        "responses": {
          "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}},
          "404": {"description": "missing", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/APIErrorBody"}}}},
          "default": {"description": "fallback", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/APIErrorBody"}}}}
        }
      },
      "put": {
        "operationId": "updatePet",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}},
        "responses": {
          "200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}},
          "422": {"description": "invalid", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/APIErrorBody"}}}}
        }
      }
    },
    "/pets": {
      "post": {
        "operationId": "createPet",
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"$ref": "#/components/schemas/PetUnion"}}}},
        "responses": {"201": {"description": "created", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Pet"}}}}}
      }
    },
    "/closed": {
      "get": {
        "operationId": "getClosed",
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/ClosedThing"}}}}}
      }
    },
    "/tags": {
      "get": {
        "operationId": "listTags",
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/Tags"}}}}}
      }
    },
    "/audit": {
      "get": {
        "operationId": "getAudit",
        "responses": {"200": {"description": "ok", "content": {"application/json": {"schema": {"$ref": "#/components/schemas/PetAudit"}}}}}
      }
    },
    "/用户/{编号}": {
      "get": {
        "operationId": "get用户",
        "parameters": [{"name": "编号", "in": "path", "required": true, "schema": {"type": "string"}}],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func richOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		PackageName: "petapi",
		ModulePath:  "example.com/petapi",
		TargetDir:   filepath.Join(t.TempDir(), "sdk"),
	}
}

func modelByName(t *testing.T, res *Result, name string) ModelSummary {
	t.Helper()
	for _, m := range res.Models {
		if m.Name == name {
			return m
		}
	}
	t.Fatalf("model %q not found; have %+v", name, res.Models)
	return ModelSummary{}
}

func TestRichContractModels(t *testing.T) {
	res, err := Generate([]byte(richSpec), richOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, want := range []struct{ name, kind string }{
		{"Pet", "object"},
		{"Owner", "object"},
		{"Cat", "object"},
		{"Dog", "object"},
		{"PetUnion", "union"},
		{"APIErrorBody", "object"},
		{"PetAudit", "object"},
		{"Tags", "alias"},
	} {
		got := modelByName(t, res, want.name)
		if got.Kind != want.kind {
			t.Fatalf("model %s kind = %q, want %q", want.name, got.Kind, want.kind)
		}
	}

	models := string(fileByPath(t, res, "models.go").Content)
	for _, want := range []string{
		"type Pet struct",
		"Tag Optional[string]",
		"Nickname Optional[string]",
		"Friends Optional[[]Pet]",
		"Owner Optional[*Owner]",
		"AdditionalProperties map[string]json.RawMessage",
		"Status Optional[PetStatus]",
		"func (m Pet) MarshalJSON()",
		"func (m *Pet) UnmarshalJSON(",
		"func (m *Pet) Validate() error",
		"type PetUnion struct",
		"case \"cat\":",
		"type PetAudit struct",
		"UpdatedAt string",
		"CreatedAt string",
		"type Tags []string",
	} {
		if !strings.Contains(models, want) {
			t.Fatalf("models.go missing %q:\n%s", want, models)
		}
	}

	ops := string(fileByPath(t, res, "operations.go").Content)
	for _, want := range []string{
		"func (c *Client) GetPetTyped(",
		`apiErr.Typed = &value`,
		"func (in *UpdatePetInput) WithBody(v *Pet)",
	} {
		if !strings.Contains(ops, want) {
			t.Fatalf("operations.go missing %q:\n%s", want, ops)
		}
	}
	for _, pattern := range []string{
		`Status200\s+\*Pet`,
		`Status404\s+\*APIErrorBody`,
		`StatusDefault\s+\*APIErrorBody`,
	} {
		if !regexp.MustCompile(pattern).MatchString(ops) {
			t.Fatalf("operations.go missing typed field %q:\n%s", pattern, ops)
		}
	}
	if !strings.Contains(models, "AdditionalPropertiesParam") {
		t.Fatalf("reserved property name was not renamed:\n%s", models)
	}
}

func TestGeneratedRichModuleCompilesAndRoundTrips(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain is not available")
	}
	opts := richOptions(t)
	plan, err := Preview([]byte(richSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	testSrc := `package petapi_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	petapi "example.com/petapi"
)

func ptr[T any](v T) *T { return &v }

func TestOptionalNullAndAbsent(t *testing.T) {
	var p petapi.Pet
	if err := json.Unmarshal([]byte(` + "`" + `{"id":"1","name":"n","nickname":null}` + "`" + `), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Nickname.IsSet() || !p.Nickname.IsNull() {
		t.Fatalf("nickname = set %v null %v", p.Nickname.IsSet(), p.Nickname.IsNull())
	}
	if p.Tag.IsSet() {
		t.Fatalf("absent tag reported as set")
	}
	// Unset optional fields are omitted; explicit null is emitted.
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if _, ok := got["tag"]; ok {
		t.Fatalf("unset optional tag was serialized: %s", out)
	}
	if string(got["nickname"]) != "null" {
		t.Fatalf("explicit null nickname = %s", got["nickname"])
	}
	// A present zero value is emitted.
	p.Tag.Set("")
	out, _ = json.Marshal(p)
	_ = json.Unmarshal(out, &got)
	if _, ok := got["tag"]; !ok {
		t.Fatalf("present zero tag was omitted: %s", out)
	}
}

func TestRecursiveAndAdditionalProperties(t *testing.T) {
	src := ` + "`" + `{"id":"1","name":"root","nickname":"r","metadata":{"a":"b"},"extra":{"n":1},
	          "friends":[{"id":"2","name":"child","nickname":null,"extra":"kept"}]}` + "`" + `
	var p petapi.Pet
	if err := json.Unmarshal([]byte(src), &p); err != nil {
		t.Fatal(err)
	}
	friends, ok := p.Friends.Get()
	if !ok || len(friends) != 1 || friends[0].Name != "child" {
		t.Fatalf("friends = %+v", p.Friends)
	}
	if string(p.AdditionalProperties["extra"]) != ` + "`" + `{"n":1}` + "`" + ` {
		t.Fatalf("additional = %s", p.AdditionalProperties["extra"])
	}
	if v, ok := p.Metadata.Get(); !ok || v["a"] != "b" {
		t.Fatalf("metadata = %v %v", v, ok)
	}
	out, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var round map[string]any
	if err := json.Unmarshal(out, &round); err != nil {
		t.Fatal(err)
	}
	if round["extra"] == nil {
		t.Fatalf("additional member lost: %s", out)
	}
}

func TestUnionDiscriminator(t *testing.T) {
	var u petapi.PetUnion
	if err := json.Unmarshal([]byte(` + "`" + `{"kind":"cat","meow":true}` + "`" + `), &u); err != nil {
		t.Fatal(err)
	}
	if u.Cat == nil || u.Dog != nil {
		t.Fatalf("union decoded wrong: %+v", u)
	}
	if meow, ok := u.Cat.Meow.Get(); !ok || !meow {
		t.Fatalf("union decoded wrong: %+v", u)
	}
	out, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != ` + "`" + `{"kind":"cat","meow":true}` + "`" + ` {
		t.Fatalf("union encoded %s", out)
	}
	if err := json.Unmarshal([]byte(` + "`" + `{"kind":"bird"}` + "`" + `), &u); err == nil {
		t.Fatal("expected an unknown discriminator error")
	}
	var both petapi.PetUnion
	both.Cat = &petapi.Cat{}
	both.Dog = &petapi.Dog{}
	if _, err := json.Marshal(both); err == nil {
		t.Fatal("expected exactly-one-variant error")
	}
}

func TestAllOfAndEnum(t *testing.T) {
	var a petapi.PetAudit
	if err := json.Unmarshal([]byte(` + "`" + `{"createdAt":"c","updatedAt":"u","note":"n"}` + "`" + `), &a); err != nil {
		t.Fatal(err)
	}
	note, ok := a.Note.Get()
	if a.CreatedAt != "c" || a.UpdatedAt != "u" || !ok || note != "n" {
		t.Fatalf("allOf fields = %+v", a)
	}
	if petapi.PetStatusAvailable != "available" || petapi.PetStatusSold != "sold" {
		t.Fatalf("enum constants missing")
	}
}

func TestTypedResponseAndError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/pets/missing" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(` + "`" + `{"code":7,"message":"gone"}` + "`" + `))
			return
		}
		_, _ = w.Write([]byte(` + "`" + `{"id":"1","name":"n","nickname":null}` + "`" + `))
	}))
	defer srv.Close()
	c, err := petapi.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	res, err := c.GetPetTyped(context.Background(), &petapi.GetPetInput{PetId: ptr("1")})
	if err != nil {
		t.Fatal(err)
	}
	if res.Status200 == nil || res.Status200.Name != "n" {
		t.Fatalf("typed success = %+v", res)
	}
	res, err = c.GetPetTyped(context.Background(), &petapi.GetPetInput{PetId: ptr("missing")})
	if err == nil {
		t.Fatal("expected APIError")
	}
	var apiErr *petapi.APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error is not APIError: %v", err)
	}
	if res.Status404 == nil || res.Status404.Code != 7 {
		t.Fatalf("typed error = %+v", res)
	}
	if apiErr.Typed == nil {
		t.Fatalf("APIError.Typed not set")
	}
}

func TestBodyAndValidation(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got = string(b)
		_, _ = w.Write([]byte(` + "`" + `{"id":"1","name":"n","nickname":null}` + "`" + `))
	}))
	defer srv.Close()
	c, _ := petapi.NewClient(srv.URL)
	in := &petapi.UpdatePetInput{PetId: ptr("1")}
	if _, err := in.WithBody(&petapi.Pet{Id: "x"}); err == nil {
		t.Fatal("expected required-field validation")
	}
	pet := &petapi.Pet{Id: "x", Name: "n"}
	pet.Nickname.SetNull()
	if _, err := in.WithBody(pet); err != nil {
		t.Fatal(err)
	}
	if _, err := c.UpdatePet(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(got, ` + "`" + `"nickname":null` + "`" + `) || !strings.Contains(got, ` + "`" + `"id":"x"` + "`" + `) {
		t.Fatalf("body = %s", got)
	}
}
`
	if err := os.WriteFile(filepath.Join(opts.TargetDir, "client_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	runGeneratedGoTest(t, opts.TargetDir)
}

func runGeneratedGoTest(t *testing.T, dir string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "go", "test", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GOWORK=off", "GOPROXY=off", "GOSUMDB=off", "GOTOOLCHAIN=local",
		"GOFLAGS=-mod=mod", "CGO_ENABLED=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("generated module failed: %v\n%s", err, out)
	}
	if len(out) > 0 {
		t.Logf("generated module output:\n%s", out)
	}
}

func TestNullableKeywordRejectedIn31(t *testing.T) {
	spec := specWith("get", `{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"x":{"type":"string","nullable":true}}}}}}}}`)
	_, err := Generate([]byte(spec), testOptions(t))
	if !errors.Is(err, ErrUnsupportedOperations) || !strings.Contains(err.Error(), "nullable") {
		t.Fatalf("expected nullable rejection, got %v", err)
	}
}

func TestCompositionFailures(t *testing.T) {
	cases := []struct{ name, spec, want string }{
		{
			name: "oneof-without-discriminator",
			spec: specWith("get", `{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"oneOf":[{"type":"object","properties":{"a":{"type":"string"}}},{"type":"object","properties":{"b":{"type":"string"}}}]}}}}}}`),
			want: "discriminator",
		},
		{
			name: "allof-nonobject",
			spec: `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"allOf":[{"type":"string"}]}}}}}}}}}`,
			want: "allOf",
		},
		{
			name: "remote-schema-ref",
			spec: `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"https://example.com/a.json"}}}}}}}}}`,
			want: "local component",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Generate([]byte(tc.spec), testOptions(t))
			if !errors.Is(err, ErrUnsupportedOperations) {
				t.Fatalf("expected ErrUnsupportedOperations, got %v", err)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestRecursiveComponentTerminates(t *testing.T) {
	spec := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},
	  "components":{"schemas":{
	    "A":{"type":"object","properties":{"b":{"$ref":"#/components/schemas/B"}}},
	    "B":{"type":"object","properties":{"a":{"$ref":"#/components/schemas/A"}}}}},
	  "paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/A"}}}}}}}}}`
	done := make(chan struct{})
	var res *Result
	var err error
	go func() {
		defer close(done)
		res, err = Generate([]byte(spec), testOptions(t))
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("recursive schema generation did not terminate")
	}
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if modelByName(t, res, "A").Kind != "object" || modelByName(t, res, "B").Kind != "object" {
		t.Fatalf("recursive models missing: %+v", res.Models)
	}
}

func TestStableModelOutputAcrossRuns(t *testing.T) {
	opts := richOptions(t)
	first, err := Generate([]byte(richSpec), opts)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Generate([]byte(richSpec), opts)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Models) != len(second.Models) {
		t.Fatalf("model count differs")
	}
	for i := range first.Models {
		if first.Models[i] != second.Models[i] {
			t.Fatalf("model order/content differs at %d: %+v vs %+v", i, first.Models[i], second.Models[i])
		}
	}
	for i := range first.Files {
		if !bytes.Equal(first.Files[i].Content, second.Files[i].Content) {
			t.Fatalf("file %s is not deterministic", first.Files[i].RelPath)
		}
	}
}

func TestDuplicateParametersRejected(t *testing.T) {
	cases := []struct{ name, spec, want string }{
		{
			name: "duplicate-header-case",
			spec: specWith("get", `{"parameters":[{"name":"X-Id","in":"header","schema":{"type":"string"}},{"name":"x-id","in":"header","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "duplicate header parameter",
		},
		{
			name: "duplicate-query",
			spec: specWith("get", `{"parameters":[{"name":"q","in":"query","schema":{"type":"string"}},{"name":"q","in":"query","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "duplicate query parameter",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Generate([]byte(tc.spec), testOptions(t))
			if !errors.Is(err, ErrUnsupportedOperations) || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected %q rejection, got %v", tc.want, err)
			}
		})
	}
}

func TestAllOfConflictingTypesRejected(t *testing.T) {
	spec := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},
	  "components":{"schemas":{
	    "A":{"type":"object","properties":{"x":{"type":"string"}}},
	    "B":{"type":"object","properties":{"x":{"type":"integer"}}},
	    "C":{"allOf":[{"$ref":"#/components/schemas/A"},{"$ref":"#/components/schemas/B"}]}}},
	  "paths":{"/x":{"get":{"responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/C"}}}}}}}}}`
	_, err := Generate([]byte(spec), testOptions(t))
	if !errors.Is(err, ErrUnsupportedOperations) || !strings.Contains(err.Error(), "conflicting types") {
		t.Fatalf("expected an allOf conflict, got %v", err)
	}
}

func TestCJKModelAndFieldNames(t *testing.T) {
	spec := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},
	  "components":{"schemas":{"元数据":{"type":"object","additionalProperties":false,
	    "required":["名称"],
	    "properties":{"名称":{"type":"string"},"类型":{"type":"string"}}}}},
	  "paths":{"/元数据":{"get":{"operationId":"获取元数据","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"$ref":"#/components/schemas/元数据"}}}}}}}}}`
	res, err := Generate([]byte(spec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if modelByName(t, res, "元数据").Kind != "object" {
		t.Fatalf("CJK model missing: %+v", res.Models)
	}
	models := string(fileByPath(t, res, "models.go").Content)
	for _, want := range []string{
		"type 元数据 struct",
		"名称 string",
		"类型 Optional[string]",
		`fields["类型"]`,
	} {
		if !strings.Contains(models, want) {
			t.Fatalf("CJK identifiers lost (%q missing):\n%s", want, models)
		}
	}
}

func TestPathLevelParameterOverriddenByOperation(t *testing.T) {
	spec := `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"parameters":[{"name":"q","in":"query","required":false,"schema":{"type":"string"}}],"get":{"parameters":[{"name":"q","in":"query","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}}}}`
	res, err := Generate([]byte(spec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ops := string(fileByPath(t, res, "operations.go").Content)
	if !strings.Contains(ops, "Q *string") || strings.Contains(ops, "Q2") {
		t.Fatalf("operation-level parameter did not override the path-level one:\n%s", ops)
	}
}
