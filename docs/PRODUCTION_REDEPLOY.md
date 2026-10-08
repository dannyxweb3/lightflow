# Lightflow 生产服务端重部署

本流程用于已有数据的 `/path/to/lightflow` 部署。控制面、数据库和网关仍由 `compose.yaml` 管理；Nginx/Cloudflare 在应用外层处理公开 HTTPS。不要在已有部署重新运行 `scripts/init.py`，也不要使用 `docker compose down -v`：两者分别会冲突现有密钥和删除数据库卷。

## 目标状态

| 入口 | 地址 | 说明 |
| --- | --- | --- |
| 客户端 API | `https://lightflow.aibusinesses.cc` | Nginx 终止 HTTPS，仅代理 `/v1/*` 和 `/readyz` 到 `127.0.0.1:9010` |
| Hysteria2 网关 | `lightflow-gw.aibusinesses.cc:4433/udp` | Cloudflare DNS 设为“仅 DNS”，客户端直连；网关证书 SAN 与 SNI 均为该域名 |
| 管理后台 | 受控 HTTPS 管理入口 `/console/` | 不在公开 API 主机名上转发 `/console/*`、`/admin/*`、`/metrics` 或 `/internal/*` |
| 数据库和内部控制 | `postgres:9011`、`control:9012` | 仅容器网络；不开放公网端口 |

