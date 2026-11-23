package mobile

// Relay support for the Android shell: pairing helpers plus a receiver and a
// sender that go through ft.cwlindean.cn (signaling + relay, TLS 1.3 pinned).

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// DefaultSignalURL is the deployed signaling endpoint.
const DefaultSignalURL = "wss://ft.cwlindean.cn/signal"

var (
	relayMu  sync.Mutex
	relayRcv *relayReceiver
)

type relayReceiver struct {
	id     string
	cancel context.CancelFunc
	logf   *os.File
	once   sync.Once
}

// DeviceIDFor returns the hex device id stored in keyPath, creating the key on
// first use.
func DeviceIDFor(keyPath string) (string, error) {
	k, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	return k.IDHex(), nil
}

// PairURIFor returns an ftpair:// URI for this device and also writes it to
// <keyPath>.uri so it can be pulled off the phone with adb.
func PairURIFor(name, keyPath string) (string, error) {
	k, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	secret := make([]byte, 16)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	uri := identity.PairURI(k.ID(), k.Public(), name, secret)
	if err := os.WriteFile(keyPath+".uri", []byte(uri+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("write pair uri: %w", err)
	}
	return uri, nil
}

// PairImport adds a peer URI to pairedPath.
func PairImport(uri, pairedPath string) error {
	d, _, err := identity.ParsePairURI(uri)
	if err != nil {
		return err
	}
	store, err := identity.LoadPaired(pairedPath)
	if err != nil {
		return err
	}
	if err := store.Add(d); err != nil {
		return err
	}
	return store.Save()
}

// StartRelayReceiver logs into the signaling server and receives transfers
// into outDir until StopReceiver. It returns this device's id.
func StartRelayReceiver(name, outDir, signalURL, keyPath, pairedPath string) (string, error) {
	relayMu.Lock()
	defer relayMu.Unlock()
	if relayRcv != nil {
		return relayRcv.id, nil
	}
	if name == "" {
		name = "android"
	}
	if signalURL == "" {
		signalURL = DefaultSignalURL
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	logf, err := os.OpenFile(filepath.Join(outDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open log: %w", err)
	}
	logger := logging.New(logf)
	importPendingPairs(pairedPath, logger)
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		_ = logf.Close()
		return "", err
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &relayReceiver{id: key.IDHex(), cancel: cancel, logf: logf}
	relayRcv = r
	go r.loop(ctx, name, outDir, signalURL, pairedPath, key, logger)
	return r.id, nil
}

// StopRelayReceiver stops the relay receiver (StartReceiver is separate).
func StopRelayReceiver() error {
	relayMu.Lock()
	r := relayRcv
	relayRcv = nil
	relayMu.Unlock()
	if r == nil {
		return nil
	}
	r.once.Do(func() {
		r.cancel()
		_ = r.logf.Close()
	})
	return nil
}

// importPendingPairs consumes <pairedPath>.import: one ftpair:// URI per line,
// so a PC can push the file with adb and the phone picks it up on next start.
func importPendingPairs(pairedPath string, logger *logging.Logger) {
	path := pairedPath + ".import"
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if err := PairImport(line, pairedPath); err != nil {
			logger.Event("pair.import_error", map[string]any{"error": err.Error()})
			continue
		}
		logger.Event("pair.imported", map[string]any{"uri": line[:min(len(line), 24)] + "…"})
	}
	_ = os.Remove(path)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *relayReceiver) loop(ctx context.Context, name, outDir, signalURL, pairedPath string, key *identity.Key, logger *logging.Logger) {
	backoff := time.Second
	for {
		if ctx.Err() != nil {
			return
		}
		client, err := signaling.Dial(ctx, signalURL, key, logger)
		if err != nil {
			logger.Event("signal.dial_error", map[string]any{"error": err.Error()})
		} else {
			backoff = time.Second
			err = r.serve(ctx, client, key, name, outDir, pairedPath, logger)
			client.Close()
			if ctx.Err() != nil {
				return
			}
			logger.Event("signal.reconnect", map[string]any{"backoff_ms": backoff.Milliseconds(), "error": errString(err)})
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 15*time.Second {
			backoff *= 2
		}
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// serve accepts transfers until the signaling connection fails.
func (r *relayReceiver) serve(ctx context.Context, client *signaling.Client, key *identity.Key, name, outDir, pairedPath string, logger *logging.Logger) error {
	type request struct {
		from string
	}
	readyCh := make(chan signaling.RelayReady, 2)
	reqCh := make(chan request, 2)
	client.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		req, err := signaling.Decode[signaling.SendRequest](e)
		if err != nil {
			return
		}
		select {
		case reqCh <- request{from: req.FromDevice}:
		default:
		}
		logger.Event("signal.request", map[string]any{"from": truncID(req.FromDevice), "files": len(req.Files)})
		_ = client.Send(context.Background(), signaling.TypeSendResp, signaling.SendResponse{
			ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
		})
	})
	client.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		rr, err := signaling.Decode[signaling.RelayReady](e)
		if err != nil {
			return
		}
		select {
		case readyCh <- rr:
		default:
		}
	})

	for {
		var from string
		select {
		case req := <-reqCh:
			from = req.from
		case <-ctx.Done():
			return ctx.Err()
		case <-client.Done():
			return fmt.Errorf("signaling connection dropped: %v", client.Err())
		}
		var ready signaling.RelayReady
		select {
		case ready = <-readyCh:
		case <-ctx.Done():
			return ctx.Err()
		case <-client.Done():
			return fmt.Errorf("signaling connection dropped: %v", client.Err())
		}
		if err := r.receiveOne(ctx, ready, from, key, name, outDir, pairedPath, logger); err != nil {
			logger.Event("relay.error", map[string]any{"error": err.Error(), "from": truncID(from)})
		}
	}
}

func (r *relayReceiver) receiveOne(ctx context.Context, ready signaling.RelayReady, from string, key *identity.Key, name, outDir, pairedPath string, logger *logging.Logger) error {
	peer, err := pairedPeer(pairedPath, from)
	if err != nil {
		return err
	}
	rc, err := relay.Dial(ctx, relayURL(DefaultSignalURL, ready))
	if err != nil {
		return err
	}
	defer rc.Close()
	conn, err := serverTLS(ctx, rc, key, peer)
	if err != nil {
		return err
	}
	s, err := session.Accept(conn, session.Params{DeviceID: key.ID(), Name: name, Capabilities: proto.CapResume})
	if err != nil {
		return fmt.Errorf("handshake over relay: %w", err)
	}
	defer s.Close()
	res, err := transfer.Receive(s, outDir, transfer.Config{Log: logger})
	if err != nil {
		return err
	}
	logger.Event("relay.received", map[string]any{"from": truncID(from), "files": res.Files, "bytes": res.Bytes, "chunks": res.Chunks})
	return nil
}

// SendViaRelay sends files (pathsJSON is a JSON array) through the signaling
// server and the relay to toID. It returns a JSON summary.
func SendViaRelay(signalURL, keyPath, pairedPath, toID, name, pathsJSON string) (string, error) {
	res := summary{OK: false, Path: "relay"}
	if signalURL == "" {
		signalURL = DefaultSignalURL
	}
	if name == "" {
		name = "android"
	}
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return marshalSummary(res), fmt.Errorf("pathsJSON must be a JSON array of paths: %w", err)
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return marshalSummary(res), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	client, err := signaling.Dial(ctx, signalURL, key, nil)
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	defer client.Close()

	files := make([]signaling.FileInfo, 0, len(paths))
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil || st.IsDir() {
			res.Error = fmt.Sprintf("file %s is not readable", p)
			return marshalSummary(res), fmt.Errorf("%s", res.Error)
		}
		files = append(files, signaling.FileInfo{Name: filepath.Base(p), Size: st.Size()})
	}
	sessionID, err := randHex16()
	if err != nil {
		return marshalSummary(res), err
	}
	transferID, err := randHex16()
	if err != nil {
		return marshalSummary(res), err
	}
	respCh := make(chan signaling.SendResponse, 1)
	errCh := make(chan error, 1)
	var once sync.Once
	client.On(signaling.TypeSendResp, func(e *signaling.Envelope) {
		r, err := signaling.Decode[signaling.SendResponse](e)
		if err == nil {
			once.Do(func() { respCh <- r })
		}
	})
	client.On(signaling.TypeError, func(e *signaling.Envelope) {
		b, _ := signaling.Decode[signaling.ErrorBody](e)
		once.Do(func() { errCh <- fmt.Errorf("signaling: %s: %s", b.Code, b.Message) })
	})
	if err := client.Send(ctx, signaling.TypeSendRequest, signaling.SendRequest{
		ToDevice: toID, SessionID: sessionID, TransferID: transferID, Files: files,
	}); err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	var answer signaling.SendResponse
	select {
	case answer = <-respCh:
	case err := <-errCh:
		res.Error = err.Error()
		return marshalSummary(res), err
	case <-ctx.Done():
		res.Error = ctx.Err().Error()
		return marshalSummary(res), ctx.Err()
	}
	if !answer.Accept {
		res.Error = "peer rejected the transfer"
		return marshalSummary(res), fmt.Errorf("%s", res.Error)
	}

	ready, err := openRelayRoom(ctx, client, toID, transferID)
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	peer, err := pairedPeer(pairedPath, toID)
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	rc, err := relay.Dial(ctx, relayURL(signalURL, ready))
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	defer rc.Close()
	conn, err := clientTLS(ctx, rc, key, peer)
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	s, err := session.Dial(conn, session.Params{DeviceID: key.ID(), Name: name, Capabilities: proto.CapResume})
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	defer s.Close()
	tid, _ := hex.DecodeString(transferID)
	var tidArr [16]byte
	copy(tidArr[:], tid)
	out, err := transfer.Send(s, paths, transfer.Config{TransferID: tidArr})
	if err != nil {
		res.Error = err.Error()
		return marshalSummary(res), err
	}
	res.OK = true
	res.Peer = truncID(toID)
	res.TransferID = transferID
	res.Files = out.Files
	res.Bytes = out.Bytes
	res.Chunks = out.Chunks
	res.DurationMS = out.Duration.Milliseconds()
	return marshalSummary(res), nil
}

