#!/usr/bin/env bash
# M4 evidence: STUN, local punch/race, real-network attempts, IPv6/PMTUD notes.
# Usage: tools/m4-evidence.sh <log>
set -u
cd "$(dirname "$0")/.."
LOG="${1:-evidence/stage2/$(date +%Y%m%d-%H%M)-m4.log}"
mkdir -p "$(dirname "$LOG")"
PB="$PWD/.tools/bin"
export TMP="$PWD/.tools/tmp" TEMP="$PWD/.tools/tmp" MSYS_NO_PATHCONV=1

say() { printf '\n=== %s ===\n' "$*" >>"$LOG"; }
run() { printf '$ %s\n' "$*" >>"$LOG"; eval "$@" >>"$LOG" 2>&1; printf '[exit=%s]\n' "$?" >>"$LOG"; }

say "env"
run "date -Iseconds"
run "'$PWD/.tools/go/bin/go.exe' version"
run "go env GOOS GOARCH 2>/dev/null || true"

say "pc network"
run "/c/Windows/System32/ipconfig.exe | sed -n '/WLAN/,+6p'"
run "/c/Windows/System32/route.exe print -4 | grep -E '0.0.0.0 +0.0.0.0' | head -3"
run "/c/Windows/System32/WindowsPowerShell/v1.0/powershell.exe -NoProfile -Command \"Get-NetIPAddress -AddressFamily IPv6 | Where-Object {\\\$_.IPAddress -notlike 'fe80*' -and \\\$_.PrefixOrigin -ne 'WellKnown'} | Select-Object InterfaceAlias,IPAddress | Format-Table -AutoSize\""

say "stun (public)"
run "python tools/stunprobe.py 123.57.253.208:40000 123.57.253.208:40001"

say "phone"
run "adb shell getprop ro.product.model; adb shell getprop ro.build.version.sdk"
run "adb shell ip -o addr show | grep -E 'wlan|rmnet|tun' | awk '{print \$2, \$4}'"
run "adb shell ip route get 123.57.253.208 | head -1"

say "local punch (loopback candidates)"
if curl -fsS --max-time 2 http://127.0.0.1:18790/healthz >/dev/null 2>&1; then
  rm -rf .tools/m4ev-recv && mkdir -p .tools/m4ev-recv
  FT_PUNCH_LOOPBACK=1 nohup "$PB/ft.exe" recv --punch --signal ws://127.0.0.1:18790/signal \
    --key .tools/keys/recv.key --paired .tools/keys/paired-recv.json --out .tools/m4ev-recv \
    --name m4-recv --log .tools/m4ev-recv.jsonl >>"$LOG" 2>&1 &
  sleep 3
  run "FT_PUNCH_LOOPBACK=1 '$PB/ft.exe' send --punch --signal ws://127.0.0.1:18790/signal --key .tools/keys/send.key --paired .tools/keys/paired-send.json --to-id a7fd14b608f4041fb9a546d634e93790f16c30bb4c5290120d24a1272393b859 --file .tools/testdata/test-2.1m.bin --log .tools/m4ev-send.jsonl"
  run "sha256sum .tools/testdata/test-2.1m.bin .tools/m4ev-recv/test-2.1m.bin"
  run "tail -3 .tools/m4ev-recv.jsonl"
else
  printf 'local ft-server not running; skipped\n' >>"$LOG"
fi

say "recent real-network attempts"
run "tail -4 .tools/t6c-recv.jsonl 2>/dev/null || true"
run "tail -2 .tools/punch-server.jsonl 2>/dev/null || true"

say "done"
echo "M4_EVIDENCE=$LOG"
