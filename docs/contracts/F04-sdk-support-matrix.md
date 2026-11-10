# F04 SDK 生成器能力矩阵与契约

本文件是本路（F04）对 SDK 生成器可表达范围的声明，供集成人和其他路引用。凡此处标为“阻断”的输入，生成器不会静默降级或丢弃：`Generate` 返回 `ErrUnsupportedOperations`，UI 预览逐条列出 `Method Path: Reason`，且不写出任何文件。

规范来源：OpenAPI 3.1.2 / 3.2.1（`docs/sources.md` 第 40–41 行），JSON Schema 2020-12；实现位于 `internal/sdkgen/`。

## 1. 输入契约

| 项 | 取值 | 行为 |
|---|---|---|
| `openapi` 版本 | `3.1.x`、`3.2.x` | 其他值 → `sdkgen: unsupported OpenAPI version %q (supported: 3.1.x, 3.2.x)` |
| 空快照 | `len == 0` | `sdkgen: OpenAPI snapshot is empty` |
| 无 `paths` | 空对象 | `sdkgen: snapshot has no paths` |
| 快照格式 | JSON（由 `core.ExportOpenAPI` 产生） | 解析失败 → `sdkgen: parse OpenAPI JSON snapshot: %w` |
| `nullable` | 3.0 构造 | 3.1/3.2 快照中出现即报错：`` `nullable` is an OpenAPI 3.0 construct and is not valid in OpenAPI 3.1/3.2; use type [..., "null"] `` |
| null 表达 | `type: [..., "null"]` | 进入中间模型的 `Type.Nullable` |
| 语言 | 空/`go`；`typescript`/`ts` | 其他 → `sdkgen: unsupported language %q (supported: go, typescript)` |

## 2. Schema → 类型映射

中间模型：`TypeKind{Any,String,Int,Number,Bool,Array,Map,Model}`；`ModelKind{object,enum,union,alias}`；`maxSchemaDepth = 64`。

| JSON Schema | Go | TypeScript | 说明 |
|---|---|---|---|
| 无 `type` / 未知结构 | `json.RawMessage` | `unknown` | 保真逃生口，不做猜测 |
| `string` | `string` | `string` | 不消费 `format`（见 §7 限制） |
| `integer` | `int64` | `number` | `format: int32` 也映射 `int64` |
| `number` | `float64` | `number` | `format: float/double` 不区分 |
| `boolean` | `bool` | `boolean` | |
| `array` + `items` | `[]T` | `Array<T>` | 无 `items` → `array schema has no items` |
| `object` + `additionalProperties`（true/schema） | `map[string]T`（根级）/ 结构体字段 `AdditionalProperties map[string]T` | `Record<string, T>` | 声明字段与额外属性冲突 → `<Model>: additional property %q collides with a declared field` |
| `object` + `properties` | 具名 struct | `interface`（封闭）/ `type X = {...} & Record<string, unknown|T>`（开放） | 模型名由 `names.go` 分配 |
| `enum` | 具名常量类型 + `Validate` | `const X = {...} as const` + `type X = (typeof X)[keyof typeof X]` | 混合 JSON 类型 → `enum values mix incompatible JSON types` |
| `allOf` | 合并后的 struct | 合并后的 interface/交叉类型 | 仅支持全部为 object 模型的成员 |
| `oneOf`/`anyOf` + `discriminator.propertyName` | 带 `Kind` 判别字段的 union + 解码 | `(A & { d: "v" }) \| ...` | 无 discriminator → `%s requires a discriminator (propertyName) to generate an accurate tagged union` |
| `$ref` 本地组件 | 引用同名模型 | 引用同名类型 | 仅 `#/components/schemas/`；远程 → `reference %q is not a local component schema reference` |
| 递归 `$ref` | object/union 字段用指针，可递归 | 自引用 interface/type | 编译深度超 `64` → `schema nesting exceeds 64 levels` |

### 2.1 必填 / 可空 / 可选

Go：

