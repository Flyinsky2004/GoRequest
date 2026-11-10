# F02 协议能力契约与兼容矩阵（GraphQL / WebSocket / 有界 WS 脚本）

> 2026-11-10 集成更新：最终适配器位于 `internal/protocolrun/runneradapter`，已接入桌面 Runner/Mock 与 Suite 请求选择器。WS 事实以 `AttemptResult.ProtocolFacts` 脱敏保存，失败时仍保留，HTTPStatus 不伪造成功。GraphQL 业务 errors 默认失败；显式保存 `Extensions["graphqlAllowErrors"]=true` 可接受预期错误响应。订阅不进入 Runner。 详见 [集成报告](../task-results/integration-F01-F04-2026-11-10.md)。

状态：F02 第一阶段交付，2026-11-10。基于本 worktree 的真实代码编写，供集成人接线与后续维护使用。
本文件只描述**已实现并自动化验证**的能力；未实现项在第 6 节单列，不以“后续补充”混同为已完成。

依据：[F02 任务卡](../task-briefs/F02-protocols.md)、[共同约定](../task-briefs/formal4-common-20261110.md)、[协议规格](../specs/protocols.md)、[F01 Runner 扩展端口契约](F01-runner-extension.md)。

## 1. 目的与边界

- 协议能力分三层，彼此不复制逻辑：
  1. `internal/core`：SDL 解析/离线校验、Introspection 文档与响应解析、GraphQL over HTTP 计划派生、schema 引用扩展。
  2. `internal/websocket`：`graphql-transport-ws` 订阅客户端；`internal/protocolrun`：有界 WS 脚本计划（connect → send → await/assert → close）。
  3. `internal/desktop`：原生 UI（Schema Explorer、订阅时间线、脚本工作区、二进制文件发送）。
- 本卡**不新建 Runner**。协议执行接入 F01 的 `runner.ProtocolExecutor` 端口；适配器以 patch 形式交付（`docs/task-results/F02-integration.patch`），因为当前 worktree 不含 F01 端口代码。
- 纯 Go core 不引入 GUI 依赖；打开项目、选择文件、收到帧都不自动发网络请求。

## 2. 上游协议版本与来源

| 协议 | 采用版本/文档 | 链接 | 本仓库实现位置 |
| --- | --- | --- | --- |
| GraphQL over HTTP | Current Working Draft（查询于 2026-11-10） | https://http-spec.graphql.org/draft/ | `internal/core/graphql.go` |
| GraphQL over WebSocket | `graphql-ws` 仓库 `master` 的 `PROTOCOL.md`（查询于 2026-11-10） | https://github.com/enisdenjo/graphql-ws/blob/master/PROTOCOL.md | `internal/websocket/subscription.go` |
| GraphQL SDL 解析/校验 | `github.com/vektah/gqlparser/v2 v2.5.64` | https://github.com/vektah/gqlparser | `internal/core/graphql_schema.go` |
| WebSocket (RFC 6455) | `github.com/gorilla/websocket v1.5.3` | https://github.com/gorilla/websocket | `internal/websocket`、`internal/protocolrun/websocket_transport.go` |
| GraphQL 语言规范（文档/变量语法） | October 2021 语义（由 gqlparser 提供） | https://spec.graphql.org/October2021/ | `internal/core/graphql.go` |

`go.mod` 本卡唯一改动：`github.com/agnivade/levenshtein v1.2.2 // indirect`（gqlparser 的传递依赖）。

## 3. GraphQL over WebSocket（`graphql-transport-ws`）

### 3.1 消息矩阵

