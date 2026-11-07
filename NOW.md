# NOW.md — 当前状态

> 更新于 2026-11-07。接续任务先读这里，再按 `PROJECT_INDEX.md` 取需要的资料。

## 当前目标

以“我的手机 ↔ 我的电脑”之间的最佳传输体验为主、兼顾陌生设备（D12），按 `PLAN.md` 的 M0–M9 推进至 v1.0。当前处于 **M3 信令 + 中继（含 BJ 部署）**，由 work agent 执行（D25，规约见 `WORK_AGENT.md`）：M0（G0 已关闭）、M1（G1 证据齐备）、M2（G2 已通过）之后，M3 的身份/信令/中继代码、BJ 部署、ESA 预检、经 ESA 的中继传输与内存采样均已完成；剩余扫码配对、中继外层 TLS 钉定、断线重连、VPN(T13) 实测与 G3 报告。

## 进展（截至 2026-11-07）

- [x] 需求澄清与两轮决策确认（D1–D21，见 `DECISIONS.md`）。
- [x] BJ 事实同步自运维档案（2026-11-03 实测版，见 `docs/context/infra-bj.md`）。
- [x] 参考实现调研（`docs/context/references.md`）。
- [x] 架构审查（`docs/context/review.md`）与设计 v0.2（`docs/context/design.md`）。
- [x] 路线图 v0.4（`PLAN.md`）与 work agent 执行规约（`WORK_AGENT.md`）。
- [x] 第三轮决策：ESA 子域名、不测速、学校网络 + 热点场景、分工（D22–D27）。
- [x] 第四轮：VPN（TUN）场景纳入全部链路设计与验收（D28）。
- [x] Git 仓库初始化与首次提交（D21）。
- [x] M0-a 协议规范：`docs/spec/protocol.md`（v1.0-draft）+ 金标向量（`docs/spec/vectors/`，生成器 `tools/gen_vectors.py`）。
- [x] M0-b Go 工程骨架：`core/proto` 帧/消息编解码 + 金标向量单测，`go build/vet/test` 通过；`ft -version` 可运行。
- [x] M0-d 规范定稿（G0）：§6 数值冻结、空文件改为**支持**、信令字段留待 M3、多文件串行；金标向量新增空文件清单用例。
- [x] M0-c 开发机工具链：Go 1.27.3、Temurin JDK 17.0.21、Android cmdline-tools 12.0 / build-tools 34.0.0 / platform android-34 / NDK r27c、gomobile + gobind 均装在仓库内 `.tools/`（不入库）；`gomobile bind` 产出 arm64 AAR 并校验（`evidence/stage0/20261107-1800.log`）。
- [x] M1 本机核心传输：`core/session`（HELLO/HELLO_ACK、累计偏移 ACK、去重、PING、ERROR）、`core/transfer`（MANIFEST、64 KiB 分块、块与整文件 SHA-256、`.ftpart` 续传、FIN 校验）、`core/pathmgr`（TCP `Dial/Accept/Close`）、`core/logging`（JSONL）、`cmd/ft send|recv`；单测含故障注入；证据 `evidence/stage0/20261107-1835.log`（M1_EVIDENCE=PASS）。
- [x] M2 发现与 LAN 直连：`core/discovery`（广播 + 组播双发、设备表、TTL、虚拟网卡/TUN 过滤）、`cmd/ft send --discover/--peer`、`ft recv --lan`；本机回环与「电脑连手机热点」双向直传均通过（2.1 MB / 10 MB，哈希一致），拓扑判定 `topology=hotspot`（对端为网关）。
- [x] M2 安卓最小 APK：`core/mobile` gomobile 绑定 + `android/`（Java 单 Activity、MulticastLock、无 Gradle 构建脚本），Redmi M332BF（Android 17 / SDK 37）安装运行，手机 ↔ 电脑热点双向传输哈希一致（`evidence/stage1/20261107-1909.log`）。
- [x] M2 两个平台缺陷已修：Android 17 禁止应用读取 netlink（`route ip+net: netlinkrib: permission denied`）→ 改为 Java 枚举 Wi-Fi 网卡并把 `{name,index,ip,prefix}` 经 gomobile 传给 Go（`discovery.Hint`）；Android 保留端口含 40002（`ip_local_reserved_ports`）导致 bind 失败 → 发现端口改为 **45321**（Windows 侧 50000-50059 也被排除）。
- [x] M2 学校网络实测（T1）：2026-11-07 19:20–19:25，DLUT-PanJin（PC 10.201.2.127 / 手机 10.201.2.123，同一 /24）。广播与组播发现**双向均被 AP 过滤**（PC 公告 8s、手机公告 8s 均搜不到；PC 定向广播探针 3 次、手机 0/3 收到），但**单播 TCP 双向可达**：手机→PC 2.1MB×2、10MB 与 PC→手机 2.1MB 全部哈希一致、`topology=lan`。记录 `evidence/stage1/20261107-1920.log`；结论已写入 `RISKS.md` R6（LAN 直连依赖手动/信令单播地址）。
- [x] M2 VPN(TUN) 诊断（T10）：热点与学校网络两次实测均在 PC 开 Meta TUN（198.18.0.1/30 Up）时完成，LAN 直连成功、`discovery.announce.start.interfaces` 只含物理网卡（Meta 被过滤）。手机侧无 VPN。
- [x] M2 安卓前台服务（M2-5）：`TransferService`（`foregroundServiceType=dataSync`、通知渠道 `ft-transfer`、未决通知），接收与发送期间启动、停止时退出；按 HOME 退到后台后 PC→手机 10MB 仍完成且哈希一致（`evidence/stage1/20261107-1920.log`）。`targetSdk=34` 在 Android 17 上未触发本地网络权限拦截（实测）；若日后升到 SDK 36+ 需复核 `LOCAL_NETWORK` 类权限。
- [x] M2 投放小窗预研（M2-6）：`cmd/ft-desktop` Win32 原型（置顶工具窗、`DragAcceptFiles`、每次投放输出一行 JSON、`-size/-margin/-pos/-log`）；合成 HDROP 用例与**真实资源管理器拖放**均通过（2026-11-07 拖入 `test-2.1m.bin`，窗口显示「收到 1 个文件」）；结论写入 `docs/context/design.md` §8.1（接收可行；“拖拽即弹窗”无官方 API，v1 用常驻小窗/边缘条/托盘投放区），证据 `evidence/stage1/20261107-2035.log`。
- [x] M3 身份与信令（本地）：`core/identity`（Ed25519 密钥、设备 ID = sha256(pub)、PEM 存取）、`core/signaling`（WSS 登录挑战、在线表、候选/请求转发、15s 心跳保活）、`server/`（`/signal` + `/relay` + `/healthz`，内存与连接上限）、`core/relay`（WebSocket 拼成 `net.Conn`）、`cmd/ft-server`、`cmd/ft send/recv --signal/--allow relay`、`cmd/ft whoami`；单测覆盖错误签名、未知消息、中继拼接。
- [x] M3 本地全链路：两台设备密钥经本机 `ft-server` 中继传 2.1MB/10MB，两端哈希一致（提交 `8059181`、`d7674a6`）。
- [x] M3 BJ 部署（用户批准后）：`/opt/ft-server/ft-server` + systemd `ft-server`（`MemoryMax=256M`、`GOMEMLIMIT=160MiB`、只监听 `127.0.0.1:18790`）；`ft.cwlindean.cn` 站点（443 反代 `/signal`、`/relay`，复用通配符证书）；只 `nginx -t` + reload。运维回写已完成（`修改log.md` 2026-11-07 21:10）。
- [x] M3-0 ESA 预检：公网 `healthz` 200、WSS 登录 250ms 级、Ping 38–41ms；**空闲 30s 即被断开**（keepalive=32/40s 均 ~30.3s EOF，28s 以下存活）→ 心跳取 15s；中继 2.1MB 6.1s、10MB 33.9s 哈希一致，`ft-server` 峰值内存 7.8MB（限额 256MB）。证据 `evidence/stage3/20261107-2118.log`、`20261107-2110-deploy.log`、`20261107-2136-esa-idle.log`（空闲 30s 断开与 15s 心跳 6 分钟存活）。
- [x] M3 配对：`ft pair --show/--add/--list` 生成与导入 `ftpair://v1/<device_id>/<pubkey>/<secret>?name=…`（`sha256(pub)==device_id` 校验），存 `~/.ft/paired.json`（0600）；6 位码 PAKE 按 protocol §8.6 留 M6。
- [x] M3 中继内层 TLS 1.3 + 身份钉定：`core/identity/tls.go`（Ed25519 自签证书、`VerifyPeerCertificate` 比对已配对公钥、`MinVersion=TLS1.3`）；未配对对端被拒。2.1MB/10MB 经 BJ 中继双向哈希一致（10MB 19.9s，两端均有 `relay.tls pinned=true`）；证据 `evidence/stage3/20261107-2220-tls10m.log`。
- [x] M3 断线重连：接收端信令断开后按 1→2→4→…→30s 指数退避重连（`signal.reconnect` 事件），重连后继续收；永久性错误（未配对、签名失败）不重试。证据 `evidence/stage3/20261107-2235-reconnect.log`（`M3_RECONNECT=PASS`）。
- [x] M3 未授权拒绝：未配对设备发 `send_request` 时接收端拒绝（`peer … is not paired`），发送端收不到 `relay_ready`，接收目录无文件。证据 `evidence/stage3/20261107-2230-g3.log`（`G3_LOCAL=PASS`）。
- [x] M3 VPN(T13) 实测（2026-11-07 22:40）：两端 TUN（电脑 `Meta` 198.18.0.1/30、手机 `tun0` 198.51.100.1/30），两端解析 `ft.cwlindean.cn` 均为 fake-ip（198.18.0.91 / 198.18.0.10）；经 BJ 中继 2.1MB 3.4s、10MB 16.2s，手机端 sha256 与源一致；重启 App 后重新点「中继接收」可继续收（3.5s，哈希一致）；`ft-server` 内存 7.0MB。证据 `evidence/stage3/20261107-2240-t13.log`。APK 同时补上中继接收与配对 URI 导入（`导入对方配对 URI` / `显示本机配对 URI`）。
- [ ] M3 剩余：G3 报告与 README/MAP 收尾（T2、T13、内存、未授权、其他站点、重连均已取证）。

