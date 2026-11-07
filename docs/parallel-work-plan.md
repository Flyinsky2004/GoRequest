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
| B02 | CC-B02（当前配置默认模型） | `../GoRequest-B02` / `cc/B02-workspace` | 已集成，自动化通过；人工/平台验收待补 | `a83cda1` / `8c1f18a` + 集成修复 | 基线 `c0b4f61`；Herdr `t24`，`B02 工作区 · CC`；补全局/项目互转与工作区缺口 |
| B04 | agy-B04（Argon Medium） | `../GoRequest-B04` / `agy/B04-network-response` | 已集成，自动化通过；人工/平台验收待补 | `082f479` / `e5bda1b` + 网络接线 | 基线 `c0b4f61`；Herdr `t25`，`B04 网络响应 · agy Argon`；网络/Cookie/响应及 B05 策略端口挂接 |
| B06 | CC-B06（当前配置默认模型） | `../GoRequest-B06` / `cc/B06-local-history` | 已集成，自动化通过；人工/平台验收待补 | `c78f468` / `c5548f1` + 集成修复 | 基线 `c0b4f61`；Herdr `t26`，`B06 本地历史 · CC`；在现有元数据历史上补快照/筛选/限额/隐私 |
| B07 | CC-B07（当前配置默认模型） | `../GoRequest-B07` / `cc/B07-git` | 已集成，自动化通过；人工/平台验收待补 | `80014fd` / `0bee8f4` + 集成修复 | 基线 `c0b4f61`；Herdr `t27`，`B07 Git 工作流 · CC`；独立 Git 服务、面板及 hook/凭据策略 |
| B08 | agy-B08（Argon Medium） | `../GoRequest-B08` / `agy/B08-import-export` | 已集成，自动化通过；人工/平台验收待补 | `6cf5fbd` / `0be5ae5` + 集成修复 | 基线 `c0b4f61`；Herdr `t28`，`B08 导入导出 · agy Argon`；cURL/Postman/便携包及变更预览 |
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

## 10. 第二批交付集成与后续派工（2026-11-06）

集成起点 `7639c0e`，独立集成分支 `integrate/foundation-deliveries`。仅合并实际提交且可验证的交付；主目录原有示例环境和调试二进制保留，不提交。交付 patch 的 B02/B06 接线已在功能提交内，B07/B08 由集成人补齐；完整证据见 [第二批集成报告](task-results/integration-foundation-2026-11-06.md)。

- [x] B02 本轮代码交付集成：工作区/互转/设置，补互转最新保存内容与脏缓冲保护。
- [x] B06 本轮代码交付集成：本地历史/隐私策略，与工作区设置共存。
- [x] B07 本轮代码交付集成：Git 导航、面板、模态及关闭时取消接线。
- [x] B04 本轮代码交付集成：高级网络/Cookie/JSON 树/二进制保存，发送/请求设置入口接线；生产确认仍缺。
- [x] B08 本轮代码交付集成：菜单/导入导出模态，补撤销编辑保护与附件冲突保护。

以上子项只表示代码集成与自动化通过，不关闭第 3 节整卡；人工/平台及交付报告中的剩余项继续保留。B04 随后完成提交 `082f479` 并以 `e5bda1b` 合入；主发送路径/设置接线与网络元数据脱敏由集成人补齐。

## 11. 下一批领取边界（2 agy + 3 CC）

第二批五卡合并后从新 main 的同一干净提交创建以下 worktree。实际终端/基线将在启动核对后登记；未核对启动前不视为执行中。各 agent 只修改本卡专属文件，公共模型/加载/桌面壳层交集成人 patch；本账本与实现状态由集成人更新。

| 子任务 | 代理 | 分支 / worktree | 本轮边界与并行依赖 |
| --- | --- | --- | --- |
| A01 | CC | `cc/A01-runner-core` / `../GoRequest-A01` | 断言/提取、Runner/Suite/数据隔离/事件/报告；A02 等稳定接口后再开。Schema 校验用注入端口，复用 B04 网络。 |
| A03 | agy Argon Medium | `agy/A03-openapi-schema` / `../GoRequest-A03` | OpenAPI/Schema 独立服务，3.1/3.2 分别验收；复用 B08 预览，公共路由和模型交 patch。 |
| B05-UI | CC | `cc/B05-identity-ui` / `../GoRequest-B05-UI` | 复用方案/文件夹鉴权/系统凭据管理与生产确认 UI，复用现有引擎；不做 OAuth。 |
| A04 | agy Argon Medium | `agy/A04-oauth-oidc` / `../GoRequest-A04` | OAuth/OIDC 独立服务/专属 UI，复用 B04/B05，不重写基础方案/凭据 UI。 |
| R01-parser | CC | `cc/R01-core-parser` / `../GoRequest-R01-parser` | 仅移除 Core 对 MyGo Markdown parser 包路径依赖，保留文档语义；本轮唯一直接写 go.mod/go.sum 者。完整 R01 继续未完成。 |

