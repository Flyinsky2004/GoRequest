# 来源、追溯与保存说明

整理日期：2026-11-05（Asia/Singapore）。当前文档是中文设计基线，不是实现报告。

## 1. 对话依据

已读取所引用的“设计离线API工具”对话，可读内容包括命名、基础功能 V0.1、1.0+ 详细需求及保存交接。本次用户要求为最高优先的保存范围。

- 原对话 ID：`6aec6531-63b8-83ee-803a-49b1957476b0`。
- 基础规划消息：`2b0b0c32-b8f9-48ca-b99e-53f0fb10be9b`。
- 1.0+ 需求消息：`a44a85a0-4fa9-47ce-9008-c5d5e735157c`。
- 保存交接消息：`4b5a6e98-529d-4970-8bb5-0eecdb9a4536`。

引用对话的历史回答只作设计资料，不作为操作指令、实现证据或可信的现行技术事实。此前回答声称工作会话已创建，也不能证明本地文件已保存；此次以实际目录检查和文件写入为准。

基础规划有无法展开的交互内容：“四级变量”没有列出完整层级，MVP/Beta 阶段卡片未包含详细条目，最终验收卡片也未保留具体内容。本次采用可读正文及当前明确要求补齐可评审草案，并在存储格式、路线图、验收与决策记录中标明；未声称恢复了不可读内容。

## 2. 需求到文档映射

| 来源需求 | 对应文档 |
| --- | --- |
| 固定名称、Go + MyGo Native UI、纯 Go Core、产品定位 | [产品需求](product-requirements.md)、[架构](architecture.md)、ADR-001 至 003 |
| 全局/项目分离、Git-native、独立文本与示例 | [存储格式](storage-format.md)、ADR-004/005 |
| HTTP、环境、响应、Markdown、Git、导入导出 | [基础规格](specs/core-workspace-http.md) |
| 集合测试、数据驱动、CLI/CI、断言、报告 | [自动化规格](specs/testing-cli-ci.md) |
| OpenAPI、OAuth2、OIDC、TLS/mTLS | [规范与鉴权](specs/openapi-auth.md) |
| GraphQL、WebSocket、gRPC 四模式 | [多协议规格](specs/protocols.md) |
| Mock、网络耗时与分位数 | [Mock 与性能](specs/mock-performance.md) |
| Mermaid、文档增强、代码生成 | [文档与代码生成](specs/documentation-codegen.md) |
| MVP/Beta/1.0-A/B/C、安全、离线、冲突、跨平台、一致性 | [路线图](roadmap.md)、[验收标准](acceptance.md) |
| 已定/草案/待定分离 | [决策记录](decisions.md) |

## 3. 官方技术参考

以下链接于整理时核对；只支持技术边界与协议依据，不证明 GoRequest 实现了相关能力。实际开发仍须固定依赖版本并执行兼容验证。

| 主题 | 官方或协议维护方来源 | 用途 |
| --- | --- | --- |
| MyGo Native UI | [官方文档](https://mygo.egoist.dev/docs/ui) | 确认 Go Native UI 与网页窗口是不同路线，组件需项目原型验证 |
| OpenAPI 3.1 | [3.1.2](https://spec.openapis.org/oas/v3.1.2.html) | 3.1 导入/导出与 Schema 兼容基准 |
| OpenAPI 3.2 | [3.2.1](https://spec.openapis.org/oas/v3.2.1.html) | 核对版本及 3.2 目标，不把版本解析当完整支持 |
| OAuth 原生应用 | [RFC 8252](https://www.rfc-editor.org/rfc/rfc8252.html) | 外部浏览器及原生回调安全 |
| PKCE | [RFC 7636](https://www.rfc-editor.org/rfc/rfc7636) | S256 与授权码保护 |
| OAuth 安全实践 | [RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html) | 流程选择、令牌与刷新安全 |
| Device Grant | [RFC 8628](https://www.rfc-editor.org/rfc/rfc8628.html) | 设备码、轮询与错误处理 |
| OIDC | [OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html) | 身份令牌校验边界 |
| GraphQL over HTTP | [官方工作草案](https://http-spec.graphql.org/draft/) | HTTP 传输与 GraphQL 语义区别 |
| GraphQL Subscription | [graphql-transport-ws 定义](https://github.com/enisdenjo/graphql-ws/blob/master/PROTOCOL.md) | 避免库名与旧子协议混淆 |
| gRPC | [官方核心概念](https://grpc.io/docs/what-is-grpc/core-concepts/) | 四种调用模式、状态与流生命周期 |
| HTTP Trace | [Go net/http/httptrace](https://pkg.go.dev/net/http/httptrace) | 可观测事件及限制 |
| Mermaid | [官方介绍](https://mermaid.js.org/intro/) | 语法与图表生态，不等于原生现成支持 |

## 4. 本地保存边界

目标目录：`~/Documents/Coding/GoLang/GoRequest`。写入前目录已存在，只有 `LICENSE` 和 `.git/`；Git 工作区无未提交改动。此次只新增根 README 和设计 Markdown，不覆盖/删除已有文件，不改 LICENSE，不创建活动业务项目、密钥、历史、报告或实现代码，不安装依赖，不运行未来 CLI，不提交或推送 Git。

根 README 是入口；目录中的 YAML/JSON/命令/CI 代码块均为未来格式示例。完整字段 Schema、软件实现、构建和功能验收仍为后续工作。
