# F01 Runner 扩展端口契约（非交互鉴权与协议执行）

状态：F01 第一阶段交付，2026-11-10。基于真实 `internal/runner` API 编写，供 F02 及集成人接线使用。字段为**新增可选端口**，HTTP 默认路径与 A01/A02 已有行为不变；不是完整 1.0-A 验收声明。

依据：[F01 任务卡](../task-briefs/F01-automation-completion.md)、[共同约定](../task-briefs/formal4-common-20261110.md)、[Runner Core 契约](../specs/runner-core-contracts.md)、[桌面自动化规格](../specs/desktop-test-automation.md)、[OAuth 证据](../specs/oauth-evidence.md)、[OpenAPI/鉴权规格](../specs/openapi-auth.md)、[AC-06/07/09](../acceptance.md)。

## 1. 目的与边界

- Runner 仍是唯一执行引擎。F02 的 GraphQL/WebSocket 等协议执行**不得复制** Runner、断言、提取、重试、数据集或报告逻辑，只能通过本契约注入执行端口。
- OAuth/OIDC token 获取由 Core `core.OAuthService` 负责；Runner 只通过注入的 `AuthProvider` 端口调用，Core Runner 不依赖 GUI、系统浏览器、Loopback、钥匙串或桌面 `projectState`。
- 两个端口都是**可选**的。缺少适配器时，涉及对应能力的 Suite 在预检阶段以配置错误拒绝，**零业务请求**；纯 HTTP 且非 OAuth 的运行完全不受影响。
- 本契约不承诺三协议本体、Device/PKCE 交互、真实窗口或三平台验收。

## 2. OAuth/OIDC 端口 `runner.AuthProvider`

```go
// internal/runner/port.go
type AuthRequest struct {
    StepID         string               // suite step id（诊断用）
    SchemeID       string               // 鉴权方案 ID；内联方案为 "inline:<request relPath>"
    SchemeType     string               // "oauth2" | "oidc"
    Scheme         *core.AuthScheme     // 克隆后的方案（含 OAuth 配置，不含运行时 token）
    Variables      map[string]string    // 该步骤已分层解析的变量（用于 {{...}} 展开）
    Secrets        core.SecretProvider  // 已冻结的本地秘密提供者
    ExecuteOptions core.ExecuteOptions  // 冻结的网络/目标策略；Interactive/Confirmed 恒为 false
    EnvID          string
    Redactor       *core.Redactor       // 仅用于注册秘密/清洗错误，不进入报告
}

type AuthResult struct {
    Secrets   []string  // 运行时 token/凭据值，供 Runner 注册脱敏；绝不序列化
    ExpiresAt time.Time // 可选，仅诊断
}

type AuthProvider interface {
    // PreflightAuth 必须在零网络请求、零用户交互的前提下判断该方案能否自动完成。
    PreflightAuth(ctx context.Context, req AuthRequest) error
    // ApplyAuth 可访问 token 端点（client_credentials / refresh）；不得弹浏览器。
    // 成功后把鉴权头写入 plan（保持已有 Authorization 时不得覆盖），并返回需脱敏值。
    ApplyAuth(ctx context.Context, plan *core.Plan, req AuthRequest) (AuthResult, error)
}
```

### 2.1 非交互允许范围

| 情形 | PreflightAuth | 说明 |
| --- | --- | --- |
| 已有未过期且可用的 access/id token（按 `useToken`） | 通过 | 不访问网络 |
| 已有 access token 过期但存在 refresh token | 通过 | 运行时静默刷新；轮换/并发由 Core singleflight 处理 |
| `client_credentials` | 通过 | 要求 ClientID/TokenURL（或 Issuer/DiscoveryURL）可解析；ClientSecret 按 clientAuthMethod 校验 |
| `authorization_code` / `device_code` 且无缓存 token | 拒绝 | 返回 `OAuthErrInteractionRequired` 类错误；Runner 标配置错误 |
| 缺少 ClientID/TokenURL/secret 引用/非法 grantType | 拒绝 | 配置错误 |
| 方案内含明文运行时 token | 拒绝 | 复用 `core.ValidateOAuthScheme` 的安全规则 |

