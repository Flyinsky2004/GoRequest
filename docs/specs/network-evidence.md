# B04 Network & Response 设计与实现证据记录

状态：B04 分支设计与实现证据（对应 `Q-09` 传输层/代理/证书/目标策略接线、基础规格 §2–3、高级鉴权规格 §6 与 `AC-03`）。基线提交：`c0b4f61`。本文为冻结前实证材料，不直接修改 `docs/decisions.md` 总表状态，亦不擅自宣称格式或兼容契约冻结。

## 1. Q-09：传输层连接、TLS/mTLS、代理与目标策略网络挂接

### 1.1 阶段超时与即刻取消（`StageTimeouts`）

1. **阶段边界定义**：
   - `DialTimeout`（默认 `10s`）：作用于 `net.Dialer.Timeout` 及 SOCKS5 代理握手截止时间，覆盖 DNS 解析与 TCP 三次握手。
   - `TLSHandshakeTimeout`（默认 `10s`）：作用于 `http.Transport.TLSHandshakeTimeout`，覆盖 TCP 建连后的 TLS 握手阶段。
   - `ResponseHeaderTimeout`（默认 `30s`）：作用于 `http.Transport.ResponseHeaderTimeout`，限制请求写完后等待服务端首字节响应头（TTFB）的时间。
   - `TotalTimeout`（默认继承 `Plan.Timeout` 或 `DefaultTimeout = 10s`）：通过 `context.WithTimeout` 约束包含重定向与响应体下载在内的整体执行时长。
2. **下载中途取消与套接字即刻释放**：
   - 在读取响应体期间启动受控守护协程监听 `ctx.Done()`，一旦用户点击取消或超时触发，立即调用 `resp.Body.Close()` 唤醒阻塞的底层 socket 读取，并在 `ownTrans.CloseIdleConnections()` 中回收空闲连接。

### 1.2 重定向策略、跨源凭据剥离与逐跳目标策略（复用 B05 端口）

1. **三种重定向模式（`RedirectMode`）**：
   - `follow`（默认，默认上限 `MaxHops = 10`）：跟随 3xx 跳转，每一跳重新执行 B05 `EvaluateRedirectHop`。
   - `none`：在第一跳 3xx 处返回 `http.ErrUseLastResponse`，直接展示 3xx 响应头与 `Set-Cookie`。
   - `same_origin`：仅当下一跳 `CanonicalOrigin`（`scheme` + `host` + 有效端口）完全一致时允许跟随，跨源/跨端口/协议变化立即以 `*ConfigError` 拦截。
2. **逐跳安全重校验与自定义敏感头剥离**：
   - 初始请求（Hop 0）在 `HasTargetPolicy` 启用时调用 B05 `EvaluateTarget`；每个重定向跳（Hop $1 \dots N$）在 `http.Client.CheckRedirect` 中调用 `EvaluateRedirectHop`。
   - 当发生跨主机、跨端口或同主机 `https` → `http` 降级跳转时，除剥离标准 `Authorization`、`Proxy-Authorization`、`Cookie` 外，同时剥离当前 `AuthScheme` 声明的自定义 `apikey` Header **以及 `Redactor` 中登记的所有敏感请求头**，防止标准库 `net/http` 默认透传自定义鉴权头导致凭据外泄。

### 1.3 HTTP / SOCKS5 代理与本机凭据边界（`ProxyConfig`）

1. **代理模式**：
   - `system`（默认）：遵循 `http.ProxyFromEnvironment`，并支持设备级 `NoProxy` 规则先行绕过。
   - `none`：强制直连，忽略环境变量代理。
   - `http`：显式 HTTP/HTTPS 正向代理，支持 Basic `Proxy-Authorization`。
   - `socks5`：显式纯 Go RFC 1928 + RFC 1929（用户名/密码子协商）拨号器，支持 `socks5://` 与 `socks5h://`（远端域名解析），握手阶段绑定 `ctx` 取消与 `DialTimeout`。
2. **NoProxy 匹配与凭据隔离**：
   - `ShouldBypassProxy` 支持 `*`、精确主机名、`.domain` / `*.domain` 后缀匹配、`host:port` 以及 IP CIDR（如 `10.0.0.0/8`）。
   - 代理密码仅从本机内存会话或 `SecretProvider`（`PasswordSecret`）读取，同步向 B05 `Redactor.RegisterBasicPair` 登记，且 `platform.NetworkStore` 落盘 `network.json` 时强制清空明文 `Password`。

