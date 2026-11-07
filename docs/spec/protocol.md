# 传输协议规范（protocol）v1.0-draft

> 状态：**M0 产出，待 G0 评审**。评审通过后冻结为 v1.0；此后修改帧格式或消息语义，必须先改本文件并经用户确认（`WORK_AGENT.md` §4）。
> 本文件是**帧格式、会话消息、状态机、错误码、信令消息的唯一权威来源**。设计动机见 `docs/context/design.md` §2–§7；决策依据见 `DECISIONS.md`。
> 测试向量：`docs/spec/vectors/`（`frames.json` 合法帧、`invalid.json` 非法帧）。所有端必须用同一组向量做一致性测试。

## 0. 范围与传输假设

1. 本规范分两层：
   - **会话协议**（§2–§7）：设备之间的文件传输，运行在可靠、有序的字节流上。
   - **信令协议**（§8）：客户端与 ft-server 之间的控制面，运行在 WebSocket（WSS）上，消息为 JSON。
2. 帧不吃 MTU：M1 数据块 64 KiB，整帧约 64 KiB；由底层字节流（TCP / QUIC stream / 中继 WSS）负责分片与重传。
3. 一个 **session** 由一次 HELLO 建立；同一 session 可以先后使用多条路径（LAN → 打洞 → 中继，D1–D3），`session_id` 与偏移状态跨路径保持。
4. 会话协议自身**不做超时重传**：可靠流保证字节不丢；路径失效由选路层在 3 s 无进展时降级（design §5），切换后由接收端 ACK 的累计偏移继续。
5. 所有整数字段为**网络字节序（大端）**；字符串为 UTF-8，长度前缀为 u16。
6. 长度单位：字段表中 `u8/u16/u32/u64/i64` 为定长整数；`bytes[n]` 为定长字节串。

## 1. 帧格式

### 1.1 通用头（36 字节）

```
 0                   1                   2                   3
 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1 2 3 4 5 6 7 8 9 0 1
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          magic "FTRX"                         |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|   version     |     type      |            flags              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          session_id (16)                      |
|                              ...                              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                            seq (u64)                          |
|                              ...                              |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                          length (u32)                         |
+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+-+
|                        payload (length)                       |
```

| 偏移 | 长度 | 字段 | 规则 |
|---|---|---|---|
| 0 | 4 | `magic` | 固定 ASCII `FTRX`（`46 54 52 58`），否则按 `bad_magic` 关闭连接 |
| 4 | 1 | `version` | 主版本，当前 `1`；不支持则回 `ERROR(version_unsupported)` 后关闭 |
| 5 | 1 | `type` | 见 §3 消息总表 |
| 6 | 2 | `flags` | 保留。发送方必须置 0；接收方忽略未知位 |
| 8 | 16 | `session_id` | 会话标识，HELLO 由发起方生成，之后不变；**全零非法** |
| 24 | 8 | `seq` | 每方向独立、从 0 开始、逐帧加 1（见 §1.3） |
| 32 | 4 | `length` | 载荷字节数，上限 `1048576`（1 MiB），否则 `frame_too_large` |
| 36 | `length` | `payload` | 各消息定义见 §3 |

### 1.2 合法性检查（按顺序）

| 顺序 | 检查 | 失败处理 |
|---|---|---|
| 1 | 收到 ≥36 字节且 `magic == FTRX` | 不匹配：不解析、关闭路径、日志 `bad_magic`（可能是误连到非本协议端口） |
| 2 | `version == 1` | 回 `ERROR(0x0001)`，关闭 |
| 3 | `length <= 1 MiB` | 回 `ERROR(0x0004)`，关闭（无法安全跳过） |
| 4 | `session_id != 0` | 回 `ERROR(0x0003)`，关闭 |
| 5 | `type` 已知 | 未知类型：回 `ERROR(0x0005)` 并**跳过该帧**继续解析 |
| 6 | 载荷长度与消息定义相符 | 回 `ERROR(0x0003)` 并跳过该帧（定长不足即无法跳过时关闭） |

一帧只承载一条消息，**不分片**。

### 1.3 seq 语义

