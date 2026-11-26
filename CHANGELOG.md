# 变更日志

## 1.0.0 — 2026-11-26

首个正式开源版本（采用 **AGPL-3.0** 协议发布）：

- **Windows 桌面矢量图标与界面精简**：Windows 桌面端全面替换为纯 Go 解析式 SDF 抗锯齿矢量图标绘制（`cmd/ft-desktop/icons.go`），并将「关于」页面整合进「设置」，使 Windows 与 Android 统一为「传输 / 设备 / 设置」三段式导航。
- **设备名称识别与回显统一**：Android 端统一优先读取系统自定义设备名（`Settings.Global.DEVICE_NAME`）并规范 UTF-8 截断；Windows 桌面端发送完成状态栏与拖放面板气泡统一优先展示接收方回报的对端设备名称。
- **三档自适应传输与大文件热升级**：默认「局域网直连 → UDP/QUIC 打洞（含 STUN / UPnP / NAT-PMP / PCP / 对称 NAT 生日悖论探测）→ 私有 WSS 加密中继」，支持 4 MiB 滑动窗口与 `.ftpart` 断点续传、SHA-256 整文件校验。
- **文件夹层级与网页临时快传**：支持安全的嵌套文件夹目录树传输、`/transfer` `/app` `/s/<token>` 浏览器分块流式上传下载与暂停/恢复/取消，并支持「同 Wi-Fi 极速直连」切换。
- **AI 智能体与全端联动**：内置 `ferry mcp`（stdio JSON-RPC 2.0 MCP Server）及本机回环 IPC，支持 CLI、MCP、桌面端与右键菜单自动复用在线长连与配对表。

## 1.0.0-beta.37 — 2026-11-26

功能补齐与历史标识清理版本：

- **文件夹层级传输**：`core/proto` `Manifest` 支持安全 POSIX `/` 相对路径（单段 ≤ 255 B、总长 ≤ 1024 B，空文件夹跳过，单文件保持旧版兼容）；Windows 桌面端、`core/mobile` 与 Android 均支持嵌套目录收发及取消后 `.ftpart` 空目录清理。
- **浏览器与临时快传流式及暂停/取消**：`core/lanweb` 新增分块流式上传接口；`core/tempshare` 支持 `get`/`put` 暂停、恢复与取消而不中断 WebSocket；`server/assets/app.html` 改为 `file.slice` 流式上传与 `showSaveFilePicker` + 纯 JS 增量 SHA-256 流式下载；桌面端临时快传窗补齐暂停/取消控制。
- **自定义服务器设置 UI**：Windows 桌面与 Android 设置页支持配置信令 WSS、临时快传 HTTPS 与 STUN 服务器列表，支持一键恢复默认并即时重连。
- **包名与历史 `ft` 标识清理**：Android 包名正式迁移至 `cn.cwlindean.ferry`（构建输出 `ferry-android-arm64.apk`）；`cmd/ft-setup`、`cmd/ft-shellmenu/ftmenu.c` 迁移注册表至 `HKCU\Software\Ferry` 并注入 CI 版本号；`site/index.html` 同步清理旧 `ft` 示例。
- **已知限制**：IPv6 直连探测在本阶段暂未实现，跨网直连仍基于 IPv4 打洞与 WSS 中继。

## 0.8.0 — 2026-11-09

首个可安装版本。Windows 托盘/投放小窗/主界面、安卓 App、私有信令与中继服务器。

- 传输：分块（默认 64 KiB）、整文件 SHA-256、`.ftpart` 断点续传（含切网自动续传）。
- 路径：局域网直连（热点/Wi-Fi）、UDP 打洞（双端口 STUN `40000-40001`）、服务器中继；发送端 t=0 三档竞速自动选路，失败回退中继。
- 安全：Ed25519 设备身份 + TLS 1.3 证书钉定；6 位码 PAKE 配对；信令登录挑战签名与按 IP 限频；私钥 DPAPI 封装（Windows）。
- 浏览器：HTTPS 伪装门户页 + 口令解锁 + 6 位码发现；LAN 直连走设备自己的 `http://<ip>:<port>/<token>/`，公网页面经中继。
- Windows：`ferry.exe`（CLI）、`ferry-desktop.exe`（托盘、投放小窗、原生主界面、开机自启、发送到）、`ferry-setup.exe`（安装/卸载）。
- 安卓：接收/发送、配对、分享菜单、前台服务保活；发布密钥签名。
- 服务器：`/metrics` 指标（在线数、中继字节、内存、协程）与每日日志轮转。

已知限制：校园网/企业 Wi-Fi 常过滤客户端广播、组播乃至客户端间 UDP（打洞会失败并自动回退中继）；对称 NAT 下反向打洞可能失败；浏览器端在 HTTPS 页面不直连 LAN。
