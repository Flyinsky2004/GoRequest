# M03 文档聚焦布局稳定性修复

负责人：当前 Herdr 交互 Codex，单代理；未启动子代理。分支 `codex/M03-stable-document-layout`，worktree `/private/tmp/gorequest-M03-20261109`。实际共同基线 `2aa2aba`；来源为 [实施方案](../task-briefs/M03-stable-document-layout.md)、账本 §47–49 和只读参考图 `/private/tmp/gorequest-M03-reference.png`。实现来源提交：`2222d8899bf42e35af1e1863d1f917b94881d3bb`。

## 基线复现

先读方案、AGENTS、M01/M02 报告，再查看参考图及锁定 MyGo v0.7.4 源码。基线三包测试全部通过：docedit 0.529s / doctable 0.527s / desktop 34.931s，日志 `/tmp/M03-baseline.log`。

新增 `TestM03ReadmeFocusLayout` 通过 `App.View` 的真实画布入口加载示例 README 的原始“请求回显”三行正文，实际点击渲染块。修改前失败，日志 `/tmp/M03-repro.log`：

- idle host `{301 150 859 43}`；active text `{309 152 843 58.5}`，rich input 强制三条源码行。
- 下一个标题由 Y=195 移至 Y=214.5，位移 **19.5 DIP**。
- 修复后同一入口下标题 Y=195→195；正文按可用宽度排版，不再受三条源码行约束。

上述坐标是测试测量值，用于证据，没有写入实现作为宽度/高度补丁。

## 根因与实现

MyGo `ui/markdown/spans.go` 明确将 parser.SoftBreak 显示为空格、LineBreak 显示为换行。原 `docRichInput.layout` 按源码 `strings.Split` 逐行 Shape，使 softbreak 强制换行；纯文本还走带不同 inset 的 editor.View。

- `InlineView` 按 AST 保留 soft/hard break 的源范围：LF/CRLF、两个尾空格、反斜杠与 continuation 空白映射为一个可见字符。源码字节不被重序列化。空行块边界、list item 边界和代码字面换行不全局合并。
- 列表用原有 Projection/ListLines 的 item 元数据，仅把同项普通续行连接为空格；保留嵌套 gutter、编号、loose 间距和 checkbox。视觉行记录来源行，checkbox 与 IME overlay 不因行合并错指后续项。
- 增加非空选区的源范围内侧亲和映射；选中/删除紧挨 code、strong、link 的软换行时，不吃掉相邻隐藏分隔符。Slash/@ 保持原源坐标协议。
- 可安全投影的普通段落、富文本、标题与列表/引用，闲置与活动态使用同一 keyed host、缓存的 block state 和原生 RichText 子节点。纯文本不再切换到另一套 editor.View；输入/选择/caret 叠加于原布局。
- 原生 RichText 根据布局阶段的真实宽度测量高度；不再给富文本容器设置计算高度，不再用 600px fallback 决定首帧排版。空文本只保留主题的一行最小高度。保留主题字体、行高、code/strong/link 样式及 heading rule、quote bar/indent。
- 每次绘制用实际 bounds 更新 retained TextLayout 和文字起点；首次点击从 host 坐标换算到实际绘制的文字 bounds。聚焦、失焦、重新聚焦只切换输入与光标，不冻结高度；真实编辑/resize 正常重排。

只读检查 MyGo `markdown/parser/inline.go`、`spans.go`、`view.go`、`theme.go`、`ui/text_layout.go`、RichText 的原生测量/绘制路径与 editor.Buffer 的 LF/CRLF 协议。未修改依赖或模块缓存。应用了本地 ui-ux-pro-max 的 layout shift/content-driven container 审查原则；没有加入新设计系统或依赖。

## 验证矩阵

| 检查 | 自动化证据 |
| --- | --- |
| README 原始三行 softbreak | 真实画布点击前后，行数/行末/baseline/文字起点与下一个标题位置不变 |
| 临界折行宽度 | 从真实字形自然宽度推导阈值 ±1 DIP，断言一行/两行；聚焦不改变行布局 |
| 逐帧与像素 | paint observer 检查点击、焦点首帧、后续帧、Escape、refocus；比较文字行范围/Rect/baseline 和实际绘制像素，仅排除 caret 区域 |
| 主题/字号/窗口 | 13 类用例 × 亮/暗 × 100%/150% × 1200×820/1024×640，104 对、208 张 PNG |
| 文本类型 | README、纯文本、code/strong/link 混排、两类 hardbreak、CRLF/CJK/emoji、quote、nested/loose list continuation、H1–H6 |
| 首击与滚动 | 用实际文字 caret 命中并断言源偏移；真实滚动后点击不改 scroll anchor，resize 后采用新宽度，失焦不跳布局 |
| 源保真 | 聚焦/失焦/重新聚焦不改 Dirty/Undo；保存文件逐字节相等；LF/CRLF 跨 softbreak 删除/替换、保存后重新解析、单步 undo/redo |
| 分隔符/IME | 相邻 code/strong/link 的软换行替换；native 选区/前向删除保留分隔符；列表长 preedit 不错移后项源行；preedit 不写 canonical，确认才提交 |
| M01/M02 回归 | 全 desktop 普通测试及列表/表格/代码/跨行/slash 专项 race；代码和表格保留各自共用实例路径 |