新卡不自动将整张正式版任务勾完；A01/A03/A04 先完成可运行闭环，再如实登记剩余阶段。新 agy 沿用用户切换后的 `gemini-4-argon-medium` 与会话 Full Access；CC 沿用当前默认配置。不得恢复额度已耗尽的旧 Opus 会话。

### 本轮清理与实际启动记录

`main` 已快进至 `4223cd4`（含五项合并及最终接线/脱敏修复；前序公共接线 `9c1115f`）。最终 `go build ./...`、`go vet ./...`、`go test -race ./...` 全部通过。B02/B04/B06/B07/B08 五个 worktree 均确认干净、分支头已是 main 祖先；关闭本轮对应 Herdr t24～t28 后以不带 force 的 worktree remove / branch -d 删除旧目录和分支，同时删除已合并的集成分支/worktree。用户其他终端与主目录原有示例环境/调试二进制未动。功能提交均保留在 main 历史，未操作远程。

新任务共同基线 `4223cd48612ac5925a2451e391184b5551d35066`，以下均通过新版 Herdr app 的 tab.new 在同一个 GoRequest space 启动，彼此独立 worktree；新派工不重复使用已删除的旧目录。

| 子任务 | 实际终端 | 实际分支 / worktree | 负责人/模型 | 领取状态 |
| --- | --- | --- | --- | --- |
| A01 | `t2b` | `cc/A01-runner-core` / `../GoRequest-A01` | CC / 当前默认模型 / acceptEdits | 实施中；已核对正确目录、任务与读取命令 |
| A03 | `t2c` | `agy/A03-openapi-schema` / `../GoRequest-A03` | agy / Gemini 4 Argon Medium / Full Access | 实施中；已确认目录信任，屏幕显示 Argon Medium 并已读取任务资料 |
| B05-UI | `t2d` | `cc/B05-identity-ui` / `../GoRequest-B05-UI` | CC / 当前默认模型 / acceptEdits | 实施中；已核对正确目录、任务与读取命令 |
| A04 | `t2f` | `agy/A04-oauth-oidc` / `../GoRequest-A04` | agy / Gemini 4 Argon Medium / Full Access | 实施中；已确认目录信任，屏幕显示 Argon Medium 并已读取任务资料 |
| R01-parser | `t2g` | `cc/R01-core-parser` / `../GoRequest-R01-parser` | CC / 当前默认模型 / acceptEdits | 实施中；已核对正确目录、任务与读取命令 |

提示词与精确 tab ID 仅存本机临时 `/private/tmp/gorequest-next-20261106/{sessions.json,<ID>.prompt.txt}`，不入 Git。后续查询以终端屏幕、分支提交与交付文档交叉核对；新任务未交付/未集成，暂不打勾。A02 仍未分派，等待 A01 稳定接口。B05-UI 和 R01-parser 为旧卡的独立剩余子任务，不能把它们算作新的正式版整卡。

启动核对补充：五个终端均为 working，三个 CC 屏幕显示当前配置 DeepSeek-V4.1-Flash / accept edits；两个 agy 屏幕均显示 Gemini 4 Argon Medium，目录信任按用户既有授权解除。当前 agy 对 `--model gemini-4-argon-medium` 有未识别提示并忽略该 flag，但会话实际沿用用户已选中的 Argon Medium，未回落到 Opus；后续重启应先核对 CLI 支持的模型 ID 与实际屏幕，不能只凭启动参数断言模型。

## 12. 用户反馈 UI 修复并行派工（Codex / Herdr）

用户新要求：①标签栏滚动条遮住标签底部，参考 VS Code 放上方并更细；②关闭叉与图标/文字不在同一水平线；③发送及大部分功能按钮 hover 缺少手型；④响应区正文/头部/Cookie/网络应固定在最左侧，不被仅正文存在的美化/原始/树形切换挤动。

前两项共用标签组件，合并给一个 Codex，另两项各一名 Codex，共三个独立 worktree。原 A01/A03/A04/B05-UI/R01-parser 五条开发继续，不中断。新任务只处理现有 UI，公共模型/业务引擎/依赖不改；涉及现有功能 agent 的共享壳层只提交精确接线 patch。

