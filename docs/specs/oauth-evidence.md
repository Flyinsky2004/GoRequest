# OAuth 2.0 与 OpenID Connect (OIDC) 设计契约与证据（A04）

> **关联卡片**：A04 (`oauth-oidc`)  
> **覆盖规范与验收**：`docs/specs/openapi-auth.md` §4–7、`docs/decisions.md` Q-10、`docs/acceptance.md` AC-09、`docs/specs/network-evidence.md` (B04)、`docs/specs/identity-evidence.md` (B05)  
> **适用包**：`internal/core` (`oauth.go`, `oauth_flows.go`, `oidc.go`)、`internal/platform` (`oauth.go`)、`internal/desktop` (`oauth_ui.go`, `oauth_i18n.go`)

---

## 1. Q-10 选型结论与纯 Go 标准库实现证据

根据 `docs/decisions.md` Q-10（OAuth2 / OIDC / JOSE 栈选型）及并行卡片约束（本轮 `go.mod`/`go.sum` 由 R01-parser 独占维护，非必要不引入重型第三方 Web 框架或具有历史 CVE 风险的旧版 JWT 库）：

1. **纯 Go 1.27 标准库自包含实现**：
   - OAuth 2.0 状态机（Authorization Code + PKCE、Client Credentials、Device Authorization Grant、Refresh Token 轮换与 RFC 7009 Revocation）及 OIDC Discovery / JWKS / ID Token JWS 验签直接基于 Go 标准库实现：
     - 哈希与摘要：`crypto/sha256`、`crypto/sha512`、`crypto`
     - 非对称签名验证：`crypto/rsa`（`RS256`、`RS384`、`RS512` PKCS#1 v1.5 与 `PS256`、`PS384`、`PS512` RSA-PSS）、`crypto/ecdsa` + `crypto/elliptic`（`ES256` P-256、`ES384` P-384、`ES512` P-521，严格处理 JWS 定长 `R || S` 编码）、`crypto/ed25519`（`EdDSA` Ed25519）
     - X.509 证书链兜底：`crypto/x509`（当 JWK 仅提供 `x5c` 或需校验证书公钥一致性时解析）
     - 安全随机数：`crypto/rand`（PKCE `code_verifier`、`state`、`nonce` 均使用 CSPRNG 生成）
     - 常量时间比较：`crypto/subtle`（`state`、`nonce`、`at_hash` 比较使用 `subtle.ConstantTimeCompare`）
2. **零 `go.mod` 变更需求**：
   - 无需新增外部第三方依赖，完全消除了第三方 JWT 库常见的 `alg: "none"` 绕过、RSA/HMAC 算法混淆（Key Confusion）及无界内存解析漏洞。

---

## 2. 架构边界与可注入端口（Core 零 GUI 依赖）

`internal/core` 严格保持纯 Go 业务逻辑，不直接引用 `github.com/egoist/mygo`、OS Keychain 具体驱动或真实系统浏览器命令，所有平台交互通过最小接口注入：

| 端口接口 | 所在包 | 职责 | 平台实现 (`internal/platform`) | 测试替身 |
|---|---|---|---|---|
| `OAuthTokenStore` | `internal/core` | 按项目克隆隔离键读写/删除 `OAuthTokenRecord` | `KeychainOAuthTokenStore`（底层复用 `KeychainDriver` + 会话内存缓存） | `MemoryOAuthTokenStore` / 注入 `ErrUnsupported` 的假驱动 |
| `BrowserOpener` | `internal/core` | 在用户明确触发授权时打开系统默认浏览器 | `SystemBrowserOpener`（校验仅允许 `https://` 或 Loopback `http://` 后调用 `mygo.Shell.OpenExternal`） | 内存回调函数（拦截授权 URL 并用 `httptest` 客户端模拟用户同意） |
| `LoopbackListenerFactory` | `internal/core` | 启动单次临时本地回环 HTTP 监听 (`127.0.0.1:0` / `[::1]:0`) 并等待单次回调 | `LocalLoopbackFactory`（基于 `net.Listen` + `http.Server` 严控超时与立即关闭） | 直接复用本地 `127.0.0.1:0` 真实回环监听或内存通道模拟 |
| `OAuthHTTPDoer` | `internal/core` | 发送 Discovery / JWKS / Token / Device / Revoke 请求 | 默认复用 B04 `ExecuteWithOptions`（继承自定义 CA、mTLS、Proxy、StageTimeouts）与 B05 `EvaluateTarget` | 指向本地 `httptest.Server`（含自签内存 PKI TLS 服务端） |

