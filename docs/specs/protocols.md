# GraphQL、WebSocket 与 gRPC

状态：1.0-B 需求基线；协议文件字段、库和流式脚本格式尚待定稿。

## 1. 共享能力

三个协议模块复用 Core 的项目文件、环境、变量、Secret、鉴权、取消、断言、事件时间线和报告。协议差异由适配器表达，不能为了统一 UI 而丢掉 gRPC Status、GraphQL errors 或 WebSocket Close 等事实。

流式自动化必须声明连接/消息/整体超时、最大消息数和结束条件；到达上限时有明确完成或失败状态，不得无限等待。事件记录有方向、序号、时间、关联 ID 和脱敏负载；大型消息落本机受限存储，不入 Git。

## 2. GraphQL

### 编辑、Schema 与执行

- 专用编辑器支持语法高亮、根据 Schema 自动补全和错误定位；Query、Mutation、Subscription 都是正式请求类型。
- Schema 可从本地 `.graphql` SDL 加载，也可由用户主动 Introspection 获取并保存版本/内容摘要；服务器禁用 Introspection 时仍能使用本地 SDL。
- Variables 独立 JSON 编辑区，支持 Core 变量解析；含多个 Operation 的文档必须显式选择 `operationName`，不能随机取第一个。
- 提供 Schema Explorer 展示类型、字段、参数、描述与弃用信息；SDL 与 Operation 可独立文件保存，引用不重复复制整个 Schema。
- Query/Mutation 使用 GraphQL over HTTP；请求方法、媒体类型、状态处理按选定协议兼容矩阵验证，GET 不用于执行 Mutation。GraphQL over HTTP 当前为工作草案，不能只按普通 POST 猜测行为。[官方工作草案](https://http-spec.graphql.org/draft/)

### 结果与自动化

响应分开显示 HTTP 事实、`data`、`errors` 和 extensions；200 不等于业务成功。测试默认将非预期 GraphQL errors 判为失败，允许显式声明预期错误和部分数据。支持字段/Schema/错误路径/耗时断言及敏感变量提取。

Subscription 优先采用名为 **`graphql-transport-ws`** 的 WebSocket 子协议；库名 `graphql-ws` 与旧版同名子协议不能混淆。连接初始化、确认、ping/pong、subscribe/next/error/complete、认证参数、Operation ID、取消和关闭均按其协议验证。[协议定义](https://github.com/enisdenjo/graphql-ws/blob/master/PROTOCOL.md)

认证初始化负载同样带敏感标记，不能原样记入报告。GUI 可让用户显式重连；自动化不静默重连，不把断流包装成订阅成功。SSE、旧版订阅协议和增量传输属于后续兼容评估。

验收：关闭网络仍能用本地 SDL 编辑并校验；对本地服务完成带 Variables 的 Query/Mutation、多 Operation 选择、部分错误展示及订阅；CLI 在限定时间内收到预期事件后完成，取消可终止连接。

## 3. WebSocket

### 会话界面

显示 disconnected/connecting/open/closing/closed/error 状态，URL、握手 Headers/Cookies、子协议、TLS/代理配置以及关闭码与原因。支持 Text、JSON 辅助编辑、Binary（十六进制/文件）收发，Ping/Pong 和往返时间、过滤、方向、搜索、时间线、选中消息保存。

发送帧边界与展示的逻辑消息需区分，分片重组和压缩的支持列入兼容清单。断线时禁止继续假装发送成功。消息重放在用户选择的目标环境重新解析 Secret，不能携带历史抓到的认证值。保存为项目示例前必须脱敏。

### 自动化会话

采用有界声明式动作：connect → send → await/assert → close。未来示例场景：

1. 连接本地测试服务并验证协商子协议。
2. 发送由 Secret 引用构建的认证消息。
3. 最多等待 3 秒收到 `auth.success`。
4. 发送订阅，验证随后事件的 JSONPath 和序号。
5. 正常 close，断言关闭码并输出报告。

`await` 明确从哪个接收游标开始、超时后如何处理，以及不匹配消息是保留还是跳过；不能无意消费其他步骤等待的消息。多等待者匹配规则需要协议脚本评审。自动化默认无重连；手工调试的自动重连需显式开启、有次数上限并在时间线上显示间断。

验收：Text/Binary、Ping/Pong、服务端主动关闭、错误握手、异常断流和超时均有真实结果；同一脚本 Desktop 与 CLI 判定一致，取消后服务端观察到连接终止。

## 4. 原生 gRPC

gRPC 四种调用模式必须全部覆盖，不能把只支持 Unary 宣称为完整 gRPC。[gRPC 核心概念](https://grpc.io/docs/what-is-grpc/core-concepts/)

| 模式 | 编辑与执行 | 测试结束条件 |
| --- | --- | --- |
| Unary | 一条请求、一条响应 | 响应与最终 Status |
| Server Streaming | 一条请求，持续接收消息 | EOF/Status 或显式消息条件与取消 |
| Client Streaming | 顺序发送多条请求，显式 half-close，接收结果 | 发送关闭后最终响应/Status |
| Bidirectional Streaming | 独立发送与接收时间线，允许交错和 half-close | 声明的消息序列与最终状态/取消 |

### 服务定义与请求

- 用户主动调用 Server Reflection 获取服务/方法；不可用时可导入本地 `.proto` 及依赖、描述符。
- 按 `Service → Method` 导航，保存 fully qualified service/method、模式、Proto/descriptor 引用和参数，不在每个请求中复制 Proto。
- 按 Protobuf JSON 映射生成模板；处理枚举、bytes、64 位整数、oneof、well-known types 和默认值时不得做有损的普通 JSON 类型猜测。
- Proto import roots 必须显式且受限，离线依赖由用户提供；不得隐式运行仓库内的 protoc/plugin。
- 支持 Metadata（含 binary metadata）、Deadline、TLS、自定义 CA、mTLS、明文连接显式配置及压缩设置；鉴权可映射到合适 Metadata。

### 响应与流控制

分开显示 Headers/Initial Metadata、每条消息、Trailers、gRPC Status 与错误详情，HTTP 层成功不能代替 gRPC OK。支持单条发送、批量脚本、half-close、暂停查看和取消；暂停查看不暂停无限接收或无限积压。

脚本动作包含 send、await、half-close、expect-status、cancel。服务端流与双向流必须能按序或显式匹配条件断言；消息数上限、每次等待和整体 Deadline 共同限制资源。Reflection 的结果可缓存到本机，明确版本，不能把旧描述符当当前服务事实。

初期只覆盖原生 gRPC；gRPC-Web 是独立接入方式，后续按需求评估，不因为桌面产品已有 UI 而默认支持它。

验收：对有 Reflection 和仅本地 Proto 的测试服务分别发现方法；完成四模式调用、Metadata/Trailer/Status 检查、证书失败、deadline exceeded、流中错误、half-close 与取消。CLI 无桌面依赖，并输出与 Desktop 同样的协议结果和断言判定。