| 修复子任务 | 用户问题 | 分支 / worktree | 文件归属 | 状态 |
| --- | --- | --- | --- | --- |
| UI01-tabs | 1、2 | `codex/UI01-tabs` / `../GoRequest-UI01-tabs` | `desktop/tab_bar.go`、`uikit/tab.go` 及独立测试；滚动条上移/变细和关闭按钮对齐，同步实际内联标签与组件示例 | 已领取 / Herdr working |
| UI02-cursors | 3 | `codex/UI02-cursors` / `../GoRequest-UI02-cursors` | `uikit/components.go`、`uikit/response.go`、其他按钮组件及必要现有视图的 cursor 增量；不直接改 UI01/UI03 专属文件 | 已领取 / Herdr working |
| UI03-response | 4 | `codex/UI03-response-toolbar` / `../GoRequest-UI03-response` | `desktop/response_view.go`、独立布局/交互测试；内容分类固定靠左，正文格式切换与上下文操作在其后，不改网络引擎 | 已领取 / Herdr working |

UI02 如发现标签/响应文件存在直接按钮，交该两卡精确位置清单/patch，交集成人统一应用；不批量覆盖其他工作。UI01/UI03 在本卡修改区域同步设定启用按钮 CursorPointer，禁用状态/文本编辑/拖拽/滚动仍保留正确光标。视觉更细不等于缩小关闭按钮或滚动条拖拽命中范围。必须保留标签切换/中键/双击/脏关闭保护，以及响应树/二进制/下载/取消功能。

- [ ] UI01 标签栏滚动条与关闭按钮：待实现、集成及验证。
- [ ] UI02 功能按钮鼠标样式：待实现、集成及验证。
- [ ] UI03 响应区固定切换顺序：待实现、集成及验证。

先核对当前 MyGo v0.7.4 本地 API，再选择最小修复；不修改 Go module 缓存或升级依赖。验证应针对几何/hover/交互风险，无窗口自动化和真实窗口检查分别记录；不要宣称未做的人工验收通过。提交专属交付报告 docs/task-results/UI0x.md，功能集成后由集成人打勾。

### UI 修复实际启动记录

共同代码基线 `870339d9e4c389db69adec4122bf9acd49d38a15`。三条通过新版 `herdr-app do tab.new` 创建独立标签，Codex 交互模式含 `--no-daemon`，沿用当前配置默认模型；屏幕均显示 **GPT-6.1-Sol default**、正确任务与目录，hc 均为 working。会话级 `danger-full-access` / `approval never`，未改全局设置；其余五条功能开发未中断。

| 子任务 | Herdr 终端 | tab ID | 实际 worktree / 分支 |
| --- | --- | --- | --- |
| UI01 | `t2h` | `tba6311f6a4d13cd3f47a95ffa88c8c735` | `../GoRequest-UI01-tabs` / `codex/UI01-tabs` |
| UI02 | `t2j` | `tb2c214198624c5d1df0da742ffccb4a55` | `../GoRequest-UI02-cursors` / `codex/UI02-cursors` |
| UI03 | `t2k` | `tbbce012658901615a4b9862379369656e` | `../GoRequest-UI03-response` / `codex/UI03-response-toolbar` |

本机完整提示词与启动句柄：`/private/tmp/gorequest-ui-codex-20261106/`，仅为临时资料，不入 Git。长期范围与交付证据以上述卡片及后续 `docs/task-results/UI01.md` / `UI02.md` / `UI03.md` 为准。当前仅领取和启动，不表示修复完成；功能集成后再打勾。

## 13. 第二批 UI 修复派工（Codex / Herdr）

用户新增三项：顶部空白按住不能拖动窗口；左上 GoRequest 文字需下移至 macOS 红黄绿按钮与下方分隔线之间；Markdown 预览贴边，需留白及可配置默认渲染字体比例等参数。本批继续三个独立 Codex，不中断之前八条任务。

| 子任务 | 分支 / worktree | 专属归属和边界 | 状态 |
| --- | --- | --- | --- |
| UI04-window-drag | `codex/UI04-window-drag` / `../GoRequest-UI04-window-drag` | 新增窗口拖拽适配/测试；公共 app/manager/workspace 顶部挂载交最小 patch。不直接修改 UI05 的 navHeader 几何；不拦截按钮/输入/标签点击 | 已领取 / Herdr working |
| UI05-brand-position | `codex/UI05-brand-position` / `../GoRequest-UI05-brand-position` | 仅 `nav.go` 的 navHeader 几何与专属测试；保留系统三按钮安全区，按用户指定上下关系定位；顶部拖拽归 UI04 | 已领取 / Herdr working |
| UI06-markdown-preview | `codex/UI06-markdown-preview` / `../GoRequest-UI06-markdown-preview` | `documentation.go` 预览样式、专属 Markdown 本机偏好模块；必要 `workspace_settings.go` 偏好结构/读取/保存增量。设置入口/公共翻译交 patch；Core parser 归 R01-parser，禁止修改 | 已领取 / Herdr working |

