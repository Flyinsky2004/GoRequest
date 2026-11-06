# 正式版差距与 CC 并行任务账本

核对日期：2026-11-06。代码参考：`a339a80`；核对包含当时工作区可见的未提交改动，但它们不属于该提交，后续派工必须重新确定共同基线。

用途：后续开多个 Claude Code（CC）时按任务 ID 分配独立 worktree；完成并集成验证后再打勾。初次建立时仅完成分析与规划；2026-11-06 已创建两个 agy worktree，现已通过新版 Herdr app 交互启动，已获用户 Full Access 授权并恢复执行，领取记录见第 6 节。

## 1. 比较口径

“正式版”按 [产品需求](product-requirements.md)、[路线图](roadmap.md) 的完整目标解释：补齐 MVP/Beta，再到 1.0-A/B/C；不只指 1.0-A。需求优先级和阶段保持原文，以下分组是实施建议。

设计约束以 [决策记录](decisions.md) 为准；字段、依赖库、兼容版本、Mermaid 子集等草案尚未冻结，任务开工先补相应决策证据，不能把草案直接当稳定契约。**不恢复独立产品 CLI，不更换 MyGo Native UI，不把运行状态写入共享项目。**

[实现状态](implementation-status.md) 用作线索，本次另外检查了模型、存储、变量、鉴权、执行、桌面表单与现有测试文件。以下是静态核对结论，本轮没有重新运行 Go 构建/测试，也没有真实窗口人工验收；旧文档记录的验证属于历史证据。

## 2. 现有基础与实际差距

当前已有可运行的基础闭环：纯 Go Core、独立 YAML 请求、原子保存与摘要冲突检测、普通变量分层及 Secret 引用、基础 HTTP、原生多项目窗口、环境编辑、文件夹创建/移动、标签关闭保护、中文/英文与主题、Markdown 编辑/预览/分栏。

| 模块 | 代码现状与证据 | 距离正式版的缺口 | 任务 |
| --- | --- | --- | --- |
| 项目/文件 | `core/store.go`、`project.go` 有安全保存；桌面已有多项目/树/标签 | 新建项目向导、全局临时区、搜索、复制/更名/删除、最近列表管理；外部文件监听/刷新和冲突三版本交互 | B01、B02 |
| 格式/兼容 | `core/model.go` 严格解码入口、v1 模型；保存按结构体重序列化 | 注释/排版保真、公开 Schema、资源限制、自定义解码分支严格性、迁移变更集与恢复 | B01、R01 |
| HTTP 编辑 | `core/plan.go` 支持 JSON/Text/XML；`desktop/app.go` 仅七种方法、三种 Body | 自定义方法 UI、Form/multipart/Binary、`body.file`、编码预览/补全、批量参数编辑 | B03 |
| 网络/响应 | 超时/取消、默认 TLS、32 MB 截断、Pretty/Raw 与 Headers 已有 | Cookie Jar、代理、阶段超时、重定向策略、CA/mTLS、JSON 树/搜索/二进制保存、传输/解压大小区分 | B04 |
| 变量/鉴权 | `ResolveVars` 四层普通变量；`AuthFor` 项目默认/请求；Core 支持 API Key | 文件夹鉴权、来源/解析预览、API Key UI、系统钥匙串、目标策略、敏感属性传播 | B05 |
| 鉴权保存风险 | `desktop/tab.go` 的 `fillAuth` 展开 ref，`requestFromTab` 写回内联；未知模式回到 none | Bearer/Basic 引用及继承丢失；API Key 在桌面编辑/发送/保存链路可能变成 none，优先补回归用例 | B05 |
| 历史/本地状态 | 配置/最近项目已有；`platform/secrets.go` 仅环境变量后端 | 脱敏历史、保留/清除策略、设备目录/权限与克隆隔离；Cookie 与 Token 等不得入项目 | B05、B06 |
| 自动化 | `Request.Assert` 为 `[]yaml.Node`，不执行；`errors.go` 有配置/运行错误基础 | 单请求断言/提取、Runner、跨集合 Suite、DAG、数据行隔离、重试、统一取消/结果、JSON/JUnit、桌面运行界面 | A01、A02 |
| Git/导入导出 | 文件可供 Git 使用；没有产品 Git 操作和转换器 | 状态/Diff/选择性提交/Pull/Push、hook 策略；cURL/Postman/便携包、变更预览/损失与恢复 | B07、B08 |
| OpenAPI/契约 | 无 Operation 元数据/多响应契约及规范转换实现 | 3.1/3.2 分别验证、Schema 断言、引用/兼容性报告、来源模式与三方同步 | A03 |
| 高级鉴权 | 无 OAuth/OIDC 执行与缓存实现 | PKCE、Client Credentials、Device、刷新轮换/合并、OIDC claims/JWKS | A04 |
| 多协议 | `Request` 仍以 HTTP 为执行模型 | GraphQL 三类操作、WS 会话、gRPC 四模式及对应 UI/自动化；共享有界流生命周期 | P01、P02、P03 |
| 文档 | `desktop/workspace.go` 有 Markdown 编辑与原生预览 | GFM 兼容证据、大纲/图片/附件/跳转/搜索、稳定请求引用、链接诊断、离线导出、Mermaid | B09、C03 |
| Mock/性能/生成 | 无对应引擎 | Mock 生命周期、逐跳 Trace/采样统计、五类 Snippet、规范驱动 SDK | C01、C02、C04 |
| 应用/发布 | 主题/语言/启动恢复已有；无窗口测试与帧预算测试已有 | 字体/编辑器/自动保存/快捷键、输入法/无障碍/高 DPI、三平台安装签名与离线包验证 | B02、R01 |

