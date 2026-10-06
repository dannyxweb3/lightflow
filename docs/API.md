# API 与客户端接入

机器可读契约见 `openapi.json`。应用 API 使用 HTTP `/v1`；对公网提供 HTTPS 时在 Nginx/Cloudflare 层终止；`/admin` 与 `/metrics` 只从本机或受控管理网络访问。错误响应为 `{code, request_id, retryable}`，不包含底层 SQL、凭据或网关配置。

## 1. 登录、刷新和登出

`POST /v1/auth/login`：

```json
{"email":"user@example.test","password":"从初始化凭据文件读取"}
```

返回 `access_token`、`refresh_token`、`token_type=Bearer`、`expires_at`。访问令牌有效 15 分钟，刷新令牌从该次登录起固定有效 7 天；轮换不会延长这个期限。后续接口使用 `Authorization: Bearer <access_token>`。

`POST /v1/auth/refresh` 发送 `{"refresh_token":"..."}`。刷新后旧 refresh token 失效；重放旧 token 会撤销该登录家族的全部令牌。客户端必须串行刷新并原子保存新令牌；网络丢失响应后不能无限重放旧令牌，应重新登录。

`POST /v1/auth/logout` 撤销**整个账户**的访问/刷新令牌和所有 VPN 租约。当前没有“只登出一个登录会话”的接口。

默认登录限流：单账户标识每 5 分钟 10 次、单直接对端 IP 每 5 分钟 100 次，存于 PG，对多 API 实例有效。不信任请求自行提供的 X-Forwarded-For；经过反向代理的登录请求会共享该代理 IP 的限制，扩大服务规模前需增加受信代理识别和边缘限流。限流返回 429 和 `Retry-After: 300`。

## 2. 设备注册

客户端本地生成 Ed25519 密钥对，私钥存入系统安全存储。发送：

```http
POST /v1/devices
Authorization: Bearer <access_token>
Content-Type: application/json
```

```json
{"name":"My Linux PC","os":"linux","public_key":"32 字节公钥的标准 Base64"}
```

`os` 支持 `windows`、`macos`、`linux`。返回 `{"id":"设备ID"}`；相同账号相同公钥重试返回原设备。删除的公钥不能重新注册，用户需要明确注册新设备密钥。

`GET /v1/devices` 查看设备，`DELETE /v1/devices/{id}` 删除设备并撤销其所有连接。设备删除通过账户令牌授权，使用户可以从另一设备删除丢失设备。

## 3. 设备请求签名

创建、激活、续租、释放连接都需要账户访问令牌和下列头：

```text
X-Device-ID: <设备ID>
X-Device-Timestamp: <Unix秒数>
X-Device-Nonce: <每请求随机字符串，16..128字节>
X-Device-Signature: <Ed25519签名的标准Base64>
```

签名原文为 UTF-8，五行，无末尾换行：

```text
大写HTTP方法
URL转义后的路径，不含查询串
原样的X-Device-Timestamp
原样的X-Device-Nonce
原始请求体字节的SHA256小写十六进制
```

例如 `POST\n/v1/connection-sessions\n1791273600\n<nonce>\n<body-sha256>`。空请求体也要计算空字节的 SHA256；不能把 `null`、`{}` 和空请求体混用。时间窗口为 ±60 秒，nonce 防重放保留 2 分钟。客户端重试业务请求时使用**新 nonce 和新签名**，但保持原 Idempotency-Key。

## 4. 创建连接

```http
POST /v1/connection-sessions
Idempotency-Key: <16..128字节随机字符串>
```

```json
{"country_code":"SG","mode":"smart","protocols":["hysteria2"]}
```

`country_code` 省略或空字符串表示快速连接。手动国家不会回退到其他国家。`mode` 为 `smart` 或 `global`；直连模式不需要租约，应在客户端本地执行。当前只有 Hysteria2 网关；不支持的协议返回 `NO_COMPATIBLE_ENDPOINT`。

服务端在数据库事务中校验订阅、设备及同时连接数量，预留一个逻辑会话。每台设备最多一个活动会话。网关每 2 秒获取完整授权快照并 ACK；服务端收到 ACK 后才返回 200：

```json
{
  "schema_version": 1,
  "lease_id": "session-id",
  "device_id": "device-id",
  "state": "issued",
  "expires_at": "2026-10-06T10:10:00Z",
  "country_code": "SG",
  "renew_after_seconds": 200,
  "candidates": [{
    "endpoint_id": "gateway-1",
    "protocol": "hysteria2",
    "transport": "quic",
    "host": "sg-vpn.example.com",
    "port": 4433,
    "credential": "临时秘密",
    "public_params": {"server_name": "sg-vpn.example.com"}
  }]
}
```

目前每个计划只有一个候选。认证秘密仅供本地网络服务使用，不进入 UI、日志、URL 或公开缓存。凭据由主密钥和租约 ID 派生，数据库与 Agent 只保存摘要；知道租约 ID 不能计算凭据。

4 秒内未收到 ACK 时返回 202、`Retry-After: 1` 和 `{"lease_id":"...","state":"pending"}`，此时没有可用凭据。按相同请求及幂等键重试。未获 ACK 的 pending 会话在 30 秒后被清理任务终止；已终止会话返回 `SESSION_CLOSED`，需要新幂等键重新连接。幂等记录与结束租约一起保留至到期后 7 天，超过保留期不保证旧键继续幂等。

