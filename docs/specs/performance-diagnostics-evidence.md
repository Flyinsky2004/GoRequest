# C02-phase1：HTTP 性能诊断、逐跳时间线与受控采样实现说明（Evidence）

> 状态：C02-phase1 实现与证据文档（不修改已冻结规范或总账本）
> 对齐规范：`docs/specs/mock-performance.md` §4–5、`docs/acceptance.md` §3.10、`docs/decisions.md` DEC-0001 / DEC-0005 / DEC-0006

---

## 1. 范围边界与非压测声明

`internal/diagnostics` 与 `internal/desktop/performance*.go` 实现**单请求受控低并发 HTTP 诊断采样**，用于定位客户端视角下的连接建立、TLS 握手、首字节等待（TTFB）、响应体接收、连接复用与重试/重定向耗时。

- **硬性上限**：
  - `Iterations`: `1..200`（默认 `10`）
  - `Concurrency`: `1..8`（默认 `1`）
  - `Warmup`: `0..20`（默认 `0`）
  - `Interval`: `0s..30s`
  - `Deadline`: `1ms..5m`（默认 `60s`）
- **非压测声明**：所有报告固定携带 `scopeDisclaimer`，明确本能力不提供高并发压测、分布式施压或服务端容量评估。
- **零自动网络与门禁守卫**：
  - 打开项目、切换请求或打开性能诊断模态框均不发送任何网络请求。
  - 执行前通过 `core.EvaluateTarget`（`Interactive=false, Confirmed=false`）执行目标策略与生产环境检查；若命中 `DecisionConfirmRequired` 或 `DecisionDeny`，直接返回 `StatusPolicyBlocked` 且 `0` 次请求发出。
  - 缺失变量或缺失 `{{secret.*}}` 在 `Prepare` 阶段直接拒绝（`StatusConfigError`），绝不发送半成品或空凭据请求。

---

## 2. B04 现有实现局限核查与 C02 连接策略/逐跳时间线设计

### 2.1 B04 `core.ExecuteWithOptions` 的连接生命周期与 Trace 局限

通过审查 `internal/core/plan.go` 与 `internal/core/network.go`，确认基线 B04 存在以下两项关键行为：

1. **默认 `opts.Client == nil` 时无法跨样本复用连接**：
   - `core.ExecuteWithOptions` 在 `opts.Client == nil` 时每次调用都会新建 `ownTrans := &http.Transport{...}` 并注册 `defer ownTrans.CloseIdleConnections()`。
   - 若直接以 `opts.Client == nil` 循环执行采样，即使配置 `keep_alive` 也会在每次请求返回前关闭空闲连接，导致跨样本连接复用恒为 `false`。
2. **默认 `ExecResult.Trace` 为单次执行聚合结构**：
   - `core.NetworkTrace` 在多跳重定向或 Happy Eyeballs 并发拨号下只保留最终/累计字段，不保留每跳独立的 `GetConn -> GotConn`、`DNSStart -> DNSDone`、并发 `ConnectStart -> ConnectDone` 区间以及中间 3xx 跳的观测边界。

### 2.2 C02-phase1 的兼容解决方案

1. **Run 作用域受控 `*http.Client` / `*http.Transport`（兼容 Core 关闭规则）**：
   - `Engine.prepareRunTransport` 在采样开始时构造与 `core` 一致的 `*http.Transport`（复用 `core.ShouldBypassProxy`、TLS 最小版本/证书池/mTLS、阶段超时），并在 `Engine.Run` 退出时执行 `defer tr.CloseIdleConnections()`。
   - `StrategyKeepAlive`：`DisableKeepAlives = false`，允许同源样本间复用空闲连接。
   - `StrategyFresh`：`DisableKeepAlives = true`，强制每个样本新建连接。
   - **诚实降级（不伪造支持）**：当选项依赖 `core` 内部未导出的每请求构建器（如 `ProxySOCKS5` 或未冻结到内存 PEM 的 TLS 文件引用）时，回退至 `opts.Client = nil`，并将报告 `reuseSupport` 标记为 `"unmeasurable_fallback"`，同时在 `reuseNote` 与 `limitations` 中向用户说明原因。
