#!/usr/bin/env bash
# 采样 BJ 上 ft-server 的存活/内存/在线数/中继字节，输出一行 JSON。
set -euo pipefail
ssh BJ 'systemctl show ft-server -p ActiveState,SubState,MemoryCurrent,MemoryMax,NRestarts --no-pager | sed "s/^/sys./"; curl -fsS --max-time 5 http://127.0.0.1:18790/healthz; curl -fsS --max-time 5 http://127.0.0.1:18790/metrics'
