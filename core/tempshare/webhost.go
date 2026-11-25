// Package tempshare implements client-initiated temporary file transfer
// sessions that work identically for browsers (/s/<token>) and Ferry apps
// (LAN-first with automatic fallback to the BJ relay).
package tempshare

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"os"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/transfer"
)

const webChunk = 256 << 10

type webHost struct {
	srv        *lanweb.Server
	log        *logging.Logger
	conn       *websocket.Conn
	onProgress func(TransferProgress)

	writeMu sync.Mutex

	getMu     sync.Mutex
	getActive bool
	getCtrl   *transfer.Controller
	getTxID   uint64
	getName   string
	getDone   int64
	getTotal  int64
	getWg     sync.WaitGroup

	putMu     sync.Mutex
	part      *os.File
	partName  string
	partPath  string
	partHash  hash.Hash
	partSize  int64
	partWant  int64
	partTxID  uint64
	partLast  time.Time
	putPaused bool

	done bool
}

func newWebHost(conn *websocket.Conn, srv *lanweb.Server, log *logging.Logger, onProgress func(TransferProgress)) *webHost {
	return &webHost{
		srv:        srv,
		log:        log,
		conn:       conn,
		onProgress: onProgress,
	}
}

// ServeWebHost serves one connected guest WebSocket over the relay room.
func ServeWebHost(ctx context.Context, conn *websocket.Conn, srv *lanweb.Server, log *logging.Logger) error {
	return ServeWebHostWithProgress(ctx, conn, srv, log, nil)
}

// ServeWebHostWithProgress serves one connected guest WebSocket over the relay
// room and reports live upload/download progress via onProgress.
func ServeWebHostWithProgress(ctx context.Context, conn *websocket.Conn, srv *lanweb.Server, log *logging.Logger, onProgress func(TransferProgress)) error {
	wh := newWebHost(conn, srv, log, onProgress)
	return wh.run(ctx)
}

func (h *webHost) run(ctx context.Context) error {
	h.conn.SetReadLimit(4 << 20)
	defer func() {
		h.cancelActiveGet(ctx, false)
		h.getWg.Wait()
		h.abort()
	}()
	for {
		typ, data, err := h.conn.Read(ctx)
		if err != nil {
			return nil
		}
		switch typ {
		case websocket.MessageText:
			if err := h.control(ctx, data); err != nil {
				h.event("tempshare.web_error", map[string]any{"error": err.Error()})
				_ = h.sendJSON(ctx, map[string]any{"t": "err", "msg": err.Error()})
			}
			if h.done {
				return nil
			}
		case websocket.MessageBinary:
			if err := h.chunk(data); err != nil {
				h.event("tempshare.web_error", map[string]any{"error": err.Error()})
				_ = h.sendJSON(ctx, map[string]any{"t": "err", "msg": err.Error()})
			}
		}
	}
}

func (h *webHost) event(name string, kv map[string]any) {
	if h.log != nil {
		h.log.Event(name, kv)
	}
}

func (h *webHost) sendJSON(ctx context.Context, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return h.conn.Write(ctx, websocket.MessageText, b)
}

func (h *webHost) sendBinary(ctx context.Context, b []byte) error {
	h.writeMu.Lock()
	defer h.writeMu.Unlock()
	return h.conn.Write(ctx, websocket.MessageBinary, b)
}

func (h *webHost) control(ctx context.Context, data []byte) error {
	var msg struct {
		T    string `json:"t"`
		Name string `json:"name"`
		Size int64  `json:"size"`
	}
	if err := json.Unmarshal(data, &msg); err != nil {
		return fmt.Errorf("bad control message")
	}
	switch msg.T {
	case "list":
		return h.sendJSON(ctx, map[string]any{"t": "files", "files": h.srv.Snapshot()})
	case "get":
		return h.startGet(ctx, msg.Name)
	case "get.pause":
		h.setGetPaused(ctx, true, "guest")
		return nil
	case "get.resume":
		h.setGetPaused(ctx, false, "guest")
		return nil
	case "get.cancel":
		h.cancelActiveGet(ctx, true)
		return nil
	case "put.begin":
		return h.begin(ctx, msg.Name, msg.Size)
	case "put.pause":
		h.setPutPaused(ctx, true, false)
		return nil
	case "put.resume":
		h.setPutPaused(ctx, false, false)
		return nil
	case "put.cancel":
		h.abort()
		return h.sendJSON(ctx, map[string]any{"t": "put.cancelled"})
	case "put.end":
		return h.end(ctx)
	case "bye":
		h.done = true
		return nil
	default:
		return fmt.Errorf("unknown operation %q", msg.T)
	}
}

