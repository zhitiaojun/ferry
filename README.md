# filetransfer

手机、电脑、浏览器之间互传文件的技术骨架。链路按三档设计，能直连就用直连，直连不成才走服务器：

1. **局域网**：同一网段直接发现并传输，最快，不经服务器。
2. **打洞**：不同网络时，双方经服务器交换地址后 UDP 打洞，成功后点对点直传。
3. **中继**：打洞失败时由服务器转发，兜底可用但慢。

## 定位

以**自有固定设备（我的手机 ↔ 我的电脑）之间的最佳体验**为主：一次配对自动连接、托盘 / 后台接收、右键与分享菜单、拖拽投放小窗、断点续传；同时支持用浏览器链接、附近设备、会话码给陌生设备传文件。技术路线：Go 单核心（Windows / 安卓 / BJ 服务端共用）。

## 当前状态（2026-11-08）

**三档链路、UDP 打洞与浏览器端均已跑通（M1–M5 完成，G3/G4 证据齐备，G5 待确认）。** `ft send` / `ft recv` 支持：本机 TCP、同网段直连（广播/组播发现，校园网被 AP 过滤时可手动指定地址）、经 BJ 信令与中继（`ft.cwlindean.cn`，内层 TLS 1.3 + 已配对公钥钉定，断线按指数退避重连）。手机端有最小 APK（Java + gomobile，热点与校园网实测通过）。协议规范 v1.0 已定稿，金标向量单测通过；UDP 打洞（STUN + QUIC，内层 TLS 1.3 钉定）与 t=0 竞速/降级已实现（M4）。浏览器端（M5）：设备局域网地址走 lanweb 直传；`https://ft.cwlindean.cn` 提供伪装页 + 口令解锁 + 6 位码发现 + 经 ESA 的中继传输（HTTPS 页面受混合内容限制，不能直连设备 HTTP LAN 地址，故公网页面固定走中继）。

| 里程碑 | 目标 | 状态 |
|---|---|---|
| M0 | 收口、协议规范、工程骨架 | 完成（G0 已关闭，2026-11-07） |
| M1 | 本机双进程互传，验证分块与校验 | 完成（G1 通过） |
| M2 | 局域网（Win ↔ 安卓）+ 最小 APK | 完成（G2 通过） |
| M3 | BJ 信令 + 中继 + 设备身份 | 完成：部署/中继/TLS 钉定/配对/重连/两端 VPN(T13) 均通过，G3 报告已出 |
| M4 | 打洞 / IPv6 直连 + 自动切换 | 完成：STUN 已上 BJ、打洞与竞速/降级落地；T5 跨 NAT 打洞成功，T6 受校园网 UDP 过滤时干净回退中继，G4 报告已出（IPv6 环境不具备） |
| M5 | 浏览器端（仅局域网 + 中继） | 完成：lanweb（设备局域网地址）与 `ft.cwlindean.cn` web 端（伪装页/口令/6 位码/中继）已部署并通过 T3/T4 验收 |
| M6 | 安全收尾 | 未开始 |
| M7 | 产品化（托盘、投放小窗、右键、后台、续传） | 未开始 |
| M8–M9 | 运维发布与验收 | 未开始 |

完整路线图见 `PLAN.md`；需求与判据见 `docs/context/requirements.md`；实现由 work agent 按 `WORK_AGENT.md` 执行。

> M6 已完成安全收尾：一次性 6 位码走 SPAKE2（密码不上线）、私钥在 Windows 用 DPAPI 封装、服务端限频与限流已生效；自查清单见 `docs/security-checklist.md`。仍建议先用自己的测试文件验证（R8）。

## 怎么用

M1 只支持本机回环（同一台 Windows 机器上的两个进程）：

```bash
# 终端 A：先起接收端（会打印实际监听地址与端口）
ft recv --listen 127.0.0.1:18800 --out ./recv

# 终端 B：发送端指定同一个地址
ft send --addr 127.0.0.1:18800 --file ./a.bin --file ./b.txt
```

成功后两端 stdout 各输出一行 JSON 摘要（`ok`、`path`、`files`、`bytes`、`chunks`）；加 `--log events.jsonl` 可留 JSONL 事件日志。中断后再执行同样的命令，接收端会按 `<out>/<文件名>.ftpart` 的大小自动续传。操作步骤与验证方法一律去 `RUNBOOK.md`，命令不以文档形式授权执行。

## 文档入口

- 不知道从哪读起 → `PROJECT_INDEX.md`（按任务路由）
- 接续工作 → `NOW.md`
- 路线图（直至完工）→ `PLAN.md`
- 架构审查与优化建议 → `docs/context/review.md`
- 规则与纪律 → `AGENTS.md`
- 需求与验收判据 → `docs/context/requirements.md`
- 服务器事实 → `docs/context/infra-bj.md`
- 设计 → `docs/context/design.md`
- 参考实现 → `docs/context/references.md`

## 安装与使用（0.8.0）

- **Windows**：运行 `ft-setup.exe`（`dist/windows/`）安装到 `%LOCALAPPDATA%\ft`，登录时自启托盘、注册右键「发送到」；卸载用 `ft-setup.exe -uninstall`。
- **安卓**：安装 `dist/android/ft-0.8.0.apk`（发布密钥签名）。首次使用在电脑上 `ft pair --show` 生成配对 URI，手机「导入对方配对 URI」；接收时点一次「远程接收」（打洞/中继自动竞速），发送时选文件点「发送（自动选路）」。
- **命令行**：`ft send --addr host:port --file a`（LAN）、`ft send --punch --allow relay --signal wss://ft.cwlindean.cn/signal --to-id <设备ID>`（竞速）、`ft recv --signal ...`（等发送方）。
- 已知限制见 `CHANGELOG.md` 末尾与 `RUNBOOK.md` §C。