2. **上下文组合式 `httptrace.ClientTrace` 逐跳采集 + 可选公共扩展补丁**：
   - Go 标准库 `httptrace.WithClientTrace` 支持在 `ctx` 上叠加钩子。`diagnostics` 在调用 `Executor.Execute` 前挂载 `sampleTraceCollector`，按 `GetConn` 切分跳（Hop），记录单调时钟相对偏移区间 `[startOffsetMs, endOffsetMs]`。
   - 重叠区间（如 `conn_wait` 与 `dns` / `tcp_connect` / `tls_handshake` 重叠、Happy Eyeballs 多路并发拨号重叠）完整保留各自的 `[startOffsetMs, endOffsetMs]`，并在 `DialAttempts` 中标记最终获胜连接 `winner=true`，绝不将重叠阶段简单相加。
   - 对于标准库 `net/http` 未暴露的中间 3xx 跳协议版本与响应体排空耗时，显式标记为 `unobserved` 并写入 `HopTimeline.Limitations`。
   - 同时在 `docs/task-results/C02-phase1-integration.patch` 中提供了最小公共接线补丁。

---

## 3. 统计口径、独立 `n` 与阈值门禁契约

1. **Nearest-Rank 分位数**：
   - 仅统计 `Outcome == "success"` 且 `Truncated == false` 的完整测量样本（预热样本 `WarmupSamples` 独立记录，不混入测量统计）。
   - 排序后按 $P_q = x[\lceil q \times n \rceil]$（1-based）计算 `P50`、`P90`、`P95`、`P99`。
   - 基准向量 `[100, 200, 300, 400, 500]` ms 恒得：`P50 = 300`、`P90 = 500`、`P95 = 500`、`P99 = 500`、`mean = 300`、`min = 100`、`max = 500`、`n = 5`。
   - 当 $n < 20$ 或 $n < 100$ 时，在 `DistributionStats.ResolutionNote` 中输出样本量分辨率提示。
2. **阶段独立样本数 `n`（禁止将未发生/复用记为 `0 ms`）**：
   - 每个阶段（`dns`、`tcp_connect`、`tls_handshake`、`conn_wait`、`request_write`、`server_wait`、`ttfb`、`download`、`hop_total`）各自维护独立的 `StageSummary`：
     - `n` (`ObservedCount`)：实际发生且完整观测的样本数。
     - `skippedReusedCount`：因连接复用未发生的样本数（状态 `not_applicable_reused`）。
     - `notApplicableCount`：不适用样本数（如明文 HTTP 的 TLS，或 `HEAD`/`204`/`304` 的 `download`）。
     - `incompleteCount`：截断、超时或中途失败的样本数。
     - `unobservedCount`：代理隧道远端解析或合成执行器未观测的样本数。
3. **阈值门禁（`ThresholdGate`）**：
   - 必须同时声明 `quantile`、`maxLatencyMs`、`minValidSamples`、`maxFailureRate`。
   - 当 `ValidSamples < MinValidSamples` 时，门禁结果固定为 `status = "insufficient_samples"`、`passed = false`，报告整体状态为 `gate_failed`，绝不因样本不足而假通过。

---

## 4. JSON / CSV 显式导出格式契约

- **JSON 报告 (`ReportFormatVersion = 1`)**：
  - 顶层包含 `formatVersion`、`scopeDisclaimer`、`runId`、`requestId`、`requestPath`、`method`、`targetUrl`、`snapshotDigest`、`environment`、`coreVersion`、`config`、`proxyMode`、`reuseSupport`、`summary`、`successLatency`、`failureLatency`、`stageStats`、`gateResult`、`comparison`、`warmupSamples`、`samples`、`limitations`。
- **CSV 报告**：
  - 每行对应一个预热或测量样本（`phase` 区分 `warmup` / `measured`）。
  - 每个阶段成对输出 `<stage>_status` 与 `<stage>_ms` 两列；当 `<stage>_status != "observed"` 或样本非完整成功时，`<stage>_ms` 单元格保持为空字符串 `""`，绝不导出为 `0`。
- **路径隔离与脱敏**：
  - 所有报告字段在内存构建与导出序列化时双重经过 `core.Redactor`。
  - `ExportReportFile` 拒绝写入项目根目录（`projectRoot`）、`.gorequest`、`.git` 或任何符号链接路径，使用 `0600` 临时文件原子替换。
