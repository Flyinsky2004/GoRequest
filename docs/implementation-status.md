# 实现状态与运行说明

状态日期：2026-11-05。本文记录**已实现的可运行代码**，与设计基线文档（`README`、`docs/*.md`）中的未来规划分开。设计文档仍描述完整目标；本文只写实际存在、已通过验证的能力。

> 变更：2026-11-05 用户决定取消独立产品 CLI（见 [ADR-009](decisions.md)）。本文反映取消后的状态：GoRequest 只有一个产品入口——MyGo 原生桌面应用。

## 1. 已实现（MVP）

### 纯 Go Core（`internal/core`，不依赖 GUI）

- 项目定位与加载：查找 `.gorequest/project.yaml`；加载 `collections/`、`environments/`、`auth/`、`docs/`。
- 存储：UTF-8 文本、稳定字符串 ID、`schemaVersion`；严格 YAML 解析（拒绝未知字段与重复键）；有序 Header/Query 数组。
- 安全保存：内容 SHA-256 摘要比对、同目录临时文件 + 原子替换、外部修改冲突保护（`ConflictError`）；拒绝路径穿越与符号链接逃逸。
- 变量解析：项目 → 环境 → 集合 → 请求分层合并；`{{name}}` 递归解析、循环检测、缺失即报错；`{{secret.name}}` 由注入的 `SecretProvider` 提供，明文不落项目。
- 鉴权：None / Bearer / Basic / API Key（header/query/cookie）。
- HTTP 执行：方法、URL、有序 Query/Headers（重复键保留）、JSON/Text/XML Body、Header 注入防护、绝对 URL 校验、超时（请求级或默认 10s）、`context` 取消、TLS 默认验证；响应返回状态码、协议、耗时、Body 大小、Headers、Pretty/Raw、32 MB 截断标记。
- 校验：schema 版本、ID 唯一、字段/引用/超时/body 类型/鉴权检查，返回结构化 `Issue`。
- 测试：`httptest` 覆盖执行、超时、取消、冲突、路径穿越、重复 ID、变量/鉴权、Body 往返；CLI 已删除后由 core 测试与桌面构建共同验证。

### MyGo 原生 Desktop（`cmd/gorequest-desktop` + `internal/desktop`，唯一产品入口）

- MyGo **Native UI**：GPU 绘制、无 HTML/JS/WebView 主界面。
- 左侧为始终存在的全高导航栏（默认约 260 宽，`Split` 可拖动调整）：顶部 GoRequest 名称与“打开项目”按钮，中部为独立滚动的最近项目列表（再次打开同一路径不重复、同名项目以路径区分、当前项目高亮），底部固定“设置”入口。
- 打开项目使用平台原生目录选择器（`mygo.Dialog.Open` 的目录模式），不再在全局顶栏常驻路径输入框；打开失败只在状态栏提示，不加入列表、不改变当前项目。
- 真实多项目切换：每个项目的标签页、未保存编辑、选中项与环境选择各自保留，切换不丢数据；新建项目不会清空已打开项目的编辑。
- 当前项目的 Requests/Documents 在同一导航栏的分组区域展示，新请求/新文档入口位于右侧工作区顶栏并归属当前项目。
- 原生设置面板（`ui.Modal`）：外观（跟随系统/浅色/深色，真实生效并持久化）与应用信息（版本、平台、Go 版本、配置文件位置、最近项目数）。设置往返不清空编辑。
- 最近项目列表与外观偏好保存在用户配置目录的 `GoRequest/config.json`，不写入受 Git 跟踪的项目文件；配置缺失、损坏或不可用时不崩溃，退化为内存列表。
- 右侧工作区顶部只保留当前项目/环境与工作区操作（环境切换、新请求、新文档、保存），下方仍是请求/文档标签页、编辑区和响应区；无项目时显示简洁欢迎空态与打开项目引导。
- 环境管理：工作区顶栏 Env 选择旁提供“New Env”“Edit Env”两个可发现入口。新建/编辑使用 MyGo `ui.Modal` 原生面板：环境 ID（新建可输入，编辑只读）、逐行 Key/Value 变量表格（增删行；重复 Key、填了 value 未填 Key 报错；纯空白行忽略；value 原样保留空串、内部/有效空格、URL、`:`、`=`、`{{variable}}`、`{{secret.name}}`）、独立的密钥名称声明列表（仅声明名称，不读取/回填/打印真实密钥值）。选择 `(none)` 时编辑禁用并提示原因，新建始终可用；没有环境的项目可从零创建。保存经 Core `NewEnvironment`/`SaveEnvironment` 复用 `SaveChecked`（新建要求目标文件不存在、编辑按读取基线做冲突检测），成功后即时更新该项目的 `Environments`/`EnvOrder` 与下拉（新建自动选中、编辑保留当前选择），不重新 `openProject`，不丢失标签、脏编辑、响应或表单草稿，也不修改其他项目；取消不写盘，脏草稿关闭需确认放弃，保存失败保留输入与错误。每个已加载环境记录真实相对路径与基线摘要，支持 `.yml` 及文件名≠环境 id，路径穿越/符号链接逃逸由 Store 拒绝。
- 请求编辑：方法选择、URL、超时、Params/Headers、Body（JSON/Text/XML）、Auth（none/bearer/basic）。
  - Params/Headers 为结构化逐行 Key/Value 表格：Enabled 勾选、可编辑 Key、Value、删除，固定列标题、独立滚动与“Add row”。每行有稳定 UI 标识，删除中间行不串行。Key 输入框为可编辑的自动补全框（MyGo 原生 `Autocomplete`）：可按输入过滤、上下键选择、Enter 确认、Escape 关闭，且允许任意自定义输入；Headers 与 Query 使用互不混用的建议集合，Content-Type/Accept 的 Value 提供媒体类型建议（仍可自由输入）。
  - 结构化行状态直接映射 `core.KV`：有序往返保留重复键、空 value、含 `:`/`=`/有效空格的值以及 `{{variable}}`/`{{secret.name}}`；已保存的禁用行打开后仍可见并保持禁用；纯空白行在保存/发送时被丢弃，填了 value 却漏 key 的不完整行就近提示并在保存/发送时报错。Query value 不按 `&`/`=` 切分，Header value 不按 `:` 切分。