func (h *webHost) startGet(ctx context.Context, name string) error {
	path := h.srv.Path(name)
	if path == "" {
		return fmt.Errorf("no such file")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no such file")
	}
	st, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return err
	}

	h.getMu.Lock()
	if h.getActive {
		h.getMu.Unlock()
		_ = f.Close()
		return fmt.Errorf("download already in progress")
	}
	ctrl := transfer.NewController()
	txID := lanweb.NextProgressID()
	h.getActive = true
	h.getCtrl = ctrl
	h.getTxID = txID
	h.getName = name
	h.getDone = 0
	h.getTotal = st.Size()
	h.getWg.Add(1)
	h.getMu.Unlock()

	go h.runGet(ctx, name, f, st.Size(), txID, ctrl)
	return nil
}

func (h *webHost) runGet(ctx context.Context, name string, f *os.File, total int64, txID uint64, ctrl *transfer.Controller) {
	defer f.Close()
	defer h.getWg.Done()
	defer func() {
		h.getMu.Lock()
		if h.getCtrl == ctrl {
			h.getActive = false
			h.getCtrl = nil
			h.getTxID = 0
			h.getName = ""
			h.getDone = 0
			h.getTotal = 0
		}
		h.getMu.Unlock()
		if h.onProgress != nil {
			h.onProgress(TransferProgress{
				ID:        txID,
				Direction: "send",
				Channel:   "relay",
				FileName:  name,
				Done:      true,
			})
		}
	}()

	if err := h.sendJSON(ctx, map[string]any{"t": "get.begin", "name": name, "size": total}); err != nil {
		return
	}
	if h.onProgress != nil {
		h.onProgress(TransferProgress{
			ID:         txID,
			Direction:  "send",
			Channel:    "relay",
			Stage:      "transferring",
			FileName:   name,
			FileIndex:  1,
			FileCount:  1,
			BytesDone:  0,
			BytesTotal: total,
		})
	}

	hsh := sha256.New()
	buf := make([]byte, webChunk)
	var sent int64
	lastEmit := time.Now()
	for {
		for ctrl.IsPaused() && !ctrl.IsCancelled() {
			ch := ctrl.StateCh()
			select {
			case <-ch:
			case <-ctx.Done():
				return
			}
		}
		if ctrl.IsCancelled() {
			return
		}
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := hsh.Write(buf[:n]); werr != nil {
				_ = h.sendJSON(ctx, map[string]any{"t": "err", "msg": werr.Error()})
				return
			}
			if werr := h.sendBinary(ctx, buf[:n]); werr != nil {
				return
			}
			sent += int64(n)
			h.getMu.Lock()
			if h.getCtrl == ctrl {
				h.getDone = sent
			}
			h.getMu.Unlock()
			if h.onProgress != nil {
				now := time.Now()
				if now.Sub(lastEmit) >= 120*time.Millisecond || (total > 0 && sent >= total) {
					lastEmit = now
					h.onProgress(TransferProgress{
						ID:         txID,
						Direction:  "send",
						Channel:    "relay",
						Stage:      "transferring",
						FileName:   name,
						FileIndex:  1,
						FileCount:  1,
						BytesDone:  sent,
						BytesTotal: total,
					})
				}
			}
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			_ = h.sendJSON(ctx, map[string]any{"t": "err", "msg": err.Error()})
			return
		}
	}
	if ctrl.IsCancelled() {
		return
	}
	h.event("tempshare.web_get", map[string]any{"name": name, "bytes": sent})
	_ = h.sendJSON(ctx, map[string]any{
		"t":      "get.end",
		"name":   name,
		"size":   sent,
		"sha256": hex.EncodeToString(hsh.Sum(nil)),
	})
}

