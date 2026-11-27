<div align="center">

<img src="./deploy/msix/Assets/Square150x150Logo.png" alt="轻渡 Ferry" width="128" height="128" />

# 轻渡 · Ferry

**面向自有设备、浏览器与 AI 智能体的端到端加密跨平台文件摆渡工具**

<p align="center">
  <a href="./LICENSE">
    <img src="https://img.shields.io/github/license/zhitiaojun/ferry?color=brightgreen" alt="license">
  </a><!--
  --><a href="https://github.com/zhitiaojun/ferry/releases/latest">
    <img src="https://img.shields.io/github/v/release/zhitiaojun/ferry?color=brightgreen" alt="release">
  </a><!--
  --><a href="./go.mod">
    <img src="https://img.shields.io/badge/Go-1.27+-00ADD8?logo=go&logoColor=white" alt="Go">
  </a><!--
  --><a href="https://www.aliyun.com/product/esa" target="_blank">
    <img src="https://img.shields.io/badge/Aliyun%20ESA-%E8%BE%B9%E7%BC%98%E5%AE%89%E5%85%A8%E5%8A%A0%E9%80%9F-FF6A00?logo=alibabacloud&logoColor=white" alt="Aliyun ESA">
  </a><!--
  --><a href="https://github.com/zhitiaojun/ferry/stargazers" target="_blank">
    <img src="https://img.shields.io/github/stars/zhitiaojun/ferry?style=flat&color=orange" alt="GitHub Stars">
  </a>
</p>

<p align="center">
  <a href="#capabilities">核心能力</a> •
  <a href="#platforms">全端矩阵</a> •
  <a href="#quick-start">快速开始</a> •
  <a href="#mcp">MCP 集成</a> •
  <a href="#deployment">私有部署</a> •
  <a href="#development">源码构建</a> •
  <a href="#license">开源协议</a>
</p>

</div>

---

## 📝 项目说明

**轻渡（Ferry）** 是一款专注于「我的手机 ↔ 我的电脑 ↔ 浏览器 ↔ AI 智能体」之间的跨平台文件与文本传输工具。无需注册账号、不经过第三方网盘压缩或留存，基于单一 Go 核心实现 **局域网直连 → UDP 打洞 P2P（QUIC）→ 加密中继** 三档链路自动竞速调度，并在底层内置原生 stdio **MCP Server**，让 AI 助手可直接将生成产物或文本发送至你的设备。

> [!NOTE]
> - 当前正式版本：`v1.0.2`（协议版本 `v1`，采用 **AGPL-3.0** 协议开源）。
> - 默认预置公共信令与中继节点，开箱即用；你也可以在客户端「设置」或命令行参数中一键切换至自建服务端。
> - 当前跨网直连与穿透基于 IPv4（LAN / UDP 打洞 / WSS 中继），IPv6 直连探测将在后续版本规划支持。

---

<a id="capabilities"></a>

## ✨ 核心能力

| 能力领域 | 说明 |
| --- | --- |
| **三档自适应选路** | 发送端 `t = 0` 自动识别并调度「局域网直连 → UDP/QUIC 打洞 → WSS 加密中继」；≥ 4 MiB 传输过程中持续尝试后台 P2P 打洞，打通后经 `.ftpart` 无损热切换直连 |
| **NAT 穿透与抗干扰** | 内置标准 STUN 探测、纯 Go UPnP IGD / NAT-PMP / PCP 网关端口映射，以及面向对称 NAT 的 `FTBP` 生日悖论探测；支持识别并过滤虚拟网卡（TUN）确保可靠回退 |
| **高吞吐与断点续传** | 采用 `256 KiB` 分块、`4 MiB` 并发滑动窗口与 WebSocket 写合并流水线，全内存流式读写；传输中断保留 `.ftpart` 进度并在重连后自动续传，落盘前执行整文件 **SHA-256** 校验 |
| **端到端安全与零知识中继** | 本地生成 Ed25519 设备身份（Windows 下使用 DPAPI 密封存储），公网打洞与中继会话内层均嵌套双向公钥钉定（Pinning）的 **TLS 1.3** 隧道，中继节点仅拼接密文字节、不存盘 |
| **免账号双模式配对** | 支持扫码 / 导入 `ftpair://` 凭证，以及一次性 6 位 **SPAKE2** 配对码（口令仅在本地完成零知识密钥协商，不落网明文传递） |
| **免安装临时网页快传** | 支持生成 10 分钟有效的 HTTPS 二维码与 6 位口令「一码双用」：客户端扫码直连，或用任意手机/桌面浏览器打开即可分块流式上传下载，支持暂停/恢复/取消与同 Wi-Fi 直连切换 |
| **AI 智能体原生接入** | 通过 `ferry mcp` 暴露标准 JSON-RPC 2.0 MCP 工具集，并经本机回环 IPC 自动复用桌面端或 `ferry daemon` 长连状态与配对列表 |

