# 全分支合并与清理交接（2026-11-06）

用户最新要求：总结进度，改由新版 Herdr 新开一个 CC 负责完成剩余代码集成与清理。下面是实测状态，不根据 agent done 代替验收。

## 当前 Git 状态

- 主目录 `/Users/flyinsky/Documents/Coding/GoLang/GoRequest` 在 main `4142dbc`，尚未快进。保留用户改动 `examples/sample-project/.gorequest/environments/dev.yaml` 及两个未跟踪调试产物 `cmd/gorequest-desktop/__debug_bin1224747532`、`examples/demo-server/__debug_bin3694048428`；不要提交/删除/覆盖这些内容，实际操作前重查。
- 独立集成目录 `../GoRequest-integration`，分支 `integrate/all-agent-deliveries`，当前合并头 `3a8631b`。
- 11 个交付分支已全部通过 no-ff 合入该集成分支，工作分支本身均干净；尚未删除任何旧分支/worktree/Herdr agent。

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

## 已完成但尚未提交的接线

集成 worktree 的 15 个已修改源文件均为上一位集成人本轮工作，正式交给接手 CC 继续，不是用户不明改动：core/auth.go、interop_types.go、model.go、plan.go、yaml_strict.go；desktop/app.go、body_http_ui.go、documentation.go、identity_ui.go、nav.go、settings.go、tree_actions.go、welcome.go、workspace.go、workspace_settings.go。约 +124/-64 行，接手先 git diff 核对保留。

- A03 公共 patch 已应用：OpenAPI 显式路由、严格加载保留 extensions、原生模态和项目菜单。
- A04 公共 patch 已应用：OAuth 模型/clone/校验/严格字段，身份页入口和构建计划时令牌注入。
- B05-UI patch 已组合：方案/凭据/文件夹菜单、模态、发送前目标预检、精确首跳一次确认；重定向不沿用授权。
- UI02 通用 cursor patch 已应用。UI01 标签和 UI03 响应栏本身已有 cursor 修复，未把旧 patch 强套回新布局。曾临时套入 openedMenu 单个 hunk 导致括号错误，已撤回该重复 wrapper，当前源码正常并通过全仓 race。
- UI04 已组合 UI05：品牌 header 仅增加 windowDragBrand(c,left)，保留 UI05 动态安全带/文字位置；工具栏空白与欢迎品牌区域拖拽接线完成。
- UI06 设置组与跨窗口刷新绑定已应用，保留原编辑器/历史设置。
- A01-integration.patch 是接线说明而非代码 diff；UI05 patch 为已提交的 navHeader 同一改动；不得重复应用。

## 已实际通过的验证

- 初始接线 `go build ./...` 通过。
- 最新上述接线 `go test -race ./...` 全包通过，desktop 86.776s；日志 `/private/tmp/gorequest-all-first-race.log`。
- `go test -race -tags ui04_integration ./internal/desktop -run 'TestWindowDrag|TestBrandChrome|TestMarkdown|TestResponseToolbar|TestUI0' -count=1` 通过，desktop 23.871s；日志 `/private/tmp/gorequest-all-ui-combo.log`。
- 鉴权/OAuth/生产确认专项 `go test ./internal/desktop -run 'TestOAuth|TestIdentityManagement|TestProductionPolicy' -v -timeout=40s` 通过，5.272s；日志 `/private/tmp/gorequest-all-auth-diagnostic.log`。
- 源码 whitespace 检查通过。最终 go vet 需要重跑（此前一次运行恰逢临时括号错误，不应记录为最终通过）。
- 未做真实窗口/macOS 拖动/三平台实机验收，不得伪填通过。

## 接手 CC 的剩余工作（先核对，修复必要组合问题，避免扩成新产品任务）

1. 核对全部差异与现有交付报告，完成公共接线的审查/组合回归。下面已发现或怀疑的跨卡问题尚未修改：
   - `identity_management_test.go/TestProductionPolicyPromptIsOneShotAndExact` 原来预设接线前拒绝发送，接线后确认会访问 `https://api.example.com`。必须改为 httptest 本地服务，验证真正确认发送及无隐式公网请求，不因旧测试通过就保留公网调用。
   - `oauth_ui.go/resolveActiveOAuthScheme` 最后 fallback 无条件读取 Entry.Request.Auth 的旧 inline OAuth；如果用户在 UI 选择 none/其他模式，可能错误恢复旧 OAuth。应以当前模式为准并加回归，避免凭据/旧模式泄漏。
   - B05 `draftFromScheme` 对 oauth2/oidc 落入 default 并改为 bearer，toScheme 也不保留 OAuth 字段。与 A04 集成后编辑/保存方案不能静默损失 OAuth；最小安全保留或明确只读/拒绝不支持编辑，并用无真实系统凭据测试覆盖。
   - A04 的 ref/inherit OAuth 配置编辑目前直接改内存方案、只标请求 Dirty，保存请求可能不会保存方案配置。核对该路径；必要时提供明确、带摘要保护的保存路径，或清楚限制编辑避免虚假保存；不要隐式写每次键入/覆盖外改。
   - A01 与 A03 各自 Schema 端口不完全相同，尚无跨包 adapter。可保留明确未接线的边界，不要伪称联合验收/完整Runner UI（A02尚未开发）；如做最小adapter，需精确版本/引用边界及对应测试。
   - 请求模式列表尚未包含oauth2/oidc，A04只能经已存在ref/inline进入。按最终实际可用性决定最小接线，不扩大高级鉴权范围；不要只加模式但保存/发包走旧逻辑。
