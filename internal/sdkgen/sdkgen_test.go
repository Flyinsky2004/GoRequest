package sdkgen

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const petSpec = `{
  "openapi": "3.1.2",
  "info": {"title": "宠物 API", "version": "1.2.3"},
  "paths": {
    "/pets/{petId}": {
      "get": {
        "operationId": "getPet",
        "summary": "Get a pet",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}},
          {"name": "limit", "in": "query", "schema": {"type": "integer"}},
          {"name": "tags", "in": "query", "schema": {"type": "array", "items": {"type": "string"}}},
          {"name": "X-Trace", "in": "header", "schema": {"type": "string"}}
        ],
        "responses": {"200": {"description": "ok"}}
      },
      "post": {
        "operationId": "updatePet",
        "parameters": [
          {"name": "petId", "in": "path", "required": true, "schema": {"type": "string"}}
        ],
        "requestBody": {"required": true, "content": {"application/json": {"schema": {"type": "object"}}}},
        "responses": {"200": {"description": "ok"}}
      }
    },
    "/search": {
      "get": {
        "parameters": [
          {"name": "q", "in": "query", "required": true, "schema": {"type": "string"}}
        ],
        "responses": {"200": {"description": "ok"}}
      }
    }
  }
}`

func testOptions(t *testing.T) Options {
	t.Helper()
	return Options{
		PackageName: "petapi",
		ModulePath:  "example.com/petapi",
		TargetDir:   filepath.Join(t.TempDir(), "sdk"),
	}
}

func fileByPath(t *testing.T, res *Result, rel string) File {
	t.Helper()
	for _, f := range res.Files {
		if f.RelPath == rel {
			return f
		}
	}
	t.Fatalf("generated file %q not found; have %v", rel, filePaths(res))
	return File{}
}

func filePaths(res *Result) []string {
	out := make([]string, 0, len(res.Files))
	for _, f := range res.Files {
		out = append(out, f.RelPath)
	}
	return out
}

