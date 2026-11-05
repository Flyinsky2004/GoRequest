# 集合测试、独立 CLI 与 CI

状态：1.0-A 需求基线；命令、参数、退出码及结果字段为设计草案，尚无可用 `gorequest` 程序。

## 1. Collection Runner 与 Test Suite

Collection 用于组织请求，Runner 可按显式顺序运行集合或文件夹；Test Suite 用于业务场景，可跨集合选择请求，声明依赖和数据传递。不得把显示排序或磁盘遍历顺序隐式当作测试依赖。

单请求支持断言；套件叠加步骤断言、变量和重试策略。执行前一次性校验 ID、依赖环、环境、必需密钥、数据集、Schema 与目标策略；无法建立有效计划时不发送任何请求。

前置/后置能力先采用声明式动作：设置变量、生成动态数据、准备或清理显式测试资源。任意 JavaScript/Go/shell 脚本不在初期范围。未来脚本需独立安全评审与授权机制，CI 默认禁用未知脚本。

## 2. 数据驱动与状态隔离

- CSV 表头映射字段，JSON 使用对象数组；明确 UTF-8、空值、缺列、数字/布尔等类型转换，转换失败定位到行与字段。
- 每行创建独立迭代上下文、Cookie Jar 和步骤输出，失败不能污染下一行。大数据集采用受限流式读取，限制行数及资源。
- 独立步骤可限制并发；有依赖的步骤等待所有必需上游成功。并发分支不可共同写普通变量；下游通过 `steps.<id>.<output>` 读取显式输出。
- 动态 UUID、时间、随机字符串只在指定作用域生成一次并缓存，不能每次模板引用都变化。固定种子和冻结时钟用于可重复测试，但业务秘密仍来自凭据后端。
- 运行变量不回写共享环境，敏感属性沿提取、模板和报告传播。

## 3. 断言与提取

| 类别 | 要求 |
| --- | --- |
| HTTP | 状态码、Header、Cookie、Body 文本/JSONPath、响应大小与耗时 |
| Schema | 根据指定 JSON Schema/OpenAPI Operation 校验请求或实际响应，输出实例路径、期望与实际 |
| 数据比较 | exists/equals/contains/类型/范围/正则；明确空值与缺字段不同 |
| GraphQL | 同时区分传输、`data` 与 `errors`，允许声明预期的部分错误 |
| 流协议 | WebSocket/gRPC/Subscription 在时限内等待满足条件的消息，校验序列、数量、状态与关闭 |
| 统计 | 重复采样的 P95 等阈值单独标识样本范围，不混入单次延迟断言 |

JSONPath 和正则方言、类型强制规则、耗时边界属于待定格式契约，必须在文档及 Core 中统一。提取支持 JSONPath、Header，后续扩展 Cookie/文本；缺失必需输出导致步骤失败，不发布半成品输出。默认仅当传输与步骤断言成功后提交提取值；失败时原始诊断需脱敏。

## 4. 失败、重试与取消草案

`onFailure: stop` 停止启动新步骤；`continue` 运行仍可执行的独立分支，依赖失败的后继标为 skipped。套件取消标为 canceled；其余未启动步骤不伪装成通过。

自动重试默认关闭。显式策略可限定网络错误、状态码或断言类型，配置最大次数、退避、抖动和总 Deadline。非幂等请求需要明确允许；每次尝试保留事件和结果，既展示最终判定也展示重试次数，不能靠重试抹去不稳定性。取消和配置错误不重试。

网络超时、TLS/连接失败、服务拒绝、协议错误及断言失败均属于用户请求的运行失败，CLI 返回 `1`；项目定义错误/缺密钥/格式不兼容返回 `2`；执行器崩溃、无法写最终报告等本地内部故障返回 `3`。正常响应但未声明状态断言时，HTTP 4xx/5xx 作为协议事实展示，是否通过由测试规则决定；不得隐式把所有错误状态当引擎故障。

## 5. 统一结果与报告

结果模型至少包含项目/格式/Core 版本、项目快照摘要、run ID、环境名称、种子、开始/结束时间、总时长、数据行、步骤/尝试、协议事实、断言、提取元信息、Trace、失败分类和跳过/取消原因。

- JSON 为机器消费的完整结构；JUnit XML 提供 CI 测试用例与失败详情；静态 HTML 在后续阶段展示，性能采样另提供 CSV。
- stdout 的 JSON 模式只输出约定结果，进度/诊断进入 stderr；安静、详细模式都必须脱敏。
- 退出、取消和报告写入遵循最终化流程：关闭连接、停止临时 Mock、写入完整或明确标记不完整的报告。磁盘失败要如实返回错误。
- Secret、Authorization、Cookie、私钥、敏感提取值及包含它们的正文不能进入日志/报告。失败断言的“实际值”也必须脱敏。
- 报告默认写本机运行目录；仓库内显式输出仅在目标已忽略时允许。报告不作为共享项目事实来源。

