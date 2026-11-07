package pathmgr

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/quic-go/quic-go"
)

// ALPNProto is the application protocol name used on the punched QUIC tunnel.
const ALPNProto = "ft-punch/1"

// QUIC is a UDP hole-punching path: both sides share one UDP socket between
// STUN, punching and the QUIC handshake, so the NAT mapping the candidates
// advertise is the one the tunnel uses.
type QUIC struct {
	udp *net.UDPConn
	tr  *quic.Transport
	tls *tls.Config

	mu     sync.Mutex
	ln     *quic.Listener
	remote string
	closed bool
}

// NewQUIC returns a path on the given UDP socket. tlsConfig must present this
// device's certificate and pin the peer's (identity.ClientTLS/ServerTLS).
func NewQUIC(udp *net.UDPConn, tlsConfig *tls.Config) *QUIC {
	cfg := tlsConfig.Clone()
	cfg.NextProtos = []string{ALPNProto}
	return &QUIC{
		udp: udp,
		tr:  &quic.Transport{Conn: udp},
		tls: cfg,
	}
}

// PrepareListen starts a QUIC listener on the shared socket.
func (q *QUIC) PrepareListen(ctx context.Context) error {
	ln, err := q.tr.Listen(q.tls, nil)
	if err != nil {
		return fmt.Errorf("punch: quic listen: %w", err)
	}
	q.mu.Lock()
	q.ln = ln
	q.mu.Unlock()
	return nil
}

// SetPeer sets the address Dial connects to.
func (q *QUIC) SetPeer(addr string) {
	q.mu.Lock()
	q.remote = addr
	q.mu.Unlock()
}

// Dial implements Path: it opens a QUIC connection to the peer address and
// returns its first bidirectional stream as a net.Conn.
func (q *QUIC) Dial(ctx context.Context) (net.Conn, error) {
	q.mu.Lock()
	remote := q.remote
	q.mu.Unlock()
	if remote == "" {
		return nil, fmt.Errorf("punch: dial without peer address")
	}
	return q.DialAddr(ctx, remote)
}

// DialAddr connects to one specific candidate address.
func (q *QUIC) DialAddr(ctx context.Context, remote string) (net.Conn, error) {
	addr, err := net.ResolveUDPAddr("udp4", remote)
	if err != nil {
		return nil, fmt.Errorf("punch: resolve %s: %w", remote, err)
	}
	conn, err := q.tr.Dial(ctx, addr, q.tls, nil)
	if err != nil {
		return nil, fmt.Errorf("punch: quic dial %s: %w", remote, err)
	}
	stream, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(0, "no stream")
		return nil, fmt.Errorf("punch: open stream: %w", err)
	}
	return &streamConn{stream: stream, conn: conn, laddr: q.udp.LocalAddr(), raddr: addr}, nil
}

// Accept implements Path: it waits for a peer QUIC connection and returns its
// first accepted stream as a net.Conn.
func (q *QUIC) Accept(ctx context.Context) (net.Conn, error) {
	q.mu.Lock()
	ln := q.ln
	q.mu.Unlock()
	if ln == nil {
		return nil, fmt.Errorf("punch: accept before listen")
	}
	conn, err := ln.Accept(ctx)
	if err != nil {
		return nil, fmt.Errorf("punch: quic accept: %w", err)
	}
	stream, err := conn.AcceptStream(ctx)
	if err != nil {
		conn.CloseWithError(0, "no stream")
		return nil, fmt.Errorf("punch: accept stream: %w", err)
	}
	return &streamConn{stream: stream, conn: conn, laddr: q.udp.LocalAddr(), raddr: conn.RemoteAddr()}, nil
}

// Name implements Path.
func (q *QUIC) Name() string { return "punch" }

// Close implements Path.
func (q *QUIC) Close() error {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return nil
	}
	q.closed = true
	if q.ln != nil {
		_ = q.ln.Close()
	}
	return nil
}

// streamConn adapts a QUIC stream to net.Conn.
type streamConn struct {
	stream *quic.Stream
	conn   *quic.Conn
	laddr  net.Addr
	raddr  net.Addr
}

func (c *streamConn) Read(p []byte) (int, error)  { return c.stream.Read(p) }
func (c *streamConn) Write(p []byte) (int, error) { return c.stream.Write(p) }

func (c *streamConn) Close() error {
	_ = c.stream.Close()
	return c.conn.CloseWithError(0, "done")
}

func (c *streamConn) LocalAddr() net.Addr  { return c.laddr }
func (c *streamConn) RemoteAddr() net.Addr { return c.raddr }

func (c *streamConn) SetDeadline(t time.Time) error {
	if err := c.stream.SetReadDeadline(t); err != nil {
		return err
	}
	return c.stream.SetWriteDeadline(t)
}

func (c *streamConn) SetReadDeadline(t time.Time) error  { return c.stream.SetReadDeadline(t) }
func (c *streamConn) SetWriteDeadline(t time.Time) error { return c.stream.SetWriteDeadline(t) }