| 组合 | 渲染 |
|---|---|
| required 且非 null | 基础类型；object/union 模型用 `*T` |
| required 且 nullable | `Optional[T]`（`Optional[*T]`） |
| 非 required（无论是否 nullable） | `Optional[T]`（`Optional[*T]`） |

`Optional[T]` 提供 `Some`/`Null`/`IsSet`/`IsNull`/`Get`/`Set`/`SetNull`，自定义 `MarshalJSON`/`UnmarshalJSON`，区分“缺失 / 显式 null / 有值”。生成注释分别为 `// Name (required, JSON "x").` 与 `// Name is optional (JSON "x").`。

TypeScript：

| 组合 | 渲染 |
|---|---|
| required 且非 null | `name: T` |
| required 且 nullable | `name: T \| null` |
| 非 required | `name?: T`（nullable 时 `name?: T \| null`） |

生成头注释固定说明：`Optional and nullable are separate: field?: T means absent or present, field: T | null means present and possibly null.`

### 2.2 命名

- 类型名与字段名由 `nameAllocator` 分配：大小写不敏感去重；`reservedTypeNames`（Client、Response、APIError、Optional、SDKError 等）与 `reservedFieldNames`（Validate、Body、AdditionalProperties、MarshalJSON、UnmarshalJSON、Error）避让，必要时追加 `Type`/`Model` 后缀或数字。
- 参数与 operation 名称冲突时追加 `Param` 后缀。
- CJK 等非 ASCII 属性名：Go 用合法标识符 + JSON tag 保真；TS 对非 ASCII key 用引号包裹（`tsPropertyKey`），`isASCIIIdentifier` 判定。

## 3. 参数矩阵

| `in` | 允许的 style | 其他限制 | 阻断消息（示例） |
|---|---|---|---|
| `path` | `simple`（或省略） | 必须 required（规范强制） | `path parameter %q style %q is not supported` |
| `header` | `simple`（或省略） | 名称必须合法；不得命中 managed header 集合 | `header parameter %q targets a managed header`、`header parameter %q has an invalid header name` |
| `query` | `form`（默认）、`spaceDelimited`、`pipeDelimited` | 后两者必须为数组且不得 `explode=true` | `query parameter %q style %q requires an array`、`query parameter %q style %q does not support explode=true` |
| `query` | `deepObject` | 一律拒绝 | `query parameter %q uses unsupported deepObject style` |
| `cookie` | — | 一律拒绝 | `cookie parameter %q is not supported` |

其他参数规则：

- `content` 代替 `schema` → `parameter %q uses content instead of schema, which is not supported`。
- managed header 集合（不区分大小写）：`host`、`content-length`、`transfer-encoding`、`connection`、`trailer`、`upgrade`、`proxy-connection`、`te`、`keep-alive`。
- 参数 schema 仅接受单类型标量或一维数组：union/allOf/oneOf/anyOf → `union or composed schemas are not supported`；多类型 → `multi-type schemas are not supported`；嵌套数组 → `nested arrays are not supported`；`$ref` 嵌套超过 8 层 → `schema $ref nesting is too deep`；未知类型 → `schema type %q is not supported`；无 items 的数组 → `array schema has no items`。
- 重复参数（`in` + 名称，header 名称大小写不敏感）→ `duplicate %s parameter %q (also declared as %q); duplicate parameters cannot be merged faithfully`。
- 编码语义：query 数组 explode → 重复同名参数；非 explode → `,`/空格/`|` 连接（按 style）；path 数组 → `,` 连接后逐段转义；header 数组 → `,` 连接。

## 4. 请求体与响应