## 6. CLI 命令草案

所有命令从同一项目文件加载同一 Core；不启动 GUI，不依赖 Desktop 常驻或窗口 IPC。

| 命令 | 预期用途 |
| --- | --- |
| `gorequest run` | 通过互斥的 `--request` / `--collection` / `--suite` 选择目标 |
| `gorequest validate` | 项目结构、版本、引用、变量、套件和文档静态校验；默认不联网 |
| `gorequest openapi export` | 选择 3.1/3.2 目标版本，导出 YAML/JSON 及兼容报告 |
| `gorequest openapi validate` | 本地定义、引用与语义校验；远程引用需显式许可 |
| `gorequest mock start` | 独立启动本地 Mock，默认回环监听，响应取消信号 |
| `gorequest generate` | 请求 Snippet 或规范驱动代码；预览、版本和输出冲突检查 |
| `gorequest doctor` | 检查格式支持、必要凭据/工具/本地依赖，不输出密钥 |
| `gorequest version` | 输出 CLI/Core、格式范围和协议/生成器能力 |

通用参数计划含 `--project`、`--env`、`--non-interactive`、`--json`、`--quiet`、`--verbose`；运行支持标签筛选、超时、并发、失败策略、报告和 Secret 绑定。参数精确拼写及互斥规则在格式评审时冻结。

```bash
# 以下是未来命令接口示例，并非当前可执行命令。
gorequest validate --project . --env ci --non-interactive
gorequest run --project . --suite smoke --env ci \
  --secret loginPassword=env:GOREQUEST_LOGIN_PASSWORD \
  --report junit:.gorequest-reports/results.xml \
  --report json:.gorequest-reports/results.json \
  --non-interactive
```

Secret 参数只接受后端/环境引用，不推荐直接在命令参数里传值，避免 shell 历史和进程列表泄漏。上述 `.gorequest-reports/` 必须已加入忽略规则。普通显式变量覆盖需与 Secret 通道区分。

### 退出码草案

| 退出码 | 含义 |
| --- | --- |
| 0 | 操作成功，所选测试全部通过 |
| 1 | 请求/测试运行失败，包括网络、协议、断言或明确不通过的门禁 |
| 2 | 参数、项目、密钥绑定、Schema 或不支持格式等配置错误 |
| 3 | 本地运行环境或引擎内部故障、报告落盘失败 |
| 130 | 用户中断（平台可实现的 shell 惯例），结果另标 canceled |

同次运行发生多类失败时，完整报告保留全部原因，退出码按内部故障 → 配置错误 → 取消 → 测试失败 → 成功的优先序确定。空筛选结果默认作为配置错误，不能返回“0 项测试通过”的假成功。

## 7. CI 集成

支持 GitHub Actions、GitLab CI、Jenkins 等普通进程执行环境。以下仅为工作流草案；不写入活动 `.github/workflows/`，不捏造尚未发布的安装地址：

```yaml
name: GoRequest API Tests
on: [push, pull_request]
jobs:
  api:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4  # 实际投产时固定经审核的 action SHA
      - name: Install pinned GoRequest CLI
        run: |
          # 发布后替换为固定版本、完整性校验及受信任来源的安装步骤。
          # 此占位主动失败，防止误以为 CI 已具备执行能力。
          exit 2
      - name: Run suite
        env:
          GOREQUEST_LOGIN_PASSWORD: ${{ secrets.API_TEST_PASSWORD }}
        run: |
          gorequest validate --project . --env ci --non-interactive
          gorequest run --project . --suite smoke --env ci \
            --secret loginPassword=env:GOREQUEST_LOGIN_PASSWORD \
            --report junit:.gorequest-reports/results.xml \
            --non-interactive
```

实际 CI 需准备独立测试服务、忽略的报告路径、失败时上传的报告和短期工件保留策略。Fork/未知 PR 不获得生产密钥或执行生产变更。CI 交互被禁止：不得弹浏览器、Device 授权、迁移确认或原生窗口；Client Credentials/CI Secret 是认证入口。

手动、CLI 和 OS/CI 定时调度复用同一命令。项目中的“计划”只是描述，不能因 Git 拉取就启用他人电脑的任务调度。允许主机、生产环境和危险变更规则在每次请求及重定向前检查。

## 8. 验收

完成登录 → 提取敏感 Token → 查询用户 → 更新资料 → 验证设置的业务套件；用两行数据确认隔离，在 Desktop 与 headless CLI 上获得相同判定。再验证故意断言失败、连接失败、配置错误、超时、取消、报告写失败和空筛选的退出码与报告分类。JUnit/JSON 不出现测试注入的可识别秘密值。
