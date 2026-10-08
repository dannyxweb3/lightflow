# Windows ↔ Linux 测试网关联调

日期：2026-10-07。控制面 `https://192.168.194.128:8443`，Hysteria2 网关 `192.168.194.128:4433/udp`，网关 TLS SNI `localhost`。此文档记录受控测试入口，不记录用户访问历史或凭据。

## 实测结论

**真实 QUIC/TLS 认证和租约生命周期已通过，公网 HTTPS 转发尚未通过。桌面 UI 仍是开发演示，没有接入整机 VPN。**

| 检查项 | 结果与边界 |
| --- | --- |
| 控制面 HTTPS | Go 客户端使用同步 CA 完成证书链与主机名验证，未关闭 TLS 校验 |
| 用户登录 | 读取本机用户账号文件成功登录；不使用管理员/网关密钥 |
| 设备注册与签名 | 临时 Ed25519 Windows 设备注册成功；五行签名格式通过服务端验证 |
| 临时连接租约 | 使用 SG/global/hysteria2 取得经网关 ACK 的计划 |
| 候选地址 | 实际已返回测试虚拟机地址，不需要 localhost 地址覆盖 |
| Hysteria2 | 官方核心确认 QUIC/TLS 握手和临时凭据认证成功 |
| 激活与续租 | 激活 API 成功；测试提前续租，网关确认且期限延长；未验证长期按 renew_after_seconds 调度 |
| 释放 | 释放 API 成功；释放后用同一凭据的新核心握手被明确拒绝 |
| 公网域名转发 | 固定 HTTPS 测试站点未能访问，核心返回出口 DNS 错误类别；需在服务端核对解析器与出口网络 |
| Windows 本机 DNS 辅助诊断 | 本机解析固定测试域名也失败，不能把此辅助测试视为修复 |
| 内网 HTTPS 辅助诊断 | 经隧道访问内网控制面时报告策略拒绝；可能为预期的私网访问限制，不应为联调盲目关闭该保护 |
| 清理 | 已停止测试核心、释放租约、删除临时设备、删除含临时秘密的测试目录 |

没有验证整机 TUN、系统 DNS/路由、智能分流、Kill Switch、自动重连、租约自然到期，或已建立业务流的吊销速度。拒绝释放后的新握手不能替代“切断现存流”的验收。

## 本地测试工具

`cmd/nimbus-probe` 是独立的 Go 联调命令，使用 `internal/controlclient` 按同步 API 执行授权，不经过 React。它启动一个仅监听 loopback 随机端口的 Hysteria2 SOCKS5 进程，不设置系统代理、不修改 DNS/路由/防火墙。

默认资料：

- `.local/initial-account.json`：普通用户 email/password；不会输出内容。
- `.local/ca.crt`：测试 CA，仅在本工具的 TLS 配置中信任，不安装到系统根证书存储。
- `.local/core/hysteria.exe`：固定官方测试核心。
- `contracts/server-api.md`：此次同步的服务端接口文档；后续还需同步机器可读 OpenAPI。

测试设备私钥仅在进程内存中存在，结束后删除注册的临时设备；这不是正式客户端的持久设备身份。持久客户端仍需系统安全存储。工具没有调用账户级 logout，以免撤销该账户其他客户端的令牌和连接。

核心配置位于当前用户专属 ACL 的独立临时目录；退出时清理。核心输出由内存过滤器提取固定类别，不保留或输出原始日志、目标地址或秘密。

### 复现认证与租约生命周期

在仓库根目录执行：

```powershell
go run ./cmd/nimbus-probe -handshake-only
```

这个模式只声明认证和租约测试，不声明可正常上网。释放后重新启动核心尝试同一旧凭据，以验证新握手拒绝。

### 复现完整 HTTPS 转发

```powershell
go run ./cmd/nimbus-probe
```

固定测试站点为 `https://example.com/`。流量明确经 loopback SOCKS5，不存在失败后直连回退。默认流程须转发成功才进入激活/续租测试，否则释放租约并清理设备。

只有候选仍为 loopback 时才使用显式实验参数：

```powershell
go run ./cmd/nimbus-probe -lab-gateway-host 192.168.194.128
```

该参数保持原 SNI 和 CA 校验，只允许 loopback 替换或与目标相同；不能任意覆盖其他候选。当前候选已可远程访问，无需使用。

`-lab-resolve-probe` 和 `-lab-control-probe` 是隔离 DNS/出口策略问题的辅助测试。前者仅在本机解析固定测试域名，并保持网站 SNI/Host 及证书校验；后者只测试数值 IP 的控制面 HTTPS，不能证明公网访问。辅助模式不能与 handshake-only 混用。

## 测试核心来源

此次使用 [官方 Hysteria2 app/v2.13.0 发布](https://github.com/HyNetworks/hysteria/releases/tag/app/v2.13.0) 的 `hysteria-windows-amd64.exe`，下载后与官方发布 API 的 SHA256 digest 比较，工具每次执行前再次核验：

```text
162ef8fe55dc7ec810dda662908f8e65ec36bd6da39cb87c3bd66184ab68f067
```

配置参照 [官方客户端配置](https://v2.hysteria.network/docs/advanced/Full-Client-Config/)。Hysteria2 在本阶段作为独立协议验证核心使用，不代表更换技术方案中的首选 Mihomo，也不构成正式的签名更新机制。

## 交给服务端的排查事项

可将以下内容交给 Linux 服务端 Codex：

```text
Windows 已实测通过控制面 HTTPS、用户登录、Ed25519 设备签名、
网关 ACK 的租约、Hysteria2 QUIC/TLS 认证、激活、续租、释放，
以及释放后旧凭据的新握手拒绝。

但通过 SOCKS5 访问固定公网 HTTPS 测试站点时，核心报告出口 DNS 失败。
请检查实际 gateway 进程/容器的 DNS 配置、域名解析及公网出口，
包括该运行环境内对 example.com 的解析和 HTTPS 访问。
请区分宿主机和容器网络，不把控制面 HTTPS 可访问当作网关公网出口正常。

内网控制面经代理访问被策略拒绝，可能是预期私网隔离；
不要为了联调取消私网保护。请修复公网域名转发路径，并同步实际验证结果。
```

修复后重新运行默认探测，只有经过真实转发验证后才进入桌面正式连接接入。
