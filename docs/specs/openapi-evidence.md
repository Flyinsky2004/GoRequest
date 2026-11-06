# A03 OpenAPI 3.1 / 3.2、Swagger 2.0 迁移与 JSON Schema 校验工程证据文档（候选草案）

> **状态声明**：本文档记录任务 `A03`（`openapi-schema` — OpenAPI 3.1/3.2 规范服务、Swagger 2.0 迁移、Operation 契约元数据、离线多文件 `$ref` 解析与响应 JSON Schema 校验端口）的官方规范核对结论、本地依赖审查、版本兼容矩阵与候选接口契约。文中涉及的 `extensions.openapi` 结构、3.2 扩展字段映射及三方同步状态属于 **待架构/集成评审确认的候选方案（Candidate Specification）**，不单方面宣称未冻结草案已最终定稿。

---

## 1. 官方规范核对与本地依赖审查（Q-07 / Q-08 证据）

### 1.1 官方规范基线核对
本模块针对以下官方规范版本完成逐项语义核对：
1. **OpenAPI Specification v3.1.2**（2024-10-24）：
   - 顶层 `openapi: "3.1.x"`（目标导出默认 `"3.1.2"`）。
   - Schema 对象完全对齐 **JSON Schema Draft 2020-12**（默认方言 `https://spec.openapis.org/oas/3.1/dialect/base`）：支持 `type` 数组（如 `["string", "null"]` 取代 3.0 的 `nullable: true`）、`const`、`examples` 数组、`prefixItems`、`dependentRequired`、`dependentSchemas`、`allOf`/`anyOf`/`oneOf`/`not`、`if`/`then`/`else`、`$defs`、布尔 Schema（`true`/`false`）与 `discriminator`。
   - 顶层支持 `paths`、`webhooks`、`components`、`servers`、`security`、`tags`、`externalDocs`、`jsonSchemaDialect`；`info` 支持 `summary` 与 `license.identifier`（SPDX，与 `license.url` 互斥）。
   - **Path Item 方法约束**：OpenAPI 3.1.2 的 `PathItem` 仅允许固定标准 HTTP 方法字段（`get`、`put`、`post`、`delete`、`options`、`head`、`patch`、`trace`），不支持 `query` 方法字段或自定义 HTTP 方法映射。
2. **OpenAPI Specification v3.2.1**（2026-09-10，兼容 3.2.0）：
   - 顶层 `openapi: "3.2.x"`（目标导出默认 `"3.2.1"`），新增根级 `$self` URI 基准声明。
   - **Path Item 扩展（核心差异）**：新增 `query` 操作字段（对应 HTTP `QUERY` 方法）以及 `additionalOperations`（`Map[string, Operation]`，承载 `PURGE`、`PROPFIND`、`LOCK`、`REPORT` 等自定义 HTTP 方法，且严禁在 `additionalOperations` 中重复声明标准方法）。
   - **Tag 增强**：`Tag` 对象新增 `summary`、`parent`（层级父标签引用）与 `kind`（如 `nav`、`badge`、`audience`）。
   - **Server 增强**：`Server` 对象新增 `name` 字段。
   - **Example 增强**：`Example` 对象新增结构化 `dataValue` 与序列化 `serializedValue` 字段。
   - **SecurityScheme 增强**：新增 `deprecated`、`oauth2MetadataUrl` 以及 `flows.deviceAuthorization`（RFC 8628 设备授权流）。
   - **流式媒体类型（Streaming MediaType）**：`MediaType` 新增 `itemSchema`、`itemEncoding`、`prefixEncoding`（用于 `text/event-stream`、`application/jsonl`、`application/x-ndjson` 等逐项流式负载）。
