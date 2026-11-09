# M03：点击进入编辑后布局不跳动

用户截图反馈：文档画布点击进入编辑时部分组件位移，原本一行的文字突然换行。延续先制定逐项实施方案、再由 Herdr Codex 实现的协作方式。单代理，不开子代理。

## 基线、范围与复现证据

main 起点 `0ff698b`（M01/M02 已集成）。本方案/账本提交作为实际创建基线。分支 `codex/M03-stable-document-layout`，worktree `/private/tmp/gorequest-M03-20261109`。归属 docedit inline/投影及 desktop doc_live/doc_rich_input 等布局路径和对应测试；不重做 M01 列表/表格和 M02 代码功能。用户 dev.yaml/document.md/debug 产物不触碰。只本地 commit，不 merge/main、push、部署、重启用户应用，不修改全局设置、依赖、模块缓存或其他会话。

截图参考（只读）：`/private/tmp/gorequest-M03-reference.png`。主代理已看到截图：README「典型操作→请求回显」正文激活时，光标落在“响应中的”之后，下方 query 与“适合验证”各起一行，后续标题随之下移。不要根据截图推算物理像素就写死宽度。

主代理核对本仓库 `examples/sample-project/.gorequest/docs/README.md:60`，该段源码确实分三行：

    `POST /echo` 携带重复的 `tag` 查询参数、自定义 `X-Demo` 头与 JSON Body。响应中的
    `query` 数组保留先后顺序与重复键，`headers` 保留同名多值，`body` 为收到的原文。
    适合验证 Query/Header 的有序与重复处理，以及 JSON Body 提交。

普通 Markdown softbreak 在渲染时会由 parser 合成空格/连续段落；`docRichInput.layout` 却对 `strings.Split(text, "\n")` 每个源码行单独 ShapeRichTextWith，强制形成视觉换行。当前 inline projection 默认对 softbreak 仍输出原始源拼写，须查 AST 核实具体行为。这是截图的高相关根因线索，必须在修改前用真实画布入口复现。除此之外，纯文本无样式段落走 editor.View（有自身 inset），有 inline 样式才切 rich widget；quote 与 list 的 gutter/spacing、heading rule 和源/可见偏移可能也有差异。

## 实施方案

1. 先读 AGENTS.md、账本 §47–49、M01/M02 报告、本方案及锁定 MyGo v0.7.4 Markdown paragraph/list/quote/inline shaping 实现。跑现有文档相关测试。新增能在基线失败的测试：同一 README 段落在宽屏 idle 时排成单行/少行，点击后不得按三条源码行强制折行；另造文本宽度刚好在换行阈值两侧的短用例，避免窗口太宽把问题掩盖。
2. 在文档可见投影中区分源码 softbreak、显式 hardbreak（两个尾空格/反斜杠等以 parser 真语义为准）、空行块边界、list item 边界、continuation、代码字面换行。段落 softbreak 显示处理必须与已安装 preview renderer 相同；不能把整篇所有换行替换空格，不能把硬换行/列表/代码合并。
3. 保留 canonical 源文本原始字节。通过 AST 与 retained run 映射将可见空格/合并字符对应到源 softbreak（支持 CRLF 与 UTF-8）。只点击、选择、移动光标、失焦、切模式、保存，不得悄悄把源码的三行重写成一行，也不能引入 Dirty/Undo。输入/删除跨 softbreak、选择区覆盖 source newline 时采用明确一致的 source↔visible 映射，最小替换，保留未知/未改内容。引用、列表续行只按真实容器语义合并；代码行保持 literal。
4. 统一 idle/active 段落的字体家族、字号、fallback、weight、line-height、inline code 字体/大小/背景 inset、链接/粗体样式、可用宽度、padding、baseline 和 block/item spacing。优先共享同一 paragraph layout/geometry 数据或 idle/active 同一原生组件，以真实源码 API 能力为准；不能用一边固定多加几像素或冻结高度遮掩不同换行。无需输入的焦点切换只改变光标/选择区/焦点提示，不改变文本布局。
5. 专门处理纯文本↔含 inline 样式时的路径切换，避免 editor.View 与 rich widget 的 4 DIP 等自身 inset 差异；行内代码相邻 CJK/英文标点的分词/断行规则须与渲染一致。heading 各级 weight/size/rule，quote bar/indent，list gutter/continuation/loose间距逐项检查。表格与代码已经采用 idle/active 同一实例，要回归而非重写它们。
6. 修复首次点击定位使用的 geometry：从实际文字起点/实际行布局做 hit-test，不再按源换行计数、行尾或未经扣减的 padding 猜位置。保持 UTF-8 source 与 UTF-16 native text 范围正确；隐藏 inline delimiter 不占宽度。连续切换块、初次布局、窗口 resize、字号变化时都使用最新布局，不能出现 first frame 600px fallback 瞬间重排。
7. 验证画布滚动不会因焦点切换改变锚点：同一正文、同一窗口/缩放、无输入，active block 的内容起点、视觉行数/行末、baseline 和高度相同，下一个标题/组件的 Y 不变化（仅允许明确的亚像素舍入误差）。长段落在光标激活时不能跳 scroll。用户真的输入造成换行则正常重排，不锁死行高。
8. 保留 IME composition 生命周期、全局撤销、源模式、格式操作、slash/@、保存最后一笔；尤其归一化 softbreak 后 slash/@ 的偏移必须仍为源坐标。已认可的 M01 列表表格和 M02 代码行为不退化。不引入 WebView、不升级依赖、不编辑模块缓存。

## 验证与交付门槛

- Test fixture 用源码同段落，图片在临时目录保存，不修改用户 README/示例数据。参考截图仅用于复现。
- 真实画布测试捕获 idle→点击（逐帧检查初次切换）→active→失焦→重新聚焦的几何、可见行/换行点和下一个组件 Y；只比较 host bounds 不够，必须有文字/行布局断言，证明不是“容器等高但文字跳行”。
- 覆盖纯段落、段内三行 softbreak、显式 hardbreak、code/strong/link 混排、CJK/emoji/CRLF、nested/loose list continuation、quote、heading、表格与代码。选择/删除/输入跨 source softbreak 后保存/重开/undo/redo，未改文本逐字节保持；单次聚焦不变 Dirty/Undo。
- 亮/暗、100%/150% 文档字号、1200×820/1024×640以及临界 wrap 宽度；每个核心用例同源 idle/active 前后截图，实际查看差异。至少复现并消除用户 README 这段的额外换行与后续标题位移。截图存临时路径，不能文本注入声称实机 IME 验收。
- `go test ./internal/docedit ./internal/doctable ./internal/desktop`、`go build ./...`、`go vet ./...` 与 M03/M01/M02/表格文档编辑专项 race；若改映射，docedit 全套 race。不要 skip/删除有意义断言迁就实现。
- 本地 commit 前审 diff/staged，只含本卡文件。写 `docs/task-results/M03-stable-document-layout.md`（基线失败证据、根因/API选择、完成矩阵、精确验证、截图、限制、来源提交），更新账本本卡。未集成、未实机验收不勾完整完成。保持 Herdr 交互会话，交付具体报告，不停在计划。

本轮已参照 ui-ux-pro-max 的 layout shift 文本排版原则查询，核心是共用真实内容度量/容器，焦点切换不触发布局变化。
