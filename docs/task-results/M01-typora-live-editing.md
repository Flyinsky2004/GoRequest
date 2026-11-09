# M01 实施记录

负责人：当前 Herdr Codex 会话。分支 `codex/M01-typora-live-editing`，worktree `/private/tmp/gorequest-M01-20261109`，实际代码基线 `9f88d29dafd3577899e7c64ae107203b29cad5c6`。未集成、未实机验收。

## 复现与 API 选择（实现前确认）

基线 `go test ./internal/docedit ./internal/doctable ./internal/desktop` 通过（0.547s / 0.523s / 32.848s）。源码检查确认 flat list 仅移除前缀，没有 gutter；nested/loose/continuation list 回退 identity；inline code/strong/link 直接显示源码；fence 没有内容投影；表格所有输入固定13px、行高30/32、字符估宽7.2。

锁定 MyGo v0.7.4（只读 module cache）：

- `ui/markdown/parser.Inline.Start/End` 为 UTF-8 源字节，包含 delimiter；`Next` 跳过子树。`Code.Text` 是规范化后的内容。`Document.Inlines` 默认非 streaming，不补全未闭合语法。
- `ui.ShapeRichTextWith` / `ui.Font.LineHeight` / `ui.TextLayout` 提供样式字体、等宽代码背景、换行、UTF-16 caret、selection rect、grapheme boundary 和 hit-test。
- `ui.Element.HandleTextInput` / `ui.TextInputClient` 提供原生组合输入；范围单位是 UTF-16，需要在存储边界转换。
- 原生 `editor.View` 的 lexer face 只支持粗斜体，不能为单个 run 改字体，也不暴露 delimiter conceal 和 list gutter 的完整布局。采用仓库内有界的专用 native widget，而非修改 module cache。
- 现有 `editor.State` 保留局部源文本（仅块前缀投影），样式投影只负责显示/输入定位；canonical 仍是 `tab.DocEditor.Buffer`，不重序列化文档。
- `ui/markdown/table.go`：padding 为 `FontSize * TableCellPadding`，宽度取真实 inline 测量加 padding，行高取字体与 line-height，header 用 `StrongWeight`。

