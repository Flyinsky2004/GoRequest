# U21–U25 集成与验证（2026-11-08）

用户明确要求开一个 agy 合并全部已完成 U21–U25 任务并清理旧分支和 agent。本轮范围为 U21–U25；其他项目、非本批用户终端与 MyGo showcase PR 分支保留。

## 1. 来源与合并映射

主工作区 `main` 共同集成基线为 `a12e525`（`docs: assign U21-U25 integration and cleanup to agy`）。五个来源 worktree 均干净、有完整本地提交与交付报告，对应 Herdr 会话均为 `done`。主工作区用户未提交文件（`examples/sample-project/.gorequest/environments/dev.yaml`、`examples/sample-project/.gorequest/docs/document.md`、`examples/demo-server/__debug_bin457189101`）已登记 SHA-256 摘要，不带入集成。

在独立集成工作区 `/private/tmp/gorequest-u21-u25-integration-20261108/worktree`（分支 `agy/integrate-U21-U25`）依次执行 `--no-ff` 合并：

| 任务 | 分支 | 来源头 | 合并提交 |
| --- | --- | --- | --- |
| U21 | `cc/U21-menu-zh` | `7a992f7` (`7a992f79242e8cd69579b85c49899d237c2c55ee`) | `74341f4` (`74341f49ea9638bcc087995fd3bee186549f4b9c`) |
| U22 | `cc/U22-import-zh` | `be2139f` (`be2139fdae38746821101bf910fab288d2fa8fab`) | `d29ed89` (`d29ed89a2ad909912b638ee5775659697937b388`) |
| U23 | `cc/U23-curl-duplicates` | `2141d04` (`2141d041ce46c04f71d83cdd336aff650fd5c369`) | `17990e3` (`17990e39b607e6f732056d9b95129c8cd524d44b`) |
| U24 | `cc/U24-sse-stream` | `5c74af2` (`5c74af20f7bab3234fda869831b7770bd4744a60`) | `4e3bc40` (`4e3bc409b4a82ed088720acb7d5fcf76a0c2e716`) |
| U25 | `cc/U25-app-updates` | `176df3f` (`176df3f892a3a06ea952a6e193e3b0af57567ef5`) | `2d7cdcc` (`2d7cdcc727ad90cfe0aecdc2c4772e313e249553`) |

五条分支全部 `--no-ff` 自动合入无冲突，`internal/desktop/app.go` 的 U24（`send`/`finishStreamSend`/`cancelAllRequests`）与 U25（`App.View` 的 `updateModalFor` 及 `settingsModal`）按函数区域合并，未覆盖彼此回调或生命周期。

## 2. 公共补丁与集成审阅修正

1. **`U21-interop-i18n.patch` 核对（不重复套用）**：
   - U21 交付了 `docs/task-results/U21-interop-i18n.patch` 修正 `import_export_i18n.go` 的语言选择器。
   - 核对 U22 代码确认 `internal/desktop/import_export_i18n.go` 已实现 `interopLanguage()` 与 `interopMsg()` 按每帧读取 `cfg.language()` 切换中英文，已完整覆盖该补丁，因此未重复套用。
2. **`U23-import_export_ui.patch` 精确接入**：
   - U23 交付的 `docs/task-results/U23-import_export_ui.patch` 为零上下文格式（`@@ -102,0 +103 @@`），因 U22 修改了 `internal/desktop/import_export_ui.go` 行号，直接 `git apply` 会落到文件末尾。
   - 集成时手工在 `internal/desktop/import_export_ui.go` 的 `importModal` 来源分段切换回调（`uikit.SimpleSegmented(c, p, &st.ImportSourceKind, sourceLabels)`）内接入 `st.ConflictPolicyIdx = defaultConflictPolicyIdx(st.ImportSourceKind)`，使从通用「导入 (cURL / Postman / 便携包)…」入口进入并在模态框内切换到 cURL 时同样自动默认「分配新 ID 副本」（`ConflictRenameNewID`），切回 Postman/便携包时恢复「冲突时阻止」（`ConflictFail`）。
