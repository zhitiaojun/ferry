package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/transfer"
)

func TestPollReceivedDrain(t *testing.T) {
	_ = PollReceived() // clear any prior test state

	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(p1, make([]byte, 123), 0o644); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, "album", "sub", "b.jpg")
	if err := os.MkdirAll(filepath.Dir(nested), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(nested, make([]byte, 456), 0o644); err != nil {
		t.Fatal(err)
	}
	recordReceived(dir, "relay", []string{"a.bin", "album/sub/b.jpg", "missing.txt"}, 999)

	raw := PollReceived()
	var evts []ReceivedEvent
	if err := json.Unmarshal([]byte(raw), &evts); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if len(evts) != 3 {
		t.Fatalf("want 3 events, got %d: %s", len(evts), raw)
	}
	if evts[0].File != "a.bin" || evts[0].FullPath != p1 || evts[0].Via != "relay" || evts[0].Bytes != 123 {
		t.Fatalf("unexpected first event: %+v", evts[0])
	}
	if evts[1].File != "album/sub/b.jpg" || evts[1].FullPath != nested || evts[1].Via != "relay" || evts[1].Bytes != 456 {
		t.Fatalf("unexpected nested event: %+v", evts[1])
	}
	if evts[2].File != "missing.txt" || evts[2].Bytes != 0 {
		t.Fatalf("unexpected third event: %+v", evts[2])
	}

	if again := PollReceived(); again != "[]" {
		t.Fatalf("second PollReceived = %q, want []", again)
	}
}

func TestStateLogPath(t *testing.T) {
	base := t.TempDir()
	inbox := filepath.Join(base, "inbox")
	got := stateLogPath(inbox, "events.jsonl")
	want := filepath.Join(base, "state", "events.jsonl")
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(base, "state")); err != nil {
		t.Fatalf("state dir not created: %v", err)
	}
}

func TestActiveTransferStatusLifecycle(t *testing.T) {
	seq := beginActiveTransfer("send", "办公室电脑", "a.bin", 2, 1024)
	updateActiveTransfer(seq, "transferring", "punch", "", "b.bin", 2, 2, 512, 1024)

	var st ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &st); err != nil {
		t.Fatalf("unmarshal active status: %v", err)
	}
	if !st.Active || st.Direction != "send" || st.Peer != "办公室电脑" || st.Channel != "punch" ||
		st.Stage != "transferring" || st.FileName != "b.bin" || st.FileIndex != 2 || st.BytesDone != 512 {
		t.Fatalf("unexpected active status: %+v", st)
	}

	// Stale seq must not clobber or end current transfer.
	endActiveTransfer(seq - 1)
	if !activeTx.Active {
		t.Fatal("stale seq ended current transfer")
	}

	endActiveTransfer(seq)
	if got := ActiveTransferStatus(); got != `{"active":false}` {
		t.Fatalf("after end ActiveTransferStatus = %q, want {\"active\":false}", got)
	}
}

func TestActiveTransferSpeedComputation(t *testing.T) {
	seq := beginActiveTransfer("send", "网页快传", "video.mp4", 1, 20*1024*1024)
	defer endActiveTransfer(seq)

	time.Sleep(180 * time.Millisecond)
	updateActiveTransfer(seq, "transferring", "lan", "网页快传", "video.mp4", 1, 1, 4*1024*1024, 20*1024*1024)

	var st ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &st); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if st.SpeedBPS <= 0 {
		t.Fatalf("expected SpeedBPS > 0, got %+v", st)
	}

	updateActiveTransfer(seq, "verifying", "lan", "网页快传", "video.mp4", 1, 1, 20*1024*1024, 20*1024*1024)
	var stVerifying ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &stVerifying); err != nil {
		t.Fatalf("unmarshal verifying: %v", err)
	}
	if stVerifying.SpeedBPS != 0 {
		t.Fatalf("expected SpeedBPS = 0 in verifying stage, got %+v", stVerifying)
	}
}