可运行入口：`ft -version`、`ft whoami`、`ft send` / `ft recv`（`cmd/ft`；本机 TCP、LAN 发现、经 BJ 中继的 `--signal/--allow relay`）；`ft-server`（信令+中继）、`ft-probe`（登录与心跳探测）。`core/proto` 有金标向量单测，`core/session`、`core/transfer` 有单测与故障注入；`core/{identity,logging,pathmgr}` 已实现最小功能；`cmd/ft-desktop` 为投放小窗预研原型（非正式客户端）；`core/{punch,lanweb}` 仍为占位。

## 阻塞 / 待确认

无阻塞。G0 已关闭（用户委托定稿：§6 冻结、空文件改为支持、信令留待 M3、多文件串行）。执行中的待实测项（ESA 上 WSS 行为、学校网络可达性、IPv6、Android 17 本地网络权限与前台服务限制）已列入对应里程碑任务卡。

## 下一步（按优先级，由 work agent 执行）

M3 收尾（按 `PLAN.md` 任务卡）：

1. 出 G3 报告（T2、T13、内存、未授权、其他站点、重连），并更新 MAP/RUNBOOK/README 的 M3 状态。
2. 进 M4（打洞 + 竞速升级）：`core/punch`、STUN `0.0.0.0:40000-40001/udp`、路径竞速；开始前读 PLAN M4 任务卡。

