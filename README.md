# Lightflow

本仓库包含 Windows 客户端与 Linux 服务端。本工作区开发 Windows 客户端；控制面和网关由 Linux 工作区维护。

## Nimbus VPN · Windows 客户端

基于 REQUIREMENT.md 和 TECHNICAL_DESIGN.md 的客户端开发项目。

当前交付 M1：Tauri 2 + React/TypeScript UI、Rust IPC Bridge、Go 本地开发服务、Windows Named Pipe 和演示状态机。**当前版本不会建立真实 VPN，不修改系统 DNS、路由或防火墙，也不提供 Kill Switch 保护。**

### 启动

不熟悉命令行时，在文件资源管理器打开项目目录，选择一个启动入口双击，无需输入命令：

- `启动生产客户端.cmd`：选择生产联调目标，控制面 `https://lightflow.aibusinesses.cc`，网关 `lightflow-gw.aibusinesses.cc`，实际端口由 API 提供。
- `启动测试客户端.cmd`：选择内网测试目标，控制面 `https://192.168.194.128:8443`，网关 `192.168.194.128:4433/udp`。

窗口显示所选环境与公开地址；两个入口当前均启动开发演示，尚未接入真实桌面登录或连接。启动器不读取账号文件、不复制凭据、不安装 CA，也不改变系统网络。首次构建可能需要几分钟；启动期间保留出现的终端窗口。当前共用本地管道和 1420 开发端口，两个入口需分别运行，不能同时启动。

桌面窗口打开后，通过“国家与地区”选择位置，再返回“连接”页点击“快速连接”。当前仍为演示操作；账户页尚无真实登录，按钮不会接管系统流量。正式安装程序、桌面快捷方式及真实登录/节点连接流程仍待实现。

已安装 Node.js、Go、Rust MSVC、Visual Studio C++ Build Tools 和 WebView2 后，在仓库根目录执行：

```powershell
./scripts/dev.ps1
```

命令行默认选择生产目标；测试目标使用 `./scripts/dev.ps1 -Profile Test`，浏览器预览也支持同一 `-Profile` 参数。

脚本构建并启动当前用户的 Go 开发服务，再启动 Tauri 桌面应用；退出开发脚本时清理它创建的服务进程。Rust 在默认 `.cargo/bin` 时自动补入当前进程 PATH。

若本工作区已有当前用户的开发服务，启动器验证 Named Pipe 的演示快照后复用该进程，避免重复创建管道导致 Access is denied。复用前检查进程路径、开发参数和用户 SID；退出时只清理本次启动器创建的服务。新服务通过实际 IPC 响应判断就绪，不依赖固定等待 400 毫秒。

仅预览页面：

```powershell
./scripts/dev.ps1 -Browser
```

打开 `http://127.0.0.1:1420/?preview=1`。没有 preview 参数的浏览器页面不能建立连接，也不会自动回退到演示。

### 检查

```powershell
go test ./...
go vet ./...
cd apps/desktop
npm ci
npm run build
npm test
npm run test:e2e
cd src-tauri
cargo check --locked
```

界面测试默认使用本机 Microsoft Edge；CI/其他机器需要安装 Edge 或调整 Playwright channel。首次 Rust 构建需要下载依赖。

### 项目结构

```text
apps/desktop/             React UI 与 Tauri 桌面壳
cmd/nimbus-daemon/        Go 开发服务入口
internal/daemon/          连接状态机、取消、幂等、设置
internal/ipc/             Windows Named Pipe 与实际通信测试
contracts/               本机 IPC schema 与云端联调要求
scripts/                 Windows 启动与验证脚本
docs/                    客户端开发进度和验证记录
```

Go 云端控制面与 Gateway Agent 由 Linux 服务器上的 Codex 开发。两端共享需求、设计和契约，不共享聊天上下文。

详细进度见 [CLIENT_DEVELOPMENT.md](docs/CLIENT_DEVELOPMENT.md)。

### 真实网关联调

独立探测命令 `go run ./cmd/nimbus-probe -handshake-only` 已实测认证与租约生命周期。它不会接管整机网络；默认公网转发测试尚未通过。准备资料、测试边界与服务端排查事项见 [GATEWAY_TEST.md](docs/GATEWAY_TEST.md)。

### 原生桌面 smoke

先执行前端构建，再在 `apps/desktop/src-tauri` 执行：

