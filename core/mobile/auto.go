package mobile

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/punch"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// autoReceiver is the single "receive" entry point of the app: one signaling
// session, and whichever path (punch or relay) completes its handshake first
// is used (D1/D2).
type autoReceiver struct {
	cancel context.CancelFunc
	done   chan struct{}
}

var (
	autoMu  sync.Mutex
	autoRcv *autoReceiver
)

// StartAutoReceive starts the one-button receiver. It announces nothing: the
// sender books either a punch attempt or a relay room, and the first session
// that reaches the handshake wins.
func StartAutoReceive(name, outDir, signalURL, keyPath, pairedPath, hintJSON, stunList string) (string, error) {
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = "android"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	autoMu.Lock()
	if autoRcv != nil {
		autoMu.Unlock()
		return key.IDHex(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &autoReceiver{cancel: cancel, done: make(chan struct{})}
	autoRcv = r
	autoMu.Unlock()

	hint := parseHint(hintJSON)
	servers := splitSTUN(stunList)
	logger := openLogger(outDir)
	go func() {
		defer close(r.done)
		for ctx.Err() == nil {
			if err := autoServe(ctx, key, name, outDir, signalURL, pairedPath, hint, servers, logger); err != nil {
				if logger != nil {
					logger.Event("auto.recv_error", map[string]any{"error": err.Error()})
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}()
	return key.IDHex(), nil
}

// StopAutoReceive stops the auto receiver.
func StopAutoReceive() error {
	autoMu.Lock()
	r := autoRcv
	autoRcv = nil
	autoMu.Unlock()
	if r == nil {
		return nil
	}
	r.cancel()
	select {
	case <-r.done:
	case <-time.After(3 * time.Second):
	}
	return nil
}

func autoServe(ctx context.Context, key *identity.Key, name, outDir, signalURL, pairedPath string, hint *discovery.Hint, servers []string, logger *logging.Logger) error {
	g, err := gatherHint(ctx, servers, hint, logger)
	if err != nil {
		return err
	}
	defer g.Close()

	client, err := signaling.Dial(ctx, signalURL, key, logger)
	if err != nil {
		return err
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
	if logger != nil {
		logger.Event("auto.ready", map[string]any{"device_id": truncID(key.IDHex())})
	}

	var req signaling.SendRequest
	select {
	case <-ctx.Done():
		return ctx.Err()
	case e := <-requests:
		r, err := signaling.Decode[signaling.SendRequest](e)
		if err != nil {
			return err
		}
		req = r
	}
	peerPub, err := pairedPeer(pairedPath, req.FromDevice)
	if err != nil {
		_ = client.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "unauthorized", Message: err.Error()})
		return err
	}
	if err := client.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
		ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
	}); err != nil {
		return err
	}

	serverCfg, err := identity.ServerTLS(key, peerPub)
	if err != nil {
		return err
	}
	q := pathmgr.NewQUIC(g.Conn, serverCfg)
	defer q.Close()
	if err := q.PrepareListen(ctx); err != nil {
		return err
	}
	if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
		ToDevice: req.FromDevice, SessionID: req.SessionID, Candidates: signalingCandidates(g.Candidates),
	}); err != nil {
		return err
	}

	punchAttempt := pathmgr.Attempt[*session.Session]{
		Name: "punch",
		Fn: func(actx context.Context) (*session.Session, error) {
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
			peerLocal := localCandidates(peerCands)
			pctx, cancel := context.WithCancel(actx)
			defer cancel()
			go g.Punch(pctx, peerLocal)
			cctx, ccancel := context.WithTimeout(actx, 25*time.Second)
			defer ccancel()
			s, err := punch.Connect(cctx, q, punch.Resolve(peerLocal), key.ID(), parseDeviceID(req.FromDevice), sessionParams(key, name))
			if err != nil {
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
			rc, err := relay.Dial(actx, relayURL(signalURL, ready))
			if err != nil {
				return nil, err
			}
			conn, err := serverTLS(actx, net.Conn(rc), key, peerPub)
			if err != nil {
				return nil, err
			}
			s, err := session.Accept(conn, sessionParams(key, name))
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return s, nil
		},
	}

	sess, winner, err := pathmgr.Race(ctx, punchAttempt, relayAttempt)
	if err != nil {
		return fmt.Errorf("race: %w", err)
	}
	defer sess.Drain(2 * time.Second)
	if logger != nil {
		logger.Event("path.selected", map[string]any{"path": winner, "peer": truncID(req.FromDevice)})
	}
	_, err = transfer.Receive(sess, outDir, transfer.Config{
		ChunkSize: 0, Timeout: 30 * time.Second, Log: logger,
		TransferID: hex16(req.TransferID),
	})
	if err != nil {
		return err
	}
	if logger != nil {
		logger.Event("auto.done", map[string]any{"path": winner, "peer": truncID(req.FromDevice)})
	}
	return nil
}

// SendAuto is the one-button sender: it books a relay room and starts the
// punch at the same moment, then uses whichever session completes first.
func SendAuto(signalURL, keyPath, pairedPath, toID, name, pathsJSON, hintJSON, stunList string) (string, error) {
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return "", fmt.Errorf("paths: %w", err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no files")
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	peerPub, err := pairedPeer(pairedPath, toID)
	if err != nil {
		return "", err
	}
	logger := openTempLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	hint := parseHint(hintJSON)
	g, err := gatherHint(ctx, splitSTUN(stunList), hint, logger)
	if err != nil {
		return "", err
	}
	defer g.Close()

	files, _, err := describeFiles(paths)
	if err != nil {
		return "", err
	}
	sid, err := randHex16()
	if err != nil {
		return "", err
	}
	tid, err := randHex16()
	if err != nil {
		return "", err
	}
	client, err := signaling.Dial(ctx, signalURL, key, logger)
	if err != nil {
		return "", err
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
		ToDevice: toID, SessionID: sid, TransferID: tid, Files: files,
	}); err != nil {
		return "", fmt.Errorf("send request: %w", err)
	}

	var peerCands []signaling.Candidate
	accepted := false
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for !accepted || peerCands == nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("peer did not answer the request")
		case e := <-events:
			switch e.Type {
			case signaling.TypeError:
				eb, _ := signaling.Decode[signaling.ErrorBody](e)
				return "", fmt.Errorf("peer error: %s", eb.Message)
			case signaling.TypeSendResp:
				r, err := signaling.Decode[signaling.SendResponse](e)
				if err != nil {
					return "", err
				}
				if !r.Accept {
					return "", fmt.Errorf("peer declined: %s", r.Reason)
				}
				accepted = true
				if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
					ToDevice: toID, SessionID: sid, Candidates: signalingCandidates(g.Candidates),
				}); err != nil {
					return "", fmt.Errorf("send candidates: %w", err)
				}
			case signaling.TypeCandidates:
				c, err := signaling.Decode[signaling.Candidates](e)
				if err != nil {
					return "", err
				}
				peerCands = c.Candidates
			}
		}
	}

	peerLocal := localCandidates(peerCands)
	tlsCfg, err := identity.ClientTLS(key, peerPub)
	if err != nil {
		return "", err
	}
	q := pathmgr.NewQUIC(g.Conn, tlsCfg)
	defer q.Close()

	punchAttempt := pathmgr.Attempt[*session.Session]{
		Name: "punch",
		Fn: func(actx context.Context) (*session.Session, error) {
			pctx, pcancel := context.WithCancel(actx)
			defer pcancel()
			go g.Punch(pctx, peerLocal)
			conn, err := dialRace(actx, q, punch.Resolve(peerLocal), 12*time.Second)
			if err != nil {
				return nil, err
			}
			pcancel()
			s, err := session.Dial(conn, sessionParams(key, name))
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
			room, err := openRelayRoom(actx, client, toID, tid)
			if err != nil {
				return nil, err
			}
			rc, err := relay.Dial(actx, relayURL(signalURL, room))
			if err != nil {
				return nil, err
			}
			conn, err := clientTLS(actx, net.Conn(rc), key, peerPub)
			if err != nil {
				return nil, err
			}
			s, err := session.Dial(conn, sessionParams(key, name))
			if err != nil {
				_ = conn.Close()
				return nil, err
			}
			return s, nil
		},
	}

	sess, winner, err := pathmgr.Race(ctx, punchAttempt, relayAttempt)
	if err != nil {
		return "", fmt.Errorf("race: %w", err)
	}
	defer sess.Drain(2 * time.Second)
	if logger != nil {
		logger.Event("path.selected", map[string]any{"path": winner, "peer": truncID(toID)})
	}
	start := time.Now()
	res, err := transfer.Send(sess, paths, transfer.Config{
		ChunkSize: 0, Timeout: 30 * time.Second, Log: logger,
		TransferID: hex16(tid),
	})
	if err != nil {
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "path": winner, "peer": truncID(toID), "transfer_id": tid,
		"files": res.Files, "bytes": res.Bytes, "chunks": res.Chunks,
		"duration_ms": time.Since(start).Milliseconds(),
	})
	return string(out), nil
}
