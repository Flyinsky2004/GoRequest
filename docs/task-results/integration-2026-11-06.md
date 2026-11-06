# B01 / B03 / B05 / B09 集成记录

日期：2026-11-06。主分支基线：`8d4e379`（已经包含 UI stage3）。本轮在独立 worktree `GoRequest-integration`、分支 `integrate/agy-deliveries` 完成合并和验证，再快进主分支；不改动主目录原有的示例环境值或调试二进制。未 push；合并时保留各功能 worktree，随后按用户要求清理已合并旧分支及 worktree，见[任务账本清理记录](../parallel-work-plan.md#8-2026-11-06-旧分支与-worktree-清理)。

## 合并来源

| 交付 | 分支末端 | 合并提交 | 交付时报告 |
| --- | --- | --- | --- |
| B01 文件同步 | `19f005c` | `a4ab037` | [B01](B01.md) |
| B05 鉴权与凭据 | `bdca843` | `45170c4` | [B05](B05.md) |
| B03 HTTP Body | `ec3d6d5` | `b9a5812` | [B03](B03.md) |
| B09 文档工作台 | `5bbe4c7` | `6336f7d` | [B09](B09.md) |

公共接线及组合修正与本报告一并提交，可从主分支提交历史定位。上述分支末端均保留在主分支祖先历史中；没有压成单个不可追溯补丁。原交付报告描述的是各分支交付时状态，其中尚未接线的说法与 patch 应以本报告为准，不要再次原样套用。

## 本轮接线与组合修正

- 普通项目加载使用有界读取和 YAML AST 安全检查，模型可保留 `extensions`、集合 `auth`；保留重复 ID 等加载期诊断，避免身份重载/重新校验时丢失或重复累积。
- 真实项目窗口生命周期自动启停文件监听；后台通知经所属窗口投递。关闭时释放同步控制器和自定义方法弹窗状态；避免监听通知回调等待自身退出。
- 顶栏保存、关闭脏标签、移动前保存与退出批量保存使用同一同步入口；请求/环境使用保留注释及未改动字段的 YAML 保存。保存文档后同步编辑器保存点。
- 移除旧同步适配器对鉴权的二次推断，采用新编辑器的 inherit/none/ref/inline 语义；修复修改 ref 后仍被旧适配器还原的问题。
- 用 `BuildTrackedPlanWithOptions` 将 B03 的复杂 Body/文件边界与 B05 的敏感追踪组合在同一发送入口。旧 `BuildPlan` 调用复用复杂 Body 构建器；错误、发送状态与历史 URL 使用脱敏结果。
- 按项目 ID 与规范化本地克隆路径绑定凭据后端；显式注入提供者和环境变量仍可参与解析，不因打开项目而丢失测试或运行时注入。
- 文件同步刷新时同时更新文件夹鉴权缓存，避免新集合变量已生效但鉴权仍用旧值。
- 合并 KV 表格冲突，保留现有拖拽/Alt 改序、焦点键盘流转、密度和自动 Header 展示，并加入 Raw Encoded Query。保留 URL 变量来源标签和点击补变量入口，同时展示编码后的 URL。
- 环境草稿单独持有打开时的路径和基线；后台刷新、删除或更名不能将该草稿悄悄提升为最新基线。保存冲突保留磁盘版本和未保存草稿，不复活文件。
- B09 文档页接入已有工作台；B01 刷新后新增 Markdown 可打开，保持大纲、搜索、附件和导航模块独立。

## 实际验证

平台：darwin/arm64，Go 1.27.3。以下在最终代码上执行并通过：

```text
go build ./...
go vet ./...
go test -race ./...
```

最终全量竞态结果：`examples/demo-server`、`internal/core`、`internal/desktop`、`internal/desktop/uikit`、`internal/platform` 全部通过；桌面包约 35 秒。构建包含 `cmd/gorequest-desktop`。

新增 `internal/desktop/delivery_integration_test.go`，验证各分支单独测试不能覆盖的组合：

- `TestMergedBodyAuthAndSyncedSave`：修改已有鉴权 ref 后同步保存，urlencoded Body 携带 Secret 向本地服务实发，凭据和正文不丢失，敏感值被追踪，保存重开保留 Body 类型。
- `TestMergedSyncRefreshesFolderAuthAndDocumentSearch`：正常项目加载文件夹鉴权，外部更改后同步更新有效鉴权，新文档随刷新进入项目并可打开。
- `TestMergedManagerStartsAndStopsFileWatcher`：使用与生产一致的管理入口启动监听，通过 UI 队列发现外部文档，关闭窗口回收监听和控制器。
- `TestMergedEnvironmentDraftRetainsOpeningBaselineAcrossRefresh`：环境草稿编辑期间外部改盘并刷新，保存拒绝覆盖，磁盘内容和草稿均保持。

现有鉴权实发、Body 模式保存重开、原始 Query 编码、URL 变量交互、冲突 UI、文档工具、帧预算、多窗口隔离和关闭保护测试共同回归通过。此证据不等于真实窗口或三个 OS 平台的人工验收。

## 剩余边界

1. 本轮完成四个代码交付的合并和自动化验证；任务账本只勾选本轮集成子项，不将整张正式版卡自动改为已验收。
2. B05 的复用方案管理、文件夹鉴权编辑、系统凭据录入/管理弹窗、设备目标策略配置和生产操作确认未完成。目标/重定向策略已有 Core 端口及诊断，完整传输接线仍归后续 B04/B05；不能声称已执行全部策略。
3. 文件监听使用有界轮询和防抖，未实现三个 OS 的原生文件事件源。保留注释/语义字段不等于字节级排版完全不变；多文件变更集不宣称具备完整格式迁移发布流程。
4. 文档 Core 目前直接依赖 `github.com/egoist/mygo/ui/markdown/parser`。`go list -deps ./internal/core` 确认 MyGo 依赖只有这个纯 Go parser 子包，无 GUI/窗口/渲染/平台传递依赖；但严格的“Core 不导入任何 MyGo 包路径”目标尚未满足，列入 R01 处理，不因纯 Go 而隐去差距。
5. 真实 macOS 窗口、文件选择器/钥匙串交互、图片解码显示、输入法/高 DPI/辅助功能，以及 Windows/Linux 实机均未验证。各交付报告的已知限制继续适用。
6. Runner、OpenAPI/OAuth、多协议、Mock、性能、Mermaid、代码生成等未因本次合并变为已完成；后续按账本依赖安排。
