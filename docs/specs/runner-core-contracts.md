# A01 Runner Core：初始契约与敏感传播（未冻结）

状态：A01 初始工作契约，2026-11-06。**字段格式尚未冻结**，A02 UI 只允许消费 `Runner.Prepare`/`Run`、稳定事件/结果/报告端口，不得把下列 YAML 草案写死到公共项目模型。A03 拥有 Schema 实现，A04 拥有 OAuth；本卡仅定义注入端口。

依据：`docs/specs/desktop-test-automation.md`、`docs/acceptance.md` AC-06/07、`docs/decisions.md` 安全边界；已有 `core.BuildTrackedPlanWithOptions`、`core.ExecuteWithOptions`、`core.ResolveOptions`、`core.Redactor`、`core.CookieJar`、`core.Store.ReadBounded`。

## 1. 类型化断言

`Request.Assert []yaml.Node` 与 `SuiteStep.Assert` 都按相同语法解析，前者属于请求，后者叠加在步骤上；全部断言都执行并记录。每条恰好一个 target：

```yaml
assert:
  - status: 200                          # 或 {equals: 200} / {in: [200, 204]}
  - header: X-Request-Id
    exists: true
  - jsonpath: $.user.id
    equals: "{{data.userId}}"           # 支持一层运行时模板解析
  - body: {contains: "ok", notContains: "internal"}
  - durationMs: {lte: 1500}
  - size: {lt: 65536}
  - schema: {ref: "openapi.yaml#/..."} # 通过 SchemaValidator 注入 A03
```

通用 matcher（`header/jsonpath/body` 与 `body:` 内的 matcher）：`exists`（缺省为 false 时可单独使用）、`equals`、`notEquals`、`in`、`contains`、`notContains`、`regex`（Go `regexp`）、`type`（string/number/boolean/object/array/null）。`equals` 与数字按数值比较，字符串不加隐式类型转换；`exists: false` 表示缺字段/缺值，与空字符串/0/null 不同。`jsonpath` 匹配多个节点时全部节点都要满足 matcher；无匹配且不要求 `exists: false` 时失败。断言的 `expected/actual/message` 进入结果前统一经 `Redactor.RedactString`。

## 2. JSONPath 子集

根 `$`；`.name`、`['name']`/`["name"]`；`[0]` / `[-1]`；`.*` / `[*]`；`..name` / `..*`（递归下降）。对象 wildcard 输出按 key 排序，数组按索引排序，保证报告确定性。**不支持**过滤器、切片、union、表达式、脚本；解析阶段以 config error 拒绝，不静默忽略。

## 3. 提取与敏感传播

```yaml
extract:
  - name: token
    from: jsonpath       # jsonpath | header | cookie | body
    path: $.accessToken
    required: true       # 默认 true
    sensitive: true      # 可显式覆盖启发式
  - {name: userId, from: jsonpath, path: $.user.id}
```

仅当传输成功、全部断言通过、全部必需提取成功时，才把 `{{steps.<stepId>.<name>}}` 发布到该行上下文；任一失败不发布**任何**该步骤输出。`Sensitive` 默认规则：header 名为 Authorization/Proxy-Authorization/Cookie/Set-Cookie、任何 cookie、JSONPath 末段含 token/secret/password/api_key/authorization/cookie/session/credential/private_key 时为真。敏感值立即注册到步骤与 run 级 `core.Redactor`，模板引用自动携带 taint；报告/事件只记录 `***`、名称、是否发布，绝不含原值。显式 `sensitive: false` 可发布非敏感值，但报告仍对该值脱敏。`core.BuildTrackedPlanWithOptions` 会把敏感 step output 注册到步骤 Redactor，并对请求 URL/头/Body/诊断应用编码变体脱敏。

## 4. Suite / 运行计划与事件

`SuiteStep.Needs` 构成 DAG，声明顺序只用于同就绪级排序；依赖失败或取消时后继 `skipped`。`onFailure: stop` 不再启动新步骤/新行，`continue` 仍跑独立分支。每行独立 `RunVars/StepVars/DataVars/StepOutputs` 与独立 `core.CookieJar`；并发步骤共享只读输出快照，写通过 mutex，重试不跨行共享状态。步骤输出引用必须在 `Needs` 传递闭包内，且上游声明该输出，否则预检失败。`CollectionSuite` 以稳定相对路径排序构造线性 chain，不使用磁盘遍历顺序。

