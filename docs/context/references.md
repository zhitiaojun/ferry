# 参考实现清单（references）

> 检索记录：2026-11-07，GitHub 仓库搜索（按星数排序），共 5 组查询 + 一次补充检索。星数为当日快照，会变。
> 结论：**没有一个项目能当骨架直接抄**——真正成熟的三档实现（Go / Rust）都在万行量级且带完整产品外壳；有价值的是它们的**协议设计与状态机**。工具链不是限制（可安装），语言不构成否决理由。

## 1. 架构级参考（优先读）

| 项目 | 星 | 语言 | 借鉴点 |
|---|---|---|---|
| [syncthing/syncthing](https://github.com/syncthing/syncthing) | 90.2k | Go | 目录分层几乎就是我们要的模块划分：`lib/beacon`（局域网广播/组播发现）、`lib/stun`、`lib/nat`（UPnP/NAT-PMP 端口映射）、`lib/relay`（中继协议与客户端）、传输层 TCP/QUIC 并存。核心理念：中继永远保留，不把直连当必须前提 |
| [tailscale/tailscale](https://github.com/tailscale/tailscale) | 38.7k | Go | `disco/` 的多路径并发探测与择优：同一对端点多条路径持续 ping/pong，按最近延迟打分，直连打通后从 DERP 切走；`net/netcheck` 的 NAT 类型探测决定是否值得打洞 |
| [n0-computer/iroh](https://github.com/n0-computer/iroh) + [sendme](https://github.com/n0-computer/sendme) | 12.9k / 1.2k | Rust | “relay 先载数据、打通后热切换”的成熟实现（QUIC + 打洞 + relay 升级）；接口抽象值得照搬 |
| [schollz/croc](https://github.com/schollz/croc) | 40.9k | Go | 代码短小、单文件可读全流程：信令、中继、分块、校验；`--relay` / 局域网模式的设计对比 |

## 2. 打洞与 STUN（可读的最小实现）

| 项目 | 星 | 语言 | 借鉴点 |
|---|---|---|---|
| [dwoz/python-nat-hole-punching](https://github.com/dwoz/python-nat-hole-punching) | 326 | Python | 约 200 行，客户端 + STUN 服务端齐全，最小完整示例 |
| [mwarning/UDP-hole-punching-examples](https://github.com/mwarning/UDP-hole-punching-examples) | 111 | C | 按 NAT 类型逐场景演示 |
| [samyk/pwnat](https://github.com/samyk/pwnat) | 3.9k | C | 双方都在 NAT 后时靠服务器预开洞的经典手法 |
| [threatexpert/gonc](https://github.com/threatexpert/gonc) | 737 | Go | “自动穿透”的完整编排：探测 → 打洞 → 回退中继的执行顺序 |
| [pion/stun](https://github.com/pion/stun) | 864 | Go | STUN 报文编解码（Binding Request、XOR-MAPPED-ADDRESS），自写 STUN 服务端时的参照 |
| [pion/ice](https://github.com/pion/ice) | 622 | Go | 候选配对、优先级、连通性检查——我们的“三路竞速”本质是简化版 ICE |
| [libp2p/go-libp2p](https://github.com/libp2p/go-libp2p)（DCUtR） | 6.9k | Go | 打洞协调：经中继交换地址 → 双方同步后**同时发包**，成功率显著高于单方先打 |

## 3. 局域网发现（阶段 1 直接照搬协议思路）

| 项目 | 星 | 语言 | 借鉴点 |
|---|---|---|---|
| [localsend/localsend](https://github.com/localsend/localsend) | 97.7k | Dart | 组播发现 + HTTPS 直传，手机/桌面/Web 全覆盖；我们的阶段 1 几乎是其子集，协议文档简单 |
| [KDE/kdeconnect-android](https://github.com/KDE/kdeconnect-android) | 1.5k | Kotlin (GPL-2.0) | 安卓端“UDP 广播发现 + TCP/TLS 传输”的成熟代码，手写 APK 的网络层模板 |
| [syncthing/syncthing-android](https://github.com/syncthing/syncthing-android) | 4.3k | Java | 较老，但纯 Java、依赖少（2024-12 后基本停更） |

## 4. 中继与 Web 端

| 项目 | 星 | 语言 | 借鉴点 |
|---|---|---|---|
| [pion/turn](https://github.com/pion/turn) | 2.3k | Go | 标准 TURN 服务端；浏览器端做中继基本只能走 TURN |
| [mycrl/turn-rs](https://github.com/mycrl/turn-rs) | 519 | Rust | 更轻的 TURN 实现 |
| [ntop/n2n](https://github.com/ntop/n2n) | 7.1k | C | supernode + edge 架构：中继只转发不解析业务 |
| [yangchuansheng/derper](https://github.com/yangchuansheng/derper) | — | Dockerfile | 自建 DERP 中继的 Docker 化版本 |
| [magic-wormhole](https://github.com/magic-wormhole/magic-wormhole) | 23k | Python | 纯中继 + 配对码（PAKE/SPAKE2）：无需证书的鉴权与加密设计 |

## 5. 完整三档产品（行为参照，不作代码来源）

- [dashbeam](https://github.com/tonyantony300/dashbeam) 10.2k（Tauri/Rust，原 AltSendme）：功能定义最完整，可当行为规范；
- [denislupookov/altersend](https://github.com/denislupookov/altersend) 2.1k（TS）：浏览器 WebRTC 如何用 ICE 实现三档；
- [SpatiumPortae/portal](https://github.com/SpatiumPortae/portal) 1.8k（Go，2024-08 后停更）：中继式实现的最小样本；
- [pradt2/always-online-stun](https://github.com/pradt2/always-online-stun) 1k：公共 STUN 列表，BJ 就绪前可先用。

## 6. 本轮检索中出现的同题小项目（星少，但结构与目标一致，可按需看一眼）

- `saagpatel/Relay`（Rust）：QUIC 局域网直连 + 自动回退中继，与本项目架构高度同构；
- `NETFORY/NetDrop`（Rust，iroh）：零知识 P2P 传文件，打洞 + relay fallback；
- `xdzdw/lan-transfer`（TS）：WebRTC P2P + WebSocket 中继，无安装；
- `dhruv*` 的 `parcel`（Go）：局域网发现 + 加密 + 可续传 + 中继回退。

## 7. 检索方法与坑（下次复用）

- 用 GitHub 仓库搜索 API：`search/repositories?q=...&sort=stars`；未认证限速 **10 次/分钟**，核心 API **60 次/小时**，超限返回 403 且 `remaining=0`。
- 本机沙箱内 `raw.githubusercontent.com` **DNS 解析失败**；`cdn.jsdelivr.net/gh/<repo>@<branch>/<path>` 可用；`mcp__anysearch__extract` 对 raw / jsdelivr / github blob 均失败。
- 结论：仓库级事实用 `api.github.com/repos/...`，正文改用 jsdelivr 直取。
