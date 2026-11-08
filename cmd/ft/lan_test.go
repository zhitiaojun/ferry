package main

import (
	"net"
	"testing"
)

func TestLanRoutedThroughTunnel(t *testing.T) {
	cases := []struct {
		local, remote string
		want          bool
	}{
		{"198.18.0.1", "10.201.2.123", true},    // TUN captured a LAN peer
		{"10.201.2.127", "10.201.2.123", false}, // normal LAN
		{"198.18.0.2", "198.18.0.9", false},     // both sides on the tunnel
		{"10.0.0.5", "198.18.0.1", false},       // peer is the tunnel
	}
	for _, c := range cases {
		got := lanRoutedThroughTunnel(net.ParseIP(c.local), net.ParseIP(c.remote))
		if got != c.want {
			t.Fatalf("%s -> %s: got %v want %v", c.local, c.remote, got, c.want)
		}
	}
}
