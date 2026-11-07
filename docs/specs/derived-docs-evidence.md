# 请求派生文档与离线导出 — 实现证据（C03 phase 1）

状态：C03 第一阶段交付伴随资料；实现位于 `internal/docgen/**` 与 `internal/desktop/derived_docs*.go`。依据：[文档/代码生成规格](documentation-codegen.md) §1/§3、[任务账本](../parallel-work-plan.md) §24 C03-phase1 行、B09 交付记录。本文只记录决策、边界与静态验证；**本批次按用户既有要求未执行 go test / go build / go vet，未启动真实应用**，文中所列用例均为"待执行"。

## 1. 引用语法（定稿）

规格 §1 中的协议与指令属草案；本实现按下表定稿并冻结 v1 行为。设计原则：普通 Markdown 阅读者始终看到可读正文 + 惰性注释；语法受限到可以直接用行扫描与既有解析器处理，不新造通用 parser。

| 形态 | 语法 | 语义 |
| --- | --- | --- |
| 内联链接 | `[任意文本](gorequest://request/users.me)` | 指向稳定请求 ID；应用内点击跳转请求 Tab；导出后在 HTML 中指向同页派生卡片锚点 |
| 卡片指令 | `<!-- gorequest:request id="users.me" view="parameters,responses" -->` | 独占一行的惰性注释；渲染/导出时其后生成派生卡片块 |
| Begin 标记 | `<!-- gorequest:begin -->` | 派生块开头，由 docgen 生成 |
| End 标记 | `<!-- gorequest:end -->` | 派生块结尾；两者之间内容归 docgen 所有 |

- **ID 语法**：`[A-Za-z0-9][A-Za-z0-9._-]{0,127}`，禁止 `..`。与项目工具链已生成的 ID（core `Slug`、OpenAPI 导入的 sanitize 后 operationId）一致；超出字符集的 ID 在文档内不可引用，诊断会明确指出而不是半解析。`view` 令牌白名单：`card`、`parameters`、`responses`；未知令牌按错误诊断（拼错的分节名不会静默丢失内容）。缺省 view = 全部分节。
- **指令之外的 gorequest:// 形式**（如 `gorequest://folder/x`）一律按"格式错误引用"诊断；`IsValidRefShape` 只用前缀区分"我们的引用"与"别人的链接"。

## 2. 往返与幂等

- 展开（`docgen.ExpandDoc`）只发生"指令行 → 指令行 + Begin + 派生内容 + End"；指令行本身永远原样保留，因此定义只有一份（存储的请求 YAML）。
- 再次展开时，紧跟指令的 **Begin 标记**开启上一个生成块；展开器把该块（到下一个代码区安全的 End 标记为止）整体消费后重写。`ExpandDoc(p, ExpandDoc(p, src).Markdown) == ExpandDoc(p, src)`（待执行用例 `TestExpandDocIsIdempotent`）。
- 若指令后**没有** Begin 标记（首次展开），其后内容一概不动；用户内容永远不会被吞（`generatedBlockEnd` 只认显式 Begin）。
- 修改存储请求后重新派生，卡片随之更新（待执行用例 `TestExpandDocUpdatesAfterRequestChange`）；不渲染时不发请求——展开只读取存储定义与 contract 元数据。
- 孤立的 End 标记保留原文并按警告诊断（`docgen-stray-end`）；代码围栏内/缩进 4+ 空格的伪引用是惰性文本，不展开、不诊断（歧义一律取安全方向；该近似在文档嵌套列表内的缩进指令场景为"不识别"，属已记录限制）。

## 3. 派生卡片内容

`BuildRequestCard` 只消费：存储的 `Request`（method/URL/query/headers/body 定义）、`extensions.openapi` 中作者写入的 `OperationContract`（经 `core.GetRequestOpenAPI`）、用于鉴权摘要的 auth scheme 引用。规则：

