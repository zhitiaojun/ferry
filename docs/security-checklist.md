# 安全自查清单（M6，G6 判据）

> 范围：本项目 M0–M6 已实现的代码路径。每项给出「约束在哪实现 / 用什么证据证明」。改动后须重新跑对应测试或脚本。
> 最近核对：2026-11-26（对应 `v1.0.0-beta.37` 代码路径）。

## 1. 文件名与路径

| 检查 | 实现 | 证据 |
|---|---|---|
| 传输清单校验文件名与安全相对路径，拒绝 `\`、`:`、NUL、空段、`.`、`..` 或超长名 | `core/proto/messages.go` `Manifest.Validate`（`MaxNameLen=255`，`MaxRelPathLen=1024`） | `core/proto/manifest_test.go` 的 `TestManifestRejectsUnsafeNames`、`TestManifestAllowsSafeRelativePaths` |
| 清单文件数上限 | `Manifest.Validate`（MaxFiles=1024） | `TestManifestRejectsTooManyFiles` |
| 写盘前再次清洗相对路径与文件名 | `core/transfer/recv.go` `safeRelPath`、`core/lanweb/server.go` `cleanName` | `core/transfer/transfer_test.go`（`TestFolderRelativePathsAndCancelCleanup`）、`core/lanweb/server_test.go` 路径穿越用例（`..`、`%2e%2e%2f`、`..%5c` 返回 404） |
| 浏览器上传名清洗 | `lanweb` 上传路径 `cleanName` + 重名后缀 | 同上 |

## 2. 帧与载荷上限

| 检查 | 实现 | 证据 |
|---|---|---|
| 单帧载荷 ≤ 1 MiB | `core/proto/frame.go` `MaxPayload`，`ReadFrame` 超限报错 | `core/proto` 单测与金标向量 `invalid.json`（`frame_too_large`） |
| 信令信封 ≤ 64 KiB 且版本必须为 1 | `core/signaling/message.go` `Parse` | `core/signaling/message_test.go` |
| WebSocket 读上限：信令/中继 1 MiB，设备侧 web 房间 4 MiB（浏览器 64 KiB 帧） | `server/server.go` `SetReadLimit`、`core/tempshare/webhost.go` | 中继 10 MB 上传实测（`evidence/stage2/20261108-1521-web-upload10m.log`） |

## 3. 清单与内存

- 清单最多 1024 个文件、每段名称 ≤ 255 字节、相对路径总长 ≤ 1024 字节；传输逐文件串行，发送端按 64 KiB 读盘、接收端流式写 `.part`，内存不随文件大小线性增长（G1 已测 1 GiB 峰值 RSS 14 MB）。
- 服务器中继只拼接字节：每房间 2 个 socket、无缓冲整文件（`server/relay.go`）。

## 4. 日志脱敏

- 日志只写设备 id（截断 12 字符）、传输 id、字节数、路径档位、错误码；不写私钥、令牌、pair 载荷、文件内容。
- 核对方式：`grep -rn "log.Event" server cmd/ferry core | grep -iE "token|payload|secret|key"` 应只命中字段名而非取值；PAKE 的 `pair_begin/pair_relay` 载荷不落日志。
- 服务器 `server.relay_open` 记录 `from/to/transfer`，`relay.close` 记录字节数（R7 反证用），均不含令牌。

## 5. 私钥存储

| 平台 | 方式 | 证据 |
|---|---|---|
| Windows | DPAPI（用户作用域）封装后写盘，文件头 `FTKEYDP1`；目录 0700、文件 0600 | `core/identity/key_seal_test.go`（含“文件内不出现 PRIVATE KEY”） |
| 其他 | PKCS#8 PEM + 0600（Android 用应用私有目录，受系统沙箱保护） | `TestLoadLegacyPlainPEM` 保证旧文件仍可读 |
| 兼容 | 无 `FTKEYDP1` 头的旧 PEM 文件仍可加载 | 同上 |

## 6. 防滥用（服务端）

| 项 | 值 | 位置 |
|---|---|---|
| 登录挑战有效期 | 30 s | `server.Limits.LoginTTL` |
| 信令空闲断开 | 90 s（客户端 15 s 心跳） | `PingIdle` |
| 中继房间上限 / 空闲 | 50 间 / 60 s | `MaxRelayRooms`、`RelayIdle` |
| 每房间带宽 | 16 MiB/s，超预算挂起至下一秒（不缓冲） | `Limits.RelayRate`、`room.throttle`、`TestRoomThrottleParksOverBudget` |
| 中继令牌 | 24 hex，61 s 过期，一次性 | `server/server.go` `openRelay` |
| 配对消息限频 | 每设备 30 条/分钟，超出 `rate_limited` | `Server.allowPair` + `TestPairBudgetPerPeer` |
| 登录尝试限频（按 IP） | 每源地址 10 次/分钟，超出 429 并断开 | `Server.allowLogin` + `TestLoginBudgetPerIP` |
| Web 解锁失败 | 8 次/5 分钟，失败固定 350 ms 延迟 | `server/web.go` `WebLimits` |
| Web 6 位码查询 | 20 次/5 分钟，200 ms 假延迟，不返回设备列表 | 同上 |
| Web 设备注册有效期 | 10 分钟心跳续期 | `WebLimits.DeviceTTL` |

## 7. 已收尾与边界说明

- 桌面端界面采用纯 Win32 GDI + DWM Acrylic 自绘，不嵌入 WebView2 控件，无浏览器渲染引擎攻击面。
- IPv6 直连探测在本阶段暂未实现；跨网直连仅在 IPv4 打洞成功并完成内层 TLS 1.3 双向钉定校验后启用，失败自动回退受端到端加密保护的 WSS 中继。
- 安卓端已完成包名迁移至 `cn.cwlindean.ferry`，并支持 App 内 6 位码 SPAKE2 配对、`ferry://` 扫码与 `ftpair://` 导入/扫码二次确认弹窗。
- 按 IP 的限频目前已覆盖登录限频（`Server.allowLogin`）与已登录设备的配对限频（`Server.allowPair`）；未登录握手入口的速率可在反代层按需追加 `limit_req`。