## 证据入口

| 阶段 | 证据位置 | 状态 |
|---|---|---|
| M0 | `evidence/stage0/20261107-1745.log`（向量生成 + 构建 + 测试）、`20261107-1800.log`（Android 工具链 + AAR）、`20261107-1805.log`（规范定稿 + 向量重生成） | 已有 |
| M1 本机 | `evidence/stage0/20261107-1835.log`（构建/测试/CLI×3 哈希/故障注入/续传/内存，`M1_EVIDENCE=PASS`） | 已有 |
| M2 局域网 | `evidence/stage1/20261107-1909.log`（热点双向、哈希、TUN 过滤、端口 45321）、`20261107-1920.log`（学校网络：广播/组播被过滤、单播双向、4 轮哈希）、`20261107-2035.log`（投放小窗真实拖放）、`20261107-2040.log`（热点下手机→电脑 10MB） | 已有 |
| M4 打洞 | 待建：`evidence/stage2/*.log` | 无 |
| M3 中继 | `evidence/stage3/20261107-2118.log`（公网健康检查、probe、2.1MB/10MB 中继哈希、BJ 内存）、`20261107-2110-deploy.log`（systemd/站点/证书/端口状态）、`20261107-2136-esa-idle.log`（ESA 空闲 30s 与 15s 心跳）、`20261107-2220-tls10m.log`（TLS1.3 钉定 + 10MB）、`20261107-2230-g3.log`（未配对拒绝 + 重连）、`20261107-2235-reconnect.log`（信令中断重连）、`20261107-2240-t13.log`（两端 TUN + fake-ip + 中继 2.1MB/10MB + 重启重连） | 已有 |

