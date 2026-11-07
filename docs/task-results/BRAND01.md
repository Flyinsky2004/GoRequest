# BRAND01 用户 Logo 接入 — 交付记录

- 分支 / worktree：`cc/BRAND01-logo-integration` / `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-BRAND01`
- 共同基线：`6046a34d4af37b21c8bba7d5e54b85803ff6f7f5`（已核对 HEAD 与分支一致）
- 任务范围：把用户提供的现成 Logo 接入原生应用/应用图标资源与必要最小品牌展示；不重绘、不调用图像生成、不改颜色/裁剪/叠加阴影或第二层圆角。

## 1. 图片来源与核对

- 原图由用户提供：`/Users/flyinsky/Downloads/GoRequest macOS 容器无阴影版.png`；由集成人**无损复制**为本卡未跟踪资源 `assets/brand/gorequest-logo.png`。Downloads 原文件全程未修改。
- 已实际查看原图：白色 macOS 圆角方形容器 + 红/橙丝带箭头（含运动模糊尾迹），四角外侧透明。设计中已有圆角容器与 alpha，本卡保留，不叠加底板/阴影/第二层圆角。
- 静态核对：`sips` = `1600 x 1600`、`hasAlpha: yes`、`format: png`；`file` = `PNG image data, 1600 x 1600, 8-bit/color RGBA`。
- 哈希（源文件与内嵌副本一致）：`fc29afc1ac3a8c1eb36227b9c18a7d1ab53ba1bdbcf868978aa67b018ac5e206`（`shasum -a 256`）。

## 2. 资源与接线

| 文件 | 作用 |
| --- | --- |
| `assets/brand/gorequest-logo.png` | 原图无损入仓（本卡未跟踪资源，纳入提交） |
| `internal/brand/gorequest-logo.png` | 同一 PNG 的库内副本，供 `//go:embed`；路径稳定，不依赖 Downloads 或仓库根相对路径 |
| `internal/brand/brand.go` | `//go:embed` 原图；`PNG()` 返回拷贝供原生图标 API，`Logo()` 供 `ui.Image`；`ui.DecodeImage` 在 `init` 解码一次（MyGo `ui/bitmap.go` 已 `image/png` 空导入注册解码器） |
| `assets/brand/gorequest.icns` | macOS 应用图标（确定性生成，提交） |
| `assets/brand/gorequest-512.png` | 512px 可预览 PNG（README / Linux 等用途，提交） |
| `scripts/make-macos-icon.sh` | 仅用系统 `sips`/`iconutil` 生成 `.icns` 与 512 PNG，临时 `.iconset` 在 `mktemp` 中并清理 |
| `scripts/make-macos-app.sh` | 生成最小**未签名开发用** `GoRequest.app`，把 `.icns` 接进 `Info.plist` 的 `CFBundleIconFile` 与 `Contents/Resources/gorequest.icns`；不是发布/签名/安装包 |
| `internal/desktop/manager.go` | `Manager.WireApp`（由 `WhenReady` 调用）内 `mygo.App.Dock.SetIcon(brand.PNG())` |
| `internal/desktop/window.go` | `newRealWindow` 在 `mygo.NewWindow` 后 `w.SetIcon(brand.PNG())` |
| `internal/desktop/welcome.go` | 欢迎/项目选择窗口：现有 "GoRequest" 文字与 tagline 左侧加 48×48 同 Logo 小图；文字、操作、标题安全区、拖拽区、功能 SVG 图标均未替换 |
| `README.md` | 顶部 96px 同 Logo 展示 + 来源、生成命令、运行时/打包接线、未验收项说明 |

### MyGo v0.7.4 真实 API（读本机 `~/go/pkg/mod/github.com/egoist/mygo@v0.7.4`，未猜）

- `func (d *Dock) SetIcon(png []byte) error`（`app.go:615`）→ `backend().App().SetDockIcon(png)`；darwin `internal/darwin/app.go:439` 实际调用 `NSApplication setApplicationIconImage:`，Linux `internal/linux/app.go:301` 设定 GTK 默认窗口图标，Windows `internal/windows/app.go:620` 设定 `HICON`。`WhenReady` 在 `Run` 完成 `b.Init` / `initialized=true` 之后触发，调用点合法。
- `func (w *Window) SetIcon(png []byte) error`（`window.go:1225`）→ 平台 `SetIcon`；`internal/darwin/window.go:1161` 为 no-op（macOS 窗口自身无图标），Linux/Windows 用于标题栏/任务栏。`WindowOptions` **没有** icon 字段，故在构造后应用。
- `ui.DecodeImage` / `ui.Image(c, src).Size(w,h)`（`ui/widgets.go:536`、`ui/api_gen.go:1339`、`Element.Size`）用于窗口内展示；`Contain` 默认按宽高比缩放。
- 图标 API 失败均**不致命**：保留平台默认图标，应用照常运行（不因一张图阻塞启动）。

### 现有打包核对

- 仓库**没有**已提交的 `.app`、`Info.plist`、`.icns`、`.ico` 或打包脚本；`README` 原有仅为 `go build -o bin/gorequest-desktop ./cmd/gorequest-desktop` 的裸二进制。
- 因此本卡补最小可复现 bundle 接线，不凭空声称 Dock 已生效；裸二进制的运行时图标与 `.app` 的 Finder/Dock 图标是两条不同路径。

## 3. 复现命令

