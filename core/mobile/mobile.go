// Package mobile is the gomobile-bindable surface of the transfer core.
//
// It is compiled into an AAR by `gomobile bind` and called from the Android
// shell (android/). The API stays small on purpose: start/stop a receiver,
// discover peers, send files. Errors are returned as Go errors, which gomobile
// surfaces as Java exceptions.
package mobile

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/pathmgr"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// Version is the core version shown in the Android UI.
const Version = "1.0.0"

type lanPeerEntry struct {
	TCPAddr  string
	LastSeen time.Time
}

var (
	mu           sync.Mutex
	rcv          *receiver
	lanPeersMu   sync.Mutex
	lanPeers     = map[string]lanPeerEntry{}
	lanListening bool
)

type receiver struct {
	name   string
	outDir string
	devID  [32]byte
	ln     *pathmgr.TCP
	lanLst *discovery.Listener
	cancel context.CancelFunc
	logger *logging.Logger
	logf   *os.File

	stopOnce sync.Once
}

// Device is one peer seen on the LAN (plain fields for gomobile).
type Device struct {
	Name  string
	Addr  string
	Iface string
}

type summary struct {
	OK         bool     `json:"ok"`
	Path       string   `json:"path"`
	Peer       string   `json:"peer,omitempty"`
	TransferID string   `json:"transfer_id,omitempty"`
	Files      []string `json:"files,omitempty"`
	Bytes      int64    `json:"bytes"`
	Chunks     int      `json:"chunks"`
	DurationMS int64    `json:"duration_ms"`
	Error      string   `json:"error,omitempty"`
}

// StartReceiver listens on all interfaces, announces on the LAN and accepts
// transfers into outDir until StopReceiver. It returns the listen address.
func StartReceiver(name, outDir, hintJSON string) (string, error) {
	return startReceiver(name, outDir, "", hintJSON)
}

// StartReceiverWithKey is like StartReceiver but uses the persistent Ed25519
// device key at keyPath so LAN peers match entries in paired.json by DeviceID.
func StartReceiverWithKey(name, outDir, keyPath, hintJSON string) (string, error) {
	return startReceiver(name, outDir, keyPath, hintJSON)
}

