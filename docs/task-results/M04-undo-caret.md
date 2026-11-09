# M04 撤销/重做光标恢复交付

负责人：当前 Herdr 交互 Codex，单代理，未启动子代理。分支 `codex/M04-undo-caret`，独立 worktree `/private/tmp/gorequest-M04-20261109`。实际共同基线 `fe3a9b5`，包含 M03 集成 `4a69141` 和集成验证登记 `d9e6a4f`；实施前已完整阅读方案、AGENTS、账本 §47–50 及 M01–M03 报告。

实现提交：`4783c7f75ad6a83dd75025409d7e192ace8ff86b`。加强原生 IME 确认测试提交：`7a178223b38441c8face912fa138a5a338e6c763`。

## 基线失败

新增 `TestM04MiddleDeleteCaret` 通过真实 `App.View` 画布点击、Home/Right 定位、Backspace 和 Cmd+Z 复现用户问题。在“甲乙丙丁戊”删除“丙”后，撤销正文恢复，但选区 `{Anchor:0 Head:0}`，应为 `{Anchor:9 Head:9}`；可见 UTF-16 光标应在 3，caret X 应为 38.6965 DIP，实际跳到 0。日志 `/tmp/M04-baseline-repro.log`。

命令：`go test ./internal/desktop -run 'TestM04MiddleDeleteCaret|TestDocCanvasUndoIsTheDocumentHistory' -count=1`。新增光标断言失败，原有正文撤销用例通过；未将基线失败算作验证通过。

## 根因和实现选择

`docBlockUndo` 原来恢复 canonical 文本后无条件 `be.ed.Select(0,0)`。`commitDocBlock` 取得的 selection 已是编辑后的值，直接保留它也无法恢复 Backspace 前位置或反向选区。

只读核对锁定 MyGo v0.7.4 的 `ui/editor/buffer.go`、`state.go`、`input.go`、`edit.go` 和命令实现：公开 Buffer.Edit 没有 owner/selection 参数；Buffer.Undo/Redo 只返回 bool。State.Insert 会把实际 replacement 选区作为 before，不能在维持原 canonical 最小 replacement 的同时任意指定操作前后选区。因此采用最小的版本关联选区元数据，没有新增 reflection/unsafe、没有修改依赖或缓存。

- canonical Buffer 仍独占正文及 undo/redo 历史，原来的最小 Edit 和事务粒度保持；从不调用局部 Buffer.Undo 代替全局撤销。
- 在输入发生前、rich/native replacement 和格式入口捕获 Anchor/Head；after 使用提交时的真实选区。两者均转换为整篇 canonical UTF-8 源字节坐标，不排序丢失方向。
- `docCaretHistory` 仅关联最多 512 条选区和节点链接，不保存正文或文档快照。逐条淘汰最旧关联，保留最近关联。关联头同时验证 Buffer 对象及 Version；只有 canonical Undo/Redo 确认成功后才沿相应元数据边移动。
- 新输入覆盖该节点的 redo 关联，canonical 本身负责清除 redo；重复的相同正文变化仍区分 Backspace/Delete 的原 caret，不按文本内容猜测事务身份。
- 依据历史选区 Head 重新找到目标块，通过 Projection 恢复局部源选区，由已有 InlineView 转成可见 UTF-16 选区/真实 caret。沿用 softbreak、UTF-8/UTF-16、隐藏 delimiter 和代码围栏映射；不死守当前块起点。清除旧的首次点击位置，防止下一帧重新覆盖恢复结果。
- 源码模式、表格、结构操作、外部编辑或 Reset 导致版本不匹配时，丢弃过期关联。未知 undo/redo 使用实际前后源码的 UTF-8 安全最小变化范围末端定位，不能重放旧选区，也不一律设为段首。结构事务仍由 canonical 全局撤销。
- 恢复 canonical editor 的选区、聚焦目标活动块，并按 Buffer.Modified 更新 Dirty。原保存、模式 flush、文档滚动和输入法生命周期继续使用既有路径。
- Canvas 局部快捷键覆盖 Cmd/Ctrl+Z 和 Cmd/Ctrl+Shift+Z；没有全局按键劫持，源码编辑器仍由既有原生编辑器处理。

## 验证内容

新增 M04 测试均走实际 App.View 画布和按键；矩阵的部分初始选区通过原生/可见选区 API 设置。另有 Home/Right 定位和真实反向鼠标拖选用例，未把所有 API 设置选区描述成鼠标操作。