不能据此宣称“完整 MVP 已验收”：路线图仍要求全局临时请求、外部刷新等；README 的“MVP 已实现”只反映已有可运行子集。文件保护目前能拒绝覆盖，但不等于已提供基线/磁盘/编辑三版本恢复界面；Header 黑名单脱敏也不等于端到端敏感数据传播。无窗口测试不能替代三平台真实交互验收。

## 3. Worktree 分组与完成清单

分支统一建议 `cc/<任务ID>-<slug>`，路径建议仓库同级 `GoRequest-<任务ID>`。**这些是待创建的建议名称**；若同级目录无写权限，创建时按实际权限流程处理。一个 CC 同时只领一张卡，任务过大可在自己的 worktree 内拆连续提交，不另开相互覆盖的副本。

下表 `[ ]` 表示尚未集成验收，既不是“没有人写代码”，也不是“测试失败”。未领取任务的负责人/提交/验收为“未分配 / 无 / 待验收”；实际领取情况见第 6 节。每次领取、阻塞和完成都更新第 6 节；各阶段可独立完成，不要求整条产品线一次写完。

### 基础补齐：Beta 前优先

- [ ] **B01 `file-sync`：外部刷新与格式保护。** 独占 `core/store.go` 的保存/恢复部分及新增文件同步模块；新增 `desktop/file_sync*.go`。监听修改/删除/更名，干净缓冲刷新、脏缓冲三版本处理；保存保留未修改字段与注释，审计自定义 YAML 解码严格性、大小/递归等限制。验收：外部编辑、删除、更名、监听风暴、保存失败均不丢编辑或复活文件；无操作打开不改盘。依据：基础规格 §1/6、存储格式 §3/8、AC-02、Q-02。
- [ ] **B02 `workspace`：项目与个人工作区。** 新建项目、全局临时请求与项目互转、搜索/面包屑、最近列表管理、请求/文件夹/文档复制更名删除，补字体/编辑器/快捷键/自动保存设置。复用现有窗口/树/标签。验收：复制换 ID、移动/更名保留 ID，引用/脏编辑/删除保护；全局状态隔离；自动保存遵守 B01。依据：基础规格 §1/8、AC-01/02、Q-04。依赖 B01 的刷新/事务接口；这是 UI 主线，和当前未提交 UI 改动先协调。
- [ ] **B03 `http-body`：完整请求编辑。** 独占 `core/bodyhelp.go`、`desktop/request_form.go`；新增 Body 编码模块。自定义方法、Form/multipart/Binary、外部文件映射、URL/变量预览、参数批量与原始视图。验收：本地服务逐项核对重复参数和编码、multipart boundary、文件缺失发网前失败、上传取消与上限；不吞掉未支持字段。依据：基础规格 §2、AC-03、Q-06。`plan.go` 的 Body 构建区由本卡拥有，网络执行区归 B04。
- [ ] **B04 `network-response`：传输、Cookie 与响应工具。** 独占 `core/plan.go` 的 Execute/transport 区及 `desktop/response_view.go`，新增网络配置/Cookie 模块。代理/阶段超时/重定向/CA/mTLS、隔离 Cookie Jar、JSON 树/搜索/下载、大小口径。验收：跨源重定向含自定义敏感头不泄密；坏证书/代理错误可定位；Cookie 按项目/会话隔离；下载取消释放资源。依据：基础规格 §2/3、高级鉴权 §6、AC-03、Q-09。接入 B05 的设备绑定/策略接口。
- [ ] **B05 `identity`：变量、基础鉴权与凭据。** 独占 `core/variables.go`、`auth.go`、`platform/secrets.go` 和 `desktop/tab.go` 的鉴权部分。先修引用/继承/API Key 往返，再补文件夹继承、来源预览、钥匙串适配、主机/生产策略和敏感标记/脱敏接口。验收：仅改 URL 不改变 ref/none/继承，API Key 三位置均可发；秘密及编码/拼接值不进诊断/导出；凭据缺失明确失败。依据：基础规格 §4、高级鉴权 §4、AC-04、Q-03/06/09。
- [ ] **B06 `local-history`：本地历史与运行存储。** 新增本地状态/历史模块，不另做网络引擎。脱敏快照、时间/项目/接口/结果筛选、期限/数量/清除、敏感及大 Body 不落盘。验收：同仓库不同克隆隔离，清除完整，路径/权限失败可诊断；运行前后共享文件不变。依据：基础规格 §3、存储格式 §1、Q-04；依赖 B05 脱敏、A01 结果契约，消费 B04 结果。
- [ ] **B07 `git`：Git 工作流。** 新增 Git 服务与独立桌面面板。状态、原生 Diff、选择性 Commit/Pull/Push；先记录 Q-05 的调用/hook/凭据策略。验收：非仓库/无 Git 仍可编辑 HTTP；提交只含所选文件；Pull 前处理脏缓冲；冲突不自动覆盖。依据：基础规格 §6、AC-05；依赖 B01。产品实现中的显式确认流程不意味着本轮已获 push 授权。
- [ ] **B08 `import-export`：基础互操作。** 新增转换模块，覆盖 cURL、Postman Collection/Environment、原生便携包；OpenAPI/Swagger 路由归 A03。生成可预览变更集、稳定 ID 映射、损失报告，确认后可恢复应用。验收：不执行 shell/脚本，重复导入/ID 冲突可控，路径/压缩炸弹/符号链接拒绝，失败不留下半项目，导出不携带私人状态。依据：基础规格 §7、AC-05；依赖 B01 变更集与 B05 脱敏。
- [ ] **B09 `docs-basic`：基础文档工作台。** 独占新增文档模块与 `desktop/workspace.go` 的 docView 部分。验证并列出 CommonMark/GFM 覆盖，补大纲/全文搜索/本地图片附件/离线导航/编辑体验。验收：中文/空格路径和缺失附件可定位，打开不下载远程图片、不执行 HTML，附件大小受限。依据：基础规格 §5、AC-15；请求派生卡片与 Mermaid 留给 C03。