```bash
# 由原图确定性生成 .icns 与 512 PNG（macOS 自带工具，无下载依赖）
sh scripts/make-macos-icon.sh

# 生成最小未签名开发用 .app（含 CFBundleIconFile 与 Resources 图标）
sh scripts/make-macos-app.sh            # 默认输出到 ./bin（gitignored）

# 原样运行裸二进制（运行时 API 设置图标）
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project
```

`.icns` 生成步骤：10 个标准 iconset 尺寸（16/32/128/256/512 及 @2x）逐一 `sips -z` 等比重采样同一 1600² 源图（不裁剪），`iconutil -c icns` 打包。源图始终不变。

## 4. 静态验证（本卡按约束**不运行 `go test`**）

| 检查 | 结果 |
| --- | --- |
| `sips` 源图 1600×1600 / hasAlpha / PNG；`file` RGBA | 通过 |
| 源图与内嵌副本 `shasum -a 256` 相同 | 通过 |
| `sh scripts/make-macos-icon.sh` 连续两次输出 `.icns` 哈希相同；512 PNG 同理 | 通过（确定性） |
| 生成 512 PNG `hasAlpha: yes`（保留透明边界） | 通过 |
| 实际查看 128 / 64 / 32px 缩略图 | 128px 箭头与尾迹清晰；64/32px 细丝带细节丢失、成为较密的红橙标记，箭头轮廓与白色圆角容器仍可辨。**未断言小尺寸“好看”**，仅记录实际观察 |
| `gofmt -l`（4 个改动/新增 .go） | 空（通过） |
| `go build ./internal/brand` / `go vet ./internal/brand` | 通过 |
| 全树 `go build ./...`（真实工作树） | **被基线已有编译错误阻塞，非本卡改动** |
| 引用路径、`//go:embed` 目标、README/脚本路径、`sh -n` 两个脚本 | 通过 |
| `Info.plist`：`plutil -lint` OK、`CFBundleIconFile = gorequest.icns`、Resources 内 `.icns` 与提交产物哈希一致 | 通过 |
| 未提交临时 iconset / bin / .app / debug 二进制 | `bin/` 在 `.gitignore`；临时目录均在 `/tmp` |

### 基线编译阻塞（保留，未跨卡修复）

真实工作树 `go build ./...` 依次遇到 3 处**均存在于 HEAD 6046a34、我未修改**的错误：

1. `internal/diagnostics/trace.go:4` — `"context" imported and not used`（C02 性能模块）
2. `internal/desktop/mock_ui.go:87` — `ui.Checkbox` 参数顺序颠倒（FIX10 agy 独占修复中）
3. `internal/desktop/performance.go:125` — `a.activeTab undefined`（C02 模块）

按任务约束：不修其他任务的编译错误、不碰 `mock_ui.go`、不运行 `go test`。为确认**本卡代码**类型正确，在 `/tmp` 的一次性副本里对以上 3 处加最小临时 shim（不写回工作树、不改任何已跟踪文件）后，`go build ./...` 与 `go vet ./internal/brand ./internal/desktop` 均**无错误**。`make-macos-app.sh` 因此在本分支当前状态下会在 `go build` 步骤失败并给出明确提示；bundle 的 `Info.plist`/图标接线已独立验证有效，待这些编译错误由其归属任务修复后重跑即可生成可执行文件。

## 5. 实际生效范围 / 未验收与限制

- **已接线（代码层）**：运行时 Dock/应用图标（macOS/Windows/Linux）、窗口标题栏/任务栏图标（Linux/Windows）、欢迎窗口小图、README 展示、可复现 `.icns` 与最小 `.app` 资源接线。
- **未做实机验收，不宣称通过**：真实 Dock / Finder / 任务栏显示、真实窗口内 Logo 观感、`.app` 启动与图标加载、Windows/Linux 图标、深浅色/窄窗口下欢迎页品牌块布局、小尺寸图标在实际 Dock 缩放下的观感。
- **未做**：应用签名/公证/发布安装包（明确不在本卡范围）；未把位图塞进每个按钮，未替换任何功能性 SVG 图标；未改 `go.mod`/`go.sum`、module 缓存、业务代码、示例环境、debug 产物、用户运行中的窗口或系统设置。
- **未运行** `go test` / 构建套件（用户此前要求先不跑测试；本卡仅做资源尺寸/alpha/格式/引用、gofmt、静态编译与 plist/哈希检查）。
- 小尺寸图标观感仅基于 `sips` 缩略图的实际观察如实记录，未做真实 Dock 渲染对比，不作“好看/清晰”结论。

## 6. 提交

本地 commit：本分支 `HEAD`，主题 `feat: integrate user-provided GoRequest logo into native icons and app`（用户已按 AGENTS.md 授权，无需再问；具体哈希见交付消息与 `git log -1`，避免文档内自引用哈希随 amend 变化）。仅纳入本卡文件：

```
assets/brand/gorequest-logo.png
assets/brand/gorequest.icns
assets/brand/gorequest-512.png
internal/brand/brand.go
internal/brand/gorequest-logo.png
internal/desktop/manager.go
internal/desktop/welcome.go
internal/desktop/window.go
scripts/make-macos-icon.sh
scripts/make-macos-app.sh
README.md
docs/task-results/BRAND01.md
```

`go.mod` / `go.sum` / `docs/parallel-work-plan.md` / `docs/implementation-status.md` 未改动；总账本与整卡勾选由集成人维护，本卡不自勾。未 merge / cherry-pick / push / reset / clean，未改共享 git config，未操作其他 agent 或用户目录。
