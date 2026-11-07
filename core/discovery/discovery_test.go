package discovery

import (
	"net"
	"strings"
	"testing"
)

func testID(t *testing.T) [32]byte {
	t.Helper()
	var id [32]byte
	for i := range id {
		id[i] = byte(i)
	}
	return id
}

func TestAnnouncementRoundTrip(t *testing.T) {
	cfg := Config{DeviceID: testID(t), Name: "HyperX", Port: 18800}
	b, err := cfg.announcement("WLAN")
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	a, err := ParseAnnouncement(b)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if a.Name != "HyperX" || a.Port != 18800 || a.Iface != "WLAN" || a.V != PayloadVersion {
		t.Fatalf("unexpected announcement: %+v", a)
	}
	id, err := a.DeviceIDBytes()
	if err != nil || id != cfg.DeviceID {
		t.Fatalf("device id mismatch: %v %x", err, id)
	}
}

func TestAnnouncementRejectsInvalid(t *testing.T) {
	good := Announcement{V: PayloadVersion, Type: "announce", DeviceID: strings.Repeat("ab", 32), Name: "n", Port: 1}
	cases := map[string]func(*Announcement){
		"version":   func(a *Announcement) { a.V = 2 },
		"type":      func(a *Announcement) { a.Type = "hello" },
		"device_id": func(a *Announcement) { a.DeviceID = "abcd" },
		"name":      func(a *Announcement) { a.Name = "" },
		"long_name": func(a *Announcement) { a.Name = strings.Repeat("x", MaxNameLen+1) },
		"port":      func(a *Announcement) { a.Port = 0 },
	}
	for name, mutate := range cases {
		a := good
		mutate(&a)
		if _, err := a.Encode(); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestParseAnnouncementRejectsGarbage(t *testing.T) {
	for _, b := range [][]byte{nil, []byte("{}"), []byte("not json"), make([]byte, MaxPayload+1)} {
		if _, err := ParseAnnouncement(b); err == nil {
			t.Errorf("expected error for %d bytes", len(b))
		}
	}
}

func TestInterfaceClassification(t *testing.T) {
	virtual := []string{"Meta", "Wintun", "vEthernet (Default Switch)", "Tailscale", "WireGuard", "utun3", "clash-tun"}
	physical := []string{"WLAN", "Ethernet", "以太网", "Wi-Fi"}
	for _, n := range virtual {
		if !isVirtualName(n) {
			t.Errorf("%q should be classified virtual", n)
		}
	}
	for _, n := range physical {
		if isVirtualName(n) {
			t.Errorf("%q should be physical", n)
		}
	}
	if !isFakeIP(net.ParseIP("198.18.0.1")) || !isFakeIP(net.ParseIP("198.19.255.254")) {
		t.Error("198.18.0.0/15 must be fake-ip")
	}
	if isFakeIP(net.ParseIP("198.20.0.1")) {
		t.Error("198.20.0.1 is outside the fake-ip range")
	}
	if b := broadcastFor(net.ParseIP("10.201.2.127"), net.CIDRMask(17, 32)); !b.Equal(net.ParseIP("10.201.127.255")) {
		t.Errorf("broadcast = %v, want 10.201.127.255", b)
	}
}
