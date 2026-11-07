# C03 phase1 请求派生文档与离线导出 — 交付记录

- 任务：C03 `docs-derived`（phase 1）
- Worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-C03-docs`
- 分支：`vibe/C03-derived-docs`
- 共同基线：`b91ba32`
- 代理：Herdr 派出的 Vibe worktree agent（C03-phase1）

## 1. 实际提交

| 提交 | 内容 |
| --- | --- |
| `f9235f9` | 实现：`internal/docgen/**`（8 源文件 + 7 测试文件）、`internal/desktop/derived_docs{state,ui,actions}.go`、`docs/specs/derived-docs-evidence.md`、`docs/task-results/C03-phase1-integration.patch` |
| （本记录提交） | 仅本交付文档 `docs/task-results/C03-phase1.md` |

共 20 个文件、+5709 行；未改 `go.mod/go.sum`，未改任何公共文件，未动总账本与 implementation-status，未动 B09/其他卡模块，未 merge/push。

## 2. 交付范围与功能

按账本 §24 C03-phase1 行：稳定 ID 引用、派生卡片/参数/响应、行号诊断、受控离线 Markdown/HTML 导出。

- **引用语法 v1 定稿**（受限、可往返）：`[文本](gorequest://request/<id>)` 内联链接 + `<!-- gorequest:request id="…" view="card|parameters|responses" -->` 独占行指令；ID 字符集与项目既有 slug/operationId 对齐；生成块用 `gorequest:begin`/`gorequest:end` 包裹，指令行永不被改写，重复派生幂等（stale 块整体替换），孤立 End 标记与坏语法均产出行号诊断；围栏/缩进内的伪引用惰性（安全方向近似，已记录）。
- **派生接口卡片/参数/响应**：数据源限存储定义 + 作者写入的 `extensions.openapi` contract（`core.GetRequestOpenAPI`）；无 contract 时按启用 KV 派生参数并标注；不发明响应、不输出鉴权值；`{{secret.*}}`/`{{var}}` 保持模板字面。
- **行号诊断**：`DiagnoseRefs` 产出 `core.DocDiagnostic`（缺失请求、坏引用、无卡片引用 info、孤立 End、超限、净化告警）；`MergeDocDiagnostics` 合并 B09 链接/附件诊断并接管 gorequest 目的地条目，行/列/严重度全保留。
- **离线导出**：Markdown 集（展开 `.md` + 附件 + README 索引）与静态 HTML（`.html` + index + 附件 + 内联样式）两格式；`PreviewExport` 零写入给出文件清单/摘录/诊断/冲突/截断；`RunExport` 原子写（同目录 tmp+rename），失败即报告。
- **安全闸门**：目标目录必须在 `.gorequest` store 之外；写入目录逐级拒 symlink；只覆盖自带 `<!-- gorequest:generated -->` 标记的文件（手工文件永不覆盖，冲突逐一列明）；文件数 200 / 总量 64 MiB / 单示例 4 KiB 上限；HTML 全转义、无脚本/无事件/无外链资源/无远程图；href/src 中文空格逐段百分号编码；凭据形态（前缀、长 base64/hex 高熵、PEM、Bearer/Basic 头、URL user:pass）无法证明安全则整值阻断为 `***` 且计数回显；不读 secret provider/最近响应，不解析 `{{var}}` 为真实值。
- **Mermaid**：源码完整保留 + 可见"当前不渲染"警示；不伪称支持；无 CDN/脚本/远程抓取。

## 3. 功能/公共接线

功能全部在本卡专属文件内；公共文件零直接提交。集成所需最小 patch：`docs/task-results/C03-phase1-integration.patch`（3 文件：core/doclinks.go 的 gorequest scheme 解析与诊断映射、documentation.go 的入口按钮/面板/插入菜单/预览展开接线、documentation_actions.go 的链接点击分流；另含 `docMessages` 两字段中英文）。集成冲突面仅 `docHeader` 签名一处。

## 4. 静态验证结果

- `gofmt -l`：本卡全部文件为空；未触碰基线中本就未格式化文件（`body_state.go` 等，保持原样未与他人改动混淆）。
- `gofmt -e` 语法解析：全部通过。
- 符号静态核对：docgen/desktop 对 core、markdown、uikit、mygo 的 58 个引用逐一比对导出声明，全部解析；关键签名（`SaveRequest`、`SetRequestOpenAPI`、`Store.WriteAtomic/Read`、`ui.Local[T]`、`Dialog.Open(Directory:true)`、`Menu.Submenu`）在源码层确认。
- `git apply --check`：patch 对基线可干净应用；为验证已在 worktree 临时应用并核对（gofmt/symbol），随后仅对这 3 个公共文件执行 `git checkout --` 撤回自己的临时改动。
- `git diff --cached --check`：本卡代码/文档无空白错误；patch 文件内部提示为 diff 上下文行固有格式（`+`/空格前缀），非代码问题。
- 依赖：零新增（不触及"仅 P01 可改 go.mod"约束）。

## 5. 待执行用例

`internal/docgen` 7 个测试文件、38 个用例（引用语法往返/扫描定位/诊断合并/幂等展开/更新传播/卡片派生/净化阻断/HTML 渲染子集/导出安全闸门，含 nil-project、symlink、目标冲突、手工文件保护、上限截断等边界），完整期望见 [derived-docs-evidence](../specs/derived-docs-evidence.md) §10。**按本批"先不执行测试"的要求尚未运行**，未以任何形式标记为通过。

## 6. 未编译 / 未人工 / 跨平台未验收

- 未执行 `go build/go vet/go test`，不能声称编译通过；类型层面做了静态核对但可能有遗漏。
- 未启动真实窗口：面板布局、按钮/菜单/诊断行点击、系统目录选择器、状态栏回显、中文排版均待实机验收。
- 跨平台：Windows symlink/路径、真实自然科学排版未在目标平台验证（开发平台 macOS）。

## 7. 限制与后续

- HTML 列表嵌套按扁平模型 + 字节区间包含重建，最深常见两层已覆盖，深嵌套边界情形渲染近似；Mermaid 子集渲染按账本"另期"，本阶段明确不渲染。
- 净化启发式（≥0.30 字节多样性的长 base64/hex 即判凭据）偏保守，可能误阻断正常长值——作者可从卡片尾部计数块察觉并改写示例；短于阈值的伪装凭据理论上可能漏网，故公开前仍需人工审阅。
- 4 空格缩进指令、HTML 锚点 fragment 再编码、生成块围栏自平衡为已记录的句法近似。
- 本卡未实现：跨项目全局索引、导出模板定制、Mermaid 渲染、Snippet（C04 范围）。

交付到此为止，等待集成人应用 patch 并按账本验收；未勾选总账本任何条目。
