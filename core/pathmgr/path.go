// Package pathmgr defines the transport-path abstraction: every way of
// reaching a peer (LAN, UDP hole punching, relay) is a reliable ordered byte
// stream with Dial / Accept / Close. M1 implements plain TCP only.
package pathmgr

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"
)

// Path is one way to reach a peer.
type Path interface {
	// Name identifies the path in logs ("lan", "punch", "relay").
	Name() string
	// Dial opens an outbound connection to the peer.
	Dial(ctx context.Context) (net.Conn, error)
	// Accept waits for an inbound connection.
	Accept(ctx context.Context) (net.Conn, error)
	// Close stops listening and releases resources.
	Close() error
}

// TCP is the M1 path: a plain TCP stream, local or LAN.
type TCP struct {
	addr string
	ln   net.Listener

	mu     sync.Mutex
	closed bool
}

// NewTCPDial returns a path that dials addr.
func NewTCPDial(addr string) *TCP { return &TCP{addr: addr} }

// NewTCPListener starts listening on addr ("127.0.0.1:0" picks a port).
func NewTCPListener(addr string) (*TCP, error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	return &TCP{addr: ln.Addr().String(), ln: ln}, nil
}

// Name implements Path.
func (p *TCP) Name() string { return "lan" }

// Addr returns the listen address (empty for a dial-only path).
func (p *TCP) Addr() string { return p.addr }

// Dial implements Path.
func (p *TCP) Dial(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	nc, err := d.DialContext(ctx, "tcp", p.addr)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", p.addr, err)
	}
	return nc, nil
}

// Accept implements Path.
func (p *TCP) Accept(ctx context.Context) (net.Conn, error) {
	p.mu.Lock()
	ln := p.ln
	p.mu.Unlock()
	if ln == nil {
		return nil, errors.New("path: accept on a dial-only path")
	}
	var deadline time.Time
	if d, ok := ctx.Deadline(); ok {
		deadline = d
	}
	if dl, ok := ln.(interface{ SetDeadline(time.Time) error }); ok {
		if err := dl.SetDeadline(deadline); err != nil {
			return nil, err
		}
	}
	nc, err := ln.Accept()
	if err != nil {
		return nil, fmt.Errorf("accept: %w", err)
	}
	return nc, nil
}

// Close implements Path.
func (p *TCP) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	p.closed = true
	if p.ln != nil {
		return p.ln.Close()
	}
	return nil
}
