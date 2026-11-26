package session

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/proto"
)

func TestHandshakeAndPing(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	params := Params{Name: "tester", Capabilities: proto.CapResume}
	type res struct {
		s   *Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := Accept(b, params)
		ch <- res{s, err}
	}()
	sa, err := Dial(a, params)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatalf("accept: %v", got.err)
	}
	sb := got.s
	defer sa.Close()
	defer sb.Close()

	if sa.Peer.Name != "tester" || sb.Peer.Name != "tester" {
		t.Fatalf("peer names: %q / %q", sa.Peer.Name, sb.Peer.Name)
	}
	if sa.SessionID() != sb.SessionID() {
		t.Fatal("session ids differ")
	}
	// Ping is a round trip; the peer must read the PING and answer.
	go func() {
		v, err := sb.Expect(proto.TypePing, 2*time.Second)
		if err != nil {
			return
		}
		p, _ := v.(*proto.Ping)
		if p != nil {
			_ = sb.SendMsg(proto.TypePong, p)
		}
	}()
	if err := sa.Ping(2 * time.Second); err != nil {
		t.Fatalf("ping from dial side: %v", err)
	}
}

func TestExpectRejectsWrongType(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	type res struct {
		s   *Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := Accept(b, Params{Name: "b"})
		ch <- res{s, err}
	}()
	sa, err := Dial(a, Params{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatal(got.err)
	}
	sb := got.s
	defer sa.Close()
	defer sb.Close()

	sent := make(chan error, 1)
	go func() { sent <- sa.SendMsg(proto.TypePing, &proto.Ping{Token: [8]byte{1}}) }()
	_, err = sb.Expect(proto.TypePong, 2*time.Second)
	if serr := <-sent; serr != nil {
		t.Fatal(serr)
	}
	var pe *proto.Error
	if !errors.As(err, &pe) || pe.Code != proto.CodeUnexpectedMessage {
		t.Fatalf("want unexpected_message, got %v", err)
	}
}

func TestRemoteErrorPropagation(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	type res struct {
		s   *Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := Accept(b, Params{Name: "b"})
		ch <- res{s, err}
	}()
	sa, err := Dial(a, Params{Name: "a"})
	if err != nil {
		t.Fatal(err)
	}
	got := <-ch
	if got.err != nil {
		t.Fatal(got.err)
	}
	defer sa.Close()
	defer got.s.Close()

	sent := make(chan error, 1)
	go func() { sent <- got.s.SendError(proto.CodeUnauthorized, "nope") }()
	_, err = sa.Expect(proto.TypeData, 2*time.Second)
	if serr := <-sent; serr != nil && err == nil {
		t.Fatal(serr)
	}
	var re *RemoteError
	if !errors.As(err, &re) || re.Code != proto.CodeUnauthorized {
		t.Fatalf("want unauthorized RemoteError, got %v", err)
	}
}

func TestHandshakeCapAuthVerified(t *testing.T) {
	pubA, privA, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idA := sha256.Sum256(pubA)
	idB := sha256.Sum256([]byte("receiver-b"))

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	type res struct {
		s   *Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := Accept(b, Params{DeviceID: idB, Name: "recv", VerifyAuth: true})
		ch <- res{s, err}
	}()
	sa, err := Dial(a, Params{
		DeviceID: idA,
		Name:     "send",
		PubKey:   pubA,
		Sign:     func(m []byte) []byte { return ed25519.Sign(privA, m) },
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sa.Close()

	got := <-ch
	if got.err != nil {
		t.Fatalf("accept: %v", got.err)
	}
	defer got.s.Close()

	if !got.s.Peer.AuthVerified {
		t.Fatal("expected AuthVerified=true")
	}
	if !bytes.Equal(got.s.Peer.PubKey, pubA) {
		t.Fatal("verified PubKey mismatch")
	}
	if got.s.Peer.DeviceID != idA {
		t.Fatal("verified DeviceID mismatch")
	}
}

func TestHandshakeCapAuthRejectsBadSignature(t *testing.T) {
	pubA, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	_, otherPriv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	idA := sha256.Sum256(pubA)
	idB := sha256.Sum256([]byte("receiver-b"))

	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	ch := make(chan error, 1)
	go func() {
		_, err := Accept(b, Params{DeviceID: idB, Name: "recv", VerifyAuth: true})
		ch <- err
	}()
	sa, err := Dial(a, Params{
		DeviceID: idA,
		Name:     "impostor",
		PubKey:   pubA,
		Sign:     func(m []byte) []byte { return ed25519.Sign(otherPriv, m) },
	})
	if err == nil {
		_, err = sa.Expect(proto.TypeAck, 2*time.Second)
		_ = sa.Close()
	}
	var re *RemoteError
	if !errors.As(err, &re) || re.Code != proto.CodeUnauthorized {
		t.Fatalf("dial/recv err = %v, want RemoteError CodeUnauthorized", err)
	}
	accErr := <-ch
	var pe *proto.Error
	if !errors.As(accErr, &pe) || pe.Code != proto.CodeUnauthorized {
		t.Fatalf("accept err = %v, want CodeUnauthorized", accErr)
	}
}

func TestHandshakeLegacySenderFallbackUnverified(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()

	type res struct {
		s   *Session
		err error
	}
	ch := make(chan res, 1)
	go func() {
		s, err := Accept(b, Params{Name: "recv", VerifyAuth: true})
		ch <- res{s, err}
	}()
	// Legacy sender without PubKey/Sign does not advertise CapAuth.
	sa, err := Dial(a, Params{Name: "legacy-sender", Capabilities: proto.CapResume})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer sa.Close()

	got := <-ch
	if got.err != nil {
		t.Fatalf("accept: %v", got.err)
	}
	defer got.s.Close()

	if got.s.Peer.AuthVerified {
		t.Fatal("expected AuthVerified=false for legacy sender")
	}
	if len(got.s.Peer.PubKey) != 0 {
		t.Fatal("expected empty PubKey for legacy sender")
	}
}
