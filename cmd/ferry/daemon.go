package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/ipc"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

type daemonState struct {
	key        *identity.Key
	keyPath    string
	pairedPath string
	outDir     string
	signalURL  string
	name       string
	stun       []string
	port       int
	log        *logging.Logger

	mu           sync.Mutex
	sigState     string
	sigClient    *signaling.Client
	sigHoldCount int
	sigResumeCh  chan struct{}
	sigStopCur   context.CancelFunc
	cloudPeers   map[string]bool
	lanPeers     map[string]daemonLANPeer
	candRoutes   map[string]chan *signaling.Envelope
	readyRoute   chan signaling.RelayReady
}

type daemonLANPeer struct {
	name     string
	addr     string
	iface    string
	lastSeen time.Time
}

func runDaemon(args []string) int {
	fs := flag.NewFlagSet("daemon", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	port := fs.Int("port", 18800, "LAN TCP listen port")
	dir := fs.String("dir", mcpDefaultOutDir(), "output directory for received files")
	keyPath := fs.String("key", defaultKeyPath(), "device key file")
	pairedPath := fs.String("paired", defaultPairedPath(), "trusted peer table")
	signalURL := fs.String("signal", client.DefaultSignalURL, "signaling server WebSocket URL")
	stun := fs.String("stun", defaultSTUN, "comma-separated STUN servers")
	name := fs.String("name", defaultName(), "device name shown to peers")
	logPath := fs.String("log", filepath.Join(client.DefaultStateDir(), "events.jsonl"), "JSONL event log path")
	if err := fs.Parse(args); err != nil {
		return 2
	}

	if err := os.MkdirAll(*dir, 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "ferry daemon: create dir: %v\n", err)
		return 1
	}
	key, err := loadDeviceKey(*keyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry daemon: %v\n", err)
		return 1
	}
	logger, logCloser, err := logging.OpenRotating(*logPath, logging.DefaultMaxBytes)
	if err != nil {
		logger = logging.New(os.Stderr)
	} else {
		defer logCloser.Close()
	}

	ln, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%d", *port))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry daemon: listen port %d: %v\n", *port, err)
		return 1
	}
	defer ln.Close()
	actualPort := ln.Addr().(*net.TCPAddr).Port

	d := &daemonState{
		key:        key,
		keyPath:    *keyPath,
		pairedPath: *pairedPath,
		outDir:     *dir,
		signalURL:  strings.TrimSpace(*signalURL),
		name:       pairing.SanitizeName(*name),
		stun:       splitCSV(*stun),
		port:       actualPort,
		log:        logger,
		sigState:   "offline",
	}
	if d.name == "" {
		d.name = "ferry-device"
	}

	stateDir := filepath.Dir(*keyPath)
	ipcSrv, err := ipc.Start(stateDir, d, ipc.StartOptions{
		KeyPath:    *keyPath,
		PairedPath: *pairedPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "ferry daemon: start ipc: %v\n", err)
		return 1
	}
	defer ipcSrv.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go d.acceptLANLoop(ctx, ln)
	go d.runLANDiscovery(ctx)
	if d.signalURL != "" {
		go d.runSignalingLoop(ctx)
	}

	logger.Event("daemon.ready", map[string]any{
		"device_id": truncHex(key.IDHex()),
		"lan_port":  actualPort,
		"ipc":       ipcSrv.Addr(),
		"out_dir":   *dir,
	})
	fmt.Fprintf(os.Stderr, "ferry daemon listening on LAN :%d (ipc %s, id %s)\n", actualPort, ipcSrv.Addr(), truncHex(key.IDHex()))
	<-ctx.Done()
	return 0
}

func (d *daemonState) acceptLANLoop(ctx context.Context, ln net.Listener) {
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	for {
		nc, err := ln.Accept()
		if err != nil {
			return
		}
		go d.handleLANConn(nc)
	}
}

