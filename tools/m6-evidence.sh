#!/usr/bin/env bash
# M6 self-check evidence: crypto, pairing, limits, key sealing, secrets in logs.
set -uo pipefail
cd /c/project/filetransfer
export PATH="$PWD/.tools/go/bin:$PATH"
export GOCACHE="C:/project/filetransfer/.tools/gocache" GOPATH="C:/project/filetransfer/.tools/gopath" GOTMPDIR="C:/project/filetransfer/.tools/tmp" GOFLAGS=-buildvcs=false
LOG=evidence/stage2/$(date +%Y%m%d-%H%M)-m6.log
mkdir -p "$(dirname "$LOG")"
{
  echo "# M6 self-check $(date -Iseconds)"
  echo "## go version"; go version
  echo "## gofmt"; gofmt -l core cmd server
  echo "## go vet"; go vet ./... && echo VET_OK
  echo "## tests"; go test ./... 2>&1 | tail -30
  echo "## key file sealing"
  cat > /tmp/ftkeycheck_test.txt <<'TXT'
TXT
  go test ./core/identity/ -run "Seal|Legacy" -v 2>&1 | grep -E "^(=== RUN|--- (PASS|FAIL)|ok|FAIL)" 
  echo "## manifest limits"
  go test ./core/proto/ -run "Manifest" -v 2>&1 | grep -E "^(=== RUN|--- (PASS|FAIL)|ok|FAIL)"
  echo "## pairing budget"
  go test ./server/ -run "PairBudget" -v 2>&1 | grep -E "^(=== RUN|--- (PASS|FAIL)|ok|FAIL)"
  echo "## secrets in logs (should print nothing)"
  grep -rn "log.Event" server cmd/ft core 2>/dev/null | grep -iE "\"token\"|\"payload\"|\"secret\"|\"key\":" || true
  echo "## checklist doc"; ls -l docs/security-checklist.md
  echo "M6_EVIDENCE_DONE"
} > "$LOG" 2>&1
tail -40 "$LOG"
