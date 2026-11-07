package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/punch"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// punchOptions carries what both hole-punching directions need (M4).
type punchOptions struct {
	SignalURL  string
	KeyPath    string
	PairedPath string
	Name       string
	ToID       string
	Files      []string
	OutDir     string
	Chunk      int
	Timeout    time.Duration
	STUN       []string
	Log        *logging.Logger
}

// signalingCandidates converts local candidates to the wire format.
func signalingCandidates(cs []punch.Candidate) []signaling.Candidate {
	out := make([]signaling.Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, signaling.Candidate{Kind: c.Kind, Addr: c.Addr, Priority: c.Priority})
	}
	return out
}

// localPeerCandidates converts wire candidates back.
func localPeerCandidates(cs []signaling.Candidate) []punch.Candidate {
	out := make([]punch.Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, punch.Candidate{Kind: c.Kind, Addr: c.Addr, Priority: c.Priority})
	}
	return out
}

func gatherCandidates(ctx context.Context, o punchOptions) (*punch.Gathered, error) {
	g, err := punch.Gather(ctx, o.STUN, time.Second, o.Log)
	if err != nil {
		return nil, err
	}
	// FT_PUNCH_LOOPBACK=1 adds a loopback candidate. It exists so two processes
	// on one Windows host can exercise the punch path: inbound UDP to a random
	// port is blocked by the default firewall, while loopback is exempt.
	if os.Getenv("FT_PUNCH_LOOPBACK") == "1" {
		if ap, ok := g.Conn.LocalAddr().(*net.UDPAddr); ok {
			g.Candidates = append(g.Candidates, punch.Candidate{
				Kind: "lan", Addr: net.JoinHostPort("127.0.0.1", fmt.Sprint(ap.Port)), Priority: 100,
			})
		}
	}
	if len(g.Candidates) == 0 {
		g.Close()
		return nil, fmt.Errorf("no usable candidate (no LAN address and no STUN response)")
	}
	return g, nil
}