3. **Swagger 2.0（OpenAPI 2.0）与 OpenAPI 3.0.x 迁移基线**：
   - Swagger 2.0 顶层 `swagger: "2.0"`：`host` + `basePath` + `schemes` 合并为 `servers` 与 `baseUrl` 变量；`consumes`/`produces` 合并为请求 `Content-Type`/`Accept` 与 `requestBody`/`responses` 媒体类型；`in: body` 与 `in: formData` 迁移为 `requestBody`；`responses[status].schema` 迁入 `content[mediaType].schema`；`securityDefinitions` 迁入 `components.securitySchemes` 与 `.gorequest/auth/*.auth.yaml`；`#/definitions/<Name>` 重写为 `#/components/schemas/<Name>`；`x-nullable: true` 迁移为 3.1 类型数组 `["<type>", "null"]`。
   - OpenAPI 3.0.x（`3.0.0`～`3.0.4`）：导入时自动将 `nullable: true` 归一化为 3.1 `type` 数组，布尔型 `exclusiveMinimum`/`exclusiveMaximum` 结合 `minimum`/`maximum` 归一化为 Draft 2020-12 数值边界，并记录迁移说明。

### 1.2 本地依赖与纯 Go 实现选型证据
检查仓库当前 `go.mod`：
- 直接依赖仅为 `github.com/egoist/mygo v0.7.4` 与 `gopkg.in/yaml.v3 v3.0.1`（且本轮 `go.mod`/`go.sum` 由 `R01-parser` 唯一直接写入）。
- 结论：**采用基于标准库（`encoding/json`、`net/url`、`regexp`、`math`、`unicode/utf8`）与 `gopkg.in/yaml.v3` AST 的纯 Go 确定性实现**，无需引入第三方重型 OpenAPI/Schema 依赖。
- 优势：
  1. `internal/core` 保持 100% 纯 Go，零 GUI/CGO 依赖，零 `go.mod` 合并冲突。
  2. 原生控制离线 `$ref` 文件沙箱边界（复用 `core.Store` 路径限域与符号链接拦截），彻底杜绝第三方库隐式发起 `http://`/`https://` 远程 Schema 抓取或读取宿主机任意路径。
  3. 精确输出符合规格要求的 JSONPath 实例路径（如 `$.data.id`、`$.items[2].price`）与 JSON Pointer（`/data/id`），并保留实际类型与期望类型对比。

---

## 2. OpenAPI 3.1 与 3.2 明确支持矩阵（绝不以 3.1 冒充 3.2）

| 规范特性 / 语义项 | OpenAPI 3.1 (`3.1.2`) | OpenAPI 3.2 (`3.2.1`) | 3.2 → 3.1 降级导出行为 |
| --- | --- | --- | --- |
| 顶层版本标识 | `"3.1.0"`～`"3.1.2"` | `"3.2.0"`～`"3.2.1"` | 写入 `"3.1.2"` |
| 根级 `$self` | 不支持（3.1 校验报错/告警） | **支持**（导入保留至 `ProjectOpenAPIMetadata.SelfURI`，3.2 导出还原） | **有损项**（`openapi-3.2-self-unsupported`）：严格模式拒绝导出；允许有损时剥离并写入报告 |
| 标准 8 种 HTTP 方法 (`GET`..`TRACE`) | **完整支持**（导入/导出/往返） | **完整支持**（导入/导出/往返） | 无损 |
| `PathItem.query` (`HTTP QUERY`) | **不支持**（3.1 严格导出拒绝） | **完整支持**（映射为 GoRequest `method: QUERY` 请求） | **有损阻塞项**（`openapi-3.1-query-method-unsupported`）：严格模式失败；允许有损时跳过该 Operation 并记入兼容性报告 |
| `PathItem.additionalOperations`（自定义方法如 `PURGE`/`PROPFIND`） | **不支持**（3.1 严格导出拒绝） | **完整支持**（映射为 GoRequest B03 自定义方法请求；校验禁止与标准方法重名） | **有损阻塞项**（`openapi-3.1-custom-method-unsupported`）：严格模式失败；允许有损时跳过并记入兼容性报告 |
| `Server.name` | 不支持 | **支持**（保存在 `OpenAPIServer.Name`） | **有损项**（`openapi-3.2-server-name-dropped`）：严格模式拒绝；允许有损时剥离 `name` |
| `Tag.summary` / `Tag.parent` / `Tag.kind` | 不支持 | **支持**（保存在 `OpenAPITag` 并在 3.2 导出时还原） | **有损项**（`openapi-3.2-tag-hierarchy-dropped`）：严格模式拒绝；允许有损时仅保留 `name`/`description`/`externalDocs` |
| `Example.dataValue` / `Example.serializedValue` | 不支持 | **支持**（保存在 `OpenAPIExampleObject`） | **有损项**（`openapi-3.2-example-fields-dropped`）：严格模式拒绝；允许有损时回退至 `value` |
| `SecurityScheme.deprecated` / `oauth2MetadataUrl` / `flows.deviceAuthorization` | 不支持 | **支持元数据保留与导出**（运行时 OAuth/Device 握手交由 `A04`） | **有损项**（`openapi-3.2-security-fields-dropped`）：严格模式拒绝；允许有损时剥离 3.2 专属字段 |
| `MediaType.itemSchema` / `itemEncoding` / `prefixEncoding`（流式契约） | 不支持 | **识别并显式报告损失**（1.0-A 仅执行单次 HTTP 请求，流式协议归 `1.0-B`） | 导入/导出在严格有损拒绝模式（`StrictLossReject`）下直接拒绝（`openapi-3.2-streaming-item-schema-unsupported`）；宽松模式下保留元数据并产出明确 `LossUnsupported` 报告 |
| `webhooks` / `callbacks` / `links` | **识别并显式报告损失**（不伪造普通 REST 请求） | **识别并显式报告损失** | 严格有损拒绝模式下拒绝导入；宽松模式下保留在契约元数据并生成 `LossUnsupported` 条目 |
| 多响应状态码 & 多媒体类型 (`200`/`201`/`400`/`default`, `application/json`/`xml` 等) | **完整支持** | **完整支持** | 无损 |
| 离线本文件 & 多文件相对 `$ref` | **完整支持**（受项目/规范根目录沙箱约束） | **完整支持**（受项目/规范根目录沙箱约束） | 无损 |

