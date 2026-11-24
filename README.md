# 轻渡 · Ferry

**面向自有设备、浏览器与 AI 智能体的端到端加密跨平台文件摆渡工具。**

轻渡（Ferry）专注于在你的手机、电脑与浏览器之间快速、安全、无损地传输文件与文本：无需账号注册、不经第三方网盘压缩或留存，基于 Go 单一核心实现 **局域网直连 → NAT 打洞 P2P（QUIC）→ 高吞吐 WSS 中继** 三档链路自动调度，并支持大模型 MCP 工具直连调用。

> [!NOTE]
> 当前版本：`v1.0.0-beta.26`（协议版本 `v1`）。仓库保持私有维护，按公开发布文档标准编写；建议从 Releases 下载预编译产物或按下方指引从源码构建。

---

## 核心特性

### 1. 三档自适应传输与大文件热升级

```text
发送端发起传输
  ├─[1] 同网段局域网直连 ──▶ UDP 广播/组播发现（或手动地址），零服务器流量
  ├─[2] 跨网 UDP 打洞 P2P ──▶ STUN + UPnP/NAT-PMP/PCP + 对称 NAT 生日悖论探测（600 ms 快速探测）
  │                              └─▶ 内层双向认证 TLS 1.3 over QUIC（低于 5 Mbps 自动保底回退中继）
  └─[3] 私有 WSS 加密中继 ──▶ 服务端零知识字节拼接（4 MiB 滑动窗口 / 256 KiB 分块 / 写合并流水线）
                                 └─▶ ≥ 4 MiB 批次传输期间后台持续尝试 P2P 打洞，打通后通过 .ftpart 无损热切换直连
```

- **穿透增强**：内置标准 STUN 探测、纯 Go UPnP IGD / NAT-PMP / PCP 网关端口映射，以及面向对称型 NAT 的 `FTBP` 生日悖论端口探测；即使在代理 TUN 环境下也可根据路由与网络条件自动探测并平稳兜底。
- **高吞吐流水线**：采用 `256 KiB` 分块与 `4 MiB` 并发滑动窗口，配合 WebSocket 写合并与多帧缓冲流水线；每段数据逐文件流式读写，内存占用不随文件体积增长。
- **断点续传与一致性校验**：接收端通过 `<文件名>.ftpart` 记录已落盘偏移量并在建连握手后自动续传，传输完成后执行整文件 **SHA-256** 校验。

### 2. 端到端安全与零知识中继

- **独立设备身份**：每台设备本地生成 Ed25519 密钥对并派生 64 位十六进制设备 ID。Windows 下通过 **DPAPI** 用户作用域加密封装（`FTKEYDP1`），Android 存放在应用沙箱私有目录，Linux/macOS 以 `0600` 权限持久化。
- **双向证书固定（TLS 1.3 Pinning）**：除显式调试外，App ↔ App 在公网打洞（QUIC）与中继（WSS）隧道内部均嵌套双向认证的 TLS 1.3 会话，严格固定已配对设备的公钥指纹，中继节点只能看到加密字节流，且不持久化配对列表。
- **两种免账号配对方式**：
  - 扫描二维码或导入 `ftpair://` 配对 URI。
  - **SPAKE2 一次性 6 位配对码**：口令仅在本地参与密钥协商，明文口令不经由服务器传递。
- **路径穿越与载荷防护**：协议层（`Manifest.Validate`）与落盘前清洗（`safeName` / `cleanName`）双重拦截 `..`、路径分隔符与非法文件名；服务端内置按 IP 登录限频、按设备配对消息限频及单房间带宽保护。

### 3. 全平台客户端与临时快传

| 端 / 组件 | 入口 / 产物 | 核心能力 |
|---|---|---|
| **Windows 桌面端** | `ferry-desktop-windows-amd64.exe` / `ferry-setup-windows-amd64.exe` | 原生 Win32 界面、托盘常驻、DWM Acrylic 接收确认弹窗、顶部磁吸拖拽投放面板、资源管理器右键菜单扩展（`ftmenu.dll`）、开机自启、一键安装/卸载 |
| **Android 原生端** | `ferry-android-arm64.apk` | 原生 Java + `gomobile` Go 核心（API 21+，16 KB 页对齐），支持文件/文字/剪贴板发送、系统「分享」面板接入、1.5x 默认变焦扫码、`TransferService` 常驻后台与锁屏低功耗休眠 |
| **浏览器临时快传** | `/transfer`、`/app`、`/s/<token>` | 免配对一码双用：客户端生成 Ed25519 签名的 10 分钟临时 HTTPS 链接与 6 位取件码；浏览器支持公网加密中继与「同 Wi-Fi 极速直连」切换 |
| **跨平台 CLI & MCP** | `ferry` (`cmd/ferry`) | Windows / Linux / macOS 命令行收发、常驻守护进程 (`ferry daemon`)、临时快传 (`ferry share`)、标准 stdio JSON-RPC 2.0 **MCP Server** (`ferry mcp`) |
| **信令与中继服务端** | `ferry-server-linux-amd64` (`cmd/ft-server`) | 单二进制、全内存状态；集成 WebSocket 信令 (`/signal`)、房间中继 (`/relay`)、匿名快传网页门户与双端口 UDP STUN 服务 |

