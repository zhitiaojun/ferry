#!/usr/bin/env bash
# G6 summary: rerun the security checks and print a judgement table.
set -uo pipefail
cd /c/project/filetransfer
export PATH="$PWD/.tools/go/bin:$PATH"
export GOCACHE=C:/project/filetransfer/.tools/gocache GOPATH=C:/project/filetransfer/.tools/gopath GOTMPDIR=C:/project/filetransfer/.tools/tmp GOFLAGS=-buildvcs=false
LOG=evidence/stage2/$(date +%Y%m%d-%H%M)-g6.log
mkdir -p "$(dirname "$LOG")"
{
  echo "# G6 判据核对 $(date -Iseconds)"
  echo "## 1 抓包无明文（TLS1.3 + 钉定）"
  echo "见 evidence/stage3/20261107-2220-tls10m.log（relay.tls pinned=true tls=1.3）与 20261107-2240-t13.log"
  echo "## 2 伪造身份/错误码无法连接"
  go test ./server/ -run "BadSignature|PAKE|PairBudget" -v 2>&1 | grep -E "^(--- (PASS|FAIL)|ok|FAIL)"
  echo "见 core/pairing/spake2_test.go（异码异键、坏 share 拒绝）"
  echo "## 3 自查清单"
  go test ./core/proto/ ./core/identity/ ./core/lanweb/ 2>&1 | tail -5
  ls -l docs/security-checklist.md
  echo "## 4 README 安全口径"
  grep -n "M6 已完成安全收尾" README.md
  echo "## 全量测试"
  go test ./... 2>&1 | tail -15
  echo "G6_DONE"
} > "$LOG" 2>&1
tail -20 "$LOG"