---

## 3. Operation 契约元数据与持久化方案

### 3.1 核心原則：绝不从单次调试响应猜测完整契约
- 请求的日常发送响应（`ExecResult`）仅用于展示、历史记录及运行期 Schema 断言，**绝不自动改写或推断请求的 OpenAPI 契约定义**。
- 操作级契约元数据（`operationId`、`summary`、`description`、`deprecated`、路径模板、参数 Schema、多请求媒体类型、多状态码响应 Schema/Headers/Examples、安全要求、外部来源指纹）由用户在「OpenAPI 契约面板」显式编辑或通过 OpenAPI 导入生成。

### 3.2 存储表示（兼容当前 `core/model.go` + 增量补丁）
- 在当前基线中，`core.Request`、`core.ProjectFile`、`core.Collection`、`core.AuthScheme` 已具备 `Extensions map[string]any`（YAML `extensions:` 映射）。
- 本模块将请求级契约持久化于 `Request.Extensions["openapi"]`（结构体视图为 `*OperationContract`），将项目级规范元数据与外部规范同步基线持久化于 `ProjectFile.Extensions["openapi"]`（结构体视图为 `*ProjectOpenAPIMetadata`）。
- 关联的复用 JSON Schema 独立存储于 `.gorequest/schemas/<slug>.schema.json`，遵循 `storage-format.md` §2 的目录约定。
- 同时在 `docs/task-results/A03-integration.patch` 中提供 `core/yaml_preserve.go` 的 `extensions` 变更检测补丁，确保通过 `SaveRequestPreserving` 仅修改 `extensions.openapi` 时也能精确更新磁盘 YAML 并保留原有注释。

---

## 4. 离线 `$ref` 解析与安全边界

1. **默认离线（禁止隐式联网）**：
   - 任何以 `http://`、`https://`、`ftp://`、`ws://`、`wss://` 开头的远程 `$ref` 默认一律拦截，绝不发起网络请求。
   - 被拦截的远程引用汇总至 `MissingDependencies []MissingRefDependency`（记录引用所在的源文件路径、字段路径与目标 URI），并在校验/导入预览中显式展示。
