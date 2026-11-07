#!/usr/bin/env bash
# M4 path-race evidence (D2): both paths start at t=0, the first session wins,
# both sides must agree on the path and the bytes must match.
#   case A: punch reachable  -> either path may win; both sides must agree
#   case B: FT_PUNCH_BLOCK=1 -> punch always fails; relay must win
set -u
cd "$(dirname "$0")/.."
LOG=${1:-evidence/stage2/$(date +%Y%m%d-%H%M)-race.log}
FT=.tools/bin/ft.exe
KEY_R=.tools/keys/recv.key
KEY_S=.tools/keys/send.key
PAIR_R=.tools/keys/paired-recv.json
PAIR_S=.tools/keys/paired-send.json
TOID=a7fd14b608f4041fb9a546d634e93790f16c30bb4c5290120d24a1272393b859
SIG=ws://127.0.0.1:18790/signal
F2=.tools/testdata/test-2.1m.bin
F10=.tools/testdata/test-10m.bin
fail=0

mkdir -p "$(dirname "$LOG")" .tools/race-a .tools/race-b
{
  echo "=== M4 path race evidence $(date -Iseconds) ==="
  "$FT" -version
  curl -fsS --max-time 2 http://127.0.0.1:18790/healthz || echo "local ft-server not running"

  run_case() { # name file outdir block
    local name=$1 file=$2 out=$3 block=$4
    echo "--- case $name: $file (FT_PUNCH_BLOCK=$block) ---"
    rm -rf "$out"; mkdir -p "$out"
    if [ "$block" = 1 ]; then
      FT_PUNCH_BLOCK=1 "$FT" recv --signal "$SIG" --punch --key "$KEY_R" --paired "$PAIR_R" \
        --out "$out" --log "$out/recv.jsonl" > "$out/recv.out" 2>&1 &
    else
      "$FT" recv --signal "$SIG" --punch --key "$KEY_R" --paired "$PAIR_R" \
        --out "$out" --log "$out/recv.jsonl" > "$out/recv.out" 2>&1 &
    fi
    local rpid=$!
    sleep 2
    if [ "$block" = 1 ]; then
      FT_PUNCH_BLOCK=1 "$FT" send --signal "$SIG" --punch --allow punch,relay --key "$KEY_S" \
        --paired "$PAIR_S" --to-id "$TOID" --file "$file" --timeout 20s \
        --log "$out/send.jsonl"
    else
      "$FT" send --signal "$SIG" --punch --allow punch,relay --key "$KEY_S" \
        --paired "$PAIR_S" --to-id "$TOID" --file "$file" --timeout 20s \
        --log "$out/send.jsonl"
    fi
    echo "send_exit=$?"
    wait "$rpid" || true
    cat "$out/recv.out"
    local base; base=$(basename "$file")
    echo "recv_sha  $(sha256sum "$out/$base" 2>/dev/null || echo missing)"
    echo "src_sha   $(sha256sum "$file")"
    cmp -s "$out/$base" "$file" && echo "CHECK $name hash PASS" || { echo "CHECK $name hash FAIL"; fail=1; }
    local sp rp
    sp=$(grep -o '"path":"[a-z]*"' "$out/send.jsonl" 2>/dev/null | head -1)
    rp=$(grep -o '"path":"[a-z]*"' "$out/recv.out" 2>/dev/null | head -1)
    echo "send_$sp recv_$rp"
    [ -n "$sp" ] && [ "$sp" = "$rp" ] && echo "CHECK $name same path PASS" || { echo "CHECK $name same path FAIL"; fail=1; }
    if [ "$block" = 1 ]; then
      echo "$sp" | grep -q punch && { echo "CHECK $name relay won FAIL"; fail=1; } || echo "CHECK $name relay won PASS"
    fi
  }

  run_case A "$F10" .tools/race-a 0
  run_case B "$F2" .tools/race-b 1

  [ "$fail" -eq 0 ] && echo "PATH_RACE=PASS" || echo "PATH_RACE=FAIL"
} 2>&1 | tee "$LOG"
exit "$fail"
