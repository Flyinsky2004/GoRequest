package sdkgen

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func tsOptions(t *testing.T) Options {
	opts := richOptions(t)
	opts.Language = LanguageTypeScript
	return opts
}

func tsFileByPath(t *testing.T, res *Result, rel string) File {
	t.Helper()
	for _, f := range res.Files {
		if f.RelPath == rel {
			return f
		}
	}
	t.Fatalf("generated file %q not found; have %v", rel, filePaths(res))
	return File{}
}

func TestTypeScriptGeneration(t *testing.T) {
	res, err := Generate([]byte(richSpec), tsOptions(t))
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if res.Language != string(LanguageTypeScript) {
		t.Fatalf("language = %q", res.Language)
	}
	for _, rel := range []string{"types.ts", "client.ts", "errors.ts", "operations.ts", "index.ts", "package.json", "tsconfig.json", "verify.mjs", "README.md", ManifestName} {
		tsFileByPath(t, res, rel)
	}
	types := string(tsFileByPath(t, res, "types.ts").Content)
	for _, want := range []string{
		"export interface ClosedThing {",
		"export type Pet = {",
		"tag?: string | null;",
		"nickname: string | null;",
		"friends?: Array<Pet>;",
		"owner?: Owner;",
		"metadata?: Record<string, string>;",
		`export type PetUnion = (Cat & { kind: "cat" }) | (Dog & { kind: "dog" });`,
		"export const PetStatus = {",
		"export type Tags = Array<string>;",
		"[key: string]: unknown",
	} {
		if !strings.Contains(types, want) && !(want == "[key: string]: unknown") {
			t.Fatalf("types.ts missing %q:\n%s", want, types)
		}
	}
	if !strings.Contains(types, "Record<string, unknown>") {
		t.Fatalf("additionalProperties intersection missing:\n%s", types)
	}
	ops := string(tsFileByPath(t, res, "operations.ts").Content)
	for _, want := range []string{
		"export async function getPet(",
		"encodePathValue(String(input.petId))",
		"query.push({ name: \"tags\", value: String(value) });",
		"headers[\"X-Trace\"] = String(input.xTrace);",
		"throw new ApiError(raw.status, raw.headers, raw.body, data);",
		"client.decodeJSON<Pet>(raw)",
	} {
		if !strings.Contains(ops, want) {
			t.Fatalf("operations.ts missing %q:\n%s", want, ops)
		}
	}
	client := string(tsFileByPath(t, res, "client.ts").Content)
	for _, want := range []string{"readBounded", "AbortController", "TimeoutError", "ResponseTooLargeError", "never retries", "encodePathValue"} {
		if !strings.Contains(client, want) {
			t.Fatalf("client.ts missing %q", want)
		}
	}
	manifest := string(tsFileByPath(t, res, ManifestName).Content)
	if !strings.Contains(manifest, `"language": "typescript"`) || !strings.Contains(manifest, TemplateVersionTypeScript) {
		t.Fatalf("manifest missing language/template:\n%s", manifest)
	}
	pkg := string(tsFileByPath(t, res, "package.json").Content)
	if !strings.Contains(pkg, `"type": "module"`) || strings.Contains(pkg, "dependencies") {
		t.Fatalf("package.json is wrong:\n%s", pkg)
	}

	second, err := Generate([]byte(richSpec), tsOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	for i := range res.Files {
		if !bytes.Equal(res.Files[i].Content, second.Files[i].Content) {
			t.Fatalf("TypeScript output %s is not deterministic", res.Files[i].RelPath)
		}
	}
}

func TestTypeScriptSecretsNotEmbedded(t *testing.T) {
	const fakeSecret = "sk-test-FAKE-abcdef"
	spec := strings.Replace(petSpec, "宠物 API", fakeSecret, 1)
	spec = strings.Replace(spec, `"summary": "Get a pet"`, `"summary": "`+fakeSecret+`"`, 1)
	opts := testOptions(t)
	opts.Language = LanguageTypeScript
	res, err := Generate([]byte(spec), opts)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	for _, f := range res.Files {
		if bytes.Contains(f.Content, []byte(fakeSecret)) {
			t.Fatalf("generated file %q contains the fake secret", f.RelPath)
		}
	}
}

func TestCrossLanguageManifestConflict(t *testing.T) {
	opts := richOptions(t)
	plan, err := Preview([]byte(richSpec), opts)
	if err != nil {
		t.Fatalf("Preview Go: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply Go: %v", err)
	}

	tsOpts := opts
	tsOpts.Language = LanguageTypeScript
	if _, err := Preview([]byte(richSpec), tsOpts); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected a cross-language conflict, got %v", err)
	}
	tsOpts.TargetDir = filepath.Join(t.TempDir(), "ts-sdk")
	tsPlan, err := Preview([]byte(richSpec), tsOpts)
	if err != nil {
		t.Fatalf("Preview TS in another dir: %v", err)
	}
	if _, err := Apply(tsPlan); err != nil {
		t.Fatalf("Apply TS: %v", err)
	}
	// The Go SDK is untouched.
	if _, err := os.Stat(filepath.Join(opts.TargetDir, "client.go")); err != nil {
		t.Fatalf("Go SDK disappeared: %v", err)
	}
	// Generating Go again into the TS directory is refused.
	opts.TargetDir = tsOpts.TargetDir
	if _, err := Preview([]byte(richSpec), opts); !errors.Is(err, ErrConflict) {
		t.Fatalf("expected conflict for Go into a TypeScript dir, got %v", err)
	}
}