func (d *daemonState) handleLANConn(nc net.Conn) {
	defer nc.Close()
	s, err := session.Accept(nc, session.Params{
		DeviceID:     d.key.ID(),
		Name:         d.name,
		Capabilities: proto.CapResume,
		VerifyAuth:   true,
	})
	if err != nil {
		return
	}
	defer s.Close()

	paired := client.IsVerifiedPaired(d.pairedPath, s.Peer)
	peerClean := pairing.SanitizeName(s.Peer.Name)
	if paired && peerClean != "" {
		identity.UpdatePeerName(d.pairedPath, hex.EncodeToString(s.Peer.DeviceID[:]), peerClean)
	}
	cfg := transfer.Config{Log: d.log}
	if !paired {
		cfg.Confirm = func(_ *proto.Manifest) error {
			return proto.NewError(proto.CodeUnauthorized, "对方为后台守护模式，不接收未配对局域网临时文件")
		}
	}
	res, err := transfer.Receive(s, d.outDir, cfg)
	if err != nil {
		d.log.Event("daemon.lan_recv_error", map[string]any{"error": err.Error()})
		return
	}
	d.log.Event("daemon.lan_recv_done", map[string]any{
		"peer": peerClean, "files": res.Files, "bytes": res.Bytes,
	})
}

