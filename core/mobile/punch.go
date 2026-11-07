package mobile

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/punch"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

var (
	punchMu  sync.Mutex
	punchRcv *punchReceiver
)

type punchReceiver struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// DefaultSTUN is used when the caller passes an empty server list.
const DefaultSTUN = "123.57.253.208:40000,123.57.253.208:40001"

// StartPunchReceiver waits on the signaling server for a punch request and
// receives the files into outDir. It returns this device's id.
func StartPunchReceiver(name, outDir, signalURL, keyPath, pairedPath, hintJSON, stunList string) (string, error) {
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
	logger := openLogger(outDir)
	hint := parseHint(hintJSON)
	servers := splitSTUN(stunList)

	ctx, cancel := context.WithCancel(context.Background())
	r := &punchReceiver{cancel: cancel, done: make(chan struct{})}
	punchMu.Lock()
	if punchRcv != nil {
		punchMu.Unlock()
		cancel()
		return key.IDHex(), nil
	}
	punchRcv = r
	punchMu.Unlock()

	go func() {
		defer close(r.done)
		for ctx.Err() == nil {
			if err := r.serveOnce(ctx, name, outDir, signalURL, keyPath, pairedPath, hint, servers, key, logger); err != nil {
				if logger != nil {
					logger.Event("punch.recv_error", map[string]any{"error": err.Error()})
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

// StopPunchReceiver stops the background receiver.
func StopPunchReceiver() error {
	punchMu.Lock()
	r := punchRcv
	punchRcv = nil
	punchMu.Unlock()
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

func (r *punchReceiver) serveOnce(ctx context.Context, name, outDir, signalURL, keyPath, pairedPath string, hint *discovery.Hint, servers []string, key *identity.Key, logger *logging.Logger) error {
	client, err := signaling.Dial(ctx, signalURL, key, logger)
	if err != nil {
		return err
	}
	defer client.Close()

	reqCh := make(chan *signaling.Envelope, 4)
	client.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		select {
		case reqCh <- e:
		default:
		}
	})

	var req signaling.SendRequest
	select {
	case <-ctx.Done():
		return ctx.Err()
	case e := <-reqCh:
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

	g, err := gatherHint(ctx, servers, hint, logger)
	if err != nil {
		_ = client.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "internal", Message: err.Error()})
		return err
	}
	defer g.Close()

	serverCfg, err := identity.ServerTLS(key, peerPub)
	if err != nil {
		return err
	}
	q := pathmgr.NewQUIC(g.Conn, serverCfg)
	defer q.Close()
	if err := q.PrepareListen(ctx); err != nil {
		return err
	}
	if err := client.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
		ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
	}); err != nil {
		return err
	}
	if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
		ToDevice: req.FromDevice, SessionID: req.SessionID, Candidates: signalingCandidates(g.Candidates),
	}); err != nil {
		return err
	}

	candCh := make(chan *signaling.Envelope, 8)
	client.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		select {
		case candCh <- e:
		default:
		}
	})
	var peer []signaling.Candidate
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for peer == nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("peer did not publish candidates")
		case e := <-candCh:
			c, err := signaling.Decode[signaling.Candidates](e)
			if err != nil {
				return err
			}
			peer = c.Candidates
		}
	}
	punchCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go g.Punch(punchCtx, localCandidates(peer))

	conn, err := q.AcceptOrDial(ctx, punch.Resolve(localCandidates(peer)))
	if err != nil {
		return err
	}
	sess, err := session.Accept(conn, sessionParams(key, name))
	if err != nil {
		_ = conn.Close()
		return err
	}
	defer sess.Drain(2 * time.Second)

	cfg := transfer.Config{
		ChunkSize:  0,
		Timeout:    30 * time.Second,
		Log:        logger,
		TransferID: hex16(req.TransferID),
	}
	self := truncID(req.FromDevice)
	start := time.Now()
	res, err := transfer.Receive(sess, outDir, cfg)
	if err != nil {
		return err
	}
	if logger != nil {
		logger.Event("punch.received", map[string]any{
			"peer": self, "files": len(res.Files), "bytes": res.Bytes,
			"chunks": res.Chunks, "duration_ms": time.Since(start).Milliseconds(),
		})
	}
	return nil
}