func startReceiver(name, outDir, keyPath, hintJSON string) (string, error) {
	mu.Lock()
	defer mu.Unlock()
	if rcv != nil {
		return rcv.ln.Addr(), nil
	}
	name = pairing.SanitizeName(name)
	if name == "" {
		name = "android"
	}
	devID := identity.DeviceID(name)
	if keyPath != "" {
		k, err := identity.LoadOrCreateKey(keyPath)
		if err != nil {
			return "", err
		}
		devID = k.ID()
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	logf, err := os.OpenFile(stateLogPath(outDir, "events.jsonl"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return "", fmt.Errorf("open log: %w", err)
	}
	logger := logging.New(logf)
	ln, err := pathmgr.NewTCPListener("0.0.0.0:0")
	if err != nil {
		_ = logf.Close()
		return "", err
	}
	port := portOf(ln.Addr())
	if port == 0 {
		_ = ln.Close()
		_ = logf.Close()
		return "", fmt.Errorf("receiver: bad listen address %q", ln.Addr())
	}

	ctx, cancel := context.WithCancel(context.Background())
	hint := parseHint(hintJSON)
	lanLst, lerr := discovery.Listen(discovery.Config{
		DeviceID: devID,
		Name:     name,
		Log:      logger,
		Hint:     hint,
	})
	if lerr != nil {
		logger.Event("discovery.listen.error", map[string]any{"error": lerr.Error()})
	}
	r := &receiver{name: name, outDir: outDir, devID: devID, ln: ln, lanLst: lanLst, cancel: cancel, logger: logger, logf: logf}
	rcv = r
	go func() {
		err := discovery.Announce(ctx, discovery.Config{
			DeviceID: devID,
			Name:     name,
			Port:     port,
			Log:      logger,
			Hint:     hint,
		})
		if err != nil {
			logger.Event("discovery.announce.error", map[string]any{"error": err.Error()})
		}
	}()
	if lanLst != nil {
		lanPeersMu.Lock()
		lanListening = true
		lanPeersMu.Unlock()
		go func() {
			if err := lanLst.Run(ctx); err != nil && ctx.Err() == nil {
				logger.Event("discovery.listen.run_error", map[string]any{"error": err.Error()})
			}
		}()
		go syncLANPeers(ctx, lanLst)
	}
	go r.acceptLoop(ctx)
	return ln.Addr(), nil
}

func syncLANPeers(ctx context.Context, lst *discovery.Listener) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		devs := lst.Devices()
		now := time.Now()
		lanPeersMu.Lock()
		for id, entry := range lanPeers {
			if now.Sub(entry.LastSeen) > discovery.DeviceTTL {
				delete(lanPeers, id)
			}
		}
		for _, d := range devs {
			if d.TCPAddr == "" {
				continue
			}
			idHex := hex.EncodeToString(d.DeviceID[:])
			lanPeers[idHex] = lanPeerEntry{TCPAddr: d.TCPAddr, LastSeen: d.LastSeen}
		}
		lanPeersMu.Unlock()

		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func lanPeerAddr(idHex string) string {
	idHex = strings.TrimSpace(idHex)
	if idHex == "" {
		return ""
	}
	lanPeersMu.Lock()
	defer lanPeersMu.Unlock()
	e, ok := lanPeers[idHex]
	if !ok || time.Since(e.LastSeen) > discovery.DeviceTTL {
		return ""
	}
	return e.TCPAddr
}

func snapshotLANPeers() (bool, map[string]string) {
	lanPeersMu.Lock()
	defer lanPeersMu.Unlock()
	now := time.Now()
	out := make(map[string]string, len(lanPeers))
	for id, e := range lanPeers {
		if now.Sub(e.LastSeen) <= discovery.DeviceTTL && e.TCPAddr != "" {
			out[id] = e.TCPAddr
		}
	}
	return lanListening, out
}

// StopReceiver stops listening and announcing.
func StopReceiver() error {
	mu.Lock()
	r := rcv
	rcv = nil
	mu.Unlock()
	if r == nil {
		return nil
	}
	r.stop()
	return nil
}

func (r *receiver) stop() {
	r.stopOnce.Do(func() {
		r.cancel()
		_ = r.ln.Close()
		if r.lanLst != nil {
			_ = r.lanLst.Close()
		}
		lanPeersMu.Lock()
		lanListening = false
		lanPeers = map[string]lanPeerEntry{}
		lanPeersMu.Unlock()
		_ = r.logf.Close()
	})
}

func (r *receiver) acceptLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		acceptCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		nc, err := r.ln.Accept(acceptCtx)
		cancel()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
			}
			continue
		}
		go r.handle(ctx, nc)
	}
}

func (r *receiver) handle(ctx context.Context, nc net.Conn) {
	defer nc.Close()
	s, err := session.Accept(nc, session.Params{
		DeviceID:     r.devID,
		Name:         r.name,
		Capabilities: proto.CapResume,
	})
	if err != nil {
		r.logger.Event("session.error", map[string]any{"stage": "handshake", "error": err.Error()})
		return
	}
	defer s.Close()
	peerName := pairing.SanitizeName(s.Peer.Name)
	if peerName == "" {
		peerName = "局域网设备"
	}
	txSeq := beginActiveTransfer("recv", peerName, "", 0, 0)
	defer endActiveTransfer(txSeq)
	updateActiveTransfer(txSeq, "transferring", "lan", "", "", 0, 0, -1, 0)

	r.logger.Event("session.ready", map[string]any{"peer": s.Peer.Name, "path": "lan"})
	res, err := transfer.Receive(s, r.outDir, transfer.Config{
		Log: r.logger,
		OnDetailProgress: func(snap transfer.ProgressSnapshot) {
			updateActiveTransfer(txSeq, snap.Stage, "lan", "", snap.FileName, snap.FileIndex, snap.FileCount, snap.BytesDone, snap.BytesTotal)
		},
	})
	if err != nil {
		r.logger.Event("transfer.error", map[string]any{"error": err.Error()})
		return
	}
	recordReceivedFrom(r.outDir, "lan", pairing.SanitizeName(s.Peer.Name), res.Files, res.Bytes)
}

