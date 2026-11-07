# R7/R8 验收截图：路径与可复现步骤

## 状态

本会话**未生成实机截图**。原因：

1. 最终证据要求全部 R 项集成后的版本；当前 `cc/R-response` 只包含 R7/R8，其他 R 项仍在各自 worktree 开发。
2. 截图需要真实 macOS 窗口、交互式打开示例项目、点击发送并等待响应；本终端会话没有可用的 GUI 自动化/截图授权，无法在不干扰用户桌面的前提下完成。
3. 无窗口 `ui.Tester` 渲染只用于布局/像素断言，**不是实机截图**，不作为验收图片。

以下步骤在集成完成后由人工执行，产出两张最终证据。

## 目标文件

- `docs/task-results/screenshots/r7-r8-1200x820.png`
- `docs/task-results/screenshots/r7-r8-1024x640.png`

建议提交这两张 PNG 作为最终证据；若仓库策略不纳入二进制，则保留在本机并在集成报告中记录绝对路径与 SHA-256。

## 前置

- 仅使用回环示例服务 `127.0.0.1:4010`，不接生产、不使用真实凭据。
- 使用示例项目的**隔离临时副本**，不修改 `examples/sample-project` 或用户配置。
- 通过隔离的 `HOME` 写入窗口尺寸/语言/外观，避免改动用户 `~/Library/Application Support/GoRequest/config.json`。

## 可复现步骤

```bash
# 1) 构建（在集成后的 main/worktree）
go build -o /tmp/gorequest-r7r8/gorequest-desktop ./cmd/gorequest-desktop

# 2) 启动本地示例服务（虚构数据，内存存储）
go run ./examples/demo-server -addr 127.0.0.1:4010

# 3) 复制示例项目到隔离目录（不碰共享样例）
rm -rf /tmp/gorequest-r7r8/project /tmp/gorequest-r7r8/home
cp -R examples/sample-project /tmp/gorequest-r7r8/project

# 4) 隔离 HOME 与窗口配置（1200×820；第二张改成 1024/640）
mkdir -p "/tmp/gorequest-r7r8/home/Library/Application Support/GoRequest"
cat > "/tmp/gorequest-r7r8/home/Library/Application Support/GoRequest/config.json" <<'JSON'
{
  "language": "zh-CN",
  "appearance": "Light",
  "lastProject": "/tmp/gorequest-r7r8/project",
  "restoreLastProject": true,
  "windowWidth": 1200,
  "windowHeight": 820
}
JSON

# 5) 启动应用；登录密码用一次性虚构值，经环境变量提供，不落盘
HOME=/tmp/gorequest-r7r8/home \
GOREQUEST_SECRET_LOGINPASSWORD='demo-password-not-real' \
  /tmp/gorequest-r7r8/gorequest-desktop -project /tmp/gorequest-r7r8/project
```

窗口内操作：

1. 打开 `auth/login`（用户登录）。
2. 按 `⌘↩` 发送；确认响应区状态胶囊为 `200`，协议 `HTTP/1.1`，耗时/大小已显示。
3. 确认工具行顺序：状态组在首行首位，其后是 正文 / 头部 / Cookie / 网络，正文视图内是 美化 / 原始 / 树形，行尾是查找/复制/保存。
4. 确认第二行不存在；Cookie 视图内可见 `管理项目 Cookie 罐…`，网络视图内可见 `网络与证书…`。
5. 确认只读正文首行没有灰色当前行底。
6. 截图窗口内容（不含桌面其他区域）：

```bash
# 方式 A：交互式选择窗口
screencapture -x -w docs/task-results/screenshots/r7-r8-1200x820.png

# 方式 B：按窗口 ID（先用系统工具取得窗口 ID）
screencapture -x -l <window-id> docs/task-results/screenshots/r7-r8-1200x820.png
```

7. 关闭应用，把配置改为 `"windowWidth": 1024, "windowHeight": 640`，重复步骤 5–6，输出 `r7-r8-1024x640.png`。

## 验收记录

| 尺寸 | 文件 | 生成时间 | 结果 |
| --- | --- | --- | --- |
| 1200×820 | `docs/task-results/screenshots/r7-r8-1200x820.png` | 待集成后人工生成 | 未生成 |
| 1024×640 | `docs/task-results/screenshots/r7-r8-1024x640.png` | 待集成后人工生成 | 未生成 |

本分支已用无窗口测试覆盖 1200×820 / 1024×640 的控件边界与状态首位（`TestUI03ResponseToolRowAtAcceptanceSizes`），但该测试不替代实机截图。
