# M04：撤销删除后光标恢复到原编辑位置

用户复现：在一段文字中间删除一个字，按 Ctrl+Z 撤销，文字恢复但光标跳到段落首字前。需要实际修复，延续主代理先方案、Herdr Codex 实现方式；单代理，不开子代理。

## 根因线索与范围

主代理已确认 `internal/desktop/doc_live_edit.go:docBlockUndo` 调 canonical `Buffer.Undo/Redo` 后 `be.adopt(b)`、重置局部 buffer，并无条件 `be.ed.Select(0,0)`。这是直接导致段首跳转的代码；不能简单把0改成当前cursor：撤销删除要回到删除前位置，重做回到删除后位置，且跨块/方向选区/软换行与隐藏语法存在映射。

MyGo v0.7.4 canonical `Buffer.Edit` 是无 owner 的公开 API；底层 transaction 有 selBefore/selAfter 但属于私有实现，Buffer.Undo 只回 bool。只读核对实际 API 后选最小可靠做法，不为本问题新增 reflection/unsafe，也不改 module cache/依赖。不用全局键盘事件劫持其他编辑器。只本地 commit，不 merge/main、push、部署或重启应用。

分支 `codex/M04-undo-caret`，worktree `/private/tmp/gorequest-M04-20261109`。实际创建基线以账本为准，必须包含 M03 修复。独占 doc_live_edit 相关撤销/事务映射及必要 helper/tests；M01–M03 已认可行为不能退化。主目录用户数据/debug产物不动。

## 修改步骤

1. 先读 AGENTS.md、账本 §47–50、M01–M03交付、本方案；跑现有撤销专项。用真实 App.View 画布和按键复现：在“甲乙丙丁戊”中间鼠标/键盘定位，Backspace 删除“丙”，Ctrl+Z（macOS 同测 Cmd+Z），断言正文恢复且光标/实际caret回到删除前位置，而非0。先看到基线失败。
2. 撤销/重做继续使用 canonical 唯一正文与同一 history。优先查现有 editor.State 公开操作是否能带正确 selections 接入 canonical transaction。若不可，维护最小的选区元数据关联（canonical 事务/版本与 before/after Anchor/Head），或通过实际 undo/redo 源变化恢复可靠位置；在报告说明选型。禁止另建一套正文撤销栈、无界保存整篇快照、或与 canonical history 错位的平行栈。要处理 source模式/格式/表格/外部变化等没有本地元数据的事务，安全fallback不能一律归零或错误重放。
3. 写入 canonical 时记录/映射正确的操作前后选区；保留方向（Anchor/Head，不只Start/End）。注意当前 commitDocBlock 所拿 local selection 已是编辑后的值，恢复删除前位置不能把它当before。删除/插入/替换、backspace/forward delete、拖选替换/删除、连续撤销/重做、undo后新编辑清redo都要验证。
4. 操作前后位置应在 canonical source 坐标记录，恢复到目标块后通过 Projection/InlineView 映射为真实可见光标/选区；block start因早前内容变更而移动、块拆合或目标块不同，要定位到历史编辑目标而非死守当前 be.start。UTF-8源偏移与UTF-16客户端范围、软换行、隐藏code/strong/link delimiter保持正确。保存/Dirty/focus/scroll与撤销原行为一致。
5. 不要求剪掉原有全局历史或改成局部Buffer.Undo来掩盖问题；表格/代码块结构编辑仍可全局撤销。先最小改正本bug，再补必要的共用映射，不重构无关布局。IME预编辑不进canonical历史，确认文本后可单步撤销；不新增延迟reset导致下一帧跳回段首。

## 测试与交付

- 核心用例实际按键、连续画帧，检查源文本、ed.Selection及真实caret几何。Ctrl+Z/Cmd+Z与项目Redo快捷键按平台既有规则测试，不凭“文字恢复”就算成功。
- 中英文/emoji，段落中间删除单字，首尾删除，forward delete，非空/反向选区，含inline code/strong/link，M03软换行边界，多次undo/redo和undo后新输入。当前块变化或切换另一块后撤销，确保恢复目标位置；代码和列表也回归，不破坏表格全局撤销。
- `go test ./internal/docedit ./internal/doctable ./internal/desktop`、`go build ./...`、`go vet ./...`；M04与现有canvas undo/跨行/M01–M03核心回归专项race。不用为了本修复重复完整截图矩阵多次，除非布局发生变化/失败需要。
- 审 diff/staged，仅提交本卡相关文件。本地 commit 后写 `docs/task-results/M04-undo-caret.md`：根因、实现选择、before/after选区语义、精确命令/结果、来源提交与限制；更新账本。保持未集成/实机验收未勾，保留交互会话。不启动子代理、不动其他会话。
