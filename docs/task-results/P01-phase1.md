# P01-phase1 交付：WebSocket 与共享流生命周期

日期：2026-11-07
worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-P01-ws`
分支：`vibe/P01-websocket`
共同基线：`b91ba32fdb7b6c5c039bd74701da75f83bab27d7`
代码提交：`e8af406`（本报告随后单独提交）

## 1. 交付内容

| 范围 | 文件 | 说明 |
| --- | --- | --- |
| 共享有界流契约 | `internal/stream/stream.go`、`id.go` | 事件方向/序号/时间/关联 ID/脱敏/容量/游标/背压统计/四类 deadline |
| WebSocket 会话 | `internal/websocket/session.go`、`control.go`、`options.go`、`errors.go`、`proxy.go`、`tls.go` | 连接状态、握手、Text/Binary、Ping/Pong、关闭码原因、错误握手/断流事实、有界收发 |
| 原生 UI | `internal/desktop/websocket_state.go`、`websocket_ui.go`、`websocket_i18n.go` | 独立会话模态、时间线/过滤/详情/保存、发送/Ping/关闭/断开 |
| 依赖 | `go.mod`、`go.sum` | 固定 `github.com/gorilla/websocket v1.5.3`（BSD-2-Clause） |
| 证据/接线 | `docs/specs/websocket-evidence.md`、`docs/task-results/P01-phase1-integration.patch` | 选型、契约、兼容清单、待执行用例；公共接线最小 patch |
| 未运行用例 | `internal/stream/stream_test.go`、`internal/websocket/session_test.go` | 纯本地/loopback 用例，本批未执行 |

## 2. 功能

- 共享流：`Seq` 单调、`Cursor` 增量读取、`Subscribe` 非阻塞投递、`Stats` 暴露容量/溢出/截断/敏感/订阅丢弃；`Sensitive` 在 append 时脱敏（支持 owner 提供的串行化 `Redact` 函数）；`SanitizedSnapshot` 供报告导出。
- 会话：`disconnected/connecting/open/closing/closed/error` 展示（终态失败以 ERROR 徽标呈现）；连接/握手/消息/总体 deadline；取消；手动 Ping RTT；优雅关闭（码/原因）与强制终止；不自动连接、不静默重连、不自动 keep-alive、不无限缓冲。
- 事实：HTTP 状态、子协议、远端、代理标签、TLS 版本/套件/SNI/是否跳过校验、压缩协商、收发/ping/pong 计数、收件驱逐、关闭码/原因、结束原因。
- 安全：发网前 `core.EvaluateTarget`（按 `WEBSOCKET` 可变流量）；`ws/wss` 映射 `http/https`；确认一次性使用；HTTP/HTTPS 代理自实现 CONNECT（含 Basic 认证，HTTPS 代理先 TLS），SOCKS5 复用 gorilla 内置客户端，`NoProxy` 生效，未知 scheme 预检拒绝；TLS 复用 `core.TLSConfig`，`InsecureSkipVerify` 受策略约束，mTLS 私钥仅 `local:<slot>`；敏感头值/代理凭据/私钥/发送时 Secret 注册到会话 redactor。
- UI 生命周期：关闭模态或窗口即终止连接；后台回调经 `weak.Pointer[App]` + generation 回到 UI；事件泵有界，丢投递可在流统计观察。

## 3. 公共接线（未提交，需集成人应用）

`docs/task-results/P01-phase1-integration.patch` 相对共同基线，仅两处：

```diff
 internal/desktop/app.go:      + a.WebSocketModals(c, t)
 internal/desktop/workspace.go:+ a.BuildWebSocketProjectMenuItems(mm)
