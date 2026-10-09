# 客户端与服务端交接

2026-10-08。方案见 [TECHNICAL_DESIGN.md](TECHNICAL_DESIGN.md)，计划见 [EXECUTION_PLAN.md](EXECUTION_PLAN.md)。本文件记录请求，不修改服务端，不代表已直接发送另一聊天。

| 编号 | 请求/状态 | 需要的交付 |
| --- | --- | --- |
| S0-ACCOUNT | 2026-10-09 已取得用户保存的 S0 文件，Windows 测试登录通过 | 测试与生产隔离；账号、CA 保留在忽略目录，不输出密码 |
| S0-WINDOWS | Linux 验证不代替 Windows，客户端将实测 API/租约/Mihomo | 当前候选、端口/SNI 与证书有效；有问题时客户端提交安全字段、错误码与 `X-Request-ID` |
| DATE | 服务端已检查 Date 存在，客户端需验证新鲜度/往返时间/签名窗口 | 保持 HTTPS 响应 Date 可用；只有实际不足时再商定最小契约补充 |
| VLESS | 当前 API 只有 Hysteria2，不猜测 VLESS 字段或现存流撤销能力 | S1/S2 后以 `API:` 提交更新 API/OpenAPI、单候选样例、错误码和真实测试入口，附持续流撤销/到期实测限制 |
| OWNERSHIP | 根 `AGENTS.md` 已明列新旧客户端目录、三个现有 scripts、启动器与 CI 归属 | 服务端不批量修改/删除这些路径；客户端迁移单独交接，迁移后更新规则 |
| DESIGN-LINK | 客户端已在 `docs/client/` 独立编写技术方案与执行计划 | 服务端在根总体文档引用客户端方案，客户端不改根设计正文 |

服务端准备 S0/VLESS 环境不必等待 M0′ 完成。客户端先提交 Mihomo 配置与真实分流/恢复证据，再共同测 UDP 被阻断后释放旧租约、新 VLESS 会话、转发/续租/断开。第二国家节点与邀请制内测安排在回退联调之后。

交接只包含脱敏配置、固定测试结论和安全运行摘要；不写生产主机、私钥、令牌、代理凭据、密码、原始抓包或 SSH 参数。

## 2026-10-09 C0 / M0′ 初次结果

真实 Mihomo 全局/智能海外 SOCKS 转发各完成一次成功探测：新设备、新租约、TLS、公开 HTTPS 内容、activate、立即 renew、再次转发、release 和临时设备删除均通过。管道实际 ACL 与当前用户/SYSTEM 白名单一致。没有发现需要服务端修改才能完成此次代理转发的问题。

自定义 CA 初次加载问题通过客户端核心 bootstrap/reload 顺序解决，源码推断与实测见 [MIHOMO_BASELINE.md](MIHOMO_BASELINE.md)。后续已核验驱动并开展管理员 TUN 实验，TUN 转发/有效抓包尚未通过，DNS/IPv6、异常恢复及跨身份拒绝访问仍需补证据；不能把以上结果作为 VPN 交付验收。此次未向另一聊天发送消息。

## 2026-10-09 后续联调状态

客户端 TUN 接口已建立，IPv4/IPv6 路由曾选择该接口，但转发超时；关闭核心 DNS 模块仍复现，原因未确定。正常失败回收后原网络基线比较一致，强杀恢复未验收。客户端继续定位，不请求服务端代改客户端或凭空调整接口。

最新无 TUN 对照在登录前失败：`CONTROL_REQUEST_FAILED`，Windows 无法建立到测试控制面 `192.168.194.128:8443` 的 TCP 连接。VMnet8 为 Up，测试网段路由与虚拟机进程存在。无 HTTP 响应，故无 `X-Request-ID`；这是一项环境可达性阻塞，尚不能判定服务端代码故障。请测试环境负责人确认虚拟机 IP、服务监听和入口防火墙，恢复现有 S0 入口；不需要提供新密码，也未切换生产账号。恢复后客户端复测无 TUN 对照，再继续网络实验。完整证据边界见 [M0_EVIDENCE.md](M0_EVIDENCE.md)。

后续：测试 IP 入口已恢复，固定公网 IPv4 的真实 SOCKS 转发通过；域名请求在 TLS 握手阶段 EOF，网关域名解析/出站仍待核对，TUN 转发仍失败。用户指定控制面改用 `https://lightflow.u26d.local:8443`，Windows 解析到 S0 地址，但严格 TLS 返回 `CONTROL_CERT_NAME_MISMATCH`，没有 HTTP 响应或请求 ID。请求服务端为 HTTPS 证书增加 `DNS:lightflow.u26d.local` SAN 并重载入口；若 CA 更换，安全同步测试 CA。客户端不绕过证书校验，不自动切换入口。此次未直接向另一聊天发送消息。
