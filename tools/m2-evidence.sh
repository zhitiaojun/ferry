#!/usr/bin/env bash
# Collects the M2 hotspot evidence into evidence/stage1/<timestamp>.log.
set -uo pipefail
cd "$(dirname "$0")/.."
ROOT=$PWD
A=.tools/android-sdk/platform-tools/adb.exe
OUT=evidence/stage1/$(date +%Y%m%d-%H%M).log
mkdir -p evidence/stage1

say() { printf '\n== %s ==\n' "$*"; }
ts() { printf '\n--- %s ---\n' "$(date '+%Y-%m-%dT%H:%M:%S%z')"; }

{
  say "M2 hotspot: phone <-> PC direct LAN transfer"
  ts
  echo "host: $(hostname)  go: $(.tools/go/bin/go.exe version)"

  say "phone"
  ts
  $A -s "$($A devices | awk 'NR==2{print $1}')" shell getprop ro.product.model
  $A -s "$($A devices | awk 'NR==2{print $1}')" shell getprop ro.build.version.release
  $A -s "$($A devices | awk 'NR==2{print $1}')" shell getprop ro.build.version.sdk
  $A devices

  say "PC network (hotspot on WLAN, Meta TUN running = VPN/TUN present, must be ignored)"
  ts
  ipconfig | grep -A6 "WLAN" | grep -E "IPv4|默认网关|Default Gateway" | head -4
  /c/Windows/System32/route.exe print -4 | grep -E "^ +0\.0\.0\.0" | head -4

  say "discovery port (host-reserved ports made 40002 unusable on Android)"
  ts
  echo "port constant: $(grep -n 'Port = ' core/discovery/announce.go | head -1)"
  $A shell cat /proc/sys/net/ipv4/ip_local_reserved_ports

  say "phone -> PC (phone discovered 'pc' via LAN announcement, sent test-2.1m.bin)"
  ts
  cat .tools/recv-from-phone.jsonl
  sha256sum .tools/recv-from-phone/test-2.1m.bin .tools/testdata/test-2.1m.bin

  say "PC -> phone (ft send --addr <phone>:port, two files)"
  ts
  cat .tools/pc2phone.jsonl
  sha256sum .tools/phone-recv2/received/test-2.1m.bin .tools/phone-recv2/received/test-10m.bin \
            .tools/testdata/test-2.1m.bin .tools/testdata/test-10m.bin
} >"$OUT" 2>&1

echo "wrote $OUT"
grep -c . "$OUT"
