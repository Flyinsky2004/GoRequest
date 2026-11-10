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

**行/步骤并发共享的可变状态必须在锁内读写，新增的并发入口要单独跑 `-race` 验证。**
- F01 第二阶段（`e3ba1c4` 之后的未提交改动）新增 OAuth `ApplyAuth` 成功后向 `Runner.knownSecrets` 追加令牌值，同时 `buildAttemptPlan`/`dataset.go` 在其他行 goroutine 中读同一底层数组；普通 `go test` 全绿，只有 `go test -race ./internal/desktop -run TestRunnerOAuth` 报 data race。若只跑默认测试就会把真实并发缺陷当成通过交付。
- 修复 commit：`568285c`（`internal/runner/prepare.go` 增加 `secretsMu sync.Mutex` 与 `rememberSecrets`/`knownSecretSnapshot`，`runner.go`、`schema_adapter_report.go` 全部改走这两个入口）。
- Done right: 先列出哪些字段会被 row/step goroutine 并发访问，读路径返回快照副本、写路径统一走加锁方法，不在持锁期间做注册/校验等重活；新增任何并发路径后至少对该包跑一次 `-race`，而不是只跑全量默认测试。
- Seen 1× (2026-11-10).

**headless UI 断言不能依赖固定高度内层滚动区里行的几何位置。**
- F01 第二阶段 `TestRunnerResultViewRendersInLightAndDarkThemes` 用 `tt.Find("step-039")` 断言结果行可见，返回 `{X:0 Y:0 W:0 H:0}`；换成 `step-000` 同样为零，说明结果行位于 180px `ui.Scroll` 内时 `Find` 不返回可命中的几何。最初据此判断“最后一行未渲染”是错误结论，白改了一轮。
- 修复 commit：`568285c`（测试改为对分页 chrome 断言几何，对行内容用 `tt.HasText`）。
- Done right: 只对模态窗口内直接布局的控件断言 X/Y/W/H 与窗口边界；滚动区内容用 `HasText`/内容断言，并单独覆盖滚动可用性。断言失败时先区分“元素不存在”“元素存在但不在视口”“元素在滚动容器内”，再改实现或测试。
- Seen 1× (2026-11-10).

**局部编辑必须确认没有顺带删掉相邻分支，编辑后立即跑该包测试与格式检查。**
- F01 开发中一次对 `classifyRunKind` 的局部替换误删了 `timeout` 分支，同一轮还出现过 Edit 遗留多余 tab；两者都由随后的编译/测试暴露，但如果只做格式化提交就会带出行为回退。
- 修复 commit：当场恢复，未进入任何提交（无独立修复 commit）。
- Done right: 编辑前把待改函数完整读一遍，替换片段只覆盖目标分支；编辑后先 `gofmt -l` 与 `go build ./...`/该包测试，再继续下一处；提交前 `git diff` 逐 hunk 复核，确认没有删除无关分支。
- Seen 2× (2026-11-10, 2026-11-10).

**改公共内部函数签名时，先枚举全部调用点并确认归属，测试调用点与生产调用点一起改。**
- F01 第二阶段给 `prepareDesktopRunner` 增加 `desktopRunnerOptions` 参数；若不先枚举，`runner_test.go`/`runner_integration_test.go` 会编译失败，或误改到其他路文件。实际三处调用点（1 生产 + 2 测试）都在本卡文件内，无需 integration patch。
- 修复 commit：`568285c`（调用点同步更新）。
- Done right: 改签名前 `grep -rn` 全仓枚举调用点，逐个确认文件归属；跨卡公共文件只出 `integration.patch`，不直接改；改完 `go build ./...` + `go vet ./...` + 相关包测试，并在报告中写明“仅本卡内接线，无公共 patch”。
- Seen 1× (2026-11-10).

**界面测试的“有像素/尺寸在范围内”断言通过，不等于画面正确；必须实际查看渲染图。**
- F03 首次实现的原生 diagram 卡片只断言“ink 像素 > 0、宽度不超过窗口”，全部通过，但实际渲染 PNG 是全宽卡片、图形缩在左上、大片空白。仅靠计数断言无法发现，查看截图后才定位；相关证据 `docs/task-results/f03-visuals/`。引入于本卡实现期间的工作区，未单独提交，故无独立引入 commit。
- 修复 commit：`8cf6a911`（本卡首个提交，含比例断言与 8 张 PNG）。
- Done right: 除计数外增加 ink 包围盒与卡片尺寸的比例断言（`inkBounds` 要求图形占卡片 70% 以上、卡片不超过窗口 60%），并实际打开 PNG 目视核对亮/暗、两档字号、窄宽窗口各一张。
- Seen 1× (2026-11-10).

