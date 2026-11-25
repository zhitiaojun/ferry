// Package lanweb serves a small browser UI for transferring files with
// devices that have no client installed (M5, D20).
//
// The page lives under a per-session random token so that other machines on
// the same LAN cannot stumble into the directory by guessing the address:
//
//	http://<lan-ip>:<port>/<token>/
//
// Uploads are streamed to disk and hashed on the fly; the browser never has to
// buffer a whole file. Downloads are served straight from the directory.
package lanweb

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/cwlindean/filetransfer/core/logging"
	"github.com/cwlindean/filetransfer/core/transfer"
)

// MaxUpload bounds one browser upload (single file) to keep a hostile page
// from filling the disk in one request.
const MaxUpload = 4 << 30 // 4 GiB

// TokenLen is the number of random bytes behind the page path.
const TokenLen = 16

// File describes one stored file.
type File struct {
	Name       string `json:"name"`
	Size       int64  `json:"size"`
	SHA256     string `json:"sha256"`
	ModTime    int64  `json:"mtime_ms"`
	BatchCount int    `json:"-"`
}

// Progress reports live HTTP or relay transfer progress from lanweb.
type Progress struct {
	ID         uint64
	Direction  string // "send" | "recv"
	Channel    string // "lan" | "relay"
	Stage      string // "transferring" | "paused" | "verifying"
	FileName   string
	BytesDone  int64
	BytesTotal int64
	Done       bool
}

type chunkUpload struct {
	mu         sync.Mutex
	id         string
	name       string
	tmpPath    string
	expected   int64
	written    int64
	batchCount int
	f          *os.File
	h          io.Writer
	sum        interface{ Sum([]byte) []byte }
	txID       uint64
	lastEmit   time.Time
	paused     bool
	cancelled  bool
}

var progressSeq atomic.Uint64

// NextProgressID returns a unique monotonically increasing transfer progress ID.
func NextProgressID() uint64 {
	return progressSeq.Add(1)
}

// ScopedOptions configures a mode-scoped LAN web server for temporary share
// sessions ("send" exposes only SharedFiles; "recv" accepts uploads into
// RecvDir without exposing pre-existing files; "both" exposes RecvDir).
type ScopedOptions struct {
	Mode        string
	RecvDir     string
	SharedFiles []string
	DeviceName  string
	Token       string
	Log         *logging.Logger
	OnUploaded  func(File)
	OnProgress  func(Progress)
}

// Server is the LAN web endpoint.
type Server struct {
	dir        string
	token      string
	mode       string
	deviceName string
	log        *logging.Logger
	onUploaded func(File)
	onProgress func(Progress)

	mu        sync.Mutex
	files     map[string]File   // by name
	filePaths map[string]string // exposed name -> absolute path

	chunksMu sync.Mutex
	chunks   map[string]*chunkUpload

	activeMu      sync.Mutex
	activeKind    string // "upload" | "download" | ""
	activeChunkID string
	activeCtrl    *transfer.Controller
	activePaused  atomic.Bool
}

// New creates a server rooted at dir (created if needed). A random token is
// generated unless one is supplied.
func New(dir, token string, log *logging.Logger) (*Server, error) {
	return NewScoped(ScopedOptions{
		Mode:    "both",
		RecvDir: dir,
		Token:   token,
		Log:     log,
	})
}

// NewScoped creates a mode-scoped LAN web server.
func NewScoped(opts ScopedOptions) (*Server, error) {
	mode := strings.TrimSpace(opts.Mode)
	if mode == "" {
		mode = "both"
	}
	switch mode {
	case "send", "recv", "both":
	default:
		return nil, fmt.Errorf("invalid mode %q", mode)
	}
	if mode != "send" {
		if opts.RecvDir == "" {
			return nil, fmt.Errorf("RecvDir is required for mode %q", mode)
		}
		if err := os.MkdirAll(opts.RecvDir, 0o755); err != nil {
			return nil, err
		}
	}
	token := opts.Token
	if token == "" {
		b := make([]byte, TokenLen)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		token = hex.EncodeToString(b)
	}
	s := &Server{
		dir:        opts.RecvDir,
		token:      token,
		mode:       mode,
		deviceName: opts.DeviceName,
		log:        opts.Log,
		onUploaded: opts.OnUploaded,
		onProgress: opts.OnProgress,
		files:      map[string]File{},
		filePaths:  map[string]string{},
	}
	switch mode {
	case "send":
		for _, p := range opts.SharedFiles {
			abs, err := filepath.Abs(p)
			if err != nil {
				continue
			}
			info, err := os.Stat(abs)
			if err != nil || !info.Mode().IsRegular() {
				continue
			}
			sum, err := hashFile(abs)
			if err != nil {
				continue
			}
			name := s.UniqueName(filepath.Base(abs))
			s.files[name] = File{
				Name:    name,
				Size:    info.Size(),
				SHA256:  sum,
				ModTime: info.ModTime().UnixMilli(),
			}
			s.filePaths[name] = abs
		}
	case "recv":
		// Start with an empty listing so pre-existing files in RecvDir are not exposed.
	case "both":
		if err := s.scan(); err != nil {
			return nil, err
		}
	}
	return s, nil
}

