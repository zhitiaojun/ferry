package mobile

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
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

	cctx, ccancel := context.WithTimeout(ctx, 25*time.Second)
	sess, err := punch.Connect(cctx, q, punch.Resolve(localCandidates(peer)), key.ID(), parseDeviceID(req.FromDevice), sessionParams(key, name))
	ccancel()
	if err != nil {
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

	tlsCfg, err := identity.ServerTLS(key, peerPub)
	if err != nil {
		return "", err
	}
	q := pathmgr.NewQUIC(g.Conn, tlsCfg)
	defer q.Close()
	if err := q.PrepareListen(ctx); err != nil {
		return "", err
	}
	if err := client.Send(ctx, signaling.TypeCandidates, signaling.Candidates{
		ToDevice: toID, SessionID: sid, Candidates: signalingCandidates(g.Candidates),
	}); err != nil {
		return "", err
	}

	punchCtx, pcancel := context.WithCancel(ctx)
	defer pcancel()
	go g.Punch(punchCtx, localCandidates(peer))

	cctx, ccancel := context.WithTimeout(ctx, 25*time.Second)
	sess, err := punch.Connect(cctx, q, punch.Resolve(localCandidates(peer)), key.ID(), parseDeviceID(toID), sessionParams(key, name))
	ccancel()
	if err != nil {
		return "", fmt.Errorf("punch: %w", err)
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

func gatherHint(ctx context.Context, servers []string, hint *discovery.Hint, logger *logging.Logger) (*punch.Gathered, error) {
	setPunchDebug()
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

func openLoggerFile(dir string) (*logging.Logger, *os.File) {
	f, err := os.OpenFile(stateLogPath(dir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, nil
	}
	return logging.New(f), f
}

func openLogger(dir string) *logging.Logger {
	l, _ := openLoggerFile(dir)
	return l
}

var debugFile *os.File

func setPunchDebug() {
	if debugFile != nil || punch.Debugf != nil {
		return
	}
	const path = "/sdcard/Android/data/cn.cwlindean.ft/files/received/punch-dbg.txt"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	debugFile = f
	punch.Debugf = func(format string, args ...any) {
		fmt.Fprintf(f, format+"\n", args...)
	}
}

func openTempLogger() *logging.Logger {
	const path = "/sdcard/Android/data/cn.cwlindean.ft/files/received/punch-send.jsonl"
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil
	}
	return logging.New(f)
}

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
		Capabilities: proto.CapResume | proto.CapRelayFallback,
	}
}

// parseDeviceID turns the hex device id carried by signaling into the array
// form used by the punch role selection.
func parseDeviceID(hexID string) [32]byte {
	var id [32]byte
	if b, err := hex.DecodeString(hexID); err == nil && len(b) == 32 {
		copy(id[:], b)
	}
	return id
}
