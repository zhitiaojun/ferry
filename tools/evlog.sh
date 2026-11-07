#!/usr/bin/env bash
# Runs a command and appends a timestamped record to an evidence log.
#
#   tools/evlog.sh evidence/stage1/20261107-2000.log -- ft send --discover ...
#
# Every record line starts with "[YYYY-MM-DDTHH:MM:SS+08:00] $ command" followed
# by the command's stdout/stderr, so the log stays raw evidence as WORK_AGENT
# requires.
set -uo pipefail
log=$1
shift
[ "$1" = "--" ] && shift

mkdir -p "$(dirname "$log")"
{
  printf '\n[%s] $ %s\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$*"
  "$@" 2>&1
  printf '[%s] exit=%d\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')" "$?"
} >>"$log"
