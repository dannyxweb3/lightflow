# Windows 宿主机内网联调资料

本方案用于 VMware Linux 虚拟机与 Windows 宿主机之间的联调，不是公网生产部署。应用仍只处理 HTTP；Linux 上独立的 Nginx 在 `192.168.194.128:8443` 终止 HTTPS，仅代理 `/v1/*` 和 `/readyz`。它不会代理 `/admin/*`、`/internal/*` 或管理后台。

## 当前地址与信任

| 用途 | 地址或文件 |
| --- | --- |
| 控制面 HTTPS | `https://192.168.194.128:8443` |
| 网关候选地址 | 完成下方节点更新后为 `192.168.194.128:4433/udp` |
| 网关 TLS SNI | `localhost`；证书的 SAN 含 `localhost`，拨号地址仍是虚拟机内网 IP |
| API 和网关 CA | `.local/ca.crt`，仅分发公有证书，不分发 `.local/certs/ca.key` |
| 用户初始账户 | `.local/initial-account.json`，仅供客户端本地读取，不提交或打印密码 |
| API 规则 | `.local/API.md`，包含 Ed25519 设备请求签名、幂等重试和租约生命周期 |

CA 签发的 API 证书 SAN 包含 `192.168.194.128`；网关证书 SAN 包含 `localhost`。客户端调用 API 时用 CA 验证 **IP 地址**，拨号 Hysteria2 时用同一 CA 验证 **SNI `localhost`**。不能关闭 TLS 验证。API 控制面端口是 TCP 8443；网关是 UDP 4433，不是 TCP。

## Linux 虚拟机配置

在部署目录执行：

```bash
python3 scripts/lan_https.py --ip 192.168.194.128
curl --cacert .local/ca.crt https://192.168.194.128:8443/readyz
```

此命令启动独立的非特权 Nginx 进程。虚拟机重启后须重新执行；虚拟机 DHCP 地址变化时也须重新执行，以便证书 SAN 与新 IP 一致。需要允许 Windows 宿主机访问虚拟机的 TCP 8443、UDP 4433。不要把 Docker 内部 8443、HTTP 8080、`/admin/*` 暴露给 Windows。

已初始化的节点最初登记为 `localhost`，仅改 `.env` 不会更新数据库。更新服务端后执行：

```bash
sudo docker compose up -d --build control
python3 scripts/lan_endpoint.py --ip 192.168.194.128
```

脚本调用受保护的管理 API，把节点 `host` 改成虚拟机 IP、保留与现有网关证书匹配的 `server_name=localhost`，并更新 `.env` 供以后重新初始化使用。地址变更会撤销该节点的旧租约；脚本确认网关重新就绪后打印不含秘密的候选地址。再次运行普通 `bootstrap.py` 不会改变已存在节点的密码或地址。

如果刚重建 `control` 时脚本提示连接关闭，等待控制面健康后重新运行同一命令。脚本会先读取节点现状；地址已经更新时不会再次提交修改或撤销租约。

## 将联调文件复制到 Windows 项目

Linux 部署目录的 `.local/` 已准备好 `API.md`、`initial-account.json` 和 `ca.crt`。在 **Windows 客户端项目根目录**运行 PowerShell：

```powershell
New-Item -ItemType Directory -Force .local | Out-Null
scp w@192.168.194.128:/home/w/work/codex-lightflow/.local/API.md .\.local\API.md
scp w@192.168.194.128:/home/w/work/codex-lightflow/.local/initial-account.json .\.local\initial-account.json
scp w@192.168.194.128:/home/w/work/codex-lightflow/.local/ca.crt .\.local\ca.crt
```

Windows 项目应忽略 `.local/`。账号文件包含用户密码；只在本机读取，不粘贴到聊天、日志或代码仓库。管理员密钥和 CA 私钥均不需要复制。

## 验证

Windows PowerShell 中先验证 API：

```powershell
Test-NetConnection 192.168.194.128 -Port 8443
curl.exe --cacert .\.local\ca.crt https://192.168.194.128:8443/readyz
```

预期为 TCP 连通，`/readyz` 返回 `{"status":"ready"}`。再由客户端按 `API.md` 登录、注册设备并申请连接；响应的 `candidates[0]` 应包含 `host=192.168.194.128`、`port=4433`、`protocol=hysteria2`、`public_params.server_name=localhost`。在 Windows 侧使用返回的**临时** `credential`、拨号 IP、SNI 和 CA 建立 Hysteria2 连接，才能实际验证 UDP 4433。`Test-NetConnection` 只能验证 TCP，不能证明 UDP 网关可用。客户端应按 `renew_after_seconds` 续租，退出时释放租约。

Linux 侧验证不能代替 Windows 实测。若 Windows 无法连接，先检查 VMware 网络模式、宿主机到 `192.168.194.128` 的路由与防火墙，再检查 TCP 8443 和 UDP 4433 放行情况。