---

## 3. OAuth 2.0 授权流安全契约

### 3.1 Authorization Code + PKCE (RFC 7636 / RFC 8252)
1. **强制 S256**：
   - 仅支持 `code_challenge_method=S256`；禁止降级为 `plain`。
   - `code_verifier` 使用 32 字节（256-bit）`crypto/rand` 随机熵经 Base64URL（无填充）编码为 43 字符字符串；`code_challenge = BASE64URL-ENCODE(SHA256(ASCII(code_verifier)))`。
2. **高熵单次 `state` 与 OIDC `nonce`**：
   - 每次发起 PKCE 授权均生成全新 24 字节（192-bit）CSPRNG `state`（若请求包含 `openid` scope 或启用 OIDC，则同步生成 24 字节 `nonce`）。
   - 回调处理使用 `subtle.ConstantTimeCompare` 比对 `state`；缺失、不匹配或重复回调立即拒绝并返回 `*OAuthError{Category: OAuthErrStateMismatch}`。
3. **临时 Loopback 监听器生命周期**：
   - 默认监听 `127.0.0.1:0`（或配置指定的本地回环端口），仅绑定本地回环网卡，绝不监听 `0.0.0.0` 或 `::`。
   - 仅接受单次匹配回调路径（如 `/oauth/callback`）的 `GET` 请求；收到首个有效或无效 OAuth 回调、上下文取消或超时后，立即异步执行 `server.Shutdown` 释放端口。
   - 回调 HTML 响应设置 `Cache-Control: no-store`、`Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'`，不回显任何敏感参数。
4. **仅限用户显式触发**：
   - 打开项目、切换请求 Tab、后台校验或非交互式批量运行（`Interactive: false`）绝不自动弹出浏览器或启动 Loopback 监听。

### 3.2 Client Credentials (`client_credentials`, RFC 6749 §4.4)
1. 支持 `client_secret_basic`（默认，按 RFC 6749 §2.3.1 进行 URL-encoding 后 Basic 编码）、`client_secret_post` 与 `none`。
2. 支持 `scope`、`audience`、`resource` 及自定义 `extraParams` 表单参数。
3. 严禁假设服务端返回 `refresh_token`；当 Access Token 过期且无 `refresh_token` 时，`EnsureValidToken` 自动通过 `client_credentials` 重新获取新令牌，天然适配非交互式 Runner。

### 3.3 Device Authorization Grant (`device_code`, RFC 8628)
1. **两阶段可观测契约**：
   - 第一阶段 `StartDeviceAuthorization(ctx, ...)` 请求 `deviceAuthorizationUrl`，解析并校验 `device_code`、`user_code`、`verification_uri`（或兼容字段 `verification_url`）、可选 `verification_uri_complete`、`expires_in` 与 `interval`（默认最小 5 秒）。
   - 第二阶段 `PollDeviceToken(ctx, ...)` 按间隔轮询 Token Endpoint（`grant_type=urn:ietf:params:oauth:grant-type:device_code`）：
     - `authorization_pending`：保持当前轮询间隔继续等待；
     - `slow_down`：严格遵循 RFC 8628 §3.5，将后续轮询间隔永久增加 `+5s`；
     - `access_denied`：立即终止轮询并归类为 `OAuthErrAccessDenied`；
     - `expired_token` 或超过 `expires_in`：立即终止轮询并归类为 `OAuthErrExpiredDeviceCode`；
     - `ctx.Done()`（用户点击取消或关闭窗口）：立即中断等待并返回 `context.Canceled`。
2. **非交互式保护**：
   - 当 `Interactive == false` 且无已缓存有效 Token 时，Device Flow 直接失败并返回 `OAuthErrInteractionRequired`，禁止在无人值守模式下无限阻塞。

---

## 4. 刷新轮换、并发单飞 (Singleflight) 与吊销契约

1. **预过期窗口 (Expiry Skew)**：
   - 默认提前 `30s`（可配置）判定 Access Token 即将过期并触发静默刷新。
2. **按隔离键并发合并 (Singleflight)**：
   - 同一隔离键（同一项目克隆 + 鉴权方案 + Issuer + ClientID + Scopes）下的并发 `EnsureValidToken` / `RefreshToken` 调用共享同一个飞行中的刷新请求。
   - 这保证了在启用 **Refresh Token Rotation（单次使用刷新令牌轮换）** 的服务端下，多个并发请求不会拿同一个旧 `refresh_token` 并发撞击 Token Endpoint 导致令牌家族被服务端判定重放而吊销。