### 1.0-A：自动化与契约

- [ ] **A01 `runner-core`：自动化 Core。** 新增断言/提取/运行计划/事件/报告模块。先实现 status/header/body/JSONPath 与单请求判定，再实现 Collection/Suite DAG、CSV/JSON、行/分支隔离、声明式前后置、失败/重试/取消、JSON/JUnit。冻结 Q-06/07/17 相关契约。验收：登录提取→查询→更新，至少两行；失败不发布提取，依赖失败 skipped；取消不重试，配置错误发网前失败；报告失败分类与敏感传播完整。依据：桌面自动化规格、AC-06/07；Schema 断言适配由 A03 提供，不在此重复造库。
- [ ] **A02 `runner-ui`：桌面测试与报告。** 新增独立 Runner/Suite 面板，复用 A01；选择请求/集合/套件、实时进度、断言/提取编辑、逐次尝试、错误分类、取消与导出。验收：同一保存快照与 Core 测试结果一致，多窗口不串结果，空筛选/报告写失败可见；未保存编辑有标识。依据：桌面自动化规格、AC-07。依赖 A01 稳定事件/结果/报告接口。
- [ ] **A03 `openapi-schema`：OpenAPI/Schema。** 新增契约模型与转换/Schema 模块；先 3.1，再单独通过 3.2 矩阵；涵盖 Swagger 2 迁移、Operation 元数据、多响应、引用、损失/覆盖率报告、原生/外部来源及三方同步、响应断言。验收：各目标版本 YAML/JSON 往返、离线多文件引用、错误实例路径、严格有损失败；不从一次响应猜契约。依据：OpenAPI 规格 §1–3、AC-08、Q-07/08。使用 B08 的变更集/预览边界，与 A01 通过 Schema 校验端口对接。
- [ ] **A04 `oauth-oidc`：高级认证。** 新增 OAuth/OIDC 服务与原生交互面板；PKCE 系统浏览器/loopback、Client Credentials、Device、并发刷新与轮换、Discovery/JWKS/claims 校验。验收：错误 state/issuer/audience/nonce/签名被拒绝；pending/slow_down/过期/取消与刷新竞争；缓存本地隔离且日志脱敏。依据：高级鉴权规格 §4–7、AC-09、Q-10。依赖 B04 transport、B05 凭据/策略；TLS/mTLS 不重复归本卡实现。