// Mode returns "send", "recv", or "both".
func (s *Server) Mode() string { return s.mode }

// AllowUpload reports whether this session accepts browser/peer uploads.
func (s *Server) AllowUpload() bool { return s.mode != "send" }

// NotifyUploaded triggers the OnUploaded callback when a file is stored via relay.
func (s *Server) NotifyUploaded(f File) {
	if s.onUploaded != nil {
		s.onUploaded(f)
	}
}

// NotifyProgress emits a live transfer progress snapshot if OnProgress is set.
func (s *Server) NotifyProgress(p Progress) {
	if s.onProgress != nil {
		s.onProgress(p)
	}
}

// Token returns the URL path component guarding the page.
func (s *Server) Token() string { return s.token }

// Dir returns the directory served by the page.
func (s *Server) Dir() string { return s.dir }

// URL renders the page address for a host (no scheme).
func (s *Server) URL(host string) string {
	if host == "" {
		host = "127.0.0.1"
	}
	return "http://" + host + "/" + s.token + "/"
}

// ListenAndServe listens on addr and serves until the process exits.
func (s *Server) ListenAndServe(addr string, ln net.Listener) error {
	srv := &http.Server{Handler: s, ReadHeaderTimeout: 10 * time.Second}
	if ln == nil {
		var err error
		ln, err = net.Listen("tcp", addr)
		if err != nil {
			return err
		}
	}
	if s.log != nil {
		s.log.Event("lanweb.listen", map[string]any{"addr": ln.Addr().String(), "dir": s.dir})
	}
	return srv.Serve(ln)
}

func (s *Server) scan() error {
	ents, err := os.ReadDir(s.dir)
	if err != nil {
		return err
	}
	for _, e := range ents {
		if e.IsDir() || !e.Type().IsRegular() {
			continue
		}
		info, err := e.Info()
		if err != nil {
			continue
		}
		sum, err := hashFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		s.files[e.Name()] = File{Name: e.Name(), Size: info.Size(), SHA256: sum, ModTime: info.ModTime().UnixMilli()}
	}
	return nil
}

