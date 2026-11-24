package ipc

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeHandler struct {
	lastSend SendRequest
	opened   bool
}

func (f *fakeHandler) Status(context.Context) StatusResponse {
	return StatusResponse{
		OK:          true,
		Version:     "1.0.0-test",
		DeviceID:    "abcdef",
		Name:        "desk",
		SignalState: "online",
		LANPort:     18800,
	}
}

func (f *fakeHandler) Peers(context.Context) (PeersResponse, error) {
	return PeersResponse{
		Paired: []PairedPeerInfo{{DeviceID: "peer1", Name: "Phone", Online: true}},
		LAN:    []LANPeerInfo{{DeviceID: "peer1", Name: "Phone", Addr: "192.168.1.10:18800"}},
	}, nil
}

func (f *fakeHandler) Send(_ context.Context, req SendRequest) SendResponse {
	f.lastSend = req
	if len(req.Files) == 0 {
		return SendResponse{OK: false, Error: "no files"}
	}
	return SendResponse{
		OK: true, Path: "lan", Peer: "Phone", TransferID: "tx1", Files: req.Files, Bytes: 42, Chunks: 1, DurationMS: 5,
	}
}

func (f *fakeHandler) SendText(_ context.Context, req SendTextRequest) SendResponse {
	return SendResponse{OK: true, Path: "punch", Peer: req.Peer, Files: []string{req.Filename}, Bytes: int64(len(req.Text)), Chunks: 1}
}

func (f *fakeHandler) OpenUI(context.Context) error {
	f.opened = true
	return nil
}

func TestIPCRoundTripAuthAndClose(t *testing.T) {
	stateDir := t.TempDir()
	h := &fakeHandler{}
	srv, err := Start(stateDir, h, StartOptions{KeyPath: "k", PairedPath: "p"})
	if err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	// Unauthenticated request must fail with 401.
	resp, err := http.Get("http://" + srv.Addr() + "/v1/status")
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", resp.StatusCode)
	}

	cl, err := TryConnect(stateDir)
	if err != nil {
		t.Fatalf("TryConnect: %v", err)
	}

	ctx := t.Context()
	st, err := cl.Status(ctx)
	if err != nil || !st.OK || st.Name != "desk" || st.LANPort != 18800 {
		t.Fatalf("Status = %+v, err=%v", st, err)
	}

	peers, err := cl.Peers(ctx)
	if err != nil || len(peers.Paired) != 1 || peers.Paired[0].Name != "Phone" {
		t.Fatalf("Peers = %+v, err=%v", peers, err)
	}

	sr, err := cl.Send(ctx, SendRequest{Files: []string{"a.txt"}, Interactive: true})
	if err != nil || !sr.OK || sr.Path != "lan" || !h.lastSend.Interactive {
		t.Fatalf("Send = %+v, err=%v", sr, err)
	}

	if err := cl.Open(ctx); err != nil || !h.opened {
		t.Fatalf("OpenUI err=%v opened=%v", err, h.opened)
	}

	if err := srv.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(stateDir, discoveryFileName)); !os.IsNotExist(err) {
		t.Fatalf("daemon.json not removed on Close: err=%v", err)
	}

	// Stale daemon.json pointing to dead port must fail fast.
	_ = os.WriteFile(filepath.Join(stateDir, discoveryFileName), []byte(`{"addr":"`+srv.Addr()+`","token":"dead"}`), 0o600)
	start := time.Now()
	if _, err := TryConnect(stateDir); err == nil {
		t.Fatal("expected TryConnect on stale daemon.json to fail")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("stale TryConnect took too long: %s", time.Since(start))
	}
}
