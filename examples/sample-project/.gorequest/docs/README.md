# 示例服务与请求样例

这是 GoRequest 的本地示例项目，配合 `examples/demo-server` 演示桌面内的请求编辑、
发送与响应查看。所有数据均为虚构，仅用于本地联调，不含真实密钥。

## 运行

```bash
# 终端 1：启动本地演示服务（默认只监听 127.0.0.1:4010）
go run ./examples/demo-server -addr 127.0.0.1:4010

# 终端 2：构建并打开桌面应用（唯一产品入口，无 CLI）
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project
```

服务默认绑定回环地址，使用 Go 标准库，仅供本机调试。

## 环境与密钥

`environments/dev.yaml` 提供两个普通变量与两个密钥名称：

- 变量：`baseUrl`（默认 `http://127.0.0.1:4010`）、`username`（默认 `alice`）。
- 密钥：`loginPassword`、`apiToken`。真实值不写入项目文件，需在桌面本地凭据或
  环境变量中绑定：
  - `GOREQUEST_SECRET_LOGINPASSWORD`
  - `GOREQUEST_SECRET_APITOKEN`

请求示例只用 `{{baseUrl}}`、`{{username}}`、`{{secret.loginPassword}}` 等现有变量。
`auth.login` 登录成功后返回 `data.accessToken`（服务端生成的虚构令牌），可作为
Bearer 模板值的参考来源；`users.me` 使用 `auth/bearer.auth.yaml` 的
`{{secret.apiToken}}`。

## 端点一览

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/health` | 健康检查，返回 `{"status":"ok"}` |
| POST | `/auth/login` | JSON 登录，返回 `data.accessToken`（虚构） |
| GET | `/users/me` | 需 `Authorization: Bearer <token>`，返回当前用户 |
| 任意 | `/echo` | 回显方法、有序 Query（保留重复键）、Headers、Body |
| GET | `/users` | 分页列表：`?page=`、`?pageSize=`（1–100） |
| POST | `/users` | 新建用户，返回 201 与 `Location` |
| GET | `/users/{id}` | 用户详情，缺失返回 404 |
| PUT | `/users/{id}` | 整体更新 |
| PATCH | `/users/{id}` | 局部更新（仅传入字段） |
| DELETE | `/users/{id}` | 删除，成功返回 204 |
| GET | `/status/{code}` | 返回指定 4xx/5xx 状态码与 JSON 错误体 |
| GET | `/delay?ms=N` | 延迟 N 毫秒（0–30000），支持超时/取消 |
| GET | `/redirect?to=&status=` | 同源重定向（301/302/303/307/308） |
| GET | `/redirect/chain/{n}` | 多跳重定向，`n=0` 时返回 200 |
| GET | `/format/{json\|text\|xml}` | 同一资源的不同响应格式 |
| GET | `/response/multi` | 重复响应头与两个 `Set-Cookie` |
| GET | `/large?size=N` | 流式返回 N 字节（默认 1 MiB，上限 64 MiB） |

## 典型操作

### 请求回显（`echo.inspect`）

`POST /echo` 携带重复的 `tag` 查询参数、自定义 `X-Demo` 头与 JSON Body。响应中的
`query` 数组保留先后顺序与重复键，`headers` 保留同名多值，`body` 为收到的原文。
适合验证 Query/Header 的有序与重复处理，以及 JSON Body 提交。

### 用户管理（`users/*`）

- `users.list`：`GET /users?page=1&pageSize=2` 查看分页与 `pagination` 元数据。
- `users.detail`：`GET /users/1` 查看单个用户。
- `users.create`：`POST /users` 提交 `name`（必填）、`email`、`role`。
- `users.update`：`PUT /users/1` 整体更新。
- `users.delete`：`DELETE /users/5` 删除；成功后再次访问返回 404。

服务启动时内置 5 个虚构用户，所有新增/修改/删除只作用于内存，重启即恢复初始数据。
`users.delete` 具破坏性，删除后列表与详情结果会变化。

### 错误与边界（`misc/*`）

- `misc.status`：`GET /status/404` 演示非 2xx 响应的展示。
- `misc.delay`：`GET /delay?ms=300`（请求超时 2s）演示正常延迟。
- `misc.timeout`：`GET /delay?ms=3000`（请求超时 500ms）演示超时错误与取消路径。

分页、状态码、延迟等参数非法时，服务返回 400 并给出结构化错误体。

### 重定向（`misc.redirect`）

`GET /redirect?to=/users&status=302`。客户端默认跟随重定向，最终落到 `/users` 列表。
`/redirect/chain/2` 演示多跳后成功返回。

### 响应格式与特性（`responses/*`）

- `responses.json` / `responses.text` / `responses.xml`：同一资源分别以 JSON、纯文本、
  XML 返回，`Content-Type` 不同。
- `responses.multiheader`：响应含重复的 `X-Multi` 头与两个 `Set-Cookie`，可在响应
  头表格中看到重复项，`Set-Cookie` 值在桌面内脱敏显示。
- `responses.large`：`GET /large?size=1048576` 返回 1 MiB 文本。将 `size` 调到超过
  32 MiB 可触发 core 的响应截断标记（桌面在正文外提示截断）。

## 与 core 已支持能力的对应关系

示例只使用当前 core 已实现的能力：有序且可重复的 Query/Headers、JSON/Text/XML Body、
None/Bearer/Basic/API Key 鉴权、请求级或默认超时、`context` 取消、跟随重定向、
Pretty/Raw 响应正文、重复响应头保留与 `Set-Cookie` 脱敏、32 MiB 响应截断。

> 说明：请求文件中的 `assert` 字段目前仅随请求保存，执行时**不会**进行断言求值
> （core 尚未实现断言引擎），因此新增样例未使用 `assert`，以免误导。

密钥值只用 `{{secret.name}}` 引用，不写入任何项目文件。GoRequest 不提供独立命令行程序。
