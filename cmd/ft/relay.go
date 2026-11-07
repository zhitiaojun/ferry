package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/identity"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/proto"
	"github.com/cwlindean/filetransfer/core/relay"
	"github.com/cwlindean/filetransfer/core/session"
	"github.com/cwlindean/filetransfer/core/signaling"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// relayOptions carries what both relay directions need.
type relayOptions struct {
	SignalURL string
	KeyPath   string
	Name      string
	ToID      string
	Files     []string
	OutDir    string
	Chunk     int
	Timeout   time.Duration
	Log       *logging.Logger
}

// defaultKeyPath is ~/.ft/device.key.
func defaultKeyPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "device.key"
	}
	return filepath.Join(home, ".ft", "device.key")
}

func randomHex16() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("read random: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func loadDeviceKey(path string) (*identity.Key, error) {
	k, err := identity.LoadOrCreateKey(path)
	if err != nil {
		return nil, fmt.Errorf("device key %s: %w", path, err)
	}
	return k, nil
}

func sessionParams(key *identity.Key, name string) session.Params {
	return session.Params{DeviceID: key.ID(), Name: name, Capabilities: proto.CapResume}
}

// sendViaRelay logs in, asks the peer to accept, books a relay room and runs
// the normal session over the spliced stream (protocol §8.5).
func sendViaRelay(ctx context.Context, o relayOptions) (summary, error) {
	key, err := loadDeviceKey(o.KeyPath)
	if err != nil {
		return summary{}, err
	}
	client, err := signaling.Dial(ctx, o.SignalURL, key, o.Log)
	if err != nil {
		return summary{}, err
	}
	defer client.Close()

	files, total, err := describeFiles(o.Files)
	if err != nil {
		return summary{}, err
	}
	sessionID, err := randomHex16()
	if err != nil {
		return summary{}, err
	}
	transferID, err := randomHex16()
	if err != nil {
		return summary{}, err
	}

	req := signaling.SendRequest{
		ToDevice:   o.ToID,
		SessionID:  sessionID,
		TransferID: transferID,
		Files:      files,
	}
	// The peer's answer is forwarded with a fresh envelope id, so it arrives as
	// an event rather than as the reply to our call.
	respCh := make(chan signaling.SendResponse, 1)
	errCh := make(chan error, 1)
	var once sync.Once
	client.On(signaling.TypeSendResp, func(e *signaling.Envelope) {
		r, err := signaling.Decode[signaling.SendResponse](e)
		if err != nil {
			return
		}
		once.Do(func() { respCh <- r })
	})
	client.On(signaling.TypeError, func(e *signaling.Envelope) {
		body, _ := signaling.Decode[signaling.ErrorBody](e)
		once.Do(func() { errCh <- fmt.Errorf("signaling: %s: %s", body.Code, body.Message) })
	})
	if err := client.Send(ctx, signaling.TypeSendRequest, req); err != nil {
		return summary{}, fmt.Errorf("send_request: %w", err)
	}
	var answer signaling.SendResponse
	select {
	case answer = <-respCh:
	case err := <-errCh:
		return summary{}, err
	case <-ctx.Done():
		return summary{}, ctx.Err()
	}
	if !answer.Accept {
		return summary{}, fmt.Errorf("peer rejected the transfer: %s", answer.Reason)
	}
	if o.Log != nil {
		o.Log.Event("signal.accepted", map[string]any{"to": truncHex(o.ToID), "files": len(files), "bytes": total})
	}

	ready, err := openRelayRoom(ctx, client, o.ToID, transferID)
	if err != nil {
		return summary{}, err
	}
	rc, err := relay.Dial(ctx, ready.url)
	if err != nil {
		return summary{}, err
	}
	s, err := session.Dial(rc, sessionParams(key, o.Name))
	if err != nil {
		_ = rc.Close()
		return summary{}, fmt.Errorf("handshake over relay: %w", err)
	}
	defer s.Close()

	start := time.Now()
	res, err := transfer.Send(s, o.Files, transfer.Config{
		ChunkSize: uint32(o.Chunk), Timeout: o.Timeout, Log: o.Log,
	})
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK: true, Path: "relay", Peer: truncHex(o.ToID),
		TransferID: transferID, Files: res.Files, Bytes: res.Bytes,
		Chunks: res.Chunks, DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

// recvViaRelay logs in, auto-accepts the next transfer request, joins the relay
// room and receives.
func recvViaRelay(ctx context.Context, o relayOptions) (summary, error) {
	key, err := loadDeviceKey(o.KeyPath)
	if err != nil {
		return summary{}, err
	}
	client, err := signaling.Dial(ctx, o.SignalURL, key, o.Log)
	if err != nil {
		return summary{}, err
	}
	defer client.Close()

	readyCh := make(chan signaling.RelayReady, 1)
	client.On(signaling.TypeRelayReady, func(e *signaling.Envelope) {
		r, err := signaling.Decode[signaling.RelayReady](e)
		if err == nil {
			select {
			case readyCh <- r:
			default:
			}
		}
	})
	client.On(signaling.TypeSendRequest, func(e *signaling.Envelope) {
		req, err := signaling.Decode[signaling.SendRequest](e)
		if err != nil {
			return
		}
		if o.Log != nil {
			o.Log.Event("signal.request", map[string]any{"from": truncHex(req.FromDevice), "files": len(req.Files)})
		}
		_ = client.Send(context.Background(), signaling.TypeSendResp, signaling.SendResponse{
			ToDevice: req.FromDevice, TransferID: req.TransferID, Accept: true,
		})
	})

	var ready signaling.RelayReady
	select {
	case ready = <-readyCh:
	case <-ctx.Done():
		return summary{}, ctx.Err()
	}
	rc, err := relay.Dial(ctx, readyURL(o.SignalURL, ready))
	if err != nil {
		return summary{}, err
	}
	s, err := session.Accept(rc, sessionParams(key, o.Name))
	if err != nil {
		_ = rc.Close()
		return summary{}, fmt.Errorf("handshake over relay: %w", err)
	}
	defer s.Close()

	start := time.Now()
	res, err := transfer.Receive(s, o.OutDir, transfer.Config{
		ChunkSize: uint32(o.Chunk), Timeout: o.Timeout, Log: o.Log,
	})
	if err != nil {
		return summary{}, err
	}
	return summary{
		OK: true, Path: "relay", Peer: s.Peer.Name,
		TransferID: fmt.Sprintf("%x", res.TransferID), Files: res.Files,
		Bytes: res.Bytes, Chunks: res.Chunks, DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

type relayRoom struct {
	url string
}

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

func readyURL(signalURL string, ready signaling.RelayReady) string {
	u, err := relay.RelayURL(signalURL, ready.RelayURL, ready.Token)
	if err != nil {
		return ""
	}
	return u
}

func describeFiles(paths []string) ([]signaling.FileInfo, int64, error) {
	var out []signaling.FileInfo
	var total int64
	for _, p := range paths {
		st, err := os.Stat(p)
		if err != nil {
			return nil, 0, err
		}
		if st.IsDir() {
			return nil, 0, fmt.Errorf("%s is a directory", p)
		}
		out = append(out, signaling.FileInfo{Name: filepath.Base(p), Size: st.Size()})
		total += st.Size()
	}
	return out, total, nil
}

func truncHex(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
