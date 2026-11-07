# A02 phase 2 交付记录：Runner 断言与提取编辑器

负责人：Vibe（Mistral Vibe，用户经 Herdr 派出）；worktree `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A02-editor`；分支 `vibe/A02-assertion-editor`；共同基线 `b91ba32fdb7b6c5c039bd74701da75f83bab27d7`。不勾总账本/A02 整卡。

## 实际提交

- `12b0a7f` — `feat(a02-phase2): structured Runner assertion & extraction editor`，7 文件，+2803/−3，仅本卡文件：
  - 新增 `internal/runner/editorview.go`；
  - 新增 `internal/desktop/runner_editor.go` / `runner_editor_ui.go` / `runner_editor_i18n.go` / `runner_editor_test.go`；
  - 修改 `internal/desktop/runner_ui.go`（入口按钮、编辑器模态渲染、结果详情断言/提取行）、`internal/desktop/runner_i18n.go`（runnerMessages 增加一个 `EditorEntry` 字段，双语值位置一致）。

## 功能与公共接线

- **无需公共 patch**：phase 1 的两行公共接线（`app.go` Modal 列表 / `workspace.go` 项目菜单）已在共同基线内；编辑器入口放在已有 Runner 模态内（"编辑断言与提取"按钮），`RunnerEditorModals` 由 `RunnerModals` 调用渲染，未改任何集成人拥有的公共文件。
- **编辑目标都是引擎真实消费的存储，不发明 DSL**：
  - 断言：当前已保存请求的 `Request.Assert []yaml.Node`（A01 已有字段），结构化新增/编辑/删除 status/header/jsonpath/body 四类目标，完整覆盖引擎全部 8 种 matcher（exists/equals/notEquals/in/contains/notContains/regex/type）与带类型的字面量（string/number/boolean/null，字符串不做隐式类型转换）；`message` 可选。
  - 提取：引用当前请求的已保存 Suite 步骤的 `extract:` 列表（`runner.Extraction`：name/from/path/header/cookie/regex/group/required/sensitive，required 与 sensitive 三态保留 `*bool` 的 nil/default 语义，敏感默认启发式在 UI 明示 "(default)"）。
- **internal/runner 最小增量**（仅新增 `editorview.go`，零引擎行为改动，先包内静态核对）：`runner.DescribeAssertions`（逐条描述，坏节点报 `assertion N: ...` 定位且节点原样保留）、`runner.ValidateAssertions`（保存前引擎校验，含定位）、`runner.ValidateExtractions`、`runner.ExtractionEffectiveSensitive`。解析/校验全部复用现有 `parseAssertion`/`parseExtractions` 能力，UI 不复制解析逻辑。
- **未知/未支持节点不静默丢失**：durationMs/size/schema 目标与无法解析的节点按原节点深拷贝原样保留（注释/样式随节点走），仅可显式删除；规整行保存时经 `MergeRequestYAML` 位置比对，未修改行的原格式与注释逐字保留。
- **保存与冲突保护**：请求经 `SaveRequestPreserving(entry, req, entry.BaselineDigest)`（与本卡 Tab 保存同一基线摘要通道）；Suite 手术式补丁后先 `runner.ParseSuite`+`ValidateStructure` 整文件重校验（错误带步骤定位）再 `Store.SaveChecked(rel, bytes, digest)`。冲突显式提示"文件在磁盘上已被修改，请重载"，不覆盖。
- **脏 Tab/外部修改显式处理**：编辑器打开时列出该请求的脏 Tab 数并提示；保存后仅刷新该窗口的非脏 Tab（`fillTabFromRequest`+新基线），脏 Tab 保留其编辑，其旧基线会被后续保存显式冲突，绝不静默覆盖磁盘结果。
- **秘密**：编辑器不读取也不写入任何秘密值；规则值按用户输入原样保存 `{{secret.name}}` 引用，仅在运行时由引擎解析；提取规则的敏感值从不显示（声明只显示名称与三态）。
- **窗口独立/更新有界**：状态经 weak App key 关联（镜像 `runnerState` 生命周期），双窗口互不串；关窗/Escape/点外部关闭一律丢弃草稿；编辑器全 UI 线程同步有界操作（Suite 扫描 ≤32 个文件、每文件 ≤1 MiB），无后台 goroutine、无空闲 ticker/动画。