3. **原子轮换更新与保留策略**：
   - 若刷新响应返回新 `refresh_token`，立即替换持久化存储中的旧 `refresh_token`；若服务端未返回新 `refresh_token`，保留原 `refresh_token`。
   - 若刷新响应返回新 `id_token`，同步执行 OIDC 验签与 Claims 校验（含與原 ID Token 一致的 `iss`/`sub` 校验）后再更新。
   - 若刷新返回 `invalid_grant`：标记缓存记录为需重新授权（清除失效的 `access_token`/`refresh_token`），并返回明确的 `OAuthErrReauthRequired`。
4. **本地清理 vs 远端吊销 (RFC 7009)**：
   - `ClearLocalToken`：仅从本地隔离缓存（内存 + OS Keychain）移除令牌。
   - `RevokeToken`：无论远端吊销是否成功，均**先行或最终确保清除本地令牌**；同时返回结构化 `RevokeResult{LocalCleared: true, RemoteAttempted: bool, RemoteSucceeded: bool, RemoteError: error}`，以便 UI 明确区分“本地已清除且服务端已吊销”与“本地已清除但服务端不支持/网络失败”。

---

## 5. OIDC Discovery、JWKS 有界缓存与 ID Token 校验契约

### 5.1 OIDC Discovery (`/.well-known/openid-configuration`)
1. 支持传入规范化 Issuer URL（自动拼接 `/.well-known/openid-configuration`）或完整 Discovery URL。
2. **Issuer 严格匹配**：响应 JSON 中的 `issuer` 字段必须与预期 `Issuer` 精确匹配（去除尾部斜杠规范化容差仅在显式允许时启用，默认按 OpenID Connect Discovery 1.0 §4.3 要求逐字符严格相等），否则拒绝并返回 `OAuthErrDiscoveryMismatch`。
3. 响应体大小限制为 `1 MiB`，防止恶意端点耗尽内存。

### 5.2 JWKS 有界缓存与密钥轮换 (Key Rotation)
1. **有界资源限制**：
   - 单个 JWKS 响应最大字节数：`1 MiB` (`MaxJWKSPayloadBytes`)；
   - 单个 JWKS 最多解析密钥数：`64` (`MaxJWKSKeys`)；
   - 缓存条目上限：`32` 个不同 `jwks_uri`（LRU/最早过期淘汰）；
   - 默认缓存 TTL：`15m`（可通过 `Cache-Control: max-age` 在 `[1m, 24h]` 范围内自适应）。
2. **未知 `kid` 触发防抖轮换刷新**：
   - 当 ID Token JWS Header 携带的 `kid` 不在当前缓存的 JWKS 中时，若距离上次针对该 `jwks_uri` 的网络拉取已超过最小冷却时间（默认 `30s`，`MinJWKSRefreshInterval`），则自动强制重新拉取一次 JWKS 以无缝支持服务端密钥轮换；若仍在冷却期内，则直接拒绝，防止伪造随机 `kid` 触发对 JWKS 端点的 DoS 放大攻击。
3. **离线与网络错误分类**：
   - 当网络不可达或 TLS 校验失败时，返回明确的 `OAuthErrJWKSFetchFailed`，绝不静默跳过签名验证。

### 5.3 ID Token 签名与 Claims 校验
1. **JWS 算法白名单**：
   - 默认允许：`RS256`, `RS384`, `RS512`, `PS256`, `PS384`, `PS512`, `ES256`, `ES384`, `ES512`, `EdDSA`。
   - 严禁 `alg: "none"` 及任何对称算法（`HS256`/`HS384`/`HS512`）作为 OIDC JWK 验签算法，彻底杜绝公钥伪装 HMAC 密钥攻击。
   - 若 JWK 声明了 `use`，必须为 `"sig"`；若 JWK 声明了 `alg`，必须与 JWS Header 的 `alg` 一致。
2. **Claims 强制校验项（OpenID Connect Core 1.0 §3.1.3.7）**：
   - `iss`：非空且与预期 Issuer 完全一致；
   - `sub`：非空且长度不超过 255 ASCII 字符；
   - `aud`：支持单字符串或字符串数组，必须包含当前 `ClientID`；若 `aud` 含多个值且存在 `azp`，则 `azp` 必须等于 `ClientID`；
   - `exp`：当前时间（减去默认 `60s` 时钟偏移容差 `ClockSkew`）必须严格早于 `exp`；
   - `nbf`（若存在）：当前时间（加上 `ClockSkew`）必须不早于 `nbf`；
   - `iat`：不能为空，且不能领先当前时间超过 `ClockSkew`（防止未来签发伪造）；
   - `nonce`：若授权请求发送了 `nonce`（或 ID Token 包含 `nonce`），必须与请求态中的 `nonce` 常量时间相等；
   - `at_hash`（若 ID Token 包含且同时返回了 `access_token`）：取当前 JWS 算法对应哈希函数（256/384/512）对 `access_token` ASCII 字节做摘要，取左半部分经 Base64URL 无填充编码后与 `at_hash` 常量时间比对。
