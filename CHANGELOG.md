# 变更日志

## 1.0.0-beta.37 — 2026-11-26

功能补齐与历史标识清理版本：

- **文件夹层级传输**：`core/proto` `Manifest` 支持安全 POSIX `/` 相对路径（单段 ≤ 255 B、总长 ≤ 1024 B，空文件夹跳过，单文件保持旧版兼容）；Windows 桌面端、`core/mobile` 与 Android 均支持嵌套目录收发及取消后 `.ftpart` 空目录清理。
- **浏览器与临时快传流式及暂停/取消**：`core/lanweb` 新增分块流式上传接口与 `#tx-pause`；`core/tempshare` 支持 `get`/`put` 暂停、恢复与取消而不中断 WebSocket；`server/assets/app.html` 改为 `file.slice` 流式上传与 `showSaveFilePicker` + 纯 JS 增量 SHA-256 流式下载；桌面端临时快传窗补齐暂停/取消控制。
- **自定义服务器设置 UI**：Windows 桌面与 Android 设置页支持配置信令 WSS、临时快传 HTTPS 与 STUN 服务器列表，支持一键恢复默认并即时重连。
- **极简「关于」页**：Windows 桌面端与 Android 均新增第 4 导航页「关于」（品牌徽标、版本与 GitHub 仓库链接）。
- **包名与历史 `ft` 标识清理**：Android 包名正式迁移至 `cn.cwlindean.ferry`（构建输出 `ferry-android-arm64.apk`）；`cmd/ft-setup`、`cmd/ft-shellmenu/ftmenu.c` 迁移注册表至 `HKCU\Software\Ferry` 并注入 CI 版本号；`site/index.html` 同步清理旧 `ft` 示例。
- **已知限制**：IPv6 直连探测在本阶段暂未实现，跨网直连仍基于 IPv4 打洞与 WSS 中继。

## 0.8.0 — 2026-11-09

首个可安装版本。Windows 托盘/投放小窗/主界面、安卓 App、BJ 信令与中继服务器。

- 传输：分块（默认 64 KiB）、整文件 SHA-256、`.ftpart` 断点续传（含切网自动续传，实测 200 MB `attempts=9`）。
- 路径：局域网直连（热点/Wi-Fi）、UDP 打洞（STUN `123.57.253.208:40000-40001`）、服务器中继；发送端 t=0 三档竞速自动选路，失败回退中继。
- 安全：Ed25519 设备身份 + TLS 1.3 证书钉定；6 位码 PAKE 配对；信令登录挑战签名与按 IP 限频；私钥 DPAPI 封装（Windows）。
- 浏览器：`https://ft.cwlindean.cn/` 伪装页 + 口令解锁 + 6 位码发现；LAN 直连走设备自己的 `http://<ip>:<port>/<token>/`，公网页面经中继（HTTPS 页面无法直连 LAN HTTP）。
- Windows：`ft.exe`（CLI）、`ft-desktop.exe`（托盘、投放小窗、原生主界面、开机自启、发送到）、`ft-setup.exe`（安装/卸载）。
- 安卓：接收/发送、配对、分享菜单、前台服务保活；`ft-0.8.0.apk` 用发布密钥签名（密钥不入库）。
- 服务器：`/metrics` 指标（在线数、中继字节、内存、协程）与每日日志轮转。

已知限制：校园网/企业 Wi-Fi 常过滤客户端广播、组播乃至客户端间 UDP（打洞会失败并自动回退中继）；对称 NAT 下反向打洞可能失败；浏览器端在 HTTPS 页面不直连 LAN。