func (h *webHost) setGetPaused(ctx context.Context, paused bool, by string) bool {
	h.getMu.Lock()
	if !h.getActive || h.getCtrl == nil {
		h.getMu.Unlock()
		return false
	}
	ctrl := h.getCtrl
	txID, name, done, total := h.getTxID, h.getName, h.getDone, h.getTotal
	h.getMu.Unlock()

	if paused {
		ctrl.Pause()
		if h.onProgress != nil {
			h.onProgress(TransferProgress{
				ID:         txID,
				Direction:  "send",
				Channel:    "relay",
				Stage:      "paused",
				FileName:   name,
				FileIndex:  1,
				FileCount:  1,
				BytesDone:  done,
				BytesTotal: total,
			})
		}
		_ = h.sendJSON(ctx, map[string]any{"t": "get.paused", "by": by})
	} else {
		ctrl.Resume()
		if h.onProgress != nil {
			h.onProgress(TransferProgress{
				ID:         txID,
				Direction:  "send",
				Channel:    "relay",
				Stage:      "transferring",
				FileName:   name,
				FileIndex:  1,
				FileCount:  1,
				BytesDone:  done,
				BytesTotal: total,
			})
		}
		_ = h.sendJSON(ctx, map[string]any{"t": "get.resumed"})
	}
	return true
}

func (h *webHost) cancelActiveGet(ctx context.Context, notify bool) bool {
	h.getMu.Lock()
	if !h.getActive || h.getCtrl == nil {
		h.getMu.Unlock()
		return false
	}
	ctrl := h.getCtrl
	h.getMu.Unlock()

	ctrl.Cancel()
	if notify {
		nctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		_ = h.sendJSON(nctx, map[string]any{"t": "get.cancelled"})
	}
	return true
}

func (h *webHost) setPutPaused(ctx context.Context, paused bool, notifyGuest bool) bool {
	h.putMu.Lock()
	if h.part == nil {
		h.putMu.Unlock()
		return false
	}
	h.putPaused = paused
	txID, name, done, want := h.partTxID, h.partName, h.partSize, h.partWant
	h.putMu.Unlock()

	stage := "transferring"
	if paused {
		stage = "paused"
	}
	if h.onProgress != nil && txID != 0 {
		h.onProgress(TransferProgress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "relay",
			Stage:      stage,
			FileName:   name,
			FileIndex:  1,
			FileCount:  1,
			BytesDone:  done,
			BytesTotal: want,
		})
	}
	if notifyGuest {
		if paused {
			_ = h.sendJSON(ctx, map[string]any{"t": "put.pause"})
		} else {
			_ = h.sendJSON(ctx, map[string]any{"t": "put.resume"})
		}
	}
	return true
}

func (h *webHost) hasActive() bool {
	h.getMu.Lock()
	ga := h.getActive
	h.getMu.Unlock()
	if ga {
		return true
	}
	h.putMu.Lock()
	pa := h.part != nil
	h.putMu.Unlock()
	return pa
}

func (h *webHost) isActivePaused() bool {
	h.getMu.Lock()
	ga, ctrl := h.getActive, h.getCtrl
	h.getMu.Unlock()
	if ga && ctrl != nil {
		return ctrl.IsPaused()
	}
	h.putMu.Lock()
	pa, pp := h.part != nil, h.putPaused
	h.putMu.Unlock()
	if pa {
		return pp
	}
	return false
}

func (h *webHost) togglePauseActive() bool {
	h.getMu.Lock()
	ga, ctrl := h.getActive, h.getCtrl
	h.getMu.Unlock()
	if ga && ctrl != nil {
		return h.setGetPaused(context.Background(), !ctrl.IsPaused(), "host")
	}
	h.putMu.Lock()
	pa, pp := h.part != nil, h.putPaused
	h.putMu.Unlock()
	if pa {
		return h.setPutPaused(context.Background(), !pp, true)
	}
	return false
}

func (h *webHost) cancelActive() bool {
	if h.cancelActiveGet(context.Background(), true) {
		return true
	}
	h.putMu.Lock()
	pa := h.part != nil
	h.putMu.Unlock()
	if pa {
		h.abort()
		nctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = h.sendJSON(nctx, map[string]any{"t": "put.cancelled"})
		return true
	}
	return false
}