func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// ServeHTTP routes the browser API under the token prefix.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	prefix := "/" + s.token
	if !strings.HasPrefix(r.URL.Path, prefix) {
		http.NotFound(w, r)
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, prefix)
	// The public web UI calls this server cross-origin after resolving the
	// device through ft.cwlindean.cn, so list/upload/download need CORS.
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-File-Size")
	w.Header().Set("Access-Control-Max-Age", "600")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	if rest == "" || rest == "/" {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, pageHTML)
		return
	}
	switch {
	case rest == "/api/list":
		s.handleList(w, r)
	case rest == "/api/upload/init":
		s.handleUploadInit(w, r)
	case rest == "/api/upload/chunk":
		s.handleUploadChunk(w, r)
	case rest == "/api/upload/finish":
		s.handleUploadFinish(w, r)
	case rest == "/api/upload/cancel":
		s.handleUploadCancel(w, r)
	case rest == "/api/upload/status":
		s.handleUploadStatus(w, r)
	case rest == "/upload":
		s.handleUpload(w, r)
	case strings.HasPrefix(rest, "/f/"):
		s.handleDownload(w, r, strings.TrimPrefix(rest, "/f/"))
	default:
		http.NotFound(w, r)
	}
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	out := s.Snapshot()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"mode":         s.mode,
		"name":         s.deviceName,
		"allow_upload": s.AllowUpload(),
		"files":        out,
	})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.AllowUpload() {
		http.Error(w, "upload disabled in send-only mode", http.StatusForbidden)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	var saved []File
	for partIdx := 0; ; partIdx++ {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			http.Error(w, "read part: "+err.Error(), http.StatusBadRequest)
			return
		}
		name := cleanName(part.FileName())
		if name == "" {
			_ = part.Close()
			continue
		}
		var expectedSize int64
		if raw := strings.TrimSpace(part.Header.Get("X-File-Size")); raw != "" {
			if n, perr := strconv.ParseInt(raw, 10, 64); perr == nil && n > 0 {
				expectedSize = n
			}
		} else if partIdx == 0 {
			if raw := strings.TrimSpace(r.Header.Get("X-File-Size")); raw != "" {
				if n, perr := strconv.ParseInt(raw, 10, 64); perr == nil && n > 0 {
					expectedSize = n
				}
			}
		}
		f, err := s.save(name, part, expectedSize)
		_ = part.Close()
		if err != nil {
			http.Error(w, "save: "+err.Error(), http.StatusInternalServerError)
			return
		}
		saved = append(saved, f)
	}
	s.mu.Lock()
	for _, f := range saved {
		s.files[f.Name] = f
	}
	s.mu.Unlock()
	for _, f := range saved {
		f.BatchCount = len(saved)
		s.NotifyUploaded(f)
	}
	if s.log != nil {
		for _, f := range saved {
			s.log.Event("lanweb.upload", map[string]any{"file": f.Name, "size": f.Size, "sha256": f.SHA256})
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"files": saved})
}

// save streams the upload to a temporary file, hashes it, then renames it into
// place. A name collision gets a numeric suffix instead of overwriting.
func (s *Server) save(name string, r io.Reader, expectedSize int64) (File, error) {
	s.mu.Lock()
	final := name
	for i := 2; ; i++ {
		if _, taken := s.files[final]; !taken {
			if _, err := os.Stat(filepath.Join(s.dir, final)); os.IsNotExist(err) {
				break
			}
		}
		ext := filepath.Ext(name)
		final = fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, ext), i, ext)
	}
	s.mu.Unlock()

	var txID uint64
	if s.onProgress != nil {
		txID = NextProgressID()
		s.NotifyProgress(Progress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "lan",
			Stage:      "transferring",
			FileName:   final,
			BytesDone:  0,
			BytesTotal: expectedSize,
		})
		defer s.NotifyProgress(Progress{
			ID:        txID,
			Direction: "recv",
			Channel:   "lan",
			FileName:  final,
			Done:      true,
		})
	}

	tmp := filepath.Join(s.dir, "."+final+".part")
	out, err := os.Create(tmp)
	if err != nil {
		return File{}, err
	}
	pr := &progressReader{
		r:        r,
		txID:     txID,
		fileName: final,
		total:    expectedSize,
		notify:   s.onProgress,
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), pr)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return File{}, err
	}
	if s.onProgress != nil {
		total := expectedSize
		if n > total {
			total = n
		}
		s.NotifyProgress(Progress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "lan",
			Stage:      "verifying",
			FileName:   final,
			BytesDone:  n,
			BytesTotal: total,
		})
	}
	if err := os.Rename(tmp, filepath.Join(s.dir, final)); err != nil {
		_ = os.Remove(tmp)
		return File{}, err
	}
	info, _ := os.Stat(filepath.Join(s.dir, final))
	var mtime int64
	if info != nil {
		mtime = info.ModTime().UnixMilli()
	}
	return File{Name: final, Size: n, SHA256: hex.EncodeToString(h.Sum(nil)), ModTime: mtime}, nil
}

