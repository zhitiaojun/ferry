//go:build windows

package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unsafe"

	"github.com/cwlindean/filetransfer/core/client"
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

// sendViaRelay is the headless path (-send / Explorer "Send to"): it requires
// exactly one paired device, because there is no window to ask in.
func sendViaRelay(a *app, files []string) (client.Result, client.Peer, error) {
	peers, err := client.Peers(a.pairedPath)
	if err != nil {
		return client.Result{}, client.Peer{}, fmt.Errorf("读取配对表失败: %w", err)
	}
	switch len(peers) {
	case 0:
		return client.Result{}, client.Peer{}, fmt.Errorf("没有已配对设备：先在两端完成配对（ft pair --show / --add，或 6 位码）")
	case 1:
	default:
		names := make([]string, 0, len(peers))
		for _, p := range peers {
			names = append(names, p.Name+"("+truncID(p.ID)+")")
		}
		return client.Result{}, client.Peer{}, fmt.Errorf("已配对 %d 台设备（%s），请从托盘投放小窗发送以选择接收方", len(peers), strings.Join(names, "、"))
	}
	res, err := sendToPeer(a, files, peers[0])
	return res, peers[0], err
}

// expandSendPaths flattens any dropped/selected directories into their regular
// file paths because client.SendFiles transfers flat file entries.
func expandSendPaths(paths []string) []string {
	var out []string
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if !st.IsDir() {
			out = append(out, p)
			continue
		}
		_ = filepath.WalkDir(p, func(sub string, d fs.DirEntry, werr error) error {
			if werr == nil && !d.IsDir() {
				out = append(out, sub)
			}
			return nil
		})
	}
	return out
}

func summarizeFiles(files []string) string {
	if len(files) == 1 {
		return sanitizeLedgerText(filepath.Base(files[0]))
	}
	return fmt.Sprintf("%d 个文件", len(files))
}

// sendToPeer pushes files to one paired device and logs the outcome.
func sendToPeer(a *app, files []string, peer client.Peer) (client.Result, error) {
	files = expandSendPaths(files)
	if len(files) == 0 {
		return client.Result{}, fmt.Errorf("所选文件夹内没有可发送的文件")
	}
	var totalBytes int64
	for _, fp := range files {
		if st, serr := os.Stat(fp); serr == nil {
			totalBytes += st.Size()
		}
	}
	txID := a.beginTransfer("send", peerLabel(peer), filepath.Base(files[0]), len(files), totalBytes)
	defer a.endTransfer(txID)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	opts := client.Options{
		SignalURL:   a.signalURL,
		KeyPath:     a.keyPath,
		PairedPath:  a.pairedPath,
		Name:        a.deviceName(),
		STUNServers: a.stunServers,
		CandRoute:   a.registerCandRoute,
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
	var err error
	if lanAddr := a.peerLANAddr(peer.ID); lanAddr != "" {
		res, err = client.SendLAN(ctx, opts, lanAddr, files)
		if err != nil {
			a.event("desktop.lan_fallback", map[string]any{"to": truncID(peer.ID), "addr": lanAddr, "error": err.Error()})
			a.updateTransfer(txID, "connecting", "", "", filepath.Base(files[0]), 1, len(files), 0, totalBytes)
		}
	}
	if res.Channel == "" {
		if sigCl := a.activeSignalClient(); sigCl != nil {
			opts.SignalClient = sigCl
		} else {
			release := a.holdSignaling()
			defer release()
		}
		res, err = client.SendAuto(ctx, opts, peer.ID, files)
	}
	if err != nil {
		a.event("desktop.send_error", map[string]any{"to": truncID(peer.ID), "error": err.Error()})
		a.note(fmt.Sprintf("%s  → %s · %s · 失败", time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeFiles(files)))
		return client.Result{}, err
	}
	if res.PeerName != "" && res.PeerName != peer.Name {
		peer.Name = res.PeerName
		a.updateTransfer(txID, "", "", res.PeerName, "", 0, 0, -1, 0)
		if thePanel != nil {
			thePanel.reloadPeers()
		}
		if h := existingMainWindow(); h != 0 {
			procPostMessageW.Call(uintptr(h), wmAppReload, 0, 0)
		}
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
		time.Now().Format("15:04"), sanitizeLedgerText(peerLabel(peer)), summarizeFiles(files), humanBytes(res.Bytes), fmtDur(res.Duration), chLabel))
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
	}
	procSetForegroundWindow.Call(hwnd)
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
