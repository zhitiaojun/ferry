package server

import (
	"context"
	"errors"
	"time"

	"github.com/coder/websocket"
)

var errRoomHalf = errors.New("relay: room is missing a side")

func (r *room) ready() <-chan struct{} { return r.readyCh }

func (r *room) bothConnected() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.conns[0] != nil && r.conns[1] != nil
}

// attach records one side of the relay. The second caller closes readyCh so
// the first one stops waiting and both start pumping.
func (r *room) attach(conn *websocket.Conn) (idx int, both bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	idx = -1
	for i := range r.conns {
		if r.conns[i] == nil {
			r.conns[i] = conn
			idx = i
			break
		}
	}
	if idx == -1 {
		return 0, true // room already full; caller closes its own conn
	}
	both = r.conns[0] != nil && r.conns[1] != nil
	if both {
		select {
		case <-r.readyCh:
		default:
			close(r.readyCh)
		}
	}
	conn.SetReadLimit(2 << 20)
	return idx, both
}

// startSplice launches the pump exactly once per room.
func (r *room) startSplice(ctx context.Context, idle time.Duration) {
	r.once.Do(func() {
		go r.splice(ctx, idle)
	})
}

// splice pumps binary messages between the two sockets until either side stops.
func (r *room) splice(ctx context.Context, idle time.Duration) {
	defer close(r.done)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errc := make(chan error, 2)
	for _, i := range []int{0, 1} {
		go func(i int) {
			errc <- r.pump(ctx, i, idle)
		}(i)
	}
	<-errc
	cancel()
	r.closeAll()
	a, b := r.byteCounts()
	if r.log != nil {
		r.log.Event("relay.close", map[string]any{
			"transfer": r.transfer, "bytes_a": a, "bytes_b": b,
		})
	}
}

func (r *room) pump(ctx context.Context, from int, idle time.Duration) error {
	src, dst := r.conns[from], r.conns[1-from]
	if src == nil || dst == nil {
		return errRoomHalf
	}
	for {
		rctx, cancel := context.WithTimeout(ctx, idle)
		typ, data, err := src.Read(rctx)
		cancel()
		if err != nil {
			return err
		}
		if typ != websocket.MessageBinary || len(data) == 0 {
			continue
		}
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		err = dst.Write(wctx, websocket.MessageBinary, data)
		wcancel()
		if err != nil {
			return err
		}
		r.addBytes(from, len(data))
	}
}

func (r *room) closeAll() {
	r.mu.Lock()
	conns := r.conns
	r.conns = [2]*websocket.Conn{}
	r.mu.Unlock()
	for _, c := range conns {
		if c != nil {
			_ = c.Close(websocket.StatusNormalClosure, "relay closed")
		}
	}
}