### 1.0-B：多协议，分别独立 worktree

- [ ] **P01 `websocket`：WS 与共享流生命周期。** 先提供有界事件/消息存储/游标/背压/Deadline 契约，再做 Text/Binary、握手/子协议、Ping/Pong、Close、重放与声明式自动化。验收：错误握手/异常关闭/超时/洪泛/取消、游标匹配与无静默重连。依据：协议规格 §1/3、AC-11、Q-11。依赖 A01/A02、B04/B05。
- [ ] **P02 `graphql`：GraphQL。** Query/Mutation/Subscription、SDL/主动 Introspection、Explorer/补全、Variables/Operation 选择、data/errors 分类及自动化。验收：本地 SDL 离线、部分错误、禁止 GET Mutation、`graphql-transport-ws` 生命周期与取消。依据：协议规格 §2、AC-10。订阅消费 P01 冻结的共享流接口，不能只交 HTTP POST 冒充全量完成。
- [ ] **P03 `grpc`：原生 gRPC。** Reflection/本地 Proto、四模式、Metadata/Trailers/Status、Protobuf JSON、half-close、TLS/Deadline 及流式自动化。验收：四模式成功/错误/取消/边界、Reflection 不可用时离线 Proto、资源上限；不自动执行 protoc/plugin。依据：协议规格 §4、AC-12。依赖共享流契约与 A01/A02，复用 B05 鉴权绑定。

### 1.0-C 与发布

- [ ] **C01 `mock`：本地 Mock。** 新增确定性匹配、响应场景/模板/延迟、受限访问记录、回环监听与 Suite 启停。验收：重叠规则可解释、端口冲突、取消与测试失败均回收，不因打开项目自动监听。依据：Mock 规格、AC-13、Q-13；依赖 A01 生命周期、A03 契约。
- [ ] **C02 `trace-performance`：性能诊断。** 基于 B04 的 transport 接口新增逐跳 Trace、复用/代理/阶段区间、多次低并发采样、nearest-rank/失败率/独立样本数、JSON/CSV。验收：不可观测不填 0、不把重叠阶段相加；固定样本 P50/P95/P99；失败/取消/样本不足与比较配置差异如实报告。依据：性能规格、AC-14、Q-14；依赖 A01 事件/报告。
- [ ] **C03 `docs-diagrams`：派生文档与 Mermaid。** 稳定 ID 引用、卡片/参数/公开示例、失效链接定位、离线 Markdown/HTML 导出；先决策并验证原生/离线 Mermaid 方案，声明子集与版本。验收：断网无 CDN，定义变化更新派生内容；不支持语法保留源码说明，中文/资源上限/目标冲突均覆盖。依据：文档增强规格 §1–3、AC-15、Q-12；依赖 B09/A03/B05。
- [ ] **C04 `codegen`：Snippet 与 SDK。** cURL/Go/Python/JS/TS Snippet；OpenAPI/Proto 驱动 SDK、固定模板/工具版本、输出清单/差异预览/重复生成。验收：向本地服务发送语义等价请求，编码/multipart 用例，Secret 占位，不覆盖手工文件、不自动下载或运行未知工具。依据：代码生成规格 §4–5、AC-16、Q-15；依赖 B03/B05/A03，Proto SDK 等 P03 契约。
- [ ] **R01 `release-quality`：发布与横向验收。** 公开格式 Schema/兼容迁移、故障恢复、Core/GUI 依赖检查、macOS/Windows/Linux 矩阵、输入法/键盘/高 DPI/可访问性、性能预算、版本固定/签名/完整性与离线安装包。验收：按 AC-01…16 逐项保留实际平台/版本/输入/结果；未验证项公开标明，不以构建通过冒充 UI/平台通过。依据：验收文档、Q-01/02/18。质量检查贯穿前述任务，完整发布门槛最后关闭。

## 4. 推荐派工顺序与公共文件规则

