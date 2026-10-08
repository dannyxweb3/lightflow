# Lightflow 共享仓库协作约定

本仓库由 Linux 服务端和 Windows 客户端两个工作区协作。按文件归属修改；阅读其他区域可以，修改前先由对应负责人提出接口请求，避免两个 Codex 同时编辑同一文件。

| 文件或目录 | 归属 | 约定 |
| --- | --- | --- |
| `TECHNICAL_DESIGN.md`、`docs/SERVER_MVP_REDESIGN.md` | 服务端 | 总体技术设计只由服务端修改；客户端设计结论写在 `docs/client/`，服务端在总体设计中引用。 |
| `docs/API.md`、`docs/openapi.json` | 服务端 | 接口契约唯一来源；兼容改动的提交信息以 `API:` 开头，并附请求、响应示例供客户端同步。 |
| `internal/`（除 `internal/client/`）、`cmd/nimbus/`、`deploy/`、`scripts/`、`compose.yaml` | 服务端 | 客户端不修改；需要服务端改动时在 `docs/client/SERVER_REQUESTS.md` 记录需求。 |
| `apps/desktop/`、`cmd/lightflow-daemon/`、`internal/client/`、`docs/client/` | 客户端 | 服务端不修改。客户端的设计文档只写入 `docs/client/`，不编辑 `TECHNICAL_DESIGN.md`；新增 Go 依赖前检查 Linux `go test ./...`。 |
| `REQUIREMENT.md` | 项目负责人 | 服务端和客户端均不擅自修改；范围调整先提交需求变更记录，待负责人确认。 |

共同的 MVP 目标是邀请制 Windows 内测：登录、快速连接或选国家、优先 Hysteria2、UDP 不通时自动回退 VLESS-Reality、智能/全局模式、断开后网络恢复。服务端接口或容器健康不能替代 Windows 真人端到端验收。

服务端向客户端提供可访问的测试 API、测试账号、Hysteria2/VLESS 测试入口及契约样例；客户端向服务端提供脱敏 Mihomo 配置、带 `X-Request-ID` 的接口问题和真实回退耗时。凭据、生产主机和 SSH 信息不提交到仓库。
