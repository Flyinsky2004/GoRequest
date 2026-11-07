# A02 phase 1 交付记录（实施中）

负责人：Codex；worktree `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A02`；分支 `codex/A02-runner-first-loop`；共同基线 `3d51b99c29d9f583fcec8339e3ab77dc7f121643`。本记录不勾选 A02 整卡。

## 第一步：Schema 联合接线

新增 `runner.NewProjectSchemaAdapter(project)`，实现 A01 `SchemaValidator` / `SchemaPreflighter`，实例验证调用 A03 `core.ValidateParsedSchema`，无 core→runner 依赖。预检独立检查配置，不验证虚拟实例；递归遍历所有已支持 Schema 分支，包括未被实例触及的 properties/$defs/组合分支，离线解析引用并冻结到内存。最多 128 文件、合计 8 MiB、48 层、10000 Schema 节点；文件经项目 Store 读取，拒绝远程/越界/符号链接路径、循环、缺引用、缺 Schema、未知关键字或不支持的配置。运行不重读 Schema 文件。

引用规则（初始版本，未冻结）：

- `schema: {ref: schemas/user.yaml}` 或 `file.yaml#/components/schemas/User`；路径相对当前项目 `.gorequest/`，嵌套 `$ref` 相对所在文件，fragment 仅 RFC6901 pointer。
- `schema: {operation: getUser}` 选择唯一已保存 `extensions.openapi.operationId`，不可同时指定 ref。响应选择顺序：精确状态、`nXX`、default；无匹配为断言失败。
- 第一阶段 Operation 仅支持恰好一个 `application/json` body、没有 response header contracts 的响应定义；所有声明响应都须有 Schema。实际 Content-Type 不匹配为断言失败；缺 Content-Type 沿用 JSON 默认。其他媒体/头契约明确预检拒绝，不宣称完整 OpenAPI 响应覆盖。
- 支持 Schema：boolean、type、properties/$defs/definitions、items/prefixItems、additionalProperties、allOf/anyOf/oneOf/not、required、pattern、长度/数量/数值界限、multipleOf、enum/const、uniqueItems 及常规注解；仅接受 2020-12 `$schema`。不支持 `$id`/anchor/dynamicRef、递归引用、format、conditional/dependent/patternProperties/contains/unevaluated；未知关键字失败。`$ref` 与显式 allOf 同级暂拒绝。

A01 端口最小兼容增量：`SchemaRef.Headers [][2]string`，现有实现不受影响，evalSchema 传入实际响应头。Schema 断言报告不携带原始 ref；adapter 不输出 schema 常量、actual 或引擎原始 message，仅约束名与实例路径。坏配置诊断固定文字，避免预检前泄露 Schema 内容。

验证：`go test -race ./internal/runner` 通过；新增测试覆盖离线多文件 fragment、冻结后文件变化、精确 `$.data.id`、未知/缺失/循环/远程/越界引用、未触及分支坏 pattern、坏类型、零 Executor 调用、Operation 状态不匹配与无 Schema 拒绝。

第二步桌面 Runner、第三步联合回归尚在实施，后续提交补齐记录。

## 第二步：最小原生桌面 Runner

新增 `desktop/runner*.go`，双语原生项目菜单入口和 Modal；菜单接线只需随附 `A02-phase1-integration.patch` 两处调用，不添加公共状态字段。状态由 weak App key 关联，不强引用 App/Window/Tab/projectState；关闭窗口取消，后台只通过发起窗口 Update 投递，同窗口 generation 拒绝旧结果。项目菜单→集合/Suite Runner→选择集合或现有 `*.suite.yaml/yml`→明确环境→运行保存快照→逐次尝试事件/最终行步骤/分类汇总→显式导出 JSON/JUnit。根集合选项 `collections` 代表全部保存请求，私有合成 CollectionEntry 后仍交 `CollectionSuite` 构建确定顺序。现有 Suite 通过 `LoadSuite` 解析，无源码编辑冒充可视化。

开始时在 UI 线程 `OpenProject` 获取独占保存对象，复制 B04 ExecuteOptions，获取 B05 SecretProvider 内容，Prepare 冻结数据集、Schema、固定项目附件和 TLS PEM，然后密封 provider 再进入后台。后台不读取任何 UI 对象；本次环境、Cookie 行隔离、step outputs、并发各 1；不会随项目切换、编辑或环境变化重读。脏编辑提示明确。事件队列和可见列表各最多 256，最多一个待处理 Update；完整 Core 结果仍在内存并用于报告。关闭/Escape/点击外部取消运行，无空闲 ticker 或持续刷新。

补齐实际发现的 A01 闭环缺口：Prepare 拒绝尚未联合的 OAuth/OIDC（含有效继承方案），不会无鉴权发送；已知静态目标在 dry-build 后调用现有 EvaluateTarget，非交互/未确认，仍由执行 Core 对每个实际目标和重定向重新检查。预检开始即注册可枚举 Secret，Schema 内容摘要加入快照（可选 `SchemaSnapshotDigest()`，原 Schema 接口保持兼容）。没有更改 Core 安全语义。

JSON/JUnit 导出支持输入绝对本地路径及原生 Save 路径选择；实际导出按钮明确写文件，0600 临时文件原子替换，失败显示 `internal_error / report`；拒绝项目目录内及经父级 symlink 指向项目的路径，不写共享历史/运行状态。报告沿用 Runner 编码器和脱敏结果。

阶段限制：本地槽位上传和动态附件路径在发网前明确拒绝；固定项目附件最多合计 64 MiB，TLS 每项最多 8 MiB。OAuth/OIDC 不在此阶段适配。快照准备有界但在 UI 线程同步进行，大附件可能短暂阻塞，后续可将纯文件读取移出 UI。可视化断言/提取编辑器、完整 Suite 编排、数据编辑 UI、最终完整结果分页/搜索均后续；当前实时/最终列表有界，完整结果看显式导出。保存 Suite 的发现规则为 `.suite.yaml/.suite.yml`。

本步验证：已临时应用自己的两行公共 patch；`go test -race ./internal/desktop ./internal/runner -run 'TestRunner|TestSchemaAdapter|Test.*Production'` 通过，覆盖原生控件 headless 渲染、脏编辑提示、10000 事件仅一个待处理回调/256 事件、文件快照密封、导出失败可见及项目路径/symlink 拒绝。此处 headless 不是人工窗口验收。第三步继续补充实际本地 HTTP 联合回归及全仓门禁。
