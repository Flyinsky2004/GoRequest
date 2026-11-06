# B05 Identity 设计与决策证据记录

状态：B05 分支设计与实现证据（对应 `Q-03`、`Q-06` 变量/文件夹鉴权子集、`Q-09` 及敏感传播契约）。基线提交：`be92379`。本文为冻结前实证材料，不直接修改 `docs/decisions.md` 总表状态，供集成评审合入。

## 1. Q-03：系统凭据后端与项目克隆隔离

### 1.1 本地依赖实查证据（`github.com/egoist/mygo v0.7.4`）

核查本地模块缓存中 `mygo v0.7.4` 的 `secrets.go` 及三平台实现，无需新增任何第三方依赖即可使用原生系统凭据存储：

| 平台 | `mygo.Secrets` 底层机制 | 缺失/不可用行为 | 限制 |
| --- | --- | --- | --- |
| macOS (`internal/darwin/secrets.go`) | `Security.framework`（`SecItemAdd`/`SecItemCopyMatching`/`SecItemUpdate`/`SecItemDelete`），`kSecClassGenericPassword` 登录钥匙串；不依赖签名 Entitlement | 未找到返回 `mygo.ErrSecretNotFound`；系统错误带 `OSStatus` 描述 | 键长 `<= 256` B，值 `<= 2560` B（`mygo.MaxSecretSize`），无 NUL |
| Windows (`internal/windows/secrets.go`) | `advapi32.dll`（`CredWriteW`/`CredReadW`/`CredDeleteW`），`CRED_TYPE_GENERIC` + DPAPI 用户级加密 | 未找到（`ERROR_NOT_FOUND=1168`）返回 `ErrSecretNotFound` | 同上（对齐 Windows Credential Blob 上限 2560 B） |
| Linux (`internal/linux/secrets.go`) | `libsecret-1.so.0` + D-Bus Session Bus（`org.freedesktop.Secret.Generic`，兼容 GNOME Keyring / KWallet / KeePassXC） | 缺 `libsecret`、无 Session Bus 或无 Secret Service 时返回满足 `errors.Is(err, errors.ErrUnsupported)` 的错误 | 同上 |

### 1.2 分层边界与无明文降级契约

1. **纯 Go Core 隔离（ADR-003）**：`internal/core` 不导入 `mygo`，仅定义 `SecretProvider`（`Get(name) (string, bool)`）与可选诊断接口 `SecretInspector`（区分 `bound` / `missing` / `unavailable`，永不返回秘密原文）。
2. **同仓库多克隆隔离（`storage-format.md` §1）**：系统凭据键采用 `<projectSlug>:<sha256(canonicalProjectRoot)[:16]>/<secretName>`，总长度严格校验 `<= 256` 字节。同一 Git 仓库在不同本地目录克隆、或不同项目声明同名 `apiToken` 时互不串读。
3. **无明文降级硬约束**：
   - 解析顺序：显式内存覆盖（单次运行/测试注入）→ 系统钥匙串（`KeychainSecrets`）→ 显式环境变量（`EnvSecrets`：`GOREQUEST_SECRET_<NAME>`）。
   - 当系统凭据存储不可用（`errors.ErrUnsupported` 或钥匙串锁定）且未通过环境变量提供该秘密时，直接返回 `*core.ConfigError` 并给出后端状态诊断，**严禁回退写入或读取任何本地明文文件**。
4. **跨平台验收边界声明**：当前工作环境（macOS 无交互测试进程）通过可注入 `KeychainDriver` 完整验证成功读写、克隆隔离、超限拒绝、钥匙串锁定、`errors.ErrUnsupported` 与零磁盘落盘，并验证 `MyGoKeychainDriver` 真实编译对接 `mygo.Secrets`；真实 macOS 钥匙串解锁弹窗、Windows Credential Manager 与 Linux `libsecret`/D-Bus 实机交互留待三平台人工验收，不在自动化测试中污染宿主真实登录钥匙串。

## 2. Q-06（B05 子集）：变量分层、保留命名空间与文件夹鉴权继承

### 2.1 普通变量优先级与保留命名空间

1. **普通变量七层合并（低 → 高）**：
   `project`（`project.yaml`）→ `environment`（所选 `environments/<id>.yaml`）→ `folder`（`collections/**/collection.yaml`，由外层祖先到内层直接父目录逐层覆盖，并记录具体 `RelPath`）→ `request`（`*.request.yaml`）→ `run`（套件级运行变量）→ `step`（当前步骤变量）→ `override`（单次显式覆盖）。
2. **显式保留命名空间（不可被普通同名变量覆盖或伪造）**：
   - `{{secret.<name>}}`：始终路由至 `SecretProvider`；即使普通变量表存在键 `"secret.foo"` 也不得覆盖或绕过秘密后端。
   - `{{data.<field>}}`：始终路由至运行期数据集行（`ResolveContext.Data`）；非数据驱动运行时引用即在发网前报 `*ConfigError`。
   - `{{steps.<stepId>.<output>}}`：始终路由至运行期步骤提取输出（`ResolveContext.Steps`，支持携带 `Sensitive` 标记）；未注入时发网前报 `*ConfigError`。
   - **向后兼容证据**：现有测试（`internal/desktop/request_form_test.go`）已使用 `{{user.id}}` 作为普通点号变量名，因此仅保留 `secret.`、`data.`、`steps.` 三个显式前缀，其他含点普通变量名保持兼容。
