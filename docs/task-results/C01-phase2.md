# C01-phase2 交付报告：Mock 规则与场景编辑器

- **任务 ID**：`C01-phase2`（主卡 `C01` 第二阶段）
- **worktree**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-C01-editor`
- **分支**：`vibe/C01-rule-editor`
- **共同基线**：`b91ba32`（`docs: scope five independent Vibe worktree tasks`）
- **负责人**：Vibe（Herdr 派出，本 worktree 独立 agent）
- **状态**：代码实现 + 静态核对完成，本地 commit；**按本批约束未执行 go build / go vet / go test，未启动任何真实监听**

---

## 1. 实际提交

| 提交 | 内容 |
| --- | --- |
| `1371094` | `feat(mock): C01 phase 2 structured rule & scenario editor` —— mockserver 深拷贝助手 + 桌面结构化规则/场景编辑器 + 双语词条 + 待执行回归用例（8 文件，+3356/−37） |
| 本报告与 mock-evidence.md §6 所在提交 | `docs(mock): add C01 phase 2 editor contract evidence and delivery report`（分支顶端，哈希见 `git log -1`；自引用哈希无法写入本文档本身） |

分支 `vibe/C01-rule-editor`，基于共同基线 `b91ba32`；未 push、未 merge、未删分支。

## 2. 改动文件（仅本卡专属范围）

**新增**
- `internal/mockserver/rule_model.go` —— `CloneMockRule` / `CloneMockFile` 深拷贝助手（含指针谓词、`In` 列表、`jsonEquals`/`jsonFields`、per-variant headers/`delayRangeMs`、CORS、variables、Source 元数据）。
- `internal/desktop/mock_rule_editor.go` —— 编辑器模型与动作：draft 构建（克隆文件 + 打开时 digest 基线）、行模型（单谓词行 + locked 保留行）、字段级校验（id 字符集/项目内唯一、method、path 模板、status/delay 区间、行谓词、in/regex 值）、候选文件组装（整文件 Encode 校验 + 以编码字节 digest 记录）、内存编译预览（跨文件 id 冲突 + 同优先级歧义，走引擎 `CompileMockFiles`）、显式保存（`SaveMockFile` + 打开时基线 digest，外改 → `*core.ConflictError` → 冲突横幅，绝不静默覆盖）、外改两条出路（继续编辑 / 显式丢弃并重载）、规则删除（先重载文件取新鲜 digest 作基线，拒绝删最后一条规则/正在编辑的规则）。
- `internal/desktop/mock_rule_editor_ui.go` —— 编辑器渲染：分组表单（Identity / Match / Response / Variants / 校验与冲突预览 / 保存-取消），锁定行只读徽标，逐字段错误就近展示，单主操作（Save=PrimaryButton），取消与面板关闭对脏草稿走显式丢弃确认（沿用 env editor 的 open/visible/confirmDiscard 模式）。
- `internal/desktop/mock_rule_editor_test.go` —— 回归用例（见 §5，未运行）。
- `internal/mockserver/rule_model_test.go` —— 深拷贝独立性回归用例（未运行）。

**修改**
- `internal/desktop/mock.go` —— `mockState` 增加 `visible`/`editor`/`deletingRuleKey`；项目切换清空编辑器草稿；新增 `ApplyMockSnapshotToServer`（显式把面板快照应用到运行中服务，仅无冲突时 swap，不启停监听）。原有的启停/场景切换/从请求或 OpenAPI 生成规则逻辑未动。
- `internal/desktop/mock_ui.go` —— Modal 改双布尔挂载（Escape/点外 edición 脏时升级为丢弃确认）；规则页改为「按文件分组的规则列表 + 每行 编辑/复制/删除（两步确认）」，文件头有该文件的“新建规则”入口；配置条增加“运行中服务仍使用旧快照”横幅 + 显式 Apply 按钮。
- `internal/desktop/mock_i18n.go` —— 新增 `mockEditEN/ZH`、`mockListEN/ZH` 词条组（含位置参数 `%[1]s` Go fmt 语义），`mockT` 查找扩为四组目录；全部 71 键 EN/ZH 一一对应。
- `docs/specs/mock-evidence.md` —— 追加 §6（本阶段编辑器契约证据与测试债）。

**未触碰**：公共文件（`core/model.go`、`project.go`、`validate.go`、`plan.go`，`desktop/app.go`、`manager.go`、`nav.go`、`workspace.go`、`tab.go`、`project_state.go`、`config.go`、`settings.go`、`i18n.go`）、`go.mod/go.sum`、总账本、`implementation-status.md`、其他卡的模块（runner/performance/docgen 等）均零改动——因此**本卡不需要 integration patch**（阶段 1 的 `MockModals`/`BuildMockProjectMenuItems` 挂载点已存在并被复用）。

## 3. 功能与公共接线

- 入口全部复用阶段 1 已接线的 `App.View → MockModals` 与项目菜单项，无新公共挂载点。
- 编辑器仅消费引擎已支持字段与既有 API：`LoadProjectMocks`/`LoadMockFile`/`SaveMockFile`/`EncodeMockFile`/`CompileMockFiles`/`ValidateMockFileStructure`/`Server.CurrentSnapshot`；未加引擎字段、未猜复杂 Schema 生成、未改 matcher/compile/access_log/server/suite 语义。
- 运行中快照一致性：保存后按现有 Reload 语义重编译（无冲突才 swap）；服务当前快照摘要与面板快照不一致时显示横幅，由用户显式 Apply；受控 Suite 运行使用冻结快照，不受面板编辑影响。
- 安全边界沿用：秘密不写入共享明文（引擎校验兜底，编辑器对凭据头 raw 值提前报错提示）、访问日志脱敏逻辑未动、LAN 绑定仍显式授权且警告未动、未匹配请求不转发公网。

## 4. 已执行的静态核对（非编译验证）

- `gofmt -l`（本卡全部 8 个文件）：无输出，格式 Clean。
- `git diff --check`：无空白错误。
- 符号/引用静态比对（脚本 + 人工）：UI↔逻辑字段名、函数定义↔调用点、i18n 键覆盖（使用 ⊆ 定义，EN/ZH 各出现两次）、fmt 动词与实参数量 Positional 一致性（EN/ZH 计数均为 0 处不匹配）。
- 引擎契约核对：`BodyMatcher` 无 `notEquals`（已按引擎真实字段建模）；`ValueMatcher/KeyValueMatcher/RuleResponse/DelayRange/CORSConfig/KV` 字段逐一对照 `contracts.go`；`SaveChecked` 冲突语义按 `core/store.go` 实现建模。
- Git：`git status` 仅含本卡文件；共同基线 `b91ba32` 未偏移（无 merge/rebase）。

**未执行的验证（明确声明，不得视为通过）**：`go build` / `go vet` / `go test` / 真实窗口人工验收 / 三平台验收 / 组合构建。类型正确性靠人工比对 + mygo API 源码（v0.7.4 模块缓存）核对，存在残余风险，以集成人后续 §5 门禁为准。

## 5. 待执行用例（已写好、未运行）

```bash
# mockserver 深拷贝独立性
go test -race ./internal/mockserver/ -run 'TestCloneMock'