- 发送/取消（后台 goroutine + `Window.Update`），响应区显示状态码/协议、耗时、大小、发送中/错误/取消状态。执行回调始终更新发起请求的项目的标签，切换项目后不会串到其他项目。
  - Body / Headers 分段切换，默认 Body。Body 编辑器只显示响应正文；Pretty/Raw 仅作用于正文，Pretty 复用 `ExecResult.Pretty` 的缩进 JSON（原生编辑器启用 JSON 高亮），Raw 保留原始正文，均不拼入 HTTP 头或状态行。
  - Headers 以只读 Key/Value 表格展示并保留重复头（不转成会丢重复的 map），可滚动、可选中复制；Authorization/Set-Cookie 等敏感头保持脱敏（`***`），切换视图不泄露。
  - 空正文、非 JSON、非法 JSON（回退原文）、请求错误/取消、32 MB 截断均有合理表现；截断提示在正文之外展示，不写入 JSON 正文。正文/头编辑器缓存按响应版本与 Pretty/Raw 状态失效，切换不出现旧内容、不每帧重建或重复解析。
- Markdown 编辑/原生预览/分栏，文档保存。
- 保存请求与文档带冲突检测；脏标记。

### 示例

- `examples/sample-project/`：可运行的最小项目（项目、环境、鉴权、请求、Markdown）。
- `examples/demo-server/`：本地 HTTP 演示服务（供桌面手动联调）。

## 2. 构建与运行

```bash
# 构建与测试
go build ./...
go test -race ./...
go vet ./...

# 桌面应用（唯一入口）
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project

# 本地演示服务（可选，供桌面手动联调）
go run ./examples/demo-server -addr 127.0.0.1:4010
```

密钥通过桌面本地凭据后端或环境变量 `GOREQUEST_SECRET_<NAME>` 注入；项目文件只保存 `{{secret.name}}` 引用。**不提供产品 CLI。**

## 3. 验证结果

在本机（darwin/arm64，Go 1.27.3）执行并通过：

