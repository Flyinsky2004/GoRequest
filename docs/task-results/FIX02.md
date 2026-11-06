# FIX02 交付报告 — 移动请求/文件夹后左侧树不刷新

- 任务 ID：FIX02
- 分支：`cc/FIX02-tree-refresh`
- worktree：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX02`
- 共同基线：`3cc1731459ff1c44d70cbd48b0e5ea3fa7773caf`（main）

## 1. 用户问题

把一个接口（请求）移动到另一个文件夹后，界面左侧树没有及时更新，必须关闭并重新打开项目、重新渲染后才看到新位置。移动文件夹同样存在该现象。

## 2. 根因

分两层核对：

1. **内存树是否重建：是。** `internal/core/folders.go` 中：
   - `MoveRequest` 在移动成功后设置 `entry.RelPath` 并调用 `p.rebuildTree()`；
   - `MoveFolder → moveFolderDir → writeFolderCollection` 也会重新映射 `colByDir` / `p.Requests` 的路径并调用 `p.rebuildTree()`。

   因此 `ps.project.Collections` 已经是移动后的新树，`internal/desktop/collection_tree.go` 每帧直接从该树渲染，模型层面没有残留旧树。

2. **是否缺少重绘通知：是（真正根因）。** `internal/desktop/tree_actions.go`：
   - `moveRequestTo`（右键菜单 `Move To…` 与拖拽 Drop 都走它）和
   - `moveFolderTo`（右键菜单移动文件夹）

   在修改完状态后只设置了 `ps.sel` / `a.status`，**没有请求窗口重绘**。而本工程中凡是在「渲染帧之外」修改界面状态的路径（删除、重命名、文件同步、撤销等）都显式调用 `a.win.Update(func(){})`，这才是 MyGo `Window.Update` 触发 `contentChanged → RequestFrame` 的唯一方式（见 `mygo content.go`）。

   右键菜单（`ui.PopoverMenu`）的 `Chosen()` 回调与拖拽 Drop 事件都在**已经绘制出本帧侧栏之后**才执行：本帧里请求行/文件夹行已经按旧位置画好，随后内存树被重建，但没有任何东西去索取下一帧。于是侧栏保持旧位置，直到下一次无关输入或重开项目重绘为止——正是用户描述的「必须关闭并重新打开项目」。

## 3. 改动文件

仅本卡文件，未触碰任何公共文件（i18n.go / app.go / project_state.go / manager.go / nav.go / workspace.go / tab.go / config.go / go.mod / go.sum 均未修改）。

- `internal/desktop/tree_actions.go`
  - `moveRequestTo` 末尾新增 `a.requestTreeRepaint()`；
  - `moveFolderTo` 末尾新增 `a.requestTreeRepaint()`；
  - 新增辅助方法 `requestTreeRepaint()`：`a.win == nil || a.win.IsDestroyed()` 时安全空操作，否则 `a.win.Update(func(){})`。用 `Update`（而非 `Invalidate`）与既有「渲染帧外改状态」的写法保持一致。
- `internal/desktop/tree_refresh_test.go`（新增）
  - `TestMoveRequestRefreshesTreeImmediately`：右键移动路径后，目标文件夹包含该请求、根位置不再包含、`RelPath` 正确、选中与目标链展开正确，且恰好请求 1 次重绘。
  - `TestMoveRequestByIDRefreshesTreeImmediately`：拖拽路径（`moveRequestByID`）同样一致并请求重绘。
  - `TestMoveFolderRefreshesTreeImmediately`：移动文件夹后旧位置消失、新位置存在、内部请求 `RelPath` 更新、选中/展开正确，并请求重绘。
  - `TestMoveRequestRefreshesRenderedTree`：真实 `ui.NewTester` 渲染视图下，移动后 `tt.Frame()` 即可在树中看到请求、目标文件夹展开——不需要重开项目。

说明：`internal/desktop/collection_tree.go`（渲染）与 `internal/core/folders.go`（MoveRequest/MoveFolder）经核对无需改动，故未改。

## 4. 验证命令与结果

均在 `cc/FIX02-tree-refresh` worktree、基线 `3cc1731` 之上执行：

```
gofmt -l internal/desktop/tree_actions.go internal/desktop/tree_refresh_test.go   # 无输出（已格式化）
go build ./...                                                                     # 通过
go vet ./...                                                                       # 通过
go test -race ./...                                                                # 全部通过
```

`go test -race ./...` 结果（节选）：

```
ok  gorequest/internal/core            6.662s
ok  gorequest/internal/desktop        81.711s
ok  gorequest/internal/desktop/uikit   2.556s
ok  gorequest/internal/gitservice      7.514s
ok  gorequest/internal/localstate      3.779s
ok  gorequest/internal/markdown        1.758s
ok  gorequest/internal/platform        1.896s
ok  gorequest/internal/runner          2.576s
ok  gorequest/examples/demo-server     2.046s
```

回归有效性自证：临时注释掉 `moveRequestTo` 里的 `requestTreeRepaint()` 后，`TestMoveRequestRefreshesTreeImmediately` / `TestMoveRequestByIDRefreshesTreeImmediately` 报 `move requested 0 repaints, want exactly 1` 而失败；恢复后通过。说明新增用例确实能拦住「缺少重绘」这一回归。

## 5. 未做人工项 / 限制

- **未做真实窗口人工验收**：本卡只做无窗口自动化验证（`ui.NewTester` + fakeWindow 记录 `Update` 调用次数）。真实 macOS 窗口下「右键移动、拖拽移动、移动文件夹后左树立即刷新、目标链自动展开、无残影」需要人工按一遍，未宣称通过。
- 重绘请求依赖真实窗口：自动化用 `fakeWindow.updates` 计数验证「发出了 1 次 Update」，但真实 `RequestFrame` 的可见刷新效果仍需实机确认。
- 未改动移动弹窗（项目菜单 `Move…`，`internal/desktop/doMove`）：该路径与其它模态按钮（`submitNewRequest`/`submitNewFolder`）一样在应用内帧内点击处理，未观察到与本卡相同的「原生/弹出菜单回调在帧外」问题；不在本卡归属文件内，故保持原样。若集成人希望统一加一道 `win.Update` 兜底，可在 `doMove` 成功后一行处理（非必需）。

## 6. 需集成人接线项

- 无。本次无需修改任何公共文件、无需 i18n 新文案、无需 go.mod/go.sum 变更。
- 合并时只需带入：`internal/desktop/tree_actions.go`、`internal/desktop/tree_refresh_test.go`。

## 7. 集成/验收建议

1. 合入后跑 `go build ./... && go vet ./... && go test -race ./...`。
2. 人工实机验收：右键请求 → `Move To…` 选目标文件夹；直接拖拽请求到文件夹；右键文件夹 → `Move To…`。确认左树立即更新（请求出现在目标文件夹、目标链展开、选中合理、源位置消失），且拖拽/筛选/选中/脏编辑保护/既有 Move 语义不受影响。
