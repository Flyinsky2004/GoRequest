# N03：桌面 Runner 的受控 Mock 生命周期（C01-phase3）

负责人 CC-N03；分支 `cc/N03-mock-runner`；worktree `/private/tmp/gorequest-next5-20261109/mock-runner`。

目标：用户勾选本次运行使用 Mock，选择已保存规则/场景，点击运行后完成启动→注入实际端口→Suite→关闭；取消/失败/关闭窗口也必须释放，不写共享环境，不留监听器。

## 先读与文件归属

读共同约束、`mock-performance.md`、`mock-evidence.md`、最新集成报告，代码 `internal/mockserver/{compile,server,suite_lifecycle}.go`、`desktop/runner.go/runner_snapshot.go/runner_ui.go/mock*.go`。

拥有 `desktop/runner.go`、`runner_snapshot.go` 的必要最小改动、新 `mock_runner*.go`、专属测试/词条，`internal/mockserver/suite_lifecycle.go` 的必要准备/关闭扩展与测试。不改 `mock_rule_editor.go` 和已有失败测试（N01），不改 Suite 编排（N02）、Core/Runner 的协议/格式。`runner_ui.go` 交 patch。其他 mock_ui 公共修改尽量另文件，只交调用 patch。

## 必须保持的现有契约

- `StartRunner` 目前从磁盘新开项目，固定环境/设备网络/Secret/TLS/上传文件快照，`prepareDesktopRunner` 在 UI 线程 Prepare 后切断 provider，再把 runner 移到 worker。不能直接调用新 runner 并传活的 App、设备 provider 或重新读盘，丢掉这套隔离。
- `RunSuiteWithControlledMock` 已有生命周期和变量注入，但内部会重新 `runner.New`；不能无脑把已经 Prepare 的 runner 换成该函数并以为仍使用相同快照。
- 只回环监听，端口默认 0；共享项目/环境不得写 mockUrl；默认关闭功能；打开项目、面板或修改下拉不启动服务。

## 分步实施方案

1. 增加本次运行的 UI 配置与纯值状态：enabled、场景、端口(0/合法端口)、可选 endpointVar（默认 mockUrl/mockBaseUrl 等现有名称）。显示回环说明、场景来源与端口错误。运行中禁止修改本次配置；下一次可改。
2. 在用户 Start 后，先验证配置并冻结项目规则 CompiledSnapshot 和选定场景；不要在 worker 再加载原规则。Secret/访问记录的 Redactor 必须在服务可接收请求前注册，不能存在先启动后脱敏的窗口。若现有服务 API 不支持，最小扩展其准备流程。
3. 实际端口必须在请求 Prepare 前已知：采用可清理的 PreparedRun/lease（或等价小结构），先显式绑定受控 Mock，再复制 Suite.Variables 注入实际地址，再走现有 prepareDesktopRunner。启动/编译有界，重活移 worker时只能消费冻结的纯值，不触碰 UI。若将绑定放 UI 线程，证明处理有界且不做请求/长等待。
4. 若需拆分 prepareDesktopRunner 的 freeze options 阶段，可以提取私有 helper，但保留原函数签名与非 Mock 行为，维护所有现有测试。新 runner 只创建/Prepare 一次；Prepare 失败也必须立即释放 Mock。
5. worker defer 使用有超时的 stop，覆盖成功、断言失败、配置失败、网络错误、context 取消；Runner 和服务共用本次 run context。关闭 Runner/窗口/应用已有 CancelRunner 必须取消本次 lease。stop 幂等、可并发，无重复 close panic，无孤儿 goroutine；停止错误和实际 ServerStopped 不得假成功。
6. 接收结果继续复用 mailbox/generation/weak owner/Window.Update；项目/环境切换不串结果。旧 generation 回调不能取消新的 Mock。结果显示实际端口、场景、规则摘要和服务是否已释放；访问记录有界且脱敏。不要把 Mock lifecycle 结果塞进共享 YAML。
7. 保留普通 JSON/JUnit Runner 导出；如导出 Mock 元数据，用附加本地报告/显式字段而不破坏既有解析，记录格式版本。不能让本地完整历史选项关闭报告脱敏。
8. 交 `N03-integration.patch`，只包含 Runner 面板配置/状态显示调用和任何必要公共关闭回调；N02 同时改 Runner 面板入口，避免整段替换。

## 必须执行的测试

- 临时两步/两行 Suite 真实 loopback Mock 运行成功，实际分配端口可连，返回后再次连接失败/端口可重新绑定。
- 成功、断言失败、Prepare 配置错误、延迟期间取消、窗口/Runner 关闭、占用固定端口、坏规则/未知场景，每种都检查资源回收与真实分类。
- 开始运行后改原规则/环境/Secret fake/设备设置：本次继续用冻结快照，下次才看到新值；Git 共享文件摘要不变。
- 两个项目并发 Mock 不串端口/记录/取消；同项目快速取消重跑旧回调不污染新 generation。
- 访问日志、错误、JSON/JUnit/附加元数据均无 fake Secret 的明文及已支持编码变体。
- 原非 Mock Runner 与 TLS/上传快照回归；专项 race、build/vet、普通全仓测试，报告 N03 生产接线和未实机限制。
