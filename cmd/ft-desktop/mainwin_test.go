//go:build windows

package main

import (
	"testing"
	"time"
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
