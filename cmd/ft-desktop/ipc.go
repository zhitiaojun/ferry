//go:build windows

package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/ipc"
	"github.com/cwlindean/filetransfer/core/transfer"
)

type pickPeerRequest struct {
	peers []client.Peer
	reply chan int
}

var (
	uiPickMu    sync.Mutex
	uiPickQueue []*pickPeerRequest
)

func enqueuePickPeer(peers []client.Peer) (client.Peer, bool) {
	if trayHandle == nil || trayHandle.hwnd == 0 {
		return client.Peer{}, false
	}
	req := &pickPeerRequest{
		peers: peers,
		reply: make(chan int, 1),
	}
	uiPickMu.Lock()
	uiPickQueue = append(uiPickQueue, req)
	uiPickMu.Unlock()
	procPostMessageW.Call(uintptr(trayHandle.hwnd), wmPickPeer, 0, 0)
	idx := <-req.reply
	if idx < 0 || idx >= len(peers) {
		return client.Peer{}, false
	}
	return peers[idx], true
}

func handleUIPickPeer() {
	uiPickMu.Lock()
	if len(uiPickQueue) == 0 {
		uiPickMu.Unlock()
		return
	}
	req := uiPickQueue[0]
	uiPickQueue = uiPickQueue[1:]
	uiPickMu.Unlock()

	picked, ok := pickPeer(req.peers)
	if !ok {
		req.reply <- -1
		return
	}
	idx := -1
	for i, p := range req.peers {
		if p.ID == picked.ID {
			idx = i
			break
		}
	}
	req.reply <- idx
}

type desktopIPC struct {
	app *app
	key *identity.Key
}

func (d *desktopIPC) Status(context.Context) ipc.StatusResponse {
	a := d.app
	a.mu.Lock()
	sigState := a.signalState
	if sigState == "" {
		sigState = "offline"
	}
	online := make([]string, 0, len(a.cloudPeers))
	for id, ok := range a.cloudPeers {
		if ok {
			online = append(online, id)
		}
	}
	now := time.Now()
	lans := make(map[string]string, len(a.lanPeers))
	for id, e := range a.lanPeers {
		if e.addr != "" && now.Sub(e.lastSeen) <= discovery.DeviceTTL {
			lans[id] = e.addr
		}
	}
	a.mu.Unlock()

	return ipc.StatusResponse{
		OK:          true,
		Version:     version,
		DeviceID:    d.key.IDHex(),
		Name:        a.deviceName(),
		SignalState: sigState,
		LANPort:     a.port,
		ReceiveDir:  a.receiveDir(),
		KeyPath:     a.keyPath,
		PairedPath:  a.pairedPath,
		OnlinePeers: online,
		LANPeers:    lans,
	}
}

func (d *desktopIPC) Peers(context.Context) (ipc.PeersResponse, error) {
	a := d.app
	store, err := identity.LoadPaired(a.pairedPath)
	if err != nil {
		return ipc.PeersResponse{}, err
	}
	now := time.Now()
	a.mu.Lock()
	sigOnline := a.signalState == "online"
	cloudCopy := make(map[string]bool, len(a.cloudPeers))
	for k, v := range a.cloudPeers {
		cloudCopy[k] = v
	}
	lanCopy := make(map[string]lanPeerEntry, len(a.lanPeers))
	for k, v := range a.lanPeers {
		if v.addr != "" && now.Sub(v.lastSeen) <= discovery.DeviceTTL {
			lanCopy[k] = v
		}
	}
	a.mu.Unlock()

	devs := store.List()
	paired := make([]ipc.PairedPeerInfo, 0, len(devs))
	lanList := make([]ipc.LANPeerInfo, 0, len(lanCopy))
	nameByID := make(map[string]string, len(devs))
	for _, p := range devs {
		nameByID[p.DeviceID] = p.Name
		paired = append(paired, ipc.PairedPeerInfo{
			DeviceID: p.DeviceID,
			Name:     p.Name,
			PubKey:   p.PubKey,
			PairedAt: p.PairedAt,
			Online:   sigOnline && cloudCopy[p.DeviceID],
			LANAddr:  lanCopy[p.DeviceID].addr,
		})
	}
	for id, entry := range lanCopy {
		name := nameByID[id]
		if name == "" {
			name = entry.name
		}
		lanList = append(lanList, ipc.LANPeerInfo{
			DeviceID: id,
			Name:     name,
			Addr:     entry.addr,
			Iface:    entry.iface,
		})
	}
	return ipc.PeersResponse{Paired: paired, LAN: lanList}, nil
}