已阅读 Typora [Quick Start](https://support.typora.io/Quick-Start/)、[Table Editing](https://support.typora.io/Table-Editing/)、[Markdown Reference](https://support.typora.io/Markdown-Reference/)。遵从用户“已完成语法在编辑时隐藏”的要求。

## 实现与完成矩阵

代码提交：`a97da79b1530ae5dd2b89593faffa4ecd22bba73`；编号 gutter 的追加修复提交 `ae2f660ebafa804b398fb413ae1b9c96033140db`。提交均在本分支，没有合并 main。

| 项目 | 本轮结果与证据 |
| --- | --- |
| flat / nested / loose / continuation list | 内容投影保留原始 prefix；AST 提供 item/depth/编号与 continuation 元数据。原生 gutter 与正文分离，换行正文和续行按真实层级对齐。 |
| 非 1 起始、`)` 编号、长编号 | 保留源标记；可见编号按 AST 列表规则生成；按真实字体测量最长编号预留 gutter，不占正文或选区。 |
| task checkbox | 原生 gutter 显示状态，点击仅替换 checkbox 状态字节；一次全局撤销恢复。 |
| Enter / Tab / Shift+Tab / Backspace | 续项、空项退出一级、连同子项/续行缩进与反缩进、项首退一级/移除自身 marker。首个顶层项 Tab 不制造虚假嵌套。 |
| inline code | 单/多反引号、嵌入反引号、padding 和跨行 code span 均以真实 AST 确认闭合；delimiter 不进入布局。代码字体、字号比例、背景保留，跨行按 CommonMark 映射为空格。 |
| strong / emphasis / strike / inline link | 以 run 记录样式，隐藏闭合 delimiter/链接目的地。粗体、斜体、删除线、链接样式进入同一 TextLayout；Cmd+B/I 和格式菜单仍编辑原始局部源文本。 |
| 点击、选择、光标 | 首次激活与后续点击使用 retained layout；UTF-8 源范围与 UTF-16 原生文本服务范围分开转换。拖选、Shift 选区、方向键、按词移动/删除及双击选词使用可见文本几何。 |
| fenced code | 闭合围栏隐藏，语言/围栏长度/换行原样保留，编辑的是代码内容。未闭合围栏保留字面源码。 |
| source transaction / undo / save | canonical 仍为 `tab.DocEditor.Buffer`。新增 UTF-8 安全最小 replacement；未改 prefix/suffix 不进入内容输入事务。末笔输入保存、跨块撤销、表格 undo/redo、slash 隐藏目的地选择等回归通过。 |
| slash / @ | 保留源坐标协议；原生富文本增加复用原目录搜索与 link builder 的 @ 菜单，方向键/Enter/鼠标选择。邮箱、代码和既有引用回归随 desktop 全套测试验证。 |
| composition | preedit 仅叠加到 retained 样式 run，不重解析 Markdown、不写正文、不替换输入器；原生 commit 才一次落入原选区。模拟 candidate Enter 不续项。此项仅为 headless/客户端协议证据。 |
| 表格三态字体与尺寸 | 调用方传入 `docMarkdownTheme`；预览、idle、focused 使用相同文档字体、字号、行高、StrongWeight、表格 padding/max-column/border/header/stripe。idle/focused 为同一原生 rich input，聚焦只增加 caret/selection/ring。 |
| 表格尺寸更新 | 去掉 13px、30/32px 行高和 7.2 字符估宽。真实 inline 字体测量列宽，内容高度支持换行/CJK。字号/行距变化重算度量，保留输入实例和选区；普通输入只测受影响行，避免整表列宽闪动。 |
| 表格结构操作 | 保留 Tab/Shift+Tab/末格扩行、增删行列、对齐、TSV 矩形粘贴、转义、横向滚动、模型上限与错误提示。原生 clipboard 使用持久化 `ui.Services`，不在事件间复用失效 frame Context。 |

另修复原有容器源范围问题：Goldmark 包装层对包含围栏子块的列表可能截短。`docedit.Parse` 以 MyGo 外层 list 的真实范围补齐容器，使复杂列表保持一个来源完整的源码块，避免子块重复绘制。新增测试先复现了该问题，修复后验证为单块且逐字节往返。

## 本轮验证

- 基线三包测试通过；本轮最终 `go test ./internal/docedit ./internal/doctable ./internal/desktop` 通过（最终 desktop 32.950s）。
- `go build ./...` 通过；`go vet ./...` 通过。
- `go test -race ./internal/docedit ./internal/doctable` 全部通过。
- desktop 专项 race：`go test -race ./internal/desktop -run 'TestM01|TestDocTable|TestAuthoringIntegratedTableEditUndo|TestDocCanvasSlashOffset|TestDocCanvasCrossLine' -count=1` 通过；追加 gutter 修复后再次执行 `go test -race ./internal/desktop -run TestM01 -count=1 -v` 通过（11.473s，含新增长编号断言）。
- `TestM01` 覆盖活动编辑态光标/hit-test、首次点击、真实 style span、代码输入保真、checkbox 单步撤销、结构按键、子树缩进、@ 选择、格式缓存失效、关闭 delimiter 后继续输入、保存末笔、IME 客户端组合协议、表格 code 内容编辑、聚焦不跳布局、样式更新保留选区和长编号 gutter。
- 原有断言按新的行为调整，保留功能检查：代码/嵌套列表改为内容投影；列宽改为主题和真实测量；slash 测试点击查询末尾而非 marker gutter。未 skip 测试，也没有把失败标成通过。
- 本轮 `git diff --check` / staged diff 检查通过。只纳入此任务代码、测试、报告与账本。

## 视觉证据

最终代码 `ae2f660` 的 race 验证生成并保留：

- 表格：`/var/folders/mg/7wt8b3r108sdzk4mvgt5y_kw0000gn/T/gorequest-M01-visuals-3997066901/`
- 活动列表：`/var/folders/mg/7wt8b3r108sdzk4mvgt5y_kw0000gn/T/gorequest-M01-list-visuals-1620459211/`

测试在临时目录写 PNG，不在仓库保存 debug 产物。

- 表格：亮/暗，14/21 DIP 正文，1200×820 与 1024×640，idle/focused 各一张，共 16 张。每张含同源 Markdown preview 和原生表格的上下对照。
- 列表：应用实际 100%/150% 文档字号，亮/暗，两种窗口尺寸，共 8 张，均为已激活且有原生 caret 的编辑态，包含嵌套、代码、粗体、链接、checkbox、长 CJK 自动换行与续行。
- 已实际查看表格亮主题大字号 focused 对照图、列表暗主题大字号小窗口 active 图；另有字体、code run、caret/hit-test、focused 前后 element bounds 完全相等、输入实例及选区保留的测试断言。并非失焦截图或仅断言源码字符消失。

## 限制与待验收

- 尚未集成 main，未勾 M01、B09 或三平台实机验收。此会话可继续交互；未 push、部署、重启用户应用。
- 没有操作用户正在运行的原生 GoRequest 窗口，没有真实中文 IME 候选窗/撤销/组合确认实测；macOS、Windows、Linux 原生输入与渲染仍须集成人验收。headless 文字输入和 TextInputClient 测试不等于实机 IME 通过。
- 包含嵌入代码块、表格、标题、引用、HTML 等子块的复杂列表明确回退为一个保真的源码块；普通嵌套/loose/续行列表不回退。
- 原生行内层支持 code/strong/emphasis/strike/已解析 inline link。转义、entity、图片、HTML、数学等未另建富文本对象的内容保留源拼写，未声称与预览所有扩展像素等价；只在其他块定义的 reference link 尚不向局部 parser 注入全篇 definitions。
- CRLF 与无最终换行已测试；混合换行风格沿用现有 canonical editor 的行尾策略，未增加逐行混合行尾存储。
- 新控件是有界文档适配层，未替换源码模式的完整 code-editor 功能；跨块复杂选区、完整 bidi 视觉亲和选区、图片对象编辑仍不在本轮已验证范围。
- 所有用户示例数据、主仓库 dev.yaml/document.md/debug 文件、全局配置、Go module cache 均未修改。没有启动子代理。