**先设共同基线 G0**：处理现有 UI 工作的交付归属；让其负责人提交或明确暂不纳入，再选择所有 worktree 共用的干净提交。当前脏文件含 `app.go`、`collection_tree.go`、`i18n.go`、`nav.go`、`project_state.go`、`uikit/icons.go`、`uikit/tab.go` 及示例环境；另有未跟踪调试二进制。不得替它们提交、丢弃或把二进制纳入版本。实际清单开工前重新 `git status --short`。

**建议先开 4 个 CC**：

| 槽位 | 首张任务 | 第一阶段交付边界 | 后续接力 |
| --- | --- | --- | --- |
| CC-1 | B01 文件保护 | 文件同步/冲突/变更集服务及专属 UI；不重构全局窗口 | B02，再 B07 |
| CC-2 | B03 HTTP Body | 编码与 Body 表单；仅拥有 plan 的 Body 区和 tab 的 Body 区 | B04，再 C02 |
| CC-3 | B05 鉴权凭据 | ref/继承/API Key 回归先行，变量/脱敏/凭据端口 | A04，再 B06 |
| CC-4 | B09 基础文档 | 专属文档文件与 docView，不改 HTTP/auth 区 | B08，再 A03/C03 |

首批 B01/B03/B05/B09 均不等于“随意同时改所有 core/desktop 文件”。**并行以各自模块与测试为主，公共壳层接线由集成人串行完成。** 四张卡先各交小的接口/模型提案，由集成人一次性合入必要模型增量，再让各分支接续；不等整张大卡写完才第一次集成。

| 公共入口 | 所有权/协作规则 |
| --- | --- |
| `core/model.go`、`project.go`、`validate.go` | 集成人统一合模型/加载/校验增量；分支通过专属模块实现，不各自重构入口 |
| `core/plan.go` | B03 管 Body 构建、B04 管传输；第一批只 B03 修改；C02 等 B04 后基于端口接入 |
| `desktop/tab.go`、`workspace.go` | B03 只 Body/表单，B05 只鉴权，B09 只 docView；字段/函数内不得交叉改，公共改动交集成人 |
| `desktop/app.go`、`project_state.go`、`nav.go`、`manager.go`、`i18n.go`、`go.mod/go.sum` | 集成人单一写入者；CC 交接线/翻译键/依赖清单及最小 patch，统一合入 |
| `docs/implementation-status.md`、本账本 | 集成人更新；CC 不各自把同一清单勾完造成合并覆盖 |
| 示例/测试服务 | 各卡用独立 fixture/测试文件，公共示例变更由集成人统一接入 |

建议后续波次：B01/B05 稳定后，A01 可以与 B02/B04/B08 并行；A01 契约稳定后 A02/A03/A04 分开；再冻结 Q-11，P01 先交共享流契约，P02/P03 基于该契约并行；最后 C01/C02/C03/C04。R01 持续记录证据，最后汇总验收。此顺序允许基础卡尚有 UI 接线时先进引擎开发，但不得提前关闭其验收。

## 5. 给每个 CC 的派工模板

```text
任务：<ID + 名称>，worktree：<实际路径>，分支：<实际分支>。
共同基线：<提交哈希>；前置依赖：<ID + 已集成哈希/冻结接口>。
先读 AGENTS.md、docs/parallel-work-plan.md 中本卡、对应规格/决策/验收。
只做本卡范围；遵守公共文件所有权，公共入口需提交最小接线方案由集成人合入。
先核对实际代码与本卡缺口，已有能力不重复实现；新增格式先提供兼容与往返方案。
纯 Go Core 承担业务，Native UI 承担交互；无产品 CLI、无 WebView 主界面。
完成必要的成功/失败/取消/隔离验证，检查 diff，仅提交本任务内容；不 push。
交付提交哈希、改动文件、验证命令/结果、人工未验收项、限制和集成步骤。
可把交付记录写 docs/task-results/<ID>.md；不要自行勾总账本或改公共实现状态。
```

2026-11-06 已按用户指定的 `~/.config/herdr-app/AGENTS.md`，通过新版 `herdr-app do tab.new` 启动两个交互 agy（默认 agent/model；当前为用户授权的 Full Access），目录信任已解除；2026-11-06 用户明确授权 Full Access 后，保留原会话通过 `--dangerously-skip-permissions` 恢复执行，已核对两个进程参数和工作状态。新版 `herdr-app`/`hc` 与旧版 `herdr` CLI 不同，不套用旧版的 HERDR_ENV 前提。其他 worktree 仍是建议；预计依赖库/协议兼容性不视为已经验证。

