# Windows Daemon 最小设计

2026-10-08；C1 实现依据，当前尚未实现生产服务。只实现下面八个模块。

| 模块 | 职责与 MVP 边界 |
| --- | --- |
| Service 外壳 | Windows Service 生命周期、安装身份和当前交互用户绑定；启动只处理产品残留核心/接口，按完整路径、账户与接口身份确认归属，不按名称批量杀进程/删 Wintun。 |
| IPC | 本机 Named Pipe + 精确 ACL，拒绝远程并核验用户/服务身份；仅 status、login、logout、connect、disconnect、set_settings；不接受 YAML、路径或命令行。schema 留 `contracts/`。 |
| 凭据存储 | 服务上下文 DPAPI + ACL；按环境/OS 用户/账号隔离令牌、设备密钥；不保存密码，UI 不能导出秘密。 |
| API 客户端 | 固定受信 HTTPS、测试独立 CA；登录、串行刷新、订阅/国家/设备/签名；Date 校时，设备证明最多校正重试一次。 |
| 租约管理 | 202 同键同 body、新 nonce；创建、激活、按返回周期续租和幂等释放；只认可已 ACK 截止时间，跨 token 刷新持续运行。 |
| Mihomo 运行器 | 唯一固定核心；配置校验、受限 ProgramData 临时配置、进程/Job 管理、控制管道 ACL；无通用网络 journal，UI 无权直接操作核心。 |
| 连接控制 | 单写队列、generation、取消、真实转发/TUN 探测、回退和恢复；释放状态未知不创建第二租约；对 UI 仅五种产品状态。 |
| 网络监听 | 网络/IP/唤醒事件，1 秒去抖，优先有效租约；退避最长 30 秒；主动断开取消重连，后期接 WFP 开/关保护。 |

入口 `cmd/lightflow-daemon/`，实现 `internal/client/`。同机一个活动隧道由当前控制用户独占，用户切换/注销按断开策略处理。设备密钥持久化，普通断开不删除设备。

IPC 只返回安全账号/订阅摘要、国家、状态、operation_id、instance_id、sequence、稳定错误码。密码仅短时提交后清空，令牌/代理秘密/配置不回传。快照序号和 generation 防止旧回调覆盖状态；命令超时通过 status 查询，不等于取消。

核心启动后真实转发与 TUN 探测通过，再 activate，才显示已连接。断开取消异步操作、停核心、释放租约、检查恢复；异常保留恢复错误和待释放状态，不覆盖其他 VPN 或用户网络策略。本机 logout 不默认调用全账户 logout。
