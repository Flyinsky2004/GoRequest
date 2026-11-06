# B05-UI 任务交付报告（2026-11-06）

- **任务 ID**：`B05-UI`（可复用方案管理、文件夹鉴权编辑、系统凭据录入/更新/删除、来源/缺失诊断、生产策略确认 UI）
- **工作目录 / 分支**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-B05-UI` / `cc/B05-identity-ui`
- **共同提交基线**：`4223cd48612ac5925a2451e391184b5551d35066`
- **提交**：`fa7583d`（`feat(identity): add native scheme, folder and credential management`）
- **本卡状态**：专属模块、独立测试、证据文档和接线 patch 已交付；**公共入口未直接接线、未勾选总卡**，由集成人应用 `B05-UI-integration.patch` 后统一验收。

## 1. 先核对实际代码，避免重复实现

已阅读 `AGENTS.md`、`docs/parallel-work-plan.md` 第 10/11 节、`docs/implementation-status.md` §4/4.1、`docs/task-results/integration-foundation-2026-11-06.md`、`docs/task-results/B05.md`、`docs/specs/identity-evidence.md`、`docs/acceptance.md` 与相关规格/决策。实查确认：

- ref/none/继承/API Key 保真往返、文件夹鉴权引擎、`ProjectScopeKey` / `LayeredSecrets` / `KeychainSecrets`、Redactor、目标/生产策略、B04 网络/代理/CA/mTLS UI 均已合入；本卡没有重复实现。
- 本轮公共 `core/model.go`、`project.go`、`validate.go`、`auth.go`、`desktop/{app.go,project_state.go,manager.go,nav.go,workspace.go,tab.go,settings.go,config.go,i18n.go,send_guard.go}`、`docs/parallel-work-plan.md`、`docs/implementation-status.md` 由集成人拥有；本分支**未直接修改**。唯一共享 core 变更以 patch 形式交出（`core/plan.go` 的首跳确认逻辑），并已临时应用验证后精确撤回。
- `go.mod` / `go.sum` 零改动；未新增第三方依赖。MyGo Native UI 仍是唯一产品界面，没有 WebView 主界面、没有独立产品 CLI。

## 2. 实际实现范围

### 2.1 纯 Go Core（`internal/core/identity_management.go`、`identity_confirm_adapter.go`）

- 可复用方案 CRUD：`auth/<id>.auth.yaml` 的 ID 校验、清单、载入（含 baseline）、新建、保真更新、删除。
- 删除方案必须做引用诊断：项目默认、文件夹、请求三层 `auth.ref` 全部列出；有引用时返回 `AuthSchemeInUseError`。确认删除时在同一 `ChangeSet` 内把引用原子改写为显式 `none`，不留下悬空引用；`ref` 永不展开写入项目。
- 方案/文件夹/请求的保真保存复用 `MergeAuthSchemeYAML` / `MergeCollectionYAML` / `MergeRequestYAML` / `MergeProjectFileYAML`、`Store.SaveChecked` 与 `ChangeSet.Apply`；保留注释、ID/名称/变量、`extensions` 与未修改字段。外改产生 `*ConflictError`，不覆盖磁盘。
- 文件夹鉴权：`SaveFolderAuthPreserving`、`FolderAuthImpact`（只列出真正继承该文件夹的请求）与脏缓冲/外改保护。
- 秘密来源与缺失诊断：`SecretReferences`、`IdentityDiagnostics` 汇总环境声明、方案、文件夹、请求中的 `{{secret.*}}` 来源；区分 `bound` / `missing` / `unavailable`，输出不含秘密值或编码变体。
- 生产确认：`TargetConfirmation` 精确绑定请求 ID、方法、环境、规范化 URL（含查询，忽略 fragment）；`OneShotConfirmGate` 只授权首跳一次；重定向逐跳仍由 `EvaluateRedirectHop` 以 `Confirmed=false` 重新检查，不能沿用宽泛授权。门闩以 `weak.Pointer[Plan]` + `runtime.AddCleanup` 保存，没有全局强引用 UI 对象。

### 2.2 Platform keychain 端口增量（`internal/platform/keychain_management.go`）

- 增加 1–2 方法小接口 `CredentialWriter` / `CredentialInspector` 和 `ManagedCredentials`（`Observe` / `List` / `Set` / `Delete`）。
- 复用 `ProjectScopeKey(projectID, root)` 的 `<slug>:<sha256>` 克隆隔离；测试通过 `NewManagedCredentials(fakeDriver, ...)` 注入内存假后端，**不写本机真实钥匙串**。
- 后端不可用返回错误，绝不降级写明文项目文件；状态只展示绑定情况，不回显值。

### 2.3 MyGo Native UI（`internal/desktop/identity_management*.go`）

- 管理弹窗四个分区：可复用方案、文件夹鉴权、系统凭据、来源/缺失诊断；中英双语独立消息表在专属文件，不改公共 `i18n.go`。
- 方案编辑器：新建/编辑/重载/删除，引用列表与确认提示；脏缓冲引用方案时阻断删除。
- 文件夹编辑器：继承/none/ref/内联 bearer/basic/apikey，显示受影响请求与外改/脏缓冲确认；只写 `collection.yaml`，请求文件保持不变。
- 凭据编辑器：名称、密码式输入（`.Password()`）、写入/更新、删除二次确认、绑定状态、来源、未绑定/不可用诊断；值不会写共享项目或诊断文本。
- 生产确认弹窗：显示准确请求、目标、方法、环境、原因与一次性说明；确认后只授权这一次发送，重定向重新检查。
- UI 状态全部在 `weak.Pointer[projectState]` 弱键表并随 projectState 回收，没有全局强引用 map 保存 UI 对象。
- i18n 表反射测试校验中英字段齐全且不是简单复制；无窗口 `ui.NewTester` 覆盖真实弹窗渲染、空闲帧预算与秘密零回显。

## 3. 文件清单

| 文件 | 说明 |
| --- | --- |
| `internal/core/identity_management.go` | 方案/文件夹 CRUD、影响/引用/秘密/诊断、一次性确认令牌与门闩 |
| `internal/core/identity_confirm_adapter.go` | 弱键 per-Plan 门闩表，避免直接改 `ExecuteOptions` 公共字段 |
| `internal/core/identity_management_test.go` | Core 成功/失败/取消/外改/隔离/边界与重定向不复用授权用例 |
| `internal/platform/keychain_management.go` | 可注入凭据管理端口 |
| `internal/platform/keychain_management_test.go` | 内存假后端生命周期、克隆隔离、不可用不降级、零明文落盘 |
| `internal/desktop/identity_management.go` | 状态、方案/文件夹/凭据/诊断控制器、目标预检与确认流程 |
| `internal/desktop/identity_management_i18n.go` | 专属中英消息表 |
| `internal/desktop/identity_management_ui.go` | 原生弹窗、菜单接线入口、生产确认弹窗 |
| `internal/desktop/identity_management_test.go` | 无窗口创建→引用→编辑→发送→保存保留 ref、文件夹继承、凭据缺失/失败/删除、诊断、空闲帧预算 |
| `docs/specs/identity-ui-evidence.md` | 接口/兼容性证据，不宣称未冻结格式稳定 |
| `docs/task-results/B05-UI-integration.patch` | 精确公共接线补丁 |
| `docs/task-results/B05-UI.md` | 本交付报告 |

未修改：`go.mod`/`go.sum`、公共 core model/project/validate/auth、公共 desktop 壳层、`docs/parallel-work-plan.md`、`docs/implementation-status.md`。`core/plan.go` 基线文件已精确还原，仅以 patch 增量提供。

## 4. 必要验证

本机 `darwin/arm64`、Go `1.27.3`。以下均在临时夹具、内存假钥匙串或本地 `httptest` 服务中执行，不访问真实远程仓库或系统钥匙串。

| 命令 | 结果 |
| --- | --- |
| `gofmt -w <本卡 .go 文件>` | 通过 |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过 |
| `go test -race ./...` | 通过（全包，exit 0；core 5.790s、desktop 56.943s、platform 1.447s，其余包通过） |
| 临时应用 `B05-UI-integration.patch` 后 `go build ./...`、`go vet ./...`、`go test -race ./...` | 通过；随后 `git apply -R` 精确撤回，`git apply --check` 再次通过 |
| 未应用 patch 的基线 `go build ./...`、`go vet ./...`、`go test -race ./...` | 通过；未接线时生产策略仍失败关闭，不静默放行 |

新增关键用例：

- Core：方案 CRUD 保留注释、重复创建/陈旧 baseline/非法 ID 拒绝；删除引用诊断与原子清除引用；文件夹保真保存、继承影响、外改冲突；秘密来源与缺失/不可用诊断不泄露值；确认令牌精确匹配、一次性、首跳与重定向隔离。
- Platform：内存假后端创建→更新→删除、克隆隔离、键值上限、不可用后端失败关闭、零明文落盘。
- Desktop：无窗口创建方案→请求引用→本地服务实发→仅改 URL→保存→重开仍保留 `ref`；文件夹继承与显式 none 不受影响；外改/脏缓冲保护；凭据缺失→写入→更新→删除→失败；方案删除确认与脏缓冲阻断；生产确认一次性/精确范围/重发重新预检；双语完整性、真实弹窗渲染、静止无动画帧唤醒、秘密值零回显。

## 5. 公共接线步骤（集成人）

1. 先确认本分支专属文件已进入集成分支；从共同基线检查 `git apply --check docs/task-results/B05-UI-integration.patch`。
2. 应用 patch：
   - `desktop/app.go`：`App.View` 末尾调用 `a.IdentityManagementModals(c, t)`；`send` 在构建 plan 前调用 `a.prepareConfirmedSend(tab)`，有 pending 确认弹窗时不发网；构建后调用 `a.attachConfirmedNetworkOptions(tab, plan, confirmed)`。
   - `desktop/workspace.go`：项目菜单加入 `a.BuildIdentityManagementMenuItems(mm, tab)`。
   - `desktop/tree_actions.go`：文件夹右键菜单加入 `a.BuildIdentityFolderMenuItem(mm, n.RelPath)`。
   - `core/plan.go`：无需给 `ExecuteOptions` 增加字段；`ExecuteWithOptions` 只让首跳消费 `PlanConfirmGate(plan)`，每个重定向仍以 `Confirmed=false` 重新评估策略。
3. **不要**设置 `tabNetStateFor(tab).policyConfirmed = true` 作为宽泛授权；这会让重定向沿用确认。只使用 `prepareConfirmedSend` 返回的一次性令牌和 `SetPlanConfirmGate`。
4. 双语消息如需统一合入公共 `i18n.go`，把 `identityManagementMessages` 平移即可；本次不改公共翻译表也能工作。
5. 集成后重跑 `go build ./...`、`go vet ./...`、`go test -race ./...`，并人工走查菜单、文件夹右键、弹窗与确认发送。若其他并行卡已改动同一行，按实际代码合并冲突，不机械覆盖。

## 6. 限制与未做的人工/平台项

- **三平台真机未做**：macOS 真实 `Security.framework` 登录钥匙串写入/锁定弹窗、Windows Credential Manager、Linux libsecret/GNOME Keyring/KWallet 与无 D-Bus 最小系统错误路径均未在真机验收。自动化测试只使用内存假后端和编译期 `MyGoKeychainDriver` 接口核对，不污染宿主钥匙串。
- **真实 GPU 窗口人工交互未做**：键盘焦点、输入法、高 DPI、三平台菜单/弹窗视觉排版、真实系统保存对话框没有人工走查；本卡用 `ui.NewTester` 驱动实际 MyGo 控件并验证空闲帧预算。
- 凭据清单以项目声明的 `{{secret.*}}` 名称为来源；OS 后端不提供枚举全部键的可移植接口，因此未声明的秘密不会出现在管理列表。这是有意的边界，不读取或展示已保存的值。
- 本卡不增加 OAuth/OIDC token 引擎或表单，不修改 A04 模块；不重复 B04 网络/代理/CA/mTLS UI；不实现 Runner、历史保留策略或跨协议鉴权。
- 未应用集成 patch 前，公共菜单/发送路径没有本卡 UI 入口；生产策略仍按既有行为明确要求确认（不会静默发送）。这不是功能完成，需集成人接线后再验收。
- 不自行勾选总账本或实现状态；整张正式版卡仍有剩余项。

## 7. 交接

- 分支 `cc/B05-identity-ui` 只提交本卡文件，不 merge/cherry-pick/push，不操作 main 或其他 worktree，不改共享 `.git` 配置，不 reset/clean。
- 会话保持可接管；集成人按 patch 接线后可继续人工验收与总卡勾选。