- 发送端每发一帧 `seq++`，从 0（HELLO）开始；ACK、PING、ERROR 同样占用 seq，保证“一帧一号”。
- 接收端记录 `last_seq`；正常情况应严格递增。
- 出现 `seq <= last_seq` 的帧：**丢弃并记日志，不致命**（路径切换或重放可能造成重复）。若同 seq 的帧内容不同，记 `protocol_violation` 但不断开会话。
- seq 不参与去重；去重以 DATA 的 `offset` 为准（§5）。

## 2. 会话标识与路径

- `session_id`：16 字节，由发起方用 `crypto/rand` 生成；接收方 HELLO_ACK 沿用。
- 路径层在上、会话层在下（design §4）：LAN TCP（M2 起 TLS）、QUIC（M4）、中继 WSS 内层 TLS（M3）。
- 换路径时**不重发 HELLO**，`session_id`、seq、偏移状态全部保留；新路径完成传输层握手后直接继续收发会话帧。
- 中继路径对会话层是不可见字节流；服务端只做拼接，不解析帧内容（M3 起内层 TLS，服务端只见密文）。

## 3. 消息

### 3.1 总表

| type | 名称 | 方向 | 引入里程碑 | 用途 |
|---|---|---|---|---|
| 0x01 | `HELLO` | 发送方 → 接收方 | M1 | 版本协商、身份、能力、nonce |
| 0x02 | `HELLO_ACK` | 接收方 → 发送方 | M1 | 接受会话、回显 nonce |
| 0x03 | `MANIFEST` | 发送方 → 接收方 | M1 | 文件清单与分块参数 |
| 0x04 | `DATA` | 发送方 → 接收方 | M1 | 数据块 + 块哈希 |
| 0x05 | `ACK` | 接收方 → 发送方 | M1 | 累计偏移确认 |
| 0x06 | `RESUME` | 接收方 → 发送方 | M7（M1 仅定义） | 续传起点 |
| 0x07 | `FIN` | 双向 | M1 | 结束、取消、整文件哈希 |
| 0x08 | `ERROR` | 双向 | M1 | 错误码 + 说明 |
| 0x09 | `PING` | 双向 | M1 | 保活 |
| 0x0A | `PONG` | 双向 | M1 | PING 回显 |

> 0x0B–0x7F 保留给会话层扩展；0x80–0xFF 保留给私有实验，不得用于与服务器交互。

### 3.2 HELLO（0x01）

| 字段 | 类型 | 说明 |
|---|---|---|
| `min_version` | u16 | 发送方支持的最低协议版本（当前 1） |
| `max_version` | u16 | 发送方支持的最高版本 |
| `device_id` | bytes[32] | 设备 ID = SHA-256(Ed25519 公钥)；M1 骨架期可为随机 32 字节，M3 起必须为真实身份 |
| `nonce` | bytes[16] | 随机数，用于 HELLO_ACK 回显 |
| `capabilities` | u32 | 能力位，见 §3.2.1 |
| `name_len` | u16 | 设备名长度，0–64 |
| `name` | bytes[name_len] | UTF-8 设备名（可为空） |

版本选择：`chosen = max(共同支持)`；交集为空 → `ERROR(0x0001)`。

#### 3.2.1 capabilities 位

| 位 | 名称 | 含义 |
|---|---|---|
| 0 | `RESUME` | 支持 `.ftpart` + RESUME 续传（M7） |
| 1 | `MULTIPATH` | 支持跨路径切换（M4） |
| 2 | `TLS_PIN` | 支持按身份公钥钉定的 TLS（M3） |
| 3–31 | 保留 | 发送 0，接收忽略 |

### 3.3 HELLO_ACK（0x02）

| 字段 | 类型 | 说明 |
|---|---|---|
| `version` | u16 | 选定的协议版本 |
| `device_id` | bytes[32] | 接收方设备 ID |
| `nonce_echo` | bytes[16] | 原样回显 HELLO 的 `nonce` |
| `capabilities` | u32 | 接收方能力位 |
| `name_len` | u16 | 设备名长度，0–64 |
| `name` | bytes[name_len] | UTF-8 |

