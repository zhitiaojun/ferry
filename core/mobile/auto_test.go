package mobile

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/server"
)

type connSnapshot struct {
	SignalState  string            `json:"signal_state"`
	LastPongMS   int64             `json:"last_pong_ms"`
	LanListening bool              `json:"lan_listening"`
	OnlinePeers  []string          `json:"online_peers"`
	LanPeers     map[string]string `json:"lan_peers"`
}

func readConnSnapshot(t *testing.T) connSnapshot {
	t.Helper()
	var s connSnapshot
	if err := json.Unmarshal([]byte(ConnectionStatus()), &s); err != nil {
		t.Fatalf("unmarshal ConnectionStatus: %v", err)
	}
	return s
}

func TestAutoReceiveConnectionStatusAndReconnectOnDrop(t *testing.T) {
	_ = StopAutoReceive()

	initSnap := readConnSnapshot(t)
	if initSnap.SignalState != "offline" {
		t.Fatalf("initial signal_state = %q, want offline", initSnap.SignalState)
	}

	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "device.key")
	pairedPath := filepath.Join(dir, "paired.json")
	outDir := filepath.Join(dir, "out")
	if _, err := identity.LoadOrCreateKey(keyPath); err != nil {
		t.Fatal(err)
	}

	if _, err := StartAutoReceive("test-phone", outDir, sigURL, keyPath, pairedPath, "", ""); err != nil {
		t.Fatalf("StartAutoReceive: %v", err)
	}
	defer func() { _ = StopAutoReceive() }()

	deadline := time.Now().Add(5 * time.Second)
	var firstClientDone <-chan struct{}
	for time.Now().Before(deadline) {
		snap := readConnSnapshot(t)
		sigMu.Lock()
		c := sigClient
		sigMu.Unlock()
		if snap.SignalState == "online" && c != nil {
			firstClientDone = c.Done()
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if firstClientDone == nil {
		t.Fatalf("auto receiver did not become online in time (state=%+v)", readConnSnapshot(t))
	}

	// Simulate underlying WebSocket drop: autoServe must wake from <-client.Done()
	// and establish a fresh signaling client automatically (regression test).
	sigMu.Lock()
	oldCl := sigClient
	sigMu.Unlock()
	_ = oldCl.Close()

	select {
	case <-firstClientDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first signaling client did not close")
	}

	reconnected := false
	deadline = time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		sigMu.Lock()
		c := sigClient
		st := sigState
		sigMu.Unlock()
		if st == "online" && c != nil && c != oldCl {
			reconnected = true
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !reconnected {
		t.Fatalf("auto receiver did not reconnect after socket drop (status=%s)", ConnectionStatus())
	}
}
