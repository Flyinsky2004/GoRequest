# 存储格式与项目数据

状态：文本存储和数据隔离方向已确定；以下路径布局、YAML 字段、引用语法及默认值均为 **v1 设计草案**，不是现有可执行标准。示例仅嵌入文档，本次不建立活动 `.gorequest/` 项目或运行任何请求。

实现现状（2026-11-07 同步）：变量/Secret 解析与凭据后端已落地——`{{secret.<name>}}` 保留命名空间（不回退到同名普通变量，缺失或后端不可用即报错）、`SecretProvider` 分层解析（内存覆盖 → 系统凭据 → 环境变量）、项目级系统凭据存储与零明文回退均已实现，见 ADR-010 及 `internal/core`、`internal/platform`。其余路径布局、完整字段表与默认值仍以本文草案为准，未全部冻结。

## 1. 全局、项目与运行状态

| 数据 | 位置原则 | Git 策略 |
| --- | --- | --- |
| 项目元信息、请求、集合、环境非敏感值、鉴权方案 | 仓库内 `.gorequest/` | 可提交 |
| 测试、公开/脱敏数据集、Schema、SDL、Proto、Mock 规则、文档 | `.gorequest/` 中独立文本文件 | 可提交 |
| 图片等文档附件 | `.gorequest/assets/` 相对路径 | 用户选择纳入；不存执行附件缓存 |
| 全局临时请求、个人集合、最近项目、设备设置 | OS 应用数据/配置目录，与任何项目分离 | 不提交 |
| 密码、Token、Refresh Token、客户端私钥、Cookie | 系统凭据存储或本地受保护存储；由本机或环境变量注入 | 不提交 |
| 请求历史、流式消息、Mock 访问日志、运行缓存 | OS 本地数据/缓存目录，按本地项目键分区 | 不提交 |
| 测试/性能报告、临时导出/生成代码 | 本地运行目录或明确忽略的输出目录 | 不提交 |

可移植的普通环境值保存在项目；实际 `secret` 值从本机凭据后端或显式绑定取得。系统凭据不可用时必须说明并要求配置后端，不能静默改为明文文件。个人历史数据库可以采用 SQLite 等实现，选型待定；项目事实来源始终不是数据库。

实现现状：`secret` 值解析顺序为内存覆盖 → 系统凭据（macOS Keychain / Windows Credential Manager / Linux Secret Service）→ 环境变量；项目凭据按 `<projectSlug>:sha256(规范化项目根)[:16]` 隔离，后端缺失/锁定/不支持时零明文文件回退并报不可用。无 GUI 或 CI 场景用环境变量 `GOREQUEST_SECRET_<NAME>` 注入，`<NAME>` 为 secret 名大写、非字母数字转下划线。

全局目录由平台 API 解析，不硬编码用户主目录。候选映射：macOS 的 Application Support/Caches，Windows 的 AppData，Linux 的 XDG Config/Data/State/Cache；最终细分及权限行为需跨平台验证。项目运行键建议由稳定项目 ID 与规范化本地路径组合，防止同仓库的不同克隆误用凭据和历史。

## 2. 示例业务项目目录

```text
my-service/
├── src/                               # 业务代码（如有）
├── .gitignore
└── .gorequest/
    ├── project.yaml
    ├── collections/
    │   ├── auth/
    │   │   ├── collection.yaml
    │   │   └── login.request.yaml
    │   └── users/
    │       ├── collection.yaml
    │       ├── me.request.yaml
    │       ├── update.request.yaml
    │       └── settings.request.yaml
    ├── environments/
    │   ├── dev.yaml
    │   └── ci.yaml
    ├── auth/
    │   └── bearer.auth.yaml
    ├── suites/
    │   └── smoke.suite.yaml
    ├── datasets/
    │   └── users.json
    ├── schemas/
    │   ├── login.schema.json
    │   └── user.schema.json
    ├── examples/
    │   └── user-success.json
    ├── graphql/
    │   ├── schema.graphql
    │   └── user-query.graphql
    ├── proto/
    │   └── user.proto
    ├── mocks/
    │   └── users.mock.yaml
    ├── docs/
    │   ├── README.md
    │   └── authentication.md
    └── assets/
        └── auth-flow.png
```

