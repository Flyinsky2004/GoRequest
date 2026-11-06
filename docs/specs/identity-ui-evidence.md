# B05-UI 身份管理 UI 接口证据（2026-11-06）

本文件只记录本卡实际核对和新增的**小接口**，不冻结任何新的公开存储格式。基线 `4223cd4`，实际代码见本分支 `internal/core/identity_management*.go`、`internal/desktop/identity_management*.go`、`internal/platform/keychain_management.go`。

## 1. 先核对的现有能力（避免重复实现）

| 能力 | 实查位置 | 结论 |
| --- | --- | --- |
| `ref` / `none` / 继承 / API Key 往返 | `internal/desktop/tab.go:141` `fillAuth`、`identity_ui.go:148` `authRefFromTab` | 已合入，本卡不重写；新增回归验证创建→引用→编辑→发送→保存仍保留 `ref`。 |
| 文件夹鉴权引擎 | `internal/core/identity_auth.go`：`FolderAuth`、`FolderAuthBaseline`、`SaveFolderAuth` | 已有；本卡补保真保存入口 `SaveFolderAuthPreserving` 与影响诊断。 |
| 系统凭据后端 | `internal/platform/keychain.go`：`ProjectScopeKey`、`KeychainSecrets`、`LayeredSecrets` | 已有三平台 `mygo.Secrets` 适配与克隆隔离；本卡只补可注入的录入/更新/删除端口，不新增依赖。 |
| 保真保存 | `internal/core/yaml_preserve.go`：`MergeRequestYAML`、`MergeCollectionYAML`、`MergeProjectFileYAML`；`Store.SaveChecked` / `RemoveChecked`；`ChangeSet.Apply` | 复用：方案更新保留注释/扩展字段；删除方案与清除引用在同一 ChangeSet 原子提交。 |
| 目标/生产策略 | `internal/core/identity_policy.go`：`MergeTargetPolicy`、`EvaluateTarget`；`internal/platform/network.go` `DeviceNetworkSettings.DevicePolicy` | 已有；本卡只补原生确认 UI 与一次性确认令牌，不新增策略模型、不改仓库可放宽设备上限的原则。 |
| Redactor / 脱敏 | `internal/core/identity_redact.go`、`internal/desktop/identity_ui.go` 的 `resolveWithRedactor` | 复用：确认弹窗中的策略原因仅含主机/方法/环境，经 `Redactor.RedactString` 再清洗；诊断不展示秘密值。 |
| 网络传输 / 代理 / CA / mTLS | `internal/core/plan.go`、`internal/desktop/network_ui.go` | B04 已交付，本卡不重复实现；仅交 `core/plan.go` 最小接线补丁。 |

## 2. 本卡新增的小接口（均为本分支专属文件）

### Core（纯 Go，无 GUI 依赖）

- `AuthSchemeInfo` / `IdentityReference` / `AuthSchemeInUseError`：方案清单、引用诊断、删除阻断。
- `ValidateAuthSchemeID(id)`、`ListAuthSchemes()`、`LoadAuthScheme(id)`、`CreateAuthScheme(s)`、`SaveAuthScheme(s, baseline)`、`DeleteAuthScheme(id, baseline, clearReferences)`：`.gorequest/auth/<id>.auth.yaml` 的基线检查 CRUD。`auth.ref` 永远只保存引用 ID，**不会展开成内联方案**。
- `SaveFolderAuthPreserving(folderRel, auth, baseline)`：用 `MergeCollectionYAML` + `SaveCollectionPreserving` 写 `collection.yaml`，保留 ID/名称/变量/注释/扩展字段；外改时返回 `*ConflictError`。
- `FolderAuthImpact(folderRel)`：只列出当前确实从该文件夹继承鉴权的请求（更深层文件夹或请求级覆盖不计入）。
- `SecretReferences(secrets)` / `IdentityDiagnostics(secrets)`：汇总环境声明、方案、文件夹、请求中的 `{{secret.*}}` 来源；输出 `bound` / `missing` / `unavailable` 与缺失方案引用，**不含任何秘密值或编码变体**。
- `ValidateSecretName(name)`：拒绝空、保留命名空间、`..`、路径分隔符、模板花括号、控制字符与超长名称。
- `TargetConfirmation` + `OneShotConfirmGate` + `SetPlanConfirmGate(plan, gate)` / `PlanConfirmGate(plan)`：确认与请求 ID、方法、环境、规范化 URL（含查询、忽略 fragment）精确绑定；`Consume` 仅成功一次，`AuthorizeInitialHop` 只授权首跳。门闩保存在 `weak.Pointer[Plan]` 弱键表并随 Plan 回收，**没有全局强引用 UI 对象**。

### Platform（`platform/keychain` 端口增量）

- `CredentialWriter`（`Set` / `Delete`）、`CredentialInspector`（`InspectSecret`）：1–2 方法小接口。
- `ManagedCredentials`：`NewManagedCredentials(driver, projectID, root)` 或 `NewManagedCredentialsFrom(writer, insp)`；`Observe` / `List` / `Set` / `Delete` 只处理名称与非敏感状态。底层复用 `ProjectScopeKey` 三平台隔离；驱动不可用返回错误，**绝不降级写明文文件**。测试通过 `NewManagedCredentials(driver, ...)` 注入内存假后端。

### Desktop（MyGo 原生 UI）

- `IdentityManagementModals(c, t)`、`IdentityManagementView(c)`、`BuildIdentityManagementMenuItems(mm, tab)`、`BuildIdentityFolderMenuItem(mm, folderRel)`：集成人接线入口（见 `docs/task-results/B05-UI-integration.patch`）。
- `prepareConfirmedSend(tab)` / `attachConfirmedNetworkOptions(tab, plan, token)`：公共 send 路径的精确接线入口。
- 状态全部放在 `weak.Pointer[projectState]` 弱键表 + `runtime.AddCleanup`，与 B04 `projectNetStateFor` 一致；没有全局强引用 map 保存 UI 对象。

## 3. 未冻结项与兼容性声明

- `.gorequest/auth/<id>.auth.yaml` 仍沿用已合入的 `AuthScheme` v1 字段（`id/type/token/username/password/name/in/value/extensions`），本卡**没有新增或改变字段**；`extensions` 与注释经保真合并保留。
- `collection.yaml` 的 `auth` 仍沿用已合入的 `AuthRef` 语义（`none` 标量、`{ref: id}`、内联映射）；本卡不引入新语法。
- 一次性确认令牌是**进程内临时对象**，不写共享项目、不写设备配置、不进入历史；不宣称是稳定持久化格式。
- `core/plan.go` 的 `PlanConfirmGate(plan)` 调用是集成补丁的最小增量；未应用补丁时 `SetPlanConfirmGate` 仍可记录门闩，但 `Execute` 会按现有策略明确要求确认（失败关闭），不会静默放行或沿用宽泛授权。
- `mygo.Secrets` 三平台兼容性证据沿用 `docs/specs/identity-evidence.md` 的 Q-03 记录；本卡没有新增依赖，`go.mod` / `go.sum` 零改动。真机三平台钥匙串交互仍未验收，见 `docs/task-results/B05-UI.md`。