func (s *Server) handleDownload(w http.ResponseWriter, r *http.Request, name string) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	p := s.Path(name)
	if p == "" {
		http.NotFound(w, r)
		return
	}
	clean := cleanName(name)
	f, err := os.Open(p)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", fmt.Sprint(info.Size()))
	w.Header().Set("Content-Disposition", "attachment; filename=\""+clean+"\"")

	ctrl := transfer.NewController()
	s.setActiveDownload(ctrl)
	defer s.clearActiveDownload(ctrl)

	dst := w
	if r.Method == http.MethodGet {
		var txID uint64
		if s.onProgress != nil {
			txID = NextProgressID()
			total := info.Size()
			s.NotifyProgress(Progress{
				ID:         txID,
				Direction:  "send",
				Channel:    "lan",
				Stage:      "transferring",
				FileName:   clean,
				BytesDone:  0,
				BytesTotal: total,
			})
			defer s.NotifyProgress(Progress{
				ID:        txID,
				Direction: "send",
				Channel:   "lan",
				FileName:  clean,
				Done:      true,
			})
		}
		dst = &progressResponseWriter{
			ResponseWriter: w,
			txID:           txID,
			fileName:       clean,
			total:          info.Size(),
			ctrl:           ctrl,
			reqCtx:         r.Context(),
			notify:         s.onProgress,
		}
	}
	http.ServeContent(dst, r, clean, info.ModTime(), f)
}

func (s *Server) setActiveDownload(ctrl *transfer.Controller) {
	s.activeMu.Lock()
	s.activeKind = "download"
	s.activeChunkID = ""
	s.activeCtrl = ctrl
	s.activePaused.Store(false)
	s.activeMu.Unlock()
}

func (s *Server) clearActiveDownload(ctrl *transfer.Controller) {
	s.activeMu.Lock()
	if s.activeKind == "download" && s.activeCtrl == ctrl {
		s.activeKind = ""
		s.activeCtrl = nil
		s.activePaused.Store(false)
	}
	s.activeMu.Unlock()
}

func (s *Server) setActiveUpload(id string) {
	s.activeMu.Lock()
	s.activeKind = "upload"
	s.activeChunkID = id
	s.activeCtrl = nil
	s.activePaused.Store(false)
	s.activeMu.Unlock()
}

func (s *Server) clearActiveUpload(id string) {
	s.activeMu.Lock()
	if s.activeKind == "upload" && s.activeChunkID == id {
		s.activeKind = ""
		s.activeChunkID = ""
		s.activePaused.Store(false)
	}
	s.activeMu.Unlock()
}

// ActivePaused reports whether the current HTTP upload or download is paused.
func (s *Server) ActivePaused() bool {
	if s == nil {
		return false
	}
	return s.activePaused.Load()
}

// TogglePauseActive pauses or resumes the active HTTP upload or download.
func (s *Server) TogglePauseActive() bool {
	if s == nil {
		return false
	}
	s.activeMu.Lock()
	kind := s.activeKind
	ctrl := s.activeCtrl
	chunkID := s.activeChunkID
	s.activeMu.Unlock()

	if kind == "download" && ctrl != nil {
		paused := ctrl.TogglePause()
		s.activePaused.Store(paused)
		return true
	}
	if kind == "upload" && chunkID != "" {
		cu := s.getChunk(chunkID)
		if cu == nil {
			return false
		}
		cu.mu.Lock()
		if cu.cancelled {
			cu.mu.Unlock()
			return false
		}
		cu.paused = !cu.paused
		paused := cu.paused
		stage := "transferring"
		if paused {
			stage = "paused"
		}
		txID, name, done, total := cu.txID, cu.name, cu.written, cu.expected
		cu.mu.Unlock()
		s.activePaused.Store(paused)
		s.NotifyProgress(Progress{
			ID:         txID,
			Direction:  "recv",
			Channel:    "lan",
			Stage:      stage,
			FileName:   name,
			BytesDone:  done,
			BytesTotal: total,
		})
		return true
	}
	return false
}

// CancelActive cancels the active HTTP upload or download.
func (s *Server) CancelActive() bool {
	if s == nil {
		return false
	}
	s.activeMu.Lock()
	kind := s.activeKind
	ctrl := s.activeCtrl
	chunkID := s.activeChunkID
	s.activeMu.Unlock()

	if kind == "download" && ctrl != nil {
		ctrl.Cancel()
		return true
	}
	if kind == "upload" && chunkID != "" {
		return s.cancelChunk(chunkID)
	}
	return false
}

func (s *Server) getChunk(id string) *chunkUpload {
	s.chunksMu.Lock()
	defer s.chunksMu.Unlock()
	if s.chunks == nil {
		return nil
	}
	return s.chunks[id]
}