- [ ] UI04 顶部空白拖拽移动窗口：待实现、集成和验证。
- [ ] UI05 GoRequest 文字下移至系统按钮与分隔线之间：待实现、集成和验证。
- [ ] UI06 Markdown 预览留白与默认渲染设置：待实现、集成和验证。

UI04/05 对同一标题区串行接线：UI05 只做布局，UI04 交 navHeader 的最小 DragWindow 补丁（如需），最终由集成人组合；不各自重构顶部。UI02 的手型增量如涉及本批专属文件只交补丁。UI06 只改 Desktop 渲染及本机偏好，不替换 MyGo 或变更 Core Markdown 解析语义；不与 R01-parser、UI01/03 或 A04/B05 业务界面交叉。默认预览应有舒适内边距，可调字体比例及有本地 API 支持的阅读参数，设合理默认/边界/恢复默认，老配置兼容且不写共享项目。即时应用不得清空编辑/滚动/标签/环境状态。真实 macOS 拖窗与系统按钮几何需要人工证据，无窗口验证不得冒充已完成实机验收。

### 第二批 UI 实际启动记录

共同基线 `d45d8d144a0ff39d54f4d4a49e229aed7d78c356`。三条均通过新版 Herdr tab.new 交互启动 Codex，含 `--no-daemon`，会话级 danger-full-access / approval never，未覆盖全局模型/设置。已核对正确任务/目录、屏幕 GPT-6.1-Sol default、hc working；启动成功只代表正在实施，不代表功能完成。

| 子任务 | Herdr 终端 | tab ID | 实际分支 / worktree |
| --- | --- | --- | --- |
| UI04 | `t2m` | `tba929cef77386f4d297c9924ca7c343d0` | `codex/UI04-window-drag` / `../GoRequest-UI04-window-drag` |
| UI05 | `t2n` | `tbd59bfd0d42cbcc1f985205eb42d96e25` | `codex/UI05-brand-position` / `../GoRequest-UI05-brand-position` |
| UI06 | `t2p` | `tbb334d79b5e3069136d41f33a19aeb9a1` | `codex/UI06-markdown-preview` / `../GoRequest-UI06-markdown-preview` |

完整提示词/句柄存本机临时 `/private/tmp/gorequest-ui2-codex-20261106/`，不提交；长期交付记录为 docs/task-results/UI04.md、UI05.md、UI06.md。UI02 已发送新增文件归属提醒，documentation.go/workspace_settings.go/navHeader 的手型增量仅交 patch。旧任务会话不关闭，R01-parser 已显示 done 但本轮未审查/合并，不据此勾完成。

## 14. 全分支集成移交 CC（2026-11-06）

用户要求由 Herdr 新开一个 CC 接手合并和清理。当前 main `4142dbc` 未变；11条功能/UI分支已合入 `integrate/all-agent-deliveries` / `../GoRequest-integration` 的 `3a8631b`。公共接线15个源文件已修改且交给接手者继续，尚未commit/快进main。首轮全仓race、带ui04_integration的UI组合专项、鉴权/OAuth专项通过；最终vet和修复后的门禁待完成，真实窗口/平台待验收。旧11个分支/worktree/agent暂未删除。

精确交接、验证日志、待查跨卡问题及清理tab清单见 [全分支集成交接](task-results/integration-all-handoff-2026-11-06.md)。后续负责人须保留该集成worktree的既有接线改动，不按未知dirty丢弃。仅在最终进入main且验收满足后更新勾选。

### 14.1 接手集成与交付（2026-11-06，t2r）

