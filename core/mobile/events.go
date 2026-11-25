package mobile

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/proto"
)

// EventListener receives push notifications from the Go transfer core so Android
// does not need a background 1.5 s JNI polling loop.
type EventListener interface {
	OnReceived(eventJSON string)
	OnTransferProgress(statusJSON string)
	OnConnectionChanged(statusJSON string)
	OnConfirmIncoming(reqJSON string)
}

var (
	evtListenerMu sync.RWMutex
	evtListener   EventListener

	mainUIOpen atomic.Bool

	confirmMu  sync.Mutex
	confirmSeq atomic.Uint64
	confirmMap = map[string]chan bool{}
)

// SetMainUIOpen records whether the Android MainActivity is currently open in the
// foreground. Temporary (unpaired) LAN incoming transfers are only accepted
// while the main UI is open; transitioning to false cancels pending prompts.
func SetMainUIOpen(open bool) {
	mainUIOpen.Store(open)
	if !open {
		confirmMu.Lock()
		for id, ch := range confirmMap {
			select {
			case ch <- false:
			default:
			}
			delete(confirmMap, id)
		}
		confirmMu.Unlock()
	}
}

// IsMainUIOpen reports whether the Android MainActivity is currently open.
func IsMainUIOpen() bool {
	return mainUIOpen.Load()
}

// RespondIncoming resolves a pending unpaired LAN confirmation prompt started
// by OnConfirmIncoming.
func RespondIncoming(reqID string, accept bool) {
	confirmMu.Lock()
	ch, ok := confirmMap[reqID]
	if ok {
		delete(confirmMap, reqID)
	}
	confirmMu.Unlock()
	if ok {
		select {
		case ch <- accept:
		default:
		}
	}
}

type incomingConfirmReq struct {
	ID         string   `json:"id"`
	Peer       string   `json:"peer"`
	Addr       string   `json:"addr,omitempty"`
	FileCount  int      `json:"file_count"`
	TotalBytes int64    `json:"total_bytes"`
	Files      []string `json:"files,omitempty"`
}

func remoteHost(nc net.Conn) string {
	if nc == nil || nc.RemoteAddr() == nil {
		return ""
	}
	raw := nc.RemoteAddr().String()
	if host, _, err := net.SplitHostPort(raw); err == nil {
		return host
	}
	return raw
}

func requestIncomingConfirm(peerName, remoteAddr string, m *proto.Manifest) error {
	if !IsMainUIOpen() {
		return proto.NewError(proto.CodeUnauthorized, "对方未打开轻渡主界面，无法接收局域网临时文件")
	}
	l := currentListener()
	if l == nil {
		return proto.NewError(proto.CodeUnauthorized, "对方未打开轻渡主界面，无法接收局域网临时文件")
	}

	reqID := fmt.Sprintf("lan-%d", confirmSeq.Add(1))
	ch := make(chan bool, 1)
	confirmMu.Lock()
	confirmMap[reqID] = ch
	confirmMu.Unlock()
	defer func() {
		confirmMu.Lock()
		delete(confirmMap, reqID)
		confirmMu.Unlock()
	}()

	// Re-check after registration in case SetMainUIOpen(false) raced.
	if !IsMainUIOpen() {
		return proto.NewError(proto.CodeUnauthorized, "对方未打开轻渡主界面，无法接收局域网临时文件")
	}

	var total int64
	limit := len(m.Files)
	if limit > 5 {
		limit = 5
	}
	names := make([]string, 0, limit)
	for i, f := range m.Files {
		total += int64(f.Size)
		if i < limit {
			names = append(names, f.Name)
		}
	}
	payload, err := json.Marshal(incomingConfirmReq{
		ID:         reqID,
		Peer:       peerName,
		Addr:       remoteAddr,
		FileCount:  len(m.Files),
		TotalBytes: total,
		Files:      names,
	})
	if err != nil {
		return err
	}
	go l.OnConfirmIncoming(string(payload))

	timer := time.NewTimer(60 * time.Second)
	defer timer.Stop()
	select {
	case accept := <-ch:
		if !accept {
			if !IsMainUIOpen() {
				return proto.NewError(proto.CodeCancelled, "对方已关闭主界面")
			}
			return proto.NewError(proto.CodeCancelled, "对方拒绝接收")
		}
		return nil
	case <-timer.C:
		return proto.NewError(proto.CodeTimeout, "等待对方确认超时")
	}
}

