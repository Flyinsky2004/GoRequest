# Request Snippet 实现证据（C04-phase1）

依据：[documentation-codegen.md §4](documentation-codegen.md)、AC-16、Q-15、任务账本 §24（C04-phase1 行）。
状态：**阶段 1 交付（本分支未集成、未编译、未运行）**。本文件只描述本卡实现，不声称整卡关闭。

## 1. 交付范围

| 归属 | 内容 |
| --- | --- |
| 新增 `internal/snippet/` | 纯 Go 生成器：`snippet.go`（API/语言注册/Result）、`snapshot.go`（定义+环境快照与语义合并）、`escape.go`（转义/env 命名）、`expr.go`（跨模板表达式与文件占位命名）、`lang_curl.go`、`lang_go.go`、`lang_python.go`、`lang_js.go`（JS/TS 共享核心）、`snippet_test.go`（交付用例，见 §5） |
| 新增 `internal/desktop/snippet_*.go` | 原生 MyGo 弹窗：`snippet_state.go`（per-window 状态，weak.Pointer+AddCleanup，无强引用全局）、`snippet_ui.go`（语言切换/预览/复制/显式导出/诊断渲染）、`snippet_i18n.go`（EN/ZH） |
| 公共接线 | `docs/task-results/C04-phase1-integration.patch`：仅 `app.go` `a.SnippetModals(c,t)` 一行 + `workspace.go` `a.BuildSnippetProjectMenuItems(mm)` 一行。已在本 worktree 对基线 `git apply --check` 通过后撤回 |

只消费只读 Core API（`Project.ResolveVars` / `ResolveEffectiveAuth` / `InspectBody` / `ValidateBody` / `ValidateHTTPMethod` / `ParseFileRef` / `ValidateLocalSlot` / `NormalizeAPIKeyLocation` / `DefaultTimeout`），不修改 core/desktop 公共文件。SDK/Proto 生成（§5）另期。

## 2. 语义等价对照（与 core runner 对齐点）

- **Query 合并**：与 `Execute` 一致——先解析好的 URL `url.Parse`，`q.Add` 逐对加入后 `q.Encode()`（键名字典序、重复键按序保留）生成最终 URL 快照。
- **Header**：定义顺序、重复保留；`applyBodyContentType` 语义一致（multipart 强制替换、其余 ensure-only；Content-Type 恒为最后一对）。
- **Auth**：`ResolveEffectiveAuth`（project→folder→request）；bearer/basic/apikey(header|query|cookie)/oauth2|oidc 占位语义见 §3；apikey query 与定义 query 同样折叠进 URL。
- **超时**：`req.Timeout`（`time.ParseDuration`）或 `DefaultTimeout=10s`；非法值阻断（与 BuildPlan 相同的 config error 语义）。
- **Body**：`ValidateBody`+`InspectBody` 后按 `BuildBody` 分支：json（map 键字典序 + `encoding/json` 转义含 HTML 转义，与 renderNode→json.Marshal 字节一致）、text/xml/urlencoded（Encoded 字段不二次编码，混入时按 runner 规则输出预构建字面量并注明）、form-data（text/file、fileName/contentType 规则、固定 boundary 显式信息注记）、binary（需 file/localFile）。file 引用一律显式路径占位常量 + 诊断。
- **阻断项**（`Result.Blocked`，无代码输出）：非 http 协议、无 http 定义、非法 method/timeout、绝对 URL 校验失败、未定义变量/变量环/深度超限、URL/query 中出现 secret 或 dataset/step 占位、header 名空/换行注入、unsupported body 类型、binary 无文件、unsupported auth 类型、urlencoded 混 Encoded 字段时掺 secret。与 runner 的拒绝路径一致或更严格（宁可少生成）。

## 3. Secret 与占位策略

- Secret 值**从不解析**：生成路径不持 SecretProvider；`{{secret.name}}` → 运行时环境变量占位 `GOREQ_SECRET_<NAME>`（名称规范化确定性映射，注记列明）。
- 凭据字段（bearer token / basic password / apikey value / 敏感键名 Header 字面量）仅当显式含 `{{secret.*}}` 才成为具名运行时查找；否则（字面量或经普通变量解析出的凭据）一律替换为占位 + `scrubbed`/`credential-scrubbed` 注记——对齐 core 导出对凭据的脱敏方向并更强（宁可占位不可误嵌）。
- JSON 体内部的 secret 引用无法做运行时拼接，保留可见 `{{secret.<name>}}` 文本占位 + 注记；`{{data.*}}/{{steps.*}}` 仅存在于 dataset/suite 运行期，保留可见占位 + `unsupported` 注记，不捏造值。
- OAuth/OIDC：不读取本地 token；显式 `GOREQ_OAUTH_ACCESS_TOKEN` 占位 + warning。
- 本机路径：项目相对文件与 local slot 一律输出显式占位常量（`/path/to/…`）+ 注记；不输出任何宿主绝对路径。

## 4. 每语言模板运行时声明与已知差异（Result.Runtime + Notes）

| 语言 | 声明 | 运行时差异的可见处理 |
| --- | --- | --- |
| cURL | curl 7.68+，POSIX 单引号，生成/复制绝不执行 shell | urlencoded 含 secret 时 `--data-urlencode`（空格 %20 vs `+`）注记；multipart 由 curl 自带 boundary |
| Go | 1.21+ 纯标准库 | User-Agent 与 runner 相同（Go 默认），无差异注记 |
| Python | 3.8+ 纯标准库 urllib.request；重复 Header 自动切换 http.client | http.client 不跟随重定向（warning）；UA 默认值（info） |
| JavaScript | Node 18+ 内建 fetch/undici，无 npm 依赖 | Headers 重复项合并单行（warning）；UA（info） |
| TypeScript | TS 5+ / Node 18+；文件输入需 @types/node（info 注记） | 同 JavaScript |

网络/代理/TLS/mTLS/stage 超时/CookieJar/重定向策略属 B04 设备层配置，静态片段无法携带：每条结果附带 `snippet-network-policy`（info）说明，TLS 校验不降级（三语言均默认开启证书校验）。

## 5. 静态核对与待运行用例

已做：`gofmt`（全部本卡文件通过）、`git diff --check` 通过、`gofmt -e` 语法解析全通过、core 引用清单逐一比对存在（25 项）、包内标识符交叉核对无未定义、patch `git apply --check` 通过。
未做（按用户"先不执行测试"约定）：`go build`/`go vet`/`go test` 均未运行，以下用例**未验证通过**，仅交付待集成后执行：

- `internal/snippet`：secret 占位五语言、basic 密码不嵌入、JSON+query 编码与超时、重复 header（cURL 双份/Python http.client/JS Headers）、敏感 Header 字面量清洗、multipart+文件占位、urlencoded 预构建字面量、阻断三类（非法超时/非 http/未定义变量）、两次生成逐字节一致、运行时声明存在、JSON 内 secret 可见占位。
- `internal/desktop`：弹窗每语言生成、未定义变量阻断、导出拒绝覆盖→允许覆盖写入且内容等于预览。

## 6. 限制与后续

- 复制/导出仅写剪贴板/文件，不执行生成代码；导出默认拒绝覆盖，覆盖需显式勾选。
- binary/file-backed JSON/text/urlencoded 体在片段中以显式路径占位呈现（定义快照不含文件内容，避免读取大文件与嵌入风险）。
- OAuth token 注入、CookieJar 会话、设备网络配置不在快照内（有注记）；SDK/Proto 生成、套件级 snippet、CLI 输出另期。
- 未做跨平台运行验收；未启动真实应用/网络/监听。
