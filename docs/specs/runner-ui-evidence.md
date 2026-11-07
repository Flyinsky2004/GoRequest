# A02 phase 1：Runner / Schema / 原生桌面联合证据

此为第一阶段工作契约与验证证据，不是 A02 整卡、完整自动化产品或三平台人工验收。共同基线 `3d51b99c29d9f583fcec8339e3ab77dc7f121643`；负责人 Codex；分支 `codex/A02-runner-first-loop`；worktree `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A02`。

## 可达路径与集成

公共文件只交 [精确两行接线 patch](../task-results/A02-phase1-integration.patch)。在本分支上临时应用该 patch 后验证了生产入口：项目名菜单 → 集合 / Suite 运行器 → 保存集合或已有 `.suite.yaml/.suite.yml` → 明确环境 → 运行保存快照 → 实时事件/逐次尝试/最终分类与汇总 → 取消或显式导出 JSON/JUnit。不是孤立面板；`TestA02PatchedAppRunnerEntry` 使用生产 `App.View` 点击项目菜单、原生 MenuItem，再检查实际运行控件。

集成人串行执行：

```sh
git apply --check docs/task-results/A02-phase1-integration.patch
git apply docs/task-results/A02-phase1-integration.patch
go build ./...
go vet ./...
go test -race -tags a02_integration ./...
```

patch 只在 `app.go` 的现有 Modal 列表添加 `RunnerModals`，以及 `workspace.go` 的现有项目菜单添加 `BuildRunnerProjectMenuItems`；没有公共状态字段、生命周期 manager hunk 或依赖变更。分支交付时已经仅用此 patch 的逆向应用撤回自己的临时公共接线；集成人需要应用它才有产品入口。`a02_integration` 测试刻意要求该 patch；无 patch 的默认构建/测试不依赖此入口。

## 固定输入与窗口归属

- `OpenProject` 独占读取保存对象；Suite 调用现有 `LoadSuite`，集合调用 `CollectionSuite`。根选项 `collections` 用本次运行私有 CollectionEntry 表示全部保存请求，排序及线性依赖仍由 Core 构建。运行不读取脏 Tab，UI 有排除未保存编辑的明确提示。
- 开始时在 UI 线程复制 B04 网络配置、B05 绑定值及本地 TLS 材料；Prepare 加载数据、离线 Schema 和固定项目附件，然后密封 provider，再在后台执行。运行期间 Core 使用独占项目模型、固定环境和密封端口，不读取 App/Tab/projectState；没有后台调用 UI 网络 helper。快照摘要包含 A01 请求/环境/数据等摘要和本次冻结 Schema 内容摘要。
- 每次运行 step/row concurrency 均为 1；每行独立 CookieJar/data/step outputs。使用现有网络阶段超时、代理、TLS、目标策略和 Secret 端口。非交互且未确认，生产限制不自动放行；OAuth/OIDC 尚未联合，Prepare 对有效方案明确拒绝，零请求。
- 窗口状态通过 weak App key 关联，状态无 App/Window/Tab/projectState 强引用；关闭回调取消并删除状态。后台发送到发起窗口 `Window.Update`，generation 拒绝过期结果，两个窗口各有独立 mailbox/结果。没有 idle ticker、动画循环或空闲请求重绘。
- pending events 和 UI events 各最多 256，最多一个待处理 Update；最终内存结果单独保留，不丢最终汇总。实时视图说明最近 256 事件，最终视图限制 256 个步骤/尝试条目；完整逐次结果通过报告导出。并非完整结果分页 UI。
- 保存快照准备有界但当前在 UI 线程同步完成。大附件或慢文件系统可能短暂阻塞；后续应把纯文件快照准备改为可取消后台阶段，不把本轮声明成完成全部响应性验收。模板化的真实目标仍由执行 Core 再检查，预检对 dry-build 静态/占位目标保守拒绝。

## Schema 选择与配置预检

[交付记录](../task-results/A02-phase1.md) 列出完整支持子集。A02 adapter 实现 Runner `ValidateSchema` / `PreflightSchema`，通过 A03 `SchemaValidatorPort.ValidateInstance` 校验实际 JSON 实例。配置检查独立于实例：扫描所有支持分支、严格 YAML/重复键/多文档检查、限定字段类型、解析并冻结离线 `$ref`；不使用虚拟实例探测配置，不复制实例校验器，不形成 core→runner 依赖。

文件 ref 相对 `.gorequest/`，嵌套 ref 相对所在文件；支持 RFC6901 fragment，无远程、anchor、动态引用、循环。项目 Store 拒绝越界和符号链接逃逸；资源界限为 128 文件、合计 8 MiB、48 层、10000 Schema 节点，另有严格 YAML AST 节点/展开限额；文件/节点预算在本次 adapter 内累计，展开 JSON 还有保守估算 8 MiB 总预算（字符串按最坏转义估算），防止大常量重复引用放大。Operation 通过唯一已保存 operationId 明确选择，响应按精确状态→nXX→default；目前仅支持单一 application/json body、没有响应 Header contract。所有响应须有 Schema，缺失、不支持或坏 ref 在发网前失败；运行时未匹配状态/媒体或非 JSON 是断言失败。

