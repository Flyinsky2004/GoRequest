# C04-phase1 任务交付报告：五语言请求代码片段（cURL/Go/Python/JS/TS）

## 1. 基本信息

- **任务 ID**：`C04-phase1`（文档生成卡 C04 第一阶段，见任务账本 §24）
- **开发分支 / worktree**：`vibe/C04-request-snippets` / `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-C04-snippets`
- **共同提交基线**：`b91ba32fdb7b6c5c039bd74701da75f83bab27d7`（本 worktree 起点已核对一致）
- **本卡最终提交**：本文件与功能改动同次本地 commit；哈希可由 `git log -1 --format=%H -- docs/task-results/C04-phase1.md` 查询（本报告即在该提交内）
- **规范依据**：`docs/specs/documentation-codegen.md` §4、`docs/acceptance.md` AC-16、任务账本 §24 C04-phase1 行
- **实现证据**：`docs/specs/snippet-evidence.md`（语义对照、占位策略、模板差异、限制）

## 2. 交付文件（全部为本卡专属，未触碰公共/其他卡文件）

### 2.1 纯 Go 生成器 `internal/snippet/`

- `snippet.go` — 公共 API：`Generate(p, entry, lang, Options)`；`Language`/`Languages()`；每语言 `RuntimeDecl` 运行时/依赖声明；`FileExtension`；`Note{Code,Severity,Message}` 五级（info/warning/scrubbed/unsupported/blocked）；`Result{Runtime,Code,Notes,RequiredEnv,Blocked}`。语义拒绝经 `Result.Blocked` 返回（无代码输出），仅 nil 参数报错。
- `snapshot.go` — 定义+环境可审阅快照：模板解析（`{{var}}` 递归、`{{secret.*}}` 占位、`{{data.*}}/{{steps.*}}` 可见占位）、敏感键名凭据清洗、bearer/basic/apikey(header|query|cookie)/oauth|oidc 注入、URL+query 经 `url.Values` 折叠（与 runner 同键序/重复键序）、`BuildBody` 全分支（json 键排序与 JSON 转义字节级对齐、text/xml/urlencoded Encoded 语义、multipart、binary）、超时解析、Content-Type ensure/replace（含 multipart 强制）。
- `escape.go` — POSIX 单引号/`$GOREQ_*` 双引号展开组合、Go/Python/JS 双引号转义、确定性 env 命名（`GOREQ_SECRET_<NAME>` / `GOREQ_<KEY>`）、秒数格式化。
- `expr.go` — 各语言分段表达式（literal + 运行时 env 查找拼接）、Header/Content-Type 拆分、文件占位路径（`/path/to/…`，绝无宿主绝对路径）与确定性变量命名（去重后缀）。
- `lang_curl.go` — POSIX 多行 cURL：`-X/-H/-u/--data-raw/--data-urlencode/--data-binary/-F/--max-time`；multipart 交由 curl 自带 boundary（与 runner 随机 boundary 等价，注记说明）。
- `lang_go.go` — Go 1.21+ 纯标准库完整程序：`net/http` + `encoding/base64`/`mime/multipart`/`net/url`/`textproto` 按需组装；`mustEnv` 缺失变量显式 Fatal。
- `lang_python.go` — Python 3.8+ 纯标准库：urllib.request 默认、HTTPError 捕获（对齐 Go“任意状态码返回”）、重复 header 切 http.client（不跟随重定向 → warning 注记）、multipart 手工组装（固定 boundary + 一致 Content-Type）。
- `lang_js.go` — Node 18+ 内建 fetch ESM（JS `.mjs`/TS 共享核心）：AbortSignal.timeout、URLSearchParams/FormData、重复 Header 用 `new Headers([...])` 并注记合并语义；TS 版含类型标注与 `@types/node` 提示。
- `snippet_test.go` — 交付用例 11 组（§5 列出），**按约未运行**。

### 2.2 原生 MyGo UI `internal/desktop/snippet_*.go`