- 参数：优先 contract 的 `parameters`（path 参数视为必填）；无 contract 时按已保存定义的启用 query/header 键值对派生，行内标注"按已保存的请求定义派生"。
- 请求体：contract 的 requestBody 媒体类型 + 单行 schema 摘要（`object{id:string,…}`，属性截断 12 个）；无 contract 时只展示存储 body 的类型与（≤4KiB、净化后的）示例。
- 响应：只输出 contract 记录的状态（具体码先、段码/`default` 后）；无 contract 时不发明响应表，改以说明行呈现。
- 鉴权：只展示类型与 scheme 标识（如 `BEARER (ref "bearer-auth")`）；token/password/api key 值永不输出。
- **公开示例**：仅消费作者显式写入 contract 的 `example`/`examples`；派生时经 Scrubber：注册机密（`core.Redactor`）→ 标准敏感头名（authorization/cookie/…，含项目 apikey 头名）整值打码 → 凭据形态（`sk-`/`ghp_`/`AKIA` 前缀、长 base64/hex 高熵串、PEM 私钥、`Bearer/Basic` 头、URL 内 `user:pass@`）无法证明安全则整值阻断为 `***` → `{{secret.*}}` 模板引用保持字面并计数。打码/阻断计数以引用块形式回显在卡片尾部（"无法证明安全则明确阻止"落实为可见文案与 `docgen-scrubbed` 诊断）。

## 4. HTML 渲染子集（本实现边界）

`RenderHTML` 基于 `internal/markdown`（B09 既有解析器）的扁平块/内联模型渲染，未新造 Markdown parser：

- 块：H1–H6（GitHub slug 锚点；派生卡片标题行获得 `docgen-request-<id>` 锚点）、段落、围栏/缩进代码、引用、有序/无序列表（嵌套以字节区间包含关系重建；**局限**：与扁平模型的深嵌套边界情形存在近似，见第 8 条）、GFM 表格（解析器把表头与数据行同为 `TableRow`，按"首行为表头"渲染 `<thead>`）、任务列表（✓/☐ 字符，不用表单控件）、HTML 块与行内 HTML 一律转义为文本、数学块转义保留、脚注引用按 `[^label]` 文本。
- 内联：文本、软/硬换行、*em*、**strong**、`code`、~~del~~、链接、图片、脚注、行内数学；嵌套强调/链接的子内联按扁平 opener 的字节区间包含关系递归重建。
- **Mermaid**：代码块原样保留于 `<pre><code class="language-mermaid">`，前置可见警示框（默认文案 `DefaultMermaidNote`，可由导出选项覆盖）；不伪称支持，不生成错误图形。
- 输出零脚本、零事件处理器、零外链 CSS/字体、零远程图（远程图只渲染替代文本 + 可见说明）；整页样式为一段内联 `<style>`（系统字体栈）。

## 5. 离线与安全闸门

- 只读边界：解析引用/预览/导出全程不执行请求、不解析 `{{var}}` 到真实环境值、不访问 secret provider、不读取最近响应或运行历史；唯一数据源是项目内文件与作者写入的 contract 元数据。
- 读取侧：文档与附件经 `core.Store` 既有约束（拒绝绝对路径/`..`/反斜杠/控制符/symlink 逃逸，附件类型白名单 + 10 MiB， 远程/data: 拒绝加载）。
- 写侧：导出目标必须是**项目 store 之外**的绝对目录（`.gorequest` 内或文件系统根直接拒绝）；写入目录逐级 `Lstat`，存在 symlink 组件即拒绝该文件；每文件先写同目录临时文件再原子 rename，从不删除任何东西。
- **不覆盖手工文件**：每个 docgen 生成文件首行为 `<!-- gorequest:generated -->` 标记；已存在文件仅在"允许覆盖 && 自带该标记"时可替换，手工文件无论开关一律列入冲突并跳过。冲突在预览中逐条列明（`ExportConflict.TargetPath/Reason`）。
- 资源上限：文件数 200、总字节 64 MiB（可调），超限在预览中标记截断并如实列出放弃的文件/字节；单示例 4 KiB、导出摘录 2 KiB。
- HTML 安全：全部文本/属性经 `html.EscapeString`；href/src 对中文与空格路径逐段百分号编码；危险 scheme（JavaScript/data/file 等）不产生 href，只渲染文本。

