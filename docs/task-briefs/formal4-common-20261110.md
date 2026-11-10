# F01–F04 并行开发共同约定（2026-11-10）

用户已同意四路功能开发，并要求通过 Herdr 开 Claude Code。主代理负责先给各路实施方案、共同接口/文件归属、集成和持续验收。不是仅做规划：各路应实际实现、测试、本地 commit、交付报告。完整正式版仍按 docs/roadmap.md 和 docs/acceptance.md；本批阶段完成不得冒充完整1.0。

## 基线与隔离

代码起点 main `d0be2fa`，当前只有用户 `examples/sample-project/.gorequest/environments/dev.yaml` 修改及未跟踪 `docs/document.md`，两者不触碰。所有 worktree 从本批方案/账本的共同登记提交创建，精确 SHA 见账本 §51.1。每路一个独立分支/worktree，不从其他代理目录开发，不创建子代理，不 merge/main/push/部署，不关闭其他会话、不重启用户应用。沿用 CC 默认模型，不改全局配置/凭据/Herdr文件/模块缓存。

先读 AGENTS.md、账本最新记录、roadmap/acceptance、对应 spec 和本卡方案，核对真实代码，不能把旧来源报告“待接线/未跑”当当前状态。当前N01–N05、M01–M04均已集成；历史四项失败已修，禁止沿用 skip 清单。主代理本批重跑build/vet/普通全仓测试，结果后补。每卡仍需实际运行相关验证。

## 公共文件归属和依赖

- F01 独占 `internal/runner/**`（现有报告/断言/调度/Schema）、`internal/desktop/runner*.go`、`suite_editor*.go` 和必要 OAuth desktop 接入文件。新增鉴权/协议扩展端口也归 F01。
- F02 独占 `internal/core/graphql*.go`、`internal/websocket/**`、`internal/stream/**`、`internal/desktop/graphql*.go`、`websocket*.go`；新增 `internal/protocolrun/**` 和对应新桌面文件。不得直接改 runner 目录，与 F01 通过契约和独立 adapter/接线 patch 交付。
- F03 独占新增 `internal/diagram/**`、`internal/desktop/doc_diagram*.go`、`internal/docgen/render.go`/对应图表导出实现及测试；对 doc_live.go、doc_code.go、documentation.go、markdown diagnostics 的改动只交最小 integration patch，由集成人串行应用。
- F04 独占 `internal/sdkgen/**`、`internal/desktop/sdk*.go`；不改 core OpenAPI模型/runner/snippet/图表。
- `app.go`、`workspace.go`、`body_http_ui.go`、`model.go`、`go.mod/go.sum`、AGENTS、总账本/implementation-status/decisions 与 release workflow 由集成人独占。需要改公共文件时交 `docs/task-results/Fxx-integration.patch`（基于共同基线、只含必要入口）和理由；不在分支直接改。私有各路新增文件正常提交。F01可在独占runner_ui内接本路入口；跨路接口由集成人处理。
- 各路不要编辑总账本。只写自己的 `docs/task-results/Fxx.md`、必要的 `docs/contracts/Fxx-*.md`/本路能力矩阵。集成人统一登记真实来源提交、集成和验收。

F01开工第一阶段产出 `docs/contracts/F01-runner-extension.md`：只基于真实Runner APIs，明确非交互OAuth token注入与协议执行扩展的最小输入/输出、预检/取消/敏感传播/结果分类。不要假定HTTP Response能表达全部流式事实。F02可立即做SDL/订阅和独立有界WS动作执行；协议Runner glue依赖F01已验证的port。看不到端口时继续本路独立工作，报告依赖，不复制一套Runner。F01端口提交完成后，向主代理报告hash/path；主代理负责传递并集成契约，不自行越权发消息给其他会话。

## 安全和产品约束

纯Go Core不依赖GUI，MyGo Native UI，不引入WebView/在线CDN/隐式工具下载。打开项目不发网、不启动Mock/订阅/浏览器/生成器。真实Secret/生产服务不访问，测试用loopback/fake secrets。TLS、目标策略、跨源凭据过滤、取消和资源上限不放宽。引用/未知字段保真，输入/生成文件摘要冲突和回滚保护不退化。

新增依赖必须具体说明版本/许可证/用途，交 go.mod/go.sum patch 由集成人决定，本批不自行升级Go/MyGo。各路有UI工作时应用本地 ui-ux-pro-max 技能的相关原生交互规则；技术协议按当前官方主文档查证并记录链接/兼容范围，不按记忆猜字段。无真实窗口/IME/平台证据不声称实机通过。

## 验证与交付

先复现/建立本阶段验收矩阵，再实现。各路必跑 `go build ./...`、`go vet ./...`、对应完整包测试和新增关键路径race，必要桌面专项。任何未运行项明确列出，禁止删断言/skip掩盖失败。公共patch临时接入验证应在自己worktree保存原内容、精确恢复且不带入公共文件提交；必须记录是否仅模块通过/入口已接。

本地commit前审diff/staged，只含本卡。报告写：基线与分支/worktree、代码/接口提交hash、实际入口/接线patch、完成矩阵、命令与结果/日志、截图或原生控件证据、限制与剩余依赖。完成先给阶段结果，保持Herdr交互会话可接管；不自行合并/清理/标完整卡完成。集成人承担组合测试/回归/真实窗口可执行验收，用户只协助无法访问的平台和真实输入法等。

## 用户新增强制规则：LESSONS.md（2026-11-10）

开工/续做必须先读仓库根 LESSONS.md，并检查本卡是否重犯已有问题。实际犯过的错误、用户指出的问题及复盘得到的教训，必须及时写入本 worktree 的 LESSONS.md，记录场景/根因/修正/预防和证据，不能只口头总结。旧worktree缺文件时先读取主工作区 `/Users/flyinsky/Documents/Coding/GoLang/GoRequest/LESSONS.md` 并复制为本地起始版本再追加。不要写主工作区文件；集成人合并各路教训。交付前核对新增教训已落盘。如获授权再创建其他agent，首条任务必须明确宣告这条规则；本批原有“不启动子代理”仍有效。

用户指定条目格式：`**<一句话规则>**` 后依次四个 bullet：事发commit/issue与后果、修复commit、`Done right: <做对的例子>`、`Seen N× (YYYY-MM-DD, YYYY-MM-DD).`。Seen只计独立事件，不把重复测试/提醒计成重犯；未知哈希明确待定位。按主线最新LESSONS模板整理本路条目，保留已新增教训，不覆盖丢失。
