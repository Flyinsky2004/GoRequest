# 错误与教训记录

所有编码代理开工、续做和派工前必读。新建 agent 时，首条任务必须明确宣告先读并遵守本文件，并将实际犯的错误和新教训写回。并行 worktree 独立追加，集成人保留合并；同类更新已有条目，不删除历史，不记录密钥或私人数据。

每条严格采用以下格式：

```markdown
**<一句话规则>**
- <哪个 commit / issue 出的事，后果是什么>
- <修复的 commit>
- Done right: <做对的例子>
- Seen N× (YYYY-MM-DD, YYYY-MM-DD).
```

Seen 只统计有证据的独立发生事件；补文档、同一事件的重复测试或重复提醒不增加次数。日期按实际发生或明确反馈日期填写；无法确认标注待核实。引入或修复 commit 尚未定位时明确写“待定位/未修复”，后续补齐，不编造哈希，也不以文档提交冒充功能修复。每条的问题/根因、修正和预防检查放进上述对应行。

**隐藏 Markdown 标记后，还必须保持编辑态的视觉和代码操作体验。**
- `a97da79`（M01）代码块只保留围栏内容与等宽字体，仍使用通用 rich input，缺少预览容器、语言栏、高亮和代码缩进；用户反馈代码块体验没有提升。详见 `docs/task-results/M02-code-block-editing.md`。
- 修复 commit：`b3b7ea5`。
- Done right: 原生代码编辑器与闲置态共用容器、主题和 lexer；实际聚焦后验证高亮、Enter/Tab、复制、滚动、保存与撤销，不能只检查标记消失或失焦预览。
- Seen 1× (2026-11-09).

**编辑与渲染必须按同一语义处理软换行，并共用真实排版度量。**
- `a97da79`（M01）新增的 rich input 按源码逐行 Shape，普通 softbreak 变成硬换行；M03 真实入口复现 README 点击后标题下移19.5 DIP，纯文本与富文本 inset 差异也会造成位移。详见 `docs/task-results/M03-stable-document-layout.md`。
- 修复 commit：`2222d88`。
- Done right: 按 AST 区分 softbreak/hardbreak，保留 source↔visible 映射，idle/active 共用原生排版；逐帧检查行末、baseline、文字起点、后续组件Y和滚动锚点，覆盖临界宽度/CJK/CRLF/字号变化；聚焦不改源码、Dirty或Undo。
- Seen 1× (2026-11-09).

**撤销和重做必须恢复对应事务的光标与定向选区，不能重置或猜测位置。**
- `e02b918` 引入的 docBlockUndo 在恢复文本后无条件 `Select(0,0)`；M04 用户反馈段落中间删除一字后Ctrl/Cmd+Z光标跳段首，保留编辑后的cursor也不能代表删除前位置。引入行已用 git blame 核实，详见 `docs/task-results/M04-undo-caret.md`。
- 修复 commit：`4783c7f`；原生IME确认回归加强：`7a17822`。
- Done right: canonical事务前后记录定向Anchor/Head，undo/redo按目标块映射到可见位置；同时断言正文、源/UTF-16选区和逐帧caret，覆盖Delete/Backspace、反向选区、softbreak、跨块和IME。元数据有界，未知事务明确回退，不另建正文历史。
- Seen 1× (2026-11-09).

**代理必须完成自己能执行的验收，并明确区分自动化、真实窗口和实机输入法证据。**
- 用户在2026-11-10询问“验收是你去还是我去手动验证”，指出长期登记待实机验收而未继续闭环的责任问题；关联账本§47–50，未定位单一引入commit。后果是验证负担留给用户，自动化未覆盖的真实交互问题持续暴露。
- 修复 commit：责任和派工约定记录于 `08eb3d6`、`33c0338`；完整实机验收仍未完成，这些文档提交不代表验收通过。
- Done right: 主代理负责用例、可执行端到端/真实窗口、故障回归与证据；用户仅协助无法访问的平台或真实候选窗。逐项标明已实现/自动验证/实机验证/未验证，headless注入和PNG不得冒充真实IME，agent done不得替代验收。
- Seen 1× (2026-11-10).

**判断当前缺口前，必须核对主线代码和最新集成证据，不能照搬历史交付限制。**
- 正式版差距复核发现旧来源报告仍写“未接线/未执行测试”，而 `545d91f` 等后续主线集成已经完成相应入口与验证；这是新旧状态并存的文档问题，未定位单一引入commit。直接照搬会误报缺口、重复派工或恢复旧实现。
- 修复 commit：`545d91f`、`a1c06ae` 的集成状态记录提供当前证据；`33c0338` 记录复核规则。旧报告保留为历史，不代表所有状态文档已完成统一更新。
- Done right: 先查HEAD、生产入口、最新集成报告和账本，明确区分来源时状态与当前状态；合并记录保留双方证据并追加新状态，不单边取旧记录、不丢弃另一方记录。
- Seen 1× (2026-11-10).

