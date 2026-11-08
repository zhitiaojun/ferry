package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// lanResult is what a LAN send produced, plus how many attempts it took.
type lanResult struct {
	Res      *transfer.Result
	Peer     string
	Attempts int
}

// sendLAN pushes files over TCP and, if the connection drops (Wi-Fi switch,
// cable pulled, AP roam), dials again and lets the receiver's RESUME tell it
// where to continue from. The receiver keeps `<name>.ftpart` across sessions, so
// a retry normally resumes at the byte it stopped on instead of starting over.
func sendLAN(addr string, files []string, chunk int, frameTimeout time.Duration,
	deadline time.Duration, log *logging.Logger) (lanResult, error) {

	start := time.Now()
	var lastErr error
	for attempt := 1; ; attempt++ {
		if time.Since(start) > deadline {
			return lanResult{Attempts: attempt - 1}, fmt.Errorf("gave up after %v: %w", deadline, lastErr)
		}
		res, peer, err := sendLANOnce(addr, files, chunk, frameTimeout, log)
		if err == nil {
			return lanResult{Res: res, Peer: peer, Attempts: attempt}, nil
		}
		lastErr = err
		if !retryable(err) {
			return lanResult{Attempts: attempt}, err
		}
		wait := time.Duration(attempt) * 500 * time.Millisecond
		if wait > 4*time.Second {
			wait = 4 * time.Second
		}
		if log != nil {
			log.Event("lan.retry", map[string]any{
				"attempt": attempt, "wait_ms": wait.Milliseconds(), "error": err.Error(),
			})
		}
		time.Sleep(wait)
	}
}

func sendLANOnce(addr string, files []string, chunk int, frameTimeout time.Duration, log *logging.Logger) (*transfer.Result, string, error) {
	p := pathmgr.NewTCPDial(addr)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	nc, err := p.Dial(ctx)
	if err != nil {
		return nil, "", err
	}
	s, err := session.Dial(nc, session.Params{
		DeviceID:     identity.DeviceID(defaultName()),
		Name:         defaultName(),
		Capabilities: proto.CapResume,
	})
	if err != nil {
		_ = nc.Close()
		return nil, "", err
	}
	defer s.Close()
	res, err := transfer.Send(s, files, transfer.Config{
		ChunkSize: uint32(chunk),
		Timeout:   frameTimeout,
		Log:       log,
	})
	if err != nil {
		return nil, s.Peer.Name, err
	}
	return res, s.Peer.Name, nil
}

// retryable keeps permanent failures (bad manifest, hash mismatch, refused
// handshake) from spinning: only transport-shaped errors are worth a redial.
func retryable(err error) bool {
	if err == nil {
		return false
	}
	var re *session.RemoteError
	if errors.As(err, &re) {
		return false // the peer answered and rejected us: retrying will not help
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	if errors.As(err, &ne) {
		return true
	}
	var oe *net.OpError
	return errors.As(err, &oe)
}
