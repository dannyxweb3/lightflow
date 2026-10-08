# 客户端契约

2026-10-07 已同步服务端 [API 文档](server-api.md)，并新增 Go `internal/controlclient` 与独立 `nimbus-probe` 联调命令。实际测试结果见 [GATEWAY_TEST.md](../docs/GATEWAY_TEST.md)。机器可读 OpenAPI 尚待同步；桌面 UI 尚未接入正式授权。

`ipc.schema.json` 是本机 IPC 的 v1 契约，不是云端 API。

- Windows 开发管道：`\\.\pipe\nimbus-vpn-dev-v1`。
- 一条连接一个请求和一个响应，UTF-8 JSON，以 LF 结尾；请求不超过 8192 字节，响应不超过 65536 字节，读写截止时间 3 秒。
- 请求 api_version 固定 1。方法只允许 get_snapshot、connect、disconnect、update_settings。
- connect 返回完整快照，operation_id 标识异步连接意图；轮询 get_snapshot 观察阶段。
- sequence 在同一 instance_id 下单调增加，服务重启会生成新 instance_id。UI 丢弃同一实例的旧快照。
- request_id 幂等范围为当前服务实例最近 256 个成功写请求；相同 ID 不同请求体返回 IDEMPOTENCY_CONFLICT。失败请求不缓存。不承诺跨进程重启幂等。
- disconnect 取消全部旧连接阶段；更改设置要求 disconnected。direct 模式拒绝 connect。
- 开发模式 simulation 必须为 true，connected 只表示状态机演示，不意味着流量保护。
- 管道由当前用户 SID DACL 限制，库拒绝远程客户端。当前实现不是管理员 Windows Service；没有设备鉴权或生产安装服务。

客户端与 Linux 服务端联调前，应从服务端同步 `contracts/openapi.yaml`。客户端需要：

1. 认证 issuer、PKCE/OIDC 回调、开发认证方式及生产禁用规则。
2. /v1/me 的订阅状态、到期时间、设备与并发额度。
3. 国家目录、设备注册/删除及设备证明方式。
4. 连接租约创建/激活/续期/释放、错误码、幂等键、版本兼容。
5. 真实协议候选、公有握手参数及敏感凭据的生命周期。

节点凭据和核心配置只进入 Go Daemon，不进入 React 数据模型。本次不创建另一份未经服务端确认的云端 OpenAPI。