func (d *desktopIPC) Send(ctx context.Context, req ipc.SendRequest) ipc.SendResponse {
	if len(req.Files) == 0 {
		return ipc.SendResponse{OK: false, Error: "没有可发送的文件"}
	}
	a := d.app
	mode := strings.ToLower(strings.TrimSpace(req.Mode))
	if mode == "" {
		mode = "auto"
	}

	targetPeer := strings.TrimSpace(req.Peer)
	if targetPeer != "" {
		if _, port, err := net.SplitHostPort(targetPeer); err == nil && port != "" {
			if n, aerr := strconv.Atoi(port); aerr == nil && n > 0 && n <= 65535 {
				if mode != "auto" && mode != "lan" {
					return ipc.SendResponse{OK: false, Error: fmt.Sprintf("目标 %s 为局域网地址，不支持 %s 模式", targetPeer, mode)}
				}
				return d.sendDirectLAN(ctx, targetPeer, req.Files)
			}
		}
		peer, err := resolveDesktopPeer(a.pairedPath, targetPeer)
		if err != nil {
			return ipc.SendResponse{OK: false, Error: err.Error()}
		}
		return d.sendToSelectedPeer(ctx, peer, req.Files, mode)
	}

	peers, err := client.Peers(a.pairedPath)
	if err != nil {
		return ipc.SendResponse{OK: false, Error: "读取配对表失败: " + err.Error()}
	}
	switch len(peers) {
	case 0:
		if req.Interactive && trayHandle != nil && trayHandle.hwnd != 0 {
			procPostMessageW.Call(uintptr(trayHandle.hwnd), wmOpenPair, 0, 0)
		}
		return ipc.SendResponse{OK: false, Error: "没有已配对设备：请先完成设备配对"}
	case 1:
		return d.sendToSelectedPeer(ctx, peers[0], req.Files, mode)
	default:
		if !req.Interactive {
			return ipc.SendResponse{OK: false, Error: fmt.Sprintf("已配对 %d 台设备，请指定目标设备", len(peers))}
		}
		picked, ok := enqueuePickPeer(peers)
		if !ok {
			return ipc.SendResponse{OK: false, Error: "已取消发送"}
		}
		return d.sendToSelectedPeer(ctx, picked, req.Files, mode)
	}
}