## 5. 激活、续租、释放

- `POST /v1/connection-sessions/{id}/activate`：客户端连接成功后的协调上报；不替代网关实际鉴权。
- `POST /v1/connection-sessions/{id}/renew`：重新检查权益并申请延长；网关 ACK 后返回新的计划。202 期间只能依赖上次确认的截止时间，不能自行延长。
- `DELETE /v1/connection-sessions/{id}`：释放租约，重复释放幂等返回 204。

初始租约默认 600 秒，建议约 200 秒续租，与返回值一致；不会超过订阅截止时间。API 不接受客户端自行指定到期时间。

状态为 `pending → issued → active`，终态为 `released / revoked / expired`。即使客户端没有 activate，issued 也占名额并受网关约束。自动重连优先复用尚未到期的租约；换入口需先释放原租约再创建新租约，当前不支持无缝多入口切换。

## 6. 策略与更新元数据

管理员通过 `POST /admin/documents/policy` 或 `/admin/documents/manifest` 发布：

```json
{"version":1,"payload":{"schema_version":1,"protocol_order":["hysteria2"]}}
```

版本必须严格递增。客户端分别通过 `GET /v1/policies/current`、`GET /v1/artifacts/manifest` 获取：

```json
{"algorithm":"Ed25519","payload":"Base64编码的待验签JSON字节","signature":"Base64签名"}
```

使用预置公钥验证解码后的 **payload 原始字节**，不能先反序列化再重新编码后验签。签名内容包含 kind、version、published_at、valid_until 和 content。响应有效期 24 小时；客户端还须保存已接受的最高版本并校验 kind、有效期及兼容性。当前只提供签名发布机制，未内置规则数据、制品上传、对象存储或 CDN；管理员对 payload 负责，客户端必须按各自 schema 验证，不执行任意远程命令。

发布二进制清单时建议 content 包含平台、架构、版本、HTTPS URL、size、SHA256、最小客户端/核心版本。下载、校验及回滚属于客户端职责。

## 7. 管理接口

使用独立 `ADMIN_KEY` 作为 Bearer 凭据；不能使用普通用户访问令牌。

| 接口 | 请求 |
| --- | --- |
| `POST /admin/users` | email、password、subscription |
| `PUT /admin/users/{id}/subscription` | plan、expires_at、device_limit、concurrent_limit、enabled |
| `POST /admin/countries` | code、name；按 code 更新名称 |
| `POST /admin/endpoints` | id、country_code、host、port、server_name、capacity、auth_token |
| `PATCH /admin/endpoints/{id}` | `{"enabled":false}` 或 `{"auth_token":"新网关令牌"}`；停用或换令牌会撤销现存租约 |
| `GET /admin/endpoints` | 节点目录、ready、last_seen_at |
| `GET /admin/overview` | 用户、活跃/待确认租约、节点数量 |
| `GET /admin/users?search=&offset=` | 用户与订阅列表，每页 50 条 |
| `GET /admin/users/{id}` | 用户订阅、最近 100 条设备与连接 |
| `GET /admin/countries` | 全部国家及启用状态 |
| `POST /admin/documents/{kind}` | version、payload |
| `GET /metrics` | Prometheus 文本格式 |

所有订阅修改都撤销现有租约，客户端需重新申请，包括单纯延长订阅。设备数量下调不会自动删除设备，超过新上限后禁止再注册；并发限制立即通过撤销旧租约生效。

管理后台位于 `/console/`，浏览器经 `/console/api/*` 访问同一组管理操作；使用独立密码、HttpOnly Cookie 和写操作 CSRF 令牌，不把 `ADMIN_KEY` 发给浏览器。后台功能和访问限制见 [管理后台功能文档](ADMIN_CONSOLE.md)。

## 8. 错误处理

| code | 建议行为 |
| --- | --- |
| AUTH_EXPIRED / AUTH_REQUIRED | 刷新令牌或重新登录 |
| INVALID_CREDENTIALS / REFRESH_REPLAY | 重新登录，不重复旧刷新令牌 |
| SUBSCRIPTION_INACTIVE | 展示订阅状态，停止重连 |
| DEVICE_LIMIT / CONCURRENT_LIMIT | 展示限制，允许用户管理设备或断开其他会话 |
| DEVICE_REVOKED | 停止该设备的连接 |
| DEVICE_ALREADY_CONNECTED | 复用已有租约或先释放 |
| INVALID_DEVICE_PROOF / PROOF_REPLAY | 校对时间、密钥和签名；每次重试使用新 nonce |
| IDEMPOTENCY_CONFLICT | 相同键对应不同请求，检查客户端逻辑 |
| NO_CAPACITY | 等待并退避重试，不改变手选国家 |
| NO_COMPATIBLE_ENDPOINT | 客户端没有可用协议能力 |
| SESSION_CLOSED | 原租约结束，重新授权 |
| RATE_LIMITED | 按 Retry-After 退避 |
| INTERNAL_ERROR | 用 X-Request-ID 排障，不向用户暴露内部错误 |

内部 `GET /internal/gateways/{id}/snapshot` 与 `POST /internal/gateways/{id}/ack` 需要 `Authorization: Bearer <该网关令牌>`；仅供私有网络上的 Agent 使用，默认不映射宿主机端口。
