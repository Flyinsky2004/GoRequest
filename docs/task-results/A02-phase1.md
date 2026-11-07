# A02 phase 1 交付记录

负责人：Codex；worktree `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A02`；分支 `codex/A02-runner-first-loop`；共同基线 `3d51b99c29d9f583fcec8339e3ab77dc7f121643`。本记录不勾选 A02 整卡。

## 第一步：Schema 联合接线

新增 `runner.NewProjectSchemaAdapter(project)`，实现 A01 `SchemaValidator` / `SchemaPreflighter`，实例验证调用 A03 `core.NewSchemaValidator().ValidateInstance`，无 core→runner 依赖。预检独立检查配置，不验证虚拟实例；递归遍历所有已支持 Schema 分支，包括未被实例触及的 properties/$defs/组合分支，离线解析引用并冻结到内存。最多 128 文件、合计 8 MiB、48 层、10000 Schema 节点；引用文件和节点预算在本次 adapter 内累计，另有保守估算 8 MiB 的展开 JSON 预算（字符串按最坏转义估算），拒绝重复引用大常量造成的放大；文件经项目 Store 读取，拒绝远程/越界/符号链接逃逸路径、循环、缺引用、缺 Schema、未知关键字或不支持的配置。运行不重读 Schema 文件。

引用规则（初始版本，未冻结）：

- `schema: {ref: schemas/user.yaml}` 或 `file.yaml#/components/schemas/User`；路径相对当前项目 `.gorequest/`，嵌套 `$ref` 相对所在文件，fragment 仅 RFC6901 pointer。
- `schema: {operation: getUser}` 选择唯一已保存 `extensions.openapi.operationId`，不可同时指定 ref。响应选择顺序：精确状态、`nXX`、default；无匹配为断言失败。
- 第一阶段 Operation 仅支持恰好一个 `application/json` body、没有 response header contracts 的响应定义；所有声明响应都须有 Schema。实际 Content-Type 不匹配为断言失败；缺 Content-Type 沿用 JSON 默认。其他媒体/头契约明确预检拒绝，不宣称完整 OpenAPI 响应覆盖。
- 支持 Schema：boolean、type、properties/$defs/definitions、items/prefixItems、additionalProperties、allOf/anyOf/oneOf/not、required、pattern、长度/数量/数值界限、multipleOf、enum/const、uniqueItems 及常规注解；仅接受 2020-12 `$schema`。不支持 `$id`/anchor/dynamicRef、递归引用、format、conditional/dependent/patternProperties/contains/unevaluated；未知关键字失败。`$ref` 与显式 allOf 同级暂拒绝。

A01 端口最小兼容增量：`SchemaRef.Headers [][2]string`，现有实现不受影响，evalSchema 传入实际响应头。Schema 断言报告不携带原始 ref；adapter 不输出 schema 常量、actual 或引擎原始 message，仅约束名与实例路径。坏配置诊断固定文字，避免预检前泄露 Schema 内容。

验证：`go test -race ./internal/runner` 通过；新增测试覆盖离线多文件 fragment、冻结后文件变化、精确 `$.data.id`、未知/缺失/循环/远程/越界引用、未触及分支坏 pattern、坏类型、零 Executor 调用、Operation 状态不匹配与无 Schema 拒绝。

第一步提交：`688b05a`（后续联合回归提交进一步收紧配置资源边界）。

## 第二步：最小原生桌面 Runner

新增 `desktop/runner*.go`，双语原生项目菜单入口和 Modal；菜单接线只需随附 `A02-phase1-integration.patch` 两处调用，不添加公共状态字段。状态由 weak App key 关联，不强引用 App/Window/Tab/projectState；关闭窗口取消，后台只通过发起窗口 Update 投递，同窗口 generation 拒绝旧结果。项目菜单→集合/Suite Runner→选择集合或现有 `*.suite.yaml/yml`→明确环境→运行保存快照→逐次尝试事件/最终行步骤/分类汇总→显式导出 JSON/JUnit。根集合选项 `collections` 代表全部保存请求，私有合成 CollectionEntry 后仍交 `CollectionSuite` 构建确定顺序。现有 Suite 通过 `LoadSuite` 解析，无源码编辑冒充可视化。

