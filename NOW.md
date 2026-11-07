# NOW.md — 当前状态

> 更新于 2026-11-07。接续任务先读这里，再按 `PROJECT_INDEX.md` 取需要的资料。

## 当前目标

以“我的手机 ↔ 我的电脑”之间的最佳传输体验为主、兼顾陌生设备（D12），按 `PLAN.md` 的 M0–M9 推进至 v1.0。当前处于 **M0 收口与初始化**，由 work agent 执行中（D25，规约见 `WORK_AGENT.md`）：协议规范、Go 骨架、开发机 Android 工具链已完成；G0 于 2026-11-07 关闭（用户委托 work agent 定稿）。当前进入 **M1 本机核心传输**。

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
- [ ] M1–M9 —— 未开始。

可运行入口：`ft -version`（`cmd/ft`）。`core/proto` 已有金标向量单测；其余 `core/*` 与 `server/`、`cmd/ft-desktop/` 仅为占位。

## 阻塞 / 待确认

无阻塞。G0 已关闭（用户委托定稿：§6 冻结、空文件改为支持、信令留待 M3、多文件串行）。执行中的待实测项（ESA 上 WSS 行为、学校网络可达性、IPv6、Android 17 权限）已列入对应里程碑任务卡。执行中的待实测项（ESA 上 WSS 行为、学校网络可达性、IPv6、Android 17 权限）已列入对应里程碑任务卡。

## 下一步（按优先级，由 work agent 执行）

M1（按 `PLAN.md` 任务卡）：

1. 会话层：HELLO 握手、累计偏移 ACK、去重、FIN/ERROR、PING。
2. 传输层：MANIFEST、64 KiB 分块、块哈希 + 整文件 SHA-256、流式读写、`.ftpart` 预留。
3. 路径接口 `Dial/Accept/Close`，先本地 TCP。
4. CLI：`ft send --file a --file b`、`ft recv --out <dir>`。
5. JSONL 日志 + 证据脚本；单测 + 故障注入；切路径预演。

## 证据入口

| 阶段 | 证据位置 | 状态 |
|---|---|---|
| M1 本机 | `evidence/stage0/20261107-1745.log`（M0：向量生成 + 构建 + 测试）、`evidence/stage0/20261107-1800.log`（M0-c：Android 工具链 + AAR）、`evidence/stage0/20261107-1805.log`（M0-d：规范定稿 + 向量重生成） | 已有 |
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
