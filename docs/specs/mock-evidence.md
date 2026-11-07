# C01 Phase 1 — Local Mock Server Specification & Contract Evidence

> **Status**: Phase 1 implementation contract (`schemaVersion: 1`, unfrozen until final C01 integration review).
> **Scope**: `internal/mockserver/**` and `internal/desktop/mock*.go` (`docs/specs/mock-performance.md` §1–3, AC-13, Q-13).

---

## 1. `.gorequest/mocks/*.mock.yaml` v1 Bounded Strict Format

Mock definition files live under `.gorequest/mocks/*.mock.yaml` (or `.mock.yml`) and are loaded exclusively through the project's confined `core.Store`:
- Maximum file size: `1 MiB` (`DefaultMaxMockFileBytes`).
- Maximum rules per project: `512` (`DefaultMaxRulesPerProject`).
- Strict parsing: `core.ParseStrictYAMLTree` rejects YAML anchors/aliases, merge keys (`<<`), custom tags, and excessive nesting; `yaml.Decoder.KnownFields(true)` rejects unknown fields; `schemaVersion` must be `1` (higher versions are rejected with a clear upgrade message).
- File discovery: `ListProjectMockFiles` normalizes paths with forward slashes and sorts them lexicographically before parsing. Filesystem iteration order never influences rule compilation or matching.

```yaml
schemaVersion: 1
id: orders-service
name: Orders Local Mock
defaultScenario: default
variables:
  region: ap-east
cors:
  allowOrigins:
    - https://app.local
  allowMethods:
    - GET
    - POST
  allowHeaders:
    - Content-Type
    - X-Trace-Id
rules:
  - id: get-order
    name: Get Order by ID
    priority: 100
    match:
      method: GET
      path: /v1/orders/{orderId}
      pathParams:
        orderId:
          prefix: ord-
      query:
        - name: expand
          equals: items
      headers:
        - name: X-Client
          equals: gorequest
    response:
      status: 200
      contentType: application/json; charset=utf-8
      delayMs: 15
      body: |
        {
          "orderId": "{{path.orderId}}",
          "region": "{{var.region}}",
          "seq": {{req.seq}}
        }
    variants:
      error_400:
        status: 400
        contentType: application/json; charset=utf-8
        body: '{"error":"bad_request"}'
      error_500:
        status: 500
        contentType: application/json; charset=utf-8
        body: '{"error":"internal_error"}'
```

---

## 2. Deterministic Priority & Same-Priority Ambiguity Detection

1. **Deterministic Ordering**:
   - Enabled rules applicable to the target scenario are ordered by `Priority DESC`, with `Rule.ID ASC` as the stable tie-breaker.
   - Cross-file `MockFile.ID` and `MockRule.ID` uniqueness is enforced at load/compile time.
2. **Same-Priority Overlap Detection (`AmbiguityError`)**:
   - Before a snapshot can be served, `CompileMockFiles` inspects all pairs of enabled rules in the target scenario that share the same `Priority` and `Method`.
   - If their path templates can match the same URL path (identical literal segments or parameter wildcard overlap) and their path-param, query, header, and body predicates are not mutually exclusive (e.g., `equals: "a"` vs `equals: "b"` on the same key), compilation returns a structured `*AmbiguityError` listing every `RuleConflict`.
   - A snapshot with conflicts blocks `Server.Start`, `Server.SwapSnapshot`, and `RunSuiteWithControlledMock`.

---

## 3. Declarative Response Templates & Security Boundaries

- Supported template expressions in response bodies and headers:
  - `{{path.<name>}}` — matched URL path parameter
  - `{{query.<name>}}` — first value of matched request query parameter
  - `{{var.<name>}}` — mock-file static variable (`MockFile.Variables`)
  - `{{req.id}}`, `{{req.seq}}` — deterministic request sequence identifier
  - `{{rand.int}}`, `{{rand.hex8}}`, `{{rand.uuid}}` — deterministic values seeded from `(Snapshot.Seed, req.Seq)` via `math/rand/v2.NewPCG`
- **Media-Type Safe Escaping**:
  - When rendering `{{path.*}}` or `{{query.*}}` into a JSON response (`application/json` or `+json`), string content is JSON-escaped without outer quotes; in HTML/XML responses, values are HTML-escaped; in headers, CR/LF characters are stripped.
- **Forbidden Constructs**:
  - `{{secret.*}}` and arbitrary expressions/scripts are rejected at compile time.
  - Raw `Authorization`, `Cookie`, `Set-Cookie`, or `Proxy-Authorization` credential literals in shared `.mock.yaml` rules are rejected.
  - Unmatched requests never forward or proxy to any external network; they return HTTP `404 Not Found` with sanitized JSON near-miss diagnostics.

---

## 4. A03-Validated JSON Schema Example Synthesis Subset

`GenerateSchemaExample` and `BuildRuleFromOperationContract` synthesize deterministic JSON examples from OpenAPI 3.1/3.2 / JSON Schema Draft 2020-12 objects and validate every synthesized payload through A03 `core.NewSchemaValidator().ValidateInstance(..., ValidateFormats: true)`:

- **Supported Subset**:
  - Explicit `const`, `enum`, `example`, `examples`, `default`
  - `type: object` (`properties`, `required`, `minProperties`, `maxProperties`, `additionalProperties`)
  - `type: array` (`prefixItems`, `items`, `minItems`, `maxItems`, `uniqueItems`)
  - `type: string` (`minLength`, `maxLength`, `format`: `email`, `uri`, `url`, `uuid`, `date`, `date-time`, `ipv4`, `ipv6`, `hostname`)
  - `type: integer` / `type: number` (`minimum`, `maximum`, `exclusiveMinimum`, `exclusiveMaximum`, `multipleOf`)
  - `type: boolean`, `type: null`
  - In-document `$ref` (`#/...`) with cycle detection
  - `allOf` (compatible object property merge) and `anyOf` / `oneOf` (selecting a branch verified by A03)
- **Explicit Failure Contract (`*SchemaExampleError`)**:
  - Arbitrary regex `pattern` without an explicit `example`/`const`/`enum`/`default`, external `$ref`, recursive `$ref` cycles, `contains`, or unsatisfiable `not`/`oneOf` constraints fail with a structured `*SchemaExampleError` instructing the user to provide an explicit example.

---

## 5. Loopback-First Bind Policy, Resource Bounds & Suite Controlled Lifecycle

- **Bind Policy**:
  - Default bind is `127.0.0.1` (`AllowLAN: false`). Non-loopback hosts (`0.0.0.0`, `::`, LAN IPs) are rejected with `*BindPolicyError` unless `AllowLAN: true` is explicitly enabled, in which case `Server.IsLAN()` reports `true` and the UI displays a prominent LAN exposure warning.
  - Fixed port conflicts return `*PortConflictError` without killing external processes or silently switching ports. Ephemeral port `0` exposes the actual bound `host:port` via `Server.BoundAddr()` and `Server.BaseURL()`.
- **Suite Controlled Lifecycle (`RunSuiteWithControlledMock`)**:
  - Compiles rules into a frozen `*CompiledSnapshot` -> binds `127.0.0.1:0` -> verifies readiness -> injects `mockUrl`, `mockBaseUrl`, `mockHost`, `mockPort`, and optional `EndpointVar` (e.g. `baseUrl`) into a private clone of `Suite.Variables` -> runs `runner.Runner` -> guarantees `Server.Stop` on pass, fail, preflight error, or context cancellation.
  - Switching the active scenario in the desktop panel swaps only the panel server's snapshot pointer (`SwapSnapshot`), never mutating the frozen snapshot used by an in-flight Suite run.