开始时在 UI 线程 `OpenProject` 获取独占保存对象，复制 B04 ExecuteOptions，获取 B05 SecretProvider 内容，Prepare 冻结数据集、Schema、固定项目附件和 TLS PEM，然后密封 provider 再进入后台。后台不读取任何 UI 对象；本次环境、Cookie 行隔离、step outputs、并发各 1；不会随项目切换、编辑或环境变化重读。脏编辑提示明确。事件队列和可见列表各最多 256，最多一个待处理 Update；完整 Core 结果仍在内存并用于报告。关闭/Escape/点击外部取消运行，无空闲 ticker 或持续刷新。

补齐实际发现的 A01 闭环缺口：Prepare 拒绝尚未联合的 OAuth/OIDC（含有效继承方案），不会无鉴权发送；已知静态目标在 dry-build 后调用现有 EvaluateTarget，非交互/未确认，仍由执行 Core 对每个实际目标和重定向重新检查。预检开始即注册可枚举 Secret，Schema 内容摘要加入快照（可选 `SchemaSnapshotDigest()`，原 Schema 接口保持兼容）。没有更改 Core 安全语义。

JSON/JUnit 导出支持输入绝对本地路径及原生 Save 路径选择；实际导出按钮明确写文件，0600 临时文件原子替换，失败显示 `internal_error / report`；拒绝项目目录内及经父级 symlink 指向项目的路径，不写共享历史/运行状态。报告沿用 Runner 编码器和脱敏结果。

阶段限制：本地槽位上传和动态附件路径在发网前明确拒绝；固定项目附件最多合计 64 MiB，TLS 每项最多 8 MiB。OAuth/OIDC 不在此阶段适配。快照准备有界但在 UI 线程同步进行，大附件可能短暂阻塞，后续可将纯文件读取移出 UI。可视化断言/提取编辑器、完整 Suite 编排、数据编辑 UI、最终完整结果分页/搜索均后续；当前实时/最终列表有界，完整结果看显式导出。保存 Suite 的发现规则为 `.suite.yaml/.suite.yml`。

本步验证：已临时应用自己的两行公共 patch；`go test -race ./internal/desktop ./internal/runner -run 'TestRunner|TestSchemaAdapter|Test.*Production'` 通过，覆盖原生控件 headless 渲染、脏编辑提示、10000 事件仅一个待处理回调/256 事件、文件快照密封、导出失败可见及项目路径/symlink 拒绝。此处 headless 不是人工窗口验收。第二步提交：`355ac70`；联合回归及最终修正见下。


## 第三步：联合回归与最终修正

实际本地 `httptest` 登录→敏感 Token 提取→查询→更新→Schema，分别通过 CSV 两行和 JSON 两行。每次桌面运行 6 请求；断言每行 Cookie/Authorization/Body 隔离。比较同一保存输入的直接 Core 与桌面 Snapshot、Status、Summary、逐步骤状态/尝试数；开始后改 UI 的脏 URL、环境、模型和设备 denylist，不改变本次结果。坏 Schema 未触及分支的缺 ref 为配置错误且 0 请求；Schema 不符精确到 `$.data.id`，后继 update skipped。两个窗口并发运行，取消 A 不影响 B，关闭 B 回收本地请求；显式 3 次重试策略下各只执行 1 次。实际写 JSON/JUnit 后检查可识别 Token/Secret 不存在。空保存集合显示可导出的配置错误；报告失败显示 internal_error/report，拒绝其它项目的 `.gorequest` 及 Git 元数据路径。

联合测试发现并最小修复两个 A01 问题（没有改 Core 安全语义）：

