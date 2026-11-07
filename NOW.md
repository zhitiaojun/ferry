# NOW.md — 当前状态

> 更新于 2026-11-07。接续任务先读这里，再按 `PROJECT_INDEX.md` 取需要的资料。

## 当前目标

以“我的手机 ↔ 我的电脑”之间的最佳传输体验为主、兼顾陌生设备（D12），按 `PLAN.md` 的 M0–M9 推进至 v1.0。当前处于 **M0 收口与初始化**，由 work agent 执行中（D25，规约见 `WORK_AGENT.md`）：协议规范与 Go 骨架已完成，开发机 Android 工具链尚未就绪。

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
- [ ] M0-c 开发机工具链：Go 1.27.3 已就位（仓库内 `.tools/`，不入库）；Android SDK/NDK 与 gomobile 未装。
- [ ] M1–M9 —— 未开始。

可运行入口：`ft -version`（`cmd/ft`）。`core/proto` 已有金标向量单测；其余 `core/*` 与 `server/`、`cmd/ft-desktop/` 仅为占位。

## 阻塞 / 待确认

无阻塞用户的问题。执行中的待实测项（ESA 上 WSS 行为、学校网络可达性、IPv6、Android 17 权限）已列入对应里程碑任务卡。

## 下一步（按优先级，由 work agent 执行）

1. M0-c：在开发机安装 Android SDK/NDK 与 gomobile，并做一次空 AAR 构建验证（不在 BJ 构建）。
2. M0 收口：提交 G0 关卡报告（判据：protocol.md 评审通过、`go build ./...` 通过、MAP.md 与实际目录一致）。
3. G0 通过后进入 M1（本机核心传输）。

## 证据入口

| 阶段 | 证据位置 | 状态 |
|---|---|---|
| M1 本机 | `evidence/stage0/20261107-1745.log`（M0：向量生成 + 构建 + 测试原始输出） | 已有 |
| M2 局域网 | 待建：`evidence/stage1/*.log` | 无 |
| M4 打洞 | 待建：`evidence/stage2/*.log` | 无 |
| M3 中继 | 待建：`evidence/stage3/*.log` | 无 |

规则：证据必须是带时间戳的原始输出（命令 + 输出），不放结论句；`evidence/` 建立前不要凭空引用。目录编号沿用原阶段号（stage2 = 打洞、stage3 = 中继），与里程碑顺序不同。
