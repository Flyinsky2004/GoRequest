# P01 WebSocket 与共享流：实现证据与兼容清单

状态：phase 1 草稿（2026-11-07）。本文件记录选型、契约与**待执行**验证；本批未运行 go test/go build/go vet，未启动真实应用或网络服务，因此以下“验证”均为静态核对或待执行用例，不代表已通过。

## 1. 依赖选型

| 项 | 值 | 依据 |
| --- | --- | --- |
| 库 | `github.com/gorilla/websocket` | 成熟、广泛使用、BSD-2-Clause；只使用其握手/帧/控制帧实现，不自制协议 |
| 版本 | `v1.5.3`（固定） | 本机模块缓存 `github.com/gorilla/websocket@v1.5.3` 已存在；`go.sum` 记录 h1 校验 |
| 变更范围 | 仅 `go.mod`/`go.sum` 增加该直接依赖 | 本卡是本批唯一允许修改模块文件的 agent |
| 协议能力 | RFC 6455 握手、Text/Binary、Ping/Pong、Close、分片重组、可选 permessage-deflate | gorilla 客户端实现；本包只编排生命周期与事实记录 |

许可：BSD-2-Clause（见模块内 LICENSE）。未引入其他新依赖。

## 2. 共享有界事件流契约（`internal/stream`）

- 事件字段：`Seq`（日志级单调序号）、`Time`（UTC）、`StreamID`（会话关联）、`OpID`（操作关联，如 ping→pong）、`Direction`（in/out/none）、`Kind`、`Summary`、`Payload`、`IsText`、`Truncated`、`PayloadLen`、`Sensitive`、`Code`、`Reason`。
- 有界性：`Capacity` 环形上限（默认 500）、`MaxPayloadBytes` 单事件载荷上限（默认 64 KiB）。溢出/截断/敏感计数在 `Stats` 可观察；`LastOverflow` 给出最近一次驱逐对应的事件序号。
- 游标：`SnapshotSince(Cursor)` 无丢失读取（已被环形驱逐的事件不可恢复，缺口通过 `Stats.Overflowed` 观察）；`Latest()` 返回最新序号。
- 背压：`Subscribe(ch)` 非阻塞投递，满通道计入 `Stats.SubscriberDrops`；慢消费者用游标读取保证不丢。
- 脱敏：`Sensitive` 事件在 append 时经 `Options.Redact`（优先）或 `Options.Redactor` 处理；二进制载荷整体掩码为 `***`；`PayloadLen` 保留原始长度。`SanitizedSnapshot/SanitizedSince` 供报告/夹具导出。
- 取消/超时：`DeadlinePolicy{Connect, Handshake, Message, Total}` 为适配器必须遵守的四类时限；`Message` 不是空闲超时。
- 无 UI/网络依赖：该包不启动 goroutine、不做 I/O。

## 3. WebSocket 会话（`internal/websocket`）

### 生命周期与事实

- 状态：`disconnected`（未建会话）、`connecting`、`open`、`closing`、`closed`、`error`（终态错误通过 `Snapshot.LastErr`/事件表达）。
- 结束原因：`user_closed`、`server_closed`、`aborted`、`total_deadline`、`lost_connection`、`handshake_failed`、`config_rejected`。
- 事实快照：HTTP 状态、协商子协议、远端地址、代理标签（无凭据）、TLS 版本/套件/SNI/是否跳过校验、压缩是否协商、连接/握手/消息/总体时限、消息上限、环形容量、收件容量、收发/ping/pong 计数、收件驱逐数、流统计、关闭码与脱敏原因。
- 错误握手：保留 HTTP 状态、响应头与受限 body 摘要（`HandshakeError`）；`Set-Cookie`/`WWW-Authenticate` 等敏感响应头值直接掩码。
- 断流：读错误映射为 `lost_connection` 事实，绝不伪装成功；关闭帧路径记录对端关闭码/原因。

### 资源边界

| 边界 | 默认 | 行为 |
| --- | --- | --- |
| 单条消息 | 1 MiB | `SetReadLimit`；超限读失败并记录错误事实 |
| 事件环 | 500 条 | 超出驱逐最旧事件并计数 |
| 单事件载荷预览 | 64 KiB | 截断并标记 `Truncated`，保留原始长度 |
| 收件（完整载荷） | 200 条 | 超出驱逐并计数 `InboxDropped` |
| 连接/握手/消息 | 10s/15s/10s | 可在 `Deadlines` 覆盖 |
| 总体 | 0（不限） | 用户可设；触发时取消连接、记录事实、1001 关闭 |

无自动重连、无自动连接、无自动 keep-alive ping、无无限缓冲。

### 代理与 TLS