| 消息 | 方向 | 本仓库行为 | 代码事实 |
| --- | --- | --- | --- |
| `connection_init` | C→S | 连接后立即发送一次；payload 来自 `SubscriptionConfig.InitPayload` | `subscription.go` |
| `connection_ack` | S→C | 必须在 `AckTimeout` 内到达，否则连接失败并关闭 | `DefaultSubscriptionAckTimeout = 10s` |
| `connection_error` | S→C | 视为 init 被拒绝，返回错误且不发送 `subscribe` | `TestGraphQLTransportWSInitRejectedLive` |
| `ping` | 双向 | 收到即回 `pong`；`pongsReceived` 计入快照 | `SubscriptionSnapshot.PongsReceived` |
| `pong` | 双向 | 计入快照，不阻塞消息流 | 同上 |
| `subscribe` | C→S | 每个 operation 携带唯一 `id`；`Subscribe` 返回 `*SubscriptionOperation` | `Subscribe(ctx, query, operationName, variables, sensitive)` |
| `next` | S→C | 投递到该 operation 的有界队列 | `QueueCapacity`（默认 256） |
| `error` | S→C | 记录到 `SubscriptionSnapshot.ProtocolErrors`，终止该 operation | 同上 |
| `complete` | 双向 | 服务端结束 operation 映射为 `ErrSubscriptionComplete`；客户端取消时发送 `complete` 后关闭 | `TestSubscriptionCancelSendsComplete` |
| 未知/错配 operation id | S→C | 记录协议错误，不影响其它 operation | `TestSubscriptionPingPongAndUnknownOperation` |

### 3.2 关闭码矩阵（**规范值，仅测试中作为字面量出现**）

`subscription.go` 只导出子协议常量 `GraphQLTransportWSSubprotocol = "graphql-transport-ws"`；
下列关闭码在实现中**不是具名常量**，只在测试里以数字字面量断言。引用时不要声称代码中存在这些常量。

| 关闭码 | 规范含义 | 本仓库测试覆盖 |
| --- | --- | --- |
| 4400 | Bad Request | 规范项，未单独测试 |
| 4401 | Unauthorized | 规范项，未单独测试 |
| 4403 | Forbidden | 规范项，未单独测试 |
| 4408 | Connection Init Timeout | `TestGraphQLTransportWSAckTimeoutLive`（以超时行为验证） |
| 4409 | Subscriber for <id> already exists | 规范项，未单独测试 |
| 4429 | Too Many Initialisation Requests | 规范项，未单独测试 |
| 1000 | Normal Closure | `TestSubscriptionCancelSendsComplete` |

### 3.3 明确不做的行为

- **不静默重连**：`ErrSubscriptionClosed` 直接返回给调用方；桌面 UI 需用户显式操作。
- **不接受旧版同名 `graphql-ws` 子协议**：协商值不匹配即报错（`TestGraphQLTransportWSRejectsWrongSubprotocol`）。
- **不把普通 WS 连接当作订阅实现**：必须走 `ConnectSubscription` 完成 init/ack 握手。
- init 认证材料只放在 `InitPayload`；`InitPayloadSensitive = true` 时时间线按敏感值脱敏，同时值注册进 redactor（`TestSubscriptionSensitiveInitIsRedactedAndRegistered`）。

## 4. GraphQL over HTTP

### 4.1 规范要求与本仓库编码状态

| 规范项 | 规范等级 | 本仓库状态 |
| --- | --- | --- |
| POST 为 MUST | MUST | 已实现：默认 POST |
| GET 仅用于 query | MAY | 已实现：GET 只允许 query；mutation/subscription 走 POST |
| mutation over GET 返回 4xx | MUST | 规范项；本仓库在**计划构建阶段**就拒绝，不发请求 |
| 405 Method Not Allowed | RECOMMENDED | **未编码为常量**，仅规范描述 |
| `application/graphql-response+json` | 规范媒体类型 | 已编码：`graphQLAccept = "application/graphql-response+json, application/json"` |
| 部分成功（data + errors） | SHOULD | 已实现：`GraphQLResult.Partial()` / `ClassifyGraphQLResult` |
| HTTP 200 ≠ 业务成功 | 规范语义 | 已实现：结果区分为 HTTP 事实 / data / errors / extensions |

只有 `graphQLAccept` 是代码常量；294/405 等状态码语义属于规范层描述，**代码中没有对应常量**。

### 4.2 多 operation 与变量