规则：证据必须是带时间戳的原始输出（命令 + 输出），不放结论句；`evidence/` 建立前不要凭空引用。目录编号沿用原阶段号（stage2 = 打洞、stage3 = 中继），与里程碑顺序不同。

## 关卡 G0：已关闭（2026-11-07）

**结论**：用户表示“你看着办吧”，委托 work agent 定稿；按提案值冻结，空文件改为支持。

1. 判据逐条对照（`PLAN.md` G0）

| 判据 | 证据 | 结果 |
|---|---|---|
| `docs/spec/protocol.md` 评审通过 | `docs/spec/protocol.md` §6 冻结、§12 已定稿 5 项（含空文件支持） | 通过（2026-11-07 定稿） |
| `go build ./...` 通过 | `evidence/stage0/20261107-1800.log`：BUILD_OK / VET_OK / `go test ./...` 通过 | 通过 |
| `MAP.md` 与实际目录一致 | `MAP.md` §1 与 `core/*`、`cmd/ft`、`cmd/ft-desktop`、`server`、`docs`、`android`、`web`、`tools`、`evidence`、`history` 逐一核对 | 通过 |

2. “已通”要素：本关卡无网络链路结论，不涉及档位 / VPN 状态 / 回退排除。
3. 未满足 / 待核实：无；信令字段按 §12 留待 M3 实测细化，不阻塞 M1。
4. 对决策 / 计划的建议：无（不改 DECISIONS / PLAN）。
5. 文档更新：NOW、MAP、README 已更新；RUNBOOK、RISKS、infra-bj 本关卡无变化；BJ 无任何写入。提交：`7541c06`、`3e2c6da`、`719c8cc`。

## 关卡 G1：证据齐备，待用户确认（2026-11-07）

**结论**：M1 判据全部通过，证据在 `evidence/stage0/20261107-1835.log`（脚本 `.tools/m1-evidence.sh`，每项输出 `CHECK PASS/FAIL`，末尾 `M1_EVIDENCE=PASS checks=0`）。

| 判据（`PLAN.md` G1） | 证据 | 结果 |
|---|---|---|
| 2.1 MB / 10 MB 各 3 次哈希一致 | 同上：CLI 循环 3 次，每次 `src sha256` 与 `dst sha256` 相同 | 通过 |
| 故障注入行为正确 | `go test -v ./core/...`：块哈希不符→`chunk_hash_mismatch`、偏移非法→`offset_invalid`、重复块去重、对端断开报错、`.ftpart` 续传 | 通过 |
| 内存不随文件线性增长 | 1 GiB 续传 + 完整传输时接收端峰值 WorkingSet=14,262,272 B（断言 < 64 MiB） | 通过 |
| 切路径预演 | 两条本地 TCP：发送端被杀（连接断开）后重连，`resume_from=54,632,448` 与 part 大小一致，续传后两端 SHA-256 相同（`41090892…db070`） | 通过 |