| 项 | 支持 | 阻断/诊断 |
|---|---|---|
| `application/json` 单一媒体 | 是 | — |
| 多请求体媒体 | 否 | `multi-media request bodies are not supported` |
| `multipart/form-data` | 否 | `request body media type "multipart/form-data" is not supported (only application/json)` |
| `application/x-www-form-urlencoded` | 否 | 同上（媒体名替换） |
| 3.2 `itemSchema` 流式请求体 | 否 | `OpenAPI 3.2 streaming request bodies are not supported` |
| 3.2 流式响应 | 否 | `OpenAPI 3.2 streaming responses are not supported` |
| 响应多状态码 | 是 | 生成 `<Op>Result.Status<Label>`；`default` 走默认分支 |
| 响应无 `application/json` | 保留原始字节 | info 诊断 `sdk-response-untyped` |
| 响应多 JSON 媒体 | 取一个 JSON 媒体 | info 诊断 `sdk-response-media`，其余按原始字节返回 |

## 5. Operation 级阻断（`ErrUnsupportedOperations`）

未勾选“只生成支持子集”时，出现下列任一项即整体阻断且不写文件；勾选后跳过并产生 `sdk-operation-skipped`（warning）。

| 来源 | Reason 文本 |
|---|---|
| path item 级 `$ref` | `path item $ref is not supported` |
| operation 级 `$ref` | `operation $ref is not supported` |
| `callbacks` | `callbacks are not supported` |
| `webhooks` | `webhooks are not generated as client operations` |
| 3.2 `additionalOperations` | `OpenAPI 3.2 additionalOperations are not supported in this phase` |
| 安全需求 AND 组合 | `security requirements combined with AND are not supported; supply authentication through Client.Auth` |
| path 级 `parameters` 非数组 | `path-level parameters is not an array` |
| operation 对象无法解析 | `operation object is not valid JSON` |
| 参数/请求体/响应校验失败 | 上述各节的原始消息 |

安全方案只记录方案名用于文档，从不写入任何凭据；AND 组合被拒绝而不是猜测如何合并。

## 6. 组合（allOf / oneOf / anyOf）错误

allOf：

- `allOf[%d]: %w`（成员本身编译失败）
- `allOf[%d] is not an object model`
- `allOf contains a recursive reference to %q`
- `allOf property %q has conflicting types`
- `allOf members declare conflicting additionalProperties`
- `required property %q is not defined by the merged allOf object`
- `schema and allOf members declare conflicting additionalProperties`

oneOf/anyOf：

- `%s requires a discriminator (propertyName) to generate an accurate tagged union`
- `%s has no variants`
- `%s[%d] is not a schema object`
- `%s[%d] must be a local component reference`
- `%s[%d] does not resolve to an object model`
- `%s variant %q does not declare discriminator property %q`
- `%s has duplicate discriminator value %q`

生成侧：Go union 的 `UnmarshalJSON` 对未知判别值返回 `<Model>: unknown discriminator %q`。

## 7. 诊断码（仅三个）

| code | 级别 | 位置 | 含义 |
|---|---|---|---|
| `sdk-response-untyped` | info | `<method> <path> <status>` | 响应无 JSON 内容，不生成类型模型 |
| `sdk-response-media` | info | `<method> <path> <status>` | 采用某 JSON 媒体，其余按原始字节返回 |
| `sdk-operation-skipped` | warning | `<method> <path>` | 仅在 `IncludeUnsupported` 时出现，说明被跳过及原因 |

诊断不会静默合并或丢弃：UI 的“支持与损失报告”逐条显示 `[code] location: message`。

## 8. 生成工件清单

Go（`renderGoFiles`）：

| 文件 | Kind | 条件 |
|---|---|---|
| `client.go` | source | 总是 |
| `models.go` | source | 存在模型时 |
| `operations.go` | source | 存在 operation 时 |
| `README.md` | readme | 总是 |
| `go.mod` | module | 总是（`module <ModulePath>` + `go 1.21`） |

TypeScript（`renderTypeScriptFiles`）：

| 文件 | Kind |
|---|---|
| `types.ts` | source |
| `errors.ts` | source |
| `client.ts` | source |
| `operations.ts` | source |
| `index.ts` | source |
| `package.json` | module |
| `tsconfig.json` | config |
| `verify.mjs` | config |
| `README.md` | readme |

