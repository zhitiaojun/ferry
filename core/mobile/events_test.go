package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPollReceivedDrain(t *testing.T) {
	_ = PollReceived() // clear any prior test state

	dir := t.TempDir()
	p1 := filepath.Join(dir, "a.bin")
	if err := os.WriteFile(p1, make([]byte, 123), 0o644); err != nil {
		t.Fatal(err)
	}
	recordReceived(dir, "relay", []string{"a.bin", "missing.txt"}, 999)

	raw := PollReceived()
	var evts []ReceivedEvent
	if err := json.Unmarshal([]byte(raw), &evts); err != nil {
		t.Fatalf("unmarshal: %v (%s)", err, raw)
	}
	if len(evts) != 2 {
		t.Fatalf("want 2 events, got %d: %s", len(evts), raw)
	}
	if evts[0].File != "a.bin" || evts[0].FullPath != p1 || evts[0].Via != "relay" || evts[0].Bytes != 123 {
		t.Fatalf("unexpected first event: %+v", evts[0])
	}
	if evts[1].File != "missing.txt" || evts[1].Bytes != 0 {
		t.Fatalf("unexpected second event: %+v", evts[1])
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
