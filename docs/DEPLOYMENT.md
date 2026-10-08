# Docker Compose 部署

项目对外名称为 **Lightflow**。现有 Compose 项目名、PostgreSQL 用户/库名、Go 模块路径和内部协议标识仍沿用 `nimbus`，以便已有部署在升级时继续使用原数据库卷、账号和租约密钥；不要仅为改名删除或重建这些资源。

生产部署应分别配置客户端 HTTPS API 域名和客户端直连的 Hysteria2 网关域名。前者由 Cloudflare/Nginx 代理，后者在 Cloudflare 设置为“仅 DNS”并直达网关 UDP 端口；网关域名也应作为 TLS SNI。管理后台若需远程访问，应使用独立的受控 HTTPS 入口，限制访问身份和来源，只代理 `/console/*`；公开 API 入口不代理管理路径。实际生产地址、证书和数据库中的网关候选地址由部署环境配置，不能沿用内网联调地址或 `localhost`。

## 服务和端口

| 服务 | 用途 | 监听与端口映射 |
| --- | --- | --- |
| postgres | 业务数据、租约和配额 | 容器网络 `postgres:9011/tcp`，不映射宿主机端口 |
| control | HTTP API、管理接口 | `127.0.0.1:9010/tcp` |
| control 内部监听 | 网关授权快照和 ACK，HTTP + 网关令牌 | 默认不暴露；容器网络 `control:9012/tcp` |
| gateway | Agent 监管的 Hysteria2 网关 | `4433/udp` |

网关容器内的认证回调和统计接口分别监听 `127.0.0.1:9013`、`127.0.0.1:9014`。上述 TCP 服务端口均位于 9010–9020 区间。客户端协议入口仍使用 `4433/udp`，它不属于管理 HTTP 服务端口。

Compose 中没有 HTTPS 终止服务。应用进程仅监听 HTTP。以后由现有 Nginx、Cloudflare Tunnel 或其他边缘层终止公开 API 的 HTTPS，并转发到 `127.0.0.1:9010`。边缘层只放行 `/v1/*`、`/healthz`、`/readyz`；`/admin/*`、`/metrics` 和 `/internal/*` 不应公开。

管理后台位于 `/console/`，同样只走应用 HTTP。公开 API 入口**不要**转发 `/console/*`；通过 SSH 隧道或独立的受控管理入口访问，具体功能见 [管理后台功能文档](ADMIN_CONSOLE.md)。生产外部 HTTPS 由 Nginx/Cloudflare 终止，`ADMIN_COOKIE_SECURE=true`。代理需保留原始 `Host`，以便后台校验写请求来源。

网关的 QUIC 协议仍使用 TLS 证书，这是 VPN 协议握手所需，与 API HTTPS 终止无关。网关认证回调和统计接口只监听容器内 `127.0.0.1`。

## 首次启动

需要 Docker Engine、Compose v2+、Python 3 和 OpenSSL。仓库根目录执行：

```bash
python3 scripts/init.py
docker compose config --quiet
docker compose up -d --build postgres control
python3 scripts/bootstrap.py
docker compose up -d --build gateway
curl --fail http://127.0.0.1:9010/readyz
```

初始化生成 `.env`、网关证书和私有 CA、策略签名公钥。随机网关令牌写入 `.env`，数据库仅存其 SHA-256 摘要；每个网关应使用不同令牌。所有私钥和 `.env` 均已被 Git 忽略。已有 `.env` 和 `.local` 时 `init.py` 拒绝覆盖，直接使用现有密钥。

管理后台初始密码保存在 `.local/admin-console.json`，访问地址为 `http://127.0.0.1:9010/console/`。生产环境保持 `ADMIN_COOKIE_SECURE=true`，通过外部 HTTPS 管理入口访问；本机纯 HTTP 调试若浏览器不接受 Secure Cookie，可仅在本机测试环境将 `.env` 中 `ADMIN_COOKIE_SECURE=false` 并重建 control 容器。不要把后台和 `/admin/*` 放进公开 API 代理规则。