**新增 i18n 字段必须同一提交内补齐全部语言字面量，并靠反射测试兜住遗漏。**
- F02 的 WebSocket 脚本工作区一次性给 `wsWorkspaceMessages` 增加 32 个字段，只改了结构体就继续接线，`wsWorkspaceMessagesEn/Zh` 留空；若不补齐，`TestWSWorkspaceMessagesCoverEveryField` 会直接失败，界面也会出现空白标签。
- 修复 commit：本卡交付提交（F02 脚本工作区），未单独拆分修复 commit。
- Done right: 加字段时立即补 EN/ZH 两份字面量并跑 `go test ./internal/desktop -run TestWSWorkspaceMessagesCoverEveryField`；反射测试是最后一道闸，不能等 UI 接线完成才发现。
- Seen 1× (2026-11-10).

**新增 UI 分区必须同时接线标签、分区 case 和上下两栏路由，否则功能实际不可达。**
- F02 实现了 `wsPlanSection`/`wsPlanResultPane` 全套渲染，却没有第四个 tab 标签、没有 `wsConfigSection` 分支、下方面板也未按分区切换；编译和单测都通过，但用户永远看不到脚本入口，等于未交付。
- 修复 commit：本卡交付提交（`websocket_ui.go` 三处接线），未单独拆分修复 commit。
- Done right: 新分区落地时按“标签 → 分区渲染 → 上下两栏路由”三处清单逐项核对，并写 `ui.NewTester` 测试断言该分区可见且手动面板被替换；只测纯函数渲染不足以证明入口可达。
- Seen 1× (2026-11-10).

**调用依赖 entry 作用域的解析前，必须显式检查 `tab.Entry == nil`。**
- `wsPlanRun` 直接调用 `core.Project.ResolveVars(tab.Entry, …)`，而 `ResolveVars` 会解引用 `entry.CollectionVars`；未挂接请求的 tab 会 panic，而不是给出可读错误。
- 修复 commit：本卡交付提交（`websocket_plan.go` 的 nil-entry 守卫），未单独拆分修复 commit。
- Done right: 在获取路径/项目检查之后立即守卫 `tab.Entry == nil` 并返回用户可读错误；测试用一个只设置 `Owner` 的 `&Tab{}` 断言走错误分支而非崩溃。
- Seen 1× (2026-11-10).

**给标签/徽标并行数组插入分区时，必须同步移动徽标下标或改为按标签推导，不能用旧下标。**
- F02 在 GraphQL 工作区 `labels` 的索引 2 插入 Schema 分区后，`graphql_ui.go` 仍保留 `badges[2] = badgeCount(kvCount(tab.HeaderRows))`，Header 数量徽标因此渲染到 Schema 标签上；编译、vet 和既有测试都不会发现，因为徽标是纯视觉输出。`git show HEAD:` 证实基线四标签版本中 Headers 就在索引 2，是本卡插入分区引入的回归。
- 修复 commit：本卡交付提交（`graphql_ui.go` 改为 `badges[3]` 并加注释、`graphql.go` 的 section 注释同步改为五分区），未单独拆分修复 commit。
- Done right: 插入/删除分区后同时核对标签数组、分区 case、上下栏路由和徽标下标四处；`uikit.UnderlineTabs` 的下标与标签并行，最好按标签值或常量推导而非硬编码；至少用 `ui.NewTester` 断言徽标出现在预期分区。
- Seen 1× (2026-11-10).

**异步启动的 UI 动作，测试必须用真实完成信号同步，不能假设调用返回后状态已就绪。**
- 新增 `websocket_plan_test.go` 时，`wsPlanRun` 在 goroutine 内连接，测试却在其后同步断言 `running`，必然失败；取消测试还错误断言失败步为 1，实际取消发生在尚未连接完成时，首个适用守卫/步骤才是事实。
- 修复 commit：本卡交付提交（测试以服务器握手 `accepted` 信号为同步点），未单独拆分修复 commit。
- Done right: 用测试服务器握手/回显信号或 `waitWS` 等待真实状态变化，不用 sleep 也不假设同步完成；断言按实际执行顺序取“最后一个已执行步骤”，并核对 `FailedStep` 与 `len(res.Steps)-1` 一致。
- Seen 1× (2026-11-10).

**给协议适配器设计前，必须先核对端口结构体实际透出的上下文，不能假设从 Plan 推断。**
- F02 第二阶段设计 Runner 适配器时发现 `runner.ProtocolRequest` 只含 `{StepID, Entry, Plan, ExecuteOptions, Redactor}`，没有计划文件路径、也没有变量表；若想当然“从 Plan 取步骤与模板值”，计划的 send/await 步骤和 `{{var}}` 模板都无处而来，适配器会变成不可用的假接线。端口设计 `e3ba1c4` 即如此，无单一回归 commit。
- 修复 commit：本卡交付提交（新增 `internal/protocolrun/planref.go` 用请求扩展 `Extensions["wsPlan"]` 携带计划引用；接线补丁提出 `Variables map[string]string` 的最小端口扩展）。
- Done right: 设计 adapter 前先读端口源码，把所需上下文分成“端口已透出 / 需最小端口扩展 / 可用 Extensions 兼容入口绕过”三类；能走扩展的就不动冻结端口，必须动端口的在 patch 里给出最小改动与理由，并临时应用端口验证编译后再撤回。
- Seen 1× (2026-11-10).
