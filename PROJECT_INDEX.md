# PROJECT_INDEX.md — 路由索引

本文件只负责「遇到什么问题 → 读哪里」。不复制各专题正文。

## 项目定位

手机 / 电脑 / 浏览器互传文件：自有设备之间的最佳体验优先，兼顾陌生设备；三档链路为局域网直连 → 打洞 / IPv6 直连 → BJ 中继（443）。当前处于 M0，无代码。

## 任务 → 文件

| 你要做什么 | 读这里 |
|---|---|
| 了解项目用途、当前能做什么 | `README.md` |
| 接续工作，看目标 / 进展 / 阻塞 / 下一步 | `NOW.md` |
| **作为 work agent 执行实现任务**（执行循环、围栏、请示、汇报） | `WORK_AGENT.md` |
| 确认长期规则与读写纪律 | `AGENTS.md` |
| 明确需求、三档定义、验收判据、测试矩阵 | `docs/context/requirements.md` |
| 改服务器（BJ）前，了解主机事实、端口、权限边界 | `docs/context/infra-bj.md` |
| 设计或修改链路逻辑（发现 / 打洞 / 中继 / 竞速） | `docs/context/design.md` |
| 找现成参考实现、看别人怎么解决同一问题 | `docs/context/references.md` |
| 查“为什么这么定”，避免翻案 | `DECISIONS.md` |
| 上手操作：起服务、跑测试、验收 | `RUNBOOK.md` |
| 评估已知风险与验证缺口 | `RISKS.md` |
| 看完整路线图、里程碑与关卡（直至完工） | `PLAN.md` |
| 看架构问题与优化建议（未决，待用户确认） | `docs/context/review.md` |
| 追溯历史决策过程 | `history/README.md` |
| 定位文件放哪、服务跑在哪、配置在哪 | `MAP.md` |

## 快速判断

- 只想知道「现在到哪一步了」→ 只读 `NOW.md`。
- 要写代码 → `AGENTS.md`（规则）+ `WORK_AGENT.md`（执行规约）+ `PLAN.md` 当前里程碑 + `docs/context/design.md`（设计）+ `DECISIONS.md`（已定约束）。
- 要碰 BJ → `docs/context/infra-bj.md` + `RUNBOOK.md` + `RISKS.md`，缺一不可。