1. 成功 Prepare 后再次调用 Prepare 返回 typed nil `*preflightError`，Run 将它视作失败并 panic；现在成功缓存明确返回 nil。覆盖 Prepare→Prepare→Run。
2. 仅步骤脱敏不能覆盖数据行值、保存名称元数据及后续才发现的 Secret。新增 `schema_adapter_report.go` 做所有 Run 返回前的结果脱敏；Prepare 登记 password/token/secret 等敏感数据字段，原始值仍只用于私有执行行。现有 `core.MapSecrets` 实际不实现 SecretNames，显式适配该具体 provider 和可枚举 provider；不修改 B05 端口。新增用例检查返回 UI 结果、JSON、JUnit、取消结果元数据均不含绑定 Secret/数据密码。现有 `runner.go` 只增加三处最终结果脱敏调用，这是本卡真实闭环所需的安全修复。

Schema adapter 最终直接调用 A03 `SchemaValidatorPort.ValidateInstance`。严格解析多文档/重复键，字段类型及 JSON 兼容性检查，累计引用文件/节点与展开大小界限；没有重造实例校验器。Schema 参数 Headers 为新增 struct 字段：仓库内既有调用均使用 keyed literal，保持兼容；外部非 keyed 位置字面量需要相应增加字段。契约仍未全面冻结。

最终代码与验证证据见 [runner-ui-evidence.md](../specs/runner-ui-evidence.md)。第三步提交即包含本节的联合回归提交，最终哈希随会话交付；前两步分别为 `688b05a`、`355ac70`。

公共接线已临时应用进行生产 App.View 项目菜单测试及全仓检查，随后只逆向撤回自己的两行 patch。交付树不含公共文件改动，`git apply --check docs/task-results/A02-phase1-integration.patch` 可通过；集成人串行应用后，使用 `-tags a02_integration` 重新验证入口。本分支没有修改总账本/implementation-status、FIX06–09 文件、依赖、示例或其它 worktree，没有 merge/cherry-pick/push。

未验收：真实窗口/原生文件对话框/系统凭据/真实 OAuth/跨平台人工矩阵、大附件响应性；当前准备阶段在 UI 线程同步完成。后续：可视化断言/提取编辑、完整 Suite 编排/数据编辑、完整结果分页。OAuth/OIDC、本地槽上传/动态附件路径及第一步列出的 Schema 方言范围以明确拒绝方式处理，不能视为全量 A02 完成。

紧凑窗口布局验证：`TestRunnerPanelExportReachableAtCompactHeight` 在 1000×700 headless 视口检查运行、JSON/JUnit 导出按钮实际边界，修复原布局导出按钮落到窗口外的问题。最终面板根据视口缩放高度，头部取消/关闭和底部导出固定，中间内容滚动；该测试不代替真实窗口人工验收。

## 最终验证与接管

本机 darwin/arm64、Go 1.27.3；最终实现完成后：

- `go build ./...`：exit 0。
- `go vet ./...`：exit 0。
- `go test -race ./...`：exit 0；最后一次 desktop 87.120s，其余包通过或缓存。
- 临时应用两行公共 patch 后，`go test -race -tags a02_integration ./internal/desktop ./internal/runner -run 'TestA02|TestRunner|TestSchemaAdapter|TestPreparedRunner' -count=1 -timeout=60s`：exit 0（desktop 2.612s，runner 2.483s）。此命令包括最终紧凑布局、生产菜单入口、本地两行 HTTP、隔离/取消、报告及配置边界回归。
- 实施中也完整通过带接线的 `go test -race -tags a02_integration ./...`；最终交付树撤回公共接线后再次通过上述默认全仓门禁。
- `git diff --check` 与 `git apply --check docs/task-results/A02-phase1-integration.patch`：exit 0。公共文件无残留临时修改。

集成人应用随附 patch 即可接入生产入口，按 evidence 中命令复核并进行真实窗口/平台验收；本任务不合并、不推送、不自动扩展下一阶段，保持当前分支可接管。
