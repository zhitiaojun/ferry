package mobile

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/zhitiaojun/ferry/core/client"
	"github.com/zhitiaojun/ferry/core/discovery"
	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/logging"
	"github.com/zhitiaojun/ferry/core/pairing"
	"github.com/zhitiaojun/ferry/core/signaling"
	"github.com/zhitiaojun/ferry/core/transfer"
)

type autoReceiver struct {
	cancel      context.CancelFunc
	done        chan struct{}
	reconnectCh chan struct{}

	mu            sync.Mutex
	attemptCancel context.CancelFunc
}

var (
	autoMu  sync.Mutex
	autoRcv *autoReceiver

	sigMu          sync.Mutex
	sigState       = "offline" // "offline" | "connecting" | "online"
	sigClient      *signaling.Client
	sigOnlinePeers = map[string]bool{}
	autoHoldCount  int
	autoResumeCh   = make(chan struct{})
	screenAwake    = true
	netAvailable   = true
	dynamicHint    *discovery.Hint
	wakeCh         = make(chan struct{})

	inFlightCount atomic.Int32

	candRouteMu sync.Mutex
	candRoutes  = map[string]chan *signaling.Envelope{}

	readyRouteMu sync.Mutex
	readyRoute   chan signaling.RelayReady
)

// StartAutoReceive starts the persistent background receiver over signaling.
// It delegates per-transfer path racing, on-demand STUN gathering, birthday
// hot-upgrade, and slow-punch fallback to core/client.ReceiveAuto.
func StartAutoReceive(name, outDir, signalURL, keyPath, pairedPath, hintJSON, stunList string) (string, error) {
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	name = pairing.SanitizeName(name)
	if name == "" {
		name = "android"
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", fmt.Errorf("create output dir: %w", err)
	}
	if strings.TrimSpace(signalURL) == "" {
		signalURL = DefaultSignalURL
	}

	autoMu.Lock()
	if autoRcv != nil {
		autoMu.Unlock()
		UpdateNetworkHint(hintJSON)
		return key.IDHex(), nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := &autoReceiver{
		cancel:      cancel,
		done:        make(chan struct{}),
		reconnectCh: make(chan struct{}, 1),
	}
	autoRcv = r
	autoMu.Unlock()

	sigMu.Lock()
	sigState = "connecting"
	sigClient = nil
	sigOnlinePeers = map[string]bool{}
	dynamicHint = parseHint(hintJSON)
	sigMu.Unlock()
	notifyConnectionChanged()

	servers := splitSTUN(stunList)
	logger, logCloser := openRotatingLoggerFile(outDir)

	go func() {
		defer close(r.done)
		if logCloser != nil {
			defer func() { _ = logCloser.Close() }()
		}
		defer func() {
			sigMu.Lock()
			sigState = "offline"
			sigClient = nil
			sigOnlinePeers = map[string]bool{}
			sigMu.Unlock()
			notifyConnectionChanged()
		}()

		backoff := 2 * time.Second
		for ctx.Err() == nil {
			if !waitUntilActive(ctx) {
				return
			}

			actx, acancel := context.WithCancel(ctx)
			r.mu.Lock()
			r.attemptCancel = acancel
			r.mu.Unlock()

			wasOnline, err := autoServe(actx, ctx, key, name, outDir, signalURL, pairedPath, servers, logger)

			acancel()
			r.mu.Lock()
			r.attemptCancel = nil
			r.mu.Unlock()

			if ctx.Err() != nil {
				return
			}
			if err != nil && logger != nil {
				logger.Event("auto.recv_error", map[string]any{"error": err.Error()})
			}

			if !canConnectNow() {
				continue
			}
			if wasOnline {
				backoff = 2 * time.Second
			} else if backoff < 60*time.Second {
				backoff *= 2
				if backoff > 60*time.Second {
					backoff = 60 * time.Second
				}
			}

			select {
			case <-ctx.Done():
				return
			case <-r.reconnectCh:
				backoff = 2 * time.Second
			case <-time.After(backoff):
			}
		}
	}()
	return key.IDHex(), nil
}

func canConnectNow() bool {
	sigMu.Lock()
	ok := autoHoldCount == 0 && screenAwake && netAvailable
	sigMu.Unlock()
	return ok
}

func waitUntilActive(ctx context.Context) bool {
	for {
		sigMu.Lock()
		held := autoHoldCount > 0
		resumeCh := autoResumeCh
		awake := screenAwake
		netOK := netAvailable
		wch := wakeCh
		if !held && awake && netOK {
			sigMu.Unlock()
			return true
		}
		sigState = "offline"
		sigMu.Unlock()
		notifyConnectionChanged()

		select {
		case <-ctx.Done():
			return false
		case <-resumeCh:
		case <-wch:
		}
	}
}

func pulseWake() {
	sigMu.Lock()
	if wakeCh != nil {
		close(wakeCh)
		wakeCh = make(chan struct{})
	}
	sigMu.Unlock()
	autoMu.Lock()
	r := autoRcv
	autoMu.Unlock()
	if r != nil {
		select {
		case r.reconnectCh <- struct{}{}:
		default:
		}
	}
}

func cancelActiveAttempt() {
	autoMu.Lock()
	r := autoRcv
	autoMu.Unlock()
	if r != nil {
		r.mu.Lock()
		ac := r.attemptCancel
		r.mu.Unlock()
		if ac != nil {
			ac()
		}
	}
}

// UpdateNetworkHint updates the Wi-Fi / LAN interface hint without recreating
// the receiver; if a signaling loop is active and awake, it nudges health check.
func UpdateNetworkHint(hintJSON string) {
	h := parseHint(hintJSON)
	sigMu.Lock()
	dynamicHint = h
	sigMu.Unlock()
}

func currentHint() *discovery.Hint {
	sigMu.Lock()
	h := dynamicHint
	sigMu.Unlock()
	return h
}

// SetScreenAwake suspends background signaling/listening when the phone locks
// (unless a transfer is currently active, in which case it suspends once done)
// and reconnects immediately when the screen wakes.
func SetScreenAwake(awake bool) {
	sigMu.Lock()
	prev := screenAwake
	screenAwake = awake
	sigMu.Unlock()
	if prev == awake {
		return
	}
	if awake {
		pulseWake()
		notifyConnectionChanged()
		return
	}
	if inFlightCount.Load() == 0 {
		cancelActiveAttempt()
	}
	notifyConnectionChanged()
}

// SetNetworkAvailable pauses background reconnect loops when Android reports no
// usable network, and wakes them immediately on network restoration.
func SetNetworkAvailable(available bool) {
	sigMu.Lock()
	prev := netAvailable
	netAvailable = available
	sigMu.Unlock()
	if prev == available {
		return
	}
	if available {
		pulseWake()
		notifyConnectionChanged()
		return
	}
	cancelActiveAttempt()
	notifyConnectionChanged()
}

// ReconnectAutoReceive checks whether the active signaling connection is dead or
// stale and cancels the current attempt so StartAutoReceive redials at once.
func ReconnectAutoReceive() {
	if !canConnectNow() {
		return
	}
	autoMu.Lock()
	r := autoRcv
	autoMu.Unlock()
	if r == nil {
		return
	}
	sigMu.Lock()
	c := sigClient
	st := sigState
	sigMu.Unlock()

	trigger := func() {
		select {
		case r.reconnectCh <- struct{}{}:
		default:
		}
		r.mu.Lock()
		ac := r.attemptCancel
		r.mu.Unlock()
		if ac != nil {
			ac()
		}
	}

	if c == nil || !c.Alive() || st != "online" {
		trigger()
		return
	}
	lp := c.LastPong()
	if lp.IsZero() || time.Since(lp) > 25*time.Second {
		trigger()
		return
	}
	if time.Since(lp) > 5*time.Second {
		go func(cl *signaling.Client) {
			pctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			if _, err := cl.Ping(pctx); err != nil {
				_ = cl.Close()
				trigger()
			}
		}(c)
	}
}

// ConnectionStatus returns a JSON snapshot of real signaling and LAN state.
func ConnectionStatus() string {
	sigMu.Lock()
	st := sigState
	c := sigClient
	awake := screenAwake
	netOK := netAvailable
	peers := make([]string, 0, len(sigOnlinePeers))
	for id, ok := range sigOnlinePeers {
		if ok {
			peers = append(peers, id)
		}
	}
	sigMu.Unlock()

	var lastPongMS int64
	if st == "online" {
		if c == nil || !c.Alive() {
			st = "connecting"
		} else if lp := c.LastPong(); !lp.IsZero() {
			lastPongMS = lp.UnixMilli()
			if time.Since(lp) > 45*time.Second {
				st = "connecting"
			}
		}
	}
	lanLst, lanMap := snapshotLANPeers()
	b, _ := json.Marshal(map[string]any{
		"signal_state":      st,
		"last_pong_ms":      lastPongMS,
		"lan_listening":     lanLst,
		"online_peers":      peers,
		"lan_peers":         lanMap,
		"screen_awake":      awake,
		"network_available": netOK,
	})
	return string(b)
}

func holdAutoSignal() func() {
	sigMu.Lock()
	autoHoldCount++
	if autoHoldCount == 1 {
		autoResumeCh = make(chan struct{})
	}
	sigMu.Unlock()

	cancelActiveAttempt()

	var once sync.Once
	return func() {
		once.Do(func() {
			sigMu.Lock()
			if autoHoldCount > 0 {
				autoHoldCount--
				if autoHoldCount == 0 && autoResumeCh != nil {
					close(autoResumeCh)
				}
			}
			sigMu.Unlock()
			autoMu.Lock()
			ar := autoRcv
			autoMu.Unlock()
			if ar != nil {
				select {
				case ar.reconnectCh <- struct{}{}:
				default:
				}
			}
		})
	}
}

func acquireSignalForPair(ctx context.Context, signalURL string, key *identity.Key) (*signaling.Client, func(), error) {
	cl, _, release, err := acquireSignalClient(ctx, signalURL, key, nil)
	return cl, release, err
}

func acquireSignalClient(ctx context.Context, signalURL string, key *identity.Key, logger *logging.Logger) (*signaling.Client, bool, func(), error) {
	sigMu.Lock()
	c := sigClient
	st := sigState
	sigMu.Unlock()
	if c != nil && c.Alive() && st == "online" {
		return c, true, func() {}, nil
	}
	release := holdAutoSignal()
	cl, err := signaling.Dial(ctx, signalURL, key, logger)
	if err != nil {
		release()
		return nil, false, nil, err
	}
	return cl, false, func() {
		_ = cl.Close()
		release()
	}, nil
}

func registerCandRoute(sid string, ch chan *signaling.Envelope) {
	if sid == "" || ch == nil {
		return
	}
	candRouteMu.Lock()
	candRoutes[sid] = ch
	candRouteMu.Unlock()
}

func unregisterCandRoute(sid string) {
	if sid == "" {
		return
	}
	candRouteMu.Lock()
	delete(candRoutes, sid)
	candRouteMu.Unlock()
}

func dispatchCandEnvelope(e *signaling.Envelope) {
	c, err := signaling.Decode[signaling.Candidates](e)
	if err != nil || c.SessionID == "" {
		return
	}
	candRouteMu.Lock()
	ch := candRoutes[c.SessionID]
	candRouteMu.Unlock()
	if ch != nil {
		select {
		case ch <- e:
		default:
		}
	}
}

func setReadyRoute(ch chan signaling.RelayReady) {
	readyRouteMu.Lock()
	readyRoute = ch
	readyRouteMu.Unlock()
}

func clearReadyRoute(ch chan signaling.RelayReady) {
	readyRouteMu.Lock()
	if readyRoute == ch {
		readyRoute = nil
	}
	readyRouteMu.Unlock()
}

func dispatchReady(rr signaling.RelayReady) {
	readyRouteMu.Lock()
	ch := readyRoute
	readyRouteMu.Unlock()
	if ch != nil {
		select {
		case ch <- rr:
		default:
		}
	}
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

func autoServe(ctx, rootCtx context.Context, key *identity.Key, name, outDir, signalURL, pairedPath string, servers []string, logger *logging.Logger) (bool, error) {
	sigMu.Lock()
	if rootCtx.Err() == nil && canConnectNowLocked() {
		sigState = "connecting"
	}
	sigMu.Unlock()
	notifyConnectionChanged()

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	cl, err := signaling.Dial(dialCtx, signalURL, key, logger)
	dialCancel()
	if err != nil {
		return false, err
	}

	defer func() {
		sigMu.Lock()
		if sigClient == cl {
			sigClient = nil
			sigOnlinePeers = map[string]bool{}
			if rootCtx.Err() != nil || !canConnectNowLocked() {
				sigState = "offline"
			} else {
				sigState = "connecting"
			}
		}
		sigMu.Unlock()
		notifyConnectionChanged()
		_ = cl.Close()
	}()

	sigMu.Lock()
	sigClient = cl
	sigState = "online"
	sigOnlinePeers = map[string]bool{}
	for _, p := range cl.InitialPeers() {
		if p.Online && p.DeviceID != "" {
			sigOnlinePeers[p.DeviceID] = true
		}
	}
	sigMu.Unlock()
	notifyConnectionChanged()

	type offerItem struct {
		req       signaling.SendRequest
		peerPub   ed25519.PublicKey
		peerName  string
		candCh    chan *signaling.Envelope
		unregCand func()
	}
	offerCh := make(chan offerItem, 4)

	cl.On(signaling.TypePresence, func(e *signaling.Envelope) {
		p, perr := signaling.Decode[signaling.Presence](e)
		if perr != nil || p.DeviceID == "" {
			return
		}
		sigMu.Lock()
		if sigClient == cl {
			if p.Online {
				sigOnlinePeers[p.DeviceID] = true
			} else {
				delete(sigOnlinePeers, p.DeviceID)
			}
		}
		sigMu.Unlock()
		notifyConnectionChanged()
	})

	cl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		req, rerr := signaling.Decode[signaling.SendRequest](e)
		if rerr != nil {
			return
		}
		pub, perr := client.PairedPeer(pairedPath, req.FromDevice)
		if perr != nil {
			_ = cl.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "unauthorized", Message: perr.Error()})
			return
		}
		peerName := truncID(req.FromDevice)
		if store, serr := identity.LoadPaired(pairedPath); serr == nil {
			if dev, ok := store.Get(req.FromDevice); ok && dev.Name != "" {
				peerName = dev.Name
			}
		}
		candCh := make(chan *signaling.Envelope, 8)
		registerCandRoute(req.SessionID, candCh)
		unreg := func() { unregisterCandRoute(req.SessionID) }
		select {
		case offerCh <- offerItem{req: req, peerPub: pub, peerName: peerName, candCh: candCh, unregCand: unreg}:
		default:
			unreg()
		}
	})

	cl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		dispatchCandEnvelope(e)
	})

	cl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		if rr, rerr := signaling.Decode[signaling.RelayReady](e); rerr == nil {
			dispatchReady(rr)
		}
	})

	if logger != nil {
		logger.Event("auto.ready", map[string]any{"device_id": truncID(key.IDHex())})
	}

	for {
		select {
		case <-ctx.Done():
			return true, nil
		case <-cl.Done():
			return true, fmt.Errorf("signaling dropped: %v", cl.Err())
		case off := <-offerCh:
			readyCh := make(chan signaling.RelayReady, 2)
			setReadyRoute(readyCh)
			receiveOneMobileAuto(rootCtx, cl, key, name, outDir, signalURL, pairedPath, servers, off.req, off.peerPub, off.peerName, off.candCh, readyCh, logger)
			clearReadyRoute(readyCh)
			off.unregCand()

			if !canConnectNow() {
				return true, nil
			}
		}
	}
}