## 6. 导出布局与快照摘要

- Markdown 集：`docs/**`（各文档为展开后的 `.md`，目录结构与项目一致，跨文档相对链接原样可用）+ 引用到的本地附件复制 + `README.md` 索引。
- HTML 集：`docs/**.html`（.md 链接改写为 .html，锚点保留）+ `index.html` + 附件复制；两格式文件首行均带生成标记。
- 索引含快照摘要：项目名、文档/附件/引用请求数、每个被引用请求的 ID 与 12 位存储摘要（`core.Digest(EncodeRequest)`，确定性输出，无时间戳，重复导出无无关差异）。
- 预览（`PreviewExport`，零写入）输出：文件清单（大小、来源、生成/复制类别）、逐文档 2 KiB 摘录、合并诊断（B09 链接/附件诊断 + docgen 引用诊断 + `docgen-doc-outside-export`）、目标冲突、截断状态与失败原因。

## 7. 原生界面（MyGo Native）

- 文档页头新增"导出"按钮（公共接线 patch 注入，见 §8）；面板（`derived_docs_ui.go`）提供范围（当前文档/全部文档）、格式（Markdown 集/静态 HTML）、目标目录（文本框 + 系统目录选择器，后台线程 + `win.Update` 回 UI 线程）、复制附件与允许覆盖开关、预览与导出动作。
- 预览区：计数条、可点击诊断行（跳转到原文 行:列；未展开源码的行号与编辑器一致）、冲突行、逐文档摘录。错误级诊断未清零时导出被阻止并显示具体数量。
- 插入菜单新增"请求链接…/请求卡片指令…"两个子菜单（按 project.Order 排序，上限 60 项），在光标处插入 `[名称](gorequest://request/<id>)` 或卡片指令行。
- 预览正文渲染 docgen 展开后的文本（patch 传入 `docgenPreviewSource(root, tab, src)`），无引用的文档渲染结果与原文一致；编辑器缓冲与 Problems 面板继续基于未展开源文。
- 面板状态保存在 docView 根元素的 `ui.Local`（独立 key），按 Tab 隔离并随 Tab 关闭清理——与 B09 的 docViewState 同一生命周期模式，不引入全局 UI map。

## 8. 公共接线 patch（docs/task-results/C03-phase1-integration.patch）

相对共同基线 b91ba32 可应用的精确 patch，仅含把入口接到既有界面的最小改动：

| 文件 | 改动 |
| --- | --- |
| `internal/core/doclinks.go` | 新 `LinkDocRef` 类型与 `LinkProblemMissingRequest`；`ResolveDocLink` 增加 `gorequest` scheme 分支（`resolveRequestRef` 校验 `request/<id>` 是否在项目内）；`DiagnoseDoc` 将 missing-request 映射为新诊断 `DiagMissingRequest` |
| `internal/desktop/documentation.go` | `docMessages` 增 `Export`/`MissingRequestRef` 两字段（中英）；`diagText` 增对应 case；`docWorkspaceView` 剪枝派生面板状态、渲染面板；`docHeader` 签名加 `root` 并挂导出按钮；插入菜单尾部接两个子菜单；`docBody` 调用点传入展开后预览文本 |
| `internal/desktop/documentation_actions.go` | `handleDocLink` 顶部拦截 `gorequest://` 目的地（跳转请求 Tab）；switch 增 `LinkDocRef` case |

静态核对（在未提交公共文件的约束下完成）：生成前先以基线内容为 a/ 侧生成 diff；`git apply --check` 通过后**临时**应用到本 worktree，`gofmt -e` 语法与跨包符号核对全部通过（含 `resolveRequestRef`、`LinkDocRef`、`docgenExportButton/Panel/InsertMenuEntries/OpenRequestLink/PreviewSource` 的引用一致性），随后仅将这 3 个文件恢复到基线（`git checkout --`），提交内容不含公共文件改动。集成时预计唯一冲突点：`documentation.go` 的 `docHeader` 签名行。

