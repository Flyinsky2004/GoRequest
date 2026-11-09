# N02：Suite 可视化编排与数据源配置首阶段（A02-phase3）

负责人 CC-N02；分支 `cc/N02-suite-editor`；worktree `/private/tmp/gorequest-next5-20261109/suite-editor`。

目标：用户在桌面创建、打开、修改一个真实 `.suite.yaml`，选择跨集合请求、依赖和 CSV/JSON 数据源，保存后由现有 Runner 执行。不能只交一个无保存行为的表单。第一阶段只配置已有数据文件并预览，不做电子表格式数据内容编辑；报告保留该后续范围。

## 先读与文件归属

读共同约束、`desktop-test-automation.md`、`runner-core-contracts.md`，实际 `runner/contracts.go/parse.go/prepare.go/dataset.go`、`desktop/runner.go/runner_ui.go/runner_editor.go`、`core/store.go/changeset.go`。

拥有新包 `internal/suiteedit/`、新文件 `internal/desktop/suite_editor*.go`、专属测试/中英词条/报告。不能改 `runner_editor*`（N01）、`runner.go/runner_snapshot.go`（N03）、`runner_ui.go`（公共 patch）、Runner 格式/解析/执行行为。新包只能依赖 Core/Runner/标准库/yaml，不能依赖 GUI。

## 具体方案

1. 新建纯 Go Draft/Load/Validate/Save 服务，使用现有 Suite/Step/DatasetSpec，不引入第二种 Suite 格式。打开用 ReadBounded+真实 digest，严格 ParseSuite；未知或不支持字段保持显式错误，不能清洗后保存。
2. UI 左侧有已保存 Suite 列表和新建入口；右侧基础字段（新建 ID、名称、onFailure）、步骤列表、选中步骤表单、数据源配置与错误区、保存/取消。小窗口可滚动，深浅色、中英、键盘可达。返回或关闭有脏草稿时确认放弃；保存失败保留草稿。
3. 新建默认写 `suites/<安全slug>.suite.yaml`；要求不覆盖已有目标、不允许绝对路径/`..`/反斜杠逃逸；ID 非空且步骤 ID 唯一。新建前可改 ID，已有步骤 ID 首阶段只读，避免重命名破坏 needs 与 `{{steps.ID...}}`。
4. 每步选择已保存的 HTTP 请求，展示名称/方法/相对路径并保存稳定请求 ID；保留原文件已有的 path 引用，不打开后无故重写为 ID。WS 和未来 GraphQL 首阶段不可选择为“已支持自动化”。可增删步骤、用上下按钮排序，稳定 UI row ID 不随索引串行。删除有依赖或被模板引用的步骤要阻止并解释，不自动删依赖或替换字符串。
5. needs 用现有 step ID 多选/列表；阻止自依赖、重复、缺失、循环；错误精确到步骤。`ValidateStructure` 是事实来源，不在 UI 发明另一套 DAG 判定。重排不能改变依赖或断言/提取/actions/retry。
6. 保留并展示已有 assert/extract/before/after/retry/variables 的摘要/只读说明；本阶段不另造断言编辑器。与原断言/提取编辑入口可互相导航，但不丢未保存草稿、不静默改磁盘。编辑顺序隔离靠 digest 冲突保护。
7. 数据源配置：无数据/csv/json、项目相对 file、maxRows/maxBytes、CSV delimiter；其余 Fields/Types 原样保留。用 Store 限域读取，拒绝 symlink/越界/无效类型和不存在文件。预览最多 20 行/256 KiB，显式说明预览截断与真实执行上限；大文件不能 UI 线程全量解码。复用 Runner 数据解析可行 API；如需增加导出端口，只交最小 patch，不复制解析语义。预览敏感列及 Secret 模板不得显示实际 Secret，不能读取 SecretProvider。
8. 保存以原 YAML AST 更新有编辑的字段；原 step 节点随稳定身份重排，保留注释/未知未编辑内容。避免把 Suite 结构体整体 marshal 覆盖所有节点。最终 ParseSuite+ValidateStructure；新建 SaveChecked 空基线，编辑原 digest。外部修改后报冲突且保留草稿，不能刷新 digest 后自动覆盖。
9. 成功保存后刷新原 Runner 的已保存 Suite 选择且选中新/编辑项；不重开项目、不清空原请求标签、响应或环境。交 `N02-integration.patch`：`RunnerModals` 的独立新模态调用、Runner 内入口/刷新选择，必要关闭生命周期调用。N03 也会给 runner_ui patch，必须是小片段，不复制完整文件。

## 验收用例

- 临时项目创建跨集合两步 Suite→保存→重开一致→实际 Runner 跑通本地服务；JSON/CSV 两行分别运行，数据行隔离。可以调用现有纯 Go Runner 验证保存产物，不改 N03 的启动路径。
- 添加/重排/删除不影响未改 assert/extract/actions/retry/注释；有 needs/template 引用的删除被阻止；自依赖/环/重复 ID/缺请求/非法路径/损坏数据保存有错误。
- 外部写入/删除后保存拒绝覆盖，失败和取消保留输入；无操作打开关闭不写盘；两个项目和多个编辑器不串草稿；headless 真实点击、键盘/滚动/保存/放弃/取消至少覆盖关键流程。
- build/vet、suiteedit 与 SuiteEditor 专项、相关包 race；全仓普通测试按共同规则记录。报告 N02 的完整编排首阶段边界，不勾整张 A02。
