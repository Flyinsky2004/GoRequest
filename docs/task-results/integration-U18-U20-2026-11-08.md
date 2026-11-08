# U18–U20 集成与验证（2026-11-08）

用户要求合并全部已完成任务并清理旧分支/agent。本轮范围为U18–U20；其他项目、用户终端与MyGo showcase PR分支保留。

## 来源与合并

主工作区main基线 `27db078`。三个来源worktree均干净、都有提交/报告，Herdr均done。原有dev.yaml、document.md与demo-server debug产物已记录内容摘要，不带入集成。独立工作区 `/private/tmp/gorequest-u18-u20-integration-20261108/worktree` / `integrate/U18-U20`，依次no-ff合并：

| 任务 | 来源头 | 合并提交 |
| --- | --- | --- |
| U18 | `c3eff83` | `67e2a15` |
| U19 | `2d97ef7` | `db267c2` |
| U20 | `7b205c5` | `7a5adaf` |

全部无冲突；manager.go的openProject与showWelcome按函数归属自动合并。没有待应用的U19补丁，U18新增的Select在组件层自动获得统一外观。

## 已集成的行为

- Secret可显式选择系统Keychain或本机 `~/.gorequest/secrets` 文件存储，按项目/克隆隔离，默认Keychain、不静默降级；本地文件明确标记明文，目录0700/文件0600，实际值不入项目Git。
- 切换先展示复制计划/冲突，复制与读回校验成功后保存选择并刷新provider/缓存；来源副本保留。冲突可保留目标或显式覆盖；HTTP/WS/Runner等通过选中provider读取。
- 所有原生下拉入口改为窗口内绘制的共享MenuButton；Select统一新建菜单的几何/色彩/高亮，保留每项图标与可控弹簧。AST扫描确认无ui.MenuButton/ui.Select残留、SelectBase仅在组件内部。项目/环境/标签/文档/身份/Runner/Mock下拉实际接线，右键上下文与顶层系统菜单栏明确不属于本任务的点击下拉。
- 欢迎窗口默认720×640，创建面板默认560高、放大窗口封顶600；小窗口正文滚动，取消/创建操作仍可达。

## 集成审阅与修正

新增回归首先实际复现下列问题（首次失败输出保存在临时regression-before.log），随后修正：

- 迁移时Get的false同时表示缺失与读取失败，锁定/损坏的值可能被跳过后继续切换。现在InspectSecret区分missing/bound/unavailable，读取失败明确中止并回滚本轮写入，不在错误信息中包含值。计划与执行时探测文件有效性，空计划也不能绕过来源文件损坏；探测只在用户动作执行，移除桌面begin函数的重复探测。
- 目标Set可能已写入却报告晚期失败，原回滚日志在Set成功后才追加，会遗漏这一次写入。改为写入前记录旧值，并验证晚期失败时恢复原目标值。
- 偏好损坏/版本或后端标识不支持时原先会回到Keychain。SelectedKind现在返回显式错误，Manager打开项目不会静默换后端；仅缺少偏好时使用默认值。偏好与manifest增加版本检查。
- 文件读取现在也拒绝符号链接存储根；偏好/清单不能通过链接根读取。已有“不可用目标”用例更新为更早拒绝链接manifest根，不再为了构建计划而读取它；仍断言来源方案和值保持。
- 从文件枚举出来、未声明/未经过管理UI的名称，在复制到不可枚举Keychain前后需保持可发现性；切换成功前将计划内已有值的名称加入manifest，失败回滚目标写入。新增真实桌面路径验证复制后管理列表仍有该名称。
- 原子文件保存依靠重命名前已设0600的临时文件，不在rename后再做多余chmod而制造“已提交却报失败”的窗口。没有依赖变更。

## 验证与证据

- `go build ./...`、`go vet ./...`：通过。
- Secret/迁移/文件后端/身份/环境/菜单AST与真实下拉/欢迎几何专项：通过。
- 最终 `go test ./... -count=1`：完整执行，仅4项旧失败，其余通过。已在合并前main `27db078`实际复跑相同4项，输出一致：
  - desktop TestMockEditorSuggestionsAreProjectUnique：copy suggestion x。
  - desktop TestRunnerEditorDescribeAssertions：want 6 specs, got 7。
  - desktop TestRunnerEditorPatchSuiteExtract：comments lost。
  - diagnostics TestEngine_PolicyBlockedAndMissingSecretPreflight：expected target policy error for unconfirmed prod environment。
- 上述4项断言/实现未修改，普通全仓没有skip。全仓race结果见下节，显式且仅跳过这4项旧失败。
- 更早的全仓运行曾保留U18“链接根仍构建计划”旧断言而失败；该断言随更早拒绝的实际新契约调整，未跳过用例。首次回归/中间运行/最终结果均留日志，不以重跑成功掩盖问题。
- `git diff --check 27db078 -- internal README.md docs/decisions.md docs/storage-format.md`通过；go.mod/go.sum无变更。测试、快照、merge映射日志仅留本机临时目录，不入Git。

## 限制与未验收

- 未启动真实桌面窗口，headless UI/可控时钟/假Keychain/临时目录与loopback HTTP测试不等于真实系统弹窗、macOS输入法、跨平台视觉与权限验收。未读取、迁移或清理用户真实Keychain/真实Secret文件。
- 文件后端是明文，依赖文件权限；Windows ACL行为未实机验证。文件读改写只有进程内协调，没有跨进程锁；切换是一次复制，不是持续双写。
- 旧Keychain中既未声明也未进入清单的条目无法枚举；不会声称迁移整个系统凭据库。来源不可用时拒绝切换，需要先恢复其可读性。
- Select保留可控弹簧；MyGo绘制Popover菜单未提供表面动画钩子，迁移后是统一窗口内主题菜单，并不代表所有Popover均有开合bounce。
- 欢迎窗口默认尺寸固定，极小屏幕系统窗口处理未实机验证；小窗口滚动与footer可达由headless覆盖。


## 最终race结果

```sh
go test -race ./... -skip '^(TestMockEditorSuggestionsAreProjectUnique|TestRunnerEditorDescribeAssertions|TestRunnerEditorPatchSuiteExtract|TestEngine_PolicyBlockedAndMissingSecretPreflight)$' -count=1
```

退出码0、所有包通过，无race报告（desktop 113.071s，uikit 11.417s，platform 2.339s）。仅跳过前述4项已复现旧失败，没有跳过新增Secret迁移、菜单或窗口用例。最终普通全仓与build/vet也在全部修正之后执行，结果如上。