// sendViaPunch exchanges candidates through the signaling channel and sends
// the files over a QUIC tunnel punched through the NATs (protocol §8.4).
func sendViaPunch(ctx context.Context, o punchOptions) (summary, error) {
	key, err := loadDeviceKey(o.KeyPath)
	if err != nil {
		return summary{}, err
	}
	peerPub, err := pairedPeer(o.PairedPath, o.ToID)
	if err != nil {
		return summary{}, err
	}
	g, err := gatherCandidates(ctx, o)
	if err != nil {
		return summary{}, err
	}
	defer g.Close()

	files, _, err := describeFiles(o.Files)
	if err != nil {
		return summary{}, err
	}
	sid, err := randomHex16()
	if err != nil {
		return summary{}, err
	}
	tid, err := randomHex16()
	if err != nil {
		return summary{}, err
	}

	client, err := signaling.Dial(ctx, o.SignalURL, key, o.Log)
	if err != nil {
		return summary{}, err
	}
	defer client.Close()

	events := make(chan *signaling.Envelope, 16)
	for _, typ := range []string{signaling.TypeSendResp, signaling.TypeCandidates, signaling.TypeError} {
		t := typ
		client.On(t, func(e *signaling.Envelope) {
			select {
			case events <- e:
			default:
			}
		})
	}

	if err := client.Send(ctx, signaling.TypeSendRequest, signaling.SendRequest{
		ToDevice:   o.ToID,
		SessionID:  sid,
		TransferID: tid,
		Files:      files,
	}); err != nil {
		return summary{}, fmt.Errorf("send request: %w", err)
	}

	var peerCands []signaling.Candidate
	accepted := false
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for !accepted || peerCands == nil {
		select {
		case <-ctx.Done():
			return summary{}, ctx.Err()
		case <-deadline.C:
			return summary{}, fmt.Errorf("peer did not answer the punch request")
		case e := <-events:
			switch e.Type {
			case signaling.TypeError:
				eb, _ := signaling.Decode[signaling.ErrorBody](e)
				return summary{}, fmt.Errorf("peer error: %s", eb.Message)
			case signaling.TypeSendResp:
				r, err := signaling.Decode[signaling.SendResponse](e)
				if err != nil {
					return summary{}, err
				}
				if !r.Accept {
					return summary{}, fmt.Errorf("peer declined: %s", r.Reason)
				}
				accepted = true
			case signaling.TypeCandidates:
				c, err := signaling.Decode[signaling.Candidates](e)
				if err != nil {
					return summary{}, err
				}
				peerCands = c.Candidates
				if !accepted {
					// The receiver may publish candidates before its response.
					accepted = false
				}
			}
		}
	}
	if !accepted {
		return summary{}, fmt.Errorf("peer never accepted the transfer")
	}

	peerLocal := localPeerCandidates(peerCands)
	peerAddr, err := punch.Primary(peerLocal)
	if err != nil {
		return summary{}, err
	}

	punchCtx, cancelPunch := context.WithCancel(ctx)
	defer cancelPunch()
	go g.Punch(punchCtx, peerLocal)

	serverTLS, err := identity.ServerTLS(key, peerPub)
	if err != nil {
		return summary{}, err
	}
	q := pathmgr.NewQUIC(g.Conn, serverTLS)
	defer q.Close()
	q.SetPeer(peerAddr.String())
	if err := q.PrepareListen(ctx); err != nil {
		return summary{}, err
	}
	// Listen before publishing: the peer dials the mapping we advertise, and
	// on a symmetric NAT it is our outbound packets that open it.
	if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
		ToDevice:   o.ToID,
		SessionID:  sid,
		Candidates: signalingCandidates(g.Candidates),
	}); err != nil {
		return summary{}, fmt.Errorf("send candidates: %w", err)
	}
	if o.Log != nil {
		o.Log.Event("punch.dial", map[string]any{"local": g.Conn.LocalAddr().String(), "candidates": len(peerLocal)})
	}
	actx, cancelAccept := context.WithTimeout(ctx, 25*time.Second)
	sess, err := punch.Connect(actx, q, punch.Resolve(peerLocal), key.ID(), parseDeviceID(o.ToID), sessionParams(key, o.Name))
	cancelAccept()
	if err != nil {
		return summary{}, fmt.Errorf("hole punching (%s): %w", peerAddr, err)
	}
	cancelPunch()
	cfg := transfer.Config{
		ChunkSize:  uint32(o.Chunk),
		Timeout:    o.Timeout,
		Log:        o.Log,
		TransferID: hex16(tid),
	}
	start := time.Now()
	res, err := transfer.Send(sess, o.Files, cfg)
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK:         true,
		Path:       "punch",
		Peer:       truncHex(o.ToID),
		TransferID: tid,
		Files:      res.Files,
		Bytes:      res.Bytes,
		Chunks:     res.Chunks,
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// punchRaceDial dials every peer candidate concurrently and returns the first
// QUIC tunnel that completes. Trying all of them matters because the highest
// priority candidate (the STUN mapping) may be unreachable when the peer sits
// behind the same NAT as us, while its LAN address works.
func punchRaceDial(ctx context.Context, q *pathmgr.QUIC, cands []punch.Candidate, timeout time.Duration) (net.Conn, error) {
	addrs := punch.Resolve(cands)
	if len(addrs) == 0 {
		return nil, fmt.Errorf("peer published no usable candidate")
	}
	type result struct {
		conn net.Conn
		err  error
	}
	results := make(chan result, len(addrs))
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	for _, a := range addrs {
		addr := a.String()
		go func() {
			conn, err := q.DialAddr(dctx, addr)
			results <- result{conn: conn, err: err}
		}()
	}
	var last error
	for range addrs {
		select {
		case <-dctx.Done():
			return nil, dctx.Err()
		case r := <-results:
			if r.err == nil {
				return r.conn, nil
			}
			last = r.err
		}
	}
	if last == nil {
		last = fmt.Errorf("no candidate accepted the tunnel")
	}
	return nil, last
}

