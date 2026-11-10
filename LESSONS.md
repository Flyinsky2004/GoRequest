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

**界面测试的“有像素/尺寸在范围内”断言通过，不等于画面正确；必须实际查看渲染图。**
- F03 首次实现的原生 diagram 卡片只断言“ink 像素 > 0、宽度不超过窗口”，全部通过，但实际渲染 PNG 是全宽卡片、图形缩在左上、大片空白。仅靠计数断言无法发现，查看截图后才定位；相关证据 `docs/task-results/f03-visuals/`。引入 commit 待定位（本卡提交后回填）。
- 修复 commit：待定位（本卡提交后回填）。
- Done right: 除计数外增加 ink 包围盒与卡片尺寸的比例断言（`inkBounds` 要求图形占卡片 70% 以上、卡片不超过窗口 60%），并实际打开 PNG 目视核对亮/暗、两档字号、窄宽窗口各一张。
- Seen 1× (2026-11-10).

**跨卡公共文件的改动只交 patch 时，依赖它的测试必须在未应用 patch 时显式 skip，并给出可执行的补丁路径。**
- F03 的 diagram 画布/预览测试依赖 `doc_live.go`/`documentation.go` 的公共 hook；若直接 `t.Fatal` 会让本卡在干净基线上红，若静默通过又会掩盖未接线。未定位单一引入 commit，属于新卡实现中的设计问题；证据见 `internal/desktop/doc_diagram_test.go` 的 `docDiagramWired` 门控与 skip 文本。
- 修复 commit：待定位（本卡提交后回填）。
- Done right: 用原子标志在安装 hook 时置位，测试在渲染两帧后检查该标志并以 “requires docs/task-results/F03-integration.patch (public … hooks)” 跳过；同时保留不依赖接线、始终运行的 painter/布局断言，避免整包测试在无 patch 时全空。
- Seen 1× (2026-11-10).

**契约和报告里写的诊断 kind、行号语义、改动文件清单，必须逐条对照真实代码核对，不能按设计意图书写。**
- F03 首版 `docs/contracts/F03-mermaid-subset.md` §7 写了并不存在的 `mermaid-error`/`mermaid-rendered` 诊断 kind 和 `doc_code.go` 改动，实际实现复用 `core.DiagMermaid` 且改动的是另外 7 个文件；照此交付会让集成人按错误文件接线。引入 commit 待定位（本卡提交后回填）。
- 修复 commit：待定位（本卡提交后回填）。
- Done right: 写完契约后用 `git diff --name-only` 与 `grep` 逐项核对 kind 常量、文件路径和行号换算代码，并在报告里列出 patch 的 7 个文件与行号语义（围栏起始行 + 图内行号）。
- Seen 1× (2026-11-10).

**围栏内代码的诊断行号必须换算到文档坐标，且不能假设闭合围栏一定存在。**
- F03 在 docgen 与 core 诊断里直接使用图内行号会指向错误行；同时项目解析器的 fenced block 可能止于最后一行内容，闭合围栏可省略，按“最后一行一定是围栏”处理会吃掉真实源码行。引入 commit 待定位（本卡提交后回填）。
- 修复 commit：待定位（本卡提交后回填）。
- Done right: 用 `1 + strings.Count(src[:b.Start], "\n")`（core 用 `lineColumnAt`）加图内行号得到文档行；剥离围栏时仅在最后一行确以 ``` 或 ~~~ 开头时删除，并用真实文档样例断言 `line 22:3` 这类跨围栏行号。
- Seen 1× (2026-11-10).

**通过 `Project.Store` 直接写入文档后，必须重新打开项目再做导出预览，否则新文档不在内存索引里，容易误判为导出缺陷。**
- F03 生成离线导出工件时先 `WriteAtomic` 再 `PreviewExport`，结果输出缺少新文档，一度怀疑导出代码；实际是项目内存状态未刷新。引入与修复都在临时验收脚本内，未进仓库，无功能提交可指。
- 修复 commit：无（临时脚本改为 `core.OpenProject(p.Dir)` 后重跑，脚本已删除，未提交）。
- Done right: 先写入、再 `OpenProject` 重新加载，然后 `PreviewExport`/`RunExport`；把真实导出的 HTML/Markdown/SVG 保留为 `docs/task-results/f03-artifacts/` 证据。
- Seen 1× (2026-11-10).

**按固定前缀长度切字符串前，必须先确认长度足够；能表达“去掉前缀”的意图时用 `strings.TrimPrefix`，不要用字节切片。**
- F03 的 `internal/docgen/render.go` 中 `diagramCaption` 写成 `s.Title[len("flowchart "):]`，`Scene.Title` 若短于 10 字节（如未来非 `flowchart ` 前缀或异常场景）会 panic；当前调用路径恰好总有该前缀所以测试未暴露，属自查发现未上线缺陷。引入 commit 待定位（本卡提交后回填）。
- 修复 commit：待定位（本卡提交后回填）。
- Done right: 改为 `strings.TrimPrefix(s.Title, "flowchart ")`，语义即“去掉前缀”，短字符串安全；改后 `gofmt -l` 干净并重跑 `go test -count=1 ./internal/docgen ./internal/diagram`（`ok 1.531s` / `ok 0.297s`）。
- Seen 1× (2026-11-10).

**全量测试出现与本卡无关的偶发失败时，既不能算作通过，也不能只重跑一次就宣布无关，要保留两次证据并说明归因依据。**
- F03 全量 `go test ./...` 首次在 `internal/core` 的 `TestWatcherStormCoalescingAndTempFileFiltering` 失败（`collections/burst.request.yaml changed on disk since it was read`），本卡未改 watcher；若直接写成“全绿”就是虚报。该 flake 的引入 commit 待定位（不在本卡范围）。
- 修复 commit：无（非本卡缺陷）；证据为 `/tmp/F03-final-tests.log` 的失败与 `/tmp/F03-core-retry.log`、`/tmp/F03-final-tests2.log` 的干净重跑。
- Done right: 报告中列出失败用例、原始错误、本卡是否触及相关代码的判断依据，以及单包重跑和第二次全量（23 包全 ok）的结果，不隐藏失败。
- Seen 1× (2026-11-10).
