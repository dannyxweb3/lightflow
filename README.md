# Nimbus VPN · Windows 客户端

基于 REQUIREMENT.md 和 TECHNICAL_DESIGN.md 的客户端开发项目。

当前交付 M1：Tauri 2 + React/TypeScript UI、Rust IPC Bridge、Go 本地开发服务、Windows Named Pipe 和演示状态机。**当前版本不会建立真实 VPN，不修改系统 DNS、路由或防火墙，也不提供 Kill Switch 保护。**

## 启动

不熟悉命令行时，在文件资源管理器打开项目目录，双击根目录的 `启动客户端.cmd`。启动器自动执行下面的开发启动流程，无需输入命令。首次构建可能需要几分钟；启动期间保留出现的终端窗口。

桌面窗口打开后，通过“国家与地区”选择位置，再返回“连接”页点击“快速连接”。当前仍为演示操作；账户页尚无真实登录，按钮不会接管系统流量。正式安装程序、桌面快捷方式及真实登录/节点连接流程仍待实现。

已安装 Node.js、Go、Rust MSVC、Visual Studio C++ Build Tools 和 WebView2 后，在仓库根目录执行：

```powershell
./scripts/dev.ps1
```

脚本构建并启动当前用户的 Go 开发服务，再启动 Tauri 桌面应用；退出开发脚本时清理它创建的服务进程。Rust 在默认 `.cargo/bin` 时自动补入当前进程 PATH。

仅预览页面：

```powershell
./scripts/dev.ps1 -Browser
```

打开 `http://127.0.0.1:1420/?preview=1`。没有 preview 参数的浏览器页面不能建立连接，也不会自动回退到演示。

## 检查

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

## 项目结构

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

## 真实网关联调

独立探测命令 `go run ./cmd/nimbus-probe -handshake-only` 已实测认证与租约生命周期。它不会接管整机网络；默认公网转发测试尚未通过。准备资料、测试边界与服务端排查事项见 [GATEWAY_TEST.md](docs/GATEWAY_TEST.md)。

## 原生桌面 smoke

先执行前端构建，再在 `apps/desktop/src-tauri` 执行：

```powershell
cargo build --features custom-protocol --locked
```

返回仓库根目录执行 `./scripts/smoke-desktop.ps1`。该测试临时启动桌面程序和开发服务，验证真实 IPC，然后关闭它们；需要 Microsoft Edge/WebView2 与已安装的 Playwright 测试依赖。