- `snippet_state.go` — per-window `SnippetState`（weak.Pointer[App]+AddCleanup，无强引用全局绕过生命周期）；OpenSnippetModal（活动 Tab→选中请求→请求列表首项）、RefreshSnippetPreview/SelectSnippetLanguage、CopySnippetToClipboard（仅剪贴板，不执行）、ExecuteSnippetExportToFile（显式路径 + 默认拒绝覆盖 + 覆盖需勾选）。
- `snippet_ui.go` — 项目菜单项 + 弹窗：语言分段切换、runtime 声明、RequiredEnv 清单、按严重度着色的诊断（含 code 文本，不靠颜色单独传达）、monospace 预览、导出路径/Browse/覆盖勾选；EN/ZH i18n（`snippet_i18n.go`，默认中文）。

### 2.3 公共接线（交集成人）

- `docs/task-results/C04-phase1-integration.patch` — 相对共同基线的最小 patch：`app.go` View 加 `a.SnippetModals(c, t)` 一行；`workspace.go` buildProjectMenu 加 `a.BuildSnippetProjectMenuItems(mm)` 一行。已在本 worktree `git apply --check` 验证可应用后**仅撤回临时改动**，公开文件未提交。

## 3. 功能与公共接线摘要

- 五语言可审阅代码生成：定义+当前环境取值快照、确定的输出（两次生成逐字节一致是交付用例之一）。
- Secret 策略：不持 SecretProvider、不读 OAuth token/Cookie/密码/私钥；未解析 Secret → 环境变量占位；凭据字面量一律清洗 + 注记；JSON 内保留 `{{secret.*}}` 可见文本占位。
- 语义与限制不做假完成：无法等价表达的（URL/query 中的 secret、dataset/step 上下文、混合 Encoded urlencoded 掺 secret 等）阻断且无代码；可表达但有差异的（curl 空格编码、http.client 重定向、fetch 重复 header 合并、multipart boundary、UA 默认值、设备网络配置不带）全部产出可见诊断注记。
- TLS 全部模板默认开启校验、不降级；复制不执行代码；导出不覆盖用户文件（除非显式勾选）。

## 4. 静态验证结果（本批均未编译/未运行）

- `gofmt`：本卡全部 13 文件 `gofmt -l` 无输出（预存的其他未格式化文件未触碰）。
- `gofmt -e` 语法解析：全部通过（不作编译验证）。
- `git diff --check`：通过。
- Core 引用核对：`core.*` 25 项引用逐一比对存在；包内标识符交叉核对无未定义调用；`pendingAuthQuery` 等早期草稿残留已清零。
- Integration patch：`git apply --check` 通过。

## 5. 待执行用例（集成后运行）

- `go test ./internal/snippet ./internal/desktop`：secret 占位五语言不泄漏、basic 密码不嵌入、JSON/query 编码与超时对齐、重复 header 三语言行为、敏感 Header 清洗、multipart/文件占位、urlencoded 预构建字面量、阻断（非法超时/非 http/未定义变量）、确定性、JSON secret 可见占位；桌面状态机（每语言生成/阻断/导出拒绝覆盖→覆盖写入内容一致）。
- 人工验收：真实项目的复制/导出动作、语义等价请求对本地服务的比对（属 AC-16 整体验收，本期样本不含真实网络）。

## 6. 未验证 / 限制

- 未运行 `go build`/`go vet`/`go test`，未启动真实应用/网络/Mock 监听；**不得将上述静态检查表述为编译通过**。
- 未验收跨平台行为（生成代码在目标运行时的完成行为依赖声明版本）。
- binary/文件型 body 以显式路径占位呈现（不读文件内容）；OAuth 注入、CookieJar 会话、设备网络配置不在快照内（均有注记）。
- SDK/Proto 生成（规格 §5）、套件级 snippet、CLI 输出不在本阶段；`go.mod/go.sum` 未动（仅 P01 授权）。
- 总账本/implementation-status 未勾选，等集成人验证。
