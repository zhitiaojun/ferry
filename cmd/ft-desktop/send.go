//go:build windows

package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cwlindean/filetransfer/core/client"
)

// sendFiles sends the dropped files to the paired device over the relay. With
// more than one paired device it stops: picking the receiver is the tray menu's
// job (next step), and silently choosing one would be surprising.
func (d *dropWindow) sendFiles(files []string) error {
	d.setLabel("正在发送…")
	res, peer, err := sendViaRelay(d.app, files)
	if err != nil {
		d.setLabel(fmt.Sprintf("发送失败：%v", err))
		return err
	}
	d.setLabel(fmt.Sprintf("已发送给 %s（%d 个文件）", peer.Name, len(res.Files)))
	return nil
}

// sendViaRelay picks the only paired device and pushes the files over the relay.
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
		return client.Result{}, client.Peer{}, fmt.Errorf("已配对 %d 台设备（%s），此版本还不支持选择接收方", len(peers), strings.Join(names, "、"))
	}

	peer := peers[0]
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	res, err := client.SendFiles(ctx, client.Options{
		SignalURL:  a.signalURL,
		KeyPath:    a.keyPath,
		PairedPath: a.pairedPath,
		Name:       "windows-desktop",
	}, peer.ID, files)
	if err != nil {
		a.event("desktop.send_error", map[string]any{"to": truncID(peer.ID), "error": err.Error()})
		return client.Result{}, peer, err
	}
	a.event("desktop.send_done", map[string]any{
		"to": truncID(peer.ID), "bytes": res.Bytes, "chunks": res.Chunks,
		"duration_ms": res.Duration.Milliseconds(), "transfer_id": res.TransferID,
	})
	return res, peer, nil
}

// sendFiles is the drop window's action; it reports the outcome in the box.

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
