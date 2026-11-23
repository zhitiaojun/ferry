package mobile

import (
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/signaling"
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

func TestPairWhileAutoReceiveActive(t *testing.T) {
	_ = StopAutoReceive()

	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	mobKeyPath := filepath.Join(dir, "mob.key")
	mobPairedPath := filepath.Join(dir, "mob-paired.json")
	outDir := filepath.Join(dir, "out")
	if _, err := identity.LoadOrCreateKey(mobKeyPath); err != nil {
		t.Fatal(err)
	}

	if _, err := StartAutoReceive("test-phone", outDir, sigURL, mobKeyPath, mobPairedPath, "", ""); err != nil {
		t.Fatalf("StartAutoReceive: %v", err)
	}
	defer func() { _ = StopAutoReceive() }()

	deadline := time.Now().Add(5 * time.Second)
	online := false
	for time.Now().Before(deadline) {
		if readConnSnapshot(t).SignalState == "online" {
			online = true
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if !online {
		t.Fatalf("StartAutoReceive failed to go online")
	}

	// Simulate desktop responder waiting with QR code.
	deskKey, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	deskPaired, err := identity.LoadPaired(filepath.Join(dir, "desk-paired.json"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	deskCl, err := signaling.Dial(ctx, sigURL, deskKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deskCl.Close()

	code := "654321"
	respErrCh := make(chan error, 1)
	go func() {
		_, perr := pairing.RunWithName(ctx, deskCl, deskKey, deskPaired, pairing.Respond, code, "", "my-pc", 5*time.Second)
		respErrCh <- perr
	}()
	time.Sleep(30 * time.Millisecond)

	rawURI, err := BuildMyPairURI(filepath.Join(dir, "desk-temp-unused.key"), code, "my-pc", 600)
	_ = rawURI
	// Build URI directly for deskKey and pair from mobile while StartAutoReceive is online.
	uri := pairing.BuildPairURI(deskKey.IDHex(), code, "my-pc", time.Now().UTC().Add(10*time.Minute))
	res, err := PairWithURINamed(sigURL, mobKeyPath, mobPairedPath, uri, "test-phone")
	if err != nil {
		t.Fatalf("PairWithURINamed while StartAutoReceive active failed: %v", err)
	}
	if !strings.Contains(res, `"ok":true`) {
		t.Fatalf("unexpected pair result: %s", res)
	}
	if rerr := <-respErrCh; rerr != nil {
		t.Fatalf("desktop responder failed: %v", rerr)
	}
	if st := readConnSnapshot(t).SignalState; st != "online" {
		t.Fatalf("StartAutoReceive signal_state after pairing = %q, want online", st)
	}
}
