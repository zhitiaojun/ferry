package server

import (
	"context"
	"encoding/hex"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/signaling"
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
