// Package client carries the "send files to a paired peer" flow shared by the
// CLI (ft send --allow relay) and the Windows desktop shell.
package client

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/discovery"
	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/pairing"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// Status reports live progress stages for file transfers.
type Status struct {
	Stage      string // "connecting" | "waiting" | "racing" | "transferring" | "paused" | "verifying"
	Channel    string // "" | "lan" | "punch" | "relay"
	FileIndex  int
	FileCount  int
	FileName   string
	BytesDone  int64
	BytesTotal int64
}

// Options configures one transfer.
type Options struct {
	SignalURL          string
	SignalClient       *signaling.Client // optional persistent signaling client to reuse
	Key                *identity.Key     // optional pre-loaded device key
	KeyPath            string            // device key file
	PairedPath         string            // trusted-peer table
	Name               string            // device name shown to the peer
	STUNServers        []string          // nil uses DefaultSTUNList; empty non-nil skips STUN
	LANHint            *discovery.Hint   // optional interface hint (Android avoids netlink)
	SkipLANEnum        bool              // true skips discovery.Eligible() when LANHint is nil
	CandRoute          func(sid string, ch chan *signaling.Envelope) func()
	Chunk              int
	Timeout            time.Duration
	DialTimeout        time.Duration // LAN dial timeout (default 3s)
	HotUpgradeMinBytes int64         // 0 defaults to HotUpgradeMinBytes (4 MiB)
	DisableHotUpgrade  bool
	DisablePunch       bool
	NoTLS              bool
	Control            *transfer.Controller
	Log                *logging.Logger
	OnStatus           func(Status)
}

// Result summarises a completed transfer.
type Result struct {
	TransferID string
	Channel    string // "LAN" | "打洞" | "中继"
	PeerName   string
	Files      []string
	Bytes      int64
	Chunks     int
	Duration   time.Duration
}

// Peer is one entry of the trusted-peer table.
type Peer struct {
	ID   string
	Name string
	Pub  ed25519.PublicKey
}

// Peers lists the paired devices, newest first.
func Peers(pairedPath string) ([]Peer, error) {
	store, err := identity.LoadPaired(pairedPath)
	if err != nil {
		return nil, err
	}
	var out []Peer
	for _, d := range store.List() {
		pub, err := d.PublicKeyBytes()
		if err != nil {
			continue
		}
		out = append(out, Peer{ID: d.DeviceID, Name: d.Name, Pub: pub})
	}
	return out, nil
}