- 目标策略：发网前 `core.EvaluateTarget`，WebSocket 按可变流量（method `WEBSOCKET`）评估；`ws://` 映射为 `http://`、`wss://` 映射为 `https://` 参与判定。需要交互确认时返回 `*core.ConfigError` 包装的 `ErrConfirmRequired`；非交互调用直接拒绝。
- 代理：`none` 直连；`system` 读取环境（`HTTP_PROXY`/`HTTPS_PROXY`/`NO_PROXY`，Go 对 loopback 自动直连）；`http`/`https` 由本包实现 CONNECT 隧道（含 `Proxy-Authorization: Basic`，`https` 代理先做 TLS）；`socks5`/`socks5h` 使用 gorilla 内置 RFC 1928/1929 客户端。`NoProxy` 在所有模式生效。无法支持的代理 scheme 在发网前以 `*core.ConfigError` 拒绝，不静默忽略。
- TLS：复用 `core.TLSConfig`（系统 CA、自定义 CA、mTLS、SNI、最低版本）；`InsecureSkipVerify` 在 `MergedTargetPolicy.EnforceTLSVerify` 下预检拒绝；mTLS 私钥只允许 `local:<slot>`，仓库路径拒绝；PEM 读取有 4 MiB 上限且要求常规文件。
- 敏感值：握手敏感头值、代理凭据、mTLS 私钥在会话 redactor 注册；发送时解析出的 Secret 通过 `Session.RegisterSecret` 注册后再 append，避免日志/报告泄漏。

### 手工 UI（`internal/desktop/websocket*.go`）

- 入口：项目菜单 “WebSocket Session...”（接线 patch）。
- 独立模态：URL、子协议、握手 Headers（多行编辑器）、permessage-deflate、总体超时；连接前显示策略确认步骤。
- 会话视图：状态徽标、网络/TLS/时限/限额事实；时间线（方向与文本过滤、最多 200 行/帧）、详情（文本或二进制 hex/sha256 预览）、选中消息保存；Text/Binary 发送、Ping、优雅关闭（码+原因）、断开、清除视图。
- 生命周期：关闭模态或窗口即 `Terminate`；后台回调通过 `weak.Pointer[App]` + generation 校验回到 UI，不持有全局 UI 强引用；事件泵用有界订阅通道，丢投递在流统计中可观察。
- 保存：选中消息写入用户选择的本地路径；敏感发送的保留载荷在 append 时已脱敏，因此保存内容同样脱敏。

## 4. 自动化/后续适配接口（不依赖 UI）

- `websocket.Connect(ctx, Options) (*Session, error)`：失败时仍返回带事实日志的 `Session`（除非调用方不需要）。
- `Session.Events() *stream.Log`：游标读取或订阅；`StreamID()` 关联。
- `Session.Snapshot()`、`Inbox()`、`InboxFind(seq)`、`State()`。
- `Session.SendText/SendBinary(ctx, payload, sensitive)`、`Ping(ctx)`、`CloseWith(code, reason)`、`Terminate()`、`RegisterSecret(value)`。
- A01 流式调度可直接消费 `stream.Log` 的 `SnapshotSince`/`Subscribe` 与 `Stats`；P02 GraphQL Subscription 可复用 `stream` 与 `DeadlinePolicy`，只需替换协议适配器；P03 gRPC 流可复用同一事件契约，方向/关联 ID/关闭事实按 gRPC Status 映射。
- 完整 `connect → send → await/assert → close` 声明式 Runner 调度不在本阶段。

## 5. 兼容清单与已知限制

- 分片：gorilla 自动重组逻辑消息；UI 以逻辑消息为行，不展示帧边界。
- 压缩：可选协商 permessage-deflate；事实行标注是否协商。
- Cookie：本阶段通过握手 `Cookie` 头手工提供；项目 CookieJar 自动注入未接入，属后续适配。
- 二进制文件发送：本阶段提供十六进制输入；文件选择发送未实现。
- 自动重连：不提供（手工调试的显式重连也需关闭模态重开，且确认不复用）。
- 代理：HTTP/HTTPS CONNECT 与 SOCKS5 已实现；其他 scheme 预检拒绝。HTTPS 代理使用系统根证书校验代理本身，未提供独立代理 CA 配置。
- 平台：仅在 macOS 开发环境做静态核对；Windows/Linux 原生 UI 未验收。

## 6. 待执行验证（本批未运行）

1. `gofmt -l internal/stream internal/websocket internal/desktop/websocket_*.go`（已执行，干净）。
2. `git diff --check`（已执行，干净）。
3. `go test ./internal/stream ./internal/websocket`（未运行；测试文件已提供，使用 loopback `httptest`）。
4. `go test -race ./internal/stream ./internal/websocket`（未运行）。
5. `go test ./internal/desktop`（未运行；新增 UI 代码需随集成 patch 一起编译）。
6. 手工：本地 `wss` 回显服务验证 Text/Binary、Ping RTT、服务端主动关闭码、错误握手、异常断流、总体超时、取消后服务端观察到断开。
7. 手工：系统/HTTP/SOCKS5 代理与 `NO_PROXY`、自定义 CA、mTLS、`InsecureSkipVerify` 策略拒绝。
8. 跨平台：Windows/Linux 窗口与原生对话框。
