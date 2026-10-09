# M0′ Mihomo 固定基线

2026-10-09。此基线已用于 Windows 真实 SOCKS 转发，TUN 基线尚未验收。它不是桌面 UI 的连接实现，也不是服务交付。

## 已固定资源

资源清单与下载地址见 [`assets.lock.json`](../../apps/desktop/tools/m0/assets.lock.json)。下载和每次运行均核验 SHA256；规则的 `latest` 地址会变化，变化后拒绝执行，不自动更新锁文件。资源实际保存在 Git 忽略的 `.local/m0/assets/`。

| 资源 | 版本与 SHA256 |
| --- | --- |
| Mihomo ZIP | v1.19.32；`1ac84e795b5b915446e20139677fb6cc013a0778e876e8d4c96f994750b4c4a3`，与官方 release digest 一致 |
| Windows amd64 EXE | `beb9878924d7bd38c67176b441ab5e668cc378bfe751c8dc8fef3eb5aaf566d5` |
| geoip.dat | 2026-10-09 下载；`116cc0f03d48991962f7f9cdbbcf53d45d89777f9c3cd1a663210853e5df6093` |
| geosite.dat | 2026-10-09 下载；`b697cf0728afa1757297d528a83b5c2270ec06652cee5b788353bc246d6b2cd3` |
| Wintun 驱动 | 0.14.1；官方 ZIP `07c256185d6ee3652e09fa55c0b673e2624b565e02c4b9091c79ca7d2f24ef51`；amd64 DLL `e5da8447dc2c320edc0fc52fa01885c103de8c118481f683643cacc3220dafce` |

版本实测输出：`Mihomo Meta v1.19.32 windows amd64 with go1.26.8`，构建日期 2026-09-30，tag `with_gvisor`。核心以官方 Windows amd64 包为准；没有增加 Go 依赖。

## 配置与运行边界

手写模板是 [`baseline.json`](../../apps/desktop/tools/m0/baseline.json)，JSON 已通过本版本 `-t` 验证。原型只用测试 API，申请 SG、smart/global、hysteria2 的新租约；节点地址必须与测试网关匹配，不改写服务端返回的候选。

| 设置 | 已实现的原型行为 |
| --- | --- |
| 代理入口 | 127.0.0.1 随机 SOCKS 端口、每次随机认证；`allow-lan=false` |
| 管理入口 | 随机命名管道；TCP 控制端口为空；启动环境显式设置 `LISTEN_NAMEDPIPE_SDDL` |
| 管道权限 | 受保护 DACL，仅当前进程用户 SID 和 SYSTEM 的文件完全控制；读取实际 DACL 后精确比较，再调用 `/version`；重载后重新检查 |
| 网关 TLS | candidate 的 SNI；`skip-cert-verify=false`；测试 CA 只加载到该核心进程，不安装到系统信任库 |
| 全局原型 | `mode=rule`，唯一规则 `MATCH,LF-TUNNEL`，避免全局 selector 的默认选择歧义 |
| 智能原型 | `GEOSITE,cn,DIRECT`、`GEOIP,cn,DIRECT,no-resolve`、`MATCH,LF-TUNNEL`；国内直连路径尚无实测 |
| DNS / IPv6 / TUN | 均禁用；仅固定 HTTPS 目标由 SOCKS 请求交给核心，不代表系统 DNS 接管或 IPv6 防泄漏 |
| 核心输出 | 模板为 warning；诊断运行显式改为 debug，仅送内存中的白名单分类器，不输出或落盘原始日志、目的地址、完整配置 |
| 临时配置 | `ProgramData/LightflowM0-*`，写入前使用现有 `securefile` 限制为当前用户的受保护 ACL；核心退出后删除 |
| 生命周期 | 临时设备、签名租约、真实转发后 activate、立即 renew、再次转发；最终结束核心、release、删除临时设备 |

上表描述默认 SOCKS 实验。核心进程死亡和目录清理只作用于此次工具创建的对象。默认实验不设置系统代理或修改网络；显式 TUN 实验会由 Mihomo 创建接口、路由和动态 WFP 会话，退出后核对恢复。SOCKS 成功只能说明本工具的测试请求经过代理。

## 管理员 TUN 实验（尚未通过）

[`tun.json`](../../apps/desktop/tools/m0/tun.json) 是独立覆盖层，只有显式 `-tun` 才启用。当前采用 gVisor、MTU 1500、自动路由/严格路由、IPv4/IPv6 默认路由、fake-IP DNS、UDP/TCP 53 劫持和按规则访问的固定 DoH 上游。IPv6 路由接管后以规则拒绝，尚未证明无泄漏；`strict-route` 不作为已验收的 Kill Switch。全局实验明确允许回环与 RFC1918 私网直连，必须后续验证这一边界。