## 9. 静态验证记录（未编译、未运行）

在最终文件集合上执行并通过：

- `gofmt -l`（internal/docgen、派生的 3 个 desktop 文件、patch 后的 3 个公共文件）均为空；`gofmt -e` 语法解析全部通过。
- 跨包符号静态核对（脚本比对每个 `core./markdown./docgen./uikit./ui./mygo.` 引用与被引用包导出声明）：全部解析，包含 `core.GetRequestOpenAPI/SetRequestOpenAPI/Redactor/DocDiagnostic/Diag*/ResolveDocAsset/EncodeRequest/Digest/GitHubHeadingSlug/LinkAsset` 等 58 个符号；mygo UI 链式 API（`MaxHeight/MaxWidth/Key/Label/SingleLine/TrackScroll/ActionCursor/SimpleSegmented/NativeSegmented/MethodColor` 等）逐一在 mygo@v0.7.4 源码中确认存在。
- 依赖无新增：`internal/docgen` 与派生桌面文件只引用 stdlib + 既有依赖（`gorequest/internal/*`、`github.com/egoist/mygo(/ui)`、`gopkg.in/yaml.v3`）；`go.mod/go.sum` 未改动（符合"仅 P01 可改依赖"）。
- `strings.Lines`（定向行扫描依赖）在 GOROOT 源中确认为共享内存子切片迭代，`segmentOffset` 的行偏移推导成立。

**未做**（本阶段禁令）：`go build` / `go vet` / `go test` 均未执行；不把静态检查表述为编译通过。

## 10. 待执行用例（编写完成、未运行）

执行环境要求：本地 Go ≥1.27，无网络依赖。全部位于 `internal/docgen/*_test.go`：

