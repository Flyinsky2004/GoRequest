# FIX03 任务交付记录：JSON 语法高亮标点/分隔符颜色过淡

- **任务 ID**：`FIX03`（JSON 语法高亮标点颜色过淡）
- **工作目录**：`/Users/flyinsky/Documents/Coding/GoLang/GoRequest-FIX03`
- **工作分支**：`cc/FIX03-json-syntax-color`
- **共同基线**：`3cc1731459ff1c44d70cbd48b0e5ea3fa7773caf`（main）
- **状态说明**：本分支为纯配色调整，已完成实现、自测（build/vet/`test -race`）与断言验证；**尚未合入主干，未经人工肉眼验收**，本报告不声称整卡已集成。

---

## 1. 问题与根因

请求/响应 JSON 高亮中，花括号 `{}`、方括号 `[]`、逗号 `,`、冒号 `:` 等结构/分隔符号几乎看不清。

根因：`internal/desktop/uikit/syntax.go` 中 `editor.TokenPunctuation` 与 `editor.TokenOperator` 均被设为 `p.Faint`（最淡的一档文本色，alpha ≈ 0.30–0.32）。经 WCAG 相对亮度计算，Faint 叠加在背景上对比度仅 **浅色 1.82:1 / 深色 2.48:1**，远低于可读阈值，因此在两种主题下都过淡。

## 2. 改动内容

### 2.1 取色逻辑（`internal/desktop/uikit/syntax.go`）

```go
// Punctuation and operators (JSON braces, brackets, commas, colons) are
// structural separators. Faint was too low-contrast to read; sitting
// 60% from Text toward Muted keeps them clearly legible on both the light
// and dark backgrounds while staying one step softer than the keys.
punctColor := p.Text.Mix(p.Muted, 0.60)
...
set(editor.TokenPunctuation, punctColor)
set(editor.TokenOperator, punctColor)
```

- 新色 = `Text` 向 `Muted` 混合 60%（复用既有 `ui.Color.Mix`，不改 palette.go）。
- 同步更新文件内 token 映射注释，说明标点改为 `Text` 与 `Muted` 之间的中间色。

### 2.2 未改动

- 只改 `TokenPunctuation` / `TokenOperator` 两处取色。`key/property`（Text）、`string`（GET 绿混 Muted）、`number`（PUT 蓝）、`boolean/null`（PATCH 紫）、`XML tag`（PUT 蓝）、`XML attribute`（POST 琥珀混 Muted）、`comment`（Faint）等映射意图全部保持原样。
- 未改解析/渲染结构，未动 `palette.go`、公共壳层（i18n.go/app.go/project_state.go）及其他卡文件。

## 3. 选色理由与对比度

选择依据：既要清晰可辨，又不应像 key/value 那样鲜艳——需在 `Text`（键色，最高对比）与 `Muted`（次级文本）之间落一档，保持“标点弱于正文、强于最淡”的层次。

以 WCAG 2.x 相对亮度、按 0.60 混合后的实际合成色计算（叠加在各自背景上）：

| 主题 | 新标点色合成结果（约） | 新对比度 | 原 Faint 对比度 | WCAG AA 正文 4.5:1 |
| :--- | :--- | :--- | :--- | :--- |
| 浅色 | `#5f5f63` | **6.36:1** | 1.82:1 | 通过 |
| 深色 | `#b5b5bb` | **9.02:1** | 2.48:1 | 通过 |

- 两套主题均从“几乎不可见”提升到正文级可读对比（≥4.5:1）。
- 亮度严格介于 `Text` 与 `Muted` 之间（测试断言校验），保证标点比键值柔和但仍清晰。

## 4. 验证结果

| 检查 | 命令 | 结果 |
| :--- | :--- | :--- |
| 格式 | `gofmt -l`（syntax.go / 新测试） | 无输出，通过 |
| 构建 | `go build ./...` | 通过 |
| 静态检查 | `go vet ./...` | 通过 |
| 单测（uikit，含 -race） | `go test -race ./internal/desktop/uikit/...` | `ok` |
| 全量测试（-race） | `go test -race ./...` | 全部包 `ok`（core / desktop / uikit / gitservice / localstate / markdown / platform / runner / demo-server），通过 |

新增断言（`internal/desktop/uikit/syntax_theme_test.go`）：

- `TestSyntaxPunctuationReadable`：浅/深两套主题下
  - 标点与运算符同色；
  - 不再等于 `p.Faint`、也不等于 `p.Text`；
  - 叠加背景后对比度 ≥ 4.5:1（实测 6.36 / 9.02）；
  - 高于原 Faint 对比度；
  - 亮度严格介于 Text 与 Muted 之间。
- `TestSyntaxExistingMappingsIntact`：锁定 key/string/number/constant/tag 既有配色映射不被本次改动带偏。

日志摘录：

```
light: punctuation contrast 6.36:1 (Faint 1.82:1)
dark:  punctuation contrast 9.02:1 (Faint 2.48:1)
```

## 5. 需人工确认项

- 本环境无截图/像素验收能力（`TestRenderPreview` 仅做无头渲染的客观健全性检查，未验证标点可读性视觉观感）。**标点最终观感需人工在应用内查看请求/响应 JSON，确认“不抢 key/value、但清晰可辨”达到预期。**
- 若人工认为 6.36:1（浅色）偏亮/偏暗，可微调 `Mix` 比例（如 0.55–0.70）；测试对比度阈值 4.5:1 会继续守住可读下限。

## 6. Git 提交

- 本地提交到 `cc/FIX03-json-syntax-color`（不 push），提交哈希见交付回执。
- 仅纳入本卡文件：`internal/desktop/uikit/syntax.go`、`internal/desktop/uikit/syntax_theme_test.go`、`docs/task-results/FIX03.md`。
