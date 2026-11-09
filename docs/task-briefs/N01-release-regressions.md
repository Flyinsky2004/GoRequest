# N01：清除四项既有测试失败并固定真实契约

负责人：CC-N01。分支 `cc/N01-release-regressions`；worktree `/private/tmp/gorequest-next5-20261109/regressions`。

先读共同约束 `next5-common-20261109.md`、`docs/acceptance.md`、`runner-core-contracts.md`、`identity-evidence.md`、`mock-evidence.md`。这是正式发布门禁修复，不是批量修改期望值。

## 文件归属

允许：`internal/desktop/runner_editor.go`、`runner_editor_test.go` 及专属回归测试，`internal/runner/editorview.go` 及专属测试，`internal/desktop/mock_rule_editor.go`、其测试，`internal/diagnostics/*` 中和策略预检相关的最小修正与测试。不改 Runner 启动/快照、Suite 新编辑器、Core 目标策略默认值、公共模型和网络执行器。

## 主代理已核对的根因线索与实施步骤

### 1. 断言描述长度与索引

`TestRunnerEditorDescribeAssertions` 的 YAML 原来含六项，随后又 append 一项非法 Schema，共七项；现断言却期待六项，并把第六项合法 Schema 当作 invalid。`DescribeAssertions` 本身保持输入输出一对一是正确契约。

- 重新核对 parser 对合法/非法 schema 的判定；测试应覆盖前四项 structured、duration 与合法 schema preserved、最后一项 invalid，而不是通过删输入或过滤节点变绿。
- 使用命名子用例或按索引完整断言七项；保留 Raw/Err/Structured 的检查，增加回建保留规则与实际 engine 解析一致性。
- 若实现另有问题，以契约修实现；不得让 DescribeAssertions 静默丢弃合法/非法节点。

### 2. Suite 提取规则替换丢注释

`runnerEditorPatchSuiteExtract` 当前删除整个旧 extract key/value，再追加新节点；yaml.v3 把 `# step comment stays` 附在被删节点上，所以实际会丢注释。这是需要修复的生产路径。

- 在原 key 位置替换 value，保留 key 和可保留注释；key/value/序列上的 Head/Line/FootComment 要明确迁移归属。
- 清空 extract 时删除字段，但将属于步骤或原字段的说明保留到稳定位置，不能覆盖相邻注释、重复附加或错归另一步骤。只删除已不适用的语义需报告，不得默默丢失。
- 再解析并通过 `runner.ParseSuite`/`ValidateStructure`；保持其他步骤、未知可保留节点、assert/actions/retry、稳定 step ID。
- 测试替换/清空/新增、两次重复保存、不同位置的注释、相邻步骤与错误来源；保留已有失败断言。节点 round-trip 不承诺字节级排版，修正文中不实“byte-faithful”描述。

### 3. Mock 复制 ID 建议

当前 helper 的契约是 base 空闲时直接用 base；否则 `base-copy`、`base-copy-2`…；现失败测试的 taken 只有 `x-copy/x-copy-2`，没有 `x`，却期望 `x-copy-3`。另一个用例又明确要求 free base 原样返回。

- 先核对真实复制调用如何收集项目内 taken（文件间全局唯一）；不要为了错误 fixture 无条件给 free base 加 copy。
- 修正测试的占用集合使其确实代表原规则存在；加覆盖跨文件占用、连续后缀、空闲 base 和保存重复 ID 的实际拒绝路径。
- 若发现真实调用没有纳入来源 ID 或同项目其他文件，修调用及集成回归。不要扩大到 Mock Runner 接线（N03 负责）。

### 4. 生产目标预检

`makeTestProject` 生成 GET；现用例把 GET 放到 prod，期待默认 confirm 策略禁止。Core 的默认契约是对生产变更方法要求确认，允许安全读取；仅名字含 charge 不能把 GET 变成写操作。当前 diagnostics 已调用 EvaluateTarget，并强制非交互、不确认。

- 对照 `core/identity_policy.go` 的 `IsSafeReadMethod`、`MergeTargetPolicy`、`EvaluateTarget` 确认以上结论。
- 正例应显式配置 POST/PUT/PATCH/DELETE 生产请求并断言执行器调用数为零；补 GET 在默认 confirm 下允许，deny 策略下 GET 也零调用。
- 缺失 Secret、配置错误、显式阻断主机、Interactive/Confirmed 传入 true 仍不能在批量采样自动授权；结果分类和错误脱敏应保持。
- 如发现实际门禁缺陷，只在 diagnostics 修，不能将全局生产 GET 改成一律拒绝，也不能弱化 HTTPS/凭据规则。

## 验收与交付

先复现四项失败，保存精简输出。完成后四项都要通过且测试意图更精确。执行受影响 desktop/runner/diagnostics 专项及 race；执行 build/vet 和不 skip 的全仓测试。只在真实全仓无失败时报告全绿。报告 `docs/task-results/N01.md` 区分测试 fixture 错误与生产 bug，给出每项证据、提交及限制。