**跨卡公共文件的改动只交 patch 时，依赖它的测试必须在未应用 patch 时显式 skip，并给出可执行的补丁路径。**
- F03 的 diagram 画布/预览测试依赖 `doc_live.go`/`documentation.go` 的公共 hook；若直接 `t.Fatal` 会让本卡在干净基线上红，若静默通过又会掩盖未接线。未定位单一引入 commit，属于新卡实现中的设计问题，未单独提交；证据见 `internal/desktop/doc_diagram_test.go` 的 `docDiagramWired` 门控与 skip 文本。
- 修复 commit：`8cf6a911`（本卡首个提交，门控随实现一并交付）。
- Done right: 用原子标志在安装 hook 时置位，测试在渲染两帧后检查该标志并以 “requires docs/task-results/F03-integration.patch (public … hooks)” 跳过；同时保留不依赖接线、始终运行的 painter/布局断言，避免整包测试在无 patch 时全空。
- Seen 1× (2026-11-10).

**契约和报告里写的诊断 kind、行号语义、改动文件清单，必须逐条对照真实代码核对，不能按设计意图书写。**
- F03 首版 `docs/contracts/F03-mermaid-subset.md` §7 写了并不存在的 `mermaid-error`/`mermaid-rendered` 诊断 kind 和 `doc_code.go` 改动，实际实现复用 `core.DiagMermaid` 且改动的是另外 7 个文件；照此交付会让集成人按错误文件接线。引入于本卡实现期间的工作区，未单独提交。
- 修复 commit：`8cf6a911`（本卡首个提交，契约 §7 已改为 `DiagMermaid` + 真实 7 个文件）。
- Done right: 写完契约后用 `git diff --name-only` 与 `grep` 逐项核对 kind 常量、文件路径和行号换算代码，并在报告里列出 patch 的 7 个文件与行号语义（围栏起始行 + 图内行号）。
- Seen 1× (2026-11-10).

