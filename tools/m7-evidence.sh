#!/usr/bin/env bash
# M7 desktop evidence: tray receiver, drop/SendTo send paths over the relay,
# and the autostart registry entry (read path only in the agent sandbox).
set -u
cd "$(dirname "$0")/.."
LOG=${1:-evidence/stage3/$(date +%Y%m%d-%H%M)-m7.log}
mkdir -p "$(dirname "$LOG")" .tools/m7recv
export GOCACHE="$(cygpath -w "$PWD/.tools/gocache")" GOPATH="$(cygpath -w "$PWD/.tools/gopath")" \
       GOTMPDIR="$(cygpath -w "$PWD/.tools/tmp")" GOFLAGS=-buildvcs=false
export PATH="$PWD/.tools/go/bin:$PATH"

say() { echo "$*" >>"$LOG"; }
run() { echo "\$ $*" >>"$LOG"; "$@" >>"$LOG" 2>&1; echo "exit=$?" >>"$LOG"; }

: >"$LOG"
say "# M7 desktop evidence $(date -Iseconds)"
run .tools/go/bin/go version
run .tools/go/bin/go build ./...
run .tools/go/bin/go vet ./cmd/ft-desktop ./core/client
run .tools/go/bin/go test ./core/... ./server/...

say ""
say "## autostart (read path)"
run .tools/bin/ft-desktop.exe -startup status

say ""
say "## relay send: drop-window path (-send) and Explorer path (positional args)"
.tools/bin/ft-server.exe -listen 127.0.0.1:18794 -log .tools/m7srv.jsonl &
SRV=$!
sleep 1
.tools/bin/ft.exe recv --signal ws://127.0.0.1:18794/signal --key .tools/keys/recv.key \
  --paired .tools/keys/paired-recv.json --out .tools/m7recv --log .tools/m7recv.jsonl --once=false &
RCV=$!
sleep 2
run .tools/bin/ft-desktop.exe -send .tools/testdata/test-2.1m.bin -key .tools/keys/send.key \
  -paired .tools/keys/paired-send.json -signal ws://127.0.0.1:18794/signal -log .tools/m7send.jsonl
# ft recv serves one transfer per process; restart it for the second path.
kill $RCV 2>/dev/null
sleep 1
.tools/bin/ft.exe recv --signal ws://127.0.0.1:18794/signal --key .tools/keys/recv.key \
  --paired .tools/keys/paired-recv.json --out .tools/m7recv --log .tools/m7recv.jsonl &
RCV=$!
sleep 2
run .tools/bin/ft-desktop.exe -key .tools/keys/send.key -paired .tools/keys/paired-send.json \
  -signal ws://127.0.0.1:18794/signal .tools/testdata/test-10m.bin
sleep 1
say "## received hashes"
sha256sum .tools/m7recv/* >>"$LOG" 2>&1
sha256sum .tools/testdata/test-2.1m.bin .tools/testdata/test-10m.bin >>"$LOG" 2>&1
kill $RCV $SRV 2>/dev/null
taskkill //F //IM ft-server.exe >/dev/null 2>&1
say ""
say "M7_EVIDENCE_DONE"
echo "log: $LOG"
