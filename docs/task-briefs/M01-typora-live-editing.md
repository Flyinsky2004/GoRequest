# M01：Typora 式文档编辑一致性修复

日期：2026-11-09。用户明确要求通过 Herdr 开一个 Codex，由主代理先制定每项修改方案，再让实现代理执行。不是规划专用任务，必须实际实现、验证、本地提交并写交付报告。

## 归属与基线

负责人：Herdr 中的 Codex；单代理串行处理两个问题，不开子代理。起点 main `a1c06ae`，派工文档登记提交为实际代码共同基线。分支 `codex/M01-typora-live-editing`；worktree `/private/tmp/gorequest-M01-20261109`。主仓库有用户 dev.yaml、document.md 和 debug 二进制，均不得触碰或纳入提交。不升级 Go/MyGo、不改用户配置、不 push、不部署、不重启用户应用。遵守仓库 AGENTS.md；只本地 commit。此卡承接 B09 编辑体验，不代表完整 B09 已验收。

## 先读、先复现

读 `docs/parallel-work-plan.md` 最新 M01 记录、`docs/design/markdown-live-authoring.md`、`internal/docedit/`、`internal/desktop/doc_live*.go`、`doc_table*.go`、`markdown_preferences.go` 和锁定 MyGo v0.7.4 的 editor/markdown 原生 API。先跑针对 docedit/doctable/desktop 文档的已有测试，记录复现用例和基线，不把历史报告视为本轮通过。

主代理已核对：`Projection.NewProjection` 只对标题、简单 quote/flat list 隐藏块前缀；嵌套/loose list 回退 identity，inline code/bold 没有投影。`docCanvasActiveBlock` 仍用单一 editor.View，只有 quote 额外画容器，没有项目符号和行内富文本布局。`doc_table.go` 的输入硬编码 FontSize(13)，行高30/32、字符估宽7.2，和 Markdown 偏好/预览脱节。这是待进一步复现的根因线索，不是已完成修复。

## 问题一：列表、反引号等编辑时暴露源码

目标：默认画布点击/键盘进入已解析内容后，无序/有序/任务列表继续显示项目符号、编号、复选框和真实缩进；完成的行内代码继续使用等宽字体/代码背景，反引号分隔符隐藏；粗体、斜体和链接同样审计并尽量统一。代码块显示可编辑代码内容、围栏隐藏。不能只把语法字符涂成背景色；其占位、点击定位、选择区也要正确。源码模式仍能编辑原始 Markdown。未闭合/转义符号按字面保留，不能正则删字符损坏文本。

实施顺序：

1. 建立本轮复现矩阵：flat/nested/loose/multiline list，非1起始和 `1)` 编号，task checkbox，列表内行内代码/粗体/链接，单/多反引号 code span、代码围栏。记录当前编辑态和预览态差异。
2. 在 docedit 层建立/扩展结构化可见文本投影：利用既有 Markdown AST 的真实源范围（先核实 API 的范围语义），为内容 run、隐藏 delimiter、列表 item/depth/marker/continuation 提供元数据与 source↔visible 的 UTF-8 字节偏移映射。保存仅修改实际被编辑的源范围；没动的 delimiter、链接地址、空格、编号/缩进、CRLF 和未知扩展逐字节保留。不要全文件 AST 重序列化。
3. 先落实列表 item 的原生结构编辑：marker gutter 与内容区域共用渲染几何，自动换行对齐正文，嵌套列表缩进保留；不要简单投影成无标记纯文本。Enter 续项/空项退出，Tab/Shift+Tab 缩进，Backspace 项首行为，checkbox 点击，至少覆盖当前支持的平级与普通嵌套列表，复杂结构不能默默降级成整块源码。
4. 落实行内样式编辑：先检验锁定 MyGo 的富文本/编辑布局/自定义绘制接口。使用可见 run 布局与光标、hit-test、选区同一几何数据，隐藏闭合语法且保留 inline code/strong/emphasis/link 样式。若原生 editor.View 无法正确提供，使用仓库内有界原生文档编辑适配层/专用 widget；不能直接编辑 Go module cache，不能悄悄更换依赖或引入 WebView。只隐藏字符但仍用错字号/光标坐标不能交付。先在报告说明具体可用 API 与方案选择再实现，无需再次请求用户批准常规实现。
5. 保持 canonical `tab.DocEditor.Buffer` 是唯一正文，沿用最小事务与全局撤销；同一操作一次撤销。输入期间不能每帧重置 buffer/focus；中文组合期间不重解析/重建活动输入，候选 Enter 不当成续项。源范围/可见范围映射必须用于格式命令、slash、@引用、选择、撤销、块拆合、保存前同步，避免源文本偏移混用。
6. 完成后重新测试实际激活编辑态，不能用失焦后的渲染截图证明编辑正确。未支持结构显式标记并列限制；列表和行内代码这两个核心目标未完成不可宣称本卡完成。

