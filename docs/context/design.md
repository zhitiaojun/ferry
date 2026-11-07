# 系统设计（design）v0.2

> 状态：**建议，部分已被决策约束**。已确认的约束以 `DECISIONS.md` 为准（D1–D21）；本文件讨论“怎么做”，可被推翻。问题来源与理由见 `review.md`；里程碑见 `../../PLAN.md`。
> 更新：2026-11-07，吸收 review 意见与第二至四轮问卷（D12–D28）。实现由 work agent 执行，约束见 `../../WORK_AGENT.md`。

## 1. 总体架构

```
┌────────── Windows 客户端（Go 单 exe）──────────┐      ┌──── 安卓 APK ────┐
│ 托盘常驻 │ 投放小窗 │ 主界面(WebView) │ 右键菜单   │      │ Kotlin 外壳：UI、  │
│ ───────────── Go 核心（core/）──────────────── │      │ 分享菜单、前台服务 │
│ identity │ discovery │ signaling │ punch │ relay │      │ ── Go 核心 AAR ── │
│ session  │ transfer  │ pathmgr   │ lanweb(给浏览器)│      │ （同一份 core/）  │
└──────────────────────────────────────────────┘      └──────────────────┘
                 │ wss://ft.cwlindean.cn（443，经 ESA）│ UDP 直连 123.57.253.208:40000/40001
                 ▼                              ▼
        ┌──────── BJ：ft-server（Go，systemd，MemoryMax=256M）────────┐
        │ 信令（在线表、配对、候选交换） │ 中继（WSS 字节流拼接） │ STUN │
        │ Web 页（浏览器收发陌生文件）    │ 指标与日志                 │
        └───────────────────────────────────────────────────────────┘
```

Go 模块划分（D13）：

```
core/identity    设备身份：Ed25519 密钥、设备 ID、已配对设备表
core/discovery   局域网发现（UDP 广播 + 组播），带身份签名
core/signaling   与 ft-server 的 WSS 长连接：上线、在线状态、配对、候选交换、唤醒
core/punch       STUN、NAT 映射判定、IPv4 打洞、IPv6 直连
core/relay       中继客户端（WSS 内承载端到端加密流）
core/session     会话层：session_id、偏移确认、路径切换、续传
core/transfer    清单、分块、SHA-256、流式读写、.part 文件
core/pathmgr     三路竞速与升降级决策（只做决策，不碰 socket）
core/lanweb      局域网内给浏览器用的 HTTP 服务（陌生设备免安装）
cmd/ft           CLI（测试与证据采集）
cmd/ft-desktop   Windows 客户端
server/          ft-server（BJ）
android/         Kotlin 外壳 + gomobile 绑定
web/             浏览器页面（中继版由 ft-server 提供，局域网版由 lanweb 提供）
```

## 2. 身份与配对（自有设备的核心，D12/D19）

- 每台设备首次启动生成 Ed25519 身份密钥，设备 ID = 公钥哈希。私钥只存本机（Windows：DPAPI 加密；安卓：Keystore 包裹）。
- **一次配对**：电脑显示二维码（设备 ID + 公钥 + 一次性配对密钥），手机扫码；或输入 6 位码，经 ft-server 用 PAKE（SPAKE2）协商。成功后双方把对方公钥写入“已配对设备”表。
- **之后自动连接**：所有传输通道都用双向 TLS 1.3，证书为由身份密钥生成的自签证书，按已配对公钥**钉定**校验——LAN（TCP）、打洞（QUIC）、中继（WSS 里再套一层 TLS）一致，中继和服务器都看不到明文。
- ft-server 只做路由，不存配对关系：设备上线时用身份密钥签名挑战完成登录，服务器维护“设备 ID → 在线连接”。

> 与 D4 的关系：M1–M2 骨架可明文；M3 起配对需要密钥，建议届时直接启用 TLS（QUIC 本身也必须 TLS）。是否提前，M3 开工前确认。

## 3. 陌生设备（D20）

