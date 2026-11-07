# FIX09 密钥声明 → 实际值录入 → 绑定状态 → 请求使用闭环

- 任务 ID：FIX09
- 分支：`cc/FIX09-secret-value-entry`
- Worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX09`
- 共同基线：`a20960beffbc63880eb9e74214ec2675523af6bf`（main，已含 FIX01–05）
- 状态：实现完成，必要自动化门禁通过；**真实窗口 / 三平台实机验收未做**（见「验证边界」）；整卡未自行勾选，交集成人。
- 用户反馈：创建「本地密钥名称」之后找不到填实际值的地方，无法请求。

## 1. 目标与现状核对

基线已有能力（不重做）：

- ADR-010 已确定 Secret 外部化：项目/环境只存 `secrets:` 名称清单与 `{{secret.<name>}}` 引用；真实值由 `SecretProvider`（内存覆盖 → 系统凭据 → 环境变量）提供；零明文文件回退。
- `platform.ManagedCredentials`（`internal/platform/keychain_management.go`）已提供项目隔离的 `Set/Delete/List/Observe/ValidateName`，按 `<projectSlug>:sha256(规范化项目根)[:16]` 隔离；`identityKeychainDriverFactory` 可注入假后端；`InspectSecret` 只返回非敏感状态。
- `desktop/identity_management*.go` 已有「项目菜单 → 系统凭据」页：名称/密码值/保存、绑定状态、删除确认、后端失败提示。
- 发送链路 `buildTrackedTabPlan` → `core.BuildTrackedPlanWithOptions` 已通过 `a.mgrSecrets()` 解析 `{{secret.name}}`（`ResolveVars` 的保留命名空间，不回退同名普通变量）。
- `App.View` 已在基线里调用 `a.envEditorModal`（app.go:194）与 `a.IdentityManagementModals`（app.go:205）。

**缺口**：环境编辑器里「密钥名称」只是声明行（`desktop/environment_editor.go` 的 `envSecretRow` 只有名称输入框 + 删除 ✕），旁边没有任何「去哪里填值」的线索；用户必须自己猜到去项目菜单找「系统凭据」页，且两处名称没有联动预填。声明 → 填值 → 绑定状态 → 请求这条链在 UI 上是断开的。

## 2. 实现（全部在本卡归属文件内）

| 文件 | 改动 |
| --- | --- |
| `internal/desktop/secret_entry.go`（新增） | `secretEntryDialog` 状态（按 `weak.Pointer[projectState]` 隔离，随项目窗口释放）、`secretEntryFor`、`secretEntryActive`、开关/保存/删除控制器、`secretStatusFromManaged`（只取非敏感状态）、`secretEntryModal` 单模态视图、`secretEntryStatus` 状态徽标。 |
| `internal/desktop/environment_editor.go` | 单模态让位（`envEditorModal` 在值录入对话框打开时跳过本帧渲染，**不关闭/不丢弃草稿**）；密钥声明区加可操作提示；`envSecretRow` 改为按 App 渲染，每个已声明名称显示「已绑定 / 未设置 / 后端不可用」徽标 + 「设置值 / 更新本机值」按钮；`refreshEnvSecretStatuses` 用一次 `ManagedCredentials.List(names)` 缓存非敏感状态。 |
| `internal/desktop/identity_management_ui.go` | `IdentityManagementModals` 增加「对话框打开时抑制管理模态 backdrop」+ 调用 `secretEntryModal`（无需改 `app.go`）；`renderCredentialRow` 每行加「设置/更新值」按钮，打开同一个预填名称的对话框。 |
| `internal/desktop/identity_management.go` | `saveCredential` / `confirmDeleteCredential` 成功后递增共享 `generation`，让环境编辑器徽标在系统凭据页写入后也刷新（两个入口共用同一项目隔离 `ManagedCredentials`）。 |
| `internal/desktop/identity_management_i18n.go` | 新增 `secretEntryMessages` 中/英双语字段与目录（不动 `i18n.go`）；`secretEntryMessagesFor(lang)` 复用 `identityLangCode()`。 |
| `internal/desktop/secret_entry_test.go`（新增） | 7 个专属回归（见第 4 节）。 |

### 关键设计

- **可发现入口**：环境编辑器每个已声明名称旁直接出现「设置值 / 更新本机值」按钮（点击 → 预填同名、名称锁定的专注小弹窗）；系统凭据页每行也加了同款按钮。用户不需要先读文档再去找菜单。
- **真实值只用密码输入**：`ui.TextInput(...).Password()` 每帧应用，值不回显为 `ui.Text` 标签；保存/失败/取消/Esc/关闭所有退出路径都清空 `d.value`；从不调用 `Get` 把值读回界面；不写项目 yaml、不写普通变量、不写 config、不写日志/导出。项目文件里只有名称和 `{{secret.name}}`。
- **绑定状态**：`已绑定（后端）/ 未设置 / 后端不可用（后端）` 三态，来自 `ManagedCredentials.List` 的非敏感 `State/Backend/Detail`。后端不可用/锁定保存返回明确错误，`core.ConfigError` 原样透出，**无明文降级**；状态同时展示在三处（环境行、专注弹窗、系统凭据页）。
- **引用与值来源说明**：弹窗显式显示 `{{secret.<name>}}` 引用、「项目文件只保存名称与引用，真实值只写入本机系统凭据，发送时读取」以及 ADR-010 的不可用即报错说明；普通变量 `value` 完全不参与，兼容性不变（`build()` 逐字保留）。
- **单模态、不叠层**：`envEditorModal` 与 `identityManagementModal` 在值对话框打开时都让位；对话框经 `IdentityManagementModals` 渲染（`App.View` 基线已挂）。未保存环境草稿、键盘焦点归属、当前项目均不改动；关闭对话框后草稿原样回来。`secretEntryDialog` 按 `weak` 键绑定 `projectState`，切项目不会串状态。
- **零公共文件接线**：`app/manager/nav/workspace/tab/config/i18n.go/go.mod/go.sum` 全部未改。见 `FIX09-integration.patch`（明确的零 hunk 记录）。
- **平台端口复用**：只调用现有 `ManagedCredentials`；未改 Core 命名/解析、`Keychain` 隔离策略、`SecretProvider` 或依赖。

## 3. 验证命令与真实结果

本机 darwin/arm64，Go 1.27.3，从 `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX09` 执行：

| 命令 | 结果 |
| --- | --- |
| `go build ./...` | 通过（无输出） |
| `go vet ./...` | 通过（无输出） |
| `go test -race ./...` | 全部通过：`internal/desktop` 79.9s，其余 8 包 ok / no test files；无 FAIL |
| `go test ./internal/desktop/ -run 'TestSecretEntry' -count=1 -v` | 7/7 PASS（0.75s） |

门禁日志：`/tmp/fix09_build.log`、`/tmp/fix09_vet.log`、`/tmp/fix09_race.log`（本机临时文件，不入 Git）。

### 新增回归用例（`secret_entry_test.go`，全部使用临时目录 + `withIdentityFakeBackend` 假系统凭据 + `httptest` 回环，不写真实钥匙串、不打真实 API）

1. `TestSecretEntryMessagesCoverBothLanguages` — 反射校验新增双语字段 zh/en 均非空且确有翻译。
2. `TestSecretEntryEnvDeclareSetSendReopen` — **完整用户闭环**：写 `environments/dev.yaml` 声明 `apiToken` → 渲染环境编辑器点行内「设置值」→ 专注弹窗里向密码框 `tt.Type` 真实值 → 点「设置值」保存 → 断言假系统凭据里按项目作用域存了值、临时输入已清空、绑定状态为已绑定 → 构造 `Authorization: Bearer {{secret.apiToken}}` 请求经 `buildTrackedTabPlan` + `core.Execute` 发到 `httptest`，断言服务端收到精确值 → 重开显示「更新本机值」且不回显值 → 系统凭据页同样显示已绑定 → 普通变量 `token` 的值逐字保留、项目文件零真实值。
3. `TestSecretEntryCredentialPageEntryAndSharedStatus` — 系统凭据页每行入口可点击并打开同一预填弹窗（单 overlay）；用该页**原有**表单保存的值进入同一隔离存储，环境编辑器徽标随之刷新。
4. `TestSecretEntryUpdateDeleteAndErrors` — 空值/无效名称/锁定后端（明确错误、无明文降级、状态为不可用）、创建→更新→删除确认、后端错误信息不泄值、共享项目文件零真实值。
5. `TestSecretEntryCloneIsolation` — 同 ID、不同根目录的两个克隆作用域不同，A 的值 B 看不到；对话框状态按项目隔离。
6. `TestSecretEntryPreservesEnvDraftAndSingleOverlay` — 对话框打开时只渲染一个 overlay（精确计数 1），未保存环境草稿签名不变，切到另一项目不串草稿/对话框，关闭清空临时值且草稿完好。
7. `TestSecretEntryPasswordNotEchoed` — 真实值不出现在任何 `ui.Text` 文本（弹窗与环境编辑器）。

## 4. 验证边界（如实保留，不虚报）

- 全部为**无窗口自动化**（`ui.NewTester` 驱动真实 `App.View` 视图与点击/输入）+ `-race`；**未做真实窗口人工点击验收**，未做 macOS/Windows/Linux 三平台实机验收，未验证真实系统钥匙串（Keychain / Credential Manager / Secret Service）的锁定/权限弹窗行为。测试一律用可注入的假后端与 `httptest` 回环。
- 未自动测试真实外部 API；发送验证只到本机 `httptest` 服务器。
- 深色/浅色主题、窄窗口下新按钮与徽标的观感，以及系统凭据真实后端不可用时的原生错误文案，均需人工确认。
- 未改 `docs/parallel-work-plan.md` 与 `docs/implementation-status.md`；整卡不自行勾选，待集成与人工验收后由集成人处理。

## 5. 集成步骤（给集成人）

1. 与 FIX06（settings.go）/ FIX07（uikit/response.go）/ FIX08（network_ui.go）无文件重叠；本卡只碰上表 5 个源码 + 新测试 + 两份交付文档。
2. **无需公共文件接线**：`app.go:194/205`、`workspace.go:87` 的既有挂载已足够；详见 `FIX09-integration.patch`（零 hunk 记录，含合并注意点）。
3. 若其他卡同时改了 `IdentityManagementModals`，保留三行：`identityManagementModal`（用 `!a.secretEntryActive(a.ps)` 守卫）、`productionPolicyModal`、`secretEntryModal`。若其他卡改了 `envEditorBody` 密钥区，保留本卡的行内徽标 + 按钮。
4. 请勿用 `IdentityManagementView`（测试专用包装，会在 `a.View` 之后再渲染一次模态）做精确 overlay 计数断言；用 `a.View`。
5. 集成后建议再跑一次 `go build ./...`、`go vet ./...`、`go test -race ./...`，并补真实窗口人工验收（环境编辑器里点「设置值」→ 真钥匙串存取 → 发请求）。

## 6. 本分支本地 commit 范围（用户已授权，不 push/merge/reset/clean）

- `internal/desktop/secret_entry.go`（新增）
- `internal/desktop/secret_entry_test.go`（新增）
- `internal/desktop/environment_editor.go`
- `internal/desktop/identity_management.go`
- `internal/desktop/identity_management_ui.go`
- `internal/desktop/identity_management_i18n.go`
- `docs/task-results/FIX09.md`（本文件）
- `docs/task-results/FIX09-integration.patch`

主目录示例环境 / `__debug_bin*` dirty 文件未读改、未提交、未删除；未操作 main、他人 worktree/agent、共享 Git 配置。