```text
发送端发起传输
  ├─[1] 同网段局域网直连 ──▶ UDP 广播/组播发现（或手动地址），零服务器流量
  ├─[2] 跨网 UDP 打洞 P2P ──▶ STUN + UPnP/NAT-PMP/PCP + 对称 NAT 生日悖论探测（600 ms 快速探测）
  │                              └─▶ 内层双向认证 TLS 1.3 over QUIC（低于 5 Mbps 自动保底回退中继）
  └─[3] 私有 WSS 加密中继 ──▶ 服务端零知识字节拼接（4 MiB 滑动窗口 / 256 KiB 分块 / 写合并流水线）
                                 └─▶ ≥ 4 MiB 批次传输期间后台持续尝试 P2P 打洞，打通后 .ftpart 无损热切换直连
```

---

<a id="platforms"></a>

## 📱 全端矩阵

| 端 / 组件 | 产物 / 入口 | 核心特性 |
| --- | --- | --- |
| **Windows 桌面端** | `ferry-setup-windows-amd64.exe` | 纯 Win32 GDI + SDF 矢量图标 + DWM Acrylic 弹窗（零 WebView2 依赖）、单焦点发射台与自适应目标设备药丸、托盘常驻、拖拽浮窗投放、Windows 11 一级右键菜单（`ftmenu.dll`）、文件/文件夹/剪贴板截图文本互传、自定义服务器配置与独立传输台账 |
| **Android 原生端** | `ferry-android-arm64.apk` / `ferry-android-armv7a.apk` | 原生 Java（Material Design 3 + 四栏悬浮胶囊）+ `gomobile` Go 核心（`cn.cwlindean.ferry`，API 21+，`arm64-v8a` 支持 16 KB 页对齐 + 兼容 32 位 `armeabi-v7a`），支持文件/文件夹树发送、智能剪贴板一键发送、系统「分享」面板接入、扫码配对与公共 `Download/Ferry` 落盘 |
| **浏览器临时快传** | `/transfer` · `/app` · `/s/<token>` | 免装客户端一码双用：`file.slice` 分块流式上传、`showSaveFilePicker` + 纯 JS 增量 SHA-256 流式下载、暂停/恢复与取消（不中断控制信道），支持一键切换同 Wi-Fi 直连 |
| **跨平台 CLI & MCP** | `ferry`（`./cmd/ferry`） | Windows / Linux / macOS 命令行收发、常驻守护进程（`ferry daemon`）、临时快传（`ferry share`）与标准 stdio **MCP Server**（`ferry mcp`） |
| **信令与中继服务端** | `ferry-server-linux-amd64`（`./cmd/ft-server`） | 单二进制、全内存状态、内置 `-memlimit`；集成 WebSocket 信令（`/signal`）、加密中继（`/relay`）、匿名快传门户与双端口 UDP STUN 服务 |

---

<a id="quick-start"></a>

## 🚀 快速开始

### 1. 安装客户端