## 6. 领取、交付与打勾规则

状态流转：未分配 → 已领取 → 实现中 → 待集成 → 待验收 → 已完成；阻塞时记依赖/问题，不打勾。每次更新下面表格，并在第 3 节定位对应 `[ ]`。

| 任务 | 负责人 | 实际 worktree / 分支 | 状态 | 功能提交 / 集成提交 | 验证证据 / 限制 |
| --- | --- | --- | --- | --- | --- |
| B01 | agy-B01 | `../GoRequest-B01` / `agy/B01-file-sync` | 已合并，自动化通过；平台验收待补 | `19f005c` / `a4ab037` + 本轮接线 | 基线 `be92379`；Herdr 终端 `t1j`，标签 `B01 文件保护 · agy`；优先文件刷新/冲突模型，公共壳层交接线 patch |
| B05 | agy-B05 | `../GoRequest-B05` / `agy/B05-identity` | 已合并，部分 UI/策略未完成 | `bdca843` / `45170c4` + 本轮接线 | 基线 `be92379`；Herdr 终端 `t1k`，标签 `B05 鉴权凭据 · agy`；优先 ref/继承/API Key 保真，公共壳层交接线 patch |
| B03 | agy-B03 | `../GoRequest-B03` / `agy/B03-http-body` | 已合并，自动化通过；人工验收待补 | `ec3d6d5` / `b9a5812` + 本轮接线 | 基线 `666ed61`；终端 `t1q`，标签 `B03 HTTP Body · agy`；复杂 Body/文件映射/编码优先，公共入口交最小接线 patch |
| B09 | agy-B09（Opus 5.5 High） | `../GoRequest-B09` / `agy/B09-docs-basic` | 已合并，自动化通过；架构/人工验收待补 | `5bbe4c7` / `6336f7d` + 本轮接线 | 基线 `23662f3`；终端 `t1r`，标签 `B09 文档工作台 · Opus 5.5`；模型 `claude-opus-5-5-high`；大纲/搜索/图片附件/离线导航，公共入口交接线 patch |
| B02 | CC-B02（当前配置默认模型） | `../GoRequest-B02` / `cc/B02-workspace` | 实施中 | 无 / 未集成 | 基线 `c0b4f61`；Herdr `t24`，`B02 工作区 · CC`；补全局/项目互转与工作区缺口 |
| B04 | agy-B04（Argon Medium） | `../GoRequest-B04` / `agy/B04-network-response` | 实施中（Full Access） | 无 / 未集成 | 基线 `c0b4f61`；Herdr `t25`，`B04 网络响应 · agy Argon`；网络/Cookie/响应及 B05 策略端口挂接 |
| B06 | CC-B06（当前配置默认模型） | `../GoRequest-B06` / `cc/B06-local-history` | 实施中 | 无 / 未集成 | 基线 `c0b4f61`；Herdr `t26`，`B06 本地历史 · CC`；在现有元数据历史上补快照/筛选/限额/隐私 |
| B07 | CC-B07（当前配置默认模型） | `../GoRequest-B07` / `cc/B07-git` | 实施中 | 无 / 未集成 | 基线 `c0b4f61`；Herdr `t27`，`B07 Git 工作流 · CC`；独立 Git 服务、面板及 hook/凭据策略 |
| B08 | agy-B08（Argon Medium） | `../GoRequest-B08` / `agy/B08-import-export` | 实施中（Full Access） | 无 / 未集成 | 基线 `c0b4f61`；Herdr `t28`，`B08 导入导出 · agy Argon`；cURL/Postman/便携包及变更预览 |
| 其余未勾选任务 | 未分配 | 尚未创建 | 未分配 | 无 | 等后续派工逐项登记 |

本轮启动记录：`/private/tmp/gorequest-agy-20261106/sessions.json`；每项同目录有 `<ID>.prompt.txt`、`<ID>.retry.log`、`<ID>.retry.output.json`。这些是本机临时运行资料，不提交 Git，后续可能清理；长期交付证据由各 agent 写入各自分支 `docs/task-results/<ID>.md`。首次 auto 启动因账号不支持及后台命令无法交互授权而退出；accept-edits + 会话命令 allow 规则重试同样被拒绝，均已退出，未产出功能改动。不修改用户全局设置。随后按用户指定的新版说明，用 Herdr app 在 GoRequest space 新建 B01/B05 两个标签页并交互启动 agy。首次启动分别为 `t1j`、`t1k`，当时状态 blocked，屏幕为 “Do you trust the contents of this project?”；当时依 hc 手册等待用户确认；后续已获明确 Full Access 授权并解除。最新启动句柄另记本机 `herdr-sessions.json`。PID 仅表示历史启动进程，后续查询应结合进程、日志和分支提交确认状态。启动前确认另有 `ui-stage3` worktree，两个 agy 均被要求不触碰它或 main；主目录的示例环境和 `.claude/` 保持原状。