| 方式 | 流程 | 链路 |
|---|---|---|
| 浏览器链接 / 二维码 | 发送端生成链接 `https://ft.cwlindean.cn/r/<房间ID>#<密钥>`；`#` 后的密钥不会发给服务器，浏览器用 Web Crypto（ECDH/AES-GCM）端到端加解密 | 中继（WSS）；同局域网时优先 `http://<局域网IP>:<端口>`（lanweb） |
| 局域网附近设备 | 广播列出附近已安装 App 的设备，点选后对方弹窗确认 | LAN |
| 一次性会话码 | 6 位码 + PAKE 派生临时密钥，用后作废 | 三档竞速 |

陌生设备与自有设备共用会话层、传输层；区别只在“信任来自钉定公钥还是一次性密钥”。中继不区分限制（D20）。

## 4. 路径与会话层

四层结构（自下而上）：

```
路径层     LAN: TCP+TLS | 打洞: QUIC(IPv4/IPv6) | 中继: WSS 内 TLS
会话层     session_id、累计偏移确认、去重、路径切换
传输层     清单（多文件）、64 KiB 分块、每块 SHA-256、整文件 SHA-256
选路层     竞速、升级、降级
```

候选路径（按优先级）：

1. **LAN 直连**：地址来源三路并用——UDP 广播、组播、信令交换的局域网单播地址（学校网络常过滤广播但单播可能可达，R6）。
2. **IPv6 直连**：双方全局 IPv6，同时发包穿过有状态防火墙（学校教育网可能有 IPv6）。
3. **IPv4 打洞**：STUN 观测地址 + 同时发包（DCUtR 思路）；STUN 与 QUIC 共用同一 UDP socket；STUN 目标为 `123.57.253.208:40000/40001`（不经 ESA）。
4. **中继**：WSS 经 `ft.cwlindean.cn`（ESA）到 ft-server，按会话拼接。

**热点场景**（D24）：电脑连手机热点时，对端是网关地址；此时 LAN 必须成功；LAN 失败时**默认不让数据回退到中继**（否则手机蜂窝流量双倍消耗）——中继只保持信令，弹出提示，由用户手动确认后才走中继，绝不静默回退。

LAN 失败诊断：日志区分“未收到广播 / 组播”“单播不可达”“TLS 握手失败”，便于判断学校网络的隔离行为。

### 4.1 VPN（TUN）场景（D28）

- **网卡过滤**：`core/discovery` 与候选收集只用物理网卡（Wi-Fi / 以太网 / 蜂窝）；按网卡类型、名称（Wintun、utun、tun、Meta、Clash 等）与路由表识别 TUN，排除其虚拟地址。
- **绕过 VPN**：Windows 端直连 socket 绑定物理网卡（`IP_UNICAST_IF` 或绑定物理网卡源地址）；安卓端对直连 socket 调 `Network.bindSocket(底层 Wi-Fi / 蜂窝网络)`，若 VPN 不允许绕过则记录并放弃该路径。信令 / 中继保持走系统默认路由（可经 VPN），保证总能连上。
- **检测与诊断**：启动时及网络变化时记录 `vpn: {on, iface, fake_ip, stun_ip_is_vpn_exit}`；fake-ip 判据：`ft.cwlindean.cn` 解析为 198.18.0.0/15；VPN 出口判据：绑定物理网卡与走默认路由两次 STUN 结果不同。
- **策略**：检测到 VPN 且无法绕过时，打洞降为低优先级、不阻塞中继；IPv6 候选只取物理网卡上的全局地址。
- **MTU**：QUIC 初始包 1200 B，开启 PMTUD。
- **用户提示**：诊断页 / 日志给出“建议把本应用、`ft.cwlindean.cn`、`123.57.253.208` 加入 VPN 直连规则（安卓：分应用绕过）”，不以“关闭 VPN”作为唯一方案。

## 5. 竞速状态机

```
t=0     并发：LAN 发现 | 信令（已在线则 0 RTT）+ STUN | 中继建连（只握手不发数据）
t≤0.5s  LAN 通 → 用 LAN，取消其他（中继零流量）
t=0.5s  LAN 未通 → 中继开始承载数据（D3）
任意时刻 更优路径通过“可写 + 对端确认 + 实测吞吐不低于当前路径” → 按偏移量切流 → 关闭旧路径数据流
任意路径 3 s 无进展 → 降级到下一条可用路径
```

每次选路 / 切换写 JSONL：`{ts, session, event, from, to, offset, rtt_ms, mbps}`；ft-server 记录每会话中继字节数，作为“没有静默走中继”的反证（R7）。

