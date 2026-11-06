# B08 基础互操作与便携包工程证据文档（候选草案）

> **状态声明**：本文档记录任务 `B08`（基础互操作：cURL / Postman / 原生便携包导入导出）的候选工程契约、版本兼容矩阵、降级/损失报告分类与压缩包安全边界。文中涉及的便携包清单结构与兼容矩阵属于 **待架构/集成评审确认的候选方案（Candidate Specification）**，不单方面声称公开格式已最终冻结。

---

## 1. 范围与边界（Scope & Non-Goals）

### 1.1 B08 负责范围
1. **cURL 纯 Go 参数解析导入与脱敏导出**：
   - 纯 Go 状态机词法/参数解析器，**绝不调用系统 Shell（`sh`/`bash`/`zsh`/`cmd`/`powershell`）或外部进程**。
   - 支持将单条 `curl` 命令导入为 `.gorequest/collections/**/req-*.request.yaml`，或对已有请求生成经 `Redactor` 脱敏的多行 `curl` 预览/剪贴板导出。
2. **Postman Collection / Environment 明确版本矩阵导入与导出**：
   - 识别并转换 Postman Collection `v2.1.0` 与 `v2.0.0`，以及 Postman Environment（`values` 变量集）。
   - 对旧版 Postman Collection `v1.0` 或未知 JSON 结构给出明确版本/格式不支持诊断。
   - **绝不执行** `event`（`prerequest` / `test`）脚本，将其纳入损失报告（`LossReport`）。
3. **原生请求 / 文件夹 / 完整项目便携包（Portable Bundle）导出与导入**：
   - 支持按 **单请求（Request）**、**文件夹子树（Folder）**、**完整项目（Project）** 三个粒度导出与导入 `.gorequest-bundle.zip`。
   - 携带关联公开依赖（引用的 `auth/*.auth.yaml`、`docs/*.md`、`assets/*` 及项目内相对路径请求体附件）。
   - 严格排除真实 Secret 值、Cookie、本地执行历史、设备配置（`config.json`、`local-files.json`）、测试/性能报告及宿主机绝对路径。
4. **预览优先与可逆事务（复用 B01 `ChangeSet` + B05 `Redactor`）**：
   - 导入分两阶段：**阶段一（Stage & Preview）** 只在内存中计算目标路径、ID 映射、引用重写、变量/Secret 来源、损失报告与冲突列表；**阶段二（Confirm & Apply）** 经用户确认后通过 `ChangeSet.Apply()` 原子落盘，中途失败自动回滚，成功应用后支持 `Receipt.Rollback()` 一键撤销。

### 1.2 明确分流与非本卡范围
- **OpenAPI 3.0/3.1/3.2 与 Swagger 2.0**：规范契约导入/导出与双向同步归属任务 **A03**。B08 检测器识别到 `openapi` 或 `swagger` 顶层字段时，返回明确的 `openapi-routed-to-a03` 分流诊断（`CanApply = false`），**绝不声称已实现 OpenAPI 转换**。
- **同实例打开项目间普通拖拽/移动**：归属任务 **B02**，B08 聚焦外部格式与便携归档包的输入输出。

---

## 2. cURL 解析与脱敏导出矩阵