# 桌面编辑器回归：往返保留未知字段、字段级校验、同优先级冲突预览与消解、
# 外部修改冲突保护、删除安全（最后一条/编辑中/常规删除）
go test -race ./internal/desktop -run 'TestMockRuleEditor|TestMockDeleteRule|TestMockParsePathTemplate|TestMockEditorSuggestions'

# 阶段 1 回归（确认未破坏既有面板行为）
go test -race ./internal/desktop -run 'TestMock'
```

## 6. 限制与边界

- 本批约束“先不执行测试”：所有“通过”仅指静态核对，**不含**编译/类型检查/运行验证。
- 未做真实窗口交互、深浅色对比、键盘焦点走向与中文输入法验收；桌面 UI 仅为静态代码。
- 编辑 `.mock.yaml` 会按引擎规范重排（标准 2 空格 YAML、map 键序归一）；注释/排版保真属 B01 范畴，本卡未处理（保存为结构体再序列化，与阶段 1 行为一致）。
- 变体行仅暴露 status/Content-Type/body/delayMs；headers/delayRangeMs 以锁定数据保留（显示“已保留”徽标），编辑它们需直接改 YAML——防止 UI 诱增表达能力。
- 复杂 Schema 示例生成、A02 Runner 主面板联动（suite/mock 合流接线）按账本留后续，不在本卡。
- `body_state.go`、`file_sync_i18n.go`、`oauth_test.go`、`request_form_test.go` 在基线中即已不满足 gofmt（非本卡改动，未触碰）。