发送方必须校验 `nonce_echo` 与自己发出的 nonce 一致，否则 `ERROR(0x0003)` 并关闭。

### 3.4 MANIFEST（0x03）

| 字段 | 类型 | 说明 |
|---|---|---|
| `transfer_id` | bytes[16] | 本次传输 ID（发送方随机生成；续传时沿用，M7） |
| `chunk_size` | u32 | 数据块大小，范围 4096–262144，默认 65536 |
| `file_count` | u32 | 文件数，1–1024 |
| 每个文件（重复 `file_count` 次）： | | |
| `name_len` | u16 | 文件名长度，1–255 字节 |
| `name` | bytes[name_len] | UTF-8 文件名；**不得含** `/`、`\`、NUL、`.`、`..`；接收方必须清洗（只取最后一段、替换非法字符） |
| `size` | u64 | 文件字节数（≥0；空文件允许：发送方不发 DATA，接收方直接创建 0 字节文件） |
| `mtime_ms` | i64 | 修改时间（Unix 毫秒），仅用于显示与续传判定 |
| `file_sha256` | bytes[32] | 整文件 SHA-256；发送方未预计算时填 32 个 0，此时以 FIN 中的值为准 |

约束：
- `file_count`、文件名、总大小在接收方都要做上限检查（防滥用）；超限回 `ERROR(0x0007)`。
- 文件按清单顺序**串行**传输，同一时刻只有一个 `file_index` 在传。
- 发送方发送 MANIFEST 后等待接收方 `ACK(offset=0)` 才开始发 DATA。

### 3.5 DATA（0x04）

| 字段 | 类型 | 说明 |
|---|---|---|
| `transfer_id` | bytes[16] | 与 MANIFEST 一致 |
| `file_index` | u32 | 文件序号，从 0 开始 |
| `offset` | u64 | 文件内绝对偏移 |
| `chunk_sha256` | bytes[32] | 本块明文的 SHA-256 |
| `data` | bytes[剩余] | 块数据，长度 1–`chunk_size` |

约束：
- `offset` 必须是 `chunk_size` 的整数倍（最后一块除外）；最后一块长度 = `size - offset`。
- 接收端必须校验 `chunk_sha256`；不符 → `ERROR(0x0008)`，终止该传输（`.ftpart` 保留，供 M7 续传）。
- 接收端按 `offset` 落盘；重复块（§5）丢弃；非预期偏移 → `ERROR(0x000A)`。

### 3.6 ACK（0x05）

| 字段 | 类型 | 说明 |
|---|---|---|
| `transfer_id` | bytes[16] | 与 MANIFEST 一致 |
| `file_index` | u32 | 当前文件序号 |
| `offset` | u64 | 该文件**已连续收到**的字节数（累计确认） |

发送时机：收到 MANIFEST 后发 `offset=0`；传输中每累计 1 MiB 或每 200 ms（先到者）发一次；文件完成时发 `offset=size`。

### 3.7 RESUME（0x06）

| 字段 | 类型 | 说明 |
|---|---|---|
| `transfer_id` | bytes[16] | 断点传输 ID |
| `file_index` | u32 | 从哪个文件续传 |
| `offset` | u64 | 该文件已确认的偏移 |

M1 只定义格式、不实现；M7 实现时接收端必须先逐块复核已收数据，再发 RESUME。

### 3.8 FIN（0x07）

| 字段 | 类型 | 说明 |
|---|---|---|
| `status` | u8 | 0=OK，1=CANCELLED，2=FAILED，3=HASH_MISMATCH |
| `transfer_id` | bytes[16] | 与 MANIFEST 一致 |
| `file_count` | u32 | 文件数（必须与 MANIFEST 一致） |
| 每个文件： | | |
| `file_sha256` | bytes[32] | 该文件完整 SHA-256（发送方：计算值；接收方：计算值） |

- 空文件（`size=0`）不发送任何 DATA；其整文件哈希为 SHA-256 空串（`e3b0c442…b855`），并照常计入 `file_count`。
- 发送方发完最后一个 DATA 后发 `FIN(0/…, 各文件哈希)`；
- 接收方复核整文件哈希后回 `FIN(0)`；不一致回 `FIN(3)`，并在此之前发 `ERROR(0x0009)`；
- 双方都以收到对方 FIN 作为传输结束；收到 `FIN(≥1)` 视为失败，保留 `.ftpart`（M7）。
- 取消：任一端发 `FIN(1)`，对方停止并清理已写出的半成品（M1 在测试模式下由 `--keep-part` 控制）。

### 3.9 ERROR（0x08）

| 字段 | 类型 | 说明 |
|---|---|---|
| `code` | u16 | §7 错误码 |
| `msg_len` | u16 | 说明长度，0–512 |
| `message` | bytes[msg_len] | UTF-8，供日志与 CLI 显示，不得含密钥或文件内容 |

收到 ERROR 后：当前传输终止，会话进入 FAILED；是否关闭连接由错误码决定（`0x0001`、`0x0002`、`0x0004` 必须关闭）。

### 3.10 PING / PONG（0x09 / 0x0A）

| 字段 | 类型 | 说明 |
|---|---|---|
| `token` | bytes[8] | 随机数 |

- 无业务帧 15 s 时发 PING；收到 PING 立即回 PONG，token 原样回显。
- 收到 PONG 时若 token 匹配未决 PING，则刷新“对端存活”时间。
- 45 s 内未收到任何帧（含 PONG）→ 判定该路径失联（§6），交给选路层处理，不直接判会话失败。

## 4. 会话流程与状态机

### 4.1 建立

```
发送方                                    接收方
  |  HELLO(session_id, nonce, caps)  -->    |
  |  <--  HELLO_ACK(version, nonce_echo)    |