## 问题二：表格编辑字号和样式偏小

目标：同一文档同一表格在预览、画布未聚焦、单元格聚焦三态字号/字体/行距/表头权重/对齐/边框/背景/内边距一致。聚焦只增加主题光标/选区/焦点提示，不缩小字体、不跳行、不突然改变列宽。文档字号和行距设置实时生效。

实施顺序：

1. 查看 MyGo markdown 表格渲染 Theme/metrics 的真实字段与布局，比较 `docMarkdownTheme`、canvas Options 与预览 Options 的偏好解析，收敛为同一 Markdown theme/metrics 来源，不在两边复制独立常数。
2. 从调用方传递解析后的文档样式给 table renderer（保留必要的测试注入契约或同步调整相关测试）。去掉正文输入13px及固定估宽/行高依赖，字体家族、字号、权重、颜色、行距与 header/cell padding 从共用 theme 派生。优先真实字体测量 CJK/等宽内容；最小尺寸只是下限，不是固定内容高度。主题/字号变化要使列宽缓存失效，但不能丢活动输入或重置选区。
3. 单元格非活动态使用与预览一致的 Markdown inline 渲染；活动态沿用问题一的内容编辑能力，粗体/代码/链接保持外观。Tab/Shift+Tab/末格扩行、对齐、增删行列、TSV 粘贴、横向滚动、转义、200×30 等既有保护全部保留；普通单元格输入不触发整表重排闪动。
4. 在亮/暗主题、默认及调大文档字号、1200×820 与1024×640检查聚焦前后字体/布局。宽表格滚动在容器内；长文本/CJK不裁剪。工具栏仍为应用 UI 字号，内容字号遵从文档设置。

## 验证与交付

- 增补有意义的 docedit 投影与源范围往返测试：UTF-8/emoji/CRLF、可见边界偏移、未改内容字节保持、嵌套/续行/空项、转义和不同长度反引号、只改一个 run、格式操作与undo/redo。
- 使用 MyGo headless tester 实际点击激活列表/段落/代码/表格并键盘输入，验证保存/重开、末笔输入同步、跨块撤销、slash/@引用回归和焦点。视觉验证需要渲染帧/截图或字体布局断言，不能仅断言文本中无符号。保留亮暗主题及两种字号对照图到临时目录；报告给出路径。
- `go test ./internal/docedit ./internal/doctable ./internal/desktop`、`go build ./...`、`go vet ./...`；必要的变更专项 `-race`。完整测试失败要查根因，分清新旧，不 skip 后宣称全通过。
- 若可访问本地原生窗口，完成真实交互检查；无实机IME/三平台证据则明确待验收。不能把 headless 文本注入说成中文 IME 实测通过。
- 提交前看 git diff 和 staged，仅提交本卡文件。写 `docs/task-results/M01-typora-live-editing.md`：根因、API/架构选择、完成矩阵、命令/结果、图片位置、限制、commit。
- 更新账本 M01 行的来源提交与验证状态，未集成/未验收不勾完成。不改旧卡状态，不合并 main，不清理其他会话。保持 Herdr Codex 会话可继续交互，完成或卡住时给出清晰结果。

## 参考依据

- https://support.typora.io/Quick-Start/ ：inline 样式完成后 live preview，block 随输入或 Enter/切段后呈现。
- https://support.typora.io/Table-Editing/ ：原位 GFM 表格、键盘导航与结构操作。
- https://support.typora.io/Markdown-Reference/ ：列表/代码/inline 语法语义。
- 用户明确要求优先于模仿：普通正文编辑时已完成的标记继续隐藏；不能照搬“光标所在整行都暴露源码”的折中。
- 已用 ui-ux-pro-max `font size line height layout shift editing consistency --domain ux` 查询；采用统一行高/度量与聚焦不跳动原则，原生 Go 桌面不套用网页/移动端框架代码。