## 静态验证（本批约束：不运行测试/构建/vet）

- `gofmt -l` 本卡 7 文件 + `internal/`：本卡文件全部干净（`internal/` 列出的既有未格式化文件均非本卡改动，未触碰）。
- `git diff --check` 与 `git diff --cached --check`：exit 0。
- 引用核对：runner 交叉引用脚本（30 个 `runner.X` 符号全部在包内定义）；desktop 侧 `runnerEditor*`/`runnerMessages` 字段逐一比对无缺失/无重复；与既有 `runner*.go` 及全 `internal/` 无标识符冲突（`boolPtr`/`runnerEditor*` 均为本卡首定义）；词条字段与 EN/ZH 键集逐一对齐（脚本核对，修复了模型切换中断期间一处字面量键残留与 matcher 草稿结构体字段类型不一致）。
- 公共文件 `git status`/`git log` 核对：工作区仅本卡 7 文件被修改/新增；`go.mod/go.sum` 未改（非 P01）；总账本/implementation-status 未改。
- **上述均为静态检查，不构成编译/测试通过声明。**

## 待执行用例（已写、未运行；`internal/desktop/runner_editor_test.go`）

`go test ./internal/desktop -run 'TestRunnerEditor' -count=1`：

1. `TestRunnerEditorDescribeAssertions` — 四个结构化目标描述、durationMs/schema 原样保留、坏节点精确报错。
2. `TestRunnerEditorAssertionRebuildRoundTrip` — spec→节点重建整列表通过 `runner.ValidateAssertions`，保留节点逐字进入输出。
3. `TestRunnerEditorSpecFromFormErrors` — 表单错误精确定位；`exists:false` 与其他 matcher 组合被引擎拒绝。
4. `TestRunnerEditorCSVLiterals` — in 列表标量类型推断。
5. `TestRunnerEditorExtractFormTriState` — required/sensitive 三态往返无损。
6. `TestRunnerEditorPatchSuiteExtract` — 手术补丁：注释/其他步骤/键序保留；清空删除 extract 键；未知 from 报错带名称；越界步骤失败。
7. `TestRunnerEditorStateSavesRequestAssertions` — 真实保存流（临时项目）：新增→保存（校验基线摘要更新）→删除→保存；"Nothing to save"提示。
8. `TestRunnerEditorSuiteTargetsAndSave` — Suite 目标发现（Request 按 RelPath/id 双匹配，镜像 prepare.go）、提取保存、注释保留。
9. `TestRunnerEditorRendersHeadlessSmoke` — headless 渲染模态（1000x700 视口），标题/断言行/脏 Tab 提示可见，关闭清草稿。headless 渲染不是人工窗口验收。

## 未编译/未运行/人工与跨平台未验收

- 本批未执行 `go build/go vet/go test`、未启动应用：**交付树未经过编译验证**；集成人应用面板后请先 `go build ./...` 与上述用例。
- 真实窗口交互、双语界面人工核对、原生对话框、macOS/Windows/Linux 实机、accessibleName/输入法/高 DPI、大项目（>32 Suite 或大量断言）响应性均未验收。
- Suite 步骤级 `assert:`（叠加在请求断言之上的步骤断言）本阶段不可结构化编辑，原样保留；不存在"请求级提取"存储——提取规则唯一真实存储是 Suite 步骤，空目标时给显式指引不自动建文件。
- Suite 文件编辑每文件限 1 MiB 与 32 文件扫描上界，超界 Suite 计入"跳过数"提示。
- 结果详情断言/提取行沿用 256 条上限；完整明细走导出。
- 双语：行摘要保持 DSL 词汇（status/equals/jsonpath token），标签双语。
