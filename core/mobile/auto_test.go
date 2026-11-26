package mobile

import (
	"encoding/json"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/pairing"
	"github.com/zhitiaojun/ferry/core/proto"
	"github.com/zhitiaojun/ferry/core/relay"
	"github.com/zhitiaojun/ferry/core/session"
	"github.com/zhitiaojun/ferry/core/signaling"
	"github.com/zhitiaojun/ferry/core/transfer"
	"github.com/zhitiaojun/ferry/server"
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

func TestSendAutoReusesSignalWithoutDisconnect(t *testing.T) {
	_ = StopAutoReceive()

	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	mobKeyPath := filepath.Join(dir, "mob.key")
	mobPairedPath := filepath.Join(dir, "mob-paired.json")
	outDir := filepath.Join(dir, "out")
	deskRecvDir := filepath.Join(dir, "desk-recv")

	mobKey, err := identity.LoadOrCreateKey(mobKeyPath)
	if err != nil {
		t.Fatal(err)
	}
	deskKey, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	mobPaired, err := identity.LoadPaired(mobPairedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := mobPaired.Add(identity.PairedDevice{
		DeviceID: deskKey.IDHex(), Name: "my-pc", PubKey: deskKey.PubHex(), PairedAt: time.Now().UTC(),
	}); err != nil {
		t.Fatal(err)
	}
	if err := mobPaired.Save(); err != nil {
		t.Fatal(err)
	}

	if _, err := StartAutoReceive("test-phone", outDir, sigURL, mobKeyPath, mobPairedPath, "", ""); err != nil {
		t.Fatalf("StartAutoReceive: %v", err)
	}
	defer func() { _ = StopAutoReceive() }()

	var initialCl *signaling.Client
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		snap := readConnSnapshot(t)
		sigMu.Lock()
		c := sigClient
		sigMu.Unlock()
		if snap.SignalState == "online" && c != nil {
			initialCl = c
			break
		}
		time.Sleep(25 * time.Millisecond)
	}
	if initialCl == nil {
		t.Fatalf("StartAutoReceive failed to go online")
	}

	// Simulate paired peer (desktop) connected on signaling server receiving relay file.
	ctx := t.Context()
	deskCl, err := signaling.Dial(ctx, sigURL, deskKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer deskCl.Close()

	reqCh := make(chan signaling.SendRequest, 1)
	readyCh := make(chan signaling.RelayReady, 1)
	deskCl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		if r, rerr := signaling.Decode[signaling.SendRequest](e); rerr == nil {
			reqCh <- r
		}
	})
	deskCl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			readyCh <- rr
		}
	})

	recvErrCh := make(chan error, 1)
	go func() {
		req := <-reqCh
		if err := deskCl.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
			ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
		}); err != nil {
			recvErrCh <- err
			return
		}
		if err := deskCl.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
			ToDevice: req.FromDevice, SessionID: req.SessionID, Candidates: []signaling.Candidate{},
		}); err != nil {
			recvErrCh <- err
			return
		}
		rr := <-readyCh
		rc, err := relay.Dial(ctx, relayURL(sigURL, rr))
		if err != nil {
			recvErrCh <- err
			return
		}
		conn, err := serverTLS(ctx, net.Conn(rc), deskKey, mobKey.Public())
		if err != nil {
			recvErrCh <- err
			return
		}
		sess, err := session.Accept(conn, session.Params{
			DeviceID: deskKey.ID(), Name: "my-pc", Capabilities: proto.CapResume,
		})
		if err != nil {
			_ = conn.Close()
			recvErrCh <- err
			return
		}
		defer sess.Drain(2 * time.Second)
		_, err = transfer.Receive(sess, deskRecvDir, transfer.Config{TransferID: hex16(req.TransferID)})
		recvErrCh <- err
	}()

	srcFile := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(srcFile, []byte("hello reused signaling"), 0o644); err != nil {
		t.Fatal(err)
	}
	pathsJSON, _ := json.Marshal([]string{srcFile})

	rawRes, err := SendAuto(sigURL, mobKeyPath, mobPairedPath, deskKey.IDHex(), "test-phone", string(pathsJSON), "", "")
	if err != nil {
		t.Fatalf("SendAuto failed: %v", err)
	}
	if !strings.Contains(rawRes, `"ok":true`) {
		t.Fatalf("unexpected SendAuto summary: %s", rawRes)
	}
	if rerr := <-recvErrCh; rerr != nil {
		t.Fatalf("receiver error: %v", rerr)
	}

	sigMu.Lock()
	afterCl := sigClient
	sigMu.Unlock()
	if afterCl != initialCl {
		t.Fatalf("SendAuto replaced active sigClient (%p -> %p), causing disconnect flicker", initialCl, afterCl)
	}
	select {
	case <-initialCl.Done():
		t.Fatal("initial sigClient closed during SendAuto")
	default:
	}
	if st := readConnSnapshot(t).SignalState; st != "online" {
		t.Fatalf("SignalState after SendAuto = %q, want online", st)
	}
}

func TestAutoReceiveScreenAwakeAndNetworkPause(t *testing.T) {
	_ = StopAutoReceive()
	SetScreenAwake(true)
	SetNetworkAvailable(true)
	defer func() {
		_ = StopAutoReceive()
		SetScreenAwake(true)
		SetNetworkAvailable(true)
	}()

	srv := server.New(nil)
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	sigURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/signal"

	dir := t.TempDir()
	keyPath := filepath.Join(dir, "mob.key")
	pairedPath := filepath.Join(dir, "paired.json")
	outDir := filepath.Join(dir, "out")

	if _, err := StartAutoReceive("test-phone", outDir, sigURL, keyPath, pairedPath, "", ""); err != nil {
		t.Fatalf("StartAutoReceive: %v", err)
	}

	waitState := func(wantSignal string, wantAwake, wantNet bool) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			var raw struct {
				SignalState      string `json:"signal_state"`
				ScreenAwake      bool   `json:"screen_awake"`
				NetworkAvailable bool   `json:"network_available"`
			}
			if err := json.Unmarshal([]byte(ConnectionStatus()), &raw); err == nil {
				if raw.SignalState == wantSignal && raw.ScreenAwake == wantAwake && raw.NetworkAvailable == wantNet {
					return
				}
			}
			time.Sleep(25 * time.Millisecond)
		}
		t.Fatalf("timed out waiting for signal=%q awake=%v net=%v (got %s)", wantSignal, wantAwake, wantNet, ConnectionStatus())
	}

	waitState("online", true, true)

	SetScreenAwake(false)
	waitState("offline", false, true)

	SetScreenAwake(true)
	waitState("online", true, true)

	SetNetworkAvailable(false)
	waitState("offline", true, false)

	SetNetworkAvailable(true)
	waitState("online", true, true)
}