func (d *daemonState) runLANDiscovery(ctx context.Context) {
	go func() {
		_ = discovery.Announce(ctx, discovery.Config{
			DeviceID: d.key.ID(),
			Name:     d.name,
			Port:     d.port,
			Log:      d.log,
		})
	}()
	lst, err := discovery.Listen(discovery.Config{
		DeviceID: d.key.ID(),
		Name:     d.name,
		Log:      d.log,
	})
	if err != nil {
		return
	}
	defer lst.Close()
	go func() { _ = lst.Run(ctx) }()

	t := time.NewTicker(time.Second)
	defer t.Stop()
	for {
		devs := lst.Devices()
		now := time.Now()
		d.mu.Lock()
		if d.lanPeers == nil {
			d.lanPeers = map[string]daemonLANPeer{}
		}
		for id, e := range d.lanPeers {
			if now.Sub(e.lastSeen) > discovery.DeviceTTL {
				delete(d.lanPeers, id)
			}
		}
		for _, dev := range devs {
			if dev.TCPAddr == "" {
				continue
			}
			idHex := hex.EncodeToString(dev.DeviceID[:])
			d.lanPeers[idHex] = daemonLANPeer{
				name:     dev.Name,
				addr:     dev.TCPAddr,
				iface:    dev.Iface,
				lastSeen: dev.LastSeen,
			}
		}
		d.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (d *daemonState) runSignalingLoop(ctx context.Context) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		for {
			d.mu.Lock()
			held := d.sigHoldCount > 0
			resumeCh := d.sigResumeCh
			d.mu.Unlock()
			if !held {
				break
			}
			select {
			case <-ctx.Done():
				return
			case <-resumeCh:
			}
		}

		actx, acancel := context.WithCancel(ctx)
		d.mu.Lock()
		d.sigStopCur = acancel
		d.mu.Unlock()

		wasOnline := d.serveOneSignaling(actx, ctx)
		acancel()
		d.mu.Lock()
		d.sigStopCur = nil
		d.mu.Unlock()

		if ctx.Err() != nil {
			return
		}
		if wasOnline {
			backoff = 2 * time.Second
		} else if backoff < 30*time.Second {
			backoff *= 2
			if backoff > 30*time.Second {
				backoff = 30 * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (d *daemonState) serveOneSignaling(ctx, rootCtx context.Context) bool {
	d.mu.Lock()
	if rootCtx.Err() == nil {
		d.sigState = "connecting"
	}
	d.mu.Unlock()

	dctx, dcancel := context.WithTimeout(ctx, 10*time.Second)
	cl, err := signaling.Dial(dctx, d.signalURL, d.key, d.log)
	dcancel()
	if err != nil {
		return false
	}

	defer func() {
		d.mu.Lock()
		if d.sigClient == cl {
			d.sigClient = nil
		}
		d.cloudPeers = nil
		if rootCtx.Err() != nil {
			d.sigState = "offline"
		} else {
			d.sigState = "connecting"
		}
		d.mu.Unlock()
		_ = cl.Close()
	}()

	d.mu.Lock()
	d.sigClient = cl
	d.sigState = "online"
	d.cloudPeers = make(map[string]bool)
	for _, p := range cl.InitialPeers() {
		if p.Online && p.DeviceID != "" {
			d.cloudPeers[p.DeviceID] = true
		}
	}
	d.mu.Unlock()

	cl.On(signaling.TypePresence, func(e *signaling.Envelope) {
		p, perr := signaling.Decode[signaling.Presence](e)
		if perr != nil || p.DeviceID == "" {
			return
		}
		d.mu.Lock()
		if d.cloudPeers == nil {
			d.cloudPeers = map[string]bool{}
		}
		if p.Online {
			d.cloudPeers[p.DeviceID] = true
		} else {
			delete(d.cloudPeers, p.DeviceID)
		}
		d.mu.Unlock()
	})

	type offerItem struct {
		req       signaling.SendRequest
		peerPub   ed25519.PublicKey
		candCh    chan *signaling.Envelope
		unregCand func()
	}
	offerCh := make(chan offerItem, 4)

	cl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		req, rerr := signaling.Decode[signaling.SendRequest](e)
		if rerr != nil {
			return
		}
		pub, perr := client.PairedPeer(d.pairedPath, req.FromDevice)
		if perr != nil {
			_ = cl.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "unauthorized", Message: perr.Error()})
			return
		}
		candCh := make(chan *signaling.Envelope, 8)
		unreg := d.registerCandRoute(req.SessionID, candCh)
		select {
		case offerCh <- offerItem{req: req, peerPub: pub, candCh: candCh, unregCand: unreg}:
		default:
			unreg()
		}
	})
	cl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		c, derr := signaling.Decode[signaling.Candidates](e)
		if derr != nil || c.SessionID == "" {
			return
		}
		d.mu.Lock()
		ch := d.candRoutes[c.SessionID]
		d.mu.Unlock()
		if ch != nil {
			select {
			case ch <- e:
			default:
			}
		}
	})
	cl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		rr, rerr := signaling.Decode[signaling.RelayReady](e)
		if rerr != nil {
			return
		}
		d.mu.Lock()
		ch := d.readyRoute
		d.mu.Unlock()
		if ch != nil {
			select {
			case ch <- rr:
			default:
			}
		}
	})

	for {
		select {
		case <-ctx.Done():
			return true
		case <-cl.Done():
			return true
		case off := <-offerCh:
			readyCh := make(chan signaling.RelayReady, 2)
			d.mu.Lock()
			d.readyRoute = readyCh
			d.mu.Unlock()

			rctx, rcancel := context.WithTimeout(ctx, 10*time.Minute)
			res, rerr := client.ReceiveAuto(rctx, client.ReceiveOptions{
				Key:         d.key,
				SignalURL:   d.signalURL,
				OutDir:      d.outDir,
				PairedPath:  d.pairedPath,
				Name:        d.name,
				STUNServers: d.stun,
				Log:         d.log,
			}, cl, off.req, off.peerPub, off.candCh, readyCh)
			rcancel()

			d.mu.Lock()
			if d.readyRoute == readyCh {
				d.readyRoute = nil
			}
			d.mu.Unlock()
			off.unregCand()

			if rerr != nil {
				d.log.Event("daemon.auto_recv_error", map[string]any{"peer": truncHex(off.req.FromDevice), "error": rerr.Error()})
			} else {
				d.log.Event("daemon.auto_recv_done", map[string]any{"peer": truncHex(off.req.FromDevice), "channel": res.Channel, "files": res.Files, "bytes": res.Bytes})
			}
		}
	}
}

