package punch

import (
	"context"
	"crypto/sha256"
	"math/rand/v2"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
)

func TestBirthdayPunchAndConnectLoopback(t *testing.T) {
	keyA, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	keyB, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}

	// Bind sockets inside a deterministic high-port block so probes collide
	// reliably regardless of whether OS ephemeral allocation is random (Linux) or sequential (Windows).
	lo := 38000 + rand.IntN(18000)
	hi := lo + 400

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfgA := BirthdayConfig{
		SelfID:          keyA.ID(),
		PeerID:          keyB.ID(),
		SessionID:       "sess-bp-1",
		PeerCandidates:  []Candidate{{Kind: "lan", Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(lo)), Priority: 50}},
		NumSockets:      24,
		Waves:           3,
		ProbesPerWave:   300,
		WaveInterval:    150 * time.Millisecond,
		PortMin:         lo,
		PortMax:         hi,
		BindInPortRange: true,
	}
	cfgB := cfgA
	cfgB.SelfID = keyB.ID()
	cfgB.PeerID = keyA.ID()

	type res struct {
		s       *session.Session
		cleanup func()
		err     error
	}
	chA := make(chan res, 1)
	chB := make(chan res, 1)

	go func() {
		s, cl, e := BirthdayConnect(ctx, cfgA, keyA, keyB.Public(), session.Params{
			DeviceID:     keyA.ID(),
			Name:         "peerA",
			Capabilities: proto.CapResume | proto.CapHotUpgrade,
		})
		chA <- res{s, cl, e}
	}()
	go func() {
		s, cl, e := BirthdayConnect(ctx, cfgB, keyB, keyA.Public(), session.Params{
			DeviceID:     keyB.ID(),
			Name:         "peerB",
			Capabilities: proto.CapResume | proto.CapHotUpgrade,
		})
		chB <- res{s, cl, e}
	}()

	ra := <-chA
	rb := <-chB
	if ra.err != nil {
		t.Fatalf("BirthdayConnect A: %v", ra.err)
	}
	defer ra.cleanup()
	defer ra.s.Close()
	if rb.err != nil {
		t.Fatalf("BirthdayConnect B: %v", rb.err)
	}
	defer rb.cleanup()
	defer rb.s.Close()

	if ra.s.Peer.Name != "peerB" || rb.s.Peer.Name != "peerA" {
		t.Fatalf("unexpected peer names: A saw %q, B saw %q", ra.s.Peer.Name, rb.s.Peer.Name)
	}
	go func() {
		f, rerr := rb.s.Recv(2 * time.Second)
		if rerr == nil && f.Type == proto.TypePing {
			if p, perr := proto.ParsePing(f.Payload); perr == nil {
				_ = rb.s.SendMsg(proto.TypePong, p)
			}
		}
	}()
	if err := ra.s.Ping(2 * time.Second); err != nil {
		t.Fatalf("session Ping over birthday QUIC failed: %v", err)
	}
}

func TestBirthdayPunchCancelCleanly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	idA := sha256.Sum256([]byte("a"))
	idB := sha256.Sum256([]byte("b"))
	_, _, err := BirthdayPunch(ctx, BirthdayConfig{
		SelfID:         idA,
		PeerID:         idB,
		SessionID:      "cancel-test",
		PeerCandidates: []Candidate{{Kind: "lan", Addr: "127.0.0.1:59999"}},
		NumSockets:     12,
		Waves:          2,
		ProbesPerWave:  64,
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}
