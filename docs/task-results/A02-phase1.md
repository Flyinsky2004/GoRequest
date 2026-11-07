# A02 phase 1 交付记录（实施中）

负责人：Codex；worktree `/Users/flyinsky/Documents/Coding/GoLang/GoRequest-A02`；分支 `codex/A02-runner-first-loop`；共同基线 `3d51b99c29d9f583fcec8339e3ab77dc7f121643`。本记录不勾选 A02 整卡。

## 第一步：Schema 联合接线

新增 `runner.NewProjectSchemaAdapter(project)`，实现 A01 `SchemaValidator` / `SchemaPreflighter`，实例验证调用 A03 `core.ValidateParsedSchema`，无 core→runner 依赖。预检独立检查配置，不验证虚拟实例；递归遍历所有已支持 Schema 分支，包括未被实例触及的 properties/$defs/组合分支，离线解析引用并冻结到内存。最多 128 文件、合计 8 MiB、48 层、10000 Schema 节点；文件经项目 Store 读取，拒绝远程/越界/符号链接路径、循环、缺引用、缺 Schema、未知关键字或不支持的配置。运行不重读 Schema 文件。

引用规则（初始版本，未冻结）：

- `schema: {ref: schemas/user.yaml}` 或 `file.yaml#/components/schemas/User`；路径相对当前项目 `.gorequest/`，嵌套 `$ref` 相对所在文件，fragment 仅 RFC6901 pointer。
- `schema: {operation: getUser}` 选择唯一已保存 `extensions.openapi.operationId`，不可同时指定 ref。响应选择顺序：精确状态、`nXX`、default；无匹配为断言失败。
- 第一阶段 Operation 仅支持恰好一个 `application/json` body、没有 response header contracts 的响应定义；所有声明响应都须有 Schema。实际 Content-Type 不匹配为断言失败；缺 Content-Type 沿用 JSON 默认。其他媒体/头契约明确预检拒绝，不宣称完整 OpenAPI 响应覆盖。
- 支持 Schema：boolean、type、properties/$defs/definitions、items/prefixItems、additionalProperties、allOf/anyOf/oneOf/not、required、pattern、长度/数量/数值界限、multipleOf、enum/const、uniqueItems 及常规注解；仅接受 2020-12 `$schema`。不支持 `$id`/anchor/dynamicRef、递归引用、format、conditional/dependent/patternProperties/contains/unevaluated；未知关键字失败。`$ref` 与显式 allOf 同级暂拒绝。

A01 端口最小兼容增量：`SchemaRef.Headers [][2]string`，现有实现不受影响，evalSchema 传入实际响应头。Schema 断言报告不携带原始 ref；adapter 不输出 schema 常量、actual 或引擎原始 message，仅约束名与实例路径。坏配置诊断固定文字，避免预检前泄露 Schema 内容。

验证：`go test -race ./internal/runner` 通过；新增测试覆盖离线多文件 fragment、冻结后文件变化、精确 `$.data.id`、未知/缺失/循环/远程/越界引用、未触及分支坏 pattern、坏类型、零 Executor 调用、Operation 状态不匹配与无 Schema 拒绝。

第二步桌面 Runner、第三步联合回归尚在实施，后续提交补齐记录。
