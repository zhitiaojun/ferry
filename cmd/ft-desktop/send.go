//go:build windows

package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/zhitiaojun/ferry/core/client"
	"github.com/zhitiaojun/ferry/core/transfer"
)

// sendFiles is the drop window's action: choose the receiver (one paired device
// or a menu) and push the files over the relay.
func (d *dropWindow) sendFiles(files []string) error {
	a := d.app
	peers, err := client.Peers(a.pairedPath)
	if err != nil {
		d.setLabel("读取配对表失败")
		return fmt.Errorf("读取配对表失败: %w", err)
	}
	if len(peers) == 0 {
		d.setLabel("没有已配对设备")
		return fmt.Errorf("没有已配对设备：先在两端完成配对（ft pair --show / --add，或 6 位码）")
	}
	peer := peers[0]
	if len(peers) > 1 {
		picked, ok := pickPeer(peers)
		if !ok {
			d.setLabel("已取消发送")
			return nil
		}
		peer = picked
	}
	d.setLabel(fmt.Sprintf("正在发送给 %s…", peer.Name))
	res, err := sendToPeer(a, files, peer)
	if err != nil {
		d.setLabel(fmt.Sprintf("发送失败：%v", err))
		return err
	}
	d.setLabel(fmt.Sprintf("已发送给 %s（%d 个文件）", peer.Name, len(res.Files)))
	return nil
}

// sendInteractive sends files from Explorer right-click / SendTo when no IPC
// daemon is active: pops pickPeer when multiple paired devices exist.
func sendInteractive(a *app, files []string) (client.Result, client.Peer, bool, error) {
	peers, err := client.Peers(a.pairedPath)
	if err != nil {
		return client.Result{}, client.Peer{}, false, fmt.Errorf("读取配对表失败: %w", err)
	}
	switch len(peers) {
	case 0:
		return client.Result{}, client.Peer{}, false, fmt.Errorf("没有已配对设备：先在两端完成配对（ferry pair --show / --add，或 6 位码）")
	case 1:
		res, err := sendToPeer(a, files, peers[0])
		return res, peers[0], false, err
	default:
		peer, ok := pickPeer(peers)
		if !ok {
			return client.Result{}, client.Peer{}, true, nil
		}
		res, err := sendToPeer(a, files, peer)
		return res, peer, false, err
	}
}

// sendViaRelay is the headless path (-send): it requires
// exactly one paired device, because there is no window to ask in.
func sendViaRelay(a *app, files []string) (client.Result, client.Peer, error) {
	peers, err := client.Peers(a.pairedPath)
	if err != nil {
		return client.Result{}, client.Peer{}, fmt.Errorf("读取配对表失败: %w", err)
	}
	switch len(peers) {
	case 0:
		return client.Result{}, client.Peer{}, fmt.Errorf("没有已配对设备：先在两端完成配对（ferry pair --show / --add，或 6 位码）")
	case 1:
	default:
		names := make([]string, 0, len(peers))
		for _, p := range peers {
			names = append(names, p.Name+"("+truncID(p.ID)+")")
		}
		return client.Result{}, client.Peer{}, fmt.Errorf("已配对 %d 台设备（%s），请通过正在运行的轻渡发送以选择接收方", len(peers), strings.Join(names, "、"))
	}
	res, err := sendToPeer(a, files, peers[0])
	return res, peers[0], err
}

func describeDesktopPaths(paths []string) ([]transfer.SendItem, int64, string, error) {
	items, err := transfer.ExpandSendItems(paths)
	if err != nil {
		return nil, 0, "", err
	}
	var totalBytes int64
	for _, it := range items {
		if st, serr := os.Stat(it.DiskPath); serr == nil {
			totalBytes += st.Size()
		}
	}
	return items, totalBytes, items[0].RelPath, nil
}

func summarizeItems(items []transfer.SendItem) string {
	if len(items) == 1 {
		return sanitizeLedgerText(filepath.Base(filepath.FromSlash(items[0].RelPath)))
	}
	return fmt.Sprintf("%d 个文件", len(items))
}

func summarizeFiles(files []string) string {
	if len(files) == 1 {
		if st, err := os.Stat(files[0]); err == nil && st.IsDir() {
			return fmt.Sprintf("文件夹「%s」", sanitizeLedgerText(filepath.Base(filepath.Clean(files[0]))))
		}
		return sanitizeLedgerText(filepath.Base(filepath.FromSlash(files[0])))
	}
	return fmt.Sprintf("%d 个文件", len(files))
}

