package server

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/pairing"
	"github.com/zhitiaojun/ferry/core/relay"
	"github.com/zhitiaojun/ferry/core/signaling"
)

func wsURL(t *testing.T, ts *httptest.Server, path string) string {
	t.Helper()
	return "ws" + strings.TrimPrefix(ts.URL, "http") + path
}

func newTestServer(t *testing.T) (*httptest.Server, *Server) {
	t.Helper()
	srv := New(nil)
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts, srv
}

func TestSignalLoginAndPresence(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	a, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	ca, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), a, nil)
	if err != nil {
		t.Fatalf("dial a: %v", err)
	}
	defer ca.Close()

	presence := make(chan signaling.Presence, 1)
	ca.On(signaling.TypePresence, func(e *signaling.Envelope) {
		p, _ := signaling.Decode[signaling.Presence](e)
		presence <- p
	})

	cb, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), b, nil)
	if err != nil {
		t.Fatalf("dial b: %v", err)
	}
	defer cb.Close()

	select {
	case p := <-presence:
		if p.DeviceID != b.IDHex() || !p.Online {
			t.Fatalf("presence = %+v, want %s online", p, b.IDHex())
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for presence")
	}

	// A ping must round-trip through the same connection.
	if _, err := ca.Ping(ctx); err != nil {
		t.Fatalf("ping: %v", err)
	}
}

func TestSignalRejectsBadSignature(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	key, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	conn, _, err := websocket.Dial(ctx, wsURL(t, ts, "/signal"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()

	begin, _ := signaling.Build(signaling.TypeLoginBegin, "", signaling.LoginBegin{
		DeviceID: key.IDHex(), PubKey: key.PubHex(),
	})
	if err := writeTest(ctx, conn, begin); err != nil {
		t.Fatal(err)
	}
	raw := readTest(t, ctx, conn)
	if raw.Type != signaling.TypeLoginChal {
		t.Fatalf("want challenge, got %s", raw.Type)
	}
	ch, _ := signaling.Decode[signaling.LoginChallenge](raw)
	bad := make([]byte, 64)
	msg, _ := signaling.Build(signaling.TypeLogin, raw.ID, signaling.Login{Signature: hex.EncodeToString(bad)})
	if err := writeTest(ctx, conn, msg); err != nil {
		t.Fatal(err)
	}
	reply := readTest(t, ctx, conn)
	if reply.Type != signaling.TypeError {
		t.Fatalf("want error, got %s", reply.Type)
	}
	body, _ := signaling.Decode[signaling.ErrorBody](reply)
	if body.Code != signaling.ErrAuthFailed {
		t.Fatalf("error code = %q, want %q", body.Code, signaling.ErrAuthFailed)
	}
	_ = ch
}

func TestRelaySplice(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	keyA, _ := identity.GenerateKey()
	keyB, _ := identity.GenerateKey()
	a, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), keyA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), keyB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	bReady := make(chan signaling.RelayReady, 1)
	b.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		r, _ := signaling.Decode[signaling.RelayReady](e)
		bReady <- r
	})

	reply, err := a.Call(ctx, signaling.TypeRelayOpen, signaling.RelayOpen{
		ToDevice: keyB.IDHex(), TransferID: "0123456789abcdef0123456789abcdef",
	}, signaling.TypeRelayReady)
	if err != nil {
		t.Fatalf("relay_open: %v", err)
	}
	readyA, _ := signaling.Decode[signaling.RelayReady](reply)
	if readyA.Token == "" {
		t.Fatal("empty relay token")
	}
	var readyB signaling.RelayReady
	select {
	case readyB = <-bReady:
	case <-ctx.Done():
		t.Fatal("b did not receive relay_ready")
	}
	if readyB.Token != readyA.Token {
		t.Fatalf("tokens differ: %q vs %q", readyA.Token, readyB.Token)
	}

	urlA, err := relay.RelayURL(wsURL(t, ts, "/signal"), readyA.RelayURL, readyA.Token)
	if err != nil {
		t.Fatal(err)
	}
	urlB, err := relay.RelayURL(wsURL(t, ts, "/signal"), readyB.RelayURL, readyB.Token)
	if err != nil {
		t.Fatal(err)
	}

	ra, err := relay.Dial(ctx, urlA)
	if err != nil {
		t.Fatalf("dial relay a: %v", err)
	}
	defer ra.Close()
	rb, err := relay.Dial(ctx, urlB)
	if err != nil {
		t.Fatalf("dial relay b: %v", err)
	}
	defer rb.Close()

	go func() {
		_, _ = ra.Write([]byte("ping over relay"))
	}()

	buf := make([]byte, 64)
	n, err := rb.Read(buf)
	if err != nil {
		t.Fatalf("relay read: %v", err)
	}
	if string(buf[:n]) != "ping over relay" {
		t.Fatalf("relay payload = %q", buf[:n])
	}

	go func() {
		_, _ = rb.Write([]byte("pong over relay"))
	}()
	n, err = ra.Read(buf)
	if err != nil {
		t.Fatalf("relay read back: %v", err)
	}
	if string(buf[:n]) != "pong over relay" {
		t.Fatalf("relay payload back = %q", buf[:n])
	}

	// /metrics must report the bytes that went through the splice, plus the
	// build version and the liveness fields the monitoring relies on.
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatalf("metrics: %v", err)
	}
	defer resp.Body.Close()
	var m map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil {
		t.Fatalf("metrics json: %v", err)
	}
	if ok, _ := m["ok"].(bool); !ok {
		t.Fatalf("metrics ok = %v", m["ok"])
	}
	if b, _ := m["relay_bytes"].(float64); b <= 0 {
		t.Fatalf("relay_bytes = %v, want > 0", m["relay_bytes"])
	}
	if m["version"] == "" {
		t.Fatal("metrics version is empty")
	}
}