```

- 接收方在连接建立后 5 s 内未收到 HELLO → `ERROR(0x000D)` 并关闭。
- 发送方 5 s 内未收到 HELLO_ACK → 判该路径失败；有其他路径则切换，否则 `ERROR(0x000D)`。

### 4.2 传输时序

```
S                                          R
|  HELLO ------------------------------->  |
|  <------------------------------- HELLO_ACK
|  MANIFEST ---------------------------->  |
|  <------------------------------ ACK(0)  |   （接收端创建 .ftpart）
|  DATA(0, chunk) ---------------------->  |
|  ...                                     |
|  <---------------------- ACK(每 1 MiB)   |
|  DATA(last) ---------------------------> |
|  FIN(status=0, 各文件哈希) -------------> |
|  <----------------------- FIN(status=0)  |   （接收端复核后回）
```

多文件：文件 0 完成（`ACK(offset=size)`）后，发送方从文件 1 的 `offset=0` 继续，`file_index++`。

### 4.3 状态机（发送方）

| 状态 | 事件 | 动作 | 下一状态 |
|---|---|---|---|
| `INIT` | 开始发送 | 生成 session_id/nonce，发 HELLO | `HELLO_SENT` |
| `HELLO_SENT` | 收到 HELLO_ACK 且 nonce 匹配 | 记录对端信息 | `READY` |
| `HELLO_SENT` | 超时 / 收到 ERROR | 记失败，关闭或切路径 | `FAILED` |
| `READY` | 用户确认发送 / 已配对自动接收 | 发 MANIFEST | `MANIFEST_SENT` |
| `MANIFEST_SENT` | 收到 ACK(0) | 开始发 DATA | `TRANSFERRING` |
| `TRANSFERRING` | 继续发 DATA | — | `TRANSFERRING` |
| `TRANSFERRING` | 当前文件全部 ACK | 切下一个文件或发 FIN | `TRANSFERRING` / `FIN_SENT` |
| `FIN_SENT` | 收到 FIN(0) | 关闭会话 | `DONE` |
| `FIN_SENT` | 收到 FIN(≥1) | 记失败 | `FAILED` |
| 任意 | 本地取消 | 发 FIN(1) | `FAILED` |
| 任意 | 收到 ERROR | 记错误码 | `FAILED` |

### 4.4 状态机（接收方）

| 状态 | 事件 | 动作 | 下一状态 |
|---|---|---|---|
| `INIT` | 收到 HELLO | 校验版本/能力，发 HELLO_ACK | `READY` |
| `INIT` | 5 s 未收到 HELLO | 发 ERROR(0x000D)，关闭 | `FAILED` |
| `READY` | 收到 MANIFEST 且合法 | 创建 `.ftpart` + 侧车清单，发 ACK(0) | `RECEIVING` |
| `READY` | MANIFEST 非法 | 回 ERROR(0x0007) | `FAILED` |
| `RECEIVING` | 收到 DATA | 校验块哈希、按偏移落盘、按节奏 ACK | `RECEIVING` |
| `RECEIVING` | 收到 FIN | 复核整文件哈希 | `VERIFYING` |
| `VERIFYING` | 哈希一致 | 发 FIN(0)，`.ftpart` 改名 | `DONE` |
| `VERIFYING` | 哈希不一致 | 发 ERROR(0x0009) + FIN(3) | `FAILED` |
| 任意 | 收到 ERROR / 本地取消 | 清理或保留 `.ftpart` | `FAILED` |

### 4.5 不变式

1. `session_id` 在会话生命周期内不变；seq 只增不减。
2. 接收方只接受“下一个期望偏移”开始的数据；已确认偏移不可回退。
3. 任一方向收到 ERROR 后，不再发送 DATA。
4. 会话失败不等于路径失败；路径失败也不等于会话失败（可切换继续）。

## 5. 分块、哈希与去重

- 分块：`chunk_size` 由 MANIFEST 声明（默认 65536），所有文件统一；最后一块可短。
- 每块：`chunk_sha256` 覆盖该块明文；接收方校验通过才写盘（先写临时文件再原子追加，避免半块入盘）。
- 整文件：FIN 携带完整 SHA-256；接收方复核后回 FIN(0)。
- 去重：接收方维护本文件“已收到连续偏移” `recv_offset` 与块位图；DATA 的 `offset < recv_offset` 且整块已收 → 丢弃并 ACK；部分重叠或空洞 → `ERROR(0x000A)`。
- 流式：发送方读一块、发一块、可释放内存；接收方写一块、释放一块；内存不随文件大小线性增长（G1 判据）。

## 6. 超时、保活与路径降级（G0 定稿，2026-11-07）

| 参数 | 提案值 | 说明 |
|---|---|---|
| HELLO 等待 | 5 s | 建链超时，按路径失败处理 |
| PING 间隔 | 15 s | 无业务帧时发送 |
| 对端失联判定 | 45 s | 3 个 PING 周期无任何帧 |
| 路径无进展降级 | 3 s | design §5；由选路层执行，会话层只上报“无 ACK 进展” |
| LAN 候选窗口 | 0.5 s | D3；超时后中继先承载 |
| ACK 节奏 | 1 MiB / 200 ms | 先到者；避免小文件频繁 ACK |
| MANIFEST 至首块 | 10 s | 接收方建文件、校验清单；超时打日志并等待 |
| 中继空闲断开（服务端） | 60 s | design §10 |
| WSS 信令心跳 | 30 s（初值） | M3 按 ESA 实测调整（design §9） |
| 重连退避 | 1 s → 2 s → 4 s → 8 s，上限 30 s | 仅信令层 |

> 除 0.5 s（D3）外，以上由 work agent 提案，2026-11-07 G0 定稿。实测后可微调，但须在 `NOW.md` 记录并保持消息格式不变。

## 7. 错误码

| 码 | 名称 | 含义 | 关闭连接 |
|---|---|---|---|
| 0x0001 | `version_unsupported` | 协议版本无交集 | 是 |
| 0x0002 | `bad_magic` | 不是本协议流量 | 是 |
| 0x0003 | `protocol_violation` | 帧/状态非法（含 nonce 不符） | 视情况 |
| 0x0004 | `frame_too_large` | length > 1 MiB | 是 |
| 0x0005 | `unknown_type` | 未知消息类型 | 否 |
| 0x0006 | `unexpected_message` | 当前状态不接受该消息 | 否 |
| 0x0007 | `manifest_invalid` | 清单越界/文件名为空/文件名非法 | 否 |
| 0x0008 | `chunk_hash_mismatch` | 块哈希不符 | 否（终止传输） |
| 0x0009 | `file_hash_mismatch` | 整文件哈希不符 | 否（终止传输） |
| 0x000A | `offset_invalid` | 偏移不在期望位置 | 否 |
| 0x000B | `io_error` | 读写磁盘失败 | 否 |
| 0x000C | `cancelled` | 本地或对端取消 | 否 |
| 0x000D | `timeout` | 建链/保活超时 | 是 |
| 0x000E | `unauthorized` | 身份校验失败（M3 起） | 是 |
| 0x000F | `relay_session_not_found` | 中继会话不存在或过期（M3） | 是 |
| 0x0010 | `unsupported_chunk_size` | chunk_size 越界 | 否 |
| 0x0011 | `payload_invalid` | 载荷内部字段非法 | 否 |
| 0xFFFF | `internal` | 内部错误 | 否 |

## 8. 信令协议（客户端 ↔ ft-server，M3 实现）

### 8.1 传输与信封

- 传输：`wss://ft.cwlindean.cn/signal`（经 ESA → openresty 443 → `127.0.0.1:18790`）；M3 实测 ESA 的 WebSocket 与空闲超时。
- 信封：每条 WebSocket **文本消息**为一个 JSON 对象：

