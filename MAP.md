# MAP.md — 目录 / 环境 / 部署映射

> 只记录“东西在哪、跑在哪、版本是什么”。不虚构尚未建立的目录。

## 1. 仓库目录（当前实际）

```
filetransfer/
├─ AGENTS.md            项目规则（读写纪律 + 硬约束）
├─ PROJECT_INDEX.md     任务 → 文件 路由
├─ README.md            用途与状态
├─ NOW.md               当前进展 / 阻塞 / 下一步
├─ MAP.md               本文件
├─ RUNBOOK.md           操作与验证步骤
├─ DECISIONS.md         已确认决策
├─ RISKS.md             风险与验证缺口
├─ PLAN.md              路线图与里程碑（直至完工）
├─ WORK_AGENT.md        work agent 执行规约（防偏航）
├─ go.mod               Go 模块 github.com/cwlindean/filetransfer
├─ core/proto/          帧与消息编解码（金标向量单测）
├─ core/session/        HELLO 握手、ACK/PING/ERROR、连接封装（M1）
├─ core/transfer/       发送/接收、分块校验、`.ftpart` 续传（M1）
├─ core/pathmgr/        TCP Dial / Accept / Close（M1，仅 LAN）
├─ core/logging/        JSONL 事件日志（M1）
├─ core/identity/       device_id 派生（M1）
├─ core/discovery/      LAN 广播+组播发现、接口过滤、热点拓扑（M2）
├─ core/mobile/         gomobile 绑定面（StartReceiver/Discover/SendFiles，M2）
├─ core/{signaling,punch,relay,lanweb}/  仅包说明，随后续里程碑填充
├─ cmd/ft/              CLI：-version / send / recv（M1）
├─ cmd/ft-desktop/      投放小窗预研原型（Win32 拖放，M2）
├─ server/              ft-server（占位）
├─ docs/context/        专题：requirements / infra-bj / design / references / review
├─ docs/spec/           protocol.md + vectors/（金标向量）
├─ android/             最小 APK：Java 单 Activity + Manifest + 无 Gradle 构建脚本（M2）
├─ web/                 浏览器页面（占位）
├─ tools/               gen_vectors.py、m2-evidence.sh（证据脚本，不进入产品路径）
├─ evidence/            证据日志（stage0..stage3）
└─ history/             历史归档索引
```

目录职责与模块边界见 `docs/context/design.md` §1；上表只反映实际存在的目录。

## 2. 运行环境

| 角色 | 环境 | 说明 |
|---|---|---|
| 开发机 | Windows 11（日常在学校网络） | Go 1.27.3、Temurin JDK 17.0.21、Android cmdline-tools 12.0 + build-tools 34.0.0 + platform android-34、NDK r27c、gomobile/gobind 均装在仓库内 `.tools/`（不入库）；已用空 AAR 验证（`evidence/stage0/20261107-1800.log`）；APK 在此构建，不在 BJ 构建 |
| 手机 | 安卓（小米 22081212C，HyperOS，Android 17 / SDK 37） | 测试网络：电信蜂窝；学校网络（部分隔离）；手机开热点供电脑连入（R6） |
| 服务器 | BJ（阿里云 ECS） | 内网 `172.24.52.162`，公网 `123.57.253.208` |

细节与端口占用见 `docs/context/infra-bj.md`。

## 3. 服务部署位置

**当前无部署。** 计划（M3，方案待部署前复核）：

| 组件 | 位置 |
|---|---|
| ft-server 程序 | BJ `/opt/ft-server/` |
| 进程管理 | systemd 常驻单元 `ft-server.service`（`MemoryMax=256M`，D15） |
| 监听 | `127.0.0.1:18790/tcp`；`0.0.0.0:40000-40001/udp` |
| 对外入口 | `https://ft.cwlindean.cn`（已解析到 ESA，D22）→ 回源 openresty 443 → 18790；UDP 直连 `123.57.253.208:40000-40001` |
| 站点配置 | `/opt/1panel/www/conf.d/<子域名>.conf` + `sites/<子域名>/proxy/ft.conf` |
| 日志 | journald（`journalctl -u ft-server`） |

禁忌：不得部署到 LA-DR 与 rn。

## 4. 版本与时间基准

| 对象 | 版本 / 标识 | 来源 |
|---|---|---|
| 本文档集 | v0.5（2026-11-07：VPN / TUN 场景） | 本次任务 |
| 代码 | M2：本机、热点 LAN 与最小 APK 可用（`ft send/recv`、`core/discovery`、`core/mobile`、`android/`）；信令/打洞/中继/Web 仍占位 | `go.mod`、`android/build.sh`、`evidence/stage1/20261107-1909.log` |
| 开发机工具链 | Go 1.27.3 / JDK 17.0.21 / NDK r27c / gomobile（x/mobile v0.0.0-20261011034528） | `.tools/`（不入库） |
| 运维档案 | 《服务器信息-BJ》2026-11-03 实测版 | 用户提供 |

## 5. 文件与事实的对应关系

- 服务器事实 → `docs/context/infra-bj.md`（唯一权威）
- 需求与判据 → `docs/context/requirements.md`（唯一权威）
- 决策与理由 → `DECISIONS.md`（唯一权威）
- 当前进展 → `NOW.md`（唯一权威）
- 路线图与里程碑 → `PLAN.md`（唯一权威）

改这些事实时改对应权威文件，其他位置只留摘要与链接。