**围栏内代码的诊断行号必须换算到文档坐标，且不能假设闭合围栏一定存在。**
- F03 在 docgen 与 core 诊断里直接使用图内行号会指向错误行；同时项目解析器的 fenced block 可能止于最后一行内容，闭合围栏可省略，按“最后一行一定是围栏”处理会吃掉真实源码行。引入于本卡实现期间的工作区，未单独提交。
- 修复 commit：`8cf6a911`（本卡首个提交，docgen 与 core patch 均换算文档坐标）。
- Done right: 用 `1 + strings.Count(src[:b.Start], "\n")`（core 用 `lineColumnAt`）加图内行号得到文档行；剥离围栏时仅在最后一行确以 ``` 或 ~~~ 开头时删除，并用真实文档样例断言 `line 22:3` 这类跨围栏行号。
- Seen 1× (2026-11-10).

**通过 `Project.Store` 直接写入文档后，必须重新打开项目再做导出预览，否则新文档不在内存索引里，容易误判为导出缺陷。**
- F03 生成离线导出工件时先 `WriteAtomic` 再 `PreviewExport`，结果输出缺少新文档，一度怀疑导出代码；实际是项目内存状态未刷新。引入与修复都在临时验收脚本内，未进仓库，无功能提交可指。
- 修复 commit：无（临时脚本改为 `core.OpenProject(p.Dir)` 后重跑，脚本已删除，未提交）。
- Done right: 先写入、再 `OpenProject` 重新加载，然后 `PreviewExport`/`RunExport`；把真实导出的 HTML/Markdown/SVG 保留为 `docs/task-results/f03-artifacts/` 证据。
- Seen 1× (2026-11-10).

**按固定前缀长度切字符串前，必须先确认长度足够；能表达“去掉前缀”的意图时用 `strings.TrimPrefix`，不要用字节切片。**
- F03 的 `internal/docgen/render.go` 中 `diagramCaption` 写成 `s.Title[len("flowchart "):]`，`Scene.Title` 若短于 10 字节（如未来非 `flowchart ` 前缀或异常场景）会 panic；当前调用路径恰好总有该前缀所以测试未暴露，属自查发现未上线缺陷；引入于本卡实现期间的工作区，未单独提交。
- 修复 commit：`8cf6a911`（本卡首个提交）。
- Done right: 改为 `strings.TrimPrefix(s.Title, "flowchart ")`，语义即“去掉前缀”，短字符串安全；改后 `gofmt -l` 干净并重跑 `go test -count=1 ./internal/docgen ./internal/diagram`（`ok 1.531s` / `ok 0.297s`）。
- Seen 1× (2026-11-10).

**全量测试出现与本卡无关的偶发失败时，既不能算作通过，也不能只重跑一次就宣布无关，要保留两次证据并说明归因依据。**
- F03 全量 `go test ./...` 首次在 `internal/core` 的 `TestWatcherStormCoalescingAndTempFileFiltering` 失败（`collections/burst.request.yaml changed on disk since it was read`），本卡未改 watcher；若直接写成“全绿”就是虚报。该 flake 的引入 commit 待定位（不在本卡范围）。
- 修复 commit：无（非本卡缺陷）；证据为 `/tmp/F03-final-tests.log` 的失败与 `/tmp/F03-core-retry.log`、`/tmp/F03-final-tests2.log` 的干净重跑。
- Done right: 报告中列出失败用例、原始错误、本卡是否触及相关代码的判断依据，以及单包重跑和第二次全量（23 包全 ok）的结果，不隐藏失败。
- Seen 1× (2026-11-10).

**测试断言必须对照生成器的实际渲染规则书写，不能凭“应该长这样”的直觉写期望值。**
- F04（`cc/F04-sdk`，共同基线 `08eb3d66`）`model_test.go` 的 `TestCJKModelAndFieldNames` 对可选字段断言 `"类型 string"`，而 Go 后端对非必填字段渲染为 `Optional[string]`；测试首次运行即失败，需回头核对 `goPropertyType` 才定位，浪费一轮验证。详见 `docs/task-results/F04.md`。
- 修复 commit：`0859b95`（F04 本卡提交，修正与报告同次提交）。
- Done right: 写断言前先读后端映射函数（本例 `goPropertyType`/`tsType`），按必填/可空/可选三种组合各取一个真实样例；断言可执行的最小片段（`名称 string`、`类型 Optional[string]`、`fields["类型"]`）而不是整段文本。
- Seen 1× (2026-11-10).

**本机 PATH 上的工具链版本不满足生成物要求时，测试必须明确 skip 并说明“未执行”，不能报失败也不能报通过。**
- F04 中 `TestTypeScriptTypecheckIfAvailable` 直接调用 PATH 上的 `tsc`，本机为 TypeScript 2.9.2，无法解析生成物的 `.ts` import specifier 与 `rewriteRelativeImportExtensions`；表现为编译错误，容易被误读为生成器缺陷。
- 修复 commit：`0859b95`（加入 `tsc --version` 版本闸门与 `GOREQUEST_TSC` 覆盖）。
- Done right: 先 `--version` 判定 5.7+ 门槛，低于门槛 `t.Skipf("TypeScript %s is too old (5.7+ required); type checking was not run")`；缺工具时 skip 文案写明“未运行类型检查”；交付报告同步写“仅用本地缓存 5.9.3 验证过一次，生成器不捆绑、不自动安装 TypeScript”。
- Seen 1× (2026-11-10).

**生成的文档必须引用实际实现的名字，不能按常见写法描述。**
- F04 生成的 TypeScript README 写路径参数使用 `encodeURIComponent`，而实现是自写的 `encodePathValue`（为与 Go `net/url.PathEscape` 逐字节对齐）；文档与代码不一致会让使用者按错误语义排查。
- 修复 commit：`0859b95`（修正生成 README 文案并纳入生成测试断言）。
- Done right: 生成 README 的每个行为描述都对应一个被测试覆盖的标识符；改名或替换实现时同步改文案，并把该文案纳入生成测试的 `strings.Contains` 断言集合。
- Seen 1× (2026-11-10).

**新增 i18n 字段必须同一提交内补齐全部语言字面量，并靠反射测试兜住遗漏。**
- F02 的 WebSocket 脚本工作区一次性给 `wsWorkspaceMessages` 增加 32 个字段，只改了结构体就继续接线，`wsWorkspaceMessagesEn/Zh` 留空；若不补齐，`TestWSWorkspaceMessagesCoverEveryField` 会直接失败，界面也会出现空白标签。
- 修复 commit：`9db46f1`（F02交付，F02 脚本工作区），未单独拆分修复 commit。
- Done right: 加字段时立即补 EN/ZH 两份字面量并跑 `go test ./internal/desktop -run TestWSWorkspaceMessagesCoverEveryField`；反射测试是最后一道闸，不能等 UI 接线完成才发现。
- Seen 1× (2026-11-10).

**新增 UI 分区必须同时接线标签、分区 case 和上下两栏路由，否则功能实际不可达。**
- F02 实现了 `wsPlanSection`/`wsPlanResultPane` 全套渲染，却没有第四个 tab 标签、没有 `wsConfigSection` 分支、下方面板也未按分区切换；编译和单测都通过，但用户永远看不到脚本入口，等于未交付。
- 修复 commit：`9db46f1`（F02交付，`websocket_ui.go` 三处接线），未单独拆分修复 commit。
- Done right: 新分区落地时按“标签 → 分区渲染 → 上下两栏路由”三处清单逐项核对，并写 `ui.NewTester` 测试断言该分区可见且手动面板被替换；只测纯函数渲染不足以证明入口可达。
- Seen 1× (2026-11-10).

**调用依赖 entry 作用域的解析前，必须显式检查 `tab.Entry == nil`。**
- `wsPlanRun` 直接调用 `core.Project.ResolveVars(tab.Entry, …)`，而 `ResolveVars` 会解引用 `entry.CollectionVars`；未挂接请求的 tab 会 panic，而不是给出可读错误。
- 修复 commit：`9db46f1`（F02交付，`websocket_plan.go` 的 nil-entry 守卫），未单独拆分修复 commit。
- Done right: 在获取路径/项目检查之后立即守卫 `tab.Entry == nil` 并返回用户可读错误；测试用一个只设置 `Owner` 的 `&Tab{}` 断言走错误分支而非崩溃。
- Seen 1× (2026-11-10).

**给标签/徽标并行数组插入分区时，必须同步移动徽标下标或改为按标签推导，不能用旧下标。**
- F02 在 GraphQL 工作区 `labels` 的索引 2 插入 Schema 分区后，`graphql_ui.go` 仍保留 `badges[2] = badgeCount(kvCount(tab.HeaderRows))`，Header 数量徽标因此渲染到 Schema 标签上；编译、vet 和既有测试都不会发现，因为徽标是纯视觉输出。`git show HEAD:` 证实基线四标签版本中 Headers 就在索引 2，是本卡插入分区引入的回归。
- 修复 commit：`9db46f1`（F02交付，`graphql_ui.go` 改为 `badges[3]` 并加注释、`graphql.go` 的 section 注释同步改为五分区），未单独拆分修复 commit。
- Done right: 插入/删除分区后同时核对标签数组、分区 case、上下栏路由和徽标下标四处；`uikit.UnderlineTabs` 的下标与标签并行，最好按标签值或常量推导而非硬编码；至少用 `ui.NewTester` 断言徽标出现在预期分区。
- Seen 1× (2026-11-10).

**异步启动的 UI 动作，测试必须用真实完成信号同步，不能假设调用返回后状态已就绪。**
- 新增 `websocket_plan_test.go` 时，`wsPlanRun` 在 goroutine 内连接，测试却在其后同步断言 `running`，必然失败；取消测试还错误断言失败步为 1，实际取消发生在尚未连接完成时，首个适用守卫/步骤才是事实。
- 修复 commit：`9db46f1`（F02交付，测试以服务器握手 `accepted` 信号为同步点），未单独拆分修复 commit。
- Done right: 用测试服务器握手/回显信号或 `waitWS` 等待真实状态变化，不用 sleep 也不假设同步完成；断言按实际执行顺序取“最后一个已执行步骤”，并核对 `FailedStep` 与 `len(res.Steps)-1` 一致。
- Seen 1× (2026-11-10).

**给协议适配器设计前，必须先核对端口结构体实际透出的上下文，不能假设从 Plan 推断。**
- F02 第二阶段设计 Runner 适配器时发现 `runner.ProtocolRequest` 只含 `{StepID, Entry, Plan, ExecuteOptions, Redactor}`，没有计划文件路径、也没有变量表；若想当然“从 Plan 取步骤与模板值”，计划的 send/await 步骤和 `{{var}}` 模板都无处而来，适配器会变成不可用的假接线。端口设计 `e3ba1c4` 即如此，无单一回归 commit。
- 修复 commit：`9db46f1`（F02交付，新增 `internal/protocolrun/planref.go` 用请求扩展 `Extensions["wsPlan"]` 携带计划引用；接线补丁提出 `Variables map[string]string` 的最小端口扩展）。
- Done right: 设计 adapter 前先读端口源码，把所需上下文分成“端口已透出 / 需最小端口扩展 / 可用 Extensions 兼容入口绕过”三类；能走扩展的就不动冻结端口，必须动端口的在 patch 里给出最小改动与理由，并临时应用端口验证编译后再撤回。
- Seen 1× (2026-11-10).

**模块编译通过不代表产品可达，跨卡接口必须以真实端到端路径验收。**
- F01 `e3ba1c4`/`568285c` 与 F02 `9db46f1` 分别交付端口/协议模块，但变量没有透传、桌面没有装配适配器，Suite仍只选HTTP；F03 `8cf6a91` 的画布和诊断也依赖未应用的公共patch。只合并来源会留下“有模块无入口”的功能缺口。
- 修复 commit：`c1f17ce`（Mermaid入口）、`bd1978d`（Runner适配、Suite选择、变量/数据/依赖校验、失败事实）。
- Done right: 在集成树实际点击Schema入口，并以同一Runner跑loopback GraphQL/WS；检查业务errors、失败close码、报告JSON与未知计划/本机路径拒绝，不能仅测试fake port或适配器能编译。
- Seen 1× (2026-11-10).

**新后台操作必须随所属标签和窗口关闭取消，并阻止旧代际回写。**
- F02 `9db46f1` 的订阅/Introspection有启动和按钮取消，但未接removeTab/cancelAllRequests；Introspection worker还读取UI状态，关闭或修改界面可能留下连接或产生旧结果回写。
- 修复 commit：`bd1978d`。
- Done right: 共用gqlTeardownTab取消所有操作，在UI线程冻结计划/网络/鉴权输入，worker只执行冻结值，回写校验generation；测试关闭实际标签后服务端观察到订阅和Introspection连接终止。
- Seen 1× (2026-11-10).

**保存生成的Schema必须保护已有手写文件，不能直接WriteAtomic覆盖固定文件名。**
- F02 `9db46f1` 将Introspection固定写入schemas/introspected.graphql，第二次保存可能覆盖用户已有Schema；原子写保证写入完整，不代表允许覆盖。
- 修复 commit：`bd1978d`。
- Done right: 选择未占用目标并以SaveChecked空基线创建；UI点击保存回归先放置手写文件，断言其字节不变且新引用指向另一个文件。
- Seen 1× (2026-11-10).

**SDK类型必须忠实表达契约，不能用可声明但无法正确赋值的交叉类型掩盖不支持项。**
- F04 `0859b95` 将命名字段与受约束additionalProperties输出为TypeScript Record交集；索引签名也约束命名字段，可能令合法JSON无法赋值，或让实现者被迫放宽类型。
- 修复 commit：`bd1978d`。
- Done right: 纯map和unknown开放对象照常生成；TypeScript对“命名字段+受约束额外键”明确拒绝，Go不受该限制；回归测试同时检查TS拒绝和Go仍可生成，并实跑tsc。
- Seen 1× (2026-11-10).

**调用已有接口前也要核实参数类型，不能凭变量名猜测签名。**
- 本次集成未提交编辑中，将Plan误传给buildExecuteOptionsForTab的AuthScheme参数，编译立即失败；没有进入来源或功能提交。
- 修复 commit：`bd1978d`（改为解析并传入实际有效AuthScheme）。
- Done right: 修改调用前读取声明与相邻调用点，随后立即编译最小相关包；静态类型错误在继续组合验证前解决，不把失败输出掩盖为通过。
- Seen 1× (2026-11-10).

**HTTP取消测试的服务端必须先消费请求体，再观察断连，并在失败时清理连接。**
- 本次集成临时TestFormal4CloseCancelsIntrospection的handler未读POST正文就等待Context.Done，出现取消断言失败并在Server.Close阻塞，最终30秒测试超时；这是测试夹具错误，不是据此证明产品取消失败。
- 修复 commit：`bd1978d`（夹具读完正文，补CloseClientConnections兜底；订阅/取消专项随后通过）。
- Done right: 用io.Copy(io.Discard,r.Body)读完请求，再通过明确channel观察服务端Context取消；有独立超时和连接清理，保留首次失败及修正后的证据。
- Seen 1× (2026-11-10).

**全局界面缓存不能以强指针永久持有已关闭的文档状态。**
- F03 `8cf6a91` 使用全局 map 以文档状态指针作键缓存图表；即使关闭文档，map 仍持有其状态，清理回调不能正常释放该对象。
- 修复 commit：`bd1978d`（改用 weak.Pointer 键并由 runtime.AddCleanup 清理）。
- Done right: 缓存不拥有界面对象生命周期，使用弱引用或显式关闭移除，检查清理回调本身也不捕获强引用。
- Seen 1× (2026-11-10).