3. **来源与覆盖链预览（`InspectVariables`）**：
   - 输出每个可见变量的当前生效来源（如 `environment:dev`、`folder:collections/users`）、被覆盖的下层来源列表（`Overridden`）与解析状态。
   - 若某普通变量直接或间接引用了 `{{secret.*}}` 或敏感 `steps.*`，其污点标记 `Sensitive=true`，预览展示值强制置为 `"***"`，且 `ResolvedValue` 不向调用方暴露明文。

### 2.2 鉴权继承与显式清除规则

1. **三级继承链（低 → 高）**：`project.yaml (defaults.auth)` → 祖先文件夹 `collection.yaml (auth)`（由外到内）→ `request.yaml (auth)`。
2. **语义枚举**：
   - 缺省（YAML 省略 `auth` / `Auth == nil` / `Mode == ""`）：**`inherit`**，沿用最近上级的有效决定。
   - `auth: none`（`Mode == "none"`）：**显式清除**上级鉴权；若内层文件夹设为 `none`，其下设为 `inherit` 的请求实际生效为无鉴权，除非请求自身再声明 `ref` 或 `inline`。
   - `auth: { ref: <id> }`（`Mode == "ref"`）：引用 `.gorequest/auth/<id>.auth.yaml`；桌面编辑非鉴权字段（如 URL）保存时保持 `{ ref: <id> }`，绝不展开为 `inline`。
   - `auth: <inline>`（`Mode == "inline"`）：请求或文件夹内联 `bearer`、`basic`、`apikey`（`in: header | query | cookie`）。

## 3. 敏感属性传播与多编码脱敏契约（供 B06 / B08 / A01 复用）

仅按 `Authorization` / `Cookie` 头名黑名单过滤无法防止秘密经自定义 API Key 头（如 `X-API-Key`）、Query 参数、JSON Body、Basic Base64 拼接或错误消息泄漏（`acceptance.md` §2）。B05 在 `internal/core` 建立统一 `Redactor` 与污点解析端口：

1. **污点追踪（`ResolveTracked` / `BuildTrackedPlan`）**：解析过程中凡触达 `secret.*` 或标记 `Sensitive` 的运行时值，结果携带 `Sensitive=true` 并将秘密原文及关联上下文（如 Basic 的 `username:password`）登记到 `Redactor`。
2. **派生与编码变体脱敏**：对登记的秘密同步生成以下变体并按**最长子串优先**替换为 `***`：
   - 原始值；
   - Basic 拼接 `base64(user + ":" + secret)` 及单值 Base64（`StdEncoding`、`RawStdEncoding`、`URLEncoding`、`RawURLEncoding`）；
   - URL 编码变体（`url.QueryEscape`、`url.PathEscape`）；
   - JSON 字符串转义内容（`json.Marshal` 去引号内容）。
3. **结构化槽位兜底脱敏**：无论秘密长度如何（包括 `< 4` 字节的短秘密），固定屏蔽 `Authorization`、`Proxy-Authorization`、`Cookie`、`Set-Cookie` 以及当前生效 `apikey` 方案声明的 Header 名、Query 参数名、Cookie 名对应值。
4. **错误类型保真脱敏（`RedactError`）**：清洗错误消息文本中的秘密及编码变体，同时保留 `errors.As` 对 `*ConfigError`、`*RunError`、`*ConflictError` 的分类判定，不破坏自动化与 UI 错误分支。

## 4. Q-09：设备与项目目标/生产策略单调合并

1. **权限单调收敛原则（“仓库不能自行扩大权限”）**：
   - `ProjectTargetPolicy`（项目配置）仅可**收紧**限制：标记环境为生产环境、标记主机模式为生产主机、收窄允许主机白名单、要求非回环凭据走 HTTPS。
   - `DeviceTargetPolicy`（本机配置）掌握最终安全上限：项目配置**不能**关闭本机 TLS 校验、**不能**放行本机黑名单主机、**不能**将本机生产保护级别（`Deny` > `Confirm` > `AllowReadOnly` > `Allow`）调低。
2. **执行与重定向逐跳校验**：
   - **生产环境与非幂等方法保护**：目标命中生产环境（如 `prod`/`production` 或显式标记）或生产主机时，按合并后动作判定；写操作（非 `GET`/`HEAD`/`OPTIONS`）在交互模式无显式确认许可时返回 `DecisionConfirmRequired`，在非交互模式（Runner/自动化）直接返回 `*ConfigError` 拒绝发网。
   - **明文传输凭据保护**：携带鉴权凭据或秘密向非回环（非 `localhost` / `127.0.0.1` / `::1`）的 `http://` 目标发送时，默认拦截或要求显式确认，同时保留本地 `127.0.0.1`/`httptest` 开发联调畅通。
   - **跨源与降级重定向剥离（`EvaluateRedirectHop` / `StripCrossOriginCredentials`）**：每一跳重新执行目标策略检查（防止开放重定向跳入受限内网/生产主机）；只要 `scheme`、`host` 或有效端口任一变化（含同主机 `https` → `http` 降级），立即剥离 `Authorization`、`Proxy-Authorization`、`Cookie` **以及自定义 `apikey` 请求头**（弥补标准库 `net/http` 不认识自定义敏感头的缺口，供 B04 transport 直接调用）。