func (s *Server) cancelChunk(id string) bool {
	s.chunksMu.Lock()
	cu := s.chunks[id]
	if cu != nil {
		delete(s.chunks, id)
	}
	s.chunksMu.Unlock()
	if cu == nil {
		return false
	}
	cu.mu.Lock()
	cu.cancelled = true
	if cu.f != nil {
		_ = cu.f.Close()
		cu.f = nil
	}
	if cu.tmpPath != "" {
		_ = os.Remove(cu.tmpPath)
	}
	txID, name := cu.txID, cu.name
	cu.mu.Unlock()
	s.clearActiveUpload(id)
	s.NotifyProgress(Progress{
		ID:        txID,
		Direction: "recv",
		Channel:   "lan",
		FileName:  name,
		Done:      true,
	})
	return true
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleUploadInit(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !s.AllowUpload() {
		http.Error(w, "upload disabled in send-only mode", http.StatusForbidden)
		return
	}
	var req struct {
		Name       string `json:"name"`
		Size       int64  `json:"size"`
		BatchCount int    `json:"batch_count"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16<<10)).Decode(&req); err != nil {
		http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
		return
	}
	name := cleanName(req.Name)
	if name == "" {
		http.Error(w, "invalid file name", http.StatusBadRequest)
		return
	}
	if req.Size < 0 || req.Size > MaxUpload {
		http.Error(w, "size out of range", http.StatusBadRequest)
		return
	}
	final := s.UniqueName(name)
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		http.Error(w, "id generation failed", http.StatusInternalServerError)
		return
	}
	id := hex.EncodeToString(raw[:])
	tmpPath := filepath.Join(s.dir, "."+id+".part")
	out, err := os.Create(tmpPath)
	if err != nil {
		http.Error(w, "create temp file: "+err.Error(), http.StatusInternalServerError)
		return
	}
	h := sha256.New()
	txID := NextProgressID()
	cu := &chunkUpload{
		id:         id,
		name:       final,
		tmpPath:    tmpPath,
		expected:   req.Size,
		batchCount: req.BatchCount,
		f:          out,
		h:          io.MultiWriter(out, h),
		sum:        h,
		txID:       txID,
	}
	s.chunksMu.Lock()
	if s.chunks == nil {
		s.chunks = map[string]*chunkUpload{}
	}
	s.chunks[id] = cu
	s.chunksMu.Unlock()
	s.setActiveUpload(id)
	s.NotifyProgress(Progress{
		ID:         txID,
		Direction:  "recv",
		Channel:    "lan",
		Stage:      "transferring",
		FileName:   final,
		BytesDone:  0,
		BytesTotal: req.Size,
	})
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "name": final})
}

func (s *Server) handleUploadChunk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	cu := s.getChunk(id)
	if cu == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "upload session not found", "cancelled": true})
		return
	}
	off, perr := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("offset")), 10, 64)
	if perr != nil || off < 0 {
		http.Error(w, "bad offset", http.StatusBadRequest)
		return
	}

	cu.mu.Lock()
	defer cu.mu.Unlock()
	if cu.cancelled {
		writeJSON(w, http.StatusConflict, map[string]any{"cancelled": true, "error": "cancelled"})
		return
	}
	if cu.paused {
		writeJSON(w, http.StatusConflict, map[string]any{"paused": true, "written": cu.written})
		return
	}
	if off != cu.written {
		http.Error(w, fmt.Sprintf("offset mismatch: got %d want %d", off, cu.written), http.StatusConflict)
		return
	}
	const maxChunkBody = 8 << 20
	limited := io.LimitReader(http.MaxBytesReader(w, r.Body, maxChunkBody), maxChunkBody+1)
	n, err := io.Copy(cu.h, limited)
	if err != nil {
		http.Error(w, "write chunk: "+err.Error(), http.StatusInternalServerError)
		return
	}
	if n > maxChunkBody {
		http.Error(w, "chunk too large", http.StatusRequestEntityTooLarge)
		return
	}
	if cu.written+n > cu.expected {
		http.Error(w, "upload exceeds declared size", http.StatusBadRequest)
		return
	}
	cu.written += n
	now := time.Now()
	if now.Sub(cu.lastEmit) >= 120*time.Millisecond || (cu.expected > 0 && cu.written >= cu.expected) {
		cu.lastEmit = now
		s.NotifyProgress(Progress{
			ID:         cu.txID,
			Direction:  "recv",
			Channel:    "lan",
			Stage:      "transferring",
			FileName:   cu.name,
			BytesDone:  cu.written,
			BytesTotal: cu.expected,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"written": cu.written})
}

func (s *Server) handleUploadStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	cu := s.getChunk(id)
	if cu == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"cancelled": true})
		return
	}
	cu.mu.Lock()
	paused, cancelled, written := cu.paused, cu.cancelled, cu.written
	cu.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"paused": paused, "cancelled": cancelled, "written": written})
}

func (s *Server) handleUploadFinish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	s.chunksMu.Lock()
	cu := s.chunks[id]
	if cu != nil {
		delete(s.chunks, id)
	}
	s.chunksMu.Unlock()
	if cu == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"cancelled": true, "error": "upload session not found"})
		return
	}
	cu.mu.Lock()
	defer cu.mu.Unlock()
	s.clearActiveUpload(id)
	if cu.cancelled {
		writeJSON(w, http.StatusConflict, map[string]any{"cancelled": true, "error": "cancelled"})
		return
	}
	s.NotifyProgress(Progress{
		ID:         cu.txID,
		Direction:  "recv",
		Channel:    "lan",
		Stage:      "verifying",
		FileName:   cu.name,
		BytesDone:  cu.written,
		BytesTotal: cu.expected,
	})
	defer s.NotifyProgress(Progress{
		ID:        cu.txID,
		Direction: "recv",
		Channel:   "lan",
		FileName:  cu.name,
		Done:      true,
	})
	if cu.f != nil {
		if err := cu.f.Sync(); err != nil {
			_ = cu.f.Close()
			cu.f = nil
			_ = os.Remove(cu.tmpPath)
			http.Error(w, "sync: "+err.Error(), http.StatusInternalServerError)
			return
		}
		if err := cu.f.Close(); err != nil {
			cu.f = nil
			_ = os.Remove(cu.tmpPath)
			http.Error(w, "close: "+err.Error(), http.StatusInternalServerError)
			return
		}
		cu.f = nil
	}
	if cu.written != cu.expected {
		_ = os.Remove(cu.tmpPath)
		http.Error(w, fmt.Sprintf("size mismatch: got %d want %d", cu.written, cu.expected), http.StatusBadRequest)
		return
	}
	final := s.UniqueName(cu.name)
	finalPath := filepath.Join(s.dir, final)
	if err := os.Rename(cu.tmpPath, finalPath); err != nil {
		_ = os.Remove(cu.tmpPath)
		http.Error(w, "rename: "+err.Error(), http.StatusInternalServerError)
		return
	}
	info, _ := os.Stat(finalPath)
	var mtime int64
	if info != nil {
		mtime = info.ModTime().UnixMilli()
	}
	rec := File{
		Name:       final,
		Size:       cu.written,
		SHA256:     hex.EncodeToString(cu.sum.Sum(nil)),
		ModTime:    mtime,
		BatchCount: cu.batchCount,
	}
	s.mu.Lock()
	s.files[rec.Name] = rec
	s.mu.Unlock()
	s.NotifyUploaded(rec)
	if s.log != nil {
		s.log.Event("lanweb.upload", map[string]any{"file": rec.Name, "size": rec.Size, "sha256": rec.SHA256})
	}
	writeJSON(w, http.StatusOK, map[string]any{"file": rec})
}

func (s *Server) handleUploadCancel(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	s.cancelChunk(id)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

type progressReader struct {
	r        io.Reader
	txID     uint64
	fileName string
	total    int64
	done     int64
	lastEmit time.Time
	notify   func(Progress)
}

func (p *progressReader) Read(b []byte) (int, error) {
	n, err := p.r.Read(b)
	if n > 0 && p.notify != nil {
		p.done += int64(n)
		now := time.Now()
		if now.Sub(p.lastEmit) >= 120*time.Millisecond {
			p.lastEmit = now
			total := p.total
			if total > 0 && p.done > total {
				total = p.done
			}
			p.notify(Progress{
				ID:         p.txID,
				Direction:  "recv",
				Channel:    "lan",
				Stage:      "transferring",
				FileName:   p.fileName,
				BytesDone:  p.done,
				BytesTotal: total,
			})
		}
	}
	return n, err
}

// progressResponseWriter wraps http.ResponseWriter intentionally without
// io.ReaderFrom so http.ServeContent writes chunks through Write([]byte).
type progressResponseWriter struct {
	http.ResponseWriter
	txID     uint64
	fileName string
	total    int64
	done     int64
	lastEmit time.Time
	ctrl     *transfer.Controller
	reqCtx   context.Context
	notify   func(Progress)
}

func (p *progressResponseWriter) Write(b []byte) (int, error) {
	if p.ctrl != nil {
		wasPaused := false
		for p.ctrl.IsPaused() {
			if p.ctrl.IsCancelled() {
				return 0, transfer.ErrCancelled
			}
			if !wasPaused {
				wasPaused = true
				if p.notify != nil && p.txID != 0 {
					p.notify(Progress{
						ID:         p.txID,
						Direction:  "send",
						Channel:    "lan",
						Stage:      "paused",
						FileName:   p.fileName,
						BytesDone:  p.done,
						BytesTotal: p.total,
					})
				}
			}
			ch := p.ctrl.StateCh()
			select {
			case <-ch:
			case <-p.reqCtx.Done():
				return 0, p.reqCtx.Err()
			}
		}
		if wasPaused && p.notify != nil && p.txID != 0 {
			p.notify(Progress{
				ID:         p.txID,
				Direction:  "send",
				Channel:    "lan",
				Stage:      "transferring",
				FileName:   p.fileName,
				BytesDone:  p.done,
				BytesTotal: p.total,
			})
		}
		if p.ctrl.IsCancelled() {
			return 0, transfer.ErrCancelled
		}
	}
	if cl := strings.TrimSpace(p.ResponseWriter.Header().Get("Content-Length")); cl != "" {
		if n, err := strconv.ParseInt(cl, 10, 64); err == nil && n > 0 {
			p.total = n
		}
	}
	n, err := p.ResponseWriter.Write(b)
	if n > 0 && p.notify != nil && p.txID != 0 {
		p.done += int64(n)
		now := time.Now()
		if now.Sub(p.lastEmit) >= 120*time.Millisecond || (p.total > 0 && p.done >= p.total) {
			p.lastEmit = now
			p.notify(Progress{
				ID:         p.txID,
				Direction:  "send",
				Channel:    "lan",
				Stage:      "transferring",
				FileName:   p.fileName,
				BytesDone:  p.done,
				BytesTotal: p.total,
			})
		}
	}
	return n, err
}

// cleanName reduces a browser-supplied name to a safe base name.
func cleanName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "" || name == "." || name == ".." {
		return ""
	}
	if strings.ContainsAny(name, "\x00/") {
		return ""
	}
	return name
}

// Snapshot returns the current file index, sorted by name. The relay host
// serves the same listing to browsers that cannot reach the LAN address.
func (s *Server) Snapshot() []File {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]File, 0, len(s.files))
	for _, f := range s.files {
		out = append(out, f)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Path resolves a browser-supplied name to a path inside the allowed scope. It
// returns "" when the name is unsafe or outside the session scope.
func (s *Server) Path(name string) string {
	name = cleanName(name)
	if name == "" {
		return ""
	}
	s.mu.Lock()
	if p, ok := s.filePaths[name]; ok {
		s.mu.Unlock()
		return p
	}
	_, inFiles := s.files[name]
	s.mu.Unlock()
	if s.mode == "send" {
		return ""
	}
	if s.mode == "recv" && !inFiles {
		return ""
	}
	return filepath.Join(s.dir, name)
}

// RecvPath resolves a safe destination path inside RecvDir for an incoming
// upload when uploads are permitted; returns "" in send-only mode or for
// unsafe names.
func (s *Server) RecvPath(name string) string {
	if !s.AllowUpload() || s.dir == "" {
		return ""
	}
	name = cleanName(name)
	if name == "" {
		return ""
	}
	return filepath.Join(s.dir, name)
}

// UniqueName returns name, or name-2, name-3... while the directory holds a
// file under that name.
func (s *Server) UniqueName(name string) string {
	base := cleanName(name)
	if base == "" {
		base = "file"
	}
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := base
	for i := 2; ; i++ {
		if _, ok := s.files[candidate]; !ok {
			return candidate
		}
		candidate = fmt.Sprintf("%s-%d%s", stem, i, ext)
	}
}

// Register indexes a file written through another channel (the relay host).
func (s *Server) Register(name string, size int64, sum string) File {
	f := File{Name: name, Size: size, SHA256: sum, ModTime: time.Now().UnixMilli()}
	s.mu.Lock()
	s.files[name] = f
	s.mu.Unlock()
	return f
}