func (d *desktopIPC) SendText(ctx context.Context, req ipc.SendTextRequest) ipc.SendResponse {
	if req.Text == "" {
		return ipc.SendResponse{OK: false, Error: "文本内容不能为空"}
	}
	fname := strings.TrimSpace(req.Filename)
	if fname == "" {
		fname = "note-" + time.Now().Format("150405") + ".txt"
	} else {
		fname = filepath.Base(strings.ReplaceAll(fname, "\\", "/"))
		if fname == "" || fname == "." || fname == ".." {
			return ipc.SendResponse{OK: false, Error: "无效文件名"}
		}
	}
	tmpDir, err := os.MkdirTemp("", "ferry-ipc-text-*")
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

func (d *desktopIPC) OpenUI(context.Context) error {
	if trayHandle != nil && trayHandle.hwnd != 0 {
		procAllowSetForegroundWindow.Call(^uintptr(0))
		procPostMessageW.Call(uintptr(trayHandle.hwnd), wmShowMain, 0, 0)
	}
	return nil
}

func (d *desktopIPC) sendDirectLAN(ctx context.Context, addr string, files []string) ipc.SendResponse {
	a := d.app
	items, totalBytes, firstRel, derr := describeDesktopPaths(files)
	if derr != nil || len(items) == 0 {
		return ipc.SendResponse{OK: false, Error: "没有可发送的文件"}
	}
	txID := a.beginTransfer("send", addr, firstRel, len(items), totalBytes)
	ctrl := transfer.NewController()
	a.attachTransferControl(txID, ctrl, true, true)
	defer a.endTransfer(txID)

	res, err := client.SendLAN(ctx, client.Options{
		KeyPath:    a.keyPath,
		PairedPath: a.pairedPath,
		Name:       a.deviceName(),
		Control:    ctrl,
		OnStatus: func(st client.Status) {
			a.updateTransfer(txID, st.Stage, "LAN", "", st.FileName, st.FileIndex, st.FileCount, st.BytesDone, st.BytesTotal)
		},
	}, addr, files)
	if err != nil {
		if ctrl.IsCancelled() {
			a.note(fmt.Sprintf("%s  → %s · %s · 已取消", time.Now().Format("15:04"), sanitizeLedgerText(addr), summarizeItems(items)))
			a.notifyMainPaint()
			return ipc.SendResponse{OK: false, Error: "已取消传输"}
		}
		if transfer.IsCancelled(err) {
			a.note(fmt.Sprintf("%s  → %s · %s · 对方已取消", time.Now().Format("15:04"), sanitizeLedgerText(addr), summarizeItems(items)))
			a.notifyMainPaint()
			return ipc.SendResponse{OK: false, Error: "对方已取消传输"}
		}
		a.note(fmt.Sprintf("%s  → %s · %s · 失败", time.Now().Format("15:04"), sanitizeLedgerText(addr), summarizeItems(items)))
		a.notifyMainPaint()
		return ipc.SendResponse{OK: false, Error: err.Error()}
	}
	peerLabel := res.PeerName
	if peerLabel == "" {
		peerLabel = addr
	} else {
		a.updateTransfer(txID, "", "", peerLabel, "", 0, 0, -1, 0)
	}
	a.note(fmt.Sprintf("%s  → %s · %s · %s · %s · LAN",
		time.Now().Format("15:04"), sanitizeLedgerText(peerLabel), summarizeItems(items), humanBytes(res.Bytes), fmtDur(res.Duration)))
	a.notifyMainPaint()
	return ipc.SendResponse{
		OK: true, Path: "lan", Peer: peerLabel, TransferID: res.TransferID,
		Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
	}
}

func (d *desktopIPC) sendToSelectedPeer(ctx context.Context, peer client.Peer, files []string, mode string) ipc.SendResponse {
	a := d.app
	switch mode {
	case "auto":
		res, err := sendToPeer(a, files, peer)
		if err != nil {
			return ipc.SendResponse{OK: false, Error: err.Error()}
		}
		return ipcResultFromClient(peerLabel(peer), res)
	case "lan":
		lanAddr := a.peerLANAddr(peer.ID)
		if lanAddr == "" {
			return ipc.SendResponse{OK: false, Error: "目标设备当前未在局域网内发现"}
		}
		return d.sendDirectLAN(ctx, lanAddr, files)
	case "punch", "relay":
		items, totalBytes, firstRel, derr := describeDesktopPaths(files)
		if derr != nil || len(items) == 0 {
			return ipc.SendResponse{OK: false, Error: "所选文件夹内没有可发送的文件"}
		}
		txID := a.beginTransfer("send", peerLabel(peer), firstRel, len(items), totalBytes)
		ctrl := transfer.NewController()
		a.attachTransferControl(txID, ctrl, true, true)
		defer a.endTransfer(txID)

		opts := client.Options{
			SignalURL:    a.currentSignalURL(),
			KeyPath:      a.keyPath,
			PairedPath:   a.pairedPath,
			Name:         a.deviceName(),
			STUNServers:  a.currentSTUNServers(),
			CandRoute:    a.registerCandRoute,
			DisablePunch: mode == "relay",
			Control:      ctrl,
			OnStatus: func(st client.Status) {
				ch := ""
				switch st.Channel {
				case "lan":
					ch = "LAN"
				case "punch":
					ch = "打洞"
				case "relay":
					ch = "中继"
				}
				a.updateTransfer(txID, st.Stage, ch, "", st.FileName, st.FileIndex, st.FileCount, st.BytesDone, st.BytesTotal)
			},
		}
		if sigCl := a.activeSignalClient(); sigCl != nil {
			opts.SignalClient = sigCl
		} else {
			release := a.holdSignaling()
			defer release()
		}
		var res client.Result
		var err error
		if mode == "relay" {
			res, err = client.SendFiles(ctx, opts, peer.ID, files)
		} else {
			res, err = client.SendAuto(ctx, opts, peer.ID, files)
		}
		if err != nil {
			if ctrl.IsCancelled() {
				a.note(fmt.Sprintf("%s  → %s · %s · 已取消", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items)))
				a.notifyMainPaint()
				return ipc.SendResponse{OK: false, Error: "已取消传输"}
			}
			if transfer.IsCancelled(err) {
				a.note(fmt.Sprintf("%s  → %s · %s · 对方已取消", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items)))
				a.notifyMainPaint()
				return ipc.SendResponse{OK: false, Error: "对方已取消传输"}
			}
			a.note(fmt.Sprintf("%s  → %s · %s · 失败", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items)))
			a.notifyMainPaint()
			return ipc.SendResponse{OK: false, Error: err.Error()}
		}
		return ipcResultFromClient(peerLabel(peer), res)
	default:
		return ipc.SendResponse{OK: false, Error: fmt.Sprintf("未知传输模式 %q", mode)}
	}
}

func ipcResultFromClient(fallbackPeer string, res client.Result) ipc.SendResponse {
	p := res.PeerName
	if p == "" {
		p = fallbackPeer
	}
	path := "relay"
	switch res.Channel {
	case "LAN":
		path = "lan"
	case "打洞":
		path = "punch"
	}
	return ipc.SendResponse{
		OK: true, Path: path, Peer: p, TransferID: res.TransferID,
		Files: res.Files, Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: res.Duration.Milliseconds(),
	}
}

func resolveDesktopPeer(pairedPath, raw string) (client.Peer, error) {
	peers, err := client.Peers(pairedPath)
	if err != nil {
		return client.Peer{}, err
	}
	low := strings.ToLower(strings.TrimSpace(raw))
	if len(low) == 64 {
		if _, herr := hex.DecodeString(low); herr == nil {
			for _, p := range peers {
				if strings.EqualFold(p.ID, low) {
					return p, nil
				}
			}
			return client.Peer{}, fmt.Errorf("peer %s is not paired", truncID(low))
		}
	}
	var matches []client.Peer
	for _, p := range peers {
		if strings.EqualFold(p.Name, raw) {
			return p, nil
		}
		if strings.HasPrefix(strings.ToLower(p.ID), low) ||
			(p.Name != "" && strings.Contains(strings.ToLower(p.Name), low)) {
			matches = append(matches, p)
		}
	}
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) > 1 {
		return client.Peer{}, fmt.Errorf("ambiguous peer %q", raw)
	}
	return client.Peer{}, fmt.Errorf("peer %q not found in paired devices", raw)
}
