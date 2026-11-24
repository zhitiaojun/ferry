package ipc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Server exposes local loopback HTTP control routes guarded by a per-process
// bearer token written to <stateDir>/daemon.json (0600).
type Server struct {
	infoPath string
	token    string
	ln       net.Listener
	httpSrv  *http.Server
	handler  Handler

	closeOnce sync.Once
}

// StartOptions configures metadata persisted alongside the daemon discovery file.
type StartOptions struct {
	KeyPath    string
	PairedPath string
}

// Start binds 127.0.0.1:0, writes <stateDir>/daemon.json, and begins serving.
func Start(stateDir string, handler Handler, opts StartOptions) (*Server, error) {
	if stateDir == "" {
		return nil, errors.New("ipc: empty state directory")
	}
	if handler == nil {
		return nil, errors.New("ipc: nil handler")
	}
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return nil, fmt.Errorf("ipc: create state dir: %w", err)
	}
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return nil, fmt.Errorf("ipc: generate token: %w", err)
	}
	token := hex.EncodeToString(raw[:])

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("ipc: listen loopback: %w", err)
	}

	s := &Server{
		infoPath: filepath.Join(stateDir, discoveryFileName),
		token:    token,
		ln:       ln,
		handler:  handler,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/v1/status", s.withAuth(s.handleStatus))
	mux.HandleFunc("/v1/peers", s.withAuth(s.handlePeers))
	mux.HandleFunc("/v1/send", s.withAuth(s.handleSend))
	mux.HandleFunc("/v1/send-text", s.withAuth(s.handleSendText))
	mux.HandleFunc("/v1/open", s.withAuth(s.handleOpen))

	s.httpSrv = &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	info := DaemonInfo{
		Addr:       ln.Addr().String(),
		Token:      token,
		PID:        os.Getpid(),
		KeyPath:    opts.KeyPath,
		PairedPath: opts.PairedPath,
	}
	data, err := json.MarshalIndent(info, "", "  ")
	if err != nil {
		_ = ln.Close()
		return nil, err
	}
	if err := writePrivateFile(s.infoPath, data); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("ipc: write %s: %w", s.infoPath, err)
	}

	go func() {
		_ = s.httpSrv.Serve(ln)
	}()
	return s, nil
}

// Addr returns the bound loopback address.
func (s *Server) Addr() string {
	if s == nil || s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// Close shuts down the HTTP server and deletes daemon.json if it still belongs
// to this server instance.
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var retErr error
	s.closeOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		retErr = s.httpSrv.Shutdown(ctx)
		if data, err := os.ReadFile(s.infoPath); err == nil {
			var cur DaemonInfo
			if json.Unmarshal(data, &cur) == nil && cur.Token == s.token {
				_ = os.Remove(s.infoPath)
			}
		}
	})
	return retErr
}

func (s *Server) withAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		got := auth[len(prefix):]
		if len(got) != len(s.token) || subtle.ConstantTimeCompare([]byte(got), []byte(s.token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, http.StatusOK, s.handler.Status(r.Context()))
}

func (s *Server) handlePeers(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	res, err := s.handler.Peers(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) handleSend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SendRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, SendResponse{OK: false, Error: "invalid request: " + err.Error()})
		return
	}
	res := s.handler.Send(r.Context(), req)
	status := http.StatusOK
	if !res.OK {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, res)
}

func (s *Server) handleSendText(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req SendTextRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, SendResponse{OK: false, Error: "invalid request: " + err.Error()})
		return
	}
	res := s.handler.SendText(r.Context(), req)
	status := http.StatusOK
	if !res.OK {
		status = http.StatusUnprocessableEntity
	}
	writeJSON(w, status, res)
}

func (s *Server) handleOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := s.handler.OpenUI(r.Context()); err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

func writePrivateFile(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".daemon-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	return os.Rename(tmpName, path)
}
