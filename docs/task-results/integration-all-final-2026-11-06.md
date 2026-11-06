# 全分支集成最终报告（2026-11-06）

接手人：Herdr 终端 `t2r` / tab `tbc11684376b52d0324d8a2c4c1f8483e0`（集成与清理 · CC）。用户授权本地 commit、ff-only 合并到 main、删除本轮旧分支/worktree 与关闭旧交付 agent；不 push、不改共享 git config、不 reset/clean。用户自本会话起不再并发写集成 worktree。

## 1. 起点与继承

- 主目录 `/Users/flyinsky/Documents/Coding/GoLang/GoRequest` 在 main `4142dbc`，保留用户 dirty：`examples/sample-project/.gorequest/environments/dev.yaml` 及未跟踪调试二进制；全程未提交/删除/覆盖。
- 集成目录 `../GoRequest-integration` / 分支 `integrate/all-agent-deliveries`：上一位集成人已完成 11 条交付分支的 no-ff 合并（合并头 `3a8631b`），并在 `15b7955` 仅提交交接文档与账本。工作区 15 个已修改源文件是**公共接线**，由本会话继承，不是未知用户 dirty。本会话不重做 11 次合并。
- 完整交接事实见 [integration-all-handoff-2026-11-06.md](integration-all-handoff-2026-11-06.md)；领取/进度登记见 [账本 §14.1](../parallel-work-plan.md)。

## 2. 已合并的 11 条交付

| 分支 | 合并提交 |
| --- | --- |
| `cc/R01-core-parser` | `fb3bd92` |
| `cc/A01-runner-core` | `744a870` |
| `agy/A03-openapi-schema` | `d4ac4ac` |
| `agy/A04-oauth-oidc` | `e2fcfb9` |
| `cc/B05-identity-ui` | `178e7ff` |
| `codex/UI01-tabs` | `45cf374` |
| `codex/UI02-cursors` | `ffba532` |
| `codex/UI03-response-toolbar` | `666018a` |
| `codex/UI04-window-drag` | `5f359db` |
| `codex/UI05-brand-position` | `e5dedcc` |
| `codex/UI06-markdown-preview` | `3a8631b` |

## 3. 公共接线（继承并完成）

- A03：OpenAPI 显式路由、严格加载保留 extensions、原生模态和项目菜单。
- A04：OAuth 模型/clone/校验/严格字段、身份页入口、构建计划时令牌注入。
- B05-UI：方案/凭据/文件夹菜单、模态、发送前目标预检、精确首跳一次确认；重定向不沿用授权。
- UI02 通用 cursor patch；UI01/UI03 保留各自布局内已有 cursor 修复。
- UI04 组合 UI05：品牌 header 的 `windowDragBrand(c,left)`，保留 UI05 动态安全带/文字位置；工具栏空白与欢迎品牌区域拖拽接线。
- UI06 设置组与跨窗口刷新绑定，保留原编辑器/历史设置。
- A01-integration.patch / UI05 patch 为说明或已提交同一改动，未重复应用。

## 4. 本会话必要跨卡修复

均配有无窗口回归测试（`internal/desktop/integration_crosscard_test.go`、`internal/core/oauth_test.go`、`internal/desktop/identity_management_test.go`），未扩成完整 A02 或新产品功能。

1. **生产确认测试去公网**：`TestProductionPolicyPromptIsOneShotAndExact` 改用本地 `httptest` 服务。断言确认后真实发送、恰好命中本地服务一次、方法正确；未确认时提示仍要求确认且不再评估不发任何请求。消除对 `https://api.example.com` 的隐式公网依赖。
2. **OAuth 当前模式为准**：`resolveActiveOAuthScheme` 以 `tab.AuthMode` 分支判定；`none`/`bearer`/`ref`(非 OAuth) 不再无条件回退读取 `Request.Auth` 的旧 inline OAuth，避免用户选 none 后旧凭据/模式复活。新增 `TestResolveActiveOAuthSchemeHonorsCurrentMode`。
3. **B05 方案编辑不丢 OAuth**：`draftFromScheme` 对 oauth2/oidc 保留真实类型与 `OAuthConfig`、标记 `Unsupported`；`toScheme` 拒绝把只读草稿写回；方案编辑器隐藏类型 Select/字段/保存按钮并显示只读说明。防止编辑保存静默把 OAuth 转成 bearer 丢字段。新增 `TestSchemeDraftPreservesOAuthFields`、`TestSchemeEditorOAuthIsReadOnly`。
4. **ref/inherit OAuth 不伪称保存**：请求 Auth 页对 ref/inherit 解析出的 OAuth 方案渲染只读检视面板（明确"此处编辑不会被保存；请在身份管理方案编辑器修改"），不再改内存方案、不标请求 Dirty、不提供流按钮。内联 oauth2/oidc 模式仍可编辑且随请求保存。新增 `TestRefOAuthEditorIsReadOnlyAndDoesNotMutateScheme`。
5. **OAuth 单一序列化表示**：`SetOAuthConfig` 在存在原生 `AuthScheme.OAuth` hook 时只写该字段并清除遗留 `extensions.oauth`；此前两者都写导致 YAML 中 OAuth 配置出现两份、严格加载器拒绝新保存的请求文件。`ExtractOAuthConfig` 保留对旧 `extensions.oauth` 文件的兼容读取。新增 `TestSetOAuthConfigUsesSingleRepresentation`。
6. **oauth2/oidc 成为一等请求模式**：`requestAuthModes` 加入两者；新建内联方案由 `ensureInlineOAuthScheme` 建立，`authRefFromTab` 随请求保存，`fillAuth` 重开还原，`buildTrackedTabPlan` 发包时注入令牌——不是"只加模式但保存/发包走旧逻辑"。新增 `TestOAuthModesAreSelectableAndRoundTrip`（含落盘重开往返）。

