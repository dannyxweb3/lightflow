# 生产环境连接记录

首次实测日期：2026-10-07；最近复测：2026-10-08。测试端为 Windows 客户端工作区。

控制面：`https://lightflow.aibusinesses.cc`。
网关域名：`lightflow-gw.aibusinesses.cc`；实际 UDP 端口、TLS SNI 和临时凭据应由控制面连接计划提供。

## 当前结果

**2026-10-08 最新候选诊断：使用全新租约取得的 Host 已更新为生产网关域名，端口 4433，但 TLS SNI 仍为 `localhost`。该诊断仅申请并读取租约，没有启动核心，不代表已建立连接。**

```json
{
  "control_base_url": "https://lightflow.aibusinesses.cc",
  "candidates": [
    {
      "host": "lightflow-gw.aibusinesses.cc",
      "port": 4433,
      "public_params": { "server_name": "localhost" }
    }
  ]
}
```

此新租约已释放，临时设备已删除。`-show-candidates` 通过显式字段投影仅输出上述公开信息，已通过凭据排除测试；不输出原始连接计划。

此前连接重试：登录、设备注册、设备签名及网关 ACK 的租约申请均成功，但当时候选网关返回 loopback 地址，报 `CANDIDATE_NOT_REMOTE_REACHABLE`。

2026-10-08 再次运行完整转发探测命令，结果仍相同：授权通过，在候选地址校验处停止；租约已释放，临时设备已删除。没有进行 QUIC 握手或 HTTPS 转发，也没有持续运行的生产连接。

生产用户账号已保存于 `.local/production/initial-account.json`，目录 ACL 限制为当前 Windows 用户，位于 Git 忽略目录中。没有输出账号、密码、设备私钥或代理凭据。失败后已释放本次租约、删除临时设备；未修改系统网络。

服务端必须将已登记网关候选改为远程可用的 `lightflow-gw.aibusinesses.cc` 和实际 UDP 端口，并同步证书对应的 TLS SNI。只修改部署环境变量不能证明数据库中的已登记节点已更新。本次没有覆盖生产候选地址，也没有关闭 TLS 校验。

此前用旧内网账号重试登录返回 `INVALID_CREDENTIALS (HTTP 401)`，生产账号已解决该问题。

以下为首次尝试记录，HTTP 502 已不再是本次登录的阻塞原因：

- 两个域名均可从 Windows 解析。
- 控制面 HTTPS 通过正常证书验证；公开 GET `/`、`/healthz`、`/v1/countries` 均返回 nginx 的 `502 Bad Gateway`。`/healthz` 只是入口诊断，未确认它是服务端正式健康检查路径。
- 使用本地普通用户账号实际调用 `POST /v1/auth/login`，同样返回 HTTP 502；尚不能判断该账号在生产环境是否有效。
- 未注册设备、未签发租约、未启动 Hysteria2 核心，未修改系统代理、路由、DNS 或防火墙。
- 对网关域名的 TCP 443 HTTPS 辅助检查出现证书名称不匹配。这不是 UDP QUIC 测试，不能据此判断 Hysteria2 证书或 UDP 服务是否正常。

## 客户端证书配置与复测

连接工具现已支持空 CA 参数，使用 Windows 系统可信根证书校验控制面；生成的 Hysteria2 配置也省略实验 CA 字段。仍保留主机名校验，不关闭 TLS 验证。

服务端入口修复后，在仓库根目录先验证协议认证与租约生命周期：

```powershell
go run ./cmd/nimbus-probe -control https://lightflow.aibusinesses.cc -ca= -account .local/production/initial-account.json -handshake-only
```

再验证真实 HTTPS 转发：

```powershell
go run ./cmd/nimbus-probe -control https://lightflow.aibusinesses.cc -ca= -account .local/production/initial-account.json
```

只申请全新租约、输出公开候选字段后清理（不启动连接）：

```powershell
go run ./cmd/nimbus-probe -control https://lightflow.aibusinesses.cc -ca= -account .local/production/initial-account.json -show-candidates
```

默认用户账号文件为 `.local/initial-account.json`，如生产账号不同，通过 `-account` 指定本地用户账号文件。不要使用管理员密钥或网关令牌。以上是短时联调工具，会在结束时释放租约和删除临时设备；桌面 UI 当前仍为开发演示，工具成功不能代表整机 VPN 已完成。

## 服务端排查交接

最新交接信息：

```text
Windows 使用生产普通用户账号已通过登录、临时 Ed25519 设备注册、
设备签名和网关 ACK 的租约申请；但候选网关 Host 为 loopback 地址，
客户端报 CANDIDATE_NOT_REMOTE_REACHABLE，尚未启动核心。
请更新控制面实际登记的 endpoint/node，使连接计划返回
lightflow-gw.aibusinesses.cc、实际 UDP 端口、正确 TLS SNI，
同时检查对应 QUIC 服务使用生产域名证书并通过公网可信证书验证。
请以真实 POST /v1/connection-sessions 返回候选为准验证更新生效。
Windows 已释放失败测试租约并删除临时设备。
```

以下信息为首次尝试的排查交接，保留作历史记录：

```text
Windows 访问 https://lightflow.aibusinesses.cc 的 TLS 校验通过，
但 GET /、GET /v1/countries 和 POST /v1/auth/login 均返回 nginx HTTP 502。
请检查实际承接该域名的 nginx error log、对应 location/proxy_pass、
control 进程监听地址/端口及 nginx 到 control 的连通性。
若使用容器，核对代理所在容器的上游地址，不将容器内 localhost 当作宿主机。
修复后请从外部入口验证登录 API 能返回应用响应，
并确认连接计划返回生产网关域名、实际 UDP 端口及正确 TLS SNI。
```

502 的具体原因尚未确认，需要服务端日志和运行状态证据。

客户端变更已通过 `go test ./...` 和 `go vet ./...`；新增测试确认使用系统根证书时拒绝未受信任的私有证书，且不会向其发送登录请求。
