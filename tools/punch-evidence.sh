#!/usr/bin/env bash
# Local punch-path evidence: two processes on this host exchange candidates
# through a local ft-server and transfer files over the QUIC tunnel.
set -u
cd "$(dirname "$0")/.." || exit 1

LOG=${1:-evidence/stage2/$(date +%Y%m%d-%H%M)-punch.log}
BIN=.tools/bin/ft.exe
SERVER=.tools/bin/ft-server.exe
RECV_ID=a7fd14b608f4041fb9a546d634e93790f16c30bb4c5290120d24a1272393b859
OUT=.tools/punch-recv
SIGNAL=ws://127.0.0.1:18790/signal

mkdir -p "$(dirname "$LOG")" "$OUT"

exec > >(tee -a "$LOG") 2>&1
echo "=== M4 punch evidence $(date -Iseconds) ==="
echo "--- build info ---"
"$BIN" -version 2>&1 | head -4
"$SERVER" -version 2>&1 | head -2
echo "--- local server ---"
if ! curl -fsS --max-time 1 http://127.0.0.1:18790/healthz >/dev/null 2>&1; then
  "$SERVER" -listen 127.0.0.1:18790 -log .tools/punch-server.jsonl &
  SRV=$!
  sleep 1
else
  SRV=""
fi
curl -fsS --max-time 2 http://127.0.0.1:18790/healthz || echo "server healthz missing"

run_once() {
  local file=$1 name=$2
  rm -rf "$OUT"/* 
  rm -f .tools/punch-recv.jsonl .tools/punch-send.jsonl
  "$BIN" recv --signal "$SIGNAL" --punch --key .tools/keys/recv.key \
    --paired .tools/keys/paired-recv.json --out "$OUT" --log .tools/punch-recv.jsonl \
    > .tools/punch-recv.out 2>&1 &
  local RPID=$!
  sleep 2
  echo "--- send $name ($file) ---"
  "$BIN" send --signal "$SIGNAL" --punch --allow punch --key .tools/keys/send.key \
    --paired .tools/keys/paired-send.json --to-id "$RECV_ID" --file "$file" \
    --log .tools/punch-send.jsonl --timeout 30s
  local rc=$?
  wait $RPID 2>/dev/null
  echo "send_exit=$rc"
  echo "local  sha256: $(sha256sum "$file" | awk '{print $1}')"
  echo "remote sha256: $(sha256sum "$OUT/$(basename "$file")" | awk '{print $1}')"
  if cmp -s "$file" "$OUT/$(basename "$file")"; then echo "CHECK hash PASS for $name"; else echo "CHECK hash FAIL for $name"; fi
  grep -h 'transfer.done' .tools/punch-send.jsonl | tail -1
}

run_once .tools/testdata/test-2.1m.bin 2.1MB
run_once .tools/testdata/test-10m.bin 10MB

if [ -n "${SRV:-}" ]; then
  kill "$SRV" 2>/dev/null
fi
echo "PUNCH_EVIDENCE_DONE"
