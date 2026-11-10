# F01：HTTP 自动化与鉴权/契约闭环

先读 formal4-common-20261110.md。分支 `cc/F01-automation-completion`，worktree `/private/tmp/gorequest-formal4-20261110/automation`。本卡拥有runner和runner/suite UI；不要修改其他路文件。

## 当前缺口和阶段目标

`runner/prepare.go` 明确拒绝oauth2/oidc；`desktop/runner_snapshot.go`将Step/RowConcurrency固定1且缺可选非交互token provider。Schema adapter仅JSON正文且无响应header contract。已有Suite/断言/提取/Mock调度不重做。本阶段交付可使用已授权OAuth运行HTTP Suite、受控并发、扩大契约断言覆盖、可检索有界结果入口；不实现三协议本体。

## 实施顺序

1. 静态核对Options、Prepare、执行attempt、desktop snapshot、core OAuth manager/cache/store、目标策略和Schema预检。先写并提交本路端口契约（见共同约定）及对应小测试。协议可扩展端口必须可选、缺少适配器时预检拒绝且零请求；HTTP默认行为不变，不为了接口强行实现未知协议。将端口提交和契约路径报告主代理，之后继续本路OAuth工作。
2. 通过注入接口复用既有core OAuth/OIDC token管理，非交互只允许已有授权token、合法refresh、显式client credentials等可安全自动执行流程；缺授权/需PKCE或Device交互时在发业务请求前拒绝并给出明确诊断，Runner不弹浏览器。设备token store由desktop adapter提供，Core Runner无GUI/keychain依赖。不要把OAuth配置当普通Bearer快照绕过过期刷新。
3. Prepare冻结方案/环境/克隆scope，token刷新/rotation single-flight，取消、过期/失效、部分准备失败无先发请求，鉴权值及编码/拼接形式进入redactor。目标策略仍强制非交互权限，OAuth不绕过生产确认。报告不含Token/refresh/client secret，跨源重定向不泄漏。
4. 在Runner已有StepConcurrency/RowConcurrency语义上增加有界UI输入及快照，不改变依赖顺序和数据行隔离。运行中冻结配置，每行Cookie/output隔离，失败策略/取消和Mock注入仍正确。上限与实际Core一致，非法输入明确报错。
5. 扩大ProjectSchemaAdapter的response contract：至少支持响应Header契约、多个可支持媒体类型（JSON及可准确校验的text/binary边界），保留严格unsupported预检、离线ref限域/总预算。按真实OpenAPI 3.1/3.2契约区分状态/媒体；复杂不支持语义报告，不伪称全JSON Schema兼容。补本路能力矩阵。
6. Runner最终结果支持有界分页/筛选和失败定位，不能只看前256条；报告完整性不退化。不将全结果变成每帧巨大控件树。Suite数据文件内容编辑不是本阶段硬门槛，已有配置/预览/注释保真保留。

## 验收

loopback授权服务+资源服务：已授权token、过期refresh/轮换、client credentials、缺token/需交互零业务请求、并发refresh、取消、fake secret与编码值无泄漏。相同保存快照桌面与纯Runner结果一致；两数据行×跨集合依赖和并发隔离、错误分类/JSON/JUnit、Mock清理。多媒体/Header契约正反例与离线坏ref预检。UI实际输入并发/筛选/分页，亮暗及窄窗口。

运行runner/相关core与desktop完整相关测试、build/vet和新路径race。写 docs/task-results/F01.md、端口契约与公共文件patch。本卡不能擅自承诺完整1.0-A全部实机验收；待集成人接线/组合验证。
