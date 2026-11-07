<p align="center">
  <img src="assets/brand/gorequest-512.png" width="120" height="120" alt="GoRequest" />
</p>

<h1 align="center">GoRequest</h1>

<p align="center">
  <strong>本地优先、Git 原生的 API 开发与文档工作台</strong>
</p>

<p align="center">
  在原生桌面应用里调试 HTTP、管理环境与密钥、编写接口文档、运行自动化测试和本地 Mock。<br />
  请求、环境与文档都以纯文本保存在你的 Git 仓库中。
</p>

<p align="center">
  <a href="https://github.com/Flyinsky2004/GoRequest"><img src="https://img.shields.io/github/stars/Flyinsky2004/GoRequest?style=flat-square&logo=github" alt="GitHub stars" /></a>
  <img src="https://img.shields.io/badge/Go-1.27.3-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.27.3" />
  <img src="https://img.shields.io/badge/Platform-macOS%20%7C%20Windows-lightgrey?style=flat-square" alt="macOS and Windows" />
  <img src="https://img.shields.io/badge/UI-MyGo%20Native-6C5CE7?style=flat-square" alt="MyGo Native UI" />
  <img src="https://img.shields.io/badge/License-AGPL--3.0-blue?style=flat-square" alt="AGPL-3.0" />
</p>

---

## 为什么选择 GoRequest

- **本地优先**：无需登录账号，编辑、调试、文档和 Mock 都在本机完成。
- **Git 原生**：每个请求是独立 YAML 文件，项目资料放在 `.gorequest/`，可以随业务代码一起审阅、提交和合并。
- **原生桌面**：Go + MyGo Native UI 直接绘制界面，不使用 WebView；唯一产品入口是桌面应用，不提供独立 CLI。
- **密钥不落库**：项目文件只保存 `{{secret.name}}` 引用，真实值来自系统凭据或环境变量。
- **多项目并行**：每个项目独立窗口，标签、未保存编辑、环境和响应互不干扰。

## 核心功能

### 请求调试

- 常用与自定义 HTTP 方法，有序 Query / Headers（保留重复键）
- JSON、Text、XML、form-urlencoded、multipart、Binary 与项目内文件
- 变量分层解析、URL 预览、发送与取消、请求级超时
- Params / Headers 结构化编辑与自动补全

### 环境、变量与鉴权

- 项目 → 环境 → 集合 → 请求的分层变量
- None / Bearer / Basic / API Key，以及 OAuth 2.0 与 OIDC
- Secret 只保存引用，真实值由系统凭据或 `GOREQUEST_SECRET_<NAME>` 注入
- 生产环境变更的显式确认

### 网络与响应

- HTTP / SOCKS5 代理、重定向策略、阶段超时
- 自定义 CA、mTLS、隔离的 Cookie Jar
- Pretty / Raw / JSON 树，Headers、Cookie 与网络阶段信息
- 二进制响应检视与保存

### 接口文档

- Markdown 源码、原生预览与分栏
- 原生画布编辑、GFM 表格、`@` 接口引用
- 大纲、项目内全文搜索、本地图片与附件
- 离线 Markdown / HTML 导出

### 测试与 Mock

- 集合 / Suite Runner，断言与提取
- 运行进度、取消，JSON / JUnit 报告导出
- 本地 Mock 规则、场景与访问记录
- 性能诊断：阶段耗时、分位数与 JSON / CSV 导出

### 互操作

- OpenAPI 3.1 / 3.2 导入导出与 JSON Schema 校验
- cURL、Postman Collection / Environment 导入导出
- 原生便携包
- cURL / Go / Python / JavaScript / TypeScript Snippet

### Git 与本地历史

- 状态、Diff、选择性提交、Pull / Push
- 外部修改自动刷新与冲突保护
- 脱敏的本地请求历史与恢复

### 桌面体验

- 多项目窗口、标签页与关闭保护
- 简体中文 / English，浅色 / 深色主题
- 自动保存、快捷键与可配置编辑器
- WebSocket 手工会话

> **开发状态**：GoRequest 仍在持续开发中。以上是当前代码中已具备的能力；完整范围与未完成项见 [实现状态](docs/implementation-status.md) 与 [开发路线图](docs/roadmap.md)。

## 下载

安装包通过 GitHub [Releases](https://github.com/Flyinsky2004/GoRequest/releases) 分发。发布工作流为以下目标构建产物：

| 平台 | 架构 | 产物 |
| --- | --- | --- |
| macOS 12+ | Apple Silicon / Intel | 含 `GoRequest.app` 的 `.dmg` |
| Windows | x64 / ARM64 | `.exe` 安装器、`.zip` 便携版 |

macOS 使用 ad-hoc 签名，Windows 未配置代码签名，首次打开时系统可能提示来源验证。实际可用版本与附件以 Releases 页面为准；如果尚无可用附件，可以按下面的方式从源码运行。目前不提供 Linux 发行包。

## 从源码体验

需要 **Go 1.27.3 或更高版本**。

```bash
git clone https://github.com/Flyinsky2004/GoRequest.git
cd GoRequest

# 直接运行桌面应用并打开示例项目
go run ./cmd/gorequest-desktop -project examples/sample-project
```

可选：启动本地演示服务，配合桌面应用手动联调。

```bash
go run ./examples/demo-server -addr 127.0.0.1:4010
```

构建独立二进制：

```bash
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project
```

## 反馈与贡献

- 问题与建议：提交 [GitHub Issue](https://github.com/Flyinsky2004/GoRequest/issues)
- 代码与文档改进：欢迎 Pull Request；开始前请阅读 [AGENTS.md](AGENTS.md) 与 [实现状态](docs/implementation-status.md)
- 请勿在 Issue、PR 或示例项目中粘贴真实密钥或凭据

## 许可证

GoRequest 以 [GNU Affero General Public License v3.0](LICENSE)（AGPL-3.0）发布。

## 开发文档

| 文档 | 内容 |
| --- | --- |
| [实现状态](docs/implementation-status.md) | 当前可用能力、构建运行与验证边界 |
| [产品需求](docs/product-requirements.md) | 产品定位、用户场景与能力范围 |
| [技术架构](docs/architecture.md) | Native UI、纯 Go Core 与模块边界 |
| [存储格式](docs/storage-format.md) | 项目目录、YAML 结构与版本 |
| [开发路线图](docs/roadmap.md) | 从 MVP 到后续里程碑 |
| [设计决策](docs/decisions.md) | 已确定方向与关键取舍 |
