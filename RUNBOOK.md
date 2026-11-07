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

已知限制：

- 手机侧 Go 不能读 netlink，接口信息由 Java 枚举后经 `discovery.Hint` 传入。
- 校园网 / 部分企业 Wi-Fi 会在 AP 上过滤客户端之间的广播与组播（DLUT-PanJin 实测：双向发现失败、定向广播探针 0/3，但单播 TCP 正常）。这类网络下用 `--peer/--addr <ip:port>` 直连；失败时先确认两台设备是否在同一子网，再判断是不是 AP 隔离（`ping` + 手动地址都不通）。
- 接收/发送期间 App 启动前台服务（`dataSync`）保活；Android 13+ 首次会请求通知权限，拒绝只是看不到通知，传输本身不受影响。

## D. BJ 服务器核验（只读，未执行；依据运维档案）

前置：按运维仓库的《服务器信息-BJ》与《服务器总览》确认连接方式；不得新建连接或改密钥。

```bash
ss -lntup | head -40            # 观察已占端口，确认不与他站冲突
nft list ruleset | head -40     # 确认本机是否有 INPUT 钩子（档案记录为无）
```

验证：输出与 `docs/context/infra-bj.md` 的端口表一致；不一致时先改文档再动手。

## E. BJ 部署 ft-server（已于 2026-11-07 21:10 执行）

前置（当初执行时的做法，后续重部署照做）：读运维仓库档案；`ss -lntup` 复核 18790/tcp 与 40000-40100/udp 空闲；`free -m` 记录可用内存；**改 openresty 前把具体变更告知用户**（D16）。运维记录见运维仓库 `修改log.md`（2026-11-07 21:10 条目，含回滚步骤）。

完成的步骤：

1. `GOOS=linux GOARCH=amd64 go build -o .tools/dist/ft-server ./cmd/ft-server`，上传到 BJ `/opt/ft-server/ft-server`（0755）。
2. `/etc/systemd/system/ft-server.service`：`ExecStart=/opt/ft-server/ft-server -listen 127.0.0.1:18790 -log /var/log/ft-server/events.jsonl`、`MemoryMax=256M`、`Environment=GOMEMLIMIT=160MiB`、`Restart=on-failure`；`systemctl daemon-reload && systemctl enable --now ft-server`。
3. 站点：`/opt/1panel/www/sites/ft.cwlindean.cn/{index,log,proxy,ssl}`；从 `agent.db` 表 `website_ssls` 的 id=8（通配符 `*.cwlindean.cn`，到期 2027-01-14）导出 `ssl/fullchain.pem`、`ssl/privkey.pem`（644/600）。
4. `conf.d/ft.cwlindean.cn.conf`（80 只留 ACME 并 301；443 `ssl_protocols TLSv1.3 TLSv1.2`、`/healthz` 反代、`include .../proxy/*.conf`、其余 204）+ `proxy/ft.conf`（`/signal`、`/relay`，Upgrade 头、`proxy_read_timeout 3600s`、`proxy_buffering off`）。仓库内副本见 `deploy/`。
5. `docker exec 1Panel-openresty-rAPZ nginx -t` 通过后 `nginx -s reload`（**从未 restart**）。

验证：`curl -s https://ft.cwlindean.cn/healthz` 200；WSS 登录 250ms 级；中继 2.1MB/10MB 哈希一致；`systemctl show ft-server -p MemoryCurrent` 约 7 MB（限额 256 MB）。

### E.1 客户端经中继使用（M3 当前形态）

```bash
# 0) 互信：双方各自生成配对 URI，线下交换后导入（含对端公钥，防止中继冒充）
.tools/bin/ft.exe pair --show --key ./recv.key --name phone    > phone.uri
.tools/bin/ft.exe pair --show --key ./send.key --name laptop   > laptop.uri
.tools/bin/ft.exe pair --add "$(cat laptop.uri)" --paired ./paired-recv.json
.tools/bin/ft.exe pair --add "$(cat phone.uri)"  --paired ./paired-send.json

# 接收端：登录信令等待 send_request。接收端没有 --allow 参数（收到请求即接受）。
.tools/bin/ft.exe recv --signal wss://ft.cwlindean.cn/signal   --key ./recv.key --paired ./paired-recv.json --out ./recv/ --log recv.jsonl

# 发送端：先 whoami 取自己的 device_id；用 --to-id 指定对端
.tools/bin/ft.exe whoami --key ./send.key
.tools/bin/ft.exe send --signal wss://ft.cwlindean.cn/signal --allow relay   --key ./send.key --paired ./paired-send.json   --to-id <对端 device_id> --file a.bin --log send.jsonl
```

验证：两端 JSON `"path":"relay"`、`bytes`/`chunks` 一致，接收目录内 SHA-256 与源相同；两端日志各有 `{"event":"relay.tls","pinned":true,"tls":"1.3"}`。未配对的对端会被拒绝（`peer … is not paired`），不会传输任何字节。

信令断开时接收端按 1s→2s→4s…最多 30s 指数退避重连（日志 `signal.reconnect`），重连后可继续接收。

注意：ESA 会把**空闲 30 s** 的 WSS 断开，客户端已按 15 s 心跳保活（`core/signaling.KeepaliveInterval`），不要再调大。

## F. 证据归档

每次验证后，把原始输出写入 `evidence/stageN/YYYYMMDD-HHMM.log`，并在 `NOW.md` 的「证据入口」表登记路径。