```json
{"v":1,"type":"login","id":"c-7f3a","ts":1762500000000,"data":{}}
```

| 字段 | 类型 | 说明 |
|---|---|---|
| `v` | number | 信封版本，当前 1 |
| `type` | string | 消息类型（§8.3） |
| `id` | string | 可选；请求方生成，响应必须原样回带 |
| `ts` | number | Unix 毫秒；仅诊断，不参与签名 |
| `data` | object | 类型相关字段 |

- 未知 `type`：回 `error{code:"unsupported_type"}`，不关闭连接。
- 单条消息上限 64 KiB；超出断开。

### 8.2 登录（Ed25519 挑战签名）

| 步骤 | 方向 | 消息 | data |
|---|---|---|---|
| 1 | C→S | `login_begin` | `{"device_id":"<64 hex>","pubkey":"<64 hex>"}` |
| 2 | S→C | `login_challenge` | `{"nonce":"<32 hex>","expires_in_ms":30000}` |
| 3 | C→S | `login` | `{"signature":"<128 hex>"}` |
| 4 | S→C | `login_ok` | `{"server_time":<ms>,"peers":[{"device_id":"...","online":true}]}` |

签名原文（ASCII，`\n` 分隔）：

```
FT-SIGNAL-V1
<device_id_hex>
<nonce_hex>
```