仅在以下条件全部满足后，由集成人将对应 `[ ]` 改为 `[x]`：功能提交已进入共同集成分支；该卡验收满足；必要测试通过；实现状态文档同步；记录提交和证据。只有引擎或只有 UI 的部分完成，应另记子项完成，不能勾整卡。

代码任务按影响运行针对性用例；集成门禁为 `go build ./...`、`go test -race ./...`、`go vet ./...`。真实窗口/平台验收未做就明确保留，不填“通过”。本次仅文档任务，检查 Markdown 路径、任务 ID、依赖归属与 diff，不为文档重复运行 GUI 测试。

后续会话从此文件恢复派工与打勾，不依赖聊天上下文。若实际代码超前或需求变更，先更新证据与缺口，再调整任务，不根据旧账本重复开发。

2026-11-06 权限更新：用户明确要求两个 agy 进入 Full Access；已在原 Herdr 终端 `t1j`/`t1k` 中恢复各自原会话，启动参数含 `--dangerously-skip-permissions`，未改全局设置。目录信任等待为历史状态，现已解除；功能仍未集成验收，不打勾。

2026-11-06 第三条派工：经用户要求通过新版 Herdr 新建 B03 标签页和独立 worktree，沿用会话级 Full Access、默认 agent/model。B01/B05 已有交付文档及分支提交，尚未审查/集成/验收；不以 Herdr done 状态认定整卡完成。现有用户终端 t1m/t1p 未操作。

2026-11-06 第四条派工：用户要求再开一条 agy 并指定 Opus 5.5；`agy models` 确认 `claude-opus-5-5-high` 可用后，以该模型在 Herdr 新标签交互启动 B09，沿用会话级 Full Access。不改其他 agent 的模型，不重复 B03 的 Body/B05 的鉴权范围。

B09 启动核对：已确认新 worktree 的目录信任；屏幕明确显示 `Claude Opus 5.5 · high`，进程参数包含 `--model claude-opus-5-5-high` 和 `--dangerously-skip-permissions`，hc 状态为 working，已读取资料并执行初始命令。

## 7. 2026-11-06 合并验收记忆

用户要求将已交付代理成果合并。本轮基于主分支 `8d4e379`（已含 UI stage3），在 `integrate/agy-deliveries` 独立 worktree 完成四分支合并、公共入口接线和组合回归，再快进回主分支。各子分支保留，不删除用户会话或 worktree。此前的“实施中/待合并”记录为历史。

以下仅勾选**本轮代码集成交付**，第 3 节整卡继续保留未勾选：不能把四个分支合并等同于全部正式版验收完成。

- [x] B01 交付已合并；监听生命周期/同步保存/严格加载接线，外部刷新与环境草稿冲突回归通过。剩余真实窗口/跨平台验收、字节级格式保真等见集成报告。
- [x] B03 交付已合并；复杂 Body 与鉴权敏感追踪使用同一发送路径，保留现有 KV/URL 交互，自动化通过。真实文件选择器/窗口验收待补。
- [x] B05 交付已合并；ref/继承/API Key、文件夹鉴权刷新、项目隔离凭据端口与脱敏接线通过。方案/文件夹/钥匙串录入 UI、目标策略确认及传输层重定向接线仍待完成，整卡不勾。
- [x] B09 交付已合并；文档大纲/搜索/安全附件/导航可用，刷新新文档组合用例通过。人工平台验收与 Core 直接依赖 MyGo 纯 Go parser 的边界处理待补。

集成事实、提交与验证见 [本轮集成报告](task-results/integration-2026-11-06.md)。后续从剩余项继续，不重复派工这些已经合入的代码；旧的 integration.patch 是交付时的参考，不再原样重复应用。

## 8. 2026-11-06 旧分支与 worktree 清理

用户明确要求清理旧分支。核对 B01/B03/B05/B09 与集成分支均为 `main`（`d64c380`）的祖先，五个 worktree 均无未提交或未跟踪文件，Herdr 中已无占用这些目录的运行 agent；随后使用不带 force 的 `git worktree remove` 和 `git branch -d` 清理：

