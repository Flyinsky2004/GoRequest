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
- 打开项目、左侧请求/文档树、多标签、环境切换。
- 请求编辑：方法选择、URL、超时、Params/Headers、Body（JSON/Text/XML）、Auth（none/bearer/basic）。
  - Params/Headers 为结构化逐行 Key/Value 表格：Enabled 勾选、可编辑 Key、Value、删除，固定列标题、独立滚动与“Add row”。每行有稳定 UI 标识，删除中间行不串行。Key 输入框为可编辑的自动补全框（MyGo 原生 `Autocomplete`）：可按输入过滤、上下键选择、Enter 确认、Escape 关闭，且允许任意自定义输入；Headers 与 Query 使用互不混用的建议集合，Content-Type/Accept 的 Value 提供媒体类型建议（仍可自由输入）。
  - 结构化行状态直接映射 `core.KV`：有序往返保留重复键、空 value、含 `:`/`=`/有效空格的值以及 `{{variable}}`/`{{secret.name}}`；已保存的禁用行打开后仍可见并保持禁用；纯空白行在保存/发送时被丢弃，填了 value 却漏 key 的不完整行就近提示并在保存/发送时报错。Query value 不按 `&`/`=` 切分，Header value 不按 `:` 切分。
- 发送/取消（后台 goroutine + `Window.Update`），响应区显示状态码/协议、耗时、大小、发送中/错误/取消状态。
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
- `go test -race ./...`：全部通过（core 覆盖率约 56%）。
- 原生 UI 技术验证：最小 MyGo Native UI 程序与完整桌面程序均可构建并创建窗口（运行至超时仍存活），确认无 WebView 主界面路线可行。
- 请求表单 / 响应展示改造：新增 `internal/desktop/request_form.go`、`response_view.go` 及逻辑测试（`request_form_test.go`、`response_view_test.go`）。覆盖：响应正文不混入响应头元数据、Pretty/Raw 正文分离、重复头保留与 Authorization/Set-Cookie 脱敏、空/非 JSON/非法 JSON 回退、截断提示在正文之外；表单与 `core.KV` 的有序往返（重复键/空 value/含 `:`/`=`/空格/`{{var}}`/`{{secret.x}}`）、禁用行保留、空白行丢弃、漏 Key 报错、建议集合分离与筛选、删除中间行标识稳定。`go build ./...`、`go vet ./...`、`go test -race ./...` 均通过。
- `examples/demo-server` 可作为本地服务供桌面手动联调；core 的 HTTP 行为已由 `httptest` 覆盖。

## 4. 尚未实现（保持设计计划，未宣称完成）

- 断言/JSONPath/提取执行、Collection Runner、跨集合 Suite、数据驱动、JUnit/JSON 报告与固定重试策略（`assert` 字段目前仅解析保留，不执行；桌面内 Runner/测试/报告界面尚未实现）。
- OpenAPI 3.1/3.2 导入导出、Schema 校验、OAuth2/OIDC、TLS/mTLS 高级配置。
- GraphQL / WebSocket / gRPC 协议；Mock；性能分析；Mermaid；代码生成。
- 全局临时工作区、最近打开、本地历史、Cookie Jar、多项目切换。
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