2. 修复后重新 go build ./...、go vet ./...、go test -race ./... 以及有变化/必要的组合测试（包含ui04_integration）。不用无限重复已通过且未改动的专项；有新改动再跑相关门禁。
3. 更新 docs/implementation-status.md、docs/parallel-work-plan.md（领取/合并/验证/限制、UI子项代码集成完成；真实人工待验收仍保留），写最终集成报告。仅提交本次集成文件，不纳入用户环境/二进制。当前交接文档可随集成提交，不要把临时日志/账号信息提交。
4. 本地 commit 接线与必要修复，确认 main 未被他人推进；主目录保留用户dirty，差异无 examples/debug 交叉时 git merge --ff-only integrate/all-agent-deliveries。若main变化需合理串行合并并验证，不能reset/stash丢用户改动。用户已授权本地commit、合并、删除本轮旧分支和关闭已交付agent；不需再询问，不push。
5. 确认每个旧分支都是main祖先、worktree干净后，关闭下列11个旧agent tab，再用不带force的git worktree remove/git branch -d删除旧worktree/分支。不能删除未合并提交或未提交内容；有新变动先核实。保留main与用户当前对话 t1h，自己的新集成CC终端保留供用户查看交付。
6. 在main目录结束工作，集成worktree干净且分支已进入main后可从main删除集成worktree/branch。不要在被删除目录里继续执行命令。记录清理结果及最终提交，核对hc ls只剩当前主会话/新集成人和用户其他独立终端。

## 旧 Herdr agent 与清理句柄

以下11个全部由本轮用户派工建立，用户明确要求完成后关闭；不能关闭 t1h 或停止 Herdr app/core。

| 任务 | terminal | tab | worktree / branch |
| --- | --- | --- | --- |
| A01 | `t2b` | `tb59577f82fc32054b9d9677311ae36551` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A01` / `cc/A01-runner-core` |
| A03 | `t2c` | `tbbb55861f81df7a2a65b5e2a8e5def7e1` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A03` / `agy/A03-openapi-schema` |
| B05-UI | `t2d` | `tb11670d51e26cfd5f1cfafb9c5216524a` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-B05-UI` / `cc/B05-identity-ui` |
| A04 | `t2f` | `tbc76e46e2e7d11f3263f31cbed76c8740` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A04` / `agy/A04-oauth-oidc` |
| R01-parser | `t2g` | `tb6d1cc7d68c50c8739f0dae7822ed351f` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-R01-parser` / `cc/R01-core-parser` |
| UI01 | `t2h` | `tba6311f6a4d13cd3f47a95ffa88c8c735` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-UI01-tabs` / `codex/UI01-tabs` |
| UI02 | `t2j` | `tb2c214198624c5d1df0da742ffccb4a55` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-UI02-cursors` / `codex/UI02-cursors` |
| UI03 | `t2k` | `tbbce012658901615a4b9862379369656e` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-UI03-response` / `codex/UI03-response-toolbar` |
| UI04 | `t2m` | `tba929cef77386f4d297c9924ca7c343d0` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-UI04-window-drag` / `codex/UI04-window-drag` |
| UI05 | `t2n` | `tbd59bfd0d42cbcc1f985205eb42d96e25` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-UI05-brand-position` / `codex/UI05-brand-position` |
| UI06 | `t2p` | `tbb334d79b5e3069136d41f33a19aeb9a1` | `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-UI06-markdown-preview` / `codex/UI06-markdown-preview` |

按 `~/.config/herdr-app/AGENTS.md` 使用新版 `herdr-app do tab.close '{"id":"实际tab","confirmed":true}'`；只操作精确ID。终端done/idle不代替提交祖先和clean检查。原始临时启动资料可以消失，以上表格是持久交接证据。接手不再启动子agent、不派新功能任务，只完成集成和清理。
