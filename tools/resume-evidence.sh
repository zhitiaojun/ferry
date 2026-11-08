#!/usr/bin/env bash
# Verifies that a dropped LAN session resumes instead of restarting: kill the
# receiver mid-transfer, restart it on the same port, and compare hashes.
set -u
cd "$(dirname "$0")/.."
FT=.tools/bin/ft.exe
WORK=.tools/resume-evidence
SRC=.tools/testdata/test-200m.bin
LOG=${1:-evidence/stage3/$(date +%Y%m%d-%H%M)-resume.log}
PORT=18960
mkdir -p "$(dirname "$LOG")" "$WORK/recv"
rm -rf "$WORK/recv"/*; mkdir -p "$WORK/recv"
rm -f "$WORK/send.jsonl" "$WORK/recv.jsonl"

exec > >(tee "$LOG") 2>&1
echo "# $(date -Is) resume-after-disconnect evidence"
echo "## source"; ls -l "$SRC"; sha256sum "$SRC"

"$FT" recv --listen 127.0.0.1:$PORT --out "$WORK/recv" --once=false --log "$WORK/recv.jsonl" >"$WORK/recv.out" 2>&1 &
RECV=$!
sleep 1
"$FT" send --addr 127.0.0.1:$PORT --file "$SRC" --chunk 16384 --timeout 10s --retry-for 60s --log "$WORK/send.jsonl" >"$WORK/send.out" 2>&1 &
SEND=$!
sleep 0.7
MSYS_NO_PATHCONV=1 taskkill //F //PID $RECV >/dev/null 2>&1 || kill -9 $RECV 2>/dev/null
echo "## receiver killed at $(date -Is), partial size: $(stat -c %s "$WORK/recv/test-200m.bin.ftpart" 2>/dev/null || echo none)"
sleep 2
"$FT" recv --listen 127.0.0.1:$PORT --out "$WORK/recv" --once=false --log "$WORK/recv.jsonl" >>"$WORK/recv.out" 2>&1 &
RECV2=$!
wait $SEND
echo "## sender summary"; cat "$WORK/send.out"
echo "## receiver events"; grep -h "resume_from\|transfer.done" "$WORK/recv.jsonl" | tail -3
echo "## received file"; ls -l "$WORK/recv"; sha256sum "$WORK/recv/test-200m.bin"
kill $RECV2 2>/dev/null
echo "RESUME_EVIDENCE_DONE"