- 多 operation 文档必须显式指定 `operationName`；未指定时拒绝执行，**不随机取第一个**。
- 变量 JSON 上限 `MaxGraphQLVariablesBytes = 256 << 10`，嵌套深度上限 `MaxGraphQLVariablesDepth = 64`。
- 文档上限 `MaxGraphQLDocumentBytes = 256 << 10`、`MaxGraphQLTokens = 20000`。
- HTTP 路径上的 subscription operation 被显式拒绝（`AnalyzeGraphQL`），并提示改用 WS 订阅工作区。

## 5. 本地 SDL、Introspection 与 schema 引用

### 5.1 引用与摘要

```go
// internal/core/graphql_schema.go
const GraphQLSchemaExtensionKey = "graphqlSchema"

type GraphQLSchemaRef struct {
    Path   string `json:"path" yaml:"path"`
    Digest string `json:"digest,omitempty" yaml:"digest,omitempty"`
}
```

- 引用存于 request 的 `Extensions[GraphQLSchemaExtensionKey]`，`SetGraphQLSchemaRef`/`GraphQLSchemaRefFromRequest` 读写，`Path` 为空即删除。
- `Digest` 是用户校验过的 SDL **原始字节**的 SHA-256 小写十六进制；文件被改动后 digest 变化可见，不会静默沿用旧校验结果。
- SDL 与 operation 分开保存：请求只存引用，不复制 SDL 正文。

### 5.2 上限表

| 上限 | 值 | 位置 |
| --- | --- | --- |
| `MaxGraphQLSchemaBytes` | `2 << 20` | `graphql_schema.go` |
| `MaxGraphQLSchemaTokens` | `200000` | 同上 |
| `MaxGraphQLSchemaTypes` | `5000` | 同上 |
| `MaxGraphQLSchemaFieldsPerType` | `2000` | 同上 |
| `MaxGraphQLSchemaArgsPerField` | `256` | 同上 |
| `MaxGraphQLSchemaEnumValues` | `2000` | 同上 |
| `MaxGraphQLSchemaDescriptionBytes` | `4096` | 同上 |
| `MaxGraphQLIntrospectionBytes` | `8 << 20` | `graphql_introspection.go` |
| `MaxGraphQLDocumentBytes` | `256 << 10` | `graphql.go` |
| `MaxGraphQLVariablesBytes` | `256 << 10` | `graphql.go` |
| `MaxGraphQLTokens` | `20000` | `graphql.go` |
| `MaxGraphQLVariablesDepth` | `64` | `graphql.go` |

### 5.3 离线语义校验

- `GraphQLSchemaModel.ValidateDocument(document, operationName) (*GraphQLAnalysis, []GraphQLDiagnostic, error)`：先解析文档，再对已加载的 `ast.Schema` 做完整语义校验，返回带 `Line/Column` 的诊断。
- 解析失败时仍返回 operation 清单（`GraphQLAnalysis`），下拉框不会因选择错误而清空。
- 全程无网络 I/O；无 schema 时退回 `core.AnalyzeGraphQLDocument` 的纯语法分析。

### 5.4 Introspection（仅显式用户操作）

- `GraphQLIntrospectionDocument()` 返回固定查询文档；`GraphQLIntrospectionRef(source *Request)` 生成请求引用（保留目标，丢弃响应体）。
- 只有用户点击才发起；打开项目或编辑文档**从不**自动抓取。
- 抓取复用既有 auth / 网络取消 / 目标策略 / 体积上限；结果先进入预览，用户确认后才落盘为 SDL。
- `ParseGraphQLIntrospection(body []byte) (string, *GraphQLSchemaModel, error)` 先校验 `MaxGraphQLIntrospectionBytes`，再按类型/字段上限截断。

## 6. 有界 WS 脚本计划（`internal/protocolrun`）

### 6.1 计划结构与限制

