# 示例服务

这是 GoRequest 的最小示例项目，用本地 `demo-server` 演示请求编辑与执行。

## 运行

```bash
# 终端 1：启动本地演示服务
go run ./examples/demo-server -addr 127.0.0.1:4010

# 终端 2：校验项目
./bin/gorequest validate --project examples/sample-project

# 无需密钥即可运行的请求
./bin/gorequest run --project examples/sample-project --request users.health --env dev

# 登录（需要注入密码密钥）
GOREQUEST_SECRET_LOGINPASSWORD=pass123 \
  ./bin/gorequest run --project examples/sample-project --request auth.login --env dev \
  --secret loginPassword=env:GOREQUEST_SECRET_LOGINPASSWORD

# 需要 Bearer 令牌的请求
GOREQUEST_SECRET_APITOKEN=demo-token-value \
  ./bin/gorequest run --project examples/sample-project --request users.me --env dev
```

密钥值只用 `{{secret.name}}` 引用，不写入任何项目文件。
