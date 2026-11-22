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

func stateLogPath(outDir, name string) string {
	stateDir := filepath.Join(filepath.Dir(filepath.Clean(outDir)), "state")
	_ = os.MkdirAll(stateDir, 0o755)
	return filepath.Join(stateDir, name)
}
