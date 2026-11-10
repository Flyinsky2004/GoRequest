# F04：规范驱动 SDK 类型模型与 TypeScript 首阶段

先读 formal4-common-20261110.md。分支 `cc/F04-sdk`，worktree `/private/tmp/gorequest-formal4-20261110/sdk`。独占sdkgen和sdk desktop文件；不改core OpenAPI、snippet或公共菜单/依赖清单。

## 现状和目标

现有Go SDK支持primitive参数/JSON RawMessage，缺响应类型模型/组合类型，只有Go目标。阶段目标是Go类型生成与TypeScript HTTP SDK首阶段，共用显式契约快照、可审阅兼容报告和受保护生成。Python/JS独立SDK、Proto和gRPC本体仍留后续，不将Snippet算SDK。

## 实施顺序

1. 读documentation-codegen.md、sdkgen/spec/generate/apply、OpenAPI exporter及3.1/3.2支持矩阵，查目标Go/TypeScript官方文档确定编译和类型语义。先建立Schema→中间类型模型→语言后端，保留现有客户端默认行为/公开API兼容。
2. 支持并测试object/properties/required、数组、enum、nullable、optional、additionalProperties与本地ref；组合类型allOf/oneOf/anyOf/discriminator先明确可准确表达范围，不支持默认阻断并报告，不能映射成any后宣称完整类型。递归ref必须有界且可表示，不在展开时死循环。JSON Schema nullable联合与3.0 nullable不混用，optional和null语义分开。
3. Go生成输入/请求/按状态响应类型和error模型，明确指针/optional表示与JSON字段行为，避免丢失“未设置/零值/null”区别。生成可编译示例，保留Raw逃生口但在矩阵标注。不将token、示例敏感值、服务器地址/绝对路径写死。
4. TypeScript首后端采用固定最低TS/运行时契约、明确fetch依赖范围，生成types/client/operations/error和配置示例；路径query编码、重复header等无法等价时诊断而非静默合并。请求取消、超时、响应上限、认证注入口与TLS原则说明。客户端不能偷偷自动重试危险方法或执行生成代码。
5. UI新增语言/模型选项、支持/损失报告与preview；相同输入稳定输出，manifest包含目标语言/模板版本，防两种语言生成相互覆盖。保留手工文件、外部变化/过期预览、stale删除确认、symlink防护、写失败回滚。必要公共菜单只交patch。
6. 生成模块测试应离线编译。Go可用现有toolchain；TypeScript先检测已安装node/tsc，不自动联网安装；若工具缺失，必须提供可运行验证入口和明确未验证项，不伪称TS编译通过。本地回环服务验证两语言相同Query/JSON/Auth/error/cancel语义，复杂multipart/binary另列矩阵。

## 验收

多状态响应、CJK命名/关键字冲突、enum/null/optional、组合/递归ref正反例、deterministic输出、生成代码编译与回环等价请求。manifest跨语言/版本、改动文件/目录symlink/外部并发/中途失败回滚。fake secrets所有输出/日志无泄漏；UI切语言/过期预览/生成冲突实际控件检查。

跑sdkgen完整测试和race、desktop SDK专项、build/vet；写docs/task-results/F04.md和支持矩阵/工件验证命令。未经执行的TS编译必须明确待验证，不能勾完整C04。
