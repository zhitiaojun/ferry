// Package punch implements STUN-assisted candidate discovery, NAT mapping
// detection and UDP hole punching.
package punch

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"time"
)

// STUN message types and constants (RFC 5389 subset).
const (
	TypeBindingRequest = 0x0001
	TypeBindingSuccess = 0x0101
	TypeBindingError   = 0x0111

	MagicCookie = 0x2112A442

	attrMappedAddress = 0x0001
	attrXORMappedAddr = 0x0020
	attrSoftware      = 0x8022
	attrErrorCode     = 0x0009

	headerSize = 20
)

// ErrNotSTUN is returned when a datagram is not a STUN message.
var ErrNotSTUN = errors.New("punch: not a STUN message")

// Message is the subset of STUN we need: binding requests and responses with
// XOR-MAPPED-ADDRESS / MAPPED-ADDRESS and SOFTWARE.
type Message struct {
	Type     uint16
	Txn      [12]byte
	Software string
	XORAddr  netip.AddrPort
	HasXOR   bool
	MapAddr  netip.AddrPort
	HasMap   bool
	ErrCode  int
}

// NewBindingRequest builds a binding request with a random transaction ID.
func NewBindingRequest() (*Message, error) {
	m := &Message{Type: TypeBindingRequest}
	if _, err := rand.Read(m.Txn[:]); err != nil {
		return nil, err
	}
	return m, nil
}

// Marshal encodes the message.
func (m *Message) Marshal() ([]byte, error) {
	var attrs []byte
	if m.HasXOR {
		b, err := encodeAddr(attrXORMappedAddr, m.XORAddr, true, m.Txn)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, b...)
	}
	if m.HasMap {
		b, err := encodeAddr(attrMappedAddress, m.MapAddr, false, m.Txn)
		if err != nil {
			return nil, err
		}
		attrs = append(attrs, b...)
	}
	if m.Software != "" {
		attrs = append(attrs, encodeAttr(attrSoftware, []byte(m.Software))...)
	}
	if m.ErrCode != 0 {
		v := make([]byte, 4)
		v[2] = byte(m.ErrCode / 100)
		v[3] = byte(m.ErrCode % 100)
		attrs = append(attrs, encodeAttr(attrErrorCode, v)...)
	}
	buf := make([]byte, headerSize+len(attrs))
	binary.BigEndian.PutUint16(buf[0:], m.Type)
	binary.BigEndian.PutUint16(buf[2:], uint16(len(attrs)))
	binary.BigEndian.PutUint32(buf[4:], MagicCookie)
	copy(buf[8:20], m.Txn[:])
	copy(buf[headerSize:], attrs)
	return buf, nil
}

// ParseMessage decodes a STUN message.
func ParseMessage(b []byte) (*Message, error) {
	if len(b) < headerSize {
		return nil, ErrNotSTUN
	}
	if binary.BigEndian.Uint32(b[4:8]) != MagicCookie {
		return nil, ErrNotSTUN
	}
	m := &Message{Type: binary.BigEndian.Uint16(b[0:2])}
	copy(m.Txn[:], b[8:20])
	n := int(binary.BigEndian.Uint16(b[2:4]))
	if headerSize+n > len(b) {
		return nil, fmt.Errorf("punch: STUN length %d exceeds datagram %d", n, len(b))
	}
	attrs := b[headerSize : headerSize+n]
	for len(attrs) >= 4 {
		typ := binary.BigEndian.Uint16(attrs[0:2])
		l := int(binary.BigEndian.Uint16(attrs[2:4]))
		if 4+l > len(attrs) {
			return nil, fmt.Errorf("punch: truncated STUN attribute 0x%04x", typ)
		}
		val := attrs[4 : 4+l]
		switch typ {
		case attrXORMappedAddr:
			a, err := decodeAddr(val, true, m.Txn)
			if err != nil {
				return nil, err
			}
			m.XORAddr, m.HasXOR = a, true
		case attrMappedAddress:
			a, err := decodeAddr(val, false, m.Txn)
			if err != nil {
				return nil, err
			}
			m.MapAddr, m.HasMap = a, true
		case attrSoftware:
			m.Software = string(val)
		case attrErrorCode:
			if len(val) >= 4 {
				m.ErrCode = int(val[2])*100 + int(val[3])
			}
		}
		adv := 4 + ((l + 3) &^ 3)
		if adv > len(attrs) {
			break
		}
		attrs = attrs[adv:]
	}
	return m, nil
}