`bootstrap.py` 注册国家和网关，创建 30 天、5 设备、2 并发的首个账号。密码仅写入 `.local/initial-account.json`。重复执行不会更改已有账号密码或网关令牌。默认 `GATEWAY_HOST=localhost` 和私有 CA 证书只适合本机测试。向真实用户分发前，替换网关证书为客户端信任的证书链，或为客户端安全预置私有 CA。

必须先注册网关，再启动 `gateway`。如果先对全部服务执行 `docker compose up`，尚未登记的 Agent 会因 `gateway control authentication rejected` 而反复重启；执行 bootstrap 后会自动恢复。已登记节点出现同样日志时，可运行 `python3 scripts/bootstrap.py --rotate-gateway-token`，使数据库令牌与当前 `.env` 一致；该操作会撤销该节点的现有租约。

数据库数据使用 Docker 卷持久化。普通停止和升级不要使用 `docker compose down -v`，它会删除数据库卷。

## 已有配置升级

从旧端口升级时，先改写现有 `.env` 中的 `API_PORT=8080`，再重建使用新端口的服务：

```bash
python3 scripts/upgrade_ports.py
docker compose up -d --build --wait postgres control
docker compose up -d --build --wait gateway
curl --fail http://127.0.0.1:9010/readyz
```

PostgreSQL 在原数据卷上改为监听容器内的 9011，不创建新库，也不映射宿主机端口。控制面内部接口改为 9012；同机 Agent 会随 Compose 更新其 `CONTROL_URL`。如有独立网关，须将其 `CONTROL_URL` 和私网映射改为 9012 后再启动。同步将生产 Nginx 的上游改为 `127.0.0.1:9010`；先前内网联调使用 `scripts/lan_https.py` 启动的 Nginx，还须以相同 `--ip` 参数重新运行脚本来加载新上游。不要使用 `docker compose down -v`。

已有部署增加管理后台密码：

```bash
python3 scripts/init_admin.py
docker compose up -d --build control
```

脚本只在缺少密码哈希时写入 `.env`，并把初始密码保存到 `.local/admin-console.json`。再次执行不会改变密码；需轮换时先通过受控维护流程更新哈希并使原有 `admin_sessions` 失效。

如果 `.env` 是早期带内置代理和内部证书配置的版本，先执行：

```bash
python3 scripts/upgrade_local.py
docker compose down --remove-orphans
docker compose up -d --build postgres control
python3 scripts/bootstrap.py --rotate-gateway-token
docker compose up -d --build gateway
```

脚本仅在缺少 `GATEWAY_AUTH_TOKEN` 时向现有 `.env` 添加随机令牌，不覆盖数据库密钥。`down --remove-orphans` 会移除旧代理容器，保留数据库卷，但连接会中断。`--rotate-gateway-token` 将令牌摘要写入已存在的网关记录，并撤销旧租约；最后重建网关容器以读取新令牌。旧 `API_DOMAIN`、内部证书配置项和未使用的证书文件可以在确认新部署正常后手动清理。

## 公开 API 和多地区网关

公开 API 默认只绑定宿主机回环地址。将来的 Nginx/Cloudflare 层负责公网证书、HTTPS 和源站访问限制。**不要将 9010 直接发布到公网**：应用只提供 HTTP，且管理接口与用户接口共用该监听端口。反向代理须限制路径，管理操作通过服务器本机或 SSH 隧道进行。

同机网关通过 Compose 私有网络访问 `http://control:9012`。如果网关独立部署，先建立 WireGuard、IPsec 或其他加密私网，并把控制接口仅绑定在该私网地址：

```bash
# .env 里先配置 INTERNAL_BIND=<私网IP>
docker compose -f compose.yaml -f deploy/compose.private-control.yaml up -d --build
```

私网网关机器使用 [独立网关 Compose](../deploy/compose.gateway.yaml)，并提供 `.gateway.env`：

```dotenv
CONTROL_URL=http://control.private.example:9012
GATEWAY_ID=gateway-jp-1
GATEWAY_AUTH_TOKEN=<仅这个网关使用的随机高强度令牌>
GATEWAY_PORT=4433
LOCAL_UID=1000
LOCAL_GID=1000
```

