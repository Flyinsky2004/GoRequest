# R01-parser 交付记录 — 文档 Core 依赖解耦

日期：2026-11-06
任务：`R01-parser` 独立子任务（完整 R01 仍未完成）
分支：`cc/R01-core-parser`
实际目录：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-R01-parser`
共同基线：`4223cd48612ac5925a2451e391184b5551d35066`
提交：本文件与功能改动合并为一次本地提交；提交哈希在提交后由交付回复报告。

## 1. 实际范围

只做文档 Core 对 `github.com/egoist/mygo/ui/markdown/parser` 的直接/传递依赖解耦，保留 B09 文档语义与兼容边界。

- 新增专属中性解析包 `internal/markdown`（`markdown.go`、`math.go`、`markdown_test.go`）：基于本机核对过源码的 `github.com/yuin/goldmark v1.8.2`（纯 Go、MIT、只依赖标准库、CommonMark 0.31.2），提供 `Parse`/`Document`/`Block`/`Inline`/`PlainText` 小接口；用官方扩展点补上旧 `parser.Default` 的 `$$` 块与 `$$…$$`、`\(…\)`、`\[…\]` Math。未复制 MyGo parser 源码，未新增 vendor 目录。
- `internal/core/documentation.go`、`internal/core/doclinks.go` 只把文档解析/大纲/链接/诊断/兼容矩阵改接 `internal/markdown`；搜索、Store、ChangeSet、Redactor、网络、历史、保真保存、桌面 UI 均未改动。
- `internal/core/parser_dependency_test.go` 静态扫描 `internal/core/*.go` 的全部 import，禁止 `github.com/egoist/mygo` 及其子路径回归。
- `go.mod`/`go.sum` 本轮仅新增 goldmark 一条直接与两条校验和；未替其他并行卡添加依赖。
- `docs/specs/markdown-core-evidence.md`：选型依据、本机源码核对清单、依赖图证明、语义回归与明确差异。
- `docs/task-results/R01-parser.md`：本记录。

没有生成 `R01-parser-integration.patch`：本卡没有改公共入口/字段/格式，Core 与桌面之间的既有导出 API（`ExtractHeadings`、`DiagnoseDoc`、`MarkdownCompatibility`、`ResolveDocLink` 等）保持不变，集成人无需接线。桌面仍用 MyGo Native UI/预览渲染器；不做 C03 Mermaid、请求派生卡片、整套导出；不改 A01/A03/A04/B05-UI 或其它卡。

## 2. 核对过的现状与规格

先读 `AGENTS.md`、`docs/parallel-work-plan.md` §10/§11、`docs/implementation-status.md` §4/§4.1、`docs/task-results/integration-foundation-2026-11-06.md`、`docs/task-results/B09.md`、`docs/specs/core-workspace-http.md` §5、`docs/specs/documentation-codegen.md`、`docs/decisions.md`、`docs/acceptance.md`，再核对实际代码。确认 B09 文档工作台、B01–B08 集成修复已在共同基线；`implementation-status.md` §4 与 `integration-2026-11-06.md` 明确记录“Core 直接依赖 MyGo parser 子包，严格移除全部 MyGo 包路径仍待 R01”，本卡只处理这一项，不重复实现旧差距。

## 3. 验证（本机 darwin/arm64、Go 1.27.3）

离线、只读模块模式下执行：

```text
$ go list -deps ./internal/core | grep -i mygo
（无输出，grep 退出码 1）
$ go list -deps ./internal/markdown ./internal/core | grep -E 'mygo|gorequest/internal' | sort -u
gorequest/internal/core
gorequest/internal/markdown
$ go list -deps ./cmd/gorequest-desktop | grep -c 'github.com/egoist/mygo'
30
$ go mod verify
all modules verified
go build ./...                 # 通过
go vet ./...                   # 通过
go test -race ./... -count=1   # 通过（9 个包，含 internal/desktop 55s）
go test -cover ./internal/markdown ./internal/core
ok  gorequest/internal/markdown   coverage: 83.9% of statements
ok  gorequest/internal/core       coverage: 66.9% of statements
```

Core 覆盖率是全包既有基线，本卡不宣称整仓 80%；专属 `internal/markdown` 新增包覆盖 83.9%。

回归清单：

- B09 30 行 `TestMarkdownCompatibilityMatrixMatchesParser` 全部保留原布尔断言，含单美元 Math/front matter/定义列表/Mermaid 为 false；没有降低旧断言换取通过。
- 大纲/搜索/链接/附件/诊断的既有 Core 与 Desktop 无窗口测试全部继续通过：中文/空格路径、百分号编码、引用链接、图片、附件扩展名白名单、标题纯文本、GFM 表格、代码块里的伪标题、危险协议、远程图片、缺失锚点、Mermaid 源码提示、位置排序、附件不覆盖、项目隔离、脏/清洁 Tab 状态等。
- 新增中性包测试：成功/失败/边界/并发隔离，覆盖标题层级与容器、纯文本、内联链接/图片/自动链接/强调/删除线/HTML、实体与反斜杠转义、GFM 块、脚注、数学、软/硬换行、畸形与 CRLF 输入、源偏移边界、8 goroutine × 20 次 `-race` 并发解析。
- 新增 `TestCoreHasNoDesktopToolkitImports` 防止任何 MyGo 包路径回归 Core。
- 删除所有临时 `.probe` 探针文件与临时 coverage 文件；未写真实系统凭据、历史、设备配置，未访问真实远程仓库。

## 4. 限制与明确差异

详见 [markdown-core-evidence.md](../specs/markdown-core-evidence.md) §5。关键几点：

1. 桌面预览仍由 MyGo Native 组件渲染，Core 用 goldmark 做大纲/诊断/兼容矩阵；B09 探针结论一致但不再是同一 AST，未列矩阵语法的预览一致性未逐项证明。证据文档已如实记录，没有伪造等价。
2. GFM Linkify 默认规则与旧解析器对任意裸 URL/邮箱的边界可能不同；旧解析器未公开的边界不做逐字节等价保证。
3. 脚注定义由 goldmark 重排到文末，块起始位置取定义正文首个子块；B09 只消费存在性布尔值。
4. 块/内联偏移在已测场景一致，但未对全部 CommonMark 构造做逐字节等价证明；容器型内联 `End` 只用于粗定位，Core 诊断使用 `Start`。
5. 未实现 C03 Mermaid 渲染、派生接口卡片、整套离线导出；未做真实窗口、Windows/Linux 实机、签名、无障碍、高 DPI、中文字体与输入法验收；未验证真实原生文件对话框/系统浏览器。
6. 完整 R01 跨平台/签名/无障碍等继续未完成，本卡只交文档 Core 依赖解耦与相应自动化；不自行勾选总卡。

## 5. 集成人接线步骤

- 无需代码 patch：`internal/core` 的导出函数签名、`DocDiagnostic`/`Heading`/`LinkTarget`/`MarkdownFeature` 结构与稳定 `Kind` 字符串全部未变；桌面调用点无需改动。
- 合并前/后请在本分支或集成分支复跑 `go build ./...`、`go vet ./...`、`go test -race ./...`，以及 `go list -deps ./internal/core | grep -i mygo`（应无输出）。
- `go.mod`/`go.sum` 仅加入 `github.com/yuin/goldmark v1.8.2`；若其它并行卡交来依赖清单/补丁，以它们的精确差异叠加，避免覆盖本卡两条 `go.sum` 校验和。
- 集成后由集成人统一更新 `docs/parallel-work-plan.md`、`docs/implementation-status.md`；可把 §4 的“严格移除全部 MyGo 包路径仍待 R01”改为“Core 依赖图已无 MyGo 包路径，桌面仍保留 MyGo Native UI”，但不勾选整张 R01。
- 真实窗口/三平台预览一致性和完整 R01 验收仍列为未完成项。

## 6. 会话可接管信息

本卡在指定 worktree/分支完成，未 merge/cherry-pick/push，未操作 main、其他 agent/worktree、共享 `.git` 配置，未 reset/clean。工作树只含本卡文件；提交后保持会话可接管，不自动继续 A01/A03/A04/B05-UI 或其它卡。