这是未来业务项目的数据示例；与当前 GoRequest 仓库保存产品设计的根目录 `docs/` 区分。项目不要求存在 `src/`、Git 或全部可选子目录。每个请求独立 YAML；Markdown 可放专用 `docs/` 或与请求相邻，通过引用关联。

## 3. 格式与引用通则

- UTF-8、LF、末尾换行、YAML 两空格缩进，不写自动更新时间、机器路径或运行生成顺序。
- YAML 采用严格解析：拒绝重复键与不支持的类型；初始格式不使用跨文件隐式合并。安全限制递归、别名展开、文件数量和大小。
- 每个独立结构化实体有 `schemaVersion` 与稳定 `id`；ID 的唯一性按实体类型校验，请求 ID 全项目唯一。更名/移动保留 ID，复制分配新 ID。
- 请求/套件 ID 使用稳定字符串，例如 `auth.login`、`smoke`；跨文件引用不依赖当前显示名称。
- 结构化文档中的路径以 `.gorequest/` 为基准，统一 `/`；Markdown 普通链接以所在文档为基准。拒绝路径穿越与符号链接逃逸；外部上传文件、证书等使用本机显式映射。
- 请求的 Header/Query 用有序数组，允许重复键及禁用项；对象字段顺序保持稳定，不因打开保存而重排整个文件。
- 有意编排用显式 `order`，目录列表展示可按名称排序；不把文件系统遍历顺序当测试顺序。
- 未识别且影响执行的字段报错；扩展放入命名的 `extensions` 区。高于支持版本时不执行、不保存降级；不能静默删字段。
- 计划发布公开格式 Schema；本次没有创建或声称已提供完整 JSON Schema。

## 4. 项目、环境与鉴权示例

`project.yaml`：

```yaml
schemaVersion: 1
id: example.user-service
name: 用户服务
variables:
  baseUrl: "http://127.0.0.1:4010"
defaults:
  timeout: 10s
  auth: none
```

`environments/ci.yaml`（共享普通配置与密钥名称，不含密钥值）：

```yaml
schemaVersion: 1
id: ci
variables:
  baseUrl: "http://127.0.0.1:4010"
secrets:
  - loginPassword
```

`auth/bearer.auth.yaml`：

```yaml
schemaVersion: 1
id: bearer
type: bearer
token: "{{accessToken}}"
```

直接使用固定令牌的个人调试可把 `token` 配成 `{{secret.apiToken}}`；不要把值写入文件。示例登录套件使用提取结果作为 `accessToken`。

## 5. 请求 YAML 示例

`collections/auth/login.request.yaml`：

```yaml
schemaVersion: 1
id: auth.login
name: 用户登录
protocol: http
tags: [auth, smoke]
http:
  method: POST
  url: "{{baseUrl}}/auth/login"
  headers:
    - name: Content-Type
      value: application/json
      enabled: true
  body:
    type: json
    value:
      username: "{{username}}"
      password: "{{secret.loginPassword}}"
auth: none
timeout: 10s
assert:
  - type: status
    equals: 200
  - type: schema
    schema: schemas/login.schema.json
documentation:
  - docs/authentication.md
```

`collections/users/me.request.yaml`：

```yaml
schemaVersion: 1
id: users.me
name: 当前用户
protocol: http
http:
  method: GET
  url: "{{baseUrl}}/users/me"
  query:
    - name: include
      value: settings
      enabled: true
auth:
  ref: bearer
timeout: 10s
assert:
  - type: status
    equals: 200
```

其他 Body 类型计划为 `xml/text/form-data/urlencoded/binary`，内容可用独立文件引用；互斥字段、二进制映射和 multipart 精确定义待格式评审。JSON `value` 中的字符串模板由 JSON 编码器转义，不将文本替换直接拼成 JSON；期望数字/布尔等类型时需显式类型绑定，语法待定。