- `go build ./...`：成功（含 Desktop 原生 UI）。
- `go vet ./...`：无报告。
- `go test -race ./...`：全部通过（core 覆盖率约 56%）。新增 `internal/desktop` 测试覆盖：配置持久化与损坏容错、最近项目去重与上限、路径规范化、打开失败不污染状态、重复打开去重、项目切换保留标签/未保存编辑/环境选择、无效路径点击回退选中、跨重启恢复列表；并用 MyGo `ui.NewTester` 无窗口跑真实视图，验证空态、打开项目、设置面板开关与窄窗口（760×560）布局不崩溃。
- 原生 UI 技术验证：完整桌面程序构建后启动，窗口创建并持续运行（本轮由外部终止），确认窗口与事件循环正常、无 WebView 主界面路线可行。真实窗口中的点击交互（打开两个项目、切换、设置往返等）未做人工操作验证，相应逻辑改由上面的无窗口测试覆盖。
- 请求表单 / 响应展示改造：新增 `internal/desktop/request_form.go`、`response_view.go` 及逻辑测试（`request_form_test.go`、`response_view_test.go`）。覆盖：响应正文不混入响应头元数据、Pretty/Raw 正文分离、重复头保留与 Authorization/Set-Cookie 脱敏、空/非 JSON/非法 JSON 回退、截断提示在正文之外；表单与 `core.KV` 的有序往返（重复键/空 value/含 `:`/`=`/空格/`{{var}}`/`{{secret.x}}`）、禁用行保留、空白行丢弃、漏 Key 报错、建议集合分离与筛选、删除中间行标识稳定。`go build ./...`、`go vet ./...`、`go test -race ./...` 均通过。
- `examples/demo-server` 可作为本地服务供桌面手动联调；core 的 HTTP 行为已由 `httptest` 覆盖。
- 两项界面改造合并后，新增无窗口工作区集成测试，覆盖结构化表单与独立响应视图同时显示、切换项目保留编辑和响应标签、保存后重新打开请求保留重复键与禁用行，以及窄窗口渲染。合并结果通过构建、竞态测试和静态检查；未新增真实窗口人工交互验收。
- 环境管理（新建/编辑当前环境）：新增 `internal/core/environment.go`、`internal/core/environment_test.go`、`internal/desktop/environment_editor.go`、`internal/desktop/environment_editor_test.go`。Core 用真实临时文件覆盖：新建与编辑往返、空值及模板保留、重复 ID 与目标文件已存在拒绝（不覆盖）、真实 `.yml` 路径且文件名≠id、连续保存、外部修改/删除冲突、非法 ID 与非法变量 Key、失败不污染内存，以及 `ResolveVars`/`BuildPlan` 在保存后使用新环境值。Desktop 用 `ui.NewTester` 与状态测试覆盖：无环境时新建可用而 `(none)` 不可编辑、编辑载入实际变量与密钥声明、保存刷新与自动选择、校验失败保留输入、取消/放弃不写盘、项目隔离、既有脏请求标签不丢失。`go build ./...`、`go vet ./...`、`go test -race ./...` 均通过。**限制**：真实窗口中的鼠标交互（新建、编辑、取消、切换后发送）未做人工操作验证（本环境无法人工点击），相应逻辑由上述无窗口测试覆盖，不虚报为人工验收通过。

## 4. 尚未实现（保持设计计划，未宣称完成）

- 断言/JSONPath/提取执行、Collection Runner、跨集合 Suite、数据驱动、JUnit/JSON 报告与固定重试策略（`assert` 字段目前仅解析保留，不执行；桌面内 Runner/测试/报告界面尚未实现）。
- OpenAPI 3.1/3.2 导入导出、Schema 校验、OAuth2/OIDC、TLS/mTLS 高级配置。
- GraphQL / WebSocket / gRPC 协议；Mock；性能分析；Mermaid；代码生成。
- 全局临时工作区、本地历史、Cookie Jar；最近项目列表的删除/固定/搜索与项目内集合分组的可折叠树尚未实现（当前列表按名称平铺，新建请求可能暂不归入既有集合目录的层级展示）。
- 系统钥匙串后端（当前桌面使用环境变量注入）；代理/自定义 CA/重定向策略配置。
- Body 外部文件引用（`body.file`）、form-data/urlencoded/binary 等复杂 Body、响应二进制保存。
- 请求/文档的重命名、复制、移动 UI；Git 状态/Diff/Commit 集成。
- 保存时的注释与原始排版保真（当前按结构体重序列化，注释会丢失）；编辑 `auth` 引用会把 `ref` 写回为内联方案。

上述限制在进入 Beta/1.0 前按 `docs/roadmap.md` 与 `docs/decisions.md` 的待决策项处理，不因本文存在而改变其计划状态。

## 5. 代码结构

```text
cmd/gorequest-desktop/  MyGo 原生 UI 入口（唯一产品入口）
internal/core/          模型、存储、变量、鉴权、执行、校验（纯 Go）
internal/platform/      环境变量密钥等 OS 适配
internal/desktop/       原生 UI 视图与交互状态（仅依赖 core）
examples/sample-project 可运行示例项目
examples/demo-server    本地演示 HTTP 服务
```