// Discover waits timeoutMS for LAN announcements and returns a JSON array of
// devices (name, addr, iface). It announces this device's name too.
func Discover(name string, timeoutMS int, hintJSON string) (string, error) {
	name = pairing.SanitizeName(name)
	if name == "" {
		name = "android"
	}
	if timeoutMS <= 0 {
		timeoutMS = 3000
	}
	timeout := time.Duration(timeoutMS) * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), timeout+time.Second)
	defer cancel()
	hint := parseHint(hintJSON)
	l, err := discovery.Listen(discovery.Config{
		DeviceID: identity.DeviceID(name),
		Name:     name,
		Hint:     hint,
	})
	if err != nil {
		return "", err
	}
	defer l.Close()
	go func() { _ = l.Run(ctx) }()

	announceCtx, stopAnnounce := context.WithCancel(ctx)
	defer stopAnnounce()
	go func() {
		_ = discovery.Announce(announceCtx, discovery.Config{
			DeviceID: identity.DeviceID(name),
			Name:     name,
			Port:     1, // discovery-only: no TCP service in this call
			Hint:     hint,
		})
	}()

	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		time.Sleep(100 * time.Millisecond)
	}
	devices := make([]Device, 0, 8)
	for _, d := range l.Devices() {
		devices = append(devices, Device{Name: d.Name, Addr: d.TCPAddr, Iface: d.Iface})
	}
	b, err := json.Marshal(devices)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// SendFiles sends the files named in pathsJSON (a JSON array of absolute
// paths) to peerAddr and returns a JSON summary. gomobile cannot bind
// []string, so the list crosses the bridge as JSON.
func SendFiles(peerAddr, name, pathsJSON string) (string, error) {
	res := summary{OK: false, Path: "lan"}
	name = pairing.SanitizeName(name)
	if name == "" {
		name = "android"
	}
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		res.Error = "pathsJSON must be a JSON array of paths: " + err.Error()
		return marshalSummary(res), fmt.Errorf("%s", res.Error)
	}
	if peerAddr == "" || len(paths) == 0 {
		res.Error = "peerAddr and at least one path are required"
		return marshalSummary(res), fmt.Errorf("%s", res.Error)
	}
	var totalBytes int64
	for _, p := range paths {
		if st, serr := os.Stat(p); serr == nil {
			totalBytes += st.Size()
		}
	}
	txSeq := beginActiveTransfer("send", peerAddr, filepath.Base(paths[0]), len(paths), totalBytes)
	defer endActiveTransfer(txSeq)

	res, err := sendFilesLAN(context.Background(), peerAddr, name, identity.DeviceID(name), paths, 10*time.Second, txSeq, nil)
	return marshalSummary(res), err
}

func sendFilesLAN(ctx context.Context, peerAddr, name string, devID [32]byte, paths []string, dialTimeout time.Duration, txSeq uint64, logger *logging.Logger) (summary, error) {
	res := summary{OK: false, Path: "lan"}
	d := pathmgr.NewTCPDial(peerAddr)
	dctx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()
	nc, err := d.Dial(dctx)
	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	s, err := session.Dial(nc, session.Params{
		DeviceID:     devID,
		Name:         name,
		Capabilities: proto.CapResume,
	})
	if err != nil {
		_ = nc.Close()
		res.Error = err.Error()
		return res, err
	}
	defer s.Close()
	updateActiveTransfer(txSeq, "transferring", "lan", pairing.SanitizeName(s.Peer.Name), "", 0, 0, -1, 0)
	out, err := transfer.Send(s, paths, transfer.Config{
		Log: logger,
		OnDetailProgress: func(snap transfer.ProgressSnapshot) {
			updateActiveTransfer(txSeq, snap.Stage, "lan", "", snap.FileName, snap.FileIndex, snap.FileCount, snap.BytesDone, snap.BytesTotal)
		},
	})
	if err != nil {
		res.Error = err.Error()
		return res, err
	}
	res.OK = true
	res.Peer = s.Peer.Name
	res.TransferID = fmt.Sprintf("%x", out.TransferID)
	res.Files = out.Files
	res.Bytes = out.Bytes
	res.Chunks = out.Chunks
	res.DurationMS = out.Duration.Milliseconds()
	return res, nil
}

// Gateway returns the default gateway as text (diagnostics for hotspot mode),
// or an empty string when it cannot be determined.
func Gateway() string {
	if ip := discovery.GatewayIPv4(); ip != nil {
		return ip.String()
	}
	return ""
}

func marshalSummary(s summary) string {
	b, err := json.Marshal(s)
	if err != nil {
		return `{"ok":false,"error":"summary marshal failed"}`
	}
	return string(b)
}

// parseHint decodes the JSON interface hint Java passes in; an empty string
// means "enumerate normally".
func parseHint(s string) *discovery.Hint {
	s = strings.TrimSpace(s)
	if s == "" || s == "null" {
		return nil
	}
	var h discovery.Hint
	if err := json.Unmarshal([]byte(s), &h); err != nil || h.IP == "" {
		return nil
	}
	return &h
}

func portOf(addr string) int {
	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 0
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		return 0
	}
	return n
}