---

## 快速开始

### 一、安装桌面端与移动端

- **Windows**：运行安装向导 `ferry-setup-windows-amd64.exe`，默认安装至用户本地目录并注册托盘自启与右键菜单；如需卸载可运行：
  ```powershell
  ferry-setup-windows-amd64.exe -uninstall
  ```
- **Android**：安装 `ferry-android-arm64.apk`（支持 Android 5.0+，`arm64-v8a` 架构）。首次使用建议授予存储/媒体读取权限、通知权限（用于后台接收状态与传输通知）以及相机权限（用于扫码配对与快传）。

### 二、设备配对（首次跨网互传前）

1. **图形界面配对**：在 Windows 桌面端打开「配对」生成二维码或复制 `ftpair://` 链接，在 Android「设备」页点击「扫码配对」或通过「配对码」完成绑定。
2. **命令行配对**：
   ```bash
   # 查看本机设备 ID 与公钥
   ferry whoami

   # 方式 A：通过配对 URI
   ferry pair --show --name "MyPC"
   ferry pair --add "ftpair://..."

   # 方式 B：通过一次性 6 位 SPAKE2 配对码（经信令协商，口令不落网）
   ferry pair --code 482910 --wait   --signal wss://your-server.example/signal
   ferry pair --code 482910 --with <对端64位设备ID> --signal wss://your-server.example/signal
   ```

### 三、命令行常用收发

#### 1. 局域网直连传输
```bash
# 接收端（开启全接口监听并发送局域网广播发现）
ferry recv --lan --out ./recv

# 或指定固定监听地址
ferry recv --listen 0.0.0.0:18800 --out ./recv

# 发送端（自动发现局域网内接收方）
ferry send --discover --to "MyPC" --file ./photo.png --file ./archive.zip

# 或直连已知地址（同一局域网、热点或本机环回）
ferry send --addr 192.168.1.23:18800 --file ./archive.zip
```

#### 2. 跨网 P2P 打洞 + 中继自动选路（共享桌面/守护内核）
```bash
# 接收端上线等待传输（支持打洞与中继自动竞速 + 后台生日悖论热升级）
ferry recv --signal wss://your-server.example/signal --stun your-server.example:40000,your-server.example:40001 --out ./recv

# 发送端默认启用「UDP 打洞优先 → 中继 + 后台热升级」（本机桌面端或 ferry daemon 在线时自动复用长连）
ferry send --to-id <对端64位设备ID> --file ./video.mp4

# 无头 Linux / 服务器常驻守护进程（接收局域网 + 跨网传输，并暴露本机 IPC）
ferry daemon --dir ./downloads
```

#### 3. 临时快传与局域网浏览器收发
```bash
# 终端内启动局域网接收网页并打印二维码（手机浏览器扫码即传）
ferry recv --web --qr --out ./recv

# 发起免口令临时快传（生成 6 位码 + /s/<token> 链接 + 二维码）
ferry share --send ./archive.zip --base https://your-server.example

# 等待陌生设备上传文件，或通过 6 位码加入他人临时快传
ferry share --recv --out ./recv
ferry share join 123456 --out ./recv
```

> [!TIP]
> 命令行 `send` / `recv` 成功后会在 `stdout` 输出单行 JSON 摘要（包含 `ok`、`path`、`files`、`bytes`、`chunks`、`duration_ms`）；追加 `--log events.jsonl` 可记录结构化传输与选路事件。

---

## AI 智能体集成（MCP Server）

