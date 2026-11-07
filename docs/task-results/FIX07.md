# FIX07 发送按钮 hover / pressed 状态反馈

- 任务 ID：FIX07
- 分支：`cc/FIX07-send-feedback`
- worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX07`
- 共同基线：`a20960beffbc63880eb9e74214ec2675523af6bf`（开工时 `HEAD` 一致，`git status` 干净）
- 本卡本地 commit：`feat(ui): add hover/pressed state feedback to SendButton`（哈希见交付会话报告 / `git log -1`，避免文档内自引用哈希因 amend 变动）
- 状态：本卡实现与自有验证完成；**整卡验收待集成人在集成分支勾选**，真实窗口/三平台人工验收未做（见下）。

## 目标回顾

用户反馈：发送按钮 hover 和 active（按下）没有颜色变化。原 `SendButton` 固定
`Background(p.Accent)`，只有 `ActionCursor` 手型和发送/取消图标 morph，没有任何
指针状态样式。要求：默认 / hover / pressed 三态可辨、跟随既有 Palette / Motion
规范、用现有 API 平滑过渡、深浅主题都清楚且文字图标对比足够、发送与取消两种
功能态都具反馈、hover 不启动请求、按下释放与取消手势正确、保留 104×40 尺寸 /
圆角 / 手型 / 无障碍标签 / 键盘行为、不强制持续重绘、禁用或父禁用不显示可点击
状态色也不触发动作、用真实 Move/按下/释放与像素/颜色验证、保留 UI02 既有测试。

## 改动范围（只改本卡归属）

| 文件 | 性质 |
| --- | --- |
| `internal/desktop/uikit/response.go` | 修改，仅 `SendButton` 与其新增的 `sendButtonColors` 辅助函数 |
| `internal/desktop/uikit/fix07_send_state_test.go` | 新增，本卡专属测试（独立文件，不覆盖他卡测试） |
| `docs/task-results/FIX07.md` | 新增，本交付文档 |

未改 `PillButton` / `SendPill` / `IconButton` / 任何其他布局或通用组件；未改
`app.go` / `manager.go` / `nav` / `workspace.go` / `tab` / `config` / `i18n.go` /
`go.mod` / `go.sum` / 总账本 `docs/parallel-work-plan.md` /
`docs/implementation-status.md`。**无需集成 patch**：本卡是纯 uikit 内部状态样式，
`SendButton` 的签名、返回值与 `workspace.go` 两处调用点（`sending=true` 取消 /
`false` 发送）完全不变，没有新增任何公共接线。

## 实现

### MyGo v0.7.4 API 核对（本地模块缓存，未猜 API、未改依赖缓存）

先读 `/Users/flyinsky/go/pkg/mod/github.com/egoist/mygo@v0.7.4/ui/` 源码确认：

- `Element.Hovered()` / `Pressed()` / `IsDisabled()` / `Clicked()` 均为公开 API
  （`ui/input.go`、`ui/element.go`、`ui/api_gen.go`）。`Hovered()` / `Pressed()`
  在 disabled（自身或祖先）时返回 `false`；`Clicked()` 在 disabled 时不触发。
- `Element.AnimateColor(key, target, a)`（`ui/motion.go`）：颜色平滑过渡，
  **仅在颜色实际移动时** `AnimationFrame()` 请求帧，到达目标后停止；
  `ReduceMotion` 下立即到位（`ui/preferences.go`）。
- `Element.AnimatePose(ui.Pose{Scale: ...}, ui.Snappy)`：按压缩放，同样会 settle。
- `Color.Mix(o, t)`（`ui/color.go`）：在 sRGB/宽色域都按同一比例混合。
- `ui.Tester` 的 `Move` / `Press` / `Release` / `ClickAt` / `Cursor` /
  `FramePending` / `Image().RGBAAt` / `SetPreferences` / `Advance`
  （`ui/headless.go`）可做真实手势与像素验证。
- 沿用 uikit 既有惯例：`components.go` 的 `IconButton` / `pillButton` 正是在
  build 期查询 `Hovered()` / `Pressed()`，配 `AnimateColor(..., AnimHover)` 与
  `AnimatePose(..., ui.Snappy)`。`SendButton` 原本是唯一缺状态反馈的主按钮。

### 三态配色（`sendButtonColors`）

从 `Palette` 单独派生，不查 theme，保持组件自洽：

| 主题 | rest | hover | pressed |
| --- | --- | --- | --- |
| 浅色 | `p.Accent`（原值不变） | `Accent.Mix(black, 0.12)` | `Accent.Mix(black, 0.25)` |
| 暗色 | `p.Accent`（原值不变） | `Accent.Mix(black, 0.15)` | `Accent.Mix(black, 0.32)` |

**关键决策**：三个状态一律向黑色混合，不向白色混合、不用半透明 wash。
`p.OnAccent` 是固定白色文字 + 两个图标，所以加深底色只会提高对比度；
若照搬 `uikit.Theme` 的暗色「hover 向白 15%」通用 token，实测白色文字对比度
从 3.42:1 掉到 **2.91:1**（低于 WCAG AA 4.5:1）。改为向黑后实测：

| 主题 | 状态 | 实测 RGB | 与白色 OnAccent 对比度 |
| --- | --- | --- | --- |
| 浅 | rest | `226,51,60` | 4.40:1（既有 palette token，本卡未改） |
| 浅 | hover | `199,45,53` | 5.45:1 |
| 浅 | pressed | `170,38,45` | 6.94:1 |
| 暗 | rest | `244,80,88` | 3.42:1（既有 palette token，本卡未改） |
| 暗 | hover | `207,68,75` | 4.58:1 |
| 暗 | pressed | `166,54,60` | 6.53:1 |

本卡引入的 hover/pressed 四态均 ≥ 4.5:1；rest 是未改动的既有 token，如实记录
其低于 AA 的既有事实，未冒充已达 AA。相邻态通道差：浅 rest→hover 40、
hover→press 44；暗 rest→hover 62、hover→press 70、rest→press 132，均可辨。

### 交互与状态

- build 期读 `Hovered()` / `Pressed()`，优先级 `disabled → pressed → hover → rest`；
  显式 `IsDisabled()` 作二次护栏（父禁用 path）。
- `Background(AnimateColor("send-bg", bg, AnimHover))` 走 motion.go 的 120ms
  `AnimHover` 平滑过渡；`AnimatePose(ui.Pose{Scale: down ? ScalePrimaryPress : 1},
  ui.Snappy)` 用 motion.go 既有的 0.97 主按钮按压档。
- **不新增任何定时器 / Loop / Keyframes**：`SendProgress` / `SkeletonLines` 的
  动画原样不动；颜色/位姿到达目标后 `FramePending()` 为 false。
- 保留：104×40、`Radius(10)`、`Center()`、`ActionCursor` 手型、`Label` 无障碍、
  `Clicked()` 动作、发送/取消图标 morph（`SpringIconMorph` 340ms/0.30）。
- hover 与「按下但未释放」都不调用动作；动作只在释放时经 `Clicked()` 触发一次；
  在按钮外释放（取消手势）不触发。发送与取消两态共用同一套状态色。
- 禁用：在 `SendButton` 构建**之前**由外层容器设 `Disabled`（应用侧真实模式，
  也是 UI02.md 记录的 MyGo v0.7.4 顺序约定）时，三态像素完全一致、默认箭头、
  0 次动作，只见 MyGo 自身的 0.5 禁用变暗。
  - 已知边界（MyGo v0.7.4 公开 API 限制，非本卡引入）：若调用者在
    `SendButton` **返回之后**才对返回元素直接 `.Disabled(true)`，该帧已观测到的
    hover 无法撤回，可能出现一帧 hover 色调；`Clicked()` 仍被 MyGo 抑制、不触发
    动作。真实 `workspace.go` 不使用这种顺序。此限制与 UI02.md 对 `ActionCursor`
    的说明一致，已在 `response.go` 注释中写明。

## 验证结果（命令真实输出）

前置：`gofmt -w` 两个 .go 文件。

- `go build ./...`：**通过**（无输出，exit 0）
- `go vet ./...`：**通过**（无输出，exit 0）
- `go test -race ./...`：**全部通过**（exit 0，见下方「门禁记录」）

新增 `internal/desktop/uikit/fix07_send_state_test.go`，全部用 `ui.NewTester`
真实手势 + 渲染像素断言，不只断言调用链：

| 测试 | 覆盖 |
| --- | --- |
| `TestFIX07RestIsAccent` | 浅/暗 × 发送/取消，rest 采样点像素精确等于 `p.Accent`（钉住采样点，保证没改坏原固定底色） |
| `TestFIX07PointerStates` | 浅/暗 × 发送/取消：`Move`→`Press`→`Release`→移出，5 个 settled 像素三态互异、释放回到 hover、移出回到 rest；hover/pressed 与白字对比 ≥ AA 且不低于 rest；104×40 几何不变；恰好 1 次动作且功能态正确 |
| `TestFIX07HoverAndPressAloneDoNotStartRequest` | 单独 hover、单独按住、按钮外释放三种手势均 0 次动作（hover 不启动请求、取消手势正确） |
| `TestFIX07DisabledShowsNoStateColor` | 浅/暗 × 自身/父禁用：hover/press 像素与 rest 完全一致、默认箭头、0 次动作、禁用面明显区别于启用 accent |
| `TestFIX07ReduceMotionStillChangesColor` | `SetPreferences{ReduceMotion:true}` 下状态色仍立即出现且可辨，不丢反馈，settle 后无 pending 帧 |
| `TestFIX07PressScaleVisible` | 顶边像素在按下时因 0.97 缩放露出列背景，确认按压缩放真的渲染 |
| `TestFIX07IdleFrameBudget` | 静止 hover 下连续 10 个 `Frame()` 均 `FramePending()==false` 且像素不变，确认不强制持续重绘 |
| `TestFIX07KeyboardAndCursor` | 图标/文字/空白三点手型、Space/Enter 键盘激活、`send-target` 无障碍标签保留 |

UI02 既有回归（未修改其文件，直接重跑）：`TestUI02SendCursorAndHitTarget`、
`TestUI02CommonCursorDisabledInputAndKeyboard`、`TestUI02NativeSegmentedMatchesMyGo`、
`TestUI02ScrollbarsAndNestedInputKeepTheirCursor` **全部通过**——子图标/文字/空白
可点击、cursor、禁用输入、键盘、NativeSegmented 像素对照均未被破坏。

### 门禁记录

`go test -race ./...`（后台运行，日志 `/tmp/fix07-race.log`，`RACE_EXIT=0`）：

```
?   	gorequest/cmd/gorequest-desktop	[no test files]
ok  	gorequest/examples/demo-server	2.038s
ok  	gorequest/internal/core	7.228s
ok  	gorequest/internal/desktop	83.898s
ok  	gorequest/internal/desktop/uikit	4.858s
ok  	gorequest/internal/gitservice	7.573s
ok  	gorequest/internal/localstate	4.098s
ok  	gorequest/internal/markdown	1.369s
ok  	gorequest/internal/platform	1.450s
ok  	gorequest/internal/runner	2.193s
```

聚焦回归：`go test ./internal/desktop/uikit/ -run 'TestFIX07|TestUI02' -count=1`
8 个 FIX07 + 4 个 UI02 全部 PASS（0.488s）。

## 需人工确认项（未做，不冒充验收）

- **未打开真实 GoRequest 窗口**做人工 hover / 按下 / 释放观感验收；所有验证均为
  `ui.NewTester` 无窗口软件渲染 + 像素取样。OS 原生游标外观、真实 GPU 绘制下的
  颜色观感、动画节奏手感、动态类型/缩放、多显示器 DPI 仍需真窗口确认。
- **未做 Windows / Linux / macOS 三平台实机验收**；本次仅本机 darwin/arm64。
- 未使用 Herdr 截图冒充 GoRequest 验收。
- 打开项目 / 渲染视图不隐式发网：本卡为纯 UI 状态样式，未新增任何网络调用。

## 集成步骤与边界

- 无需改公共文件、无需 `-integration.patch`：`SendButton` 签名与两处调用点不变，
  `response.go` 其余函数（`StatusPill` / `SendProgress` / `SkeletonLines`）未动。
- 本卡测试为独立新文件 `fix07_send_state_test.go`，与他卡测试文件名不冲突；
  未改 UI02 测试文件。
- 集成人在 `integrate/fix-batch` 合并本分支后重跑门禁（`go build ./...` /
  `go vet ./...` / `go test -race ./...`），确认无冲突，并在真实窗口补做 hover /
  pressed / 暗色 / 取消态观感验收，满足后再勾选 `docs/parallel-work-plan.md` §16
  的 FIX07 条目。**本卡不自勾整卡完成**。
- 遵守 ADR-010：本卡不涉及任何 Secret 名称/真实值、系统凭据、环境变量注入或
  项目文件；未写真实系统凭据 / 本机历史 / 全局配置；测试只用内存 `ui.Tester`
  与临时调色计算，不落盘、不触碰示例环境或 debug 文件。
- 未 merge / cherry-pick / push / reset / clean，未操作 main 或他人 worktree，
  未启子 agent，未修改共享 Git 配置。

## 提交范围（本分支本地 commit，不 push）

- `internal/desktop/uikit/response.go`
- `internal/desktop/uikit/fix07_send_state_test.go`（新增）
- `docs/task-results/FIX07.md`（本文件）
