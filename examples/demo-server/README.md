# demo-server

`examples/demo-server` 是一个仅用于本地调试的 HTTP 服务，配合
`examples/sample-project` 演示 GoRequest 桌面端的请求编辑、发送与响应查看。

- 只使用 Go 标准库，无第三方依赖，无生产基础设施。
- 默认监听回环地址 `127.0.0.1:4010`，可通过 `-addr` 覆盖。
- 数据全部为虚构值，不含真实密钥；用户数据只存在内存中，重启即重置。

```bash
go run ./examples/demo-server -addr 127.0.0.1:4010
go test -race ./examples/demo-server
```

## 通用约定

- 成功响应为 JSON，带 `Content-Type: application/json; charset=utf-8` 与
  `X-Demo-Server: gorequest-demo` 头。
- 错误响应统一为 `{"error":"...","detail":"..."}`，使用明确的 4xx/5xx 状态码；
  其中 `detail` 为补充说明，为空时会省略。
- 需要特定方法但用错时返回 `405`，并设置 `Allow` 头。

## 端点

### GET /health

返回 `{"status":"ok"}`，用于冒烟检查。

### POST /auth/login

请求 JSON：`{"username":"...","password":"..."}`，两者必填。
成功返回：

```json
{"data":{"accessToken":"demo-access-token-alice","tokenType":"Bearer","expiresIn":3600}}
```

`accessToken` 由用户名派生，明显为虚构值，仅用于演示。

### GET /users/me

需要 `Authorization: Bearer <token>`，缺失或为空返回 401；成功返回当前用户及设置。

### 任意方法 /echo

回显请求本身，便于检查方法、重复 Query、Headers 与 Body：

```json
{
  "method": "POST",
  "path": "/echo",
  "rawQuery": "tag=a&tag=b&q=hello%20world",
  "query": [
    {"name": "tag", "value": "a"},
    {"name": "tag", "value": "b"},
    {"name": "q", "value": "hello world"}
  ],
  "headers": {"X-Custom": ["value"], "Host": ["127.0.0.1:4010"]},
  "contentType": "application/json",
  "contentLength": 17,
  "body": "{\"hello\":\"world\"}",
  "bodyTruncated": false
}
```

`query` 保留原始顺序与重复键；请求 Body 超过 1 MiB 时截断并置 `bodyTruncated=true`。

### 用户集合（内存 CRUD）

| 方法 | 路径 | 行为 |
| --- | --- | --- |
| GET | `/users?page=&pageSize=` | 分页列表，`page>=1`，`pageSize` 1–100；返回 `data` 与 `pagination` |
| POST | `/users` | 新建，`name` 必填，`role` ∈ {admin, member, viewer}，成功 201 + `Location` |
| GET | `/users/{id}` | 详情，缺失 404，非法 id 400 |
| PUT | `/users/{id}` | 整体更新（字段同创建） |
| PATCH | `/users/{id}` | 局部更新，仅更新传入的字段 |
| DELETE | `/users/{id}` | 删除，成功 204，缺失 404 |

列表响应示例：

```json
{"data":[{"id":1,"name":"alice","email":"alice@example.com","role":"admin","createdAt":"..."}],
 "pagination":{"page":1,"pageSize":10,"total":5,"totalPages":1}}
```

内置 5 个种子用户；新增/修改/删除只改内存。并发访问由 `sync.RWMutex` 保护。

### GET /status/{code}

返回指定的 4xx/5xx 状态码，响应体为 `{"status":code,"message":"..."}`。`code` 必须
在 400–599 之间，否则返回 400。

### GET /delay?ms=N

延迟 N 毫秒后返回 `{"delayedMs":N}`。`N` 取 0–30000，缺省或空值为 500，超范围返回 400。等待期间若
客户端超时或取消（`r.Context()` 结束），服务立即停止等待，不再写入响应，便于演示
桌面端的超时与取消。

### GET /redirect?to=&status=

发出重定向。`to` 必须是本地路径（以单个 `/` 开头，拒绝 `//` 与换行），否则 400；
`status` 允许 301/302/303/307/308，默认 302。客户端默认会跟随到目标路径。

### GET /redirect/chain/{n}

`n>0` 时 302 跳到 `n-1`；`n==0` 时返回 `{"redirected":true,"remaining":0}`。
`n` 取 0–20。用于演示多跳重定向。注意 Go 默认 `http.Client` 最多跟随 10 次重定向，
因此 `n>10` 需要客户端放宽该上限才能一路跟随到 `remaining:0`。

### GET /format/{json|text|xml}

同一虚构资源以三种格式返回，`Content-Type` 分别为 `application/json`、
`text/plain`、`application/xml`。未知格式返回 404。

### GET /response/multi

返回重复的 `X-Multi` 响应头与两个 `Set-Cookie`，用于验证重复头保留与 Cookie 脱敏：

```
X-Multi: one
X-Multi: two
Set-Cookie: session=demo-session; Path=/; HttpOnly
Set-Cookie: theme=dark; Path=/
```

### GET /large?size=N

流式返回 N 字节纯文本，`N` 取 0–64 MiB，默认 1 MiB；并设置 `X-Content-Length`。
将 `size` 设为超过 core 的 32 MiB 上限，可观察响应被截断（桌面在正文外提示）。

## 限制

- 仅用于本地调试：无持久化、无鉴权后端、无并发限流、无 TLS 终止，请勿部署到生产。
- 除 `/format/text`、`/format/xml`、`/large`、`/redirect` 的跳转响应以及 `DELETE`
  成功的 204 空响应外，其余响应均为 JSON。
- 进程重启后用户数据恢复为内置种子。