func encodeAttr(typ uint16, val []byte) []byte {
	pad := (4 - len(val)%4) % 4
	out := make([]byte, 4+len(val)+pad)
	binary.BigEndian.PutUint16(out[0:], typ)
	binary.BigEndian.PutUint16(out[2:], uint16(len(val)))
	copy(out[4:], val)
	return out
}

func encodeAddr(typ uint16, ap netip.AddrPort, xor bool, txn [12]byte) ([]byte, error) {
	addr := ap.Addr().Unmap()
	var val []byte
	if addr.Is4() {
		a4 := addr.As4()
		port := ap.Port()
		if xor {
			port ^= uint16(MagicCookie >> 16)
			c := make([]byte, 4)
			binary.BigEndian.PutUint32(c, MagicCookie)
			for i := 0; i < 4; i++ {
				a4[i] ^= c[i]
			}
		}
		val = make([]byte, 8)
		val[1] = 0x01
		binary.BigEndian.PutUint16(val[2:], port)
		copy(val[4:], a4[:])
	} else if addr.Is6() {
		a16 := addr.As16()
		port := ap.Port()
		if xor {
			port ^= uint16(MagicCookie >> 16)
			cookie := uint32(MagicCookie)
			for i := 0; i < 4; i++ {
				a16[i] ^= byte(cookie >> (24 - 8*i))
			}
			for i := 0; i < 12; i++ {
				a16[4+i] ^= txn[i]
			}
		}
		val = make([]byte, 20)
		val[1] = 0x02
		binary.BigEndian.PutUint16(val[2:], port)
		copy(val[4:], a16[:])
	} else {
		return nil, fmt.Errorf("punch: unsupported address %v", ap)
	}
	return encodeAttr(typ, val), nil
}

func decodeAddr(val []byte, xor bool, txn [12]byte) (netip.AddrPort, error) {
	if len(val) < 8 {
		return netip.AddrPort{}, fmt.Errorf("punch: short address attribute")
	}
	port := binary.BigEndian.Uint16(val[2:])
	if xor {
		port ^= uint16(MagicCookie >> 16)
	}
	var addr netip.Addr
	switch val[1] {
	case 0x01:
		a4 := [4]byte{}
		copy(a4[:], val[4:8])
		if xor {
			c := make([]byte, 4)
			binary.BigEndian.PutUint32(c, MagicCookie)
			for i := 0; i < 4; i++ {
				a4[i] ^= c[i]
			}
		}
		addr = netip.AddrFrom4(a4)
	case 0x02:
		if len(val) < 20 {
			return netip.AddrPort{}, fmt.Errorf("punch: short IPv6 address attribute")
		}
		a16 := [16]byte{}
		copy(a16[:], val[4:20])
		if xor {
			cookie := uint32(MagicCookie)
			for i := 0; i < 4; i++ {
				a16[i] ^= byte(cookie >> (24 - 8*i))
			}
			for i := 0; i < 12; i++ {
				a16[4+i] ^= txn[i]
			}
		}
		addr = netip.AddrFrom16(a16)
	default:
		return netip.AddrPort{}, fmt.Errorf("punch: unknown address family %d", val[1])
	}
	return netip.AddrPortFrom(addr, port), nil
}

// Client sends STUN binding requests from one UDP socket. The same socket is
// later reused for hole punching / QUIC (per design §4).
type Client struct {
	conn *net.UDPConn
}

// NewClient wraps an existing UDP socket.
func NewClient(conn *net.UDPConn) *Client { return &Client{conn: conn} }

// ListenClient binds a fresh dual-stack-capable UDP socket on port 0.
func ListenClient(ipv6 bool) (*Client, error) {
	network := "udp4"
	if ipv6 {
		network = "udp6"
	}
	c, err := net.ListenUDP(network, &net.UDPAddr{})
	if err != nil {
		return nil, err
	}
	return &Client{conn: c}, nil
}

