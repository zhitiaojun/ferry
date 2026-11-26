package client

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/zhitiaojun/ferry/core/discovery"
	"github.com/zhitiaojun/ferry/core/identity"
	"github.com/zhitiaojun/ferry/core/pairing"
	"github.com/zhitiaojun/ferry/core/session"
	"github.com/zhitiaojun/ferry/core/transfer"
)

// ErrLANHijacked reports that an outbound LAN socket left through a TUN fake-ip
// adapter instead of a physical NIC, meaning a VPN client intercepted the route.
var ErrLANHijacked = errors.New("LAN path went through a VPN tunnel")

// CheckLANPath verifies nc is not routed out through a TUN fake-ip interface
// toward a non-fake-ip peer, failing fast instead of stalling for HandshakeTimeout.
func CheckLANPath(nc net.Conn) error {
	if nc == nil {
		return nil
	}
	lAddr, lok := nc.LocalAddr().(*net.TCPAddr)
	rAddr, rok := nc.RemoteAddr().(*net.TCPAddr)
	if !lok || !rok {
		return nil
	}
	if discovery.IsTunnelFakeIP(lAddr.IP) && !discovery.IsTunnelFakeIP(rAddr.IP) {
		return fmt.Errorf("%w (%s -> %s)", ErrLANHijacked, lAddr.IP, rAddr.IP)
	}
	return nil
}

// SendLAN sends files directly over TCP to a LAN-discovered peer without
// touching the signaling server.
func SendLAN(ctx context.Context, o Options, addr string, files []string) (Result, error) {
	if len(files) == 0 {
		return Result{}, fmt.Errorf("no files to send")
	}
	if addr == "" {
		return Result{}, fmt.Errorf("empty LAN address")
	}
	if o.Control != nil {
		if o.Control.IsCancelled() {
			return Result{}, transfer.ErrCancelled
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithCancel(ctx)
		defer cancel()
		unreg := o.Control.OnCancel(cancel)
		defer unreg()
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

	key := o.Key
	if key == nil {
		key, err = identity.LoadOrCreateKey(o.KeyPath)
		if err != nil {
			return Result{}, fmt.Errorf("device key %s: %w", o.KeyPath, err)
		}
	}
	dialTimeout := o.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 3 * time.Second
	}
	nc, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp", addr)
	if err != nil {
		return Result{}, fmt.Errorf("lan dial %s: %w", addr, err)
	}
	if err := CheckLANPath(nc); err != nil {
		_ = nc.Close()
		return Result{}, err
	}
	s, err := session.Dial(nc, sessionParams(key, pairing.SanitizeName(o.Name)))
	if err != nil {
		_ = nc.Close()
		return Result{}, fmt.Errorf("lan handshake: %w", err)
	}
	defer s.Close()

	peerName := pairing.SanitizeName(s.Peer.Name)
	peerIDHex := hex.EncodeToString(s.Peer.DeviceID[:])
	if o.PairedPath != "" && peerName != "" && peerIDHex != "" {
		identity.UpdatePeerName(o.PairedPath, peerIDHex, peerName)
	}
	emitStatus(Status{
		Stage:      "waiting",
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
		ChunkSize: uint32(o.Chunk), Timeout: o.Timeout, StartTimeout: 65 * time.Second, Control: o.Control, Log: o.Log,
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
