# GoRequest

本地优先、Git 原生的 API 开发与文档管理工作台。

**当前状态：MVP 已实现（可运行）。** 本仓库同时保存需求设计与 MVP 代码。核心、独立 CLI 与 MyGo 原生 Desktop 已可构建运行；完整目标的其余部分仍为计划。设计基线日期：2026-11-05（Asia/Singapore）。已实现范围、运行方式与验证结果见 [实现状态](docs/implementation-status.md)。

```bash
# 构建、测试与运行（详见实现状态文档）
go build ./...
go test -race ./...
go build -o bin/gorequest ./cmd/gorequest
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project
```

## 已确定的方向

- 产品名称固定为 **GoRequest**，独立命令行程序名称为 `gorequest`。
- 使用 **Go + MyGo Native UI**，主界面不采用 React、HTML/JavaScript 页面或 WebView。
- Desktop 与独立 CLI/CI 共享纯 Go **GoRequest Core**、同一份项目文件、同一套执行语义。
- Local-first：编辑、文档、项目校验、本地 Mock 和已安装的生成工具不依赖账号或云端服务。
- Git-native：每个请求独立成文件，项目资料保存在 `.gorequest/`，可以与业务代码共用仓库。
- 全局个人工作区与项目数据分离。密钥、本地历史、设备配置、运行缓存和报告不入 Git。

目标工作流：定义 API → 编写文档 → 调试请求 → 编写测试 → 本地 Mock → 批量运行 → CLI/CI 验证 → 导出规范与代码。

## 文档导航

| 文档 | 内容 |
| --- | --- |
| [实现状态](docs/implementation-status.md) | **MVP 已实现能力、构建/运行、验证结果与未实现项** |
| [产品需求](docs/product-requirements.md) | 定位、用户场景、基础能力、1.0+ 范围和优先级 |
| [技术架构](docs/architecture.md) | Native UI、纯 Go Core、模块边界与执行生命周期 |
| [存储格式](docs/storage-format.md) | 全局/项目分离、目录树、版本、请求与测试套件 YAML 示例 |
| [基础功能规格](docs/specs/core-workspace-http.md) | 工作区、HTTP、响应、变量、Markdown、Git、导入导出 |
| [自动化、CLI 与 CI](docs/specs/testing-cli-ci.md) | 集合测试、数据驱动、断言、提取、报告、命令及退出码 |
| [OpenAPI 与高级鉴权](docs/specs/openapi-auth.md) | 3.1/3.2、Schema、OAuth2、OIDC、TLS/mTLS |
| [多协议规格](docs/specs/protocols.md) | GraphQL、WebSocket、gRPC 四模式及流式测试 |
| [Mock 与性能分析](docs/specs/mock-performance.md) | 规则、生命周期、HTTP 阶段耗时、分位数 |
| [文档增强与代码生成](docs/specs/documentation-codegen.md) | Mermaid、接口引用、离线导出、Snippet 与 SDK |
| [开发路线图](docs/roadmap.md) | MVP → Beta → 1.0-A → 1.0-B → 1.0-C |
| [安全、质量与验收](docs/acceptance.md) | 安全、离线、冲突、跨平台、GUI/CLI 一致性及验收场景 |
| [设计决策记录](docs/decisions.md) | 已确定方向、设计草案、待决策问题及验证门槛 |
| [来源与追溯](docs/sources.md) | 原对话依据、官方规范、整理边界 |

## 设计状态的含义

**已确定**：本次明确要求和原对话中持续沿用的产品、架构与安全约束。

**设计草案**：为使需求可实施而细化的字段、命令、算法、接口与默认值，需要在实现前评审，不视为已批准的稳定格式。

**待决策**：尚无充分依据选择的实现方案，集中记录于决策文档。选择方案时不得违背已确定方向。

## 开发顺序

1. MVP：验证 MyGo 原生界面与文件安全，完成 HTTP、项目文件和 Markdown 的最小闭环。
2. Beta：补齐工作区体验、变量/密钥、Git UI、导入导出和基础断言。
3. 1.0-A：集合测试、独立 CLI/CI、OpenAPI 3.1/3.2、OAuth2/OIDC。
4. 1.0-B：GraphQL、WebSocket、原生 gRPC 与对应自动化。
5. 1.0-C：Mock、性能诊断、Mermaid、文档增强及代码生成。

这些是能力里程碑，不是发布承诺或完成情况。仓库已有的 [LICENSE](LICENSE) 保持原样；此次整理不新增业务实现、构建脚手架或真实凭据，也不执行 Git 提交和推送。
