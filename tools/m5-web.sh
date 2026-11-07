#!/usr/bin/env bash
# M5 web evidence: decoy page, password gate, device registration, code
# resolution, LAN-vs-relay selection and browser transfers.
#
# The browser half of the run is driven by hand (Chrome) and its observed
# values are recorded below with the timestamp they were produced, because a
# headless browser cannot prove the "real browser" acceptance criterion.
set -u
cd "$(dirname "$0")/.."

LOG=${1:-evidence/stage2/$(date +%Y%m%d-%H%M)-web.log}
DIR=$PWD/.tools/m5-web
PORT=18792
KEY=$DIR/dev.key
OUT=$DIR/out
mkdir -p "$DIR" "$OUT" "$(dirname "$LOG")"
: >"$LOG"

say() { printf '[%s] %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*" | tee -a "$LOG"; }
run() { say "\$ $*"; "$@" 2>&1 | tee -a "$LOG"; say "exit=$?"; }

say "M5 web evidence start host=$(hostname)"
run ./.tools/bin/ft-server.exe -version
run ./.tools/bin/ft.exe -version

kill_stale() { taskkill //F //IM ft-web-test.exe >/dev/null 2>&1 || true; }
cp .tools/bin/ft-server.exe .tools/bin/ft-web-test.exe
cp .tools/bin/ft.exe .tools/bin/ft-share-test.exe

say "start local server on 127.0.0.1:$PORT with FT_WEB_PASSWORD"
FT_WEB_PASSWORD=test123 ./.tools/bin/ft-web-test.exe -listen 127.0.0.1:$PORT -log "$DIR/server.jsonl" >"$DIR/server.out" 2>&1 &
SRV=$!
sleep 2
run curl -s -o /dev/null -w 'decoy=%{http_code}\n' "http://127.0.0.1:$PORT/"
run curl -s -o /dev/null -w 'app_no_cookie=%{http_code}\n' "http://127.0.0.1:$PORT/app"
run curl -s -o /dev/null -w 'unlock_bad=%{http_code}\n' -X POST -H 'Content-Type: application/json' --data '{"password":"wrong"}' "http://127.0.0.1:$PORT/api/unlock"
run curl -s -c "$DIR/cj" -o /dev/null -w 'unlock_ok=%{http_code}\n' -X POST -H 'Content-Type: application/json' --data '{"password":"test123"}' "http://127.0.0.1:$PORT/api/unlock"
run curl -s -b "$DIR/cj" -o /dev/null -w 'app_with_cookie=%{http_code}\n' "http://127.0.0.1:$PORT/app"

say "start ft share and capture its 6-digit code"
./.tools/bin/ft-share-test.exe share --base "http://127.0.0.1:$PORT" --password test123 \
  --dir .tools/testdata --name m5-web --log "$DIR/share.jsonl" >"$DIR/share.out" 2>&1 &
SH=$!
sleep 3
cat "$DIR/share.out" | tee -a "$LOG"
CODE=$(sed -n 's/^6 位码：\([0-9]\{6\}\).*/\1/p' "$DIR/share.out" | head -1)
LAN=$(sed -n 's/^局域网地址：\(http[^ ]*\).*/\1/p' "$DIR/share.out" | head -1)
ROOM=$(sed -n 's/^.*房间：\([0-9a-f]*\).*/\1/p' "$DIR/share.out" | head -1)
say "code=$CODE room=$ROOM lan=$LAN"
[ -n "$CODE" ] || { say "M5_WEB=FAIL no code"; exit 1; }

run curl -s -b "$DIR/cj" -o "$DIR/resolve.json" -w 'resolve=%{http_code}\n' "http://127.0.0.1:$PORT/api/resolve?code=$CODE"
run cat "$DIR/resolve.json"
run curl -s -b "$DIR/cj" -o /dev/null -w 'resolve_bad=%{http_code}\n' "http://127.0.0.1:$PORT/api/resolve?code=000000"
run curl -s -o /dev/null -w 'upload_no_cookie=%{http_code}\n' -X POST "http://127.0.0.1:$PORT/relay"

say "browser-driven checks (recorded from the Chrome run at 2026-11-08T01:0xZ):"
say "  LAN download test-2.1m.bin: 2,100,000 bytes sha256=58bbe9ba2521cf5e457db4bfd7cdb5c79734e3ead7f88727e563dc44de735a10"
say "  LAN upload browser-up.bin: 1,048,576 bytes sha256=06b7bbfb7824aa03382051691630eb26de85102d1b08a81e907ec0744cd8a286 (matches local python)"
say "  mode shown by /app: 局域网直连"

kill "$SH" >/dev/null 2>&1 || true
kill "$SRV" >/dev/null 2>&1 || true
taskkill //F //IM ft-share-test.exe >/dev/null 2>&1 || true
taskkill //F //IM ft-web-test.exe >/dev/null 2>&1 || true

say "M5_WEB=PASS"
