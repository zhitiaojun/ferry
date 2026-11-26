package discovery

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"golang.org/x/net/ipv4"

	"github.com/zhitiaojun/ferry/core/logging"
)

// Device is a peer seen on the LAN.
type Device struct {
	DeviceID  [32]byte
	Name      string
	IP        net.IP
	TCPAddr   string
	Iface     string
	FirstSeen time.Time
	LastSeen  time.Time
}

// Listener receives announcements on Port from every eligible interface.
type Listener struct {
	conn *net.UDPConn
	pc   *ipv4.PacketConn
	self [32]byte
	log  *logging.Logger

	mu      sync.Mutex
	devices map[[32]byte]Device
	closed  bool
}

// Listen binds the announcement port and joins the multicast group on each
// eligible physical interface. Run must be called to receive datagrams.
func Listen(cfg Config) (*Listener, error) {
	conn, err := listenUDP(&net.UDPAddr{IP: net.IPv4zero, Port: Port})
	if err != nil {
		return nil, fmt.Errorf("discovery: listen udp :%d: %w", Port, err)
	}
	pc := ipv4.NewPacketConn(conn)
	group := &net.UDPAddr{IP: net.ParseIP(MulticastGroup), Port: Port}
	ifaces, err := cfg.ifaces()
	if err != nil {
		_ = conn.Close()
		return nil, err
	}
	joined := 0
	for _, ifc := range ifaces {
		if err := pc.JoinGroup(&net.Interface{Index: ifc.Index}, group); err != nil {
			if cfg.Log != nil {
				cfg.Log.Event("discovery.join_error", map[string]any{"iface": ifc.String(), "error": err.Error()})
			}
			continue
		}
		joined++
	}
	if cfg.Log != nil {
		cfg.Log.Event("discovery.listen", map[string]any{"port": Port, "joined_interfaces": joined, "eligible": len(ifaces)})
	}
	l := &Listener{conn: conn, pc: pc, self: cfg.DeviceID, log: cfg.Log, devices: map[[32]byte]Device{}}
	return l, nil
}

// Run reads announcements until ctx is done or the socket fails.
func (l *Listener) Run(ctx context.Context) error {
	buf := make([]byte, 2048)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		if err := l.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond)); err != nil {
			return err
		}
		n, from, err := l.conn.ReadFromUDP(buf)
		if err != nil {
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				continue
			}
			select {
			case <-ctx.Done():
				return nil
			default:
			}
			return fmt.Errorf("discovery: read: %w", err)
		}
		l.handle(buf[:n], from)
	}
}

func (l *Listener) handle(b []byte, from *net.UDPAddr) {
	a, err := ParseAnnouncement(b)
	if err != nil {
		if l.log != nil {
			l.log.Event("discovery.payload_invalid", map[string]any{"from": from.String(), "error": err.Error()})
		}
		return
	}
	id, err := a.DeviceIDBytes()
	if err != nil || id == l.self {
		return
	}
	now := time.Now()
	dev := Device{
		DeviceID: id,
		Name:     a.Name,
		IP:       from.IP,
		TCPAddr:  net.JoinHostPort(from.IP.String(), fmt.Sprintf("%d", a.Port)),
		Iface:    a.Iface,
		LastSeen: now,
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if prev, ok := l.devices[id]; ok {
		dev.FirstSeen = prev.FirstSeen
	} else {
		dev.FirstSeen = now
	}
	l.devices[id] = dev
}

// Devices returns the currently known peers, dropping expired entries.
func (l *Listener) Devices() []Device {
	now := time.Now()
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Device, 0, len(l.devices))
	for id, d := range l.devices {
		if now.Sub(d.LastSeen) > DeviceTTL {
			delete(l.devices, id)
			continue
		}
		out = append(out, d)
	}
	return out
}

// WaitFor polls the device table until match accepts a peer or timeout expires.
func (l *Listener) WaitFor(match func(Device) bool, timeout time.Duration) (Device, error) {
	deadline := time.Now().Add(timeout)
	for {
		for _, d := range l.Devices() {
			if match == nil || match(d) {
				return d, nil
			}
		}
		if time.Now().After(deadline) {
			return Device{}, errors.New("discovery: no matching device")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

// Close releases the socket.
func (l *Listener) Close() error {
	l.mu.Lock()
	if l.closed {
		l.mu.Unlock()
		return nil
	}
	l.closed = true
	l.mu.Unlock()
	_ = l.pc.Close()
	return l.conn.Close()
}