// sendToPeer pushes files/folders to one paired device and logs the outcome.
func sendToPeer(a *app, files []string, peer client.Peer) (client.Result, error) {
	items, totalBytes, firstRel, err := describeDesktopPaths(files)
	if err != nil || len(items) == 0 {
		return client.Result{}, fmt.Errorf("所选文件夹内没有可发送的文件")
	}
	txID := a.beginTransfer("send", peerLabel(peer), firstRel, len(items), totalBytes)
	ctrl := transfer.NewController()
	a.attachTransferControl(txID, ctrl, true, true)
	defer a.endTransfer(txID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	opts := client.Options{
		SignalURL:   a.currentSignalURL(),
		KeyPath:     a.keyPath,
		PairedPath:  a.pairedPath,
		Name:        a.deviceName(),
		STUNServers: a.currentSTUNServers(),
		CandRoute:   a.registerCandRoute,
		Control:     ctrl,
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

	var res client.Result
	if lanAddr := a.peerLANAddr(peer.ID); lanAddr != "" {
		res, err = client.SendLAN(ctx, opts, lanAddr, files)
		if err != nil && !ctrl.IsCancelled() && !transfer.IsCancelled(err) {
			a.event("desktop.lan_fallback", map[string]any{"to": truncID(peer.ID), "addr": lanAddr, "error": err.Error()})
			a.updateTransfer(txID, "connecting", "", "", firstRel, 1, len(items), 0, totalBytes)
		}
	}
	if res.Channel == "" && !ctrl.IsCancelled() && !transfer.IsCancelled(err) {
		if sigCl := a.activeSignalClient(); sigCl != nil {
			opts.SignalClient = sigCl
		} else {
			release := a.holdSignaling()
			defer release()
		}
		res, err = client.SendAuto(ctx, opts, peer.ID, files)
	}
	if err != nil {
		if ctrl.IsCancelled() {
			a.event("desktop.send_cancelled", map[string]any{"to": truncID(peer.ID)})
			a.note(fmt.Sprintf("%s  → %s · %s · 已取消", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items)))
			a.notifyMainPaint()
			a.notifyPeersReload()
			return client.Result{}, fmt.Errorf("已取消传输")
		}
		if transfer.IsCancelled(err) {
			a.event("desktop.send_peer_cancelled", map[string]any{"to": truncID(peer.ID)})
			a.note(fmt.Sprintf("%s  → %s · %s · 对方已取消", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items)))
			a.notifyMainPaint()
			a.notifyPeersReload()
			return client.Result{}, fmt.Errorf("对方已取消传输")
		}
		a.event("desktop.send_error", map[string]any{"to": truncID(peer.ID), "error": err.Error()})
		a.note(fmt.Sprintf("%s  → %s · %s · 失败", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items)))
		a.notifyMainPaint()
		a.notifyPeersReload()
		return client.Result{}, err
	}
	if res.PeerName != "" && res.PeerName != peer.Name {
		peer.Name = res.PeerName
		a.updateTransfer(txID, "", "", res.PeerName, "", 0, 0, -1, 0)
		a.notifyPeersReload()
	}
	chLabel := res.Channel
	if chLabel == "" {
		chLabel = "中继"
	}
	a.event("desktop.send_done", map[string]any{
		"to": truncID(peer.ID), "channel": chLabel, "bytes": res.Bytes, "chunks": res.Chunks,
		"duration_ms": res.Duration.Milliseconds(), "transfer_id": res.TransferID,
	})
	a.note(fmt.Sprintf("%s  → %s · %s · %s · %s · %s",
		time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeItems(items), humanBytes(res.Bytes), fmtDur(res.Duration), chLabel))
	a.notifyMainPaint()
	return res, nil
}

