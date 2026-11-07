# FIX10 交付报告：Mock Checkbox 参数顺序与同批模块编译修复

- **任务 ID**：`FIX10`
- **工作树路径**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX10`
- **开发分支**：`agy/FIX10-mock-checkbox`
- **共同提交基线**：`957ea339967e41e7e774374068007c6f6e5e5e0b`
- **使用模型**：Gemini 4 Argon (Medium)

---

## 1. 根因分析

1. **`internal/desktop/mock_ui.go:87`（主问题：`IncompatibleAssign`）**
   - 本机依赖 `github.com/egoist/mygo` `v0.7.4`（`ui/api_gen.go:834`）中 `ui.Checkbox` 的真实签名为：
     ```go
     func Checkbox(c *Context, checked *bool, label string) Element
     ```
   - `internal/desktop/mock_ui.go:87` 原调用将 `label`（`mockT(a, "mock.field.allow_lan")`，`string`）与布尔指针（`&s.allowLAN`，`*bool`）位置写反，写成了 `ui.Checkbox(c, mockT(a, "mock.field.allow_lan"), &s.allowLAN)`，触发两条 Go 编译器 `IncompatibleAssign` 类型不匹配错误。

2. **同批合入性能诊断模块（`C02-phase1`）暴露的编译与 API 误用**
   - **`internal/diagnostics/trace.go:4`**：导入了未使用的标准库包 `"context"`，导致 `go build ./...` 报错 `internal/diagnostics/trace.go:4:2: "context" imported and not used`。
   - **`internal/desktop/performance.go:125`**：`OpenPerformance()` 调用了不存在的方法 `a.activeTab()`（`*App` 无此方法，活动标签页方法定义在 `*projectState` 上即 `a.ps.activeTab()`），导致 `go build ./...` 报错 `internal/desktop/performance.go:125:14: a.activeTab undefined (type *App has no field or method activeTab)`。
   - **`internal/diagnostics/diagnostics_test.go:220-262`**：静态扫描同批模块时发现，`TestEngine_WarmupFailuresTruncatedAndGateIndeterminate` 构造 `core.NetworkTrace` 字面量时使用了不存在的字段名（`TTFB`、`Download`、`Total`、`TCPObserved`、`TCPConnect`），而 `internal/core/network.go:111` 中 `core.NetworkTrace` 的真实字段为 `TTFBDuration`、`TTFBObserved`、`DownloadDuration`、`ConnectObserved`、`ConnectDuration`。

---

## 2. 具体最小修复

1. **[internal/desktop/mock_ui.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX10/internal/desktop/mock_ui.go)**（第 87 行）：
   - 将：
     ```go
     lanToggle := ui.Checkbox(c, mockT(a, "mock.field.allow_lan"), &s.allowLAN).Disabled(s.running)
     uikit.ActionCursor(lanToggle)
     ```
     修复为：
     ```go
     lanToggle := ui.Checkbox(c, &s.allowLAN, mockT(a, "mock.field.allow_lan")).Disabled(s.running)
     uikit.ActionCursor(lanToggle)
     ```
   - 完整保留 `.Disabled(s.running)`、`uikit.ActionCursor(lanToggle)` 以及默认仅绑定回环地址、开启 `allowLAN` 且非回环时展示局域网暴露警告与 `ValidateBindAddress` 安全策略，未放宽任何安全限制。

2. **[internal/diagnostics/trace.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX10/internal/diagnostics/trace.go)**（第 4 行）：
   - 移除未使用的 `"context"` 导入，不改动任何逐跳时间线与阶段统计算法。

3. **[internal/desktop/performance.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX10/internal/desktop/performance.go)**（第 125 行）：
   - 将 `if tab := a.activeTab(); tab != nil && tab.Entry != nil` 最小修复为 `if tab := a.ps.activeTab(); tab != nil && tab.Entry != nil`（前方第 121 行已校验 `a.ps != nil`）。

4. **[internal/diagnostics/diagnostics_test.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX10/internal/diagnostics/diagnostics_test.go)**（第 220–262 行）：
   - 将测试夹具中 `core.NetworkTrace` 的错误字段名对齐到 `internal/core/network.go:111` 真实定义（`TTFBDuration`、`TTFBObserved: true`、`DownloadDuration`、`ConnectObserved`、`ConnectDuration`），未删除任何测试或断言。

5. **全仓同类调用扫描结果**：
   - 扫描 `internal/` 下全部 `ui.Checkbox` 调用（共 22 处，含 `mock_ui.go`、`performance_ui.go`、`network_ui.go`、`body_editor.go`、`openapi_ui.go`、`workspace_settings.go`、`settings.go`、`request_form.go`、`cookie_ui.go`、`import_export_ui.go`），确认全部符合 `ui.Checkbox(c, checked *bool, label string)` 签名，无其他参数颠倒。

---

## 3. 构建命令与真实结果

针对本轮明确的编译修复需求，已执行以下命令确认全仓可编译且格式规范：

| 命令 | 退出码 | 真实结果 |
| --- | --- | --- |
| `gofmt -w internal/desktop/mock_ui.go internal/desktop/performance.go internal/diagnostics/trace.go internal/diagnostics/diagnostics_test.go` | `0` | 通过，无格式差异 |
| `go build ./...` | `0` | 全仓所有非测试包编译通过，零编译错误、零输出 |
| `git diff --check` | `0` | 无空白或补丁格式问题 |

---

## 4. 未跑测试与未做人工验收项（诚实声明）

- **按用户约束未执行的测试与实验**：
  - 严格遵守“先不跑测试”的约束，**未执行** `go test`（含单测与全仓测试）、**未启动**真实本地 Mock 监听服务、**未执行**性能采样运行、**未触碰**真实系统凭据（Keychain）或发起任何公网请求。
  - **不将 `go build ./...` 编译通过视为功能测试或真实窗口交互验收通过。**
- **待后续阶段完成的人工与测试验收项**：
  1. 解除“先不跑测试”限制后，运行 `go vet ./...` 与 `go test -race ./internal/mockserver/... ./internal/diagnostics/... ./internal/desktop/...` 验证单元与桌面集成回归。
  2. 在真实桌面窗口（macOS / Windows / Linux）中打开 `Local Mock Server` 与 `HTTP Performance Diagnostics` 面板，人工验收 `Allow LAN Bind` 复选框切换、运行态禁用（`Disabled(s.running)`）、手型光标（`ActionCursor`）及局域网绑定警告展示。

---

## 5. Git 提交信息

- **分支**：`agy/FIX10-mock-checkbox`
- **基线提交**：`957ea339967e41e7e774374068007c6f6e5e5e0b`
- **代码修复提交**：`b7de884c3594d006332e093485102ab90cafc967` (`fix(desktop,diagnostics): fix Mock Checkbox arg order and C02 compile errors`)
- **改动文件**：
  - `internal/desktop/mock_ui.go`
  - `internal/desktop/performance.go`
  - `internal/diagnostics/trace.go`
  - `internal/diagnostics/diagnostics_test.go`
  - `docs/task-results/FIX10.md`
- **边界遵守**：未修改 `go.mod` / `go.sum`、未修改 `docs/parallel-work-plan.md` 或 `docs/implementation-status.md`，未自勾任务总账本。
