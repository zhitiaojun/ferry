package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/discovery"
	"github.com/zhitiaojun/ferry/core/identity"
)

type testConfirmListener struct {
	mu       sync.Mutex
	autoAck  bool
	requests []incomingConfirmReq
}

func (l *testConfirmListener) OnReceived(string)          {}
func (l *testConfirmListener) OnTransferProgress(string)  {}
func (l *testConfirmListener) OnConnectionChanged(string) {}
func (l *testConfirmListener) OnConfirmIncoming(reqJSON string) {
	var req incomingConfirmReq
	if err := json.Unmarshal([]byte(reqJSON), &req); err != nil {
		return
	}
	l.mu.Lock()
	l.requests = append(l.requests, req)
	accept := l.autoAck
	l.mu.Unlock()
	RespondIncoming(req.ID, accept)
}

func TestReceiverLANConfirmAndVerifiedPairedBypass(t *testing.T) {
	_ = StopReceiver()
	defer func() {
		SetEventListener(nil)
		SetMainUIOpen(false)
		_ = StopReceiver()
	}()

	dir := t.TempDir()
	recvKeyPath := filepath.Join(dir, "recv.key")
	recvPairedPath := filepath.Join(dir, "recv-paired.json")
	sendKeyPath := filepath.Join(dir, "send.key")
	outDir := filepath.Join(dir, "out")

	if _, err := identity.LoadOrCreateKey(recvKeyPath); err != nil {
		t.Fatal(err)
	}
	sendKey, err := identity.LoadOrCreateKey(sendKeyPath)
	if err != nil {
		t.Fatal(err)
	}

	src := filepath.Join(dir, "temp.txt")
	if err := os.WriteFile(src, []byte("lan confirm payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	pathsJSON, _ := json.Marshal([]string{src})

	addr, err := StartReceiverWithAuth("recv-phone", outDir, recvKeyPath, recvPairedPath, "")
	if err != nil {
		t.Fatalf("StartReceiverWithAuth: %v", err)
	}

	// 1. Unpaired sender rejected immediately when main UI is closed.
	SetMainUIOpen(false)
	listener := &testConfirmListener{autoAck: false}
	SetEventListener(listener)
	if _, err := SendFilesWithKey(addr, "stranger", sendKeyPath, string(pathsJSON)); err == nil {
		t.Fatal("expected unpaired LAN send to fail when main UI is closed")
	}
	if _, serr := os.Stat(filepath.Join(outDir, "temp.txt")); !os.IsNotExist(serr) {
		t.Fatalf("unexpected file created when main UI closed: %v", serr)
	}

	// 2. Main UI open + user rejects -> prompt invoked, file NOT saved.
	SetMainUIOpen(true)
	if _, err := SendFilesWithKey(addr, "stranger", sendKeyPath, string(pathsJSON)); err == nil {
		t.Fatal("expected unpaired LAN send to fail when rejected")
	}
	listener.mu.Lock()
	gotReqs := len(listener.requests)
	listener.mu.Unlock()
	if gotReqs != 1 {
		t.Fatalf("prompt count = %d, want 1", gotReqs)
	}
	if _, serr := os.Stat(filepath.Join(outDir, "temp.txt")); !os.IsNotExist(serr) {
		t.Fatalf("unexpected file created when rejected: %v", serr)
	}

	// 3. Main UI open + user accepts -> succeeds and saves file.
	listener.mu.Lock()
	listener.autoAck = true
	listener.mu.Unlock()
	if _, err := SendFilesWithKey(addr, "stranger", sendKeyPath, string(pathsJSON)); err != nil {
		t.Fatalf("expected unpaired LAN send to succeed on confirm: %v", err)
	}
	if _, serr := os.Stat(filepath.Join(outDir, "temp.txt")); serr != nil {
		t.Fatalf("expected temp.txt after accept: %v", serr)
	}
	_ = os.Remove(filepath.Join(outDir, "temp.txt"))

	// 4. Verified paired sender bypasses confirmation even when main UI is closed.
	pairedStore, err := identity.LoadPaired(recvPairedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := pairedStore.Add(identity.PairedDevice{
		DeviceID: sendKey.IDHex(),
		Name:     "trusted-sender",
		PubKey:   sendKey.PubHex(),
		PairedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := pairedStore.Save(); err != nil {
		t.Fatal(err)
	}

	SetMainUIOpen(false)
	listener.mu.Lock()
	beforeCount := len(listener.requests)
	listener.mu.Unlock()

	if _, err := SendFilesWithKey(addr, "trusted-sender", sendKeyPath, string(pathsJSON)); err != nil {
		t.Fatalf("expected paired verified LAN send to succeed with main UI closed: %v", err)
	}
	listener.mu.Lock()
	afterCount := len(listener.requests)
	listener.mu.Unlock()
	if afterCount != beforeCount {
		t.Fatalf("paired verified sender should not prompt, prompts %d -> %d", beforeCount, afterCount)
	}
	if _, serr := os.Stat(filepath.Join(outDir, "temp.txt")); serr != nil {
		t.Fatalf("expected temp.txt for paired sender: %v", serr)
	}
}

func TestDiscoverNoHintAndDeviceIDSerialization(t *testing.T) {
	if err := discoverNoHintError("android", nil); err == nil {
		t.Fatal("expected error on android without hint")
	} else if !strings.Contains(err.Error(), "Wi-Fi") {
		t.Fatalf("unexpected android error message: %v", err)
	}
	if err := discoverNoHintError("android", &discovery.Hint{Name: "wlan0", IP: "192.168.1.2", Prefix: 24}); err != nil {
		t.Fatalf("unexpected error with valid hint: %v", err)
	}
	if err := discoverNoHintError("windows", nil); err != nil {
		t.Fatalf("unexpected error on windows without hint: %v", err)
	}

	b, err := json.Marshal(Device{
		ID:    "abcd",
		Name:  "HyperX",
		Addr:  "192.168.1.5:1234",
		Iface: "wlan0",
	})
	if err != nil {
		t.Fatalf("json.Marshal(Device): %v", err)
	}
	got := string(b)
	for _, want := range []string{`"ID":"abcd"`, `"Name":"HyperX"`, `"Addr":"192.168.1.5:1234"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("marshaled Device = %s, missing %s", got, want)
		}
	}
}