### 2.1 词法与安全边界
- 支持单引号 `'...'`、双引号 `"..."`（含 `\"`、`\\` 转义）、ANSI-C 引号 `$'...'`（`\n`、`\r`、`\t`、`\'`、`\\`）、行尾续行符 `\` + `\n`/`\r\n`。
- **Shell 构造拦截与告警**：
  - 检测到命令替换 `$(...)`、反引号 `` `...` ``、管道 `|`、多命令串联 `;` / `&&` / `||`、重定向 `>` / `<` 时，**绝不执行任何子命令**，并在 `Losses` 中记录 `curl-shell-construct-blocked` 诊断。

### 2.2 参数映射表

| cURL 参数 | GoRequest 映射行为 | 损失/脱敏规则 |
|---|---|---|
| `<URL>` / `--url <URL>` | 解析为基础 URL 与 `Query` 切片（保留重复键顺序） | 缺失 scheme 时默认补齐 `http://` 并记录提示 |
| `-X` / `--request <METHOD>` | 映射为大写 HTTP Method | 若未显式指定且存在 `-d`/`-F`/`--json` 则推导为 `POST`；`-I`/`--head` 推导为 `HEAD`；`-G`/`--get` 强制 `GET` 并将 `-d` 转为 Query |
| `-H` / `--header <K: V>` | 映射至 `Headers`（特殊处理 `Authorization`、`Cookie`、`Content-Type`） | `-H @filename` 拒绝读取宿主机任意文件并报告 `curl-header-file-unsupported`；敏感凭据值自动抽取为 `{{secret.<slot>}}` 并经 `Redactor` 脱敏 |
| `-u` / `--user <user:pass>` | 映射为 `AuthConfig{Type: "basic", Username: user, Password: "{{secret.<slot>}}"}` | 原始密码注册至 `Redactor`，绝不明文落盘，记入 `SecretBindings` |
| `-b` / `--cookie <data>` | 转为脱敏占位符或安全提示，不将真实会话 Cookie 明文固化入共享库 | 记录 `curl-cookie-scrubbed`（真实 Cookie 属于本地私有态） |
| `-A` / `--user-agent <ua>` | 映射为 `User-Agent` 请求头 | 原样保留 |
| `-e` / `--referer <url>` | 映射为 `Referer` 请求头 | 原样保留 |
| `-d` / `--data` / `--data-raw` / `--data-ascii` / `--data-binary` | 根据 `Content-Type` 或结构映射为 `json`、`form-urlencoded` 或 `raw` Body | `-d @filepath` 不直接内联宿主机私密文件内容，转为 `local:<slot>` 逻辑槽位并记录绑定提醒 |
| `--data-urlencode <expr>` | 映射至 `form-urlencoded`（支持 `name=val`、`=val`、`name@file` 槽位提示） | 保持键顺序 |
| `--json <payload>` | 设置 `Body.Mode = "json"`，自动补齐 `Content-Type: application/json` 与 `Accept: application/json` | 原样保留 JSON 文本 |
| `-F` / `--form <name=content>` | 设置 `Body.Mode = "multipart"`；`@file` / `<file` 转为 `kind: file` + `local:<slot>`（或项目内相对路径） | 支持 `;type=` 与 `;filename=` 修饰符；宿主机绝对路径脱敏为 `local:<slot>` |
| `--compressed` | 记录兼容说明（GoRequest HTTP 传输层默认支持压缩协商） | 无损 |
| `-k`/`--insecure`、`--proxy`、`--cert`、`--key`、`-L`/`--location`、`-o`/`--output` 等 | 不写入共享 Request 模型（TLS/代理策略属项目/设备安全策略，归属 B04/B05） | 逐项记录 `curl-flag-unsupported` 损失条目 |

---

## 3. Postman 版本兼容与降级矩阵

### 3.1 版本识别矩阵

| 格式类型 | 识别特征 | 支持状态 |
|---|---|---|
| **Postman Collection v2.1.0** | `info.schema` 含 `/collection/v2.1.0/` | **支持（导入 + 导出）** |
| **Postman Collection v2.0.0** | `info.schema` 含 `/collection/v2.0.0/` | **支持（导入，导出统一为 v2.1.0）** |
| **Postman Collection v1.0** | 顶层含 `requests` 数组 + `order` 数组且无 `info.schema` | **明确拒绝**（返回 `postman-v1-unsupported` 诊断，提示先在 Postman 升级至 v2.1） |
| **Postman Environment** | `_postman_variable_scope == "environment"` 或含 `name` + `values[]` | **支持（导入 + 导出）** |
| **OpenAPI 3.x / Swagger 2.0** | 顶层含 `openapi: "3..."` 或 `swagger: "2.0"` | **明确分流至 A03**（返回 `openapi-routed-to-a03` 诊断） |

### 3.2 字段转换与损失规则
- **目录与请求树**：递归转换 `item[]`；含子 `item` 的节点转为 `.gorequest/collections/<path>/folder.yaml` + 可选说明文档；含 `request` 的节点转为 `req-<slug>.request.yaml`。
- **认证（Auth）**：
  - `noauth` -> `type: none`
  - 未设置（继承父级） -> 省略 `auth`（继承父目录或集合默认身份）
  - `bearer` -> `type: bearer`（若 token 为字面量机密，转为 `{{secret.<slot>}}` 并在 `SecretBindings` 登记；若已为 `{{var}}` 模板则保留）
  - `basic` -> `type: basic`（密码转为 `{{secret.<slot>}}` 或保留 `{{var}}`）
  - `apikey` -> `type: api-key`（映射 `key`、`value`、`in: header|query`）
  - `oauth2` / `awsv4` / `digest` / `hawk` / `ntlm` -> 降级为 `none` 并在 `Losses` 中报告 `postman-auth-unsupported`。
- **请求体（Body）**：
  - `raw`：按 `options.raw.language` 或内容特征映射为 `json`、`xml` 或 `raw`。
  - `urlencoded`：映射为 `form-urlencoded`（保留 `disabled -> enabled: false`）。
  - `formdata`：`type: "text"` 映射为文本字段；`type: "file"` 若 `src` 为宿主机绝对路径或外置文件名，映射为 `local:<slot>` 并在 `LocalFileSlots` 与 `Losses` 中提示本地绑定。
  - `file`：映射为 `mode: binary` + `local:<slot>`。
  - `graphql`：将 `query` 与 `variables` 组装为标准 JSON Body（`{"query": ..., "variables": ...}`），并记录 `postman-graphql-converted-to-json` 提示。
- **变量与 Secret**：
  - Collection `variable[]` 与 Environment `values[]`：
    - `type == "secret"` 或键名匹配敏感词（`secret`/`token`/`password`/`api_key`/`private_key` 且含非空字面值）：**绝不将明文写入 `variables`**，记入 `Environment.Secrets` 列表，引用点使用 `{{secret.<name>}}`，原始值注册进 `Redactor`。
    - 普通变量写入 `Environment.Variables`。