func openRelayRoom(ctx context.Context, client *signaling.Client, toID, transferID string) (signaling.RelayReady, error) {
	reply, err := client.Call(ctx, signaling.TypeRelayOpen, signaling.RelayOpen{
		ToDevice: toID, TransferID: transferID,
	}, signaling.TypeRelayReady)
	if err != nil {
		return signaling.RelayReady{}, err
	}
	r, err := signaling.Decode[signaling.RelayReady](reply)
	if err != nil {
		return signaling.RelayReady{}, err
	}
	return r, nil
}

func relayURL(signalURL string, ready signaling.RelayReady) string {
	if ready.RelayURL == "" {
		return signalURL
	}
	u := signalURL
	if i := len(u); i > 0 {
		// strip the /signal path, keep scheme://host
		if j := indexAfter(u, "://"); j > 0 {
			if k := indexFrom(u, j, '/'); k > 0 {
				u = u[:k]
			}
		}
	}
	return u + ready.RelayURL + "?token=" + ready.Token
}

func indexAfter(s, sep string) int {
	for i := 0; i+len(sep) <= len(s); i++ {
		if s[i:i+len(sep)] == sep {
			return i + len(sep)
		}
	}
	return -1
}

func indexFrom(s string, from int, b byte) int {
	for i := from; i < len(s); i++ {
		if s[i] == b {
			return i
		}
	}
	return -1
}

