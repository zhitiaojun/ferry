package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/punch"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// sendViaRace starts the punch and the relay paths at t=0 and keeps whichever
// finishes its session handshake first (D2). The loser is cancelled through the
// attempt context.
func sendViaRace(ctx context.Context, po punchOptions, ro relayOptions) (summary, error) {
	key, err := loadDeviceKey(po.KeyPath)
	if err != nil {
		return summary{}, err
	}
	peerPub, err := pairedPeer(po.PairedPath, po.ToID)
	if err != nil {
		return summary{}, err
	}
	g, err := gatherCandidates(ctx, po)
	if err != nil {
		return summary{}, err
	}
	defer g.Close()

	files, _, err := describeFiles(po.Files)
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

	client, err := signaling.Dial(ctx, ro.SignalURL, key, ro.Log)
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
		ToDevice: po.ToID, SessionID: sid, TransferID: tid, Files: files,
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
			return summary{}, fmt.Errorf("peer did not answer the race request")
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
				if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
					ToDevice: po.ToID, SessionID: sid, Candidates: signalingCandidates(g.Candidates),
				}); err != nil {
					return summary{}, fmt.Errorf("send candidates: %w", err)
				}
			case signaling.TypeCandidates:
				c, err := signaling.Decode[signaling.Candidates](e)
				if err != nil {
					return summary{}, err
				}
				peerCands = c.Candidates
			}
		}
	}

	peerLocal := localPeerCandidates(peerCands)
	tlsCfg, err := identity.ClientTLS(key, peerPub)
	if err != nil {
		return summary{}, err
	}
	q := pathmgr.NewQUIC(g.Conn, tlsCfg)
	defer q.Close()

	punchAttempt := pathmgr.Attempt[*session.Session]{
		Name: "punch",
		Fn: func(actx context.Context) (*session.Session, error) {
			if os.Getenv("FT_PUNCH_BLOCK") == "1" {
				return nil, fmt.Errorf("punch blocked by FT_PUNCH_BLOCK (test hook)")
			}
			punchCtx, cancel := context.WithCancel(actx)
			defer cancel()
			go g.Punch(punchCtx, peerLocal)
			conn, err := punchRaceDial(actx, q, peerLocal, 12*time.Second)
			if err != nil {
				return nil, err
			}
			cancel()
			s, err := session.Dial(conn, sessionParams(key, po.Name))
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return s, nil
		},
	}
	relayAttempt := pathmgr.Attempt[*session.Session]{
		Name: "relay",
		Fn: func(actx context.Context) (*session.Session, error) {
			room, err := openRelayRoom(actx, client, po.ToID, tid)
			if err != nil {
				return nil, err
			}
			rc, err := relay.Dial(actx, room.url)
			if err != nil {
				return nil, err
			}
			conn, err := clientTLS(actx, net.Conn(rc), key, peerPub)
			if err != nil {
				return nil, err
			}
			s, err := session.Dial(conn, sessionParams(key, po.Name))
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return s, nil
		},
	}

	sess, winner, err := pathmgr.Race(ctx, punchAttempt, relayAttempt)
	if err != nil {
		return summary{}, fmt.Errorf("race: %w", err)
	}
	defer sess.Drain(2 * time.Second)
	if ro.Log != nil {
		ro.Log.Event("path.selected", map[string]any{"path": winner, "peer": truncHex(po.ToID)})
	}

	start := time.Now()
	res, err := transfer.Send(sess, po.Files, transfer.Config{
		ChunkSize: uint32(po.Chunk), Timeout: po.Timeout, Log: ro.Log,
		TransferID: hex16(tid),
	})
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK: true, Path: winner, Peer: truncHex(po.ToID),
		TransferID: tid, Files: res.Files, Bytes: res.Bytes,
		Chunks: res.Chunks, DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// recvViaRace accepts whichever path connects first: the punched QUIC tunnel or
