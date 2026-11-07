package pathmgr

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"testing"
	"time"
)

// TestQUICPunchSocket mimics the punch flow: both sockets bind 0.0.0.0, junk
// datagrams are written concurrently, and the dial targets the listener port.
func TestQUICPunchSocket(t *testing.T) {
	cert := selfSigned(t)
	cfg := &tls.Config{Certificates: []tls.Certificate{cert}, InsecureSkipVerify: true, MinVersion: tls.VersionTLS13}

	srvUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer srvUDP.Close()
	srvPort := srvUDP.LocalAddr().(*net.UDPAddr).Port
	srv := NewQUIC(srvUDP, cfg)
	defer srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := srv.PrepareListen(ctx); err != nil {
		t.Fatal(err)
	}

	cliUDP, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		t.Fatal(err)
	}
	defer cliUDP.Close()
	cli := NewQUIC(cliUDP, cfg)
	defer cli.Close()

	// junk writes on both sides, like Gathered.Punch
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		for {
			select {
			case <-stop:
				return
			case <-time.After(50 * time.Millisecond):
				_, _ = srvUDP.WriteToUDP([]byte("FT-PUNCH-1"), cliUDP.LocalAddr().(*net.UDPAddr))
				_, _ = cliUDP.WriteToUDP([]byte("FT-PUNCH-1"), &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: srvPort})
			}
		}
	}()

	done := make(chan error, 1)
	go func() {
		c, err := srv.Accept(ctx)
		if err != nil {
			done <- err
			return
		}
		buf := make([]byte, 4)
		_, err = c.Read(buf)
		if err == nil {
			_, err = c.Write([]byte("pong"))
		}
		done <- err
	}()

	conn, err := cli.DialAddr(ctx, net.JoinHostPort("127.0.0.1", fmt.Sprint(srvPort)))
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
	if err := <-done; err != nil {
		t.Fatalf("server: %v", err)
	}
}