// SendViaPunch negotiates over signaling, punches the QUIC tunnel and sends the
// files. pathsJSON is a JSON array of absolute paths. The JSON summary is
// returned as a string for the Java layer.
func SendViaPunch(signalURL, keyPath, pairedPath, toID, name, pathsJSON, hintJSON, stunList string) (string, error) {
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = "android"
	}
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return "", fmt.Errorf("paths must be a JSON array: %w", err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no files selected")
	}
	peerPub, err := pairedPeer(pairedPath, toID)
	if err != nil {
		return "", err
	}
	slog := openTempLogger()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	g, err := gatherHint(ctx, splitSTUN(stunList), parseHint(hintJSON), slog)
	if err != nil {
		return "", err
	}
	defer g.Close()

	files, total, err := describeFiles(paths)
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

	client, err := signaling.Dial(ctx, signalURL, key, slog)
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
	var peer []signaling.Candidate
	accepted := false
	deadline := time.NewTimer(15 * time.Second)
	defer deadline.Stop()
	for !accepted || peer == nil {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-deadline.C:
			return "", fmt.Errorf("peer did not answer the punch request")
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
					return "", err
				}
			case signaling.TypeCandidates:
				c, err := signaling.Decode[signaling.Candidates](e)
				if err != nil {
					return "", err
				}
				if c.ToDevice == "" || c.ToDevice == key.IDHex() {
					peer = c.Candidates
				}
			}
		}
	}

	tlsCfg, err := identity.ClientTLS(key, peerPub)
	if err != nil {
		return "", err
	}
	q := pathmgr.NewQUIC(g.Conn, tlsCfg)
	defer q.Close()

	punchCtx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	go g.Punch(punchCtx, localCandidates(peer))

	conn, err := dialRace(ctx, q, punch.Resolve(localCandidates(peer)), 12*time.Second)
	if err != nil {
		return "", fmt.Errorf("punch: %w", err)
	}
	sess, err := session.Dial(conn, sessionParams(key, name))
	if err != nil {
		_ = conn.Close()
		return "", err
	}
	defer sess.Drain(2 * time.Second)

	start := time.Now()
	res, err := transfer.Send(sess, paths, transfer.Config{
		ChunkSize: 0, Timeout: 30 * time.Second, Log: slog,
		TransferID: hex16(tid),
	})
	if err != nil {
		return "", err
	}
	s := summary{
		OK: true, Path: "punch", Peer: truncID(toID), TransferID: tid,
		Files: res.Files, Bytes: total, Chunks: res.Chunks,
		DurationMS: time.Since(start).Milliseconds(),
	}
	return marshalSummary(s), nil
}

func dialRace(ctx context.Context, q *pathmgr.QUIC, addrs []*net.UDPAddr, timeout time.Duration) (net.Conn, error) {
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no candidate addresses")
	}
	type out struct {
		c   net.Conn
		err error
	}
	ch := make(chan out, len(addrs))
	for _, a := range addrs {
		go func(addr string) {
			c, err := q.DialAddr(ctx, addr)
			ch <- out{c, err}
		}(a.String())
	}
	var lastErr error
	t := time.NewTimer(timeout)
	defer t.Stop()
	for i := 0; i < len(addrs); i++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-t.C:
			return nil, fmt.Errorf("punch dial timeout")
		case o := <-ch:
			if o.err == nil {
				return o.c, nil
			}
			lastErr = o.err
		}
	}
	return nil, lastErr
}

func gatherHint(ctx context.Context, servers []string, hint *discovery.Hint, logger *logging.Logger) (*punch.Gathered, error) {
	if hint == nil {
		return punch.GatherSTUNOnly(ctx, servers, 2*time.Second, logger)
	}
	return punch.GatherHint(ctx, servers, 2*time.Second, hint, logger)
}

func splitSTUN(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		s = DefaultSTUN
	}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func signalingCandidates(cs []punch.Candidate) []signaling.Candidate {
	out := make([]signaling.Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, signaling.Candidate{Kind: c.Kind, Addr: c.Addr, Priority: c.Priority})
	}
	return out
}

func localCandidates(cs []signaling.Candidate) []punch.Candidate {
	out := make([]punch.Candidate, 0, len(cs))
	for _, c := range cs {
		out = append(out, punch.Candidate{Kind: c.Kind, Addr: c.Addr, Priority: c.Priority})
	}
	return out
}

func describeFiles(paths []string) ([]signaling.FileInfo, int64, error) {
	out := make([]signaling.FileInfo, 0, len(paths))
	var total int64
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, 0, err
		}
		if st.IsDir() {
			return nil, 0, fmt.Errorf("%s is a directory", p)
		}
		out = append(out, signaling.FileInfo{
			Name: filepath.Base(p), Size: st.Size(),
		})
		total += st.Size()
	}
	return out, total, nil
}

func openLogger(dir string) *logging.Logger {
	f, err := os.OpenFile(filepath.Join(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return logging.New(f)
}

func openTempLogger() *logging.Logger { return nil }

func hex16(s string) [16]byte {
	var out [16]byte
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil || len(b) != len(out) {
		return out
	}
	copy(out[:], b)
	return out
}

func sessionParams(key *identity.Key, name string) session.Params {
	return session.Params{
		DeviceID:     key.ID(),
		Name:         name,
		Capabilities: proto.CapResume,
	}
}
