# Nimbus VPN 服务端

Go 控制面 + PostgreSQL + 受监管的 Hysteria2 网关。提供不内置 HTTPS 终止的 Docker Compose 部署、设备签名、临时租约、服务端强制断连和可执行集成测试。

这是首个可运行的服务端交付，**不是整个 PRD 的 V1 完成声明**。真实接入的协议为 Hysteria2；VLESS/Reality、VMess、Trojan、Snell、客户端及多协议自动回退尚未实现。服务端不会把未实现的协议返回给客户端。

## 快速部署

需要 Docker Engine、Docker Compose v2+、Python 3、OpenSSL。以下命令在仓库根目录执行：

```bash
# 仅首次执行；已有 .env / .local 时跳过，脚本不会覆盖密钥。
python3 scripts/init.py

docker compose config --quiet
docker compose up -d --build
python3 scripts/bootstrap.py
curl --fail http://127.0.0.1:8080/readyz
```

初始化账户保存在 `.local/initial-account.json`，管理员密钥保存在 `.env`；不提供通用默认密码。API 默认只监听宿主机回环地址，网关开放 UDP 4433。应用 API 只提供 HTTP，默认网关证书由私有 CA 签发，测试客户端必须信任 `.local/certs/ca.crt`，不要关闭证书校验。

管理后台位于 `http://127.0.0.1:8080/console/`，初始密码在 `.local/admin-console.json`。已有 `.env` 的部署先运行 `python3 scripts/init_admin.py` 并重建 control。后台只适合本机、SSH 隧道或受控管理入口；外部 HTTPS 仍由 Nginx/Cloudflare 处理。

- [部署、外层 HTTPS、备份和升级](docs/DEPLOYMENT.md)
- [API、设备签名与客户端接入](docs/API.md)
- [管理后台功能文档](docs/ADMIN_CONSOLE.md)
- [实现范围、设计取舍和验收](docs/IMPLEMENTATION.md)
- [OpenAPI 契约](docs/openapi.json)
- [原始需求](REQUIREMENT.md) / [总体设计](TECHNICAL_DESIGN.md)

## 已实现

- 邮箱密码登录、短期访问令牌、刷新令牌轮换和重放撤销、全账户登出。
- 订阅、套餐设备/并发限制；管理员变更订阅时撤销旧租约。
- Ed25519 设备身份，连接接口请求签名与防重放。
- 国家及网关管理，健康过滤、按容量占用选择网关、手选国家约束。
- PostgreSQL 事务配额、幂等连接创建、网关 ACK 后下发凭据、续租和释放。
- 网关令牌身份、完整授权快照同步、Hysteria2 HTTP 认证及现存连接踢除。
- Agent 本地单调时钟执行到期；无法监管时结束网关进程。
- 签名策略/更新元数据发布、基础指标、数据库版本迁移、定期数据清理。
- 单管理员管理后台：概览、用户和订阅、国家、节点；数据库会话与 CSRF 保护。

## 开发和验证

Go 1.26+，PostgreSQL 16+：

```bash
go build ./cmd/nimbus
go vet ./...
go test -race ./...

# 使用专门测试数据库；每个用例在独立 schema 中运行并清理。
TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/testdb?sslmode=disable' \
  go test -race -count=1 ./...

# 增加真实 Hysteria2 连接、撤销与离线过期测试。
TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/testdb?sslmode=disable' \
TEST_HYSTERIA_BIN='/absolute/path/to/hysteria' \
  go test -race -count=1 ./...
```

未设置集成测试环境变量时，相关用例会明确 `SKIP`，不代表已验证数据库或真实网关。
