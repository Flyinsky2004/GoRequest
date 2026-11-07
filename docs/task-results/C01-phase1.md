# C01-phase1 交付报告：本地确定性无状态 HTTP Mock 服务与原生面板（第一阶段）

- **任务 ID**：`C01-phase1`
- **所属主卡**：`C01`（本地确定性无状态 HTTP Mock 服务，第一阶段收口）
- **开发分支**：`agy/C01-local-mock`
- **工作树路径**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01`
- **共同提交基线**：`00c1bf42b9d15a23ac5b970f7431b895c4239786`
- **交付状态**：第一阶段代码与回归测试夹具已实现并本地分阶段提交；**按用户本轮最新约束未执行 `go test` / `go build` / `go vet` / 真实监听验证（待后续统一执行验收）**。

---

## 1. 本次实现范围与交付物

### 1.1 纯 Go Mock 引擎（`internal/mockserver/**`）
- **[contracts.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/contracts.go)**：
  - 定义 `.gorequest/mocks/*.mock.yaml`（`schemaVersion: 1`）有界严格格式（`MockFile`、`MockRule`、`RuleMatch`、`ValueMatcher`、`KeyValueMatcher`、`BodyMatcher`、`JSONFieldMatcher`、`RuleResponse`、`CORSConfig`、`ServerLimits`）。
  - 通过 `core.ParseStrictYAMLTree` + `KnownFields(true)` 拒绝未知字段、YAML 锚点/别名、合并键与超限文件（单文件上限 `1 MiB`，单项目规则上限 `512`）。
  - `ListProjectMockFiles` / `LoadProjectMocks` 对路径归一化并按字典序稳定排序，绝不依赖操作系统目录遍历顺序。
- **[compile.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/compile.go)**：
  - 规则按显式 `Priority DESC` + 稳定 `Rule.ID ASC` 排序编译为不可变快照 `CompiledSnapshot`（含 `SnapshotDigest`）。
  - 同优先级重叠/歧义检测（`detectRuleAmbiguities` -> `*AmbiguityError`）：同场景、同优先级、同 HTTP Method 下路径模板可能命中同一 URL 且 PathParam/Query/Header/Body 条件不互斥时明确报错并阻止启动。
  - 校验响应模板表达式，仅允许受限声明式占位符（`{{path.*}}`、`{{query.*}}`、`{{var.*}}`、`{{req.id}}`、`{{req.seq}}`、`{{rand.int}}`、`{{rand.hex8}}`、`{{rand.uuid}}`），严禁 `{{secret.*}}` 与任意脚本执行，禁止 `allowOrigins: ["*"]` 与 `allowCredentials: true` 共存。
- **[matcher.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/matcher.go)**：
  - 支持 Method、路径模板（`/v1/users/{id}` 或 `/v1/users/:id`）、路径参数条件、Query、Header、Body（文本 `equals`/`contains`/`prefix`/`regex` 与 JSON `jsonEquals`/`jsonFields`）匹配。
  - 基于 `(Snapshot.Seed, req.Seq)` 的 `math/rand/v2.NewPCG` 提供确定性延迟（`delayMs` / `delayRangeMs`）和随机占位符渲染，保证同规则 + 同种子输出一致。
  - 按响应 Content-Type 对回显的 `{{path.*}}` / `{{query.*}}` 执行 JSON/HTML/Header 安全转义。
  - 未命中请求绝不转发公网，返回清晰的结构化 `404 Not Found` JSON（含脱敏的 `nearMisses` 诊断原因）。
- **[access_log.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/access_log.go)**：
  - 内存有界环形缓冲区 `AccessLog`（默认容量 `200` 条，单条预览上限 `4 KiB`），支持随时 `Clear()`。
  - 自动联动 `core.Redactor` 脱敏 `Authorization`、`Cookie`、`Set-Cookie`、`Proxy-Authorization`、敏感 Query 参数、JSON Body 敏感字段及已注册凭据秘密，绝不落盘或写入共享项目文件。
- **[server.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/server.go)**：
  - 默认仅绑定本地回环 `127.0.0.1`；非回环/局域网地址在未显式开启 `AllowLAN: true` 时返回 `*BindPolicyError` 拒绝启动。
  - 固定端口被占用时返回 `*PortConflictError`，绝不终止外部进程或暗自切换固定端口；端口设为 `0` 时暴露实际分配的 `BoundAddr()` / `BaseURL()` / `Port()`。
  - 强制请求体上限（`http.MaxBytesReader`，超限返回 `413`）、最大并发信号量、可随请求取消或服务停止立即唤醒的延迟计时器，以及有界 `Stop(ctx)` 连接与 goroutine 回收。
- **[builder.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/builder.go)**：
  - `BuildRuleFromRequest`：从现有 `core.Request` 与响应示例生成规则，自动剥离 `Authorization`/`Cookie`/`Set-Cookie`/敏感 Query 与响应体秘密字段，内置 `default`/`success`/`error_400`/`error_401`/`error_500` 场景变体。
  - `GenerateSchemaExample` / `BuildRuleFromOperationContract`：支持明确 JSON Schema 子集的确定性示例生成，并强制通过 A03 `core.NewSchemaValidator().ValidateInstance(..., ValidateFormats: true)` 校验；遇到不支持的构造（如无显式示例的正则 `pattern`、外部 `$ref`、递归 `$ref` 等）返回 `*SchemaExampleError` 要求显式示例，绝不冒充任意 Schema 生成成功。
- **[suite_lifecycle.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/suite_lifecycle.go)**：
  - 提供 `SuiteMockLifecyclePort`、`ControlledSuiteMock` 与 `RunSuiteWithControlledMock`：先校验编译快照 -> 启动独立 `127.0.0.1:0` Mock 服务 -> 就绪检查 -> 将实际地址注入 Suite 私有运行变量副本（`mockUrl`、`mockBaseUrl`、`mockHost`、`mockPort` 及指定 `EndpointVar`），不污染共享环境文件 -> 复用 `runner.Runner` 执行并在成功/失败/预检错误/取消时有界关闭服务。
  - 交互面板切换场景只原子替换面板服务的快照指针（`SwapSnapshot`），不影响已启动的受控 Suite 快照。
- **[mockserver_test.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/mockserver/mockserver_test.go)**：
  - 覆盖严格解析、优先级与同优先级歧义报错、Method/Path/Query/Header/JSON Body 匹配与 404 诊断、确定性种子、端口冲突/取消/重启/LAN 策略、访问记录与规则构建秘密脱敏、A03 Schema 子集校验与越界失败、Suite 受控 Mock 生命周期与场景快照隔离。

### 1.2 原生桌面端面板与生命周期（`internal/desktop/mock*.go`）
- **[mock_i18n.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/mock_i18n.go)**：中英双语词条（`mockEN` / `mockZH`）。
- **[mock.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/mock.go)**：
  - 遵循与 `runner.go` 一致的 `weak.Pointer[App]` + `runtime.AddCleanup` + `a.win.OnClosed(...)` 每窗口状态绑定，无全局强引用 UI map 泄漏；关闭窗口或切换项目时自动有界停止 Mock 服务并取消受控 Suite 运行。
  - 打开项目绝不自动启动监听；仅在用户点击启动或运行受控 Suite 时启动。
- **[mock_ui.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/mock_ui.go)**：
  - 提供 `BuildMockProjectMenuItems` 与 `MockModals`，展示监听地址/端口、`Allow LAN Bind` 开关与局域网暴露警告、场景选择器、确定性种子、快照摘要、同优先级冲突详情、从当前请求/OpenAPI Schema 创建规则按钮、有界脱敏访问记录与清空按钮、Suite 受控 Mock 运行页签。
  - 所有可点击控件统一挂接 `uikit.ActionCursor` 并随运行/冲突状态设置 `.Disabled(...)`。
- **[mock_test.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/mock_test.go)** 与 **[mock_entry_integration_test.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/mock_entry_integration_test.go)**（`//go:build c01_integration`）。

### 1.3 契约证据与公共接线补丁
- **[docs/specs/mock-evidence.md](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/docs/specs/mock-evidence.md)**：记录 `.gorequest/mocks/*.mock.yaml` v1 未冻结格式、优先级与歧义检测规则、安全模板子集、A03 Schema 示例生成子集与受控 Suite 生命周期说明。
- **[docs/task-results/C01-phase1-integration.patch](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/docs/task-results/C01-phase1-integration.patch)**：`internal/desktop/app.go` 与 `internal/desktop/workspace.go` 的 2 行最小公共接线补丁。

---

## 2. 本地分阶段 Git 提交记录

1. `6b318cb` — `feat(mockserver): implement C01 phase 1 deterministic local HTTP mock engine`
2. `120b8d5` — `feat(desktop): add C01 phase 1 native Local Mock Server panel and lifecycle`
3. `cb7e029`（修订更新本行哈希）— `docs(c01): add C01-phase1 contract evidence, integration patch, and delivery report`

---

## 3. 已执行静态检查与未运行验证说明（重要）

- **已执行静态检查**：
  - `gofmt -w internal/mockserver internal/desktop/mock*.go`（全部通过，零语法错误）
  - `git diff --check`（无空白/格式错误）
  - `git apply --check docs/task-results/C01-phase1-integration.patch`（退出码 `0`，补丁可干净合入）
- **按用户约束未执行的验证（待后续验收执行）**：
  - 本轮遵循用户“先不用执行测试”约束，**未执行 `go test`、`go build`、`go vet`，也未启动真实进程监听验证**。

---

## 4. 后续验收与回归执行计划（供集成人/后续阶段运行）

合入或解除“不执行测试”限制后，按顺序执行以下命令完成验证：

```bash
# 1. 纯 Go Mock 引擎静态检查与单元/集成回归（含优先级/歧义、端口冲突、取消、重启、秘密脱敏、稳定种子、A03 Schema 子集、Suite 受控启停）
go vet ./internal/mockserver/...
go test -v -race ./internal/mockserver/...

# 2. 桌面端 Mock 面板无补丁独立回归（验证不自动启动、从请求/OpenAPI 建规则、冲突阻断、脱敏日志、受控 Suite 运行）
go vet ./internal/desktop/...
go test -v -run 'TestMock' ./internal/desktop

# 3. 应用最小公共接线补丁并验证生产入口可达性
git apply --check docs/task-results/C01-phase1-integration.patch
git apply docs/task-results/C01-phase1-integration.patch
go test -v -tags c01_integration -run TestC01PatchedAppMockEntry ./internal/desktop
```

---

## 5. 精确公共文件接线说明

集成时只需应用 [C01-phase1-integration.patch](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/docs/task-results/C01-phase1-integration.patch)：
1. 在 [app.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/app.go) 的 `App.View` 弹窗挂载序列中增加 1 行：`a.MockModals(c, t)`。
2. 在 [workspace.go](file:///Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01/internal/desktop/workspace.go) 的 `App.buildProjectMenu` 项目菜单中增加 1 行：`a.BuildMockProjectMenuItems(mm)`。

---

## 6. 剩余工作与后续阶段边界（未将第一阶段当整卡完成）

`C01-phase1` 完成了确定性无状态 HTTP Mock 核心引擎、A03 Schema 子集生成、Suite 受控 Mock 生命周期端口与桌面端原生可达面板闭环。以下内容留待后续阶段与主线合入后推进：
1. 执行上述第 4 节全部 `go vet` / `go test -race` / `-tags c01_integration` 门禁并记录实测通过证据。
2. 在桌面端提供可视化的单条 Mock Rule 细粒度图形化字段编辑器（当前第一阶段支持从当前请求/脱敏响应、OpenAPI Schema 生成规则以及直接编辑/重载 `.gorequest/mocks/*.mock.yaml`）。
3. 扩充 JSON Schema 示例生成器对更复杂组合关键字（如受限正则 `pattern` 子集、多文档组件引用）的覆盖面。
4. 与 A02 Runner 主面板的受控 Mock 开关做合流接线（待公共桌面文件窗口期由集成人统一合入）。