- 服务端校验 `SHA-256(pubkey) == device_id` 与签名；失败回 `error{code:"auth_failed"}` 并断开。
- 服务端不持久化配对关系，只维护内存中的“设备 ID → 在线连接”。

### 8.3 消息类型

| type | 方向 | data 要点 |
|---|---|---|
| `login_begin` / `login_challenge` / `login` / `login_ok` | C↔S | §8.2 |
| `presence` | S→C | `{"device_id":"...","online":true/false}` |
| `pair_begin` | C→S | `{"to_device":"...","method":"qr"/"code","proof":"..."}` |
| `pair_relay` | S→C | `{"from_device":"...","payload":"<base64>"}`（服务端只转发配对载荷） |
| `pair_result` | S→C | `{"ok":true,"peer":{"device_id":"...","pubkey":"..."}}` |
| `candidates` | 双向 | `{"to_device":"...","session_id":"...","candidates":[{"kind":"lan"/"ipv6"/"stun","addr":"ip:port","priority":0}]}` |
| `send_request` | C→S（→C） | `{"to_device":"...","session_id":"...","transfer_id":"...","files":[{"name":"...","size":123}]}` |
| `send_response` | C→S（→C） | `{"to_device":"...","transfer_id":"...","accept":true,"reason":""}` |
| `relay_open` | C→S | `{"to_device":"...","transfer_id":"..."}` |
| `relay_ready` | S→C | `{"relay_url":"wss://ft.cwlindean.cn/relay","token":"...","expires_in_ms":60000}` |
| `ping` / `pong` | C↔S | `{"t":<ms>}`；服务端回显 |
| `error` | S→C | `{"code":"...","message":"...","ref":"<id>"}` |

