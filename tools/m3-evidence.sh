#!/usr/bin/env bash
# M3 evidence: deployed ft-server through ESA, signaling and relay transfers.
#
# usage: tools/m3-evidence.sh [logfile]
# Requires: .tools/bin/ft.exe (host build), .tools/keys/{send,recv}.key,
#           .tools/testdata/test-2.1m.bin and test-10m.bin.
set -uo pipefail
cd "$(dirname "$0")/.." || exit 1

SIGNAL="${SIGNAL:-wss://ft.cwlindean.cn/signal}"
LOG="${1:-evidence/stage2/$(date +%Y%m%d-%H%M).log}"
mkdir -p "$(dirname "$LOG")"
: > "$LOG"

FAILS=0

say() { printf '\n== %s\n' "$*" | tee -a "$LOG"; }
run() {
  printf '[%s] $ %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%:z')" "$*" | tee -a "$LOG"
  "$@" 2>&1 | tee -a "$LOG"
  local rc=${PIPESTATUS[0]}
  printf 'exit=%d\n' "$rc" | tee -a "$LOG"
  return $rc
}
check() { # check <label> <expected> <actual>
  if [ "$2" = "$3" ]; then
    printf 'CHECK PASS %s (%s)\n' "$1" "$3" | tee -a "$LOG"
  else
    printf 'CHECK FAIL %s (want %s got %s)\n' "$1" "$2" "$3" | tee -a "$LOG"
    FAILS=$((FAILS+1))
  fi
}

say "env"
run .tools/go/bin/go.exe version
run .tools/bin/ft.exe -version
run sha256sum .tools/testdata/test-2.1m.bin .tools/testdata/test-10m.bin

say "public endpoint (ESA -> BJ openresty -> 127.0.0.1:18790)"
CODE=$(curl -s -o /dev/null -w '%{http_code}' --max-time 20 https://ft.cwlindean.cn/healthz)
check "healthz_public" 200 "$CODE"
curl -s -D - -o /dev/null --max-time 20 https://ft.cwlindean.cn/healthz 2>&1 | tee -a "$LOG" | head -12

say "signaling login + pings over ESA"
run timeout 60 .tools/bin/ft-probe.exe -signal "$SIGNAL" -key .tools/keys/probe.key -duration 10s -ping 5s

say "relay 2.1 MB"
.tools/bin/ft.exe whoami --key .tools/keys/recv.key > .tools/m3-recv-id.txt 2>&1
tee -a "$LOG" < .tools/m3-recv-id.txt
RECV_ID=$(sed -n 's/^device_id //p' .tools/m3-recv-id.txt)
rm -rf .tools/m3-relay-2m; mkdir -p .tools/m3-relay-2m
.tools/bin/ft.exe recv --signal "$SIGNAL" --key .tools/keys/recv.key --name relay-recv \
    --out .tools/m3-relay-2m --log .tools/m3-relay-2m/recv.jsonl >/dev/null 2>&1 &
RPID=$!
sleep 3
run timeout 300 .tools/bin/ft.exe send --signal "$SIGNAL" --allow relay \
    --key .tools/keys/send.key --name relay-send --to-id "$RECV_ID" \
    --file .tools/testdata/test-2.1m.bin --log .tools/m3-relay-2m/send.jsonl
wait $RPID 2>/dev/null
H1=$(sha256sum .tools/testdata/test-2.1m.bin | awk '{print $1}')
H2=$(sha256sum .tools/m3-relay-2m/test-2.1m.bin 2>/dev/null | awk '{print $1}')
check "relay_2.1m_hash" "$H1" "$H2"

say "relay 10 MB"
rm -rf .tools/m3-relay-10m; mkdir -p .tools/m3-relay-10m
.tools/bin/ft.exe recv --signal "$SIGNAL" --key .tools/keys/recv.key --name relay-recv \
    --out .tools/m3-relay-10m --log .tools/m3-relay-10m/recv.jsonl >/dev/null 2>&1 &
RPID=$!
sleep 3
run timeout 600 .tools/bin/ft.exe send --signal "$SIGNAL" --allow relay \
    --key .tools/keys/send.key --name relay-send --to-id "$RECV_ID" \
    --file .tools/testdata/test-10m.bin --log .tools/m3-relay-10m/send.jsonl
wait $RPID 2>/dev/null
H1=$(sha256sum .tools/testdata/test-10m.bin | awk '{print $1}')
H2=$(sha256sum .tools/m3-relay-10m/test-10m.bin 2>/dev/null | awk '{print $1}')
check "relay_10m_hash" "$H1" "$H2"

say "server memory (256 MB cap)"
ssh BJ 'systemctl show ft-server -p MemoryCurrent -p MemoryMax; cat /sys/fs/cgroup/system.slice/ft-server.service/memory.peak 2>/dev/null; free -m | sed -n 2p' 2>&1 | tee -a "$LOG"

say "result"
if [ "$FAILS" -eq 0 ]; then echo "M3_EVIDENCE=PASS" | tee -a "$LOG"; else echo "M3_EVIDENCE=FAIL fails=$FAILS" | tee -a "$LOG"; fi