## 6. 帧格式（草案）

```
magic(4) | version(1) | type(1) | flags(2) | session_id(16) | seq(8) | len(4) | payload(len)
```

- `type`：HELLO / HELLO_ACK / MANIFEST / DATA / ACK(offset) / RESUME(offset) / FIN / ERROR / PING
- 分块 64 KiB；QUIC/TCP 都是可靠流，不再在应用层切 1200 B。
- 规范与测试向量放 `docs/spec/protocol.md`（M0 产出），所有端以此做一致性测试。

## 7. 断点续传（D19，M7 实现，M1 预留）

- 接收端写 `<文件名>.ftpart` + 侧车清单（传输 ID、文件大小、整文件哈希、已确认分块位图）。
- 重连后接收端发 `RESUME(offset)`，并对已收部分逐块复核哈希；发送端从 offset 续传。
- 传输 ID = 发送端设备 ID + 文件路径 + 大小 + 修改时间的哈希；文件变化则作废续传。

## 8. Windows 客户端（D19）

| 功能 | 方案 | 风险 / 预研 |
|---|---|---|
| 托盘常驻 | Go 调 Win32 `Shell_NotifyIcon`；开机自启（注册表 Run 或启动文件夹） | 低 |
| **投放小窗**（示意：QQ 的“文件拖到这”） | 常驻置顶小窗（位置可配置）用 `DragAcceptFiles` 接收投放；拖放开始即自动弹出仅作可选增强（`WH_MOUSE_LL` + 探测 Shell 拖拽图像窗口） | **M2 预研已完成**（结论见 §8.1）：接收端可行且已出原型；“全局拖拽检测”无官方 API，不作为 v1 唯一方案 |
| 右键菜单 | 先做“发送到”菜单（SendTo 快捷方式，零注册表）+ 经典右键菜单项；Win11 一级菜单需 `IExplorerCommand` + 稀疏包身份，列为可选 | 中 |
| 主界面 | WebView2 承载 `web/` 同一套界面（Wails v3 或 go-webview2，M7 前预研定） | 中 |
| 接收 | 托盘常驻保持信令长连接，收到请求弹通知；已配对设备可设为自动接收 | 低 |

### 8.1 投放小窗预研结论（M2，2026-11-07）

预研原型：`cmd/ft-desktop/main.go`（Windows，433 行，无 cgo，仅 `golang.org/x/sys/windows` 与 `user32/shell32/gdi32` 懒加载），产出 4.3 MiB 单文件。已实现：置顶无边框工具窗、`DragAcceptFiles` + `WM_DROPFILES` 取路径、每投递输出一行 JSON（`{"event":"drop","ts","files":[...]}`）并可选追加日志、窗口内文字提示、`-size/-margin/-pos/-log` 参数。

**结论一：接收投放可行，可作为 v1 形态。** 资源管理器拖入时 `WM_DROPFILES` 正常触发。合成 HDROP 用例已覆盖中文、空格、引号、制表符、UNC 转义；2026-11-07 又在真机上用资源管理器拖入 `.tools/testdata/test-2.1m.bin`，终端输出一行 JSON（`{"event":"drop","ts":"2026-11-07T20:35:38+08:00","files":[...绝对路径...]}`），窗口文字同步变为「收到 1 个文件」（截图 `.tools/shots/drop-done.png`，证据 `evidence/stage1/20261107-2035.log`）。

**结论二：“拖拽开始即自动弹窗”不可靠，不做 v1 唯一方案。** Windows 没有“任意位置开始拖拽”的公开通知；`IDropTarget`/`DragAcceptFiles` 只在光标进入已注册窗口时才有事件。若强行实现，需 `WH_MOUSE_LL` 全局钩子 + 探测系统拖拽图像窗口，受多显示器/DPI、Explorer 版本、其他 shell 影响，脆弱且难排障。

**因此 v1 采用常驻形态**：可配置位置的小窗、屏幕边缘迷你条或托盘投放区（用户可拖动并记住位置）；自动弹出只作为后续实验性增强。

**实测限制（需在 M7 产品化时处理）**：

