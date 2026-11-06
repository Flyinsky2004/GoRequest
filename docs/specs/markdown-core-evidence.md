# R01 文档 Core 解析依赖证据

状态：2026-11-06，独立子任务 `R01-parser` 的证据；只覆盖文档 Core 的 Markdown 解析依赖解耦，不代替完整 R01 跨平台/无障碍/签名验收。

## 1. 为什么要改

B09 交付时 `internal/core/documentation.go` 与 `internal/core/doclinks.go` 直接导入
`github.com/egoist/mygo/ui/markdown/parser`。该子包本身是纯 Go、只依赖标准库，
但包路径属于桌面 UI 工具链；项目边界要求 `internal/core` 的依赖图中不出现任何
MyGo 包路径。集成记录 `docs/task-results/integration-2026-11-06.md` 与实现状态
§4 都明确把这一差距留给 R01。

## 2. 选型依据（本机核对过源码）

| 选项 | 本机核对结果 | 结论 |
| --- | --- | --- |
| 继续用 MyGo parser 子包 | `go list -deps ./internal/core` 命中 `github.com/egoist/mygo/ui/markdown/parser`；包路径不满足严格边界 | 不采用 |
| 把 MyGo parser 源码复制为 vendor / 内部目录 | 违反任务限制，且复制约 4.5k 行解析器代码藏成自有模块 | 不采用 |
| `github.com/yuin/goldmark` v1.8.2 | 本机模块缓存与 `README.md` 明确：纯 Go、只依赖标准库、CommonMark 0.31.2、内置 GFM 扩展；`go.mod` 为 `go 1.22`；MIT 许可；AST 保留源位置，可用 `goldmark.Extender` 补足数学语法 | 采用 |
| `github.com/russross/blackfriday` v2 | 本机只有旧版缓存；goldmark README 明确记录其非 CommonMark 且不可外部扩展 | 不采用 |

实际接口按本机 `$(go env GOMODCACHE)/github.com/yuin/goldmark@v1.8.2` 下的源码核对：
`markdown.go`、`ast/ast.go`、`ast/block.go`、`ast/inline.go`、`parser/parser.go`、
`extension/gfm.go`、`extension/footnote.go`、`extension/strikethrough.go`、
`extension/table.go`、`extension/tasklist.go`、`extension/ast/*.go`、
`renderer/html/html.go`、`util/util.go`、`util/html5entities.go`。没有声称核对过
未读的兼容性或远程文档。

选用固定版本 `v1.8.2`（本机模块缓存中有完整 zip/ziphash、离线可复现），没有为
其他并行卡预先加入任何依赖。`go.mod`/`go.sum` 本卡只新增 goldmark 及其两条校验和。

## 3. 中性适配边界

`internal/markdown` 是专属的小接口包：

- `Parse(src string) *Document` 返回扁平、源序的 `Blocks()`，每个块对应一段
  `Inlines()`；位置是源字节偏移。Core 不接触 goldmark AST，也不依赖桌面 API。
- `PlainText` 复刻旧解析器对标题纯文本的语义：Text/Code/HTML/Math 计入，换行变
  单空格，脚注引用写成 `[^label]`。
- `resolveText` 复刻旧的转义/实体行为：反斜杠转义标点是字面字符（因此 `\&amp;`
  保留为 `&amp;`），未转义的数字/具名实体解码；链接/图片 destination 与 title
  同样处理。HTML5 实体表来自 goldmark 的 `util.LookUpHTML5EntityByName`。
- 使用 `extension.GFM`（表格/任务列表/删除线/裸自动链接）与
  `extension.Footnote`（脚注定义/引用），未启用 `extension.DefinitionList`、
  `extension.Typographer`、`extension.Linkify` 之外的其他开关，也未启用
  `ExtDollarMath`；单美元数学仍为普通文本。
- `internal/markdown/math.go` 用 goldmark 官方扩展点补上旧 `parser.Default` 的
  `$$` 块、`$$…$$`、`\(…\)`、`\[…\]` 内联 Math。它只产出源区间，不渲染、不执行。
- 该包不解析、不下载、不执行 HTML；不做 Mermaid、代码生成、整套导出或任何
  UI/WebView 渲染。桌面仍用原 MyGo Native UI 与渲染器；Core 只负责大纲/搜索/
  链接/诊断/兼容矩阵所需语义。

## 4. 依赖图证明（实际命令输出）

