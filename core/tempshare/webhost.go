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

	"github.com/coder/websocket"

	"github.com/cwlindean/filetransfer/core/lanweb"
	"github.com/cwlindean/filetransfer/core/logging"
)

const webChunk = 64 << 10

type webHost struct {
	srv  *lanweb.Server
	log  *logging.Logger
	conn *websocket.Conn

	part     *os.File
	partName string
	partPath string
	partHash hash.Hash
	partSize int64
	partWant int64
	done     bool
}

// ServeWebHost serves one connected guest WebSocket over the relay room.
func ServeWebHost(ctx context.Context, conn *websocket.Conn, srv *lanweb.Server, log *logging.Logger) error {
	conn.SetReadLimit(4 << 20)
	h := &webHost{srv: srv, log: log, conn: conn}
	defer h.abort()
	for {
		typ, data, err := conn.Read(ctx)
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
	return h.conn.Write(ctx, websocket.MessageText, b)
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
		return h.get(ctx, msg.Name)
	case "put.begin":
		return h.begin(ctx, msg.Name, msg.Size)
	case "put.end":
		return h.end(ctx)
	case "bye":
		h.done = true
		return nil
	default:
		return fmt.Errorf("unknown operation %q", msg.T)
	}
}

func (h *webHost) get(ctx context.Context, name string) error {
	path := h.srv.Path(name)
	if path == "" {
		return fmt.Errorf("no such file")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("no such file")
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return err
	}
	if err := h.sendJSON(ctx, map[string]any{"t": "get.begin", "name": name, "size": st.Size()}); err != nil {
		return err
	}
	hsh := sha256.New()
	buf := make([]byte, webChunk)
	var sent int64
	for {
		n, err := f.Read(buf)
		if n > 0 {
			if _, werr := hsh.Write(buf[:n]); werr != nil {
				return werr
			}
			if werr := h.conn.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
				return werr
			}
			sent += int64(n)
		}
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}
	h.event("tempshare.web_get", map[string]any{"name": name, "bytes": sent})
	return h.sendJSON(ctx, map[string]any{"t": "get.end", "name": name, "size": sent,
		"sha256": hex.EncodeToString(hsh.Sum(nil))})
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
	h.part, h.partName, h.partPath, h.partHash, h.partSize, h.partWant = f, unique, path, sha256.New(), 0, size
	h.event("tempshare.web_put_begin", map[string]any{"name": unique, "size": size})
	return h.sendJSON(ctx, map[string]any{"t": "put.ready", "name": unique})
}

func (h *webHost) chunk(data []byte) error {
	if h.part == nil {
		return fmt.Errorf("no upload in progress")
	}
	if h.partSize+int64(len(data)) > h.partWant {
		return fmt.Errorf("upload exceeds declared size")
	}
	if _, err := h.partHash.Write(data); err != nil {
		return err
	}
	if _, err := h.part.Write(data); err != nil {
		return err
	}
	h.partSize += int64(len(data))
	return nil
}

func (h *webHost) end(ctx context.Context) error {
	if h.part == nil {
		return fmt.Errorf("no upload in progress")
	}
	f := h.part
	partPath := h.partPath
	sum := hex.EncodeToString(h.partHash.Sum(nil))
	h.part = nil
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
	if h.partSize != h.partWant {
		return fmt.Errorf("size mismatch: got %d want %d", h.partSize, h.partWant)
	}
	rec := h.srv.Register(h.partName, h.partSize, sum)
	h.srv.NotifyUploaded(rec)
	h.event("tempshare.web_put_done", map[string]any{"name": h.partName, "bytes": h.partSize, "sha256": sum})
	name, size := h.partName, h.partSize
	return h.sendJSON(ctx, map[string]any{"t": "put.ok", "name": name, "size": size, "sha256": sum})
}

func (h *webHost) abort() {
	if h.part == nil {
		return
	}
	partPath := h.partPath
	_ = h.part.Close()
	if partPath != "" {
		_ = os.Remove(partPath + ".part")
	}
	h.part = nil
}
