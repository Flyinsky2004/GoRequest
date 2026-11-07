# GoRequest

<img src="assets/brand/gorequest-512.png" alt="GoRequest logo" width="96" height="96" align="left" style="margin-right: 12px" />

本地优先、Git 原生的 API 开发与文档管理工作台。

**当前状态：MVP 已实现（可运行）。** 本仓库同时保存需求设计与 MVP 代码。纯 Go Core 与 MyGo 原生 Desktop 已可构建运行；完整目标的其余部分仍为计划。GoRequest 为单一桌面应用，**不提供独立产品 CLI**（2026-11-05 决策，见 [ADR-009](docs/decisions.md)）。设计基线日期：2026-11-05（Asia/Singapore）。已实现范围、运行方式与验证结果见 [实现状态](docs/implementation-status.md)。

```bash
# 构建、测试与运行（详见实现状态文档）
go build ./...
go test -race ./...
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project
```

**启动行为（IDE 式）**：显式 `-project` 路径优先；否则在开启“启动恢复上次项目”时直接打开最后活动的项目；没有可恢复目标或恢复失败才显示轻量的项目选择窗口（约 720×480）。每个项目在自己的工作窗口中打开（默认 1200×820），重复打开同一规范化路径会聚焦已有窗口而非新建编辑状态。

## Logo 与应用图标

[assets/brand/gorequest-logo.png](assets/brand/gorequest-logo.png) 是用户提供的官方 Logo（1600×1600、带 alpha 的 PNG）。代码与设计均不重绘、不调色、不裁剪；只做确定性的尺寸/格式转换：

```bash
# 由原图生成 macOS .icns 与可预览的 512px PNG（仅用系统自带 sips/iconutil）
sh scripts/make-macos-icon.sh

# 生成最小、未签名的开发用 GoRequest.app，把 .icns 接进 Info.plist 的
# CFBundleIconFile / Contents/Resources（不是发布安装包）
sh scripts/make-macos-app.sh
```

运行时图标由 `internal/brand` 内嵌的同一 PNG 提供：`Manager.WireApp` 在就绪后调用 `mygo.App.Dock.SetIcon`（macOS Dock、Windows/Linux 应用图标），每个真实窗口在 `newRealWindow` 里调用 `*mygo.Window.SetIcon`（Linux/Windows 标题栏与任务栏；macOS 窗口自身无图标）。欢迎/项目选择窗口在现有文字旁显示同一 Logo 的小图，既有文字、操作、标题安全区与功能 SVG 图标不变。

直接 `go build` 出的裸二进制由上述运行时 API 设置图标；开发用 `.app` 可通过 `make-macos-app.sh` 生成，发布包则由下述 MyGo 构建流程生成。真实 Dock/Finder 显示、小尺寸观感与各平台人工验收仍需实机核验。

## 自动发布构建

[Release workflow](.github/workflows/release.yml) 在 GitHub Release **发布**时触发，正式版和预发布版均支持；仅保存草稿或推送 tag 不触发。先将本流程合并到待发布版本，再创建对应 tag / Release。tag 使用 `v1.2.3`、`1.2.3` 或 `v1.2.3-beta.1` 等版本格式，应用版本自动取自 tag。

| 平台 | 架构 | Release 附件 |
| --- | --- | --- |
| Windows | x64 / amd64 | 安装器 `.exe`、便携版 `.zip` |
| Windows | ARM64 | 安装器 `.exe`、便携版 `.zip` |
| macOS | Intel / amd64 | 含 `GoRequest.app` 的 `.dmg` |
| macOS | Apple Silicon / ARM64 | 含 `GoRequest.app` 的 `.dmg` |

四个目标均成功后，产物及 `SHA256SUMS` 自动上传到对应 Release。使用仓库自带的 `GITHUB_TOKEN`，无需添加发布凭据。Actions → Release → Run workflow 可输入已有 Release 的 tag 重新构建并替换同名附件；构建始终检出该 tag 的源码。修改发布配置的 PR 也会构建四个目标验证打包，但不会上传到 Release。

