# 系统设计（design）v0.2

> 状态：**建议，部分已被决策约束**。已确认的约束以 `DECISIONS.md` 为准（D1–D21）；本文件讨论“怎么做”，可被推翻。问题来源与理由见 `review.md`；里程碑见 `../../PLAN.md`。
> 更新：2026-11-07，吸收 review 意见与第二轮问卷（D12–D21）。

## 1. 总体架构

```
┌────────── Windows 客户端（Go 单 exe）──────────┐      ┌──── 安卓 APK ────┐
│ 托盘常驻 │ 投放小窗 │ 主界面(WebView) │ 右键菜单   │      │ Kotlin 外壳：UI、  │
│ ───────────── Go 核心（core/）──────────────── │      │ 分享菜单、前台服务 │
│ identity │ discovery │ signaling │ punch │ relay │      │ ── Go 核心 AAR ── │
│ session  │ transfer  │ pathmgr   │ lanweb(给浏览器)│      │ （同一份 core/）  │
└──────────────────────────────────────────────┘      └──────────────────┘
                 │ wss://ft.<域名>（443）       │ UDP 40000/40001
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
core/punch       STUN、NAT 映射判定、IPv4 打洞、IPv6 直连、NAT 回环
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
| 浏览器链接 / 二维码 | 发送端生成链接 `https://ft.<域名>/r/<房间ID>#<密钥>`；`#` 后的密钥不会发给服务器，浏览器用 Web Crypto（ECDH/AES-GCM）端到端加解密 | 中继（WSS）；同局域网时优先 `http://<局域网IP>:<端口>`（lanweb） |
| 局域网附近设备 | 广播列出附近已安装 App 的设备，点选后对方弹窗确认 | LAN |
| 一次性会话码 | 6 位码 + PAKE 派生临时密钥，用后作废 | 三档竞速 |

陌生设备与自有设备共用会话层、传输层；区别只在“信任来自钉定公钥还是一次性密钥”。中继不区分限制（D20）。

## 4. 路径与会话层

四层结构（自下而上）：

```
路径层     LAN: TCP+TLS | NAT 回环: QUIC | 打洞: QUIC(IPv4/IPv6) | 中继: WSS 内 TLS
会话层     session_id、累计偏移确认、去重、路径切换
传输层     清单（多文件）、64 KiB 分块、每块 SHA-256、整文件 SHA-256
选路层     竞速、升级、降级
```

候选路径（按优先级）：

1. **LAN 直连**：广播发现或信令交换的局域网地址。
2. **NAT 回环**：双方外网 IPv4 相同（同一路由器后），尝试经路由器回环直连——用于家中 Wi-Fi 开了客户端隔离的情况（R6），需实测。
3. **IPv6 直连**：双方全局 IPv6 地址，同时发包穿过有状态防火墙。
4. **IPv4 打洞**：STUN 观测地址 + 同时发包（DCUtR 思路）；STUN 与 QUIC 共用同一 UDP socket。
5. **中继**：WSS 经 443 到 ft-server，按会话拼接。

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
| **投放小窗**（示意：QQ 的“文件拖到这”） | 低级鼠标钩子（`WH_MOUSE_LL`）检测按下 + 拖动，并确认系统处于拖放状态（例如出现 Shell 拖拽图像窗口）后，在右下角滑出置顶小窗，列出已配对设备；小窗用 `DragAcceptFiles` / OLE 投放目标接收文件；拖放结束或移开后自动收起 | **高**：全局拖拽检测无官方 API，需在 M2 期间做技术预研；备选方案为托盘图标常驻投放区或屏幕边缘迷你条 |
| 右键菜单 | 先做“发送到”菜单（SendTo 快捷方式，零注册表）+ 经典右键菜单项；Win11 一级菜单需 `IExplorerCommand` + 稀疏包身份，列为可选 | 中 |
| 主界面 | WebView2 承载 `web/` 同一套界面（Wails v3 或 go-webview2，M7 前预研定） | 中 |
| 接收 | 托盘常驻保持信令长连接，收到请求弹通知；已配对设备可设为自动接收 | 低 |

## 9. 安卓客户端（D6、D19）

- 目标设备：小米 22081212C，HyperOS，**Android 17 / SDK 37**（来自 battery-alert 档案）。
- 后台接收：前台服务（常驻通知）保持 WSS 长连接；心跳间隔按蜂窝 NAT 超时实测（初值 60 s）；必须引导用户打开 HyperOS「自启动」与「省电策略：无限制」（battery-alert 已验证此为必要条件）。
- 前台服务类型、Android 17 的本地网络权限要求、`MulticastLock`：M2 内实测确定（R10）。
- 分享菜单：`ACTION_SEND` / `SEND_MULTIPLE`；为每台已配对设备发布 Direct Share 快捷方式，实现“一点就发到电脑”。
- 构建：在 Windows 开发机构建（gomobile + Gradle 或手工工具链），**不在 BJ 上构建**。

## 10. ft-server（BJ）

- 单个 Go 进程，监听 `127.0.0.1:18790`（HTTP/WSS，经 openresty 443 反代）与 `0.0.0.0:40000-40001/udp`（STUN）。
- 中继：两端各一条 WSS，校验会话令牌后拼接；每会话固定缓冲（约 2×64 KiB），空闲 60 s 断开；全局会话上限（初值 50）防内存失控。
- 无持久化业务数据（在线表在内存）；日志进 journald。
- systemd：`MemoryMax=256M`、`GOMEMLIMIT=180MiB`、`Restart=on-failure`。

## 11. 与最终架构的关系

本设计即 v1.0 架构；M1–M4 的骨架代码按此模块落位，不写一次性脚本。
