# Windows 客户端开发记录

日期：2026-10-06。当前阶段：M1 开发骨架，真实网络能力尚未实现。

2026-10-07 联调进展：新增独立 Go 控制面客户端与 Hysteria2 探测命令，真实认证和租约生命周期通过，出口 DNS/公网转发仍待排查。该探测没有改变桌面演示状态机，也没有接管系统流量。详见 [GATEWAY_TEST.md](GATEWAY_TEST.md)。

## 已实现

- 首页、国家搜索与选择、网络设置、账户占位及本地诊断。
- React Query 轮询 Daemon 快照；同实例 sequence 防止旧状态覆盖，新 instance_id 支持服务重启。
- Tauri Rust Bridge 只转发类型化白名单命令，通过本地 Named Pipe 通信；3 秒超时和消息大小上限。
- Go 状态机：授权→准备→连接→验证→演示连接；断开使 generation 失效并取消后续阶段。
- 请求幂等与冲突检查、状态检查、国家/设置校验；连着时不能修改网络设置。
- Named Pipe 当前 OS 用户 SID ACL、拒绝远程连接、连接并发和读写大小限制。
- 显式 --development 模式；不指定时拒绝启动，生产不会意外使用演示连接。
- 统一启动脚本、前端和 Cargo 依赖锁文件、本机 IPC schema、测试。

## 当前限制

- Go 服务以当前用户运行，还不是 SCM 管理的特权 Windows Service。
- 国家、延迟、协议偏好与连接状态是示例；设置只存于内存，不改变网络行为。
- 尚无真实登录、订阅、设备、临时租约、Mihomo、TUN、DNS、分流、重连或 Kill Switch。
- 尚无托盘、自动启动、签名安装器及更新；界面明确显示待接入。
- 当前用户 ACL 不等于生产客户端认证；生产服务还需安装身份绑定、设备会话鉴权、可信管道服务校验及权限审计。
- 本阶段不代表 M0 的跨平台网络保护/协议授权可行性已经验证，不代表 V1 可发布。

## 下一阶段顺序

1. 与 Linux 服务端同步 OpenAPI、认证和租约机制，先建立类型化控制面客户端。
2. 完成 Windows Service 安装/卸载与生产 IPC 权限边界。
3. 接入锁定版本 Mihomo，先实现配置校验、进程管理和隔离拨号探测。
4. 在可恢复测试环境验证 TUN、双栈、路由/DNS 事务 journal、崩溃和断开恢复。
5. 接入真实临时凭据与服务器过期/吊销，端到端验证后才允许 UI 显示真实已连接。
6. 完成智能分流、断线保护、网络切换与休眠恢复，再增加发布能力。

所有真实网络实验必须可追踪其修改归属，预先保存恢复记录；不能为了演示将连接状态伪装成 VPN 可用。

## 验证范围

本机验证环境：Windows x64，Node 24.21.0、Go 1.26.8、Rust/Cargo 1.99.0，已安装 MSVC Build Tools 和 WebView2。

2026-10-06 已完成：

| 验证 | 结果 |
| --- | --- |
| npm run build | TypeScript 检查及 Vite 生产构建通过 |
| npm test | 5 项状态/契约测试通过 |
| npm run test:e2e | 3 项 Edge 页面交互测试通过，无页面运行错误 |
| go test ./... | 6 项状态机测试及 1 项真实 Windows Named Pipe 测试通过 |
| go vet ./... | 通过 |
| cargo check | 通过 |
| cargo build --features custom-protocol | Windows debug 桌面可执行程序构建通过 |
| scripts/smoke-desktop.ps1 | 原生 WebView2 页面→Tauri→Rust→Named Pipe→Go→连接/断开通过 |

原生 smoke 只在测试启动的进程中临时启用 loopback WebView2 调试端口，完成后关闭测试进程；发布配置不包含调试端口。CI 文件已提供，尚未在远程流水线运行。

浏览器和桌面 smoke 只验证本地产品交互及 IPC，不能作为 DNS/路由/VPN 验收证据。

参考实现机制核对依据：[Tauri 调用 Rust](https://v2.tauri.app/develop/calling-rust/)、[Tauri 配置](https://v2.tauri.app/reference/config/)、[Microsoft go-winio](https://github.com/microsoft/go-winio)。