说明与偏离：
- 续传预演等价于“关闭一条连接、换一条连接按偏移继续”，但两条连接仍是同一种 LAN TCP；不同物理路径间的切换留到 M3/M4。
- `PLAN.md` M1-2 写“RESUME 帧预留（不实现）”，但 M1-7 的切路径预演要求按偏移续传，故 M1 实现了最小续传（接收端据 `.ftpart` 发 RESUME）。`docs/spec/protocol.md` §3.7 已同步改写，逐块复核与跨 MANIFEST 协商留 M7。
- 证据目录按 `PLAN.md` M1-5 放在 `evidence/stage0/`（stage0=本机，stage1 留给 M2 局域网）。
- 用户 m00529「你看着办吧」授权定稿的部分仅限协议数值；G1 本身仍请用户过目后再进 M2（M2 需要手机配合）。

## 关卡 G2：证据齐备，待用户确认（2026-11-07）

**结论**：M2 判据全部通过，证据在 `evidence/stage1/`。

| 判据（`PLAN.md` G2） | 证据 | 结果 |
|---|---|---|
| T1b 热点双向 2.1 MB / 10 MB 哈希一致，`--allow lan` 下成功（未走服务器） | `20261107-1909.log`：手机→PC 2.1 MB、PC→手机 2.1 MB + 10 MB；`20261107-2040.log`：手机→PC 10 MB（153 块 968 ms，`e7524dd8…` 两端相同） | 通过 |
| T10（Win 开 TUN）在热点下 LAN 成功或给出明确诊断 | 两次热点测试均在 `Meta` TUN 198.18.0.2 默认路由存在时完成；`discovery.announce.start.interfaces` 只含 `WLAN`，Meta 被过滤 | 通过 |
| T1（学校网络）多次尝试的原始记录与成功率 | `20261107-1920.log`：广播/组播发现双向 0 成功（PC→手机 8 s、手机→PC 8 s、定向广播探针 0/3），单播 4/4 成功（2.1 MB×3、10 MB×1，哈希一致） | 通过 |
| 投放小窗预研有结论 | `docs/context/design.md` §8.1；合成 HDROP 用例 + 真实资源管理器拖放 `20261107-2035.log` | 通过 |

说明与偏离：
- 手机 App 直接发 TCP，没有 `--allow` 参数（该参数只在 CLI）；M2 没有信令/中继进程，`--allow relay` 返回 `ft_exit=3`，可证明无服务器可回退；接收端日志均为 `path:"lan"`。
- 学校网络广播发现双向失败、单播成功，已记 `RISKS.md` R6，并补一条 Android 后台广播 `sendmsg: operation not permitted` 观察（待 M3/M7 复核）。
- G2 只验证“可达 + 档位正确 + 哈希一致”，不做测速（D23）。
- `targetSdk=34` 在 Android 17 上未触发本地网络权限拦截（实测）；若升到 SDK 36+ 需复核 `LOCAL_NETWORK` 类权限。

## 下次开工提示（2026-11-07 22:45 记录）

- M3 已全部取证：本地代码、BJ 部署、ESA 预检、中继 TLS 1.3 钉定、配对、断线重连、未配对拒绝、T13 两端 VPN 中继；只差 G3 报告与文档收尾，随后进 M4（打洞）。
- BJ 侧 `ft-server` 由 systemd 常驻，**本机关机不影响**；恢复后先 `curl https://ft.cwlindean.cn/healthz` 确认。
- 手机 App 当前已带「中继接收」与配对 URI 导入；手机侧设备 ID `c6d743a5bc8393848d0ee1a6eb1dd9c4400bdfe3dba98017ad4c0afecaed9313`（测试用），配对文件在 App 外部目录 `received/paired.json`；adb 访问该目录需 `MSYS_NO_PATHCONV=1` 且 `TMP/TEMP` 指到 `.tools/tmp`。
- 恢复命令：`export GOCACHE=$PWD/.tools/gocache GOPATH=$PWD/.tools/gopath GOTMPDIR=$PWD/.tools/tmp GOFLAGS=-buildvcs=false`，然后 `.tools/go/bin/go.exe test ./...`。
