# 产品需求

状态：设计基线；功能均待实现。日期：2026-11-05。字段及技术细节的状态见[决策记录](decisions.md)。

## 1. 产品定位与目标

GoRequest 面向个人开发者和通过 Git 协作的研发团队，将 API 定义、请求调试、文档、测试和交付放在同一个本地项目中。Desktop 使用 Go + MyGo Native UI；无图形环境的 CI 使用独立 CLI；二者共享纯 Go Core。

产品应当同时满足：不开应用也能读改项目文件；不登录账号也能编辑、调试本地服务和管理文档；没有 Git 也能工作；使用 Git 时能审阅真实语义差异。项目可以独立存在，也可以放进已有业务代码仓库。

## 2. 核心用户场景

| 场景 | 预期结果 |
| --- | --- |
| 临时调试 | 在全局工作区新建请求，配置参数并查看响应，再选择是否保存进项目 |
| 团队 API 项目 | 打开业务仓库中的 `.gorequest/`，共同维护请求、Schema、文档与测试 |
| 登录与业务回归 | 登录提取 Token，查询用户、更新资料、校验结果，运行状态不污染共享文件 |
| CI 门禁 | 在无桌面 Linux Runner 中校验、执行测试、输出 JUnit 与明确退出码 |
| 多协议调试 | 用同一环境和鉴权管理 HTTP、GraphQL、WebSocket、gRPC |
| 后端未就绪 | 从接口示例/Schema 启动本地 Mock，供前端或测试使用 |
| 交付接口 | 输出 OpenAPI、可离线阅读的文档和不包含密钥的调用代码 |

## 3. 基础能力清单

| 模块 | 需求范围 | 主要阶段 |
| --- | --- | --- |
| 工作区 | 全局临时请求、多项目、最近打开、目录树、多标签、搜索、复制移动；独立窗口可选 | MVP/Beta |
| HTTP | 常用及自定义方法，URL/Query/Headers，JSON/XML/Text/Form/Binary，Cookie，发送/取消/重试 | MVP/Beta |
| 网络设置 | 超时、重定向、HTTP/SOCKS5 代理、TLS、自定义 CA、mTLS 的分阶段支持 | MVP 至 1.0-A |
| 响应 | 状态、耗时、大小、Pretty/Raw、JSON 树、Headers/Cookies、搜索与二进制保存 | MVP/Beta |
| 环境与鉴权 | 环境切换、变量来源、继承、密钥引用，None/Bearer/Basic/API Key | MVP/Beta |
| Markdown | 源码、原生预览、分栏、GFM、大纲、本地图片、链接、请求关联 | MVP/Beta |
| Git | 独立文本文件、外部刷新、冲突保护；状态/Diff/Commit/Pull/Push 分阶段加入 | MVP/Beta |
| 导入导出 | cURL、Postman Collection/Environment、OpenAPI 3.x、Swagger 2.0 迁移；请求/文件夹/项目便携导出 | Beta 至 1.0-A |
| 应用设置 | 系统深浅色、字体字号、编辑器、自动保存、快捷键、语言、网络设置 | MVP/Beta |

基础功能详见[基础规格](specs/core-workspace-http.md)。

## 4. 1.0+ 需求与优先级

原对话称为“10 项功能”，实际分列了 11 个规格单元；这里全部保留，不因标题计数删减范围。P0/P1/P2 是进入 1.0+ 后的相对优先级。

| 编号 | 能力 | 优先级 | 必须覆盖的范围 |
| --- | --- | --- | --- |
| ADV-01 | 集合测试与自动化 | P0 | Collection Runner、跨集合 Suite、依赖、CSV/JSON 数据驱动、断言、提取、重试、报告 |
| ADV-02 | 独立 CLI 与 CI | P0 | run/validate/openapi/mock/generate/doctor/version、无交互、JSON/JUnit、取消与退出码 |
| ADV-03 | OpenAPI | P0 | 3.1.x/3.2.x YAML/JSON 导入导出、引用与结构校验、响应 Schema、差异与兼容性报告 |
| ADV-04 | OAuth2/OIDC | P0 | Code + PKCE、Client Credentials、Device、刷新、Discovery、ID Token 校验 |
| ADV-05 | GraphQL | P1 | Query、Mutation、Subscription、SDL/Introspection、变量、操作选择、错误与自动化 |
| ADV-06 | WebSocket | P1 | 会话调试、Text/JSON/Binary、Ping/Pong、关闭、消息脚本与重放 |
| ADV-07 | gRPC | P1 | Unary、Server/Client/Bidirectional Streaming、Reflection/Proto、Metadata/Deadline/TLS |
| ADV-08 | Mock Server | P1 | 确定性匹配、响应场景、模板、延迟、CLI、套件生命周期 |
| ADV-09 | 网络性能分析 | P1 | HTTP 阶段、重定向/复用/代理、多次采样、P50/P90/P95/P99、报告 |
| ADV-10 | Mermaid 与文档增强 | P1 | 离线图表、接口引用、参数表/响应示例、链接检测、文档导出 |
| ADV-11 | 代码生成 | P2 | cURL、Go、Python、JS/TS Snippet；规范驱动 SDK、模板版本及重复生成 |

## 5. 不变约束与能力边界

- UI 只负责交互和展示，不复制执行引擎、断言或变量解析逻辑。
- 项目事实来源是独立文本文件，不是云数据库或隐藏的数据库快照。
- 全局历史、凭据、Cookie、设备代理、最近打开和运行缓存均为个人数据。
- 打开项目只读取资料，不自动发请求、执行脚本、启动 Mock、获取远程 Schema 或启用定时任务。
- Mermaid 离线可读是需求，完整 Mermaid 兼容性和渲染器是待评估实现，不以 WebView 绕过技术方向。
- GraphQL/gRPC/WebSocket 保留本协议资料，不强行转成 REST OpenAPI。AsyncAPI、gRPC-Web、有状态 Mock、完整压测和任意脚本属于后续评估。
- Local-first 不意味着所有外部 API 都能离线调用；主动请求、OAuth、Git 远程同步等操作仍需对应网络。

## 6. 完成的判断方式

用同一个示例业务项目覆盖定义、文档、调试、测试、Mock、CLI/CI、规范与代码导出，而非仅检查界面按钮。每项能力必须有成功、失败、取消、离线或协议边界的可复现验收证据。具体门槛见[验收标准](acceptance.md)和[路线图](roadmap.md)。
