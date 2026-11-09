# N04：原生 GraphQL Query/Mutation 首阶段（P02-phase1）

负责人 CC-N04；分支 `cc/N04-graphql-http`；worktree `/private/tmp/gorequest-next5-20261109/graphql`。

目标：第一等 GraphQL 请求能保存/重开，原生编辑 Document/Variables/operationName，经现有网络鉴权发送 Query/Mutation，明确呈现 data/errors。范围不含 Subscription、Introspection、SDL Explorer、自动补全与完整 Runner GraphQL 断言；必须明确标为后续，不能勾完整 P02。已有 WS 不是 GraphQL Subscription。

## 依据与唯一依赖写入权

读共同约束、`specs/protocols.md` §1–2、`storage-format.md`、现有 websocket_request/model/validate/plan/yaml_preserve、desktop websocket_workspace/tab/body_http_ui/network_ui/oauth。

主代理已核对官方参考：

- GraphQL over HTTP 工作草案 https://http-spec.graphql.org/draft/
- GraphQL September 2025 https://spec.graphql.org/September2025/
- 解析库官方仓库 https://github.com/vektah/gqlparser

你是本批唯一允许改 `go.mod/go.sum` 的人，仅允许固定 `github.com/vektah/gqlparser/v2 v2.5.64`（主代理已用 go list 核对版本/Go兼容），以及其必要间接依赖。不要临时选新库。使用 AST parser 做 operation 选择，不用正则/字符串前缀猜 query/mutation，不写自制 GraphQL parser。先 go get 固定版本，查本地模块的 token/depth 限制 API；有 token limit 就必须使用。任何依赖变化记录完整 diff，不升级 MyGo/Go 或无关依赖。

## 文件归属和持久化方案

拥有新 `internal/core/graphql*.go`、`internal/desktop/graphql*.go` 和专属测试/词条，以及必要的 `core/model.go/validate.go/plan.go/yaml_preserve.go/yaml_strict.go`、`desktop/tab.go/body_http_ui.go` 最小接线。`core/project.go` 若加载必须改则只最小协议接线。不能改 Runner 编辑/启动/快照。`app.go/workspace.go`、请求新建菜单/树图标公共入口交 patch。

明确采用与 WS 同样的共享 HTTP 目标 + 专属字段：`protocol: graphql`；`http` 持有 URL/headers/query/method；新 `graphql` 块持有 document、operationName、variables（JSON 字符串或保类型节点，选一种并记录）。不要同时把同一 document 保存在 http.body 和 graphql 两处，造成双事实来源。顶层 schemaVersion 仍为1的新增协议是可选能力，旧版本不能识别时应明确拒绝而不是降为普通 HTTP。协议字段严格解析、大小/深度有界。

## 实施步骤

1. 纯 Core 定义/解析/校验：document 必填，≤256 KiB；variables 必须 JSON object（空输入等价空对象，拒绝 array/scalar/多余 JSON），≤256 KiB，有界嵌套。JSON number 不提前转 float64 导致 ID 精度损失。
2. 通过 gqlparser AST 获得 operations；多 Operation 必须明确 operationName；无此名称、多个同名、匿名混用、语法错误有行列诊断。subscription 在本阶段发网前明确拒绝，不能用 HTTP POST 冒充支持。保留用户 document 原文，不格式化或自动做 introspection。无 Schema 时不能假称完成 Schema 语义校验。
3. 支持 POST Query/Mutation；GET 只允许 Query，拒绝 Mutation/Subscription。POST 用 JSON envelope `{query, operationName?, variables?}`，Content-Type application/json，Accept 同时支持 graphql-response+json 与 application/json；GET 用 url.Values 正确编码三个参数，处理用户已有 URL query 的冲突，不能双发两个 query 参数。
4. 变量解析复用 Core 的分层 Resolver/Secret 和 tracked body 流程。只解析 variables 中的模板，不对 GraphQL 文档用不安全全局字符串替换；严格保留布尔/null/数字/数组/嵌套对象类型。模板失败发网前报错。派生 HTTP plan 与原请求分开，不修改保存对象；小心 BuildTrackedPlan 递归调用时把派生 protocol 置 HTTP，避免无限递归。
5. 传输复用 ExecuteWithOptions/既有 tab 网络路径，包含 OAuth、API Key、Cookie、代理、TLS、target policy、重定向敏感头过滤、超时/取消、32MiB 截断等。不能另造 http.Client 绕过设置。POST Query 在当前 HTTP 生产策略下按 POST 处理，不借 Query 分类绕过本机保护。
6. 结果用有界 JSON 解码保留 HTTP 状态/原始正文和 data/errors/extensions。200 且 errors 非空明确显示 GraphQL 错误或部分成功；data:null 与 absent 分开；errors 的 message/path/locations 显示正确但经过 Redactor，不能把响应体未脱敏值带入报告。HTTP错误/协议错误/非JSON/截断/取消分别显示，不把200当成功。
7. 原生独立请求编辑 view：Document 编辑器、Variables JSON 编辑器、operation 下拉/显式名称、GET/POST、发送/取消、data/errors 分区。复用已有请求标签与保存/关闭保护；文案中英、主题可读；新界面状态必须绑定 Tab 稳定身份而不是全局当前项目。若使用独立状态 registry，按现有 weak/Cleanup 方式清理。
8. `requestFromTab`/fillTab 与 YAML 保真路径必须保存 graphql 字段：只改 URL/auth 也不能丢 document/variables；普通 HTTP/WS 无变化；外部文件刷新、复制/移动/重开都保留稳定 ID 和 GraphQL 数据。不要让通用 Body 编辑器把 graphql 请求覆写成 HTTP。
9. 暂未接入 GraphQL 业务判定的 Runner/Mock/Snippet/OpenAPI 路径必须明确拒绝或记 not applicable，不能当普通 HTTP 自动化通过。任何必要调用只交最小 patch，不碰 N02/N03 文件。新 GraphQL 请求的创建与主 view 路由交 `N04-integration.patch`，主体编译并用临时应用 patch 验证真实可达入口。

## 验收用例

- httptest 验证 POST query/mutation、GET query、精确变量类型、大整数、特殊字符、URL原有query、多operation选择；GET mutation/subscription/坏variables/缺secret/坏document均零发网。
- HTTP200+data、200+errors、data+errors部分成功、data:null、非JSON/4xx/截断、超时取消，结果分类真实；fake Secret 不出现在错误/元数据报告。
- TLS/鉴权继承/跨源剥离复用真实 Core 路径至少一组回归；不访问公网 GraphQL。
- 新建→保存→重开→只改 URL 再保存→外部修改冲突；两个项目结果隔离；headless 输入和真实按钮触发。
- core/desktop GraphQL 专项与 race、build/vet、普通全仓测试。报告 N04 和明确 compatibility matrix；不宣称订阅/Explorer/整卡完成。
