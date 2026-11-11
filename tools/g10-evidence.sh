#!/usr/bin/env bash
# M10 证据：三端构建 + 桌面截图 + Web 页面检查。
# 用法：tools/g10-evidence.sh [日志路径]
set -u
cd "$(dirname "$0")/.." || exit 1
LOG="${1:-evidence/stage3/$(date +%Y%m%d-%H%M)-m10.log}"
mkdir -p "$(dirname "$LOG")"
exec > >(tee -a "$LOG") 2>&1

export GOCACHE="$(cygpath -w "$PWD/.tools/gocache")"
export GOPATH="$(cygpath -w "$PWD/.tools/gopath")"
export GOTMPDIR="$(cygpath -w "$PWD/.tools/tmp")"
export GOFLAGS=-buildvcs=false
export TMP="$PWD/.tools/tmp" TEMP="$PWD/.tools/tmp"
GO=.tools/go/bin/go.exe

say() { printf '\n== %s ==\n' "$*"; }
say "env"
date -Is
git rev-parse --short HEAD
$GO version
echo "uname: $(uname -a)"

say "gofmt"
out=$("$GO" fmt ./... 2>&1); echo "${out:-clean}"

say "build"
$GO build ./... && echo BUILD_OK

say "vet"
$GO vet ./... && echo VET_OK

say "test"
$GO test ./... 2>&1 | tail -30

say "desktop build"
$GO build -o .tools/bin/ft-desktop.exe ./cmd/ft-desktop/ && ls -l .tools/bin/ft-desktop.exe

say "desktop screenshot"
mkdir -p .tools/shots evidence/stage3
.tools/bin/ft-desktop.exe -main -log .tools/g10-desktop.jsonl &
DESK_PID=$!
sleep 3
pwsh -NoProfile -File .tools/shot-win.ps1 -Class ftMainWin -Out "$PWD/.tools/shots/m10-main.png"
sleep 1
taskkill //F //PID $DESK_PID >/dev/null 2>&1
ls -l .tools/shots/m10-main.png

say "web local server"
FT_WEB_PASSWORD=test123 .tools/bin/ft-server.exe -listen 127.0.0.1:18888 -log .tools/g10-web.jsonl &
SRV_PID=$!
sleep 1
curl -s -o /dev/null -w "decoy=%{http_code}\n" http://127.0.0.1:18888/
curl -s -o /dev/null -w "app_no_cookie=%{http_code}\n" http://127.0.0.1:18888/app
curl -s -o /dev/null -w "healthz=%{http_code}\n" http://127.0.0.1:18888/healthz
curl -s http://127.0.0.1:18888/ | grep -o '<title>[^<]*</title>' | head -1
curl -s -o /dev/null -w "unlock_bad=%{http_code}\n" -X POST -H 'Content-Type: application/json' -d '{"password":"bad"}' http://127.0.0.1:18888/api/unlock
curl -s -o /dev/null -w "unlock_ok=%{http_code}\n" -X POST -H 'Content-Type: application/json' -d '{"password":"test123"}' http://127.0.0.1:18888/api/unlock
kill $SRV_PID 2>/dev/null

say "done"
echo "M10_EVIDENCE_DONE"