// Conn returns the underlying socket.
func (c *Client) Conn() *net.UDPConn { return c.conn }

// LocalAddr returns the bound local address.
func (c *Client) LocalAddr() netip.AddrPort {
	if c == nil || c.conn == nil {
		return netip.AddrPort{}
	}
	if ua, ok := c.conn.LocalAddr().(*net.UDPAddr); ok {
		return ua.AddrPort()
	}
	return netip.AddrPort{}
}

// Close closes the socket.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// Query sends a binding request to server and returns the mapped address as
// seen by that server.
func (c *Client) Query(ctx context.Context, server string, timeout time.Duration) (netip.AddrPort, time.Duration, error) {
	ua, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return netip.AddrPort{}, 0, err
	}
	req, err := NewBindingRequest()
	if err != nil {
		return netip.AddrPort{}, 0, err
	}
	raw, err := req.Marshal()
	if err != nil {
		return netip.AddrPort{}, 0, err
	}
	if timeout <= 0 {
		timeout = 800 * time.Millisecond
	}
	start := time.Now()
	for attempt := 0; attempt < 3; attempt++ {
		if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
			return netip.AddrPort{}, 0, err
		}
		if _, err := c.conn.WriteToUDP(raw, ua); err != nil {
			return netip.AddrPort{}, 0, err
		}
		buf := make([]byte, 1500)
		for {
			n, from, err := c.conn.ReadFromUDP(buf)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					break // retransmit
				}
				return netip.AddrPort{}, 0, err
			}
			if !from.IP.Equal(ua.IP) || from.Port != ua.Port {
				continue // stray packet (e.g. a punch datagram)
			}
			m, err := ParseMessage(buf[:n])
			if err != nil || m.Type != TypeBindingSuccess || m.Txn != req.Txn {
				continue
			}
			ap := m.XORAddr
			if !m.HasXOR {
				ap = m.MapAddr
			}
			if !ap.IsValid() {
				return netip.AddrPort{}, 0, fmt.Errorf("punch: STUN response without mapped address")
			}
			return ap, time.Since(start), nil
		}
	}
	return netip.AddrPort{}, 0, fmt.Errorf("punch: no STUN response from %s", server)
}

// Behavior classifies NAT mapping behavior using two STUN servers (different
// ports on the same host, per design §4).
type Behavior string

const (
	BehaviorCone      Behavior = "cone"      // same mapped address on all servers
	BehaviorSymmetric Behavior = "symmetric" // mapped port changes per destination
	BehaviorUnknown   Behavior = "unknown"
)

// DetectMapping queries every server and classifies the mapping behavior.
func (c *Client) DetectMapping(ctx context.Context, servers []string, timeout time.Duration) (Behavior, []netip.AddrPort, error) {
	addrs := make([]netip.AddrPort, 0, len(servers))
	var first netip.AddrPort
	for i, s := range servers {
		ap, _, err := c.Query(ctx, s, timeout)
		if err != nil {
			return BehaviorUnknown, addrs, err
		}
		addrs = append(addrs, ap)
		if i == 0 {
			first = ap
			continue
		}
		if ap.Addr() != first.Addr() || ap.Port() != first.Port() {
			return BehaviorSymmetric, addrs, nil
		}
	}
	if len(addrs) < 2 {
		return BehaviorUnknown, addrs, nil
	}
	return BehaviorCone, addrs, nil
}

// HandleRequest answers a binding request datagram. It returns the response
// bytes and true when the datagram was a binding request.
func HandleRequest(b []byte, from netip.AddrPort) ([]byte, bool) {
	m, err := ParseMessage(b)
	if err != nil || m.Type != TypeBindingRequest {
		return nil, false
	}
	resp := &Message{
		Type:     TypeBindingSuccess,
		Txn:      m.Txn,
		Software: "ft-server",
		XORAddr:  from,
		HasXOR:   true,
	}
	out, err := resp.Marshal()
	if err != nil {
		return nil, false
	}
	return out, true
}