- **接手句柄**：Herdr 终端 `t2r`、tab `tbc11684376b52d0324d8a2c4c1f8483e0`，新版 Herdr 新开的集成与清理 CC；默认模型 Hy4 (hy4std)，用户未覆盖。用户声明从此时起不再并发写该 worktree。上一位集成人已于 `15b7955` 仅提交交接文档/账本，15 个源文件接线留在工作区由本会话继承并完成。
- **交付合并**：11 条功能/UI 分支的 no-ff 合并（至 `3a8631b`）由上一位集成人完成，本会话不重做；继承全部 15 个公共接线源文件并在集成分支提交 `integrate(all): finish public wiring and cross-card fixes for all 11 deliveries`（25 files / +806/-141，最终哈希见 main 的 `git log`，主目录 ff-only 快进后即为 main 提交）。最终集成报告见 [integration-all-final-2026-11-06.md](task-results/integration-all-final-2026-11-06.md)。
- **必要跨卡修复**（均有无窗口回归测试，不扩成 A02 或新产品功能）：
  1. `TestProductionPolicyPromptIsOneShotAndExact` 改用本地 `httptest`，确认后真实发送到本机且只命中一次，未确认的再评估不发任何请求；消除对 `api.example.com` 的公网依赖。
  2. `resolveActiveOAuthScheme` 以当前 `AuthMode` 为准，`none`/`bearer`/其他模式不再回退读取 `Request.Auth` 里的旧 inline OAuth，避免旧凭据/模式泄漏。
  3. B05 方案编辑：`draftFromScheme` 对 oauth2/oidc 保留真实类型与 `OAuthConfig` 并标记只读，`toScheme` 拒绝转换；UI 隐藏类型 Select/字段/保存按钮，防止编辑保存静默把 OAuth 变 bearer 丢字段。
  4. A04 ref/inherit OAuth 编辑改为只读检视（明确提示"此处编辑不保存，请用身份管理方案编辑器"），不再改内存方案、不标请求 Dirty、不虚报保存；内联 oauth2/oidc 模式仍可编辑并随请求保存。
  5. `SetOAuthConfig` 只写原生 `AuthScheme.OAuth` 单一表示，清除遗留 `extensions.oauth`，修复双重序列化导致严格加载器拒绝新请求文件；`ExtractOAuthConfig` 保留旧文件兼容。
  6. `requestAuthModes` 加入 `oauth2`/`oidc` 作为一等内联模式（保存走 `authPassthrough`、发包走 `ApplyTabOAuthIfConfigured`，非只加模式），并有保存/重开往返测试。
- **验证**：`go build ./...`、`go vet ./...`、`go test -race ./...`（全包）、`-tags ui04_integration` UI 组合专项、鉴权/OAuth/生产确认专项及跨卡回归均通过。日志 `/private/tmp/gorequest-final-race.log`。
- **如实记录的限制**：A01 Runner Core 与 A03 Schema 各有独立端口，**未做跨包 adapter 联合接线**，不宣称 Runner/Schema 联合验收或完整 Runner UI（A02 尚未开发）；未做真实窗口/macOS 拖动、系统凭据后端、原生文件对话框及 Windows/Linux 三平台实机人工验收，不勾为人工通过。
- **清理与 final main**：11 个旧交付 agent tab 均用 `herdr-app do tab.close` 精确 ID + `confirmed:true` 返回 `ok:true`（不动 `t1h`、不停 Herdr app/core、不关闭本会话 `t2r`）；逐个复核 11 分支均为 main 祖先且 worktree 全部 CLEAN 后，用不带 force 的 `git worktree remove` / `git branch -d` 删除全部 11 个旧 worktree/分支；集成 worktree 干净且 `integrate/all-agent-deliveries` 已是 main 祖先后，从主目录删除该 worktree/分支。主目录 `git merge --ff-only integrate/all-agent-deliveries` 把 main 从 `4142dbc` 快进到 **`ac06595`**（最终代码集成提交）；随后在主目录仅提交记账文档与一处措辞澄清（`docs: record final main ff-only, branch/agent cleanup and verification` 等，均在 `ac06595` 之后、无源码改动；各提交哈希见 `git log`）。`hc ls` 仅剩 `t1h`（用户）与 `t2r`（本集成 CC），`git worktree list` 仅剩主目录 main，`git branch` 仅剩 `main`。主目录用户 dirty（`examples/sample-project/.gorequest/environments/dev.yaml`、`examples/demo-server/__debug_bin3694048428`）全程保留，不提交/删除/覆盖。不 push、不改共享 git config、不 reset/clean。

## 15. 用户反馈修复并行派工（CC / Herdr，2026-11-07）

用户反馈六项：①存储同步误报「当前版本与磁盘版本不一致」，点「恢复」把编辑回退成旧版本；②移动接口到别的文件夹后左树不刷新，需重开项目才更新；③JSON 高亮的花括号/逗号等标点颜色太淡；④响应区 JSON 树折叠/展开按钮过大、箭头过小、留白多（用户已确认指响应区 JSON 树，非左目录树）；⑤单独开 CC 讨论「环境密钥」设计（普通变量 vs Secret、本机工具威胁模型）；⑥设置界面改为左分类栏 + 右主内容。

共同基线 `3cc1731459ff1c44d70cbd48b0e5ea3fa7773caf`（main，未含主目录脏的示例环境/dev 产物）。各卡独立 worktree，均由新版 Herdr `herdr-app do tab.new` 在同一 GoRequest space 启动 Claude Code（CC），会话级 `--dangerously-skip-permissions`（经用户明确授权 Full Access；安全分类器首次拦截、用户授权后放行）。`--task` 把任务作为首消息下发，启动核对均为 working、目录正确、无信任框阻塞。