Go 版本跟随 `go.mod`，MyGo CLI 版本与应用的 MyGo 依赖一致；[mygo.json](mygo.json) 指定原生应用入口、名称、图标和 bundle identifier。macOS 使用 ad-hoc 签名，尚未配置 Apple Developer ID 和公证；Windows 产物也未配置代码签名，系统可能提示来源验证或阻止直接打开。

## 已确定的方向

- 产品名称固定为 **GoRequest**，以 **MyGo 原生桌面应用**交付，不提供独立产品 CLI。
- 使用 **Go + MyGo Native UI**，主界面不采用 React、HTML/JavaScript 页面或 WebView。
- 桌面前端与纯 Go **GoRequest Core** 分层：Core 不依赖 GUI，请求/变量/鉴权/执行语义集中在 Core。
- Local-first：编辑、文档、项目校验、本地 Mock 和已安装的生成工具不依赖账号或云端服务。
- Git-native：每个请求独立成文件，项目资料保存在 `.gorequest/`，可以与业务代码共用仓库。
- 全局个人工作区与项目数据分离。密钥、本地历史、设备配置、运行缓存和报告不入 Git。

目标工作流：定义 API → 编写文档 → 调试请求 → 编写测试 → 本地 Mock → 桌面内批量运行与验证 → 导出规范与代码。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [实现状态](docs/implementation-status.md) | **MVP 已实现能力、构建/运行、验证结果与未实现项** |
| [正式版差距与并行任务账本](docs/parallel-work-plan.md) | 代码差距、CC/worktree 分工、依赖、验收与完成勾选 |
| [产品需求](docs/product-requirements.md) | 定位、用户场景、基础能力、1.0+ 范围和优先级 |
| [技术架构](docs/architecture.md) | Native UI、纯 Go Core、模块边界与执行生命周期 |
| [存储格式](docs/storage-format.md) | 全局/项目分离、目录树、版本、请求与测试套件 YAML 示例 |
| [基础功能规格](docs/specs/core-workspace-http.md) | 工作区、HTTP、响应、变量、Markdown、Git、导入导出 |
| [桌面测试自动化](docs/specs/desktop-test-automation.md) | 集合 Runner、数据驱动、断言、提取、结果与报告 |
| [OpenAPI 与高级鉴权](docs/specs/openapi-auth.md) | 3.1/3.2、Schema、OAuth2、OIDC、TLS/mTLS |
| [多协议规格](docs/specs/protocols.md) | GraphQL、WebSocket、gRPC 四模式及流式测试 |
| [Mock 与性能分析](docs/specs/mock-performance.md) | 规则、生命周期、HTTP 阶段耗时、分位数 |
| [文档增强与代码生成](docs/specs/documentation-codegen.md) | Mermaid、接口引用、离线导出、Snippet 与 SDK |
| [开发路线图](docs/roadmap.md) | MVP → Beta → 1.0-A → 1.0-B → 1.0-C |
| [安全、质量与验收](docs/acceptance.md) | 安全、离线、冲突、跨平台、Core/GUI 分层及验收场景 |
| [设计决策记录](docs/decisions.md) | 已确定方向、设计草案、待决策问题及验证门槛 |
| [来源与追溯](docs/sources.md) | 原对话依据、官方规范、整理边界 |

## 设计状态的含义

**已确定**：本次明确要求和原对话中持续沿用的产品、架构与安全约束。

**设计草案**：为使需求可实施而细化的字段、命令、算法、接口与默认值，需要在实现前评审，不视为已批准的稳定格式。

**待决策**：尚无充分依据选择的实现方案，集中记录于决策文档。选择方案时不得违背已确定方向。

## 开发顺序

1. MVP：验证 MyGo 原生界面与文件安全，完成 HTTP、项目文件和 Markdown 的最小闭环。
2. Beta：补齐工作区体验、变量/密钥、Git UI、导入导出和基础断言。
3. 1.0-A：桌面内集合测试与报告、OpenAPI 3.1/3.2、OAuth2/OIDC。
4. 1.0-B：GraphQL、WebSocket、原生 gRPC 与对应自动化。
5. 1.0-C：Mock、性能诊断、Mermaid、文档增强及代码生成。

这些是能力里程碑，不是发布承诺或完成情况。仓库已有的 [LICENSE](LICENSE) 保持原样；不新增真实凭据，也不执行 Git 推送。