func (d *daemonState) registerCandRoute(sid string, ch chan *signaling.Envelope) func() {
	if sid == "" || ch == nil {
		return func() {}
	}
	d.mu.Lock()
	if d.candRoutes == nil {
		d.candRoutes = map[string]chan *signaling.Envelope{}
	}
	d.candRoutes[sid] = ch
	d.mu.Unlock()
	return func() {
		d.mu.Lock()
		delete(d.candRoutes, sid)
		d.mu.Unlock()
	}
}

func (d *daemonState) holdSignaling() func() {
	d.mu.Lock()
	d.sigHoldCount++
	if d.sigHoldCount == 1 {
		d.sigResumeCh = make(chan struct{})
	}
	stopCur := d.sigStopCur
	d.mu.Unlock()
	if stopCur != nil {
		stopCur()
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			d.mu.Lock()
			if d.sigHoldCount > 0 {
				d.sigHoldCount--
				if d.sigHoldCount == 0 && d.sigResumeCh != nil {
					close(d.sigResumeCh)
				}
			}
			d.mu.Unlock()
		})
	}
}

func (d *daemonState) Status(context.Context) ipc.StatusResponse {
	d.mu.Lock()
	defer d.mu.Unlock()
	online := make([]string, 0, len(d.cloudPeers))
	for id, ok := range d.cloudPeers {
		if ok {
			online = append(online, id)
		}
	}
	now := time.Now()
	lans := make(map[string]string, len(d.lanPeers))
	for id, e := range d.lanPeers {
		if e.addr != "" && now.Sub(e.lastSeen) <= discovery.DeviceTTL {
			lans[id] = e.addr
		}
	}
	return ipc.StatusResponse{
		OK:          true,
		Version:     version,
		DeviceID:    d.key.IDHex(),
		Name:        d.name,
		SignalState: d.sigState,
		LANPort:     d.port,
		ReceiveDir:  d.outDir,
		KeyPath:     d.keyPath,
		PairedPath:  d.pairedPath,
		OnlinePeers: online,
		LANPeers:    lans,
	}
}

func (d *daemonState) Peers(context.Context) (ipc.PeersResponse, error) {
	store, err := identity.LoadPaired(d.pairedPath)
	if err != nil {
		return ipc.PeersResponse{}, err
	}
	now := time.Now()
	d.mu.Lock()
	sigOnline := d.sigState == "online"
	cloudCopy := make(map[string]bool, len(d.cloudPeers))
	for k, v := range d.cloudPeers {
		cloudCopy[k] = v
	}
	lanCopy := make(map[string]daemonLANPeer, len(d.lanPeers))
	for k, v := range d.lanPeers {
		if v.addr != "" && now.Sub(v.lastSeen) <= discovery.DeviceTTL {
			lanCopy[k] = v
		}
	}
	d.mu.Unlock()

	paired := make([]ipc.PairedPeerInfo, 0)
	for _, p := range store.List() {
		paired = append(paired, ipc.PairedPeerInfo{
			DeviceID: p.DeviceID,
			Name:     p.Name,
			PubKey:   p.PubKey,
			PairedAt: p.PairedAt,
			Online:   sigOnline && cloudCopy[p.DeviceID],
			LANAddr:  lanCopy[p.DeviceID].addr,
		})
	}
	lans := make([]ipc.LANPeerInfo, 0, len(lanCopy))
	for id, p := range lanCopy {
		lans = append(lans, ipc.LANPeerInfo{
			DeviceID: id,
			Name:     p.name,
			Addr:     p.addr,
			Iface:    p.iface,
		})
	}
	return ipc.PeersResponse{Paired: paired, LAN: lans}, nil
}

