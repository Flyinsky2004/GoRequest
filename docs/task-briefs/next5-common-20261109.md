# N01–N05 并行开发共同约束（2026-11-09）

用户要求：通过 Herdr 启动五个交互式 Claude Code；主代理提供明确实施方案。五个分支从同一份登记提交创建，不在 main 开发。历史参考代码为 `3bed514`；实际共同基线由主代理启动时记录在账本 §45。

## 开始前

1. 读取本仓库 AGENTS.md、账本 §45、本文件、自己的任务说明，核对 `pwd`、`git branch --show-current`、`git log -1 --oneline`、`git status --short`。只在指定 worktree 内工作。
2. 读取对应规格和下面列明的实际代码。不要从旧报告的“尚未接线/未测试”推断当前代码状态；最新集成记录为 `docs/task-results/integration-U32-U36-2026-11-09.md`。
3. UI 任务读取 `/Users/flyinsky/.agents/skills/ui-ux-pro-max/SKILL.md`，采用已有 MyGo Native UI、UIKit、SVG、Palette、中英词条、原生表单。按组件任务做一次针对性 UX 查询。这里没有 React/Tailwind/WebView；不要照搬浏览器 aria 属性。先查本地 MyGo 0.7.4 实际 API，不能猜组件名/回调签名。保持品牌色与现有界面密度。
4. 先在报告草稿写出“实际现状/实现步骤/文件列表/验收用例”，随后直接实现；不需要等待主代理再次批准已明确的任务范围。遇到超出文件归属的必要改动，提交最小 patch 和原因，不扩权重构。

## 公共文件与交付规则

- 不派子代理；不操作其他 agent/session/worktree；不 merge/rebase/push、部署、发版、启动真实安装器或退出用户应用；不改全局模型/权限/Herdr 配置。
- 用户主工作区 `examples/sample-project/.gorequest/environments/dev.yaml`、未跟踪 `docs/document.md` 和调试产物不复制、不修改、不提交。自己的测试全部用临时项目、fake Secret、loopback 服务。
- 不读取真实密钥、历史或设备网络配置；不调用生产服务。打开项目/面板不能自动发网或监听。禁止任意脚本、shell cURL、仓库 hooks 和外部生成器。
- Core/业务层不依赖 GUI。UI 不另造 HTTP、变量、鉴权、断言执行引擎。保留 TLS/重定向凭据过滤/主机策略与默认脱敏；用户完整本地历史选项不得扩大到公共报告或代码生成。
- 除 N04 独占 `go.mod/go.sum` 的指定 GraphQL 解析库外，其他任务不修改依赖。不得修改 MyGo/module 缓存源码。
- `app.go/workspace.go/manager.go/project_state.go/i18n.go` 由集成人统一接线；任务只交 `docs/task-results/<ID>-integration.patch`。patch 只能包含必要入口/生命周期接线，不含主体实现。分支主体必须编译；如生产入口尚待 patch，报告明确说明。可以在自己的隔离 worktree 临时应用 patch 验证，再精确撤回 patch，不能丢弃其他文件。
- N01 独占旧 Runner 断言/提取编辑器和 Mock 规则编辑器相关修复；N02 只做新的 Suite 编排编辑模块；N03 独占 `desktop/runner.go`、`runner_snapshot.go` 和 Mock/Runner 生命周期；N04 独占 GraphQL 相关 Core 协议/构建与 `desktop/tab.go`；N05 只做新的 SDK 模块。`desktop/runner_ui.go` 归集成人，N02/N03 都只交小 patch，不能整文件重写。
- 不改 `docs/parallel-work-plan.md`、`implementation-status.md`、其他任务说明和其他卡报告。只写自己的 `docs/task-results/<ID>.md` 与 patch，不能勾总卡完成。
- 使用小的可审阅函数与明确错误类型；不要全仓 gofmt、批量替换、删除现有能力。保留稳定 ID、注释、未编辑字段和外部修改冲突保护；新建要求文件不存在，编辑按真实读取摘要保存。
- 完成后检查 diff、`git diff --check` 与暂存内容，提交相关源文件/测试/自己的报告和 patch。允许本地 commit，报告精确哈希。不要在报告写“全部完成”来替代真实验证。

## 验证基线和边界

主代理在 `3bed514` 实际执行：`go build ./...`、`go vet ./...` 通过；未跳过 `go test ./... -count=1` 只有四项失败：

1. `TestMockEditorSuggestionsAreProjectUnique`
2. `TestRunnerEditorDescribeAssertions`
3. `TestRunnerEditorPatchSuiteExtract`
4. `TestEngine_PolicyBlockedAndMissingSecretPreflight`

N01 负责查根因；不是预先认定四项都是生产 bug。其他任务不能改这四个断言来让自己全绿。N01 也必须用明确契约证据修正错误 fixture，不能删检查、skip 或改变安全语义掩盖失败。

每卡必须执行 `go build ./...`、`go vet ./...` 和针对性测试/受影响包 race。普通全仓测试至少执行一次且不 skip，记录实际失败。若需全仓 race，可仅排除上面四个已知失败；不重复跑昂贵全仓检查。功能测试必须有实际执行结果，不只“已写测试”。必要测试设 timeout 防止挂住。不要主动创建额外测试终端/agent，直接在本 CC 的工具执行即可。

报告包含：实际分支/基线/提交；生产入口是否接线；数据结构和兼容范围；每条命令及退出结果；成功/失败/取消/隔离的关键证据；仍缺项与实机限制；精确集成步骤。Headless ui.Tester、像素/事件输入不等于系统输入法、窗口、真实 OAuth/凭据、三平台验收。

任务完成后留在当前交互会话待主代理复核，不擅自清理分支、worktree 或其他终端。