// SetEventListener registers (or clears with nil) the active EventListener.
func SetEventListener(l EventListener) {
	evtListenerMu.Lock()
	evtListener = l
	evtListenerMu.Unlock()
}

func currentListener() EventListener {
	evtListenerMu.RLock()
	l := evtListener
	evtListenerMu.RUnlock()
	return l
}

func notifyConnectionChanged() {
	l := currentListener()
	if l == nil {
		return
	}
	snap := ConnectionStatus()
	go l.OnConnectionChanged(snap)
}

func notifyProgress(snap ActiveTransfer) {
	l := currentListener()
	if l == nil {
		return
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return
	}
	go l.OnTransferProgress(string(b))
}

func notifyReceived(evts []ReceivedEvent) {
	l := currentListener()
	if l == nil || len(evts) == 0 {
		return
	}
	b, err := json.Marshal(evts)
	if err != nil {
		return
	}
	go l.OnReceived(string(b))
}

// ReceivedEvent describes one completed incoming file waiting for Android UI
// export into public Downloads.
type ReceivedEvent struct {
	File     string `json:"file"`
	FullPath string `json:"full_path"`
	Via      string `json:"via"`
	Peer     string `json:"peer,omitempty"`
	Bytes    int64  `json:"bytes"`
	Ts       int64  `json:"ts"`
}

var (
	recvEvtMu sync.Mutex
	recvEvts  []ReceivedEvent
)

func recordReceived(outDir, via string, files []string, totalBytes int64) {
	recordReceivedFrom(outDir, via, "", files, totalBytes)
}

func recordReceivedFrom(outDir, via, peer string, files []string, totalBytes int64) {
	if len(files) == 0 {
		return
	}
	now := time.Now().UnixMilli()
	added := make([]ReceivedEvent, 0, len(files))
	recvEvtMu.Lock()
	for _, f := range files {
		name := filepath.Base(f)
		full := filepath.Join(outDir, name)
		sz := int64(0)
		if info, err := os.Stat(full); err == nil {
			sz = info.Size()
		} else if len(files) == 1 {
			sz = totalBytes
		}
		ev := ReceivedEvent{
			File:     name,
			FullPath: full,
			Via:      via,
			Peer:     peer,
			Bytes:    sz,
			Ts:       now,
		}
		recvEvts = append(recvEvts, ev)
		added = append(added, ev)
	}
	recvEvtMu.Unlock()
	notifyReceived(added)
}

