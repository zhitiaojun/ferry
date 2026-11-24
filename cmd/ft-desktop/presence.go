//go:build windows

package main

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/signaling"
)

type lanPeerEntry struct {
	addr     string
	lastSeen time.Time
}

func (a *app) notifyMainPaint() {
	if h := existingMainWindow(); h != 0 {
		procPostMessageW.Call(uintptr(h), wmAppPaint, 0, 0)
	}
}

func (a *app) peerReachability(id string) string {
	if a == nil || id == "" {
		return "offline"
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	if e, ok := a.lanPeers[id]; ok && e.addr != "" && now.Sub(e.lastSeen) <= discovery.DeviceTTL {
		return "lan"
	}
	if a.signalState == "online" && a.cloudPeers[id] {
		return "cloud"
	}
	return "offline"
}

func (a *app) peerLANAddr(id string) string {
	if a == nil || id == "" {
		return ""
	}
	now := time.Now()
	a.mu.Lock()
	defer a.mu.Unlock()
	e, ok := a.lanPeers[id]
	if !ok || e.addr == "" || now.Sub(e.lastSeen) > discovery.DeviceTTL {
		return ""
	}
	return e.addr
}

func (a *app) signalStatusText() string {
	if a == nil {
		return "未连接"
	}
	a.mu.Lock()
	st := a.signalState
	a.mu.Unlock()
	switch st {
	case "online":
		return "已连接（服务端确认）"
	case "connecting":
		return "重连中…"
	default:
		return "未连接"
	}
}

func (a *app) signalHomeStatus() (string, string) {
	if a == nil {
		return "信令服务器：未连接", "offline"
	}
	a.mu.Lock()
	st := a.signalState
	c := a.sigClient
	a.mu.Unlock()
	if st == "online" && c != nil && c.Alive() {
		return "信令服务器：已连接", "online"
	}
	if st == "connecting" || st == "online" {
		return "信令服务器：重连中…", "connecting"
	}
	return "信令服务器：未连接", "offline"
}

func (a *app) registerCandRoute(sid string, ch chan *signaling.Envelope) func() {
	if a == nil || sid == "" || ch == nil {
		return func() {}
	}
	a.mu.Lock()
	if a.candRoutes == nil {
		a.candRoutes = map[string]chan *signaling.Envelope{}
	}
	a.candRoutes[sid] = ch
	a.mu.Unlock()
	return func() {
		a.mu.Lock()
		delete(a.candRoutes, sid)
		a.mu.Unlock()
	}
}

func (a *app) dispatchCand(e *signaling.Envelope) {
	if a == nil {
		return
	}
	c, err := signaling.Decode[signaling.Candidates](e)
	if err != nil || c.SessionID == "" {
		return
	}
	a.mu.Lock()
	ch := a.candRoutes[c.SessionID]
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- e:
		default:
		}
	}
}

func (a *app) setReadyRoute(ch chan signaling.RelayReady) {
	if a == nil {
		return
	}
	a.mu.Lock()
	a.readyRoute = ch
	a.mu.Unlock()
}

func (a *app) clearReadyRoute(ch chan signaling.RelayReady) {
	if a == nil {
		return
	}
	a.mu.Lock()
	if a.readyRoute == ch {
		a.readyRoute = nil
	}
	a.mu.Unlock()
}

func (a *app) dispatchReady(rr signaling.RelayReady) {
	if a == nil {
		return
	}
	a.mu.Lock()
	ch := a.readyRoute
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- rr:
		default:
		}
	}
}

func (a *app) activeSignalClient() *signaling.Client {
	if a == nil {
		return nil
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.signalState == "online" && a.sigClient != nil && a.sigClient.Alive() {
		return a.sigClient
	}
	return nil
}

func (a *app) holdSignaling() func() {
	if a == nil {
		return func() {}
	}
	a.mu.Lock()
	a.sigHoldCount++
	if a.sigHoldCount == 1 {
		a.sigResumeCh = make(chan struct{})
	}
	stopCur := a.sigStopCur
	a.mu.Unlock()

	if stopCur != nil {
		stopCur()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			a.mu.Lock()
			if a.sigHoldCount > 0 {
				a.sigHoldCount--
				if a.sigHoldCount == 0 && a.sigResumeCh != nil {
					close(a.sigResumeCh)
				}
			}
			a.mu.Unlock()
		})
	}
}

func (a *app) acquireSignalForPair(ctx context.Context, key *identity.Key) (*signaling.Client, func(), error) {
	if cl := a.activeSignalClient(); cl != nil {
		return cl, func() {}, nil
	}
	release := a.holdSignaling()
	cl, err := signaling.Dial(ctx, a.signalURL, key, nil)
	if err != nil {
		release()
		return nil, nil, err
	}
	return cl, func() {
		_ = cl.Close()
		release()
	}, nil
}

func (a *app) startPresence(ctx context.Context, key *identity.Key) {
	if a == nil || key == nil {
		return
	}
	go a.runLANDiscovery(ctx, key)
	if a.signalURL != "" {
		go a.runSignalingPresence(ctx, key)
	}
}

