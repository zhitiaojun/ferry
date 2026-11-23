// Package relay carries an ordered byte stream over the ft-server relay
// WebSocket (protocol §8.5). The server splices two connections; each side sees
// a message-oriented socket that this package turns into a net.Conn so TLS can
// run inside it.
package relay

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
)

// Conn adapts a WebSocket to net.Conn. Reads return the current binary message
// and pull the next one when it is exhausted; writes become one binary message
// each.
type Conn struct {
	ws *websocket.Conn

	mu            sync.Mutex
	buf           []byte
	readDeadline  atomic.Int64
	writeDeadline atomic.Int64
}

// Dial connects to the relay endpoint URL (wss://…/relay?token=…).
func Dial(ctx context.Context, rawURL string) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("relay: dial: %w", err)
	}
	ws.SetReadLimit(2 << 20)
	return &Conn{ws: ws}, nil
}

// RelayURL builds the relay URL from the signaling URL (used to resolve the
// relative "/relay" path the server returns).
func RelayURL(signalURL, relayPath, token string) (string, error) {
	u, err := url.Parse(signalURL)
	if err != nil {
		return "", fmt.Errorf("relay: parse signaling url: %w", err)
	}
	u.Path = relayPath
	u.RawQuery = "token=" + url.QueryEscape(token)
	u.Fragment = ""
	if u.Scheme == "https" {
		u.Scheme = "wss"
	}
	if u.Scheme == "http" {
		u.Scheme = "ws"
	}
	return u.String(), nil
}

// Read implements net.Conn.
func (c *Conn) Read(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for len(c.buf) == 0 {
		ctx := context.Background()
		var cancel context.CancelFunc
		if ns := c.readDeadline.Load(); ns != 0 {
			ctx, cancel = context.WithDeadline(ctx, time.Unix(0, ns))
		}
		typ, data, err := c.ws.Read(ctx)
		if cancel != nil {
			cancel()
		}
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, os.ErrDeadlineExceeded
			}
			return 0, err
		}
		if typ != websocket.MessageBinary {
			continue
		}
		c.buf = data
	}
	n := copy(p, c.buf)
	c.buf = c.buf[n:]
	return n, nil
}

// Write implements net.Conn: one binary message per call.
func (c *Conn) Write(p []byte) (int, error) {
	ctx := context.Background()
	var cancel context.CancelFunc
	if ns := c.writeDeadline.Load(); ns != 0 {
		ctx, cancel = context.WithDeadline(ctx, time.Unix(0, ns))
		defer cancel()
	}
	if err := c.ws.Write(ctx, websocket.MessageBinary, p); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, os.ErrDeadlineExceeded
		}
		return 0, err
	}
	return len(p), nil
}

// Close implements net.Conn.
func (c *Conn) Close() error {
	err := c.ws.Close(websocket.StatusNormalClosure, "bye")
	if errors.Is(err, net.ErrClosed) {
		return nil
	}
	return err
}

// LocalAddr implements net.Conn.
func (c *Conn) LocalAddr() net.Addr { return relayAddr("local") }

// RemoteAddr implements net.Conn.
func (c *Conn) RemoteAddr() net.Addr { return relayAddr("remote") }

// SetDeadline implements net.Conn.
func (c *Conn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t)
	return c.SetWriteDeadline(t)
}

// SetReadDeadline implements net.Conn.
func (c *Conn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		c.readDeadline.Store(0)
	} else {
		c.readDeadline.Store(t.UnixNano())
	}
	return nil
}

// SetWriteDeadline implements net.Conn.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.writeDeadline.Store(0)
	} else {
		c.writeDeadline.Store(t.UnixNano())
	}
	return nil
}

type relayAddr string

func (a relayAddr) Network() string { return "relay" }
func (a relayAddr) String() string  { return string(a) }

// IsRelayURL reports whether a URL looks like this package's transport.
func IsRelayURL(raw string) bool {
	return strings.HasPrefix(raw, "wss://") || strings.HasPrefix(raw, "ws://")
}
