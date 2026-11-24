package mobile

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/logging"
)

// EventListener receives push notifications from the Go transfer core so Android
// does not need a background 1.5 s JNI polling loop.
type EventListener interface {
	OnReceived(eventJSON string)
	OnTransferProgress(statusJSON string)
	OnConnectionChanged(statusJSON string)
}

var (
	evtListenerMu sync.RWMutex
	evtListener   EventListener
)

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
	StartedAt  int64  `json:"started_at,omitempty"`
}

var (
	activeTxMu    sync.Mutex
	activeTxSeq   uint64
	activeTx      ActiveTransfer
	lastProgressT time.Time
)

func beginActiveTransfer(dir, peer, fileName string, fileCount int, total int64) uint64 {
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
		StartedAt:  time.Now().UnixMilli(),
	}
	snap := activeTx
	lastProgressT = time.Now()
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
		activeTx.BytesDone = done
	}
	if total > 0 {
		activeTx.BytesTotal = total
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