func (a *app) runLANDiscovery(ctx context.Context, key *identity.Key) {
	go func() {
		err := discovery.Announce(ctx, discovery.Config{
			DeviceID: key.ID(),
			Name:     a.deviceName(),
			Port:     a.port,
		})
		if err != nil && ctx.Err() == nil {
			a.event("desktop.discovery.announce_error", map[string]any{"error": err.Error()})
		}
	}()

	lst, err := discovery.Listen(discovery.Config{
		DeviceID: key.ID(),
		Name:     a.deviceName(),
	})
	if err != nil {
		a.event("desktop.discovery.listen_error", map[string]any{"error": err.Error()})
		return
	}
	defer lst.Close()

	go func() {
		if rerr := lst.Run(ctx); rerr != nil && ctx.Err() == nil {
			a.event("desktop.discovery.run_error", map[string]any{"error": rerr.Error()})
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		devs := lst.Devices()
		now := time.Now()
		changed := false

		a.mu.Lock()
		if a.lanPeers == nil {
			a.lanPeers = map[string]lanPeerEntry{}
		}
		for id, entry := range a.lanPeers {
			if now.Sub(entry.lastSeen) > discovery.DeviceTTL {
				delete(a.lanPeers, id)
				changed = true
			}
		}
		for _, d := range devs {
			if d.TCPAddr == "" {
				continue
			}
			idHex := hex.EncodeToString(d.DeviceID[:])
			prev, ok := a.lanPeers[idHex]
			if !ok || prev.addr != d.TCPAddr {
				changed = true
			}
			a.lanPeers[idHex] = lanPeerEntry{addr: d.TCPAddr, lastSeen: d.LastSeen}
		}
		a.mu.Unlock()
		if changed {
			a.notifyMainPaint()
		}

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (a *app) runSignalingPresence(ctx context.Context, key *identity.Key) {
	backoff := 2 * time.Second
	for ctx.Err() == nil {
		for {
			a.mu.Lock()
			held := a.sigHoldCount > 0
			resumeCh := a.sigResumeCh
			a.mu.Unlock()
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
		a.mu.Lock()
		a.sigStopCur = acancel
		a.mu.Unlock()

		wasOnline := a.serveOneSignaling(actx, ctx, key)

		acancel()
		a.mu.Lock()
		a.sigStopCur = nil
		a.mu.Unlock()

		if ctx.Err() != nil {
			return
		}
		if wasOnline {
			backoff = 2 * time.Second
		} else if backoff < 10*time.Second {
			backoff *= 2
			if backoff > 10*time.Second {
				backoff = 10 * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
	}
}

func (a *app) serveOneSignaling(ctx, rootCtx context.Context, key *identity.Key) bool {
	a.mu.Lock()
	if rootCtx.Err() == nil {
		a.signalState = "connecting"
	}
	a.mu.Unlock()
	a.notifyMainPaint()

	dialCtx, dialCancel := context.WithTimeout(ctx, 10*time.Second)
	cl, err := signaling.Dial(dialCtx, a.signalURL, key, nil)
	dialCancel()
	if err != nil {
		if ctx.Err() == nil {
			a.event("desktop.signal.dial_error", map[string]any{"error": err.Error()})
		}
		return false
	}

	defer func() {
		a.mu.Lock()
		if a.sigClient == cl {
			a.sigClient = nil
		}
		a.cloudPeers = nil
		if rootCtx.Err() != nil {
			a.signalState = "offline"
		} else {
			a.signalState = "connecting"
		}
		a.mu.Unlock()
		a.notifyMainPaint()
		_ = cl.Close()
	}()

	a.mu.Lock()
	a.sigClient = cl
	a.signalState = "online"
	a.cloudPeers = make(map[string]bool)
	for _, p := range cl.InitialPeers() {
		if p.Online && p.DeviceID != "" {
			a.cloudPeers[p.DeviceID] = true
		}
	}
	a.mu.Unlock()
	a.notifyMainPaint()
	a.event("desktop.signal.online", map[string]any{"id": truncID(key.IDHex())})

	cl.On(signaling.TypePresence, func(e *signaling.Envelope) {
		p, perr := signaling.Decode[signaling.Presence](e)
		if perr != nil || p.DeviceID == "" {
			return
		}
		a.mu.Lock()
		if a.cloudPeers == nil {
			a.cloudPeers = map[string]bool{}
		}
		if p.Online {
			a.cloudPeers[p.DeviceID] = true
		} else {
			delete(a.cloudPeers, p.DeviceID)
		}
		a.mu.Unlock()
		a.notifyMainPaint()
	})

	type autoOffer struct {
		req       signaling.SendRequest
		peerPub   ed25519.PublicKey
		peerName  string
		candCh    chan *signaling.Envelope
		unregCand func()
	}
	offerCh := make(chan autoOffer, 4)

	cl.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		req, rerr := signaling.Decode[signaling.SendRequest](e)
		if rerr != nil {
			return
		}
		pub, perr := client.PairedPeer(a.pairedPath, req.FromDevice)
		if perr != nil {
			_ = cl.Send(ctx, signaling.TypeError, signaling.ErrorBody{Code: "unauthorized", Message: perr.Error()})
			return
		}
		if !recvEnabled.Load() {
			_ = cl.Send(ctx, signaling.TypeSendResp, signaling.SendResponse{
				ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: false, Reason: "paused",
			})
			return
		}
		peerName := truncID(req.FromDevice)
		if store, serr := identity.LoadPaired(a.pairedPath); serr == nil {
			if dev, ok := store.Get(req.FromDevice); ok && dev.Name != "" {
				peerName = dev.Name
			}
		}
		candCh := make(chan *signaling.Envelope, 8)
		unreg := a.registerCandRoute(req.SessionID, candCh)
		select {
		case offerCh <- autoOffer{req: req, peerPub: pub, peerName: peerName, candCh: candCh, unregCand: unreg}:
		default:
			unreg()
		}
	})

	cl.On(signaling.TypeCandidates, func(e *signaling.Envelope) {
		a.dispatchCand(e)
	})

	cl.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		rr, rerr := signaling.Decode[signaling.RelayReady](e)
		if rerr != nil {
			return
		}
		a.dispatchReady(rr)
	})

	for {
		select {
		case <-ctx.Done():
			return true
		case <-cl.Done():
			a.event("desktop.signal.dropped", map[string]any{"error": fmt.Sprintf("%v", cl.Err())})
			return true
		case offer := <-offerCh:
			readyCh := make(chan signaling.RelayReady, 2)
			a.setReadyRoute(readyCh)
			a.receiveOneAuto(ctx, cl, key, offer.req, offer.peerPub, offer.peerName, offer.candCh, readyCh)
			a.clearReadyRoute(readyCh)
			offer.unregCand()
		}
	}
}

func (a *app) receiveOneAuto(
	ctx context.Context,
	cl *signaling.Client,
	key *identity.Key,
	req signaling.SendRequest,
	peerPub ed25519.PublicKey,
	peerName string,
	candCh <-chan *signaling.Envelope,
	readyCh <-chan signaling.RelayReady,
) {
	start := time.Now()
	var totalBytes int64
	firstFile := ""
	if len(req.Files) > 0 {
		firstFile = req.Files[0].Name
		for _, f := range req.Files {
			totalBytes += f.Size
		}
	}
	txID := a.beginTransfer("recv", peerName, firstFile, len(req.Files), totalBytes)
	defer a.endTransfer(txID)

	rctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
	defer cancel()

	outDir := a.receiveDir()
	res, err := client.ReceiveAuto(rctx, client.ReceiveOptions{
		Key:         key,
		SignalURL:   a.signalURL,
		OutDir:      outDir,
		PairedPath:  a.pairedPath,
		Name:        a.deviceName(),
		STUNServers: a.stunServers,
		OnStatus: func(st client.Status) {
			ch := ""
			switch st.Channel {
			case "punch":
				ch = "打洞"
			case "relay":
				ch = "中继"
			}
			a.updateTransfer(txID, st.Stage, ch, "", st.FileName, st.FileIndex, st.FileCount, st.BytesDone, st.BytesTotal)
		},
	}, cl, req, peerPub, candCh, readyCh)
	if err != nil {
		a.event("desktop.auto_recv_error", map[string]any{"peer": truncID(req.FromDevice), "error": err.Error()})
		a.note(fmt.Sprintf("%s  ← %s · 失败", time.Now().Format("15:04"), sanitizeLedgerText(peerName)))
		a.notifyMainPaint()
		if trayHandle != nil {
			trayHandle.balloon("接收失败", err.Error())
		}
		return
	}
	if res.PeerName != "" && res.PeerName != peerName {
		peerName = res.PeerName
		if thePanel != nil {
			thePanel.reloadPeers()
		}
		if h := existingMainWindow(); h != 0 {
			procPostMessageW.Call(uintptr(h), wmAppReload, 0, 0)
		}
		a.updateTransfer(txID, "", "", peerName, "", 0, 0, -1, 0)
	}
	chLabel := res.Channel
	if chLabel == "" {
		chLabel = "中继"
	}
	a.event("desktop.auto_recv_done", map[string]any{
		"peer": truncID(req.FromDevice), "channel": chLabel, "files": res.Files, "bytes": res.Bytes,
		"duration_ms": res.Duration.Milliseconds(),
	})
	a.note(fmt.Sprintf("%s  ← %s · %s · %s · %s · %s",
		time.Now().Format("15:04"), sanitizeLedgerText(peerName), summarizeFiles(res.Files), humanBytes(res.Bytes), fmtDur(time.Since(start)), chLabel))
	a.notifyMainPaint()
	if trayHandle != nil {
		trayHandle.balloon(fmt.Sprintf("已接收 %d 个文件", len(res.Files)),
			fmt.Sprintf("来自 %s（%s），共 %d 字节，已存入 %s", peerName, chLabel, res.Bytes, outDir))
	}
}
