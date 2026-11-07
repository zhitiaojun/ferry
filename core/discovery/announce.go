package discovery

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/cwlindean/filetransfer/core/logging"
)

const (
	// Port is the UDP port used for LAN announcements.
	Port = 40002
	// MulticastGroup is the admin-scoped IPv4 group used next to broadcast.
	MulticastGroup = "239.255.42.99"
	// PayloadVersion is the announcement format version.
	PayloadVersion = 1
	// DefaultInterval is how often a device announces itself.
	DefaultInterval = time.Second
	// DeviceTTL is how long a peer stays in the table without announcements.
	DeviceTTL = 15 * time.Second
	// MaxPayload is the largest announcement datagram accepted.
	MaxPayload = 1024
	// MaxNameLen matches the HELLO device name limit.
	MaxNameLen = 64
)

// Announcement is the JSON datagram a device sends to be discovered.
type Announcement struct {
	V        int    `json:"v"`
	Type     string `json:"type"`
	DeviceID string `json:"device_id"`
	Name     string `json:"name"`
	Port     int    `json:"port"`
	Iface    string `json:"iface,omitempty"`
}

// Encode validates and serialises the announcement.
func (a Announcement) Encode() ([]byte, error) {
	if a.V != PayloadVersion {
		return nil, fmt.Errorf("discovery: unsupported payload version %d", a.V)
	}
	if a.Type != "announce" {
		return nil, fmt.Errorf("discovery: unknown payload type %q", a.Type)
	}
	raw, err := hex.DecodeString(a.DeviceID)
	if err != nil || len(raw) != 32 {
		return nil, errors.New("discovery: device_id must be 32 bytes of hex")
	}
	if a.Name == "" || len(a.Name) > MaxNameLen {
		return nil, fmt.Errorf("discovery: name must be 1..%d bytes", MaxNameLen)
	}
	if a.Port <= 0 || a.Port > 65535 {
		return nil, fmt.Errorf("discovery: invalid port %d", a.Port)
	}
	b, err := json.Marshal(a)
	if err != nil {
		return nil, err
	}
	if len(b) > MaxPayload {
		return nil, errors.New("discovery: announcement too large")
	}
	return b, nil
}

// ParseAnnouncement validates a received datagram.
func ParseAnnouncement(b []byte) (*Announcement, error) {
	if len(b) == 0 || len(b) > MaxPayload {
		return nil, errors.New("discovery: announcement size out of range")
	}
	var a Announcement
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("discovery: decode announcement: %w", err)
	}
	if _, err := a.Encode(); err != nil { // reuse validation
		return nil, err
	}
	return &a, nil
}

// DeviceID parses the announcement's hex device ID.
func (a Announcement) DeviceIDBytes() ([32]byte, error) {
	var id [32]byte
	raw, err := hex.DecodeString(a.DeviceID)
	if err != nil || len(raw) != 32 {
		return id, errors.New("discovery: invalid device_id")
	}
	copy(id[:], raw)
	return id, nil
}

// Config controls announcing and listening.
type Config struct {
	DeviceID [32]byte
	Name     string
	// Port is the TCP port peers should dial.
	Port     int
	Interval time.Duration
	Log      *logging.Logger
}

func (c Config) interval() time.Duration {
	if c.Interval <= 0 {
		return DefaultInterval
	}
	return c.Interval
}

func (c Config) announcement(iface string) ([]byte, error) {
	return Announcement{
		V:        PayloadVersion,
		Type:     "announce",
		DeviceID: hex.EncodeToString(c.DeviceID[:]),
		Name:     c.Name,
		Port:     c.Port,
		Iface:    iface,
	}.Encode()
}

// Announce sends announcements on every eligible physical interface until ctx
// is done. Broadcast (directed per interface) and multicast are both sent.
func Announce(ctx context.Context, cfg Config) error {
	ifaces, err := Eligible()
	if err != nil {
		return err
	}
	if len(ifaces) == 0 {
		return errors.New("discovery: no eligible interface for announcement")
	}
	if cfg.Log != nil {
		names := make([]string, 0, len(ifaces))
		for _, ifc := range ifaces {
			names = append(names, ifc.String())
		}
		cfg.Log.Event("discovery.announce.start", map[string]any{"interfaces": names, "port": cfg.Port})
	}

	type sender struct {
		ifc  Iface
		pc   *ipv4.PacketConn
		data []byte
	}
	senders := make([]sender, 0, len(ifaces))
	defer func() {
		for _, s := range senders {
			_ = s.pc.Close()
		}
	}()

	mcast := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: Port}
	for _, ifc := range ifaces {
		conn, err := listenUDP(&net.UDPAddr{IP: ifc.IP, Port: 0})
		if err != nil {
			if cfg.Log != nil {
				cfg.Log.Event("discovery.announce.iface_error", map[string]any{"iface": ifc.String(), "error": err.Error()})
			}
			continue
		}
		pc := ipv4.NewPacketConn(conn)
		if err := pc.SetMulticastInterface(&net.Interface{Index: ifc.Index}); err != nil && cfg.Log != nil {
			cfg.Log.Event("discovery.announce.multicast_iface_error", map[string]any{"iface": ifc.String(), "error": err.Error()})
		}
		data, err := cfg.announcement(ifc.Name)
		if err != nil {
			_ = pc.Close()
			return err
		}
		senders = append(senders, sender{ifc: ifc, pc: pc, data: data})
	}
	if len(senders) == 0 {
		return errors.New("discovery: could not open any announcement socket")
	}

	send := func() {
		for _, s := range senders {
			cm := &ipv4.ControlMessage{IfIndex: s.ifc.Index}
			if _, err := s.pc.WriteTo(s.data, cm, &net.UDPAddr{IP: s.ifc.Broadcast, Port: Port}); err != nil && cfg.Log != nil {
				cfg.Log.Event("discovery.announce.broadcast_error", map[string]any{"iface": s.ifc.String(), "error": err.Error()})
			}
			if _, err := s.pc.WriteTo(s.data, cm, mcast); err != nil && cfg.Log != nil {
				cfg.Log.Event("discovery.announce.multicast_error", map[string]any{"iface": s.ifc.String(), "error": err.Error()})
			}
		}
	}
	send()
	ticker := time.NewTicker(cfg.interval())
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			send()
		}
	}
}