func TestGenerateDeterministic(t *testing.T) {
	opts := testOptions(t)
	first, err := Generate([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	second, err := Generate([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Generate twice: %v", err)
	}
	if len(first.Files) != len(second.Files) {
		t.Fatalf("file count differs: %d vs %d", len(first.Files), len(second.Files))
	}
	for i := range first.Files {
		if first.Files[i].RelPath != second.Files[i].RelPath {
			t.Fatalf("file order differs at %d: %q vs %q", i, first.Files[i].RelPath, second.Files[i].RelPath)
		}
		if !bytes.Equal(first.Files[i].Content, second.Files[i].Content) {
			t.Fatalf("file %q is not byte-stable", first.Files[i].RelPath)
		}
	}
	if first.Manifest.SpecDigest != second.Manifest.SpecDigest {
		t.Fatalf("spec digest differs")
	}
	if first.Manifest.SpecDigest != digestBytes([]byte(petSpec)) {
		t.Fatalf("manifest spec digest does not match the raw snapshot")
	}
}

func TestGeneratedOperationsAndNaming(t *testing.T) {
	res, err := Generate([]byte(petSpec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	names := map[string]bool{}
	for _, op := range res.Operations {
		names[op.GoName] = true
	}
	for _, want := range []string{"GetPet", "UpdatePet", "GetSearch"} {
		if !names[want] {
			t.Fatalf("missing generated operation %q; have %v", want, names)
		}
	}
	ops := string(fileByPath(t, res, "operations.go").Content)
	for _, want := range []string{
		"type GetPetInput struct",
		"func (c *Client) GetPet(",
		"func (in *GetPetInput) Validate() error",
		"type UpdatePetInput struct",
		"Body json.RawMessage",
		"func (c *Client) GetSearch(",
	} {
		if !strings.Contains(ops, want) {
			t.Fatalf("operations.go missing %q", want)
		}
	}
	readme := string(fileByPath(t, res, "README.md").Content)
	if !strings.Contains(readme, "宠物 API") {
		t.Fatalf("README lost the Chinese title")
	}
	if !strings.Contains(readme, "| GET | `/pets/{petId}` | `GetPet` |") {
		t.Fatalf("README operation table missing GetPet:\n%s", readme)
	}
}

func TestOperationNameCollisionsAndKeywords(t *testing.T) {
	spec := `{
      "openapi": "3.2.1",
      "info": {"title": "x", "version": "1"},
      "paths": {
        "/a": {"get": {"operationId": "type", "responses": {"200": {"description": "ok"}}}},
        "/b": {"get": {"operationId": "Type", "responses": {"200": {"description": "ok"}}}},
        "/c": {"get": {"operationId": "1st", "responses": {"200": {"description": "ok"}}}},
        "/d": {"get": {"operationId": "Client", "responses": {"200": {"description": "ok"}}}}
      }
    }`
	res, err := Generate([]byte(spec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	seen := map[string]bool{}
	for _, op := range res.Operations {
		if seen[op.GoName] {
			t.Fatalf("duplicate Go name %q", op.GoName)
		}
		seen[op.GoName] = true
		if goKeywords[strings.ToLower(op.GoName)] {
			t.Fatalf("operation name %q is a Go keyword", op.GoName)
		}
	}
}

func TestUnsupportedOperationsBlockByDefault(t *testing.T) {
	spec := `{
      "openapi": "3.1.2",
      "info": {"title": "x", "version": "1"},
      "paths": {
        "/ok": {"get": {"operationId": "ok", "responses": {"200": {"description": "ok"}}}},
        "/upload": {"post": {
          "operationId": "upload",
          "requestBody": {"content": {"multipart/form-data": {"schema": {"type": "object"}}}},
          "responses": {"200": {"description": "ok"}}
        }}
      }
    }`
	_, err := Generate([]byte(spec), testOptions(t))
	if !errors.Is(err, ErrUnsupportedOperations) {
		t.Fatalf("expected ErrUnsupportedOperations, got %v", err)
	}
	opts := testOptions(t)
	opts.IncludeUnsupported = true
	res, err := Generate([]byte(spec), opts)
	if err != nil {
		t.Fatalf("Generate with subset: %v", err)
	}
	if len(res.Operations) != 1 || res.Operations[0].GoName != "Ok" {
		t.Fatalf("expected only the supported operation, got %+v", res.Operations)
	}
	if len(res.Unsupported) != 1 || !strings.Contains(res.Unsupported[0].Reason, "multipart") {
		t.Fatalf("expected a multipart unsupported reason, got %+v", res.Unsupported)
	}
	readme := string(fileByPath(t, res, "README.md").Content)
	if !strings.Contains(readme, "Not generated") || !strings.Contains(readme, "multipart/form-data") {
		t.Fatalf("README does not list the skipped operation:\n%s", readme)
	}
}

// specWith wraps one operation under /x.
func specWith(method, opJSON string) string {
	return `{"openapi":"3.1.2","info":{"title":"x","version":"1"},"paths":{"/x":{"` +
		method + `":` + opJSON + `}}}`
}

func TestUnsupportedReasonMatrix(t *testing.T) {
	cases := []struct {
		name string
		spec string
		want string
	}{
		{
			name: "multipart",
			spec: specWith("post", `{"requestBody":{"content":{"multipart/form-data":{"schema":{"type":"object"}}}},"responses":{"200":{"description":"ok"}}}`),
			want: "multipart/form-data",
		},
		{
			name: "urlencoded",
			spec: specWith("post", `{"requestBody":{"content":{"application/x-www-form-urlencoded":{"schema":{"type":"object"}}}},"responses":{"200":{"description":"ok"}}}`),
			want: "application/x-www-form-urlencoded",
		},
		{
			name: "multi-media",
			spec: specWith("post", `{"requestBody":{"content":{"application/json":{},"text/plain":{}}},"responses":{"200":{"description":"ok"}}}`),
			want: "multi-media",
		},
		{
			name: "remote-ref",
			spec: specWith("get", `{"parameters":[{"name":"id","in":"query","schema":{"$ref":"https://example.com/schema.json"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "not a local",
		},
		{
			name: "callbacks",
			spec: specWith("get", `{"callbacks":{"onEvent":{}},"responses":{"200":{"description":"ok"}}}`),
			want: "callbacks",
		},
		{
			name: "streaming",
			spec: specWith("post", `{"requestBody":{"content":{"application/json":{"itemSchema":{"type":"string"}}}},"responses":{"200":{"description":"ok"}}}`),
			want: "streaming",
		},
		{
			name: "security-and",
			spec: specWith("get", `{"security":[{"a":[],"b":[]}],"responses":{"200":{"description":"ok"}}}`),
			want: "AND",
		},
		{
			name: "managed-header",
			spec: specWith("get", `{"parameters":[{"name":"Content-Length","in":"header","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "managed header",
		},
		{
			name: "cookie-param",
			spec: specWith("get", `{"parameters":[{"name":"sid","in":"cookie","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "cookie",
		},
		{
			name: "deep-object",
			spec: specWith("get", `{"parameters":[{"name":"filter","in":"query","style":"deepObject","schema":{"type":"string"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "deepObject",
		},
		{
			name: "object-schema",
			spec: specWith("get", `{"parameters":[{"name":"o","in":"query","schema":{"type":"object"}}],"responses":{"200":{"description":"ok"}}}`),
			want: "not supported",
		},
		{
			name: "additional-operations",
			spec: `{"openapi":"3.2.1","info":{"title":"x","version":"1"},"paths":{"/x":{"additionalOperations":{"PURGE":{"responses":{"200":{"description":"ok"}}}}}}}`,
			want: "additionalOperations",
		},
		{
			name: "webhooks",
			spec: `{"openapi":"3.2.1","info":{"title":"x","version":"1"},"paths":{"/x":{"get":{"responses":{"200":{"description":"ok"}}}}},"webhooks":{"newPet":{"post":{"responses":{"200":{"description":"ok"}}}}}}`,
			want: "webhooks",
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

func TestQueryArrayStyles(t *testing.T) {
	spec := specWith("get", `{"parameters":[
      {"name":"a","in":"query","schema":{"type":"array","items":{"type":"integer"}}},
      {"name":"b","in":"query","style":"form","explode":false,"schema":{"type":"array","items":{"type":"string"}}},
      {"name":"c","in":"query","style":"spaceDelimited","schema":{"type":"array","items":{"type":"string"}}},
      {"name":"d","in":"query","style":"pipeDelimited","schema":{"type":"array","items":{"type":"string"}}}
    ],"responses":{"200":{"description":"ok"}}}`)
	res, err := Generate([]byte(spec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	ops := string(fileByPath(t, res, "operations.go").Content)
	for _, want := range []string{
		`q.Add("a", strconv.FormatInt(v, 10))`,
		`q.Set("b", strings.Join(vals, ","))`,
		`q.Set("c", strings.Join(vals, " "))`,
		`q.Set("d", strings.Join(vals, "|"))`,
	} {
		if !strings.Contains(ops, want) {
			t.Fatalf("operations.go missing %q:\n%s", want, ops)
		}
	}
}

func TestChinesePathAndDescription(t *testing.T) {
	spec := `{"openapi":"3.1.2","info":{"title":"中文 API","version":"1"},"paths":{"/用户/{编号}":{"get":{"summary":"获取用户","parameters":[{"name":"编号","in":"path","required":true,"schema":{"type":"string"}}],"responses":{"200":{"description":"成功"}}}}}}`
	res, err := Generate([]byte(spec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if len(res.Operations) != 1 {
		t.Fatalf("expected one operation, got %d", len(res.Operations))
	}
	if res.Operations[0].GoName == "" {
		t.Fatal("Chinese path produced an empty Go name")
	}
	ops := string(fileByPath(t, res, "operations.go").Content)
	if !strings.Contains(ops, "编号") {
		t.Fatalf("Chinese parameter name was lost:\n%s", ops)
	}
}

func TestSecretsAreNotEmbedded(t *testing.T) {
	const fakeSecret = "sk-test-FAKE-abcdef"
	spec := `{
      "openapi": "3.1.2",
      "info": {"title": "` + fakeSecret + `", "version": "1"},
      "paths": {
        "/x": {"get": {
          "operationId": "x",
          "summary": "` + fakeSecret + `",
          "parameters": [
            {"name": "X-Api-Key", "in": "header", "schema": {"type": "string"}, "example": "` + fakeSecret + `"}
          ],
          "responses": {"200": {"description": "` + fakeSecret + `"}}
        }}
      }
    }`
	res, err := Generate([]byte(spec), testOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range res.Files {
		if bytes.Contains(f.Content, []byte(fakeSecret)) {
			t.Fatalf("generated file %q contains the fake secret", f.RelPath)
		}
	}
	for _, d := range res.Diagnostics {
		if strings.Contains(d.Message, fakeSecret) {
			t.Fatalf("diagnostic contains the fake secret: %s", d.Message)
		}
	}
}

func TestPreviewApplyCreateAndUnchanged(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if len(plan.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan.Conflicts)
	}
	for _, a := range plan.Actions {
		if a.Kind != ActionCreate {
			t.Fatalf("expected create action, got %+v", a)
		}
	}
	res, err := Apply(plan)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(res.Created) != len(plan.Result.Files) {
		t.Fatalf("created %d files, expected %d", len(res.Created), len(plan.Result.Files))
	}
	for _, f := range plan.Result.Files {
		got, err := os.ReadFile(filepath.Join(opts.TargetDir, filepath.FromSlash(f.RelPath)))
		if err != nil {
			t.Fatalf("read %s: %v", f.RelPath, err)
		}
		if !bytes.Equal(got, f.Content) {
			t.Fatalf("written %s differs from generated content", f.RelPath)
		}
	}

	// Second preview: everything is unchanged.
	plan2, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview twice: %v", err)
	}
	for _, a := range plan2.Actions {
		if a.Kind != ActionUnchanged {
			t.Fatalf("expected unchanged action on re-generation, got %+v", a)
		}
	}
	if _, err := Apply(plan2); err != nil {
		t.Fatalf("Apply unchanged: %v", err)
	}
}

func TestUpdateRequiresOverwrite(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	changed := strings.Replace(petSpec, `"summary": "Get a pet"`, `"summary": "Get one pet"`, 1)
	plan2, err := Preview([]byte(changed), opts)
	if err != nil {
		t.Fatalf("Preview changed: %v", err)
	}
	var sawConflict bool
	for _, c := range plan2.Conflicts {
		if strings.Contains(c.Reason, "enable overwrite") {
			sawConflict = true
		}
	}
	if !sawConflict {
		t.Fatalf("expected an overwrite conflict, got %+v", plan2.Conflicts)
	}
	if _, err := Apply(plan2); !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply with conflicts should fail with ErrConflict, got %v", err)
	}

	opts.AllowOverwrite = true
	plan3, err := Preview([]byte(changed), opts)
	if err != nil {
		t.Fatalf("Preview with overwrite: %v", err)
	}
	if len(plan3.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts with overwrite: %+v", plan3.Conflicts)
	}
	res, err := Apply(plan3)
	if err != nil {
		t.Fatalf("Apply with overwrite: %v", err)
	}
	if len(res.Updated) == 0 {
		t.Fatalf("expected at least one updated file")
	}
}

func TestManualFileProtection(t *testing.T) {
	opts := testOptions(t)
	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manual := filepath.Join(opts.TargetDir, "client.go")
	if err := os.WriteFile(manual, []byte("package petapi\n// hand written\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	var found bool
	for _, c := range plan.Conflicts {
		if c.RelPath == "client.go" && strings.Contains(c.Reason, "manual file") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a manual-file conflict, got %+v", plan.Conflicts)
	}
	before, _ := os.ReadFile(manual)
	if _, err := Apply(plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply should refuse, got %v", err)
	}
	after, _ := os.ReadFile(manual)
	if !bytes.Equal(before, after) {
		t.Fatalf("manual file was modified")
	}
}

func TestModifiedGeneratedFileBlocks(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	target := filepath.Join(opts.TargetDir, "client.go")
	if err := os.WriteFile(target, []byte("package petapi\n// tampered\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	plan2, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview after tamper: %v", err)
	}
	var found bool
	for _, c := range plan2.Conflicts {
		if c.RelPath == "client.go" && strings.Contains(c.Reason, "modified manually") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a modified-file conflict, got %+v", plan2.Conflicts)
	}
}

func TestStaleDeletion(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	// Register an obsolete generated file in the existing manifest.
	obsolete := []byte("package petapi\n// obsolete\n")
	if err := os.WriteFile(filepath.Join(opts.TargetDir, "obsolete.go"), obsolete, 0o644); err != nil {
		t.Fatal(err)
	}
	appendManifestEntry(t, opts.TargetDir, ManifestEntry{
		Path: "obsolete.go", Digest: digestBytes(obsolete), Kind: "source",
	})

	// Without stale deletion the file is reported and blocks Apply.
	plan2, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview with stale: %v", err)
	}
	var staleFound, conflictFound bool
	for _, s := range plan2.Stale {
		if s.RelPath == "obsolete.go" {
			staleFound = true
		}
	}
	for _, c := range plan2.Conflicts {
		if c.RelPath == "obsolete.go" {
			conflictFound = true
		}
	}
	if !staleFound || !conflictFound {
		t.Fatalf("expected obsolete.go to be stale and blocking, got stale=%+v conflicts=%+v", plan2.Stale, plan2.Conflicts)
	}
	if _, err := Apply(plan2); !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply should refuse while a stale file remains, got %v", err)
	}

	// With stale deletion enabled the file is removed and the manifest updated.
	opts.AllowDeleteStale = true
	plan3, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview with stale deletion: %v", err)
	}
	if len(plan3.Conflicts) != 0 {
		t.Fatalf("unexpected conflicts: %+v", plan3.Conflicts)
	}
	res, err := Apply(plan3)
	if err != nil {
		t.Fatalf("Apply with stale deletion: %v", err)
	}
	if len(res.Deleted) != 1 || res.Deleted[0] != "obsolete.go" {
		t.Fatalf("expected obsolete.go to be deleted, got %+v", res.Deleted)
	}
	if _, err := os.Stat(filepath.Join(opts.TargetDir, "obsolete.go")); !os.IsNotExist(err) {
		t.Fatalf("obsolete.go still exists")
	}
}

func TestPreviewAfterChangeRejected(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// A file appears after preview but before apply.
	if err := os.WriteFile(filepath.Join(opts.TargetDir, "client.go"), []byte("late"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(plan); !errors.Is(err, ErrConflict) {
		t.Fatalf("Apply should reject a file that appeared after preview, got %v", err)
	}
}

func TestMaliciousManifestRejected(t *testing.T) {
	opts := testOptions(t)
	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := Manifest{
		GeneratorVersion: GeneratorVersion,
		TemplateVersion:  TemplateVersion,
		SpecDigest:       "x",
		PackageName:      "petapi",
		ModulePath:       "example.com/petapi",
		Files: []ManifestEntry{
			{Path: "../../etc/passwd", Digest: "x", Kind: "source"},
		},
	}
	data, _ := json.Marshal(bad)
	if err := os.WriteFile(filepath.Join(opts.TargetDir, ManifestName), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Preview([]byte(petSpec), opts); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("expected ErrInvalidManifest, got %v", err)
	}
}

func TestSymlinkTargetRejected(t *testing.T) {
	opts := testOptions(t)
	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.go")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(opts.TargetDir, "client.go")); err != nil {
		t.Fatal(err)
	}
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	var found bool
	for _, c := range plan.Conflicts {
		if c.RelPath == "client.go" && strings.Contains(c.Reason, "symbolic link") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected a symlink conflict, got %+v", plan.Conflicts)
	}
}

func TestProjectInternalTargetRejected(t *testing.T) {
	root := t.TempDir()
	opts := Options{
		PackageName: "petapi",
		ModulePath:  "example.com/petapi",
		TargetDir:   filepath.Join(root, "sdk"),
		ProjectRoot: root,
	}
	_, err := Preview([]byte(petSpec), opts)
	if !errors.Is(err, ErrUnsafeTarget) {
		t.Fatalf("expected ErrUnsafeTarget, got %v", err)
	}
}

func TestRollbackOnMidWriteFailure(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	written := 0
	plan.beforeWrite = func(rel string) error {
		written++
		if written == 2 {
			return errors.New("injected write failure")
		}
		return nil
	}
	if _, err := Apply(plan); err == nil {
		t.Fatalf("expected an injected failure")
	}
	entries, err := os.ReadDir(opts.TargetDir)
	if err != nil {
		t.Fatalf("read target dir: %v", err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".sdkgen-tmp-") {
			t.Fatalf("temporary file left behind: %s", e.Name())
		}
	}
	if _, err := os.Stat(filepath.Join(opts.TargetDir, "README.md")); !os.IsNotExist(err) {
		t.Fatalf("first written file was not rolled back")
	}
}

func TestApplyMatchesManifestDirectoryDigest(t *testing.T) {
	opts := testOptions(t)
	plan, err := Preview([]byte(petSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	m, _, err := readManifest(opts.TargetDir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if m == nil {
		t.Fatal("manifest missing after apply")
	}
	for _, e := range m.Files {
		data, err := os.ReadFile(filepath.Join(opts.TargetDir, filepath.FromSlash(e.Path)))
		if err != nil {
			t.Fatalf("manifest file %s missing: %v", e.Path, err)
		}
		if got := digestBytes(data); got != e.Digest {
			t.Fatalf("manifest digest mismatch for %s: %s vs %s", e.Path, got, e.Digest)
		}
	}
	entries, err := os.ReadDir(opts.TargetDir)
	if err != nil {
		t.Fatalf("read target dir: %v", err)
	}
	if len(entries) != len(m.Files)+1 {
		t.Fatalf("target has %d entries, manifest lists %d files (+manifest)", len(entries), len(m.Files))
	}
}

func TestLimits(t *testing.T) {
	opts := testOptions(t)
	opts.MaxFiles = 1
	if _, err := Generate([]byte(petSpec), opts); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded, got %v", err)
	}
	opts = testOptions(t)
	opts.MaxTotalBytes = 10
	if _, err := Generate([]byte(petSpec), opts); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("expected ErrLimitExceeded for bytes, got %v", err)
	}
}

func TestInvalidOptions(t *testing.T) {
	opts := testOptions(t)
	opts.PackageName = "main"
	if _, err := Generate([]byte(petSpec), opts); err == nil {
		t.Fatalf("expected an error for package name main")
	}
	opts = testOptions(t)
	opts.ModulePath = "../escape"
	if _, err := Generate([]byte(petSpec), opts); err == nil {
		t.Fatalf("expected an error for a relative module path")
	}
}

func appendManifestEntry(t *testing.T, dir string, entry ManifestEntry) {
	t.Helper()
	m, _, err := readManifest(dir)
	if err != nil {
		t.Fatalf("read manifest: %v", err)
	}
	if m == nil {
		t.Fatalf("manifest is missing")
	}
	m.Files = append(m.Files, entry)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ManifestName), append(data, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}
}
