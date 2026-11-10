# F02：GraphQL Schema/订阅及 WebSocket 有界自动化

先读 formal4-common-20261110.md。分支 `cc/F02-protocols`，worktree `/private/tmp/gorequest-formal4-20261110/protocols`。拥有core/graphql、graphql/ws UI、stream/websocket及新增protocolrun；不直接编辑runner文件/依赖清单。

## 现状与阶段目标

GraphQL已支持Query/Mutation、Variables、operationName、部分成功分类，但拒绝Subscription，无SDL/Explorer/Introspection。WS有手工会话和stream基础，无完整声明式send/await/assert/close/replay。阶段目标是可在原生界面使用本地Schema与有界订阅，以及可复用/可接Runner的WS动作执行；gRPC本体留后续，不为了赶工伪装成完成。

## 实施顺序

1. 先读 protocols.md、core/graphql、stream/websocket实际API；查GraphQL官方规范、graphql-transport-ws协议当前主文档，记录版本/链接与支持矩阵。不把普通WS连接当作GraphQL订阅实现。
2. 使用现有gqlparser支持本地SDL文件及显式schema引用/摘要/错误定位；路径/大小/深度限制，断网仍可语义校验。新增Schema Explorer和字段/参数/弃用信息；编辑器可行的schema补全须验证真实API，不扩展全局编辑器。Introspection只由用户显式动作触发，复用鉴权/网络取消/上限，结果预览确认后保存；打开项目/编辑不自动抓取。
3. 订阅实现graphql-transport-ws的init/ack、操作id、next/error/complete、ping/pong、关闭/取消/超时/消息上限与并发操作隔离。初始化认证敏感值注册redactor，默认无静默重连。保留HTTP Query/Mutation的既有行为，UI区分data/errors/transport close。
4. 在新增internal/protocolrun定义/实现可单测的有界WS动作plan：connect→send(text/binary)→await/assert→close；明确receive cursor、超时、跳过/保留不匹配消息、最大消息/字节、整体deadline。重放从保存定义重新解析当前环境/Secret，不复制历史认证值；每步事实/方向/序号/失败和关闭原因完整，不压成HTTP状态。
5. F01先产出Runner extension契约。先把SDL/订阅和独立protocolrun做完；得到契约后交实现adapter/最小integration patch供集成人接Runner。不要复制一套Suite调度或编辑F01 runner代码。若端口尚未到达，报告准确依赖并继续独立测试，不能把无接线adapter写成自动化已完成。
6. WS补文件选择发送binary、明确项目CookieJar握手注入策略与凭据限域（按core真实API），若需共享网络接口则交patch。原生UI把手动调试和有界脚本动作/重放入口做清楚。新增请求字段先用已有extensions兼容入口或提出core/model patch，不擅自改冻结格式。

## 验收

本地SDL与禁用introspection服务；多operation/坏schema/无网；loopback graphql ws服务覆盖ack超时、init拒绝、next/complete/error/ping、两操作隔离、取消关闭、secret脱敏。WS脚本正反断言、receive cursor不误消费、binary、超时/洪泛/取消/close code/无重连，桌面与独立executor结果一致。query/mutation及原WS全部回归。

跑core/graphql、stream/websocket/protocolrun完整相关测试、build/vet、协议race与desktop专项。写 docs/task-results/F02.md、契约矩阵、Runner/public入口patch。协议结束事实必须保留；未集成不勾完整P01/P02。