func TestUnknownTypeGetsError(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	key, _ := identity.GenerateKey()
	c, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), key, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	if _, err := c.Call(ctx, "no_such_type", map[string]any{}, signaling.TypeError); err == nil {
		t.Fatal("expected an error reply for an unknown type")
	}
}

func writeTest(ctx context.Context, conn *websocket.Conn, e *signaling.Envelope) error {
	return writeEnvelope(ctx, conn, e)
}

func readTest(t *testing.T, ctx context.Context, conn *websocket.Conn) *signaling.Envelope {
	t.Helper()
	_, raw, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	e, err := signaling.Parse(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return e
}

func TestPairBudgetPerPeer(t *testing.T) {
	srv := New(nil)
	p := &peer{id: "deadbeef"}
	for i := 0; i < srv.limits.PairBurst; i++ {
		if !srv.allowPair(p) {
			t.Fatalf("message %d rejected inside the burst budget", i+1)
		}
	}
	if srv.allowPair(p) {
		t.Fatal("message past PairBurst accepted")
	}
	// A fresh window restores the budget.
	p.pairMu.Lock()
	p.pairWindow = time.Now().Add(-srv.limits.PairWindow - time.Second)
	p.pairMu.Unlock()
	if !srv.allowPair(p) {
		t.Fatal("budget not restored after the window")
	}
}

func TestRoomThrottleParksOverBudget(t *testing.T) {
	r := &room{rate: 100}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	start := time.Now()
	r.throttle(ctx, 40)
	if d := time.Since(start); d > 500*time.Millisecond {
		t.Fatalf("under budget waited %v", d)
	}
	start = time.Now()
	r.throttle(ctx, 80) // 40+80 > 100/s
	if d := time.Since(start); d < 300*time.Millisecond {
		t.Fatalf("over budget did not wait (%v)", d)
	}
}

func TestLoginBudgetPerIP(t *testing.T) {
	srv := New(nil)
	for i := 0; i < srv.limits.LoginBurst; i++ {
		if !srv.allowLogin("203.0.113.7") {
			t.Fatalf("attempt %d rejected inside the burst budget", i+1)
		}
	}
	if srv.allowLogin("203.0.113.7") {
		t.Fatal("attempt past LoginBurst accepted")
	}
	if !srv.allowLogin("198.51.100.9") {
		t.Fatal("a different address must keep its own budget")
	}
	srv.loginMu.Lock()
	srv.loginLimits["203.0.113.7"].window = time.Now().Add(-srv.limits.LoginWindow - time.Second)
	srv.loginMu.Unlock()
	if !srv.allowLogin("203.0.113.7") {
		t.Fatal("budget not restored after the window")
	}
}

func TestPairNameExchange(t *testing.T) {
	ts, _ := newTestServer(t)
	sigURL := wsURL(t, ts, "/signal")
	dir := t.TempDir()

	initKey, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	respKey, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	initPaired, err := identity.LoadPaired(filepath.Join(dir, "init-paired.json"))
	if err != nil {
		t.Fatal(err)
	}
	respPaired, err := identity.LoadPaired(filepath.Join(dir, "resp-paired.json"))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initCl, err := signaling.Dial(ctx, sigURL, initKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer initCl.Close()

	respCl, err := signaling.Dial(ctx, sigURL, respKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer respCl.Close()

	const code = "482913"
	var wg sync.WaitGroup
	var respPeer pairing.Peer
	var respErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		respPeer, respErr = pairing.RunWithName(ctx, respCl, respKey, respPaired, pairing.Respond, code, "", "办公室电脑", 8*time.Second)
	}()

	time.Sleep(50 * time.Millisecond)
	initPeer, initErr := pairing.RunWithName(ctx, initCl, initKey, initPaired, pairing.Initiate, code, respKey.IDHex(), "我的小米手机", 8*time.Second)
	wg.Wait()

	if initErr != nil {
		t.Fatalf("initiate pairing: %v", initErr)
	}
	if respErr != nil {
		t.Fatalf("respond pairing: %v", respErr)
	}
	if initPeer.Name != "办公室电脑" {
		t.Fatalf("initiator got peer name = %q, want 办公室电脑", initPeer.Name)
	}
	if respPeer.Name != "我的小米手机" {
		t.Fatalf("responder got peer name = %q, want 我的小米手机", respPeer.Name)
	}

	if dev, ok := initPaired.Get(respKey.IDHex()); !ok || dev.Name != "办公室电脑" {
		t.Fatalf("initiator stored peer = %+v (ok=%v), want Name=办公室电脑", dev, ok)
	}
	if dev, ok := respPaired.Get(initKey.IDHex()); !ok || dev.Name != "我的小米手机" {
		t.Fatalf("responder stored peer = %+v (ok=%v), want Name=我的小米手机", dev, ok)
	}
}

func TestPingRequiresRegisteredPeer(t *testing.T) {
	ts, srv := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	k, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	cl, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), k, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer cl.Close()

	if !cl.Alive() || cl.LastPong().IsZero() {
		t.Fatalf("expected alive client with non-zero LastPong after login")
	}
	if _, err := cl.Ping(ctx); err != nil {
		t.Fatalf("initial ping: %v", err)
	}

	// Simulate stale/evicted registration in server peer table: ping must drop the socket.
	srv.mu.Lock()
	delete(srv.peers, k.IDHex())
	srv.mu.Unlock()

	pctx, pcancel := context.WithTimeout(ctx, 2*time.Second)
	defer pcancel()
	if _, err := cl.Ping(pctx); err == nil {
		t.Fatal("expected ping to fail when peer is no longer registered")
	}
}

func TestPairOfflineFailsFast(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	initKey, _ := identity.GenerateKey()
	offKey, _ := identity.GenerateKey()
	paired, err := identity.LoadPaired(filepath.Join(t.TempDir(), "paired.json"))
	if err != nil {
		t.Fatal(err)
	}
	cl, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), initKey, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cl.Close()

	start := time.Now()
	_, err = pairing.RunWithName(ctx, cl, initKey, paired, pairing.Initiate, "123456", offKey.IDHex(), "phone", 30*time.Second)
	if err == nil || !strings.Contains(err.Error(), "peer_offline") {
		t.Fatalf("err = %v, want peer_offline", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("offline pair took %v, want fast fail", elapsed)
	}
}

func TestRelayConnReadDeadline(t *testing.T) {
	ts, _ := newTestServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	keyA, _ := identity.GenerateKey()
	keyB, _ := identity.GenerateKey()
	a, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), keyA, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := signaling.Dial(ctx, wsURL(t, ts, "/signal"), keyB, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()

	bReady := make(chan signaling.RelayReady, 1)
	b.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		r, _ := signaling.Decode[signaling.RelayReady](e)
		bReady <- r
	})
	reply, err := a.Call(ctx, signaling.TypeRelayOpen, signaling.RelayOpen{
		ToDevice: keyB.IDHex(), TransferID: "fedcba9876543210fedcba9876543210",
	}, signaling.TypeRelayReady)
	if err != nil {
		t.Fatal(err)
	}
	readyA, _ := signaling.Decode[signaling.RelayReady](reply)
	readyB := <-bReady

	urlA, _ := relay.RelayURL(wsURL(t, ts, "/signal"), readyA.RelayURL, readyA.Token)
	urlB, _ := relay.RelayURL(wsURL(t, ts, "/signal"), readyB.RelayURL, readyB.Token)
	ra, err := relay.Dial(ctx, urlA)
	if err != nil {
		t.Fatal(err)
	}
	defer ra.Close()
	rb, err := relay.Dial(ctx, urlB)
	if err != nil {
		t.Fatal(err)
	}
	defer rb.Close()

	_ = ra.SetReadDeadline(time.Now().Add(150 * time.Millisecond))
	start := time.Now()
	buf := make([]byte, 16)
	_, err = ra.Read(buf)
	if err == nil {
		t.Fatal("expected deadline error on idle relay Read")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("relay Read took %v, expected ~150ms deadline", elapsed)
	}
}