3. **导入/导出提示横幅与繁忙错误的双向语言热切换**：
   - 审阅发现 `importModal` / `exportModal` 的 `st.ImportNotice`（含已导入文件变更提示、撤销提示、陈旧计划重规划提示）、`st.ExportNotice` 及 `ImportBufferBusy` 错误在已生成状态下热切换语言时未经过显示层双向转换。
   - 在 `internal/desktop/import_export_display.go` 新增 `displayImportNotice`、`displayExportNotice` 并补齐 `ImportBufferBusy` 的中英双向转换，在 `internal/desktop/import_export_ui.go` 接线渲染，保证模态框打开期间切换中英文时所有诊断、冲突、错误与通知横幅在下一帧同步更新。
4. **多项目窗口下更新弹窗宿主迁移与测试确定性修正**：
   - 审阅 U24 + U25 的 `App`/`Manager` 生命周期发现：当同时打开两个项目窗口且更新弹窗宿主为第一个项目窗口时，关闭第一个项目窗口（此时 `len(m.workspaces) > 0` 不触发 `showWelcome`）会使 `updateController.host` 仍指向已销毁窗口。
   - 在 `internal/desktop/manager.go`（`onWorkspaceClosed`）、`internal/desktop/update_controller.go`（`post`）与 `internal/desktop/update_ui.go`（`modalFor`）补齐已销毁宿主检测与自动向存活项目窗口转移宿主并触发 `Update` 的接线。
   - 审阅发现 `internal/desktop/update_test.go` 的 `TestUpdateManualCheckQueuedBehindAuto` 含一处 `time.Sleep(time.Millisecond)` 轮询等待；按“测试用 fake clock/channel，不以 sleep 判定正确性”要求，改为通过 `fakeUpdateChecker.onCall` 通道阻塞与同步唤醒完成并发排队验证，消除 `time.Sleep`。
5. **组合回归测试（`internal/desktop/u21_u25_integration_test.go`）**：
   - `TestU21U22U23ProjectMenuGenericImportSourceSwitchAndRepeatedCURLChinese`：验证中文项目菜单同时包含 U21 全部追加组与 U22 导入导出项、菜单与模态框中英文热切换、从通用导入菜单进入并在 UI 内切换来源到 cURL 时默认策略自动变为新副本、连续 3 次导入相同 cURL 生成独立 ID/文件且首份内容不变、中文预览/重命名标记/脱敏生效、撤销第 3 份保留前 2 份、陈旧预览自动重规划并支持中英文热切换。
   - `TestU24U25SSEAndUpdateModalCoexistWithCrossChunkRedactionAndHostTransfer`：验证 `App` 同时运行多 Tab 实时 SSE 流与 Manager 更新弹窗、SSE 首事件在服务器 EOF 前已在 UI 可见、跨 chunk 拆分的 Secret 脱敏与跨 chunk UTF-8/CRLF 重组、多 Tab 事件隔离、运行期中英文热切换同时作用于 SSE 状态与更新弹窗、取消单条 SSE 流保留已收事件且不影响另一 Tab 流与更新弹窗、关闭宿主项目窗口时取消该窗口 SSE 流并将更新弹窗平滑转移至存活项目窗口。

## 3. 验证命令与结果

- **未合并基线 `main` (`a12e525`) 4 项历史失败复核**：
  - `go test ./internal/desktop -run '^(TestMockEditorSuggestionsAreProjectUnique|TestRunnerEditorDescribeAssertions|TestRunnerEditorPatchSuiteExtract)$' -count=1`：复现 3 项既有失败（`copy suggestion: "x"`、`want 6 specs, got 7`、`comments lost`）。
  - `go test ./internal/diagnostics -run '^TestEngine_PolicyBlockedAndMissingSecretPreflight$' -count=1`：复现 1 项既有失败（`expected target policy error for unconfirmed prod environment`）。
  - 未修改上述 4 项断言或被测逻辑掩盖问题。
