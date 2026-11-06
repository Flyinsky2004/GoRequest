# FIX05 设置界面：左分类栏 + 右主内容

- 任务 ID：FIX05
- 分支：`cc/FIX05-settings-layout`
- 共同基线：`3cc1731459ff1c44d70cbd48b0e5ea3fa7773caf`（main）
- 状态：实现完成，自测通过；真人窗口视觉验收未做（见“需人工确认项”）。

## 目标回顾

把原先“单列滚动模态”的设置界面改成 macOS“系统设置”式左右布局：左侧为分类项列表，右侧只显示所选分类的设置，保留原有全部控件的读写行为与状态提示；窄窗口不横向溢出，模态可关闭；不改动已存配置语义/键值，老配置兼容；不新增无实际作用的项。

## 结构改动

`internal/desktop/settings.go`

- `renderSettings` 签名保持不变（`(c, t, cfg, prefs, open, setStatus)`），被 `app.go` / `welcome.go` 的公共渲染调用无需改动。
- `settingsBody` 重写为两栏结构：
  - 顶部标题 `m.SettingsTitle`；
  - 中部 `ui.Row`：左侧分类栏 + 右侧内容区；
    - **左侧** `settingsCategoryRail`：每个分类一行（可点击、左对齐、悬停高亮、选中用 accent 填充 + OnAccent 文字），选中索引写入 `ui.Local` 的帧间状态；
    - **右侧** `ui.Scroll` 竖向滚动，只渲染当前分类的内容；
  - 底部“完成”按钮（点击置 `*open=false`）；点击模态外或按 Esc 仍可关闭（`ui.Modal` 既有行为）。
- `settingsCategoryContent`：按分类分派到各自的设置分组，控件读写与 `setStatus` 状态提示逐一原样保留。
- `settingsAbout`：抽出版本/平台/Go/配置文件/最近项目信息卡 + SecretNote（“关于”分类）。
- 保留 `settingsGroup`、`applyAppearance`、外观/历史/配置路径等辅助函数原样。

尺寸自适应（避免窄窗口横向溢出）：

- 内容宽 `width = c.Size().w - 80`（模态面板最大 `w-40`，两侧内边距各 20，故减 80 恰好落在面板内），上限 640，最小兜底 200。
- 左栏宽 = `width*0.30`，夹在 `[104,168]`。
- 内容行 `Height = h-92`（下限 160），并对外层内容行加 `Clip()`，作为“绝不横向溢出”的硬保证；右栏 `Scroll` 竖向滚动。

`internal/desktop/settings_i18n.go`（新增，未改 `i18n.go`）

- `settingsCategory` 枚举（Appearance/Language/Startup/Workspace/Markdown/History/About）。
- `settingsCategoryTitles(language)`：中文 + 英文分类标题。

分类清单（显示顺序）：

1. 外观 — 外观样式选择（System/Light/Dark，存稳定英文 token）
2. 语言 — 中文 / English（存稳定 language token）
3. 启动 — 启动时恢复上次项目
4. 工作区偏好 — 编辑器 / 自动保存 / 快捷键（沿用 `workspacePrefsStore.settingsGroups`）
5. Markdown 预览 — 字号比例 / 留白 / 行距 / 恢复默认（沿用 `markdownSettingsGroup`）
6. 历史 — 快照、正文、保留期限（原逻辑不变）
7. 关于 — 版本 / 平台 / Go / 配置文件 / 最近项目数量 / SecretNote

## 配置兼容性

- 所有持久化键、取值 token（外观 `System/Light/Dark`、语言 `zh-CN/en`、历史天数、Markdown 偏好、工作区偏好等）均未改变，只改渲染布局，老配置直接兼容。
- 未新增任何无实际作用的设置项。

## 验证结果

- `go build ./...`：通过
- `go vet ./...`：通过
- `go test -race ./...`：全部通过（`internal/desktop` 79.8s，其余包 ok）

新增无窗口布局测试 `internal/desktop/settings_layout_test.go`：

- `TestSettingsCategoryContentNarrow`：两种语言 × 340/420/520 三档宽度 × 全部 7 个分类，逐个无窗口构建，断言不 panic、文本矩形不越出窗口左右边界。
- `TestSettingsBodyRailSwitchesCategory`：540px 全模态构建；默认显示“外观”，点击左栏“关于”后切换到关于面板（外观面板消失、出现“版本”），切换后仍无横向溢出。
- `TestSettingsStartupToggleWritesConfig`：点击“启动时恢复上次项目”复选框后配置值发生变化——配置读写回归。

另外用 `ui.NewTester` 直接把 640/480 宽的设置模态渲染成 PNG 做了自查（临时测试已删除，未提交）；左栏分类、右栏外观/工作区面板布局与选中态正常。

## 对既有测试的最小适配（需集成人知悉）

新布局不再“一屏堆叠所有分组”，因此以下既有测试原本断言“默认就能看到全部分组”，做了最小适配使其按新分类栏导航后再断言（只改测试，不改产品逻辑）：

- `internal/desktop/workspace_management_test.go`：`TestSettingsSheetShowsWorkbenchPreferenceGroups` 先点“Workspace”再断言编辑器/自动保存/快捷键。
- `internal/desktop/foundation_integration_test.go`：`TestFoundationCombinedSettingsAndInteropEntry` 先点“Workspace”再断言 Editor/Auto-save。
- `internal/desktop/view_test.go`：`TestWorkspaceViewSeparatedFromWelcome` 先断言“Appearance”，再点“Startup”断言恢复项。
- `internal/desktop/i18n_test.go`：`TestSwitchLanguageViaSettingsPersistsAndUpdatesImmediately` 先点“Language”类，再操作语言下拉。

这些测试文件不在“公共文件”禁用清单内，改动仅限导航步骤，属本卡行为变更的必要连带调整。

## 需人工确认项（视觉验收）

- 未做真实窗口的视觉验收（无 GUI 环境）。请人工在 macOS 真窗口确认：左右栏比例、选中态配色、窄窗口（≈480–520px 及更窄）观感、滚动行为、暗色模式。
- 左栏英文“Workspace / Markdown preview / Appearance …”在极窄宽度下 `SingleLine` 可能被裁切（已有 `Clip` 不溢出，但可能截断文字）；如需更好观感可在窄宽度下缩短英文标签。

## 需集成人接线项

- 无需改动公共文件；`renderSettings` 签名与调用点保持不变，`i18n.go` 未改。
- 若集成时其他分支也动了 `settingsBody`，请以本卡的分栏实现为准合并；本卡只新增 `settings_i18n.go`，与 `i18n.go` 无冲突。
- 未勾选总账本 `docs/parallel-work-plan.md`，交由集成人统一勾选。

## 提交范围（本分支本地 commit，不 push）

- `internal/desktop/settings.go`
- `internal/desktop/settings_i18n.go`（新增）
- `internal/desktop/settings_layout_test.go`（新增）
- 上述四个既有测试文件的最小适配
- 本文件 `docs/task-results/FIX05.md`
