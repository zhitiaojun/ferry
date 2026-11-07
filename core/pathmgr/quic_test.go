package pathmgr

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"testing"
	"time"
)

func selfSigned(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// TestQUICLoopback verifies the QUIC path adapter without any punching.
func TestQUICLoopback(t *testing.T) {
	cert := selfSigned(t)
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}

	srvUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer srvUDP.Close()

	srv := NewQUIC(srvUDP, cfg)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.PrepareListen(ctx); err != nil {
		t.Fatal(err)
	}

	cliUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	cli := NewQUIC(cliUDP, cfg)
	defer cli.Close()
	cli.SetPeer(srvUDP.LocalAddr().String())

	done := make(chan error, 1)
	go func() {
		c, err := srv.Accept(ctx)
		if err != nil {
			done <- err
			return
		}
		buf := make([]byte, 4)
		if _, err := c.Read(buf); err != nil {
			done <- err
			return
		}
		_, err = c.Write([]byte("pong"))
		done <- err
	}()

	conn, err := cli.Dial(ctx)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatalf("write: %v", err)
	}
	buf := make([]byte, 4)
	if _, err := conn.Read(buf); err != nil {
		t.Fatalf("read: %v", err)
	}
	if string(buf) != "pong" {
		t.Fatalf("got %q", buf)
	}
	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}