- **构建与静态检查**：
  - `go build ./...`：退出码 0。
  - `go vet ./...`：退出码 0。
  - `gofmt -l`（本轮改动文件）：无输出。
  - `git diff --check a12e525 -- internal cmd examples docs`：通过；`git diff a12e525 -- go.mod go.sum` 无变化。
- **普通全仓测试（不带 `-skip`）**：
  - `go test ./... -count=1`：完整执行，除上述 §37 记录并已在基线复核的 4 项既有失败外，其余所有包与新增用例全部通过（`core` 7.760s、`desktop/uikit` 2.259s、`gitservice` 10.055s、`localstate` 4.126s、`platform` 1.549s、`runner` 3.481s、`snippet` 2.022s、`update` 0.544s 等）。
- **受影响专项与全仓 `-race` 验证**：
  - `go test -race ./internal/core ./internal/update -run '^(TestCURL|TestStageAutoImport|SSE|EventStream|NonSSE|TestCheck|TestDownload|TestState|TestParse|TestCompare|TestIsDevelopment|TestChecksum|TestSafe|TestToday)' -count=1`：全部 `ok`（`core` 2.228s、`update` 1.445s），无 race。
  - `go test -race ./internal/desktop -run '^(TestProjectMenu|TestInterop|TestDesktopRepeatedCURL|TestDesktopCURLStale|TestDesktopImportModalDefaultPolicy|TestDesktopImportReplanned|TestSSE|TestTabSSE|TestUpdate|TestInstalledVersionLabel|TestU21U22U23|TestU24U25)' -count=1`：`ok`（10.766s），无 race。
  - 全仓 race（显式且仅跳过 4 项已复现旧失败）：
    ```sh
    go test -race ./... -skip '^(TestMockEditorSuggestionsAreProjectUnique|TestRunnerEditorDescribeAssertions|TestRunnerEditorPatchSuiteExtract|TestEngine_PolicyBlockedAndMissingSecretPreflight)$' -count=1
    ```
    退出码 0，全部 21 个包通过，无 race（`desktop` 122.357s、`uikit` 11.305s、`core` 9.001s、`gitservice` 8.923s、`localstate` 4.645s、`runner` 3.944s、`docgen` 3.343s、`platform` 2.682s、`mockserver` 2.312s、`snippet` 2.262s、`update` 1.248s）。未额外跳过任何新增或既有测试。

## 4. 限制与未验收范围

- 本轮全部验证均为命令行构建、单元/集成测试、MyGo headless `ui.Tester`、可控 fake clock/channel、fake 下载/安装器与本地 loopback `httptest`，**未启动真实 macOS/Windows/Linux 桌面窗口，不冒称实机窗口视觉、鼠标交互、输入法或安装器替换验收**。
- 测试未请求真实 GitHub Releases 公网 API、未下载或运行真实发布安装器、未修改或替换用户已安装应用、未读取或写入用户真实 Keychain/Secret。
- 各功能既有边界保持各分报告说明：
  - U21/U22：各子功能模态框内部非菜单/非导入导出区域的存量英文文案不在本轮范围；未知 Core 错误保留原始英文技术详情并带中文分类前缀。
  - U23：桌面提供「撤销上次导入」栈顶回滚；`diskRequestIDs` 在导入 staging/apply 时扫描项目请求文件。
  - U24：SSE 为单次请求流式查看，不含 `Last-Event-ID` 自动重连；单事件 256 KiB、总正文 32 MiB、UI 待处理缓冲 8 MiB 超限截断并提示。
  - U25：开发运行（`mygo.IsDev()` 且无注入 `buildVersion`）明确显示开发版本、不参与升级比较；下载校验后仅打开系统安装器/磁盘映像，不退出当前应用、不冒称已自动完成安装。

## 5. `main` 快进与来源清理

（在完成本地提交、`main` 快进及 5 个来源 Herdr tab/worktree/分支清理后补记最终哈希与清理结果。）
