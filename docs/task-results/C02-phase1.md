# C02-phase1 任务交付报告：HTTP 性能诊断、逐跳时间线、受控低并发采样与原生面板

## 1. 基本信息

- **任务 ID**：`C02-phase1`（隶属 `C02` 第一阶段）
- **开发分支**：`agy/C02-performance`
- **工作树路径**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-C02`
- **共同提交基线**：`00c1bf42b9d15a23ac5b970f7431b895c4239786`
- **规范对齐**：`docs/specs/mock-performance.md` §4–5、`docs/acceptance.md` §3.10、`docs/decisions.md` DEC-0001 / DEC-0005 / DEC-0006、`docs/parallel-work-plan.md` §19

---

## 2. 本次交付范围与文件清单

### 2.1 纯 Go 诊断与采样引擎（`internal/diagnostics/**`）

- `internal/diagnostics/types.go`
  - 定义受控低并发采样参数 `SamplingConfig` 与硬性上限（`Iterations: 1..200`、`Concurrency: 1..8`、`Warmup: 0..20`、`Interval: 0..30s`、`Deadline: 1ms..5m`、`Seed`、`ConnectionStrategy: keep_alive | fresh`），拒绝负数或超界压测参数。
  - 固定携带 `ScopeDisclaimer`，明确声明本能力为受控低并发客户端 HTTP 诊断而非负载/容量压测。
  - 定义阶段观测状态 `StageObsStatus`（`observed`、`not_applicable_reused`、`unobserved`、`incomplete`、`not_applicable`）、逐跳时间线 `HopTimeline`、并发拨号记录 `DialAttempt`、样本记录 `SampleRecord` 与报告结构 `Report`。
- `internal/diagnostics/stats.go`
  - 实现 Nearest-Rank 分位数计算（`NearestRankQuantile`、`ComputeDistribution`）：对完整成功样本（`Outcome == "success" && !Truncated`）计算 `P50 / P90 / P95 / P99 / mean / min / max / n`，满足规范基准向量 `[100, 200, 300, 400, 500] -> P50=300, P90=500, P95=500, P99=500, mean=300, min=100, max=500, n=5`，并在 $n < 20$ / $n < 100$ 时附加样本量分辨率提示。
  - 失败率与失败耗时分布（`FailureLatency`）独立统计，预热样本（`WarmupSamples`）独立记录且不混入测量统计。
  - 各传输阶段（`dns`、`tcp_connect`、`tls_handshake`、`conn_wait`、`request_write`、`server_wait`、`ttfb`、`download`、`hop_total`）只聚合 `ObsObserved` 且完整成功的样本并给出**独立样本数 `n`**；因连接复用未发生（`not_applicable_reused`）、不适用（`not_applicable`）、未观测（`unobserved`）或截断/未完成（`incomplete`）的阶段绝不按 `0 ms` 计入分布。
  - 实现阈值门禁 `EvaluateGate`：要求同时声明分位数、最大耗时阈值、最少有效样本数与最大失败率；当有效样本数 `ValidSamples < MinValidSamples` 时固定判定为 `insufficient_samples`（`Passed = false`、`Status = gate_failed`），绝不假通过。
- `internal/diagnostics/trace.go`
  - 通过在 `ctx` 上挂载组合式 `httptrace.ClientTrace`（`sampleTraceCollector`），捕获每一跳的 `GetConn -> GotConn`、`DNSStart -> DNSDone`、并发 `ConnectStart -> ConnectDone`、`TLSHandshakeStart -> TLSHandshakeDone`、`WroteRequest` 与 `GotFirstResponseByte` 单调时钟相对偏移区间 `[startOffsetMs, endOffsetMs]`。
  - 保留重叠区间（如 `conn_wait` 与 `dns/tcp_connect/tls_handshake` 重叠、Happy Eyeballs 多路并发拨号重叠并标注 `winner=true`），不将重叠阶段简单相加。
  - 如实记录连接复用、HTTP/SOCKS5 代理、HTTP/2 多路复用以及标准库 `CheckRedirect` 下中间 3xx 跳响应体排空/协议版本不可观测限制。
- `internal/diagnostics/engine.go`
  - 复用 B04（`core.BuildTrackedPlanWithOptions`、`core.ExecuteWithOptions`、`core.ShouldBypassProxy`）与 B05（`core.EvaluateTarget`、`core.Redactor`、`core.SecretProvider`），不复制 HTTP 引擎。
  - Preflight 强制执行 `Interactive=false, Confirmed=false` 目标策略与生产环境门禁；命中生产确认或拒绝规则时返回 `StatusPolicyBlocked` 且 `0` 次请求发出；缺失变量或缺失 `{{secret.*}}` 在 `Prepare` 阶段直接返回 `StatusConfigError`。
  - 针对 B04 `opts.Client == nil` 每次请求新建并关闭 `http.Transport` 导致无法跨样本复用连接的局限，`prepareRunTransport` 在采样周期内构造受控 `*http.Client` / `*http.Transport` 并在 `Run` 结束时执行 `defer tr.CloseIdleConnections()`；当配置依赖 `core` 内部每请求隔离构建器（如 `ProxySOCKS5` 或未冻结的 TLS 文件引用）时，诚实回退并将 `ReuseSupport` 标记为 `"unmeasurable_fallback"`。
- `internal/diagnostics/compare.go`
  - 实现 `CompareReports(baseline, candidate *Report)`：核对 `requestPath`、`requestId`、`method`、`targetUrl`、`snapshotDigest`、`environment`、`connectionStrategy`、`reuseSupport`、`proxyMode`、`concurrency`、`iterations`、`warmup`、`interval`、`seed`、`coreVersion` 差异，区分 `blocking` 与 `advisory` 差异，并附带 `AttributionDisclaimer` 防止将本机网络抖动直接归因为后端性能回归。
- `internal/diagnostics/export.go`
  - 实现脱敏 JSON（`MarshalJSONReport` / `WriteJSONReport`）与 CSV（`MarshalCSVReport` / `WriteCSVReport`）序列化；CSV 中每个阶段成对输出 `<stage>_status` 与 `<stage>_ms`，未发生/复用/不可观测阶段的数值列保持为空字符串 `""`（绝不导出为 `0`）。
  - 实现 `ExportReportFile`：强制要求位于共享项目目录（`projectRoot`）、`.gorequest` 与 `.git` 之外的本地绝对路径，解析父目录符号链接并拒绝最终符号链接目标，使用 `0600` 临时文件 + 原子 `os.Rename`。
- `internal/diagnostics/diagnostics_test.go`
  - 覆盖 `[100, 200, 300, 400, 500]` 分位数基准、参数边界校验、阶段独立 `n` 与连接复用排除、预热排除、成功/HTTP 500/超时/取消/响应体截断分类、门禁样本不足判定、生产策略拦截与缺失凭据拒绝、本地 `httptest` Keep-Alive vs Fresh 连接复用与 302 重定向逐跳时间线、Secret 脱敏、报告对比与导出路径隔离。

### 2.2 原生桌面端性能诊断面板（`internal/desktop/performance*.go`）

- `internal/desktop/performance_i18n.go`：中英双语（`zh-CN` / `en`）文案定义。
- `internal/desktop/performance_snapshot.go`：UI 线程快照冻结器 `prepareDesktopPerformance`，通过 `perfSecrets` 与 `perfFiles` 拷贝并封闭（seal）凭据与项目文件，通过 `freezePerformanceTLS` 在 UI 线程将 TLS 引用冻结为内存 PEM 字节，确保后台采样协程不持有任何 `App` / `projectState` / `Tab` 引用。
- `internal/desktop/performance.go`：按 `weak.Pointer[App]` + `runtime.AddCleanup` + `win.OnClosed` 隔离窗口状态 `performanceState`；实现 256 条有界事件与单次合并 `win.Update` 唤醒的 `performanceMailbox`（最终报告独立槽位永不丢弃）；提供启动、取消、基线固定/清除接口。
- `internal/desktop/performance_ui.go`：提供项目菜单入口 `BuildPerformanceProjectMenuItems` 与模态面板 `PerformanceModals`（含脏状态未保存警告、采样与门禁配置、实时进度、5 个结果标签页 `Summary / Stages / Timeline / Samples / Compare`、本地 JSON/CSV 显式导出与失败可见提示，适配紧凑视口 `1000x700`）。
- `internal/desktop/performance_test.go` 与 `internal/desktop/performance_entry_integration_test.go`（`//go:build c02_integration`）：桌面端快照封闭、有界邮箱、显式导出失败提示、脏状态警告、紧凑视口可达性、多窗口状态隔离与公共入口集成用例。

### 2.3 规范证据与公共接线补丁

- `docs/specs/performance-diagnostics-evidence.md`：C02-phase1 报告格式、B04 局限处理、统计口径与导出边界证据文档。
- `docs/task-results/C02-phase1-integration.patch`：`internal/desktop/app.go` 与 `internal/desktop/workspace.go` 的最小两行公共接线补丁。

---

## 3. Git 提交记录

1. `5810ee5` — `feat(diagnostics): add C02-phase1 HTTP performance diagnostics engine and export`
2. 第二阶段提交（包含 `internal/desktop/performance*.go`、`docs/task-results/C02-phase1-integration.patch`、`docs/task-results/C02-phase1.md` 及 `internal/diagnostics` 符号链接/测试修正，见最终汇报哈希）。

---

## 4. 验证状态说明（按用户指令未执行构建与测试）

> **重要说明（诚实记录，不伪造通过）**：
> 按照用户在本轮会话中的明确约束（**“先不用执行测试”**），本轮仅执行了源码静态核对、`gofmt` 格式化与 `git diff` 检查，**未执行** `go test`、`go build`、`go vet` 或启动真实网络服务。所有新增单元测试、桌面端测试与集成测试均已编写就绪，当前状态为**未运行验证 / 待集成验收**。

### 后续验收与回归执行计划（供集成人或后续会话执行）

1. **纯 Go 诊断引擎与导出回归测试**：
   ```bash
   go test -v ./internal/diagnostics -run 'TestNearestRankQuantiles_KnownSample5|TestSamplingConfig_BoundsValidation|TestAggregateSamples_IndependentStageNAndReuse|TestEngine_WarmupFailuresTruncatedAndGateIndeterminate|TestEngine_PolicyBlockedAndMissingSecretPreflight|TestEngine_LocalHTTPTest_KeepAliveVsFreshAndRedirects|TestRedactionAndExportConfinement|TestCompareReports_ConfigParityAndAttribution'
   ```
2. **桌面端性能诊断面板单测（未打公共 patch 前即可独立运行）**：
   ```bash
   go test -v ./internal/desktop -run 'TestPerformanceMailboxBoundedAndFinalReport|TestPerformanceSnapshotProvidersSealed|TestPerformanceReportExplicitExportAndVisibleFailure|TestPerformanceNativePanelDirtyWarningAndCompactViewport|TestPerformanceWindowIsolationAndBoundsRejection'
   ```
3. **打入公共接线补丁后的端到端入口验证**：
   ```bash
   git apply --check docs/task-results/C02-phase1-integration.patch
   git apply docs/task-results/C02-phase1-integration.patch
   go test -v -tags c02_integration ./internal/desktop -run TestC02PatchedAppPerformanceEntry
   ```
4. **静态检查与竞态门禁**：
   ```bash
   go vet ./internal/diagnostics/... ./internal/desktop/...
   go test -race ./internal/diagnostics/...
   ```

---

## 5. 精确公共文件接线说明

合入时仅需应用 `docs/task-results/C02-phase1-integration.patch`（不触碰 `FIX08` 的 `network_ui.go` 或 `FIX09` 的 `environment_editor.go` / `identity_management*.go`）：

1. `internal/desktop/app.go`（在 `View` 的 `a.RunnerModals(c, t)` 后增加一行）：
   ```go
   a.PerformanceModals(c, t)
   ```
2. `internal/desktop/workspace.go`（在 `buildProjectMenu` 的 `a.BuildRunnerProjectMenuItems(mm)` 后增加一行）：
   ```go
   a.BuildPerformanceProjectMenuItems(mm)
   ```

---

## 6. 剩余工作与阶段边界说明

- 本次完成的是 `C02-phase1`（受控低并发 HTTP 性能诊断、逐跳时间线、Nearest-Rank 分位数与独立 `n`、阈值门禁、基线对比、原生面板与 JSON/CSV 导出）。
- 已知边界与后续扩展项（已写入报告 `Limitations` 与 `docs/specs/performance-diagnostics-evidence.md`）：
  1. 当使用 `SOCKS5` 代理时，由于 `core` 的 SOCKS5 拨号器位于未导出的每请求构建路径中，`StrategyKeepAlive` 下无法跨样本共享连接池，引擎诚实标记 `ReuseSupport = "unmeasurable_fallback"`，不伪造复用支持；后续若合入公共 `internal/core` 扩展，可将 `buildProxyConfiguration` 复用到跨样本客户端。
  2. 标准库 `net/http` 在自动跟随 3xx 重定向时不向 `httptrace` 暴露中间跳的 HTTP 协议版本文字列与中间响应体排空耗时，报告显式标记为 `unobserved`。
  3. 本轮按用户指令跳过了 `go test` / `go vet` 实际运行，需在集成验收阶段执行第 4 节所列命令全部通过后，再由集成人在 `docs/parallel-work-plan.md` 与 `docs/implementation-status.md` 中勾选 `C02`。