// PollReceived drains pending received-file events as a JSON array ("[]").
func PollReceived() string {
	recvEvtMu.Lock()
	if len(recvEvts) == 0 {
		recvEvtMu.Unlock()
		return "[]"
	}
	batch := recvEvts
	recvEvts = nil
	recvEvtMu.Unlock()

	b, err := json.Marshal(batch)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// ActiveTransfer describes the currently running send or receive transfer.
type ActiveTransfer struct {
	Active     bool   `json:"active"`
	Direction  string `json:"direction,omitempty"` // "send" | "recv"
	Peer       string `json:"peer,omitempty"`
	Stage      string `json:"stage,omitempty"`   // "connecting" | "racing" | "transferring" | "verifying"
	Channel    string `json:"channel,omitempty"` // "" | "lan" | "punch" | "relay"
	FileIndex  int    `json:"file_index,omitempty"`
	FileCount  int    `json:"file_count,omitempty"`
	FileName   string `json:"file_name,omitempty"`
	BytesDone  int64  `json:"bytes_done,omitempty"`
	BytesTotal int64  `json:"bytes_total,omitempty"`
	SpeedBPS   int64  `json:"speed_bps,omitempty"`
	StartedAt  int64  `json:"started_at,omitempty"`
}

type txSpeedSample struct {
	t     time.Time
	bytes int64
}

var (
	activeTxMu    sync.Mutex
	activeTxSeq   uint64
	activeTx      ActiveTransfer
	txSamples     []txSpeedSample
	lastProgressT time.Time
)

func beginActiveTransfer(dir, peer, fileName string, fileCount int, total int64) uint64 {
	now := time.Now()
	activeTxMu.Lock()
	activeTxSeq++
	activeTx = ActiveTransfer{
		Active:     true,
		Direction:  dir,
		Peer:       peer,
		Stage:      "connecting",
		FileIndex:  1,
		FileCount:  fileCount,
		FileName:   fileName,
		BytesTotal: total,
		StartedAt:  now.UnixMilli(),
	}
	txSamples = []txSpeedSample{{t: now, bytes: 0}}
	snap := activeTx
	lastProgressT = now
	seq := activeTxSeq
	activeTxMu.Unlock()
	notifyProgress(snap)
	return seq
}

func updateActiveTransfer(seq uint64, stage, channel, peer, fileName string, idx, cnt int, done, total int64) {
	now := time.Now()
	activeTxMu.Lock()
	if !activeTx.Active || activeTxSeq != seq {
		activeTxMu.Unlock()
		return
	}
	major := false
	if stage != "" && stage != activeTx.Stage {
		activeTx.Stage = stage
		major = true
	}
	if channel != "" && channel != activeTx.Channel {
		activeTx.Channel = channel
		major = true
	}
	if peer != "" && peer != activeTx.Peer {
		activeTx.Peer = peer
		major = true
	}
	if fileName != "" {
		activeTx.FileName = fileName
	}
	if idx > 0 {
		activeTx.FileIndex = idx
	}
	if cnt > 0 {
		activeTx.FileCount = cnt
	}
	if done >= 0 {
		prevDone := activeTx.BytesDone
		activeTx.BytesDone = done
		if done < prevDone || len(txSamples) == 0 {
			txSamples = []txSpeedSample{{t: now, bytes: done}}
		} else {
			txSamples = append(txSamples, txSpeedSample{t: now, bytes: done})
			for len(txSamples) > 2 && now.Sub(txSamples[0].t) > 2*time.Second {
				txSamples = txSamples[1:]
			}
		}
	}
	if total > 0 {
		activeTx.BytesTotal = total
	}
	if activeTx.Stage == "verifying" {
		activeTx.SpeedBPS = 0
	} else {
		var spd int64
		if len(txSamples) >= 2 {
			first := txSamples[0]
			dt := now.Sub(first.t).Seconds()
			db := activeTx.BytesDone - first.bytes
			if dt >= 0.15 && db > 0 {
				spd = int64(float64(db) / dt)
			}
		}
		if spd == 0 && activeTx.BytesDone > 0 && activeTx.StartedAt > 0 {
			elapsed := float64(now.UnixMilli()-activeTx.StartedAt) / 1000.0
			if elapsed >= 0.5 {
				spd = int64(float64(activeTx.BytesDone) / elapsed)
			}
		}
		activeTx.SpeedBPS = spd
	}
	shouldNotify := major || now.Sub(lastProgressT) >= 150*time.Millisecond
	var snap ActiveTransfer
	if shouldNotify {
		lastProgressT = now
		snap = activeTx
	}
	activeTxMu.Unlock()
	if shouldNotify {
		notifyProgress(snap)
	}
}

func endActiveTransfer(seq uint64) {
	activeTxMu.Lock()
	cleared := false
	if activeTxSeq == seq {
		activeTx = ActiveTransfer{}
		txSamples = nil
		cleared = true
	}
	activeTxMu.Unlock()
	if cleared {
		notifyProgress(ActiveTransfer{Active: false})
	}
}

func hasActiveTransfer() bool {
	activeTxMu.Lock()
	active := activeTx.Active
	activeTxMu.Unlock()
	return active
}

// ActiveTransferStatus returns the current live transfer state as JSON
// ({"active":false} when no transfer is in flight).
func ActiveTransferStatus() string {
	activeTxMu.Lock()
	snap := activeTx
	activeTxMu.Unlock()
	if !snap.Active {
		return `{"active":false}`
	}
	b, err := json.Marshal(snap)
	if err != nil {
		return `{"active":false}`
	}
	return string(b)
}

func stateLogPath(outDir, name string) string {
	stateDir := filepath.Join(filepath.Dir(filepath.Clean(outDir)), "state")
	_ = os.MkdirAll(stateDir, 0o755)
	return filepath.Join(stateDir, name)
}

func openRotatingLoggerFile(outDir string) (*logging.Logger, io.Closer) {
	l, c, err := logging.OpenRotating(stateLogPath(outDir, "events.jsonl"), logging.DefaultMaxBytes)
	if err != nil {
		return nil, nil
	}
	return l, c
}