func TestActiveTransferPauseAndCancelControl(t *testing.T) {
	seq := beginActiveTransfer("send", "办公室电脑", "big.bin", 1, 1024)
	ctrl := transfer.NewController()
	attachActiveTransferController(seq, ctrl, true, true)
	defer endActiveTransfer(seq)

	if paused := TogglePauseActiveTransfer(); !paused {
		t.Fatal("expected TogglePauseActiveTransfer to return true")
	}
	var st ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Paused || st.Stage != "paused" || !st.CanPause || !st.CanCancel {
		t.Fatalf("unexpected paused status: %+v", st)
	}
	if paused := TogglePauseActiveTransfer(); paused {
		t.Fatal("expected second TogglePauseActiveTransfer to resume")
	}
	if !CancelActiveTransfer() || !ctrl.IsCancelled() {
		t.Fatal("expected CancelActiveTransfer to cancel controller")
	}
}

func TestActiveTransferPauseReconcileAndOwnerRule(t *testing.T) {
	seq := beginActiveTransfer("recv", "HyperX", "a.apk", 1, 2048)
	ctrl := transfer.NewController()
	attachActiveTransferController(seq, ctrl, true, true)
	defer endActiveTransfer(seq)

	// 1. Local pause must survive stale "transferring" progress updates (no flicker).
	if !TogglePauseActiveTransfer() {
		t.Fatal("expected local pause to succeed")
	}
	updateActiveTransfer(seq, "transferring", "relay", "", "a.apk", 1, 1, 512, 2048)
	var st ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &st); err != nil {
		t.Fatal(err)
	}
	if !st.Paused || st.Stage != "paused" || st.SpeedBPS != 0 {
		t.Fatalf("local pause flickered after updateActiveTransfer: %+v", st)
	}
	TogglePauseActiveTransfer() // resume local

	// 2. Peer pause syncs peer_paused + speed 0, and local TogglePauseActiveTransfer refuses resume.
	ctrl.SetPeerPaused(true)
	var stPeer ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &stPeer); err != nil {
		t.Fatal(err)
	}
	if !stPeer.PeerPaused || stPeer.Paused || stPeer.Stage != "peer_paused" || stPeer.SpeedBPS != 0 {
		t.Fatalf("unexpected peer_paused state: %+v", stPeer)
	}
	if TogglePauseActiveTransfer() {
		t.Fatal("local TogglePauseActiveTransfer should return false when paused by peer")
	}
	ctrl.SetPeerPaused(false)
	var stResumed ActiveTransfer
	if err := json.Unmarshal([]byte(ActiveTransferStatus()), &stResumed); err != nil {
		t.Fatal(err)
	}
	if stResumed.PeerPaused || stResumed.Stage != "transferring" {
		t.Fatalf("expected transferring after peer resume, got %+v", stResumed)
	}
}

func TestRecordReceiveOutcome(t *testing.T) {
	_ = PollReceived()

	recordReceiveOutcome("relay", "HyperX", "big.apk", 2048, "cancelled", "已取消接收")
	recordReceiveOutcome("punch", "HyperX", "a.txt 等 2 个文件", 4096, "peer_cancelled", "对方已取消传输")
	recordReceiveOutcome("lan", "WorkPC", "doc.pdf", 1024, "failed", "io error")

	raw := PollReceived()
	var evts []ReceivedEvent
	if err := json.Unmarshal([]byte(raw), &evts); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if len(evts) != 3 {
		t.Fatalf("want 3 outcome events, got %d: %s", len(evts), raw)
	}
	if evts[0].Status != "cancelled" || evts[0].FullPath != "" || evts[0].File != "big.apk" || evts[0].Via != "relay" {
		t.Fatalf("unexpected first outcome: %+v", evts[0])
	}
	if evts[1].Status != "peer_cancelled" || evts[1].Peer != "HyperX" || evts[1].Error != "对方已取消传输" {
		t.Fatalf("unexpected second outcome: %+v", evts[1])
	}
	if evts[2].Status != "failed" || evts[2].Error != "io error" || evts[2].Bytes != 1024 {
		t.Fatalf("unexpected third outcome: %+v", evts[2])
	}
}
