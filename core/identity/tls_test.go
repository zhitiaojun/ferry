package identity

import (
	"crypto/tls"
	"net"
	"testing"
)

func TestPinnedTLSHandshake(t *testing.T) {
	a, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	b, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	clientCfg, err := ClientTLS(a, b.Public())
	if err != nil {
		t.Fatal(err)
	}
	serverCfg, err := ServerTLS(b, a.Public())
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	errCh := make(chan error, 1)
	go func() {
		s := tls.Server(c2, serverCfg)
		errCh <- s.Handshake()
		s.Close()
	}()
	cl := tls.Client(c1, clientCfg)
	if err := cl.Handshake(); err != nil {
		t.Fatalf("client handshake: %v", err)
	}
	if err := <-errCh; err != nil {
		t.Fatalf("server handshake: %v", err)
	}
	cl.Close()
}
