# NOW.md — 当前状态

> 更新于 2026-11-07。接续任务先读这里，再按 `PROJECT_INDEX.md` 取需要的资料。

## 当前目标

以“我的手机 ↔ 我的电脑”之间的最佳传输体验为主、兼顾陌生设备（D12），按 `PLAN.md` 的 M0–M9 推进至 v1.0。当前处于 **M0 收口与初始化**。

## 进展（截至 2026-11-07）

- [x] 需求澄清与两轮决策确认（D1–D21，见 `DECISIONS.md`）。
- [x] BJ 事实同步自运维档案（2026-11-03 实测版，见 `docs/context/infra-bj.md`）。
- [x] 参考实现调研（`docs/context/references.md`）。
- [x] 架构审查（`docs/context/review.md`）与设计 v0.2（`docs/context/design.md`）。
- [x] 路线图 v0.3（`PLAN.md`）。
- [x] Git 仓库初始化与首次提交（D21）。
- [ ] M0：`docs/spec/protocol.md`、Go 工程骨架、开发机工具链 —— 未开始。
- [ ] M1–M9 —— 未开始。

代码行数、可运行入口：**目前为零**。

## 阻塞 / 待确认（详见 `PLAN.md` M0）

1. **Q1 子域名**：名称与接入方式（推荐 `ft.cwlindean.cn` DNS 直连 BJ）。阻塞 M3 部署。
2. **Q2 BJ 公网带宽规格**。影响中继性能判定。
3. **Q3 家中 Wi-Fi 隔离**：电脑能否有线、路由器能否关隔离 / 支持 NAT 回环。影响主场景体验（R6）。
4. **Q4 性能口径与 M4 判据替代**是否采纳。
5. **Q5 M3 起是否提前启用 TLS**。

## 下一步（按优先级）

1. 写 `docs/spec/protocol.md` 与测试向量；建 Go 工程骨架（M0）。
2. 实现 M1 核心传输并留 `evidence/stage0/`。
3. M2：局域网 + 安卓最小 APK；同时实测家中 NAT 回环、预研投放小窗。

## 证据入口

| 阶段 | 证据位置 | 状态 |
|---|---|---|
| M1 本机 | 待建：`evidence/stage0/*.log` | 无 |
| M2 局域网 | 待建：`evidence/stage1/*.log` | 无 |
| M4 打洞 | 待建：`evidence/stage2/*.log` | 无 |
| M3 中继 | 待建：`evidence/stage3/*.log` | 无 |

规则：证据必须是带时间戳的原始输出（命令 + 输出），不放结论句；`evidence/` 建立前不要凭空引用。目录编号沿用原阶段号（stage2 = 打洞、stage3 = 中继），与里程碑顺序不同。