| 任务 | 用户问题 | 分支 / worktree | 终端 | 专属文件归属 |
| --- | --- | --- | --- | --- |
| FIX01 | ①同步冲突误报 / 恢复回退 | `cc/FIX01-sync-conflict` / `../GoRequest-FIX01` | `t2t` | `desktop/file_sync*.go`、`core/file_sync.go`(SyncEngine)、`core/file_watcher.go`(仅必要) |
| FIX02 | ②移动后树不刷新 | `cc/FIX02-tree-refresh` / `../GoRequest-FIX02` | `t2v` | `desktop/tree_actions.go`、`desktop/collection_tree.go`(渲染)、`core/folders.go`(Move*) |
| FIX03 | ③JSON 标点色过淡 | `cc/FIX03-json-syntax-color` / `../GoRequest-FIX03` | `t2w` | `desktop/uikit/syntax.go`（`TokenPunctuation/Operator` 由 `p.Faint` 改可读折中色） |
| FIX04 | ④响应 JSON 树折叠按钮过大 | `cc/FIX04-json-tree-toggle` / `../GoRequest-FIX04` | `t2x` | `desktop/response_view.go`（`responseJSONTreeView` 折叠控件/占位） |
| FIX05 | ⑥设置左分类栏+右内容 | `cc/FIX05-settings-layout` / `../GoRequest-FIX05` | `t2y` | `desktop/settings.go`、新增 `desktop/settings_i18n.go`（不改 `i18n.go`） |
| ASK01 | ⑤密钥设计讨论（不写码） | 主目录只读（无分支） | `t2z` | 只读 `platform/secrets.go`、`core/variables.go/auth.go`、相关 docs；只讨论不改文件 |

约束与验证：各卡只在本 worktree 工作、不操作 main/他人 worktree、不 merge/cherry-pick/push、不 reset/clean、不启子 agent、不动示例环境与 `__debug_bin*`；公共壳层（`i18n.go/app.go/project_state.go/manager.go/nav.go/workspace.go/tab.go/config.go/go.mod/go.sum`）只交最小接线 patch 由集成人合入。各卡须 `go build ./...`、`go vet ./...`、`go test -race ./...` 并加回归用例，交付写 `docs/task-results/<ID>.md`，不自行勾总账本。本机提示词与句柄存 `/private/tmp/gorequest-fix-20261107/`（`*.prompt.txt`、`sessions.json`），不入 Git。当前仅领取/启动，不代表修复完成；集成验收后另行打勾。ASK01 为与用户直接对话的设计讨论，不产出代码。

### 15.1 交付、集成与清理（2026-11-07）

- **代码交付**：5 个 CC 各自在 worktree 内完成并本地 commit（`be58543` FIX01、`42fc504` FIX02、`82e1e46` FIX03、`22b0520` FIX04、`1bf14c6` FIX05），worktree 交付时均干净；均未触碰公共源码（`app.go/project_state.go/i18n.go/manager.go/nav.go/workspace.go/tab.go/config.go/go.mod/go.sum` 全未改），FIX05 仅改自己的 `settings.go`+新 `settings_i18n.go` 并连带调整几个既有测试。无跨卡文件冲突。
- **集成**：独立 worktree `../GoRequest-integration` / 分支 `integrate/fix-batch`，从 `c11d02d` 起逐个 `--no-ff` 合并 5 分支（`4933658`/`5987700`/`02aed9a`/`c253380`/`97697f4`），零冲突。门禁 `go build ./...`、`go vet ./...`、`go test -race ./...`（core 7s、desktop 82s 等，全包通过，日志 `/private/tmp/gorequest-fix-20261107/race.log`）。主目录 `git merge --ff-only integrate/fix-batch` 把 main 从 `c11d02d` 快进到 **`97697f4`**。
- **ASK01**：讨论会话越界补写了 `docs/decisions.md`(ADR-010) 与 `docs/storage-format.md` 的 Secret 实现现状说明；已核对引用的 `platform/keychain*.go`、`platform/secrets.go`、`GOREQUEST_SECRET_`、`SecretProvider` 均真实存在后，作为文档提交 `92d9968` 纳入 main（仅文档，不改代码语义）。
- **清理**：5 个功能分支已入 main，删除其 worktree 与分支；集成分支/worktree 亦删除；关闭 5 个代码 agent 标签（`t2t/t2v/t2w/t2x/t2y`）；ASK01（`t2z`）与用户终端 `t1h`、集成 CC `t2r`、`t2s` 不动。
- **如实保留的限制**：均为无窗口自动化 + `-race` 通过；FIX01 三版本模态键鼠、FIX02 拖拽/右键移动即时刷新、FIX03 标点色观感、FIX04 折叠按钮尺寸、FIX05 左右栏布局与暗色/窄窗口，均**未做真实窗口人工/三平台验收**，不据此宣称人工通过。FIX01 另提一处**可选**记账接线（非必需）。

## 16. 设置高度、发送反馈、超时与密钥入口（2026-11-07）

