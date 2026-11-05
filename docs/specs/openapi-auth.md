# OpenAPI、Schema 与高级鉴权

状态：1.0-A 需求基线；具体依赖库、字段和兼容矩阵待实现前验证。

## 1. OpenAPI 版本策略

必须覆盖 OpenAPI **3.1.x 与 3.2.x**，显式选择 YAML/JSON 和目标版本。先完成稳定的 3.1 支持，再以单独测试矩阵完成 3.2；不以“能够解析版本号”代替完整兼容。

2026-11-05 核对的官方最新规范为 **3.2.1（2026-09-10）**；设计以 3.1.2 和 3.2.1 作为版本样例，实际支持的补丁版本在发布时列明。[OpenAPI 3.1.2](https://spec.openapis.org/oas/v3.1.2.html)、[OpenAPI 3.2.1](https://spec.openapis.org/oas/v3.2.1.html)

导出范围包括路径/方法、path/query/header/cookie 参数、请求媒体类型与 Body、响应状态/Headers/Schema/示例、tags、operationId、servers、安全方案及文档描述。请求编辑模型必须能补充操作级元数据和多种响应定义；不能从一次调试响应推断完整契约。

支持 `$ref`、本地多文件引用、JSON Schema 的组合与约束，例如 `oneOf/anyOf/allOf`。3.2 新增语义需逐项列出已支持、仅保留、不可降级项；从 3.2 导出 3.1 时不静默丢弃不兼容字段。

## 2. 事实来源与导入同步

两种模式显式选择：

1. **原生模式**：GoRequest 请求及关联 Schema 为结构化事实来源，Markdown 提供叙述，OpenAPI 为派生导出物。
2. **外部规范模式**：导入的 OpenAPI 为契约来源；记录来源路径、内容摘要、operationId 与原生请求映射。上游更新先做三方差异预览；本地可保留执行参数，不能无条件覆盖两边修改。

禁止同时宣称两份文件都可自由编辑且会无损自动双向同步。转换产生的 ID 需稳定；冲突、重复 operationId、缺失响应定义及不支持字段必须给出位置和处理选项。

HTTP API 可导出；GraphQL 保留 SDL、gRPC 保留 Proto、普通 WebSocket 保留会话描述，不生成虚假 REST Operation。AsyncAPI 为后续评估。

## 3. 校验、响应契约与覆盖率

- 定义校验：版本、结构、路径模板参数、重复操作、必需字段、安全引用和 `$ref` 解析；离线默认仅访问项目内文件。
- 运行校验：按 Operation、状态与媒体类型选择 Schema，校验实际响应并定位实例路径，例如 `$.data.id` 的实际 string 与期望 integer。
- 兼容性报告：列出字段位置、目标版本、无法映射的原因和严重级别；严格导出在有损项时失败，用户显式允许后才能产生附带报告的降级文件。
- 文档同步：结构化描述、参数表与示例从请求/Schema 派生；Markdown 通过显式关联提供业务说明，不从任意叙述猜测契约。
- 覆盖率：按“可导出的 HTTP Operation”作为分母，分别统计描述、响应 Schema、示例、测试关联，显示不适用项，不能把文档齐全当成测试已通过。
- 导出先预览和校验，检查目标冲突；稳定排序并清除 Secret。导出后再导入按关键语义比较，而非只比较文本字节。

远程 `$ref` 默认不抓取；需要用户主动获取、限制来源和大小，固定内容摘要并缓存。离线缺少资源时给出依赖清单，不临时退回云端校验。

## 4. 鉴权对象与基本规则

鉴权为独立可复用对象，支持项目 → 文件夹 → 请求继承与显式清除。可复用非敏感配置保存项目，密钥、令牌、客户端私钥和刷新状态保存本机受保护存储。认证请求、Token 响应、错误和日志采用同样脱敏规则。

| 方式 | 必须覆盖的行为 |
| --- | --- |
| None / Bearer / Basic | 显式清除继承、Secret 引用、正确 Header 编码 |
| API Key | Header、Query 或 Cookie；导出和日志不展开真实值 |
| Authorization Code + PKCE | 系统浏览器、S256、state、Loopback 回调、超时/取消 |
| Client Credentials | Token Endpoint、客户端认证、scope、过期和并发刷新 |
| Device Authorization | 交互终端展示 verification URI/user code，遵守轮询间隔、slow_down、过期和拒绝 |
| Refresh Token | 过期前刷新、轮换原子更新、失败重新认证、主动清除与可用时撤销 |
| OIDC | Discovery、JWKS、ID Token 签名与 claims 校验、会话标识 |
| TLS / mTLS | 系统 CA、自定义 CA、客户端证书/私钥、SNI/证书错误诊断 |
| 自定义鉴权 | Header 模板先行；签名算法及扩展机制单独评审 |

## 5. OAuth2 安全与交互

Desktop 采用系统浏览器完成 Code + PKCE，不使用内嵌 WebView 收集登录凭据；实现 S256、单次使用的 state、严格回调匹配、临时回环监听及授权结束清理。原生应用不依赖内置“客户端密钥”保密；流程依据原生应用及 OAuth 安全实践。[RFC 8252](https://www.rfc-editor.org/rfc/rfc8252.html)、[RFC 7636](https://www.rfc-editor.org/rfc/rfc7636)、[RFC 9700](https://www.rfc-editor.org/rfc/rfc9700.html)

Token 缓存按本地项目、issuer、client、scope、身份等隔离；刷新并发合并，避免多个请求同时消耗轮换 Refresh Token。只有服务端签发了刷新令牌才尝试刷新；不假定 Client Credentials 一定返回 Refresh Token。主动撤销时区分删除本地值与服务端撤销是否成功。

非交互式自动化只允许 Client Credentials 或受保护 Secret 注入，缺认证时报配置错误；交互式桌面可选择 Device Flow，非交互不得轮询等待用户。Device 按规范处理频率与到期。[RFC 8628](https://www.rfc-editor.org/rfc/rfc8628.html)

Implicit 与 Resource Owner Password Credentials 不列为实现承诺或默认兼容选项；遗留需求如提出需独立评审，不能改变安全基线。生产主机/权限与危险操作策略在 Token Endpoint、Discovery 和业务请求上都有明确边界。

## 6. OIDC 与 TLS 细节

OIDC Discovery 的 issuer 必须匹配期望值；JWKS 轮换有受限缓存；验证 ID Token 的签名、算法允许列表、issuer、audience、过期及流程要求的 nonce/相关 claims。展示未经验证的解码值必须标记状态；ID Token 不能默认作为 API Access Token。[OpenID Connect Core](https://openid.net/specs/openid-connect-core-1_0.html)

TLS 默认验证证书与主机名，自定义 CA 与 mTLS 通过本机映射或凭据引用配置。共享文件可保留证书配置名称，不包含私钥或机器绝对路径。跨主机重定向不泄漏凭据；私钥加载失败、证书过期、代理握手失败分类可诊断且不输出秘密材料。

## 7. 验收

- 包含路径参数、JSON Body、多状态响应、Schema、安全方案和关联说明的 HTTP 项目分别导出有效 3.1/3.2 YAML/JSON；关键语义往返一致，降级损失可见。
- 错误字段类型返回实例路径；断网仍能校验已在本地的规范和引用；缺远程依赖时明确失败。
- 本地测试授权服务验证 PKCE 成功、错误 state、取消、过期、轮换和并发刷新；非交互自动化使用 Client Credentials 完成套件。
- Device 授权处理 pending/slow_down/denied/expired；OIDC 伪造签名、错误 issuer/audience/nonce 被拒绝。
- 任何日志、报告、Git 可提交文件与生成代码都找不到注入的 Token、密码或私钥。
