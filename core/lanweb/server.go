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
	"strings"
	"sync"
	"time"

	"github.com/cwlindean/filetransfer/core/logging"
)

// MaxUpload bounds one browser upload (single file) to keep a hostile page
// from filling the disk in one request.
const MaxUpload = 4 << 30 // 4 GiB

// TokenLen is the number of random bytes behind the page path.
const TokenLen = 16

// File describes one stored file.
type File struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	SHA256  string `json:"sha256"`
	ModTime int64  `json:"mtime_ms"`
}

// Server is the LAN web endpoint.
type Server struct {
	dir   string
	token string
	log   *logging.Logger

	mu    sync.Mutex
	files map[string]File // by name
}

// New creates a server rooted at dir (created if needed). A random token is
// generated unless one is supplied.
func New(dir, token string, log *logging.Logger) (*Server, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	if token == "" {
		b := make([]byte, TokenLen)
		if _, err := rand.Read(b); err != nil {
			return nil, err
		}
		token = hex.EncodeToString(b)
	}
	s := &Server{dir: dir, token: token, log: log, files: map[string]File{}}
	if err := s.scan(); err != nil {
		return nil, err
	}
	return s, nil
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
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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
	s.mu.Lock()
	out := make([]File, 0, len(s.files))
	for _, f := range s.files {
		out = append(out, f)
	}
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{"files": out})
}

func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, MaxUpload)
	mr, err := r.MultipartReader()
	if err != nil {
		http.Error(w, "bad multipart: "+err.Error(), http.StatusBadRequest)
		return
	}
	var saved []File
	for {
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
		f, err := s.save(name, part)
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
func (s *Server) save(name string, r io.Reader) (File, error) {
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

	tmp := filepath.Join(s.dir, "."+final+".part")
	out, err := os.Create(tmp)
	if err != nil {
		return File{}, err
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), r)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = os.Remove(tmp)
		return File{}, err
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
	name = cleanName(name)
	if name == "" {
		http.NotFound(w, r)
		return
	}
	f, err := os.Open(filepath.Join(s.dir, name))
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
	w.Header().Set("Content-Disposition", "attachment; filename=\""+name+"\"")
	http.ServeContent(w, r, name, info.ModTime(), f)
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