用户要求继续用 Herdr 开几个 CC 修四项。开工代码基线 main `e56fc0f`：旧任务均已合并清理，当前仅主worktree；用户示例环境改动与两个调试二进制不纳入基线。修复现有入口，不重复实现网络或凭据引擎；ADR-010 的 Secret 外部化与系统凭据隔离边界继续有效。

| 任务 | 分支 / worktree | 独占范围与目标 | 状态 |
| --- | --- | --- | --- |
| FIX06 | `cc/FIX06-settings-height` / `../GoRequest-FIX06` | `desktop/settings.go` 面板高度：保留左分类/右内容，把当前接近整窗高改为约现有一半的紧凑默认，内容滚动/Done可达，兼容小窗口 | 已合并 / 本轮跳过验证 |
| FIX07 | `cc/FIX07-send-feedback` / `../GoRequest-FIX07` | `desktop/uikit/response.go` SendButton：启用hover/pressed可见颜色差异，保留手型/发送取消图标/点击/尺寸，深浅主题都可辨 | 已合并 / 本轮跳过验证 |
| FIX08 | `cc/FIX08-timeout-entry` / `../GoRequest-FIX08` | `desktop/network_ui.go` 超时段与专属双语提示/测试：明确总超时入口/单位/继承后实际值与阶段超时归属，不再隐藏在技术设置中；顶层快捷入口若需公共文件交patch | 已领取 / working |
| FIX09 | `cc/FIX09-secret-value-entry` / `../GoRequest-FIX09` | `desktop/environment_editor.go` 密钥声明入口、`identity_management*.go` 凭据录入：从本地密钥名称直接设置/更新同名本机秘密值、绑定状态与不可用错误，复用原系统凭据管理，不能写明文项目 | 已领取 / working |

- [ ] FIX06 紧凑设置面板：代码已合并，用户要求本轮不跑测试，组合/人工验收待补。
- [ ] FIX07 发送按钮 hover/pressed 颜色：代码已合并，用户要求本轮不跑测试，组合/人工验收待补。
- [ ] FIX08 请求超时的可发现入口与准确说明：待集成验证。
- [ ] FIX09 声明密钥后的实际值录入与请求闭环：待集成验证。

公共 app/manager/nav/workspace/tab/config/i18n、go.mod/go.sum 与账本由集成人拥有，新公共接线交最小patch；FIX06 设置壳层只接入口patch，不与FIX08/09并行重写公共设置。按钮状态样式仅改SendButton，FIX08不改uikit按钮；FIX09不改网络超时。各卡独立测试与临时夹具，不写真实凭据/本机配置、不触碰用户窗口/示例dirty，不push/merge/reset/clean/改共享git config/启子agent。完成适当验证后只commit本卡文件，写 docs/task-results/FIX0x.md。人工/三平台未做必须明示；集成后再勾选。

### 16.1 实际启动

共同基线 `a20960beffbc63880eb9e74214ec2675523af6bf`。四条均通过新版 Herdr tab.new 交互启动 CC，沿用此前授权的会话 Full Access，不改全局设置。屏幕已确认各自目录/任务、Hy4 (hy4std) 当前默认模型、bypass permissions；hc均为working。未修改其他用户终端。

| 任务 | Herdr终端 | tab ID | 实际worktree / 分支 |
| --- | --- | --- | --- |
| FIX06 | `t32` | `tb54260f1c08be1ac2b3dab9b5ebf36b79` | `../GoRequest-FIX06` / `cc/FIX06-settings-height` |
| FIX07 | `t33` | `tbea34262dc5be98ea32daf47da9664ded` | `../GoRequest-FIX07` / `cc/FIX07-send-feedback` |
| FIX08 | `t34` | `tb9b69eaf892616671b83cf58c899e40ab` | `../GoRequest-FIX08` / `cc/FIX08-timeout-entry` |
| FIX09 | `t35` | `tb0b17915347082d0862d3effb432f8c06` | `../GoRequest-FIX09` / `cc/FIX09-secret-value-entry` |

本机提示词/句柄 `/private/tmp/gorequest-fix06-09-20261107/` 不入Git。新任务只完成领取，未交付/未集成，不打勾。现有超时入口为请求编辑区设置→请求总超时；密钥真实值入口为顶部项目菜单→系统凭据，声明名本身不等于绑定值。本批需改善上述发现与操作闭环，不能只重述旧入口即交差。

## 17. A02 首个闭环：Runner/Schema 联合接线与桌面运行（2026-11-07）

用户要求建议优先步骤并开一个 Codex 实施。选择先完成自动化可用闭环，不同时启动多协议；按顺序由同一个 agent 执行：①A01/A03 Schema 适配和发网前预检；②最小桌面集合/Suite Runner，运行/进度/取消/结果/JSON与JUnit导出；③使用本地认证→提取→查询→更新及至少两行数据完成联合回归。此为 A02 第一阶段，不能声称完整 A02 或正式版验收完成。