func TestIncompatibleManifestVersionRejected(t *testing.T) {
	opts := richOptions(t)
	if err := os.MkdirAll(opts.TargetDir, 0o755); err != nil {
		t.Fatal(err)
	}
	bad := `{"generatorVersion":"sdkgen-go/1","templateVersion":"1","specDigest":"x","packageName":"petapi","modulePath":"example.com/petapi","files":[]}`
	if err := os.WriteFile(filepath.Join(opts.TargetDir, ManifestName), []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Preview([]byte(richSpec), opts); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("expected ErrInvalidManifest, got %v", err)
	}
}

func nodeVersion(t *testing.T) string {
	t.Helper()
	out, err := exec.Command("node", "--version").Output()
	if err != nil {
		t.Skip("node is not installed")
	}
	return strings.TrimSpace(string(out))
}

func nodeSupportsTS(t *testing.T) bool {
	t.Helper()
	ver := nodeVersion(t)
	major, minor := 0, 0
	fmt.Sscanf(strings.TrimPrefix(ver, "v"), "%d.%d", &major, &minor)
	if major > 22 || (major == 22 && minor >= 6) {
		return true
	}
	t.Skipf("node %s does not support type stripping", ver)
	return false
}

// TestTypeScriptRuntimeLoopback runs the generated TypeScript SDK under Node's
// type stripping against an in-process Go server. It verifies the same
// query/JSON/auth/error/cancel/timeout/size semantics as the Go backend.
func TestTypeScriptRuntimeLoopback(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	nodeSupportsTS(t)

	type record struct {
		Method string
		Path   string
		Query  string
		Trace  string
		Auth   string
		Body   string
	}
	var mu sync.Mutex
	var records []record
	var hits int32

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		records = append(records, record{
			Method: r.Method,
			Path:   r.URL.EscapedPath(),
			Query:  r.URL.RawQuery,
			Trace:  r.Header.Get("X-Trace"),
			Auth:   r.Header.Get("Authorization"),
			Body:   string(b),
		})
		mu.Unlock()
		if r.URL.Path == "/pets/slow" {
			time.Sleep(300 * time.Millisecond)
		}
		if r.URL.Path == "/pets/missing" {
			w.WriteHeader(404)
			_, _ = w.Write([]byte(`{"code":7,"message":"gone"}`))
			return
		}
		if r.URL.Path == "/pets/huge" {
			_, _ = w.Write(bytes.Repeat([]byte("x"), 4096))
			return
		}
		_, _ = w.Write([]byte(`{"id":"1","name":"n","nickname":null}`))
	}))
	defer srv.Close()

	opts := tsOptions(t)
	plan, err := Preview([]byte(richSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}

	script := fmt.Sprintf(`import assert from "node:assert/strict";
import { Client } from "./client.ts";
import { ApiError, TimeoutError, ResponseTooLargeError } from "./errors.ts";
import { getPet, updatePet } from "./operations.ts";

const base = %q;
const client = new Client({
  baseURL: base,
  timeoutMs: 2000,
  auth: (req) => { req.headers.set("Authorization", "Bearer loopback-token"); },
});

const ok = await getPet(client, { petId: "a/b", tags: ["x", "y"], status: "sold", xTrace: "trace-1" });
assert.equal(ok.status, 200);
assert.equal(ok.data.name, "n");
assert.equal(ok.data.nickname, null);

const updated = await updatePet(client, {
  petId: "p1",
  body: { id: "p1", name: "n2", nickname: null, tag: null, extra: { keep: true } },
});
assert.equal(updated.status, 200);

let apiErr;
try {
  await getPet(client, { petId: "missing" });
  assert.fail("expected ApiError");
} catch (err) {
  apiErr = err;
}
assert.ok(apiErr instanceof ApiError);
assert.equal(apiErr.status, 404);
assert.equal(apiErr.data.code, 7);

const aborted = new AbortController();
aborted.abort();
await assert.rejects(() => getPet(client, { petId: "p1" }, { signal: aborted.signal }));

await assert.rejects(
  () => getPet(client, { petId: "p1", xTrace: "bad\r\nX-Injected: 1" }),
  /invalid value for header/,
);

const tight = new Client({ baseURL: base, timeoutMs: 50 });
await assert.rejects(() => getPet(tight, { petId: "slow" }), (err) => err instanceof TimeoutError);

const small = new Client({ baseURL: base, maxResponseBytes: 10 });
await assert.rejects(() => getPet(small, { petId: "huge" }), (err) => err instanceof ResponseTooLargeError);

console.log("ts runtime ok");
`, srv.URL)

	if err := os.WriteFile(filepath.Join(opts.TargetDir, "runtime_check.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "runtime_check.mjs")
	cmd.Dir = opts.TargetDir
	cmd.Env = append(os.Environ(), "NODE_NO_WARNINGS=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node runtime check failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "ts runtime ok") {
		t.Fatalf("unexpected node output:\n%s", out)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(records) < 3 {
		t.Fatalf("expected at least 3 requests, got %+v", records)
	}
	first := records[0]
	if first.Method != "GET" || first.Path != "/pets/a%2Fb" {
		t.Fatalf("first request = %+v", first)
	}
	if first.Query != "status=sold&tags=x&tags=y" {
		t.Fatalf("query = %q", first.Query)
	}
	if first.Trace != "trace-1" || first.Auth != "Bearer loopback-token" {
		t.Fatalf("headers = %+v", first)
	}
	second := records[1]
	if second.Method != "PUT" || second.Path != "/pets/p1" {
		t.Fatalf("update request = %+v", second)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(second.Body), &body); err != nil {
		t.Fatalf("body is not JSON: %q", second.Body)
	}
	if body["name"] != "n2" || body["nickname"] != nil || body["tag"] != nil {
		t.Fatalf("body = %s", second.Body)
	}
	if _, ok := body["extra"]; !ok {
		t.Fatalf("additional member lost: %s", second.Body)
	}
	if atomic.LoadInt32(&hits) > 6 {
		t.Fatalf("unexpected extra requests: %d", hits)
	}
}

func TestTypeScriptTypecheckIfAvailable(t *testing.T) {
	tsc := os.Getenv("GOREQUEST_TSC")
	var args []string
	if tsc == "" {
		path, err := exec.LookPath("tsc")
		if err != nil {
			t.Skip("tsc is not installed; TypeScript type checking was not run (set GOREQUEST_TSC to a local tsc)")
		}
		tsc = path
	} else if strings.HasSuffix(tsc, ".js") || strings.HasSuffix(tsc, ".cjs") || strings.HasSuffix(tsc, ".mjs") {
		args = append(args, tsc)
		tsc, _ = exec.LookPath("node")
		if tsc == "" {
			t.Skip("GOREQUEST_TSC points at a script but node is not installed")
		}
	}
	if v, err := exec.Command(tsc, append(args, "--version")...).Output(); err != nil {
		t.Skipf("cannot query the TypeScript version (%v); type checking was not run", err)
	} else if major, minor := parseTSVersion(string(v)); major < 5 || (major == 5 && minor < 7) {
		// rewriteRelativeImportExtensions is a TypeScript 5.7 feature; older
		// compilers reject the generated import specifiers.
		t.Skipf("TypeScript %s is too old (5.7+ required); type checking was not run", strings.TrimSpace(string(v)))
	}
	opts := tsOptions(t)
	plan, err := Preview([]byte(richSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	args = append(args, "--noEmit", "-p", opts.TargetDir)
	cmd := exec.CommandContext(ctx, tsc, args...)
	cmd.Dir = opts.TargetDir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tsc failed: %v\n%s", err, out)
	}
}

// parseTSVersion reads the "Version X.Y.Z" banner printed by tsc --version.
func parseTSVersion(out string) (major, minor int) {
	fields := strings.Fields(strings.TrimSpace(out))
	for _, f := range fields {
		if _, err := fmt.Sscanf(f, "%d.%d", &major, &minor); err == nil {
			return major, minor
		}
	}
	return 0, 0
}

// TestPathEscapingMatchesGo proves the TypeScript path encoder produces the
// same percent-encoding as Go's net/url.PathEscape.
func TestPathEscapingMatchesGo(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is not installed")
	}
	nodeSupportsTS(t)
	inputs := []string{
		"a+b", "a b", "中文", "a?b", "a#b", "a%b", "a&b", "a=b", "a;b", "a,b",
		"a/b", "a:b", "a@b", "a$b", "a!b", "a'b", "a(b)", "a*b", "a[b]", "a{b}",
		"a|b", "a\\b", "a^b", "a`b", `a"b`, "a<b>", "a\tb", "a\nb", "%2F",
	}
	opts := tsOptions(t)
	plan, err := Preview([]byte(richSpec), opts)
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if _, err := Apply(plan); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	want := make([]string, len(inputs))
	for i, s := range inputs {
		want[i] = url.PathEscape(s)
	}
	script := "import { encodePathValue } from \"./client.ts\";\n"
	script += "const inputs = " + mustJSON(inputs) + ";\n"
	script += "console.log(JSON.stringify(inputs.map((v) => encodePathValue(v))));\n"
	if err := os.WriteFile(filepath.Join(opts.TargetDir, "escape_check.mjs"), []byte(script), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("node", "escape_check.mjs")
	cmd.Dir = opts.TargetDir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("node failed: %v\n%s", err, out)
	}
	var got []string
	if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
		t.Fatalf("decode node output %q: %v", out, err)
	}
	if len(got) != len(want) {
		t.Fatalf("length = %d, want %d", len(got), len(want))
	}
	for i := range inputs {
		if got[i] != want[i] {
			t.Fatalf("input %q: ts %q != go %q", inputs[i], got[i], want[i])
		}
	}
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return string(b)
}