```text
$ go list -deps ./internal/core | grep -i mygo
（无输出；grep 退出码 1）

$ go list -deps ./internal/core | grep 'gorequest/internal/markdown\|github.com/yuin/goldmark' | sort -u
github.com/yuin/goldmark
github.com/yuin/goldmark/ast
github.com/yuin/goldmark/extension
github.com/yuin/goldmark/extension/ast
github.com/yuin/goldmark/parser
github.com/yuin/goldmark/renderer
github.com/yuin/goldmark/renderer/html
github.com/yuin/goldmark/text
github.com/yuin/goldmark/util
gorequest/internal/markdown

$ go list -deps ./cmd/gorequest-desktop | grep -c 'github.com/egoist/mygo'
30
```

第二组 goldmark 包中有 `renderer`/`renderer/html` 是 `goldmark.New` 构造默认
渲染器时带入的编译依赖；Core 从不调用 `Convert` 或 `Renderer`，也不产生 HTML。
桌面仍保留 30 个 MyGo 包路径，没有替换 UI 或渲染器。另有
`internal/core/parser_dependency_test.go` 在每次 Core 测试时静态扫描全部
`internal/core/*.go` 的 import，禁止任何 `github.com/egoist/mygo` 前缀。

## 5. 语义回归与已知差异

自动化回归覆盖：

- B09 既有 `TestMarkdownCompatibilityMatrixMatchesParser` 的 30 行矩阵，全部保留
  原断言值（含 `Dollar math=false`、front matter/定义列表/Mermaid=false），没有
  放宽任何旧断言。矩阵现在由中性包解析探针计算；注释中的“与预览相同的解析器”
  不再成立，见下条差异。
- 大纲、搜索、链接、附件、诊断既有 Core/Desktop 测试全部继续通过；新增中性包
  测试覆盖中文标题/空格、引用链接、图片、附件扩展名路径、标题纯文本、GFM
  表格/任务列表/代码块、围栏代码块里的伪标题、转义/实体、脚注、Math、软/硬
  换行、位置边界、畸形输入、并发解析。
- 新增/回归用例包含成功、失败、边界和取消/隔离：`ResolveDocLink` 的越界/危险
  协议/缺失文档与锚点、诊断上限与排序、搜索上限与截断、项目隔离；中性包解析
  不持有全局可变状态，用 `-race` 并发解析同一份文档。

**明确差异（不伪造等价）**：

1. 桌面预览仍由 MyGo 的 Markdown 组件渲染，Core 现在用 goldmark 做大纲/诊断/
   兼容矩阵。两者对 B09 探针的布尔支持结论一致，但不是同一套 AST；对未列入
   B09 矩阵的边缘语法，预览与 Core 的解析边界可能不同。完整预览一致性需后续
   用桌面真实组件做人工/平台验收。
2. goldmark 的 `Linkify` 与旧解析器对裸 URL/邮箱的识别规则不完全一致；本适配
   采用 `extension.GFM` 的 Linkify 默认规则，并用 B09 的 `https://example.com`
   探针与 Core 诊断测试验证。旧解析器未公开的任意裸地址边界没有逐项等价证明。
3. footnote AST 由 goldmark 重排到文末；`Block.Start` 取定义正文首个子块的源
   位置，引用标签/偏移由源文本恢复。B09 只消费“存在脚注定义/引用”布尔值，
   大纲和链接诊断不依赖脚注块位置。
4. 源位置偏移在块级采用 goldmark 的 `Pos()`/`Lines()`，旧实现采用 MyGo 的
   `Block.Start`。两者在 ATX/Setext/围栏代码块/Mermaid 诊断行等已测场景一致
   （见 `TestOffsetsStayInsideSource` 和既有 `TestDiagnoseDocReportsPositionedFindings`），
   但没有对全部 CommonMark 构造做逐字节等价证明。
5. 旧解析器把未闭合的 `$$` 当作 Math 块到文末；本扩展同样支持，但只保证
   “其中不产生标题/链接诊断”，不保证任意畸形数学的分块边界逐字节相同。
6. 内联 `End` 偏移对 Emphasis/Link/Image/Strikethrough 等容器节点基于子节点
   最大末端，足以定位诊断起点；未承诺覆盖整个 Markdown 标记（如闭合 `]` 后的
   目标字符串）的精确末尾。Core 诊断只用 `Start` 定位。

## 6. 未完成 / 不声明完成

- C03 Mermaid 原生渲染、请求派生接口卡片、整套离线导出仍不实现。
- 完整 R01 的 macOS/Windows/Linux 真机、签名、无障碍、高 DPI、真实窗口中
  Markdown 预览与 Core 大纲/诊断的观感一致性未验收。
- 未启动真实桌面窗口，未访问真实远程仓库，未写入任何凭据/历史/设备配置。
- `docs/parallel-work-plan.md`、`docs/implementation-status.md` 与本卡整卡状态
  由集成人统一接线；本卡不自行勾选。
