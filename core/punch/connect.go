package punch

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/session"
)

// Debugf receives punch diagnostics when a caller installs it.
var Debugf func(format string, args ...any)

func debugf(format string, args ...any) {
	if Debugf != nil {
		Debugf(format, args...)
	}
}

// Connect establishes the session over the punched QUIC tunnel.
//
// Both sides listen and dial, but not at the same time: the device with the
// smaller id dials first while the other only accepts. Doing it in lockstep
// avoids the classic punch failure where both sides end up with a different
// connection: A talks on the link it dialed while B listens on the link it
// dialed, and each waits for a hello that is queued on the other one.
//
// After roleTimeout the preferred role gives up and both sides do both, which
// covers the case where the smaller id sits behind a NAT that drops outbound
// UDP to the peer.
func Connect(ctx context.Context, q *pathmgr.QUIC, addrs []*net.UDPAddr, self, peer [32]byte, params session.Params) (*session.Session, error) {
	const roleTimeout = 8 * time.Second

	dialFirst := bytes.Compare(self[:], peer[:]) < 0
	var both atomic.Bool
	go func() {
		t := time.NewTimer(roleTimeout)
		defer t.Stop()
		select {
		case <-ctx.Done():
		case <-t.C:
			both.Store(true)
		}
	}()

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	type result struct {
		s   *session.Session
		err error
	}
	ch := make(chan result, 32)
	report := func(s *session.Session, err error) {
		select {
		case ch <- result{s, err}:
		default:
		}
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			conn, err := q.Accept(ctx)
			if err != nil {
				debugf("accept stopped: %v", err)
				return
			}
			debugf("accepted conn remote=%s", conn.RemoteAddr())
			wg.Add(1)
			go func(conn net.Conn) {
				defer wg.Done()
				if dialFirst && !both.Load() {
					// The peer dials; accepting now keeps the roles apart.
				}
				s, err := session.Accept(conn, params)
				if err != nil {
					_ = conn.Close()
					report(nil, err)
					return
				}
				report(s, nil)
			}(conn)
		}
	}()

	for _, a := range addrs {
		addr := a.String()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if dialFirst || both.Load() {
					conn, err := q.DialAddr(ctx, addr)
					if err != nil {
						debugf("dial %s failed: %v", addr, err)
					}
					if err == nil {
						s, serr := session.Dial(conn, params)
						if serr != nil {
							_ = conn.Close()
							report(nil, serr)
						} else {
							report(s, nil)
						}
					}
				} else if !dialFirst && !both.Load() {
					// Wait for the preferred dialer to finish, or for the
					// fallback window to expire.
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(300 * time.Millisecond):
				}
			}
		}()
	}

	var last error
	for {
		select {
		case <-ctx.Done():
			if last == nil {
				last = ctx.Err()
			}
			return nil, fmt.Errorf("punch: connect: %w", last)
		case r := <-ch:
			if r.s != nil {
				return r.s, nil
			}
			last = r.err
		}
	}
}