```json
{
  "version": 1,
  "name": "ticks",
  "limits": {"totalTimeout": "30s", "maxMessageBytes": 1048576, "maxReceivedBytes": 8388608, "maxRetainMessages": 256},
  "steps": [
    {"action": "connect", "url": "wss://example.test/ws", "headers": [{"name": "X-Token", "value": "{{token}}"}], "subprotocols": ["ticks"]},
    {"action": "send", "text": "{\"op\":\"subscribe\"}"},
    {"action": "await", "match": {"jsonPath": "payload.type", "jsonEquals": "\"tick\""}, "timeout": "5s", "cursor": "current", "keepUnmatched": true},
    {"action": "await", "match": {"contains": "\"done\""}, "timeout": "2s", "cursor": "start"},
    {"action": "close", "code": 1000, "reason": "done"}
  ]
}
```

| 限制 | 默认值 | 说明 |
| --- | --- | --- |
| `PlanVersion` | `1` | 其他版本拒绝 |
| `MaxPlanBytes` | `1 << 20` | `OpenPlan` 读取上限 |
| `MaxPlanSteps` | `128` | 步骤数上限 |
| `MaxBinaryFileBytes` | `1 << 20` | 文件/内联二进制上限 |
| `DefaultTotalTimeout` | `30s` | 整次运行 deadline |
| `DefaultAwaitTimeout` | `5s` | 单个 await |
| `DefaultMaxMessageBytes` | `1 << 20` | 单帧 |
| `DefaultMaxReceivedBytes` | `8 << 20` | 累计接收 |
| `DefaultMaxRetainMessages` | `256` | 保留未匹配消息条数 |

- `Duration` 只接受 JSON 字符串（如 `"5s"`）；裸数字和负值拒绝。
- `DecodePlan` 使用 `DisallowUnknownFields`，未知字段直接报错。
- `Validate()` 只做结构校验：恰好一个 connect、connect 不得在 close 之后、最多一个 close、send 必须且只能指定 text/binaryHex/file 之一、await 必须有 match、close code 在 0–4999。

### 6.2 运行语义

- `Run(ctx, compiled, transport) *Result` 总是返回 `Result`；结构/传输失败作为显式事实记录，不 panic。
- **不重连、不重试步骤**。
- `await` 的 `cursor`：`current`（默认，从当前接收游标起）或 `start`（从最早保留消息起）。
- 未匹配消息：`keepUnmatched=true` 时保留供后续步骤使用，否则跳过；`SkippedFact{Seq, Bytes, Retained}` 逐条记录。
- 接收事实：`StepResult` 保留 `Direction`、`CursorBefore/CursorAfter`、`SentSeq`、`ReceivedSeq`、`ReceivedBytes`、`Skipped`、`Error`、`CloseCode`、`CloseReason`。
- **协议结束事实不扁平化为 HTTP 状态**：`Result.CloseCode`/`CloseReason` 与每步事实独立保留，`ExecResult.Status` 不承载这些信息。
- 预览有界并脱敏：`Payload` 是截断后的脱敏预览，`Truncated` 标记；二进制预览按掩码处理。
- 取消：上下文取消关闭连接并记录 close 事实（`TestLiveWebSocketCancelClosesConnection`）。
- 模板在运行时解析，计划文件**永不保存解析后的凭据**；重放重新读取文件并针对当前环境/Secrets 重新解析，绝不复用历史鉴权值。
- **计划与请求的绑定**：计划文件作为项目文件保存，请求只携带项目相对引用 `Extensions["wsPlan"] = {path, digest}`（`protocolrun.PlanRefFromRequest` / `SetPlanRef`）；`digest` 是已审阅计划字节的 SHA-256，文件被改动后不一致可见。桌面选择项目内计划文件时写入该引用，打开 tab 时按引用本地重载（零网络），项目外的计划只留在本 tab、不写入请求 YAML。

### 6.3 传输层

`protocolrun.NewWebSocketTransport(base websocket.Options, limits Limits)`：

- `base` 提供 proxy/TLS/目标策略/EnvID/CookieJar/Secrets/ProjectRoot/Redactor；连接步只覆盖 URL/headers/subprotocols/max message。
- 帧处理在读取泵上同步执行，必须快速且不得阻塞。
- 目标策略确认（`PromptRequiredBy`）在**任何连接尝试之前**完成；确认只对本次运行生效。

