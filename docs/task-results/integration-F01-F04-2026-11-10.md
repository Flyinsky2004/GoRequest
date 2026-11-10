# F01–F04 集成验收（2026-11-10）

用户要求合并、清理本批 agent/分支并同步任务文档。main 起点 `6a0d2a6`，集成分支 `cc/integrate-F01-F04`；四路共同开发基线 `08eb3d6`。本文更新各来源报告的历史“未接线/未集成”状态。

| 卡 | 来源头 | 合并提交 | 本阶段验收 |
| --- | --- | --- | --- |
| F01 | `7a260da869ba4ac553b10514415ae7d67d0a3f5f` | `b99a1d5` | Runner 非交互 OAuth、契约校验、并发与结果分页已集成 |
| F02 | `9db46f1` | `15f6583` | SDL/Explorer/Introspection/交互订阅、有限 WS 脚本及 Runner 接线已集成 |
| F03 | `fb9b1776e4c25b80b72946c4d727a6c7070d4ca8` | `638b63e` | 原生离线 flowchart/sequence 子集、诊断及导出已集成 |
| F04 | `5bf90d1` | `f0e3d74` | Go 类型模型及 TypeScript HTTP SDK、输出保护已集成 |

## 集成修正

`c1f17ce` 将 F03 接入原生文档、预览及诊断入口。`bd1978d` 接入 F02 协议适配器与 Suite 选择器、Runner 和 Mock 快照，增加协议端到端测试；协议步骤携带实际数据集变量和依赖检查回调，读取项目冻结文件并验证摘要，非交互 WS 连接执行目标策略。

GraphQL 响应含业务 errors 默认判失败；需要预期错误响应时可显式保存 `Extensions["graphqlAllowErrors"]=true`。已绑定 SDL 时预检进行语义验证。WS 报告保存脱敏 `protocolFacts`，错误时仍保留协议事实，不伪造 HTTP 200。订阅仍是交互功能，Runner 不支持订阅。

修复标签/窗口关闭时订阅和 Introspection 的取消及过期回调；异步工作使用冻结输入。Introspection 保存使用新路径和摘要冲突保护，保留手写 Schema。SDL/Query 解析深度有界；Mermaid 缓存避免强引用滞留已关闭文档。TypeScript 对“命名属性 + 有类型 additionalProperties”明确拒绝，避免产生无法赋值的交叉类型；纯 map 和 unknown 扩展仍按支持矩阵处理。

F02 来源 go.mod 唯一未提交变更是已有传递依赖 `github.com/agnivade/levenshtein v1.2.2 // indirect`，已纳入 `bd1978d`；清理前保存来源 diff 并核对 main 同行，之后仅还原这一已集成变更。

## 验证

- 最终 `go test ./... -json -count=1 -timeout 600s` 全通过，25 个有测试的包通过，desktop 46.065s；没有测试级 skip。两个无测试文件包不算验收测试。日志：`/private/tmp/gorequest-formal4-20261110/final-tests.jsonl`。
- `go test -race ./... -count=1 -timeout 900s` 全通过，desktop 301.691s。日志：`/private/tmp/gorequest-formal4-20261110/integration-race.log`。
- 全仓 race 编译后补充的 TS 类型拒绝改动另经 SDK race（6.721s）验证；最终桌面接线专项 `go test -race ./internal/desktop -run TestFormal4 -count=1` 通过（3.215s）。最终普通全仓测试覆盖最终代码。
- `go build ./...`、`go vet ./...` 通过。使用本机已有 TypeScript 编译器，通过 `GOREQUEST_TSC=/Users/flyinsky/.npm/_npx/2db181330ea4b15b/node_modules/typescript/bin/tsc` 执行生成产物类型检查，没有跳过 TypeScript 验证。
- 实际原生控件测试覆盖 Schema Explorer、保存不覆盖手写文件、关闭标签取消订阅、关闭时取消 Introspection；协议回环服务验证 GraphQL 业务错误与 WS 关闭事实。检查 F03 headless 图像证据，未把离屏画布当作真实桌面窗口。
- 初次新增取消测试夹具未消费 HTTP 请求体导致超时，已修正并加入失败清理；接口调用参数类型错误在编译阶段已修正。两项均写入 LESSONS.md；以上最终验证均为修正后结果。

## 完成范围

本批四张阶段卡的代码、入口集成和自动化验收完成。完整正式版仍有 gRPC、Python SDK、全部 Mermaid 语法、GraphQL Schema 代码补全及 Runner 订阅等范围。真实窗口、输入法、系统凭据及三平台验证没有完成，相关完整目标不勾选。M01–M04 已合入的编辑修复随本次全仓回归通过，不新增实机验收结论。

main 合并和清理的最终结果见任务账本 §51.3。
