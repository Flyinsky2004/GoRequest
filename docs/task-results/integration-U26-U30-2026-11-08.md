# U26–U30 集成与清理记录（2026-11-08）

## 负责人、基线与来源

最终由Codex主agent执行。用户中途要求关闭集成agy，精确关闭t5k（tab tbb096ecaf8f5f48d086a08303d38672f5，ok:true），确认hc finished后rm；该agy未留下代码/提交。接管干净工作区，将agy/integrate-U26-U30重命名codex/integrate-U26-U30。

共同集成基线59e0f75；工作区/private/tmp/gorequest-u26-u30-integration-20261108/worktree。来源均在合并前确认干净，顺序使用no-ff合并，无冲突：

| 任务 | 来源头 | 合并提交 |
| --- | --- | --- |
| U26 单一顶栏新建菜单 | da2d613 | 3ed917b |
| U27 关于与检查更新 | 6b7358d | 8265413 |
| U28 本机密钥成功关窗 | ddc07e9 | 001008e |
| U29 输入与提示语义配色 | 05e5ee6 | 272475c |
| U30 响应树形SVG图标 | f42f8eb | 620be27 |

## 集成修正与接线

- 校验并实际应用U29-integration.patch至settings.go、update_ui.go、secret_entry.go及response_view.go。所有生产主按钮通过uikit.PrimaryButton构造，保留品牌红；普通输入焦点/光标/选区取青蓝语义，真实错误保留红色。树形搜索匹配色使用Focus，U30的16px SVG、20px点击区及叶子占位保留。
- 复核U27发现全局aboutRequested可被其他已开设置窗口抢先渲染消费，改为UI线程目标窗口aboutHost。Manager在目标显示/聚焦前登记，只有该窗口的settingsBody消费；欢迎/项目窗口均覆盖。新增TestUpdateAboutNavigationStaysWithSelectedWindow以另一窗口先渲染复现并验证隔离，原更新弹窗跳转测试继续通过。
- U26保留完整创建菜单与快捷键，删独立新建请求胶囊，使用单一SVG下拉入口；U27关于中展示开发者、邮箱、项目链接和完整更新控制；U28仅写入成功自动关窗、清空临时敏感输入和刷新缓存，失败保留输入可重试；U30改用清晰SVG并保持行对齐、展开搜索复制功能。

## 验证

最终代码上go build ./...、go vet ./...均退出0；git diff --check通过；go.mod/go.sum无变更。

专项go test ./internal/desktop -run 'TestUpdateAboutNavigationStaysWithSelectedWindow|TestUpdateDialogSettingsOpensAbout' -count=1通过（0.666s）。普通全仓go test ./... -count=1无skip，仅以下4项既有失败，desktop 33.616s；其余用例通过，未改这些旧断言：

- TestMockEditorSuggestionsAreProjectUnique：copy suggestion: "x"
- TestRunnerEditorDescribeAssertions：want 6 specs, got 7
- TestRunnerEditorPatchSuiteExtract：comments lost
- TestEngine_PolicyBlockedAndMissingSecretPreflight：expected target policy error for unconfirmed prod environment

最终全仓race：

```text
go test -race ./... -skip '^(TestMockEditorSuggestionsAreProjectUnique|TestRunnerEditorDescribeAssertions|TestRunnerEditorPatchSuiteExtract|TestEngine_PolicyBlockedAndMissingSecretPreflight)$' -count=1
```

退出0，无race；desktop 126.331s、uikit 12.686s、core 10.310s、update 1.441s。仅明确跳过4项已知旧失败，未额外skip；此验证包含本轮公共patch及目标窗口跳转修正。清理已完成，见下节。原始日志存/private/tmp/gorequest-u26-u30-integration-20261108/final-{build,vet,test,race}.log，不提交临时日志。

## 验证边界

使用headless MyGo ui.Tester、临时项目、fake存储/更新及loopback服务。未启动真实桌面窗口，不冒称实机鼠标/键盘/输入法、高DPI、亮暗视觉或邮件/浏览器打开验收。未读取或写入真实用户Secret，未实际安装更新，未push。用户原有dev.yaml、document.md和两个debug产物保留，摘要在本机登记供清理前后核对。


## main与清理结果

代码与验证报告提交8a04310，main从59e0f75通过ff-only快进至该提交。逐个再次核对来源头均为main祖先、工作区干净且HEAD与登记一致后，精确关闭5个Herdr tab（均ok:true）：

| 任务 | 终端 | tab |
| --- | --- | --- |
| U26 | t5d | tb3970d9794c9cb60f8005a4eeed6d25d1 |
| U27 | t5f | tb915f72029242d093c3d174fea98924db |
| U28 | t5g | tb9354889fd2fa9e658812091f041e2449 |
| U29 | t5h | tbd6935a22b2503729ce4f525050e34a3d |
| U30 | t5j | tba0bfd93260ecf811a2f815414b00c2f9 |

全部确认hc finished后rm，非force删除5个来源worktree及分支。集成worktree干净、集成分支为main祖先后，同样非force移除codex/integrate-U26-U30及其worktree。先前集成agy t5k已移除；本批共6个agent会话清理完毕。仓库仅main及主worktree，其他项目/用户会话保留。

清理后用户dev.yaml、document.md及两个debug产物SHA256与初始快照完全一致，未push。最终清理文档另提交到main，不改变已验证产品代码。
