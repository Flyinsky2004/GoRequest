# U11–U17 集成与验证（2026-11-08）

用户要求合并全部已完成CC任务并清理旧分支/agent。本轮仅处理U11–U17；保留其他项目、既有用户终端及MyGo showcase PR分支。

## 来源与集成基线

主工作区main `2394cce`，原有示例dev.yaml、document.md及demo-server debug产物已记录内容摘要，不带入集成。七个来源worktree初查均干净、都有交付提交/报告，Herdr均done。独立集成目录 `/private/tmp/gorequest-u11-u17-integration-20261108/worktree`，分支 `integrate/U11-U17`，依次no-ff合并全部无冲突：

| 任务 | 来源头 | 合并提交 |
| --- | --- | --- |
| U11 | `74a6c21` | `c453667` |
| U12 | `575880b` | `428957d` |
| U13 | `35d4b36` | `e720e3b` |
| U14 | `1024d9f` | `4606ee6` |
| U15 | `43f884d` | `c8f3a36` |
| U16 | `bb377af` | `15e5ff8` |
| U17 | `4eedad6` | `0f8050c` |

## 公共接线与集成修正

- U14的tab_bar补丁因U12新增WS协议色与注释无法直接应用，人工接入 `TabItemWithActions`，保留点击/双击重命名/中键关闭/右键与脏标记/滚动语义；为TabSpec增加可选协议颜色覆盖，WS保持可读颜色，不被未知HTTP方法色替代。
- 给真实工作区HTTP/WS/文档标签内容、HTTP/WS配置页和设置分类接入单层TabContent过渡，不叠旧页面；表单/编辑状态继续由原有Tab对象保存。
- U15斜杠菜单补丁因U16新增KeepFocus无法直接应用，人工组合稳定Key、DropdownInlineTransition与KeepFocus；不破坏真实editor焦点修复。鼠标选择回归用tester可控时钟等待菜单入场完成，不用真实sleep；首次全仓运行暴露的即点零高度动画菜单问题已修正测试时序，再跑全仓仅剩原4项基线失败。
- 审阅发现WS连接尚未返回Session时关闭标签只失效回调，未取消握手。新增按Tab持有的connectCancel，关闭标签/窗口、断开、清空均主动取消；连接中显示可用的断开按钮。真实本地TCP服务阻塞TLS握手的新增回归确认四种路径均关闭连接，且已通过race。
- 首次全仓race在未被七卡修改的Mock服务关闭用例中报告 `use of closed network connection`。检查发现Stop先手动Close listener，再Shutdown同一HTTP server，Shutdown可能再次关闭尚未解除注册的listener并返回错误。主分支单项race压力20次/100次未复现；因此没有把它冒称为已复现基线，也未跳过该用例。修正为先Shutdown，再关闭原listener兜底（覆盖Serve尚未注册的启动时序），随后跑Mock包压力和全仓验证，结果见下。
- U11两种项目路径、U12持久化与每Tab会话、U13设置图标、U16光标/行首slash、U17表格接口均已直接接线。原U14/U15报告的待接线描述由本节覆盖，不再次套用历史补丁。

## 验证

- `go build ./...`：通过。
- `go vet ./...`：通过。
- 受影响App/UI专项（文档/Authoring/Tab/UI01/WS/设置/欢迎/响应/布局/分栏）：通过。
- `go test -race ./internal/desktop -run TestWSWorkspaceCancelsPendingHandshake -count=1`：通过，覆盖关闭标签/窗口、断开、清空四条真实TLS握手取消路径。
- `go test ./... -count=1`最终完整执行：仅4项已存在失败，其他包与用例通过。已在尚未合并的主工作区main `2394cce`实际复跑4项，输出一致：
  - desktop `TestMockEditorSuggestionsAreProjectUnique`：copy suggestion x。
  - desktop `TestRunnerEditorDescribeAssertions`：want 6 specs, got 7。
  - desktop `TestRunnerEditorPatchSuiteExtract`：comments lost。
  - diagnostics `TestEngine_PolicyBlockedAndMissingSecretPreflight`：expected target policy error for unconfirmed prod environment。
- 不修改无关断言掩盖上述失败；普通全仓测试没有skip。
- 全仓race显式跳过且只跳过上述4项基线失败，结果在下节登记。
- `git diff --check 2394cce -- internal`：通过；全变更检查仅在历史U14/U15补丁文本中提示diff上下文的空格+tab及空行尾空格，保留合法原始patch而不改公共Git配置；go.mod/go.sum无改动。完整命令日志、用户文件摘要、来源/merge映射仅保留本机临时目录，不入Git。

## 限制与尚未验收

- 未启动真实应用窗口，未提供两尺寸真实截图；ui.Tester输入/像素/受控动画与本地网络测试不等于真实macOS输入法/跨平台视觉/辅助功能验收。
- U15仅可控Select弹层与自绘slash菜单获得开合动效。OS原生MenuButton和MyGo v0.7.4未开放表面钩子的Popover菜单维持原行为，不能宣称所有下拉均已动画；未为此升级依赖或重写系统菜单。
- U14保留NativeSegmented的原生几何变体；主题Tab/分段可复用，并不代表每一个业务页都接了独立内容淡入。已在集成时覆盖主工作区及配置/设置等核心路径。
- U12 WS复用Bearer/Basic/API Key，OAuth2/OIDC运行时token注入尚未接入，不宣称该流程可用。新请求默认{{wsUrl}}需用户配置。
- U16菜单在画布滚动内容内，矮窗口可能需要滚动；表格命令插入后仍停留活动块源码，退出块后显示表格。U17表格单元格仍编辑行内Markdown源码、无合并或多行单元格，长文连续输入不逐键重算列宽。
- 各任务其余首版边界保留U11–U17报告；本轮代码集成不代表实机产品验收全部完成。


## 最终race结果

修正Mock关闭顺序后，`go test -race ./internal/mockserver -count=20` 通过（15.679s）。最终再次执行 `go build ./...`、`go vet ./...`通过；普通全仓仅前述4项失败。

```sh
go test -race ./... -skip '^(TestMockEditorSuggestionsAreProjectUnique|TestRunnerEditorDescribeAssertions|TestRunnerEditorPatchSuiteExtract|TestEngine_PolicyBlockedAndMissingSecretPreflight)$' -count=1
```

退出码0、所有包通过（desktop 112.128s，uikit 12.033s，mockserver 2.057s），无race报告。仅跳过已有4项普通基线失败，未跳过Mock关闭用例、七卡新增用例或集成用例。失败的首次race、主分支20/100次调查输出及修正后结果均保留临时日志，不用重跑成功隐藏首次失败。


## main与清理结果

集成代码提交 `f622ec1`，main由 `2394cce` 快进到该提交。已验证7条来源提交全部为main祖先、来源工作区干净，随后逐个关闭本批7个Herdr tab，确认terminal finished后hc rm；使用非force操作删除7个来源分支/worktree及临时集成分支/worktree。最终GoRequest仅main与主工作区。

保留原有t1h/t4q/t3w/t2s及其他项目t31/t4k、MyGo showcase PR分支；用户dev.yaml/document.md/demo-server debug文件前后摘要一致。未push。来源提交和报告保留在main历史；清理记录另行提交到账本。