Cloudflare 的普通 HTTP 代理不转发此网关的 UDP 服务，所以网关记录须使用“仅 DNS”；如使用 Cloudflare 代理公开 API，源站 Nginx 应使用有效证书并设置 Full (strict)。[Cloudflare 代理限制](https://developers.cloudflare.com/dns/proxy-status/limitations/)；[Full (strict) 要求](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/)。网关客户端直连，不能用仅供 Cloudflare 验证源站的 Origin CA 证书代替客户端可信的证书。

## 重部署前检查和备份

在生产服务器上以有 Docker 权限的用户执行；以下假定已有配置位于 `/path/to/lightflow`：

```bash
cd /path/to/lightflow
pwd
git status --short --branch
docker compose config --quiet
docker compose ps
umask 077
mkdir -p .local/backups
docker compose exec -T postgres pg_dump -U nimbus -d nimbus -Fc > .local/backups/nimbus-before-redeploy.dump
tar -czf .local/backups/config-before-redeploy.tgz .env .local/certs .local/signing-public-key.txt
```

备份包包含私钥和管理员凭据，只保留在受控位置，并另做加密离机备份。记录当前提交 `git rev-parse HEAD`，确认数据库备份非空。现有 `.env`、`.local`、Compose 卷及管理员账号均应保留；代码更新不会自动改已登记节点的 `host` 或 `server_name`。

生产环境的账号密码和 `.env` 曾被复制到聊天中，应安排凭据轮换。`POSTGRES_PASSWORD` 需要与数据库角色密码协调修改；`CREDENTIAL_KEY` 和 `SIGNING_KEY` 不能仅改环境变量，否则会影响租约验证或客户端信任。先完成连通性恢复，再按维护窗口制定这些密钥的轮换步骤。

## 更新代码和控制面

```bash
cd /path/to/lightflow
git pull --ff-only origin main
docker compose config --quiet
docker compose build control gateway
docker compose up -d --wait postgres control
curl --fail http://127.0.0.1:9010/readyz
```

已有数据库不要重新运行 `scripts/bootstrap.py` 来修正节点地址；该脚本不会更新已有节点的 `host`。若 Nginx 位于同机，确认其 API 上游是 `127.0.0.1:9010`，而不是旧端口。对外只允许用户 API 和健康检查；管理后台通过独立受控入口访问。应用进程本身不处理 HTTPS。

## 给网关安装公网域名证书

先在 Cloudflare 检查 `lightflow-gw.aibusinesses.cc` 的 A/AAAA 记录指向网关公网地址且为“仅 DNS”，放行到网关的 UDP 4433。**优先复用你在 Nginx 层已有的公网可信证书**，前提是证书 SAN 覆盖 `lightflow-gw.aibusinesses.cc`（或有效通配符），并且能取得对应私钥和完整证书链。只覆盖 API 域名 `lightflow.aibusinesses.cc` 的证书不能直接用于网关；Cloudflare Origin CA 证书也不能被直连的 Windows 客户端默认信任。

假设现有 Nginx 证书和私钥分别位于下面两条路径，把它们替换为实际路径后执行：

```bash
cd /path/to/lightflow
scripts/deploy_gateway_certificate.sh /path/to/nginx/fullchain.pem /path/to/nginx/privkey.pem
```

脚本内部使用 `openssl x509 -checkhost` **读取并检查**证书覆盖的域名，不会签发新证书；还检查证书与私钥匹配。然后将证书链和私钥复制到 Compose 挂载路径，强制重建并等待 `gateway` 健康。Nginx 现有证书保持原位。Nginx 上已经安装证书并不等于 gateway 容器也在使用它，两处服务必须分别读取自己的证书文件。

如果现有证书不覆盖网关域名，才需要为网关另行签发。若生产 Nginx 可处理该域名的 HTTP-01 验证，可使用 [Certbot 官方说明](https://certbot.eff.org/instructions?os=ubuntubionic&tab=standard&ws=nginx)安装插件，再执行：

```bash
certbot certonly --nginx --cert-name lightflow-gw.aibusinesses.cc -d lightflow-gw.aibusinesses.cc
scripts/deploy_gateway_certificate.sh /etc/letsencrypt/live/lightflow-gw.aibusinesses.cc
```

无论复用还是新签发，只要网关证书由 Certbot 管理，就设置续期后的重新部署钩子，并验证续期配置：

```bash
ln -sfn /path/to/lightflow/scripts/deploy_gateway_certificate.sh \
  /etc/letsencrypt/renewal-hooks/deploy/lightflow-gateway.sh
certbot renew --dry-run
```

钩子只处理包含网关域名的成功续期；手动执行一次上面的部署脚本已验证证书复制和容器重建。Certbot `--deploy-hook` / deploy-hook 目录仅在成功签发或续期后运行；`--dry-run` 主要验证续期挑战，不代替网关实际握手验证。[Certbot 续期钩子说明](https://eff-certbot.readthedocs.io/en/stable/using.html#renewal)。如果 Nginx 证书由其他工具续期，应在那个工具的续期成功钩子里重新调用 `scripts/deploy_gateway_certificate.sh 证书链路径 私钥路径`，避免网关继续使用过期的复制件。

## 更新已登记节点

确认新网关容器健康后，运行：

```bash
cd /path/to/lightflow
python3 scripts/set_gateway_address.py --host lightflow-gw.aibusinesses.cc --server-name lightflow-gw.aibusinesses.cc --port 4433
```

脚本从本机 `.env` 读取管理密钥，不打印密钥；先检查已安装证书的 SAN，再用管理 API 更新数据库中的 `host`、`server_name`、`port`，同步 `.env`，等待网关重新就绪。同值重跑不会再次提交 PATCH。地址或 SNI 变更会撤销该节点的现有租约，客户端须重新申请。只改 `.env`、只重启容器或重跑 `bootstrap.py` 都不会修复已有数据库记录。

## 验证与回滚边界

```bash
docker compose ps
curl --fail http://127.0.0.1:9010/readyz
curl --fail https://lightflow.aibusinesses.cc/readyz
openssl x509 -in .local/certs/gateway.crt -noout -checkhost lightflow-gw.aibusinesses.cc
```

确认新租约返回 `host=lightflow-gw.aibusinesses.cc`、`port=4433`、`public_params.server_name=lightflow-gw.aibusinesses.cc`，再用 Windows 客户端完成真实 Hysteria2 QUIC/UDP 握手、激活和续租。`Test-NetConnection` 只测 TCP，不能证明 UDP 4433 可用。检查公开 API 主机名上的 `/admin/*`、`/console/*`、`/internal/*` 和 `/metrics` 被边缘层拒绝。

客户端配置和完整验收流程见 [Windows 客户端连接生产环境](CLIENT_PRODUCTION.md)。

若证书部署失败，不要先更新 SNI；修正证书或恢复备份的证书/私钥并重建网关。若更新节点后无法握手，先核对实际运行证书、SNI、DNS 和 UDP 防火墙，再回退对应变更。代码回退前检查数据库迁移是否与旧版兼容；必要时使用重部署前的数据库和密钥备份恢复，不要删除现有卷尝试“重装”。