固定计划快照 `snapshotDigest`：suite 文件/结构化摘要、项目 ID/schemaVersion、全部请求相对路径+基线摘要、环境及基线摘要、数据集摘要；运行 ID/时间不进入摘要。报告为内存结果，导出由调用者显式指定本机目录；不写共享项目。

事件端口：`Emitter func(Event)`，引擎串行调用；`EventKind` = run/row/step/attempt started/finished、extraction_published、run_finished。事件含 runId/row/stepId/attempt/status/httpStatus/durationMs/message，消息脱敏，敏感提取只发名称与 `sensitive=true`。

## 5. 结果分类、失败/重试/取消

- `Status`：passed / failed / skipped / canceled / config_error / internal_error。
- `Failure.Category`：run、assertion、config、internal、canceled；`Kind` 细分 timeout/tls/connection/protocol/assertion/extraction/schema/target_policy/dataset/definition/missing_secret/template/report/panic/other。
- 配置错误（未知变量/缺密钥/数据集错误/未知请求/非法断言或 JSONPath/依赖环/未声明步骤输出/文件映射错误/Schema 预检失败/生产确认）在发网前一次性预检并 dry-build 失败，**零请求**。运行上下文强制 `Interactive=false` / `Confirmed=false`，绝不自动放行生产确认。
- 状态断言缺省时 4xx/5xx 只是协议事实，不自动失败；`status`/`header`/`body`/`jsonpath` 等显式规则决定判定。
- 每次 attempt 保留事件、HTTP 事实、断言、提取元信息和脱敏失败信息，`RetryCount` 可见；重试默认关闭，显式 `RetryPolicy` 支持 network/timeout/status/assertion、固定延迟、倍率退避、抖动、总 Deadline。取消、config/internal 错误、生产确认绝不重试；POST/PATCH 需 `allowNonIdempotent: true` 才允许重试。
- 取消：未启动步骤/行标记 skipped 并注明原因，运行为 canceled；等待退避的取消立即停止，不重试。

## 6. 报告

`RunResult` 含格式版本、项目/套件、run ID、快照摘要、环境、种子、起止/时长、数据行信息、逐行逐步骤逐尝试、断言/提取元信息、脱敏响应头/URL、失败分类、汇总、取消标记；**不含原始请求/响应 Body、Cookie 值、Secret 或私钥**。JSON 完整结构；JUnit XML 一行一 suite、一步一 testcase，skipped 不伪装成功。`ExportReports` 仅写调用者给定的本地目录，内存编码后用同目录临时文件原子替换；磁盘失败返回错误，不写共享项目。

## 7. 公共模型增量

`Request.Assert`（`[]yaml.Node`）已存在，无需修改 `core/model.go`。Suite/数据集走本包专属 `LoadSuite`/`ParseSuite`（`core.Store.ReadBounded`），不新增公共模型字段。A02 接入时只需 `New(project, suite, opts)`、`Prepare(ctx)`、`Run(ctx)` 和 `WriteJSONReport`/`WriteJUnitReport`/`ExportReports`；如需桌面主发送路径的 B04 网络选项，通过 `Options.ExecuteOptions`、`Options.PlanOptions` 和 `PreparePlan` 注入，不改公共入口。A03 实现 `SchemaValidator`（可选 `SchemaPreflighter`）；A04 继续拥有 OAuth/OIDC。生产确认交互归 B05-UI，Runner 非交互只失败不弹窗。

## 8. 尚未冻结 / 限制

- YAML 字段名与 matcher 方言是初始契约；A02/A03 联调后由集成人统一冻结并更新本文件与 `core/model.go` 增量。
- GraphQL/WS/gRPC/统计分位数断言归 P01–P03/C02；静态 HTML、性能 CSV 归后续卡。
- 数据集目前受 `DefaultMaxRows`/`DefaultMaxDatasetBytes` 有界后载入内存，非流式；行数、列数、字段集不一致均预检失败。
- 前后置动作仅声明式 set/generate；不执行脚本、不准备外部资源；`generate` 固定种子按行隔离，不调用真实随机源。
- 真实窗口、原生导出对话框、三平台矩阵与完整 A02 面板未验收；本文件不是整卡完成声明。