func pairedPeer(pairedPath, idHex string) (ed25519.PublicKey, error) {
	store, err := identity.LoadPaired(pairedPath)
	if err != nil {
		return nil, err
	}
	d, ok := store.Get(idHex)
	if !ok {
		return nil, fmt.Errorf("peer %s is not paired; import its URI first", truncID(idHex))
	}
	return d.PublicKeyBytes()
}

func clientTLS(ctx context.Context, rc net.Conn, key *identity.Key, peer ed25519.PublicKey) (net.Conn, error) {
	cfg, err := identity.ClientTLS(key, peer)
	if err != nil {
		return nil, err
	}
	tc := tls.Client(rc, cfg)
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		_ = rc.Close()
		return nil, fmt.Errorf("tls handshake with relay peer: %w", err)
	}
	return tc, nil
}

func serverTLS(ctx context.Context, rc net.Conn, key *identity.Key, peer ed25519.PublicKey) (net.Conn, error) {
	cfg, err := identity.ServerTLS(key, peer)
	if err != nil {
		return nil, err
	}
	tc := tls.Server(rc, cfg)
	hctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := tc.HandshakeContext(hctx); err != nil {
		_ = rc.Close()
		return nil, fmt.Errorf("tls handshake with relay peer: %w", err)
	}
	return tc, nil
}

func randHex16() (string, error) {
	var b [16]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func truncID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
