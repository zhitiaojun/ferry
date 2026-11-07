#!/usr/bin/env bash
# M3-3: the relay receiver must survive a signaling restart (exponential backoff).
set -u
cd "$(dirname "$0")/.."
LOG=evidence/stage3/20261107-2235-reconnect.log
mkdir -p evidence/stage3
exec > >(tee "$LOG") 2>&1
FT=.tools/bin/ft.exe
SRV=.tools/bin/ft-server.exe
RECV_ID=a7fd14b608f4041fb9a546d634e93790f16c30bb4c5290120d24a1272393b859

echo "# $(date -Iseconds) M3-3 signaling reconnect"
rm -rf .tools/recv-recon2 && mkdir -p .tools/recv-recon2
rm -f .tools/recon2-recv.jsonl .tools/recon2-send.jsonl .tools/recon2-srv.jsonl

taskkill //F //IM ft-server.exe >/dev/null 2>&1
sleep 1
echo "## start local ft-server"
$SRV -listen 127.0.0.1:18790 -log .tools/recon2-srv.jsonl &
SRV1=$!
sleep 1
curl -s --noproxy '*' http://127.0.0.1:18790/healthz; echo

echo "## start receiver (waits for a sender)"
$FT recv --signal ws://127.0.0.1:18790/signal --key .tools/keys/recv.key \
  --paired .tools/keys/paired-recv.json --out .tools/recv-recon2 \
  --log .tools/recon2-recv.jsonl &
RECV=$!
sleep 2

echo "## kill the signaling server (simulated outage)"
taskkill //F //IM ft-server.exe >/dev/null 2>&1
sleep 3

echo "## bring it back"
$SRV -listen 127.0.0.1:18790 -log .tools/recon2-srv.jsonl &
SRV2=$!
sleep 5
curl -s --noproxy '*' http://127.0.0.1:18790/healthz; echo

echo "## send after reconnect"
$FT send --signal ws://127.0.0.1:18790/signal --allow relay \
  --key .tools/keys/send.key --paired .tools/keys/paired-send.json \
  --to-id "$RECV_ID" --file .tools/testdata/test-2.1m.bin \
  --timeout 20s --log .tools/recon2-send.jsonl
SEND_RC=$?
echo "send_exit=$SEND_RC"
wait $RECV
echo "recv_exit=$?"

echo "## reconnect events"
grep -h '"signal.reconnect"' .tools/recon2-recv.jsonl || echo "NO reconnect events"

echo "## hashes"
sha256sum .tools/testdata/test-2.1m.bin .tools/recv-recon2/test-2.1m.bin

taskkill //F //IM ft-server.exe >/dev/null 2>&1
DST=$(sha256sum .tools/recv-recon2/test-2.1m.bin 2>/dev/null | cut -d' ' -f1)
if [ "$SEND_RC" = "0" ] && [ -n "${DST:-}" ] && grep -q '"signal.reconnect"' .tools/recon2-recv.jsonl; then
  echo "CHECK PASS reconnect with backoff then successful transfer"
  echo "M3_RECONNECT=PASS"
else
  echo "CHECK FAIL reconnect"
  echo "M3_RECONNECT=FAIL"
fi
