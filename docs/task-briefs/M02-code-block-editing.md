# M02：代码块编辑体验补齐

用户反馈：M01 合入后，列表/表格等大致符合要求，代码块编辑体验没有提升。延续用户“主代理先给逐项实施方案，再通过 Herdr 开 Codex 修改”的工作方式。本卡只修代码块及必要共用事务，不重新设计已认可的列表和表格。

## 领取与保护

负责人：Herdr 交互 Codex，单代理，不开子代理。分支 `codex/M02-code-block-editing`，worktree `/private/tmp/gorequest-M02-20261109`。起点 main `f6cd400`；本方案/账本提交为实际创建基线。先读 AGENTS.md、账本 §47/48、M01 交付报告和本方案。只本地 commit，不 merge/main、不 push、不部署、不重启用户应用、不改用户偏好/全局配置/模块缓存/依赖版本。主目录有用户 dev.yaml、document.md 和 debug 二进制，不触碰。

## 主代理已查根因线索

- `doc_live.go:docCanvasActiveBlock` 强制 CodeBlock 走 `docRichInput`，设置 literal=true，因此虽然隐藏围栏且用等宽字体，但只得到无语法高亮的普通内容输入。
- 活动态没有 MyGo Markdown 默认代码块的 `CodeBlockBackground/Border/Radius/Padding` 与 language/copy header，host 使用正文 Padding(2,8)，进入编辑后视觉容器变化。
- `doc_rich_input.go` 的 Enter 直接插入换行；没有专用代码 Tab/反缩进/智能缩进。代码走通用结构/mention hook 也须确认不会误触发 slash/@、格式命令。
- `newDocBlockEditor` 未依据 fence info 配置 language，SoftWrap 固定 true。预览默认使用横向滚动，形成布局差异。
- `projectFence` 只识别已闭合反引号/波浪号围栏，必须检查空块、未闭合和缩进代码块的真实行为；不能将“隐藏闭合围栏”视为完整体验完成。

上述是静态证据，先运行已有 docedit/desktop 文档测试并复现，再决定具体改法。用户对具体最明显症状的补充若到达，优先处理，但不要停在等反馈或仅写计划。

## 实施方案一：使用真正的代码编辑器

1. 读取锁定 MyGo v0.7.4 的 `ui/markdown/code.go`（DefaultCodeBlock/Body/highlight）、`ui/editor` State/Buffer/Language/Theme/输入与光标命中 API，列清真实 API。优先为 CodeBlock 独立分流到原生 `editor.View`，复用既有 `be.ed` 及 canonical 提交生命周期，让语言 lexer、代码缩进和文本服务实际生效；不要继续为代码拼造一套普通富文本模拟。如果原生 View 有尺寸限制，先针对性验证，再使用最小适配解决，不编辑 module cache、不更换依赖。
2. 从真实 block AST/fence info 提取 language，使用已安装 lexer registry 和语言 alias，映射 `go/golang`、json、js/javascript 等；未知/无语言纯文本可编辑。info 中其他属性原样保留，不用只含语言的新串重写整行。不要把代码内 Markdown 当作富文本。
3. 为代码块 idle/active 共用外层容器与度量：背景、边框、圆角、语言头、复制按钮、内边距、代码字体、代码字号比例和 CodeLineHeight 均来自同一解析后 Markdown Theme。复制只复制代码正文。点击复制/语言栏不误激活或移动正文光标；聚焦只增加可见焦点/光标，容器不跳高、正文起点不移动。
4. 活动态提供与预览一致的语法色，尽量共用已安装 lexer 与主题色映射，不能仅配置 language 但实际渲染仍黑白。语言信息没有改变时不要每帧重建 lexer/buffer/focus。不强加与预览不符的行号/大工具栏。

## 实施方案二：代码键盘、滚动和定位

1. Enter 继承当前行缩进，Tab/Shift+Tab 对光标/多行选区正确缩进/反缩进；保持语言和已有缩进风格，不格式化整段代码。输入法组词 Enter 不当作换行/自动缩进。
2. 长行行为和预览一致，优先代码容器内横向滚动；若已有 WrapCode 配置则遵从。原生 Tab 宽度、行高、软换行与指针点击必须使用同一布局。滚动后点击首/中/末行、拖选、键盘选择和光标可见性正确。保存/菜单/模式切换不丢最后一笔。
3. 保留 canonical buffer 唯一正文与最小 replacement/全局 undo。代码内 `#`、`*`、`/`、`@`、反引号、链接、TSV 等全部作为代码文本，不启动正文 slash/@补全或富文本格式。退出编辑/继续正文要有明确键盘方式，沿用项目约定并验证空代码块、首尾边界；不要普通 Enter 就意外退出。
4. 首次点击从渲染块到代码编辑器，必须扣除真实 header/padding/横向滚动量并通过代码编辑器布局命中；当前 initialClick 为 generic rich input 设计，不能直接沿用错误坐标或强制跳行尾。稳定的 host key 与原生输入焦点要保留。

## 实施方案三：源格式与围栏安全

1. 不编辑时逐字节保留 fence 长度/字符、info、空白、CRLF、最终换行和周围块。修改正文只替换对应源范围，保持全局撤销。覆盖反引号和波浪号、>=4字符围栏、空块和单行代码。
2. 代码内容输入了足以结束当前围栏的独立一行时，不可静默截断/转成正文：依据 Markdown 真实闭合规则，在同一可撤销事务中仅必要地延长成对 fence（或者采取等价且明确的保真策略）；验证内容与相邻块仍完整。不要无条件每次增大围栏。
3. 检查未闭合 fence（输入三个反引号后 Enter 的正常创建流程）与缩进代码块，尽量进入同样容器和代码编辑；未支持的具体结构必须报告，不把支持全部代码块写成完成。对空块编辑、尾部空行尤其检查 parser source range。

## 验证与交付

- 先复现当前“激活后容器/高亮消失、Enter/Tab行为”问题，有针对性测试，再修复；不要只调整字号。
- 测试真实入口：点击已渲染代码→有焦点的 editor→输入/缩进/滚动/复制→保存重开→undo/redo；确保复制按钮不脏、不发网、不执行代码。
- docedit 源范围/UTF-8/CRLF/fence碰撞往返测试；代码编辑光标命中、语言 token/主题颜色、Tab/Shift+Tab/Enter、IME客户端协议、空块/长行、退出/模式切换、格式命令不污染代码。列表/表格和 M01 全部回归。
- 亮/暗、100%/150%字号、1200×820与1024×640的同源 idle/active 对照图；审查实际活动态语法色/容器/滚动/光标。截图存临时目录，报告给路径。Headless 文本注入不得称真实 IME/三平台通过。
- 必须执行 `go test ./internal/docedit ./internal/doctable ./internal/desktop`、`go build ./...`、`go vet ./...`；新增代码块专项 race。失败查根因，不 skip，不删除有意义断言迁就实现。
- 本地 commit 前审 diff/staged，只提交本卡相关文件。写 `docs/task-results/M02-code-block-editing.md`，登记基线、根因/API选择、实现矩阵、精确验证、截图、限制和来源提交；更新账本本卡，不勾未集成/未实机验收。不操作其他会话。