- **位置**：默认“右下角 16px”在真实桌面上容易被悬浮窗/流量球/任务栏小组件遮挡，实测中即使 `HWND_TOPMOST` 也会被同为置顶的悬浮窗压住 → 已加 `-pos X,Y` 参数（越界自动夹到工作区内）；v1 需位置可配置并持久化，默认避开右下角。
- **UIPI**：管理员权限进程与普通权限 Explorer 之间拖放会被系统静默拦截 → 安装包不应请求提权。
- **来源限制**：只有 shell `CF_HDROP` 拖拽触发 `WM_DROPFILES`；浏览器、部分压缩软件的 OLE 拖拽不触发，需要备用路径（如“发送到”菜单）。
- **DPI**：`SetProcessDPIAware` 下 `-size/-margin` 为物理像素；多显示器不同缩放需 Per-Monitor v2 才能一致。
- **退出**：预研原型无关闭按钮（测试用 `taskkill`），正式版由托盘菜单退出。



## 9. 安卓客户端（D6、D19）

- 目标设备：小米 22081212C，HyperOS，**Android 17 / SDK 37**（来自 battery-alert 档案）。
- 后台接收：前台服务（常驻通知）保持 WSS 长连接；心跳间隔取“蜂窝 NAT 超时”与“ESA 空闲超时”中较小者（M3 实测，初值 30 s）；必须引导用户打开 HyperOS「自启动」与「省电策略：无限制」（battery-alert 已验证此为必要条件）。
- 前台服务类型、Android 17 的本地网络权限要求、`MulticastLock`：M2 内实测确定（R10）。
- 分享菜单：`ACTION_SEND` / `SEND_MULTIPLE`；为每台已配对设备发布 Direct Share 快捷方式，实现“一点就发到电脑”。
- 构建：在 Windows 开发机构建（gomobile + Gradle 或手工工具链），**不在 BJ 上构建**。

## 10. ft-server（BJ）

- 单个 Go 进程，监听 `127.0.0.1:18790`（HTTP/WSS；ESA → openresty 443 → 此端口）与 `0.0.0.0:40000-40001/udp`（STUN，客户端直连公网 IP）。
- 限频、日志中的客户端 IP 取 ESA 透传的真实 IP 头（具体头名 M3 实测确认）。
- 中继：两端各一条 WSS，校验会话令牌后拼接；每会话固定缓冲（约 2×64 KiB），空闲 60 s 断开；全局会话上限（初值 50）防内存失控。
- 无持久化业务数据（在线表在内存）；日志进 journald。
- systemd：`MemoryMax=256M`、`GOMEMLIMIT=180MiB`、`Restart=on-failure`。

## 11. 与最终架构的关系

本设计即 v1.0 架构；M1–M4 的骨架代码按此模块落位，不写一次性脚本。

## 12. 浏览器端（M5）

- 入口只有两个：设备内 `lanweb`（`http://<设备IP>:<端口>/<token>/`，局域网直连）与服务器 `https://ft.cwlindean.cn/`（伪装页 → 口令解锁 → `/app`）。
- 服务器页只做发现与编排：设备用 6 位码注册（`POST /api/devices`，带 `device_token` 防劫持），浏览器用码解析出 LAN 地址与中继房间（`GET /api/resolve`），随后**并发**尝试 LAN 与中继，LAN 在 1.2 s 宽限内可达即用 LAN，否则回退中继。
- 口令由部署方经环境变量 `FT_WEB_PASSWORD` 注入，服务器只存 SHA-256，错误尝试按 IP 限速并固定延迟；解锁成功后下发 HMAC 签名 cookie（2 h，`HttpOnly`，TLS 下 `Secure`）。
- 伪装页不出现“传文件”字样，需在页脚连点 5 次才出现口令框；`/app` 未解锁一律 404。这层只是降低被顺手发现的概率，不是安全边界（D 系列审查对抗见 `RISKS.md`）。
- `ft share` 负责把设备登记到服务器：起 lanweb、注册 6 位码、打印码/房间/LAN 地址/二维码、每 2 分钟心跳，并保持 `/web/<room>` 的 host 连接。
- 目录片段（`#key`）留给 M6：浏览器到设备的中继载荷加密在 M5 暂用服务器可见的明文（与 M6 PAKE 一并收紧）；中继模式的文件传输载荷尚未接入。
