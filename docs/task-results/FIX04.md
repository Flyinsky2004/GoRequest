# FIX04 — 响应区 JSON 树折叠按钮过大、图标过小

- 任务 ID: FIX04
- 分支: `cc/FIX04-json-tree-toggle`
- 共同基线: `3cc1731459ff1c44d70cbd48b0e5ea3fa7773caf` (main)
- 负责文件: `internal/desktop/response_view.go`（JSON 树折叠控件与相关行布局）
- 新增测试: `internal/desktop/response_json_tree_fix04_test.go`

## 问题

右侧响应区可折叠 JSON 树中，每个容器行前的折叠/展开按钮（▾/▸）用 `ui.Button` 绘制。
`ui.Button` 会套用主题按钮外观（`styleButton`：纵向 padding `Space(1.5)`≈6px、横向 `Space(3.5)`≈14px、外加 1px 边框、圆角、Surface 背景）。
于是单个箭头字形被裹在一个约 36–39px 宽、约 30px 高的按钮框里，字形本身仅约 14px 高——形成“按钮框太大、字形太小、行内留白过多（肚脐眼）”的观感。

非容器行用 `ui.Box(c).Width(22).Height(1)` 占位，与容器行的实际按钮宽度并不一致，导致同深度的键列无法严格对齐。

## 改动

`internal/desktop/response_view.go`：

1. 新增本卡专用常量（唯一真源，折叠控件与占位共用，防止两者宽度漂移）：

   ```go
   const (
       jsonTreeToggleSize  float32 = 16 // 折叠/展开箭头的方形点击区（px）
       jsonTreeToggleGlyph float32 = 12 // 箭头字号（px），与行内文本一致
   )
   ```

2. 容器行折叠控件由带主题外观的 `ui.Button` 改为无外观的 `ui.ButtonBase`，
   显式设定 `Size(16,16)`、`Radius(3)`、`Cursor(pointer)`，内部放 12px 的 ▾/▸ 文本：

   ```go
   toggle := ui.ButtonBase(c).
       Size(jsonTreeToggleSize, jsonTreeToggleSize).
       Radius(3).Cursor(ui.CursorPointer)
   toggle.Children(func() {
       ui.Text(c, glyph).FontSize(jsonTreeToggleGlyph).TextColor(p.Muted)
   })
   if toggle.Clicked() {
       ts.collapsedPaths[n.Path] = row.Expanded
   }
   ```

   `ButtonBase` 保持与 `Button` 相同的指针/键盘点击语义（`Clicked()`），因此折叠/展开行为不变，只是去掉了主题按钮的宽 padding、背景与边框。

3. 非容器行占位改为与折叠控件同尺寸，保证整列对齐：

   ```go
   ui.Box(c).Width(jsonTreeToggleSize).Height(jsonTreeToggleSize)
   ```

未触碰的既有功能：搜索命中自动展开、复制路径/复制值、全部展开/全部折叠——逻辑均未改动。

## 参数对比（改前 / 改后）

| 项目 | 改前 | 改后 |
| --- | --- | --- |
| 折叠控件实现 | `ui.Button(glyph)`（带主题外观） | `ui.ButtonBase` + 12px 文本，无外观 |
| 横向内边距 | `Space(3.5)`≈14px×2 + 1px 边框×2 | 0 |
| 纵向内边距 | `Space(1.5)`≈6px×2 + 1px 边框×2 | 0 |
| 控件约尺寸 | ≈36–39px 宽 × ≈30px 高 | 16px × 16px |
| 箭头字号 | 主题默认（≈13px），相对控件≈0.43 | 12px，相对控件 12/16 = 0.75 |
| 非容器占位 | `Width(22).Height(1)` | `Width(16).Height(16)` |
| 同深度键列对齐 | 容器按钮宽≈37 vs 占位 22，错位≈15–21px | 两者均 16px，键 X 完全一致 |

（改前后均为按 `Theme.Spacing` 默认 4、默认字号 13 推算；实测数据见下。）

## 验证结果

命令（均在 worktree 内执行）：

- `go build ./...` —— 通过
- `go vet ./...` —— 通过
- `go test -race ./...` —— 全部包通过（`internal/desktop` 82s，其余见输出）

新增无窗口（headless）布局/行为断言，使用既有 `ui.NewTester` 渲染真实 `responseJSONTreeView`：

1. `TestJSONTreeToggleCompactAndAligned`
   - 箭头字形盒紧凑：实测 `chevron W=5.66 H=14.13`（≤18 阈值）。
   - 同深度容器键 `a:` 与标量键 `c:` 左缘一致：均 `X=58`（改前带 pad 的按钮约 37px 宽会造成容器键右移，本断言会失败）。
   - 嵌套键 `b:` 按深度右缩进：`X=74`（+16）。
   - 点击目标缩小：在键左 28px 的行间隙点击不再触发折叠（`b:` 仍存在）；改前按钮带 14px padding，该点落在按钮内会被误触折叠，本断言会失败。
2. `TestJSONTreeToggleCollapseExpandAndCopy`
   - 点击 ▾ 折叠后子行消失；点击 ▸ 重新展开。
   - 复制路径得到含 `$` 的 JSONPath；复制值得到含原始文档内容的字符串（复制路径/复制值功能保持）。

此外渲染了一帧离屏位图做目视核对（临时测试文件已删除，未纳入提交）。

## 需人工目视确认项

- 在真实窗口、浅色/深色主题下确认 ▾/▸ 的观感：控件紧凑、箭头清晰、与 12px 行文本协调。
- 说明：JSON 树“每行”高度仍由行尾的 Copy Path / Copy Value 按钮（其余行布局，非本卡折叠控件）主导，约 35px；本卡按范围只收敛了折叠控件本身及其占位对齐，未改动复制按钮外观。如后续希望进一步压缩整行高度，需另立任务处理行尾按钮。

## 备注

- 仅修改本卡所属 `response_view.go` 及本卡新增测试文件；未触碰公共壳层、未 push、未 merge、未改 .git 配置。
- 未勾选总账本。