`ferry mcp` 实现了标准换行分隔的 JSON-RPC 2.0 **Model Context Protocol (stdio)** 服务端。默认自动共享 `%LOCALAPPDATA%\Ferry`（非 Windows 为 `~/.ferry`，并自动迁移旧版 `ft` / `.ft` 目录），若本机桌面端或 `ferry daemon` 正在运行则优先通过本机 IPC 复用在线信令与局域网缓存，让 AI 助手直接把生成的文件、长文本或构建产物发送到你的手机或电脑。

### 客户端配置示例

```json
{
  "mcpServers": {
    "ferry": {
      "command": "ferry",
      "args": [
        "mcp",
        "--signal", "wss://your-server.example/signal",
        "--stun", "your-server.example:40000,your-server.example:40001"
      ]
    }
  }
}
```

### 暴露的 MCP 工具

| 工具名 | 作用 |
|---|---|
| `ferry_whoami` | 获取本机设备 ID、名称、新生成的配对 URI 及当前信令/密钥路径 |
| `ferry_list_peers` | 列出已配对信任设备，并支持扫描局域网活跃接收端 (`discover_lan`) |
| `ferry_send_files` | 向已配对设备或 `host:port` 发送一个或多个本地文件（支持 `auto` / `lan` / `punch` / `relay`） |
| `ferry_send_text` | 将 UTF-8 文本封装为 `.txt` 文件直接摆渡至目标设备 |
| `ferry_receive_once` | 等待接收一次传入文件并保存至指定目录（默认 `auto`，支持 `race` / `punch` / `relay` / `lan`） |
| `ferry_pair_add` | 解析并信任对端的 `ferry://` 配对二维码或 `ftpair://` URI |

---

## 私有化服务端部署（`ft-server`）

`ft-server` 是无状态、全内存运行的轻量级信令/中继/STUN 服务端，默认通过 `-memlimit`（默认 `180 MiB`）严控内存占用，适合运行在低配云主机上。

### 1. 启动参数

```bash
ft-server \
  -listen 127.0.0.1:18790 \
  -stun 0.0.0.0:40000,0.0.0.0:40001 \
  -memlimit 188743680 \
  -log /var/log/ft-server/events.jsonl
```

| 参数 | 默认值 | 说明 |
|---|---|---|
| `-listen` | `127.0.0.1:18790` | HTTP/WebSocket 监听地址（建议仅监听回环并交由 Nginx / Caddy 等反代提供 TLS） |
| `-stun` | `""` | 逗号分隔的双 UDP STUN 监听地址（如 `0.0.0.0:40000,0.0.0.0:40001`）；需在防火墙放行对应 UDP 端口 |
| `-memlimit` | `188743680` (180 MiB) | Go 运行时软内存上限（字节，`0` 表示不限制） |
| `-log` | stderr | 结构化 JSONL 日志输出路径 |

> 环境变量 `FT_WEB_PASSWORD`（可选）：若设置，可启用服务端密码保护入口的校验口令；不设置则仅开放匿名签名临时房间与设备配对信令/中继能力。

### 2. 反向代理与防火墙要点

- **WebSocket 端点**：`/signal`、`/relay`、`/web/` 均需启用 `Upgrade` / `Connection: upgrade`，并关闭代理缓冲（`proxy_buffering off;`）及调大读写超时（参考仓库内置示例 [`deploy/nginx/ft-proxy.conf`](deploy/nginx/ft-proxy.conf) 与 [`deploy/ft-server.service`](deploy/ft-server.service)）。
- **Web 与健康检查端点**：
  - 页面与 API：`/`、`/transfer`、`/app`、`/s/`、`/api/`
  - 运维观测：`/healthz`（存活检查）、`/metrics`（返回在线设备数、中继房间数、累计转发字节、Goroutine 与堆内存 JSON；建议在反代层限制为仅内网访问）。
- **UDP 端口**：若启用 `-stun`，请在云安全组与主机防火墙放行对应的 UDP 端口（如 `40000-40001/udp`），并确保流量直连服务器公网 IP（不要经过仅支持 HTTP 的 CDN 代理）。
- **客户端指向自建服务端**：
  - CLI / MCP：`--signal wss://your-server.example/signal --stun your-server.example:40000,your-server.example:40001`
  - Windows 桌面端：`ferry-desktop.exe -signal wss://your-server.example/signal -stun your-server.example:40000,your-server.example:40001 -web https://your-server.example`

---

## 从源码构建

### 环境要求