3. **未校验预览 vs 已校验状态分离**：
   - `InspectIDTokenUnverified(raw)` 仅用于 UI 在无 Discovery/JWKS 配置时查看载荷结构，返回结果强制标记 `Verified: false` 且携带明确警告文案。
   - 只有通过 `VerifyIDToken` 完整签名与 Claims 校验的令牌才会标记 `Verified: true`。
4. **Access Token 与 ID Token 边界**：
   - 默认始终使用 `access_token` 发送业务 API 请求（`UseToken: "access_token"`）。
   - 仅当用户在方案中显式设置 `useToken: "id_token"` 时，才将 `id_token` 注入 `Authorization: Bearer <id_token>`。

---

## 6. 令牌隔离、系统凭据后端与零明文回退

1. **五维隔离键 (`OAuthCacheKey`)**：
   - 缓存键由以下维度规范化拼接并计算 SHA-256 指纹：
     1. `ProjectCloneID`：复用 B05 `ProjectCredentialScope(projectRoot, projectSlug)`（格式 `<slug>:<sha256(canonicalPath)[:16]>`）；
     2. `SchemeID`：鉴权方案 ID（或内联请求路径标识）；
     3. `Issuer / TokenURL`：规范化后的服务端端点；
     4. `ClientID`：客户端标识；
     5. `Scopes + Audience + UseToken + Subject`：排序去重后的权限范围与目标受众。
   - 不同本地项目克隆目录、不同方案、不同 Client/Scope 绝不串用 Token。
2. **零共享文件污染与零明文回退**：
   - `access_token`、`refresh_token`、`id_token`、`client_secret` 绝不写入 `gorequest.yaml`、`auth/*.yaml`、`requests/**/*.yaml` 或任何 Git 跟踪文件。
   - 持久化缓存通过 `internal/platform.KeychainOAuthTokenStore` 写入系统凭据后端（`service = "gorequest.oauth"`）。
   - 当系统凭据后端返回 `errors.ErrUnsupported` 时：
     - 若开启了持久化要求（`RequirePersistentStore: true` 或显式持久化保存），直接返回 `*core.ConfigError`（含 `errors.ErrUnsupported`），**绝不回退写入本地明文 JSON/YAML 文件**；
     - 仅在用户当前内存会话周期内可选择性使用进程内存缓存（进程退出即销毁）。

---

## 7. 目标安全策略 (B04/B05) 与全链路脱敏

1. **HTTPS 与生产环境策略继承**：
   - OAuth Discovery、JWKS、Token、Device、Revocation 端点请求均先经过 B05 `EvaluateTarget(url, policy)` 检查：
     - 除 RFC 8252 允许的本地回环（`localhost` / `127.0.0.1` / `::1`）或策略显式加入 `AllowPlaintextHTTP` 的内部开发主机外，向明文 `http://` 端点发送 `client_secret`、`refresh_token` 或 `access_token` 会被直接拦截（`IdentityErrInsecureHTTP`）。
     - 当 `EnforceTLSVerify: true`（如生产环境）时，严禁对 OAuth/OIDC 端点启用 `InsecureSkipVerify`（拦截并返回 `IdentityErrTLSBypassDenied`）。
2. **跨源重定向凭据剥离**：
   - 所有 OAuth 端点调用默认禁止自动跟随跨源或协议降级重定向；业务请求注入 Bearer Token 后由 B05 `StripCrossOriginCredentials` 保障重定向至其他 Origin 时自动剥离 `Authorization` 头。
3. **Redactor 全量注册**：
   - 每次获取、刷新或加载 Token，以及解析 `client_secret`、`code_verifier`、`device_code` 时，均立即调用 `redactor.RegisterSecret(...)`。
   - 所有错误消息、UI 状态提示、导出诊断日志、执行计划预览（`Plan`）与历史记录快照均经过 `Redactor.RedactString` 清洗，确保不出现明文 Token 或 Secret。