func (d *daemonState) Send(ctx context.Context, req ipc.SendRequest) ipc.SendResponse {
	if len(req.Files) == 0 {
		return ipc.SendResponse{OK: false, Error: "files must not be empty"}
	}
	if req.Peer == "" {
		return ipc.SendResponse{OK: false, Error: "peer is required for headless daemon"}
	}
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "auto"
	}
	if _, port, err := net.SplitHostPort(req.Peer); err == nil && port != "" {
		if n, aerr := strconv.Atoi(port); aerr == nil && n > 0 && n <= 65535 {
			res, lerr := client.SendLAN(ctx, client.Options{
				KeyPath: d.keyPath, PairedPath: d.pairedPath, Name: d.name, Log: d.log,
			}, req.Peer, req.Files)
			if lerr != nil {
				return ipc.SendResponse{OK: false, Error: lerr.Error()}
			}
			return ipc.SendResponse{
				OK: true, Path: "lan", Peer: res.PeerName, TransferID: res.TransferID,
				Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
			}
		}
	}

	target, kind, err := resolveMCPTarget(d.pairedPath, req.Peer)
	if err != nil {
		return ipc.SendResponse{OK: false, Error: err.Error()}
	}
	if kind == "lan" {
		res, lerr := client.SendLAN(ctx, client.Options{
			KeyPath: d.keyPath, PairedPath: d.pairedPath, Name: d.name, Log: d.log,
		}, target, req.Files)
		if lerr != nil {
			return ipc.SendResponse{OK: false, Error: lerr.Error()}
		}
		return ipc.SendResponse{
			OK: true, Path: "lan", Peer: res.PeerName, TransferID: res.TransferID,
			Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
		}
	}

	opts := client.Options{
		SignalURL:   d.signalURL,
		Key:         d.key,
		KeyPath:     d.keyPath,
		PairedPath:  d.pairedPath,
		Name:        d.name,
		STUNServers: d.stun,
		CandRoute:   d.registerCandRoute,
		Log:         d.log,
	}
	if mode == "auto" {
		d.mu.Lock()
		lanEntry := d.lanPeers[target]
		d.mu.Unlock()
		if lanEntry.addr != "" && time.Since(lanEntry.lastSeen) <= discovery.DeviceTTL {
			if res, lerr := client.SendLAN(ctx, opts, lanEntry.addr, req.Files); lerr == nil {
				return ipc.SendResponse{
					OK: true, Path: "lan", Peer: res.PeerName, TransferID: res.TransferID,
					Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
				}
			}
		}
	}
	if mode == "lan" {
		return ipc.SendResponse{OK: false, Error: "mode=lan requires an active LAN peer or host:port"}
	}

	d.mu.Lock()
	var activeCl *signaling.Client
	if d.sigState == "online" && d.sigClient != nil && d.sigClient.Alive() {
		activeCl = d.sigClient
	}
	d.mu.Unlock()
	if activeCl != nil {
		opts.SignalClient = activeCl
	} else {
		release := d.holdSignaling()
		defer release()
	}

	var res client.Result
	if mode == "relay" {
		res, err = client.SendFiles(ctx, opts, target, req.Files)
	} else {
		res, err = client.SendAuto(ctx, opts, target, req.Files)
	}
	if err != nil {
		return ipc.SendResponse{OK: false, Error: err.Error()}
	}
	return ipc.SendResponse{
		OK: true, Path: channelPathName(res.Channel), Peer: res.PeerName, TransferID: res.TransferID,
		Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
	}
}

func (d *daemonState) SendText(ctx context.Context, req ipc.SendTextRequest) ipc.SendResponse {
	if req.Text == "" {
		return ipc.SendResponse{OK: false, Error: "text cannot be empty"}
	}
	fname := strings.TrimSpace(req.Filename)
	if fname == "" {
		fname = "note-" + time.Now().Format("150405") + ".txt"
	} else {
		fname = filepath.Base(strings.ReplaceAll(fname, "\\", "/"))
	}
	tmpDir, err := os.MkdirTemp("", "ferry-daemon-text-*")
	if err != nil {
		return ipc.SendResponse{OK: false, Error: err.Error()}
	}
	defer os.RemoveAll(tmpDir)
	fp := filepath.Join(tmpDir, fname)
	if err := os.WriteFile(fp, []byte(req.Text), 0o600); err != nil {
		return ipc.SendResponse{OK: false, Error: err.Error()}
	}
	return d.Send(ctx, ipc.SendRequest{Peer: req.Peer, Files: []string{fp}, Mode: req.Mode})
}

func (d *daemonState) OpenUI(context.Context) error { return nil }