| 场景 | 检查 |
| --- | --- |
| 中英文、emoji | 中间/首尾 Backspace、前向 Delete、非空/反向选区删除和替换；UTF-8 字节与 UTF-16 可见坐标 |
| inline/softbreak/list | code、strong、link 隐藏语法、跨 softbreak、列表内容；每次 undo 恢复源文本与定向选区，redo 恢复编辑后选区 |
| 逐帧实际 caret | 16 项矩阵分别执行 Cmd/Ctrl undo 和 Shift redo；undo 连续三帧检查 Selection、TextCaret 几何和实际 caret 像素，无后续帧归零 |
| 历史 | 多次 undo/redo、无中间帧删除后立即撤销、重复文本变化但不同原 caret、undo 后新输入清 redo |
| 跨块 | 第一块删除、第二块插入，连续撤销/重做；只切换到另一块后撤销；块起点随前文恢复而移动；块合并 undo/redo |
| native code/IME | 代码反向替换及位置；native/rich 多次 preedit 不进 canonical，在 preedit 仍活动时直接确认，单步 undo 恢复原定向选区 |
| 未知事务 | source 模式切换及另一 owner 的公开 State.Insert；直接 canonical Edit；以实际变化定位，不用旧 metadata |
| 元数据生命周期 | 1100 次事务容量上界、外部 Edit、Reset、Buffer 对象替换导致关联失效 |
| M01–M03/表格 | 完整三包普通测试；专项 race 保留列表/代码/表格、跨行、slash、M03 焦点/临界宽度/滚动/softbreak/native 选区回归 |

## 命令和结果

全部通过，无 skip。最终运行时生产代码与实现提交一致；随后仅把 IME 用例加强为活动 preedit 直接确认，并补跑相关普通测试及全部 M04 race。

- `go test ./internal/docedit ./internal/doctable ./internal/desktop -count=1`：0.128s / 0.107s / 42.398s；`/tmp/M04-final-tests.log`。
- `go build ./...`：exit 0；`/tmp/M04-build.log`。
- `go vet ./...`：exit 0；`/tmp/M04-vet.log`。
- `go test -race ./internal/docedit ./internal/doctable -count=1`：1.360s / 1.328s；`/tmp/M04-race-source.log`。
- `go test -race ./internal/desktop -run 'TestM04|TestM03(Readme|SoftBreak|Scroll|Critical|Native|Inline|List)|TestM02|TestM01|TestDocTable|TestAuthoringIntegratedTableEditUndo|TestDocCanvasUndo|TestDocCanvasSlashOffset|TestDocCanvasCrossLine' -count=1`：31.545s；`/tmp/M04-race-desktop.log`。本次没有重复完整 M03 截图矩阵的 race。
- `go test ./internal/desktop -run 'TestM04NativeCodeAndComposition|TestM04RichCompositionAndFormat' -count=1`：0.870s；`/tmp/M04-ime-confirm.log`。
- `go test -race ./internal/desktop -run TestM04 -count=1`：7.927s；`/tmp/M04-ime-confirm-race.log`，包括最终加强的原生确认测试。
- 先后审查 production/test diff 和 staged 内容；`git diff --check`、`git diff --cached --check` 均通过。只提交本卡实现、测试、报告和账本，没有纳入临时产物。

## 限制与交接

- **未集成 main、未实机 IME/三平台验收**。M04 与完整 B09 保持未勾选，当前交互会话保留。
- Headless App.View、真实输入客户端协议和 caret 像素是自动化证据，不等于操作用户原生应用或真实候选窗。没有运行全仓 race。
- 没有 metadata 的事务，以及超过 512 条关联容量的旧历史，仍可 canonical undo/redo，但只能以实际源码变化末端恢复折叠 caret，不能还原未知事务原来的方向选区。版本失配后宁可失去选区关联，也不错误重放历史。原 canonical 的历史容量不变。
- 完整跨块范围选择、复杂对象/嵌入块编辑、完整 bidi 亲和继续沿用 M01–M03 的范围；本次跨块验证针对历史目标定位和块合并，不声称增加任意跨块富文本选择。
- 未启动子代理，未 merge/main、push、部署、重启应用或关闭其他会话。未修改全局配置、依赖、模块缓存、主目录用户数据/debug 文件；所有日志在临时目录。

## 集成人后续记录（2026-11-09）

来源头 `2a311c4` 已通过 `c45afc6` 合入 main，包含实现 `4783c7f` 和 IME 测试 `7a17822`。main 上全仓普通测试无 skip 全通过（desktop 42.860s）、build/vet 通过；docedit/doctable 全套 race（1.565s/1.541s）和 M04/M01–M03核心/表格/画布专项 race（33.180s）通过。未运行全仓 race。账本冲突保留启动与交付记录。

确认来源干净、头不变、全部提交已在 main 中后，关闭 M04 tab，确认 `t6b` finished 后移除记录；非 force 删除来源 worktree 和分支，目前仅 main/主 worktree。用户示例数据及debug产物摘要不变，其他会话未操作；未 push、部署或重启应用。本节更新历史“未集成/会话保留”状态，未知/超容量历史的选区限制和实机验收边界仍有效。