## 7. 桌面 UI 入口

| 入口 | 行为 | 文件 |
| --- | --- | --- |
| GraphQL 工作区五个分区 | Document / Variables / Schema / Headers / Auth；徽标下标随分区数组同步 | `graphql_ui.go`、`graphql.go` |
| Schema Explorer | 类型/字段/参数/描述/弃用；搜索过滤；点选字段插入代码片段 | `graphql_schema_ui.go`、`graphql_schema.go` |
| Introspection 预览 | 显式按钮触发，可取消；预览确认后才保存 | 同上 |
| 订阅时间线 | 数据 / 错误 / 传输关闭 三态可区分；显式取消 | `graphql_subscription_ui.go` |
| WebSocket Script 分区 | 选择计划文件 → 校验 → 运行/取消 → 逐步事实与 close 事实 | `websocket_plan.go`、`websocket_plan_test.go` |
| 二进制文件发送 | 原生文件选择 + 大小上限校验后发送 | `websocket_state.go` |

手动调试（Messages 面板）与有界脚本动作（Script 面板）是**两个独立分区**，互不复用运行状态。

## 8. 明确未实现 / 未验证项

以下各项**不**视为已完成：

1. **无 SDL 或 selection 深度上限**。核心只有变量 JSON 的 `MaxGraphQLVariablesDepth = 64`；schema 解析和文档选择集没有深度限制。
2. **无 schema 驱动的编辑器补全**。只有 Schema Explorer 的字段片段插入（`gqlInsertField`），没有对真实编辑器 API 的补全集成。
3. **无桌面 UI 自动化测试**覆盖 Schema Explorer、Introspection 预览、订阅时间线；只有纯函数/模型测试和脚本工作区 `ui.NewTester` 测试。
4. **无真实窗口 / 真实设备验收**：本卡未运行应用窗口、未做真实 IME 或平台交互验证。
5. **Runner 集成未接线**：适配器只在 patch 中提供；当前 worktree 缺 F01 端口，无法编译验证。
6. **gRPC 四模式**：规格第 4 节，明确不在本阶段范围。
7. **关闭码 4400/4401/4403/4409/4429**：仅规范描述，未逐项测试。

## 9. 与 F01 端口的对接约束

适配器实现 `runner.ProtocolExecutor`：

```go
Supports(req *core.Request) bool
PreflightProtocol(ctx context.Context, req runner.ProtocolRequest) error
ExecuteProtocol(ctx context.Context, req runner.ProtocolRequest) (*core.ExecResult, error)
```

- F01 预检阶段已保证 `req.Plan != nil`，并用已注册已知秘密的 redactor 调用；因此 `PreflightProtocol` 只做**零网络**的配置校验（计划可解码、URL scheme、子协议、限额、文件大小）。
- `ExecuteProtocol` 负责全部运行时边界（deadline、消息/字节上限、取消、close 事实）。
- 接线还需在 `runner_snapshot.go` 的 `runner.Options` 设置 `Protocol:` **和** `PlanOptions.AllowGraphQL = true`；缺后者时 GraphQL 请求在计划构建阶段即以 “GraphQL requests are not supported by this execution path yet” 失败，根本到不了端口。
- 适配器从请求扩展 `Extensions["wsPlan"]` 取得项目相对计划路径（`protocolrun.PlanRefFromRequest`），用 `ExecuteOptions.ProjectFiles` 有界加载并核对 digest；这不需要给端口加路径字段。
- 端口的 `ProtocolRequest` **当前没有变量表**；适配器解析计划模板需要该数据行已解析的变量，故接线补丁需给 `ProtocolRequest` 增加 `Variables map[string]string`（两个调用点 Runner 已算出 `vars`）。详见 `docs/task-results/F02-integration.patch` §3。
- WebSocket 请求在 `BuildTrackedPlanWithOptions` 中不被拒绝，会以 `Method == "WS"` 进入端口。