- **脚本与扩展（绝不执行）**：
  - `event`（`listen: "prerequest"` / `"test"`）：**严禁执行任何 JavaScript**，每个非空脚本块生成一条 `postman-script-ignored` 损失记录（标明来源路径与行数）。
  - `response`（保存的响应示例）与 `protocolProfileBehavior`：记录 `postman-saved-response-ignored` / `postman-protocol-profile-ignored`。

---

## 4. 原生便携包（`.gorequest-bundle.zip`）候选结构与安全边界

### 4.1 候选清单结构（`bundle-manifest.yaml`）
便携包为标准 ZIP 归档，根目录包含 `bundle-manifest.yaml` 以及相对 `.gorequest/` 的安全子集文件：

```yaml
schemaVersion: 1
bundleKind: request # request | folder | project
sourceProjectName: Demo API
rootTargetRel: collections/orders/req-create-order.request.yaml
entities:
  - kind: request
    id: req-create-order
    relPath: collections/orders/req-create-order.request.yaml
  - kind: auth
    id: auth-orders-bearer
    relPath: auth/auth-orders-bearer.auth.yaml
  - kind: doc
    id: docs/orders.md
    relPath: docs/orders.md
publicAttachments:
  - assets/diagram.png
  - testdata/sample.json
requiredSecrets:
  - orders_api_token
requiredLocalSlots:
  - invoice_pdf
excludedCategories:
  - secrets
  - cookies
  - history
  - device-config
  - reports
  - host-paths
```

### 4.2 导出净化规则（Redaction & Exclusion）
1. **排除项**：绝不打包 `local-files.json`、`config.json`、会话 Cookie、执行历史、测试/性能报告或任何 `.tmp-*` / `.DS_Store` 文件。
2. **宿主路径剥离**：若 Request Body 或 Multipart 字段引用了 `local:<slot>`，便携包仅保留 `local:<slot>` 逻辑槽位并在 `requiredLocalSlots` 中声明，绝不导出 `local-files.json` 中的宿主机真实路径。
3. **机密隔离**：若 Environment 或 Auth 包含字面量机密，导出前通过 `Redactor` 与敏感键检测剥离字面值，仅在 `requiredSecrets` 与 `secrets` 列表中保留名称。
4. **公开依赖随包携带**：导出单请求或文件夹时，自动分析并打包其引用的：
   - `auth.ref` 指向的 `auth/<id>.auth.yaml`（含父级 `folder.yaml` 链上引用的共享身份）；
   - `documentation` 指向的 `docs/*.md` 及文档内引用的 `assets/*` 相对路径资源；
   - `body.file` 或 `multipart` 中位于项目根目录内的相对路径公开附件（受大小上限约束）。

### 4.3 冲突策略与复制 ID 映射（Conflict Policy）
导入至现有项目时提供四种显式冲突策略：
- `fail`（默认安全策略）：当目标路径或实体 ID 已存在时，在 Preview 阶段标记冲突并将 `CanApply` 设为 `false`，阻止静默覆盖。
- `rename`（复制新 ID 并重写引用）：为冲突的 Request / Folder / Auth 分配全新稳定 ID 与文件名（例如 `req-create-order-copy-2`），并**自动重写同一导入批次内部的 `auth.ref` 与 `documentation` 引用**，确保副本自洽。
- `overwrite`（基线校验覆盖）：对已存在文件生成 `ChangeUpdate` 操作并绑定当前磁盘文件 `ExpectedBaseline` SHA-256 摘要；若确认应用前外部发生并发修改，`ChangeSet.Apply()` 拒绝落盘并回滚。
- `skip`（跳过已存在项）：仅导入不存在的新增实体与依赖。

### 4.4 压缩包防攻击边界（Archive Hardening）
在 `StagePortableBundleImport` 解析阶段即强制执行以下限制（默认值可通过 `ArchiveLimits` 收紧）：
- `MaxArchiveBytes`: `64 MiB`（归档总字节上限）
- `MaxEntryCount`: `2,000`（归档内条目总数上限）
- `MaxEntryBytes`: `16 MiB`（单文件解压后真实读取字节上限，使用 `io.LimitReader` 实测字节流，不盲信 ZIP Header 的 `UncompressedSize64`）
- `MaxTotalUncompressedBytes`: `128 MiB`（全包累计解压真实字节上限）
- `MaxCompressionRatio`: `100:1`（对非微小文件检测高压缩比解压炸弹）
- **路径与类型硬化**：
  - 拒绝任何含 `..`、前导 `/`、反斜杠 `\`、Windows 盘符（`C:`）、控制字符或空白尾缀的条目名；
  - 拒绝任何符号链接条目（`hdr.Mode() & os.ModeSymlink != 0`）或非常规文件（FIFO/Device/Socket）；
  - 落盘阶段复用 B01 `Store` / `ChangeSet` 的逐级 `os.Lstat` 符号链接校验，防止通过项目内预置软链逃逸出项目根目录。