- `agy/B01-file-sync` / `../GoRequest-B01`
- `agy/B03-http-body` / `../GoRequest-B03`
- `agy/B05-identity` / `../GoRequest-B05`
- `agy/B09-docs-basic` / `../GoRequest-B09`
- `integrate/agy-deliveries` / `../GoRequest-integration`

以上分支名称、路径和终端 ID 在前文均为历史交付记录，不代表仍存在。所有功能提交保留于 `main` 历史；后续剩余任务从最新 `main` 新建分支，不尝试恢复旧目录。仅保留主 worktree 和本地 `main`；未操作远程分支、未 push，主目录原有环境改动与调试文件保持原状。

## 9. 2026-11-06 基础工作台第二批：2 agy + 3 CC

用户要求按 `~/.config/herdr-app/AGENTS.md` 使用新版 Herdr，把五条基础工作台任务分为两个 agy、三个 Claude Code。五个独立 worktree 共用干净提交 `c0b4f61`；主目录原有环境与调试文件未纳入派工基线。表中 `t24`～`t28` 为实际 Herdr 终端，均已核对目录与工作状态。

两个 agy 起初使用 Opus 5.5 High，但均因额度用完未开始编码。随后用户明确表示已切换账号并选择 Argon，要求重新尝试：在原终端重新提交完整任务，确认两个界面为 **Gemini 4 Argon Medium**（`gemini-4-argon-medium`）、状态 working，且已读取文件/执行初始命令；不再切回 Opus。两者沿用会话级 Full Access。三个 CC 使用现有配置默认模型，以 acceptEdits 和本次会话工具允许配置启动，未覆盖模型/全局设置；启动核对时均 working。账号身份与凭据不写入仓库。

### 本批文件归属与依赖处理

| 任务 | 专属范围 | 复用与边界 |
| --- | --- | --- |
| B02 / CC | 新增 `core/workspace*.go`、`desktop/workspace_management*.go` / `global_workspace*.go` / `workspace_settings*.go`，必要的 `tree_actions.go` 工作区增量 | 现有重命名/删除/复制/移动、最近项与快捷键先核对；补全局临时区、新建项目与互转、搜索/面包屑/设备设置，自动保存复用 B01 |
| B04 / agy | 新增网络/Cookie/证书本机映射模块；`core/plan.go` 仅 Execute/transport 区，`desktop/response_view.go` 本卡增量 | 保留 B03 Body 构建；复用 B05 目标/重定向/脱敏端口；只补已有响应搜索之外的缺口，不做历史模块 |
| B06 / CC | `desktop/history.go`、新增 history/localstate 模块及测试 | 延续已有元数据历史；先适配当前 Plan/ExecResult/error/canceled，再提供未来 Runner 的 HistoryInput 入口，不重复造 A01，也不空等 A01；完整 Runner 适配仍待 A01 |
| B07 / CC | 新增纯 Go Git service、`desktop/git*.go`、独立临时仓库测试及 Q-05 证据文档 | 明确 hook/凭据策略；保留现有暂存区、选择性提交；Pull/Push 仅临时 fixture origin 联调，不向真实项目远程操作 |
| B08 / agy | 新增转换/便携包模块、`desktop/import_export*.go`、独立夹具 | 复用 B01 ChangeSet 与 B05 Redactor；只做 cURL/Postman/便携包，OpenAPI/Swagger 归 A03；B02 的全局项目互转不重复实现 |

公共 `core/model.go/project.go/validate.go` 与桌面 `app.go/project_state.go/nav.go/manager.go/workspace.go/tab.go/config.go/settings.go/i18n.go/send_guard.go`、`go.mod/go.sum` 由集成人统一接线，各 agent 提供专属模块与精确最小 patch；必要时可在自己 worktree 临时应用自己 patch 验证，再精确撤回自己的临时改动。禁止用全局强引用 map 绕开状态所有权，禁止重置整工作区。各 agent 不操作 main/他人 worktree，不启动更多子 agent，不修改共享 Git 配置，不 push。

交付到各自分支 `docs/task-results/<ID>.md` 与必要的 `<ID>-integration.patch`，记录提交、实际验证、未完成项、人工/平台限制。只在后续集成验收后勾完成，本次创建/启动不等于功能已实现。

本机可恢复启动资料：`/private/tmp/gorequest-foundation-20261106/sessions.json` 与五份 `<ID>.prompt.txt`；临时资料可能被清理，长期任务范围以本节及对应任务卡为准。旧的第一批清理记录为历史，不代表本批新 worktree 已被删除。