// waitQUICDial retries the QUIC handshake while the punch packets open the NAT
// mappings; each failed attempt is cheap and immediately retried.
func waitQUICDial(ctx context.Context, q *pathmgr.QUIC, timeout time.Duration) (net.Conn, error) {
	deadline := time.Now().Add(timeout)
	var last error
	for time.Now().Before(deadline) {
		dctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		conn, err := q.Dial(dctx)
		cancel()
		if err == nil {
			return conn, nil
		}
		last = err
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
	if last == nil {
		last = fmt.Errorf("timed out")
	}
	return nil, last
}

// recvViaPunch waits for a punch request and receives the files over the QUIC
// tunnel (protocol §8.4).
func recvViaPunch(ctx context.Context, o punchOptions) (summary, error) {
	key, err := loadDeviceKey(o.KeyPath)
	if err != nil {
		return summary{}, err
	}
	g, err := gatherCandidates(ctx, o)
	if err != nil {
		return summary{}, err
	}
	defer g.Close()

	client, err := signaling.Dial(ctx, o.SignalURL, key, o.Log)
	if err != nil {
		return summary{}, err
	}
	defer client.Close()

	requests := make(chan *signaling.Envelope, 8)
	cands := make(chan *signaling.Envelope, 8)
	client.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		select {
		case requests <- e:
		default:
		}
	})
	client.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		select {
		case cands <- e:
		default:
		}
	})

	if o.Log != nil {
		o.Log.Event("punch.ready", map[string]any{"device_id": truncHex(key.IDHex())})
	}

	select {
	case <-ctx.Done():
		return summary{}, ctx.Err()
	case e := <-requests:
		req, err := signaling.Decode[signaling.SendRequest](e)
		if err != nil {
			return summary{}, err
		}
		peerPub, err := pairedPeer(o.PairedPath, req.FromDevice)
		if err != nil {
			_ = client.Send(ctx, signaling.TypeError, signaling.ErrorBody{
				Code:    "unauthorized",
				Message: err.Error(),
			})
			return summary{}, err
		}
		if err := client.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
			ToDevice:   req.FromDevice,
			TransferID: req.TransferID,
			Accept:     true,
		}); err != nil {
			return summary{}, err
		}
		serverTLS, err := identity.ServerTLS(key, peerPub)
		if err != nil {
			return summary{}, err
		}
		q := pathmgr.NewQUIC(g.Conn, serverTLS)
		defer q.Close()
		if err := q.PrepareListen(ctx); err != nil {
			return summary{}, err
		}
		if o.Log != nil {
			o.Log.Event("punch.listen", map[string]any{"local": g.Conn.LocalAddr().String()})
		}
		if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
			ToDevice:   req.FromDevice,
			SessionID:  req.SessionID,
			Candidates: signalingCandidates(g.Candidates),
		}); err != nil {
			return summary{}, err
		}

		var peerCands []signaling.Candidate
		timeout := time.NewTimer(10 * time.Second)
		defer timeout.Stop()
		for peerCands == nil {
			select {
			case <-ctx.Done():
				return summary{}, ctx.Err()
			case <-timeout.C:
				return summary{}, fmt.Errorf("peer did not publish candidates")
			case ce := <-cands:
				c, err := signaling.Decode[signaling.Candidates](ce)
				if err != nil {
					return summary{}, err
				}
				peerCands = c.Candidates
			}
		}
		peerLocal := localPeerCandidates(peerCands)
		punchCtx, cancelPunch := context.WithCancel(ctx)
		defer cancelPunch()
		go g.Punch(punchCtx, peerLocal)

		actx, cancelAccept := context.WithTimeout(ctx, 25*time.Second)
		sess, err := punch.Connect(actx, q, punch.Resolve(peerLocal), key.ID(), parseDeviceID(req.FromDevice), sessionParams(key, o.Name))
		cancelAccept()
		if err != nil {
			return summary{}, err
		}
		cancelPunch()
		start := time.Now()
		res, err := transfer.Receive(sess, o.OutDir, transfer.Config{
			ChunkSize:  uint32(o.Chunk),
			Timeout:    o.Timeout,
			Log:        o.Log,
			TransferID: hex16(req.TransferID),
		})
		if err != nil {
			return summary{}, err
		}
		return summary{
			OK:         true,
			Path:       "punch",
			Peer:       truncHex(req.FromDevice),
			TransferID: req.TransferID,
			Files:      res.Files,
			Bytes:      res.Bytes,
			Chunks:     res.Chunks,
			DurationMS: time.Since(start).Milliseconds(),
		}, nil
	}
}

// parseDeviceID turns the hex device id carried by signaling into the array
// form used by identity and the punch role selection.
func parseDeviceID(hexID string) [32]byte {
	var id [32]byte
	if b, err := hex.DecodeString(hexID); err == nil && len(b) == 32 {
		copy(id[:], b)
	}
	return id
}
