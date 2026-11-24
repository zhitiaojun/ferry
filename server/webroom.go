package server

import (
	"context"
	"errors"
	"sync"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/logging"
)

var errRoomClosed = errors.New("room closed")

// webRoom pairs one browser (guest) with one device (host) for a short code.
// Messages are relayed verbatim: the server never sees the file content
// because the page encrypts it with a key it keeps in the URL fragment.
type webRoom struct {
	room    string
	log     *logging.Logger
	mu      sync.Mutex
	guest   *websocket.Conn
	host    *websocket.Conn
	ready   chan struct{}
	closed  bool
	once    sync.Once
	started sync.Once
}

func (r *webRoom) attach(ctx context.Context, role string, c *websocket.Conn) error {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return errRoomClosed
	}
	if role == "guest" {
		if r.guest != nil {
			_ = r.guest.Close(websocket.StatusPolicyViolation, "another guest is connected")
		}
		r.guest = c
	} else {
		if r.host != nil {
			_ = r.host.Close(websocket.StatusPolicyViolation, "another host is connected")
		}
		r.host = c
	}
	ready := r.guest != nil && r.host != nil
	r.mu.Unlock()

	if ready {
		r.once.Do(func() {
			if r.ready != nil {
				close(r.ready)
			}
		})
	}
	return nil
}

// serve waits for both sides, then starts the relay pumps exactly once: two
// HTTP handlers reach here (host and guest) and duplicate pumps would read the
// same socket concurrently.
func (r *webRoom) serve(ctx context.Context) {
	if r.ready != nil {
		select {
		case <-r.ready:
		case <-ctx.Done():
			return
		}
	}
	r.mu.Lock()
	g, h := r.guest, r.host
	r.mu.Unlock()
	if g == nil || h == nil {
		return
	}
	r.started.Do(func() {
		go r.pump(g, h)
		go r.pump(h, g)
	})
}

type webFrame struct {
	typ  websocket.MessageType
	data []byte
}

// pump copies one direction with read/write pipelining until it fails, then tears the room down.
func (r *webRoom) pump(dst, src *websocket.Conn) {
	ch := make(chan webFrame, 8)
	done := make(chan struct{})
	go func() {
		defer close(ch)
		for {
			typ, data, err := src.Read(context.Background())
			if err != nil {
				return
			}
			select {
			case ch <- webFrame{typ: typ, data: data}:
			case <-done:
				return
			}
		}
	}()
	for f := range ch {
		if err := dst.Write(context.Background(), f.typ, f.data); err != nil {
			break
		}
	}
	close(done)
	r.closeAll()
}

// isClosed reports whether one of the pumps already tore this room down. A
// closed room must never be reused: its ready channel is spent and both
// sockets are gone, so the next attach has to create a fresh room instead.
func (r *webRoom) isClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

func (r *webRoom) closeAll() {
	r.mu.Lock()
	r.closed = true
	g, h := r.guest, r.host
	r.mu.Unlock()
	if g != nil {
		_ = g.Close(websocket.StatusNormalClosure, "bye")
	}
	if h != nil {
		_ = h.Close(websocket.StatusNormalClosure, "bye")
	}
}
