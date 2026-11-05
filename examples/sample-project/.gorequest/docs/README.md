# 示例服务

这是 GoRequest 的最小示例项目，用本地 `demo-server` 演示桌面内的请求编辑与执行。

## 运行

```bash
# 终端 1：启动本地演示服务
go run ./examples/demo-server -addr 127.0.0.1:4010

# 终端 2：构建并打开桌面应用（唯一产品入口，无 CLI）
go build -o bin/gorequest-desktop ./cmd/gorequest-desktop
./bin/gorequest-desktop -project examples/sample-project
```

在桌面应用中：

- `users.health` 无需密钥，可直接发送。
- `auth.login` 使用 `{{secret.loginPassword}}`，需在桌面本地凭据或环境变量
  `GOREQUEST_SECRET_LOGINPASSWORD` 中绑定密码密钥后再发送。
- `users.me` 使用 Bearer（`auth/bearer.auth.yaml` 的 `{{secret.apiToken}}`），
  需绑定 `GOREQUEST_SECRET_APITOKEN`。

密钥值只用 `{{secret.name}}` 引用，不写入任何项目文件。GoRequest 不提供独立命令行程序。
