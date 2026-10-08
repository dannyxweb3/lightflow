# 客户端与服务端交接

2026-10-08。方案见 [TECHNICAL_DESIGN.md](TECHNICAL_DESIGN.md)，计划见 [EXECUTION_PLAN.md](EXECUTION_PLAN.md)。本文件记录请求，不修改服务端，不代表已直接发送另一聊天。

| 编号 | 请求/状态 | 需要的交付 |
| --- | --- | --- |
| S0-ACCOUNT | 最新 S0 文档交付独立 Windows 测试账号，本地尚缺 `.local/windows-s0-account.json` | 受控同步账号与 CA，区分测试/生产；不在 Git、文档或聊天输出密码 |
| S0-WINDOWS | Linux 验证不代替 Windows，客户端将实测 API/租约/Mihomo | 当前候选、端口/SNI 与证书有效；有问题时客户端提交安全字段、错误码与 `X-Request-ID` |
| DATE | 服务端已检查 Date 存在，客户端需验证新鲜度/往返时间/签名窗口 | 保持 HTTPS 响应 Date 可用；只有实际不足时再商定最小契约补充 |
| VLESS | 当前 API 只有 Hysteria2，不猜测 VLESS 字段或现存流撤销能力 | S1/S2 后以 `API:` 提交更新 API/OpenAPI、单候选样例、错误码和真实测试入口，附持续流撤销/到期实测限制 |
| OWNERSHIP | 根 `AGENTS.md` 已明列新旧客户端目录、三个现有 scripts、启动器与 CI 归属 | 服务端不批量修改/删除这些路径；客户端迁移单独交接，迁移后更新规则 |
| DESIGN-LINK | 客户端已在 `docs/client/` 独立编写技术方案与执行计划 | 服务端在根总体文档引用客户端方案，客户端不改根设计正文 |

服务端准备 S0/VLESS 环境不必等待 M0′ 完成。客户端先提交 Mihomo 配置与真实分流/恢复证据，再共同测 UDP 被阻断后释放旧租约、新 VLESS 会话、转发/续租/断开。第二国家节点与邀请制内测安排在回退联调之后。

交接只包含脱敏配置、固定测试结论和安全运行摘要；不写生产主机、私钥、令牌、代理凭据、密码、原始抓包或 SSH 参数。
