# FIX01 任务交付记录：同步冲突误报与“恢复”回退

- **任务 ID**：`FIX01`（同步冲突误报 / self-write 被当成外部修改 / “恢复”回退）
- **分支**：`cc/FIX01-sync-conflict`
- **实际目录**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX01`
- **共同基线**：`3cc1731459ff1c44d70cbd48b0e5ea3fa7773caf`（main）
- **状态**：代码修改 + 回归测试已完成，本地验证通过；未做真实窗口人工验收（见 §4）。尚未勾选 `docs/parallel-work-plan.md` 总账本。

---

## 1. 根因

用户反馈：修改一个请求并本地保存后，过一会儿弹出“外部修改冲突”横幅；点“重新加载磁盘版（恢复）”后内容回到旧版本、编辑丢失。

排查后确认，**核心原因是 SyncEngine 没有记录 GoRequest 自身的部分磁盘写入**，导致 watcher 把“自己的写入”当成“外部修改”：

1. **self-write 漏记账**：`saveTabSynced` 的普通保存路径会调用 `RecordLocalSave` 并从磁盘回读字节，本身是一致的。但请求的**就地重命名**走的是 `ps.project.RenameRequest → core.SaveRequest`（重写同名文件、内容变化），**移动/复制**走 `MoveRequest / SaveRequest`——这些路径都直接写盘，**没有**通知 `SyncEngine`（`RecordLocalMove` 在桌面侧从未被调用）。于是 `SyncEngine.lastSnap` 停留在旧摘要。
2. **伪冲突触发**：随后 watcher 触发 `ScanAndSync`，`DiffSnapshots` 因 `lastSnap` 陈旧而生成 `SyncModified` 事件；若此时该请求标签仍有未保存编辑（Dirty），旧逻辑把任何“脏缓冲 + 磁盘变化”都判成三版本冲突。而这些“磁盘变化”其实就是 App 自己刚才的重命名写入。
3. **恢复即回退**：该伪冲突的 `DiskBytes` 是“改名后、旧 URL”的磁盘内容，横幅上的“重新加载磁盘版”会把标签内容替换为这份旧内容，用户的 URL 编辑被静默回退。
4. **watcher ack 过宽（伴随缺陷）**：`AcknowledgeCurrent()` 原本按**整棵树** token 确认，本地保存一个文件时会把同一时间窗内**其它文件**的真实外部变化一并“确认掉”（漏报外部修改），与“真正的外部修改必须识别”相冲突。

## 2. 改动文件

仅改动本卡归属文件（未触碰公共文件）：

- `internal/core/file_sync.go`
  - `ScanAndSync` 的自写抑制从“仅干净缓冲”扩展为：**当事件 `NewDigest` 等于缓冲自身读取基线 `BaselineDigest` 时，无论是否 Dirty 都判定为自写反射，不产生冲突**；对脏缓冲同时把该实体加入 protected 集合，避免其内存条目被磁盘副本覆盖。真正的分歧（`NewDigest != BaselineDigest`）仍照常冲突。
- `internal/core/file_watcher.go`
  - `AcknowledgeCurrent(paths ...string)`：支持**按路径**确认。无参时保持原有“整树确认”语义（`SyncNow` 全量扫描后使用）；传路径时只推进这些路径的基线，其余文件保留旧基线，从而**不会吞掉同一窗口内其它文件的真实外部修改**。
  - 内部改为维护 `baselineFiles map[string]string` 并派生 token（新增 `computeTreeState` / `treeTokenOf`；`computeTreeToken` 保留为薄封装），`flushState` 在结算时保存整份状态。
- `internal/desktop/file_sync.go`
  - `recordLocalSave` 改为 `acknowledgeWatcher(relPath)`，只确认刚写入的路径。
  - `captureTabSaveConflict` 同样只确认冲突文件自身路径。
  - `acknowledgeWatcher` 签名改为可变参数并转发。
- `internal/desktop/file_sync_ui.go`
  - 冲突横幅上的**破坏性快捷操作**（“重新加载磁盘版”“接受删除并关闭”）不再一键直接执行，改为打开三版本模态框，让用户在对照“读取基线 / 磁盘 / 当前编辑”后**明确选择**；仅保留非破坏性的“另存为副本”为内联一单击。

新增回归测试（本卡新增文件）：

- `internal/core/file_watcher_fix01_test.go`
  - `TestFIX01SelfWriteSameBaselineSuppressesConflict`：脏缓冲 + `NewDigest==Baseline` 时不冲突；真实分歧仍冲突。
  - `TestFIX01AcknowledgePathDoesNotSwallowOtherExternalChange`：按路径 ack 不吞掉其它文件的真实外部变化；整树 ack 仍能结算。
- `internal/desktop/file_sync_fix01_test.go`
  - `TestFIX01LocalSaveAndSelfRenameProduceNoFalseConflict`：普通保存与 App 自身就地重命名都不产生伪冲突，且未保存编辑与重命名都被保留、可继续保存。
  - `TestFIX01RealExternalModificationStillConflictsWithThreeVersions`：真实外部修改仍冲突，且保留基线/磁盘/编辑三方。
  - `TestFIX01ResolveKeepEditedDoesNotRevertToOldBaseline`：解决冲突时保留用户编辑，不回退到旧基线/磁盘版本。
  - `TestFIX01RealWatcherRepeatedSaveNoConflict`：真实后台 watcher 下反复编辑保存不产生伪冲突。

## 3. 验证命令与结果

在 `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX01` 下执行：

```bash
go build ./...
go vet ./internal/core ./internal/desktop
go test -race -count=1 ./internal/core/ ./internal/desktop/
```

- `go build ./...`：通过。
- `go vet ./internal/core ./internal/desktop`：通过。
- 全量 `go test -race ./...`：见下方“最终结果”。
- 新增针对性回归：`go test -race -run TestFIX01 ./internal/core/ ./internal/desktop/` → `ok`。
- 修复前复现证据：重命名（App 自写）后 `SyncNow` 得到 `kind=modified` 伪冲突，`DiskBytes` 为“旧 URL”内容，点重新加载即回退；修复后该用例通过。

## 4. 未做的人工项 / 限制

- **未做真实窗口人工验收**：三版本模态框的实际键鼠交互、分栏拖拽观感、macOS/Windows/Linux 实机均未验证；仅用 Go 测试与 `ui.NewTester` 无头渲染。
- 本次未在 `tree_actions.go` / `tab_actions.go` / `folders.go` 的移动/复制/重命名调用点接入 `RecordLocalSave/RecordLocalMove/RecordLocalDelete`（这些文件不在本卡归属内）。当前修复通过“同一基线的自写抑制 + 按路径 ack”在不改动这些文件的前提下消除了误报；但更彻底的做法仍是让这些自写点直接向 SyncEngine 记账（见 §5）。
- watcher 仍为有界树摘要轮询 + `Trigger()`，未接入平台原生 FSEvents/inotify（本卡不涉及）。

## 5. 需集成人接线项（可选，更彻底）

若要进一步从源头消除“自写漏记账”，建议在下列调用点补记 `SyncEngine`（需在公共/相邻卡文件中做最小接线）：

- `internal/desktop/tree_actions.go:commitRename` → `RenameRequest` 成功后，对受影响路径调用 `ctrl.recordLocalSave(新路径, 新磁盘字节)`。
- `internal/desktop/tab_actions.go:doMove` / `tree_actions.go:moveRequestTo` → `MoveRequest` 成功后调用 `ctrl.recordLocalMove(oldRel, newRel, content)`。
- `internal/desktop/tree_actions.go` 复制请求/文档 → 调用 `ctrl.recordLocalSave(newRel, bytes)`。

当前实现已在缺少上述接线时保证“不误报、不回退”，上述接线属于性能/记账完备性优化，非必需。

## 6. 最终验证结果

在 `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX01`（`cc/FIX01-sync-conflict`）实际执行：

```
$ go build ./...
（通过，无输出）

$ go vet ./internal/core ./internal/desktop
（通过，无告警）

$ go test -race -count=1 ./...
ok  gorequest/internal/core          6.994s
ok  gorequest/internal/desktop      82.013s
ok  gorequest/internal/desktop/uikit 2.573s
ok  gorequest/examples/demo-server   2.023s
ok  gorequest/internal/gitservice    7.984s
ok  gorequest/internal/localstate    4.287s
ok  gorequest/internal/markdown      1.335s
ok  gorequest/internal/platform      1.956s
ok  gorequest/internal/runner        2.274s
（exit 0）

$ go test -race -count=1 -run TestFIX01 ./internal/core/ ./internal/desktop/
ok  gorequest/internal/core
ok  gorequest/internal/desktop
```

修复前复现证据（`TestFIX01LocalSaveAndSelfRenameProduceNoFalseConflict` 的重命名分支在修复前会失败）：
`false conflict ... kind=modified disk="...url: '{{baseUrl}}'..."` —— 即 App 自身重命名被误判为外部修改，且“重新加载磁盘版”会把 URL 回退为 `{{baseUrl}}`。修复后该用例与其余 FIX01 回归全部通过。

未做：真实窗口人工键鼠验收、三平台实机验证（见 §4）。

