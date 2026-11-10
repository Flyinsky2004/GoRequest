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

**测试断言必须对照生成器的实际渲染规则书写，不能凭“应该长这样”的直觉写期望值。**
- F04（`cc/F04-sdk`，共同基线 `08eb3d66`）`model_test.go` 的 `TestCJKModelAndFieldNames` 对可选字段断言 `"类型 string"`，而 Go 后端对非必填字段渲染为 `Optional[string]`；测试首次运行即失败，需回头核对 `goPropertyType` 才定位，浪费一轮验证。详见 `docs/task-results/F04.md`。
- 修复 commit：本卡本地提交（修正与报告同次提交；哈希用 `git log -1 --format=%H -- docs/task-results/F04.md` 查询）。
- Done right: 写断言前先读后端映射函数（本例 `goPropertyType`/`tsType`），按必填/可空/可选三种组合各取一个真实样例；断言可执行的最小片段（`名称 string`、`类型 Optional[string]`、`fields["类型"]`）而不是整段文本。
- Seen 1× (2026-11-10).

**本机 PATH 上的工具链版本不满足生成物要求时，测试必须明确 skip 并说明“未执行”，不能报失败也不能报通过。**
- F04 中 `TestTypeScriptTypecheckIfAvailable` 直接调用 PATH 上的 `tsc`，本机为 TypeScript 2.9.2，无法解析生成物的 `.ts` import specifier 与 `rewriteRelativeImportExtensions`；表现为编译错误，容易被误读为生成器缺陷。
- 修复 commit：本卡本地提交（加入 `tsc --version` 版本闸门与 `GOREQUEST_TSC` 覆盖；哈希同上）。
- Done right: 先 `--version` 判定 5.7+ 门槛，低于门槛 `t.Skipf("TypeScript %s is too old (5.7+ required); type checking was not run")`；缺工具时 skip 文案写明“未运行类型检查”；交付报告同步写“仅用本地缓存 5.9.3 验证过一次，生成器不捆绑、不自动安装 TypeScript”。
- Seen 1× (2026-11-10).

**生成的文档必须引用实际实现的名字，不能按常见写法描述。**
- F04 生成的 TypeScript README 写路径参数使用 `encodeURIComponent`，而实现是自写的 `encodePathValue`（为与 Go `net/url.PathEscape` 逐字节对齐）；文档与代码不一致会让使用者按错误语义排查。
- 修复 commit：本卡本地提交（修正生成 README 文案并纳入生成测试断言；哈希同上）。
- Done right: 生成 README 的每个行为描述都对应一个被测试覆盖的标识符；改名或替换实现时同步改文案，并把该文案纳入生成测试的 `strings.Contains` 断言集合。
- Seen 1× (2026-11-10).