- [ ] A02-phase1：代码已合并并接入项目菜单/模态；用户要求本轮不跑测试，组合/人工验收待补，完整A02仍未完成。

分支 `codex/A02-runner-first-loop`，worktree `../GoRequest-A02`。开工参考 main `a9fa5b0`，实际共同基线以领取记录为准；基线已包含 A01/A03/A04/B05/UI01-06，FIX06-09 四条worktree仍独立开发（其中FIX07已有提交但未合入），不得依赖或改它们目录。

专属实现 `internal/runner` 的新增 Schema adapter（必要接口最小增量）、`desktop/runner*.go` 与专属双语词条/测试、独立本地 fixtures 和 A02 专属证据/交付文档。复用现有 Core/网络/凭据/报告，不另建自动化引擎。app/project_state/manager/nav/workspace/tab/i18n/config公共状态/菜单/生命周期接线交最小patch，临时套自己patch联调后只撤回自己的临时更改；不得碰FIX06的settings.go、FIX07的uikit/response.go、FIX08的network_ui.go、FIX09的environment_editor/identity_management*。

先核对现有 runner.SchemaValidator 与 A03 SchemaValidatorPort 的真实差异，再做明确引用规则/错误分类/预检；不得把不可解析或没有schema误当通过，不让未知格式/坏引用在发网后才失败。运行固定保存快照，脏编辑清楚标识；UI更新只投递发起窗口，取消/关窗回收，结果数量/事件列表有界，不写共享项目历史/令牌/报告，不放宽生产策略。桌面选择集合/已有Suite文件、显示步骤/逐次尝试/错误与skipped，报告显式导出本地；未实现的可视化断言/提取编辑器、完整Suite编排/数据编辑界面如实列后续，不用大段源码输入假冒可视化闭环。

必要成功/失败/取消/隔离/Schema错误路径/脱敏/导出失败回归与build/vet/race。真实窗口/平台未验证如实标注，契约首次联合验证后记录兼容与待冻结点，不宣称全面冻结。仅本卡可交付代码完成验证后自行commit；最终集成与勾选由集成人执行，不push/merge/删分支/操作其他agent，不启子agent。

### 17.1 实际领取

负责人 Codex / 当前默认 GPT-6.1-Sol，Herdr终端 `t36`，tab `tba6a08517f98dd3bea9c1ac3a00c6dfc8`（A02 自动化首个闭环 · Codex）。实际worktree `../GoRequest-A02` / `codex/A02-runner-first-loop`，共同基线 `3d51b99c29d9f583fcec8339e3ab77dc7f121643`。新版tab.new交互启动含 `--no-daemon`，会话 danger-full-access / approval never，未改全局模型/配置；已核对屏幕任务、目录和模型，hc working。其余四个FIX任务未中断。

完整提示词/句柄存本机临时 `/private/tmp/gorequest-a02-20261107/`，不入Git；长期范围见§17，后续交付 `docs/task-results/A02-phase1.md`。当前只领取，未交付/未集成，不勾整张A02。

## 18. A02-phase1 / FIX06 / FIX07 合并（2026-11-07）

用户明确要求只合并这三项到main、清理对应旧分支/worktree并关闭Herdr agent，**先不执行测试**。从main `0695700` 在独立 `integrate/a02-fix06-fix07` / `../GoRequest-integration` 合并三分支，无冲突；FIX08/FIX09仍在开发，完全保留。

| 交付 | 分支头 | 本轮合并提交 |
| --- | --- | --- |
| `codex/A02-runner-first-loop` | `df7dc63` | `6bb7fd1` |
| `cc/FIX06-settings-height` | `746097a` | `00348a2` |
| `cc/FIX07-send-feedback` | `22d28f0` | `aad72cc` |

A02公共两行接线已实际应用：App.View渲染RunnerModals，项目菜单BuildRunnerProjectMenuItems；FIX06/07无需额外接线。新增Schema adapter与预检、保存快照Runner/导出、设置紧凑高度、发送hover/pressed反馈进入本轮代码。A02仍为第一阶段：可视化断言/提取编辑、完整Suite/数据编排界面、结果分页等未完成；OAuth/本机上传槽/部分Schema方言按交付限制明确拒绝。

本轮仅检查交付说明、diff、补丁适用性和Git祖先/干净状态，不运行build/vet/go test。各agent交付文档内的独立验证是历史证据，不冒充本轮组合门禁通过；真实窗口/跨平台未验收，故主验收checkbox继续未勾。未push、不改共享Git配置，不提交用户示例环境或调试产物。清理结果在操作完成后补记。
