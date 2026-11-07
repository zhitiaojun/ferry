package punch

import (
	"context"
	"fmt"
	"net"
	"net/netip"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/logging"
)

// Candidate is one way a peer might be reachable. The kinds match the
// signaling wire format (docs/spec/protocol.md §8.4).
type Candidate struct {
	Kind     string
	Addr     string
	Priority int
}

// Gathered is the outcome of local candidate collection.
type Gathered struct {
	Candidates []Candidate
	// Conn is the IPv4 UDP socket whose port the candidates advertise. Hole
	// punching and the STUN traffic share it, so the NAT mapping stays the same.
	Conn *net.UDPConn
	// Mapping is the NAT behavior inferred from the STUN servers, if reachable.
	Mapping Behavior
}

// Gather collects LAN and STUN candidates for this host. STUN failures are
// logged but do not fail the collection: LAN candidates still work.
func Gather(ctx context.Context, stunServers []string, timeout time.Duration, log *logging.Logger) (*Gathered, error) {
	if timeout <= 0 {
		timeout = time.Second
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("punch: open udp socket: %w", err)
	}
	localPort := conn.LocalAddr().(*net.UDPAddr).Port

	out := &Gathered{Conn: conn, Mapping: BehaviorUnknown}

	ifaces, err := discovery.Eligible()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("punch: list interfaces: %w", err)
	}
	for _, ifc := range ifaces {
		out.Candidates = append(out.Candidates, Candidate{
			Kind:     "lan",
			Addr:     net.JoinHostPort(ifc.IP.String(), fmt.Sprint(localPort)),
			Priority: 50,
		})
	}

	client := NewClient(conn)
	mappings := make([]netip.AddrPort, 0, len(stunServers))
	for _, server := range stunServers {
		mapped, _, err := client.Query(ctx, server, timeout)
		if err != nil {
			if log != nil {
				log.Event("punch.stun_error", map[string]any{"server": server, "error": err.Error()})
			}
			continue
		}
		mappings = append(mappings, mapped)
		out.Candidates = append(out.Candidates, Candidate{
			Kind:     "stun",
			Addr:     mapped.String(),
			Priority: 80,
		})
	}
	// STUN queries left a read deadline in the past; the QUIC listener that
	// later reads this socket would fail every read. Clear it before returning.
	_ = conn.SetReadDeadline(time.Time{})

	if len(mappings) >= 2 {
		out.Mapping = BehaviorCone
		for _, m := range mappings[1:] {
			if m.Addr() != mappings[0].Addr() || m.Port() != mappings[0].Port() {
				out.Mapping = BehaviorSymmetric
				break
			}
		}
	}
	if log != nil {
		kinds := make([]string, 0, len(out.Candidates))
		for _, c := range out.Candidates {
			kinds = append(kinds, c.Kind+"="+c.Addr)
		}
		log.Event("punch.gathered", map[string]any{
			"candidates": kinds,
			"mapping":    string(out.Mapping),
		})
	}
	return out, nil
}

// Close releases the UDP socket.
func (g *Gathered) Close() error {
	if g == nil || g.Conn == nil {
		return nil
	}
	return g.Conn.Close()
}

// Punch sends small keep-alive datagrams to every candidate of the peer so the
// local NAT opens mappings toward each one. It returns once the context ends.
func (g *Gathered) Punch(ctx context.Context, peer []Candidate) {
	const payload = "FT-PUNCH-1"
	ticker := time.NewTicker(50 * time.Millisecond)
	defer ticker.Stop()
	for {
		for _, c := range peer {
			addr, err := net.ResolveUDPAddr("udp4", c.Addr)
			if err != nil {
				continue
			}
			_, _ = g.Conn.WriteToUDP([]byte(payload), addr)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// Resolve turns signaling candidates into UDP addresses, dropping the ones that
// do not parse (they may be IPv6 or placeholder entries).
func Resolve(candidates []Candidate) []*net.UDPAddr {
	var out []*net.UDPAddr
	for _, c := range candidates {
		if c.Kind == "ipv6" {
			continue
		}
		addr, err := net.ResolveUDPAddr("udp4", c.Addr)
		if err != nil || addr.IP == nil {
			continue
		}
		out = append(out, addr)
	}
	return out
}

// Primary picks the highest-priority candidate address to dial.
func Primary(candidates []Candidate) (*net.UDPAddr, error) {
	var best *Candidate
	for i := range candidates {
		c := &candidates[i]
		if c.Kind == "ipv6" {
			continue
		}
		if _, err := net.ResolveUDPAddr("udp4", c.Addr); err != nil {
			continue
		}
		if best == nil || c.Priority > best.Priority {
			best = c
		}
	}
	if best == nil {
		return nil, fmt.Errorf("punch: no routable candidate")
	}
	return net.ResolveUDPAddr("udp4", best.Addr)
}