## 6. 测试套件 YAML 示例

`suites/smoke.suite.yaml`：

```yaml
schemaVersion: 1
id: smoke
name: 登录与个人资料冒烟测试
tags: [smoke]
dataset:
  file: datasets/users.json
  format: json
execution:
  concurrency: 1
  onFailure: stop
steps:
  - id: login
    request: auth.login
    variables:
      username: "{{data.username}}"
    assert:
      - type: jsonpath
        path: $.data.accessToken
        operator: exists
    extract:
      token:
        from: jsonpath
        path: $.data.accessToken
        sensitive: true
        required: true
  - id: profile
    request: users.me
    dependsOn: [login]
    variables:
      accessToken: "{{steps.login.token}}"
    assert:
      - type: jsonpath
        path: $.data.username
        operator: equals
        value: "{{data.username}}"
      - type: duration
        metric: total
        lessThan: 2s
```

`datasets/users.json`：

```json
[
  {"username": "alice"},
  {"username": "bob"}
]
```

`schemas/login.schema.json`（测试服务的 Token 只在实际响应中出现）：

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "required": ["data"],
  "properties": {
    "data": {
      "type": "object",
      "required": ["accessToken"],
      "properties": {"accessToken": {"type": "string", "minLength": 1}}
    }
  }
}
```

依赖指向步骤 ID；`steps.login.token` 只在当前数据行且依赖成功后可读。请求自身断言与套件步骤断言累加，不覆盖；提取只在请求传输与该步骤全部断言通过后提交。示例需要提供兼容的本地测试服务及注入 `loginPassword`，不是当前可直接执行的工具样例。

## 7. 变量与鉴权解析草案

普通同名变量从低到高为：项目默认 → 所选环境 → 祖先文件夹（由外到内）→ 请求 → 套件运行变量 → 当前步骤变量 → 本次显式 UI 覆盖。原对话曾提“四级”但未列出层级，本表为补全草案，不冒充历史定稿。

`{{data.name}}`、`{{steps.stepId.output}}`、`{{secret.name}}` 为保留显式命名空间，不参与普通同名覆盖；动态值使用独立动态解析器，UUID/时间/随机的精确语法待定。解析循环与缺失值在发网前报错，展示值来源时同样脱敏。全局个人变量不隐式进入项目，避免因缺少设备状态而语义不同。

实现现状：`{{secret.name}}` 与普通变量的区分已按上段实现——secret 值由 `SecretProvider` 提供、不读项目文件，解析到的 secret 值与敏感运行值在预览/导出/错误中脱敏；普通变量不在脱敏范围，值随项目文件保存。`{{steps.*}}` 提取的网络值按字面串处理，避免响应内容注入 `{{secret.*}}` 模板。

鉴权从低到高为项目 → 祖先文件夹 → 请求；缺省表示继承，显式 `none` 表示清除。运行级凭据绑定只替换引用值，不悄悄替换鉴权方案。是否允许套件显式切换方案列为待决策。

## 8. Git 忽略、保存与迁移

未来项目可在确认合并已有规则后加入下列示意规则；本次不修改任何 `.gitignore`：

```gitignore
/.gorequest-local/
/.gorequest-reports/
/.gorequest-generated/
```

默认仍优先写入 OS 本地目录；这些根目录仅供显式指定本地输出使用。应用对仓库内的报告/缓存路径需检查忽略状态，未忽略时拒绝持久化并提示调整路径；这不影响将经过预览、脱敏的规范/SDK 作为有意交付物另行导出。`.gitignore` 不能清除已被跟踪的历史密钥，应用应在导入和提交前检查敏感内容。

保存前比对读取摘要，不覆盖读取后发生的外部修改；采用同目录临时文件及原子替换。无内存修改时可刷新；有修改时保留双方并让用户选择合并。检测 Git 冲突标记时禁止执行该对象。

迁移必须预览变更、校验引用、保留恢复副本并明确确认。应用默认不迁移，格式不兼容时返回配置错误。备份、事务日志放本机状态区；可恢复机制不得把带密钥的临时文件留进项目。