| 用例 | 期望（一句话） |
| --- | --- |
| `TestParseDirectiveLineRoundTrip` | FormatDirective/ParseDirectiveLine 往返一致；end 标记与未知 gorequest 注释不是指令 |
| `TestParseDirectiveLineRejectsBadGrammar` | 7 类非法指令行全部报错 |
| `TestValidRequestIDCharset` | ID 字符集与长度上限 |
| `TestParseRefLink` | 合法/非法 `gorequest://request/<id>`；他 scheme 非我们的引用 |
| `TestScanDocumentPositionsAndFences` | 链接/指令行号列号准确；围栏内伪引用不算数；最多 1 个有效 users.me 指令 |
| `TestDiagnoseRefsReportsMissingAndGrammar` | 缺失请求/坏引用/无卡片引用三类诊断携带 id、行号、级别；nil project 不 panic |
| `TestMergeDocDiagnosticsDropsGorequestBaseFindings` | B09 对 gorequest 目的地的诊断被 docgen 诊断替代，其余保留 |
| `TestExpandDocInsertsMarkedBlocksAndKeepsDirectives` | 卡片/缺失注释按 begin/end 块插入；指令保留；围栏内不展开 |
| `TestExpandDocIsIdempotent` | 二次展开逐字节相同且 Changed=false；孤立 End 数量一致 |
| `TestExpandDocUpdatesAfterRequestChange` | 修改存储请求 URL 后重派生生效；对已展开文本再派生收敛 |
| `TestExpandDocCardSitesPointAtHeadings` | CardSite.HeadingLine 指向展开文本中的卡片标题行 |
| `TestExpandDocNilProjectStillDerivesNotes` | nil project 仍产出可见缺失说明块 |
| `TestDiagnoseRefsStrayEndLines` | 孤立 End 标记行号与警告；对已展开文本再展开收敛 |
| `TestBuildRequestCardFromContract` | contract 参数/体/schema 摘要/响应（具体码先、段码后）全部按数据派生 |
| `TestHealthCardDerivesFromDefinitionOnly` | 无 contract 请求按定义派生参数；Authorization 值打码；不发明响应 |
| `TestBuildCardMarkdownLayout` | 中文标签表、打开请求链接、打码占位、secret 模板字面保留 |
| `TestDeprecatedCardMarksHeading` | 弃用徽标进入标题 |
| `TestScrubberMasksByRegisteredSecret` | Redactor 注册值打码且计数 |
| `TestScrubberBlocksCredentialShapes` | Bearer/PEM/ghp_/AKIA/高熵 blob 全部整值阻断 |
| `TestScrubberKeepsReadableValues` | 普通模板/日期/头值零误伤 |
| `TestScrubberCountsSecretTemplateRefs` | `{{secret.*}}` 计数、字面保留 |
| `TestURLCredentialsAreMasked` | URL 内 user:pass 打码 |
| `TestNamedValuesMaskWholeValue` | 敏感名整值打码 |
| `TestRenderHTMLHeadingAnchorsAndCards` | 标题 slug 锚点 + 派生卡片锚点 |
| `TestRenderHTMLLinkClasses` | gorequest 链接→同页锚点；坏引用无 href；外链保留；危险 scheme 不产 href；相对 .md→.html 且锚点百分号编码 |
| `TestRenderHTMLImages` | 本地图 <img>；远程/缺失图只给替代文本 + 可见说明；从不出现 `src="http` |
| `TestRenderHTMLTablesAndLists` | 表头/对齐/单元格；嵌套列表包含关系渲染；任务字形 |
| `TestRenderHTMLMermaidAndHTMLBlockStaysInert` | 无 `<script>` 输出；HTML 块转义为源码；Mermaid 源码保留 + 警示框 |
| `TestRenderHTMLDocumentWrapper` | doctype/meta/内联样式；无 link/script；标题转义 |
| `TestPreviewMarkdownSetPlansFilesWithoutWriting` | 预览零写入；文件清单（文档+附件+README）；诊断类别齐全 |
| `TestPreviewHTMLSetPlansAssetsAndEscapes` | html 集、index.html、附件收集、无脚本/远程资源 |
| `TestRunExportWritesAtomicallyAndReRuns` | 首写成功带生成标记；无覆盖开关时全部冲突；允许覆盖时仅替换带标记文件 |
| `TestRunExportNeverOverwritesManualFiles` | 预置手工文件 + 允许覆盖仍 0 写入，内容原样 |
| `TestExportRefusesTargetInsideProjectStore` | store 内目标拒绝 |
| `TestExportSymlinkAndTraversalProtection` | 目标目录含 symlink 组件时写入失败 |
| `TestExportLimitsBoundTheSet` | 文件上限截断计划并如实标记 |
| `TestExportSingleDocumentDiagnosticsForOutsideLinks` | 单文档导出时项目内未入选文档链接给 info 诊断 |
| `TestPreviewNilProjectFailsCleanly` | nil project 返回 Err、无诊断 |

## 11. 未验收 / 限制

- **未编译**：整模块从未 `go build`；类型/签名核对为静态比对，可能有遗漏（重点复核点：`RequestCard` 未导出字段与渲染函数参数、`exportSet` 私有方法、测试文件字段名）。
- **未人工验收**：真实窗口中的面板/按钮/菜单交互、目录选择器、状态栏文案与中文排版需实机验收。
- **跨平台未验收**：Symlink 语义（Windows）、路径分隔、中文/空格路径在 Windows/Linux 文件系统上的实际行为未在目标平台验证（开发平台 macOS，逻辑层全部采用 store 既有约束 + 显式百分号编码）。
- 语义近似：4 空格缩进视为代码区（列表项内的缩进指令不识别）；HTML 锚点 fragment 不做百分号再编码；`generatedBlockEnd` 依赖生成块围栏自平衡（卡片示例的围栏总是成对输出并在其中再缩进防穿透）。
- 净化为启发式：`byteVariety ≥ 0.30` 的长 base64/hex 判为凭据；极短或刻意伪装的凭据可能漏网，作者回显的计数用于人工复核，不能替代审阅。
- Mermaid 仅保留源码 + 警示；子集渲染属后续卡片（账本 C03 行"Mermaid 原生渲染另期"）。
- 导出集外诊断（`docgen-doc-outside-export`）只为 info 提示，HTML 中相应链接退化为纯文本，不产生死链。