## 5. 验证证据

在 darwin/arm64、Go 1.27.3，集成 worktree 内执行：

| 门禁 | 结果 |
| --- | --- |
| `go build ./...` | 通过 |
| `go vet ./...` | 通过（最终重跑，非此前括号错误那次） |
| `go test -race ./... -count=1` | 全包通过；日志 `/private/tmp/gorequest-final-race.log` |
| `go test -race -tags ui04_integration ./internal/desktop -run 'TestWindowDrag\|TestBrandChrome\|TestMarkdown\|TestResponseToolbar\|TestUI0' -count=1` | 通过 |
| 鉴权/OAuth/生产确认与跨卡回归专项 | 通过 |

## 6. 如实记录的限制（未伪称通过）

- **A01 Runner Core 与 A03 Schema 端口未联合接入**：两卡各自实现 Schema 校验端口，尚无跨包 adapter，不做 Runner/Schema 联合验收声明；A02 桌面 Runner/断言/报告 UI 尚未开发，Runner Core 为独立模块。本轮按交接不为此扩大范围。
- 未做真实窗口/macOS 顶部拖动、系统钥匙串/浏览器真实 OAuth 流程、原生文件保存对话框、Windows/Linux 三平台实机及高 DPI/输入法人工验收；相应逻辑仅由无窗口 `ui.NewTester` 与 `httptest` 覆盖，不勾为人工通过。
- 真实远程 Git 凭据、跨进程多实例本地历史写入协调、字节级原始排版全实体保真等仍按既有账本 §5 保持未完成。

## 7. 交付动作与结果

- 仅提交本次集成相关文件（接线、跨卡修复、回归测试、账本/状态/最终报告）；不含用户环境 yaml、调试二进制、临时日志或账号资料。集成分支提交 `integrate(all): finish public wiring and cross-card fixes for all 11 deliveries`（25 files / +806/-141）。
- main 未被他人推进，从主目录执行 `git merge --ff-only integrate/all-agent-deliveries` 成功，main 从 `4142dbc` 快进到 **`ac06595`**（最终集成提交，130 files / +34328/-378 相对旧 main，含全部 11 条交付）。主目录原有 dirty 完整保留。
- 精确关闭的 11 个旧交付 agent tab（`herdr-app do tab.close` + 精确 ID + `confirmed:true`，全部 `ok:true`）：A01 `tb59577f82fc32054b9d9677311ae36551`、A03 `tbbb55861f81df7a2a65b5e2a8e5def7e1`、B05-UI `tb11670d51e26cfd5f1cfafb9c5216524a`、A04 `tbc76e46e2e7d11f3263f31cbed76c8740`、R01 `tb6d1cc7d68c50c8739f0dae7822ed351f`、UI01 `tba6311f6a4d13cd3f47a95ffa88c8c735`、UI02 `tb2c214198624c5d1df0da742ffccb4a55`、UI03 `tbbce012658901615a4b9862379369656e`、UI04 `tba929cef77386f4d297c9924ca7c343d0`、UI05 `tbd59bfd0d42cbcc1f985205eb42d96e25`、UI06 `tbb334d79b5e3069136d41f33a19aeb9a1`。未关闭 `t1h`、未停止 Herdr app/core、未关闭本会话 `t2r`。
- 删除的 11 个旧分支/worktree（逐个复核 CLEAN 且为 main 祖先，均使用不带 force 的 `git worktree remove` / `git branch -d`）：`cc/A01-runner-core`、`agy/A03-openapi-schema`、`agy/A04-oauth-oidc`、`cc/B05-identity-ui`、`cc/R01-core-parser`、`codex/UI01-tabs`、`codex/UI02-cursors`、`codex/UI03-response-toolbar`、`codex/UI04-window-drag`、`codex/UI05-brand-position`、`codex/UI06-markdown-preview`（对应 `../GoRequest-A01` 等 11 个目录）。集成 worktree `../GoRequest-integration` 干净且分支已是 main 祖先，随后从主目录删除其 worktree 与 `integrate/all-agent-deliveries` 分支。
- 最终状态：`git worktree list` 仅主目录、`git branch` 仅 `main`；`hc ls` 仅 `t1h`（用户）与 `t2r`（本集成 CC，保留供查看交付）。主目录 dirty：`M examples/sample-project/.gorequest/environments/dev.yaml`、`?? examples/demo-server/__debug_bin3694048428` 均保留。不 push、不改共享 git config、不 reset/clean。