```powershell
cargo build --features custom-protocol --locked
```

返回仓库根目录执行 `./scripts/smoke-desktop.ps1`。该测试临时启动桌面程序和开发服务，验证真实 IPC，然后关闭它们；需要 Microsoft Edge/WebView2 与已安装的 Playwright 测试依赖。

## Lightflow 服务端

Go 控制面 + PostgreSQL + 受监管的 Hysteria2 网关。提供不内置 HTTPS 终止的 Docker Compose 部署、设备签名、临时租约、服务端强制断连和可执行集成测试。

这是首个可运行的服务端交付，**不是整个 PRD 的 V1 完成声明**。真实接入的协议为 Hysteria2；VLESS/Reality、VMess、Trojan、Snell、正式客户端连接及多协议自动回退尚未实现。服务端不会把未实现的协议返回给客户端。

### 快速部署

需要 Docker Engine、Docker Compose v2+、Python 3、OpenSSL。以下命令在仓库根目录执行：

```bash
## 仅首次执行；已有 .env / .local 时跳过，脚本不会覆盖密钥。
python3 scripts/init.py

docker compose config --quiet
docker compose up -d --build postgres control
python3 scripts/bootstrap.py
docker compose up -d --build gateway
curl --fail http://127.0.0.1:9010/readyz
```

初始化账户保存在 `.local/initial-account.json`，管理员密钥保存在 `.env`；不提供通用默认密码。API 默认只监听宿主机回环地址，网关开放 UDP 4433。应用 API 只提供 HTTP，默认网关证书由私有 CA 签发，测试客户端必须信任 `.local/certs/ca.crt`，不要关闭证书校验。

管理后台位于 `http://127.0.0.1:9010/console/`，初始密码在 `.local/admin-console.json`。已有 `.env` 的部署先运行 `python3 scripts/init_admin.py` 并重建 control。后台只适合本机、SSH 隧道或受控管理入口；外部 HTTPS 仍由 Nginx/Cloudflare 处理。

- [部署、外层 HTTPS、备份和升级](docs/DEPLOYMENT.md)
- [服务端 MVP 重新设计与改进计划](docs/SERVER_MVP_REDESIGN.md)
- [生产服务端重部署](docs/PRODUCTION_REDEPLOY.md)
- [Windows 客户端连接生产环境](docs/CLIENT_PRODUCTION.md)
- [API、设备签名与客户端接入](docs/API.md)
- [Windows 宿主机内网联调](docs/WINDOWS_LAN.md)
- [管理后台功能文档](docs/ADMIN_CONSOLE.md)
- [实现范围、设计取舍和验收](docs/IMPLEMENTATION.md)
- [OpenAPI 契约](docs/openapi.json)
- [原始需求](REQUIREMENT.md) / [总体设计](TECHNICAL_DESIGN.md)

### 已实现

- 邮箱密码登录、短期访问令牌、刷新令牌轮换和重放撤销、全账户登出。
- 订阅、套餐设备/并发限制；管理员变更订阅时撤销旧租约。
- Ed25519 设备身份，连接接口请求签名与防重放。
- 国家及网关管理，健康过滤、按容量占用选择网关、手选国家约束。
- PostgreSQL 事务配额、幂等连接创建、网关 ACK 后下发凭据、续租和释放。
- 网关令牌身份、完整授权快照同步、Hysteria2 HTTP 认证及现存连接踢除。
- Agent 本地单调时钟执行到期；无法监管时结束网关进程。
- 签名策略/更新元数据发布、基础指标、数据库版本迁移、定期数据清理。
- 单管理员管理后台：概览、用户和订阅、国家、节点；数据库会话与 CSRF 保护。

### 开发和验证

Go 1.26+，PostgreSQL 16+：

```bash
go build ./cmd/nimbus
go vet ./...
go test -race ./...

## 使用专门测试数据库；每个用例在独立 schema 中运行并清理。
TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/testdb?sslmode=disable' \
  go test -race -count=1 ./...

## 增加真实 Hysteria2 连接、撤销与离线过期测试。
TEST_DATABASE_URL='postgres://user:password@127.0.0.1:5432/testdb?sslmode=disable' \
TEST_HYSTERIA_BIN='/absolute/path/to/hysteria' \
  go test -race -count=1 ./...
```

未设置集成测试环境变量时，相关用例会明确 `SKIP`，不代表已验证数据库或真实网关。
