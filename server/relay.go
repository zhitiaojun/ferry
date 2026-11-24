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

// throttle keeps one room under RelayRate bytes per second by sleeping once
// the budget for the current window is spent. High rates (>= 1 MiB/s) use
// 100 ms sub-windows to avoid burst-stall pauses on TCP. A zero rate disables it.
func (r *room) throttle(ctx context.Context, n int) {
	if r.rate <= 0 {
		return
	}
	win := time.Second
	limit := r.rate
	if r.rate >= 1<<20 {
		win = 100 * time.Millisecond
		limit = r.rate / 10
	}
	r.rateMu.Lock()
	now := time.Now()
	if r.rateWindow.IsZero() || now.Sub(r.rateWindow) >= win {
		r.rateWindow = now
		r.rateBytes = 0
	}
	r.rateBytes += int64(n)
	over := r.rateBytes > limit
	wait := time.Until(r.rateWindow.Add(win))
	r.rateMu.Unlock()
	if !over || wait <= 0 {
		return
	}
	select {
	case <-time.After(wait):
	case <-ctx.Done():
	}
}

func (r *room) pump(ctx context.Context, from int, idle time.Duration) error {
	src, dst := r.conns[from], r.conns[1-from]
	if src == nil || dst == nil {
		return errRoomHalf
	}
	ch := make(chan []byte, 8)
	rerr := make(chan error, 1)
	go func() {
		defer close(ch)
		for {
			rctx, cancel := context.WithTimeout(ctx, idle)
			typ, data, err := src.Read(rctx)
			cancel()
			if err != nil {
				rerr <- err
				return
			}
			if typ != websocket.MessageBinary || len(data) == 0 {
				continue
			}
			select {
			case ch <- data:
			case <-ctx.Done():
				rerr <- ctx.Err()
				return
			}
		}
	}()

	for data := range ch {
		wctx, wcancel := context.WithTimeout(ctx, 10*time.Second)
		err := dst.Write(wctx, websocket.MessageBinary, data)
		wcancel()
		if err != nil {
			return err
		}
		r.addBytes(from, len(data))
		r.throttle(ctx, len(data))
	}
	select {
	case err := <-rerr:
		return err
	default:
		return nil
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
