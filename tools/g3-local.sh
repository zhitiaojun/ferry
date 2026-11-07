#!/usr/bin/env bash
# G3 checks: unauthorized peer rejection and reconnect after restart.
set -u
cd "$(dirname "$0")/.."
LOG=evidence/stage3/20261107-2230-g3.log
mkdir -p evidence/stage3
exec > >(tee "$LOG") 2>&1

FT=.tools/bin/ft.exe
RECV_ID=a7fd14b608f4041fb9a546d634e93790f16c30bb4c5290120d24a1272393b859
SEND_ID=eabd863d00577abea6d2357af4ab70b05ae32566b3edd6a2e899cdb60a53edde

echo "# $(date -Iseconds) G3: unauthorized rejection + reconnect"

echo "## create a stranger key (not paired anywhere)"
$FT whoami --key .tools/keys/stranger.key
STRANGER_ID=$($FT whoami --key .tools/keys/stranger.key | sed 's/^device_id //')
echo "stranger_id=$STRANGER_ID"

# stranger trusts recv, but recv does not trust stranger
rm -f .tools/keys/paired-stranger.json .tools/keys/paired-recv2.json
URI_RECV=$($FT pair --show --key .tools/keys/recv.key --name recv-bj --paired .tools/keys/paired-recv2.json | python -c "import sys,json;print(json.load(sys.stdin)['uri'])")
$FT pair --add "$URI_RECV" --paired .tools/keys/paired-stranger.json >/dev/null
$FT pair --add "$($FT pair --show --key .tools/keys/send.key --name send-bj --paired .tools/keys/paired-recv2.json | python -c "import sys,json;print(json.load(sys.stdin)['uri'])")" --paired .tools/keys/paired-recv2.json >/dev/null

echo "## receiver trusts only send-bj:"
$FT pair --list --paired .tools/keys/paired-recv2.json

rm -rf .tools/recv-g3 && mkdir -p .tools/recv-g3
echo "## unauthorized send (stranger, recv will refuse to pin it)"
$FT recv --signal wss://ft.cwlindean.cn/signal --key .tools/keys/recv.key \
  --paired .tools/keys/paired-recv2.json --out .tools/recv-g3 \
  --log .tools/g3-recv.jsonl &
RECV_PID=$!
sleep 2
$FT send --signal wss://ft.cwlindean.cn/signal --allow relay \
  --key .tools/keys/stranger.key --paired .tools/keys/paired-stranger.json \
  --to-id "$RECV_ID" --file .tools/testdata/test-2.1m.bin \
  --timeout 10s --log .tools/g3-stranger-send.jsonl
echo "stranger_send_exit=$?"
sleep 1
wait $RECV_PID 2>/dev/null
echo "recv_exit=$?"
echo "## receiver log tail"
tail -c 600 .tools/g3-recv.jsonl

echo "## authorized reconnect: recv restarts, send again"
rm -rf .tools/recv-g3b && mkdir -p .tools/recv-g3b
$FT recv --signal wss://ft.cwlindean.cn/signal --key .tools/keys/recv.key \
  --paired .tools/keys/paired-recv.json --out .tools/recv-g3b \
  --log .tools/g3b-recv.jsonl &
RECV2=$!
sleep 2
$FT send --signal wss://ft.cwlindean.cn/signal --allow relay \
  --key .tools/keys/send.key --paired .tools/keys/paired-send.json \
  --to-id "$RECV_ID" --file .tools/testdata/test-2.1m.bin \
  --log .tools/g3b-send.jsonl
SECOND_RC=$?
echo "second_send_exit=$SECOND_RC"
wait $RECV2
echo "second_recv_exit=$?"
sha256sum .tools/testdata/test-2.1m.bin .tools/recv-g3b/test-2.1m.bin

if [ ! -e .tools/recv-g3/test-2.1m.bin ] && [ "$SECOND_RC" = "0" ]; then
  echo "CHECK PASS stranger refused, paired reconnect delivered"
  echo "G3_LOCAL=PASS"
else
  echo "CHECK FAIL see logs above"
  echo "G3_LOCAL=FAIL"
fi
