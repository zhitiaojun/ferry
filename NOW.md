# NOW.md — 当前状态

> 更新于 2026-11-07。接续任务先读这里，再按 `PROJECT_INDEX.md` 取需要的资料。

## 当前目标

以“我的手机 ↔ 我的电脑”之间的最佳传输体验为主、兼顾陌生设备（D12），按 `PLAN.md` 的 M0–M9 推进至 v1.0。当前处于 **M0 收口与初始化**，由 work agent 执行中（D25，规约见 `WORK_AGENT.md`）：协议规范、Go 骨架、开发机 Android 工具链已完成；G0 于 2026-11-07 关闭（用户委托 work agent 定稿）。M1 本机核心传输的代码与 G1 证据已完成，待用户确认后进入 M2。

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
- [ ] M2–M9 —— 未开始。

可运行入口：`ft -version`、`ft send` / `ft recv`（`cmd/ft`，仅本机 TCP）。`core/proto` 有金标向量单测，`core/session`、`core/transfer` 有单测与故障注入；`core/{identity,logging,pathmgr}` 已实现最小功能；`core/{discovery,signaling,punch,relay,lanweb}`、`server/`、`cmd/ft-desktop/` 仍为占位。

## 阻塞 / 待确认

无阻塞。G0 已关闭（用户委托定稿：§6 冻结、空文件改为支持、信令留待 M3、多文件串行）。执行中的待实测项（ESA 上 WSS 行为、学校网络可达性、IPv6、Android 17 权限）已列入对应里程碑任务卡。执行中的待实测项（ESA 上 WSS 行为、学校网络可达性、IPv6、Android 17 权限）已列入对应里程碑任务卡。

## 下一步（按优先级，由 work agent 执行）

M1 已完成，等用户确认 G1。确认后进入 M2（按 `PLAN.md` 任务卡，涉及用户手机，开工前先对齐测试窗口）：

1. 发现：UDP 广播 + 组播双发（载荷带设备 ID），安卓 `MulticastLock`；手动 `--peer <ip:port>` 兜底。
2. LAN 路径 TCP 直连；识别“电脑连安卓热点”拓扑，此拓扑下 LAN 失败默认不回退中继。
3. VPN/TUN 网卡识别与排除（D28）；日志记录 VPN 状态。
4. gomobile 打包 AAR + Kotlin 单 Activity + 前台服务；确认 Android 17 本地网络权限。
5. 学校网络与热点实测，产出 `evidence/stage1/`；投放小窗预研。

## 证据入口

| 阶段 | 证据位置 | 状态 |
|---|---|---|
| M0 | `evidence/stage0/20261107-1745.log`（向量生成 + 构建 + 测试）、`20261107-1800.log`（Android 工具链 + AAR）、`20261107-1805.log`（规范定稿 + 向量重生成） | 已有 |
| M1 本机 | `evidence/stage0/20261107-1835.log`（构建/测试/CLI×3 哈希/故障注入/续传/内存，`M1_EVIDENCE=PASS`） | 已有 |
| M2 局域网 | 待建：`evidence/stage1/*.log` | 无 |
| M4 打洞 | 待建：`evidence/stage2/*.log` | 无 |
| M3 中继 | 待建：`evidence/stage3/*.log` | 无 |

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
