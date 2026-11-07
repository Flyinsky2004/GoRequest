# DOC01 — GitHub README 产品展示重写

- 任务：DOC01，面向 GitHub 访客重写 `README.md`，由使用者视角介绍 GoRequest。
- 分支 / worktree：`cc/DOC01-readme` / `/private/tmp/gorequest-doc01-20261108`
- 共同基线：`3c8f0de`（`docs: register DOC01 product README task`）
- 范围：仅修改 `README.md`，新增本报告；未改代码、依赖、`go.mod`/`go.sum`、总账本、其他文档或全局配置。
- 未派子 agent，未 push / merge，未操作其他终端。

## 1. 事实核对来源

| 内容 | 依据 |
| --- | --- |
| 产品定位、场景、能力边界 | `docs/product-requirements.md`、`docs/implementation-status.md` |
| 已集成功能与首版限制 | `docs/parallel-work-plan.md` §31、U01–U10 交付报告、各阶段集成报告 |
| Go 版本 | `go.mod`：`go 1.27.3` |
| 发布目标与产物 | `.github/workflows/release.yml`、`mygo.json` |
| 许可证 | `LICENSE`：GNU AGPL-3.0 |
| Logo | `assets/brand/gorequest-512.png`（仓库内真实资源） |
| 启动与示例命令 | `cmd/gorequest-desktop`、`examples/sample-project`、`examples/demo-server` |

## 2. 内容结构

1. Logo、产品名、一句话定位与徽章（Go 版本、平台、Native UI、AGPL-3.0、GitHub stars）。
2. 「为什么选择 GoRequest」：本地优先、Git 原生、原生桌面、密钥不落库、多项目并行。
3. 「核心功能」：请求调试、环境/变量/鉴权、网络与响应、接口文档、测试与 Mock、互操作、Git 与本地历史、桌面体验。
4. 下载：链接 Releases，说明发布工作流覆盖 Windows 与 macOS 的目标和产物，并注明签名状态与「以 Releases 实际附件为准」。
5. 从源码体验：按 `go.mod` 给出 Go 1.27.3 前置条件与真实可执行命令。
6. 反馈与贡献、AGPL-3.0 许可证、精简开发文档入口。

## 3. 明确未宣称的内容

- 未使用任何截图；仓库目前只有品牌 Logo 资源，没有产品截图。
- 未宣称发布包已成功上传或可下载；只说明发布工作流的目标平台，并指向 Releases 页面。
- 未宣称 Linux 发行包；发布工作流仅覆盖 Windows 与 macOS。
- 未宣称 GraphQL、gRPC、Mermaid 渲染或 SDK 生成已支持；这些能力不在当前代码中，README 未列出。
- 未宣称跨平台实机验收、性能跑分或完整功能验收；README 保留「仍在持续开发中」的状态说明。
- 未把桌面应用描述为 CLI；明确唯一产品入口是桌面应用，不提供独立 CLI。
- 未在首页堆砌内部实现、ADR、任务账本或验收历史；开发文档入口只保留面向开发者的少量文档。

## 4. 验证

仅做文档相关的静态与链接检查，未运行 Go 构建或测试（本次无代码改动）：

- 检查 README 中所有相对链接指向的文件是否存在。
- 检查徽章与外部链接的 URL 形式。
- 检查 `git diff --check` 无空白错误。
- 检查改动范围仅为 `README.md` 与本报告。

## 5. 限制

- 徽章中的 GitHub stars 由 shields.io 动态读取；离线或网络受限时可能不显示。
- Releases 页面当前是否有可用附件取决于仓库发布状态，README 已按「以实际页面为准」表述，未虚构发布成功。
- 产品能力描述基于当前仓库代码与实现状态文档；真实窗口、跨平台人工验收仍以对应验收记录为准。