// the relay room the sender books at the same moment (D2).
func recvViaRace(ctx context.Context, po punchOptions, ro relayOptions) (summary, error) {
	key, err := loadDeviceKey(po.KeyPath)
	if err != nil {
		return summary{}, err
	}
	g, err := gatherCandidates(ctx, po)
	if err != nil {
		return summary{}, err
	}
	defer g.Close()

	client, err := signaling.Dial(ctx, ro.SignalURL, key, ro.Log)
	if err != nil {
		return summary{}, err
	}
	defer client.Close()

	requests := make(chan *signaling.Envelope, 4)
	cands := make(chan *signaling.Envelope, 8)
	readyCh := make(chan signaling.RelayReady, 1)
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
	client.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		r, err := signaling.Decode[signaling.RelayReady](e)
		if err == nil {
			select {
			case readyCh <- r:
			default:
			}
		}
	})
	if po.Log != nil {
		po.Log.Event("race.ready", map[string]any{"device_id": truncHex(key.IDHex())})
	}

	var req signaling.SendRequest
	select {
	case <-ctx.Done():
		return summary{}, ctx.Err()
	case e := <-requests:
		r, err := signaling.Decode[signaling.SendRequest](e)
		if err != nil {
			return summary{}, err
		}
		req = r
	}
	peerPub, err := pairedPeer(po.PairedPath, req.FromDevice)
	if err != nil {
		_ = client.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "unauthorized", Message: err.Error()})
		return summary{}, err
	}
	if err := client.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
		ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
	}); err != nil {
		return summary{}, err
	}

	serverCfg, err := identity.ServerTLS(key, peerPub)
	if err != nil {
		return summary{}, err
	}
	q := pathmgr.NewQUIC(g.Conn, serverCfg)
	defer q.Close()
	if err := q.PrepareListen(ctx); err != nil {
		return summary{}, err
	}
	if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
		ToDevice: req.FromDevice, SessionID: req.SessionID, Candidates: signalingCandidates(g.Candidates),
	}); err != nil {
		return summary{}, err
	}

	punchAttempt := pathmgr.Attempt[*session.Session]{
		Name: "punch",
		Fn: func(actx context.Context) (*session.Session, error) {
			if os.Getenv("FT_PUNCH_BLOCK") == "1" {
				return nil, fmt.Errorf("punch blocked by FT_PUNCH_BLOCK (test hook)")
			}
			var peerCands []signaling.Candidate
			t := time.NewTimer(10 * time.Second)
			defer t.Stop()
			for peerCands == nil {
				select {
				case <-actx.Done():
					return nil, actx.Err()
				case <-t.C:
					return nil, fmt.Errorf("peer did not publish candidates")
				case ce := <-cands:
					c, err := signaling.Decode[signaling.Candidates](ce)
					if err != nil {
						return nil, err
					}
					peerCands = c.Candidates
				}
			}
			peerLocal := localPeerCandidates(peerCands)
			if po.Log != nil {
				var addrs []string
				for _, a := range punch.Resolve(peerLocal) {
					addrs = append(addrs, a.String())
				}
				po.Log.Event("punch.peer_candidates", map[string]any{"addrs": addrs, "count": len(peerCands)})
			}
			punchCtx, cancel := context.WithCancel(actx)
			defer cancel()
			go g.Punch(punchCtx, peerLocal)
			cctx, ccancel := context.WithTimeout(actx, 25*time.Second)
			punch.Debugf = func(format string, args ...any) {
				po.Log.Event("punch.debug", map[string]any{"msg": fmt.Sprintf(format, args...)})
			}
			s, err := punch.Connect(cctx, q, punch.Resolve(peerLocal), key.ID(), parseDeviceID(req.FromDevice), sessionParams(key, po.Name))
			ccancel()
			if err != nil {
				if po.Log != nil {
					po.Log.Event("punch.error", map[string]any{"error": err.Error()})
				}
				return nil, err
			}
			cancel()
			return s, nil
		},
	}
	relayAttempt := pathmgr.Attempt[*session.Session]{
		Name: "relay",
		Fn: func(actx context.Context) (*session.Session, error) {
			var ready signaling.RelayReady
			select {
			case <-actx.Done():
				return nil, actx.Err()
			case ready = <-readyCh:
			}
			rc, err := relay.Dial(actx, readyURL(ro.SignalURL, ready))
			if err != nil {
				return nil, err
			}
			conn, err := serverTLS(actx, net.Conn(rc), key, peerPub)
			if err != nil {
				return nil, err
			}
			if ro.Log != nil {
				ro.Log.Event("relay.tls", map[string]any{"peer": truncHex(req.FromDevice), "tls": "1.3", "pinned": true})
			}
			s, err := session.Accept(conn, sessionParams(key, po.Name))
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return s, nil
		},
	}

	sess, winner, err := pathmgr.Race(ctx, punchAttempt, relayAttempt)
	if err != nil {
		return summary{}, fmt.Errorf("race: %w", err)
	}
	defer sess.Drain(2 * time.Second)
	if po.Log != nil {
		po.Log.Event("path.selected", map[string]any{"path": winner, "peer": truncHex(req.FromDevice)})
	}

	start := time.Now()
	res, err := transfer.Receive(sess, po.OutDir, transfer.Config{
		ChunkSize: uint32(po.Chunk), Timeout: po.Timeout, Log: po.Log,
		TransferID: hex16(req.TransferID),
	})
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK: true, Path: winner, Peer: truncHex(req.FromDevice),
		TransferID: req.TransferID, Files: res.Files, Bytes: res.Bytes,
		Chunks: res.Chunks, DurationMS: time.Since(start).Milliseconds(),
	}, nil
}
