# 变更日志

## 1.0.2-beta.4 — 2026-11-27

- **桌面端目标设备药丸自适应宽度与 105% 上限**：`cmd/ft-desktop/mainwin.go` 改用与实际渲染一致的 Win32 `DrawTextW(DT_CALCRECT|DT_SINGLELINE|DT_NOPREFIX)` 精确测宽（按选中加粗态 `w.fontBold` / 未选中 `w.fontSm` 与对称内边距计算，并加上 `dtNoPrefix` 防 `&` 转义），短设备名自动收紧、`REDMI K90 Ultra` 宽度以内零省略、超长设备名准确截断在 `"REDMI K90 Ultra"` 宽度的 105% 上限，同时分离 `· 局域网` 后缀避免互相挤占截断。
- **Win32 GDI 防截断与上限单元测试**：新增 `TestTargetPillAdaptiveWidthAndK90UCap`，覆盖 `96 DPI` 与 `144 DPI` 缩放下短名称、标准名称与超长名称的实际文本截断判定。

## 1.0.2-beta.3 — 2026-11-26

- **修复桌面端对端设备名称未及时刷新并加固 Android 友好名称读取**：Windows 主界面在激活/显示及传输握手后（含取消/失败路径与 IPC 发送）自动重载配对表以立即同步最新设备名，同时加宽目标设备药丸最大宽度；Android 端 `defaultDeviceName` 增加 `bluetooth_name` / `device_name` / `market_name` 多源回退，避免回退为硬件型号码（如 `M...`），并统一 `MainActivity` 与 `TransferService` 的配置读写。
- **优化 Android 传输记录卡片四边等宽内边距**：将记录列表卡片与首页「最近一条」卡片统一调整为四边 `16dp` 等宽内边距（含同心圆角点击波纹与齐平分隔线），解决文件名与操作标签贴近左右边框的问题。

## 1.0.2-beta.2 — 2026-11-26

修复 `v1.0.2-beta.1` 的两个严重问题：

- **修复 Windows 桌面端启动死锁导致界面无法打开及无法接收文件**：`triggerLANScan` 持有 `app.mu` 期间递归调用 `deviceName()` 造成自死锁，阻塞主窗口创建、信令上线与局域网接收；现已解除锁内递归调用并补充单元测试 `TestTriggerLANScanNoDeadlock`。
- **修复 Android 首页主发送按钮布局错位**：修正 `buildHomePage` 中主按钮文字区与图标容器误挂载到父级垂直容器的问题，使「发照片 / 文件」主按钮、目标状态点与上传图标在深浅色模式下正常排版显示。

## 1.0.2-beta.1 — 2026-11-26

界面与交互重构测试版（面向核心「发送文件」场景优化）：

- **Windows 桌面端单焦点发射台重构**：首页移除常驻历史分栏改为全宽发送主舞台；顶部新增横向目标设备选择药丸（带局域网/中继在线状态点与智能默认选中）及统一 `+ 临时扫码传` 入口；主区提供虚线拖放区与「选择文件并发送 / 发文件夹 / 发文字」居中操作行；信令提示栏改为渐进式披露，正常在线时隐藏、仅断连异常时以琥珀色提示条展示并支持一键重试。
- **Windows 智能剪贴板截图一键发送**：新增 24/32 位 DIB 位图转 PNG 解码与非阻塞剪贴板探测，首页底部提供独立智能剪贴板发送条，支持一键将剪贴板截图或文本发送至默认目标设备。
- **Android 悬浮胶囊四栏导航与单手发送优化**：底部导航升级为「传输 / 记录 / 设备 / 设置」四栏悬浮胶囊并带未读记录绿点；首页传输进度卡置顶，发送区重构为全宽主按钮「发照片 / 文件」+ 紧凑三键行（「发剪贴板 / 发文字 / 发文件夹」）+ 智能剪贴板一键发送条 + 底部最近一条摘要；启动时静默发现局域网设备，移除设置页重复的临时快传入口。
- **独立「传输记录」页面**：Windows 左侧导航与 Android 底部胶囊均新增独立「记录」标签页承载完整记录与筛选，首页仅保留单行最近动态与快速入口。

## 1.0.1 — 2026-11-26

- **新增 Android 32 位 (`armeabi-v7a`) 安装包**：CI 与本地构建脚本同步产出 `ferry-android-arm64.apk`（`arm64-v8a`，16 KB 页对齐）与 `ferry-android-armv7a.apk`（`armeabi-v7a`），兼容更多老旧 32 位 Android 设备。
- **32 位 ARM 编译兼容性修复**：将 `core/lanweb.MaxUpload` 显式定义为 `int64`，并在 CI 测试任务增加 `linux/arm GOARM=7` 的 `go vet` 检查。

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