func canConnectNowLocked() bool {
	return autoHoldCount == 0 && screenAwake && netAvailable
}

func receiveOneMobileAuto(
	rootCtx context.Context,
	cl *signaling.Client,
	key *identity.Key,
	name, outDir, signalURL, pairedPath string,
	servers []string,
	req signaling.SendRequest,
	peerPub ed25519.PublicKey,
	peerName string,
	candCh <-chan *signaling.Envelope,
	readyCh <-chan signaling.RelayReady,
	logger *logging.Logger,
) {
	inFlightCount.Add(1)
	defer inFlightCount.Add(-1)

	var initTotal int64
	firstFile := ""
	fileTitle := "接收文件"
	if len(req.Files) > 0 {
		firstFile = req.Files[0].Name
		if len(req.Files) == 1 {
			fileTitle = firstFile
		} else {
			fileTitle = fmt.Sprintf("%s 等 %d 个文件", firstFile, len(req.Files))
		}
		for _, f := range req.Files {
			initTotal += f.Size
		}
	}
	txSeq := beginActiveTransfer("recv", peerName, firstFile, len(req.Files), initTotal)
	ctrl := transfer.NewController()
	attachActiveTransferController(txSeq, ctrl, true, true)
	defer endActiveTransfer(txSeq)

	rctx, cancel := context.WithTimeout(rootCtx, 10*time.Minute)
	defer cancel()

	var viaMu sync.Mutex
	lastVia := ""
	res, err := client.ReceiveAuto(rctx, client.ReceiveOptions{
		Key:         key,
		SignalURL:   signalURL,
		OutDir:      outDir,
		PairedPath:  pairedPath,
		Name:        name,
		STUNServers: servers,
		LANHint:     currentHint(),
		SkipLANEnum: true,
		Control:     ctrl,
		Log:         logger,
		OnStatus: func(st client.Status) {
			if st.Channel != "" {
				viaMu.Lock()
				lastVia = st.Channel
				viaMu.Unlock()
			}
			updateActiveTransfer(txSeq, st.Stage, st.Channel, "", st.FileName, st.FileIndex, st.FileCount, st.BytesDone, st.BytesTotal)
		},
	}, cl, req, peerPub, candCh, readyCh)
	if err != nil {
		viaMu.Lock()
		via := lastVia
		viaMu.Unlock()
		switch {
		case ctrl.IsCancelled():
			if logger != nil {
				logger.Event("auto.recv_cancelled", map[string]any{"peer": truncID(req.FromDevice)})
			}
			recordReceiveOutcome(via, peerName, fileTitle, initTotal, "cancelled", "已取消接收")
		case transfer.IsCancelled(err):
			if logger != nil {
				logger.Event("auto.recv_peer_cancelled", map[string]any{"peer": truncID(req.FromDevice)})
			}
			recordReceiveOutcome(via, peerName, fileTitle, initTotal, "peer_cancelled", "对方已取消传输")
		default:
			if logger != nil {
				logger.Event("auto.recv_transfer_error", map[string]any{"peer": truncID(req.FromDevice), "error": err.Error()})
			}
			recordReceiveOutcome(via, peerName, fileTitle, initTotal, "failed", err.Error())
		}
		return
	}
	if res.PeerName != "" {
		peerName = res.PeerName
		updateActiveTransfer(txSeq, "", "", peerName, "", 0, 0, -1, 0)
	}
	via := channelCode(res.Channel)
	recordReceivedFrom(outDir, via, peerName, res.Files, res.Bytes)
	if logger != nil {
		logger.Event("auto.done", map[string]any{"path": via, "peer": truncID(req.FromDevice)})
	}
}