几何断言只将 host 高度按 1/1024 DIP 舍入以消除 float32 坐标相减误差；文字行末、baseline、行高、宽度等仍精确比较，正文像素必须完全一致（caret 外）。没有删断言或 skip 测试。两个旧测试按正确语义加强：IME Compose 只代表 preedit，补真实 Type commit；列表软续行现在属于同一个 item layout，改为检查其同属项/缩进，保留换行检查。

## 检查命令与日志

以下全部通过，无 skip：

- `go test ./internal/docedit ./internal/doctable ./internal/desktop -count=1`：0.109s / 0.100s / 37.151s；日志 `/tmp/M03-final-tests.log`。
- `go build ./...`：exit 0，日志 `/tmp/M03-build.log`；`go vet ./...`：exit 0，日志 `/tmp/M03-vet.log`。
- `go test -race ./internal/docedit ./internal/doctable -count=1`：1.340s / 1.125s；日志 `/tmp/M03-race-source.log`。
- `go test -race ./internal/desktop -run 'TestM03|TestM02|TestM01|TestDocTable|TestAuthoringIntegratedTableEditUndo|TestDocCanvasSlashOffset|TestDocCanvasCrossLine' -count=1 -v`：43.289s；日志 `/tmp/M03-race.log`。此轮包括最终实现及当时的精简视觉矩阵。
- 将视觉矩阵扩大到所有 13 类/全部 8 种主题字号窗口组合后，追加 `go test -race ./internal/desktop -run TestM03 -count=1 -v`：107.676s；日志 `/tmp/M03-expanded-race.log`。扩大矩阵后的完整普通三包测试也已再跑通过，耗时见上文。
- 提交前审查源码、测试及 staged diff，`git diff --check` / `git diff --cached --check` 均通过。实现提交只含 8 个本卡源码/测试文件；本报告和账本作为独立交付记录提交。

## 截图

完整矩阵：`/var/folders/mg/7wt8b3r108sdzk4mvgt5y_kw0000gn/T/gorequest-M03-visuals-4220043129/`；仅位于临时目录，没有把图片/debug 产物提交到仓库。

每个文件名包含用例、dark 布尔、字号百分比、窗口尺寸及 idle/active。已实际查看 README 亮色 100% 1200×820 的 idle/active 两图、暗色 150% 1024×640 的活动图和 nested/loose list 活动图；正文换行位置一致，活动态有真实 caret。追加 race 的完整矩阵为 `/var/folders/mg/7wt8b3r108sdzk4mvgt5y_kw0000gn/T/gorequest-M03-visuals-2630621996/`，路径也记录在 `/tmp/M03-expanded-race.log`。

## 限制与交接

- **未集成 main、未实机 IME/三平台验收**。账本 M03 和完整 B09 保持未勾选；保持当前 Herdr 交互会话。
- 图片、HTML、数学、脚注、需要解码的 escape/entity、复杂不可投影列表/引用继续交给既有 Markdown/源码回退路径；没有声称这些对象编辑也达到本轮文字布局等价。跨块复杂选择、全 bidi 亲和及全篇 reference definitions 仍沿用 M01 范围。
- Canvas 与独立 Preview 共用 parser 和主题；本次保证的是所列可编辑文字块 idle/active 的同一原生排版，并未重写 Preview 的所有对象渲染。
- canonical editor 原本将 CRLF 内存文本规范化为 LF，并保留文件换行元数据；本轮测试同时验证磁盘 CRLF 原样保存。混合行尾文件仍遵从既有统一行尾策略。
- 没有操作用户正在运行的应用或真实候选窗；headless 原生客户端协议和 PNG 不等于实机 IME 验收。不声称全仓 race 已通过。
- 未 merge/main、push、部署、重启应用，未操作主目录用户数据/debug 产物、全局设置、依赖版本或模块缓存。只本地提交本卡源码、测试及交付记录。