2. **多文件相对路径沙箱**：
   - 允许解析同一规范目录或 `.gorequest/` 项目内的相对文件引用（例如 `./common.yaml#/components/schemas/Error`、`schemas/user.schema.json#/$defs/Address`）。
   - 复用路径规范化与逐级 `os.Lstat` 检查：拒绝含 `..` 越界、绝对路径（`/etc/...`、`C:\...`）、反斜杠路径或符号链接（Symlink）的目标文件。
3. **资源与递归上限**：
   - `MaxRefDepth`（默认 `32` 层引用深度）与循环引用检测栈（发现 `A -> B -> A` 立即报错 `openapi-ref-cycle`）。
   - `MaxRefFiles`（默认 `128` 个外部引用文件）与单文件体积上限（默认 `8 MiB`）。

---

## 5. 响应 Schema 校验与 A01 Runner 对接端口

为满足 `A01`（`runner-core`）并行开发与桌面端即时校验需求，`internal/core/schema_validator.go` 提供独立的纯 Go 端口：

```go
// SchemaValidatorPort 供 A01 Runner、断言执行器与 Desktop 面板调用。
type SchemaValidatorPort interface {
	ValidateInstance(schemaBytes []byte, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error)
	ValidateProjectSchemaFile(p *Project, schemaRelPath string, instanceBytes []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error)
	ValidateResponseForRequest(p *Project, req *Request, statusCode int, headers []KV, body []byte, opts SchemaValidationOptions) (*SchemaValidationResult, error)
}
```

- **Operation + 状态码 + 媒体类型自动匹配**：
  - `ValidateResponseForRequest` 先按响应状态码（如 `"200"`，或通配 `"2XX"` / `"default"`）和 `Content-Type` 媒体类型（忽略 `; charset=utf-8` 参数，如 `"application/json"`）在 `OperationContract.Responses` 中查找匹配的 Schema（支持内联 Schema、项目内 `schemas/*.schema.json` 引用或 `$ref`）；若请求同时在 `req.Assert` 中声明了 `type: schema`，也支持直接按路径加载校验。
- **实例路径定位（Instance Path）**：
  - 每个校验失败项返回 `InstancePath`（标准 JSONPath，如 `$.data.id`、`$.users[0].roles[1]`）与 `InstancePointer`（RFC 6901 JSON Pointer，如 `/data/id`）、`SchemaPath`、`Keyword`、`Expected`（如 `"integer"`）与 `Actual`（如 `"string"`）。

---

## 6. 事实来源模式与外部规范三方同步边界

1. **两种显式模式（`SourceMode`）**：
   - `native`（原生模式，默认）：`.gorequest/collections/**/*.request.yaml` 与 `.gorequest/schemas/*.schema.json` 为事实来源，OpenAPI 3.1/3.2 为派生导出物。
   - `external-spec`（外部规范模式）：导入时记录外部规范相对路径、SHA-256 摘要、`operationId` 到请求 ID 的映射，以及每个 Operation 的导入基线快照（`BaselineSnapshot`）。
2. **外部规范更新的三方差异预览（`StageOpenAPISyncPreview`）**：
   - 当外部规范更新时，对比 **Baseline（上次导入基线）**、**Upstream（新规范 Operation）** 与 **Local（本地当前请求）**：
     - 若本地仅修改了执行期参数（如 `timeout`、`variables`、新增本地断言、保持 `{{baseUrl}}` 变量模板），且未与上游契约变更冲突，则自动保留本地执行参数并合入上游新增/修改的参数、状态码或 Schema。
     - 若同一契约字段（如路径、方法、同名参数、请求体结构）在 Upstream 与 Local 均偏离了 Baseline 且内容不一致，则标记为 **三方同步冲突（`SyncConflictItem`）**，在预览阶段将 `CanApply` 设为 `false`（除非用户显式选择覆盖策略），绝不静默覆盖本地修改。
   - **阶段边界说明**：本轮实现完整的 `SourceMode` 元数据记录与 `StageOpenAPISyncPreview` 三方差异预览 + B01 `ChangeSet` 原子应用/回滚；跨文件图形化逐字段交互合并器留作后续增强阶段，不在本轮宣称全自动无损双向自由编辑。
