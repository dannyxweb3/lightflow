# Lightflow 共享仓库协作约定

本仓库由 Linux 服务端和 Windows 客户端两个工作区协作。按文件归属修改；阅读其他区域可以，修改前先由对应负责人提出接口请求，避免两个 Codex 同时编辑同一文件。

修改架构前阅读 `REQUIREMENT.md`、`TECHNICAL_DESIGN.md`、`docs/client/TECHNICAL_DESIGN.md`。客户端 MVP 执行方案以客户端文档为准，原 V1 蓝图不作为当前实现清单。

| 文件或目录 | 归属 | 约定 |
| --- | --- | --- |
| `TECHNICAL_DESIGN.md`、`docs/SERVER_MVP_REDESIGN.md` | 服务端 | 总体技术设计只由服务端修改；客户端设计结论写在 `docs/client/`，服务端在总体设计中引用。 |
| `docs/API.md`、`docs/openapi.json` | 服务端 | 接口契约唯一来源；兼容改动的提交信息以 `API:` 开头，并附请求、响应示例供客户端同步。 |
| `internal/`（除 `internal/client/`）、`cmd/nimbus/`、`deploy/`、`scripts/`、`compose.yaml` | 服务端 | 客户端不修改；需要服务端改动时在 `docs/client/SERVER_REQUESTS.md` 记录需求。 |
| `apps/desktop/`、`cmd/lightflow-daemon/`、`internal/client/`、`docs/client/` | 客户端 | 服务端不修改。客户端的设计文档只写入 `docs/client/`，不编辑 `TECHNICAL_DESIGN.md`；新增 Go 依赖前检查 Linux `go test ./...`。 |
| `REQUIREMENT.md` | 项目负责人 | 服务端和客户端均不擅自修改；范围调整先提交需求变更记录，待负责人确认。 |

共同的 MVP 目标是邀请制 Windows 内测：登录、快速连接或选国家、优先 Hysteria2、UDP 不通时自动回退 VLESS-Reality、智能/全局模式、断开后网络恢复。服务端接口或容器健康不能替代 Windows 真人端到端验收。

服务端向客户端提供可访问的测试 API、测试账号、Hysteria2/VLESS 测试入口及契约样例；客户端向服务端提供脱敏 Mihomo 配置、带 `X-Request-ID` 的接口问题和真实回退耗时。凭据、生产主机和 SSH 信息不提交到仓库。

## 客户端目录明细与现有代码例外

以下明细优先于上表对 `internal/`、`scripts/` 的泛指。服务端不得覆盖、批量修改或删除客户端实现。迁移必须以独立客户端提交更新导入、入口、CI 和文档，迁移完成后再更新本表。

| 文件或目录 | 客户端用途与规则 |
| --- | --- |
| `apps/desktop/` | React/Tauri UI、Rust Bridge、安装包资源、客户端测试。M0′ 原型脚本放 `apps/desktop/tools/`，后续新客户端脚本也放这里。 |
| `cmd/lightflow-daemon/` | 后续真实 Windows Service 入口，尚未实现。 |
| `internal/client/` | 后续服务、IPC、DPAPI、API、租约、Mihomo、连接控制、网络事件；Windows 实现用 build tag，避免影响 Linux。 |
| `docs/client/` | 独立客户端技术方案、执行计划、配置基线、实测证据、服务端请求；服务端引用结论，不代改正文。 |
| `cmd/nimbus-daemon/`、`cmd/nimbus-probe/` | 现有客户端演示服务与协议联调工具；迁移前继续归客户端，不是服务端代码。 |
| `internal/daemon/`、`internal/ipc/`、`internal/controlclient/`、`internal/securefile/` | 现有客户端实现；迁移至 `internal/client/` 前归客户端。其他 `internal/` 路径仍归服务端。 |
| `scripts/dev.ps1`、`scripts/dev-daemon.ps1`、`scripts/smoke-desktop.ps1` | 仅这三个现有脚本归客户端；其他根 `scripts/` 文件归服务端。新增客户端脚本不继续放根 `scripts/`。 |
| `启动生产客户端.cmd`、`启动测试客户端.cmd`、`.github/workflows/client.yml` | 现有开发入口与 Windows CI，归客户端；环境标签不代表真实连接能力。 |
| `docs/CLIENT_DEVELOPMENT.md`、`docs/GATEWAY_TEST.md`、`docs/PRODUCTION_CONNECTION.md` | 客户端历史记录，迁移时保留历史与链接；新方案和新证据统一写入 `docs/client/`。 |
| `contracts/ipc.schema.json` | 客户端主责的共享本机契约；变更同步 Rust/Go/TS 类型及版本兼容。云端唯一契约仍为 `docs/API.md`、`docs/openapi.json`，旧客户端 API 副本不继续作为新实现依据。 |

`AGENTS.md`、`README.md`、`.gitignore`、`go.mod`、`go.sum` 是共享文件，改动在提交说明中写明，保留双方入口、忽略规则和依赖，不单方面改模块名。新增 Go 依赖验证 Windows 与 Linux。客户端不实现竞争控制面或网关。

## 客户端执行与安全规则

- 先完成 M0′ 真实 Mihomo 网络证据，再开发真实 Daemon 和 UI。TUN、路由、DNS、物理接口选择优先由 Mihomo 处理；MVP 不做三平台网络抽象、通用 journal、多核心能力矩阵、OIDC 或独立核心更新。
- UI 不持有访问/刷新令牌、设备私钥、代理凭据或核心配置，不执行网络修改。邮箱密码登录的用户输入仅短暂经白名单 IPC 交给服务，提交后清空，不保存或记录。Rust Bridge 只转发类型化命令。
- Go Daemon 使用 Windows Service；令牌和设备私钥由服务用 DPAPI 保存，配合精确文件 ACL 与当前控制用户身份绑定。
- 保留 generation 取消和幂等处理。只有真实隧道转发探测通过且 activate 成功，UI 才能显示已连接。
- 模拟必须显式、可见，不得声称保护流量。真实模式在适配器未完成时拒绝连接，不静默回退为模拟或直连。
- 不记录凭据、用户访问目的地址、域名、DNS 查询、完整 URL、浏览历史或完整核心配置。秘密、生产主机配置和 SSH 信息不提交 Git。
- UI 演示不修改系统路由、DNS 或防火墙。真实原型在隔离测试机验证，只清理本产品拥有的进程、接口和规则，不覆盖其他软件或系统既有配置。
- 服务端接口问题写入 `docs/client/SERVER_REQUESTS.md`，携带安全错误码和 `X-Request-ID`；客户端不自行修改服务端或根总体设计。

## 验证与交接

- 前端：在 `apps/desktop` 执行 `npm run build`、`npm test`。
- Go：根目录执行 `go test ./...`、`go vet ./...`；依赖或 Windows 专属代码变化还需保证 Linux `go test ./...` 可运行。
- Rust：在 `apps/desktop/src-tauri` 执行 `cargo check --locked`。
- 网络改动补 Windows 实测与抓包；模拟、编译、API 健康和后台连接数不替代 VPN 验收。
- 两端通过共享仓库规则、方案和提交说明交接。向另一聊天直接发送消息须有用户授权，文档请求不宣称消息已发送。
