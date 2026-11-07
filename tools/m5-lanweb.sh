#!/usr/bin/env bash
# M5 lanweb evidence: browser-facing HTTP server over the LAN.
#
#   tools/m5-lanweb.sh [out.log]
#
# Starts `ft recv --web` on a fixed port with a known token, uploads two test
# files with curl, downloads them again, and checks hashes and server memory.
# The page itself is plain HTTP + one script, so curl exercises the same paths
# the browser uses (multipart upload, list JSON, ranged download).
set -uo pipefail

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT" || exit 1

OUT=${1:-evidence/stage2/$(date +%Y%m%d-%H%M)-lanweb.log}
PORT=${PORT:-18081}
TOKEN=${TOKEN:-m5demo}
DIR=.tools/m5web
FILES=".tools/testdata/test-2.1m.bin .tools/testdata/test-10m.bin"

mkdir -p "$(dirname "$OUT")" "$DIR"
: >"$OUT"

say() { echo "$@" | tee -a "$OUT"; }
run() {
  say "\$ $*"
  "$@" 2>&1 | tee -a "$OUT"
  say "exit=${PIPESTATUS[0]}"
}

say "== M5 lanweb $(date '+%F %T %z') =="
run .tools/go/bin/go.exe version
run bash -c "go version 2>/dev/null || true"

rm -rf "$DIR"/* 2>/dev/null
say "-- start lanweb on 0.0.0.0:$PORT --"
.tools/bin/ft.exe recv --web --out "$DIR" --web-port "$PORT" --web-token "$TOKEN" --log .tools/m5web.jsonl >.tools/m5web.out 2>&1 &
SRV=$!
for i in $(seq 1 30); do
  code=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/$TOKEN/" || true)
  [ "$code" = "200" ] && break
  sleep 0.3
done
say "page_status=$code"
cat .tools/m5web.out | tee -a "$OUT"

ps_rss() {
  powershell -NoProfile -Command "(Get-Process ft -ErrorAction SilentlyContinue | Measure-Object -Property WorkingSet64 -Sum).Sum" 2>/dev/null | tr -d '\r'
}
RSS0=$(ps_rss)
say "rss_before_bytes=${RSS0:-0}"

for f in $FILES; do
  base=$(basename "$f")
  want=$(sha256sum "$f" | awk '{print $1}')
  say "-- upload $base ($(stat -c %s "$f") bytes) --"
  curl -s -F "file=@$f" "http://127.0.0.1:$PORT/$TOKEN/upload" | tee -a "$OUT"
  echo | tee -a "$OUT"
  got=$(curl -s "http://127.0.0.1:$PORT/$TOKEN/api/list" | tr ',' '\n' | grep -A2 "$base" | grep sha256 | cut -d'"' -f4)
  say "want_sha=$want"
  say "srv_sha=$got"
  [ "$want" = "$got" ] && say "CHECK upload $base PASS" || say "CHECK upload $base FAIL"

  say "-- download $base --"
  curl -s "http://127.0.0.1:$PORT/$TOKEN/f/$base" -o "$DIR.dl.bin"
  back=$(sha256sum "$DIR.dl.bin" | awk '{print $1}')
  say "back_sha=$back"
  [ "$want" = "$back" ] && say "CHECK download $base PASS" || say "CHECK download $base FAIL"
  rm -f "$DIR.dl.bin"
done

RSS1=$(ps_rss)
say "rss_after_bytes=${RSS1:-0}"
say "token_guard=$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/")"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/")" = "404" ] && say "CHECK token guard PASS" || say "CHECK token guard FAIL"
[ "$(curl -s -o /dev/null -w '%{http_code}' "http://127.0.0.1:$PORT/$TOKEN/f/../secret")" != "200" ] && say "CHECK traversal PASS" || say "CHECK traversal FAIL"

kill "$SRV" 2>/dev/null
wait "$SRV" 2>/dev/null
say "-- server stopped --"

fails=$(grep -c "CHECK .* FAIL" "$OUT" || true)
if [ "${fails:-0}" -eq 0 ]; then say "M5_LANWEB=PASS"; else say "M5_LANWEB=FAIL checks=$fails"; fi
