package punch

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestMessageRoundTrip(t *testing.T) {
	m := &Message{
		Type:     TypeBindingSuccess,
		Software: "ft-test",
		XORAddr:  netip.MustParseAddrPort("203.0.113.9:45678"),
		HasXOR:   true,
	}
	if _, err := rand.Read(m.Txn[:]); err != nil {
		t.Fatal(err)
	}
	raw, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != m.Type || got.Txn != m.Txn || !got.HasXOR {
		t.Fatalf("header mismatch: %+v", got)
	}
	if got.XORAddr != m.XORAddr {
		t.Fatalf("xor addr = %v, want %v", got.XORAddr, m.XORAddr)
	}
	if got.Software != "ft-test" {
		t.Fatalf("software = %q", got.Software)
	}
}

func TestMessageIPv6(t *testing.T) {
	m := &Message{Type: TypeBindingSuccess, XORAddr: netip.MustParseAddrPort("[2001:db8::1]:9999"), HasXOR: true}
	raw, err := m.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseMessage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if got.XORAddr != m.XORAddr {
		t.Fatalf("xor addr = %v, want %v", got.XORAddr, m.XORAddr)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := ParseMessage([]byte("GET / HTTP/1.1\r\n\r\n")); err == nil {
		t.Fatal("expected error for non-STUN datagram")
	}
	// right magic, truncated
	b := make([]byte, 20)
	binary.BigEndian.PutUint32(b[4:], MagicCookie)
	binary.BigEndian.PutUint16(b[2:], 100)
	if _, err := ParseMessage(b); err == nil {
		t.Fatal("expected error for truncated attribute area")
	}
}

// fakeServer answers binding requests, optionally rewriting the reported port
// to simulate symmetric NAT mapping.
func fakeServer(t *testing.T, reportPort uint16) (addr string, stop func()) {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		buf := make([]byte, 1500)
		for {
			n, from, err := conn.ReadFromUDP(buf)
			if err != nil {
				return
			}
			m, err := ParseMessage(buf[:n])
			if err != nil || m.Type != TypeBindingRequest {
				continue
			}
			port := from.AddrPort().Port()
			if reportPort != 0 {
				port = reportPort
			}
			resp := &Message{Type: TypeBindingSuccess, Txn: m.Txn, XORAddr: netip.AddrPortFrom(from.AddrPort().Addr(), port), HasXOR: true}
			raw, err := resp.Marshal()
			if err != nil {
				continue
			}
			_, _ = conn.WriteToUDP(raw, from)
			select {
			case <-done:
				return
			default:
			}
		}
	}()
	return conn.LocalAddr().String(), func() { close(done); conn.Close() }
}

func TestClientQuery(t *testing.T) {
	addr, stop := fakeServer(t, 0)
	defer stop()
	c, err := ListenClient(false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ap, rtt, err := c.Query(ctx, addr, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if !ap.IsValid() || ap.Port() == 0 {
		t.Fatalf("bad mapped address %v", ap)
	}
	if rtt <= 0 || rtt > 2*time.Second {
		t.Fatalf("unexpected rtt %v", rtt)
	}
	// The mapped address must be the local socket address (loopback -> same port).
	if ap.Port() != c.LocalAddr().Port() {
		t.Fatalf("mapped port %d != local port %d", ap.Port(), c.LocalAddr().Port())
	}
}

func TestDetectMappingCone(t *testing.T) {
	a, stopA := fakeServer(t, 0)
	defer stopA()
	b, stopB := fakeServer(t, 0)
	defer stopB()
	c, err := ListenClient(false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	ctx := context.Background()
	behavior, addrs, err := c.DetectMapping(ctx, []string{a, b}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if behavior != BehaviorCone {
		t.Fatalf("behavior = %s, want cone", behavior)
	}
	if len(addrs) != 2 || addrs[0].Port() != addrs[1].Port() {
		t.Fatalf("addresses = %v", addrs)
	}
}

func TestDetectMappingSymmetric(t *testing.T) {
	a, stopA := fakeServer(t, 1111)
	defer stopA()
	b, stopB := fakeServer(t, 2222)
	defer stopB()
	c, err := ListenClient(false)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	behavior, _, err := c.DetectMapping(context.Background(), []string{a, b}, 500*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if behavior != BehaviorSymmetric {
		t.Fatalf("behavior = %s, want symmetric", behavior)
	}
}

func TestHandleRequest(t *testing.T) {
	req, err := NewBindingRequest()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := req.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	from := netip.MustParseAddrPort("198.51.100.7:32000")
	out, ok := HandleRequest(raw, from)
	if !ok {
		t.Fatal("expected a binding response")
	}
	m, err := ParseMessage(out)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != TypeBindingSuccess || m.Txn != req.Txn || m.XORAddr != from {
		t.Fatalf("unexpected response: %+v", m)
	}
	if _, ok := HandleRequest([]byte("hello"), from); ok {
		t.Fatal("non-STUN datagram must not be answered")
	}
}