// SendFiles logs in, asks toID to accept, books a relay room and runs the file
// transfer over the spliced stream (protocol §8.5).
func SendFiles(ctx context.Context, o Options, toID string, files []string) (Result, error) {
	if len(files) == 0 {
		return Result{}, fmt.Errorf("no files to send")
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
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	key, err := identity.LoadOrCreateKey(o.KeyPath)
	if err != nil {
		return Result{}, fmt.Errorf("device key %s: %w", o.KeyPath, err)
	}
	cl := o.SignalClient
	if cl == nil || !cl.Alive() {
		cl, err = signaling.Dial(ctx, o.SignalURL, key, o.Log)
		if err != nil {
			return Result{}, err
		}
		defer cl.Close()
	} else {
		defer cl.Off(signaling.TypeSendResp)
		defer cl.Off(signaling.TypeError)
	}

	sessionID, err := randomHex16()
	if err != nil {
		return Result{}, err
	}
	transferID, err := randomHex16()
	if err != nil {
		return Result{}, err
	}

	req := signaling.SendRequest{
		ToDevice:   toID,
		SessionID:  sessionID,
		TransferID: transferID,
		Files:      infos,
	}
	// The peer's answer is forwarded under a fresh envelope id, so it arrives as
	// an event rather than as the reply to our call.
	respCh := make(chan signaling.SendResponse, 1)
	errCh := make(chan error, 1)
	var once sync.Once
	cl.On(signaling.TypeSendResp, func(e *signaling.Envelope) {
		r, err := signaling.Decode[signaling.SendResponse](e)
		if err != nil {
			return
		}
		once.Do(func() { respCh <- r })
	})
	cl.On(signaling.TypeError, func(e *signaling.Envelope) {
		body, _ := signaling.Decode[signaling.ErrorBody](e)
		once.Do(func() { errCh <- fmt.Errorf("signaling: %s: %s", body.Code, body.Message) })
	})
	if err := cl.Send(ctx, signaling.TypeSendRequest, req); err != nil {
		return Result{}, fmt.Errorf("send_request: %w", err)
	}
	emitStatus(Status{
		Stage:      "waiting",
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})
	var answer signaling.SendResponse
	select {
	case answer = <-respCh:
	case err := <-errCh:
		return Result{}, err
	case <-cl.Done():
		return Result{}, fmt.Errorf("signaling connection dropped: %v", cl.Err())
	case <-ctx.Done():
		return Result{}, ctx.Err()
	}
	if !answer.Accept {
		return Result{}, fmt.Errorf("peer rejected the transfer: %s", answer.Reason)
	}
	if o.Log != nil {
		o.Log.Event("signal.accepted", map[string]any{"to": truncHex(toID), "files": len(infos), "bytes": total})
	}

	ready, err := openRelayRoom(ctx, cl, toID, transferID)
	if err != nil {
		return Result{}, err
	}
	rc, err := relay.Dial(ctx, ready.url)
	if err != nil {
		return Result{}, err
	}
	conn := net.Conn(rc)
	if !o.NoTLS {
		peer, err := pairedPeer(o.PairedPath, toID)
		if err != nil {
			_ = rc.Close()
			return Result{}, err
		}
		conn, err = clientTLS(ctx, conn, key, peer)
		if err != nil {
			_ = rc.Close()
			return Result{}, err
		}
		if o.Log != nil {
			o.Log.Event("relay.tls", map[string]any{"peer": truncHex(toID), "tls": "1.3", "pinned": true})
		}
	}
	s, err := session.Dial(conn, sessionParams(key, pairing.SanitizeName(o.Name)))
	if err != nil {
		_ = rc.Close()
		return Result{}, fmt.Errorf("handshake over relay: %w", err)
	}
	defer s.Close()

	peerName := pairing.SanitizeName(s.Peer.Name)
	if peerName != "" {
		identity.UpdatePeerName(o.PairedPath, toID, peerName)
	}
	emitStatus(Status{
		Stage:      "transferring",
		Channel:    "relay",
		FileIndex:  1,
		FileCount:  len(infos),
		FileName:   firstName,
		BytesTotal: total,
	})

	start := time.Now()
	res, err := transfer.Send(s, files, transfer.Config{
		ChunkSize: uint32(o.Chunk), Timeout: o.Timeout, Control: o.Control, Log: o.Log,
		TransferID: hex16(transferID),
		OnDetailProgress: func(snap transfer.ProgressSnapshot) {
			emitStatus(Status{
				Stage:      snap.Stage,
				Channel:    "relay",
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
	return Result{
		TransferID: transferID, Channel: "中继", PeerName: peerName, Files: res.Files, Bytes: res.Bytes,
		Chunks: res.Chunks, Duration: time.Since(start),
	}, nil
}

type relayRoom struct{ url string }

func openRelayRoom(ctx context.Context, c *signaling.Client, toID, transferID string) (relayRoom, error) {
	reply, err := c.Call(ctx, signaling.TypeRelayOpen, signaling.RelayOpen{
		ToDevice: toID, TransferID: transferID,
	}, signaling.TypeRelayReady)
	if err != nil {
		return relayRoom{}, fmt.Errorf("relay_open: %w", err)
	}
	ready, err := signaling.Decode[signaling.RelayReady](reply)
	if err != nil {
		return relayRoom{}, err
	}
	return relayRoom{url: readyURL(c.SignalURL(), ready)}, nil
}

func sessionParams(key *identity.Key, name string) session.Params {
	return session.Params{
		DeviceID:     key.ID(),
		Name:         name,
		Capabilities: proto.CapResume | proto.CapRelayFallback | proto.CapHotUpgrade,
		PubKey:       key.Public(),
		Sign:         key.Sign,
	}
}

func describeFiles(paths []string) ([]signaling.FileInfo, int64, error) {
	items, err := transfer.ExpandSendItems(paths)
	if err != nil {
		return nil, 0, err
	}
	out := make([]signaling.FileInfo, 0, len(items))
	var total int64
	for _, it := range items {
		st, err := os.Stat(it.DiskPath)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, signaling.FileInfo{Name: it.RelPath, Size: st.Size()})
		total += st.Size()
	}
	return out, total, nil
}

func randomHex16() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func hex16(s string) [16]byte {
	var out [16]byte
	if len(s) != 32 {
		return out
	}
	if _, err := hex.Decode(out[:], []byte(s)); err != nil {
		return [16]byte{}
	}
	return out
}

func isTransferCancelled(err error) bool {
	if errors.Is(err, transfer.ErrCancelled) || errors.Is(err, context.Canceled) {
		return true
	}
	var rem *session.RemoteError
	return errors.As(err, &rem) && rem.Code == proto.CodeCancelled
}

func truncHex(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
