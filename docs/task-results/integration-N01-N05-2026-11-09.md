# N01–N05 集成、验证与清理记录（2026-11-09）

负责人：主代理。用户授权合并全部本批交付、清理旧分支/agent、同步任务状态。main 起点 `5a60910`；隔离集成分支 `codex/integrate-N01-N05`，worktree `/private/tmp/gorequest-next5-20261109/integration`。

## 来源与合并

| 任务/来源 | 核对的来源头 | no-ff 合并提交 |
| --- | --- | --- |
| N01 四项旧失败 | `a7c2025` | `07f31c7` |
| N02 Suite 编排/数据配置 | `c1094eb` | `7ff853c` |
| N03 受控 Mock/Runner | `a67ea36` | `eaef3b9` |
| N04 GraphQL Query/Mutation | `be6e960` | `f64dd78` |
| N05 Go SDK | `01d72ac` | `c3ec43e` |
| 上轮 U32 对比度补充 | `cd82d47` | `38fc2af` |
| 上轮 U35 退出指纹补充 | `d22ae13` | `1cb412a` |
| 上轮 U36 存储边界文档补充 | `953723e` | `8af2270` |

五个来源工作区均干净，八个来源头与用户已有文件当前摘要已在集成前保存在本机 `integration-start.json`。没有 cherry-pick 后丢弃来源历史。唯一合并冲突是 U36 报告：保留新增容量/捕获矩阵与验证清单，同时保留 main 中已完成的原始 SSE/错误输出接线说明，未恢复过时限制。

## 公共入口与独立审查修正

- N02/N03 的 Runner 入口 patch 分别接入编排编辑、Mock 配置和结果；子编辑器可操作时不再由父 Runner 模态拦截输入。N04 接入新建菜单、专属编辑视图和保存/外部刷新草稿重置。N05 接入项目菜单与应用模态。保留原 patch 作为历史交付证据，不重复应用。
- Suite 草稿加入项目窗口/退出/更新的脏状态、保存/放弃/取消保护；无效数值输入仍算未保存；保存冲突不关闭，更新启动失败保留草稿，放弃授权在内容变化后失效。无效 GraphQL 文档/Variables 也直接进入退出指纹，不依赖能否构建合法请求。
- SDK 预览绑定目标目录、包/模块名、版本、子集/覆盖/删除选项及契约快照；任何变化必须重新预览。关闭/重开使旧后台预览失效。
- SDK Apply 每步写入前重新核对摘要；回滚不会覆盖期间产生的外部编辑；manifest 路径的中间 symlink 也拒绝，不能借旧文件删除越出输出目录。
- GraphQL 的复合变量同时注册完整值与底层 Secret，防止服务只回显 Secret 子串时漏脱敏；普通 HTTP/WS 及原网络安全策略保持。

集成接线/修正提交 `0503adc`。新增集成回归位于 `internal/desktop/next5_integration_test.go`、`internal/core/graphql_compound_secret_test.go`、`internal/sdkgen/apply_concurrent_test.go`。只使用临时项目、fake 安装器/Secret、loopback；不读取真实密钥，不触发真实安装器或退出用户应用。

## 验证证据

- 第一轮接线后 `go build ./...`、`go vet ./...`、`go test ./... -count=1 -timeout 180s` 全部通过（desktop 35.155s），未 skip；N01 原四项失败已消除。
- 独立审查修正后，core/sdkgen/desktop 相关专项实际通过；包含真实 SDK 写入/冲突/回滚、目录 symlink、Suite 子模态按钮输入、窗口关闭、更新失败保留、保存冲突、过期预览与复合 Secret。
- `go test -race ./... -count=1 -timeout 300s` 全部通过，无 skip、无 race；desktop 146.352s、UIKit 12.010s、core 11.376s、sdkgen 3.851s、suiteedit 2.409s。
- 最后补充无效 GraphQL 草稿指纹后，`go test -race ./internal/desktop -run 'TestSDK|TestSuiteDraftParticipates|TestInvalidSuiteLimit|TestInvalidGraphQLDraft|TestHandoff' -count=1 -timeout 90s` 通过（4.028s）。该项是在上述全仓 race 后单独验证的最终增量，不声称它已包含在早先全仓命令中。
- 最终 `go build ./...`、`go vet ./...`、`go test ./... -count=1 -timeout 180s` 再次全部通过（desktop 35.343s、core 7.537s、sdkgen 3.022s、suiteedit 2.125s），包含最后指纹增量，不 skip。完整输出存本机 `final-test.log`、`final-race.log`。文档与暂存内容的 `git diff --check` 通过。

## 完成范围与仍需验收

| 任务 | 当前完成范围 | 继续保留的范围 |
| --- | --- | --- |
| N01 | 生产注释丢失修复、三项错误 fixture 校正及更精确回归；原四失败消除 | 不涉及实机产品体验验收 |
| N02 | Suite 编排、依赖/排序、已有数据源配置/预览、安全保存与关闭保护已集成，自动化通过 | 无数据单元格编辑/已有 ID 重命名/完整分页；实机/三平台待验收 |
| N03 | Mock 显式启停、快照/实际端口注入、Suite 生命周期、取消回收已集成，自动化通过 | 不含 Mock 元数据 JSON/JUnit 导出、完整复杂 Schema 生成；实机待验收 |
| N04 | GraphQL Query/Mutation 首阶段已集成，自动化通过 | Subscription、SDL/Explorer/Introspection/补全和业务自动化仍缺；不是完整 P02 |
| N05 | Go HTTP SDK 首阶段及安全预览/重复生成已集成，生成模块实编译和 loopback 调用通过 | 其他语言 SDK、Proto、完整 Schema 类型模型仍缺；不是完整 C04 |

以上是阶段实现和自动化验证，不能把完整 A02/C01/P02/C04 或 R01 实机/三平台发布门槛勾为通过。主任务账本、实现状态页和五卡报告同步当前状态，旧交付验证保留为历史证据。

## main 与清理

待最终提交/验证完成后记录 main 快进、app 打包及精确清理证据。清理前必须确认来源头未变、工作区干净且提交已是 main 祖先；只操作本批登记的五个 CC 与八条来源分支，保留其他用户终端/项目和本地未提交示例文件。非 force 删除，不 push。
