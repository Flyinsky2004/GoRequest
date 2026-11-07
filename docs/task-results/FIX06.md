# FIX06 设置窗口紧凑高度

- 任务 ID：FIX06
- 分支：`cc/FIX06-settings-height`
- worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX06`
- 共同基线：`a20960beffbc63880eb9e74214ec2675523af6bf`
- 状态：实现完成，门禁通过（见“验证”）；**真实窗口 / 三平台人工视觉验收未做**。

## 用户反馈与范围

FIX05 的设置面板 `height = winH-80`、`rowH = height-92`，在常规 1200×820 项目窗口里几乎占满整窗。用户要求把占用高度降到约现在的一半，同时：

- 保留 FIX05 的左分类栏 + 右内容、Markdown / 历史 / 编辑器等设置；
- 采用合理上限与小窗口自适应，不压扁控件、不隐藏类别、Done 常驻；
- 左类别多时可滚动 / 完整可达，右内容独立滚动；
- 欢迎窗口 / 小屏 / 放大字号不越界；
- 切换类别不能因内容高度不同而跳动；
- 不改类别文案 / 顺序 / 业务，不动公共文件。

只改 `internal/desktop/settings.go` 与新增专属测试文件；未触碰 `app.go` / `manager.go` / `nav.go` / `workspace.go` / `tab.go` / `config.go` / `i18n.go` / `go.mod` / `go.sum`、总账本与 implementation-status，也未改其他测试文件。**无需接线 patch**：`renderSettings` 签名与调用点不变，`settings_i18n.go` 未动。FIX08/09 若需设置入口，由集成人自行处理，本卡未越界。

## 实现

`internal/desktop/settings.go`

1. **纯函数几何** `settingsGeometryFor(winW, winH)`（新增 `settingsGeometry` 结构与常量）：
   - 宽度沿用 FIX05 的 `winW-80`，夹在 `[200, 640]`，左栏 `[104, 168]`。
   - 高度改为 `clamp(winH*0.55 - 40, 240, 380)` 内容 DIP，再按窗口可用高度 `winH-80` 兜底夹紧：
     - 绝对上限 `settingsMaxContentHeight = 380`（+ 模态自身 40 padding = **420 DIP 面板**）；
     - 小窗口按 55% 比例缩小；
     - 下限 240（280 DIP 面板）保证控件不被压扁；
     - 窗口比下限还矮时由窗口夹紧，Done 不会被推出窗口。
   - 高度**只依赖窗口尺寸**，与所选类别 / 内容高度无关，杜绝切类别跳动。
2. **固定根高 + Grow 分配**：`ui.Column(...).Height(g.height)`，标题 `.Shrink(0)`、Done 行 `.Shrink(0)`，中间两栏行 `.Grow(1)`。放大字号或主题变化时，多出来的高度从**可滚动的两栏**里扣，而不是把 Done 挤出面板。
3. **左类别栏独立滚动**：`settingsCategoryRail` 在原卡片外观内加一层 `ui.Scroll` 包裹类别列表（保留 30 DIP 行高、选中 accent / hover wash / 点击切换 / 文案与顺序）。类别多或窗口矮时滚动到末尾仍可点击。
4. **右内容保持独立 `ui.Scroll`**：两个 `Scroll` 各自持有滚动状态，实测滚动右侧不会带动左栏，反之亦然。
5. 保留 `ui.Modal` 的点击外部 / Esc 关闭、`applyAppearance`、全部分类内容分派与状态反馈、About / 历史 / Markdown / 工作区偏好逻辑，均未改语义。

### 实际最终高度（内容 DIP，模态面板另加 40）

| 窗口 | 旧 FIX05 面板 | 新面板 | 占比 | 内容高 |
| --- | --- | --- | --- | --- |
| 1200×820（常规项目窗） | 780 | **420** | **54%** | 380 |
| 1440×900 | 860 | **420** | 49% | 380 |
| 760×560（窄项目窗） | 520 | **308** | 59% | 268 |
| 720×480（欢迎窗） | 440 | **280** | 64% | 240 |
| 560×360（欢迎窗最小） | 320 | **280** | — | 240 |
| 400×300 | 260 | **260**（窗口夹紧） | — | 220 |
| 300×200 | 160 | **160**（窗口夹紧） | — | 120 |

目标 1200×820 从 780 降到 420 DIP，即约现有高度的一半；一律不超过窗口高度。放大字号（base 26）与 zoom 1.5/2 下 Done 仍在窗口内且可点击。

## 验证（真实命令与结果）

- `go build ./...`：**通过**（BUILD_OK）
- `go vet ./...`：**通过**（VET_OK）
- `go test -race ./...`：**全部通过**（exit 0；`internal/desktop` 86.554s、`internal/core` 7.230s、`uikit` 2.808s、`gitservice` 7.539s、`localstate` 3.963s、`platform` 1.957s、`runner` 2.218s、`markdown` 1.371s、`examples/demo-server` 2.085s，日志 `/private/tmp/gorequest-fix06-race.log`）
- 既有设置回归（单独复跑，全部通过）：`TestSettingsCategoryContentNarrow`、`TestSettingsBodyRailSwitchesCategory`、`TestSettingsStartupToggleWritesConfig`、`TestWelcomeViewSmoke`、`TestWorkspaceViewSeparatedFromWelcome`、`TestSettingsSheetShowsWorkbenchPreferenceGroups`、`TestFoundationCombinedSettingsAndInteropEntry`、`TestSwitchLanguageViaSettingsPersistsAndUpdatesImmediately`。**未删任何既有断言**；FIX05 的 `assertNoHorizontalOverflow` 可达性检查在新测试里继续复用。

新增独立测试文件 `internal/desktop/settings_height_fix06_test.go`（不覆盖他卡测试）：

- `TestSettingsGeometryForWindows`：7 档窗口的纯几何断言（上限、窗口内、1200×820 紧凑到旧值 60% 以下、小窗口自适应），并逐档 `t.Logf` 记录实际最终高度。
- `TestSettingsSheetFrameIsCompactAndInWindow`：中英双语 × 6 档窗口，用渲染出的标题 / Done 反推真实模态面板上下边界，断言在窗口内、Done 可见可点击、高窗不超 60%，并复用 `assertNoHorizontalOverflow`。
- `TestSettingsSheetHeightStableAcrossCategories`：切换 Workspace / About / History / Appearance 后标题与 Done 的 Y **完全不变**（无跳动）；滚动长 Workspace 右栏时其自身内容上移而框架不动。
- `TestSettingsRailScrollsToEveryCategory`：560×360 下最后一个类别初始被裁切，滚动左栏后可见且可点击打开 About；并验证滚动右栏不会串动左栏（两栏独立）。
- `TestSettingsSheetEnlargedTextAndZoom`：base font 26、zoom 1.5 / 2 组合下面板不越界、Done 可见可点击且能关闭弹窗。

测试用 `ui.NewTester` 无窗口渲染真实视图；几何数值来自 MyGo v0.7.4 `ui.Modal`/`coreModal`（`c.w-40` + 20 DIP padding）与本地 `Tester.Find` 实测，未猜 API、未引 WebView、未改依赖缓存、未写真实系统凭据 / 历史 / 全局配置，打开视图不隐式发网。

## 边界与未做项

- **真实 macOS 窗口视觉验收、暗色模式观感、三平台（Windows / Linux）实机、真实鼠标滚轮与滚动条拖拽均未做**；本卡只有无窗口自动化 + 全仓 race 门禁证据，不用 Herdr 截图冒充 GoRequest 验收。
- 类别文案 / 顺序 / 业务、翻译表、配置键均未改；老配置兼容。
- 极矮窗口（< 280 面板高）保留最小可用高度并按窗口夹紧：此时面板占窗口比例较高，但控件不被压扁、Done 不出界；如需更矮需另行产品设计。
- 未勾总账本 `docs/parallel-work-plan.md`，自勾整卡由集成人在合并并人工验收后完成。

## 集成步骤

1. 合并 `cc/FIX06-settings-height`；本卡只改 `internal/desktop/settings.go` + 新增 `internal/desktop/settings_height_fix06_test.go` + 本交付文档，与 FIX07/08/09 的专属文件无冲突。
2. 无需公共壳层接线；`renderSettings` 签名、`ui.Modal` 调用点、`settings_i18n.go` 分类表均不变。
3. 集成人运行 `go build ./...`、`go vet ./...`、`go test -race ./...`，并按“边界与未做项”补真实窗口 / 暗色 / 三平台人工验收后再勾 FIX06。
4. FIX08/09 若需要新增设置入口，请由集成人统一处理其公共 patch；本卡未预留或改动任何设置壳层入口。

## 提交范围（本分支本地 commit，不 push）

- `internal/desktop/settings.go`
- `internal/desktop/settings_height_fix06_test.go`（新增）
- `docs/task-results/FIX06.md`（本文件）
