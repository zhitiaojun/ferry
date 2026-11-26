package server

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/zhitiaojun/ferry/core/punch"
)

func TestSTUNServerAnswersClient(t *testing.T) {
	s, err := ListenSTUN([]string{"127.0.0.1:0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx) }()

	addr := s.Addrs()[0]
	c, err := punch.ListenClient(false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ap, _, err := c.Query(context.Background(), addr, 800*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	local := c.LocalAddr()
	if !ap.Addr().IsLoopback() || ap.Port() != local.Port() {
		t.Fatalf("mapped %v, local %v", ap, local)
	}
}

func TestSTUNServerIgnoresGarbage(t *testing.T) {
	s, err := ListenSTUN([]string{"127.0.0.1:0"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { _ = s.Serve(ctx) }()

	conn, err := net.Dial("udp", s.Addrs()[0])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("not a stun packet")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 64)
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if _, err := conn.Read(buf); err == nil {
		t.Fatal("server must not answer non-STUN datagrams")
	}
}