为每个新网关调用 `POST /admin/endpoints`，将 `auth_token` 与该网关环境变量设为相同值。`auth_token` 只在管理请求中传递一次，不会从查询接口返回。独立网关仅需自己的公网协议证书/私钥，不需要数据库、管理员、内部 CA 或控制面凭据派生密钥。运行方式：

```bash
docker compose --project-directory . --env-file .gateway.env \
  -f deploy/compose.gateway.yaml up -d --build
```

控制面与远程网关之间的 HTTP 必须位于加密私网内；不要在公网直接开放内部 9012。若以后由 Nginx 等独立边缘层为这个接口终止 HTTPS，Agent 的 `CONTROL_URL` 可改为对应 `https://` 地址，应用服务仍无需监听 HTTPS。内部接口必须在边缘层限制网关来源，且不与公开用户 API 共用公开路由。

## 运维与配置

| 变量 | 含义 |
| --- | --- |
| `POSTGRES_PASSWORD` | 数据库初始密码；改环境变量不会修改已有数据库角色密码 |
| `ADMIN_KEY` | 本机管理 API Bearer 凭据，至少 32 字符 |
| `CREDENTIAL_KEY` | 租约凭据派生主密钥，32 字节 Base64，必须备份 |
| `SIGNING_KEY` | 策略和更新元数据签名密钥，32 字节 Base64 |
| `GATEWAY_AUTH_TOKEN` | 当前网关独立授权同步令牌，不与用户令牌混用 |
| `LEASE_SECONDS` | 租约长度，默认 600，允许 10–3600 |
| `GATEWAY_HOST` / `GATEWAY_PORT` | 返回给客户端的协议入口地址和 UDP 端口 |
| `API_PORT` | 回环地址上的应用 HTTP 端口，默认 9010；旧 `.env` 运行 `scripts/upgrade_ports.py` 更新 |
| `LOCAL_UID` / `LOCAL_GID` | 读取私有网关证书的容器用户 |

单个网关令牌需要更换时，生成新值，通过 `PATCH /admin/endpoints/{id}` 提交 `auth_token`。控制面会将网关标为未就绪并撤销该网关租约；用相同新令牌重建 Agent。旧 Agent 在下次同步收到 401 后会关闭自身及 Hysteria 子进程。不要在正常重试 `bootstrap.py` 时轮换令牌。

数据库保存 `CREDENTIAL_KEY` 指纹，意外更换主密钥后拒绝启动。计划轮换需停机、撤销所有旧租约并重建网关；操作前备份。签名密钥轮换需先让客户端信任新公钥，目前没有自动公钥轮换协议。

备份数据库：

```bash
mkdir -p .local/backups
chmod 700 .local/backups
docker compose exec -T postgres pg_dump -U nimbus -d nimbus -Fc > .local/backups/nimbus.dump
chmod 600 .local/backups/nimbus.dump
```

还需加密备份 `.env`、网关证书私钥、签名公钥及 CA 私钥。恢复到空数据库后再启动应用。迁移在 PostgreSQL advisory lock 下运行；已应用迁移的 checksum 改变会使控制面拒绝启动。升级前备份并保留上一版镜像。当前默认单控制面和单数据库，没有数据库高可用。

检查 `GET /readyz`、授权 `GET /metrics`、`GET /admin/endpoints` 的 `ready` 和 `last_seen_at`。新租约只使用最近 10 秒有健康 ACK 的网关。创建返回 202 时，用原 Idempotency-Key 和新的设备签名 nonce 重试。控制面暂时失联时 Agent 只继续服务到现有租约截止，不允许离线续期。

## 验证边界

`docker compose config --quiet`、独立网关 Compose 静态配置和本机真实 PostgreSQL/Hysteria2 集成测试已通过。当前开发主机无法访问 Docker socket，因此没有在本机完成镜像构建和 Compose 启动；CI 工作流包含这两项验证。
