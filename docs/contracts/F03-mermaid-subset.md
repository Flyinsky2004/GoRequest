# F03 Mermaid 离线原生子集契约

状态：F03 实现契约（2026-11-10）。实现位于 `internal/diagram/**`、`internal/desktop/doc_diagram*.go` 与
`internal/docgen/render.go`/`export.go` 的图表导出部分。本文是**能力声明**，不是完整 Mermaid 兼容承诺。

## 1. 版本与来源

- 语法基线：Mermaid **v12.3.0** 官方主文档（2026-11-10 查证）：
  - Flowchart：<https://mermaid.js.org/syntax/flowchart.html>
  - Sequence：<https://mermaid.js.org/syntax/sequenceDiagram.html>
- 实现方式：纯 Go 解析 → 受限 AST → 确定性布局 → 与 GUI 无关的 `Scene`。原生 MyGo Canvas 绘制和
  离线 SVG 导出使用**同一个 Scene**，不运行 JavaScript、不引入 Dagre、不访问网络、不加载 CDN/远程字体。
- 兼容等级：**受支持子集（subset）**。文档与界面只可表述为“支持 flowchart/sequenceDiagram 的离线子集”，
  不得表述为“支持 Mermaid”。

## 2. 通用语法规则

- 围栏信息串必须首词为 `mermaid`。
- 空行与以 `%%` 开头的整行注释被忽略；注释不能出现在语句中间。
- 图表类型由首个有效行决定：`flowchart`、`graph` 或 `sequenceDiagram`；其他类型给出“不支持”诊断。
- `%%{init: ...}%%`、`accTitle`、`accDescr`、`direction` 等指令一律拒绝，不静默忽略。
- 任何不支持的语法返回**首个**错误，带 1 基行号/列号（列按 Unicode 码点计），并保留完整源码。
  不允许“忽略未知行后画一张看似正确的残缺图”。

## 3. Flowchart 子集

声明的方向：`TD`、`TB`（两者等价）、`BT`、`LR`、`RL`；省略时默认 `TD`。

节点：

| 形式 | 含义 |
| --- | --- |
| `A` | 矩形，标签为 ID |
| `A[文本]` / `A["文本"]` | 矩形（引号内可含 `]`） |
| `A(文本)` | 圆角矩形 |
| `A([文本])` | 体育场形（stadium） |
| `A((文本))` | 圆形 |
| `A{文本}` | 菱形（判断） |

- 标签支持 `<br>`、`<br/>` 换行（解析器按物理行处理，引号内跨物理行不支持）；不支持 Markdown 字符串（`` A["`x`"] ``）与 `@{ ... }` 通用形状语法。
- 标签中的其他 HTML 标签、脚本与 `click` 一律拒绝。
- 未声明标签的节点使用 ID 作为标签；同一 ID 后出现标签时以后者为准（确定性）。
- ID 允许 Unicode 字母/数字、`_`、`-`、`.`；不允许以运算符字符开头。

连线：

| 形式 | 含义 |
| --- | --- |
| `A --> B` | 实线箭头 |
| `A --- B` | 实线无箭头 |
| `A -.-> B` | 虚线箭头 |
| `A -.- B` | 虚线无箭头 |
| `A ==> B` | 粗实线箭头 |
| `A === B` | 粗实线无箭头 |
| `A -->|标签| B`（各连线形式同理） | 连线标签 |
| `A --> B --> C` | 链式连线 |
| `A & B --> C` | 节点组展开（含目标端的 `&`） |

明确**不支持**并诊断：`--o`/`--x`/`o--o`/`x--x`/`<-->`/`<--`（多向或圆/叉端点）、`~~~` 不可见连线、
`A-- 文本 -->B` 内联标签（提示使用 `|标签|`）、`subgraph`/`end`、`class`/`classDef`/`style`/`linkStyle`、
`click`（含 `href`）、边 ID `e1@-->`、`@{ shape: ... }`。

## 4. SequenceDiagram 子集

