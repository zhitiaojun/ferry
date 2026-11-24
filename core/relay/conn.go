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

const (
	maxCoalesceWriteFrame = 256 << 10
	maxCoalesceQueueBytes = 512 << 10
	closeFlushTimeout     = 2 * time.Second
)

// Conn adapts a WebSocket to net.Conn. Reads return the current binary message
// and pull the next one when it is exhausted; writes coalesce consecutive
// small records (such as 16 KiB TLS records) with zero idle hold time.
type Conn struct {
	ws *websocket.Conn

	mu           sync.Mutex
	buf          []byte
	readDeadline atomic.Int64
	readCancelMu sync.Mutex
	readCancel   context.CancelFunc

	wmu           sync.Mutex
	wcond         *sync.Cond
	wbuf          []byte
	werr          error
	wclosing      bool
	wclosed       bool
	wdone         chan struct{}
	writeDeadline atomic.Int64
	writeCancelMu sync.Mutex
	writeCancel   context.CancelFunc
}

func newConn(ws *websocket.Conn) *Conn {
	c := &Conn{
		ws:    ws,
		wdone: make(chan struct{}),
	}
	c.wcond = sync.NewCond(&c.wmu)
	go c.writeLoop()
	return c
}

// Dial connects to the relay endpoint URL (wss://…/relay?token=…).
func Dial(ctx context.Context, rawURL string) (*Conn, error) {
	ws, _, err := websocket.Dial(ctx, rawURL, &websocket.DialOptions{})
	if err != nil {
		return nil, fmt.Errorf("relay: dial: %w", err)
	}
	ws.SetReadLimit(2 << 20)
	return newConn(ws), nil
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
		} else {
			ctx, cancel = context.WithCancel(ctx)
		}
		c.readCancelMu.Lock()
		c.readCancel = cancel
		c.readCancelMu.Unlock()

		typ, data, err := c.ws.Read(ctx)

		c.readCancelMu.Lock()
		c.readCancel = nil
		c.readCancelMu.Unlock()
		cancel()

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || (errors.Is(err, context.Canceled) && c.deadlineExpired(&c.readDeadline)) {
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

// Write implements net.Conn. Writes enqueue into a small coalescing buffer and
// flush immediately when the WebSocket writer is idle.
func (c *Conn) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	c.wmu.Lock()
	defer c.wmu.Unlock()
	for len(c.wbuf) >= maxCoalesceQueueBytes && c.werr == nil && !c.wclosing && !c.wclosed {
		var timer *time.Timer
		if ns := c.writeDeadline.Load(); ns != 0 {
			rem := time.Until(time.Unix(0, ns))
			if rem <= 0 {
				return 0, os.ErrDeadlineExceeded
			}
			timer = time.AfterFunc(rem, func() {
				c.wmu.Lock()
				c.wcond.Broadcast()
				c.wmu.Unlock()
			})
		}
		c.wcond.Wait()
		if timer != nil {
			timer.Stop()
		}
	}
	if c.werr != nil {
		return 0, c.werr
	}
	if c.wclosing || c.wclosed {
		return 0, net.ErrClosed
	}
	if c.deadlineExpired(&c.writeDeadline) {
		return 0, os.ErrDeadlineExceeded
	}
	c.wbuf = append(c.wbuf, p...)
	c.wcond.Signal()
	return len(p), nil
}

func (c *Conn) writeLoop() {
	defer close(c.wdone)
	var pkt []byte
	for {
		c.wmu.Lock()
		for len(c.wbuf) == 0 && !c.wclosing && !c.wclosed && c.werr == nil {
			c.wcond.Wait()
		}
		if c.werr != nil || c.wclosed || (c.wclosing && len(c.wbuf) == 0) {
			c.wmu.Unlock()
			return
		}
		n := len(c.wbuf)
		if n > maxCoalesceWriteFrame {
			n = maxCoalesceWriteFrame
		}
		pkt = append(pkt[:0], c.wbuf[:n]...)
		if n == len(c.wbuf) {
			c.wbuf = c.wbuf[:0]
		} else {
			copy(c.wbuf, c.wbuf[n:])
			c.wbuf = c.wbuf[:len(c.wbuf)-n]
		}
		closing := c.wclosing
		c.wcond.Broadcast()
		c.wmu.Unlock()

		ctx := context.Background()
		var cancel context.CancelFunc
		if ns := c.writeDeadline.Load(); ns != 0 {
			ctx, cancel = context.WithDeadline(ctx, time.Unix(0, ns))
		} else if closing {
			ctx, cancel = context.WithTimeout(ctx, closeFlushTimeout)
		} else {
			ctx, cancel = context.WithCancel(ctx)
		}
		c.writeCancelMu.Lock()
		c.writeCancel = cancel
		c.writeCancelMu.Unlock()

		err := c.ws.Write(ctx, websocket.MessageBinary, pkt)

		c.writeCancelMu.Lock()
		c.writeCancel = nil
		c.writeCancelMu.Unlock()
		cancel()

		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) || (errors.Is(err, context.Canceled) && c.deadlineExpired(&c.writeDeadline)) {
				err = os.ErrDeadlineExceeded
			}
			c.wmu.Lock()
			if c.werr == nil {
				c.werr = err
			}
			c.wcond.Broadcast()
			c.wmu.Unlock()
			return
		}
	}
}

func (c *Conn) deadlineExpired(d *atomic.Int64) bool {
	ns := d.Load()
	return ns != 0 && time.Now().UnixNano() >= ns
}

// Close implements net.Conn, flushing buffered writes before closing the socket.
func (c *Conn) Close() error {
	c.wmu.Lock()
	alreadyClosed := c.wclosed
	if !c.wclosing && !c.wclosed {
		c.wclosing = true
		c.wcond.Broadcast()
	}
	c.wmu.Unlock()

	if !alreadyClosed {
		<-c.wdone
		c.wmu.Lock()
		c.wclosed = true
		c.wcond.Broadcast()
		c.wmu.Unlock()
	}

	c.readCancelMu.Lock()
	rc := c.readCancel
	c.readCancelMu.Unlock()
	if rc != nil {
		rc()
	}

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
	if !t.IsZero() && !t.After(time.Now()) {
		c.readCancelMu.Lock()
		rc := c.readCancel
		c.readCancelMu.Unlock()
		if rc != nil {
			rc()
		}
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
	if !t.IsZero() && !t.After(time.Now()) {
		c.writeCancelMu.Lock()
		wc := c.writeCancel
		c.writeCancelMu.Unlock()
		if wc != nil {
			wc()
		}
	}
	c.wmu.Lock()
	c.wcond.Broadcast()
	c.wmu.Unlock()
	return nil
}

type relayAddr string

func (a relayAddr) Network() string { return "relay" }
func (a relayAddr) String() string  { return string(a) }

// IsRelayURL reports whether a URL looks like this package's transport.
func IsRelayURL(raw string) bool {
	return strings.HasPrefix(raw, "wss://") || strings.HasPrefix(raw, "ws://")
}
