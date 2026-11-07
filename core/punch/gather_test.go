package punch

import (
	"net"
	"testing"
)

func TestPrimaryPrefersHighestPriority(t *testing.T) {
	cands := []Candidate{
		{Kind: "lan", Addr: "10.0.0.2:1000", Priority: 50},
		{Kind: "stun", Addr: "59.45.22.140:2000", Priority: 80},
		{Kind: "ipv6", Addr: "[2001:db8::1]:3000", Priority: 90},
	}
	addr, err := Primary(cands)
	if err != nil {
		t.Fatalf("primary: %v", err)
	}
	if addr.String() != "59.45.22.140:2000" {
		t.Fatalf("got %s, want the STUN candidate", addr)
	}
}

func TestPrimarySkipsUnparsable(t *testing.T) {
	_, err := Primary([]Candidate{{Kind: "lan", Addr: "not-an-address", Priority: 10}})
	if err == nil {
		t.Fatal("expected an error for an unparsable candidate")
	}
}

func TestResolveDropsIPv6(t *testing.T) {
	got := Resolve([]Candidate{
		{Kind: "lan", Addr: "10.0.0.2:1000"},
		{Kind: "ipv6", Addr: "[2001:db8::1]:3000"},
	})
	if len(got) != 1 || got[0].IP.String() != "10.0.0.2" {
		t.Fatalf("got %v", got)
	}
}

func TestCandidateAddressRoundTrip(t *testing.T) {
	// The wire format is host:port for IPv4, so parsing must be symmetric.
	for _, s := range []string{"192.168.1.5:4000", "59.45.22.140:62370"} {
		a, err := net.ResolveUDPAddr("udp4", s)
		if err != nil || a.String() != s {
			t.Fatalf("%s: %v (%v)", s, a, err)
		}
	}
}
