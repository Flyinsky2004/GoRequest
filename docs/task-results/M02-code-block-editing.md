# M02 代码块编辑交付

负责人：当前 Herdr 交互 Codex，单代理。分支 `codex/M02-code-block-editing`，独立 worktree `/private/tmp/gorequest-M02-20261109`。实际基线 `bfbaa3927f7dfeb30a2a79203d4894e1f172f0c1`；任务来源为 `docs/task-briefs/M02-code-block-editing.md` 和账本 §47/48。实现来源提交：`b3b7ea5274aa663c2d7b30e58b86518eb0439eb4`。

## 复现与根因

基线三包测试通过（docedit 0.528s / doctable 0.517s / desktop 34.958s）。随后把新增回归用例运行在基线实现上，实际得到以下失败，日志保存在 `/tmp/M02-baseline-native.log`、`/tmp/M02-baseline-reproduction.log`：

- 点击渲染代码后得到 literal `docRichInput`，没有 native language editor；`TestM02NativeCodeEditing` 断言失败。
- 空围栏的内容投影重建会凭空多出一条空行。
- 正文输入可闭合围栏的独立行时，原实现不延长围栏；`fencedCodeEnd` 还只检查三个字符的前缀，忽略实际开围栏长度、尾随正文及缩进，导致 canonical 范围截断，继而破坏编辑与 undo。

仅还原相关文件进行基线复现，结束后恢复全部本卡改动；没有操作其他 worktree。

## API 与实现矩阵

只读检查了锁定 MyGo v0.7.4 的 `ui/markdown/code.go`、Theme、`ui/editor` State/View/Buffer/LanguageNamed/输入/布局/绘制代码，没有修改模块缓存或依赖。

| 项目 | 实现与证据 |
| --- | --- |
| 编辑器分流 | 顶层 CodeBlock 独立使用原生 `editor.View`、原有 `be.ed` 和 canonical 最小 replacement 生命周期；代码不再经过 rich input、list structure、slash 或 mention hooks。 |
| 闲置/活动容器 | 两态使用同一 keyed host、语言头、复制按钮、padding 与 native editor 实例。保留语法高亮、指针选区和横向滚动，首次点击由原生代码布局直接命中，无 generic initialClick 换算。 |
| 主题/高亮 | 使用解析后的 Markdown CodeFont/CodeSize/CodeLineHeight/CodeBlockBackground/Border/Radius/Padding 和 CodeColors；背景 alpha 先合成，避免 editor 再叠一次颜色。复制头使用 Markdown 默认的 em 度量和应用已有图标。 |
| 语言 | 开围栏 info 的首词交给已安装 LanguageNamed registry，支持 go/golang、json、js/javascript 等 alias；unknown/空语言为纯文本。保存不重写 info 和属性。SetLanguage 对同一 registry 对象不重置 syntax。 |
| Enter/Tab | 原生继承缩进、Tab/Shift+Tab 与多行缩进；根据已有空格缩进估算单位，保留 tab 风格，不格式化整段。正文键入开围栏后首个 Enter 进入正文，不额外插入空行。 |
| 滚动/定位 | 默认不软换行，原生横向滚动；遵从已有 Options.WrapCode。竖向由文档 canvas 滚动。首/中/末行首次点击、滚动后命中、拖选、Shift+方向键选择均有真实控件测试。 |
| 代码字面内容 | `# * / @`、反引号、链接、中文/emoji 保持代码；粗体/斜体快捷键及对应格式入口、链接/模板菜单不会把 Markdown 包装进代码。 |
| 退出/边界 | Escape 提交并退出；普通 Enter 留在代码。块首 Backspace 与块尾 Delete 不移除围栏、不合并相邻正文；可用 Escape 后点击正文/文末添加区继续写作。 |
| 源保真 | 反引号/波浪号、长围栏、完整 info、前后空白、CRLF、无最终换行、空块、尾部空行及 UTF-8 往返；支持未闭合 fence、常规四空格或 tab 缩进代码。带 0–3 空格开围栏的内容按实际缩进投影。 |
| 围栏碰撞 | 按相同字符、足够长度、最多三个前导空格、尾部仅空白判断闭合；仅必要时延长开围栏及已有闭围栏，和正文在同一个 canonical Edit 中写入，undo/redo 一步恢复正文与围栏。 |
| 保存/模式 | 原 canonical buffer 仍为唯一正文；保存和模式 flush 包含未绘制的最后一笔输入，复制仅取代码正文，不脏、不打开 URL。 |
| IME 协议 | native preedit 留在局部 Buffer；组词期间屏蔽同步/提交，确认后一次写入 canonical 并标记 Dirty。测试覆盖多次 preedit、候选 Enter、确认、单步 undo/redo、UTF-16 原生输入路径。 |

