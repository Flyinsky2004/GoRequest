# 实现状态与运行说明

状态日期：2026-11-05。本文记录 **MVP 已实现的可运行代码**，与设计基线文档（`README`、`docs/*.md`）中的未来规划分开。设计文档仍描述完整目标；本文只写实际存在、已通过验证的能力。

## 1. 已实现（MVP）

### 纯 Go Core（`internal/core`，不依赖 GUI）

- 项目定位与加载：查找 `.gorequest/project.yaml`；加载 `collections/`、`environments/`、`auth/`、`docs/`。
- 存储：UTF-8 文本、稳定字符串 ID、`schemaVersion`；严格 YAML 解析（拒绝未知字段与重复键）；有序 Header/Query 数组。
- 安全保存：内容 SHA-256 摘要比对、同目录临时文件 + 原子替换、外部修改冲突保护（`ConflictError`）；拒绝路径穿越与符号链接逃逸。
- 变量解析：项目 → 环境 → 集合 → 请求分层合并；`{{name}}` 递归解析、循环检测、缺失即报错；`{{secret.name}}` 由注入的 `SecretProvider` 提供，明文不落项目。
- 鉴权：None / Bearer / Basic / API Key（header/query/cookie）。
- HTTP 执行：方法、URL、有序 Query/Headers（重复键保留）、JSON/Text/XML Body、Header 注入防护、绝对 URL 校验、超时（请求级或默认 10s）、`context` 取消、TLS 默认验证；响应返回状态码、协议、耗时、Body 大小、Headers、Pretty/Raw、32 MB 截断标记。
- 校验：schema 版本、ID 唯一、字段/引用/超时/body 类型/鉴权检查，返回结构化 `Issue`。

### 独立 CLI（`cmd/gorequest`）

- `version`、`validate`、`run`、`doctor`。
- 退出码：0 成功、1 运行失败、2 配置错误、3 内部错误、130 取消。
- `--project`、`--request`、`--env`、`--timeout`、`--json`、`--secret name=env:VAR`。
- 输出脱敏：Authorization/Set-Cookie 与已知密钥值在打印前替换为 `***`。
- 无 GUI 依赖，可在无桌面环境运行。

### 原生 Desktop（`cmd/gorequest-desktop` + `internal/desktop`）

- MyGo **Native UI**：GPU 绘制、无 HTML/JS/WebView 主界面。
- 打开项目、左侧请求/文档树、多标签、环境切换。
- 请求编辑：方法选择、URL、超时、Params/Headers（逐行编辑）、Body（JSON/Text/XML）、Auth（none/bearer/basic）。
- 发送/取消（后台 goroutine + `Window.Update`），响应区显示状态/耗时/大小、Pretty/Raw、Headers/Body（只读可选中）。
- Markdown 编辑/原生预览/分栏，文档保存。
- 保存请求与文档带冲突检测；脏标记。

### 示例与测试

- `examples/sample-project/`：可运行的最小项目（项目、环境、鉴权、请求、Markdown）。
- `examples/demo-server/`：本地 HTTP 演示服务。
- 单元/集成测试（`internal/core`、`cmd/gorequest`），含 `httptest` 行为验证、超时、取消、冲突、路径穿越、重复 ID、端到端 CLI 运行。

## 2. 构建与运行

```bash
# 构建
go build ./...
go build -o bin/gorequest ./cmd/gorequest
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
go build -o bin/demo-server ./examples/demo-server

# 测试与静态检查
go test -race ./...
go vet ./...

# 桌面（默认打开当前目录，若存在 .gorequest/）
./bin/gorequest-desktop -project examples/sample-project

# CLI 演示：先起本地服务
./bin/demo-server -addr 127.0.0.1:4010 &
./bin/gorequest validate --project examples/sample-project --verbose
./bin/gorequest run --project examples/sample-project --request users.health --env dev
GOREQUEST_SECRET_LOGINPASSWORD=pass123 \
  ./bin/gorequest run --project examples/sample-project --request auth.login --env dev \
  --secret loginPassword=env:GOREQUEST_SECRET_LOGINPASSWORD --json
```

密钥通过环境变量 `GOREQUEST_SECRET_<NAME>` 或 `--secret name=env:VAR` 注入；项目文件只保存 `{{secret.name}}` 引用。

## 3. 验证结果

在本机（darwin/arm64，Go 1.27.3）执行并通过：

- `go build ./...`：成功（含 Desktop 原生 UI 与 CLI）。
- `go vet ./...`：无报告。
- `go test -race ./...`：全部通过（core 覆盖率约 56%）。
- 原生 UI 技术验证：最小 MyGo Native UI 程序可构建并创建窗口（运行至超时仍存活），确认无 WebView 主界面路线可行。
- 端到端：`examples/sample-project` 对 `demo-server` 的 `users.health`、`auth.login`（带密钥）、`users.me`（Bearer）均返回 200；缺失密钥时 CLI 返回退出码 2。

## 4. 尚未实现（保持设计计划，未宣称完成）

- 断言/JSONPath/提取执行、Collection Runner、跨集合 Suite、数据驱动、JUnit/JSON 报告与固定重试策略（CLI 的 `assert` 字段目前仅解析保留，不执行）。
- OpenAPI 3.1/3.2 导入导出、Schema 校验、OAuth2/OIDC、TLS/mTLS 高级配置。
- GraphQL / WebSocket / gRPC 协议；Mock；性能分析；Mermaid；代码生成。
- 全局临时工作区、最近打开、本地历史、Cookie Jar、多项目切换。
- 系统钥匙串后端（当前 Desktop/CLI 均使用环境变量注入）；代理/自定义 CA/重定向策略配置。
- Body 外部文件引用（`body.file`）、form-data/urlencoded/binary/XML 之外的复杂 Body、响应二进制保存。
- 请求/文档的重命名、复制、移动 UI；Git 状态/Diff/Commit 集成。
- 保存时的注释与原始排版保真（当前按结构体重序列化，注释会丢失）；编辑 `auth` 引用会把 `ref` 写回为内联方案。

上述限制在进入 Beta/1.0 前按 `docs/roadmap.md` 与 `docs/decisions.md` 的待决策项处理，不因本文存在而改变其计划状态。

## 5. 代码结构

```text
cmd/gorequest/          独立 CLI
cmd/gorequest-desktop/  MyGo 原生 UI 入口
internal/core/          模型、存储、变量、鉴权、执行、校验（纯 Go）
internal/platform/      环境变量密钥等 OS 适配
internal/desktop/       原生 UI 视图与交互状态（仅依赖 core）
examples/sample-project 可运行示例项目
examples/demo-server    本地演示 HTTP 服务
```
