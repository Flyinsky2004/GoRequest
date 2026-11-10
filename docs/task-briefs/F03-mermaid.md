# F03：离线 Mermaid 原生渲染与导出

先读 formal4-common-20261110.md。分支 `cc/F03-mermaid`，worktree `/private/tmp/gorequest-formal4-20261110/mermaid`。独占新增diagram、doc_diagram文件和docgen图表渲染；公共文档入口只交integration patch，不改M01–M04 doc_live/doc_code核心。

## 现状和目标

当前mermaid围栏在canvas被当普通代码，导出仅源码+warning。设计要求离线、Native、保留源码、明确兼容子集。本阶段交付经验证的flowchart与sequenceDiagram子集及离线静态导出，绝不能声称全Mermaid兼容。无需node/browser/CDN/在线渲染服务。

## 实施顺序

1. 查当前Mermaid官方flowchart/sequence文档，选定并文档化支持矩阵/版本，先写 docs/contracts/F03-mermaid-subset.md（不用修改总decisions）。先技术验证：中文字体测量、多行标签、箭头/折线、布局时限、原生绘制和SVG导出共用模型，证明可行再扩语法。
2. internal/diagram分为parser→受限AST→layout→scene/export，Core图模型不能依赖GUI，原生MyGo绘制放desktop adapter。建议flowchart TD/LR、基本节点/菱形/连线标签；sequence participant、同步/返回消息、注释等明确支持的子集。循环图布局必须有界，不用递归无限展开；不支持subgraph/复杂样式/交互指令时给准确行列错误和完整源码。
3. 节点/边/输入字节/标签长度/布局时间/输出尺寸均有预算，取消有效。禁止脚本/click危险URL/自动外链加载。escape和中文标签正确；不允许“遇到未知语法但画一张看似正确的残缺图”。
4. 新增原生diagram widget和缓存（source+theme+字号/宽度），闲置/活动共享稳定容器。查看图/编辑源码有明确入口；源码编辑复用M02能力和M04全局撤销。代码块高亮、复制、保存不退化。对canvas/preview CodeBlock hooks交公共patch，避免变动已有段落布局。
5. docgen离线HTML/Markdown导出携带同一scene生成的安全SVG/本地资源；SVG必须无外部资源/脚本，图片/路径和目标冲突检查沿用原导出规则。不支持语法保留源码+原因；受支持图不再显示“不渲染”warning。现有诊断Core接线如需修改只交patch，不隐藏其他错误。

## 验收

受支持flowchart/sequence、CJK/emoji/多行、环/长链/重名/错误行列、恶意click/script/超限/取消。原生亮暗/两档字号/窄宽窗口截图和箭头/节点几何断言，不能只给AST文本测试。断网导出后无CDN/远程字体/JS，SVG可独立查看，源码/未知块往返与M01–M04回归。

跑diagram/docgen完整测试、build/vet、关键路径race与desktop图表专项；报告具体支持矩阵、图片、离线工件和公共patch到docs/task-results/F03.md。若两个图类型任一未实现须明确未交付，不能仅解析少数语法就宣布C03完成。