// SendAuto is the one-button sender: checks LAN discovery first, then delegates
// punch+relay racing, birthday hot-upgrade, and slow-punch fallback to core/client.
func SendAuto(signalURL, keyPath, pairedPath, toID, name, pathsJSON, hintJSON, stunList string) (string, error) {
	var paths []string
	if err := json.Unmarshal([]byte(pathsJSON), &paths); err != nil {
		return "", fmt.Errorf("paths: %w", err)
	}
	if len(paths) == 0 {
		return "", fmt.Errorf("no files")
	}
	name = pairing.SanitizeName(name)
	if name == "" {
		name = "android"
	}
	if strings.TrimSpace(signalURL) == "" {
		signalURL = DefaultSignalURL
	}
	key, err := identity.LoadOrCreateKey(keyPath)
	if err != nil {
		return "", err
	}
	if _, err := pairedPeer(pairedPath, toID); err != nil {
		return "", err
	}
	logger := openTempLogger()

	inFlightCount.Add(1)
	defer inFlightCount.Add(-1)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	files, totalBytes, err := describeFiles(paths)
	if err != nil {
		return "", err
	}
	targetLabel := truncID(toID)
	if store, serr := identity.LoadPaired(pairedPath); serr == nil {
		if dev, ok := store.Get(toID); ok && dev.Name != "" {
			targetLabel = dev.Name
		}
	}
	firstFile := ""
	if len(files) > 0 {
		firstFile = files[0].Name
	}
	txSeq := beginActiveTransfer("send", targetLabel, firstFile, len(files), totalBytes)
	ctrl := transfer.NewController()
	attachActiveTransferController(txSeq, ctrl, true, true)
	defer endActiveTransfer(txSeq)

	if lanAddr := lanPeerAddr(toID); lanAddr != "" {
		res, lerr := sendFilesLAN(ctx, lanAddr, name, key.ID(), key, paths, 3*time.Second, txSeq, ctrl, logger)
		if lerr == nil && res.OK {
			if peerName := pairing.SanitizeName(res.Peer); peerName != "" {
				identity.UpdatePeerName(pairedPath, toID, peerName)
			}
			out, _ := json.Marshal(map[string]any{
				"ok": true, "path": "lan", "peer": truncID(toID), "transfer_id": res.TransferID,
				"files": res.Files, "bytes": res.Bytes, "chunks": res.Chunks,
				"duration_ms": res.DurationMS,
			})
			return string(out), nil
		}
		if ctrl.IsCancelled() {
			return "", errors.New("已取消传输")
		}
		if transfer.IsCancelled(lerr) {
			return "", errors.New("对方已取消传输")
		}
		if logger != nil && lerr != nil {
			logger.Event("send.lan_fallback", map[string]any{"peer": truncID(toID), "addr": lanAddr, "error": lerr.Error()})
		}
	}

	sigCl, reused, releaseSig, err := acquireSignalClient(ctx, signalURL, key, logger)
	if err != nil {
		return "", err
	}
	defer releaseSig()

	var hint *discovery.Hint
	if strings.TrimSpace(hintJSON) != "" {
		hint = parseHint(hintJSON)
	} else {
		hint = currentHint()
	}

	opts := client.Options{
		SignalURL:   signalURL,
		Key:         key,
		KeyPath:     keyPath,
		PairedPath:  pairedPath,
		Name:        name,
		STUNServers: splitSTUN(stunList),
		LANHint:     hint,
		SkipLANEnum: true,
		Control:     ctrl,
		Log:         logger,
		OnStatus: func(st client.Status) {
			updateActiveTransfer(txSeq, st.Stage, st.Channel, "", st.FileName, st.FileIndex, st.FileCount, st.BytesDone, st.BytesTotal)
		},
	}
	if reused {
		opts.SignalClient = sigCl
		opts.CandRoute = func(sid string, ch chan *signaling.Envelope) func() {
			registerCandRoute(sid, ch)
			return func() { unregisterCandRoute(sid) }
		}
	}

	res, err := client.SendAuto(ctx, opts, toID, paths)
	if err != nil {
		if ctrl.IsCancelled() {
			return "", errors.New("已取消传输")
		}
		if transfer.IsCancelled(err) {
			return "", errors.New("对方已取消传输")
		}
		return "", err
	}
	out, _ := json.Marshal(map[string]any{
		"ok": true, "path": channelCode(res.Channel), "peer": truncID(toID), "transfer_id": res.TransferID,
		"files": res.Files, "bytes": res.Bytes, "chunks": res.Chunks,
		"duration_ms": res.Duration.Milliseconds(),
	})
	return string(out), nil
}

func channelCode(ch string) string {
	switch ch {
	case "LAN":
		return "lan"
	case "打洞":
		return "punch"
	default:
		return "relay"
	}
}
