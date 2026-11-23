package mobile

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

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
	recvEvtMu.Lock()
	defer recvEvtMu.Unlock()
	for _, f := range files {
		name := filepath.Base(f)
		full := filepath.Join(outDir, name)
		sz := int64(0)
		if info, err := os.Stat(full); err == nil {
			sz = info.Size()
		} else if len(files) == 1 {
			sz = totalBytes
		}
		recvEvts = append(recvEvts, ReceivedEvent{
			File:     name,
			FullPath: full,
			Via:      via,
			Peer:     peer,
			Bytes:    sz,
			Ts:       now,
		})
	}
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
	activeTxMu  sync.Mutex
	activeTxSeq uint64
	activeTx    ActiveTransfer
)

func beginActiveTransfer(dir, peer, fileName string, fileCount int, total int64) uint64 {
	activeTxMu.Lock()
	defer activeTxMu.Unlock()
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
	return activeTxSeq
}

func updateActiveTransfer(seq uint64, stage, channel, peer, fileName string, idx, cnt int, done, total int64) {
	activeTxMu.Lock()
	defer activeTxMu.Unlock()
	if !activeTx.Active || activeTxSeq != seq {
		return
	}
	if stage != "" {
		activeTx.Stage = stage
	}
	if channel != "" {
		activeTx.Channel = channel
	}
	if peer != "" {
		activeTx.Peer = peer
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
}

func endActiveTransfer(seq uint64) {
	activeTxMu.Lock()
	defer activeTxMu.Unlock()
	if activeTxSeq == seq {
		activeTx = ActiveTransfer{}
	}
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