### 2.2 执行与失败分类

- Runner 在 `runStep` 构建 attempt plan 后、调用业务 Executor 前调用 `ApplyAuth`。失败时**该 attempt 标记失败且不发送业务请求**；鉴权失败不套用 RetryPolicy。
- 失败分类：
  - 交互必需、需重新授权、配置非法、Discovery issuer 不匹配、ID Token 签名/claims/过期、目标策略拒绝、token store 不可用 → `config`（`KindAuth`，目标策略用 `KindTargetPolicy`）。
  - token/JWKS/Discovery 网络失败、token 端点 5xx/429 → `run`（`KindAuth`）。
  - `context.Canceled` → `canceled`。
- 成功返回的 `Secrets` 由 Runner 注册进步骤与运行级 `core.Redactor`；`ApplyAuth` 自身也应向 `req.Redactor` 注册。报告中不出现 token/refresh/client secret。
- `ProjectRoot`/`ProjectSlug` 由桌面 adapter 构造（当前使用项目目录与 `project.File.ID`），CacheKey 隔离由 `core.BuildOAuthCacheKey` 负责；Runner 不感知。

## 3. 协议执行端口 `runner.ProtocolExecutor`

```go
type ProtocolRequest struct {
    StepID         string
    Entry          *core.RequestEntry
    Plan           *core.Plan            // 已解析的传输计划（HTTP 定义被协议复用）
    ExecuteOptions core.ExecuteOptions   // 冻结网络/目标策略/凭据
    Redactor       *core.Redactor
}

type ProtocolExecutor interface {
    // Supports 判断是否接管该请求协议；返回 false 时预检直接拒绝。
    Supports(req *core.Request) bool
    // PreflightProtocol 必须在零业务请求前完成全部静态校验。
    PreflightProtocol(ctx context.Context, req ProtocolRequest) error
    // ExecuteProtocol 返回与 HTTP 一致的 *core.ExecResult 事实结构，供断言/提取/报告复用。
    ExecuteProtocol(ctx context.Context, req ProtocolRequest) (*core.ExecResult, error)
}
```

- 触发条件：`Request.Protocol` 非空且不等于 `http`。`graphql` 请求若由 F02 适配器接管，也必须走本端口；runner 不再对非 HTTP 协议静默套用 HTTP 执行。
- 缺少端口、`Supports` 为 false 或预检失败 → `config_error` + `KindProtocol`，零请求。
- `ExecResult` 是唯一结果表达：状态、时长、响应头、Body、ContentType、FinalURL 等现有字段；流式协议若无法映射全部事实，必须在 F02 报告中声明限制，不得伪造。
- 端口实现负责其自身的取消、超时、消息/资源上限；Runner 只传播 `ctx` 并保持行隔离。

## 4. 并发与结果查询（同批新增）

- `Options.StepConcurrency` / `RowConcurrency` 现有语义不变；本批新增 `runner.MaxStepConcurrency=64`、`MaxRowConcurrency=16`、`ValidateConcurrency(step,row)`。桌面 UI 输入经该函数校验，非法值明确报错；有效值冻结进本次运行并写入 `RunResult.StepConcurrency/RowConcurrency` 与快照摘要。
- `runner.QueryResult(result, filter, offset, limit)` 提供有界分页/筛选（状态、步骤、文本），`runner.FailureLocations(result, max)` 提供失败定位；UI 只渲染当前页，不再只显示前 256 条。

## 5. 兼容性

- 现有调用方（A02 桌面运行、测试）无需实现新端口；`Options{}` 零值保持纯 HTTP 行为。
- 端口为接口注入，不修改 `core/model.go`、`go.mod/go.sum`、`app.go` 等公共文件。
- 未冻结项：F02 端口字段若需扩展（例如流式事实），由集成人统一评审后版本化；当前契约不做长期冻结承诺。
