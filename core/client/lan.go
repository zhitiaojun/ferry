package client

import (
	"context"
	"encoding/hex"
	"fmt"
	"net"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// SendLAN sends files directly over TCP to a LAN-discovered peer without
// touching the signaling server.
func SendLAN(ctx context.Context, o Options, addr string, files []string) (Result, error) {
	if len(files) == 0 {
		return Result{}, fmt.Errorf("no files to send")
	}
	if addr == "" {
		return Result{}, fmt.Errorf("empty LAN address")
	}
	infos, total, err := describeFiles(files)
	if err != nil {
		return Result{}, err
	}
	firstName := infos[0].Name
	emitStatus := func(st Status) {
		if o.OnStatus != nil {
			o.OnStatus(st)
		}
	}
	emitStatus(Status{
		Stage:      "connecting",
		Channel:    "lan",
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	key, err := identity.LoadOrCreateKey(o.KeyPath)
	if err != nil {
		return Result{}, fmt.Errorf("device key %s: %w", o.KeyPath, err)
	}
	nc, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return Result{}, fmt.Errorf("lan dial %s: %w", addr, err)
	}
	s, err := session.Dial(nc, sessionParams(key, pairing.SanitizeName(o.Name)))
	if err != nil {
		_ = nc.Close()
		return Result{}, fmt.Errorf("lan handshake: %w", err)
	}
	defer s.Drain(2 * time.Second)

	peerName := pairing.SanitizeName(s.Peer.Name)
	peerIDHex := hex.EncodeToString(s.Peer.DeviceID[:])
	if o.PairedPath != "" && peerName != "" && peerIDHex != "" {
		identity.UpdatePeerName(o.PairedPath, peerIDHex, peerName)
	}
	emitStatus(Status{
		Stage:      "transferring",
		Channel:    "lan",
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	tid, err := randomHex16()
	if err != nil {
		tid = ""
	}
	start := time.Now()
	res, err := transfer.Send(s, files, transfer.Config{
		ChunkSize: uint32(o.Chunk), Timeout: o.Timeout, Log: o.Log,
		TransferID: hex16(tid),
		OnDetailProgress: func(snap transfer.ProgressSnapshot) {
			emitStatus(Status{
				Stage:      snap.Stage,
				Channel:    "lan",
				FileIndex:  snap.FileIndex,
				FileCount:  snap.FileCount,
				FileName:   snap.FileName,
				BytesDone:  snap.BytesDone,
				BytesTotal: snap.BytesTotal,
			})
		},
	})
	if err != nil {
		return Result{}, err
	}
	if o.Log != nil {
		o.Log.Event("lan.send_done", map[string]any{"peer": truncHex(peerIDHex), "addr": addr, "files": len(res.Files), "bytes": res.Bytes})
	}
	return Result{
		TransferID: tid, Channel: "LAN", PeerName: peerName, Files: res.Files, Bytes: res.Bytes,
		Chunks: res.Chunks, Duration: time.Since(start),
	}, nil
}