两个后端都只生成标准库/平台能力代码，不引入第三方运行时依赖，不嵌入凭据、Cookie、示例敏感值或服务器绝对路径。

## 9. 生成物运行时契约

Go：

- `DefaultTimeout = 30 * time.Second`、`DefaultMaxResponseBytes int64 = 8 << 20`
- `NewClient(baseURL string, opts ...Option) *Client`；`WithHTTPClient`/`WithAuth`/`WithUserAgent`/`WithMaxResponseBytes`
- `Client.do` 有界读取、超时与取消、**从不重试**
- 非 2xx 返回 `*APIError{StatusCode, Header, Body, Typed any}`，`Typed` 为已解码的声明响应类型
- 生成头 4 行：`// Code generated by GoRequest sdkgen; DO NOT EDIT.`、generator、template、`spec-sha256`

TypeScript：

- `DEFAULT_TIMEOUT_MS = 30_000`、`DEFAULT_MAX_RESPONSE_BYTES = 8 * 1024 * 1024`
- 错误类：`SDKError`、`ApiError`、`TimeoutError`、`ResponseTooLargeError`
- `ClientOptions{baseURL, fetch?, auth?, timeoutMs?, maxResponseBytes?, defaultHeaders?}`
- `encodePathValue` 与 Go `net/url.PathEscape` 逐字节一致（`TestPathEscapingMatchesGo`）
- header 值拒绝 CRLF；未解析路径模板显式报错；`AbortController` + 超时 + 用户 `signal`；**从不重试**
- 工具链：TypeScript 5.7+ 做类型检查，Node 22.18+ 直接执行 `.ts`；生成器不安装、不运行工具
- `verify.mjs` 默认不发送任何请求，仅当设置 `GOREQUEST_VERIFY_BASE_URL` 时向该回环/测试地址发送首个无参 GET

## 10. Manifest 与写入保护

- 文件名 `.gorequest-sdk-manifest.json`；字段 `generatorVersion`、`templateVersion`、`language`、`specVersion`、`specDigest`、`packageName`、`modulePath`、`files[{path,digest,kind}]`。
- 版本常量：`GeneratorVersion = "sdkgen/2"`、`TemplateVersionGo = "go/2"`、`TemplateVersionTypeScript = "ts/1"`。
- 目标目录已有另一语言 manifest → `ErrConflict`：`target directory already contains a %s SDK (manifest language %q); choose a different directory for %s`。
- generator/template 版本不符 → `ErrInvalidManifest`：`manifest generator %q is not %q; choose a new output directory` 等。
- 冲突原因（`Conflict.Reason`）固定文案：`target is a symbolic link`、`target is not a regular file`、`existing file is not owned by the SDK generator (manual file)`、`generated file was modified manually; refusing to overwrite`、`generated file would change; enable overwrite to update it`、`manifest disappeared since preview`、`manifest changed since preview`、`manifest is a symbolic link`、`manifest is not a regular file`、`manifest exists but was not valid at preview`、`stale target is a symbolic link`、`stale target is not a regular file`、`stale generated file was modified manually; refusing to delete`、`stale generated file remains; enable stale deletion to remove it`、`target directory changed since preview`。
- 限制：`DefaultMaxFiles = 500`、`DefaultMaxTotalBytes = 32 << 20`、manifest 读取上限 `1 MiB`；写失败按已写集合回滚。

## 11. 已明确不支持的项（本阶段）

- multipart / urlencoded 请求体、多请求体媒体
- 远程 `$ref`、非 `#/components/schemas/` 引用
- callbacks、webhooks、3.2 `additionalOperations`、3.2 streaming
- AND 组合安全需求（只支持单一安全需求，凭据由调用方注入）
- cookie 参数、deepObject、对象型参数 schema、嵌套数组参数
- 不消费 `format`（int32/int64 同为 `int64`；float/double 同为 `float64`；date-time/uuid/byte 等字符串格式仍为 `string`）
- 未生成 Python/JS 独立 SDK、Proto/gRPC