### 1.4 TLS 证书校验、自定义 CA 与 mTLS 本机私钥约束（`TLSConfig`）

1. **默认开启 TLS 校验，禁止静默跳过**：
   - `TLSConfig.InsecureSkipVerify` 默认 `false`；最低版本默认 `TLS 1.2`。
   - 当 `MergedTargetPolicy.EnforceTLSVerify == true`（本机默认开启）时，任何尝试设置 `InsecureSkipVerify: true` 的调用在发网前直接返回 `*ConfigError`。
2. **系统 CA 与自定义 CA 组合**：
   - `UseSystemCA` 默认 `true`（加载 `x509.SystemCertPool()`）；`CustomCARef` 支持通过 `local:<slot>` 或 `.gorequest/` 相对路径加载 PEM 根证书/中间证书（上限 `4 MiB`，校验有效 `CERTIFICATE` PEM 块）。
3. **mTLS 私钥仅限本机槽位映射**：
   - `ClientKeyRef` 强制要求 `local:<slot>` 本机映射槽位（由 `platform.NetworkStore` 或 `LocalFileProvider` 解析为本机绝对路径）；若引用共享项目相对路径（如 `certs/client.key`），在发网前直接拒绝并返回 `*ConfigError`，确保私钥绝不进入 Git 跟踪目录。私钥内容加载后立即登记至 `Redactor`。

## 2. 项目/会话隔离 Cookie Jar（`core.CookieJar`）

1. **无全局状态，严格按项目与会话隔离**：
   - 每个 `projectState` 持有独立的 `*core.CookieJar`（作用域为 `<projectSlug>:<hash>`），不同项目窗口互不共享 Cookie；`CloneForSession` 为单次自动化运行或数据行提供深拷贝隔离副本，子会话 `Set-Cookie` 变更不污染父项目 Cookie 罐。
   - Cookie 仅驻留内存会话，绝不写入 `.gorequest/` 仓库文件。
2. **RFC 6265 语义与安全防护**：
   - 区分 `HostOnly`（无 `Domain` 属性时仅精确匹配请求主机）与 Domain Cookie（子域匹配）；拒绝跨站域、单标签公共后缀（如 `Domain=com`）及非回环明文 HTTP 写入 `Secure` Cookie。
   - 按最长 `Path` 优先、最早 `CreatedAt` 次之排序发送；`Max-Age < 0` 或过期时间已过的 `Set-Cookie` 自动删除既有条目；所有 Cookie 值自动登记至 `Redactor`。

## 3. 响应大小口径、JSON 树、受限搜索与二进制安全保存

1. **传输字节（`TransferSize`）与解压后大小（`BodySize`）分离**：
   - Transport 层设 `DisableCompression: true`，在未显式指定 `Accept-Encoding` 时自动附加 `Accept-Encoding: gzip, deflate`。
   - `wireCountingReader` 精确统计底层 socket 实际读取的压缩传输字节（`TransferSize`），外层按 `Content-Encoding`（`gzip`、`x-gzip`、`deflate` zlib/raw flate）流式解压并按 `MaxResponseBytes`（默认 `32 MiB`）截断，分别记录 `TransferSize` 与解压后 `BodySize`。
2. **保序有界 JSON 树与受限搜索**：
   - `ParseJSONTree` 使用 `json.Decoder` 按 token 流解析，100% 保留服务端原始对象键顺序与大整数精度（`UseNumber`），受 `MaxJSONTreeBytes`（`2 MiB`）与 `MaxJSONTreeNodes`（`5000` 节点）双重预算保护。
   - 文本搜索与 JSON 树搜索均受 `MaxResponseSearchMatches`（`10000` 处匹配）上限保护，JSON 树搜索命中时自动展开祖先路径并支持一键复制 `JSONPath`（如 `$.a_first.token`）或节点值。
3. **二进制响应检测与无损保存**：
   - `InspectPayload` 结合 `Content-Type` 媒体类型、前 `8 KiB` NUL 字节/UTF-8 合法性判定 `IsBinary`，并对 `Content-Disposition` 文件名执行路径穿越清洗（如 `../avatar.png` → `avatar.png`）。
   - 二进制响应在 UI 中隔离渲染十六进制 + ASCII 预览与 SHA-256 摘要，保存时直接写出原始 `res.Body` 字节切片，绝不经过字符串重编码。
