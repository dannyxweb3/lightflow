# Windows 客户端连接生产环境

本文对应控制面 `https://lightflow.aibusinesses.cc` 与 Hysteria2 网关 `lightflow-gw.aibusinesses.cc:4433/udp`。完整请求/响应和签名规则见 [API.md](API.md)。生产环境的账号和证书必须从生产服务器或受信任的发放渠道取得；内网联调目录中的账号、`ca.crt` 和地址不能用于生产。

## 客户端配置要改什么

| 项目 | 生产值或规则 |
| --- | --- |
| API base URL | `https://lightflow.aibusinesses.cc`，不要附加 `/v1`、端口 9010 或虚拟机 IP |
| API TLS | 按 Windows 系统信任链校验证书和 `lightflow.aibusinesses.cc` 主机名 |
| 登录凭据 | 使用生产账号；`initial-account.json` 只是初始账号的本地导入文件，保存在客户端受限目录，不放入代码仓库或日志 |
| 设备身份 | 在 Windows 本机生成并安全保存 Ed25519 私钥，注册时 `os=windows`；不要从 Linux 测试设备复制私钥 |
| 网关地址与 SNI | 每次使用连接计划中的 `candidates[].host`、`port`、`public_params.server_name`；当前预期分别为 `lightflow-gw.aibusinesses.cc`、`4433`、`lightflow-gw.aibusinesses.cc` |
| 网关 TLS | 验证实际证书链与计划中的 SNI；公网可信证书使用 Windows 系统信任链，不设置 `insecure`/`skip_cert_verify` |
| 网关认证 | `candidates[].credential` 是当前租约的临时 Hysteria2 认证值，仅交给本地连接核心，不能写入 UI、URL 或日志 |
| 协议 | `hysteria2` + QUIC/UDP；客户端网络须可直连网关 UDP 4433 |

若服务端仍返回 `public_params.server_name=localhost`，说明已登记节点或实际证书尚未完成生产切换。客户端不能擅自改写 SNI；应停止连接并让服务端按 [生产重部署步骤](PRODUCTION_REDEPLOY.md)修正。若使用公开可信证书，也不需要分发 Linux 内网测试用的 `.local/ca.crt`。生产 API 与网关是两个不同 TLS 入口，分别校验各自主机名。

如果 Windows 项目需要从文件导入生产初始账号，可在其项目根目录通过 PowerShell 安全复制（SSH 会交互提示认证，不要把密码写进命令）：

```powershell
New-Item -ItemType Directory -Force .\.local | Out-Null
scp -P SSH_PORT USER@SERVER_HOST:/path/to/lightflow/.local/initial-account.json .\.local\initial-account.json
scp -P SSH_PORT USER@SERVER_HOST:/path/to/lightflow/.local/signing-public-key.txt .\.local\signing-public-key.txt
```

仅在这份初始账号仍有效时使用该文件；密码修改后应改用当前生产凭据。确保 Windows 项目的 `.local/` 不被 Git 跟踪，并限制账号文件的本机访问权限。公网可信证书由 Windows 根证书库验证，无须复制生产服务器上的网关私钥或旧的内网 CA。

## 连接流程

1. 使用生产账号调用 `POST /v1/auth/login`，安全存储访问/刷新令牌。刷新令牌每次刷新后会轮换，客户端须串行刷新并原子保存。
2. 首次在 Windows 本机生成 Ed25519 密钥对，以标准 Base64 公钥调用 `POST /v1/devices`，保存返回的设备 ID。后续启动复用该身份。
3. 创建、激活、续租和释放请求按 [API.md 的五行格式](API.md#3-设备请求签名)签名：大写方法、URL 转义路径（无查询串）、Unix 秒、随机 nonce、原始请求体 SHA-256 小写十六进制；UTF-8 行间 `\n`，末尾无换行。时间偏差不得超过 ±60 秒；每次重试生成新 nonce 和签名。
4. 使用同一设备与新的 `Idempotency-Key` 调用 `POST /v1/connection-sessions`，声明 `protocols:["hysteria2"]`。收到 202 时保留原幂等键和原请求体，按 `Retry-After` 重试直到 200；202 的 pending 响应没有可拨号凭据。
5. 以 200 响应的候选地址、端口、认证值、SNI 建立真正的 Hysteria2 QUIC/UDP 连接。连接成功后调用 `POST /v1/connection-sessions/{id}/activate`。TCP 探测成功不能代替 UDP 握手。
6. 按响应的 `renew_after_seconds` 续租，续租后的计划和截止时间以新响应为准。断开时 `DELETE /v1/connection-sessions/{id}`；重连优先复用尚未到期的租约，失效后再申请新租约。

生产服务器更新节点地址或 SNI 会撤销该节点的现有租约；遇到 `SESSION_CLOSED` 时应清理旧本地连接并重新申请。不要硬编码旧候选地址、旧 SNI、租约 ID 或认证值。

## 验收

在 Windows 上确认 `https://lightflow.aibusinesses.cc/readyz` 的证书有效且返回 ready。新租约的候选应返回网关公网域名、UDP 4433 和同名 SNI；随后由客户端核心完成真实 QUIC 握手、激活、续租和释放。若 API 可用但 QUIC 不通，检查 DNS 是否为“仅 DNS”、防火墙 UDP 4433、网关实际证书与 SNI，以及网关健康状态。

如客户端验证签名策略或更新元数据，还须从生产环境取得 `.local/signing-public-key.txt` 中的**公钥**并预置在客户端；不能沿用测试环境公钥。生产环境没有要求客户端导入服务端私钥、管理员密钥或网关令牌。