// sendToLANTarget pushes files/folders directly over TCP to an unpaired LAN peer
// (temporary transfer; receiver must have its main UI open and confirm).
func sendToLANTarget(a *app, files []string, targetName, addr string) (client.Result, error) {
	items, totalBytes, firstRel, err := describeDesktopPaths(files)
	if err != nil || len(items) == 0 {
		return client.Result{}, fmt.Errorf("所选文件夹内没有可发送的文件")
	}
	label := strings.TrimSpace(targetName)
	if label == "" {
		label = addr
	}
	txID := a.beginTransfer("send", label, firstRel, len(items), totalBytes)
	ctrl := transfer.NewController()
	a.attachTransferControl(txID, ctrl, true, true)
	defer a.endTransfer(txID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	opts := client.Options{
		KeyPath: a.keyPath,
		Name:    a.deviceName(),
		Control: ctrl,
		OnStatus: func(st client.Status) {
			a.updateTransfer(txID, st.Stage, "LAN", "", st.FileName, st.FileIndex, st.FileCount, st.BytesDone, st.BytesTotal)
		},
	}
	res, err := client.SendLAN(ctx, opts, addr, files)
	if err != nil {
		if ctrl.IsCancelled() {
			a.event("desktop.lan_send_cancelled", map[string]any{"addr": addr})
			a.note(fmt.Sprintf("%s  → %s · %s · 已取消", time.Now().Format("15:04"), sanitizeLedgerText(label), summarizeItems(items)))
			a.notifyMainPaint()
			return client.Result{}, fmt.Errorf("已取消传输")
		}
		if transfer.IsCancelled(err) {
			a.event("desktop.lan_send_peer_cancelled", map[string]any{"addr": addr})
			a.note(fmt.Sprintf("%s  → %s · %s · 对方已取消", time.Now().Format("15:04"), sanitizeLedgerText(label), summarizeItems(items)))
			a.notifyMainPaint()
			return client.Result{}, fmt.Errorf("对方已取消传输")
		}
		a.event("desktop.lan_send_error", map[string]any{"addr": addr, "error": err.Error()})
		a.note(fmt.Sprintf("%s  → %s · %s · 失败", time.Now().Format("15:04"), sanitizeLedgerText(label), summarizeItems(items)))
		a.notifyMainPaint()
		return client.Result{}, err
	}
	if res.PeerName != "" {
		label = res.PeerName
	}
	a.event("desktop.lan_send_done", map[string]any{
		"addr": addr, "peer": label, "bytes": res.Bytes, "chunks": res.Chunks,
		"duration_ms": res.Duration.Milliseconds(), "transfer_id": res.TransferID,
	})
	a.note(fmt.Sprintf("%s  → %s · %s · %s · %s · LAN",
		time.Now().Format("15:04"), sanitizeLedgerText(label), summarizeItems(items), humanBytes(res.Bytes), fmtDur(res.Duration)))
	a.notifyMainPaint()
	return res, nil
}

func fmtDur(d time.Duration) string {
	if d < time.Second {
		ms := d.Milliseconds()
		if ms < 1 {
			ms = 1
		}
		return fmt.Sprintf("%d ms", ms)
	}
	if d < time.Minute {
		return fmt.Sprintf("%.1f s", d.Seconds())
	}
	return fmt.Sprintf("%.1f min", d.Minutes())
}

func sanitizeLedgerText(s string) string {
	s = strings.ReplaceAll(s, "·", "-")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

// pickPeer shows a popup menu with one entry per paired device.
func pickPeer(peers []client.Peer) (client.Peer, bool) {
	menu, _, _ := procCreatePopupMenu.Call()
	if menu == 0 {
		return client.Peer{}, false
	}
	defer procDestroyMenu.Call(menu)
	const base = 100
	for i, p := range peers {
		label := p.Name
		if label == "" {
			label = truncID(p.ID)
		}
		appendMenu(menu, mfString, uint16(base+i), fmt.Sprintf("发送到 %s", label))
	}
	var pt pointT
	procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
	hwnd := uintptr(0)
	if dropHandle != nil {
		hwnd = uintptr(dropHandle.hwnd)
	} else if trayHandle != nil && trayHandle.hwnd != 0 {
		hwnd = uintptr(trayHandle.hwnd)
	}
	if hwnd != 0 {
		procSetForegroundWindow.Call(hwnd)
	}
	cmd, _, _ := procTrackPopupMenu.Call(menu, tpmRightButton|tpmLeftAlign|tpmReturnCmd,
		uintptr(pt.x), uintptr(pt.y), 0, hwnd, 0)
	procPostMessageW.Call(hwnd, 0, 0, 0)
	if cmd == 0 {
		return client.Peer{}, false
	}
	i := int(uint16(cmd)) - base
	if i < 0 || i >= len(peers) {
		return client.Peer{}, false
	}
	return peers[i], true
}

// setLabel rewrites the drop window text and repaints it.
func (d *dropWindow) setLabel(text string) {
	d.label = text
	if d.hwnd != 0 {
		procInvalidateRect.Call(uintptr(d.hwnd), 0, 1)
	}
}

func truncID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
