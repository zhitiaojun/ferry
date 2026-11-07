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

## C. M2：局域网直传与手机热点（2026-11-07 已执行，`evidence/stage1/20261107-1909.log`）

前置：`./android/build.sh` 产出 `android/build/ft-debug.apk` 并 `adb install -r`；手机开启热点、电脑连入；发现端口 **45321/UDP**（40002 在 Android 上属保留端口，见 `core/discovery/announce.go`）。

```bash
# 电脑侧（接收端，同时公告自己）
ft recv --lan --out ./recv/ --name pc --log recv.jsonl --once
# 电脑侧（发送端：自动发现，或直接给地址）
ft send --discover --to <设备名> --file ./test-2.1m.bin --log send.jsonl
ft send --addr <手机IP>:<端口> --file ./test-2.1m.bin --log send.jsonl
```

手机侧：安装 APK →「开始接收」（日志显示监听端口）→ 电脑 `ft send --addr`；反向则由手机「搜索设备」→ 选文件并发送。

验证：

1. 两端 JSON 摘要 `ok:true`，`path:"lan"`，热点场景 `topology:"hotspot"`；
2. `sha256sum` 比较源文件与接收端文件（手机侧用 `adb pull` 取回），必须一致；
3. 对照：关掉 Wi-Fi/热点后同样操作应失败（证明没有静默走服务器；M2 尚无中继可回退）；
4. 日志里 `discovery.announce.start.interfaces` 只含物理网卡（TUN/VPN 被过滤）；原始输出写 `evidence/stage1/`。

已知限制：手机侧 Go 不能读 netlink，接口信息由 Java 枚举后经 `discovery.Hint` 传入；Android 前台服务尚未加入，传输时请保持应用在前台。

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