固定 Mihomo 依赖 `sing-tun v0.4.27`，其 amd64 Wintun DLL 内嵌加载；官方 Wintun 0.14.1 DLL 与依赖源码中的 DLL hash 一致，Authenticode 验证为 Valid，签名者 WireGuard LLC。未把另行下载的 DLL 加入搜索路径。官方 ZIP 中的许可文件仅保存在忽略的资源目录；安装包再分发仍需随包保留许可。依据：[固定依赖](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/go.mod)、[内嵌 DLL](https://github.com/MetaCubeX/sing-tun/blob/v0.4.27/internal/wintun/dll_windows_amd64.go)、[Wintun 官方资源](https://www.wintun.net/)。EXE hash 校验覆盖实际使用的内嵌资源。

本机普通互联网出口为 WLAN，测试网关位于 VMware VMnet8。原型在启用 TUN 前查询测试网关物理路由，只给 Hysteria2 出站绑定该接口；这是一项诊断措施，尚未证明是转发故障的根因或解法。核心默认检查发现没有全局 IPv6 上行时会禁用 TUN IPv6；原型仅给自有核心进程设置 `SKIP_SYSTEM_IPV6_CHECK=1`，以便接管并拒绝 IPv6，不更改系统环境。依据：[固定版本 IPv6 检查](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/config/utils.go)。

管理员包装器校验提升前后的原型 EXE hash，隐藏运行窗口；自有核心加入 kill-on-close Job Object。实验前拒绝已有活动 VPN、地址冲突、正在使用的 PktMon 会话或已有抓包过滤器。抓包限定测试网关 UDP 4433 和固定公开测试目标 TCP 443，原始抓包、网络快照仅留在受限且 Git 忽略的 `.local/m0/evidence/`，不保存原始核心日志。TUN 诊断使用 debug 输出，但只在内存保留白名单错误码/阶段，不保存目的地址。

目前已建立 TUN，曾验证 IPv4/IPv6 路由指向该接口；转发仍失败。详情与恢复测量边界见 [实测记录](M0_EVIDENCE.md)。

## 固定版本需要的 CA 启动顺序

v1.19.32 的 YAML/JSON 字段拼写为 `tls.custom-certifactes`。Hysteria2 在解析代理时取得信任池，执行器随后才重置并加载自定义 CA；首次直接加载完整配置实测返回 `CORE_CA_UNTRUSTED`。按源码推断，代理持有的是加载 CA 前的信任池。

原型先加载没有代理、没有 SOCKS、`MATCH,REJECT` 的 bootstrap 配置，完成自定义 CA 加载；核验管道 ACL 后通过 `PUT /configs?force=true` 加载正式临时配置。这一顺序已通过真实转发。没有使用 `skip-cert-verify`。未来升级必须重新验证，不能假设该行为跨版本不变。

2026-10-09 后续：按用户指定，测试控制面默认改为 `https://lightflow.u26d.local:8443`；当前证书尚不覆盖新域名，返回 `CONTROL_CERT_NAME_MISMATCH`，须由服务端补 DNS SAN。`run.ps1` / `run-tun.ps1` 的 `-Control` 可显式选择旧测试 IP 入口，始终严格校验证书，不能自动 fallback。`-ResolveProbe` 仅在核心启动前向固定 DNS 查询固定公网目标，再以其 IPv4 连接并保留原域名 Host/SNI；它不证明系统或网关 DNS 正常。`-DNSOff`、`-Relaxed`、`-OSRoute` 和 `-ProxyOnly` 都是显式诊断选项，不改变默认安全基线。实际 TUN 探测优先于 SOCKS 辅助对照，转发验证失败不 activate。

源码依据：[TLS 信任池](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/component/ca/config.go)、[Hysteria2 TLS 初始化](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/adapter/outbound/hysteria2.go)、[执行器](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/executor/executor.go)、[配置字段](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/config/config.go)。管道默认权限与覆盖变量见 [Windows listener](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/adapter/inbound/listen_windows.go)；管道不校验 secret 见 [controller 源码](https://github.com/MetaCubeX/mihomo/blob/v1.19.32/hub/route/server.go)。

## 开发者操作

在仓库根目录运行：

```powershell
# 准备并校验固定资源，只检查配置，不登录、不运行网络代理
./apps/desktop/tools/m0/run.ps1 -Check
./apps/desktop/tools/m0/run.ps1 -Check -Mode smart
# 测试环境真实代理验证，完成后自动断开并清理
./apps/desktop/tools/m0/run.ps1
./apps/desktop/tools/m0/run.ps1 -Mode smart
# 已授权的隔离测试机管理员 TUN 实验，会出现 UAC；当前仍为失败排查入口
./apps/desktop/tools/m0/run-tun.ps1
# 专项诊断：关闭核心 DNS 模块，不能作为 DNS 保护验收
./apps/desktop/tools/m0/run-tun.ps1 -DNSOff
# 强杀自有核心；须在服务恢复后补测，当前未取得真实恢复证据
./apps/desktop/tools/m0/run-tun.ps1 -ForceKill
```

也可双击 `apps/desktop/tools/m0/运行测试原型.cmd`。默认账号文件 `.local/windows-s0-account.json`，需要测试 CA `.local/ca.crt`。本机实存文件为 `.local/windows-s0~account.json`；包装脚本仅对这一测试账号别名作可见提示，既不读取生产账号也不隐藏切换环境。该入口不会启动桌面演示 UI。

此工具用于开发联调，需要 Go 和 PowerShell。正式产品的登录、选国家和连接将在 C1 UI 中提供，不要求用户复制开发资料。

## 后续门禁

测试服务恢复后先复测无 TUN 对照，再排查启用 TUN 后的转发超时，取得有效双网卡抓包。继续补智能国内直连、全局私网边界、DNS/IPv6、强杀恢复和不同 Windows 用户拒绝访问实测。完整 M0′ 条件见 [执行计划](EXECUTION_PLAN.md)，当前不进入 C1。