- `sequenceDiagram` 首行。
- 参与者：`participant A`、`actor A`、`participant A as 标签`、`actor A as 标签`。`actor` 与
  `participant` 都按矩形头部绘制（不绘制人形图标，文档与报告须说明）。未声明参与者按首次出现顺序加入。
- 消息：`A->B: 文本`、`A-->B:`（虚线）、`A->>B:`（实线箭头）、`A-->>B:`（虚线箭头）、
  `A-xB:`（实线叉号端点）、`A--xB:`（虚线叉号端点）。自消息（`A->>A: ...`）支持。
- 文本支持 `<br>`/`<br/>` 换行与数字实体 `#NNN;`/`#xHHHH;`；其他 HTML 标签拒绝，语句不能跨物理行。
- 注释（Note）：`Note left of A: 文本`、`Note right of A: 文本`、`Note over A: 文本`、
  `Note over A,B: 文本`。
- `autonumber`（无参数）为消息加“序号.”前缀。
- 明确**不支持**并诊断：`loop`/`alt`/`else`/`opt`/`par`/`and`/`critical`/`option`/`break`/`rect`/`box`/`end`、
  `activate`/`deactivate` 与箭头 `+`/`-` 激活后缀、`create`/`destroy`、参与者 stereotype `@{...}`、
  中心生命线 `->>()`、`<<->>` 双向箭头、`-)`/`--)` 异步箭头、`autonumber <start> <increment>`。

## 5. 布局与预算

布局是自研的确定性分层/时序布局，有界、无递归展开：

- 输入源码 ≤ 64 KiB；flowchart 节点 ≤ 200、边 ≤ 400；sequence 参与者 ≤ 20、消息/注释行 ≤ 300。
- 单标签 ≤ 200 个码点、≤ 8 个显式行；换行后 ≤ 12 行；超限返回诊断。
- 布局总时限默认 250 ms，循环内检查 `context.Context`；取消/超时返回诊断而不是半成品图。
- 输出 DIP 尺寸 ≤ 2400 × 6000；SVG 序列化 ≤ 2 MiB；超限返回诊断。
- Flowchart 环：用 DFS 去除回边后做最长路径分层；回边从图右侧绕行，箭头仍指向目标边界。
- 连线端点裁剪到节点矩形/菱形边界；菱形、圆形使用各自形状的边界近似。

文本度量：`internal/diagram` 使用与字体无关的确定性度量（CJK/emoji 1.1em、其他 0.62em、行高 1.25em），
保证 SVG 与原生布局一致；桌面端另用 MyGo `ui.ShapeText` 实测中文标签宽度作为验收断言（见测试）。

## 6. Scene 与导出

- `Scene` 只含几何、文本行和样式枚举，不依赖 GUI 包。
- 原生绘制由 desktop adapter 用 `ui.Painter` 完成；SVG 导出由 `internal/diagram.RenderSVG` 完成。
- SVG：根元素含 `xmlns`、`width`/`height`/`viewBox`、`role="img"`；仅系统字体族名；无 `script`、
  无 `foreignObject`、无 `href`/`xlink:href`、无远程 `url(...)`、无事件属性。所有标签文本转义。
- docgen HTML：受支持图内联安全 SVG 并保留可展开源码；不支持图保留源码 + 明确原因。
- docgen Markdown：文档内容保持源码原样（往返安全），同一 Scene 生成的独立 `.svg` 作为生成资源加入导出集。

## 7. 诊断与接线

- 沿用既有诊断 kind `DiagMermaid`（`mermaid-unsupported`）。受支持图不再产生诊断；不支持的图产生
  **Warning**，`Message` 带具体原因，行号为文档坐标（围栏起始行 + 图内行号），列按码点计。
- 对 `core/doclinks.go`、`core/doclinks_test.go`、`core/documentation.go`、`core/documentation_test.go`、
  `desktop/derived_docs_ui.go`、`desktop/doc_live.go`、`desktop/documentation.go` 的改动只通过
  `docs/task-results/F03-integration.patch` 交付，由集成人应用。
- 兼容性表 `core.MarkdownCompatibility` 的 Mermaid 行由集成 patch 改为受支持子集说明。