### 8.4 候选交换

- 双方各自收集 LAN / IPv6 / STUN 候选，通过 `candidates` 发给对方（服务端只转发给 `to_device`）。
- 候选带 `kind` 与 `addr`；`stun` 候选的公网地址由客户端向 BJ `123.57.253.208:40000/40001` 探测（UDP 直连，不经 ESA）。
- 服务端不校验候选内容，仅做在线投递；离线则回 `error{code:"peer_offline"}`。

### 8.5 中继开房

1. 发送方 `relay_open{to_device, transfer_id}`；
2. 服务端为两端签发同一 `token`（一次性、60 s 有效）并回 `relay_ready`；
3. 双方各自 WSS 连 `/relay?token=...`，服务端配对后拼接两条连接；
4. 拼接完成后两端在字节流内做 TLS 1.3 握手（钉定对方身份公钥），再跑会话帧；
5. 任一端断开 → 服务端关闭另一端，释放会话；每会话固定缓冲 2×64 KiB，空闲 60 s 断开。

### 8.6 配对

- **扫码（自有设备首选）**：电脑显示二维码，内容 `ftpair://v1/<device_id_hex>/<pubkey_hex>/<secret_b64url>`；手机扫码后经信令 `pair_begin{method:"qr", proof:HMAC-SHA256(secret, nonce)}`，电脑确认后双方把对方公钥写入已配对表。
- **6 位码（陌生设备 / 无摄像头）**：PAKE（SPAKE2，P-256）经信令通道协商临时密钥，成功后建立一次性信任；6 位码用后作废（M6 完整实现，M3 先做扫码）。
- 配对不经过服务端存储：服务端只转发 `pair_relay` 载荷。

### 8.7 心跳与断线

- 客户端每 30 s 发 `ping`，服务端回 `pong`；90 s 无任何消息断开。
- 断线后按 1/2/4/8 s（上限 30 s）退避重连；重连后重新 `login`。
- 重连不中断已建立的会话数据路径（LAN/打洞）；中继路径断开时按 §6 交由选路层降级。

## 9. 版本与兼容

- 主版本不兼容时拒绝（HELLO 版本协商）；同主版本内新增消息类型时，旧实现按 `unknown_type` 回 ERROR 并继续，不崩溃。
- 新增字段只能加在载荷尾部，且旧实现必须能按 `length` 跳过。

## 10. 安全边界（与里程碑对应）

- **M1–M2**：局域网骨架可明文（D4）；仅用合成文件测试（D10）。外网传输不得明文。
- **M3 起**：LAN TCP、QUIC、中继内层一律 TLS 1.3，按身份公钥钉定（D26）；服务器只做路由与字节拼接，看不到明文。
- 日志不得包含密钥、令牌、文件内容；`file_sha256` 可记录，文件名按需脱敏。

## 11. 测试向量

| 文件 | 内容 |
|---|---|
| `docs/spec/vectors/frames.json` | 合法帧：HELLO、HELLO_ACK、MANIFEST（2 文件 / 含空文件）、DATA、ACK、RESUME、FIN、ERROR、PING、PONG；每条含完整十六进制帧与期望解析结果 |
| `docs/spec/vectors/invalid.json` | 非法帧：magic 错、版本错、超长、全零 session、未知类型、载荷截断；期望错误名 |

向量由 `tools/gen_vectors.py`（独立于 Go 实现的 Python 编码器）生成，Go 测试 `core/proto/vectors_test.go` 解码并逐字段比对，避免“用实现生成实现”的循环验证。

## 12. 已定稿项（G0，2026-11-07）

1. §6 全部数值按提案值冻结；实测后可微调，但须记录在 `NOW.md` 且不改变消息格式。
2. `chunk_size` 上限 256 KiB 保留；M1 实测后按需调整。
3. 空文件（`size=0`）**支持**：不发 DATA，接收方直接创建 0 字节文件。
4. §8.3 信令字段留待 M3 开工时按 ESA 实测细化（不阻塞 M1）。
5. 多文件维持**串行**；流水线化不在 v1.0 范围。

当前无待评审项；后续变更仍须按 `WORK_AGENT.md` §4 请示。
