# N05：OpenAPI 驱动 Go SDK 与受保护重复生成首阶段（C04-phase2）

负责人 CC-N05；分支 `cc/N05-go-sdk`；worktree `/private/tmp/gorequest-next5-20261109/go-sdk`。

目标：从明确的 OpenAPI 契约生成可编译、可调用本地服务的 Go SDK，提供输出清单/差异预览/人工文件保护，原生入口显式生成。不是再做一个单请求 Go Snippet。首阶段只做 Go HTTP SDK；Python/JS/TS SDK、Proto、复杂 Schema 类型生成后续，报告不能勾完整 C04。

## 先读和文件归属

读共同约束、`documentation-codegen.md` §4–5、`snippet-evidence.md`、现有 `core/openapi_types.go/openapi_export.go/changeset.go/store.go`、`snippet/`、desktop snippet/docgen/openapi 的模态/输出安全模式。

拥有新 `internal/sdkgen/`、`internal/desktop/sdk*.go`、专属测试/词条/报告。不能改 `internal/snippet`、OpenAPI 引擎、Core模型/网络/协议、其他任务文件、go.mod/go.sum。`app.go/workspace.go` 接线交 `N05-integration.patch`。

## 输入、生成契约与实现步骤

1. 纯 Go 分层：SpecSnapshot → Validate/SupportedOperations → GeneratedFiles → Preview → Apply。使用已有 `core.ExportOpenAPI` 导出的显式契约快照（JSON，固定目标版本，严格损失拒绝），不是从一次 HTTP 响应或历史数据猜 SDK。不调用 SecretProvider，不读取设备状态、Cookie/history/token、真实密码，也不发 introspection/ref 网络请求。
2. 固定 templateVersion/generatorVersion 与支持矩阵。只接受 OpenAPI3.1/3.2 的明确 HTTP operations；没有 operationId 时按 method+path 稳定派生，规范化 Go 标识符并处理关键字/数字/重名/大小写冲突，排序不能依赖 map。不能用未经转义的 description/example 当 Go 源码；quote 用 strconv.Quote，最后 go/format。
3. 生成 package、标准库 Client、每个 operation 的 Input 和方法、通用 Response、最小README、所有权 manifest。Client 持有调用者明确传入的 baseURL/http.Client，方法接 context.Context；默认有合理 timeout，body/连接始终关闭、响应读有上限，非2xx保留真实状态与有界正文/错误，取消可传播。可配置鉴权注入 callback/header provider，输出中只有调用者参数，不嵌入项目已有 Authorization/APIKey/token 示例。
4. 首阶段支持 primitive path/query/header 参数、重复 query（明确支持的 array form/explode）、无 Body 和 application/json Body（json.RawMessage，或可证明正确的简单类型）。响应先保留 Raw JSON/bytes，不伪称生成所有 Schema 类型。明确 Required 校验、path segment 正确转义、base path拼接、不丢query、不双重编码。header 参数不能注入换行；不任意覆盖Host/Content-Length等受管理头。
5. 对 multipart/binary/复杂对象序列化/多媒体 Body、远程ref、callbacks/webhooks/3.2流式、不能表达的 security AND/OR 等，生成前给 operation 级不支持诊断并默认阻断，不能悄悄跳过后叫完整 SDK。可以明确选择“只生成支持子集”，但 UI 预览必须列出跳过项并需用户选择，不能默认勾选。按规范快照引用离线schemas需复用安全读边界，不跟随任意路径。
6. 文件输出在用户显式选择的项目外目录；禁止进入当前共享 `.gorequest` 或源码目录进行隐式覆盖。用 Store/ChangeSet 或同等摘要保护事务；所有路径经过约束，拒绝 symlink/绝对路径/路径穿越。保存前 Preview 列出新建/更新/删除/冲突、支持/不支持 operations 和生成版本。只有确认 Apply 才写盘。
7. manifest 记录每个生成文件的相对路径、内容digest、模板版本和输入spec digest；重复输入输出字节稳定，不写当前时间/run ID。已存在无manifest的文件视为手工文件，阻断覆盖；manifest内文件被手工修改也阻断整批。预览后改文件再次Apply必须拒绝；manifest自身也要校验。不要信任恶意manifest路径或将手工文件纳入自动删除。
8. 重复生成仅更新仍由生成器拥有且摘要匹配的文件；旧生成文件过期时可预览并删除，绝不删除未登记文件。一次写失败回滚先前文件，manifest最后提交；不能留下半SDK。取消不写盘，失败保留预览与错误。输出尺寸/文件数上限明确。
9. UI 独立模态：选择 Go SDK、包名/模块名、目标目录、生成预览、可见不支持项/冲突、确认输出、成功反馈。必须有真实文件产物与可重复预览，不只返回字符串。长生成后台进行、Window.Update回所属窗口，防过期 generation 回调。打开模态不发网/运行生成器。是否运行生成代码由测试完成，产品不自动执行新代码。

## 必须验证

- 同输入两次生成完全一致；中文description/path、关键字/重名operationId、路径/查询转义、required缺失、JSON body与非2xx、取消均有用例。
- 将生成文件放临时目录，通过固定 Go 工具 `go test`/`go build` 实际编译；生成模块只用标准库，测试时 `GOWORK=off GOPROXY=off GOSUMDB=off`，不下载依赖、不执行外部任意模板。用生成SDK真实访问 loopback server，核对 method/path/query/headers/body；不能只比较生成字符串。
- manifest新建/无变化/更新/过期删除、手工文件/被改生成文件/预览后变化/恶意manifest/symlink/中途写失败回滚，验证完整目录摘要。
- 注入 fake secrets/敏感examples/headers 到项目契约，检查生成源码/README/manifest/诊断均不含秘密；无法安全输出的契约明确阻断。
- headless真实预览/确认/取消/失败交互，两个窗口隔离；sdkgen专项 race、build/vet、普通全仓测试。交 N05报告、生产入口patch，列明原生入口尚待集成与真实平台限制。