MyGo v0.7.4 的 `editor.State` **没有公开 composition 查询 API**，而 `SetMarkedText` 会暂写 Buffer。为保留原生输入器并避免 preedit 落盘，本卡在 `docCodeComposing` 用 Go reflection **只读** `State.ed.markedActive`，无 unsafe、无内部字段修改。`TestM02NativeCompositionContract` 与实际 Compose 测试锁住该版本契约；升级 MyGo 时必须复核，待上游提供公开查询后应替换此桥接。

## 最终验证

以下均无 skip；没有删除有意义断言迁就实现。两个既有测试随明确的新行为调整：未闭合 fence 现在投影正文；代码点击目标改为正文 native editor，语言头明确不激活编辑。拖选测试给普通拖动留出双击间隔，避免测试器连续点击触发原生选词手势。

- `go test ./internal/docedit ./internal/doctable ./internal/desktop -count=1`：通过（docedit 0.108s / doctable 0.307s / desktop 33.384s）。
- `go build ./...`：通过。
- `go vet ./...`：通过。
- `go test -race ./internal/docedit ./internal/doctable -count=1`：通过（docedit 1.356s / doctable 1.136s）。
- `go test -race ./internal/desktop -run 'TestM02|TestM01|TestDocTable|TestAuthoringIntegratedTableEditUndo|TestDocCanvasSlashOffset|TestDocCanvasCrossLine' -count=1 -v`：通过（25.113s），覆盖 M02 专项和 M01 列表/表格回归。
- 完整日志：`/tmp/M02-final-tests.log`、`/tmp/M02-build.log`、`/tmp/M02-vet.log`、`/tmp/M02-race-source.log`、`/tmp/M02-race.log`。
- `git diff --check`、提交前源码/测试 diff 与暂存文件检查通过；只纳入本卡代码、必要接口、测试和交付文档。

## 视觉证据

最终专项 race 生成：`/var/folders/mg/7wt8b3r108sdzk4mvgt5y_kw0000gn/T/gorequest-M02-visuals-1392233839/`。16 张 PNG：亮/暗 × 100%/150% 字号 × 1200×820/1024×640 × idle/active。同源、同窗口、同编辑器的前后对照；图片只保存在临时目录。

每组断言聚焦前后 body bounds 完全一致，有真实 native caret，并检查活动态 keyword 颜色确实出现在绘制像素中，而非只给 Buffer 设置 language。已实际查看亮主题默认字号大窗口和暗主题大字号小窗口活动态图片，确认容器、语言头与高亮保留。

## 限制与交接状态

- 本地交付，**未集成 main、未实机 IME/三平台验收**。M02 与完整 B09 保持未勾选；集成人需复核、合入及实机验收。
- headless Compose/TextInputClient 路径与 PNG 是自动化证据，不等于操作用户原生窗口、候选窗或三平台实测。
- 列表/引用内嵌代码仍沿用 M01 的容器/源码回退范围，不声称新增独立子块编辑。缩进代码覆盖四空格/tab 前缀；不足四空格再混合 tab 等非标准组合可能保持源码缩进显示，未声称所有混合缩进语义投影完成。
- 缩进单位依据已有空格缩进的公约数估算，不是语言格式化器。独立 preview 模式继续使用 MyGo 默认 Markdown 渲染；本卡统一的是 canvas idle/active 的 native 容器，并共用 preview 的主题/lexer。
- 未运行全仓 race；真实 IME、三平台、复杂跨块选择仍留待集成人。版本私有布尔桥接的维护边界见上文。
- 未启动子代理，未 merge/main、push、部署、重启用户应用，未修改全局配置、依赖版本、模块缓存或主目录用户数据。交互会话保留。

## 集成人后续记录（2026-11-09）

用户要求合并与清理后，来源头 `fd152b6` 已通过 `f7ddfe7` 合入 main。main 上全仓普通测试无 skip 全通过（desktop 31.852s）、build/vet 通过；docedit/doctable 全套 race（1.619s/1.602s）和 desktop M02/M01/列表表格专项 race（24.367s）通过，未运行全仓 race。账本冲突保留启动与交付记录。

已确认来源干净且提交全在 main 中，关闭登记的 M02 tab，确认 `t68` finished 后移除记录；非 force 删除来源 worktree 和已合并分支，当前仅 main/主 worktree。用户未提交数据及 debug 产物摘要不变，其他会话未操作。未 push、部署或重启应用。本节更新上文“未集成/保留会话”的历史交接状态；真实 IME/三平台及已列实现限制仍有效。