```

应用方式（在集成工作区）：

```bash
git apply docs/task-results/P01-phase1-integration.patch
```

本 worktree 已临时应用并反向撤销验证过 patch 可应用；`app.go`/`workspace.go` 未纳入提交。`WebSocketView(c)` 供 headless UI 测试包装 `View + WebSocketModals`。

## 4. 静态验证（已执行）

- `gofmt -l internal/stream internal/websocket internal/desktop/websocket_*.go` → 干净。
- `git diff --check` → 干净。
- `go list ./internal/stream ./internal/websocket ./internal/desktop` → 包解析通过（不进行类型检查）。
- `git apply --check docs/task-results/P01-phase1-integration.patch` → 可应用。
- 逐项核对真实源码/模块缓存签名：`core.Redactor/ConfigError/RunError/EvaluateTarget/TargetCheckInput/MergedTargetPolicy/ProxyConfig/TLSConfig/ParseFileRef/NewDirFileOpener/FormatBinaryPreview/NewResolver`，`uikit.Palette/ActionCursor/SimpleSegmented/EditorTheme`，`ui.Modal/TextInput.Placeholder/Element.*`，`editor.State{Buffer,Theme,ReadOnly}/View/NewBuffer/SetLanguage/LanguageNamed`，`mygo.Dialog.Save/SaveDialogOptions`，`desktop.window/nativeWindower/buildExecuteOptionsForTab/projectState`，`gorilla/websocket v1.5.3` 的 `Dialer/Conn/ErrBadHandshake/FormatCloseMessage`。
- 核对中修正的草稿问题：`core.ConfigErrorNew` 不存在、`uikit.Color` 不存在、`PlaceholderText` 应为 `Placeholder`、缺少 `headersEditor` 字段、gorilla 不支持 HTTP 代理（改为自实现 CONNECT）、敏感响应头未实际掩码、发送时 Secret 未注册到会话 redactor、总 deadline 未中止进行中的拨号、关闭路径未停止定时器/取消上下文、窗口销毁时可能遗留会话。

## 5. 未执行/未验收（重要）

本批遵守“先不执行测试”：**未运行** `go test`、`go build`、`go vet`，**未编译**，未启动真实应用、真实网络、Mock 监听或生产服务。因此：

- 代码尚未经过编译器/类型检查器验证；上述静态核对不能等同于编译通过。
- 未做运行时验证：连接、收发、Ping、关闭、错误握手、断流、超时、取消、代理、TLS/mTLS。
- 未做 UI 手工验收与截图；未做 Windows/Linux 跨平台验收。
- 未验证 `-race`。

待执行用例（建议顺序）：

1. `go test ./internal/stream ./internal/websocket`
2. `go test -race ./internal/stream ./internal/websocket`
3. 应用接线 patch 后 `go test ./internal/desktop`
4. 本地回显服务手工验证 Text/Binary、Ping RTT、服务端主动关闭、错误握手、异常断流、总体超时、取消后服务端观察到断开
5. 代理（system/HTTP/HTTPS CONNECT/SOCKS5/NO_PROXY）、自定义 CA、mTLS、策略拒绝
6. 跨平台窗口与原生保存对话框

## 6. 已知限制与后续

- Cookie：本阶段通过握手 `Cookie` 头手工提供；项目 CookieJar 自动注入未接入。
- 二进制文件发送：仅十六进制输入；文件选择发送未实现。
- 重连：不提供自动重连；手工重连需关闭模态重开，确认不复用。
- 代理：HTTPS 代理使用系统根证书校验代理本身，未提供独立代理 CA 配置。
- 自动化：提供 `Connect/Events/Snapshot/SendText/SendBinary/Ping/CloseWith/Terminate/RegisterSecret`，完整 `connect → send → await/assert → close` Runner 调度另期。
- 事件环/收件容量、消息上限均为显式默认，可在 `Options` 覆盖。

## 7. 给 A01/P02/P03 的接口

- A01 流式调度：消费 `Session.Events()`（`SnapshotSince`/`Subscribe`/`Stats`），以 `StreamID` 关联，用 `Snapshot` 读事实。
- P02 GraphQL Subscription：复用 `internal/stream` 与 `DeadlinePolicy`，替换协议适配器；子协议 `graphql-transport-ws` 可直接通过 `Options.Subprotocols` 协商。
- P03 gRPC 流：复用事件契约，方向/关联 ID/关闭事实按 gRPC Status 映射。
- 所有消费方不依赖 UI；`internal/websocket` 无 MyGo 导入。

## 8. 未做事项声明

- 未修改总账本/`implementation-status`，未勾完成。
- 未修改公共 `core` 模型/入口文件；仅提交本卡专属文件与接线 patch。
- 未 push、未 merge、未删除分支、未操作其他终端、未启动子 agent。