从 [GitHub Releases](https://github.com/zhitiaojun/ferry/releases/latest) 下载对应平台的最新正式构建产物：

- **Windows**：下载并运行 `ferry-setup-windows-amd64.exe`，自动安装桌面端、`ferry.exe` CLI、开机自启与右键菜单；如需卸载可执行：
  ```powershell
  ferry-setup-windows-amd64.exe -uninstall
  ```
- **Android**：现代设备下载安装 `ferry-android-arm64.apk`（`arm64-v8a`），老旧 32 位设备可选 `ferry-android-armv7a.apk`（`armeabi-v7a`，均支持 Android 5.0+）。首次使用建议按引导授予存储/通知/相机权限，并在省电设置中放行后台保活以减少休眠断连。
- **Linux / macOS CLI**：下载 `ferry-linux-amd64` 或通过源码编译后放入 `PATH`。

### 2. 设备配对

- **图形界面配对**：在桌面端或 Android 的「设备」页生成二维码 / `ftpair://` 链接或 6 位一次性配对码，对端扫码或输入配对码即可完成双向公钥信任绑定。
- **命令行配对**：

```bash
# 查看本机设备 ID 与公钥
ferry whoami

# 方式 A：通过配对 URI
ferry pair --show --name "MyPC"
ferry pair --add "ftpair://..."

# 方式 B：通过一次性 6 位 SPAKE2 配对码（口令不落网）
ferry pair --code 482910 --wait --signal wss://your-server.example/signal
ferry pair --code 482910 --with <对端64位设备ID> --signal wss://your-server.example/signal
```

### 3. 命令行常用收发

<details>
<summary>点击展开 CLI 局域网直连、跨网选路与临时快传示例</summary>

```bash
# 1. 局域网直连收发
ferry recv --lan --out ./recv
ferry send --discover --to "MyPC" --file ./photo.png --file ./archive.zip
ferry send --addr 192.168.1.23:18800 --file ./archive.zip

# 2. 跨网 UDP 打洞 + 中继自动竞速（本机桌面端或 ferry daemon 在线时自动复用 IPC 长连）
ferry recv --signal wss://your-server.example/signal --stun your-server.example:40000,your-server.example:40001 --out ./recv
ferry send --to-id <对端64位设备ID> --file ./video.mp4
ferry daemon --dir ./downloads

# 3. 临时快传与局域网浏览器收发
ferry recv --web --qr --out ./recv
ferry share --send ./archive.zip --base https://your-server.example
ferry share --recv --out ./recv
ferry share join 123456 --out ./recv
```

> 命令行 `send` / `recv` 成功后将在 `stdout` 输出单行 JSON 摘要（含 `ok`、`path`、`files`、`bytes`、`chunks`、`duration_ms`）；追加 `--log events.jsonl` 可输出结构化日志。

</details>

---

<a id="mcp"></a>

## 🤖 AI 智能体集成（MCP Server）

`ferry mcp` 实现了标准换行分隔的 JSON-RPC 2.0 **Model Context Protocol (stdio)** 服务端。默认自动共享 `%LOCALAPPDATA%\Ferry`（非 Windows 为 `~/.ferry`），若本机桌面端或 `ferry daemon` 正在运行则优先通过本机 IPC 复用在线信令与局域网缓存。

### 客户端配置示例

```json
{
  "mcpServers": {
    "ferry": {
      "command": "ferry",
      "args": ["mcp"]
    }
  }
}
```

### 暴露的 MCP 工具

| 工具名 | 作用 |
| --- | --- |
| `ferry_whoami` | 获取本机设备 ID、名称、新生成的配对 URI 及当前密钥路径 |
| `ferry_list_peers` | 列出已配对信任设备，并支持扫描局域网活跃接收端（`discover_lan`） |
| `ferry_send_files` | 向已配对设备或 `host:port` 发送文件或文件夹（支持 `auto` / `lan` / `punch` / `relay`） |
| `ferry_send_text` | 将 UTF-8 文本封装为 `.txt` 文件直接摆渡至目标设备 |
| `ferry_receive_once` | 等待接收一次传入文件并保存至指定目录（默认 `auto`） |
| `ferry_pair_add` | 解析并信任对端的 `ferry://` 配对二维码或 `ftpair://` URI |

---

<a id="deployment"></a>

## ☁️ 私有化服务端部署（`ft-server`）

`ft-server` 全内存运行、无数据库依赖，默认 `-memlimit` 为 `180 MiB`，适合部署在轻量云主机上。

### 启动命令与参数

```bash
ft-server \
  -listen 127.0.0.1:18790 \
  -stun 0.0.0.0:40000,0.0.0.0:40001 \
  -memlimit 188743680 \
  -log /var/log/ft-server/events.jsonl
```

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `-listen` | `127.0.0.1:18790` | HTTP/WebSocket 监听地址（建议仅监听回环并由 Nginx / Caddy 反代提供 TLS） |
| `-stun` | `""` | 逗号分隔的双端口 UDP STUN 地址（如 `0.0.0.0:40000,0.0.0.0:40001`） |
| `-memlimit` | `188743680`（180 MiB） | Go 运行时软内存上限（字节，`0` 表示不限制） |
| `-log` | stderr | 结构化 JSONL 事件日志输出路径 |

> 可选环境变量 `FT_WEB_PASSWORD`：配置后开启服务端受密码保护网页入口；不设置则仅开放匿名临时快传房间与设备配对信令/中继服务。

### 反向代理与防火墙提示

- **WebSocket 路由**：`/signal`、`/relay`、`/web/` 需开启 `Upgrade` / `Connection "upgrade"`、关闭 `proxy_buffering off;` 并调大读写超时。
- **UDP STUN 直连**：云安全组与本机防火墙需放行对应 UDP 端口（如 `40000-40001/udp`），且 STUN 需直连服务器公网 IP，不经仅支持 HTTP 的 CDN。
- 完整 systemd 单元、日志轮转与 Nginx 配置模板见 [`deploy/README.md`](./deploy/README.md)、[`deploy/nginx/ferry.example.conf`](./deploy/nginx/ferry.example.conf) 与 [`deploy/ft-server.service`](./deploy/ft-server.service)。

---

<a id="development"></a>

## 🛠️ 源码构建与目录结构

### 环境要求

- **Go**：`1.27.0`（见 [`go.mod`](./go.mod)）
- **Windows 安装包与右键菜单**：MinGW-w64 `gcc`（用于编译 [`cmd/ft-shellmenu/ftmenu.c`](./cmd/ft-shellmenu/ftmenu.c) 为 `ftmenu.dll`）
- **Android APK**：JDK 17、Android SDK (`platforms;android-34`、`build-tools;34.0.0`)、NDK r27c (`27.2.12479018`)、`gomobile` 与 `gobind`（无 Gradle 依赖）

### 测试与构建命令

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
# 构建 Android APK（同时生成 arm64-v8a 与 armeabi-v7a 双架构安装包并签名）
VER_NAME=1.0.0 ./android/build.sh
```

<details>
<summary>点击查看项目目录结构</summary>

```text
.
├── .github/workflows/        # GitHub Actions 跨平台自动构建与 Release 发布流
├── android/                  # 原生 Android 客户端（Java MD3、Manifest、资源与无 Gradle 构建脚本）
├── cmd/
│   ├── ferry/                # 跨平台 CLI、daemon、临时快传、配对与 MCP Server
│   ├── ft-desktop/           # Windows 桌面端（托盘、主界面、投放面板、IPC、SDF 图标、Acrylic 弹窗）
│   ├── ft-msix-stub/         # Windows 右键菜单扩展辅助桩程序
│   ├── ft-probe/             # 网络与 STUN 探测调试工具
│   ├── ft-server/            # 信令、中继、匿名快传门户与 STUN 服务端入口
│   ├── ft-setup/             # Windows 安装与卸载向导
│   └── ft-shellmenu/         # Windows 资源管理器 IExplorerCommand C 扩展
├── core/
│   ├── client/               # 桌面/CLI 统一自动选路、打洞/中继竞速与后台热升级
│   ├── discovery/            # 局域网 UDP 广播/组播自动发现与网卡拓扑识别
│   ├── identity/             # Ed25519 设备身份、DPAPI 密钥封装、信任列表与 TLS 钉定证书
│   ├── ipc/                  # 本机回环 Bearer 鉴权 IPC 通道
│   ├── lanweb/               # 局域网浏览器点对点收发服务
│   ├── mobile/               # 供 gomobile 绑定的 Android Go 核心接口
│   ├── pairing/              # ftpair:// URI 编解码与 SPAKE2 一次性 6 位配对协议
│   ├── pathmgr/              # TCP / QUIC 路径管理与连接竞速框架
│   ├── proto/                # 帧格式、传输清单校验、错误码与金标向量测试
│   ├── punch/                # STUN 客户端、UPnP/NAT-PMP/PCP 映射与 FTBP 生日悖论打洞
│   ├── relay/                # 高吞吐 WebSocket 中继连接包装（写合并、立即超时取消）
│   ├── session/              # 内外层会话握手与能力协商
│   ├── signaling/            # WebSocket 信令客户端与消息信封
│   ├── tempshare/            # 临时链接 / 6 位取件码托管与浏览器直连/中继协调
│   └── transfer/             # 4 MiB 滑动窗口传输引擎、SHA-256 校验与 .ftpart 断点续传
├── deploy/                   # systemd 单元、Nginx 反代模板、logrotate 与 MSIX 清单
├── docs/
│   ├── spec/                 # 轻渡线协议规范与二进制金标向量
│   └── security-checklist.md # 安全设计自查清单
├── server/                   # ft-server 服务端核心实现与内嵌 Web 资源
├── site/                     # 静态产品展示页
└── tools/                    # 协议金标向量生成与右键扩展编译辅助脚本
```

</details>

---

## 🙏 特别鸣谢

<p align="center">
  <a href="https://www.aliyun.com/product/esa" target="_blank">
    <img src="https://img.shields.io/badge/Aliyun%20ESA-%E8%BE%B9%E7%BC%98%E5%AE%89%E5%85%A8%E5%8A%A0%E9%80%9F-FF6A00?style=for-the-badge&logo=alibabacloud&logoColor=white" alt="Aliyun ESA">
  </a>
</p>

<p align="center">
  <strong>感谢 <a href="https://www.aliyun.com/product/esa" target="_blank">阿里云边缘安全加速 ESA</a> 为本项目提供边缘加速、计算与安全保护</strong>
</p>

---

## 📚 更多文档

| 文档 / 入口 | 说明 |
| --- | --- |
| [`docs/spec/protocol.md`](./docs/spec/protocol.md) | 轻渡线协议规范与二进制金标向量 |
| [`docs/security-checklist.md`](./docs/security-checklist.md) | 安全设计与路径/限频/加密自查清单 |
| [`deploy/README.md`](./deploy/README.md) | `ft-server` 私有化服务端部署指南 |
| [`CHANGELOG.md`](./CHANGELOG.md) | 版本迭代与更新记录 |
| [GitHub Issues](https://github.com/zhitiaojun/ferry/issues) | 问题反馈与功能建议 |

---

<a id="license"></a>

## 📜 开源协议

本项目基于 [GNU Affero General Public License v3.0 (AGPL-3.0)](./LICENSE) 开源协议发布。

---

## 🌟 Star History

<div align="center">

[![Star History Chart](https://api.star-history.com/svg?repos=zhitiaojun/ferry&type=Date)](https://star-history.com/#zhitiaojun/ferry&Date)

</div>

---

<div align="center">

### 💖 感谢使用 轻渡 Ferry

如果这个项目对你有帮助，欢迎点亮右上角 ⭐️ Star 支持！

**[版本下载](https://github.com/zhitiaojun/ferry/releases)** • **[问题反馈](https://github.com/zhitiaojun/ferry/issues)** • **[协议规范](./docs/spec/protocol.md)**

</div>
