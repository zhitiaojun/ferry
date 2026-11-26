package server

import (
	"context"
	"net"
	"net/netip"
	"time"

	"github.com/zhitiaojun/ferry/core/logging"
	"github.com/zhitiaojun/ferry/core/punch"
)

// STUN binds one or more UDP sockets and answers STUN binding requests. It is
// intentionally independent from the signaling /relay HTTP server: STUN must
// be reachable directly by IP:port (UDP), not through ESA (design §4).
type STUN struct {
	conns []*net.UDPConn
	log   *logging.Logger
}

// ListenSTUN binds the given "host:port" UDP addresses (e.g. 0.0.0.0:40000).
func ListenSTUN(addrs []string, log *logging.Logger) (*STUN, error) {
	s := &STUN{log: log}
	for _, a := range addrs {
		ua, err := net.ResolveUDPAddr("udp", a)
		if err != nil {
			s.Close()
			return nil, err
		}
		c, err := net.ListenUDP("udp", ua)
		if err != nil {
			s.Close()
			return nil, err
		}
		s.conns = append(s.conns, c)
	}
	return s, nil
}

// Addrs returns the bound addresses.
func (s *STUN) Addrs() []string {
	out := make([]string, 0, len(s.conns))
	for _, c := range s.conns {
		out = append(out, c.LocalAddr().String())
	}
	return out
}

// Serve answers requests until ctx is done or the sockets are closed.
func (s *STUN) Serve(ctx context.Context) error {
	errs := make(chan error, len(s.conns))
	for _, c := range s.conns {
		go func(c *net.UDPConn) {
			errs <- s.serveConn(ctx, c)
		}(c)
	}
	for range s.conns {
		if err := <-errs; err != nil && ctx.Err() == nil {
			return err
		}
	}
	return nil
}

func (s *STUN) serveConn(ctx context.Context, c *net.UDPConn) error {
	buf := make([]byte, 1500)
	for {
		if ctx.Err() != nil {
			return nil
		}
		_ = c.SetReadDeadline(time.Now().Add(time.Second))
		n, from, err := c.ReadFromUDPAddrPort(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				continue
			}
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		resp, ok := punch.HandleRequest(buf[:n], netip.AddrPortFrom(from.Addr().Unmap(), from.Port()))
		if !ok {
			continue
		}
		if _, err := c.WriteToUDPAddrPort(resp, from); err != nil {
			if s.log != nil {
				s.log.Event("stun.write_error", map[string]any{"from": from.String(), "error": err.Error()})
			}
		}
	}
}

// Close closes all sockets.
func (s *STUN) Close() error {
	var first error
	for _, c := range s.conns {
		if err := c.Close(); err != nil && first == nil {
			first = err
		}
	}
	s.conns = nil
	return first
}