兼容增量仅 `SchemaRef.Headers [][2]string`（实际响应头）和可选 `SchemaSnapshotDigest()`；原有 validator 不必实现新方法。Schema 断言结果使用通用 target，不输出原始引用、Schema 常量/示例或实例值；失配输出实例路径与 keyword，再经过 Runner taint 脱敏。Schema 与 UI 契约仍是初始版本，未宣称冻结全部 Q-06/07/17。

## 自动化证据

以下均为临时目录、显式内存凭据和本地 httptest；未访问真实公网、用户示例或系统钥匙串。

| 用例 | 证据 |
| --- | --- |
| 两行 CSV 与两行 JSON 登录→敏感 Token 提取→查询→更新→Schema | `TestRunnerDesktopHTTPFlowTwoRowsCSVJSON`；每次桌面运行 6 请求、2 次登录、每用户 2 次下游调用；Cookie/Authorization/Body 均断言按行隔离 |
| 保存快照一致 | 同一用例比较直接 Core 与桌面的 Snapshot、Status、Summary、逐步骤状态及尝试数；运行后改脏 URL、项目模型、环境、设备 denylist 不影响本次运行 |
| 密封附件/凭据 | `TestRunnerSnapshotProvidersSealed`；开始后改源 Secret map 和附件文件，临时 HTTP 服务仍收到原值及原字节；未知附件不可运行时重开 |
| 坏 Schema 零请求、精确失败路径、依赖 skipped | `TestRunnerDesktopBadSchemaAndPreciseSkipped`；坏未触及引用 0 请求；不符合时 `$.data.id`，每行 update skipped；另有 adapter 单测覆盖多文件、循环、远程、越界、严格配置和 Operation 缺 Schema/未匹配响应 |
| 取消不重试、关闭回收、两个窗口隔离 | `TestRunnerMultiWindowCancellationNoRetryAndClose`；显式 3 次重试仍只各发 1 次；取消 A 不触碰 B，关闭 B 导致本地请求 context 结束，并删除窗口状态 |
| 生产与 OAuth 发网前拒绝 | `TestRunnerOAuthAndProductionArePreflightErrors`；第二步骤的限制不会允许第一步骤提前发送，Executor 0 次；OAuth 信息明确 |
| 导出失败可见、路径边界、秘密 | `TestRunnerReportExplicitPathAndVisibleFailure`；不存在父目录显示 internal_error/report；拒绝项目与指向项目 symlink；HTTP 联合用例实际读取 JSON/JUnit 并检查 Token/绑定 Secret 不存在 |
| 有界事件 | `TestRunnerMailboxBoundedAndFinal`；10000 事件只排一个 Update/保留 256 事件且完整最终结果未丢失 |
| 紧凑窗口布局 | `TestRunnerPanelExportReachableAtCompactHeight` 检查 1000×700 中运行/导出按钮非零且位于视口内；固定取消/导出，中间滚动，未用 headless 替代人工验收 |
| 原生控件/入口 | `TestRunnerNativePanelAndDirtySnapshot` 及带 tag 的 `TestA02PatchedAppRunnerEntry`；均为 MyGo headless，不是人工窗口验证 |
| 数据行及保存元数据无秘密 | `TestRunnerReportsMaskDatasetAndSavedMetadata`；已绑定 Secret、password 数据字段及取消结果元数据在返回 UI 结果/JSON/JUnit 均脱敏；修复 A01 仅步骤脱敏的缺口 |
| Prepare→Prepare→Run | `TestPreparedRunnerRunDoesNotReturnTypedNil`；修复成功预检缓存的 typed nil error 引发 Run panic 的实际 A01 bug |

## 未验收与后续

未做真实桌面窗口、原生 Save 对话框点击、系统凭据后端、真实 OAuth、Windows/Linux/macOS 多平台人工矩阵、可访问性/高 DPI/输入法及大项目响应性验收。可视化断言/提取编辑器、完整 Suite 编排/数据编辑、完整结果分页/搜索仍为后续。第一阶段拒绝本地槽位上传/动态附件路径，固定项目附件合计限 64 MiB，TLS 每项限 8 MiB。

报告在 Run 最终返回前统一脱敏，覆盖数据行和名称元数据；敏感数据字段值不回写执行行。报告只在显式用户路径写入，默认内存；0600 临时文件原子替换，拒绝当前项目目录内的目标（比仅拒绝项目运行状态更保守）。没有写共享运行状态/历史/Token，也没有新增 WebView/独立产品 CLI、WS/GraphQL/gRPC/Mock 或依赖。未修改总账本/implementation-status，未自行勾 A02。

## 验证结果

最终工作树（已撤回自己的公共接线）：`go build ./...`、`go vet ./...`、`go test -race ./...` 均 exit 0；desktop 最后一次 87.120s。最终版本临时应用 patch 后的 `go test -race -tags a02_integration ./internal/desktop ./internal/runner -run 'TestA02|TestRunner|TestSchemaAdapter|TestPreparedRunner' -count=1 -timeout=60s` exit 0（2.612s / 2.483s）。实施中完整带接线的 `go test -race -tags a02_integration ./...` 也通过。`git diff --check`、撤回接线后 `git apply --check` 均通过。测试环境 darwin/arm64、Go 1.27.3。