- **Go**：`1.27.0`（见 [`go.mod`](go.mod)）
- **Windows 安装包与右键菜单**：MinGW-w64 `gcc`（用于将 [`cmd/ft-shellmenu/ftmenu.c`](cmd/ft-shellmenu/ftmenu.c) 编译为 `ftmenu.dll`）
- **Android APK**：JDK 17、Android SDK (`platforms;android-34`、`build-tools;34.0.0`)、NDK r27c (`27.2.12479018`)、`gomobile` 与 `gobind`（无 Gradle 依赖）

### 构建与测试命令

```bash
# 运行完整静态检查与单元测试
go vet ./...
go test -count=1 ./...

# 构建 Linux CLI 与服务端
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o out/ferry-linux-amd64 ./cmd/ferry
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o out/ferry-server-linux-amd64 ./cmd/ft-server
```

```powershell
# 构建 Windows CLI 与桌面端 GUI
go build -trimpath -o out/ferry-windows-amd64.exe ./cmd/ferry
go build -trimpath -ldflags="-H=windowsgui" -o out/ferry-desktop-windows-amd64.exe ./cmd/ft-desktop
```

```bash
# 构建 Android APK（arm64-v8a，自动应用 16 KB 页对齐并签名）
./android/build.sh
```

> 完整的跨平台 CI/CD 流水线（Windows 安装包合成、Linux 二进制及 Android 发布签名）见 [`.github/workflows/release.yml`](.github/workflows/release.yml)。

---

## 目录结构

```text
.
├── android/                  # 原生 Android 外壳（Java、Manifest、资源与无 Gradle 构建脚本）
├── cmd/
│   ├── ferry/                # 跨平台 CLI、daemon、临时快传、配对与 MCP Server
│   ├── ft-desktop/           # Windows 桌面端（托盘、主界面、投放面板、IPC 服务、Acrylic 弹窗）
│   ├── ft-msix-stub/         # Windows 右键菜单扩展辅助桩程序
│   ├── ft-probe/             # 网络与 STUN 探测调试工具
│   ├── ft-server/            # 信令、中继、匿名快传门户与 STUN 服务端入口
│   ├── ft-setup/             # Windows 安装与卸载向导（内嵌桌面端、CLI 与右键扩展）
│   └── ft-shellmenu/         # Windows 资源管理器 IExplorerCommand C 扩展实现
├── core/
│   ├── client/               # 桌面/CLI 统一自动选路、打洞/中继竞速与后台热升级
│   ├── discovery/            # 局域网 UDP 广播/组播自动发现与网卡拓扑识别
│   ├── identity/             # Ed25519 设备身份、DPAPI 密钥封装、信任列表与 TLS 钉定证书
│   ├── ipc/                  # 本机回环 Bearer 鉴权 IPC 通道（桌面端/daemon 与 CLI/MCP/右键共享）
│   ├── lanweb/               # 局域网浏览器点对点收发小站
│   ├── mobile/               # 供 gomobile 绑定的 Android Go 核心接口与事件回调
│   ├── pairing/              # ftpair:// URI 编解码与 SPAKE2 一次性 6 位配对协议
│   ├── pathmgr/              # TCP / QUIC 路径管理与连接竞速框架
│   ├── proto/                # 帧格式、传输清单校验、错误码与金标向量测试
│   ├── punch/                # STUN 客户端、UPnP/NAT-PMP/PCP 映射与 FTBP 生日悖论打洞
│   ├── relay/                # 高吞吐 WebSocket 中继连接包装（写合并、立即超时取消）
│   ├── session/              # 内外层会话握手与能力协商
│   ├── signaling/            # WebSocket 信令客户端与消息信封
│   ├── tempshare/            # 临时链接 / 6 位取件码托管与浏览器直连/中继协调
│   └── transfer/             # 4 MiB 滑动窗口传输引擎、SHA-256 校验与 .ftpart 断点续传
├── deploy/                   # systemd 单元、Nginx 反代片段、logrotate 与 MSIX 清单模板
├── docs/
│   ├── spec/protocol.md      # 轻渡线协议规范与二进制金标向量
│   └── security-checklist.md # 安全设计自查清单
├── server/                   # ft-server 服务端核心实现与内嵌 Web 资源
└── site/                     # 静态产品展示页
```

---

## 更多文档

- 协议与线格式规范：[`docs/spec/protocol.md`](docs/spec/protocol.md)
- 安全自查清单：[`docs/security-checklist.md`](docs/security-checklist.md)
- 变更日志：[`CHANGELOG.md`](CHANGELOG.md)
- 协作导航索引：[`PROJECT_INDEX.md`](PROJECT_INDEX.md)
