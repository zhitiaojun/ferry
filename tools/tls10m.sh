#!/usr/bin/env bash
# M3: 10MB over the relay with the pinned TLS 1.3 layer.
set -u
cd "$(dirname "$0")/.."
LOG=evidence/stage3/20261107-2220-tls10m.log
mkdir -p evidence/stage3
exec > >(tee "$LOG") 2>&1

echo "# $(date -Iseconds) M3 relay TLS 10MB"
echo "## go $(.tools/go/bin/go.exe version 2>/dev/null || echo 'go missing')"
echo "## ft-server deployed at ft.cwlindean.cn, TLS 1.3 pinning via paired keys"

rm -rf .tools/recv-tls10 .tools/tls10-recv.jsonl .tools/tls10-send.jsonl
mkdir -p .tools/recv-tls10

echo "## start receiver"
.tools/bin/ft.exe recv \
  --signal wss://ft.cwlindean.cn/signal \
  --key .tools/keys/recv.key \
  --paired .tools/keys/paired-recv.json \
  --out .tools/recv-tls10 \
  --log .tools/tls10-recv.jsonl &
RECV_PID=$!
sleep 2

echo "## send 10MB"
.tools/bin/ft.exe send \
  --signal wss://ft.cwlindean.cn/signal \
  --allow relay \
  --key .tools/keys/send.key \
  --paired .tools/keys/paired-send.json \
  --to-id a7fd14b608f4041fb9a546d634e93790f16c30bb4c5290120d24a1272393b859 \
  --file .tools/testdata/test-10m.bin \
  --log .tools/tls10-send.jsonl | tee .tools/tls10-summary.json
SEND_RC=${PIPESTATUS[0]}
echo "send_exit=$SEND_RC"

wait $RECV_PID
echo "recv_exit=$?"

echo "## hashes"
sha256sum .tools/testdata/test-10m.bin .tools/recv-tls10/test-10m.bin

echo "## relay.tls events"
grep -h '"relay.tls"' .tools/tls10-send.jsonl .tools/tls10-recv.jsonl || echo "NO relay.tls events"

echo "## server memory"
ssh BJ "systemctl show ft-server -p MemoryCurrent -p MemoryPeak -p MemoryMax" 2>/dev/null || echo "ssh unavailable"

SRC=$(sha256sum .tools/testdata/test-10m.bin | cut -d' ' -f1)
DST=$(sha256sum .tools/recv-tls10/test-10m.bin 2>/dev/null | cut -d' ' -f1)
if [ "$SRC" = "$DST" ] && grep -q '"ok":true' .tools/tls10-summary.json && grep -q '"relay.tls"' .tools/tls10-send.jsonl; then
  echo "CHECK PASS relay tls 10MB hash and pinning"
  echo "M3_TLS10M=PASS"
else
  echo "CHECK FAIL relay tls 10MB (src=$SRC dst=$DST)"
  echo "M3_TLS10M=FAIL"
fi
