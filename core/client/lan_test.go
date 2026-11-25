package client

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
)

func TestIsVerifiedPairedAndSendLAN(t *testing.T) {
	dir := t.TempDir()
	sendKeyPath, _, sendPairedPath, recvPairedPath, sendKey, recvKey := setupPairedKeys(t, dir)

	src := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(src, []byte("lan auth test payload"), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(dir, "recv-out")

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	// Case 1: Paired sender verified via CapAuth skips confirmation.
	done := make(chan error, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			done <- err
			return
		}
		defer nc.Close()
		s, err := session.Accept(nc, session.Params{
			DeviceID:   recvKey.ID(),
			Name:       "recv-pc",
			VerifyAuth: true,
		})
		if err != nil {
			done <- err
			return
		}
		defer s.Close()
		cfg := transfer.Config{}
		if !IsVerifiedPaired(recvPairedPath, s.Peer) {
			cfg.Confirm = func(_ *proto.Manifest) error {
				return proto.NewError(proto.CodeUnauthorized, "should not require confirm when paired")
			}
		}
		_, err = transfer.Receive(s, outDir, cfg)
		done <- err
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := SendLAN(ctx, Options{
		KeyPath:    sendKeyPath,
		PairedPath: sendPairedPath,
		Name:       "send-pc",
	}, ln.Addr().String(), []string{src})
	if err != nil {
		t.Fatalf("paired SendLAN: %v", err)
	}
	if res.Channel != "LAN" || res.Bytes == 0 {
		t.Fatalf("unexpected SendLAN result: %+v", res)
	}
	if err := <-done; err != nil {
		t.Fatalf("receiver paired err: %v", err)
	}

	// Case 2: Spoofed DeviceID (wrong key or missing paired entry) fails IsVerifiedPaired.
	if IsVerifiedPaired(recvPairedPath, session.Peer{DeviceID: sendKey.ID(), AuthVerified: false}) {
		t.Fatal("unverified peer must not pass IsVerifiedPaired")
	}
	otherKey, err := identity.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	if IsVerifiedPaired(recvPairedPath, session.Peer{DeviceID: sendKey.ID(), AuthVerified: true, PubKey: otherKey.Public()}) {
		t.Fatal("mismatched PubKey must not pass IsVerifiedPaired")
	}

	// Case 3: Unpaired sender triggers Confirm and gets rejected cleanly when unconfirmed.
	emptyPaired := filepath.Join(dir, "empty-paired.json")
	done2 := make(chan error, 1)
	go func() {
		nc, err := ln.Accept()
		if err != nil {
			done2 <- err
			return
		}
		defer nc.Close()
		s, err := session.Accept(nc, session.Params{
			DeviceID:   recvKey.ID(),
			Name:       "recv-pc",
			VerifyAuth: true,
		})
		if err != nil {
			done2 <- err
			return
		}
		defer s.Close()
		cfg := transfer.Config{}
		if !IsVerifiedPaired(emptyPaired, s.Peer) {
			cfg.Confirm = func(_ *proto.Manifest) error {
				return proto.NewError(proto.CodeUnauthorized, "unpaired rejected")
			}
		}
		_, err = transfer.Receive(s, filepath.Join(dir, "unpaired-out"), cfg)
		done2 <- err
	}()

	_, err = SendLAN(ctx, Options{
		KeyPath: sendKeyPath,
		Name:    "stranger-pc",
	}, ln.Addr().String(), []string{src})
	if err == nil {
		t.Fatal("expected unpaired SendLAN rejection")
	}
	if err := <-done2; err == nil {
		t.Fatal("expected receiver error on reject")
	}
}
