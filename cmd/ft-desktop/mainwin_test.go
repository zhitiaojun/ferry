//go:build windows

package main

import (
	"strings"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/transfer"
)

func TestFmtDur(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{0, "1 ms"},
		{42 * time.Millisecond, "42 ms"},
		{1500 * time.Millisecond, "1.5 s"},
		{90 * time.Second, "1.5 min"},
	}
	for _, tc := range cases {
		if got := fmtDur(tc.d); got != tc.want {
			t.Errorf("fmtDur(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

func TestParseLedgerLine(t *testing.T) {
	cases := []struct {
		line string
		want ledgerRow
	}{
		{
			line: "15:04  → Redmi K70 · 2 个文件 · 2.1 MB · 1.4 s · 中继",
			want: ledgerRow{time: "15:04", dir: "发", peer: "Redmi K70", files: "2 个文件", size: "2.1 MB", dur: "1.4 s", path: "中继"},
		},
		{
			line: "15:05  ← Pixel · 1 个文件 · 128 KB · 42 ms · LAN",
			want: ledgerRow{time: "15:05", dir: "收", peer: "Pixel", files: "1 个文件", size: "128 KB", dur: "42 ms", path: "LAN"},
		},
		{
			line: "15:06  → Laptop · 3 个文件 · 失败",
			want: ledgerRow{time: "15:06", dir: "发", peer: "Laptop", files: "3 个文件", path: "失败"},
		},
		{
			line: "15:07  ← Phone · 失败",
			want: ledgerRow{time: "15:07", dir: "收", peer: "Phone", path: "失败"},
		},
		{
			line: "15:08  → Laptop · 3 个文件 · 已取消",
			want: ledgerRow{time: "15:08", dir: "发", peer: "Laptop", files: "3 个文件", path: "已取消"},
		},
		{
			line: "15:09  ← Phone · note.txt · 对方已取消",
			want: ledgerRow{time: "15:09", dir: "收", peer: "Phone", files: "note.txt", path: "对方已取消"},
		},
		{
			line: "15:10  ← Phone · 已取消",
			want: ledgerRow{time: "15:10", dir: "收", peer: "Phone", path: "已取消"},
		},
	}
	for _, tc := range cases {
		got := parseLedgerLine(tc.line)
		if got != tc.want {
			t.Errorf("parseLedgerLine(%q) = %+v, want %+v", tc.line, got, tc.want)
		}
	}
}

func TestChannelDisplayLabelAndETA(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"LAN", "LAN 直连"},
		{"打洞", "UDP 打洞"},
		{"中继", "服务器中继"},
		{"已取消", "已取消"},
		{"对方已取消", "对方已取消"},
		{"失败", "失败"},
		{"", ""},
	}
	for _, tc := range cases {
		lbl, _, _ := channelDisplayLabel(tc.in, palLight, false)
		if lbl != tc.want {
			t.Errorf("channelDisplayLabel(%q) = %q, want %q", tc.in, lbl, tc.want)
		}
	}

	if got := formatETA(2*time.Second, 200, 1000); got != "剩余约 8s" {
		t.Errorf("formatETA(2s, 200/1000) = %q, want 剩余约 8s", got)
	}
	if got := formatETA(500*time.Millisecond, 500, 1000); got != "" {
		t.Errorf("formatETA(<1s) = %q, want empty", got)
	}
}

func TestAppActiveTransferLifecycle(t *testing.T) {
	a := newApp(defaultReceiveDir(), 18800)
	if a.currentTransfer() != nil {
		t.Fatal("new app should have nil active transfer")
	}
	id := a.beginTransfer("send", "Redmi K70", "a.bin", 2, 2048)
	if id == 0 {
		t.Fatal("expected non-zero transfer id")
	}
	a.updateTransfer(id, "transferring", "中继", "", "b.bin", 2, 2, 1024, 2048)
	cur := a.currentTransfer()
	if cur == nil || cur.stage != "transferring" || cur.channel != "中继" || cur.fileName != "b.bin" || cur.bytesDone != 1024 {
		t.Fatalf("unexpected currentTransfer: %+v", cur)
	}
	// Stale ID must not clear active transfer.
	a.endTransfer(id + 1)
	if a.currentTransfer() == nil {
		t.Fatal("stale endTransfer cleared current transfer")
	}
	a.endTransfer(id)
	if a.currentTransfer() != nil {
		t.Fatal("expected nil after endTransfer")
	}
}

func TestFormatSpeedAndMetrics(t *testing.T) {
	if got := formatSpeed(512); got != "512 B/s" {
		t.Fatalf("formatSpeed(512) = %q", got)
	}
	if got := formatSpeed(1536); got != "1.5 KB/s" {
		t.Fatalf("formatSpeed(1536) = %q", got)
	}
	if got := formatSpeed(5 * 1024 * 1024); got != "5.0 MB/s" {
		t.Fatalf("formatSpeed(5MB) = %q", got)
	}

	m := formatTransferMetrics(time.Now().Add(-2*time.Second), 4*1024*1024, 10*1024*1024, 2*1024*1024)
	for _, want := range []string{"4.0 MB / 10.0 MB", "2.0 MB/s", "已用", "剩余约"} {
		if !strings.Contains(m, want) {
			t.Fatalf("formatTransferMetrics = %q, missing %q", m, want)
		}
	}

	a := newApp(defaultReceiveDir(), 18800)
	id := a.beginTransfer("send", "网页快传", "big.bin", 1, 10*1024*1024)
	time.Sleep(180 * time.Millisecond)
	a.updateTransfer(id, "transferring", "LAN", "", "big.bin", 1, 1, 2*1024*1024, 10*1024*1024)
	cur := a.currentTransfer()
	if cur == nil || cur.speedBPS <= 0 {
		t.Fatalf("expected positive speedBPS, got %+v", cur)
	}
}

func TestAppTransferPauseAndCancelControl(t *testing.T) {
	a := newApp(defaultReceiveDir(), 18800)
	id := a.beginTransfer("send", "Redmi K70", "big.bin", 1, 10*1024*1024)
	ctrl := transfer.NewController()
	a.attachTransferControl(id, ctrl, true, true)
	a.updateTransfer(id, "transferring", "LAN", "", "big.bin", 1, 1, 4*1024*1024, 10*1024*1024)

	if !a.togglePauseTransfer() {
		t.Fatal("togglePauseTransfer() = false, want true")
	}
	if !ctrl.IsPaused() {
		t.Fatal("controller should be paused")
	}
	cur := a.currentTransfer()
	if cur == nil || !cur.paused || cur.stage != "paused" || cur.speedBPS != 0 {
		t.Fatalf("expected paused state with zero speed, got %+v", cur)
	}
	pausedMetrics := formatTransferMetricsState(cur.startedAt, cur.bytesDone, cur.bytesTotal, cur.speedBPS, true)
	if !strings.Contains(pausedMetrics, "已暂停") || strings.Contains(pausedMetrics, "剩余约") {
		t.Fatalf("unexpected paused metrics: %q", pausedMetrics)
	}

	if !a.togglePauseTransfer() {
		t.Fatal("second togglePauseTransfer() = false, want true")
	}
	if ctrl.IsPaused() {
		t.Fatal("controller should be resumed")
	}

	if !a.cancelTransfer() {
		t.Fatal("cancelTransfer() = false, want true")
	}
	if !ctrl.IsCancelled() {
		t.Fatal("controller should be cancelled")
	}
}

func TestAppPeerPauseOwnerOnlyAndHumanBytes(t *testing.T) {
	if got := humanBytes(429599); got != "419.5 KB" {
		t.Fatalf("humanBytes(429599) = %q, want 419.5 KB", got)
	}
	if got := stageDisplayLabel("peer_paused"); got != "对方已暂停" {
		t.Fatalf("stageDisplayLabel(peer_paused) = %q, want 对方已暂停", got)
	}

	a := newApp(defaultReceiveDir(), 18800)
	id := a.beginTransfer("recv", "REDMI K90 Ultra", "a.bin", 1, 10*1024*1024)
	ctrl := transfer.NewController()
	a.attachTransferControl(id, ctrl, true, true)

	ctrl.SetPeerPaused(true)
	a.updateTransfer(id, "transferring", "中继", "", "a.bin", 1, 1, 2*1024*1024, 10*1024*1024)
	cur := a.currentTransfer()
	if cur == nil || !cur.peerPaused || cur.paused || cur.stage != "peer_paused" || cur.speedBPS != 0 {
		t.Fatalf("expected peer_paused state after updateTransfer, got %+v", cur)
	}
	if a.togglePauseTransfer() {
		t.Fatal("desktop should not be allowed to resume peer-paused transfer")
	}
	ctrl.SetPeerPaused(false)
	cur = a.currentTransfer()
	if cur == nil || cur.peerPaused || cur.stage != "transferring" {
		t.Fatalf("expected transferring after peer resume, got %+v", cur)
	}
}
