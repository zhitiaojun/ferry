# RUNBOOK.md — 操作与验证

> 约定：每条操作写「前置 → 命令 → 验证 → 依据」。**未实际执行过的步骤标注「未执行」**，命令存在不等于已生效。

## A. 本地环境自检

前置：无。

```bash
go version
gomobile version
```

验证：均打印版本号（M0 安装后执行）；实际版本记入 `MAP.md` §4。

## B. M1：本机双进程互传（2026-11-07 已执行，`evidence/stage0/20261107-1835.log`）

前置：已 `go build ./...`（或直接用构建产物 `ft`）。两端在同一台机器，走本地 TCP。

```bash
# 终端 1（接收端，先起；监听 0 端口时由日志打印实际地址）
ft recv --listen 127.0.0.1:18900 --out ./recv/ --log recv.jsonl
# 终端 2（发送端）
ft send --addr 127.0.0.1:18900 --file ./test-2.1m.bin --log send.jsonl
```

验证：

1. 两端 stdout 的 JSON 摘要 `ok:true`，`bytes`/`chunks` 与文件大小相符；
2. `sha256sum` 比较源文件与 `<out>/<文件名>`，必须一致；
3. 中断演练：传输中杀掉发送进程，确认 `<out>/<文件名>.ftpart` 保留；重跑同样的 `recv` 与 `send`，日志出现 `resume_from`，完成后哈希仍一致；
4. 原始输出写入 `evidence/stage0/YYYYMMDD-HHMM.log` 并在 `NOW.md` 登记。

## C. M2：局域网直传（未执行）

前置：Win 与手机同网段（热点场景必通；学校网络有概率，R6）；手机侧 APK 已构建安装（构建方式见 `docs/context/design.md` §6，尚属待定项）。

```bash
# Win 侧：确认本机在局域网内的地址
ipconfig
# 手机侧：确认能看到 Win 的发现广播（抓包或应用日志）
```

验证（三选二即可）：

- 手机侧日志出现 Win 设备名与地址；
- 传输完成且校验一致；
- 对照测试：关闭 Wi-Fi 后应当**无法**建立局域网路径（证明不是走了别的档）。

## D. BJ 服务器核验（只读，未执行；依据运维档案）

前置：按运维仓库的《服务器信息-BJ》与《服务器总览》确认连接方式；不得新建连接或改密钥。

```bash
ss -lntup | head -40            # 观察已占端口，确认不与他站冲突
nft list ruleset | head -40     # 确认本机是否有 INPUT 钩子（档案记录为无）
```

验证：输出与 `docs/context/infra-bj.md` 的端口表一致；不一致时先改文档再动手。

## E. BJ 部署 ft-server（未执行，M3）

前置：M3 ESA 预检已完成（`PLAN.md` M3 任务 0）；读运维档案；`ss -lntup` 复核 18790/tcp、40000-40001/udp 空闲；`free -m` 记录部署前可用内存；**改 openresty 前把具体变更告知用户**（D16）。

步骤（草案，沿用 battery-alert 一键脚本模式）：

1. 开发机交叉编译 `GOOS=linux GOARCH=amd64`，上传到 `/opt/ft-server/`。
2. 写 `/etc/systemd/system/ft-server.service`：`MemoryMax=256M`、`Environment=GOMEMLIMIT=180MiB`、`Restart=on-failure`；`systemctl enable --now ft-server`。
3. DNS：`ft.cwlindean.cn` 已解析到 ESA（D22），无需改动；在 ESA 侧确认回源到 BJ 443 并启用 WebSocket（用户操作）；1Panel 申请证书并导出到 `sites/ft.cwlindean.cn/ssl/`。
4. 新建站点 conf + `proxy/ft.conf`（反代 `127.0.0.1:18790`，开启 WebSocket 升级头、长超时）。
5. `nginx -t` 通过后 reload（**禁止 restart**）。

验证：`ss -lntup` 见监听；`curl https://ft.cwlindean.cn/health` 200；WSS 连通；现有 6 个站点公网探活不变；`systemctl show ft-server -p MemoryPeak` < 256 MB；回写运维仓库 `修改log.md` 与《服务器信息-BJ》。

回滚：`systemctl disable --now ft-server`；删 unit 与 `/opt/ft-server`；删站点 conf → `nginx -t` → reload。

## F. 证据归档

每次验证后，把原始输出写入 `evidence/stageN/YYYYMMDD-HHMM.log`，并在 `NOW.md` 的「证据入口」表登记路径。