func (h *webHost) begin(ctx context.Context, name string, size int64) error {
	h.abort()
	if !h.srv.AllowUpload() {
		return fmt.Errorf("对方仅分享文件，未开启上传")
	}
	if size < 0 || size > lanweb.MaxUpload {
		return fmt.Errorf("file too large")
	}
	unique := h.srv.UniqueName(name)
	path := h.srv.RecvPath(unique)
	if path == "" {
		return fmt.Errorf("bad destination path")
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	txID := lanweb.NextProgressID()
	h.putMu.Lock()
	h.part, h.partName, h.partPath, h.partHash, h.partSize, h.partWant = f, unique, path, sha256.New(), 0, size
	h.partTxID = txID
	h.partLast = time.Now()
	h.putPaused = false
	h.putMu.Unlock()

	if h.onProgress != nil {
		h.onProgress(TransferProgress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "relay",
			Stage:      "transferring",
			FileName:   unique,
			FileIndex:  1,
			FileCount:  1,
			BytesDone:  0,
			BytesTotal: size,
		})
	}
	h.event("tempshare.web_put_begin", map[string]any{"name": unique, "size": size})
	return h.sendJSON(ctx, map[string]any{"t": "put.ready", "name": unique})
}

func (h *webHost) chunk(data []byte) error {
	h.putMu.Lock()
	if h.part == nil {
		h.putMu.Unlock()
		return fmt.Errorf("no upload in progress")
	}
	if h.partSize+int64(len(data)) > h.partWant {
		h.putMu.Unlock()
		return fmt.Errorf("upload exceeds declared size")
	}
	if _, err := h.partHash.Write(data); err != nil {
		h.putMu.Unlock()
		return err
	}
	if _, err := h.part.Write(data); err != nil {
		h.putMu.Unlock()
		return err
	}
	h.partSize += int64(len(data))
	h.putPaused = false
	now := time.Now()
	emit := h.onProgress != nil && (now.Sub(h.partLast) >= 120*time.Millisecond || (h.partWant > 0 && h.partSize >= h.partWant))
	var snap TransferProgress
	if emit {
		h.partLast = now
		snap = TransferProgress{
			ID:         h.partTxID,
			Direction:  "recv",
			Channel:    "relay",
			Stage:      "transferring",
			FileName:   h.partName,
			FileIndex:  1,
			FileCount:  1,
			BytesDone:  h.partSize,
			BytesTotal: h.partWant,
		}
	}
	h.putMu.Unlock()
	if emit {
		h.onProgress(snap)
	}
	return nil
}

func (h *webHost) end(ctx context.Context) error {
	h.putMu.Lock()
	if h.part == nil {
		h.putMu.Unlock()
		return fmt.Errorf("no upload in progress")
	}
	f := h.part
	partPath := h.partPath
	partName := h.partName
	partSize := h.partSize
	partWant := h.partWant
	txID := h.partTxID
	sum := hex.EncodeToString(h.partHash.Sum(nil))
	h.part = nil
	h.partTxID = 0
	h.putPaused = false
	h.putMu.Unlock()

	if h.onProgress != nil {
		h.onProgress(TransferProgress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "relay",
			Stage:      "verifying",
			FileName:   partName,
			FileIndex:  1,
			FileCount:  1,
			BytesDone:  partSize,
			BytesTotal: partWant,
		})
		defer h.onProgress(TransferProgress{
			ID:        txID,
			Direction: "recv",
			Channel:   "relay",
			FileName:  partName,
			Done:      true,
		})
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(partPath + ".part")
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(partPath + ".part")
		return err
	}
	if err := os.Rename(partPath+".part", partPath); err != nil {
		_ = os.Remove(partPath + ".part")
		return err
	}
	if partSize != partWant {
		return fmt.Errorf("size mismatch: got %d want %d", partSize, partWant)
	}
	rec := h.srv.Register(partName, partSize, sum)
	h.srv.NotifyUploaded(rec)
	h.event("tempshare.web_put_done", map[string]any{"name": partName, "bytes": partSize, "sha256": sum})
	return h.sendJSON(ctx, map[string]any{"t": "put.ok", "name": partName, "size": partSize, "sha256": sum})
}

func (h *webHost) abort() {
	h.putMu.Lock()
	if h.part == nil {
		h.putMu.Unlock()
		return
	}
	f := h.part
	partPath := h.partPath
	txID := h.partTxID
	partName := h.partName
	h.part = nil
	h.partTxID = 0
	h.putPaused = false
	h.putMu.Unlock()

	_ = f.Close()
	if partPath != "" {
		_ = os.Remove(partPath + ".part")
	}
	if txID != 0 && h.onProgress != nil {
		h.onProgress(TransferProgress{
			ID:        txID,
			Direction: "recv",
			Channel:   "relay",
			FileName:  partName,
			Done:      true,
		})
	}
}
