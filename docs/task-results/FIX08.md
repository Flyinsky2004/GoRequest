# FIX08 请求超时的可发现入口与准确说明

- 任务 ID：FIX08
- 分支：`cc/FIX08-timeout-entry`
- worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX08`
- 共同基线：`a20960beffbc63880eb9e74214ec2675523af6bf`
- 状态：实现完成，本地门禁通过；真实窗口 / 三平台人工验收未做（见「边界与未做项」）。**未自行勾选总账本。**

## 目标回顾

回答普通用户「请求超时时间在哪里设置？」：把原先埋在请求编辑区 `Settings` 标签 → `renderRequestNetworkSettings` 首组里、只有一个无标签 `10s` 占位框的「请求总超时」做成清楚可发现、可理解的中文/英文入口；给出时长单位与例子、空值继承后的**当前有效值与来源**，区分「请求级保存」与「本机阶段设置」；修复无效输入被静默忽略的反馈；保留原正确值/草稿，不默认绕过限制。不重复造超时配置、不改网络执行引擎语义/模型/持久化范围、不把本机代理/证书写进请求、不引入全局设置里另一个互相覆盖的总超时。

## 先核对的代码事实（优先级，不硬说「始终 10s」）

执行时的总时限来自 `internal/core/plan.go:157-160` 的 `ExecuteWithOptions`：

1. `opts.Timeouts.TotalTimeout > 0` 时**优先**：来自本机设备 `NetworkStore`（`network.json`）的 `StageTimeouts.TotalTimeout`。桌面 `applyNetworkDrafts()` 保存的 `StageTimeouts` 只含 Dial/TLS/TTFB 三个字段，**不写 TotalTimeout**；但它会随设备配置持久化，一旦存在就是最高优先级。
2. 否则用 `plan.Timeout`：`BuildTrackedPlanWithOptions`（`internal/core/identity_redact.go:512-529`）在 `req.Timeout` 为合法正数时采用它，否则回退 `core.DefaultTimeout = 10s`（`internal/core/plan.go:22`）。请求值来自 `tab.Timeout` → `requestFromTab` → `base.Timeout`（`internal/desktop/tab.go:132`）。
3. `defaults.timeout`（项目 `project.yaml`，新建时写 `10s`）经全仓检索**从未被任何 plan builder 读取**（只有 `project.go:479` 写入、`model.go:184` 定义、`identity_auth.go`/`identity_management.go` 读取的是 `defaults.auth`）。因此本卡**不宣称「项目继承」已生效**，UI 文案只说「留空 = 内置默认 10s」，来源标签写「内置默认（请求未设置）」。新增测试 `TestProjectDefaultsTimeoutIsNotAppliedByPlanBuilder` 把这一事实钉住。
4. Dial / TLS 握手 / TTFB 是另一类**本机设备级阶段时限**（`core.StageTimeouts`，默认 10s/10s/30s），作用于这台机器上的全部请求，与「请求总超时」互不相同；它们不是 `tab.Timeout`，也从不写入项目请求文件。

`resolveRequestTimeout()`（`timeout_ui.go`）严格镜像上述 1+2 层，只计算展示用的有效值与来源，不改动任何执行语义。

## 实现

### `internal/desktop/network_ui.go`（本卡归属）

- `networkMessages` 新增 17 个双语字段（标题、范围说明、单位/例子提示、有效值格式、三种来源标签、无请求提示、无效/非正反馈、阶段超时归属说明、无效草稿反馈），`networkMessagesZh` / `networkMessagesEn` 全部填写且互不相同；通过既有 `TestNetworkMessagesCoverBothLanguages` 反射检查与新增 `TestTimeoutMessagesCoverBothLanguages`。
- `projectNetworkState` 新增 `timeoutErr`（阶段超时的字段级内联反馈，与模态级 `netErrMsg` 分开）。
- `renderRequestNetworkSettings` 首组拆成两张卡并置于最上方（不再埋在技术设置里）：
  - `a.renderRequestTimeoutCard(...)`：请求级「请求超时」入口，含单位/例子、**当前生效：X（来源：Y）**、无效/非正值的 danger 内联反馈、请求级保存与本机阶段设置的区分说明。
  - `a.renderStageTimeoutCard(...)`：本机 Dial/TLS/TTFB 阶段设置，明确「只保存在本机 network.json，不写入项目请求文件，作用于这台机器全部请求」。
  - 后续分组注释重编号为 3–6，渲染与既有行为不变。
- `openNetworkSettingsModal` 打开时按保留的草稿重算内联反馈（不显示过期错误、不掩盖仍无效的输入）。
- 模态 Save 按钮改用 `applyNetworkDraftsFromUI()`：无效阶段草稿不再被 `_ = st.applyNetworkDrafts()` 静默丢弃，而是显示内联错误、保持弹窗打开、保留草稿、不覆盖上一份有效设备设置；其它保存错误仍走 `netErrMsg`。
- Close / 模态 Dismiss 视为取消：保留用户草稿与上一份有效设备设置，只清除瞬态模态级提示，不隐式改写任何配置。
- 请求级 `tab.Timeout` 输入框：无效值即时显示内联 danger 反馈并保留草稿（此前无任何反馈），不改写 core 的 `time.ParseDuration` 限制，也不在发送前静默按旧值绕过。
- 全文件顺带 `gofmt` 修正了原有对齐漂移（纯空白、无语义变化；`-w` diff 为 94 增 / 31 删）。`network_ui.go` 为本卡独占文件，无跨卡冲突。

### `internal/desktop/timeout_ui.go`（新增，本卡归属）

- `requestSettingsSection = 6`：请求编辑区 Settings 标签索引（与 `workspace.go` 的 7 个标签一致）。
- `timeoutOrigin` / `timeoutResolution` / `resolveRequestTimeout(raw, deviceTotal)`：纯函数，镜像执行时的两层优先级；返回有效值、来源、解析错误、非正标记。
- `timeoutSourceLabel`：三种来源的中英文标签。
- `validateStageTimeoutDrafts`：复用既有 `parseOptionalDuration`，先校验三个草稿，让错误可见。
- `applyNetworkDraftsFromUI`：先校验（阶段错误 → `timeoutErr` 并返回），成功后才 `applyNetworkDrafts()`；草稿从不重写，故上一份有效设置始终生效。
- `renderRequestTimeoutCard` / `renderStageTimeoutCard`：两张卡的渲染；`renderTimeoutQuickEntry`：可选顶层快捷入口，只把 `tab.Section` 切到 Settings 并显示有效值/来源，不新增设置面、不新增控制器状态。

### 顶层快捷入口（可选，交集成人接线）

`docs/task-results/FIX08-integration.patch` 是对公共 `workspace.go` 的**最小 4 行接线**（`urlGroup` 内在 `b03URLPreview` 前调用 `a.renderTimeoutQuickEntry(c, t, tab)`）。`git apply --check` 通过；临时应用后 `go build ./...` 通过；随后已还原 `workspace.go`，**本分支未改任何公共文件**。不接此 patch 时，请求编辑区 Settings 标签与响应栏「网络与证书…」弹窗内的两张卡已完整可达（见「入口如何到达」）。

## 入口如何到达（交接）

- **现有入口 1（本卡已增强）**：打开任一请求 → 请求编辑区下划线标签 `设置 / Settings`（索引 6）→ 首卡「请求超时（本机保存，随请求文件共享）」。`tab.Timeout` 输入框即请求级总超时；修改后按 `⌘S` /「保存」写入该请求的 `*.request.yaml`（`timeout:` 字段，随项目共享）。同页第二张卡是「阶段超时（均可随时取消）」的本机 Dial/TLS/TTFB。
- **现有入口 2（本卡已增强）**：响应区工具行的 `网络与证书… / Network & TLS…` → 弹窗内同样两张卡（Settings 标签与弹窗共用 `renderRequestNetworkSettings`）。请求级输入框在弹窗里也出现；本机阶段设置通过「应用并保存本机网络设置」落 `network.json`。
- **可选入口 3（需集成 patch）**：请求行下方一行 `请求超时… / Request Timeout…` 按钮 + 「当前生效：X（来源：Y）」；点击只跳到 Settings 标签，不另开设置面。
- 无效值（如 `10`、`10 seconds`、`0s`、`-3s`）在输入框下方即时显示中文/英文 danger 反馈并保留原草稿；不会静默按旧值发送，也不绕过 core 的 `time.ParseDuration` 限制。

## 验证结果

门禁（工作目录为本 worktree，日志 `/private/tmp/gorequest-fix08-race.log`）：

- `go build ./...`：**通过**
- `go vet ./...`：**通过**
- `go test -race ./...`：**通过**，exit 0，全部包 `ok`。最终一次（所有改动完成后）复跑：`internal/desktop` 81.306s，其余包均 `ok` / `(cached)`；首次完整跑：`internal/core` 7.157s、`internal/desktop` 82.375s、`internal/desktop/uikit` 2.573s、`internal/gitservice` 6.945s、`internal/localstate` 3.830s、`internal/markdown` 1.353s、`internal/platform` 1.973s、`internal/runner` 2.216s、`examples/demo-server` 2.136s；`cmd/gorequest-desktop` 无测试文件。日志 `/private/tmp/gorequest-fix08-race.log` 与 `/private/tmp/gorequest-fix08-race-final.log`。
- 集成 patch 临时应用后 `go build ./...` 亦通过，`git apply --check docs/task-results/FIX08-integration.patch` 通过（随后已还原 `workspace.go`）。

新增独立测试 `internal/desktop/timeout_ui_test.go`（不覆盖他卡测试文件）：

- 优先级表驱动 `TestResolveRequestTimeoutPriority`（12 例）：空/空白→10s 内置默认；合法请求值/组合单位/带空格；坏单位、纯数字、散文、0、负值无效；设备 TotalTimeout 覆盖请求/默认/无效请求串。
- `TestProjectDefaultsTimeoutIsNotAppliedByPlanBuilder`：改 `project.yaml` 的 `defaults.timeout` 为 42s 并重新 `OpenProject`，空白请求仍由 `BuildTrackedPlanWithOptions` 得到 10s，钉住「项目继承当前不生效」。
- 双语词条完整性与 `%s` 格式串检查；`TestNetworkMessagesCoverBothLanguages`（既有反射）自动覆盖新字段。
- 卡片可发现性：`TestTimeoutCardIsDiscoverableWithUnitsAndEffectiveSource`（默认 10s、请求 3s、设备 7s 三种来源）、`TestTimeoutCardShowsChineseCatalog`、`TestTimeoutCardInlineFeedbackForBadValues`（保留草稿）、`TestTimeoutCardWithoutTabExplainsEntry`。
- 阶段设置：`TestStageTimeoutCardNotesDeviceLocalScope`、`TestStageTimeoutInvalidDraftReportsAndKeepsLastValid`（无效值内联报错 + 保留草稿 + 不覆盖上一份有效值 + 修正后清除）、`TestNetworkModalSaveRejectsInvalidStageDraftAndKeepsOpen`、`TestNetworkModalCancelPreservesDraftAndSettings`（取消/重开）。
- 快捷入口：`TestTimeoutQuickEntryNavigatesToSettingsTab`、`TestTimeoutQuickEntryInvalidFlag`。
- 保存→重开闭环：`TestRequestTimeoutSaveAndReopen`（真实 `saveTabSynced` 写盘、读取 `timeout: 1m30s`、`core.OpenProject` 重开、UI 解析一致）。
- **真实本地慢服务**：`slowTestServer` 仅在 `r.Context().Done()` 后返回，无长 sleep、无公网。`TestRequestTimeoutDrivesRealSlowServiceAndDefaultInheritance`（60ms 请求超时确实切断慢服务且错误含 `timed out after 60ms`；空值继承 10s 且快速本地端点成功）、`TestDeviceTotalTimeoutOverridesRequestOnRealSlowService`（请求 30s、设备 80ms 胜出）、`TestRequestTimeoutHonorsCancellationBeforeDeadline`（显式 cancel 先于 deadline）、`TestInvalidRequestTimeoutFailsPlanBuildNotSilentlyIgnored`。
- `TestFIX08NetworkStoreIsInMemory`：确认测试用空路径 NetworkStore 不写真实设备配置。

所有测试夹具使用 `t.TempDir()`、内存 NetworkStore、`httptest.NewServer(127.0.0.1)`；未写真实系统凭据 / 历史 / 全局配置，未使用示例环境或 debug 二进制，未触发隐式公网请求。遵循 ADR-010：本卡不涉及 Secret 真实值，项目侧只存名称/引用的边界不变。

## 边界与未做项

- **未做真实窗口 / macOS·Windows·Linux 三平台人工验收**：均为 `ui.NewTester` 无窗口自动化 + `-race`；不以 Herdr 截图冒充 GoRequest 验收。卡片文字换行、暗色主题对比、窄窗口观感请真窗口确认。
- **未改** `internal/core/*`、`internal/platform/*` 执行/持久化语义；未新增设备 TotalTimeout 的 UI 写入口（明确不引入与请求总超时互相覆盖的第二个全局总超时），只在设备值已存在时如实显示其为优先来源。
- 本卡只修复**超时**输入的静默忽略反馈（请求级 `tab.Timeout` 与三个本机阶段超时）。B04 原有的重定向/代理/TLS/目标策略等 17 处 `_ = st.applyNetworkDrafts()` 仍保留原样，不在 FIX08 范围内；因为它们共用的 `applyNetworkDrafts()` 会先校验三个时长，阶段草稿无效时其它字段的改动同样不会保存，`timeoutErr` 也仍如实显示，不会产生新的静默「成功」假象。
- 项目 `defaults.timeout` 当前 inert；若将来引擎实现继承，需同步更新 `resolveRequestTimeout` 与来源文案（本测试会提示）。
- **未改公共文件**：`app.go` / `manager.go` / `nav.go` / `workspace.go` / `tab.go` / `config.go` / `i18n.go` / `go.mod` / `go.sum` 与总账本、`implementation-status.md` 全部未动；未操作 main / 他人目录 / agent，未 merge / cherry-pick / push / reset / clean，未改共享 Git 配置。
- 顶层快捷入口需集成人应用最小 patch；不自勾总账本，集成验收后再勾选。

## 集成步骤

1. 合并 `cc/FIX08-timeout-entry`（只含 `network_ui.go`、`timeout_ui.go`、`timeout_ui_test.go`、`FIX08-integration.patch`、`FIX08.md`）。
2. 可选：在公共 `workspace.go` 应用 `docs/task-results/FIX08-integration.patch`（`git apply --check` 已验证；4 行，只加一个调用）。与 FIX06/07/09 无文件冲突（本卡未碰 `settings.go`、`uikit/response.go`、`environment_editor.go` / `identity_management*.go`）。
3. 跑 `go build ./...`、`go vet ./...`、`go test -race ./...`。
4. 真窗口按「入口如何到达」逐项验收；满足后由集成人勾选总账本 FIX08。

## 提交范围（本分支本地 commit，不 push）

- `internal/desktop/network_ui.go`
- `internal/desktop/timeout_ui.go`（新增）
- `internal/desktop/timeout_ui_test.go`（新增）
- `docs/task-results/FIX08-integration.patch`（新增）
- `docs/task-results/FIX08.md`（本文件）
